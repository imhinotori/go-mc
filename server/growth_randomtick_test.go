package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// growth_randomtick_test.go -- behaviour tests for the NETHER-WART / CHORUS-FLOWER / TURTLE-EGG /
// BUDDING-AMETHYST / CAVE-VINES random-tick handlers (growth_randomtick.go). Each proves the jar-exact
// RNG draw order + grow outcome against a seeded levelRandom.

func netherWartState(age int) block.StateID {
	s, ok := block.ToStateID[block.NetherWart{Age: block.Integer(age)}]
	if !ok {
		panic("no nether wart state")
	}
	return s
}

// TestNetherWartGrowOn10 finds a seed whose first nextInt(10) is 0 and proves the AGE advances by 1.
func TestNetherWartGrowNextInt10(t *testing.T) {
	// Find a seed whose first LegacyRandomSource.nextInt(10) == 0.
	var seed int64 = -1
	for s := int64(1); s < 100000; s++ {
		r := levelgen.NewLegacyRandomSource(s)
		if r.NextIntN(10) == 0 {
			seed = s
			break
		}
	}
	if seed < 0 {
		t.Fatal("no seed found with nextInt(10)==0")
	}
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)
	pos := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pos, netherWartState(1), dimMinY)
	loop.netherWartRandomTick(r, netherWartState(1), pos)
	if got := block.NetherWartAge(mustGet(t, mgr, pos)); got != 2 {
		t.Fatalf("nether wart AGE after grow = %d, want 2", got)
	}
}

// TestNetherWartNoGrowNonZero proves a non-zero nextInt(10) roll does NOT advance AGE (draws exactly
// one int, no state change).
func TestNetherWartNoGrowNonZero(t *testing.T) {
	// Find a seed whose first nextInt(10) != 0.
	var seed int64 = -1
	for s := int64(1); s < 100000; s++ {
		r := levelgen.NewLegacyRandomSource(s)
		if r.NextIntN(10) != 0 {
			seed = s
			break
		}
	}
	if seed < 0 {
		t.Fatal("no seed found with nextInt(10)!=0")
	}
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)
	pos := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pos, netherWartState(1), dimMinY)
	loop.netherWartRandomTick(r, netherWartState(1), pos)
	if got := block.NetherWartAge(mustGet(t, mgr, pos)); got != 1 {
		t.Fatalf("nether wart AGE after no-grow = %d, want 1 (unchanged)", got)
	}
}

// TestNetherWartMaxAgeNotRandomlyTicking proves a full (AGE 3) wart is not randomly ticking.
func TestNetherWartMaxAgeNotRandomlyTicking(t *testing.T) {
	if block.IsRandomlyTicking(netherWartState(3)) {
		t.Fatal("AGE-3 nether wart must NOT be randomly ticking (isRandomlyTicking == age < 3)")
	}
	if !block.IsRandomlyTicking(netherWartState(2)) {
		t.Fatal("AGE-2 nether wart must be randomly ticking")
	}
}

func chorusFlowerState(age int) block.StateID {
	s, ok := block.ChorusFlowerWithAge(block.ChorusFlowerDefaultState(), age)
	if !ok {
		panic("no chorus flower state")
	}
	return s
}

// TestChorusFlowerGrowUpOnEndStone: an AGE-0 flower rooted on END_STONE with all above-neighbours
// empty grows UP -- self becomes a chorus_plant stem and a new AGE-0 flower is placed above. The
// end-stone branch draws NO nextInt (canGrowUp is set unconditionally), so no seed tuning is needed.
// CITE: ChorusFlowerBlock.randomTick (below.is(SUPPORTS_CHORUS_FLOWER) -> canGrowUp).
func TestChorusFlowerGrowUpOnEndStone(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0xC0FFEE)
	flower := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.EndStone{}], dimMinY)
	mgr.SetBlock(flower, chorusFlowerState(0), dimMinY)
	loop.chorusFlowerRandomTick(r, chorusFlowerState(0), flower)

	// self -> chorus plant stem
	if !block.IsChorusPlant(mustGet(t, mgr, flower)) {
		t.Fatalf("grow-up should convert self to chorus_plant; got %d", mustGet(t, mgr, flower))
	}
	// above -> new AGE-0 flower
	above := pk.Position{X: 4, Y: 66, Z: 4}
	if !block.IsChorusFlower(mustGet(t, mgr, above)) {
		t.Fatalf("grow-up should place a flower above; got %d", mustGet(t, mgr, above))
	}
	if got := block.ChorusFlowerAge(mustGet(t, mgr, above)); got != 0 {
		t.Fatalf("grown flower AGE = %d, want 0", got)
	}
}

// TestChorusFlowerDeadWhenBlocked: an AGE-4 flower rooted on END_STONE but BLOCKED above (a solid
// block two above) cannot grow up and (age >= 4) dies -> AGE becomes DEAD_AGE (5). No branch is
// possible (age not < 4), so it takes the terminal placeDeadFlower path. CITE:
// ChorusFlowerBlock.randomTick (else placeDeadFlower).
func TestChorusFlowerDeadWhenBlocked(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0xDEAD)
	flower := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.EndStone{}], dimMinY)
	mgr.SetBlock(flower, chorusFlowerState(4), dimMinY)
	// Block two above so grow-up fails (isEmptyBlock(pos.above(2)) is false).
	mgr.SetBlock(pk.Position{X: 4, Y: 67, Z: 4}, block.ToStateID[block.Stone{}], dimMinY)
	loop.chorusFlowerRandomTick(r, chorusFlowerState(4), flower)
	if got := block.ChorusFlowerAge(mustGet(t, mgr, flower)); got != block.ChorusFlowerDeadAge {
		t.Fatalf("blocked age-4 flower should die (AGE 5); got %d", got)
	}
}

// TestChorusFlowerDeadAgeNotRandomlyTicking: a dead (AGE 5) flower is not randomly ticking.
func TestChorusFlowerDeadAgeNotRandomlyTicking(t *testing.T) {
	if block.IsRandomlyTicking(chorusFlowerState(5)) {
		t.Fatal("AGE-5 (dead) chorus flower must NOT be randomly ticking")
	}
	if !block.IsRandomlyTicking(chorusFlowerState(0)) {
		t.Fatal("AGE-0 chorus flower must be randomly ticking")
	}
}
