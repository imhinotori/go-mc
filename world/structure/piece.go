package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// WorldGenView is the cross-chunk block read/write proxy a piece's PostProcess writes
// through. *world.Neighborhood (package world) satisfies it STRUCTURALLY (SetBlock/
// GetBlock with the exact signatures), so PostProcess takes this interface rather than
// *world.Neighborhood — the dependency points world -> world/structure ONLY (the cache
// already lives here), with NO import cycle (T-14-10). Writes are clipped to the 3x3
// neighborhood by the Neighborhood itself (blockStateWriteRadius=1) AND to the piece
// bbox + the chunk writable box by placeBlock's IsInside guard.
type WorldGenView interface {
	// SetBlock writes a block state at world (wx,wy,wz). Out-of-window writes are dropped.
	SetBlock(wx, wy, wz int, st block.StateID)
	// GetBlock returns the block state at world (wx,wy,wz); out-of-window reads return air.
	GetBlock(wx, wy, wz int) block.StateID
	// SetBlockEntity records a loot-bearing block entity (a structure chest) at world
	// (wx,wy,wz): the block-entity type plus the {LootTable, LootTableSeed} the chest rolls
	// LAZILY on first open (Pitfall 2 — never at gen). lootTable is the loot-table id
	// (e.g. "minecraft:chests/simple_dungeon"); lootSeed is the piece-RNG nextLong() draw.
	// Out-of-window writes are dropped (same clip as SetBlock). 20-02 Task 2 (the chest BE
	// carrier — RandomizableContainerBlockEntity's LootTable/LootTableSeed NBT keys).
	SetBlockEntity(wx, wy, wz int, typ block.EntityType, lootTable string, lootSeed int64)
	// SetSpawner records a mob_spawner BLOCK-ENTITY at world (wx,wy,wz) set to spawn entityID
	// (e.g. "minecraft:silverfish") — the stronghold's silverfish trap. This is a BLOCK, NOT a
	// live entity (Pitfall 5): the spawner block-entity carries SpawnData.entity.id, mirroring
	// BaseSpawner.setEntityId (jar). Out-of-window writes are dropped (same clip as SetBlock).
	// The SPAWNER block itself is placed by a preceding SetBlock; SetSpawner adds the BE. 20-04.
	SetSpawner(wx, wy, wz int, entityID string)
	// RecordSpawn buffers a structure-inhabitant spawn REQUEST (a witch/cat/villager) for the
	// tick to drain onto the entity store — it NEVER spawns a live entity off-tick (TICK-05 /
	// Pitfall 5). The recorder (the world.Neighborhood) appends the request; placeStructures
	// forwards the buffer onto the emitted ChunkResult.Spawns, and the tick performs the only
	// store add. STRUCT-POLISH-02. The silverfish SPAWNER is NOT recorded here — it is a block
	// (SetBlock + SetSpawner). 20-04 (the spawn seam, alongside 20-02's SetBlockEntity).
	RecordSpawn(req SpawnRequest)
}

// Rotation ports net.minecraft.world.level.block.Rotation: a horizontal rotation applied
// to a placed block state's FACING. Ordinals match the jar ($values bootstrap):
// NONE=0, CLOCKWISE_90=1, CLOCKWISE_180=2, COUNTERCLOCKWISE_90=3.
//
// Source: javap -c / CFR net.minecraft.world.level.block.Rotation.
type Rotation int

const (
	RotNone Rotation = iota
	RotClockwise90
	RotClockwise180
	RotCounterclockwise90
)

// Mirror ports net.minecraft.world.level.block.Mirror: NONE / LEFT_RIGHT (mirror Z-axis
// facings) / FRONT_BACK (mirror X-axis facings).
//
// Source: CFR net.minecraft.world.level.block.Mirror.
type Mirror int

const (
	MirrorNone Mirror = iota
	MirrorLeftRight
	MirrorFrontBack
)

// rotateDirection ports Rotation.rotate(Direction): Y-axis directions are unchanged;
// horizontal directions step around the compass. CFR-exact:
//
//	CLOCKWISE_180        -> getOpposite
//	COUNTERCLOCKWISE_90  -> getCounterClockWise
//	CLOCKWISE_90         -> getClockWise
//	NONE                 -> direction
func (r Rotation) rotateDirection(d block.Direction) block.Direction {
	if d == block.Up || d == block.Down {
		return d
	}
	switch r {
	case RotClockwise180:
		return directionOpposite(d)
	case RotCounterclockwise90:
		return directionCounterClockWise(d)
	case RotClockwise90:
		return directionClockWise(d)
	default:
		return d
	}
}

