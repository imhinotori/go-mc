package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// beehive_test.go -- validation gates for BeehiveBlockEntity (BEEHIVE-01), ported 1:1 from the 26.2 jar.
// Uses the newBELoop harness (all-air chunks + block-tick container) and seeds a deterministic levelRandom
// so the honey-bump nextInt(100) roll is reproducible.

// beehiveTestState returns the default beehive state id, facing SOUTH by default (the release front is the
// SOUTH neighbour, which is air in the all-air test world so a bee can exit).
func beehiveTestState() block.StateID {
	return block.DefaultStateID["minecraft:beehive"]
}

// TestBeehiveMaxThreeOccupants asserts addOccupant beyond MAX_OCCUPANTS (3) is a no-op (isFull true at 3).
func TestBeehiveMaxThreeOccupants(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 2, Y: 5, Z: 2}
	mgr.SetBlock(pos, beehiveTestState(), dimMinY)
	b := loop.resolveBeehive(pos)
	if b == nil {
		t.Fatal("resolveBeehive returned nil for a placed beehive")
	}
	for i := 0; i < 3; i++ {
		bee := loop.spawnHiveBee(pos, beehiveOccupant{})
		loop.beehiveAddOccupant(pos, b, bee)
	}
	if b.occupantCount() != 3 || !b.isFull() {
		t.Fatalf("occupantCount=%d isFull=%v after 3 adds; want 3/true", b.occupantCount(), b.isFull())
	}
	// A 4th add must be a NO-OP (still 3).
	bee4 := loop.spawnHiveBee(pos, beehiveOccupant{})
	loop.beehiveAddOccupant(pos, b, bee4)
	if b.occupantCount() != 3 {
		t.Fatalf("occupantCount=%d after a 4th add; want 3 (no-op)", b.occupantCount())
	}
}

// TestBeehiveReleaseAfterMinTicksNectarless asserts a nectarless bee is released strictly AFTER 600 ticks
// (ticksInHive > 600), never at or before.
func TestBeehiveReleaseAfterMinTicksNectarless(t *testing.T) {
	loop, mgr := newBELoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(7)
	pos := pk.Position{X: 2, Y: 5, Z: 2}
	mgr.SetBlock(pos, beehiveTestState(), dimMinY)
	b := loop.resolveBeehive(pos)
	b.storeBee(beehiveOccupant{hasNectar: false, ticksInHive: 0, minTicksInHive: 600})

	before := loop.only().entities.len()
	// Tick 601 times: BeeData.tick returns true only when the PRE-increment ticksInHive > 600, i.e. the
	// 602nd tick (pre-value 601). After 601 ticks the pre-value reached 600, which is NOT > 600 -> still in.
	for i := 0; i < 601; i++ {
		loop.beehiveServerTick(pos, b)
	}
	if b.occupantCount() != 1 {
		t.Fatalf("bee released too early: occupantCount=%d after 601 ticks; want 1", b.occupantCount())
	}
	loop.beehiveServerTick(pos, b) // pre-value 601 > 600 -> release
	if b.occupantCount() != 0 {
		t.Fatalf("bee not released: occupantCount=%d after 602 ticks; want 0", b.occupantCount())
	}
	if loop.only().entities.len() != before+1 {
		t.Fatalf("released bee not added to the world: entities delta=%d, want 1", loop.only().entities.len()-before)
	}
}

