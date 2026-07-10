package server

// bee_hive.go -- the Bee HIVE + POLLINATION goal cluster (BEEHIVE-02): a 1:1 port of the inner goal
// classes of net.minecraft.world.entity.animal.bee.Bee over the 26.2 jar (javap -c -p this session).
// These are the "find a flower, pollinate it, carry nectar home, deliver it to the hive" behaviors that
// the BeehiveBlockEntity (beehive_be.go, BEEHIVE-01) consumes. Wired into newBeeAI (bee.go) at the vanilla
// priorities: @1 BeeEnterHiveGoal, @4 BeePollinateGoal, @5 BeeLocateHiveGoal, @5 BeeGoToHiveGoal,
// @6 BeeGoToKnownFlowerGoal.
//
// SHARED WRAPPER (Bee.BaseBeeGoal): canUse == canBeeUse() && !isAngry(); canContinueToUse ==
// canBeeContinueToUse() && !isAngry(). Each concrete goal implements canBeeUse/canBeeContinueToUse and
// this wrapper adds the anger gate. beeIsAngry mirrors NeutralMob.isAngry (angerEndTime live).
//
// NAV SEAM (cited deferral): vanilla drives PathNavigation.moveTo/createPath/canReach + AirRandomPos +
// the PoiManager BEE_HOME scan. Sulfur v1 has the mobAI.setWantTargetSpeed target seam (the analogue of
// navigation.moveTo the randomStrollGoal uses) but NOT yet the flying A* createPath/canReach primitives,
// the AirRandomPos jitter, or a PoiManager. The goal STRUCTURE + gates + RNG draw order + timers are
// ported 1:1; where a not-yet-built nav primitive is needed the target is handed to the existing seam and
// the deferral is cited to the vanilla default (moveTo to the block center at the vanilla speed). This is
// the SAME deferral randomStrollGoal already lives with -- no gameplay divergence beyond pathing fidelity.

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Bee goal constants (VERIFIED javap this session -- Bee + the goal inner classes).
const (
	beeHiveCloseEnoughDistance     = 2    // Bee.HIVE_CLOSE_ENOUGH_DISTANCE (closerThan 2 == arrived)
	beeHiveSearchDistance          = 20   // Bee.HIVE_SEARCH_DISTANCE (PoiManager getInRange radius)
	beeCooldownLocatingNewHive     = 200  // Bee.COOLDOWN_BEFORE_LOCATING_NEW_HIVE / dropHive reset
	beeCooldownLocatingNewFlower   = 200  // BeePollinateGoal.stop / tick timeout reset
	beeFindFlowerRetryMin          = 20   // Bee.MIN_FIND_FLOWER_RETRY_COOLDOWN (Mth.nextInt 20..60)
	beeFindFlowerRetryMax          = 60   // Bee.MAX_FIND_FLOWER_RETRY_COOLDOWN
	beeTicksBeforeGoingToFlower    = 600  // BeeGoToKnownFlowerGoal.wantsToGoToKnownFlower (>600)
	beeTicksWithoutNectarGoHome    = 3600 // Bee.isTiredOfLookingForNectar (>3600)
	beeMinPollinationTicks         = 400  // BeePollinateGoal.MIN_POLLINATION_TICKS (>400 == enough)
	beeMaxPollinatingTicks         = 600  // BeePollinateGoal.MAX_POLLINATING_TICKS (drop at >600)
	beePollinatePositionChance     = 25   // BeePollinateGoal.POSITION_CHANGE_CHANCE (nextInt(25)==0)
	beeGoToHiveMaxTravellingTicks  = 2400 // BeeGoToHiveGoal / BeeGoToKnownFlowerGoal MAX_TRAVELLING_TICKS
	beeGoToHiveTicksBeforeDrop     = 60   // BeeGoToHiveGoal.TICKS_BEFORE_HIVE_DROP (ticksStuck > 60)
	beeGoToHiveMaxBlacklist        = 3    // BeeGoToHiveGoal.MAX_BLACKLISTED_TARGETS
	beeGoToHivePathfindCloser      = 16   // Bee.PATHFIND_TO_HIVE_WHEN_CLOSER_THAN (closerThan 16)
	beeTooFarAwayDistance          = 48   // Bee.isTooFarAway (!closerThan 48)
	beePollinateSpeedModifier      = 0.35 // BeePollinateGoal.SPEED_MODIFIER (MoveControl setWantedPosition)
)