// mirrorDirection ports Mirror.mirror(Direction): FRONT_BACK flips X-axis facings,
// LEFT_RIGHT flips Z-axis facings, all else unchanged.
func (m Mirror) mirrorDirection(d block.Direction) block.Direction {
	switch m {
	case MirrorFrontBack:
		if d == block.West || d == block.East {
			return directionOpposite(d)
		}
	case MirrorLeftRight:
		if d == block.North || d == block.South {
			return directionOpposite(d)
		}
	}
	return d
}

// directionOpposite/ClockWise/CounterClockWise port net.minecraft.core.Direction's
// horizontal-compass helpers. The horizontal cycle is N -> E -> S -> W -> N (clockwise).
// Verticals are returned unchanged (the structure pieces only carry horizontal facings).
func directionOpposite(d block.Direction) block.Direction {
	switch d {
	case block.North:
		return block.South
	case block.South:
		return block.North
	case block.West:
		return block.East
	case block.East:
		return block.West
	case block.Up:
		return block.Down
	case block.Down:
		return block.Up
	}
	return d
}

func directionClockWise(d block.Direction) block.Direction {
	switch d {
	case block.North:
		return block.East
	case block.East:
		return block.South
	case block.South:
		return block.West
	case block.West:
		return block.North
	}
	return d
}

func directionCounterClockWise(d block.Direction) block.Direction {
	switch d {
	case block.North:
		return block.West
	case block.West:
		return block.South
	case block.South:
		return block.East
	case block.East:
		return block.North
	}
	return d
}

// get2DDataValue ports Direction.get2DDataValue (the data2d field): SOUTH=0, WEST=1,
// NORTH=2, EAST=3 — the index into Plane.HORIZONTAL's per-direction arrays (the desert
// pyramid's hasPlacedChest[] is indexed by it).
func get2DDataValue(d block.Direction) int {
	switch d {
	case block.South:
		return 0
	case block.West:
		return 1
	case block.North:
		return 2
	case block.East:
		return 3
	}
	return 0
}

// horizontalPlane ports Direction.Plane.HORIZONTAL's faces array (the iteration +
// getRandomHorizontalDirection order): [NORTH, EAST, SOUTH, WEST].
var horizontalPlane = [4]block.Direction{block.North, block.East, block.South, block.West}

// getRandomHorizontalDirection ports StructurePiece.getRandomHorizontalDirection =
// Plane.HORIZONTAL.getRandomDirection(random) = faces[random.nextInt(4)] (Util.getRandom).
// This is the orientation draw the desert pyramid piece makes at construction — a
// load-bearing RNG draw (it advances the piece RNG stream before the height-offset draw).
func getRandomHorizontalDirection(rng levelgen.RandomSource) block.Direction {
	return horizontalPlane[rng.NextIntN(4)]
}

// transformState applies a piece's mirror THEN rotation to a placed block state's FACING,
// mirroring placeBlock's order (mirror first, then rotate — StructurePiece.placeBlock).
// Blocks without a Facing prop (sandstone, terracotta, tnt, ...) are returned unchanged.
// Stairs/chest carry a Facing; the desert pyramid only ever places STRAIGHT stairs, whose
// SHAPE is rotation/mirror-invariant, so transforming FACING alone is jar-faithful here
// (StairBlock.rotate sets FACING=rotation.rotate(FACING); StairBlock.mirror on a STRAIGHT
// shape == rotate(CLOCKWISE_180) i.e. FACING=opposite, matching mirrorDirection on the
// stair's facing axis). NONE/NONE (orientation NORTH) is the identity path.
//
// Source: CFR StructurePiece.placeBlock + StairBlock.rotate/mirror.
func transformState(st block.StateID, mirror Mirror, rotation Rotation) block.StateID {
	if mirror == MirrorNone && rotation == RotNone {
		return st
	}
	if st < 0 || int(st) >= len(block.StateList) {
		return st
	}
	b := block.StateList[st]

	// Stairs carry a SHAPE that StairBlock.mirror transforms (inner/outer left<->right + a
	// 180 facing flip on the matching axis) — FACING-only is NOT enough for L-shaped stairs.
	// The jar applies mirror THEN rotate to the whole state; do the same for stairs.
	if sb, ok := stairOf(b); ok {
		sb = mirrorStair(sb, mirror)
		sb = rotateStair(sb, rotation)
		if id, ok := block.ToStateID[withStair(b, sb)]; ok {
			return id
		}
		return st
	}

	// Multi-face blocks (vine, redstone wire, tripwire) carry per-compass-direction props
	// (N/E/S/W) that their jar rotate/mirror permute around the compass. Port that permutation.
	if nb, ok := transformFaces(b, mirror, rotation); ok {
		if id, ok := block.ToStateID[nb]; ok {
			return id
		}
		return st
	}

	// Generic horizontal-facing blocks (chest, dispenser, lever, piston, tripwire-hook,
	// repeater): mirror flips the facing on its axis, rotate steps it. Up/Down facings (the
	// upward sticky piston) are axis-invariant under the horizontal mirror/rotate.
	facing, ok := facingOf(b)
	if !ok {
		return st // no Facing prop -> rotation/mirror is the identity (BlockBehaviour default)
	}
	if mirror != MirrorNone {
		facing = mirror.mirrorDirection(facing)
	}
	if rotation != RotNone {
		facing = rotation.rotateDirection(facing)
	}
	nb := withFacing(b, facing)
	if id, ok := block.ToStateID[nb]; ok {
		return id
	}
	return st
}

