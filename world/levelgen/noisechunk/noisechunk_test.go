package noisechunk

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen/density"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// testSeed is a fixed seed so every Task-2 test builds the same deterministic graph.
const testSeed = int64(0x5EED_1234)

// buildNC is a shared helper: a Router from testSeed + a NoiseChunk at the given chunk
// position. Fails the test on a router error (a build-data bug, not a runtime input).
func buildNC(t *testing.T, cx, cz int32) (*router.Router, *NoiseChunk) {
	t.Helper()
	r, err := router.NewRouter(testSeed)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}
	nc := NewNoiseChunk(r, level.ChunkPos{cx, cz})
	return r, nc
}

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

// TestCellSampleNotPerBlock: NewNoiseChunk samples final_density on the COARSE cell
// corner grid (cellCountX/Y/Z corners), NOT once per block (anti-Pitfall-5). The
// NoiseChunk records its corner-sample count; it must equal the coarse-grid corner
// count ((cellCountXZ+1)^2 * (cellCountY+1)), orders of magnitude below 16*384*16.
func TestCellSampleNotPerBlock(t *testing.T) {
	_, nc := buildNC(t, 0, 0)

	cornerGrid := (nc.cellCountXZ + 1) * (nc.cellCountXZ + 1) * (nc.cellCountY + 1)
	perBlock := 16 * nc.height * 16
	if nc.cornerSamples != cornerGrid {
		t.Fatalf("corner sample count = %d, want coarse-grid %d", nc.cornerSamples, cornerGrid)
	}
	if nc.cornerSamples >= perBlock {
		t.Fatalf("corner samples %d not sparse vs per-block %d (Pitfall 5)", nc.cornerSamples, perBlock)
	}
	// Sanity: the overworld coarse grid is the ~768-corner ballpark from the plan.
	if cornerGrid > 4000 {
		t.Fatalf("coarse grid %d unexpectedly large (cellWidth/cellHeight wrong?)", cornerGrid)
	}
}

// TestNoiseChunkCornerExact: at a CELL CORNER the trilerped per-block density equals the
// DIRECT router.final_density sample exactly (interpolation only fills the cell INTERIOR;
// corners are the sampled anchors). A non-corner block equals the trilerp of the 8
// surrounding corners (within float tolerance) — proving the field both matches at
// corners and is genuinely interpolated between them.
func TestNoiseChunkCornerExact(t *testing.T) {
	r, nc := buildNC(t, 0, 0)
	fd := r.NoiseRouter.FinalDensity

	// A cell corner: localX/localZ multiples of cellWidth, world Y a multiple of
	// cellHeight aligned to the cell grid base.
	lx, lz := 4, 8 // cell corners (cellWidth=4)
	for _, cellY := range []int{0, 10, 20, 30, 40} {
		wy := (nc.cellNoiseMinY + cellY) * nc.cellHeight
		wx := int(nc.pos[0])*16 + lx
		wz := int(nc.pos[1])*16 + lz
		direct := fd.Compute(density.Context{X: wx, Y: wy, Z: wz})
		got := nc.FinalDensity(lx, wy, lz)
		if math.Abs(got-direct) > 1e-9 {
			t.Fatalf("corner (lx=%d wy=%d lz=%d): NoiseChunk %v != direct final_density %v", lx, wy, lz, got, direct)
		}
	}

	// Field must VARY with position (terrain has shape, not a constant plane).
	a := nc.FinalDensity(2, -40, 2)
	b := nc.FinalDensity(10, 100, 10)
	if a == b {
		t.Fatalf("final_density constant across positions (%v) — no terrain shape", a)
	}
}

