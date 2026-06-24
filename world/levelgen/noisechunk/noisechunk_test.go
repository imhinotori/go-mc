package noisechunk

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen/density"
)

// constFn is a tiny test density.Function: a fixed constant at every context.
type constFn float64

func (c constFn) Compute(density.Context) float64 { return float64(c) }
func (c constFn) MinValue() float64               { return float64(c) }
func (c constFn) MaxValue() float64               { return float64(c) }

// rampFn is a test density.Function whose value varies linearly with X+Y+Z, so a
// trilinear interpolation of its cell corners is exact (a linear field is reproduced
// by trilerp with zero error) — the ground truth for the cell-fill order test.
type rampFn struct{ ax, ay, az, b float64 }

func (f rampFn) Compute(c density.Context) float64 {
	return f.ax*float64(c.X) + f.ay*float64(c.Y) + f.az*float64(c.Z) + f.b
}
func (f rampFn) MinValue() float64 { return math.Inf(-1) }
func (f rampFn) MaxValue() float64 { return math.Inf(+1) }

// lerpRef mirrors net.minecraft.util.Mth.lerp(t,a,b) = a + t*(b-a) — the reference the
// port must reproduce constant-for-constant.
func lerpRef(t, a, b float64) float64 { return a + t*(b-a) }

// trilerpRef is the reference trilinear interpolation (Mth.lerp3 order) the
// interpolator must match: lerp over Y, then X, then Z.
//
//	args: noiseXYZ where X in {0,1}=slice0/slice1, Y in {0,1}, Z in {0,1}
func trilerpRef(dx, dy, dz, n000, n100, n010, n110, n001, n101, n011, n111 float64) float64 {
	xz00 := lerpRef(dy, n000, n010)
	xz10 := lerpRef(dy, n100, n110)
	xz01 := lerpRef(dy, n001, n011)
	xz11 := lerpRef(dy, n101, n111)
	z0 := lerpRef(dx, xz00, xz10)
	z1 := lerpRef(dx, xz01, xz11)
	return lerpRef(dz, z0, z1)
}

// TestTrilerpCorners: given 8 known corner values loaded into one interpolator cell,
// the staged updateForY/updateForX/updateForZ lerp reproduces the exact trilinear
// interpolation (the Mth.lerp3 order) at every in-cell offset.
func TestTrilerpCorners(t *testing.T) {
	const cellWidth, cellHeight = 4, 8
	// 8 arbitrary, distinct corner values.
	n000, n100, n010, n110 := 1.0, 2.0, 3.0, 5.0
	n001, n101, n011, n111 := 8.0, 13.0, 21.0, 34.0

	ip := newInterpolator(constFn(0), 1, 1, cellWidth, cellHeight)
	// Load the single cell's corners directly into the 2x2x2 slice grid.
	// slice0 = X=0 face, slice1 = X=1 face; first index = Z (xz), second = Y.
	ip.slice0[0][0], ip.slice0[0][1] = n000, n010
	ip.slice0[1][0], ip.slice0[1][1] = n001, n011
	ip.slice1[0][0], ip.slice1[0][1] = n100, n110
	ip.slice1[1][0], ip.slice1[1][1] = n101, n111
	ip.selectCellYZ(0, 0)

	for iy := 0; iy < cellHeight; iy++ {
		dy := float64(iy) / float64(cellHeight)
		ip.updateForY(dy)
		for ix := 0; ix < cellWidth; ix++ {
			dx := float64(ix) / float64(cellWidth)
			ip.updateForX(dx)
			for iz := 0; iz < cellWidth; iz++ {
				dz := float64(iz) / float64(cellWidth)
				ip.updateForZ(dz)
				got := ip.value()
				want := trilerpRef(dx, dy, dz, n000, n100, n010, n110, n001, n101, n011, n111)
				if math.Abs(got-want) > 1e-12 {
					t.Fatalf("trilerp(ix=%d iy=%d iz=%d): got %v want %v", ix, iy, iz, got, want)
				}
			}
		}
	}
}

