package world

// feature_blue_ice.go ports BlueIceFeature.place — the `blue_ice` configured feature
// (net.minecraft.world.level.levelgen.feature.BlueIceFeature, verified via `javap -c -p`
// against temp/cache/26.2-inner.jar). It scatters blue_ice patches on a deep-ocean floor
// directly under water: an origin gate (must be at/below sea level with water at or above
// the origin, and packed_ice adjacent), a base blue_ice block, then up to 200 offset
// attempts that spread blue_ice onto ice-ish cells that touch existing blue_ice.
//
// Config is NoneFeatureConfiguration (empty {}), so there is nothing to parse.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.BlueIceFeature.place(FeaturePlaceContext)
//
// RNG draw order (the determinism contract) is a literal mirror of the bytecode:
//   - the base block placement takes ZERO draws;
//   - each of the 200 iterations draws EXACTLY nextInt(5), nextInt(6) (for `off`),
//     then, only if `n >= 1`, nextInt(n)*4 (the x/z offset pair) — matching the jar's
//     evaluation order. The offset draws are skipped (continue) when n < 1, exactly as
//     the jar `continue`s before the pos.offset(...) call.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("blue_ice", blueIceBody) }

// blueIceBody ports BlueIceFeature.place. The `level.getSeaLevel()` gate is the generator
// sea level (bctx.seaLevelOr(63) — the same value WorldGenLevel.getSeaLevel() returns).
//
// place():
//
//	if pos.getY() > level.getSeaLevel() - 1:                 return false
//	if !getBlockState(pos).is(WATER) && !getBlockState(pos.below()).is(WATER): return false
//	found := false
//	for dir in Direction.values():
//	    if dir == DOWN: continue
//	    if getBlockState(pos.relative(dir)).is(PACKED_ICE): found = true; break
//	if !found: return false
//	setBlock(pos, BLUE_ICE, 2)
//	for i in 0..200:
//	    xExtent := nextInt(5) - nextInt(6)
//	    yOff := 3
//	    if xExtent < 2: yOff += xExtent / 2
//	    if yOff >= 1:
//	        target := pos.offset(nextInt(yOff)-nextInt(yOff), xExtent, nextInt(yOff)-nextInt(yOff))
//	        st := getBlockState(target)
//	        if st.isAir() || st.is(WATER) || st.is(PACKED_ICE) || st.is(ICE):
//	            for d2 in Direction.values():
//	                if getBlockState(target.relative(d2)).is(BLUE_ICE):
//	                    setBlock(target, BLUE_ICE, 2); break
//	return true
func blueIceBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	seaLevel := bctx.seaLevelOr(63)

	// pos.getY() > getSeaLevel() - 1 -> reject.
	if pos.Y > seaLevel-1 {
		return false
	}

	// !is(WATER, pos) && !is(WATER, pos.below()) -> reject.
	if !blueIceIsWater(bctx.getState(pos)) &&
		!blueIceIsWater(bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})) {
		return false
	}

	// At least one non-DOWN neighbour must be packed_ice.
	found := false
	for _, d := range icebergDirections {
		if d == dirDown {
			continue
		}
		if blueIceIsPackedIce(bctx.getState(icebergRelative(pos, d))) {
			found = true
			break
		}
	}
	if !found {
		return false
	}

	// Base blue_ice block (setBlock flag 2 -> plain worldgen write through the view).
	bctx.placeState(pos, blueIceStateID)

	for i := 0; i < 200; i++ {
		// xExtent = nextInt(5) - nextInt(6). BOTH draws happen every iteration.
		xExtent := int(rng.NextIntN(5)) - int(rng.NextIntN(6))
		yOff := 3
		if xExtent < 2 {
			yOff += xExtent / 2
		}
		if yOff < 1 {
			continue
		}
		// pos.offset(nextInt(n)-nextInt(n), xExtent, nextInt(n)-nextInt(n)) — the four
		// offset draws happen ONLY when yOff >= 1, matching the bytecode `continue` above.
		dx := int(rng.NextIntN(int32(yOff))) - int(rng.NextIntN(int32(yOff)))
		dz := int(rng.NextIntN(int32(yOff))) - int(rng.NextIntN(int32(yOff)))
		target := placement.BlockPos{X: pos.X + dx, Y: pos.Y + xExtent, Z: pos.Z + dz}

		st := bctx.getState(target)
		if !(block.IsAir(st) || blueIceIsWater(st) || blueIceIsPackedIce(st) || blueIceIsIce(st)) {
			continue
		}
		for _, d := range icebergDirections {
			if blueIceIsBlueIce(bctx.getState(icebergRelative(target, d))) {
				bctx.placeState(target, blueIceStateID)
				break
			}
		}
	}
	return true
}

// ---- states / block-type predicates (cited) ----

var blueIceStateID = block.ToStateID[block.BlueIce{}]

// blueIceIsWater ports state.is(Blocks.WATER): a WATER block of any level (source or
// flowing). A waterlogged block is a different concrete type and does NOT match, exactly
// like the jar's block-type `is` check. Mirrors freeze_top_layer's water-block test.
func blueIceIsWater(st block.StateID) bool { return icebergIsBlockType[block.Water](st) }

// blueIceIsPackedIce ports state.is(Blocks.PACKED_ICE).
func blueIceIsPackedIce(st block.StateID) bool { return icebergIsBlockType[block.PackedIce](st) }

// blueIceIsIce ports state.is(Blocks.ICE).
func blueIceIsIce(st block.StateID) bool { return icebergIsBlockType[block.Ice](st) }

// blueIceIsBlueIce ports state.is(Blocks.BLUE_ICE).
func blueIceIsBlueIce(st block.StateID) bool { return icebergIsBlockType[block.BlueIce](st) }
