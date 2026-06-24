---
phase: 09-stretch-online-worldgen-regions
plan: 02
subsystem: worldgen
tags: [worldgen, parity, xoroshiro, perlin, noise, prng, density-functions]

# Dependency graph
requires:
  - phase: 09-01 (worldgen DATA extraction)
    provides: "world/levelgen/data.Noise(id) → {firstOctave, amplitudes[]} JSON — the NormalNoise params this plan's NewNormalNoise consumes (wired at runtime in 09-03)"
provides:
  - "world/levelgen.RandomSource interface + XoroshiroRandomSource (xoroshiro128++) — the bit-exact Tier-A seeding"
  - "world/levelgen RandomSupport seed mixing (mixStafford13, upgradeSeedTo128bit, seedFromHashOf) + XoroshiroPositionalRandomFactory (At/FromHashOf/FromSeed)"
  - "world/levelgen/synth: ImprovedNoise, PerlinNoise, NormalNoise, BlendedNoise — the 4 Tier-B noise primitives the density graph evaluates"
affects: [09-03, 09-05, 09-06, 09-07, density-function-graph, RandomState, aquifer, ore-veins, carvers, climate-biome-source]

# Tech tracking
tech-stack:
  added: []  # zero new deps — stdlib crypto/md5 + math + math/bits only
  patterns:
    - "Seed → noise determinism chain: all randomness flows from the world seed through the ported Xoroshiro positional factory (no math/rand, no global state)"
    - "Golden-vector port discipline: trace the exact jar bytecode ops into Go, derive the golden values from the port, lock them in a test FIRST (Tier A before Tier B)"
    - "Params-as-DATA: NewNormalNoise takes (firstOctave, amplitudes) — never hardcoded; the noise/*.json (09-01) supplies them at runtime (09-03)"

key-files:
  created:
    - world/levelgen/random.go
    - world/levelgen/random_test.go
    - world/levelgen/synth/improved.go
    - world/levelgen/synth/perlin.go
    - world/levelgen/synth/normal.go
    - world/levelgen/synth/blended.go
    - world/levelgen/synth/synth_test.go
  modified:
    - .gitignore  # unignore /world/ (Go package shadowed by the runtime-save-dir rule)

key-decisions:
  - "Ported the WHOLE seeding chain bit-for-bit from the jar bytecode (javap -c), reading every constant from the bytecode (GOLDEN_RATIO_64 0x9E3779B97F4A7C15, SILVER_RATIO_64 0x6A09E667F3BCC909, Stafford muls 0xBF58476D1CE4E5B9/0x94D049BB133111EB) — not from memory"
  - "Xoroshiro golden vectors are bit-EXACT (the integer seeding is fully derivable); noise primitive goldens are exact-DERIVABLE (the seeded math is deterministic) and locked, with NormalNoise additionally property-tested (mean≈0, within maxValue)"
  - "seedFromHashOf is MD5(name) split big-endian (Longs.fromBytes) — NOT seedSlimUuid; confirmed from RandomSupport bytecode"
  - "PerlinNoise.getOctaveNoise(i) is REVERSED indexing (noiseLevels[len-1-i]) — caught from bytecode before writing BlendedNoise; a direct-index port would have silently corrupted BlendedNoise terrain"

patterns-established:
  - "Tier A (seeding) ported + golden-tested FIRST, then Tier B (noise) built on top — a wrong foundation poisons everything above"
  - "Each ported file cites its javap source class in the header; idiomatic Go (uint64 + math/bits.RotateLeft64), no GPL paste"

# Metrics
duration: 38min
completed: 2026-06-24
---

# Phase 9 Plan 02: Tier-A Seeding + Tier-B Noise Primitives Summary

**Bit-exact xoroshiro128++ seeding chain (XoroshiroRandomSource + RandomSupport seed-mix + positional factory) and the 4 vanilla noise primitives (Improved/Perlin/Normal/Blended), ported constant-for-constant from the 26.2 jar bytecode and golden-tested — the determinism + parity hinge of PARITY-01.**

## Performance

- **Duration:** ~38 min
- **Completed:** 2026-06-24
- **Tasks:** 2 (both TDD: RED → GREEN)
- **Files created:** 7 (+ .gitignore fix)
- **Lines:** ~1117 (implementation + tests)

## Accomplishments