// TestBeehiveReleaseAfterMinTicksNectar asserts a nectar bee is held for 2400 ticks (still in at 2400,
// released on the 2401st -- strictly greater), and that release bumps the honey level (HONEY_DELIVERED).
func TestBeehiveReleaseAfterMinTicksNectar(t *testing.T) {
	loop, mgr := newBELoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(7)
	pos := pk.Position{X: 2, Y: 5, Z: 2}
	mgr.SetBlock(pos, beehiveTestState(), dimMinY)
	b := loop.resolveBeehive(pos)
	b.storeBee(beehiveOccupant{hasNectar: true, ticksInHive: 0, minTicksInHive: 2400})

	for i := 0; i < 2401; i++ {
		loop.beehiveServerTick(pos, b)
	}
	if b.occupantCount() != 1 {
		t.Fatalf("nectar bee released too early: occupantCount=%d after 2401 ticks; want 1", b.occupantCount())
	}
	loop.beehiveServerTick(pos, b)
	if b.occupantCount() != 0 {
		t.Fatalf("nectar bee not released: occupantCount=%d after 2402 ticks; want 0", b.occupantCount())
	}
	// HONEY_DELIVERED bumped the block honey_level from 0 to >=1.
	ns, _ := mgr.GetBlock(pos, dimMinY)
	if hl := block.HoneyLevel(ns); hl < 1 {
		t.Fatalf("honey_level=%d after a nectar release; want >=1", hl)
	}
}

// TestBeehiveHoneyBumpCappedAtFive asserts a HONEY_DELIVERED release bumps honey_level by +1 (deterministic
// seed avoids the 1/100 +2 roll) and never exceeds 5 (a release at level 5 leaves it at 5).
func TestBeehiveHoneyBumpCappedAtFive(t *testing.T) {
	loop, mgr := newBELoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(1)
	pos := pk.Position{X: 2, Y: 5, Z: 2}
	// Start the hive already at honey_level 5.
	full, ok := block.WithHoneyLevel(beehiveTestState(), 5)
	if !ok {
		t.Fatal("WithHoneyLevel(5) failed")
	}
	mgr.SetBlock(pos, full, dimMinY)
	loop.resolveBeehive(pos)
	// A nectar bee released against a level-5 hive: dropOffNectar runs but hl<5 is false -> no bump.
	if !loop.beehiveReleaseOccupant(pos, beehiveOccupant{hasNectar: true, minTicksInHive: 2400}, beeHoneyDelivered, nil) {
		t.Fatal("releaseOccupant returned false against an open front")
	}
	ns, _ := mgr.GetBlock(pos, dimMinY)
	if hl := block.HoneyLevel(ns); hl != 5 {
		t.Fatalf("honey_level=%d after a release at 5; want 5 (capped)", hl)
	}

	// Now from level 0 the same delivery bumps to exactly +1 (seed 1: first nextFloat then nextInt(100)!=0).
	loop2, mgr2 := newBELoop()
	loop2.only().levelRandom = levelgen.NewLegacyRandomSource(1)
	mgr2.SetBlock(pos, beehiveTestState(), dimMinY)
	b2 := loop2.resolveBeehive(pos)
	_ = b2
	if !loop2.beehiveReleaseOccupant(pos, beehiveOccupant{hasNectar: true, minTicksInHive: 2400}, beeHoneyDelivered, nil) {
		t.Fatal("releaseOccupant(from 0) returned false")
	}
	ns2, _ := mgr2.GetBlock(pos, dimMinY)
	if hl := block.HoneyLevel(ns2); hl != 1 {
		t.Fatalf("honey_level=%d after a level-0 nectar release; want 1", hl)
	}
}

// TestBeehiveBlockedFrontHoldsBee asserts a release is BLOCKED when the FACING-front block has collision
// (non-EMERGENCY status): the bee stays in the hive.
func TestBeehiveBlockedFrontHoldsBee(t *testing.T) {
	loop, mgr := newBELoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(7)
	pos := pk.Position{X: 2, Y: 5, Z: 2}
	state := beehiveTestState()
	mgr.SetBlock(pos, state, dimMinY)
	// Fill the FACING-front cell with a full solid block (stone) so the release front is blocked.
	dir, _ := block.BeehiveFacing(state)
	front := relative(pos, dir)
	mgr.SetBlock(front, block.DefaultStateID["minecraft:stone"], dimMinY)
	if released := loop.beehiveReleaseOccupant(pos, beehiveOccupant{hasNectar: false, minTicksInHive: 600}, beeReleased, nil); released {
		t.Fatal("releaseOccupant returned true with a blocked front; want false (bee held)")
	}
}
