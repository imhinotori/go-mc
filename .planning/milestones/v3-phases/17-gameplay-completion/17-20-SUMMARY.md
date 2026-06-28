---
phase: 17
plan: 20
subsystem: inventory / container-menu
tags: [inventory, container-click, ENT-04, gameplay-1to1, AbstractContainerMenu]
requires:
  - server/inventory.go (Inventory model, sendContent, broadcastInventoryChanges, containerSetSlot)
  - server/slot_encode.go (HashedStack decode, containerSetContent/SetSlot encoders)
  - server/item_entity.go (maxStackSize, addResource, window-slot constants, NewItemEntity)
provides:
  - AbstractContainerMenu.clicked/doClick 1:1 for the player inventory window (containerId 0)
  - cursor (carried) item state + ClientboundContainerSetSlot(-1,-1) carried sync
  - QUICK_MOVE (shift-click) via InventoryMenu.quickMoveStack + moveItemStackTo
  - SWAP / CLONE / THROW / PICKUP_ALL / QUICK_CRAFT drag-distribute
affects:
  - server/inventory.go
  - server/inventory_click.go (new)
  - server/inventory_doclick.go (new)
  - server/inventory_click_test.go (new)
  - server/inventory_test.go
tech-stack:
  added: []
  patterns:
    - "menuSlot abstraction over the flat []component.SlotData backing the 46-slot InventoryMenu window"
    - "in-place ItemStack-equivalent mutation (split/grow/shrink) tick-owned, no parallel state"
key-files:
  created:
    - server/inventory_click.go
    - server/inventory_doclick.go
    - server/inventory_click_test.go
  modified:
    - server/inventory.go
    - server/inventory_test.go
decisions:
  - "Modelled net.minecraft.world.inventory.Slot as a menuSlot value (inv + index) over Sulfur's flat slot array, rather than a Slot+Container object graph, preserving the exact Slot primitive semantics."
  - "carried (cursor) item added to the Inventory struct; sendContent and the click resolver read/write it; the carried sync uses ClientboundContainerSetSlot(-1, stateId, -1, carried)."
  - "TestContainerClickAuthoritative updated: the new clicked() syncs via per-slot SetSlot diffs (vanilla broadcastChanges) instead of always re-sending ContainerSetContent (the old stub); the authoritative discard invariant is preserved and now asserted against a real server-side stack."
metrics:
  duration: ~1h
  completed: 2026-06-26
---

# Phase 17 Plan 20: AbstractContainerMenu.clicked Inventory Click 1:1 Summary

Replaced Sulfur's stub container-click handler (which decoded the packet and only re-sent content) with a literal 1:1 port of `AbstractContainerMenu.clicked` → `doClick` for the player's survival inventory window (InventoryMenu, containerId 0). On a real client, pickup/deposit on the cursor, shift-click (QUICK_MOVE), hotbar swap (SWAP), drop (THROW/Q), double-click collect-all (PICKUP_ALL), drag-distribute (QUICK_CRAFT), and creative CLONE now all resolve server-authoritatively, with changed slots and the carried item synced back.

## What was built

### Cursor (carried) state + sync — `server/inventory.go`
- Added `carried component.SlotData` plus the QUICK_CRAFT drag fields (`quickcraftStatus`, `quickcraftType`, `quickcraftSlots []int`) to `Inventory`, with `getCarried`/`setCarried`/`resetQuickCraft`/`addQuickcraftSlot`.
- `sendContent` now carries the real cursor item (was hardcoded `Count:0`).
- Rewrote `handleContainerClick` to decode (keeping the HashedStack decode, discarding hashes — T-6-02) then call the new `clicked(...)`.
- `clicked(...)` ports `AbstractContainerMenu.clicked`: window-0-only guard (other menus resend authoritative content), snapshot slots+carried, run `doClick` inside a panic-recover (the vanilla try/CrashReport equivalent — on a panic, restore the pre-click snapshot and resend, T-6-04), then `broadcastInventoryChanges` (per-slot SetSlot diffs) and the carried sync `containerSetSlot(-1, stateId, -1, carried)` (`synchronizeCarriedToRemote`).

