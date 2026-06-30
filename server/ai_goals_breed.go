package server

// ai_goals_breed.go — MOB-SUB-09 (Phase 33, Plan 03, C1): the GO-NATIVE BreedGoal@3 plus the
// shared identity helper adjustedTickDelay, the faithful Mob.isPanicking read, the Animal.canMate
// partner gate (entity.go), getFreePartner's same-class scan, and breed() (the
// Animal.spawnChildFromBreeding child spawn + parent cooldown + inLove reset + the two breed-path
// RNG draws).
//
// PORTED (the STANDING MANDATE, idiomatic Go, never a GPL paste) from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar), read via javap -c -p this session:
//
//   - net.minecraft.world.entity.ai.goal.BreedGoal (priority 3, speed 1.0, flags {MOVE, LOOK}):
//       PARTNER_TARGETING = forNonCombat().range(8.0).ignoreLineOfSight();
//       canUse(): if (!animal.isInLove()) return false; partner = getFreePartner(); return partner != null;
//       canContinueToUse(): partner.isAlive() && partner.isInLove() && loveTime < 60 && !partner.isPanicking();
//       stop(): partner = null; loveTime = 0;
//       tick(): lookAt(partner); navigation.moveTo(partner, speed); ++loveTime;
//               if (loveTime >= adjustedTickDelay(60) && distanceToSqr(partner) < 9.0) breed();
//       getFreePartner(): nearest same-class in getBoundingBox().inflate(8.0), where
//                         animal.canMate(other) && !other.isPanicking();
//       breed(): animal.spawnChildFromBreeding(level, partner).
//   - net.minecraft.world.entity.animal.Animal.spawnChildFromBreeding -> getBreedOffspring (the
//       variant nextBoolean() draw) -> setBaby(true) -> finalizeSpawnChildFromBreeding (setAge(6000)
//       both parents + resetLove both + broadcastEntityEvent(18) + XP orb 1+nextInt(7)).
//   - net.minecraft.world.entity.ai.goal.Goal.adjustedTickDelay(int): returns n at 20 TPS (identity).
//   - net.minecraft.world.entity.Mob.isPanicking(): the PanicGoal currently RUNNING.
//
// SINGLE-OWNER (TICK-05): the goal + breed() run on the tick goroutine over tick-owned state. The
// same-class scan + child spawn use the OWNING-region store (the documented v5 same-region cut); a
// goal tick reads/writes the world only through *TickLoop + *Entity (no goroutine, no xsync).

// adjustedTickDelay ports net.minecraft.world.entity.ai.goal.Goal.adjustedTickDelay(int n): it scales
// a goal's tick interval by the server TPS, but at the canonical 20 TPS it returns n UNCHANGED — an
// identity. BreedGoal's loveTime threshold is adjustedTickDelay(60) == 60; FollowParentGoal's re-path
// interval is adjustedTickDelay(10) == 10. This is a DIFFERENT helper from the reduced-tick-delay
// helper (ai_goals_passive.go: ceil(n/2), which HALVES the interval to 30/5) — the breed/follow goals
// use adjustedTickDelay only, never the reduced/halved helper, so the thresholds are the full 60/10.
//
//	[VERIFIED javap Goal.adjustedTickDelay: at 20 TPS the time-scale factor is 1.0, so the method
//	 returns its argument unchanged (the only divergence is when serverTps != 20, never the case here).]
func adjustedTickDelay(n int) int { return n }

// isPanicking ports net.minecraft.world.entity.Mob.isPanicking(): true iff the mob's PanicGoal is
// currently RUNNING. Our goal selector stores []*wrappedGoal each with a `running` bit (ai_goal.go);
// the PanicGoal is the *panicGoal (ai_goals_panic.go). So we scan the mob's goals for the *panicGoal
// and return its running state. This is the FAITHFUL read — NOT a hurtTime>0 proxy (hurtTime decays
// in ~10 ticks and fires for any damage, while a pig panics for the full flee duration only on a
// panic_causes hit; the running-PanicGoal state is exactly what vanilla checks). Pure read, no RNG.
//
//	[VERIFIED javap Mob.isPanicking: getGoalsByClass(PanicGoal.class).anyMatch(WrappedGoal::isRunning)
//	 — the running PanicGoal among the mob's goals.]
func (t *TickLoop) isPanicking(e *Entity) bool {
	if e == nil || e.ai == nil {
		return false
	}
	for _, wg := range e.ai.goals.goals {
		if _, ok := wg.g.(*panicGoal); ok {
			return wg.running
		}
	}
	return false
}

