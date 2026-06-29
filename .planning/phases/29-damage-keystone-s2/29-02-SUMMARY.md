---
phase: 29-damage-keystone-s2
plan: 02
subsystem: mob-hurt-pipeline
tags: [combat, damage, jar-port, mob, living-entity, mob-sub-01, mob-sub-02, keystone]
requires:
  - "data/tag package (Plan 01): DamageTypeTags + DamageTypeIDs — is(tag) reads BY NAME"
  - "server/combat.go free helpers (combatRulesGetDamageAfterAbsorb, maxF, isNaN32/isInf32, hurt* consts)"
  - "server/entity.go Entity.getAttributeValue(*attribute.Attribute) — the REAL *attribute.Map read"
  - "server/region_transfer.go emitEntityEvent (region-scoped plugin dispatch)"
  - "temp/cache/26.2-inner.jar (LivingEntity.hurtServer/actuallyHurt/baseTick; DamageSource.is/getEntity)"
provides:
  - "server/damage_source.go: damageSource{typeTag, attacker} value type + is(tag) + damageTypePlayerAttack/MobAttack/Fall consts"
  - "server/combat_mob.go: applyDamageEntity (hurtServer) + actuallyHurtEntity (actuallyHurt) + getDamageAfterArmorAbsorbEntity + tickMobIFrames + dieEntity seam"
  - "server/entity.go: *Entity hurt fields health/lastHurt/invulnerableTime/hurtTime/hurtDuration/lastDamageSource"
affects:
  - "Phase 29 Plan 03 (attack routing + cross-region damageIntent — calls applyDamageEntity)"
  - "Phase 29 Plan 04 (death/loot/XP — fills the dieEntity seam)"
  - "Phase 31 PanicGoal (reads lastDamageSource.is(panic_causes))"
  - "Phase 36 wolf anger (reads lastDamageSource.attacker)"
tech-stack:
  added: []
  patterns:
    - "sibling *Entity hurt path mirroring the v3-sealed *tickPlayer combat.go (no interface-generalize)"
    - "genuine damage-type tag read (src.is over data/tag.DamageTypeTags) — never const false"
    - "baseTick i-frame decrement as a dedicated RNG-free per-mob loop in tickAI (outside serverAiStep)"
key-files:
  created:
    - "server/damage_source.go"
    - "server/combat_mob.go"
    - "server/combat_mob_test.go"
  modified:
    - "server/entity.go (6 hurt value fields on *Entity)"
    - "server/plugin_mob_decl.go (health = getAttributeValue(MaxHealth) at spawn)"
    - "server/tick_phases.go (tickMobIFrames per-mob loop wired into tickAI)"
decisions:
  - "lastDamageStamp (the gameTime stamp set alongside lastDamageSource in the flag2 block) is NOT modeled — no consumer reads it yet; cited to slot in next to the source store when a reader lands (the source itself is the MOB-SUB-02 deliverable)"
  - "getAbsorptionAmount/setAbsorptionAmount are cited no-ops (constant 0 / no-op store) — a mob carries no absorption field in v1; the fold formula is preserved verbatim so a future MAX_ABSORPTION slots in unchanged (mirrors the player path's absorption stub)"
  - "the i-frame decrement is a SEPARATE per-mob loop in tickAI BEFORE serverAiStep — integer-only/RNG-free, structurally outside the AI RNG flow, so the pig oracle stays byte-identical"
metrics:
  duration: ~10min
  completed: 2026-06-29
---

# Phase 29 Plan 02: Mob Hurt Pipeline Keystone Summary

Built the keystone mob (`*Entity`) hurt pipeline — the jar-faithful sibling of the v3-sealed
`*tickPlayer` combat path. A Go-native mob now loses i-frame-gated health when hit, reading armor
off its REAL `*attribute.Map`, with a genuine `bypasses_armor` tag branch, recording its real
`lastDamageSource`, and firing a region-scoped `on_damage` Emit post-mitigation — every numeric op a
1:1 float32 port of `LivingEntity.hurtServer`/`actuallyHurt`/`baseTick` verified against
`temp/cache/26.2-inner.jar` (MOB-SUB-01, MOB-SUB-02).

