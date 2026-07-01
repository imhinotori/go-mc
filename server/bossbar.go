package server

// bossbar.go — the ClientboundBossEvent WIRE (proto-776) + the ServerBossEvent add/remove/update
// broadcast API, ported 1:1 from the unobfuscated 26.2 inner jar (temp/cache/26.2-inner.jar,
// net.minecraft.network.protocol.game.ClientboundBossEventPacket + net.minecraft.server.level
// .ServerBossEvent + net.minecraft.world.BossEvent, CFR + javap this session).
//
// The MODEL half (serverBossEvent: name/progress/visible/players) already lives in raid.go. This
// file adds the missing wire (the note in raid.go: "no ClientboundBossEventPacket in net/packet")
// and the per-player emission ServerBossEvent.addPlayer/removePlayer/broadcast perform.
//
// WIRE LAYOUT (VERIFIED CFR ClientboundBossEventPacket.write):
//   write(buf): writeUUID(id); writeEnum(operation.getType()); operation.write(buf).
//   writeEnum(e) == writeVarInt(e.ordinal()) (VERIFIED javap FriendlyByteBuf.writeEnum:
//     invokevirtual Enum.ordinal; invokevirtual writeVarInt).
//
//   OperationType ordinals (VERIFIED CFR OperationType enum declaration order):
//     ADD=0, REMOVE=1, UPDATE_PROGRESS=2, UPDATE_NAME=3, UPDATE_STYLE=4, UPDATE_PROPERTIES=5.
//
//   ADD payload           = Component name, Float progress, VarInt color, VarInt overlay, Byte flags.
//   REMOVE payload        = (nothing).
//   UPDATE_PROGRESS       = Float progress.
//   UPDATE_NAME           = Component name.
//   UPDATE_STYLE          = VarInt color, VarInt overlay.
//   UPDATE_PROPERTIES     = Byte flags.
//   flags byte (VERIFIED CFR encodeProperties): FLAG_DARKEN 1 | FLAG_MUSIC 2 | FLAG_FOG 4.
//
//   The Component is ComponentSerialization.TRUSTED_STREAM_CODEC (the NBT network form — the SAME
//   codec ClientboundSystemChat uses here via chat.Message, so chat.Message is the faithful port).

