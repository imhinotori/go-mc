package server

// chest_open.go — STRUCT-POLISH-01 chest-OPEN UI (the 20-02 W2 follow-up split). Wires the
// runtime right-click → resolve chest BlockEntity → unpackLootTable (lazy, one-shot) → open a
// container menu (windowId + ClientboundOpenScreen generic_9x3 + ContainerSetContent) → handle
// clicks → close (free the windowId + persist the chest items). This is the net-new interaction
// subsystem 20-02 deferred: it makes structure-chest loot actually visible/takeable in-game.
//
// 1:1 jar port (temp/cache/26.2-inner.jar, javap'd this session). The vanilla chain:
//
//	ServerPlayerGameMode.useItemOn: bl9 = isSecondaryUseActive() && bothHandsEmpty; if (!bl9)
//	    r = blockState.useItemOn(...); if (r.consumesAction()) return r;   // the BLOCK runs first
//	BlockBehaviour.useItemOn → useWithoutItem for a chest:
//	  net.minecraft.world.level.block.ChestBlock.useWithoutItem(state, level, pos, player, hit):
//	      if (level instanceof ServerLevel sl) {
//	          MenuProvider mp = getMenuProvider(state, level, pos);
//	          if (mp != null) { player.openMenu(mp); player.awardStat(OPEN_CHEST);
//	                            PiglinAi.angerNearbyPiglins(sl, player, true); }
//	      }
//	      return InteractionResult.SUCCESS;                                // CONSUMES → no place
//	net.minecraft.server.level.ServerPlayer.openMenu(MenuProvider):
//	      if (containerMenu != inventoryMenu) closeContainer();
//	      nextContainerCounter();                                          // (counter % 100) + 1
//	      AbstractContainerMenu menu = mp.createMenu(containerCounter, inventory, player);
//	      connection.send(new ClientboundOpenScreenPacket(menu.containerId, menu.getType(),
//	                                                       mp.getDisplayName()));
//	      initMenu(menu); containerMenu = menu; return OptionalInt.of(containerCounter);
//	net.minecraft.server.level.ServerPlayer.nextContainerCounter():
//	      this.containerCounter = this.containerCounter % 100 + 1;
//
// Sulfur subset: v1 has no stats/piglin/spectator systems, so awardStat + angerNearbyPiglins +
// the spectator branch are faithful no-ops (cited). The menu's ContainerSetContent (the initial
// slot sync) is sent right after OpenScreen — vanilla's initMenu → broadcastChanges pushes the
// full slot list to the client on open; Sulfur sends ContainerSetContent explicitly (slot_encode).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/recipe"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// openContainer is the tick-owned state of a player's currently-open non-inventory window — the
// Sulfur analogue of ServerPlayer.containerMenu narrowed to what the chest + crafting paths need.
// windowID is the allocated containerCounter (1..100). kind discriminates the window backing: a CHEST
// (chestPos -> the world chest container) or a CRAFTING_TABLE (craftGrid -> the transient 3x3 grid,
// no backing block-entity — vanilla's CraftingMenu.craftSlots is a TransientCraftingContainer freed on
// close). A ContainerClick/Close on windowID resolves back through the kind.
type openContainer struct {
	windowID int
	kind     containerKind

	// chestPos is the world position of the open chest (kind == containerKindChest).
	chestPos pk.Position

	// craftGrid is the transient 3x3 crafting-table grid (kind == containerKindCrafting): 9 cells
	// row-major, plus the result is recomputed into craftResult on each grid change. No persistence —
	// on close the 9 cells are returned to the player (CraftingMenu.removed -> clearContainer).
	craftGrid   [9]component.SlotData
	craftResult component.SlotData

	// cutInput/cutResult/cutResults/cutSelected back the STONECUTTER window (kind ==
	// containerKindStonecutter, PLUGIN-05 Plan 25-03): a single transient INPUT slot (cutInput,
	// StonecutterMenu.inputSlot), the displayed RESULT (cutResult, StonecutterMenu.resultSlot — a
	// virtual ResultContainer entry), the per-input recipe list (cutResults, the
	// SelectableRecipe$SingleInputSet.selectByInput entries), and the selected index (cutSelected, the
	// selectedRecipeIndex DataSlot — -1 = none). On close the input is returned to the player
	// (StonecutterMenu.removed -> clearContainer over the input). No block-entity (transient).
	cutInput    component.SlotData
	cutResult   component.SlotData
	cutResults  []recipe.Stack
	cutSelected int
}

