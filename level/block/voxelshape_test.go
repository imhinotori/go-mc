package block

// voxelshape_test.go — behavior tests for the VoxelShape.collideX port (voxelshape.go) over
// the extracted per-state grids (collision_shapes.go). Expected values are the vanilla
// shapes verified from the 26.2 jar this session:
//   - stone: Shapes.block() (identity), the 1×1×1 cube;
//   - stone_slab type=bottom: Block.box(0,0,0,16,8,16) → y ∈ [0, 0.5];
//   - oak_fence unconnected: post 6..10 px, 1.5 blocks tall (y ∈ [0, 1.5], x/z ∈ [0.375, 0.625]);
//   - oak_stairs bottom/north/straight: base slab y ∈ [0, 0.5] + riser y ∈ [0.5, 1] on the
//     north half (z ∈ [0, 0.5]);
//   - air / short_grass: Shapes.empty() (no collision).

import (
	"math"
	"testing"
)

// stateOf resolves a Block struct literal to its StateID, failing the test if unknown.
func stateOf(t *testing.T, b Block) StateID {
	t.Helper()
	sid, ok := ToStateID[b]
	if !ok {
		t.Fatalf("no StateID for %#v", b)
	}
	return sid
}

func TestCollisionShapeFullCube(t *testing.T) {
	s := CollisionShape(stateOf(t, Stone{}))
	if s.IsEmpty() {
		t.Fatal("stone: shape empty, want full cube")
	}
	if !s.Block {
		t.Fatal("stone: not identity-equal to Shapes.block()")
	}
	// Entity box hovering above the cube at block (0,0,0), falling 1.0: clamps flush onto
	// the top face y=1 (delta -0.5 exactly).
	box := Box{MinX: 0.2, MinY: 1.5, MinZ: 0.2, MaxX: 0.8, MaxY: 3.3, MaxZ: 0.8}
	if got := s.Collide(AxisY, 0, 0, 0, box, -1.0); got != -0.5 {
		t.Fatalf("fall onto cube: clamped %v, want -0.5", got)
	}
	// Resting exactly on the face: moving down is fully blocked (0).
	rest := Box{MinX: 0.2, MinY: 1.0, MinZ: 0.2, MaxX: 0.8, MaxY: 2.8, MaxZ: 0.8}
	if got := s.Collide(AxisY, 0, 0, 0, rest, -0.078); got != 0 {
		t.Fatalf("rest on cube: clamped %v, want 0", got)
	}
	// Walking +X into the cube's west face x=0 from maxX=-0.3: clamps to 0.3.
	side := Box{MinX: -0.9, MinY: 0.2, MinZ: 0.2, MaxX: -0.3, MaxY: 0.8, MaxZ: 0.8}
	if got := s.Collide(AxisX, 0, 0, 0, side, 1.0); got != 0.3 {
		t.Fatalf("walk into cube: clamped %v, want 0.3", got)
	}
	// Same walk but the box is entirely above the cube (no Y overlap): unobstructed.
	above := Box{MinX: -0.9, MinY: 1.2, MinZ: 0.2, MaxX: -0.3, MaxY: 1.8, MaxZ: 0.8}
	if got := s.Collide(AxisX, 0, 0, 0, above, 1.0); got != 1.0 {
		t.Fatalf("walk above cube: clamped %v, want 1.0", got)
	}
	// World offset: the same cube placed at (5, 10, -3).
	offBox := Box{MinX: 5.2, MinY: 11.5, MinZ: -2.8, MaxX: 5.8, MaxY: 13.3, MaxZ: -2.2}
	if got := s.Collide(AxisY, 5, 10, -3, offBox, -1.0); got != -0.5 {
		t.Fatalf("fall onto offset cube: clamped %v, want -0.5", got)
	}
}

func TestCollisionShapeEmpty(t *testing.T) {
	for _, b := range []Block{Air{}, ShortGrass{}, Dandelion{}} {
		s := CollisionShape(stateOf(t, b))
		if !s.IsEmpty() {
			t.Fatalf("%#v: shape not empty", b)
		}
		box := Box{MinX: 0.2, MinY: 0.0, MinZ: 0.2, MaxX: 0.8, MaxY: 1.8, MaxZ: 0.8}
		if got := s.Collide(AxisY, 0, 0, 0, box, -1.0); got != -1.0 {
			t.Fatalf("%#v: empty shape clamped %v, want -1.0 (no collision)", b, got)
		}
	}
	// Water: LiquidBlock.getCollisionShape is Shapes.empty() for the default context.
	if s := CollisionShape(stateOf(t, Water{Level: 0})); !s.IsEmpty() {
		t.Fatal("water: collision shape not empty")
	}
}

