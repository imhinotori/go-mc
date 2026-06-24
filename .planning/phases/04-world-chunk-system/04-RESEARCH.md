# Phase 4: World & Chunk System - Research

**Researched:** 2026-06-23
**Domain:** Minecraft 26.2 (protocol 776) chunk wire format, paletted-container encoding, Anvil region IO, off-tick chunk IO channeled into the tick
**Confidence:** HIGH — the decisive 776 byte layouts were decompiled directly from `temp/cache/26.2-server.jar` (unobfuscated), not asserted from the wiki/training data.

## Summary

This is the highest-risk wire-format phase. The failure mode ("client renders void or stripes") comes from one of a small set of byte-layout mistakes in the Chunk Data section blob. I resolved every one of those layout questions **authoritatively against the actual 26.2 server jar bytecode** (`javap -p -c` over `net/minecraft/world/level/chunk/*` and `net/minecraft/network/protocol/game/ClientboundLevelChunk*`), so the plan does not rest on the community wiki (which only documents ≤773).

The single most important finding: **the fork's `level.Section.WriteTo` is missing the per-section fluid-count short.** Vanilla `LevelChunkSection.write` writes **two** shorts — `nonEmptyBlockCount` then `fluidCount` — before the states PalettedContainer. The fork writes only one. Because the fork's `Section.ReadFrom` is *symmetrically* wrong (also reads one short), a Go self-round-trip test **passes while the wire is broken** — this is exactly why the authoritative WORLD-02 gate must be a **capture-diff against the real vanilla server**, not a self-round-trip.

The good news: most of the rest is already correct. `PaletteContainer.WriteTo/ReadFrom` is already retargeted to the 1.21.5+ no-length-prefix `writeFixedSizeLongArray` form (verified against `PalettedContainer$Data.write`). Heightmaps are already encoded as typed long-array entries (`heightMapEntry` = VarInt type + VarInt-prefixed long array, wrapped in a count) matching `HEIGHTMAPS_STREAM_CODEC = ByteBufCodecs.map(..., Heightmap.Types.STREAM_CODEC, ByteBufCodecs.LONG_ARRAY)`. The light-data field order (`skyYMask, blockYMask, emptySkyYMask, emptyBlockYMask, skyUpdates, blockUpdates`) matches `ClientboundLightUpdatePacketData` exactly. The Anvil region IO (`save/region/mca.go`) and `save.Chunk` ⇄ `level.Chunk` conversion are complete and reusable. The section count derives correctly from the embedded `dimension_type` (`registry.codec.go` has `MinY`/`Height`; overworld `height=384` → 24 sections).

**Primary recommendation:** Reuse the fork's `level`/`save`/`save/region` packages wholesale. Fix exactly **two** confirmed 776 deviations in `level/chunk.go` — (1) add the fluid-count short to `Section.WriteTo`/`ReadFrom`, (2) send only the 3 `sendToClient` heightmaps (WORLD_SURFACE, MOTION_BLOCKING, MOTION_BLOCKING_NO_LEAVES) instead of all 6. Build the off-tick chunk load/gen as a worker that returns an immutable `chunkReady` result drained in `applyAsyncResults`; fill `tickChunks`/`flushOutbound` to stream the view-distance ring. Use `golang.org/x/sync/singleflight` (the one new dep that belongs here) to dedup concurrent gen requests. **Verify WORLD-02/03 by byte-diffing our encoder output against a chunk packet captured from `temp/cache/26.2-server.jar` on a known superflat seed** — the same capture-diff method that caught the Phase-2 registry/tags bugs.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Chunk load/store (Anvil .mca) | Disk IO (off-tick worker) | — | Blocking file IO must never run on the tick goroutine (WORLD-01) |
| Chunk generation (superflat stub) | Compute (off-tick worker) | — | CPU work; channels result back into the tick (WORLD-01/04) |
| `save.Chunk` ⇄ `level.Chunk` conversion | Data layer (`level`/`save`) | — | Already implemented in fork; pure transform |
| Paletted-container encode/decode | Data layer (`level`) | — | The WORLD-02 wire layout; lives in `level/chunk.go`+`palette.go` |
| Chunk Data + Light packet assembly | Tick goroutine (`flushOutbound`) | Data layer (`level.Chunk.WriteTo`) | Packet bytes built on-tick from owned chunk state, enqueued via `Client.Send` |
| View-distance ring streaming | Tick goroutine (`tickChunks`) | — | Decides which chunks each player needs; single-owner player state |
| Dedup concurrent gen requests | Off-tick (singleflight) | Tick (request issue) | Two players entering same ungenerated chunk → one generation |
| Section count derivation | Data layer (dimension_type) | — | `Height/16` from `registry.codec.go` `DimensionType` (overworld → 24) |

## Standard Stack

The chunk/region foundation is the fork itself — there is **no new library to choose**. The only external addition is `singleflight`.

