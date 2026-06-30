# Phase 35: Hostiles + Spawn Rules (zombie / skeleton / spider) - Pattern Map

**Mapped:** 2026-06-30
**Files analyzed:** 18 (new + modified)
**Analogs found:** 18 / 18 (every file has a direct Phase-29/31/34 analog in the tree)

This phase is overwhelmingly an EXTEND-EXISTING-PATTERN phase, not a green-field one. The mob-as-plugin
machinery (registry / boot-load / spawn / declare_mob+goal routing / categoryOf / countByCategory /
serverAiStep order) ALL exist from Phases 24/29/31/34; Phase 35 adds three new things on top of them:
1. a SECOND goalSelector instance (the targetSelector) ticked in jar order on `mobAI`,
2. the shared combat/target goals (MeleeAttackGoal / NearestAttackableTargetGoal / HurtByTargetGoal),
3. the MONSTER cap + day/night gate + 3 new vanilla_<mob> plugin pairs.

The ai_mob.go serverAiStep doc ALREADY documents the exact jar tick order including the targetSelector
seam (`sensing -> targetSelector.tick -> goalSelector.tick -> targetSelector.tickRunningGoals(true) ->
goalSelector.tickRunningGoals(true)`) — the seam is commented, not yet wired. Phase 35 wires it live.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `server/ai_mob.go` (MODIFY) | runtime/AI driver | event-driven | self (serverAiStep + newPigAI) | exact (self-extend) |
| `server/ai_goal.go` (REUSE, maybe MODIFY) | runtime/AI selector | event-driven | self (goalSelector type) | exact (instantiate twice) |
| `server/ai_goals_target.go` (NEW) | AI goal port | event-driven | `server/ai_goals_passive.go` (lookAtPlayerGoal) | role-match |
| `server/ai_goals_attack.go` (NEW) | AI goal port | request-response (melee) | `server/ai_goals_panic.go` + `combat_mob.go` | role-match |
| `server/combat_mob.go` (MODIFY) | combat keystone | request-response | self (applyDamageEntity) | exact (self-extend) |
| `server/entity.go` (MODIFY) | model/state | — | self (lastDamageSource/breedAge fields) | exact (self-extend) |
| `server/mob_category.go` (MODIFY) | config/value map | CRUD (lookup) | self (categoryOf cow/sheep/chicken) | exact (self-extend) |
| `server/spawner.go` (MODIFY) | service/spawn | batch | self (countByCategory + creatureCap) | exact (self-extend) |
| `server/async.go` (MODIFY) | service/spawn rejoin | event-driven | self (spawnCandidatesReady.applyTo + pickNaturalCreatureMob) | exact (self-extend) |
| `server/vanilla_hostile.go` or extend `vanilla_pig.go` | registry/spawn | CRUD | `server/vanilla_pig.go` (spawnVanillaMob) | exact |
| `server/vanilla_pig_embed.go` (MODIFY) | boot-load/embed | file-I/O | self (loadVanillaMobRegistry + vanillaMobNames) | exact (self-extend) |
| `server/commands_dbg.go` (MODIFY) | command dispatcher | request-response | self (`case "cow"`) | exact (self-extend) |
| `plugins/vanilla_zombie/main.star` (NEW) | plugin/mob decl | declarative | `plugins/vanilla_cow/main.star` + `vanilla_pig/main.star` | exact (template) |
| `plugins/vanilla_skeleton/main.star` (NEW) | plugin/mob decl | declarative | `plugins/vanilla_cow/main.star` | exact (template) |
| `plugins/vanilla_spider/main.star` (NEW) | plugin/mob decl | declarative | `plugins/vanilla_cow/main.star` | exact (template) |
| `plugins/vanilla_<mob>/plugin.toml` ×3 (NEW) | plugin manifest | config | `plugins/vanilla_cow/plugin.toml` | exact (template) |
| `server/assets/vanilla_<mob>/{main.star,plugin.toml}` ×3 (NEW) | embedded asset | config | `server/assets/vanilla_cow/*` | exact (byte-identical copy) |
| `server/plugin_mob_decl.go` (LIKELY NO CHANGE) | plugin routing | event-driven | self (parseGoalFlags TARGET + baseTypeByName) | exact (already supports it) |
| `server/hostiles_test.go` + RNG tests (NEW) | test | — | `server/cow_test.go` + `ai_goals_panic_test.go` | role-match |

## Pattern Assignments

### `server/ai_mob.go` — MODIFY (the targetSelector machinery, SC#1)

**Analog:** self — the existing `serverAiStep` (lines 145-210) and `newPigAI` (lines 245-292).