// transformFaces ports the rotate/mirror of the multi-compass-face blocks the jungle temple
// places (Vine, RedstoneWire, Tripwire). Each carries an independent value per compass
// direction (a Boolean for vine/tripwire, a RedstoneSide for wire); rotate/mirror PERMUTE the
// (N,E,S,W) slots around the compass, leaving any non-directional prop (Up, attached, power)
// in place. The mirror is applied first (matching placeBlock's state.mirror().rotate()).
//
// The face permutation (from javap -c VineBlock/RedStoneWireBlock/TripWireBlock rotate/mirror):
//
//	CW90  : new.N=old.E, new.E=old.S, new.S=old.W, new.W=old.N
//	CW180 : new.N=old.S, new.E=old.W, new.S=old.N, new.W=old.E
//	CCW90 : new.N=old.W, new.E=old.N, new.S=old.E, new.W=old.S
//	LEFT_RIGHT : swap N<->S
//	FRONT_BACK : swap E<->W
//
// Returns (transformed, true) for those blocks, (b, false) otherwise.
func transformFaces(b block.Block, mirror Mirror, rotation Rotation) (block.Block, bool) {
	switch v := b.(type) {
	case block.Vine:
		n, e, s, w := boolFaces(bool(v.North), bool(v.East), bool(v.South), bool(v.West), mirror, rotation)
		v.North, v.East, v.South, v.West = block.Boolean(n), block.Boolean(e), block.Boolean(s), block.Boolean(w)
		return v, true
	case block.Tripwire:
		n, e, s, w := boolFaces(bool(v.North), bool(v.East), bool(v.South), bool(v.West), mirror, rotation)
		v.North, v.East, v.South, v.West = block.Boolean(n), block.Boolean(e), block.Boolean(s), block.Boolean(w)
		return v, true
	case block.RedstoneWire:
		n, e, s, w := sideFaces(v.North, v.East, v.South, v.West, mirror, rotation)
		v.North, v.East, v.South, v.West = n, e, s, w
		return v, true
	}
	return b, false
}

// permuteFaces applies the mirror-then-rotate compass permutation to 4 generic face values
// (indexed [N,E,S,W]=[0,1,2,3]) and returns the new [N,E,S,W].
func permuteFaces(n, e, s, w any, mirror Mirror, rotation Rotation) (any, any, any, any) {
	// Mirror first.
	switch mirror {
	case MirrorLeftRight:
		n, s = s, n
	case MirrorFrontBack:
		e, w = w, e
	}
	// Then rotate (new value at a slot = old value of the slot it rotated FROM).
	switch rotation {
	case RotClockwise90:
		return e, s, w, n
	case RotClockwise180:
		return s, w, n, e
	case RotCounterclockwise90:
		return w, n, e, s
	default:
		return n, e, s, w
	}
}

func boolFaces(n, e, s, w bool, mirror Mirror, rotation Rotation) (bool, bool, bool, bool) {
	an, ae, as, aw := permuteFaces(n, e, s, w, mirror, rotation)
	return an.(bool), ae.(bool), as.(bool), aw.(bool)
}

func sideFaces(n, e, s, w block.RedstoneSide, mirror Mirror, rotation Rotation) (block.RedstoneSide, block.RedstoneSide, block.RedstoneSide, block.RedstoneSide) {
	an, ae, as, aw := permuteFaces(n, e, s, w, mirror, rotation)
	return an.(block.RedstoneSide), ae.(block.RedstoneSide), as.(block.RedstoneSide), aw.(block.RedstoneSide)
}

// stairState is the (facing, shape) pair a stair-shape transform operates on.
type stairState struct {
	facing block.Direction
	shape  block.StairsShape
}

// rotateStair ports StairBlock.rotate: FACING = rotation.rotate(FACING); SHAPE unchanged.
func rotateStair(s stairState, rotation Rotation) stairState {
	if rotation != RotNone {
		s.facing = rotation.rotateDirection(s.facing)
	}
	return s
}

