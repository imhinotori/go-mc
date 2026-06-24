---
phase: 04-world-chunk-system
plan: 03
subsystem: server
tags: [world, chunk, streaming, tick, rejoin, view-distance, center-out, batch, single-owner]
requires:
  - "Phase-3 tick seam: asyncResult interface, asyncIn channel, applyAsyncResults (no-op when nil), tickChunks/flushOutbound stubs, TestTickPhaseOrder"
  - "world.ChunkManager (Get/State/IsEmpty/MarkLoading/Insert/MarkEmpty/Len), world.Worker (Request/Results/Run), world.ChunkResult (immutable)"
  - "world.NewSuperflat, world.WriteLevelChunkWithLight, SetChunkCacheCenter/Radius, ChunkBatchStart/Finished"
  - "server/client.go Client.Send (bounded outbound queue; writeLoop sole socket writer)"
  - "level.ChunkPos, level.EmptyChunk"
provides:
  - "server.chunkReady (asyncResult): off-tick chunk rejoin; applyTo inserts the immutable chunk on the owner, MarkEmpty on error (WORLD-01 tick side)"
  - "TickLoop.SetWorld(mgr, worker): wires the worker Results() -> chunkReady adapter onto asyncIn (applyAsyncResults body UNCHANGED)"
  - "tickChunks filled: per-player center-out needed-ring request issue (one worker.Request per Empty column)"
  - "flushOutbound filled: per-player SetChunkCacheCenter+Radius then ChunkBatchStart -> N LevelChunkWithLight (center-out) -> ChunkBatchFinished(N), each column sent once"
  - "centerOutRing / clampViewDistance / serverViewDistance (the DoS clamp) / chunkCenterOf in server/world_stream.go"
  - "world.ChunkManager.IsEmpty (request predicate) + Len (bounded-ring observability)"
affects:
  - "Phase 5 (PLAY-01/03): sets the real spawn center, updates tickPlayer.center on movement (re-arms centerSent), sends the 'start waiting for chunks' Game Event"
  - "Plan 04-04: capture-diff + real-client visual is the authoritative WORLD-02/03/05 byte-correctness proof; this wave makes chunks STREAM"
tech-stack:
  added: []
  patterns:
    - "off-tick worker emits an immutable ChunkResult; an adapter goroutine re-wraps it as chunkReady (touching NO tick state) onto the internal asyncBridge; the tick is the sole mutator of the manager + per-player sent-set (single-owner, -race clean by construction)"
    - "center-out (Chebyshev) ring emitted shell-by-shell (no sort) so the player's own chunk arrives first"
    - "server view-distance clamp as the load-bearing DoS control: needed-ring is (2r+1)^2, bounded by the server, never by the client"
    - "idempotent streaming: tickChunks requests only Empty columns; flushOutbound sends only Ready, not-yet-sent columns — position spam re-walks the same bounded ring with no duplicate work"
key-files:
  created:
    - server/world_stream.go
    - server/world_stream_test.go
  modified:
    - server/tick.go
    - server/tick_phases.go
    - server/gameplay_tick.go
    - cmd/sulfur/main.go
    - world/manager.go
decisions:
  - "Added tickPlayer streaming fields + SetWorld + chunkReady in Task 1 (not deferred to Task 2) so the build stays green at every step — the test file references both tasks, so the package must compile with both present."
  - "Added world.ChunkManager.IsEmpty (exported request predicate) instead of exporting the loadState enum, so the tick decides 'should I request this column' without leaking the Loading/Ready distinction; added Len for the bounded-ring spam test."
  - "Overworld superflat shape wired in main.go: secs=24, minY=-64, surfaceY=-48 (a 16-block-thick lit floor above bedrock) — a stable square the client stands on; real terrain is Phase 9."
  - "serverViewDistance=2 fixed clamp for v1 (research A4: radius 2 suffices to 'stand on solid ground'); clampViewDistance floors to 2 and caps to serverViewDistance so a future client-supplied value cannot enlarge the ring."
  - "asyncBridge buffered at 256; the adapter goroutine ranges Worker.Results() until close and only re-wraps the immutable result, so it adds no race and the worker backpressures rather than growing memory if it ever filled."
