package server

// raid_tick.go — the Raid.tick / spawnGroup / joinRaid / updateRaiders drive loop, ported 1:1 from the
// unobfuscated 26.2 jar (net.minecraft.world.entity.raid.Raid, CFR this session). Split from raid.go so
// the state + predicates (raid.go) stay separate from the mutating tick (this file).
//
// Runs ONLY on the coordinator goroutine (raids.go ticks it at the quiescent post-fan-out barrier),
// where cross-region entity spawns/reads are legal (all regions joined). It uses t.cur() (falls back to
// globalRegion off the fan-out) for the entity store + spawnDeclaredMob.

import (
	"math"

	"github.com/imhinotori/sulfur/level"
)

// chunkPresentAt is the level.hasChunkAt(center) analogue: is the raid center's column loaded?
// (Raid.tick sets this.active = level.hasChunkAt(this.center).) A nil world (headless test) is treated
// as present so the raid loop is exercisable without a chunk manager — the test seam relies on this.
func (t *TickLoop) chunkPresentAt(cx, cz int) bool {
	w := t.world()
	if w == nil {
		return true
	}
	_, ok := w.Get(level.ChunkPos{int32(cx >> 4), int32(cz >> 4)})
	return ok
}

// tickRaid ports Raid.tick(ServerLevel) — the ONGOING branch (the wave loop) + the isOver celebration
// branch. VERIFIED CFR Raid.tick. The village/POI center guards (isVillage /
// moveRaidCenterToNearbyVillageSection) are CITE-DEFERRED (no POI subsystem, see raid.go v1 REDUCTIONS);
// everything else is faithful. PEACEFUL difficulty -> stop (there is no live difficulty setting, so a
// raid is created NORMAL and never PEACEFUL — the guard is kept structurally). The Raids manager
// (raids.go) prunes a STOPPED raid.
func (t *TickLoop) tickRaid(rm *raidsManager, r *Raid) {
	if r.isStopped() {
		return
	}
	if r.status == raidStatusOngoing {
		oldActive := r.active
		r.active = t.chunkPresentAt(r.centerX, r.centerZ)
		// PEACEFUL -> stop(): the difficulty is fixed at creation (NORMAL); kept structurally faithful.
		if r.difficulty == difficultyPeaceful {
			t.raidStop(r)
			return
		}
		if oldActive != r.active {
			t.bossSetVisible(r, r.active)
		}
		if !r.active {
			return
		}
		// village center guards (isVillage / moveRaidCenterToNearbyVillageSection / the groupsSpawned>0
		// -> LOSS branch): CITE-DEFERRED (no POI). The raid holds its fixed center.

		r.ticksActive++
		if r.ticksActive >= raidTimeoutTicks {
			t.raidStop(r)
			return
		}
		raidersAlive := r.getTotalRaidersAlive()
		// The pre-wave cooldown countdown (VERIFIED CFR): while raidersAlive==0 && hasMoreWaves, count
		// raidCooldownTicks down and drive the boss-bar progress; when it hits 0 with groupsSpawned>0,
		// reset to 300 and return (the between-wave pause). The waveSpawnPos probing is reduced (no
		// heightmap search — spawnGroup uses the center), so the shouldTryToFindSpawnPos block collapses.
		if raidersAlive == 0 && r.hasMoreWaves() {
			if r.raidCooldownTicks > 0 {
				// updatePlayers on tick 300 (the first pre-wave tick) and every 20 ticks thereafter
				// (VERIFIED CFR: raidCooldownTicks == 300 || raidCooldownTicks % 20 == 0), BEFORE the
				// decrement — the boss bar picks up players as they enter VALID_RAID_RADIUS during the pause.
				if r.raidCooldownTicks == raidDefaultPreTicks || r.raidCooldownTicks%20 == 0 {
					t.bossUpdatePlayers(r, rm)
				}
				r.raidCooldownTicks--
				t.bossSetProgress(r, clampF32(float32(raidDefaultPreTicks-r.raidCooldownTicks)/float32(raidDefaultPreTicks), 0, 1))
			} else if r.raidCooldownTicks == 0 && r.groupsSpawned > 0 {
				r.raidCooldownTicks = raidDefaultPreTicks
				t.bossSetName(r, "event.minecraft.raid")
				return
			}
		}
		if r.ticksActive%20 == 0 {
			t.bossUpdatePlayers(r, rm)
			t.raidUpdateRaiders(r)
			raidersAlive = r.getTotalRaidersAlive()
			if raidersAlive > 0 && raidersAlive <= raidLowMobThreshold {
				t.bossSetName(r, "event.minecraft.raid.raiders_remaining")
			} else {
				t.bossSetName(r, "event.minecraft.raid")
			}
		}
		// The wave-spawn loop (VERIFIED CFR): while shouldSpawnGroup(), place a wave at the center. The
		// attempt cap (>5 -> stop) is kept; spawnPos is the center (findRandomSpawnPos reduced), always
		// non-null, so the attempt path never trips in v1 (the counter is structurally present).
		attempt := 0
		hornPlayed := false // Raid.tick local bl (hasSpawnedNextWave): the horn plays once per tick.
		for r.shouldSpawnGroup() {
			spawnOK := t.raidSpawnGroup(r)
			if spawnOK {
				r.started = true
				// playSound(RAID_HORN) once per tick, on the FIRST successful spawn (Raid.tick guards
				// it with a local flag: `if (!bl) { playSound(level, spawnPos); bl = true; }`). The
				// horn seed is a single draw from the raid own stream (r.rng.nextLong()) taken inside
				// raidPlaySound, so the raid stream stays draw-order-faithful.
				if !hornPlayed {
					t.raidPlaySound(r)
					hornPlayed = true
				}
			} else {
				attempt++
			}
			if attempt > 5 {
				t.raidStop(r)
				break
			}
		}
		// Final-wave-cleared -> VICTORY (VERIFIED CFR): after the final wave with no raiders alive, count
		// postRaidTicks to 40 then flip to VICTORY. The hero-of-the-village effect grant is cite-deferred
		// (no player-hero tracking / HERO_OF_THE_VILLAGE effect).
		if r.isStarted() && !r.hasMoreWaves() && r.getTotalRaidersAlive() == 0 {
			if r.postRaidTicks < raidPostRaidLimit {
				r.postRaidTicks++
			} else {
				r.status = raidStatusVictory
			}
		}
		return
	}
	if r.isOver() {
		r.celebrationTicks++
		if r.celebrationTicks >= raidMaxCelebration {
			t.raidStop(r)
			return
		}
		if r.celebrationTicks%20 == 0 {
			t.bossUpdatePlayers(r, rm)
			t.bossSetVisible(r, true)
			if r.isVictory() {
				t.bossSetProgress(r, 0)
				t.bossSetName(r, "event.minecraft.raid.victory")
			} else {
				t.bossSetName(r, "event.minecraft.raid.defeat")
			}
		}
	}
}

