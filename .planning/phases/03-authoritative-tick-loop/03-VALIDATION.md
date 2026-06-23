---
phase: 3
slug: authoritative-tick-loop
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-23
---

# Phase 3 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

This phase is **pure Go stdlib + in-repo fork APIs** (no new dependency). It builds the single-owner tick spine, the game-time anchor, the minimal subtick seam, the no-op async rejoin seam, and wires the fork's existing `KeepAlive` on its own goroutine. The hard-to-get-right primitive is the **timing loop** (fixed-timestep accumulator + spiral-of-death clamp + monotonic measurement), and the load-bearing invariant is **single-owner game state** (TICK-05). Validation is therefore dominated by **deterministic Go unit tests driven by an INJECTABLE CLOCK** (synthetic monotonic frames — never real `time.Sleep`), plus a **`-race` gate in `golang:1.26` Docker** proving the ownership boundary, and a **scripted in-memory end-to-end** check that a connection survives in a ticking server (no disconnect, keep-alive holds, MSPT observable). There is **no capture-diff** here: the tick is internal and not directly observable on the wire, so no manual vanilla-eyeball gate is required.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing`; timing tests use an injectable `Clock` (a `now func() time.Time` / advanceable fake), never wall-clock `time.Sleep` |
| **Config file** | none — `go test ./...` (Go convention: `*_test.go`) |
| **Quick run command** | `go build ./... && go vet ./server/... && go test ./server/... -count=1` |
| **Full suite command** | `go vet ./... && go build ./... && go test ./... -count=1` then (race) `docker run --rm -v "$PWD":/src -w /src golang:1.26 go test ./server/... -race -count=10` |
| **Estimated runtime** | ~10–40 seconds for the deterministic suite (injectable clock means **zero** real waiting); the Docker `-race -count=10` gate is a separate ~1–3 min step |
| **Injectable-clock harness** | The tick loop takes its time source as an interface/closure (`Clock` with `Now() time.Time`). Tests construct a fake that returns caller-controlled monotonic instants, drive `tickOnce`/the accumulator with synthetic frame deltas, and assert game-time / MSPT / spiral behavior **without waiting real time**. Designed in from Wave 0, never retrofitted (the research flags `time.Sleep` jitter as Pitfall 3). |

> Race note: the host has **no C compiler** (`CGO_ENABLED=0`), so `-race` runs in `golang:1.26` Docker — the established Phase-2 path `[VERIFIED: 02-01-SUMMARY.md]`. A correctly single-owned tick is race-clean by construction.

---

## Sampling Rate

- **After every task commit:** `go build ./... && go vet ./server/... && go test ./server/... -count=1`
- **After the tick-core task (03-01) and the subtick task (03-02):** add the Docker `-race` smoke — `docker run --rm -v "$PWD":/src -w /src golang:1.26 go test ./server/... -race -count=1`
- **After every plan wave:** `go vet ./... && go build ./... && go test ./... -count=1` + the Docker `-race -count=10` on `./server/...` (the TICK-05 ownership gate).
- **Phase gate (before `/gsd-verify-work`):** full suite green **AND** Docker `-race -count=10` clean **AND** the scripted end-to-end (`TestPlayerStandsInTickingWorld`) passes: a piped connection enters the tick, is kept alive on the independent timer, and the server exposes a live MSPT/TPS snapshot — with no disconnect and no game-state pointer crossing the network boundary.
- **Max feedback latency:** < 60 seconds for the deterministic suite (the Docker race gate is excluded — it is the wave/phase gate, not a per-task check).

---

## Per-Task Verification Map

