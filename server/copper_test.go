package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// copper_test.go -- the COPPER OXIDATION random-tick gate (copper.go + level/block/copper.go). It
// proves:
//   - NEXT_BY_BLOCK transitions: copper_block -> exposed_copper -> weathered_copper -> oxidized_copper,
//     and OXIDIZED has no next (CopperCanOxidize false);
//   - the changeOverTime 0.05688889 first-gate short-circuits with exactly one nextFloat;
//   - a fully-isolated copper block oxidizes when both nextFloat rolls pass (chance = 1*1*0.75 for the
//     unaffected tier);
//   - a LESS-oxidized copper neighbour halts oxidation (getNextState empty) with NO second draw.
// Uses the shared newRandomTickLoop / mustGet helpers.

func copperBlockID(id string) block.StateID {
	s, ok := block.DefaultStateID[id]
	if !ok {
		panic("no block " + id)
	}
	return s
}

// TestCopperNextByBlockChain: the NEXT_BY_BLOCK map advances each tier to the next and stops at
// oxidized. CITE: WeatheringCopper.NEXT_BY_BLOCK.
func TestCopperNextByBlockChain(t *testing.T) {
	chain := []string{"minecraft:copper_block", "minecraft:exposed_copper", "minecraft:weathered_copper", "minecraft:oxidized_copper"}
	for i := 0; i < 3; i++ {
		cur := copperBlockID(chain[i])
		want := copperBlockID(chain[i+1])
		got, ok := block.CopperGetNext(cur)
		if !ok || got != want {
			t.Fatalf("%s -> next: got (%d, %v), want %d", chain[i], got, ok, want)
		}
		if !block.CopperCanOxidize(cur) {
			t.Fatalf("%s should be able to oxidize", chain[i])
		}
	}
	oxi := copperBlockID("minecraft:oxidized_copper")
	if _, ok := block.CopperGetNext(oxi); ok {
		t.Fatal("oxidized_copper must have no next tier")
	}
	if block.CopperCanOxidize(oxi) {
		t.Fatal("oxidized_copper must not random-tick")
	}
	if block.IsRandomlyTicking(oxi) {
		t.Fatal("oxidized_copper must not be randomly ticking")
	}
	if !block.IsRandomlyTicking(copperBlockID("minecraft:copper_block")) {
		t.Fatal("copper_block must be randomly ticking")
	}
}

// TestCopperFirstGateShortCircuits: a first nextFloat >= 0.05688889 returns immediately with exactly
// one draw and no block change. CITE: ChangeOverTimeBlock.changeOverTime.
func TestCopperFirstGateShortCircuits(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	// Find a seed whose FIRST nextFloat is >= the change-over-time chance.
	var seed int64
	for s := int64(1); s < 100000; s++ {
		if levelgen.NewLegacyRandomSource(s).NextFloat() >= float32(copperChangeOverTimeChance) {
			seed = s
			break
		}
	}
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	cp := pk.Position{X: 5, Y: 70, Z: 5}
	mgr.SetBlock(cp, copperBlockID("minecraft:copper_block"), dimMinY)
	loop.copperRandomTick(r, copperBlockID("minecraft:copper_block"), cp)

	if got := mustGet(t, mgr, cp); got != copperBlockID("minecraft:copper_block") {
		t.Fatalf("copper oxidized despite a failed first gate; got %d", got)
	}
	fresh := levelgen.NewLegacyRandomSource(seed)
	fresh.NextFloat()
	if r.levelRandom.NextFloat() != fresh.NextFloat() {
		t.Fatal("first-gate short-circuit drew != 1 nextFloat")
	}
}

// TestCopperOxidizesIsolated: an isolated copper block (no copper neighbours) oxidizes when both
// nextFloat rolls pass. With moreOxidized=lessOxidized=0, f = 1/1 = 1, chance = 1*1*0.75 = 0.75. We
// pick a seed whose first roll < 0.05688889 and second roll < 0.75. CITE: ChangeOverTimeBlock.getNextState.
func TestCopperOxidizesIsolated(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	var seed int64
	for s := int64(1); s < 5000000; s++ {
		rr := levelgen.NewLegacyRandomSource(s)
		if rr.NextFloat() < float32(copperChangeOverTimeChance) && rr.NextFloat() < 0.75 {
			seed = s
			break
		}
	}
	if seed == 0 {
		t.Fatal("no oxidize seed found")
	}
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	cp := pk.Position{X: 6, Y: 70, Z: 6}
	mgr.SetBlock(cp, copperBlockID("minecraft:copper_block"), dimMinY)
	loop.copperRandomTick(r, copperBlockID("minecraft:copper_block"), cp)

	if got := mustGet(t, mgr, cp); got != copperBlockID("minecraft:exposed_copper") {
		t.Fatalf("isolated copper did not oxidize to exposed_copper; got %d", got)
	}
}

// TestCopperLessOxidizedNeighbourHalts: a less-oxidized copper neighbour makes getNextState return
// empty BEFORE the second nextFloat -- so an EXPOSED block next to plain copper_block does not
// oxidize, and only the first nextFloat is drawn. CITE: ChangeOverTimeBlock.getNextState (o < age ->
// Optional.empty).
func TestCopperLessOxidizedNeighbourHalts(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	// Seed whose FIRST roll passes the change gate (so getNextState is entered).
	var seed int64
	for s := int64(1); s < 5000000; s++ {
		if levelgen.NewLegacyRandomSource(s).NextFloat() < float32(copperChangeOverTimeChance) {
			seed = s
			break
		}
	}
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	cp := pk.Position{X: 8, Y: 70, Z: 8}
	mgr.SetBlock(cp, copperBlockID("minecraft:exposed_copper"), dimMinY)
	// A LESS-oxidized neighbour (plain copper_block, ordinal 0 < exposed 1) within Manhattan 4.
	mgr.SetBlock(pk.Position{X: cp.X + 1, Y: cp.Y, Z: cp.Z}, copperBlockID("minecraft:copper_block"), dimMinY)

	loop.copperRandomTick(r, copperBlockID("minecraft:exposed_copper"), cp)

	if got := mustGet(t, mgr, cp); got != copperBlockID("minecraft:exposed_copper") {
		t.Fatalf("exposed copper oxidized despite a less-oxidized neighbour; got %d", got)
	}
	// Only ONE nextFloat drawn (the change gate); the halt returns before the second roll.
	fresh := levelgen.NewLegacyRandomSource(seed)
	fresh.NextFloat()
	if r.levelRandom.NextFloat() != fresh.NextFloat() {
		t.Fatal("less-oxidized-neighbour halt drew a second nextFloat (must not)")
	}
}