metrics:
  duration: ~7m
  completed: 2026-06-24
---

# Phase 4 Plan 03: Off-Tick Chunk Rejoin + Center-Out View-Ring Streaming Summary

Wired the Plan-04-02 `world/` subsystem into the Phase-3 tick spine: the off-tick chunk worker now rejoins the tick through the **unchanged** `applyAsyncResults` seam via a concrete `chunkReady` asyncResult (WORLD-01 tick side), and chunks stream to each player as a server-clamped, center-out neighbor ring with chunk-cache + batch framing (WORLD-05). The rejoin is `-race` clean in golang:1.26 Docker — only the immutable `ChunkResult` crosses the off-tick boundary and the tick is the sole mutator of the manager and the per-player sent-set.

## What Was Built

### Task 1 — chunkReady rejoin + asyncIn wiring (WORLD-01 tick side)
- **`server/tick.go`**: `chunkReady struct{ res world.ChunkResult }` implementing `asyncResult`. `applyTo` runs on the OWNER goroutine inside `applyAsyncResults`: on `res.Err != nil` it calls `t.world.MarkEmpty(res.Pos)` (un-strand the Loading holder so `tickChunks` retries); otherwise `t.world.Insert(res.Pos, res.Chunk)` — the single mutation that crosses the off-tick boundary, never a nil chunk.
- `TickLoop` gained tick-owned `world *world.ChunkManager`, `worker *world.Worker`, and the internal `asyncBridge chan asyncResult`.
- **`SetWorld(mgr, worker)`** stores the manager/worker, makes an `asyncBridge` (buffered 256), assigns it to `asyncIn`, and starts a small adapter goroutine that ranges `worker.Results()` and forwards each as `chunkReady{res}` onto the bridge. **`applyAsyncResults`'s body is UNCHANGED** — it still drains `asyncIn` on the owner; the pipeline order is preserved (`TestTickPhaseOrder` passes).
- **`server/gameplay_tick.go`**: `AcceptPlayer` defaults the new streaming fields at registration (center `{0,0}`, `viewDist = clampViewDistance(serverViewDistance)`, empty `sentChunks`, `secs = overworldSections=24`).
- **`cmd/sulfur/main.go`**: builds `world.NewSuperflat(24, -64, -48)`, `world.NewWorker(gen, "", 256)`, `world.NewChunkManager()`, calls `tick.SetWorld(mgr, worker)` BEFORE `go tick.Run(...)`, and starts `go worker.Run(ctx)`.

### Task 2 — center-out view-ring streaming + clamp (WORLD-05)
- **`server/world_stream.go`** (new): `serverViewDistance = 2` (the fixed v1 clamp), `clampViewDistance(req)` (floors to 2, caps to `serverViewDistance`), `centerOutRing(center, r)` (emits the `(2r+1)^2` Chebyshev square shell-by-shell, center first, no sort), and `chunkCenterOf`/`floorDiv16` (the Phase-5 movement seam, floor-div correct for negative coords).
- **`server/tick_phases.go` `tickChunks`** (filled, `t.trace("tickChunks")` kept first): per player, walks the center-out ring out to the clamped `viewDist`; for each `IsEmpty` column it `MarkLoading` + `worker.Request` — exactly one request per Empty column, never re-requesting Loading/Ready (idempotent over the bounded ring; worker singleflight dedups cross-player overlap).
- **`server/tick_phases.go` `flushOutbound`** (filled, `t.trace("flushOutbound")` kept first): per player, sends `SetChunkCacheCenter` + `SetChunkCacheRadius` once per center (`centerSent`), then collects the center-out ring columns that are `Ready` and not in `sentChunks`; if any, brackets them in `ChunkBatchStart` → N× `WriteLevelChunkWithLight` (center-out) → `ChunkBatchFinished(N)`, adding each to the sent-set so each column is sent at most once. All via the bounded `Client.Send` (writeLoop stays the sole socket writer).
- **`world/manager.go`**: added `IsEmpty(pos)` (request predicate, keeps the `loadState` enum package-private) and `Len()` (bounded-ring observability for the spam test).

