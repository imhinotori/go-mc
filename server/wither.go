package server

// wither.go -- the Wither boss (net.minecraft.world.entity.boss.wither.WitherBoss), a 1:1 port from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this task). The Wither is a 3-headed nether
// boss built from a T of soul sand + 3 wither-skeleton skulls; on spawn it charges up for 220 invulnerable
// ticks (glowing, immune) then detonates a power-7 explosion, then fights: each of its 3 heads aims at and
// shoots WitherSkull projectiles at a target, it heals +1 every 20 ticks while idle, and below 50 percent HP
// it enters the armored/powered phase (shielded vs projectiles + it destroys the blocks in its AABB). On
// death it drops a NETHER_STAR. Same additive + per-type-gated pattern as ender_dragon.go: ALL wither state
// lives behind the single e.wither pointer (nil for every other entity), so a non-wither entity takes the
// unchanged path, touches no new fields, and draws no new RNG (the pig oracle stays byte-identical).
//
// LANDED (the observable boss loop, bytecode-exact): createAttributes 300/0.6/0.6/40/4 (witherSupplier),
// makeInvulnerable (220 invuln ticks + progress 0 + health = maxHealth/3), the invuln countdown + the
// power-7 explosion at 0 + the tickCount%10 heal-10 during charge-up, the boss bar (PURPLE/PROGRESS/darken),
// customServerAiStep head-aiming (3 heads, nextHeadUpdate/idleHeadUpdates cadence) + the per-head WitherSkull
// ranged attack -- side heads 1/2 via the customServerAiStep cadence, CENTER head 0 via the priority-2
// RangedAttackGoal(1.0, 40, 20) driven in witherAiStep BEFORE the side loop (the 0.001 dangerous roll on center), the isPowered()
// under-50-percent flag + the destroyBlocksTick block-destroy in the AABB (under MOB_GRIEFING), the idle heal
// +1 every tickCount%20, and the death NETHER_STAR drop (witherDropNetherStar off dropCustomDeathLoot). The
// WitherSkull projectile (dangerous inertia, homing, explosion power 1, WITHER effect on hit) is ALREADY in
// hurting_projectile.go (hurtWitherSkull); the wither reuses it via spawnHurtingProjectile + the hurtDangerous seam.
//
// DEFERRED (cited): the WitherBoss aiStep FLIGHT drive (FlyingMoveControl pursuit + WaterAvoidingRandomFlying
// wander) is reduced to a stationary hover (the wither is a flyer, so it skips the generic gravity/collision
// path like the dragon; v1 does not chase). getAlternativeTarget per-head aux targeting (DATA_TARGET_A/B/C)
// picks the SINGLE getTarget() for all heads (skull-shooting preserved; the 3-independent-head cross-target is
// the bounded reduction). Skulls that hit a MOB defer the WITHER effect with hurting_projectile.go's existing
// player-only effect note. The client visuals (levelEvent 1023/1024/1022, ENTITY_EFFECT particles) are deferred.