import (
	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// bossBarColor is net.minecraft.world.BossEvent$BossBarColor. The ORDINAL is the wire value
// (writeEnum -> writeVarInt(ordinal)). VERIFIED CFR enum declaration order:
// PINK=0, BLUE=1, RED=2, GREEN=3, YELLOW=4, PURPLE=5, WHITE=6.
type bossBarColor int32

const (
	bossBarColorPink   bossBarColor = 0
	bossBarColorBlue   bossBarColor = 1
	bossBarColorRed    bossBarColor = 2
	bossBarColorGreen  bossBarColor = 3
	bossBarColorYellow bossBarColor = 4
	bossBarColorPurple bossBarColor = 5
	bossBarColorWhite  bossBarColor = 6
)

// bossBarOverlay is net.minecraft.world.BossEvent$BossBarOverlay. The ORDINAL is the wire value.
// VERIFIED CFR enum declaration order:
// PROGRESS=0, NOTCHED_6=1, NOTCHED_10=2, NOTCHED_12=3, NOTCHED_20=4.
type bossBarOverlay int32

const (
	bossBarOverlayProgress  bossBarOverlay = 0
	bossBarOverlayNotched6  bossBarOverlay = 1
	bossBarOverlayNotched10 bossBarOverlay = 2 // the raid bar overlay: ServerBossEvent(RED, NOTCHED_10)
	bossBarOverlayNotched12 bossBarOverlay = 3
	bossBarOverlayNotched20 bossBarOverlay = 4
)

// bossEventOperation is ClientboundBossEventPacket$OperationType. The ORDINAL is the wire VarInt.
// VERIFIED CFR OperationType enum declaration order.
type bossEventOperation int32

const (
	bossEventOpAdd              bossEventOperation = 0
	bossEventOpRemove           bossEventOperation = 1
	bossEventOpUpdateProgress   bossEventOperation = 2
	bossEventOpUpdateName       bossEventOperation = 3
	bossEventOpUpdateStyle      bossEventOperation = 4
	bossEventOpUpdateProperties bossEventOperation = 5
)

// bossEventProperties packs the three property booleans into the flags byte exactly as
// ClientboundBossEventPacket.encodeProperties (VERIFIED CFR): FLAG_DARKEN 1 | FLAG_MUSIC 2 |
// FLAG_FOG 4.
func bossEventProperties(darkenScreen, playMusic, createWorldFog bool) int8 {
	var f int8
	if darkenScreen {
		f |= 1
	}
	if playMusic {
		f |= 2
	}
	if createWorldFog {
		f |= 4
	}
	return f
}

// encodeBossEventAdd builds ClientboundBossEvent{ADD} (VERIFIED CFR AddOperation.write):
// UUID id, VarInt ADD(0), Component name, Float progress, VarInt color, VarInt overlay, Byte flags.
// The Component is the TRUSTED_STREAM_CODEC (chat.Message NBT network form — the same codec
// ClientboundSystemChat uses here).
//
//	[VERIFIED CFR ClientboundBossEventPacket.write -> writeUUID; writeEnum(ADD); AddOperation.write:
//	 TRUSTED_STREAM_CODEC.encode(name); writeFloat(progress); writeEnum(color); writeEnum(overlay);
//	 writeByte(encodeProperties(darken, music, fog)).]
func encodeBossEventAdd(id uuid.UUID, name chat.Message, progress float32, color bossBarColor, overlay bossBarOverlay, darkenScreen, playMusic, createWorldFog bool) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundBossEvent),
		pk.UUID(id),
		pk.VarInt(int32(bossEventOpAdd)),
		name, // ComponentSerialization.TRUSTED_STREAM_CODEC
		pk.Float(progress),
		pk.VarInt(int32(color)),
		pk.VarInt(int32(overlay)),
		pk.Byte(bossEventProperties(darkenScreen, playMusic, createWorldFog)),
	)
}

// encodeBossEventRemove builds ClientboundBossEvent{REMOVE} (VERIFIED CFR REMOVE_OPERATION.write is
// EMPTY): UUID id, VarInt REMOVE(1), then no payload.
//
//	[VERIFIED CFR: createRemovePacket -> REMOVE_OPERATION; write() is empty.]
func encodeBossEventRemove(id uuid.UUID) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundBossEvent),
		pk.UUID(id),
		pk.VarInt(int32(bossEventOpRemove)),
	)
}

// encodeBossEventUpdateProgress builds ClientboundBossEvent{UPDATE_PROGRESS} (VERIFIED CFR
// UpdateProgressOperation.write): UUID id, VarInt UPDATE_PROGRESS(2), Float progress.
//
//	[VERIFIED CFR: createUpdateProgressPacket -> UpdateProgressOperation(progress); write ->
//	 writeFloat(progress).]
func encodeBossEventUpdateProgress(id uuid.UUID, progress float32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundBossEvent),
		pk.UUID(id),
		pk.VarInt(int32(bossEventOpUpdateProgress)),
		pk.Float(progress),
	)
}

// encodeBossEventUpdateName builds ClientboundBossEvent{UPDATE_NAME} (VERIFIED CFR
// UpdateNameOperation.write): UUID id, VarInt UPDATE_NAME(3), Component name.
//
//	[VERIFIED CFR: createUpdateNamePacket -> UpdateNameOperation(name); write ->
//	 TRUSTED_STREAM_CODEC.encode(name).]
func encodeBossEventUpdateName(id uuid.UUID, name chat.Message) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundBossEvent),
		pk.UUID(id),
		pk.VarInt(int32(bossEventOpUpdateName)),
		name,
	)
}

