# Phase 29: Damage Keystone (S2) - Pattern Map

**Mapped:** 2026-06-29
**Files analyzed:** 12 (3 new tooling, 1 new data package, 3 new server, 5 modified)
**Analogs found:** 12 / 12 (every new/modified file has a real in-repo analog read this session)

This phase is ~80% reuse. The keystone hurt pipeline is a sibling copy of the v3-sealed
`*tickPlayer` combat path (`server/combat.go`), with three jar-verified `actuallyHurt` tail diffs and
the i-frame decrement moved to a mob-baseTick step. Every analog below is a real `file:line` read.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `server/combat_mob.go` (NEW) | service (combat) | event-driven (discrete hit) | `server/combat.go` (`applyDamage`/`actuallyHurt`) | exact — same `hurtServer`/`actuallyHurt` bytecode |
| `server/damage_source.go` (NEW) | model (value type) | transform (tag lookup) | `level/loot/context.go` LootContext + `data/item/food.go` map shape | role-match (small typed-data + flat map read) |
| `server/death_mob.go` (NEW, split-able) | service (death/loot/XP) | event-driven (death) | `server/block_drop.go` (`spawnBlockDrop`/`NewItemEntity`) + `server/chest_loot.go` (`loot.Roll`) | exact (loot roll + per-stack Item spawn) |
| `data/tag/` (NEW package) | config (generated data) | (static embedded table) | `data/item/food.go` (generated `var Food = map[...]`) | exact (same generated-map package shape) |
| `tools/gen_tags.go` (NEW) | utility (codegen) | file-I/O (JSON read → Go emit) | `tools/gen_item_food.go` + `tools/gen_registryid.go` | exact (JSON-read → map-emit generator) |
| `tools/java/GenTags.java` (NEW) | utility (extractor) | file-I/O (jar zip read → JSON emit) | `tools/java/ExtractAll.java` (ZipFile read) + `tools/java/GenEntities.java` (manual JSON write) | exact (ZipFile jar-read + hand-written JSON) |
| `server/entity.go` (MODIFY) | model | (tick-owned fields) | `tickPlayer` health/invulnerableTime/lastHurt/hurtTime fields + existing `Entity` ITEM-pickup int fields | exact (sibling fields already on tickPlayer) |
| `server/attack_dispatch.go` (MODIFY) | controller (dispatch) | request-response (packet) | existing `handleAttack` player resolve + `applyAttackDamage` | exact (dual-resolve extension) |
| `server/region.go` (MODIFY) | model (region struct) | (cross-region queue field) | `pendingTransfers []transferIntent` (region.go:132) | exact (sibling queue field) |
| `server/region_transfer.go` (MODIFY) | service (cross-region) | event-driven (barrier-queued) | `transferIntent`/`detectTransfers`/`applyCrossRegionTransfers` | exact (mirror the discipline) |
| `server/region_coordinator.go` (MODIFY) | service (barrier) | batch (barrier drain) | `applyCrossRegionTransfers()` call site (coordinator.go:176) | exact (add `applyCrossRegionDamage()` next to it) |
| `server/plugin_entity.go` (MODIFY) | provider (Starlark handle) | transform (frozen scalar) | `entityHandle.Attr` read cases (`health`/`on_ground`) | exact (add `was_hurt`/`last_damage_type` read cases) |

## Pattern Assignments

### `server/combat_mob.go` (service, event-driven) — THE KEYSTONE

**Analog:** `server/combat.go` — `applyDamage` (l.188-238), `actuallyHurt` (l.262-321),
`getDamageAfterArmorAbsorb` (l.337-350), `getDamageAfterMagicAbsorb` (l.365-392). The mob path calls
the SAME free helpers verbatim — do NOT duplicate them.

**Reuse-verbatim free helpers (combat.go, no copy):** `mthClampF` (l.128), `combatRulesGetDamageAfterAbsorb`
(l.543), `combatRulesGetDamageAfterMagicAbsorb` (l.562), `maxF` (l.603), `isNaN32`/`isInf32` (l.613-614),
`maxFloat32` (l.599). Constants: `hurtCooldownConst` (l.141), `hurtInvulnerableTicks` (l.146),
`hurtDurationTicks` (l.150).

