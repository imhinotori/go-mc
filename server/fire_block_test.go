package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// fire_block_test.go — the BLOCK FIRE gate (fire_block.go). It proves the flammability table
// matches the jar, fireTick ages a young fire, fire spreads to a flammable air cell, and
// fireCheckBurnOut removes a flammable block. RNG is a seeded LegacyRandomSource (Level.random),
// matching the jar java.util.Random draw order.

func fireState(t *testing.T, age int) block.StateID {
	s, ok := block.FireWithAge(mustFireDefault(t), age)
	if !ok {
		t.Fatalf("no fire state for age %d", age)
	}
	return s
}

func mustFireDefault(t *testing.T) block.StateID {
	s, ok := block.FireDefaultState()
	if !ok {
		t.Fatal("no default fire state")
	}
	return s
}

func TestFireFlammabilityTableMatchesJar(t *testing.T) {
	cases := []struct {
		id            string
		wantIg, wantB int
	}{
		{"minecraft:oak_planks", 5, 20},
		{"minecraft:spruce_planks", 5, 20},
		{"minecraft:oak_log", 5, 5},
		{"minecraft:oak_leaves", 30, 60},
		{"minecraft:white_wool", 60, 20},
		{"minecraft:black_wool", 60, 20},
		{"minecraft:white_carpet", 30, 60},
		{"minecraft:red_carpet", 30, 60},
		{"minecraft:tnt", 15, 100},
		{"minecraft:coal_block", 5, 5},
		{"minecraft:bookshelf", 30, 20},
		{"minecraft:hay_block", 60, 20},
		{"minecraft:target", 15, 20},
		{"minecraft:vine", 15, 100},
		{"minecraft:dandelion", 60, 100},
		{"minecraft:pale_moss_carpet", 5, 100},
		{"minecraft:mangrove_roots", 5, 20},
		{"minecraft:oak_shelf", 30, 20},
		{"minecraft:stone", 0, 0},
		{"minecraft:netherrack", 0, 0},
		{"minecraft:moss_carpet", 0, 0},
	}
	for _, c := range cases {
		if got := fireIgniteTable[c.id]; got != c.wantIg {
			t.Errorf("%s ignite = %d, want %d", c.id, got, c.wantIg)
		}
		if got := fireBurnTable[c.id]; got != c.wantB {
			t.Errorf("%s burn = %d, want %d", c.id, got, c.wantB)
		}
	}
}

func TestFireIgniteOddsWaterloggedZero(t *testing.T) {
	loop, _, _ := newRandomTickLoop()
	dry := block.ToStateID[block.OakFence{}]
	wet := block.ToStateID[block.OakFence{Waterlogged: true}]
	if got := loop.fireIgniteOdds(dry); got != 5 {
		t.Fatalf("dry oak_fence ignite = %d, want 5", got)
	}
	if got := loop.fireIgniteOdds(wet); got != 0 {
		t.Fatalf("waterlogged oak_fence ignite = %d, want 0", got)
	}
	if got := loop.fireBurnOdds(wet); got != 0 {
		t.Fatalf("waterlogged oak_fence burn = %d, want 0", got)
	}
}

// TestIgniteFireAtPlainFire proves the flint&steel PLAIN-FIRE branch (FlintAndSteelItem.useOn ->
// BaseFireBlock.canBePlacedAt's getState().canSurvive() disjunct): igniteFireAt lights a fire in an
// AIR cell that has a sturdy floor below it, and schedules the fire's first FireBlock tick so it
// spreads/burns out. A cell with no sturdy floor and no burnable neighbour (fire cannot survive) is
// NOT lit. CITE: FlintAndSteelItem.useOn; BaseFireBlock.canBePlacedAt / getState; FireBlock.canSurvive.
func TestIgniteFireAtPlainFire(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(0xF14E)

	// Air cell over a sturdy stone floor -> fireCanSurvive true -> fire is lit.
	pos := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(below(pos), block.ToStateID[block.Stone{}], dimMinY)
	// pos is air by default in the empty chunk.
	if !loop.igniteFireAt(pos) {
		t.Fatal("igniteFireAt should light a fire on a sturdy floor")
	}
	if got := mustGet(t, mgr, pos); !block.IsFire(got) {
		t.Fatalf("cell is %v after ignite, want fire", block.StateList[got].ID())
	}

	// Air cell floating in air (no sturdy floor, no burnable neighbour) -> fireCanSurvive false -> no fire.
	empty := pk.Position{X: 8, Y: 80, Z: 8} // surrounded by air
	if loop.igniteFireAt(empty) {
		t.Fatal("igniteFireAt must NOT light a fire where it cannot survive (no floor, no burnable neighbour)")
	}
	if got := mustGet(t, mgr, empty); block.IsFire(got) {
		t.Fatal("no fire should have been placed in the floating-air cell")
	}
}