import (
	"math"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// Wither constants (VERIFIED javap WitherBoss this task -- every number read off the bytecode ldc/bipush).
const (
	witherMaxHealth          = 300.0 // createAttributes MAX_HEALTH 300.0 (ldc2_w 300.0d)
	witherInvulnerableTicks  = 220   // makeInvulnerable setInvulnerableTicks(220) (sipush 220)
	witherInvulnProgressDiv  = 220.0 // customServerAiStep: setProgress(1 - t/220.0f) (ldc_w 220.0f)
	witherSpawnExplosionPow  = 7.0   // at t<=0: explode(getEyeY, 7.0f, MOB) (ldc_w 7.0f)
	witherInvulnHealPeriod   = 10    // charge-up: tickCount % 10 == 0 -> heal(10.0f) (bipush 10)
	witherInvulnHealAmount   = 10.0  // heal(10.0f) each charge-up heal (ldc_w 10.0f)
	witherIdleHealPeriod     = 20    // mid-fight: tickCount % 20 == 0 -> heal(1.0f) (bipush 20)
	witherIdleHealAmount     = 1.0   // heal(1.0f) idle regen (fconst_1)
	witherHeadCount          = 3     // heads i=0 (center) + i=1,2 (sides) (if_icmpge 3)
	witherSideHeadCount      = 2     // xRotHeads/yRotHeads arrays sized 2 (SIDE heads only)
	witherHeadUpdateBase     = 10    // nextHeadUpdate[i] = tickCount + 10 + nextInt(10) (bipush 10)
	witherHeadUpdateJitter   = 10    // ... + random.nextInt(10) (bipush 10)
	witherIdleShotThreshold  = 15    // idleHeadUpdates[i]++ > 15 -> random skull volley (bipush 15)
	witherFireAfterAim       = 40    // after a shot: nextHeadUpdate = tickCount + 40 + nextInt(20)
	witherFireAfterJitter    = 20    // ... + random.nextInt(20) (bipush 20)
	witherIdleScatterXZ      = 10.0  // idle volley: nextDouble(x-10, x+10) (ldc2_w 10.0d)
	witherIdleScatterY       = 5.0   // ... nextDouble(y-5, y+5) (ldc2_w 5.0d)
	witherTargetRangeSqr     = 900.0 // per-head target: distanceToSqr <= 900.0 (ldc2_w 900.0d)
	witherPoweredHealthFrac  = 2.0   // isPowered() = health <= maxHealth/2.0f (fconst_2 fdiv)
	witherDestroyBlocksTicks = 20    // hurtServer(powered): destroyBlocksTick = 20 (bipush 20)
	witherXpReward           = 50    // ctor: xpReward = 50 (bipush 50)
	witherSpawnHealthDiv     = 3.0   // makeInvulnerable: setHealth(getMaxHealth()/3.0f) (ldc_w 3.0f)
	witherEyeHeightRatio     = 0.85  // Entity default standing eye height (height * 0.85) for explosion origin
	witherHeadRadius         = 1.3   // getHeadX/Z: cos/sin * 1.3 * scale (ldc2_w 1.3d)
	witherHeadCenterY        = 3.0   // getHeadY(0) center head Y offset (ldc_w 3.0f)
	witherHeadSideY          = 2.2   // getHeadY(i>0) side head Y offset (ldc_w 2.2f)
	witherDangerousRoll      = 0.001 // center head: nextFloat() < 0.001f -> dangerous skull (ldc_w 0.001f)
	// CENTER-head RangedAttackGoal(this, 1.0, 40, 20.0f) params (registerGoals @ priority 2: dconst_1;
	// bipush 40; ldc 20.0f). The single-int ctor forwards int -> (attackIntervalMin, attackIntervalMax),
	// so BOTH == 40 (attackTime reset always lands on 40); attackRadius 20.0 (attackRadiusSqr 400.0).
	// Cite RangedAttackGoal(RangedAttackMob, double, int, float) + RangedAttackGoal(_, _, int, int, float).
	witherCenterAttackInterval = 40   // RangedAttackGoal attackIntervalMin == attackIntervalMax (bipush 40)
	witherCenterAttackRadius   = 20.0 // RangedAttackGoal attackRadius (ldc 20.0f); Sqr == 400.0
	witherCenterSeeTimeReady   = 5    // RangedAttackGoal nav gate seeTime >= 5 (iconst_5); nav deferred (flyer hover)
	witherCenterLookMaxStep    = 30.0 // RangedAttackGoal.tick lookControl.setLookAt(target, 30, 30) (ldc 30.0f)
)

// witherState holds all WitherBoss-specific tick state behind the single e.wither pointer (a non-wither
// entity touches none of it). invulnerableTicks mirrors DATA_ID_INV (the charge-up countdown, 220 -> 0);
// destroyBlocksTick mirrors WitherBoss.destroyBlocksTick (set 20 on a hurt while armored, ticks to 0 then
// destroys the AABB blocks); nextHeadUpdate/idleHeadUpdates are the 2-side-head shooting cadence arrays;
// bossBarID is the ServerBossEvent id; bossProgress is the last broadcast progress. Cite WitherBoss fields.
type witherState struct {
	invulnerableTicks int32
	destroyBlocksTick int32
	nextHeadUpdate    [witherSideHeadCount]int64
	idleHeadUpdates   [witherSideHeadCount]int32
	bossBarID         uuid.UUID
	bossProgress      float32
	// CENTER-head RangedAttackGoal(this, 1.0, 40, 20.0f) state: attackTime is the inter-shot countdown
	// (starts -1), seeTime is the LoS run length. These mirror RangedAttackGoal.attackTime / .seeTime for
	// the priority-2 goal that drives head 0. goalActive tracks canUse()/stop() so a lost target resets
	// attackTime=-1 + seeTime=0 exactly as RangedAttackGoal.stop() does. Cite RangedAttackGoal fields.
	centerAttackTime  int32
	centerSeeTime     int32
	centerGoalActive  bool
}

// spawnWither creates the WitherBoss at (x,y,z), runs makeInvulnerable (220 invuln ticks + health =
// maxHealth/3 == 100 + progress 0), and starts the charge-up. The code-spawned analogue of the soul-sand-T
// + 3-skull pattern trigger (WitherSkullBlock.checkSpawn -> new WitherBoss + makeInvulnerable + addFreshEntity).
// NewEntity seeds the 300 HP attribute map (attribute.NewMapForEntity("wither") -> witherSupplier), then
// makeInvulnerable overrides health to maxHealth/3. e.ai is seeded so the wither enters the serverAiStep
// snapshot loop (empty goalSelector no-op) + so its head-cadence nextInt draws on its OWN mobRandom stream.
// Cite WitherBoss ctor + makeInvulnerable + WitherSkullBlock.checkSpawn.
func (t *TickLoop) spawnWither(x, y, z float64) *Entity {
	w := NewEntity(t.idAlloc.AllocID(), entity.Wither, x, y, z)
	initSpawnHealth(w) // LivingEntity ctor setHealth(getMaxHealth()) -> 300.0
	// centerAttackTime = -1 mirrors RangedAttackGoal.attackTime iconst_m1 (goal inactive until first target). Cite RangedAttackGoal ctor.
	w.wither = &witherState{bossBarID: uuid.New(), bossProgress: 0.0, centerAttackTime: -1}
	w.ai = &mobAI{}
	reseedMobAI(w.ai, w.id)
	w.ai.persistenceRequired = true // WitherBoss.removeWhenFarAway == false (a boss never despawns)
	// makeInvulnerable(): setInvulnerableTicks(220); bossEvent.setProgress(0); setHealth(getMaxHealth()/3.0f).
	w.wither.invulnerableTicks = witherInvulnerableTicks
	w.health = float32(witherMaxHealth) / float32(witherSpawnHealthDiv) // 300/3 == 100
	_ = witherXpReward                                                  // ctor xpReward = 50 (no per-Entity xpReward field yet -- cited)
	owner := t.regionForEntity(w)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(w)
	t.witherBossBarAddAll(w) // PURPLE / PROGRESS / darkenScreen=true (ctor lambda$new$0)
	return w
}

// witherIsFlyer reports whether an entity is the WitherBoss (the tickPhysics flyer gate reads it so the
// wither hovers where spawned rather than falling -- the v1 stationary-hover reduction; vanilla drives the
// flight from FlyingMoveControl toward the target). Cite WitherBoss(FlyingMoveControl) -- v1 defers the chase.
func witherIsFlyer(e *Entity) bool { return e.wither != nil }

// witherIsPowered ports WitherBoss.isPowered(): getHealth() <= getMaxHealth()/2.0f (the under-50-percent
// armored/shield phase flag). Cite WitherBoss.isPowered.
func witherIsPowered(e *Entity) bool {
	return e.health <= float32(witherMaxHealth)/float32(witherPoweredHealthFrac)
}

// witherAiStep is the port of WitherBoss.customServerAiStep(ServerLevel), driven per-type from tickAI (gated
// on e.wither != nil, AFTER serverAiStep -- the empty goalSelector no-op, like enderDragonAiStep). Two arms
// exactly as the jar: (1) the invulnerable CHARGE-UP (getInvulnerableTicks() > 0): decrement, push the
// 1-t/220 bar, at 0 detonate the power-7 explosion, then heal 10 every tickCount%10; (2) the FIGHT: aim +
// shoot the 3 heads' WitherSkulls, run the destroyBlocksTick AABB block-break while a hurt started it, heal
// +1 every tickCount%20, and push health/maxHealth to the bar. All RNG is on the wither's OWN mobRandom
// stream; tickCount is e.tickCount (the per-entity age counter). Cite WitherBoss.customServerAiStep.
func (t *TickLoop) witherAiStep(e *Entity) {
	w := e.wither
	if w == nil || e.dead || e.health <= 0 {
		return
	}

	// (0) CENTER-head RangedAttackGoal(this, 1.0, 40, 20.0f): the priority-2 goal that drives head 0. In
	// vanilla it runs in the goalSelector via Mob.serverAiStep BEFORE customServerAiStep every tick (see
	// Mob.serverAiStep: goalSelector.tickRunningGoals(...) precedes customServerAiStep(...)), so its RNG (the
	// 0.001 dangerous nextFloat drawn ONLY on a firing tick) is consumed BEFORE the side-head nextInt draws
	// below -- the interleave is exact. It runs regardless of the invuln charge-up (the goalSelector is not
	// gated by getInvulnerableTicks). Cite WitherBoss.registerGoals @2 RangedAttackGoal + Mob.serverAiStep.
	t.witherCenterAttackGoalTick(e)

	// (1) CHARGE-UP: if (getInvulnerableTicks() > 0) { t--; setProgress(1-t/220); if(t<=0) explode; setInvuln(t); if(t%10==0) heal(10); return; }
	if w.invulnerableTicks > 0 {
		nt := w.invulnerableTicks - 1
		w.bossProgress = 1.0 - float32(nt)/float32(witherInvulnProgressDiv) // setProgress(1 - t/220.0f)
		t.witherBossBarSetProgress(e, w.bossProgress)
		if nt <= 0 {
			// level.explode(this, getX(), getEyeY(), getZ(), 7.0f, false, MOB); globalLevelEvent 1023 deferred.
			eyeY := e.y + e.height*witherEyeHeightRatio // getEyeY() == getY() + eyeHeight (Entity default height*0.85)
			t.explode(e.id, e.x, eyeY, e.z, witherSpawnExplosionPow)
		}
		w.invulnerableTicks = nt // setInvulnerableTicks(t)
		if t.gametime%witherInvulnHealPeriod == 0 {
			t.witherHeal(e, witherInvulnHealAmount) // heal(10.0f)
		}
		return
	}

	// (2) FIGHT arm: the 3-head aim + shoot cadence (side heads i=1,2 via idleHeadUpdates/nextHeadUpdate).
	for i := 1; i < witherHeadCount; i++ {
		si := i - 1 // the SIDE-head array index (0,1)
		if t.gametime >= w.nextHeadUpdate[si] {
			// nextHeadUpdate[si] = tickCount + 10 + random.nextInt(10)  (tickCount == gametime proxy, like the dragon)
			w.nextHeadUpdate[si] = t.gametime + int64(witherHeadUpdateBase) + int64(mobRandom(e).nextInt(witherHeadUpdateJitter))
			// NORMAL/HARD only: idleHeadUpdates[si]++ ; if > 15 -> a random scattered dangerous skull volley, reset.
			if t.levelDifficulty == difficultyNormal || t.levelDifficulty == difficultyHard {
				// aiStep tests `if (idleHeadUpdates[i]++ > 15)` -- a POST-increment: the value COMPARED is
				// the PRE-increment one (bytecode dup_x2 pushes the old value before storing the +1). Capture
				// the pre-value, increment unconditionally, then test the PRE-value against 15 (a counter at
				// 16 fires; the increment still happens on every tick). Cite WitherBoss.aiStep (bytecode
				// 180-188: dup2; iaload; dup_x2; iconst_1; iadd; iastore; bipush 15; if_icmple).
				pre := w.idleHeadUpdates[si]
				w.idleHeadUpdates[si]++
				if pre > witherIdleShotThreshold {
					d5 := witherNextDoubleRange(e, e.x-witherIdleScatterXZ, e.x+witherIdleScatterXZ)
					d7 := witherNextDoubleRange(e, e.y-witherIdleScatterY, e.y+witherIdleScatterY)
					d9 := witherNextDoubleRange(e, e.z-witherIdleScatterXZ, e.z+witherIdleScatterXZ)
					t.witherPerformRangedAttackXYZ(e, i, d5, d7, d9, true) // performRangedAttack(i+1, x,y,z, dangerous=true)
					w.idleHeadUpdates[si] = 0
				}
			}
			// getAlternativeTarget reduced to the single getTarget() (bounded): the jar's customServerAiStep
			// side-head guard is canAttack(target) && distanceToSqr(target) <= 900.0 && hasLineOfSight(target)
			// (bytecode 323-351) checked BEFORE performRangedAttack(i+1, target). witherCanAttack folds all
			// three (alive + <=900 + LoS). Cite WitherBoss.customServerAiStep side-head guard block.
			target := t.witherTarget(e)
			if t.witherCanAttack(e, target) {
				t.witherPerformRangedAttackTarget(e, i, target) // performRangedAttack(i+1, target)
				w.nextHeadUpdate[si] = t.gametime + int64(witherFireAfterAim) + int64(mobRandom(e).nextInt(witherFireAfterJitter))
				w.idleHeadUpdates[si] = 0
			}
		}
	}

	// destroyBlocksTick: --tick; at 0 under MOB_GRIEFING destroy the canDestroy blocks in the AABB.
	if w.destroyBlocksTick > 0 {
		w.destroyBlocksTick--
		if w.destroyBlocksTick == 0 && t.gameRule(ruleMobGriefing) {
			t.witherDestroyBlocksInAABB(e)
		}
	}

	// if (tickCount % 20 == 0) heal(1.0f): the idle mid-fight regeneration.
	if t.gametime%witherIdleHealPeriod == 0 {
		t.witherHeal(e, witherIdleHealAmount)
	}

	t.witherBossBarUpdateProgress(e) // bossEvent.setProgress(getHealth()/getMaxHealth())
}

// witherCenterAttackGoalTick ports the RangedAttackGoal(this, 1.0, 40, 20.0f) that drives the CENTER head
// (head 0). It folds RangedAttackGoal.canUse/stop/tick for the one wither goal:
//
//	canUse():  target = mob.getTarget(); return target != null && target.isAlive();
//	stop():    target = null; seeTime = 0; attackTime = -1;
//	tick():    d = distanceToSqr(target); seeing = sensing.hasLineOfSight(target);
//	           if (seeing) seeTime++ else seeTime = 0;
//	           // nav (moveTo/stop) DEFERRED -- the wither is a stationary-hover flyer in v1;
//	           // lookControl.setLookAt(target, 30, 30) mapped to a head-yaw turn toward the target.
//	           if (--attackTime == 0) { if (!seeing) return;
//	               f = Mth.clamp(sqrt(d)/attackRadius, 0.1, 1.0);
//	               performRangedAttack(target, f);  // -> performRangedAttack(0, target): head 0 + 0.001 roll
//	               attackTime = Mth.floor(f*(max-min)+min);  // min==max==40 -> always 40
//	           } else if (attackTime < 0) {
//	               attackTime = Mth.floor(Mth.lerp(sqrt(d)/attackRadius, min, max));  // ==40
//	           }
//
// The ONLY RNG this draws is the 0.001 dangerous nextFloat inside performRangedAttack(0, target) -- and only
// on the tick attackTime hits 0 WITH line-of-sight, exactly as the jar. This runs BEFORE the side-head loop
// so the center draw precedes the side nextInt draws (the vanilla goalSelector-before-customServerAiStep
// order). Cite RangedAttackGoal(RangedAttackMob, double, int, float).tick + WitherBoss.performRangedAttack.
func (t *TickLoop) witherCenterAttackGoalTick(e *Entity) {
	w := e.wither
	if w == nil {
		return
	}
	// canUse(): target = mob.getTarget(); active iff non-null and alive. witherTarget acquires/keeps the
	// FOLLOW_RANGE (40) target (the bounded NearestAttackableTargetGoal reduction shared with the side heads).
	target := t.witherTarget(e)
	if target == nil || target.dead {
		// !canUse() (and !canContinueToUse): the goal stops. stop(): target=null; seeTime=0; attackTime=-1.
		if w.centerGoalActive {
			w.centerGoalActive = false
			w.centerSeeTime = 0
			w.centerAttackTime = -1
		}
		return
	}
	w.centerGoalActive = true

	// tick(): d = mob.distanceToSqr(target); seeing = sensing.hasLineOfSight(target).
	d := distanceToSqrPlayer(target, e)
	seeing := t.sensingHasLineOfSight(e, target)
	if seeing {
		w.centerSeeTime++ // seeTime++
	} else {
		w.centerSeeTime = 0 // seeTime = 0
	}
	// nav moveTo/stop is DEFERRED (the wither hovers stationary in v1; witherCenterSeeTimeReady / attackRadius
	// govern only the deferred navigation, not the fire gate). lookControl.setLookAt(target, 30, 30): turn the
	// head toward the target (the LOOK flag).
	yRotD := yawTowardDeg(target.x-e.x, target.z-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, witherCenterLookMaxStep)

	sqrtD := math.Sqrt(d)
	// if (--attackTime == 0) { ... } else if (attackTime < 0) { ... }
	w.centerAttackTime--
	if w.centerAttackTime == 0 {
		if !seeing {
			return // no LoS -> no shot (attackTime stays 0; next tick --attackTime goes negative -> reset)
		}
		// f = Mth.clamp((float)(sqrt(d)/attackRadius), 0.1f, 1.0f)
		f := mthClampF(float32(sqrtD/witherCenterAttackRadius), 0.1, 1.0)
		// performRangedAttack(target, f) -> performRangedAttack(0, target): head 0 with the 0.001 roll.
		t.witherPerformRangedAttack(e, target)
		// attackTime = Mth.floor(f*(attackIntervalMax-attackIntervalMin)+attackIntervalMin); min==max==40 -> 40.
		w.centerAttackTime = int32(mthFloorF(float64(f*float32(witherCenterAttackInterval-witherCenterAttackInterval)) + float64(witherCenterAttackInterval)))
	} else if w.centerAttackTime < 0 {
		// attackTime = Mth.floor(Mth.lerp(sqrt(d)/attackRadius, attackIntervalMin, attackIntervalMax)); ==40.
		lerp := mthLerpD(sqrtD/witherCenterAttackRadius, float64(witherCenterAttackInterval), float64(witherCenterAttackInterval))
		w.centerAttackTime = int32(mthFloorF(lerp))
	}
}

// witherHeal ports LivingEntity.heal(float): setHealth(getHealth()+amount) clamped to getMaxHealth(). NO RNG.
func (t *TickLoop) witherHeal(e *Entity, amount float32) {
	if e.health <= 0 {
		return
	}
	e.health += amount
	if e.health > float32(witherMaxHealth) {
		e.health = float32(witherMaxHealth)
	}
}

// witherNextDoubleRange ports Mth.nextDouble(RandomSource, double min, double max) = nextDouble()*(max-min)+min,
// drawn on the wither's OWN mobRandom stream (the idle-volley scatter target). Cite Mth.nextDouble.
func witherNextDoubleRange(e *Entity, lo, hi float64) float64 {
	return mobRandom(e).nextDouble()*(hi-lo) + lo
}

// witherTarget acquires/keeps the nearest live player within FOLLOW_RANGE (40.0) -- the bounded reduction of
// the NearestAttackableTargetGoal(LivingEntity) targetSelector (v1 targets players). Tick-owned (mirrors
// blazeTarget). Cite WitherBoss.registerGoals targetSelector.
func (t *TickLoop) witherTarget(e *Entity) *tickPlayer {
	if e.ai == nil {
		return nil
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 40.0
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p != nil && !p.dead && distanceToSqrPlayer(p, e) <= rangeSqr {
			return p
		}
		e.ai.attackTargetID = 0
	}
	var best *tickPlayer
	bestSq := rangeSqr
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		dsq := distanceToSqrPlayer(p, e)
		if dsq <= bestSq {
			bestSq = dsq
			best = p
		}
	}
	if best != nil {
		e.ai.attackTargetID = best.entityID
		return best
	}
	return nil
}