// containerKind discriminates an open non-inventory window.
type containerKind int

const (
	containerKindChest       containerKind = iota // a world chest (chestPos)
	containerKindCrafting                          // a transient crafting-table 3x3 (craftGrid)
	containerKindStonecutter                       // a transient stonecutter single-input picker (cutInput)
)

// chestMenuSize is the chest-window slot count: 27 chest container slots + 27 player main + 9
// hotbar = 63 (the generic_9x3 ChestMenu slot count). ChestMenu adds the chest grid first
// (containerRows × 9 = 27), then addStandardInventorySlots (27 main + 9 hotbar). Verified
// ChestMenu(MenuType, int, Inventory, Container, rows) bytecode.
const chestMenuSize = chestContainerSize + 27 + 9 // 27 + 36 = 63

// chestTitle is the chest window's display name. Vanilla MenuProvider.getDisplayName for a chest
// is the translatable "container.chest" → "Chest". v1 sends the plain literal (no client-side i18n
// dependence); structured to become a translatable Component when the chat layer grows one.
const chestTitle = "Chest"

// nextContainerCounter ports ServerPlayer.nextContainerCounter(): containerCounter = (counter %
// 100) + 1, cycling window ids 1..100 (never 0, the player-inventory window). Tick-owned.
func (p *tickPlayer) nextContainerCounter() int {
	p.containerCounter = p.containerCounter%100 + 1
	return p.containerCounter
}

// menuTypeID resolves the wire menu-type id (the index into the minecraft:menu registry) for a
// menu resource name, or -1 if absent. ClientboundOpenScreen encodes the MenuType as its registry
// index (ByteBufCodecs.registry(Registries.MENU)), so generic_9x3 is its position in registryid.Menu.
func menuTypeID(reg []string, name string) int32 {
	for i, n := range reg {
		if n == name {
			return int32(i)
		}
	}
	return -1
}

// isChestBlock reports whether a block state is an openable chest (ChestBlock). v1 opens the plain
// minecraft:chest (the block structure chests place); trapped/ender/copper chests are out of the v1
// open subset (a later plan extends this to ChestBlock subclasses). CITE ChestBlock instanceof.
func isChestBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:chest"
}

// useBlockInteraction (chest_open override of the block_interact.go hook) ports the BLOCK's own
// right-click step (ServerPlayerGameMode.useItemOn step 1 → BlockState.useItemOn → ChestBlock.
// useWithoutItem). It returns true (the interaction CONSUMED the action → placement is skipped)
// when the clicked block is a chest and the open succeeds; false (PASS) otherwise so a non-chest
// click falls through to placement.
//
// The vanilla bl9 sneak guard (isSecondaryUseActive() && bothHandsEmpty → skip the block, go
// straight to the item) collapses to "always run the block" in v1: ServerPlayer.isCrouching()/
// isSecondaryUseActive() has no sneak-pose decode (cited false stub, mirroring food.go's
// isCrouching), so bl9 is always false → the block interaction always runs. A chest therefore opens
// on any right-click. Structured so a real sneak read flips the guard later without touching this.
func (t *TickLoop) useBlockInteraction(p *tickPlayer, hitPos pk.Position, direction int) bool {
	_ = direction
	if t.world() == nil {
		return false
	}
	state, ok := t.world().GetBlock(hitPos, dimMinY)
	if !ok {
		return false // unloaded: PASS → placement runs
	}
	isChest := isChestBlock(state)
	isCraft := isCraftingTableBlock(state)
	isCut := isStonecutterBlock(state)
	isBed := isBedBlock(state)
	if !isChest && !isCraft && !isCut && !isBed {
		return false // not an interactive block (chest/crafting_table/stonecutter/bed): PASS → placement runs
	}
	// Reach-gate the interaction (the same server-authoritative reach the place/break paths use):
	// a far block is not openable. Vanilla gates the whole useItemOn behind the interaction
	// distance; reusing withinReach keeps the open under the same bound.
	if !t.withinReach(p, hitPos) {
		return false
	}
	if isCraft {
		// CraftingTableBlock.useWithoutItem -> player.openMenu(crafting). The 3x3 transient menu opens
		// on any right-click (the bl9 sneak guard collapses to false in v1, like the chest path).
		return t.openCraftingTable(p, hitPos)
	}
	if isCut {
		// StonecutterBlock.useWithoutItem -> player.openMenu(stonecutter). The single-input picker menu
		// opens on any right-click (the bl9 sneak guard collapses to false in v1, like the chest path).
		return t.openStonecutter(p, hitPos)
	}
	if isBed {
		// BedBlock.useWithoutItem -> player.startSleepInBed (SLEEP-01, bed_block.go useBed). The bed
		// right-click puts the player to sleep (the base subsystem the cat comfort goals gate on). It
		// consumes the action on any bed click (the bl9 sneak guard collapses to false in v1, like the
		// chest path), so placement is skipped whenever the target is a bed.
		return t.useBed(p, hitPos)
	}
	return t.openChest(p, hitPos)
}

