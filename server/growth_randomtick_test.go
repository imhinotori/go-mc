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