// encodeBossEventUpdateStyle builds ClientboundBossEvent{UPDATE_STYLE} (VERIFIED CFR
// UpdateStyleOperation.write): UUID id, VarInt UPDATE_STYLE(4), VarInt color, VarInt overlay.
//
//	[VERIFIED CFR: createUpdateStylePacket -> UpdateStyleOperation(color, overlay); write ->
//	 writeEnum(color); writeEnum(overlay).]
func encodeBossEventUpdateStyle(id uuid.UUID, color bossBarColor, overlay bossBarOverlay) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundBossEvent),
		pk.UUID(id),
		pk.VarInt(int32(bossEventOpUpdateStyle)),
		pk.VarInt(int32(color)),
		pk.VarInt(int32(overlay)),
	)
}

// encodeBossEventUpdateProperties builds ClientboundBossEvent{UPDATE_PROPERTIES} (VERIFIED CFR
// UpdatePropertiesOperation.write): UUID id, VarInt UPDATE_PROPERTIES(5), Byte flags.
//
//	[VERIFIED CFR: createUpdatePropertiesPacket -> UpdatePropertiesOperation(darken, music, fog);
//	 write -> writeByte(encodeProperties(...)).]
func encodeBossEventUpdateProperties(id uuid.UUID, darkenScreen, playMusic, createWorldFog bool) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundBossEvent),
		pk.UUID(id),
		pk.VarInt(int32(bossEventOpUpdateProperties)),
		pk.Byte(bossEventProperties(darkenScreen, playMusic, createWorldFog)),
	)
}

// bossBarName maps the boss-bar model's name STRING to its Component. The raid always sets
// translatable keys (event.minecraft.raid / .victory / .defeat / .raiders_remaining), so the string
// is a translate key -> chat.Message{Translate: key} (the ComponentSerialization the ADD/UPDATE_NAME
// payload encodes). A future non-translate name would be {Text: key}; the raid only uses keys.
func bossBarName(name string) chat.Message {
	return chat.Message{Translate: name}
}

// --- ServerBossEvent broadcast API (VERIFIED CFR net.minecraft.server.level.ServerBossEvent) ------
//
// The wire encoders above are stateless; these *TickLoop methods are the ServerBossEvent side: they
// mutate the raid's serverBossEvent MODEL and emit ClientboundBossEvent to the tracked players. A
// boss bar is NOT entity-attached (unlike the tracker packets), so sends go per-player via
// tickPlayer.client.Send over t.players — the SAME seam broadcastSystemChat uses (TICK-05: the tick
// goroutine owns t.players; the raid ticks on the coordinator at the quiescent barrier, so reading
// t.players + sending here is single-owner-safe). Each ServerBossEvent method mirrors the vanilla
// setter's `if (x != this.x)` change guard so an UPDATE is sent ONLY on an actual change.

// bossPlayerByEntityID resolves a tracked-player entity id to its live *tickPlayer (the send handle).
// The boss-bar player set stores THIN entity ids (the Folia rule); the send needs the connection, so
// this scans t.players — boss bars are rare and player counts small, so the linear scan is fine.
// Returns nil if the id is no longer a live player (it will be pruned on the next updatePlayers pass).
func (t *TickLoop) bossPlayerByEntityID(entityID int32) *tickPlayer {
	for _, pl := range t.players {
		if pl != nil && pl.entityID == entityID {
			return pl
		}
	}
	return nil
}

// bossBroadcast fans a single already-marshalled ClientboundBossEvent to every player currently
// tracking the bar (ServerBossEvent.broadcast: `if (this.visible) for (player : players) send`). The
// visible guard is honored: an invisible bar sends nothing (its ADD is withheld until setVisible).
func (t *TickLoop) bossBroadcast(r *Raid, p pk.Packet) {
	if !r.bossEvent.visible {
		return
	}
	for id := range r.bossEvent.players {
		if pl := t.bossPlayerByEntityID(id); pl != nil && pl.client != nil {
			pl.client.Send(p)
		}
	}
}

