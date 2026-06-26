---
phase: 17
plan: 17
subsystem: gameplay / block-placement / inventory
tags: [gameplay, block-placement, blockitem, useitemon, inventory, survival-shrink, bugfix, 1to1-port]
requires: [handleUseItemOn wire decode (ENT-03), tick-owned Inventory + broadcastInventoryChanges (ENT-04 / 17-15), block.FromID/ToStateID + item.ByID generated data, world.ChunkManager.SetBlock/GetBlock]
provides: [held-item-driven block placement, empty-hand no-op (bugfix), survival stack shrink on place, BlockPlaceContext replaceable/replace-clicked geometry, useBlockInteraction PASS hook]
affects: [server/block_interact.go, server/block_place.go, server/block_interact_test.go]
key-files:
  modified:
    - server/block_interact.go
    - server/block_interact_test.go
  created:
    - server/block_place.go
decisions:
  - "Replace the hardcoded `block.ToStateID[block.Stone{}]` v1 stand-in with the held item's block, ported 1:1 from ServerPlayerGameMode.useItemOn -> BlockItem.useOn -> BlockItem.place -> stack.consume(1). An EMPTY main hand (ItemStack.isEmpty) resolves to no block and places nothing — the empty-hand-stone bugfix."
  - "Reproduce Block.byItem(stack.getItem()) -> BlockItem.getBlock() via the SHARED resource name: block.FromID[\"minecraft:\"+item.Name]. A hit means the item is a BlockItem bound to that block; a miss (sword/food/air) means Block.byItem returns Blocks.AIR -> no placement. block.ToStateID of the resolved zero-value block is its defaultBlockState id (Block.getStateForPlacement default; facing is the cited future extension point)."
  - "Port BlockPlaceContext geometry faithfully: replaceClicked = canBeReplaced(clicked block) -> getClickedPos is the hit pos (replace-in-place) else hit+normal; canPlace() requires the target be replaceable (air/water/lava). v1's replaceable set is the BlockBehaviour.replaceable subset Sulfur's world produces (Air/CaveAir/VoidAir/Water/Lava). The self-replace guard (held item == target block) is honored."
  - "Survival shrinks the held stack by 1 via ItemStack.consume(1, player) (BlockItem.place tail) and sends the changed slot through the existing broadcastInventoryChanges diff. Creative does NOT shrink: ItemStack.consume short-circuits on hasInfiniteMaterials() — mirrored by gating the shrink on gameMode != gameModeCreative."
metrics:
  completed: 2026-06-26
---

# Phase 17 Plan 17: Held-Item Block Placement (useItemOn -> BlockItem.place 1:1 Port) Summary

Fixed a confirmed GAMEPLAY bug: right-clicking a block placed a STONE block even with an
EMPTY hand. Root cause was a documented v1 stand-in at `server/block_interact.go`
`handleUseItemOn` — `placeState := block.ToStateID[block.Stone{}]` — that ignored the held
item entirely. Replaced it with a method-for-method port of the vanilla right-click-on-block
placement flow so the placed block comes FROM the held item, an empty hand places nothing,
the target must be replaceable, and the survival stack shrinks by one.

## The 1:1 Port (decompiled from `temp/cache/26.2-inner.jar` via `javap -c -p`)

### `ServerPlayerGameMode.useItemOn(ServerPlayer, Level, ItemStack, InteractionHand, BlockHitResult)`

The right-click flow, faithfully mirrored in `handleUseItemOn`:

1. **Block's own interaction first** (when not secondary-use). Bytecode 131–148:
   `blockState.useItemOn(...)`; if `consumesAction()`, return — NO block placed. v1 has no
   interactive blocks, so this is the `useBlockInteraction` hook that always returns `false`
   (`InteractionResult.PASS`), structured faithfully for a later chest/lever/door plan.
2. **Empty / cooldown short-circuit** (bytecode 223–244): `if (stack.isEmpty() || cooldown) return PASS;`
3. **`UseOnContext` + creative count guard** (bytecode 245–297): if `hasInfiniteMaterials()`,
   save `getCount()`, call `useOn`, then `setCount(saved)` — creative never loses the item.
   Otherwise plain `stack.useOn(ctx)`.

### `BlockItem.useOn(ctx)` -> `place(new BlockPlaceContext(ctx))`

`useOn` (bytecode 0–9) wraps the `UseOnContext` in a `BlockPlaceContext` and calls `place`.
`place`:
- `if (!ctx.canPlace()) return FAIL;` (bytecode 21–31) — target must be replaceable.
- `state = getPlacementState(ctx)` (bytecode 46–59) -> `Block.getStateForPlacement(ctx)`;
  for a plain block this is `defaultBlockState()`. **Cited extension point** for orientation/
  facing (stairs, logs, doors) in a later plan.
- `placeBlock(ctx, state)` (bytecode 60–66) -> `Level.setBlock(getClickedPos(), state, 11)`.
- tail (bytecode 260–271): `stack.consume(1, player); return SUCCESS;`

### `BlockPlaceContext` geometry

`BlockPlaceContext.<init>` (bytecode 33–47): `replaceClicked = level.getBlockState(hitPos).canBeReplaced(this)`.
- `getClickedPos()` = `replaceClicked ? hitPos : hitPos.relative(face)` — replace-in-place vs adjacent.
- `canPlace()` = `replaceClicked || getBlockState(getClickedPos()).canBeReplaced(this)`.