### Core (already in the fork — reuse, do not rewrite)
| Package / API | Purpose | Status for 776 |
|---------------|---------|----------------|
| `level.BitStorage` (`level/bitstorage.go`) | Compacted LSB long-packed `[]uintN` for sections + heightmaps; `NewBitStorage(bits,length,data)`, `Get/Set/Swap`, `Raw()` | **Packing logic correct (LSB).** ⚠ Its own `WriteTo`/`ReadFrom` still emit a VarInt length prefix — but the section path does NOT use those; PaletteContainer writes the longs itself. Heightmaps use `bs.Raw()` directly. So `BitStorage.WriteTo` is effectively dead for chunk packets. |
| `level.PaletteContainer[T]` (`level/palette.go`) | Per-section block-state & biome paletted container; single-value / linear / hash / global palette selection; `NewStatesPaletteContainer`, `NewBiomesPaletteContainer`, `…WithData` | **CORRECT for 776.** `WriteTo` writes `UnsignedByte(bits)` + palette + raw longs with **no length prefix** (matches `PalettedContainer$Data.write` → `writeFixedSizeLongArray`). |
| `level.Chunk` / `level.Section` (`level/chunk.go`) | Column = `[]Section` + `HeightMaps` + `BlockEntity`; `Chunk.WriteTo/ReadFrom`, `Section.WriteTo/ReadFrom`, `EmptyChunk(secs)`, `Data()`/`PutData()` | **Heightmap + light layout CORRECT. ⚠ `Section.WriteTo` MISSING fluid-count short (the one real bug). ⚠ sends all 6 heightmaps; vanilla sends 3.** |
| `level.HeightMaps` + `heightMapEntry` (`level/chunk.go`) | Typed long-array heightmaps (Type enum + packed longs) | **CORRECT** — matches `HEIGHTMAPS_STREAM_CODEC` map(typeId, LONG_ARRAY). WORLD-03 satisfied. |
| `lightData` (`level/chunk.go`) | sky/block masks + empty masks + sky/block updates | **Field order CORRECT** vs `ClientboundLightUpdatePacketData`. ⚠ empty-mask = bitwise-complement shortcut (see Pitfall 7). |
| `block.StateID`, `block.BitsPerBlock`, `block.FromID`, `block.ToStateID`, `block.StateList`, `block.IsAir` (`level/block/`) | Generated 776 global block-state palette (1196 blocks / 32366 states); `BitsPerBlock = bits.Len(len(StateList))` = 15 | **CORRECT** — the direct-palette index space the global palette writes into. |
| `biome.Type`, `biome.BitsPerBiome` (`level/biome/`) | Generated biome palette | **CORRECT** |
| `save.Chunk`, `save.Section`, `save.BlockState`, `save.BiomeState` (`save/chunk.go`) | Anvil NBT chunk schema; `level.ChunkFromSave` / `level.ChunkToSave` bridge | **Reuse** — the on-disk ⇄ in-memory transform. |
| `save/region.Region` (`save/region/mca.go`) | Anvil `.mca` 32×32 region IO: `Open/Create/Load`, `ReadSector(x,z)`, `WriteSector(x,z,data)`, `In/At` coord math, `PadToFullSector` | **Reuse wholesale** — complete, working Anvil IO. NOT MT-safe (see Architecture). |
| `registry.codec.go` `DimensionType{MinY,Height,LogicalHeight,…}` | Source of section count: `secs = Height/16`; overworld `height=384` → 24, `min_y=-64` | **Reuse** — the bot already does `EmptyChunk(int(dimType.Height)/16)` (`bot/world/chunks.go`). |
| `data/packetid` IDs | `ClientboundLevelChunkWithLight` (45), `ClientboundSetChunkCacheCenter` (94), `ClientboundSetChunkCacheRadius` (95), `ClientboundChunkBatchStart`/`Finished`, `ClientboundForgetLevelChunk` (37) | **Present** — generated 776 IDs. |

### Supporting (new — the only additions this phase needs)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `golang.org/x/sync/singleflight` | latest (v0.x) | Dedup concurrent chunk load/gen requests: two players entering the same ungenerated chunk → exactly one generation, both get the result | The ONE x/sync piece that belongs in Phase 4. `singleflight.Group.Do(key, fn)` keyed by packed `(cx,cz)`. **Not yet a dependency — must `go get golang.org/x/sync`.** [VERIFIED: `go list -m golang.org/x/sync` → "not a known dependency"] |

### Deferred to Phase 8 (DO NOT introduce now)
| Library | Why deferred |
|---------|--------------|
| `xsync/v4` (concurrent maps, MPMCQueue) | The chunk-holder map is owned by the tick goroutine (single-owner, like `players`). A plain `map[ChunkPos]*ChunkHolder` is correct and faster under single ownership. xsync is Phase 8 (OPT-04). |
| `ants/v2` (goroutine pool) | The off-tick chunk worker can be a single goroutine + bounded request channel for v1. A recycling pool is Phase 8. |
| `conc` (structured concurrency) | No tick-bounded fan-out yet. Phase 8. |
| Linear region format | OPT-05, Phase 8. Use Anvil `.mca` now. |

**Installation:**
```bash
go get golang.org/x/sync@latest   # ONLY for singleflight (chunk-gen dedup)
```

**Version verification:** `golang.org/x/sync` is API-stable; `singleflight` has been frozen for years. Pin whatever `@latest` resolves and record it. No other package is added.

## Architecture Patterns

### System Architecture Diagram

```
                         TICK GOROUTINE (single owner — TICK-05)
  player join/move ─┐    ┌──────────────────────────────────────────────────────┐
  (subtick buffer)  │    │ tickWorld → tickChunks → ... → applyAsyncResults → ... │
                    │    │                │                      ▲          flushOutbound
                    │    │                │ compute needed ring  │ drain        │
                    ▼    │                ▼ (per player view-dist)│ chunkReady   ▼
            dispatch ────┤        ┌───────────────┐   request    │      Client.Send (bounded
                         │        │ ChunkManager  │──(cx,cz)──┐   │      outbound queue, 1 writer)
                         │        │ map[Pos]Holder│           │   │
                         └────────┤ (tick-owned)  │◄──────────┼───┘
                                  └───────────────┘  chunkReady│ (immutable result)
                                          │ miss → enqueue      │
                                          ▼ load/gen request    │  results channel
   ════════════════════════════════ TICK BOUNDARY ═════════════╪══════════════════════
                                          ▼                     │
                              ┌──────────────────────┐  singleflight.Do(key)
                              │ OFF-TICK CHUNK WORKER │  (dedup duplicate requests)
                              │  goroutine(s)         │
                              │  1. region.ReadSector │──► save/region/*.mca (disk)
                              │  2. ChunkFromSave OR  │
                              │     generate(superflat)│
                              │  3. send chunkReady ──────────────► results channel ──┘
                              └──────────────────────┘
```

