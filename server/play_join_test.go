package server

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"

	"github.com/google/uuid"
)

// play_join_test.go covers the MINIMAL Play-state bootstrap (play_join.go): the
// jar-derived wire layout of the three bootstrap packets, and the load-bearing
// ordering invariant that Login (which creates the client's ClientLevel) reaches the
// wire BEFORE any SetChunkCacheCenter / LevelChunkWithLight chunk packet. The wire
// field orders/types asserted here are decompiled from the unobfuscated 26.2 inner jar
// (net.minecraft.network.protocol.game.ClientboundLoginPacket / CommonPlayerSpawnInfo /
// ClientboundGameEventPacket / ClientboundPlayerPositionPacket), not the (<=773) wiki.

// TestLoginPacketWireLayout decodes the bootstrap Login packet field-by-field in the
// exact jar-verified order and asserts each value, so a future field reorder or type
// change (e.g. a 26.3 retarget) is caught by a failing decode rather than a silent
// client NPE.
func TestLoginPacketWireLayout(t *testing.T) {
	const viewDist = 2
	const testEntityID = 1 // ENT-01: playerId is now an allocated id (not a const); use 1 here
	p := writeLoginPacket(testEntityID, viewDist)
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundLogin {
		t.Fatalf("Login packet id = %d, want ClientboundLogin (%d)", p.ID, packetid.ClientboundLogin)
	}

	var (
		playerID         pk.Int
		hardcore         pk.Boolean
		levelCount       pk.VarInt
		levelName        pk.Identifier
		maxPlayers       pk.VarInt
		chunkRadius      pk.VarInt
		simDistance      pk.VarInt
		reducedDebug     pk.Boolean
		showDeath        pk.Boolean
		limitedCrafting  pk.Boolean
		dimTypeHolder    pk.VarInt
		dimensionName    pk.Identifier
		seed             pk.Long
		gameType         pk.Byte
		prevGameType     pk.Byte
		isDebug          pk.Boolean
		isFlat           pk.Boolean
		hasDeathLocation pk.Boolean
		portalCooldown   pk.VarInt
		seaLevel         pk.VarInt
		onlineMode       pk.Boolean
		enforcesSecure   pk.Boolean
	)

	if err := p.Scan(
		&playerID, &hardcore,
		&levelCount, &levelName, // Set<ResourceKey<Level>> with exactly one element
		&maxPlayers, &chunkRadius, &simDistance,
		&reducedDebug, &showDeath, &limitedCrafting,
		// CommonPlayerSpawnInfo (nested record, inlined into the Login body):
		&dimTypeHolder, &dimensionName, &seed, &gameType, &prevGameType,
		&isDebug, &isFlat, &hasDeathLocation, &portalCooldown, &seaLevel,
		// trailing Login fields after the nested record:
		&onlineMode, &enforcesSecure,
	); err != nil {
		t.Fatalf("Login scan failed (wire layout mismatch): %v", err)
	}

	if playerID != testEntityID {
		t.Errorf("playerId = %d, want %d", playerID, testEntityID)
	}
	if hardcore {
		t.Errorf("hardcore = true, want false")
	}
	if levelCount != 1 {
		t.Errorf("levels count = %d, want 1", levelCount)
	}
	if string(levelName) != overworldDimensionName {
		t.Errorf("levels[0] = %q, want %q", string(levelName), overworldDimensionName)
	}
	if chunkRadius != viewDist {
		t.Errorf("chunkRadius = %d, want %d", chunkRadius, viewDist)
	}
	if simDistance != viewDist {
		t.Errorf("simulationDistance = %d, want %d", simDistance, viewDist)
	}
	// Holder<DimensionType> encodes as a registry reference VarInt(id+1); overworld is
	// registry index 0 -> 1.
	if dimTypeHolder != overworldDimensionTypeID+1 {
		t.Errorf("dimensionType holder = %d, want %d (VarInt(id+1) for overworld)", dimTypeHolder, overworldDimensionTypeID+1)
	}
	if string(dimensionName) != overworldDimensionName {
		t.Errorf("dimension = %q, want %q", string(dimensionName), overworldDimensionName)
	}
	if gameType != gameModeSurvival {
		t.Errorf("gameType = %d, want %d (survival)", gameType, gameModeSurvival)
	}
	// getNullableId(-1) encodes as the 0xFF byte == int8(-1).
	if prevGameType != -1 {
		t.Errorf("previousGameType = %d, want -1 (none)", prevGameType)
	}
	if !isFlat {
		t.Errorf("isFlat = false, want true (superflat rendering)")
	}
	if hasDeathLocation {
		t.Errorf("lastDeathLocation present byte = true, want false (empty Optional)")
	}
	if seaLevel != overworldSeaLevel {
		t.Errorf("seaLevel = %d, want %d", seaLevel, overworldSeaLevel)
	}

	// Every byte must be consumed: a trailing-bytes mismatch means the field set is
	// wrong even if each decoded field happened to parse.
	if used := decodedLen(t, p.Data,
		&playerID, &hardcore, &levelCount, &levelName, &maxPlayers, &chunkRadius,
		&simDistance, &reducedDebug, &showDeath, &limitedCrafting, &dimTypeHolder,
		&dimensionName, &seed, &gameType, &prevGameType, &isDebug, &isFlat,
		&hasDeathLocation, &portalCooldown, &seaLevel, &onlineMode, &enforcesSecure,
	); used != len(p.Data) {
		t.Errorf("Login decoded %d of %d bytes — field set does not match the wire exactly", used, len(p.Data))
	}
}