## How the rejoin consumes applyAsyncResults

`applyAsyncResults` (Phase 3) is **untouched**: it non-blocking-drains `t.asyncIn` on the owner and calls `r.applyTo(t)`. Plan 04-03 only (a) defines the concrete `chunkReady` asyncResult and (b) makes `asyncIn` non-nil. `SetWorld` bridges the type mismatch — the worker emits `world.ChunkResult`, the adapter goroutine re-wraps each as `chunkReady` onto the internal `asyncBridge` (assigned to `asyncIn`). The adapter touches no tick state, so the only thing crossing the boundary is the immutable result; the insert happens on the owner inside `applyTo`. A loop built without `SetWorld` keeps `asyncIn == nil`, so `applyAsyncResults` stays a pure no-op (Phase-3 contract preserved — `TestApplyAsyncResultsNoop`, `TestRejoinNoopWithoutWorld`).

## View-ring streaming shape

- **Per-player sent-set**: `tickPlayer.sentChunks map[level.ChunkPos]bool` (tick-owned, lazy-init), so each column is streamed to a player at most once; a re-flush of the same center sends nothing new.
- **Center-out order**: `centerOutRing` emits ring shells `d = 0..r` (d=0 is the center, then each Chebyshev shell), so the chunk the player stands in arrives before the surrounding ring — "stand-in chunk plus the surrounding ring before render."
- **Clamp**: `viewDist` is `clampViewDistance(...)` ∈ `[2, serverViewDistance]`; the needed-ring is `(2*viewDist+1)^2 = 25` columns, bounded by the server. A client requesting a huge view, or spamming position updates, can only re-walk the same bounded ring (no OOM, no tick stall).

## Gate Results

| Gate | Result |
|------|--------|
| `go build ./...` | clean (exit 0) |
| `go vet ./server/... ./world/...` | clean |
| `go test ./server/... ./world/... -count=1` (native) | PASS |
| `go test ./... -count=1` (full repo) | PASS (exit 0) |
| **Docker `-race` (golang:1.26, `go test -race ./server/... ./world/...`)** | **clean — off-tick rejoin + streaming proven race-free** |
| `grep 'func (r chunkReady) applyTo' server/tick.go` | present (1) |
| `grep 'WriteLevelChunkWithLight' server/tick_phases.go` | present (1) |
| `TestTickPhaseOrder` | PASS (pipeline order + trace calls preserved) |
| zero new deps (no xsync/ants/conc) | confirmed |

Plan-verify tests (all PASS): `TestChunkReadyRejoin` (real worker, owner-drain insert — under -race), `TestChunkReadyApplyToInserts` (insert on ok / skip + MarkEmpty on err), `TestRejoinNoopWithoutWorld` + `TestApplyAsyncResultsNoop` (nil asyncIn no-op), `TestViewRingCenterOut` (25 chunks, center-first, non-decreasing Chebyshev, no dupes), `TestViewDistanceClamp` (huge→cap, 0/-50→floor 2, ring bounded), `TestTickChunksRequestsRing` (ring Loading + idempotent second pass), `TestPositionSpamBounded` (1000 tickChunks never grow the manager past the bounded ring), `TestFlushOutboundBatches` (exact center/radius/start/N/finished sequence over a real piped client; second flush sends nothing).

## Correctness Checks (from the plan)

