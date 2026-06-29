# Feature Research

**Domain:** Mob / living-entity subsystems + new mob types for Sulfur (1:1 Minecraft Java 26.2 server in Go)
**Researched:** 2026-06-29
**Confidence:** HIGH (every behavioral claim below is javap-disassembled from `temp/cache/26.2-inner.jar` this session; class chains cited inline)

> **Orienting frame.** This milestone is NOT "design mob AI". It is "finish porting the jar". The
> jar already dictates every goal, priority, draw order, and constant. The only open work is
> building the four missing *subsystems* the deferred goals read, then wiring each new mob's
> `registerGoals()` 1:1. So the "features" below are split into **Subsystems** (the load-bearing
> prerequisites — build these first) and **Mobs** (passive / hostile / neutral — each a thin
> Starlark plugin once its subsystems exist). The dependency graph is the build order.

---

## The four subsystems (the keystone work — everything else depends on these)

These are the cited-deferred prerequisites from `deferred-goals.md`. Each is jar-verified below.
Building a mob before its subsystems exist = built-but-unwired or faked, both forbidden by the
1:1 mandate.

### S1 — Mob JumpControl + fluid detection (`FloatGoal@0`)

**Jar chain:** `net.minecraft.world.entity.ai.goal.FloatGoal` (disassembled).
- ctor: `setFlags(EnumSet.of(Goal.Flag.JUMP))` + `mob.getNavigation().setCanFloat(true)`.
- `requiresUpdateEveryTick()` = `true`.
- `canUse()` (exact bytecode): `(mob.isInWater() && mob.getFluidHeight(FluidTags.WATER) > mob.getFluidJumpThreshold()) || mob.isInLava()`.
- `tick()` (exact bytecode): `if (mob.getRandom().nextFloat() < 0.8F) mob.getJumpControl().jump();`

**Observable behavior:** a mob in water deeper than a threshold (or in lava) bobs up — each tick it
has an 80% chance to fire a jump impulse, so it floats at the surface instead of sinking/drowning.

