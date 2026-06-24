---
phase: 04-world-chunk-system
plan: 02
subsystem: world
tags: [world, chunk, generator, superflat, worker, singleflight, packet, light]
requires:
  - "level.EmptyChunk / Chunk.WriteTo (post-04-01: 2 shorts + 3 CLIENT heightmaps)"
  - "level/block.ToStateID, level/biome.Type"
  - "save.Chunk.Load, save/region.Region, level.ChunkFromSave"
  - "data/packetid (ClientboundLevelChunkWithLight/SetChunkCacheCenter/Radius/ChunkBatchStart/Finished)"
  - "golang.org/x/sync/singleflight"
provides:
  - "world.Superflat (deterministic WORLD-04 generator; section count is a parameter)"
  - "world.ChunkManager (tick-owned Empty/Loading/Ready map; Get/State/MarkLoading/Insert/Remove/MarkEmpty)"
  - "world.Worker + world.ChunkResult (off-tick load-or-generate, singleflight dedup, immutable handoff)"
  - "world.WriteLevelChunkWithLight + SetChunkCacheCenter/Radius + ChunkBatchStart/ChunkBatchFinished"
affects:
  - "Plan 04-03 (server streamer): wires TickLoop -> Worker.Request, drains Worker.Results() in applyAsyncResults, calls ChunkManager.Insert/MarkEmpty, emits the cache/batch/chunk packets"
tech-stack:
  added:
    - "golang.org/x/sync v0.21.0 (singleflight only)"
  patterns:
    - "off-tick worker emits an immutable ChunkResult; the tick is the sole mutator (single-owner discipline, same as the players map)"
    - "singleflight.Group.Do dedups concurrent same-key chunk generation"
    - "deterministic, position-independent superflat fill (no RNG)"
key-files:
  created:
    - world/generator.go
    - world/manager.go
    - world/worker.go
    - world/packet.go
    - world/generator_test.go
    - world/manager_test.go
    - world/worker_test.go
    - world/packet_test.go
  modified:
    - go.mod
    - go.sum
decisions:
  - "Worker dispatches each request's load in its own short-lived goroutine (reader stays a single owner of the requests channel) so two concurrent same-key loads actually meet inside singleflight.Do — required to satisfy the dedup acceptance criterion with a serial reader."
  - "Heightmap value convention: WorldSurface/MotionBlocking[NoLeaves] = (SurfaceY - MinY) + 1 for all 256 columns (height above the world floor of the first block above the solid top)."
  - "SurfaceY default in tests = MinY + 4*16 - 1 (4 sections of solid fill); the caller (Plan 04-03) supplies the real value derived from the dimension type."
  - "MarkEmpty is implemented as delete(holder) (identical to Remove) but named for the W1 error-retry path so 04-03's applyAsyncResults error branch un-strands a Loading holder."
metrics:
  duration: ~6m
  completed: 2026-06-24
---

# Phase 4 Plan 02: World Package — Superflat Generator, Chunk Manager, Off-Tick Worker, Chunk Packet Assembly Summary

Built the server-side `world/` package as pure, tick-decoupled code: a deterministic superflat generator (WORLD-04), a tick-owned chunk manager, an off-tick singleflight-deduped load-or-generate worker emitting immutable `ChunkResult`s (WORLD-01 worker side), and the `ClientboundLevelChunkWithLight` assembly plus the cache/batch builders (WORLD-02 full + WORLD-03 light). Proven `-race` clean in golang:1.26 Docker.

## What Was Built

### Task 1 — Superflat generator + ChunkManager (WORLD-04)
- `world/generator.go`: `Generator` interface + `Superflat`. `Generate` fills every column identically (bedrock at MinY, stone to SurfaceY, air above), sets `FluidCount=0` and a full 2048-byte SkyLight on every section, plains biome on the 4×4×4 biome grid, the 3 CLIENT heightmaps, and `Status=StatusFull`. Pure/deterministic — no RNG. Section count (`Secs`) is a constructor parameter, derived by the caller from `dimType.Height/16`, never hard-coded.
- `world/manager.go`: `ChunkManager` — a plain `map[level.ChunkPos]*holder` with `stateEmpty/stateLoading/stateReady`; `Get/State/MarkLoading/Insert/Remove` plus **`MarkEmpty`** (W1 error-retry). Not goroutine-safe by design (tick-owned).

