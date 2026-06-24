# WORLD-02/03 Capture-Diff: Sulfur vs. Vanilla 26.2 Superflat Chunk

**Phase 4, Plan 04 — the authoritative WORLD-02/03 wire gate.**

This document records the byte-for-byte comparison of Sulfur's
`ClientboundLevelChunkWithLight` encoder output against a real vanilla
Minecraft 26.2 server's, captured for the same superflat chunk. It is the only
authoritative correctness check for protocol 776 chunk bytes — no published spec
covers 776, and the fork's `Section.WriteTo`/`ReadFrom` are *symmetric*, so every
self-round-trip test in Plans 04-01/02/03 passes even on a wire a real client
rejects. The capture-diff exposed exactly such a hidden divergence (see
**Wire Fixes** below).

Status: **automatable capture/diff COMPLETE. Awaiting human real-client sign-off
(Task 2).**

---

## 1. Capture Method (reproducible)

### Vanilla 26.2 server (the golden source)

- Jar: `temp/cache/26.2-server.jar`, sha1 `823e2250d24b3ddac457a60c92a6a941943fcd6a`.
- Java: Zulu 25.0.3 (`java -Xmx2G -jar server.jar nogui`).
- Scratch dir: `temp/vanilla-scratch/` (gitignored), `eula=true`.
- `server.properties` (the load-bearing keys):

  ```
  level-type=minecraft:flat
  level-seed=144
  online-mode=false
  server-port=25599
  view-distance=4
  generate-structures=false
  gamemode=creative
  ```

  Default flat preset = bedrock + 2 × dirt + grass_block (4 solid layers at the
  bottom of section 0), plains biome, everything above is air.

### Sulfur server (the encoder under test)

- `cmd/sulfur` built from this branch, run `-addr :25600`.
- Superflat profile (`cmd/sulfur/main.go`): `secs=24`, `minY=-64`,
  `surfaceY=-48` → a bedrock floor at y=-64 and solid **stone** from y=-63 up to
  y=-48 (16 solid layers), plains biome, full sky light, the 3 CLIENT heightmaps.

### Capture harness

`temp/chunkcapture/` (gitignored, built on the fork's `net`/`bot`/`packetid`,
adapted from the Phase-2 `temp/captureclient`): handshake (proto 776) → offline
login → full Configuration leg (echo Known Packs, ack Finish) → **Play**, where it
confirms the spawn teleport (`ServerboundAcceptTeleportation`), paces chunk
batches (`ServerboundChunkBatchReceived`), and dumps the RAW body of the first
`ClientboundLevelChunkWithLight` for chunk **(0,0)** to the file named by `$DUMP`.

```
# vanilla:
DUMP=temp/captures/vanilla-chunk-0-0.bin temp/chunkcapture/chunkcapture.exe 127.0.0.1:25599 VanCap
# sulfur:
DUMP=temp/captures/sulfur-chunk-0-0.bin  temp/chunkcapture/chunkcapture.exe 127.0.0.1:25600 SulCap
```

The vanilla (0,0) body is committed as
`.planning/phases/04-world-chunk-system/fixtures/vanilla-superflat-chunk.bin`
(7280 bytes, sha1 `3dea4e10a79c8eb938c2d414dc1fdf7e60e20603`) so CI byte-diffs
without booting Java each run. `temp/chunkdiff/` is the field-by-field parser used
for the tables below.

Captured chunk = **(0,0)**, body = everything after the packet-id varint
(`Int x, Int z, heightmaps, section blob, block entities, light`).

---

## 2. Per-Field Diff (chunk (0,0))

Legend: **MATCH** = byte-structure identical; **CONTENT** = framing identical,
block/biome/light *values* differ (expected — different generators); **FIXED** =
a Sulfur divergence the capture exposed and this plan corrected.