- Worker→tick boundary carries only the immutable `ChunkResult`/`chunkReady`; the tick is the sole manager + sent-set mutator — proven by the Docker `-race` gate (`TestChunkReadyRejoin`).
- Error result → `MarkEmpty` → retry next tick (`TestChunkReadyApplyToInserts`).
- View distance clamped to `serverViewDistance`; ring bounded `(2r+1)^2` (`TestViewDistanceClamp`, `TestPositionSpamBounded`).
- `applyAsyncResults` body unchanged; `Run(ctx, inbound)` signature + `Intent` type unchanged; pipeline order preserved (`TestTickPhaseOrder`).
- No xsync/ants/conc; zero new dependencies (a plain tick-owned map + one worker goroutine + one tiny adapter goroutine).

## Deviations from Plan

### Auto-fixed / necessary design choices

**1. [Rule 2 - Missing critical functionality] Added `ChunkManager.IsEmpty` + `Len` to the world package.**
- **Found during:** Task 2 (GREEN).
- **Issue:** `tickChunks` needs to test "is this column unrequested" but `loadState`/`stateEmpty` are unexported in `world`; the `server` package cannot reference them. The spam test needs the manager column count, also not exported.
- **Fix:** Added `IsEmpty(pos) bool` (the request predicate — keeps the `loadState` enum package-private) and `Len() int` (bounded-ring observability). Pure reads over the tick-owned map; no behavior change to existing methods.
- **Files:** world/manager.go.
- **Commit:** 8d7efc90.

**2. [Rule 3 - Build-green ordering] tickPlayer streaming fields + chunkReady + SetWorld all landed in the Task-1 implementation step.**
- **Found during:** Task 1 (GREEN).
- **Issue:** The combined test file (`server/world_stream_test.go`) references both tasks' symbols, so the package cannot compile with only Task-1 code present.
- **Resolution:** Added the tickPlayer streaming fields, `chunkReady`, and `SetWorld` in the Task-1 edit and created `server/world_stream.go` (the ring/clamp helpers) so the package compiles; the per-task commits split the rejoin (Task 1) from the streaming phases + ring helpers (Task 2). The plan explicitly permitted adding the fields in Task 1 "to keep the build green either way." No behavior deviation.
- **Files:** server/tick.go, server/world_stream.go.
- **Commits:** b39b191f (rejoin), 8d7efc90 (streaming).

### Test-shape additions (not behavior deviations)

**3. Added `TestPositionSpamBounded` and `TestChunkReadyApplyToInserts` / `TestRejoinNoopWithoutWorld` beyond the named plan tests.**
- These directly assert the threat-register mitigations (T-4-06 bounded ring under spam; the error-path no-insert / nil-asyncIn no-op) that the plan calls for but did not name as separate tests. They strengthen the `-race` and DoS proofs.
- **Files:** server/world_stream_test.go.
- **Commit:** 274576ea.

## Out-of-Scope (deferred, per the plan)

- The "start waiting for chunks" Game Event and the real spawn center / movement-driven `center` update are PLAY-01/03 (Phase 5) — NOT added here. Phase 4 streams around a default `{0,0}` center so a chunk square exists.
- Byte-correctness of the streamed chunk/light is **not** proven by this wave; the authoritative WORLD-02/03/05 proof is Plan 04-04's capture-diff + real-client visual. This wave proves the ring is in flight with the correct packet sequence/framing and is `-race` clean.

## TDD Gate Compliance

Gate sequence present in git log: `test(04-03)` (274576ea, RED) → `feat(04-03)` rejoin (b39b191f, GREEN) → `feat(04-03)` streaming (8d7efc90, GREEN). No unexpected RED-phase pass (the test file failed to compile until the symbols were implemented).

## Self-Check: PASSED
- server/world_stream.go, server/world_stream_test.go: FOUND (created)
- server/tick.go, server/tick_phases.go, server/gameplay_tick.go, cmd/sulfur/main.go, world/manager.go: FOUND (modified)
- Commits 274576ea, b39b191f, 8d7efc90: FOUND in git log
- Docker `-race` on ./server/... ./world/...: clean
- `func (r chunkReady) applyTo` in server/tick.go and `WriteLevelChunkWithLight` in server/tick_phases.go: present
- zero new deps; applyAsyncResults body + Run signature + Intent unchanged: confirmed