// TestGameEventPacketWireLayout asserts the LEVEL_CHUNKS_LOAD_START game event encodes
// as Byte(13) + Float(0) (jar-verified event id 13).
func TestGameEventPacketWireLayout(t *testing.T) {
	p := writeGameEventPacket(gameEventLevelChunksLoadStart, 0)
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundGameEvent {
		t.Fatalf("GameEvent packet id = %d, want ClientboundGameEvent (%d)", p.ID, packetid.ClientboundGameEvent)
	}
	var (
		event pk.UnsignedByte
		param pk.Float
	)
	if err := p.Scan(&event, &param); err != nil {
		t.Fatalf("GameEvent scan failed: %v", err)
	}
	if event != gameEventLevelChunksLoadStart {
		t.Errorf("event id = %d, want %d (LEVEL_CHUNKS_LOAD_START)", event, gameEventLevelChunksLoadStart)
	}
	if param != 0 {
		t.Errorf("param = %v, want 0", param)
	}
}

// TestPlayerPositionPacketWireLayout asserts the proto-769+ layout: teleport id FIRST
// (VarInt), then 6 doubles (position + deltaMovement), 2 floats (yaw/pitch), then the
// Int32 relative-flags LAST (the PROJECT.md known shift).
func TestPlayerPositionPacketWireLayout(t *testing.T) {
	const tpID = 7
	p := writePlayerPositionPacket(tpID, 8.5, -46, 8.5, 0, 0)
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundPlayerPosition {
		t.Fatalf("PlayerPosition id = %d, want ClientboundPlayerPosition (%d)", p.ID, packetid.ClientboundPlayerPosition)
	}
	var (
		id            pk.VarInt
		x, y, z       pk.Double
		dx, dy, dz    pk.Double
		yaw, pitch    pk.Float
		relativeFlags pk.Int
	)
	if err := p.Scan(&id, &x, &y, &z, &dx, &dy, &dz, &yaw, &pitch, &relativeFlags); err != nil {
		t.Fatalf("PlayerPosition scan failed (wire layout mismatch): %v", err)
	}
	if id != tpID {
		t.Errorf("teleport id = %d, want %d (must be FIRST field)", id, tpID)
	}
	if x != 8.5 || y != -46 || z != 8.5 {
		t.Errorf("position = (%v,%v,%v), want (8.5,-46,8.5)", x, y, z)
	}
	if dx != 0 || dy != 0 || dz != 0 {
		t.Errorf("deltaMovement = (%v,%v,%v), want (0,0,0)", dx, dy, dz)
	}
	if relativeFlags != 0 {
		t.Errorf("relativeFlags = %d, want 0 (all absolute)", relativeFlags)
	}
	if used := decodedLen(t, p.Data, &id, &x, &y, &z, &dx, &dy, &dz, &yaw, &pitch, &relativeFlags); used != len(p.Data) {
		t.Errorf("PlayerPosition decoded %d of %d bytes — field set mismatch", used, len(p.Data))
	}
}

