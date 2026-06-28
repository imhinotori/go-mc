# Chest open + BlockEntity creation — vanilla 26.2 jar spec (1:1 port reference)

Source: `temp/cache/26.2-inner.jar`, decompiled with `javap -c -p` (Zulu 25).
Goal: fix "player-placed chest does NOT open on right-click" in Sulfur (`server/`, branch `ender-776`).

**Root cause (spoiler):** Sulfur's place path (`server/block_interact.go handleUseItemOn`)
writes ONLY the block state via `world.SetBlock`. Vanilla's `LevelChunk.setBlockState`
has a branch — gated on `newState.hasBlockEntity()` + `block instanceof EntityBlock` —
that calls `EntityBlock.newBlockEntity(pos, state)` and `addAndRegisterBlockEntity(be)`.
Sulfur never runs that branch, so a placed chest has NO ChestBlockEntity in the chunk's
`BlockEntity` list. The open path then has nothing to resolve. (`chest_open.go` currently
papers over this by synthesizing an empty container, see "QUÉ FALTA EN SULFUR".)

---

## 1. ServerPlayerGameMode.useItemOn — the right-click-on-block flow

Signature:
```
InteractionResult useItemOn(ServerPlayer player, Level level, ItemStack stack,
                            InteractionHand hand, BlockHitResult hit)
```

Bytecode walk (offsets from `javap`):

- `0–13`  `pos = hit.getBlockPos(); state = level.getBlockState(pos)`
- `15–33` `if (!state.getBlock().isEnabled(level.enabledFeatures())) return FAIL;`
- `34–73` **SPECTATOR branch**: `if (gameModeForPlayer == SPECTATOR) { mp = state.getMenuProvider(level,pos); if (mp != null){ player.openMenu(mp); return CONSUME; } return PASS; }`
  (spectators can open menus but not edit — Sulfur v1 has no spectator, faithful skip)
- `74–99`  `bl8 = player.getMainHandItem().isEmpty() && player.getOffhandItem().isEmpty();`  // **bothHandsEmpty**
- `101–118` `bl9 = player.isSecondaryUseActive() && bl8;`  // **the sneak guard**
- `120–124` `ItemStack copy = stack.copy();`  // for the advancement trigger
- `126–222` **`if (!bl9) { ... the BLOCK runs first ... }`**:
  - `131–148` `InteractionResult r = state.useItemOn(player.getItemInHand(hand), level, player, hand, hit);`  // BlockBehaviour.useItemOn — the **item-aware** block hook
  - `150–173` `if (r.consumesAction()) { CriteriaTriggers.ITEM_USED_ON_BLOCK.trigger(...); return r; }`
  - `174–222` `if (r instanceof InteractionResult.TryEmptyHandInteraction && hand == MAIN_HAND) {`
    - `190–199` `InteractionResult r2 = state.useWithoutItem(level, player, hit);`  // **the item-less block hook — THIS is where ChestBlock opens**
    - `201–222` `if (r2.consumesAction()) { CriteriaTriggers.DEFAULT_BLOCK_USE.trigger(...); return r2; }`
  - `}`
- `223–244` `if (stack.isEmpty() || player.getCooldowns().isOnCooldown(stack)) return PASS;`
- `245–320` **the ITEM runs** (placement): `UseOnContext ctx = new UseOnContext(player, hand, hit);`
  - `259–286` `if (player.hasInfiniteMaterials()) { int n = stack.getCount(); r = stack.useOn(ctx); stack.setCount(n); }`  // CREATIVE save/restore
  - `289–295` `else { r = stack.useOn(ctx); }`  // SURVIVAL — BlockItem.useOn shrinks
  - `297–320` `if (r.consumesAction()) ITEM_USED_ON_BLOCK.trigger(...); return r;`

### Order (the answer to "cuándo corre el BLOQUE vs el ITEM"):
1. SPECTATOR special-case (open-only).
2. If **NOT** `bl9` (sneak-with-empty-hands): run the **BLOCK** — first `state.useItemOn(...)`
   (item-aware), and if that returns `TryEmptyHandInteraction` on the main hand, then
   `state.useWithoutItem(...)`. **A chest consumes here (SUCCESS) → method returns → NO placement.**
