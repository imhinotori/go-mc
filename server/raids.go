package server

import pk "github.com/imhinotori/sulfur/net/packet"

// raids.go — the per-level Raids MANAGER (net.minecraft.world.entity.raid.Raids, the SavedData that
// holds every active Raid for a level), ported 1:1 from the unobfuscated 26.2 jar (CFR this session).
// It owns the raid map keyed by an incrementing id, ticks every raid once per tick, and prunes STOPPED
// raids. It hangs off the region (raidsManager field) exactly as vanilla's Raids hangs off ServerLevel
// (ServerLevel.getRaids()); the coordinator ticks it at the quiescent post-fan-out barrier.
//
// 1:1 ANCHOR (VERIFIED CFR net.minecraft.world.entity.raid.Raids):
//   - tick(level): ++tick; for each raid: (RAIDS gamerule off -> raid.stop()); if raid.isStopped() ->
//     remove + setDirty; else raid.tick(level). (The RAIDS gamerule + setDirty persistence are reduced
//     — no gamerule/SavedData subsystem; the prune + per-raid tick are faithful.)
//   - nextId starts 1; getUniqueId() == ++nextId. createOrExtendRaid (the bad-omen village trigger) is
//     CITE-DEFERRED (it reads level.getPoiManager().getInRange(VILLAGE) — no POI subsystem). In its place
//     createRaidAt is the test/dbg SEAM: it builds a Raid at a caller-supplied center+difficulty and
//     registers it, so the raid loop is fully EXERCISED and hasActiveRaid becomes real+testable.
//   - getNearbyRaid / getRaidAt: find the active raid whose center is within maxDistSqr (RAID_REMOVAL_
//     THRESHOLD_SQR for getRaidAt). Used by Raider.hasActiveRaid's fallback (the entity's currentRaid
//     back-pointer is the primary read; this is the positional lookup).

// raidsManager is the ported Raids SavedData (per-region). Single-owner (TICK-05): mutated ONLY on the
// coordinator goroutine (raidsTick runs at the barrier), so it needs no lock. It lives on the region.
type raidsManager struct {
	raidMap map[int]*Raid
	nextId  int // Raids.nextId — starts 1; getUniqueId returns ++nextId
	tick    int // Raids.tick — the manager's own tick counter
}

func newRaidsManager() *raidsManager {
	return &raidsManager{raidMap: map[int]*Raid{}, nextId: 1}
}

// getUniqueId ports Raids.getUniqueId(): ++nextId.
func (rm *raidsManager) getUniqueId() int {
	rm.nextId++
	return rm.nextId
}

// get ports Raids.get(int): the raid with that id, or nil.
func (rm *raidsManager) get(id int) *Raid { return rm.raidMap[id] }

// raidCount is a test/introspection helper: the number of registered raids.
func (rm *raidsManager) raidCount() int { return len(rm.raidMap) }

// getRaidAt ports Raids.getRaidAt(pos) (via getNearbyRaid with RAID_REMOVAL_THRESHOLD_SQR): the closest
// ACTIVE raid whose center is within 12544 of pos, or nil. This is the positional membership lookup
// Raider.hasRaid()/hasActiveRaid's fallback uses when the entity has no currentRaid back-pointer yet.
func (rm *raidsManager) getRaidAt(x, y, z float64) *Raid {
	const maxDistSqr = 12544.0
	var closest *Raid
	closestDistSqr := maxDistSqr
	for _, raid := range rm.raidMap {
		if !raid.isActive() {
			continue
		}
		dx := float64(raid.centerX) - x
		dy := float64(raid.centerY) - y
		dz := float64(raid.centerZ) - z
		d := dx*dx + dy*dy + dz*dz
		if d < closestDistSqr {
			closest = raid
			closestDistSqr = d
		}
	}
	return closest
}

// getRaidAtBlock is the BlockPos overload of Raids.getRaidAt(BlockPos): the closest ACTIVE raid whose
// center is within RAID_REMOVAL_THRESHOLD_SQR of the block pos, or nil. It is what createOrExtendRaid ->
// getOrCreateRaid and the bad-omen guard read. (getRaidAt(float) already implements getNearbyRaid with the
// threshold; this just adapts the integer BlockPos.)
func (rm *raidsManager) getRaidAtBlock(pos pk.Position) *Raid {
	return rm.getRaidAt(float64(pos.X), float64(pos.Y), float64(pos.Z))
}

// createRaidAt is the test/dbg SEAM that stands in for the CITE-DEFERRED Raids.createOrExtendRaid
// (the bad-omen village auto-trigger — deferred, no POI subsystem). It builds a Raid at (cx,cy,cz) with
// the given difficulty + raid-omen level, registers it under a fresh id, and returns it. The raid's RNG
// is seeded from its id (deterministic; the raid stream is not vanilla-seed-pinned). This makes the raid
// loop startable + hasActiveRaid observable without the POI/omen chain.
func (rm *raidsManager) createRaidAt(cx, cy, cz int, d difficulty, raidOmenLevel int) *Raid {
	id := rm.getUniqueId()
	raid := newRaid(id, cx, cy, cz, d, uint64(id)^raidCreateSeedSalt)
	if raidOmenLevel < 0 {
		raidOmenLevel = 0
	}
	if raidOmenLevel > raidDefaultMaxOmen {
		raidOmenLevel = raidDefaultMaxOmen
	}
	raid.raidOmenLevel = raidOmenLevel
	rm.raidMap[id] = raid
	return raid
}

// raidCreateSeedSalt de-correlates the raid RNG stream from the entity streams (a nothing-up-my-sleeve
// mix) while staying deterministic per raid id.
const raidCreateSeedSalt uint64 = 0xA24BAED4963EE407

// raidsTick ports Raids.tick(ServerLevel): ++tick, tick every raid, prune STOPPED ones. Runs on the
// coordinator at the quiescent barrier (raidsTickAllRegions). The RAIDS gamerule short-circuit +
// setDirty persistence are reduced (no gamerule/SavedData subsystem); the per-raid tick + STOPPED prune
// are faithful.
func (t *TickLoop) raidsTick(rm *raidsManager) {
	rm.tick++
	for id, raid := range rm.raidMap {
		if raid.isStopped() {
			delete(rm.raidMap, id)
			continue
		}
		t.tickRaid(raid)
		if raid.isStopped() {
			delete(rm.raidMap, id)
		}
	}
}

// raidsTickAllRegions ticks every region's raidsManager once, on the coordinator, at the quiescent
// post-fan-out barrier (all regions joined -> cross-region entity spawns/reads are legal). Wired into
// tickOnce after applyCrossRegionDamage. A region with no raidsManager (never had a raid) is a no-op.
func (t *TickLoop) raidsTickAllRegions() {
	for _, r := range t.regions {
		if r.raidsManager == nil {
			continue
		}
		t.withRegion(r, func() { t.raidsTick(r.raidsManager) })
	}
}

// ensureRaidsManager lazily constructs the region's raidsManager (a raid is a rare event; most regions
// never have one). Coordinator-only.
func (r *region) ensureRaidsManager() *raidsManager {
	if r.raidsManager == nil {
		r.raidsManager = newRaidsManager()
	}
	return r.raidsManager
}
