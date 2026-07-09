package server

// ai_goals_avoid.go — the generic vanilla FLEE goal, PORTED (the STANDING MANDATE: idiomatic non-1:1
// Go, no GPL paste) from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, read via
// javap -c -p / CFR this session):
//
//   net.minecraft.world.entity.ai.goal.AvoidEntityGoal<T extends LivingEntity>  (flags {MOVE})
//
// The generic "avoid a nearby entity class" goal: the Creeper flees Cat/Ocelot @3, the Skeleton flees
// Wolf, Rabbit/Fox flee threats. Built generically here + wired as the native goal kind "avoid_entity"
// (buildNativeGoal, plugin_mob_ai.go) so any mob plugin can DECLARE it with an avoid_type.
//
// AvoidEntityGoal bytecode (javap this session):
//
//	// the ctor stores: avoidClass, maxDist (float), walkSpeedModifier (double), sprintSpeedModifier
//	// (double); setFlags(EnumSet.of(MOVE)); avoidEntityTargeting = TargetingConditions.forCombat()
//	// .range((double)maxDist).selector(avoidPredicate && predicateOnAvoidEntity).
//	public boolean canUse() {
//	    ServerLevel sl = getServerLevel(mob);
//	    this.toAvoid = sl.getNearestEntity(
//	        mob.level().getEntitiesOfClass(avoidClass,
//	            mob.getBoundingBox().inflate((double)maxDist, 3.0, (double)maxDist), e -> true),
//	        avoidEntityTargeting, mob, mob.getX(), mob.getY(), mob.getZ());
//	    if (this.toAvoid == null) return false;                                  // no threat in range
//	    Vec3 posAway = DefaultRandomPos.getPosAway(mob, 16, 7, toAvoid.position());
//	    if (posAway == null) return false;                                       // no flee pos found
//	    // REJECT if the flee pos is CLOSER to the threat than the mob already is (do not flee toward it)
//	    if (toAvoid.distanceToSqr(posAway.x, posAway.y, posAway.z) < toAvoid.distanceToSqr(mob)) return false;
//	    this.path = pathNav.createPath(posAway.x, posAway.y, posAway.z, 0);
//	    return this.path != null;
//	}
//	public boolean canContinueToUse() { return !pathNav.isDone(); }
//	public void start() { pathNav.moveTo(path, walkSpeedModifier); }
//	public void stop()  { this.toAvoid = null; }
//	public void tick()  {
//	    if (mob.distanceToSqr(toAvoid) < 49.0) mob.getNavigation().setSpeedModifier(sprintSpeedModifier);
//	    else                                    mob.getNavigation().setSpeedModifier(walkSpeedModifier);
//	}
//
// RNG DISCIPLINE (the pig-oracle care item — trivially safe): a passive pig declares NO avoid_entity
// goal, so this goal never ticks on it — ZERO draws reach the pinned pig stream. When it DOES fire,
// canUse draws ONLY the getPosAway RNG (best-of-10, each candidate: nextFloat + nextDouble + nextInt),
// and ONLY after a threat is found (no threat -> return before any draw).
//
// THE PATH-AWAY SEAM (CITED DEFERRALS, never silently dropped):
//   - Vanilla's canUse builds a real Path via pathNav.createPath(posAway, 0) and rejects when it is
//     null; start() feeds that Path to pathNav.moveTo(path, walkSpeedModifier). v1's navigation is the
//     async setWantTarget seam (no synchronous per-tick Path object), so "a path exists" is approximated
//     by "getPosAway returned a candidate that is farther from the threat than the mob" (the jar's OWN
//     acceptance gate), and start() commits the flee pos via setWantTargetSpeed at the walk speed
//     (the moveTo(path, walkSpeedModifier) analogue). This matches the ported PanicGoal (ai_goals_panic.go)
//     and MeleeAttackGoal (ai_goals_attack.go) nav seams. Upgrade when the synchronous Path lands.
//   - getEntitiesOfClass's search box inflates by (maxDist, 3.0, maxDist); nearestEntityOfTypeAt inflates
//     by (maxDist, 4.0, maxDist) (the FOLLOW_RANGE convention) — a broad-phase SUPERSET — then re-checks
//     the precise maxDist^2 inside the loop, so the spherical maxDist gate (the observable filter) is
//     identical; only the broad-phase vertical band differs. getNearestEntity compares from getEyeY
//     while nearestEntityOfTypeAt compares from feet-y — the SAME cited delta the wolf skeleton-target
//     goal already carries (ai_goals_target.go). Both become exact when the eye-based getNearestEntity /
//     3.0-band search box land, off no mob lockstep stream.
//   - avoidEntityTargeting (forCombat, range maxDist, NO_CREATIVE_OR_SPECTATOR + predicates): a v1
//     avoided candidate is a live mob of the avoided type; the forCombat visibility/attackable filters
//     are a cited no-op (no sensing subsystem) — the range gate is the live filter. Cite
//     AvoidEntityGoal.avoidEntityTargeting.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// avoidDefaultMaxDist / avoidWalkSpeedModifier / avoidSprintSpeedModifier are the Creeper literal
// AvoidEntityGoal<Cat>(this, 6.0f, 1.0, 1.2) ctor args — the ONE consumer wired so far (Creeper flees
// Cat). maxDist 6.0 is the search+range radius; the walk/sprint modifiers switch on the 7-block
// (49.0 = 7^2) proximity band in tick(). A future declaration seam can carry per-goal overrides; these
// are the faithful default for the wired consumer.
//
//	[VERIFIED CFR Creeper.registerGoals: new AvoidEntityGoal<>(this, Cat.class, 6.0f, 1.0, 1.2);
//	 same for Ocelot.class.]
const (
	avoidDefaultMaxDist      = 6.0
	avoidWalkSpeedModifier   = 1.0
	avoidSprintSpeedModifier = 1.2
)