**The seam is ALREADY documented.** The serverAiStep doc comment (lines 3-13, 145-154, 166) spells out the
exact jar tick order and explicitly marks targetSelector as "intentionally SKIPPED" for a passive pig.
Phase 35 un-skips it. The pattern to copy:

**The second goalSelector field** — add a `targetSelector goalSelector` field to `mobAI` (lines 29-89),
sibling of the existing `goals goalSelector` (line 31). Both are the SAME `goalSelector` type from
ai_goal.go — the targetSelector is a SECOND INSTANCE, not a new type.

**The jar-order tick** — the existing serverAiStep body (lines 155-210) currently runs only
`m.goals.tick(t,e)` + `m.goals.tickRunningGoals(t,e,true)` (lines 167-168). Replace the skipped-seam
comment (lines 165-166) with the live order, exactly as the file's own header documents it:
```go
// (sensing.tick — skipped: the v1 goals probe the world directly in their canUse.)
m.targetSelector.tick(t, e)                  // run the target goals FIRST (jar order)
m.goals.tick(t, e)                           // then the action goals
m.targetSelector.tickRunningGoals(t, e, true)
m.goals.tickRunningGoals(t, e, true)
```
NOTE: `goalSelector.tick` (ai_goal.go:271) ALREADY calls `tickRunningGoals` at its tail, so verify whether
the explicit trailing `tickRunningGoals` calls double-tick. The existing code (line 168) calls it
explicitly AFTER `tick` — matching vanilla's `goalSelector.tick()` then `tickRunningGoals(canSimulate)`.
Preserve that exact shape for BOTH selectors.

**The per-hostile AI builder** — `newPigAI` (lines 245-292) is the template for any Go-native hostile AI
builder (if a Go-native zombie is built for the per-hostile test alongside the .star). Each `addGoal(prio,
goal)` line maps to a registerGoals entry; the TARGET-flag goals go to `m.targetSelector.addGoal(...)`
instead of `m.goals.addGoal(...)`. But note: the production hostiles are .star-driven, so buildAIFromDecl
(below) is the real builder — newPigAI is the shape reference.

---

### `server/ai_goal.go` — REUSE (the targetSelector is a second instance of this type)

**Analog:** self — `goalSelector` (lines 140-145) is already a self-contained type instantiated per-field.

**No new type needed.** The targetSelector is `goalSelector{}` again. The `flagTarget` bit ALREADY exists
(line 42: `flagTarget // Goal$Flag.TARGET — attack-target selection (targetSelector)`). The arbitration
(`canBeReplacedBy`, `goalCanBeReplacedForAllFlags`, lines 113-191) works unchanged on a second instance —
TARGET goals lock the TARGET flag among themselves in the targetSelector, MOVE/LOOK goals lock theirs in
the goalSelector. The two selectors do NOT share a lock map (each `goalSelector` carries its own `lockedBy`,
line 142), which is exactly vanilla (each GoalSelector has independent lockedFlags).

POSSIBLE MODIFY: confirm nothing assumes a single selector per mob (grep for `e.ai.goals` direct accesses
that should NOT also hit the targetSelector). The type itself needs no change.

---

### `server/ai_goals_target.go` — NEW (NearestAttackableTargetGoal + HurtByTargetGoal, SC#1)

**Analog:** `server/ai_goals_passive.go` (the goal-port file pattern) — specifically `lookAtPlayerGoal`
(its `nearestPlayerWithin`/`nearestPlayerAt` scan, lines 300, 342-350) for the target acquisition, and
`combat_mob.go` for the `lastDamageSource`/`hasLastDamage` read.

**Goal-port file structure** (copy from ai_goals_passive.go:1-63): package header citing the jar class,
`mobRandom(e)` helper (lines 44-53) for the per-entity RNG, a concrete struct embedding `baseGoal`
(ai_goal.go:88-101), `canUse`/`canContinueToUse`/`start`/`stop`/`tick` methods.

**NearestAttackableTargetGoal.canUse** — the JARNOTES-confirmed bytecode (35-JARNOTES.md:97-109):
```
if (randomInterval > 0 && mob.getRandom().nextInt(randomInterval) != 0) return false;  // RNG GATE
findTarget();                                          // AABB scan w/ TargetingConditions
return target != null;
```
The RNG gate is the EXACT shape of the stroll/lookAround gate already ported in ai_goals_passive.go and
vanilla_pig/main.star:360 (`if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0: return False`). Reuse that
draw discipline. `randomInterval = reducedTickDelay(10)` — CONFIRM the exact ctor value at exec (JARNOTES
flags this as lockstep-critical; adjustedTickDelay stays IDENTITY per Phase-34, so the bound is the full
value run every tick). The `findTarget` AABB scan REUSES `nearestPlayerAt` (ai_goals_passive.go:350, the
position-based core factored out of `nearestPlayerWithin`) bounded by the FOLLOW_RANGE attribute (read via
`e.getAttributeValue(attribute.FollowRange)` — the same accessor combat_mob.go:426 uses for
KnockbackResistance).

