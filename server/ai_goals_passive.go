package server

// ai_goals_passive.go — AI-01: the concrete passive Pig goals, PORTED (the STANDING MANDATE,
// idiomatic non-1:1 Go, no GPL paste) from the unobfuscated 26.2 jar (javap, this session):
//
//   - net.minecraft.world.entity.ai.goal.RandomStrollGoal (base of
//     WaterAvoidingRandomStrollGoal): flags {MOVE}; DEFAULT_INTERVAL 120; speedModifier 1.0
//     for the Pig; getPosition() = DefaultRandomPos.getPos(mob, 10, 7) (10 horizontal / 7
//     vertical radius); canUse rolls 1-in-reducedTickDelay(interval), picks a random reachable
//     pos, sets wantedX/Y/Z; canContinueToUse = !navigation.isDone(); start() = navigation
//     .moveTo(wanted…, speed); stop() = navigation.stop().
//   - net.minecraft.world.entity.ai.goal.LookAtPlayerGoal: flags {LOOK}; lookDistance 6.0,
//     probability DEFAULT 0.02; canUse rolls < probability then finds the nearest player in
//     range; canContinueToUse = lookAt alive && within distance² && lookTime>0; start() sets
//     lookTime = 40 + rnd(40); tick() aims lookControl at the target and decrements lookTime;
//     stop() clears the target.
//   - net.minecraft.world.entity.ai.goal.RandomLookAroundGoal: flags {MOVE, LOOK} (jar-
//     confirmed — NOT LOOK-only); canUse rolls < 0.02; start() picks a random heading
//     (relX=cos(2π·rnd), relZ=sin(2π·rnd)) and lookTime = 20 + rnd(20); requiresUpdateEveryTick
//     true; tick() decrements lookTime and aims at (x+relX, eyeY, z+relZ).
//
// A GOAL SETS A TARGET, IT DOES NOT MOVE THE MOB (07-RESEARCH Pattern 1 'Critical'):
//   - randomStrollGoal.start() SETS mobAI.wantTarget (the navigation.moveTo analogue); Plan
//     07-02's navigation consumes it and steps the mob. It never calls moveEntity here.
//   - lookAtPlayerGoal / randomLookAroundGoal SET the mob's headYaw/yaw toward the look point
//     (the v1 LookControl analogue — vanilla's LookControl turns the head gradually; the
//     gradual-turn control lands with navigation in 07-02; for now the goal sets the angle
//     directly, which still proves the goal arbitration + the "faces the player" behavior).
//
// RNG (Phase 24): vanilla mobs roll against a PER-ENTITY RandomSource (Mob.getRandom()); the goals
// now draw from the mob's per-entity seeded source (e.ai.rng — ai_random.go) in the EXACT bytecode
// draw ORDER (jar-verified this session), REPLACING the shared package-global stdlib RNG used in v1.
// This makes the port 1:1-faithful (the draw order is the observable behavior) AND deterministic for
// a fixed seed (the TestTickAIDrivesMobs flake fix), and removes a shared-global-rand data race.
// Each goal still carries a test seam (forceTrigger / alwaysLook / always) so a unit test can make
// the chance roll deterministic; the seam bypasses the ROLL, not the source.

import (
	"math"
)

// mobRandom returns the mob's per-entity seeded RandomSource (e.ai.rng). It is non-nil for every
// goal-bearing mob (newPigAI / buildAIFromDecl set it; reseedMobAI guarantees it at spawn); the
// nil-guard returns the package-default-seeded source so a hand-built mobAI in a test without an rng
// still draws deterministically rather than panicking.
func mobRandom(e *Entity) *entityRandom {
	if e != nil && e.ai != nil && e.ai.rng != nil {
		return e.ai.rng
	}
	return newEntityRandom(defaultEntityRandomSeed)
}

// yawTowardDeg returns the MC body-yaw (degrees) that faces a horizontal direction (dx, dz).
// MC yaw convention (jar / 06-debug): 0°=+Z(south), 90°=-X(west), 270°/-90°=+X(east); the
// look-vector→yaw formula vanilla uses is atan2(-dx, dz) in degrees. A zero vector yields 0.
func yawTowardDeg(dx, dz float64) float32 {
	if dx == 0 && dz == 0 {
		return 0
	}
	return float32(math.Atan2(-dx, dz) * 180 / math.Pi)
}

