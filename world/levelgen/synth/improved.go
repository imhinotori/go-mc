// Package synth ports the Minecraft 26.2 (protocol 776) noise primitives
// DIRECTLY from the unobfuscated server jar (temp/cache/26.2-inner.jar, read via
// `javap -c`): ImprovedNoise (single-octave improved-Perlin), PerlinNoise (the
// octave stack), NormalNoise (the 2-PerlinNoise normalized noise the density
// graph calls), and BlendedNoise (the legacy old_blended_noise the overworld
// base_3d_noise uses). All randomness flows from the Tier-A Xoroshiro source
// (world/levelgen.RandomSource) — pure + deterministic over the world seed.
//
// These are constant-for-constant ports (the gradient table, the octave factors,
// the INPUT_FACTOR/expectedDeviation/valueFactor constants, the base_3d_noise
// scales are all read from the bytecode), not copies of Mojang source.
package synth

import (
	"math"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// gradient is net.minecraft.world.level.levelgen.synth.SimplexNoise.GRADIENT —
// the canonical 16-entry improved-Perlin gradient table, read from the bytecode.
var gradient = [16][3]float64{
	{1, 1, 0}, {-1, 1, 0}, {1, -1, 0}, {-1, -1, 0},
	{1, 0, 1}, {-1, 0, 1}, {1, 0, -1}, {-1, 0, -1},
	{0, 1, 1}, {0, -1, 1}, {0, 1, -1}, {0, -1, -1},
	{1, 1, 0}, {0, -1, 1}, {-1, 1, 0}, {0, -1, -1},
}

// ---- Mth helpers (net.minecraft.util.Mth), ported constant-for-constant ----

// mthFloor = (int)Math.floor(d).
func mthFloor(d float64) int { return int(math.Floor(d)) }

// smoothstep = t*t*t*(t*(t*6-15)+10) — the 6t^5-15t^4+10t^3 fade curve.
func smoothstep(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

// lerp = a + t*(b-a).
func lerp(t, a, b float64) float64 { return a + t*(b-a) }

// lerp2 / lerp3 = bilinear / trilinear interpolation (Mth.lerp2 / Mth.lerp3).
func lerp2(tx, ty, a, b, c, d float64) float64 {
	return lerp(ty, lerp(tx, a, b), lerp(tx, c, d))
}
func lerp3(tx, ty, tz, a, b, c, d, e, f, g, h float64) float64 {
	return lerp(tz, lerp2(tx, ty, a, b, c, d), lerp2(tx, ty, e, f, g, h))
}

// clampedLerp clamps t to [0,1] before lerping (Mth.clampedLerp).
func clampedLerp(a, b, t float64) float64 {
	switch {
	case t < 0:
		return a
	case t > 1:
		return b
	default:
		return lerp(t, a, b)
	}
}

// gradDot computes GRADIENT[hash&15] . (x,y,z) (ImprovedNoise.gradDot + SimplexNoise.dot).
func gradDot(hash int, x, y, z float64) float64 {
	g := gradient[hash&15]
	return g[0]*x + g[1]*y + g[2]*z
}

// ImprovedNoise is a single-octave improved-Perlin noise.
// Source: net.minecraft.world.level.levelgen.synth.ImprovedNoise.
type ImprovedNoise struct {
	p          [256]int // the permutation table
	xo, yo, zo float64  // the per-instance offsets
}

// NewImprovedNoise seeds the permutation table + offsets from a RandomSource.
// Java ctor: xo/yo/zo = nextDouble()*256; p[i]=i; then a Fisher-Yates shuffle
// with j = nextInt(256-i), swap(p[i], p[i+j]).
func NewImprovedNoise(r levelgen.RandomSource) *ImprovedNoise {
	im := &ImprovedNoise{}
	im.xo = r.NextDouble() * 256
	im.yo = r.NextDouble() * 256
	im.zo = r.NextDouble() * 256
	for i := 0; i < 256; i++ {
		im.p[i] = i
	}
	for i := 0; i < 256; i++ {
		j := int(r.NextIntN(int32(256 - i)))
		im.p[i], im.p[i+j] = im.p[i+j], im.p[i]
	}
	return im
}

// Offset accessors (the seeded xo/yo/zo) — exposed for golden testing.
func (im *ImprovedNoise) OffsetX() float64 { return im.xo }
func (im *ImprovedNoise) OffsetY() float64 { return im.yo }
func (im *ImprovedNoise) OffsetZ() float64 { return im.zo }

// p mirrors ImprovedNoise.p(int): p[i & 255] & 255.
func (im *ImprovedNoise) pp(i int) int { return im.p[i&255] & 255 }

// Noise samples the noise at (x,y,z) with no y-fade (the 3-arg overload).
func (im *ImprovedNoise) Noise(x, y, z float64) float64 {
	return im.NoiseWithFade(x, y, z, 0, 0)
}

// NoiseWithFade is the 5-arg overload: yScale/yMax implement the y-axis fade
// truncation BlendedNoise uses. Java ImprovedNoise.noise(x,y,z,yScale,yMax).
func (im *ImprovedNoise) NoiseWithFade(x, y, z, yScale, yMax float64) float64 {
	xx := x + im.xo
	yy := y + im.yo
	zz := z + im.zo
	fx := mthFloor(xx)
	fy := mthFloor(yy)
	fz := mthFloor(zz)
	dx := xx - float64(fx)
	dy := yy - float64(fy)
	dz := zz - float64(fz)

	var trunc float64
	if yScale != 0 {
		// Java: t = (yMax >= 0 && yMax < dy) ? yMax : dy;
		//       trunc = floor(t/yScale + 1.0000000116860974E-7) * yScale;
		t := dy
		if yMax >= 0 && yMax < dy {
			t = yMax
		}
		trunc = float64(mthFloor(t/yScale+1.0000000116860974e-7)) * yScale
	}
	return im.sampleAndLerp(fx, fy, fz, dx, dy-trunc, dz, dy)
}

// sampleAndLerp gathers the 8 corner gradients and trilerps with smoothstep
// fades (ImprovedNoise.sampleAndLerp). Note the y-fade uses the ORIGINAL dy
// (the `t` arg), while the corner gradients use the truncated dy.
func (im *ImprovedNoise) sampleAndLerp(fx, fy, fz int, dx, dy, dz, t float64) float64 {
	i := im.pp(fx)
	j := im.pp(fx + 1)
	k := im.pp(i + fy)
	l := im.pp(i + fy + 1)
	m := im.pp(j + fy)
	n := im.pp(j + fy + 1)

	d0 := gradDot(im.pp(k+fz), dx, dy, dz)
	d1 := gradDot(im.pp(m+fz), dx-1, dy, dz)
	d2 := gradDot(im.pp(l+fz), dx, dy-1, dz)
	d3 := gradDot(im.pp(n+fz), dx-1, dy-1, dz)
	d4 := gradDot(im.pp(k+fz+1), dx, dy, dz-1)
	d5 := gradDot(im.pp(m+fz+1), dx-1, dy, dz-1)
	d6 := gradDot(im.pp(l+fz+1), dx, dy-1, dz-1)
	d7 := gradDot(im.pp(n+fz+1), dx-1, dy-1, dz-1)

	sx := smoothstep(dx)
	sy := smoothstep(t)
	sz := smoothstep(dz)
	return lerp3(sx, sy, sz, d0, d1, d2, d3, d4, d5, d6, d7)
}