// breedRange is BreedGoal's PARTNER_TARGETING range: forNonCombat().range(8.0) — getFreePartner
// inflates the animal's bounding box by 8.0 and scans it. We approximate the inflated-AABB scan with
// a center-distance gate (distSqr <= 8.0²): the partner must be within 8 blocks. (The exact AABB
// inflate vs center-distance differ only for partners straddling the 8-block edge; the nearest-within
// pick is identical for any pair actually close enough to navigate together and breed.)
//
//	[VERIFIED javap BreedGoal.getFreePartner: getBoundingBox().inflate(8.0d); getNearbyEntities.]
const breedRange = 8.0

// breedLoveThreshold is BreedGoal's adjustedTickDelay(60) — the loveTime the goal must reach (with
// the partner within distSqr<9.0) before breed() fires. 60 ticks == 3 seconds of mutual courting.
const breedLoveThreshold = 60

// breedDistanceSqr is BreedGoal's tick() breed gate: distanceToSqr(partner) < 9.0 (within 3 blocks).
const breedDistanceSqr = 9.0

// breedGoal ports net.minecraft.world.entity.ai.goal.BreedGoal (flags {MOVE, LOOK}, priority 3,
// speed 1.0). It finds a same-class in-love partner (getFreePartner), navigates to it, and after
// loveTime reaches the threshold within 3 blocks, breeds. partner + loveTime are the goal's carried
// state (the Java partner/loveTime fields).
type breedGoal struct {
	baseGoal
	speedModifier float64
	partner       *Entity // BreedGoal.partner — the chosen free partner
	loveTime      int     // BreedGoal.loveTime — courting counter, ++ per tick
}

// newBreedGoal builds the BreedGoal with the pig's defaults: flags {MOVE, LOOK} (the ctor's
// setFlags(EnumSet.of(MOVE, LOOK))) and speed 1.0 (Pig.registerGoals @3 BreedGoal(mob, 1.0)).
//
//	[VERIFIED javap BreedGoal.<init>: setFlags(EnumSet.of(Goal$Flag.MOVE, Goal$Flag.LOOK));
//	 Pig.registerGoals: iconst_3; new BreedGoal; dconst_1 (1.0d); BreedGoal.<init>(Animal, double).]
func newBreedGoal(speed float64) *breedGoal {
	return &breedGoal{
		baseGoal:      newBaseGoal(flagMove | flagLook),
		speedModifier: speed,
	}
}

// getFreePartner ports BreedGoal.getFreePartner: scan the same-class animals near the mob within
// breedRange (the inflate(8.0) box), keep those where canMate(other) && !other.isPanicking(), and
// return the NEAREST. The scan uses the OWNING-region store (t.cur().entities.near at the goal-tick
// position) — the documented v5 same-region cut (a cross-region partner is not found until one
// wanders into the other's region; full-fidelity cross-region match deferred). Draws no RNG.
//
//	[VERIFIED javap BreedGoal.getFreePartner: getNearbyEntities(partnerClass, PARTNER_TARGETING,
//	 animal, inflate(8.0)); for each: canMate(other) && !isPanicking() && distanceToSqr < best ->
//	 keep nearest; return best (or null).]
func (g *breedGoal) getFreePartner(t *TickLoop, e *Entity) *Entity {
	var best *Entity
	bestDistSqr := breedRange * breedRange // start at the range bound (8.0²); only closer wins
	for _, other := range t.cur().entities.near(e.x, e.z, 1) {
		if other == e || other.typ != e.typ {
			continue // not a same-class candidate
		}
		if !e.canMate(other) || t.isPanicking(other) {
			continue // not a free, in-love, non-panicking partner
		}
		d := entityDistSqr(e, other)
		if d < bestDistSqr {
			bestDistSqr = d
			best = other
		}
	}
	return best
}

