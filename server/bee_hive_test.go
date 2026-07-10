package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// bee_hive_test.go -- validation gates for the Bee hive/pollination goal cluster (BEEHIVE-02), ported 1:1
// from the 26.2 jar. Uses the newBELoop harness + spawnBee.

// beeGoalBee spawns a bee at pos and returns it (with its bee AI wired).
func beeGoalBee(loop *TickLoop, x, y, z float64) *Entity {
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(7)
	b := loop.spawnBee(x, y, z, false)
	return b
}

// findGoal returns the first registered goal of the given type (by casting) from the bee AI, or nil.
func beePollinateOf(e *Entity) *beePollinateGoal {
	for _, wg := range e.ai.goals.goals {
		if g, ok := wg.g.(*beePollinateGoal); ok {
			return g
		}
	}
	return nil
}

// TestBeePollinateCompletionSetsNectar asserts BeePollinateGoal.stop sets hasNectar when the bee pollinated
// long enough (successfulPollinatingTicks > 400).
func TestBeePollinateCompletionSetsNectar(t *testing.T) {
	loop, _ := newBELoop()
	bee := beeGoalBee(loop, 2.5, 5.0, 2.5)
	g := beePollinateOf(bee)
	if g == nil {
		t.Fatal("bee AI has no BeePollinateGoal")
	}
	// Simulate a fully-pollinated goal: pollinated long enough, then stop.
	g.pollinating = true
	g.successfulPollinatingTicks = 401 // > MIN_POLLINATION_TICKS (400) -> hasPollinatedLongEnough
	if bee.beeHasNectar {
		t.Fatal("bee already had nectar before stop")
	}
	g.stop(loop, bee)
	if !bee.beeHasNectar {
		t.Fatal("BeePollinateGoal.stop did not set beeHasNectar after pollinating long enough")
	}
	// A stop WITHOUT enough pollination must NOT grant nectar.
	bee2 := beeGoalBee(loop, 2.5, 5.0, 2.5)
	g2 := beePollinateOf(bee2)
	g2.pollinating = true
	g2.successfulPollinatingTicks = 400 // NOT > 400 -> not long enough
	g2.stop(loop, bee2)
	if bee2.beeHasNectar {
		t.Fatal("stop granted nectar without pollinating long enough (needs strictly > 400)")
	}
}

// TestBeeEnterHiveAddsOccupant asserts BeeEnterHiveGoal.start deposits the bee into its hive via
// beehiveAddOccupant (occupant count rises, bee is discarded).
func TestBeeEnterHiveAddsOccupant(t *testing.T) {
	loop, mgr := newBELoop()
	hivePos := pk.Position{X: 2, Y: 5, Z: 2}
	mgr.SetBlock(hivePos, block.DefaultStateID["minecraft:beehive"], dimMinY)
	be := loop.resolveBeehive(hivePos)
	if be == nil {
		t.Fatal("resolveBeehive nil")
	}
	// Spawn the bee AT the hive center (within HIVE_CLOSE_ENOUGH_DISTANCE 2), carrying nectar so
	// wantsToEnterHive is true, hivePos set.
	bee := beeGoalBee(loop, 2.5, 5.5, 2.5)
	hp := hivePos
	bee.beeHivePos = &hp
	bee.beeHasNectar = true // wantsToEnterHive: hasNectar branch

	g := &beeEnterHiveGoal{}
	if !g.canBeeUse(loop, bee) {
		t.Fatal("BeeEnterHiveGoal.canBeeUse false; expected true (in range, wants to enter, hive has space)")
	}
	before := be.occupantCount()
	g.start(loop, bee)
	if be.occupantCount() != before+1 {
		t.Fatalf("occupantCount=%d after enter; want %d", be.occupantCount(), before+1)
	}
	if !bee.dead {
		t.Fatal("bee was not discarded after entering the hive")
	}
}

// TestBeeLocateHiveFindsNearbyHive asserts BeeLocateHiveGoal.start picks a nearby non-full hive and sets
// hivePos.
func TestBeeLocateHiveFindsNearbyHive(t *testing.T) {
	loop, mgr := newBELoop()
	hivePos := pk.Position{X: 3, Y: 5, Z: 3}
	mgr.SetBlock(hivePos, block.DefaultStateID["minecraft:beehive"], dimMinY)
	loop.resolveBeehive(hivePos) // register the hive in the store (the PoiManager analogue)

	bee := beeGoalBee(loop, 2.5, 5.0, 2.5)
	bee.beeHasNectar = true // wantsToEnterHive true (via hasNectar); no hive yet
	bee.beeHivePos = nil
	bee.beeRemainingCooldownLocatingHive = 0

	g := &beeLocateHiveGoal{}
	if !g.canBeeUse(loop, bee) {
		t.Fatal("BeeLocateHiveGoal.canBeeUse false; expected true (no hive, wants to enter, cooldown 0)")
	}
	g.start(loop, bee)
	if bee.beeHivePos == nil {
		t.Fatal("BeeLocateHiveGoal.start did not set hivePos to the nearby hive")
	}
	if *bee.beeHivePos != hivePos {
		t.Fatalf("hivePos=%v; want %v", *bee.beeHivePos, hivePos)
	}
	if bee.beeRemainingCooldownLocatingHive != beeCooldownLocatingNewHive {
		t.Fatalf("locate cooldown=%d; want %d", bee.beeRemainingCooldownLocatingHive, beeCooldownLocatingNewHive)
	}
}

// TestBeePollinateFindsFlower asserts BeePollinateGoal.canBeeUse finds a nearby BEE_ATTRACTIVE flower and
// sets savedFlowerPos.
func TestBeePollinateFindsFlower(t *testing.T) {
	loop, mgr := newBELoop()
	flowerPos := pk.Position{X: 3, Y: 5, Z: 2}
	mgr.SetBlock(flowerPos, block.DefaultStateID["minecraft:dandelion"], dimMinY)

	bee := beeGoalBee(loop, 2.5, 5.0, 2.5)
	bee.beeHasNectar = false
	bee.beeRemainingCooldownLocatingFlower = 0

	g := &beePollinateGoal{}
	if !g.canBeeUse(loop, bee) {
		t.Fatal("BeePollinateGoal.canBeeUse false; expected true (dandelion in range, no nectar, not raining)")
	}
	if bee.beeSavedFlowerPos == nil || *bee.beeSavedFlowerPos != flowerPos {
		t.Fatalf("savedFlowerPos=%v; want %v", bee.beeSavedFlowerPos, flowerPos)
	}
}
