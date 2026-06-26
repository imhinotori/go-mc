package server

import (
	"io"
	"math"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// play_join.go is the Play-state join bootstrap. Its original Phase-4 core sends the
// three packets a vanilla 26.2 client needs in order to build its ClientLevel and
// render the already-correct superflat chunks the streamer (world_stream.go /
// tick_phases.flushOutbound) produces; Plan 05-02 APPENDS the early-Play tail
// (PlayerAbilities -> SetHeldSlot -> PlayerInfoUpdate(self) -> SetDefaultSpawnPosition)
// after those three so the joining client becomes a real, listed player (PLAY-01/05).
// The three core packets are:
//
//  1. ClientboundLogin (Join Game) — CREATES the client's ClientLevel. Without it
//     the client receives chunk packets with no level and NPEs in
//     handleSetChunkCacheCenter ("this.level is null"). This is the bug a real
//     PrismLauncher 26.2 client proved.
//  2. ClientboundGameEvent LEVEL_CHUNKS_LOAD_START (event id 13) — tells the client
//     to show terrain instead of hanging on the loading screen.
//  3. ClientboundPlayerPosition (Synchronize Player Position) — places the player on
//     solid ground above the superflat surface so it does not fall through void.
//
// CRITICAL ORDERING INVARIANT: Login MUST reach the wire before any
// SetChunkCacheCenter / LevelChunkWithLight. AcceptPlayer enqueues these bootstrap
// packets through the per-connection bounded outbound queue (Client.Send) BEFORE it
// registers the player with the tick (loop.register). The single writeLoop drains
// that queue strictly FIFO, so a bootstrap packet enqueued before registration is
// guaranteed to land on the wire before any chunk packet the tick later enqueues for
// this player. Sending the per-connection bootstrap off-tick at AcceptPlayer mutates
// NO tick-owned state — it only moves bytes through the connection's own queue — so it
// keeps the single-owner tick discipline (TICK-05) intact.
//
// ALL WIRE LAYOUTS ARE JAR-DERIVED (decompiled from the unobfuscated 26.2 inner jar,
// net.minecraft.network.protocol.game.*), NOT guessed from the (<=773) community wiki.
// The early-Play tail's two MEDIUM-confidence encoders (the PlayerInfoUpdate entry
// sub-encoding and the SetDefaultSpawnPosition RespawnData bytes) are pinned to the jar's
// field count/types here and sealed to exact bytes by Plan 05-03's capture-diff. Real
// inventory, multi-player tab broadcast, and a real spawn from world data remain Phase 5+.

// overworldDimensionTypeID is the registry index of minecraft:overworld within the
// dimension_type registry sent in the Configuration state (server/registrydata).
// Entries are sorted alphabetically by filename: overworld(0), overworld_caves(1),
// the_end(2), the_nether(3). The Holder<DimensionType> in CommonPlayerSpawnInfo
// encodes as a registry reference VarInt(id+1) — for overworld that is VarInt(1).
const overworldDimensionTypeID = 0

// overworldDimensionName is the dimension's level ResourceKey (writeResourceKey ==
// an Identifier on the wire), distinct from the dimension TYPE holder above.
const overworldDimensionName = "minecraft:overworld"

// gameEventLevelChunksLoadStart is the ClientboundGameEvent Type.id for
// LEVEL_CHUNKS_LOAD_START — jar-verified id 13 (bipush 13 in the packet's static
// initializer). Its float param is unused (0) for this event.
const gameEventLevelChunksLoadStart = 13

// gameModeSurvival / noPreviousGameMode are the GameType byte encodings in
// CommonPlayerSpawnInfo. gameType is GameType.getId (survival == 0); previousGameType
// is GameType.getNullableId, which encodes "no previous mode" as -1 (0xFF byte).
const (
	gameModeSurvival = 0
	// gameModeCreative is GameType.CREATIVE.getId() (==1). Used by the Plan 17-14 block-drop
	// gate (ServerPlayerGameMode.destroyBlock: creative drops nothing). v1 never assigns it
	// today, but the gate compares against it so a future creative player drops nothing.
	gameModeCreative   = 1
	noPreviousGameMode = -1
)

// overworldSeaLevel is the sea level reported in CommonPlayerSpawnInfo. The superflat
// floor sits below this; the value only affects client-side ambient/fog cues, not the
// rendered chunks. 63 is the vanilla overworld sea level.
const overworldSeaLevel = 63

// writeLoginPacket builds the proto-776 ClientboundLogin (Join Game). Wire order is
// jar-derived from ClientboundLoginPacket's RegistryFriendlyByteBuf constructor:
//
//	Int      playerId
//	Boolean  hardcore
//	Set      levels            (writeCollection: VarInt count, then N Identifiers)
//	VarInt   maxPlayers
//	VarInt   chunkRadius        (view distance)
//	VarInt   simulationDistance
//	Boolean  reducedDebugInfo
//	Boolean  showDeathScreen    (enableRespawnScreen)
//	Boolean  doLimitedCrafting
//	CommonPlayerSpawnInfo commonPlayerSpawnInfo  (nested, see writeCommonPlayerSpawnInfo)
//	Boolean  onlineMode         (enforcesSecureChat's sibling — "online mode" / secure)
//	Boolean  enforcesSecureChat
//
// isFlat (inside CommonPlayerSpawnInfo) is set TRUE so the client renders the world as
// a superflat (matching the Superflat generator), giving the correct void-fog and a
// flat horizon rather than normal-terrain rendering.
//
// entityID is the player's server-issued entity id, allocated per-join from the tick's
// monotonic EntityIDAllocator (ENT-01) — the replacement for the old hard-coded
// joinEntityID=1 const, so a second player or a spawned entity can never collide the
// playerId (06-RESEARCH Pitfall 7 / threat T-6-07).
func writeLoginPacket(entityID int32, viewDist int) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundLogin),
		pk.Int(entityID),                      // playerId (allocated, not the old const 1)
		pk.Boolean(false),                     // hardcore
		levelsEncoder{overworldDimensionName}, // levels: Set<ResourceKey<Level>>
		pk.VarInt(maxPlayersJoin),             // maxPlayers
		pk.VarInt(int32(viewDist)),            // chunkRadius (server-clamped view distance)
		pk.VarInt(int32(viewDist)),            // simulationDistance
		pk.Boolean(false),                     // reducedDebugInfo
		pk.Boolean(true),                      // showDeathScreen (enableRespawnScreen)
		pk.Boolean(false),                     // doLimitedCrafting
		commonPlayerSpawnInfoEncoder{},        // commonPlayerSpawnInfo (nested record)
		pk.Boolean(false),                     // onlineMode (offline server — NET-03)
		pk.Boolean(false),                     // enforcesSecureChat
	)
}

