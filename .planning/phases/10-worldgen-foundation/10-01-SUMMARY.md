---
phase: 10-worldgen-foundation
plan: 01
subsystem: world/levelgen
tags: [worldgen, rng, lcg, determinism, gen2]
requires: []
provides:
  - "LegacyRandomSource (java.util.Random LCG) — a second RandomSource impl"
  - "WorldgenRandom wrapper (SetDecorationSeed/SetFeatureSeed/SetLargeFeatureSeed/SetLargeFeatureWithSalt)"
  - "legacyPositionalFactory (At/FromHashOf/FromSeed) + javaStringHash"
affects:
  - "world/levelgen (adds a second RandomSource beside Xoroshiro)"
  - "future feature decoration pass (Phase 11+) + structures (Phase 14+) seed derivation"
tech-stack:
  added: []
  patterns:
    - "Jar-exact LCG port (LegacyRandomSource/BitRandomSource/WorldgenRandom), idiomatic Go, no GPL paste"
    - "Golden-vector tests pinned to canonical java.util.Random(0) output"
    - "var _ RandomSource compile-time interface assertions"
key-files:
  created:
    - ".planning/phases/10-worldgen-foundation/deferred-items.md"
  modified:
    - "world/levelgen/random.go (+216 lines, pure append below Xoroshiro)"
    - "world/levelgen/random_test.go (+136 lines, appended LCG tests)"
decisions:
  - "Used built-in Edit (not Serena MCP) — Serena MCP tools are not present in this agent's tool set; the append is surgical and identical in effect; go build/test are the source of truth per the plan's stale-LSP caveat"
  - "Ported all four WorldgenRandom seed helpers now (incl. the GEN3 large-feature pair) to avoid a second edit to random.go"
metrics:
  duration: "~12m"
  completed: 2026-06-25
  tasks: 2
  files: 3
---

# Phase 10 Plan 01: LegacyRandomSource (LCG) + WorldgenRandom Summary

JVM-exact `java.util.Random` LCG (`LegacyRandomSource`) plus the `WorldgenRandom`
seed-derivation wrapper, ported from the unobfuscated 26.2 jar and appended to
`world/levelgen/random.go` as a second drop-in `RandomSource` beside Xoroshiro,
pinned bit-for-bit to the canonical `java.util.Random(0)` golden sequence.

## What Was Built

### Task 1 — `LegacyRandomSource` + `WorldgenRandom` (commit `e98ddc8a`)
Appended below the Xoroshiro section of `world/levelgen/random.go` (216 insertions,
0 deletions — Xoroshiro types byte-identical):

- **Constants** `lcgMultiplier=0x5DEECE66D`, `lcgIncrement=0xB`, `lcgMask=(1<<48)-1`.
- **`LegacyRandomSource{seed uint64}`** with `NewLegacyRandomSource`, `SetSeed`
  (`(seed ^ MULT) & MASK`), unexported `next(bits)` (advance then `int32(state >> (48-bits))`).
- **RandomSource interface methods** (all jar-exact, algorithmically DISTINCT from Xoroshiro):
  `NextInt()`=`next(32)`; `NextIntN(bound)` = power-of-two fast path else `next(31)%bound`
  rejection loop (panics on `bound<=0`); `NextLong()`=`next(32)<<32 + next(32)` with a
  **signed** low term; `NextFloat()`=`next(24)*2^-24`; `NextDouble()`=`(next(26)<<27+next(27))*2^-53`
  (two draws); `ConsumeCount`; `Fork()`; `ForkPositional()`.
- **`legacyPositionalFactory{seed int64}`** — `At`(reuses `mthGetSeed`), `FromHashOf`
  (+ `javaStringHash` = `31*h+c`), `FromSeed`.
- **`WorldgenRandom{*LegacyRandomSource}`** — `SetDecorationSeed` (returns the seed),
  `SetFeatureSeed` (no draws), `SetLargeFeatureSeed` (GEN3), `SetLargeFeatureWithSalt` (GEN3, no draws).
- **`var _ RandomSource`** assertions for both `*LegacyRandomSource` and `*WorldgenRandom`;
  `var _ PositionalRandomFactory` for the factory.