### Task 2 — Off-tick Worker + ChunkResult (WORLD-01 worker side)
- `world/worker.go`: `ChunkResult{Pos, Chunk, Err}` (immutable handoff) and `Worker`. A single reader goroutine owns the bounded `requests` channel and dispatches each load in its own goroutine through `singleflight.Group.Do`, so concurrent same-key requests collapse to one `Generate`. `loadOrGenerate` tries the region (`region.Open/ReadSector` → `save.Chunk.Load` → `level.ChunkFromSave`) when `regionDir != ""`; `ErrNoSector/ErrNoData`/missing file fall through to generation; `ErrSectorNegativeLength/ErrTooLarge`/IO errors surface as `ChunkResult.Err` (no silent regenerate over corruption). `region.Region` is opened/closed per call (Not MT-Safe). Added `golang.org/x/sync v0.21.0`.

### Task 3 — Packet assembly (WORLD-02 full + WORLD-03 light)
- `world/packet.go`: `WriteLevelChunkWithLight(cx, cz, ch)` writes `Int x, Int z` then `level.Chunk.WriteTo` (3 CLIENT heightmaps + section blob with the two shorts + block entities + light) into a `ClientboundLevelChunkWithLight` packet — the chunk body is reused wholesale. Plus `SetChunkCacheCenter` (VarInt x, VarInt z), `SetChunkCacheRadius` (VarInt), `ChunkBatchStart` (no fields), `ChunkBatchFinished` (VarInt batch size). Uses `packetid.*` symbols only.

## Interfaces for Plan 04-03 (the streamer rejoin)

```go
// generator
type Generator interface{ Generate(pos level.ChunkPos) *level.Chunk }
func NewSuperflat(secs, minY, surfaceY int) *Superflat

// manager (tick-owned; not goroutine-safe)
func NewChunkManager() *ChunkManager
func (m *ChunkManager) Get(pos level.ChunkPos) (*level.Chunk, bool)
func (m *ChunkManager) State(pos level.ChunkPos) loadState        // stateEmpty/stateLoading/stateReady
func (m *ChunkManager) MarkLoading(pos level.ChunkPos)            // Empty -> Loading
func (m *ChunkManager) Insert(pos level.ChunkPos, ch *level.Chunk) // Loading -> Ready  (rejoin)
func (m *ChunkManager) MarkEmpty(pos level.ChunkPos)              // Loading -> Empty   (W1 error retry)
func (m *ChunkManager) Remove(pos level.ChunkPos)                 // unload

// worker (off-tick)
type ChunkResult struct{ Pos level.ChunkPos; Chunk *level.Chunk; Err error } // IMMUTABLE
func NewWorker(gen Generator, regionDir string, buf int) *Worker
func (w *Worker) Results() <-chan ChunkResult
func (w *Worker) Request(pos level.ChunkPos)   // non-blocking; drops if the bounded queue is full
func (w *Worker) Run(ctx context.Context)       // launch in a goroutine

// packet builders
func WriteLevelChunkWithLight(cx, cz int32, ch *level.Chunk) (pk.Packet, error)
func SetChunkCacheCenter(cx, cz int32) pk.Packet
func SetChunkCacheRadius(r int32) pk.Packet
func ChunkBatchStart() pk.Packet
func ChunkBatchFinished(n int32) pk.Packet
```

**Wiring contract for 04-03 `applyAsyncResults`:** on a tick, for each `res` drained from `Worker.Results()`: if `res.Err != nil` call `mgr.MarkEmpty(res.Pos)` (retry next tick); else `mgr.Insert(res.Pos, res.Chunk)`. The tick is the ONLY caller of the manager mutators — never the worker. The chunk-data+light packet entry point is `WriteLevelChunkWithLight`, bracketed by `ChunkBatchStart`/`ChunkBatchFinished` with `SetChunkCacheCenter`/`SetChunkCacheRadius` sent on view changes.

## Gate Results

| Gate | Result |
|------|--------|
| `go build ./...` | clean |
| `go vet ./world/...` | clean |
| `go test ./world/ -count=1` | PASS (8 tests) |
| Docker `-race` (golang:1.26, `go test -race ./world/...`) | **clean — single-owner + worker handoff proven race-free** |
| `grep golang.org/x/sync go.mod` | present (v0.21.0) |
| no xsync/ants/conc | confirmed absent |

