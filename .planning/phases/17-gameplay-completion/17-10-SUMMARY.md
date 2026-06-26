---
phase: 17
plan: 17-10
subsystem: gameplay / environmental-damage
tags: [fall-damage, port-faithful, 1to1, protocol-776, refactor]
requires:
  - 17-01 (tickPlayer fall-damage fields: fallDistance/wasOnGround/lastY; tickFallDamage call site)
  - 17-02 (fluid_physics.go: playerInWater AABB water-intersection check)
  - 17-08 (vanilla two-guard water negation; first jar-verified water fix)
  - combat.go (applyDamage -> SetHealth/die flow)
provides:
  - literal 1:1 structure-preserving port of vanilla 26.2 fall damage (method-mirrored call chain)
  - restored numeric fidelity: the (float) cast, the explicit attribute multiplier product, the FALL_DAMAGE_IMMUNE guard
affects:
  - server/fall_damage.go
  - server/fall_damage_test.go
tech-stack:
  added: []
  patterns:
    - mirror each vanilla method with a dedicated Go function (calculateFallPower / calculateFallDamage / checkFallDamage / causeFallDamage / resetFallDistance / mthFloor) so the port is auditable line-for-line against the bytecode
    - encode attribute bases as named constants (safeFallDistanceAttr=3.0, fallDamageMultiplierAttr=1.0) kept as EXPLICIT factors, so a future attribute system swaps them to getAttributeValue(...) with no formula change
key-files:
  created: []
  modified:
    - server/fall_damage.go
    - server/fall_damage_test.go
decisions:
  - "Re-ported fall damage as a LITERAL 1:1 copy of vanilla Java 26.2 instead of the previous paraphrase: the prior code collapsed the whole call chain into tickFallDamage and baked the multipliers away. Game logic must be a copy, so the call chain is now split into mirrored helpers matching the jar method-for-method."
  - "Kept power * damageMultiplier * fallDamageMultiplierAttr as an explicit three-factor product (matching the two dmul ops in calculateFallDamage bytecode) rather than simplifying to power, so the formula is the literal vanilla product even though both attribute factors are currently 1.0/3.0 constants."
  - "Modeled the FALL_DAMAGE_IMMUNE tag check as a constant-false guard (players are not in the tag) rather than omitting it, so calculateFallDamage's structure matches the jar and a future per-entity tag lookup slots in unchanged."
  - "Ported the (float) narrowing cast verbatim as float64(float32(deltaY)) — the d2f/f2d bytecode pair — because it is part of vanilla's numeric behavior; a new test pins that the cast is present."
metrics:
  duration: ~1 task
  completed: 2026-06-25
---

# Phase 17 Plan 17-10: Fall-Damage Literal 1:1 Re-Port Summary

Re-ported Sulfur's fall damage to be a **literal, structure-preserving 1:1 copy** of vanilla
Java 26.2 (protocol 776). The 17-08 code was functionally close but PARAPHRASED the algorithm: it
collapsed the entire `checkFallDamage -> fallOn -> causeFallDamage -> calculateFallDamage ->
calculateFallPower` call chain into one inlined block inside `tickFallDamage`, and baked the
attribute multipliers and the `FALL_DAMAGE_IMMUNE` guard away. The mandate was explicit: game logic
must be a copy, not a paraphrase. `server/fall_damage.go` now mirrors the jar method-for-method.

Only `server/fall_damage.go` and `server/fall_damage_test.go` were touched. `playerInWater`
(fluid_physics.go), `applyDamage` (combat.go), and tick.go were reused unchanged.

## Re-verification against the jar (this session)

Every method was re-decompiled with `javap -c -p -classpath temp/cache/26.2-inner.jar` and the
bytecode quoted in the source comments. Confirmed numeric ops:

- **`Entity.checkFallDamage(double, boolean, BlockState, BlockPos)`** — `isInWater ifne` /
  `dload_1 dconst_0 dcmpg ifge` guard; accumulation is `getfield fallDistance | dload_1 d2f f2d
  dsub | putfield fallDistance` — i.e. `fallDistance -= (double)(float) deltaY`. The **`d2f` then
  `f2d`** pair is the (float) narrowing-then-widening cast. On `iload_3` (onGround): `fallDistance >
  0` (`dcmpl ifle`) -> `Block.fallOn(...)` -> `causeFallDamage(fallDistance, 1.0F, FALL)`, then
  `resetFallDistance()`.
- **`LivingEntity.causeFallDamage(double, float, DamageSource)`** — the impulse branch
  (`isIgnoringFallDamageFromCurrentImpulse ifeq 58`) is skipped for a normal landing; the ELSE path
  (`58: dload_1 dstore_5`) passes `d` through unchanged. Then `calculateFallDamage(d, mul) ->
  istore_8`; `iload_8 ifle 117` -> `hurt(src, (float) i)` (`i2f`).
