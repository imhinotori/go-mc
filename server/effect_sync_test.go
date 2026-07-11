package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestUpdateMobEffectWireOnAdd asserts adding a duration effect to a player emits exactly one
// ClientboundUpdateMobEffectPacket to that player, with the jar wire shape (VarInt entityId, VarInt
// effect holder id, VarInt amplifier, VarInt duration, byte flags) and blend=true on a fresh add.
func TestUpdateMobEffectWireOnAdd(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1000)

	// SPEED I for 200 ticks — a MOVEMENT_SPEED modifier-bearing effect (also exercises the attribute flush).
	loop.addPlayerEffect(p, 0, effectSpeed, 200, 0, 1.0)

	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundUpdateMobEffect); n != 1 {
		t.Fatalf("UpdateMobEffect count = %d, want 1", n)
	}
	for _, pkt := range pkts {
		if pkt.ID != int32(packetid.ClientboundUpdateMobEffect) {
			continue
		}
		r := bytes.NewReader(pkt.Data)
		var eid, effID, amp, dur pk.VarInt
		var flags pk.Byte
		for _, f := range []pk.FieldDecoder{&eid, &effID, &amp, &dur, &flags} {
			if _, err := f.ReadFrom(r); err != nil {
				t.Fatalf("decode UpdateMobEffect: %v", err)
			}
		}
		if int32(eid) != p.entityID {
			t.Fatalf("entityId = %d, want %d", eid, p.entityID)
		}
		if want := mobEffectHolderID(effectSpeed); int32(effID) != want {
			t.Fatalf("effect holder id = %d, want %d (%q)", effID, want, registryid.MobEffect[effID])
		}
		if amp != 0 {
			t.Fatalf("amplifier = %d, want 0", amp)
		}
		if dur != 200 {
			t.Fatalf("duration = %d, want 200", dur)
		}
		// A fresh add: FLAG_VISIBLE|FLAG_SHOW_ICON|FLAG_BLEND (0x2|0x4|0x8 = 0xE), not ambient.
		if byte(flags) != (mobEffectFlagVisible | mobEffectFlagShowIcon | mobEffectFlagBlend) {
			t.Fatalf("flags = 0x%02x, want 0x%02x (visible|showIcon|blend)", byte(flags),
				mobEffectFlagVisible|mobEffectFlagShowIcon|mobEffectFlagBlend)
		}
	}
}

// TestRemoveMobEffectWireOnExpiry asserts an effect expiring emits ClientboundRemoveMobEffectPacket
// (VarInt entityId, VarInt effect holder id) to the affected player.
func TestRemoveMobEffectWireOnExpiry(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1000)

	// duration 1 so the next effect tick expires it.
	loop.addPlayerEffect(p, 0, effectSpeed, 1, 0, 1.0)
	loop.tickPlayerEffects(p)
	if playerHasEffect(p, effectSpeed) {
		t.Fatalf("SPEED should have expired at duration 1")
	}

	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundRemoveMobEffect); n != 1 {
		t.Fatalf("RemoveMobEffect count = %d, want 1", n)
	}
	for _, pkt := range pkts {
		if pkt.ID != int32(packetid.ClientboundRemoveMobEffect) {
			continue
		}
		r := bytes.NewReader(pkt.Data)
		var eid, effID pk.VarInt
		if _, err := eid.ReadFrom(r); err != nil {
			t.Fatalf("decode entityId: %v", err)
		}
		if _, err := effID.ReadFrom(r); err != nil {
			t.Fatalf("decode effect: %v", err)
		}
		if int32(eid) != p.entityID {
			t.Fatalf("entityId = %d, want %d", eid, p.entityID)
		}
		if want := mobEffectHolderID(effectSpeed); int32(effID) != want {
			t.Fatalf("effect holder id = %d, want %d", effID, want)
		}
	}
}