Tests: `TestSuperflatSectionCount`, `TestSuperflatDeterministic`, `TestChunkManagerLifecycle`, `TestChunkManagerMarkEmpty`, `TestLevelChunkPacketAssembly`, `TestCachePackets`, `TestWorkerEmitsResult`, **`TestWorkerSingleflightDedup`** (asserts exactly ONE `Generate` call for two concurrent same-key requests).

## Correctness Checks (from the plan)

- Section count derived from Height/16, not hard-coded — `Secs` is a constructor parameter; tests use 24 but the type never assumes it.
- Only the immutable `ChunkResult` crosses worker→tick; the tick is the sole map mutator (proven by the Docker `-race` gate).
- `singleflight` dedups concurrent gen of the same pos (`TestWorkerSingleflightDedup`).
- `MarkEmpty`/`Remove`-on-error exists so a gen error doesn't strand the holder (W1).
- No xsync/ants/conc introduced.

## Deviations from Plan

### Auto-fixed / necessary design choices

**1. [Rule 3 - Blocking issue] Worker dispatches loads in per-request goroutines.**
- **Found during:** Task 2 (RED).
- **Issue:** The plan sketch calls `sf.Do` inline in a single serial `Run` loop, but a serial reader can never have two same-key loads in flight simultaneously, so `singleflight` would never dedup — `TestWorkerSingleflightDedup` (a hard acceptance criterion) could not pass.
- **Fix:** `Run` stays the single owner/reader of the bounded `requests` channel but dispatches each load via `go w.handle(...)`, so concurrent same-key loads meet inside `singleflight.Do`. Concurrency is still bounded by the request-channel cap and singleflight collapse; the immutable-handoff contract is unchanged (proven `-race` clean).
- **Files:** world/worker.go.
- **Commit:** 405b4938.

**2. [Rule 3 - Blocking issue] `go` directive bumped 1.22 -> 1.25.0 by `go get`/`go mod tidy`.**
- **Found during:** Task 2.
- **Issue:** `go get golang.org/x/sync@latest` and `go mod tidy` raised `go 1.22` to `go 1.25.0` (the minimum x/sync v0.21.0 declares). I attempted to restore `go 1.22` (build still passed), but `go mod tidy` re-raised it, so it is authoritative.
- **Resolution:** Left at `go 1.25.0`. The installed/CI toolchain is Go 1.26.1 (a strict superset per CLAUDE.md), and the Docker `-race` gate runs golang:1.26 successfully — no compatibility impact.
- **Files:** go.mod, go.sum.
- **Commit:** 405b4938.

### Test-shape adjustment (not a behavior deviation)

**3. Packet light assertion targets the source chunk, not the round-trip.**
- **Found during:** Task 3 (GREEN).
- **Issue:** `level.Chunk.WriteTo` correctly serializes the full 2048-byte SkyLight per section, but `level.Chunk.ReadFrom` (fork code) decodes the light masks/arrays off the wire and discards them — it does not re-attach them to `Section.SkyLight`. The initial test asserted recovered-section light, which the fork decoder cannot provide.
- **Fix:** The test asserts the full 2048-byte SkyLight on the SOURCE chunk (the bytes that actually go on the wire) AND that the packet body fully drains after `Int x, Int z, Chunk.ReadFrom` (proving the light tail was both written and read). This is the accurate, verifiable WORLD-03 assertion; re-attaching light on decode is a server-side read concern out of scope for this plan (the server generates, it does not read its own light back). Logged as the only out-of-scope observation.
- **Files:** world/packet_test.go.
- **Commit:** 7ecbb60a.

## Out-of-Scope Observation (not fixed — logged)

`level.Chunk.ReadFrom` reads the light section off the wire but does not populate `Section.SkyLight/BlockLight` (the anonymous `lightData` in its Tuple is dropped). This is harmless for the server (which writes, never reads, its own chunk light) and pre-exists this plan. If a future phase needs to deserialize chunk light (e.g. a proxy/relay), `Chunk.ReadFrom` would need to re-attach the decoded arrays. Not a blocker for WORLD-01/02/03/04.

## Self-Check: PASSED
- world/generator.go, world/manager.go, world/worker.go, world/packet.go and their 4 test files: FOUND
- Commits 79132226, 49398c53, 29c97f0e, 405b4938, 1ee6f2fc, 7ecbb60a: FOUND
- Docker `-race` on ./world/...: clean
- `golang.org/x/sync` in go.mod; no xsync/ants/conc: confirmed
