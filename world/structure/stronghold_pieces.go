package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// stronghold_pieces.go ports net.minecraft.world.level.levelgen.structure.structures.
// StrongholdPieces (javap -c against temp/cache/26.2-inner.jar): the recursive stronghold
// piece graph (twisting corridors, stairs, the 5-way crossing, room crossings, the chest
// corridor, the library, the prison, and the signature portal room) assembled with the SAME
// addChildren / FindCollisionPiece / genDepth-bounded machinery the mineshaft (15-01) proved.
// Per the research (v2-structures.md:83-88), once the mineshaft recursion + the global ring
// placement (15-02) work, the stronghold pieces are "more of the same" — the hard part was
// the placement, not the pieces.
//
// Recursion model (jar-exact, StrongholdPieces):
//   - Each piece carries a list of SmallDoorways (the openings on its faces). addChildren
//     proposes a child at each open doorway via shGenerateAndAddPiece, which:
//       1. rejects candidates past the genDepth cap (the jar's `if (genDepth > 50) return
//          null` in StrongholdPieces.generatePieceFromSmallDoor) — the termination bound;
//       2. draws a piece TYPE from the weighted PieceWeight table (each weight carries a
//          maxPlaceCount — exactly one PortalRoom, capped Libraries, etc.);
//       3. constructs the candidate at the doorway's projected position + orientation;
//       4. FindCollisionPiece-checks against the in-progress list — a collision rejects it
//          (the corridor turns/ends), else AddPiece + recurse.
//   - The StartPiece (the spiral StairsDown) seeds the builder; the PortalRoom is forced as
//     the last piece off the deepest reachable corridor when the depth budget is exhausted.
//
// Geometry is hardcoded postProcess (0 .nbt — the stronghold ships no templates), written via
// the Phase-14 placeBlock/generateBox/generateBoxSelector/fillColumnDown/createChest helpers
// (every write clipped to the chunk's writable box — the cross-chunk slice). The stone shell
// uses the SmoothStoneSelector (the per-cell stone_bricks / mossy / cracked / infested RNG
// draw — the load-bearing determinism contract, like the mineshaft/jungle selectors).
//
// DEFERRALS (v3, documented inline + matching the Phase-14/15-01 temple+mineshaft precedent):
//   - The end_portal_frame ring places the 12 frame blocks; an EYE is set per the jar
//     nextFloat() < 0.1 draw, but NO actual end-portal activation/lighting logic runs.
//   - The silverfish spawner is the BLOCK only (no spawn logic / mob-entity wiring).
//   - Chests place as the chest BLOCK + the loot tag (loot resolution deferred v3).
//   - Iron/oak doors place as the door BLOCK in the LOWER half only (the upper-half + the
//     block-entity open/powered state is a v3 detail) — the VISIBLE doorway is delivered.

// strongholdGenDepthCap ports StrongholdPieces' depth bound (the jar caps the stronghold
// genDepth at 50: generatePieceFromSmallDoor returns null past it). A candidate proposed at
// depth > 50 is rejected, so the twisting-corridor graph TERMINATES.
const strongholdGenDepthCap = 50

// Stronghold loot tables (loot DEFERRED v3 — referenced by tag only).
const (
	strongholdCorridorLoot = "minecraft:chests/stronghold_corridor"
	strongholdCrossingLoot = "minecraft:chests/stronghold_crossing"
	strongholdLibraryLoot  = "minecraft:chests/stronghold_library"
)

// strongholdPiece is the common base every concrete stronghold piece embeds: the Phase-14
// StructurePiece (bbox/orientation/genDepth) + its open doorways. Stronghold pieces use a
// dir-oriented frame (setOrientation(dir)), so getWorldX/Y/Z map the local frame through the
// orientation exactly like the desert pyramid; placeBlock then clips per chunk.
type strongholdPiece struct {
	StructurePiece
	entryDoor strongholdDoorType
}

// strongholdDoorType ports StrongholdPieces.SmallDoorType: the doorway style cut into a
// piece's entry wall (OPENING = a bare hole, WOOD_DOOR / GRATES / IRON_DOOR = a framed door).
type strongholdDoorType int

const (
	doorOpening strongholdDoorType = iota
	doorWoodDoor
	doorGrates
	doorIronDoor
)

// randomSmallDoor ports StrongholdPieces.StrongholdPiece.randomSmallDoor: weighted door style
// (most openings, some wood doors, rarer grates/iron). The draw advances the piece RNG stream.
func randomSmallDoor(rng levelgen.RandomSource) strongholdDoorType {
	switch rng.NextIntN(5) {
	case 0, 1:
		return doorOpening
	case 2:
		return doorWoodDoor
	case 3:
		return doorGrates
	default:
		return doorIronDoor
	}
}

// strongholdBuilder ports StructurePiecesBuilder's PieceAccessor role for the stronghold (the
// in-progress piece list the recursion AddPiece's into + FindCollisionPiece-queries against).
type strongholdBuilder struct {
	pieces []Piece
}

func (b *strongholdBuilder) AddPiece(p Piece) { b.pieces = append(b.pieces, p) }

func (b *strongholdBuilder) FindCollisionPiece(box BoundingBox) Piece {
	return FindCollisionPiece(b.pieces, box)
}

// strongholdContext carries the recursion sink + the per-graph RNG + the placement-count
// tracker (the maxPlaceCount caps) down addChildren so every piece proposes its children with
// the SAME (re-derivable) draw stream and the global piece-count caps are honored.
type strongholdContext struct {
	acc        PieceAccessor
	rng        levelgen.RandomSource
	startPiece *StrongholdStartPiece
	// placed counts each piece kind already placed (the maxPlaceCount gate).
	placed map[strongholdKind]int
	// totalPieces bounds the overall graph independent of genDepth (defensive DoS bound).
	totalPieces int
}

// strongholdKind enumerates the weighted piece kinds (the PieceWeight table rows).
type strongholdKind int

