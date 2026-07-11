package block

import "testing"

// support_test.go spot-checks the SUB-FACESTURDY table (support.go) against known vanilla 26.2
// behavior. The values are the precomputed BlockBehaviour$BlockStateBase block-support cache
// (IsFaceSturdy/IsSolid/IsCollisionShapeFullBlock/IsSuffocating), extracted no-context with
// EmptyBlockGetter — see gen_block_support.go / GenBlockSupport.java for the javap citations.
//
// Each case is a vanilla ground-truth fact (verified against the jar this session):
//   - a full block (stone) is sturdy on every face for every SupportType, solid, full-cube,
//     and suffocating;
//   - air supports nothing;
//   - a bottom slab is sturdy on its DOWN face (FULL) but NOT its UP face, and is not a full
//     cube / not suffocating; a top slab is the mirror;
//   - a fence post is NOT FULL-sturdy on top but IS CENTER-sturdy (so a torch can sit on it);
//   - glass and leaves are full collision cubes yet override isSuffocating to false.

func TestIsFaceSturdy_FullBlock(t *testing.T) {
	s := ToStateID[Stone{}]
	dirs := []Direction{Down, Up, North, South, West, East}
	types := []SupportType{SupportFull, SupportCenter, SupportRigid}
	for _, d := range dirs {
		for _, st := range types {
			if !IsFaceSturdy(s, d, st) {
				t.Fatalf("IsFaceSturdy(stone, %v, %v) = false, want true (full block is sturdy everywhere)", d, st)
			}
		}
	}
	if !IsSolid(s) {
		t.Fatalf("IsSolid(stone) = false, want true")
	}
	if !IsCollisionShapeFullBlock(s) {
		t.Fatalf("IsCollisionShapeFullBlock(stone) = false, want true")
	}
	if !IsSuffocating(s) {
		t.Fatalf("IsSuffocating(stone) = false, want true")
	}
}

func TestIsFaceSturdy_Air(t *testing.T) {
	s := ToStateID[Air{}]
	dirs := []Direction{Down, Up, North, South, West, East}
	types := []SupportType{SupportFull, SupportCenter, SupportRigid}
	for _, d := range dirs {
		for _, st := range types {
			if IsFaceSturdy(s, d, st) {
				t.Fatalf("IsFaceSturdy(air, %v, %v) = true, want false (air supports nothing)", d, st)
			}
		}
	}
	if IsSolid(s) || IsCollisionShapeFullBlock(s) || IsSuffocating(s) {
		t.Fatalf("air: IsSolid/IsCollisionShapeFullBlock/IsSuffocating should all be false")
	}
}

func TestIsFaceSturdy_Slab(t *testing.T) {
	// Bottom slab: occupies the lower half — its DOWN face is a full sturdy square (FULL),
	// its UP face is NOT sturdy. Side faces are half squares, so not FULL-sturdy. Not a full
	// collision cube; not suffocating.
	bottom := ToStateID[OakSlab{Type: SlabTypeBottom, Waterlogged: false}]
	if !IsFaceSturdy(bottom, Down, SupportFull) {
		t.Fatalf("IsFaceSturdy(oak_slab[bottom], Down, FULL) = false, want true")
	}
	if IsFaceSturdy(bottom, Up, SupportFull) {
		t.Fatalf("IsFaceSturdy(oak_slab[bottom], Up, FULL) = true, want false (top of a bottom slab is not sturdy)")
	}
	if IsFaceSturdy(bottom, North, SupportFull) {
		t.Fatalf("IsFaceSturdy(oak_slab[bottom], North, FULL) = true, want false (slab side is a half face)")
	}
	if IsCollisionShapeFullBlock(bottom) {
		t.Fatalf("IsCollisionShapeFullBlock(oak_slab[bottom]) = true, want false")
	}
	if IsSuffocating(bottom) {
		t.Fatalf("IsSuffocating(oak_slab[bottom]) = true, want false")
	}

	// Top slab: the mirror — UP face sturdy (FULL), DOWN face not.
	top := ToStateID[OakSlab{Type: SlabTypeTop, Waterlogged: false}]
	if !IsFaceSturdy(top, Up, SupportFull) {
		t.Fatalf("IsFaceSturdy(oak_slab[top], Up, FULL) = false, want true")
	}
	if IsFaceSturdy(top, Down, SupportFull) {
		t.Fatalf("IsFaceSturdy(oak_slab[top], Down, FULL) = true, want false")
	}

	// Double slab behaves like a full block.
	double := ToStateID[OakSlab{Type: SlabTypeDouble, Waterlogged: false}]
	if !IsFaceSturdy(double, Up, SupportFull) || !IsFaceSturdy(double, Down, SupportFull) {
		t.Fatalf("IsFaceSturdy(oak_slab[double], Up/Down, FULL) = false, want true (double slab is a full block)")
	}
	if !IsCollisionShapeFullBlock(double) {
		t.Fatalf("IsCollisionShapeFullBlock(oak_slab[double]) = false, want true")
	}
}

