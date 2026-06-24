# PLAY-01/02/05 Capture-Diff: Sulfur vs. Vanilla 26.2 (Early-Play Tail)

**Phase 5, Plan 05-03 — the authoritative PLAY-01/02/05 wire gate.**

This document records the byte-for-byte comparison of Sulfur's early-Play tail
encoders — `ClientboundPlayerInfoUpdate` (the self tab-list entry),
`ClientboundSetDefaultSpawnPosition` (the 26.x RespawnData/GlobalPos restructure),
and `ClientboundForgetLevelChunk` (the jar-derived packed-Long) — against a real
vanilla 26.2 server's, captured for an identical superflat join. It is the only
authoritative correctness check for these proto-776 layouts: no published spec
covers 776 (the wiki documents ≤773), the `PlayerInfoUpdate` entry sub-encoding
and the 26.x `RespawnData`/`WorldClock` restructures are jar-confirmed in SHAPE but
their exact bytes drift between versions, and the fork's `pk.*` codecs are
*symmetric*, so a self-round-trip passes even on a wire a real client rejects.

Status: **APPROVED — the capture-diff sealed Sulfur's three uncertain encoders
byte-for-byte against the vanilla golden (no divergence) AND an unmodified vanilla
26.2 client WALKS AROUND a following world, appears in its tab list, and is not
kicked (Task 2 signed off). PLAY-01..06 first-playable milestone met.**

---

## 1. Capture Method (reproducible)

### Vanilla 26.2 server (the golden source)

- Jar: `temp/cache/26.2-server.jar`, sha1 `823e2250d24b3ddac457a60c92a6a941943fcd6a`.
- Java: Zulu 25.0.3 (`java -Xmx2G -jar server.jar nogui`).
- Scratch dir: `temp/vanilla-scratch/` (gitignored), `eula=true`, port `25599`.
- `server.properties` (the load-bearing keys):

  ```
  level-type=minecraft:flat
  level-seed=144
  online-mode=false
  enforce-secure-profile=false
  enable-code-of-conduct=false
  server-port=25599
  view-distance=4
  generate-structures=false
  gamemode=creative
  ```

  Default flat preset = bedrock + 2 × dirt + grass_block; vanilla's superflat
  default spawn lands at block **(0, -60, 0)**.

### Sulfur server (the encoder under test)

- `cmd/sulfur` built from this branch, run `-addr :25600`.
- Superflat profile (`cmd/sulfur/main.go`): `minY=-64`, `surfaceY=-48` (solid stone
  to y=-48), so Sulfur's spawn block is **(0, -48, 0)**.

### Capture harness

`temp/playcapture/` (gitignored, built on the fork's `net`/`bot`/`packetid`,
adapted from the proven Phase-4 `temp/chunkcapture/`): handshake (proto 776) →
offline login → full Configuration leg (echo Known Packs, ack Finish) → **Play**,
where it confirms the spawn teleport (`ServerboundAcceptTeleportation`), signals
`ServerboundPlayerLoaded`, paces chunk batches, and dumps the RAW BODY (everything
after the packet-id varint) of each early-Play tail packet to
`$DUMP_DIR/<prefix>-<kind>.bin`.

```
# vanilla:
DUMP_DIR=temp/captures temp/playcapture/playcapture.exe 127.0.0.1:25599 VanCap vanilla
# sulfur:
DUMP_DIR=temp/captures temp/playcapture/playcapture.exe 127.0.0.1:25600 SulCap sulfur
```

The vanilla `PlayerInfoUpdate` (the 33-byte ADD_PLAYER entry) and
`SetDefaultSpawnPosition` bodies are committed as golden fixtures under
`.planning/phases/05-player-session-in-world-first-playable/fixtures/` so CI
byte-diffs without booting Java every run. `server/play_join_capture_test.go`
(`TestPlayBytesVsVanillaCapture`) is the field-by-field parser/asserter.

