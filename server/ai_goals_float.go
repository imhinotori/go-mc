package server

// ai_goals_float.go — MOB-SUB-04 (Phase 30-03): the Go-native FloatGoal@0, the first of the pig's
// five deferred goals. It is the JUMP-flag consumer that makes a pig in water bob/jump to stay afloat
// instead of sinking + suffocating. Wired @0 (highest precedence) on newPigAI in LOCKSTEP with the
// plugin declaration (plugins/vanilla_pig/main.star) — never split across plans (the oracle contract).
//
// PORTED 1:1 from net.minecraft.world.entity.ai.goal.FloatGoal in the unobfuscated 26.2 jar
// (javap -c -p temp/cache/26.2-inner.jar, this session):
//
//	public FloatGoal(Mob mob) {
//	    setFlags(EnumSet.of(Flag.JUMP));            // -> newBaseGoal(flagJump)
//	    mob.getNavigation().setCanFloat(true);      // -> navigation.canFloat = true (flag set; pathing deferred)
//	}
//	public boolean canUse() {
//	    return isInWater() && getFluidHeight(WATER) > getFluidJumpThreshold() || isInLava();   // NO RNG
//	}
//	public boolean requiresUpdateEveryTick() { return true; }
//	public void tick() {
//	    if (mob.getRandom().nextFloat() < 0.8f) mob.getJumpControl().jump();   // the ONLY new RNG
//	}
//
// RNG DISCIPLINE (CONTEXT Grey Area 3 / Pitfall 1): the single nextFloat() draw lives in tick() ONLY,
// drawn from the mob's per-entity seeded source (mobRandom(e) = e.ai.rng). canUse is a PURE fluid
// predicate (the Plan-01 mobInWater/mobFluidHeight/mobInLava reads + the Plan-02 jumpControl.doJump),
// so it draws nothing. In the DRY pig oracle world (plugin_pig_test.go: fillFloor only) canUse is
// always false → FloatGoal never ticks → zero new draws → TestPluginPigEqualsGoNativePig stays
// byte-identical with FloatGoal@0 added to BOTH pigs in lockstep.

// floatJumpProbability is FloatGoal.tick's threshold: nextFloat() < 0.8f arms the jump this tick.
//
//	[VERIFIED javap FloatGoal.tick: invokeinterface RandomSource.nextFloat; ldc float 0.8f; fcmpg;
//	 ifge → return — i.e. `if (nextFloat() < 0.8f) jumpControl.jump();`.]
const floatJumpProbability = 0.8

// floatGoal ports net.minecraft.world.entity.ai.goal.FloatGoal (flag {JUMP}). It claims JUMP at the
// highest priority (@0) and, while the mob is in a swimmable fluid, draws a per-tick 0.8 chance to arm
// the jump control — the swim-jump that keeps the mob afloat. No fields beyond baseGoal: FloatGoal
// holds only the mob back-reference in the jar, which the (t, e) parameters supply here.
type floatGoal struct {
	baseGoal
}

// newFloatGoal builds the FloatGoal with the JUMP flag (FloatGoal ctor: setFlags(EnumSet.of(JUMP))).
// The ctor's mob.getNavigation().setCanFloat(true) is applied when the goal is registered on a mob
// (newPigAI sets navigation.canFloat) — the goal struct itself is mob-agnostic (built once, reused).
//
//	[VERIFIED javap FloatGoal.<init>: setFlags(EnumSet.of(Goal$Flag.JUMP)); getNavigation().setCanFloat(true).]
func newFloatGoal() *floatGoal {
	return &floatGoal{baseGoal: newBaseGoal(flagJump)}
}

// canUse ports FloatGoal.canUse: isInWater() && getFluidHeight(WATER) > getFluidJumpThreshold() ||
// isInLava(). It reads the Plan-01 mob fluid predicates directly — NO RNG draw (Pitfall 1: the only
// FloatGoal draw is in tick()). The strict `>` matches the bytecode (dcmpl; ifgt).
//
//	[VERIFIED javap FloatGoal.canUse: isInWater; ifeq → check lava; getFluidHeight(WATER) vs
//	 getFluidJumpThreshold (dcmpl; ifgt); else isInLava.]
func (g *floatGoal) canUse(t *TickLoop, e *Entity) bool {
	return (t.mobInWater(e) && t.mobFluidHeight(e, fluidWater) > t.getFluidJumpThreshold(e)) || t.mobInLava(e)
}

// requiresUpdateEveryTick ports FloatGoal.requiresUpdateEveryTick = true: tick() runs every tick while
// the goal is active (so the swim-jump chance is rolled continuously, not just on the start edge).
func (g *floatGoal) requiresUpdateEveryTick() bool { return true }

// canContinueToUse ports FloatGoal's INHERITED Goal.canContinueToUse, which returns canUse() (FloatGoal
// declares no override, so it gets Goal's default `canContinueToUse(){ return this.canUse(); }`). Without
// this, floatGoal would inherit baseGoal.canContinueToUse → true (ai_goal.go:95) and keep running after
// the mob leaves water — holding the JUMP flag forever AND drawing nextFloat() every tick on dry land,
// diverging from the jar AND from the plugin pig (whose starlarkGoal.canContinueToUse already delegates
// to canUse). The DRY oracle world hides this (canUse never true → goal never starts), but a wet-world
// run desyncs the RNG stream. Delegating to canUse is the 1:1 fix.
//
//	[VERIFIED javap Goal.canContinueToUse: aload_0; invokevirtual canUse; ireturn (default = canUse()).]
func (g *floatGoal) canContinueToUse(t *TickLoop, e *Entity) bool { return g.canUse(t, e) }

// tick ports FloatGoal.tick: roll the per-mob RandomSource and, on nextFloat() < 0.8f, arm the jump
// control (the JumpControl.jump() analogue = jumpControl.doJump()). This is the SINGLE new RNG draw in
// the FloatGoal subsystem, drawn from mobRandom(e) (= e.ai.rng) — lockstep with the plugin float_tick.
// The serverAiStep JUMP slot's jumpControl.tick + entityJumpStep (Plan 02) then turn the armed flag
// into the +0.04 swim impulse.
//
//	[VERIFIED javap FloatGoal.tick: getRandom().nextFloat() < 0.8f → getJumpControl().jump().]
func (g *floatGoal) tick(_ *TickLoop, e *Entity) {
	// DRAW 1: nextFloat() < 0.8 (the swim-jump chance) — the ONLY new RNG in this subsystem.
	if mobRandom(e).nextFloat() < floatJumpProbability {
		if e != nil && e.ai != nil {
			e.ai.jumpControl.doJump()
		}
	}
}
