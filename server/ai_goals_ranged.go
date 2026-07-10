package server

// ai_goals_ranged.go — PROJECTILE-01 (Task #8): the RangedBowAttackGoal, PORTED (STANDING MANDATE:
// idiomatic Go, no GPL paste) from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, CFR/javap this session — see .planning/PROJECTILE-JARNOTES.md):
//
//   - net.minecraft.world.entity.ai.goal.RangedBowAttackGoal: flags {MOVE, LOOK}, requiresUpdateEveryTick.
//     Skeleton builds it as new RangedBowAttackGoal<>(this, 1.0, 20|40, 15.0) → speedModifier 1.0,
//     attackIntervalMin 40 (NORMAL) / 20 (HARD), attackRadius 15 → attackRadiusSqr 225.
//   - AbstractSkeleton.performRangedAttack: builds an arrow aimed at the target's 1/3 height with a
//     ballistic lob (yd + dist*0.2), launched at velocity 1.6 with inaccuracy 14 - difficulty*4.
//
// v1 STUBS (cited, never silently dropped):
//   - Line-of-sight (Sensing.hasLineOfSight): NO sensing subsystem in v1 → the cited "always visible"
//     stub (hasLineOfSight == true). seeTime therefore only ever counts UP.
//   - Bow item / isUsingItem / getTicksUsingItem: NO item-use subsystem on mobs in v1. The skeleton
//     ALWAYS holds a bow (isHoldingBow == true, cited), and the 20-tick charge is modeled by a per-goal
//     drawTicks counter that mirrors LivingEntity.getTicksUsingItem — the skeleton releases at exactly 20
//     (BowItem.getPowerForTime(20) == 1.0f, so power is always 1.0), which is the vanilla observable.
//   - MoveControl.strafe (the strafe kiting): NO MoveControl.strafe seam in v1 → the strafe draws are
//     PRESERVED (nextFloat() gated on strafingTime>=20) to keep the mob's RNG stream in lockstep, but the
//     strafe MOVE is a cited no-op; the mob still stops its nav at close range (navigation.stop) and
//     re-paths when the target leaves attackRadius (the observable "backs up / closes to bow range").
//
// THE PIG ORACLE IS UNTOUCHED: only the skeleton declares this goal; it never ticks on a pig, so no draw
// reaches the pinned pig stream.

import (
	"math"
	"math/rand/v2"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
)

// RangedBowAttackGoal constants (verified CFR — the skeleton's ctor args + the jar literals).
const (
	bowSpeedModifier      = 1.0   // RangedBowAttackGoal.speedModifier (skeleton: 1.0)
	bowAttackIntervalMin  = 40    // AbstractSkeleton.getAttackInterval() (NORMAL)
	bowHardAttackInterval = 20    // AbstractSkeleton.getHardAttackInterval() (HARD)
	// Bogged overrides BOTH intervals (javap Bogged.getAttackInterval bipush 70 /
	// getHardAttackInterval bipush 50): a bogged fires SLOWER than a plain skeleton/stray.
	boggedAttackIntervalMin  = 70 // Bogged.getAttackInterval() (NORMAL)
	boggedHardAttackInterval = 50 // Bogged.getHardAttackInterval() (HARD)
	bowAttackRadius       = 15.0  // skeleton attackRadius → attackRadiusSqr 225
	bowAttackRadiusSqr    = bowAttackRadius * bowAttackRadius
	bowFullDrawTicks      = 20    // getTicksUsingItem() release point (power = getPowerForTime(20) = 1.0)
	bowReleasePower       = 1.0   // BowItem.getPowerForTime(20) == 1.0f (full charge)
	bowLaunchVelocity     = 1.6   // performRangedAttack velocity arg
	bowLookMaxStep        = 30.0  // lookAt(target, 30, 30) — the head-turn cap
	arrowLaunchSpeed      = bowLaunchVelocity

	// Illusioner's RangedBowAttackGoal ctor args differ from the skeleton's. Illusioner.registerGoals
	// builds new RangedBowAttackGoal(this, 0.5d, 20, 15.0f) @6: speedModifier 0.5, attackIntervalMin 20,
	// attackRadius 15.0 (radiusSqr 225 — same as the skeleton). The skeleton builds (this, 1.0, 20|40,
	// 15.0). Only speed + interval differ; the radius is shared.
	//	[VERIFIED javap Illusioner.registerGoals @6: ldc2_w 0.5d; bipush 20; ldc 15.0f;
	//	 RangedBowAttackGoal.<init>(Monster;DIF). skeleton: 1.0d / 20|40 / 15.0f.]
	illusionerBowSpeedModifier     = 0.5 // Illusioner RangedBowAttackGoal speedModifier
	illusionerBowAttackIntervalMin = 20  // Illusioner RangedBowAttackGoal attackIntervalMin

	// AbstractSkeleton.performRangedAttack shoot-sound (playSound(SKELETON_SHOOT, 1.0f, 1/(nextFloat()*0.4+0.8))).
	skeletonShootSoundID   = 1491 // SoundEvents.SKELETON_SHOOT (data/soundid: "entity.skeleton.shoot")
	skeletonShootVolume    = 1.0  // fconst_1 volume arg
	skeletonShootPitchScale = 0.4 // ldc 0.4f (nextFloat()*0.4 ...)
	skeletonShootPitchBase  = 0.8 // ldc 0.8f (... + 0.8)
)

