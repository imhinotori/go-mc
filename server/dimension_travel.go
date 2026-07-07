package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// dimension_travel.go — Entity.changeDimension for a ServerPlayer: the observable core of moving a
// player between the overworld and the nether. It is the Go realization of the vanilla teardown-and-
// rebuild that ServerPlayer.changeDimension / PlayerList.respawn perform: send ClientboundRespawn with
// the TARGET dimension's spawn-info (so the client rebuilds a fresh ClientLevel of that dimension),
// reposition at the coordinate-scaled destination, and reset the streamer so the target world's chunks
// stream in.
//
// CITED JAR (CFR/javap, 26.2-inner.jar):
//   - net.minecraft.server.level.ServerPlayer.changeDimension(DimensionTransition): teleport +
//     connection.send(new ClientboundRespawn(createCommonSpawnInfo(newLevel), (byte)3)) [dataToKeep=
//     KEEP_ALL_DATA=3 for a dimension change, vs 0 for a death respawn], then resetChunkTracking +
//     the position teleport.
//   - net.minecraft.world.level.portal.PortalShape / DimensionType.coordinateScale: the overworld<->
//     nether horizontal 8:1 scale (DimensionType.NETHER coordinate_scale == 8.0). Overworld->nether
//     divides X/Z by 8; nether->overworld multiplies by 8. Y is preserved (clamped to the target).
//   - ClientboundRespawnPacket dataToKeep bits: KEEP_ATTRIBUTES(1)|KEEP_ENTITY_DATA(2) == 3 for a
//     dimension change (keeps the player's data, only rebuilds the level).

// respawnDataKeepAll is the ClientboundRespawn dataToKeep for a DIMENSION CHANGE (KEEP_ALL_DATA == 3:
// KEEP_ATTRIBUTES|KEEP_ENTITY_DATA), vs respawnDataKeepNone(0) for a death respawn. CITE:
// ServerPlayer.changeDimension sends ClientboundRespawn(..., (byte)3).
const respawnDataKeepAll byte = 3

// netherCoordinateScale is DimensionType.NETHER.coordinateScale() == 8.0 — the overworld<->nether
// horizontal distance ratio (1 nether block == 8 overworld blocks). CITE: dimension_type/the_nether.json
// "coordinate_scale": 8.0.
const netherCoordinateScale = 8.0

// scaledDimensionPos ports the horizontal coordinate scale between dimensions
// (PortalForcer/DimensionTransition target math): moving overworld->nether divides X/Z by 8; nether->
// overworld multiplies by 8. Y is carried over (clamped to the destination's build range by the
// caller's spawn resolution). CITE: DimensionType.coordinateScale + Level.getWorldBorder scaling.
func scaledDimensionPos(x, z float64, fromDim, toDim int) (float64, float64) {
	scale := 1.0
	if fromDim == dimOverworld && toDim == dimNether {
		scale = 1.0 / netherCoordinateScale
	} else if fromDim == dimNether && toDim == dimOverworld {
		scale = netherCoordinateScale
	}
	return x * scale, z * scale
}

// changeDimension moves a player to targetDim, rebuilding the client's level and re-streaming the
// target world's chunks. It is the shared core behind the /nether command (and, later, the nether
// portal's entityInside travel). It:
//
//  1. sends ClientboundRespawn with the TARGET dimension's spawn-info (dataToKeep=KEEP_ALL_DATA) so
//     the client tears down the old ClientLevel and rebuilds one of the target dimension (nether fog/
//     ceiling, or the overworld sky), then GameEvent(LEVEL_CHUNKS_LOAD_START) so it waits for chunks;
//  2. flips p.dimension + p.secs (the target geometry) so t.dimWorld(p) and the streamer size to the
//     new world;
//  3. computes the coordinate-scaled destination (8:1) and teleports there with a fresh teleport id
//     (re-arming the confirm gate), clamped to a safe Y in the target;
//  4. resets the streamer (centerSent=false + fresh sentChunks) so the target world's ring streams in.
//
// Runs on the tick goroutine (TICK-05); all sends go through the bounded outbound queue.
func (t *TickLoop) changeDimension(p *tickPlayer, targetDim int) {
	if p.client == nil || p.dimension == targetDim {
		return
	}
	fromDim := p.dimension

	// (3-pre) Coordinate-scale the destination from the CURRENT position before we overwrite dimension.
	tx, tz := scaledDimensionPos(p.x, p.z, fromDim, targetDim)

	// (1) Respawn into the target dimension's spawn-info. KEEP_ALL_DATA(3): a dimension change keeps
	// the player's attributes/inventory, only rebuilding the level.
	p.client.Send(changeDimensionRespawnPacket(targetDim))
	p.client.Send(writeGameEventPacket(gameEventLevelChunksLoadStart, 0))

	// (2) Flip the player's dimension + geometry so the streamer + dimWorld target the new world.
	p.dimension = targetDim
	p.secs = dimSecsFor(targetDim)

	// (3) Teleport to the scaled destination. The target Y: for the nether, drop the player onto a safe
	// column top the generator produced (a follow-up will search for a real floor; v1 uses a fixed safe
	// mid-nether Y above the lava sea so the player does not spawn embedded). For the overworld return,
	// reuse the world spawn Y. This is the placement seam the PortalForcer's destination search refines.
	ty := changeDimensionTargetY(t, targetDim)
	p.x, p.y, p.z = tx, ty, tz
	p.center = chunkCenterOf(int32(math.Floor(tx)), int32(math.Floor(tz)))
	teleportID := t.nextTeleportID()
	p.awaitingTeleport = teleportID
	p.confirmedTeleport = false
	p.client.Send(writePlayerPositionPacket(teleportID, tx, ty, tz, 0, 0))

	// (4) Reset the streamer so the TARGET world's ring streams into the fresh ClientLevel.
	p.centerSent = false
	p.sentChunks = make(map[level.ChunkPos]bool)

	udebugPlayer(p, "dimension", "changed %d -> %d at (%.1f,%.1f,%.1f)", fromDim, targetDim, tx, ty, tz)
}

// changeDimensionRespawnPacket builds ClientboundRespawn for a dimension change to targetDim, using the
// target dimension's spawn-info (dimension type + name + isFlat + seaLevel) and dataToKeep=KEEP_ALL_DATA.
func changeDimensionRespawnPacket(targetDim int) pk.Packet {
	info := commonPlayerSpawnInfoEncoder{}
	if targetDim == dimNether {
		info = commonPlayerSpawnInfoEncoder{
			dimTypeID: netherDimensionTypeID,
			dimName:   netherDimensionName,
			isFlatSet: true, isFlat: false, // the nether is not flat-rendered
			seaLevel: netherSeaLevelWire,
		}
	}
	return pk.Marshal(int32(packetid.ClientboundRespawn), info, pk.Byte(respawnDataKeepAll))
}

// changeDimensionTargetY picks a safe destination Y in the target dimension. For the nether this is a
// fixed mid-dimension Y (64) above the lava sea (sea_level 32) — a naive-but-standable placement until
// the PortalForcer's real floor search lands; the nether's netherrack ceiling is at ~127, so 64 is open
// interior for most columns. For the overworld it is the world spawn surface Y+2 (the join placement).
func changeDimensionTargetY(t *TickLoop, targetDim int) float64 {
	if targetDim == dimNether {
		return 64.0
	}
	if t.hasSpawnPoint {
		return t.spawnPoint.Y
	}
	return float64(t.spawnSurfaceY + 2)
}
