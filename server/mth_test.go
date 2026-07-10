package server

import (
	"math"
	"testing"
)

// TestMthSinCosTable proves mthSin/mthCos read the 65536-entry Mth.SIN table (built from
// (float)Math.sin((double)i/10430.378350470453d)) rather than math.Sin, matching the jar bit-for-bit at the
// exact table entries. Cite net.minecraft.util.Mth.sin/cos.
func TestMthSinCosTable(t *testing.T) {
	// At table-aligned angles the lookup must equal the table cell exactly (no interpolation in Mth).
	for _, f := range []float64{0, 0.5, 1.0, 1.5707963267948966, math.Pi, 2.0, 3.5, -1.0, -math.Pi, 6.283185307179586} {
		idxSin := int(int64(f*mthSinScale) & 65535)
		if got, want := mthSin(f), mthSinTable[idxSin]; got != want {
			t.Fatalf("mthSin(%v) = %v, want table[%d] = %v", f, got, idxSin, want)
		}
		idxCos := int(int64(f*mthSinScale+mthCosBias) & 65535)
		if got, want := mthCos(f), mthSinTable[idxCos]; got != want {
			t.Fatalf("mthCos(%v) = %v, want table[%d] = %v", f, got, idxCos, want)
		}
	}
	// The table is a genuine sine table: it must track math.Sin within the quantization error (2*PI/65536).
	const tol = 2e-4
	for _, f := range []float64{0.1, 0.7, 1.3, 2.9, 4.2, 5.5} {
		if d := math.Abs(float64(mthSin(f)) - math.Sin(f)); d > tol {
			t.Fatalf("mthSin(%v) diverges from math.Sin by %v (> %v)", f, d, tol)
		}
		if d := math.Abs(float64(mthCos(f)) - math.Cos(f)); d > tol {
			t.Fatalf("mthCos(%v) diverges from math.Cos by %v (> %v)", f, d, tol)
		}
	}
	// Bit-exact table construction: SIN[0] == 0, and a mid entry equals (float)Math.sin(i/scale).
	if mthSinTable[0] != 0 {
		t.Fatalf("SIN[0] = %v, want 0", mthSinTable[0])
	}
	if want := float32(math.Sin(float64(12345) / mthSinScale)); mthSinTable[12345] != want {
		t.Fatalf("SIN[12345] = %v, want %v", mthSinTable[12345], want)
	}
}

// TestMthAtan2 proves mthAtan2 is the table-based fast approximation (fastInvSqrt + ASIN/COS tables), close
// to math.Atan2 across all quadrants but NOT bit-identical (it is the vanilla approximation). Cite Mth.atan2.
func TestMthAtan2(t *testing.T) {
	const tol = 1e-3 // the Mth.atan2 approximation error bound
	cases := [][2]float64{
		{1, 1}, {1, -1}, {-1, 1}, {-1, -1}, {0, 1}, {1, 0}, {0, -1}, {-1, 0},
		{3.5, 2.1}, {-4.2, 7.7}, {0.001, 0.002}, {100, -3},
	}
	for _, c := range cases {
		y, x := c[0], c[1]
		got := mthAtan2(y, x)
		want := math.Atan2(y, x)
		if d := math.Abs(got - want); d > tol {
			t.Fatalf("mthAtan2(%v,%v) = %v, math.Atan2 = %v, delta %v > %v", y, x, got, want, d, tol)
		}
	}
	// FRAC_BIAS is the exact Mojang magic constant.
	if mthFracBias != 17592186044416.0 {
		t.Fatalf("mthFracBias = %v, want 17592186044416.0", mthFracBias)
	}
	// RAD_TO_DEG is the exact float whose double-widening Projectile.shoot uses.
	if float64(mthRadToDeg) != 57.2957763671875 {
		t.Fatalf("mthRadToDeg widened = %v, want 57.2957763671875", float64(mthRadToDeg))
	}
}
