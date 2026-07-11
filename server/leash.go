package server

// leash.go is the SERVER-SIDE leash-link broadcast surface: the 1:1 port of
// net.minecraft.world.entity.Leashable.setLeashedTo (attach) and dropLeash (detach), reduced to
// their OBSERVABLE half -- the ClientboundSetEntityLink broadcast that makes the lead appear (attach)
// or disappear (detach) on every tracking client. The full Leashable interface (the physics pull,
// the leash-knot fence entity, the LeashData NBT) is a larger deferred subsystem; this file wires
// the piece the tracker needs so a leashed mob is CORRECT on the wire the moment it is leashed and
// at every subsequent tracking-start (the tracker's sendPairingData branch reads e.leashHolderID).
//
// e.leashHolderID is the single source of truth (entity.go): 0 == not leashed. attachLeash sets it
// and broadcasts SetEntityLink(this, holder); dropLeash clears it and broadcasts SetEntityLink(this,
// 0) (the detach form, destId 0). A plain mob (the pig) never calls these -- leashHolderID stays 0,
// no packet, byte-identical.
//
//	[VERIFIED javap Leashable.setLeashedTo(holder, broadcast): setLeashData(new LeashData(holder));
//	 if broadcast on a ServerLevel -> getChunkSource().sendToTrackingPlayers(this, new
//	 ClientboundSetEntityLinkPacket(this, holder)). Leashable.dropLeash: getChunkSource()
//	 .sendToTrackingPlayers(this, new ClientboundSetEntityLinkPacket(this, null)) (destId 0);
//	 setLeashData(null). ClientboundSetEntityLinkPacket(entity, holder-or-null).]

// attachLeash leashes e to the holder entity (by id) and broadcasts the ClientboundSetEntityLink to
// every player tracking e, so observers immediately see the lead. It mirrors
// Leashable.setLeashedTo(holder, true): store the holder, then send the link packet. RNG-free (a
// pure state write + a broadcast). The broadcast fans out via broadcastToTrackers (the same
// sendToTrackingPlayers analogue the equipment/swing broadcasts use).
func (t *TickLoop) attachLeash(e *Entity, holderID int32) {
	if e == nil || holderID == 0 {
		return // a nil entity or a 0 holder is not an attach (0 is the detach sentinel; use dropLeash)
	}
	e.leashHolderID = holderID
	t.broadcastToTrackers(e.id, encodeSetEntityLink(e.id, holderID))
}

// dropLeash removes e's leash and broadcasts the ClientboundSetEntityLink with destId 0 (the detach
// form) to every player tracking e, so the lead disappears client-side. It mirrors
// Leashable.dropLeash: send SetEntityLink(this, null) then clear the leash data. RNG-free. A no-op
// (no packet) when e is already un-leashed (leashHolderID == 0), so a double-drop is idempotent.
func (t *TickLoop) dropLeash(e *Entity) {
	if e == nil || e.leashHolderID == 0 {
		return // not leashed: nothing to detach (idempotent)
	}
	e.leashHolderID = 0
	t.broadcastToTrackers(e.id, encodeSetEntityLink(e.id, 0)) // destId 0 == detach
}