| Field | Vanilla | Sulfur (after fixes) | Verdict |
|-------|---------|----------------------|---------|
| `Int x, Int z` | `0, 0` | `0, 0` | MATCH |
| Heightmap entry count | 3 | 3 | MATCH |
| Heightmap ids | {1, 4, 5} (WORLD_SURFACE, MOTION_BLOCKING, MB_NO_LEAVES) | {1, 4, 5} | MATCH |
| Heightmap entry shape | VarInt type + VarInt-len `long[]` (37 longs each) | VarInt type + VarInt-len `long[]` (37 longs each) | MATCH |
| Section count | 24 (= dimType.Height/16) | 24 | MATCH |
| **Section short #1 (blockCount)** | present (`1024` in sec 0) | present (`4096` sec 0 / `256` sec 1) | MATCH (framing) / CONTENT (value) |
| **Section short #2 (fluidCount)** | **present**, `0` | **present**, `0` | MATCH — *Open Q1 resolved* |
| States container header | `UnsignedByte bits` (sec 0: `4`) | `UnsignedByte bits` (sec 0: `4`) | MATCH |
| States data longs | no length prefix; `256` longs for bits=4 | no length prefix; `256` longs for bits=4 | MATCH — *and the bug FIXED* |
| States palette | indirect (palLen 4: air/bedrock/dirt/grass) | indirect (palLen 3: air/bedrock/stone) | CONTENT (block ids differ) |
| Biomes container | single-valued (`bits=0`, 1 palette id, 0 longs) | single-valued (`bits=0`, 1 palette id, 0 longs) | MATCH — *and the bug FIXED* |
| Block entities | count `0` | count `0` | MATCH |
| Light field order | sky/block/emptySky/emptyBlock masks, sky/block updates | same order | MATCH |
| Light masks form | selective (`skyMask=0x6`, `emptySky=0x1`) | full-present (`skyMask=0xffffff`, `emptySky=^skyMask`) | CONTENT — *Open Q3 resolved* |
| Light arrays | 2 sky arrays × 2048 B | 24 sky arrays × 2048 B | CONTENT |
| **Whole-body framing** | **0 trailing bytes** (parses clean) | **0 trailing bytes** (parses clean) | MATCH |