// avoidSprintDistSqr is AvoidEntityGoal.tick proximity band: within 49.0 (== 7^2) blocks^2 of the
// threat the mob SPRINTS (sprintSpeedModifier); farther out it WALKS (walkSpeedModifier).
//
//	[VERIFIED javap AvoidEntityGoal.tick: mob.distanceToSqr(toAvoid); ldc2_w 49.0d; dcmpg; iflt (sprint).]
const avoidSprintDistSqr = 49.0

// avoidPosAwayHorizontal / avoidPosAwayVertical are DefaultRandomPos.getPosAway(mob, 16, 7, avoidPos)
// horizontalDist / verticalDist args — the flee-search radius.
//
//	[VERIFIED javap AvoidEntityGoal.canUse: bipush 16; bipush 7; toAvoid.position();
//	 invokestatic DefaultRandomPos.getPosAway(PathfinderMob, int, int, Vec3).]
const (
	avoidPosAwayHorizontal = 16
	avoidPosAwayVertical   = 7
)

// avoidMaxXzRadiansFromDir is DefaultRandomPos.getPosAway maxXzRadiansFromDir arg (pi/2) passed to
// RandomPos.generateRandomDirectionWithinRadians — the flee direction is drawn within +/-90 deg of the
// away-from-threat heading. The jar literal is the float-widened double 1.5707963705062866.
//
//	[VERIFIED CFR DefaultRandomPos.getPosAway: generateRandomDirectionWithinRadians(random, 0.0,
//	 horizontalDist, verticalDist, 0, dirAway.x, dirAway.z, 1.5707963705062866).]
const avoidMaxXzRadiansFromDir = 1.5707963705062866

