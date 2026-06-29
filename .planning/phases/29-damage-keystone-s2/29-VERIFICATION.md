---
phase: 29-damage-keystone-s2
verified: 2026-06-29T00:00:00Z
status: human_needed
score: 5/5 must-haves verified
overrides_applied: 0
human_verification:
  - test: "In-game: spawn a Go-native mob (pig), melee it with a real 26.2 client"
    expected: "Mob loses health, flashes red (hurt animation), i-frame-gates rapid follow-up hits, and on lethal hits dies (status-3 death animation), drops 1-3 raw porkchop Item entities, and spawns a 1-3 XP orb"
    why_human: "Visual/client-observed behavior — the hurt flash, death animation, dropped items rendering and XP orb pickup cannot be confirmed programmatically; the full pipeline (damage->health->death->drops->XP) is unit-verified headless but the on-wire/visual rendering needs a live client (per 29-VALIDATION.md Manual-Only row)"
---

# Phase 29: Damage Keystone (S2) Verification Report

**Phase Goal:** A mob can take and deal damage through a faithful, parallel `*Entity` hurt pipeline, remembering what hurt it — the keystone every panic, target-selector, melee, and wolf-anger behavior reads.
**Verified:** 2026-06-29
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth (Success Criterion) | Status | Evidence |
|---|---------------------------|--------|----------|
| 1 | Player can hit a Go-native mob; it loses health, i-frame/invulnerable-time gated identical to the jar; `actuallyHurtEntity` reads the mob's real `*attribute.Map` | ✓ VERIFIED | `server/combat_mob.go`: `applyDamageEntity` (hurtServer port) has the `>10.0F` excess gate, `amount<=lastHurt` no-op, fresh-hit `invulnerableTime=20`/`hurtDuration=hurtTime=10` arm; `actuallyHurtEntity` reads armor via `e.getAttributeValue(attribute.Armor/ArmorToughness)` with the `math.Floor` d2f cast. Tests green: `go test ./server/ -run 'Damage\|Hurt'` (TestApplyDamageEntity_FreshHit, _IFrameExcessGate, TestActuallyHurtEntity_Armor). Reuses `combatRulesGetDamageAfterAbsorb`/`maxF`/`isNaN32`/`isInf32` from combat.go (defined once, l.543/603/613/614 — NOT duplicated). i-frame decrement (`tickMobIFrames`) wired as a separate integer-only loop BEFORE serverAiStep (tick_phases.go:310). |
| 2 | Mob records its real `lastDamageSource` (ported source, not a faked flag), readable via `was_hurt`/`last_damage_type` handle attr | ✓ VERIFIED | `server/damage_source.go`: real `damageSource{typeTag, attacker}` value type (pointer-free, Folia-safe) with genuine `is(tag)`. `server/entity.go:178` `lastDamageSource damageSource` field. `combat_mob.go:104-106` sets `e.lastDamageSource = src` in the flag2 fresh-hit block (bytecode 444-462). `server/plugin_entity.go:177,185` handle attrs `was_hurt` (`e.hurtTime>0`) + `last_damage_type` (`e.lastDamageSource.typeTag`), both frozen scalars re-resolved through region-bound `h.store()`, listed in AttrNames (l.200). Test TestWasHurtHandleAttr green. |
| 3 | `source.is(tag)` genuine against a jar-extracted damage-type tag table (no `const false`); `on_damage` Emit at post-mitigation | ✓ VERIFIED | `data/tag/tags.go` (generated): `DamageTypeTags["bypasses_armor"]` = 19 real members, `panic_causes` = 29, `is_player_attack` = 3 (26/34/37) — `panic_causes` is a proper superset (nested `#`-ref flattened). `combat_mob.go:211` `getDamageAfterArmorAbsorbEntity` gates on `!src.is("bypasses_armor")` (genuine read, no `const false` for the bypass). `actuallyHurtEntity` fires `t.regionForEntity(e).emitEntityEvent(host.EventDamage, ...)` at the POST-mitigation site (combat_mob.go:181, after armor+magic+absorption fold). Tag tests (TestTagMembership pos+neg, TestNestedTagFlatten superset) green. |
| 4 | Cross-region hits route through OWNER's region as a barrier-queued `damageIntent` (never `cur()`); Docker -race + strictRegion clean | ✓ VERIFIED | `region_transfer.go:173` `type damageIntent struct`, `:186` `queueDamageIntent` (appends to SOURCE region's `pendingDamage`, tagged `to`), `:254` `applyCrossRegionDamage` (re-resolves owner by id via `owningRegion`, drop-if-gone guard, applies via `withRegion(owner)`, `[:0]` reset). `region.go:144` `pendingDamage []damageIntent`. Wired at the barrier: `region_coordinator.go:184` `t.applyCrossRegionDamage()` immediately after `applyCrossRegionTransfers()`. `attack_dispatch.go` has ZERO `t.cur()`/`t.only()` calls (grep clean — only comments explaining the avoided trap). **Docker -race re-run THIS verification: `go test -race -run 'CrossRegionDamage\|SameRegionDamage\|ReachGate\|WasHurt' ./server/` = ok 2.2s, no DATA RACE.** strictRegion armed in the cross-region tests (damage_region_test.go:69,104) with a different-region precondition assert (l.78). |
| 5 | Standing: every op float32, helpers reused not duplicated, CGO=0 + no new deps, pig oracle GREEN | ✓ VERIFIED | `CGO_ENABLED=0 go build ./...` exit 0. Helpers reused from combat.go (no duplicate definitions in combat_mob.go — grep confirms the 5 free helpers live only in combat.go). `go list -deps ./... \| grep -i gopy` EMPTY (no cgo/new-dep leak). `TestPluginPigEqualsGoNativePig` GREEN (1.2s) — i-frame decrement is integer-only and outside serverAiStep, no AI RNG perturbed. Every ported function cites its jar method in comments (LivingEntity.hurtServer/actuallyHurt/baseTick/die/dropExperience; DamageSource.is). |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `data/tag/tags.go` | Generated DamageTypeTags + ItemTags membership | ✓ VERIFIED | Generated header present; DamageTypeTags (34), ItemTags (224), DamageTypeIDs/Names. Real membership (bypasses_armor 19, panic_causes 29). |
| `tools/java/GenTags.java` + `tools/gen_tags.go` | ZipFile extractor + generator with nested flatten | ✓ VERIFIED | Registered `{"tags", genTags}` in tools/main.go:45. Nested `#`-ref flatten proven by the superset test. |
| `server/damage_source.go` | damageSource value type + is(tag) | ✓ VERIFIED | Pointer-free `{typeTag, attacker}`, `is(tag)` reads DamageTypeTags by name, consts resolve via DamageTypeIDs (no magic ids). |
| `server/combat_mob.go` | applyDamageEntity/actuallyHurtEntity/getDamageAfterArmorAbsorbEntity/tickMobIFrames | ✓ VERIFIED | All present, 1:1 ported with the 3 mob diffs + lastDamageSource set + region-scoped Emit; reuses combat.go helpers. |
| `server/entity.go` | 6 hurt fields + dead guard | ✓ VERIFIED | health/lastHurt/invulnerableTime/hurtTime/hurtDuration/lastDamageSource (l.157-178) + dead bool (l.187), all plain values (snapshot-friendly). health initialized to MaxHealth at spawn (plugin_mob_decl.go:393). |
| `server/attack_dispatch.go` | handleAttack dual-resolve + reach gate + routing | ✓ VERIFIED | handleMobAttack mob branch resolves via owningRegion, reach-gated, same-region synchronous vs cross-region queueDamageIntent; no cur()/only(). |
| `server/region_transfer.go` / `region.go` / `region_coordinator.go` | damageIntent queue + barrier drain | ✓ VERIFIED | All present and wired at the coordinator barrier. |
| `server/plugin_entity.go` | was_hurt / last_damage_type frozen-scalar attrs | ✓ VERIFIED | Both cases + AttrNames; resolve through h.store(). |
| `server/death_mob.go` | dieEntity + dropMobLoot + dropMobExperience | ✓ VERIFIED | Death removal (regionForEntity, not cur()), status-3 broadcast, loot roll into owner-region Item entities, XP orb at the Animal.getBaseExperienceReward value. |
| `level/loot/` extension | entity-table conditions/functions (A4) | ✓ VERIFIED | any_of/all_of/killed_by_player/entity_properties + furnace_smelt/enchanted_count_increase handlers added; EntityLootParams context fields. TestEntityLootRoll green; golden chest/block rolls not perturbed. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| combat_mob.go actuallyHurtEntity | combat.go free helpers | combatRulesGetDamageAfterAbsorb / maxF | ✓ WIRED | Called, not duplicated (helpers defined only in combat.go). |
| combat_mob.go getDamageAfterArmorAbsorbEntity | data/tag DamageTypeTags | src.is("bypasses_armor") | ✓ WIRED | Genuine tag read; no const false. |
| combat_mob.go actuallyHurtEntity | Entity.getAttributeValue(*attribute.Map) | getAttributeValue(attribute.Armor) | ✓ WIRED | Reads real attribute map with d2f floor cast. |
| attack_dispatch.go handleAttack | combat_mob.go applyDamageEntity | same-region fast path OR queueDamageIntent | ✓ WIRED | Dual-resolve routes to applyDamageEntity. |
| region_coordinator.go barrier | combat_mob.go applyDamageEntity | applyCrossRegionDamage -> withRegion(owner) | ✓ WIRED | Drained at the quiescent barrier after transfers. |
| plugin_entity.go Attr | *Entity.lastDamageSource | case "last_damage_type" frozen scalar | ✓ WIRED | Re-resolved on owner via h.store(). |
| combat_mob.go applyDamageEntity | death_mob.go dieEntity | if e.health <= 0 { t.dieEntity(e, src) } | ✓ WIRED | Lethal tail drives death; seam replaced by Plan 04. |
| death_mob.go dropMobLoot | level/loot.Roll | LoadTable -> Roll -> NewItemEntity -> regionForEntity(e).entities.add | ✓ WIRED | Owner-region routing, server-generated seed. |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build clean (CGO=0) | `CGO_ENABLED=0 go build ./...` | exit 0 | ✓ PASS |
| Core suites green | `CGO_ENABLED=0 go test ./server/ ./data/tag/ ./level/loot/ -count=1` | all ok (server 29.0s) | ✓ PASS |
| Pig oracle green | `CGO_ENABLED=0 go test ./server/ -run TestPluginPigEqualsGoNativePig` | ok 1.2s | ✓ PASS |
| No cgo/dep leak | `CGO_ENABLED=0 go list -deps ./... \| grep -i gopy` | empty | ✓ PASS |
| Docker -race + strictRegion (cross-region path) | `docker run ... go test -race -run 'CrossRegionDamage\|SameRegionDamage\|ReachGate\|WasHurt' ./server/` | ok 2.2s, no DATA RACE | ✓ PASS |
| Tag table is real (not const false) | inspect data/tag/tags.go | bypasses_armor=19, panic_causes=29 members; nested flatten present | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| MOB-SUB-01 | 29-02, 29-03, 29-04 | Mob take/deal damage — applyDamageEntity/actuallyHurtEntity reading real *attribute.Map, i-frame gating, on_damage Emit post-mitigation, attack flows wired, death | ✓ SATISFIED | combat_mob.go + attack_dispatch.go + death_mob.go; SC1/3/4 verified; tests green. |
| MOB-SUB-02 | 29-02, 29-03 | Mob records lastDamageSource (real ported source), readable via was_hurt/last_damage_type | ✓ SATISFIED | damage_source.go + entity.go field + plugin_entity.go attrs; SC2 verified. |
| MOB-SUB-03 | 29-01 | Damage-type tag membership data-driven (no const false) | ✓ SATISFIED | data/tag/tags.go generated with genuine jar-flattened membership; tag tests pos+neg+superset green. |

