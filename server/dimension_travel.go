package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
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

	// END arrival (overworld/nether -> the_end): EndPortalBlock.getPortalDestination places the
	// player at the obsidian spawn platform (atBottomCenterOf(END_SPAWN_POINT) == 100.5, _, 0.5),
	// NOT the coordinate-scaled position. Override X/Z here; the platform blocks are laid in step (5).
	if targetDim == dimEnd {
		tx, tz = endSpawnX, endSpawnZ
	}

	// END return (the_end -> overworld): EndPortalBlock.getPortalDestination's `flag6` (dimension==END)
	// branch resolves the destination to `respawnData.dimension()` at `respawnData.pos()` -- the player's
	// respawn (bed) position, or the world-spawn point when no bed is set -- NOT the carried End coords.
	// The overworld<->End pair has NO coordinate scale (scaledDimensionPos returns the position unchanged),
	// so without this the returning player would land at the End's (100.5, _, 0.5) column in the overworld.
	// Route X/Z to the respawn/world-spawn horizontal position, mirroring vanilla (the Y is resolved to the
	// same spawn in changeDimensionTargetY step 3). CITE: EndPortalBlock.getPortalDestination (flag6 branch:
	// respawnData.pos()).
	if fromDim == dimEnd && targetDim == dimOverworld && t.hasSpawnPoint {
		tx, tz = t.spawnPoint.X, t.spawnPoint.Z
	}

	// NETHER PORTAL DESTINATION (overworld<->nether): NetherPortalBlock.getPortalDestination ->
	// getExitPortal -> PortalForcer.findClosestPortalPosition (else createPortal). This RESOLVES the real
	// destination portal (an existing one within 16 blocks in the nether / 128 in the overworld, else a
	// freshly built 4x5 frame), REPLACING the naive coordinate-scaled-column-with-fixed-Y landing. The
	// clamp-to-bounds of the scaled entry point is DimensionType.getTeleportationScale + WorldBorder
	// .clampToBounds (getPortalDestination offsets 78-101); here scaledDimensionPos already applied the 8:1
	// scale, so we clamp X/Z and search from that column. resolvedY carries the portal Y (the standable cell
	// at the portal base) so step (3) uses it instead of changeDimensionTargetY. CITE: NetherPortalBlock
	// .getPortalDestination / getExitPortal + PortalForcer.findClosestPortalPosition / createPortal.
	resolvedY, haveResolvedY := 0.0, false
	if (fromDim == dimOverworld && targetDim == dimNether) || (fromDim == dimNether && targetDim == dimOverworld) {
		if rx, ry, rz, ok := t.resolveNetherPortalDestination(targetDim, tx, tz); ok {
			tx, tz = rx, rz
			resolvedY, haveResolvedY = ry, true
		}
	}

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
	if haveResolvedY {
		ty = resolvedY // the PortalForcer-resolved portal Y (found or freshly built)
	}
	p.x, p.y, p.z = tx, ty, tz
	p.center = chunkCenterOf(int32(math.Floor(tx)), int32(math.Floor(tz)))
	teleportID := t.nextTeleportID()
	p.awaitingTeleport = teleportID
	p.confirmedTeleport = false
	p.client.Send(writePlayerPositionPacket(teleportID, tx, ty, tz, 0, 0))

	// (4) Reset the streamer so the TARGET world's ring streams into the fresh ClientLevel.
	p.centerSent = false
	p.sentChunks = make(map[level.ChunkPos]bool)

	// (5) END: lay the obsidian spawn platform under the arrival point so the player does not
	// fall into the void. Vanilla runs EndPlatformFeature.createEndPlatform during the transition
	// (PLACE_PORTAL_TICKET force-loads the destination chunk); here we build it directly on the End
	// world once its chunk is present (ensureEndPlatform force-generates the platform chunk if the
	// async worker has not produced it yet).
	if targetDim == dimEnd {
		t.ensureEndPlatform()
		// ENDER DRAGON (Task): lazily init the dragon fight on the FIRST player entry into the End (spawn
		// the boss at the fight origin + the healing-crystal ring + the boss bar). spawnEndDragonFight is
		// idempotent (t.endDragonFightInit guards it), so subsequent End arrivals do not respawn the dragon.
		// This is the reduced analogue of EnderDragonFight.tryRespawn/spawnDragon triggered on arrival.
		t.spawnEndDragonFight()
	}

	udebugPlayer(p, "dimension", "changed %d -> %d at (%.1f,%.1f,%.1f)", fromDim, targetDim, tx, ty, tz)
}

