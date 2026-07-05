package world

// feature_scattered_ore.go ports ScatteredOreFeature — the nether ancient-debris
// scattered-ore variant (net.minecraft.world.level.levelgen.feature.ScatteredOreFeature,
// javap -c against temp/cache/26.2-inner.jar). It EXTENDS Feature<OreConfiguration> and
// REUSES the shared OreFeature.canPlaceOre gate (feature_ore.go's oreCanPlace + the
// OreConfiguration decode + RuleTest set), so it inherits the tag_match/block_match/...
// target matching and the discard-on-air-exposure roll bit-for-bit. This body registers
// under "scattered_ore" via registerFeatureBody from init().
//
// Unlike OreFeature (a lerp-line ellipsoid blob), ScatteredOreFeature scatters up to
// (size+1) INDIVIDUAL ore blocks, each at a jittered offset from the origin. The exact
// rng draw order IS the determinism contract (T-12-05):
//
//	i6 = nextInt(size + 1)                      // the scatter count (1 draw)
//	for i8 in [0, i6):
//	    dist = min(i8, 7)                        // MAX_DIST_FROM_ORIGIN clamp (no draw)
//	    offsetTargetPos: 3x getRandomPlacementInOneAxisRelativeToOrigin(rng, dist)
//	        each = Math.round((nextFloat() - nextFloat()) * dist)   // TWO nextFloat draws
//	        -> mutable.setWithOffset(origin, ox, oy, oz)
//	    existing = getBlockState(mutable)
//	    for target in targets:
//	        if OreFeature.canPlaceOre(existing, ..., rng, config, target, mutable):
//	            setBlock(mutable, target.state); break   // FIRST match wins, then break the pos
//	return true                                          // ALWAYS returns true (1)
//
// So each scatter step draws EXACTLY 6 nextFloat (the 3 axis jitters), THEN — per the
// canPlaceOre path — the target RuleTest's own draws + the discard-on-air roll (all
// inherited from feature_ore.go, byte-identical). Every write goes through
// bctx.placeState -> Neighborhood.SetBlock, so a scatter straddling a chunk edge spills
// into the neighbor.
//
// Source mapping (all javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.ScatteredOreFeature.place /
//     offsetTargetPos / getRandomPlacementInOneAxisRelativeToOrigin
//   - net.minecraft.world.level.levelgen.feature.OreFeature.canPlaceOre (REUSED via
//     feature_ore.go oreCanPlace)

import (
	"math"

	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("scattered_ore", scatteredOreBody) }

// scatteredOreMaxDistFromOrigin ports ScatteredOreFeature.MAX_DIST_FROM_ORIGIN = 7 (the
// Math.min(i8, 7) clamp on the per-step jitter distance).
const scatteredOreMaxDistFromOrigin = 7

// scatteredOreBody ports ScatteredOreFeature.place. It decodes the (shared) OreConfiguration,
// draws the scatter count, then for each scatter draws the 3-axis jitter and runs the shared
// canPlaceOre gate. Returns true unconditionally (matching the bytecode's `iconst_1 ireturn`).
func scatteredOreBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	cfg, err := decodeOreConfigCached(cf)
	if err != nil {
		// A config decode failure is a build-data error; loudly panic so it surfaces in
		// generation rather than silently placing nothing (T-12-07).
		panic(err)
	}

	// i6 = nextInt(size + 1): the scatter count (1 draw).
	count := int(rng.NextIntN(int32(cfg.size + 1)))

	for step := 0; step < count; step++ {
		// dist = min(step, 7) (MAX_DIST_FROM_ORIGIN clamp — no draw).
		dist := step
		if dist > scatteredOreMaxDistFromOrigin {
			dist = scatteredOreMaxDistFromOrigin
		}
		// offsetTargetPos: 3 axis jitters, each 2 nextFloat draws (IN THIS ORDER: x, y, z).
		ox := scatteredOreAxisJitter(rng, dist)
		oy := scatteredOreAxisJitter(rng, dist)
		oz := scatteredOreAxisJitter(rng, dist)
		p := placement.BlockPos{X: origin.X + ox, Y: origin.Y + oy, Z: origin.Z + oz}

		existing := bctx.getState(p)
		for _, tgt := range cfg.targets {
			if oreCanPlace(bctx, existing, rng, cfg, tgt, p.X, p.Y, p.Z) {
				bctx.placeState(p, tgt.state)
				break
			}
		}
	}
	return true
}

// scatteredOreAxisJitter ports ScatteredOreFeature.getRandomPlacementInOneAxisRelativeToOrigin:
//
//	Math.round((nextFloat() - nextFloat()) * dist)
//
// The TWO nextFloat draws (in order) are the determinism contract; Math.round(float)
// rounds half up toward +inf (Java: floor(f + 0.5)).
func scatteredOreAxisJitter(rng levelgen.RandomSource, dist int) int {
	f := (rng.NextFloat() - rng.NextFloat()) * float32(dist)
	return javaRoundF(f)
}

// javaRoundF ports java.lang.Math.round(float) EXACTLY (JDK 25 javap -c). The half-way
// tie-break is the bit-manipulation path (NOT a naive floor(f+0.5), which mis-rounds
// certain ties), so it is transcribed instruction-for-instruction:
//
//	intBits    = Float.floatToRawIntBits(f)
//	biasedExp  = (intBits & 0x7F800000) >> 23
//	shift      = 149 - biasedExp                       // 149 = SIGNIFICAND_WIDTH-2 + EXP_BIAS
//	if (shift & -32) == 0:                              // finite, 2^-32 <= ulp < 1
//	    r = (intBits & 0x007FFFFF) | 0x00800000        // mantissa | implicit 1
//	    if intBits < 0: r = -r                          // apply sign
//	    return ((r >> shift) + 1) >> 1                  // round-half-up
//	else:
//	    return (int)f                                   // truncate (huge/NaN/tiny path)
//
// Source: java.lang.Math.round(float).
func javaRoundF(f float32) int {
	intBits := int32(math.Float32bits(f))
	biasedExp := (intBits & 0x7F800000) >> 23
	shift := int32(149) - biasedExp
	if (shift & -32) == 0 {
		r := (intBits & 0x007FFFFF) | 0x00800000
		if intBits < 0 {
			r = -r
		}
		return int(((r >> uint(shift)) + 1) >> 1)
	}
	return int(f)
}