// maxPlayersJoin is the maxPlayers field reported in the Login packet. It mirrors the
// advertised server cap (cmd/sulfur maxPlayers == 20); kept here as a Play-state
// constant so the bootstrap has no cross-package dependency.
const maxPlayersJoin = 20

// levelsEncoder writes the Login packet's levels field — a Set<ResourceKey<Level>>
// encoded as readCollection/writeCollection: VarInt(count) followed by count
// Identifiers (each ResourceKey<Level> is a plain identifier on the wire). The set is
// the list of dimension names the world exposes; for the single-overworld Phase-4
// server that is exactly {minecraft:overworld}.
type levelsEncoder []string

func (l levelsEncoder) WriteTo(w io.Writer) (int64, error) {
	var n int64
	cnt, err := pk.VarInt(len(l)).WriteTo(w)
	n += cnt
	if err != nil {
		return n, err
	}
	for _, id := range l {
		m, err := pk.Identifier(id).WriteTo(w)
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// commonPlayerSpawnInfoEncoder writes the nested CommonPlayerSpawnInfo record. Wire
// order is jar-derived from CommonPlayerSpawnInfo.write:
//
//	Holder<DimensionType> dimensionType  (registry ref: VarInt(id+1))
//	ResourceKey<Level>    dimension      (writeResourceKey == Identifier)
//	Long                  seed
//	Byte                  gameType       (GameType.getId; survival == 0)
//	Byte                  previousGameType (GameType.getNullableId; none == -1 == 0xFF)
//	Boolean               isDebug
//	Boolean               isFlat
//	Optional<GlobalPos>   lastDeathLocation (empty == Boolean(false))
//	VarInt                portalCooldown
//	VarInt                seaLevel
type commonPlayerSpawnInfoEncoder struct{}

func (commonPlayerSpawnInfoEncoder) WriteTo(w io.Writer) (int64, error) {
	var n int64
	write := func(f pk.FieldEncoder) error {
		m, err := f.WriteTo(w)
		n += m
		return err
	}
	// dimensionType: Holder<DimensionType> as a registry reference. The vanilla
	// holder-registry codec writes VarInt(registryId + 1) for a referenced entry
	// (0 is reserved for an inline/direct holder, which a registry-backed dimension
	// type never is). overworld is index 0 -> VarInt(1).
	if err := write(pk.VarInt(overworldDimensionTypeID + 1)); err != nil {
		return n, err
	}
	if err := write(pk.Identifier(overworldDimensionName)); err != nil { // dimension (level key)
		return n, err
	}
	if err := write(pk.Long(0)); err != nil { // seed (hashed seed; 0 for the stub world)
		return n, err
	}
	if err := write(pk.Byte(gameModeSurvival)); err != nil { // gameType
		return n, err
	}
	if err := write(pk.Byte(noPreviousGameMode)); err != nil { // previousGameType (-1 == 0xFF)
		return n, err
	}
	if err := write(pk.Boolean(false)); err != nil { // isDebug
		return n, err
	}
	if err := write(pk.Boolean(true)); err != nil { // isFlat (superflat rendering)
		return n, err
	}
	if err := write(pk.Boolean(false)); err != nil { // lastDeathLocation: empty Optional
		return n, err
	}
	if err := write(pk.VarInt(0)); err != nil { // portalCooldown
		return n, err
	}
	if err := write(pk.VarInt(overworldSeaLevel)); err != nil { // seaLevel
		return n, err
	}
	return n, nil
}

// writeGameEventPacket builds ClientboundGameEvent. Wire order (jar-derived from
// ClientboundGameEventPacket.write): Byte(event.id) then Float(param). For
// LEVEL_CHUNKS_LOAD_START (id 13) the param is unused (0).
func writeGameEventPacket(eventID int, param float32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundGameEvent),
		pk.UnsignedByte(eventID),
		pk.Float(param),
	)
}

