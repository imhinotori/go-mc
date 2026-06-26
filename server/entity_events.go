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

// sendChangesMoveThreshold is ServerEntity.sendChanges's "did it move enough to send a delta"
// gate: delta.lengthSqr() >= 7.62939453125E-6 (== (1/128)^2 / 4... the LP precision floor). Below
// it the position is treated as unchanged for the tick (a sub-pixel jitter sends nothing).
const sendChangesMoveThreshold = 7.62939453125e-6

// sendChangesForceSyncEvery is the % 60 cadence: even with no net movement, sendChanges sends a
// position component every 60 ticks (var10 = moved || tickCount % 60 == 0) so a long-idle entity
// stays anchored. tickEntityMovement uses teleportDelay as the per-entity tick counter.
const sendChangesForceSyncEvery = 60

// sendChangesTeleportDelayCap is the 400-tick guard: if teleportDelay (ticks since the last
// absolute sync) exceeds 400, sendChanges sends an absolute EntityPositionSync instead of a delta
// (re-anchoring against accumulated delta drift). Cite: ServerEntity.sendChanges `teleportDelay
// <= 400` branch.
const sendChangesTeleportDelayCap = 400

// tickEntityMovement ports net.minecraft.server.level.ServerEntity.sendChanges's MOVEMENT half:
// once per tracked entity, decide between a DELTA move packet (MoveEntityPos/PosRot/Rot, ~6 bytes)
// and an absolute EntityPositionSync (~32 bytes), update the per-entity send state, and broadcast
// to the players tracking the entity. This replaces the previous tracker behavior of sending an
// absolute TeleportEntity to EVERY observer EVERY tick (the deviation: 5-10× bandwidth + remote
// avatars stuttering). Runs ONCE per entity (the send decision is per-entity, not per-observer),
// then broadcasts the chosen packet via broadcastToTrackers — exactly ServerEntity's
// synchronizer.sendToTrackingPlayers fan-out.
//
// THE DECISION (decompiled from sendChanges, 26.2-inner.jar):
//   - rotChanged = |packDegrees(yaw)-lastSentYRot| >= 1 || |packDegrees(pitch)-lastSentXRot| >= 1
//   - teleportDelay++
//   - delta = currentPos - base(lastSentPos); moved = delta.lengthSqr() >= 7.629e-6
//   - sendPos = moved || teleportDelay % 60 == 0   (the long-idle re-anchor cadence)
//   - encode delta via VecDeltaCodec (round(curr*4096) - round(base*4096)); overflow = any |d| > Short
//   - if overflow OR teleportDelay > 400 OR wasOnGround != onGround → EntityPositionSync (absolute),
//     reset teleportDelay=0, wasOnGround=onGround, re-base the codec to the current pos
//   - else: (sendPos && rotChanged) → PosRot ; sendPos → Pos ; rotChanged → Rot
//   - on a Pos/PosRot (a real position component) re-base the codec; on rotChanged update lastSent*Rot
//
// Runs on the tick goroutine over tick-owned Entity state, BEFORE tracker.Tick so a freshly-
// visible entity (added by the tracker THIS tick) is not yet in any tracked set and so gets no
// delta — its AddEntity already carries the absolute pos, and next tick the delta is from the
// seeded base. moveInit seeds the base/angles for a never-sent entity (the ctor setBase analogue).
func (t *TickLoop) tickEntityMovement() {
	t.trace("tickEntityMovement")
	if t.entities == nil {
		return
	}
	for _, e := range t.entities.all() {
		if e == nil {
			continue
		}
		if !e.moveInit {
			// ServerEntity ctor: positionCodec.setBase(spawnPos); lastSent*Rot = packDegrees(angle).
			e.lastSentX, e.lastSentY, e.lastSentZ = e.x, e.y, e.z
			e.lastSentYRot = packDegrees(e.yaw)
			e.lastSentXRot = packDegrees(e.pitch)
			e.wasOnGround = e.onGround
			e.teleportDelay = 0
			e.moveInit = true
			continue
		}
		t.sendEntityMovementChanges(e)
	}
}