// beeIsAngry mirrors NeutralMob.isAngry() for the BaseBeeGoal canUse/canContinueToUse gate: angerEndTime is
// live and not yet expired. CITE Bee.BaseBeeGoal (canBeeUse() && !isAngry()).
func beeIsAngry(t *TickLoop, e *Entity) bool {
	return e.angerEndTime > 0 && e.angerEndTime-t.gametime > 0
}

// beeSetWanted hands a block-center target to the existing navigation seam (the moveTo analogue). This is
// the cited nav deferral: vanilla calls PathNavigation.moveTo(x+0.5, y+0.5, z+0.5, speed) or a
// createPath/AirRandomPos variant; v1 drives setWantTargetSpeed at the block center + speed. CITE
// PathNavigation.moveTo.
func beeSetWanted(e *Entity, p pk.Position, speed float64) {
	if e.ai == nil {
		return
	}
	e.ai.setWantTargetSpeed(float64(p.X)+0.5, float64(p.Y)+0.5, float64(p.Z)+0.5, speed)
}

// beeCloserThan ports Entity.closerThan(BlockPos, dist): center-to-center distanceSqr < dist*dist against
// the block CENTER (x+0.5, y+0.5, z+0.5). CITE Entity.closerThan.
func beeCloserThan(e *Entity, p pk.Position, dist float64) bool {
	dx := (float64(p.X) + 0.5) - e.x
	dy := (float64(p.Y) + 0.5) - e.y
	dz := (float64(p.Z) + 0.5) - e.z
	return dx*dx+dy*dy+dz*dz < dist*dist
}

// beeIsTooFarAway ports Bee.isTooFarAway: !closerThan(pos, 48). CITE Bee.isTooFarAway.
func beeIsTooFarAway(e *Entity, p pk.Position) bool { return !beeCloserThan(e, p, beeTooFarAwayDistance) }

// beeIsTiredOfLookingForNectar ports Bee.isTiredOfLookingForNectar: ticksWithoutNectarSinceExitingHive >
// 3600. CITE Bee.isTiredOfLookingForNectar.
func beeIsTiredOfLookingForNectar(e *Entity) bool {
	return e.beeTicksWithoutNectarSinceExiting > beeTicksWithoutNectarGoHome
}

// beeWantsToEnterHive ports Bee.wantsToEnterHive: (stayOutOfHiveCountdown<=0 && !pollinating && !hasStung
// && target==null) && (hasNectar || isTiredOfLookingForNectar || BEES_STAY_IN_HIVE) && !isHiveNearFire.
// BEES_STAY_IN_HIVE default false (cited); target==null always true in v1 (no bee target seam, cited);
// isHiveNearFire deferred to false (no fire scan on the hive BE from the bee side yet, cited -- a fire-free
// world is the vanilla common case). CITE Bee.wantsToEnterHive.
func beeWantsToEnterHive(t *TickLoop, e *Entity) bool {
	if e.beeStayOutOfHiveCountdown > 0 || e.beePollinating || e.beeHasStung {
		return false
	}
	// getTarget() != null -> false: no bee combat-target seam in v1, treated as null (cited).
	const beesStayInHive = false // EnvironmentAttributes.BEES_STAY_IN_HIVE default (cited)
	want := e.beeHasNectar || beeIsTiredOfLookingForNectar(e) || beesStayInHive
	if !want {
		return false
	}
	// !isHiveNearFire(): deferred to true (no fire near the hive in v1, cited).
	return true
}

// beeDropHive ports Bee.dropHive: hivePos=null; remainingCooldownBeforeLocatingNewHive=200. CITE Bee.dropHive.
func beeDropHive(e *Entity) {
	e.beeHivePos = nil
	e.beeRemainingCooldownLocatingHive = beeCooldownLocatingNewHive
}

// beeDropFlower ports Bee.dropFlower: savedFlowerPos=null; remainingCooldownBeforeLocatingNewFlower =
// Mth.nextInt(random, 20, 60). CITE Bee.dropFlower.
func beeDropFlower(e *Entity) {
	e.beeSavedFlowerPos = nil
	e.beeRemainingCooldownLocatingFlower = beeFindFlowerRetryMin + int(mobRandom(e).nextInt(beeFindFlowerRetryMax-beeFindFlowerRetryMin+1))
}

// beeSetHasNectar ports Bee.setHasNectar(b): if b resetTicksWithoutNectarSinceExitingHive(); set the flag.
// CITE Bee.setHasNectar.
func beeSetHasNectar(e *Entity, b bool) {
	if b {
		e.beeTicksWithoutNectarSinceExiting = 0 // resetTicksWithoutNectarSinceExitingHive
	}
	e.beeHasNectar = b
}

