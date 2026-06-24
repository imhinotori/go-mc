---
phase: 08-leaf-concurrency-optimizations
plan: 03
subsystem: world-region-io
tags: [linear, region, zstd, codec, disk-io, compression, opt-in, race, opt-05]

# Dependency graph
requires:
  - phase: 04-world-chunk-system
    provides: "the off-tick world.Worker.loadOrGenerate/tryRegion seam + save.Chunk.Load -> level.ChunkFromSave chunk deserialization the .linear loader reuses unchanged"
  - phase: 08-leaf-concurrency-optimizations
    plan: 01
    provides: "the Wave-0 concurrency substrate (this plan adds the one external dep beyond it: pure-Go klauspost/compress/zstd)"
provides:
  - "OPT-05: the .linear region file format (whole-region zstd) extending save/region in-place alongside the UNCHANGED .mca Anvil codec — a single zstd-compressed file per 32x32 region, ~50-98% smaller than the equivalent per-chunk-zlib .mca"
  - "save/region/linear.go: WriteLinear/ReadLinear/OpenLinear/SaveLinear + LinearRegion with ReadSectorLinear/WriteSectorLinear (mca-parity single-chunk shape), the exact 0xc3ff13183cca9d9a signature + >QBQbhIQ header framing, and the decompression-bomb guard (512MiB cap)"
  - "world/worker.go format-aware loadOrGenerate: PREFERS r.<x>.<z>.linear, FALLS BACK to r.<x>.<z>.mca, else generates — opt-in (.linear write is never forced; .mca read support permanent for vanilla worlds)"
affects: [08-04-async-tracker, 08-05-async-spawning, 08-06-hot-collections]

# Tech tracking
tech-stack:
  added:
    - "github.com/klauspost/compress v1.18.6 — pure-Go zstd (Encoder.EncodeAll / Decoder.DecodeAll); the ONE new Phase-8 external dep beyond ants/xsync. No cgo: CGO_ENABLED=0 go build ./... verified clean."
  patterns:
    - "codec-only swap behind an existing off-tick seam: .linear changes the on-disk BYTES inside loadOrGenerate, not the threading — the off-tick handle goroutine + chunkReady rejoin are untouched, so no new tick-boundary crossing"
    - "whole-region framing: [leading sig u64][header >QBQbhIQ][zstd body][trailing sig u64]; body = 1024 >II(rawSize,timestamp) cell headers + concatenated present blobs, zstd-EncodeAll'd as one blob"
    - "integrity-before-decompress: both signatures (leading + trailing) and the declared body length are verified BEFORE zstd-DecodeAll runs, and the decoded size is capped (maxDecompressedSize=512MiB) — a corrupt/forged/bomb file is a surfaced error, never an OOM or a silent wrong-chunk"
    - "format-aware opt-in loader: os.Stat the .linear path first; present -> linear codec; absent -> .mca codec; both miss -> generate. Per-chunk NBT blobs are identical between codecs so save.Chunk.Load -> level.ChunkFromSave is reused unchanged."

key-files:
  created:
    - "save/region/linear.go - the .linear codec: linearSignature 0xc3ff13183cca9d9a + linearVersion 1, maxDecompressedSize 512MiB cap, LinearRegion (1024 cells indexed z*32+x), WriteLinear/ReadLinear (signature+header framing, zstd body), OpenLinear/SaveLinear (atomic temp-rename), ReadSectorLinear/WriteSectorLinear (mca-parity), shared reentrant zstd Encoder/Decoder"
    - "save/region/linear_test.go - TestLinearRoundTrip (byte-identical encode->decode, empty cells stay absent), TestLinearSignatureFraming (leading+trailing sig, version, chunkCount; bad sigs rejected), TestLinearCorruptSignatureRejected (truncated/oversized-body), TestLinearDecompressionBombRejected (over-cap body -> ErrLinearTooLarge), TestLinearSmallerThanMca (footprint comparison)"
    - "world/worker_linear_test.go - TestWorkerPrefersLinear, TestCrossFormatMcaStillLoads, TestWorkerLinearLoads, TestWorkerLinearMissGenerates, TestWorkerCorruptLinearSurfaces + current-format chunk-blob fixture"
  modified:
    - "world/worker.go - tryRegion made format-aware (prefer .linear, fall back .mca, else miss); readLinearSector + decodeChunk helpers extracted; off-tick handle goroutine + chunkReady rejoin UNCHANGED"
    - "go.mod / go.sum - klauspost/compress v1.18.6 added (only new dep)"