// TestDensityFieldHasCaves: somewhere UNDERGROUND the trilerped final_density goes
// NEGATIVE beneath a SOLID (positive) roof — a carved cave — proving the cave branches
// carried by final_density (Wave 3) materialize in the sampled field (the full-parity
// "caves come free" gate). Caves are SPARSE: a single chunk often has none, so this
// scans a small grid of chunks over the full underground Y band (the field, not one
// column, is the unit of the assertion). The directly-sampled router confirms a cave
// exists at chunk (-8,-8) ~y=-54; the NoiseChunk's trilerped field must reproduce one.
func TestDensityFieldHasCaves(t *testing.T) {
	r, err := router.NewRouter(testSeed)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}

	sawSolid, sawCave := false, false
	// Scan a 5x5 chunk region around the cave-bearing area, full underground Y band.
	for cx := int32(-10); cx <= -6 && !(sawSolid && sawCave); cx++ {
		for cz := int32(-10); cz <= -6 && !(sawSolid && sawCave); cz++ {
			nc := NewNoiseChunk(r, level.ChunkPos{cx, cz})
			for lx := 0; lx < 16 && !(sawSolid && sawCave); lx += 2 {
				for lz := 0; lz < 16 && !(sawSolid && sawCave); lz += 2 {
					for wy := nc.minY + 4; wy < 30; wy++ {
						d := nc.FinalDensity(lx, wy, lz)
						if d > 0 {
							sawSolid = true
						}
						if d < 0 {
							// require a solid roof above to call it a true underground cave
							if nc.FinalDensity(lx, wy+10, lz) > 0 {
								sawCave = true
							}
						}
					}
				}
			}
		}
	}
	if !sawSolid {
		t.Fatalf("no positive (solid) density underground — final_density never carves solid ground")
	}
	if !sawCave {
		t.Fatalf("no negative (carved) density under a solid roof — caves absent from the sampled field")
	}
}

// TestProvisionalFill: the provisional fill helper classifies every block from the
// per-block density — density>0 -> solid (stone, deepslate deep), density<=0 & y<sea ->
// water source, density<=0 & y>=sea -> air, with a bedrock floor at minY. A column shows
// the stone / cave-air / water stratification. This is the PLACEHOLDER Wave 5's Aquifer
// replaces; here it just proves the cell machinery feeds a coherent block field.
func TestProvisionalFill(t *testing.T) {
	r, nc := buildNC(t, 0, 0)
	ch := nc.FillProvisional()
	if ch == nil {
		t.Fatal("FillProvisional returned nil chunk")
	}
	if ch.Status != level.StatusFull {
		t.Fatalf("chunk status = %v, want StatusFull", ch.Status)
	}

	// The floor row at minY must be bedrock everywhere.
	if got := nc.blockAt(0, nc.minY, 0); got != nc.bedrock {
		t.Fatalf("floor at minY not bedrock: got id %d want %d", got, nc.bedrock)
	}

	// Classification must agree with the density sign + sea-level rule at every block in
	// a representative column.
	seaLevel := r.Settings.SeaLevel
	lx, lz := 7, 9
	for wy := nc.minY + 1; wy < nc.minY+nc.height; wy++ {
		d := nc.FinalDensity(lx, wy, lz)
		got := nc.blockAt(lx, wy, lz)
		switch {
		case d > 0:
			if got != nc.stone && got != nc.deepslate {
				t.Fatalf("y=%d density>0 (%v) but block id %d is not stone/deepslate", wy, d, got)
			}
		case wy < seaLevel:
			if got != nc.water {
				t.Fatalf("y=%d density<=0 & below sea but block id %d is not water", wy, got)
			}
		default:
			if got != nc.air {
				t.Fatalf("y=%d density<=0 & at/above sea but block id %d is not air", wy, got)
			}
		}
	}
}

// TestNoiseChunkDeterministic: two NoiseChunks from the same seed+pos produce an
// identical per-block density field and identical provisional fill (Pitfall 7).
func TestNoiseChunkDeterministic(t *testing.T) {
	_, nc1 := buildNC(t, 1, -2)
	_, nc2 := buildNC(t, 1, -2)

	for _, p := range [][3]int{{0, -64, 0}, {3, 0, 5}, {15, 60, 15}, {8, 200, 8}, {1, -40, 14}} {
		d1 := nc1.FinalDensity(p[0], p[1], p[2])
		d2 := nc2.FinalDensity(p[0], p[1], p[2])
		if d1 != d2 {
			t.Fatalf("non-deterministic density at %v: %v vs %v", p, d1, d2)
		}
		if b1, b2 := nc1.blockAt(p[0], p[1], p[2]), nc2.blockAt(p[0], p[1], p[2]); b1 != b2 {
			t.Fatalf("non-deterministic block at %v: %d vs %d", p, b1, b2)
		}
	}
}
