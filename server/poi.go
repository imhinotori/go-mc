package server

// poi.go — the POINT-OF-INTEREST subsystem (net.minecraft.world.entity.ai.village.poi.*), ported 1:1
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap/CFR this session). It is the
// foundation villages/raids/villager-jobs read: a per-section store of POI records (a BlockPos + a
// PoiType + a free-ticket count), a block-state -> PoiType map, and the spatial queries
// (getInRange/getInSquare/findClosest) plus the village-center machinery (isVillage/sectionsToVillage)
// that the bad-omen -> raid auto-trigger needs.
//
// SCOPE (this landing): the PoiManager storage + occupancy/ticket system + the village-for-raid query.
// The POI TYPE table is landed for HOME (beds) and MEETING (bells) — the two village-relevant types the
// raid center query reads (PoiTypeTags.VILLAGE). The villager JOB-SITE POI types (armorer/farmer/... —
// the #acquirable_job_site members) are CITE-DEFERRED: there are no villagers to claim job sites yet, so
// their block-state mappings and registrations are omitted and slot in behind poiTypeForState later.
// See .planning/FINAL-MILESTONE-PARITY.md.
//
// 1:1 ANCHORS (VERIFIED this session):
//   - PoiManager extends SectionStorage; add(pos,type)/remove(pos)/getInRange/getInSquare/findClosest;
//     getCountInRange; Occupancy{HAS_SPACE,IS_OCCUPIED,ANY}. VILLAGE_SECTION_SIZE=1, MAX_VILLAGE_DISTANCE=6.
//     (net.minecraft.world.entity.ai.village.poi.PoiManager)
//   - PoiRecord{pos, poiType, freeTickets}; ctor freeTickets = poiType.maxTickets(); acquireTicket
//     (freeTickets>0 -> --), releaseTicket (freeTickets<maxTickets -> ++), hasSpace (freeTickets>0),
//     isOccupied (freeTickets != maxTickets). (PoiRecord)
//   - PoiSection: records keyed by SectionPos.sectionRelativePos (a short); add returns null on a
//     same-type collision at the cell. (PoiSection.add/remove/getRecords)
//   - PoiType(matchingStates, maxTickets, validRange); HOME=(beds,1,1), MEETING=(bell,32,6). PoiTypes.
//     forState -> Optional<Holder<PoiType>>. (PoiTypes.bootstrap/register/forState)
//   - ServerLevel.updatePOIOnBlockStateChange(pos, old, new): oldType=forState(old), newType=forState(new);
//     equal -> return; else remove(old-had-poi), add(new-has-poi). (ServerLevel.updatePOIOnBlockStateChange)
//   - PoiManager.DistanceTracker (SectionTracker, MAX 6): getLevelFromSource = isVillageCenter?0:7;
//     isVillageCenter(sectionLong) = section has an IS_OCCUPIED record tagged #village.
//     ServerLevel.isVillage(pos) = isCloseToVillage(pos,1) = sectionsToVillage(SectionPos.of(pos)) <= 1.
//   - Raids.createOrExtendRaid: getInRange(#village, raidPos, 64, IS_OCCUPIED) -> average the POI
//     positions -> raid center (else raidPos). (Raids.createOrExtendRaid)
//
// SINGLE-OWNER (TICK-05): the poiManager hangs off region (region.go), mutated ONLY on the region's
// goroutine (the block place/break hooks + the coordinator-barrier village scan), so it needs no lock —
// exactly like raidsManager. Persistence (SectionStorage save/load) is REDUCED: POI is rebuilt from the
// runtime block edits; no poi/*.mca is written yet (cite-deferred — no villager memory to persist).

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// poiType is net.minecraft.world.entity.ai.village.poi.PoiType (record{matchingStates,maxTickets,validRange}).
// matchingStates is represented as the poiTypeForState predicate rather than an inlined Set<BlockState> (the
// villager job sites can extend it), but the numeric fields are literal from PoiTypes.bootstrap.
type poiType struct {
	key        string // the ResourceKey path (e.g. "minecraft:home") — the Holder identity for #village tests
	maxTickets int
	validRange int
}

