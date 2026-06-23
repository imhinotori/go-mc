# Phase 3: Authoritative Tick Loop - Research

**Researched:** 2026-06-23
**Domain:** Game-server tick architecture (single-owner fixed-timestep loop, game-time anchoring, subtick input layer) in Go
**Confidence:** HIGH

## Summary

Phase 3 builds the **synchronous authoritative spine** that owns all game state, plus the **seams** later phases attach to. The hard parts are not the loop mechanics (a fixed-timestep accumulator over `time.Ticker` is settled, well-documented across Paper/Folia/Minestom and the canonical "Fix Your Timestep" pattern) — they are the *invariants and seam shapes* that must be correct on day one so Phase 8's async optimizations are **additive, not a rewrite**:

1. **Ownership-based synchronization (TICK-05):** exactly one goroutine — the tick goroutine — mutates game state. Everything else (network read/write goroutines from Phase 2, async workers in Phase 8) communicates only through channels. This is the cheap single-threaded form of synchronization that makes `-race` trivially clean today and keeps the same shape when async lands. The `applyAsyncResults` rejoin seam exists as a **no-op phase** in the ordered pipeline from day one.
2. **Game-time vs. internal scheduler decoupling (TICK-02):** Minecraft defines game-time as a pure **tick counter** (`gametime++` per logical tick, 24000 ticks/day = 20 min) `[VERIFIED: minecraft.wiki/w/Commands/time]`. The logical game clock MUST advance **exactly 20 times/second** regardless of how often the internal loop wakes. The accumulator decouples real wall-clock from logical ticks: you advance game-time one step *per consumed 50 ms accumulator chunk*, never per loop iteration.
3. **CS2-style subtick layer (TICK-03):** vanilla itself already separates "world ticking" from "player movement" — `/tick freeze` stops the world but players and ridden entities still move `[VERIFIED: minecraft.wiki/w/Commands/tick]`. That is the precedent. Phase 3 builds the **minimal seam**: a per-player µs-timestamped input buffer drained in chronological order inside the tick, with the *full* movement/collision/projectile resolution deferred to Phase 6. The client already signals its input batch boundary via `ServerboundClientTickEnd` (new in 1.21.2 / present in 776) `[VERIFIED: minecraft.wiki/w/Java_Edition_protocol/Packets]`.

**Primary recommendation:** Build a single-goroutine tick loop driven by a fixed-timestep accumulator (`time.Ticker` at a sub-tick wake rate feeding a 50 ms accumulator with a spiral-of-death clamp). Structure the tick body as an explicit ordered phase list with `applyAsyncResults` and `tracker.Tick()` present as no-op/synchronous stubs. Reuse the fork's existing channel-driven `KeepAlive` component **as-is** on its own goroutine (it is already the independent-timer design TICK-04 wants). Introduce **no** concurrency-stack dependency (xsync/ants/conc) — none belong in Phase 3.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Tick scheduling / timing | Tick goroutine (game core) | — | The loop wall-clock driver; owns the accumulator and game-time counter |
| Game-state mutation (world/entity/player) | Tick goroutine (game core) | — | TICK-05 ownership law: exactly one mutator |
| Inbound packet decode + dispatch | Tick goroutine (game core) | Network read goroutine (enqueue only) | readLoop produces `Intent`; tick *consumes and dispatches* — Phase-2 seam |
| Outbound packet write | Network write goroutine (per-conn) | Tick goroutine (enqueue via `Client.Send`) | Single-writer-per-conn from Phase 2; tick never writes the socket directly |
| Keep Alive ping/timeout | Independent KeepAlive goroutine | — | TICK-04: its own timer, decoupled from the 50 ms tick cadence |
| Subtick input buffering | Network read goroutine (timestamp+enqueue) | Tick goroutine (ordered drain) | Capture happens at arrival; resolution happens in-tick in chronological order |
| Async result rejoin (`applyAsyncResults`) | Tick goroutine (apply) | Phase-8 worker pools (compute) | Compute-off / apply-on: results cross back via channel, applied by the owner |
| MSPT measurement | Tick goroutine | — | Measured around the tick body by the owner; no cross-goroutine timing needed |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go stdlib `time` (`time.Ticker`, `time.Now`, `time.Duration`) | Go 1.26.1 | Wall-clock driver for the accumulator; MSPT measurement | `time.Now()` returns a **monotonic** reading on every platform; durations between two `time.Now()` values are immune to wall-clock jumps/NTP `[VERIFIED: pkg.go.dev/time — "Monotonic Clocks"]`. `time.Ticker` adjusts intervals to make up for slow receivers (no unbounded drift) `[VERIFIED: pkg.go.dev/time#Ticker]` |
| Go stdlib `context` | Go 1.26.1 | Tick-loop + KeepAlive lifecycle/shutdown | The fork's `KeepAlive.Run(ctx)` already uses it `[VERIFIED: server/keepalive.go]` |
| Go channels (`chan Intent`) | language | Network→tick seam (inbound), async→tick rejoin (Phase 8) | The Phase-2 seam is a `chan Intent`; the tick loop is its consumer `[VERIFIED: server/client.go]` |
| Fork `server.Client` API (`Start`, `Send`, `Close`) | in-repo | Per-connection plumbing the tick attaches to **unchanged** | Single-writer-per-conn + bounded drop-and-disconnect already proven `-race` clean `[VERIFIED: server/client.go, 02-01-SUMMARY.md]` |
| Fork `server.KeepAlive` component | in-repo | Independent-timer keep-alive (TICK-04) | Already channel-driven on its own `Run(ctx)` goroutine with 15 s ping / 30 s timeout timers — exactly vanilla values `[VERIFIED: server/keepalive.go; minecraft keepalive 15s interval]` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| Go stdlib `sync/atomic` | Go 1.26.1 | Publish read-only telemetry (current MSPT, TPS, gametime) for observability without locking | TICK-06: expose an `atomic.Int64`/`atomic.Pointer` snapshot the tick writes and observers read. Keep it telemetry-only — never a backdoor to mutate game state off-thread |
| Go stdlib `container/list` | Go 1.26.1 | Already used by `KeepAlive`'s ping/wait lists | No new code; it's inside the reused component `[VERIFIED: server/keepalive.go]` |

