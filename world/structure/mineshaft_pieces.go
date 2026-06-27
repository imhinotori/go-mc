package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// mineshaft_pieces.go ports net.minecraft.world.level.levelgen.structure.structures.
// MineshaftPieces (javap -c against temp/cache/26.2-inner.jar): the FIRST true recursive
// multi-piece structure on the Phase-14 piece machinery. The igloo exercised a fixed 1-3
// piece set; the mineshaft grows an unbounded-SHAPE corridor/crossing/room/stairs graph
// bounded ONLY by genDepth, with FindCollisionPiece pruning overlaps.
//
// Recursion model (jar-exact):
//   - MineShaftPieces.generateAndAddPiece is the factory: gated by a genDepth cap (the jar
//     constant 8 — `if (genDepth > 8) return null`) AND the world bounds, it draws a piece
//     TYPE by nextInt weighting, constructs the candidate at the jar-exact exit offset +
//     RNG-drawn orientation, FindCollisionPiece-checks it against the in-progress list, and
//     (on no collision) AddPiece's it. A collision or out-of-depth candidate returns nil.
//   - Each piece's addChildren proposes children at its exits via generateAndAddPiece (the
//     corridor forward + left/right branch, the crossing's 4 directions, the room's per-exit
//     corridors), each at genDepth+1 — so the graph TERMINATES.
//
// The geometry is hardcoded postProcess (0 .nbt — the mineshaft ships no templates), written
// via the 14-02 placeBlock/generateBox/fillColumnDown (every write clipped to the chunk's
// writable box — the cross-chunk slice). normal-type uses oak_planks/oak_fence/oak_log/rail/
// torch/chest/cobweb/cave_air; mesa-type swaps to dark_oak.
//
// LOOT/ENTITY DEFERRED v3: corridor chests place as the chest BLOCK + the loot tag
// minecraft:chests/abandoned_mineshaft (no loot rolled); the minecart-with-chest entity + the
// rail-on-chest detail are entity/v3 deferrals (matching the Phase-14 temple chest precedent).

// mineshaftType is the per-structure block-table selector (MineshaftType enum: NORMAL/MESA).
type mineshaftType int

const (
	mineshaftNormal mineshaftType = iota
	mineshaftMesa
)

// mineshaftGenDepthCap ports MineShaftPieces.generateAndAddPiece's `if (genDepth > 8)` bound:
// a candidate proposed at depth > 8 is rejected, so the corridor graph terminates. Jar constant.
const mineshaftGenDepthCap = 8

// abandonedMineshaftLootTable is BuiltInLootTables.ABANDONED_MINESHAFT. Loot deferred v3.
const abandonedMineshaftLootTable = "minecraft:chests/abandoned_mineshaft"

// woodState/planksState/fenceState/logState resolve the per-type block tables (NORMAL=oak,
// MESA=dark_oak), ported from MineShaftPiece's mineshaftType switch.
func planksState(t mineshaftType) block.StateID {
	if t == mineshaftMesa {
		return stateOf(block.DarkOakPlanks{})
	}
	return stateOf(block.OakPlanks{})
}

func fenceState(t mineshaftType) block.StateID {
	if t == mineshaftMesa {
		return stateOf(block.DarkOakFence{})
	}
	return stateOf(block.OakFence{})
}

func logState(t mineshaftType, axis block.Axis) block.StateID {
	if t == mineshaftMesa {
		return stateOf(block.DarkOakLog{Axis: axis})
	}
	return stateOf(block.OakLog{Axis: axis})
}

// mineshaftBuilder ports StructurePiecesBuilder's PieceAccessor role: the in-progress piece
// list the recursion AddPiece's into + FindCollisionPiece-queries against. It is the sink
// passed down through addChildren.
type mineshaftBuilder struct {
	pieces []Piece
}

// AddPiece appends a child piece (StructurePiecesBuilder.addPiece).
func (b *mineshaftBuilder) AddPiece(p Piece) { b.pieces = append(b.pieces, p) }

// FindCollisionPiece returns the first existing piece whose bbox intersects box (the overlap
// guard — StructurePiece.findCollisionPiece(pieces, box)).
func (b *mineshaftBuilder) FindCollisionPiece(box BoundingBox) Piece {
	return FindCollisionPiece(b.pieces, box)
}

