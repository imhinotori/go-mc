package server

// ai_goals_llama.go — GAP 4 (LLAMA SPIT): the Llama targetSelector + RangedAttackGoal that FIRE the
// otherwise-computed-but-never-launched llama spit, PORTED 1:1 (the STANDING MANDATE, idiomatic non-1:1 Go,
// no GPL paste) from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//   - net.minecraft.world.entity.animal.equine.Llama.registerGoals: goalSelector @3
//     RangedAttackGoal(this, 1.25, 40, 20.0f); targetSelector @1 Llama$LlamaHurtByTargetGoal, @2
//     Llama$LlamaAttackWolfGoal.
//   - Llama$LlamaHurtByTargetGoal extends HurtByTargetGoal (the base retaliate-at-attacker). The ONLY
//     override is canContinueToUse: if the llama didSpit, clear didSpit + drop the target (it stops chasing
//     once it has spat). v1 reuses the base newHurtByTargetGoal() and cite-defers the didSpit refinement
//     (no didSpit field on Entity — a non-owned change; the base retaliate is the observable behavior).
//   - Llama$LlamaAttackWolfGoal extends NearestAttackableTargetGoal<Wolf>(this, Wolf.class, 16, false, true,
//     target -> !((Wolf)target).isTame()) with getFollowDistance() == super * 0.25 (a llama only engages an
//     UNTAMED wolf within a quarter of its FOLLOW_RANGE). Ported as a SELF-CONTAINED target goal here (not
//     via the shared nearestAttackableTargetGoal parameterization) to keep the concurrently-edited
//     ai_goals_target.go untouched.
//   - net.minecraft.world.entity.ai.goal.RangedAttackGoal: flags {MOVE, LOOK}, requiresUpdateEveryTick.
//     tick: chase while out of radius / seeTime<5, else stop; look at the target; on attackTime hitting 0
//     with LoS -> performRangedAttack -> Llama.spit -> spawn a LlamaSpit toward the target.
//   - Llama.spit(LivingEntity): new LlamaSpit(level, this); dx = target.x - x; dy = target.getY(0.3333) -
//     spit.getY(); dz = target.z - z; horiz = sqrt(dx²+dz²) * 0.20000000298023224; spawnProjectileUsingShoot(
//     spit, level, EMPTY, dx, dy+horiz, dz, 1.5f, 10.0f); setDidSpit(true); playSound(LLAMA_SPIT).
//
// THE PIG ORACLE IS UNTOUCHED: a passive pig declares NONE of these goals (they are wired only on the
// Go-native Llama AI, newLlamaAI in horse.go), so they never tick on it — no draw reaches the pinned pig
// stream. The llama's spit launch draws the Projectile.shoot gaussian spread on the LLAMA's own rng stream.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Llama ranged-attack + spit constants (VERIFIED javap Llama.registerGoals / Llama.spit / RangedAttackGoal
// this session).
const (
	// llamaRangedSpeed / llamaRangedInterval / llamaRangedRadius are the RangedAttackGoal(this, 1.25, 40,
	// 20.0f) ctor args (goalSelector @3): speedModifier 1.25, attackIntervalMin==attackIntervalMax==40 (the
	// 4-arg ctor sets both to the single int), attackRadius 20.0. Cite Llama.registerGoals @3
	// (RangedAttackGoal(this, ldc2_w 1.25d, bipush 40, ldc 20.0f)).
	llamaRangedSpeed     = 1.25
	llamaRangedInterval  = 40
	llamaRangedRadius    = 20.0
	llamaRangedRadiusSqr = llamaRangedRadius * llamaRangedRadius

	// llamaSpitLaunchVelocity / llamaSpitInaccuracy are the Projectile.spawnProjectileUsingShoot(spit, level,
	// EMPTY, dx, dy+horiz, dz, 1.5f, 10.0f) velocity + inaccuracy args in Llama.spit. Cite Llama.spit offsets
	// 87-107 (ldc_w 1.5f; ldc_w 10.0f).
	llamaSpitLaunchVelocity = 1.5
	llamaSpitInaccuracy     = 10.0

	// llamaSpitTargetYFraction is the target aim height Llama.spit reads: target.getY(0.3333333333333333)
	// == target.y + target.height * 1/3. Cite Llama.spit offset 23-30 (ldc2_w 0.3333333333333333d; getY(d)).
	llamaSpitTargetYFraction = 0.3333333333333333

	// llamaSpitBulletDamage is LlamaSpit.onHitEntity's damage: hurtOrSimulate(damageSources().spit(this,
	// owner), 1.0f). Cite LlamaSpit.onHitEntity (fconst_1 -> 1.0F). Carried on the spawned spit for the
	// flight-tick hit (the flight tick itself is cite-deferred — see spawnLlamaSpit).
	llamaSpitBulletDamage = 1.0

	// llamaAttackWolfFollowScale is Llama$LlamaAttackWolfGoal.getFollowDistance()'s multiplier:
	// super.getFollowDistance() * 0.25 (ldc2_w 0.25d; dmul). A llama only acquires a wolf within a quarter of
	// its FOLLOW_RANGE. Cite Llama$LlamaAttackWolfGoal.getFollowDistance.
	llamaAttackWolfFollowScale = 0.25
)