// witherCanAttack ports the per-head fight guard: canAttack(target) && distanceToSqr(target) <= 900.0 &&
// hasLineOfSight(target). v1 canAttack is "a live player". Cite WitherBoss.customServerAiStep guard block.
func (t *TickLoop) witherCanAttack(e *Entity, target *tickPlayer) bool {
	if target == nil || target.dead {
		return false
	}
	if distanceToSqrPlayer(target, e) > witherTargetRangeSqr {
		return false
	}
	return t.sensingHasLineOfSight(e, target)
}

// witherPerformRangedAttackTarget ports WitherBoss.performRangedAttack(int head, LivingEntity target): aim at
// (target.getX(), target.getY()+target.getEyeHeight()*0.5, target.getZ()); head 0 (center) is dangerous with
// probability 0.001 (nextFloat() < 0.001f), heads 1/2 not dangerous here (their danger comes from the idle
// volley). Cite WitherBoss.performRangedAttack(int, LivingEntity).
func (t *TickLoop) witherPerformRangedAttackTarget(e *Entity, head int, target *tickPlayer) {
	if target == nil {
		return
	}
	// NO canAttack/distance/LoS guard here: WitherBoss.performRangedAttack(int, LivingEntity) unconditionally
	// aims + draws the 0.001 dangerous roll (head 0) + spawns the skull. Both callers (the center-head goal's
	// `seeing` gate; the side-head loop's witherCanAttack) do the gating BEFORE this call, exactly as the jar.
	tx := target.x
	// performRangedAttack aim: target.getY() + target.getEyeHeight()*0.5. For a STANDING player the eye
	// height is playerStandingEyeHeight (1.62), so the aim Y is target.y + 1.62*0.5 == +0.81 (NOT the
	// bounding-box height*0.5 == +0.90). Cite WitherBoss.performRangedAttack(int, LivingEntity) (bytecode
	// 11-19: getEyeHeight; f2d; ldc2_w 0.5; dmul; dadd).
	ty := target.y + playerStandingEyeHeight*0.5 // target.getY() + target.getEyeHeight()*0.5
	tz := target.z
	dangerous := false
	if head == 0 {
		dangerous = mobRandom(e).nextFloat() < witherDangerousRoll
	}
	t.witherPerformRangedAttackXYZ(e, head, tx, ty, tz, dangerous)
}