// bossSetProgress ports ServerBossEvent.setProgress (VERIFIED CFR): if the progress actually changed,
// store it and broadcast UPDATE_PROGRESS. (BossEvent.setProgress clamps nothing; the raid already
// clamps before calling.)
//
//	[VERIFIED CFR ServerBossEvent.setProgress: if (progress != this.progress) { super.setProgress;
//	 setDirty; broadcast(createUpdateProgressPacket). }]
func (t *TickLoop) bossSetProgress(r *Raid, progress float32) {
	if progress == r.bossEvent.progress {
		return
	}
	r.bossEvent.progress = progress
	t.bossBroadcast(r, encodeBossEventUpdateProgress(r.bossEvent.id, progress))
}

// bossSetName ports ServerBossEvent.setName (VERIFIED CFR): if the name changed, store it and
// broadcast UPDATE_NAME. The raid's name is a translate key (bossBarName wraps it as a Component).
//
//	[VERIFIED CFR ServerBossEvent.setName: if (!Objects.equal(name, this.name)) { super.setName;
//	 setDirty; broadcast(createUpdateNamePacket). }]
func (t *TickLoop) bossSetName(r *Raid, name string) {
	if name == r.bossEvent.name {
		return
	}
	r.bossEvent.name = name
	t.bossBroadcast(r, encodeBossEventUpdateName(r.bossEvent.id, bossBarName(name)))
}

// bossAddPlayer ports ServerBossEvent.addPlayer (VERIFIED CFR): add the player to the tracked set and,
// if it was newly added AND the bar is visible, send it the ADD packet (the full bar state).
//
//	[VERIFIED CFR ServerBossEvent.addPlayer: if (players.add(player) && this.visible)
//	 player.connection.send(createAddPacket(this)).]
func (t *TickLoop) bossAddPlayer(r *Raid, p *tickPlayer) {
	if p == nil {
		return
	}
	if _, present := r.bossEvent.players[p.entityID]; present {
		return
	}
	r.bossEvent.players[p.entityID] = struct{}{}
	if r.bossEvent.visible && p.client != nil {
		p.client.Send(encodeBossEventAdd(
			r.bossEvent.id,
			bossBarName(r.bossEvent.name),
			r.bossEvent.progress,
			r.bossEvent.color,
			r.bossEvent.overlay,
			r.bossEvent.darkenScreen,
			r.bossEvent.playBossMusic,
			r.bossEvent.createWorldFog,
		))
	}
}

// bossRemovePlayer ports ServerBossEvent.removePlayer (VERIFIED CFR): drop the player from the set
// and, if it was actually present AND the bar is visible, send it the REMOVE packet.
//
//	[VERIFIED CFR ServerBossEvent.removePlayer: if (players.remove(player) && this.visible)
//	 player.connection.send(createRemovePacket(this.getId())).]
func (t *TickLoop) bossRemovePlayer(r *Raid, entityID int32) {
	if _, present := r.bossEvent.players[entityID]; !present {
		return
	}
	delete(r.bossEvent.players, entityID)
	if !r.bossEvent.visible {
		return
	}
	if pl := t.bossPlayerByEntityID(entityID); pl != nil && pl.client != nil {
		pl.client.Send(encodeBossEventRemove(r.bossEvent.id))
	}
}

// bossRemoveAllPlayers ports ServerBossEvent.removeAllPlayers (VERIFIED CFR): remove every tracked
// player (each send a REMOVE). Iterates a snapshot of the ids so the delete inside bossRemovePlayer
// does not mutate the map under the range.
//
//	[VERIFIED CFR ServerBossEvent.removeAllPlayers: for (player : Lists.newArrayList(players))
//	 removePlayer(player).]
func (t *TickLoop) bossRemoveAllPlayers(r *Raid) {
	if len(r.bossEvent.players) == 0 {
		return
	}
	ids := make([]int32, 0, len(r.bossEvent.players))
	for id := range r.bossEvent.players {
		ids = append(ids, id)
	}
	for _, id := range ids {
		t.bossRemovePlayer(r, id)
	}
}