**HurtByTargetGoal.canUse** — JARNOTES-confirmed (35-JARNOTES.md:84-92), NO RNG:
```
int timestamp = mob.getLastHurtByMobTimestamp();
LivingEntity last = mob.getLastHurtByMob();
if (timestamp == this.timestamp || last == null) return false;
... canAttack(last, HURT_BY_TARGETING);
```
This needs the `lastHurtByMob` / `lastHurtByMobTimestamp` fields on the entity — which do NOT yet exist
(grep confirmed zero hits). Phase 31's `lastDamageSource`/`hasLastDamage` (entity.go:205-219) are
DAMAGE-SOURCE tracking for PanicGoal, NOT the attacker-ENTITY bookkeeping HurtByTargetGoal needs. The
`damageSource` struct DOES carry `attacker int32` (damage_source.go:71) — so the entity.go extension is to
record `lastHurtByMob = src.attacker` + `lastHurtByMobTimestamp = t.gametime` at the SAME flag2 store-point
combat_mob.go:110-117 already sets `lastDamageSource`/`hasLastDamage`. See the entity.go + combat_mob.go
entries below.

**The target field write** — both goals set `e.ai.attackTargetID` (the new thin-id field, the
`Mob.getTarget()` analogue) on a successful acquire; MeleeAttackGoal canUse-gates on it being non-zero.

---

### `server/ai_goals_attack.go` — NEW (MeleeAttackGoal + ZombieAttackGoal/SpiderAttackGoal + LeapAtTargetGoal, SC#2)

**Analog:** `server/ai_goals_panic.go` (a goal that drives navigation toward a target + a cooldown) for the
structure, and `combat_mob.go` `applyDamageEntity` (lines 53-144) for the damage application keystone.

**MeleeAttackGoal core** (decompile at exec — JARNOTES OPEN list line 124, 146): path to target, swing when
in reach (`getMeleeAttackRangeSqr`), attack cooldown, then `hurt(target, ATTACK_DAMAGE)`. The path-to-target
half copies the panic/stroll nav-want pattern: a goal SETS `setWantTarget(tx,ty,tz)` (ai_mob.go:119-124) and
the existing navigation.tick steps the mob. It NEVER moves the mob itself (the 07-RESEARCH Pattern 1 rule the
whole goal layer obeys).

**The damage application** routes through the Phase-29 keystone: `t.applyDamageEntity(targetEntity, src,
attackDamage)` — but the target here is a PLAYER, so it routes through the *tickPlayer* combat path
(combat.go), the sibling of combat_mob.go. The `damageSource` is built with `attacker = mob.id`
(damage_source.go:66-71), so the player's own damage path sets the right source. CONFIRM the mob-attacks-
player direction uses the player hurt path (combat.go), not applyDamageEntity (which is mob-victim).

**The attack cooldown** is a per-goal int field (the MeleeAttackGoal `ticksUntilNextAttack`), an RNG-FREE
countdown — same shape as FollowParentGoal's `timeToRecalcPath` (vanilla_pig/main.star:308-318) and
TemptGoal's `calmDown`. The focused RNG test (SC#5) covers the cooldown timing.