The worker NEVER touches tick-owned state. It receives an immutable request `(cx,cz, seed/dim)`, produces a fully-built `*level.Chunk`, and sends it back as an immutable `chunkReady` message. The tick goroutine is the only thing that mutates the `ChunkManager` map and builds packets.

### Recommended Project Structure
```
world/                       # NEW package (server-side world; distinct from bot/world)
├── manager.go               # ChunkManager: tick-owned map[level.ChunkPos]*ChunkHolder; needed-ring calc
├── holder.go                # ChunkHolder: chunk + load state (Empty/Loading/Ready/Sent-per-player)
├── worker.go                # off-tick load/gen worker; singleflight dedup; emits chunkReady
├── generator.go             # Generator interface + SuperflatGenerator (WORLD-04 stub)
├── stream.go                # view-distance ring: which chunks each player needs, send order
└── packet.go                # ClientboundLevelChunkWithLight assembly + SetChunkCacheCenter/Radius

server/                      # EXISTING — fill the Phase-3 seams
├── tick_phases.go           # fill tickChunks() + flushOutbound(); applyAsyncResults drains chunkReady
└── tick.go                  # add asyncIn-style chunkReady channel wiring (see Pattern 1)
```

### Pattern 1: Off-tick chunk IO → `applyAsyncResults` rejoin (consume the Phase-3 seam unchanged)
**What:** Blocking region IO and generation run on a worker goroutine; results re-enter the tick through the existing `applyAsyncResults` slot — *without reshaping the pipeline* and *without Phase-8 async infra*.
**Why this fits:** `tick.go` already defines `asyncResult interface{ applyTo(*TickLoop) }` and a nil `asyncIn <-chan asyncResult` whose drain is `applyAsyncResults` (`server/tick_phases.go`). Phase 4 wires a concrete channel + result type into exactly that seam.

```go
// world/worker.go — runs OFF the tick goroutine
type chunkReady struct {           // implements server.asyncResult
    pos   level.ChunkPos
    chunk *level.Chunk             // fully built, immutable handoff
    err   error
}

// server/tick.go — the seam already exists; give it a concrete channel.
// In Phase 3 asyncIn is nil and applyAsyncResults is a no-op. Phase 4 sets it:
//   loop.asyncIn = worker.Results()   // <-chan asyncResult
// applyAsyncResults (server/tick_phases.go) drains it on the OWNER goroutine:
func (t *TickLoop) applyAsyncResults() {
    t.trace("applyAsyncResults")
    if t.asyncIn == nil { return }              // Phase-3 no-op path preserved
    for {
        select {
        case r := <-t.asyncIn: r.applyTo(t)     // chunkReady.applyTo inserts into ChunkManager
        default: return                          // non-blocking, never parks the tick
        }
    }
}
```

`chunkReady.applyTo` inserts the built chunk into the tick-owned `ChunkManager` map and marks holders Ready. **No game state crosses the boundary except the immutable `chunkReady` message** — same discipline as `Intent` and the register/unregister channels. This is `-race` clean by construction (matches the Phase-3 proof).

### Pattern 2: Paletted-container section encode (the WORLD-02 byte layout — JAR-VERIFIED)
**Confirmed `LevelChunkSection.write` order (26.2 bytecode):**
```
writeShort(nonEmptyBlockCount)   // short #1
writeShort(fluidCount)           // short #2  ← THE FORK IS MISSING THIS
states.write(buf)                // PalettedContainer: byte bitsPerEntry, palette, fixed-size long[] (NO length prefix)
biomes.write(buf)                // PalettedContainerRO: same shape
```
`getSerializedSize` begins at `iconst_4` (= 4 bytes = two shorts), corroborating two shorts.

**`PalettedContainer$Data.write` (26.2 bytecode):** `writeByte(bitsPerEntry)` → `palette.write` → `writeFixedSizeLongArray(getRaw())`. `writeFixedSizeLongArray` emits **no VarInt length prefix** — the reader recomputes long count from `bitsPerEntry` and the 4096 entry count. The fork's `PaletteContainer.WriteTo` already does exactly this (`level/palette.go` lines 165-182). ✓

**Bits-per-entry → palette format thresholds (from `statesCfg.bits`, jar-consistent):**
| bits requested | stored bits | palette format |
|---------------|-------------|----------------|
| 0 | 0 | single-valued (one VarInt block id, zero data longs) |
| 1–4 | **4** (floored) | indirect / linear palette |
| 5–8 | 5–8 | indirect / hash palette |
| ≥9 | `block.BitsPerBlock` = **15** | direct (global palette, no palette section) |
Biomes: 0 → single; 1–3 → linear; ≥4 → direct (`biome.BitsPerBiome`). [VERIFIED: `level/palette.go` + `level/biome/list.go`]

### Pattern 3: Chunk Data packet body (JAR-VERIFIED `ClientboundLevelChunkPacketData.write`)
```
heightmaps   : HEIGHTMAPS_STREAM_CODEC  = map(VarInt count → {Heightmap.Types(VarInt id), LONG_ARRAY(VarInt-len + long[])})
buffer       : writeVarInt(len) + writeBytes(sections-blob)   // concatenated Section.write for ALL 24 sections
blockEntities: LIST_STREAM_CODEC                              // VarInt count + per-entity records
```
The fork's `Chunk.WriteTo` produces exactly `Array(hmEntries), ByteArray(data), Array(BlockEntity), &light` — order correct. The section blob = `Chunk.Data()` = every `Section.WriteTo` concatenated. ✓ (subject to the fluid-count fix.)

