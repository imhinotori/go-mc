---
phase: 6
slug: entities-physics-interaction
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-24
---

# Phase 6 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

Phase 6 is **mostly greenfield game LOGIC over an existing ~80% data layer**. It FILLS the Phase-3 seams (`tickEntities()`/`tickPhysics()` stubs in `server/tick_phases.go`, the `noopTracker` behind `tracker.Tick()` in `server/tick.go`) with SYNCHRONOUS, tick-owned reference implementations — NO xsync/ants/conc, NO async tracker (that is Phase 8 / OPT-02). It REUSES `data/entity` (158 entities + AABB Width/Height), `level/block` (32366 states + `ToStateID`/`IsAir`), `level/component` (`SlotData` slot codec + 111 schemas), `save`/`save/region` (Anvil IO), `nbt`, and `server/internal/bvh` (AABB primitive). New work = the entity store + tracker, AABB physics, block-edit reconciliation, the inventory state machine, the death/respawn flow, persistence wiring, and a handful of new clientbound ENCODERS.

The decisive validation fact, inherited from Phases 2/4/5: **a Go self-round-trip is NOT the wire-correctness proof.** The Phase-2 `net.Pipe` bot tolerated the `login_finished` + empty-tags bugs a real client rejected; the Phase-4 symmetric chunk read/write hid two paletted-container bugs only a vanilla capture-diff caught; the Phase-5 capture-diff sealed the Play encoders byte-identical. The same discipline applies to Phase 6's THREE MEDIUM-confidence wire surfaces:

- **Entity metadata** (`ClientboundSetEntityData`) — the indexed-entry list framing (`Byte index`, `VarInt serializerId`, codec value, `0xFF` terminator) and the `AddEntity` `Vec3.LP` low-precision short movement scaling are jar-confirmed in SHAPE but their exact bytes (the LP short factor, whether a test entity needs a non-default metadata entry to render) are MEDIUM (06-RESEARCH A2/A3, Open Questions 1/2).
- **Component-slot `ItemStack`** (`ClientboundContainerSetContent`/`ContainerSetSlot`) — the `count, id, addedCount, removedCount, [added], [removed]` shape matches `SlotData`, but the added-component value codecs are MEDIUM (06-RESEARCH A4, Pitfall 2).
- **`ServerboundContainerClick` `HashedStack`** (1.21.5+) — the `Int2ObjectMap<HashedStack> changedSlots` + `HashedStack carriedItem` framing must be jar-derived (`HashedPatchMap.write`) before the decoder is written, or the packet mis-frames and the server reads garbage (06-RESEARCH A6, Pitfall 3, Open Question 3).

Self-decode/round-trip tests remain valuable as **cheap native regression** (they catch a re-introduced framing bug fast) — they are necessary, not sufficient. The capture-diff and the real-client interactive check (place a block and SEE it; take damage and SEE the health bar; an entity spawns and is VISIBLE) are the phase gate.

