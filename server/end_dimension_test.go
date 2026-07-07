package server

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// TestEndDimensionType asserts the the_end dimension_type carries the 26.2 values the client
// rebuilds the End ClientLevel from: min_y 0, height 256, no ceiling, a fixed time, ambient
// light. Read from the extracted registrydata JSON (the source the server sends in Config).
// CITE: dimension_type/the_end.json.
func TestEndDimensionType(t *testing.T) {
	raw, err := os.ReadFile("registrydata/registries/dimension_type/the_end.json")
	if err != nil {
		t.Fatalf("read the_end dimension_type: %v", err)
	}
	var dt struct {
		MinY         int     `json:"min_y"`
		Height       int     `json:"height"`
		HasCeiling   bool    `json:"has_ceiling"`
		HasFixedTime bool    `json:"has_fixed_time"`
		AmbientLight float64 `json:"ambient_light"`
		CoordScale   float64 `json:"coordinate_scale"`
	}
	if err := json.Unmarshal(raw, &dt); err != nil {
		t.Fatalf("parse the_end dimension_type: %v", err)
	}
	if dt.MinY != 0 {
		t.Errorf("the_end min_y = %d, want 0", dt.MinY)
	}
	if dt.Height != 256 {
		t.Errorf("the_end height = %d, want 256", dt.Height)
	}
	if dt.HasCeiling {
		t.Errorf("the_end has_ceiling = true, want false")
	}
	if !dt.HasFixedTime {
		t.Errorf("the_end has_fixed_time = false, want true")
	}
	if dt.AmbientLight != 0.25 {
		t.Errorf("the_end ambient_light = %v, want 0.25", dt.AmbientLight)
	}
	if dt.CoordScale != 1.0 {
		t.Errorf("the_end coordinate_scale = %v, want 1.0", dt.CoordScale)
	}
	if endDimensionTypeID != 2 {
		t.Errorf("endDimensionTypeID = %d, want 2", endDimensionTypeID)
	}
}

// TestEndWorldgenCentralIsland asserts the End generator produces a solid central island of
// end_stone (not air) at chunk (0,0). CITE: end.json default_block end_stone +
// DensityFunctions$EndIslandDensityFunction.
func TestEndWorldgenCentralIsland(t *testing.T) {
	g := world.NewEndGenerator(1234, dimEndSecs, dimEndMinY)
	ch := g.Generate(level.ChunkPos{0, 0})
	if ch == nil {
		t.Fatal("End generator returned nil chunk")
	}
	endStone := block.ToStateID[block.EndStone{}]
	count := 0
	for _, sec := range ch.Sections {
		for i := 0; i < 4096; i++ {
			if sec.GetBlock(i) == endStone {
				count++
			}
		}
	}
	if count < 1000 {
		t.Fatalf("central island end_stone count = %d, want a solid island (>=1000)", count)
	}
}

