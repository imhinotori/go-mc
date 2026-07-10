package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// mob_effect_behavior_test.go pins EFFECT-BEHAVIOR-01: the self-contained movement/utility effects
// that have REAL server-side behavior in v1 — ABSORPTION (add fills hearts / remove clamps them back /
// a hit is absorbed), HUNGER (per-tick exhaustion accrues), and INVISIBILITY (the shared invisible
// metadata flag is set on add and cleared on remove, broadcast to trackers). Every assertion is
// backed by the jar bytecode cited in mob_effect.go. These effects are all gated on being PRESENT in
// activeEffects, so a no-effect entity (the pig oracle) is untouched — see the pig-oracle regression.

// TestAbsorptionAddFillsHearts: adding ABSORPTION at amplifier amp sets getAbsorptionAmount() to
// 4*(amp+1). VERIFIED AbsorptionMobEffect.onEffectStarted: setAbsorptionAmount(max(cur, 4*(1+amp))),
// with the MAX_ABSORPTION +4*(amp+1) modifier raising the clamp ceiling first.
func TestAbsorptionAddFillsHearts(t *testing.T) {
	for _, tc := range []struct {
		amp  int
		want float32
	}{{0, 4}, {1, 8}, {3, 16}} {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 1)
		loop.addPlayerEffect(p, 0, effectAbsorption, 200, tc.amp, 1.0)
		if got := p.getAbsorptionAmount(); got != tc.want {
			t.Fatalf("absorption amp %d: getAbsorptionAmount() = %v, want %v (4*(amp+1))", tc.amp, got, tc.want)
		}
	}
}

// TestAbsorptionRemoveClampsToZero: when ABSORPTION expires, the MAX_ABSORPTION modifier is detached
// and setAbsorptionAmount(getAbsorptionAmount()) re-clamps the leftover hearts to 0 (MAX_ABSORPTION
// base is 0). VERIFIED LivingEntity.onEffectsRemoved -> removeAttributeModifiers + setAbsorptionAmount
// clamp to [0, getMaxAbsorption()].
func TestAbsorptionRemoveClampsToZero(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	// duration 1 so the very next tick expires it (e.duration-- -> 0 -> delete + removeEffectModifiers).
	loop.addPlayerEffect(p, 0, effectAbsorption, 1, 0, 1.0)
	if p.getAbsorptionAmount() != 4 {
		t.Fatalf("pre-expiry absorption = %v, want 4", p.getAbsorptionAmount())
	}
	loop.tickPlayerEffects(p)
	if playerHasEffect(p, effectAbsorption) {
		t.Fatalf("absorption should have expired after one tick at duration 1")
	}
	if got := p.getAbsorptionAmount(); got != 0 {
		t.Fatalf("post-expiry absorption = %v, want 0 (clamped by the removed MAX_ABSORPTION ceiling)", got)
	}
}

// TestAbsorptionAbsorbsHit: a hit against a player with ABSORPTION hearts is folded into the
// absorption shield first (actuallyHurt: amount = max(amount - absorption, 0)); health is untouched
// while the shield covers the hit, and the shield drains by the hit amount. VERIFIED
// LivingEntity.setAbsorptionAmount fold in the combat.go actuallyHurt port.
func TestAbsorptionAbsorbsHit(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	loop.addPlayerEffect(p, 0, effectAbsorption, 200, 0, 1.0) // 4 absorption hearts
	if p.getAbsorptionAmount() != 4 {
		t.Fatalf("setup absorption = %v, want 4", p.getAbsorptionAmount())
	}
	// A 3.0 magic hit is fully covered by the 4.0 shield: health stays full, shield drops to 1.0.
	loop.applyDamage(p, damageSourceMagic(), 3.0)
	if p.health != maxHealth {
		t.Fatalf("health = %v after an absorbed hit, want %v (fully shielded)", p.health, maxHealth)
	}
	if got := p.getAbsorptionAmount(); got != 1 {
		t.Fatalf("absorption after a 3.0 hit = %v, want 1 (4 - 3)", got)
	}
}

// TestWitherDoTHurtsEvery40Ticks: WITHER deals 1.0 wither damage on the WitherMobEffect
// shouldApplyEffectTickThisTick cadence (40>>amp ticks), and -- unlike poison -- it CAN kill (no
// health>1.0 floor). VERIFIED WitherMobEffect.applyEffectTick (hurtServer(wither(), 1.0F)) +
// shouldApplyEffectTickThisTick (40>>amp).
func TestWitherDoTHurtsEvery40Ticks(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.health = maxHealth
	// A long-duration amp-0 wither: the DoT fires when remaining % 40 == 0. Drive the effect ticker and
	// count the health lost across a window covering at least one 40-tick boundary.
	loop.addPlayerEffect(p, 0, effectWither, 200, 0, 1.0)
	start := p.health
	for i := 0; i < 41; i++ {
		p.invulnerableTime = 0 // clear the i-frame grace so each scheduled DoT hit lands
		loop.tickPlayerEffects(p)
	}
	if p.health >= start {
		t.Fatalf("WITHER dealt no damage over 41 ticks (health %v -> %v); DoT not wired", start, p.health)
	}

	// WITHER can KILL (no health>1.0 floor, unlike poison). A near-dead player afflicted by wither dies.
	p2 := combatPlayer(loop, 2)
	p2.health = 1.0
	loop.addPlayerEffect(p2, 0, effectWither, 200, 3, 1.0) // amp 3 -> 40>>3 == 5-tick cadence
	for i := 0; i < 60 && p2.health > 0 && !p2.dead; i++ {
		p2.invulnerableTime = 0
		loop.tickPlayerEffects(p2)
	}
	if p2.health > 0 && !p2.dead {
		t.Fatalf("WITHER did not kill a 1.0-health player (health %v); it must have no poison-style floor", p2.health)
	}
}

