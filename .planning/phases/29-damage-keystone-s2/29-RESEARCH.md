# Phase 29: Damage Keystone (S2) - Research

**Researched:** 2026-06-29
**Domain:** 1:1 jar-port of the mob (`*Entity`) damage/hurt/death pipeline for Sulfur — a Minecraft Java 26.2 (proto 776), Folia-regionized (N=2), CGO=0 Go server. Parallel to the v3-sealed `*tickPlayer` combat path.
**Confidence:** HIGH (every jar method below was disassembled this session via `javap -c -p` on `temp/cache/26.2-inner.jar`; every integration point is a real `file:line` read this session)

## Summary

Phase 29 builds `applyDamageEntity` / `actuallyHurtEntity` — the `*Entity` siblings of the already-shipped, jar-faithful `*tickPlayer` `applyDamage`/`actuallyHurt` (`server/combat.go`). The single most valuable finding: **the mob path is the SAME `LivingEntity.hurtServer`/`actuallyHurt` bytecode the player path already ported — `combat.go` IS that port** — but for a non-player the `actuallyHurt` tail differs in three jar-verified ways, and the i-frame DECREMENT site moves from `ServerPlayer.tick` to `LivingEntity.baseTick`. So this is ~80% a copy of `combat.go` with the player-specific tail (food exhaustion, `SetHealth` wire, `PlayerCombatKill` death) swapped for the LivingEntity tail (`recordDamage`/`gameEvent`, broadcast-status-3 death, `dropAllDeathLoot`+`dropExperience`). [VERIFIED: javap LivingEntity.hurtServer/actuallyHurt/baseTick/die]

The keystone's REAL deliverable (per PITFALLS Pitfall 3) is a **genuine damage-type tag table** so `source.is(BYPASSES_ARMOR)` and (Phase 31) `source.is(panic_causes)` are real reads, not `const false`. The tag data already lives in the jar as plain JSON under `data/minecraft/tags/damage_type/` and `data/minecraft/tags/item/` — **no Java reflection extractor is needed; the existing `ZipFile`-reading Java pattern (`ExtractAll.java`) can read those JSON entries directly.** The one non-trivial extractor concern is **recursive nested-tag resolution** (`#minecraft:panic_environmental_causes`, `#minecraft:is_player_attack` appear inside `panic_causes.json`) — the extractor must flatten tag-of-tags exactly as vanilla `TagLoader.build` does. [VERIFIED: unzip of 26.2-inner.jar — 51 damage_type entries, 35 damage_type tags, 224 item tags, nested `#` refs present]

The Folia hazard is real and central: a player in region A hitting a mob in region B is the project's **first true cross-region write**. The fix is the exact `transferIntent` discipline — a new `damageIntent` queued onto the OWNER region mid-tick, drained at the quiescent barrier on the coordinator right next to `applyCrossRegionTransfers()` (`region_coordinator.go:176`). Same-region hits stay synchronous (the fast path). Death+loot+XP is the largest net-new piece: the v3 loot evaluator (`level/loot`) ALREADY embeds all 94 entity loot tables and generalizes the roll, BUT its `LootContext` carries no entity/attacker context and its conditions don't handle `entity_properties`/`killed_by_player` — which the real entity tables use. This is the one item the planner must scope deliberately (extend the loot context, or accept a documented cut).

**Primary recommendation:** Split into 4 plans — (1) tag codegen (both families, recursive flatten) → (2) `*Entity` hurt fields + `damage_source.go` + `combat_mob.go` (`applyDamageEntity`/`actuallyHurtEntity`) + baseTick i-frame decrement → (3) `attack_dispatch` dual-resolve + cross-region `damageIntent` barrier queue + `on_damage` Emit + `was_hurt`/`last_damage_type` handle attrs → (4) death removal + loot + XP. Keep the pig oracle GREEN: damage is event-driven, touches NO `serverAiStep`/goal RNG, and the only per-tick add (baseTick i-frame `--`) is integer-only.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Damage-type tag extraction (Grey Area 1) — BUILD THE FULL CODEGEN TAG TOOLING NOW (BOTH tag families).**
Add a `GenTags.java` extractor + `gen_tags.go` generator emitting BOTH the `minecraft:damage_type` tags (`PANIC_CAUSES`, `BYPASSES_ARMOR`, `IS_FIRE`, …) AND the item tags (`PIG_FOOD`, `COW_FOOD`, `WOLF_FOOD`, `Items.CARROT_ON_A_STICK`, …) into `data/tag/`. Front-loads shared tag tooling so Phase 32 reuses it with no new extractor. Mirror `tools/gen_item_food.go` / `tools/gen_registryid.go`; register in `tools/main.go`. `datapack.json` already exposes `minecraft:damage_type {elements,tags:true}` — extract the membership maps (datapack/registries report is authoritative, no reflective mid-tick extraction). **Phase 29 CONSUMES only the damage-type tags (armor-bypass + panic-causing branches); item tags are generated-but-not-yet-wired here (consumer lands Phase 32) — intentional shared-tooling front-loading, NOT a no-built-but-unwired violation (data is real + tested headless).**

**Mob death + loot drops + XP (Grey Area 2) — FULL `LivingEntity.die()` NOW (death removal + loot drops + XP).**
When mob health ≤ 0, port the real `LivingEntity.die()` / `Mob.dropFromLootTable` / `dropExperience` chain 1:1: remove the entity + death broadcast (EntityEvent death-status / RemoveEntities packets), roll the mob's loot table into Item entities (reuse the v3 STRUCT-POLISH loot evaluator — confirm it generalizes from chest/block-drop to entity loot tables), drop the XP orb. **Scope note (force at plan time):** the plan MUST javap-verify the mob loot-table id resolution (`Mob.getLootTable`) + `dropFromLootTable` and confirm the v3 loot evaluator generalizes; if the entity-loot path is a large net-new subsystem, SPLIT it into its own plan within Phase 29 (death-removal plan + loot/XP plan) so the keystone hurt pipeline is not blocked by loot tooling. **Death REMOVAL is the hard requirement; loot/XP ride with it.**

**lastDamageSource representation (Grey Area 3) — REAL `DamageSource{type, attacker?}` 1:1.**
Port the real `DamageSource` — a damage-type id + an optional attacker entity id (thin-id, never a live pointer) — stored as `Entity.lastDamageSource`. `source.is(tag)` = damage-type-id ∈ the jar-extracted tag set. Unblocks PanicGoal (Phase 31, tag check) AND wolf anger-on-hit (Phase 36, attacker read) with NO later refactor. Satisfies the deferred-goals.md "do NOT fake a hurt flag" rule. Exposed to goals via a `was_hurt` / `last_damage_type` handle attr returning frozen scalars across the Starlark boundary (host-side read; the goal never holds a live source).

### Claude's Discretion

- The exact split of Phase 29 into plans (likely: tag codegen → entity hurt-pipeline fields+`actuallyHurtEntity` → attack routing + cross-region `damageIntent` → death/loot/XP).
- Whether to add a small minimal damage-type entry table (exponent/scaling/message-id) if the existing registry extraction surfaces only IDs — verify at plan time; the `DamageSource` math is a javap port regardless.

### Deferred Ideas (OUT OF SCOPE)

