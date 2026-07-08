package structure

// jigsaw_placement.go — STRUCT-05 part 2: the bounded-BFS JigsawPlacement.Placer (the HARD
// logic of STRUCT-05). It reads a piece's jigsaw blocks, resolves target pools, finds
// aligning templates, VoxelShape-collision-checks against the accumulated placed boxes, and
// grows a village BREADTH-FIRST via a SequencedPriorityIterator work queue (NOT stack
// recursion, so the piece-selection RNG draws stay in lockstep with vanilla — Pitfall #6).
//
// THE THREE BOUNDS ARE LOAD-BEARING (Pitfall #6) — drop any ONE and the village grows
// forever or overlaps itself:
//   1. maxDepth (the structure JSON `size`, 6 for villages): a piece at depth==maxDepth
//      attaches ONLY fallback/terminator elements.
//   2. max_distance_from_center (80): the free VoxelShape is the 80-radius box around the
//      start center; a child whose box leaves it is rejected by the collision test.
//   3. VoxelShape collision: a child overlapping an already-placed box is rejected
//      (Shapes.joinIsNotEmpty over the accumulated placed boxes, the rigid projection).
// A defensive total-piece cap (1000, the stronghold precedent) backstops a malformed pool.
//
// Ported (idiomatic Go, no GPL paste) from CFR (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.structure.pools.JigsawPlacement.addPieces
//     (public): Rotation.getRandom(rng), pool.getRandomTemplate(rng) -> EmptyPoolElement ->
//      empty; build the root PoolElementStructurePiece; center = ((maxX+minX)/2,(maxZ+minZ)/2)
//   - JigsawPlacement.addPieces (private): build the 80-radius free VoxelShape (the AABB of
//      [cx-80..cx+80+1] x [minY..maxY+1] x [cz-80..cz+80+1] minus the root box), then
//      new Placer(...); tryPlacingChildren(root, ...); while(placing.hasNext()) tryPlacingChildren
//   - JigsawPlacement$Placer.tryPlacingChildren: the per-jigsaw-block expansion (the heart)
//   - JigsawPlacement$Placer (the SequencedPriorityIterator `placing` work queue)
//   - net.minecraft.util.SequencedPriorityIterator (priority-keyed FIFO; villages = priority 0)
//   - net.minecraft.world.level.block.JigsawBlock.canAttach (front-opposite + top-match
//      unless rollable + target==name) / Rotation.getShuffled
//   - net.minecraft.world.level.levelgen.structure.PoolElementStructurePiece

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// jigsawTotalPieceCap is the defensive total-piece backstop (the stronghold's 1000-piece
// precedent): even with all three bounds, a pathological self-referencing pool with a huge
// 80-radius footprint could place a great many pieces; this caps the count so the Placer is
// guaranteed to terminate in bounded memory/CPU (T-16-03). Real villages place ~10-40 pieces.
const jigsawTotalPieceCap = 1000

// pieceState ports JigsawPlacement$PieceState: a queued piece + the free-shape reference it
// expands against + its depth. The SequencedPriorityIterator orders these by placementPriority.
type pieceState struct {
	piece *PoolElementStructurePiece
	free  *voxelShape // the MutableObject<VoxelShape> the jar threads (subtracted as pieces place)
	depth int
}

// voxelShape is the ported (rigid) VoxelShape: the accumulated FREE space as a set of placed
// boxes that have been SUBTRACTED from the 80-radius bound, expressed as (bound, placed[]):
// a box is "free" iff it is inside `bound` and does not overlap any `placed` box. The jar uses
// net.minecraft.world.phys.shapes.VoxelShape (Shapes.join/joinIsNotEmpty over real voxel
// grids); for the RIGID village projection this reduces to a box-overlap test, which is the
// jar-faithful behavior (the 0.25 deflate the jar applies before the collision test prevents
// edge-touch false positives — replicated via a strict-overlap test, not an inclusive one).
type voxelShape struct {
	bound  BoundingBox   // the 80-radius free region (a child must stay inside this — bound #2)
	placed []BoundingBox // the boxes already occupied (a child must not overlap these — bound #3)
}

