---
phase: 03-authoritative-tick-loop
plan: 01
subsystem: infra
tags: [tick-loop, fixed-timestep, accumulator, game-time, mspt, single-owner, injectable-clock, stdlib]

# Dependency graph
requires:
  - phase: 02-net-protocol-state-machine
    provides: "chan Intent seam (Intent{*Client, pk.Packet}), Client.Start/Send/Close, stubTickConsumer attach point"
provides:
  - "TickLoop: the single-owner authoritative game loop (sole mutator of all game state)"
  - "Run(ctx, inbound <-chan Intent): production driver over time.Ticker + injectable Clock"
  - "Fixed-timestep accumulator anchoring game-time to exactly 20 logical ticks/sec (TICK-02) with a 250ms spiral-of-death clamp"
  - "Ordered phase pipeline (TICK-01): resolveSubtickInputs -> world -> chunk -> entity -> AI -> physics -> applyAsyncResults -> tracker.Tick -> flushOutbound"
  - "applyAsyncResults no-op rejoin seam + tracker.Tick() synchronous stub (TICK-05 seam, Phase-8 attach points)"
  - "MSPT/TPS/gametime published as a read-only atomic TickStats snapshot via Stats() (TICK-06)"
  - "Injectable Clock interface (clock.go) so timing is deterministically testable with no real time.Sleep"
affects: [03-02-subtick-buffer, 03-03-gameplay-wiring, phase-4-world-chunk, phase-6-entity-physics, phase-7-ai, phase-8-async-optimizations]

# Tech tracking
tech-stack:
  added: []  # ZERO new dependencies — pure Go stdlib (time, context, sync/atomic, sort)
  patterns:
    - "Single-owner tick goroutine (TICK-05): only Intent crosses the network->tick boundary; -race clean by construction"
    - "Injectable Clock abstraction (design-it-in) so the accumulator/game-time/MSPT are fake-clock testable"
    - "Fixed-timestep accumulator with spiral-of-death clamp; gametime++ strictly inside for acc >= step"
    - "Shared advance()/advanceDraining() seam: one definition of step/clamp logic used by both Run (production) and tests"
    - "No-op-from-day-one rejoin seam (applyAsyncResults) + interface-backed tracker stub so Phase 8 is additive"
    - "Read-only atomic.Pointer[TickStats] telemetry snapshot (sole writer = tick goroutine)"

key-files:
  created:
    - server/clock.go
    - server/tick.go
    - server/tick_phases.go
    - server/tick_test.go
  modified: []  # client.go and keepalive.go deliberately UNCHANGED (Phase-2 seam consumed as-is)

key-decisions:
  - "advance() (test seam) and Run() (production driver) share advanceDraining() so step/clamp/accumulator logic lives in exactly one place — no duplicated constants"
  - "tracker.Tick is traced at its tickOnce call site (the interface impl cannot append to the phase trace itself)"
  - "TPS is derived from rolling-average tick cost, capped at the 20 target (avg <= 50ms => 20 TPS; only degrades below 20 when overloaded)"
  - "p99 computed by copying the valid ring window (<=100 entries) and sorting — trivial allocation, off the steady-state hot path concern for Phase 3"
  - "dispatch() is total: ServerboundClientTickEnd/ServerboundKeepAlive are recognized markers (no-op for now), unknown IDs are cheap no-ops, *Client deref deferred to Wave 2/3 per-player dispatch"

patterns-established:
  - "Single-owner tick goroutine: the cheap single-threaded form of TICK-05 ownership"
  - "Injectable Clock for deterministic timing tests (fakeClock advanced by the caller, never time.Sleep)"
  - "Fixed-order phase pipeline with no-op future-phase slots asserted by a phase-trace test"

requirements-completed: [TICK-01, TICK-02, TICK-05, TICK-06]

# Metrics
duration: 18min
completed: 2026-06-23
---

# Phase 3 Plan 01: Tick Spine Core Summary