// writePlayerPositionPacket builds the proto-776 ClientboundPlayerPosition
// (Synchronize Player Position). Wire order is jar-derived from
// ClientboundPlayerPositionPacket's STREAM_CODEC composite over (VAR_INT id,
// PositionMoveRotation.STREAM_CODEC change, Relative.SET_STREAM_CODEC relatives):
//
//	VarInt  id (teleport id)
//	Double  x, Double y, Double z            (PositionMoveRotation.position)
//	Double  dx, Double dy, Double dz          (PositionMoveRotation.deltaMovement)
//	Float   yaw, Float pitch                  (PositionMoveRotation.yRot/xRot)
//	Int     relativeFlags                     (Relative.SET_STREAM_CODEC == ByteBufCodecs.INT, big-endian)
//
// This is the proto-769+ layout (teleport id FIRST, Int32 relative-flags LAST — the
// PROJECT.md "restructured Teleport / Int32 flags" known shift), confirmed against the
// 26.2 jar. relativeFlags == 0 means every component is ABSOLUTE.
func writePlayerPositionPacket(teleportID int, x, y, z float64, yaw, pitch float32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundPlayerPosition),
		pk.VarInt(int32(teleportID)),
		pk.Double(x), pk.Double(y), pk.Double(z),
		pk.Double(0), pk.Double(0), pk.Double(0), // deltaMovement (no velocity)
		pk.Float(yaw), pk.Float(pitch),
		pk.Int(0), // relative flags: 0 == all absolute
	)
}