3. Only if the block did NOT consume: short-circuit on empty/cooldown, then run the **ITEM**
   (`stack.useOn` → `BlockItem.place`).

`bl9` true means: skip the block entirely, go straight to the item — that's the "sneak to
place against an interactive block" behaviour. In Sulfur `isSecondaryUseActive()` is a false
stub, so `bl9` is always false → the block always runs first (faithful for v1; flips when a
real sneak read lands).

> NOTE: ChestBlock does NOT override `useItemOn` (item-aware) — only `useWithoutItem`. So a
> chest opens through the `r instanceof TryEmptyHandInteraction` → `useWithoutItem` sub-branch.
> The base `BlockBehaviour.useItemOn` returns `TryEmptyHandInteraction` by default, which is
> what routes an empty-hand (or any-hand) chest click into `useWithoutItem`.

---

## 2. ChestBlock.useWithoutItem + getMenuProvider

`net.minecraft.world.level.block.ChestBlock` extends
`AbstractChestBlock<ChestBlockEntity>` implements `SimpleWaterloggedBlock`.

### useWithoutItem
Signature: `InteractionResult useWithoutItem(BlockState state, Level level, BlockPos pos, Player player, BlockHitResult hit)`

Bytecode:
- `0–11`  `if (level instanceof ServerLevel sl) {`
- `13–20` `MenuProvider mp = getMenuProvider(state, level, pos);`
- `22–24` `if (mp != null) {`
- `27–34` `player.openMenu(mp);`
- `35–43` `player.awardStat(getOpenChestStat());`  // Stats.CUSTOM[OPEN_CHEST]
- `44–49` `PiglinAi.angerNearbyPiglins(sl, player, true);`
- `}}`
- `52–55` **`return InteractionResult.SUCCESS;`**  (always SUCCESS, even if mp == null / client side)

So: server-side only, resolve the menu provider, open it, award stat, anger piglins. Returns
SUCCESS unconditionally → consumes the action → placement is skipped.

### getMenuProvider
Signature: `MenuProvider getMenuProvider(BlockState state, Level level, BlockPos pos)`
Bytecode: `combine(state, level, pos, false).apply(MENU_PROVIDER_COMBINER)` then
`.orElse(null)` cast to `MenuProvider`.

`combine(...)` → `DoubleBlockCombiner.combineWithNeigbour(blockEntityType.get(), ...)`.
`combineWithNeigbour` looks up the BlockEntity at `pos` (and the neighbour for a double chest).
**The MENU_PROVIDER_COMBINER (a single-chest combiner) yields the BlockEntity itself as the
MenuProvider** — `ChestBlockEntity extends RandomizableContainerBlockEntity extends
BaseContainerBlockEntity`, which `implements MenuProvider`. So `getMenuProvider` returns the
`ChestBlockEntity` (wrapped in `Optional`), or **`null` if there is no BlockEntity at pos**.

> **CRITICAL:** if the BE does NOT exist, `getMenuProvider` returns null → `useWithoutItem`
> skips `openMenu` but STILL returns SUCCESS. In vanilla the BE always exists for a placed
> chest (see §3), so this null case never happens in practice. In Sulfur it WOULD return null
> (no BE created on place) — which is exactly the bug class.

`ChestBlock.getContainer(...)` (static, used by hoppers) goes through the same `combine` →
`CHEST_COMBINER` → `Optional<Container>.orElse(null)` path. Same null-on-missing-BE behaviour.

---

## 3. ★ CÓMO VANILLA CREA EL BE AL COLOCAR UN COFRE ★ (the core fix)

ChestBlock is an `EntityBlock`:
```
ChestBlock extends AbstractChestBlock<ChestBlockEntity>   // AbstractChestBlock implements EntityBlock
```

`ChestBlock.newBlockEntity(BlockPos pos, BlockState state)`:
```
new ChestBlockEntity(pos, state)        // returns a fresh, EMPTY ChestBlockEntity
```
Bytecode: `new ChestBlockEntity; dup; aload pos; aload state; invokespecial <init>; areturn`.
No items, no loot table — a bare empty 27-slot container (see §4).