// mirrorStair ports StairBlock.mirror: if the FACING axis matches the mirror's flip axis
// (LEFT_RIGHT flips Z-axis facings, FRONT_BACK flips X-axis facings) then apply a
// CLOCKWISE_180 facing flip and swap the L-shape's handedness per the jar switch:
//
//	LEFT_RIGHT (facing axis Z) / FRONT_BACK (facing axis X):
//	  INNER_LEFT -> INNER_RIGHT (LR) | INNER_RIGHT (FB swaps the other way) ...
//
// The jar's StairBlock.mirror table (canonical): for the matching axis,
//
//	LEFT_RIGHT:  STRAIGHT->180 only; INNER_LEFT<->INNER_RIGHT swapped to RIGHT/LEFT; OUTER ditto
//	FRONT_BACK:  the opposite handedness swap.
//
// We port the exact jar table below.
func mirrorStair(s stairState, mirror Mirror) stairState {
	if mirror == MirrorNone {
		return s
	}
	axisZ := s.facing == block.North || s.facing == block.South
	axisX := s.facing == block.West || s.facing == block.East
	switch mirror {
	case MirrorLeftRight:
		if !axisZ {
			return s // facing axis X: LEFT_RIGHT does not affect it
		}
		s.facing = directionOpposite(s.facing) // CLOCKWISE_180 on a horizontal facing
		switch s.shape {
		case block.StairsShapeInnerLeft:
			s.shape = block.StairsShapeInnerRight
		case block.StairsShapeInnerRight:
			s.shape = block.StairsShapeInnerLeft
		case block.StairsShapeOuterLeft:
			s.shape = block.StairsShapeOuterRight
		case block.StairsShapeOuterRight:
			s.shape = block.StairsShapeOuterLeft
		}
	case MirrorFrontBack:
		if !axisX {
			return s // facing axis Z: FRONT_BACK does not affect it
		}
		s.facing = directionOpposite(s.facing)
		switch s.shape {
		case block.StairsShapeInnerLeft:
			s.shape = block.StairsShapeInnerRight
		case block.StairsShapeInnerRight:
			s.shape = block.StairsShapeInnerLeft
		case block.StairsShapeOuterLeft:
			s.shape = block.StairsShapeOuterRight
		case block.StairsShapeOuterRight:
			s.shape = block.StairsShapeOuterLeft
		}
	}
	return s
}

// stairOf returns a stair block's (facing, shape) if b is a stair the pieces place
// (sandstone/cobblestone/spruce). Routed through the full StairBlock mirror/rotate transform
// (which also flips SHAPE), unlike the generic facing-only path.
func stairOf(b block.Block) (stairState, bool) {
	switch v := b.(type) {
	case block.SandstoneStairs:
		return stairState{v.Facing, v.Shape}, true
	case block.CobblestoneStairs:
		return stairState{v.Facing, v.Shape}, true
	case block.SpruceStairs:
		return stairState{v.Facing, v.Shape}, true
	case block.StoneBrickStairs:
		return stairState{v.Facing, v.Shape}, true
	}
	return stairState{}, false
}

// withStair returns a copy of b with its (facing, shape) replaced (the inverse of stairOf).
// Half + Waterlogged are preserved (the pieces place BOTTOM, non-waterlogged stairs).
func withStair(b block.Block, s stairState) block.Block {
	switch v := b.(type) {
	case block.SandstoneStairs:
		v.Facing, v.Shape = s.facing, s.shape
		return v
	case block.CobblestoneStairs:
		v.Facing, v.Shape = s.facing, s.shape
		return v
	case block.SpruceStairs:
		v.Facing, v.Shape = s.facing, s.shape
		return v
	case block.StoneBrickStairs:
		v.Facing, v.Shape = s.facing, s.shape
		return v
	}
	return b
}

// facingOf returns a block's horizontal Facing prop if it carries one (chest, dispenser,
// lever, piston, tripwire-hook, repeater, furnace, ladder). Implemented as an explicit type
// switch (NOT reflection) over the blocks the structure pieces place, so the transform is
// allocation-free + exact. Stairs are handled separately by stairOf (they need a SHAPE flip).
func facingOf(b block.Block) (block.Direction, bool) {
	switch v := b.(type) {
	case block.Chest:
		return v.Facing, true
	case block.Dispenser:
		return v.Facing, true
	case block.Lever:
		return v.Facing, true
	case block.StickyPiston:
		return v.Facing, true
	case block.TripwireHook:
		return v.Facing, true
	case block.Repeater:
		return v.Facing, true
	case block.Furnace:
		return v.Facing, true
	case block.Ladder:
		return v.Facing, true
	}
	return block.North, false
}

