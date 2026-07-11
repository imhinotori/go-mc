package server

// ai_goals_ocelot_attack.go — the OcelotAttackGoal port (net.minecraft.world.entity.ai.goal
// .OcelotAttackGoal, a TOP-LEVEL class in 26.2 — NOT an inner class of Ocelot, and NOT a
// MeleeAttackGoal subclass). PORTED 1:1 (the STANDING MANDATE, idiomatic non-1:1 Go, no GPL paste)
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, read via javap -c -p this session).
//
// CRITICAL: the 26.2 OcelotAttackGoal is a STANDALONE Goal (extends Goal directly, not MeleeAttackGoal).
// The earlier-jar OcelotAttackGoal (pre-1.14) DID extend MeleeAttackGoal with a getAttackReachSqr()
// override + a post-hit nextFloat()<0.5 sit roll; that was rewritten in 26.2. The faithful 26.2
// port follows the actual bytecode:
//   - ctor: setFlags(EnumSet.of(MOVE, LOOK)) — {MOVE, LOOK}, NOT {MOVE}
//   - canUse: target = mob.getTarget(); return target != null  (NO RNG, NO other checks)
//   - canContinueToUse: target.isAlive() && mob.distanceToSqr(target) <= 225.0 (15²)
//     && (navigation.isDone() ? canUse() : true)
//   - stop: target = null; navigation.stop()
//   - requiresUpdateEveryTick: true
//   - tick: lookControl.setLookAt(target, 30, 30)
//          reach² = (mob.getBbWidth() * 2)²       // (2*width)², NOT a constant 4.0
//          distSqr = mob.distanceToSqr(target)
//          speed = 0.8
//          if distSqr > reachSqr && distSqr < 16.0: speed = 1.33
//          if distSqr < 225.0:                    speed = 0.6
//          navigation.moveTo(target, speed)
//          attackTime = max(attackTime - 1, 0)
//          if distSqr > reachSqr: return          // out of reach — no swing
//          if attackTime > 0: return              // cooldown
//          attackTime = 20
//          mob.doHurtTarget(getServerLevel(mob), target)
//
// THE PIG ORACLE IS UNTOUCHED: a passive pig declares no ocelot_attack goal (and an ocelot is its own
// base type), so this goal never ticks on it — no draw reaches the pinned pig stream (it has none —
// NO RNG in canUse/canContinueToUse/tick except the attackTime countdown, which is RNG-free).

import (
	"github.com/imhinotori/sulfur/level/attribute"
)

// --- ocelot attack reach / speed constants (jar-verified) -------------------------------------

// ocelotAttackAggroMaxSqr is OcelotAttackGoal.canContinueToUse's distance band: the ocelot keeps
// attacking while mob.distanceToSqr(target) <= 225.0 (15² blocks, the ldc2_w 225.0d constant in
// canContinueToUse). Past 15 blocks the goal drops the target.
//
//	[VERIFIED javap OcelotAttackGoal.canContinueToUse: distanceToSqr; ldc2_w 225.0d dcmpl ifle → 0.]
const ocelotAttackAggroMaxSqr = 225.0

// ocelotAttackPounceMaxSqr is the upper bound of the pounce-speed band: the speed becomes 1.33 only
// when distSqr is STRICTLY < 16.0 (the ldc2_w 16.0d dcmpg ifge → skip).
//
//	[VERIFIED javap OcelotAttackGoal.tick: d2 > d1 && d2 < 16.0: speed = 1.33 (ldc2_w 1.33d, ldc2_w 16.0d).]
const ocelotAttackPounceMaxSqr = 16.0

// ocelotAttackDefaultSpeed / ocelotAttackPounceSpeed / ocelotAttackApproachSpeed are the three
// navigation.moveTo(target, speed) speed scalars the tick() branches on (the default 0.8, the
// pounce-band 1.33, and the approach-band 0.6). The three bands stack: distSqr < 225.0 wins over
// distSqr < 16.0, so the approach 0.6 ONLY fires at 16.0 <= distSqr < 225.0.
//
//	[VERIFIED javap OcelotAttackGoal.tick: speed = 0.8; if d2 > d1 && d2 < 16.0: speed = 1.33;
//	 if d2 < 225.0: speed = 0.6; navigation.moveTo(target, speed).]
const (
	ocelotAttackDefaultSpeed  = 0.8
	ocelotAttackPounceSpeed   = 1.33
	ocelotAttackApproachSpeed = 0.6
)