// The two village-relevant POI types (VERIFIED PoiTypes.bootstrap):
//   HOME    = register(HOME,    BEDS,          1,  1)
//   MEETING = register(MEETING, Blocks.BELL,   32, 6)
// Job-site types (ARMORER..WEAPONSMITH, the #acquirable_job_site members) are cite-deferred (no villagers).
var (
	poiTypeHome    = &poiType{key: "minecraft:home", maxTickets: 1, validRange: 1}
	poiTypeMeeting = &poiType{key: "minecraft:meeting", maxTickets: 32, validRange: 6}
)

// poiTypeVillage reports whether a poiType is a member of PoiTypeTags.VILLAGE. VERIFIED
// registrydata/tags/point_of_interest_type/village.json = {#acquirable_job_site, home, meeting}. With the
// job sites deferred, the landed village members are HOME and MEETING; the #acquirable_job_site branch is
// cite-deferred (adds the job-site keys here when villager POIs land). This is the holder.is(#village)
// predicate the raid center query and isVillageCenter use.
func poiTypeVillage(pt *poiType) bool {
	return pt == poiTypeHome || pt == poiTypeMeeting
}

// poiTypeForState is PoiTypes.forState(BlockState): the block-state -> PoiType map (Optional). A bed state
// -> HOME, a bell state -> MEETING, else no POI. VERIFIED PoiTypes.bootstrap block sets (BEDS via
// #minecraft:beds; MEETING via Blocks.BELL). The job-site block sets (composter/lectern/...) are deferred.
func poiTypeForState(s block.StateID) *poiType {
	if isBedBlock(s) { // #minecraft:beds -> HOME
		return poiTypeHome
	}
	if isBellBlock(s) { // Blocks.BELL -> MEETING
		return poiTypeMeeting
	}
	return nil
}

// isBellBlock reports whether the state is a bell (every bell state maps to MEETING, exactly as
// PoiTypes.getBlockStates(Blocks.BELL) enumerates them). Matched by block id — the bell is a single block
// whose attachment/facing/powered properties are all MEETING states.
func isBellBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:bell"
}

// poiRecord is net.minecraft.world.entity.ai.village.poi.PoiRecord: an immutable pos + poiType and a
// mutable freeTickets count. freeTickets starts at poiType.maxTickets (the PoiRecord(pos,type,setDirty)
// ctor). setDirty is reduced (no SectionStorage persistence yet); the occupancy math is faithful.
type poiRecord struct {
	pos         pk.Position
	poiType     *poiType
	freeTickets int
}

// newPoiRecord is PoiRecord(pos, poiType, setDirty): freeTickets = poiType.maxTickets().
func newPoiRecord(pos pk.Position, pt *poiType) *poiRecord {
	return &poiRecord{pos: pos, poiType: pt, freeTickets: pt.maxTickets}
}

// acquireTicket ports PoiRecord.acquireTicket: if freeTickets<=0 return false; else --freeTickets, true.
func (r *poiRecord) acquireTicket() bool {
	if r.freeTickets <= 0 {
		return false
	}
	r.freeTickets--
	return true
}

// releaseTicket ports PoiRecord.releaseTicket: if freeTickets>=maxTickets return false; else ++, true.
func (r *poiRecord) releaseTicket() bool {
	if r.freeTickets >= r.poiType.maxTickets {
		return false
	}
	r.freeTickets++
	return true
}

// hasSpace ports PoiRecord.hasSpace: freeTickets > 0.
func (r *poiRecord) hasSpace() bool { return r.freeTickets > 0 }

// isOccupied ports PoiRecord.isOccupied: freeTickets != maxTickets. A FRESH POI (freeTickets==maxTickets)
// is NOT occupied — it becomes occupied only when a ticket is acquired (a villager claims the bed/bell).
func (r *poiRecord) isOccupied() bool { return r.freeTickets != r.poiType.maxTickets }

// poiOccupancy is PoiManager$Occupancy: the record filter applied to every query. VERIFIED:
// HAS_SPACE=hasSpace, IS_OCCUPIED=isOccupied, ANY=true.
type poiOccupancy int

const (
	poiOccupancyHasSpace   poiOccupancy = iota // Occupancy.HAS_SPACE   -> r.hasSpace()
	poiOccupancyIsOccupied                     // Occupancy.IS_OCCUPIED -> r.isOccupied()
	poiOccupancyAny                            // Occupancy.ANY         -> true
)