// --- Early-Play tail builders (Plan 05-02, PLAY-01/05) ---------------------------
//
// These four packets are APPENDED to the bootstrap after Login/GameEvent/PlayerPosition
// (and still before loop.register) so a joining client becomes a real, listed player
// rather than just a camera on solid ground. All four wire layouts are jar-derived
// (decompiled from the unobfuscated 26.2 inner jar). PlayerAbilities and SetHeldSlot are
// simple/fully-confirmed; the PlayerInfoUpdate entry sub-encoding and the
// SetDefaultSpawnPosition RespawnData bytes are flagged below as Plan-05-03 capture-diff
// candidates (the FIELD COUNT/TYPES come from the jar now; the exact bytes are sealed by
// the capture-diff against a real vanilla server).

// PlayerInfoUpdate action-mask bits. The 776 ClientboundPlayerInfoUpdatePacket encodes
// its EnumSet<Action> via RegistryFriendlyByteBuf.writeEnumSet(actions, Action.class).
// The Action enum has exactly 8 values (ADD_PLAYER, INITIALIZE_CHAT, UPDATE_GAME_MODE,
// UPDATE_LISTED, UPDATE_LATENCY, UPDATE_DISPLAY_NAME, UPDATE_LIST_ORDER, UPDATE_HAT — the
// last two added vs the old 6), so writeEnumSet emits a fixed 1-byte bitset where bit i
// is set iff the i-th enum constant is present. A plain pk.Byte(mask) is byte-identical to
// that single-byte FixedBitSet (Assumption A3, sealed by the Plan-05-03 capture-diff).
const (
	piuAddPlayer      = 0x01 // bit 0: ADD_PLAYER
	piuUpdateGameMode = 0x04 // bit 2: UPDATE_GAME_MODE
	piuUpdateListed   = 0x08 // bit 3: UPDATE_LISTED
)

// playerAbilitiesFlags packs the four ability booleans into the jar-verified bit layout
// (ClientboundPlayerAbilitiesPacket.write: invulnerable=0x01, isFlying=0x02, canFly=0x04,
// instabuild=0x08). Survival default is all-false -> 0x00.
const (
	abilityInvulnerable = 0x01
	abilityFlying       = 0x02
	abilityCanFly       = 0x04
	abilityInstabuild   = 0x08
)

// defaultFlyingSpeed / defaultWalkingSpeed are the Abilities speeds for a fresh survival
// player (Abilities.getFlyingSpeed / getWalkingSpeed defaults). Sent as Floats after the
// flags byte.
const (
	defaultFlyingSpeed  = 0.05
	defaultWalkingSpeed = 0.1
)

// defaultHeldSlot is the hotbar slot the player starts holding (slot 0). Sent as a single
// VarInt by ClientboundSetHeldSlot.
const defaultHeldSlot = 0

// writePlayerAbilities builds ClientboundPlayerAbilities. Wire order is jar-derived from
// ClientboundPlayerAbilitiesPacket.write: a single Byte of flags followed by Float
// flyingSpeed and Float walkingSpeed. The survival join sends all-false flags (0x00) so
// the client is a normal, non-flying, non-creative player.
func writePlayerAbilities(invuln, flying, canFly, instabuild bool, flySpeed, walkSpeed float32) pk.Packet {
	var flags int8
	if invuln {
		flags |= abilityInvulnerable
	}
	if flying {
		flags |= abilityFlying
	}
	if canFly {
		flags |= abilityCanFly
	}
	if instabuild {
		flags |= abilityInstabuild
	}
	return pk.Marshal(
		int32(packetid.ClientboundPlayerAbilities),
		pk.Byte(flags),
		pk.Float(flySpeed),
		pk.Float(walkSpeed),
	)
}

// writeSetHeldSlot builds ClientboundSetHeldSlot — a single VarInt hotbar slot index
// (jar-verified STREAM_CODEC over ByteBufCodecs.VAR_INT).
func writeSetHeldSlot(slot int32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetHeldSlot),
		pk.VarInt(slot),
	)
}

