// state_packets.go -- inbound serverbound STATE-change packet handlers: the client toggling
// flight, changing its game mode, and changing / locking the world difficulty. Each is a 1:1 port
// of the matching ServerGamePacketListenerImpl handler in the unobfuscated 26.2 jar. They are
// low-frequency state mutations (no positional/temporal ordering), so dispatch (tick.go) routes
// them DIRECTLY here on the tick goroutine rather than through the subtick buffer -- exactly like
// handleClientInformation. Every handler decodes defensively (a Scan error is a silent no-op,
// never a panic -- T-3-02) and mirrors the vanilla authority gate before mutating any state.
package server

import (
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/imhinotori/sulfur/data/packetid"
)

// abilitiesFlagFlying is ServerboundPlayerAbilitiesPacket.FLAG_FLYING: the isFlying bit in the
// single packed abilities byte. VERIFIED javap ServerboundPlayerAbilitiesPacket ctor(FriendlyByteBuf):
// readByte, then isFlying = (b & 2) != 0; write: (isFlying ? b | 2 : b). So the wire is ONE byte and
// only bit 1 (value 2) is meaningful serverbound.
const abilitiesFlagFlying = 0x02

// mayFly derives Abilities.mayfly from the player GameType, mirroring
// net.minecraft.world.level.GameType.updatePlayerAbilities: CREATIVE and SPECTATOR set mayfly true;
// SURVIVAL and ADVENTURE set it false. handlePlayerAbilities reads this to decide whether the
// client flight toggle is honored.
func mayFly(gameMode int32) bool {
	return gameMode == gameModeCreative || gameMode == gameModeSpectator
}

// handlePlayerAbilities is a 1:1 port of
// net.minecraft.server.network.ServerGamePacketListenerImpl.handlePlayerAbilities:
//
//	player.getAbilities().flying = packet.isFlying() && player.getAbilities().mayfly;
//
// The server accepts the client flight toggle ONLY when the player is actually allowed to fly
// (mayfly, i.e. creative/spectator); a survival/adventure player claiming flight is clamped to
// false. There is NO clientbound echo in the 26.2 handler (it just writes the field). Without this
// the server keeps believing a player who stopped flying is still airborne -- a desync. Decoded on
// the owner; a Scan error is a silent no-op (T-3-02).
func (t *TickLoop) handlePlayerAbilities(p *tickPlayer, packet pk.Packet) {
	var flags pk.UnsignedByte
	if err := packet.Scan(&flags); err != nil {
		return
	}
	isFlying := byte(flags)&abilitiesFlagFlying != 0
	p.flying = isFlying && mayFly(p.gameMode)
}

// handleChangeGameMode is a 1:1 port of
// net.minecraft.server.network.ServerGamePacketListenerImpl.handleChangeGameMode:
//
//	if (!GameModeCommand.PERMISSION_CHECK.check(player.permissions())) { LOGGER.warn(...); return; }
//	GameModeCommand.setGameMode(player, packet.mode());
//
// PERMISSION_CHECK == COMMANDS_GAMEMASTER. GameModeCommand.setGameMode(player, mode) resolves to
// ServerPlayer.setGameMode(mode), which is exactly what setPlayerGameMode already ports (it sends
// the CHANGE_GAME_MODE game event + the player-info update to the client). The op gate is the same
// permission node the /gamemode command uses; a non-gamemaster request is refused (a no-op here,
// vanilla additionally logs a warning). The wire is a single VarInt GameType id
// (GameType.STREAM_CODEC == ByteBufCodecs.idMapper -> VarInt).
func (t *TickLoop) handleChangeGameMode(p *tickPlayer, packet pk.Packet) {
	var mode pk.VarInt
	if err := packet.Scan(&mode); err != nil {
		return
	}
	// GameType.byId clamps out-of-range ids to the default in vanilla; here an unknown id is simply
	// not one of the four valid modes, so refuse rather than assign a bogus GameType.
	if int(mode) < gameModeSurvival || int(mode) > gameModeSpectator {
		return
	}
	// Op gate: GameModeCommand.PERMISSION_CHECK (COMMANDS_GAMEMASTER). Mirrored by the /gamemode
	// command permission node -- a non-operator request is refused.
	if !t.playerHasPermission(p, "minecraft.command.gamemode") {
		return
	}
	// GameModeCommand.setGameMode -> ServerPlayer.setGameMode: apply + tell the client.
	t.setPlayerGameMode(p, int(mode))
}

