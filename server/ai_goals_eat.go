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
// adjustedTickDelay is NOT identity (the prior comment here was WRONG and self-contradicting: it claimed
// "our Go driver calls canUse EVERY tick UNCONDITIONALLY (no decimation)"). That premise is FALSE — the
// (tickCount+id)%2 selector decimation was ported at ai_mob.go:437-449 / GoalSelector.tickRunningGoals
// (false) at ai_goal.go:282, and EatBlockGoal does NOT override requiresUpdateEveryTick (jar default
// FALSE), so the sheep's eat goal canUse runs every OTHER server tick, exactly like vanilla. Therefore
// vanilla's own adjustedTickDelay(n) for this goal = reducedTickDelay(n) = ceil(n/2), NOT n:
//   canUse gate:  nextInt(adjustedTickDelay(isBaby? 50 : 1000)) = nextInt(isBaby? 25 : 500)
//   start timer:  eatAnimationTick = adjustedTickDelay(40) = 20
//   tick acts at: eatAnimationTick == adjustedTickDelay(4) = 2
// (In vanilla these halved delays run at half-rate for the same ~1000/50/40/4 WALL-clock cadence; our
// port decimates identically, so the halved values are the faithful ones. The RAW EatBlockGoal literals
// stay 1000/50/40/4 — the client eat-animation scale uses EAT_ANIMATION_TICKS=40 raw, only the goal's
// armed/gate values pass through adjustedTickDelay.)
//
// SCOPE NOTE (surfaced, NOT silently applied): this goal is PLUGIN-EXPRESSED — the actual RNG draw + timer
// live in plugins/mobs/vanilla_sheep/main.star (EAT_GATE_ADULT/BABY, EAT_ANIM_TICKS, EAT_ACT_TICK), and
// the stream-pinned TestEatBlockGoalRNGGate mirrors nextInt(eatGateBoundAdult). Halving these to the
// faithful 500/25/20/2 SHIFTS that pinned sheep-eat RNG stream, so it is a coordinated .star + test
// rebaseline that must be signed off (the same protocol the pig oracle follows), NOT changed piecemeal
// here. The constants below therefore still carry the RAW literals with the correction documented on each.
// Never use `n` as the "identity" — the driver decimates; use adjustedTickDelay(n, false) == ceil(n/2).

const (
	// eatAnimationTicks is EatBlockGoal.EAT_ANIMATION_TICKS (raw 40 — the client head-eat animation scale).
	// The goal's armed timer is adjustedTickDelay(40, false) == reducedTickDelay(40) == 20 (EatBlockGoal is
	// non-every-tick on the decimated selector); the .star's start() should arm 20, not the raw 40 (pending
	// the coordinated .star + TestEatBlockGoalRNGGate rebaseline noted in the header).
	eatAnimationTicks = 40

	// eatActTick is the eatAnimationTick value at which EatBlockGoal.tick performs the eat: the act fires
	// ONLY when the decremented timer == adjustedTickDelay(4, false) == reducedTickDelay(4) == 2 (the
	// decimated-selector value; raw literal 4). The .star calls entity.eat_grass_block when its timer reaches
	// this (2 after the coordinated rebaseline noted in the header).
	eatActTick = 4

	// eatGateBoundAdult / eatGateBoundBaby are the canUse RNG-gate bounds. Vanilla draws
	// nextInt(adjustedTickDelay(isBaby? 50 : 1000)); EatBlockGoal is non-every-tick on the decimated
	// selector, so adjustedTickDelay HALVES the bound: the faithful bounds are ceil(1000/2)=500 (adult) and
	// ceil(50/2)=25 (baby), run at the every-other-tick cadence for the same ~1/1000 / ~1/50 WALL-clock
	// trigger. These constants still hold the RAW 1000/50 pending the coordinated .star + TestEatBlockGoalRNG
	// Gate rebaseline (header SCOPE NOTE); the halved values are the ones that make the observable eat rate
	// match vanilla. The .star draws entity.rand_int(bound) and acts on a 0 result.
	eatGateBoundAdult = 1000
	eatGateBoundBaby  = 50
)

// eatBlockGoalFlags is EatBlockGoal's flag set {MOVE, LOOK, JUMP} — documented here for the .star's goal
// declaration (it declares the goal with these flags so the goalSelector locks MOVE/LOOK/JUMP while the
// sheep eats, matching vanilla). Referenced as the cited source for the declared flag bits.
//
//	[VERIFIED javap EatBlockGoal.<init>: setFlags(EnumSet.of(Flag.MOVE, Flag.LOOK, Flag.JUMP)).]
const eatBlockGoalFlags = flagMove | flagLook | flagJump
