package server

// ai_goals_attack.go — MOB-SUB-10 (Phase 35-01): the shared melee attack goal, PORTED (the STANDING
// MANDATE, idiomatic non-1:1 Go, no GPL paste) from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, read via javap -c -p this session):
//
//   - net.minecraft.world.entity.ai.goal.MeleeAttackGoal: flags {MOVE}; canUse is a gameTime-cooldown
//     gate (NO RNG); tick paths to the target (SETS a nav want — never moves the mob) + faces it +
//     checkAndPerformAttack when in melee range, which on an RNG-FREE per-attack cooldown calls
//     mob.doHurtTarget (the Phase-29 damage keystone). ZombieAttackGoal/SpiderAttackGoal are subclasses.
//
// Built ONCE here so the zombie/spider (this phase) and the wolf (Phase 36) reuse it. It canUse-gates
// on mobAI.getTarget() != 0 (the Mob.getTarget() the targetSelector's goals set, ai_goals_target.go).
//
// THE PIG ORACLE IS UNTOUCHED: a passive pig declares no attack goal (no target/melee goal), so this
// goal never ticks on it — no draw reaches the pinned pig stream.
//
//   ⚠ THE doHurtTarget VICTIM IS A PLAYER: a mob attacking a player routes through the PLAYER hurt
//   path (combat.go applyDamage), NOT applyDamageEntity (which is mob-victim). The damageSource is
//   built with attacker = mob.id (damage_source.go), so the player's own damage path applies the
//   existing armor/i-frame guards against the host-set (never plugin-forgeable) attacker id.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"

	"github.com/imhinotori/sulfur/level/attribute"
)

// meleeCooldownBetweenCanUseChecks is MeleeAttackGoal.COOLDOWN_BETWEEN_CAN_USE_CHECKS == 20L: canUse
// re-evaluates the (expensive) path build at most once per 20 gameTime ticks.
//
//	[VERIFIED javap MeleeAttackGoal.canUse: time = level.getGameTime(); if (time - lastCanUseCheck < 20L)
//	 return false; lastCanUseCheck = time; ...]
const meleeCooldownBetweenCanUseChecks = 20

// meleeAttackResetCooldown is resetAttackCooldown()'s ticksUntilNextAttack value: adjustedTickDelay(20).
// adjustedTickDelay is IDENTITY in our full-rate-tick driver (the same rule reducedTickDelay/the
// NearestAttackableTargetGoal gate follow), so the faithful value is the raw 20 — a swing at most once
// per 20 ticks (~1s @20TPS). RNG-FREE countdown.
//
//	[VERIFIED javap MeleeAttackGoal.resetAttackCooldown: ticksUntilNextAttack = adjustedTickDelay(20).]
const meleeAttackResetCooldown = 20

// meleeLookMaxYawStep is MeleeAttackGoal.tick's setLookAt(target, 30, 30) yaw cap: the HEAD turns at
// most 30°/tick toward the target (the LookControl clamp). The body yaw is separate (MoveControl/nav).
//
//	[VERIFIED javap/CFR MeleeAttackGoal.tick: getLookControl().setLookAt(target, 30.0f, 30.0f).]
const meleeLookMaxYawStep float32 = 30.0

// meleeChaseSpeedModifier is the ZombieAttackGoal/SpiderAttackGoal speedModifier (1.0): the MoveControl
// setSpeed is speedModifier × MOVEMENT_SPEED, and both hostiles pass 1.0.
//
//	[VERIFIED javap ZombieAttackGoal.<init>(zombie, 1.0, false); Spider$SpiderAttackGoal super(spider, 1.0, true).]
const meleeChaseSpeedModifier = 1.0

// spiderDaylightFleeChance is Spider$SpiderAttackGoal.canContinueToUse's daylight-flee bound: when the
// spider is in bright light it drops its target with probability 1-in-100 PER TICK (nextInt(100)==0).
//
//	[VERIFIED javap Spider$SpiderAttackGoal.canContinueToUse: bipush 100; nextInt(100); ifne ... .]
const spiderDaylightFleeChance = 100

// defaultAttackReach is net.minecraft.world.entity.Mob.DEFAULT_ATTACK_REACH, computed in Mob's static
// init as Math.sqrt(2.0399999618530273) - 0.6000000238418579 (the exact float-widened doubles the jar
// emits). isWithinMeleeAttackRange inflates the attacker's bounding box by this reach and tests AABB
// intersection with the target's hitbox; v1 reproduces that as an inflated-AABB / hitbox overlap test
// (no held weapon -> the DEFAULT_ATTACK_REACH, min-range 0).
//
//	[VERIFIED javap Mob static {}: ldc2_w 2.0399999618530273d; Math.sqrt; ldc2_w 0.6000000238418579d;
//	 dsub; putstatic DEFAULT_ATTACK_REACH. Mob.isWithinMeleeAttackRange: getAttackBoundingBox(reach)
//	 .intersects(target.getHitbox()).]
var defaultAttackReach = math.Sqrt(2.0399999618530273) - 0.6000000238418579

