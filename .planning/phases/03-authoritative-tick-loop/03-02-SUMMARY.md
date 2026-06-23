---
phase: 03-authoritative-tick-loop
plan: 02
subsystem: tick-loop
tags: [subtick, input-buffer, keep-alive, tick-loop, go, single-owner, ddos-bound]

# Dependency graph
requires:
  - phase: 03-01
    provides: "TickLoop + ordered phase pipeline + resolveSubtickInputs no-op slot + dispatch switch + injectable Clock + fakeClock + tickPlayer collection"
  - phase: 02
    provides: "Intent{Client,Packet} network->tick seam; KeepAlive component (server/keepalive.go)"
provides:
  - "SubtickInput{At,Packet}: server-stamped (arrival) µs-timestamped input record"
  - "subtickBuffer: bounded (subtickCap=256) drop-oldest per-player input buffer, chronological drain"
  - "applyInput: documented Phase-3 stub (records lastInputAt) with applyInputHook test seam"
  - "resolveSubtickInputs: filled to drain each player chronologically through applyInput"
  - "dispatch wiring: movement/use/attack -> server-stamped subtick append; ClientTickEnd -> boundary marker; clientIndex O(1) routing"
  - "TICK-04 proof: keep-alive independence test (stalled tick fires no timeout)"
affects: [phase-6-entities, phase-6-movement, 03-03-keepalive-wiring, phase-8-async]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Bounded drop-oldest per-player buffer (DoS bound, mirrors Phase-2 outbound discipline)"
    - "Server-stamped arrival time as the ordering key (never trust client timestamps)"
    - "Single-owner tick-goroutine state (no mutex/xsync); test-only hooks gated by nil-check"
    - "Independence proof via a provably-stalled sibling goroutine + accelerated own-timer"

key-files:
  created:
    - server/subtick.go
    - server/subtick_test.go
    - server/keepalive_independent_test.go
  modified:
    - server/tick.go
    - server/tick_phases.go

key-decisions:
  - "subtickCap=256: generous above any legitimate per-tick input count; fixed allocation; hard DoS bound"
  - "drain() sorts defensively (SliceStable by At) so a merge/re-stamp still resolves chronologically even though single-channel arrival is already ordered"
  - "applyInput is a STUB (records lastInputAt) + applyInputHook test seam — no physics until Phase 6"
  - "dispatch routes via clientIndex (plain map, tick-owned); nil/unknown client = cheap no-op (T-3-02)"
  - "Keep-alive test accelerates the component's OWN listTimer (no keepalive.go edit) to stay fast/deterministic; never waits real 15s/30s"

patterns-established:
  - "Per-player bounded input buffer with drop-oldest overflow (T-3-01 DoS bound)"
  - "Arrival-timestamp authority: server stamps At=clock.Now(); client time never trusted to reorder (T-3-07)"
  - "Independence test: run component on own goroutine + own timers, stall a sibling goroutine, assert progress"

requirements-completed: [TICK-03, TICK-04]

# Metrics
duration: 18min
completed: 2026-06-23
---

# Phase 3 Plan 02: Minimal CS2-style Subtick Seam + Keep-Alive Independence Summary

**Per-player bounded µs-timestamped input buffer drained in chronological order inside the tick through a stub applyInput (TICK-03), plus a test proving the fork's KeepAlive keeps pinging while the tick is stalled (TICK-04) — both with zero new dependencies and zero physics.**

## Performance

- **Duration:** ~18 min
- **Tasks:** 2 (both TDD)
- **Files modified:** 5 (3 created, 2 edited)

## Accomplishments
- **TICK-03 minimal seam:** `SubtickInput{At,Packet}` + bounded `subtickBuffer` (cap 256, drop-oldest), drained chronologically (stable sort by `At`) each tick through a documented stub `applyInput`. The CS2-style separation of input RESOLUTION (subtick-precise) from broadcast RATE (vanilla 20/s flush) is proven — no physics (deferred to Phase 6).
- **DoS bound (T-3-01):** an input flood of `4*cap` never grows the buffer past `subtickCap` at any point; only the newest `cap` inputs survive, still in chronological order. A flood degrades only that player, never stalls the tick.
- **Timestamp authority (T-3-07):** dispatch stamps `At = clock.Now()` (server arrival) for movement/use/attack packets; client-supplied time is never trusted to reorder.
- **ClientTickEnd handling:** recorded as a boundary marker (`sawTickEnd`) only — payload not decoded (deferred to Phase 6).
- **TICK-04 independence proof:** `TestKeepAliveIndependentOfTick` runs `KeepAlive.Run(ctx)` on its own goroutine, provably stalls a sibling "tick" goroutine, and asserts (a) a ping still fires off the tick and (b) no timeout-disconnect fires solely due to the stall. `keepalive.go` reused verbatim.

## Task Commits

1. **Task 1: Subtick buffer + chronological drain + stub applyInput (TICK-03)** - `07011e53` (feat)
2. **Task 2: Keep-alive independence proof (TICK-04)** - `88a71c95` (test)

_TDD: each task's test + implementation committed cohesively (the buffer API and its tests are a single seam; the keepalive task is test-only against an unmodified component)._