// raidSpawnGroup ports Raid.spawnGroup(level, pos) — VERIFIED CFR. It iterates RaiderType.VALUES in
// ordinal order, computes numSpawns = getDefaultNumSpawns + getPotentialBonusSpawns (the bonus DRAWS on
// the raid stream, draw-order-faithful), and for each spawn creates the raider (raidCreateRaider) and
// joins it to the raid (raidJoinRaid). A RaiderType whose mob is absent among the 22 (VINDICATOR/EVOKER/
// PILLAGER/RAVAGER) resolves to raidCreateRaider==nil and the `!= null` loop guard breaks it — the jar's
// exact behavior when EntityType.create returns null. The ravager-rider spawns + the leader ominous
// banner are cite-deferred with those absent mobs. Returns true (a wave slot was processed) — the
// spawnPos is the center (never null), so the attempt path in tickRaid never trips. leaderSet is tracked
// (the canBeLeader/setLeader wiring) but the banner grant is deferred (the witch is not a leader).
func (t *TickLoop) raidSpawnGroup(r *Raid) bool {
	groupNumber := r.groupsSpawned + 1
	r.totalHealth = 0.0
	isBonusGroup := r.shouldSpawnBonusGroup()
	leaderSet := false
	for _, rt := range raiderTypesValues {
		numSpawns := r.getDefaultNumSpawns(rt, groupNumber, isBonusGroup) +
			r.getPotentialBonusSpawns(rt, groupNumber, r.difficulty, isBonusGroup)
		for i := 0; i < numSpawns; i++ {
			raider := t.raidCreateRaider(r, rt)
			if raider == nil {
				break // EntityType.create(...) == null -> the jar's loop guard breaks this RaiderType
			}
			if !leaderSet {
				// canBeLeader() is true for a PatrollingMonster; the WITCH is NOT a PatrollingMonster, so
				// (as in vanilla) it is never the leader. setPatrolLeader/setLeader (the ominous banner) is
				// therefore cite-deferred for the witch-only v1 wave. leaderSet stays false.
				_ = leaderSet
			}
			t.raidJoinRaid(r, groupNumber, raider)
		}
	}
	r.groupsSpawned++
	t.updateBossbar(r)
	return true
}