// portalTransitionTicks / portalCooldownTicks are NetherPortalBlock.getPortalTransitionTime + the
// player's getDimensionChangingDelay. The transition time is the survival portal dwell — the gamerule
// PLAYERS_NETHER_PORTAL_DEFAULT_DELAY (default 80) for a survival player, PLAYERS_NETHER_PORTAL_CREATIVE_
// DELAY (default 0, instant) for creative. v1 has no gamerule subsystem, so these are CITED CONSTANTS
// equal to the vanilla gamerule defaults, structured to become real gamerule reads later — never baked
// away. The cooldown is Player.getDimensionChangingDelay() == 10. CITE: NetherPortalBlock
// .getPortalTransitionTime + Player.getDimensionChangingDelay.
const (
	portalTransitionTicks         = 80 // survival dwell (PLAYERS_NETHER_PORTAL_DEFAULT_DELAY default)
	portalTransitionTicksCreative = 0  // creative: instant (PLAYERS_NETHER_PORTAL_CREATIVE_DELAY default)
	portalCooldownTicks           = 10 // Player.getDimensionChangingDelay()
)

// playerInNetherPortal reports whether the player is standing IN a nether_portal block, read from the
// player's-dimension world (dimWorld). Vanilla's entityInside fires for any block the entity's AABB
// overlaps; v1 checks the feet + eye cells of the player's column (a full-height player overlaps both),
// which is the faithful common case for a 3+-tall portal. CITE: NetherPortalBlock.entityInside via the
// AABB block-overlap sweep.
func (t *TickLoop) playerInNetherPortal(p *tickPlayer) bool {
	mgr := t.dimWorld(p)
	if mgr == nil {
		return false
	}
	minY := dimMinYFor(p.dimension)
	bx := int(mthFloorF(p.x))
	bz := int(mthFloorF(p.z))
	feetY := int(mthFloorF(p.y))
	for _, by := range [2]int{feetY, feetY + 1} { // feet + eye/head cell
		s, ok := mgr.GetBlock(pk.Position{X: bx, Y: by, Z: bz}, minY)
		if !ok || int(s) < 0 || int(s) >= len(block.StateList) {
			continue
		}
		if _, isPortal := block.StateList[s].(block.NetherPortal); isPortal {
			return true
		}
	}
	return false
}

// tickNetherPortal is the 1:1 port of Entity.handlePortal for every player: process the portal cooldown,
// then — if the player is inside a nether portal — accrue portalTime and teleport at the transition
// threshold; if not inside, decay portalTime by 4/tick (PortalProcessor.decayTick) and clear the timer at
// 0. On teleport it flips the player's dimension (overworld<->nether) and sets the portal cooldown so the
// player does not immediately bounce back. Tick-owned (called each tick from tickEntities). CITE:
// Entity.handlePortal + PortalProcessor.processPortalTeleportation + Entity.setPortalCooldown.
func (t *TickLoop) tickNetherPortal() {
	// Only when the nether world is wired (the second dimension is armed); otherwise portals are inert.
	if t.netherWorld == nil {
		return
	}
	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue
		}
		// processPortalCooldown: decrement the cooldown toward 0 each tick (Entity.processPortalCooldown).
		if p.portalCooldown > 0 {
			p.portalCooldown--
		}

		inside := t.playerInNetherPortal(p)
		if inside {
			// canUsePortal(false) == !isOnPortalCooldown(). While on cooldown the timer does not advance
			// toward a teleport (the just-arrived player standing in the destination portal must not bounce).
			if p.portalCooldown > 0 {
				continue
			}
			transition := portalTransitionTicks
			if p.gameMode == gameModeCreative {
				transition = portalTransitionTicksCreative
			}
			// processPortalTeleportation: portalTime++ >= transitionTime -> teleport.
			reached := p.portalTime >= transition
			p.portalTime++
			if reached {
				target := dimNether
				if p.dimension == dimNether {
					target = dimOverworld
				}
				p.portalCooldown = portalCooldownTicks // setPortalCooldown (getDimensionChangingDelay == 10)
				p.portalTime = 0
				t.changeDimension(p, target)
			}
		} else {
			// decayTick: portalTime = max(portalTime-4, 0) when not inside a portal this tick.
			p.portalTime -= 4
			if p.portalTime < 0 {
				p.portalTime = 0
			}
		}
	}
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
	} else if targetDim == dimEnd {
		info = commonPlayerSpawnInfoEncoder{
			dimTypeID: endDimensionTypeID,
			dimName:   endDimensionName,
			isFlatSet: true, isFlat: false, // the End is not flat-rendered
			seaLevel: endSeaLevelWire, seaLevelSet: true, // end.json sea_level 0 (report the real 0)
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
	if targetDim == dimEnd {
		// The obsidian spawn platform: ServerLevel.END_SPAWN_POINT is (100,50,0); the player is
		// placed at atBottomCenterOf(END_SPAWN_POINT).subtract(0,1,0) == y 49, standing on the
		// obsidian floor createEndPlatform lays at y 48. CITE: EndPortalBlock.getPortalDestination.
		return endSpawnY
	}
	if t.hasSpawnPoint {
		return t.spawnPoint.Y
	}
	return float64(t.spawnSurfaceY + 2)
}