### Deferred to Phase 8 (do NOT add in Phase 3)
| Library | Belongs In | Why Not Now |
|---------|-----------|-------------|
| `github.com/puzpuzpuz/xsync/v4` | Phase 8 | Concurrent maps matter only once multiple goroutines touch the entity/chunk tables. In Phase 3 only the tick goroutine reads/writes state — a plain `map`/slice is correct and faster `[CITED: CLAUDE.md "Do NOT introduce ants/xsync yet"]` |
| `github.com/panjf2000/ants/v2` | Phase 8 | No async subsystems exist to pool yet. The `applyAsyncResults` seam is a no-op now `[CITED: CLAUDE.md]` |
| `github.com/sourcegraph/conc` | Phase 8 (maybe) | Tick-bounded fan-out/join is a Phase-8 concern; Phase 3 establishes the single-owner spine first `[CITED: critical_runtime_facts]` |
| `golang.org/x/sync` (singleflight/semaphore) | Phase 4+ | singleflight dedups chunk-gen (Phase 4); semaphore throttles region IO (Phase 4/8) `[CITED: CLAUDE.md]` |

**Installation:** None. Phase 3 adds **zero** new module dependencies — it is pure stdlib + in-repo fork APIs. `go.mod` stays at its current `require` set (`google/uuid`, `golang.org/x/exp`) `[VERIFIED: go.mod]`.

**Version verification:** No external packages to version-check. The runtime is Go 1.26.1; `go.mod` declares `go 1.22` (a valid floor — 1.26.1 is a superset) `[VERIFIED: go.mod]`.

## Architecture Patterns

### System Architecture Diagram

```
                        per-connection (Phase 2, UNCHANGED)
  vanilla 26.2 client ──TCP──> [readLoop] ──Intent{*Client,Packet}──┐
        ▲                                                            │
        │                                              chan Intent   │  (the seam)
        │  ┌──[writeLoop]<── Client.Send(pk) ◄──┐                    ▼
        │  │   (sole writer,                    │        ┌─────────────────────────────┐
        └──┘    bounded queue)                  │        │      TICK GOROUTINE          │
                                                │        │  (sole owner of game state) │
                                                │        │                             │
   ┌──────────────────────────────────────┐    │        │  fixed-timestep accumulator │
   │  KeepAlive goroutine (Run(ctx))       │    │        │  wakes on time.Ticker       │
   │  own 15s ping / 30s timeout timers    │    │        │                             │
   │  SendKeepAlive / SendDisconnect ──────┼────┘        │  for each due 50ms step:    │
   │  (TICK-04: independent of tick rate)  │             │   1. drain inbound (dispatch│
   └──────────────────────────────────────┘             │      Intents; buffer subtick│
                                                         │      input by µs timestamp) │
   ┌──────────────────────────────────────┐             │   2. world   (Phase 4)      │
   │  Phase-8 async workers (FUTURE)       │  results    │   3. chunk   (Phase 4)      │
   │  pathfinding / tracker / spawner ─────┼──chan──────►│   4. entity  (Phase 6)      │
   │  (compute OFF the tick)               │             │   5. AI      (Phase 7)      │
   └──────────────────────────────────────┘             │   6. physics (Phase 6)      │
                                                         │   7. applyAsyncResults ◄────│ no-op now
                                                         │   8. tracking (tracker.Tick)│ stub now
                                                         │   9. flush outbound ────────┼─► Client.Send
                                                         │  gametime++ (exactly 1/step)│
                                                         │  record MSPT (TICK-06)      │
                                                         └─────────────────────────────┘
```

Trace the primary path: a packet arrives → `readLoop` makes an `Intent` → tick goroutine drains it in phase 1 → dispatch mutates owned state → flush phase enqueues clientbound packets via `Client.Send` → `writeLoop` writes the socket. **No game-state pointer ever leaves the tick goroutine.**

### Recommended Project Structure
```
server/
├── tick.go            # TickLoop: accumulator, ordered phase pipeline, gametime, MSPT
├── tick_phases.go     # the ordered phase functions (drain/world/.../flush) + applyAsyncResults no-op
├── subtick.go         # per-player µs-timestamped input buffer + chronological drain (minimal seam)
├── gameplay_tick.go   # the real GamePlay replacing stubGamePlay: AcceptPlayer registers conn with the tick
├── client.go          # UNCHANGED Phase-2 seam (Intent, Client, Start, Send, Close)
├── keepalive.go       # UNCHANGED fork component, now wired on its own goroutine (TICK-04)
cmd/sulfur/main.go     # wire the real GamePlay + start the tick goroutine (replaces stubGamePlay)
```