// ocelotAttackCooldownTicks is the post-swing attackTime value (OcelotAttackGoal.attackTime = 20
// after a hit — the bipush 20 in tick's reset).
//
//	[VERIFIED javap OcelotAttackGoal.tick: bipush 20; putfield attackTime.]
const ocelotAttackCooldownTicks = 20

// ocelotAttackLookYawMax / ocelotAttackLookPitchMax are the LookControl.setLookAt(target, 30, 30)
// yaw/pitch caps in OcelotAttackGoal.tick — the same 30°/tick head cap MeleeAttackGoal.tick uses.
//
//	[VERIFIED javap OcelotAttackGoal.tick: ldc 30.0f; ldc 30.0f; setLookAt.]
const (
	ocelotAttackLookYawMax   float32 = 30.0
	ocelotAttackLookPitchMax float32 = 30.0
)

// --- ocelotAttackGoal ---------------------------------------------------------------------------

// ocelotAttackGoal ports net.minecraft.world.entity.ai.goal.OcelotAttackGoal (the TOP-LEVEL class in
// 26.2). It is a STANDALONE Goal (extends Goal directly), NOT a MeleeAttackGoal subclass, so it
// carries its own attackTime countdown (no meleeAttackGoal fields) and its own target cache (the
// jar's `this.target` field, captured in canUse and read in tick/canContinueToUse/stop).
type ocelotAttackGoal struct {
	baseGoal
	// target is the jar's OcelotAttackGoal.target field — captured in canUse (this.target = mob
	// .getTarget()) and cleared in stop (this.target = null). The tick body reads it from the field
	// (not from mob.getTarget()) — a faithful read of the jar's local cache.
	target int32
	// attackTime is the jar's OcelotAttackGoal.attackTime (the swing cooldown). Decremented every tick
	// in tick (max(.. - 1, 0)) and reset to 20 (ocelotAttackCooldownTicks) on every landed hit. The
	// FIRST swing proceeds with attackTime=0 (the default), so the ocelot attacks immediately when it
	// enters reach — no startup gate.
	attackTime int
}

// newOcelotAttackGoal builds the goal with the {MOVE, LOOK} flags (OcelotAttackGoal ctor:
// setFlags(EnumSet.of(MOVE, LOOK))). MOVE locks the navigation control so a pathfinder mob doesn't
// wander mid-pounce; LOOK locks the look control so no other goal fights the setLookAt(target, 30, 30).
//
//	[VERIFIED javap OcelotAttackGoal.<init>: setFlags(EnumSet.of(MOVE, LOOK)).]
func newOcelotAttackGoal() *ocelotAttackGoal {
	return &ocelotAttackGoal{baseGoal: newBaseGoal(flagMove | flagLook)}
}

// requiresUpdateEveryTick ports OcelotAttackGoal.requiresUpdateEveryTick = true. The ocelot
// attack must tick on the every-tick path (the GoalSelector.tickRunningGoals(z) only ticks a running
// goal when z || requiresUpdateEveryTick; with a decimated selector and attackTime decrementing per
// tick, a false here would double the cooldown to 40 ticks — a direct divergence from the jar).
//
//	[VERIFIED javap OcelotAttackGoal.requiresUpdateEveryTick: iconst_1 ireturn.]
func (g *ocelotAttackGoal) requiresUpdateEveryTick() bool { return true }

// canUse ports OcelotAttackGoal.canUse (bytecode-verified this session), NO RNG:
//
//	this.target = mob.getTarget();
//	if (this.target == null) return false;
//	return true;
//
// v1's getTarget() is a player id (mobAI.getTarget()); a present target is the gate. NO distance /
// onGround / LoS / daylight checks here — those live in canContinueToUse.
//
//	[VERIFIED javap OcelotAttackGoal.canUse: getTarget; putfield target; ifnull → 0; iconst_1 ireturn.]
func (g *ocelotAttackGoal) canUse(_ *TickLoop, e *Entity) bool {
	if e == nil || e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 { // getTarget() == null
		return false
	}
	g.target = id // this.target = mob.getTarget() (cached for tick/canContinueToUse/stop)
	return true
}

