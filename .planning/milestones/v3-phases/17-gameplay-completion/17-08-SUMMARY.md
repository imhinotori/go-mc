---
phase: 17
plan: 17-08
subsystem: gameplay / environmental-damage
tags: [fall-damage, water, bugfix, port-faithful, protocol-776]
requires:
  - 17-01 (tickPlayer fall-damage fields: fallDistance/wasOnGround/lastY; tickFallDamage call site)
  - 17-02 (fluid_physics.go: playerInWater AABB water-intersection check)
  - combat.go (applyDamage -> SetHealth/die flow)
provides:
  - vanilla-faithful water negation of fall damage (the headline Phase 17 visual-gate fix)
affects:
  - server/fall_damage.go
  - server/fall_damage_test.go
tech-stack:
  added: []
  patterns:
    - reuse 17-02 playerInWater for BOTH the accumulation guard and the water reset (no re-impl)
key-files:
  created: []
  modified:
    - server/fall_damage.go
    - server/fall_damage_test.go
decisions:
  - "Ported the FULL vanilla two-guard water model, not just the checkFallDamage accumulation guard: the second guard (Entity.updateFluidInteraction resetFallDistance when in water) is what zeroes fall distance built up BEFORE entering water, and is required for fall-into-water to deal 0 damage."
metrics:
  duration: ~1 task
  completed: 2026-06-25
---

# Phase 17 Plan 17-08: Fall-Damage Water Guard Fix Summary

Fixed the confirmed "fall damage is not at all like vanilla" bug: Sulfur omitted the water guard,
so a player falling into water still accumulated fall distance and took full landing damage.
Ported the complete vanilla two-guard water-negation model into `server/fall_damage.go`, reusing
17-02's `playerInWater` AABB check. Fall into water now deals 0 damage, matching vanilla.

## Diagnosis (verified against the jar)

`javap -c -p -classpath temp/cache/26.2-inner.jar` on `net.minecraft.world.entity.Entity` and
`LivingEntity` this session confirmed:

1. **`Entity.checkFallDamage`** — accumulation is guarded by `!isInWater()`:
   `if (!isInWater() && deltaY < 0) fallDistance -= (float) deltaY;`
   then on `onGround`: `if (fallDistance > 0) fallOn -> causeFallDamage; resetFallDistance();`.