// meleeAttackGoal ports net.minecraft.world.entity.ai.goal.MeleeAttackGoal (flag {MOVE}). It carries
// the gameTime-cooldown gate + the RNG-free per-attack countdown — NO RNG in canUse.
type meleeAttackGoal struct {
	baseGoal
	speedModifier float64 // MeleeAttackGoal.speedModifier — the navigateTowards(target) move speed

	lastCanUseCheck      int64 // MeleeAttackGoal.lastCanUseCheck — the gameTime of the last canUse eval
	ticksUntilNextAttack int   // MeleeAttackGoal.ticksUntilNextAttack — RNG-free swing countdown

	// ticksUntilNextPathRecalculation / pathedTargetX|Y|Z port MeleeAttackGoal's path-recompute THROTTLE
	// (the jar fields of the same names). tick() recomputes the nav want ONLY when the countdown hits 0
	// AND the target moved ≥1 block from the last pathed position (or a 1/20 nextFloat() nudge) — without
	// this the want is re-set every tick the player crosses a block boundary, which oscillates the async
	// path and reads as the mob "jittering / trying to go back". pathedTarget* default 0 (the vanilla
	// "no pathed target yet" sentinel that forces the first recompute).
	//	[VERIFIED javap/CFR MeleeAttackGoal.tick: ticksUntilNextPathRecalculation = max(-1,0); recompute
	//	 when <=0 && (pathedTarget all 0 || target.distanceToSqr(pathedTarget) >= 1.0 || nextFloat()<0.05);
	//	 on recompute pathedTarget = target.pos, cooldown = 4 + nextInt(7) (+10 if d²>1024, +5 if >256,
	//	 +15 if moveTo fails), adjustedTickDelay (identity here).]
	ticksUntilNextPathRecalculation             int
	pathedTargetX, pathedTargetY, pathedTargetZ float64

	// daylightGated is the Spider$SpiderAttackGoal delta: a spider in BRIGHT light drops its target
	// 1-in-100 per tick (canContinueToUse's daylight-flee). When set, canContinueToUse runs the
	// stochastic flee draw (nextInt(100)) gated on the day/night proxy (isBright == !isDarkEnoughToSpawn,
	// 35-02). v1 has no light engine; the gate is the proxy. ZombieAttackGoal leaves this false (plain
	// melee, NO daylight draw). Cite Spider$SpiderAttackGoal.canContinueToUse.
	daylightGated bool
}

// newMeleeAttackGoal builds the plain melee goal (ZombieAttackGoal == MeleeAttackGoal + a client
// aggressive flag, behaviorally identical — the aggressive metadata bit is a cited deferral). The
// MOVE flag matches MeleeAttackGoal's setFlags(EnumSet.of(MOVE)).
//
//	[VERIFIED javap MeleeAttackGoal.<init>: setFlags(EnumSet.of(Goal$Flag.MOVE)); ZombieAttackGoal is
//	 MeleeAttackGoal(zombie, speed, followingTargetEvenIfNotSeen=false) + the raiseArm/aggressive flag.]
func newMeleeAttackGoal(speed float64) *meleeAttackGoal {
	return &meleeAttackGoal{baseGoal: newBaseGoal(flagMove), speedModifier: speed}
}

// newSpiderAttackGoal builds the Spider$SpiderAttackGoal delta: a MeleeAttackGoal whose
// canContinueToUse drops the target 1-in-100 per tick in bright light (the "spiders calm in daylight"
// flee). The jar ctor is super(spider, 1.0, true) — speedModifier 1.0, followingTargetEvenIfNotSeen
// true (a cited no-op in v1, no LoS/sensing). Behaviorally the base melee + the daylight-flee draw.
//
//	[VERIFIED javap Spider$SpiderAttackGoal.<init>: dconst_1; iconst_1; invokespecial
//	 MeleeAttackGoal.<init>(PathfinderMob, double, boolean).]
func newSpiderAttackGoal(speed float64) *meleeAttackGoal {
	g := newMeleeAttackGoal(speed)
	g.daylightGated = true
	return g
}