### Where newBlockEntity is called: LevelChunk.setBlockState

`net.minecraft.world.level.chunk.LevelChunk.setBlockState(BlockPos pos, BlockState newState, int flags)`
— after writing the state into the section, the BE branch:

- `545–549` **`if (newState.hasBlockEntity()) {`**  // BlockBehaviour.hasBlockEntity() — true for chest
  - `552–568` `if (section.getBlockState(...).is(newState.getBlock())) {`  // confirm the write stuck
    - `571–579` `BlockEntity be = getBlockEntity(pos, EntityCreationType.CHECK);`  // existing BE? (don't create yet)
    - `581–637` `if (be != null && !be.isValidBlockState(newState)) { LOGGER.warn("mismatched..."); removeBlockEntity(pos); be = null; }`
    - `639–669` **`if (be == null) {`**
      - `644–656` `be = ((EntityBlock) newState.getBlock()).newBlockEntity(pos, newState);`
      - `658–668` `if (be != null) addAndRegisterBlockEntity(be);`  // ← **inserts the empty BE into the chunk**
    - `672–681` `} else { be.setBlockState(newState); updateBlockEntityTicker(be); }`  // reuse existing
  - `}`
- `684` `markUnsaved(); return oldState;`

### The exact flow (place a chest):
1. `BlockItem.place` → `level.setBlock(clickedPos, chestState, flags)`.
2. `Level.setBlock` → `LevelChunk.setBlockState(pos, chestState, flags)`.
3. Section write: the chest BlockState lands in the LevelChunkSection.
4. `chestState.hasBlockEntity()` is **true** (ChestBlock declares a BE via its properties).
5. No existing BE at pos → `newBlockEntity(pos, chestState)` creates an **empty ChestBlockEntity**.
6. `addAndRegisterBlockEntity(be)` puts it in the chunk's BE map (and registers tickers/game-event listeners).
7. Result: a freshly-placed chest has a real, empty, 27-slot ChestBlockEntity, ready to open.

This is automatic and synchronous inside `setBlockState`. The BE is created the instant the
chest state is written — NOT lazily, NOT on first open. `getMenuProvider` therefore always
finds the BE.

---

## 4. ChestBlockEntity — constructor, size, empty-on-create

`net.minecraft.world.level.block.entity.ChestBlockEntity extends RandomizableContainerBlockEntity`.

Public ctor: `ChestBlockEntity(BlockPos pos, BlockState state)`
→ `this(BlockEntityType.CHEST, pos, state)`.

Protected ctor `ChestBlockEntity(BlockEntityType<?> type, BlockPos pos, BlockState state)`:
- `super(type, pos, state)`  // RandomizableContainerBlockEntity → BaseContainerBlockEntity → BlockEntity
- `this.items = NonNullList.withSize(getContainerSize(), ItemStack.EMPTY);`  // **27 EMPTY stacks**
- builds a `ChestBlockEntity$1` ContainerOpenersCounter + a `ChestLidController`.

`getContainerSize()` → `bipush 27; ireturn` → **27**.

So a fresh ChestBlockEntity has `items = 27 × ItemStack.EMPTY`, no loot table. `getItems()`
returns that list; `setItems` replaces it. **The menu provider (the BE itself) requires the BE
to be present** — `getMenuProvider` returns null without it — but the BE's items being empty
is totally fine: an empty chest opens normally.

Loot: `loadAdditional`/`saveAdditional` go through `ContainerHelper.loadAllItems/saveAllItems`
plus the inherited `lootTable`/`lootTableSeed` from RandomizableContainerBlockEntity. A
player-placed chest has NO loot table → opens immediately showing 27 empty slots.

---

## 5. openMenu → ChestMenu → packets

`Player.openMenu(MenuProvider mp)` (ServerPlayer override):
- `if (containerMenu != inventoryMenu) closeContainer();`
- `nextContainerCounter();`  // `containerCounter = containerCounter % 100 + 1` (1..100)
- `AbstractContainerMenu menu = mp.createMenu(containerCounter, getInventory(), this);`
- if `menu == null` → (spectator note) return empty; else:
- `connection.send(new ClientboundOpenScreenPacket(menu.containerId, menu.getType(), mp.getDisplayName()));`
- `initMenu(menu);`  // → `menu.broadcastChanges()` pushes initial slot contents
- `this.containerMenu = menu; return OptionalInt.of(containerCounter);`