// test ports Occupancy.getTest().test(record).
func (o poiOccupancy) test(r *poiRecord) bool {
	switch o {
	case poiOccupancyHasSpace:
		return r.hasSpace()
	case poiOccupancyIsOccupied:
		return r.isOccupied()
	default: // ANY
		return true
	}
}

// sectionRelativePos ports SectionPos.sectionRelativePos(BlockPos): (relX<<8)|(relZ<<4)|relY, where
// sectionRelative(n) = n & 15. The short key into a PoiSection's records map. Returns int16 (a Java short).
func sectionRelativePos(pos pk.Position) int16 {
	relX := pos.X & 15
	relY := pos.Y & 15
	relZ := pos.Z & 15
	return int16(relX<<8 | relZ<<4 | relY)
}

// sectionPosLong ports SectionPos.asLong(BlockPos): pack the SECTION coords (blockToSection = >>4) as
// x:22bits<<42 | z:22bits<<20 | y:20bits. The section key into the PoiManager's section map. VERIFIED
// SectionPos.asLong(int,int,int).
func sectionPosLong(pos pk.Position) int64 {
	return sectionLongFromCoords(pos.X>>4, pos.Y>>4, pos.Z>>4)
}

// sectionLongFromCoords packs section coords directly (also used by the village-distance scan).
func sectionLongFromCoords(sx, sy, sz int) int64 {
	return (int64(sx)&0x3FFFFF)<<42 | (int64(sz)&0x3FFFFF)<<20 | (int64(sy) & 0xFFFFF)
}

// poiSection is net.minecraft.world.entity.ai.village.poi.PoiSection: the records for one 16^3 section,
// keyed by their section-relative short. (The byType index is folded into a linear scan of records — the
// section is at most 4096 cells; query cost is dominated by the section iteration either way.)
type poiSection struct {
	records map[int16]*poiRecord
}

func newPoiSection() *poiSection { return &poiSection{records: map[int16]*poiRecord{}} }

// add ports PoiSection.add(pos,type): if a record of the SAME type already occupies the cell, return nil
// (the vanilla "add returns null on same-type collision"); otherwise create + store the record.
func (s *poiSection) add(pos pk.Position, pt *poiType) *poiRecord {
	key := sectionRelativePos(pos)
	if existing, ok := s.records[key]; ok && existing.poiType == pt {
		return nil
	}
	rec := newPoiRecord(pos, pt)
	s.records[key] = rec
	return rec
}

// remove ports PoiSection.remove(pos): drop the record at the cell (a no-op if none — vanilla logs an
// error; the reduced form just returns).
func (s *poiSection) remove(pos pk.Position) {
	delete(s.records, sectionRelativePos(pos))
}

// poiManager is net.minecraft.world.entity.ai.village.poi.PoiManager (per-region). It owns the loaded POI
// sections keyed by SectionPos.asLong and a lazily-recomputed village-distance cache. Single-owner (region
// goroutine).
type poiManager struct {
	sections map[int64]*poiSection

	// villageDist caches sectionsToVillage results; cleared whenever a village-relevant POI changes
	// (add/remove). This stands in for PoiManager$DistanceTracker's incremental SectionTracker: the
	// observable result (min Chebyshev section distance to an IS_OCCUPIED #village section, capped at
	// MAX_VILLAGE_DISTANCE=6) is identical; the propagation is recomputed lazily on demand instead of
	// incrementally — a pure OPTIMIZATION/reduction of the tracker (no gameplay difference).
	villageDist map[int64]int

	// dirty is the SectionStorage/SavedData dirty flag (PoiManager -> SectionStorage.setDirty). SET by
	// add/remove (a POI record changed -> the owning section's chunk is dirty); the periodic save
	// (poi_persist.go) CLEARS it after a successful flush. A loaded manager starts clean. Region-owned.
	dirty bool
}

func newPoiManager() *poiManager {
	return &poiManager{sections: map[int64]*poiSection{}, villageDist: map[int64]int{}}
}

// setDirty marks the manager for the next POI save pass (SectionStorage.setDirty(sectionPos)).
func (m *poiManager) setDirty() { m.dirty = true }