// beeAttractsBees ports Bee.attractsBees(state): state.is(BEE_ATTRACTIVE) && !WATERLOGGED && (double-tall
// SUNFLOWER upper-half exclusion). The double-plant upper-half detail is deferred (cited: v1 flower search
// checks the BEE_ATTRACTIVE tag + not-waterlogged, the observable "is this a flower" predicate). CITE
// Bee.attractsBees.
func beeAttractsBees(t *TickLoop, p pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(p, dimMinY)
	if !ok {
		return false
	}
	// state.is(BlockTags.BEE_ATTRACTIVE): the runtime block-tag query (block_tags.go). The WATERLOGGED
	// exclusion + double-tall SUNFLOWER upper-half check are cite-deferred details (a waterlogged/upper flower
	// is rare and the tag membership is the observable "is this a flower a bee pollinates" predicate). CITE
	// Bee.attractsBees (BlockTags.BEE_ATTRACTIVE).
	return blockInTag(s, "bee_attractive")
}

// ---- BeeEnterHiveGoal (@1) -------------------------------------------------------------------------
//
// canBeeUse: hivePos!=null && wantsToEnterHive() && hivePos.closerToCenterThan(pos, 2) && the hive BE is
// present && !isFull (if full -> hivePos=null, return false). canBeeContinueToUse: false (one-shot).
// start: getBeehiveBlockEntity(); if present addOccupant(this). CITE Bee.BeeEnterHiveGoal.
type beeEnterHiveGoal struct{ baseGoal }

func newBeeEnterHiveGoal() *beeEnterHiveGoal { return &beeEnterHiveGoal{} }

func (g *beeEnterHiveGoal) canUse(t *TickLoop, e *Entity) bool {
	return g.canBeeUse(t, e) && !beeIsAngry(t, e) // BaseBeeGoal.canUse
}
func (g *beeEnterHiveGoal) canContinueToUse(t *TickLoop, e *Entity) bool { return false }

func (g *beeEnterHiveGoal) canBeeUse(t *TickLoop, e *Entity) bool {
	if e.beeHivePos == nil || !beeWantsToEnterHive(t, e) {
		return false
	}
	// hivePos.closerToCenterThan(position(), 2.0): the bee is within 2 of the hive center.
	if !beeCloserThan(e, *e.beeHivePos, 2.0) {
		return false
	}
	// getBeehiveBlockEntity(): the resolved hive BE (null when hivePos too far / not a hive).
	be := t.beeGetHiveBE(e)
	if be == nil {
		return false
	}
	// if (be.isFull()) { hivePos = null; return false; }  else return true;
	if be.isFull() {
		e.beeHivePos = nil
		return false
	}
	return true
}

func (g *beeEnterHiveGoal) start(t *TickLoop, e *Entity) {
	// getBeehiveBlockEntity(); if (be != null) be.addOccupant(this): the Part-1 seam.
	be := t.beeGetHiveBE(e)
	if be != nil && e.beeHivePos != nil {
		t.beehiveAddOccupant(*e.beeHivePos, be, e)
	}
}

// beeGetHiveBE ports Bee.getBeehiveBlockEntity: null when hivePos==null or isTooFarAway(hivePos); else the
// resolved BeehiveBlockEntity at hivePos (or null when the block is no longer a hive). CITE
// Bee.getBeehiveBlockEntity.
func (t *TickLoop) beeGetHiveBE(e *Entity) *beehiveBE {
	if e.beeHivePos == nil {
		return nil
	}
	if beeIsTooFarAway(e, *e.beeHivePos) {
		return nil
	}
	return t.resolveBeehive(*e.beeHivePos)
}

// ---- BeeLocateHiveGoal (@5) ------------------------------------------------------------------------
//
// canBeeUse: remainingCooldownBeforeLocatingNewHive==0 && !hasHive() && wantsToEnterHive().
// canBeeContinueToUse: false. start: cooldown=200; find nearby hives with space; if none return; else pick
// the first non-blacklisted (else clearBlacklist + pick [0]) and set hivePos. The PoiManager BEE_HOME scan
// is DEFERRED (no PoiManager in v1): beeFindNearbyHivesWithSpace scans the tick-owned beehive store within
// HIVE_SEARCH_DISTANCE for a hive with space, nearest-first -- the same observable result. CITE
// Bee.BeeLocateHiveGoal.
type beeLocateHiveGoal struct{ baseGoal }

func newBeeLocateHiveGoal() *beeLocateHiveGoal { return &beeLocateHiveGoal{} }

