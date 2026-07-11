package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// TestScaledDimensionPos verifies the 8:1 overworld<->nether coordinate scale (DimensionType.NETHER
// coordinate_scale == 8.0): overworld->nether divides X/Z by 8; nether->overworld multiplies by 8.
func TestScaledDimensionPos(t *testing.T) {
	// Overworld (800, 80) -> nether (100, 10).
	if x, z := scaledDimensionPos(800, 80, dimOverworld, dimNether); x != 100 || z != 10 {
		t.Fatalf("overworld->nether scale = (%.1f,%.1f), want (100,10)", x, z)
	}
	// Nether (100, 10) -> overworld (800, 80).
	if x, z := scaledDimensionPos(100, 10, dimNether, dimOverworld); x != 800 || z != 80 {
		t.Fatalf("nether->overworld scale = (%.1f,%.1f), want (800,80)", x, z)
	}
	// Same dimension: no scale.
	if x, z := scaledDimensionPos(50, 60, dimOverworld, dimOverworld); x != 50 || z != 60 {
		t.Fatalf("same-dim scale = (%.1f,%.1f), want (50,60)", x, z)
	}
}

// TestChangeDimensionToNether drives changeDimension(overworld->nether) directly (bypassing the flaky
// bot command dispatch) and asserts the observable contract:
//   - a ClientboundRespawn is sent carrying the NETHER spawn-info (dimensionType holder 3+1==4, name
//     minecraft:the_nether, isFlat false, seaLevel 32, dataToKeep KEEP_ALL_DATA==3);
//   - the player's dimension flips to dimNether and geometry to 8 sections;
//   - the position is coordinate-scaled (8:1) and the streamer is reset (centerSent=false, sentChunks
//     cleared) so the nether world streams in;
//   - a PlayerPosition teleport is sent (re-arming the confirm gate).
func TestChangeDimensionToNether(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Wire an overworld world + a nether world so dimWorld resolves both. NewChunkManager only (no
	// worker needed for this test — changeDimension does not itself generate chunks).
	// Set the region worlds directly (no worker bridge needed for this test).
	for _, r := range loop.regions {
		r.world = world.NewChunkManager()
	}
	loop.netherWorld = world.NewChunkManager()

	p := &tickPlayer{
		client:   captureClient(8192),
		entityID: 1,
		x:        800.0, y: 70.0, z: 80.0,
		dimension:  dimOverworld,
		secs:       24,
		viewDist:   serverViewDistance,
		sentChunks: map[level.ChunkPos]bool{{0, 0}: true}, // pretend a chunk was already sent
		centerSent: true,
	}
	loop.players = append(loop.players, p)

	loop.changeDimension(p, dimNether)

	// Player state flipped.
	if p.dimension != dimNether {
		t.Fatalf("dimension = %d, want dimNether(%d)", p.dimension, dimNether)
	}
	if p.secs != dimNetherSecs {
		t.Fatalf("secs = %d, want %d (nether)", p.secs, dimNetherSecs)
	}
	// Position: the destination is now resolved by the PortalForcer (findClosestPortalPosition ->
	// createPortal). The nether world is empty here (no portal, no loaded chunks), so createPortal takes the
	// NOTHING_FOUND forced-platform fallback at the clamped 8:1-scaled column (800,80)->(100,10): the frame
	// bottomLeft is (exitPos.x - direction.stepX(EAST=1), clamp(y,70,worldTop-9), exitPos.z) == (99,70,10),
	// and the player is centered on that base cell -> (99.5, 70, 10.5). CITE: PortalForcer.createPortal
	// forced-platform fallback; the 8:1 scale is still applied upstream (scaledDimensionPos).
	if p.x != 99.5 || p.z != 10.5 {
		t.Fatalf("position = (%.1f,_,%.1f), want (99.5,_,10.5) [8:1 scale -> forced portal base center]", p.x, p.z)
	}
	if p.y != 70.0 {
		t.Fatalf("position Y = %.1f, want 70 (forced-platform clamp floor)", p.y)
	}
	// Streamer reset so the nether ring re-streams.
	if p.centerSent {
		t.Fatalf("centerSent = true, want false (streamer must re-send the framing)")
	}
	if len(p.sentChunks) != 0 {
		t.Fatalf("sentChunks not cleared (%d entries) — the nether ring would not re-stream", len(p.sentChunks))
	}

	// Decode the sent packets: expect a ClientboundRespawn (nether spawn-info) and a PlayerPosition.
	pkts := drainPackets(p.client)
	var sawRespawn, sawPosition bool
	for _, pkt := range pkts {
		switch packetid.ClientboundPacketID(pkt.ID) {
		case packetid.ClientboundRespawn:
			sawRespawn = true
			var (
				dimTypeHolder pk.VarInt
				dimName       pk.Identifier
				seed          pk.Long
				gameType      pk.Byte
				prevGameType  pk.Byte
				isDebug       pk.Boolean
				isFlat        pk.Boolean
				hasDeath      pk.Boolean
				portalCd      pk.VarInt
				seaLevel      pk.VarInt
				dataToKeep    pk.Byte
			)
			if err := pkt.Scan(&dimTypeHolder, &dimName, &seed, &gameType, &prevGameType,
				&isDebug, &isFlat, &hasDeath, &portalCd, &seaLevel, &dataToKeep); err != nil {
				t.Fatalf("Respawn scan failed: %v", err)
			}
			if int(dimTypeHolder) != netherDimensionTypeID+1 {
				t.Errorf("Respawn dimensionType holder = %d, want %d (nether id+1)", dimTypeHolder, netherDimensionTypeID+1)
			}
			if string(dimName) != netherDimensionName {
				t.Errorf("Respawn dimension = %q, want %q", string(dimName), netherDimensionName)
			}
			if bool(isFlat) {
				t.Errorf("Respawn isFlat = true, want false (nether is not flat)")
			}
			if int(seaLevel) != netherSeaLevelWire {
				t.Errorf("Respawn seaLevel = %d, want %d", seaLevel, netherSeaLevelWire)
			}
			if byte(dataToKeep) != respawnDataKeepAll {
				t.Errorf("Respawn dataToKeep = %d, want %d (KEEP_ALL_DATA)", dataToKeep, respawnDataKeepAll)
			}
		case packetid.ClientboundPlayerPosition:
			sawPosition = true
		}
	}
	if !sawRespawn {
		t.Fatalf("no ClientboundRespawn sent on dimension change")
	}
	if !sawPosition {
		t.Fatalf("no ClientboundPlayerPosition (teleport) sent on dimension change")
	}
}

// TestNetherPortalTravelTimer drives tickNetherPortal for a survival player standing in an overworld
// nether_portal block: portalTime accrues each tick and the player teleports to the nether at the
// transition threshold (80), with the cooldown set so it does not immediately bounce back.
func TestNetherPortalTravelTimer(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	ow := world.NewChunkManager()
	// A loaded overworld chunk at (0,0) with a nether_portal block at the player's feet.
	readyOverworldChunk(ow)
	loop.netherWorld = world.NewChunkManager()
	for _, r := range loop.regions {
		r.world = ow
	}
	portalState := block.ToStateID[block.NetherPortal{Axis: block.X}]
	feet := pk.Position{X: 8, Y: 70, Z: 8}
	if !ow.SetBlock(feet, portalState, dimMinY) {
		t.Fatalf("failed to place nether_portal block for the test")
	}

	p := &tickPlayer{
		client:   captureClient(8192),
		entityID: 1,
		x:        8.5, y: 70.0, z: 8.5, // standing in the portal cell
		dimension: dimOverworld,
		secs:      24, gameMode: gameModeSurvival,
		viewDist: serverViewDistance,
	}
	loop.players = append(loop.players, p)

	// Tick up to (transition+2): the player must NOT travel before 80, and must travel by then.
	traveled := false
	for i := 0; i < portalTransitionTicks+2; i++ {
		loop.tickNetherPortal()
		if p.dimension == dimNether {
			traveled = true
			if i < portalTransitionTicks {
				t.Fatalf("traveled too early at tick %d (transition is %d)", i, portalTransitionTicks)
			}
			break
		}
	}
	if !traveled {
		t.Fatalf("player never traveled after %d ticks in the portal (portalTime=%d)", portalTransitionTicks+2, p.portalTime)
	}
	if p.portalCooldown != portalCooldownTicks {
		t.Fatalf("portalCooldown = %d after travel, want %d", p.portalCooldown, portalCooldownTicks)
	}
}

// TestNetherPortalTimerDecays verifies portalTime decays by 4/tick when the player is NOT in a portal
// (PortalProcessor.decayTick), so a brief brush with a portal does not accumulate a teleport.
func TestNetherPortalTimerDecays(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	ow := world.NewChunkManager()
	readyOverworldChunk(ow)
	loop.netherWorld = world.NewChunkManager()
	for _, r := range loop.regions {
		r.world = ow
	}
	p := &tickPlayer{
		client:   captureClient(8192),
		entityID: 1,
		x:        8.5, y: 70.0, z: 8.5, // NO portal block here
		dimension: dimOverworld, secs: 24, gameMode: gameModeSurvival,
		viewDist:  serverViewDistance,
		portalTime: 20, // pretend some accrued time
	}
	loop.players = append(loop.players, p)

	loop.tickNetherPortal()
	if p.portalTime != 16 { // 20 - 4
		t.Fatalf("portalTime after one decay tick = %d, want 16 (decay by 4)", p.portalTime)
	}
	if p.dimension != dimOverworld {
		t.Fatalf("player traveled while not in a portal")
	}
}

// readyOverworldChunk inserts a loaded all-air overworld chunk at (0,0) so SetBlock/GetBlock work.
func readyOverworldChunk(m *world.ChunkManager) {
	ch := level.EmptyChunk(24)
	ch.Status = level.StatusFull
	m.Insert(level.ChunkPos{0, 0}, ch)
}

// TestDimWorldRouting verifies dimWorld returns the nether world for a nether player and the overworld
// for an overworld player, and dimMinYFor/dimSecsFor return the right geometry.
func TestDimWorldRouting(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	ow := world.NewChunkManager()
	nether := world.NewChunkManager()
	for _, r := range loop.regions {
		r.world = ow
	}
	loop.netherWorld = nether

	pOver := &tickPlayer{dimension: dimOverworld}
	pNether := &tickPlayer{dimension: dimNether}
	if loop.dimWorld(pOver) != ow {
		t.Fatalf("dimWorld(overworld player) != overworld world")
	}
	if loop.dimWorld(pNether) != nether {
		t.Fatalf("dimWorld(nether player) != nether world")
	}
	if loop.dimWorld(nil) != ow {
		t.Fatalf("dimWorld(nil) != overworld world (default)")
	}
	if dimMinYFor(dimNether) != 0 || dimSecsFor(dimNether) != 8 {
		t.Fatalf("nether geometry = (%d,%d), want (0,8)", dimMinYFor(dimNether), dimSecsFor(dimNether))
	}
	if dimMinYFor(dimOverworld) != dimMinY || dimSecsFor(dimOverworld) != 24 {
		t.Fatalf("overworld geometry = (%d,%d), want (%d,24)", dimMinYFor(dimOverworld), dimSecsFor(dimOverworld), dimMinY)
	}
}
