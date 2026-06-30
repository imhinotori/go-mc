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

## ============================================================================
## EXEC-TIME DECOMPILES (verbatim bytecode, captured 2026-06-30 at phase open)
## ============================================================================

## --- adjustedTickDelay / reducedTickDelay (Goal) — CRITICAL for EatBlockGoal lockstep ---
## adjustedTickDelay(ticks) = requiresUpdateEveryTick() ? ticks : reducedTickDelay(ticks)
## reducedTickDelay(ticks) = Mth.positiveCeilDiv(ticks, 2)
## requiresUpdateEveryTick() default = false. EatBlockGoal does NOT override it → default false.
## ⇒ adjustedTickDelay(1000)=500, (50)=25, (40)=20, (4)=2.
## So the EatBlockGoal canUse RNG gate is nextInt(500) (adult) / nextInt(25) (baby) — NOT 1000/50.
## MUST port Mth.positiveCeilDiv(n,2) exactly = (n + 2 - 1) / 2 = (n+1)/2 for positive n (Java int div).
##   Mth.positiveCeilDiv(x, y) = -Math.floorDiv(-x, y)  — for (1000,2)=500,(50,2)=25,(40,2)=20,(4,2)=2,(1,2)=1.

## --- Cow milking (net.minecraft.world.entity.animal.cow.AbstractCow.mobInteract) — NO RNG ---
## @Override mobInteract(Player player, InteractionHand hand) {
##     ItemStack itemStack = player.getItemInHand(hand);
##     if (itemStack.is(Items.BUCKET) && !this.isBaby()) {
##         player.playSound(SoundEvents.COW_MILK, 1.0f, 1.0f);
##         ItemStack r = ItemUtils.createFilledResult(itemStack, player, Items.MILK_BUCKET.getDefaultInstance());
##         player.setItemInHand(hand, r);
##         return InteractionResult.SUCCESS;
##     }
##     return super.mobInteract(player, hand);   // the feed/breed path (Animal.mobInteract)
## }
## Wire into handleInteract BEFORE the feed path: held is BUCKET && !isBaby → consume 1 bucket, give milk_bucket
## (createFilledResult: if stack size 1 → replace with milk_bucket; else shrink 1 + add milk_bucket to inv or drop),
## COW_MILK sound. Else fall through to the existing feed/breed handler. Cow-only (gate on mob name/type).

## --- Sheep EatBlockGoal (net.minecraft.world.entity.ai.goal.EatBlockGoal) — flags {MOVE,LOOK,JUMP} ---
## EAT_ANIMATION_TICKS=40; IS_EDIBLE = state.is(BlockTags.EDIBLE_FOR_SHEEP);
## canUse():  if (random.nextInt(adjustedTickDelay(isBaby?50:1000)) != 0) return false;  // ONE nextInt, UNCONDITIONAL gate
##            pos = blockPosition();
##            if (IS_EDIBLE.test(getBlockState(pos))) return true;                        // tall-grass/fern at mob
##            return getBlockState(pos.below()).is(Blocks.GRASS_BLOCK);                    // OR grass_block below
## start():   eatAnimationTick = adjustedTickDelay(40)=20; level.broadcastEntityEvent(mob, (byte)10); navigation.stop();
## stop():    eatAnimationTick = 0;
## canContinueToUse(): eatAnimationTick > 0;
## tick():    eatAnimationTick = Math.max(0, eatAnimationTick - 1);
##            if (eatAnimationTick != adjustedTickDelay(4)=2) return;                      // only acts at tick==2
##            pos = blockPosition();
##            if (IS_EDIBLE.test(getBlockState(pos))) {
##                if (MOB_GRIEFING gamerule) level.destroyBlock(pos, false);
##                mob.ate();
##            } else {
##                below = pos.below();
##                if (getBlockState(below).is(GRASS_BLOCK)) {
##                    if (MOB_GRIEFING) { level.levelEvent(2001, below, Block.getId(GRASS_BLOCK.defaultBlockState()));
##                                        level.setBlock(below, DIRT.defaultBlockState(), 2); }
##                    mob.ate();
##                }
##            }
## RNG: EXACTLY ONE nextInt per tick (the canUse gate, drawn every tick canUse runs). No other RNG in the goal.
## EDIBLE_FOR_SHEEP is a BLOCK tag NOT extracted → either extend the block-tag extractor OR cite-defer the
## tall-grass branch and check GRASS_BLOCK-below only (the common eat case). Document the deferral if taken.
## mob.ate() on Sheep (Sheep.ate): super.ate(); setSheared(false) [wool regrow]; if(canAgeUp) ageUp(60).

