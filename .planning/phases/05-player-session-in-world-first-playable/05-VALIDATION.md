---
phase: 5
slug: player-session-in-world-first-playable
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-23
---

# Phase 5 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

Phase 5 is **NOT greenfield** — it EXTENDS the pulled-forward Join Game bootstrap (`server/play_join.go`, commit `0fd96850`) and the Phase-4 view-distance streamer (`server/world_stream.go` + `server/tick_phases.go`). A real vanilla 26.2 client already logs in and stands on solid ground; the delta is **walking around** (movement decode → tick-owned position → the ring FOLLOWS the player), the **teleport-id gate** (drop movement until confirmed), the **early-Play tail** (abilities / held-slot / tab-list / spawn-pos), and the **PLAY-06 real-client walk-around** gate.

The decisive validation fact, inherited from Phases 2 and 4: **a Go self-round-trip is NOT the wire-correctness proof.** The Phase-2 `net.Pipe` bot tolerated the `login_finished` + empty-tags bugs a real client rejected; the Phase-4 symmetric chunk read/write hid two paletted-container bugs only a vanilla capture-diff caught. The same discipline applies here:

- **Movement decode** (PLAY-04) — the trailing field of every `ServerboundMovePlayer*` packet is a **packed flags Byte** (`bit0=onGround`, `bit1=horizontalCollision`), NOT the `Boolean onGround` the ≤773 wiki documents (jar-confirmed this session). A decode test that masks the byte is the cheap regression; the real client walking is the authoritative confirmation it frames correctly.
- **The uncertain encoders** — `ClientboundForgetLevelChunk` (field order **jar-derived in Wave 0**, not guessed), `ClientboundPlayerInfoUpdate` entry sub-encoding, `ClientboundSetDefaultSpawnPosition` (26.x `RespawnData`/`GlobalPos` restructure), and the optional `ClientboundSetTime` (`WorldClock`/`ClockNetworkState` restructure) — are **capture-diff candidates** sealed against a real vanilla 26.2 server, mirroring the Phase-4 harness (`temp/chunkcapture/` pattern, `WORLD-CAPTURE-DIFF.md`).