**i-frame gate pattern — copy `applyDamage` control flow (combat.go:188-238) exactly, with ONE add:**
The player `applyDamage` does NOT store `lastDamageSource`; the mob port MUST set `e.lastDamageSource = src`
in the `flag2` block (the fresh-hit branch, where the player sets `lastHurt`/`invulnerableTime`). The structure:
```go
// combat.go:213-230 (the i-frame gate to mirror, victim is now *Entity, add lastDamageSource set):
if float32(p.invulnerableTime) > hurtCooldownConst {   // src.is("bypasses_cooldown") gate added: && !src.is(...)
    if amount <= p.lastHurt {
        return  // no-op (i-frame absorbs)
    }
    t.actuallyHurt(p, amount-p.lastHurt)
    p.lastHurt = amount
} else {
    p.lastHurt = amount
    p.invulnerableTime = hurtInvulnerableTicks
    t.actuallyHurt(p, amount)
    p.hurtDuration = hurtDurationTicks
    p.hurtTime = p.hurtDuration
}
// MOB DIFF: set e.lastDamageSource = src here (the flag2 block; bytecode 449-451).
if p.health <= 0 { t.die(p) }   // MOB: t.dieEntity(e, src)
```

**actuallyHurt pattern — copy combat.go:262-321 with THREE jar-verified DIFFs:**
```go
// combat.go:271-280 — REUSE this armor/absorption fold verbatim (helpers shared):
amount = t.getDamageAfterArmorAbsorb(p, amount)   // MOB: must call src.is("bypasses_armor") — NOT const false (Pitfall 3)
amount = t.getDamageAfterMagicAbsorb(p, amount)   // v1 no-op
absorption := p.getAbsorptionAmount()
withAbsorb := amount
amount = maxF(amount-absorption, 0.0)
p.setAbsorptionAmount(absorption - (withAbsorb - amount))
if amount == 0.0 { return }
// DIFF 1: NO causeFoodExhaustion (combat.go:294 is Player-only — OMIT for mob)
// DIFF 2: NO client.Send(setHealth(...)) (combat.go:320 — a mob has no client; just mutate e.health, clamp at 0)
// DIFF 3: recordDamage + gameEvent(ENTITY_DAMAGE) stay cited no-ops (player tracker is also a stub)
```

**on_damage Emit pattern — combat.go:310-315 is the LOCKED post-mitigation site, but region-scoped:**
The player path uses raw `t.plugins.Emit`; the mob path MUST use `r.emitEntityEvent` (region_transfer.go:86)
NOT raw `t.plugins.Emit` (Pitfall 7):
```go
// combat.go:310-315 (the player site to mirror) becomes, for the mob (region-scoped):
r.emitEntityEvent(host.EventDamage, host.DamageEvent{EntityID: int(e.id), Amount: float64(amount)})
```

**Mob attribute reads — reuse `Entity.getAttributeValue` (entity.go:200) 1:1.** It already reads the
mob's real `*attribute.Map` with `attr.DefaultValue()` fallback. For the armor curve, read
`e.getAttributeValue(attribute.Armor)` / `attribute.ArmorToughness` and apply the d2f cast at the call site
exactly as the player `getArmorValue` (combat.go:570) does `math.Floor`.

---

### `server/damage_source.go` (model, transform)

**Analog:** `level/loot/context.go` (a small typed value struct with accessor methods) + the
`data/tag/` generated flat-map for the `is(tag)` lookup (see `data/item/food.go` map shape).

**Value-type pattern (RESEARCH §Code Examples, jar `DamageSource.is(TagKey)` = `type.is(tag)`):**
```go
type damageTypeID int32 // generated consts: damageTypePlayerAttack, damageTypeMobAttack, damageTypeFall, ...
type damageSource struct {
    typeTag  damageTypeID
    attacker int32 // entity id of causing entity; 0 = none (DamageSource.getEntity == causingEntity)
}
func (s damageSource) is(tag string) bool {
    return tag.DamageTypeTags[tag][s.typeTag] // generated: map[string]map[damageTypeID]bool
}
```
Plain-value struct (int enum + int32, NO pointers) — satisfies the entity.go snapshot-friendly contract
(entity.go:30-36), so it travels with the `*Entity` at the barrier with no special handling.

---

### `server/death_mob.go` (service, event-driven death)

