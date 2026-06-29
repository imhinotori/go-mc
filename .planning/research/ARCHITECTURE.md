# Architecture Research — v5 Mob Subsystem Integration

**Domain:** Living-entity subsystems (damage, jump/fluid, aging/breeding, held-item/tags) + new mobs as Starlark plugins, integrated into the existing Sulfur tick-owned, Folia-regionized server
**Researched:** 2026-06-29
**Confidence:** HIGH (every integration point below is a real `file:function` read this session, not inferred)

---

## 0. The Integration Surface — Read These First

The v5 subsystems do NOT get a greenfield design. They bolt onto five existing, shipped seams. The single most important fact discovered during research:

> **There are TWO separate attribute systems.** `*tickPlayer` carries its own `attributeHolder` (`server/attributes.go` — an `attributeKey int` enum over a `playerAttributeBase` map). `*Entity` carries the REAL `*attribute.Map` (`server/entity.go:135`, `level/attribute`), read via `e.getAttributeValue(*attribute.Attribute)`. **The entire `combat.go` damage pipeline is built on the `*tickPlayer` flavor and reads the player holder.** A mob has the real `*attribute.Map`, not the player holder. This is the deciding constraint for the damage keystone (§1).

The five seams and their owning files:

| Seam | Owner file:function | What it is today | v5 needs |
|------|--------------------|------------------|----------|
| Damage pipeline | `combat.go` `applyDamage(p *tickPlayer)` / `actuallyHurt(p *tickPlayer)` | `*tickPlayer`-only, reads player attr holder, `on_damage` Emit at `actuallyHurt` post-mitigation site (`combat.go:310`) | a `*Entity` mob path + `lastDamageSource` + damage-type tags |
| AI driver | `ai_mob.go` `serverAiStep(t, e)` (line 105) + `mobAI` struct (line 29) | navigation only; jumpControl DEFERRED (line 124 comment) | a `jumpControl` seam + mob `isInWater`/`getFluidHeight` |
| Goal arbitration | `ai_goal.go` `goalSelector` (flag-locking, line 233 `tick`) | single selector per mob; flags MOVE/LOOK/JUMP/TARGET already defined (line 38) | TARGET flag is defined but unused; needs a 2nd (target) selector for hostiles |
| Fluid read | `fluid.go` `fluidAt(pos)` (line 246), `decodeFluid` | raw block→`fluidState`; player fluid in `breath.go`/`suffocation.go` is `*tickPlayer`-only | a position/AABB mob `isInWater` predicate built on `fluidAt` |
| Plugin mob spawn | `plugin_mob_decl.go` `spawnDeclaredMob` (line 385), `buildAIFromDecl` (`plugin_mob_ai.go:185`) | builds a `*mobAI`, routes to `regionForEntity(e).entities.add` | reused verbatim for baby spawn + new mob types |

Plus the Folia substrate that constrains every new mutable field:

| Folia primitive | Owner | Rule for v5 |
|-----------------|-------|-------------|
| Thin-id handle | `plugin_entity.go:35` `entityHandle{t, id, region, caps}` (NEVER a live `*Entity`) | any new plugin-facing read/mutate goes through a handle op, re-resolving `store().get(id)` on the owner |
| Region routing | `region_transfer.go` `regionForEntity` (line 56), `regionForColumn`, `withRegion` (line 100) | new state lives on `*Entity` and travels with the entity at the barrier (`applyCrossRegionTransfers`, line 193) — never aliased mid-tick |
| Cross-region scan | `region_transfer.go` `entitiesNearAcrossRegions` (line 143), `spawner.go` `mobNearAcrossRegions` | any partner/parent/target broad-phase that may cross the seam runs at the QUIESCENT barrier, not mid-region-tick |
| Region-aware Emit | `region_transfer.go` `emitEntityEvent` (line 86) | new entity-scoped events fire through the region-bound path so payload handles resolve the owning region's store |

---

## 1. KEYSTONE — Mob Damage Pipeline

**Decision: PARALLEL `*Entity` path, NOT generalize-in-place. Build `applyDamageEntity` / `actuallyHurtEntity` as new functions in a new `combat_mob.go`.**

### Why parallel, not generalize

`combat.go`'s `applyDamage`/`actuallyHurt` are `func (t *TickLoop) applyDamage(p *tickPlayer, ...)`. They:
1. Read the **player attribute holder** (`p.getAttributeValue(attrArmor)` → `attributeKey` enum, `server/attributes.go:148`). A mob reads `e.getAttributeValue(*attribute.Attribute)` — a different backend.
2. Drive **player-specific death** (`die(p)` → `playerCombatKill` packet + death screen, `combat.go:399`). A mob death is a despawn + `EntityDeathEvent`, never a `PlayerCombatKill`.
3. Send **`setHealth(...)` to `p.client`** (`combat.go:320`). A mob has no client; its health change is wire-broadcast as entity metadata (a later metadata concern) — for v5 the mob's `health` field is internal AI state (PanicGoal reads `lastDamageSource`, not health-bar wire).

