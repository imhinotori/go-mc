package server

// totem_test.go -- PLATE/TOTEM validation gates for checkTotemDeathProtection (LivingEntity), the
// player + mob totem-of-undying revive. Each asserts the ported behavior against the unobfuscated 26.2
// jar (net.minecraft.world.entity.LivingEntity.checkTotemDeathProtection + DeathProtection clinit):
//   - a would-be-lethal hit with a totem held -> survive at 1 HP, totem consumed, REGEN/ABSORPTION/
//     FIRE_RESISTANCE applied, and the totem-pop entity event (35) broadcast.
//   - no totem -> the player/mob dies normally (identical old path).
//   - a BYPASSES_INVULNERABILITY source (the void) ignores the totem and kills anyway.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/tag"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// totemStack is a 1-count Totem of Undying (item id 1333) SlotData.
func totemStack() component.SlotData {
	return component.SlotData{Count: 1, ItemID: pk.VarInt(totemOfUndyingItemID)}
}

// giveTotemMainHand puts a totem in the player selected hotbar slot.
func giveTotemMainHand(p *tickPlayer) {
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), totemStack())
}

// TestTotemPlayerSurvivesLethal: a player at full health taking a lethal hit while holding a totem
// survives at exactly 1 HP, the totem is consumed, and the three death_protection effects are applied.
func TestTotemPlayerSurvivesLethal(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	giveTotemMainHand(p)

	// A 999 generic hit would kill outright; the totem cancels the death and revives at 1 HP.
	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 999)

	if p.dead {
		t.Fatalf("player with a totem died on a lethal hit (want survive)")
	}
	if p.health != 1.0 {
		t.Fatalf("totem revive health = %v, want 1.0", p.health)
	}
	// The totem was consumed from the main hand.
	got := playerItemBySlot(p, eqSlotMainHand)
	if got.Count != 0 {
		t.Fatalf("totem not consumed: mainhand count = %d, want 0", got.Count)
	}
	// The three death_protection effects are present at the jar durations/amplifiers.
	assertEffect(t, p, effectRegeneration, totemRegenerationDuration, totemRegenerationAmplifier)
	assertEffect(t, p, effectAbsorption, totemAbsorptionDuration, totemAbsorptionAmplifier)
	assertEffect(t, p, effectFireResistance, totemFireResistanceDuration, totemFireResistanceAmplifier)
}

// assertEffect fails unless the player carries id at the given duration + amplifier.
func assertEffect(t *testing.T, p *tickPlayer, id string, dur, amp int) {
	t.Helper()
	e, ok := p.activeEffects[id]
	if !ok {
		t.Fatalf("effect %s not applied", id)
	}
	if e.duration != dur || e.amplifier != amp {
		t.Fatalf("effect %s = dur %d amp %d, want dur %d amp %d", id, e.duration, e.amplifier, dur, amp)
	}
}

// TestTotemOffHandAlsoWorks: a totem in the OFFHAND (not the main hand) still triggers the revive
// (the hand scan is MAINHAND then OFFHAND).
func TestTotemOffHandAlsoWorks(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	inv := ensureInventory(p)
	inv.set(offhandWindowSlot, totemStack())

	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 999)

	if p.dead || p.health != 1.0 {
		t.Fatalf("offhand totem did not revive: dead=%v health=%v", p.dead, p.health)
	}
	if got := playerItemBySlot(p, eqSlotOffHand); got.Count != 0 {
		t.Fatalf("offhand totem not consumed: count = %d", got.Count)
	}
}

// TestTotemPopEvent: the totem revive broadcasts the entity event 35 to the player own client.
func TestTotemPopEvent(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	giveTotemMainHand(p)

	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 999)

	pkts := drainPackets(p.client)
	found := false
	for _, pkt := range pkts {
		if pkt.ID != int32(packetid.ClientboundEntityEvent) {
			continue
		}
		var id pk.Int
		var status pk.Byte
		if err := pkt.Scan(&id, &status); err != nil {
			continue
		}
		if int32(id) == p.entityID && int8(status) == int8(entityEventTotemOfUndying) {
			found = true
		}
	}
	if !found {
		t.Fatalf("totem-pop entity event (35) not broadcast to the player")
	}
}

// TestTotemNoTotemDiesNormally: without a totem the player dies normally on a lethal hit (the gated
// no-totem path is identical to the old death path).
func TestTotemNoTotemDiesNormally(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 999)

	if !p.dead {
		t.Fatalf("player with NO totem did not die on a lethal hit")
	}
	if len(p.activeEffects) != 0 {
		t.Fatalf("no-totem death applied effects: %d, want 0", len(p.activeEffects))
	}
}

// TestTotemBypassesInvulnerabilityIgnored: a BYPASSES_INVULNERABILITY source (the void) kills even a
// totem holder (checkTotemDeathProtection early-returns false, so the totem is neither consumed nor
// does it revive).
func TestTotemBypassesInvulnerabilityIgnored(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	giveTotemMainHand(p)

	outOfWorld := damageTypeID(tag.DamageTypeIDs["minecraft:out_of_world"])
	loop.applyDamage(p, damageSourceOf(outOfWorld), 999)

	if !p.dead {
		t.Fatalf("out_of_world (bypasses_invulnerability) did not kill a totem holder")
	}
	// The totem revive did NOT fire: no revive effects and the player is dead (the inventory clears on a
	// normal death, so the totem stack is dropped, NOT consumed-by-totem -- the effects are the tell).
	if len(p.activeEffects) != 0 {
		t.Fatalf("bypass source triggered the totem revive: %d effects, want 0", len(p.activeEffects))
	}
}

// TestTotemMobSurvivesLethal: a mob holding a totem survives a lethal hit at 1 HP, the totem is
// consumed, and the death is cancelled (the entity is NOT removed / not dead).
func TestTotemMobSurvivesLethal(t *testing.T) {
	loop, _ := newBlockLoop()
	e := NewEntity(1, entity.Pig, 8.5, 64, 8.5)
	e.health = 10.0
	e.setItemSlot(eqSlotMainHand, totemStack())
	loop.only().entities.add(e)

	loop.applyDamageEntity(e, damageSourcePlayerAttack(0), 999)

	if e.dead {
		t.Fatalf("mob with a totem died on a lethal hit (want survive)")
	}
	if e.health != 1.0 {
		t.Fatalf("mob totem revive health = %v, want 1.0", e.health)
	}
	if got := e.getItemBySlot(eqSlotMainHand); got.Count != 0 {
		t.Fatalf("mob totem not consumed: count = %d, want 0", got.Count)
	}
	if !entityHasEffect(e, effectRegeneration) {
		t.Fatalf("mob totem did not apply regeneration")
	}
}

// TestTotemMobNoTotemDies: a mob without a totem dies normally on a lethal hit.
func TestTotemMobNoTotemDies(t *testing.T) {
	loop, _ := newBlockLoop()
	e := NewEntity(1, entity.Pig, 8.5, 64, 8.5)
	e.health = 10.0
	loop.only().entities.add(e)

	loop.applyDamageEntity(e, damageSourcePlayerAttack(0), 999)

	if !e.dead {
		t.Fatalf("mob with NO totem did not die on a lethal hit")
	}
}