// withFacing returns a copy of b with its Facing prop set to dir (the inverse of facingOf).
func withFacing(b block.Block, dir block.Direction) block.Block {
	switch v := b.(type) {
	case block.Chest:
		v.Facing = dir
		return v
	case block.Dispenser:
		v.Facing = dir
		return v
	case block.Lever:
		v.Facing = dir
		return v
	case block.StickyPiston:
		v.Facing = dir
		return v
	case block.TripwireHook:
		v.Facing = dir
		return v
	case block.Repeater:
		v.Facing = dir
		return v
	case block.Furnace:
		v.Facing = dir
		return v
	case block.Ladder:
		v.Facing = dir
		return v
	}
	return b
}

// StructurePiece is the ported base net.minecraft.world.level.levelgen.structure.
// StructurePiece: the world-block bbox, the orientation-derived rotation/mirror (set via
// setOrientation), and genDepth. Concrete pieces (DesertPyramidPiece) embed it and use its
// block helpers (placeBlock/generateBox/fillColumnDown/createChest), which clip every write
// to the piece bbox AND the per-chunk writable box (the cross-chunk mechanism, Pitfall #2).
//
// Source: javap -c / CFR net.minecraft.world.level.levelgen.structure.StructurePiece.
type StructurePiece struct {
	bbox        BoundingBox
	orientation block.Direction
	hasOrient   bool
	rotation    Rotation
	mirror      Mirror
	genDepth    int
}

// BoundingBox returns the piece's world-block AABB (satisfies the Piece interface).
func (p *StructurePiece) BoundingBox() BoundingBox { return p.bbox }

// GenDepth returns the piece's generation depth (the addChildren recursion bound).
func (p *StructurePiece) GenDepth() int { return p.genDepth }

// Move shifts the piece bbox by (dx,dy,dz) — ScatteredFeaturePiece's height adjustment
// (StructurePiece.move) re-anchors the whole pyramid to terrain height.
func (p *StructurePiece) Move(dx, dy, dz int) {
	p.bbox.MinX += dx
	p.bbox.MinY += dy
	p.bbox.MinZ += dz
	p.bbox.MaxX += dx
	p.bbox.MaxY += dy
	p.bbox.MaxZ += dz
}

// setOrientation ports StructurePiece.setOrientation: deriving (rotation, mirror) from the
// piece orientation. NONE/NORTH -> identity; SOUTH -> mirror LEFT_RIGHT; WEST -> mirror
// LEFT_RIGHT + rotate CW90; EAST -> rotate CW90.
func (p *StructurePiece) setOrientation(dir block.Direction, has bool) {
	p.hasOrient = has
	p.orientation = dir
	if !has {
		p.rotation = RotNone
		p.mirror = MirrorNone
		return
	}
	switch dir {
	case block.South:
		p.mirror = MirrorLeftRight
		p.rotation = RotNone
	case block.West:
		p.mirror = MirrorLeftRight
		p.rotation = RotClockwise90
	case block.East:
		p.mirror = MirrorNone
		p.rotation = RotClockwise90
	default: // NORTH
		p.mirror = MirrorNone
		p.rotation = RotNone
	}
}

// getWorldX/getWorldY/getWorldZ port StructurePiece.getWorldX/Y/Z: map the piece-LOCAL
// (x,y,z) to WORLD coords via the orientation. With no orientation the local coords are
// returned unchanged (the bbox is already in world coords); with an orientation the bbox
// min/max + the orientation swap/flip place the local frame into the world.
//
// Source: CFR StructurePiece.getWorldX/getWorldY/getWorldZ (the switch on orientation).
func (p *StructurePiece) getWorldX(x, z int) int {
	if !p.hasOrient {
		return x
	}
	switch p.orientation {
	case block.North, block.South:
		return p.bbox.MinX + x
	case block.West:
		return p.bbox.MaxX - z
	case block.East:
		return p.bbox.MinX + z
	default:
		return x
	}
}

func (p *StructurePiece) getWorldY(y int) int {
	if !p.hasOrient {
		return y
	}
	return y + p.bbox.MinY
}

func (p *StructurePiece) getWorldZ(x, z int) int {
	if !p.hasOrient {
		return z
	}
	switch p.orientation {
	case block.North:
		return p.bbox.MaxZ - z
	case block.South:
		return p.bbox.MinZ + z
	case block.West, block.East:
		return p.bbox.MinZ + x
	default:
		return z
	}
}

