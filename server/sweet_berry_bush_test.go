package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/loot"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
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

// TestSweetBerryBushHarvestDeterministicRNG locks the SweetBerryBushBlock.useWithoutItem RNG source
// to the per-region levelRandom (Level.getRandom analogue). A fixed-seed LegacyRandomSource oracle
// rolls the same table with the same source, then consumes 3 NextDouble per emitted stack (mirroring
// the production code's x/y/z Mth.nextDouble jitter draws) plus one NextFloat (the sound pitch), so
// the oracle is in lockstep with the production rng. The production harvest is then driven and the
// post-harvest state is asserted on THREE axes:
//
//  1. RNG lockstep: the production levelRandom's NEXT draw equals the oracle's NEXT draw (any
//     divergence means a future refactor reseeded, switched to math/rand/v2, or skipped a draw).
//  2. Deterministic drop count: the number of emitted Item entities matches the oracle's stack
//     count exactly (the loot table's age-3 pool uniform(2,3) + single-entry fast path yields 1).
//  3. Deterministic post-state: AGE is reset to 1 (the `setValue(AGE, 1)` side effect).
//
// Pinning this triple prevents a future refactor from re-introducing math/rand/v2 draws (the pig
// oracle's levelRandom stream is NEVER perturbed by a sweet-berry harvest) and from accidentally
// dropping the levelRandom capture or reseeding the context.
func TestSweetBerryBushHarvestDeterministicRNG(t *testing.T) {
	const seed = int64(0x5BB13F00D) // arbitrary fixed LCG seed; the oracle's deterministic counterpart.

	loop, mgr := newBlockLoop()
	// Seed the production levelRandom with the fixed seed (replaces the nondeterministic uniqueLevelRandomSeed
	// newRegion() would otherwise install) so the production stream is bit-reproducible across runs.
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)

	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, sweetBerryBushState(t, 3), dimMinY)
	beforeEntities := countItemEntities(loop)

	// Oracle: same seed, same LCG. Roll the SAME table the production handler rolls, with the SAME
	// source shape (NewBlockInteractLootContextWithSource -- the helper production calls). The seed
	// argument to Roll is IGNORED when ctx is non-nil, but passing 0 makes the intent explicit.
	oracle := levelgen.NewLegacyRandomSource(seed)
	oracleCtx := loot.NewBlockInteractLootContextWithSource(oracle, "minecraft:sweet_berry_bush", map[string]string{"age": "3"})
	tbl, err := loot.LoadTable(sweetBerryHarvestTable)
	if err != nil {
		t.Fatalf("load sweet_berry_bush table: %v", err)
	}
	expectedDrops := loot.Roll(tbl, 0, oracleCtx)
	// Mirror the per-stack jitter draws the production code performs (x, y, z = 3 NextDouble per drop).
	for range expectedDrops {
		_ = oracle.NextDouble()
		_ = oracle.NextDouble()
		_ = oracle.NextDouble()
	}
	// Mirror the post-roll pitch draw (0.8F + nextFloat() * 0.4F -- one NextFloat regardless of drop count).
	_ = oracle.NextFloat()

	// Run the production harvest via the standard block-use dispatch seam.
	ui := useItemOnPacket(0, pos, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 42)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// (2) Deterministic drop count: production must emit exactly the same stack count the oracle did.
	gotDrops := countItemEntities(loop) - beforeEntities
	if gotDrops != len(expectedDrops) {
		t.Fatalf("drop count = %d, want %d (oracle lockstep; the age-3 pool is a single-entry uniform(2,3))",
			gotDrops, len(expectedDrops))
	}
	// (3) Deterministic post-state: AGE reset to 1 (the `state.setValue(AGE, 1)` side effect).
	got, ok := mgr.GetBlock(pos, dimMinY)
	if !ok {
		t.Fatalf("after harvest GetBlock(%v) not ok", pos)
	}
	if block.SweetBerryAge(got) != 1 {
		t.Fatalf("after harvest AGE = %d, want 1", block.SweetBerryAge(got))
	}
	// (1) RNG lockstep: the production levelRandom's NEXT draw must equal the oracle's NEXT draw.
	prodNext := loop.only().levelRandom.NextLong()
	oracleNext := oracle.NextLong()
	if prodNext != oracleNext {
		t.Fatalf("production stream diverged: prod.NextLong=%d, oracle.NextLong=%d (a draw was reseeded, "+
			"skipped, or routed through math/rand/v2 instead of the shared levelRandom)", prodNext, oracleNext)
	}
}