// writePlayerInfoUpdateAdd builds a single-entry ClientboundPlayerInfoUpdate that adds the
// joining player to its OWN tab list (PLAY-05). Wire order is jar-derived from
// ClientboundPlayerInfoUpdatePacket.write:
//
//	Byte                 actions        (writeEnumSet over the 8-action enum -> 1-byte mask)
//	playerInfoEntriesEncoder            (writeCollection: VarInt(count) + per-entry body)
//
// The self entry uses the minimal listed set ADD_PLAYER|UPDATE_GAME_MODE|UPDATE_LISTED
// (0x0D). Within the entry the body is UUID first, then each present action's writer runs
// in ENUM ORDER (ADD_PLAYER before UPDATE_GAME_MODE before UPDATE_LISTED). The ADD_PLAYER
// property list (GAME_PROFILE_PROPERTIES) is a count-prefixed list; the offline server
// sends 0 properties. The entry sub-encoding (property-list shape, listed Boolean) is a
// Plan-05-03 capture-diff candidate — the framing here matches the jar; the exact bytes
// are sealed by the capture-diff.
func writePlayerInfoUpdateAdd(id uuid.UUID, name string, gameMode int32) pk.Packet {
	mask := int8(piuAddPlayer | piuUpdateGameMode | piuUpdateListed)
	return pk.Marshal(
		int32(packetid.ClientboundPlayerInfoUpdate),
		pk.Byte(mask),
		playerInfoEntriesEncoder{id: id, name: name, gameMode: gameMode},
	)
}

// playerInfoEntriesEncoder writes the PlayerInfoUpdate entries collection for the single
// self-entry: VarInt(1) count, then the entry. The entry begins with the profile UUID,
// then the present actions are written in enum order:
//
//	[ADD_PLAYER]       String name, VarInt(0) property count (no signed properties offline)
//	[UPDATE_GAME_MODE] VarInt gameMode (GameType.getId)
//	[UPDATE_LISTED]    Boolean listed (true — the player appears in its own list)
//
// CAPTURE-DIFF CANDIDATE (Plan 05-03): the property-list sub-encoding and the listed
// Boolean are pinned to the jar's framing here; the capture-diff seals the exact bytes.
type playerInfoEntriesEncoder struct {
	id       uuid.UUID
	name     string
	gameMode int32
}

func (e playerInfoEntriesEncoder) WriteTo(w io.Writer) (int64, error) {
	var n int64
	write := func(f pk.FieldEncoder) error {
		m, err := f.WriteTo(w)
		n += m
		return err
	}
	if err := write(pk.VarInt(1)); err != nil { // entry count
		return n, err
	}
	if err := write(pk.UUID(e.id)); err != nil { // profileId (UUID, 16 raw bytes)
		return n, err
	}
	// ADD_PLAYER: profile name + property count (0 properties offline).
	if err := write(pk.String(e.name)); err != nil {
		return n, err
	}
	if err := write(pk.VarInt(0)); err != nil { // GAME_PROFILE_PROPERTIES count
		return n, err
	}
	// UPDATE_GAME_MODE: GameType.getId as VarInt.
	if err := write(pk.VarInt(e.gameMode)); err != nil {
		return n, err
	}
	// UPDATE_LISTED: listed Boolean.
	if err := write(pk.Boolean(true)); err != nil {
		return n, err
	}
	return n, nil
}

// writePlayerInfoUpdateRemove builds a ClientboundPlayerInfoRemove that drops the given
// players' tab-list entries (GAMEPLAY-01 leave broadcast, Plan 17-01). It is a SEPARATE packet
// id from ClientboundPlayerInfoUpdate.
//
// Wire layout is jar-derived from net.minecraft.network.protocol.game.
// ClientboundPlayerInfoRemovePacket.write (decompiled this session): a single
// writeCollection(profileIds, UUIDUtil.STREAM_CODEC) — i.e. a VarInt count followed by that
// many raw 16-byte UUIDs, no per-entry framing. pk.UUID writes the raw 16 bytes, matching
// UUIDUtil.STREAM_CODEC.
func writePlayerInfoUpdateRemove(ids ...uuid.UUID) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundPlayerInfoRemove),
		playerInfoRemoveEncoder{ids: ids},
	)
}