Vanilla body = **7280 bytes**; Sulfur body = **56453 bytes**. The size gap is
**entirely content**, not framing: (a) Sulfur fills 16 solid stone layers
(blockCount 4096 vs vanilla's 1024) and (b) Sulfur sends a full 2048-byte sky
array for all 24 sections (49 152 B) where vanilla sends 2. Both bodies parse to
**zero trailing bytes** with the real per-field walk — the wire structure matches.

### The decisive framing assertion (why the size gap is harmless)

The `level/chunk_capture_test.go::TestSectionWireVsVanillaCapture` test walks both
bodies as a *client* would — reading each section's two shorts, then the states
header bits-per-entry, then deriving the long count **from that header** (no
length prefix). For both vanilla and Sulfur the derived long count equals the
packed long count and the body consumes to exactly zero trailing bytes. That is
the WORLD-02 proof: the section is framed identically to vanilla.

---

## 3. Wire Fixes Found by the Capture-Diff

The capture exposed **two** Sulfur encoder divergences that every symmetric
self-round-trip test passed over. Both were fixed in the owning file
(`level/palette.go` — the paletted-container codec) and the world generator
(`world/generator.go`); the affected tests + the capture-diff were re-run.

### Fix 1 — `PaletteContainer.Set` wrote the wrong header bits-per-entry (STRIPES/VOID)

**Symptom (first capture):** Sulfur's section 0 states container wrote header
`bits=1` but packed **256 longs** (a 4-bit-wide data array). A vanilla client
reading `bits=1` expects `ceil(4096 / 64) = 64` longs, mis-frames after 64, and
renders stripes or rejects the chunk (void). The fork's symmetric `ReadFrom`
re-floors `bits` to 4 on read, so it read 256 longs too — the round-trip was
green on a broken wire (the textbook 776 trap).

**Root cause:** `PaletteContainer.Set`'s resize path set the container's `bits`
field (and thus the wire header, `WriteTo` emits `UnsignedByte(p.bits)`) to the
*requested* width `vv` returned by `palette.id()`, while the data `BitStorage`
and palette used the *floored* width `config.bits(vv)` (states floor: 1..4 → 4).
Header said 1, data was 4-bit.

**Fix (`level/palette.go`):**

```go
storedBits := p.config.bits(vv)        // the floored stored width
newContainer := PaletteContainer[T]{
    bits:    storedBits,               // was: vv  (the wire-header bug)
    config:  p.config,
    palette: p.config.create(vv),
    data:    NewBitStorage(storedBits, length, nil),
}
```

**Verification:** post-fix, Sulfur's section 0 encodes header `bits=4` + 256 longs
— byte-structurally identical to vanilla's `bits=4` + 256 longs. The capture-diff
test asserts `headerBits ⇒ packedLongCount` for every section and was confirmed
**non-vacuous**: temporarily reverting to `bits: vv` makes the test FAIL with
`read sulfur states dataLong: unexpected EOF`.

### Fix 2 — superflat biome container carried a phantom palette entry

**Symptom (first capture):** Sulfur's per-section biomes encoded as a 2-entry
linear palette `[badlands(0), plains(40)]` with a data long, where vanilla emits
a **single-valued** container (`bits=0`, one palette id, zero longs) for a
uniform-biome section. Byte-valid and renders (all cells resolve to plains), but
not vanilla-shaped.

**Root cause:** the generator filled biomes by calling `Biomes.Set(cell, plains)`
on the fresh single-value container; the first `Set` resizes single→linear and
carries the stale default biome (id 0 = badlands) into the palette as a phantom
index-0 entry.

**Fix (`world/generator.go`):** construct the section's biomes as single-valued
plains directly, matching vanilla:

```go
s.Biomes = level.NewBiomesPaletteContainer(4*4*4, g.plains) // single-value, no phantom
```

**Verification:** post-fix, Sulfur's biomes encode `bits=0, palLen=1, longs=0` —
byte-identical to vanilla's single-valued biome container. Sulfur body shrank
7280→ section-blob 4533→4293 bytes for this change.

---

## 4. The Three Open Questions — Resolved from the Capture

### Q1 — `fluidCount` value the 26.2 client enforces

**Resolved: vanilla writes the second short as `0` for a fluid-free section, and
Sulfur sending `0` matches byte-for-byte.** The vanilla section-0 container
decodes as `blockCount=1024, fluidCount=0`; Sulfur's decodes as
`blockCount=4096/256, fluidCount=0`. The *presence and position* of the second
short is the load-bearing fact (the 04-01 fix), and it aligns. `fluidCount=0` is
correct for the fluid-free superflat; a real client renders it (Task 2 confirms).

### Q2 — `ChunkBatchStart/Finished` framing

**Resolved: vanilla DOES bracket the chunk send with batch markers.** The capture
harness observed `ClientboundChunkBatchStart` … N × `ClientboundLevelChunkWithLight`
… `ClientboundChunkBatchFinished` from the vanilla server and had to ack with
`ServerboundChunkBatchReceived` to keep it streaming. Sulfur (Plan 04-03) sends
the same markers (`world.ChunkBatchStart`/`ChunkBatchFinished` around the
center-out ring). The harness drove both servers through the identical batch
flow. Keeping the markers is correct and matches vanilla.

### Q3 — Empty-light masks: bitwise-complement shortcut vs presence-based

**Resolved: the shortcut is byte-valid and internally consistent for the
all-present superflat; it diverges from vanilla in COMPACTNESS, not correctness.**

- Vanilla (selective): `skyMask=0x6` (sections 1,2 carry explicit arrays),
  `emptySkyMask=0x1` (section 0 below-floor is empty), sections 3–23 implicit;
  it sends only **2** sky arrays.
- Sulfur (`level/chunk.go` `bitSetRev` shortcut): `skyMask=0xffffff` (all 24
  present sections carry a full-bright array), `emptySkyMask = ^skyMask`
  (`0x…ff000000` — bits 24–63, the *unused* section slots). The complement does
  **not overlap** the present sections (0–23), so no section is marked both
  present and empty — the masks are self-consistent. Sulfur sends **24** sky
  arrays, every one a full-bright 2048-byte block.

Both forms are valid 776 light framing (field order matches
`ClientboundLightUpdatePacketData` exactly, all arrays are the fixed 2048 bytes).
Sulfur's form is simply verbose (the 49 KB size gap). Because every present
section ships a full-bright sky array, the superflat renders fully lit. The
shortcut is retained for v1 per research A3/Pitfall 7. **If the Task-2 real-client
test shows dark/black chunks**, switch `level/chunk.go` `lightData` to vanilla's
selective presence-based masks (mark only sections whose light differs from the
implicit default and omit the rest) — the fix would land in `level/chunk.go` and
re-run 04-01's tests. The capture shows this is an optimization, not a blocker.

---

## 5. Gate Results (automatable half — all green)

| Gate | Command | Result |
|------|---------|--------|
| Capture-diff | `go test ./level/ -run TestSectionWireVsVanillaCapture -count=1` | PASS (asserts Sulfur framing == vanilla golden) |
| Non-vacuous proof | reverted Fix 1 → test | FAIL (`unexpected EOF`) → restored | PASS |
| Section regressions | `go test ./level/ -run 'TestSectionRoundTrip\|TestChunkHeightmapsClientSet\|TestSectionByteLength'` | PASS |
| World package | `go test ./world/... -count=1` | PASS |
| Full suite | `go test ./... -count=1` | PASS |
| `-race` (Docker) | `docker run … golang:1.26 go test ./level/... ./world/... ./server/... -race` | PASS |
| Build + vet | `go build ./...`, `go vet ./level/...` | clean |

Zero new dependencies. Fixes confined to `level/palette.go` (the owning codec)
and `world/generator.go` (the generator); no generated files were hand-edited.

---

## 6. Real-Client Smoke (Task 2 — PENDING human sign-off)

The decisive WORLD-02/03/05 check cannot be self-approved: an **unmodified vanilla
26.2 client** must connect to `cmd/sulfur` (`localhost:25565`) and stand on solid,
visible ground — no void (does not fall through), no stripes (no misaligned
palette), not stuck at "Loading terrain…". `cmd/sulfur` builds, listens on proto
776, streams the center-out superflat ring with batch framing (Plan 04-03), and is
`-race` clean.

**Sign-off line (filled in after the real-client test):**

> _Reviewed by: ____________  Date: __________
> Result: [ ] solid ground, no void/stripes (APPROVED)  [ ] divergence: _________