| Req ID | Plan | Wave | Behavior | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|--------|------|------|----------|------------|-----------------|-----------|-------------------|-------------|--------|
| TICK-01 | 03-01 | 1 | Ordered phases run in fixed order each logical tick (drain → world → chunk → entity → AI → physics → applyAsyncResults → tracking → flush); `applyAsyncResults` present as a no-op phase | T-3-02 (slow/malformed handler stalls tick) | Each phase is total & non-blocking (no IO/park inside the tick); order is asserted by a recorded call-trace | unit (injectable clock) | `go test ./server/ -run TestTickPhaseOrder -count=1` | ❌ W0 | ⬜ pending |
| TICK-02 | 03-01 | 1 | 60 s of synthetic frames → exactly 1200 `gametime` increments; gametime advances once per consumed 50 ms step, never per wake | T-3-04 (NTP/time-source skew) | Monotonic `Clock.Now()` deltas drive the accumulator; gametime is a pure tick counter immune to wall-clock jumps | unit (injectable clock) | `go test ./server/ -run TestGameTimeAnchor -count=1` | ❌ W0 | ⬜ pending |
| (spiral) | 03-01 | 1 | An injected ≥2 s frame is clamped (250 ms cap); the catch-up loop is bounded and recovers; TPS telemetry reflects slowdown rather than freezing | T-3-02 (DoS via stall) | Spiral-of-death clamp bounds catch-up: one bad frame degrades to slowdown, never an unbounded freeze | unit (injectable clock) | `go test ./server/ -run TestSpiralClamp -count=1` | ❌ W0 | ⬜ pending |
| TICK-06 | 03-01 | 1 | MSPT/TPS/gametime snapshot is published every tick and readable off-thread; reflects an injected stall | — | Telemetry is a read-only `atomic.Pointer[TickStats]` snapshot — never a backdoor to mutate game state off-thread | unit (injectable clock) | `go test ./server/ -run TestMSPTObservable -count=1` | ❌ W0 | ⬜ pending |
| TICK-05 (ownership) | 03-01 | 1 | No game-state pointer crosses the network/async boundary; the tick goroutine is the sole mutator; race-clean | T-3-03 (state escapes owner goroutine) | Only `Intent` (`*Client`+`pk.Packet`) crosses the network seam; `applyAsyncResults` is the sole apply point — `-race` clean by construction | race (Docker) | `docker run --rm -v "$PWD":/src -w /src golang:1.26 go test ./server/... -race -count=10` | ⚠ harness exists (Phase 2), tick tests ❌ W0 | ⬜ pending |
| TICK-05 (async seam) | 03-01 | 1 | `applyAsyncResults` exists as a real ordered phase that is a no-op today; `tracker.Tick()` is a synchronous stub; shaped so Phase 8 attaches without reorder | — | The async-result channel is nil today; when wired (Phase 8) results are immutable and applied only by the owner | unit | `go test ./server/ -run 'TestApplyAsyncResultsNoop\|TestTrackerTickStub' -count=1` | ❌ W0 | ⬜ pending |
| TICK-03 | 03-02 | 2 | µs-stamped inputs are drained per player in chronological order inside the tick; broadcast still at the 20/s flush rate; per-player buffer is size-capped | T-3-01 (inbound flood saturates drain / grows buffer) | Per-player subtick buffer is bounded (cap + drop-oldest); chronological drain by arrival stamp; a flood degrades that player, never the whole tick | unit | `go test ./server/ -run 'TestSubtickOrdering\|TestSubtickBufferCap' -count=1` | ❌ W0 | ⬜ pending |
| TICK-04 | 03-02 | 2 | The tick goroutine stalled ≥5 s → no keep-alive timeout fires (independent timer); pings continue on `KeepAlive.Run`'s own goroutine | T-3-05 (keep-alive coupled to tick → mass 20 s disconnects); T-3-06 (keep-alive spoof) | `KeepAlive.Run(ctx)` runs on its OWN goroutine with its OWN 15 s/30 s timers, never gated on tick cadence; server-generated incrementing IDs validated on return | unit | `go test ./server/ -run TestKeepAliveIndependentOfTick -count=1` | ❌ W0 (reuses fork keepalive.go unchanged) | ⬜ pending |
| TICK-01..06 (integration) | 03-03 | 3 | A piped connection enters the real tick-driven GamePlay, is kept alive, the world ticks, and MSPT/TPS is observable — no disconnect, no game-state leak | T-3-03, T-3-05 | End-to-end over `net.Pipe`: the real `gameTick.AcceptPlayer` registers the conn with the tick + independent keep-alive; `-race` clean | integration (pipe) + race | `go test ./server/ -run TestPlayerStandsInTickingWorld -count=1` then Docker `-race -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Test scaffolds, shared fixtures, and the timing skeleton that MUST exist before the corresponding implementation task runs. The implementer creates the test/skeleton (RED) before/with the implementation.

- [ ] **Injectable clock (design-it-in, not retrofitted):** a `Clock` abstraction (`type Clock interface { Now() time.Time }` plus a real `systemClock{}` and a test `fakeClock` whose `Now()` returns caller-advanced monotonic instants). The `TickLoop` takes a `Clock` so every timing test is deterministic and instant. Lives in `server/clock.go` (real) + exercised from `server/tick_test.go` (fake). **Mandatory in 03-01 Task 1** — all of TICK-02/TICK-06/spiral depend on it.
- [ ] **Tick-phase skeleton/enum:** the ordered phase list expressed so its order is testable — either named phase methods called in a fixed sequence by `tickOnce()`, or a phase enum/trace the test can assert against (`TestTickPhaseOrder`). Lives in `server/tick.go` + `server/tick_phases.go`.
- [ ] `server/tick_test.go` — covers TICK-01 (`TestTickPhaseOrder`), TICK-02 (`TestGameTimeAnchor`), TICK-06 (`TestMSPTObservable`), spiral (`TestSpiralClamp`), and the seam stubs (`TestApplyAsyncResultsNoop`, `TestTrackerTickStub`). Drives the loop with synthetic monotonic frames via the fake clock — **no real waiting**.
- [ ] `server/subtick_test.go` — covers TICK-03 (`TestSubtickOrdering` chronological drain, `TestSubtickBufferCap` bounded buffer).
- [ ] `server/keepalive_independent_test.go` — covers TICK-04 (`TestKeepAliveIndependentOfTick`: stall the tick goroutine, assert no keep-alive timeout fires because its timer runs on its own goroutine). Reuses the fork `server/keepalive.go` UNCHANGED.
- [ ] `server/gameplay_tick_test.go` — covers the integration (`TestPlayerStandsInTickingWorld`) over the Phase-2 `net.Pipe` harness (`server/pipe_test.go`, already present from Phase 2): a piped conn reaches the tick-driven GamePlay, stays connected, and exposes a live MSPT/TPS snapshot.
- [ ] **Tick `-race` reuse:** the Phase-2 `net.Pipe` harness is reused as the integration driver for the TICK-05 ownership race gate (`go test ./server/... -race` in Docker). No new harness needed.

*(Framework already present — no install needed. Zero new module dependencies in Phase 3.)*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| (none) | — | The tick is an **internal** subsystem with no directly-observable wire output that diverges from vanilla (no capture-diff target — unlike Phase 2's Configuration stream). Every Phase-3 contract is provable by a deterministic timing test, the `-race` ownership gate, or the scripted in-memory end-to-end. No human eyeball gate is required. | — |

> Justification for keeping 03-03 fully automated (per the orchestrator's question): the "client stands in a ticking world" check in Phase 3 asserts *liveness and observability* (connection survives, keep-alive holds, MSPT snapshot is live, no game-state leak) — all of which are machine-checkable over `net.Pipe`. There is no *visual/world-rendering* claim yet (the world is still empty until Phase 4), so there is nothing for a human to eyeball that a test cannot assert. The real-vanilla-client visual milestone is Phase 5 (PLAY-06), which keeps its human sign-off.

---

## Validation Sign-Off

- [ ] Every task has an `<automated>` verify command or an explicit Wave 0 dependency (Nyquist).
- [ ] Sampling continuity: no 3 consecutive tasks without an automated build/test check.
- [ ] The tick-core task (03-01) and the subtick task (03-02) run under Docker `-race`.
- [ ] Wave 0 stands up the **injectable clock** and the tick-phase skeleton before any timing implementation (no test depends on real wall-clock time).
- [ ] The TICK-05 ownership race gate (`-race -count=10` in `golang:1.26` Docker) is a required wave/phase gate.
- [ ] No `time.Sleep`-driven timing assertions; no watch-mode flags.
- [ ] Feedback latency < 60s for the deterministic suite (Docker race gate excluded — it is the wave/phase gate).
- [ ] `nyquist_compliant: true` (every TICK-0x behavior maps to an automated command + a Wave 0 scaffold; there is no prescribed manual gate this phase).

**Approval:** pending