**What must be built:** (1) a mob `JumpControl` with a `jump()` impulse seam + the per-tick consume
(`JumpControl.tick()` sets the impulse, the entity's `aiStep` adds the upward delta) — today
`ai_mob.go` documents jumpControl as DEFERRED (line 96); (2) a mob fluid predicate set:
`isInWater()`, `getFluidHeight(WATER)`, `getFluidJumpThreshold()` (LivingEntity default 0.4),
`isInLava()` — today fluid code in `fluid.go`/`breath.go` is `*tickPlayer`-only.

**Complexity:** MEDIUM. The fluid predicates reuse `TickLoop.fluidAt` block reads; the JumpControl
is a small control object but the jump-impulse must be the vanilla `JumpControl.jump()` (set a flag,
consume in aiStep), not a raw `vy` poke (explicitly forbidden).

**Table-stakes / differentiator:** TABLE STAKES — without it any mob walking into water sinks and
suffocates, which is visibly broken. Shared by every new mob (FloatGoal appears in cow, sheep,
chicken, spider, wolf registerGoals; skeleton/zombie include it via their helpers).

---

### S2 — Mob damage / hurt pipeline + `lastDamageSource` + damage-type tags (`PanicGoal@1`, ALL hostiles)

**Jar chain:** `net.minecraft.world.entity.ai.goal.PanicGoal` (disassembled).
- ctor `(PathfinderMob, double)` defaults the tag to `DamageTypeTags.PANIC_CAUSES` (confirmed:
  `getstatic DamageTypeTags.PANIC_CAUSES` in the 2-arg ctor); a 3-arg ctor takes a
  `Function<PathfinderMob, TagKey<DamageType>>`. Flags = `{MOVE}`.
- `shouldPanic()` (exact bytecode): `mob.getLastDamageSource() != null && mob.getLastDamageSource().is(panicCausingDamageTypes.apply(mob))`.
- `canUse()`: `if (!shouldPanic()) return false;` then `if (mob.isOnFire()) { BlockPos water = lookForWater(level, mob, 5); if (water != null) { posX/Y/Z = water; return true; } }` else `return findRandomPosition();` (a random flee position via `DefaultRandomPos.getPos`).

**Observable behavior:** when an animal is hurt by a panic-causing damage type (or is on fire) it
flees — toward water if on fire (vertical search distance 5), otherwise to a random reachable
position — running at the goal's speed modifier.

**What must be built:** a mob damage pipeline (mob `hurt`/`actuallyHurt`/`applyDamage`) + per-mob
`lastDamageSource` field set on every hurt + a `DamageSource.is(TagKey<DamageType>)` test + the
`DamageTypeTags.PANIC_CAUSES` tag membership. Today `combat.go` is `*tickPlayer`-only — mobs have no
`applyDamage` path. The pig's deferred-goals note explicitly forbids faking a "was hurt" flag.

**Complexity:** HIGH. This is the keystone. It is the prerequisite for PanicGoal AND for every
hostile mob (which must both take damage from the player AND deal melee damage to a target). The
pipeline must mirror the vanilla LivingEntity.hurt call chain (invulnerable-time / lastHurt /
actuallyHurt / damage-after-armor) faithfully, then set `lastDamageSource`.

**Table-stakes / differentiator:** TABLE STAKES. A mob that cannot take or deal damage is not a mob.
Build this FIRST among the gameplay-facing subsystems — it unblocks the most.

---

### S3 — Animal aging + breeding (`BreedGoal@3`, `FollowParentGoal@5`)

**Jar chain:**
- `net.minecraft.world.entity.AgeableMob` (disassembled signatures): `getAge()`, `setAge(int)`,
  `ageUp(int)`, `isBaby()` (final), `setBaby(boolean)` (final), `canAgeUp()`, `BABY_START_AGE`.
  Age is a tick counter: negative = baby (counts up to 0 = adult), positive = breeding cooldown
  (counts down to 0 = can breed again).
- `net.minecraft.world.entity.animal.Animal` (disassembled signatures): `private int inLove;`,
  `isInLove()`, `setInLove(Player)`, `setInLoveTime(int)`, `getInLoveTime()`, `canFallInLove()`,
  `canMate(Animal)`, `isFood(ItemStack)` (abstract — each mob overrides),
  `spawnChildFromBreeding(ServerLevel, Animal)`, `finalizeSpawnChildFromBreeding(...)`, `getLoveCause()`.
- `net.minecraft.world.entity.ai.goal.BreedGoal` (disassembled): `canUse()` calls
  `Animal.isInLove()` then `getFreePartner()`; `getFreePartner()` calls
  `ServerLevel.getNearbyEntities(class, TargetingConditions, mob, AABB)` then `Animal.canMate(partner)`;
  `breed()` calls `Animal.spawnChildFromBreeding(serverLevel, partner)`.
- `net.minecraft.world.entity.ai.goal.FollowParentGoal` (disassembled): `canUse()` gates on
  `Animal.getAge()` (only runs when this animal is a baby, age in the baby range), scans nearby
  adults of the same class within `HORIZONTAL_SCAN_RANGE`, picks the nearest; `tick()` calls
  `getNavigation().moveTo(parent, speedModifier)`.

**Observable behavior:** feeding an animal its `isFood` item (when not a baby and off cooldown) puts
it in love mode (`setInLove`, hearts, `inLove` ticks down). Two in-love animals of the same type
within range find each other (`getFreePartner` + `canMate`), path together, and spawn a baby
(`spawnChildFromBreeding`); both then enter breeding cooldown (positive age). Babies (negative age)
follow the nearest adult of their type and grow up as age counts toward 0.

**What must be built:** the `age`/`inLove` tick state on the entity, the love-mode-on-feed path
(reads S4's held-item), `canMate`, partner search (`getNearbyEntities`), baby spawn
(`spawnChildFromBreeding` → a new entity of the same type with `setBaby(true)`), and the baby-follow
nav. None exists today (`grep isBaby|getAge|inLove` → none).

**Complexity:** HIGH. Two goals + a stateful age/love system + child spawning. Depends on S4
(feeding reads the held item).

**Table-stakes / differentiator:** breeding itself is TABLE STAKES for "farm animals exist
meaningfully" (a static cow you cannot breed is a decoration). Baby-follow is part of the same
subsystem. Wool/milk side-features are separate (per-mob).

---

### S4 — Held-item read + ItemTags (`TemptGoal@4`, feeding for S3)

**Jar chain:** `net.minecraft.world.entity.ai.goal.TemptGoal` (disassembled).
- ctor `(PathfinderMob, double speed, Predicate<ItemStack> items, boolean canScare)` → delegates to
  the 5-arg with `stopDistance = 2.5`. Flags = `{MOVE, LOOK}`.
- `canUse()`: if `calmDown > 0` decrement and return false; else
  `player = getServerLevel(mob).getNearestPlayer(targetingConditions, mob)` where the targeting
  range = `mob.getAttributeValue(Attributes.TEMPT_RANGE)` and the selector is `shouldFollow`;
  return `player != null`.
- `shouldFollow(LivingEntity)` (exact bytecode): `items.test(e.getMainHandItem()) || items.test(e.getOffhandItem())`.
- The `items` predicate is built per-mob in `registerGoals` via a lambda. Pig:
  `ItemStack.is(ItemTags.PIG_FOOD)` and a second TemptGoal for `Items.CARROT_ON_A_STICK`. Cow:
  `ItemTags.COW_FOOD` (confirmed `AbstractCow.lambda$registerGoals$0` → `ItemStack.is(ItemTags.COW_FOOD)`).

**Observable behavior:** when a player holds the mob's tempt item (main or off hand) within tempt
range (an attribute, default ~10 for most animals) the mob looks at and follows the player; if
`canScare` is true (false for pig/cow) a sudden player move scares it (calmDown cooldown).

**What must be built:** read the nearest player's main/off-hand `ItemStack`, an `ItemStack.is(TagKey)`
membership test, the `ItemTags.*_FOOD` tags + `Items.CARROT_ON_A_STICK`, and the
`Attributes.TEMPT_RANGE` read (attribute system exists from v3.1 SUB-ATTRIB — add TEMPT_RANGE if
absent). Nothing exists today (`grep getMainHandItem|PIG_FOOD` → none).

**Complexity:** MEDIUM. The player-scan seam already exists (`nearestPlayerAt`); the new pieces are
the held-item read across the plugin/handle boundary and the item-tag membership data.

**Table-stakes / differentiator:** TemptGoal is TABLE STAKES for breedable animals (it is how the
player leads/feeds them, and the same held-item read powers S3's love-mode-on-feed). The item-tag
infrastructure is reused by every animal.

---

## Passive mobs

All passive animals share the SAME 8-slot `registerGoals` skeleton as the already-shipped pig
(Float@0, Panic@1, Breed@2/3, Tempt@3/4, FollowParent@4/5, Stroll@5/6, LookAtPlayer@6/7,
LookAround@7/8) — only the speed modifiers, the tempt food tag, and a per-mob extra goal differ.
Once S1–S4 land, each is a thin Starlark plugin (the pig template generalizes directly).

| Feature | Jar chain (verified) | Shares with pig | Unique additions | Subsystem deps | Complexity | T/D |
|---|---|---|---|---|---|---|
| **Cow** | `animal.cow.AbstractCow.registerGoals` — Float@0, Panic(2.0)@1, Breed(1.0)@2, Tempt(1.25, COW_FOOD)@3, FollowParent(1.25)@4, Stroll(1.0)@5, LookAt(Player,6.0)@6, LookAround@7 | identical skeleton (one Tempt vs pig's two) | **Milking**: `mobInteract` with `Items.BUCKET` → `MILK_BUCKET` (confirmed in `AbstractCow.mobInteract`) | S1,S2,S3,S4 | LOW (after subsystems) | TABLE STAKES |
| **Sheep** | `animal.sheep.Sheep.registerGoals` — **EatBlockGoal field stored**, Float@1, Panic(1.25)@1, Breed(1.0)@2, Tempt(1.1)@3, FollowParent(1.1)@4, **EatBlockGoal@5**, Stroll(1.0)@6, LookAt(6.0)@7, LookAround@8 | same skeleton + Eat | **EatBlockGoal** (`ai.goal.EatBlockGoal` — eat grass, 40-tick animation, regrows wool + ages baby); **shearing** (`mobInteract` w/ shears → drop wool, set sheared) | S1,S2,S3,S4 + **EatBlockGoal** | MEDIUM (EatBlockGoal + shear/wool state) | TABLE STAKES (sheep exists) / wool-regrow & dye = DIFFERENTIATOR |
| **Chicken** | `animal.chicken.Chicken.registerGoals` — Float@0, Panic(1.4)@1, Breed(1.0)@2, Tempt(1.0)@3, FollowParent(1.1)@4, Stroll(1.0)@5, LookAt(6.0)@6, LookAround@7 | identical skeleton | **Slow-fall** (`aiStep`: clamp downward `getDeltaMovement` to `-0.6` when falling — confirmed `0.6d` const) + **egg-lay** (`aiStep` decrements `eggTime`; at 0 `spawnAtLocation(EGG)` + `SoundEvents.CHICKEN_EGG`, reset `eggTime = 6000 + nextInt(6000)`) | S1,S2,S3,S4 + aiStep hooks | MEDIUM (aiStep egg timer + fall clamp) | slow-fall = TABLE STAKES (iconic) / egg-lay = DIFFERENTIATOR |

**MobCategory:** all four (incl. pig) are `CREATURE` (cap 10 per the `10 * spawnableChunkCount / 289`
formula). `categoryOf` in `mob_category.go` must add `cow/sheep/chicken → categoryCreature` (today
only pig is mapped). Their attribute suppliers already exist in `level/attribute/defaults.go` (cow
10.0, sheep 8.0, chicken 4.0 — confirmed).

---

## Hostile mobs

Hostiles introduce a SECOND goal selector — the **targetSelector** (separate from goalSelector) —
holding `HurtByTargetGoal` + `NearestAttackableTargetGoal`. They depend on S2 (damage pipeline, both
directions). They are `MONSTER` category (cap 70) and spawn by light/dark rules (S5).

| Feature | Jar chain (verified) | goalSelector | targetSelector | Subsystem deps | Complexity | T/D |
|---|---|---|---|---|---|---|
| **Zombie** | `monster.zombie.Zombie.registerGoals` | **ZombieAttackGoal(1.0,false)@3** (NOT generic MeleeAttackGoal — modern zombie has its own), MoveThroughVillageGoal@6, WaterAvoidingRandomStroll@7, LookAt(Player,8.0)@8, LookAround@8; (Float + SpearUseGoal@1/2 added via helper) | HurtByTargetGoal(setAlertOthers)@1, NearestAttackableTargetGoal(Player)@2, (Villager/IronGolem)@3, (Turtle w/ selector)@5 | **Day burn**: `aiStep` → `isSunSensitive()` true + sky-exposed in daylight → `igniteForSeconds(...)` (confirmed `igniteForSeconds` in aiStep) | S1,S2,S5 | HIGH | TABLE STAKES |
| **Skeleton** | `monster.skeleton.AbstractSkeleton.registerGoals` — RestrictSunGoal@2, FleeSunGoal@3, AvoidEntity(Wolf,6.0,1.2)@3, WaterAvoidingRandomStroll@5, LookAt(Player,8.0)@6, LookAround@6 | HurtByTargetGoal@1, NearestAttackableTargetGoal(Player)@2, (IronGolem)@3 | **Bow attack** NOT in registerGoals — added dynamically in `reassessWeaponGoal()` (RangedBowAttackGoal w/ bow, else MeleeAttackGoal). **Day burn** (same `isSunSensitive` path) + RestrictSun/FleeSun | S1,S2,S5 | HIGH (ranged + weapon reassess + sun-flee + burn) | TABLE STAKES (melee-only acceptable v1; bow = differentiator) |
| **Spider** | `monster.spider.Spider.registerGoals` — Float@1, AvoidEntity(Armadillo,6.0)@2, LeapAtTargetGoal(0.4)@3, **Spider$SpiderAttackGoal@4** (inner melee), WaterAvoidingRandomStroll(0.8)@5, LookAt(Player,8.0)@6, LookAround@6 | **Spider$SpiderTargetGoal** (inner — targets only in darkness), HurtByTargetGoal@1, NearestAttackableTargetGoal@... | **Light-gated aggression** (SpiderTargetGoal: hostile only when dark) + LeapAtTarget pounce + wall-climb (CLIMBING flag) | S1,S2,S5 | HIGH (inner attack/target goals + leap + climb + light aggro) | TABLE STAKES (spider+melee+leap) / wall-climb = DIFFERENTIATOR |

**Notes:**
- All three use `NearestAttackableTargetGoal` (`ai.goal.target.NearestAttackableTargetGoal`) and
  `HurtByTargetGoal` (`ai.goal.target.HurtByTargetGoal`) — the SHARED target-selector primitives to
  build once. HurtByTargetGoal directly reads the S2 hurt source (triggers when hurt → targets the attacker).
- `MeleeAttackGoal` (`ai.goal.MeleeAttackGoal`) is the shared base for the wolf and the dynamic
  skeleton melee; zombie/spider use mob-specific subclasses with the same shape (path to target,
  swing in reach, deal damage via S2). Build `MeleeAttackGoal` once; the variants are thin.
- `categoryOf` must map `zombie/skeleton/spider → categoryMonster`. Attribute suppliers for
  zombie/skeleton/spider already exist in `defaults.go` (confirmed).

---

## Neutral mobs

| Feature | Jar chain (verified) | Subsystem deps | Complexity | T/D |
|---|---|---|---|---|
| **Wolf** | `animal.wolf.Wolf.registerGoals` — Float@1, **TamableAnimal$TamableAnimalPanicGoal(1.5)@1**, **SitWhenOrderedToGoal@2**, Wolf$WolfAvoidEntityGoal(Llama)@3, LeapAtTargetGoal(0.4)@4, **MeleeAttackGoal@5**, **FollowOwnerGoal(10.0)@6**, BreedGoal@7, WaterAvoidingRandomStroll@8, **BegGoal(8.0)@9**, LookAt(Player,8.0)@10, LookAround@10. targetSelector: **OwnerHurtByTargetGoal@1**, **OwnerHurtTargetGoal@2**, HurtByTargetGoal@3, NearestAttackableTargetGoal(Player)@4, **NonTameRandomTargetGoal(Animal)@5** | S1,S2,S3,S4 + **taming/sit/owner state** | HIGH (most complex single mob) | DIFFERENTIATOR (a tameable companion is flagship; a wild-only wolf is the table-stakes subset) |

**What's unique to wolf (build on top of S1–S4):**
- **Taming**: `TamableAnimal` owner-UUID state + `mobInteract` feeding bones → random tame chance.
- **Sitting**: `SitWhenOrderedToGoal` + the orderedToSit flag (toggled by interact when tamed).
- **Owner-follow**: `FollowOwnerGoal` (teleport/path to owner when far).
- **Owner-hurt anger**: `OwnerHurtByTargetGoal` (target whoever hit the owner) + `OwnerHurtTargetGoal`
  (target whoever the owner hit) — both read S2's hurt source.
- **Anger / wild aggression**: `NonTameRandomTargetGoal` (untamed wolves hunt animals);
  `HurtByTargetGoal` (anger when hit). Neutral-until-provoked = the "neutral mob" definition.
- **Wolf attribute supplier does NOT yet exist** in `defaults.go` (verified — must be added, ported
  from `Wolf.createAttributes`).

**Recommended wolf staging:** ship the *wild* wolf first (Float/Panic/Avoid/Leap/Melee/Stroll/Look +
HurtByTarget/NonTameRandomTarget) which needs only S1–S2 + the target primitives; add
tame/sit/owner/beg as a second pass (needs the TamableAnimal owner-state subsystem). This keeps the
keystone subsystems unblocking the most mobs before the wolf-specific state lands.

---

## S5 — MobCategory + spawn rules (hostile day/night/light)

**Jar chains (verified):**
- `net.minecraft.world.entity.MobCategory` `<clinit>` bytecode: `MONSTER` constructed with `max = 70`,
  `CREATURE` with `max = 10`. These match the already-shipped `mob_category.go` table exactly.
- `net.minecraft.world.entity.monster.Monster.checkMonsterSpawnRules(type, level, reason, pos, rng)`
  = `isDarkEnoughToSpawn(level, pos, rng) && checkMobSpawnRules(...)`.
- `Monster.isDarkEnoughToSpawn(level, pos, rng)`: reads `getBrightness(LightLayer.BLOCK, pos)` vs
  `DimensionType.monsterSpawnBlockLightLimit()`, then `getBrightness(...)` vs a sampled
  `DimensionType.monsterSpawnLightTest().sample(rng)` — i.e. monsters need block-light below a limit
  AND a (sky+block) light test below a random threshold (the classic "dark enough" check).
- `checkSurfaceMonstersSpawnRules` / `checkAnyLightMonsterSpawnRules` are the surface vs any-light
  variants used by specific monster types.

**What determines where/when each spawns:**
- **CREATURE** (cow/sheep/chicken/pig): spawn on grass in daylight, brightness > 0 (the v1 spawner
  already relaxes the light rule — documented in `spawner.go`). Cap = `10 * spawnableChunkCount / 289`.
- **MONSTER** (zombie/skeleton/spider): spawn in darkness (light limit check), at night or in unlit
  areas. Cap = `70 * spawnableChunkCount / 289` (7× the creature budget — why a cave/night fills with
  hostiles). Today the spawner ONLY spawns CREATURE (one Pig); adding MONSTER needs: the `categoryOf`
  MONSTER mapping (cap accounting already supports it via `maxInstancesPerChunk`), the
  `isDarkEnoughToSpawn` light gate (needs a light engine — the v1 spawner has none, documented
  relaxed), and a per-category spawn pass.

**Complexity:** MEDIUM–HIGH. The cap accounting + category formula already exist (`spawner.go`,
`mob_category.go`). The genuinely new work is the **light gate** (`isDarkEnoughToSpawn` needs block +
sky light the v1 light engine doesn't fully provide) and a **MONSTER spawn pass** distinct from the
single-Pig CREATURE pass. Without a light engine, a faithful `isDarkEnoughToSpawn` is the hard part —
a time-of-day-only gate (spawn hostiles at night) is a documented-relaxed v1 subset.

**Table-stakes / differentiator:** hostile spawning is TABLE STAKES for "a survival night exists".
Full light-propagation-driven cave spawning is a DIFFERENTIATOR (gated on a light engine).

---

## Feature Dependencies

```
S2 (mob damage pipeline + lastDamageSource)   <-- THE KEYSTONE
  ├──unblocks──> PanicGoal@1  ──> every passive mob (Float/Panic skeleton)
  ├──unblocks──> HurtByTargetGoal / OwnerHurt*  ──> every hostile + wolf
  └──unblocks──> MeleeAttackGoal (deal damage)  ──> zombie, skeleton, spider, wolf

S1 (JumpControl + fluid detect)
  └──unblocks──> FloatGoal@0  ──> ALL mobs (pig, cow, sheep, chicken, spider, wolf, ...)

S4 (held-item read + ItemTags)
  ├──unblocks──> TemptGoal@4  ──> pig, cow, sheep, chicken, wolf
  └──feeds────> S3 love-mode-on-feed

S3 (aging + breeding)  ──requires──> S4 (feeding reads held item)
  ├──unblocks──> BreedGoal@3       ──> pig, cow, sheep, chicken, wolf
  └──unblocks──> FollowParentGoal@5 ──> all breedable animals (babies)

S5 (MobCategory MONSTER + spawn light rules)
  └──unblocks──> natural spawning of zombie/skeleton/spider (hostiles)

Per-mob extras (each requires its mob's base goals + the subsystem(s) noted):
  EatBlockGoal ──> Sheep        (needs S1-S4 sheep base)
  shearing/wool ──> Sheep       (mobInteract + wool state)
  egg-lay/slow-fall ──> Chicken (aiStep hooks)
  milking ──> Cow               (mobInteract)
  taming/sit/owner ──> Wolf     (TamableAnimal owner-state, on top of S1-S4 + target primitives)
  day-burn ──> Zombie, Skeleton (isSunSensitive + igniteForSeconds in aiStep; needs S5/light or time gate)
```

### Dependency Notes
- **S2 is the single highest-leverage build.** It unblocks PanicGoal (all passives), the entire
  target-selector family (all hostiles), and melee damage (all hostiles + wolf). Build it first.
- **S3 requires S4** — love mode is entered by feeding, which reads the held item. Don't schedule S3
  before S4.
- **Hostiles require S2 + the target primitives + S5** — a zombie that can't path-and-hit (S2/melee)
  or can't be targeted/spawned is not a zombie. `MeleeAttackGoal`, `NearestAttackableTargetGoal`,
  `HurtByTargetGoal` are shared primitives — build once, reuse across all three hostiles + wolf.
- **FloatGoal (S1) is independent** of the others and is needed by literally every mob — it can be
  built in parallel with S2.

---

## MVP Definition

### Launch With (this milestone, in dependency order)

- [ ] **S1** JumpControl + fluid detection — unblocks FloatGoal for ALL mobs (independent, build early)
- [ ] **S2** mob damage pipeline + lastDamageSource + PANIC_CAUSES tag — the keystone (build first/parallel S1)
- [ ] **S4** held-item read + ItemTags (PIG_FOOD/COW_FOOD/CARROT_ON_A_STICK) — unblocks Tempt + feeds S3
- [ ] **S3** aging + breeding (age/inLove/canMate/spawnChild + FollowParent) — needs S4
- [ ] **Pig parity** — wire the pig's 5 deferred goals onto the existing plugin (the 1:1 dogfood completes; proves S1–S4)
- [ ] **Cow, Sheep, Chicken** plugins — thin once S1–S4 exist (sheep needs EatBlockGoal; chicken needs aiStep egg/fall; cow needs milking interact)
- [ ] **Shared hostile primitives** — `MeleeAttackGoal`, `NearestAttackableTargetGoal`, `HurtByTargetGoal` + targetSelector wiring (needs S2)
- [ ] **Zombie, Skeleton, Spider** plugins (melee-only subset acceptable for skeleton/spider v1)
- [ ] **S5** MONSTER category mapping + a hostile spawn pass (time-of-day gate if no light engine)
- [ ] **Wolf** — wild subset first (Float/Panic/Melee/target), then tame/sit/owner second pass

### Add After (this milestone or next)

- [ ] Skeleton **bow attack** (RangedBowAttackGoal + reassessWeaponGoal) — melee-only is the v1 subset
- [ ] Spider **wall-climb** (CLIMBING flag) + **light-gated aggression** (SpiderTargetGoal)
- [ ] Sheep **wool regrow + dye colors**; Chicken **egg-lay**; baby **growth particles**
- [ ] Full **light-propagation cave spawning** (`isDarkEnoughToSpawn` with a real light engine)
- [ ] Wolf **owner-hurt anger** full chain (OwnerHurtBy/OwnerHurt) + **BegGoal**

### Future Consideration (defer)

- [ ] Mob variants (Husk/Drowned/Stray/Bogged, CaveSpider, MushroomCow) — same skeletons, deferred
- [ ] `MoveThroughVillageGoal` (zombie@6) — needs village pathing context
- [ ] Brain/behavior-tree mobs (none of these six use Brain; all use goalSelector — fine for now)

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---|---|---|---|
| S2 mob damage pipeline | HIGH | HIGH | P1 (keystone) |
| S1 JumpControl + fluid | HIGH | MEDIUM | P1 |
| S4 held-item + ItemTags | MEDIUM | MEDIUM | P1 |
| S3 aging + breeding | HIGH | HIGH | P1 |
| Pig parity (5 deferred goals) | MEDIUM | LOW | P1 (dogfood gate) |
| Cow / Chicken plugins | HIGH | LOW | P1 |
| Sheep plugin (+EatBlockGoal) | HIGH | MEDIUM | P1 |
| Hostile target primitives | HIGH | HIGH | P1 |
| Zombie / Spider plugins | HIGH | HIGH | P1 |
| Skeleton plugin (melee subset) | HIGH | MEDIUM | P1 |
| S5 hostile spawn (time gate) | HIGH | MEDIUM | P2 |
| Wolf (wild subset) | MEDIUM | HIGH | P2 |
| Wolf taming/sit/owner | HIGH | HIGH | P2 |
| Skeleton bow / Spider climb | MEDIUM | MEDIUM | P3 |
| Light-engine cave spawning | MEDIUM | HIGH | P3 |
| Sheep wool-regrow / Chicken egg | LOW | LOW | P3 |

## Anti-Features (avoid)

| Feature | Why Requested | Why Problematic | Alternative |
|---|---|---|---|
| Faking a "was hurt" flag for PanicGoal | quick way to ship Panic without S2 | explicitly forbidden by 24-CONTEXT; produces non-vanilla behavior | build S2 (the real damage pipeline) first |
| Raw `vy` poke for FloatGoal jump | shortcut around JumpControl | not `JumpControl.jump()`; wrong impulse magnitude/timing | build the real JumpControl impulse + aiStep consume |
| Generic MeleeAttackGoal for zombie | "one melee goal for all" | zombie uses its own `ZombieAttackGoal`; spider uses `Spider$SpiderAttackGoal` — different reach/timing | build MeleeAttackGoal as the base, port the per-mob subclasses 1:1 |
| Brain/behavior-tree port for these mobs | "modern AI" | none of the six use Brain — all use goalSelector; a Brain port is unused scope | stick to goalSelector (matches the jar + the shipped pig) |
| Spawning hostiles without category/cap mapping | "just spawn zombies at night" | bypasses the MONSTER cap (70×count/289) → flood (the v1 Pitfall 3 the spawner exists to prevent) | extend `categoryOf` to MONSTER; reuse the existing cap formula |

## Confidence Assessment

| Area | Level | Reason |
|---|---|---|
| FloatGoal / PanicGoal / TemptGoal bytecode | HIGH | full disassembly read this session (canUse/tick/ctor) |
| BreedGoal / FollowParentGoal / Animal aging | HIGH | method signatures + canUse/breed bytecode disassembled |
| Cow/Sheep/Chicken/Zombie/Skeleton/Spider/Wolf registerGoals | HIGH | each registerGoals disassembled this session (goal list + priorities + ctor args) |
| MobCategory caps (MONSTER 70 / CREATURE 10) | HIGH | `<clinit>` constants read + cross-checked against shipped `mob_category.go` |
| Monster spawn light rules | HIGH (mechanism) / MEDIUM (port feasibility) | `isDarkEnoughToSpawn`/`checkMonsterSpawnRules` disassembled; feasibility MEDIUM because a faithful version needs a light engine the v1 spawner lacks |
| Chicken egg/slow-fall, Zombie sun-burn | HIGH | `aiStep` constants (0.6, eggTime, CHICKEN_EGG, igniteForSeconds, isSunSensitive) confirmed |
| Existing codebase seams | HIGH | read directly (`defaults.go`, `mob_category.go`, `spawner.go`); wolf supplier confirmed ABSENT |

## Sources

- `temp/cache/26.2-inner.jar` via `javap -p -c` (Zulu 25) — ALL behavioral claims; classes cited inline
- `.planning/milestones/v4-phases/24-vanilla-mobs-as-plugins/deferred-goals.md` — the deferred-goal audit (S1–S4 prerequisites)
- `plugins/vanilla_pig/main.star` — the 1:1 plugin template the new passive mobs generalize from
- `server/ai_goals_passive.go` — the shipped Go oracle for the 3 passive goals (draw-order reference)
- `server/spawner.go`, `server/mob_category.go` — the shipped NaturalSpawner cap logic (CREATURE 10, MAGIC_NUMBER 289) + category table (MONSTER 70)
- `level/attribute/defaults.go` — existing attribute suppliers (cow/sheep/chicken/zombie/skeleton/spider present; wolf absent)

---
*Feature research for: Sulfur v5 mob subsystems + mob types*
*Researched: 2026-06-29*
