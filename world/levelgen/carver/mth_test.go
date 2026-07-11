package carver

import (
	"math"
	"testing"
)

// TestMthSinTableMatchesJava checks the ported Mth SIN table reproduces the vanilla
// table build (SIN[i] = (float)Math.sin(i / 10430.378350470453)) and the sin/cos index
// math, at a few known angles.
func TestMthSinTableMatchesJava(t *testing.T) {
	// The table is coarse (65536 entries over 2*PI), so we compare against the SAME
	// quantized definition vanilla uses, not against a continuous sin.
	cases := []float64{0, 0.5, 1.0, 1.5707963705062866, math.Pi, 3.1415927, 6.2831855, -1.0, -3.14}
	for _, d := range cases {
		wantSin := float32(math.Sin(float64(int64(d*mthSinScale)&65535) / mthSinScale))
		if got := mthSin(d); got != wantSin {
			t.Fatalf("mthSin(%v)=%v want %v", d, got, wantSin)
		}
		wantCos := float32(math.Sin(float64(int64(d*mthSinScale+16384.0)&65535) / mthSinScale))
		if got := mthCos(d); got != wantCos {
			t.Fatalf("mthCos(%v)=%v want %v", d, got, wantCos)
		}
	}

	// mthSin(PI/2) must be ~1 (the createRoom horizontal-radius angle).
	if s := mthSin(1.5707963705062866); math.Abs(float64(s)-1.0) > 1e-3 {
		t.Fatalf("mthSin(PI/2)=%v want ~1", s)
	}
	// mthCos(0) must be ~1.
	if c := mthCos(0); math.Abs(float64(c)-1.0) > 1e-4 {
		t.Fatalf("mthCos(0)=%v want ~1", c)
	}
}

// TestMthSinDiffersFromMathSin: the table is a QUANTIZED sin, so at a generic angle the
// 16-bit-truncated table lookup differs from the continuous math.Sin. This is exactly
// why the carver MUST use the table (real-number sin diverges from vanilla seeds).
func TestMthSinDiffersFromMathSin(t *testing.T) {
	// An angle whose 10430.378* index is NOT close to an integer -> a visible gap.
	d := 0.12345
	if float64(mthSin(d)) == math.Sin(d) {
		t.Fatalf("mthSin(%v) unexpectedly equals math.Sin exactly; table not quantized", d)
	}
	// But it must still be CLOSE (within one table step's worth of error).
	if math.Abs(float64(mthSin(d))-math.Sin(d)) > 1e-3 {
		t.Fatalf("mthSin(%v)=%v too far from math.Sin=%v", d, mthSin(d), math.Sin(d))
	}
}
