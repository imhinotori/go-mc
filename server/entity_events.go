package server

import (
	"bytes"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// entity_events.go ports the vanilla "event packets" a player's action broadcasts to the
// OTHER players that can see it (GAMEPLAY-07, the real-client visual gate): arm swings
// (ClientboundAnimate) and held-item/equipment changes (ClientboundSetEquipment). Vanilla
// routes these through ServerChunkCache.sendToTrackingPlayers — every player whose entity
// tracker holds the acting entity gets the packet; the actor does NOT (the client predicts
// its own swing/equipment locally). Sulfur's tracker records exactly that visibility in
// tickPlayer.tracked, so broadcastToTrackers replays the same "to everyone tracking X" fan-out.
//
// All on the tick goroutine over tick-owned state (TICK-05): the handlers run inside
// applyInput (subtick.go), iterate t.players, and emit via p.client.Send (the bounded
// outbound queue) — no goroutine, no new synchronization.

// broadcastToTrackers sends pkt to every player (other than the actor) whose entity tracker
// currently holds actorEntityID — i.e. every player that can SEE the actor. This is the
// Sulfur analogue of ServerChunkCache.sendToTrackingPlayers(entity, packet): a tracked id in
// p.tracked means the player has been sent that entity's AddEntity and is rendering it, so it
// must also receive the entity's event packets. The actor is skipped (vanilla's swing/equipment
// broadcast excludes self; the client predicts its own animation).
func (t *TickLoop) broadcastToTrackers(actorEntityID int32, pkt pk.Packet) {
	for _, other := range t.players {
		if other == nil || other.client == nil {
			continue
		}
		if other.entityID == actorEntityID {
			continue // never echo the actor's own swing/equipment to itself
		}
		if other.tracked == nil || !other.tracked[actorEntityID] {
			continue // the other player cannot see the actor: nothing to send
		}
		other.client.Send(pkt)
	}
}

// handleSwing resolves ServerboundSwing (jar: ServerGamePacketListenerImpl.handleAnimate ->
// ServerPlayer.swing(hand)). The wire payload is the InteractionHand enum (a VarInt ordinal:
// 0 = MAIN_HAND, 1 = OFF_HAND — FriendlyByteBuf.readEnum). Vanilla maps the hand to the
// ClientboundAnimate action (MAIN_HAND -> 0, OFF_HAND -> 3) and broadcasts to tracking players
// (NOT self). A malformed/short payload is a silent no-op (T-6-02).
//
//	[VERIFIED javap: ServerboundSwingPacket reads readEnum(InteractionHand); handleAnimate ->
//	 player.swing(hand); LivingEntity.swing(hand,false) -> ClientboundAnimatePacket action
//	 (MAIN_HAND?0:3) -> sendToTrackingPlayers.]
func (t *TickLoop) handleSwing(p *tickPlayer, pkt pk.Packet) {
	r := bytes.NewReader(pkt.Data)
	var hand pk.VarInt
	if _, err := hand.ReadFrom(r); err != nil {
		return // malformed: no broadcast
	}
	// InteractionHand: 0 = MAIN_HAND, 1 = OFF_HAND. Any other ordinal is treated as main hand
	// (defensive — vanilla's readEnum would reject it; here we just default to the common case).
	action := animateActionMainHandSwing
	if hand == 1 {
		action = animateActionOffHandSwing
	}
	udebug("combat", "swing player=%d hand=%d action=%d", p.entityID, int(hand), action)
	t.broadcastToTrackers(p.entityID, encodeAnimate(p.entityID, action))
}

// playerMainHand returns the player's currently-held (MAINHAND) item: inventory slot
// (windowHotbarFirst + heldSlot). An empty/uninitialized inventory yields an empty SlotData.
// Mirrors Inventory.getSelected() == items.get(selected).
func playerMainHand(p *tickPlayer) component.SlotData {
	if p.inventory == nil {
		return component.SlotData{Count: 0}
	}
	return p.inventory.get(int16(windowHotbarFirst) + p.inventory.heldSlot)
}

// tickEquipment ports LivingEntity.detectEquipmentUpdates for the v1 equipment surface: each
// tick, compare the player's current MAINHAND item against lastMainHand; on a change, broadcast
// ClientboundSetEquipment to the players tracking this player so observers see the held item.
// This is what makes a held-item change (hotbar reselect via SetCarriedItem, or an inventory
// click that swaps the selected slot) VISIBLE in 3rd person — vanilla detects it per-tick in
// LivingEntity.tick, not in the SetCarriedItem handler, so an inventory click is covered too.
//
// Armor slots are NOT synced (no armor inventory exists yet — the cited v1 default is empty
// equipment for the other 7 EquipmentSlots; when an armor inventory lands, this extends to a
// per-slot compare exactly like vanilla's lastEquipmentItems map). The first call seeds
// lastMainHand without broadcasting (equipInit) so a join with an empty hand is silent.
func (t *TickLoop) tickEquipment() {
	t.trace("tickEquipment")
	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue
		}
		cur := playerMainHand(p)
		if !p.equipInit {
			p.lastMainHand = cur
			p.equipInit = true
			continue
		}
		if slotDataEqual(cur, p.lastMainHand) {
			continue
		}
		p.lastMainHand = cur
		udebug("eat", "equip mainhand player=%d item=%d count=%d", p.entityID, int(cur.ItemID), int(cur.Count))
		t.broadcastToTrackers(p.entityID, encodeSetEquipment(p.entityID, equipmentSlotMainHand, cur))
	}
}

// broadcastUsingItem syncs DATA_LIVING_ENTITY_FLAGS to the players tracking this player so the
// 3rd-person eat/use pose appears (and clears). using=true sets bit 0x01 (IS_USING_ITEM) plus
// 0x02 when the active hand is the OFF_HAND; using=false clears the flags (byte 0). Mirrors
// LivingEntity.startUsingItem / stopUsingItem's setLivingEntityFlag, pushed via SetEntityData.
// The eater is NOT included — it predicts its own first-person animation off the
// ServerboundUseItem it sent (broadcastToTrackers already excludes the actor).
func (t *TickLoop) broadcastUsingItem(p *tickPlayer, using bool, hand int32) {
	var flags int8
	if using {
		flags = livingFlagUsingItem
		if hand == interactionHandOff {
			flags |= livingFlagOffHandUse
		}
	}
	udebug("eat", "pose player=%d using=%v hand=%d flags=0x%02x", p.entityID, using, hand, byte(flags))
	t.broadcastToTrackers(p.entityID, encodeSetEntityDataByID(p.entityID, livingEntityFlagsEntry(flags)))
}