Trying to make these generic with an interface would (a) force the bit-fragile shared AI flow through a new abstraction and (b) risk perturbing the player combat path that v3 sealed and tested. A second, sibling path keeps the player path BYTE-IDENTICAL (the player oracle is also implicitly protected) and lets the mob path port `LivingEntity.hurtServer`/`actuallyHurt` against the mob's real `*attribute.Map`.

**The shared core to FACTOR OUT (no behavior change):** `combatRulesGetDamageAfterAbsorb`, `combatRulesGetDamageAfterMagicAbsorb`, `mthClampF`, `maxF`, the NaN/Inf clamp — these are already free functions in `combat.go` (lines 543, 562, 128, 603). Both paths call them. Do NOT duplicate them.

### Where `lastDamageSource` lives — and Folia safety

Add to `*Entity` (`server/entity.go`, next to `health`/`ai`):

```go
// LivingEntity.lastDamageSource / lastHurt / invulnerableTime / hurtTime — the i-frame +
// panic-read state. Tick-owned (TICK-05), travels with the *Entity at the barrier
// (applyCrossRegionTransfers adopts the pointer, so this state is never aliased mid-tick).
lastDamageSource damageSource  // zero value = "never hurt" (PanicGoal.shouldPanic reads the tag)
health           float32       // LivingEntity.health (a mob's, distinct from tickPlayer.health)
lastHurt         float32
invulnerableTime int32
hurtTime, hurtDuration int32
```

**Folia race-safety story:** `lastDamageSource` is a plain value/struct field on `*Entity`. The Entity is owned by exactly one region's store at any tick (the static `regionOf` split, `region_transfer.go:43`). All damage application happens on the OWNER region's goroutine. The cross-region case — a player in region A attacks a mob in region B — is the ONLY hazard, addressed below. The field is NOT part of the snapshot-friendly value set the async tracker copies (same exemption `ai`/`attributes` already carry, documented at `entity.go:119`), so it does not break the snapshot contract.

`damageSource` is a NEW small value type (not a pointer to shared state):

```go
// net.minecraft.world.damagesource.DamageSource — v5 subset: the type tag + the optional
// attacker id (a THIN id, never a live *Entity — Folia rule). Tags drive PanicGoal +
// wolf-anger (lastDamageSource.is(panicCausingDamageTypes) / getEntity()).
type damageSource struct {
    typeTag   damageTypeTag  // PLAYER_ATTACK, MOB_ATTACK, FALL, IN_FIRE, ... (an enum/bitset)
    attacker  int32          // entity id of the attacker, 0 = none (re-resolve on owner if needed)
}
```

### The three attack flows — data flow for the keystone

```
PLAYER → MOB (the main new flow):
  ServerboundAttack (subtick.go → handleAttack, attack_dispatch.go:91)
    └─ TODAY: lookupPlayerByEntityID → tickPlayer victim ONLY
    └─ v5: ALSO try store().get(targetID) → *Entity mob victim
         (region note: the attack runs on the PLAYER's region goroutine; the mob may be in
          ANOTHER region. RESOLUTION: do NOT mutate the mob's store cross-region mid-tick.
          Queue the hit as a damageIntent{victimID, source, amount} onto the OWNING region's
          inbox, applied at the barrier — the SAME discipline as transferIntent/asyncIn2.
          The checkerboard split means an adjacent column IS the other region, and attackReach
          (3.5) routinely crosses a 16-block chunk seam at chunk edges — so the barrier-queue
          path is the correct, race-clean one, not "same-region only".)
    └─ applyDamageEntity(victimMob, source{PLAYER_ATTACK, attackerID}, total)
         (mirrors applyAttackDamage's i-frame boolean so the knockback/sweep tail gates correctly)

MOB → PLAYER (hostiles):
  MeleeAttackGoal.tick (Starlark, in the mob plugin) → entity handle attack op
    └─ host op resolves the nearest player (nearestPlayerAt) within attack-reach
    └─ apply player damage — the EXISTING player path
         (a mob hitting a player runs on the MOB's region goroutine; players are NOT in the
          per-region entity store — t.players is coordinator-shared read — so reading player
          pos is fine, but MUTATING player health from a region goroutine must route through
          the same barrier-queue / coordinator-applied path. Treat player-victim hits as a
          mob→player damageIntent applied at the barrier.)

MOB → MOB (wolf retaliation, future-proof):
  same shape as PLAYER→MOB but attacker is a mob id; applyDamageEntity on the victim's owning region.
```

### `on_damage` Emit — it already generalizes cleanly

The Emit at `combat.go:310` carries `host.DamageEvent{EntityID, Amount}` (post-mitigation `amount`). It is already keyed by entity id, not by `*tickPlayer`. The mob path's `actuallyHurtEntity` fires the SAME Emit with `EntityID: int(e.id)` — but through `r.emitEntityEvent(host.EventDamage, ...)` (`region_transfer.go:86`) so the dispatch is region-scoped. **PanicGoal does NOT read `on_damage`** — it reads `e.lastDamageSource` directly via a new entity handle attr (`entity.last_damage_type` / `entity.was_hurt`). The Emit is for external plugins; the goal reads the field. Keep these two paths separate (the deferred-goals.md "do NOT fake a hurt flag" rule means the field must be the REAL ported `lastDamageSource`, set by `applyDamageEntity`).

