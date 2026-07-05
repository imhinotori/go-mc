package world

// feature_underwater_magma.go ports UnderwaterMagmaFeature.place and its helpers — the
// `underwater_magma` configured feature
// (net.minecraft.world.level.levelgen.feature.UnderwaterMagmaFeature, verified via `javap -c -p`
// against temp/cache/26.2-inner.jar). It scans down from the origin through water to the ocean
// floor, then over a cube of side (2*placementRadiusAroundFloor+1) centred on that floor it
// probabilistically places magma_block on valid, fully-enclosed underwater cells.
//
// Config is UnderwaterMagmaConfiguration:
//   - floor_search_range                    (int  0..512)
//   - placement_radius_around_floor          (int  0..64)
//   - placement_probability_per_valid_position (float 0..1)
//
// Sources (javap -c, 26.2-inner.jar):
//   - UnderwaterMagmaFeature.place / getFloorY / isValidPlacement / isWaterOrAir / isVisibleFromOutside
//   - net.minecraft.world.level.levelgen.Column.scan / scanDirection / create / getFloor
//   - net.minecraft.core.BlockPos.betweenClosed(int,int,int,int,int,int) (iteration order)
//
// CRITICAL RNG DRAW ORDER (the determinism contract): vanilla builds a lazy Stream over
// BoundingBox.fromCorners(floor-radius, floor+radius) via BlockPos.betweenClosedStream, then
//     .filter(pos -> random.nextFloat() < probability)   // draws nextFloat() for EVERY pos
//     .filter(pos -> isValidPlacement(level, pos))         // only for pos that passed
//     .mapToInt(pos -> { setBlock(magma); return 1; }).sum()
// Java stream laziness => for EACH position in betweenClosed order (x innermost, then y, then z),
// nextFloat() is drawn FIRST; isValidPlacement (and the setBlock) runs only when the float
// passes. So nextFloat() is consumed once per box cell regardless of validity. This port
// reproduces exactly that: one nextFloat() per cell, in betweenClosed order, then the gate.
//
// betweenClosed(minX..maxX, minY..maxY, minZ..maxZ) yields index 0..N-1 with
//     x = idx % width; y = (idx / width) % height; z = idx / width / height
// i.e. X varies fastest, then Y, then Z (BlockPos.betweenClosed, javap/CFR-verified).

import (
	"encoding/json"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("underwater_magma", underwaterMagmaBody) }

// underwaterMagmaConfig is the decoded UnderwaterMagmaConfiguration.
type underwaterMagmaConfig struct {
	floorSearchRange                     int
	placementRadiusAroundFloor           int
	placementProbabilityPerValidPosition float32
}

// jsonUnderwaterMagmaConfig is the on-disk shape (verified underwater_magma.json).
type jsonUnderwaterMagmaConfig struct {
	FloorSearchRange                     int     `json:"floor_search_range"`
	PlacementRadiusAroundFloor           int     `json:"placement_radius_around_floor"`
	PlacementProbabilityPerValidPosition float32 `json:"placement_probability_per_valid_position"`
}

func decodeUnderwaterMagmaConfig(cf *feature.ConfiguredFeature) (underwaterMagmaConfig, bool) {
	raw := configRaw(cf)
	if len(raw) == 0 {
		return underwaterMagmaConfig{}, false
	}
	var j jsonUnderwaterMagmaConfig
	if err := json.Unmarshal(raw, &j); err != nil {
		return underwaterMagmaConfig{}, false
	}
	return underwaterMagmaConfig{
		floorSearchRange:                     j.FloorSearchRange,
		placementRadiusAroundFloor:           j.PlacementRadiusAroundFloor,
		placementProbabilityPerValidPosition: j.PlacementProbabilityPerValidPosition,
	}, true
}

// underwaterMagmaBody ports UnderwaterMagmaFeature.place.
func underwaterMagmaBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg, ok := decodeUnderwaterMagmaConfig(cf)
	if !ok {
		return false
	}

	floorY, found := underwaterMagmaGetFloorY(bctx, pos, cfg)
	if !found {
		return false
	}
	center := placement.BlockPos{X: pos.X, Y: floorY, Z: pos.Z}
	r := cfg.placementRadiusAroundFloor

	// BoundingBox.fromCorners(center - (r,r,r), center + (r,r,r)) -> [center-r, center+r] in each axis.
	minX, minY, minZ := center.X-r, center.Y-r, center.Z-r
	maxX, maxY, maxZ := center.X+r, center.Y+r, center.Z+r

	placed := 0
	// betweenClosed order: X fastest, then Y, then Z. nextFloat() per cell (lazy stream).
	for z := minZ; z <= maxZ; z++ {
		for y := minY; y <= maxY; y++ {
			for x := minX; x <= maxX; x++ {
				// filter #0: random.nextFloat() < probability — ALWAYS drawn.
				if !(rng.NextFloat() < cfg.placementProbabilityPerValidPosition) {
					continue
				}
				p := placement.BlockPos{X: x, Y: y, Z: z}
				// filter #1: isValidPlacement.
				if !underwaterMagmaIsValidPlacement(bctx, p) {
					continue
				}
				// mapToInt: setBlock(magma_block, flag 2), return 1.
				bctx.placeState(p, underwaterMagmaStateID)
				placed++
			}
		}
	}
	return placed > 0
}