// canUse ports BreedGoal.canUse: `if (!animal.isInLove()) return false; partner = getFreePartner();
// return partner != null`. The isInLove gate on line 1 is the oracle-safety contract — the un-fed
// oracle pig (inLove == 0) returns false here, so the breed path (and its RNG draws) never fires.
func (g *breedGoal) canUse(t *TickLoop, e *Entity) bool {
	if !e.isInLove() {
		return false
	}
	g.partner = g.getFreePartner(t, e)
	return g.partner != nil
}

// canContinueToUse ports BreedGoal.canContinueToUse: the partner is alive && still in love &&
// loveTime < 60 && !partner.isPanicking(). (The loveTime<60 bound stops the goal once courting
// completes — tick() breeds at loveTime>=adjustedTickDelay(60) within 3 blocks; if the pair never
// closed to <9.0 by loveTime 60 the goal ends and re-acquires.)
func (g *breedGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.partner != nil && g.partner.isAlive() && g.partner.isInLove() &&
		g.loveTime < breedLoveThreshold && !t.isPanicking(g.partner)
}

// start ports BreedGoal.start: BreedGoal has no explicit start() override beyond the Goal default,
// and loveTime is reset on stop(); we mirror that by ensuring loveTime starts at 0 for a fresh run.
func (g *breedGoal) start(_ *TickLoop, _ *Entity) {
	g.loveTime = 0
}

// tick ports BreedGoal.tick: aim the head at the partner (setLookAt), navigate toward it at speed,
// ++loveTime, and once loveTime >= adjustedTickDelay(60) AND within distSqr < 9.0, breed(). The look
// seam reuses the temptGoal yaw write; the move seam is setWantTarget (the navigation.moveTo(partner,
// speed) analog — the want carries the partner pos, the nav tick applies the speed).
func (g *breedGoal) tick(t *TickLoop, e *Entity) {
	if g.partner == nil || e.ai == nil {
		return
	}
	// setLookAt(partner): aim the head/body toward the partner.
	yaw := yawTowardDeg(g.partner.x-e.x, g.partner.z-e.z)
	e.headYaw = yaw
	e.yaw = yaw
	// navigation.moveTo(partner, speed): want the partner's position (nav applies speedModifier).
	e.ai.setWantTarget(g.partner.x, g.partner.y, g.partner.z)
	g.loveTime++
	if g.loveTime >= adjustedTickDelay(breedLoveThreshold) && entityDistSqr(e, g.partner) < breedDistanceSqr {
		t.breed(e, g.partner)
	}
}

// stop ports BreedGoal.stop: drop the partner and reset loveTime to 0.
func (g *breedGoal) stop(_ *TickLoop, e *Entity) {
	g.partner = nil
	g.loveTime = 0
	if e.ai != nil {
		e.ai.clearWantTarget() // the navigation stops when no want is set
	}
}