// --- randomStrollGoal (WaterAvoidingRandomStrollGoal v1) -------------------------------

// strollDefaultInterval is RandomStrollGoal.DEFAULT_INTERVAL (jar) — the mean 1-in-N chance
// per tick that the goal triggers, BEFORE Goal.reducedTickDelay halves it (canUse rolls
// nextInt(reducedTickDelay(interval)) == nextInt(60), so the effective stroll cadence is ~1-in-60).
const strollDefaultInterval = 120

// reducedTickDelay ports net.minecraft.world.entity.ai.goal.Goal.reducedTickDelay(int):
// `Mth.positiveCeilDiv(ticks, 2)` == ceil(ticks/2). The goal selector ticks the AI at a reduced
// cadence, so vanilla halves a goal's tick-delay interval; RandomStrollGoal.canUse rolls
// nextInt(reducedTickDelay(interval)). positiveCeilDiv(n,2) for n>=0 is (n+1)/2 in integer math.
//
//	[VERIFIED javap Goal.reducedTickDelay: `iload_0; iconst_2; invokestatic Mth.positiveCeilDiv(I,I)`.]
//	[VERIFIED javap Mth.positiveCeilDiv == Math.floorDiv(x + y - 1, y); for y=2: (n+1)/2 == ceil(n/2).]
func reducedTickDelay(ticks int) int { return (ticks + 1) / 2 }

// strollHorizontalRadius / strollVerticalRadius are getPosition()'s DefaultRandomPos.getPos
// (mob, 10, 7) radii (jar): a random target within ±10 blocks horizontally, ±7 vertically.
const (
	strollHorizontalRadius = 10
	strollVerticalRadius   = 7
)

// randomStrollGoal ports RandomStrollGoal/WaterAvoidingRandomStrollGoal (flags {MOVE}).
type randomStrollGoal struct {
	baseGoal
	speedModifier float64
	interval      int
	forceTrigger  bool // RandomStrollGoal.forceTrigger / trigger(): skip the chance roll once

	probability float32 // WaterAvoidingRandomStrollGoal.probability — Pig ctor default 0.001f

	// wantCandidates is the 10 RAW candidate offsets getPosition emits this canUse (the
	// RandomPos.generateRandomPos supplier results — BlockPos.containing(xt+x, yt+y, zt+z), NOT yet
	// ground-snapped). Per the Phase-30.1 architecture split (CONTEXT <decisions>): the GOAL draws
	// only the direction (the per-mob RNG is the single lockstep source); the shared Go RUNTIME
	// (mobAI.snapStrollWant in serverAiStep, RNG-free) validates + ground-snaps them to the first
	// reachable walkable column. start() hands this slice to the runtime via setWantCandidates.
	wantCandidates [][3]float64

	// wantLandMode is the WaterAvoidingRandomStrollGoal.getPosition branch chosen by the probability
	// nextFloat() draw: false => the common DefaultRandomPos.getPos path (validate isOutsideLimits/
	// isRestricted/isNotStable/hasMalus, NO up-snap, NO water reject); true => the rare LandRandomPos
	// .getPos path (validate isOutsideLimits/isRestricted/isNotStable, THEN moveUpOutOfSolid, THEN
	// isWater/hasMalus). The runtime snap reads this mode to apply the correct validation. The DRAW
	// COUNT is identical in both branches (the probability nextFloat + 30 direction draws), so the
	// bit-fragile pig oracle stays in lockstep regardless of which branch the mode picks.
	wantLandMode bool
}

// strollWaterAvoidingProbability is WaterAvoidingRandomStrollGoal.PROBABILITY — the default the
// Pig's WaterAvoidingRandomStrollGoal(mob, 1.0) 2-arg ctor passes to the 3-arg ctor (jar:
// `ldc 0.001f; invokespecial <init>(mob, speed, 0.001f)`). getPosition rolls nextFloat() < this to
// take the rare LandRandomPos.getPos (ground-snap) path; otherwise it takes DefaultRandomPos.getPos.
const strollWaterAvoidingProbability = 0.001