`mp.createMenu` for a single chest → `ChestMenu.threeRows(containerId, playerInventory, theBE)`
(`MenuType.GENERIC_9x3`). The BE is the `Container` backing the menu. Double chest →
`ChestMenu.sixRows` (GENERIC_9x6, 54 slots) — out of v1 scope.

`ChestMenu(MenuType, int id, Inventory, Container, int rows)`:
- adds `rows*9` chest slots (27 for three rows) from the Container,
- `addStandardInventorySlots(playerInventory, 8, ...)` → 27 main + 9 hotbar = 36.
- Total slot count = 27 + 36 = 63.

Packets sent on open:
1. `ClientboundOpenScreenPacket(containerId, MenuType.GENERIC_9x3 [registry index], displayName)`.
2. `ClientboundContainerSetContentPacket(containerId, stateId, fullSlotList[63], carried)`
   (from `initMenu → broadcastChanges → synchronizeMenuToRemote`).

Display name: `BaseContainerBlockEntity.getDisplayName()` → custom name or
`getDefaultName()` → `Component.translatable("container.chest")` ("Chest").

This part matches `server/chest_open.go` exactly (openScreen + containerSetContent, 63 slots,
windowId cycle 1..100). No change needed in the OPEN packet layer.

---

## 6. Does a freshly-placed empty chest open in vanilla? — YES

Confirmed by the chain:
- place → `setBlockState` creates an **empty ChestBlockEntity** (§3) and registers it.
- right-click → `useWithoutItem` → `getMenuProvider` finds that BE (non-null) → `openMenu` →
  `ChestMenu.threeRows` over the BE's 27 empty `ItemStack.EMPTY` slots.
- Client shows an open chest with 27 empty slots. No loot table required.

The Sulfur bug ("a placed chest does not open") is precisely the **missing BE-on-place** step.
Worldgen/structure chests work because `Neighborhood.SetBlockEntity` (world/neighborhood.go:151)
appends a chest BE to the chunk list — but the **player place path has no equivalent**.

---

## QUÉ FALTA EN SULFUR (gap analysis)

### Current state
- `server/block_interact.go handleUseItemOn` (place path, ~line 238): writes ONLY the block
  state — `t.world.SetBlock(placePos, placeState, dimMinY)` — then `reconcileEdit`. **No BE is
  created.** This is the literal divergence from `LevelChunk.setBlockState`'s `hasBlockEntity()`
  branch (§3).
- `server/chest_open.go resolveChest` (line ~175) + `decodeChestBE` (line ~202): the OPEN path
  looks up a chest BE in the chunk's `BlockEntity` list. For a **player-placed** chest that list
  has no entry, so `decodeChestBE` returns nil and `resolveChest` **synthesizes** an empty
  `&chestLoot{}` in memory (line ~187). It is cached only in `t.openChests[pos]` — NOT written
  back into the chunk's `BlockEntity` list, NOT persisted.
- `world/neighborhood.go SetBlockEntity` (line 151): the only place a chest BE is appended to a
  chunk — and it's worldgen-only (the `Neighborhood`/3x3 scheduler view), not reachable from the
  runtime tick place path.

### Why the chest "does not open"
If `resolveChest`'s in-memory synthesis path is reached, the chest *should* open empty. So the
observed "does not open" implies one of:
1. `isChestBlock(state)` (chest_open.go:87) returns false for the just-placed state — it checks
   `block.StateList[s].ID() == "minecraft:chest"`. If placement wrote a different state id than
   the open path resolves (e.g. a default vs a FACING/waterlogged variant, or `GetBlock` reads a
   stale/other state), the guard fails → `useBlockInteraction` PASSes → no open.
2. The placed state's `ID()` isn't exactly `"minecraft:chest"` (StateList lookup mismatch), so
   the open path treats it as a non-chest.