**Analog:** `server/block_drop.go` — `spawnBlockDrop` (l.127-160), `blockDropsFor` (l.65-78),
`NewItemEntity` (l.168-188); `server/chest_loot.go` — `unpackLootTable` (l.89-107). These are the loot-roll
→ per-stack Item-spawn pattern, verbatim.

**Loot roll pattern (block_drop.go:65-78 + chest_loot.go:98-105):**
```go
// loot.LoadTable("minecraft:entities/pig") → loot.Roll(tbl, seed, loot.NewLootContext(seed, 0))
tbl, err := loot.LoadTable("minecraft:entities/" + name)
if err != nil { return }                       // no table → no drop (block_drop.go:70-77 nil-handling)
seed := rand.Int64()                           // event-time math/rand/v2 draw (block_drop.go:137) — outside oracle window
for _, stack := range loot.Roll(tbl, seed, loot.NewLootContext(seed, 0)) {
    if stack.Count <= 0 { continue }           // block_drop.go:147 explosion-decay guard
    ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, stack)
    t.regionForEntity(e).entities.add(ie)      // OWNER-region routing (region_transfer.go:56) — NOT cur() (Pitfall 2)
}
```
NOTE block_drop.go:158 uses `t.cur().entities.add` (it runs on a tick path already in the owning region);
the death path runs at the barrier or owner region, so use `t.regionForEntity(e).entities.add` /
`withRegion` (region_transfer.go:100) to route correctly.

**Item entity spawn — reuse `NewItemEntity` (block_drop.go:168-188) verbatim** (toss velocity + ITEM
metadata + pickupDelay already solved).

**Loot-context gap (RESEARCH Pitfall 4 — the scope risk):** `level/loot/context.go` LootContext (l.20-46)
carries `rng/luck/Biome/HasTool/Explosion` — NO `this`-entity / attacker / `killed_by_player`. The pig
entity table at `level/loot/data/loot_table/entities/pig.json` (CONFIRMED present) references entity-context
conditions the evaluator does not implement. The death/loot plan's FIRST task is a headless
`loot.Roll(loot.LoadTable("minecraft:entities/pig"), seed, ctx)` to confirm the parser accepts
`"type":"minecraft:entity"` and to scope the condition extension vs. documented cut (A4/Open Question 1).

**Death broadcast (A2):** confirm the tracker's store-remove despawn path broadcasts RemoveEntities; if not,
add the explicit `broadcastEntityEvent(this, 3)` death-status + RemoveEntities send. Death REMOVAL is the
hard requirement; loot/XP ride with it.

---

### `data/tag/` (config, generated)

**Analog:** `data/item/food.go` (l.1-22) — the `// Code generated by ...; DO NOT EDIT.` header + `package`
+ a typed `var Food = map[string]ItemFood{...}`.

**Generated package shape:**
```go
// Code generated by tools/gen_tags.go from 26.2 jar tags; DO NOT EDIT.
package tag

// DamageTypeTags maps a tag name (e.g. "bypasses_armor") to the SET of damage-type ids in it
// (recursively flattened — nested #-tag refs resolved, like TagLoader.build).
var DamageTypeTags = map[string]map[int32]bool{ ... }
// ItemTags (generated-but-not-yet-wired in P29; consumer lands P32) — same shape, keyed by item id.
var ItemTags = map[string]map[int32]bool{ ... }
```

---

### `tools/gen_tags.go` (utility, codegen)

**Analog:** `tools/gen_item_food.go` (l.64-149) — the full `genX(jsonDir, goMCRoot)` shape: `readJSON`
into a typed struct, build sorted rows (deterministic output), `generatedHeader(...)`, `package`,
write the map, `format.Source`, `writeFile`, `logf`. Also `tools/gen_registryid.go` (l.13-70) for the
multi-output / sorted-keys discipline.

