---
phase: 20-structure-polish-loot-inhabitants-beard-persistence
plan: 06
subsystem: inventory-containers
tags: [chest-open, container-menu, block-entity, loot, open-screen, generic-9x3, vanilla-parity, struct-polish-01]

# Dependency graph
requires:
  - phase: 20-structure-polish-loot-inhabitants-beard-persistence
    plan: 02
    provides: "server/chest_loot.go chestLoot.unpackLootTable (the lazy roll-on-first-open seam) + world/structure createChest's {LootTable, LootTableSeed} chest BlockEntity + world/neighborhood.go SetBlockEntity (the chunk BE list)"
  - phase: 20-structure-polish-loot-inhabitants-beard-persistence
    plan: 01
    provides: "level/loot.Roll (the shared evaluator the chest rolls through)"
  - phase: ENT-04 (inventory)
    provides: "server/inventory.go (ContainerSetContent/SetSlot encoders, handleContainerClick/Close, the doClick engine) + the cursor (carried) on Inventory + the tick-owned 46-slot player window"
  - phase: ENT-03 (block interact)
    provides: "server/block_interact.go handleUseItemOn (the right-click entry) + the useBlockInteraction step-1 hook + withinReach"
provides:
  - "server/chest_open.go: the chest-OPEN path — openChest (resolve BE -> lazy roll -> windowId -> OpenScreen + ContainerSetContent), resolveChest/decodeChestBE (chunk BlockEntity list -> tick-owned chestLoot), nextContainerCounter (1..100), menuTypeID, isChestBlock, the useBlockInteraction override (ChestBlock.useWithoutItem -> ServerPlayer.openMenu), chestMenuItems + sendChestContent (the ChestMenu 0..26 chest / 27..62 player slot layout)"
  - "server/chest_click.go: the chest-window click engine — chestResolveSlot (slot->container map), clickedChest (snapshot + panic-recover + authoritative resend), doChestClick (PICKUP/QUICK_MOVE/THROW ports over the two-container view)"
  - "server/slot_encode.go: openScreen encoder (ClientboundOpenScreenPacket: CONTAINER_ID VarInt + MENU registry id VarInt + Component title)"
  - "server/chest_loot.go: chestContainerSize (27) + ensureContainer (pad the rolled packed list to the 27-slot backing)"
  - "TickLoop.openChests (runtime chest store keyed by world pos) + tickPlayer.openContainer/containerCounter (the open-window state, tick-owned)"
affects: [chest-item persistence-on-save follow-up, the v3 real-client visual gate (structure-chest loot is now visible/takeable)]

# Tech tracking
tech-stack:
  added: []  # NO new deps — pure in-repo (chat Component encoder, data/registryid Menu, level/loot, nbt)
  patterns:
    - "Runtime BlockEntity resolution from a world position: decodeChestBE walks the owning chunk's ch.BlockEntity list (UnpackXZ + Y match), decodes the bare {LootTable, LootTableSeed} compound (nbt.RawMessage.Unmarshal), and caches a tick-owned chestLoot in TickLoop.openChests so re-opens + item moves persist (the net-new seam 20-02 deferred)"
    - "Per-player windowId allocator: ServerPlayer.nextContainerCounter = (counter % 100) + 1, cycling 1..100, never colliding the player-inventory window 0"
    - "Two-container menu without refactoring the player engine: chestResolveSlot maps the 63 chest-window slots onto (chest 0..26, player window 9..44); a dedicated chest doClick ports PICKUP/QUICK_MOVE/THROW over chestSlotRef, leaving inventory_doclick.go UNTOUCHED (zero regression surface)"
    - "Faithful sneak-guard collapse: the vanilla bl9 (isSecondaryUseActive && bothHandsEmpty -> skip the block) is always false in v1 (cited isCrouching=false stub, mirroring food.go), so the chest always opens on right-click — structured to flip when a real sneak read lands"

key-files:
  created:
    - server/chest_open.go (the open path: BE resolve + lazy roll + OpenScreen + ContainerSetContent + the ChestMenu slot layout)
    - server/chest_click.go (the chest-window ContainerClick engine: PICKUP/QUICK_MOVE/THROW over the chest+player view)
    - server/chest_open_test.go (TestChestOpenRollsAndSends, ...ReopenDoesNotReroll, ...ClickMovesItemToPlayer, ...ClosePersistsAndFrees, ...QuickMoveToPlayer)
  modified:
    - server/slot_encode.go (openScreen encoder + the chat Component import)
    - server/chest_loot.go (chestContainerSize + ensureContainer — the 27-slot container backing)
    - server/tick.go (TickLoop.openChests + tickPlayer.openContainer/containerCounter)
    - server/block_interact.go (the v1 no-op useBlockInteraction stub REMOVED — moved to chest_open.go as the real chest dispatch)
    - server/inventory.go (clicked() routes the chest windowId to clickedChest; handleContainerClose frees the windowId + persists)