3. `withinReach` / column-load gating differs between place and open.

The robust fix is to stop relying on the synthesis fallback and make Sulfur **mirror vanilla**:
create the chest BE at place time, exactly as `LevelChunk.setBlockState` does.

### THE FIX (port of §3) — what to add to the place path
In the place path, AFTER `world.SetBlock(placePos, placeState, …)` succeeds, replicate the
`hasBlockEntity()` branch:

1. Determine `placeState.hasBlockEntity()` — i.e. the placed block is an `EntityBlock`
   (for v1: `isChestBlock(placeState)` is the chest case; generalize to a `blockHasBlockEntity`
   table later). This is the `newState.hasBlockEntity()` gate (offset 545).
2. If true and no BE already exists at `placePos` (the `getBlockEntity(CHECK)` + null check,
   offsets 571–639): create an **empty** chest BE — the analogue of
   `newBlockEntity(pos, state)` = empty `ChestBlockEntity` with 27 empty slots and **no loot
   table** — and **append it to the owning chunk's `BlockEntity` list** (the analogue of
   `addAndRegisterBlockEntity`). Reuse the `level.BlockEntity{Y, Type, Data}` shape from
   `Neighborhood.SetBlockEntity` (neighborhood.go:159), with `Type =
   block.EntityTypes["minecraft:chest"]` and `Data` an EMPTY compound (no LootTable key — a
   placed chest has none). `PackXZ(local x, local z)` like SetBlockEntity does.
3. Needs a runtime (tick-side) `world.SetBlockEntity` on the chunk-manager view that the place
   path can call (the existing `Neighborhood.SetBlockEntity` is the worldgen view; the tick path
   uses `world.ChunkManager` per block_interact.go's header). Add the equivalent insert on the
   tick-owned chunk so the BE lands in the same `BlockEntity` list `decodeChestBE` reads.

With that, `decodeChestBE` finds a real (empty) chest BE → `resolveChest` returns it without
synthesizing → `isChestBlock` + open proceed → the placed chest opens with 27 empty slots,
**and the BE persists** (it's in the chunk list, so save/load round-trips it).

> Faithful-port note: vanilla creates the BE inside `setBlockState` for EVERY block-entity
> block, gated on `hasBlockEntity()`. The minimal v1 fix scopes it to chests (the only openable
> BE block in scope), but structure it as a `hasBlockEntity(state)` check + a generic
> `newBlockEntity(state)` factory so non-chest BE blocks (furnaces, etc.) drop in later without
> touching the place path again — mirroring the vanilla `EntityBlock.newBlockEntity` dispatch.

### Secondary cleanup (after the fix lands)
Once the place path creates the BE, `resolveChest`'s synthesis fallback (chest_open.go:183–190)
becomes dead for placed chests — keep it only as a tolerant guard, or remove it so the open path
is a strict 1:1 of `getMenuProvider` (BE present → open; BE absent → the vanilla null path, which
in practice never happens because place always creates one).

---

## Citations (class :: method → jar offsets)
- `net.minecraft.server.level.ServerPlayerGameMode :: useItemOn` — block-first @126, useItemOn @145, useWithoutItem @196, item place @245–320, creative save/restore @259–286.
- `net.minecraft.world.level.block.ChestBlock :: useWithoutItem` @0–55 (getMenuProvider @17, openMenu @31, return SUCCESS @52).
- `net.minecraft.world.level.block.ChestBlock :: getMenuProvider` @0–26 (combine → MENU_PROVIDER_COMBINER → orElse(null)).
- `net.minecraft.world.level.block.ChestBlock :: newBlockEntity` @0–9 (new ChestBlockEntity(pos,state)).
- `net.minecraft.world.level.chunk.LevelChunk :: setBlockState` — hasBlockEntity gate @545, getBlockEntity(CHECK) @571, mismatch-removal @581–637, **newBlockEntity @644–656 + addAndRegisterBlockEntity @663–666**, reuse-existing @672–681.
- `net.minecraft.world.level.block.entity.ChestBlockEntity :: <init>` (items = NonNullList.withSize(27, EMPTY)) and `:: getContainerSize` → 27.