- **Item-food tags** are GENERATED in Phase 29 but WIRED in Phase 32 (TemptGoal) — shared-tooling front-load, not a Phase-29 deliverable to wire.
- **Full damage-type entry fields** (exponent/scaling/message-id) only if the registry surfaces them; otherwise a minimal table — verify at plan time.
- **Magic/resistance/enchant absorption curves** remain v1 stubs (as in the player path) unless the jar port trivially includes them.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| MOB-SUB-01 | A mob can take and deal damage — parallel `*Entity` pipeline (`applyDamageEntity`/`actuallyHurtEntity`) reading the mob's real `*attribute.Map`, i-frame/invuln gating, `on_damage` Emit post-mitigation, player↔mob/mob↔mob/mob↔player flows wired | §1 (hurtServer/actuallyHurt bytecode, the 3 mob-vs-player diffs); §2 (`Entity.getAttributeValue` already reads the `*attribute.Map`); §4 (attack-flow routing); reuses `combat.go` free helpers verbatim |
| MOB-SUB-02 | A mob records its last damage source — per-`*Entity` `lastDamageSource` (owner-thread-safe under Folia), readable by goals via `was_hurt`/`last_damage_type` handle attr (real ported source, NOT a faked hurt flag) | §1 (hurtServer bytecode 449-451: `this.lastDamageSource = source` set inside the `flag2` block); §3 (`damageSource{typeTag,attacker}` value type); §5 (handle attr, frozen scalar) |
| MOB-SUB-03 | Damage-type tag membership is data-driven — jar-extracted tag table (`PANIC_CAUSES`, `BYPASSES_ARMOR`, `IS_FIRE`, …) so `source.is(tag)` is genuine, not `const false` | §6 (tag codegen design; jar JSON location; recursive flatten; `DamageSource.is(tag)`=`type.is(tag)` bytecode) |
</phase_requirements>

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Mob damage math (armor/absorption/i-frame) | Server tick (region-owned `*Entity`) | — | `LivingEntity.hurtServer`/`actuallyHurt` is server-authoritative; mob has no client to veto |
| `lastDamageSource` storage + read | Server tick (`*Entity` field) | Starlark plugin (frozen-scalar handle read) | Tick-owned single-owner; goals read a host-computed frozen bool/int, never a live source |
| Damage-type tag membership | Build-time codegen (`tools/`) → embedded Go data | Server tick (read-only lookup) | Static jar data; immutable embedded table read lock-free from any region goroutine |
| Cross-region hit application | Coordinator barrier (post-fan-out) | Region goroutine (queue the intent) | First true cross-region WRITE; must be applied quiescent, never mid-tick from the actor's goroutine |
| Death removal + broadcast | Server tick (owner region store remove) | Network (EntityEvent/RemoveEntities) | Store mutation is owner-region; the broadcast rides the existing tracker |
| Loot roll → Item entities | Server tick (death event) | Build-time (embedded loot tables) | Event-driven roll; reuses `level/loot` + `NewItemEntity` spawn pattern |
| XP orb | Server tick (death event) | Network (AddEntity for the orb) | `ExperienceOrb.award` spawn; entity type 49 already generated |

## Standard Stack

**NO new Go dependencies. NO cgo.** This phase is a pure 1:1 jar-port onto existing seams; CGO_ENABLED=0 is preserved. The "stack" is the existing in-repo machinery — reuse, do not add.

### Core (all already present — reuse verbatim)

| Asset | Location | Purpose | Why Standard |
|-------|----------|---------|--------------|
| `combat.go` free helpers | `server/combat.go` | `combatRulesGetDamageAfterAbsorb` (l.543), `combatRulesGetDamageAfterMagicAbsorb` (l.562), `mthClampF` (l.128), `maxF` (l.603), `isNaN32`/`isInf32` (l.613-614), `maxFloat32` (l.599) | These ARE the verified `LivingEntity`/`CombatRules` ports; the mob path calls the SAME functions (the math is identical between Player and LivingEntity) [VERIFIED: combat.go read this session] |
| i-frame constants | `server/combat.go` | `hurtCooldownConst=10.0` (l.141), `hurtInvulnerableTicks=20` (l.146), `hurtDurationTicks=10` (l.150) | Identical for mobs (the `> 10.0F` gate, the `=20` arm, the `=10` flash are in LivingEntity.hurtServer, shared) [VERIFIED: javap hurtServer bytecode 188/246/259] |
| `Entity.getAttributeValue` | `server/entity.go:200` | Reads the mob's real `*attribute.Map` with `DefaultValue()` fallback | Already the `LivingEntity.getAttributeValue` port for `*Entity`; reuse 1:1 for ARMOR/ARMOR_TOUGHNESS/MAX_ABSORPTION/KNOCKBACK_RESISTANCE reads [VERIFIED: entity.go read] |
| attribute singletons | `level/attribute/` | `MaxHealth`(20.0)/`Armor`/`ArmorToughness`/`MaxAbsorption`/`KnockbackResistance` | 1:1 vanilla defaults exist; the Map/Instance fold is generic [CITED: CONTEXT.md code_context; SUMMARY.md] |
| `transferIntent` pattern | `server/region_transfer.go:160-213` | The barrier-queue discipline to MIRROR for `damageIntent` | The proven cross-region hand-off; queue mid-tick on region goroutine, drain at coordinator barrier [VERIFIED: region_transfer.go read] |
| `emitEntityEvent` | `server/region_transfer.go:86` | Region-scoped plugin Emit for `on_damage` | The frozen-registry, region-resolved dispatch; nil-guarded [VERIFIED: region_transfer.go read] |
| `level/loot` evaluator | `level/loot/{embed,roll,parse,model,context,condition,function}.go` | `LoadTable(id)`, `Roll(table, seed, ctx)`, `NewLootContext(seed, luck)` | Embeds ALL 94 entity loot tables already; the same evaluator chests/block-drops use [VERIFIED: ls level/loot/data/loot_table/entities/ = 94 files; chest_loot.go/block_drop.go read] |
| `NewItemEntity` + store add | `server/block_drop.go:168`, `entities.add` | Spawn an Item entity per rolled stack | The death-loot drop reuses this exact pattern (per-stack spawn + tracker broadcast) [VERIFIED: block_drop.go read] |
| `ExperienceOrb` entity type | `data/entity` (id 49, `experience_orb`) | XP orb spawn target | Generated entity type exists; `ExperienceOrb.award(ServerLevel, Vec3, int)` is the spawn entry [VERIFIED: grep data/entity; javap ExperienceOrb.award] |
| `host.EventDamage` / `DamageEvent` | `plugin/host/event.go:23,155` | The `on_damage` Emit reused for mobs | Already keyed by entity id + post-mitigation amount [CITED: CONTEXT.md; ARCHITECTURE.md] |

### Supporting (NEW files this phase)