// raidCreateRaider ports raiderType.entityType.create(level, EVENT): spawn the RaiderType's mob at the
// raid center (spawnPos reduced to the center, EVENT spawn reason) via spawnVanillaMob. A RaiderType
// with no v1 mob (mobName=="") or an un-boot-loaded declaration returns nil, mirroring EntityType.create
// returning null for an unregistered/absent type. The witch is the only RaiderType present among the 22.
func (t *TickLoop) raidCreateRaider(r *Raid, rt raiderType) *Entity {
	if rt.mobName == "" {
		return nil
	}
	if t.mobRegistry == nil {
		return nil
	}
	if _, ok := t.mobRegistry.byName[rt.mobName]; !ok {
		return nil // the declaration is not boot-loaded -> treat as create()==null (graceful, no panic)
	}
	// joinRaid positions the raider at pos.x+0.5, pos.y+1.0, pos.z+0.5 (VERIFIED CFR joinRaid.setPos).
	return t.spawnVanillaMob(rt.mobName, float64(r.centerX)+0.5, float64(r.centerY)+1.0, float64(r.centerZ)+0.5)
}

// raidJoinRaid ports Raid.joinRaid(level, groupNumber, raider, pos, false) — VERIFIED CFR. It adds the
// raider to the wave set (addWaveMob), sets its currentRaid back-pointer + wave + canJoinRaid, and adds
// totalHealth. The finalizeSpawn/applyRaidBuffs/addFreshEntityWithPassengers are handled by
// spawnVanillaMob (the mob is already live in the store); this wires the raid MEMBERSHIP so
// hasActiveRaid() becomes true for the raider.
func (t *TickLoop) raidJoinRaid(r *Raid, groupNumber int, raider *Entity) {
	if r.groupRaiderMap[groupNumber] == nil {
		r.groupRaiderMap[groupNumber] = map[int32]*Entity{}
	}
	r.groupRaiderMap[groupNumber][raider.id] = raider
	r.totalHealth += raider.health
	if raider.ai != nil {
		raider.ai.currentRaid = r
		raider.ai.raidWave = groupNumber
		raider.ai.canJoinRaid = true
		raider.ai.ticksOutsideRaid = 0
	}
	t.updateBossbar(r)
}

// raidUpdateRaiders ports Raid.updateRaiders(level) — the per-20-tick membership prune. A raider is
// removed from the raid when it is dead/removed (the store no longer holds it) or has strayed beyond
// RAID_REMOVAL_THRESHOLD_SQR (12544) from the center. The tickCount<=600 grace + noActionTime>2400
// outside-raid counter are reduced to the dead/removed + distance checks (the observable membership).
func (t *TickLoop) raidUpdateRaiders(r *Raid) {
	const removalThresholdSqr = 12544.0
	var toRemove []*Entity
	for _, set := range r.groupRaiderMap {
		for _, raider := range set {
			if raider.dead {
				toRemove = append(toRemove, raider)
				continue
			}
			if _, ok := t.cur().entities.get(raider.id); !ok {
				toRemove = append(toRemove, raider)
				continue
			}
			dx := float64(r.centerX) - raider.x
			dy := float64(r.centerY) - raider.y
			dz := float64(r.centerZ) - raider.z
			if dx*dx+dy*dy+dz*dz >= removalThresholdSqr {
				toRemove = append(toRemove, raider)
			}
		}
	}
	for _, raider := range toRemove {
		t.raidRemoveRaider(r, raider, true)
	}
}