// openChest ports ChestBlock.useWithoutItem → ServerPlayer.openMenu for a loot/plain chest at pos:
// resolve (or lazily build) the chest container, roll its loot on first open (one-shot), allocate a
// windowId, send ClientboundOpenScreen(generic_9x3) + the initial ContainerSetContent, and record
// the open-container state. Returns true (the action was consumed) once the menu is sent; false only
// if the chest could not be resolved (then placement is NOT skipped — there is no chest to open).
func (t *TickLoop) openChest(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil {
		return false
	}
	cl := t.resolveChest(pos)
	if cl == nil {
		return false
	}
	// Lazy one-shot roll on first access (RandomizableContainer.unpackLootTable). A re-open finds
	// LootTable already cleared, so this is a no-op the second time. ensureContainer pads the rolled
	// (packed) list to the full 27-slot backing so menu indices 0..26 always resolve.
	if cl.unpackLootTable() {
		// SUB-PERSIST: the roll CLEARED the LootTable and FILLED the container, a persistent state
		// change (ChestBlockEntity now saves Items, not the loot table). Dirty the column so the
		// rolled contents flush even if the player closes without clicking. No-op if persistence off.
		t.markChestDirty(pos)
	}
	cl.ensureContainer()

	// ServerPlayer.openMenu: close any previously-open chest window first (containerMenu !=
	// inventoryMenu → closeContainer). v1 tracks one chest at a time; a stale open is freed without
	// re-persisting beyond what its own clicks already wrote to its tick-owned chestLoot.
	if p.openContainer != nil {
		p.openContainer = nil
	}

	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindChest, chestPos: pos}

	// connection.send(new ClientboundOpenScreenPacket(containerId, generic_9x3, getDisplayName())).
	menuID := menuTypeID(registryid.Menu, "minecraft:generic_9x3")
	p.client.Send(openScreen(int32(win), menuID, chestTitle))

	// initMenu → broadcastChanges: push the full slot list (chest 0..26 + player 27..62). The
	// chest-menu state id is the player inventory's state counter (one open window at a time).
	t.sendChestContent(p, cl)
	return true
}

// createBlockEntityOnPlace replicates LevelChunk.setBlockState's hasBlockEntity() branch for the
// block-entity blocks Sulfur supports: when a chest is placed, create + register its empty
// ChestBlockEntity in the chunk so the open path resolves it (a placed chest opens with 27 empty
// slots, no loot table). The BE Data is an EMPTY compound (no LootTable -> unpackLootTable no-ops).
// Extend the switch as more block-entity blocks land (furnace, etc). CITE: ChestBlock is an
// EntityBlock; ChestBlock.newBlockEntity = new ChestBlockEntity(pos,state) (empty, on-place).
func (t *TickLoop) createBlockEntityOnPlace(pos pk.Position, state block.StateID) {
	if t.world() == nil || !isChestBlock(state) {
		return
	}
	// Empty bare compound: a placed chest has no LootTable and no Items yet.
	empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
	t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:chest"], empty, dimMinY)
}

