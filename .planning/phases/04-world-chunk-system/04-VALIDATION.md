---
phase: 4
slug: world-chunk-system
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-23
---

# Phase 4 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

This phase is **~90% reuse of correct fork primitives** (`level.PaletteContainer`, `level.BitStorage`, `level.heightMapEntry`, `level.lightData`, `save.Chunk`, `save/region.Region`, `registry.DimensionType`) plus **two surgical wire fixes** in `level/chunk.go` and **one new `world/` subsystem** (chunk manager + superflat generator + off-tick worker + view-distance streaming + Chunk Data/Light packet assembly).

The decisive validation fact for this phase: **a Go self-round-trip is NOT the WORLD-02 correctness proof.** The fork's `Section.WriteTo` and `Section.ReadFrom` are *symmetrically* wrong (both omit the per-section fluid-count short, jar-confirmed), so a Go encode→decode round-trip **passes on a wire the client rejects** (stripes/void). The authoritative WORLD-02/03 gate is therefore a **capture-diff against the real vanilla 26.2 server** (`temp/cache/26.2-server.jar`, sha1 `823e2250…`, JDK 25, fixed superflat seed) — byte-comparing Sulfur's `ClientboundLevelChunkWithLight` against vanilla's for the same chunk — plus a **real-client visual smoke** (an unmodified 26.2 client stands on solid ground, no void/stripes). This is the same capture-diff method that caught the Phase-2 registry/login bugs the `net.Pipe` bot tolerated.

Self-round-trip and golden-length tests remain valuable as **cheap regression** (they catch a re-introduced asymmetry or a length-math error fast and natively) — they are necessary, not sufficient.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` (+ Docker `-race` for the off-tick worker seam) |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go build ./... && go test ./level/... ./save/... ./world/... ./server/... -count=1` |
| **Full suite command** | `go vet ./... && go build ./... && go test ./... -count=1` then the race gate below |
| **Race gate (off-tick worker)** | `MSYS_NO_PATHCONV=1 docker run --rm -v "//d/ender://src" -w //src golang:1.26 go test ./world/... ./server/... -race -count=1` (host is `CGO_ENABLED=0`; `-race` needs cgo → Docker, as established in Phases 2–3) |
| **Capture-diff harness** | The authoritative WORLD-02/03 gate: stand up the cached vanilla `temp/cache/26.2-server.jar` (JDK 25) on a fixed superflat preset/seed, capture one `ClientboundLevelChunkWithLight`, byte-compare against Sulfur's encoder output for the same chunk coords. The captured vanilla packet is committed as a golden `.bin` so CI does not boot Java every run. |
| **Real-client visual smoke** | An unmodified vanilla 26.2 client connects to `cmd/sulfur` and renders solid ground (no void/stripes) — the WORLD-05 / phase-gate check, mirroring Phase 2's NET-04 human sign-off. |

---

## Sampling Rate

