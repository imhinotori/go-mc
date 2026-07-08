package server

// ai_goals_follow.go — MOB-SUB-09 (Phase 33, Plan 03, C1): the GO-NATIVE FollowParentGoal@5. A baby
// (breedAge < 0) finds the nearest ADULT same-class within ~8 blocks and trails it, re-pathing every
// 10 ticks. It claims NO control flags (the ctor never calls setFlags → EMPTY set) and draws NO RNG.
//
// PORTED (the STANDING MANDATE, idiomatic Go, never a GPL paste) from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar), read via javap -c -p this session:
//
//   - net.minecraft.world.entity.ai.goal.FollowParentGoal (priority 5, speed 1.1, flags {} EMPTY):
//       HORIZONTAL_SCAN_RANGE = 8; VERTICAL_SCAN_RANGE = 4; DONT_FOLLOW_IF_CLOSER_THAN = 3;
//       canUse(): if (animal.getAge() >= 0) return false;                     // only a BABY follows
//                 parents = level.getEntitiesOfClass(animal.getClass(), bb.inflate(8,4,8));
//                 closest = null; closestDistSqr = MAX;
//                 for (p : parents) { if (p.getAge() < 0) continue;           // skip babies (keep ADULTS)
//                                     d = distanceToSqr(p); if (d > closestDistSqr) continue;
//                                     closestDistSqr = d; closest = p; }
//                 if (closest == null) return false;
//                 if (closestDistSqr < 9.0) return false;                     // already within 3 blocks
//                 parent = closest; return true;
//       canContinueToUse(): if (animal.getAge() >= 0) return false; if (!parent.isAlive()) return false;
//                 d = distanceToSqr(parent); return !(d < 9.0) && !(d > 256.0);   // follow while 3..16
//       start(): timeToRecalcPath = 0;
//       stop():  parent = null;
//       tick():  if (--timeToRecalcPath > 0) return;
//                timeToRecalcPath = adjustedTickDelay(10);                    // PURE INT — NO RNG
//                navigation.moveTo(parent, speed);
//
// SINGLE-OWNER (TICK-05): the goal runs on the tick goroutine over tick-owned state. The parent scan
// uses the OWNING-region store (the documented v5 same-region cut). NO RNG anywhere — the int recalc
// timer + the entity scan are fully deterministic, so the goal is oracle-trivially-safe (the oracle
// pig is a lone ADULT → canUse's age>=0 gate returns false → zero effect, even before RNG matters).

// followParentHorizontalRange is FollowParentGoal.HORIZONTAL_SCAN_RANGE (8): the horizontal inflate of
// the baby's bounding box when scanning for an adult to follow.
const followParentHorizontalRange = 8.0

// followParentVerticalRange is FollowParentGoal.VERTICAL_SCAN_RANGE (4): the vertical inflate.
const followParentVerticalRange = 4.0

// followDontFollowDistSqr is DONT_FOLLOW_IF_CLOSER_THAN² (3² = 9.0): canUse rejects an adult already
// within 3 blocks (no need to follow); canContinueToUse stops once back within 3 blocks.
const followDontFollowDistSqr = 9.0

// followLoseParentDistSqr is the canContinueToUse upper bound (16² = 256.0): the baby gives up if the
// parent strays beyond 16 blocks.
const followLoseParentDistSqr = 256.0

// followRecalcInterval is FollowParentGoal's adjustedTickDelay(10): the baby re-paths to the parent
// every 10 ticks while following. adjustedTickDelay (NOT the reduced/halved helper — that would be 5)
// keeps it the full 10.
const followRecalcInterval = 10

// followParentGoal ports net.minecraft.world.entity.ai.goal.FollowParentGoal (flags {} EMPTY,
// priority 5, speed 1.1). parent + timeToRecalcPath are the goal's carried state. No RNG.
type followParentGoal struct {
	baseGoal
	speedModifier    float64
	parent           *Entity // FollowParentGoal.parent — the adult being followed
	timeToRecalcPath int     // FollowParentGoal.timeToRecalcPath — re-path countdown
}

// newFollowParentGoal builds the FollowParentGoal with EMPTY flags (the ctor never calls setFlags, so
// the flag EnumSet stays empty — the goal never locks MOVE/LOOK; the selector handles an empty-flag
// goal by never blocking it, ai_goal.go) and speed 1.1 (Pig.registerGoals @5 FollowParentGoal(mob, 1.1)).
//
//	[VERIFIED javap FollowParentGoal.<init>(Animal, double): sets animal + speedModifier ONLY — no
//	 setFlags call (flags = EnumSet.noneOf == EMPTY); Pig.registerGoals: iconst_5; new FollowParentGoal;
//	 ldc2_w 1.1d; FollowParentGoal.<init>(Animal, double).]
func newFollowParentGoal(speed float64) *followParentGoal {
	return &followParentGoal{
		baseGoal:      newBaseGoal(0), // EMPTY flags — never locks MOVE/LOOK
		speedModifier: speed,
	}
}