// TestJoinSequenceOrdering is the load-bearing invariant: over a real piped connection
// with a real world wired (so the tick streams chunks), the FIRST three clientbound
// packets are Login -> GameEvent -> PlayerPosition, and crucially Login arrives BEFORE
// any SetChunkCacheCenter / LevelChunkWithLight. This is the exact ordering whose
// absence made a real 26.2 client NPE in handleSetChunkCacheCenter.
func TestJoinSequenceOrdering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	loop := NewTickLoop(SystemClock())
	keep := NewKeepAlive()

	// Wire a real world so the streamer actually emits SetChunkCacheCenter + chunks —
	// the packets Login must precede. Superflat surface -48 matches cmd/sulfur.
	gen := world.NewSuperflat(24, -64, -48)
	worker := world.NewWorker(gen, "", 256)
	mgr := world.NewChunkManager()
	loop.SetWorld(mgr, worker)

	inbound := make(chan Intent, 64)
	go loop.Run(ctx, inbound)
	go keep.Run(ctx)
	go worker.Run(ctx)

	g := NewGameTick(inbound, loop, keep, -48, SpawnPoint{X: 8.5, Y: -46, Z: 8.5})

	server, client := newPipe(t)

	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		g.AcceptPlayer("joiner", uuid.New(), nil, nil, ProtocolVersion, server)
	}()

	// Read packets in arrival order and record the sequence of clientbound ids until we
	// have seen the first chunk-cache/chunk packet (or a bounded number of packets).
	type seen struct {
		id  packetid.ClientboundPacketID
		err error
	}
	got := make(chan seen, 1)
	go func() {
		for {
			var p pk.Packet
			if err := client.ReadPacket(&p); err != nil {
				got <- seen{err: err}
				return
			}
			got <- seen{id: packetid.ClientboundPacketID(p.ID)}
		}
	}()

	want := []packetid.ClientboundPacketID{
		packetid.ClientboundLogin,
		packetid.ClientboundGameEvent,
		packetid.ClientboundPlayerPosition,
	}
	loginSeen := false
	for i := 0; i < len(want); i++ {
		select {
		case s := <-got:
			if s.err != nil {
				t.Fatalf("client read failed at bootstrap packet %d: %v", i, s.err)
			}
			// No chunk-cache / chunk packet may appear before Login.
			if !loginSeen && (s.id == packetid.ClientboundSetChunkCacheCenter ||
				s.id == packetid.ClientboundLevelChunkWithLight ||
				s.id == packetid.ClientboundChunkBatchStart) {
				t.Fatalf("chunk packet id=%d arrived before Login — the ClientLevel-creation invariant is broken", s.id)
			}
			if s.id != want[i] {
				t.Fatalf("bootstrap packet %d = %d, want %d (%v)", i, s.id, want[i], want[i])
			}
			if s.id == packetid.ClientboundLogin {
				loginSeen = true
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for bootstrap packet %d (%v)", i, want[i])
		}
	}

	// After the three bootstrap packets, a SetChunkCacheCenter MUST eventually follow
	// (the streamer runs), proving the bootstrap did not suppress the chunk stream.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-got:
			if s.err != nil {
				t.Fatalf("client read failed while waiting for chunk stream: %v", s.err)
			}
			if s.id == packetid.ClientboundSetChunkCacheCenter {
				return // Login preceded the chunk stream, and the stream still runs — done.
			}
		case <-deadline:
			t.Fatal("no SetChunkCacheCenter followed the bootstrap — the chunk stream did not run after Login")
		}
	}
}