key-decisions:
  - "Chunk index for cell (x,z) is z*32+x, matching .mca's offsets[z][x] z-major convention, so the (cx,cz)->cell mapping is byte-identical across both codecs (region.In/region.At reused). The per-cell blobs ARE the same save.Chunk NBT blobs mca's ReadSector returns — the codec swap is at file framing, not chunk serialization."
  - "rawSize==0 means an ABSENT cell (reads back as absent, nil blob), matching the canonical linear.py and vanilla (a stored chunk always has non-empty NBT). LinearRegion.Set treats nil OR empty as clear-the-cell, so there is no lossy present-but-zero-length cell. This resolved the only spec ambiguity (the plan's initial round-trip test included a present-zero-length cell, which the format cannot represent — removed as not a real scenario; see Deviations)."
  - "Decompression-bomb guard is a 512MiB cap on the DECODED body length (the format permits up to 4GB; a tighter practical bound is chosen and documented). Verified BEFORE accepting the region: DecodeAll runs, then len(body) > cap -> ErrLinearTooLarge. Signatures + declared body length are checked before DecodeAll so a malformed frame is rejected without decompressing."
  - "Both leading AND trailing 0xc3ff13183cca9d9a signatures are verified, the trailing one BEFORE the body is decompressed (integrity gate). The header magic (the in-header repeat of the signature) and version==1 are also checked. Any mismatch/truncation -> ErrLinearCorrupt, mirroring mca's ErrTooLarge discipline."
  - ".linear is OPT-IN: the loader is format-aware but WRITE is never forced. An existing .mca world stays permanently readable (TestCrossFormatMcaStillLoads). The v1 regionDir=\"\" default (always-generate) is unaffected — verified."
  - "zstd codecs are shared package-level Encoder/Decoder (EncodeAll/DecodeAll are reentrant/thread-safe) so callers skip per-call pool setup; region.LinearRegion itself stays Not MT-Safe and is opened/decoded/closed per call inside tryRegion (same discipline as region.Region)."

patterns-established:
  - "WriteLinear: build body (1024 >II cell headers + concatenated present blobs) -> sharedLinearEncoder.EncodeAll -> frame [sig][>QBQbhIQ header][body][sig]"
  - "ReadLinear: verify leading sig + header magic + version -> read chunkCount/dataLength -> verify trailing sig (pre-decompress) -> DecodeAll under the size cap -> slice 1024 cell headers -> walk present blobs; chunkCount==present and blobBytes-fills-body are cross-checked"
  - "format-aware tryRegion: os.Stat(.linear) ? readLinearSector(OpenLinear -> ReadSectorLinear) : region.Open(.mca) -> ReadSector; both feed decodeChunk (save.Chunk.Load -> level.ChunkFromSave); corrupt surfaced, miss -> generate"

requirements-completed: [OPT-05, OPT-06]

# Metrics
duration: 22min
completed: 2026-06-24
---

# Phase 8 Plan 03: Linear Region File Format (OPT-05) Summary

The `.linear` region file format (whole-region zstd, signature `0xc3ff13183cca9d9a`) now extends `save/region` in-place alongside the unchanged `.mca` Anvil codec, wired opt-in behind the existing off-tick `world.Worker.loadOrGenerate` seam — `.linear` preferred, `.mca` fallback, else generate. Compression is pure-Go `klauspost/compress/zstd`, preserving the `CGO_ENABLED=0` static binary, and a 512MiB decompressed-size cap plus dual leading/trailing signature verification reject corrupt or decompression-bomb files as surfaced errors rather than OOMs or silent wrong-chunks.

## What Was Built

### Task 1 — the `.linear` codec (`save/region/linear.go`)

The exact 08-RESEARCH / canonical `linear.py` layout, all big-endian:

```
[leading signature  u64 = 0xc3ff13183cca9d9a]
[header ">QBQbhIQ": magic(u64=sig) version(u8=1) newestTimestamp(u64)
   compressionLevel(i8) chunkCount(i16) dataLength(u32) dataHash(u64, reserved=0)]
[zstd body, dataLength bytes]
[trailing signature u64 = 0xc3ff13183cca9d9a]
```

The zstd body decompresses to `1024 × >II(rawSize u32, timestamp u32)` cell headers (cell index `z*32+x`) followed by the concatenated raw chunk-NBT blobs of the present cells in index order. `rawSize==0` ⇒ empty/absent cell. `WriteLinear`/`ReadLinear` operate on `io.Writer`/`[]byte`; `OpenLinear`/`SaveLinear` add file IO (the save path uses an atomic temp-file rename). `LinearRegion.ReadSectorLinear`/`WriteSectorLinear` give `.linear` the same single-chunk read/write shape as mca's `ReadSector`/`WriteSector`, returning `ErrNoSector` for an absent cell.

Guards: both signatures + version + declared body length are verified before `DecodeAll`; the decoded body length is capped at 512MiB (`ErrLinearTooLarge`); `chunkCount` must equal the count of present cells and the concatenated blobs must exactly fill the body (`ErrLinearCorrupt`).

### Task 2 — format-aware off-tick loader (`world/worker.go`)

`tryRegion` now `os.Stat`s `r.<rx>.<rz>.linear` first: present ⇒ decode via the `.linear` codec; absent ⇒ the existing `.mca` path; both miss ⇒ generate. A corrupt `.linear`/`.mca` is surfaced (never regenerated over). Both codecs produce identical per-chunk NBT blobs, fed into the unchanged `save.Chunk.Load → level.ChunkFromSave` path via the new `decodeChunk` helper. The off-tick `handle` goroutine and the `chunkReady`/`results` rejoin are untouched — this is a codec-only change with no new tick-boundary crossing.

## .linear Layout Implemented + Spec Ambiguity Resolved

Implemented exactly as the canonical `xymb-endcrystalme/LinearRegionFileFormatTools` `linear.py`: leading signature, `>QBQbhIQ` header (with the signature repeated as the header magic), zstd-compressed body of 1024 `>II` cell headers + concatenated blobs, trailing signature.

One ambiguity resolved: the plan's initial round-trip behavior listed a *present-but-zero-length* cell as distinct from an absent cell. The `.linear` format encodes presence as a non-zero `rawSize`, so it genuinely cannot represent a present-zero-length cell — and vanilla never stores one (a saved chunk always carries non-empty NBT). The contract was made explicit: `rawSize==0` ⇒ absent, `LinearRegion.Set` treats nil-or-empty as clear-the-cell, and the misleading test cell was removed. Round-trip correctness for real (non-empty) blobs and absent-stays-absent is fully asserted.

## Dependency Added

`github.com/klauspost/compress v1.18.6` — pure-Go zstd (`Encoder.EncodeAll` / `Decoder.DecodeAll`, both reentrant). It is the only new external dep (verified: `go.mod` shows just this addition beyond the pre-existing ants/xsync/uuid/x-sync/x-exp set).

## CGO_ENABLED=0 Confirmation

`CGO_ENABLED=0 go build ./...` exits 0 (clean) after adding klauspost/compress — the pure-Go zstd pulls no C dependency, so the "no JVM, pure Go static binary" value prop holds. Confirmed both locally and inside the `golang:1.26` Docker `-race` run.

## Test Results

- `go test ./save/region/ -run TestLinear -count=1` — 5/5 pass (round-trip, signature framing, corrupt-signature, decompression-bomb, smaller-than-mca). `.linear` measured at ~1.7% of the equivalent `.mca` for a populated highly-compressible region.
- `go test ./world/ -run 'TestWorker|TestCrossFormat' -count=1` — all pass (prefers-linear, mca-still-loads, linear-loads, miss-generates, corrupt-surfaces) plus the pre-existing worker tests.
- `go test ./save/... ./world/... -count=1` — all green (existing mca + chunk + level tests unaffected; `.linear` is purely additive).
- `go vet ./save/... ./world/...` — clean.
- `go build ./...` and `CGO_ENABLED=0 go build ./...` — both exit 0.