key-decisions:
  - "Dedicated chest doClick over a chestSlotRef two-container view, NOT a generalization of the player inventory_doclick engine — the player engine (PICKUP/QUICK_MOVE/SWAP/CLONE/THROW/QUICK_CRAFT/PICKUP_ALL over a flat *Inventory) is large and load-bearing; refactoring it to a multi-container abstraction was the balloon risk the plan warned about, so the chest gets its own focused engine covering the operations a chest actually receives (left/right-click, shift-click both directions, Q). Window 0 keeps its full engine untouched (zero regression)."
  - "The chest's rolled items live in the tick-owned TickLoop.openChests (keyed by world pos), decoded ONCE from the chunk BE NBT on first open. Thereafter the runtime chestLoot is authoritative — every click mutates it in place and it persists across opens/closes for the server's lifetime. ContainerClose is therefore just 'free the windowId' (no copy-back needed)."
  - "The cursor (carried) is the player's single inv.carried (vanilla AbstractContainerMenu.carried) — a player has one open menu at a time, so the shared cursor is faithful and lets the chest reuse the exact ItemStack primitives the player engine uses."
  - "unpackLootTable still produces the PACKED roll list (20-01/20-02's golden form); the chest-open path ensureContainer-pads it to 27 slots, placing the rolled stacks sequentially in slots 0..N-1. Vanilla LootTable.fill SHUFFLES stacks into random container slots — that random-slot placement is the documented deferral (items are equally visible/takeable, only their cells differ)."

requirements-completed: [STRUCT-POLISH-01]  # the chest-OPEN UI was the remaining user-facing proof; 20-01+20-02 shipped the evaluator + the lazy store/seam

# Metrics
duration: 90min
completed: 2026-06-27
---

# Phase 20 Plan 06: Chest-OPEN UI (the 20-02 W2 Follow-up) Summary

**Right-clicking a structure chest now resolves the chest BlockEntity from its world position, rolls the stored `{LootTable, LootTableSeed}` lazily on first open (one-shot `unpackLootTable`), allocates a per-player windowId, and opens a `generic_9x3` container menu on the client (`ClientboundOpenScreen` + `ContainerSetContent` with the rolled 27 chest slots + the 36 player slots) — and clicks on that window move items between the chest and the player, with close freeing the windowId while the chest contents persist. This is the net-new interaction subsystem 20-02 explicitly split out, making structure-chest loot visible and takeable in-game (the STRUCT-POLISH-01 user-facing proof).**

## Performance
- **Duration:** ~90 min
- **Tasks:** open path (commit 1) + click/close engine + tests (commit 2)
- **Files:** 3 created + 5 modified

## Task Commits
1. **Chest-open path** — `10157c6b` (feat): BE resolution, lazy roll, OpenScreen + ContainerSetContent, the ChestMenu slot layout, the open-state fields.
2. **Chest container clicks + close** — `87fc2681` (feat): the chest-window click engine (PICKUP/QUICK_MOVE/THROW), the windowId routing in `clicked()`, `handleContainerClose` free+persist, and the 5 capture-verified tests.

## Accomplishments
- **Runtime chest BE resolution.** `decodeChestBE` walks the owning chunk's `ch.BlockEntity` list (matching `UnpackXZ` + `Y`), decodes the bare `{LootTable, LootTableSeed}` compound (`nbt.RawMessage.Unmarshal`), and `resolveChest` caches a tick-owned `chestLoot` in `TickLoop.openChests` — the runtime resolution-from-a-world-position the 20-02 SUMMARY noted did not exist.
- **Lazy one-shot roll on the open path.** `openChest` calls `cl.unpackLootTable()` (rolls through `level/loot.Roll` at the server-stored seed, clears the table) then `ensureContainer()` (pads the packed roll to the 27-slot backing). A re-open finds the table cleared → no re-roll (`TestChestReopenDoesNotReroll`).
- **The vanilla open menu wire.** `ServerPlayer.openMenu` ported: `nextContainerCounter` (`(counter % 100) + 1`, cycling 1..100), `ClientboundOpenScreen(windowId, generic_9x3 menu registry id, "Chest")`, then the initial `ContainerSetContent` (the 63-slot `chestMenuItems`: chest 0..26, player main 27..53, hotbar 54..62 — the `ChestMenu.addChestGrid` + `addStandardInventorySlots` order).
- **Chest container clicks.** A dedicated `doChestClick` ports the PICKUP (left/right-click take/put/swap over the shared cursor), QUICK_MOVE (shift-click move chest↔player, vanilla hotbar-first reverse fill), and THROW (Q-drop) branches over a `chestSlotRef` two-container view. `clickedChest` wraps it in the vanilla `clicked()` snapshot + panic-recover, then re-sends authoritative content + syncs the cursor. The player-inventory engine (`inventory_doclick.go`) is untouched.
- **Close frees + persists.** `handleContainerClose` clears `p.openContainer` (frees the windowId); the chest items are already authoritative in `TickLoop.openChests` and persist across opens (`TestChestClosePersistsAndFrees`).