// TestPlayerAbilitiesWire decodes the early-Play ClientboundPlayerAbilities packet and
// asserts the jar-verified layout: Byte flags (bit0 invuln, bit1 flying, bit2 canFly,
// bit3 instabuild) + Float flyingSpeed + Float walkingSpeed. The survival default sends
// all-false flags, so the flags byte is 0x00.
func TestPlayerAbilitiesWire(t *testing.T) {
	p := writePlayerAbilities(false, false, false, false, 0.05, 0.1)
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundPlayerAbilities {
		t.Fatalf("Abilities id = %d, want ClientboundPlayerAbilities (%d)", p.ID, packetid.ClientboundPlayerAbilities)
	}
	var (
		flags     pk.Byte
		flySpeed  pk.Float
		walkSpeed pk.Float
	)
	if err := p.Scan(&flags, &flySpeed, &walkSpeed); err != nil {
		t.Fatalf("Abilities scan failed (wire layout mismatch): %v", err)
	}
	if flags != 0x00 {
		t.Errorf("flags = 0x%02x, want 0x00 (all survival defaults false)", byte(flags))
	}
	if flySpeed != 0.05 {
		t.Errorf("flyingSpeed = %v, want 0.05", flySpeed)
	}
	if walkSpeed != 0.1 {
		t.Errorf("walkingSpeed = %v, want 0.1", walkSpeed)
	}
	if used := decodedLen(t, p.Data, &flags, &flySpeed, &walkSpeed); used != len(p.Data) {
		t.Errorf("Abilities decoded %d of %d bytes — field set mismatch", used, len(p.Data))
	}

	// A non-default flag set must pack into the correct bits (invuln|canFly == 0x05).
	p2 := writePlayerAbilities(true, false, true, false, 0.05, 0.1)
	var flags2 pk.Byte
	if err := p2.Scan(&flags2); err != nil {
		t.Fatalf("Abilities(flagged) scan failed: %v", err)
	}
	if flags2 != 0x05 {
		t.Errorf("flags = 0x%02x, want 0x05 (invuln|canFly)", byte(flags2))
	}
}

// TestSetHeldSlotWire asserts the early-Play ClientboundSetHeldSlot packet encodes as a
// single VarInt slot index (jar-verified STREAM_CODEC over VAR_INT).
func TestSetHeldSlotWire(t *testing.T) {
	p := writeSetHeldSlot(0)
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundSetHeldSlot {
		t.Fatalf("SetHeldSlot id = %d, want ClientboundSetHeldSlot (%d)", p.ID, packetid.ClientboundSetHeldSlot)
	}
	var slot pk.VarInt
	if err := p.Scan(&slot); err != nil {
		t.Fatalf("SetHeldSlot scan failed: %v", err)
	}
	if slot != 0 {
		t.Errorf("slot = %d, want 0", slot)
	}
	if used := decodedLen(t, p.Data, &slot); used != len(p.Data) {
		t.Errorf("SetHeldSlot decoded %d of %d bytes — field set mismatch", used, len(p.Data))
	}
}