### Slot primitives + click engine — `server/inventory_click.go` (new)
Ported `net.minecraft.world.inventory.Slot` over a `menuSlot` (inv + index):
`getItem`, `hasItem`, `setItem`/`setByPlayer`, `mayPickup`, `mayPlace`, `getMaxStackSize()`/`getMaxStackSize(stack)`, `remove` (→ `Container.removeItem` → `ContainerHelper.removeItem`), `tryRemove`, `allowModification`, `safeTake`, `safeClone`, `safeInsert`, `onTake`/`onQuickCraft`/`onSwapCraft`.
ItemStack-equivalent helpers: `stackEmpty`, `stackMaxSize` (→ `ItemStack.getMaxStackSize` via the existing `maxStackSize`), `stackIsStackable`, `stackCopyWithCount`, `stackSplit`, `stackSameItem`, `stackSameItemSameComponents`.
QUICK_CRAFT helpers: `getQuickcraftType`, `getQuickcraftHeader`, `isValidQuickcraftType`, `canItemQuickReplace`, `getQuickCraftPlaceCount`, `canDragTo`.
SWAP index mapping: `invGetItem`/`invSetItem`/`invMenuIndex` (Inventory index space: hotbar 0-8 → menu 36-44, offhand 40 → menu 45), and `invAdd` (→ `Inventory.add` via `addResource`).
Shift-click engine: `moveItemStackTo` (two-pass merge-then-fill, reverse walk, in-place shrink) and `quickMoveStack` (the InventoryMenu survival routing: result 0 → 9..45 reverse; craft/armor 1-8 → 9..45; main 9-36 → hotbar 36-45; hotbar 36-45 → main 9-36; fallback 9..45).

### doClick dispatcher — `server/inventory_doclick.go` (new)
`doClick` dispatches the 7 `ContainerInput` ordinals (verified `$VALUES`: PICKUP=0, QUICK_MOVE=1, SWAP=2, CLONE=3, THROW=4, QUICK_CRAFT=5, PICKUP_ALL=6):
- `doClickQuickCraft` — the START/ADD/END drag state machine, including the single-slot drag → recursive PICKUP and the even/single/clone distribution with `getQuickCraftPlaceCount`.
- `doClickPickupOrQuickMove` — outside-window drop (i==-999, PRIMARY drops all / SECONDARY splits one), QUICK_MOVE (loop on `quickMoveStack` while same-item), and the PICKUP matrix (empty-slot deposit via `safeInsert`, take via `tryRemove`+`onTake`, same-item merge via `safeInsert`, swap, and merge-slot-into-carried via `tryRemove`+`grow`).
- `doClickSwap` — the 4-way empty/non-empty matrix with split-if-over-max and the inventory.add/drop fallback.
- `doClickClone` — creative-only clone to a max stack onto an empty cursor (`safeClone`).
- `doClickThrow` — drop from the slot (j==0 one / j==1 drain same-item via `safeTake`).
- `doClickPickupAll` — the two-pass double-click collect into the cursor up to max.
- `playerDrop` ports `Player.drop` reusing `NewItemEntity` + `entities.add`; `playerCanDropItems`/`handleCreativeModeItemDrop`/`canTakeItemForPickAll` are the cited bases.

## Ported methods (all verified against temp/cache/26.2-inner.jar this session)
- `AbstractContainerMenu.clicked` / `doClick` (all 7 inputs) / `moveItemStackTo` / `canItemQuickReplace` / `getQuickCraftPlaceCount` / `getQuickcraftType` / `getQuickcraftHeader` / `isValidQuickcraftType` / `resetQuickCraft` / `canDragTo` / `synchronizeCarriedToRemote`
- `InventoryMenu.quickMoveStack` / `canTakeItemForPickAll`
- `Slot.safeInsert` / `safeTake` / `tryRemove` / `remove` / `allowModification` / `setByPlayer` / `set` / `getItem` / `hasItem` / `mayPickup` / `mayPlace` / `getMaxStackSize` / `safeClone` / `onTake` / `onQuickCraft` / `onSwapCraft`
- `ContainerHelper.removeItem`; `Inventory.removeItem` / `add` / `getItem` index layout
- `ItemStack.split` / `grow` / `shrink` / `copyWithCount` / `setCount` / `getMaxStackSize` / `isSameItem` / `isSameItemSameComponents`
- `Player.drop` / `canDropItems` / `handleCreativeModeItemDrop` / `hasInfiniteMaterials`
- `ContainerInput` enum ordinal order (`$VALUES`)