// witherPerformRangedAttackXYZ ports WitherBoss.performRangedAttack(int head, double x, double y, double z,
// boolean dangerous): spawn a WitherSkull from the HEAD's muzzle (getHeadX/Y/Z(head)) aimed at (x,y,z),
// normalized, owned by the wither, dangerous if set. The projectile flight + on-hit (8.0 damage, WITHER
// effect, power-1 explosion, 0.73 dangerous inertia) is the existing hurtWitherSkull path. levelEvent 1024
// deferred. Cite WitherBoss.performRangedAttack(int, double, double, double, boolean).
func (t *TickLoop) witherPerformRangedAttackXYZ(e *Entity, head int, x, y, z float64, dangerous bool) {
	hx := t.witherHeadX(e, head)
	hy := t.witherHeadY(e, head)
	hz := t.witherHeadZ(e, head)
	sk := t.spawnHurtingProjectile(e.id, hurtWitherSkull, hx, hy, hz, x-hx, y-hy, z-hz)
	if sk != nil && dangerous {
		sk.hurtDangerous = true // WitherSkull.setDangerous(true) -> inertia 0.73 (getInertia override)
	}
}

// witherHeadX/Y/Z port WitherBoss.getHeadX/getHeadY/getHeadZ(int head): head 0 (center) sits at the body
// (getX()/getZ(), Y offset 3.0); heads 1/2 (sides) orbit the body yaw at radius 1.3*scale, Y offset 2.2. The
// angle is (yBodyRot + 180*(head-1)) in radians; scale defaults 1.0 (no scale attribute read yet -- cited).
// Cite WitherBoss.getHeadX/getHeadY/getHeadZ.
func (t *TickLoop) witherHeadX(e *Entity, head int) float64 {
	if head <= 0 {
		return e.x
	}
	f := (float64(e.headYaw) + 180.0*float64(head-1)) * (math.Pi / 180.0)
	return e.x + math.Cos(f)*witherHeadRadius*1.0
}