// canContinueToUse ports OcelotAttackGoal.canContinueToUse (bytecode-verified this session),
// NO RNG — but gated on three things:
//
//	if (!target.isAlive()) return false;                                   // alive check
//	if (mob.distanceToSqr(target) > 225.0) return false;                  // aggro band
//	if (navigation.isDone()) return canUse();                             // re-check when path finished
//	return true;                                                          // path in flight
//
// v1: target.isAlive() ≈ t.playerByEntityID(target) != nil (the player is present + alive);
// distanceToSqr(target) is entityDistSqr(e, target as entity) — but the target here is a LivingEntity
// (a player), so we read it through the player seam (t.playerByEntityID). navigation.isDone() is
// !e.ai.navigation.active() (the v1 inverse). No RNG.
//
//	[VERIFIED javap OcelotAttackGoal.canContinueToUse: target.isAlive ifeq → 0; distanceToSqr; ldc2_w
//	 225.0d dcmpl ifle → 0; navigation.isDone → canUse; else iconst_1.]
func (g *ocelotAttackGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e == nil || e.ai == nil {
		return false
	}
	id := g.target
	if id == 0 {
		return false
	}
	// target.isAlive(): the OcelotAttackGoal target is a LivingEntity — a PLAYER (via playerByEntityID) OR a
	// MOB (the ocelot's chicken/baby-turtle prey, via the owning-region store). Either present+alive counts.
	tx, ty, tz, alive := g.resolveOcelotTarget(t, id)
	if !alive {
		return false
	}
	// mob.distanceToSqr(target) <= 225.0: the squared feet-to-feet distance (the OcelotAttackGoal uses
	// Entity.distanceToSqr(Entity) which is the (x,y,z) triple diff squared, NOT a horizontal-only
	// projection — faithful 1:1).
	dx := e.x - tx
	dy := e.y - ty
	dz := e.z - tz
	distSqr := dx*dx + dy*dy + dz*dz
	if distSqr > ocelotAttackAggroMaxSqr {
		return false // past 15 blocks → drop the target
	}
	// navigation.isDone() ? canUse() : true: re-evaluate canUse only when the path is finished; while
	// the path is in flight the goal keeps running.
	if !e.ai.navigation.active() { // !isDone → path is done → re-check canUse
		return g.canUse(t, e)
	}
	return true // path in flight → keep attacking
}

// resolveOcelotTarget resolves the OcelotAttackGoal.target LivingEntity (the field cached in canUse) to its
// (x,y,z) + an alive flag, handling BOTH victim shapes: a PLAYER (via playerByEntityID) and a MOB — the
// ocelot's chicken/baby-turtle prey the targetSelector @1 goals acquire (via the owning-region entity store,
// t.cur() — the SAME store the target scan used). The jar target is a plain LivingEntity; v1 splits
// Player/Mob into two resolves. NO RNG.
func (g *ocelotAttackGoal) resolveOcelotTarget(t *TickLoop, id int32) (x, y, z float64, alive bool) {
	if p := t.playerByEntityID(id); p != nil && !p.dead {
		return p.x, p.y, p.z, true
	}
	if other, ok := t.cur().entities.get(id); ok && !other.dead {
		return other.x, other.y, other.z, true
	}
	return 0, 0, 0, false
}

// stop ports OcelotAttackGoal.stop (bytecode-verified this session), NO RNG:
//
//	this.target = null;
//	mob.getNavigation().stop();
//
// v1: target = null clears the cache; navigation.stop() → e.ai.clearWantTarget() (the seam).
//
//	[VERIFIED javap OcelotAttackGoal.stop: aconst_null; putfield target; getNavigation; stop.]
func (g *ocelotAttackGoal) stop(t *TickLoop, e *Entity) {
	g.target = 0 // this.target = null
	if e != nil && e.ai != nil {
		e.ai.clearWantTarget() // navigation.stop()
	}
}