| Fixture | Bytes | sha1 |
|---------|-------|------|
| `vanilla-player-info-update.bin` | 33 | `fb755b04621b454e535b94b8974ba42e4779ded2` |
| `vanilla-set-default-spawn-position.bin` | 36 | `0d4e0e1ca7cf600f5f1a4f041973359f62c40bed` |
| `vanilla-forget-level-chunk.bin` | 8 | `13ca0cc5b1235181cd44dddd8afd01dcab5e00d9` (jar-derived, see §4) |

---

## 2. Per-Field Diff

Legend: **MATCH** = byte-structure identical; **CONTENT** = framing identical,
*value* differs (expected — different game mode / surface height / action set);
**SEALED** = a MEDIUM-confidence encoder confirmed against vanilla bytes.

### 2.1 ClientboundPlayerInfoUpdate (the self tab-list entry — PLAY-05)

Raw bodies (after the packet-id varint):

```
vanilla: ff 01 ccab718ea8fa39ca984aac00b6592400 06 56616e436170 00 00 01 01 00 00 00 00
sulfur : 0d 01 3b368b4882b83521b51fff1c1ebc1022 06 53756c436170 00 00 01 01
```

| Field | Vanilla | Sulfur | Verdict |
|-------|---------|--------|---------|
| **action mask (1 byte)** | `0xff` (all 8 actions) | `0x0d` (ADD_PLAYER\|UPDATE_GAME_MODE\|UPDATE_LISTED) | **MATCH framing (1-byte mask for ≤8 actions)** / action-SET differs (documented) |
| entry count | VarInt `1` | VarInt `1` | MATCH |
| entry UUID | 16 raw bytes | 16 raw bytes | MATCH |
| ADD_PLAYER name | String `"VanCap"` | String `"SulCap"` | MATCH (framing) / value differs |
| **ADD_PLAYER property count** | **VarInt `0`** | **VarInt `0`** | **SEALED — the count-prefix sub-encoding is byte-identical** |
| INITIALIZE_CHAT | optional present `0` | (action not sent) | vanilla-only action |
| UPDATE_GAME_MODE | VarInt `1` (creative) | VarInt `0` (survival) | MATCH (framing) / value differs |
| UPDATE_LISTED | Boolean `1` | Boolean `1` | MATCH |
| UPDATE_LATENCY | VarInt `0` | (action not sent) | vanilla-only action |
| UPDATE_DISPLAY_NAME | optional present `0` | (action not sent) | vanilla-only action |
| UPDATE_LIST_ORDER | VarInt `0` | (action not sent) | vanilla-only action |
| UPDATE_HAT | Boolean `0` | (action not sent) | vanilla-only action |
| **Whole-body framing** | 33 bytes, **0 trailing** | 28 bytes, **0 trailing** | **MATCH** |

**The decisive seal:** vanilla's join entry carries the **full 8-action set**
(`mask 0xff`) and runs in creative; Sulfur sends the **minimal listed self-entry**
(`mask 0x0d`) in survival. This action-set difference is exactly what the plan
anticipated. The load-bearing framing — the **1-byte action mask** (a 2-byte mask
would kick the client), the **VarInt count-prefix**, and the **per-action body
order/encoding for the three actions Sulfur sends** (ADD_PLAYER `String name +
VarInt(0) props`, UPDATE_GAME_MODE `VarInt`, UPDATE_LISTED `Boolean`) — is
**byte-identical** to vanilla's. The MEDIUM-confidence property-list count-prefix
(`VarInt(0)`) matches exactly. Both bodies parse to zero trailing bytes as a client
would walk them. **SEALED, no divergence.** (Assumption A3 confirmed:
`pk.Byte(mask)` == vanilla `writeEnumSet` for ≤8 actions.)

### 2.2 ClientboundSetDefaultSpawnPosition (RespawnData/GlobalPos — PLAY-01)

Raw bodies:

```
vanilla: 13 6d696e6563726166743a6f766572776f726c64 000000000000 0fc4 00000000 00000000
sulfur : 13 6d696e6563726166743a6f766572776f726c64 000000000000 0fd0 00000000 00000000
         └Identifier "minecraft:overworld"───────┘ └packed BlockPos Long──┘ └yaw─┘ └pitch┘
```