// placeBlock ports StructurePiece.placeBlock — the cross-chunk clip (Pitfall #2, jar-exact):
//
//  1. getWorldPos(x,y,z): local -> world via the orientation.
//  2. if !box.IsInside(worldPos): the write is DROPPED (the clip to the chunk's writable
//     box — THIS is what makes a chunk-spanning piece write ONLY the current chunk's slice).
//  3. transformState: mirror THEN rotate the block state's facing.
//  4. view.SetBlock.
//
// `box` IS the chunk's writableBox (passed into PostProcess). canBeReplaced is the jar's
// always-true default for these pieces, so it is omitted.
func (p *StructurePiece) placeBlock(view WorldGenView, st block.StateID, x, y, z int, box BoundingBox) {
	wx := p.getWorldX(x, z)
	wy := p.getWorldY(y)
	wz := p.getWorldZ(x, z)
	if !box.IsInside(wx, wy, wz) {
		return
	}
	view.SetBlock(wx, wy, wz, transformState(st, p.mirror, p.rotation))
}

// getBlock ports StructurePiece.getBlock: read the world block at piece-local (x,y,z),
// returning air when outside the chunk box (the WorldGenLevel "out-of-box reads as air").
func (p *StructurePiece) getBlock(view WorldGenView, x, y, z int, box BoundingBox) block.StateID {
	wx := p.getWorldX(x, z)
	wy := p.getWorldY(y)
	wz := p.getWorldZ(x, z)
	if !box.IsInside(wx, wy, wz) {
		return stateAir
	}
	return view.GetBlock(wx, wy, wz)
}

// generateBox ports StructurePiece.generateBox: fill the local sub-box [x0..x1]x[y0..y1]x
// [z0..z1] with edgeBlock on the faces and fillBlock in the interior, each cell via
// placeBlock (so every write is clipped). skipAir skips cells whose current world block is
// air (used by the desert pyramid cellar — generateBox(..., true)).
func (p *StructurePiece) generateBox(view WorldGenView, box BoundingBox, x0, y0, z0, x1, y1, z1 int, edge, fill block.StateID, skipAir bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if skipAir && block.IsAir(p.getBlock(view, x, y, z, box)) {
					continue
				}
				if y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1 {
					p.placeBlock(view, edge, x, y, z, box)
				} else {
					p.placeBlock(view, fill, x, y, z, box)
				}
			}
		}
	}
}

// BlockSelector ports StructurePiece.BlockSelector: a per-cell block chooser the
// generateBox BlockSelector overload consults. next(rng, x, y, z, isEdge) advances the
// piece RNG and returns the chosen state; the jungle temple's MossStoneSelector draws
// nextFloat() per cell to pick cobblestone vs mossy cobblestone (a load-bearing RNG draw —
// the per-cell draw order is part of the determinism contract, Pitfall #3).
type BlockSelector interface {
	next(rng levelgen.RandomSource, x, y, z int, isEdge bool) block.StateID
}

// generateBoxSelector ports StructurePiece.generateBox(... boolean alwaysReplace,
// RandomSource, BlockSelector): fill the local sub-box, choosing each cell's state via
// selector.next (which draws from the piece RNG). The iteration order is y -> x -> z
// (jar-exact, matching the BlockState generateBox), so the per-cell nextFloat draws happen
// in the same order vanilla makes them — the placement is RNG-faithful.
//
// JAR-EXACT GATE (Pitfall #3): the selector's next() draw + the placeBlock happen ONLY when
// `alwaysReplace || getBlock(x,y,z).isAir()`. The selector is NOT consulted (no RNG draw) for
// a cell that already holds a non-air block when alwaysReplace=false — so the per-cell draw
// COUNT depends on what is already placed. The jungle temple passes alwaysReplace=false; in a
// fresh preliminary-surface view the interior cells are air, so the draws fire as vanilla.
//
// Source: javap -c StructurePiece.generateBox(...,Z,RandomSource,BlockSelector)
// (if (alwaysReplace || getBlock(..).isAir()) { selector.next(..); placeBlock(getNext(),..) }) +
// JungleTemplePiece$MossStoneSelector.next.
func (p *StructurePiece) generateBoxSelector(view WorldGenView, box BoundingBox, x0, y0, z0, x1, y1, z1 int, alwaysReplace bool, rng levelgen.RandomSource, sel BlockSelector) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if !alwaysReplace && !block.IsAir(p.getBlock(view, x, y, z, box)) {
					continue
				}
				isEdge := y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1
				st := sel.next(rng, x, y, z, isEdge)
				p.placeBlock(view, st, x, y, z, box)
			}
		}
	}
}

// generateAirBox ports StructurePiece.generateAirBox: fill the local sub-box with air.
func (p *StructurePiece) generateAirBox(view WorldGenView, box BoundingBox, x0, y0, z0, x1, y1, z1 int) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				p.placeBlock(view, stateAir, x, y, z, box)
			}
		}
	}
}

