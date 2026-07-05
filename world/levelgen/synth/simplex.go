package synth

import (
	"math"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// SimplexNoise ports net.minecraft.world.level.levelgen.synth.SimplexNoise: the
// improved-Perlin/Simplex value noise (2D + 3D). It has its OWN construction and
// permutation table (a 512-entry p[] seeded from a RandomSource, the xo/yo/zo
// offsets, the SQRT_3-derived F2/G2 2D skew constants), DISTINCT from ImprovedNoise.
// It is the primitive PerlinSimplexNoise stacks (Biome.TEMPERATURE_NOISE /
// FROZEN_TEMPERATURE_NOISE / BIOME_INFO_NOISE).
//
// Ported constant-for-constant from the bytecode (CFR, temp/cache/26.2-inner.jar):
//   - GRADIENT (the 16-entry table, shared with SimplexNoise.GRADIENT; identical to
//     the improved.go `gradient` table read from the same class, reused here).
//   - SQRT_3 = Math.sqrt(3.0); F2 = 0.5*(SQRT_3-1.0); G2 = (3.0-SQRT_3)/6.0.
//   - the ctor: xo/yo/zo = nextDouble()*256; p[i]=i for 0..255 then a Fisher-Yates
//     shuffle over 0..255 with offset = nextInt(256-i), swap(p[i], p[offset+i]).
//     p is 512 wide (upper half stays 0) but p(x)=p[x&0xFF] only ever reads 0..255.
//   - getValue(x,y) 2D and getValue(x,y,z) 3D (the simplex skew/unskew + the 3/4
//     corner contributions, gi = p(...)%12, getCornerNoise3D with base 0.5/0.6,
//     scaled by 70.0 (2D) / 32.0 (3D)).
//
// A faithful algorithmic port, NOT a copy of Mojang source.
type SimplexNoise struct {
	p          [512]int
	xo, yo, zo float64
}

// simplex skew constants — Biome noise math depends on the EXACT double values.
// F2 = 0.5*(sqrt(3)-1); G2 = (3-sqrt(3))/6. Computed the SAME way Java computes
// them (Math.sqrt(3.0)) so the last-bit representation matches the jar.
var (
	simplexSqrt3 = mathSqrt3()
	simplexF2    = 0.5 * (simplexSqrt3 - 1.0)
	simplexG2    = (3.0 - simplexSqrt3) / 6.0
)

// mathSqrt3 returns Math.sqrt(3.0) — isolated so the F2/G2 initializers read a
// single deterministic constant (Go's math.Sqrt is IEEE-754 correctly-rounded, as
// is Java's Math.sqrt, so the bits match).
func mathSqrt3() float64 { return math.Sqrt(3.0) }

// NewSimplexNoise seeds the permutation table + offsets from a RandomSource, exactly
// as SimplexNoise(RandomSource): xo/yo/zo = nextDouble()*256; p[i]=i (0..255); then
// for i in 0..255: offset = nextInt(256-i); swap(p[i], p[offset+i]).
func NewSimplexNoise(r levelgen.RandomSource) *SimplexNoise {
	s := &SimplexNoise{}
	s.xo = r.NextDouble() * 256.0
	s.yo = r.NextDouble() * 256.0
	s.zo = r.NextDouble() * 256.0
	for i := 0; i < 256; i++ {
		s.p[i] = i
	}
	for i := 0; i < 256; i++ {
		offset := int(r.NextIntN(int32(256 - i)))
		tmp := s.p[i]
		s.p[i] = s.p[offset+i]
		s.p[offset+i] = tmp
	}
	return s
}

// Offset accessors (the seeded xo/yo/zo). PerlinSimplexNoise reads the zero octave's
// xo/yo/zo (for the high-frequency seed and the useNoiseStart offsets), and they are
// exposed for golden testing.
func (s *SimplexNoise) OffsetX() float64 { return s.xo }
func (s *SimplexNoise) OffsetY() float64 { return s.yo }
func (s *SimplexNoise) OffsetZ() float64 { return s.zo }

// p mirrors SimplexNoise.p(int): p[x & 0xFF].
func (s *SimplexNoise) pp(x int) int { return s.p[x&0xFF] }

// getCornerNoise3D ports SimplexNoise.getCornerNoise3D(index,x,y,z,base):
//
//	t0 = base - x*x - y*y - z*z; if (t0 < 0) 0 else { t0*=t0; t0*t0*dot(GRADIENT[index],x,y,z) }
func getCornerNoise3D(index int, x, y, z, base float64) float64 {
	t0 := base - x*x - y*y - z*z
	if t0 < 0.0 {
		return 0.0
	}
	t0 *= t0
	return t0 * t0 * simplexDot(gradient[index], x, y, z)
}

// simplexDot ports SimplexNoise.dot(int[] g, x, y, z) = g[0]*x + g[1]*y + g[2]*z.
// gradient values are ints in Java; the multiply is (double)g[i]*coord.
func simplexDot(g [3]float64, x, y, z float64) float64 {
	return g[0]*x + g[1]*y + g[2]*z
}

// GetValue2D ports SimplexNoise.getValue(double xin, double yin) — the 2D simplex
// value, scaled by 70.0.
func (s *SimplexNoise) GetValue2D(xin, yin float64) float64 {
	skew := (xin + yin) * simplexF2
	i := mthFloor(xin + skew)
	j := mthFloor(yin + skew)
	t := float64(i+j) * simplexG2
	x0 := xin - (float64(i) - t)
	y0 := yin - (float64(j) - t)
	var i1, j1 int
	if x0 > y0 {
		i1 = 1
		j1 = 0
	} else {
		i1 = 0
		j1 = 1
	}
	x1 := x0 - float64(i1) + simplexG2
	y1 := y0 - float64(j1) + simplexG2
	x2 := x0 - 1.0 + 2.0*simplexG2
	y2 := y0 - 1.0 + 2.0*simplexG2
	ii := i & 0xFF
	jj := j & 0xFF
	gi0 := s.pp(ii+s.pp(jj)) % 12
	gi1 := s.pp(ii+i1+s.pp(jj+j1)) % 12
	gi2 := s.pp(ii+1+s.pp(jj+1)) % 12
	n0 := getCornerNoise3D(gi0, x0, y0, 0.0, 0.5)
	n1 := getCornerNoise3D(gi1, x1, y1, 0.0, 0.5)
	n2 := getCornerNoise3D(gi2, x2, y2, 0.0, 0.5)
	return 70.0 * (n0 + n1 + n2)
}

// GetValue3D ports SimplexNoise.getValue(double xin, double yin, double zin) — the 3D
// simplex value, scaled by 32.0. The skew factor F3 = 1/3 and unskew G3 = 1/6 are the
// exact double literals from the bytecode (0.3333333333333333 / 0.16666666666666666).
func (s *SimplexNoise) GetValue3D(xin, yin, zin float64) float64 {
	const f3 = 0.3333333333333333
	const g3 = 0.16666666666666666
	skew := (xin + yin + zin) * f3
	i := mthFloor(xin + skew)
	j := mthFloor(yin + skew)
	k := mthFloor(zin + skew)
	t := float64(i+j+k) * g3
	x0 := xin - (float64(i) - t)
	y0 := yin - (float64(j) - t)
	z0 := zin - (float64(k) - t)
	var i1, j1, k1, i2, j2, k2 int
	if x0 >= y0 {
		if y0 >= z0 {
			i1, j1, k1, i2, j2, k2 = 1, 0, 0, 1, 1, 0
		} else if x0 >= z0 {
			i1, j1, k1, i2, j2, k2 = 1, 0, 0, 1, 0, 1
		} else {
			i1, j1, k1, i2, j2, k2 = 0, 0, 1, 1, 0, 1
		}
	} else if y0 < z0 {
		i1, j1, k1, i2, j2, k2 = 0, 0, 1, 0, 1, 1
	} else if x0 < z0 {
		i1, j1, k1, i2, j2, k2 = 0, 1, 0, 0, 1, 1
	} else {
		i1, j1, k1, i2, j2, k2 = 0, 1, 0, 1, 1, 0
	}
	x1 := x0 - float64(i1) + g3
	y1 := y0 - float64(j1) + g3
	z1 := z0 - float64(k1) + g3
	x2 := x0 - float64(i2) + f3
	y2 := y0 - float64(j2) + f3
	z2 := z0 - float64(k2) + f3
	x3 := x0 - 1.0 + 0.5
	y3 := y0 - 1.0 + 0.5
	z3 := z0 - 1.0 + 0.5
	ii := i & 0xFF
	jj := j & 0xFF
	kk := k & 0xFF
	gi0 := s.pp(ii+s.pp(jj+s.pp(kk))) % 12
	gi1 := s.pp(ii+i1+s.pp(jj+j1+s.pp(kk+k1))) % 12
	gi2 := s.pp(ii+i2+s.pp(jj+j2+s.pp(kk+k2))) % 12
	gi3 := s.pp(ii+1+s.pp(jj+1+s.pp(kk+1))) % 12
	n0 := getCornerNoise3D(gi0, x0, y0, z0, 0.6)
	n1 := getCornerNoise3D(gi1, x1, y1, z1, 0.6)
	n2 := getCornerNoise3D(gi2, x2, y2, z2, 0.6)
	n3 := getCornerNoise3D(gi3, x3, y3, z3, 0.6)
	return 32.0 * (n0 + n1 + n2 + n3)
}
