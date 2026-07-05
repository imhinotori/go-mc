package world

// feature_freeze.go ports SnowAndFreezeFeature.place — the freeze_top_layer feature
// (net.minecraft.world.level.levelgen.feature.SnowAndFreezeFeature, javap -c against
// temp/cache/26.2-inner.jar). It is the LAST decoration step (TOP_LAYER_MODIFICATION):
// over the 16x16 columns of the origin chunk it lays SNOW_LAYER on cold solid ground and
// turns exposed WATER to ICE in cold columns. Its config is NoneFeatureConfiguration (empty).
//
// SnowAndFreezeFeature.place takes ZERO rng draws (pure per-column geometry + biome climate
// reads), so it never touches the decoration draw stream — matching the bytecode.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.SnowAndFreezeFeature.place
//   - net.minecraft.world.level.biome.Biome.shouldFreeze(LevelReader, BlockPos, boolean)
//   - net.minecraft.world.level.biome.Biome.shouldSnow(LevelReader, BlockPos)
//   - net.minecraft.world.level.biome.Biome.getPrecipitationAt / hasPrecipitation / coldEnoughToSnow
//   - net.minecraft.world.level.block.SnowLayerBlock.canSurvive (the snow ground gate)
//
// coldEnoughToSnow is REUSED from world/levelgen/surface (temperature.go — the already-ported
// biome temperature model), exported as surface.ColdEnoughToSnow; it is NOT re-ported here.
// hasPrecipitation is read from the same model (surface.HasPrecipitation).
//
// GEN-TIME LIGHT SEAM: shouldFreeze/shouldSnow both gate on getBrightness(BLOCK, pos) < 10.
// During world generation block light is 0 for every column (no light sources placed yet), so
// the gate is always satisfied — the jar's own worldgen behaviour. The port therefore treats
// the light condition as always-true (== gen-time), which is behavior-identical to running the
// jar's SnowAndFreezeFeature at decoration time. The edge argument to shouldFreeze is false
// (the jar passes literal false), so the water-edge scan is skipped exactly as in the jar.

import (
	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
	"github.com/imhinotori/sulfur/world/levelgen/surface"
)

func init() { registerFeatureBody("freeze_top_layer", freezeTopLayerBody) }

// freezeTopLayerBody ports SnowAndFreezeFeature.place (javap -c), ZERO rng draws. For each of
// the 16x16 columns of the ORIGIN chunk (dx,dz in [0..15]) it:
//
//	x = origin.X + dx;  z = origin.Z + dz
//	topY = getHeight(MOTION_BLOCKING, x, z)     // first Y ABOVE the highest motion-blocking/fluid
//	pos1 = (x, topY, z)                          // the empty spot snow lands in
//	pos2 = pos1.below() = (x, topY-1, z)         // the surface block (or water) ice replaces
//	biome = getBiome(pos2)
//	if biome.shouldFreeze(level, pos2, false): setBlock(pos2, ICE)
//	if biome.shouldSnow(level, pos1):
//	    setBlock(pos1, SNOW)
//	    below := getBlockState(pos2); if below has SNOWY property: setBlock(pos2, below{snowy=true})
//
// The origin passed to the feature is a chunk-corner block pos (the placed_feature origin), so
// the 16x16 sweep covers exactly that chunk's columns. All reads/writes go through the 3x3 view
// (via bctx) + the placement context (heightmap + biome).
func freezeTopLayerBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	_ levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	seaLevel := bctx.seaLevelOr(63)
	placed := false

	for dx := 0; dx < 16; dx++ {
		for dz := 0; dz < 16; dz++ {
			x := pos.X + dx
			z := pos.Z + dz
			topY := ctx.GetHeight(placement.MotionBlocking, x, z)
			pos1 := placement.BlockPos{X: x, Y: topY, Z: z}
			pos2 := placement.BlockPos{X: x, Y: topY - 1, Z: z}
			bt := ctx.BiomeAt(pos2.X, pos2.Y, pos2.Z)

			// shouldFreeze(level, pos2, edge=false): cold enough + the block is a water SOURCE
			// (WATER fluid + LiquidBlock). Water -> ICE.
			if freezeShouldFreeze(bctx, bt, pos2, seaLevel) {
				bctx.placeState(pos2, freezeIceState())
				placed = true
			}

			// shouldSnow(level, pos1): cold + precip + the spot is air-or-snow + snow can survive
			// on the block below. -> SNOW; then propagate SNOWY to the ground below.
			if freezeShouldSnow(bctx, bt, pos1, seaLevel) {
				bctx.placeState(pos1, freezeSnowState())
				placed = true
				below := bctx.getState(pos2)
				if snowy, ok := freezeSnowyState(below); ok {
					bctx.placeState(pos2, snowy)
				}
			}
		}
	}
	return placed
}

// freezeShouldFreeze ports Biome.shouldFreeze(level, pos, false) at worldgen light (block
// light 0 < 10, so the light gate is always satisfied):
//
//	if warmEnoughToRain(pos, seaLevel):                 return false   // == !coldEnoughToSnow
//	// isInsideBuildHeight + brightness<10 (gen: true):
//	fluid := getFluidState(pos); block := getBlock(pos)
//	if fluid.is(WATER) && block instanceof LiquidBlock:
//	    // edge==false -> return true immediately (skip the 4-neighbour water-edge scan)
//	    return true
//	return false
//
// The "WATER fluid + LiquidBlock" pair is the water SOURCE/flowing water block at worldgen
// (block.IsWaterFluid over a Water block — NOT a waterlogged block, which is not a LiquidBlock).
func freezeShouldFreeze(bctx *bodyContext, bt biome.Type, pos placement.BlockPos, seaLevel int) bool {
	if !surface.ColdEnoughToSnow(bt, pos.X, pos.Y, pos.Z, seaLevel) {
		return false
	}
	st := bctx.getState(pos)
	return freezeIsWaterLiquidBlock(st)
}