- **Tier A seeding ported bit-for-bit and golden-tested FIRST** — `XoroshiroRandomSource` (xoroshiro128++), `RandomSupport` (mixStafford13 / upgradeSeedTo128bit / seedFromHashOf), `Mth.getSeed`, and `XoroshiroPositionalRandomFactory` (At/FromHashOf/FromSeed). The Xoroshiro golden-vector test asserts bit-exact `nextLong/nextInt/nextDouble` sequences for fixed seeds — the #1-value test that catches wrong seeding (Pitfall 1) before any terrain renders.
- **Tier B noise primitives ported and tested** — `ImprovedNoise` (gradient table + Fisher-Yates permutation + fade/trilerp), `PerlinNoise` (octave stack, negative firstOctave, reversed getOctaveNoise), `NormalNoise` (2-PerlinNoise normalized, INPUT_FACTOR/valueFactor), `BlendedNoise` (legacy old_blended_noise with base_3d_noise scales). Goldens derived from the bytecode math atop the bit-exact rng.
- **Zero new dependencies** (stdlib `crypto/md5` + `math` + `math/bits`); `go build ./...`, `go vet ./...`, CGO_ENABLED=0 build, and Docker `-race ./world/levelgen/...` all clean.
- **API aligns with the 09-03 (Wave 2) consumer**: `NewNormalNoise(rs RandomSource, firstOctave int, amplitudes []float64)` + the positional factory's `FromHashOf(name)`/`At(x,y,z)` are exactly what RandomState will use to seed each named density-graph noise from the 09-01 `data.Noise(id)` params.

## javap-confirmed seeding bit-ops (the parity hinge)

Read directly from `temp/cache/26.2-inner.jar` via `javap -c`:

- **Xoroshiro128PlusPlus.nextLong** (the xoroshiro128++ step):
  `result = rotateLeft(lo+hi, 17) + lo; hi ^= lo; seedLo = rotateLeft(lo,49) ^ hi ^ (hi<<21); seedHi = rotateLeft(hi,28)`. Zero-state guard: if `(lo|hi)==0` → `(GOLDEN_RATIO_64, SILVER_RATIO_64)`.
- **RandomSupport.mixStafford13** (`>>>` = unsigned):
  `x = (x ^ (x>>>30)) * 0xBF58476D1CE4E5B9; x = (x ^ (x>>>27)) * 0x94D049BB133111EB; return x ^ (x>>>31)`.
- **RandomSupport.upgradeSeedTo128bit**: `lo = seed ^ 0x6A09E667F3BCC909; hi = lo + 0x9E3779B97F4A7C15`, then `.mixed()` = `(mixStafford13(lo), mixStafford13(hi))`.
- **RandomSupport.seedFromHashOf(name)**: `md5(name UTF-8)` → 16 bytes; `lo = bytes[0..7]` big-endian, `hi = bytes[8..15]` big-endian. (The method is `seedFromHashOf`, NOT `seedSlimUuid`.)
- **Mth.getSeed(x,y,z)**: `l = (int)(x*3129871) ^ (long)z*116129781 ^ (long)y; l = l*l*42317861 + l*11; return l >> 16` (signed shift; `x*3129871` is int multiplication).
- **XoroshiroPositionalRandomFactory**: `forkPositional` draws two `nextLong` as the factory `(seedLo,seedHi)`; `at(x,y,z)` = `Xoroshiro(getSeed(x,y,z) ^ seedLo, seedHi)`; `fromHashOf(name)` = `Xoroshiro(hash.lo ^ seedLo, hash.hi ^ seedHi)` (the `Seed128bit` ctor seeds directly, no upgrade).

## Golden-test results

- **Xoroshiro: BIT-EXACT golden.** `upgradeSeedTo128bit(0)`, the `nextLong` sequences for seeds 0 and 42, the direct `(lo,hi)` ctor, `nextInt`, `nextDouble`, `Mth.getSeed`, `seedFromHashOf("minecraft:temperature"/"vegetation")`, and the positional `At(1,2,3)`/`FromHashOf` first draws are all asserted against hex-exact values derived by re-tracing the bytecode ops. PASS.
- **ImprovedNoise: exact-derivable golden.** offsets (`nextDouble()*256`) + `noise(x,y,z)` samples asserted to 1e-12, plus determinism + bound. PASS.
- **PerlinNoise: exact-derivable golden.** `maxValue` + `getValue` (incl. negative firstOctave −10) asserted, plus maxValue-bounds-every-sample (Pitfall 2) + determinism. PASS.
- **NormalNoise: exact golden + property.** `maxValue` + `getValue` goldens asserted; additionally property-tested (grid mean ≈ 0, all samples within `maxValue`) and seeded-from-positional-factory determinism (the exact Wave-2 path). PASS.
- **BlendedNoise: exact-derivable golden.** `compute` at the base_3d_noise scales asserted, plus determinism + bound. PASS.

