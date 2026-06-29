---
phase: 29-damage-keystone-s2
plan: 03
subsystem: combat
tags: [combat, damage, mob, attack-routing, cross-region, folia, jar-port, mob-sub-01, mob-sub-02, keystone]
requires:
  - phase: 29-damage-keystone-s2 (Plan 02)
    provides: "applyDamageEntity/actuallyHurtEntity (LivingEntity.hurtServer port), damageSource value type, damageSourcePlayerAttack, *Entity hurt fields (health/hurtTime/invulnerableTime/lastDamageSource), region-scoped emitEntityEvent"
  - phase: 27-folia-regionization (Plan 03)
    provides: "the transferIntent discipline (pendingTransfers + detectTransfers + applyCrossRegionTransfers), owningRegion/withRegion/regionForColumn, the strictRegion guard, the coordinator barrier"
  - phase: 17-gameplay-completion (Plan 11)
    provides: "handleAttack player path + the Player.attack(Entity) damage math (ATTACK_DAMAGE x strength-scale x crit), applyAttackDamage hurtOrSimulate bridge, withinAttackReach, attackReach=3.5"
provides:
  - "server/attack_dispatch.go: handleAttack dual-resolve (player OR mob victim) + handleMobAttack + withinAttackReachEntity + applyMobAttackDamage + canCriticalAttackEntity"
  - "server/region_transfer.go: damageIntent type + queueDamageIntent (source-region-owned, tagged to) + applyCrossRegionDamage (barrier drain)"
  - "server/region.go: region.pendingDamage []damageIntent field"
  - "server/region_coordinator.go: applyCrossRegionDamage() wired at the barrier next to applyCrossRegionTransfers()"
  - "server/plugin_entity.go: was_hurt / last_damage_type frozen-scalar handle attrs"
affects:
  - "Phase 29 Plan 04 (death/loot/XP — the dieEntity seam now has a real caller via the routed mob hits)"
  - "Phase 31 PanicGoal (reads entity.was_hurt + entity.last_damage_type against the panic_causes tag)"
  - "Phase 36 wolf anger (reads lastDamageSource.attacker via the routed player_attack source)"
tech-stack:
  added: []
  patterns:
    - "dual-resolve dispatch: handleAttack routes a non-player target to the mob hurt pipeline via owningRegion (never cur())"
    - "cross-region barrier-queue WRITE (the project's first): queueDamageIntent on the SOURCE region tagged to the owner, drained quiescent at the coordinator (mirrors transferIntent EXACTLY)"
    - "frozen-scalar handle read of damage state (was_hurt = hurtTime>0, last_damage_type = lastDamageSource.typeTag) — the goal never holds a live DamageSource"
key-files:
  created:
    - "server/damage_region_test.go"
  modified:
    - "server/attack_dispatch.go (handleMobAttack dual-resolve + the three *Entity sibling helpers)"
    - "server/region_transfer.go (damageIntent + queueDamageIntent + applyCrossRegionDamage)"
    - "server/region.go (pendingDamage field)"
    - "server/region_coordinator.go (applyCrossRegionDamage barrier call)"
    - "server/plugin_entity.go (was_hurt / last_damage_type attrs + AttrNames)"
key-decisions:
  - "The same-region vs cross-region split is decided by the ATTACKER's region (regionForColumn(columnOf(p.x,p.z))), NOT t.cur() — handleAttack runs on the dispatch goroutine with NO region registered, where cur() would fall to region 0 (or PANIC under strictRegion). This is a faithful-correctness deviation from the plan's literal t.cur() (Rule 1/3, see Deviations)."
  - "Mob knockback + sweep-over-mobs are a cited follow-on, NOT shipped: the hit LANDS (damage + lastDamageSource + on_damage emit all fire), but the post-hit impulse for a mob victim is deferred — no consumer reads a mob's post-hit velocity yet, and a cross-region mob's velocity write is a barrier concern. sprintKb is computed for the faithful trace but not yet consumed."
  - "was_hurt predicate = e.hurtTime > 0 (LivingEntity.hurtTime, the red-flash window set to 10 on a fresh hit, decremented in baseTick) — the canonical 'this mob was just hurt' signal PanicGoal reacts to."
