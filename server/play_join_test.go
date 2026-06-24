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
	p := writeLoginPacket(viewDist)
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

	if playerID != joinEntityID {
		t.Errorf("playerId = %d, want %d", playerID, joinEntityID)
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

	g := NewGameTick(inbound, loop, keep, -48)

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