| File | Purpose | When to Use |
|------|---------|-------------|
| `tools/java/GenTags.java` (NEW) | Reads tag JSON from the jar zip, recursively flattens nested `#`-tag refs, emits a flat membership JSON | The extractor step; mirror `ExtractAll.java`'s `ZipFile` pattern |
| `tools/gen_tags.go` (NEW) | Reads the extractor JSON, emits `data/tag/*.go` (damage-type tag sets + item tag sets) | Mirror `gen_item_food.go`; register in `tools/main.go` generators list (l.29) |
| `data/tag/` (NEW package) | The generated tag membership tables (e.g. `DamageTypeTags["bypasses_armor"] = {…ids}`) | Read by `damage_source.go`'s `is(tag)` |
| `server/damage_source.go` (NEW) | `damageSource{typeTag, attacker int32}` value type + `is(tag)` + the damage-type id enum/consts | Built once; PanicGoal (P31) + wolf (P36) read it |
| `server/combat_mob.go` (NEW) | `applyDamageEntity` / `actuallyHurtEntity` / mob `getDamageAfterArmorAbsorb` + the `damageIntent` queue plumbing | The keystone hurt pipeline |
| `server/death_mob.go` (NEW, suggested) | `dieEntity` / `dropMobLoot` / `dropMobExperience` / death broadcast | The death+loot+XP plan (split-able) |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Parallel `*Entity` path | Interface-generalize `applyDamage` over a `LivingEntity` interface | REJECTED (ARCHITECTURE Anti-Pattern 1): two attribute backends, two death flows, risks the v3-sealed player oracle. Parallel sibling keeps player path byte-identical. |
| Reading tag JSON from the jar zip directly | A reflective Java extractor over `DamageTypeTags`/`ItemTags` static fields | Direct zip-read is simpler AND authoritative (the datapack JSON IS the source of truth vanilla `TagLoader` reads). Reflection would need Bootstrap binding. CONTEXT mandates "datapack/registries report is authoritative, no reflective mid-tick extraction." |
| `damageIntent` barrier queue (cross-region) | Same-region-only hit (drop cross-seam hits) | REJECTED: the checkerboard split + 3.5-block reach crosses chunk seams constantly; same-region-only would silently drop legitimate hits. Barrier-queue is the race-clean answer. |
| `damageSource{typeTag, attacker int32}` | A faked `wasHurt bool` flag | REJECTED (deferred-goals.md "do NOT fake a hurt flag"): the real source unblocks P31 tag-read + P36 attacker-read with no refactor. |

**Installation:** none — `CGO_ENABLED=0 go build ./...` stays green; `go list -deps ./... | grep -i gopy` stays empty.

**Codegen run (after adding the generator):** `cd tools && go run . --version 26.2` (the existing pipeline; the new `gen_tags` slots into the `generators` list).

## Architecture Patterns

### System Architecture Diagram (the keystone data flow)

```
PLAYER → MOB (the main new flow)
  ServerboundAttack ──► applyInput (subtick.go) ──► handleAttack (attack_dispatch.go:91)
     │  TODAY: lookupPlayerByEntityID → tickPlayer victim ONLY (returns nil for a mob id)
     │  NEW:   ALSO resolve the mob *Entity by id
     ▼
  resolve victim:
     ├─ player id?  → existing applyAttackDamage(tickPlayer) ──► applyDamage (combat.go)  [UNCHANGED]
     └─ mob id?     → resolve the mob's OWNING region (regionForEntity / owningRegion)
                       │
                       ├─ SAME region as the attack goroutine?  ── FAST PATH ──► applyDamageEntity(mob, src, amt)  [synchronous]
                       │
                       └─ DIFFERENT region?  ── queue damageIntent{victimID, src, amt} onto OWNER region
                                                  (region.pendingDamage, appended mid-tick on the actor's goroutine)
                                                       │
                                                       ▼  (BARRIER — coordinator, all regions quiescent)
                                                  applyCrossRegionDamage()  [next to applyCrossRegionTransfers, region_coordinator.go:176]
                                                       │  re-resolve owner by id (drop if gone), then:
                                                       ▼
  applyDamageEntity(mob, src, amt)  ── port of LivingEntity.hurtServer ──
     ├─ isDeadOrDying guard
     ├─ amount<0 → 0 ; NaN/Inf → Float.MAX_VALUE
     ├─ i-frame gate:  (float)invulnerableTime > 10.0F && !src.is(BYPASSES_COOLDOWN)
     │     ├─ amount<=lastHurt → return false (no-op)
     │     └─ actuallyHurtEntity(amount - lastHurt); lastHurt=amount
     │   else: lastHurt=amount; invulnerableTime=20; actuallyHurtEntity(amount); hurtDuration=hurtTime=10
     ├─ set this.lastDamageSource = src   (MOB-SUB-02)   [the flag2 block, bytecode 449-451]
     └─ if isDeadOrDying → dieEntity(src)
                              │
  actuallyHurtEntity(mob, src, amt)  ── port of LivingEntity.actuallyHurt (NOT Player) ──
     ├─ getDamageAfterArmorAbsorb (src.is(BYPASSES_ARMOR) ? skip : combatRulesGetDamageAfterAbsorb)  [REUSE helper]
     ├─ getDamageAfterMagicAbsorb (v1 stub no-op)                                                    [REUSE helper]
     ├─ absorption fold (Math.max, setAbsorptionAmount)                                              [REUSE maxF]
     ├─ if amount==0 → return
     ├─ NO causeFoodExhaustion  ◄── DIFF vs player path (LivingEntity has none)
     ├─ recordDamage (combat tracker — v1 stub) ; setHealth(getHealth()-amount)
     ├─ on_damage Emit  ◄── region-scoped via emitEntityEvent (post-mitigation amount)  (MOB-SUB-01)
     └─ gameEvent(ENTITY_DAMAGE) (v1 stub)
                              │
  dieEntity(mob, src)  ── port of LivingEntity.die ──
     ├─ guard (isRemoved || dead) ; dead=true
     ├─ dropAllDeathLoot: dropFromLootTable (getLootTable → level/loot.Roll → NewItemEntity per stack)
     │                     + dropExperience (lastHurtByPlayer gate → ExperienceOrb.award)
     ├─ broadcastEntityEvent(this, 3)  ◄── the death-status packet
     ├─ setPose(DYING)
     └─ remove from owner region store + RemoveEntities broadcast (the tracker path)

MOB → PLAYER (hostiles, future P35): MeleeAttackGoal → host attack op → mob→player damageIntent applied at barrier
MOB → MOB (wolf, future P36): same shape; applyDamageEntity on the victim's owning region
```

### Recommended file structure

```
server/
├── combat.go            # REUSE the free helpers (do NOT duplicate); player path UNCHANGED
├── combat_mob.go        # NEW: applyDamageEntity, actuallyHurtEntity, mob getDamageAfterArmorAbsorb,
│                        #      damageIntent queue + applyCrossRegionDamage
├── damage_source.go     # NEW: damageSource value type, damageTypeTag/id enum, is(tag)
├── death_mob.go         # NEW (split-able): dieEntity, dropMobLoot, dropMobExperience, death broadcast
├── entity.go            # MODIFY: add health/lastHurt/invulnerableTime/hurtTime/hurtDuration/lastDamageSource
├── attack_dispatch.go   # MODIFY: handleAttack dual-resolve (player OR mob)
├── region.go            # MODIFY: region.pendingDamage []damageIntent
├── region_coordinator.go# MODIFY: call applyCrossRegionDamage() at the barrier
├── ai_mob.go / tick     # MODIFY: baseTick i-frame decrement for *Entity (RNG-free)
└── plugin_entity.go     # MODIFY: was_hurt / last_damage_type handle attrs (frozen scalars)
tools/
├── main.go              # MODIFY: register {"tags", genTags}
├── gen_tags.go          # NEW: mirror gen_item_food.go
└── java/GenTags.java    # NEW: mirror ExtractAll.java ZipFile reader + recursive tag flatten
data/tag/                # NEW package: generated tag membership tables
```

### Pattern 1: `LivingEntity.hurtServer` — the i-frame gate (the player path IS this port)