func (g *beeLocateHiveGoal) canUse(t *TickLoop, e *Entity) bool {
	return g.canBeeUse(t, e) && !beeIsAngry(t, e)
}
func (g *beeLocateHiveGoal) canContinueToUse(t *TickLoop, e *Entity) bool { return false }

func (g *beeLocateHiveGoal) canBeeUse(t *TickLoop, e *Entity) bool {
	// remainingCooldownBeforeLocatingNewHive == 0 && !hasHive() && wantsToEnterHive()
	return e.beeRemainingCooldownLocatingHive == 0 && e.beeHivePos == nil && beeWantsToEnterHive(t, e)
}

func (g *beeLocateHiveGoal) start(t *TickLoop, e *Entity) {
	e.beeRemainingCooldownLocatingHive = beeCooldownLocatingNewHive // = 200
	hives := t.beeFindNearbyHivesWithSpace(e)
	if len(hives) == 0 {
		return
	}
	// for (BlockPos p : hives) if (!goToHiveGoal.isTargetBlacklisted(p)) { hivePos = p; return; }
	for _, p := range hives {
		if !e.beeHiveBlacklisted(p) {
			hp := p
			e.beeHivePos = &hp
			return
		}
	}
	// all blacklisted: clearBlacklist(); hivePos = hives.get(0).
	e.beeClearHiveBlacklist()
	hp := hives[0]
	e.beeHivePos = &hp
}

// beeFindNearbyHivesWithSpace ports BeeLocateHiveGoal.findNearbyHivesWithSpace via the tick-owned beehive
// store (the PoiManager BEE_HOME getInRange analogue, DEFERRED cited): every registered beehive/bee_nest
// within HIVE_SEARCH_DISTANCE (20) that is not full, sorted nearest-first by distSqr. CITE
// BeeLocateHiveGoal.findNearbyHivesWithSpace.
func (t *TickLoop) beeFindNearbyHivesWithSpace(e *Entity) []pk.Position {
	var out []pk.Position
	if t.beehives == nil {
		return out
	}
	ex, ey, ez := e.x, e.y, e.z
	// nearest-first insertion sort (small N: at most a handful of hives near a bee).
	type cand struct {
		p pk.Position
		d float64
	}
	var cs []cand
	for p, b := range t.beehives {
		if b.isFull() {
			continue // doesHiveHaveSpace: !isFull
		}
		if !beeCloserThan(e, p, float64(beeHiveSearchDistance)) {
			continue // getInRange(pos, 20)
		}
		dx := (float64(p.X) + 0.5) - ex
		dy := (float64(p.Y) + 0.5) - ey
		dz := (float64(p.Z) + 0.5) - ez
		cs = append(cs, cand{p, dx*dx + dy*dy + dz*dz})
	}
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].d < cs[j-1].d; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
	for _, c := range cs {
		out = append(out, c.p)
	}
	return out
}

// beeHiveBlacklisted ports BeeGoToHiveGoal.isTargetBlacklisted. CITE BeeGoToHiveGoal.isTargetBlacklisted.
func (e *Entity) beeHiveBlacklisted(p pk.Position) bool {
	for _, b := range e.beeHiveBlacklist {
		if b == p {
			return true
		}
	}
	return false
}

// beeBlacklistHive ports BeeGoToHiveGoal.blacklistTarget: add + trim to MAX_BLACKLISTED_TARGETS (3, FIFO).
// CITE BeeGoToHiveGoal.blacklistTarget.
func (e *Entity) beeBlacklistHive(p pk.Position) {
	e.beeHiveBlacklist = append(e.beeHiveBlacklist, p)
	for len(e.beeHiveBlacklist) > beeGoToHiveMaxBlacklist {
		e.beeHiveBlacklist = e.beeHiveBlacklist[1:] // remove(0)
	}
}

// beeClearHiveBlacklist ports BeeGoToHiveGoal.clearBlacklist. CITE BeeGoToHiveGoal.clearBlacklist.
func (e *Entity) beeClearHiveBlacklist() { e.beeHiveBlacklist = e.beeHiveBlacklist[:0] }

// ---- BeeGoToHiveGoal (@5) --------------------------------------------------------------------------
//
// canBeeUse: hivePos!=null && !isTooFarAway(hivePos) && !hasHome() && wantsToEnterHive() &&
// !hasReachedTarget(hivePos) && level.getBlockState(hivePos).is(BEEHIVES). canBeeContinueToUse == canBeeUse.
// tick: ++travellingTicks; if > adjustedTickDelay(2400) dropAndBlacklistHive; else if within 16 blocks
// pathfind directly (path-stuck 60-tick drop), else pathfindRandomlyTowards. The A* createPath/canReach +
// path-sameAs stuck detection are DEFERRED (no flying A* in v1): the target is handed to the nav seam and
// the travelling-tick timeout + reached-target arrival remain 1:1. hasHome() is deferred to false (no bee
// home-block seam; hivePos IS the home). CITE Bee.BeeGoToHiveGoal.
type beeGoToHiveGoal struct {
	baseGoal
	travellingTicks int
	ticksStuck      int
}