// sendEntityMovementChanges is the per-entity body of tickEntityMovement (one ServerEntity
// .sendChanges movement decision). Separated so a test can drive a single entity. Mutates the
// entity's send state and broadcasts the chosen move packet to trackers.
func (t *TickLoop) sendEntityMovementChanges(e *Entity) {
	e.sendTickCount++ // ServerEntity.tickCount: free-running, drives the %60 idle re-anchor
	yRot := packDegrees(e.yaw)
	xRot := packDegrees(e.pitch)
	rotChanged := absI8(yRot-e.lastSentYRot) >= 1 || absI8(xRot-e.lastSentXRot) >= 1

	e.teleportDelay++

	// VecDeltaCodec delta + overflow check (encode = round(d*4096); base = encode(lastSent*)).
	dxL := encodeDelta(e.x) - encodeDelta(e.lastSentX)
	dyL := encodeDelta(e.y) - encodeDelta(e.lastSentY)
	dzL := encodeDelta(e.z) - encodeDelta(e.lastSentZ)
	overflow := dxL < -32768 || dxL > 32767 || dyL < -32768 || dyL > 32767 || dzL < -32768 || dzL > 32767

	ddx := e.x - e.lastSentX
	ddy := e.y - e.lastSentY
	ddz := e.z - e.lastSentZ
	moved := (ddx*ddx + ddy*ddy + ddz*ddz) >= sendChangesMoveThreshold
	sendPos := moved || e.sendTickCount%sendChangesForceSyncEvery == 0

	// Absolute re-sync branch: overflow / 400-tick cap / onGround flip.
	if overflow || e.teleportDelay > sendChangesTeleportDelayCap || e.wasOnGround != e.onGround {
		e.wasOnGround = e.onGround
		e.teleportDelay = 0
		t.broadcastToTrackers(e.id, encodeEntityPositionSync(e))
		// EntityPositionSync re-bases the codec to the current pos (positionCodec.setBase via the
		// sync). lastSent*Rot are NOT updated here (vanilla sets them only on a Rot/PosRot send),
		// but the next tick's rotChanged compares against them; the head/rot is re-sent then if
		// still different — observably correct (the absolute sync already carried the float angle).
		e.lastSentX, e.lastSentY, e.lastSentZ = e.x, e.y, e.z
		t.broadcastToTrackers(e.id, encodeRotateHead(e.id, e.headYaw))
		return
	}

	// Delta branch: PosRot / Pos / Rot per sendPos+rotChanged.
	switch {
	case sendPos && rotChanged:
		t.broadcastToTrackers(e.id, encodeMoveEntityPosRotB(e.id,
			pk.Short(int16(dxL)), pk.Short(int16(dyL)), pk.Short(int16(dzL)), yRot, xRot, e.onGround))
		e.lastSentX, e.lastSentY, e.lastSentZ = e.x, e.y, e.z // re-base (a position component was sent)
		e.lastSentYRot, e.lastSentXRot = yRot, xRot
	case sendPos:
		t.broadcastToTrackers(e.id, encodeMoveEntityPos(e.id,
			pk.Short(int16(dxL)), pk.Short(int16(dyL)), pk.Short(int16(dzL)), e.onGround))
		e.lastSentX, e.lastSentY, e.lastSentZ = e.x, e.y, e.z // re-base
	case rotChanged:
		t.broadcastToTrackers(e.id, encodeMoveEntityRotB(e.id, yRot, xRot, e.onGround))
		e.lastSentYRot, e.lastSentXRot = yRot, xRot
	}

	// RotateHead (the body's head yaw) is sent alongside a move when the head turned. ServerEntity
	// sends it whenever the entity moved/rotated; gate it on a position-or-rotation send so an idle
	// entity emits nothing.
	if sendPos || rotChanged {
		t.broadcastToTrackers(e.id, encodeRotateHead(e.id, e.headYaw))
	}
}

// absI8 is Math.abs over the int8 angle difference, computed in int to avoid int8 overflow on the
// -128 edge (the bytecode does `isub; Math.abs(int)` on the byte values widened to int).
func absI8(d int8) int {
	v := int(d)
	if v < 0 {
		return -v
	}
	return v
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