// TestUpdateAttributesWireOnModifier asserts that attaching a MOVEMENT_SPEED modifier (via SPEED) and
// flushing emits ClientboundUpdateAttributesPacket with the jar shape: VarInt entityId, list(VarInt
// count) of { VarInt attr holder id, Double base, collection(VarInt count) of { Identifier, Double,
// VarInt operation } }.
func TestUpdateAttributesWireOnModifier(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1000)

	loop.addPlayerEffect(p, 0, effectSpeed, 200, 0, 1.0)
	loop.flushPlayerAttributes(p)

	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundUpdateAttributes); n != 1 {
		t.Fatalf("UpdateAttributes count = %d, want 1", n)
	}
	for _, pkt := range pkts {
		if pkt.ID != int32(packetid.ClientboundUpdateAttributes) {
			continue
		}
		r := bytes.NewReader(pkt.Data)
		var eid, count pk.VarInt
		if _, err := eid.ReadFrom(r); err != nil {
			t.Fatalf("decode entityId: %v", err)
		}
		if int32(eid) != p.entityID {
			t.Fatalf("entityId = %d, want %d", eid, p.entityID)
		}
		if _, err := count.ReadFrom(r); err != nil {
			t.Fatalf("decode list count: %v", err)
		}
		if count != 1 {
			t.Fatalf("attribute snapshot count = %d, want 1", count)
		}
		var holderID pk.VarInt
		var base pk.Double
		var modCount pk.VarInt
		if _, err := holderID.ReadFrom(r); err != nil {
			t.Fatalf("decode holder id: %v", err)
		}
		if want := attributeHolderID("minecraft:movement_speed"); int32(holderID) != want {
			t.Fatalf("attr holder id = %d, want %d (movement_speed)", holderID, want)
		}
		if _, err := base.ReadFrom(r); err != nil {
			t.Fatalf("decode base: %v", err)
		}
		if float64(base) != playerAttributeBase[attrMovementSpeed] {
			t.Fatalf("base = %v, want %v", base, playerAttributeBase[attrMovementSpeed])
		}
		if _, err := modCount.ReadFrom(r); err != nil {
			t.Fatalf("decode modifier count: %v", err)
		}
		if modCount != 1 {
			t.Fatalf("modifier count = %d, want 1 (effect.speed)", modCount)
		}
		var id pk.Identifier
		var amount pk.Double
		var op pk.VarInt
		if _, err := id.ReadFrom(r); err != nil {
			t.Fatalf("decode modifier id: %v", err)
		}
		if string(id) != speedModifierID {
			t.Fatalf("modifier id = %q, want %q", string(id), speedModifierID)
		}
		if _, err := amount.ReadFrom(r); err != nil {
			t.Fatalf("decode amount: %v", err)
		}
		if float64(amount) != effectSpeedAmount {
			t.Fatalf("amount = %v, want %v", amount, effectSpeedAmount)
		}
		if _, err := op.ReadFrom(r); err != nil {
			t.Fatalf("decode operation: %v", err)
		}
		if attribute.Operation(op) != attribute.AddMultipliedTotal {
			t.Fatalf("operation = %d, want AddMultipliedTotal (2)", op)
		}
	}
}

// TestAttributeFlushOnlyOnChange asserts a second flush with no modifier change emits nothing (the
// dirty set drained on the first flush) — the faithful once-per-change behavior.
func TestAttributeFlushOnlyOnChange(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1000)

	loop.addPlayerEffect(p, 0, effectSpeed, 200, 0, 1.0)
	loop.flushPlayerAttributes(p)
	_ = drainPackets(p.client) // consume the first flush

	// Fresh capturing client, flush again with no change: expect ZERO UpdateAttributes.
	p.client = captureClient(64)
	loop.clientIndex[p.client] = p
	loop.flushPlayerAttributes(p)
	if n := countID(drainPackets(p.client), packetid.ClientboundUpdateAttributes); n != 0 {
		t.Fatalf("second flush emitted %d UpdateAttributes, want 0 (nothing dirty)", n)
	}
}