patterns-established:
  - "Pattern 1: cross-region damageIntent barrier-queue — the SAME transferIntent shape (source-owned slice, tagged `to`, drained centrally, owner re-resolved by id with drop-if-gone)"
  - "Pattern 2: dual-resolve attack dispatch — player path unchanged; a non-player id falls through to handleMobAttack which mirrors the player damage math verbatim"
requirements-completed: [MOB-SUB-01, MOB-SUB-02]

duration: ~30min
completed: 2026-06-29
---

# Phase 29 Plan 03: Attack Routing + Cross-Region Damage Barrier Summary

**A player's melee hit now reaches a Go-native mob — same-region synchronously, cross-region via the
project's FIRST true cross-region write (a damageIntent queued on the source region and drained at the
quiescent coordinator barrier) — reach-gated, Docker -race + strictRegion clean, with goals reading
the real lastDamageSource through frozen was_hurt / last_damage_type handle scalars (MOB-SUB-01/02).**

## Performance

- **Duration:** ~30 min
- **Tasks:** 5 (Task 0 TDD RED scaffold + Tasks 1-4)
- **Files modified:** 6 (5 modified, 1 created)

## Accomplishments

- **handleAttack dual-resolve** — a non-player target now routes to `handleMobAttack`, the mob arm
  that mirrors the `Player.attack(Entity)` bytecode verbatim (javap-verified: `Player.attack` calls
  `target.hurtOrSimulate` at bytecode 242 → `LivingEntity.hurtServer` at 184, so the same ATTACK_DAMAGE
  × strength-scale × crit math applies to a mob). The victim is resolved through `owningRegion` (never
  `cur()` — the cross-region-victim-to-region-0 trap).
- **Cross-region damage barrier-queue (THE research flag, T-29-02)** — `damageIntent` + `pendingDamage`
  + `queueDamageIntent` + `applyCrossRegionDamage` mirror the proven `transferIntent` discipline EXACTLY:
  a boundary hit is queued on the ATTACKER's region (the actor's own slice — Assumption A3), tagged to
  the owner, and applied at the quiescent coordinator barrier next to `applyCrossRegionTransfers`, with
  the owner re-resolved by id (drop-if-gone). This is the project's first true cross-region WRITE.
- **Same-region fast path** — a hit whose owner == the attacker's region applies synchronously inline
  via `applyMobAttackDamage` (the mob `hurtOrSimulate` boolean bridge), gating the (deferred) tail on the
  hit landing exactly as the player path gates on `if (hurt)`.
- **was_hurt / last_damage_type handle attrs** — frozen scalars (`hurtTime>0` and
  `lastDamageSource.typeTag`), host-computed and re-resolved through the region-bound `h.store()` (never
  `cur()`), so a Starlark goal reads the genuine damage state without holding a live source (MOB-SUB-02).
- **Reach gate reused** — `withinAttackReachEntity` mirrors `withinAttackReach` (squared distance vs
  `attackReach`=3.5) for the *Entity victim — a client cannot melee a mob across the map (T-29-01).

## Task Commits

1. **Task 0: cross-region/dual-resolve test scaffold (TDD RED)** - `62be9fe3` (test)
2. **Task 1: damageIntent + pendingDamage + queueDamageIntent** - `a740dac2` (feat)
3. **Task 2: applyCrossRegionDamage drain at the barrier** - `9e8a46eb` (feat)
4. **Task 3: handleAttack dual-resolve (mob victim + reach + routing)** - `133bcd73` (feat)
5. **Task 4: was_hurt / last_damage_type handle attrs** - `b468a97b` (feat)

## Files Created/Modified

- `server/damage_region_test.go` (created) - 5 tests: TestSameRegionDamage (synchronous health drop),
  TestCrossRegionDamage (queued + barrier-drained, strictRegion armed), TestCrossRegionDamage_DropIfGone
  (despawn-before-barrier guard), TestReachGate, TestWasHurtHandleAttr.
- `server/attack_dispatch.go` - `handleAttack` dual-resolve; `handleMobAttack` (the mob arm: owningRegion
  resolve, reach gate, shared Player.attack math, same/cross-region routing); `withinAttackReachEntity`,
  `applyMobAttackDamage`, `canCriticalAttackEntity` (the *Entity siblings).
- `server/region_transfer.go` - `damageIntent` type, `queueDamageIntent`, `applyCrossRegionDamage`.
- `server/region.go` - `pendingDamage []damageIntent` field (next to `pendingTransfers`).
- `server/region_coordinator.go` - `t.applyCrossRegionDamage()` at the barrier after the transfer drain.
- `server/plugin_entity.go` - `was_hurt` / `last_damage_type` Attr cases + AttrNames.