// --- llamaAttackWolfTargetGoal (targetSelector @2) ----------------------------------------------

// llamaAttackWolfTargetGoal ports Llama$LlamaAttackWolfGoal (flags {TARGET}) — a NearestAttackableTargetGoal
// <Wolf> that acquires the nearest UNTAMED wolf within getFollowDistance() (super * 0.25). It carries the
// SAME RNG gate as the shared NearestAttackableTargetGoal (nextInt(reducedTickDelay(10))=5), and the SAME
// TargetGoal.start/stop (commit/clear the acquired mob id as the llama's attack target). NO anger gate.
// Written self-contained (not via nearestAttackableTargetGoal's targetClass switch) so the concurrently-
// edited ai_goals_target.go is untouched. Cite Llama.registerGoals targetSelector @2 (Llama$LlamaAttackWolfGoal).
type llamaAttackWolfTargetGoal struct {
	baseGoal
	randomInterval int
	target         int32
	forceTrigger   bool // test seam — skip the RNG gate once (mirrors nearestAttackableTargetGoal.forceTrigger)
}

// newLlamaAttackWolfTargetGoal builds the goal with the TARGET flag (NearestAttackableTargetGoal ctor:
// setFlags(EnumSet.of(TARGET))) and the shared halved randomInterval reducedTickDelay(10)==5.
func newLlamaAttackWolfTargetGoal() *llamaAttackWolfTargetGoal {
	return &llamaAttackWolfTargetGoal{
		baseGoal:       newBaseGoal(flagTarget),
		randomInterval: nearestTargetRandomInterval, // reducedTickDelay(10) == 5 (the jar ctor's halved DEFAULT_RANDOM_INTERVAL)
	}
}

// llamaWolfFollowDistance ports Llama$LlamaAttackWolfGoal.getFollowDistance(): super.getFollowDistance() *
// 0.25 == getAttributeValue(FOLLOW_RANGE) * 0.25. Cite Llama$LlamaAttackWolfGoal.getFollowDistance.
func llamaWolfFollowDistance(e *Entity) float64 {
	return e.getAttributeValue(attribute.FollowRange) * llamaAttackWolfFollowScale
}

// canUse ports NearestAttackableTargetGoal.canUse for the wolf goal: the RNG gate (nextInt(randomInterval)),
// then findTarget (nearest untamed wolf within getFollowDistance()), then target != null. Draws EXACTLY ONE
// nextInt per canUse on the llama's own stream (the SAME gate the shared goal draws). Cite
// NearestAttackableTargetGoal.canUse + Llama$LlamaAttackWolfGoal.getFollowDistance/selector.
func (g *llamaAttackWolfTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	if e == nil || e.ai == nil {
		return false
	}
	if !g.forceTrigger {
		if g.randomInterval > 0 && mobRandom(e).nextInt(g.randomInterval) != 0 {
			return false // the 1-in-randomInterval acquire gate (DRAW: nextInt)
		}
	}
	g.forceTrigger = false
	g.target = 0
	// findTarget: getNearestEntity(getEntitiesOfClass(Wolf, searchArea), conditions, mob, x, eyeY, z) — the
	// nearest UNTAMED entity.Wolf.ID within getFollowDistance() (super * 0.25). The selector `!wolf.isTame()`
	// is the e.tame membership on the store entity. Reuses the SAME owning-region near() broad-phase +
	// entityDistSqr nearest-wins loop nearestEntityOfTypeAt uses. Cite Llama$LlamaAttackWolfGoal.
	follow := llamaWolfFollowDistance(e)
	rangeChunks := int(math.Ceil(follow / 16.0))
	if rangeChunks < 1 {
		rangeChunks = 1
	}
	best := follow * follow
	for _, other := range t.cur().entities.near(e.x, e.z, rangeChunks) {
		if other == e || other.dead || other.typ != entity.Wolf.ID {
			continue
		}
		if other.tame { // selector: !((Wolf)target).isTame() — a llama leaves tamed wolves alone
			continue
		}
		d := entityDistSqr(e, other)
		if d <= best {
			best = d
			g.target = other.id
		}
	}
	return g.target != 0
}