// underwaterMagmaGetFloorY ports UnderwaterMagmaFeature.getFloorY:
//
//	Column.scan(level, origin, floorSearchRange, isWater, !isWater).map(Column::getFloor).orElseGet(empty)
//
// == scan the column at origin; the origin MUST be water (scan's initial isStateAtPosition gate),
// then walk DOWN through water for up to floorSearchRange-1 steps and return the first Y that is
// NOT water (the ocean floor). getFloor() returns that DOWN-direction result.
func underwaterMagmaGetFloorY(bctx *bodyContext, origin placement.BlockPos, cfg underwaterMagmaConfig) (int, bool) {
	// Column.scan initial gate: origin must satisfy the FIRST predicate (isWater).
	if !underwaterMagmaIsWater(bctx.getState(origin)) {
		return 0, false
	}
	// getFloor == scanDirection(DOWN): move down while water (up to range-1 steps from y=originY),
	// then the terminal cell must be !water to yield a floor Y.
	return underwaterMagmaScanDown(bctx, origin, cfg.floorSearchRange)
}

// underwaterMagmaScanDown ports Column.scanDirection with Direction.DOWN and the (isWater,
// !isWater) predicate pair getFloorY passes. It sets y=startY then, for i in [1, range),
// while the cell isWater it moves down one; afterwards, if the current cell is !isWater it
// returns its Y (a floor), else empty. This is the DOWN scan whose result getFloor() returns.
func underwaterMagmaScanDown(bctx *bodyContext, origin placement.BlockPos, searchRange int) (int, bool) {
	y := origin.Y
	for i := 1; i < searchRange; i++ {
		if !underwaterMagmaIsWater(bctx.getState(placement.BlockPos{X: origin.X, Y: y, Z: origin.Z})) {
			break
		}
		y--
	}
	if !underwaterMagmaIsWater(bctx.getState(placement.BlockPos{X: origin.X, Y: y, Z: origin.Z})) {
		return y, true
	}
	return 0, false
}

// underwaterMagmaIsValidPlacement ports UnderwaterMagmaFeature.isValidPlacement:
//
//	if isWaterOrAir(getBlockState(pos)):                                          return false
//	if isVisibleFromOutside(level, pos.below(), UP):                             return false
//	for dir in HORIZONTAL:
//	    if isVisibleFromOutside(level, pos.relative(dir), dir.getOpposite()):    return false
//	return true
func underwaterMagmaIsValidPlacement(bctx *bodyContext, pos placement.BlockPos) bool {
	if underwaterMagmaIsWaterOrAir(bctx.getState(pos)) {
		return false
	}
	below := placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	if underwaterMagmaIsVisibleFromOutside(bctx, below, dirUp) {
		return false
	}
	for _, d := range icebergHorizontal {
		if underwaterMagmaIsVisibleFromOutside(bctx, icebergRelative(pos, d), icebergOpposite(d)) {
			return false
		}
	}
	return true
}

// underwaterMagmaIsWaterOrAir ports UnderwaterMagmaFeature.isWaterOrAir: state.is(WATER) || isAir().
func underwaterMagmaIsWaterOrAir(st block.StateID) bool {
	return underwaterMagmaIsWater(st) || block.IsAir(st)
}

// underwaterMagmaIsVisibleFromOutside ports UnderwaterMagmaFeature.isVisibleFromOutside:
//
//	shape = state.getFaceOcclusionShape(dir)
//	return shape != Shapes.empty() && Block.isShapeFullBlock(shape) ? false : true
//
// i.e. NOT visible only when the face occludes as a full unit square; visible otherwise
// (empty or partial face occlusion). SEAM: the codebase exposes the "is the face-occlusion
// shape a full block" test as block.IsCollisionShapeFullBlock (the same reduction
// tree_decorator/dungeon use for isShapeFullBlock of a solid render block — for the ocean-floor
// blocks this feature actually sees, stone/dirt/gravel/deepslate occlude their full faces while
// water/air/magma do not, so the full-cube test is face-direction-independent and
// behavior-identical). Returns true (visible) when the block is NOT a full-cube occluder.
func underwaterMagmaIsVisibleFromOutside(bctx *bodyContext, pos placement.BlockPos, _ icebergDir) bool {
	return !block.IsCollisionShapeFullBlock(bctx.getState(pos))
}

// underwaterMagmaIsWater ports state.is(Blocks.WATER).
func underwaterMagmaIsWater(st block.StateID) bool { return icebergIsBlockType[block.Water](st) }

var underwaterMagmaStateID = block.ToStateID[block.MagmaBlock{}] // Blocks.MAGMA_BLOCK.defaultBlockState()