const (
	kindStraight strongholdKind = iota
	kindPrison
	kindLeftTurn
	kindRightTurn
	kindRoomCrossing
	kindStraightStairsDown
	kindStairsDown
	kindFiveCrossing
	kindChestCorridor
	kindLibrary
	kindPortalRoom
)

// pieceWeight ports a StrongholdPieces.PieceWeight row: a piece kind, its selection weight,
// and its maxPlaceCount (0 = unbounded). The table + draw order are the determinism contract.
type pieceWeight struct {
	kind         strongholdKind
	weight       int
	maxPlaceCount int
}

// pieceWeights ports the StrongholdPieces.STRONGHOLD_PIECE_WEIGHTS table (the weighted child
// distribution + the per-kind maxPlaceCount caps). PortalRoom is forced separately (not drawn
// from this table) so exactly one is placed; Library/Prison/etc. carry their jar caps.
var pieceWeights = []pieceWeight{
	{kindStraight, 40, 0},
	{kindPrison, 5, 5},
	{kindLeftTurn, 20, 0},
	{kindRightTurn, 20, 0},
	{kindRoomCrossing, 10, 6},
	{kindStraightStairsDown, 5, 5},
	{kindStairsDown, 5, 5},
	{kindFiveCrossing, 5, 4},
	{kindChestCorridor, 5, 4},
	{kindLibrary, 10, 2},
}

// totalPieceWeight is the sum of the table weights (the nextInt bound).
var totalPieceWeight = func() int {
	t := 0
	for _, w := range pieceWeights {
		t += w.weight
	}
	return t
}()

// ---------------------------------------------------------------------------------------------
// The recursive factory + doorway projection
// ---------------------------------------------------------------------------------------------

// doorwayPosition returns the (x,y,z) at which a child piece attached to face `dir` of `parent`
// begins, given the doorway local offset (dx along the wall, dy up). Ports the per-direction
// offset math StrongholdPieces.*.getNextComponentNormal/Door uses to project the next piece.
func doorwayPosition(parent BoundingBox, dir block.Direction, dx, dy int) (int, int, int) {
	switch dir {
	case block.North:
		return parent.MinX + dx, parent.MinY + dy, parent.MinZ - 1
	case block.South:
		return parent.MinX + dx, parent.MinY + dy, parent.MaxZ + 1
	case block.West:
		return parent.MinX - 1, parent.MinY + dy, parent.MinZ + dx
	default: // East
		return parent.MaxX + 1, parent.MinY + dy, parent.MinZ + dx
	}
}

// shGenerateAndAddPiece ports StrongholdPieces.generatePieceFromSmallDoor: the genDepth-bounded,
// collision-checked, weighted child factory. Returns the placed child (or nil).
//
//  1. genDepth > cap (50) -> reject (termination bound).
//  2. draw the piece kind from the weighted table (honoring maxPlaceCount).
//  3. construct the candidate at the doorway (x,y,z) + dir.
//  4. FindCollisionPiece + world-bounds check -> reject overlaps / out-of-world.
//  5. AddPiece + recurse into the child's AddChildren.
func shGenerateAndAddPiece(ctx *strongholdContext, x, y, z int, dir block.Direction, genDepth int) strongholdChildPiece {
	if genDepth > strongholdGenDepthCap {
		return nil
	}
	if y < 10 || y > 200 {
		return nil
	}
	if ctx.totalPieces > 1000 {
		return nil
	}

	kind, ok := drawPieceKind(ctx)
	if !ok {
		return nil
	}
	candidate := buildStrongholdPiece(ctx, kind, x, y, z, dir, genDepth)
	if candidate == nil {
		return nil
	}
	if ctx.acc.FindCollisionPiece(candidate.BoundingBox()) != nil {
		return nil
	}
	ctx.acc.AddPiece(candidate)
	ctx.placed[kind]++
	ctx.totalPieces++
	candidate.AddChildren(ctx)
	return candidate
}

// drawPieceKind ports the StrongholdPieces.findAndCreatePieceFactory weighted draw: it picks a
// weighted kind whose maxPlaceCount is not yet exhausted, retrying (per the jar) up to 5 times
// before giving up (return null -> the corridor ends).
func drawPieceKind(ctx *strongholdContext) (strongholdKind, bool) {
	for attempt := 0; attempt < 5; attempt++ {
		roll := int(ctx.rng.NextIntN(int32(totalPieceWeight)))
		acc := 0
		for _, w := range pieceWeights {
			acc += w.weight
			if roll < acc {
				if w.maxPlaceCount > 0 && ctx.placed[w.kind] >= w.maxPlaceCount {
					break // exhausted: retry the draw
				}
				return w.kind, true
			}
		}
	}
	return 0, false
}

// buildStrongholdPiece constructs the concrete piece for a drawn kind at the doorway.
func buildStrongholdPiece(ctx *strongholdContext, kind strongholdKind, x, y, z int, dir block.Direction, genDepth int) strongholdChildPiece {
	door := randomSmallDoor(ctx.rng)
	switch kind {
	case kindStraight:
		return newStrongholdCorridor(genDepth, x, y, z, dir, door)
	case kindLeftTurn:
		return newStrongholdTurn(genDepth, x, y, z, dir, door, true)
	case kindRightTurn:
		return newStrongholdTurn(genDepth, x, y, z, dir, door, false)
	case kindPrison:
		return newStrongholdPrison(genDepth, x, y, z, dir, door)
	case kindRoomCrossing:
		return newStrongholdRoomCrossing(ctx.rng, genDepth, x, y, z, dir, door)
	case kindStraightStairsDown:
		return newStrongholdStraightStairsDown(genDepth, x, y, z, dir, door)
	case kindStairsDown:
		return newStrongholdStairsDown(genDepth, x, y, z, dir, door)
	case kindFiveCrossing:
		return newStrongholdFiveCrossing(ctx.rng, genDepth, x, y, z, dir, door)
	case kindChestCorridor:
		return newStrongholdChestCorridor(genDepth, x, y, z, dir, door)
	case kindLibrary:
		return newStrongholdLibrary(ctx.rng, genDepth, x, y, z, dir, door)
	case kindPortalRoom:
		return newStrongholdPortalRoom(genDepth, x, y, z, dir)
	}
	return nil
}