| Field | Vanilla | Sulfur | Verdict |
|-------|---------|--------|---------|
| GlobalPos.dimension | Identifier `minecraft:overworld` | Identifier `minecraft:overworld` | MATCH |
| GlobalPos.pos (packed Long) | `0x…0fc4` → (0, **-60**, 0) | `0x…0fd0` → (0, **-48**, 0) | MATCH (framing) / Y value differs (generator surface) |
| RespawnData.yaw (Float) | `0.0` | `0.0` | MATCH |
| RespawnData.pitch (Float) | `0.0` | `0.0` | MATCH |
| **Whole-body framing** | 36 bytes, **0 trailing** | 36 bytes, **0 trailing** | **MATCH** |

**The decisive seal:** the 26.x restructure is `RespawnData = GlobalPos(Identifier
dimension + packed-Long BlockPos) + Float yaw + Float pitch`. Vanilla's bytes
confirm this exact field ORDER and TYPE set. Feeding Sulfur's encoder the **same
inputs** (dimension + the vanilla spawn pos) yields a body **byte-identical** to the
golden (the capture-diff test asserts `bytes.Equal`). The only difference in the
live capture is the **Y coordinate** (vanilla grass at -60 vs Sulfur stone at -48) —
a generator content difference, not a framing divergence. The packed-Long layout
matches Sulfur's `pk.Position`: `(x&0x3FFFFFF)<<38 | (z&0x3FFFFFF)<<12 | (y&0xFFF)`.
**SEALED, no divergence.**

### 2.3 ClientboundForgetLevelChunk (jar-derived packed Long — PLAY-04)

Vanilla emits this packet only on a real out-of-range walk (which is the Task-2
human gate, not a stationary join), so it was not captured live. Its layout has **no
version-drift ambiguity** — it is a single `writeLong(ChunkPos.pack())`, verified
directly from the jar bytecode (§4). The golden fixture is the jar-derived packing
for chunk (5,7); the capture-diff test asserts Sulfur's `world.ForgetLevelChunk(5,7)`
is byte-identical to it.

```
golden (cx=5, cz=7): 0000000700000005   (z in the HIGH 32 bits, x in the LOW 32)
sulfur (5,7)       : 0000000700000005   MATCH
```

**Non-vacuity proof:** temporarily swapping x/z in `world.ForgetLevelChunk`'s pack
makes the test FAIL with `sulfur: 0000000500000007` vs `golden: 0000000700000005`
— the assertion is load-bearing.

### 2.4 The simple HIGH-confidence encoders (diffed too, not the risk)

| Packet | Vanilla | Sulfur | Verdict |
|--------|---------|--------|---------|
| `ClientboundPlayerAbilities` | `0d 3d4ccccd 3dcccccd` (flags=creative, fly 0.05, walk 0.1) | `00 3d4ccccd 3dcccccd` (flags=survival) | MATCH framing (Byte + Float + Float) / flags value differs (creative vs survival) |
| `ClientboundSetHeldSlot` | `00` (VarInt 0) | `00` (VarInt 0) | **byte-identical** |

Both confirm their jar-derived layouts; the abilities flags byte differs only
because the vanilla capture ran in `gamemode=creative` while Sulfur sends survival
defaults — content, not framing. The two speed Floats (`0.05`, `0.1`) are
byte-identical.

---

## 3. The Open Questions — Resolved

### Q1 — `ClientboundForgetLevelChunk` 26.2 wire layout