// playerInfoRemoveEncoder writes the ClientboundPlayerInfoRemove body: VarInt(count) + raw
// UUIDs (UUIDUtil.STREAM_CODEC, 16 bytes each). Mirrors playerInfoEntriesEncoder's
// custom-FieldEncoder style.
type playerInfoRemoveEncoder struct {
	ids []uuid.UUID
}

func (e playerInfoRemoveEncoder) WriteTo(w io.Writer) (int64, error) {
	var n int64
	write := func(f pk.FieldEncoder) error {
		m, err := f.WriteTo(w)
		n += m
		return err
	}
	if err := write(pk.VarInt(len(e.ids))); err != nil { // profileIds count
		return n, err
	}
	for _, id := range e.ids {
		if err := write(pk.UUID(id)); err != nil { // raw 16-byte UUID
			return n, err
		}
	}
	return n, nil
}

// writeSetDefaultSpawnPosition builds ClientboundSetDefaultSpawnPosition. The 26.x packet
// wraps a LevelData.RespawnData record whose STREAM_CODEC is composite(GlobalPos, FLOAT,
// FLOAT), and GlobalPos is composite(ResourceKey<Level> dimension, BlockPos). On the wire
// that is, in order:
//
//	Identifier dimension   (ResourceKey<Level>.streamCodec == a plain ResourceLocation)
//	Long       blockPos    (BlockPos.STREAM_CODEC == the packed x/z/y long; pk.Position)
//	Float      yaw
//	Float      pitch
//
// CAPTURE-DIFF CANDIDATE (Plan 05-03): the RespawnData/GlobalPos restructure is new in
// 26.x; the FIELD COUNT/TYPES are jar-confirmed here, the exact bytes are sealed by the
// capture-diff against a real vanilla 26.2 server. Its absence likely does not kick (A5);
// it is sent for compass/respawn correctness.
func writeSetDefaultSpawnPosition(dimension string, pos pk.Position, yaw, pitch float32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetDefaultSpawnPosition),
		respawnDataEncoder{dimension: dimension, pos: pos, yaw: yaw, pitch: pitch},
	)
}

// respawnDataEncoder writes the nested RespawnData record (GlobalPos + yaw + pitch),
// mirroring commonPlayerSpawnInfoEncoder's custom-FieldEncoder style. GlobalPos is the
// dimension Identifier followed by the packed BlockPos long.
type respawnDataEncoder struct {
	dimension string
	pos       pk.Position
	yaw       float32
	pitch     float32
}

func (e respawnDataEncoder) WriteTo(w io.Writer) (int64, error) {
	var n int64
	write := func(f pk.FieldEncoder) error {
		m, err := f.WriteTo(w)
		n += m
		return err
	}
	if err := write(pk.Identifier(e.dimension)); err != nil { // GlobalPos.dimension
		return n, err
	}
	if err := write(e.pos); err != nil { // GlobalPos.pos (packed BlockPos long)
		return n, err
	}
	if err := write(pk.Float(e.yaw)); err != nil {
		return n, err
	}
	if err := write(pk.Float(e.pitch)); err != nil {
		return n, err
	}
	return n, nil
}

// bootstrapParams carries the per-player identity + issued teleport id into the Play
// bootstrap. AcceptPlayer fills it from the login profile (name/id) and the per-gameTick
// teleport-id counter, so the bootstrap can build a real, listed player rather than the
// const placeholders. teleportID is the SAME id stored in tickPlayer.awaitingTeleport, so
// the Plan-05-01 gate matches the client's Confirm Teleportation echo (PLAY-02).
type bootstrapParams struct {
	name       string
	id         uuid.UUID
	teleportID int
	gameMode   int32
	// entityID is the player's server-issued entity id, claimed off-tick by AcceptPlayer
	// from the tick's EntityIDAllocator (ENT-01) and used as the ClientboundLogin playerId
	// — the replacement for the hard-coded joinEntityID=1 (06-RESEARCH Pitfall 7).
	entityID int32

	// spawnX, spawnY, spawnZ + hasSpawn carry the GAMEPLAY-02 persisted spawn position (Plan
	// 17-01). When hasSpawn is true, sendPlayBootstrap teleports the SINGLE bootstrap
	// PlayerPosition to these coords (and points SetDefaultSpawnPosition at them) instead of the
	// center-derived hardcoded spawn — so a reconnecting player lands at its saved location with
	// the same teleport id (no second teleport, Pitfall 3). hasSpawn=false keeps the v1 spawn.
	spawnX, spawnY, spawnZ float64
	hasSpawn               bool
}