### Task 2 — Golden-vector tests (commit `5a6210a0`)
Appended to `world/levelgen/random_test.go` (136 insertions); existing Xoroshiro tests untouched:

- `TestLegacyRandomNextLongGolden` — canonical `java.util.Random(0).nextLong()`:
  `-4962768465676381896, 4437113781045784766, -6688467811848818630, -8292973307042192125`.
- `TestLegacyRandomNextIntNGolden` — `nextInt(100)` = `60,48,29,47,15` (rejection loop) and
  `nextInt(16)` = `11,13,3,9,10` (pow2 fast path), plus 1000-draw in-range check.
- `TestLegacyRandomNextDoubleFloatGolden` — seed-0 `nextDouble`=`0.730967787376657`,
  `nextFloat`=`0.7309677`.
- `TestLegacyRandomNextIntNPanic` — `NextIntN(0)` panics.
- `TestWorldgenRandomDecorationSeed` / `TestWorldgenRandomFeatureSeed` — purity + bit-exactness.
- `TestLegacyImplementsRandomSource` — both types usable as `RandomSource`.

All golden longs/ints/double/float are the universally-known `java.util.Random(0)`
constants every JVM emits — proving the port is JVM-exact, not merely self-consistent.

## Verification

- `CGO_ENABLED=0 go build ./...` — clean (exit 0); runtime stays pure Go, zero new deps.
- `go vet github.com/imhinotori/sulfur/world/levelgen` — clean.
- `go test github.com/imhinotori/sulfur/world/levelgen -count=1` — **ok** (LCG + Xoroshiro all green).
- New tests run individually: all 7 PASS.
- `git show` numstat: `random.go` +216/-0 (pure append); zero file deletions.

## Deviations from Plan

### 1. [Tooling] Used built-in Edit instead of Serena MCP
- **Found during:** Task 1 setup.
- **Issue:** The prompt requested Serena tools for the code file, but no `mcp__serena__*`
  tools are present in this agent's tool set (only context7 + built-in Read/Edit/Write).
- **Fix:** Performed the surgical append with the built-in Edit tool — identical effect
  (APPEND below Xoroshiro, no edits to Xoroshiro types, verified via `git numstat` +216/-0).
  Trusted `go build`/`go test` as the source of truth per the plan's stale-LSP caveat.
- **Files modified:** none beyond the planned ones.

### 2. [Scope boundary] `go vet ./world/levelgen/...` (recursive) fails in the `surface` subpackage
- **Found during:** Task 2 verification.
- **Issue:** `world/levelgen/surface/heightmap_test.go:49: undefined: BuildWorldgenHeightmaps`
  — uncommitted in-progress work from the parallel-wave plan **10-02** in the working tree.
  NOT caused by 10-01 (which only touches `random.go`/`random_test.go`).
- **Action:** Out of scope — logged to `deferred-items.md`; did NOT fix. The `world/levelgen`
  package itself (the plan's scope) builds, vets, and tests clean.

## Authentication Gates

None.

## Deferred Issues

- `-race` not runnable locally (no GCC/cgo on this Windows host); runs in the project's
  Docker `-race` gate per 10-RESEARCH. The LCG is pure stateless arithmetic with no shared
  mutable state — race-clean by construction. Logged in `deferred-items.md`.

## TDD Gate Compliance

Both tasks are `tdd="true"`. The golden algorithm was first traced and verified against the
canonical `java.util.Random(0)` sequence (scratchpad tracer reproduced the universally-known
constants) before the values were pinned, so RED was meaningful. Per the plan's task split the
implementation lands in the `feat(10-01)` commit (`e98ddc8a`) and the golden tests in the
`test(10-01)` commit (`5a6210a0`); both feat and test gate commits are present in git log.

## Self-Check: PASSED

- `world/levelgen/random.go` — FOUND (contains `LegacyRandomSource`, `WorldgenRandom`).
- `world/levelgen/random_test.go` — FOUND (contains `TestLegacyRandom`).
- Commit `e98ddc8a` — FOUND.
- Commit `5a6210a0` — FOUND.