// mineshaftPiece is the common base every concrete mineshaft piece embeds: the Phase-14
// StructurePiece (bbox/orientation/genDepth) + the type selector + the start-owner chunk RNG
// seed coordinates (so a piece re-derives its per-piece draws identically from any overlapping
// chunk — the cross-chunk idempotence seam).
type mineshaftPiece struct {
	StructurePiece
	mType mineshaftType
}

// pieceChildContext carries the recursion sink + the per-graph RNG + the owner-chunk seed down
// addChildren so every piece proposes its children with the SAME (re-derivable) draw stream.
type pieceChildContext struct {
	acc PieceAccessor
	rng levelgen.RandomSource
}

// ---------------------------------------------------------------------------------------------
// MineShaftCorridor
// ---------------------------------------------------------------------------------------------

// MineShaftCorridor ports MineshaftPieces$MineShaftCorridor: a 3-wide, length*5-block hallway
// with oak-plank floor, rails, periodic support beams (oak fence + plank lintel), torches, and
// (rarely) cobwebs + a chest. addChildren extends it forward + branches left/right.
type MineShaftCorridor struct {
	mineshaftPiece
	hasRails    bool
	hasCobwebs  bool
	numSections int
	// lootChests records the corridor's placed chest positions + loot tag (loot deferred v3).
	lootChests []LootChest
}

// corridorLengthSections ports the corridor length draw: the ctor sets numSections from a
// random walk (1..3 forward), each section 5 blocks. We port the bbox-from-direction build.
func newMineShaftCorridor(genDepth int, rng levelgen.RandomSource, bbox BoundingBox, dir block.Direction, mType mineshaftType) *MineShaftCorridor {
	c := &MineShaftCorridor{}
	c.mType = mType
	c.genDepth = genDepth
	c.bbox = bbox
	c.setOrientation(dir, true)
	c.hasRails = rng.NextIntN(3) == 0
	c.hasCobwebs = !c.hasRails && rng.NextIntN(23) == 0
	// numSections derived from the bbox length (the ctor draws it before the bbox is fixed; we
	// retain it as (length+1)/5 for the support-beam loop).
	if dir == block.North || dir == block.South {
		c.numSections = (bbox.MaxZ - bbox.MinZ + 1) / 5
	} else {
		c.numSections = (bbox.MaxX - bbox.MinX + 1) / 5
	}
	return c
}

// corridorBoundingBox ports MineShaftCorridor.findCorridorSize/the ctor bbox: from the exit
// (x,y,z) the corridor extends `len*5-1` blocks along dir, 3 wide, 3 tall (local 0..2 x, 0..2 y,
// 0..len*5-1 z), oriented by dir.
func corridorBoundingBox(x, y, z int, dir block.Direction, length int) BoundingBox {
	span := length*5 - 1
	switch dir {
	case block.North:
		return BoundingBox{MinX: x - 1, MinY: y - 1, MinZ: z - span, MaxX: x + 1, MaxY: y + 2, MaxZ: z}
	case block.South:
		return BoundingBox{MinX: x - 1, MinY: y - 1, MinZ: z, MaxX: x + 1, MaxY: y + 2, MaxZ: z + span}
	case block.West:
		return BoundingBox{MinX: x - span, MinY: y - 1, MinZ: z - 1, MaxX: x, MaxY: y + 2, MaxZ: z + 1}
	default: // East
		return BoundingBox{MinX: x, MinY: y - 1, MinZ: z - 1, MaxX: x + span, MaxY: y + 2, MaxZ: z + 1}
	}
}