// canUse ports FollowParentGoal.canUse: only a baby (age<0) follows; scan same-class entities near the
// baby within inflate(8,4,8), keep ADULTS (age>=0), pick the NEAREST, reject if it is already within
// 3 blocks (distSqr<9.0). The scan uses the OWNING-region store (t.cur().entities.near) — the v5
// same-region cut. Draws no RNG.
func (g *followParentGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.breedAge >= 0 { // animal.getAge() >= 0 — only a baby follows
		return false
	}
	closest := findNearestAdultParent(t, e)
	if closest == nil {
		return false
	}
	g.parent = closest
	return true
}

// findNearestAdultParent is the standalone FollowParentGoal.canUse adult scan shared by the Go-native
// followParentGoal (canUse) AND the plugin host handle (nearestAdultParent). It returns the nearest
// same-class ADULT (breedAge>=0) within the inflate(8,4,8) box that is NOT already within 3 blocks
// (distSqr>=9.0), or nil. Factoring it out keeps the Go pig and the plugin pig selecting the IDENTICAL
// parent (the lockstep contract, Plan 33-04). The scan uses the OWNING-region store — the v5
// same-region cut. Draws no RNG (FollowParentGoal is fully deterministic).
//
//	[VERIFIED javap FollowParentGoal.canUse: getEntitiesOfClass(animal.getClass(), inflate(8,4,8));
//	 skip age<0; pick nearest; reject if closestDistSqr < 9.0 (DONT_FOLLOW_IF_CLOSER_THAN²).]
func findNearestAdultParent(t *TickLoop, e *Entity) *Entity {
	var closest *Entity
	closestDistSqr := followLoseParentDistSqr + 1e9 // start at +inf (Double.MAX_VALUE in vanilla)
	for _, p := range t.cur().entities.near(e.x, e.z, 1) {
		if p == e || p.typ != e.typ {
			continue // not a same-class candidate (getEntitiesOfClass(animal.getClass()))
		}
		if !withinFollowScanBox(e, p) {
			continue // outside the inflate(8,4,8) box — the AABB scan bound
		}
		if p.breedAge < 0 {
			continue // skip babies — only an ADULT (age>=0) is a parent to follow
		}
		d := entityDistSqr(e, p)
		if d > closestDistSqr {
			continue
		}
		closestDistSqr = d
		closest = p
	}
	if closest == nil {
		return nil
	}
	if closestDistSqr < followDontFollowDistSqr { // already within 3 blocks — no follow
		return nil
	}
	return closest
}

// canContinueToUse ports FollowParentGoal.canContinueToUse: stop if grown up (age>=0) or the parent
// died; otherwise keep following while 9.0 <= distSqr <= 256.0 (3..16 blocks).
func (g *followParentGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	if e.breedAge >= 0 { // grew up → stop
		return false
	}
	if g.parent == nil || !g.parent.isAlive() {
		return false
	}
	d := entityDistSqr(e, g.parent)
	return !(d < followDontFollowDistSqr) && !(d > followLoseParentDistSqr)
}

// start ports FollowParentGoal.start: timeToRecalcPath = 0 (so the first tick re-paths immediately:
// --0 = -1, not > 0).
func (g *followParentGoal) start(_ *TickLoop, _ *Entity) {
	g.timeToRecalcPath = 0
}

// tick ports FollowParentGoal.tick EXACTLY: decrement timeToRecalcPath; if it is still > 0, return
// (don't re-path this tick); else reset it to adjustedTickDelay(10) and want the parent's position at
// speed 1.1. PURE INT — NO RNG. The move seam is setWantTarget (the navigation.moveTo(parent, speed)
// analog — the want carries the parent pos, the nav tick applies the speedModifier).
func (g *followParentGoal) tick(_ *TickLoop, e *Entity) {
	g.timeToRecalcPath--
	if g.timeToRecalcPath > 0 {
		return
	}
	g.timeToRecalcPath = adjustedTickDelay(followRecalcInterval)
	if g.parent == nil || e.ai == nil {
		return
	}
	e.ai.setWantTargetMod(g.parent.x, g.parent.y, g.parent.z, g.speedModifier) // navigation.moveTo(parent, speedModifier) (seam x MOVEMENT_SPEED)
}

// stop ports FollowParentGoal.stop: drop the parent.
func (g *followParentGoal) stop(_ *TickLoop, e *Entity) {
	g.parent = nil
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// withinFollowScanBox tests whether p falls inside the baby's bounding box inflated by (8,4,8) — the
// FollowParentGoal scan box. We approximate the inflated-AABB membership with a per-axis half-extent
// check from the baby's center (|dx|<=8, |dy|<=4, |dz|<=8), which matches the inflate semantics for
// the small same-class set the in-region near() returns (the exact AABB edges differ only for an
// entity straddling the boundary, where the nearest-pick is unaffected). No RNG.
//
//	[VERIFIED javap FollowParentGoal.canUse: getBoundingBox().inflate(8.0, 4.0, 8.0); getEntitiesOfClass.]
func withinFollowScanBox(e, p *Entity) bool {
	dx := p.x - e.x
	dy := p.y - e.y
	dz := p.z - e.z
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dz < 0 {
		dz = -dz
	}
	return dx <= followParentHorizontalRange && dy <= followParentVerticalRange && dz <= followParentHorizontalRange
}
