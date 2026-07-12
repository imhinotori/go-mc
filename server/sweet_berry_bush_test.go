package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// sweet_berry_bush_test.go covers the SweetBerryBushBlock.useWithoutItem 1:1 port routed through the
// block-use dispatch seam (useBlockInteraction): a right-click on a grown bush (AGE 2/3) harvests the
// berries + resets AGE to 1 + consumes the action (no placement), and an ungrown bush (AGE 0/1) is a
// PASS (no drop, placement continues).

// sweetBerryBushState builds the sweet_berry_bush state at the given AGE (0..3).
func sweetBerryBushState(t *testing.T, age int) block.StateID {
	t.Helper()
	id, ok := block.ToStateID[block.SweetBerryBush{Age: block.Integer(age)}]
	if !ok {
		t.Fatalf("no state for sweet_berry_bush age=%d", age)
	}
	return id
}

// TestSweetBerryBushHarvestAge3: a right-click on a FULLY GROWN (AGE 3) bush pops berries, resets the
// bush to AGE 1, and consumes the action (no placement even with a block in hand). The loot roll +
// pitch draw are level.getRandom draws (not a pig stream), so at least one berry Item drops.
func TestSweetBerryBushHarvestAge3(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	// Hold a block item: a SUCCESS harvest must NOT place it (the block interaction consumes first).
	setHeldItem(p, item.Stone.ID, 5)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, sweetBerryBushState(t, 3), dimMinY)

	before := countItemEntities(loop)
	ui := useItemOnPacket(0, pos, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 21)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// The bush is reset to AGE 1.
	got, ok := mgr.GetBlock(pos, dimMinY)
	if !ok {
		t.Fatal("bush cell unreadable after harvest")
	}
	if block.SweetBerryAge(got) != 1 {
		t.Fatalf("after harvest AGE = %d, want 1 (state=%d %q)", block.SweetBerryAge(got), got, block.StateList[got].ID())
	}
	// Berries dropped: at least one Item entity was spawned (age 3 -> 2..3 berries, 1..2 stacks).
	if n := countItemEntities(loop) - before; n < 1 {
		t.Fatalf("harvest dropped %d Item entities, want >=1", n)
	}
	// The held stone was NOT placed on top (the block interaction consumed the action).
	if above, ok := mgr.GetBlock(pk.Position{X: 1, Y: 65, Z: 1}, dimMinY); ok && !block.IsAir(above) {
		t.Fatalf("harvest wrongly placed the held block above the bush: state=%d", above)
	}
	if int(ensureInventory(p).get(heldWindowSlot(0)).Count) != 5 {
		t.Fatal("harvest consumed a held item, want the stack untouched (no placement)")
	}
	// The editor got a BlockUpdate (the AGE reset broadcast) and the ack.
	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundBlockUpdate); n < 1 {
		t.Fatalf("harvest BlockUpdate = %d, want >=1 (the AGE reset broadcast)", n)
	}
}

// TestSweetBerryBushHarvestAge2: AGE 2 also harvests (age > 1), dropping the uniform(1,2) pool and
// resetting to AGE 1.
func TestSweetBerryBushHarvestAge2(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, sweetBerryBushState(t, 2), dimMinY)

	before := countItemEntities(loop)
	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 22)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	got, _ := mgr.GetBlock(pos, dimMinY)
	if block.SweetBerryAge(got) != 1 {
		t.Fatalf("age2 harvest AGE = %d, want 1", block.SweetBerryAge(got))
	}
	if n := countItemEntities(loop) - before; n < 1 {
		t.Fatalf("age2 harvest dropped %d Item entities, want >=1", n)
	}
}

// TestSweetBerryBushUngrownPass: a right-click on an AGE 0 (and AGE 1) bush is a PASS — NO berries
// drop, the AGE is unchanged, and (holding a block) placement CONTINUES (the held block is placed on
// top, since the block hook returned PASS). This is the `age <= 1 -> super.useWithoutItem` arm.
func TestSweetBerryBushUngrownPass(t *testing.T) {
	for _, age := range []int{0, 1} {
		loop, mgr := newBlockLoop()
		// Player at y=66 so it does NOT stand in the (1,65,1) placement cell (else its own body
		// obstructs the place via BlockItem.canPlace -> Level.isUnobstructed).
		p := blockPlayer(loop, 1.5, 66.0, 1.5)
		setHeldItem(p, item.Stone.ID, 5)

		pos := pk.Position{X: 1, Y: 64, Z: 1}
		start := sweetBerryBushState(t, age)
		mgr.SetBlock(pos, start, dimMinY)

		before := countItemEntities(loop)
		ui := useItemOnPacket(0, pos, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 23)
		loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

		// No berries dropped.
		if n := countItemEntities(loop) - before; n != 0 {
			t.Fatalf("age%d PASS dropped %d Item entities, want 0", age, n)
		}
		// The bush AGE is unchanged (not reset to 1).
		if got, _ := mgr.GetBlock(pos, dimMinY); got != start {
			t.Fatalf("age%d PASS changed the bush state %d -> %d, want unchanged", age, start, got)
		}
		// Placement CONTINUED: the held stone landed on the adjacent (top) face.
		above, ok := mgr.GetBlock(pk.Position{X: 1, Y: 65, Z: 1}, dimMinY)
		if !ok || above != block.ToStateID[block.Stone{}] {
			t.Fatalf("age%d PASS: block above = (%v, ok=%v), want stone (placement must continue)", age, above, ok)
		}
	}
}