```go
// Source: javap -c -p net.minecraft.world.entity.LivingEntity hurtServer (26.2-inner.jar, this session).
// The v1-relevant slice — IDENTICAL to combat.go applyDamage EXCEPT:
//   (a) the tag checks are now REAL (src.is(BYPASSES_COOLDOWN) instead of an absent constant);
//   (b) lastDamageSource is SET (bytecode 449-451, inside the flag2 block);
//   (c) the death tail calls the LivingEntity die(source), not the player PlayerCombatKill.
// Bytecode (verbatim trace):
//   0:  if (isInvulnerableTo(level, source)) return false;       // v1: const false seam
//   11: if (isDeadOrDying()) return false;
//   20: if (source.is(IS_FIRE) && hasEffect(FIRE_RESISTANCE)) return false;  // v1: hasEffect false → skip
//   53: noActionTime = 0;
//   58: if (amount < 0.0F) amount = 0.0F;
//   66: float f4 = amount;                                       // (the f1 snapshot)
//   69: ItemStack useItem = getUseItem();                        // v1 stub
//   75: amount -= applyItemBlocking(...); flag = (blocked > 0);  // v1: 0, flag=false
//   103: if (source.is(IS_FREEZING) && is(FREEZE_HURTS_EXTRA_TYPES)) amount *= 5.0F;  // v1: skip
//   129: if (source.is(DAMAGES_HELMET) && !headSlot.isEmpty()) { hurtHelmet; amount *= 0.75F; } // v1: empty → skip
//   164: if (Float.isNaN(amount) || Float.isInfinite(amount)) amount = 3.4028235E38F;
//   181: boolean flag2 = true;
//   184: if ((float)invulnerableTime > 10.0F && !source.is(BYPASSES_COOLDOWN)) {
//           if (amount <= lastHurt) return false;
//           actuallyHurt(level, source, amount - lastHurt);
//           lastHurt = amount; flag2 = false;
//        } else {
//           lastHurt = amount; invulnerableTime = 20;
//           actuallyHurt(level, source, amount);
//           hurtDuration = 10; hurtTime = hurtDuration;
//        }
//   272: resolveMobResponsibleForDamage / resolvePlayerResponsibleForDamage;  // v1 stubs
//   ... broadcastDamageEvent / markHurt / dealDefaultKnockback (NO_KNOCKBACK gated) — v1 stubs ...
//   444: boolean flag3 = flag && amount > 0.0F;                   // v1: flag false → flag3 false
//   449: if (flag2) { this.lastDamageSource = source; lastDamageStamp = gameTime; ... }  // ◄ MOB-SUB-02
//   510: ... ENTITY_HURT_PLAYER trigger (player-victim only; mob victim skips) ...
//   ... if (isDeadOrDying()) { if (!checkTotemDeathProtection) { sounds; die(source); } } else { hurtSound }
```

**Difference vs the player path (`combat.go` applyDamage):** the player port set `lastHurt`/`invulnerableTime`/health but did NOT store `lastDamageSource` (the player path has no PanicGoal reader). The mob port MUST set `e.lastDamageSource = src` in the `flag2` block. Everything else is structurally identical — reuse the same control flow.

### Pattern 2: `LivingEntity.actuallyHurt` — the THREE mob-vs-player differences

```go
// Source: javap -c -p net.minecraft.world.entity.LivingEntity actuallyHurt (this session).
//   0:  if (isInvulnerableTo(level, source)) return;
//   10: amount = getDamageAfterArmorAbsorb(source, amount);   // REUSE combatRulesGetDamageAfterAbsorb
//   17: amount = getDamageAfterMagicAbsorb(source, amount);   // v1 no-op
//   24: float f1 = amount;
//   27: amount = Math.max(amount - getAbsorptionAmount(), 0.0F);   // REUSE maxF
//   38: setAbsorptionAmount(getAbsorptionAmount() - (f1 - amount));
//   51: float f2 = f1 - amount;
//   57: if (f2 > 0.0F && f2 < 3.4028235E37F) { ... DAMAGE_DEALT_ABSORBED stat (player-attacker only) } // v1 stub
//   111: if (amount == 0.0F) return;
//   118: getCombatTracker().recordDamage(source, amount);     // ◄ DIFF: player path has causeFoodExhaustion here; LivingEntity does NOT
//   127: setHealth(getHealth() - amount);
//   137: setAbsorptionAmount(getAbsorptionAmount() - amount);
//   147: gameEvent(GameEvent.ENTITY_DAMAGE);                  // ◄ DIFF: LivingEntity ends with gameEvent, not a SetHealth client send
//   154: return;
```