func TestCollisionShapeSlabBottom(t *testing.T) {
	s := CollisionShape(stateOf(t, StoneSlab{Type: SlabTypeBottom, Waterlogged: false}))
	if s.IsEmpty() || s.Block {
		t.Fatalf("bottom slab: empty=%v block=%v, want non-empty non-block", s.IsEmpty(), s.Block)
	}
	// Fall onto the slab: clamps at its half-height top face y=0.5.
	box := Box{MinX: 0.2, MinY: 1.5, MinZ: 0.2, MaxX: 0.8, MaxY: 3.3, MaxZ: 0.8}
	if got := s.Collide(AxisY, 0, 0, 0, box, -2.0); got != -1.0 {
		t.Fatalf("fall onto bottom slab: clamped %v, want -1.0 (land at y=0.5)", got)
	}
	// Walking sideways with feet ABOVE 0.5 passes over the slab.
	over := Box{MinX: -0.9, MinY: 0.5, MinZ: 0.2, MaxX: -0.3, MaxY: 2.3, MaxZ: 0.8}
	if got := s.Collide(AxisX, 0, 0, 0, over, 1.0); got != 1.0 {
		t.Fatalf("walk over bottom slab: clamped %v, want 1.0", got)
	}
	// Walking sideways with feet below 0.5 hits the slab side.
	into := Box{MinX: -0.9, MinY: 0.0, MinZ: 0.2, MaxX: -0.3, MaxY: 1.8, MaxZ: 0.8}
	if got := s.Collide(AxisX, 0, 0, 0, into, 1.0); got != 0.3 {
		t.Fatalf("walk into bottom slab: clamped %v, want 0.3", got)
	}
	// Double slab is the full cube geometry.
	d := CollisionShape(stateOf(t, StoneSlab{Type: SlabTypeDouble, Waterlogged: false}))
	if got := d.Collide(AxisY, 0, 0, 0, box, -2.0); got != -0.5 {
		t.Fatalf("fall onto double slab: clamped %v, want -0.5 (land at y=1)", got)
	}
}

func TestCollisionShapeFence(t *testing.T) {
	s := CollisionShape(stateOf(t, OakFence{})) // unconnected post
	if s.IsEmpty() {
		t.Fatal("fence: shape empty")
	}
	if HasLargeCollisionShape(stateOf(t, OakFence{})) != true {
		t.Fatal("fence: hasLargeCollisionShape false, want true (1.5-block-tall shape)")
	}
	// Fall onto the post: clamps at y=1.5 — the fence's defining above-the-cube height.
	box := Box{MinX: 0.4, MinY: 2.0, MinZ: 0.4, MaxX: 0.6, MaxY: 3.8, MaxZ: 0.6}
	if got := s.Collide(AxisY, 0, 0, 0, box, -2.0); got != -0.5 {
		t.Fatalf("fall onto fence: clamped %v, want -0.5 (land at y=1.5)", got)
	}
	// Walk into the post side: post x ∈ [0.375, 0.625].
	side := Box{MinX: -0.6, MinY: 0.2, MinZ: 0.4, MaxX: 0.0, MaxY: 1.0, MaxZ: 0.6}
	if got := s.Collide(AxisX, 0, 0, 0, side, 1.0); got != 0.375 {
		t.Fatalf("walk into fence post: clamped %v, want 0.375", got)
	}
	// Walk PAST the post: a box offset in Z misses the 0.375..0.625 post entirely.
	miss := Box{MinX: -0.6, MinY: 0.2, MinZ: 0.7, MaxX: 0.0, MaxY: 1.0, MaxZ: 0.9}
	if got := s.Collide(AxisX, 0, 0, 0, miss, 1.0); got != 1.0 {
		t.Fatalf("walk past fence post: clamped %v, want 1.0", got)
	}
	// Stone (full cube) must NOT be flagged large.
	if HasLargeCollisionShape(stateOf(t, Stone{})) {
		t.Fatal("stone: hasLargeCollisionShape true, want false")
	}
}