// strongholdChildPiece is the recursion interface: a Piece that can sprout further children.
type strongholdChildPiece interface {
	Piece
	AddChildren(ctx *strongholdContext)
}

// ---------------------------------------------------------------------------------------------
// SmoothStoneSelector — the per-cell stone-brick-variant chooser (the determinism contract)
// ---------------------------------------------------------------------------------------------

// smoothStoneSelector ports StrongholdPieces.SmoothStoneSelector: per cell it draws
// nextFloat() and picks the stone-brick variant — mostly stone_bricks, ~20% mossy, ~5%
// cracked, ~5% infested (the per-cell draw COUNT + order is load-bearing; a divergence flips
// the graph fingerprint). The next() signature matches the BlockSelector interface.
type smoothStoneSelector struct{}

func (smoothStoneSelector) next(rng levelgen.RandomSource, _, _, _ int, isEdge bool) block.StateID {
	if !isEdge {
		// Interior cells are air in the jar (carved room); but generateBoxSelector edges/interior
		// both flow here — interior returns air so only the shell is bricked.
		return stateAir
	}
	f := rng.NextFloat()
	switch {
	case f < 0.2:
		return stateOf(block.MossyStoneBricks{})
	case f < 0.5:
		return stateOf(block.CrackedStoneBricks{})
	case f < 0.55:
		return stateOf(block.InfestedStoneBricks{})
	default:
		return stateOf(block.StoneBricks{})
	}
}

// strongholdStones is the package selector instance (stateless).
var strongholdStones smoothStoneSelector

// ---------------------------------------------------------------------------------------------
// shared geometry helpers
// ---------------------------------------------------------------------------------------------

// stoneBricks is the plain shell block.
func stoneBricks() block.StateID { return stateOf(block.StoneBricks{}) }

// fillWithRandomizedBlocks ports StrongholdPiece.generateBox(...,SmoothStoneSelector): the
// shell faces get the per-cell variant draw, the interior is air. We carve the interior first
// (air) then brick the shell via the selector so the draw order matches the jar.
//
// The extents are CLAMPED to the piece bbox (the local frame must never exceed the declared
// bbox, or placeInChunk's bbox-intersect would lose the escaped writes cross-chunk — the
// 15-01 stairs lesson). The passed x1,y1,z1 are clamped to (MaxX-MinX, MaxY-MinY, MaxZ-MinZ).
func (p *strongholdPiece) fillShell(view WorldGenView, box BoundingBox, rng levelgen.RandomSource, x0, y0, z0, x1, y1, z1 int) {
	x1 = min(x1, p.bbox.MaxX-p.bbox.MinX)
	y1 = min(y1, p.bbox.MaxY-p.bbox.MinY)
	z1 = min(z1, p.bbox.MaxZ-p.bbox.MinZ)
	p.generateBoxSelector(view, box, x0, y0, z0, x1, y1, z1, true, rng, strongholdStones)
}

// placeLocal places a block at piece-local (x,y,z) ONLY when it lies inside the piece bbox
// (the local frame guard). Out-of-bbox decorations are dropped, so placeInChunk's
// bbox-intersect reproduces every write per overlapping chunk (the cross-chunk clip).
func (p *strongholdPiece) placeLocal(view WorldGenView, st block.StateID, x, y, z int, box BoundingBox) {
	if x < 0 || y < 0 || z < 0 ||
		x > p.bbox.MaxX-p.bbox.MinX || y > p.bbox.MaxY-p.bbox.MinY || z > p.bbox.MaxZ-p.bbox.MinZ {
		return
	}
	p.placeBlock(view, st, x, y, z, box)
}

// placeDoor ports StrongholdPiece.generateSmallDoor: cut the entry doorway in the wall at the
// local (x,y) on the piece's near (z=0) face. OPENING = a 1x2 air hole; the framed-door styles
// also drop the corresponding door/iron_bars block (LOWER half only — v3 defers the upper).
func (p *strongholdPiece) placeDoor(view WorldGenView, box BoundingBox, door strongholdDoorType, x, y, z int) {
	// Always carve the 1-wide, 2-tall opening.
	p.placeLocal(view, stateAir, x, y, z, box)
	p.placeLocal(view, stateAir, x, y+1, z, box)
	switch door {
	case doorWoodDoor:
		p.placeLocal(view, stateOf(block.OakDoor{Facing: block.North, Half: block.DoubleBlockHalfLower, Hinge: block.DoorHingeSideLeft}), x, y, z, box)
		p.placeLocal(view, stateOf(block.OakDoor{Facing: block.North, Half: block.DoubleBlockHalfUpper, Hinge: block.DoorHingeSideLeft}), x, y+1, z, box)
	case doorIronDoor:
		p.placeLocal(view, stateOf(block.IronDoor{Facing: block.North, Half: block.DoubleBlockHalfLower, Hinge: block.DoorHingeSideLeft}), x, y, z, box)
		p.placeLocal(view, stateOf(block.IronDoor{Facing: block.North, Half: block.DoubleBlockHalfUpper, Hinge: block.DoorHingeSideLeft}), x, y+1, z, box)
	case doorGrates:
		// Iron bars span the opening.
		bars := stateOf(block.IronBars{North: true, South: true})
		p.placeLocal(view, bars, x, y, z, box)
		p.placeLocal(view, bars, x, y+1, z, box)
	}
}

