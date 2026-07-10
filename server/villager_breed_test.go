package server

// villager_breed_test.go -- coverage for GAP 1 (VILLAGERS NEVER BREED): the IDLE-package breed chain
// (brain_villager.go newVillagerInteractWithBreedTarget + newVillagerMakeLove) now lets two willing adult
// villagers with an available bed breed a baby villager. Ported 1:1 from net.minecraft.world.entity.ai
// .behavior.VillagerMakeLove + InteractWith + Villager.canBreed. The pig oracle (TestPluginPigEqualsGoNativePig)
// is UNTOUCHED -- the breed chain is villager-gated and its RNG draws off the villager MOB stream, never the
// pig oracle stream.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// makeWillingAdultVillager spawns a fed adult villager (canBreed true: foodLevel>=12, breedAge 0, awake).
func makeWillingAdultVillager(loop *TickLoop, x, y, z float64) *Entity {
	e := loop.spawnVanillaMob(vanillaVillagerMobName, x, y, z)
	if e == nil {
		return nil
	}
	e.villagerFoodLevel = villagerBreedFoodThreshold // fed: foodLevel == 12 -> canBreed food gate passes
	e.breedAge = 0                                   // getAge() == 0 (adult, off cooldown)
	return e
}

// TestVillagerCanBreedGate pins Villager.canBreed(): food >= 12 && !isSleeping && getAge()==0.
func TestVillagerCanBreedGate(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	e := loop.spawnVanillaMob(vanillaVillagerMobName, 8.5, 64, 8.5)
	// Fresh villager: foodLevel 0 -> cannot breed.
	if villagerCanBreed(e) {
		t.Fatal("an un-fed villager (foodLevel 0) must NOT be able to breed")
	}
	e.villagerFoodLevel = 11
	if villagerCanBreed(e) {
		t.Fatal("foodLevel 11 (< 12) must NOT allow breeding")
	}
	e.villagerFoodLevel = 12
	if !villagerCanBreed(e) {
		t.Fatal("foodLevel 12 adult awake villager MUST be able to breed")
	}
	// A baby (breedAge < 0) cannot breed even when fed.
	e.breedAge = babyStartAge
	if villagerCanBreed(e) {
		t.Fatal("a baby villager must NOT be able to breed (getAge() != 0)")
	}
	// An adult on breeding cooldown (breedAge > 0) cannot breed.
	e.breedAge = 100
	if villagerCanBreed(e) {
		t.Fatal("a villager on breeding cooldown (getAge() > 0) must NOT be able to breed")
	}
}

// TestVillagerInteractWithSetsBreedTarget pins the pairing behavior: two willing adult villagers in range
// find each other, and the InteractWith sets BREED_TARGET to the partner.
func TestVillagerInteractWithSetsBreedTarget(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	loop.withRegion(loop.only(), func() {
		a := makeWillingAdultVillager(loop, 8.5, 64, 8.5)
		b := makeWillingAdultVillager(loop, 9.5, 64, 8.5) // 1 block away, within interactionRange 8
		if a == nil || b == nil {
			t.Fatal("failed to spawn the two adult villagers")
		}

		interact := newVillagerInteractWithBreedTarget()
		// WALK_TARGET must be absent for the entry group; a fresh villager has it registered+empty.
		if !interact.behavior.hasRequiredMemories(a) {
			t.Fatal("InteractWith entry memories (BREED_TARGET/LOOK_TARGET registered, WALK_TARGET absent) not satisfied")
		}
		if !interact.trigger(loop, a, 0) {
			t.Fatal("two willing adults in range: InteractWith must set BREED_TARGET")
		}
		id, ok := a.brain.getMemoryEntityID(memBreedTarget)
		if !ok || id != b.id {
			t.Fatalf("BREED_TARGET = %d (ok=%v), want partner id %d", id, ok, b.id)
		}
	})
}