// avoidEntityGoal ports net.minecraft.world.entity.ai.goal.AvoidEntityGoal<T> (flag {MOVE}). It carries
// the per-goal avoided-class id + the maxDist + the two speed modifiers (the ctor args), and the
// captured flee-target id (the toAvoid field) + the committed flee pos (the path analogue).
type avoidEntityGoal struct {
	baseGoal
	avoidType           entity.ID // AvoidEntityGoal.avoidClass — the class to flee (per-goal)
	maxDist             float64   // AvoidEntityGoal.maxDist — the search + range radius (float widened to double)
	walkSpeedModifier   float64   // AvoidEntityGoal.walkSpeedModifier — the far-band move speed multiplier
	sprintSpeedModifier float64   // AvoidEntityGoal.sprintSpeedModifier — the near-band (<7 blocks) multiplier

	// targetPredicate is the OPTIONAL AvoidEntityGoal.<init> selector argument (the 5-arg ctor
	// `AvoidEntityGoal(Mob, Class<T>, float, double, double, Predicate<LivingEntity>)` form — the
	// vanilla-only PREDICATE that further filters the target class). nil == no extra filter (the
	// 4-arg ctor the Creeper/Ocelot avoid uses). When non-nil, canUse DROPS any candidate that
	// fails the predicate (e.g. Fox's player-avoid drops a player the fox trusts; Fox's wolf-avoid
	// drops a tamed wolf; the fox's polar-bear-avoid drops the threat when the fox is defending).
	targetPredicate func(t *TickLoop, mob, candidate *Entity) bool
	// playerScanPredicate is the fox-PLAYER-avoid predicate (different shape: candidate is a
	// player id, not an *Entity — players live in t.players, NOT the entity store). nil ==
	// the standard mob-avoid path. Set by newAvoidEntityGoalPlayer; honored by canUse's player
	// scan (the isPlayerScan branch).
	playerScanPredicate func(t *TickLoop, mob *Entity, playerID int32) bool
	// isPlayerScan is the canUse branch flag (set by newAvoidEntityGoalPlayer; honored by
	// canUse). The player avoid scans t.players via nearestPlayerIDAt; every other avoid scans
	// the entity store via nearestEntityOfTypeAt.
	isPlayerScan bool

	toAvoid int32 // AvoidEntityGoal.toAvoid (the captured threat id, 0 == none) — set by canUse, cleared by stop
	// wantX/Y/Z is the committed flee pos (the AvoidEntityGoal.path analogue): canUse computes it via
	// getPosAway + the acceptance gate, start() hands it to the nav. No synchronous Path exists in v1.
	wantX, wantY, wantZ float64
	haveWant            bool
}

// newAvoidEntityGoal builds the flee goal with the MOVE flag (AvoidEntityGoal ctor:
// setFlags(EnumSet.of(Goal$Flag.MOVE))) and the ctor args (avoided class, maxDist, walk/sprint speeds).
//
//	[VERIFIED javap AvoidEntityGoal.<init>: putfield avoidClass/maxDist/walkSpeedModifier/
//	 sprintSpeedModifier; EnumSet.of(Goal$Flag.MOVE); setFlags.]
func newAvoidEntityGoal(avoidType entity.ID, maxDist, walkSpeed, sprintSpeed float64) *avoidEntityGoal {
	return &avoidEntityGoal{
		baseGoal:            newBaseGoal(flagMove),
		avoidType:           avoidType,
		maxDist:             maxDist,
		walkSpeedModifier:   walkSpeed,
		sprintSpeedModifier: sprintSpeed,
	}
}

// newAvoidEntityGoalWithPredicate builds the 5-arg-cform AvoidEntityGoal(Mob, Class<T>, float, double,
// double, Predicate) — the 5-arg ctor the FOX uses (player: AVOID_PLAYERS + !trusts + !isDefending;
// wolf: !tame + !isDefending; polar-bear: !isDefending). The walk/sprint speeds default to the
// shared Creeper literals (1.0/1.2) — the fox's per-callsite speeds (1.6/1.4 for player, 1.6/1.4
// for wolf, 1.6/1.4 for polar bear) thread through here.
func newAvoidEntityGoalWithPredicate(avoidType entity.ID, maxDist, walkSpeed, sprintSpeed float64, predicate func(t *TickLoop, mob, candidate *Entity) bool) *avoidEntityGoal {
	g := newAvoidEntityGoal(avoidType, maxDist, walkSpeed, sprintSpeed)
	g.targetPredicate = predicate
	return g
}

// newAvoidEntityGoalPlayer builds the fox's PLAYER-avoid variant — it scans t.players (the
// player seam, NOT the entity store) via nearestPlayerIDAt (the existing seam the look-avoid
// + the NearestAttackableTargetGoal<Player> branch use). Without this special-case the
// avoidEntityGoal would never find a player (players live in t.players, not entities). Cite
// AvoidEntityGoal (player) — the player class lives outside the entity store.
func newAvoidEntityGoalPlayer(maxDist, walkSpeed, sprintSpeed float64, predicate func(t *TickLoop, mob *Entity, playerID int32) bool) *avoidEntityGoal {
	g := newAvoidEntityGoal(entity.Player.ID, maxDist, walkSpeed, sprintSpeed)
	// Override canUse via a player-scan shim: the inherited canUse's nearestEntityOfTypeAt
	// scan would never find a player; we replace it with a Player-scanning canUse (the
	// player branch of nearestPlayerIDAt + the predicate). The canUse method override on the
	// goal struct is the cleanest seam; we mark this by setting a sentinel that we read in
	// the canUse body.
	g.targetPredicate = nil // player predicate is a different shape (no *Entity, just id)
	g.playerScanPredicate = predicate
	g.isPlayerScan = true
	return g
}