// isDirty reports whether an unsaved POI mutation is pending (the SavedData contract).
func (m *poiManager) isDirty() bool { return m.dirty }

// clearDirty is called by the save pass after a successful flush (SavedData.setDirty(false)).
func (m *poiManager) clearDirty() { m.dirty = false }

// getOrCreate is SectionStorage.getOrCreate(long): the section, created empty if absent.
func (m *poiManager) getOrCreate(sectionLong int64) *poiSection {
	s := m.sections[sectionLong]
	if s == nil {
		s = newPoiSection()
		m.sections[sectionLong] = s
	}
	return s
}

// add ports PoiManager.add(pos, type): getOrCreate(SectionPos.asLong(pos)).add(pos, type). Returns the
// created record (or nil on a same-type collision). Invalidates the village-distance cache.
func (m *poiManager) add(pos pk.Position, pt *poiType) *poiRecord {
	rec := m.getOrCreate(sectionPosLong(pos)).add(pos, pt)
	if rec != nil {
		m.villageDist = map[int64]int{}
		m.setDirty() // PoiSection.add -> setDirty (a new record was stored in the section)
	}
	return rec
}

// remove ports PoiManager.remove(pos): getOrLoad(section).ifPresent(s -> s.remove(pos)). Invalidates the
// village-distance cache (the removed POI may have been a village source).
func (m *poiManager) remove(pos pk.Position) {
	if s := m.sections[sectionPosLong(pos)]; s != nil {
		s.remove(pos)
		m.villageDist = map[int64]int{}
		m.setDirty() // PoiSection.remove -> setDirty (a record was dropped from the section)
	}
}

// recordAt is a thin lookup (PoiManager.getFreeTickets/getInSquare cell probe analogue): the record at
// pos, or nil. Used by the villager-claim path (acquire/releaseTicket) once villagers land.
func (m *poiManager) recordAt(pos pk.Position) *poiRecord {
	if s := m.sections[sectionPosLong(pos)]; s != nil {
		return s.records[sectionRelativePos(pos)]
	}
	return nil
}

// getInSquare ports PoiManager.getInSquare(predicate, center, radius, occupancy): every record whose type
// passes predicate, whose occupancy passes occupancy, and whose |dx|<=radius && |dz|<=radius (an X/Z
// square, NOT Y-bounded). VERIFIED PoiManager.getInSquare (the chunk-range + section iteration is folded
// into a scan of the loaded sections — same result set; the loaded-section map IS the in-range set).
func (m *poiManager) getInSquare(predicate func(*poiType) bool, center pk.Position, radius int, occ poiOccupancy) []*poiRecord {
	var out []*poiRecord
	for _, sec := range m.sections {
		for _, rec := range sec.records {
			if predicate != nil && !predicate(rec.poiType) {
				continue
			}
			if !occ.test(rec) {
				continue
			}
			if absInt(rec.pos.X-center.X) <= radius && absInt(rec.pos.Z-center.Z) <= radius {
				out = append(out, rec)
			}
		}
	}
	return out
}

// getInRange ports PoiManager.getInRange: getInSquare then filter distSqr(center) <= radius*radius.
func (m *poiManager) getInRange(predicate func(*poiType) bool, center pk.Position, radius int, occ poiOccupancy) []*poiRecord {
	radiusSqr := float64(radius * radius)
	square := m.getInSquare(predicate, center, radius, occ)
	out := square[:0:0]
	for _, rec := range square {
		if poiDistSqr(rec.pos, center) <= radiusSqr {
			out = append(out, rec)
		}
	}
	return out
}

// getCountInRange ports PoiManager.getCountInRange: getInRange(...).count().
func (m *poiManager) getCountInRange(predicate func(*poiType) bool, center pk.Position, radius int, occ poiOccupancy) int {
	return len(m.getInRange(predicate, center, radius, occ))
}

// findClosest ports PoiManager.findClosest(predicate, center, radius, occupancy): the pos of the record in
// range minimizing distSqr(center), or (zero,false) if none. VERIFIED (map to getPos, min by comparingDouble).
func (m *poiManager) findClosest(predicate func(*poiType) bool, center pk.Position, radius int, occ poiOccupancy) (pk.Position, bool) {
	best := math.Inf(1)
	var bestPos pk.Position
	found := false
	for _, rec := range m.getInRange(predicate, center, radius, occ) {
		d := poiDistSqr(rec.pos, center)
		if d < best {
			best = d
			bestPos = rec.pos
			found = true
		}
	}
	return bestPos, found
}