// generateMaybeBox ports StructurePiece.generateMaybeBox: like generateBox but each cell is
// placed only if random.nextFloat() <= probability (a per-cell probability draw). skipAir
// skips air cells; the hasToBeInside interior check is omitted (no porting piece needs it).
func (p *StructurePiece) generateMaybeBox(view WorldGenView, box BoundingBox, rng levelgen.RandomSource, probability float32, x0, y0, z0, x1, y1, z1 int, edge, fill block.StateID, skipAir bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if rng.NextFloat() > probability {
					continue
				}
				if skipAir && block.IsAir(p.getBlock(view, x, y, z, box)) {
					continue
				}
				if y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1 {
					p.placeBlock(view, edge, x, y, z, box)
				} else {
					p.placeBlock(view, fill, x, y, z, box)
				}
			}
		}
	}
}

// maybeGenerateBlock ports StructurePiece.maybeGenerateBlock: place blockState at local
// (x,y,z) iff random.nextFloat() < probability.
func (p *StructurePiece) maybeGenerateBlock(view WorldGenView, box BoundingBox, rng levelgen.RandomSource, probability float32, x, y, z int, st block.StateID) {
	if rng.NextFloat() < probability {
		p.placeBlock(view, st, x, y, z, box)
	}
}

// fillColumnDown ports StructurePiece.fillColumnDown: from local (x,startY,z), write
// blockState downward while the existing world block is replaceable-by-structures (air or
// liquid) and above the world floor (minY+1). Clipped to box (an out-of-box start is a no-op
// — the column lives in a different chunk). The downward walk reads via GetBlock (live, so a
// just-placed solid stops the fill).
func (p *StructurePiece) fillColumnDown(view WorldGenView, st block.StateID, x, startY, z int, box BoundingBox, minY int) {
	wx := p.getWorldX(x, z)
	wz := p.getWorldZ(x, z)
	wy := p.getWorldY(startY)
	if !box.IsInside(wx, wy, wz) {
		return
	}
	for wy > minY+1 && isReplaceableByStructures(view.GetBlock(wx, wy, wz)) {
		view.SetBlock(wx, wy, wz, st)
		wy--
	}
}

// createChest ports StructurePiece.createChest: place a chest block at local (x,y,z) AND
// emit a chest BlockEntity carrying {LootTable, LootTableSeed} — the lazy-roll keystone.
// Vanilla: `setBlock(reorient(chest), 2); chestBE.setLootTable(key, random.nextLong())`. The
// `random.nextLong()` draw on the PIECE's RandomSource (threaded in from PostProcess) is what
// makes chest contents deterministic per (worldseed, chunk). Loot is NOT rolled here — the
// chest stores only the table id + seed and rolls LAZILY on first open (Pitfall 2,
// server/chest_loot.go unpackLootTable). Returns true on a successful in-box placement.
//
// Vanilla calls reorient() to face the chest away from a solid neighbor; that needs a settled
// neighborhood (a live BlockGetter), so this places the chest with the piece's default facing
// (NORTH transformed by the orientation) — the VISIBLE chest block + the loot seam are
// delivered, only reorient (cosmetic facing) is deferred. The lootTable id + seed are appended
// to lootChests (kept as the StructureStart record for persistence) when non-nil.
//
// CROSS-CHUNK DETERMINISM (Pitfall #2): Sulfur re-runs each piece's PostProcess once per
// overlapping chunk with a FRESH rng re-seeded from the start's ORIGIN chunk (place.go
// placeInChunk), clipping WRITES to that chunk's box. For the RNG streams to stay identical
// across chunks, the nextLong() draw MUST be UNCONDITIONAL — drawn regardless of whether the
// chest's world-pos falls in THIS chunk's writable box, exactly like maybeGenerateBlock draws
// nextFloat() before placeBlock clips. (Vanilla writes the whole structure into a multi-chunk
// WorldGenLevel in ONE pass, so its createChest early-returns on box.isInside before the draw;
// Sulfur's per-chunk re-run model requires the draw to PRECEDE the clip — otherwise a chest in
// chunk A but not chunk B desyncs every subsequent draw between the two passes.) The chest
// BLOCK + BE writes are still clipped to the box. Returns true when the chest landed in THIS
// chunk's box (the write happened).
//
// Source: javap StructurePiece.createChest -> setLootTable(key, random.nextLong()).
func (p *StructurePiece) createChest(view WorldGenView, box BoundingBox, rng levelgen.RandomSource, x, y, z int, lootTable string, lootChests *[]LootChest) bool {
	wx := p.getWorldX(x, z)
	wy := p.getWorldY(y)
	wz := p.getWorldZ(x, z)
	// setLootTable(key, random.nextLong()): draw the seed UNCONDITIONALLY (before the box clip)
	// so the RNG stream is identical across the per-chunk re-runs — the determinism keystone
	// (Pitfall 1/2). ONE draw per chest regardless of clip.
	seed := rng.NextLong()
	if !box.IsInside(wx, wy, wz) {
		return false // out of THIS chunk's box -> no write (the draw already happened above)
	}
	chest := block.Chest{Facing: block.North, Type: block.ChestTypeSingle, Waterlogged: false}
	st := transformState(block.ToStateID[chest], p.mirror, p.rotation)
	view.SetBlock(wx, wy, wz, st)
	view.SetBlockEntity(wx, wy, wz, block.EntityTypes["minecraft:chest"], lootTable, seed)
	if lootChests != nil {
		*lootChests = append(*lootChests, LootChest{X: wx, Y: wy, Z: wz, LootTable: lootTable, LootTableSeed: seed})
	}
	return true
}