### Pattern 1: Single-Owner Tick Goroutine (TICK-05)
**What:** Exactly one goroutine owns and mutates all game state. The Phase-2 single-writer-per-connection rule is the same idea applied to the socket; Phase 3 generalizes it to *all* game state.
**When to use:** Always, for every game-state field, from day one.
**Rule:** A value is either (a) confined to the tick goroutine, or (b) crosses a goroutine boundary **only as an immutable message on a channel**. `Intent` already obeys this — it carries `*Client + pk.Packet`, no game-state pointer `[VERIFIED: server/client.go:35-44]`. The same discipline applies to Phase-8 async results: workers receive an immutable snapshot, return an immutable result, the tick applies it.
**Verification:** `go test -race` (run in `golang:1.26` Docker — host has no C compiler, `CGO_ENABLED=0` `[VERIFIED: 02-01-SUMMARY.md]`). If state is truly single-owned, `-race` is clean by construction.

```go
// Source: pattern derived from server/client.go single-writer model + Paper/Folia main-thread ownership
func (t *TickLoop) Run(ctx context.Context, inbound <-chan Intent) {
    // Wake faster than the tick so the accumulator has fine-grained input;
    // the LOGICAL tick still advances at exactly 20/s (see Pattern 3).
    const wake = 5 * time.Millisecond
    ticker := time.NewTicker(wake)
    defer ticker.Stop()

    last := time.Now()
    var acc time.Duration
    const step = 50 * time.Millisecond // one MC tick

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            now := time.Now()
            frame := now.Sub(last) // monotonic; immune to NTP jumps
            last = now

            // Spiral-of-death clamp: never try to catch up more than a bound.
            if frame > 250*time.Millisecond {
                frame = 250 * time.Millisecond // drop the backlog; world slows, never freezes
            }
            acc += frame

            for acc >= step {
                t.drainInbound(inbound) // non-blocking: consume what's queued, don't park
                t.tickOnce()            // ONE logical tick: ordered phases + gametime++
                acc -= step
            }
        }
    }
}
```

### Pattern 2: Ordered Phase Pipeline with the Async Rejoin Seam (TICK-01, TICK-05)
**What:** The tick body is an explicit, fixed-order list of phase functions. `applyAsyncResults` is a real phase that is a **no-op today** and becomes "drain the async result channels and apply" in Phase 8 — without reordering anything.
**When to use:** Every logical tick.

```go
// Source: ordering per ROADMAP Phase-3 success criteria + Paper tick stages
func (t *TickLoop) tickOnce() {
    start := time.Now()

    // 1. inbound already drained by Run() before this call (kept separate so the
    //    accumulator can drain once per wake, not once per catch-up step).
    t.resolveSubtickInputs() // chronological drain of µs-stamped player inputs (minimal seam)
    t.tickWorld()            // Phase 4 fills
    t.tickChunks()           // Phase 4 fills
    t.tickEntities()         // Phase 6 fills
    t.tickAI()               // Phase 7 fills
    t.tickPhysics()          // Phase 6 fills
    t.applyAsyncResults()    // NO-OP today; Phase 8 drains worker result channels here
    t.tracker.Tick()         // synchronous tracker stub today; Phase 8 swaps the executor
    t.flushOutbound()        // enqueue clientbound packets via Client.Send

    t.gametime++             // EXACTLY once per logical tick — anchors TICK-02
    t.recordMSPT(time.Since(start)) // TICK-06
}

// applyAsyncResults is the rejoin seam. It exists from day one so Phase 8 attaches
// without touching the pipeline order. Today it does nothing.
func (t *TickLoop) applyAsyncResults() {
    // Phase 8: for each async subsystem, non-blocking drain its result channel and
    // apply the immutable result to owned state here, on the tick goroutine.
}
```

**Why the no-op seam matters:** Phase 8's OPT-01/02/03 explicitly "rejoin via the apply-async seam" `[VERIFIED: REQUIREMENTS.md OPT-02]`. If the phase slot does not exist now, adding it later means re-threading ordering and re-proving `-race` — exactly the rewrite the architecture is designed to avoid `[VERIFIED: ROADMAP.md Overview, STATE.md Decisions]`.

### Pattern 3: Game-Time Anchored to 50 ms, Decoupled from Wake Rate (TICK-02)
**What:** `gametime` is a pure integer counter incremented **once per consumed accumulator step**, never per loop wake. The ticker may wake every 5 ms, but `gametime++` runs only inside `for acc >= step`. Result: 20 logical ticks/second exactly → day = 24000 ticks = 20 min, and redstone/crops/weather (all tick-counted) never accelerate or slow `[VERIFIED: minecraft.wiki/w/Commands/time — "Daytime ... modulo 24000", "Gametime — age of the world in game ticks"]`.
**The trap this avoids:** Minecraft measures *everything* time-related by tick count. If you "raise effective TPS" or advance game-time per wake, the day shortens and crops grow faster — a vanilla-parity violation, explicitly Out of Scope `[VERIFIED: REQUIREMENTS.md "Raising effective TPS to speed up the world"]`.
**Vanilla precedent for the decoupling:** `/tick sprint` runs the world as fast as possible ignoring the target rate, and `/tick freeze` stops world ticks while **players still move** `[VERIFIED: minecraft.wiki/w/Commands/tick]`. The engine already separates "logical world advance" from "wall-clock" and from "player responsiveness" — Phase 3 mirrors that separation.