## jar Verification (javap -c -p temp/cache/26.2-inner.jar, this session)

- `DamageSources.playerAttack(Player)`: `DamageTypes.PLAYER_ATTACK` + the player as causing entity —
  exactly what `damageSourcePlayerAttack(p.entityID)` builds.
- `Player.attack(Entity)`: `target.hurtOrSimulate(source, total)` at bytecode 242 → for a LivingEntity
  routes to `LivingEntity.hurtServer` at 184 — so the same melee damage math applies to a mob target as
  to a player target, and `applyDamageEntity` (the mob hurtServer port from Plan 02) is the correct sink.
- `Attributes.ENTITY_INTERACTION_RANGE` exists (base 3.0) — confirms `attackReach = 3.0 + 0.5` slack.

## Decisions Made

- **Attacker-region split, not `t.cur()`** (see Deviations) — the same/cross-region decision uses
  `regionForColumn(columnOf(p.x, p.z))` because the dispatch goroutine has no region registered.
- **Mob knockback + sweep deferred (cited)** — the hit lands and all damage state fires; the post-hit
  impulse for a mob victim is a follow-on (no consumer + a cross-region velocity write is a barrier
  concern). `sprintKb` is computed for the faithful trace, marked `_ = sprintKb` with the citation.
- **`was_hurt = hurtTime > 0`** — the LivingEntity.hurtTime red-flash window, the canonical hurt signal.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Same/cross-region split uses the attacker's region, not `t.cur()`**
- **Found during:** Task 3 (handleAttack dual-resolve)
- **Issue:** The plan (Task 3 action + 29-PATTERNS) wrote the routing as `if ownerRegion == t.cur() {
  fast } else { queueDamageIntent(t.cur(), ...) }`. But `handleAttack` runs from `applyInput` on the
  DISPATCH goroutine with NO region registered. Under the strictRegion gate the plan itself mandates
  (Task 4 / T-29-02), `t.cur()` there would PANIC; without it, `t.cur()` silently falls to region 0 —
  the exact trap the whole regionization gate guards. Either way the literal `t.cur()` is wrong.
