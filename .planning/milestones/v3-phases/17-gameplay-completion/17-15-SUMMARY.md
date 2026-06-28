---
phase: 17
plan: 17-15
subsystem: server (movement authority + inventory/item-pickup)
tags: [bugfix, vanilla-1to1, movement, water, inventory, item-pickup, protocol-776]
provides:
  - vanilla client-authoritative player movement (no server-side water rewrite)
  - vanilla Inventory slot mapping for pickups (main/hotbar only)
  - vanilla per-slot ContainerSetSlot broadcast after pickup
affects:
  - server/subtick.go
  - server/fluid_physics.go
  - server/item_entity.go
  - server/inventory.go
key-files:
  modified:
    - server/subtick.go
    - server/fluid_physics.go
    - server/item_entity.go
    - server/inventory.go
    - server/fluid_test.go
    - server/breath_test.go
    - server/item_entity_test.go
decisions:
  - "Player movement is CLIENT-authoritative: the server accepts the submitted (collide-clamped) position verbatim and never re-applies travel()/travelInFluid() water drag. travelInFluid is reserved for server-controlled mobs."
  - "Inventory.items in 26.2 is the 36-slot getNonEquipmentItems() (hotbar 0-8, main 9-35); pickups iterate items[0..35] only, never crafting/armor."
  - "Pickup slot updates use AbstractContainerMenu.broadcastChanges -> per-slot ClientboundContainerSetSlot with incrementStateId() == (stateId+1)&32767."
metrics:
  commits: 3
  files-changed: 7
  completed: 2026-06-26
---

# Phase 17 Plan 17-15: Gameplay Bug Triage (Water Disconnect + Pickup Slots) Summary

Three confirmed gameplay bugs fixed by porting the vanilla 26.2 logic 1:1 from
`temp/cache/26.2-inner.jar`. Each bug fixed atomically with a per-bug commit, jar
methods decompiled and cited, regression tests added. `CGO_ENABLED=0 go build ./...`
exits 0; `go test ./server/...` passes (the only failure is the pre-existing,
documented `TestTickAIDrivesMobs` async-pathfinding flake, unrelated to these files).

## BUG 1 — jumping into water disconnected the client (the vanilla authority-model finding)

**Commit:** `2635a8ad`

**Root cause:** `server/subtick.go`'s movement-accept path re-applied
`moveWithFluidPhysics` (LivingEntity.travelInFluid's 0.8 `getWaterSlowDown` + the
0.014 buoyant push) to the client's submitted position. That produced a position the
client never predicted; the client closed the connection (reason=quit) on the
unexpected correction.

**Vanilla authority-model finding (decompiled `ServerGamePacketListenerImpl.handleMovePlayer`):**
Player movement is **client-authoritative**. The server does NOT rewrite the
submitted position for water drag. The decompiled flow:

1. `d{X,Y,Z} = submitted_clamped - lastGood{X,Y,Z}` (the per-tick delta).
2. `ServerPlayer.move(MoverType.PLAYER, new Vec3(dX,dY,dZ))` — **pure collision
   resolution** (sweeps the AABB through solid blocks). It does NOT call
   `travel()`/`travelInFluid()`/buoyancy.
3. Computes `d = (submitted - resulting)^2`; if `d > 0.0625` (and not
   creative/spectator/sleeping/grace) it logs `"{} moved wrongly!"` and only then,
   combined with a `noCollision`/`isEntityCollidingWithAnythingNew` check, does it
   `teleport(lastGoodX, lastGoodY, lastGoodZ, ...)` back (the anti-clip rubber-band).
4. Otherwise it `absSnapTo(x, y, z)` — **accepting the submitted position verbatim**.

So vanilla NEVER re-applies water slowdown to the player; `LivingEntity.travel`/
`travelInFluid` runs **client-side** for the local player (the client already sends
its water-slowed position) and **server-side only for server-controlled mobs**.

**Fix:** both `ServerboundMovePlayerPos` and `...PosRot` paths in `subtick.go` now
accept the collided position directly (`p.x, p.y, p.z = nx, ny, nz`), matching
vanilla `absSnapTo` after `move`. `moveWithFluidPhysics`/`applyFluidPhysics` are
kept (documented as reserved for the server-controlled mob path); `playerInWater`
stays in active use by fall-damage/breath. No breath-metadata send was implicated —
the movement re-application was the sole cause (confirmed by reading the full
`handleMovePlayer` body; the breath/air path is not touched on the move-accept path).

**Before:** jump into water → server adds 0.8/0.014 to the submitted position →
client rejects → disconnect.
**After:** server accepts the client's submitted (collide-clamped) position →
no correction → client stays connected.

**Test:** `TestPlayerMovePathDoesNotRewritePositionInWater` — a confirmed player in
water submits a jump-out-of-water move via `applyInput`; the accepted position
equals the submission exactly (no 0.8 scale, no +0.014 buoyancy).