## --- Chicken aiStep (net.minecraft.world.entity.animal.chicken.Chicken.aiStep) ---
## super.aiStep();
## oFlap=flap; oFlapSpeed=flapSpeed; flapSpeed += (onGround?-1:4)*0.3f; flapSpeed=clamp(0,1);
## if (!onGround && flapping<1) flapping=1; flapping*=0.9f;       // client-render visuals — server no-op (no net effect)
## Vec3 m = getDeltaMovement();
## if (!onGround && m.y < 0) setDeltaMovement(m.multiply(1.0, 0.6, 1.0));   // SLOW FALL — y *= 0.6 each falling tick
## flap += flapping * 2.0f;
## if (level instanceof ServerLevel sl) {
##     if (isAlive && !isBaby && !isChickenJockey && --eggTime <= 0) {       // eggTime decremented EVERY server tick
##         if (dropFromGiftLootTable(sl, BuiltInLootTables.CHICKEN_LAY, this::spawnAtLocation)) {  // loot draws (egg)
##             playSound(CHICKEN_EGG, 1.0f, (random.nextFloat()-random.nextFloat())*0.2f + 1.0f);  // 2 nextFloat — ONLY if dropped
##             gameEvent(ENTITY_PLACE);
##         }
##         eggTime = random.nextInt(6000) + 6000;                            // nextInt(6000) — reset, ALWAYS when eggTime hits 0
##     }
## }
## eggTime field-init = random.nextInt(6000) + 6000  (drawn at construction via entity RNG).
## RNG order when laying: [loot draws] → 2 nextFloat (sound, only if loot dropped an egg) → nextInt(6000).
## When eggTime>0: ZERO draws that tick (just the decrement). The chicken needs a per-mob aiStep/customServerAiStep
## hook (the plugin tick seam) to apply slow-fall + egg-lay. CHICKEN_LAY loot table → 1 egg (check loot evaluator).

## --- ✅ RESOLVED: adjustedTickDelay STAYS identity (=n). The phase-33 decision is CORRECT (re-verified). ---
## I initially suspected the stub `func adjustedTickDelay(n int) int { return n }` (ai_goals_breed.go:43)
## was a 1:1 break (jar adjustedTickDelay = reducedTickDelay = positiveCeilDiv(n,2) for goals that don't
## override requiresUpdateEveryTick). It is NOT a break — here is the full vanilla machinery and why identity
## is the faithful Go value:
##   Mob.serverAiStep (jar offsets ~754): the goal selector is DECIMATED —
##     if ((tickCount + getId()) % 2 == 0 || tickCount <= 1) { goalSelector.tick(); }      // even tick: full canUse pass
##     else { goalSelector.tickRunningGoals(false); }                                       // odd tick: only requiresUpdateEveryTick goals
##   So a goal whose requiresUpdateEveryTick()==false (EatBlock, Follow, Stroll, …) has canUse() evaluated
##   only EVERY OTHER server tick in vanilla. reducedTickDelay=ceilDiv(n,2) HALVES the bound to COMPENSATE
##   for that half-rate invocation — the two halvings cancel, so the goal fires at the intended real rate.
##   ⇒ A 1000-intent becomes nextInt(500) drawn every-other-tick = same expected period as nextInt(1000)
##     drawn every tick.
##   OUR Go driver (tick_phases.go:368  `for _, e := range snapshot { e.ai.serverAiStep(t, e) }`) calls
##   serverAiStep — and thus goalSelector.tick() / canUse — EVERY tick, UNCONDITIONALLY. We do NOT replicate
##   the (tickCount+id)%2 decimation. So to fire at the vanilla real-world rate, the bound must stay FULL:
##   nextInt(1000) drawn every tick. Halving it (ceilDiv) here WOULD double the fire rate — the actual 1:1 break.
##   adjustedTickDelay(n)=n (identity) is therefore the CORRECT faithful compensation, exactly as the
##   phase-33 comment ("identity at 20 TPS, NOT reducedTickDelay's ceil(n/2)") documents.
## ⇒ EatBlockGoal canUse gate in Go = nextInt(adjustedTickDelay(isBaby?50:1000)) = nextInt(50)/nextInt(1000),
##   drawn every tick. start() eatAnimationTick = adjustedTickDelay(40) = 40. tick() acts at == adjustedTickDelay(4) = 4.
##   Keep using the existing adjustedTickDelay helper for ALL these — do NOT introduce ceilDiv. Do NOT touch
##   the existing adjustedTickDelay==n tests. (If Go ever adopts the every-other-tick decimation as a later
##   optimization, adjustedTickDelay must flip to ceilDiv IN LOCKSTEP — note for the future, out of scope here.)

