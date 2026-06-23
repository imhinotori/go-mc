---
phase: 01-foundation-fork-codegen
plan: 03
subsystem: infra
tags: [protocol-776, mc-26.2, gen-diff, codegen-audit, go-only-build, protocol-constants, sulfur-cube]

# Dependency graph
requires:
  - phase: 01-01
    provides: forked go-mc @539b4a3a (#294-296), two-module layout, baseline-774/ snapshot, JDK-25 extractor
  - phase: 01-02
    provides: regenerated proto-776 Go trees (data/*, level/*) from the sha1-verified 26.2 jar
provides:
  - Reviewed 774→776 byte-layout enumeration (774-to-776-DIFF.md) — every delta jar-traceable from diff -ruN baseline-774 vs regenerated trees (GEN-04 deliverable, human-signed-off)
  - Framework self-identifies as protocol 776 / version 26.2 (corrected #295's leaking 774/1.21.11 constants)
  - End-to-end GEN-01 assertion: runtime builds/vets/tests with Go alone; no Java/tools in the runtime require graph
affects: [02 (handshake/login — consumes the 776 ProtocolVersion constant + packet IDs), all downstream server work]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Audit-by-diff: GEN-04 enumeration derived mechanically from diff/comm between the 774 baseline and regenerated 776 trees — never asserted from memory (defense against Pitfall 2 for a post-wiki version)"
    - "Set-delta over raw-diff: comm on extracted identifier sets isolates true additions/removals immune to iota/ID reindexing noise"

key-files:
  created:
    - ".planning/phases/01-foundation-fork-codegen/774-to-776-DIFF.md (GEN-04 enumeration, signed off)"
  modified:
    - "bot/mcbot.go (ProtocolVersion 774→776)"
    - "server/server.go (ProtocolName 1.21.11→26.2, ProtocolVersion 774→776)"
    - "net/rcon_test.go (server goroutine t.Fatal→t.Error+return; deterministic channel protocol — GEN-01 vet gate)"

key-decisions:
  - "Enumerated the diff via set-deltas (comm on identifier sets) rather than raw line diffs — the iota/ID reindexing from each insertion makes raw diffs huge while the semantic delta is small; set-deltas give the exact +N/-0 per tree"
  - "Resolved sulfur_cube_archetype + sulfur_cube_hot 'investigate' flags as EXPLAINED-not-regression: they are datapack/dynamic registries outside the codegen surface (confirmed absent via grep, not silently dropped)"
  - "Corrected ONLY the three #295 protocol/version constants; left the 1.21.x protocol-feature comments in bot/basic/* untouched (they annotate wire features, not version)"
  - "Fixed the pre-existing upstream net/rcon_test.go non-test-goroutine t.Fatal vet error (Rule 1) rather than declaring it out-of-scope — go vet ./... exit 0 is an explicit GEN-01 acceptance gate"

patterns-established:
  - "Audit-by-diff: every generated-data claim must trace to a line in diff -ruN baseline vs regenerated"
  - "Two-module isolation verified end-to-end: runtime require graph contains no /go-mc/tools module and no java reference"

requirements-completed: [GEN-04, GEN-01]

# Metrics
duration: ~10min
completed: 2026-06-23
---

# Phase 1 Plan 3: 774→776 Diff Enumeration + Go-Only Build Assertion Summary

**Mechanically enumerated the 1.21.11→26.2 (proto 774→776) generated-data delta from diff/comm between the Plan-01 baseline and the Plan-02 regenerated trees, corrected #295's leaking 774/1.21.11 protocol constants to 776/26.2, and asserted the runtime builds/vets/tests with Go alone — closing Phase 1.**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-06-23T14:53:00Z (approx)
- **Completed:** 2026-06-23T15:09:00Z (approx, after human sign-off)
- **Tasks:** 3 (2 autonomous + 1 blocking human-verify checkpoint)
- **Files modified:** 4 (774-to-776-DIFF.md created; bot/mcbot.go, server/server.go, net/rcon_test.go modified)

## Accomplishments
- **GEN-04 enumeration (774-to-776-DIFF.md):** a per-tree before/after/delta table plus 10 load-bearing deltas, each backed by an actual `diff -ruN` / `comm` line — not memory. Every generated file's header confirmed flipped `from 1.21.11/<report>.json` → `from 26.2/<report>.json`, proving full jar-derived regeneration.
- **The 776 delta, jar-traced:** +5 packets, +7 data components (incl. `sulfur_cube_content`), +1 entity (`sulfur_cube`), +30 blocks / +1568 block states, +32 items (incl. `music_disc_bounce`, `sulfur_cube_bucket`, `sulfur_cube_spawn_egg`), +5 attributes (incl. `bounciness`), +1 game event (`minecraft:bounce`), +130 sounds, +1 biome (`sulfur_caves`), +5 lang locales. Zero removals anywhere.
- **All "investigate" flags resolved:** packet-ID reshuffle = 775 carryover; entity/biome ID shifts = mechanical reindexing; `sulfur_cube_archetype` registry + `sulfur_cube_hot` damage type = datapack/dynamic registries correctly outside the codegen surface (confirmed absent via grep, not dropped).
- **Protocol constants corrected (Pitfall 3 / T-1-06):** `bot/mcbot.go` and `server/server.go` now self-identify as 776 / "26.2".
- **GEN-01 asserted end-to-end:** `go vet ./...`, `go build ./...`, `go test ./...` all exit 0 with Go alone; runtime `go.mod` has no `java` reference and the require graph excludes `/go-mc/tools`.
- **Human sign-off recorded** on the diff enumeration (blocking checkpoint — not self-approved).

## Task Commits

Each autonomous task was committed atomically:

1. **Task 1: Enumerate 774→776 diff (GEN-04) + correct leaking protocol constants** - `d4f251a2` (feat)
2. **Task 2 (GEN-01 vet gate fix): RCON test server goroutine must not call t.Fatal** - `5ae5ce15` (fix)

**Orchestrator framework fixes (landed on branch before finalize):** `1eca30ef` (fix: capture AcceptConfig error in connection accept loop — NET-04 silent-disconnect trap; remove single-iteration for/break in ExampleDialRCON — SA4004).

**Plan metadata:** see final docs commit below.

## Files Created/Modified
- `.planning/phases/01-foundation-fork-codegen/774-to-776-DIFF.md` - GEN-04 enumeration: per-tree before/after/delta table, 10 jar-traceable load-bearing deltas, investigate-flag resolutions, constant-correction record
- `bot/mcbot.go` - `ProtocolVersion = 776` (was 774)
- `server/server.go` - `ProtocolName = "26.2"` (was "1.21.11"), `ProtocolVersion = 776` (was 774)
- `net/rcon_test.go` - server goroutine `t.Fatal`→`t.Error`+`return` with a deterministic two-send channel protocol (GEN-01 `go vet` gate)

## Decisions Made
- **Set-delta over raw-diff for enumeration:** every const block is iota-based and every entity/biome insertion reindexes later numeric IDs, so raw diffs are large but semantically tiny. Extracting identifier sets and `comm`-ing them yields the exact +N/-0 per tree with zero reindex noise.
- **`sulfur_cube_archetype` / `sulfur_cube_hot` resolved as EXPLAINED, not regressions:** they are datapack/dynamic registries, not among the 96 built-in registries the extractor emits to `data/registryid/`. `grep -rln` confirms they are absent from the generated tree — correctly outside the codegen surface, not silently dropped.
- **Surgical constant edits only:** corrected the three #295 protocol/version constants; left `1.21.x` protocol-feature comments in `bot/basic/*` untouched (they annotate wire features by protocol number, not the version string).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] RCON test `server()` goroutine called `t.Fatal` (GEN-01 vet gate blocker)**
- **Found during:** Task 2 (GEN-01 `go vet ./...` assertion)
- **Issue:** `go vet ./...` exited 1 on `net/rcon_test.go:10` — `server()` runs via `go server(t,c)` yet called `t.Fatal` at 4 sites. `t.Fatal` from a non-test goroutine calls `runtime.Goexit` on the wrong goroutine and does not reliably fail the test. The diagnostic pre-dated this plan (present at HEAD~1, last touched by upstream commit `b1b57e06`), but `go vet ./... exit 0` is an explicit Task-2 acceptance criterion, so it was owned and fixed rather than deferred.
- **Fix:** converted the 4 `t.Fatal` calls in `server()` to `t.Error` + `return`; added a deterministic two-send channel protocol (always emits a "prepared" and a "finished" send on every path) so the main goroutine's two `<-c` receives never deadlock.
- **Files modified:** net/rcon_test.go
- **Verification:** `go vet ./...` exits 0; RCON `Test` still passes (`ok github.com/imhinotori/go-mc/net`).
- **Committed in:** `5ae5ce15`

---

**Total deviations:** 1 auto-fixed (1 bug).
**Impact on plan:** The fix was necessary to satisfy the GEN-01 `go vet ./... exit 0` acceptance gate. No scope creep — the change is confined to a single upstream test file's goroutine error-reporting.

## Issues Encountered
- A `go vet ./...` invocation initially appeared to exit 0 only because `$?` was capturing a `tail` pipe rather than vet; re-running with explicit exit-code capture exposed the real `vet=1` from the RCON diagnostic. Resolved by the Rule-1 fix above.
- The orchestrator independently fixed two adjacent framework diagnostics (`server.go` `AcceptConfig` error-drop / NET-04; `ExampleDialRCON` SA4004) and committed them as `1eca30ef` before finalize. Verified on-branch and green; accounted for in this summary.

## User Setup Required
None - no external service configuration required.

## Threat Model Adherence
- **T-1-03 (Tampering — Java/codegen leaking into runtime):** mitigated. `! grep -Eiq '\bjava\b' go.mod` clean; `go list -m all` excludes `/go-mc/tools`; `go build ./...` succeeds Go-only.
- **T-1-06 (Information Disclosure — wrong protocol constant):** mitigated. `bot/mcbot.go` + `server/server.go` corrected to 776 / "26.2"; reviewed in the signed-off diff. `grep -rn 'ProtocolVersion = 774\|"1\.21\.11"' bot/ server/` clean.

## Known Stubs
None. The diff enumeration is fully backed by generated data; the constant edits and the RCON test fix are concrete, not placeholders.

## Self-Check: PASSED

- `.planning/phases/01-foundation-fork-codegen/774-to-776-DIFF.md` — FOUND, contains "776".
- `bot/mcbot.go` `ProtocolVersion = 776`, `server/server.go` `ProtocolName = "26.2"` / `ProtocolVersion = 776` — FOUND.
- Commits `d4f251a2`, `5ae5ce15` — FOUND in git log. `1eca30ef` present on branch.
- `go vet ./...`, `go build ./...`, `go test ./...` exit 0; runtime require graph excludes tools module — verified.

## Next Phase Readiness
- **Phase 1 complete:** forked + pinned go-mc, retargeted JDK-25 codegen, committed 776 data, enumerated + signed-off 774→776 diff, Go-only runtime asserted. GEN-01/02/03/04 all satisfied.
- **Phase 2 (handshake/login) is unblocked:** the 776 `ProtocolVersion` constant and generated packet-ID tables are in place and correct; Phase 2 owns the handshake assertion behavior that consumes them.

---
*Phase: 01-foundation-fork-codegen*
*Completed: 2026-06-23*