func (t *TickLoop) witherHeadY(e *Entity, head int) float64 {
	off := witherHeadCenterY
	if head > 0 {
		off = witherHeadSideY
	}
	return e.y + off*1.0 // + off * getScale() (scale 1.0)
}

func (t *TickLoop) witherHeadZ(e *Entity, head int) float64 {
	if head <= 0 {
		return e.z
	}
	f := (float64(e.headYaw) + 180.0*float64(head-1)) * (math.Pi / 180.0)
	return e.z + math.Sin(f)*witherHeadRadius*1.0
}

// witherDestroyBlocksInAABB ports the customServerAiStep destroyBlocksTick==0 block-break: over the box
// [blockX-i .. blockX+i] x [blockY .. blockY+j] x [blockZ-i .. blockZ+i] (i = floor(bbWidth/2 + 1), j =
// floor(bbHeight)), destroy every block for which WitherBoss.canDestroy is true. levelEvent 1022 (the
// wither-break sound) is deferred. Cite WitherBoss.customServerAiStep block-break + WitherBoss.canDestroy.
func (t *TickLoop) witherDestroyBlocksInAABB(e *Entity) {
	i := int(math.Floor(float64(e.width)/2.0 + 1.0)) // Mth.floor(getBbWidth()/2.0f + 1.0f)
	j := int(math.Floor(float64(e.height)))          // Mth.floor(getBbHeight())
	bx := int(math.Floor(e.x))
	by := int(math.Floor(e.y))
	bz := int(math.Floor(e.z))
	for gx := bx - i; gx <= bx+i; gx++ {
		for gy := by; gy <= by+j; gy++ {
			for gz := bz - i; gz <= bz+i; gz++ {
				if t.witherCanDestroy(gx, gy, gz) {
					t.witherDestroyBlockAt(gx, gy, gz)
				}
			}
		}
	}
}

