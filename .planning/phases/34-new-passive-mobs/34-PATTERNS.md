# Phase 34: New Passive Mobs (cow / sheep / chicken) - Pattern Map

**Mapped:** 2026-06-30
**Files analyzed:** 18 (6 new .star pairs, 1 new goal type, 1 new test cluster, plus modifications to 6 Go surfaces)
**Analogs found:** 17 / 18 (the only NO-ANALOG file is the sheep block-tag read; everything else has a proven pig analog)

The pig (`vanilla_pig`) is the canonical analog for EVERYTHING. The mob registry, base-type
resolver, attribute seeding, and goal-decl capture are ALREADY multi-mob — `baseTypeByName`
(plugin_mob_decl.go:108) already maps `cow`/`sheep`/`chicken` to `entity.Cow`/`Sheep`/`Chicken`,
and `byName` is a `map[string]*mobDecl`. The work is: copy the pig's `.star` pattern 3×, generalize
the pig-specific boot-load + spawn helpers from `pig`-hardcoded to name-parameterized, add ONE new
goal type (sheep EatBlockGoal), and wire three per-mob extras (cow milk, sheep wool/eat, chicken
egg/slow-fall) into the proven seams.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `plugins/vanilla_cow/main.star` (NEW) | plugin/config | declare (load-time) | `plugins/vanilla_pig/main.star` | exact (subset: 8 goals, no carrot, drop @5/@6→@5/@6/@7) |
| `plugins/vanilla_cow/plugin.toml` (NEW) | config | declare | `plugins/vanilla_pig/plugin.toml` | exact |
| `server/assets/vanilla_cow/main.star` (NEW) | plugin/config (embed) | declare | `server/assets/vanilla_pig/main.star` | exact (byte-identical to repo-root) |
| `server/assets/vanilla_cow/plugin.toml` (NEW) | config (embed) | declare | `server/assets/vanilla_pig/plugin.toml` | exact |
| `plugins/vanilla_sheep/main.star` + toml (NEW ×2) | plugin/config | declare | `plugins/vanilla_pig/*` | exact + 1 NEW goal (EatBlockGoal @5) |
| `server/assets/vanilla_sheep/main.star` + toml (NEW ×2) | plugin/config (embed) | declare | `server/assets/vanilla_pig/*` | exact (byte-identical) |
| `plugins/vanilla_chicken/main.star` + toml (NEW ×2) | plugin/config | declare | `plugins/vanilla_pig/*` | exact (8 goals, no carrot) |
| `server/assets/vanilla_chicken/main.star` + toml (NEW ×2) | plugin/config (embed) | declare | `server/assets/vanilla_pig/*` | exact (byte-identical) |
| `server/ai_goals_eat.go` (NEW) | service (AI goal) | event-driven (RNG-gated) | `server/ai_goals_float.go` | role-match (a small goal type with a canUse RNG gate + tick block-eat) |
| `server/vanilla_pig_embed.go` (MODIFY → generalize) | service (boot-load) | file-I/O (embed → temp → load) | itself (`loadVanillaPigRegistry`) | exact (generalize 1 mob → 4) |
| `server/vanilla_pig.go` (MODIFY → generalize) | service (spawn) | request-response (registry lookup → spawn) | itself (`spawnVanillaPig`) | exact (generalize to `spawnVanillaMob(name)`) |
| `cmd/sulfur/main.go` (MODIFY) | config (wiring) | request-response | itself (main.go:347) | exact (rename the loader call) |
| `server/commands_dbg.go` (MODIFY) | controller (dbg command) | request-response | itself (`runDbgCommand` "pig" case) | exact (add cow/sheep/chicken cases) |
| `server/async.go` (MODIFY) | service (natural spawn) | event-driven (off-tick scan → apply) | itself (async.go:334-337) | exact (generalize `spawnVanillaPig` → name pick) |
| `server/attack_dispatch.go` (MODIFY) | controller (interact) | request-response | `tryFeedAnimal` (attack_dispatch.go:801) | role-match (milk is the FEED path's sibling) |
| chicken slow-fall + egg-lay (NEW per-mob tick hook) | service (physics/aiStep) | event-driven (per-tick) | `mobAI.serverAiStep` (ai_mob.go:155) + `dropMobLoot` (death_mob.go:199) | role-match (a customServerAiStep seam + a loot/item-drop) |
| `server/<mob>_test.go` (NEW ×3 + RNG tests) | test | n/a | `server/plugin_pig_test.go` + `server/ai_goals_panic_test.go` | exact (lighter per-mob behavior test + focused RNG test) |

## Pattern Assignments

### `plugins/vanilla_<mob>/main.star` + `server/assets/vanilla_<mob>/main.star` (plugin, declare)

**Analog:** `plugins/vanilla_pig/main.star` (584 lines) — copy WHOLESALE, then trim/retune.

The pig `.star` is a complete, jar-cited template. Each new mob is a SUBSET of the pig's goal set
with retuned params. Cow/sheep/chicken have NO carrot_on_a_stick goal (only ONE Tempt, the food
tag), so DELETE the `tempt_carrot_*` block and one `@4` goal. The goal callbacks (`float_*`,
`panic_*`, `breed_*`, `follow_*`, `stroll_*`, `look_*`, `around_*`) are COPIED VERBATIM — they are
mob-agnostic (they read `entity.*` handles). Only the constants + the `declare_mob(...)` block change.

**Per-mob goal sets** (from 34-CONTEXT.md / 34-JARNOTES.md, all jar-verified):

| prio | Cow | Sheep | Chicken |
|------|-----|-------|---------|
| 0 | FloatGoal | FloatGoal | FloatGoal |
| 1 | PanicGoal(2.0) | PanicGoal(1.25) | PanicGoal(1.4) |
| 2 | BreedGoal(1.0) | BreedGoal(1.0) | BreedGoal(1.0) |
| 3 | TemptGoal(1.25, cow_food) | TemptGoal(1.1, sheep_food) | TemptGoal(1.0, chicken_food) |
| 4 | FollowParentGoal(1.25) | FollowParentGoal(1.1) | FollowParentGoal(1.1) |
| 5 | Stroll(1.0) | **EatBlockGoal** | Stroll(1.0) |
| 6 | LookAtPlayer(6) | Stroll(1.0) | LookAtPlayer(6) |
| 7 | LookAround | LookAtPlayer(6) | LookAround |
| 8 | — | LookAround | — |

NOTE the priority numbers above are the CONTEXT table's; the JARNOTES bytecode shows the actual jar
indices (Cow @0/1/2/3/4/5/6/7; Sheep @0..8 with eatBlockGoal @5; Chicken @0..7). The pig's `.star`
uses the jar indices directly (FloatGoal @0, PanicGoal @1, BreedGoal @3, TemptGoal @4) — **use the
jar registerGoals indices from 34-JARNOTES.md**, NOT the CONTEXT summary table's collapsed numbering.

**The tempt food handle** — the pig uses a dedicated host handle `nearest_player_holding_pig_food`.
Each new mob needs its food predicate. The food tags `cow_food`/`sheep_food`/`chicken_food` are
CONFIRMED in `data/tag/tags.go` (34-JARNOTES.md:54) and the read is `itemInTag(id, "<x>_food")`
(Phase 32, parameterized). The plugin handle must be the parameterized equivalent — verify whether
`nearest_player_holding_pig_food` is hardcoded in `plugin_entity.go` (likely needs a parameterized
`nearest_player_holding_food(tag, range)` handle, or one handle per tag mirroring the pig's).

**The declaration** (copy `plugins/vanilla_pig/main.star:479-583`), retuning attributes:
```python
declare_mob(
    name = "vanilla_cow",          # vanilla_sheep / vanilla_chicken
    base_type = "cow",             # sheep / chicken — ALL THREE already in baseTypeByName (plugin_mob_decl.go:108)
    attributes = {
        "max_health": 10.0,        # Cow 10.0 / Sheep 8.0 / Chicken 4.0 (34-JARNOTES.md:70-72)
        "movement_speed": 0.2,     # Cow 0.2 / Sheep 0.23 / Chicken 0.25
    },
    goals = [ ... ],               # the per-mob set above, each goal() block copied from the pig
)
```

**Embed pair discipline** (24-CONTEXT, repeated in vanilla_pig_embed.go:25-28): the repo-root
`plugins/vanilla_<mob>/` copy and the `server/assets/vanilla_<mob>/` embed copy MUST be byte-identical.
Write one, copy it to the other.

**plugin.toml** — copy `plugins/vanilla_pig/plugin.toml` verbatim, change only `name`. The
capabilities `["entities.read", "entities.write", "world.read", "nav"]` cover the shared goals.
The chicken's egg-lay and sheep's wool/eat are HOST-side (Go), not plugin-drawn, so no new cap.

---

### `server/ai_goals_eat.go` (NEW — service, AI goal, RNG-gated event-driven)

**Analog:** `server/ai_goals_float.go` (92 lines) — the closest existing goal type: a small struct
embedding `baseGoal`, a `canUse` with a single RNG draw, a `tick`, and a `canContinueToUse`.

EatBlockGoal is jar-verified in 34-JARNOTES.md:75-87. Mirror floatGoal's structure:

**Struct + ctor pattern** (ai_goals_float.go:40-51):
```go
type floatGoal struct {
    baseGoal
}
func newFloatGoal() *floatGoal {
    return &floatGoal{baseGoal: newBaseGoal(flagJump)}
}
```
EatBlockGoal flags are `{MOVE, LOOK, JUMP}` (34-JARNOTES.md:75) → `newBaseGoal(flagMove | flagLook | flagJump)`.
It needs an `eatAnimationTick int` field (40-tick timer, EAT_ANIMATION_TICKS=40).

**The RNG gate in canUse** (model on floatGoal.tick's draw, ai_goals_float.go:85-92 — but in canUse here):
```java
// 34-JARNOTES.md:78 — canUse(): if (random.nextInt(adjustedTickDelay(isBaby? 50 : 1000)) != 0) return false;
```
The draw is `mobRandom(e).nextInt(adjustedTickDelay(...))` — same `mobRandom(e)` source floatGoal
uses (ai_goals_float.go:87). adjustedTickDelay(n) == n at 20 TPS (the pig's stroll/breed cite this).
THIS is the lockstep RNG draw the focused test must pin (see test section).

**canUse block check** (34-JARNOTES.md:80-81): `IS_EDIBLE.test(getBlockState(pos))` (tall grass at
mob) OR `getBlockState(pos.below()).is(GRASS_BLOCK)` (grass block below). The block-state read uses
`t.world().GetBlock(pk.Position{...}, dimMinY)` (physics.go:86 is the established read).
**EDIBLE_FOR_SHEEP is a BLOCK tag NOT extracted** — see "No Analog Found" below; cite-defer the
tall-grass branch and check GRASS_BLOCK below directly (the common case, 34-CONTEXT.md:67-68).

**canContinueToUse** (ai_goals_float.go:67-76 is the model for delegating): EatBlockGoal's is
`eatAnimationTick > 0` (34-JARNOTES.md:84).

**tick** (34-JARNOTES.md:85-86): `if (--eatAnimationTick == adjustedTickDelay(4))` → eat the block
(grass_block below → DIRT via SetBlock; broadcastEntityEvent byte 10) + sheep wool-regrow
(`setSheared(false)`). The block write uses `t.world().SetBlock(...)` (commands_dbg.go:46 / block_drop.go).

Wire into the sheep `.star` as a NEW goal type — this requires a host-side goal the plugin declares.
**OPEN QUESTION for planner:** the pig's goals are all expressed AS Starlark callbacks (float_tick
etc.). EatBlockGoal draws RNG host-side or plugin-side? If the sheep is dogfooded as a Go-vs-plugin
oracle, the EatBlockGoal nextInt gate must be lockstep on BOTH halves (34-CONTEXT.md:64,78-82). The
lean is a per-mob behavior test (not a full oracle), so EatBlockGoal can be a Go-native goal type
referenced by the `.star` declaration (like how breed routes through host `try_breed`), OR a pure
plugin callback drawing `entity.rand_int(...)`. The pig's `stroll`/`panic` draw plugin-side via
`entity.rand_int`; follow this if the sheep declares EatBlockGoal as a plugin goal.

---

### `server/vanilla_pig_embed.go` (MODIFY → generalize to load all 4 embeds)

**Analog:** itself. The file is a complete template for one mob; generalize the hardcoded
`vanilla_pig`/`vanillaPigFS` to a per-mob loop.

**Current embed directive** (vanilla_pig_embed.go:30-34):
```go
//go:embed assets/vanilla_pig/plugin.toml assets/vanilla_pig/main.star
var vanillaPigFS embed.FS
const vanillaPigMobName = "vanilla_pig"
```
EXTEND the embed glob to all 4: `//go:embed assets/vanilla_pig assets/vanilla_cow assets/vanilla_sheep assets/vanilla_chicken`
(or list each plugin.toml/main.star). Add name constants `vanillaCowMobName` etc.

**Current loader** (vanilla_pig_embed.go:43-75) `loadVanillaPigRegistry()` materializes ONE plugin,
loads it, asserts the ONE declaration. Generalize to `LoadVanillaMobRegistry()` that loops over the
4 mob names, materializing + loading EACH into the SAME `mobRegistry` (the `byName` map holds all 4 —
plugin_mob_decl.go:80-83). Keep the LOUD per-mob failure (vanilla_pig_embed.go:69-73). The
`materializeVanillaPig` (vanilla_pig_embed.go:80-102) + `vanillaPigCaps` (104-123) helpers
parameterize by mob name (replace the hardcoded `"assets/vanilla_pig/"` prefix + temp-dir name).

NOTE the registry `setLoadCaps` is per-load (plugin_mob_decl.go:95) — if each mob has its own
plugin.toml caps, call `setLoadCaps` before EACH mob's `LoadDirWith`, OR load each mob into its own
`LoadDirWith` call against the shared registry with the per-mob caps stamped.

---

### `server/vanilla_pig.go` (MODIFY → generalize spawnVanillaPig → spawnVanillaMob(name))

**Analog:** itself. `spawnVanillaPig` (vanilla_pig.go:36-47) is the template:
```go
func (t *TickLoop) spawnVanillaPig(x, y, z float64) *Entity {
    if t.mobRegistry == nil {
        panic("spawnVanillaPig: no mob registry installed (...)")
    }
    decl, ok := t.mobRegistry.byName[vanillaPigMobName]
    if !ok {
        panic("spawnVanillaPig: the vanilla_pig declaration is missing (...)")
    }
    return t.spawnDeclaredMob(decl, x, y, z)
}
```
Generalize to `spawnVanillaMob(name string, x, y, z float64) *Entity` — look up `byName[name]`,
delegate to `spawnDeclaredMob` (unchanged — it already handles any decl). Keep `spawnVanillaPig` as
a thin wrapper `spawnVanillaMob(vanillaPigMobName, ...)` so the pig oracle's call sites
(`spawnVanillaPigWithID`, vanilla_pig.go:54) stay byte-identical and the pig oracle test is untouched.
The `LoadVanillaPigRegistry` export (vanilla_pig.go:18) renames to `LoadVanillaMobRegistry`
(main.go:347 calls it).

---

### `cmd/sulfur/main.go` (MODIFY — boot wiring)

**Analog:** itself (main.go:347-357):
```go
if reg, err := server.LoadVanillaPigRegistry(); err != nil {
    log.Fatalf("vanilla_pig boot-load failed (...): %v", err)
} else {
    tick.SetMobRegistry(reg)
    log.Printf("vanilla_pig: bundled 1:1 pig plugin boot-loaded (...)")
}
```
Rename `LoadVanillaPigRegistry` → `LoadVanillaMobRegistry` (loads all 4). Update the log line. The
FATAL-on-failure discipline carries (a swap with a missing mob is a broken server). `SetMobRegistry`
(vanilla_pig.go:24) is unchanged — it installs the multi-mob registry.

---

### `server/commands_dbg.go` (MODIFY — add /dbg cow|sheep|chicken)

**Analog:** the `"pig"` case in `runDbgCommand` (commands_dbg.go:14-19):
```go
case "pig":
    e := t.spawnVanillaPig(p.x, p.y, p.z)
    if e != nil {
        t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned pig eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
    }
```
Add `case "cow"`, `case "sheep"`, `case "chicken"` each calling `t.spawnVanillaMob("vanilla_<mob>", ...)`.
Update the default usage string (commands_dbg.go:30) to list the new subcommands.

---

### `server/async.go` (MODIFY — natural spawn generalization)

**Analog:** the SWAP call site (async.go:334-338):
```go
dest := t.regionForColumn(columnOf(float64(c.x)+0.5, float64(c.z)+0.5))
t.withRegion(dest, func() {
    t.spawnVanillaPig(float64(c.x)+0.5, float64(c.y), float64(c.z)+0.5)
})
```
Generalize from hardcoded `spawnVanillaPig` to a category/name pick (`spawnVanillaMob(name, ...)`).
The natural spawner counts `categoryCreature` (async.go:314); cow/sheep/chicken are all
`categoryOf → CREATURE` (34-CONTEXT.md:44), so they share the same cap. The planner decides the mob
PICK (random among the 4, or weighted) — the per-tick throttle (one placement per apply, async.go:338)
and the cross-region cap re-check (async.go:308-317) are unchanged.

---

### `server/attack_dispatch.go` (MODIFY — cow milking, FEED-path sibling)

**Analog:** `tryFeedAnimal` (attack_dispatch.go:801-847) + the `handleInteract` dispatcher
(attack_dispatch.go:725-764). Cow milking is the FEED path's sibling — same right-click-with-item
flow, different item check + effect.

**The held-item read pattern** (attack_dispatch.go:802-817):
```go
inv := ensureInventory(p)
held := inv.get(heldWindowSlot(inv.heldSlot))
if slotIsEmpty(held) {
    return
}
itemID := int32(held.ItemID)
if !itemInTag(itemID, "pig_food") {   // milking: check itemID == bucket instead
    return
}
```
For milking (34-JARNOTES.md:110, Cow.mobInteract): check the held item is an empty `bucket`
(`data/item/item.go:6261` — `"bucket"`), then `usePlayerItem` (consume the bucket via
`t.shrinkHeldItem(p, inv)` — attack_dispatch.go:823) and GIVE a `milk_bucket`
(`data/item/item.go:6297`). Decompile Cow.mobInteract at exec for the exact bucket→milk swap +
SoundEvents.COW_MILK. NO RNG (34-CONTEXT.md:62). The COW_MILK sound id is `entity.cow.milk` = 449
(data/soundid — `449: "entity.cow.milk"`).

**The sound-emit pattern** (combat_mob.go:187 is the playSound→broadcast model):
```go
t.broadcastToTrackers(e.id, encodeSoundEntity(soundIDPigHurt, soundSourceNeutral, e.id, vol, pitch, seed))
```
Reuse `encodeSoundEntity` + `broadcastToTrackers` for COW_MILK. NOTE the pig feed path notes
"playEatingSound() for a PIG is a no-op so a sound-overriding animal (Phase 34) slots in here"
(attack_dispatch.go:793) — the FEED path was DESIGNED for this extension.

**Dispatch wiring** — `handleInteract` (attack_dispatch.go:763) currently calls `tryFeedAnimal`
unconditionally. Add a milk-check sibling: if the mob is a cow and the held item is a bucket, route
to `tryMilkCow` instead of/before `tryFeedAnimal`. Keep the same-region cut + owner-resolve guards
(attack_dispatch.go:741-761) — they are mob-agnostic.

---

### Chicken egg-lay + slow-fall (NEW per-mob tick hook)

**Analog (the tick seam):** `mobAI.serverAiStep` (ai_mob.go:155-210). The chicken's `aiStep` extras
(slow-fall, egg-lay) are a `customServerAiStep` — the jar order is
`...navigation.tick → customServerAiStep → moveControl/lookControl/jumpControl` (ai_mob.go:9). There
is NO `customServerAiStep` seam yet; the planner adds one (a per-mob hook in serverAiStep, OR a
per-type branch in the tickAI per-mob loop, tick_phases.go:324-333 where i-frames/aging already run
OUTSIDE serverAiStep as pure-int steps).

**Slow-fall** (34-JARNOTES.md:97): `if (!onGround && deltaMovement.y < 0) movement.multiply(1.0, 0.6, 1.0)`.
The `onGround` read is `e.onGround` (entity.go:70); deltaMovement is the entity velocity. This is a
per-mob physics override — apply BEFORE tickPhysics integrates gravity (the pig jump impulse lands
in tickAI before tickPhysics, ai_mob.go:205). NO RNG.

**Egg-lay** (34-JARNOTES.md:99-103): `if (--eggTime <= 0) { dropEgg; playSound(CHICKEN_EGG, ...2 nextFloat pitch); eggTime = nextInt(6000)+6000; }`.
The chicken needs an `eggTime` field (init `nextInt(6000)+6000` at spawn — the spawn seam is
`spawnDeclaredMob`, plugin_mob_decl.go:386, or a per-type init). RNG: `mobRandom(e).nextInt(6000)` +
2× `mobRandom(e).nextFloat()` for the pitch (mirror the voice-pitch draw, combat_mob.go:178). This is
the chicken's lockstep RNG — the focused test pins it.

**Egg-drop pattern:** `dropMobLoot` (death_mob.go:199-235) is the loot-table model, but the egg is a
direct `spawnAtLocation(EGG)` (34-CONTEXT.md:102-103). The simplest faithful path is a direct
`NewItemEntity` (block_drop.go:168 / death_mob.go:232):
```go
ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y+e.height/2.0, e.z, stack)
t.regionForEntity(e).entities.add(ie)
```
with `stack` = one `egg` item (data/item — verify the egg item id). The CHICKEN_EGG sound is
`entity.chicken.egg` = 352 (data/soundid).

**Hitbox** (34-CONTEXT.md:44-46, SC#3): chicken dims are 0.4×0.7 (entity.go Chicken: Width 0.4,
Height 0.7), cow 0.9×1.4, sheep 0.9×1.3 — already in `data/entity/entity.go`. `NewEntity(decl.baseType, ...)`
(plugin_mob_decl.go:387) copies the base type's AABB dims, so each mob auto-gets the right hitbox.
The baby ×0.5 reuses Phase-33 `babyDimensionScale` + `refreshDimensions` (plugin_mob_decl.go:413-414).

---

### `server/<mob>_test.go` (NEW ×3) + focused RNG tests

**Analog (per-mob behavior test):** `server/plugin_pig_test.go` (the pig GATE). The new mobs reuse
proven goals, so a LIGHTER per-mob test suffices (34-CONTEXT.md:79-82) — NOT the full byte-identical
Go-vs-plugin oracle.

**Boot-load + goal-set assertion** (plugin_pig_test.go:35-79 `TestPluginPigBootLoads`): spawn the mob,
assert it renders as the right `entity.<Mob>.ID`, assert the goal count + priorities. Copy this shape
for `TestCowBootLoads` / `TestSheepBootLoads` / `TestChickenBootLoads`, asserting each mob's goal set.

**Behavior test:** each mob spawns + walks + panics + tempts + breeds (the proven goals) + its extra
(cow milk, sheep eat-grass, chicken egg+slow-fall) — 34-CONTEXT.md:108-109.

**Analog (focused RNG test):** `server/ai_goals_panic_test.go` (`TestPanicGoalFleesOnPanicDamage`,
line 30) — the model for a deterministic, draw-order-pinned goal test. The NEW RNG paths need their
own lockstep coverage:
- **Sheep EatBlockGoal**: pin the `nextInt(adjustedTickDelay(50/1000))` gate (34-CONTEXT.md:64).
  `TestEatBlockGoalRNGGate` — assert the goal fires only on the `nextInt(...) == 0` draw, baby vs adult delay.
- **Chicken egg-lay**: pin `nextInt(6000)` reset + 2× `nextFloat()` pitch (34-CONTEXT.md:71-72).
  `TestEggLayRNG` — assert eggTime reset + sound pitch draw order.
- **Chicken slow-fall**: `TestSlowFall` — non-RNG, assert deltaMovement.y *= 0.6 when falling.
- **Cow milk**: `TestMilkCow` — non-RNG, assert bucket→milk_bucket swap + sound.

**The pig oracle stays byte-identical** (34-CONTEXT.md:17,129): `TestPluginPigEqualsGoNativePig`
(plugin_pig_test.go:13) is UNTOUCHED — the pig keeps its 9 goals; cow/sheep/chicken are SEPARATE mobs.
Verify `spawnVanillaPig`/`spawnVanillaPigWithID` (vanilla_pig.go) stay intact through the generalization.

## Shared Patterns

### The plugin embed pair (byte-identical repo-root + assets)
**Source:** `plugins/vanilla_pig/` + `server/assets/vanilla_pig/` (vanilla_pig_embed.go:25-28)
**Apply to:** all 3 new mob plugins. Write one `.star`, copy verbatim to the embed location. The
embedded copy is the source of truth for the swap (an on-disk copy cannot widen caps, T-24-07).

### Per-mob seeded RNG (lockstep discipline)
**Source:** `mobRandom(e)` (= `e.ai.rng`, ai_goals_float.go:87) + `reseedMobAI` (ai_mob.go:302)
**Apply to:** EatBlockGoal gate, chicken egg-lay. Every per-mob RNG draw goes through `mobRandom(e)`
(seeded per entity id at spawn via `reseedMobAI`, called in `spawnDeclaredMob`, plugin_mob_decl.go:403).
Draw ORDER must match the jar bytecode EXACTLY. Event-time RNG (loot seed) uses `rand.Int64()`
(death_mob.go:214, NOT the mob stream).

### The base-type → real-attribute spawn (custom = BEHAVIOR)
**Source:** `spawnDeclaredMob` (plugin_mob_decl.go:386-430) + `seedAttributes` (plugin_mob_decl.go:152)
**Apply to:** all 3 mobs. `NewEntity(decl.baseType, ...)` copies the wire id + AABB dims; `seedAttributes`
overrides max_health/movement_speed; `initSpawnHealth` sets health to max; `reseedMobAI` gives the
per-entity stream; the baby branch (413-418) splices DATA_BABY_ID + shrinks the AABB. ALL multi-mob
already — no change needed for the new base types (cow/sheep/chicken already in `baseTypeByName`).

### The FEED/interact path (cow milk slots in here)
**Source:** `handleInteract` (attack_dispatch.go:725) + `tryFeedAnimal` (attack_dispatch.go:801)
**Apply to:** cow milking. The same owner-region resolve (741-748) + same-region cut (758-761) +
held-item read (807-817) + `shrinkHeldItem` consume (823). The pig feed path's no-op `playEatingSound`
(attack_dispatch.go:826) was explicitly left as the seam for a sound-overriding animal (Phase 34).

### Sound emit to trackers
**Source:** `encodeSoundEntity` + `broadcastToTrackers` (combat_mob.go:187)
**Apply to:** cow milk (entity.cow.milk = 449), chicken egg (entity.chicken.egg = 352). Voice-pitch
jitter pattern: `(nextFloat() - nextFloat()) * 0.2 + 1.0` (combat_mob.go:176-178).

### Item drop
**Source:** `NewItemEntity` (block_drop.go:168) + `t.regionForEntity(e).entities.add(ie)` (death_mob.go:232-233)
**Apply to:** chicken egg drop. A direct `spawnAtLocation`-style drop at the mob center (not a loot
table) is the simplest faithful path; verify the egg item id in `data/item`.

## No Analog Found

| File / concern | Role | Data Flow | Reason |
|----------------|------|-----------|--------|
| EDIBLE_FOR_SHEEP block-tag read (sheep EatBlockGoal `IS_EDIBLE` branch) | service (block tag) | transform | BLOCK tags are NOT extracted — only damage-type + item tags live in `data/tag` (34-JARNOTES.md:88, 34-CONTEXT.md:67). NO `itemInTag`-equivalent for block tags exists. EITHER extend the tag extractor for block tags (`edible_for_sheep`) OR cite-defer the tall-grass branch and check `GRASS_BLOCK` below directly via `t.world().GetBlock` (physics.go:86) — the common eat case (34-CONTEXT.md:127). The planner decides; lean to the cite-deferred grass_block-below shortcut (a documented deferral, 34-CONTEXT.md:133-134). |

The `customServerAiStep` seam (chicken slow-fall + egg-lay tick hook) has a PARTIAL analog
(`serverAiStep` ai_mob.go:155 is the order reference; tick_phases.go:324-333 is the outside-serverAiStep
per-mob loop where i-frames/aging run) but no existing per-mob/per-type tick override — the planner
adds the seam (a small, cited extension), so it is listed as role-match, not no-analog.

## Metadata

**Analog search scope:** `server/` (pig goal files, vanilla_pig*.go, plugin_mob_decl.go, ai_mob.go,
attack_dispatch.go, commands_dbg.go, async.go, death_mob.go, block_drop.go, combat_mob.go,
tick_phases.go, plugin_pig_test.go, ai_goals_panic_test.go), `plugins/vanilla_pig/`,
`server/assets/vanilla_pig/`, `cmd/sulfur/main.go`, `data/entity/entity.go`, `data/item/item.go`,
`data/soundid/`.
**Files scanned:** ~22
**Pattern extraction date:** 2026-06-30