## 1:1 jar port (cited)
All from `temp/cache/26.2-inner.jar` (`javap -c -p`, this session):
- `net.minecraft.world.level.block.ChestBlock.useWithoutItem` — getMenuProvider → `player.openMenu` (awardStat + angerNearbyPiglins are v1 no-ops, cited). Returns SUCCESS → consumes the action → placement skipped (chest_open.go `useBlockInteraction`).
- `net.minecraft.server.level.ServerPlayer.openMenu` + `nextContainerCounter` — the close-prev / counter-advance / send-OpenScreen / set-containerMenu chain; `containerCounter = counter % 100 + 1` (chest_open.go `openChest` + `nextContainerCounter`).
- `net.minecraft.network.protocol.game.ClientboundOpenScreenPacket.STREAM_CODEC` — `CONTAINER_ID` (VarInt) + `registry(Registries.MENU)` (VarInt) + `ComponentSerialization.TRUSTED_STREAM_CODEC` (Component) (slot_encode.go `openScreen`).
- `net.minecraft.world.inventory.ChestMenu` ctor + `addChestGrid` + `addStandardInventorySlots` — the 27 chest + 27 main + 9 hotbar slot order (chest_open.go `chestMenuItems`, chest_click.go `chestResolveSlot`).
- `net.minecraft.world.inventory.ChestMenu.quickMoveStack` — the `i < containerRows*9 ? moveTo(player, reverse) : moveTo(container)` two-region routing (chest_click.go `chestQuickMove`).
- `net.minecraft.world.inventory.AbstractContainerMenu.doClick` PICKUP/THROW branches (chest_click.go `chestPickup`/`chestThrow`).
- `net.minecraft.server.level.ServerPlayerGameMode.useItemOn` — the bl9 sneak guard ordering (the block runs before the item when `!bl9`); `bl9` is always false in v1 (cited `isCrouching=false` stub).

## Deviations from Plan
None — plan executed as scoped. The W2 follow-up shipped the working vertical slice (open → see loot → take items → close) the objective demanded.

## Known Stubs / Deferrals (cited, not baked away)
| Item | File | Reason |
|------|------|--------|
| `unpackLootTable` places stacks sequentially (slots 0..N-1), not the vanilla `LootTable.fill` random-slot shuffle | server/chest_loot.go (the roll seam) + chest_open.go (ensureContainer pad) | The packed roll list is 20-01/20-02's golden form; the random-slot shuffle (`shuffleAndSplitItems`) is a cosmetic placement refinement — the items are equally visible/takeable, only their cells differ. Wire the shuffle when the fill path lands a vanilla-faithful placement. |
| Chest items persist only IN-MEMORY (`TickLoop.openChests`), not flushed to the chunk BlockEntity NBT on unload/save | server/inventory.go handleContainerClose + the chest BE (only carries {LootTable, LootTableSeed}) | The BE NBT currently has no item list; flushing the rolled/edited container back to NBT on chunk-unload/world-save is a follow-up. In-memory persistence covers the full play session. |
| Single-viewer chest only (no multi-viewer ContainerSetContent fan-out, no chest open/close animation/sound, no double-chest) | server/chest_open.go (one tickPlayer.openContainer) | The objective's explicit fallback: ship the single-viewer path, flag the rest deferred. The container is tick-owned and authoritative, so a second viewer is an additive fan-out later. |
| A cursor item left on the mouse at close is not dropped (vanilla `removed()` drops it) | server/inventory.go handleContainerClose | v1 leaves it on the player cursor; it reconciles on the next inventory interaction. Cited refinement. |
| Trapped/ender/copper chests not openable (only `minecraft:chest`) | server/chest_open.go isChestBlock | v1 opens the plain chest structure chests place; the ChestBlock subclasses are a later extension. |

## Verification Results
- `CGO_ENABLED=0 go build ./...` — exit 0
- `go vet ./server/` — clean
- `go test ./server/` — green (8 chest tests: TestChestLazyRoll, ...MatchesGolden, ...NoTableOpensEmpty [20-02], + TestChestOpenRollsAndSends, ...ReopenDoesNotReroll, ...ClickMovesItemToPlayer, ...ClosePersistsAndFrees, ...QuickMoveToPlayer [new]; full existing server suite incl. inventory/place/break stays green)
- Docker `-race` (`golang:1.26`, `MSYS_NO_PATHCONV=1 ... go test -race ./server/`) — green (full package 11s)
- `git diff go.mod go.sum` — empty (no new deps)
- `grep import "C"` — none
- No tracked-file deletions (`git diff --diff-filter=D HEAD~2 HEAD` empty)

## Self-Check: PASSED
All 3 created files exist on disk (server/chest_open.go, server/chest_click.go, server/chest_open_test.go); both task commits (10157c6b, 87fc2681) are in git history.