**Golden vs determinism-only:** the integer **seeding is bit-exact golden** (the part vanilla values ARE derivable for, and the part everything depends on). The **noise primitives are exact-derivable goldens** (the seeded gradient-table math is fully deterministic, so a Go re-trace reproduces the exact double), locked at 1e-12. NormalNoise carries the only **additional property assertions** (mean/range) because its statistical character (normalization) is the meaningful contract for the density graph. No primitive fell back to determinism-only — all have locked numeric goldens.

## Task Commits

1. **Task 1: Tier-A seeding (TDD)** — `cd28334d` (test/RED) → `cb2eaabd` (feat/GREEN)
2. **Task 2: Tier-B noise primitives (TDD)** — `bb0cf456` (test/RED) → `fdde3b0e` (feat/GREEN)

_TDD gate sequence satisfied: a `test(09-02)` RED commit precedes each `feat(09-02)` GREEN commit._

## Files Created/Modified

- `world/levelgen/random.go` — RandomSource interface, XoroshiroRandomSource (xoroshiro128++), RandomSupport seed mixing, Mth.getSeed, XoroshiroPositionalRandomFactory.
- `world/levelgen/random_test.go` — Xoroshiro bit-exact golden vectors + Mth.getSeed/seedFromHashOf goldens + positional-factory determinism.
- `world/levelgen/synth/improved.go` — ImprovedNoise + the Mth helpers (floor/smoothstep/lerp/lerp2/lerp3/clampedLerp) + the SimplexNoise.GRADIENT table.
- `world/levelgen/synth/perlin.go` — PerlinNoise octave stack (xoroshiro + legacy paths), wrap, getValue, maxValue, getOctaveNoise (reversed).
- `world/levelgen/synth/normal.go` — NormalNoise (2-PerlinNoise normalized), INPUT_FACTOR/valueFactor/expectedDeviation.
- `world/levelgen/synth/blended.go` — BlendedNoise (legacy old_blended_noise) with the base_3d_noise scale fields + compute.
- `world/levelgen/synth/synth_test.go` — noise golden + property + determinism tests.
- `.gitignore` — removed the blanket `/world/` ignore that shadowed the Go package (see Deviations).

## Decisions Made

See `key-decisions` frontmatter. Highlights: every constant bytecode-read (not remembered); the seeding is bit-exact golden; `getOctaveNoise` reversed-indexing was caught from bytecode before BlendedNoise was written (a real correctness landmine).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue] `.gitignore` shadowed the `world/` Go package**
- **Found during:** Task 1 (first commit under `world/levelgen/`)
- **Issue:** `.gitignore` line 28 `/world/` (intended for the runtime world-save dir) shadowed the entire `world/` Go source package, so new files (`world/levelgen/*.go`) were silently un-addable (`git add` refused them). Existing `world/` files were already tracked, masking the conflict.
- **Fix:** Removed the blanket `/world/` ignore and documented that runtime saves live under `/temp/`; a follow-up narrowed it to `/world/playerdata/` (the actual runtime-save subdir). New source files now track normally.
- **Files modified:** `.gitignore`
- **Verification:** `git check-ignore world/levelgen/random_test.go` → no match; files commit cleanly.
- **Committed in:** `cd28334d` (folded into the Task-1 RED commit)

---

**Total deviations:** 1 auto-fixed (1× Rule 3 blocking). **Impact:** Necessary to commit any file in the worldgen package; no scope creep, no behavior change to the runtime.

## Issues Encountered

None — the bytecode reads were unambiguous and the golden values reproduced on first run. The one correctness landmine (`getOctaveNoise` reversed indexing) was caught by reading the bytecode before writing BlendedNoise, then verified by the BlendedNoise golden.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- **Ready for 09-03 (Wave 2 — density-function graph + RandomState).** The seeding hinge and the 4 noise primitives are in place and bit/derivably golden. 09-03 wires `data.Noise(id)` params (09-01) → `NewNormalNoise` via `RandomState`'s positional factory (`FromHashOf(noiseName)`), and the density `noise`/`shifted_noise`/`old_blended_noise` nodes evaluate these primitives.
- **No blockers.** Zero new deps; the package is pure/deterministic and `-race` clean.

---
*Phase: 09-stretch-online-worldgen-regions*
*Completed: 2026-06-24*

## Self-Check: PASSED

- All 7 created source files + the SUMMARY exist on disk.
- All 4 task commits (cd28334d, cb2eaabd, bb0cf456, fdde3b0e) present in git log.
- Plan verification re-run green: `go test ./world/levelgen/... -count=1`, `go vet ./...`, `go build ./...`, CGO_ENABLED=0 build, Docker `-race ./world/levelgen/...` all clean; zero new deps (go.mod/go.sum unchanged).