// freezeShouldSnow ports Biome.shouldSnow(level, pos) at worldgen light:
//
//	if getPrecipitationAt(pos, seaLevel) != SNOW:       return false
//	    // getPrecipitationAt = hasPrecipitation() ? (coldEnoughToSnow ? SNOW : RAIN) : NONE
//	// isInsideBuildHeight + brightness<10 (gen: true):
//	state := getBlock(pos)
//	if (state.isAir() || state.is(SNOW)) && SNOW.defaultBlockState().canSurvive(level, pos):
//	    return true
//	return false
func freezeShouldSnow(bctx *bodyContext, bt biome.Type, pos placement.BlockPos, seaLevel int) bool {
	// getPrecipitationAt == SNOW iff the biome has precipitation AND it is cold enough.
	if !surface.HasPrecipitation(bt) {
		return false
	}
	if !surface.ColdEnoughToSnow(bt, pos.X, pos.Y, pos.Z, seaLevel) {
		return false
	}
	st := bctx.getState(pos)
	if !block.IsAir(st) && st != freezeSnowState() {
		return false
	}
	return freezeSnowCanSurvive(bctx, pos)
}

// freezeSnowCanSurvive ports SnowLayerBlock.canSurvive(SNOW.defaultBlockState(), level, pos):
// it reads the block BELOW pos (== pos2, the surface):
//
//	below := getBlockState(pos.below())
//	if below.is(#cannot_support_snow_layer):   return false   // ice, packed_ice, barrier
//	if below.is(#support_override_snow_layer): return true    // honey_block, soul_sand, mud
//	return isFaceFull(below.collisionShape, UP) || (below is SNOW && layers==8)
//
// isFaceFull(collisionShape, UP) is the no-context face-full read == block.IsFaceSturdy(below,
// UP, FULL) (the precomputed support table — the same read the ground bodies use). The
// snow-layers==8 special case cannot arise here (freeze places a single fresh layer), but is
// reproduced for fidelity (a below == SNOW{Layers:8} passes).
func freezeSnowCanSurvive(bctx *bodyContext, pos placement.BlockPos) bool {
	belowPos := placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	below := bctx.getState(belowPos)
	if freezeCannotSupportSnow(below) {
		return false
	}
	if freezeSupportOverrideSnow(below) {
		return true
	}
	if block.IsFaceSturdy(below, block.Up, block.SupportFull) {
		return true
	}
	// below is a full snow layer (SNOW with layers==8).
	return below == block.ToStateID[block.Snow{Layers: 8}]
}

// ---- constants / helpers (cited) ----

// freezeIceState is Blocks.ICE.defaultBlockState().
func freezeIceState() block.StateID { return freezeIceStateID }

// freezeSnowState is Blocks.SNOW.defaultBlockState() (SnowLayerBlock: layers=1).
func freezeSnowState() block.StateID { return freezeSnowStateID }

var (
	freezeIceStateID  = block.ToStateID[block.Ice{}]
	freezeSnowStateID = block.ToStateID[block.Snow{Layers: 1}]
)

// freezeIsWaterLiquidBlock reports the "fluidState.is(WATER) && block instanceof LiquidBlock"
// pair the ice gate needs: a WATER-block (source or flowing water), NOT a waterlogged block
// (a waterlogged block's getBlock() is not a LiquidBlock, so it fails the instanceof). The Go
// block package models the water fluid block as block.Water{Level}; a waterlogged block is a
// different concrete type, so a direct type check over block.Water is the faithful proxy.
func freezeIsWaterLiquidBlock(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.Water)
	return ok
}

// freezeCannotSupportSnow ports below.is(#minecraft:cannot_support_snow_layer) — resolved
// constant-for-constant from data/minecraft/tags/block/cannot_support_snow_layer.json (26.2):
// ice, packed_ice, barrier.
func freezeCannotSupportSnow(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	switch block.StateList[st].(type) {
	case block.Ice, block.PackedIce, block.Barrier:
		return true
	default:
		return false
	}
}

// freezeSupportOverrideSnow ports below.is(#minecraft:support_override_snow_layer) — resolved
// constant-for-constant from data/minecraft/tags/block/support_override_snow_layer.json (26.2):
// honey_block, soul_sand, mud.
func freezeSupportOverrideSnow(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	switch block.StateList[st].(type) {
	case block.HoneyBlock, block.SoulSand, block.Mud:
		return true
	default:
		return false
	}
}

// freezeSnowyState ports the "state.hasProperty(SnowyBlock.SNOWY)" propagation: the three
// SnowyDirtBlocks (GrassBlock/Podzol/Mycelium) carry the `snowy` property; setting it to true
// yields the snow-covered variant. Returns (state, true) when st is one of them, else (0, false).
// CITE: SnowyDirtBlock / SnowyBlock.SNOWY; SnowAndFreezeFeature.place sets snowy=true on the
// ground block directly under a freshly-placed snow layer.
func freezeSnowyState(st block.StateID) (block.StateID, bool) {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return 0, false
	}
	switch block.StateList[st].(type) {
	case block.GrassBlock:
		return block.ToStateID[block.GrassBlock{Snowy: true}], true
	case block.Podzol:
		return block.ToStateID[block.Podzol{Snowy: true}], true
	case block.Mycelium:
		return block.ToStateID[block.Mycelium{Snowy: true}], true
	default:
		return 0, false
	}
}