- **`LivingEntity.calculateFallDamage(double, float)`** — `EntityTypeTags.FALL_DAMAGE_IMMUNE is(...)
  ifeq 12` -> `iconst_0 ireturn` (return 0 if immune); else `calculateFallPower(d)` then
  `fload_3 f2d dmul` (× damageMultiplier) `dmul` (× FALL_DAMAGE_MULTIPLIER attr) `Mth.floor`. Two
  `dmul`s = the explicit three-factor product.
- **`LivingEntity.calculateFallPower(double)`** — `dload_1 ldc2_w 1.0E-6 dadd | getAttributeValue(
  SAFE_FALL_DISTANCE) dsub | dreturn` -> `(d + 1.0E-6) - safeFallDistance`.
- **`Mth.floor(double)`** — `Math.floor d2i ireturn` -> `int(math.Floor(d))`.
- **`Entity.resetFallDistance()`** — `dconst_0 putfield fallDistance` -> `fallDistance = 0`.
- **Attribute bases (`Attributes.<clinit>`)** — `SAFE_FALL_DISTANCE = RangedAttribute(3.0,
  -1024.0, 1024.0)` -> base **3.0**; `FALL_DAMAGE_MULTIPLIER = RangedAttribute(1.0, 0.0, 100.0)`
  -> base **1.0**.

## Method-mirror structure (what changed)

The single inlined block became separate functions, one per vanilla method:

| Go function | Vanilla method mirrored |
| --- | --- |
| `calculateFallPower(d)` | `LivingEntity.calculateFallPower` |
| `calculateFallDamage(d, mul)` | `LivingEntity.calculateFallDamage` (incl. FALL_DAMAGE_IMMUNE guard) |
| `(t) causeFallDamage(p, d, mul)` | `LivingEntity.causeFallDamage` (else/non-impulse path) |
| `(t) checkFallDamage(p, deltaY, onGround, inWater)` | `Entity.checkFallDamage` |
| `(p) resetFallDistance()` | `Entity.resetFallDistance` |
| `mthFloor(d)` | `Mth.floor` |
| `(t) tickFallDamage()` | per-player driver: computes deltaY = p.y - p.lastY and calls the chain |

## Previously-paraphrased parts now restored

- **The (float) cast** — was `math.Max(0, lastY-y)` accumulation; now `fallDistance -=
  float64(float32(deltaY))`, the exact `d2f/f2d` behavior, with `deltaY = p.y - p.lastY`.
- **The attribute multiplier product** — was `floor(fallDistance + eps - 3.0)`; now
  `mthFloor(power * damageMultiplier * fallDamageMultiplierAttr)`, the literal two-`dmul` product
  with `damageMultiplier` and the FALL_DAMAGE_MULTIPLIER attribute as explicit factors.
- **The FALL_DAMAGE_IMMUNE guard** — was absent; now a cited constant-false branch matching the
  `is(...) ifeq` structure.
- **The separated call chain** — was one inlined blob; now the mirrored helpers above.

The 17-08 two-guard water negation is preserved verbatim (accumulation guard + per-tick
`resetFallDistance()` when in water), still reusing 17-02's `playerInWater`.

## deltaY stand-in

Vanilla's `deltaY` is `deltaMovement.y` (vertical velocity). Sulfur is position-authoritative (no
velocity integrator), so `deltaY = p.y - p.lastY` (this tick's vertical position change) is the
faithful stand-in: a fall makes `deltaY < 0`, and `fallDistance -= (float)deltaY` adds the positive
drop, exactly as vanilla's velocity-based deltaY would. `tickFallDamage` advances `p.lastY` at the
end of each tick so the next tick's delta is correct.

## Tests

Kept all existing fall/water tests and added two pinning the restored fidelity:

- `TestCalculateFallDamageMatchesVanillaFormula` — asserts `calculateFallDamage(d, mul)` equals the
  independent oracle `floor((d + 1e-6 - 3.0) * mul * 1.0)` across whole and fractional drops and a
  0.5 multiplier, guarding against re-collapse of the product.
- `TestCheckFallDamageFloatCast` — drives a 0.1 drop and asserts `fallDistance ==
  float64(float32(0.1))` and `!= 0.1`, proving the (float) narrowing cast is present.

`TestCheckFallDamageFloatCast` calls `defer loop.Close()` to release the async ants pools created
by `NewTickLoop`, preventing a worker leak that otherwise perturbed `TestTickAIDrivesMobs`'s
timing-fragile async-pathfinding assertion under full-suite load.

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go vet ./server/` — clean.
- `CGO_ENABLED=0 go test ./server/...` — all pass; full server suite green across 4 consecutive
  runs (the async-pathfinding flake from the leaked pool is resolved by the `Close()`).
- Fall/water subset (13 tests) — all pass.

## Self-Check: PASSED

- `server/fall_damage.go` — FOUND (mirrored helpers present).
- `server/fall_damage_test.go` — FOUND (two new tests present).
- Build CGO_ENABLED=0 exit 0; `go test ./server/...` green.