**Generator pattern (gen_item_food.go:64-148):**
```go
func genTags(jsonDir, goMCRoot string) error {
    jsonPath := filepath.Join(jsonDir, "tags.json")             // the GenTags.java output
    out := filepath.Join(goMCRoot, "data", "tag", "tags.go")
    var report tagsReport
    if err := readJSON(jsonPath, &report); err != nil { return fmt.Errorf("genTags: %w", err) }
    // ... build sorted rows (sort.Slice for reproducible output — gen_item_food.go:97) ...
    var buf strings.Builder
    buf.WriteString(generatedHeader("gen_tags.go", "tags.json"))
    buf.WriteString("\npackage tag\n\n")
    // ... emit map[string]map[int32]bool ...
    formatted, err := format.Source([]byte(buf.String()))       // gen_item_food.go:140
    if err != nil { return fmt.Errorf("genTags: gofmt: %w", err) }
    return writeFile(out, formatted)
}
```

**Register in `tools/main.go` (l.34-49):** add `{"tags", genTags}` to the `generators` slice (mirror
`{"itemfood", genItemFood}` at l.44).

---

### `tools/java/GenTags.java` (utility, extractor)

**Analog:** `tools/java/ExtractAll.java` — the `ZipFile` jar-read pattern (l.103-128 `extractInnerJar`,
l.313-324 the `zip.entries()` iteration reading `META-INF/...`) + `tools/java/GenEntities.java`
(l.48-68 — the hand-written JSON output, `PrintWriter` + `jsonStr`, NO external JSON deps).

**ZipFile read pattern (ExtractAll.java:103-128):** open the inner jar with `new ZipFile(...)`, iterate
`zip.entries()`, match `e.getName().startsWith("data/minecraft/tags/damage_type/")` (and
`.../tags/item/`), `zip.getInputStream(e)` → read the JSON body.

**Recursive flatten (RESEARCH §6 — the one non-trivial concern):** `panic_causes.json` contains nested
`#minecraft:is_player_attack` refs; the extractor MUST recursively resolve tag-of-tags exactly as vanilla
`TagLoader.build` does (a `#`-prefixed value entry is another tag to expand, not a member id).

**JSON output (GenEntities.java:48-68):** hand-write the flattened membership JSON with `PrintWriter` +
the `jsonStr` helper (GenEntities.java:66-68) — no external JSON dependency. NOT registered in
ExtractAll.java's `runCustomExtractors` extractor list unless it needs Bootstrap (it reads JSON from the
zip directly, so it can be a standalone reader — but if added to the list, append `"GenTags"` to
ExtractAll.java:246 alongside the others).

---

### `server/entity.go` (MODIFY — new tick-owned fields)

**Analog:** the `tickPlayer` combat fields (`health`/`invulnerableTime`/`lastHurt`/`hurtTime`/`hurtDuration`
/`absorptionAmount`, referenced throughout combat.go) + the existing `Entity` ITEM-pickup int fields
(entity.go:83-111, which are the precedent for "plain-value fields set only for one entity class").

**Add to the `Entity` struct (entity.go:42-167), as PLAIN VALUE types (snapshot-friendly, entity.go:30-36):**
```go
health           float32      // LivingEntity health (setHealth subtracts; clamp 0)
lastHurt         float32      // hurtServer lastHurt (the i-frame excess gate)
invulnerableTime int32        // the 20-tick i-frame window (decremented in mob baseTick)
hurtTime         int32        // hurt-flash timer (decremented in mob baseTick, unconditional)
hurtDuration     int32        // hurtDuration set to 10 on a fresh hit
lastDamageSource damageSource // MOB-SUB-02 — the real ported source (value type, no pointer)
// (optional) absorptionAmount float32  // mirror tickPlayer.absorptionAmount if the fold needs it
```
These mirror the `tickPlayer` fields. `damageSource` is a small value struct (int enum + int32), so it
does NOT break the snapshot-friendly contract — same exemption `ai`/`attributes` carry (entity.go:119-135).

**i-frame DECREMENT (RESEARCH Pattern 3) — NEW per-tick mob step (NOT tickPlayerCombat):** the player puts
the decrement in `tickPlayerCombat` (combat.go:95-114, the ServerPlayer site). For a mob it lives in a
`baseTick`-equivalent step (`!(instanceof ServerPlayer)` guard is auto-true). Mirror combat.go:106-112
exactly — integer-only, RNG-free (oracle-safe):
```go
if e.hurtTime > 0 { e.hurtTime-- }
if e.invulnerableTime > 0 { e.invulnerableTime-- }
```

---

### `server/attack_dispatch.go` (MODIFY — dual-resolve)

