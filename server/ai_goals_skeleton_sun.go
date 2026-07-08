package server

// ai_goals_skeleton_sun.go — the AbstractSkeleton daytime burn-avoidance goals, ported 1:1 from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session):
//   - net.minecraft.world.entity.ai.goal.RestrictSunGoal  (AbstractSkeleton.registerGoals @2)
//   - net.minecraft.world.entity.ai.goal.FleeSunGoal      (AbstractSkeleton.registerGoals @3, speed 1.0)
//
// These are the base-class sun goals (NOT the Fox.SeekShelterGoal override) — the SAME class every burning
// mob registers, so they carry NO .star body: the skeleton .star DECLARES priority+flags+kind and these
// Go-native goals do the work (the 35-01b seam, sibling of ai_goals_fox.go's SeekShelterGoal but the
// UNMODIFIED FleeSunGoal, which crucially KEEPS the isOnFire() guard the fox override drops).
//
// FleeSunGoal.canUse (VERIFIED CFR): getTarget()==null && level.isBrightOutside() && mob.isOnFire() &&
// level.canSeeSky(blockPosition()) && getItemBySlot(HEAD).isEmpty() && setWantedPos(). getHidePos: 10
// candidates offset(nextInt(20)-10, nextInt(6)-3, nextInt(20)-10); the first NOT sky-exposed (and
// getWalkTargetValue >= 0) wins, at Vec3.atBottomCenterOf. canContinueToUse = !getNavigation().isDone().
// start = getNavigation().moveTo(wantedX,Y,Z, speedModifier). NO stop.
//
// RestrictSunGoal.canUse (VERIFIED CFR): level.isBrightOutside() && getItemBySlot(HEAD).isEmpty() &&
// hasGroundPathNavigation(mob). start = setAvoidSun(true). stop = setAvoidSun(false). NO flags (empty
// EnumSet — the ctor never calls setFlags), NO RNG, NO canContinueToUse override (defaults to canUse).
//
// v1 REDUCTIONS (cited, sibling of the fox flee-sun): isBrightOutside == !isDarkEnoughToSpawn (the same
// day/night proxy the fox SeekShelterGoal + hostile spawn rule use); canSeeSky == the superflat sky stub
// (fire.go); getItemBySlot(HEAD).isEmpty() == cited constant-true (no equipment slots on a v1 skeleton, so
// its head is always bare — the vanilla default for a naturally-spawned skeleton); getWalkTargetValue >= 0
// == cited constant-true (no path-malus subsystem; base PathfinderMob getWalkTargetValue is 0.0, jar-
// verified in ai_goals_avoid.go). setAvoidSun NOW has its real observable effect: it drives
// GroundPathNavigation.trimPath's avoid-sun tail (navigation.trimPathAvoidSun, run on path adoption in
// async.go) so a shaded day-time skeleton's fresh path is TRUNCATED at the first sky-exposed node — it
// routes only as far as the shade extends (VERIFIED CFR GroundPathNavigation.trimPath). Previously a cited
// no-op; the NodeEvaluator path-malus work landed the real trim. The RESTRICT goal still holds no MOVE
// flag, so it never fights the flee — faithful.

import "math"

// skeletonFleeSunSpeed is FleeSunGoal(this, 1.0) — AbstractSkeleton.registerGoals @3 ctor speedModifier.
const skeletonFleeSunSpeed = 1.0

// --- @3 FleeSunGoal(this, 1.0) — flags {MOVE} ------------------------------------------------------
// The burning, sky-exposed, day-time skeleton runs for shade. speedModifier routes from the declared
// movement_speed (declaredWalkSpeed) — the SAME want-multiplier the melee/bow goals use — so the ctor's
// literal 1.0 modifier scales the declared walk pace (skeletonFleeSunSpeed documents the jar arg).
type fleeSunGoal struct {
	baseGoal
	speed               float64
	wantX, wantY, wantZ float64
	haveWant            bool
}

func newFleeSunGoal(speed float64) *fleeSunGoal {
	return &fleeSunGoal{baseGoal: newBaseGoal(flagMove), speed: speed}
}