// TestInterpolatorDeterministic: the interpolator over a fixed corner field yields
// identical results across two independent runs (pure, no hidden state).
func TestInterpolatorDeterministic(t *testing.T) {
	const cellWidth, cellHeight = 4, 8
	run := func() []float64 {
		ip := newInterpolator(constFn(0), 1, 1, cellWidth, cellHeight)
		ip.slice0[0][0], ip.slice0[0][1] = 0.1, 0.9
		ip.slice0[1][0], ip.slice0[1][1] = 0.2, 0.8
		ip.slice1[0][0], ip.slice1[0][1] = 0.3, 0.7
		ip.slice1[1][0], ip.slice1[1][1] = 0.4, 0.6
		ip.selectCellYZ(0, 0)
		var out []float64
		for iy := 0; iy < cellHeight; iy++ {
			ip.updateForY(float64(iy) / cellHeight)
			for ix := 0; ix < cellWidth; ix++ {
				ip.updateForX(float64(ix) / cellWidth)
				for iz := 0; iz < cellWidth; iz++ {
					ip.updateForZ(float64(iz) / cellWidth)
					out = append(out, ip.value())
				}
			}
		}
		return out
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("length mismatch %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestCellFillOrder: a linear (ramp) field is reproduced EXACTLY by the interpolator
// only when the corner buffers are filled and the staged lerp advances in vanilla's
// cell-fill order (selectCellYZ over Z faces, updateForY over the Y axis, updateForX
// over the X faces, updateForZ over Z) — a transposed loop would mis-map corners and
// break the exactness even for a linear field. The NoiseChunk-style slice fill is
// exercised here against the ramp ground truth.
func TestCellFillOrder(t *testing.T) {
	const cellWidth, cellHeight = 4, 8
	const cellCountXZ, cellCountY = 1, 1
	// firstNoiseX/Z = world cell origin in noise-cell units; pick a non-zero origin so
	// a transposed X<->Z fill would surface.
	const firstNoiseX, firstNoiseZ = 3, 7
	field := rampFn{ax: 0.5, ay: -0.25, az: 0.125, b: 1.0}

	ip := newInterpolator(field, cellCountXZ, cellCountY, cellWidth, cellHeight)

	// Drive the slice fill exactly as NoiseChunk.fillSlice would for a 1x1 cell grid:
	// slice0 = the X=0 face (cellX=0), slice1 = the X=1 face (cellX=1); each holds a
	// [cellCountXZ+1][cellCountY+1] grid indexed [cz][cy].
	fill := func(slice [][]float64, cellX int) {
		blockX := (firstNoiseX + cellX) * cellWidth
		for cz := 0; cz <= cellCountXZ; cz++ {
			blockZ := (firstNoiseZ + cz) * cellWidth
			col := slice[cz]
			for cy := 0; cy <= cellCountY; cy++ {
				blockY := cy * cellHeight
				col[cy] = field.Compute(density.Context{X: blockX, Y: blockY, Z: blockZ})
			}
		}
	}
	fill(ip.slice0, 0) // X=0 face of the cell (slice0, from initializeForFirstCellX)
	fill(ip.slice1, 1) // X=1 face of the cell (slice1, from advanceCellX)

	ip.selectCellYZ(0, 0)
	for iy := 0; iy < cellHeight; iy++ {
		ip.updateForY(float64(iy) / cellHeight)
		for ix := 0; ix < cellWidth; ix++ {
			ip.updateForX(float64(ix) / cellWidth)
			for iz := 0; iz < cellWidth; iz++ {
				ip.updateForZ(float64(iz) / cellWidth)
				got := ip.value()
				wx := firstNoiseX*cellWidth + ix
				wy := iy
				wz := firstNoiseZ*cellWidth + iz
				want := field.Compute(density.Context{X: wx, Y: wy, Z: wz})
				if math.Abs(got-want) > 1e-9 {
					t.Fatalf("cell-fill order wrong at (ix=%d iy=%d iz=%d): got %v want %v (linear field must reproduce exactly)",
						ix, iy, iz, got, want)
				}
			}
		}
	}
}
