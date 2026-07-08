package server

// husk_test.go -- pins the Husk.doHurtTarget HUNGER effect (MOB-VARIANT): a landed husk melee with an
// empty mainhand gives the LivingEntity victim HUNGER for 140 * (int)getEffectiveDifficulty() ticks at
// amplifier 0. Cite Husk.doHurtTarget + DifficultyInstance.getEffectiveDifficulty.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
)

// TestHuskHungerOnHit: huskApplyHunger applies HUNGER 140*(int)effDiff / 0 to an empty-handed husk's
// victim; at gametime 0 the NORMAL effective difficulty is 1.5 -> (int)1 -> 140 ticks.
func TestHuskHungerOnHit(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 0 // effectiveDifficulty(NORMAL, 0, 0, 0) = 1.5 -> (int)1

	victim := combatPlayer(loop, 1)
	husk := NewEntity(70, entity.Husk, 0.5, 64, 0.5)
	husk.health = 20.0
	loop.only().entities.add(husk)

	loop.huskApplyHunger(husk, victim)

	e := victim.activeEffects[effectHunger]
	if e == nil {
		t.Fatalf("husk hit did not apply HUNGER")
	}
	want := huskHungerDurationBase * int(effectiveDifficulty(serverDifficulty, loop.gametime, 0, 0.0))
	if e.duration != want || e.amplifier != 0 {
		t.Fatalf("HUNGER = dur %d amp %d, want dur %d amp 0", e.duration, e.amplifier, want)
	}
	if want != 140 {
		t.Fatalf("expected 140 ticks at gametime 0 (NORMAL effDiff 1.5 -> int 1 -> 140), got %d", want)
	}
}

// TestHuskHungerSkipsWithHeldItem: Husk.doHurtTarget only applies HUNGER when getMainHandItem().isEmpty()
// -- a husk carrying a mainhand item skips the effect entirely.
func TestHuskHungerSkipsWithHeldItem(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 0

	victim := combatPlayer(loop, 1)
	husk := NewEntity(71, entity.Husk, 0.5, 64, 0.5)
	husk.health = 20.0
	loop.only().entities.add(husk)
	// Put a non-empty item in the husk's mainhand -> the empty-hand gate rejects the hunger.
	husk.setItemSlot(eqSlotMainHand, component.SlotData{ItemID: idBook, Count: 1})

	loop.huskApplyHunger(husk, victim)

	if victim.activeEffects[effectHunger] != nil {
		t.Fatalf("husk with a held item should NOT apply HUNGER (empty-hand gate)")
	}
}
