package structure

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// beardTestPiece is a minimal Piece for beard tests: just a bbox (no PostProcess writes).
type beardTestPiece struct{ bb BoundingBox }

func (p beardTestPiece) BoundingBox() BoundingBox { return p.bb }
func (p beardTestPiece) PostProcess(view WorldGenView, box BoundingBox, chunkPos level.ChunkPos, rng levelgen.RandomSource) {
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestBeardContribution asserts getBuryContribution + getBeardContribution match the jar at
// sample offsets (the exact math: clampedMap for bury, the kernel falloff for beard).
func TestBeardContribution(t *testing.T) {
	// getBuryContribution = Mth.clampedMap(Mth.length(dx,dy,dz), 0, 6, 1, 0).
	// At the center (0,0,0): length 0 -> inverseLerp(0,0,6)=0 -> clampedLerp(0,1,0)=1.
	if got := getBuryContribution(0, 0, 0); !approx(got, 1.0) {
		t.Fatalf("getBuryContribution(0,0,0) = %v, want 1.0", got)
	}
	// At distance >= 6: length 6 -> inverseLerp(6,0,6)=1 -> clampedLerp(1,1,0)=0.
	if got := getBuryContribution(6, 0, 0); !approx(got, 0.0) {
		t.Fatalf("getBuryContribution(6,0,0) = %v, want 0.0", got)
	}
	if got := getBuryContribution(10, 0, 0); !approx(got, 0.0) {
		t.Fatalf("getBuryContribution(10,0,0) = %v, want 0.0 (clamped)", got)
	}
	// At distance 3 (half): length 3 -> inverseLerp(3,0,6)=0.5 -> lerp(0.5,1,0)=0.5.
	if got := getBuryContribution(3, 0, 0); !approx(got, 0.5) {
		t.Fatalf("getBuryContribution(3,0,0) = %v, want 0.5", got)
	}
	// length(0,4,3) = 5 -> inverseLerp(5,0,6)=5/6 -> lerp(5/6,1,0)=1-5/6=1/6.
	if got := getBuryContribution(0, 4, 3); !approx(got, 1.0/6.0) {
		t.Fatalf("getBuryContribution(0,4,3) = %v, want %v", got, 1.0/6.0)
	}

	// getBeardContribution: out of kernel range (after +12) -> 0.
	if got := getBeardContribution(12, 0, 0, 0); got != 0 {
		t.Fatalf("getBeardContribution(12,..) kernel-range gate = %v, want 0 (kx=24 out of range)", got)
	}
	if got := getBeardContribution(-13, 0, 0, 0); got != 0 {
		t.Fatalf("getBeardContribution(-13,..) kernel-range gate = %v, want 0 (kx=-1 out of range)", got)
	}

	// getBeardContribution in range matches the literal port at (dx=0,dy=0,dz=0,n=0):
	//   d = n+0.5 = 0.5
	//   e = lengthSquared(0, 0.5, 0) = 0.25
	//   magnitude = -0.5 * fastInvSqrt(0.25/2) / 2 = -0.5 * fastInvSqrt(0.125) / 2
	//   kernel index: kz*576 + kx*24 + ky = 12*576 + 12*24 + 12
	d := 0.5
	e := 0.25
	wantMag := -d * mthFastInvSqrt(e/2.0) / 2.0
	wantKernel := float64(beardKernel[12*576+12*24+12])
	want := wantMag * wantKernel
	if got := getBeardContribution(0, 0, 0, 0); !approx(got, want) {
		t.Fatalf("getBeardContribution(0,0,0,0) = %v, want %v", got, want)
	}

	// Non-trivial in-range sample (dx=1,dy=2,dz=-1,n=2):
	//   d = 2.5; e = lengthSquared(1, 2.5, -1) = 1 + 6.25 + 1 = 8.25
	//   magnitude = -2.5 * fastInvSqrt(8.25/2) / 2
	//   kernel index: (dz+12)*576 + (dx+12)*24 + (dy+12) = 11*576 + 13*24 + 14
	d2 := 2.5
	e2 := 1.0 + 6.25 + 1.0
	wantMag2 := -d2 * mthFastInvSqrt(e2/2.0) / 2.0
	wantKernel2 := float64(beardKernel[11*576+13*24+14])
	want2 := wantMag2 * wantKernel2
	if got := getBeardContribution(1, 2, -1, 2); !approx(got, want2) {
		t.Fatalf("getBeardContribution(1,2,-1,2) = %v, want %v", got, want2)
	}
}

// TestBeardKernelDeterministic asserts the kernel center value matches the gaussian formula.
func TestBeardKernelDeterministic(t *testing.T) {
	// Kernel at the exact center cell stores computeBeardContribution(0,0,0):
	//   = Math.pow(E, -lengthSquared(0, 0.5, 0)/16) = exp(-0.25/16).
	center := float64(beardKernel[12*576+12*24+12])
	want := math.Exp(-0.25 / 16.0)
	if math.Abs(center-want) > 1e-6 {
		t.Fatalf("beardKernel center = %v, want ~%v", center, want)
	}
	if len(beardKernel) != 24*24*24 {
		t.Fatalf("beardKernel len = %d, want %d", len(beardKernel), 24*24*24)
	}
}

// TestBeardScope asserts ForStructuresInChunk gathers ONLY adapting structures, and a chunk
// with only NONE-adaptation structures yields an EMPTY Beardifier (Compute -> 0 everywhere).
func TestBeardScope(t *testing.T) {
	pos := level.ChunkPos{0, 0}
	bb := BoundingBox{MinX: 0, MinY: 60, MinZ: 0, MaxX: 8, MaxY: 70, MaxZ: 8}

	// A NONE-adaptation structure (desert_pyramid) -> EMPTY -> Compute 0 everywhere.
	noneStart := &StructureStart{
		Structure: "minecraft:desert_pyramid",
		ChunkPos:  pos,
		Pieces:    []Piece{beardTestPiece{bb: bb}},
	}
	noneStart.RecomputeBBox()
	b := ForStructuresInChunk([]*StructureStart{noneStart}, pos)
	if got := b.Compute(4, 65, 4); got != 0 {
		t.Fatalf("NONE structure beard at center = %v, want 0 (not gathered)", got)
	}
	if got := b.Compute(4, 100, 4); got != 0 {
		t.Fatalf("NONE structure beard above = %v, want 0", got)
	}

	// A village (beard_thin) start IS gathered -> a non-zero contribution somewhere in box.
	villageStart := &StructureStart{
		Structure: "minecraft:village_plains",
		ChunkPos:  pos,
		Pieces:    []Piece{beardTestPiece{bb: bb}},
	}
	villageStart.RecomputeBBox()
	vb := ForStructuresInChunk([]*StructureStart{villageStart}, pos)
	if vb.empty {
		t.Fatal("village (beard_thin) ForStructuresInChunk returned EMPTY, want gathered")
	}
	// A point just below the box bottom (n<0) should give a non-zero raise.
	var any bool
	for y := 55; y <= 70; y++ {
		if vb.Compute(4, y, 4) != 0 {
			any = true
			break
		}
	}
	if !any {
		t.Fatal("village beard contributed 0 at every sampled Y inside the box, want non-zero raise")
	}
}

// TestBeardOutsideAffectedBox asserts Compute returns 0 outside the inflated affectedBox
// (the no-double-apply gate) for an adapting structure.
func TestBeardOutsideAffectedBox(t *testing.T) {
	pos := level.ChunkPos{0, 0}
	bb := BoundingBox{MinX: 0, MinY: 60, MinZ: 0, MaxX: 8, MaxY: 70, MaxZ: 8}
	start := &StructureStart{
		Structure: "minecraft:stronghold",
		ChunkPos:  pos,
		Pieces:    []Piece{beardTestPiece{bb: bb}},
	}
	start.RecomputeBBox()
	b := ForStructuresInChunk([]*StructureStart{start}, pos)

	// affectedBox = union inflated by 24: X/Y/Z in [-24, 32]/[36,94]/[-24,32].
	// A point far outside (X=1000) must be 0.
	if got := b.Compute(1000, 65, 4); got != 0 {
		t.Fatalf("beard far outside affectedBox = %v, want 0", got)
	}
	// Y far above the inflated box (>94) -> 0.
	if got := b.Compute(4, 200, 4); got != 0 {
		t.Fatalf("beard far above affectedBox = %v, want 0", got)
	}
	// Inside the bury box center: should be non-zero (bury digs).
	if got := b.Compute(4, 65, 4); got == 0 {
		t.Fatalf("stronghold (bury) beard at box center = 0, want non-zero")
	}
}

// TestBeardThinRaisesBelow asserts the village BEARD_THIN contribution is POSITIVE for cells
// below the structure floor (raising terrain to meet a floating structure: n<0 -> larger d).
func TestBeardThinRaisesBelow(t *testing.T) {
	pos := level.ChunkPos{0, 0}
	bb := BoundingBox{MinX: 0, MinY: 64, MinZ: 0, MaxX: 8, MaxY: 74, MaxZ: 8}
	start := &StructureStart{
		Structure: "minecraft:village_plains",
		ChunkPos:  pos,
		Pieces:    []Piece{beardTestPiece{bb: bb}},
	}
	start.RecomputeBBox()
	b := ForStructuresInChunk([]*StructureStart{start}, pos)

	// At a cell well below the floor (y=62, n = 62 - 64 = -2): d = n+0.5 = -1.5 < 0,
	// magnitude = -d * invSqrt(...) / 2 > 0 -> positive contribution (raises terrain).
	got := b.Compute(4, 62, 4)
	if got <= 0 {
		t.Fatalf("beard_thin contribution below floor = %v, want > 0 (raises terrain)", got)
	}
}