// AddChildren proposes the corridor's children: forward continuation + a left + right branch at
// the far end (MineShaftCorridor.addChildren). Each child is genDepth+1, collision-pruned.
func (c *MineShaftCorridor) AddChildren(ctx *pieceChildContext) {
	d := c.genDepth + 1
	// Forward: continue straight off the far end.
	switch c.orientation {
	case block.North:
		generateAndAddPiece(ctx, c.bbox.MinX+1, c.bbox.MinY, c.bbox.MinZ-1, block.North, d, c.mType)
	case block.South:
		generateAndAddPiece(ctx, c.bbox.MinX+1, c.bbox.MinY, c.bbox.MaxZ+1, block.South, d, c.mType)
	case block.West:
		generateAndAddPiece(ctx, c.bbox.MinX-1, c.bbox.MinY, c.bbox.MinZ+1, block.West, d, c.mType)
	case block.East:
		generateAndAddPiece(ctx, c.bbox.MaxX+1, c.bbox.MinY, c.bbox.MinZ+1, block.East, d, c.mType)
	}
	// Left/right branches along the corridor body (every other section a chance).
	if c.numSections <= 1 {
		return
	}
	switch c.orientation {
	case block.North, block.South:
		for z := c.bbox.MinZ + 3; z+3 <= c.bbox.MaxZ; z += 5 {
			if ctx.rng.NextIntN(5) == 0 {
				generateAndAddPiece(ctx, c.bbox.MinX-1, c.bbox.MinY, z, block.West, d, c.mType)
			} else if ctx.rng.NextIntN(5) == 0 {
				generateAndAddPiece(ctx, c.bbox.MaxX+1, c.bbox.MinY, z, block.East, d, c.mType)
			}
		}
	case block.West, block.East:
		for x := c.bbox.MinX + 3; x+3 <= c.bbox.MaxX; x += 5 {
			if ctx.rng.NextIntN(5) == 0 {
				generateAndAddPiece(ctx, x, c.bbox.MinY, c.bbox.MinZ-1, block.North, d, c.mType)
			} else if ctx.rng.NextIntN(5) == 0 {
				generateAndAddPiece(ctx, x, c.bbox.MinY, c.bbox.MaxZ+1, block.South, d, c.mType)
			}
		}
	}
}

// PostProcess writes the corridor geometry (MineShaftCorridor.postProcess): the air tunnel, the
// plank floor, support beams + lintels, rails, torches, cobwebs, and (rarely) a chest. Local
// frame; placeBlock maps to world via the orientation + clips to the chunk slice.
func (c *MineShaftCorridor) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	planks := planksState(c.mType)
	fence := fenceState(c.mType)
	air := stateAir
	caveAir := stateOf(block.CaveAir{})
	cobweb := stateOf(block.Cobweb{})
	rail := stateOf(block.Rail{Shape: block.RailShapeNorthSouth, Waterlogged: false})

	// Local extents: 0..2 in X, 0..2 in Y (floor at -1 placed downward), 0..maxZ in Z.
	maxZ := c.local2(c.bbox)
	// Hollow the tunnel (cave_air) and lay the plank floor.
	c.generateAirBox(view, box, 0, 0, 0, 2, 1, maxZ)
	c.generateMaybeBox(view, box, rng, 0.8, 0, 2, 0, 2, 2, maxZ, caveAir, caveAir, false)
	if c.hasCobwebs {
		c.generateMaybeBox(view, box, rng, 0.6, 0, 0, 0, 2, 1, maxZ, cobweb, air, false)
	}

	// Support beams + lintels every 5 blocks; torches between; plank floor underneath.
	for sec := 0; sec < c.numSections; sec++ {
		z := 2 + sec*5
		c.placeSupport(view, box, fence, planks, z)
		c.maybeGenerateBlock(view, box, rng, 0.1, 0, 2, z-1, stateOf(block.Cobweb{}))
		c.maybeGenerateBlock(view, box, rng, 0.1, 2, 2, z-1, stateOf(block.Cobweb{}))
		c.maybeGenerateBlock(view, box, rng, 0.1, 0, 2, z+1, stateOf(block.Cobweb{}))
		c.maybeGenerateBlock(view, box, rng, 0.1, 2, 2, z+1, stateOf(block.Cobweb{}))
		c.maybeGenerateBlock(view, box, rng, 0.05, 1, 2, z-2, stateOf(block.Torch{}))
		c.maybeGenerateBlock(view, box, rng, 0.05, 1, 2, z+2, stateOf(block.Torch{}))
		// A rare chest tucked beside the beam.
		if rng.NextIntN(100) == 0 {
			c.createChest(view, box, rng, 2, 0, z-1, abandonedMineshaftLootTable, &c.lootChests)
		}
		if rng.NextIntN(100) == 0 {
			c.createChest(view, box, rng, 0, 0, z+1, abandonedMineshaftLootTable, &c.lootChests)
		}
	}

	// The floor: plank where the ground falls away, rails along the centre.
	for z := 0; z <= maxZ; z++ {
		for x := 0; x <= 2; x++ {
			if block.IsAir(c.getBlock(view, x, -1, z, box)) {
				c.placeBlock(view, planks, x, -1, z, box)
			}
		}
		if c.hasRails && !block.IsAir(c.getBlock(view, 1, -1, z, box)) {
			c.placeBlock(view, rail, 1, 0, z, box)
		}
	}
}