## -race Result

`MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./world/... ./save/... -count=1` — CLEAN:

```
ok  github.com/imhinotori/sulfur/world         2.587s
ok  github.com/imhinotori/sulfur/save          3.857s
ok  github.com/imhinotori/sulfur/save/region   6.282s
```

The off-tick codec swap introduces no new race; the shared zstd codecs are reentrant and `LinearRegion` is opened/decoded per call.

## Deviations from Plan

**[Rule 1 - Bug] Removed the present-but-zero-length test cell (spec ambiguity)**
- Found during: Task 1 (TestLinearRoundTrip first run)
- Issue: The plan's behavior listed a present-zero-length cell as distinct from an absent cell, but the `.linear` format encodes presence as non-zero `rawSize` and cannot represent it (nor does vanilla store such a chunk).
- Fix: Documented `rawSize==0 ⇒ absent` as the contract; `LinearRegion.Set` treats nil-or-empty as clear; removed the impossible test cell. Real-blob round-trip + absent-stays-absent remain fully asserted.
- Files modified: save/region/linear.go (Set doc/behavior), save/region/linear_test.go (test cell removed)
- Verification: TestLinearRoundTrip passes.
- Commit: a6fba87b / fa2a5169

**[Rule 3 - Blocking] Worker-test chunk fixture built from a minimal current-format blob**
- Found during: Task 2 (worker tests)
- Issue: The committed testdata `.mca` carries pre-776 block ids (`minecraft:grass`) that `level.ChunkFromSave` cannot map, and `save.Chunk.Data` on a freshly-generated chunk fails encoding empty `nbt.RawMessage` tick fields — both unrelated to the `.linear` codec.
- Fix: The test fixture generates a superflat chunk, runs `level.ChunkToSave`, then encodes only the fields `ChunkFromSave` consumes (Sections/Heightmaps/Status/YPos) with compression tag 3, producing a clean current-format blob that round-trips through both codecs' loader path.
- Files modified: world/worker_linear_test.go
- Verification: all Task 2 tests pass.
- Commit: 12e15e52

**[Commit hygiene] Task 2 feat folded into the test commit**
- The `world/` directory is matched by a pre-existing `.gitignore` rule (`/world/`, intended for a runtime world-save dir, not the Go package). The new test file needed `git add -f`; in the process the modified tracked `world/worker.go` was committed together with `world/worker_linear_test.go` under the `test(08-03)` message rather than a separate `feat(08-03)` commit. Content is fully committed and correct; only the commit split differs from strict TDD. No history rewrite performed (avoids risk on a shared branch).

**Total deviations:** 2 auto-fixed (1 spec-ambiguity bug, 1 blocking test-fixture) + 1 commit-hygiene note. **Impact:** none on correctness or the delivered codec; all verifications green and `-race` clean.

## Issues Encountered

None blocking. Note: the `/world/` `.gitignore` rule shadows the Go `world/` package source (new files in it require `git add -f`). Out of scope for this plan — logged here for visibility; a future cleanup should narrow that ignore rule to the runtime save directory only.

## Next Phase Readiness

OPT-05 complete. Wave-1 sibling 08-02 (OPT-01 async pathfinding) is done; this plan is file-disjoint from it (different packages). Ready for the next Phase-8 plan (08-04 async entity tracker).

## Self-Check: PASSED

- Created files verified on disk: save/region/linear.go, save/region/linear_test.go, world/worker_linear_test.go, 08-03-SUMMARY.md (all FOUND); world/worker.go modified.
- Commits verified in history: ced9a2e9 (build dep), fa2a5169 (test codec), a6fba87b (feat codec), 12e15e52 (Task 2 test+feat).
- Plan verification re-run: `go build ./...` + `CGO_ENABLED=0 go build ./...` exit 0; `go test ./save/... ./world/...` green; `go vet ./save/... ./world/...` clean; Docker `-race ./world/... ./save/...` clean; go.mod shows only klauspost/compress added.