The physics constants (gravity 0.08, air drag 0.98, friction 0.6×0.91, step 0.6) are `[ASSUMED]`/training-derived (06-RESEARCH A1) and **do NOT affect the wire** — v1 targets the VISIBLE behaviors (entity lands on ground, blocked by walls, no clip-through), not exact-constant parity. They are unit-tested for the visible behaviors, NOT capture-diffed.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` (the project standard — `server/*_test.go`, `world/*_test.go`, `level/component/*_test.go`, `save/*_test.go`) + Docker `-race` for the entity/physics/block/inventory tick seams |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go build ./... && go test ./server/ ./world/ ./level/component/ ./save/ -run 'Entity\|Tracker\|Physics\|BlockInteract\|Inventory\|Slot\|Combat\|Health\|Respawn\|Persistence\|EntityID\|SetBlock' -count=1` |
| **Full suite command** | `go vet ./... && go build ./... && go test ./... -count=1` then the race gate below |
| **Race gate (entity/physics/block/inventory/tick seams)** | `MSYS_NO_PATHCONV=1 docker run --rm -v "//d/ender://src" -w //src golang:1.26 go test ./server/... ./world/... ./level/... ./save/... -race -count=1` (host is `CGO_ENABLED=0`; `-race` needs cgo → Docker, as established in Phases 2–5) |
| **Entity store / tracker test** | `server/entity_store_test.go` + `server/tracker_test.go` — the monotonic entity-ID allocator never collides; per-section grid bucketing returns the entities near a player; the visibility diff emits `AddEntity` (+`SetEntityData` if metadata) for newly-visible, a delta/teleport `Move*` for still-visible-and-moved, and ONE batched `RemoveEntities` for no-longer-visible. |
| **Physics test** | `server/physics_test.go` — per-axis swept-AABB resolution: an entity with downward velocity LANDS on the solid floor (onGround true, does not fall through); horizontal motion into a wall is BLOCKED (no clip-through); a fast motion vector does NOT tunnel a thin wall (per-axis, never full-vector-then-test). Uses `bvh.AABB.Touch` against world block AABBs from `data/entity` dims. |
| **Block-interact test** | `server/block_interact_test.go` + `world/manager_test.go` — `ChunkManager.SetBlock(pos, state)` maps the block pos → (column, section, local) and mutates the tick-owned chunk; `PlayerAction` (break) sets air, `UseItemOn` (place) sets the held block state; both emit `ClientboundBlockChangedAck(sequence)` AND `ClientboundBlockUpdate` to trackers; an out-of-reach / unloaded-chunk edit is rejected (no mutation, no ack). |
| **Inventory test** | `server/inventory_test.go` + `level/component` extended-encoder tests — the per-player inventory holds component-slot `ItemStack`s; `ContainerSetContent`/`ContainerSetSlot` round-trip a component-free stack (count+id+0+0) and a stack with one added component; a `ContainerClick` decodes the `HashedStack` form WITHOUT mis-framing and the server re-sends authoritative content (the client hashes are discarded). |
| **Combat test** | `server/combat_test.go` — `SetHealth(Float health, VarInt food, Float saturation)` encodes the jar-verified order; damage lowers tick-owned health; health ≤ 0 sends `PlayerCombatKill`; a `ServerboundClientCommand(PERFORM_RESPAWN)` drives `ClientboundRespawn` (reusing the sealed `commonPlayerSpawnInfoEncoder` + the trailing `dataToKeep` byte) and re-teleports/streams. |
| **Persistence test** | `server/persistence_test.go` + `save` round-trip — a player snapshot → `save.PlayerData` NBT → `world/playerdata/<uuid>.dat` and back recovers pos/health/inventory; an entity snapshot → `save.Entities` NBT → `entities/r.*.mca` (via `save/region`) and back recovers the entity; a missing `.dat` on load falls back to spawn defaults (no crash). |
| **Capture-diff harness** | Mirror Phases 4/5 (`WORLD-CAPTURE-DIFF.md`, `05-CAPTURE-DIFF.md`): stand up `temp/cache/26.2-server.jar` (JDK 25, offline, flat), capture `ClientboundAddEntity`, `ClientboundSetEntityData` (a moving entity to seal the LP short scaling), `ClientboundContainerSetContent` (a known inventory), and a `ServerboundContainerClick` round-trip (the `HashedStack` form) via the fork `bot/` client or a logging proxy; commit each as a golden `.bin`; byte-diff Sulfur's encoders/decoders. `ClientboundRespawn` reuses the Phase-5-sealed spawn-info encoder (confirming diff only). |
| **Real-client interactive check (the phase gate)** | An unmodified vanilla 26.2 client (PrismLauncher) connects to `cmd/sulfur`: an entity spawns and is **VISIBLE**; the player **places a block and SEES it** persist (and breaks one and sees it vanish), with no ghost blocks; the player **takes damage and SEES the health bar** drop, dies, and respawns; no kick/hang — the milestone verifier, mirroring Phases 4/5. |

---

## Sampling Rate

- **After every task commit:** `go build ./... && go test ./<package>/ -run '<the task's tests>' -count=1` (native, fast).
- **After the entity-store/tracker task, the physics task, the block-edit task, and the inventory task:** add the Docker `-race` gate over `./server/... ./world/... ./level/...` (every entity/physics/block/inventory mutation is tick-owned — prove no off-tick mutation, TICK-05).
- **After every plan wave:** `go vet ./... && go build ./... && go test ./... -count=1` + the `-race` gate.
- **Phase gate (before `/gsd-verify-work`):** Full suite green, the capture-diffs byte-identical (SetEntityData/AddEntity, ContainerSetContent, ContainerClick HashedStack; Respawn confirming diff), AND the PLAY/ENT real-client interactive check signed off (entity visible, place/break a block and see it, take damage and see the health bar, death→respawn).
- **Max feedback latency:** ~60–120s for the automated suite; the capture-diff/real-client interactive check is the phase gate, not a per-task check.

---

## Per-Task Verification Map

| Req ID | Plan | Wave | Behavior | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|--------|------|------|----------|------------|-----------------|-----------|-------------------|-------------|--------|
| ENT-01 | 06-01 | 1 | Monotonic entity-ID allocator assigns a unique id to every player AND entity; the hard-coded `joinEntityID=1` is replaced; the entity store holds tick-owned entities with snapshot-friendly hot fields | T-6-07 (entity-id spoof/collision) | The id is SERVER-issued and monotonic (never client-supplied); ids never collide | unit | `go test ./server/ -run 'TestEntityIDAllocator\|TestEntityStore' -count=1` | ❌ W0 | ⬜ pending |
| ENT-01 | 06-01 | 1 | Per-section grid bucketing returns entities near a player position (broad phase — NOT a quadtree) | T-6-04 (malformed payload DoS) | The bucket query is bounded by the player's clamped track range | unit | `go test ./server/ -run 'TestEntityBucketing' -count=1` | ❌ W0 | ⬜ pending |
| ENT-01 | 06-02 | 2 | The synchronous `entityTracker` (behind `tracker.Tick()`) diffs per-player visibility → `AddEntity`(+`SetEntityData` if metadata) for newly-visible, delta/teleport `Move*`/`RotateHead` for still-visible-and-moved, ONE batched `RemoveEntities` for no-longer-visible | T-6-04 | All sends through `client.Send` (bounded queue); the tracker runs ON the tick goroutine over tick-owned state (TICK-05) | unit | `go test ./server/ -run 'TestTracker\|TestVisibilityDiff' -count=1` | ❌ W0 | ⬜ pending |
| ENT-01 | 06-02 | 2 | `AddEntity` (jar-derived: `Vec3.LP` short movement, byte angles, VarInt data) + `SetEntityData` (indexed-entry framing + `0xFF` terminator) encode correctly | T-6-04 | Field order/terminator jar-derived (not the ≤773 wiki); the LP short scaling and the 0xFF framing are capture-diff-sealed in 06-06 | unit + capture | `go test ./server/ -run 'TestAddEntityWire\|TestSetEntityDataWire' -count=1` | ❌ W0 | ⬜ pending |
| ENT-02 | 06-03 | 3 | Per-axis swept-AABB physics: gravity pulls an entity down; it LANDS on the solid floor (onGround, no fall-through); horizontal motion into a wall is BLOCKED; a fast vector does NOT tunnel a thin wall | T-6-06 (tunneling self-move through walls) | The server collides client-sent positions per-axis (authoritative); a clip-through is rejected; entity AABBs come from `data/entity` dims, world boxes via `bvh.AABB.Touch` | unit | `go test ./server/ -run 'TestPhysics\|TestPerAxisSweep\|TestNoTunnel' -count=1` | ❌ W0 | ⬜ pending |
| ENT-03 | 06-04 | 3 | `ChunkManager.SetBlock(pos, state)` maps pos → (column, section, local) and mutates the tick-owned chunk; break sets air, place sets the held block state | T-6-01 (out-of-range/cross-chunk edit) | The manager validates the target column is LOADED before mutating; reach distance is validated; an invalid edit is a silent no-op | unit | `go test ./world/ -run 'TestSetBlock\|TestSetBlockUnloaded' -count=1` | ❌ W0 | ⬜ pending |
| ENT-03 | 06-04 | 3 | `PlayerAction`(break)/`UseItemOn`(place) handlers apply the edit on-tick, then emit `ClientboundBlockChangedAck(sequence)` AND `ClientboundBlockUpdate` to all trackers (editor included) | T-6-01, T-6-04 | Every `Scan` error → no-op (never panic); the ack/sequence handshake reconciles the client prediction (no ghost blocks); reach validated | unit | `go test ./server/ -run 'TestBlockInteract\|TestBlockReconcile' -count=1` | ❌ W0 | ⬜ pending |
| ENT-04 | 06-05 | 4 | Per-player component-slot inventory; `ContainerSetContent`/`ContainerSetSlot` encode component-free (count+id+0+0) and one-added-component `ItemStack`s; `SlotData.WriteTo` is extended for real components | T-6-02 (forged inventory click) | Server is AUTHORITATIVE; the inventory is tick-owned; the wire is `SlotData` (no NBT-in-slot, ENT-04) | unit + capture | `go test ./server/ ./level/component/ -run 'TestInventory\|TestSlotEncode' -count=1` | ❌ W0 | ⬜ pending |
| ENT-04 | 06-05 | 4 | `ServerboundContainerClick` decodes the `HashedStack` (1.21.5+) form WITHOUT mis-framing; the server discards the client hashes and re-sends authoritative content | T-6-02, T-6-04 | The `HashedPatchMap`/`HashedStack` framing is jar-derived (06-06 Wave 0); a malformed click → no-op, never panic; client item data is never trusted | unit + capture | `go test ./server/ -run 'TestContainerClickDecode' -count=1` | ❌ W0 | ⬜ pending |
| ENT-05 | 06-05 | 4 | `SetHealth(Float, VarInt, Float)` encodes the jar order; damage lowers tick-owned health; health ≤ 0 sends `PlayerCombatKill`; `ClientCommand(PERFORM_RESPAWN)` → `ClientboundRespawn` (reused spawn-info encoder + `dataToKeep` byte) + re-teleport/stream | T-6-05 (self-claimed health/"not dead") | Health is SERVER-owned; the client only REQUESTS respawn; the respawn encoder reuses the Phase-5-sealed `commonPlayerSpawnInfoEncoder` | unit | `go test ./server/ -run 'TestSetHealthWire\|TestCombat\|TestRespawn' -count=1` | ❌ W0 | ⬜ pending |
| ENT-06 | 06-05 | 4 | Player snapshot → `save.PlayerData` NBT → `.dat` and back recovers pos/health/inventory; entity snapshot → `save.Entities` → `entities/r.*.mca` and back; a missing `.dat` falls back to spawn defaults | T-6-04 | Off-tick IO (the tick snapshots; IO off the critical path, like Phase-4 chunk load); disk-NBT ≠ wire-component (Pitfall 6) | unit | `go test ./server/ ./save/ -run 'TestPersistence\|TestPlayerDataRoundTrip' -count=1` | ❌ W0 | ⬜ pending |
| ENT-01/04 | 06-06 | 5 | Sulfur's `SetEntityData`/`AddEntity` (LP short scaling + 0xFF framing), `ContainerSetContent`, and the `ContainerClick` `HashedStack` form byte-match a real vanilla 26.2 server | T-6-04 | The wire is vanilla-correct — proven against the jar/real server, not self-consistency | golden / capture-diff | `go test ./server/ -run 'TestEntityBytesVsVanillaCapture\|TestSlotBytesVsVanillaCapture' -count=1` | ❌ W0 | ⬜ pending |
| ENT-01..05 | 06-06 | 5 | A real vanilla 26.2 client: an entity is VISIBLE; place/break a block and SEE it; take damage and SEE the health bar; death→respawn; no kick/hang | — | The full untrusted-client interaction path is exercised; edits/clicks/health are server-authoritative + bounded | **manual (human-verify)** | real-client interactive check (PrismLauncher) — the gate, not automatable | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Test scaffolds and shared fixtures that MUST exist before the corresponding implementation task runs. Each plan creates its own test file (RED) before/with the implementation; the jar-derived LP short scaling + the `HashedStack`/`HashedPatchMap` framing and the capture fixtures are produced once.

- [ ] **Jar-derive `Vec3.LP_STREAM_CODEC` short scaling** (Plan 06-02, Task 0) — decompile `ClientboundAddEntityPacket` / `Vec3.LP_STREAM_CODEC` / `ClientboundSetEntityMotionPacket` from `temp/cache/26.2-inner.jar` with `javap -p -c` to settle whether the LP movement short uses the historical ×8000 factor or a different one at 776. Record the finding inline in `server/tracker.go` and in `06-CAPTURE-DIFF.md`. Do NOT guess (06-RESEARCH Open Question 1 / A2).
- [ ] **Jar-derive `SetEntityData` serializer framing** (Plan 06-02, Task 0) — confirm `SynchedEntityData$DataValue.write` (`Byte id`, `VarInt serializerId`, codec value) + the `0xFF` (255) terminator in `pack()`; settle whether the v1 test entity (`sulfur_cube` or a simple mob) renders from `AddEntity` + the 0xFF-only metadata, or needs a non-default entry (06-RESEARCH Open Question 2 / A3). Record in `06-CAPTURE-DIFF.md`.
- [ ] **Jar-derive `HashedStack` / `HashedPatchMap.write`** (Plan 06-05, Task 0) — decompile `ServerboundContainerClickPacket` + `HashedPatchMap` + `HashedStack` from the inner jar to settle the minimum decode that avoids mis-framing while ignoring the hashes (06-RESEARCH Open Question 3 / A6, Pitfall 3). Record in `06-CAPTURE-DIFF.md`. Do NOT guess.
- [ ] **`server/entity_store_test.go`** (Plan 06-01) — `TestEntityIDAllocator` (monotonic, no collision, replaces `joinEntityID=1`), `TestEntityStore` (tick-owned add/remove/snapshot), `TestEntityBucketing` (per-section grid `near()` query). No entity store exists today.
- [ ] **`server/tracker_test.go`** (Plan 06-02) — `TestTracker`/`TestVisibilityDiff` (newly-visible → Add(+SetEntityData), moved → Move*, gone → one RemoveEntities), `TestAddEntityWire`/`TestSetEntityDataWire` (jar-derived encoders).
- [ ] **`server/physics_test.go`** (Plan 06-03) — `TestPerAxisSweep` (entity lands on floor, onGround), `TestPhysics` (gravity + horizontal block), `TestNoTunnel` (fast vector does not clip a thin wall).
- [ ] **`world/manager_test.go`** (Plan 06-04, extend) — `TestSetBlock` (pos→column/section/local mapping mutates the chunk), `TestSetBlockUnloaded` (an unloaded-column edit is a no-op, no panic).
- [ ] **`server/block_interact_test.go`** (Plan 06-04) — `TestBlockInteract` (break→air, place→held state), `TestBlockReconcile` (BlockChangedAck(sequence) + BlockUpdate to trackers; out-of-reach rejected).
- [ ] **`server/inventory_test.go`** + **`level/component` extended-encoder test** (Plan 06-05) — `TestSlotEncode` (component-free + one-component `SlotData.WriteTo`), `TestInventory` (`ContainerSetContent`/`SetSlot` round-trip), `TestContainerClickDecode` (the HashedStack form, no mis-frame).
- [ ] **`server/combat_test.go`** (Plan 06-05) — `TestSetHealthWire`, `TestCombat` (damage→death→PlayerCombatKill), `TestRespawn` (ClientCommand→Respawn reusing the sealed spawn-info encoder + dataToKeep byte).
- [ ] **`server/persistence_test.go`** + **`save` round-trip** (Plan 06-05) — `TestPlayerDataRoundTrip` (`.dat` save/load recovers state; missing→defaults), `TestEntityRegionRoundTrip` (`entities/r.*.mca` save/load).
- [ ] **`server/entity_capture_test.go`** (Plan 06-06) — `TestEntityBytesVsVanillaCapture`/`TestSlotBytesVsVanillaCapture`: load the committed golden `.bin`s, byte-diff Sulfur's `SetEntityData`/`AddEntity`/`ContainerSetContent`/`ContainerClick` encoders. Skips with a clear message if a fixture is absent (native CI stays green; the capture is the gate).
- [ ] **Capture fixtures** (Plan 06-06) — committed golden `.bin`s of the vanilla 26.2 `ClientboundAddEntity` (moving), `ClientboundSetEntityData`, `ClientboundContainerSetContent`, and a `ServerboundContainerClick` (HashedStack form), plus `06-CAPTURE-DIFF.md` recording the capture method, the per-field diff, and the resolution of the Open Questions (LP short scaling, metadata necessity, HashedStack decode depth, physics faithfulness).

*(Movement routing into the subtick buffer + the `UseItemOn`/`PlayerAction`/`Swing`/`ContainerClick` dispatch routing already exist — Wave 0 is the entity-store / tracker / physics / block-edit / inventory / combat / persistence / new-encoder layer.)*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Sulfur's `SetEntityData`/`AddEntity` (LP short scaling + 0xFF framing), `ContainerSetContent`, and the `ContainerClick` `HashedStack` byte-match a **real** vanilla 26.2 server | ENT-01, ENT-04 | No published spec covers proto 776; the entity-metadata serializer ids, the `Vec3.LP` short scaling, the component-slot value codecs, and the 1.21.5 `HashedStack` change are jar-confirmed in shape but their exact bytes drift between versions — only a byte-comparison against the actual vanilla server output proves vanilla-correctness. | Stand up `temp/cache/26.2-server.jar` (JDK 25) offline/flat. Capture each clientbound packet (logging proxy, or point the fork `bot/` client at vanilla and dump) — including a MOVING entity's `AddEntity`+`SetEntityData` to seal the LP scaling, and a `ContainerClick` round-trip for the HashedStack form. Commit each as a golden `.bin`. Diff the framing. Resolve the Open Questions. Write `06-CAPTURE-DIFF.md`. |
| A real vanilla 26.2 client: an entity is VISIBLE; place/break a block and SEE it; take damage and SEE the health bar; death→respawn; no kick/hang | ENT-01, ENT-02, ENT-03, ENT-05 | The decisive interactive check — a real client rendering a spawned entity, a placed block persisting (no ghost), the health bar dropping, and a death/respawn flow cannot be self-approved (mirrors Phases 4/5). | Connect an unmodified vanilla 26.2 client (PrismLauncher) to `cmd/sulfur`. Confirm: (a) a spawned entity is VISIBLE and moves; (b) placing a block shows it persist and breaking one removes it, with no ghost/snap-back on a valid edit; (c) taking damage drops the on-screen health bar; (d) on death the death screen appears and respawn returns the player to a streamed world; (e) no kick / "Loading terrain…" hang. |

---

## Validation Sign-Off

- [ ] Every task has an `<automated>` verify command or an explicit Wave 0 dependency (Nyquist).
- [ ] Sampling continuity: no 3 consecutive tasks without an automated build/test check.
- [ ] The entity-store/tracker, physics, block-edit, and inventory tasks run under Docker `-race` (every entity/physics/block/inventory mutation is tick-owned — prove no off-tick mutation, TICK-05).
- [ ] Wave 0 jar-derives the `Vec3.LP` short scaling, the `SetEntityData` 0xFF framing, and the `HashedStack`/`HashedPatchMap` layout BEFORE the encoders/decoders are written, and stands up the per-requirement test scaffolds before implementation; the capture fixtures are committed before the capture-diff test runs.
- [ ] The ENT-01/04 capture-diff against a real vanilla 26.2 server and the ENT-01..05 real-client interactive check are required phase gates (human sign-off in 06-06), NOT self-round-trips.
- [ ] No watch-mode flags.
- [ ] Feedback latency < 120s for the automated suite (vanilla capture-diff + real-client interactive check excluded — they are the phase gate).
- [ ] `nyquist_compliant: true` (every ENT-0x behavior maps to an automated command + a Wave 0 scaffold; the entity-metadata framing, the component-slot/HashedStack wire surfaces, and the interactive check are the prescribed jar/capture/real-client gates, justified by the bleeding-edge-proto research findings; the physics constants are unit-tested for the VISIBLE behaviors, not capture-diffed, per A1).

**Approval:** pending
</content>
</invoke>
