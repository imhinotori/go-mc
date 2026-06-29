---
phase: 29-damage-keystone-s2
reviewed: 2026-06-29T00:00:00Z
depth: standard
files_reviewed: 16
files_reviewed_list:
  - server/combat_mob.go
  - server/damage_source.go
  - server/death_mob.go
  - server/attack_dispatch.go
  - server/entity.go
  - server/region_transfer.go
  - server/region_coordinator.go
  - server/tick_phases.go
  - server/plugin_entity.go
  - server/plugin_mob_decl.go
  - server/structure_spawn.go
  - server/vanilla_pig.go
  - server/entity_encode.go
  - level/loot/context.go
  - level/loot/condition.go
  - tools/java/GenTags.java
findings:
  blocker: 1
  high: 1
  medium: 2
  low: 3
  total: 7
status: fixed
resolution:
  fixed_at: 2026-06-29
  fixed:
    - CR-01  # spawn health init centralized in initSpawnHealth, called from all 3 spawn paths + regression test
    - WR-01  # spawnVanillaPigWithID now calls the shared initSpawnHealth helper
    - WR-02  # death XP reward draws from the mob RNG (mobRandom(e).nextInt(3)); loot seed left server-generated (verified seedless getRandomItems)
    - WR-03  # i-frame decrement now covers all living entities (full store), not just e.ai != nil
  deferred:
    - IN-01  # death-region owningRegion-vs-column divergence: latent only (ordering currently agrees) — deferred
    - IN-02  # GenTags namespace-stripping: codegen output correct for current all-minecraft input — deferred
    - IN-03  # XP-orb ctor velocity draws: blocked on orb metadata/velocity subsystem (cited deferral) — deferred
---

# Phase 29: Damage Keystone (S2) — Code Review Report

**Reviewed:** 2026-06-29
**Depth:** standard (per-file, jar-fidelity focus)
**Files Reviewed:** 16
**Status:** issues_found

## Summary

The core hurt pipeline (`combat_mob.go` `applyDamageEntity`/`actuallyHurtEntity`) is a high-fidelity 1:1 port of `LivingEntity.hurtServer`/`actuallyHurt`: every op is `float32`, the `>10.0F` i-frame gate, the `amount<=lastHurt` no-op, the `=20` arm, the `flag2` `lastDamageSource` store, the three mob tail-diffs (no `causeFoodExhaustion`, no `SetHealth` send, region-scoped Emit) are all correct, and the combat.go free helpers (`combatRulesGetDamageAfterAbsorb`, `maxF`, `isNaN32`/`isInf32`, the hurt* constants) are genuinely REUSED, not duplicated. The `BYPASSES_ARMOR` branch is a real `src.is("bypasses_armor")` read against a jar-extracted, recursively-flattened tag table (verified: `bypasses_armor` contains id 10 = `fall`; `is_player_attack` = {26,34,37}). The cross-region `damageIntent` barrier-queue is correctly modeled on `transferIntent` — queued on the source region, drained at `applyCrossRegionDamage` next to `applyCrossRegionTransfers`, owner re-resolved by id (drop-if-gone), never via `cur()`. The `tickMobIFrames` decrement is RNG-free integer math placed before `serverAiStep`, oracle-safe. CGO=0 is preserved (no new deps). Cited deferrals (looting/smelts_loot/killed_by_player, XP-orb DATA_VALUE) are honestly documented and equal vanilla defaults.

The defects below are concentrated in the **spawn-side health initialization** (which silently defeats the keystone for two of three spawn paths) and a couple of **RNG-stream fidelity** deviations the tests cannot catch.

## Blocker Issues

### CR-01: Structure-spawned mobs are born with `health = 0` and cannot take damage

