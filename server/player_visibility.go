package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// player_visibility.go is the GAMEPLAY-01 keystone (Plan 17-01): it makes players VISIBLE to
// each other by putting each connected player into the tick-owned entityStore as an
// entity.Player instance (id==entityID), keeping that instance position-synced from the
// authoritative tickPlayer each tick (BEFORE the tracker reads near()), and broadcasting the
// PlayerInfoUpdate(ADD_PLAYER) / PlayerInfoRemove tab-list entries bidirectionally at
// join/leave. The tracker (tracker.go) is already entity-type-agnostic — it spawns
// AddEntity/TeleportEntity/RemoveEntities for any store Entity and skips e.id==p.entityID — so
// the fix is DATA (put players in the store + broadcast the tab entry), not tracker logic.
//
// Why the tab-list broadcast is mandatory: a Notchian 26.2 client DROPS an AddEntity for a
// player (type 156) unless a PlayerInfoUpdate(ADD_PLAYER) for that UUID already arrived
// (minecraft.wiki Java_Edition_protocol/FAQ). So at join the joiner's entry is broadcast to
// every OTHER player AND every existing player's entry is sent to the joiner; at leave a
// PlayerInfoRemove is broadcast.
//
// All mutation runs on the tick goroutine (drainRegistrations / tickEntities), so the store
// and the players slice stay single-owner -race clean (TICK-05).

// newPlayerEntity constructs the store Entity for a joining player. It is built DIRECTLY (not
// via NewEntity, which assigns a FRESH uuid) so id == p.entityID and uuid == p.uuid: the id
// equality makes the tracker's self-skip (tracker.go: e.id == p.entityID) work, and the uuid
// equality makes the AddEntity objectUUID match the broadcast tab-list entry so the client
// renders the avatar. Type/dims are the generated 776 Player record.
// Source: data/entity/entity.go:1424 (Player ID 156, Width 0.6, Height 1.8).
func newPlayerEntity(p *tickPlayer) *Entity {
	return &Entity{
		id:      p.entityID,
		typ:     entity.Player.ID, // 156
		uuid:    p.uuid,
		x:       p.x,
		y:       p.y,
		z:       p.z,
		yaw:     p.yaw,
		pitch:   p.pitch,
		headYaw: p.headYaw,
		width:   entity.Player.Width,  // 0.6
		height:  entity.Player.Height, // 1.8
		// Carry the player's displayed-skin-parts (DATA_PLAYER_MODE_CUSTOMISATION) so every
		// AddEntity-time SetEntityData a tracker sends renders the second/overlay skin layer (hat,
		// jacket, sleeves, pants). nil until the client reports its preference (then refreshed live
		// by handleClientInformation -> refreshPlayerSkinMetadata).
		metadata: playerSkinMetadata(p.displayedSkinParts),
	}
}

// syncPlayerEntities re-syncs every player's store Entity from the authoritative tickPlayer
// fields each tick. Called from tickEntities BEFORE tracker.Tick so the tracker's near()
// broad-phase and its AddEntity/TeleportEntity encoders see this tick's positions — without
// this, remote avatars freeze at spawn (Pitfall 4). The move() call re-buckets the Entity on
// a column cross so near() never returns a stale bucket. Tick-owned.
func (t *TickLoop) syncPlayerEntities() {
	for _, p := range t.players {
		if p == nil || p.playerEntity == nil {
			continue
		}
		t.entities.move(p.playerEntity, p.x, p.y, p.z)
		p.playerEntity.yaw = p.yaw
		p.playerEntity.pitch = p.pitch
		p.playerEntity.headYaw = p.headYaw
		p.playerEntity.onGround = p.onGround
	}
}

// lookupPlayerByEntityID resolves a store entity id to its owning tickPlayer, or nil. It is
// the GAMEPLAY-04 PvP target-resolution dependency: ServerboundInteract carries an entity id,
// but applyDamage operates on a tickPlayer, so the attack handler needs this reverse lookup to
// find the victim. Provided HERE (Plan 17-01) so Plan 17-03 (GAMEPLAY-04) never edits tick.go.
// A linear scan of t.players is ample at the connected-player scale. Tick-owned.
//
// used by 17-03 GAMEPLAY-04 attack target resolution.
func (t *TickLoop) lookupPlayerByEntityID(id int32) *tickPlayer {
	for _, p := range t.players {
		if p != nil && p.entityID == id {
			return p
		}
	}
	return nil
}

// broadcastPlayerInfoAdd sends the joining player's PlayerInfoUpdate(ADD_PLAYER) tab-list
// entry to every OTHER connected player, so their clients hold the entry the upcoming
// AddEntity requires (Pitfall 1). The entry is built from the SERVER-authoritative login
// profile (joiner.uuid / joiner.name), never a client-supplied field (threat T-17-01).
// Tick-owned (called from drainRegistrations on the owner).
func (t *TickLoop) broadcastPlayerInfoAdd(joiner *tickPlayer) {
	// ONLINE-01: carry the joiner's authenticated skin properties so OTHER players render its real
	// online skin (offline -> nil -> count 0, Steve/Alex). SERVER-authoritative (the hasJoined
	// response stored on the tickPlayer at registration), never a client-supplied field (T-18-05).
	pkt := writePlayerInfoUpdateAdd(joiner.uuid, joiner.name, gameModeSurvival, joiner.properties)
	for _, other := range t.players {
		if other == nil || other == joiner || other.client == nil {
			continue
		}
		other.client.Send(pkt)
	}
}