func TestFireTickAgesUp(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0xF16E)

	firePos := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(below(firePos), block.ToStateID[block.Stone{}], dimMinY)
	mgr.SetBlock(firePos, fireState(t, 0), dimMinY)

	loop.fireTick(r, mustGet(t, mgr, firePos), firePos)

	got := mustGet(t, mgr, firePos)
	if !block.IsFire(got) {
		t.Fatalf("fire on a sturdy floor with no burnable neighbour must survive; got %v", block.StateList[got].ID())
	}
	if age := block.FireAge(got); age < 0 || age > 15 {
		t.Fatalf("fire age after tick = %d, want 0..15", age)
	}
}

func TestFireSpreadsToFlammableNeighbour(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0x5EED)

	firePos := pk.Position{X: 8, Y: 65, Z: 8}
	// oak_planks below the source keeps isValidFireLocation TRUE (a burnable face-neighbour), so the
	// source fire survives its canSurvive/valid-location gates and reaches the spread loop. We RESTORE
	// this floor + re-light the source each iteration (test-harness immortality): the point under test
	// is the SPREAD path, not the source's own burn-out (which checkBurnOut/aging would otherwise cause
	// — that is exercised by the burn-out tests). CITE: FireBlock.isValidFireLocation (burnable neighbour).
	mgr.SetBlock(below(firePos), block.ToStateID[block.OakPlanks{}], dimMinY)
	mgr.SetBlock(firePos, fireState(t, 15), dimMinY)

	// Target: an air cell one east of the source with oak_planks below it -> getIgniteOdds(level, cell)
	// == 5, so the spread chance is (5 + 40 + 2*7) / (15 + 30) == 1, firing when nextInt(l) <= 1.
	spreadCell := east(firePos)
	mgr.SetBlock(below(spreadCell), block.ToStateID[block.OakPlanks{}], dimMinY)

	const maxTicks = 200000
	spread := false
	for i := 0; i < maxTicks; i++ {
		// Keep the source alive + supported (harness immortality) so every tick reaches the spread loop.
		mgr.SetBlock(below(firePos), block.ToStateID[block.OakPlanks{}], dimMinY)
		mgr.SetBlock(firePos, fireState(t, 15), dimMinY)
		mgr.SetBlock(below(spreadCell), block.ToStateID[block.OakPlanks{}], dimMinY)
		loop.fireTick(r, mustGet(t, mgr, firePos), firePos)
		if block.IsFire(mustGet(t, mgr, spreadCell)) {
			spread = true
			break
		}
	}
	if !spread {
		t.Fatalf("fire never spread to the adjacent flammable air cell over %d ticks", maxTicks)
	}
}

func TestFireCheckBurnOutRemovesBlock(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 2, Y: 65, Z: 2}
	const age = 15
	const chance = 300

	var chosen int64 = -1
	for seed := int64(1); seed < 5000; seed++ {
		rr := levelgen.NewLegacyRandomSource(seed)
		if rr.NextIntN(chance) < 20 && rr.NextIntN(age+10) >= 5 {
			chosen = seed
			break
		}
	}
	if chosen < 0 {
		t.Fatal("no seed found exercising the burn-out removeBlock path")
	}
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(chosen)
	mgr.SetBlock(pos, block.ToStateID[block.OakPlanks{}], dimMinY)

	loop.fireCheckBurnOut(loop.only(), pos, chance, age)

	if got := mustGet(t, mgr, pos); !block.IsAir(got) {
		t.Fatalf("oak_planks after burn-out = %v, want air", block.StateList[got].ID())
	}
}

func TestFireCheckBurnOutNoBurnWhenRollHigh(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 3, Y: 65, Z: 3}
	const chance = 300

	var chosen int64 = -1
	for seed := int64(1); seed < 5000; seed++ {
		rr := levelgen.NewLegacyRandomSource(seed)
		if rr.NextIntN(chance) >= 20 {
			chosen = seed
			break
		}
	}
	if chosen < 0 {
		t.Fatal("no seed found with a high burn roll")
	}
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(chosen)
	mgr.SetBlock(pos, block.ToStateID[block.OakPlanks{}], dimMinY)

	loop.fireCheckBurnOut(loop.only(), pos, chance, 5)

	if _, ok := block.StateList[mustGet(t, mgr, pos)].(block.OakPlanks); !ok {
		t.Fatalf("oak_planks must be untouched when the burn roll is high")
	}
}