// TestTwoWillingVillagersBreedBaby is THE PAYOFF: two willing adult villagers with a vacant bed run the
// VillagerMakeLove behavior; once the birth timer elapses a BABY villager spawns and both parents go on the
// 6000-tick breeding cooldown. VERIFIED chain VillagerMakeLove.start/tick/tryToGiveBirth/breed + takeVacantBed.
func TestTwoWillingVillagersBreedBaby(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	bed := block.DefaultStateID["minecraft:red_bed"]
	air := block.ToStateID[block.Air{}]

	loop.withRegion(loop.only(), func() {
		a := makeWillingAdultVillager(loop, 8.5, 64, 8.5)
		b := makeWillingAdultVillager(loop, 9.0, 64, 8.5) // within distanceToSqr <= 5.0 for birth
		if a == nil || b == nil {
			t.Fatal("failed to spawn the two adult villagers")
		}

		// Place a bed near the parents -> a vacant HOME POI (maxTickets 1) for takeVacantBed.
		bedPos := pk.Position{X: 10, Y: 64, Z: 8}
		loop.updatePoiOnBlockStateChange(bedPos, air, bed)
		pm := loop.only().poiManager
		if pm == nil || pm.recordAt(bedPos) == nil {
			t.Fatal("placing the bed must create a HOME POI record")
		}

		// Count villagers before breeding.
		before := 0
		for _, e := range loop.only().entities.all() {
			if e.typ == entity.Villager.ID {
				before++
			}
		}
		if before != 2 {
			t.Fatalf("expected 2 villagers before breeding, got %d", before)
		}

		// Set BREED_TARGET on A (the pairing step) and run VillagerMakeLove.
		a.brain.setMemory(memBreedTarget, b.id)
		ml := newVillagerMakeLove()
		if !ml.checkExtraStart(loop, a) {
			t.Fatal("VillagerMakeLove.checkExtraStartConditions (isBreedingPossible) must be true for two willing adults")
		}

		loop.gametime = 100
		ml.start(loop, a, loop.gametime) // rolls birthTimestamp = 100 + 275 + nextInt(50)

		// Advance past the max birth delay (275 + 49) and tick -> birth fires.
		loop.gametime = 100 + villagerMakeLoveBaseBirthDelay + villagerMakeLoveBirthJitter + 1
		ml.tick(loop, a, loop.gametime)

		// A baby villager must have spawned.
		after := 0
		babies := 0
		for _, e := range loop.only().entities.all() {
			if e.typ == entity.Villager.ID {
				after++
				if e.isBaby() {
					babies++
				}
			}
		}
		if after != 3 {
			t.Fatalf("expected 3 villagers after breeding (2 parents + 1 baby), got %d", after)
		}
		if babies != 1 {
			t.Fatalf("expected exactly 1 baby villager, got %d", babies)
		}

		// Both parents on the 6000-tick breeding cooldown (setAge(6000)).
		if a.breedAge != breedingCooldownAge || b.breedAge != breedingCooldownAge {
			t.Fatalf("parents must be on breeding cooldown: a.breedAge=%d b.breedAge=%d, want %d",
				a.breedAge, b.breedAge, breedingCooldownAge)
		}
		// The bed was claimed (IS_OCCUPIED) by the baby (giveBedToChild via takeVacantBed).
		if rec := pm.recordAt(bedPos); rec == nil || !rec.isOccupied() {
			t.Fatalf("the baby's bed POI must be IS_OCCUPIED after takeVacantBed, rec=%v", rec)
		}
	})
}

// TestVillagerMakeLoveNoBedNoBaby pins the tryToGiveBirth no-bed branch: with NO vacant bed in range, the
// birth produces no baby (sad particles only) and the parents are NOT put on cooldown by breed().
func TestVillagerMakeLoveNoBedNoBaby(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	loop.withRegion(loop.only(), func() {
		a := makeWillingAdultVillager(loop, 8.5, 64, 8.5)
		b := makeWillingAdultVillager(loop, 9.0, 64, 8.5)
		if a == nil || b == nil {
			t.Fatal("failed to spawn villagers")
		}

		before := 0
		for _, e := range loop.only().entities.all() {
			if e.typ == entity.Villager.ID {
				before++
			}
		}

		a.brain.setMemory(memBreedTarget, b.id)
		ml := newVillagerMakeLove()
		loop.gametime = 100
		ml.start(loop, a, loop.gametime)
		loop.gametime = 100 + villagerMakeLoveBaseBirthDelay + villagerMakeLoveBirthJitter + 1
		ml.tick(loop, a, loop.gametime)

		after := 0
		for _, e := range loop.only().entities.all() {
			if e.typ == entity.Villager.ID {
				after++
			}
		}
		if after != before {
			t.Fatalf("with no vacant bed, NO baby must spawn: villagers %d -> %d", before, after)
		}
	})
}
