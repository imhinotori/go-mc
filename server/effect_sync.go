package server

// effect_sync.go — client-facing emission of the three mob-effect / attribute packets, ported 1:1
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session).
//
// ClientboundUpdateMobEffectPacket: VarInt entityId, Holder<MobEffect> effect (MOB_EFFECT holder id
// VarInt via MobEffect.STREAM_CODEC = ByteBufCodecs.holderRegistry(MOB_EFFECT)), VarInt amplifier,
// VarInt duration, byte flags (FLAG_AMBIENT 0x1, FLAG_VISIBLE 0x2, FLAG_SHOW_ICON 0x4, FLAG_BLEND 0x8
// — the ctor ior order). [VERIFIED javap ctor + write; MobEffect.STREAM_CODEC static{}.]
//
// ClientboundRemoveMobEffectPacket (record): VarInt entityId, Holder<MobEffect> effect (same holder-id
// VarInt). [VERIFIED javap RemoveMobEffect STREAM_CODEC static{}.]
//
// ClientboundUpdateAttributesPacket: VarInt entityId, list(128) of AttributeSnapshot { Holder<Attribute>
// id (ATTRIBUTE holder VarInt via Attribute.STREAM_CODEC = holderRegistry(ATTRIBUTE)), Double base,
// collection of modifier { Identifier id (String), Double amount, Operation (VarInt idMapper 0/1/2) } }.
// [VERIFIED javap UpdateAttributes static{} + AttributeSnapshot static{}; Attribute/Operation STREAM_CODEC.]
//
// SEND SITES: ServerPlayer.onEffectAdded -> UpdateMobEffect(id, inst, blend=true); onEffectUpdated ->
// UpdateMobEffect(id, inst, blend=false); onEffectsRemoved -> per-effect RemoveMobEffect(id, effect).
// LivingEntity.onEffectAdded -> sendEffectToPassengers (each ServerPlayer passenger, blend=false); a mob
// does NOT broadcast effects to general trackers. ServerEntity.sendChanges -> UpdateAttributes over
// getAttributesToSync (per-tick dirty flush) to self/trackers. [VERIFIED javap each site.]
//
// DIRTY-TRACKING: a modifier add/remove that changes a value marks the attribute dirty; the tick loop
// flushes the dirty set to ONE UpdateAttributes per living entity. An entity with no effects and default
// attributes marks nothing dirty and emits ZERO packets — byte-identical to before (the pig oracle).