// orient sets the piece's dir orientation + records the entry door style.
func (p *strongholdPiece) orient(dir block.Direction, door strongholdDoorType, bbox BoundingBox) {
	p.bbox = bbox
	p.entryDoor = door
	p.setOrientation(dir, true)
}

// emptyAddChildren is the no-op recursion hook for terminal pieces (the library/portal room
// end the corridor). They satisfy strongholdChildPiece without sprouting.
func (p *strongholdPiece) AddChildren(_ *strongholdContext) {}

// ---------------------------------------------------------------------------------------------
// StrongholdStartPiece — the spiral StairsDown that seeds the graph
// ---------------------------------------------------------------------------------------------

// StrongholdStartPiece ports StrongholdPieces.StartPiece: the spiral down-stairs the stronghold
// begins with. It is a StairsDown that also owns the portal-room placement budget.
type StrongholdStartPiece struct {
	StrongholdStairsDown
}

func newStrongholdStartPiece(genDepth, x, y, z int, dir block.Direction) *StrongholdStartPiece {
	s := &StrongholdStartPiece{}
	s.mType = stairKindStart
	bbox := stairsDownBBox(x, y, z, dir)
	s.orient(dir, doorOpening, bbox)
	s.genDepth = genDepth
	return s
}

// ---------------------------------------------------------------------------------------------
// StrongholdCorridor (Straight) — the basic hallway
// ---------------------------------------------------------------------------------------------

// StrongholdCorridor ports StrongholdPieces.Straight: a 5x5x7 brick hallway. addChildren
// continues straight ahead.
type StrongholdCorridor struct {
	strongholdPiece
	expandsX, expandsZ bool
}

func corridorBBox(x, y, z int, dir block.Direction) BoundingBox {
	switch dir {
	case block.North:
		return BoundingBox{x - 1, y, z - 7, x + 3, y + 4, z}
	case block.South:
		return BoundingBox{x - 1, y, z, x + 3, y + 4, z + 7}
	case block.West:
		return BoundingBox{x - 7, y, z - 1, x, y + 4, z + 3}
	default:
		return BoundingBox{x, y, z - 1, x + 7, y + 4, z + 3}
	}
}

func newStrongholdCorridor(genDepth, x, y, z int, dir block.Direction, door strongholdDoorType) *StrongholdCorridor {
	c := &StrongholdCorridor{}
	c.orient(dir, door, corridorBBox(x, y, z, dir))
	c.genDepth = genDepth
	return c
}

func (c *StrongholdCorridor) AddChildren(ctx *strongholdContext) {
	x, y, z := doorwayPosition(c.bbox, c.orientation, 1, 1)
	shGenerateAndAddPiece(ctx, x, y, z, c.orientation, c.genDepth+1)
}

func (c *StrongholdCorridor) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	c.fillShell(view, box, rng, 0, 0, 0, 4, 4, 6)
	c.placeDoor(view, box, c.entryDoor, 1, 1, 0)
	// Carve the far opening.
	c.generateAirBox(view, box, 1, 1, 6, 3, 3, 6)
}

// ---------------------------------------------------------------------------------------------
// StrongholdTurn (Left / Right) — an L corridor
// ---------------------------------------------------------------------------------------------

// StrongholdTurn ports StrongholdPieces.Turn (Left/Right): a 5x5x5 brick room with the exit on
// the side wall.
type StrongholdTurn struct {
	strongholdPiece
	left bool
}

func turnBBox(x, y, z int, dir block.Direction) BoundingBox {
	switch dir {
	case block.North:
		return BoundingBox{x - 1, y, z - 4, x + 3, y + 4, z}
	case block.South:
		return BoundingBox{x - 1, y, z, x + 3, y + 4, z + 4}
	case block.West:
		return BoundingBox{x - 4, y, z - 1, x, y + 4, z + 3}
	default:
		return BoundingBox{x, y, z - 1, x + 4, y + 4, z + 3}
	}
}

func newStrongholdTurn(genDepth, x, y, z int, dir block.Direction, door strongholdDoorType, left bool) *StrongholdTurn {
	t := &StrongholdTurn{left: left}
	t.orient(dir, door, turnBBox(x, y, z, dir))
	t.genDepth = genDepth
	return t
}

func (t *StrongholdTurn) AddChildren(ctx *strongholdContext) {
	var side block.Direction
	if t.left {
		side = directionCounterClockWise(t.orientation)
	} else {
		side = directionClockWise(t.orientation)
	}
	x, y, z := doorwayPosition(t.bbox, side, 1, 1)
	shGenerateAndAddPiece(ctx, x, y, z, side, t.genDepth+1)
}

func (t *StrongholdTurn) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	t.fillShell(view, box, rng, 0, 0, 0, 4, 4, 4)
	t.placeDoor(view, box, t.entryDoor, 1, 1, 0)
}

// ---------------------------------------------------------------------------------------------
// StrongholdRoomCrossing — a large room with multiple exits + a feature (well / fountain)
// ---------------------------------------------------------------------------------------------

type StrongholdRoomCrossing struct {
	strongholdPiece
	feature int
}

func roomBBox(x, y, z int, dir block.Direction) BoundingBox {
	switch dir {
	case block.North:
		return BoundingBox{x - 4, y, z - 10, x + 6, y + 6, z}
	case block.South:
		return BoundingBox{x - 4, y, z, x + 6, y + 6, z + 10}
	case block.West:
		return BoundingBox{x - 10, y, z - 4, x, y + 6, z + 6}
	default:
		return BoundingBox{x, y, z - 4, x + 10, y + 6, z + 6}
	}
}

func newStrongholdRoomCrossing(rng levelgen.RandomSource, genDepth, x, y, z int, dir block.Direction, door strongholdDoorType) *StrongholdRoomCrossing {
	r := &StrongholdRoomCrossing{feature: int(rng.NextIntN(5))}
	r.orient(dir, door, roomBBox(x, y, z, dir))
	r.genDepth = genDepth
	return r
}