No orphaned requirements: REQUIREMENTS.md maps exactly MOB-SUB-01/02/03 to Phase 29; all claimed across the 4 plans.

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| combat_mob.go | absorption getter/setter return constant 0 / no-op | ℹ️ Info | Vanilla mob MAX_ABSORPTION base IS 0; formula preserved verbatim for a future read. Behavior-identical to jar. Not a stub of observable behavior. |
| combat_mob.go | recordDamage / gameEvent(ENTITY_DAMAGE) cited no-ops | ℹ️ Info | Same as the v3-sealed player path; no combat-tracker/game-event subsystem in v1. Matches jar tail structure. |
| death_mob.go | XP-orb DATA_VALUE (per-orb metadata) not wired | ℹ️ Info | Reward AMOUNT correct (1-3) and split into the right orb count; only the SynchedEntityData accessor deferred. Affects visual XP value display, not the headless-correct reward. |
| death_mob.go | killedByPlayer = player-attack-source proxy (lastHurtByPlayerMemoryTime not tracked) | ℹ️ Info | Credits a direct melee kill (the v1 case); the memory-time window is a cited deferral structured to become a real read. Matches the documented CONTEXT scope. |
| attack_dispatch.go | mob knockback/sweep deferred (sprintKb computed, unused) | ℹ️ Info | Hit LANDS (damage + lastDamageSource + Emit all fire); no consumer reads mob post-hit velocity yet. Cited follow-on, does not block the keystone. |