// canUse ports MeleeAttackGoal.canUse (bytecode-verified this session), NO RNG:
//
//	long time = mob.level().getGameTime();
//	if (time - lastCanUseCheck < 20L) return false;     // the 20-tick re-check gate
//	lastCanUseCheck = time;
//	LivingEntity target = mob.getTarget();
//	if (target == null) return false;
//	if (!target.isAlive()) return false;
//	this.path = navigation.createPath(target, 0);
//	if (this.path != null) return true;                 // a path exists -> chase
//	return mob.isWithinMeleeAttackRange(target);        // else only if already in reach
//
// v1: the target is a player id (mobAI.getTarget()); "alive" == the player is present on the loop;
// the createPath(target, 0) half is the "a path to the target is buildable" check — v1's nav is the
// async setWantTarget path, and a reachable target is approximated by the player being present + (for
// the in-reach fast path) isWithinMeleeAttackRange. Returns true when the mob can pursue or is already
// in reach. The 20-tick lastCanUseCheck gate is the load-bearing RNG-FREE cadence (the focused test
// pins it).
//
//	[VERIFIED javap MeleeAttackGoal.canUse: getGameTime; (time - lastCanUseCheck) < 20L -> false;
//	 lastCanUseCheck = time; getTarget ifnull -> false; isAlive ifeq -> false; createPath != null ->
//	 true; else isWithinMeleeAttackRange.]
func (g *meleeAttackGoal) canUse(t *TickLoop, e *Entity) bool {
	time := int64(t.gametime)
	if time-g.lastCanUseCheck < meleeCooldownBetweenCanUseChecks {
		return false
	}
	g.lastCanUseCheck = time
	// NOTE: SpiderAttackGoal.canUse is super.canUse() && !isVehicle() — it carries NO daylight check
	// (the daylight gate lives in canContinueToUse as the stochastic 1/100 target-drop, below). v1 has
	// no vehicle/passenger subsystem, so !isVehicle() is a cited no-op (always true) — the spider is
	// never a passenger. The wave-1 canUse daylight block was NOT faithful to the decompile and is
	// removed (Spider$SpiderAttackGoal.canUse: invokespecial MeleeAttackGoal.canUse; isVehicle ifne).
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	target := t.playerByEntityID(id)
	if target == nil || target.dead { // target == null || !isAlive()
		return false
	}
	// createPath(target, 0) != null -> chase; else only if already in melee reach. v1 approximates the
	// path-buildable check as "the target is present" (the async nav will path toward it), with the
	// isWithinMeleeAttackRange fast path also accepted.
	return true
}

// canContinueToUse ports MeleeAttackGoal.canContinueToUse + the Spider$SpiderAttackGoal daylight
// delta. The base MeleeAttackGoal: keep attacking while the target is a present, valid combat target
// (getTarget()!=null && canAttackTarget). For a daylight-gated spider, FIRST the daylight-flee gate
// runs (Spider$SpiderAttackGoal.canContinueToUse):
//
//	float br = mob.getLightLevelDependentMagicValue();
//	if (br >= 0.5f && mob.getRandom().nextInt(100) == 0) { mob.setTarget(null); return false; }  // RNG
//	return super.canContinueToUse();
//
// A spider in BRIGHT light (br >= 0.5 — daytime) drops its target 1-in-100 per tick (the "spiders
// calm in daylight" behavior). The nextInt(100) is drawn ONLY when bright; at night the branch is
// skipped (no draw). The brightness read is the day/night proxy (isBright == !isDarkEnoughToSpawn).
// NO RNG for a plain (non-daylight-gated) melee.
//
//	[VERIFIED javap MeleeAttackGoal.canContinueToUse: target = getTarget(); if null false; ... ;
//	 Spider$SpiderAttackGoal.canContinueToUse: getLightLevelDependentMagicValue; >=0.5f &&
//	 nextInt(100)==0 -> setTarget(null); iconst_0 ireturn; else super.canContinueToUse.]
func (g *meleeAttackGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if g.daylightGated && g.isBright(t) {
		// DRAW (daylight-flee, ONLY when bright): getRandom().nextInt(100). On 0 -> drop the target.
		if mobRandom(e).nextInt(spiderDaylightFleeChance) == 0 {
			if e.ai != nil {
				e.ai.setTarget(0) // mob.setTarget(null)
			}
			return false
		}
	}
	// super.canContinueToUse(): the target still resolves on the loop (a present, valid combat target).
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	return t.playerByEntityID(id) != nil
}