// joinIsNotEmpty ports Shapes.joinIsNotEmpty(freeShape, create(AABB.of(box).deflate(0.25)),
// ONLY_SECOND): true iff `box` has any cell that is NOT free — i.e. it leaves the bound OR
// overlaps a placed box. ONLY_SECOND = "in the candidate box but NOT in the free shape", so a
// non-empty result means collision/out-of-bound -> reject. The 0.25 deflate means a child
// that merely SHARES A FACE with a placed box (or the bound edge) does not collide; we model
// that with a strict-interior overlap (boxes touching at a face do not overlap).
func (s *voxelShape) joinIsNotEmpty(box BoundingBox) bool {
	// Out of the 80-radius bound? (any part of the box outside -> not fully free -> collision)
	if !boxInside(s.bound, box) {
		return true
	}
	// Overlaps an already-placed box (strict interior overlap, matching the 0.25 deflate)?
	for _, p := range s.placed {
		if strictOverlap(p, box) {
			return true
		}
	}
	return false
}

// occupy ports the jar's "subtract the placed box from the free shape" (Shapes.joinUnoptimized
// with ONLY_FIRST): the box becomes occupied, so later children cannot overlap it.
func (s *voxelShape) occupy(box BoundingBox) {
	s.placed = append(s.placed, box)
}

// boxInside reports whether `inner` is fully contained in `outer` (inclusive). The 80-radius
// bound check: a child leaving the bound by even one block is rejected (max_distance, bound #2).
func boxInside(outer, inner BoundingBox) bool {
	return inner.MinX >= outer.MinX && inner.MaxX <= outer.MaxX &&
		inner.MinY >= outer.MinY && inner.MaxY <= outer.MaxY &&
		inner.MinZ >= outer.MinZ && inner.MaxZ <= outer.MaxZ
}

// strictOverlap reports whether two boxes overlap in their INTERIOR (not merely touching at a
// face). The jar deflates the candidate AABB by 0.25 before the collision test, so two boxes
// that share a face (e.g. a street piece abutting a house) do NOT collide. Strict overlap
// requires the max of one to strictly exceed the min of the other on every axis.
func strictOverlap(a, b BoundingBox) bool {
	return a.MaxX > b.MinX && a.MinX < b.MaxX &&
		a.MaxY > b.MinY && a.MinY < b.MaxY &&
		a.MaxZ > b.MinZ && a.MinZ < b.MaxZ
}

// placer ports JigsawPlacement$Placer: the BFS assembly engine over a SequencedPriorityIterator
// work queue. It owns the maxDepth bound, the chunk-generator surface query (for terrain
// projection), the rng, and the output piece list.
type placer struct {
	maxDepth   int
	sampler    SurfaceSampler         // the WORLD_SURFACE_WG projection (CFR getFirstFreeHeight)
	rng        levelgen.RandomSource  // the piece-selection RNG (SetLargeFeatureSeed-seeded)
	pieces     []*PoolElementStructurePiece
	placing    *sequencedPriorityIterator
	projectTop bool // CFR `projectStartToHeightmap.isPresent()` — true for villages (WORLD_SURFACE_WG)
}

