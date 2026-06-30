package server

// ai_goals_eat.go — Phase 34 (MOB-PASS-02): the SHARED constants + documentation home for the sheep
// EatBlockGoal (net.minecraft.world.entity.ai.goal.EatBlockGoal), the NEW goal the vanilla_sheep mob adds
// at priority @5. The goal itself is PLUGIN-EXPRESSED: the vanilla_sheep .star declares it and draws the
// canUse nextInt gate via entity.rand_int (the same plugin-side RNG the pig's stroll/panic goals use), and
// calls the HOST eat seam (sheep_eat.go's eatGrassBlock, exposed as entity.eat_grass_block) on the eat tick
// and entity.eat_broadcast_byte10 on start. So this file is CONSTANTS + the bytecode citation, NOT a
// built-but-unwired Go goalSelector type — there is no Go-native eatBlockGoal struct, exactly as the lean
// in 34-PATTERNS.md (the sheep declares EatBlockGoal as a plugin goal, like breed routes through host
// try_breed). Keeping the constants here gives the .star a single cited source of truth for the timer/gate
// bounds.
//
// VERBATIM bytecode (net.minecraft.world.entity.ai.goal.EatBlockGoal, 34-JARNOTES.md:149-176,198-221):
//
//	flags = EnumSet.of(Flag.MOVE, Flag.LOOK, Flag.JUMP);          // newBaseGoal(flagMove|flagLook|flagJump)
//	EAT_ANIMATION_TICKS = 40;
//	canUse():  if (random.nextInt(adjustedTickDelay(isBaby? 50 : 1000)) != 0) return false;  // ONE nextInt gate
//	           pos = blockPosition();
//	           if (IS_EDIBLE.test(getBlockState(pos))) return true;          // tall-grass/fern (CITE-DEFERRED)
//	           return getBlockState(pos.below()).is(Blocks.GRASS_BLOCK);     // OR grass_block below
//	start():   eatAnimationTick = adjustedTickDelay(40);  broadcastEntityEvent(mob,(byte)10); navigation.stop();
//	stop():    eatAnimationTick = 0;
//	canContinueToUse(): eatAnimationTick > 0;
//	tick():    eatAnimationTick = max(0, eatAnimationTick - 1);
//	           if (eatAnimationTick != adjustedTickDelay(4)) return;         // acts ONLY at tick == 4
//	           ...eat the block (eatGrassBlock, sheep_eat.go)...
//
// adjustedTickDelay IDENTITY (34-JARNOTES.md:198-221, RE-VERIFIED — do NOT use ceilDiv): vanilla decimates
// the goalSelector (canUse runs every OTHER tick via the (tickCount+id)%2 gate), and reducedTickDelay =
// ceilDiv(n,2) HALVES the bound to COMPENSATE for that half-rate invocation — the two halvings cancel. Our
// Go driver (tick_phases.go) calls serverAiStep/canUse EVERY tick UNCONDITIONALLY (no decimation), so the
// bound must stay FULL (nextInt(1000)/nextInt(50)) to fire at the vanilla real-world rate; halving it here
// would DOUBLE the fire rate (the actual 1:1 break). So adjustedTickDelay(n) == n (the existing helper in
// ai_goals_breed.go) is the CORRECT faithful value. ⇒ the .star's canUse gate is
// entity.rand_int(eatGateBoundAdult)/entity.rand_int(eatGateBoundBaby); start sets the timer to
// eatAnimationTicks; tick acts at eatActTick. Reuse the existing adjustedTickDelay helper — NEVER ceilDiv.

const (
	// eatAnimationTicks is EatBlockGoal.EAT_ANIMATION_TICKS — the eat-animation timer start() arms
	// (adjustedTickDelay(40) == 40 at identity). The .star sets its eat timer to this on start.
	eatAnimationTicks = 40

	// eatActTick is the tick value at which EatBlockGoal.tick performs the eat: the act fires ONLY when
	// the decremented eatAnimationTick == adjustedTickDelay(4) == 4 (so the eat lands 36 ticks into the
	// 40-tick animation). The .star calls entity.eat_grass_block when its timer reaches this.
	eatActTick = 4

	// eatGateBoundAdult / eatGateBoundBaby are the canUse RNG-gate bounds: an ADULT sheep tests
	// nextInt(adjustedTickDelay(1000)) == 0 (a ~1/1000-per-tick eat trigger), a BABY nextInt(
	// adjustedTickDelay(50)) == 0 (a ~1/50 trigger — babies eat far more often). adjustedTickDelay is
	// IDENTITY (see above), so the bounds are 1000 / 50. The .star draws entity.rand_int(bound) and acts
	// on a 0 result.
	eatGateBoundAdult = 1000
	eatGateBoundBaby  = 50
)

// eatBlockGoalFlags is EatBlockGoal's flag set {MOVE, LOOK, JUMP} — documented here for the .star's goal
// declaration (it declares the goal with these flags so the goalSelector locks MOVE/LOOK/JUMP while the
// sheep eats, matching vanilla). Referenced as the cited source for the declared flag bits.
//
//	[VERIFIED javap EatBlockGoal.<init>: setFlags(EnumSet.of(Flag.MOVE, Flag.LOOK, Flag.JUMP)).]
const eatBlockGoalFlags = flagMove | flagLook | flagJump
