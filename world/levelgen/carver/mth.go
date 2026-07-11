package carver

import "math"

// sinTable ports net.minecraft.util.Mth.SIN: a precomputed 65536-entry float lookup
// table. The carver's yaw/pitch random walk steps through Mth.sin/Mth.cos, so routing
// those calls through this table (instead of math.Sin/math.Cos) is REQUIRED for
// seed-for-seed tunnel-path parity -- the table quantizes the angle to one of 65536
// samples, and after a few walk segments the accumulated difference vs a real-number
// sin/cos diverges the path.
//
// CITE: Mth.<clinit> lambda$static$0 fills SIN[i] = (float)Math.sin((double)i /
// 10430.378350470453). (10430.378350470453 == 65536 / (2*PI).)
var sinTable = func() [65536]float32 {
	var t [65536]float32
	for i := 0; i < len(t); i++ {
		t[i] = float32(math.Sin(float64(i) / mthSinScale))
	}
	return t
}()

// mthSinScale ports the Mth constant used by Mth.sin/cos/<clinit>:
// double 10430.378350470453.
const mthSinScale = 10430.378350470453

// mthSin ports net.minecraft.util.Mth.sin(double):
// SIN[(int)((long)(d * 10430.378350470453) & 65535L)].
// The truncation to long happens on the full product BEFORE the mask, so negative
// arguments wrap correctly (two's-complement & 65535 keeps the low 16 bits).
func mthSin(d float64) float32 {
	return sinTable[int(int64(d*mthSinScale)&65535)]
}

// mthCos ports net.minecraft.util.Mth.cos(double):
// SIN[(int)((long)(d * 10430.378350470453 + 16384.0) & 65535L)].
func mthCos(d float64) float32 {
	return sinTable[int(int64(d*mthSinScale+16384.0)&65535)]
}