// addPieces ports JigsawPlacement.addPieces (the public + private overloads merged): pick the
// root rotation + start element, place the root, build the 80-radius free shape, run the BFS.
// Returns the assembled piece list (the StructureStart's pieces). startPos is the projected
// start position; maxDistance is the max_distance_from_center (80 for villages).
//
// Source: CFR JigsawPlacement.addPieces (both overloads).
func addPieces(startPool *StructureTemplatePool, startPos Pos, maxDepth, maxDistance int, projectStartToHeightmap bool, sampler SurfaceSampler, rng levelgen.RandomSource) []*PoolElementStructurePiece {
	// Root rotation draw (CFR Rotation.getRandom(rng)) — a load-bearing RNG advance.
	rot := getRandomRotation(rng)

	// Root element pick (CFR pool.getRandomTemplate(rng) = templates[nextInt(size)]).
	rootEl := startPool.getRandomTemplate(rng)
	if rootEl == nil || rootEl.IsEmpty() {
		return nil // empty/non-existent start pool -> no structure (CFR Optional.empty)
	}

	// The root piece sits at startPos with the drawn rotation (groundLevelDelta deferred — the
	// village projects the WHOLE start to the surface, so the root's delta is the jar's 1).
	rootBox := rootEl.BoundingBox(startPos, rot)
	root := &PoolElementStructurePiece{
		element:  rootEl,
		position: startPos,
		rotation: rot,
		genDepth: 0,
	}
	root.bbox = rootBox

	// The center of the root box (the 80-radius bound is centered here — CFR (maxX+minX)/2).
	cx := (rootBox.MaxX + rootBox.MinX) / 2
	cz := (rootBox.MaxZ + rootBox.MinZ) / 2

	// The 80-radius free VoxelShape (CFR lambda$addPieces$2): AABB
	// [cx-80 .. cx+80+1] x [minY .. maxY+1] x [cz-80 .. cz+80+1] minus the root box. We model
	// it as bound + the root box already occupied. The Y span uses the level height window
	// (the village places in the overworld build limits); we use a generous band so the Y
	// bound never spuriously rejects a rigid surface village (max_distance is HORIZONTAL).
	free := &voxelShape{
		bound: BoundingBox{
			MinX: cx - maxDistance, MinY: worldMinY, MinZ: cz - maxDistance,
			MaxX: cx + maxDistance + 1, MaxY: worldMaxY, MaxZ: cz + maxDistance + 1,
		},
	}
	free.occupy(rootBox)

	p := &placer{
		maxDepth:   maxDepth,
		sampler:    sampler,
		rng:        rng,
		placing:    newSequencedPriorityIterator(),
		// projectTop mirrors JigsawStructure.projectStartToHeightmap: villages (WORLD_SURFACE_WG)
		// project per-jigsaw non-rigid children to the surface; the bastion (Optional.empty) does
		// NOT -- its rigid pieces stay pinned to the fixed start Y (absolute 33), so it passes false.
		projectTop: projectStartToHeightmap,
	}
	p.pieces = append(p.pieces, root)

	// Seed the BFS with the root, then drain the priority queue (BFS, NOT recursion).
	p.tryPlacingChildren(root, free, 0)
	for p.placing.hasNext() {
		st := p.placing.next()
		p.tryPlacingChildren(st.piece, st.free, st.depth)
	}
	return p.pieces
}

// worldMinY/worldMaxY bound the free shape's Y extent (the overworld build window). The
// max_distance bound is HORIZONTAL (CFR JigsawStructure$MaxDistance.horizontal); the Y bound
// is the level height accessor's window in the jar. -64..320 is the overworld range; a margin
// keeps a tall rigid piece from clipping the bound spuriously.
const (
	worldMinY = -64
	worldMaxY = 320
)