**File:** `server/structure_spawn.go:48-70`
**Issue:** `drainStructureSpawns` builds a mob via `NewEntity` + `attribute.FinalizeSpawn` but NEVER sets `e.health`. `NewEntity` (`server/entity.go:220-237`) does not initialize `health`, so it defaults to `0`. The keystone's entry guard in `applyDamageEntity` (`combat_mob.go:55`) is `if e.health <= 0 { return }`, and `applyMobAttackDamage` (`attack_dispatch.go:361`) returns `false` for `mob.health <= 0`. The net effect: every structure-spawned mob (Witch HP 26.0, Cat 10.0, Villager, etc.) is **permanently invulnerable** — a player hit is a silent no-op, and the mob is treated as already-dead. This directly contradicts MOB-SUB-01 ("a mob can take and deal damage") for the structure-spawn path. Only the `spawnDeclaredMob` path (`plugin_mob_decl.go:393`) initializes health; that is the only correct path of the two production spawners.

This is the port of `LivingEntity.<init>`'s `setHealth(getMaxHealth())` — it must run on EVERY mob spawn, not just declared mobs.

**Fix:** Initialize health from MaxHealth at the structure-spawn site, exactly as `spawnDeclaredMob` does:
```go
e := NewEntity(t.idAlloc.AllocID(), rec, req.X, req.Y, req.Z)
e.leftHanded = attribute.FinalizeSpawn(e.attributes, t.cur().levelRandom)
// LivingEntity.<init>: setHealth(getMaxHealth()) — getMaxHealth() == (float) getAttributeValue(MAX_HEALTH).
e.health = float32(e.getAttributeValue(attribute.MaxHealth))
```
Better: fold the `health = getMaxHealth()` init into a single shared helper (or into `NewEntity` for living types) so no future spawn path can reintroduce the gap. There is currently no test covering damage to a structure-spawned mob, which is why the suite passes despite this.

## High

### WR-01: Test-only `spawnVanillaPigWithID` omits health init — the oracle pig cannot exercise the damage/death path

**File:** `server/vanilla_pig.go:54-67`
**Issue:** `spawnVanillaPigWithID` (used by `TestPluginPigEqualsGoNativePig` and other oracle tests to pin a deterministic id) mirrors `spawnDeclaredMob` (NewEntity → seedAttributes → buildAIFromDecl → add → reseed) but DROPS the `e.health = float32(e.getAttributeValue(attribute.MaxHealth))` line that `spawnDeclaredMob` performs at `plugin_mob_decl.go:393`. Any pig spawned through this helper has `health = 0`. While the pig oracle never kills the pig (so the green oracle is not a contradiction), this means the keystone's damage/death behavior is **untestable on the canonical oracle fixture** — and any future test that hits an oracle-spawned pig will see a silent no-op rather than damage. It is the same root cause as CR-01 (health init not centralized).

**Fix:** Add the health init to mirror `spawnDeclaredMob`:
```go
e := NewEntity(id, decl.baseType, x, y, z)
seedAttributes(e.attributes, decl.attrs)
e.health = float32(e.getAttributeValue(attribute.MaxHealth)) // LivingEntity.<init> setHealth(getMaxHealth())
e.ai = buildAIFromDecl(t, decl)
```
Centralizing the init (see CR-01 fix) resolves both at once.

## Medium

### WR-02: Death XP reward uses the global RNG, not the mob's own `random` stream — a 1:1 RNG-fidelity deviation

**File:** `server/death_mob.go:261` (`entityBaseExperienceReward`), also `:157` (loot seed) and `:175` (item toss via `NewItemEntity`)
**Issue:** The port claims to be `Animal.getBaseExperienceReward == 1 + this.random.nextInt(3)`, but the implementation draws `1 + rand.IntN(3)` from the **global** `math/rand/v2` pool, not the mob's per-entity RNG (`e.ai.rng`, the documented `Mob.getRandom()` analogue used everywhere else — see `plugin_entity.go:378,395`). Per CLAUDE.md the port must "mirror the vanilla call chain and numeric ops EXACTLY — including ... RNG draw order"; `this.random.nextInt(3)` is a draw on the mob's own stream, not a process-global draw. The 04-SUMMARY frames this as acceptable because it is event-time and outside the 500-tick oracle window — true for the green oracle, but it is still a deviation from the mandated RNG source, and it makes the XP amount non-deterministic for a fixed-seed mob (a future death-determinism test would fail). The loot seed (`rand.Int64()`) and the `NewItemEntity` toss velocity (global `rand.Float64()`) share the issue; vanilla draws those from the entity's level RNG.