## What Shipped

- **`server/damage_source.go`** — the real ported `DamageSource` value type: `damageSource{typeTag
  damageTypeID, attacker int32}` (a plain value, NO pointers — the Folia snapshot rule) with
  `is(tag)` reading the genuine `data/tag.DamageTypeTags` membership (port of `DamageSource.is(TagKey)
  == type.is(tag)`, javap-cited). `damageTypePlayerAttack/MobAttack/Fall` consts resolve from
  `data/tag.DamageTypeIDs` (by NAME, no magic ids). `damageSourcePlayerAttack(attackerID)` constructor.
- **`server/combat_mob.go`** — `applyDamageEntity` (port of `LivingEntity.hurtServer`: the `>10.0F`
  i-frame gate, the `amount<=lastHurt` no-op, the fresh-hit `invulnerableTime=20`/`hurtDuration=hurtTime=10`
  arm, the `flag2` `lastDamageSource = src` set at bytecode 444-462, the `dieEntity` lethal tail) +
  `actuallyHurtEntity` (port of `LivingEntity.actuallyHurt` — NOT Player — with the three mob diffs: no
  `causeFoodExhaustion`, no `SetHealth` client send, region-scoped `emitEntityEvent`) +
  `getDamageAfterArmorAbsorbEntity` (the GENUINE `src.is("bypasses_armor")` read + the shared
  `combatRulesGetDamageAfterAbsorb` over the mob's REAL `*attribute.Map`) +
  `getDamageAfterMagicAbsorbEntity` + `tickMobIFrames` (`LivingEntity.baseTick`) + the `dieEntity`
  Plan-04 seam. Reuses every `combat.go` free helper verbatim — zero duplication.
- **`server/entity.go`** — the six `*Entity` hurt value fields (`health`, `lastHurt`,
  `invulnerableTime`, `hurtTime`, `hurtDuration`, `lastDamageSource`), mirroring the `tickPlayer`
  combat fields, each citing its `LivingEntity` field and carrying the same snapshot-friendly
  exemption `ai`/`attributes` carry.
- **`server/plugin_mob_decl.go`** — `health = float32(e.getAttributeValue(attribute.MaxHealth))` at
  `spawnDeclaredMob` (the single shared goal-bearing-mob spawn path the vanilla pig + egg mobs route
  through; port of `LivingEntity.<init>` `setHealth(getMaxHealth())`), so a mob is never born dead.
- **`server/tick_phases.go`** — `tickMobIFrames` wired as a dedicated per-mob loop in `tickAI`,
  BEFORE `serverAiStep`, integer-only/RNG-free.
- **`server/combat_mob_test.go`** — the 7-behavior Wave-0 scaffold with javap-exact float32 values
  (FreshHit, IFrameExcessGate, Armor=20→10→4.0, BypassesArmor fall-vs-player, LastDamageSource,
  MobIFrameDecrement, MobOnDamageEmit).

## jar Verification (javap -c -p, this session)

- `LivingEntity.hurtServer`: i-frame gate `(float)invulnerableTime > 10.0F && !is(BYPASSES_COOLDOWN)`
  (bytecode 185-200); `amount<=lastHurt` no-op (208); `actuallyHurt(amount-lastHurt)` (226);
  else `lastHurt=amount, invulnerableTime=20` (242-248), `actuallyHurt(amount)` (255),
  `hurtDuration=10, hurtTime=hurtDuration` (259-269); **`flag2` block `lastDamageSource = source`**
  at 444-451 (`iload 9; ifeq 510; putfield lastDamageSource`).
- `LivingEntity.actuallyHurt`: `getDamageAfterArmorAbsorb` (13) → `getDamageAfterMagicAbsorb` (20) →
  `Math.max` absorption fold (34) → `if (amount==0) return` (60-117) → `recordDamage` (124, stub) →
  `setHealth` (134) → `setAbsorption` (144) → `gameEvent(ENTITY_DAMAGE)` (151, stub). Confirmed NO
  `causeFoodExhaustion` (Player-only) and the tail ends with `gameEvent`, not a SetHealth wire send.
- `LivingEntity.baseTick`: `if(hurtTime>0)hurtTime--` (450-464, unconditional) +
  `if(invulnerableTime>0 && !(this instanceof ServerPlayer))invulnerableTime--` (467-488) — the
  instanceof guard is false for a mob, so it always decrements.
- `DamageSource.is(TagKey)` = `type.is(tag)` (Holder membership); `DamageSource.getEntity()` =
  `causingEntity` → the `attacker int32` thin-id.

## Verification

- `CGO_ENABLED=0 go build ./...` clean; `CGO_ENABLED=0 go vet ./...` clean.
- `CGO_ENABLED=0 go test ./server/ -run 'Damage|Hurt|DamageSource'` green (all 7 Task-0 behaviors).
- `CGO_ENABLED=0 go test ./server/ -run TestPluginPigEqualsGoNativePig` **GREEN** — the i-frame
  decrement is integer-only and outside `serverAiStep`, so no AI RNG draw order shifted.
- Full `CGO_ENABLED=0 go test ./server/` package exits 0 (29s); `./data/tag/` + `./level/attribute/`
  green.
- `go list -deps ./... | grep -i gopy` EMPTY (no cgo / no new deps).
- Every ported function cites its jar method in a code comment.

## Deviations from Plan

None — plan executed exactly as written. The four tasks landed in order (test scaffold RED → 
damage_source.go → entity.go fields + wiring → combat_mob.go GREEN). The plan's discretionary
no-op stubs (lastDamageStamp, absorption accessors) were taken per the cited rationale in the
Decisions frontmatter, mirroring the player path's existing stubs.

### Note on the test-runner panic artifact

A multi-package `go test ./server/ ./data/tag/ ./level/attribute/` run once printed a `FAIL` driven
by `TestRegionPanicIsolated`'s deliberate region-goroutine panic (a `conc` panic-isolation test that
prints a stack trace to stderr by design). The test PASSES in isolation and the `./server/` package
exits 0 on every standalone run — it is a display/buffering artifact of the panic-isolation test, not
a regression from this plan (the test, and the suite, are green).