// tryPlacingChildren ports JigsawPlacement$Placer.tryPlacingChildren: for each jigsaw block in
// `piece`, resolve its target (+ fallback) pool, build the weighted+shuffled candidate list,
// and for each candidate x rotation x candidate-jigsaw try to ALIGN + COLLISION-CHECK + place,
// breaking to the next jigsaw on the first success. Depth==maxDepth attaches only fallback.
//
// Villages are RIGID (projection==RIGID); the rigid branch of the height math is taken, so the
// child's Y is pinned to the parent's jigsaw Y (no per-jigsaw surface re-projection). The
// terrain_matching branch (getFirstFreeHeight per jigsaw) is ported for completeness.
//
// Source: CFR JigsawPlacement$Placer.tryPlacingChildren (the full per-jigsaw loop).
func (p *placer) tryPlacingChildren(piece *PoolElementStructurePiece, free *voxelShape, depth int) {
	element := piece.element
	pos := piece.position
	rot := piece.rotation
	pieceIsRigid := element.Projection() == ProjectionRigid
	pieceBB := piece.bbox
	pieceMinY := pieceBB.MinY

	parentJigsaws, err := element.Jigsaws(pos, rot, p.rng)
	if err != nil {
		return // a malformed jigsaw nbt -> stop expanding this piece (it is build-data)
	}

	for _, pj := range parentJigsaws {
		// The connection point: one block out from the parent jigsaw along its FRONT face.
		front := pj.FrontFacing
		connectPos := relative(pj.WorldPos, front)
		// pjYrel = parent jigsaw local Y above the piece floor (CFR pj.pos.getY() - minY).
		pjYrel := pj.WorldPos.Y - pieceMinY

		// Resolve the target pool; on miss, warn+skip (CFR Empty or non-existent pool).
		targetPool, ok := resolvePool(pj.Pool)
		if !ok {
			continue
		}
		// Resolve the fallback pool (CFR targetPool.getFallback()). A "minecraft:empty"
		// fallback resolves to the empty.json terminator pool (size 0) — NOT a panic.
		fallbackPool, _ := resolvePool(targetPool.Fallback())

		// Choose the free shape this jigsaw expands against: if the connection point is INSIDE
		// the parent piece's own box, use a LOCAL shape (the parent box) — a child folding back
		// into the parent is checked against the parent only; else use the shared free shape.
		localFree := free
		if pieceBB.IsInside(connectPos.X, connectPos.Y, connectPos.Z) {
			localFree = &voxelShape{bound: pieceBB}
		}

		// Build the candidate list: target pool's shuffled templates (only if depth != maxDepth)
		// then ALWAYS the fallback pool's shuffled templates (the terminators). The depth gate is
		// bound #1: at maxDepth, only fallback/terminators attach (CFR the `depth != maxDepth`
		// guard before addAll(targetPool.getShuffledTemplates)).
		var candidates []PoolElement
		if depth != p.maxDepth {
			candidates = append(candidates, targetPool.getShuffledTemplates(p.rng)...)
		}
		if fallbackPool != nil {
			candidates = append(candidates, fallbackPool.getShuffledTemplates(p.rng)...)
		}

		placementPriority := pj.PlacementPriority

		// Try each candidate against the CHOSEN shape (localFree when the connection folds back
		// into the parent box, else the shared free shape — CFR var29). The terminator ends the
		// search. On success, tryCandidates moves to the next jigsaw block.
		p.tryCandidates(piece, localFree, depth, pj, connectPos, pjYrel, pieceIsRigid, candidates, placementPriority)
	}
}

// tryCandidates ports the inner candidate x rotation x candidate-jigsaw loop of
// tryPlacingChildren. Returns true once a child is placed for this parent jigsaw (the jar
// `break`s to the next jigsaw on success). Each candidate's rotations are drawn via
// Rotation.getShuffled(rng) — the EXACT per-candidate RNG draws.
func (p *placer) tryCandidates(parent *PoolElementStructurePiece, free *voxelShape, depth int, pj JigsawBlockInfo, connectPos Pos, pjYrel int, parentIsRigid bool, candidates []PoolElement, placementPriority int) bool {
	for _, cand := range candidates {
		if cand.IsEmpty() {
			return true // the terminator: this jigsaw is capped, stop searching (CFR break)
		}
		for _, candRot := range getShuffledRotations(p.rng) {
			candJigsaws, err := cand.Jigsaws(Pos{0, 0, 0}, candRot, p.rng)
			if err != nil {
				continue
			}
			candIsRigid := cand.Projection() == ProjectionRigid

			for _, cj := range candJigsaws {
				if !canAttach(pj, cj) {
					continue
				}
				// Align the child's jigsaw to the parent's connection point: the child is
				// offset so cj lands at connectPos (CFR connectPos.subtract(cj.pos)).
				connectOffset := subtract(connectPos, cj.LocalPos)
				candMovedBB := cand.BoundingBox(connectOffset, candRot)
				candMinY := candMovedBB.MinY
				cjY := cj.LocalPos.Y

				// The vertical alignment (CFR var50 targetY): RIGID-RIGID pins the child Y to
				// the parent jigsaw Y; otherwise project to the surface. surfaceDelta accounts
				// for the front face's vertical step (a down/up-facing jigsaw).
				var targetY int
				if parentIsRigid && candIsRigid {
					targetY = pieceFloorPlusRel(parent, pjYrel)
				} else {
					surfaceY := p.sampler.SampleSurfaceY(pj.WorldPos.X, pj.WorldPos.Z)
					targetY = surfaceY - cjY
				}
				yDelta := targetY - candMinY

				candFinalBB := movedY(candMovedBB, yDelta)
				candFinalPos := Pos{connectOffset.X, connectOffset.Y + yDelta, connectOffset.Z}

				// THE COLLISION CHECK (bounds #2 + #3): reject if the child leaves the 80-radius
				// bound OR overlaps an already-placed box (Shapes.joinIsNotEmpty, ONLY_SECOND).
				if free.joinIsNotEmpty(candFinalBB) {
					continue
				}

				// Accept: subtract the child box from the free shape, build the piece, enqueue.
				free.occupy(candFinalBB)

				child := &PoolElementStructurePiece{
					element:  cand,
					position: candFinalPos,
					rotation: candRot,
					genDepth: depth + 1,
				}
				child.bbox = candFinalBB
				p.pieces = append(p.pieces, child)

				// Bound #1 + the defensive cap: enqueue for further expansion only if within
				// depth and the total-piece cap (T-16-03 — the runaway backstop).
				if depth+1 <= p.maxDepth && len(p.pieces) < jigsawTotalPieceCap {
					p.placing.add(&pieceState{piece: child, free: free, depth: depth + 1}, placementPriority)
				}
				return true // placed a child for this jigsaw — break to the next jigsaw block
			}
		}
	}
	return false
}