**Fix:** Draw from the mob's own RNG so the value matches the jar's `this.random.nextInt(3)`:
```go
func (t *TickLoop) entityBaseExperienceReward(e *Entity) int {
    if e.ai == nil || e.ai.rng == nil {
        return 1 // defensive: an AI-less mob has no per-entity stream
    }
    return 1 + e.ai.rng.nextInt(3) // Animal.getBaseExperienceReward: 1 + this.random.nextInt(3)
}
```
Apply the same to the loot seed (seed from the mob/level RNG rather than the global pool). If the per-entity RNG cannot be the source for some types, cite the exact reason inline rather than silently using the global pool.

### WR-03: `tickMobIFrames` only runs for mobs with `e.ai != nil` — i-frames never decrement for an AI-less living entity

**File:** `server/tick_phases.go:297-311`
**Issue:** The i-frame decrement loop iterates `snapshot`, which is populated only with entities where `e.ai != nil` (`tick_phases.go:298-301`). In vanilla the `baseTick` `hurtTime--` / `invulnerableTime--` block runs for EVERY `LivingEntity`, independent of whether it has a `goalSelector`. A living mob that takes damage but has no AI goals would have its `invulnerableTime` armed to 20 on a hit and then **never decremented**, leaving it permanently in the upper i-frame half (effectively immune to all but ever-increasing damage). For v1 all damageable mobs carry AI, so this is latent rather than live — but it is a fidelity gap that will surface the first time a no-AI living entity is added, and it is structurally coupling the i-frame countdown to AI presence, which vanilla does not.

**Fix:** Decrement i-frames for all living entities (those with a health/MaxHealth attribute or a dedicated `isLiving` flag), not just AI-bearing ones — e.g. iterate the full store and gate on a living predicate rather than `e.ai != nil`. Keep it before `serverAiStep` and integer-only (the oracle-safe placement is fine).

## Low

### IN-01: `dieEntity` resolves the death region via `regionForEntity` (column) while the barrier resolved the owner via `owningRegion` (store) — latent divergence

**File:** `server/death_mob.go:91,176,283` vs `server/region_transfer.go:266` (`applyCrossRegionDamage`)
**Issue:** On the cross-region path, `applyCrossRegionDamage` enters `withRegion(owner)` where `owner = owningRegion(victimID)` (the store-holding region). Inside, `dieEntity`/`dropMobLoot`/`awardExperienceOrbs` route store mutations through `regionForEntity(e)` = `regionForColumn(columnOf(e.x,e.z))` (the column-derived region). These agree only while a mob's store-region equals its column-region. Because `applyCrossRegionDamage` runs AFTER `applyCrossRegionTransfers`, ownership is settled and they currently match, so this is not a live bug — but the two resolution strategies are silently assumed equivalent. If transfer ordering ever changes, the loot/removal would target a different region than the one holding the entity (the remove would be a no-op on the wrong store, leaking the dead mob).

**Fix:** Resolve the death region once via the same `owningRegion(e.id)` the barrier used (or pass the owner region down into `dieEntity`), rather than re-deriving from the column. This makes the routing robust to any future transfer-ordering change.

### IN-02: `GenTags.stripNamespace` collapses all tag refs to the bare path — non-`minecraft` datapack refs could collide

**File:** `tools/java/GenTags.java:142,151-154`
**Issue:** `resolve` follows a `#`-ref by `stripNamespace`-ing it and looking the bare name up in the same family map. A non-`minecraft`-namespaced tag ref (e.g. `#sulfur:foo`) would lose its namespace and could collide with a `minecraft:foo` tag of the same path. The 26.2 jar's damage_type/item tags are all `minecraft`-namespaced, so the generated output is correct today (verified against `data/tag/tags.go`), but the flattener is not namespace-safe for a mixed-namespace datapack.