// sendPlayBootstrap enqueues the full early-Play bootstrap on the connection's outbound
// queue IN ORDER: Login -> GameEvent -> PlayerPosition (the original three) followed by
// the Plan-05-02 tail PlayerAbilities -> SetHeldSlot -> PlayerInfoUpdate(self) ->
// SetDefaultSpawnPosition. It is called by AcceptPlayer BEFORE loop.register so the single
// writeLoop drains them (FIFO) ahead of any chunk packet the tick later enqueues for this
// player — the load-bearing invariant that Login (ClientLevel creation) precedes
// SetChunkCacheCenter/LevelChunkWithLight, now extended so the entire tail also precedes
// the chunk stream.
//
// The player is placed at chunk (0,0), block center (8.5, surfaceY+2, 8.5), so it stands
// two blocks above the superflat stone surface rather than inside it or in the void.
// viewDist is the server-clamped value the streamer uses, reported as the Login chunkRadius
// so the client's view window matches what the tick streams. The PlayerPosition carries the
// issued incrementing teleport id (params.teleportID), not the const placeholder.
//
// Sending here mutates NO tick-owned state — it only moves bytes through the connection's
// own bounded queue (exactly like the original three packets) — so the single-owner tick
// discipline (TICK-05) is intact.
func sendPlayBootstrap(c *Client, viewDist int, center level.ChunkPos, surfaceY int, params bootstrapParams) {
	// Block center of the player's spawn column: the middle of chunk (center) at two
	// blocks above the solid surface. center is {0,0} for Phase 4, so this is (8.5,
	// surfaceY+2, 8.5).
	spawnX := float64(int(center[0])<<4) + 8.5
	spawnZ := float64(int(center[1])<<4) + 8.5
	spawnY := float64(surfaceY + 2)

	// GAMEPLAY-02 (Plan 17-01): a reconnecting player's persisted position overrides the
	// hardcoded spawn for the SINGLE bootstrap teleport (and the default spawn point), so it
	// lands at its saved location with the same teleport id — no second post-register teleport.
	if params.hasSpawn {
		spawnX, spawnY, spawnZ = params.spawnX, params.spawnY, params.spawnZ
	}

	// The original three (Login -> GameEvent -> PlayerPosition). The Login playerId is the
	// per-join allocated entity id (ENT-01), not the old const.
	c.Send(writeLoginPacket(params.entityID, viewDist))
	c.Send(writeGameEventPacket(gameEventLevelChunksLoadStart, 0))
	c.Send(writePlayerPositionPacket(params.teleportID, spawnX, spawnY, spawnZ, 0, 0))

	// The Plan-05-02 early-Play tail, appended after PlayerPosition and still before
	// register. The block spawn position fed to SetDefaultSpawnPosition is the integer block
	// under the player's spawn position (the persisted block when reconnecting — GAMEPLAY-02 —
	// else the spawn column floor).
	spawnPos := pk.Position{X: int(center[0]) << 4, Y: surfaceY, Z: int(center[1]) << 4}
	if params.hasSpawn {
		spawnPos = pk.Position{X: int(math.Floor(spawnX)), Y: int(math.Floor(spawnY)), Z: int(math.Floor(spawnZ))}
	}
	c.Send(writePlayerAbilities(false, false, false, false, defaultFlyingSpeed, defaultWalkingSpeed))
	c.Send(writeSetHeldSlot(defaultHeldSlot))
	c.Send(writePlayerInfoUpdateAdd(params.id, params.name, params.gameMode))
	c.Send(writeSetDefaultSpawnPosition(overworldDimensionName, spawnPos, 0, 0))
}