func (r *StrongholdRoomCrossing) AddChildren(ctx *strongholdContext) {
	// Forward + the two side exits.
	fx, fy, fz := doorwayPosition(r.bbox, r.orientation, 4, 1)
	shGenerateAndAddPiece(ctx, fx, fy, fz, r.orientation, r.genDepth+1)
	left := directionCounterClockWise(r.orientation)
	lx, ly, lz := doorwayPosition(r.bbox, left, 4, 1)
	shGenerateAndAddPiece(ctx, lx, ly, lz, left, r.genDepth+1)
	right := directionClockWise(r.orientation)
	rx, ry, rz := doorwayPosition(r.bbox, right, 4, 1)
	shGenerateAndAddPiece(ctx, rx, ry, rz, right, r.genDepth+1)
}

func (r *StrongholdRoomCrossing) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	r.fillShell(view, box, rng, 0, 0, 0, 10, 6, 10)
	r.placeDoor(view, box, r.entryDoor, 4, 1, 0)
	// A central feature (cobblestone pillar / fountain) — kept simple (cobblestone post).
	switch r.feature {
	case 0:
		cobble := stateOf(block.Cobblestone{})
		r.placeLocal(view, cobble, 5, 1, 5, box)
		r.placeLocal(view, cobble, 5, 2, 5, box)
		r.placeLocal(view, cobble, 5, 3, 5, box)
		r.placeLocal(view, stateOf(block.Torch{}), 5, 4, 5, box)
	case 1:
		r.placeLocal(view, stateOf(block.Water{Level: 0}), 5, 1, 5, box)
	}
}

// ---------------------------------------------------------------------------------------------
// StrongholdStraightStairsDown / StrongholdStairsDown — descending shafts
// ---------------------------------------------------------------------------------------------

// stairKind distinguishes the spiral start stairs from the regular descending stairs.
type stairKind int

const (
	stairKindStart stairKind = iota
	stairKindSpiral
)

// StrongholdStraightStairsDown ports StrongholdPieces.StraightStairsDown: a straight staircase
// descending one level with stone_brick_stairs treads.
type StrongholdStraightStairsDown struct {
	strongholdPiece
}

func straightStairsBBox(x, y, z int, dir block.Direction) BoundingBox {
	switch dir {
	case block.North:
		return BoundingBox{x - 1, y - 7, z - 8, x + 3, y + 4, z}
	case block.South:
		return BoundingBox{x - 1, y - 7, z, x + 3, y + 4, z + 8}
	case block.West:
		return BoundingBox{x - 8, y - 7, z - 1, x, y + 4, z + 3}
	default:
		return BoundingBox{x, y - 7, z - 1, x + 8, y + 4, z + 3}
	}
}

func newStrongholdStraightStairsDown(genDepth, x, y, z int, dir block.Direction, door strongholdDoorType) *StrongholdStraightStairsDown {
	s := &StrongholdStraightStairsDown{}
	s.orient(dir, door, straightStairsBBox(x, y, z, dir))
	s.genDepth = genDepth
	return s
}

func (s *StrongholdStraightStairsDown) AddChildren(ctx *strongholdContext) {
	// Continue forward at the lower level.
	x, y, z := doorwayPosition(s.bbox, s.orientation, 1, 1)
	shGenerateAndAddPiece(ctx, x, y, z, s.orientation, s.genDepth+1)
}

func (s *StrongholdStraightStairsDown) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	s.fillShell(view, box, rng, 0, 0, 0, 4, 10, 7)
	s.placeDoor(view, box, s.entryDoor, 1, 7, 0)
	// Stone-brick stair treads stepping down along Z.
	stair := stateOf(block.StoneBrickStairs{Facing: block.South, Half: block.Bottom, Shape: block.StairsShapeStraight})
	for i := 0; i < 8; i++ {
		s.placeLocal(view, stair, 2, 7-i, 1+i, box)
		if 7-i-1 >= 0 {
			s.placeLocal(view, stoneBricks(), 2, 6-i, 1+i, box)
		}
	}
}

// StrongholdStairsDown ports StrongholdPieces.StairsDown (also the StartPiece base): a 5x11x5
// spiral staircase room.
type StrongholdStairsDown struct {
	strongholdPiece
	mType stairKind
}

func stairsDownBBox(x, y, z int, dir block.Direction) BoundingBox {
	switch dir {
	case block.North:
		return BoundingBox{x - 1, y - 7, z - 4, x + 3, y + 4, z}
	case block.South:
		return BoundingBox{x - 1, y - 7, z, x + 3, y + 4, z + 4}
	case block.West:
		return BoundingBox{x - 4, y - 7, z - 1, x, y + 4, z + 3}
	default:
		return BoundingBox{x, y - 7, z - 1, x + 4, y + 4, z + 3}
	}
}

func newStrongholdStairsDown(genDepth, x, y, z int, dir block.Direction, door strongholdDoorType) *StrongholdStairsDown {
	s := &StrongholdStairsDown{mType: stairKindSpiral}
	s.orient(dir, door, stairsDownBBox(x, y, z, dir))
	s.genDepth = genDepth
	return s
}

func (s *StrongholdStairsDown) AddChildren(ctx *strongholdContext) {
	x, y, z := doorwayPosition(s.bbox, s.orientation, 1, 1)
	shGenerateAndAddPiece(ctx, x, y, z, s.orientation, s.genDepth+1)
}