// TestHungerTickAccruesExhaustion: each HUNGER tick calls causeFoodExhaustion(0.005*(amp+1)), routed
// into FoodData.exhaustion. VERIFIED HungerMobEffect.applyEffectTick + Player.causeFoodExhaustion.
func TestHungerTickAccruesExhaustion(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.exhaustion = 0
	loop.addPlayerEffect(p, 0, effectHunger, 200, 0, 1.0) // amp 0 -> 0.005/tick
	loop.tickPlayerEffects(p)
	if got := p.exhaustion; got != 0.005 {
		t.Fatalf("exhaustion after one HUNGER tick (amp 0) = %v, want 0.005", got)
	}
	// A second tick accrues another 0.005.
	loop.tickPlayerEffects(p)
	if got := p.exhaustion; got != 0.010 {
		t.Fatalf("exhaustion after two HUNGER ticks = %v, want 0.010", got)
	}
}

// TestHungerAmplifierScalesExhaustion: HUNGER II (amp 1) accrues 0.005*2 == 0.010 per tick.
func TestHungerAmplifierScalesExhaustion(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.exhaustion = 0
	loop.addPlayerEffect(p, 0, effectHunger, 200, 1, 1.0) // amp 1 -> 0.005*2 = 0.010/tick
	loop.tickPlayerEffects(p)
	if got := p.exhaustion; got != 0.010 {
		t.Fatalf("exhaustion after one HUNGER-II tick = %v, want 0.010 (0.005*(1+1))", got)
	}
}

// sharedFlagsFromSetData scans a ClientboundSetEntityData packet and returns the DATA_SHARED_FLAGS
// byte (index 0, serializer 0) if present. Mirrors setEntityDataFlags but for the shared-flags index.
func sharedFlagsFromSetData(t *testing.T, p pk.Packet) (int8, bool) {
	t.Helper()
	var id pk.VarInt
	r := bytes.NewReader(p.Data)
	if _, err := id.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEntityData id: %v", err)
	}
	for {
		var index pk.UnsignedByte
		if _, err := index.ReadFrom(r); err != nil {
			return 0, false
		}
		if index == 0xFF {
			return 0, false
		}
		var ser pk.VarInt
		if _, err := ser.ReadFrom(r); err != nil {
			return 0, false
		}
		var b pk.Byte
		if _, err := b.ReadFrom(r); err != nil {
			return 0, false
		}
		if uint8(index) == dataSharedFlagsIndex && int32(ser) == byteSerializerID {
			return int8(b), true
		}
	}
}

// TestInvisibilityFlagBroadcast: adding INVISIBILITY broadcasts DATA_SHARED_FLAGS with the invisible
// bit (0x20) to a tracking observer (not the invisible player itself); removing it re-broadcasts the
// cleared byte. VERIFIED LivingEntity.updateInvisibilityStatus -> setInvisible(hasEffect(INVISIBILITY))
// + Entity.setInvisible (setSharedFlag(5, value)). The observer is drained ONCE at the end (drainPackets
// closes the queue), so the add-then-remove is a single captured stream: first entry has the invisible
// bit set, the last has it cleared.
func TestInvisibilityFlagBroadcast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	actor := combatPlayer(loop, 1000)
	observer := newTrackerPlayer(loop, 1001, 9.5, 8.5)
	observer.tracked = map[int32]bool{actor.entityID: true}

	// Add INVISIBILITY (duration 1 so the next effect tick expires it), then expire it — WITHOUT draining
	// in between (drainPackets closes the outbound queue, dropping later sends).
	loop.addPlayerEffect(actor, 0, effectInvisibility, 1, 0, 1.0)
	loop.tickPlayerEffects(actor)
	if playerHasEffect(actor, effectInvisibility) {
		t.Fatalf("INVISIBILITY should have expired after one tick at duration 1")
	}

	// Collect every shared-flags byte the observer saw, in order.
	var flags []int8
	for _, p := range drainPackets(observer.client) {
		if p.ID != int32(packetid.ClientboundSetEntityData) {
			continue
		}
		if f, ok := sharedFlagsFromSetData(t, p); ok {
			flags = append(flags, f)
		}
	}
	if len(flags) < 2 {
		t.Fatalf("observer saw %d shared-flags broadcasts, want >= 2 (add sets the bit, remove clears it): %v", len(flags), flags)
	}
	if flags[0]&invisibleSharedFlagBit == 0 {
		t.Fatalf("add broadcast shared-flags byte = 0x%02x, want the invisible bit 0x20 set", uint8(flags[0]))
	}
	if last := flags[len(flags)-1]; last&invisibleSharedFlagBit != 0 {
		t.Fatalf("post-expiry shared-flags byte = 0x%02x, want the invisible bit 0x20 cleared", uint8(last))
	}

	// The invisible player must NOT receive its own metadata (broadcastToTrackers excludes the actor).
	if n := countID(drainPackets(actor.client), packetid.ClientboundSetEntityData); n != 0 {
		t.Fatalf("the invisible player received its own SetEntityData (%d), want 0", n)
	}
}
