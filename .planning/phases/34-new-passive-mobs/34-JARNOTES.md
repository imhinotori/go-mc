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