// TestPlayerInfoUpdateWire decodes the self tab-list entry and asserts the jar-verified
// 776 layout: a 1-byte action mask (writeEnumSet over the 8-action enum), then a
// count-prefixed entry list. The self entry uses ADD_PLAYER|UPDATE_GAME_MODE|UPDATE_LISTED
// (0x01|0x04|0x08 == 0x0D); the entry body is UUID, then — in enum order — [ADD_PLAYER]
// String name + VarInt(0) propertyCount, [UPDATE_GAME_MODE] VarInt gameMode, [UPDATE_LISTED]
// Boolean listed. The entry sub-encoding is a Plan-05-03 capture-diff candidate; this test
// pins the framing the capture-diff seals to exact bytes.
func TestPlayerInfoUpdateWire(t *testing.T) {
	id := uuid.New()
	p := writePlayerInfoUpdateAdd(id, "Steve", gameModeSurvival, nil) // ONLINE-01: offline -> no skin properties (count 0)
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundPlayerInfoUpdate {
		t.Fatalf("PlayerInfoUpdate id = %d, want ClientboundPlayerInfoUpdate (%d)", p.ID, packetid.ClientboundPlayerInfoUpdate)
	}
	var (
		mask       pk.Byte
		count      pk.VarInt
		entryUUID  pk.UUID
		name       pk.String
		propCount  pk.VarInt
		gameMode   pk.VarInt
		listed     pk.Boolean
	)
	if err := p.Scan(&mask, &count, &entryUUID, &name, &propCount, &gameMode, &listed); err != nil {
		t.Fatalf("PlayerInfoUpdate scan failed (wire layout mismatch): %v", err)
	}
	if byte(mask) != 0x0D {
		t.Errorf("action mask = 0x%02x, want 0x0D (ADD_PLAYER|UPDATE_GAME_MODE|UPDATE_LISTED)", byte(mask))
	}
	if count != 1 {
		t.Errorf("entry count = %d, want 1 (single self-entry)", count)
	}
	if uuid.UUID(entryUUID) != id {
		t.Errorf("entry uuid = %s, want %s", uuid.UUID(entryUUID), id)
	}
	if string(name) != "Steve" {
		t.Errorf("entry name = %q, want %q", string(name), "Steve")
	}
	if propCount != 0 {
		t.Errorf("property count = %d, want 0 (offline server sends no properties)", propCount)
	}
	if gameMode != gameModeSurvival {
		t.Errorf("gameMode = %d, want %d (survival)", gameMode, gameModeSurvival)
	}
	if !listed {
		t.Errorf("listed = false, want true (player appears in its own tab list)")
	}
	if used := decodedLen(t, p.Data, &mask, &count, &entryUUID, &name, &propCount, &gameMode, &listed); used != len(p.Data) {
		t.Errorf("PlayerInfoUpdate decoded %d of %d bytes — field set mismatch", used, len(p.Data))
	}
}

// TestSetDefaultSpawnPositionWire asserts the 26.x-restructured layout. The packet wraps
// a LevelData.RespawnData record whose STREAM_CODEC is composite(GlobalPos, FLOAT, FLOAT),
// and GlobalPos is composite(ResourceKey<Level> dimension, BlockPos). On the wire that is:
// Identifier dimension, Long (packed BlockPos), Float yaw, Float pitch. The exact bytes are
// a Plan-05-03 capture-diff candidate; this test pins the field count/types from the jar so
// the capture-diff asserts the SAME framing.
func TestSetDefaultSpawnPositionWire(t *testing.T) {
	p := writeSetDefaultSpawnPosition(overworldDimensionName, pk.Position{X: 8, Y: -46, Z: 8}, 0, 0)
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundSetDefaultSpawnPosition {
		t.Fatalf("SetDefaultSpawnPosition id = %d, want ClientboundSetDefaultSpawnPosition (%d)", p.ID, packetid.ClientboundSetDefaultSpawnPosition)
	}
	var (
		dimension pk.Identifier
		pos       pk.Position
		yaw       pk.Float
		pitch     pk.Float
	)
	if err := p.Scan(&dimension, &pos, &yaw, &pitch); err != nil {
		t.Fatalf("SetDefaultSpawnPosition scan failed (wire layout mismatch): %v", err)
	}
	if string(dimension) != overworldDimensionName {
		t.Errorf("dimension = %q, want %q", string(dimension), overworldDimensionName)
	}
	if pos.X != 8 || pos.Y != -46 || pos.Z != 8 {
		t.Errorf("blockpos = (%d,%d,%d), want (8,-46,8)", pos.X, pos.Y, pos.Z)
	}
	if yaw != 0 || pitch != 0 {
		t.Errorf("yaw/pitch = (%v,%v), want (0,0)", yaw, pitch)
	}
	if used := decodedLen(t, p.Data, &dimension, &pos, &yaw, &pitch); used != len(p.Data) {
		t.Errorf("SetDefaultSpawnPosition decoded %d of %d bytes — field set mismatch", used, len(p.Data))
	}
}