## Known Stubs

- **`dieEntity`** — a minimal Plan-04 SEAM (cited TODO): records the lethal source and pins health at
  0 (the isDeadOrDying state, so a dead mob takes no further damage). The full `LivingEntity.die`
  port (loot + XP + death broadcast + store removal) is Plan 29-04's deliverable. This plan
  intentionally stops at "health hits 0" per the objective; the seam is structured so Plan 04
  replaces it with no call-site change.
- **`recordDamage` / `gameEvent(ENTITY_DAMAGE)`** — cited no-ops in `actuallyHurtEntity`, exactly as
  the player path leaves them (no combat-tracker / game-event subsystem in v1).
- **`getAbsorptionAmount`/`setAbsorptionAmount`** — constant 0 / no-op store (a mob has no absorption
  field in v1); the fold formula is preserved verbatim so a future MAX_ABSORPTION slots in unchanged.

These are all intentional per the plan objective ("stops at health hits 0 and leaves dieEntity as a
documented seam Plan 04 fills") — not goal-blocking; the mob hurt pipeline is fully functional.

## Self-Check: PASSED

- server/damage_source.go — FOUND
- server/combat_mob.go — FOUND
- server/combat_mob_test.go — FOUND
- server/entity.go (hurt fields) — FOUND
- commit ffcc95f4 (test RED) — FOUND
- commit 8e5db2cb (damage_source) — FOUND
- commit 99f49a43 (entity fields + wiring) — FOUND
- commit 35b75f5d (combat_mob) — FOUND