2. **`Entity.updateFluidInteraction`** (run every tick via `baseTick`, bytecode offset that calls
   `resetFallDistance` at the disassembly's line ~4208) — the SECOND, decisive guard:
   `boolean inWater = fluidInteraction.isInFluid(WATER); if (inWater) { resetFallDistance(); … }`.
   This zeroes any fall distance accumulated BEFORE the entity entered the water, every tick it is
   in water — and it runs independently of the landing edge. In vanilla a player in water is never
   `onGround` over solid ground, so by the time `checkFallDamage`'s `onGround` branch is reached,
   `fallDistance` is already 0 and no damage is dealt.

3. **`LivingEntity.causeFallDamage`** — confirmed the damage routes through `hurt(source, dmg)`
   with `dmg = calculateFallDamage(d, mul) = Mth.floor((d + 1e-6 - 3.0) * mul)`. Sulfur's
   `applyDamage` is the correct health-path analogue. (Sulfur's existing formula
   `floor(fallDistance + 1e-6 - 3.0)` was already correct; not changed.)

**Conclusion:** the formula, landing-edge detection, accumulation-of-peak-descent, reset, and
bookkeeping were all already correct. The ONLY bug was the missing water guard (both halves).

## Re-verification of the broader "not at all like vanilla" claim

Per the plan, I traced the surrounding mechanics for spurious resets / stale state — all sound,
no additional bug found:

- **(a) Peak-descent accumulation:** `if (!onGround && !inWater) fallDistance += max(0, lastY-y)`
  correctly sums per-tick down-deltas while airborne. No spurious mid-fall reset exists; the only
  resets are the landing edge and (now) the water guard. Upward steps clamp to 0 (matches the jar's
  `deltaY < 0` guard). Verified by the existing `TestFallDamageAccumulates` (12-block descent).
- **(b) Landing edge fires exactly once:** `wasOnGround`/`lastY` are written at the END of every
  tick (including for nil/dead players, so a respawn does not inherit a stale edge). The
  `!wasOnGround && onGround` edge is therefore single-shot. Correct.
- **(c) `onGround` source:** `server/subtick.go` sets `p.onGround = flags & movementFlagOnGround`
  on every movement packet variant (Pos / PosRot / Rot / StatusOnly). No flicker; driven directly
  by the client's authoritative onGround bit (jar-verified Byte flags, the 1.21.3+ shift). Correct.

So "not at all" reduced entirely to the water symptom — the most visible one (any pool dive dealt
full damage). No tick.go / subtick.go field was missing; no STOP/report was required.

## The Fix (server/fall_damage.go only)

Reused `t.playerInWater(p)` (17-02, fluid_physics.go) — NOT reimplemented — sampled once per
player per tick into `inWater`, then applied both vanilla guards:

```
inWater := t.playerInWater(p)

// (0) updateFluidInteraction water reset: zero accumulated fall distance, no damage.
if inWater { p.fallDistance = 0 }

// (1) accumulation guard: only when airborne AND not in water.
if !p.onGround && !inWater { p.fallDistance += math.Max(0, p.lastY-p.y) }

// (2) landing edge unchanged: with (0), a submerged "landing" sees fallDistance 0 => dmg <= 0.
```

`(0)` runs before the landing branch so a player flagged `onGround` on a submerged floor the same
tick still takes 0 damage. Documented deferrals (slow-falling potion, per-block `fallOn`
multiplier like hay bales) remain deferrals — the water guard was the bug.

## Tests (server/fall_damage_test.go)

Added a `waterFallLoop` harness (reuses 17-02's `newFluidLoop` + `setWater`, plus `combatPlayer`
for client/health), and two new tests:

- **`TestFallIntoWaterNoDamage`** (headline): fall 13 blocks of air into a water column, then
  "land" on the submerged floor. Asserts fallDistance accumulates while dry (13), resets to 0 on
  entering water, deals 0 damage on the submerged landing, and emits 0 `SetHealth` packets. Without
  the fix this fall dealt `floor(13-3)=10` damage.
- **`TestDescentInWaterNoAccumulation`**: a player sinking entirely within water never accumulates
  fall distance and takes 0 damage on a submerged landing (the `!isInWater()` accumulation guard).

All four pre-existing fall tests (`TestFallDamageAccumulates`, `TestFallDamageOnLanding`,
`TestSmallFallNoDamage`, `TestStayGroundedNoDamage`) still pass unchanged — dry-land fall damage is
untouched.

## Before / After

| Scenario | Before (buggy) | After (vanilla-faithful) |
|----------|----------------|--------------------------|
| Fall 13 blocks onto land | 10 dmg | 10 dmg (unchanged) |
| Fall 13 blocks into water | **10 dmg** (wrong) | **0 dmg** (correct) |
| Sink/descend within water | accumulates + damages on floor | 0 accumulation, 0 dmg |
| Walk into water carrying built-up fall distance | full damage on next floor | distance zeroed on contact |

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go test ./server/...` — all pass (full server suite, including the 2 new + 4
  existing fall tests).
- `CGO_ENABLED=0 go vet ./server/...` — exit 0.
- `-race` not run: it requires cgo, and the CGO_ENABLED=0-clean constraint precludes it here;
  the change is pure tick-goroutine state (TICK-05, no locking), no new shared state introduced.

## Deviations from Plan

None. Scope held to `server/fall_damage.go` + its test. `fluid_physics.go`, `tick.go`,
`subtick.go`, `gameplay_tick.go`, `keepalive.go` were NOT modified (read-only inspection of
subtick.go to verify the `onGround` source). No missing field was discovered, so no STOP/report.

## Self-Check: PASSED

- `server/fall_damage.go` — FOUND
- `server/fall_damage_test.go` — FOUND
- `.planning/phases/17-gameplay-completion/17-08-SUMMARY.md` — FOUND
- Build (CGO_ENABLED=0) exit 0; new + existing fall/water tests pass; vet clean.