// tick ports MeleeAttackGoal.tick: face the target (setLookAt), SET the nav want toward it
// (navigation.moveTo(target, speed) — the goal SETS a want, the async nav steps the mob; the goal
// NEVER calls moveEntity), decrement ticksUntilNextAttack, and checkAndPerformAttack. The jar's
// tick() ALSO draws RNG in its path-recalculation branch (nextFloat() < 0.05 + 4 + nextInt(7)) — that
// fires ONLY for a RUNNING attack goal with a live target, never on the pig (no melee goal), so it
// never reaches the pinned pig stream. v1's nav is the async setWantTarget path (no per-tick path
// object), so the path-recalc RNG branch is a CITED deferral (recorded, not silently dropped): it
// applies when the per-tick Path navigation lands. The 20-tick lastCanUseCheck gate (canUse) + the
// ticksUntilNextAttack countdown are the RNG-free cadence v1 ships.
//
//	[VERIFIED javap MeleeAttackGoal.tick: getLookControl().setLookAt(target, 30, 30);
//	 ticksUntilNextPathRecalculation = max(.. -1, 0); (recalc branch w/ nextFloat()<0.05 + 4+nextInt(7));
//	 navigation.moveTo(target, speedModifier); ticksUntilNextAttack = max(ticksUntilNextAttack-1, 0);
//	 checkAndPerformAttack(target).]
func (g *meleeAttackGoal) tick(t *TickLoop, e *Entity) {
	id := mobTarget(e)
	if id == 0 || e.ai == nil {
		return
	}
	target := t.playerByEntityID(id)
	if target == nil {
		return
	}
	// setLookAt(target, 30, 30): turn the HEAD toward the target at most 30°/tick — the LookControl seam.
	// This is the HEAD only (headYaw), NOT the body yaw: vanilla's MeleeAttackGoal.tick calls
	// getLookControl().setLookAt(target, 30, 30), and the BODY yaw is owned by the MoveControl (our
	// navigation.tick, which rotlerps it toward the path heading). Setting the body yaw here too made two
	// controllers fight over it and snap the mob around; leave the body to the nav so the walk stays smooth.
	yRotD := yawTowardDeg(target.x-e.x, target.z-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, meleeLookMaxYawStep)

	// ticksUntilNextPathRecalculation = max(.. - 1, 0): decrement the path-recompute throttle each tick.
	g.ticksUntilNextPathRecalculation = max(g.ticksUntilNextPathRecalculation-1, 0)

	// Recompute the nav want ONLY when the throttle elapsed AND the target has meaningfully moved (or the
	// 1/20 nextFloat() nudge fires). The followingTargetEvenIfNotSeen/hasLineOfSight guard is a v1 no-op
	// (no sensing — every target is "seen"). Without this throttle the want re-set every tick the player
	// crosses a block boundary, oscillating the async path (the "jitter / tries to go back" the user saw).
	//	[VERIFIED CFR MeleeAttackGoal.tick: if((following || hasLineOfSight) && ticksUntilNextPathRecalc<=0
	//	 && (pathedTarget all 0 || target.distanceToSqr(pathedTarget)>=1.0 || nextFloat()<0.05)) { ... }.]
	if g.ticksUntilNextPathRecalculation <= 0 {
		noPathedTarget := g.pathedTargetX == 0 && g.pathedTargetY == 0 && g.pathedTargetZ == 0
		dpx := target.x - g.pathedTargetX
		dpy := target.y - g.pathedTargetY
		dpz := target.z - g.pathedTargetZ
		movedSqr := dpx*dpx + dpy*dpy + dpz*dpz
		movedFar := movedSqr >= 1.0
		// Re-engage when the nav has NO active path (our async nav CLEARS the path on arrival, unlike
		// vanilla which keeps the previous path alive and steps it through the cooldown) AND either:
		//   (a) the mob is not yet in attack reach — it must keep closing the gap; OR
		//   (b) the target has moved AT ALL since the last path (movedSqr > 0) — a player strafing/edging
		//       AROUND the mob at close range moves <1 block per recompute window, so movedFar (≥1 block)
		//       misses it; without (b) the mob parks BESIDE the moving player until the 5% nudge fires
		//       (the "se queda parado al lado" stall). A target that is in reach AND perfectly still keeps
		//       neither branch true, so the mob correctly stands and swings instead of jittering in place.
		stalled := !e.ai.navigation.active() && (!isWithinMeleeAttackRange(e, target) || movedSqr > 1e-6)
		if noPathedTarget || movedFar || stalled || mobRandom(e).nextFloat() < 0.05 {
			g.pathedTargetX = target.x
			g.pathedTargetY = target.y
			g.pathedTargetZ = target.z
			// ticksUntilNextPathRecalculation = 4 + nextInt(7), +10 if dist²>1024, +5 if >256
			// (adjustedTickDelay is identity in the full-rate driver — the moveTo-failed +15 is a cited
			// no-op: the async setWantTarget never "fails" synchronously like navigation.moveTo).
			g.ticksUntilNextPathRecalculation = 4 + mobRandom(e).nextInt(7)
			ddx := target.x - e.x
			ddy := target.y - e.y
			ddz := target.z - e.z
			distSqr := ddx*ddx + ddy*ddy + ddz*ddz
			if distSqr > 1024.0 {
				g.ticksUntilNextPathRecalculation += 10
			} else if distSqr > 256.0 {
				g.ticksUntilNextPathRecalculation += 5
			}

			// navigation.moveTo(target, speedModifier): SET the nav want toward the target AT the chase
			// SPEED — the MoveControl.setSpeed value = speedModifier × MOVEMENT_SPEED, which the ported
			// travel-physics in navigation.tick accelerates the mob by (getSpeed input, NOT a blocks/tick
			// step). ZombieAttackGoal/SpiderAttackGoal use speedModifier 1.0, so getSpeed = MOVEMENT_SPEED
			// (a zombie 0.23 → terminal ≈ 0.277 b/tick). Pass the attribute directly.
			//	[VERIFIED javap ZombieAttackGoal.<init>(zombie, 1.0, false) / Spider$SpiderAttackGoal(super, 1.0, true);
			//	 MoveControl.setSpeed(speedModifier × getAttributeValue(MOVEMENT_SPEED)).]
			getSpeed := e.getAttributeValue(attribute.MovementSpeed) * meleeChaseSpeedModifier
			e.ai.setWantTargetSpeed(target.x, target.y, target.z, getSpeed)
		}
	}

	// ticksUntilNextAttack = max(ticksUntilNextAttack - 1, 0): the RNG-FREE per-attack countdown.
	if g.ticksUntilNextAttack > 0 {
		g.ticksUntilNextAttack--
	}
	g.checkAndPerformAttack(t, e, target)
}