// rangedBowAttackGoal is the ported RangedBowAttackGoal state (the jar's private fields of the same
// names). drawTicks replaces the LivingEntity item-use timer (v1 has no item-use subsystem).
type rangedBowAttackGoal struct {
	baseGoal
	attackTime       int  // RangedBowAttackGoal.attackTime (start -1): the inter-shot cooldown
	seeTime          int  // RangedBowAttackGoal.seeTime: LoS run length (v1: only counts up)
	strafingClockwise bool
	strafingBackwards bool
	strafingTime     int  // RangedBowAttackGoal.strafingTime (start -1)
	drawTicks        int  // v1 stand-in for getTicksUsingItem(): -1 == not drawing, else 0..20
	// speedModifier + attackIntervalMin are the per-DECLARATION ctor args (RangedBowAttackGoal(this,
	// speed, interval, radius)). The skeleton declares 1.0/40, the illusioner 0.5/20. The Go goal is the
	// SAME class routed for both hostiles from buildNativeGoal (which I do not own), so the values are
	// resolved lazily from the ticking entity's type via resolveBowParams (first-tick latch) rather than
	// passed at construction. speedModifier<0 marks "unresolved" so resolveBowParams runs once.
	speedModifier    float64
	attackIntervalMin int
}

// newRangedBowAttackGoal builds the skeleton's bow goal with the vanilla start values (-1 sentinels).
func newRangedBowAttackGoal() *rangedBowAttackGoal {
	return &rangedBowAttackGoal{
		baseGoal:     newBaseGoal(flagMove | flagLook),
		attackTime:   -1,
		strafingTime: -1,
		drawTicks:    -1,
		speedModifier: -1, // < 0 == unresolved; resolveBowParams latches the per-type ctor args on first tick
	}
}

// resolveBowParams latches the per-DECLARATION RangedBowAttackGoal ctor args (speedModifier +
// attackIntervalMin) from the ticking entity's type, once. The skeleton family (Skeleton/Stray/Bogged/
// WitherSkeleton) builds RangedBowAttackGoal(this, 1.0, 40, 15.0); the Illusioner builds (this, 0.5, 20,
// 15.0). Because buildNativeGoal (which I do not own) constructs this goal identically for every hostile
// that declares kind="ranged_bow_attack", the per-type divergence is resolved here on the first tick when
// the concrete entity is in hand. Idempotent (guarded by speedModifier < 0). RNG-free.
//
// The attackIntervalMin is the reassessWeaponGoal difficulty pick (javap AbstractSkeleton.reassessWeaponGoal
// @62: interval = getHardAttackInterval(); if getDifficulty() != HARD -> interval = getAttackInterval()).
// A plain skeleton/stray uses getAttackInterval()=40 (HARD 20); a BOGGED overrides both to
// getAttackInterval()=70 (HARD 50) (javap Bogged.getAttackInterval bipush 70 / getHardAttackInterval bipush
// 50). serverDifficulty is the cited NORMAL const, so the != HARD branch takes the NORMAL interval today,
// but the HARD pick is written 1:1 so it lands the moment difficulty becomes a live read.
//	[VERIFIED javap Illusioner.registerGoals @6 RangedBowAttackGoal(this, 0.5d, 20, 15.0f);
//	 AbstractSkeleton.reassessWeaponGoal @4 RangedBowAttackGoal(this, 1.0d, 20|40, 15.0f);
//	 Bogged.getAttackInterval()=70 / getHardAttackInterval()=50; AbstractSkeleton 40 / 20.]
func (g *rangedBowAttackGoal) resolveBowParams(e *Entity) {
	if g.speedModifier >= 0 {
		return // already latched
	}
	hard := serverDifficulty == difficultyHard
	switch e.typ {
	case entity.Illusioner.ID:
		g.speedModifier = illusionerBowSpeedModifier      // 0.5
		g.attackIntervalMin = illusionerBowAttackIntervalMin // 20 (fixed ctor arg; Illusioner is not an AbstractSkeleton)
	case entity.Bogged.ID:
		g.speedModifier = bowSpeedModifier // 1.0 (skeleton family)
		if hard {
			g.attackIntervalMin = boggedHardAttackInterval // 50 (Bogged.getHardAttackInterval)
		} else {
			g.attackIntervalMin = boggedAttackIntervalMin // 70 (Bogged.getAttackInterval)
		}
	default:
		g.speedModifier = bowSpeedModifier // 1.0 (skeleton family; Skeleton/Stray/WitherSkeleton)
		if hard {
			g.attackIntervalMin = bowHardAttackInterval // 20 (AbstractSkeleton.getHardAttackInterval)
		} else {
			g.attackIntervalMin = bowAttackIntervalMin // 40 (AbstractSkeleton.getAttackInterval)
		}
	}
}

