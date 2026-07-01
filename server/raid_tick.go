package server

// raid_tick.go — the Raid.tick / spawnGroup / joinRaid / updateRaiders drive loop, ported 1:1 from the
// unobfuscated 26.2 jar (net.minecraft.world.entity.raid.Raid, CFR this session). Split from raid.go so
// the state + predicates (raid.go) stay separate from the mutating tick (this file).
//
// Runs ONLY on the coordinator goroutine (raids.go ticks it at the quiescent post-fan-out barrier),
// where cross-region entity spawns/reads are legal (all regions joined). It uses t.cur() (falls back to
// globalRegion off the fan-out) for the entity store + spawnDeclaredMob.

import (
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
func (t *TickLoop) tickRaid(r *Raid) {
	if r.isStopped() {
		return
	}
	if r.status == raidStatusOngoing {
		oldActive := r.active
		r.active = t.chunkPresentAt(r.centerX, r.centerZ)
		// PEACEFUL -> stop(): the difficulty is fixed at creation (NORMAL); kept structurally faithful.
		if r.difficulty == difficultyPeaceful {
			r.stop()
			return
		}
		if oldActive != r.active {
			r.bossEvent.visible = r.active
		}
		if !r.active {
			return
		}
		// village center guards (isVillage / moveRaidCenterToNearbyVillageSection / the groupsSpawned>0
		// -> LOSS branch): CITE-DEFERRED (no POI). The raid holds its fixed center.

		r.ticksActive++
		if r.ticksActive >= raidTimeoutTicks {
			r.stop()
			return
		}
		raidersAlive := r.getTotalRaidersAlive()
		// The pre-wave cooldown countdown (VERIFIED CFR): while raidersAlive==0 && hasMoreWaves, count
		// raidCooldownTicks down and drive the boss-bar progress; when it hits 0 with groupsSpawned>0,
		// reset to 300 and return (the between-wave pause). The waveSpawnPos probing is reduced (no
		// heightmap search — spawnGroup uses the center), so the shouldTryToFindSpawnPos block collapses.
		if raidersAlive == 0 && r.hasMoreWaves() {
			if r.raidCooldownTicks > 0 {
				r.raidCooldownTicks--
				r.bossEvent.progress = clampF32(float32(raidDefaultPreTicks-r.raidCooldownTicks)/float32(raidDefaultPreTicks), 0, 1)
			} else if r.raidCooldownTicks == 0 && r.groupsSpawned > 0 {
				r.raidCooldownTicks = raidDefaultPreTicks
				r.bossEvent.name = "event.minecraft.raid"
				return
			}
		}
		if r.ticksActive%20 == 0 {
			t.raidUpdateRaiders(r)
			raidersAlive = r.getTotalRaidersAlive()
			if raidersAlive > 0 && raidersAlive <= raidLowMobThreshold {
				r.bossEvent.name = "event.minecraft.raid.raiders_remaining"
			} else {
				r.bossEvent.name = "event.minecraft.raid"
			}
		}
		// The wave-spawn loop (VERIFIED CFR): while shouldSpawnGroup(), place a wave at the center. The
		// attempt cap (>5 -> stop) is kept; spawnPos is the center (findRandomSpawnPos reduced), always
		// non-null, so the attempt path never trips in v1 (the counter is structurally present).
		attempt := 0
		for r.shouldSpawnGroup() {
			spawnOK := t.raidSpawnGroup(r)
			if spawnOK {
				r.started = true
			} else {
				attempt++
			}
			if attempt > 5 {
				r.stop()
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
			r.stop()
			return
		}
		if r.celebrationTicks%20 == 0 {
			r.bossEvent.visible = true
			if r.isVictory() {
				r.bossEvent.progress = 0
				r.bossEvent.name = "event.minecraft.raid.victory"
			} else {
				r.bossEvent.name = "event.minecraft.raid.defeat"
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
	r.updateBossbar()
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
	r.updateBossbar()
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
		r.raidRemoveRaider(raider, true)
	}
}

// raidRemoveRaider ports Raid.removeFromRaid(level, raider, removeFromTotalHealth): drop the raider from
// its wave set, subtract its health from totalHealth, clear its currentRaid back-pointer, refresh the bar.
func (r *Raid) raidRemoveRaider(raider *Entity, removeFromTotalHealth bool) {
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
	r.updateBossbar()
}