// canUse ports AvoidEntityGoal.canUse (bytecode-verified this session): find the nearest avoided-class
// entity within maxDist (toAvoid); if none, return false (BEFORE any RNG). Else draw a flee pos away
// from it (getPosAway 16,7,+/-pi/2); if none, false. REJECT if the flee pos is closer to the threat than
// the mob already is. Else commit the flee pos (the createPath != null analogue).
//
//	[VERIFIED javap AvoidEntityGoal.canUse: getNearestEntity(getEntitiesOfClass(avoidClass,
//	 inflate(maxDist,3.0,maxDist)), avoidEntityTargeting, mob, x, y, z) -> toAvoid; ifnull -> 0;
//	 DefaultRandomPos.getPosAway(mob, 16, 7, toAvoid.position()) -> v; ifnull -> 0;
//	 toAvoid.distanceToSqr(v) < toAvoid.distanceToSqr(mob) -> 0; createPath(v, 0) != null.]
func (g *avoidEntityGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	// Resolve the threat (id + position) — the player-avoid scans t.players, every other avoid
	// scans the entity store. The threat coords are needed for getPosAway (the flee direction).
	var tx, ty, tz float64
	if g.isPlayerScan {
		id, ok := nearestPlayerIDAt(t, e.x, e.y, e.z, g.maxDist)
		if !ok {
			return false
		}
		g.toAvoid = id
		if g.playerScanPredicate != nil && !g.playerScanPredicate(t, e, id) {
			return false
		}
		p := t.playerByEntityID(id)
		if p == nil { // the player left between scan and read (no RNG)
			return false
		}
		tx, ty, tz = p.x, p.y, p.z
	} else {
		// toAvoid = getNearestEntity(getEntitiesOfClass(avoidClass, ...), avoidEntityTargeting, mob, ...).
		// nearestEntityOfTypeAt is the mob-vs-mob getNearestEntity port (ai_goals_target.go): the nearest live
		// entity of the avoided type within maxDist, or none. Deferrals (search box vertical band, eyeY basis,
		// forCombat filters) cited in the file header.
		id, ok := nearestEntityOfTypeAt(t, e, g.avoidType, g.maxDist)
		if !ok { // toAvoid == null
			return false
		}
		g.toAvoid = id
		threat, ok2 := t.cur().entities.get(id)
		if !ok2 || threat == nil { // the candidate vanished between search and read — treat as no threat (no RNG)
			return false
		}
		// The 5-arg-cform AvoidEntityGoal(Mob, Class, float, double, double, Predicate<LivingEntity>)
		// selector — the fox's per-class predicates (AVOID_PLAYERS+!trusts+!isDefending / !tame+!isDefending
		// / !isDefending) drop a candidate that fails the gate. nil for the bare 4-arg ctor.
		if g.targetPredicate != nil && !g.targetPredicate(t, e, threat) {
			return false
		}
		tx, ty, tz = threat.x, threat.y, threat.z
	}
	// DefaultRandomPos.getPosAway(mob, 16, 7, toAvoid.position()): the flee pos AWAY from the threat.
	// This is the ONLY RNG in canUse (best-of-10; per candidate nextFloat + nextDouble + nextInt), drawn
	// ONLY now that a threat exists.
	px, py, pz, found := getPosAway(mobRandom(e), e, tx, tz)
	if !found { // posAway == null
		return false
	}
	// REJECT if the flee pos is CLOSER to the threat than the mob is (do not flee TOWARD the threat):
	// toAvoid.distanceToSqr(posAway) < toAvoid.distanceToSqr(mob). Both distances are from the THREAT.
	dPos := sqrDist(tx, ty, tz, px, py, pz)
	dMob := sqrDist(tx, ty, tz, e.x, e.y, e.z)
	if dPos < dMob {
		return false
	}
	// createPath(posAway, 0) != null: v1 has no synchronous Path; the accepted flee pos IS the commit.
	g.wantX, g.wantY, g.wantZ = px, py, pz
	g.haveWant = true
	return true
}

