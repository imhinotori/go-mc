# Phase 29: Damage Keystone (S2) - Context

**Gathered:** 2026-06-29
**Status:** Ready for planning

<domain>
## Phase Boundary

Build the parallel `*Entity` mob damage/hurt pipeline — the keystone every PanicGoal, target-selector,
melee, and wolf-anger behavior reads. A player can hit a Go-native mob and it loses health with
jar-identical i-frame/invulnerable-time gating; the mob records its real `lastDamageSource`; damage-type
tag membership (`source.is(tag)`) is genuine against a jar-extracted table; the `on_damage` Emit fires
post-mitigation; cross-region hits route through the OWNER's region as a barrier-queued `damageIntent`.

Delivers REQ MOB-SUB-01 (mob take/deal damage), MOB-SUB-02 (`lastDamageSource`), MOB-SUB-03 (damage-type
tag table). Standing constraints: every op `float32`, every clamp/branch verbatim vs the jar (javap
before writing); reuse `combat.go`'s CombatRules/clamp helpers (do NOT duplicate); CGO=0 + no new Go
deps; the pig oracle `TestPluginPigEqualsGoNativePig` stays GREEN (no AI RNG touched).
</domain>

<decisions>
## Implementation Decisions

### Damage-type tag extraction (Grey Area 1)
- **Build the full codegen tag tooling NOW (both tag families).** Add a `GenTags.java` extractor +
  `gen_tags.go` generator emitting BOTH the `minecraft:damage_type` tags (`PANIC_CAUSES`,
  `BYPASSES_ARMOR`, `IS_FIRE`, …) AND the item tags (`PIG_FOOD`, `COW_FOOD`, `WOLF_FOOD`,
  `Items.CARROT_ON_A_STICK`, …) into `data/tag/`. This front-loads the shared tag tooling so Phase 32
  (Held-Item + Item Tags / TemptGoal) reuses it with no new extractor — at the cost of a slightly larger
  Phase 29. Mirror the existing `tools/gen_item_food.go` / `tools/gen_registryid.go` pattern; register in
  `tools/main.go`. `datapack.json` already exposes `minecraft:damage_type {elements,tags:true}` — extract
  the membership maps (datapack/registries report is authoritative, no reflective mid-tick extraction).
- Phase 29 CONSUMES only the damage-type tags (armor-bypass + panic-causing branches); the item tags are
  generated-but-not-yet-wired here (their consumer lands in Phase 32) — that is intentional shared-tooling
  front-loading, NOT a no-built-but-unwired violation (the data is real + tested headless).

### Mob death + loot drops + XP (Grey Area 2)
- **Full `LivingEntity.die()` now — death removal + loot drops + XP.** When mob health ≤ 0, port the real
  `LivingEntity.die()` / `Mob.dropFromLootTable` / `dropExperience` chain 1:1: remove the entity + death
  broadcast (the EntityEvent death-status / RemoveEntities packets), roll the mob's loot table into Item
  entities (reuse the shared loot evaluator built in v3 STRUCT-POLISH — confirm it generalizes from
  chest/block-drop loot to entity loot tables), and drop the XP orb.
- **Scope note (force at plan time):** this pulls the mob loot-table subsystem into Phase 29. The plan
  MUST javap-verify the mob loot-table id resolution (`Mob.getLootTable`) + `dropFromLootTable` and confirm
  the v3 loot evaluator generalizes; if the entity-loot path is a large net-new subsystem, the planner
  should split it into its own plan within Phase 29 (death-removal plan + loot/XP plan) so the keystone
  hurt pipeline is not blocked by loot tooling. Death REMOVAL is the hard requirement; loot/XP ride with it.

### lastDamageSource representation (Grey Area 3)
- **Real `DamageSource{type, attacker?}` 1:1.** Port the real `DamageSource` — a damage-type id + an
  optional attacker entity id (thin-id, never a live pointer) — stored as `Entity.lastDamageSource`.
  `source.is(tag)` = damage-type-id ∈ the jar-extracted tag set. This unblocks PanicGoal (Phase 31, tag
  check) AND wolf anger-on-hit (Phase 36, attacker read) with NO later refactor. Satisfies the
  deferred-goals.md "do NOT fake a hurt flag" rule — it is the real ported source.
- Exposed to goals via a `was_hurt` / `last_damage_type` handle attr returning frozen scalars across the
  Starlark boundary (the host-side read; the goal never holds a live source).

### Claude's Discretion
- The exact split of Phase 29 into plans (the planner decides — likely: tag codegen → entity hurt-pipeline
  fields+`actuallyHurtEntity` → attack routing + cross-region `damageIntent` → death/loot/XP).
- Whether to add a small minimal damage-type entry table (exponent/scaling/message-id) if the existing
  registry extraction surfaces only IDs — verify at plan time; the `DamageSource` math is a javap port
  regardless.