func newWaterAvoidingRandomStrollGoal(speed float64) *randomStrollGoal {
	return &randomStrollGoal{
		baseGoal:      newBaseGoal(flagMove),
		speedModifier: speed,
		interval:      strollDefaultInterval,
		probability:   strollWaterAvoidingProbability,
	}
}

// canUse ports RandomStrollGoal.canUse: unless forceTrigger, roll 1-in-interval; then pick a
// random reachable position within the radius and stash it as the wanted target. (The vanilla
// hasControllingPassenger / noActionTime gates do not apply to a v1 passive Pig with no rider
// and no combat, so they are omitted — documented faithful-scope.)
func (g *randomStrollGoal) canUse(t *TickLoop, e *Entity) bool {
	if !g.forceTrigger {
		// DRAW 1 (the gate): getRandom().nextInt(reducedTickDelay(interval)) — RandomStrollGoal.canUse.
		// reducedTickDelay(n) == Mth.positiveCeilDiv(n, 2) == ceil(n/2): the jar HALVES the interval, so
		// the Pig's 120 interval rolls nextInt(60), NOT nextInt(120) — the stroll fires ~twice as often
		// as a raw-120 roll (a faithfulness fix; the prior v1 used the raw interval). The .star gate is
		// changed in lockstep to the same reduced value so the bit-fragile pig oracle stays byte-identical.
		//	[VERIFIED javap Goal.reducedTickDelay: `iload_0; iconst_2; Mth.positiveCeilDiv(I,I)` == ceil(n/2);
		//	 RandomStrollGoal.canUse: `getRandom().nextInt(reducedTickDelay(interval))`.]
		if g.interval > 0 && mobRandom(e).nextInt(reducedTickDelay(g.interval)) != 0 {
			return false
		}
	}
	cands, ok := g.getPosition(t, e)
	if !ok {
		return false
	}
	// Stash the 10 raw candidates on the goal until start() hands them to the runtime
	// (mobAI.setWantCandidates). The runtime's RNG-free snap (snapStrollWant) picks the first
	// reachable walkable column, using the DefaultRandomPos/LandRandomPos validation mode
	// getPosition selected (g.wantLandMode).
	g.wantCandidates = cands
	g.forceTrigger = false
	return true
}

