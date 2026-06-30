# Phase 34 — pre-decompiled jar notes (cow / sheep / chicken)

Captured 2026-06-30 while Phase 33 plan-checked. KEY FINDING: all three reuse the EXACT goal set built
by Phases 30.1–33 (Float/Panic/Breed/Tempt/FollowParent/Stroll/Look/LookAround) — only the goal PARAMS +
food tags + attributes differ. The dogfood gate (Phase 33) pays off: these are mostly DATA (plugin
declarations), not new behavior — EXCEPT sheep's eatBlockGoal + chicken's egg-lay/float.

## Cow (net.minecraft.world.entity.animal.cow.AbstractCow.registerGoals) — Cow extends AbstractCow
```
0 FloatGoal
1 PanicGoal(2.0)
2 BreedGoal(1.0)
3 TemptGoal(1.25, is(COW_FOOD), false)
4 FollowParentGoal(1.25)
5 WaterAvoidingRandomStrollGoal(1.0)
6 LookAtPlayerGoal(Player, 6.0)
7 RandomLookAroundGoal
```
isFood = is(ItemTags.COW_FOOD). NO new goal — a pure reuse. (MushroomCow is a separate mob, out of scope.)

## Sheep (net.minecraft.world.entity.animal.sheep.Sheep.registerGoals)
```
0 FloatGoal
1 PanicGoal(1.25)
2 BreedGoal(1.0)
3 TemptGoal(1.1, is(SHEEP_FOOD), false)
4 FollowParentGoal(1.1)
5 this.eatBlockGoal            <-- NEW GOAL: EatBlockGoal (eat grass block → regrow wool). Decompile when the phase opens.
6 WaterAvoidingRandomStrollGoal(1.0)
7 LookAtPlayerGoal(Player, 6.0)
8 RandomLookAroundGoal
```
isFood = is(ItemTags.SHEEP_FOOD). Sheep also has wool/shear + DATA_WOOL color metadata (the dyed-wool
byte) — scope: at minimum the goal set + breeding; wool color + shear may be a cited deferral (the
ROADMAP says "jar-faithful Starlark plugins" — confirm scope with the user/roadmap; wool regrow needs
eatBlockGoal). EatBlockGoal: a 40-tick block-eat timer that consumes a grass block + sets wool regrow.

## Chicken (net.minecraft.world.entity.animal.chicken.Chicken.registerGoals)
```
0 FloatGoal
1 PanicGoal(1.4)
2 BreedGoal(1.0)
3 TemptGoal(1.0, is(CHICKEN_FOOD), false)
4 FollowParentGoal(1.1)
5 WaterAvoidingRandomStrollGoal(1.0)
6 LookAtPlayerGoal(Player, 6.0)
7 RandomLookAroundGoal
```
isFood = is(ItemTags.CHICKEN_FOOD). Chicken EXTRAS: reduced fall damage / float-down (flapping —
isFlapping, the slow-fall when falling), and EGG-LAYING (a per-tick egg timer 6000..12000 → drop an egg
item). Both are chicken-specific behavior beyond the shared goals — decompile Chicken.aiStep/customServerAiStep
when the phase opens. The float-down may need a per-mob gravity tweak.

## Food tags (CONFIRMED present in data/tag/tags.go): cow_food, sheep_food, chicken_food. ItemTags
## already extracted. isFood for each = itemInTag(id, "<x>_food") — the Phase-32 read, parameterized.

## Attributes: each mob's createAttributes (max_health, movement_speed) — javap
## Cow/Sheep/Chicken.createAttributes (or the Animal/AgeableMob base) when the phase opens.

## SCOPE NOTE for the planner: the SHARED goals are pure plugin declarations (declare_mob with the goal
## list + food tag + attributes, reusing the Go-native goal runtime). The NEW per-mob behavior
## (sheep eatBlock + wool, chicken egg-lay + float) is the real work — decide scope (full vs cited
## deferral of wool/eggs) per the ROADMAP. base_type must map each to its wire entity type (cow/sheep/chicken).
## Each new mob also needs its own oracle-style test OR a reduced gate (the pig oracle is the bit-exact one;
## new mobs reuse the proven goals so a lighter per-mob spawn+behavior test may suffice — confirm).

## --- Per-mob extras + attributes (decompiled 2026-06-30) ---