### New vs modified

| Component | New/Modified | File |
|-----------|--------------|------|
| `applyDamageEntity` / `actuallyHurtEntity` | NEW | `combat_mob.go` (new) |
| `damageSource` + `damageTypeTag` + panic-tag set | NEW | `damage_source.go` (new) |
| `*Entity.lastDamageSource/health/lastHurt/invulnerableTime/hurtTime` | NEW fields | `entity.go` (modify) |
| `damageIntent` barrier queue (cross-region hit) | NEW | `combat_mob.go` + region inbox in `region_transfer.go` |
| `handleAttack` dual-resolve (player OR mob target) | MODIFIED | `attack_dispatch.go:91` |
| CombatRules / clamp helpers | REUSED (do not duplicate) | `combat.go` |
| `on_damage` Emit (region-scoped) | REUSED via `emitEntityEvent` | — |
| entity handle `was_hurt`/`last_damage_type` attr | NEW handle attr | `plugin_entity.go` |

---

## 2. Mob JumpControl + Fluid

### JumpControl seam — fill the deferred line

`ai_mob.go:124` documents the deferral: `// (moveControl/lookControl/jumpControl.tick — folded into navigation.tick's yaw + moveEntity.)`. The FloatGoal port (`net.minecraft.world.entity.ai.goal.FloatGoal`) does `if getRandom().nextFloat() < 0.8 then getJumpControl().jump()` and claims `Goal$Flag.JUMP` (already defined, `ai_goal.go:42`).

Add to `mobAI` (`ai_mob.go:29`):

```go
// jumpControl is the JumpControl.jump() impulse latch — net.minecraft.world.entity.ai.control
// .JumpControl. A goal (FloatGoal) sets wantJump=true via jumpControl().jump(); serverAiStep
// consumes it AFTER navigation.tick (the jar order: navigation.tick → ... → jumpControl.tick),
// applies the vanilla jump impulse to e.vy, then clears it. Tick-owned (TICK-05).
jumpControl struct{ wantJump bool }
```

The jump *impulse* itself ports `LivingEntity.jumpFromGround()` (`vy = 0.42 * blockJumpFactor`, plus the sprint-forward bonus — port the exact bytecode). It is applied in `serverAiStep` in the JUMP slot AFTER `navigation.tick` (`ai_mob.go:123`), matching the jar's `moveControl/lookControl/jumpControl` tail. The plugin-facing seam is a new nav-or-entity handle op `nav.jump()` / `entity.jump()` that sets `m.jumpControl.wantJump = true`.

**Folia:** `jumpControl` is on `mobAI`, which hangs off `*Entity` and travels at the barrier. Single-owner, no new hazard.

### `isInWater` / `getFluidHeight` predicate

Build a mob fluid predicate on the EXISTING `fluidAt` (`fluid.go:246`). The player fluid code (`breath.go`, `suffocation.go`) is `*tickPlayer`-specific and uses player eye-heights, so it is NOT reusable — write a mob version:

```go
// mobIsInWater ports Entity.isInWater (updateInWaterStateAndDoFluidPushing → the WATER fluid
// AABB intersection). v5 subset: sample fluidAt over the mob's AABB feet cells; getFluidHeight
// returns the max submerged water column height in the AABB. Reads fluidAt (the raw block read)
// — NO new world primitive. Tick-owned read on the owner; the mob's AABB is e.AABB() (entity.go:214).
func (t *TickLoop) mobIsInWater(e *Entity) bool { ... }
func (t *TickLoop) mobFluidHeight(e *Entity, tag fluidTag) float64 { ... }
```

**Folia:** `fluidAt` reads `t.world().GetBlock` — the world (ChunkManager) is shared and read-only during the mob's region tick (writes go through the owner's `SetBlock`). A mob reading its own column's fluid is reading blocks its own region owns. Race-clean.

### FloatGoal's JUMP claim through the goalSelector

No new mechanism — FloatGoal declares `flags = ["JUMP"]` in the plugin and the EXISTING `goalSelector` (`ai_goal.go`) arbitrates it. At priority 0 it outranks everything; while it holds JUMP, its `requiresUpdateEveryTick=true` ticks it every tick (the existing `tickRunningGoals` path, `ai_goal.go:282`). **This is the FIRST real use of the JUMP flag** — verify the flag-lock arbitration handles a JUMP-only goal (it does: `goalCanBeReplacedForAllFlags` is flag-generic, `ai_goal.go:182`).

### New vs modified