func TestCollisionShapeStairs(t *testing.T) {
	// facing=north, half=bottom, shape=straight: base slab y ∈ [0, 0.5] everywhere, riser
	// y ∈ [0.5, 1] over the north half z ∈ [0, 0.5].
	s := CollisionShape(stateOf(t, OakStairs{Facing: North, Half: Bottom, Shape: StairsShapeStraight, Waterlogged: false}))
	if s.IsEmpty() || s.Block {
		t.Fatalf("stairs: empty=%v block=%v, want non-empty non-block", s.IsEmpty(), s.Block)
	}
	// Falling over the south half (z 0.6..0.9): lands on the base slab at y=0.5.
	south := Box{MinX: 0.2, MinY: 1.5, MinZ: 0.6, MaxX: 0.8, MaxY: 3.3, MaxZ: 0.9}
	if got := s.Collide(AxisY, 0, 0, 0, south, -2.0); got != -1.0 {
		t.Fatalf("fall onto stair low half: clamped %v, want -1.0 (land at y=0.5)", got)
	}
	// Falling over the north half (z 0.1..0.4): lands on the riser at y=1.
	north := Box{MinX: 0.2, MinY: 1.5, MinZ: 0.1, MaxX: 0.8, MaxY: 3.3, MaxZ: 0.4}
	if got := s.Collide(AxisY, 0, 0, 0, north, -2.0); got != -0.5 {
		t.Fatalf("fall onto stair high half: clamped %v, want -0.5 (land at y=1)", got)
	}
}

func TestCollideAlreadyOverlappingDoesNotClip(t *testing.T) {
	// A box already intersecting a cube is NOT clipped by it while moving out — vanilla's
	// "unstick" behavior: collideX scans only slices strictly beyond the leading edge, and
	// a face behind it by more than EPSILON returns desired unchanged.
	s := CollisionShape(stateOf(t, Stone{}))
	inside := Box{MinX: 0.2, MinY: 0.5, MinZ: 0.2, MaxX: 0.8, MaxY: 2.3, MaxZ: 0.8}
	if got := s.Collide(AxisY, 0, 0, 0, inside, 0.4); got != 0.4 {
		t.Fatalf("moving up out of overlapped cube: clamped %v, want 0.4 (no clip)", got)
	}
}

func TestCollideEpsilonDeadband(t *testing.T) {
	// |desired| < 1e-7 returns exactly 0 whenever the shape is non-empty (Shapes.collide /
	// collideX's first guard).
	s := CollisionShape(stateOf(t, Stone{}))
	box := Box{MinX: 0.2, MinY: 1.5, MinZ: 0.2, MaxX: 0.8, MaxY: 3.3, MaxZ: 0.8}
	if got := s.Collide(AxisY, 0, 0, 0, box, 5e-8); got != 0 {
		t.Fatalf("sub-epsilon desired: %v, want 0", got)
	}
	if got := s.Collide(AxisY, 0, 0, 0, box, -5e-8); got != 0 {
		t.Fatalf("sub-epsilon desired: %v, want 0", got)
	}
}

func TestIntersectsBox(t *testing.T) {
	s := CollisionShape(stateOf(t, OakFence{}))
	// Box overlapping the post volume.
	if !s.IntersectsBox(0, 0, 0, Box{MinX: 0.5, MinY: 0.5, MinZ: 0.5, MaxX: 1.5, MaxY: 2.5, MaxZ: 1.5}) {
		t.Fatal("box through fence post: no intersection reported")
	}
	// Box in the same block cell but clear of the post (corner region).
	if s.IntersectsBox(0, 0, 0, Box{MinX: 0.7, MinY: 0.0, MinZ: 0.7, MaxX: 0.95, MaxY: 1.0, MaxZ: 0.95}) {
		t.Fatal("box beside fence post: spurious intersection")
	}
	// Face-sharing (graze < EPSILON) does not intersect.
	if s.IntersectsBox(0, 0, 0, Box{MinX: 0.625, MinY: 0.0, MinZ: 0.4, MaxX: 1.0, MaxY: 1.0, MaxZ: 0.6}) {
		t.Fatal("face-sharing box: spurious intersection")
	}
}

func TestFindIndexSemantics(t *testing.T) {
	s := CollisionShape(stateOf(t, Stone{})) // coords [0, 1] each axis
	cases := []struct {
		pos  float64
		want int
	}{
		{-0.5, -1}, // before the first boundary
		{0.0, 0},   // exactly the first boundary → cell 0
		{0.5, 0},   // inside cell 0
		{1.0, 1},   // exactly the last boundary → index 1 (== size)
		{1.5, 1},   // past the end
	}
	for _, c := range cases {
		if got := s.findIndex(AxisY, 0, c.pos); got != c.want {
			t.Fatalf("findIndex(%v) = %d, want %d", c.pos, got, c.want)
		}
	}
	// NaN desired propagates through math.Min like Java Math.min — just assert Collide
	// doesn't panic on odd inputs.
	_ = s.Collide(AxisY, 0, 0, 0, Box{MinX: 0.2, MinY: 1.5, MinZ: 0.2, MaxX: 0.8, MaxY: 3.3, MaxZ: 0.8}, math.Inf(-1))
}
