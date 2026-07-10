package server

// sniffer_test.go -- deterministic pins for the Sniffer dig loop + sniffer-egg breeding (1:1 javap this
// session). Verifies (1) a full dig cycle drops a seed item entity (torchflower_seeds / pitcher_pod) exactly
// at DROP_SEED_AT_TICK (digging-start + 120), and (2) sniffer breeding drops a sniffer_egg ITEM entity (NOT
// a live baby). The pig oracle (TestPluginPigEqualsGoNativePig) is untouched -- all sniffer logic is
// typ==entity.Sniffer.ID gated.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
)

// snifferLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick sniffers.
func snifferLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// countSnifferItems counts item entities carrying the given item id in the current region store.
func countSnifferItems(loop *TickLoop, itemID item.ID) int {
	n := 0
	for _, e := range loop.cur().entities.all() {
		if e.isItem && item.ID(e.itemStack.ItemID) == itemID {
			n++
		}
	}
	return n
}

// TestSnifferSpawnDefaults: spawnSniffer builds a Sniffer rendering as entity.Sniffer.ID starting IDLING.
func TestSnifferSpawnDefaults(t *testing.T) {
	loop, floorY := snifferLoop(t)
	s := loop.spawnSniffer(8.5, float64(floorY+1), 8.5, false)
	if s.typ != entity.Sniffer.ID {
		t.Fatalf("sniffer typ = %d, want entity.Sniffer.ID %d", s.typ, entity.Sniffer.ID)
	}
	if !s.isSniffer {
		t.Fatal("sniffer not marked isSniffer")
	}
	if s.snifferState != snifferStateIdling {
		t.Fatalf("fresh sniffer state = %d, want IDLING(%d)", s.snifferState, snifferStateIdling)
	}
	if s.health != snifferMaxHealth {
		t.Fatalf("sniffer health = %v, want %v (MAX_HEALTH)", s.health, snifferMaxHealth)
	}
}

// TestSnifferDigCycleDropsSeed: driving a grounded adult sniffer through the dig cycle drops exactly one
// seed item entity at DROP_SEED_AT_TICK (digging-start + 120). We advance aiTickCount each tick (the
// serverAiStep tickCount proxy) and run snifferAiStep, mirroring the per-tick game loop.
func TestSnifferDigCycleDropsSeed(t *testing.T) {
	loop, floorY := snifferLoop(t)
	s := loop.spawnSniffer(8.5, float64(floorY+1), 8.5, false)
	s.onGround = true // canDig requires onGround (the harness has a stone floor beneath)

	seedsBefore := countSnifferItems(loop, item.TorchflowerSeeds.ID) + countSnifferItems(loop, item.PitcherPod.ID)
	if seedsBefore != 0 {
		t.Fatalf("expected 0 seed items before the dig cycle, got %d", seedsBefore)
	}

	// Drive the cycle. A full cycle is at most: cooldown(0) + scenting(<=80) + sniffing(<=80) +
	// searching(600) + digging(<=180) + rising(40). 2000 ticks is a comfortable bound.
	dug := false
	var diggingStart int
	for i := 0; i < 4000; i++ {
		s.ai.aiTickCount++ // serverAiStep increments Entity.tickCount before customServerAiStep
		wasDigging := s.snifferState == snifferStateDigging
		loop.snifferAiStep(s)
		if !wasDigging && s.snifferState == snifferStateDigging {
			diggingStart = s.ai.aiTickCount
		}
		seeds := countSnifferItems(loop, item.TorchflowerSeeds.ID) + countSnifferItems(loop, item.PitcherPod.ID)
		if seeds > 0 && !dug {
			dug = true
			// The seed must drop at DROP_SEED_AT_TICK == diggingStart + 120.
			if s.ai.aiTickCount != diggingStart+snifferDropSeedOffset {
				t.Fatalf("seed dropped at tick %d, want diggingStart(%d)+%d = %d",
					s.ai.aiTickCount, diggingStart, snifferDropSeedOffset, diggingStart+snifferDropSeedOffset)
			}
			break
		}
	}
	if !dug {
		t.Fatalf("sniffer never dropped a seed after 4000 ticks (state=%d)", s.snifferState)
	}

	seeds := countSnifferItems(loop, item.TorchflowerSeeds.ID) + countSnifferItems(loop, item.PitcherPod.ID)
	if seeds != 1 {
		t.Fatalf("expected exactly 1 seed item after one dig, got %d", seeds)
	}
}

// TestSnifferBreedDropsEgg: breeding two sniffers drops a sniffer_egg ITEM entity (Sniffer.spawnChildFromBreeding
// override) and produces NO live baby sniffer. spawnBreedOffspring(sniffer) returns nil; breed() drops the egg.
func TestSnifferBreedDropsEgg(t *testing.T) {
	loop, floorY := snifferLoop(t)
	a := loop.spawnSniffer(8.5, float64(floorY+1), 8.5, false)
	b := loop.spawnSniffer(9.5, float64(floorY+1), 8.5, false)

	snifferBefore := 0
	for _, e := range loop.cur().entities.all() {
		if e.typ == entity.Sniffer.ID {
			snifferBefore++
		}
	}
	if snifferBefore != 2 {
		t.Fatalf("expected 2 sniffers before breeding, got %d", snifferBefore)
	}

	loop.breed(a, b)

	// A sniffer_egg item entity must now exist.
	if got := countSnifferItems(loop, item.SnifferEgg.ID); got != 1 {
		t.Fatalf("expected 1 sniffer_egg item after breeding, got %d", got)
	}

	// NO new live sniffer -- still exactly the two parents (no baby).
	snifferAfter := 0
	for _, e := range loop.cur().entities.all() {
		if e.typ == entity.Sniffer.ID {
			snifferAfter++
		}
	}
	if snifferAfter != 2 {
		t.Fatalf("expected still 2 sniffers after breeding (no live baby), got %d", snifferAfter)
	}

	// Both parents on the breeding cooldown (setAge(6000)); inLove reset.
	if a.breedAge != breedingCooldownAge || b.breedAge != breedingCooldownAge {
		t.Fatalf("parents not on breeding cooldown: a=%d b=%d, want %d", a.breedAge, b.breedAge, breedingCooldownAge)
	}
	if a.inLove != 0 || b.inLove != 0 {
		t.Fatalf("parents inLove not reset: a=%d b=%d", a.inLove, b.inLove)
	}
}