// canContinueToUse ports AvoidEntityGoal.canContinueToUse = !pathNav.isDone(). In v1 nav, "not done"
// == the mob still has a pending wanted target (hasTarget), mirroring PanicGoal.canContinueToUse.
//
//	[VERIFIED javap AvoidEntityGoal.canContinueToUse: pathNav.isDone(); ifne iconst_0; iconst_1 (==!isDone).]
func (g *avoidEntityGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return e.ai != nil && e.ai.hasTarget
}

// start ports AvoidEntityGoal.start = pathNav.moveTo(path, walkSpeedModifier): commit the flee pos as
// the nav want at the WALK speed (the moveTo(path, walkSpeedModifier) analogue). The speed modifier
// scales MOVEMENT_SPEED (setWantTargetSpeed carries blocks/tick, like MeleeAttackGoal chase). tick()
// later switches to the sprint modifier inside the 7-block band. NO RNG.
//
//	[VERIFIED javap AvoidEntityGoal.start: pathNav.moveTo(path, walkSpeedModifier).]
func (g *avoidEntityGoal) start(_ *TickLoop, e *Entity) {
	if e.ai == nil || !g.haveWant {
		return
	}
	speed := e.getAttributeValue(attribute.MovementSpeed) * g.walkSpeedModifier
	e.ai.setWantTargetSpeed(g.wantX, g.wantY, g.wantZ, speed)
}

// tick ports AvoidEntityGoal.tick: switch the nav SPEED by proximity to the threat — sprint within
// 7 blocks (distanceToSqr < 49.0), else walk. The mob keeps heading to the committed flee pos (start
// want); tick only re-sets the SPEED modifier, exactly as vanilla setSpeedModifier does. NO RNG.
//
//	[VERIFIED javap AvoidEntityGoal.tick: mob.distanceToSqr(toAvoid) < 49.0 ?
//	 getNavigation().setSpeedModifier(sprintSpeedModifier) : setSpeedModifier(walkSpeedModifier).]
func (g *avoidEntityGoal) tick(t *TickLoop, e *Entity) {
	if e.ai == nil || g.toAvoid == 0 {
		return
	}
	// Resolve the threat position (player vs entity).
	var tx, ty, tz float64
	if g.isPlayerScan {
		p := t.playerByEntityID(g.toAvoid)
		if p == nil {
			return // the player left — hold the current want; canContinueToUse ends it when nav is done
		}
		tx, ty, tz = p.x, p.y, p.z
	} else {
		threat, ok := t.cur().entities.get(g.toAvoid)
		if !ok || threat == nil {
			return // the threat left the world — hold the current want; canContinueToUse ends it when nav is done
		}
		tx, ty, tz = threat.x, threat.y, threat.z
	}
	// mob.distanceToSqr(toAvoid): the squared mob->threat distance (feet-y, the entityDistSqr basis).
	d := sqrDist(e.x, e.y, e.z, tx, ty, tz)
	// setSpeedModifier(sprint | walk): re-set the SPEED of the SAME committed want. Multiply MOVEMENT_SPEED
	// by the chosen modifier (the setWantTargetSpeed seam), keeping the flee destination unchanged.
	var mod float64
	if d < avoidSprintDistSqr {
		mod = g.sprintSpeedModifier
	} else {
		mod = g.walkSpeedModifier
	}
	e.ai.setWantTargetSpeed(g.wantX, g.wantY, g.wantZ, e.getAttributeValue(attribute.MovementSpeed)*mod)
}

