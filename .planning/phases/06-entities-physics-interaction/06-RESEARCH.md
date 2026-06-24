# Phase 6: Entities, Physics & Interaction - Research

**Researched:** 2026-06-24
**Domain:** Authoritative entity simulation, AABB physics, block interaction, component-slot inventory, health/respawn, Anvil persistence — all SYNCHRONOUS, tick-owned, behind the Phase-3 seams (no Phase-8 async).
**Confidence:** HIGH (fork APIs + jar-derived wire formats verified on disk; the residual MEDIUM items are flagged for capture-diff, the project's proven method).

## Summary

Phase 6 turns the first-playable empty world interactive. The architecture seams were deliberately pre-shaped in Phase 3: `tickEntities()`/`tickPhysics()` are empty stubs in `server/tick_phases.go`, `tracker.Tick()` is a `noopTracker` behind a one-method interface in `server/tick.go`, and `applyAsyncResults` stays a no-op (Phase 8 fills it). Phase 6 fills the SYNCHRONOUS reference implementations behind these seams — it does NOT introduce xsync/ants/conc, does NOT make the tracker async, and does NOT mutate game state off the tick goroutine. Everything (entity store, physics, block edits, inventory) is tick-owned, mutated only on the tick goroutine, exactly like the players slice and the ChunkManager today.

The fork already provides almost every data dependency: the generated 776 entity table (`data/entity`, 158 entities incl. `sulfur_cube`), 32366 block states with `block.ToStateID`/`StateList` (`level/block`), 111 generated component schemas incl. the `SlotData` codec (`level/component`), the Anvil region IO (`save`, `save/region`), reflection NBT (`nbt`), a generic AABB BVH (`server/internal/bvh`), and — critically — a jar-derived, capture-diff-sealed `commonPlayerSpawnInfoEncoder` in `server/play_join.go` that `ClientboundRespawn` reuses verbatim. The work is server-side game logic (tracker visibility, AABB sweep, block-edit reconciliation, inventory state machine, death/respawn flow, persistence wiring) plus a small number of new clientbound encoders whose exact byte layout must be jar-derived/capture-diffed.

**Primary recommendation:** Build six requirement-slices in dependency order — ENT-01 (entity store + synchronous tracker) first because every other slice needs an entity model; then ENT-02 (physics) on top of the entity AABBs; ENT-03 (block place/break) needing a new `ChunkManager` block-edit API; ENT-04 (component-slot inventory) the highest-risk wire surface; ENT-05 (health/respawn) reusing the existing spawn-info encoder; ENT-06 (persistence) last, reusing `save`/`save/region` wholesale. Capture-diff three wire surfaces against a real vanilla 26.2 server: **entity metadata (`SetEntityData`)**, **the component-slot `ItemStack` codec**, and **`ContainerSetContent`/`ContainerClick` (the HashedStack mechanism)**. `AddEntity` and `Respawn` are jar-derived in this document and need only a confirming diff.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Entity store / spawn / metadata | Tick (game state) | — | Entities are tick-owned game state, mutated only on the tick goroutine (TICK-05). |
| Entity tracker (visibility → spawn/move/despawn) | Tick (synchronous, behind `tracker.Tick()`) | — | The Phase-3 seam runs on-tick today; Phase 8 swaps the executor off-tick. Phase 6 fills the synchronous reference impl. |
| Physics (gravity + AABB collision) | Tick (`tickPhysics`) | Subtick (player movement validation) | Server is authoritative; entity physics is full simulation, player movement is validate-against-collision of client-sent positions. |
| Block place/break | Tick (mutates `ChunkManager`) | Net (decode serverbound) | Block edits mutate tick-owned chunk data; reconciliation (`BlockUpdate`/`BlockChangedAck`) flushed via `Client.Send`. |
| Inventory state | Tick (per-player) | Net (decode `ContainerClick`) | Inventory is authoritative server state; client sends intents (HashedStack digests), server replies with full `ItemStack`. |
| Health / damage / respawn | Tick (per-player) | Net (`ClientCommand` respawn intent) | Death/respawn is a server-driven state machine; client only requests respawn. |
| Persistence (entity + player .dat) | Off-tick IO (reuse `save`) | Tick (snapshot → write) | Region IO is the same off-tick pattern as chunk load; the tick owns the snapshot, IO happens off the critical path. |

## Standard Stack

Phase 6 adds **zero new third-party dependencies.** Everything is the fork's own packages + stdlib. (Per CLAUDE.md "Stack Patterns by Variant": do NOT introduce ants/xsync/conc yet — no async subsystems exist to optimize until Phase 8.)

### Core (reuse — already on disk)
| Package | API surface | Purpose | Why reuse |
|---------|-------------|---------|-----------|
| `data/entity` | `entity.Entity{ID, Name, Width, Height, Type}`, vars like `entity.SulfurCube`, `entity.Pig` | Authoritative 776 entity table (158 entities); `ID` is the wire entity-type id; `Width`/`Height` are the AABB dimensions for physics | `[VERIFIED: data/entity/entity.go]` Generated from `26.2/entities.json`; hand-transcription is a non-starter. |
| `level/block` | `block.ToStateID map[Block]StateID`, `block.StateList []Block`, `block.StateID`, `block.IsAir(StateID)`, `block.Stone{}`, `block.Air{}` | Block→state-id resolution for place/break; `ToStateID[block.Air{}]` is the break target, `ToStateID[block.Stone{}]` the place example | `[VERIFIED: level/block/block.go, utilfuncs.go]` 32366 states; the generator already uses `block.ToStateID[block.Stone{}]`. |
| `level` (chunk/section) | `(*Section).GetBlock(i)`, `(*Section).SetBlock(i, BlocksState)` (maintains BlockCount), `level.Chunk.Sections`, `level.ChunkPos`, `BlocksState = block.StateID` | Per-section block mutation for ENT-03; `SetBlock` already adjusts the non-air count for correct re-encode | `[VERIFIED: level/chunk.go:468-480, level/palette.go:16]` |
| `level/component` | `SlotData{Count, ItemID, AddedCount, RemovedCount, RawComponents}` with `ReadFrom`/`WriteTo`; `NewComponent(id)`; 111 component schemas | The component-slot `ItemStack` wire core for ENT-04 | `[VERIFIED: level/component/types.go:239-303, components.go]` `WriteTo` currently writes 0 added/0 removed — minimal; extend for real items. |
| `save` | `save.PlayerData` (full player .dat NBT struct incl. Inventory `[]Item`, Health, Pos, Abilities), `ReadPlayerData`, `save.Chunk{Entities []nbt.RawMessage, BlockEntities}`, `save.Entities` (entity NBT struct), `save.Level`/`LevelData` (level.dat) | Player + entity + chunk persistence for ENT-06 | `[VERIFIED: save/playerdata.go, save/chunk.go]` `save.Item.Tag map[string]any` is the legacy on-DISK NBT-tag slot (disk format ≠ wire format — see Pitfall 6). |
| `save/region` | `region.Open/Create`, `(*Region).ReadSector(x,z)`, `WriteSector(x,z,data)`, `In`/`At` | Anvil `.mca` region IO — same API Phase 4 chunk load uses; entities go in a parallel `entities/*.mca` region | `[VERIFIED: save/region/mca.go]` Never write Anvil from scratch. |
| `nbt` | `nbt.NewEncoder/Decoder`, reflection struct mapping, `nbt.RawMessage` | Player/entity/level.dat (de)serialization | `[VERIFIED: save/*.go all use it]` Do NOT add a second NBT lib. |
| `server/internal/bvh` | Generic `AABB[I, V]{Upper, Lower}` with `WithIn`/`Touch`/`Union`/`Surface`; `Tree` (BVH) | AABB primitive for physics broad/narrow phase. **Use `AABB.Touch` for collision tests.** The `Tree` is a fallback only — start with per-section grid bucketing per CLAUDE.md | `[VERIFIED: server/internal/bvh/bound.go, bvh.go]` Generic, zero-extra-dep (only `golang.org/x/exp/constraints`). |

### 776 Packet IDs (all present in the generated `data/packetid` table — verified)

| Requirement | Clientbound | Serverbound |
|-------------|-------------|-------------|
| ENT-01 spawn/metadata/move | `ClientboundAddEntity`, `ClientboundSetEntityData`, `ClientboundMoveEntityPos/PosRot/Rot`, `ClientboundTeleportEntity`, `ClientboundEntityPositionSync`, `ClientboundRotateHead`, `ClientboundSetEntityMotion`, `ClientboundRemoveEntities`, `ClientboundSetEquipment` | — |
| ENT-03 block | `ClientboundBlockUpdate`, `ClientboundBlockChangedAck`, `ClientboundSectionBlocksUpdate` | `ServerboundPlayerAction`, `ServerboundUseItemOn`, `ServerboundUseItem`, `ServerboundSwing` |
| ENT-04 inventory | `ClientboundContainerSetContent`, `ClientboundContainerSetSlot`, `ClientboundSetCursorItem`, `ClientboundSetPlayerInventory`, `ClientboundSetHeldSlot` | `ServerboundContainerClick`, `ServerboundContainerClose`, `ServerboundSetCreativeModeSlot`, `ServerboundSetCarriedItem`, `ServerboundPickItemFromBlock` |
| ENT-05 health/respawn | `ClientboundSetHealth`, `ClientboundPlayerCombatKill`, `ClientboundRespawn`, `ClientboundDamageEvent`, `ClientboundHurtAnimation`, `ClientboundSetExperience` | `ServerboundClientCommand` (perform respawn) |

`[VERIFIED: data/packetid/packetid.go + clientboundpacketid_string.go]` All IDs resolve via the generated table.

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Per-section grid bucketing (entity broad-phase) | `server/internal/bvh.Tree` | CLAUDE.md mandates grid bucketing first (vanilla buckets entities per section); reach for the BVH only if profiling demands. Not a first move. |
| Extending `SlotData` for the wire `ItemStack` | A from-scratch ItemStack codec | `SlotData` already has the correct `Count/ItemID/AddedCount/RemovedCount` shape and component dispatch — extend it, don't replace. |
| `save.PlayerData` struct | A new player-state schema | The fork's struct already matches the vanilla player.dat NBT layout; reuse it. |

**Installation:** None. No `go get`. `go.mod` is unchanged for Phase 6.

**Version verification:** No external packages added, so no `npm view`/registry check applies. All "versions" are the pinned fork tree (branch `ender-776`); authority is the on-disk generated code, re-verified by reading the files above.

## Architecture Patterns

### System Architecture Diagram

```
                         ┌─────────────────────────────────────────────┐
   vanilla 26.2 client   │            TICK GOROUTINE (single owner)      │
        │   ▲            │                                              │
   serverbound │ clientbound                                            │
        ▼   │            │   drainInbound → dispatch (decode + route)   │
   ┌─────────────┐       │        │                                     │
   │ net reader  │──Intent──►  per-player subtick buffer (movement,     │
   │ (1/conn)    │       │        │     use, attack, place/break)        │
   └─────────────┘       │        ▼                                     │
   ┌─────────────┐       │   ── tickOnce() ORDERED PIPELINE ──          │
   │ net writer  │◄─Send─┤   resolveSubtickInputs  (player move+collide)│
   │ (1/conn)    │       │   tickWorld                                  │
   └─────────────┘       │   tickChunks                                 │
                         │   tickEntities ◄── ENT-01: spawn/move/age    │
                         │        │            entity store (tick-owned) │
                         │   tickAI            (Phase 7)                 │
                         │   tickPhysics ◄── ENT-02: gravity + AABB     │
                         │        │            sweep vs world blocks     │
                         │   applyAsyncResults (NO-OP; Phase 8)         │
                         │   tracker.Tick() ◄── ENT-01: SYNCHRONOUS     │
                         │        │     per-section bucket → per-player  │
                         │        │     visibility diff → AddEntity /    │
                         │        │     MoveEntity / RemoveEntities      │
                         │   flushOutbound ── BlockUpdate (ENT-03),     │
                         │        │            SetHealth/Respawn (ENT-05),│
                         │        │            ContainerSetContent(ENT-04)│
                         └────────┼─────────────────────────────────────┘
                                  │  block edits mutate ↓
                         ┌────────▼──────────┐      ┌──────────────────┐
                         │  ChunkManager     │      │  off-tick IO     │
                         │  (tick-owned)     │◄────►│  save/region     │
                         │  + NEW block-edit │ snap │  player.dat,     │
                         │    API (ENT-03)   │ shot │  entities/*.mca  │
                         └───────────────────┘      │  (ENT-06)        │
                                                    └──────────────────┘
```

Entry: serverbound packets → `dispatch` (already routes `UseItemOn`/`PlayerAction`/`Swing`/`ContainerClick` into the subtick buffer — see `server/tick.go:479-490`). Processing: the fixed `tickOnce` pipeline. Block edits mutate the tick-owned `ChunkManager`; the tracker and flush emit clientbound packets through the single writer.

### Recommended Project Structure
```
server/
├── entity.go          # EXTEND: currently only Pos/Rot. Add Entity struct
│                      #   (id, type entity.ID, uuid, pos, vel, yaw/pitch/headYaw,
│                      #   aabb, metadata, onGround) + entity store on TickLoop.
├── entity_store.go    # NEW: tick-owned entity map + per-section bucketing + ID alloc
├── tracker.go         # NEW: synchronous tracker behind tracker.Tick() — visibility
│                      #   diff per player → AddEntity/Move/RemoveEntities encoders
├── physics.go         # NEW: gravity + per-axis AABB sweep (ENT-02)
├── block_interact.go  # NEW: PlayerAction/UseItemOn handlers → ChunkManager edit
│                      #   + BlockUpdate/BlockChangedAck (ENT-03)
├── inventory.go       # NEW: per-player inventory state + ContainerClick handling
│                      #   + ItemStack/Slot encoders (ENT-04)
├── combat.go          # NEW: health/damage/death/respawn flow (ENT-05)
├── persistence.go     # NEW: player.dat + entity region snapshot/restore (ENT-06)
├── tick_phases.go     # EDIT: fill tickEntities()/tickPhysics() (currently stubs)
├── tick.go            # EDIT: tickPlayer gains health/inventory/entityID fields;
│                      #   TickLoop gains entity store + sets a real tracker
└── play_join.go       # REUSE: commonPlayerSpawnInfoEncoder for ENT-05 Respawn
world/
└── manager.go         # EXTEND: add GetBlock/SetBlock(blockPos) → (cx,cz,localY,localXZ)
                       #   mapping + the edited-column dirty flag for ENT-06
```

### Pattern 1: Synchronous entity tracker behind `tracker.Tick()` (ENT-01)
**What:** The `tracker` interface (`type tracker interface{ Tick() }`) is already in `server/tick.go:75`, defaulted to `noopTracker`. Phase 6 implements a real synchronous tracker and assigns it to `t.tracker` (do NOT change the interface or the `t.tracker.Tick()` call site at `tick_phases.go:26`).
**When to use:** Every tick, after physics and `applyAsyncResults`.
**How (synchronous reference impl):**
1. Bucket entities by chunk-section grid (per CLAUDE.md — NOT a quadtree). A `map[level.ChunkPos][]*Entity` (or section key) rebuilt/maintained as entities move.
2. For each player, compute the visible set (entities within the player's track range — vanilla uses entity-type-specific ranges, e.g. players 48, most mobs 80 blocks; start with a single conservative range and refine).
3. Diff against the player's `previouslyTracked` set: newly-visible → `ClientboundAddEntity` (+ initial `ClientboundSetEntityData` if metadata non-default + `ClientboundSetEntityMotion` if moving); still-visible-and-moved → `MoveEntityPos/PosRot/Rot` or `TeleportEntity`/`EntityPositionSync` for large deltas + `RotateHead`; no-longer-visible → batch into one `ClientboundRemoveEntities`.
4. The tracker runs ON the tick goroutine (synchronous), reads tick-owned entity state, and emits via `player.client.Send`. Phase 8 (OPT-02) moves the visibility computation off-tick and rejoins via `applyAsyncResults` — the seam already exists.

**Critical:** keep entity "hot fields" (pos, vel, yaw) snapshot-friendly (plain value fields on the Entity struct) so the Phase-8 async tracker can copy a cheap immutable snapshot. (ROADMAP Phase 6 note: "keep entity hot-fields snapshot-friendly.")

```go
// Source: shape derived from server/tick.go:75-82 (existing seam) + tick_phases.go:26
// Phase 6 ASSIGNS a real tracker; the interface and call site are UNCHANGED.
type entityTracker struct{ loop *TickLoop }

func (et *entityTracker) Tick() {
    for _, p := range et.loop.players {
        visible := et.loop.entities.near(p.x, p.y, p.z, trackRange) // grid bucket query
        for _, e := range visible {
            if !p.tracked[e.id] {
                p.client.Send(encodeAddEntity(e))      // jar-derived encoder (below)
                if e.hasMetadata { p.client.Send(encodeSetEntityData(e)) }
                p.tracked[e.id] = true
            } else {
                p.client.Send(encodeMove(e))            // delta or teleport
            }
        }
        et.loop.removeStale(p, visible) // → one ClientboundRemoveEntities
    }
}
```

### Pattern 2: AABB physics — per-axis sweep with gravity/drag (ENT-02)
**What:** Vanilla collision is a per-axis swept-AABB against the world's solid block AABBs, applied X then Z then Y (vanilla order is Y, X, Z for the move resolution but the canonical sequence is: compute desired motion, then `Entity.move` clips Δ per axis against colliding block boxes). Gravity is applied as a downward acceleration each tick; horizontal motion has drag (friction) from the block beneath.
**When to use:** `tickPhysics()` for non-player entities (full simulation); `resolveSubtickInputs`/`applyInput` for players (the server VALIDATES the client-sent position against collision — the player already sends authoritative-ish positions; the server collides them so the client can't clip through solid blocks).
**Vanilla constants (training-knowledge, MEDIUM — verify against jar `Entity`/`LivingEntity` if exactness matters):**
- Gravity ≈ 0.08 blocks/tick² for most living entities (items 0.04); applied as `vy -= gravity` then `vy *= 0.98` (air drag) `[ASSUMED]`.
- Horizontal friction: `vx *= friction*0.91`, friction from `Block.getFriction` (default 0.6, ice ~0.98) `[ASSUMED]`.
- Step height: players/most mobs auto-step up 0.6 blocks `[ASSUMED]`.
**How:** Build the entity AABB from `entity.Entity.Width`/`Height` (centered on x/z, base at y). Use `bvh.AABB.Touch` against each candidate solid block's unit AABB in the swept volume. Resolve each axis independently to avoid tunneling (never move the full vector then test — that lets fast entities pass through thin walls).

```go
// Source: AABB primitive verified at server/internal/bvh/bound.go:9-31
// Entity dims verified at data/entity/entity.go:9-17 (Width/Height float64).
// Per-axis sweep prevents tunneling (Pitfall 4).
func (t *TickLoop) moveEntity(e *Entity, dx, dy, dz float64) {
    dy = t.clipAxisY(e, dy) // collide Y first, then re-derive box
    e.y += dy
    dx = t.clipAxisX(e, dx); e.x += dx
    dz = t.clipAxisZ(e, dz); e.z += dz
    e.onGround = dy < 0 && /* clipped */ collidedDown
}
```

### Pattern 3: Block place/break + reconciliation (ENT-03)
**What:** Serverbound `ServerboundPlayerAction` (dig: START_DESTROY_BLOCK / STOP / FINISH stages, `Action` enum + `BlockPos pos` + `Direction direction` + `VarInt sequence`) drives break; `ServerboundUseItemOn` (`BlockHitResult blockHit` {BlockPos, Direction, cursor Vec3, insideBlock} + `InteractionHand hand` + `VarInt sequence`) drives place. The server validates, mutates the tick-owned chunk, then ACKs the sequence and broadcasts the change.
**When to use:** Decoded in `dispatch` (already routed into the subtick buffer at `tick.go:489`), resolved on-tick.
**The reconciliation contract (authoritative server):**
1. Client predicts the edit locally and sends the action with a monotonic `sequence`.
2. Server applies (or rejects) the edit to the `ChunkManager`, then sends `ClientboundBlockChangedAck(sequence)` — this tells the client "your prediction up to N is reconciled; revert anything I didn't confirm."
3. Server sends `ClientboundBlockUpdate(BlockPos, VarInt newStateId)` to all players tracking that chunk (the editor included, so a rejected edit snaps back).
**Requires a NEW `ChunkManager` block-edit API** — there is currently NO `GetBlock`/`SetBlock` at the manager level (`world/manager.go` is pure holder storage). Add: `func (m *ChunkManager) SetBlock(pos BlockPos, state block.StateID) (changed bool)` that maps block→column (floor-div 16, reuse `chunkCenterOf` logic from `server/world_stream.go:82`), section (Y>>4), and local index, then calls `Section.SetBlock`. Break = set to `block.ToStateID[block.Air{}]`; place = set to the held item's block state.

```go
// Source: Section.SetBlock verified level/chunk.go:472; air lookup level/block/utilfuncs.go;
// ToStateID verified level/block/block.go:25. ChunkManager has NO block API yet — add it.
airState := block.ToStateID[block.Air{}]
if t.world.SetBlock(pos, airState) {           // mutate tick-owned chunk
    p.client.Send(blockChangedAck(seq))         // reconcile client prediction
    t.broadcastBlockUpdate(pos, airState)       // ClientboundBlockUpdate to trackers
}
```

### Pattern 4: Component-slot inventory + HashedStack click (ENT-04)
**What:** Post-1.20.5 slots are component-based (NO NBT-in-slot on the wire). The wire `ItemStack` = `VarInt count` (0 ⇒ empty, stop) then `VarInt itemId`, `VarInt addedComponentCount`, `VarInt removedComponentCount`, then `addedCount` × (`VarInt componentTypeId` + component value via `component.NewComponent(id)` codec), then `removedCount` × `VarInt componentTypeId`. This is EXACTLY the `SlotData.ReadFrom` shape (`level/component/types.go:250`).
**1.21.5+ change (verified):** `ServerboundContainerClick.changedSlots` and `carriedItem` are **`HashedStack`** (a CRC-hash digest), NOT full `ItemStack`. The client sends a *hash* of what it thinks each changed slot now holds; the server holds the authoritative inventory and replies with full `ItemStack`s via `ClientboundContainerSetContent`/`ClientboundContainerSetSlot`. `HashedPatchMap` is present in the jar (`net/minecraft/network/HashedPatchMap`). The server can largely IGNORE the client's hashes for v1 (just re-send authoritative content), but MUST decode the packet without mis-framing.
**When to use:** `ContainerClick`/`SetCreativeModeSlot`/`SetCarriedItem` in `dispatch`; player inventory is per-player tick-owned state.
**Encoder gap:** `SlotData.WriteTo` (`types.go:292`) currently writes `count, itemId, VarInt(0), VarInt(0)` — empty component lists only. For real items with components, EXTEND it to serialize `RawComponents`/added list. For v1 most stacks are component-free (count+id+0+0) so the minimal encoder is the starting point.
**Field order — `ContainerClick` (verified):** `VarInt containerId`, `VarInt stateId`, `Short slotNum`, `Byte buttonNum`, `ContainerInput`/click-type, `Int2ObjectMap<HashedStack> changedSlots`, `HashedStack carriedItem`.
**Field order — `ContainerSetContent` (verified):** `VarInt containerId`, `VarInt stateId`, `List<ItemStack> items`, `ItemStack carriedItem`.

### Pattern 5: Health / damage / respawn flow (ENT-05)
**What:** Damage lowers `tickPlayer.health` (new field); the server sends `ClientboundSetHealth(Float health, VarInt food, Float saturation)` — **verified wire order**. On death (health ≤ 0): send `ClientboundPlayerCombatKill(VarInt playerId, Component message)` (the death screen). The client requests respawn via `ServerboundClientCommand(PERFORM_RESPAWN)`; the server responds with `ClientboundRespawn` and re-teleports/streams.
**`ClientboundRespawn` (verified):** `CommonPlayerSpawnInfo.write` + `Byte dataToKeep`. **This REUSES the existing jar-derived, capture-diff-sealed `commonPlayerSpawnInfoEncoder` in `server/play_join.go:160-204`** — the only new byte is the trailing `dataToKeep` flags byte. No new wire research needed for Respawn beyond a confirming diff.
**When to use:** Combat/fall damage on-tick; respawn flow driven by `ServerboundClientCommand`.

```go
// Source: SetHealth wire order verified (writeFloat, writeVarInt, writeFloat);
// Respawn = CommonPlayerSpawnInfo.write + Byte (verified ClientboundRespawnPacket.write);
// commonPlayerSpawnInfoEncoder REUSED from play_join.go:160.
func setHealth(h float32, food int32, sat float32) pk.Packet {
    return pk.Marshal(int32(packetid.ClientboundSetHealth),
        pk.Float(h), pk.VarInt(food), pk.Float(sat))
}
func respawnPacket(dataToKeep byte) pk.Packet {
    return pk.Marshal(int32(packetid.ClientboundRespawn),
        commonPlayerSpawnInfoEncoder{}, pk.Byte(dataToKeep)) // encoder reused
}
```

### Pattern 6: Persistence via `save`/`save/region` (ENT-06)
**What:** Player state → `save.PlayerData` (NBT, written to `world/playerdata/<uuid>.dat` gzip). Entities → `save.Entities` NBT inside a parallel `entities/r.x.z.mca` region (vanilla split entities out of the chunk region in 1.17; chunk's `Entities []nbt.RawMessage` field is the legacy in-chunk slot — modern worlds use the entities region). Reuse `region.Create`/`WriteSector`/`ReadSector`.
**When to use:** Off-tick IO (same pattern as Phase 4 chunk load — the tick snapshots state, IO happens off the critical path; do NOT block the tick on disk). Save on player leave + periodic; load on join (fall back to spawn defaults if no .dat).
**Disk format ≠ wire format (CRITICAL):** `save.Item.Tag map[string]any` is the LEGACY 1.20.4 on-disk NBT-tag slot; modern player.dat uses component NBT. The DISK still uses NBT for slots (that's correct — the no-NBT-in-slots rule is a WIRE rule for ENT-04, not a disk rule). Persist via NBT, transmit via the component-slot codec.

### Anti-Patterns to Avoid
- **Making the tracker async in Phase 6:** The seam stays synchronous. No goroutines, no xsync/ants. Phase 8 (OPT-02) does that.
- **Mutating entity/chunk/inventory state off the tick goroutine:** Violates TICK-05; would break `-race`. All mutation on-tick.
- **Moving the full motion vector then testing collision:** Causes tunneling (fast entities clip walls). Always per-axis sweep.
- **Trusting client `HashedStack` data as authoritative items:** The server owns the inventory; client hashes are validation hints only. Re-send authoritative `ContainerSetContent`.
- **Re-deriving the Respawn spawn-info layout:** Reuse `commonPlayerSpawnInfoEncoder` — it's already sealed.
- **Hard-coding entity id 1:** `joinEntityID = 1` (`play_join.go:50`) was fine for one bootstrap player. Phase 6 needs a real monotonic entity-ID allocator (tick-owned `int` counter) since entities + multiple players now coexist.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Entity type table / dimensions | A hand-typed entity list | `data/entity` (158 generated entities, Width/Height) | `[VERIFIED]` Generated from the jar; AABB dims included. |
| Block ↔ state-id mapping | A custom block registry | `block.ToStateID` / `block.StateList` / `block.IsAir` | `[VERIFIED]` 32366 states; the generator already uses it. |
| Section block read/write | Raw palette bit-twiddling | `(*Section).GetBlock/SetBlock` (maintains BlockCount) | `[VERIFIED: level/chunk.go:468-480]` |
| Component-slot wire core | A from-scratch ItemStack codec | `component.SlotData` + `component.NewComponent(id)` | `[VERIFIED: types.go:239-303]` Extend the encoder; don't rewrite. |
| Anvil region IO | A new `.mca` reader/writer | `save/region` (`ReadSector`/`WriteSector`) | `[VERIFIED: save/region/mca.go]` Same API Phase 4 uses. |
| Player .dat schema | A custom player-save struct | `save.PlayerData` (full vanilla NBT layout) | `[VERIFIED: save/playerdata.go]` |
| NBT (de)serialization | A second NBT lib | `nbt` (reflection-based) | `[VERIFIED]` Already used by all `save/*`. |
| AABB intersection math | A new box-overlap impl | `bvh.AABB.Touch`/`WithIn` | `[VERIFIED: bound.go:19-31]` |
| Respawn spawn-info bytes | A new CommonPlayerSpawnInfo encoder | `commonPlayerSpawnInfoEncoder` (play_join.go) | `[VERIFIED: play_join.go:160]` Already capture-diff-sealed. |
| Packed BlockPos / byte angles | Manual bit-packing | `pk.Position`, `pk.Angle`, `pk.UUID` | `[VERIFIED: net/packet/types.go:402-443]` |

**Key insight:** ~80% of Phase 6's data layer already exists in the fork. The genuinely NEW code is server-side game LOGIC (tracker visibility, AABB sweep, block-edit reconciliation, inventory state machine, death/respawn, persistence wiring) plus a handful of new clientbound ENCODERS whose byte layout is jar-derived in this doc and confirmed by capture-diff.

## Runtime State Inventory

> Phase 6 is greenfield feature work (no rename/refactor/migration), so most categories are N/A. The one persistence-relevant note:

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | ENT-06 INTRODUCES persisted state: `world/playerdata/<uuid>.dat`, `world/entities/r.*.mca`. None exists yet (first write this phase). | New writes; ensure load-path falls back to defaults when absent. |
| Live service config | None | None — pure Go server. |
| OS-registered state | None | None. |
| Secrets/env vars | None | None. |
| Build artifacts | None — no module path or package renames this phase. | None. |

## Common Pitfalls

### Pitfall 1: Entity metadata indexed-entry mis-framing (`SetEntityData`)
**What goes wrong:** `ClientboundSetEntityData` is an open-ended list, NOT a fixed struct. Each entry = `Byte index`, `VarInt serializerTypeId`, then a serializer-specific value; the list TERMINATES with a single `Byte 0xFF` (255) marker. Omitting the 0xFF terminator, or getting a serializer's value codec wrong, desyncs the whole stream and the client drops/ignores the entity.
**Why it happens:** The ≤773 wiki and training data describe older metadata; 776 serializer type-ids and value codecs must come from the jar.
**How to avoid:** `[VERIFIED via javap: SynchedEntityData$DataValue.write = Byte id, VarInt serializerId, codec.encode(value); pack() ends with writeByte(255)]`. For v1, emit the MINIMUM metadata (often none — the 0xFF terminator alone, or just shared flags index 0). CAPTURE-DIFF the bytes for any entity that carries metadata.
**Warning signs:** Entity appears then vanishes; client log "unknown data value id".

### Pitfall 2: Component-slot ItemStack add/remove encoding (`ItemStack` / `ContainerSetContent`)
**What goes wrong:** The wire `ItemStack` is `count, id, addedCount, removedCount, [added components], [removed ids]`. The fork's `SlotData.WriteTo` writes `count, id, 0, 0` only — fine for component-free stacks but WRONG for any item carrying components (custom name, enchantments, etc.). Mis-encoding the added/removed lists shifts every subsequent slot in a `ContainerSetContent` list.
**Why it happens:** The minimal encoder was written for a simpler case; real inventories carry components.
**How to avoid:** Start with component-free stacks (count+id+0+0). When components are needed, extend `SlotData.WriteTo` to emit the added list via `component.NewComponent`. CAPTURE-DIFF `ContainerSetContent` against a real server holding a known inventory. `[VERIFIED: SlotData shape types.go:250-303; ItemStack.STREAM_CODEC present]`
**Warning signs:** Inventory shows wrong/ghost items; client desync on click.

### Pitfall 3: HashedStack in `ContainerClick` (1.21.5+)
**What goes wrong:** Decoding `ContainerClick.changedSlots`/`carriedItem` as full `ItemStack` (the old format) instead of `HashedStack` (CRC digest) mis-frames the packet — the server reads garbage and may panic or mis-route the click.
**Why it happens:** This is a recent (1.21.5) protocol change post-dating most docs.
**How to avoid:** `[VERIFIED via javap: ContainerClick.changedSlots is Int2ObjectMap<HashedStack>, carriedItem is HashedStack; HashedPatchMap present in jar]`. Decode the hashed form (or, since the server is authoritative, decode-and-discard the hashes and re-send authoritative content). CAPTURE-DIFF a real click round-trip.
**Warning signs:** Scan error / panic on click; inventory drift.

### Pitfall 4: AABB tunneling and step-height
**What goes wrong:** Moving an entity by its full motion vector then testing collision lets fast entities pass through thin walls (tunneling); ignoring step-height makes players stick on 1-block edges they should auto-climb.
**Why it happens:** Naive `pos += velocity; if collide { revert }` instead of vanilla per-axis swept resolution.
**How to avoid:** Resolve each axis independently (clip Δy, then Δx, then Δz against colliding block boxes), and apply the ~0.6 step-up for players/most mobs. Use `bvh.AABB.Touch` per candidate block in the swept volume. `[ASSUMED constants — verify gravity 0.08/drag 0.98/step 0.6 against jar Entity/LivingEntity if exactness matters]`
**Warning signs:** Entities clip walls; player snaps/sticks on slabs/stairs; rubber-banding.

### Pitfall 5: Block-update desync (reconciliation)
**What goes wrong:** Applying a block edit server-side but not sending `BlockChangedAck(sequence)` leaves the client's predicted edit "uncommitted" — on a rejected/adjusted edit the client never snaps back, producing ghost blocks.
**Why it happens:** The ack/sequence handshake is easy to omit; the edit "works" visually because the client predicted it.
**How to avoid:** Always send `ClientboundBlockChangedAck(VarInt sequence)` for the action's sequence AND `ClientboundBlockUpdate` to all trackers (editor included). `[VERIFIED: BlockChangedAck = VarInt sequence; PlayerAction/UseItemOn carry VarInt sequence]`
**Warning signs:** Phantom blocks after a denied edit; placing a block where one already is leaves a ghost.

### Pitfall 6: Disk-NBT vs wire-component slot confusion
**What goes wrong:** Trying to write the WIRE component-slot format to player.dat, or reading the disk NBT-tag slot as if it were the wire format. They are different: disk = NBT (`save.Item.Tag map[string]any`), wire = component list.
**Why it happens:** "No NBT in slots" (ENT-04) is a WIRE rule; it does not apply to disk persistence.
**How to avoid:** Persist via `nbt` through `save.PlayerData`; transmit via `component.SlotData`. Keep the two codecs separate. `[VERIFIED: save.Item.Tag is map[string]any disk NBT; SlotData is the wire codec]`
**Warning signs:** Round-trip save/load corrupts inventory; or client rejects a slot built from disk NBT.

### Pitfall 7: Entity-ID collisions / the hard-coded `joinEntityID = 1`
**What goes wrong:** Spawning entities or a second player while the bootstrap still uses `joinEntityID = 1` collides ids — the client merges/confuses entities.
**Why it happens:** `play_join.go:50` hard-codes id 1 for the single first-playable player.
**How to avoid:** Add a tick-owned monotonic entity-ID allocator; assign each player AND each entity a unique id from it; thread the player's id into the join bootstrap instead of the constant. `[VERIFIED: play_join.go:50 const joinEntityID = 1]`
**Warning signs:** Entities flicker/teleport onto each other; the player's own entity behaves oddly when a mob spawns.

## Code Examples

### Resolve a block state id for place/break
```go
// Source: level/block/block.go:25 (ToStateID), utilfuncs.go (IsAir), generator uses this pattern
import "github.com/imhinotori/sulfur/level/block"
airState   := block.ToStateID[block.Air{}]   // break target
stoneState := block.ToStateID[block.Stone{}] // example place
// place/break = world.SetBlock(pos, state) — NEW manager API mapping pos→(column,section,local)
```

### Build an entity AABB from the generated table
```go
// Source: data/entity/entity.go:9-17 (Width/Height), bvh/bound.go:9 (AABB)
e := entity.SulfurCube // ID is the wire entity-type id; Width/Height are block dims
halfW := e.Width / 2
// AABB lower={x-halfW, y, z-halfW} upper={x+halfW, y+e.Height, z+halfW}
```

### Reuse the sealed spawn-info encoder for Respawn (ENT-05)
```go
// Source: play_join.go:160 (commonPlayerSpawnInfoEncoder, capture-diff-sealed via Login)
// ClientboundRespawn = CommonPlayerSpawnInfo.write + Byte dataToKeep (verified write())
pk.Marshal(int32(packetid.ClientboundRespawn),
    commonPlayerSpawnInfoEncoder{}, pk.Byte(dataToKeep))
```

### AddEntity (ENT-01) — jar-derived wire order
```go
// Source: javap -c ClientboundAddEntityPacket.write (verified field order):
//   VarInt id, UUID uuid, VarInt entityTypeId, Double x, Double y, Double z,
//   Vec3.LP_STREAM_CODEC movement (LOW-PRECISION: 3×Short, not 3×Double),
//   Byte xRot, Byte yRot, Byte yHeadRot, VarInt data
// NOTE the LP (low-precision short) movement encoding — NOT three doubles. Confirm via capture-diff.
pk.Marshal(int32(packetid.ClientboundAddEntity),
    pk.VarInt(e.id), pk.UUID(e.uuid), pk.VarInt(int32(e.typeID)),
    pk.Double(e.x), pk.Double(e.y), pk.Double(e.z),
    pk.Short(velShort(e.vx)), pk.Short(velShort(e.vy)), pk.Short(velShort(e.vz)),
    pk.Angle(degToByte(e.pitch)), pk.Angle(degToByte(e.yaw)), pk.Angle(degToByte(e.headYaw)),
    pk.VarInt(e.data))
```

### SetHealth (ENT-05) — jar-verified order
```go
// Source: javap -c ClientboundSetHealthPacket: writeFloat, writeVarInt, writeFloat
pk.Marshal(int32(packetid.ClientboundSetHealth),
    pk.Float(health), pk.VarInt(food), pk.Float(saturation))
```

## State of the Art

| Old Approach (≤773 wiki / training) | Current Approach (776, jar-verified) | When Changed | Impact |
|-------------------------------------|--------------------------------------|--------------|--------|
| ItemStack slot carries NBT tag | Component list: count, id, added[], removed[] | 1.20.5 | ENT-04 wire uses `SlotData`/component codecs, not NBT. |
| `ContainerClick` sends full ItemStacks | `ContainerClick` sends `HashedStack` (CRC digest) | 1.21.5 | Decode hashed form; server stays authoritative. |
| Entities stored in chunk region | Entities in separate `entities/*.mca` region | 1.17 | ENT-06 writes a parallel entities region (chunk `Entities` field is legacy). |
| `MovePlayer` trailing Boolean onGround | Packed flags byte (&1 onGround, &2 horizCollision) | 1.21.3 | Already handled Phase 5 (subtick.go). |
| Respawn carried loose dimension fields | `CommonPlayerSpawnInfo` record + dataToKeep byte | 1.20.2-era | ENT-05 reuses the sealed Login encoder. |

**Deprecated/outdated:** Any pre-1.20.5 slot-NBT example; any pre-1.21.5 ContainerClick example; the ≤773 wiki for entity metadata serializer ids.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Gravity ≈ 0.08, air drag 0.98, horizontal friction base 0.6×0.91, step height 0.6 | Pattern 2 / Pitfall 4 | Entities fall/move at wrong speed; cosmetic for v1 but verify against jar `Entity`/`LivingEntity`/`Block.getFriction` if parity matters. Does NOT affect wire framing. |
| A2 | `AddEntity` movement field uses `Vec3.LP_STREAM_CODEC` = 3×Short low-precision (not 3×Double) | Code Examples / Pattern 1 | Mis-framed AddEntity → entity drops. Bytecode showed `LP_STREAM_CODEC`; CONFIRM exact short scaling via capture-diff. |
| A3 | Minimal metadata (0xFF terminator only / shared-flags index 0) is enough for v1 entity rendering | Pitfall 1 | A required default-overriding metadata entry might be needed for some entities to render; capture-diff the target entity. |
| A4 | v1 inventories are mostly component-free (count+id+0+0), so the minimal `SlotData.WriteTo` suffices initially | Pattern 4 / Pitfall 2 | Items with components need the extended encoder; staged, low risk. |
| A5 | Vanilla entity track ranges (players 48, mobs ~80 blocks) — a single conservative range is acceptable for v1 | Pattern 1 | Wrong range = entities pop in/out at the wrong distance; cosmetic, tunable. |
| A6 | Server can decode-and-discard client `HashedStack` and re-send authoritative content for v1 | Pattern 4 / Pitfall 3 | If the client requires hash-acknowledgement to settle, may need to honor stateId; capture-diff a click round-trip. |

## Open Questions

1. **Exact `Vec3.LP_STREAM_CODEC` short scaling for AddEntity/SetEntityMotion movement**
   - What we know: it's low-precision (3×Short), confirmed via bytecode; `SetEntityMotion` historically used velocity×8000 clamped to short.
   - What's unclear: whether LP uses the same ×8000 scaling or a different factor at 776.
   - Recommendation: capture-diff a moving entity's AddEntity + SetEntityMotion bytes against vanilla.

2. **Whether any v1 entity requires non-default metadata to render at all**
   - What we know: most entities render from AddEntity alone; metadata adds state (pose, flags).
   - What's unclear: if `sulfur_cube` or a chosen test mob needs a metadata entry to appear.
   - Recommendation: spawn the test entity with the 0xFF-only metadata first; if it doesn't render, capture-diff vanilla's SetEntityData for that type.

3. **HashedStack decode depth needed for ContainerClick**
   - What we know: server is authoritative; `HashedPatchMap`/`HashedStack` present in jar.
   - What's unclear: minimum decode to avoid mis-framing while ignoring the hashes.
   - Recommendation: derive `HashedStack`/`HashedPatchMap.write`/read from the jar before implementing the decoder; capture-diff a real click.

4. **Physics scope for v1: full entity simulation vs. minimal**
   - What we know: ENT-02 requires gravity + AABB collision for entities AND players.
   - What's unclear: how faithful the constants must be for the visual gate (an entity falls and lands on the floor; a player can't clip through stone).
   - Recommendation: target the visible behaviors (land on ground, blocked by walls, no clip-through); defer exact-constant parity. Flag for the discuss/plan step.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | all build/test | ✓ | 1.26.1 | — |
| 26.2 server jar (javap source) | wire derivation / capture-diff | ✓ | `temp/cache/inner/META-INF/versions/26.2/server-26.2.jar` | — |
| `javap` (JDK 25) | jar bytecode derivation | ✓ | Zulu/Temurin 25 | — |
| Real vanilla 26.2 server | capture-diff (metadata, slots, respawn) | ✓ | `temp/vanilla-scratch/server.jar` | — |
| Docker (golang:1.26, CGO for `-race`) | race gate | ✓ | 29.4.3 | host `CGO_ENABLED=0`, race runs in Docker (Phase 2-5 precedent) |
| PrismLauncher 26.2 client | visual/interactive gate | ✓ (operator) | 26.2 | human-verify check (autonomous:false), as Phase 4/5 |

**Missing dependencies with no fallback:** None.
**Missing dependencies with fallback:** `-race` needs CGO; runs in golang:1.26 Docker (established Phase 2-5 pattern).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (no third-party) |
| Config file | none (standard `go test`) |
| Quick run command | `go test ./server/... ./world/... ./level/...` |
| Full suite command | `docker run --rm -v "$PWD":/src -w /src golang:1.26 go test -race ./server/... ./world/... ./level/... ./save/...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| ENT-01 | Tracker emits Add/Move/Remove per visibility diff | unit | `go test ./server/ -run TestTracker` | ❌ Wave 0 (`server/tracker_test.go`) |
| ENT-01 | AddEntity/SetEntityData bytes match vanilla | capture-diff | `go test ./server/ -run TestEntityBytesVsVanillaCapture` | ❌ Wave 0 |
| ENT-02 | Per-axis AABB sweep blocks clip-through; entity lands on ground | unit | `go test ./server/ -run TestPhysics` | ❌ Wave 0 (`server/physics_test.go`) |
| ENT-03 | SetBlock mutates chunk; BlockUpdate+Ack emitted | unit | `go test ./server/ ./world/ -run TestBlockInteract` | ❌ Wave 0 |
| ENT-04 | SlotData/ContainerSetContent round-trip + vanilla diff | unit+capture | `go test ./server/ ./level/component/ -run TestInventory` | ❌ Wave 0 |
| ENT-05 | SetHealth/Respawn encode; death→respawn flow | unit | `go test ./server/ -run TestCombat` | ❌ Wave 0 (`server/combat_test.go`) |
| ENT-06 | Player/entity NBT save→load round-trip | unit | `go test ./server/ ./save/ -run TestPersistence` | ❌ Wave 0 |
| ENT-01..05 | Real client: spawn an entity, place/break a block, take damage | manual | human-verify (PrismLauncher), autonomous:false | gate |

### Sampling Rate
- **Per task commit:** `go test ./server/... ./world/... ./level/...` (host)
- **Per wave merge:** full `-race` suite in golang:1.26 Docker
- **Phase gate:** full suite green + capture-diff fixtures committed + the real-client interactive check (place a block and see it; take damage and see the health bar) — a BLOCKING human-verify like Phases 4/5.

### Wave 0 Gaps
- [ ] `server/entity_store_test.go` — entity store + grid bucketing + ID allocator
- [ ] `server/tracker_test.go` — visibility diff → Add/Move/Remove (ENT-01)
- [ ] `server/physics_test.go` — per-axis sweep, gravity, step, no-clip (ENT-02)
- [ ] `server/block_interact_test.go` + `world/manager` block-edit tests (ENT-03)
- [ ] `server/inventory_test.go` + `level/component` extended-encoder tests (ENT-04)
- [ ] `server/combat_test.go` — health/death/respawn (ENT-05)
- [ ] `server/persistence_test.go` + `save` round-trip (ENT-06)
- [ ] Capture-diff fixtures: `06-CAPTURE-DIFF.md` for SetEntityData, ItemStack/ContainerSetContent, ContainerClick (HashedStack), AddEntity, Respawn

## Security Domain

> `security_enforcement` not set to false in config — section included. The "user" is an unmodified vanilla client, but the server must remain robust against a MALICIOUS/malformed client (the standing project threat model; net goroutines never touch game state).

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Offline mode for v1 (auth is v2/Phase 9). |
| V3 Session Management | partial | One writer goroutine / channel boundary already enforced (NET-05); Phase 6 adds no new session surface. |
| V4 Access Control | yes | Server is AUTHORITATIVE for block edits, inventory, health — never trust client-claimed item/health/edit; validate every action on-tick. |
| V5 Input Validation | yes | Every serverbound decode (`PlayerAction`, `UseItemOn`, `ContainerClick`) must `Scan`-error to a no-op, never panic (the established `dispatch` contract, T-3-02). |
| V6 Cryptography | no | None at this layer (encryption is v2/Phase 9). |

### Known Threat Patterns for Sulfur (Go MC server, malicious client)
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Out-of-range / cross-chunk block edit (place where the player can't reach) | Tampering | Server validates reach distance + target chunk is loaded before applying; reject silently. |
| Forged inventory click (claim an item you don't have via HashedStack) | Spoofing/Tampering | Server-authoritative inventory; re-send `ContainerSetContent`; ignore client item data. |
| Malformed `ContainerClick`/`PlayerAction` (mis-framed payload) | DoS | `Scan` error → no-op, never panic (T-3-02); decode HashedStack form correctly (Pitfall 3). |
| Movement/edit flood to grow per-player buffers | DoS | The bounded subtick buffer (cap 256, drop-oldest) already caps this (subtick.go); block edits resolve on-tick, no unbounded queue. |
| Self-claimed health / "I'm not dead" | Tampering | Health is server-owned; client only requests respawn via `ClientCommand`. |
| Tunneling fast self-moves through walls | Tampering | Server collides client-sent positions per-axis (Pattern 2); reject clip-through. |

## Sources

### Primary (HIGH confidence)
- On-disk fork code (read this session): `server/tick.go`, `server/tick_phases.go`, `server/subtick.go`, `server/entity.go`, `server/play_join.go`, `server/world_stream.go`, `world/manager.go`, `level/chunk.go`, `level/palette.go`, `level/block/block.go`+`utilfuncs.go`, `level/component/components.go`+`types.go`, `data/entity/entity.go`, `data/packetid/packetid.go`, `save/playerdata.go`+`chunk.go`, `save/region/mca.go`, `server/internal/bvh/bound.go`+`bvh.go`, `net/packet/types.go`.
- Jar bytecode (`javap -p -c`, `temp/cache/inner/.../server-26.2.jar`, unobfuscated 26.2): `ClientboundAddEntityPacket.write`, `ClientboundSetEntityDataPacket.pack` + `SynchedEntityData$DataValue.write`, `ClientboundMoveEntityPacket`, `ClientboundRespawnPacket.write` + `CommonPlayerSpawnInfo.write`, `ServerboundPlayerActionPacket`, `ServerboundUseItemOnPacket`, `ServerboundContainerClickPacket`, `ClientboundContainerSetContentPacket`, `ClientboundBlockChangedAckPacket`, `ClientboundSetHealthPacket`, `ItemStack` (OPTIONAL/STREAM_CODEC), `HashedPatchMap`/`DataComponentPatch` presence.
- Prior capture-diff artifacts: `04-WORLD-CAPTURE-DIFF.md`, `05-CAPTURE-DIFF.md` (the proven method; the `commonPlayerSpawnInfoEncoder` was sealed here).

### Secondary (MEDIUM confidence)
- Training knowledge of vanilla physics constants (gravity/drag/friction/step) and entity track ranges — flagged `[ASSUMED]` (A1, A5), verify against jar if exactness matters.
- Training knowledge of the 1.20.5 component-slot and 1.21.5 HashedStack transitions — corroborated by jar class presence (`HashedPatchMap`, `DataComponentPatch`, ItemStack codecs).

### Tertiary (LOW confidence)
- None relied upon. The ≤773 community wiki is explicitly NOT a source for 776 wire layout (STATE.md standing blocker).

## Metadata

**Confidence breakdown:**
- Standard stack (fork APIs reused): HIGH — every package/type/method read on disk this session.
- Architecture (synchronous tracker behind the seam, tick-owned physics/inventory): HIGH — the seams (`tracker.Tick()`, `tickEntities`/`tickPhysics`, `applyAsyncResults`) read directly; the synchronous-not-async constraint is explicit in the code comments.
- Wire formats: HIGH for AddEntity, SetEntityData framing, Respawn, SetHealth, PlayerAction/UseItemOn fields, ContainerClick/SetContent fields (all jar-derived). MEDIUM for the LP movement short-scaling, ItemStack component value codecs, and HashedStack body — flagged for capture-diff.
- Physics constants: MEDIUM — training-derived, flagged `[ASSUMED]`; do not affect wire framing.
- Pitfalls: HIGH — each tied to a verified jar/fork fact.

**Research date:** 2026-06-24
**Valid until:** 30 days (the fork is pinned; only a 26.x bump or a deeper jar finding would invalidate). The MEDIUM wire items should be sealed by capture-diff during execution, not re-researched.