// tick ports OcelotAttackGoal.tick (bytecode-verified this session). The body is the look-at +
// reach² + speed-branch + nav.moveTo + attackTime + doHurtTarget sequence (NO RNG anywhere):
//
//	getLookControl().setLookAt(target, 30, 30);                    // LOOK yaw/pitch clamp
//	double d1 = (getBbWidth() * 2) * (getBbWidth() * 2);           // reach² = (2*width)²
//	double d2 = mob.distanceToSqr(target.x, target.y, target.z);  // squared distance
//	double speed = 0.8;                                           // default
//	if (d2 > d1 && d2 < 16.0) speed = 1.33;                       // pounce band
//	if (d2 < 225.0)             speed = 0.6;                     // approach band (overrides pounce)
//	getNavigation().moveTo(target, speed);                         // path @ speed
//	attackTime = max(attackTime - 1, 0);                           // cooldown countdown
//	if (d2 > d1) return;                                           // out of reach — no swing
//	if (attackTime > 0) return;                                    // cooldown — no swing
//	attackTime = 20;                                               // reset
//	mob.doHurtTarget(getServerLevel(mob), target);                 // hit
//
// v1: doHurtTarget routes through the existing meleeAttackGoal.doHurtTarget seam (combat_mob.go
// applyDamage on a player victim — the SAME path the melee goal uses). The base ATTACK_DAMAGE
// attribute (Ocelot.createAttributes ATTACK_DAMAGE = 3.0, applied via seedAttributes) is the damage
// dealt. setLookAt clamps headYaw only (the same LookControl seam MeleeAttackGoal uses; the body yaw
// stays on the nav).
//
//	[VERIFIED javap OcelotAttackGoal.tick: setLookAt(30,30); (2*width)²; distanceToSqr; speed=0.8;
//	 >reach² && <16 → 1.33; <225 → 0.6; moveTo; max(attackTime-1,0); >reach² return; >0 return; =20;
//	 doHurtTarget(serverLevel, target).]
func (g *ocelotAttackGoal) tick(t *TickLoop, e *Entity) {
	if e == nil || e.ai == nil {
		return
	}
	id := g.target
	if id == 0 {
		return
	}
	// The OcelotAttackGoal.target is a LivingEntity — a PLAYER or the ocelot's chicken/baby-turtle prey (a
	// MOB). Resolve either shape to its position + alive flag; a vanished target aborts the tick.
	tx, ty, tz, alive := g.resolveOcelotTarget(t, id)
	if !alive {
		return
	}

	// setLookAt(target, 30, 30): turn the HEAD toward the target at most 30°/tick. HEAD only (headYaw);
	// the body yaw is owned by the navigation tick (the same seam MeleeAttackGoal.tick uses).
	yRotD := yawTowardDeg(tx-e.x, tz-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, ocelotAttackLookYawMax)

	// reach² = (mob.getBbWidth() * 2)² — the jar's reach computation, a function of the mob's WIDTH.
	// For an ocelot (width 0.6) the reach² = (1.2)² = 1.44. (The pre-1.14 OcelotAttackGoal used a
	// constant getAttackReachSqr=4.0 — that constant is ABSENT from the 26.2 jar.)
	d1 := float64(float32(e.width) * 2.0)
	d1 = d1 * d1
	dx := e.x - tx
	dy := e.y - ty
	dz := e.z - tz
	d2 := dx*dx + dy*dy + dz*dz // mob.distanceToSqr(target)

	// speed branching: default 0.8; pounce band (d2 > d1 && d2 < 16.0) → 1.33; approach band
	// (d2 < 225.0) → 0.6 (the approach band OVERRIDES the pounce band for 16.0 <= d2 < 225.0).
	speed := ocelotAttackDefaultSpeed
	if d2 > d1 && d2 < ocelotAttackPounceMaxSqr {
		speed = ocelotAttackPounceSpeed
	}
	if d2 < ocelotAttackAggroMaxSqr {
		speed = ocelotAttackApproachSpeed
	}

	// navigation.moveTo(target, speed): path toward the target AT the chosen speed (the blocks/tick
	// value, like MeleeAttackGoal's chase seam). setWantTargetSpeed routes through the async nav
	// (the same seam MeleeAttackGoal.tick uses).
	e.ai.setWantTargetSpeed(tx, ty, tz, speed)

	// attackTime = max(attackTime - 1, 0): the RNG-FREE per-attack countdown.
	if g.attackTime > 0 {
		g.attackTime--
	}

	// Out of reach: no swing. The check is d2 > d1 (strictly beyond the reach² — equality is in reach).
	if d2 > d1 {
		return
	}
	// Cooldown: no swing.
	if g.attackTime > 0 {
		return
	}
	// Reset the cooldown and deal the hit.
	g.attackTime = ocelotAttackCooldownTicks

	// mob.doHurtTarget(getServerLevel(mob), target): the standard Mob.doHurtTarget. Deals ATTACK_DAMAGE
	// (Ocelot.createAttributes 3.0, applied by seedAttributes). The victim is a PLAYER (the player hurt
	// path) OR a MOB — the ocelot's chicken/baby-turtle prey, routed through the entity-victim hurt path
	// (applyDamageEntity, the SAME seam meleeAttackGoal.doHurtTargetEntity uses). NO swing broadcast (the
	// jar's OcelotAttackGoal.tick calls doHurtTarget WITHOUT a preceding mob.swing, unlike MeleeAttackGoal).
	if p := t.playerByEntityID(id); p != nil {
		t.ocelotDoHurtTarget(e, p)
		return
	}
	if victim, ok := t.cur().entities.get(id); ok && !victim.dead {
		t.ocelotDoHurtTargetEntity(e, victim)
	}
}