| Component | New/Modified | File |
|-----------|--------------|------|
| `mobAI.jumpControl` field + jump-impulse in `serverAiStep` | NEW field, MODIFIED `serverAiStep` | `ai_mob.go` |
| `jumpFromGround` impulse port | NEW | `ai_mob.go` or `physics.go` |
| `mobIsInWater` / `mobFluidHeight` | NEW | `fluid_mob.go` (new) |
| `nav.jump()` / `entity.in_water` handle ops | NEW handle attrs | `plugin_entity.go` |
| FloatGoal plugin goal | NEW | `plugins/vanilla_pig/main.star` (+ each swimming mob) |

---

## 3. Animal Aging + Breeding

### Where age/inLove/babyness live

Add to `*Entity` (these are `Animal`/`AgeableMob` fields, ported from `net.minecraft.world.entity.AgeableMob` + `Animal`):

```go
age       int32 // AgeableMob.age (<0 = baby ticking up to 0 = adult; >0 = breeding cooldown)
forcedAge int32 // AgeableMob.forcedAge
inLove    int32 // Animal.inLoveTime (>0 = in love; decrements; partner search runs while >0)
loveCause int32 // Animal.loveCause (the player id who fed it — for baby owner/stats)
```

**Folia:** plain ints on `*Entity`, travel at the barrier. The decrement (`age`/`inLove` countdown) is a per-tick mob update — put it in a new `tickMobAging` step or fold it into the mob's `customServerAiStep` slot in `serverAiStep`. Single-owner.

**Bit-fragile risk:** `inLove`/`age` decrements are new per-tick mutations on EVERY animal including the pig. The pig oracle (`TestPluginPigEqualsGoNativePig`) compares Go-native vs plugin pig over 500 ticks. **Aging does not draw RNG** (it's a pure decrement), so it does not perturb the RNG stream — SAFE. The hazard is BreedGoal's spawn-time RNG (below).

### Baby spawn — reuse `spawnDeclaredMob`

A baby is spawned by `BreedGoal.spawnChildFromBreeding` → `getBreedOffspring`. **Reuse `spawnDeclaredMob` (`plugin_mob_decl.go:385`) verbatim** — it already:
- routes to `regionForEntity(e).entities.add` (line 405) — the baby lands in its position's owning region;
- reseeds per-entity RNG by id (`reseedMobAI`, line 395) — the baby gets its own deterministic stream.

The baby is the SAME declared mob with `age` set negative (the `setBaby` analogue). The plugin's breed goal calls a new spawn handle op `world.spawn_baby(parent_a, parent_b)` that resolves both parents (thin-id), picks the midpoint, and calls `spawnDeclaredMob` with a baby age.

### Partner/parent search broad-phase — Folia is the hard part

BreedGoal's `canUse` scans for a nearby in-love partner of the same type within ~8 blocks; FollowParentGoal scans for the nearest adult of the same type within range. Use the EXISTING broad-phase:
- same-region search: `t.cur().entities.near(x, z, rangeChunks)` (the per-section bucket scan the spawner uses, `spawner.go:417`).
- **cross-region search: `entitiesNearAcrossRegions` (`region_transfer.go:143`) — but it MUST run at the quiescent barrier, NOT mid-region-tick** (its doc-comment: "runs at the QUIESCENT barrier ... reading multiple region stores is -race clean by construction"). A breed partner one column away IS in the other region (checkerboard split).

**RESOLUTION for cross-region partner search:** the partner scan inside a goal's `canUse` runs DURING the region fan-out (a goal callback is on the owning region's goroutine, `plugin_mob_ai.go:78`). It therefore CANNOT call `entitiesNearAcrossRegions` (that races the other region's concurrent tick). Two options:
1. **Same-region-only partner search (recommended for v5 first cut):** the goal's handle op uses `h.region.entities.near(...)` (the bound region's store, race-clean). Breeding only works between two animals the same region owns. Visually identical for animals herded together (they share a region most of the time); the seam case (partners split across the checkerboard) simply doesn't breed until one wanders into the other's region — acceptable, documented deviation.
2. **Barrier-resolved breed (full fidelity):** the goal sets an `intent` flag; a coordinator-side post-barrier step (like `applyCrossRegionTransfers`) runs the cross-region partner match with `entitiesNearAcrossRegions` and spawns the baby. More complex; defer to a follow-up if the same-region cut shows visible gaps.

**Pick option 1 for v5.** It keeps every goal callback strictly region-local (the whole thin-handle invariant) and avoids a new barrier phase.

### New vs modified

| Component | New/Modified | File |
|-----------|--------------|------|
| `*Entity.age/forcedAge/inLove/loveCause` | NEW fields | `entity.go` |
| `tickMobAging` (decrements) | NEW | `ai_mob.go` or new `aging.go` |
| `world.spawn_baby` / `entity.set_in_love` / `entity.age` handle ops | NEW handle attrs | `plugin_entity.go` |
| same-region partner search op (`entities_near` filtered by type + in_love) | NEW handle attr (uses `h.region.entities.near`) | `plugin_entity.go` |
| BreedGoal + FollowParentGoal plugin goals | NEW | each animal plugin |
| baby spawn | REUSED `spawnDeclaredMob` | — |

---

## 4. Held-Item Read + Item Tags

### Held-item read — reuse the player scan