All anti-pattern matches are vanilla-default-equal v1 stubs, each cited and structured to become a real read per the CLAUDE.md 1:1 mandate. None affect the observable damage/death/source behavior the phase goal delivers. No 🛑 blockers, no ⚠️ warnings.

### Human Verification Required

The damage pipeline is fully unit-verified headless (health drop, i-frame gating, armor fold, lastDamageSource, cross-region routing, death removal, loot roll, XP reward), but the on-wire/visual rendering needs a live client:

1. **In-game hit + death + drops** — Spawn a pig on a live 26.2 server (SULFUR_TEST_KIT), melee it.
   - Expected: pig loses health and flashes red on each hit; rapid follow-up hits are i-frame-gated; on a lethal hit the pig plays the death animation, despawns, drops 1-3 raw porkchop, and spawns a pickup-able XP orb.
   - Why human: visual hurt flash, death animation, item/orb rendering, and pickup are client-observed; per 29-VALIDATION.md Manual-Only row.

### Gaps Summary

No gaps. All 5 ROADMAP success criteria, all 3 requirements (MOB-SUB-01/02/03), and every must-have artifact/key-link verified against the live codebase. The build is clean (CGO=0), the full CGO=0 suite is green, the pig oracle is GREEN, no gopy/cgo leak, and the Docker -race + strictRegion gate on the cross-region damage path was re-run this verification and is CLEAN. The cited v1 stubs are all vanilla-default-equal and structured for future real reads (consistent with the 1:1 mandate) — none block the observable goal.

Status is **human_needed** (not gaps_found) solely because the in-game visual confirmation (hurt flash / death animation / drops / XP orb rendering) cannot be verified programmatically — the automated chain is complete and passing.

---

_Verified: 2026-06-29_
_Verifier: Claude (gsd-verifier)_