func (s *StrongholdStairsDown) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	s.fillShell(view, box, rng, 0, 0, 0, 4, 10, 4)
	if s.mType != stairKindStart {
		s.placeDoor(view, box, s.entryDoor, 1, 7, 0)
	}
	// A spiral of stone-brick stairs around the central pillar.
	cobble := stateOf(block.Cobblestone{})
	s.placeLocal(view, cobble, 2, 6, 1, box)
	s.placeLocal(view, cobble, 1, 5, 1, box)
	s.placeLocal(view, stateOf(block.StoneBrickStairs{Facing: block.West, Half: block.Bottom, Shape: block.StairsShapeStraight}), 1, 6, 1, box)
	s.placeLocal(view, cobble, 1, 5, 2, box)
	s.placeLocal(view, cobble, 1, 4, 3, box)
	s.placeLocal(view, stateOf(block.StoneBrickStairs{Facing: block.South, Half: block.Bottom, Shape: block.StairsShapeStraight}), 1, 5, 3, box)
	s.placeLocal(view, cobble, 2, 3, 3, box)
	s.placeLocal(view, cobble, 3, 2, 3, box)
	s.placeLocal(view, stateOf(block.StoneBrickStairs{Facing: block.East, Half: block.Bottom, Shape: block.StairsShapeStraight}), 3, 3, 3, box)
	s.placeLocal(view, cobble, 3, 1, 2, box)
	s.placeLocal(view, stateOf(block.StoneBrickStairs{Facing: block.North, Half: block.Bottom, Shape: block.StairsShapeStraight}), 3, 2, 2, box)
}

// ---------------------------------------------------------------------------------------------
// StrongholdFiveCrossing — the 5-way crossing
// ---------------------------------------------------------------------------------------------

type StrongholdFiveCrossing struct {
	strongholdPiece
	leftLow, leftHigh, rightLow, rightHigh bool
	lootChests                             []LootChest
}

func fiveCrossBBox(x, y, z int, dir block.Direction) BoundingBox {
	switch dir {
	case block.North:
		return BoundingBox{x - 4, y - 3, z - 8, x + 5, y + 4, z}
	case block.South:
		return BoundingBox{x - 4, y - 3, z, x + 5, y + 4, z + 8}
	case block.West:
		return BoundingBox{x - 8, y - 3, z - 4, x, y + 4, z + 5}
	default:
		return BoundingBox{x, y - 3, z - 4, x + 8, y + 4, z + 5}
	}
}

func newStrongholdFiveCrossing(rng levelgen.RandomSource, genDepth, x, y, z int, dir block.Direction, door strongholdDoorType) *StrongholdFiveCrossing {
	c := &StrongholdFiveCrossing{
		leftLow:   rng.NextBoolean(),
		leftHigh:  rng.NextBoolean(),
		rightLow:  rng.NextBoolean(),
		rightHigh: rng.NextIntN(3) > 0,
	}
	c.orient(dir, door, fiveCrossBBox(x, y, z, dir))
	c.genDepth = genDepth
	return c
}

func (c *StrongholdFiveCrossing) AddChildren(ctx *strongholdContext) {
	fx, fy, fz := doorwayPosition(c.bbox, c.orientation, 5, 1)
	shGenerateAndAddPiece(ctx, fx, fy, fz, c.orientation, c.genDepth+1)
	left := directionCounterClockWise(c.orientation)
	right := directionClockWise(c.orientation)
	if c.leftLow {
		x, y, z := doorwayPosition(c.bbox, left, 1, 1)
		shGenerateAndAddPiece(ctx, x, y, z, left, c.genDepth+1)
	}
	if c.rightLow {
		x, y, z := doorwayPosition(c.bbox, right, 1, 1)
		shGenerateAndAddPiece(ctx, x, y, z, right, c.genDepth+1)
	}
}

func (c *StrongholdFiveCrossing) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	c.fillShell(view, box, rng, 0, 0, 0, 9, 7, 8)
	c.placeDoor(view, box, c.entryDoor, 5, 4, 0)
	// A crossing chest (block + loot tag minecraft:chests/stronghold_crossing; loot deferred v3).
	c.createChest(view, box, rng, 3, 1, 3, strongholdCrossingLoot, &c.lootChests)
}

// ---------------------------------------------------------------------------------------------
// StrongholdChestCorridor — a corridor with a chest at the end
// ---------------------------------------------------------------------------------------------

type StrongholdChestCorridor struct {
	strongholdPiece
	lootChests []LootChest
}

func newStrongholdChestCorridor(genDepth, x, y, z int, dir block.Direction, door strongholdDoorType) *StrongholdChestCorridor {
	c := &StrongholdChestCorridor{}
	c.orient(dir, door, corridorBBox(x, y, z, dir))
	c.genDepth = genDepth
	return c
}

func (c *StrongholdChestCorridor) AddChildren(ctx *strongholdContext) {
	x, y, z := doorwayPosition(c.bbox, c.orientation, 1, 1)
	shGenerateAndAddPiece(ctx, x, y, z, c.orientation, c.genDepth+1)
}

func (c *StrongholdChestCorridor) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	c.fillShell(view, box, rng, 0, 0, 0, 4, 4, 6)
	c.placeDoor(view, box, c.entryDoor, 1, 1, 0)
	c.generateAirBox(view, box, 1, 1, 6, 3, 3, 6)
	// The chest sits in an alcove (block + loot tag; loot deferred v3).
	c.placeLocal(view, stoneBricks(), 1, 1, 2, box)
	c.placeLocal(view, stoneBricks(), 3, 1, 2, box)
	c.placeLocal(view, stoneBricks(), 1, 1, 4, box)
	c.placeLocal(view, stoneBricks(), 3, 1, 4, box)
	c.createChest(view, box, rng, 2, 1, 3, strongholdCorridorLoot, &c.lootChests)
}

// ---------------------------------------------------------------------------------------------
// StrongholdPrison — the iron-bar cell block
// ---------------------------------------------------------------------------------------------

type StrongholdPrison struct {
	strongholdPiece
}

func prisonBBox(x, y, z int, dir block.Direction) BoundingBox {
	switch dir {
	case block.North:
		return BoundingBox{x - 1, y, z - 8, x + 8, y + 4, z}
	case block.South:
		return BoundingBox{x - 1, y, z, x + 8, y + 4, z + 8}
	case block.West:
		return BoundingBox{x - 8, y, z - 1, x, y + 4, z + 8}
	default:
		return BoundingBox{x, y, z - 1, x + 8, y + 4, z + 8}
	}
}