// checkAndPerformAttack ports MeleeAttackGoal.checkAndPerformAttack: if canPerformAttack (the swing
// cooldown elapsed AND the target is in melee reach), resetAttackCooldown() + mob.swing(MAIN_HAND) +
// mob.doHurtTarget(level, target). NO RNG.
//
//	[VERIFIED javap MeleeAttackGoal.checkAndPerformAttack: canPerformAttack ifeq return;
//	 resetAttackCooldown; mob.swing(MAIN_HAND); mob.doHurtTarget(getServerLevel(mob), target).]
func (g *meleeAttackGoal) checkAndPerformAttack(t *TickLoop, e *Entity, target *tickPlayer) {
	if !g.canPerformAttack(e, target) {
		return
	}
	g.resetAttackCooldown()
	t.broadcastMobSwing(e) // mob.swing(MAIN_HAND) — the arm-swing animation to trackers
	// Ravager.doHurtTarget override (RAIDER Task): a ravager sets attackTick=10 + broadcasts event 4
	// (the swing-immobilize) BEFORE super.doHurtTarget deals the damage. Ravager-gated (zero cost for
	// every other mob). Cite Ravager.doHurtTarget.
	if e.typ == entity.Ravager.ID {
		t.ravagerDidHurt(e)
	}
	g.doHurtTarget(t, e, target)
}

// canPerformAttack ports MeleeAttackGoal.canPerformAttack: isTimeToAttack() (ticksUntilNextAttack <= 0)
// && isWithinMeleeAttackRange(target) && getSensing().hasLineOfSight(target). The LoS half is a cited
// no-op in v1 (no sensing/LoS subsystem — every in-reach target is visible), leaving the cooldown +
// reach gate. NO RNG.
//
//	[VERIFIED javap MeleeAttackGoal.canPerformAttack: isTimeToAttack ifeq false;
//	 isWithinMeleeAttackRange ifeq false; getSensing().hasLineOfSight ifeq false; true.]
func (g *meleeAttackGoal) canPerformAttack(e *Entity, target *tickPlayer) bool {
	if g.ticksUntilNextAttack > 0 { // !isTimeToAttack()
		return false
	}
	return isWithinMeleeAttackRange(e, target)
}

// resetAttackCooldown ports MeleeAttackGoal.resetAttackCooldown: ticksUntilNextAttack =
// adjustedTickDelay(20) == 20 (identity in our full-rate driver). RNG-FREE.
func (g *meleeAttackGoal) resetAttackCooldown() {
	g.ticksUntilNextAttack = meleeAttackResetCooldown
}

// doHurtTarget ports the limb of Mob.doHurtTarget the v1 path needs: deal ATTACK_DAMAGE to the target
// through the player hurt path. The jar:
//
//	float f = (float) getAttributeValue(ATTACK_DAMAGE);
//	... weapon/enchant bonuses (v1: no weapon -> none) ...
//	DamageSource src = getWeaponItem().getDamageSource(this);   // no-weapon -> mob_attack(this)
//	target.hurtServer(level, src, f);
//	... knockback on success (the player path's own knockback) ...
//
// The VICTIM is a PLAYER, so this routes through applyDamage (combat.go) — NOT applyDamageEntity
// (mob-victim). The damageSource carries attacker = e.id (host-set, never plugin-forgeable — T-35-02);
// for a no-weapon mob the source type is mob_attack (DamageSources.mobAttack(this)). The player's
// applyDamage applies the existing armor/i-frame/absorption guards. NO RNG (the knockback RNG guard is
// inside the player path on a degenerate-direction hit, not here). Cite Mob.doHurtTarget + the Phase-29
// damage path.
//
//	[VERIFIED javap Mob.doHurtTarget: f = getAttributeValue(ATTACK_DAMAGE) d2f; getWeaponItem
//	 .getDamageSource(this); target.hurtServer(level, src, f); on success getKnockback/causeExtraKnockback.]
func (g *meleeAttackGoal) doHurtTarget(t *TickLoop, e *Entity, target *tickPlayer) {
	dmg := float32(e.getAttributeValue(attribute.AttackDamage)) // (float) getAttributeValue(ATTACK_DAMAGE)
	// getWeaponItem().getDamageSource(this): a no-weapon mob's weapon item is empty, whose getDamageSource
	// is the generic mob attack source DamageSources.mobAttack(this) — carrying attacker = the mob id.
	src := damageSourceMobAttack(e.id)
	t.applyDamage(target, src, dmg) // the PLAYER hurt path (victim is a player)
}