// bossSetVisible ports ServerBossEvent.setVisible (VERIFIED CFR): on a visibility change, flip the
// flag and send every tracked player an ADD (now visible) or REMOVE (now hidden). The raid drives
// visibility off r.active (tickRaid) and true during the celebration branch.
//
//	[VERIFIED CFR ServerBossEvent.setVisible: if (visible != this.visible) { this.visible = visible;
//	 setDirty; for (player : players) player.connection.send(visible ? createAddPacket(this)
//	 : createRemovePacket(getId())). }]
func (t *TickLoop) bossSetVisible(r *Raid, visible bool) {
	if visible == r.bossEvent.visible {
		return
	}
	r.bossEvent.visible = visible
	for id := range r.bossEvent.players {
		pl := t.bossPlayerByEntityID(id)
		if pl == nil || pl.client == nil {
			continue
		}
		if visible {
			pl.client.Send(encodeBossEventAdd(
				r.bossEvent.id,
				bossBarName(r.bossEvent.name),
				r.bossEvent.progress,
				r.bossEvent.color,
				r.bossEvent.overlay,
				r.bossEvent.darkenScreen,
				r.bossEvent.playBossMusic,
				r.bossEvent.createWorldFog,
			))
		} else {
			pl.client.Send(encodeBossEventRemove(r.bossEvent.id))
		}
	}
}

// bossUpdatePlayers ports Raid.updatePlayers(ServerLevel) (VERIFIED CFR): recompute the set of valid
// players (alive + this raid is the getRaidAt(pos) closest ACTIVE raid within VALID_RAID_RADIUS_SQR),
// add newly-valid players (ADD) and remove no-longer-valid ones (REMOVE). Runs each tick from the
// raid drive loop (vanilla calls it from updateBossbar's caller / the status update). rm is the raid's
// manager (for the getRaidAt closest-raid tiebreak the validPlayer predicate uses).
//
//	[VERIFIED CFR Raid.updatePlayers: current = players; newValid = level.getPlayers(validPlayer());
//	 for newValid not in current -> addPlayer; for current not in newValid -> removePlayer.
//	 validPlayer: player.isAlive() && player.level().getRaidAt(player.blockPosition()) == this.
//	 getRaidAt == getNearbyRaid(pos, VALID_RAID_RADIUS_SQR=9216).]
func (t *TickLoop) bossUpdatePlayers(r *Raid, rm *raidsManager) {
	const validRaidRadiusSqr = 9216.0 // Raid.VALID_RAID_RADIUS_SQR (96^2)
	// newValid: the players for whom THIS raid is the closest active raid within 9216 of their pos.
	newValid := map[int32]*tickPlayer{}
	for _, pl := range t.players {
		if pl == nil {
			continue
		}
		// isAlive: v1 players are always alive on the tick path (no player-death removal keeps a dead
		// tickPlayer in t.players); the getRaidAt closest-raid check is the observable membership gate.
		if rm.getRaidAt(pl.x, pl.y, pl.z) != r {
			continue
		}
		// Guard the radius explicitly too: getRaidAt uses RAID_REMOVAL_THRESHOLD_SQR (12544) as its
		// max; validPlayer tightens it to VALID_RAID_RADIUS_SQR (9216). Apply the tighter bound.
		dx := float64(r.centerX) - pl.x
		dy := float64(r.centerY) - pl.y
		dz := float64(r.centerZ) - pl.z
		if dx*dx+dy*dy+dz*dz >= validRaidRadiusSqr {
			continue
		}
		newValid[pl.entityID] = pl
	}
	// Add newly-valid players (present in newValid, absent from the tracked set).
	for id, pl := range newValid {
		if _, tracked := r.bossEvent.players[id]; !tracked {
			t.bossAddPlayer(r, pl)
		}
	}
	// Remove no-longer-valid players (tracked but absent from newValid). Snapshot the ids first so the
	// delete inside bossRemovePlayer does not mutate the map under the range.
	var stale []int32
	for id := range r.bossEvent.players {
		if _, ok := newValid[id]; !ok {
			stale = append(stale, id)
		}
	}
	for _, id := range stale {
		t.bossRemovePlayer(r, id)
	}
}
