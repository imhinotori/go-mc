# Phase 10: Worldgen Foundation — LCG, Cross-Chunk Seam & Live Heightmap — Research

**Researched:** 2026-06-24
**Domain:** Minecraft 26.2 (proto 776) worldgen pipeline + Sulfur off-tick chunk worker lifecycle
**Confidence:** HIGH (every constant/algorithm/radius below is `javap -c` confirmed against `temp/cache/26.2-inner.jar`; the worker-seam design is design-inferred but grounded on the exact code in `world/worker.go` and the jar's FEATURES-step dependency gating)

This is the load-bearing-risk foundation phase of v2. GEN2-01 and GEN2-03 are bounded, low-risk ports against code that already has the right shape. GEN2-02 — the cross-chunk worker seam — is the entire architectural cost of the milestone and is what this research resolves concretely. The same seam serves BOTH features (decoration) and structures (the GEN3 track), so it is designed once here.

---

## Summary

Three deliverables, in hard dependency order:

1. **GEN2-01 (LCG)** — Add `LegacyRandomSource` (java.util.Random) + a `WorldgenRandom` wrapper to `world/levelgen/random.go`, alongside the existing Xoroshiro, implementing the SAME `RandomSource` interface. This is ~80 lines of jar-exact bit math. All four primitive algorithms (`nextInt(bound)`, `nextLong`, `nextFloat`, `nextDouble`) DIFFER from Xoroshiro's and are jar-confirmed below — **do not copy the Xoroshiro bodies.**

2. **GEN2-03 (live WG heightmap)** — The 6 heightmap fields (`WorldSurfaceWG`, `OceanFloorWG`, `MotionBlocking`, ...) **already exist** on `level.Chunk.HeightMaps`, and `recomputeWorldSurfaceWG` already builds `WorldSurfaceWG` from post-carve terrain. The new work is a `Heightmap.update(x,y,z,state)` analog (jar-confirmed algorithm below) so worldgen block writes keep the WG heightmaps live, plus building `OCEAN_FLOOR_WG`/`MOTION_BLOCKING` from post-carve terrain before decoration begins.

3. **GEN2-02 (cross-chunk seam)** — Split `Generate(pos)` into a pure single-chunk `GenerateTerrain(pos)` (fill→surface→carve, status `carvers`) and a separate `Decorate(center, neighbors[8])` pass that runs once all 8 neighbors are at ≥ `carvers` status, over a 3×3 read/write proxy. The worker grows a **staging map** of carved-but-not-decorated chunks and a **neighborhood-ready scheduler** that emits the final immutable `ChunkResult` exactly once per chunk after decoration. This preserves every v1 invariant: single-owner handoff, `-race` clean, off-tick, the `applyAsyncResults` rejoin seam.

**Primary recommendation:** Split-`Generate` (GenerateTerrain + Decorate), staging map keyed by packed `int64`, dependency-counter scheduling, 3×3 clipping write proxy, emit-once on decoration completion. This is the only provably-order-independent design and it is exactly vanilla's `FEATURES requires CARVERS@radius1, blockStateWriteRadius=1` gating (jar-confirmed). Build order: **LCG → live heightmap → worker seam.** The seam is built and tested with an EMPTY decoration body first (a no-op `Decorate` that just promotes status), so the risky lifecycle change lands and is proven `-race`/determinism-clean BEFORE any feature logic exists to confuse the picture.

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| LCG RNG (`LegacyRandomSource`/`WorldgenRandom`) | `world/levelgen` (pure algorithm) | — | Same package + interface as the existing Xoroshiro; pure over its seed, no I/O, no tick coupling. |
| Live WG heightmaps + `update` | `level` (data structure on `Chunk`) | `world/levelgen/surface` (builds them) | Heightmaps are chunk-owned state; the `update` primitive belongs next to `BitStorage`/`HeightMaps`. Building them is the generator's job. |
| Cross-chunk decoration lifecycle | `world` (the Worker) | `world` (NoiseGenerator's `Decorate`) | The 3×3 neighborhood + hold-at-status scheduling is a **worker** concern (it owns chunk lifecycle); the generator only exposes `GenerateTerrain`/`Decorate` as pure functions. |
| 3×3 WorldGenLevel proxy | `world` (new `neighborhood` type) | — | A read/write view over 9 `*level.Chunk`; lives with the worker, used only during `Decorate`. |
| Single-owner immutable handoff | `world.Worker` → tick (unchanged `ChunkResult`/`chunkReady`) | `server` (applyAsyncResults) | Invariant preserved verbatim — the tick side does not change at all. |

---

## Standard Stack

Build in this exact order. Each is a hard dependency of the next; each lands and is tested in isolation before the next starts.

### 1. GEN2-01 — `LegacyRandomSource` (LCG) + `WorldgenRandom`

**File:** `world/levelgen/random.go` (append; do NOT touch the Xoroshiro types).

The LCG is a SECOND `RandomSource` implementation. Concrete Go types:

```go
// LegacyRandomSource is the ported java.util.Random LCG (LegacyRandomSource.class).
// It implements the SAME world/levelgen.RandomSource interface as Xoroshiro.
type LegacyRandomSource struct {
    seed uint64 // 48-bit state (held in a uint64, masked to 48 bits on every step)
}

// WorldgenRandom wraps an LCG and adds the worldgen seed-derivation helpers
// (WorldgenRandom.class extends LegacyRandomSource). It embeds *LegacyRandomSource
// so it IS a RandomSource and exposes setDecorationSeed/setFeatureSeed/setLargeFeature*.
type WorldgenRandom struct {
    *LegacyRandomSource
}
```

Constants (jar-confirmed, `LegacyRandomSource`):
```go
const (
    lcgMultiplier = uint64(0x5DEECE66D)      // MULTIPLIER = 25214903917
    lcgIncrement  = uint64(0xB)              // INCREMENT  = 11
    lcgMask       = uint64(1)<<48 - 1        // MODULUS_MASK = 281474976710655 (MODULUS_BITS = 48)
)
```

**Interface match (critical):** the existing `RandomSource` interface (random.go lines 47-64) requires `NextLong() int64`, `NextInt() int32`, `NextIntN(bound int32) int32`, `NextDouble() float64`, `NextFloat() float32`, `ConsumeCount(n int)`, `Fork() RandomSource`, `ForkPositional() PositionalRandomFactory`. The LCG must implement ALL of them so it is drop-in everywhere a `RandomSource` is taken. `NextLong` returns `int64` (Java long); the seed math (`setDecorationSeed` etc.) operates on `int64`.

`WorldgenRandom` wrapper methods (signatures, math in Code Examples):
- `SetDecorationSeed(worldSeed int64, blockX, blockZ int) int64` — per-chunk base seed; returns it.
- `SetFeatureSeed(decoSeed int64, index, step int)` — per-feature seed (pure arithmetic, no draws).
- `SetLargeFeatureSeed(worldSeed int64, chunkX, chunkZ int)` — per-chunk structure piece seed (GEN3 uses it; port now since it shares the wrapper).
- `SetLargeFeatureWithSalt(worldSeed int64, x, z, salt int)` — structure placement seed (GEN3).

> Port all four wrapper methods now even though only `SetDecorationSeed`/`SetFeatureSeed` are exercised by features. They are cheap, share the wrapper, and the structures track (v2-structures.md) depends on the large-feature pair — building them here avoids a second edit to `random.go`.

### 2. GEN2-03 — Live worldgen heightmaps

**Files:** `level/` (the `update` primitive + a `Heightmap` helper) and `world/levelgen/surface/system.go` (build OCEAN_FLOOR_WG/MOTION_BLOCKING pre-decoration).

The 6 heightmap fields **already exist** on `level.Chunk.HeightMaps` (chunk.go lines 373-380) and `WorldSurfaceWG` is already built+read mid-pipeline by `recomputeWorldSurfaceWG` (surface/system.go 573). What's missing:

1. A **`Heightmap.update(x, y, z, opaque bool)`** primitive (jar-confirmed algorithm below). It is the incremental "a block was just written at (x,y,z); fix this column's height" operation features need. Today only the bulk `recompute*`/`writeClientHeightmaps` exist (full top-down rescans); `update` is the O(1)-amortized per-write variant.

2. `OCEAN_FLOOR_WG` and a `MOTION_BLOCKING` (worldgen) built from **post-carve** terrain **before** the first feature. `recomputeWorldSurfaceWG` already does this for `WORLD_SURFACE_WG`; mirror it for the other two WG-relevant types (the predicates differ: WORLD_SURFACE_WG = NOT_AIR; OCEAN_FLOOR_WG = MATERIAL_MOTION_BLOCKING/no-fluid; MOTION_BLOCKING = blocksMotion-or-fluid).

3. The decoration write path (the 3×3 proxy `SetBlock`) calls `update` on the **three worldgen heightmaps the proxy owns** (`WORLD_SURFACE_WG`, `OCEAN_FLOOR_WG`, `MOTION_BLOCKING`) so a later same-step feature sees an earlier tree. The 3 CLIENT heightmaps are still finalized once by `writeClientHeightmaps` after decoration (they go on the wire; their live-ness during decoration is irrelevant).

Concrete primitive (lives in `level`, next to `BitStorage`):
```go
// heightmapUpdate mirrors Heightmap.update: after setting `state` at (lx, y, lz),
// fix the column's stored height. `bs` stores Y relative to minY; `opaque` is the
// per-type predicate result for the NEW state; `opaqueAt(y)` re-tests existing blocks
// when the surface block was removed. Returns the (possibly unchanged) height.
func heightmapUpdate(bs *BitStorage, lx, y, lz, minY int, opaque bool, opaqueAt func(y int) bool) {
    col := lz<<4 | lx
    firstAvail := bs.Get(col) + minY        // current "first Y above surface"
    if y <= firstAvail-2 {                   // below the surface-1 → no change (jar: if y <= getFirstAvailable-2 return false)
        return
    }
    if opaque {
        if y >= firstAvail {
            bs.Set(col, (y+1)-minY)          // new opaque top
        }
        return
    }
    if firstAvail-1 == y {                    // removed the exact surface block → scan down for the new top
        for ny := y - 1; ny >= minY; ny-- {
            if opaqueAt(ny) {
                bs.Set(col, (ny+1)-minY)
                return
            }
        }
        bs.Set(col, 0)                         // nothing opaque below → floor
    }
}
```

### 3. GEN2-02 — The cross-chunk worker seam

**Files:** `world/generator.go` (interface split), `world/noisegen.go` + `world/generator.go` Superflat (the split impl), `world/worker.go` (staging + scheduler), new `world/neighborhood.go` (the 3×3 proxy).

The `Generator` interface gains a two-method shape; `Generate` stays as a convenience that does both for the single-chunk/test path:

```go
type Generator interface {
    // GenerateTerrain produces a chunk at "carvers" status: fill→surface→carve.
    // PURE over (seed, pos). NO cross-chunk writes (the carveChunk footprint guard stays).
    GenerateTerrain(pos level.ChunkPos) *level.Chunk

    // Decorate runs the post-carve decoration pass for `center`, writing into the
    // 3×3 view (center + the 8 neighbors). PURE over (seed, center.Pos) given a
    // fully-carved neighborhood. After Decorate, center is at "full" status.
    // For Phase 10 the body is a NO-OP that only promotes status (features land in v2's
    // feature phase); the SEAM is what Phase 10 delivers and tests.
    Decorate(view *Neighborhood)
}
```

`Generate(pos)` (kept for tests / the single-chunk path) = `GenerateTerrain(pos)` then `Decorate` over a neighborhood whose 8 neighbors are also freshly terrain-generated. The existing `NoiseGenerator.Generate`/`Superflat.Generate` bodies are refactored: everything up to and including carve moves to `GenerateTerrain`; the finish step (sky light, `StatusFull`) moves to the tail of `Decorate`. `SpawnSurfaceY` switches to `GenerateTerrain` (it only needs the carved heightmap, not decoration).

`Worker` grows (see Architecture Patterns for the full diff):
```go
type Worker struct {
    // ... existing gen/regionDir/requests/results/sf ...
    staging  map[int64]*stagedChunk  // carved-but-not-decorated chunks, keyed by packed pos
    // staging is owned by ONE goroutine (the scheduler); never touched concurrently.
}

type stagedChunk struct {
    pos       level.ChunkPos
    chunk     *level.Chunk  // at "carvers" status
    carved    bool          // terrain done
    decorated bool          // decoration done → ready to emit
}
```

---

## Architecture Patterns

### System architecture (data flow through the new lifecycle)

```
 tick streamer ──Request(pos)──▶ Worker.requests (bounded chan, UNCHANGED)
                                        │
                                  Worker.Run reader  (single goroutine, UNCHANGED ownership)
                                        │ go handleTerrain(pos)
                                        ▼
              singleflight.Do(key) ─▶ loadOrGenerate ─▶ gen.GenerateTerrain(pos)
                                        │  (region load path UNCHANGED — a loaded chunk
                                        │   is already "full"; it skips staging, emits directly)
                                        ▼
                         carved *level.Chunk  ──▶ scheduler goroutine (owns `staging`)
                                                        │ stage[key] = {chunk, carved:true}
                                                        │ ensure 8 neighbors requested (auto-request)
                                                        ▼
                                   for each NEWLY-carved key, scan its 3×3:
                                   ── all 9 carved? ──no──▶ wait (neighbor will re-trigger)
                                           │ yes
                                           ▼
                            build Neighborhood(center=key, 9 *level.Chunk)
                            gen.Decorate(view)            ◀── writes clipped to 3×3, heightmaps live
                            mark center.decorated, center.Status = full
                                           ▼
                            emit ChunkResult{center.chunk}  ──▶ results chan (UNCHANGED)
                                           │  (center now owned by the tick; worker drops it
                                           │   from staging — single-owner handoff preserved)
                                           ▼
                            tick: asyncBridge ─▶ chunkReady.applyTo ─▶ ChunkManager.Insert  (UNCHANGED)
```

The tick side (`server/tick.go` `chunkReady`/`applyAsyncResults`/`SetWorld`, and `world/manager.go`) **does not change at all**. The entire seam is internal to `world/worker.go` + the generator split.

### THE worker-lifecycle change (the central GEN2-02 design)

**Vanilla's exact gating (jar-confirmed, `ChunkPyramid.lambda$static$7` = the FEATURES step):**
```
FEATURES step:  addRequirement(STRUCTURE_STARTS, 8)   // GEN3 structures, not yet
                addRequirement(CARVERS, 1)             // ← 8 neighbors must be at ≥ CARVERS
                blockStateWriteRadius(1)               // ← decoration writes into the 3×3
```
So to decorate chunk C, all chunks in the radius-1 (3×3) neighborhood must be at ≥ `carvers` status, and C's decoration may write blocks into that same 3×3. This is precisely the seam.

**Lifecycle (resolving every sub-question from the brief):**

**Q: Split `Generate` or stage in place?** → **Split.** `GenerateTerrain(pos)` (pure, single-chunk, the current fill/surface/carve) + `Decorate(view)` (the neighborhood pass). The split makes the two phases independently testable and makes `Decorate` a pure function of (seed, center, the 8 carved neighbors), which is the determinism argument. (Alternative — keep single-shot + buffer cross-chunk writes — rejected; see Open Decisions.)

**Q: Where do carved-but-not-decorated chunks live?** → A **`staging map[int64]*stagedChunk`** owned by a single scheduler goroutine inside the Worker, keyed by the packed `int64` pos (`int64(pos[0])<<32 | int64(uint32(pos[1]))` — the same packing `chunkKey` already uses, but as the int64 itself, not its string form). Single-owner ⇒ no lock ⇒ `-race` clean by construction, matching the existing manager's discipline.

**Q: How does the worker detect "all 8 neighbors carved" and schedule decoration?** → On each newly-carved chunk arriving at the scheduler:
  1. Store it in `staging` (`carved=true`).
  2. **Auto-request its 8 neighbors** if they are not already staged/requested (a carved chunk pulls its neighborhood into existence — this is what makes a single `Request(C)` eventually decorate C). Guard with a `requested` set so each neighbor is requested once (the bounded `requests` channel + `singleflight` already dedup, but the set avoids spamming).
  3. **Scan the up-to-9 candidate centers** whose neighborhood this new chunk just completed: the new chunk at `(x,z)` is a neighbor of the 9 centers `(x+dx, z+dz)` for `dx,dz ∈ {-1,0,1}`. For each such center that is staged-and-carved, check if ALL 9 of ITS neighbors are staged-and-carved; if so and it is not yet decorated, decorate it.

This is a **scan, not a counter** — simpler and robustly order-independent (a counter requires careful init when chunks arrive before their center exists). The scan is O(9·9)=O(81) per carved chunk, trivial.

**Q: How is the 3×3 WorldGenLevel view built and how does emit-once still work?** → A `Neighborhood` proxy (new `world/neighborhood.go`) holds the center pos + the 9 `*level.Chunk` (a `[3][3]*level.Chunk` or `map[int64]*level.Chunk`). It exposes world-coord `GetBlock`/`SetBlock` that route to the owning chunk and **drop writes outside the 3×3** (exactly like `carveChunk.inFootprint`, but the footprint is now 48×48 instead of 16×16). Its `SetBlock` also calls `heightmapUpdate` on the target chunk's 3 worldgen heightmaps. After `Decorate(view)` returns, ONLY the center is emitted as a `ChunkResult` and removed from staging; the 8 neighbors stay in staging (they will each be decorated when THEIR neighborhood completes, and emitted then). **Each chunk is emitted exactly once — when it is the center of a completed decoration.** A `decorated` flag guards against a second decoration if the scan re-reaches it.

**Q: singleflight interaction?** → `singleflight` stays on the **terrain** generation (`GenerateTerrain`), exactly where it is today — two concurrent `Request(C)` still collapse to one `GenerateTerrain(C)`. Decoration is NOT behind singleflight; it is driven solely by the single scheduler goroutine, so there is no concurrent-decoration race to dedup. The auto-requested neighbors flow through the SAME singleflight-guarded terrain path.

**Q: Determinism under arbitrary carve order?** → `Decorate(center, neighbors)` reads only (a) the per-chunk decoration seed, which is pure over `(worldSeed, centerBlockX, centerBlockZ)` via `SetDecorationSeed`, and (b) the fully-carved blocks of the 9 chunks. The carved blocks are themselves pure over `(seed, pos)` (the v1 invariant — `GenerateTerrain` has no order dependence). Therefore the decoration result for C is a pure function of `(seed, C.pos)` regardless of the order in which the 9 chunks finished carving or the order in which centers are decorated. **Cross-chunk writes commute** because: writes from center A into B and writes from center B into A target disjoint feature anchors (each feature's anchor + its seed are owned by exactly one center), and the `update` heightmap op + block set are order-independent per cell as long as both decorations have run before the chunk is read by the client — which the emit-once-after-own-decoration rule guarantees only for the CENTER's own contributions. (Note: vanilla emits a chunk after ITS features run, but a neighbor's features that write INTO it run when that neighbor decorates, which may be after the chunk was already sent — vanilla accepts this "late edit re-send"; for Phase 10's no-op `Decorate` the question is moot, and the feature phase will inherit vanilla's exact behavior. Flag in Open Decisions.)

### Diff to `world/worker.go`

```go
// ChunkResult: UNCHANGED. Still {Pos, Chunk, Err}, still immutable, still single-owner.

type Worker struct {
    gen       Generator
    regionDir string
    requests  chan level.ChunkPos
    results   chan ChunkResult
    sf        singleflight.Group

    // NEW: the scheduler's private state. Touched ONLY by the scheduler goroutine
    // (started in Run), never by handle goroutines → no lock, -race clean by ownership.
    carved    chan *carvedChunk     // handleTerrain → scheduler (carved chunks arrive here)
    staging   map[int64]*stagedChunk
    requested map[int64]bool
}

// handle (renamed handleTerrain): runs GenerateTerrain through singleflight, then
// sends the carved chunk to the scheduler instead of emitting it directly.
// A REGION-LOADED chunk is already "full" → it bypasses staging and emits directly
// (loaded worlds are already decorated; do not re-decorate).
func (w *Worker) handleTerrain(ctx, pos) {
    v, err, _ := w.sf.Do(key, func() (any, error) { return w.loadOrGenerate(pos) })
    if err != nil { /* emit error result, UNCHANGED */ ; return }
    ch := v.(*level.Chunk)
    if ch.Status == level.StatusFull {        // region hit: already decorated
        w.emit(ctx, ChunkResult{Pos: pos, Chunk: ch}); return
    }
    select { case <-ctx.Done(): case w.carved <- &carvedChunk{pos, ch}: }
}

// runScheduler: the NEW single goroutine that owns staging. Drains w.carved,
// stages each chunk, auto-requests its 8 neighbors, scans for newly-completable
// centers, decorates them over a Neighborhood, and emits each decorated center once.
func (w *Worker) runScheduler(ctx context.Context) { /* see skeleton in Code Examples */ }
```

`Run` starts BOTH the request reader (unchanged) and `go w.runScheduler(ctx)`.

**Why `loadOrGenerate` must call `GenerateTerrain` (not `Generate`):** the worker drives the two-phase lifecycle itself, so the generator's `GenerateTerrain` (carve-only) is what feeds staging. The `Generate` convenience (terrain+decorate-in-one) is only for tests and `SpawnSurfaceY`-style single-chunk callers.

### Where the LCG + WG-heightmap live (recap)

- **LCG / WorldgenRandom:** `world/levelgen/random.go` (same file as Xoroshiro, same `RandomSource` interface). Used by the (future) decoration pass; in Phase 10 only its tests exercise it.
- **WG heightmap `update`:** `level` package (next to `BitStorage`), as a free function or a small `Heightmap` method. The 6 fields already live on `level.Chunk.HeightMaps`. The pre-decoration build of `OCEAN_FLOOR_WG`/`MOTION_BLOCKING` lives in `world/levelgen/surface/system.go` next to `recomputeWorldSurfaceWG`.

### Anti-patterns to avoid

- **Decorating from a `handle` goroutine.** Decoration touches 9 chunks; if two handle goroutines decorate overlapping neighborhoods concurrently they race on shared chunks. ALL decoration must run on the single scheduler goroutine. (This is the `-race` trap — see Pitfalls.)
- **A second NBT/heightmap library.** `BitStorage` + the 6 existing fields are the heightmap. Do not add anything.
- **Map-iteration-order or goroutines inside `Decorate`.** The decoration RNG draw-order is the determinism contract (v2-features-decoration.md Pitfall 4). Phase 10's `Decorate` is a no-op so this is latent, but the seam must not introduce non-determinism (e.g. ranging `staging` to pick decoration order in a way that affects output — it must not, because each center is pure).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| LCG bit math + seed derivation | Re-derive constants/algorithms | The jar-transcribed math in **v2-features-decoration.md** (setDecorationSeed/setFeatureSeed/setSeed) + the BitRandomSource primitives in this doc's Code Examples | Already `javap -c` confirmed; re-deriving risks the `>>>` vs `>>` / signed-widening bugs `random.go` already documents for Xoroshiro. |
| Concurrent dedup of terrain gen | A new lock/map | The existing `singleflight.Group` on `GenerateTerrain` (untouched) | Already correct + race-tested; the seam reuses it verbatim. |
| Chunk pos → key packing | A new hash | The existing pack `int64(pos[0])<<32 \| int64(uint32(pos[1]))` (worker.go `chunkKey`) — use the int64 directly as the staging map key | One packing per codebase; already proven. |
| Heightmap storage | A new array type | The 6 `*BitStorage` fields already on `level.Chunk.HeightMaps` | They exist, serialize correctly (chunk.go WriteTo drops the 3 worldgen ones from the wire already), and round-trip through save. |
| Heightmap full rescan per feature | Re-scan the whole column on every block write | The incremental `heightmapUpdate` (jar `Heightmap.update`) | O(1) amortized vs O(height) per write; jar-confirmed algorithm. The bulk `recompute*` stays for the once-per-chunk pre-decoration build. |
| NBT / save round-trip of heightmaps | New code | Existing `ChunkToSave`/`ChunkFromSave` (chunk.go) already map all 6 WG/client heightmaps | No change needed for persistence. |
| **The staging + neighborhood scheduler** | (this is the one thing you DO build) | New code in `world/worker.go` + `world/neighborhood.go` | No library does Minecraft's hold-at-status neighborhood gating; it is the deliverable. |

**Key insight:** GEN2-01 and GEN2-03 are "fill in code whose shape already exists" (a second RandomSource; an incremental form of heightmap functions that already do the bulk version). The ONLY genuinely new construction is the worker's staging/scheduler — and even that reuses the existing singleflight, pos-packing, single-owner discipline, and the unchanged `ChunkResult`/tick rejoin. Keep the new surface area minimal.

---

## Common Pitfalls

### Pitfall 1: Reusing Xoroshiro's primitive bodies for the LCG
**What goes wrong:** `LegacyRandomSource.nextInt(bound)`, `nextLong`, `nextDouble`, `nextFloat` are ALGORITHMICALLY DIFFERENT from Xoroshiro's. Xoroshiro's `NextIntN` is a Lemire reduction (random.go 197); the LCG's `nextInt(bound)` is a power-of-2 fast path OR a `next(31) % bound` rejection loop (jar-confirmed below). Xoroshiro's `NextDouble` draws 53 bits once; the LCG draws `next(26)<<27 + next(27)` (two draws). Copy Xoroshiro and every decoration placement diverges from vanilla.
**Why it happens:** Both implement the same interface, so copy-paste looks safe.
**How to avoid:** Port each LCG primitive from the `BitRandomSource` bytecode in Code Examples. Test each against known java.util.Random vectors (a fixed seed → fixed `nextInt(100)` sequence).
**Warning signs:** A determinism test that compares against a captured vanilla sequence fails on `nextInt`/`nextDouble` but not `nextLong`.

### Pitfall 2: The `-race` trap — 9 chunks touched by one decoration
**What goes wrong:** Decoration mutates the center AND its 8 neighbors. If decoration runs in a `handle` goroutine (like terrain does today), two overlapping neighborhoods decorating concurrently write the same neighbor chunk from two goroutines → data race, and `go test -race` (the non-negotiable gate per CLAUDE.md) catches it.
**Why it happens:** The natural instinct is to keep using the existing per-request `go w.handle(...)` goroutine for the whole lifecycle.
**How to avoid:** Run ALL decoration (and the `staging` map) on a SINGLE scheduler goroutine. Terrain stays parallel (pure, single-chunk, footprint-guarded); decoration is serial. The handoff from parallel terrain to serial scheduler is a channel (`w.carved`), the same owner-crossing discipline as `asyncBridge`.
**Warning signs:** `-race` flags writes to a `*level.Chunk` from two goroutines, or to the `staging` map.

### Pitfall 3: Double-decoration
**What goes wrong:** A chunk is the neighbor of up to 9 centers; the completion scan can re-reach an already-decorated center, decorating it twice (drawing its decoration RNG twice → wrong/duplicated features, and re-running heightmap updates).
**Why it happens:** The scan visits a center every time any of its 9 neighbors arrives carved.
**How to avoid:** A `decorated bool` flag on `stagedChunk`; the scan decorates a center only if `carved && !decorated && all-9-carved`. Set `decorated=true` before emitting.
**Warning signs:** A center appears twice on the `results` channel, or a determinism test shows non-idempotent output when the same region is generated twice.

### Pitfall 4: Breaking the immutable single-owner handoff
**What goes wrong:** After emitting C as a `ChunkResult`, the worker still holds C in `staging` (because C is also a neighbor of un-decorated centers). If a LATER decoration of a neighbor center writes into C *after C was handed to the tick*, the worker mutates a chunk the tick now owns → the exact `-race`/ownership violation the v1 design forbids (worker.go lines 17-26).
**Why it happens:** A chunk is both "a center to emit" and "a neighbor to write into." Emitting it while keeping it writable is the trap.
**How to avoid:** Two clean options — (a) **for Phase 10 the no-op `Decorate` writes nothing**, so emit-and-keep is safe; design the seam so the emit-once rule is "emit when decorated as a center," and DOCUMENT that the feature phase (v2) must resolve the late-neighbor-write-after-emit question (vanilla re-sends; Sulfur's tick would need a re-send or a "hold until all 9 neighborhoods touching C are decorated" stricter rule). (b) Stricter rule even now: emit C only when C is decorated AND every center that has C as a neighbor is also decorated (so no future write can land in C). Recommend (a) for Phase 10 (matches the no-op scope) with the constraint written into the seam doc as an Open Decision for the feature phase.
**Warning signs:** `-race` flags a worker write to a chunk after it was sent on `results`.

### Pitfall 5: Determinism-under-reorder regression
**What goes wrong:** Generating a 5×5 region in request order A vs request order B produces different bytes (carve order or decoration order leaked into output).
**Why it happens:** Some shared mutable state in `GenerateTerrain` or a decoration order that depends on `staging` iteration.
**How to avoid:** `GenerateTerrain` is already pure (v1 invariant, `TestNoiseGenDeterministic`). Keep `Decorate(center, neighbors)` pure over `(seed, center.pos)` — its only inputs are the decoration seed (pure over center origin) and the carved neighbor blocks (pure over their pos). Never let the SET of decorated centers or their order affect a single center's output.
**Warning signs:** The reorder test (below) fails.

### Pitfall 6: singleflight on the wrong phase
**What goes wrong:** Putting decoration behind singleflight, or removing singleflight from terrain, causes either lost dedup (two terrain gens) or a decoration deadlock (singleflight serializes the scheduler).
**How to avoid:** singleflight wraps ONLY `loadOrGenerate`→`GenerateTerrain`, exactly as today. Decoration is serial on the scheduler and needs no dedup.

### Pitfall 7: Heightmap `update` staleness / wrong predicate
**What goes wrong:** Using one predicate for all heightmaps, or not calling `update` on a worldgen heightmap a feature reads, puts a later same-step feature underground/floating.
**How to avoid:** Each heightmap type has its own opaque predicate (WORLD_SURFACE_WG = NOT_AIR; OCEAN_FLOOR_WG = MATERIAL_MOTION_BLOCKING; MOTION_BLOCKING = blocksMotion|fluid — `Heightmap$Types` jar-confirmed). The proxy `SetBlock` calls `update` for each. (Phase 10 no-op `Decorate` makes this latent, but build the proxy + update wiring now so the feature phase inherits it correct.)

---

## Code Examples

### LCG core — `setSeed` + `next(bits)` (jar `LegacyRandomSource`)
```go
// MODULUS_BITS=48, MULTIPLIER=0x5DEECE66D, INCREMENT=0xB.
func (r *LegacyRandomSource) SetSeed(seed int64) {
    r.seed = (uint64(seed) ^ lcgMultiplier) & lcgMask
}

// next returns the top `bits` bits of the advanced state, as a SIGNED int32
// (Java BitRandomSource.next: seed = (seed*MULT + INCR) & MASK; return (int)(seed >>> (48 - bits))).
func (r *LegacyRandomSource) next(bits int) int32 {
    r.seed = (r.seed*lcgMultiplier + lcgIncrement) & lcgMask
    return int32(r.seed >> (48 - bits)) // logical shift of the 48-bit state, then truncate to int32
}
```

### LCG primitives (jar `BitRandomSource` defaults — DIFFER from Xoroshiro)
```go
// nextInt(bound): power-of-2 fast path, else next(31) % bound rejection loop. (jar-confirmed)
func (r *LegacyRandomSource) NextIntN(bound int32) int32 {
    if bound <= 0 {
        panic("levelgen: NextIntN bound must be positive")
    }
    if bound&(bound-1) == 0 { // power of two
        return int32((int64(bound) * int64(r.next(31))) >> 31)
    }
    for {
        bits := r.next(31)
        val := bits % bound
        if bits-val+(bound-1) >= 0 { // no overflow → accept
            return val
        }
    }
}

// nextLong = ((long)next(32) << 32) + next(32)  — the SECOND term is a SIGNED int (jar-confirmed).
func (r *LegacyRandomSource) NextLong() int64 {
    hi := int64(r.next(32))
    lo := int64(r.next(32))
    return hi<<32 + lo
}

// NextInt = (int)nextLong()? NO — for the LCG, Java code paths that want a raw int call next(32).
// The interface's NextInt() returns next(32) (the full 32-bit signed draw).
func (r *LegacyRandomSource) NextInt() int32 { return r.next(32) }

// nextFloat = next(24) * 0x1.0p-24  (jar-confirmed float 5.9604645E-8).
func (r *LegacyRandomSource) NextFloat() float32 { return float32(r.next(24)) * float32(5.9604645e-8) }

// nextDouble = ((long)next(26)<<27 + next(27)) * 0x1.0p-53  (jar-confirmed double 1.1102230246251565E-16).
func (r *LegacyRandomSource) NextDouble() float64 {
    hi := int64(r.next(26))
    lo := int64(r.next(27))
    return float64(hi<<27+lo) * 1.1102230246251565e-16
}
```
> `next(b)` truncates to `int32`; for `nextLong`/`nextDouble` the draws are widened to `int64` and the **arithmetic** (signed) behavior of `int32→int64` matters — a high bit set in `next(32)` makes the term negative, exactly like Java. Do NOT mask to unsigned.

### Worldgen seed derivation (jar `WorldgenRandom`, transcribed in v2-features-decoration.md — REUSE)
```go
// SetDecorationSeed(worldSeed, blockOriginX, blockOriginZ) → per-chunk base seed.
func (w *WorldgenRandom) SetDecorationSeed(worldSeed int64, blockX, blockZ int) int64 {
    w.SetSeed(worldSeed)
    a := w.NextLong() | 1
    b := w.NextLong() | 1
    seed := (int64(blockX)*a + int64(blockZ)*b) ^ worldSeed
    w.SetSeed(seed)
    return seed
}

// SetFeatureSeed(decoSeed, index, step) → per-feature seed. NO draws — pure arithmetic.
func (w *WorldgenRandom) SetFeatureSeed(decoSeed int64, index, step int) {
    w.SetSeed(decoSeed + int64(index) + int64(10000*step))
}

// SetLargeFeatureSeed(worldSeed, chunkX, chunkZ) — structure piece RNG (GEN3; jar v2-structures.md).
func (w *WorldgenRandom) SetLargeFeatureSeed(worldSeed int64, chunkX, chunkZ int) {
    w.SetSeed(worldSeed)
    a := w.NextLong()
    b := w.NextLong()
    w.SetSeed((int64(chunkX)*a)^(int64(chunkZ)*b)^worldSeed)
}
```

### The 3×3 neighborhood proxy (new `world/neighborhood.go`)
```go
// Neighborhood is a WorldGenLevel-like read/write view over a 3×3 of carved chunks,
// centered on `center`. Writes outside the 3×3 are dropped (vanilla blockStateWriteRadius=1).
// Used ONLY on the scheduler goroutine — single-owner, no locking.
type Neighborhood struct {
    center level.ChunkPos
    minY   int
    height int
    chunks map[int64]*level.Chunk // 9 entries: center + 8 neighbors, packed-pos keyed
    air, caveAir, water block.StateID
}

func (n *Neighborhood) chunkAt(wx, wz int) (*level.Chunk, bool) {
    cx, cz := wx>>4, wz>>4
    ch, ok := n.chunks[int64(int32(cx))<<32|int64(uint32(int32(cz)))]
    return ch, ok
}

func (n *Neighborhood) GetBlock(wx, wy, wz int) block.StateID {
    ch, ok := n.chunkAt(wx, wz)
    if !ok { return n.air }
    sec := (wy - n.minY) >> 4
    if sec < 0 || sec >= len(ch.Sections) { return n.air }
    return ch.Sections[sec].GetBlock((wy&15)<<8 | (wz&15)<<4 | (wx & 15))
}

func (n *Neighborhood) SetBlock(wx, wy, wz int, st block.StateID) {
    ch, ok := n.chunkAt(wx, wz)
    if !ok { return } // outside the 3×3 → dropped
    sec := (wy - n.minY) >> 4
    if sec < 0 || sec >= len(ch.Sections) { return }
    ch.Sections[sec].SetBlock((wy&15)<<8 | (wz&15)<<4 | (wx & 15), st)
    // Keep the 3 worldgen heightmaps live for later same-step features.
    lx, lz := wx&15, wz&15
    heightmapUpdate(ch.HeightMaps.WorldSurfaceWG, lx, wy, lz, n.minY, !isAir(st), /*opaqueAt*/ func(y int) bool { return !isAir(n.getLocal(ch, lx, y, lz)) })
    heightmapUpdate(ch.HeightMaps.MotionBlocking, lx, wy, lz, n.minY, blocksMotionOrFluid(st), func(y int) bool { return blocksMotionOrFluid(n.getLocal(ch, lx, y, lz)) })
    heightmapUpdate(ch.HeightMaps.OceanFloorWG, lx, wy, lz, n.minY, motionBlockingNoFluid(st), func(y int) bool { return motionBlockingNoFluid(n.getLocal(ch, lx, y, lz)) })
}
```

### Worker staging + scheduler skeleton (the deliverable, `world/worker.go`)
```go
type carvedChunk struct {
    pos level.ChunkPos
    ch  *level.Chunk
}

type stagedChunk struct {
    pos       level.ChunkPos
    chunk     *level.Chunk
    carved    bool
    decorated bool
}

func packPos(p level.ChunkPos) int64 { return int64(p[0])<<32 | int64(uint32(p[1])) }

// runScheduler owns `staging`/`requested` — a SINGLE goroutine, no locks (-race clean).
func (w *Worker) runScheduler(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case cc := <-w.carved:
            key := packPos(cc.pos)
            s := w.staging[key]
            if s == nil { s = &stagedChunk{pos: cc.pos}; w.staging[key] = s }
            s.chunk, s.carved = cc.ch, true

            // Auto-request the 8 neighbors so a single Request(C) pulls C's neighborhood in.
            for dx := -1; dx <= 1; dx++ {
                for dz := -1; dz <= 1; dz++ {
                    if dx == 0 && dz == 0 { continue }
                    np := level.ChunkPos{cc.pos[0] + int32(dx), cc.pos[1] + int32(dz)}
                    nk := packPos(np)
                    if !w.requested[nk] && w.staging[nk] == nil {
                        w.requested[nk] = true
                        w.Request(np) // bounded; singleflight dedups the terrain gen
                    }
                }
            }

            // Scan the up-to-9 centers this new chunk could have completed.
            for dx := -1; dx <= 1; dx++ {
                for dz := -1; dz <= 1; dz++ {
                    cp := level.ChunkPos{cc.pos[0] + int32(dx), cc.pos[1] + int32(dz)}
                    w.tryDecorate(ctx, cp)
                }
            }
        }
    }
}

// tryDecorate decorates `center` iff it is carved, not yet decorated, and all 9 of its
// neighborhood are carved. Emits the center exactly once afterward.
func (w *Worker) tryDecorate(ctx context.Context, center level.ChunkPos) {
    cs := w.staging[packPos(center)]
    if cs == nil || !cs.carved || cs.decorated { return }
    view := &Neighborhood{center: center, /* minY/height/states */ chunks: map[int64]*level.Chunk{}}
    for dx := -1; dx <= 1; dx++ {
        for dz := -1; dz <= 1; dz++ {
            np := level.ChunkPos{center[0] + int32(dx), center[1] + int32(dz)}
            ns := w.staging[packPos(np)]
            if ns == nil || !ns.carved { return } // neighborhood incomplete → wait
            view.chunks[packPos(np)] = ns.chunk
        }
    }
    w.gen.Decorate(view)        // Phase 10: NO-OP body that promotes status to full
    cs.decorated = true
    cs.chunk.Status = level.StatusFull
    w.emit(ctx, ChunkResult{Pos: center, Chunk: cs.chunk})
    // NOTE: keep cs in staging — center is still a neighbor of un-decorated centers.
    // (Phase 10 no-op Decorate writes nothing, so emit-and-keep is safe. The feature
    //  phase must revisit the late-write-after-emit rule — see Open Decisions.)
}

func (w *Worker) emit(ctx context.Context, res ChunkResult) {
    select { case <-ctx.Done(): case w.results <- res: }
}
```

### Heightmap `update` (jar `Heightmap.update`, transcribed above in Standard Stack §2)
The full algorithm — `if y <= firstAvailable-2 return; if opaque && y >= firstAvailable set y+1; if !opaque && firstAvailable-1 == y scan down` — is given in Standard Stack §2 and is the verbatim structure of the `Heightmap.update` bytecode (branch at `getFirstAvailable - 2`, the `setHeight(x,z,y+1)` on opaque, the `MutableBlockPos` downward scan on non-opaque-surface-removal).

### Determinism-at-seams test strategy (the GEN2-02 acceptance gate)
```go
// TestDecorationReorderIdentical: a 5×5 region generated in two DIFFERENT request orders
// must serialize to byte-identical chunks. This is the order-independence proof for the
// staging/scheduler seam (and for the no-op Decorate, it degenerates to the terrain
// determinism guarantee — which is exactly why it's safe to land the seam first).
func TestDecorationReorderIdentical(t *testing.T) {
    region := allPositions(-2, 2)         // 25 columns
    a := generateRegion(t, seed, region)  // request order: row-major
    b := generateRegion(t, seed, shuffle(region, rngFixed)) // request order: shuffled
    for _, p := range region {
        if !bytesEqual(serialize(a[p]), serialize(b[p])) {
            t.Fatalf("chunk %v differs between request orders", p)
        }
    }
}
// Run it under the Docker -race gate (-count=10) alongside the existing async stress tests
// so the scheduler's single-owner discipline is proven race-clean (CLAUDE.md mandate).
```
Additional targeted tests: (1) LCG vector tests (fixed seed → known `nextInt(100)`/`nextLong`/`nextDouble` sequence from a reference java.util.Random); (2) `heightmapUpdate` unit tests (place/remove a column-top block, assert the stored height); (3) emit-once test (each requested center appears exactly once on `Results()`); (4) neighborhood-completion test (a center is NOT emitted until all 8 neighbors are carved).

---

## Runtime State Inventory

> Greenfield code addition (no rename/migration). The only "state" considerations:

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | Region/`.linear` chunks carry `Status` + all 6 heightmaps (chunk.go `ChunkToSave`/`ChunkFromSave`). A loaded chunk is `full` and must SKIP staging/decoration. | Code edit: `handleTerrain` emits region-loaded `full` chunks directly (shown in the worker diff). No data migration — existing saves remain valid. |
| Live service config | None | None — verified: worldgen is in-process, no external services. |
| OS-registered state | None | None. |
| Secrets/env vars | `SULFUR_SUPERFLAT`/`SULFUR_DEBUG*` (main.go) — unaffected; Superflat also gains the `GenerateTerrain`/`Decorate` split (its `Decorate` is a no-op + finish). | Code edit: Superflat implements the split interface too (else it stops compiling against `Generator`). |
| Build artifacts | None | None. |

---

## Common Pitfalls (cross-reference)
See the prior research for the FEATURE-phase pitfalls that this seam must not preclude: v2-features-decoration.md Pitfalls 2 (cross-chunk cascade), 4 (stream-order RNG), 5 (heightmap staleness). Phase 10's seam is designed so all three are satisfiable by the future feature body without re-touching the worker.

---

## Confidence

| Claim | Confidence | Basis |
|-------|------------|-------|
| LCG constants (MULT 0x5DEECE66D, INCR 0xB, MASK 2^48-1) | **HIGH** | `javap -p LegacyRandomSource` fields confirmed; matches v2-features-decoration.md transcription. |
| `nextInt(bound)` = pow2-fast-path else `next(31)%bound` rejection loop | **HIGH** | `javap -c BitRandomSource.nextInt` bytecode read this session (iconst_1/isub/iand pow2 test; irem; overflow-guard branch). |
| `nextLong`=`(next(32)<<32)+next(32)` (signed lo), `nextFloat`=`next(24)*2^-24`, `nextDouble`=`(next(26)<<27+next(27))*2^-53` | **HIGH** | `javap -c BitRandomSource` for each, read this session (exact shifts/bit counts/float constants). |
| `setDecorationSeed`/`setFeatureSeed`/`setLargeFeatureSeed` math | **HIGH** | jar-transcribed in v2-features-decoration.md + v2-structures.md; `WorldgenRandom` method roster confirmed via `javap -p` this session. |
| `Heightmap.update` algorithm (firstAvailable-2 guard, opaque set y+1, non-opaque downward scan) | **HIGH** | `javap -c Heightmap.update` bytecode read this session. |
| FEATURES step gates on CARVERS@radius1 + STRUCTURE_STARTS@radius8, blockStateWriteRadius=1 | **HIGH** | `javap -c ChunkPyramid.lambda$static$7` read this session — exact `addRequirement(CARVERS,1)` (iconst_1) + `blockStateWriteRadius(1)`. |
| 6 heightmap fields already exist on `level.Chunk`; `WorldSurfaceWG` already built/read mid-pipeline | **HIGH** | Read `level/chunk.go` (HeightMaps struct) + `surface/system.go` (`recomputeWorldSurfaceWG`) this session. |
| Tick side (chunkReady/applyAsyncResults/SetWorld/ChunkManager) needs ZERO change | **HIGH** | Read `server/tick.go` SetWorld/chunkReady + `world/manager.go` — the seam is entirely inside `world/worker.go` + the generator split; the immutable `ChunkResult` contract is unchanged. |
| Split-Generate (GenerateTerrain + Decorate) seam design | **MEDIUM** (design-inferred) | Grounded on the exact current `worker.go` + jar gating, but the specific staging-map/scan-scheduler integration is an engineering choice, not jar-dictated. The reorder + -race + emit-once tests are the proof obligations. |
| Emit-once + late-neighbor-write-after-emit handling | **MEDIUM** | Safe for Phase 10's no-op `Decorate` (writes nothing). The feature phase must pick the resend-vs-hold rule — flagged in Open Decisions. |
| Auto-request-neighbors makes a single `Request(C)` eventually decorate C | **MEDIUM** | Correct by construction (a carved chunk pulls its 3×3 in), but interacts with the bounded `requests` channel's drop-on-full backpressure — needs the re-request-next-tick behavior (already how the streamer works) to converge under load. Validate with the 5×5 region test. |

---

## Open Decisions For Planner

### Decision 1 — Split-`Generate` vs in-place write-buffer (RESOLVE: Split)
**Fork:** (A) Split `Generator` into `GenerateTerrain` + `Decorate(neighborhood)` with a staging scheduler. (B) Keep single-shot `Generate(pos)` and buffer cross-chunk decoration writes into a per-neighbor `map[ChunkPos][]blockEdit`, flushed when each neighbor generates.
**Recommendation: (A) Split.** It is the only design where each chunk's output is a PURE function of `(seed, pos)` independent of generation order — the determinism is structural, not dependent on replaying vanilla's chunk-load order. (B)'s correctness hinges on reproducing vanilla's load-order for the buffer-flush merge, which is fragile and hard to test. (A) also matches vanilla's literal `CARVERS@radius1 + blockStateWriteRadius=1` gating and serves the structures track identically (v2-structures.md's "two-pass in the worker" recommendation is the same shape).
**Tradeoff:** (A) is a real worker-lifecycle change (staging map + scheduler goroutine) — more code now. (B) is a smaller worker change but pushes the risk into a fragile, order-dependent merge that is hard to prove correct. Pay the cost once, here, with the no-op `Decorate` so the lifecycle is proven before features exist.

### Decision 2 — Emit timing vs late neighbor writes (Phase-10 scope: emit-on-own-decoration; defer the strict rule)
**Fork:** When a chunk C is decorated as a center it is emitted to the tick. But C is also a neighbor of up-to-8 OTHER centers that may decorate LATER and write features INTO C. (X) Emit C as soon as C is decorated; the feature phase later adds a re-send when a neighbor writes into an already-sent C (vanilla's behavior). (Y) Hold C until C is decorated AND every center that has C as a neighbor is decorated — so no future write can land in C — then emit once, fully complete.
**Recommendation for Phase 10: (X), because `Decorate` is a no-op here and writes nothing — emit-on-own-decoration is correct and simplest, and keeps the seam minimal.** But the planner MUST record this as a constraint handed to the feature phase: the feature body will need either (X)+resend or a switch to (Y). (Y) is more memory (holds a larger frontier) but yields "complete on first send" — likely the better long-term choice; defer the pick to when features land.
**Tradeoff:** (X) now = minimal, correct-for-no-op, but defers a real question. (Y) now = more complex scheduling for zero Phase-10 benefit (nothing is written). Choosing (X) for Phase 10 is low-risk; the decision is genuinely a feature-phase concern.

---

## Sources

### Primary (HIGH confidence — jar, read this session)
- `temp/cache/26.2-inner.jar` via `javap -c`/`-p`:
  - `net.minecraft.world.level.levelgen.LegacyRandomSource` (constants, `next`, `setSeed`)
  - `net.minecraft.world.level.levelgen.BitRandomSource` (`nextInt(int)`, `nextLong`, `nextFloat`, `nextDouble` default bytecode)
  - `net.minecraft.world.level.levelgen.WorldgenRandom` (method roster: setDecorationSeed/setFeatureSeed/setLargeFeatureSeed/setLargeFeatureWithSalt)
  - `net.minecraft.world.level.levelgen.Heightmap` + `Heightmap$Types` (`update` bytecode; the 6 types)
  - `net.minecraft.world.level.chunk.status.ChunkPyramid` (`lambda$static$7` = FEATURES step: `addRequirement(CARVERS,1)`, `addRequirement(STRUCTURE_STARTS,8)`, `blockStateWriteRadius(1)`)
  - `net.minecraft.world.level.chunk.status.ChunkStatus` / `ChunkStep$Builder` (status list + `addRequirement`/`blockStateWriteRadius` API)
- Sulfur source read this session: `world/worker.go`, `world/noisegen.go`, `world/generator.go`, `world/manager.go`, `world/levelgen/random.go`, `level/chunk.go`, `level/bitstorage.go`, `level/chunkstatus.go`, `world/levelgen/surface/system.go` (heightmap functions), `cmd/sulfur/main.go` (worker wiring), `server/tick.go` (chunkReady/SetWorld), `world/noisegen_test.go` (determinism test pattern).

### Secondary (HIGH — prior phase research, jar-grounded)
- `.planning/research/v2-features-decoration.md` — LCG/seed math transcription, the Option-1 neighbor-aware seam, heightmap timing.
- `.planning/research/v2-structures.md` — the same worker seam for structures (two-pass recommendation), `setLargeFeatureSeed`.

### Project ground truth
- `CLAUDE.md` — fork/codegen decisions, `-race` mandate, xsync/singleflight blessing, "vanilla logic before Leaf async."

---

## Metadata

**Confidence breakdown:**
- GEN2-01 (LCG): HIGH — every constant + primitive algorithm is jar-confirmed bytecode this session.
- GEN2-03 (live heightmap): HIGH — `update` algorithm jar-confirmed; the 6 fields + bulk builders already exist in-tree.
- GEN2-02 (worker seam): MEDIUM — the vanilla gating (CARVERS@1, writeRadius 1) is HIGH/jar-confirmed; the specific staging+scheduler integration is a design recommendation proven by the reorder/-race/emit-once tests, not jar-dictated. The two genuine forks are in Open Decisions.

**Research date:** 2026-06-24
**Valid until:** Stable — proto 776 / 26.2 jar is pinned; the worldgen pipeline shape (LCG decoration, CARVERS@1 FEATURES gating) has been stable since 1.18. Re-verify only if the jar is retargeted (26.3+).