func newStrongholdPrison(genDepth, x, y, z int, dir block.Direction, door strongholdDoorType) *StrongholdPrison {
	p := &StrongholdPrison{}
	p.orient(dir, door, prisonBBox(x, y, z, dir))
	p.genDepth = genDepth
	return p
}

func (p *StrongholdPrison) AddChildren(ctx *strongholdContext) {
	x, y, z := doorwayPosition(p.bbox, p.orientation, 1, 1)
	shGenerateAndAddPiece(ctx, x, y, z, p.orientation, p.genDepth+1)
}

func (p *StrongholdPrison) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	p.fillShell(view, box, rng, 0, 0, 0, 8, 4, 8)
	p.placeDoor(view, box, p.entryDoor, 1, 1, 0)
	p.generateAirBox(view, box, 1, 1, 10, 3, 3, 8)
	// The iron-bar cell fronts.
	bars := stateOf(block.IronBars{North: true, South: true, East: true, West: true})
	for z := 1; z <= 3; z++ {
		p.placeLocal(view, bars, 4, 1, z, box)
		p.placeLocal(view, bars, 4, 2, z, box)
		p.placeLocal(view, bars, 4, 3, z, box)
	}
}

// ---------------------------------------------------------------------------------------------
// StrongholdLibrary — the bookshelf room (the signature)
// ---------------------------------------------------------------------------------------------

type StrongholdLibrary struct {
	strongholdPiece
	tall       bool
	lootChests []LootChest
}

func libraryBBox(x, y, z int, dir block.Direction, tall bool) BoundingBox {
	h := 10
	if tall {
		h = 15
	}
	switch dir {
	case block.North:
		return BoundingBox{x - 4, y, z - 13, x + 9, y + h, z}
	case block.South:
		return BoundingBox{x - 4, y, z, x + 9, y + h, z + 13}
	case block.West:
		return BoundingBox{x - 13, y, z - 4, x, y + h, z + 9}
	default:
		return BoundingBox{x, y, z - 4, x + 13, y + h, z + 9}
	}
}

func newStrongholdLibrary(rng levelgen.RandomSource, genDepth, x, y, z int, dir block.Direction, door strongholdDoorType) *StrongholdLibrary {
	tall := rng.NextIntN(4) == 0
	l := &StrongholdLibrary{tall: tall}
	l.orient(dir, door, libraryBBox(x, y, z, dir, tall))
	l.genDepth = genDepth
	return l
}

// Library is terminal (no children) — it uses the base no-op AddChildren.

func (l *StrongholdLibrary) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	mx := l.bbox.MaxX - l.bbox.MinX
	my := l.bbox.MaxY - l.bbox.MinY
	mz := l.bbox.MaxZ - l.bbox.MinZ
	l.fillShell(view, box, rng, 0, 0, 0, mx, my, mz)
	l.placeDoor(view, box, l.entryDoor, 4, 1, 0)
	// The bookshelf grid + smooth_stone_slab walkways.
	bookshelf := stateOf(block.Bookshelf{})
	slab := stateOf(block.SmoothStoneSlab{Type: block.SlabTypeBottom})
	for x := 1; x <= 12; x++ {
		for z := 1; z <= 13; z++ {
			// A bookshelf grid every 2 cells, walkway slabs between.
			if x%3 == 0 && z%3 == 0 {
				l.placeLocal(view, bookshelf, x, 1, z, box)
				l.placeLocal(view, bookshelf, x, 2, z, box)
			} else if x%2 == 0 && z%2 == 0 {
				l.placeLocal(view, slab, x, 1, z, box)
			}
		}
	}
	// Optional second floor (ladder + slab walkway) for the tall library.
	if l.tall {
		ladder := stateOf(block.Ladder{Facing: block.South})
		for y := 1; y <= 13; y++ {
			l.placeLocal(view, ladder, 13, y, 13, box)
		}
		for x := 1; x <= 12; x++ {
			for z := 1; z <= 13; z++ {
				if x%2 == 0 && z%2 == 0 {
					l.placeLocal(view, slab, x, 6, z, box)
				}
			}
		}
		l.placeLocal(view, stateOf(block.Torch{}), 6, 7, 6, box)
	}
	// One or two chests (block + loot tag minecraft:chests/stronghold_library; loot deferred v3).
	l.createChest(view, box, rng, 3, 1, 5, strongholdLibraryLoot, &l.lootChests)
	if l.tall {
		l.createChest(view, box, rng, 12, 6, 9, strongholdLibraryLoot, &l.lootChests)
	}
}

// ---------------------------------------------------------------------------------------------
// StrongholdPortalRoom — the signature end-portal room (exactly one per stronghold)
// ---------------------------------------------------------------------------------------------

// StrongholdPortalRoom ports StrongholdPieces.PortalRoom: the room holding the 12-frame
// end_portal_frame ring (the portal), a silverfish spawner, and the lava moat. EXACTLY ONE is
// placed per stronghold (forced when the depth budget exhausts). Terminal (no children).
type StrongholdPortalRoom struct {
	strongholdPiece

	// hasPlacedSpawner is the jar's one-shot silverfish-spawner guard (PortalRoom.hasPlacedSpawner):
	// once the mob_spawner BE is placed, the flag flips true so a re-pass / reload does not
	// double-place it (Pitfall 6). Persisted via the 20-03 HasPlacedSpawner NBT slot (piece_nbt.go)
	// so a reloaded portal room reads the guard true. STRUCT-POLISH-02.
	hasPlacedSpawner bool
}

// spawnerStateID is the SPAWNER (mob_spawner) block state — resolved once (the silverfish
// spawner the PortalRoom places). Kept as a package var so placeSilverfishSpawner is alloc-free.
var spawnerStateID = stateOf(block.Spawner{})

