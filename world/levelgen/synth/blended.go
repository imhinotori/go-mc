package synth

import (
	"github.com/imhinotori/sulfur/world/levelgen"
)

// BlendedNoise is the legacy "main" terrain noise the overworld old_blended_noise
// (base_3d_noise) node uses. Source:
// net.minecraft.world.level.levelgen.synth.BlendedNoise.
//
// It stacks three legacy PerlinNoise (minLimit/maxLimit over octaves -15..0,
// main over -7..0), seeded sequentially from the RandomSource via
// PerlinNoise.createLegacyForBlendedNoise (the xoroshiro=false octave path).
type BlendedNoise struct {
	minLimitNoise *PerlinNoise
	maxLimitNoise *PerlinNoise
	mainNoise     *PerlinNoise

	xzMultiplier         float64
	yMultiplier          float64
	xzFactor             float64
	yFactor              float64
	smearScaleMultiplier float64
}

// ones returns an amplitude slice of length (hi-lo+1) filled with 1.0 — the
// legacy octave amplitudes for the -15..0 / -7..0 ranges (IntStream.rangeClosed
// boxed into the amplitude set).
func ones(lo, hi int) []float64 {
	a := make([]float64, hi-lo+1)
	for i := range a {
		a[i] = 1
	}
	return a
}

// NewBlendedNoise mirrors BlendedNoise(rs, xzScale, yScale, xzFactor, yFactor,
// smearScaleMultiplier): the base_3d_noise scales are xzScale 0.25, yScale 0.125,
// xzFactor 80, yFactor 160, smearScaleMultiplier 8.
func NewBlendedNoise(rs levelgen.RandomSource, xzScale, yScale, xzFactor, yFactor, smearScaleMultiplier float64) *BlendedNoise {
	b := &BlendedNoise{}
	// createLegacyForBlendedNoise(rs, rangeClosed(-15,0)) — xoroshiro=false.
	b.minLimitNoise = newPerlinNoise(rs, -15, ones(-15, 0), false)
	b.maxLimitNoise = newPerlinNoise(rs, -15, ones(-15, 0), false)
	b.mainNoise = newPerlinNoise(rs, -7, ones(-7, 0), false)

	b.xzMultiplier = 684.412 * xzScale
	b.yMultiplier = 684.412 * yScale
	b.xzFactor = xzFactor
	b.yFactor = yFactor
	b.smearScaleMultiplier = smearScaleMultiplier
	return b
}

// Compute samples the blended noise at block coords (BlendedNoise.compute).
// The density-function FunctionContext supplies blockX/Y/Z; here we take them
// directly so the synth package stays free of the density layer.
func (b *BlendedNoise) Compute(blockX, blockY, blockZ int) float64 {
	x := float64(blockX) * b.xzMultiplier
	y := float64(blockY) * b.yMultiplier
	z := float64(blockZ) * b.xzMultiplier

	xMain := x / b.xzFactor
	yMain := y / b.yFactor
	zMain := z / b.xzFactor
	ySmear := b.yMultiplier * b.smearScaleMultiplier
	yMainSmear := ySmear / b.yFactor

	var minLimit, maxLimit, main float64

	// main noise: 8 octaves, persistence halving each step.
	persistence := 1.0
	for i := 0; i < 8; i++ {
		oct := b.mainNoise.getOctaveNoise(i)
		if oct != nil {
			main += oct.NoiseWithFade(
				wrap(xMain*persistence),
				wrap(yMain*persistence),
				wrap(zMain*persistence),
				yMainSmear*persistence,
				yMain*persistence,
			) / persistence
		}
		persistence /= 2
	}

	mainBlend := (main/10.0 + 1.0) / 2.0
	skipMin := mainBlend >= 1.0
	skipMax := mainBlend <= 0.0

	// min/max limit noise: 16 octaves.
	persistence = 1.0
	for i := 0; i < 16; i++ {
		wx := wrap(x * persistence)
		wy := wrap(y * persistence)
		wz := wrap(z * persistence)
		smear := ySmear * persistence
		if !skipMin {
			if oct := b.minLimitNoise.getOctaveNoise(i); oct != nil {
				minLimit += oct.NoiseWithFade(wx, wy, wz, smear, y*persistence) / persistence
			}
		}
		if !skipMax {
			if oct := b.maxLimitNoise.getOctaveNoise(i); oct != nil {
				maxLimit += oct.NoiseWithFade(wx, wy, wz, smear, y*persistence) / persistence
			}
		}
		persistence /= 2
	}

	return clampedLerp(minLimit/512.0, maxLimit/512.0, mainBlend) / 128.0
}

// MaxValue mirrors BlendedNoise.maxValue: minLimitNoise.maxBrokenValue(yMultiplier).
func (b *BlendedNoise) MaxValue() float64 {
	return b.minLimitNoise.maxBrokenValue(b.yMultiplier)
}
