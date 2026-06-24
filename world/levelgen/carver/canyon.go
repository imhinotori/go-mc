package carver

import (
	"math"

	"github.com/imhinotori/sulfur/level"
)

// canyonWorldCarver ports net.minecraft.world.level.levelgen.carver.CanyonWorldCarver
// (javap -c, temp/cache/26.2-inner.jar): the RAVINES. carve() walks a SINGLE long path
// (no branching, unlike caves), and at each step carves an ellipsoid whose VERTICAL
// radius is stretched by a per-Y widthFactors profile + a yScale, giving the tall,
// narrow vertical slot a player recognizes as a ravine. It is an algorithmic port, NOT
// a copy of Mojang source.
//
// Source: CanyonWorldCarver.{carve, doCarve, initWidthFactors, updateVerticalRadius,
// shouldSkip} + WorldCarver.carveEllipsoid/canReach.
type canyonWorldCarver struct{}

// isStartChunk ports CanyonWorldCarver.isStartChunk = rng.nextFloat() <= probability.
func (canyonWorldCarver) isStartChunk(cfg *CarverConfig, rng *legacyRandom) bool {
	return rng.nextFloat() <= float32(cfg.Probability)
}

// carve ports CanyonWorldCarver.carve.
func (canyonWorldCarver) carve(cfg *CarverConfig, cc *carveContext, rng *legacyRandom, src level.ChunkPos) bool {
	// j = (getRange()*2 - 1) * 16 = 112.
	j := (carverRange*2 - 1) * 16

	x := float64(blockXIn(src, int(rng.nextIntN(16))))
	y := float64(cfg.Y.sample(rng, cc.minGenY))
	z := float64(blockZIn(src, int(rng.nextIntN(16))))

	yaw := rng.nextFloat() * (math.Pi * 2)                       // float 6.2831855
	verticalRotation := float32(cfg.VerticalRotation.sample(rng)) // FloatProvider
	yScale := float64(cfg.YScaleFloat.sample(rng))
	thickness := float32(cfg.Shape.thickness.sample(rng))
	segmentCount := int(float32(j) * float32(cfg.Shape.distanceFactor.sample(rng)))

	return doCarve(cfg, cc, rng.nextLong(), x, y, z,
		thickness, float32(yaw), verticalRotation, 0, segmentCount, yScale)
}

// doCarve ports CanyonWorldCarver.doCarve: the single-path ravine walk.
func doCarve(
	cfg *CarverConfig, cc *carveContext, seed int64,
	x, y, z float64, thickness, yaw, verticalRotation float32,
	segment, segmentCount int, yScale float64,
) bool {
	rng := newThreadLocalLegacy(seed)
	widthFactors := initWidthFactors(cfg, cc, rng)

	var yawDelta, pitchDelta float32
	carvedAny := false

	for seg := segment; seg < segmentCount; seg++ {
		// radius = 1.5 + Mth.sin(π*seg/count)*thickness.
		radius := 1.5 + float64(float32(math.Sin(math.Pi*float64(seg)/float64(segmentCount)))*thickness)
		// vr (vertical) is derived from radius*yScale BEFORE the horizontal factor.
		vr := radius * yScale
		// hr (horizontal) scales radius by the shape's horizontalRadiusFactor.
		hr := radius * float64(cfg.Shape.horizontalRadiusFactor.sample(rng))
		vr = updateVerticalRadius(cfg, rng, vr, float32(segmentCount), float32(seg))

		// Step the walk: cosP from verticalRotation; advance x/z by cos/sin(yaw)*cosP,
		// y by sin(verticalRotation).
		cosVR := float32(math.Cos(float64(verticalRotation)))
		sinVR := float32(math.Sin(float64(verticalRotation)))
		x += math.Cos(float64(yaw)) * float64(cosVR)
		y += float64(sinVR)
		z += math.Sin(float64(yaw)) * float64(cosVR)

		verticalRotation *= 0.7
		verticalRotation += pitchDelta * 0.05
		yaw += yawDelta * 0.05
		pitchDelta *= 0.8
		yawDelta *= 0.5
		pitchDelta += (rng.nextFloat() - rng.nextFloat()) * rng.nextFloat() * 2.0
		yawDelta += (rng.nextFloat() - rng.nextFloat()) * rng.nextFloat() * 4.0

		// 1-in-4 segments are skipped (just stepped).
		if rng.nextIntN(4) == 0 {
			continue
		}
		if !canReach(cc.chunk.Pos(), x, z, seg, segmentCount, thickness) {
			return carvedAny
		}
		if cc.carveEllipsoid(cfg, x, y, z, hr, vr, canyonSkip(cc, widthFactors)) {
			carvedAny = true
		}
	}
	return carvedAny
}

// initWidthFactors ports CanyonWorldCarver.initWidthFactors: a per-Y array (length =
// genDepth) of squared width multipliers. Most rows carry the previous value; every
// ~widthSmoothness rows it jumps to a fresh 1+nextFloat()*nextFloat() — this is what
// makes the ravine's horizontal slot pinch and bulge along its height (the ravine
// silhouette).
func initWidthFactors(cfg *CarverConfig, cc *carveContext, rng *legacyRandom) []float32 {
	genDepth := cc.chunk.Height()
	factors := make([]float32, genDepth)
	f := float32(1.0)
	for i := 0; i < genDepth; i++ {
		if i == 0 || rng.nextIntN(int32(cfg.Shape.widthSmoothness)) == 0 {
			f = 1.0 + rng.nextFloat()*rng.nextFloat()
		}
		factors[i] = f * f
	}
	return factors
}

// updateVerticalRadius ports CanyonWorldCarver.updateVerticalRadius: shrinks the
// vertical radius toward the ravine's center band so the slot is widest at mid-height
// and tapers top/bottom, then jitters it by 0.75..1.0.
func updateVerticalRadius(cfg *CarverConfig, rng *legacyRandom, vr float64, segmentCount, segment float32) float64 {
	// f = 1 - |0.5 - segment/segmentCount| * 2.
	f := float32(1.0) - mthAbs(0.5-segment/segmentCount)*2.0
	// factor = verticalRadiusDefaultFactor + verticalRadiusCenterFactor * f.
	factor := float32(cfg.Shape.verticalRadiusDefaultFactor) + float32(cfg.Shape.verticalRadiusCenterFactor)*f
	return float64(factor) * vr * float64(randomBetween(rng, 0.75, 1.0))
}

// canyonSkip ports CanyonWorldCarver.shouldSkip(ctx, widthFactors, dx, dy, dz, y): the
// ravine membership test using the per-Y widthFactors profile.
//
//	idx = y - minGenY; member = dx² + dz²*widthFactors[idx-1] + dy²/6 < 1.
//
// The dy²/6 term is what makes the ravine TALL (vertical extent counts 6x less than
// horizontal toward the unit ellipsoid), and widthFactors squeezes the width per row.
func canyonSkip(cc *carveContext, widthFactors []float32) skipChecker {
	return func(dx, dy, dz float64, y int) bool {
		idx := y - cc.minGenY
		if idx-1 < 0 || idx-1 >= len(widthFactors) {
			return true
		}
		return dx*dx+dz*dz*float64(widthFactors[idx-1])+dy*dy/6.0 >= 1.0
	}
}

// mthAbs ports Mth.abs(float).
func mthAbs(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