// isVillageCenter ports PoiManager.isVillageCenter(sectionLong): the section has at least one IS_OCCUPIED
// record tagged #village. VERIFIED (section.getRecords(#village, IS_OCCUPIED).findAny().isPresent()).
func (m *poiManager) isVillageCenter(sectionLong int64) bool {
	s := m.sections[sectionLong]
	if s == nil {
		return false
	}
	for _, rec := range s.records {
		if poiTypeVillage(rec.poiType) && rec.isOccupied() {
			return true
		}
	}
	return false
}

// sectionsToVillage ports PoiManager.sectionsToVillage(SectionPos): the DistanceTracker distance (min
// number of sections, Chebyshev over the SectionTracker's 3D neighbourhood) from the given section to the
// nearest IS_OCCUPIED #village section, capped at MAX_VILLAGE_DISTANCE (6); 7 means "no village within 6".
// VERIFIED DistanceTracker (source level 0 at a village center; getLevel default 7; setLevel drops >6).
// SectionTracker propagates over the 26-neighbour 3D section adjacency (SectionTracker spread=1), so the
// distance is Chebyshev in section space. Recomputed lazily (see poiManager.villageDist doc).
func (m *poiManager) sectionsToVillage(sx, sy, sz int) int {
	const maxVillageDistance = 6 // MAX_VILLAGE_DISTANCE
	key := sectionLongFromCoords(sx, sy, sz)
	if v, ok := m.villageDist[key]; ok {
		return v
	}
	best := maxVillageDistance + 1 // 7 == "no village" (DistanceTracker default level)
	for secLong := range m.sections {
		if !m.isVillageCenter(secLong) {
			continue
		}
		csx, csy, csz := unpackSectionLong(secLong)
		d := chebyshev3(sx-csx, sy-csy, sz-csz)
		if d < best {
			best = d
		}
	}
	m.villageDist[key] = best
	return best
}

// isVillage ports ServerLevel.isVillage(BlockPos) -> isCloseToVillage(pos,1) -> sectionsToVillage(
// SectionPos.of(pos)) <= 1. VERIFIED.
func (m *poiManager) isVillage(pos pk.Position) bool {
	return m.sectionsToVillage(pos.X>>4, pos.Y>>4, pos.Z>>4) <= 1
}

// --- small numeric helpers (kept local to POI; no gameplay of their own) ---

func unpackSectionLong(v int64) (sx, sy, sz int) {
	sx = int(signExtend22(v >> 42))
	sz = int(signExtend22(v >> 20))
	sy = int(signExtend20(v))
	return
}

func signExtend22(v int64) int64 {
	v &= 0x3FFFFF
	if v&0x200000 != 0 {
		v |= ^int64(0x3FFFFF)
	}
	return v
}

func signExtend20(v int64) int64 {
	v &= 0xFFFFF
	if v&0x80000 != 0 {
		v |= ^int64(0xFFFFF)
	}
	return v
}

func chebyshev3(dx, dy, dz int) int {
	return poiMax(poiMax(absInt(dx), absInt(dy)), absInt(dz))
}

// poiDistSqr ports BlockPos.distSqr(Vec3i): integer corner distance dx*dx+dy*dy+dz*dz as a double.
// VERIFIED Vec3i.distSqr.
func poiDistSqr(a, b pk.Position) float64 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	dz := float64(a.Z - b.Z)
	return dx*dx + dy*dy + dz*dz
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func poiMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ============================================================================================
// TickLoop / region plumbing: lazy construction, the block-state-change POI hook, and the
// createOrExtendRaid village-center trigger.

// ensurePoiManager lazily constructs the region's poiManager (a region with no beds/bells never has one),
// mirroring ensureRaidsManager. Region-goroutine-owned.
func (r *region) ensurePoiManager() *poiManager {
	if r.poiManager == nil {
		r.poiManager = newPoiManager()
	}
	return r.poiManager
}