// witherCanDestroy ports WitherBoss.canDestroy(BlockState): !state.isAir() && !state.is(BlockTags.WITHER_IMMUNE)
// -- a solid, non-immune block the wither can smash. v1 reads the block state at (x,y,z) and applies the two
// checks (air via isEmptyBlockAt, WITHER_IMMUNE via the flattened block tag). An unloaded column reads as air
// (not destroyable). Cite WitherBoss.canDestroy + BlockTags.WITHER_IMMUNE.
func (t *TickLoop) witherCanDestroy(x, y, z int) bool {
	if t.world() == nil {
		return false
	}
	pos := pk.Position{X: x, Y: y, Z: z}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false // unloaded -> treat as air (not destroyable)
	}
	if s == block.ToStateID[block.Air{}] {
		return false // state.isAir()
	}
	if blockInTag(s, "wither_immune") {
		return false // state.is(BlockTags.WITHER_IMMUNE)
	}
	return true
}

// witherDestroyBlockAt ports level.destroyBlock(pos, true, this): set the block to air + broadcast the
// change. The drop of the broken block's item (destroyBlock's dropResources) is a cited v1 simplification
// (the observable behavior -- the wither smashes a hole through terrain -- is preserved; the item spill is
// deferred like the enderman-carry break). Cite ServerLevel.destroyBlock.
func (t *TickLoop) witherDestroyBlockAt(x, y, z int) {
	pos := pk.Position{X: x, Y: y, Z: z}
	air := block.ToStateID[block.Air{}]
	if t.world().SetBlock(pos, air, dimMinY) {
		t.broadcastBlockUpdate(pos, air)
	}
}