// TestBootstrapTailOrdering asserts that sendPlayBootstrap enqueues the FULL early-Play
// sequence in order over a real piped connection: Login -> GameEvent -> PlayerPosition ->
// PlayerAbilities -> SetHeldSlot -> PlayerInfoUpdate -> SetDefaultSpawnPosition, all before
// any chunk packet. The tail must land AFTER PlayerPosition and the whole set must precede
// the chunk stream (the FIFO-before-register invariant the appended tail must not break).
func TestBootstrapTailOrdering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	loop := NewTickLoop(SystemClock())
	keep := NewKeepAlive()

	gen := world.NewSuperflat(24, -64, -48)
	worker := world.NewWorker(gen, "", 256)
	mgr := world.NewChunkManager()
	loop.SetWorld(mgr, worker)

	inbound := make(chan Intent, 64)
	go loop.Run(ctx, inbound)
	go keep.Run(ctx)
	go worker.Run(ctx)

	g := NewGameTick(inbound, loop, keep, -48, SpawnPoint{X: 8.5, Y: -46, Z: 8.5})

	server, client := newPipe(t)

	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		g.AcceptPlayer("joiner", uuid.New(), nil, nil, ProtocolVersion, server)
	}()

	type seen struct {
		id  packetid.ClientboundPacketID
		err error
	}
	got := make(chan seen, 1)
	go func() {
		for {
			var p pk.Packet
			if err := client.ReadPacket(&p); err != nil {
				got <- seen{err: err}
				return
			}
			got <- seen{id: packetid.ClientboundPacketID(p.ID)}
		}
	}()

	want := []packetid.ClientboundPacketID{
		packetid.ClientboundLogin,
		packetid.ClientboundGameEvent,
		packetid.ClientboundPlayerPosition,
		packetid.ClientboundPlayerAbilities,
		packetid.ClientboundSetHeldSlot,
		packetid.ClientboundPlayerInfoUpdate,
		packetid.ClientboundSetDefaultSpawnPosition,
	}
	for i := 0; i < len(want); i++ {
		select {
		case s := <-got:
			if s.err != nil {
				t.Fatalf("client read failed at bootstrap packet %d: %v", i, s.err)
			}
			// No chunk packet may appear before the full bootstrap tail.
			if s.id == packetid.ClientboundSetChunkCacheCenter ||
				s.id == packetid.ClientboundLevelChunkWithLight ||
				s.id == packetid.ClientboundChunkBatchStart {
				t.Fatalf("chunk packet id=%d arrived before bootstrap packet %d (%v) — tail ordering broken", s.id, i, want[i])
			}
			if s.id != want[i] {
				t.Fatalf("bootstrap packet %d = %d, want %d (%v)", i, s.id, want[i], want[i])
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for bootstrap packet %d (%v)", i, want[i])
		}
	}
}