## Deviations from Plan

None functionally — plan executed as written. One test was updated to match the corrected (non-stub) sync behavior:
- **[Rule 1 - Bug] `TestContainerClickAuthoritative` asserted the OLD stub** (server always re-sends `ContainerSetContent`). The faithful `clicked()` syncs via per-slot `ClientboundContainerSetSlot` diffs (vanilla `broadcastChanges`). Updated the test to seed a real server-side stack, drive a PICKUP, and assert: the forged client item 999 never lands (the authoritative-discard invariant, preserved), a `SetSlot` is emitted (authoritative sync), and the real stack moved to the cursor. Commit: see plan commit.

## Cited stubs (each equals the vanilla default, structured to become a real read later)
- `equipmentSlotForItem` → `Player.getEquipmentSlotForItem`: returns MAINHAND (non-armor/non-offhand), so normal shift-clicks route main↔hotbar; armor/offhand auto-equip branches stay dormant until an equipment classifier lands.
- `isArmorForSlot` → `ArmorSlot.mayPlace`: default true (any item placeable into an armor slot 5-8); slot 0 (craft result) `mayPlace` is still hard false.
- `menuSlot.onTake` / `onQuickCraft` / `onSwapCraft` → base `Slot` hooks: no-op (`ResultSlot.onTake` crafting consumption out of v1 scope; no recipes wired).
- `tryItemClickBehaviourOverride` → bundle/feature-flag items: treated as returning false (out of v1 scope).
- `playerCanDropItems` → `Player.canDropItems`: true (base).
- `handleCreativeModeItemDrop` → `Player.handleCreativeModeItemDrop`: no-op (base).
- `canTakeItemForPickAll` → base: true (no crafting result container).
- `playerDrop` reuses `NewItemEntity`'s random toss + eye-height spawn rather than the full `Player.drop` look-vector throw kinematics; the observable (a dropped item entity appears) is faithful, the exact toss vector is a refinement structured to drop in without touching callers.

## Verification
- `go build ./...` — exit 0.
- `go vet ./server/` — clean.
- `go test ./server/` — green except the known RNG/timing flake `TestTickAIDrivesMobs` (passes on retry, 5/5; unrelated to inventory — a second full-suite run is fully green). All other packages pass.
- New deterministic tests (`server/inventory_click_test.go`, 9 cases, all pass): PICKUP empty→carried→deposit; PICKUP secondary half + place-one; QUICK_MOVE main→hotbar (merge then fill, exact 64/26); SWAP to-empty + exchange; THROW j=0 single & j=1 drain; PICKUP_ALL collect to max (exact 64 with ordered drain); QUICK_CRAFT type-0 even split across 3 slots (3/3/3, cursor 0); CLONE creative-only (no-op survival, 64 creative, source unchanged); carried sync emits `SetSlot(-1, -1)`.
- Race detector unavailable in this environment (no cgo/gcc); the new code is strictly tick-owned (all mutation via the existing `applyInput`→`doClick` path), race-clean by the same single-owner discipline as the rest of the inventory.
- Mob-AI files untouched.

## Known Stubs
The cited stubs above are intentional and equal the vanilla default. None block the plan goal: the survival inventory click loop (pickup/deposit/shift-click/swap/throw/collect-all/drag/clone) works end-to-end for the player window. Armor/offhand auto-equip and crafting-result consumption are the only deferred behaviors, gated behind clearly-cited stubs that a future plan replaces with real equipment/recipe data.

## Self-Check: PASSED
- server/inventory_click.go — FOUND
- server/inventory_doclick.go — FOUND
- server/inventory_click_test.go — FOUND
- server/inventory.go (modified: carried + clicked) — FOUND
- server/inventory_test.go (updated authoritative test) — FOUND
- Commit recorded below.