// pieceFloorPlusRel ports the RIGID-RIGID target Y: the parent piece floor (bbox.minY) plus
// the parent jigsaw's relative Y (CFR `pieceMinY + jigsawYrel`). The child's jigsaw is pinned
// to the same world Y the parent jigsaw sits at, so rigid pieces connect flush.
func pieceFloorPlusRel(parent *PoolElementStructurePiece, pjYrel int) int {
	return parent.bbox.MinY + pjYrel
}

// canAttach ports net.minecraft.world.level.block.JigsawBlock.canAttach(parent, child): the
// parent's front must oppose the child's front, AND (the parent is ROLLABLE OR the tops match),
// AND the parent's target name equals the child's name.
//
//	pFront == cFront.opposite()  &&  (rollable || pTop == cTop)  &&  pTarget == cName
//
// Source: CFR JigsawBlock.canAttach.
func canAttach(parent, child JigsawBlockInfo) bool {
	if parent.FrontFacing != directionOpposite(child.FrontFacing) {
		return false
	}
	rollable := parent.Joint == "rollable"
	if !rollable && parent.TopFacing != child.TopFacing {
		return false
	}
	return parent.Target == child.Name
}

// --- helpers (BlockPos arithmetic + the rotation draws) ---

// relative ports BlockPos.relative(Direction): step one block along dir.
func relative(p Pos, d block.Direction) Pos {
	switch d {
	case block.Down:
		return Pos{p.X, p.Y - 1, p.Z}
	case block.Up:
		return Pos{p.X, p.Y + 1, p.Z}
	case block.North:
		return Pos{p.X, p.Y, p.Z - 1}
	case block.South:
		return Pos{p.X, p.Y, p.Z + 1}
	case block.West:
		return Pos{p.X - 1, p.Y, p.Z}
	case block.East:
		return Pos{p.X + 1, p.Y, p.Z}
	}
	return p
}