## Files Created/Modified
- `server/subtick.go` (created) - `SubtickInput`, bounded `subtickBuffer` (append/drain/len, drop-oldest), stub `applyInput` + `applyInputHook` seam, `subtickCap=256`.
- `server/subtick_test.go` (created) - `TestSubtickOrdering` (chronological drain + empty-after), `TestSubtickBufferCap` (bound + drop-oldest + survivors ordered), `TestDispatchAppendsSubtickInput` (server-stamp, boundary marker, total/cheap dispatch).
- `server/keepalive_independent_test.go` (created) - `TestKeepAliveIndependentOfTick` + race-clean `fakeKeepAliveClient`.
- `server/tick.go` (edited) - `tickPlayer` extended (`subtick`, `lastInputAt`, `sawTickEnd`); `TickLoop` gained `clientIndex` + `applyInputHook`; `dispatch` wired for subtick append + boundary marker.
- `server/tick_phases.go` (edited) - `resolveSubtickInputs` filled (chronological drain → applyInput).

## Subtick Buffer API + Per-Player State (for Wave 3 / 03-03)

**Buffer API (`server/subtick.go`):**
```go
const subtickCap = 256
type SubtickInput struct { At time.Time; Packet pk.Packet }
type subtickBuffer struct { inputs []SubtickInput }
func (b *subtickBuffer) append(in SubtickInput)  // drop-oldest at cap
func (b *subtickBuffer) drain() []SubtickInput   // stable-sort by At, then empty
func (b *subtickBuffer) len() int
func (t *TickLoop) applyInput(p *tickPlayer, in SubtickInput) // STUB (records lastInputAt)
```

**Per-player state (`server/tick.go`):**
```go
type tickPlayer struct {
    client      *Client       // Phase-2 connection handle (flush via client.Send)
    subtick     subtickBuffer // bounded µs-timestamped input buffer
    lastInputAt time.Time     // Phase-3 observable; Phase 6 replaces with real movement state
    sawTickEnd  bool          // ClientTickEnd boundary marker recorded
}
```

**Routing hooks added to `TickLoop`:** `clientIndex map[*Client]*tickPlayer` (O(1) dispatch routing, tick-owned, nil-safe) and `applyInputHook func(*tickPlayer, SubtickInput)` (test-only, nil in prod).

**Wave 3 (03-03) guidance:** `tickPlayer` is the place to thread the keep-alive adapter — it already carries `client` and is tick-goroutine-owned, so 03-03 can add a `keepalive`/adapter field (and forward `ServerboundKeepAlive` from the existing `dispatch` switch's keep-alive case to `KeepAlive.ClientTick`) WITHOUT reshaping the struct or the dispatch signature. `clientIndex` is the join/leave registry hook point.

## Gate Results
- **Named tests** (`TestSubtickOrdering | TestSubtickBufferCap | TestKeepAliveIndependentOfTick | TestTickPhaseOrder | TestDispatchAppendsSubtickInput`): **PASS** (`-count=1 -v`).
- **Chronological-ordering test:** PASS — out-of-order appends drain ascending by `At`; buffer empty after; second drain applies nothing.
- **Buffer-cap test:** PASS — `4*cap` flood never exceeds `subtickCap` at any append; exactly the newest `cap` survive, in chronological order.
- **Keep-alive independence test:** PASS, stable across 20 runs — ping fires off a stalled tick, zero disconnects.
- **`go vet ./...` + `go build ./...`:** clean.
- **Docker `-race` (`golang:1.26`, `CGO_ENABLED=1`):** `go test ./server/... -race -count=1` — **CLEAN** across all 5 server packages.
- **No forbidden imports:** no `xsync`/`ants`/`conc` (only a comment noting "no xsync"). `server/keepalive.go` and `server/client.go` UNMODIFIED (0 changes). `Run` signature + `Intent` type unchanged.

## Decisions Made
See `key-decisions` frontmatter. Notably: `drain()` sorts defensively even though single-channel arrival is already ordered (cheap insurance for the Phase-6 multi-source/re-stamp case); the keepalive test accelerates the component's own `listTimer` rather than editing `keepalive.go`, keeping it verbatim and the test fast/deterministic.

## Deviations from Plan
None - plan executed exactly as written. (The plan left the exact `tickPlayer` shape and the dispatch routing mechanism to the executor; I introduced `clientIndex` for O(1) routing and `applyInputHook` as a test seam — both within the plan's stated latitude, no scope creep, no new deps.)

## Issues Encountered
- Docker bind-mount under Git Bash mangled the working-directory path (`C:/Program Files/Git/src`); resolved with `MSYS_NO_PATHCONV=1` and explicit `//d/ender://src` mount. The `-race` run then passed clean.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- TICK-03 seam and TICK-04 proof complete; ready for **Wave 3 (03-03)** to thread the keep-alive adapter onto `tickPlayer` and forward `ServerboundKeepAlive` from the existing dispatch case.
- Physics behind `applyInput` and the `ClientTickEnd` payload decode are deliberately deferred to **Phase 6 (ENT-02)** — the seam contract is in place for them to fill without reshaping.

## Self-Check: PASSED

All created files exist on disk (`server/subtick.go`, `server/subtick_test.go`, `server/keepalive_independent_test.go`, `03-02-SUMMARY.md`) and both task commits (`07011e53`, `88a71c95`) are present in git history.

---
*Phase: 03-authoritative-tick-loop*
*Completed: 2026-06-23*