func TestIsFaceSturdy_FencePostCenter(t *testing.T) {
	// An unconnected fence post is NOT FULL-sturdy on its top face (it is not a full square)
	// but IS CENTER-sturdy: the central post supports a centered attachment (e.g. a torch).
	s := ToStateID[OakFence{North: false, South: false, East: false, West: false, Waterlogged: false}]
	if IsFaceSturdy(s, Up, SupportFull) {
		t.Fatalf("IsFaceSturdy(oak_fence, Up, FULL) = true, want false (fence post is not a full top face)")
	}
	if !IsFaceSturdy(s, Up, SupportCenter) {
		t.Fatalf("IsFaceSturdy(oak_fence, Up, CENTER) = false, want true (fence post supports a centered attachment)")
	}
}

func TestIsSuffocating_Overrides(t *testing.T) {
	// Glass and leaves are FULL collision cubes, yet vanilla OVERRIDES isSuffocating to false.
	// This is exactly the case the pre-table non-air/non-fluid proxy got wrong.
	glass := ToStateID[Glass{}]
	if !IsCollisionShapeFullBlock(glass) {
		t.Fatalf("IsCollisionShapeFullBlock(glass) = false, want true (glass is a full collision cube)")
	}
	if IsSuffocating(glass) {
		t.Fatalf("IsSuffocating(glass) = true, want false (glass overrides isSuffocating)")
	}

	leaves := ToStateID[OakLeaves{Persistent: false, Distance: 7, Waterlogged: false}]
	if IsSuffocating(leaves) {
		t.Fatalf("IsSuffocating(oak_leaves) = true, want false (leaves override isSuffocating)")
	}

	// A plain solid full cube DOES suffocate.
	if !IsSuffocating(ToStateID[Dirt{}]) {
		t.Fatalf("IsSuffocating(dirt) = false, want true")
	}
}

func TestSupport_OutOfRange(t *testing.T) {
	// Negative / past-end ids and bad dir/type return false (no panic).
	if IsFaceSturdy(-1, Up, SupportFull) {
		t.Fatalf("IsFaceSturdy(-1) = true, want false")
	}
	if IsFaceSturdy(StateID(len(blockSupport)), Up, SupportFull) {
		t.Fatalf("IsFaceSturdy(past-end) = true, want false")
	}
	if IsFaceSturdy(ToStateID[Stone{}], Direction(99), SupportFull) {
		t.Fatalf("IsFaceSturdy(stone, bad-dir) = true, want false")
	}
	if IsSolid(-1) || IsCollisionShapeFullBlock(-1) || IsSuffocating(-1) {
		t.Fatalf("out-of-range accessors should return false")
	}
}

// TestBlocksMotion checks the BlockStateBase.blocksMotion port: false for a non-colliding
// plant (short_grass) so the motion-blocking heightmaps skip it, false for the two vanilla
// exclusions (cobweb, bamboo_sapling) even though they have collision, and true for a full
// solid block (stone). CITE: BlockBehaviour$BlockStateBase.blocksMotion.
func TestBlocksMotion(t *testing.T) {
	if BlocksMotion(ToStateID[ShortGrass{}]) {
		t.Fatalf("BlocksMotion(short_grass) = true, want false (plant is non-colliding, must not raise the motion-blocking heightmap)")
	}
	if BlocksMotion(ToStateID[Cobweb{}]) {
		t.Fatalf("BlocksMotion(cobweb) = true, want false (vanilla excludes COBWEB)")
	}
	if BlocksMotion(ToStateID[BambooSapling{}]) {
		t.Fatalf("BlocksMotion(bamboo_sapling) = true, want false (vanilla excludes BAMBOO_SAPLING)")
	}
	if !BlocksMotion(ToStateID[Stone{}]) {
		t.Fatalf("BlocksMotion(stone) = false, want true (a full solid block blocks motion)")
	}
}

// TestHasFluidState checks the getFluidState().isEmpty()==false predicate: true for water
// and lava (fluid blocks), false for a plain solid. CITE: BlockStateBase.getFluidState.
func TestHasFluidState(t *testing.T) {
	if !HasFluidState(ToStateID[Water{Level: 0}]) {
		t.Fatalf("HasFluidState(water) = false, want true")
	}
	if !HasFluidState(ToStateID[Lava{Level: 0}]) {
		t.Fatalf("HasFluidState(lava) = false, want true (lava fluid is non-empty)")
	}
	if HasFluidState(ToStateID[Stone{}]) {
		t.Fatalf("HasFluidState(stone) = true, want false")
	}
}

// TestIsLeavesBlockInstance checks the MOTION_BLOCKING_NO_LEAVES exclusion predicate:
// true for leaves, false for a non-leaf solid. CITE: Heightmap$Types.lambda$static$1
// (getBlock() instanceof LeavesBlock).
func TestIsLeavesBlockInstance(t *testing.T) {
	if !IsLeavesBlockInstance(ToStateID[OakLeaves{Distance: 1}]) {
		t.Fatalf("IsLeavesBlockInstance(oak_leaves) = false, want true")
	}
	if IsLeavesBlockInstance(ToStateID[Stone{}]) {
		t.Fatalf("IsLeavesBlockInstance(stone) = true, want false")
	}
}
