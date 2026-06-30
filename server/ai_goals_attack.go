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

	lastCanUseCheck     int64 // MeleeAttackGoal.lastCanUseCheck — the gameTime of the last canUse eval
	ticksUntilNextAttack int  // MeleeAttackGoal.ticksUntilNextAttack — RNG-free swing countdown

	// daylightGated is the SpiderAttackGoal delta: a spider "won't attack in daylight". When set, the
	// goal gates additionally on the day/night proxy (the SAME gametime-darkness proxy the spawn rule
	// uses, 35-02). v1 has no light engine; the gate is the proxy. ZombieAttackGoal leaves this false
	// (plain melee). Cite SpiderAttackGoal / Spider$SpiderAttackGoal.
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

// newSpiderAttackGoal builds the SpiderAttackGoal delta: a MeleeAttackGoal that won't attack in
// daylight. Behaviorally the base melee + the daylight gate. Cite Spider$SpiderAttackGoal.
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
	if g.daylightGated && !g.isNight(t) {
		return false // SpiderAttackGoal: no attack in daylight (the day/night proxy)
	}
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

// canContinueToUse ports MeleeAttackGoal.canContinueToUse: keep attacking while the target is a
// present, valid combat target (the jar walks getTarget()!=null && canAttackTarget &&
// !navigation.isDone() / within-reach). v1: the target still resolves on the loop. NO RNG.
//
//	[VERIFIED javap MeleeAttackGoal.canContinueToUse: target = getTarget(); if null false; if
//	 !canAttack(target) false; ... return true.]
func (g *meleeAttackGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	if g.daylightGated && !g.isNight(t) {
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
	// setLookAt(target, 30, 30): face the target (the lookAtPlayerGoal yaw seam).
	yaw := yawTowardDeg(target.x-e.x, target.z-e.z)
	e.headYaw = yaw
	e.yaw = yaw

	// navigation.moveTo(target, speedModifier): SET the nav want toward the target (a goal SETS a want;
	// the async navigation.tick steps the mob — ai_mob.go:119). v1 carries POSITION only (the
	// speedModifier is stored but not yet routed to a per-request nav speed, the SAME posture as
	// PanicGoal/stroll). NEVER calls moveEntity.
	e.ai.setWantTarget(target.x, target.y, target.z)

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

// isNight is the SpiderAttackGoal daylight gate's day/night proxy: spiders calm in daylight, so the
// goal runs only at "night". The real Spider gate reads getLightLevelDependentMagicValue (a light
// read) — no light engine exists in v1, so this is the SAME gametime-darkness proxy the hostile spawn
// rule uses (35-02). Until the spawn proxy lands, this is a cited stub returning true (always "night")
// so the spider attacks — the gate is structured to read the real proxy when it lands. Cite
// Spider$SpiderAttackGoal (getLightLevelDependentMagicValue >= 0.5 -> daylight) + Monster
// .isDarkEnoughToSpawn (the proxy).
func (g *meleeAttackGoal) isNight(_ *TickLoop) bool {
	// CITE-DEFERRED: the gametime-darkness proxy (35-02's isDarkEnoughToSpawn) is not yet built; until
	// it lands the spider is treated as always at night (attacks). Wire this to the proxy in 35-02/35-05.
	return true
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