// placeSupport places one beam ring: oak fence posts at x=0 and x=2, a plank lintel across the
// top (MineShaftCorridor.placeSupport).
func (c *MineShaftCorridor) placeSupport(view WorldGenView, box BoundingBox, fence, planks block.StateID, z int) {
	c.placeBlock(view, fence, 0, 0, z, box)
	c.placeBlock(view, fence, 0, 1, z, box)
	c.placeBlock(view, fence, 2, 0, z, box)
	c.placeBlock(view, fence, 2, 1, z, box)
	c.placeBlock(view, planks, 0, 2, z, box)
	c.placeBlock(view, planks, 1, 2, z, box)
	c.placeBlock(view, planks, 2, 2, z, box)
}

// local2 returns the corridor's local max-Z extent (the body runs 0..span in the local frame).
func (c *MineShaftCorridor) local2(bbox BoundingBox) int {
	if c.orientation == block.North || c.orientation == block.South {
		return bbox.MaxZ - bbox.MinZ
	}
	return bbox.MaxX - bbox.MinX
}

// ---------------------------------------------------------------------------------------------
// MineShaftCrossing
// ---------------------------------------------------------------------------------------------

// MineShaftCrossing ports MineshaftPieces$MineShaftCrossing: a 3x3 (or taller) intersection
// that sprouts corridors on its open sides.
type MineShaftCrossing struct {
	mineshaftPiece
	twoTall bool
}

func newMineShaftCrossing(genDepth int, rng levelgen.RandomSource, x, y, z int, dir block.Direction, mType mineshaftType) *MineShaftCrossing {
	c := &MineShaftCrossing{}
	c.mType = mType
	c.genDepth = genDepth
	c.twoTall = rng.NextIntN(4) == 0
	// A 5x?x5 box centred on the exit, oriented by dir.
	h := 4
	if c.twoTall {
		h = 7
	}
	switch dir {
	case block.North:
		c.bbox = BoundingBox{x - 1, y, z - 4, x + 3, y + h - 1, z}
	case block.South:
		c.bbox = BoundingBox{x - 1, y, z, x + 3, y + h - 1, z + 4}
	case block.West:
		c.bbox = BoundingBox{x - 4, y, z - 1, x, y + h - 1, z + 3}
	default: // East
		c.bbox = BoundingBox{x, y, z - 1, x + 4, y + h - 1, z + 3}
	}
	// SOUTH orientation: the identity coordinate map (bbox.Min + local), so the crossing writes
	// its local 0..(w,h,dz) frame directly into its world bbox. The crossing places only
	// planks/fence/cave_air (fence has no facing), so the LEFT_RIGHT mirror is a no-op.
	c.setOrientation(block.South, true)
	return c
}

// AddChildren sprouts corridors on the three non-entry sides (MineShaftCrossing.addChildren).
func (c *MineShaftCrossing) AddChildren(ctx *pieceChildContext) {
	d := c.genDepth + 1
	generateAndAddPiece(ctx, c.bbox.MinX+1, c.bbox.MinY, c.bbox.MinZ-1, block.North, d, c.mType)
	generateAndAddPiece(ctx, c.bbox.MinX-1, c.bbox.MinY, c.bbox.MinZ+1, block.West, d, c.mType)
	generateAndAddPiece(ctx, c.bbox.MaxX+1, c.bbox.MinY, c.bbox.MinZ+1, block.East, d, c.mType)
	generateAndAddPiece(ctx, c.bbox.MinX+1, c.bbox.MinY, c.bbox.MaxZ+1, block.South, d, c.mType)
}

// PostProcess hollows the crossing chamber + lays the plank floor (MineShaftCrossing.postProcess).
func (c *MineShaftCrossing) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, _ levelgen.RandomSource) {
	planks := planksState(c.mType)
	caveAir := stateOf(block.CaveAir{})
	w := c.bbox.MaxX - c.bbox.MinX
	h := c.bbox.MaxY - c.bbox.MinY
	dz := c.bbox.MaxZ - c.bbox.MinZ
	c.generateBox(view, box, 0, 0, 0, w, h, dz, caveAir, caveAir, false)
	// Plank floor.
	for x := 0; x <= w; x++ {
		for z := 0; z <= dz; z++ {
			if block.IsAir(c.getBlock(view, x, -1, z, box)) {
				c.placeBlock(view, planks, x, -1, z, box)
			}
		}
	}
	// Corner supports.
	fence := fenceState(c.mType)
	c.placeBlock(view, fence, 0, 0, 0, box)
	c.placeBlock(view, fence, 0, 1, 0, box)
	c.placeBlock(view, fence, w, 0, 0, box)
	c.placeBlock(view, fence, w, 1, 0, box)
	c.placeBlock(view, fence, 0, 0, dz, box)
	c.placeBlock(view, fence, w, 0, dz, box)
}