// isBright is the SpiderAttackGoal daylight gate's day/night proxy: a spider in BRIGHT light (vanilla
// getLightLevelDependentMagicValue() >= 0.5f) runs the 1/100 daylight-flee draw. The real Spider gate
// reads getLightLevelDependentMagicValue (a sky/block-light read) — no light engine exists in v1, so
// this is the SAME gametime-darkness proxy the hostile spawn rule uses (35-02's isDarkEnoughToSpawn).
// "Bright" == NOT dark-enough-to-spawn == daytime (gametime % 24000 outside the [13000,23000) night
// window). At night the daylight branch never runs (no flee draw, the target is retained); in daylight
// the 1/100 drop fires. The gate is structured to read the real getLightLevelDependentMagicValue >= 0.5
// when the lighting engine lands. Cite Spider$SpiderAttackGoal.canContinueToUse +
// Monster.isDarkEnoughToSpawn (the proxy).
//
// DEFERRED (recorded + in the SUMMARY): the real getLightLevelDependentMagicValue (a continuous
// light-derived float, the interpolated sky+block brightness) collapses here to a binary day/night
// proxy — the same FORCED decision the spawn gate made (35-02). It becomes a real light read with the
// lighting engine, off no mob's lockstep stream (the flee draw stays on the spider's per-entity rng).
func (g *meleeAttackGoal) isBright(t *TickLoop) bool {
	// "Bright" (daytime) is the inverse of the night-window dark proxy. isDarkEnoughToSpawn() is true
	// during the [13000,23000) night window; bright == its negation (daytime).
	return !t.isDarkEnoughToSpawn()
}

// mobTarget reads the mob's current attack-target id (the Mob.getTarget() analogue), nil-guarding the
// AI (a mob built without AI has no target). Tick-owned read.
func mobTarget(e *Entity) int32 {
	if e == nil || e.ai == nil {
		return 0
	}
	return e.ai.getTarget()
}

// isWithinMeleeAttackRange ports Mob.isWithinMeleeAttackRange(LivingEntity) for a player victim: the
// attacker's bounding box, inflated by DEFAULT_ATTACK_REACH horizontally (and reach/2 vertically),
// must intersect the target's hitbox. The jar builds getAttackBoundingBox(reach) =
// boundingBox.inflate(reach, reach/2, reach) and tests AABB.intersects(target.getHitbox()); v1 builds
// the same inflated box from the mob's width/height AABB and the player's collision box
// (playerWidth × playerHeight, feet at p.y). No held weapon -> the DEFAULT_ATTACK_REACH, min-range 0
// (the min-range second-box check is skipped for reach-min 0). NO RNG.
//
//	[VERIFIED javap Mob.isWithinMeleeAttackRange / getAttackBoundingBox: reach = DEFAULT_ATTACK_REACH;
//	 getBoundingBox().inflate(reach, reach/2, reach).intersects(target.getHitbox()); min-range 0 ->
//	 single-box test.]
func isWithinMeleeAttackRange(e *Entity, target *tickPlayer) bool {
	reach := defaultAttackReach
	// The attacker's inflated attack box (getAttackBoundingBox(reach) = boundingBox.inflate(reach,
	// reach/2, reach)): horizontal half-width = mob.width/2 + reach, vertical = [y - reach/2, y +
	// height + reach/2].
	hw := e.width/2 + reach
	aMinX, aMaxX := e.x-hw, e.x+hw
	aMinZ, aMaxZ := e.z-hw, e.z+hw
	aMinY, aMaxY := e.y-reach/2, e.y+e.height+reach/2

	// The target's hitbox (the player's collision AABB, feet at p.y): playerWidth × playerHeight.
	phw := playerWidth / 2
	tMinX, tMaxX := target.x-phw, target.x+phw
	tMinZ, tMaxZ := target.z-phw, target.z+phw
	tMinY, tMaxY := target.y, target.y+playerHeight

	// AABB.intersects: overlap on all three axes.
	return aMinX <= tMaxX && aMaxX >= tMinX &&
		aMinY <= tMaxY && aMaxY >= tMinY &&
		aMinZ <= tMaxZ && aMaxZ >= tMinZ
}