// breed ports BreedGoal.breed() == animal.spawnChildFromBreeding(level, partner): spawn a baby pig at
// the parents' position, set both parents to the breeding cooldown (age 6000), reset both inLove, fire
// the heart-event burst, and award the XP orb. The RNG draw ORDER is the jar's (verified verbatim):
//
//	spawnChildFromBreeding -> getBreedOffspring  : DRAW 1 — variant nextBoolean() (which parent's
//	                                                PigVariant the baby inherits) on e's RNG.
//	                       -> setBaby(true)       : child.breedAge = BABY_START_AGE (-24000).
//	                       -> finalizeSpawnChildFromBreeding:
//	                            setAge(6000) both parents; resetLove both; broadcastEntityEvent(18);
//	                            XP orb               : DRAW 2 — 1 + nextInt(7) on e's RNG.
//
// BOTH draws come from `e` (the breeding INITIATOR — the animal whose BreedGoal ticked; vanilla draws
// both from `this`/`animal` = e), and BOTH must be mirrored host-side in 33-04's .star breed path in
// this exact order (the lockstep rule). They fire ONLY here, mid-breeding — dormant on the un-fed
// oracle pig, so the pinned oracle RNG stream is undisturbed.
//
// The child spawns into the OWNING region's store (spawnVanillaPig delegates to spawnDeclaredMob ->
// entities.add on cur(); the parents are in cur() during their own goal tick, so the child shares
// their region — the same-region cut). After setting child.breedAge = BABY_START_AGE we refreshDimensions
// (the baby half-scale hitbox, Plan A) and broadcastBabyFlag (push DATA_BABY_ID=true to trackers).
//
//	[VERIFIED javap BreedGoal.breed: animal.spawnChildFromBreeding(level, partner);
//	 Animal.spawnChildFromBreeding: getBreedOffspring (variant nextBoolean draw) -> setBaby(true) ->
//	 snapTo -> finalizeSpawnChildFromBreeding (setAge(6000)×2, resetLove×2, broadcastEntityEvent(18),
//	 ExperienceOrb 1+nextInt(7)); Pig.getBreedOffspring: nextBoolean() ? getVariant() : partner.getVariant().]
func (t *TickLoop) breed(e, partner *Entity) {
	// spawnChildFromBreeding -> getBreedOffspring: spawn the child at the breeding animal's position
	// (vanilla snapTo(animal.getX/Y/Z) puts the baby on the parent). spawnVanillaPig builds the same
	// declared vanilla pig the parents are.
	child := t.spawnVanillaPig(e.x, e.y, e.z)

	// DRAW 1 (getBreedOffspring): the variant nextBoolean() — which parent's PigVariant the baby
	// inherits. We CONSUME the draw from e's RNG in lockstep with vanilla (the draw + its order is the
	// observable contract). The PigVariant subsystem is not yet ported (Entity has no variant field —
	// cite-deferred, NOT baked away): when a variant field lands, assign
	// child.variant = (inheritFromInitiator ? e : partner).variant at this exact seam. The selected
	// parent is computed so the wiring is ready (and to document the faithful branch).
	inheritFromInitiator := mobRandom(e).nextBoolean()
	_ = inheritFromInitiator // PigVariant deferred (no variant field) — the DRAW is what the oracle pins.
	// (When variant lands: variantSource := e; if !inheritFromInitiator { variantSource = partner }; child.variant = variantSource.variant.)

	// setBaby(true): the child is a baby — breedAge = BABY_START_AGE; refresh to the half-scale hitbox
	// (Plan A refreshDimensions) and push DATA_BABY_ID=true to the child's trackers (Plan A broadcastBabyFlag).
	child.breedAge = babyStartAge
	child.refreshDimensions()
	t.broadcastBabyFlag(child)

	// finalizeSpawnChildFromBreeding: both parents to the 6000-tick breeding cooldown (setAge(6000)),
	// reset both inLove (resetLove == inLove = 0), and fire the heart-event burst (broadcastEntityEvent(18)).
	e.breedAge = breedingCooldownAge
	partner.breedAge = breedingCooldownAge
	e.inLove = 0
	partner.inLove = 0
	t.broadcastHearts(e)

	// DRAW 2 (finalizeSpawnChildFromBreeding): the XP orb — 1 + nextInt(7) on e's RNG. awardExperienceOrbs
	// spawns the orb into e's owning region (death_mob.go), the same store-add path the death XP rides.
	t.awardExperienceOrbs(e, 1+mobRandom(e).nextInt(7))
}

// babyStartAge is net.minecraft.world.entity.AgeableMob.BABY_START_AGE (-24000): the age a freshly
// bred baby is set to (it ticks up toward 0 = adult over 20 minutes). Plan A established breedAge<0
// == isBaby; this is the literal a child is born at.
//
//	[VERIFIED javap AgeableMob: BABY_START_AGE = -24000 (the setBaby(true) age).]
const babyStartAge = -24000

// breedingCooldownAge is the parent cooldown finalizeSpawnChildFromBreeding sets (setAge(6000)): a
// just-bred adult cannot breed again for 6000 ticks (5 minutes) as the age machine ticks back to 0.
//
//	[VERIFIED javap Animal.finalizeSpawnChildFromBreeding: sipush 6000; setAge(6000) on both parents.]
const breedingCooldownAge = 6000

// entityDistSqr is net.minecraft.world.entity.Entity.distanceToSqr(Entity): the squared distance
// between two entities' positions (the BreedGoal distSqr<9.0 + FollowParentGoal 9..256 / nearest-pick
// gate all read this). Plain float math, no RNG.
//
//	[VERIFIED javap Entity.distanceToSqr(Entity): dx² + dy² + dz² over getX/Y/Z differences.]
func entityDistSqr(a, b *Entity) float64 {
	dx := a.x - b.x
	dy := a.y - b.y
	dz := a.z - b.z
	return dx*dx + dy*dy + dz*dz
}