func newBeeGoToHiveGoal() *beeGoToHiveGoal { return &beeGoToHiveGoal{} }

func (g *beeGoToHiveGoal) requiresUpdateEveryTick() bool { return false }

func (g *beeGoToHiveGoal) canUse(t *TickLoop, e *Entity) bool {
	return g.canBeeUse(t, e) && !beeIsAngry(t, e)
}
func (g *beeGoToHiveGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canBeeUse(t, e) && !beeIsAngry(t, e) // canBeeContinueToUse == canBeeUse
}

func (g *beeGoToHiveGoal) canBeeUse(t *TickLoop, e *Entity) bool {
	if e.beeHivePos == nil {
		return false
	}
	hp := *e.beeHivePos
	if beeIsTooFarAway(e, hp) {
		return false
	}
	// hasHome(): deferred to false (no bee home-block seam in v1; hivePos is the home).
	if !beeWantsToEnterHive(t, e) {
		return false
	}
	if g.hasReachedTarget(e, hp) {
		return false
	}
	// level.getBlockState(hivePos).is(BlockTags.BEEHIVES):
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(hp, dimMinY)
	if !ok || !block.IsBeehiveBlock(s) {
		return false
	}
	return true
}

// hasReachedTarget ports BeeGoToHiveGoal.hasReachedTarget: closerThan(pos, 2) OR the path is done+reachable
// at pos. The path branch is DEFERRED (no nav path readout): v1 uses the closerThan(2) arrival only (the
// observable "at the hive" test). CITE BeeGoToHiveGoal.hasReachedTarget.
func (g *beeGoToHiveGoal) hasReachedTarget(e *Entity, p pk.Position) bool {
	return beeCloserThan(e, p, float64(beeHiveCloseEnoughDistance))
}

func (g *beeGoToHiveGoal) start(t *TickLoop, e *Entity) {
	g.travellingTicks = 0
	g.ticksStuck = 0
}

func (g *beeGoToHiveGoal) stop(t *TickLoop, e *Entity) {
	g.travellingTicks = 0
	g.ticksStuck = 0
	if e.ai != nil {
		e.ai.clearWantTarget() // navigation.stop()
	}
}

func (g *beeGoToHiveGoal) tick(t *TickLoop, e *Entity) {
	if e.beeHivePos == nil {
		return
	}
	hp := *e.beeHivePos
	g.travellingTicks++
	// if (travellingTicks > adjustedTickDelay(2400)) dropAndBlacklistHive();  [adjustedTickDelay==identity v1]
	if g.travellingTicks > beeGoToHiveMaxTravellingTicks {
		g.dropAndBlacklistHive(e)
		return
	}
	// if (navigation.isInProgress()) return: v1 has no isInProgress readout; drive the target each tick (cited).
	if beeCloserThan(e, hp, float64(beeGoToHivePathfindCloser)) {
		// pathfindDirectlyTowards: hand the hive block-center target to the nav seam at speed 1.0 (moveTo
		// (x,y,z, closerThan(3)?1:2, 1.0) -- the reachRange is a nav-internal detail deferred, cited).
		beeSetWanted(e, hp, 1.0)
	} else {
		// isTooFarAway(hivePos) -> dropHive; else pathfindRandomlyTowards(hivePos).
		if beeIsTooFarAway(e, hp) {
			beeDropHive(e)
			return
		}
		t.beePathfindRandomlyTowards(e, hp)
	}
}

// dropAndBlacklistHive ports BeeGoToHiveGoal.dropAndBlacklistHive: blacklist the current hive then dropHive.
// CITE BeeGoToHiveGoal.dropAndBlacklistHive.
func (g *beeGoToHiveGoal) dropAndBlacklistHive(e *Entity) {
	if e.beeHivePos != nil {
		e.beeBlacklistHive(*e.beeHivePos)
	}
	beeDropHive(e)
}

// beePathfindRandomlyTowards ports Bee.pathfindRandomlyTowards: vanilla uses AirRandomPos.getPosTowards for
// a flying random step toward the target. The AirRandomPos jitter is DEFERRED (no flying random-pos
// primitive in v1): the bee is aimed at the target block center via the nav seam at speed 1.0 -- the
// observable "move toward the hive/flower" outcome. CITE Bee.pathfindRandomlyTowards.
func (t *TickLoop) beePathfindRandomlyTowards(e *Entity, p pk.Position) {
	beeSetWanted(e, p, 1.0)
}