TemptGoal's `canUse` finds the nearest player holding a tempting item. The player scan already exists: `nearestPlayerAt` (`ai_goals_passive.go:244`) and the handle seam `world.nearest_player` (`plugin_entity.go:607` area). v5 adds a held-item read to the player result.

**Gap:** `nearestPlayerAt` returns only `(x,y,z)`. TemptGoal needs the player's main-hand item. Add a variant that returns the player id, then a new handle op `world.player_main_hand(player_id)` → the player's selected hotbar `component.SlotData` item id. The player's inventory/held slot is `*tickPlayer` state (coordinator-shared read). **Folia note:** players are NOT in the per-region entity store; `t.players` is read on any region goroutine (the existing `nearestPlayerAt` already does this mid-tick). Reading a player's held item id (a scalar) is a race-clean read of stable-per-tick player state — same safety class as the existing position read.

### ItemTags membership

The deferred goals cite `ItemTags.PIG_FOOD` and `Items.CARROT_ON_A_STICK`. Item tag data ships in the jar and is codegen-extracted (the `tools/` pipeline already extracts registries/tags — see `server/registrydata/tags/`). v5 adds an item-tag lookup:

```go
// itemTagContains reports whether an item id is in the named tag (ItemTags.PIG_FOOD).
// Data sourced from the codegen'd item-tag tables (registrydata/tags/item/...), embedded once.
func itemTagContains(tag string, itemID int32) bool { ... }
```

The plugin TemptGoal calls a handle op `item_in_tag(item_id, "pig_food")`. **Folia:** tag data is immutable embedded data — read from any goroutine, lock-free. No hazard.

### New vs modified

| Component | New/Modified | File |
|-----------|--------------|------|
| `nearestPlayerAt` variant returning player id | MODIFIED/NEW | `ai_goals_passive.go` |
| `world.player_main_hand(id)` handle op | NEW | `plugin_entity.go` |
| `itemTagContains` + embedded item-tag data | NEW | `item_tags.go` (new) + `registrydata/tags/item/` |
| `world.item_in_tag` handle op | NEW | `plugin_entity.go` |
| TemptGoal plugin goal (×2: PIG_FOOD + CARROT_ON_A_STICK) | NEW | each animal plugin |

---

## 5. New Mobs as Plugins

### Each is a Starlark plugin via `declare_mob`/`goal`

The base types already resolve (`baseTypeByName`, `plugin_mob_decl.go:107` — cow/sheep/chicken/skeleton/spider/zombie/cat are ALREADY in the map; **wolf is NOT — add `"wolf": entity.Wolf`**). The attribute suppliers already exist (`level/attribute/defaults.go` — zombie/cow/sheep/chicken/skeleton/spider confirmed; **verify wolf has a supplier or add `wolfSupplier`**). Each mob follows the `vanilla_pig/main.star` shape exactly.

### Hostiles: the second (target) selector — DOES need a new selector instance

The vanilla `Mob` has TWO `GoalSelector`s: `goalSelector` (movement/look/jump) and `targetSelector` (TARGET-flag goals like `NearestAttackableTargetGoal`). The jar's `serverAiStep` order (documented at `ai_mob.go:6`) is:

```
sensing.tick → targetSelector.tick → goalSelector.tick
  → targetSelector.tickRunningGoals(true) → goalSelector.tickRunningGoals(true)
  → navigation.tick → customServerAiStep → moveControl/lookControl/jumpControl
```

Today `serverAiStep` SKIPS `targetSelector` (`ai_mob.go:107`: "no attack targets for a passive Pig"). **For hostiles, `mobAI` needs a second `goalSelector` field `targetSelector goalSelector`**, and `serverAiStep` must run it in the jar order:

```go
// mobAI (ai_mob.go) — ADD:
targetSelector goalSelector  // the TARGET-flag selector; empty for a passive mob (zero-cost)

// serverAiStep (ai_mob.go:105) — INSERT before goals.tick, in jar order:
m.targetSelector.tick(t, e)            // NearestAttackableTargetGoal etc.
m.goals.tick(t, e)
m.targetSelector.tickRunningGoals(t, e, true)
m.goals.tickRunningGoals(t, e, true)
```

**A second selector instance, NOT a second mechanism** — it reuses the EXISTING `goalSelector` type and arbitration. `buildAIFromDecl` (`plugin_mob_ai.go:185`) routes a goal whose flags include `TARGET` into `m.targetSelector` instead of `m.goals` (a one-line split on `gd.flags&flagTarget`).

The shared "current target" (the attack target a TargetGoal sets and MeleeAttackGoal reads) is new `*Entity`/`mobAI` state: `attackTargetID int32` (a THIN id — never a live `*Entity`, re-resolved on the owner). Folia-safe by the same id-carry discipline.

### MeleeAttackGoal — reuses the goalSelector + nav

`MeleeAttackGoal` (a normal MOVE-flag goal in `m.goals`) paths to the target via the EXISTING navigation, and when in attack-reach calls the damage op (§1, mob→player or mob→mob). Attack-reach lives as a goal constant in the plugin (vanilla computes it from `mob.getBbWidth()*2 + target.getBbWidth()` — port that). No new selector.