- **Fix:** Resolve the attacker's region explicitly via `attackerRegion := regionForColumn(columnOf(p.x,
  p.z))` and split on `ownerRegion == attackerRegion`; pass `attackerRegion` as the source region to
  `queueDamageIntent`. No `t.cur()` / `only()` call appears in attack_dispatch.go (grep-verified).
- **Files modified:** server/attack_dispatch.go
- **Verification:** TestCrossRegionDamage arms `strictRegion=true` and does NOT panic; Docker -race clean.
- **Committed in:** 133bcd73 (Task 3 commit)

**2. [Rule 2 - Missing Critical] `applyMobAttackDamage` threads the real damageSource**
- **Found during:** Task 3 (handleMobAttack same-region path)
- **Issue:** A first cut of `applyMobAttackDamage` built `damageSourcePlayerAttack(0)` internally,
  dropping the real attacker id — which would break MOB-SUB-02's lastDamageSource.attacker for wolf
  anger (P36) and any attacker-attribution consumer.
- **Fix:** `applyMobAttackDamage(mob, src, amount)` takes the genuine `src` from handleMobAttack (the
  player_attack source carrying `p.entityID`) and passes it to applyDamageEntity.
- **Files modified:** server/attack_dispatch.go
- **Verification:** TestWasHurtHandleAttr asserts last_damage_type == player_attack id; full suite green.
- **Committed in:** 133bcd73 (Task 3 commit)

**3. [Rule 1 - Bug] Cross-region test fixture must straddle the chunk seam**
- **Found during:** Task 3 (running the Task 0 cross-region test)
- **Issue:** The Task 0 scaffold placed the attacker at world X=24 (chunk 1 = region 1) attacking a mob
  at X=24.5 (also region 1) — same region, so it never exercised the cross-region path. Regions are a
  per-CHUNK `(X^Z)&1` checkerboard, so the fixture must place attacker and mob in ADJACENT chunks.
- **Fix:** Mob at X=16.5 (chunk 1 → region 1), attacker at X=15.0 (chunk 0 → region 0), 1.5 blocks apart
  (within reach), with a precondition assert that they are in different regions.
- **Files modified:** server/damage_region_test.go
- **Verification:** TestCrossRegionDamage now genuinely queues + barrier-drains; precondition asserts pass.
- **Committed in:** 133bcd73 (Task 3 commit, with the routing)

---

**Total deviations:** 3 auto-fixed (2 bugs, 1 missing critical)
**Impact on plan:** All three are correctness fixes for the plan's stated invariants (strictRegion-safe
routing, real lastDamageSource, a genuine cross-region fixture). No scope creep; the plan's artifacts +
key-links all landed as specified.

## Verification

- `CGO_ENABLED=0 go build ./...` clean; `CGO_ENABLED=0 go vet ./...` clean.
- `CGO_ENABLED=0 go test ./server/ -run 'Attack|Region|Damage|Reach|WasHurt|Hurt'` green.
- Full `CGO_ENABLED=0 go test ./server/` package exits 0 (~30s).
- `TestPluginPigEqualsGoNativePig` **GREEN** — the routing is event-driven (dispatch-side), touches no
  AI RNG (serverAiStep / navigation.tick), so the pig oracle stream is byte-identical.
- **Docker -race + strictRegion CLEAN** (T-29-02 BLOCKING gate): `go test -race -run
  'CrossRegionDamage|SameRegionDamage' ./server/` passed (2.1s), and the broader `-race -run
  'Attack|Region|Damage|Reach|WasHurt|Hurt'` passed (7.0s) — no DATA RACE, strictRegion did not panic.
- `go list -deps ./... | grep -i gopy` EMPTY (no cgo / no new deps; CGO_ENABLED=0 preserved).

## Threat Model Coverage

- **T-29-01 (Tampering — forged/out-of-reach/cross-region target):** mitigated — `owningRegion(id)`
  nil-guard silent no-op, `withinAttackReachEntity` squared-distance gate, victim resolved via
  owningRegion (never client-supplied resolution, never cur()).
- **T-29-02 (Tampering — cross-region damage race):** mitigated — barrier-queued damageIntent on the
  source region, drained quiescent at the coordinator, owner re-resolved by id (drop if gone); proven
  Docker -race + strictRegion clean. No mid-tick cross-region store mutation.
- **T-29-06 (Tampering — handle read from a region goroutine):** mitigated — the was_hurt /
  last_damage_type reads resolve through the region-bound `h.store()`, never cur(); the on_damage Emit
  (Plan 02) is region-scoped via emitEntityEvent.

## Known Stubs

- **Mob knockback + sweep-over-mobs** — deferred (cited in handleMobAttack): the hit lands (damage +
  lastDamageSource + on_damage emit all fire), but the post-hit knockback impulse + sweep for a mob
  victim are a follow-on. No consumer reads a mob's post-hit velocity yet, and a cross-region mob's
  velocity write is a barrier concern. `sprintKb` is computed for the faithful trace, marked unused.
- **dieEntity** — still the Plan-04 seam from Plan 02 (pins health at 0, records the lethal source). The
  routed mob hits now drive it to 0; the full LivingEntity.die port (loot/XP/death broadcast/store
  removal) is Plan 29-04.

## Next Phase Readiness

- The damage keystone routing is complete: a player can take a mob to 0 HP, same- or cross-region.
- Plan 29-04 (death/loot/XP) has a real caller for `dieEntity` and can fill it without a call-site change.
- Phase 31 PanicGoal can read `entity.was_hurt` + `entity.last_damage_type` immediately.

## Self-Check: PASSED

- server/damage_region_test.go — FOUND
- server/attack_dispatch.go (handleMobAttack) — FOUND
- server/region_transfer.go (damageIntent + applyCrossRegionDamage) — FOUND
- server/region.go (pendingDamage) — FOUND
- server/region_coordinator.go (applyCrossRegionDamage call) — FOUND
- server/plugin_entity.go (last_damage_type) — FOUND
- commit 62be9fe3 (Task 0 test RED) — FOUND
- commit a740dac2 (Task 1) — FOUND
- commit 9e8a46eb (Task 2) — FOUND
- commit 133bcd73 (Task 3) — FOUND
- commit b468a97b (Task 4) — FOUND

---
*Phase: 29-damage-keystone-s2*
*Completed: 2026-06-29*
