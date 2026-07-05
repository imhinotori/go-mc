package synth

import (
	"math"
	"sort"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// PerlinSimplexNoise ports net.minecraft.world.level.levelgen.synth.PerlinSimplexNoise:
// the octave wrapper over SimplexNoise. Given a sorted set of octave indices it builds
// one SimplexNoise per active octave (consuming the RandomSource in the SAME draw order
// as the jar), then getValue(x,y,useNoiseStart) sums the octave contributions with the
// highestFreq input/value factor stack.
//
// Ported constant-for-constant from the bytecode (CFR, temp/cache/26.2-inner.jar):
//
//	PerlinSimplexNoise(RandomSource, List<Integer> octaveSet):
//	  octaveSet is de-duplicated + sorted ascending (IntRBTreeSet).
//	  lowFreqOctaves  = -octaveSet.firstInt();   highFreqOctaves = octaveSet.lastInt();
//	  octaves = lowFreqOctaves + highFreqOctaves + 1;   (must be >= 1)
//	  zeroOctave = new SimplexNoise(random);           // always constructed first
//	  zeroOctaveIndex = highFreqOctaves;
//	  noiseLevels = new SimplexNoise[octaves];
//	  if (0 <= zeroOctaveIndex < octaves && octaveSet.contains(0)) noiseLevels[zeroOctaveIndex]=zeroOctave;
//	  for i in (zeroOctaveIndex+1 .. octaves-1):
//	      if (i>=0 && octaveSet.contains(zeroOctaveIndex - i)) noiseLevels[i]=new SimplexNoise(random);
//	      else random.consumeCount(262);
//	  if (highFreqOctaves > 0):
//	      seed = (long)(zeroOctave.getValue(xo,yo,zo) * 9.223372036854776E18);
//	      hfRandom = new WorldgenRandom(new LegacyRandomSource(seed));
//	      for i in (zeroOctaveIndex-1 .. 0):
//	          if (i<octaves && octaveSet.contains(zeroOctaveIndex - i)) noiseLevels[i]=new SimplexNoise(hfRandom);
//	          else hfRandom.consumeCount(262);
//	  highestFreqInputFactor = 2^highFreqOctaves;
//	  highestFreqValueFactor = 1 / (2^octaves - 1);
//
//	getValue(x,y,useNoiseStart):
//	  value=0; factor=highestFreqInputFactor; valueFactor=highestFreqValueFactor;
//	  for each noiseLevel (in array order):
//	     if noiseLevel != null:
//	        value += noiseLevel.getValue(x*factor + (useNoiseStart? xo:0), y*factor + (useNoiseStart? yo:0)) * valueFactor;
//	     factor /= 2; valueFactor *= 2;
//
// A faithful algorithmic port, NOT a copy of Mojang source. The SimplexNoise ctor
// consumes 3 nextDouble + 256 nextInt(...) = 262 draws, so consumeCount(262) skips
// exactly one octave's worth of the RandomSource — matching PerlinNoise.skipOctave.
type PerlinSimplexNoise struct {
	noiseLevels            []*SimplexNoise // one per octave (nil where the octave is inactive)
	highestFreqValueFactor float64
	highestFreqInputFactor float64
}

// perlinSimplexOctaveCost is the SimplexNoise ctor draw count (3 nextDouble + 256
// nextInt) — consumeCount(262) skips exactly one octave. Mirrors the jar's literal 262.
const perlinSimplexOctaveCost = 262

// NewPerlinSimplexNoise ports the PerlinSimplexNoise(RandomSource, List<Integer>) ctor.
// octaveSet is taken as a raw int list (e.g. []int{-2,-1,0}); it is de-duplicated and
// sorted ascending here to reproduce the IntRBTreeSet the jar wraps it in.
func NewPerlinSimplexNoise(r levelgen.RandomSource, octaveSet []int) *PerlinSimplexNoise {
	// IntRBTreeSet: sorted ascending + de-duplicated.
	uniq := make([]int, 0, len(octaveSet))
	seen := make(map[int]bool, len(octaveSet))
	for _, o := range octaveSet {
		if !seen[o] {
			seen[o] = true
			uniq = append(uniq, o)
		}
	}
	if len(uniq) == 0 {
		panic("synth: PerlinSimplexNoise: Need some octaves!")
	}
	sort.Ints(uniq)
	first := uniq[0]
	last := uniq[len(uniq)-1]
	contains := func(v int) bool { return seen[v] }

	lowFreqOctaves := -first
	highFreqOctaves := last
	octaves := lowFreqOctaves + highFreqOctaves + 1
	if octaves < 1 {
		panic("synth: PerlinSimplexNoise: Total number of octaves needs to be >= 1")
	}

	ps := &PerlinSimplexNoise{noiseLevels: make([]*SimplexNoise, octaves)}

	// zeroOctave is ALWAYS constructed first (consuming r), regardless of membership.
	zeroOctave := NewSimplexNoise(r)
	zeroOctaveIndex := highFreqOctaves
	if zeroOctaveIndex >= 0 && zeroOctaveIndex < octaves && contains(0) {
		ps.noiseLevels[zeroOctaveIndex] = zeroOctave
	}
	for i := zeroOctaveIndex + 1; i < octaves; i++ {
		if i >= 0 && contains(zeroOctaveIndex-i) {
			ps.noiseLevels[i] = NewSimplexNoise(r)
		} else {
			r.ConsumeCount(perlinSimplexOctaveCost)
		}
	}
	if highFreqOctaves > 0 {
		positiveOctaveSeed := int64(zeroOctave.GetValue3D(zeroOctave.xo, zeroOctave.yo, zeroOctave.zo) * 9.223372036854776e18)
		hfRandom := levelgen.NewWorldgenRandom(positiveOctaveSeed)
		for i := zeroOctaveIndex - 1; i >= 0; i-- {
			if i < octaves && contains(zeroOctaveIndex-i) {
				ps.noiseLevels[i] = NewSimplexNoise(hfRandom)
			} else {
				hfRandom.ConsumeCount(perlinSimplexOctaveCost)
			}
		}
	}

	ps.highestFreqInputFactor = math.Pow(2.0, float64(highFreqOctaves))
	ps.highestFreqValueFactor = 1.0 / (math.Pow(2.0, float64(octaves)) - 1.0)
	return ps
}

// GetValue ports PerlinSimplexNoise.getValue(double x, double y, boolean useNoiseStart):
// sum each active octave's 2D SimplexNoise value with the halving input/value factor
// stack. useNoiseStart adds the octave's xo/yo offset to the sample coordinate.
func (ps *PerlinSimplexNoise) GetValue(x, y float64, useNoiseStart bool) float64 {
	value := 0.0
	factor := ps.highestFreqInputFactor
	valueFactor := ps.highestFreqValueFactor
	for _, n := range ps.noiseLevels {
		if n != nil {
			ox, oy := 0.0, 0.0
			if useNoiseStart {
				ox = n.xo
				oy = n.yo
			}
			value += n.GetValue2D(x*factor+ox, y*factor+oy) * valueFactor
		}
		factor /= 2.0
		valueFactor *= 2.0
	}
	return value
}