### Day/night spawn gating

Vanilla MONSTER spawns gate on light level + (some) on `isNight`. The spawner (`spawner.go`) currently relaxes light (no light engine, documented in `spawner.go`). v5 spawn gating for hostiles lives in `spawner.go`'s `naturalSpawn` / a per-category `checkSpawnRules`. **The MONSTER category cap (70) already exists** (`mob_category.go`, referenced in PROJECT.md). Add the night/light gate to the spawn-candidate filter; gametime→day/night is `t.gametime % 24000`. This is a server-side spawn-eligibility check, not a per-mob hot-path concern — Folia-safe (runs in the existing `naturalSpawn` which already handles the global cap via `countByCategoryAcrossRegions`).

### New vs modified

| Component | New/Modified | File |
|-----------|--------------|------|
| `mobAI.targetSelector` field + `serverAiStep` jar-order insert | NEW field, MODIFIED | `ai_mob.go` |
| `buildAIFromDecl` TARGET-flag split into targetSelector | MODIFIED | `plugin_mob_ai.go` |
| `*Entity.attackTargetID` (thin id) | NEW field | `entity.go` |
| `"wolf"` base type + `wolfSupplier` (if absent) | MODIFIED/NEW | `plugin_mob_decl.go`, `level/attribute/defaults.go` |
| day/night + light spawn gate | MODIFIED | `spawner.go` |
| cow/sheep/chicken/zombie/skeleton/spider/wolf plugins | NEW | `plugins/*/main.star` |
| MeleeAttackGoal, NearestAttackableTargetGoal, wolf goals | NEW (plugin goals) | each plugin |

---

## 6. Suggested Build Order (dependency-driven)

```
Phase A — DAMAGE KEYSTONE (combat_mob.go + damage_source.go + *Entity fields)
   └─ everything downstream needs a mob that can take/deal damage
   └─ deliver: applyDamageEntity/actuallyHurtEntity, lastDamageSource, damageSource+tags,
              cross-region damageIntent barrier queue, handleAttack dual-resolve,
              entity handle was_hurt/last_damage_type attr, region-scoped on_damage Emit
   └─ GATE: a player can hit a spawned (Go-native) mob; mob takes i-frame-gated damage;
            on_damage fires; lastDamageSource is set. PIG ORACLE STILL GREEN (no AI RNG touched).

Phase B — JUMPCONTROL + FLUID (ai_mob.go jumpControl, fluid_mob.go, FloatGoal)
   └─ independent of A; sequence after A to keep the pig-plugin churn serial
   └─ deliver: mobAI.jumpControl + jump impulse, mobIsInWater/mobFluidHeight, nav.jump()/
              entity.in_water handle ops, FloatGoal on the pig plugin (+ the Go-native oracle)
   └─ GATE: pig FloatGoal@0 wired; pig in water jumps; FloatGoal claims JUMP via goalSelector.
            RNG RISK: FloatGoal draws nextFloat() < 0.8 every tick it runs — see §7.

Phase C — PANICGOAL (depends on A's lastDamageSource)
   └─ deliver: PanicGoal@1 on the pig plugin (+ oracle) reading entity.was_hurt/last_damage_type
              + the panic-causing damage-type tag set
   └─ GATE: a hit pig panics (flees). PanicGoal@1 wired.

Phase D — HELD-ITEM + ITEM TAGS (item_tags.go, player_main_hand op)
   └─ independent of A/B/C; needed before breeding (Tempt feeds → in_love)
   └─ deliver: itemTagContains + embedded tag data, world.player_main_hand,
              world.item_in_tag, nearestPlayer-with-id
   └─ GATE: pig TemptGoal@4 ×2 wired (+ oracle); pig follows a player holding carrot/pig-food.

Phase E — AGING + BREEDING (entity.go age/inLove, aging.go, spawn_baby, BreedGoal+FollowParent)
   └─ depends on D (feeding sets in_love) and reuses spawnDeclaredMob
   └─ deliver: age/inLove fields + tickMobAging, same-region partner search op,
              spawn_baby, BreedGoal@3 + FollowParentGoal@5 on the pig (+ oracle)
   └─ GATE: two fed pigs breed a baby; baby follows parent. PIG PARITY COMPLETE (all 5 deferred
            goals wired) — the DOGFOOD GATE (TestPluginPigEqualsGoNativePig over the full 8-goal set).

Phase F — NEW PASSIVE MOBS (cow/sheep/chicken plugins; reuse A–E subsystems)
   └─ pure plugin authoring against the now-complete subsystem set; no new Go subsystems
   └─ GATE: cow/sheep/chicken spawn, wander, breed, panic, float — all via plugins.

Phase G — HOSTILES (mobAI.targetSelector, serverAiStep jar-order, buildAIFromDecl split,
                     attackTargetID, day/night spawn gate, zombie/skeleton/spider plugins)
   └─ depends on A (mob→player damage) + the targetSelector insert
   └─ deliver: second selector, target-flag routing, MeleeAttackGoal, NearestAttackableTargetGoal,
              day/night spawn gating
   └─ GATE: zombie targets + chases + hits a player at night; respects MONSTER cap.

Phase H — WOLF (neutral; depends on A's lastDamageSource for anger-on-hit + G's target machinery)
   └─ deliver: wolf base type + supplier, tame/owner/sit/anger plugin goals
   └─ GATE: wolf tames, sits, retaliates when hit (reads lastDamageSource.attacker).
```