## --- ⚠ CHICKEN EGG LOOT GAP (exec-time, found pre-plan-check) ---
## level/loot/data/loot_table/gameplay/chicken_lay.json IS embedded (LoadTable("minecraft:gameplay/chicken_lay")
## works — embed.go strips "minecraft:" → data/loot_table/gameplay/chicken_lay.json). BUT it is a
## `minecraft:gift` table whose single `alternatives` entry has 3 children, EACH gated on a
## `chicken/variant` component predicate (temperate→minecraft:egg, warm→brown_egg, cold→blue_egg).
## The loot evaluator (condition.go) ports `entity_properties` ONLY for is_on_fire — the chicken/variant
## COMPONENT predicate is NOT ported. ⇒ with the table as-is, all 3 alternatives children FAIL → the gift
## table emits NOTHING → no egg EVER drops (a silent fidelity break the chicken plan must NOT ship).
## Vanilla: Chicken.finalizeSpawn picks the variant by biome (VariantUtils.selectVariantToSpawn) — overworld
## default biomes resolve to TEMPERATE → plain minecraft:egg.
## RESOLUTION (cite-defer, v1 — no chicken-variant subsystem): the chicken egg-lay drops plain minecraft:egg
## directly (the TEMPERATE default), bypassing the variant-gated alternatives. Document the deferral in
## 34-CONTEXT Deferred Ideas (brown_egg/blue_egg by cold/warm biome variant = future). Observably identical
## for a default-biome chicken. Either (a) spawnAtLocation(egg) directly in the egg-lay path with a cited
## stub, OR (b) extend the evaluator's entity_properties to treat a missing chicken/variant as temperate.
## Lean (a) — smallest, clearly-cited, no evaluator change. The egg-lay RNG (2 nextFloat + nextInt(6000))
## is UNCHANGED either way — only the item resolution is deferred.

## --- SHEEP SHEAR + DATA_WOOL (decompiled for the plan-check blocker B1) ---
## Sheep.mobInteract (the shear path — runs BEFORE super=feed):
##   if (itemStack.is(Items.SHEARS)) {
##       if (level instanceof ServerLevel sl) {
##           if (readyForShearing()) {                         // = !isSheared() && !isBaby()
##               shear(sl, SoundSource.PLAYERS, itemStack);
##               gameEvent(GameEvent.SHEAR, player);
##               itemStack.hurtAndBreak(1, player, hand.asEquipmentSlot());   // shears durability -1
##               return InteractionResult.SUCCESS_SERVER;
##           }
##       }
##       return InteractionResult.CONSUME;                     // not ready / client side
##   }
##   return super.mobInteract(player, hand);                   // the feed/breed path
## Sheep.shear(ServerLevel level, SoundSource src, ItemStack tool):
##   level.playSound(null, this, SoundEvents.SHEEP_SHEAR, src, 1.0, 1.0);
##   dropFromShearingLootTable(level, BuiltInLootTables.SHEAR_SHEEP, tool, (l, drop) -> {
##       for (int i = 0; i < drop.getCount(); i++) {
##           ItemEntity e = spawnAtLocation(l, drop.copyWithCount(1), 1.0f);
##           if (e != null) e.setDeltaMovement(e.getDeltaMovement().add(
##               (random.nextFloat()-random.nextFloat())*0.1f,   // 2 nextFloat (x scatter)
##                random.nextFloat()*0.05f,                       // 1 nextFloat (y)
##               (random.nextFloat()-random.nextFloat())*0.1f));  // 2 nextFloat (z scatter)  => 5 nextFloat PER dropped item
##       }
##   });
##   setSheared(true);
##   ⇒ SHEAR loot tables ARE embedded: level/loot/data/loot_table/shearing/sheep/<color>.json (white.json → wool).
##     LoadTable("minecraft:shearing/sheep/white") for the default WHITE sheep. The shear-scatter RNG draws
##     from the MOB stream (this.random) — 5 nextFloat per dropped wool stack. Default white sheep drops 1 wool
##     stack (rolls 1) → 5 nextFloat. Lockstep ONLY matters if the sheep is dogfooded Go-vs-plugin; the
##     pig oracle is untouched, so a per-mob behavior+RNG test suffices. The loot SEED itself is event-time
##     rand.Int64() (OFF the mob stream, like death loot) — does not perturb mobRandom(e).
## readyForShearing(): !isSheared() && !isBaby().
## isSheared():  (DATA_WOOL & 0x10) != 0.
## setSheared(b): DATA_WOOL = b ? (cur | 0x10) : (cur & 0xEF).
## getColor():   DyeColor.byId(DATA_WOOL & 0xF).   DEFAULT_COLOR = WHITE (id 0); DEFAULT_SHEARED = false.
##   ⇒ default DATA_WOOL byte = 0x00 (white, not sheared).
## DATA_WOOL_ID ACCESSOR INDEX = 18 (BYTE serializer, id 0). Chain (continuing the phase-33 dataBabyIndex=16
##   derivation, VERIFIED javap): Entity 0..7, LivingEntity 8..14, Mob 15, AgeableMob 16=DATA_BABY_ID +
##   17=AGE_LOCKED; Animal adds NO accessor (javap-confirmed empty); Sheep adds DATA_WOOL_ID = index 18.
##   The DataValue frames: Byte(18) + VarInt(0=BYTE serializerID) + Byte(woolByte). (byteSerializerID=0,
##   the first in the EntityDataSerializers registration sequence the phase-33 boolSerializerID=8 cites.)
##   Cross-0 / shear broadcast: ClientboundSetEntityData with the DATA_WOOL entry on setSheared change.
## SCOPE: v1 ships WHITE sheep only (spawn color = white; dye mechanics deferred per CONTEXT). So getColor
##   is always WHITE, the shear table is shearing/sheep/white. The shear ACTION (shears→wool+setSheared(true))
##   + wool REGROW (eat→setSheared(false)) together satisfy MOB-PASS-02 "shear/wool with regrow".