**The three diffs the planner must encode:**
1. **NO `causeFoodExhaustion`** — that is `Player.actuallyHurt`-only (`combat.go:294`). The mob path omits it entirely.
2. **NO `SetHealth` client send** — a mob has no client; `setHealth(getHealth()-amount)` just mutates `e.health` (clamp at 0). The wire reflection of mob health is entity metadata, a LATER concern (CONTEXT/ARCHITECTURE: "for v5 the mob's health field is internal AI state").
3. **`recordDamage` + `gameEvent(ENTITY_DAMAGE)`** are v1 stubs (the player path's combat tracker is also a stub). Keep them as cited no-ops.

**The `on_damage` Emit** fires at the post-mitigation site (the same locked site `combat.go:310` uses), but **region-scoped via `r.emitEntityEvent(host.EventDamage, host.DamageEvent{EntityID:int(e.id), Amount:float64(amount)})`** — NOT a raw `t.plugins.Emit` (PITFALLS Pitfall 7).

### Pattern 3: i-frame DECREMENT — moves to `baseTick` for a mob (RNG-free, oracle-safe)

```go
// Source: javap -c -p net.minecraft.world.entity.LivingEntity baseTick (bytecode 451-488, this session).
//   if (hurtTime > 0) hurtTime--;                                       // ALL LivingEntity
//   if (invulnerableTime > 0 && !(this instanceof ServerPlayer)) invulnerableTime--;  // ◄ the guard
// The PLAYER path puts invulnerableTime-- in ServerPlayer.tick (combat.go:106, tickPlayerCombat).
// For a MOB (never a ServerPlayer), the !(instanceof ServerPlayer) guard is TRUE, so the decrement
// happens IN baseTick. hurtTime-- is unconditional for both.
// → A new per-tick mob step (tickMobIFrames, or folded into the entity tick) does:
//      if e.hurtTime > 0 { e.hurtTime-- }
//      if e.invulnerableTime > 0 { e.invulnerableTime-- }
// PURE INTEGER DECREMENT — NO RNG. Oracle-safe (see §Oracle Safety below).
```

### Pattern 4: cross-region `damageIntent` — mirror `transferIntent` exactly

```go
// Source: server/region_transfer.go:160-213 (transferIntent), server/region_coordinator.go:50-88,106-181.
// 1. NEW type (combat_mob.go or region_transfer.go):
type damageIntent struct {
    victimID int32
    src      damageSource
    amount   float32
}
// 2. NEW field on region (region.go, next to pendingTransfers []transferIntent):
//      pendingDamage []damageIntent
// 3. Mid-tick (the actor's region goroutine, in handleAttack's mob-victim branch when the victim's
//    owning region != the current region): append to the OWNER region's pendingDamage. This is a
//    pure append to a slice the coordinator drains — but note: appending to ANOTHER region's slice
//    from this goroutine races that region's own tick if it also appends. SAFER MIRROR: queue onto
//    THIS goroutine's own per-region outbox keyed by target region (like pendingTransfers lives on
//    the SOURCE region and is drained centrally). Recommend: pendingDamage lives on the SOURCE
//    region (the actor's), tagged with the target region id, drained centrally — IDENTICAL shape to
//    pendingTransfers which lives on src and names `to`.
// 4. Drain at the barrier (region_coordinator.go, right after applyCrossRegionTransfers, l.176):
//      t.applyCrossRegionDamage()  // for each src region's pendingDamage:
//                                  //   owner := t.owningRegion(di.victimID); if owner==nil continue (drop if gone)
//                                  //   t.withRegion(owner, func(){ applyDamageEntity(victim, di.src, di.amount) })
//    Runs quiescent on the coordinator — race-clean by construction (like applyAsyncResults).
```

**Fast path (same region):** if the attack goroutine's region already owns the mob, call `applyDamageEntity` synchronously — no intent. Resolve via `regionForEntity(mob)` vs the current region (`cur()` / the registered region).

### Anti-Patterns to Avoid

- **Interface-generalizing `combat.go`** (ARCHITECTURE Anti-Pattern 1): two attribute backends, two death flows; risks the sealed player path. Build a sibling.
- **Cross-region mutation mid-tick** (PITFALLS Pitfall 2): `cur()` falls back to region 0 silently. Never resolve the mob victim through `cur()`/`only()` from the attacker's goroutine. Use `regionForEntity`/`owningRegion` + barrier.
- **`const bypassesArmor = false` in the mob path** (PITFALLS Pitfall 3): the keystone's job is a REAL tag read. `getDamageAfterArmorAbsorb` MUST call `src.is(BYPASSES_ARMOR)` against the generated table.
- **`float64` combat math / `math.Max`** (PITFALLS Pitfall 3): every op `float32`, reuse `maxF`/`mthClampF`, javap before writing.
- **A `wasHurt bool` flag** (deferred-goals.md): store the real `damageSource`.
- **Adding RNG to `serverAiStep`/`tickAI`** (PITFALLS Pitfall 1): damage is event-driven; keep it out of the AI flow. The baseTick i-frame `--` is integer-only.
- **Raw `t.plugins.Emit` from a region goroutine** (PITFALLS Pitfall 7): use `emitEntityEvent`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Armor/absorption/clamp math | A fresh mob armor curve | `combatRulesGetDamageAfterAbsorb`/`mthClampF`/`maxF` (combat.go) | They ARE the verified `CombatRules` port; the Player and LivingEntity math are identical |
| Mob attribute reads | A second attribute backend | `Entity.getAttributeValue(*attribute.Attribute)` (entity.go:200) | Already reads the real `*attribute.Map` with default fallback |
| Cross-region hit routing | An ad-hoc lock or `cur()` resolve | The `transferIntent`/barrier-queue pattern | Proven race-clean; `cur()` silently mis-resolves region 0 |
| Loot table roll | A new entity-loot roller | `level/loot.LoadTable("minecraft:entities/<name>")` + `Roll` | All 94 entity tables already embedded; same evaluator as chests/blocks |
| Item drop spawn | A new item-entity spawner | `NewItemEntity` + `entities.add` (block_drop.go) | Per-stack spawn + tracker broadcast already solved |
| Tag JSON parsing | A reflective Java tag extractor | Read the tag JSON from the jar zip (`ZipFile`, like ExtractAll.java) | The datapack JSON IS authoritative; no Bootstrap binding needed |
| `DamageSource.is(tag)` | A bespoke membership check | A flat `map[id]bool` per tag from the generated table | `is(tag)` = `type.is(tag)` = id ∈ tag set (bytecode confirmed) |

**Key insight:** This phase is ~80% reuse. The genuinely new code is small: the `damageSource` value type, the `is(tag)` lookup over generated data, the cross-region `damageIntent` plumbing (a faithful copy of `transferIntent`), and the mob `actuallyHurt` tail (3 line-level diffs from the player port). The two real *subsystems* are the tag codegen and the entity-loot/XP path.

## Runtime State Inventory

This is NOT a rename/refactor phase — it ADDS state, it does not relocate existing strings. The "inventory" relevant here is the NEW tick-owned `*Entity` fields and where they live in the Folia model.

| Category | Items | Action Required |
|----------|-------|-----------------|
| New `*Entity` fields | `health float32`, `lastHurt float32`, `invulnerableTime int32`, `hurtTime int32`, `hurtDuration int32`, `lastDamageSource damageSource`, (optional) `absorptionAmount float32` | Add to `entity.go` `Entity` struct. PLAIN VALUE types (snapshot-friendly contract holds — `damageSource` is a small struct of an int enum + int32, no pointers). Travel with the `*Entity` at the barrier (`applyCrossRegionTransfers` adopts the pointer). |
| New region field | `region.pendingDamage []damageIntent` | Add to `region.go` next to `pendingTransfers` (l.132). Drained at the barrier. |
| Stored data (datastores) | None — damage is transient tick state | None — verified: no DB/persistence of damage state in v5 scope |
| Build artifacts | `data/tag/*.go` (generated), the extractor JSON intermediate | Regenerate via `cd tools && go run . --version 26.2` after adding `gen_tags` |
| Secrets/env | None | None |

**Folia race-safety:** every new field is single-owner (the mob's region goroutine, or the coordinator at the barrier for a cross-region hit). `lastDamageSource` is a value field, NOT part of the async-tracker snapshot set (same exemption `ai`/`attributes` carry, `entity.go:119-135`). Gate on Docker `-race` + `strictRegion`.

## Common Pitfalls

### Pitfall 1: Combat-math paraphrase in `actuallyHurtEntity` (PITFALLS Pitfall 3)
**What goes wrong:** writing `mob.health -= max(0, amount-armor)`, dropping the i-frame excess gate, `float64` math, or stubbing `BYPASSES_ARMOR` as `const false`.
**Why:** combat math "looks like arithmetic"; the tag table is the new work and is tempting to skip.
**How to avoid:** javap `hurtServer`/`actuallyHurt` (done — see §1/§2), reuse the `combat.go` helpers, keep every op `float32`, ship the REAL tag table so `src.is(BYPASSES_ARMOR)` is genuine.
**Warning signs:** `math.Max` instead of `maxF`; `const bypassesArmor=false`; missing the `amount<=lastHurt` no-op or the `>10.0F` gate.

### Pitfall 2: Cross-region race / wrong-region silent drop (PITFALLS Pitfall 2)
**What goes wrong:** resolving the mob victim through `cur()` from the attacker's goroutine → reads/writes region B's store concurrently (`-race` fail) OR silently resolves region 0 (miss).
**Why:** `cur()` falls back to `globalRegion` when the goroutine has no registered region.
**How to avoid:** `regionForEntity`/`owningRegion` to find the owner; same-region = synchronous, cross-region = barrier-queued `damageIntent`. Apply via `withRegion(owner, …)` at the barrier.
**Warning signs:** a damage path reachable from the fan-out calls `cur()`/`only()`; `-race` reports an `entityStore.byID` or `*Entity`-field cross-goroutine access; a boundary mob takes no damage. Arm `strictRegion=true` in cross-region tests.

### Pitfall 3: Forgetting the i-frame decrement moves to baseTick for mobs
**What goes wrong:** reusing `tickPlayerCombat` (which uses the ServerPlayer site) for mobs, or never decrementing → a mob is permanently in i-frames after one hit (takes no further damage) OR a double-decrement.
**Why:** the player port deliberately mirrors `ServerPlayer.tick`; the mob site is `LivingEntity.baseTick` with the `!(instanceof ServerPlayer)` guard.
**How to avoid:** a separate per-tick mob step decrementing `hurtTime` (unconditional) and `invulnerableTime` (the guard is auto-satisfied for a mob). RNG-FREE — verify placement does not sit before/inside any AI RNG draw (§Oracle Safety).
**Warning signs:** a mob immune after one hit; the pig oracle red at a draw-driven field (would indicate the decrement was mis-placed into the AI RNG stream — it must be integer-only and outside `serverAiStep`'s goal callbacks).

### Pitfall 4: The entity-loot context gap (the death/loot scope risk)
**What goes wrong:** assuming the v3 loot evaluator rolls entity tables correctly, then producing wrong/empty drops because the entity tables use `entity_properties`/`killed_by_player`/`direct_attacker` conditions the evaluator does not implement.
**Why:** the v3 `LootContext` (`level/loot/context.go`) carries `rng/luck/Biome/HasTool/Explosion` — NO `this`-entity, NO attacker, NO `killed_by_player`. `condition.go` has no `entity_properties`/`killed_by_player` handler. The pig entity table references `direct_attacker` + `is_on_fire` predicates. [VERIFIED: context.go read; grep condition.go — no entity-context handler]
**How to avoid:** the planner MUST decide at plan time — EITHER extend the loot context with a `this`/`attacker`/`killedByPlayer` field + the `entity_properties`/`killed_by_player` conditions (a real but bounded extension), OR accept a documented cut (roll the unconditional pools only; cite the deferred conditions). Per CONTEXT, **if entity-loot is a large net-new subsystem, split it into its own plan within Phase 29** so the keystone hurt pipeline ships first.
**Warning signs:** a mob drops nothing when vanilla drops 1-3 (the `set_count` pool gated behind an unhandled condition); a "burned" mob drops raw food.

### Pitfall 5: RNG draw-order desync of the pig oracle (PITFALLS Pitfall 1) — LOW risk here, but must stay green
**What goes wrong:** any new RNG draw in the shared AI flow breaks `TestPluginPigEqualsGoNativePig`.
**Why:** the oracle pins draw count + order off one shared per-mob stream.
**How to avoid:** damage is EVENT-DRIVEN (a discrete hit), not per-tick AI. Confirm NO part of this phase draws RNG inside `serverAiStep`/goal callbacks. The loot roll + item toss + XP use `math/rand/v2` (block_drop.go pattern) at the DEATH event — outside the 500-tick observation window (the oracle pig is never killed). The baseTick i-frame `--` is integer-only.
**Warning signs:** the oracle red at `wantX`/`yaw`; a `rand_*` call in a diff at `serverAiStep`.

## Code Examples

### Resolving the mob victim in `handleAttack` (the dual-resolve)

```go
// Source: attack_dispatch.go:91-100 (the existing player resolve), region_transfer.go:53-72.
// handleAttack today: victim := t.lookupPlayerByEntityID(int32(targetID)); if nil → return.
// NEW: when the player lookup is nil, try the mob store.
victim := t.lookupPlayerByEntityID(int32(targetID))
if victim != nil {
    // ... existing player path (UNCHANGED) ...
    return
}
// Mob victim path: resolve the *Entity by id across regions (owner re-resolve, drop if gone).
ownerRegion := t.owningRegion(int32(targetID))           // O(regionCount) scan
if ownerRegion == nil {
    return // unknown/despawned target — silent no-op (defensive)
}
mob, _ := ownerRegion.entities.get(int32(targetID))
// reach gate (port the vanilla entity-interaction reach; attackReach=3.5 exists, attack_dispatch.go:37)
// ... compute total damage exactly as the player branch (ATTACK_DAMAGE attr × strength scale × crit) ...
src := damageSource{typeTag: damageTypePlayerAttack, attacker: p.entityID}
if ownerRegion == t.cur() {           // SAME region → fast synchronous path
    t.applyDamageEntity(mob, src, total)
} else {                              // cross-region → barrier-queue onto this goroutine's region outbox
    t.queueDamageIntent(ownerRegion.id, damageIntent{victimID: mob.id, src: src, amount: total})
}
```

### `damageSource.is(tag)` over the generated table

```go
// Source: javap DamageSource.is(TagKey) = type.is(tag) (bytecode: getfield type; Holder.is(tag)).
// damage_source.go:
type damageTypeID int32 // generated consts: damageTypePlayerAttack, damageTypeMobAttack, damageTypeFall, ...
type damageSource struct {
    typeTag  damageTypeID
    attacker int32 // entity id of the causing entity; 0 = none (DamageSource.getEntity → causingEntity)
}
// is reports membership of the source's damage-type id in the named tag set (the generated flat map).
func (s damageSource) is(tag string) bool {
    return tagdata.DamageTypeTags[tag][s.typeTag] // generated: map[string]map[damageTypeID]bool
}
// usage in getDamageAfterArmorAbsorb (combat_mob.go):
//   if !src.is("bypasses_armor") { amount = combatRulesGetDamageAfterAbsorb(amount, armor, toughness) }
```

### Death loot + XP (the LivingEntity.die tail)

```go
// Source: javap die / dropAllDeathLoot / dropFromLootTable / dropExperience / getExperienceReward.
// dropAllDeathLoot: flag = (lastHurtByPlayerMemoryTime > 0);  // "recently hurt by a player"
//   if shouldDropLoot: dropFromLootTable(level, src, flag); dropCustomDeathLoot(...);  // v1 stub the latter
//   dropEquipment(level);                                                              // v1 stub (no mob equipment)
//   dropExperience(level, src.getEntity());
// dropFromLootTable: getLootTable() → Optional<ResourceKey>; if empty return;
//   roll the table → for each stack: spawn an Item entity (popResource shape).
// Sulfur:
func (t *TickLoop) dropMobLoot(e *Entity, src damageSource) {
    name := entityLootTableName(e.typ)            // "minecraft:entities/pig" from the entity registry name
    tbl, err := loot.LoadTable(name)
    if err != nil { return }                      // no table → no drop (e.g. item/orb entities)
    seed := rand.Int64()                          // event-time draw, outside the oracle window
    for _, stack := range loot.Roll(tbl, seed, loot.NewLootContext(seed, 0)) {  // ◄ see Pitfall 4: context gap
        if stack.Count <= 0 { continue }
        ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y+float64(e.height)/2, e.z, stack)
        t.regionForEntity(e).entities.add(ie)     // ◄ owner-region routing, NOT cur()
    }
}
// dropExperience gate: getExperienceReward (Mob.getBaseExperienceReward = xpReward field) → ExperienceOrb.award.
// Pig xpReward is set in registerGoals/ctor; verify the per-type value at plan time (javap Pig.<init>).
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `LivingEntity.hurt(DamageSource, float)` (single entry) | `hurtServer(ServerLevel, DamageSource, float)` + client-side `hurtClient` | 1.21.x server/client split | The mob entry point is `hurtServer`; the player port (`combat.go`) already targets it. Confirmed in 26.2 bytecode. |
| ServerboundInteract carried an ATTACK Action enum | ATTACK split into its own `ServerboundAttackPacket` (just an entity id) | 26.2 (proto 776) | `handleAttack` already decodes the new packet (attack_dispatch.go:91). No new wire work for mob attacks. |
| Damage-type tags as hardcoded enums | Datapack `data/minecraft/tags/damage_type/*.json` (registry-driven, datapack-overridable) | 1.19 datapack damage types | The extractor reads JSON, not Java enums; nested `#`-refs must be flattened. |

**Deprecated/outdated:**
- Do NOT look for a `LivingEntity.hurt(...)` mob entry — it's `hurtServer` in 26.2. [VERIFIED javap]
- Do NOT expect damage-type tags as static Java fields with literal members — they're datapack JSON with nested refs.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The mob's `xpReward` (base XP) per type is set in the mob ctor/registerGoals and should be javap-verified per-type at plan time; this research did not enumerate every mob's value | §Code Examples (dropExperience) | LOW — wrong XP amount per mob; bounded to a per-type constant lookup. Plan must javap `Pig.<init>` etc. |
| A2 | The death broadcast is `broadcastEntityEvent(this, 3)` (status 3 = death animation) + a RemoveEntities once removed from the store; the exact Sulfur tracker remove-broadcast path was not traced this session | §1 diagram (dieEntity) | LOW-MEDIUM — if the tracker doesn't auto-broadcast RemoveEntities on store-remove, the death plan needs an explicit removal broadcast. Plan must confirm the tracker's despawn path (grep RemoveEntities/removeEntity in tracker.go). |
| A3 | `damageIntent` should live on the SOURCE region (mirroring `pendingTransfers`), tagged with the target region, drained centrally — rather than appending to the target region's slice cross-goroutine | §Pattern 4 | MEDIUM — if implemented as a cross-goroutine append to the target's slice, it races. The recommended source-region-owned shape avoids it; plan must pick the race-clean shape and prove it with `-race`. |
| A4 | The v3 loot evaluator's `entity`-type loot-table parsing works for the `minecraft:entity` table type (not just `minecraft:block`/`chest`); only the CONDITIONS gap is confirmed, not the top-level table-type handling | §Pitfall 4 | MEDIUM — if `parse.go` rejects `"type":"minecraft:entity"`, the loot plan needs a parser extension too. Plan must load + roll one entity table headless (e.g. `minecraft:entities/pig`) as a first step. |
| A5 | Players are resolved only via `t.players` (`lookupPlayerByEntityID`), not via a region entity store, so the mob→player direction (P35) will need a barrier-queued mob→player damageIntent applied on the coordinator | §4 (mob→player flow) | LOW (out of P29 scope — P35), but noted: the player's `*Entity` (`playerEntity`, tick.go:623) carries velocity; player health is on `tickPlayer`. |

## Open Questions

1. **Does `level/loot/parse.go` accept `"type":"minecraft:entity"` tables?**
   - What we know: all 94 entity tables are embedded; `Roll`/`LoadTable` are type-agnostic at the API surface; the chest/block paths use `minecraft:block`/`minecraft:chest` types.
   - What's unclear: whether the parser branches on table type and rejects `entity`.
   - Recommendation: the loot plan's FIRST task is a headless `loot.Roll(loot.LoadTable("minecraft:entities/pig"), seed, ctx)` test — if it parses and rolls, A4 holds; if not, extend `parse.go`.

2. **Should the minimal damage-type entry table (exhaustion/scaling/message_id) be generated now?**
   - What we know: `data/minecraft/damage_type/*.json` carry `{exhaustion, message_id, scaling}` (51 entries). Phase 29 needs only the IDs (for the tag-set keys) + the tag membership. The player path already uses a `const damageFoodExhaustion=0.1` stub.
   - What's unclear: whether any P29 branch needs `scaling`/`exhaustion` (mob `actuallyHurt` has no food exhaustion, so likely not).
   - Recommendation (CONTEXT discretion): generate the damage-type ID set (needed as tag keys) now; defer the entry FIELDS unless a branch needs them. The `gen_tags` extractor can emit the ID list as a by-product of reading `data/minecraft/damage_type/`.

3. **What is the exact Sulfur despawn/RemoveEntities broadcast on store-remove?** (A2)
   - Recommendation: the death plan greps `tracker.go`/`entity_events.go` for the removal broadcast; if removal alone doesn't broadcast, add the explicit `RemoveEntities` send.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `temp/cache/26.2-inner.jar` | Every javap port | ✓ | 24.95 MB | — (the canonical source) |
| javap (Zulu 25) | Per-method bytecode verify | ✓ | `/c/Program Files/Zulu/zulu-25/bin/javap` | CFR (`java -jar /tmp/cfr.jar`) for readable output |
| CFR | Readable decompile (scope with `--methodname`) | ✓ | `/tmp/cfr.jar` (2.1 MB) | javap (bytecode-level) |
| `level/loot` evaluator | Death loot | ✓ | in-repo (STRUCT-POLISH-01) | — (extend if entity-context conditions needed) |
| All 94 entity loot tables | Death loot | ✓ | embedded `level/loot/data/loot_table/entities/` | — |
| Tag JSON (damage_type + item) | Tag codegen | ✓ | inside the jar `data/minecraft/tags/{damage_type,item}/` | — |
| `tools/` codegen pipeline | gen_tags | ✓ | `cd tools && go run . --version 26.2` | — |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** none — all tooling present.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ `go test -race` in Docker for CGO=1) |
| Config file | none (standard `go test ./...`) |
| Quick run command | `CGO_ENABLED=0 go test ./server/ -run 'Damage\|Hurt\|DamageSource\|Tag' -count=1` |
| Full suite command | `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && go test ./... && (Docker) go test -race ./server/... ./level/...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| MOB-SUB-01 | Player hits a Go-native mob → i-frame-gated health loss, exact vanilla values across armor/absorption/i-frame | unit | `go test ./server/ -run TestApplyDamageEntity` | ❌ Wave 0 |
| MOB-SUB-01 | `on_damage` Emit fires post-mitigation with the final amount | unit | `go test ./server/ -run TestMobOnDamageEmit` | ❌ Wave 0 |
| MOB-SUB-01 | Cross-region hit (region A attacker, region B mob) lands at the barrier, `-race` clean, `strictRegion` clean | integration | `(Docker) go test -race ./server/ -run TestCrossRegionDamage` | ❌ Wave 0 |
| MOB-SUB-02 | `lastDamageSource` set to `{typeTag, attacker}` after a hit; handle attr returns frozen scalars | unit | `go test ./server/ -run TestLastDamageSource` | ❌ Wave 0 |
| MOB-SUB-03 | `src.is("bypasses_armor")` true for FALL/MAGIC, false for PLAYER_ATTACK; nested-tag flatten correct (`panic_causes` ⊇ `is_player_attack` members) | unit | `go test ./data/tag/ -run TestTagMembership` + `go test ./server/ -run TestDamageSourceIs` | ❌ Wave 0 |
| (death) | Mob health≤0 → removed from store, death-status broadcast, loot Item entities spawned, XP orb spawned | unit | `go test ./server/ -run TestMobDeath` | ❌ Wave 0 |
| (oracle) | `TestPluginPigEqualsGoNativePig` STILL GREEN (no AI RNG perturbed) | regression | `go test ./server/ -run TestPluginPigEqualsGoNativePig` | ✅ exists |
| (loot) | `loot.Roll` over `minecraft:entities/pig` parses + rolls headless | unit | `go test ./level/loot/ -run TestEntityLootRoll` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `CGO_ENABLED=0 go test ./server/ -run '<the task's test>' -count=1` + the pig oracle.
- **Per wave merge:** `CGO_ENABLED=0 go build ./... && go vet ./... && go test ./server/... ./level/loot/... ./data/tag/...`.
- **Phase gate:** full suite green + Docker `-race` + `strictRegion` clean + pig oracle GREEN + the `math/rand` global grep gate + a rebuilt `sulfur.exe` before any bot gate.

### Wave 0 Gaps
- [ ] `server/combat_mob_test.go` — exact-value damage tests (armor/absorption/i-frame/bypass-tag) for MOB-SUB-01/02
- [ ] `server/cross_region_damage_test.go` — `strictRegion`+`-race` boundary-hit test for MOB-SUB-01 Folia
- [ ] `data/tag/tag_test.go` — nested-flatten + membership for MOB-SUB-03 (or co-locate in `server/damage_source_test.go`)
- [ ] `server/death_mob_test.go` — death removal + loot + XP
- [ ] `level/loot/entity_loot_test.go` — headless entity-table roll (de-risks A4 FIRST)
- [ ] No framework install needed — Go stdlib testing covers all

## Security Domain

> `security_enforcement` not set in this project's config; the project's standing threat model (T-6-05 server-authoritative health) governs.

### Applicable controls for this phase

| Concern | Applies | Standard Control |
|---------|---------|-----------------|
| Server-authoritative damage (T-6-05) | yes | The client NAMES a target (`ServerboundAttack` = entity id only); the SERVER computes the amount. Never trust a client damage value — `applyDamageEntity` is server-only, exactly as `applyDamage` is. |
| Input validation (forged/unknown target id) | yes | `owningRegion(id)` returns nil for an unknown/despawned id → silent no-op (never panic). Mirrors `lookupPlayerByEntityID` nil-handling. Defensive `Scan` decode (already in handleAttack). |
| Reach gate (T-6-01) | yes | Server-side reach check before applying a mob hit (port the vanilla entity-interaction reach; `attackReach=3.5` exists). A client cannot melee across the map. |
| Cross-region data integrity | yes | Barrier-queued `damageIntent` + owner re-resolve (drop if gone) prevents a stale-id write; `-race`+`strictRegion` gates prove no concurrent store mutation. |
| Loot seed integrity (T-20-05) | yes | The death-loot seed is SERVER-generated (`rand.Int64()`), never client-supplied — same discipline as chest `LootTableSeed`. |

### Known threat patterns

| Pattern | STRIDE | Mitigation |
|---------|--------|-----------|
| Client claims arbitrary damage | Tampering | Server computes amount; packet carries only a target id |
| Forged target id (hit a non-existent/other-region entity to crash or grief) | Tampering/DoS | `owningRegion` nil-guard → no-op; defensive decode; reach gate |
| Cross-region race to corrupt entity state | Tampering | Owner-routed barrier apply; `-race`+`strictRegion` |
| Spoofed loot via client seed | Elevation | Server-only loot seed |

## Sources

### Primary (HIGH confidence)
- `temp/cache/26.2-inner.jar` via `javap -c -p` (Zulu 25), this session:
  - `net.minecraft.world.entity.LivingEntity` — `hurtServer` (full bytecode, the i-frame gate + the `lastDamageSource` set at 449-451), `actuallyHurt` (the 3 mob-vs-player diffs), `baseTick` (i-frame decrement at 451-488 with the `!(instanceof ServerPlayer)` guard), `die` (→ `dropAllDeathLoot` + `broadcastEntityEvent(this,3)` + `setPose(DYING)`), `dropAllDeathLoot`, `dropFromLootTable` (3 overloads), `dropExperience` (the `lastHurtByPlayerMemoryTime` gate + `ExperienceOrb.award`), `getExperienceReward`, `isInvulnerableTo`
  - `net.minecraft.world.damagesource.DamageSource` — fields (`type` Holder, `causingEntity`, `directEntity`), `is(TagKey)` = `type.is(tag)`, `getEntity` = `causingEntity`
  - `net.minecraft.world.entity.Mob` — `getLootTable` (Optional + LivingEntity fallback), `getLootTableSeed`, `getBaseExperienceReward` (the `xpReward` field)
  - `net.minecraft.world.entity.ExperienceOrb` — `award(ServerLevel, Vec3, int)`
- `unzip` of the jar — tag JSON location + counts + nested-ref content: `data/minecraft/tags/damage_type/` (35 tags incl. `bypasses_armor`, `panic_causes`, `is_fire`, `is_player_attack`), `data/minecraft/tags/item/` (224 tags incl. `pig_food`, `cow_food`, `bee_food` with nested `#` refs), `data/minecraft/damage_type/` (51 entries with `{exhaustion,message_id,scaling}`)
- Codebase (read this session): `server/combat.go` (the player port to mirror — all helpers + i-frame consts), `server/entity.go` (`Entity` struct + `getAttributeValue:200`), `server/attack_dispatch.go` (`handleAttack:91`, `applyAttackDamage:214`, `lookupPlayerByEntityID` use), `server/region_transfer.go` (`transferIntent:160-213`, `emitEntityEvent:86`, `regionForEntity:56`, `owningRegion:65`, `withRegion:100`), `server/region_coordinator.go` (the barrier sequence, `applyCrossRegionTransfers:176`), `server/region.go` (`region` struct + `pendingTransfers:132`), `server/block_drop.go` (`NewItemEntity`, the loot-roll + per-stack spawn pattern), `server/chest_loot.go` (the `level/loot` API usage), `level/loot/context.go` (the LootContext field gap), `tools/main.go` (generator list), `tools/gen_item_food.go` (the generator pattern), `tools/java/ExtractAll.java` (the ZipFile jar-read pattern), `server/player_visibility.go:102` (`lookupPlayerByEntityID`)
- `data/entity` — `ExperienceOrb` (id 49), `Item` types generated

### Secondary (MEDIUM confidence)
- `.planning/research/{SUMMARY,ARCHITECTURE,PITFALLS}.md` — the milestone research this phase research grounds against (the parallel-path decision, the cross-region flag, the combat-paraphrase/race/oracle traps)
- `.planning/phases/29-damage-keystone-s2/29-CONTEXT.md` — the 3 locked grey-area decisions

### Tertiary (LOW confidence)
- A1/A2/A4 (per-type xpReward, the exact tracker despawn broadcast, the entity-table parser acceptance) — flagged as plan-time javap/headless verifications, not asserted here.

## Metadata

**Confidence breakdown:**
- Standard stack (reuse map): HIGH — every helper/seam is a real file:line read this session; no new deps confirmed against the no-cgo mandate.
- Architecture (hurt/actuallyHurt/die bytecode + the 3 mob diffs + i-frame site): HIGH — disassembled this session.
- Tag codegen (jar JSON location, nested flatten, `is(tag)` bytecode): HIGH — jar contents inspected directly.
- Cross-region `damageIntent` (the barrier pattern): HIGH on the pattern (mirrors `transferIntent`), MEDIUM on the exact ownership shape (A3 — plan must pick the race-clean variant + prove with `-race`).
- Death/loot/XP: HIGH on the jar chain + the spawn-pattern reuse; MEDIUM on the entity-loot-context gap (A4 — de-risk headless first) and the despawn broadcast (A2).

**Research date:** 2026-06-29
**Valid until:** ~2026-07-29 (stable — the jar is the frozen authority; no fast-moving external deps). Re-verify only if the vendored jar or `level/loot` API changes.