**Rationale chain:** Damage is the keystone (PanicGoal, hostiles, wolf-anger all read `lastDamageSource`). Held-item precedes breeding (feeding sets `in_love`). Pig parity (Phase E) is the dogfood gate — it must complete before new mobs (F/G/H) so the subsystem set is proven 1:1 against the oracle before reuse. Hostiles need the target-selector; wolf needs both damage and the target machinery, so it's last.

---

## 7. THE BIT-FRAGILE PIG ORACLE — explicit risk + mitigation

`TestPluginPigEqualsGoNativePig` (`plugin_pig_test.go:256`) ticks a Go-native pig and a plugin pig in lockstep for 500 ticks and demands byte-identical observations. Both pigs draw from a per-mob seeded RandomSource (`entityRandom`, `ai_mob.go:53`) whose DRAW ORDER must match the jar. **Any new RNG draw inserted into the shared `serverAiStep`/goal flow at the wrong point shifts every subsequent draw and breaks the oracle.**

### Where new draws are SAFE vs DANGEROUS

| New draw | Where | Safe? | Why |
|----------|-------|-------|-----|
| Aging decrement (age/inLove--) | `tickMobAging` | SAFE | no RNG — pure integer decrement |
| FloatGoal `nextFloat() < 0.8` | inside FloatGoal.tick (running only) | SAFE **IF** the Go-native oracle ALSO ports FloatGoal with the identical draw | oracle pig + plugin pig must BOTH gain FloatGoal in the same draw position |
| PanicGoal flee-pos draws | inside PanicGoal.canUse (running only) | SAFE same condition | both oracle + plugin must port it identically |
| BreedGoal partner search | inside BreedGoal.canUse | DANGEROUS if it draws RNG | vanilla BreedGoal.canUse does a deterministic scan (no RNG) for the partner; the baby's attributes draw RNG only at spawn — keep spawn-RNG OUT of the 500-tick observation window or seed it identically on both sides |
| TemptGoal | inside TemptGoal.canUse | SAFE same condition | port identically into BOTH oracle + plugin |

### The governing rule

**The Go-native pig oracle (`newPigAI`, `ai_mob.go:138`) and the plugin pig (`vanilla_pig/main.star`) must gain each new goal IN LOCKSTEP, with the identical jar-faithful draw order, in the SAME phase.** The oracle is not a frozen 3-goal pig — it must grow to the full 8-goal pig alongside the plugin. The deferred-goals.md audit already pairs each goal to its jar class + draw order; port each goal's draws into BOTH sides in the same plan. The dogfood gate (Phase E) is exactly the proof that the lockstep held.

**Do NOT** add any unconditional per-tick RNG draw to `serverAiStep`, `tickAI`, or `tickPhysics` (none exists today — confirmed: the only draws are inside running goals). New per-tick mob updates (aging) must be RNG-free. New RNG must live INSIDE a goal's running callback, ported identically on both sides.

### Secondary fragility — `serverAiStep` order

Inserting the `targetSelector` calls (§5) changes `serverAiStep` for ALL mobs including the pig. **Mitigation:** the pig's `targetSelector` is EMPTY (zero goals), and `goalSelector.tick` over an empty selector draws no RNG and starts no goal — so the inserted `m.targetSelector.tick(t,e)` is a no-op for the pig. Verify with a test that an empty targetSelector tick is observationally identical (it is, by construction: empty `goals` slice → both loops in `goalSelector.tick` iterate zero times). This keeps the order change behavior-neutral for the pig oracle while enabling hostiles.

---

## 8. Integration Points Summary (file:function quick-reference)

| v5 subsystem | Hooks into (existing) | New file(s) |
|--------------|----------------------|-------------|
| Mob damage | `attack_dispatch.go:91 handleAttack`; `combat.go` CombatRules helpers (reuse); `region_transfer.go:86 emitEntityEvent` | `combat_mob.go`, `damage_source.go` |
| JumpControl/fluid | `ai_mob.go:105 serverAiStep` (JUMP slot), `:29 mobAI`; `fluid.go:246 fluidAt`; `entity.go:214 AABB` | `fluid_mob.go` |
| Aging/breeding | `entity.go *Entity`; `plugin_mob_decl.go:385 spawnDeclaredMob` (reuse); `region_transfer.go:143 entitiesNearAcrossRegions` (same-region cut); `spawner.go:417 near` | `aging.go` |
| Held-item/tags | `ai_goals_passive.go:244 nearestPlayerAt`; `plugin_entity.go` handle attrs; `registrydata/tags/item/` | `item_tags.go` |
| New mobs | `plugin_mob_decl.go:107 baseTypeByName` (+wolf); `plugin_mob_ai.go:185 buildAIFromDecl` (TARGET split); `ai_mob.go serverAiStep` (targetSelector); `spawner.go naturalSpawn` (day/night); `level/attribute/defaults.go` | `plugins/{cow,sheep,chicken,zombie,skeleton,spider,wolf}/main.star` |