### Pattern 4: Top-level packet + which heightmaps (JAR-VERIFIED)
`ClientboundLevelChunkWithLightPacket` = `Int x, Int z, chunkData, lightData`.
**Only `sendToClient()` heightmaps are sent, and `sendToClient()` is true IFF `usage == CLIENT`.** Per-type Usage from the jar `<clinit>`:
| Type | id | Usage | Sent to client? |
|------|----|-------|-----------------|
| WORLD_SURFACE_WG | 0 | WORLDGEN | no |
| **WORLD_SURFACE** | 1 | CLIENT | **yes** |
| OCEAN_FLOOR_WG | 2 | WORLDGEN | no |
| OCEAN_FLOOR | 3 | LIVE_WORLD | no |
| **MOTION_BLOCKING** | 4 | CLIENT | **yes** |
| **MOTION_BLOCKING_NO_LEAVES** | 5 | CLIENT | **yes** |

→ Vanilla sends **3** heightmap entries (ids 1, 4, 5). The fork sends all 6 (deviation; clients tolerate extra keys but match vanilla via capture-diff).

### Pattern 5: Light data (JAR-VERIFIED field order)
`ClientboundLightUpdatePacketData`: `skyYMask(BitSet)`, `blockYMask(BitSet)`, `emptySkyYMask(BitSet)`, `emptyBlockYMask(BitSet)`, `skyUpdates(List<byte[2048]>)`, `blockUpdates(List<byte[2048]>)`. Fork `lightData.WriteTo` order matches exactly. Each `BitSet` is a VarInt-length-prefixed `long[]`; each light array is a fixed 2048-byte (half a byte per cell over 16³). For an all-stone superflat with full sky, sky-light is trivial; supplying correct masks + arrays is enough for the client to render without the post-load light recalc artifacts.

### Pattern 6: View-distance neighbor ring + the spawn handshake (WORLD-05, sets up PLAY-03)
**What:** Before the client renders (and stops falling through void), it needs the chunk it stands in **plus the surrounding ring** out to the server view distance, AND the cache-center set.
**Order that works (vanilla sequence):**
1. `ClientboundSetChunkCacheCenter(cx, cz)` (id 94) — tells the client which chunk is center.
2. (optional but vanilla) `ClientboundChunkBatchStart` … N× `ClientboundLevelChunkWithLight` … `ClientboundChunkBatchFinished(N)` — batched send; the client paces requests via `ServerboundChunkBatchReceived`.
3. The "start waiting for chunks" **Game Event** (`ClientboundGameEvent`, level-chunks-load event) — belongs to PLAY-03/Phase 5, but the ring must already be in flight.
Send the ring **center-out** (spiral by increasing Chebyshev distance) so the player's own chunk arrives first. View distance for v1 can be small (radius 2–3 satisfies "stands on solid ground").

### Pattern 7: Deterministic superflat generator (WORLD-04)
**What:** A pure function `(cx,cz) → *level.Chunk` with a fixed column profile. For "all-stone flat" (the ROADMAP success criterion): bedrock at the bottom section, stone up to a fixed Y, air above; biome = plains everywhere; heightmaps computed from the profile.
**Why a stub now:** WORLD-04 explicitly scopes "superflat/noise stub". Vanilla-parity density-function worldgen is PARITY-01 (v2, Phase 9). Keep the `Generator` interface so the noise generator slots in later.
```go
type Generator interface { Generate(pos level.ChunkPos) *level.Chunk }
type Superflat struct{ secs int; surfaceY int; stone, bedrock block.StateID }
// Build EmptyChunk(secs), fill sections, recompute BlockCount + heightmaps, Status=StatusFull.
```
Determinism = no RNG, or seed-derived only; same input → identical bytes (capture-diff stability).