## --- EXEC NOTE: shearing/sheep/white.json (the shear drop) ---
## type minecraft:shearing, ONE entry minecraft:white_wool, rolls = uniform(min 1, max 3).
## ⇒ a shear drops 1–3 white_wool (the uniform roll count drawn from the LOOT SEED = event-time rand.Int64(),
##   OFF the mob stream). The loot evaluator handles this AS-IS: getRandomItemsRaw is table-type-agnostic
##   (the "minecraft:shearing" type string is parsed metadata, not an eval gate — vanilla getRandomItemsRaw
##   ignores type), and uniform NumberProvider rolls ARE supported (parseNumberProvider / GetInt). So
##   LoadTable("minecraft:shearing/sheep/white") + Roll(seed, ctx) yields 1–3 white_wool — NO evaluator change.
##   shear() spawns each rolled stack as its own ItemEntity with the 5-nextFloat scatter (MOB stream) per stack.
##   (Contrast the chicken_lay GIFT table, which IS variant-gated and needs the cite-defer to a direct egg.)

## --- ⚠ EXEC NOTE: customServerAiStep seam is UNWIRED — chicken slow-fall/egg-lay must wire it into ai_mob.go ---
## ai_mob.go's serverAiStep ORDER comment lists "...navigation.tick -> customServerAiStep -> moveControl/
## lookControl/jumpControl" BUT serverAiStep (ai_mob.go ~155-208) does NOT actually call customServerAiStep —
## there is no per-mob aiStep hook implemented yet (the pig has no aiStep override, so none was needed).
## The chicken's slow-fall (deltaMovement.y*=0.6 when !onGround && vy<0) + egg-lay are Chicken.aiStep work →
## they need a customServerAiStep seam ADDED to serverAiStep at the jar-correct slot (vanilla Mob.aiStep calls
## the entity's customServerAiStep; the order is AFTER navigation.tick, before the move/look/jump controls —
## though for slow-fall the exact placement vs moveEntity matters: chicken's y*=0.6 runs in Chicken.aiStep =
## super.aiStep() FIRST [which is LivingEntity.aiStep → travel/gravity], THEN the chicken overrides — verify the
## slow-fall applies AFTER gravity each tick). This is an ai_mob.go edit (serverAiStep) — ai_mob.go is the
## PIG'S core file. ⚠ COLLISION/ORACLE RISK: the seam call must be ADDITIVE + mob-gated (only chickens have a
## customServerAiStep; pig's is a no-op) so the pig oracle's serverAiStep RNG stream is BYTE-IDENTICAL (zero
## new draws for the pig). Whichever plan edits ai_mob.go (34-00 the shared owner) must keep the pig path
## draw-free. Confirm at exec that the seam dispatch (e.g. e.ai.customServerAiStep or a baseType switch) adds
## NOTHING to the pig's tick. The chicken's slow-fall is PURE physics (no RNG); the egg-lay RNG (2 nextFloat +
## nextInt(6000)) is chicken-only via mobRandom(e).

## --- DOCKER -race COMMAND (plan-check blocker B2 — lift verbatim from 33-05) ---
## MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/
##   Embed as an <automated> verify in 34-04 (append ` 2>&1 | tail -20`). TestRegionPanicIsolated prints a
##   scary recovered-panic stack in full runs (KNOWN false alarm — judge final ok/FAIL).

## --- Attributes (createAttributes) — all CONFIRMED ---
## Cow (AbstractCow):  Animal.createAnimalAttributes + MAX_HEALTH 10.0 + MOVEMENT_SPEED 0.2
## Sheep:              Animal.createAnimalAttributes + MAX_HEALTH 8.0  + MOVEMENT_SPEED 0.23
## Chicken:            Animal.createAnimalAttributes + MAX_HEALTH 4.0  + MOVEMENT_SPEED 0.25