**Fix:** Key the family maps by full namespaced id and resolve refs without stripping the namespace, or assert/skip non-`minecraft` refs explicitly. Low priority — codegen output is correct for the current input.

### IN-03: XP-orb spawn omits the vanilla ctor velocity draws (cited deferral, noted for completeness)

**File:** `server/death_mob.go:288`
**Issue:** `awardExperienceOrbs` spawns `NewEntity(..., entity.ExperienceOrb, ...)` with no velocity and no carried XP value. Vanilla `ExperienceOrb.<init>` sets a random launch velocity (RNG draws) and the orb carries its value via `SynchedEntityData DATA_VALUE`. The summary cites the DATA_VALUE as deferred (acceptable per CLAUDE.md), but the missing ctor velocity draws are an unstated RNG/behavior gap (the orb is stationary vs. vanilla's tossed orb). The reward AMOUNT and orb COUNT (via the exact `getExperienceValue` cap table) are correct.

**Fix:** When the orb metadata/velocity subsystem lands, port `ExperienceOrb.<init>`'s velocity draws and the `DATA_VALUE` accessor; until then, add an explicit citation for the omitted velocity draw alongside the existing DATA_VALUE citation so the deferral is complete.

---

## Fidelity checks that PASSED (no findings)

- `actuallyHurtEntity` tail diffs vs `Player.actuallyHurt`: NO `causeFoodExhaustion`, NO `SetHealth` client send, region-scoped `emitEntityEvent` — all correct (`combat_mob.go:160-184`).
- combat.go helper reuse: `combatRulesGetDamageAfterAbsorb`/`maxF`/`isNaN32`/`isInf32`/`mthClampF`/`hurt*` constants are called, not redefined, in `combat_mob.go`. No duplicated armor curve.
- `BYPASSES_ARMOR`/`bypasses_cooldown`/`bypasses_effects`/`bypasses_enchantments` are genuine `src.is(tag)` reads against the generated table (`damage_source.go:62`, `combat_mob.go:80,211,236,248`) — never `const false`.
- Tag table fidelity: recursive nested-`#` flatten verified (`panic_causes` ⊇ `panic_environmental_causes` members; `bypasses_armor` ∋ `fall`=10; `is_player_attack`={26,34,37}).
- Cross-region routing: queued on the source region (`queueDamageIntent`, `region_transfer.go:186`), drained at `applyCrossRegionDamage` (`region_transfer.go:254`) with `owningRegion` re-resolve + drop-if-gone, inside `withRegion(owner)`, never `cur()`. handleAttack runs in `resolveSubtickInputs` on the coordinator (quiescent), so the same-region fast path is also race-safe.
- Reach gate enforced for mob victims (`withinAttackReachEntity`, `attack_dispatch.go:241-243,342-347`); forged/despawned id → nil owner → silent no-op; server computes the damage amount (client only names the target). T-6-05 / T-6-01 / T-29-01 held.
- `lastDamageSource` set only in the `flag2` block (`combat_mob.go:104-106`), matching hurtServer bytecode 444-462. `was_hurt`/`last_damage_type` return frozen scalars (`plugin_entity.go:184,190`).
- i-frame decrement is integer-only, before `serverAiStep`, outside goal/navigation RNG — oracle-safe.
- Loot entity-context conditions (`killed_by_player`, `entity_properties`, `any_of`/`all_of`) and functions (`furnace_smelt`, `enchanted_count_increase`) are genuinely ported reading cited-stub context fields at the vanilla default — a real bounded extension, not a faked/baked value.
- Double-death guard (`dieEntity` `if e.dead return`) and death-status-3 broadcast (`entityEventDeath = 3`, `encodeEntityEvent` writeInt+writeByte) are jar-faithful.
- CGO=0 preserved; no new Go deps introduced.

---

_Reviewed: 2026-06-29_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