**LeapAtTargetGoal (Spider, @3)** — HAS RNG: `nextFloat`-gated leap impulse (JARNOTES:127, 140). The
nextFloat draw is the EXACT shape of FloatGoal.tick (vanilla_pig/main.star:94: `if entity.rand_float() <
FLOAT_JUMP_PROBABILITY`). The leap sets a vertical+horizontal velocity impulse (like knockbackEntity's
setDeltaMovement, combat_mob.go:460-466). Lockstep-test the nextFloat gate (SC#5).

**ZombieAttackGoal / SpiderAttackGoal** are MeleeAttackGoal subclasses (JARNOTES:26,63). In Go, model as the
base MeleeAttackGoal with a parameter/override (SpiderAttackGoal "won't attack in daylight" —
JARNOTES:63 — gates on the same day/night proxy the spawn gate uses; see spawner.go below).

---

### `server/combat_mob.go` — MODIFY (the lastHurtByMob bookkeeping for HurtByTargetGoal)

**Analog:** self — the flag2 store-point (lines 110-117) where `lastDamageSource`/`hasLastDamage` are
already recorded.

**Add the attacker-entity bookkeeping at the SAME site.** The file's own doc already anticipates this
(line 108: "lastDamageStamp (the gameTime) is not modeled here (no consumer yet) — it slots in alongside
this store when a reader lands"). Phase 35 IS that reader (HurtByTargetGoal). Inside the `if flag2` block
(lines 110-117), add:
```go
e.lastHurtByMob = src.attacker            // LivingEntity.lastHurtByMob (0 if environmental)
e.lastHurtByMobTimestamp = int32(t.gametime) // LivingEntity.lastHurtByMobTimestamp
```
`src.attacker` is the causing entity (damage_source.go:71). `t.gametime` is the per-tick counter
(block_break.go:230 reads it the same way). This is PURE field writes — no RNG, so it cannot perturb the pig
oracle (which deals no damage, PITFALLS Pitfall 5 — the same argument combat_mob.go:166-169 makes for the
voice-pitch draws).

---

### `server/entity.go` — MODIFY (the attacker-bookkeeping fields + the attackTargetID seam)

**Analog:** self — the existing `lastDamageSource damageSource` / `hasLastDamage bool` fields (lines
205-219) and `breedAge int` (lines 256-269).

**Add three fields**, each cited to the jar field it ports, in the same doc-comment style:
- `lastHurtByMob int32` — `LivingEntity.lastHurtByMob` (the attacker entity id; 0 == null).
- `lastHurtByMobTimestamp int32` — `LivingEntity.lastHurtByMobTimestamp` (the tick stamp, vs HurtByTargetGoal's own timestamp).
- The `attackTargetID int32` thin-id field (the `Mob.getTarget()` analogue) lives on `mobAI` (ai_mob.go),
  NOT the Entity, per CONTEXT line 74 ("an attackTargetID thin-id field on the entity carries the acquired
  target") — place it on mobAI alongside the existing `wantX/wantY/wantZ` nav-want fields (ai_mob.go:44-45),
  since it is AI state, tick-owned, the same single-owner discipline.

`isBaby()` (entity.go:449) is the field-accessor pattern to copy for any `getTarget()`-style helper.

---

### `server/mob_category.go` — MODIFY (the 3 MONSTER cases — the Phase-34 CREATURE-fix pattern)

**Analog:** self — `categoryOf` (lines 82-104), specifically the cow/sheep/chicken cases (lines 86-100).

**Add three cases, byte-for-byte the same shape as the cow case** (lines 87-91):
```go
case entity.Zombie.ID:
    return categoryMonster
case entity.Skeleton.ID:
    return categoryMonster
case entity.Spider.ID:
    return categoryMonster
```
`entity.Zombie`/`Skeleton`/`Spider` all exist (entity.go:1379/1055/1136) with `Type: "monster"`
(confirmed entity.go:1143, 1386) — the jar-derived `EntityType.category` the cite references.
`maxInstancesPerChunk` ALREADY returns 70 for `categoryMonster` (lines 55-56) — NO change to the cap table.
This is purely additive: the pig/cow/sheep/chicken CREATURE accounting is untouched (the pig oracle's
category accounting is unperturbed, exactly as the cow case note line 87-90 argues).

---

### `server/spawner.go` — MODIFY (the MONSTER cap + the day/night gate, SC#3 + SC#4)

**Analog:** self — `creatureCap` (lines 83-90), `countByCategory` (lines 92-106), and the cap gate in
`naturalSpawn` (lines 315-395).

**The MONSTER cap** — clone `creatureCap` (lines 88-90) to `monsterCap`:
```go
func monsterCap(spawnableChunkCount int) int {
    return categoryMonster.maxInstancesPerChunk() * spawnableChunkCount / spawnMagicNumber
}
```
Same `/289` MAGIC_NUMBER divisor (line 80) — `maxInstancesPerChunk()` returns 70 for MONSTER vs 10 for
CREATURE, so the formula is identical, only the category differs.

**countByCategory ALREADY tallies MONSTER** — it ranges every entity and buckets by `categoryOf(e.typ)`
(lines 102-104). Once categoryOf returns categoryMonster for the 3 (above), `countByCategory[categoryMonster]`
is correct with ZERO change to spawner.go's counting. Same for `countByCategoryAcrossRegions` (lines 113-124).

**The day/night light gate (THE FORCED DECISION, SC#4)** — add an `isDarkEnoughToSpawn` stub gating the
MONSTER spawn pass. NO light read exists (CONTEXT:42-48, confirmed: no light engine). Gate on `t.gametime`
(the day/night clock the server already ticks — the same field block_break.go:230 reads). The vanilla day
is 24000 ticks; night is the dayTime-in-night window (~13000..23000). Structure it as a clearly-cited stub
that EQUALS the vanilla night-time default and becomes a real sky-light read when lighting lands:
```go
// isDarkEnoughToSpawn — THE FORCED PROXY (35-CONTEXT SC#4): no light engine exists, so gate hostile
// spawns on the day/night gametime window instead of the real Monster.isDarkEnoughToSpawn sky+block
// light read. Becomes a real light read when the lighting engine lands. Cite Monster.isDarkEnoughToSpawn.
func (t *TickLoop) isDarkEnoughToSpawn() bool {
    dayTime := t.gametime % dayLengthTicks   // dayLengthTicks = 24000
    return dayTime >= nightStartTicks && dayTime < nightEndTicks
}
```
CONFIRM whether a `dayTime`/`timeOfDay` is sent to the client yet (grep found none — t.gametime is the only
clock); if the world has no separate dayTime, `gametime % 24000` IS the time-of-day proxy. The
`checkMonsterSpawnRules` / `checkSurfaceMonstersSpawnRules` predicates (JARNOTES:111-117, OPEN decompile)
collapse for v1 to: this dark gate + the existing ON_GROUND standable check (`findStandableY`, lines
185-197). REUSE findStandableY verbatim for the MONSTER pass.

**The naturalSpawn MONSTER pass** — the CREATURE gate (lines 328-350) is the template: compute the cap,
read the live count, return if at/over. Add a sibling MONSTER pass gated additionally by
`isDarkEnoughToSpawn()`. The single-in-flight + snapshot + off-tick scan machinery (lines 358-395) is
category-agnostic — REUSE it; the only deltas are `monsterCap` and the dark gate.

---

### `server/async.go` — MODIFY (the MONSTER spawn rejoin + species pick, SC#3)

**Analog:** self — `spawnCandidatesReady.applyTo` (lines 294-344), `naturalCreatureMobNames` (lines
352-357), and `pickNaturalCreatureMob` (lines 359+).

**The species pick** — `pickNaturalCreatureMob` (lines 359+) draws uniform-random among the 4 CREATURE
mobs from the per-region seeded `levelRandom` (lines 360-380). Clone to `pickNaturalMonsterMob` over a new
`naturalMonsterMobNames = {vanilla_zombie, vanilla_skeleton, vanilla_spider}` slice (the exact shape of
`naturalCreatureMobNames`, lines 352-357 — kept its own explicit list so a non-monster bundled mob is never
dragged in).

**The apply-time cap re-check** — `applyTo` (lines 294-344) re-reads the live count and drops if at/over cap
(lines 313-317). For the MONSTER pass, the re-check uses `countByCategoryAcrossRegions()[categoryMonster]`
vs `monsterCap(...)` — the exact shape of the CREATURE re-check (line 314), only the category + cap-fn
differ. The category being placed must be carried on the `spawnCandidatesReady` message (currently CREATURE
is implicit) — add a category/picker field so applyTo re-checks the RIGHT cap. The `mobNearAcrossRegions`
packing guard (line 323) and the `spawnVanillaMob(name, ...)` placement (line 340) are reused verbatim.

---

### `server/vanilla_pig.go` — REUSE (spawnVanillaMob already name-parameterized)

**Analog:** self — `spawnVanillaMob(name, x, y, z)` (lines 51-62) is ALREADY name-parameterized (the Plan
34-04 generalization). It looks up `t.mobRegistry.byName[name]` and delegates to `spawnDeclaredMob`.

**No new spawn function needed.** `t.spawnVanillaMob("vanilla_zombie", x, y, z)` works the moment the
declaration boot-loads. The MONSTER-name constants (parallel to `vanillaPigMobName` etc.,
vanilla_pig_embed.go:38-43) are the only additions — put them next to their embed (below).

---

### `server/vanilla_pig_embed.go` — MODIFY (boot-load the 3 hostile plugins)

**Analog:** self — `vanillaMobNames` (lines 48-53), the `//go:embed` directive (line 32), the name
constants (lines 38-43), and `loadVanillaMobRegistry` (lines 66-90).

**Extend the embed directive** (line 32) to add the 3 hostile asset dirs:
```go
//go:embed assets/vanilla_pig assets/vanilla_cow assets/vanilla_sheep assets/vanilla_chicken assets/vanilla_zombie assets/vanilla_skeleton assets/vanilla_spider
```
**Add 3 name constants** (after line 43, the same `const` block):
```go
vanillaZombieMobName   = "vanilla_zombie"
vanillaSkeletonMobName = "vanilla_skeleton"
vanillaSpiderMobName   = "vanilla_spider"
```
**Append the 3 to `vanillaMobNames`** (lines 48-53) so they boot-load into the ONE registry. The
per-mob materialize/caps/LoadDirWith loop (`loadOneVanillaMob`, lines 96-117) and the LOUD-failure
re-assertion (lines 84-88) are category-agnostic — they handle the 3 new mobs with ZERO change. This is the
EXACT extension Plan 34-04 made when it added cow/sheep/chicken to the pig-only loader.

---

### `server/commands_dbg.go` — MODIFY (the /dbg spawn levers)

**Analog:** self — `case "cow"` (lines 20-24).

**Add 3 cases, byte-for-byte the cow shape:**
```go
case "zombie":
    e := t.spawnVanillaMob(vanillaZombieMobName, p.x, p.y, p.z)
    if e != nil { t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned zombie eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z)) }
```
(and skeleton/spider). Update the usage string (line 45) to list them.

---

### `plugins/vanilla_zombie/main.star` (+ skeleton, spider) — NEW

**Analog:** `plugins/vanilla_cow/main.star` (the cleanest template — header lines 1-28, the goal-callback
+ constant + `declare_mob` block structure) and `plugins/vanilla_pig/main.star` (the FULL multi-goal
reference incl. the nav `path_to` candidate-emit pattern, lines 359-378).

**The DOGFOOD pattern continues** (CONTEXT:113, JARNOTES:132-134): each hostile is a `declare_mob` plugin
with goal callbacks. The pig/cow goal callbacks are "mob-agnostic — they read entity.* handles — so they are
COPIED VERBATIM; only the constants + the mob-declaration block + the tempt food handle differ" (cow header
line 6-8). For the SHARED passive goals (WaterAvoidingRandomStrollGoal, LookAtPlayerGoal,
RandomLookAroundGoal, FloatGoal-Spider@1) the .star callbacks are COPIED VERBATIM from vanilla_cow/pig.

**The NEW goal callbacks** (the real work) — the TARGET-flag goals (HurtByTargetGoal,
NearestAttackableTargetGoal) and the attack goals (ZombieAttackGoal/SpiderAttackGoal/MeleeAttackGoal,
LeapAtTargetGoal). These declare goals with `flags = ["TARGET"]` (routed to the targetSelector by
buildAIFromDecl — see below) vs `["MOVE"]`/`["MOVE","LOOK"]` for the attack goals. The RNG draws
(NearestAttackableTargetGoal `rand_int(randomInterval)` gate, LeapAtTargetGoal `rand_float`, MeleeAttackGoal
cooldown) use the SAME `entity.rand_int`/`entity.rand_float` seams the pig stroll/float goals use
(vanilla_pig/main.star:360, 94). The draw-ORDER comments must cite the jar bytecode the same way the pig
file does.

**The declare_mob block** (the template is vanilla_pig/main.star:479-583) — the goal set + priorities +
flags from 35-CONTEXT:50-59:
- Zombie: goalSelector ZombieAttackGoal@3 + WaterAvoidingRandomStroll@7 + LookAtPlayer@8 + LookAround@8;
  targetSelector HurtByTargetGoal@1 + NearestAttackableTargetGoal<Player>@2.
- Skeleton (melee subset): goalSelector WaterAvoidingRandomStroll@5 + LookAtPlayer@6 + LookAround@6;
  targetSelector HurtByTargetGoal@1 + NearestAttackableTargetGoal<Player>@2.
- Spider: goalSelector FloatGoal@1 + LeapAtTargetGoal@3 + SpiderAttackGoal@4 + WaterAvoidingRandomStroll@5
  + LookAtPlayer@6 + LookAround@6; targetSelector HurtByTargetGoal@1 + SpiderTargetGoal<Player>@2.

**The attributes dict** (the template is vanilla_pig/main.star:482-485) — from 35-CONTEXT:62-65:
zombie {max_health 20 (Mob base), movement_speed 0.23, follow_range 35.0, attack_damage 3.0, armor 2.0};
skeleton {movement_speed 0.25}; spider {max_health 16.0, movement_speed 0.3}. NOTE: `seedAttributes`
(plugin_mob_decl.go:152-165) + `attrAlias` (lines 139-142) currently alias ONLY max_health/movement_speed —
the new attrs (follow_range, attack_damage, armor) need alias entries added if they are to seed (see the
plugin_mob_decl.go note below).

---

### `plugins/vanilla_<mob>/plugin.toml` ×3 — NEW

**Analog:** `plugins/vanilla_cow/plugin.toml` (the full manifest, 12 lines).

The hostile manifests likely need a WIDER cap set than the cow's `["entities.read", "entities.write",
"world.read", "nav"]` (cow plugin.toml:12) — the attack goals' player-damage call is a HOST-side op (like
the cow's milking, NOT a plugin op), so confirm whether dealing damage needs a new capability or routes
through a host handle (preferred — keep least-privilege). `nearest_player`/`set_look_at`/`path_to` reuse
the cow's existing caps. Cite the same least-privilege reasoning the cow manifest does (lines 5-11).

---

### `server/assets/vanilla_<mob>/{main.star,plugin.toml}` ×3 — NEW (byte-identical embed copies)

**Analog:** `server/assets/vanilla_cow/*` — the embedded copy of the repo-root plugin.

**Keep each repo-root/embed pair byte-identical** (vanilla_pig_embed.go:26-30 states this invariant: "Keep
each repo-root/embed pair byte-identical. The embedded manifest governs the swap's caps"). The embed copy is
the source-of-truth for the SWAP; the repo-root copy is operator-facing. There is likely a test asserting
byte-identity (grep `plugin_pig_race_test`-style or a dedicated embed-vs-root gate) — mirror it for the 3.

---

### `server/plugin_mob_decl.go` — LIKELY NO CHANGE (already supports TARGET + the hostile base types)

**Analog:** self — `flagByName` (lines 194-199), `baseTypeByName` (lines 108-121), `parseGoalFlags`
(lines 204-221).

`"TARGET": flagTarget` is ALREADY in `flagByName` (line 198). `skeleton`, `spider`, `zombie` are ALREADY in
`baseTypeByName` (lines 113-116). So a `goal(flags=["TARGET"], ...)` declaration parses today.

**The one possible change** — buildAIFromDecl (plugin_mob_ai.go:185-208) currently routes EVERY goal to
`m.goals.addGoal` (line 192). Phase 35 must route TARGET-flag goals to `m.targetSelector.addGoal` instead.
That is a 3-line branch in buildAIFromDecl (the REAL routing change, CONTEXT:72):
```go
if gd.flags&flagTarget != 0 {
    m.targetSelector.addGoal(gd.priority, &starlarkGoal{...})
} else {
    m.goals.addGoal(gd.priority, &starlarkGoal{...})
}
```
Also: `seedAttributes`/`attrAlias` (plugin_mob_decl.go:139-165) alias only max_health/movement_speed — add
follow_range/attack_damage/armor aliases so the hostile attribute dicts seed (else they are silently skipped,
lines 156-164 — the GetInstance==nil skip). CONFIRM each attr exists in the monster attribute supplier.

---

### `server/hostiles_test.go` + RNG tests — NEW

**Analog:** `server/cow_test.go` (the per-mob behavior gate, lines 1-55 — the `loadVanillaCowRegistry`
temp-dir harness + spawn + goal-set + behavior assertions) and `server/ai_goals_panic_test.go` (the focused
RNG-goal test shape).

**Per-hostile behavior test** — clone `cow_test.go`'s `loadVanilla<Mob>Registry` harness (lines 36-55) for
each hostile: materialize the repo-root plugin to a temp dir, load with the declare_mob/goal builtins, spawn,
assert the goal set + a target-acquire + a melee-hit behavior. NOT a bit-exact Go-vs-plugin oracle (the pig
proved the subsystems — CONTEXT:91-92, cow_test.go:6-9 makes the same "lighter gate" argument).

**Focused RNG tests** (SC#5) — for each new RNG goal (NearestAttackableTargetGoal `nextInt(randomInterval)`
gate, LeapAtTargetGoal `nextFloat`, MeleeAttackGoal cooldown), a focused lockstep test in the
ai_goals_panic_test.go style (seed the per-entity rng, assert the draw count + the gate outcome). The pig
oracle (`TestPluginPigEqualsGoNativePig`) STAYS UNTOUCHED — hostiles are separate mobs, so their RNG never
touches the pig path.

## Shared Patterns

### The per-entity RNG draw discipline (lockstep)
**Source:** `server/ai_goals_passive.go:44-53` (`mobRandom(e)`) + `plugins/vanilla_pig/main.star:360,94`
(`entity.rand_int`/`entity.rand_float`).
**Apply to:** every new RNG goal (NearestAttackableTargetGoal gate, LeapAtTargetGoal, MeleeAttackGoal
cooldown if RNG). Draw from `mobRandom(e)` (== `e.ai.rng`, the `Mob.getRandom()` analogue) in the EXACT
bytecode draw order, with a cite comment. adjustedTickDelay stays IDENTITY (CONTEXT:91 — the Go driver ticks
every tick, so `reducedTickDelay(10)` bounds run at full value).

### The "a goal SETS a target, it does not move the mob" rule
**Source:** `server/ai_mob.go:119-128` (`setWantTarget`/`clearWantTarget`) + the serverAiStep nav consumer
(lines 190-198).
**Apply to:** MeleeAttackGoal (paths to the player target via setWantTarget), LeapAtTargetGoal (sets a
velocity impulse like knockbackEntity, combat_mob.go:460-466 — the ONE exception, an impulse not a path).
The navigation.tick already consumes the want; the attack goals never call moveEntity directly.

### The damage keystone (Phase 29)
**Source:** `server/combat_mob.go:53-144` (`applyDamageEntity`) + `server/damage_source.go:66-78`
(`damageSource{attacker}` + `.is(tag)`).
**Apply to:** MeleeAttackGoal's hit (build a `damageSource{attacker: mob.id}`, route through the PLAYER hurt
path since the victim is a player), and the combat_mob.go flag2 store-point extension (record
lastHurtByMob = src.attacker for HurtByTargetGoal).

### The Phase-34 additive-case pattern (categoryOf / vanillaMobNames / dbg / embed)
**Source:** `server/mob_category.go:87-100`, `server/vanilla_pig_embed.go:48-53`, `server/commands_dbg.go:20-24`,
`server/async.go:352-357`.
**Apply to:** every "register the 3 new mobs" site — clone the cow case verbatim with the new entity
id/name. These are pure additions that leave the pig/cow/sheep/chicken paths byte-identical (the pig oracle
stays green — the exact argument each existing cow-case note makes).

### The mob-as-plugin boot-load + spawn path
**Source:** `server/vanilla_pig_embed.go:66-117` (loadVanillaMobRegistry/loadOneVanillaMob),
`server/vanilla_pig.go:51-62` (spawnVanillaMob), `server/plugin_mob_decl.go:386-440` (spawnDeclaredMob).
**Apply to:** the 3 hostiles load into the ONE registry and spawn through the same `spawnVanillaMob(name,...)`
-> `spawnDeclaredMob` -> `buildAIFromDecl` -> `entities.add` chain, rendered as their base wire id
(custom=BEHAVIOR). Zero new spawn plumbing — only the embed list + name constants grow.

## No Analog Found

Every Phase-35 file has a direct in-tree analog. The genuinely NEW logic (no prior art, port from the jar at
exec) is BEHAVIOR inside the new goal files, not file STRUCTURE:

| Concern | Where | Why no analog |
|---------|-------|---------------|
| The targetSelector live tick wiring | `ai_mob.go` serverAiStep | The seam is documented but never wired (the v1 pig had no target goals) — first live use. |
| NearestAttackableTargetGoal / HurtByTargetGoal / MeleeAttackGoal / LeapAtTargetGoal bodies | `ai_goals_target.go` / `ai_goals_attack.go` | No combat/target goal has ever been ported — these are the bulk of Phase 35 (decompile at exec, JARNOTES OPEN list). The FILE pattern (ai_goals_passive.go) and the RNG/nav/damage SEAMS all exist; only the goal logic is new. |
| `lastHurtByMob` / `lastHurtByMobTimestamp` fields | `entity.go` + `combat_mob.go` | Phase 31 tracked the damage SOURCE (tag), not the attacker ENTITY. New fields, but the store-SITE (the flag2 block) already exists. |
| `isDarkEnoughToSpawn` proxy | `spawner.go` | No light engine + no prior day/night spawn gate. The FORCED proxy (CONTEXT SC#4) is new logic over the existing `t.gametime` clock. |

## Metadata

**Analog search scope:** `server/` (ai_mob.go, ai_goal.go, ai_goals_*.go, combat_mob.go, entity.go,
mob_category.go, spawner.go, async.go, vanilla_pig.go, vanilla_pig_embed.go, commands_dbg.go, plugin_mob_decl.go,
plugin_mob_ai.go, plugin_entity.go, clock.go, cow_test.go), `plugins/vanilla_{pig,cow}/`, `data/entity/entity.go`.
**Files scanned:** ~22 Go files + 3 .star/.toml + the entity data table (read-only).
**Pattern extraction date:** 2026-06-30