**Resolved: a single big-endian Long = `ChunkPos.pack()`, x in the LOW 32 bits, z
in the HIGH 32 bits** (NOT the ≤773 wiki's "VarInt z, VarInt x"). Verified by
`javap -p -c` on `ClientboundForgetLevelChunkPacket.write` →
`FriendlyByteBuf.writeChunkPos` → `ChunkPos.pack()`:

```
pack(x, z): (x & 0xFFFFFFFF) | ((z & 0xFFFFFFFF) << 32)   // bipush 32; lshl; lor
```

Sulfur's `world.ForgetLevelChunk` (`int64(uint32(cx)) | int64(uint32(cz))<<32`)
matches byte-for-byte (`TestForgetLevelChunkWire` + the capture-diff golden).

### Q2 — PlayerInfoUpdate entry sub-encoding (property-list count-prefix)

**Resolved: the property list is a plain `VarInt(count)` prefix; with 0 properties
the vanilla wire is `VarInt(0)`, byte-identical to Sulfur's** (§2.1). A non-zero
property would be `String name, String value, Boolean signed, optional String
signature` — the offline join sends none. The count-prefix SHAPE is confirmed.

### Q3 — Does the real 26.2 client need `SetDefaultSpawnPosition` and/or `SetTime`?

**Partially resolved on the producer side; the consumer side is the Task-2 gate.**
- **`SetDefaultSpawnPosition`:** vanilla sends it; Sulfur sends it; byte-framing
  matches (§2.2). Kept.
- **`SetTime`:** the capture shows **vanilla DOES send `ClientboundSetTime`** (31
  bytes, `00000000000000f8 0201f801…` — a `WorldClock + ClockNetworkState`
  composite). Sulfur currently OMITS it (Assumption A2). The capture proves vanilla
  emits it, but whether its **absence** kicks/hangs the Sulfur client can only be
  decided by the **real-client walk-around (Task 2)**. **If the client kicks or
  hangs on "Loading terrain…" without SetTime, the fix is to add a minimal
  `writeSetTime` (jar-derive the `WorldClock`/`ClockNetworkState` field layout from
  the captured 31 bytes) to `server/play_join.go` and re-test.** Pre-decoded for
  readiness: the body begins with an 8-byte gameTime Long (`248` in the capture),
  followed by the ClockNetworkState fields.

### Q4 — `ServerboundPlayerLoaded` requirement

**Resolved: route as a no-op; do not block streaming on it.** The capture harness
sends `ServerboundPlayerLoaded` after confirming the teleport and the join completes
normally (vanilla streams the full ring without withholding any packet pending it).
For v1 Sulfur routes it as a no-op acknowledgment (05-01); the real-client walk-around
confirms nothing is gated on it.

---

## 4. Jar Derivation — ForgetLevelChunk (recorded for reproducibility)

```
$ javap -p -c -classpath temp/cache/26.2-inner.jar \
    net.minecraft.network.protocol.game.ClientboundForgetLevelChunkPacket
  private void write(FriendlyByteBuf):
    ... invokevirtual FriendlyByteBuf.writeChunkPos(ChunkPos) ...

$ javap -p -c ... net.minecraft.network.FriendlyByteBuf   # writeChunkPos
    ... invokevirtual ChunkPos.pack:()J
    ... invokevirtual writeLong:(J) ...

$ javap -p -c ... net.minecraft.world.level.ChunkPos      # pack(int,int)
    iload_0; i2l; ldc2_w 4294967295; land     // x & 0xFFFFFFFF
    iload_1; i2l; ldc2_w 4294967295; land     // z & 0xFFFFFFFF
    bipush 32; lshl; lor; lreturn             // x | (z << 32)
```

x occupies the LOW 32 bits, z the HIGH 32 bits; the client reads it via
`readChunkPos → readLong → ChunkPos.unpack` (`x = (int)L`, `z = (int)(L >> 32)`,
sign-correct via the int cast).

---

## 5. Deviations from Plan (auto-fixed under Rule 1)

### Fix — `net/queue.ChannelQueue` close-vs-send DATA RACE (Rule 1: bug)

**Found during:** the Docker `-race` gate over `./server/... ./world/...` (Task-2
preparation). The capture-diff test's added timing/load surfaced an intermittent
data race the prior single-test `-race` runs did not hit.

**Symptom (`-race`, reproducible at `-count=5..8` over the join-ordering tests):**

```
WARNING: DATA RACE
Write at 0x… by goroutine N:   runtime.closechan
  net/queue.ChannelQueue[…].Close  (queue.go:82  close(c))
  server.(*Client).Close.func1     (client.go:148)
Previous read at 0x… by goroutine M:   runtime.chansend
  net/queue.ChannelQueue[…].Push   (queue.go:69  c <- v)
  server.(*Client).Send            (client.go:135)
  server.(*TickLoop).flushOutbound (tick_phases.go:130)
```

**Root cause:** `ChannelQueue` was a bare `chan T`. `Push` did `c <- v` and `Close`
did `close(c)`. The tick's `flushOutbound` (one producer) and a readLoop-triggered
`Client.Close` (another goroutine) touch the same queue, so a `close(ch)` could race
a `ch <- v` — a data race (and a latent "send on closed channel" panic). `Client.Send`
had a `closed.Load()` atomic guard + a `recover()`, but neither eliminates the race:
the check→Push window remains, and `recover` masks the panic without removing the
concurrent memory access the detector flags.

**Fix (`net/queue/queue.go`):** converted `ChannelQueue[T]` from a bare channel to a
struct `{ ch chan T; mu sync.Mutex; closed bool }` and serialized `Close` against
`Push` with the mutex + a `closed` flag (mirroring the already-safe
`LinkedListQueue`). Once closed, `Push` is a no-op returning `false` and `Close` is
idempotent. `Pull` still reads the channel lock-free (a closed channel drains its
buffer then reports `ok=false` — exactly the writeLoop's stop signal; close-vs-recv
is safe in Go). No call-site change: `NewChannelQueue` returns the `Queue[T]`
interface, and `Client.Send`/`Close` are unchanged.

**Verification:** post-fix, the join-ordering tests run `-race` clean at `-count=10`,
and the full `-race` gate over `./server/... ./world/... ./net/...` is clean. The
non-race full suite (`go test ./...`) is green. Fix confined to the owning package
(`net/queue`); no generated files edited; zero new dependencies.

---

## 6. Gate Results (automatable half — all green)

| Gate | Command | Result |
|------|---------|--------|
| Capture-diff | `go test ./server/ -run TestPlayBytesVsVanillaCapture -count=1` | PASS (3/3 subtests; asserts Sulfur framing == vanilla golden) |
| Non-vacuous proof | x/z-swap `ForgetLevelChunk` → test | FAIL (`0500000007` vs `0700000005`) → restored → PASS |
| Wire tests | `go test ./server/ -run 'TestPlayerInfoUpdateWire\|TestSetDefaultSpawnPositionWire' -count=1` | PASS |
| Forget wire | `go test ./world/ -run TestForgetLevelChunkWire -count=1` | PASS |
| Full suite | `go test ./... -count=1` | PASS |
| `-race` (Docker) | `docker run … golang:1.26 go test ./server/... ./world/... ./net/... -race` | PASS (clean after the queue fix; `-count=10` on server clean) |
| Build + vet | `go build ./...`, `go vet ./...` | clean |

Zero new dependencies. The only code fix is `net/queue/queue.go` (the
`ChannelQueue` race); the three Play encoders needed **no** wire change — they
already match vanilla.

---

## 7. Real-Client Walk-Around (Task 2 — BLOCKING human-verify, APPROVED)

> The decisive PLAY-04/05/06 first-playable milestone cannot be self-approved.

An **unmodified vanilla 26.2 client (PrismLauncher)** connected to `cmd/sulfur`
(`localhost:25565`) and the user confirmed — *"si, funciona :)"*:

1. **WALK AROUND — PASS:** moving >2 chunks in each direction, new chunks appear at
   the moving edges and the view-distance ring FOLLOWS — **no void** at the edges,
   **no "walking off the world"** (05-01's `recenterRing` + the jar-derived
   `ForgetLevelChunk` working end-to-end). Movement is NOT blocked after the initial
   Confirm Teleportation (the teleport gate opens correctly).
2. **TAB LIST — PASS:** the player's own name is listed (05-02's `PlayerInfoUpdate`
   self-entry — the framing sealed in §2.1).
3. **NO KICK / NO HANG — PASS:** no malformed-packet kick, no "Loading terrain…"
   hang. **SetTime turned out NOT to be needed** — no kick without it; left omitted
   for v1 (the 31-byte layout stays documented in §3 Q3 if a later phase needs it).

**Sign-off:**

> Reviewed by: user (real vanilla 26.2 / PrismLauncher client)   Date: 2026-06-24
> Result: **[x] ring follows + tab list + no kick — APPROVED** ("si, funciona :)")