// TestChangeDimensionToEnd drives changeDimension(overworld->the_end): the Respawn spawn-info,
// the dimension flip, the player landing ON the obsidian platform, and the platform blocks.
func TestChangeDimensionToEnd(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	for _, r := range loop.regions {
		r.world = world.NewChunkManager()
	}
	loop.endWorld = world.NewChunkManager()
	loop.endGen = world.NewEndGenerator(1234, dimEndSecs, dimEndMinY)

	p := &tickPlayer{
		client:   captureClient(8192),
		entityID: 1,
		x:        800.0, y: 70.0, z: 80.0,
		dimension:  dimOverworld,
		secs:       24,
		viewDist:   serverViewDistance,
		sentChunks: map[level.ChunkPos]bool{{0, 0}: true},
		centerSent: true,
	}
	loop.players = append(loop.players, p)

	loop.changeDimension(p, dimEnd)

	if p.dimension != dimEnd {
		t.Fatalf("dimension = %d, want dimEnd(%d)", p.dimension, dimEnd)
	}
	if p.secs != dimEndSecs {
		t.Fatalf("secs = %d, want %d (End)", p.secs, dimEndSecs)
	}
	if p.x != endSpawnX || p.y != endSpawnY || p.z != endSpawnZ {
		t.Fatalf("End arrival = (%.1f,%.1f,%.1f), want (%.1f,%.1f,%.1f)",
			p.x, p.y, p.z, endSpawnX, endSpawnY, endSpawnZ)
	}
	obsidian := block.ToStateID[block.Obsidian{}]
	air := block.ToStateID[block.Air{}]
	if s, ok := loop.endWorld.GetBlock(pk.Position{X: 100, Y: 48, Z: 0}, dimEndMinY); !ok || s != obsidian {
		t.Fatalf("platform floor (100,48,0) = %v (ok=%v), want obsidian(%v)", s, ok, obsidian)
	}
	if s, ok := loop.endWorld.GetBlock(pk.Position{X: 100, Y: 49, Z: 0}, dimEndMinY); !ok || s != air {
		t.Fatalf("platform interior (100,49,0) = %v (ok=%v), want air(%v)", s, ok, air)
	}
	for _, c := range [][2]int{{-2, -2}, {2, 2}, {-2, 2}, {2, -2}} {
		pos := pk.Position{X: 100 + c[0], Y: 48, Z: 0 + c[1]}
		if s, ok := loop.endWorld.GetBlock(pos, dimEndMinY); !ok || s != obsidian {
			t.Errorf("platform corner (%d,48,%d) = %v (ok=%v), want obsidian", pos.X, pos.Z, s, ok)
		}
	}

	pkts := drainPackets(p.client)
	sawRespawn := false
	for _, pkt := range pkts {
		if packetid.ClientboundPacketID(pkt.ID) != packetid.ClientboundRespawn {
			continue
		}
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
		if int(dimTypeHolder) != endDimensionTypeID+1 {
			t.Errorf("Respawn holder = %d, want %d", dimTypeHolder, endDimensionTypeID+1)
		}
		if string(dimName) != endDimensionName {
			t.Errorf("Respawn dimension = %q, want %q", string(dimName), endDimensionName)
		}
		if bool(isFlat) {
			t.Errorf("Respawn isFlat = true, want false")
		}
		if byte(dataToKeep) != respawnDataKeepAll {
			t.Errorf("Respawn dataToKeep = %d, want %d", dataToKeep, respawnDataKeepAll)
		}
	}
	if !sawRespawn {
		t.Fatalf("no ClientboundRespawn sent on End dimension change")
	}
}

// TestEndReturnToOverworld drives the End->overworld return: the dimension flips back and the
// world spawn Y is used. CITE: EndPortalBlock.getPortalDestination END branch (respawn dim).
func TestEndReturnToOverworld(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	for _, r := range loop.regions {
		r.world = world.NewChunkManager()
	}
	loop.endWorld = world.NewChunkManager()
	loop.endGen = world.NewEndGenerator(1234, dimEndSecs, dimEndMinY)
	loop.hasSpawnPoint = true
	loop.spawnPoint = SpawnPoint{X: 8.5, Y: 72.0, Z: 8.5}

	p := &tickPlayer{
		client:   captureClient(8192),
		entityID: 1,
		x:        endSpawnX, y: endSpawnY, z: endSpawnZ,
		dimension:  dimEnd,
		secs:       dimEndSecs,
		viewDist:   serverViewDistance,
		sentChunks: map[level.ChunkPos]bool{{6, 0}: true},
		centerSent: true,
	}
	loop.players = append(loop.players, p)

	loop.changeDimension(p, dimOverworld)

	if p.dimension != dimOverworld {
		t.Fatalf("dimension = %d, want dimOverworld(%d)", p.dimension, dimOverworld)
	}
	if p.secs != 24 {
		t.Fatalf("secs = %d, want 24 (overworld)", p.secs)
	}
	if p.y != loop.spawnPoint.Y {
		t.Fatalf("return Y = %.1f, want world spawn Y %.1f", p.y, loop.spawnPoint.Y)
	}
}