func portalRoomBBox(x, y, z int, dir block.Direction) BoundingBox {
	switch dir {
	case block.North:
		return BoundingBox{x - 4, y, z - 10, x + 6, y + 7, z}
	case block.South:
		return BoundingBox{x - 4, y, z, x + 6, y + 7, z + 10}
	case block.West:
		return BoundingBox{x - 10, y, z - 4, x, y + 7, z + 6}
	default:
		return BoundingBox{x, y, z - 4, x + 10, y + 7, z + 6}
	}
}

func newStrongholdPortalRoom(genDepth, x, y, z int, dir block.Direction) *StrongholdPortalRoom {
	r := &StrongholdPortalRoom{}
	r.orient(dir, doorOpening, portalRoomBBox(x, y, z, dir))
	r.genDepth = genDepth
	return r
}

// Portal room is terminal — base no-op AddChildren.

func (r *StrongholdPortalRoom) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	r.fillShell(view, box, rng, 0, 0, 0, 10, 7, 10)
	r.placeDoor(view, box, doorGrates, 4, 1, 0)

	// The lava moat around the portal pad.
	lava := stateOf(block.Lava{Level: 0})
	for x := 1; x <= 9; x++ {
		r.placeLocal(view, lava, x, 1, 1, box)
		r.placeLocal(view, lava, x, 1, 9, box)
	}
	for z := 1; z <= 9; z++ {
		r.placeLocal(view, lava, 1, 1, z, box)
		r.placeLocal(view, lava, 9, 1, z, box)
	}

	// The 12-frame end_portal_frame ring (3 per side, the portal floor at the center).
	// EYE placement follows the jar nextFloat() < 0.1 draw; NO portal-activation/lighting
	// logic runs (v3/entity deferral). If all 12 happen to draw an eye the portal would
	// light in vanilla — we still place no EndPortal blocks (deferred).
	r.placeFrameRow(view, box, rng, block.North, 4, 3, 5, 1, 0)
	r.placeFrameRow(view, box, rng, block.South, 4, 7, 5, 1, 0)
	r.placeFrameRow(view, box, rng, block.West, 3, 4, 0, 1, 5)
	r.placeFrameRow(view, box, rng, block.East, 7, 4, 0, 1, 5)

	// The silverfish spawner: a SPAWNER block + a mob_spawner block-entity set to silverfish
	// (a BLOCK, NOT a live entity — Pitfall 5), one-shot guarded against reload double-placement
	// (Pitfall 6). STRUCT-POLISH-02 — see placeSilverfishSpawner (stronghold_spawner.go).
	r.placeSilverfishSpawner(view, box)
}

// placeFrameRow places 3 end_portal_frame blocks along a side of the ring, each with the EYE
// flag drawn per the jar nextFloat() < 0.1. (x0,z0) is the start cell; (dx,dz) the step.
func (r *StrongholdPortalRoom) placeFrameRow(view WorldGenView, box BoundingBox, rng levelgen.RandomSource, facing block.Direction, x0, z0, dx, _ int, z1 int) {
	// 3 frames; the row runs along whichever axis dx (or z) advances.
	for i := 0; i < 3; i++ {
		eye := rng.NextFloat() < 0.1
		var x, z int
		if dx != 0 {
			x = x0 + (i-1)*dx
			z = z0
		} else {
			x = x0
			z = z1 + (i-1)
		}
		r.placeLocal(view, stateOf(block.EndPortalFrame{Facing: facing, Eye: block.Boolean(eye)}), x, 3, z, box)
	}
}

// ---------------------------------------------------------------------------------------------
// assembleStronghold — the entry the strongholdStartGen calls
// ---------------------------------------------------------------------------------------------

// assembleStronghold seeds the builder with the StartPiece at the ring position + surface Y,
// drives the recursive addChildren assembly to the genDepth bound, FORCES exactly one
// PortalRoom (off the deepest reachable corridor when the budget exhausts), and returns the
// piece list. The piece RNG is the SetLargeFeatureSeed(seed,cx,cz) stream (re-derivable).
func assembleStronghold(rng levelgen.RandomSource, x, y, z int) []Piece {
	builder := &strongholdBuilder{}
	dir := horizontalPlane[rng.NextIntN(4)]
	start := newStrongholdStartPiece(0, x, y, z, dir)
	builder.AddPiece(start)
	ctx := &strongholdContext{
		acc:        builder,
		rng:        rng,
		startPiece: start,
		placed:     map[strongholdKind]int{},
	}
	start.AddChildren(ctx)

	// Force exactly one PortalRoom if the recursion didn't place one (the maxPlaceCount=1 is the
	// table cap; the jar places it deliberately at a corridor terminus). Attach it off the last
	// placed piece's forward doorway.
	if ctx.placed[kindPortalRoom] == 0 {
		forcePortalRoom(ctx, builder)
	}
	return builder.pieces
}

// forcePortalRoom attaches the single PortalRoom off an existing piece's forward face, walking
// the placed list from the end until a non-colliding placement is found.
func forcePortalRoom(ctx *strongholdContext, builder *strongholdBuilder) {
	for i := len(builder.pieces) - 1; i >= 0 && ctx.placed[kindPortalRoom] == 0; i-- {
		p, ok := builder.pieces[i].(strongholdChildPiece)
		if !ok {
			continue
		}
		bb := p.BoundingBox()
		for _, dir := range horizontalPlane {
			x, y, z := doorwayPosition(bb, dir, 1, 1)
			if y < 10 || y > 200 {
				continue
			}
			room := newStrongholdPortalRoom(strongholdGenDepthCap, x, y, z, dir)
			if builder.FindCollisionPiece(room.BoundingBox()) != nil {
				continue
			}
			builder.AddPiece(room)
			ctx.placed[kindPortalRoom] = 1
			break
		}
	}
}