// subtract ports BlockPos.subtract(Vec3i).
func subtract(a, b Pos) Pos { return Pos{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }

// movedY ports BoundingBox.moved(0, dy, 0).
func movedY(b BoundingBox, dy int) BoundingBox {
	b.MinY += dy
	b.MaxY += dy
	return b
}

// getRandomRotation ports Rotation.getRandom(rng) = values()[nextInt(4)] (the $values order
// NONE, CLOCKWISE_90, CLOCKWISE_180, COUNTERCLOCKWISE_90 — matching the Rotation iota).
func getRandomRotation(rng levelgen.RandomSource) Rotation {
	return Rotation(rng.NextIntN(4))
}

// allRotations is the Rotation.values() array in $values order (the iota order).
var allRotations = [4]Rotation{RotNone, RotClockwise90, RotClockwise180, RotCounterclockwise90}

// getShuffledRotations ports Rotation.getShuffled(rng) = Util.shuffledCopy(values(), rng):
// a Fisher-Yates shuffle of the 4 rotations (3 draws), in the jar's exact draw order.
func getShuffledRotations(rng levelgen.RandomSource) []Rotation {
	out := make([]Rotation, 4)
	copy(out, allRotations[:])
	for i := len(out); i > 1; i-- {
		j := int(rng.NextIntN(int32(i)))
		out[i-1], out[j] = out[j], out[i-1]
	}
	return out
}

// resolvePool loads a pool by id, returning (pool, true) on success. A non-existent pool
// returns (nil, false) — the Placer warns+skips (CFR Optional.isEmpty). The "minecraft:empty"
// terminator resolves to the empty.json pool (size 0) — present, never a miss (Pitfall #6).
func resolvePool(id string) (*StructureTemplatePool, bool) {
	if id == "" {
		return nil, false
	}
	p, err := LoadTemplatePool(id)
	if err != nil {
		return nil, false
	}
	return p, true
}

// getRandomTemplate ports StructureTemplatePool.getRandomTemplate(rng): templates[nextInt(size)]
// or EmptyPoolElement if empty. The single draw over the weight-expanded list.
func (p *StructureTemplatePool) getRandomTemplate(rng levelgen.RandomSource) PoolElement {
	if len(p.templates) == 0 {
		return EmptyPoolElementInstance
	}
	return p.templates[rng.NextIntN(int32(len(p.templates)))]
}

// --- PoolElementStructurePiece ---

// PoolElementStructurePiece ports net.minecraft.world.level.levelgen.structure.
// PoolElementStructurePiece: a placed pool element (its template + rotation + world position +
// bbox + genDepth). It satisfies the Piece interface; PostProcess places the element's template
// clipped to the chunk's writable box (the cross-chunk clip, Pitfall #2).
type PoolElementStructurePiece struct {
	element  PoolElement
	position Pos
	rotation Rotation
	bbox     BoundingBox
	genDepth int
}

// BoundingBox satisfies Piece.
func (p *PoolElementStructurePiece) BoundingBox() BoundingBox { return p.bbox }

// GenDepth returns the piece's BFS depth.
func (p *PoolElementStructurePiece) GenDepth() int { return p.genDepth }

// PostProcess ports PoolElementStructurePiece.place: write the element's template into the
// chunk's writable box, clipped (the village spans many chunks; placeInChunk redraws the same
// pieces per chunk and each clips to its own slice — idempotent, Pitfall #2). The rng is the
// per-chunk WorldgenRandom; the element's processors run inside PlaceInWorld.
func (p *PoolElementStructurePiece) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	p.element.Place(view, p.position, p.rotation, box, rng)
}

// --- SequencedPriorityIterator ---

// sequencedPriorityIterator ports net.minecraft.util.SequencedPriorityIterator: a priority
// queue keyed by int priority, draining the HIGHEST priority first and FIFO within a priority
// (a Deque per priority, addLast/removeFirst). Villages use placement_priority 0, so it is a
// plain FIFO queue — i.e. BFS. The priority key matters only for pools that set non-zero
// placement_priority (the jar processes high-priority pieces before low). Ported exactly so a
// future high-priority pool keeps the jar's order.
//
// Source: CFR net.minecraft.util.SequencedPriorityIterator (add / computeNext).
type sequencedPriorityIterator struct {
	queues   map[int][]*pieceState // priority -> FIFO deque (slice; removeFirst pops index 0)
	highPrio int                   // the current highest non-empty priority
	hasHigh  bool
}

func newSequencedPriorityIterator() *sequencedPriorityIterator {
	return &sequencedPriorityIterator{queues: map[int][]*pieceState{}}
}

// add enqueues a state at the given priority (addLast).
func (it *sequencedPriorityIterator) add(st *pieceState, priority int) {
	it.queues[priority] = append(it.queues[priority], st)
	if !it.hasHigh || priority > it.highPrio {
		it.highPrio = priority
		it.hasHigh = true
	}
}

// hasNext reports whether any queued state remains.
func (it *sequencedPriorityIterator) hasNext() bool {
	for _, q := range it.queues {
		if len(q) > 0 {
			return true
		}
	}
	return false
}

// next removes + returns the next state: the head (removeFirst) of the highest non-empty
// priority queue. Recomputes the highest priority if the current one drains (CFR
// switchCacheToNextHighestPrioQueue).
func (it *sequencedPriorityIterator) next() *pieceState {
	// Find the highest non-empty priority.
	best := 0
	found := false
	for prio, q := range it.queues {
		if len(q) == 0 {
			continue
		}
		if !found || prio > best {
			best = prio
			found = true
		}
	}
	if !found {
		return nil
	}
	q := it.queues[best]
	st := q[0]
	it.queues[best] = q[1:]
	return st
}
