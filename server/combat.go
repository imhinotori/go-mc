package server

import (
	"math"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// combat.go is ENT-05: the server-owned health / damage / death / respawn loop. Health is
// SERVER-owned (threat T-6-05) — a 26.2 client has NO health-setting packet; it can only
// REQUEST a respawn via ServerboundClientCommand(PERFORM_RESPAWN) (routed in server/tick.go
// dispatch). The server drives the whole loop from the tick-owned tickPlayer.health/food/
// saturation/dead fields, on the tick goroutine (TICK-05), so it is -race clean by the same
// single-owner discipline as the rest of the player state.
//
// ALL THREE WIRE LAYOUTS ARE JAR-VERIFIED (javap'd from temp/cache/26.2-inner.jar this
// session — the field order matched the plan exactly, no correction needed):
//
//	ClientboundSetHealth        = Float health + VarInt food + Float saturation
//	ClientboundPlayerCombatKill = VarInt playerId + Component message (TRUSTED_STREAM_CODEC,
//	                              an NBT component — chat.Message.WriteTo emits exactly that)
//	ClientboundRespawn          = CommonPlayerSpawnInfo.write (the Phase-5-SEALED
//	                              commonPlayerSpawnInfoEncoder) + a trailing Byte dataToKeep
//
// The Respawn packet REUSES the Phase-5-sealed commonPlayerSpawnInfoEncoder verbatim: the
// trailing dataToKeep byte is the ONLY new byte beyond the sealed encoder. We do NOT re-derive
// the spawn-info layout (06-RESEARCH Anti-Patterns: "don't rebuild the sealed encoder").

// ServerboundClientCommand action enum (jar-verified: ServerboundClientCommandPacket.Action
// has exactly two constants). PERFORM_RESPAWN (0) is the client's "I clicked Respawn" request;
// REQUEST_STATS (1) is the statistics screen open, a v1 no-op. dispatch routes only 0, and
// only when the player is actually dead.
const (
	clientCommandPerformRespawn = 0
	clientCommandRequestStats   = 1
)

// respawnDataKeepNone is the dataToKeep byte for a FULL respawn reset (v1): keep nothing.
// dataToKeep is a bitset the client ANDs in shouldKeep(flag) to decide whether to preserve
// attributes/metadata/etc across the respawn. 0 means a clean slate — the player respawns
// with default state, which is the v1 death-then-fresh-spawn behavior. (The vanilla "keep
// all data" dimension-change respawn would set bits here; that is a later concern.)
const respawnDataKeepNone = 0

// setHealth builds ClientboundSetHealth in the JAR-VERIFIED order: Float health, VarInt food,
// Float saturation (ClientboundSetHealthPacket.write: writeFloat, writeVarInt, writeFloat).
// The server sends it whenever a player's health/food/saturation changes so the client's HUD
// reflects the authoritative server state — the client never sets these itself (T-6-05).
func setHealth(health float32, food int32, sat float32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetHealth),
		pk.Float(health),
		pk.VarInt(food),
		pk.Float(sat),
	)
}

// applyDamage lowers the player's tick-owned health by amount (clamped at 0 — never negative),
// sends the authoritative SetHealth so the client's HUD follows, and, if the blow was lethal
// (health reached 0), drives the death flow (die). It is the server-authoritative entry point
// for every damage source (fall, mob, environment); the client cannot veto or claim its own
// health (T-6-05). Runs on the tick goroutine over tick-owned state (TICK-05).
func (t *TickLoop) applyDamage(p *tickPlayer, amount float32) {
	if p.dead {
		return // already dead: a corpse takes no further damage until it respawns
	}
	p.health -= amount
	if p.health < 0 {
		p.health = 0 // clamp: health is never negative
	}
	p.client.Send(setHealth(p.health, p.food, p.saturation))
	if p.health <= 0 {
		t.die(p)
	}
}

// die drives the death flow: it sends ClientboundPlayerCombatKill (which raises the client's
// death screen) and sets the tick-owned dead flag so the player stops taking damage and a
// subsequent ServerboundClientCommand(PERFORM_RESPAWN) is honored. The combat message is a
// minimal generic death text for v1 (the real damage-source attribution is a later concern).
// Runs on the tick goroutine (TICK-05).
func (t *TickLoop) die(p *tickPlayer) {
	p.dead = true
	p.client.Send(playerCombatKill(p.entityID, chat.Text("You died")))
}

// playerCombatKill builds ClientboundPlayerCombatKill in the JAR-VERIFIED order: VarInt
// playerId + Component message (ClientboundPlayerCombatKillPacket.STREAM_CODEC composites
// ByteBufCodecs.VAR_INT then ComponentSerialization.TRUSTED_STREAM_CODEC). chat.Message.WriteTo
// emits the NBT component the TRUSTED_STREAM_CODEC expects. This packet IS the death screen on
// the client.
func playerCombatKill(playerID int32, message chat.Message) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundPlayerCombatKill),
		pk.VarInt(playerID),
		message,
	)
}