### Pattern 4: Subtick Input Buffer — Minimal Phase-3 Seam (TICK-03)
**What:** When an input packet (movement, attack, use) arrives, the read goroutine stamps it with `time.Now()` and enqueues it. Inside the tick, `resolveSubtickInputs()` drains each player's buffer **in chronological order** and applies them. Broadcasting to clients still happens at the vanilla 20/s flush rate.
**CS2 parallel:** CS2 timestamps inputs at high frequency (~4096 Hz) but resolves them at the fixed tick/frame, processing the chronologically-ordered batch so rapid input sequences (forward-then-strafe) resolve correctly rather than being collapsed to one tick `[VERIFIED: turboboost.gg/article/cs2-tickrate-subtick; steamcommunity CS2 subtick discussion]`.
**Protocol hook:** the client sends `ServerboundClientTickEnd` to mark its input-batch boundary (new in 1.21.2, present in 776) — present in the generated packet IDs as `ServerboundClientTickEnd` `[VERIFIED: data/packetid/packetid.go:242; minecraft.wiki/w/Java_Edition_protocol/Packets]`. Phase 3 may simply record the boundary; full use is Phase 6.

**MINIMAL Phase-3 seam (do NOT build full movement/collision/projectiles now — that is Phase 6 / ENT-02):**
```go
// Source: CS2 subtick model (timestamp-on-arrival, chronological in-tick resolution)
type SubtickInput struct {
    At     time.Time // µs-precise arrival stamp (monotonic via time.Now)
    Packet pk.Packet // the raw movement/use/attack intent
}

// Buffer lives in per-player state, owned by the tick goroutine. The read goroutine
// does NOT write it directly — it sends an Intent; the drain phase stamps + appends.
func (t *TickLoop) resolveSubtickInputs() {
    for _, p := range t.players {
        // inputs already chronological (appended in arrival order on one channel);
        // if multiple sources merge, sort by At before applying.
        for _, in := range p.subtick.drain() {
            t.applyInput(p, in) // Phase 3: minimal (e.g. record last pos / validate);
                                // Phase 6 fills real collision/hit-reg/projectile math
        }
    }
}
```
**The Phase-3 contract this proves:** inputs are captured with µs timestamps, resolved in chronological order inside the tick, and the client is still updated at the 20/s flush rate. The *physics* behind `applyInput` is intentionally a stub until Phase 6.

### Pattern 5: Keep Alive on an Independent Timer (TICK-04)
**What:** Reuse the fork's existing `server.KeepAlive` component **verbatim**. It runs its own `Run(ctx)` goroutine with its own `listTimer` (15 s ping) and `waitTimer` (30 s timeout), entirely decoupled from the 50 ms tick `[VERIFIED: server/keepalive.go:13-14,63-80]`. Wire each player in via `ClientJoin` on AcceptPlayer and `ClientLeft` on disconnect; respond to inbound `ServerboundKeepAlive` via `ClientTick`.
**When to use:** From the moment a player enters Play.
**Why this is already TICK-04-correct:** the component is a separate goroutine with dedicated timers — it cannot be starved by a slow tick, and a slow tick cannot delay pings past 30 s. The "mystery ~20s disconnect" happens precisely when keep-alive is driven *off* the game tick and the tick stalls; keeping it independent is the fix `[VERIFIED: github.com/PaperMC/Paper#1157 keepalive-timeout reports tie to main-thread stalls]`.
**Constants are already vanilla:** 15 s interval / 30 s timeout match vanilla `[VERIFIED: keepalive 15s interval search; server/keepalive.go]`.

### Pattern 6: MSPT Tracking & Observability (TICK-06)
**What:** Measure `time.Since(start)` around `tickOnce()`. Maintain a ring buffer (e.g. last 100 ticks → rolling avg/p99) plus a live TPS. Publish a snapshot via `atomic.Pointer[TickStats]` so an HTTP/log observer reads it without touching the tick goroutine.
**When to use:** Every tick.
**Why atomic snapshot (not a mutex):** the tick goroutine is the sole writer; observers are read-only. An `atomic.Pointer` swap of an immutable stats struct keeps observability off the critical path and `-race` clean.

```go
// Source: tick-time measurement pattern (Paper /tps, Minecraft /tick query)
type TickStats struct{ MSPTavg, MSPTp99 float64; TPS float64; GameTime int64 }

func (t *TickLoop) recordMSPT(d time.Duration) {
    t.ring[t.ringIdx%len(t.ring)] = d
    t.ringIdx++
    // recompute avg/p99/tps cheaply, publish an immutable snapshot:
    t.stats.Store(&TickStats{ /* ... */ GameTime: t.gametime})
}
```