### Internal Boundaries

| Boundary | Communication | Folia rule |
|----------|---------------|-----------|
| Plugin goal ↔ mob | thin-id `entityHandle` (`plugin_entity.go:35`), re-resolve `store().get(id)` on owner | NEVER a live `*Entity` crosses the Starlark boundary |
| Mob ↔ mob (breed/target) | thin id (`attackTargetID`, partner id), re-resolved on owner; same-region search only | no cross-region store read mid-tick |
| Player→mob / mob→player attack | barrier-queued `damageIntent` onto the victim's owning region | no cross-region health mutation mid-tick |
| New `*Entity` state ↔ region | the `*Entity` pointer is adopted at the barrier (`applyCrossRegionTransfers`) | all new fields travel with the pointer, never aliased mid-tick |
| Entity event ↔ plugin | `emitEntityEvent` (region-scoped) | registry shared+frozen; handles region-bound |

---

## Anti-Patterns (v5-specific)

### Anti-Pattern 1: Generalizing combat.go in place with an interface
**What people do:** make `applyDamage` take a `LivingEntity` interface so player + mob share one path.
**Why it's wrong:** the two paths read DIFFERENT attribute backends (player holder vs `*attribute.Map`), drive DIFFERENT death (death-screen packet vs despawn), and the player path is v3-sealed + tested. An interface forces both through a new abstraction and risks the player oracle.
**Do this instead:** parallel `applyDamageEntity` sibling; factor only the pure CombatRules/clamp helpers (already free functions).

### Anti-Pattern 2: Cross-region partner/target/damage mutation mid-tick
**What people do:** a goal callback calls `entitiesNearAcrossRegions` or mutates another region's entity directly during the fan-out.
**Why it's wrong:** races the other region's concurrent tick; `-race` fails; the checkerboard split means an adjacent column is the OTHER region, so this fires constantly.
**Do this instead:** same-region search (`h.region.entities.near`) for partner/target; barrier-queued `damageIntent` for cross-region hits — the `transferIntent`/`asyncIn2` discipline.

### Anti-Pattern 3: Adding RNG to the shared per-tick flow
**What people do:** add a `nextFloat()` somewhere in `serverAiStep`/`tickAI` for a new behavior.
**Why it's wrong:** shifts the pig oracle's draw stream → `TestPluginPigEqualsGoNativePig` breaks.
**Do this instead:** RNG ONLY inside a running goal's callback, ported identically into BOTH the Go-native oracle pig and the plugin pig in the same plan; per-tick mob updates (aging) stay RNG-free.

### Anti-Pattern 4: Faking a hurt flag / faking jumpControl
**What people do:** a bool `wasHurt` the goal reads, or a raw `vy` poke for FloatGoal.
**Why it's wrong:** the deferred-goals.md audit explicitly forbids it — it's not the vanilla `getLastDamageSource()`/`JumpControl.jump()`, violating the 1:1 mandate.
**Do this instead:** the real ported `lastDamageSource` set by `applyDamageEntity`; the real `jumpFromGround` impulse via the `jumpControl` latch.

---

## Sources

- `server/combat.go`, `attack_dispatch.go`, `attributes.go` — the player damage pipeline + the dual attribute system (read this session) — HIGH
- `server/ai_mob.go`, `ai_goal.go`, `navigation.go`, `tick_phases.go` — AI driver, goalSelector, jumpControl deferral, tickAI call site — HIGH
- `server/fluid.go` — `fluidAt` raw read primitive — HIGH
- `server/plugin_mob_decl.go`, `plugin_mob_ai.go`, `plugin_entity.go` — declare_mob/spawn path, thin-id handle bridge, region-bound handles — HIGH
- `server/region_transfer.go`, `spawner.go` — Folia routing, cross-region scan, barrier transfer, mob cap — HIGH
- `plugin/host/event.go` — the 8 EventType set + DamageEvent payload — HIGH
- `server/entity.go` — `*Entity` fields + the snapshot-friendly contract + real `*attribute.Map` — HIGH
- `.planning/milestones/v4-phases/24-vanilla-mobs-as-plugins/deferred-goals.md` — the 5 deferred goals + cited missing subsystems — HIGH
- `plugins/vanilla_pig/main.star` — the plugin shape new mobs follow — HIGH
- `level/attribute/defaults.go` (grep) — existing suppliers (zombie/cow/sheep/chicken/skeleton/spider confirmed; wolf to verify) — HIGH
- `.planning/PROJECT.md` — milestone goals, thin-id + regionize decisions — project ground truth

---
*Architecture research for: v5 mob subsystem integration into Sulfur*
*Researched: 2026-06-29*