func (g *rangedBowAttackGoal) requiresUpdateEveryTick() bool { return true }

// canUse is net.minecraft.world.entity.ai.goal.RangedBowAttackGoal.canUse:
//
//	return this.mob.getTarget() != null && this.isHoldingBow();
//
// isHoldingBow() == mob.isHolding(Items.BOW). Now that the skeleton carries a REAL bow in MAINHAND
// (populateSkeletonEquipment, the AbstractSkeleton.populateDefaultEquipmentSlots port), isHoldingBow
// reads the actual held item (isHoldingItem) instead of the previous cited constant — so a skeleton
// that ever LOST its bow would correctly drop the ranged goal, exactly as vanilla's reassessWeaponGoal.
//
//	[VERIFIED javap RangedBowAttackGoal.canUse: getTarget() != null && isHoldingBow();
//	 isHoldingBow(): mob.isHolding(Items.BOW).]
func (g *rangedBowAttackGoal) canUse(t *TickLoop, e *Entity) bool {
	return mobTarget(e) != 0 && e.isHoldingItem(int32(item.Bow.ID))
}

// canContinueToUse is RangedBowAttackGoal.canContinueToUse:
//
//	return (this.canUse() || !this.mob.getNavigation().isDone()) && this.isHoldingBow();
//
// isDone == !navigation.active(). isHoldingBow() == mob.isHolding(Items.BOW) (the real held item).
//
//	[VERIFIED javap RangedBowAttackGoal.canContinueToUse: (canUse() || !navigation.isDone()) &&
//	 isHoldingBow().]
func (g *rangedBowAttackGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	if !e.isHoldingItem(int32(item.Bow.ID)) {
		return false
	}
	return g.canUse(t, e) || e.ai.navigation.active()
}

// start: setAggressive(true) (a cited client-visual metadata bit, no-op in v1 — behaviorally identical).
func (g *rangedBowAttackGoal) start(t *TickLoop, e *Entity) {}