### Anti-Patterns to Avoid
- **Self-round-trip as the WORLD-02 proof.** The fork's read/write are symmetrically wrong (both omit fluid-count). A Go round-trip test passes on a wire format the client rejects. Use capture-diff.
- **Running `region.ReadSector` on the tick goroutine.** Blocking disk IO on the single owner stalls every player. Always off-tick (Pattern 1).
- **Sharing a `*level.Chunk` mutably across the tick boundary.** Hand off immutable; mutate only the tick-owned map. (`save/region.Region` is explicitly "Not MT-Safe!".)
- **Hard-coding 24 sections.** Derive `secs = dimType.Height/16` from `registry.codec.go` so nether/end/custom heights work and the heightmap `bits.Len(secs*16+1)` is correct.
- **Reaching for xsync/ants now.** Single-owner map + one worker goroutine is correct for v1; concurrency libs are Phase 8.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Anvil `.mca` region IO | Custom sector allocator / offset table | `save/region.Region` (`mca.go`) | Complete: sector packing, free-list, `PadToFullSector`, 1MB cap, timestamp head. Battle-tested fork code. |
| NBT chunk (de)serialization | A second NBT lib / manual tag walk | `save.Chunk` + `level.ChunkFromSave`/`ChunkToSave` | The on-disk schema ⇄ in-memory transform already exists incl. block-entity packing and palette name↔state mapping. |
| LSB long bit-packing | Hand-rolled `>>`/`&` packer | `level.BitStorage` | LSB packing, `valuesPerLong`, mask, `calcBitStorageSize` all correct. (Just don't call its prefix-emitting `WriteTo` for the wire.) |
| Palette format selection | Custom single/indirect/direct switch | `level.PaletteContainer` + `statesCfg`/`biomesCfg` | Thresholds + resize-on-overflow + global-palette fallback implemented and **already 776-correct** (no length prefix). |
| Global block-state ids | Hand-mapping block→state id | `block.ToStateID`/`FromID`/`StateList`/`BitsPerBlock` | Generated from the 776 jar (32366 states). The direct-palette index space. |
| Heightmap typed-array encoding | Manual NBT compound (the OLD format) | `level.heightMapEntry` + `Chunk.WriteTo` | Already the 774+ typed-long-array form — matches `HEIGHTMAPS_STREAM_CODEC`. |
| Light packet masks/arrays | Custom bitset framing | `level.lightData` + `pk.BitSet` | Field order verified against `ClientboundLightUpdatePacketData`. |
| chunk-gen request dedup | Mutex + in-flight map | `golang.org/x/sync/singleflight` | One generation per key, both callers get the result; battle-tested, tiny. |

**Key insight:** The fork already implements ~90% of the chunk pipeline *correctly for 776*. Phase 4 is **two surgical wire fixes + new world/streaming glue**, not a chunk-codec rewrite. Resist re-implementing anything in `level`/`save`/`save/region`.

## Runtime State Inventory

> Not a rename/refactor phase — greenfield world subsystem. The closest analogue ("what existing state must change") is the two confirmed wire-format deviations in the consumed fork code:

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Existing fork code to retarget | `level.Section.WriteTo`/`ReadFrom` (missing fluid-count short) | Code edit in `level/chunk.go` (Pitfall 1) |
| Existing fork code to align with vanilla | `level.Chunk.WriteTo` sends 6 heightmaps; vanilla sends 3 | Code edit — filter to `sendToClient` set (Pitfall 5) |
| Stored data | None — no existing world save to migrate (greenfield); generator produces fresh chunks | None |
| Live service config | None | None |
| Build artifacts | None new | None |

## Common Pitfalls

### Pitfall 1: Missing fluid-count short → stripes/void (THE confirmed bug)
**What goes wrong:** Client reads `nonEmptyBlockCount`, then expects a second `short` (fluidCount), but the fork wrote the states-palette `bitsPerEntry` byte there. Every subsequent byte is misaligned → garbage palette → stripes, or a decode error → void.
**Why it happens:** `level/chunk.go` `Section.WriteTo` (line 481) writes only `pk.Short(s.BlockCount)`. Vanilla `LevelChunkSection.write` writes **two** shorts (`nonEmptyBlockCount`, `fluidCount`). [VERIFIED: 26.2 jar bytecode — `getSerializedSize` starts `iconst_4`.]
**How to avoid:** Add `pk.Short(s.FluidCount)` after `BlockCount` in `Section.WriteTo` and read it in `Section.ReadFrom`. Add a `FluidCount int16` field (count of fluid blocks in the section; for an all-stone superflat it's 0, which is sufficient for v1 but the field/short MUST be present).
**Verification:** **Capture-diff** — a self-round-trip will NOT catch this (read/write are symmetrically wrong today).

### Pitfall 2: VarInt length prefix on the section long array (1.21.5+ removal)
**What goes wrong:** Writing `VarInt(len(longs))` before the packed longs (the pre-1.21.5 format) → client mis-frames the whole section.
**Why it happens:** It's the historically documented format and `BitStorage.WriteTo` still does it.
**How to avoid:** Use `PaletteContainer.WriteTo` (already correct — no prefix), NOT `BitStorage.WriteTo`, for section data. [VERIFIED: `PalettedContainer$Data.write` → `writeFixedSizeLongArray`.]
**Verification:** Capture-diff; also a unit assert that a known section encodes to the expected byte length (`1 + paletteBytes + 8*calcBitStorageSize(bits,4096)`).

### Pitfall 3: MSB vs LSB long packing
**What goes wrong:** Packing values most-significant-first → every block index scrambled → stripes.
**Why it happens:** A naive packer or porting MSB code.
**How to avoid:** `level.BitStorage` packs LSB-first (`offset = (n - c*valuesPerLong) * bits`, value `<< offset`). Don't replace it. [VERIFIED: `level/bitstorage.go` `calcIndex`/`Set`.]
**Verification:** `palette_test.go` already round-trips Get/Set; extend with a known-vector test.

### Pitfall 4: Wrong bits-per-entry threshold (4-bit floor, 9→direct)
**What goes wrong:** Using requested bits 1–3 verbatim instead of flooring to 4, or not switching to the global palette at ≥9 → client reads the wrong number of longs.
**How to avoid:** Use `statesCfg.bits` (0→0, 1–4→4, 5–8→passthrough, ≥9→15) and `biomesCfg.bits` (0→0, 1–3→passthrough, ≥4→direct). Don't bypass `PaletteContainer`. [VERIFIED: `level/palette.go`.]
**Verification:** Capture-diff against a superflat chunk (mostly single-valued or low-bit sections).

### Pitfall 5: Sending the wrong heightmap set
**What goes wrong:** Sending WORLDGEN/LIVE_WORLD heightmaps (the fork sends all 6). Usually tolerated by the client, but a mismatch vs vanilla can confuse lighting/placement heuristics and fails byte-diff.
**How to avoid:** Send only `sendToClient()` types: WORLD_SURFACE(1), MOTION_BLOCKING(4), MOTION_BLOCKING_NO_LEAVES(5). [VERIFIED: jar `<clinit>` Usage + `sendToClient` body.]
**Verification:** Capture-diff the heightmap entry count and type ids.

### Pitfall 6: Wrong section count from dimension height
**What goes wrong:** Hard-coding 16 or 18 sections instead of 24 → wrong heightmap bit width (`bits.Len(secs*16+1)`), wrong number of sections in the blob → void.
**How to avoid:** `secs = int(dimType.Height) / 16` from `registry.codec.go` (overworld height=384 → 24). The bot already does this. [VERIFIED: `registry/codec.go` `Height int32`; `bot/world/chunks.go`.]
**Verification:** Assert the server's `EmptyChunk` section count equals the client's `dimType.Height/16` for overworld.

### Pitfall 7: Empty-light masks computed by bitwise complement
**What goes wrong:** The fork derives `emptySkyYMask = ^skyYMask`. For an all-present, all-lit superflat this is harmless, but it is NOT vanilla's true empty-section computation (vanilla marks sections with all-zero light as empty and omits their arrays). Diverges on partially-lit or empty sections.
**How to avoid:** For the v1 superflat (every section present, sky fully lit at top), the shortcut renders fine. If capture-diff shows a mask mismatch, compute masks from actual section light presence (present-mask bit set when a light array is supplied; empty-mask bit set when the section exists but its light is all-zero and omitted).
**Verification:** Capture-diff the four bitsets + the updates list lengths.

### Pitfall 8: Blocking IO on the tick goroutine
**What goes wrong:** `region.ReadSector` / generation on the tick → MSPT spikes, every player stutters, spiral-of-death clamp engages.
**How to avoid:** Pattern 1 — worker goroutine, `chunkReady` via `applyAsyncResults`. The seam exists for exactly this.
**Verification:** `-race` clean (Docker `golang:1.26`, `CGO_ENABLED=0`); MSPT stays flat while chunks load.

## Code Examples

### Section encode with fluid-count (the fix)
```go
// level/chunk.go — Section gains FluidCount; WriteTo writes TWO shorts (jar-verified).
type Section struct {
    BlockCount int16
    FluidCount int16   // NEW — count of fluid blocks; 0 for all-stone superflat
    States     *PaletteContainer[BlocksState]
    Biomes     *PaletteContainer[BiomesState]
    SkyLight   []byte  // 2048 or nil
    BlockLight []byte  // 2048 or nil
}
func (s *Section) WriteTo(w io.Writer) (int64, error) {
    return pk.Tuple{
        pk.Short(s.BlockCount),
        pk.Short(s.FluidCount),   // ← THE MISSING SHORT (LevelChunkSection.write)
        s.States,                 // PaletteContainer: byte bits + palette + raw longs (no len prefix)
        s.Biomes,
    }.WriteTo(w)
}
func (s *Section) ReadFrom(r io.Reader) (int64, error) {
    return pk.Tuple{
        (*pk.Short)(&s.BlockCount),
        (*pk.Short)(&s.FluidCount), // ← symmetric read
        s.States,
        s.Biomes,
    }.ReadFrom(r)
}
```

### Filtering heightmaps to the sendToClient set
```go
// level/chunk.go Chunk.WriteTo — emit only ids 1,4,5 (WORLD_SURFACE, MOTION_BLOCKING, *_NO_LEAVES).
var hmEntries []heightMapEntry
if bs := c.HeightMaps.WorldSurface; bs != nil {           // id 1 (CLIENT)
    hmEntries = append(hmEntries, heightMapEntry{Type: 1, Data: bs.Raw()})
}
if bs := c.HeightMaps.MotionBlocking; bs != nil {         // id 4 (CLIENT)
    hmEntries = append(hmEntries, heightMapEntry{Type: 4, Data: bs.Raw()})
}
if bs := c.HeightMaps.MotionBlockingNoLeaves; bs != nil { // id 5 (CLIENT)
    hmEntries = append(hmEntries, heightMapEntry{Type: 5, Data: bs.Raw()})
}
// (drop WORLD_SURFACE_WG/OCEAN_FLOOR_WG/OCEAN_FLOOR — not sendToClient)
```

### Top-level ClientboundLevelChunkWithLight assembly (flushOutbound)
```go
// world/packet.go — Int x, Int z, chunkData, lightData (jar-verified top-level order).
func WriteLevelChunkWithLight(cx, cz int32, ch *level.Chunk) (pk.Packet, error) {
    var body bytes.Buffer
    if _, err := (pk.Tuple{ pk.Int(cx), pk.Int(cz) }).WriteTo(&body); err != nil { return pk.Packet{}, err }
    if _, err := ch.WriteTo(&body); err != nil { return pk.Packet{}, err } // heightmaps+data+blockEntities+light
    return pk.Packet{ ID: int32(packetid.ClientboundLevelChunkWithLight), Data: body.Bytes() }, nil
}
```

### Off-tick worker with singleflight dedup
```go
// world/worker.go — one generation per chunk key even under concurrent requests.
func (wk *Worker) load(pos level.ChunkPos) {
    key := strconv.FormatInt(int64(pos[0])<<32|int64(uint32(pos[1])), 10)
    go func() {
        v, _, _ := wk.sf.Do(key, func() (any, error) {   // singleflight.Group
            if data, err := wk.region.ReadSector(int(pos[0]&31), int(pos[1]&31)); err == nil {
                sc, _ := save.ReadChunk(data); return level.ChunkFromSave(sc)
            }
            return wk.gen.Generate(pos), nil             // miss → superflat stub
        })
        wk.results <- chunkReady{pos: pos, chunk: v.(*level.Chunk)} // → applyAsyncResults
    }()
}
```

### Section count from dimension_type (no hard-coding)
```go
dimType := registries.DimensionType.GetByID(player.DimensionType)
secs := int(dimType.Height) / 16            // overworld 384 → 24 (jar/codec-verified)
ch := level.EmptyChunk(secs)
```

## State of the Art

| Old Approach | Current (776) Approach | When Changed | Impact |
|--------------|------------------------|--------------|--------|
| Section long array prefixed with `VarInt` length | `writeFixedSizeLongArray` — NO length prefix; reader derives count | 1.21.5 / proto 770 | Fork's `PaletteContainer` already correct; don't use `BitStorage.WriteTo`. |
| One `short` (block count) per section | **Two** shorts: `nonEmptyBlockCount` + `fluidCount` | 26.1+ (≈proto 775/776) | **Fork bug** — must add fluid-count short. |
| Heightmaps as an NBT compound | Typed `Map<Heightmap.Types, long[]>` (VarInt id + LONG_ARRAY) | 1.21.x (774+) | Fork already typed-long-array; only the *set* sent differs. |
| Send all heightmaps | Send only `Usage.CLIENT` (3 of 6) | long-standing, enforced by `sendToClient()` | Fork over-sends; align to 3. |

**Deprecated/outdated:**
- minecraft.wiki protocol page (≤773 / "currently 775 in 26.1") — **do not trust for 776 chunk bytes.** The jar is authoritative.
- NBT-compound heightmaps — gone; do not re-add.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `fluidCount` may be sent as 0 for an all-stone superflat and the client renders fine (the *presence* of the short is what matters, not a correct count, for a fluid-free chunk) | Pitfall 1 / Code Examples | LOW — if the client validates fluidCount against contents it would only matter for chunks containing fluids (none in v1 superflat). Confirm via capture-diff + a live client standing on the chunk. |
| A2 | The client tolerates extra (non-`sendToClient`) heightmap entries, so the fork's 6-heightmap send "works" today but should be trimmed to 3 for parity | Pitfall 5 | LOW — worst case is a byte-diff mismatch, not a render failure. Trimming is the safe move. |
| A3 | The empty-light-mask bitwise-complement shortcut renders correctly for an all-present, fully-lit superflat | Pitfall 7 | MEDIUM — wrong masks can cause dark/black chunks. Capture-diff the 4 bitsets; fall back to true empty-section computation if mismatched. |
| A4 | A small view-distance ring (radius 2–3, center-out) is sufficient for "client renders, does not fall through void" in Phase 4; the full PLAY-03 spawn/game-event handshake is Phase 5 | Pattern 6 | LOW — radius is tunable; the neighbor-ring requirement is about *having* the surrounding chunks before render, which radius ≥2 satisfies. |
| A5 | `golang.org/x/sync/singleflight` is acceptable to add now (it is x/sync, explicitly sanctioned in CLAUDE.md for chunk-gen dedup), and is not a Phase-8-only concurrency dep | Standard Stack | LOW — CLAUDE.md names `singleflight` for exactly this ("two players entering the same ungenerated chunk → one generation"). |

## Open Questions

1. **Exact `fluidCount` semantics the 26.2 client enforces.**
   - What we know: vanilla writes `nonEmptyBlockCount` then `fluidCount` (jar-verified); the field counts fluid blocks in the section.
   - What's unclear: whether the client uses `fluidCount` for anything that would misrender if it's 0 in a fluid-free chunk (almost certainly not).
   - Recommendation: send 0 for the superflat stub; **capture-diff a vanilla superflat chunk** to confirm the byte and that a real client renders solid ground. Compute it properly when fluids exist (later phases).

2. **Whether to send `ChunkBatchStart/Finished` framing in Phase 4 or defer to Phase 5.**
   - What we know: the IDs exist; vanilla batches chunk sends and the client paces via `ServerboundChunkBatchReceived`.
   - What's unclear: whether an unbatched burst of `LevelChunkWithLight` (no batch markers) renders acceptably for a first-light Phase-4 verification.
   - Recommendation: send the ring with batch markers (cheap, matches vanilla, avoids a client-side rate stall). Confirm against capture.

3. **True empty-section light computation vs the complement shortcut (A3).**
   - Recommendation: ship the shortcut for the all-stone superflat; if capture-diff or a live client shows dark chunks, switch to presence-based masks.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `temp/cache/26.2-server.jar` (real vanilla server) | WORLD-02/03 capture-diff gate | ✓ | 26.2 (sha1 183c0499…/823e2250 boot) | — (this is the authoritative verification source) |
| Java 25 (Zulu/Temurin) | Run vanilla server for capture; `javap` decompile | ✓ | 25 | — |
| `bot/` client harness (`bot/world/chunks.go`) | Capture/parse a real chunk packet client-side | ✓ | in-repo | net.Pipe loopback alt |
| Docker `golang:1.26` (`CGO_ENABLED=0` host) | `-race` gate on the off-tick worker seam | ✓ | golang:1.26 | host `go test` (no race w/o cgo) |
| `golang.org/x/sync` | `singleflight` chunk-gen dedup | ✗ | — | hand-rolled in-flight map+mutex (discouraged) |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** `golang.org/x/sync` — `go get golang.org/x/sync@latest` (sanctioned by CLAUDE.md).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ Docker `-race` for the async seam) |
| Config file | none (Go convention) |
| Quick run command | `go test ./level/... ./save/... ./world/... -count=1` |
| Full suite command | `MSYS_NO_PATHCONV=1 docker run --rm -v "//d/ender://src" -w //src golang:1.26 go test ./... -race -count=1` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| WORLD-01 | Chunk load/gen off-tick, result via `applyAsyncResults` | unit + race | `go test ./world/ -run TestChunkReadyRejoin -race -x` | ❌ Wave 0 |
| WORLD-02 | Section round-trips AND matches vanilla bytes (fluid-count, no prefix, LSB, thresholds) | golden / capture-diff | `go test ./level/ -run TestSectionWireVsVanillaCapture -x` | ❌ Wave 0 |
| WORLD-02 | Section self-round-trip (necessary-not-sufficient) | unit | `go test ./level/ -run TestSectionRoundTrip -x` | ❌ Wave 0 |
| WORLD-03 | Heightmaps = typed long arrays (ids 1,4,5); light masks/arrays present | golden | `go test ./level/ -run TestChunkHeightmapsAndLight -x` | ❌ Wave 0 |
| WORLD-04 | Superflat generator deterministic (same pos → same bytes) | unit | `go test ./world/ -run TestSuperflatDeterministic -x` | ❌ Wave 0 |
| WORLD-05 | View-distance ring computes center-out neighbor set | unit | `go test ./world/ -run TestViewRingCenterOut -x` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./level/... ./save/... ./world/... -count=1` (quick, native)
- **Per wave merge:** Docker `-race -count=1` over all packages (the off-tick worker seam)
- **Phase gate:** the **capture-diff** test green (our encoder byte-matches a vanilla 26.2 superflat chunk packet) + a real-client smoke ("stands on solid ground") before `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] `level/chunk_test.go` — `TestSectionRoundTrip` + `TestChunkHeightmapsAndLight` (no chunk-level test exists today)
- [ ] `level/chunk_capture_test.go` — `TestSectionWireVsVanillaCapture`: stand up `temp/cache/26.2-server.jar` on a fixed superflat seed, capture one `ClientboundLevelChunkWithLight`, byte-diff vs our encoder. **This is the authoritative WORLD-02/03 gate.**
- [ ] `world/worker_test.go` — `TestChunkReadyRejoin` (race), `TestSuperflatDeterministic`
- [ ] `world/stream_test.go` — `TestViewRingCenterOut`
- [ ] Capture fixture: a committed golden `.bin` of the vanilla chunk packet for the chosen seed/coords (so CI doesn't need to boot Java every run)

## Security Domain

`security_enforcement` not set in `.planning/config.json` (no `security_enforcement` key; treat as not explicitly disabled, but this is an internal protocol/serialization phase with no auth/crypto surface). Relevant controls:

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V5 Input Validation | yes | Chunk **read** path (`ReadFrom`, region sectors) must bound-check: `region.mca.go` already guards negative/oversized sector lengths (`ErrSectorNegativeLength`, `ErrTooLarge`, 1MB cap). Generated/decoded section `bits`/`length` must not exceed expected (BitStorage `Fix` validates data length). |
| V6 Cryptography | no | No crypto in this phase. |
| V2/V3/V4 | no | No auth/session/access-control surface (offline-mode world data). |

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Malformed `.mca` sector (negative/huge length) | DoS / Tampering | Existing `region` guards (`ErrSectorNegativeLength`, `ErrTooLarge`, `length > 4096*num`) — keep them; never `make([]byte, length)` before the check. |
| Decode of an untrusted chunk blob allocating unbounded longs | DoS | `calcBitStorageSize` is bounded by fixed entry count (4096 / 64); reject sections whose declared bits/length don't match the expected container size (`BitStorage.Fix` returns an error). |

## Sources

### Primary (HIGH confidence)
- **26.2 server jar bytecode** (`temp/cache/26.2-server.jar` → `META-INF/versions/26.2/server-26.2.jar`, unobfuscated), decompiled with `javap -p -c`:
  - `net/minecraft/world/level/chunk/LevelChunkSection.write` — **two shorts (blockCount, fluidCount) + states + biomes**; `getSerializedSize` starts `iconst_4`.
  - `net/minecraft/world/level/chunk/PalettedContainer$Data.write` — `writeByte(bits)` + palette + **`writeFixedSizeLongArray`** (no length prefix).
  - `net/minecraft/network/protocol/game/ClientboundLevelChunkPacketData.write` — heightmaps (map codec) + VarInt-len byte[] section blob + block-entity list.
  - `ClientboundLevelChunkPacketData` static init — `HEIGHTMAPS_STREAM_CODEC = ByteBufCodecs.map(IntFn, Heightmap.Types.STREAM_CODEC, ByteBufCodecs.LONG_ARRAY)`; `lambda$new$0` filters by `sendToClient()`.
  - `Heightmap$Types` `<clinit>` + `sendToClient()` — Usage per type; CLIENT ⇒ sent (WORLD_SURFACE, MOTION_BLOCKING, MOTION_BLOCKING_NO_LEAVES).
  - `ClientboundLevelChunkWithLightPacket` — `int x, int z, chunkData, lightData`.
  - `ClientboundLightUpdatePacketData` — field order: skyYMask, blockYMask, emptySkyYMask, emptyBlockYMask, skyUpdates, blockUpdates.
- **Fork source on disk** (`level/bitstorage.go`, `level/palette.go`, `level/chunk.go`, `level/chunkstatus.go`, `save/region/mca.go`, `save/chunk.go`, `level/block/block.go`, `level/biome/list.go`, `registry/codec.go`, `bot/world/chunks.go`) — read directly.
- **Phase-3 tick seam** (`server/tick.go`, `server/tick_phases.go`, `.planning/phases/03-authoritative-tick-loop/03-03-SUMMARY.md`) — `asyncResult`/`applyAsyncResults` rejoin, `tickChunks`/`flushOutbound` stubs, single-owner discipline.
- **Project docs** (`.planning/REQUIREMENTS.md` WORLD-01..05, `.planning/ROADMAP.md` Phase 4, `.planning/PROJECT.md` known protocol shifts, `.planning/STATE.md`).

### Secondary (MEDIUM confidence)
- `go list -m golang.org/x/sync` → confirms NOT yet a dependency (must `go get`).

### Tertiary (LOW confidence)
- minecraft.wiki protocol page — **explicitly NOT trusted** for 776 chunk bytes (documents ≤773/775); listed only to mark it stale.

## Metadata

**Confidence breakdown:**
- Standard stack (which fork APIs to call): **HIGH** — read every relevant fork file; APIs confirmed on disk.
- Section/paletted-container wire layout (WORLD-02): **HIGH** — decompiled from the 26.2 jar; fluid-count, no-prefix, LSB, thresholds all jar-verified.
- Heightmaps + light (WORLD-03): **HIGH** — jar-verified codec + field order; only the *set* to send needed trimming.
- Off-tick rejoin pattern (WORLD-01): **HIGH** — the Phase-3 seam exists and was designed for this; pattern is a direct consumption.
- View-distance ring exact send order/batching (WORLD-05): **MEDIUM** — center-out ring is settled; batch-marker necessity for a first-light Phase-4 test is an open question (#2), resolvable by capture.
- Superflat generator (WORLD-04): **HIGH** — deterministic stub, scope-bounded; parity worldgen is v2.

**Research date:** 2026-06-23
**Valid until:** stable until the next Mojang protocol bump (26.3 → re-run codegen + re-capture). Treat as valid for this milestone; re-verify the fluid-count short and heightmap set against the jar on any version retarget.