### Anti-Patterns to Avoid
- **`time.Sleep(50ms)` as the loop driver:** `Sleep` guarantees *at least* the duration, never exactly; jitter and scheduler latency accumulate into TPS drift and a long-term clock that runs slow. Use `time.Ticker` (self-correcting) + an accumulator measured by monotonic `time.Now()` deltas `[VERIFIED: pkg.go.dev/time#Ticker "adjusts the intervals or drops ticks to make up for slow receivers"]`.
- **`gametime++` per loop wake instead of per consumed step:** accelerates the world; breaks TICK-02. Increment strictly inside `for acc >= step`.
- **Unbounded catch-up:** without the 250 ms clamp, a GC pause or laggy host triggers the spiral of death — each catch-up batch takes longer than it simulates, the accumulator grows unboundedly, the server freezes `[VERIFIED: gafferongames.com/post/fix_your_timestep "spiral of death"; clamp `frameTime > 0.25`]`.
- **Touching game state from a network or async goroutine:** violates TICK-05; `-race` will (eventually, under load/`-count`) catch it. Everything crosses the boundary as a channel message. The Phase-2 race bug (send-on-closed-channel surfacing only at `-count>=5`) is the cautionary tale `[VERIFIED: 02-01-SUMMARY.md Issues]`.
- **Driving Keep Alive off the game tick:** couples liveness to simulation; a stalled tick → mass 20 s disconnects. Keep it on its own goroutine/timer `[VERIFIED: server/keepalive.go independent Run(ctx)]`.
- **Adding xsync/ants/conc now:** premature; there is no concurrency to optimize. A plain map owned by one goroutine is correct and faster `[VERIFIED: go.mod has none; CLAUDE.md "Do NOT introduce ants/xsync yet"]`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Periodic wake / interval timing | A hand-rolled `time.Now()` busy-spin or `Sleep`-loop | `time.Ticker` | Self-correcting interval; drops/adjusts ticks for slow receivers so it doesn't drift or back up `[VERIFIED: pkg.go.dev/time#Ticker]` |
| Elapsed-time measurement (MSPT, accumulator delta) | Wall-clock subtraction that can go negative on NTP step | `time.Now()` differences | Go embeds a **monotonic** reading in `time.Now()`; `t2.Sub(t1)` uses it and is immune to wall-clock jumps `[VERIFIED: pkg.go.dev/time "Monotonic Clocks"]` |
| Fixed-timestep accumulator | A novel timing scheme | The canonical accumulator + clamp loop | Battle-tested across every game engine; the spiral-of-death clamp is the one non-obvious part and it's documented `[VERIFIED: gafferongames.com/post/fix_your_timestep]` |
| Keep-alive ping/timeout bookkeeping | New ping/timeout list + timers | Fork's `server.KeepAlive` | Already implemented with two intrusive lists + two timers, vanilla 15 s/30 s, on its own goroutine `[VERIFIED: server/keepalive.go]` |
| Network→tick handoff | A new queue type or shared buffer | The Phase-2 `chan Intent` + `Client` API | Already proven `-race` clean; changing it would break the NET-05 invariant `[VERIFIED: server/client.go, 02-01-SUMMARY.md]` |
| Concurrent collections for state | xsync maps now | A plain `map`/slice owned by the tick goroutine | Single-owner ⇒ no concurrency ⇒ no need for concurrent structures (and they're slower for single-owner access) `[VERIFIED: CLAUDE.md Stack Patterns]` |

**Key insight:** In a single-owner tick architecture, the *only* genuinely hard-to-get-right primitive is the **timing loop** (accumulator + spiral clamp + monotonic measurement) — and that is exactly what stdlib `time` + the canonical accumulator already solve. Everything else in Phase 3 is wiring existing fork components and establishing discipline, not building machinery.

## Common Pitfalls

### Pitfall 1: Game-Time Drift / World Acceleration
**What goes wrong:** The in-game day is not 20 minutes; crops/redstone/weather run fast or slow.
**Why it happens:** `gametime` advanced per loop wake (or per real-time elapsed) instead of exactly once per consumed 50 ms accumulator step. Minecraft time is tick-count-defined, so any deviation from 20 logical ticks/sec changes day length `[VERIFIED: minecraft.wiki/w/Commands/time]`.
**How to avoid:** Increment `gametime` only inside `for acc >= step`, once per step. Never tie it to `time.Now()` directly.
**Warning signs:** `/time query daytime` cycles faster/slower than 20 min wall-clock; MSPT-vs-TPS math doesn't reconcile.
**Verification:** unit test — feed the accumulator a synthetic 60 s of frames in chunks and assert `gametime` advanced exactly 1200; integration — measure a full daylight cycle against a 20-min wall-clock budget.

### Pitfall 2: Spiral of Death
**What goes wrong:** Under a GC pause or host hiccup, the server falls progressively further behind and eventually freezes/disconnects everyone.
**Why it happens:** Unbounded catch-up — each catch-up batch simulates less time than it costs, so the accumulator grows without bound `[VERIFIED: gafferongames.com/post/fix_your_timestep]`.
**How to avoid:** Clamp `frame` to a max (e.g. 250 ms) before adding to the accumulator. The world appears to slow under sustained overload (TPS < 20) instead of spiraling — strictly better behavior.
**Warning signs:** MSPT climbing monotonically; catch-up loop iterating many times per wake.
**Verification:** inject an artificial 2 s stall in a test tick and assert the loop recovers (accumulator bounded, no runaway catch-up); assert MSPT/TPS telemetry reflects the slowdown rather than the process hanging.

### Pitfall 3: `time.Sleep` Jitter → Long-Term Clock Drift
**What goes wrong:** TPS hovers below 20 even when the host is idle; the day slowly desyncs.
**Why it happens:** `time.Sleep(50ms)` sleeps *at least* 50 ms; OS scheduler latency adds a few ms each iteration, and without correction those add up.
**How to avoid:** Drive the loop with `time.Ticker` (self-correcting) and let the accumulator absorb jitter; measure real elapsed with monotonic `time.Now()` deltas, not by assuming each iteration was exactly 50 ms.
**Warning signs:** steady-state TPS like 19.3 on an unloaded server.
**Verification:** run the loop idle for 60 s, assert measured TPS ≈ 20.0 ± small epsilon and `gametime` == 1200.

### Pitfall 4: State Escaping the Owner Goroutine
**What goes wrong:** Intermittent corruption / `-race` failures, often only under load or higher `-count`.
**Why it happens:** A pointer into game state was shared with a network or (future) async goroutine instead of being passed as an immutable message.
**How to avoid:** Enforce that the network→tick boundary carries only `Intent` (no game-state pointer — already true) and that any future async boundary carries immutable snapshots in / immutable results out, applied only in `applyAsyncResults`.
**Warning signs:** the urge to add a mutex to a game-state field — that's the smell that ownership has been violated.
**Verification:** `go test -race -count=10` in `golang:1.26` Docker (the host can't run `-race` natively — no C compiler, `CGO_ENABLED=0`) `[VERIFIED: 02-01-SUMMARY.md]`. A correctly single-owned core is race-clean by construction.

### Pitfall 5: Keep-Alive Coupled to the Tick (Mystery 20s Disconnects)
**What goes wrong:** All players drop ~20–30 s after a tick stall.
**Why it happens:** keep-alive ping/timeout driven off the game tick; when the tick stalls, pings stop and the timeout fires `[VERIFIED: PaperMC/Paper#1157]`.
**How to avoid:** Run `server.KeepAlive.Run(ctx)` on its own goroutine with its own timers (already the design). Feed it `ClientJoin`/`ClientTick`/`ClientLeft` events; never gate its timers on tick cadence.
**Warning signs:** disconnect spikes correlated with MSPT spikes.
**Verification:** stall the tick goroutine for 5 s in a test and assert no keep-alive timeout fires (pings continued on the independent timer).

## Runtime State Inventory

Phase 3 is **greenfield** for game state (no rename/refactor/migration). The only pre-existing runtime artifacts it *consumes* are the Phase-2 seam (`chan Intent`, `Client`) and the fork `KeepAlive` component — both reused unchanged, no migration. **None** — verified by reading `server/client.go`, `server/keepalive.go`, `cmd/sulfur/main.go`: there is no stored/registered tick state today (the current path is `stubGamePlay` sending a Disconnect; no tick exists yet). This section is otherwise omitted as not applicable.

## Code Examples

### Consuming the Phase-2 `chan Intent` in the drain phase
```go
// Source: server/client.go Intent/stubTickConsumer seam (VERIFIED on disk)
// Replaces stubTickConsumer's body; the channel TYPE (chan Intent) is unchanged.
func (t *TickLoop) drainInbound(inbound <-chan Intent) {
    // Non-blocking: take whatever is queued this wake, never park the tick.
    for {
        select {
        case it, ok := <-inbound:
            if !ok {
                return // channel closed: server shutting down
            }
            t.dispatch(it.Client, it.Packet) // decode + route on the OWNER goroutine
        default:
            return // nothing left to drain right now
        }
    }
}
```

### The no-op async rejoin seam (shaped for Phase 8)
```go
// Source: ROADMAP Phase-8 OPT-01/02/03 "rejoin via the apply-async seam" (VERIFIED REQUIREMENTS.md)
type asyncResult interface{ applyTo(*TickLoop) } // Phase 8 defines concrete results

type TickLoop struct {
    // ... owned game state ...
    asyncIn <-chan asyncResult // nil today; Phase 8 wires worker pools to it
}

func (t *TickLoop) applyAsyncResults() {
    if t.asyncIn == nil {
        return // Phase 3: genuinely nothing to do — the seam just exists
    }
    for {
        select {
        case r := <-t.asyncIn:
            r.applyTo(t) // applied by the owner; worker only computed an immutable result
        default:
            return
        }
    }
}
```

### Tick-loop skeleton wired into the real GamePlay (replacing stubGamePlay)
```go
// Source: cmd/sulfur/main.go stubGamePlay + server.Client.Start seam (VERIFIED on disk)
type gameTick struct {
    inbound chan Intent
    loop    *TickLoop
    keep    *server.KeepAlive
}

func (g *gameTick) AcceptPlayer(name string, id uuid.UUID, pk *user.PublicKey,
    props []user.Property, protocol int32, conn *net.Conn) {

    c := server.NewClient(conn, outboundCap)
    c.Start(g.inbound)         // Phase-2 API, unchanged: one writeLoop + one readLoop
    g.keep.ClientJoin(asKeepAliveClient(c)) // TICK-04 independent timer
    // register player with the tick goroutine via a join Intent/channel; block until disconnect.
}

// main(): start the tick + keepalive goroutines, then srv.GamePlay = &gameTick{...}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Fixed-rate-only tick (input collapsed to tick boundary) | Subtick: timestamp inputs, resolve chronologically within the tick | CS2 (2023); MC added `ServerboundClientTickEnd` 1.21.2 (2024) | Movement/hit-reg precision without raising broadcast/sim rate — exactly the TICK-03 model `[VERIFIED: CS2 subtick sources; packetid.go]` |
| World tick == player movement (frozen world = frozen player) | `/tick freeze` keeps players moving while world stops | MC 1.20.4 `/tick` command (2023) | Vanilla precedent that input-responsiveness is decoupled from world-sim cadence `[VERIFIED: minecraft.wiki/w/Commands/tick]` |
| `time.Sleep` game loops | `time.Ticker` + monotonic-delta accumulator + spiral clamp | Long-settled | Drift-free, freeze-resistant timing `[VERIFIED: gafferongames; pkg.go.dev/time]` |

**Deprecated/outdated:** none relevant — the patterns here are stable. The only currency risk is protocol-shape (776 `ServerboundClientTickEnd` payload), which is jar-authoritative and confirmed present in the generated `packetid` table; its full *use* is a Phase-6 concern, so Phase 3 needs only the ID, already generated.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | A ~5 ms ticker wake rate is a good default for the accumulator (fine-grained input, low overhead). | Pattern 1 | Low — the logical tick is wake-rate-independent by construction (Pattern 3); a wrong wake constant only affects input granularity/CPU, never game-time. Tunable. |
| A2 | The 250 ms spiral clamp is an appropriate backlog bound. | Pattern 1 / Pitfall 2 | Low — any finite clamp prevents the spiral; the exact value only tunes how much catch-up is attempted before declaring slowdown. Paper uses similar-order bounds. |
| A3 | `ServerboundClientTickEnd` in 776 marks the client input-batch boundary as in 1.21.2 (payload unverified against the 776 jar in this session). | Pattern 4 | Medium — only matters when Phase 3 actually *reads* the packet. The minimal seam needs only the buffer + chronological drain; reading the packet body can be jar-verified in Phase 6. The ID's existence is verified `[packetid.go:242]`. |

## Open Questions

1. **Exact `ServerboundClientTickEnd` payload in 776**
   - What we know: the packet exists in the generated 776 IDs and was introduced 1.21.2 to delimit client input batches.
   - What's unclear: its precise field layout under 776 (not capture-diffed this session).
   - Recommendation: Phase 3 does not need the payload — build the buffer+drain seam against arrival timestamps and treat the packet as a boundary marker if read at all. Defer payload verification to Phase 6 (ENT-02 movement), where it's load-bearing, and jar/capture-diff it then.

2. **How aggressively to broadcast subtick precision in Phase 3**
   - What we know: clients are updated at the 20/s flush rate; subtick precision is server-side resolution, not a higher broadcast rate.
   - What's unclear: whether any subtick state needs surfacing before Phase 6 entities exist.
   - Recommendation: Phase 3 ships the *capture + ordered-resolution seam* and a stub `applyInput`; real movement/projectile resolution is Phase 6. This satisfies TICK-03's contract (µs capture, chronological resolution, vanilla broadcast rate) without building physics early.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | building/running the tick loop | ✓ | 1.26.1 (windows/amd64) | — |
| Docker (`golang:1.26`) | `-race` gate (host has no C compiler, CGO_ENABLED=0) | ✓ | 29 | none needed — Docker is the sanctioned race path `[VERIFIED: 02-01-SUMMARY.md]` |
| C compiler on host | native `-race` | ✗ | — | Run `-race` in `golang:1.26` Docker (established in Phase 2) |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** native `-race` → use the Docker race-gate (already the project standard).

## Validation Architecture

> `.planning/config.json` was not present in the repo at research time; treating `nyquist_validation` as enabled (default). The project already runs `go test` natively and `-race` in Docker `[VERIFIED: 02-01-SUMMARY.md]`.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` |
| Config file | none (Go convention: `*_test.go`) |
| Quick run command | `go test ./server/...` |
| Full suite command | `go test ./...` then (race) `docker run --rm -v "$PWD":/src -w /src golang:1.26 go test ./server/... -race -count=1` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| TICK-01 | Ordered phases execute in fixed order each tick; `applyAsyncResults` present as no-op | unit | `go test ./server/ -run TestTickPhaseOrder` | ❌ Wave 0 |
| TICK-02 | 60 s of synthetic frames → exactly 1200 `gametime` increments; day ≈ 20 min | unit | `go test ./server/ -run TestGameTimeAnchor` | ❌ Wave 0 |
| TICK-03 | µs-stamped inputs drained in chronological order; broadcast still at 20/s | unit | `go test ./server/ -run TestSubtickOrdering` | ❌ Wave 0 |
| TICK-04 | Tick stalled 5 s → no keep-alive timeout (independent timer) | unit | `go test ./server/ -run TestKeepAliveIndependent` | ❌ Wave 0 |
| TICK-05 | No game-state pointer crosses the network/async boundary; race-clean | race | `docker ... go test ./server/... -race -count=10` | ⚠ harness exists (Phase 2), tick tests ❌ Wave 0 |
| TICK-06 | MSPT/TPS snapshot published and reflects an injected stall | unit | `go test ./server/ -run TestMSPTObservable` | ❌ Wave 0 |
| (spiral) | Injected 2 s stall → accumulator bounded, loop recovers | unit | `go test ./server/ -run TestSpiralClamp` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./server/...`
- **Per wave merge:** `go test ./...` + Docker `-race` on `./server/...`
- **Phase gate:** full suite green + `-race` clean before `/gsd-verify-work`

### Wave 0 Gaps
- [ ] `server/tick_test.go` — covers TICK-01, TICK-02, TICK-06, spiral clamp (drive the loop with synthetic monotonic frames; make the clock injectable so tests don't wait real time)
- [ ] `server/subtick_test.go` — covers TICK-03 (chronological drain ordering)
- [ ] `server/keepalive_independent_test.go` — covers TICK-04 (stall-the-tick, assert no timeout)
- [ ] Tick `-race` test reusing the Phase-2 `net.Pipe` harness — covers TICK-05
- [ ] **Injectable clock:** the loop must take a `now func() time.Time` (or a clock interface) so timing tests are deterministic and fast. Design this in from task 1, not retrofitted.

*(Framework already present — no install needed.)*

## Security Domain

> `security_enforcement` config not found at research time (treated as enabled). Phase 3 is an internal game-loop subsystem with **no new external attack surface** — it consumes already-authenticated, already-decoded `Intent` values from the Phase-2 gate. The relevant controls are availability/DoS-shaped, not auth/crypto.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Handled in Phase 2 login gate; tick sees post-login connections only |
| V3 Session Management | partial | Keep-alive liveness + idempotent `Client.Close` already enforce session teardown `[VERIFIED: server/client.go, keepalive.go]` |
| V4 Access Control | no | No multi-tenant authz inside the tick |
| V5 Input Validation | yes | Inbound packets are still attacker-controlled: dispatch must validate packet IDs/state before mutating, and a malformed/over-frequency input must not stall the tick |
| V6 Cryptography | no | No crypto in the tick (online-mode encryption is Phase 9) |

### Known Threat Patterns for the tick loop
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Inbound flood saturating the drain phase / growing the input buffer | Denial of Service | Non-blocking bounded drain (take what's queued, don't park); rely on Phase-2 bounded outbound + drop-and-disconnect; cap per-player subtick buffer size `[VERIFIED: server/client.go bounded queue]` |
| Single slow/malformed handler stalling the whole tick | Denial of Service | Keep per-packet dispatch cheap and total; never block on IO inside the tick; spiral clamp ensures one bad tick degrades to slowdown, not freeze |
| Keep-alive spoof / liveness evasion | Spoofing | Server-generated incrementing keep-alive IDs, validated on return (already in `KeepAlive`) `[VERIFIED: server/keepalive.go keepAliveID]` |
| Time-source manipulation (NTP step) skewing the loop | Tampering | Monotonic `time.Now()` deltas — immune to wall-clock jumps `[VERIFIED: pkg.go.dev/time]` |

## Sources

### Primary (HIGH confidence)
- `server/client.go`, `server/keepalive.go`, `server/gameplay.go`, `server/server.go`, `cmd/sulfur/main.go`, `data/packetid/packetid.go`, `go.mod` — read on disk (the Phase-2 seam, KeepAlive component, packet IDs, dependency set)
- `.planning/REQUIREMENTS.md`, `.planning/PROJECT.md`, `.planning/ROADMAP.md`, `.planning/STATE.md`, `02-01-SUMMARY.md` — locked decisions and seam contracts
- `pkg.go.dev/time` — `time.Ticker` self-correction; monotonic clock semantics
- minecraft.wiki `/w/Commands/time` — game-time is a tick counter (24000 ticks/day, gametime = world age in ticks)
- minecraft.wiki `/w/Commands/tick` — `/tick freeze`/`sprint`/`step`: vanilla world-sim vs. player-movement decoupling
- minecraft.wiki `/w/Java_Edition_protocol/Packets` — `ServerboundClientTickEnd` existence/role
- gafferongames.com `/post/fix_your_timestep` — accumulator + spiral-of-death clamp + the 0.25 s frame clamp

### Secondary (MEDIUM confidence)
- minestom.net docs (schedulers / threading) — 20 TPS = 50 ms/tick; tick processing stages (`processTick`/`processTickEnd`); confirms 20-TPS-as-default rationale
- turboboost.gg / steamcommunity CS2 discussions / skin.club — CS2 subtick: µs input timestamps resolved chronologically at a fixed rate
- github.com/PaperMC/Paper#1157 — keep-alive timeouts tied to main-thread/tick stalls (motivates the independent timer)
- minecraft keepalive interval (15 s) corroboration

### Tertiary (LOW confidence)
- (none — all load-bearing claims are tied to a primary source or in-repo code)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — pure stdlib + in-repo fork APIs verified on disk; zero new deps confirmed against `go.mod`.
- Architecture (single-owner loop, ordered phases, async seam): HIGH — pattern is settled across Paper/Folia/Minestom and matches the project's own locked decisions; the seam shape is dictated by REQUIREMENTS/ROADMAP.
- Game-time anchoring: HIGH — Minecraft tick-counter time model verified; accumulator decoupling is the standard solution.
- Subtick layer: HIGH on the *model* and *minimal Phase-3 seam*; MEDIUM on the exact 776 `ServerboundClientTickEnd` payload (deferred to Phase 6, ID verified).
- Keep Alive: HIGH — reusing an existing, already-correct independent-timer component with vanilla constants.
- Pitfalls: HIGH — each tied to a primary source and to a concrete verification test.

**Research date:** 2026-06-23
**Valid until:** 2026-07-23 (stable patterns; the only currency-sensitive item is the 776 `ServerboundClientTickEnd` payload, which is jar-authoritative and only load-bearing in Phase 6)