// ---- BeeGoToKnownFlowerGoal (@6) -------------------------------------------------------------------
//
// canBeeUse: savedFlowerPos!=null && !hasHome() && wantsToGoToKnownFlower() && !closerThan(savedFlowerPos,
// 2). canBeeContinueToUse == canBeeUse. tick: ++travellingTicks; if > adjustedTickDelay(2400) dropFlower();
// else if isTooFarAway(savedFlowerPos) dropFlower(); else pathfindRandomlyTowards(savedFlowerPos).
// wantsToGoToKnownFlower: ticksWithoutNectarSinceExitingHive > 600. CITE Bee.BeeGoToKnownFlowerGoal.
type beeGoToKnownFlowerGoal struct {
	baseGoal
	travellingTicks int
}

func newBeeGoToKnownFlowerGoal() *beeGoToKnownFlowerGoal { return &beeGoToKnownFlowerGoal{} }

func (g *beeGoToKnownFlowerGoal) canUse(t *TickLoop, e *Entity) bool {
	return g.canBeeUse(t, e) && !beeIsAngry(t, e)
}
func (g *beeGoToKnownFlowerGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canBeeUse(t, e) && !beeIsAngry(t, e)
}

func (g *beeGoToKnownFlowerGoal) canBeeUse(t *TickLoop, e *Entity) bool {
	if e.beeSavedFlowerPos == nil {
		return false
	}
	// hasHome(): deferred to false. wantsToGoToKnownFlower: ticksWithoutNectarSinceExitingHive > 600.
	if e.beeTicksWithoutNectarSinceExiting <= beeTicksBeforeGoingToFlower {
		return false
	}
	// !closerThan(savedFlowerPos, 2):
	return !beeCloserThan(e, *e.beeSavedFlowerPos, float64(beeHiveCloseEnoughDistance))
}

func (g *beeGoToKnownFlowerGoal) start(t *TickLoop, e *Entity) { g.travellingTicks = 0 }