</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets (scouted — file:line)
- **`server/combat.go`** — REUSE the generic free functions: `actuallyHurt` armor/absorption fold (l.262),
  `combatRulesGetDamageAfterAbsorb` (l.543), `combatRulesGetDamageAfterMagicAbsorb` (l.562), `mthClampF`
  (l.128), `maxF` (l.603), `isNaN32`/`isInf32` (l.613-614). I-frame constants `hurtCooldownConst=10.0`
  (l.141), `hurtInvulnerableTicks=20` (l.146), `hurtDurationTicks=10` (l.150). The `on_damage` Emit fires
  post-mitigation at l.310-315. `applyDamage`/`actuallyHurt` are currently `*tickPlayer`-typed — build the
  `*Entity` sibling, reuse the free helpers.
- **`server/entity.go`** — `Entity.getAttributeValue(attr *attribute.Attribute)` (l.200) ALREADY reads the
  mob's `*attribute.Map` (l.135, seeded at spawn via `attribute.NewMapForEntity`) with a `DefaultValue()`
  fallback — reuse 1:1. MUST ADD to `*Entity`: `health float32`, `invulnerableTime int32`, `lastHurt
  float32`, `hurtTime int32`, `lastDamageSource` (these live on `tickPlayer` today, not `*Entity`).
- **`level/attribute/`** — singletons `MaxHealth`(20.0)/`Armor`/`ArmorToughness`/`MaxAbsorption`/
  `KnockbackResistance` exist with 1:1 vanilla defaults; the Map/Instance fold machinery is generic.
- **`server/attack_dispatch.go`** — `handleAttack` (l.91) resolves ServerboundAttack but routes ONLY to
  `tickPlayer` victims (l.97 `lookupPlayerByEntityID`); `handleInteract` is a no-op stub (l.540).
  `applyAttackDamage(victim, amount) bool` (l.214) is the hurtOrSimulate bridge gating knockback/sweep.
  BUILD: route to `*Entity` when the id is a mob; an `applyMobAttackDamage(victim *Entity, amount) bool`
  mirror; `knockback` (l.385).
- **`server/region_transfer.go`** — `regionOf` checkerboard hash (l.43); `transferIntent{ent,to}` (l.160);
  `detectTransfers` on the region goroutine (l.171); `applyCrossRegionTransfers` drains at the barrier,
  quiescent (l.193) — the entity pointer (ai/attributes/new health fields) travels with it. `region.
  emitEntityEvent(event, payload)` (l.86) is the region-scoped Emit. MIRROR this pattern for a
  `damageIntent` queued onto the OWNER's region, drained at the barrier (never the actor's `cur()`).
- **`plugin/host/event.go`** — `EventDamage="on_damage"` (l.23), `DamageEvent{EntityID,Amount}` (l.155),
  fired post-mitigation. Reuse for mobs with the mob entity id + post-mitigation amount.
- **`tools/`** — `tools/main.go` generator list (l.29); `gen_item_food.go` (the map-emit pattern to mirror);
  `gen_registryid.go` emits `minecraft:damage_type` registry (l.76); `datapack.json` l.122 confirms
  `damage_type {elements:true,tags:true}`. NO tag-membership extractor exists yet — build `GenTags.java` +
  `gen_tags.go`.
- **v3 loot evaluator** (STRUCT-POLISH) — the shared chest/block-drop loot roller; confirm it generalizes
  to entity loot tables for the death/drops decision.

### Established Patterns
- Parallel `*Entity` sibling, NOT interface-generalize (dual attribute systems: `tickPlayer`
  `attributeHolder` vs `*Entity` `*attribute.Map`) — reuse only pure free helpers.
- Cross-region mutation → barrier-queued intent routed to the OWNER's region (the `transferIntent`
  discipline), never a mid-tick cross-region write.
- Thin-id handles across the Starlark boundary; host-side reads return frozen scalars.
- Codegen-from-jar for all static data (tags), the report/registry JSON is authoritative.

### Integration Points
- `attack_dispatch.go handleAttack` — dual-resolve (player OR mob victim).
- `Entity` struct — new tick-owned hurt fields + `lastDamageSource`.
- `region` — new `damageIntent` queue + barrier drain; `emitEntityEvent` for `on_damage`.
- `tools/main.go` — register the new tag generator; `data/tag/` new package.
</code_context>

<specifics>
## Specific Ideas

- The user deliberately chose the BROADER scope on all three areas (full tag tooling + death/loot/XP +
  rich DamageSource) — bigger keystone now, less rework across Phases 31/32/36 later. Honor that: build
  the reusable tooling properly rather than the minimal slice.
- Verify EVERY ported op against the jar bytecode before writing (`LivingEntity.hurtServer`/`actuallyHurt`/
  `die`/`dropFromLootTable`/`dropExperience`, `DamageSource`, `DamageTypeTags`, `Mob.getLootTable`). Cite
  the class/method.
</specifics>

<deferred>
## Deferred Ideas

- Item-food tags are GENERATED in Phase 29 but WIRED in Phase 32 (TemptGoal) — shared-tooling front-load,
  not a Phase-29 deliverable to wire.
- Full damage-type entry fields (exponent/scaling/message-id) only if the registry surfaces them; otherwise
  a minimal table — verify at plan time.
- Magic/resistance/enchant absorption curves remain v1 stubs (as in the player path) unless the jar port
  trivially includes them.
</deferred>