// stop: setAggressive(false); seeTime=0; attackTime=-1; stopUsingItem(). Reset the draw + clear the nav want.
func (g *rangedBowAttackGoal) stop(t *TickLoop, e *Entity) {
	g.seeTime = 0
	g.attackTime = -1
	g.drawTicks = -1
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// tick is the port of RangedBowAttackGoal.tick. It manages the see-time counter, the move/strafe decision,
// the head look, and the charge-and-release firing (performRangedAttack at full draw).
func (g *rangedBowAttackGoal) tick(t *TickLoop, e *Entity) {
	if e.ai == nil {
		return
	}
	id := mobTarget(e)
	if id == 0 {
		return
	}
	target := t.playerByEntityID(id)
	if target == nil {
		return
	}
	g.resolveBowParams(e) // latch the per-type ctor args (skeleton 1.0/40, illusioner 0.5/20) once

	targetDistSqr := distanceToSqrPlayer(target, e)
	// hasLineOfSight: the real per-tick-cached raycast (sensing.go, divergence C-4). seeTime now
	// tracks true visibility, so a skeleton behind a wall stops closing/firing until it sees the target.
	hasLineOfSight := t.sensingHasLineOfSight(e, target)
	hadLineOfSight := g.seeTime > 0
	if hasLineOfSight != hadLineOfSight {
		g.seeTime = 0
	}
	if hasLineOfSight {
		g.seeTime++
	} else {
		g.seeTime--
	}

	// Move toward the target while out of bow range or not yet locked on (seeTime<20); else stop + strafe.
	if targetDistSqr > bowAttackRadiusSqr || g.seeTime < 20 {
		getSpeed := e.getAttributeValue(attribute.MovementSpeed) * g.speedModifier
		e.ai.setWantTargetSpeed(target.x, target.y, target.z, getSpeed) // navigation.moveTo(target, speed)
		g.strafingTime = -1
	} else {
		e.ai.clearWantTarget() // navigation.stop()
		g.strafingTime++
	}

	// Strafe direction flips (nextFloat() < 0.3 each) — the draws are PRESERVED for lockstep even though
	// the strafe MOVE is a v1 no-op (no MoveControl.strafe seam).
	if g.strafingTime >= 20 {
		if float64(mobRandom(e).nextFloat()) < 0.3 {
			g.strafingClockwise = !g.strafingClockwise
		}
		if float64(mobRandom(e).nextFloat()) < 0.3 {
			g.strafingBackwards = !g.strafingBackwards
		}
		g.strafingTime = 0
	}
	if g.strafingTime > -1 {
		if targetDistSqr > bowAttackRadiusSqr*0.75 {
			g.strafingBackwards = false
		} else if targetDistSqr < bowAttackRadiusSqr*0.25 {
			g.strafingBackwards = true
		}
		// moveControl.strafe(...): cited no-op in v1.
	}

	// lookAt(target, 30, 30): turn the head toward the target (the LOOK flag). Body yaw is the nav's.
	yRotD := yawTowardDeg(target.x-e.x, target.z-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, bowLookMaxStep)

	// Charge-and-release: model isUsingItem() via drawTicks. While drawing, count toward the 20-tick full
	// draw; at >=20 with LoS, release (performRangedAttack) and set the inter-shot cooldown. When not
	// drawing, count down attackTime and begin a new draw once it elapses.
	if g.drawTicks >= 0 { // isUsingItem()
		if !hasLineOfSight && g.seeTime < -60 {
			g.drawTicks = -1 // stopUsingItem()
		} else if hasLineOfSight {
			g.drawTicks++
			if g.drawTicks >= bowFullDrawTicks { // getTicksUsingItem() >= 20
				g.drawTicks = -1 // stopUsingItem()
				t.performRangedAttack(e, target, bowReleasePower)
				g.attackTime = g.attackIntervalMin
			}
		}
	} else {
		g.attackTime--
		if g.attackTime <= 0 && g.seeTime >= -60 {
			g.drawTicks = 0 // startUsingItem(BOW)
		}
	}
}

// performRangedAttack is the port of AbstractSkeleton.performRangedAttack(target, power). It builds the
// arrow's baseDamage (setBaseDamageFromMob), computes the ballistic aim (target 1/3 height + dist*0.2 lob),
// applies the shoot spread (normalize + triangle noise * scale), and spawns the arrow into the tick store.
//
//	[VERIFIED CFR AbstractSkeleton.performRangedAttack + Projectile.shoot/getMovementToShoot:
//	 xd = target.x - mob.x; yd = target.getY(0.3333) - arrow.getY(); zd = target.z - mob.z;
//	 dist = sqrt(xd²+zd²); shoot(xd, yd + dist*0.2, zd, 1.6, 14 - difficulty*4);
//	 movement = normalize(xd,yd',zd).add(triangle(0, 0.0172275*unc) ×3).scale(1.6).]
func (t *TickLoop) performRangedAttack(e *Entity, target *tickPlayer, power float64) {
	r := mobRandom(e)

	// setBaseDamageFromMob(power): baseDamage = power*2.0 + triangle(difficulty*0.11, 0.57425).
	diff := float64(serverDifficulty) // NORMAL == 2 (cited stub)
	baseDamage := power*2.0 + arrowTriangle(r, diff*0.11, 0.57425)

	// The arrow spawns at the shooter's shoulder-ish height. Vanilla's AbstractArrow ctor places it just
	// below eye level; v1 uses the shooter eye height as the launch origin (getY(0.3333)-style is applied
	// to the TARGET below, not the source). Use the mob's y + eye offset as a faithful launch height.
	arrowY := e.y + e.eyeHeightForArrow()

	xd := target.x - e.x
	yd := (target.y + float64(playerHeight)*0.3333333333333333) - arrowY // target.getY(0.3333)
	zd := target.z - e.z
	dist := math.Sqrt(xd*xd + zd*zd)
	ydLob := yd + dist*0.2 // the ballistic lob

	// inaccuracy = 14 - difficulty.getId()*4 (NORMAL == 6).
	inaccuracy := 14.0 - diff*4.0

	// getMovementToShoot: normalize(dir) + triangle noise*(0.0172275*inaccuracy) per axis, scaled by 1.6.
	vx, vy, vz := normalizeVec3(xd, ydLob, zd)
	spread := 0.0172275 * inaccuracy
	vx += arrowTriangle(r, 0, spread)
	vy += arrowTriangle(r, 0, spread)
	vz += arrowTriangle(r, 0, spread)
	vx *= arrowLaunchSpeed
	vy *= arrowLaunchSpeed
	vz *= arrowLaunchSpeed

	a := t.spawnArrow(e.id, e.x, arrowY, e.z, vx, vy, vz, baseDamage)
	// Tipped-arrow variants (Stray/Bogged getArrow override): tag the fired Arrow with the variant's
	// MobEffectInstance so it applies on a landed hit (Arrow.doPostHurtEffects). A plain skeleton's
	// getArrow returns an un-tipped arrow (no effects). RNG-free; no draw reaches the pig oracle.
	if fx := variantArrowEffects(e); fx != nil {
		a.arrowEffects = fx
	}

	// AbstractSkeleton.performRangedAttack ends with:
	//   playSound(SoundEvents.SKELETON_SHOOT, 1.0f, 1.0f / (getRandom().nextFloat() * 0.4f + 0.8f));
	// The nextFloat() is ONE draw on the MOB's per-entity RandomSource, drawn AFTER the four arrow-spread
	// triangles above (setBaseDamageFromMob + the 3 getMovementToShoot axes) -- it is the LAST draw of the
	// shot. This draw is REQUIRED for RNG lockstep: without it, every draw after shot #1 desyncs from the
	// jar. The pitch = 1.0 / (nextFloat()*0.4 + 0.8); volume = 1.0; sound source = HOSTILE (Monster.
	// getSoundSource). Broadcast to the mob's trackers on a fresh server sound seed (the Level.playSound
	// soundSeedGenerator.nextLong() analogue -- never the mob stream, matching playMobHurtSound).
	//	[VERIFIED javap AbstractSkeleton.performRangedAttack tail @143-170: getstatic SoundEvents.
	//	 SKELETON_SHOOT; fconst_1; fconst_1; getRandom().nextFloat(); ldc 0.4f; fmul; ldc 0.8f; fadd;
	//	 fdiv; playSound(SoundEvent,F,F). soundid 1491 "entity.skeleton.shoot".]
	shootPitch := float32(1.0) / (r.nextFloat()*skeletonShootPitchScale + skeletonShootPitchBase)
	t.broadcastToTrackers(e.id, encodeSoundEntity(skeletonShootSoundID, soundSourceHostile, e.id, skeletonShootVolume, shootPitch, rand.Int64()))
}

// eyeHeightForArrow is the launch-height offset for a mob firing a bow. Vanilla spawns the arrow at
// AbstractArrow's ctor position (source shoulder height). v1 uses a cited standing-eye fraction of the
// mob's collision height (0.85 × height ≈ standing eye) so the arrow leaves near the head. Structured so a
// real getEyeHeight() read replaces it later.
func (e *Entity) eyeHeightForArrow() float64 {
	return e.height * 0.85
}

// normalizeVec3 returns the unit vector of (x,y,z) (Vec3.normalize()); a zero vector normalizes to zero.
func normalizeVec3(x, y, z float64) (float64, float64, float64) {
	l := math.Sqrt(x*x + y*y + z*z)
	if l < 1e-12 {
		return 0, 0, 0
	}
	return x / l, y / l, z / l
}