**Analog:** the existing `handleAttack` (l.91-201) player resolve + `applyAttackDamage` (l.214-231).

**Dual-resolve pattern (RESEARCH §Code Examples; attack_dispatch.go:97-100 today returns nil for a mob id):**
```go
victim := t.lookupPlayerByEntityID(int32(targetID))
if victim != nil {
    // ... existing player path UNCHANGED (attack_dispatch.go:101-201) ...
    return
}
// NEW mob-victim path:
ownerRegion := t.owningRegion(int32(targetID))   // region_transfer.go:65 — O(regionCount), nil if gone
if ownerRegion == nil { return }                 // forged/despawned id → silent no-op (T-6-04)
mob, _ := ownerRegion.entities.get(int32(targetID))
// reach gate (attack_dispatch.go:37 attackReach=3.5) + total damage (same ATTACK_DAMAGE × scale × crit math)
src := damageSource{typeTag: damageTypePlayerAttack, attacker: p.entityID}
if ownerRegion == t.cur() {                       // SAME region → fast synchronous path
    t.applyDamageEntity(mob, src, total)
} else {                                          // cross-region → barrier-queue (see region_transfer.go)
    t.queueDamageIntent(ownerRegion.id, damageIntent{victimID: mob.id, src: src, amount: total})
}
```
Build an `applyMobAttackDamage(victim *Entity, amount) bool` mirror of `applyAttackDamage` (l.214-231) if
the knockback/sweep tail needs the hurtOrSimulate boolean for mob victims.

---

### `server/region.go` + `server/region_transfer.go` + `server/region_coordinator.go` (MODIFY — cross-region)

**Analog:** the WHOLE `transferIntent` discipline — `transferIntent` type (region_transfer.go:160-163),
`region.pendingTransfers` field (region.go:132), `detectTransfers` (region_transfer.go:171-180),
`applyCrossRegionTransfers` (region_transfer.go:193-213), and its call site (region_coordinator.go:176).

**Mirror EXACTLY (RESEARCH Pattern 4 / Assumption A3 — keep the queue on the SOURCE region):**
```go
// region_transfer.go (next to transferIntent l.160):
type damageIntent struct {
    victimID int32
    src      damageSource
    amount   float32
    to       regionID    // target (owner) region — mirrors transferIntent.to (l.162)
}
// region.go (next to pendingTransfers l.132):
pendingDamage []damageIntent
// region_coordinator.go (right after applyCrossRegionTransfers, l.176):
t.applyCrossRegionDamage()
```

**`applyCrossRegionDamage` drains at the barrier — mirror `applyCrossRegionTransfers` (region_transfer.go:193-213):**
quiescent on the coordinator, for each src region's `pendingDamage`: re-resolve `owner := t.owningRegion(di.victimID)`
(region_transfer.go:65), `if owner == nil { continue }` (drop if gone — the same defensive guard as
l.205), then `t.withRegion(owner, func(){ ... applyDamageEntity ... })` (region_transfer.go:100). Reset the
slice with `pendingDamage[:0]` (l.211). NO `t.trace` (it is an internal barrier step, not a fixed phase —
region_transfer.go:194-196 / coordinator.go note).

---

### `server/plugin_entity.go` (MODIFY — handle attrs)

**Analog:** `entityHandle.Attr` read cases (l.156-179) — `on_ground` returns `starlark.Bool`, `health`
returns a frozen `starlark.Float` from a re-resolved entity. Add to `AttrNames` (l.183-189) too.

**Frozen-scalar read pattern (plugin_entity.go:156-179):**
```go
// inside Attr, after the capEntitiesRead gate + e := h.store().get(h.id) (l.149-155):
case "was_hurt":
    return starlark.Bool(e.lastDamageSource.typeTag != 0 /* or a real "hurt this tick" flag */), nil
case "last_damage_type":
    return starlark.Int(int(e.lastDamageSource.typeTag)), nil  // frozen scalar, host-computed
```
The handle holds only an id + re-resolves on the tick goroutine (plugin_entity.go:20-22) — the goal NEVER
holds a live source (CONTEXT decision 3 / MOB-SUB-02). Returns frozen scalars across the Starlark boundary,
exactly like `health` (l.173-176).

## Shared Patterns