import (
	"bytes"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// ClientboundUpdateMobEffectPacket flag bits (ctor ior constants). [VERIFIED javap.]
const (
	mobEffectFlagAmbient  byte = 0x1
	mobEffectFlagVisible  byte = 0x2
	mobEffectFlagShowIcon byte = 0x4
	mobEffectFlagBlend    byte = 0x8
)

// mobEffectHolderID maps an effect id to its MOB_EFFECT holder id (the VarInt holderRegistry writes).
// -1 for unknown; callers gate on >= 0 so an unmodeled effect never emits a malformed id.
func mobEffectHolderID(id string) int32 {
	for i, name := range registryid.MobEffect {
		if name == id {
			return int32(i)
		}
	}
	return -1
}

// attributeHolderID maps an attribute registry name to its ATTRIBUTE holder id. -1 for unknown.
func attributeHolderID(name string) int32 {
	for i, n := range registryid.Attribute {
		if n == name {
			return int32(i)
		}
	}
	return -1
}

// effectFlagsByte builds the flags byte in ctor order (ambient, visible, showIcon, blend).
func effectFlagsByte(e *activeEffect, blend bool) byte {
	var flags byte
	if e.ambient {
		flags |= mobEffectFlagAmbient
	}
	if e.visible {
		flags |= mobEffectFlagVisible
	}
	if e.showIcon {
		flags |= mobEffectFlagShowIcon
	}
	if blend {
		flags |= mobEffectFlagBlend
	}
	return flags
}

// encodeUpdateMobEffect builds ClientboundUpdateMobEffectPacket. Returns (zero, false) for an
// unregistered effect id. duration -1 (infinite) writes as the VarInt -1 (MobEffectInstance.getDuration).
func encodeUpdateMobEffect(entityID int32, e *activeEffect, blend bool) (pk.Packet, bool) {
	hid := mobEffectHolderID(e.id)
	if hid < 0 {
		return pk.Packet{}, false
	}
	return pk.Marshal(
		int32(packetid.ClientboundUpdateMobEffect),
		pk.VarInt(entityID),
		pk.VarInt(hid),
		pk.VarInt(e.amplifier),
		pk.VarInt(e.duration),
		pk.Byte(effectFlagsByte(e, blend)),
	), true
}

// encodeRemoveMobEffect builds ClientboundRemoveMobEffectPacket. (zero, false) for unknown effect.
func encodeRemoveMobEffect(entityID int32, effectID string) (pk.Packet, bool) {
	hid := mobEffectHolderID(effectID)
	if hid < 0 {
		return pk.Packet{}, false
	}
	return pk.Marshal(
		int32(packetid.ClientboundRemoveMobEffect),
		pk.VarInt(entityID),
		pk.VarInt(hid),
	), true
}

// attrSnapshot is one AttributeSnapshot: the attribute registry name, its BASE value, and the active
// modifiers. Mirrors ClientboundUpdateAttributesPacket$AttributeSnapshot(Holder, double, Collection).
type attrSnapshot struct {
	name      string
	baseValue float64
	modifiers []attribute.AttributeModifier
}

// encodeUpdateAttributes builds ClientboundUpdateAttributesPacket. Snapshots whose attribute is not in
// the ATTRIBUTE registry are skipped. Returns (zero, false) when none survive (never send an empty list).
func encodeUpdateAttributes(entityID int32, snapshots []attrSnapshot) (pk.Packet, bool) {
	type wireSnap struct {
		holderID int32
		snap     attrSnapshot
	}
	wire := make([]wireSnap, 0, len(snapshots))
	for _, s := range snapshots {
		hid := attributeHolderID(s.name)
		if hid < 0 {
			continue
		}
		wire = append(wire, wireSnap{holderID: hid, snap: s})
	}
	if len(wire) == 0 {
		return pk.Packet{}, false
	}
	var body bytes.Buffer
	// list(128): VarInt count then each AttributeSnapshot.
	_, _ = pk.VarInt(len(wire)).WriteTo(&body)
	for _, w := range wire {
		_, _ = pk.VarInt(w.holderID).WriteTo(&body)       // Holder<Attribute> id
		_, _ = pk.Double(w.snap.baseValue).WriteTo(&body) // Double base value
		// modifier collection: VarInt count then each modifier.
		_, _ = pk.VarInt(len(w.snap.modifiers)).WriteTo(&body)
		for _, m := range w.snap.modifiers {
			_, _ = pk.Identifier(m.ID).WriteTo(&body)         // Identifier (String)
			_, _ = pk.Double(m.Amount).WriteTo(&body)         // Double amount
			_, _ = pk.VarInt(int(m.Operation)).WriteTo(&body) // Operation (VarInt idMapper)
		}
	}
	return pk.Marshal(
		int32(packetid.ClientboundUpdateAttributes),
		pk.VarInt(entityID),
		rawBytes(body.Bytes()),
	), true
}

// ---- PLAYER (self) send sites: ServerPlayer.onEffectAdded / onEffectUpdated / onEffectsRemoved ----

// sendPlayerEffectAdded is ServerPlayer.onEffectAdded -> connection.send(UpdateMobEffect(id, inst, true)).
// Also forwards to ServerPlayer passengers riding this player (base LivingEntity.sendEffectToPassengers).
func (t *TickLoop) sendPlayerEffectAdded(p *tickPlayer, e *activeEffect) {
	if p == nil || p.client == nil {
		return
	}
	if pkt, ok := encodeUpdateMobEffect(p.entityID, e, true); ok {
		p.client.Send(pkt)
	}
	t.sendEffectToPlayerPassengers(p.entityID, e)
}

// sendPlayerEffectUpdated is ServerPlayer.onEffectUpdated -> UpdateMobEffect(id, inst, false).
func (t *TickLoop) sendPlayerEffectUpdated(p *tickPlayer, e *activeEffect) {
	if p == nil || p.client == nil {
		return
	}
	if pkt, ok := encodeUpdateMobEffect(p.entityID, e, false); ok {
		p.client.Send(pkt)
	}
	t.sendEffectToPlayerPassengers(p.entityID, e)
}

// sendPlayerEffectRemoved is ServerPlayer.onEffectsRemoved -> per-effect RemoveMobEffect(id, effect).
func (t *TickLoop) sendPlayerEffectRemoved(p *tickPlayer, effectID string) {
	if p == nil || p.client == nil {
		return
	}
	if pkt, ok := encodeRemoveMobEffect(p.entityID, effectID); ok {
		p.client.Send(pkt)
	}
}

// ---- ENTITY (non-player LivingEntity) send sites: sendEffectToPassengers only ----

// sendEntityEffect is LivingEntity.onEffectAdded/onEffectUpdated -> sendEffectToPassengers(inst). For a
// mob with no player passengers (incl. the pig oracle) this emits NOTHING.
func (t *TickLoop) sendEntityEffect(e *Entity, ef *activeEffect) {
	if e == nil {
		return
	}
	t.sendEffectToEntityPassengers(e, ef)
}

// sendEntityEffectRemoved: the base LivingEntity does NOT push RemoveMobEffect to passengers (only
// ServerPlayer.onEffectsRemoved sends, and that is the self path). Intentional no-op seam.
func (t *TickLoop) sendEntityEffectRemoved(_ *Entity, _ string) {}

// sendEffectToPlayerPassengers pushes UpdateMobEffect(vehicleId, effect, blend=false) to riders.
func (t *TickLoop) sendEffectToPlayerPassengers(vehicleID int32, e *activeEffect) {
	for _, rider := range t.players {
		if rider == nil || rider.client == nil || rider.vehicleID != vehicleID {
			continue
		}
		if pkt, ok := encodeUpdateMobEffect(vehicleID, e, false); ok {
			rider.client.Send(pkt)
		}
	}
}

// sendEffectToEntityPassengers pushes UpdateMobEffect(mobId, effect, blend=false) to ServerPlayer riders.
func (t *TickLoop) sendEffectToEntityPassengers(e *Entity, ef *activeEffect) {
	if len(e.passengers) == 0 {
		return
	}
	for _, pid := range e.passengers {
		rider := t.playerByEntityID(pid)
		if rider == nil || rider.client == nil {
			continue
		}
		if pkt, ok := encodeUpdateMobEffect(e.id, ef, false); ok {
			rider.client.Send(pkt)
		}
	}
}

// ---- ATTRIBUTE dirty flush: ServerEntity.sendChanges over getAttributesToSync (once per tick) ----

// flushPlayerAttributes drains a player's dirty attribute set into ONE UpdateAttributes to self (+ any
// riders). No dirty attributes -> emits nothing (getAttributesToSync empty case).
func (t *TickLoop) flushPlayerAttributes(p *tickPlayer) {
	if p == nil || p.attributes == nil || len(p.attributes.dirty) == 0 {
		return
	}
	snaps := p.attributes.drainDirtySnapshots()
	if len(snaps) == 0 {
		return
	}
	if p.client != nil {
		if pkt, ok := encodeUpdateAttributes(p.entityID, snaps); ok {
			p.client.Send(pkt)
		}
	}
	for _, rider := range t.players {
		if rider == nil || rider.client == nil || rider.vehicleID != p.entityID {
			continue
		}
		if pkt, ok := encodeUpdateAttributes(p.entityID, snaps); ok {
			rider.client.Send(pkt)
		}
	}
}

// flushEntityAttributes drains a mob's dirty attribute set into ONE UpdateAttributes broadcast to every
// player tracking the mob (attributes ARE broadcast to trackers, unlike effects). No dirty -> nothing.
func (t *TickLoop) flushEntityAttributes(e *Entity) {
	if e == nil || len(e.attrDirty) == 0 {
		return
	}
	snaps := drainEntityAttrDirty(e)
	if len(snaps) == 0 {
		return
	}
	if pkt, ok := encodeUpdateAttributes(e.id, snaps); ok {
		t.broadcastToTrackers(e.id, pkt)
	}
}