// updatePoiOnBlockStateChange ports ServerLevel.updatePOIOnBlockStateChange(pos, oldState, newState):
// oldType = forState(old), newType = forState(new); if they are the same PoiType (or both nil) nothing
// changes; otherwise the old POI is removed and the new POI is added. Called from the block place/break
// seams (the LevelChunk.setBlockState POI-update analogue). VERIFIED ServerLevel.updatePOIOnBlockStateChange.
// Runs in the owning region's context (t.cur()).
func (t *TickLoop) updatePoiOnBlockStateChange(pos pk.Position, oldState, newState block.StateID) {
	oldType := poiTypeForState(oldState)
	newType := poiTypeForState(newState)
	if oldType == newType {
		return // Objects.equals(oldType, newType) -> no POI change
	}
	if oldType != nil {
		t.cur().ensurePoiManager().remove(pos)
	}
	if newType != nil {
		t.cur().ensurePoiManager().add(pos, newType)
	}
}

// createOrExtendRaid ports Raids.createOrExtendRaid(ServerPlayer, BlockPos): the bad-omen village raid
// trigger (fired by RaidOmenMobEffect on omen expiry). It queries the region's POI manager for occupied
// #village POIs within radius 64, averages their positions to pick the raid CENTER (falling back to the
// raid position when there are none), gets-or-creates the raid at that center, registers it, and absorbs
// the player's raid-omen level. VERIFIED CFR Raids.createOrExtendRaid + getOrCreateRaid.
//
// (RAIDS gamerule + EnvironmentAttributes.CAN_START_RAID guards are cite-deferred — no gamerule/dimension-
// attribute subsystem; a live non-peaceful world always passes. isSpectator() is a const-false analogue.)
func (t *TickLoop) createOrExtendRaid(p *tickPlayer, raidPosition pk.Position) *Raid {
	pm := t.cur().poiManager

	// List<PoiRecord> posses = poiManager.getInRange(#village, raidPosition, 64, IS_OCCUPIED).
	var raidCenter pk.Position
	count := 0
	var sumX, sumY, sumZ int
	if pm != nil {
		for _, rec := range pm.getInRange(poiTypeVillage, raidPosition, 64, poiOccupancyIsOccupied) {
			sumX += rec.pos.X
			sumY += rec.pos.Y
			sumZ += rec.pos.Z
			count++
		}
	}
	if count > 0 {
		// BlockPos.containing(posTotals.scale(1/count)) == Mth.floor per axis.
		raidCenter = pk.Position{
			X: floorInt(float64(sumX) / float64(count)),
			Y: floorInt(float64(sumY) / float64(count)),
			Z: floorInt(float64(sumZ) / float64(count)),
		}
	} else {
		raidCenter = raidPosition
	}

	rm := t.cur().ensureRaidsManager()

	// getOrCreateRaid: an existing raid within RAID_REMOVAL_THRESHOLD_SQR of the center, else a new one.
	raid := rm.getRaidAtBlock(raidCenter)
	if raid == nil {
		// new Raid(center, level.getDifficulty()) — registered under a fresh unique id, seeded from that id
		// (deterministic; the createRaidAt seam uses the same salt so both entry points share the RNG stream).
		id := rm.getUniqueId()
		raid = newRaid(id, raidCenter.X, raidCenter.Y, raidCenter.Z, serverDifficulty, uint64(id)^raidCreateSeedSalt)
		rm.raidMap[id] = raid
	}

	// if (!raid.isStarted() || raid.getRaidOmenLevel() < raid.getMaxRaidOmenLevel()) raid.absorbRaidOmen(player).
	if !raid.isStarted() || raid.getRaidOmenLevel() < raid.getMaxRaidOmenLevel() {
		raid.absorbRaidOmen(p)
	}
	return raid
}

// playerBlockPos is ServerPlayer.blockPosition(): floor of the player's position (BlockPos.containing).
func playerBlockPos(p *tickPlayer) pk.Position {
	return pk.Position{X: floorInt(p.x), Y: floorInt(p.y), Z: floorInt(p.z)}
}

// floorInt is Mth.floor(double): the floor toward negative infinity (BlockPos.containing per-axis).
func floorInt(v float64) int { return int(math.Floor(v)) }