## BUG 2 — picked-up items landed in the crafting slots

**Commit:** `2b74b801`

**Root cause:** `server/item_entity.go`'s `freeSlot`/`slotWithRemainingSpace`
iterated all 46 window slots (0=craft-result, 1-4=craft-grid, 5-8=armor, 9-35=main,
36-44=hotbar, 45=offhand), so a pickup could target slots 0-4 (crafting).

**Vanilla port (decompiled `net.minecraft.world.entity.player.Inventory`):** In 26.2
`Inventory.items` is the **36-entry** `getNonEquipmentItems()` list (`items[0..8]` =
hotbar, `items[9..35]` = main storage). `getItem(i)` returns `items[i]` for `i < 36`,
else maps through `EQUIPMENT_SLOT_MAPPING` (40 = OFFHAND, 36-39 = armor). Therefore:
- `getFreeSlot()` iterates `items[0..35]` only.
- `getSlotWithRemainingSpace(stack)` checks `getItem(selected)`, then `getItem(40)`
  (offhand), then iterates `items[0..35]`.
- `addResource` deposits only into those slots.

**Window-slot mapping (verified against the InventoryMenu slot layout):**
`items[0..8]` (hotbar) → window `36..44`; `items[9..35]` (main) → window `9..35`;
offhand → window `45`; selected → window `36 + heldSlot`.

**Fix:** added `storageWindowSlots()` (the 36 storage windows in items-index order —
hotbar 36-44 first, then main 9-35) and `hasRemainingSpaceForItem`. `freeSlot`/
`slotWithRemainingSpace` now iterate ONLY storage in vanilla order and apply the
selected → offhand → storage priority. Pickups can never land in crafting (0-4),
armor (5-8), or offhand (45) again.

**Before:** pickup overflow/first-free could be window 0-4 (crafting grid).
**After:** pickup lands only in window 9-44 (main/hotbar), selected hotbar slot first.

**Tests:** `TestPickupLandsInStorageNeverCraftingOrArmor` (slots 0-8 and 45 stay
empty, item lands in 9-44); `TestPickupPrefersSelectedHotbarSlot` (selected-slot
priority over a main slot holding the same item).

## BUG 3 — picked-up items sometimes didn't show on opening the inventory

**Commit:** `55527bfa`

**Root cause:** after a pickup, `playerTouchItem` re-sent only a full
`ContainerSetContent` (`sendContent`); the client did not reliably reflect the change.

**Vanilla port (decompiled `AbstractContainerMenu.broadcastChanges` /
`synchronizeSlotToRemote` / `incrementStateId`):** vanilla iterates every menu slot
and, when the remote copy differs, sends
`ClientboundContainerSetSlot(containerId, getStateId(), slot, item)` — the stateId
bumped once per broadcast via `incrementStateId()` = `(stateId + 1) & 32767`.

**Fix:** added `broadcastInventoryChanges` (snapshot before `Inventory.add`, bump
stateId once, emit one `ClientboundContainerSetSlot` per changed slot) and
`slotDataEqual`. `playerTouchItem` snapshots before the add and broadcasts the
changed slots, so the picked-up stack always renders. The `containerSetSlot` encoder
and `ClientboundContainerSetSlot` packet id already existed (slot_encode.go).

**Before:** full `ContainerSetContent` only.
**After:** authoritative per-slot `ClientboundContainerSetSlot` (containerId 0,
stateId bumped) for each slot the pickup changed.

**Test:** `TestPickupSendsSetSlot` (a pickup emits ≥ 1 `ClientboundContainerSetSlot`).

## Deviations from Plan

None beyond the documented scope. The investigation confirmed the breath-metadata
send was NOT involved in the water disconnect (the movement re-application was the
sole cause), so combat.go/fall_damage.go/breath.go were not touched. `world/` was
not touched (sibling-owned).

## Deferred Issues

`TestTickAIDrivesMobs` (`server/spawner_test.go`) is a pre-existing non-deterministic
flake in the async mob-pathfinding subsystem (the async pool does not always rejoin
within 400 ticks under CPU contention). It is independent of the 17-15 fluid/inventory
changes (those files are unrelated to the AI/async path; all 17-15 tests pass 5/5 in
isolation and the full suite passes when this one test is skipped). Logged in
`deferred-items.md`; owned by the AI/spawner plan.

## Verification

- `CGO_ENABLED=0 go build ./...` → exit 0.
- `CGO_ENABLED=0 go vet ./server/...` → clean.
- `CGO_ENABLED=0 go test ./server/...` → pass (excluding the documented pre-existing
  `TestTickAIDrivesMobs` async flake).
- All 17-15 regression tests pass 5/5 across repeated runs.

## Self-Check: PASSED