Self-decode/round-trip tests remain valuable as **cheap native regression** (they catch a re-introduced flags-as-bool or a field reorder fast) — they are necessary, not sufficient. The capture-diff and the real-client walk-around are the phase gate.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` (the project standard — `server/*_test.go`) + Docker `-race` for the movement/tick seam |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go build ./... && go test ./server/ -run 'Movement\|PlayerInfo\|Recenter\|Teleport\|Bootstrap\|Abilities\|HeldSlot\|SpawnPos\|ForgetChunk\|PlayerLoaded' -count=1` |
| **Full suite command** | `go vet ./... && go build ./... && go test ./... -count=1` then the race gate below |
| **Race gate (movement/tick seam)** | `MSYS_NO_PATHCONV=1 docker run --rm -v "//d/ender://src" -w //src golang:1.26 go test ./server/... ./world/... -race -count=1` (host is `CGO_ENABLED=0`; `-race` needs cgo → Docker, as established in Phases 2–4) |
| **Movement round-trip / flags-byte test** | `server/movement_test.go` — Marshal each of the 4 `ServerboundMovePlayer*` layouts, drive `dispatch`→`resolveSubtickInputs`→`applyInput`, assert the tick-owned position/flags update and that the trailing byte is masked (`&0x01` onGround, `&0x02` horizontalCollision), never read as a Boolean. |
| **Ring-follow test** | `server/world_stream_test.go` (extend) — `recenterRing` prunes `sentChunks`, emits one `ForgetLevelChunk` per dropped column, resets `centerSent` so `flushOutbound` re-emits `SetChunkCacheCenter`; a simulated cross-boundary move streams the new ring and forgets the far columns. |
| **Teleport-gate test** | `server/movement_test.go` — movement is IGNORED until `ServerboundAcceptTeleportation` echoes the matching `awaitingTeleport` id; a wrong/forged id does not confirm; position does not move pre-confirm. |
| **Capture-diff harness** | Mirror Phase 4 (`WORLD-CAPTURE-DIFF.md`): stand up `temp/cache/26.2-server.jar` (JDK 25, offline, flat), capture `ClientboundPlayerInfoUpdate`, `ClientboundSetDefaultSpawnPosition`, `ClientboundForgetLevelChunk` (and `ClientboundSetTime` if used) via the fork `bot/` client or a logging proxy; commit each as a golden `.bin`; byte-diff Sulfur's encoders. |
| **Real-client walk-around (the PLAY-06 gate)** | An unmodified vanilla 26.2 client (PrismLauncher) connects to `cmd/sulfur`, logs in, **walks around**, and the ring FOLLOWS (no void at the edges), the player appears in its own tab list, and there is no kick/hang — the milestone verifier, mirroring Phase 4's NET-04/WORLD-05 sign-off. |

---

## Sampling Rate

- **After every task commit:** `go build ./... && go test ./server/ -run '<the task's tests>' -count=1` (native, fast).
- **After the movement-decode task and the re-center task:** add the Docker `-race` gate over `./server/... ./world/...` (the movement Intent → tick-owned position → ring re-center seam — prove no off-tick mutation).
- **After every plan wave:** `go vet ./... && go build ./... && go test ./... -count=1` + the `-race` gate.
- **Phase gate (before `/gsd-verify-work`):** Full suite green, the capture-diffs byte-identical (PlayerInfoUpdate, SpawnPos, ForgetLevelChunk; SetTime if used), AND the PLAY-06 real-client walk-around signed off (the ring follows, tab list shows the player, no void-kick).
- **Max feedback latency:** ~60–90s for the automated suite; the capture-diff/real-client walk-around is the phase gate, not a per-task check.

---

## Per-Task Verification Map

| Req ID | Plan | Wave | Behavior | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|--------|------|------|----------|------------|-----------------|-----------|-------------------|-------------|--------|
| PLAY-04 | 05-01 | 1 | `applyInput` decodes all 4 `ServerboundMovePlayer*` layouts (trailing **packed flags Byte**, not Boolean), updates tick-owned `p.x/y/z/yaw/pitch/onGround` | T-5-02 (malformed movement), T-5-04 (huge coords) | Every `Scan` error → return without mutating state; never panic; never read the trailing byte as a Boolean | unit | `go test ./server/ -run 'TestMovementDecode' -count=1` | ❌ W0 | ⬜ pending |
| PLAY-04 | 05-01 | 1 | When the player crosses a chunk boundary, `recenterRing` moves `p.center`, resets `centerSent`, prunes `sentChunks`, sends one `ForgetLevelChunk` per dropped column; the streamer re-streams the new ring | T-5-05 (re-center thrash) | Re-center only on actual `newC != p.center`; the ring is server-clamped (`serverViewDistance`), so the work per move is O(ring), bounded | unit | `go test ./server/ -run 'TestRecenterRing\|TestRingFollowsOnMove' -count=1` | ❌ W0 | ⬜ pending |
| PLAY-02 | 05-01 | 1 | Movement is dropped until `ServerboundAcceptTeleportation` echoes the matching `awaitingTeleport` id; a wrong id does not confirm | T-5-01 (forged teleport confirm), T-5-03 (pre-confirm movement) | Validate the echoed VarInt against `p.awaitingTeleport`; only then accept movement; `dispatch` is total/never panics | unit | `go test ./server/ -run 'TestTeleportGate' -count=1` | ❌ W0 | ⬜ pending |
| PLAY-04 | 05-01 | 1 | `world.ForgetLevelChunk` encodes the **jar-derived** 26.2 layout | T-5-02 | Field order taken from the jar (`ClientboundForgetLevelChunkPacket`), not guessed | unit | `go test ./world/ -run 'TestForgetLevelChunkWire' -count=1` | ❌ W0 | ⬜ pending |
| PLAY-04 | 05-01 | 1 | `ServerboundPlayerLoaded` (UNIT/empty) routes as a no-op; never blocks streaming | T-5-02 | Total dispatch path; empty payload not required to be read | unit | `go test ./server/ -run 'TestPlayerLoadedNoop' -count=1` | ❌ W0 | ⬜ pending |
| PLAY-01 | 05-02 | 2 | The early-Play tail (`PlayerAbilities` → `SetHeldSlot` → `PlayerInfoUpdate` → `SetDefaultSpawnPosition`) is enqueued AFTER the existing 3 bootstrap packets, still BEFORE `loop.register` (FIFO invariant preserved) | — | All sends through the bounded outbound queue off-tick; no tick-owned mutation in `AcceptPlayer` (TICK-05) | unit (ordering + wire) | `go test ./server/ -run 'TestBootstrapTailOrdering\|TestPlayerAbilitiesWire\|TestSetHeldSlotWire' -count=1` | ❌ W0 | ⬜ pending |
| PLAY-05 | 05-02 | 2 | `PlayerInfoUpdate` writes the 8-action mask (`ADD_PLAYER\|UPDATE_GAME_MODE\|UPDATE_LISTED` = 1 byte) + a single self-entry so the player appears in its own tab list | — | Action mask written as `pk.Byte` (≤8 actions = 1-byte FixedBitSet); collection count-prefixed | unit + capture-diff | `go test ./server/ -run 'TestPlayerInfoUpdateWire' -count=1` | ❌ W0 | ⬜ pending |
| PLAY-02 | 05-02 | 2 | The bootstrap issues an **incrementing** per-player teleport id and threads it into `tickPlayer.awaitingTeleport` so the gate can match the echo | T-5-01 | The id is server-issued and incrementing (spoof-resistant); the tick matches the echo | unit | `go test ./server/ -run 'TestBootstrapTeleportID\|TestTeleportGate' -count=1` | ❌ W0 | ⬜ pending |
| PLAY-01 / PLAY-03 | 05-02 | 2 | Confirm completeness: Login references valid registries + reaches Play; the ring streams radius≥2 + `LEVEL_CHUNKS_LOAD_START` (already built — regression only) | — | Already server-clamped (T-4-01) | unit (regression) | `go test ./server/ -run 'TestLoginPacketWireLayout\|TestJoinSequenceOrdering' -count=1` | ✅ | ⬜ pending |
| PLAY-01/02/05 | 05-03 | 3 | Sulfur's `PlayerInfoUpdate`, `SetDefaultSpawnPosition`, `ForgetLevelChunk` (+`SetTime` if used) byte-match a real vanilla 26.2 server | T-5-02 | The wire is vanilla-correct (8-action mask, RespawnData/GlobalPos, jar-derived forget) — proven against the jar, not self-consistency | golden / capture-diff | `go test ./server/ -run 'TestPlayBytesVsVanillaCapture' -count=1` | ❌ W0 | ⬜ pending |
| PLAY-06 | 05-03 | 3 | A real vanilla 26.2 client connects, logs in, **walks around**; the ring follows; the player is in the tab list; no kick/hang | — | The full untrusted-client path is exercised; movement is gated + bounded | **manual (human-verify)** | real-client walk-around (PrismLauncher) — the gate, not automatable | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Test scaffolds and shared fixtures that MUST exist before the corresponding implementation task runs. Each plan creates its own test file (RED) before/with the implementation; the jar-derived `ForgetLevelChunk` layout and the capture fixtures are produced once.

- [ ] **Jar-derive `ClientboundForgetLevelChunk` 26.2 wire layout** (Plan 05-01, Task 0) — decompile `ClientboundForgetLevelChunkPacket` from `temp/cache/26.2-inner.jar` with `javap -p -c` (the method this phase's research used for movement) to settle `VarInt(z),VarInt(x)` vs a single packed Long. Record the finding inline in `world/packet.go` and in `05-CAPTURE-DIFF.md`. Do NOT guess.
- [ ] **`server/movement_test.go`** (Plan 05-01) — `TestMovementDecode` (each of the 4 `ServerboundMovePlayer*` layouts decodes, flags-byte masked), `TestTeleportGate` (movement ignored until matching `AcceptTeleportation`; forged id rejected), `TestPlayerLoadedNoop` (UNIT routes as no-op). No movement test exists today.
- [ ] **`server/world_stream_test.go`** (Plan 05-01, extend) — `TestRecenterRing` (prunes `sentChunks` + emits `ForgetLevelChunk` per dropped column + resets `centerSent`), `TestRingFollowsOnMove` (a cross-boundary move streams the new ring; far columns forgotten; no void).
- [ ] **`world/packet_test.go`** (Plan 05-01, extend) — `TestForgetLevelChunkWire` (the jar-derived layout).
- [ ] **`server/play_join_test.go`** (Plan 05-02, extend) — `TestPlayerAbilitiesWire`, `TestSetHeldSlotWire`, `TestPlayerInfoUpdateWire` (8-action mask + self-entry), `TestSetDefaultSpawnPositionWire`, `TestBootstrapTailOrdering` (tail after the 3 bootstrap packets, before register), `TestBootstrapTeleportID` (incrementing id threaded into `awaitingTeleport`).
- [ ] **`server/play_join_capture_test.go`** (Plan 05-03) — `TestPlayBytesVsVanillaCapture`: load the committed golden `.bin`s, byte-diff Sulfur's `PlayerInfoUpdate`/`SetDefaultSpawnPosition`/`ForgetLevelChunk` encoders. Skips with a clear message if a fixture is absent (native CI stays green; the capture is the gate).
- [ ] **Capture fixtures** (Plan 05-03) — committed golden `.bin`s of the vanilla 26.2 `ClientboundPlayerInfoUpdate`, `ClientboundSetDefaultSpawnPosition`, `ClientboundForgetLevelChunk` (and `ClientboundSetTime` if used), plus `05-CAPTURE-DIFF.md` recording the capture method, the per-field diff, and the resolution of the Open Questions (ForgetLevelChunk layout, PlayerInfoUpdate entry sub-encoding, SpawnPos/SetTime necessity).

*(Movement routing into the subtick buffer and `TestJoinSequenceOrdering` already exist — Wave 0 is the decode / re-center / new-encoder / teleport-gate layer.)*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Sulfur's `PlayerInfoUpdate` / `SetDefaultSpawnPosition` / `ForgetLevelChunk` (+`SetTime` if used) byte-match a **real** vanilla 26.2 server | PLAY-01, PLAY-02, PLAY-05 | No published spec covers proto 776; the entry sub-encoding (PlayerInfoUpdate) and the 26.x composite codecs (RespawnData/GlobalPos, WorldClock/ClockNetworkState) are jar-confirmed in shape but their exact bytes drift between versions — only a byte-comparison against the actual vanilla server output proves vanilla-correctness. | Stand up `temp/cache/26.2-server.jar` (sha1 `823e2250…`, JDK 25) offline/flat. Capture each clientbound Play packet (logging proxy, or point the fork `bot/` client at vanilla and dump). Commit each as a golden `.bin`. Diff: the 1-byte action mask, the count-prefixed entry collection, the RespawnData GlobalPos field order. Resolve the Open Questions. Write `05-CAPTURE-DIFF.md`. |
| A real vanilla 26.2 client connects, logs in, **walks around**; the ring follows (no void), the player is in the tab list, no kick/hang | PLAY-04, PLAY-06 | The decisive first-playable check — a real client rendering and the ring re-streaming as the player walks past the spawn ring cannot be self-approved (mirrors Phase 4's WORLD-05 sign-off). | Connect an unmodified vanilla 26.2 client (PrismLauncher) to `cmd/sulfur`. Walk >2 chunks in each direction and confirm new chunks appear at the edges (the ring follows; no void; no "walking off the world"). Open the tab list and confirm the player's name is listed. Confirm there is no kick or "Loading terrain…" hang. |

---

## Validation Sign-Off

- [ ] Every task has an `<automated>` verify command or an explicit Wave 0 dependency (Nyquist).
- [ ] Sampling continuity: no 3 consecutive tasks without an automated build/test check.
- [ ] The movement-decode task and the re-center task run under Docker `-race` (the movement Intent → tick-owned position → ring re-center seam).
- [ ] Wave 0 jar-derives `ClientboundForgetLevelChunk` before `world.ForgetLevelChunk` is written, and stands up the per-requirement test scaffolds before implementation; the capture fixtures are committed before the capture-diff test runs.
- [ ] The PLAY-01/02/05 capture-diff against a real vanilla 26.2 server and the PLAY-06 real-client walk-around are required phase gates (human sign-off in 05-03), NOT self-round-trips.
- [ ] No watch-mode flags.
- [ ] Feedback latency < 90s for the automated suite (vanilla capture-diff + real-client walk-around excluded — they are the phase gate).
- [ ] `nyquist_compliant: true` (every PLAY-0x behavior maps to an automated command + a Wave 0 scaffold; the movement flags-byte, the uncertain encoders, and the walk-around are the prescribed jar/capture/real-client gates, justified by the bleeding-edge-proto research findings).

**Approval:** pending
