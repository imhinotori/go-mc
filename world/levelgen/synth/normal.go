package synth

import (
	"math"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// inputFactor is NormalNoise.INPUT_FACTOR, read from the bytecode (the second
// PerlinNoise is sampled at coords scaled by this).
const inputFactor = 1.0181268882175227

// NormalNoise is the 2-PerlinNoise normalized noise the density-function Noise
// nodes call. Source: net.minecraft.world.level.levelgen.synth.NormalNoise.
type NormalNoise struct {
	first, second *PerlinNoise
	valueFactor   float64
	maxValue      float64
	firstOctave   int
	amplitudes    []float64
}

// expectedDeviation = 0.1 * (1.0 + 1.0/(octaves+1)) (NormalNoise.expectedDeviation).
func expectedDeviation(octaves int) float64 { return 0.1 * (1.0 + 1.0/float64(octaves+1)) }

// NewNormalNoise mirrors NormalNoise.create(rs, firstOctave, amplitudes...):
// two PerlinNoise (first + second) over the SAME (firstOctave, amplitudes), then
// a normalization valueFactor derived from the span of non-zero amplitudes.
// The (firstOctave, amplitudes) come from the embedded noise/*.json as DATA
// (Pitfall 2 — not hardcoded; Wave 2 wires params-from-JSON).
func NewNormalNoise(rs levelgen.RandomSource, firstOctave int, amplitudes []float64) *NormalNoise {
	n := &NormalNoise{firstOctave: firstOctave, amplitudes: amplitudes}
	n.first = NewPerlinNoise(rs, firstOctave, amplitudes)
	n.second = NewPerlinNoise(rs, firstOctave, amplitudes)

	// Span of indices with non-zero amplitude (Java: min/max over the list).
	jMin, jMax := math.MaxInt32, math.MinInt32
	for i, a := range amplitudes {
		if a != 0 {
			if i < jMin {
				jMin = i
			}
			if i > jMax {
				jMax = i
			}
		}
	}
	n.valueFactor = (1.0 / 6.0) / expectedDeviation(jMax-jMin)
	n.maxValue = (n.first.MaxValue() + n.second.MaxValue()) * n.valueFactor
	return n
}

// GetValue samples the normalized noise (NormalNoise.getValue):
// (first(x,y,z) + second(x*INPUT_FACTOR, y*INPUT_FACTOR, z*INPUT_FACTOR)) * valueFactor.
func (n *NormalNoise) GetValue(x, y, z float64) float64 {
	return (n.first.GetValue(x, y, z) +
		n.second.GetValue(x*inputFactor, y*inputFactor, z*inputFactor)) * n.valueFactor
}

// MaxValue returns the cached maxValue (NormalNoise.maxValue).
func (n *NormalNoise) MaxValue() float64 { return n.maxValue }

// FirstOctave / Amplitudes expose the source parameters (for graph wiring/tests).
func (n *NormalNoise) FirstOctave() int      { return n.firstOctave }
func (n *NormalNoise) Amplitudes() []float64 { return n.amplitudes }