// sendExistingPlayersTo sends each already-connected player's PlayerInfoUpdate(ADD_PLAYER)
// entry to the joiner, so the joiner's client can render the avatars that are already in the
// world (the other half of the bidirectional tab sync). Tick-owned.
func (t *TickLoop) sendExistingPlayersTo(joiner *tickPlayer) {
	if joiner.client == nil {
		return
	}
	for _, other := range t.players {
		if other == nil || other == joiner {
			continue
		}
		// ONLINE-01: carry each existing player's authenticated skin properties so the joiner
		// renders their real online skins (offline -> nil -> count 0). SERVER-authoritative.
		joiner.client.Send(writePlayerInfoUpdateAdd(other.uuid, other.name, gameModeSurvival, other.properties))
	}
}

// handleClientInformation decodes a PLAY ServerboundClientInformation and applies its
// modelCustomisation (displayed skin parts) to the player — the 1:1 slice of ServerPlayer.
// updateOptions we need for skin layers. Wire order (jar-verified ClientInformation(FriendlyByteBuf)):
// readUtf(16) language, readByte viewDistance, readEnum chatVisibility (VarInt), readBoolean
// chatColors, readUnsignedByte modelCustomisation, then mainHand/textFilter/allowsListing/particle
// (unused here). We decode through modelCustomisation and ignore the tail. A malformed/short payload
// is a no-op (never panics). On a CHANGE it refreshes the player entity's DATA_PLAYER_MODE_
// CUSTOMISATION metadata and pushes a fresh SetEntityData to every player already tracking this one
// so the overlay layers update live. Tick-owned.
func (t *TickLoop) handleClientInformation(p *tickPlayer, pkt pk.Packet) {
	var (
		language       pk.String
		viewDistance   pk.Byte
		chatVisibility pk.VarInt
		chatColors     pk.Boolean
		modelCustom    pk.UnsignedByte
	)
	if err := pkt.Scan(&language, &viewDistance, &chatVisibility, &chatColors, &modelCustom); err != nil {
		return // malformed/short: no mutation (never panic)
	}
	parts := uint8(modelCustom)
	if parts == p.displayedSkinParts {
		// Even if unchanged, make sure the entity carries the metadata (it may have been built
		// before this value was known, or rebuilt). Refresh + rebroadcast to be safe.
		if p.playerEntity != nil && len(p.playerEntity.metadata) == 0 && parts != 0 {
			p.playerEntity.metadata = playerSkinMetadata(parts)
			t.broadcastSkin(p)
		}
		return
	}
	p.displayedSkinParts = parts
	if p.playerEntity == nil {
		return // not yet spawned into the store; newPlayerEntity will carry the value at spawn
	}
	// Refresh the entity's pre-built metadata so a LATER tracker spawn includes the layers, push a
	// live SetEntityData to everyone already tracking this player, AND send the player its own
	// updated skin so its client re-renders its overlay layer.
	p.playerEntity.metadata = playerSkinMetadata(parts)
	t.broadcastSkin(p)
	t.sendSelfSkin(p)
}

// sendSelfSkin sends the player its OWN DATA_PLAYER_MODE_CUSTOMISATION via a SetEntityData on its
// own entityID, so the client renders its own second/overlay skin layer (hat/jacket/sleeves) — the
// tracker self-skips this player, so without this explicit self-send the client never receives its
// own skin metadata and shows the base model (operator: 'en vanilla puedo ver mi segunda capa, en
// Sulfur no'). Vanilla's SynchedEntityData.set broadcasts to ALL tracking connections, the owner
// included; Sulfur's tracker omits the owner, so we send it here. No-op until the client has
// reported parts (parts 0 -> nil metadata -> nothing to render yet). Tick-owned.
func (t *TickLoop) sendSelfSkin(p *tickPlayer) {
	if p.client == nil || p.playerEntity == nil || p.displayedSkinParts == 0 {
		return
	}
	p.client.Send(encodeSetEntityData(p.playerEntity))
}

// broadcastSkin pushes a live SetEntityData (the player's skin-parts metadata) to every other
// player currently tracking this player, so existing viewers update the overlay layers without
// waiting for a re-track. Tick-owned.
func (t *TickLoop) broadcastSkin(p *tickPlayer) {
	if p.playerEntity == nil {
		return
	}
	live := encodeSetEntityData(p.playerEntity)
	for _, other := range t.players {
		if other == nil || other == p || other.client == nil {
			continue
		}
		if other.tracked != nil && other.tracked[p.entityID] {
			other.client.Send(live)
		}
	}
}

// broadcastPlayerInfoRemove sends a PlayerInfoRemove for the leaving player's UUID to every
// remaining connected player, so their clients drop the tab-list entry (and stop rendering the
// avatar). Tick-owned (called from removePlayer on the owner).
func (t *TickLoop) broadcastPlayerInfoRemove(id uuid.UUID) {
	pkt := writePlayerInfoUpdateRemove(id)
	for _, other := range t.players {
		if other == nil || other.client == nil {
			continue
		}
		other.client.Send(pkt)
	}
}