**Single-owner TickLoop with a fixed-timestep accumulator over an injectable clock — game-time anchored to exactly 1200 ticks/60s, a 250ms spiral-of-death clamp, an ordered phase pipeline with no-op applyAsyncResults/tracker seams, and an atomic MSPT/TPS/gametime snapshot — built pure-stdlib and proven -race clean.**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-06-23T20:38Z (approx)
- **Completed:** 2026-06-23
- **Tasks:** 2 (both TDD)
- **Files modified:** 4 created, 0 existing modified

## Accomplishments
- **TICK-01 (ordered pipeline):** `tickOnce` runs the fixed phase order resolveSubtickInputs -> tickWorld -> tickChunks -> tickEntities -> tickAI -> tickPhysics -> applyAsyncResults -> tracker.Tick -> flushOutbound, asserted by `TestTickPhaseOrder`. `drainInbound` is deliberately kept out of `tickOnce` (it runs once per wake in `Run`, not per catch-up step).
- **TICK-02 (game-time anchor):** the fixed-timestep accumulator over the injectable `Clock` advances `gametime` exactly once per consumed 50ms step, strictly inside `for acc >= step`. `TestGameTimeAnchor` feeds 60s of mixed-size synthetic frames and asserts exactly 1200 increments, and that game-time never advances on a zero-step (sub-tick) wake.
- **Spiral-of-death clamp:** a single frame delta is clamped to 250ms before entering the accumulator. `TestSpiralClamp` injects a 2s stall and asserts a bounded <=5-step catch-up, a bounded accumulator, and a clean one-tick recovery on the next normal frame — a stall degrades to slowdown, never an unbounded freeze.
- **TICK-05 (ownership + seams):** the TickLoop is the sole mutator of all game state; only `Intent` crosses the network->tick boundary. `applyAsyncResults` is a genuine no-op while `asyncIn == nil`, and `tracker.Tick()` is a synchronous `noopTracker` stub — both shaped so Phase 8 attaches without reordering. Proven `-race -count=10` clean in golang:1.26 Docker.
- **TICK-06 (observability):** `recordMSPT` maintains a 100-tick ring and republishes a read-only `atomic.Pointer[TickStats]` (MSPTavg, MSPTp99, TPS, GameTime) readable off-thread via `Stats()`. `TestMSPTObservable` confirms an injected slow tick raises the observed p99.
- **Zero new dependencies:** pure Go stdlib (time, context, sync/atomic, sort). No xsync/ants/conc introduced. `server/client.go` and `server/keepalive.go` left untouched.

## Task Commits

Each task was committed atomically (TDD; skeleton+tests landed together so the package compiles):

1. **Task 1: Injectable Clock + TickLoop skeleton + ordered pipeline + no-op seams** — `0ee69f58` (feat)
2. **Task 2: Accumulator + game-time anchor + spiral clamp + drainInbound + MSPT observability** — `007399ea` (feat)

**Plan metadata:** (this SUMMARY + STATE/ROADMAP) — final docs commit.

## Files Created/Modified
- `server/clock.go` — `Clock` interface + `systemClock` real impl (injectable time source; monotonic, NTP-immune).
- `server/tick.go` — `TickLoop` struct + `NewTickLoop`; `TickStats`; `asyncResult`/`tracker` interfaces + `noopTracker`; `Run(ctx, inbound)`; shared `advance`/`advanceDraining` accumulator seam; `start`; `drainInbound`; total `dispatch`; `recordMSPT`; `Stats()`/`GameTime()` accessors; test-only phase-trace hook; timing constants (`tickStep`, `wakeInterval`, `maxFrame`).
- `server/tick_phases.go` — `tickOnce` ordered pipeline (with `start` capture, `gametime++`, `recordMSPT`); `applyAsyncResults` no-op seam; phase stub methods (resolveSubtickInputs/tickWorld/tickChunks/tickEntities/tickAI/tickPhysics/flushOutbound).
- `server/tick_test.go` — `fakeClock`; TestTickPhaseOrder, TestApplyAsyncResultsNoop, TestTrackerTickStub, TestGameTimeAnchor, TestSpiralClamp, TestMSPTObservable, TestDrainInboundNonBlocking.

## Consumable Seams (for Wave 2/3 and later phases)