// getPosition ports WaterAvoidingRandomStrollGoal.getPosition (the Pig's stroll goal — NOT the bare
// RandomStrollGoal.getPosition). For a NOT-in-water pig the jar is:
//
//	if (mob.isInWater()) { ... }                                   // skipped: a v1 flat-world pig
//	if (mob.getRandom().nextFloat() >= probability)                // probability == 0.001f
//	    return super.getPosition();                                // RandomStrollGoal -> DefaultRandomPos.getPos(mob,10,7)
//	return LandRandomPos.getPos(mob, 10, 7);                       // the rare (0.1%) ground-snap path
//
// So the COMMON path (≈99.9%) is DefaultRandomPos.getPos (validate isOutsideLimits/isRestricted/
// isNotStable/hasMalus over the raw candidate, NO moveUpOutOfSolid, NO isWater) and the RARE path is
// LandRandomPos.getPos (the same first three rejects, THEN moveUpOutOfSolid, THEN isWater/hasMalus).
// BOTH feed RandomPos.generateRandomPos(mob, supplier) == the UNCONDITIONAL `for i<10` (NO break)
// best-of-10 loop weighted by mob::getWalkTargetValue (all 0.0 → first non-null wins).
//
// ARCHITECTURE SPLIT (CONTEXT <decisions>): getPosition draws ONLY the RNG — the probability
// nextFloat() gate THEN 10×generateRandomDirection (30 nextInt, x/y/z order) — and emits the 10 RAW
// candidates + the chosen validation mode (wantLandMode). It does NO world read; the RNG-free runtime
// snap (mobAI.snapStrollWant) applies the mode's validity + (for LAND) the ground-snap. The DRAW
// COUNT is identical in both branches (1 nextFloat + 30 nextInt), so the bit-fragile pig oracle stays
// byte-identical regardless of which branch the probability draw selects.
//
//	[VERIFIED javap WaterAvoidingRandomStrollGoal.getPosition: isInWater?branch; nextFloat() >= probability
//	 -> RandomStrollGoal.getPosition (DefaultRandomPos.getPos 10,7); else LandRandomPos.getPos 10,7.]
//	[VERIFIED javap RandomPos.generateRandomPos(Supplier,ToDoubleFunction): for i<10, NO break; keep the
//	 STRICTLY > bestWeight candidate (first max wins ties); return atBottomCenterOf(best) or null.]
func (g *randomStrollGoal) getPosition(_ *TickLoop, e *Entity) (candidates [][3]float64, ok bool) {
	r := mobRandom(e)
	// (mob.isInWater() — a v1 flat-world pig is never in water; the in-water radius-15 branch is a
	// cited no-op that draws ZERO randoms here, faithful-scope. Upgrade when fluid nav lands.)
	// Probability gate: nextFloat() >= probability → DefaultRandomPos (common); else LandRandomPos.
	g.wantLandMode = r.nextFloat() < g.probability
	cands := make([][3]float64, 0, 10)
	for i := 0; i < 10; i++ { // RandomPos.generateRandomPos: for i<10, NO break (always 10 supplier calls)
		xt, yt, zt := generateRandomDirection(r, strollHorizontalRadius, strollVerticalRadius)
		// Both DefaultRandomPos.generateRandomPosTowardDirection and LandRandomPos's supplier compute
		// the candidate as BlockPos.containing(xt+mob.getX(), yt+mob.getY(), zt+mob.getZ()); a v1 pig
		// has NO home, so the hasHome()&&xzDist>1.0 bias branch never runs (ZERO extra draws) — a cited
		// no-op so a future home-bound mob slots the bias in.
		//	[VERIFIED CFR RandomPos.generateRandomPosTowardDirection: hasHome bias guarded by
		//	 `mob.hasHome() && distSqr>1.0`; otherwise `return BlockPos.containing(x+pos.getX(), ...)`.]
		cands = append(cands, [3]float64{e.x + float64(xt), e.y + float64(yt), e.z + float64(zt)})
	}
	return cands, true // the 10 raw candidates; the runtime (snapStrollWant) validates + snaps per mode.
}

// generateRandomDirection ports RandomPos.generateRandomDirection(random, horizontalDist,
// verticalDist) — the per-candidate direction draw. DRAW ORDER is x, y, z (the bit-fragile
// lockstep contract): xt = nextInt(2*h+1)-h, then yt = nextInt(2*v+1)-v, then zt = nextInt(2*h+1)-h.
// The OLD getPosition drew z BEFORE y, which was both a faithfulness bug and a lockstep mismatch.
//
//	[VERIFIED javap RandomPos.generateRandomDirection: nextInt(2*h+1)-h -> istore_3 (xt);
//	 nextInt(2*v+1)-v -> istore 4 (yt); nextInt(2*h+1)-h (zt); new BlockPos(xt, yt, zt).]
func generateRandomDirection(r *entityRandom, h, v int) (xt, yt, zt int) {
	xt = r.nextInt(2*h+1) - h // horizontal (h=strollHorizontalRadius=10) — DRAW order 1 of 3
	yt = r.nextInt(2*v+1) - v // vertical   (v=strollVerticalRadius=7)    — DRAW order 2 of 3
	zt = r.nextInt(2*h+1) - h // horizontal (h=strollHorizontalRadius=10) — DRAW order 3 of 3
	return
}

// canContinueToUse ports RandomStrollGoal.canContinueToUse = !navigation.isDone() (&& no
// rider). With no navigation yet, "not done" == the mob still has a pending wanted target
// (hasTarget). 07-02 will flip hasTarget false when the path completes.
func (g *randomStrollGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return e.ai != nil && e.ai.hasTarget
}