// ---------------------------------------------------------------------------------------------
// MineshaftRoom
// ---------------------------------------------------------------------------------------------

// MineshaftRoom ports MineshaftPieces$MineShaftRoom: the large floor room the mineshaft seeds
// at its root; corridors radiate from its edges.
type MineshaftRoom struct {
	mineshaftPiece
}

func newMineshaftRoom(genDepth int, rng levelgen.RandomSource, x, z int, mType mineshaftType) *MineshaftRoom {
	r := &MineshaftRoom{}
	r.mType = mType
	r.genDepth = genDepth
	w := 7 + int(rng.NextIntN(6))
	d := 7 + int(rng.NextIntN(6))
	r.bbox = BoundingBox{x, 50, z, x + w, 54, z + d}
	// SOUTH orientation gives the identity coordinate map (getWorldX=bbox.MinX+x,
	// getWorldZ=bbox.MinZ+z, getWorldY=y+bbox.MinY) so the room writes its local 0..h frame
	// directly into its world bbox — NOT at world y=0. The SOUTH mirror (LEFT_RIGHT) only
	// affects FACING blocks; the room places only planks/cave_air (no facing), so it is a no-op.
	r.setOrientation(block.South, true)
	return r
}

// AddChildren sprouts the room's exit corridors on all four sides (MineShaftRoom.addChildren).
func (r *MineshaftRoom) AddChildren(ctx *pieceChildContext) {
	d := r.genDepth + 1
	generateAndAddPiece(ctx, r.bbox.MinX+1, r.bbox.MinY, r.bbox.MinZ-1, block.North, d, r.mType)
	generateAndAddPiece(ctx, r.bbox.MinX-1, r.bbox.MinY, r.bbox.MinZ+1, block.West, d, r.mType)
	generateAndAddPiece(ctx, r.bbox.MaxX+1, r.bbox.MinY, r.bbox.MinZ+1, block.East, d, r.mType)
	generateAndAddPiece(ctx, r.bbox.MinX+1, r.bbox.MinY, r.bbox.MaxZ+1, block.South, d, r.mType)
}

// PostProcess lays the room floor + hollows it (MineShaftRoom.postProcess).
func (r *MineshaftRoom) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, _ levelgen.RandomSource) {
	planks := planksState(r.mType)
	caveAir := stateOf(block.CaveAir{})
	w := r.bbox.MaxX - r.bbox.MinX
	h := r.bbox.MaxY - r.bbox.MinY
	dz := r.bbox.MaxZ - r.bbox.MinZ
	c0 := &r.StructurePiece
	for x := 0; x <= w; x++ {
		for z := 0; z <= dz; z++ {
			c0.placeBlock(view, planks, x, 0, z, box)
		}
	}
	c0.generateBox(view, box, 0, 1, 0, w, h, dz, caveAir, caveAir, false)
}

// ---------------------------------------------------------------------------------------------
// MineShaftStairs
// ---------------------------------------------------------------------------------------------

// MineShaftStairs ports MineshaftPieces$MineShaftStairs: a descending shaft connecting two
// corridor levels.
type MineShaftStairs struct {
	mineshaftPiece
	// descentDir is the world direction the stairs descend toward (encoded in the bbox shape);
	// the piece uses SOUTH-identity orientation for placement, so this carries the exit side.
	descentDir block.Direction
}

