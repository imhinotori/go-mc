package synth

import (
	"math"
	"strconv"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// wrap mirrors PerlinNoise.wrap(double): d - lfloor(d/3.3554432E7 + 0.5)*3.3554432E7.
// It keeps coordinates in a range where double precision stays well-behaved.
func wrap(d float64) float64 {
	const r = 3.3554432e7
	return d - float64(int64(math.Floor(d/r+0.5)))*r
}

// PerlinNoise is an octave stack of ImprovedNoise.
// Source: net.minecraft.world.level.levelgen.synth.PerlinNoise.
type PerlinNoise struct {
	noiseLevels           []*ImprovedNoise // one ImprovedNoise per active octave (nil for zero-amplitude)
	firstOctave           int
	amplitudes            []float64
	lowestFreqInputFactor float64
	lowestFreqValueFactor float64
	maxValue              float64
}

// NewPerlinNoise builds the xoroshiro-seeded octave stack:
// PerlinNoise.create(rs, firstOctave, amplitudes) = new PerlinNoise(rs, (firstOctave, amplitudes), true).
// firstOctave may be negative (e.g. temperature = -10).
func NewPerlinNoise(rs levelgen.RandomSource, firstOctave int, amplitudes []float64) *PerlinNoise {
	return newPerlinNoise(rs, firstOctave, amplitudes, true)
}

// newPerlinNoise is the protected ctor. xoroshiro=true seeds each octave via the
// positional factory fromHashOf("octave_"+octave); xoroshiro=false seeds them
// sequentially from rs (the legacy path BlendedNoise uses).
func newPerlinNoise(rs levelgen.RandomSource, firstOctave int, amplitudes []float64, xoroshiro bool) *PerlinNoise {
	p := &PerlinNoise{firstOctave: firstOctave, amplitudes: amplitudes}
	size := len(amplitudes)
	negFirst := -firstOctave // Java's `int j = -firstOctave`
	p.noiseLevels = make([]*ImprovedNoise, size)

	if xoroshiro {
		factory := rs.ForkPositional()
		for i := 0; i < size; i++ {
			if amplitudes[i] != 0 {
				octave := firstOctave + i
				p.noiseLevels[i] = NewImprovedNoise(factory.FromHashOf("octave_" + strconv.Itoa(octave)))
			}
		}
	} else {
		// Legacy sequential seeding (PerlinNoise ctor, boolean false branch).
		shared := NewImprovedNoise(rs)
		if negFirst >= 0 && negFirst < size && amplitudes[negFirst] != 0 {
			p.noiseLevels[negFirst] = shared
		}
		for i := negFirst - 1; i >= 0; i-- {
			if i < size {
				if amplitudes[i] != 0 {
					p.noiseLevels[i] = NewImprovedNoise(rs)
				} else {
					skipOctave(rs)
				}
			} else {
				skipOctave(rs)
			}
		}
	}

	p.lowestFreqInputFactor = math.Pow(2, float64(-negFirst))
	p.lowestFreqValueFactor = math.Pow(2, float64(size-1)) / (math.Pow(2, float64(size)) - 1)
	p.maxValue = p.edgeValue(2.0)
	return p
}

// skipOctave advances the rng by the per-octave draw count (ImprovedNoise ctor
// consumes 3 nextDouble + 256 nextInt = 262 draws). Java PerlinNoise.skipOctave.
func skipOctave(rs levelgen.RandomSource) { rs.ConsumeCount(262) }

// GetValue samples the octave stack (PerlinNoise.getValue, 3-arg overload).
func (p *PerlinNoise) GetValue(x, y, z float64) float64 {
	result := 0.0
	inputFactor := p.lowestFreqInputFactor
	valueFactor := p.lowestFreqValueFactor
	for i := 0; i < len(p.noiseLevels); i++ {
		n := p.noiseLevels[i]
		if n != nil {
			v := n.NoiseWithFade(
				wrap(x*inputFactor),
				wrap(y*inputFactor),
				wrap(z*inputFactor),
				0, 0,
			)
			result += p.amplitudes[i] * v * valueFactor
		}
		inputFactor *= 2
		valueFactor /= 2
	}
	return result
}

// edgeValue computes the amplitude-weighted sum at a fixed sample magnitude x
// (PerlinNoise.edgeValue) — used to derive maxValue.
func (p *PerlinNoise) edgeValue(x float64) float64 {
	result := 0.0
	valueFactor := p.lowestFreqValueFactor
	for i := 0; i < len(p.noiseLevels); i++ {
		if p.noiseLevels[i] != nil {
			result += p.amplitudes[i] * x * valueFactor
		}
		valueFactor /= 2
	}
	return result
}

// maxBrokenValue = edgeValue(x + 2.0) (PerlinNoise.maxBrokenValue), used by
// BlendedNoise's maxValue.
func (p *PerlinNoise) maxBrokenValue(x float64) float64 { return p.edgeValue(x + 2.0) }

// MaxValue returns the cached maxValue (PerlinNoise.maxValue).
func (p *PerlinNoise) MaxValue() float64 { return p.maxValue }

// FirstOctave returns the first (lowest) octave index.
func (p *PerlinNoise) FirstOctave() int { return p.firstOctave }

// getOctaveNoise mirrors PerlinNoise.getOctaveNoise(i): noiseLevels[len-1-i]
// (REVERSED indexing — i=0 is the HIGHEST-frequency octave). BlendedNoise relies
// on this order.
func (p *PerlinNoise) getOctaveNoise(i int) *ImprovedNoise {
	return p.noiseLevels[len(p.noiseLevels)-1-i]
}