// raidRemoveRaider ports Raid.removeFromRaid(level, raider, removeFromTotalHealth): drop the raider from
// its wave set, subtract its health from totalHealth, clear its currentRaid back-pointer, refresh the bar.
func (t *TickLoop) raidRemoveRaider(r *Raid, raider *Entity, removeFromTotalHealth bool) {
	wave := 0
	if raider.ai != nil {
		wave = raider.ai.raidWave
	}
	set := r.groupRaiderMap[wave]
	if set == nil {
		return
	}
	if _, ok := set[raider.id]; !ok {
		return
	}
	delete(set, raider.id)
	if removeFromTotalHealth {
		r.totalHealth -= raider.health
	}
	if raider.ai != nil {
		raider.ai.currentRaid = nil
	}
	t.updateBossbar(r)
}

// raidHornSoundID / raidHornVolume are Raid.playSound RAID_HORN: SoundEvents.RAID_HORN
// ("event.raid.horn", registry id 1354) on SoundSource.NEUTRAL at volume 64.0f, pitch 1.0f. The wave-spawn
// horn is a per-player POSITIONAL sound whose (x,z) is pulled toward the raid center along a 13-block
// radius so a distant player still hears it from the raid direction.
//
//	[VERIFIED javap Raid.playSound: getstatic SoundEvents.RAID_HORN ; getstatic SoundSource.NEUTRAL ;
//	 ldc 64.0f (volume) ; fconst_1 (pitch) ; random.nextLong() (seed). id 1354 in data/registryid/soundevent.go.]
const (
	raidHornSoundID    int32   = 1354 // SoundEvents.RAID_HORN ("event.raid.horn")
	raidHornVolume     float32 = 64.0 // Raid.playSound volume (ldc 64.0f)
	raidHornPitch      float32 = 1.0  // Raid.playSound pitch (fconst_1)
	raidHornPullRadius         = 13.0 // the 13.0 radius the horn (x,z) is pulled toward the center
	raidHornHearRange          = 64.0 // dist <= 64.0 -> always heard; beyond only bossbar players hear it
)

// raidPlaySound ports Raid.playSound(ServerLevel, BlockPos) -- the RAID_HORN wave-spawn horn. VERIFIED CFR:
// it draws ONE seed (random.nextLong on the raid own stream) up front, then for EACH server player computes
// the horizontal distance to the raid center, pulls the horn (x,z) onto a 13-block circle around the center
// direction (so distant players hear it coming from the raid), and sends a positional ClientboundSound if
// the player is within 64 blocks OR is a bossbar (raid) participant. The Y is the player own Y (the horn
// tracks the listener vertically). NEUTRAL category, volume 64.0, pitch 1.0.
//
//	[VERIFIED javap Raid.playSound: fstore 13.0f ; bipush 64 ; getPlayers() ; random.nextLong() ; per player
//	 dist = sqrt((cx-px)^2 + (cz-pz)^2) ; sx = px + (13.0/dist)*(cx-px) ; sz = pz + (13.0/dist)*(cz-pz) ;
//	 if (dist > 64.0 && !players.contains(p)) skip ; else send ClientboundSoundPacket(RAID_HORN, NEUTRAL,
//	 sx, p.getY(), sz, 64.0f, 1.0f, seed).]
func (t *TickLoop) raidPlaySound(r *Raid) {
	// atCenterOf(BlockPos): the center of the raid center block (Vec3.atCenterOf adds 0.5 to each axis).
	cx := float64(r.centerX) + 0.5
	cz := float64(r.centerZ) + 0.5
	// ONE seed for the whole broadcast, drawn from the raid stream (Raid.random.nextLong), taken BEFORE the
	// per-player loop exactly as vanilla does.
	seed := r.rng.nextLong()
	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue
		}
		dx := cx - p.x
		dz := cz - p.z
		dist := math.Sqrt(dx*dx + dz*dz)
		// sx/sz = player pos + (13.0/dist) * (center - player pos): the horn pulled onto the 13-block circle.
		sx := p.x + (raidHornPullRadius/dist)*(cx-p.x)
		sz := p.z + (raidHornPullRadius/dist)*(cz-p.z)
		_, isParticipant := r.bossEvent.players[p.entityID]
		if dist > raidHornHearRange && !isParticipant {
			continue // too far and not a bossbar participant: no horn for this player
		}
		p.client.Send(encodeSound(raidHornSoundID, soundSourceNeutral, sx, p.y, sz, raidHornVolume, raidHornPitch, seed))
	}
}