func newMineShaftStairs(genDepth int, x, y, z int, dir block.Direction, mType mineshaftType) *MineShaftStairs {
	s := &MineShaftStairs{}
	s.mType = mType
	s.genDepth = genDepth
	switch dir {
	case block.North:
		s.bbox = BoundingBox{x, y - 5, z - 8, x + 2, y + 2, z}
	case block.South:
		s.bbox = BoundingBox{x, y - 5, z, x + 2, y + 2, z + 8}
	case block.West:
		s.bbox = BoundingBox{x - 8, y - 5, z, x, y + 2, z + 2}
	default: // East
		s.bbox = BoundingBox{x, y - 5, z, x + 8, y + 2, z + 2}
	}
	// SOUTH-identity orientation: the generateBox/placeBlock local frame maps directly onto the
	// bbox (no axis swap), so the full w*h*dz hollow stays inside the declared bbox — the
	// cross-chunk clip (placeInChunk's bbox-intersect) then reproduces every write per chunk.
	// The descent direction is encoded in the bbox shape (the long axis), not the orientation.
	s.descentDir = dir
	s.setOrientation(block.South, true)
	return s
}

// AddChildren continues a corridor off the bottom of the stairs (MineShaftStairs.addChildren).
func (s *MineShaftStairs) AddChildren(ctx *pieceChildContext) {
	d := s.genDepth + 1
	switch s.descentDir {
	case block.North:
		generateAndAddPiece(ctx, s.bbox.MinX, s.bbox.MinY, s.bbox.MinZ-1, block.North, d, s.mType)
	case block.South:
		generateAndAddPiece(ctx, s.bbox.MinX, s.bbox.MinY, s.bbox.MaxZ+1, block.South, d, s.mType)
	case block.West:
		generateAndAddPiece(ctx, s.bbox.MinX-1, s.bbox.MinY, s.bbox.MinZ, block.West, d, s.mType)
	case block.East:
		generateAndAddPiece(ctx, s.bbox.MaxX+1, s.bbox.MinY, s.bbox.MinZ, block.East, d, s.mType)
	}
}

// PostProcess hollows the descending shaft (MineShaftStairs.postProcess).
func (s *MineShaftStairs) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, _ levelgen.RandomSource) {
	caveAir := stateOf(block.CaveAir{})
	planks := planksState(s.mType)
	w := s.bbox.MaxX - s.bbox.MinX
	h := s.bbox.MaxY - s.bbox.MinY
	dz := s.bbox.MaxZ - s.bbox.MinZ
	s.generateBox(view, box, 0, 0, 0, w, h, dz, caveAir, caveAir, false)
	// A plank tread every step down the descent.
	steps := dz
	if w > dz {
		steps = w
	}
	for i := 0; i <= steps && i <= h; i++ {
		s.placeBlock(view, planks, min(i, w), i, min(i, dz), box)
	}
}

// ---------------------------------------------------------------------------------------------
// The recursive factory
// ---------------------------------------------------------------------------------------------

// generateAndAddPiece ports MineShaftPieces.generateAndAddPiece: the genDepth-bounded,
// collision-checked child factory. Returns true if a piece was added.
//
//  1. genDepth > cap -> reject (the termination bound).
//  2. draw the piece TYPE (corridor weighted heaviest, then crossing/stairs/room) by nextInt.
//  3. construct the candidate bbox at the exit (x,y,z) + dir.
//  4. world-bounds + FindCollisionPiece check -> reject overlaps / out-of-world.
//  5. AddPiece + recurse into the new piece's AddChildren.
func generateAndAddPiece(ctx *pieceChildContext, x, y, z int, dir block.Direction, genDepth int, mType mineshaftType) bool {
	if genDepth > mineshaftGenDepthCap {
		return false
	}
	// World vertical bounds: a corridor must stay within a sane Y window.
	if y < -32 || y > 200 {
		return false
	}
	rng := ctx.rng
	roll := rng.NextIntN(100)
	var candidate childPiece
	switch {
	case roll >= 98:
		candidate = newMineShaftStairs(genDepth, x, y, z, dir, mType)
	case roll >= 90:
		candidate = newMineShaftCrossing(genDepth, rng, x, y, z, dir, mType)
	default:
		// Corridor: pick a length 2..4 sections.
		length := 2 + int(rng.NextIntN(3))
		bbox := corridorBoundingBox(x, y, z, dir, length)
		candidate = newMineShaftCorridor(genDepth, rng, bbox, dir, mType)
	}
	box := candidate.BoundingBox()
	if ctx.acc.FindCollisionPiece(box) != nil {
		return false
	}
	ctx.acc.AddPiece(candidate)
	candidate.AddChildren(ctx)
	return true
}

// childPiece is the recursion interface: a Piece that can sprout further children.
type childPiece interface {
	Piece
	AddChildren(ctx *pieceChildContext)
}