- **Run signature:** `func (t *TickLoop) Run(ctx context.Context, inbound <-chan Intent)` — drop-in replacement for `stubTickConsumer`'s role; takes the same `chan Intent`, no API change to client.go.
- **Construction:** `NewTickLoop(clock Clock) *TickLoop` — pass `systemClock{}` in production, a fake in tests.
- **Telemetry:** `func (t *TickLoop) Stats() *TickStats` (nil before first tick) and `func (t *TickLoop) GameTime() int64`.
- **Async rejoin seam:** `asyncIn <-chan asyncResult` (nil today) drained in `applyAsyncResults`; `type asyncResult interface{ applyTo(*TickLoop) }`. Phase 8 wires the channel + concrete result types; pipeline order is unchanged.
- **Tracker seam:** `type tracker interface{ Tick() }`, default `noopTracker{}`. Phase 8 swaps the executor with no `tickOnce` change.
- **Per-player collection:** `players []*tickPlayer` (owned by the tick goroutine; `tickPlayer.client *Client`). Wave 2/3 populates it via a join path and fills `flushOutbound`/`resolveSubtickInputs`.
- **Dispatch:** `dispatch(c *Client, p pk.Packet)` switches on `packetid.ServerboundPacketID(p.ID)` — `ServerboundClientTickEnd` boundary marker (full subtick use 03-02/Phase 6), `ServerboundKeepAlive` forwarding wired in 03-03, unknown IDs no-op.

## Decisions Made
- Introduced a shared `advanceDraining(now, inbound)` so `Run` (production, drains per wake before stepping) and `advance(now)` (tests, nil inbound) use one accumulator/clamp/step definition — satisfies the plan's "do not duplicate the clamp/step constants" directive.
- Traced `tracker.Tick` at the `tickOnce` call site because the interface implementation cannot append to the loop's phase trace.
- TPS derived from rolling average and capped at 20 (only drops below when the average tick exceeds 50ms), matching the "slow, never speed up" game-time contract.

## Deviations from Plan
None - plan executed exactly as written. The two minor implementation shapes above (shared advanceDraining seam, call-site tracker trace) are within the plan's stated guidance ("expose a test-only stepping entrypoint ... over the SAME accumulator logic ... do not duplicate the clamp/step constants"), not deviations.

## Issues Encountered
- `dispatch` referenced `pk.Packet` before `tick.go` imported the packet alias — build failed once; added `pk "github.com/imhinotori/sulfur/net/packet"` to the imports. Caught immediately by the Task-2 build gate and fixed before commit.

## Known Stubs
The empty phase methods (`tickWorld`, `tickChunks`, `tickEntities`, `tickAI`, `tickPhysics`, `resolveSubtickInputs`, `flushOutbound`) and the no-op `applyAsyncResults`/`noopTracker` are **intentional, plan-mandated future-phase slots**, not blocking stubs. Each is asserted present and in-order by `TestTickPhaseOrder`, and the plan's objective is precisely to establish these seams so Phases 4/6/7/8 fill them without reordering. Resolution map: world/chunk -> Phase 4; entity/physics -> Phase 6; AI -> Phase 7; resolveSubtickInputs -> 03-02; flushOutbound -> Wave 2/3; applyAsyncResults/tracker -> Phase 8.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- The tick spine is ready for 03-02 (subtick input buffer fills `resolveSubtickInputs` + `tickPlayer.subtick`) and 03-03 (real GamePlay wires `Run` over the inbound channel, registers players into `players`, forwards keep-alive).
- All Phase-3 gates green: `go build ./...`, `go vet ./server/...`, `go test ./server/...` native; Docker `-race -count=10` clean (TICK-05 ownership by construction).
- No blockers. The Phase-2 `chan Intent` seam was consumed unchanged — the hard-blocker condition (needing to alter the Intent type) did not arise.

## Self-Check: PASSED

All 4 created source files exist on disk; both task commits (`0ee69f58`, `007399ea`) exist in git history. Gates re-verified: native `go build`/`go vet`/`go test ./server/...` green; Docker `-race -count=10` clean.

---
*Phase: 03-authoritative-tick-loop*
*Completed: 2026-06-23*