### `BlockBehaviour.canBeReplaced(state, ctx)`

Bytecode 0–36: `state.canBeReplaced()` (the per-state `replaceable` material flag) **AND**
(held item empty **OR** held item is NOT the same item as this block) — the self-replace guard.

### `ItemStack.consume(int, LivingEntity)`

Bytecode 0–16: `if (entity == null || !entity.hasInfiniteMaterials()) shrink(count);` — the
shrink only happens in survival. The creative guard lives BOTH here and in
`ServerPlayerGameMode`'s save/restore; both collapse to "shrink in survival, keep in creative".

### `Block.byItem(Item)`

Bytecode: `if (item instanceof BlockItem bi) return bi.getBlock(); else return Blocks.AIR;` —
the item->block resolution, reproduced in Sulfur via the shared `"minecraft:<name>"` id.

## Implementation

- **`server/block_place.go` (new):**
  - `blockStateForItem(SlotData) (StateID, bool)` — the `Block.byItem -> getPlacementState(default)`
    port. Empty stack (`Count <= 0`) -> `false` (the empty-hand fix); `item.ByID[ItemID].Name`
    -> `block.FromID["minecraft:"+Name]` -> `block.ToStateID[...]`. A non-block item (sword,
    food, air) misses `FromID` -> `false` (Block.byItem -> Blocks.AIR).
  - `isReplaceableState(StateID) bool` — the `BlockState.canBeReplaced()` material flag for the
    replaceable set Sulfur's world produces: `Air`, `CaveAir`, `VoidAir`, `Water`, `Lava`.
- **`server/block_interact.go`:**
  - `handleUseItemOn` rewritten: read the held main-hand item from window slot `36+heldSlot`,
    resolve its block (empty/non-block -> return), compute `replaceClicked` + `getClickedPos`
    geometry, gate on `canPlace()` (adjacent target must be replaceable, with the self-replace
    guard), `SetBlock` + `reconcileEdit`, then shrink in survival.
  - `useBlockInteraction` — faithful PASS hook for the block's own interaction (always `false`).
  - `heldWindowSlot(heldSlot)` — hotbar index (0..8) -> window slot `36+heldSlot`.
  - `shrinkHeldItem` — `ItemStack.consume(1)` survival path: decrement, empty at 0, broadcast the
    changed slot via the existing `broadcastInventoryChanges` diff (`ClientboundContainerSetSlot`).

## Before / After

| Scenario | Before (v1 stand-in) | After (1:1 port) |
|----------|----------------------|------------------|
| Empty hand right-click | places STONE (the bug) | places NOTHING (PASS) |
| Holding stone, click solid | places stone (always) | places stone on adjacent face |
| Holding stone, survival | no shrink | stack shrinks by 1, slot synced |
| Holding stone, creative | no shrink | placed, NO shrink |
| Holding a sword | places STONE | places NOTHING (non-BlockItem) |
| Click into solid target | overwrote / placed stone | no-op (canPlace false) |
| Click a water block | placed on adjacent face | REPLACES the water in place |

## Tests (`server/block_interact_test.go`)

- `TestPlaceBlock` — holding stone places stone on the adjacent face (+ack +update).
- `TestPlaceEmptyHandNoBlock` — empty hand places nothing, no ack (the bugfix).
- `TestPlaceSurvivalShrinksStack` — survival shrinks 3->2 and sends `ContainerSetSlot`.
- `TestPlaceSurvivalLastItemEmptiesSlot` — last item placed empties the slot.
- `TestPlaceCreativeNoShrink` — creative places but keeps the full stack.
- `TestPlaceIntoSolidNoOp` — adjacent solid target -> no placement, no ack.
- `TestPlaceNonBlockItemNoOp` — a wooden sword places nothing.
- `TestPlaceReplaceClicked` — clicking replaceable water replaces it in place.

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go test ./server/...` — pass (all 8 place tests + existing break/reconcile).
- `go vet ./server/...` — clean.
- Race detector (`-race`) could not run in this environment (no C compiler; CGO unavailable).
  All new code is tick-owned: `handleUseItemOn` runs on the tick goroutine, mutates only the
  tick-owned `Inventory` and `world.ChunkManager`, and all sends go through the bounded outbound
  queue — consistent with the existing TICK-05 concurrency contract.

## Scope Notes (faithful deferrals)

- `Block.getStateForPlacement` returns the default state for v1 (full-cube blocks); orientation/
  facing for stateful blocks (stairs, logs, doors) is the cited extension point in `block_place.go`.
- The `replaceable` set is the BlockBehaviour subset Sulfur's world produces (air variants +
  water + lava); other vanilla replaceables (short grass, ferns, snow_layer, fire) are not in the
  v1 world, so they are intentionally outside the set until those blocks exist.
- `useBlockInteraction` is a PASS hook (no interactive blocks in v1); it is structured so a later
  chest/lever/door plan drops in real dispatch without touching `handleUseItemOn`.

## Self-Check: PASSED

- `server/block_place.go` — FOUND (created).
- `server/block_interact.go` — FOUND (modified: hardcoded stone replaced).
- `server/block_interact_test.go` — FOUND (modified: 8 place tests).
- `go build ./...` exit 0, `go test ./server/...` pass.