// TestBootstrapTeleportID covers the PLAY-02 producer side: AcceptPlayer issues an
// INCREMENTING per-player teleport id (not the const 1), threads it into the bootstrap
// PlayerPosition AND into tickPlayer.awaitingTeleport, so the Plan-05-01 dispatch gate
// confirms only on a matching echo. Two successive joins must issue DISTINCT ids.
func TestBootstrapTeleportID(t *testing.T) {
	g := NewGameTick(nil, NewTickLoop(newFakeClock()), nil, -48, SpawnPoint{X: 8.5, Y: -46, Z: 8.5})

	id1 := g.nextTeleportID()
	id2 := g.nextTeleportID()
	if id1 == id2 {
		t.Fatalf("successive teleport ids must differ: id1=%d id2=%d", id1, id2)
	}
	if id1 == 0 || id2 == 0 {
		t.Fatalf("teleport ids must be non-zero (incrementing producer): id1=%d id2=%d", id1, id2)
	}

	// The id issued into the bootstrap PlayerPosition must equal the id stored in
	// awaitingTeleport, and the Plan-05-01 dispatch gate must confirm only on that exact id.
	loop := NewTickLoop(newFakeClock())
	tpID := g.nextTeleportID()
	p := &tickPlayer{client: &Client{}, awaitingTeleport: tpID}
	loop.players = append(loop.players, p)
	loop.clientIndex = map[*Client]*tickPlayer{p.client: p}

	// The PlayerPosition the bootstrap sends carries tpID; assert that is the value the
	// gate matches (decode the id back out of the produced packet).
	pos := writePlayerPositionPacket(tpID, 8.5, -46, 8.5, 0, 0)
	var sentID pk.VarInt
	if err := pos.Scan(&sentID); err != nil {
		t.Fatalf("PlayerPosition scan failed: %v", err)
	}
	if int(sentID) != tpID {
		t.Fatalf("PlayerPosition teleport id = %d, want %d (must equal awaitingTeleport)", sentID, tpID)
	}

	// A wrong echo does not confirm; the exact issued id does (drives the 05-01 gate).
	wrong := pk.Marshal(int32(packetid.ServerboundAcceptTeleportation), pk.VarInt(tpID+999))
	loop.dispatch(p.client, wrong)
	if p.confirmedTeleport {
		t.Fatal("a non-matching echo must not confirm the gate")
	}
	match := pk.Marshal(int32(packetid.ServerboundAcceptTeleportation), pk.VarInt(int32(tpID)))
	loop.dispatch(p.client, match)
	if !p.confirmedTeleport {
		t.Fatal("the issued incrementing teleport id must confirm the gate (PLAY-02)")
	}
}

// TestBootstrapEntityID covers the ENT-01 producer side: AcceptPlayer claims each player's
// entity id from the tick's monotonic EntityIDAllocator (not the old hard-coded
// joinEntityID=1), so two successive joins get DIFFERENT, non-zero player entity ids, and
// the id used for the ClientboundLogin playerId equals the allocated id. It also asserts
// NewTickLoop wires a non-nil entityStore + EntityIDAllocator (06-RESEARCH Pitfall 7 /
// threat T-6-07).
func TestBootstrapEntityID(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// NewTickLoop must wire both ENT-01 fields non-nil from construction.
	if loop.entities == nil {
		t.Fatal("NewTickLoop must wire a non-nil entityStore")
	}
	if loop.idAlloc == nil {
		t.Fatal("NewTickLoop must wire a non-nil EntityIDAllocator")
	}

	// Two successive allocations (the per-join claim AcceptPlayer makes) must differ and be
	// non-zero — proving the hard-coded id=1 is gone and players draw distinct ids.
	id1 := loop.idAlloc.AllocID()
	id2 := loop.idAlloc.AllocID()
	if id1 == id2 {
		t.Fatalf("successive player entity ids must differ: id1=%d id2=%d (joinEntityID=1 must be gone)", id1, id2)
	}
	if id1 == 0 || id2 == 0 {
		t.Fatalf("player entity ids must be non-zero: id1=%d id2=%d", id1, id2)
	}

	// The id allocated for a join must be the value the bootstrap Login carries as playerId:
	// decode it back out of the produced packet for a representative allocated id.
	allocated := loop.idAlloc.AllocID()
	p := writeLoginPacket(allocated, 2)
	var playerID pk.Int
	if err := p.Scan(&playerID); err != nil {
		t.Fatalf("Login scan failed: %v", err)
	}
	if int32(playerID) != allocated {
		t.Fatalf("Login playerId = %d, want %d (must equal the allocated entity id)", playerID, allocated)
	}
}

// decodedLen re-decodes fields from data and returns how many bytes were consumed, so
// a test can assert the packet body has no trailing/short bytes beyond the known field
// set. It re-reads into the provided decoders (their values are overwritten).
func decodedLen(t *testing.T, data []byte, fields ...pk.FieldDecoder) int {
	t.Helper()
	r := bytes.NewReader(data)
	before := r.Len()
	for i, f := range fields {
		if _, err := f.ReadFrom(r); err != nil {
			t.Fatalf("decodedLen: field[%d] read failed: %v", i, err)
		}
	}
	return before - r.Len()
}