// stop ports AvoidEntityGoal.stop = toAvoid = null. The v1 nav lifecycle equivalent also clears the
// pending want (the running->stopped edge), mirroring PanicGoal.stop clearWantTarget.
//
//	[VERIFIED javap AvoidEntityGoal.stop: aload_0; aconst_null; putfield toAvoid.]
func (g *avoidEntityGoal) stop(_ *TickLoop, e *Entity) {
	g.toAvoid = 0
	g.haveWant = false
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// getPosAway ports net.minecraft.world.entity.ai.util.DefaultRandomPos.getPosAway(mob, horizontalDist=16,
// verticalDist=7, avoidPos) for the mob flee direction. It returns the ABSOLUTE flee pos (world coords)
// or found=false when none of the 10 candidates survives. The threat position is passed as (avoidX,
// avoidZ) — the mob own (e.x, e.z) supplies the away vector.
//
// The jar:
//
//	Vec3 dirAway = mob.position().subtract(avoidPos);                          // AWAY from the threat
//	return RandomPos.generateRandomPos(mob, () -> {
//	    BlockPos dir = generateRandomDirectionWithinRadians(random, 0.0, 16, 7, 0,
//	        dirAway.x, dirAway.z, pi/2);
//	    if (dir == null) return null;
//	    return generateRandomPosTowardDirection(mob, 16, restrict, dir);       // + mob pos, home bias
//	});
//
// RandomPos.generateRandomPos loops i<10 (NO break), keeping the candidate with the STRICTLY-greatest
// getWalkTargetValue; a base PathfinderMob getWalkTargetValue is 0.0f for every pos (jar-verified), so
// with bestWeight starting at -Inf the FIRST non-null candidate wins (0.0 > -Inf; later 0.0 is not >
// 0.0) — yet all 10 supplier calls still RUN (all draws consumed). Returns Vec3.atBottomCenterOf(best)
// (block center X/Z, integer Y) or null.
//
//	[VERIFIED CFR DefaultRandomPos.getPosAway + RandomPos.generateRandomPos(Supplier,ToDoubleFunction):
//	 dirAway = position().subtract(avoidPos); for i<10 no break, keep value>bestWeight; getWalkTargetValue
//	 == 0.0f; atBottomCenterOf(best) or null. RandomPos.generateRandomDirectionWithinRadians:
//	 yRadiansCenter = atan2(zDir,xDir) - pi/2; yRadians = center + (2f*nextFloat()-1f)*maxRadians;
//	 dist = lerp(sqrt(nextDouble()), minH, maxH) * SQRT_OF_TWO; xt=-dist*sin(yRadians); zt=dist*cos;
//	 |xt|>maxH || |zt|>maxH -> null; yt = nextInt(2*v+1)-v + flyingHeight.]
func getPosAway(r *entityRandom, e *Entity, avoidX, avoidZ float64) (x, y, z float64, found bool) {
	// dirAway = mob.position() - avoidPos (only x/z used by the direction draw; y unused there).
	dirX := e.x - avoidX
	dirZ := e.z - avoidZ

	bestWeight := math.Inf(-1)
	haveBest := false
	var bestX, bestY, bestZ float64

	for i := 0; i < 10; i++ { // generateRandomPos: for i<10, NO break (all 10 supplier calls run)
		bx, by, bz, ok := generateRandomDirectionWithinRadians(r, 0.0, avoidPosAwayHorizontal, avoidPosAwayVertical, 0, dirX, dirZ, avoidMaxXzRadiansFromDir)
		if !ok {
			continue // dir == null -> supplier returned null (this candidate contributes nothing)
		}
		// generateRandomPosTowardDirection(mob, 16, restrict, dir): a v1 mob has NO home, so the
		// hasHome()&&xzDist>1.0 bias branch never runs (ZERO extra draws) — a cited no-op (matches the
		// stroll/panic ports). pos = BlockPos.containing(dir.x + mob.x, dir.y + mob.y, dir.z + mob.z).
		posX := math.Floor(bx + e.x)
		posY := math.Floor(by + e.y)
		posZ := math.Floor(bz + e.z)
		// getWalkTargetValue(pos) == 0.0f for a base PathfinderMob (jar-verified). weight > bestWeight
		// keeps the first candidate only (0.0 > -Inf on the first; 0.0 not > 0.0 after) — but the loop
		// runs all 10 so every candidate draws are consumed.
		const walkTargetValue = 0.0
		if walkTargetValue > bestWeight {
			bestWeight = walkTargetValue
			// Vec3.atBottomCenterOf(bestPos): block center on X/Z (+0.5), integer bottom Y.
			bestX, bestY, bestZ = posX+0.5, posY, posZ+0.5
			haveBest = true
		}
	}
	if !haveBest {
		return 0, 0, 0, false // generateRandomPos returned null (no candidate survived)
	}
	return bestX, bestY, bestZ, true
}

// generateRandomDirectionWithinRadians ports net.minecraft.world.entity.ai.util.RandomPos
// .generateRandomDirectionWithinRadians for the getPosAway supplier — the +/-maxRadians-of-heading
// direction draw. DRAW ORDER (the bit-fragile lockstep contract): nextFloat() (yaw jitter), then
// nextDouble() (radial distance), then nextInt(2*v+1) (vertical). Returns the relative direction
// BlockPos as float xt/yt/zt (before the mob-position offset), or ok=false when |xt|>maxH or |zt|>maxH.
//
//	[VERIFIED CFR RandomPos.generateRandomDirectionWithinRadians:
//	 yRadiansCenter = Mth.atan2(zDir, xDir) - 1.5707963705062866;
//	 yRadians = yRadiansCenter + (2f*random.nextFloat()-1f) * maxXzRadiansFromDir;
//	 dist = Mth.lerp(Math.sqrt(random.nextDouble()), minH, maxH) * Mth.SQRT_OF_TWO;
//	 xt = -dist*sin(yRadians); zt = dist*cos(yRadians);
//	 if (|xt|>maxH || |zt|>maxH) return null;
//	 yt = random.nextInt(2*v+1) - v + flyingHeight;
//	 return BlockPos.containing(xt, yt, zt).]
//
// FAITHFUL-SCOPE (matches ai_random.go stance): atan2/sin/cos/sqrt use math.* (Java uses Mth.atan2
// lookup table + Math.sin/cos); the DRAW ORDER + the exact nextFloat/nextDouble/nextInt sequence is the
// observable lockstep contract the codebase pins, not the bit-exact trig value (no test asserts
// bit-exact vanilla sequences — ai_random.go WHY-note). Mth.SQRT_OF_TWO = (float)Math.sqrt(2.0f).
func generateRandomDirectionWithinRadians(r *entityRandom, minHorizontalDist float64, maxHorizontalDist, verticalDist, flyingHeight int, xDir, zDir, maxXzRadiansFromDir float64) (xt, yt, zt float64, ok bool) {
	maxH := float64(maxHorizontalDist)
	// yRadiansCenter = atan2(zDir, xDir) - pi/2  (the exact float-widened double the jar subtracts).
	yRadiansCenter := math.Atan2(zDir, xDir) - avoidMaxXzRadiansFromDir
	// DRAW 1: nextFloat() — the +/-maxRadians yaw jitter about the away heading.
	yRadians := yRadiansCenter + float64(2.0*r.nextFloat()-1.0)*maxXzRadiansFromDir
	// DRAW 2: nextDouble() — the radial distance (lerp over sqrt(u) between min/max, x SQRT_OF_TWO).
	sqrtOfTwo := float64(float32(math.Sqrt(2.0))) // Mth.SQRT_OF_TWO = (float)Math.sqrt(2.0f)
	// Mth.lerp(delta, start, end) = start + delta*(end-start) — the shared lerp (explosion.go).
	dist := lerp(math.Sqrt(r.nextDouble()), minHorizontalDist, maxH) * sqrtOfTwo
	xtF := -dist * math.Sin(yRadians)
	ztF := dist * math.Cos(yRadians)
	if math.Abs(xtF) > maxH || math.Abs(ztF) > maxH {
		return 0, 0, 0, false // return null
	}
	// DRAW 3: nextInt(2*v+1) - v + flyingHeight — the vertical component.
	ytI := r.nextInt(2*verticalDist+1) - verticalDist + flyingHeight
	// BlockPos.containing(xt, yt, zt): floor to block coords (the relative direction, before the mob offset).
	return math.Floor(xtF), float64(ytI), math.Floor(ztF), true
}

// sqrDist — the squared distance between two world points (Entity.distanceToSqr basis).
func sqrDist(ax, ay, az, bx, by, bz float64) float64 {
	dx, dy, dz := ax-bx, ay-by, az-bz
	return dx*dx + dy*dy + dz*dz
}

// Compile-time assertion: avoidEntityGoal IS a server.Goal.
var _ Goal = (*avoidEntityGoal)(nil)