// --- leapAtTargetGoal ---------------------------------------------------------------------------

// leapAtTargetGoal ports net.minecraft.world.entity.ai.goal.LeapAtTargetGoal (the Spider @3). It is
// the ONE goal that IMPULSES — it sets a velocity delta toward the target (a pounce), it does NOT set
// a nav want / path. canUse is RNG-gated (a 1-in-leapReducedInterval roll, drawn ONLY after the
// distance-band + on-ground guards pass), and start() applies the impulse.
//
//	⚠ THE GATE IS nextInt, NOT nextFloat (the 35-JARNOTES pre-decompile guess said "nextFloat" — the
//	exec-time decompile this session CORRECTS that to nextInt(reducedTickDelay(5))). The 1:1-with-the-
//	jar mandate is absolute, so this ports the REAL bytecode: getRandom().nextInt(reducedTickDelay(5)).
//
// THE PIG ORACLE IS UNTOUCHED: a passive pig declares no leap goal, so this never ticks on it — no
// draw reaches the pinned pig stream.
type leapAtTargetGoal struct {
	baseGoal
	yd     float64 // LeapAtTargetGoal.yd — the vertical leap component (Spider: 0.4)
	target int32   // LeapAtTargetGoal.target — captured in canUse, used by start()'s impulse
}

// leapReducedInterval is the FAITHFUL Go RNG-gate bound for LeapAtTargetGoal.canUse: the FULL value
// (5), NOT the jar's reducedTickDelay(5) == Mth.positiveCeilDiv(5,2) == 3. The jar halves it to
// compensate for vanilla evaluating goals every-OTHER server tick (the Mob.serverAiStep (tickCount+id)
// %2 decimation); our full-rate serverAiStep does not decimate, so the raw 5 is the 1:1-faithful value
// run every tick — EXACTLY the same identity rule nearestTargetRandomInterval (10, not 5) follows.
//
//	[VERIFIED javap LeapAtTargetGoal.canUse: ... iconst_5; invokestatic reducedTickDelay; nextInt; ifeq.]
const leapReducedInterval = 5

// leapMinDistSqr / leapMaxDistSqr are the LeapAtTargetGoal.canUse distance band: the mob leaps only
// when 4.0 <= distanceToSqr(target) <= 16.0 (too close -> no leap, too far -> no leap).
//
//	[VERIFIED javap LeapAtTargetGoal.canUse: distanceToSqr; ldc2_w 4.0d dcmpg iflt; ldc2_w 16.0d dcmpl ifle.]
const (
	leapMinDistSqr = 4.0
	leapMaxDistSqr = 16.0
)

// leapHorizontalScale / leapDeltaCarry are the start() impulse vector scalars: the normalized
// horizontal direction is scaled by 0.4 and ADDED to 0.2× the mob's existing delta movement.
//
//	[VERIFIED javap LeapAtTargetGoal.start: v.normalize().scale(0.4d).add(delta.scale(0.2d)).]
const (
	leapHorizontalScale = 0.4
	leapDeltaCarry      = 0.2
)

// leapLengthSqrEpsilon is the LeapAtTargetGoal.start() guard: the horizontal direction is normalized
// only when its lengthSqr exceeds 1.0E-7 (else the impulse keeps the zero horizontal vector, applying
// just the vertical yd) — guards a divide-by-zero on a degenerate (mob == target x/z) direction.
//
//	[VERIFIED javap LeapAtTargetGoal.start: v.lengthSqr(); ldc2_w 1.0E-7d; dcmpl; ifle (skip normalize).]
const leapLengthSqrEpsilon = 1.0e-7

// newLeapAtTargetGoal builds the leap goal with the JUMP+MOVE flags (LeapAtTargetGoal ctor:
// setFlags(EnumSet.of(JUMP, MOVE))) and the vertical leap component yd.
//
//	[VERIFIED javap LeapAtTargetGoal.<init>: putfield yd; EnumSet.of(JUMP, MOVE); setFlags.]
func newLeapAtTargetGoal(yd float64) *leapAtTargetGoal {
	return &leapAtTargetGoal{baseGoal: newBaseGoal(flagJump | flagMove), yd: yd}
}

