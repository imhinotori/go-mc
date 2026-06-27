package structure

import "math"

// beardKernel ports Beardifier.BEARD_KERNEL (26.2-inner.jar): a 24*24*24 = 13824 float32
// gaussian kernel built once at init. Vanilla fills it via Util.make + lambda$static$0:
//
//	for i in [0,24): for j in [0,24): for k in [0,24):
//	  BEARD_KERNEL[i*24*24 + j*24 + k] = (float) computeBeardContribution(j-12, k-12, i-12)
//
// where computeBeardContribution(int dx, int dy, int dz) =
//   computeBeardContribution(dx, (double)dy + 0.5, dz) =
//   Math.pow(E, -lengthSquared(dx, dy+0.5, dz) / 16.0).
//
// BEARD_KERNEL_RADIUS = 12, BEARD_KERNEL_SIZE = 24. Stored float32 (the jar's d2f cast) so
// getBeardContribution's lookup is bit-identical to the jar.
//
// Cite: javap Beardifier static{} / lambda$static$0 / computeBeardContribution(III) /
// computeBeardContribution(IDI).
var beardKernel = makeBeardKernel()

func makeBeardKernel() []float32 {
	k := make([]float32, 24*24*24)
	for i := 0; i < 24; i++ {
		for j := 0; j < 24; j++ {
			for kk := 0; kk < 24; kk++ {
				k[i*24*24+j*24+kk] = float32(computeBeardContribution(j-12, kk-12, i-12))
			}
		}
	}
	return k
}

// computeBeardContribution ports Beardifier.computeBeardContribution(int dx, int dy, int dz):
// delegates to the (int, double, int) overload with dy+0.5.
func computeBeardContribution(dx, dy, dz int) float64 {
	return computeBeardContributionD(dx, float64(dy)+0.5, dz)
}

// computeBeardContributionD ports Beardifier.computeBeardContribution(int dx, double dy, int dz):
// Math.pow(E, -lengthSquared(dx, dy, dz) / 16.0). Cite: javap (uses ldc E + Math.pow + /16.0).
func computeBeardContributionD(dx int, dy float64, dz int) float64 {
	lengthSquared := mthLengthSquared3(float64(dx), dy, float64(dz))
	return math.Pow(math.E, -lengthSquared/16.0)
}