// start ports RandomStrollGoal.start = navigation.moveTo(wantedX, wantedY, wantedZ, speed): hand
// the 10 raw candidates to the mob AI (the runtime snap consumes them in serverAiStep and commits
// the first reachable walkable column via setWantTarget). It does NOT move the mob. The candidate
// loop always produces exactly 10 (RandomPos.generateRandomPos's for i<10); guard the contract.
func (g *randomStrollGoal) start(_ *TickLoop, e *Entity) {
	if e.ai == nil || len(g.wantCandidates) != 10 {
		return
	}
	var arr [10][3]float64
	copy(arr[:], g.wantCandidates)
	e.ai.setWantCandidates(arr, g.wantLandMode)
}

// stop ports RandomStrollGoal.stop = navigation.stop(): clear the pending target.
func (g *randomStrollGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// --- lookAtPlayerGoal -------------------------------------------------------------------

// lookAtDefaultProbability is LookAtPlayerGoal.DEFAULT_PROBABILITY (jar) = 0.02.
const lookAtDefaultProbability = 0.02

// lookAtPlayerGoal ports LookAtPlayerGoal(mob, Player, lookDistance) (flags {LOOK}).
type lookAtPlayerGoal struct {
	baseGoal
	lookDistance float32
	probability  float32
	lookTime     int

	// look target, captured in canUse, aimed at in tick (vanilla `lookAt`). For v1 the target
	// is a player position (x,y,z) read from loop.players rather than an Entity ref.
	hasLook            bool
	lookX, lookY, lookZ float64

	alwaysLook bool // test seam: force the probability roll to pass deterministically
}

func newLookAtPlayerGoal(lookDistance float32) *lookAtPlayerGoal {
	return &lookAtPlayerGoal{
		baseGoal:     newBaseGoal(flagLook),
		lookDistance: lookDistance,
		probability:  lookAtDefaultProbability,
	}
}

// canUse ports LookAtPlayerGoal.canUse: roll < probability, then find the nearest player in
// lookDistance and capture it as the look target. Returns true iff a player was found.
//
// SEAM NOTE: vanilla calls ServerLevel.getNearestPlayer(TargetingConditions, mob, x,eyeY,z).
// Players are NOT in the entityStore in this server (only spawned mobs are), so the nearest
// player is found by scanning the tick-owned loop.players set (the same data the tracker uses
// for player visibility) — faithful to "nearest player in range", using the real seam.
func (g *lookAtPlayerGoal) canUse(t *TickLoop, e *Entity) bool {
	// LookAtPlayerGoal.canUse: getRandom().nextFloat() < probability (the chance roll), then find a player.
	if !g.alwaysLook && mobRandom(e).nextFloat() >= g.probability {
		return false
	}
	px, py, pz, ok := nearestPlayerWithin(t, e, float64(g.lookDistance))
	if !ok {
		return false
	}
	g.lookX, g.lookY, g.lookZ = px, py, pz
	g.hasLook = true
	return true
}

// canContinueToUse ports LookAtPlayerGoal.canContinueToUse: a captured target within distance²
// and lookTime>0. (Aliveness is implicit — a player in loop.players is live; if it left range
// the distance check fails.)
func (g *lookAtPlayerGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if !g.hasLook || g.lookTime <= 0 {
		return false
	}
	dx, dy, dz := g.lookX-e.x, g.lookY-(e.y), g.lookZ-e.z
	d2 := dx*dx + dy*dy + dz*dz
	return d2 <= float64(g.lookDistance)*float64(g.lookDistance)
}

// start ports LookAtPlayerGoal.start: lookTime = 40 + getRandom().nextInt(40) ticks of staring.
func (g *lookAtPlayerGoal) start(_ *TickLoop, e *Entity) {
	g.lookTime = 40 + mobRandom(e).nextInt(40)
}

// tick ports LookAtPlayerGoal.tick: aim the head/body at the look target and count down. The
// vanilla LookControl turns the head gradually; the v1 analogue sets headYaw/yaw directly
// toward the target (07-02 adds the gradual LookControl). Decrements lookTime.
func (g *lookAtPlayerGoal) tick(_ *TickLoop, e *Entity) {
	if !g.hasLook {
		return
	}
	yaw := yawTowardDeg(g.lookX-e.x, g.lookZ-e.z)
	e.headYaw = yaw
	e.yaw = yaw
	g.lookTime--
}

// stop ports LookAtPlayerGoal.stop: drop the look target.
func (g *lookAtPlayerGoal) stop(_ *TickLoop, _ *Entity) { g.hasLook = false }

// nearestPlayerWithin returns the position of the nearest player within maxDist of the mob
// (horizontal+vertical Euclidean), or ok=false if none. Scans the tick-owned loop.players —
// the player seam (players are not entityStore entries in this server). Tick-owned read.
func nearestPlayerWithin(t *TickLoop, e *Entity, maxDist float64) (x, y, z float64, ok bool) {
	return nearestPlayerAt(t, e.x, e.y, e.z, maxDist)
}

// nearestPlayerAt is the position-based core of the player scan: the nearest player within maxDist
// of an arbitrary point (cx,cy,cz), or ok=false if none. Factored out of nearestPlayerWithin so the
// world.nearest_player handle seam (plugin_entity.go) reuses the IDENTICAL tick-owned t.players scan
// rather than re-rolling it. Tick-owned read (TICK-05).
func nearestPlayerAt(t *TickLoop, cx, cy, cz, maxDist float64) (x, y, z float64, ok bool) {
	best := maxDist * maxDist
	for _, p := range t.players {
		if p == nil {
			continue
		}
		dx, dy, dz := p.x-cx, p.y-cy, p.z-cz
		d2 := dx*dx + dy*dy + dz*dz
		if d2 <= best {
			best = d2
			x, y, z, ok = p.x, p.y, p.z, true
		}
	}
	return x, y, z, ok
}

// --- randomLookAroundGoal ---------------------------------------------------------------

// randomLookAroundGoal ports RandomLookAroundGoal (flags {MOVE, LOOK} — jar-confirmed, NOT
// LOOK-only). It occasionally picks a random heading and stares that way for a short while.
type randomLookAroundGoal struct {
	baseGoal
	relX, relZ float64
	lookTime   int
	always     bool // test seam: force canUse
}

func newRandomLookAroundGoal() *randomLookAroundGoal {
	return &randomLookAroundGoal{baseGoal: newBaseGoal(flagMove | flagLook)}
}

// canUse ports RandomLookAroundGoal.canUse: getRandom().nextFloat() < 0.02f.
func (g *randomLookAroundGoal) canUse(_ *TickLoop, e *Entity) bool {
	return g.always || mobRandom(e).nextFloat() < 0.02
}

// canContinueToUse ports RandomLookAroundGoal.canContinueToUse = lookTime >= 0.
func (g *randomLookAroundGoal) canContinueToUse(_ *TickLoop, _ *Entity) bool { return g.lookTime >= 0 }

// start ports RandomLookAroundGoal.start: pick a random heading on the unit circle and a
// 20+rnd(20)-tick stare. d = 2π·rnd; relX = cos(d); relZ = sin(d).
func (g *randomLookAroundGoal) start(_ *TickLoop, e *Entity) {
	// RandomLookAroundGoal.start draw order (jar): d = (2π) * getRandom().nextDouble() FIRST, then
	// relX/relZ = cos/sin(d), then lookTime = 20 + getRandom().nextInt(20).
	r := mobRandom(e)
	d := math.Pi * 2 * r.nextDouble()
	g.relX = math.Cos(d)
	g.relZ = math.Sin(d)
	g.lookTime = 20 + r.nextInt(20)
}

// requiresUpdateEveryTick ports RandomLookAroundGoal.requiresUpdateEveryTick = true.
func (g *randomLookAroundGoal) requiresUpdateEveryTick() bool { return true }

// tick ports RandomLookAroundGoal.tick: count down and aim at (x+relX, eyeY, z+relZ). The v1
// analogue sets the body/head yaw directly toward the chosen heading (07-02's LookControl will
// make it gradual). Does NOT move the mob (the MOVE flag here is vanilla's — it claims MOVE so
// the mob holds still while looking, not so it relocates).
func (g *randomLookAroundGoal) tick(_ *TickLoop, e *Entity) {
	g.lookTime--
	yaw := yawTowardDeg(g.relX, g.relZ)
	e.headYaw = yaw
	e.yaw = yaw
}