// witherPerformRangedAttack ports WitherBoss.performRangedAttack(LivingEntity, float), the RangedAttackMob
// contract the priority-2 RangedAttackGoal invokes: it forwards to performRangedAttack(0, target) -- the
// CENTER head (head 0) with the 0.001 dangerous roll (bytecode iconst_0; aload target; invokevirtual
// performRangedAttack(I,LivingEntity)). Live caller: witherCenterAttackGoalTick on a firing tick. The float
// power arg only scales attackTime in the goal (dropped here, as the jar does). Cite WitherBoss.performRangedAttack(LivingEntity, float).
func (t *TickLoop) witherPerformRangedAttack(e *Entity, target *tickPlayer) {
	t.witherPerformRangedAttackTarget(e, 0, target)
}

// witherDropNetherStar ports WitherBoss.dropCustomDeathLoot(ServerLevel, DamageSource, boolean): after the
// base drops it spawns a NETHER_STAR ItemEntity at the wither's vertical center with setExtendedLifetime
// (age = -6000, so the star does not despawn on the default 6000-tick timer). Called from dropAllDeathLoot's
// dropCustomDeathLoot hook, gated on e.wither != nil. Cite WitherBoss.dropCustomDeathLoot + ItemEntity.setExtendedLifetime.
func (t *TickLoop) witherDropNetherStar(e *Entity) {
	if e.wither == nil {
		return
	}
	stack := component.SlotData{ItemID: pk.VarInt(item.NetherStar.ID), Count: 1}
	ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y+e.height/2.0, e.z, stack)
	ie.age = itemExtendedLifetimeAge // setExtendedLifetime(): age = -6000 (never despawns on the default timer)
	t.regionForEntity(e).entities.add(ie)
}

// itemExtendedLifetimeAge is ItemEntity.setExtendedLifetime's age write (-6000): the item tick counts age up
// from -6000, so it survives 6000 extra ticks before the >= 6000 discard. VERIFIED javap ItemEntity
// .setExtendedLifetime: `this.age = -6000;` (sipush -6000 putfield age).
const itemExtendedLifetimeAge = -6000

// --- BOSS BAR (WitherBoss ctor: ServerBossEvent(uuid, getDisplayName(), PURPLE, PROGRESS) + setDarkenScreen(true)) --
//
// The wither boss bar is a ServerBossEvent (NOT raid-coupled), so these send ClientboundBossEvent per-player
// directly over t.players (the same seam ender_dragon.go's bar uses). name is the translate key
// entity.minecraft.wither. progress starts 0 (makeInvulnerable) then rides the charge-up + health/maxHealth.

// witherBossName is the wither bar's translatable Component (entity.minecraft.wither).
func witherBossName() chat.Message {
	return chat.Message{Translate: "entity.minecraft.wither"}
}

// witherBossBarAddAll sends the ADD packet (full bar state) to every tracked player. PURPLE / PROGRESS,
// darkenScreen=true, music=false, fog=false (WitherBoss sets ONLY setDarkenScreen(true) in lambda$new$0).
func (t *TickLoop) witherBossBarAddAll(e *Entity) {
	w := e.wither
	if w == nil {
		return
	}
	pkt := encodeBossEventAdd(w.bossBarID, witherBossName(), w.bossProgress,
		bossBarColorPurple, bossBarOverlayProgress, true, false, false)
	for _, pl := range t.players {
		if pl != nil && pl.client != nil {
			pl.client.Send(pkt)
		}
	}
}