// ocelotDoHurtTarget is the OcelotAttackGoal.tick's hit landing (no swing broadcast — the jar's
// OcelotAttackGoal.tick calls doHurtTarget WITHOUT a preceding mob.swing(MAIN_HAND), unlike
// MeleeAttackGoal.checkAndPerformAttack). Deals ATTACK_DAMAGE (Ocelot 3.0) through the existing
// player-hurt path (the SAME seam meleeAttackGoal.doHurtTarget uses).
func (t *TickLoop) ocelotDoHurtTarget(e *Entity, target *tickPlayer) {
	// mob.doHurtTarget(serverLevel, target): the same flat ATTACK_DAMAGE deal the melee goal does,
	// minus the swing broadcast (the ocelot's tail in the jar is the hit only, no animation packet).
	// Uses the existing applyDamage seam (combat.go — the player hurt path; same seam
	// meleeAttackGoal.doHurtTarget reaches on a player victim).
	dmg := float32(e.getAttributeValue(attribute.AttackDamage))
	src := damageSourceMobAttack(e.id)
	t.applyDamage(target, src, dmg)
	// doPostAttackEffects: a no-op for an ocelot (no weapon enchantments in scope; a cited
	// constant-zero loop over an empty weapon). Kept for symmetry with meleeAttackGoal.doHurtTarget.
	t.doPostAttackEffects(enchEntityRef{player: target}, src)
	// NO swing broadcast — the ocelot has no arm-swing animation; the jar's OcelotAttackGoal.tick
	// calls doHurtTarget without a preceding mob.swing(MAIN_HAND) (unlike MeleeAttackGoal
	// .checkAndPerformAttack which does swing-then-dohurt).
}

// ocelotDoHurtTargetEntity is the MOB-victim form of the OcelotAttackGoal.tick hit landing (the headline
// consumer — the ocelot hunting a CHICKEN or baby TURTLE). It is the exact sibling of ocelotDoHurtTarget,
// differing only in the victim shape: it deals the ocelot's ATTACK_DAMAGE (Ocelot 3.0) through the MOB hurt
// path (applyDamageEntity, which runs the standard dealDefaultKnockbackEntity), the SAME seam
// meleeAttackGoal.doHurtTargetEntity reaches for a mob victim. NO swing broadcast (the jar's OcelotAttackGoal
// .tick calls doHurtTarget WITHOUT a preceding mob.swing). Cite Mob.doHurtTarget(ServerLevel, Entity) +
// OcelotAttackGoal.tick offset 160-178 (doHurtTarget(getServerLevel, target)).
func (t *TickLoop) ocelotDoHurtTargetEntity(e *Entity, victim *Entity) {
	dmg := float32(e.getAttributeValue(attribute.AttackDamage))
	src := damageSourceMobAttack(e.id)
	t.applyDamageEntity(victim, src, dmg) // the MOB hurt path (also runs dealDefaultKnockbackEntity)
	// doPostAttackEffects: a cited pass-through for an ocelot (no weapon enchantments), kept for symmetry
	// with the melee entity-victim path's Thorns-reflection tail.
	t.doPostAttackEffects(enchEntityRef{mob: victim}, src)
	// NO swing broadcast (see ocelotDoHurtTarget).
}

// Compile-time assertion: ocelotAttackGoal IS a server.Goal.
var _ Goal = (*ocelotAttackGoal)(nil)