### Combat math helpers (REUSE — never duplicate)
**Source:** `server/combat.go` free functions (l.128-614)
**Apply to:** `combat_mob.go` (`actuallyHurtEntity` + the mob armor/absorb fold)
The Player and LivingEntity math are IDENTICAL — `combatRulesGetDamageAfterAbsorb`/`mthClampF`/`maxF`/
`isNaN32`/`isInf32` ARE the verified `CombatRules` port. The mob path calls the SAME functions. Anti-pattern:
`math.Max` / `float64` math / a fresh armor curve (Pitfall 1).

### Mob attribute reads
**Source:** `server/entity.go:200` (`Entity.getAttributeValue`)
**Apply to:** every mob ARMOR/ARMOR_TOUGHNESS/MAX_ABSORPTION/KNOCKBACK_RESISTANCE read in `combat_mob.go`
Already reads the real `*attribute.Map` with `DefaultValue()` fallback. Do NOT build a second attribute backend.

### Region-scoped plugin Emit
**Source:** `server/region_transfer.go:86` (`region.emitEntityEvent`)
**Apply to:** the `on_damage` Emit in `actuallyHurtEntity` (and any mob death Emit)
NEVER raw `t.plugins.Emit` from a region goroutine (Pitfall 7). The registry is frozen+shared; the helper
is nil-guarded.

### Cross-region barrier-queue discipline
**Source:** `server/region_transfer.go:160-213` (the whole `transferIntent` lifecycle) + coordinator.go:176
**Apply to:** `damageIntent` (cross-region hits), `applyCrossRegionDamage` drain
Queue on the SOURCE region mid-tick (tagged with target region id), drain at the quiescent coordinator
barrier, re-resolve owner by id (drop if gone). Never resolve a cross-region victim through `cur()`/`only()`
from the attacker's goroutine (Pitfall 2 — `cur()` silently falls back to region 0).

### Loot roll → Item entity spawn
**Source:** `server/block_drop.go:65-188` + `server/chest_loot.go:89-107`
**Apply to:** `death_mob.go` `dropMobLoot`
`loot.LoadTable(id)` → `loot.Roll(tbl, seed, loot.NewLootContext(seed, 0))` → per-stack `NewItemEntity` →
owner-region `entities.add`. Server-generated seed (`rand.Int64()`), never client-supplied (T-20-05).

### Generated-data package + codegen
**Source:** `data/item/food.go` (output shape) ← `tools/gen_item_food.go` (generator) ← `tools/java/ExtractAll.java`
(extractor), all registered via `tools/main.go:34-49`
**Apply to:** `data/tag/` ← `tools/gen_tags.go` ← `tools/java/GenTags.java`, register `{"tags", genTags}`
The `generatedHeader` + `format.Source` + `writeFile` + sorted-rows discipline is identical across generators.

## No Analog Found

None. Every new and modified file has a concrete in-repo analog. The two genuinely-new SUBSYSTEMS
(recursive tag flatten in GenTags.java; entity-loot-context conditions in death_mob.go) are EXTENSIONS of
existing analogs (ExtractAll.java's ZipFile read; the level/loot evaluator), not net-new patterns — but
both carry a plan-time verification:

| Concern | Analog extended | Verification (plan-time) |
|---------|-----------------|--------------------------|
| Recursive nested-tag flatten | `ExtractAll.java` ZipFile read (no flatten today) | Confirm `panic_causes.json` `#`-refs resolve like `TagLoader.build` |
| Entity-loot context conditions | `level/loot` evaluator (chest/block context only) | Headless `loot.Roll("minecraft:entities/pig")` FIRST (A4 / Open Q1) — extend context or document cut |

## Metadata

**Analog search scope:** `server/` (combat, entity, attack, region, plugin handles), `tools/` (generators
+ java extractors), `data/` (generated package shapes), `level/loot/` (evaluator API + context)
**Files read this session:** combat.go, entity.go, region_transfer.go, gen_item_food.go, ExtractAll.java,
attack_dispatch.go, gen_registryid.go, main.go, block_drop.go, chest_loot.go, region.go (grep),
region_coordinator.go, context.go, event.go, food.go, plugin_entity.go, GenEntities.java; confirmed
`level/loot/data/loot_table/entities/pig.json` exists
**Pattern extraction date:** 2026-06-29