// fleeSunIsOnFire ports Entity.isOnFire(): remainingFireTicks > 0. Real read (Entity fire-tick state
// exists — fire.go/entity.go), so a skeleton actually on fire triggers the flee.
func fleeSunIsOnFire(e *Entity) bool { return e.remainingFireTicks > 0 }

// getHidePos ports FleeSunGoal.getHidePos: 10 candidates offset (nextInt(20)-10, nextInt(6)-3,
// nextInt(20)-10); the first NOT sky-exposed (getWalkTargetValue<0 is cited constant-false) wins, at
// bottom-center. Draws up to 30 nextInt on the skeleton's per-mob stream.
func (g *fleeSunGoal) getHidePos(t *TickLoop, e *Entity) (x, y, z float64, ok bool) {
	r := mobRandom(e)
	px, py, pz := int(math.Floor(e.x)), int(math.Floor(e.y)), int(math.Floor(e.z))
	for i := 0; i < 10; i++ {
		ox := r.nextInt(20) - 10
		oy := r.nextInt(6) - 3
		oz := r.nextInt(20) - 10
		cx, cy, cz := px+ox, py+oy, pz+oz
		probe := *e
		probe.x, probe.y, probe.z = float64(cx), float64(cy), float64(cz)
		if t.canSeeSky(&probe) {
			continue
		}
		// Vec3.atBottomCenterOf(randomPos): center-x/z, bottom-y.
		return float64(cx) + 0.5, float64(cy), float64(cz) + 0.5, true
	}
	return 0, 0, 0, false
}

// setWantedPos ports FleeSunGoal.setWantedPos: getHidePos -> cache wantedX/Y/Z; false if none.
func (g *fleeSunGoal) setWantedPos(t *TickLoop, e *Entity) bool {
	x, y, z, ok := g.getHidePos(t, e)
	if !ok {
		return false
	}
	g.wantX, g.wantY, g.wantZ, g.haveWant = x, y, z, true
	return true
}

func (g *fleeSunGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	if e.ai.getTarget() != 0 { // getTarget() != null
		return false
	}
	if t.isNightByGametime() { // !isBrightOutside()
		return false
	}
	if !fleeSunIsOnFire(e) { // !isOnFire()
		return false
	}
	if !t.canSeeSky(e) { // !canSeeSky(blockPosition())
		return false
	}
	// getItemBySlot(HEAD).isEmpty() == cited constant-true (v1 skeleton has no head equipment).
	return g.setWantedPos(t, e)
}

// canContinueToUse ports FleeSunGoal.canContinueToUse = !getNavigation().isDone().
func (g *fleeSunGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return e.ai != nil && e.ai.navigation.active()
}

// start ports FleeSunGoal.start = getNavigation().moveTo(wantedX,Y,Z, speedModifier).
func (g *fleeSunGoal) start(_ *TickLoop, e *Entity) {
	if g.haveWant && e.ai != nil {
		e.ai.setWantTargetSpeed(g.wantX, g.wantY, g.wantZ, g.speed)
	}
}

func (g *fleeSunGoal) stop(_ *TickLoop, e *Entity) {
	g.haveWant = false
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// --- @2 RestrictSunGoal(this) — NO flags ----------------------------------------------------------
// A day-time skeleton biases its pathfinding away from sun-exposed tiles (setAvoidSun). It holds NO
// control flag, so it never contends with the flee/melee goals — it only flips the navigation avoid-sun
// bit. NO RNG.
type restrictSunGoal struct {
	baseGoal
}

func newRestrictSunGoal() *restrictSunGoal {
	return &restrictSunGoal{baseGoal: newBaseGoal(0)} // ctor never calls setFlags -> empty flag set
}

func (g *restrictSunGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil { // hasGroundPathNavigation(mob) — a v1 AI mob always has the ground navigation
		return false
	}
	// isBrightOutside() && getItemBySlot(HEAD).isEmpty() (cited constant-true) && hasGroundPathNavigation.
	return !t.isNightByGametime()
}

func (g *restrictSunGoal) start(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.navigation.setAvoidSun(true) // GroundPathNavigation.setAvoidSun(true)
	}
}

func (g *restrictSunGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.navigation.setAvoidSun(false) // GroundPathNavigation.setAvoidSun(false)
	}
}