// canContinueToUse ports TargetGoal.canContinueToUse: keep targeting while the acquired wolf is alive +
// within getFollowDistance(). Cite TargetGoal.canContinueToUse.
func (g *llamaAttackWolfTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	other, ok := t.cur().entities.get(id)
	if !ok || other.dead {
		return false
	}
	follow := llamaWolfFollowDistance(e)
	return entityDistSqr(e, other) <= follow*follow
}

// start ports NearestAttackableTargetGoal.start: mob.setTarget(this.target). Cite NearestAttackableTargetGoal.start.
func (g *llamaAttackWolfTargetGoal) start(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(g.target)
	}
}

// stop ports TargetGoal.stop: mob.setTarget(null). Cite TargetGoal.stop.
func (g *llamaAttackWolfTargetGoal) stop(_ *TickLoop, e *Entity) {
	g.target = 0
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

var _ Goal = (*llamaAttackWolfTargetGoal)(nil)

// --- llamaRangedAttackGoal (goalSelector @3) ----------------------------------------------------

// llamaRangedAttackGoal ports net.minecraft.world.entity.ai.goal.RangedAttackGoal for the Llama (ctor args
// 1.25, 40, 20.0f): flags {MOVE, LOOK}, requiresUpdateEveryTick. It chases the target while out of the
// attackRadius or seeTime<5, else parks the nav and looks; on attackTime hitting 0 with line-of-sight it
// calls performRangedAttack (Llama.spit -> a LlamaSpit toward the target). Unlike the witch's rangedAttackGoal
// (player-only), the llama's target is a MOB (the wolf it acquired via LlamaAttackWolfGoal), so it resolves the
// target through the owning-region entity store. Cite RangedAttackGoal + Llama.performRangedAttack.
type llamaRangedAttackGoal struct {
	baseGoal
	attackTime int // RangedAttackGoal.attackTime (start -1)
	seeTime    int
}

// newLlamaRangedAttackGoal builds the goal with {MOVE, LOOK} and attackTime -1 (RangedAttackGoal ctor:
// attackTime = -1; setFlags(EnumSet.of(MOVE, LOOK))). Cite RangedAttackGoal.<init>.
func newLlamaRangedAttackGoal() *llamaRangedAttackGoal {
	return &llamaRangedAttackGoal{baseGoal: newBaseGoal(flagMove | flagLook), attackTime: -1}
}

// requiresUpdateEveryTick ports RangedAttackGoal.requiresUpdateEveryTick = true.
func (g *llamaRangedAttackGoal) requiresUpdateEveryTick() bool { return true }

// canUse ports RangedAttackGoal.canUse: target = getTarget(); return target != null && target.isAlive().
// The llama's target is a MOB (a wolf) resolved via the owning-region store. Cite RangedAttackGoal.canUse.
func (g *llamaRangedAttackGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	other, ok := t.cur().entities.get(id)
	return ok && !other.dead
}

// canContinueToUse ports RangedAttackGoal.canContinueToUse: canUse() || (target.isAlive() &&
// !navigation.isDone()). Cite RangedAttackGoal.canContinueToUse.
func (g *llamaRangedAttackGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	return g.canUse(t, e) || e.ai.navigation.active()
}

// start ports RangedAttackGoal.start (empty in the jar).
func (g *llamaRangedAttackGoal) start(_ *TickLoop, _ *Entity) {}

// stop ports RangedAttackGoal.stop: target = null; seeTime = 0; attackTime = -1. Cite RangedAttackGoal.stop.
func (g *llamaRangedAttackGoal) stop(_ *TickLoop, e *Entity) {
	g.seeTime = 0
	g.attackTime = -1
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// tick ports RangedAttackGoal.tick (VERIFIED javap this session): distanceToSqr(target); seeTime++/reset by
// LoS; chase while dist > radiusSqr || seeTime < 5 else stop; setLookAt(target, 30, 30); --attackTime; when
// it hits 0 with LoS -> performRangedAttack + attackTime = floor(f*(max-min)+min) (min==max==40 -> 40); when
// attackTime < 0 (initial) -> attackTime = floor(lerp(sqrt/radius, min, max)) (== 40). Cite RangedAttackGoal.tick.
func (g *llamaRangedAttackGoal) tick(t *TickLoop, e *Entity) {
	if e.ai == nil {
		return
	}
	id := e.ai.getTarget()
	if id == 0 {
		return
	}
	target, ok := t.cur().entities.get(id)
	if !ok || target.dead {
		return
	}
	// distanceToSqr(target.getX(), getY(), getZ()) — the squared feet-to-feet distance to the mob target.
	dx := e.x - target.x
	dy := e.y - target.y
	dz := e.z - target.z
	targetDistSqr := dx*dx + dy*dy + dz*dz

	hasLineOfSight := t.sensingHasLineOfSightEntity(e, target) // getSensing().hasLineOfSight(target)
	if hasLineOfSight {
		g.seeTime++
	} else {
		g.seeTime = 0
	}

	// Chase while out of range OR not yet seen for 5 ticks; else stop the nav.
	if targetDistSqr > llamaRangedRadiusSqr || g.seeTime < 5 {
		getSpeed := e.getAttributeValue(attribute.MovementSpeed) * llamaRangedSpeed
		e.ai.setWantTargetSpeed(target.x, target.y, target.z, getSpeed) // navigation.moveTo(target, speed)
	} else {
		e.ai.clearWantTarget() // navigation.stop()
	}

	// getLookControl().setLookAt(target, 30, 30): head-only turn toward the target.
	yRotD := yawTowardDeg(target.x-e.x, target.z-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, meleeLookMaxYawStep)

	// --attackTime (pre-decrement, dup_x1 in the bytecode). When it reaches exactly 0 -> fire (if LoS).
	g.attackTime--
	if g.attackTime == 0 {
		if !hasLineOfSight {
			return
		}
		// f = clamp(sqrt(distSqr)/attackRadius, 0.1, 1.0) (the performRangedAttack power arg, unused by
		// Llama.spit but computed for the exact call shape).
		f := float32(math.Sqrt(targetDistSqr)) / float32(llamaRangedRadius)
		if f < 0.1 {
			f = 0.1
		} else if f > 1.0 {
			f = 1.0
		}
		t.llamaPerformRangedAttack(e, target, f)
		// attackTime = Mth.floor(f * (attackIntervalMax - attackIntervalMin) + attackIntervalMin). The Llama's
		// ctor is the 4-arg RangedAttackGoal(this, 1.25, 40, 20.0f), which sets attackIntervalMin ==
		// attackIntervalMax == 40, so (max - min) == 0 and this is floor(f*0 + 40) == 40 regardless of f. Cite
		// RangedAttackGoal.tick offsets 190-213 (fmul; iadd; Mth.floor).
		g.attackTime = int(math.Floor(float64(f)*float64(llamaRangedInterval-llamaRangedInterval) + float64(llamaRangedInterval)))
	} else if g.attackTime < 0 {
		// attackTime = Mth.floor(Mth.lerp(sqrt(distSqr)/attackRadius, attackIntervalMin, attackIntervalMax));
		// min==max==40 -> lerp(_, 40, 40) == 40 for any delta. Cite RangedAttackGoal.tick offsets 219-253.
		g.attackTime = llamaRangedInterval
	}
}

var _ Goal = (*llamaRangedAttackGoal)(nil)

// llamaPerformRangedAttack ports Llama.performRangedAttack(target, power) -> Llama.spit(target): spawn a
// LlamaSpit toward the target's 1/3-height, then setDidSpit(true) + the LLAMA_SPIT sound. Cite
// Llama.performRangedAttack + Llama.spit.
func (t *TickLoop) llamaPerformRangedAttack(e *Entity, target *Entity, _ float32) {
	// target.getY(0.3333333333333333) == target.y + target.height * 1/3 (getY(scale) = y + height*scale).
	targetY := target.y + float64(target.height)*llamaSpitTargetYFraction
	t.spawnLlamaSpit(e, target.x, targetY, target.z)
	// setDidSpit(true) + playSound(LLAMA_SPIT): the didSpit latch (read by Llama$LlamaHurtByTargetGoal
	// .canContinueToUse to drop the retaliation target after a spit) + the client shoot sound are cited
	// deferrals — no didSpit field on Entity (a non-owned change) and the sound is a client cue. The
	// observable gameplay (a LlamaSpit launched at the target) is faithful. Cite Llama.spit offsets 111+.
}

// spawnLlamaSpit ports Llama.spit's projectile launch: build a LlamaSpit at the llama's mouth, aim its
// velocity at (tx,ty,tz) via Projectile.shoot (normalize the direction * velocity 1.5, with the
// llama-stream gaussian inaccuracy 10.0), and add it to the owning region's store. The LlamaSpit FLIGHT +
// onHitEntity (1.0 spit damage) is a CITE-DEFERRED tick: tickLlamaSpits would need a dispatch in
// tick_phases.go (not owned — the projectile agent owns that seam), so v1 ships the faithful SPAWN + launch
// vector (the observable "the llama spits at its target"); the projectile's per-tick flight lands when the
// dispatch is wired. Cite Llama.spit + LlamaSpit ctor + Projectile.spawnProjectileUsingShoot(spit, level,
// EMPTY, dx, dy+horiz, dz, 1.5f, 10.0f).
func (t *TickLoop) spawnLlamaSpit(e *Entity, tx, ty, tz float64) *Entity {
	// The LlamaSpit ctor sets its position near the llama's mouth (getX() - width-based offset, getEyeY()
	// - 0.1, getZ() - offset). v1 collapses the exact mouth offset (which reads yBodyRot) to the eye
	// position — the launch VECTOR (below) is what points the spit at the target, and the small mouth
	// offset is a cited cosmetic. spawnY == getEyeY() analog (y + height*0.85 - 0.1).
	spawnX := e.x
	spawnY := e.y + float64(e.height)*0.85 - 0.10000000149011612
	spawnZ := e.z

	// Llama.spit direction: dx = target.x - llama.x; dy = target.getY(0.3333) - spit.getY(); dz = target.z -
	// llama.z; horiz = sqrt(dx²+dz²) * 0.20000000298023224. The launch DIRECTION is (dx, dy+horiz, dz).
	dx := tx - e.x
	dy := ty - spawnY
	dz := tz - e.z
	horiz := math.Sqrt(dx*dx+dz*dz) * 0.20000000298023224
	dirX, dirY, dirZ := dx, dy+horiz, dz

	// Projectile.shoot(dirX, dirY, dirZ, velocity 1.5, inaccuracy 10.0): normalize the direction, add the
	// per-axis gaussian * (0.0172275 * inaccuracy) spread on the LLAMA's own rng stream, then scale by
	// velocity. This is the SAME normalize->spread->scale shape the arrow/potion launch uses; the gaussian
	// draws are on the shooter's per-entity stream (llama-gated, never the pig oracle). Cite Projectile.shoot.
	vx, vy, vz := normalizeVec3(dirX, dirY, dirZ)
	r := mobRandom(e)
	spread := 0.0172275 * llamaSpitInaccuracy
	vx += arrowTriangle(r, 0, spread)
	vy += arrowTriangle(r, 0, spread)
	vz += arrowTriangle(r, 0, spread)
	vx *= llamaSpitLaunchVelocity
	vy *= llamaSpitLaunchVelocity
	vz *= llamaSpitLaunchVelocity

	spit := NewEntity(t.idAlloc.AllocID(), entity.LlamaSpit, spawnX, spawnY, spawnZ)
	spit.arrowShooterID = e.id // reuse the projectile owner field (the spit's getOwner() == the llama)
	spit.vx, spit.vy, spit.vz = vx, vy, vz
	spit.spawnData = e.id + 1 // ClientboundAddEntity object data == owner link (ownerId+1)

	horizAim := math.Sqrt(vx*vx + vz*vz)
	spit.yaw = float32(mthAtan2(vx, vz) * float64(mthRadToDeg))
	spit.pitch = float32(mthAtan2(vy, horizAim) * float64(mthRadToDeg))
	spit.headYaw = spit.yaw

	owner := t.regionForEntity(spit)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(spit)
	return spit
}