// resolveChest returns the tick-owned chestLoot container for pos, decoding it from the chunk's
// BlockEntity list on first access and caching it in t.openChests so re-opens + item moves persist.
//
// A PLAYER-PLACED chest carries no BlockEntity in the chunk list (Sulfur's place path writes only the
// block state, not a BE — vanilla's ChestBlock.newBlockEntity would create an empty ChestBlockEntity
// on place). So when decodeChestBE finds no BE but the block at pos IS a chest, we synthesize an EMPTY
// container (no loot table -> opens empty, 27 slots) — the faithful equivalent of a freshly-placed
// chest. Returns nil only when pos is not a chest at all (or the column is unloaded). Tick-owned.
func (t *TickLoop) resolveChest(pos pk.Position) *chestLoot {
	if t.openChests == nil {
		t.openChests = make(map[pk.Position]*chestLoot)
	}
	if cl, ok := t.openChests[pos]; ok {
		return cl // already resolved (rolled or in-progress) — keep the live container
	}
	cl := t.decodeChestBE(pos)
	if cl == nil {
		// No recorded BE: if the block is actually a chest (a player-placed plain chest), open an
		// empty container; otherwise it is not a chest at all -> nil (the click is a non-open).
		if state, ok := t.world().GetBlock(pos, dimMinY); ok && isChestBlock(state) {
			cl = &chestLoot{} // empty: LootTable "" -> unpackLootTable is a no-op, 27 empty slots
		} else {
			return nil
		}
	}
	cl.ensureContainer()
	t.openChests[pos] = cl
	return cl
}

// decodeChestBE looks up the chest BlockEntity at pos in the owning chunk's BlockEntity list and
// decodes its {LootTable, LootTableSeed} NBT into a fresh chestLoot, or returns nil if no chest BE
// is recorded there. The BE.Data is the bare compound payload (root header stripped by
// chestLootNBT), which nbt.RawMessage.Unmarshal decodes directly. A garbled/empty BE yields a
// chestLoot with no table (opens empty) — never a panic (T-20-04 tolerant decode).
func (t *TickLoop) decodeChestBE(pos pk.Position) *chestLoot {
	if t.world() == nil {
		return nil
	}
	col := level.ChunkPos{int32(pos.X >> 4), int32(pos.Z >> 4)}
	ch, ok := t.world().Get(col)
	if !ok {
		return nil
	}
	lx, lz := pos.X&15, pos.Z&15
	for i := range ch.BlockEntity {
		be := ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx != lx || bz != lz || int(be.Y) != pos.Y {
			continue
		}
		// Only chest BEs carry the loot container; a non-chest BE at this exact cell is not openable.
		if be.Type != block.EntityTypes["minecraft:chest"] {
			return nil
		}
		var nbtData struct {
			LootTable     string `nbt:"LootTable"`
			LootTableSeed int64  `nbt:"LootTableSeed"`
		}
		if be.Data.Type == nbt.TagCompound {
			// Tolerant decode: a corrupt BE opens empty (no table), never panics.
			_ = be.Data.Unmarshal(&nbtData)
		}
		return &chestLoot{LootTable: nbtData.LootTable, LootTableSeed: nbtData.LootTableSeed}
	}
	return nil
}

// chestMenuItems builds the 63-slot ContainerSetContent list for the chest window: chest slots
// 0..26 from the chest container, then the player inventory in ChestMenu order — main 27..53
// (player inventory indices 9..35) then hotbar 54..62 (indices 0..8). Mirrors ChestMenu.addChestGrid
// (the 27 chest slots) + addStandardInventorySlots (27 main + 9 hotbar). The slot ORDER here is the
// wire contract the client renders + the ContainerClick slot index maps back through (chestSlotRef).
func chestMenuItems(cl *chestLoot, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, chestMenuSize)
	for i := 0; i < chestContainerSize; i++ {
		out[i] = cl.items[i]
	}
	// main: menu 27..53 ← inventory window slots 9..35
	for i := 0; i < 27; i++ {
		out[chestContainerSize+i] = inv.get(int16(windowMainFirst + i))
	}
	// hotbar: menu 54..62 ← inventory window slots 36..44
	for i := 0; i < 9; i++ {
		out[chestContainerSize+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendChestContent pushes the authoritative ContainerSetContent for the open chest window (the
// initMenu → broadcastChanges full-slot sync, and the resend after any chest click). Bumps the
// player inventory's state id so the client tracks the authoritative state, exactly like sendContent.
func (t *TickLoop) sendChestContent(p *tickPlayer, cl *chestLoot) {
	if p.client == nil || p.openContainer == nil {
		return
	}
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		chestMenuItems(cl, inv), inv.getCarried()))
}