- **After every task commit:** `go build ./... && go test ./level/... ./save/... ./world/... ./server/... -count=1` (native, fast).
- **After the off-tick worker task and the tick-wiring task:** add the Docker `-race` gate over `./world/... ./server/...` (the `chunkReady` rejoin seam — prove no game-state crosses the boundary mutably).
- **After every plan wave:** `go vet ./... && go build ./... && go test ./... -count=1` + the `-race` gate.
- **Phase gate (before `/gsd-verify-work`):** the **capture-diff** test green (Sulfur's chunk packet byte-matches vanilla 26.2 on the fixed superflat chunk) **AND** the real-client visual smoke signed off (a vanilla client stands on solid ground, no void/stripes).
- **Max feedback latency:** ~60–90s for the automated suite; the capture-diff/real-client check is the phase gate, not a per-task check.

---

## Per-Task Verification Map

| Req ID | Plan | Wave | Behavior | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|--------|------|------|----------|------------|-----------------|-----------|-------------------|-------------|--------|
| WORLD-02 | 04-01 | 1 | `Section.WriteTo`/`ReadFrom` write/read TWO shorts (blockCount, fluidCount) before the states container; states use `PaletteContainer` (no length prefix, LSB) | T-4-04 (decode of untrusted section over-allocating) | `BitStorage.Fix`/palette decode rejects a section whose declared bits/length mismatch the fixed 4096 entry count — never `make` before the check | unit (self-round-trip + golden length, necessary-not-sufficient) | `go test ./level/ -run 'TestSectionRoundTrip\|TestSectionByteLength' -count=1` | ❌ W0 | ⬜ pending |
| WORLD-03 | 04-01 | 1 | `Chunk.WriteTo` emits only the 3 `sendToClient` heightmaps (ids 1,4,5); light masks/arrays present & field-ordered | — | Heightmap entry count is exactly 3; no WORLDGEN/LIVE_WORLD ids on the wire | unit (golden) | `go test ./level/ -run TestChunkHeightmapsClientSet -count=1` | ❌ W0 | ⬜ pending |
| WORLD-04 | 04-02 | 2 | Superflat generator is a pure deterministic function: same `(cx,cz)` → identical chunk bytes; solid stone column, bedrock floor, air above; heightmaps + biome filled | — | No RNG / seed-derived only; section count derived from `dimType.Height/16`, never hard-coded | unit | `go test ./world/ -run 'TestSuperflatDeterministic\|TestSuperflatSectionCount' -count=1` | ❌ W0 | ⬜ pending |
| WORLD-02 | 04-02 | 2 | `WriteLevelChunkWithLight` assembles `Int x, Int z, chunkData, lightData` over a generated chunk; section blob round-trips | T-4-01 (huge view → OOM) | Packet assembly is bounded by the derived section count; no unbounded allocation from a chunk request | unit | `go test ./world/ -run TestLevelChunkPacketAssembly -count=1` | ❌ W0 | ⬜ pending |
| WORLD-01 | 04-02 | 2 | Off-tick worker loads (region) or generates a chunk and emits an immutable `ChunkResult` on its results channel; `singleflight` dedups concurrent requests for the same key | T-4-02 (gen spam unbounded goroutines), T-4-03 (malformed `.mca`) | One generation per key (singleflight); region read uses the existing `ErrSectorNegativeLength`/`ErrTooLarge`/1MB guards — never `make([]byte, length)` before the bound check | unit + race | `go test ./world/ -run 'TestWorkerSingleflightDedup\|TestWorkerEmitsResult' -count=1` (+ Docker `-race`) | ❌ W0 | ⬜ pending |
| WORLD-01 | 04-03 | 3 | A `chunkReady` (`asyncResult`) drains in `applyAsyncResults` on the tick owner and inserts the immutable chunk into the tick-owned `ChunkManager` — no off-tick mutation of tick state | T-4-05 (game-state pointer escaping the tick) | Only the immutable `*level.Chunk` crosses the boundary; the tick goroutine is the sole mutator of the manager map (single-owner, like `players`) | unit + race | `go test ./server/ -run 'TestChunkReadyRejoin' -race -count=1` (Docker) | ❌ W0 | ⬜ pending |
| WORLD-05 | 04-03 | 3 | `tickChunks` computes the per-player center-out neighbor ring by view distance; `flushOutbound` sends SetChunkCacheCenter → ChunkBatchStart → N× LevelChunkWithLight → ChunkBatchFinished | T-4-01 (huge view distance), T-4-06 (position spam) | View distance is server-clamped (radius cap); the needed-ring set is bounded by the clamp regardless of the client's requested view or position-update rate | unit | `go test ./server/ -run 'TestViewRingCenterOut\|TestViewDistanceClamp' -count=1` | ❌ W0 | ⬜ pending |
| WORLD-02 / WORLD-03 | 04-04 | 4 | Sulfur's `ClientboundLevelChunkWithLight` byte-matches a real vanilla 26.2 superflat chunk; an unmodified client renders solid ground | T-4-04 | The wire is vanilla-correct (fluid-count short present, 3 heightmaps, no prefix, LSB, correct masks) — proven against the jar, not self-consistency | golden / capture-diff + **human-verify** | `go test ./level/ -run TestSectionWireVsVanillaCapture -count=1` + manual vanilla capture-diff & real-client smoke | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Test scaffolds and shared fixtures that MUST exist before the corresponding implementation task runs. Each plan creates its own test file (RED) before/with the implementation; the capture fixture is committed once.

- [ ] **`level/chunk_test.go`** (Plan 04-01) — `TestSectionRoundTrip` (two-short symmetric write/read), `TestSectionByteLength` (a known section encodes to `2 (blockCount) + 2 (fluidCount) + 1 (bits) + paletteBytes + 8*calcBitStorageSize(...)`), `TestChunkHeightmapsClientSet` (exactly 3 entries, ids 1/4/5). No chunk-level test exists today.
- [ ] **`level/chunk_capture_test.go`** (Plan 04-04) — `TestSectionWireVsVanillaCapture`: load the committed vanilla golden `.bin`, byte-diff against Sulfur's encoder for the same superflat chunk. **The authoritative WORLD-02/03 gate.** Skips with a clear message if the golden fixture is absent (so native CI stays green; the capture is the phase gate).
- [ ] **`world/generator_test.go`** (Plan 04-02) — `TestSuperflatDeterministic`, `TestSuperflatSectionCount`.
- [ ] **`world/packet_test.go`** (Plan 04-02) — `TestLevelChunkPacketAssembly`.
- [ ] **`world/worker_test.go`** (Plan 04-02) — `TestWorkerSingleflightDedup`, `TestWorkerEmitsResult` (run under Docker `-race`).
- [ ] **`server/world_stream_test.go`** (Plan 04-03) — `TestViewRingCenterOut`, `TestViewDistanceClamp`, `TestChunkReadyRejoin` (race).
- [ ] **Capture fixture** (Plan 04-04) — a committed golden `.bin` of the vanilla 26.2 `ClientboundLevelChunkWithLight` for the chosen superflat preset + chunk coords, plus `WORLD-CAPTURE-DIFF.md` recording the diff method, the chosen seed/preset/coords, and the resolution of the three open questions (fluidCount=0 for superflat, ChunkBatchStart/Finished necessity, empty-light masks).

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Sulfur's chunk + light wire byte-matches a **real** vanilla 26.2 server, and an unmodified 26.2 client renders solid ground (no void/stripes) | WORLD-02, WORLD-03, WORLD-05 | No published spec covers proto 776 chunk bytes; the fork's symmetric read/write hides the fluid-count bug from any self-round-trip. Only a byte-comparison against the actual vanilla server output + a real client rendering correctly proves vanilla-correctness. | Stand up `temp/cache/26.2-server.jar` (sha1 `823e2250…`, JDK 25) on a fixed superflat preset/seed. Capture its `ClientboundLevelChunkWithLight` for a known chunk (logging proxy, or point the fork `bot/` client at vanilla and dump the packet). Capture Sulfur's packet for the same chunk. Diff: the two per-section shorts (blockCount, **fluidCount**), the states/biomes palette bytes (no length prefix, LSB longs), the heightmap entry **set** (must be exactly ids 1/4/5), the four light bitsets + the updates lists. Resolve the 3 open questions from the capture. Write `WORLD-CAPTURE-DIFF.md`. Then connect an unmodified vanilla 26.2 client to `cmd/sulfur` and confirm it stands on solid stone ground — no void, no stripes. |

---

## Validation Sign-Off

- [ ] Every task has an `<automated>` verify command or an explicit Wave 0 dependency (Nyquist).
- [ ] Sampling continuity: no 3 consecutive tasks without an automated build/test check.
- [ ] The off-tick worker task and the tick-wiring task run under Docker `-race`.
- [ ] Wave 0 stands up the per-requirement test scaffolds before implementation; the vanilla capture fixture is committed before the capture-diff test runs.
- [ ] The WORLD-02/03 capture-diff against a real vanilla 26.2 server is a required phase gate (human sign-off in 04-04), NOT a self-round-trip.
- [ ] No watch-mode flags.
- [ ] Feedback latency < 90s for the automated suite (vanilla capture-diff excluded — it is the phase gate).
- [ ] `nyquist_compliant: true` (every WORLD-0x behavior maps to an automated command + a Wave 0 scaffold; WORLD-02/03 vanilla-correctness is the one prescribed manual gate, justified by the symmetric-bug research finding).

**Approval:** pending