// LootChest records a placed chest's world position + its loot-table id + the gen-time
// lootTableSeed (the piece-RNG nextLong() draw). The chest BlockEntity carries {LootTable,
// LootTableSeed} into the chunk; this record tracks the same on the StructureStart so the
// persistence/resolver path can find every chest. Loot is rolled LAZILY on first open
// (server/chest_loot.go unpackLootTable), never at gen (Pitfall 2).
type LootChest struct {
	X, Y, Z       int
	LootTable     string
	LootTableSeed int64
}

// addChildren is the recursion hook (StructurePiece.addChildren). Single-piece temples (the
// desert pyramid) do NOT recurse — addChildren is a no-op here. It exists with the accessor +
// findCollisionPiece collision check for Phase-15 mineshaft/stronghold reuse (multi-piece
// trees that DO recurse). A concrete multi-piece base overrides it.
func (p *StructurePiece) addChildren(_ Piece, _ PieceAccessor, _ levelgen.RandomSource) {}

// PieceAccessor ports StructurePieceAccessor: the addChildren recursion sink (a piece adds
// child pieces + queries collisions through it). Phase-15 multi-piece structures use it; the
// single-piece temples never call it.
type PieceAccessor interface {
	// AddPiece appends a child piece to the in-progress tree.
	AddPiece(p Piece)
	// FindCollisionPiece returns the first existing piece whose bbox intersects box (nil if
	// none) — the recursion's overlap guard.
	FindCollisionPiece(box BoundingBox) Piece
}

// FindCollisionPiece ports StructurePiece.findCollisionPiece(List, box): the first piece in
// the list whose bbox intersects box, else nil. Exported for the PieceAccessor + Phase-15.
func FindCollisionPiece(pieces []Piece, box BoundingBox) Piece {
	for _, pc := range pieces {
		if pc.BoundingBox().Intersects(box) {
			return pc
		}
	}
	return nil
}

// isReplaceableByStructures ports StructurePiece.isReplaceableByStructures: air or liquid
// (the glow-lichen/seagrass cases are omitted — the desert pyramid fillColumnDown only runs
// over sandy desert terrain, never those plants). Used by fillColumnDown's downward walk.
func isReplaceableByStructures(st block.StateID) bool {
	return block.IsAir(st) || isLiquid(st)
}

// isLiquid reports whether a state is a water/lava fluid (the structure-replaceable liquids).
func isLiquid(st block.StateID) bool {
	return st == stateWater || st == stateLava
}

// Pre-resolved state ids the piece helpers reuse (air for generateAirBox/getBlock, water/lava
// for isReplaceableByStructures). Resolved once at package init from the block registry.
var (
	stateAir   = block.ToStateID[block.Air{}]
	stateWater = block.ToStateID[block.Water{Level: 0}]
	stateLava  = block.ToStateID[block.Lava{Level: 0}]
)

// Piece is extended (vs 14-01's bbox-only placeholder) with PostProcess: a piece writes its
// geometry into the chunk's writable box via the WorldGenView, clipped by placeBlock. The
// rng is the per-piece WorldgenRandom (SetLargeFeatureSeed over (worldSeed,startChunkX,
// startChunkZ)) — re-derivable, so placing the same piece from two overlapping chunks draws
// the same stream + clips to each chunk's slice (idempotent, Pitfall #2).
type Piece interface {
	// BoundingBox returns the piece's world-block AABB.
	BoundingBox() BoundingBox
	// PostProcess writes the piece's blocks into box (the chunk's writable column), clipped.
	PostProcess(view WorldGenView, box BoundingBox, chunkPos level.ChunkPos, rng levelgen.RandomSource)
}