// canUse ports LeapAtTargetGoal.canUse (bytecode-verified this session):
//
//	if (mob.hasControllingPassenger()) return false;            // v1: no passenger subsystem -> no-op
//	this.target = mob.getTarget();
//	if (this.target == null) return false;
//	double d = mob.distanceToSqr(target);
//	if (d < 4.0 || d > 16.0) return false;                      // the leap distance band
//	if (!mob.onGround()) return false;                          // must be grounded to leap
//	return mob.getRandom().nextInt(reducedTickDelay(5)) == 0;   // RNG GATE (the raw 5, our full-rate value)
//
// The RNG gate is the LAST check — it draws EXACTLY ONE nextInt(5), and ONLY when the passenger +
// target + distance-band + on-ground guards all pass (an out-of-band / airborne reject draws ZERO RNG).
// v1's target is a player id (mobAI.getTarget()); distanceToSqr is the squared mob->player distance.
//
//	[VERIFIED javap LeapAtTargetGoal.canUse: hasControllingPassenger ifne -> 0; getTarget putfield;
//	 ifnull -> 0; distanceToSqr; <4.0 || >16.0 -> 0; onGround ifeq -> 0; nextInt(reducedTickDelay(5))
//	 ifeq -> 1 else 0.]
func (g *leapAtTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	// hasControllingPassenger(): v1 has no passenger/vehicle subsystem — a cited no-op (always false,
	// the spider is never ridden). The guard is structured to read a real passenger when one lands.
	if e.ai == nil {
		return false
	}
	g.target = e.ai.getTarget()
	if g.target == 0 { // getTarget() == null
		return false
	}
	p := t.playerByEntityID(g.target)
	if p == nil {
		return false
	}
	// distanceToSqr(target): the squared mob->player distance (the band guard, BEFORE the RNG draw).
	dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
	d := dx*dx + dy*dy + dz*dz
	if d < leapMinDistSqr || d > leapMaxDistSqr {
		return false
	}
	if !e.onGround { // !mob.onGround() -> false (must be grounded)
		return false
	}
	// DRAW (the gate, LAST): getRandom().nextInt(reducedTickDelay(5)). reducedTickDelay(5)==3 in the
	// jar, but our full-rate tick uses the raw 5 (the leapReducedInterval identity, see its doc). The
	// leap fires iff the roll == 0.
	return mobRandom(e).nextInt(leapReducedInterval) == 0
}

// canContinueToUse ports LeapAtTargetGoal.canContinueToUse: !mob.onGround() — the leap continues only
// while the mob is airborne (the pounce arc); once it lands the goal stops. NO RNG.
//
//	[VERIFIED javap LeapAtTargetGoal.canContinueToUse: onGround ifne -> iconst_0 else iconst_1.]
func (g *leapAtTargetGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return !e.onGround
}

// start ports LeapAtTargetGoal.start — the impulse (the ONE goal that sets a velocity delta, NOT a
// nav want):
//
//	Vec3 delta = mob.getDeltaMovement();
//	Vec3 v = new Vec3(target.getX() - mob.getX(), 0.0, target.getZ() - mob.getZ());
//	if (v.lengthSqr() > 1.0E-7) v = v.normalize().scale(0.4).add(delta.scale(0.2));
//	mob.setDeltaMovement(v.x, this.yd, v.z);
//
// The horizontal direction toward the target (y zeroed) is normalized + scaled by 0.4 and the mob's
// existing horizontal delta (×0.2) is carried; the vertical component is the fixed yd. setDeltaMovement
// is the e.vx/vy/vz seam (the SAME seam knockbackEntity/set_velocity uses, combat_mob.go) — NOT
// setWantTarget (the leap impulses, the navigation does not path it). NO RNG.
//
//	[VERIFIED javap LeapAtTargetGoal.start: getDeltaMovement; new Vec3(tx-x, 0, tz-z); lengthSqr >1e-7
//	 -> normalize.scale(0.4).add(delta.scale(0.2)); setDeltaMovement(v.x, yd, v.z).]
func (g *leapAtTargetGoal) start(t *TickLoop, e *Entity) {
	p := t.playerByEntityID(g.target)
	if p == nil {
		return
	}
	// delta = getDeltaMovement() (the mob's current velocity).
	deltaX, deltaZ := e.vx, e.vz
	// v = (tx - x, 0, tz - z): the horizontal direction toward the target.
	vx := p.x - e.x
	vz := p.z - e.z
	if vx*vx+vz*vz > leapLengthSqrEpsilon {
		// v = v.normalize().scale(0.4).add(delta.scale(0.2)).
		length := math.Sqrt(vx*vx + vz*vz)
		vx = vx/length*leapHorizontalScale + deltaX*leapDeltaCarry
		vz = vz/length*leapHorizontalScale + deltaZ*leapDeltaCarry
	}
	// setDeltaMovement(v.x, yd, v.z): the impulse — vertical = yd, horizontal = the scaled direction.
	// This IMPULSES (sets velocity); it does NOT setWantTarget (the leap is the one goal exception).
	e.vx, e.vy, e.vz = vx, g.yd, vz
}

// Compile-time assertion: leapAtTargetGoal IS a server.Goal.
var _ Goal = (*leapAtTargetGoal)(nil)