// respawnPacket builds ClientboundRespawn by REUSING the Phase-5-sealed
// commonPlayerSpawnInfoEncoder (CommonPlayerSpawnInfo.write) followed by the trailing Byte
// dataToKeep — the ONLY new byte beyond the sealed encoder (jar-verified:
// ClientboundRespawnPacket.write calls commonPlayerSpawnInfo.write(buf) then buf.writeByte(
// dataToKeep)). We do NOT re-derive the spawn-info layout; the encoder is the same one the
// Login bootstrap uses, capture-diff-sealed in Phase 5.
func respawnPacket(dataToKeep byte) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundRespawn),
		commonPlayerSpawnInfoEncoder{},
		pk.Byte(dataToKeep),
	)
}

// performRespawn completes the death->respawn loop for a DEAD player (the dispatch route only
// calls it when player.dead is true). It:
//
//  1. sends ClientboundRespawn (the sealed spawn-info encoder + dataToKeep) so the client tears
//     down its death screen and rebuilds a fresh ClientLevel;
//  2. resets the tick-owned health/food/saturation to a full survival player and clears dead;
//  3. re-teleports the player to the world spawn with a FRESH teleport id and re-arms the
//     confirm gate (confirmedTeleport=false), mirroring the bootstrap, so movement is gated
//     until the client echoes the new teleport (PLAY-02 / T-5-03);
//  4. resets the streamer (centerSent=false + clears sentChunks) so flushOutbound re-streams
//     the whole view ring into the fresh ClientLevel (a respawn rebuilds the client's world,
//     so every column must be re-sent — exactly the recenter discipline, but full).
//
// Runs on the tick goroutine over tick-owned state (TICK-05); every Send goes through the
// bounded outbound queue so the writeLoop stays the sole socket writer (T-5-06).
func (t *TickLoop) performRespawn(p *tickPlayer) {
	// (1) Respawn: rebuild the client's world. dataToKeep=0 → a full reset (v1 death respawn).
	// ClientboundRespawn tears down the old ClientLevel and builds a fresh, EMPTY one — so the
	// client now waits for the SAME bootstrap framing the join used before it will render the
	// world: a GameEvent(LEVEL_CHUNKS_LOAD_START) that tells the client "chunks are coming"
	// (without it the client sits on "Loading terrain…" indefinitely after a respawn), the
	// PlayerPosition teleport, and the early-Play tail (abilities + held slot). Mirroring the
	// bootstrap here is the load-bearing fix: a respawn is, to the client, a second join into a
	// fresh ClientLevel.
	p.client.Send(respawnPacket(respawnDataKeepNone))
	p.client.Send(writeGameEventPacket(gameEventLevelChunksLoadStart, 0))

	// (2) Restore a full survival player AND push the authoritative SetHealth. Resetting the
	// tick-owned health alone is NOT enough — the client's HUD still shows 0 HP (and keeps the
	// death screen up) until it receives a ClientboundSetHealth with the restored value. Send it
	// so the client clears the death overlay and shows full hearts.
	p.health = maxHealth
	p.food = maxFood
	p.saturation = defaultSaturation
	p.dead = false
	p.client.Send(setHealth(p.health, p.food, p.saturation))

	// (3) Re-teleport to the world spawn with a fresh, tick-allocated teleport id, re-arming the
	// confirm gate. 17-06 spawn-inside-a-block fix: prefer the SAFE spawn point (the ported vanilla
	// PlayerSpawnFinder column over the FULLY-DECORATED spawn chunk — the air cell ON a standable
	// floor, which may NOT be the (8,8) center when a tree/structure occupies it), the SAME
	// placement the join bootstrap uses. Only when no safe point was wired (SetSpawnPoint never
	// called — e.g. a SetSpawn-only unit test) does it fall back to the blind center column.
	spawnX := 8.5 // chunk (0,0) block center
	spawnZ := 8.5
	spawnY := float64(t.spawnSurfaceY + 2)
	if t.hasSpawnPoint {
		spawnX, spawnY, spawnZ = t.spawnPoint.X, t.spawnPoint.Y, t.spawnPoint.Z
	}
	p.center = chunkCenterOf(int32(math.Floor(spawnX)), int32(math.Floor(spawnZ)))
	teleportID := t.nextTeleportID()
	p.awaitingTeleport = teleportID
	p.confirmedTeleport = false
	p.client.Send(writePlayerPositionPacket(teleportID, spawnX, spawnY, spawnZ, 0, 0))

	// (4) Re-send the early-Play tail (abilities + held slot) the join bootstrap sends, so the
	// fresh ClientLevel has the player's movement abilities + hotbar selection restored.
	p.client.Send(writePlayerAbilities(false, false, false, false, defaultFlyingSpeed, defaultWalkingSpeed))
	p.client.Send(writeSetHeldSlot(defaultHeldSlot))

	// (5) Reset the streamer so the fresh ClientLevel re-streams the full ring. flushOutbound
	// re-emits SetChunkCacheCenter under !centerSent and re-sends every column under the cleared
	// sent-set.
	p.centerSent = false
	p.sentChunks = make(map[level.ChunkPos]bool)
}