// handleChangeDifficulty is a 1:1 port of
// net.minecraft.server.network.ServerGamePacketListenerImpl.handleChangeDifficulty:
//
//	if (!player.permissions().hasPermission(COMMANDS_GAMEMASTER) && !isSingleplayerOwner()) {
//	    LOGGER.warn("Player {} tried to change difficulty ... without required permissions", ...);
//	    return;
//	}
//	server.setDifficulty(packet.difficulty(), false);
//
// The gate is COMMANDS_GAMEMASTER OR the singleplayer host; a dedicated server has no singleplayer
// owner, so only the permission arm applies, mirrored by the /difficulty command node. On success,
// MinecraftServer.setDifficulty stores the level difficulty (WorldData.setDifficulty) and broadcasts
// ClientboundChangeDifficulty to every player. The wire is a single VarInt Difficulty id
// (Difficulty.STREAM_CODEC == ByteBufCodecs.idMapper -> VarInt; PEACEFUL 0/EASY 1/NORMAL 2/HARD 3).
func (t *TickLoop) handleChangeDifficulty(p *tickPlayer, packet pk.Packet) {
	var id pk.VarInt
	if err := packet.Scan(&id); err != nil {
		return
	}
	if int(id) < int(difficultyPeaceful) || int(id) > int(difficultyHard) {
		return
	}
	// Op gate (COMMANDS_GAMEMASTER-or-singleplayer-owner). A non-operator request is refused.
	if !t.playerHasPermission(p, "minecraft.command.difficulty") {
		return
	}
	// MinecraftServer.setDifficulty(difficulty, false): the false arg is forceUpdate -- vanilla still
	// re-broadcasts on an unchanged value, so we set + broadcast unconditionally (the setDifficulty
	// tail: worldData.setDifficulty + PlayerList.getPlayers().forEach send ClientboundChangeDifficulty).
	t.setDifficulty(difficulty(id))
}

// handleLockDifficulty is a 1:1 port of
// net.minecraft.server.network.ServerGamePacketListenerImpl.handleLockDifficulty:
//
//	if (!player.permissions().hasPermission(COMMANDS_GAMEMASTER) && !isSingleplayerOwner()) return;
//	server.setDifficultyLocked(packet.isLocked());
//
// Same op gate as handleChangeDifficulty (a non-operator request is a SILENT return -- the lock
// handler does not even log). MinecraftServer.setDifficultyLocked stores the lock and broadcasts
// ClientboundChangeDifficulty. The wire is a single Boolean (isLocked).
func (t *TickLoop) handleLockDifficulty(p *tickPlayer, packet pk.Packet) {
	var locked pk.Boolean
	if err := packet.Scan(&locked); err != nil {
		return
	}
	if !t.playerHasPermission(p, "minecraft.command.difficulty") {
		return
	}
	t.setDifficultyLocked(bool(locked))
}

// setDifficulty is the tick-owned port of the MinecraftServer.setDifficulty tail: store the level
// difficulty (WorldData.setDifficulty) and broadcast the new (difficulty, locked) to every player
// via ClientboundChangeDifficulty. Also the single mutation point a future /difficulty refactor can
// share. Tick-owned (mutates t.levelDifficulty on the tick goroutine).
func (t *TickLoop) setDifficulty(d difficulty) {
	t.levelDifficulty = d
	t.broadcastChangeDifficulty()
}

// setDifficultyLocked ports MinecraftServer.setDifficultyLocked: store the lock flag and broadcast
// the new (difficulty, locked) to every player. Tick-owned.
func (t *TickLoop) setDifficultyLocked(locked bool) {
	t.difficultyLocked = locked
	t.broadcastChangeDifficulty()
}

// broadcastChangeDifficulty marshals ONE ClientboundChangeDifficultyPacket carrying the current
// (difficulty, difficultyLocked) and fans it to EVERY player bounded outbound queue -- the
// PlayerList.getPlayers().forEach(send(...)) tail shared by MinecraftServer.setDifficulty and
// setDifficultyLocked. The wire is VarInt difficulty-id + Boolean locked (VERIFIED javap
// ClientboundChangeDifficultyPacket: STREAM_CODEC composites Difficulty.STREAM_CODEC (idMapper ->
// VarInt) with a Boolean). Runs on the tick goroutine over the tick-owned t.players slice; the
// packet is immutable after Marshal, so the same pk.Packet is safely shared across queues.
func (t *TickLoop) broadcastChangeDifficulty() {
	out := pk.Marshal(int32(packetid.ClientboundChangeDifficulty), pk.VarInt(t.levelDifficulty), pk.Boolean(t.difficultyLocked))
	for _, pl := range t.players {
		if pl != nil && pl.client != nil {
			pl.client.Send(out)
		}
	}
}