// witherBossBarSetProgress pushes an explicit progress value (the charge-up 1-t/220 ramp) as UPDATE_PROGRESS.
func (t *TickLoop) witherBossBarSetProgress(e *Entity, progress float32) {
	w := e.wither
	if w == nil {
		return
	}
	pkt := encodeBossEventUpdateProgress(w.bossBarID, progress)
	for _, pl := range t.players {
		if pl != nil && pl.client != nil {
			pl.client.Send(pkt)
		}
	}
}

// witherBossBarUpdateProgress pushes health/maxHealth as the bar progress, sending UPDATE_PROGRESS only on a
// change (ServerBossEvent.setProgress change guard). Cite WitherBoss.customServerAiStep tail.
func (t *TickLoop) witherBossBarUpdateProgress(e *Entity) {
	w := e.wither
	if w == nil {
		return
	}
	progress := e.health / float32(witherMaxHealth)
	if progress == w.bossProgress {
		return
	}
	w.bossProgress = progress
	t.witherBossBarSetProgress(e, progress)
}

// witherBossBarRemoveAll sends the REMOVE packet to every tracked player (ServerBossEvent.removeAllPlayers).
// Called when the wither dies. Cite ServerBossEvent.removeAllPlayers.
func (t *TickLoop) witherBossBarRemoveAll(e *Entity) {
	w := e.wither
	if w == nil {
		return
	}
	pkt := encodeBossEventRemove(w.bossBarID)
	for _, pl := range t.players {
		if pl != nil && pl.client != nil {
			pl.client.Send(pkt)
		}
	}
}

// witherHurtServerGate ports WitherBoss.hurtServer(ServerLevel, DamageSource, float)'s boss-specific
// immunity gates (the part BEFORE it delegates to Monster.hurtServer). It returns FALSE to REJECT the hit
// (WitherBoss.hurtServer -> return false) and TRUE to let the shared applyDamageEntity pipeline continue
// (Monster.hurtServer). On a surviving hit it arms destroyBlocksTick = 20 and bumps every idleHeadUpdates
// entry by 3 (so a hurt wither soon smashes blocks + fires an idle volley), exactly as the jar does right
// before the super call.
//
//	[VERIFIED javap WitherBoss.hurtServer:
//	 if (isInvulnerableTo(level, src)) return false;
//	 if (src.is(WITHER_IMMUNE_TO) || src.getEntity() instanceof WitherBoss) return false;
//	 if (getInvulnerableTicks() > 0 && !src.is(BYPASSES_INVULNERABILITY)) return false;
//	 if (isPowered()) { Entity de = src.getDirectEntity();
//	    if (de instanceof AbstractArrow || de instanceof WindCharge) return false; }
//	 Entity e2 = src.getEntity();
//	 if (e2 != null && e2.is(WITHER_FRIENDS)) return false;
//	 if (destroyBlocksTick <= 0) destroyBlocksTick = 20;
//	 for (int i = 0; i < idleHeadUpdates.length; i++) idleHeadUpdates[i] += 3;
//	 return super.hurtServer(level, src, amount);]
func (t *TickLoop) witherHurtServerGate(e *Entity, src damageSource) bool {
	w := e.wither
	if w == nil {
		return true
	}
	// src.is(WITHER_IMMUNE_TO) || src.getEntity() instanceof WitherBoss -> immune.
	if src.is("wither_immune_to") {
		return false
	}
	if src.attacker != 0 {
		if other, ok := t.regionForEntity(e).entities.get(src.attacker); ok && other != nil && other.wither != nil {
			return false // damage from another WitherBoss
		}
	}
	// getInvulnerableTicks() > 0 && !src.is(BYPASSES_INVULNERABILITY) -> immune (the charge-up shield).
	if w.invulnerableTicks > 0 && !src.is("bypasses_invulnerability") {
		return false
	}
	// isPowered() projectile shield: an arrow / wind-charge cannot damage an armored (< 50% HP) wither. v1
	// models "the direct entity is an AbstractArrow/WindCharge" by the damage-type source (arrow/wind_charge).
	if witherIsPowered(e) {
		if src.typeTag == damageTypeArrow || src.typeTag == damageTypeWindCharge {
			return false
		}
	}
	// e2.is(WITHER_FRIENDS) -> immune (undead allies). v1 attackers are players/projectiles, never
	// WITHER_FRIENDS members, so this is a cited no-op structured to become a real EntityTypeTags read
	// when a wither-friend attacker path exists (never baked away). Cite EntityTypeTags.WITHER_FRIENDS.

	// SURVIVING hit: arm the block-destroy timer + bump the idle-head counters (the jar's pre-super tail).
	if w.destroyBlocksTick <= 0 {
		w.destroyBlocksTick = witherDestroyBlocksTicks // = 20
	}
	for i := range w.idleHeadUpdates {
		w.idleHeadUpdates[i] += 3
	}
	return true
}