func (g *beeGoToKnownFlowerGoal) stop(t *TickLoop, e *Entity) {
	g.travellingTicks = 0
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

func (g *beeGoToKnownFlowerGoal) tick(t *TickLoop, e *Entity) {
	if e.beeSavedFlowerPos == nil {
		return
	}
	fp := *e.beeSavedFlowerPos
	g.travellingTicks++
	if g.travellingTicks > beeGoToHiveMaxTravellingTicks {
		beeDropFlower(e)
		return
	}
	// navigation.isInProgress() -> return: deferred (no readout); drive each tick.
	if beeIsTooFarAway(e, fp) {
		beeDropFlower(e)
		return
	}
	t.beePathfindRandomlyTowards(e, fp)
}

// ---- BeePollinateGoal (@4) -------------------------------------------------------------------------
//
// canBeeUse: remainingCooldownBeforeLocatingNewFlower>0 -> false; hasNectar() -> false; isRaining -> false;
// findNearbyFlower present -> savedFlowerPos=it, moveTo(center, 1.2), true; absent ->
// remainingCooldownBeforeLocatingNewFlower = Mth.nextInt(20,60), false.
// canBeeContinueToUse: pollinating && hasSavedFlowerPos && !isRaining && (hasPollinatedLongEnough ?
// nextFloat()<0.2 : true). hasPollinatedLongEnough: successfulPollinatingTicks > 400.
// start: reset counters; pollinating=true; resetTicksWithoutNectarSinceExitingHive.
// stop: if hasPollinatedLongEnough setHasNectar(true); pollinating=false; navigation.stop(); cooldown=200.
// tick (requiresUpdateEveryTick): if !hasSavedFlowerPos return; ++pollinatingTicks; if >600 dropFlower +
// stop; else hover at flower-bottom-center + (0,0.6,0), the 25-chance reposition, successfulPollinatingTicks
// ++, the 0.05 sound roll. MoveControl/LookControl DEFERRED to the nav seam (hover target at SPEED_MODIFIER
// 0.35); the successfulPollinatingTicks increment (what start->stop reads for nectar) + RNG order are 1:1.
// findNearbyFlower DEFERRED (no createPath reachability in v1): first BEE_ATTRACTIVE block within radius 5.
// CITE Bee.BeePollinateGoal.
type beePollinateGoal struct {
	baseGoal
	successfulPollinatingTicks int
	pollinatingTicks           int
	pollinating                bool
	hoverX, hoverY, hoverZ     float64
	hoverSet                   bool
}

func newBeePollinateGoal() *beePollinateGoal { return &beePollinateGoal{} }

func (g *beePollinateGoal) requiresUpdateEveryTick() bool { return true } // BeePollinateGoal.requiresUpdateEveryTick

func (g *beePollinateGoal) canUse(t *TickLoop, e *Entity) bool {
	return g.canBeeUse(t, e) && !beeIsAngry(t, e)
}
func (g *beePollinateGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canBeeContinueToUse(t, e) && !beeIsAngry(t, e)
}

func (g *beePollinateGoal) canBeeUse(t *TickLoop, e *Entity) bool {
	if e.beeRemainingCooldownLocatingFlower > 0 {
		return false
	}
	if e.beeHasNectar {
		return false
	}
	if t.isRaining() {
		return false
	}
	f, ok := t.beeFindNearbyFlower(e)
	if ok {
		fp := f
		e.beeSavedFlowerPos = &fp
		beeSetWanted(e, f, 1.2) // navigation.moveTo(center, 1.2)
		return true
	}
	e.beeRemainingCooldownLocatingFlower = beeFindFlowerRetryMin + int(mobRandom(e).nextInt(beeFindFlowerRetryMax-beeFindFlowerRetryMin+1))
	return false
}

func (g *beePollinateGoal) canBeeContinueToUse(t *TickLoop, e *Entity) bool {
	if !g.pollinating {
		return false
	}
	if e.beeSavedFlowerPos == nil {
		return false
	}
	if t.isRaining() {
		return false
	}
	if g.hasPollinatedLongEnough() {
		return mobRandom(e).nextFloat() < 0.2
	}
	return true
}

// hasPollinatedLongEnough ports BeePollinateGoal.hasPollinatedLongEnough: successfulPollinatingTicks > 400.
func (g *beePollinateGoal) hasPollinatedLongEnough() bool {
	return g.successfulPollinatingTicks > beeMinPollinationTicks
}

func (g *beePollinateGoal) start(t *TickLoop, e *Entity) {
	g.successfulPollinatingTicks = 0
	g.pollinatingTicks = 0
	g.pollinating = true
	g.hoverSet = false
	e.beePollinating = true                 // wantsToEnterHive.isPollinating() reads this mirror
	e.beeTicksWithoutNectarSinceExiting = 0 // resetTicksWithoutNectarSinceExitingHive
}

func (g *beePollinateGoal) stop(t *TickLoop, e *Entity) {
	if g.hasPollinatedLongEnough() {
		beeSetHasNectar(e, true) // setHasNectar(true) -> the nectar payoff
	}
	g.pollinating = false
	e.beePollinating = false
	if e.ai != nil {
		e.ai.clearWantTarget() // navigation.stop()
	}
	e.beeRemainingCooldownLocatingFlower = beeCooldownLocatingNewFlower // = 200
}

func (g *beePollinateGoal) tick(t *TickLoop, e *Entity) {
	if e.beeSavedFlowerPos == nil {
		return
	}
	g.pollinatingTicks++
	if g.pollinatingTicks > beeMaxPollinatingTicks {
		beeDropFlower(e)
		g.pollinating = false
		e.beePollinating = false
		e.beeRemainingCooldownLocatingFlower = beeCooldownLocatingNewFlower
		return
	}
	fp := *e.beeSavedFlowerPos
	tx := float64(fp.X) + 0.5
	ty := float64(fp.Y) + 0.6
	tz := float64(fp.Z) + 0.5
	if beeDist(tx, ty, tz, e.x, e.y, e.z) > 1.0 {
		g.hoverX, g.hoverY, g.hoverZ, g.hoverSet = tx, ty, tz, true
		g.setWantedPos(e)
		return
	}
	if !g.hoverSet {
		g.hoverX, g.hoverY, g.hoverZ, g.hoverSet = tx, ty, tz, true
	}
	arrived := beeDist(e.x, e.y, e.z, g.hoverX, g.hoverY, g.hoverZ) <= 0.1
	setWanted := true
	if !arrived && g.pollinatingTicks > beeMaxPollinatingTicks {
		beeDropFlower(e)
		return
	}
	if arrived {
		reposition := mobRandom(e).nextInt(beePollinatePositionChance) == 0
		if reposition {
			g.hoverX = tx + g.getOffset(e)
			g.hoverY = ty
			g.hoverZ = tz + g.getOffset(e)
			if e.ai != nil {
				e.ai.clearWantTarget() // navigation.stop()
			}
		} else {
			setWanted = false
		}
		// lookControl.setLookAt(target): cosmetic head aim deferred (cited).
	}
	if setWanted {
		g.setWantedPos(e)
	}
	g.successfulPollinatingTicks++
	if mobRandom(e).nextFloat() < 0.05 {
		// BEE_POLLINATE sound: cite-deferred cosmetic. The nextFloat() draw is preserved in lockstep.
		_ = e
	}
}

// setWantedPos ports BeePollinateGoal.setWantedPos: moveControl.setWantedPosition(hoverPos, 0.35). Driven
// through the nav target seam at 0.35 (MoveControl hover deferral, cited).
func (g *beePollinateGoal) setWantedPos(e *Entity) {
	if e.ai == nil || !g.hoverSet {
		return
	}
	e.ai.setWantTargetSpeed(g.hoverX, g.hoverY, g.hoverZ, beePollinateSpeedModifier)
}

// getOffset ports BeePollinateGoal.getOffset: (nextFloat()*2 - 1) * 0.33333334.
func (g *beePollinateGoal) getOffset(e *Entity) float64 {
	return float64((mobRandom(e).nextFloat()*2.0 - 1.0) * 0.33333334)
}

// beeFindNearbyFlower ports BeePollinateGoal.findNearbyFlower: scan withinManhattan(pos, 5,5,5) for the
// first BEE_ATTRACTIVE block. The unreachableFlowerCache + createPath.canReach filter are DEFERRED (no
// flying A-star in v1): the first attractive block in Manhattan range (nearest shell first). CITE
// BeePollinateGoal.findNearbyFlower.
func (t *TickLoop) beeFindNearbyFlower(e *Entity) (pk.Position, bool) {
	if t.world() == nil {
		return pk.Position{}, false
	}
	bx, by, bz := mthFloorF(e.x), mthFloorF(e.y), mthFloorF(e.z)
	const r = 5 // FLOWER_SEARCH_RADIUS
	for radius := 0; radius <= r; radius++ {
		for dx := -radius; dx <= radius; dx++ {
			for dy := -radius; dy <= radius; dy++ {
				for dz := -radius; dz <= radius; dz++ {
					if absInt(dx)+absInt(dy)+absInt(dz) != radius {
						continue
					}
					p := pk.Position{X: bx + dx, Y: by + dy, Z: bz + dz}
					if beeAttractsBees(t, p) {
						return p, true
					}
				}
			}
		}
	}
	return pk.Position{}, false
}

// beeDist is Vec3.distanceTo.
func beeDist(ax, ay, az, bx, by, bz float64) float64 {
	dx, dy, dz := ax-bx, ay-by, az-bz
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// beeHiveBookkeeping ports the per-tick hive/flower bookkeeping split across Bee.aiStep +
// Bee.customServerAiStep: decrement each live cooldown (stayOutOfHiveCountdown,
// remainingCooldownBeforeLocatingNewHive, remainingCooldownBeforeLocatingNewFlower), every 20 ticks drop an
// invalid hive (!isHiveValid -> hivePos=null), and ++ticksWithoutNectarSinceExitingHive when the bee has no
// nectar. The %20 gate uses gametime as the tickCount analogue (a per-entity tickCount is a cited follow-up;
// the observable re-validation cadence is preserved). CITE Bee.aiStep + Bee.customServerAiStep.
func (t *TickLoop) beeHiveBookkeeping(e *Entity) {
	// if (stayOutOfHiveCountdown > 0) stayOutOfHiveCountdown--;
	if e.beeStayOutOfHiveCountdown > 0 {
		e.beeStayOutOfHiveCountdown--
	}
	// if (remainingCooldownBeforeLocatingNewHive > 0) remainingCooldownBeforeLocatingNewHive--;
	if e.beeRemainingCooldownLocatingHive > 0 {
		e.beeRemainingCooldownLocatingHive--
	}
	// if (remainingCooldownBeforeLocatingNewFlower > 0) remainingCooldownBeforeLocatingNewFlower--;
	if e.beeRemainingCooldownLocatingFlower > 0 {
		e.beeRemainingCooldownLocatingFlower--
	}
	// if (tickCount % 20 == 0 && !isHiveValid()) hivePos = null;
	if t.gametime%20 == 0 && e.beeHivePos != nil {
		if t.beeGetHiveBE(e) == nil {
			e.beeHivePos = nil // !isHiveValid
		}
	}
	// customServerAiStep: if (!hasNectar()) ++ticksWithoutNectarSinceExitingHive;
	if !e.beeHasNectar {
		e.beeTicksWithoutNectarSinceExiting++
	}
}