## Attributes (createAttributes):
- Cow:     MAX_HEALTH 10.0, MOVEMENT_SPEED 0.2
- Sheep:   MAX_HEALTH 8.0,  MOVEMENT_SPEED 0.23
- Chicken: MAX_HEALTH 4.0,  MOVEMENT_SPEED 0.25
Wire entity types Cow/Sheep/Chicken all present in data/entity/entity.go. categoryOf → CREATURE (like pig).

## Sheep EatBlockGoal (net.minecraft.world.entity.ai.goal.EatBlockGoal), flags {MOVE,LOOK,JUMP}
```java
EAT_ANIMATION_TICKS=40; IS_EDIBLE = state.is(BlockTags.EDIBLE_FOR_SHEEP);
canUse():  if (random.nextInt(adjustedTickDelay(isBaby? 50 : 1000)) != 0) return false;   // RNG GATE (lockstep!)
           pos = blockPosition();
           if (IS_EDIBLE.test(getBlockState(pos))) return true;                            // tall grass / fern at the mob
           return getBlockState(pos.below()).is(GRASS_BLOCK);                              // OR grass block below
start():   eatAnimationTick = adjustedTickDelay(40); broadcastEntityEvent(mob, (byte)10); navigation.stop();
stop():    eatAnimationTick = 0;
canContinueToUse(): eatAnimationTick > 0;
tick():    if (--eatAnimationTick == adjustedTickDelay(4)) { ... eat the block: tall-grass→AIR + setSheared(false)/wool regrow,
           or grass_block below→DIRT + the eat event; the sheep's woolRegrow + a baby ageUp(60) ... }   // decompile tick fully at exec
```
RNG: ONE nextInt per tick (the gate). EDIBLE_FOR_SHEEP is a BLOCK tag NOT in data/tag (only damage-type + item
tags were extracted). The plan must EITHER extend the tag extractor for block tags (edible_for_sheep) OR
cite-defer the tall-grass branch and check GRASS_BLOCK below directly (the common eat case). The wool-regrow +
shear is the sheep's DATA_WOOL byte + setSheared; scope per the ROADMAP (SC#2 says shear/wool with regrow).

## Chicken aiStep extras (net.minecraft.world.entity.animal.chicken.Chicken.aiStep)
```java
super.aiStep();
// flap visuals (oFlap/flapSpeed/flapping — client render; the flapSpeed += (onGround?-1:4)*0.3 clamp 0..1)
// SLOW FALL: if (!onGround && deltaMovement.y < 0) setDeltaMovement(movement.multiply(1.0, 0.6, 1.0));  // y *= 0.6 each tick falling
// EGG LAY (ServerLevel, !isBaby, !chickenJockey):
if (--eggTime <= 0) {
    if (dropFromGiftLootTable(CHICKEN_LAY, spawnAtLocation))      // drop an egg item (loot table)
        playSound(CHICKEN_EGG, 1.0, (nextFloat()-nextFloat())*0.2 + 1.0);   // RNG: 2 nextFloat (pitch)
    eggTime = nextInt(6000) + 6000;                              // RNG: nextInt(6000)  — reset 5..10 min
}
```
RNG (chicken, server): the egg-lay block draws nextInt(6000) on reset + 2 nextFloat for the egg sound pitch
(only WHEN it lays). eggTime inits to nextInt(6000)+6000 at spawn. The SLOW-FALL (y*=0.6 when falling) is a
per-mob physics override — the chicken needs a customServerAiStep / aiStep hook (the plugin tick) that applies it.
Chicken hitbox is smaller (~0.4×0.7) — SC#3 per-mob sizing.

## Cow milking (Cow.mobInteract): right-click with an empty BUCKET → return a MILK_BUCKET + sound. No RNG.
## (decompile Cow.mobInteract at exec — bucket→milk_bucket swap + SoundEvents.COW_MILK.)

## SCOPE (per ROADMAP SC#2 — full per-mob extras): cow milking, sheep EatBlock+shear/wool, chicken egg+slow-fall.
## The 3 mobs are declare_mob plugin declarations reusing the 8-goal Go runtime (cheap); the per-mob extras
## (milk interact, EatBlockGoal+wool, egg-lay+float) are the real work. PIG ORACLE stays green (untouched).
## Multi-plan: likely Plan A cow (+milk), Plan B sheep (+EatBlock+wool), Plan C chicken (+egg+float), Plan D gate.
## Each new mob may get a per-mob spawn+behavior test (the pig oracle is THE bit-exact one; the new mobs reuse
## proven goals so a lighter per-mob test suffices — but any NEW RNG goal (EatBlockGoal, chicken egg) needs its
## own lockstep discipline IF the mob is dogfooded as a plugin drawing that RNG).
