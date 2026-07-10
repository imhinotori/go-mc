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
	"github.com/imhinotori/sulfur/save"
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

	// merchantVillagerID / mpay0 / mpay1 / mresult / mselectionHint / mactiveOffer back the MERCHANT
	// window (kind == containerKindMerchant, VILLAGER-MENU): the trading Villager's thin entity id
	// (merchantVillagerID — the Merchant the menu is bound to, resolved back through its owning region on
	// each click), the two transient PAYMENT input slots (mpay0/mpay1, MerchantContainer.itemStacks[0/1]),
	// the displayed RESULT (mresult, MerchantContainer.itemStacks[2] — a virtual assembled stack), the
	// selectionHint (mselectionHint, the ServerboundSelectTrade index → MerchantContainer.selectionHint),
	// and the currently-active offer index (mactiveOffer, MerchantContainer.activeOffer reduced to the
	// offer's list index; -1 = none). On close the two payment inputs are returned to the player
	// (MerchantMenu.removed -> placeItemBackInInventory over slots 0,1; the result is virtual, not returned).
	// No block-entity (transient).
	merchantVillagerID int32
	mpay0              component.SlotData
	mpay1              component.SlotData
	mresult            component.SlotData
	mselectionHint     int
	mactiveOffer       int

	// brewingStandPos is the world position of the open brewing_stand (kind == containerKindBrewingStand).
	// The window's 5 slots (3 bottles / ingredient / fuel) + the 2 data slots (brewTime, fuel) back onto the
	// tick-owned brewingStandBE at t.brewingStands[brewingStandPos] — NO transient copy (like the furnace):
	// the brewing container IS the block-entity, so a click mutates the same items the brew drive ticks, and
	// close just frees the window (the items persist in the BE, like a chest/furnace).
	brewingStandPos pk.Position

	// furnacePos is the world position of the open furnace/blast_furnace/smoker (kind ==
	// containerKindFurnace, GAMEPLAY-05). The window's 3 slots (input/fuel/result) + the 2 progress data
	// slots back onto the tick-owned furnaceBE at t.furnaces[furnacePos] — NO transient copy (unlike the
	// crafting/stonecutter grids): the furnace container IS the block-entity (AbstractFurnaceMenu wraps the
	// BE's SimpleContainer + ContainerData), so a click mutates the same items the cook drive ticks, and
	// close just frees the window (the items persist in the BE, like a chest). ContainerLevelAccess reach.
	furnacePos pk.Position

	// dispenserPos is the world position of the open dispenser/dropper (kind == containerKindDispenser,
	// REDSTONE TIER-4). The window's 9 grid slots back onto the tick-owned dispenserBE at
	// t.dispensers[dispenserPos] — NO transient copy (like the furnace/chest): the dispenser container IS
	// the block-entity (DispenserMenu wraps the BE's SimpleContainer), so a click mutates the same items the
	// dispense drive shoots from, and close just frees the window (the items persist in the BE, like a chest).
	dispenserPos pk.Position

	// hopperPos is the world position of the open hopper (kind == containerKindHopper). The window's 5 grid
	// slots back onto the tick-owned hopperBE at t.hoppers[hopperPos] — NO transient copy (like the
	// dispenser/furnace/chest): the hopper container IS the block-entity, so a click mutates the same items
	// the transfer drive moves, and close just frees the window (the items persist in the BE).
	hopperPos pk.Position

	// beaconPos is the world position of the open beacon (kind == containerKindBeacon, BEACON-01). The
	// window's single payment slot + 3 data slots back onto the tick-owned beaconBE at t.beacons[beaconPos].
	// The SetBeacon effect selection consumes the payment. On close the payment is DROPPED (BeaconMenu.removed
	// -> player.drop(payment, false)); the beacon's selected effect + level persist in the BE.
	beaconPos pk.Position

	// minecartEntityID / minecartSlotCount back the CONTAINER-MINECART window (kind ==
	// containerKindMinecartChest): the open chest/hopper minecart's THIN entity id (resolved back through its
	// owning region on each click, the Folia rule) and its container slot count (27 chest / 5 hopper). The
	// window's slots back onto the entity's minecartItems (AbstractMinecartContainer.itemStacks) — no
	// transient copy (the container IS the entity), so close just frees the window (the items persist on the
	// entity). CITE AbstractMinecartContainer.
	minecartEntityID  int32
	minecartSlotCount int

	// anvilInput / anvilAdd / anvilResult / anvilCost / anvilRepairUnits / anvilOnlyRenaming /
	// anvilName / anvilNameSet / anvilPos back the ANVIL window (kind == containerKindAnvil): the two
	// TRANSIENT input slots (anvilInput = AnvilMenu input slot 0, anvilAdd = additional slot 1 — an
	// ItemCombinerMenu SimpleContainer returned to the player on close), the displayed RESULT
	// (anvilResult = the ResultContainer, take-only, recomputed by createResult), the cost DataSlot
	// (anvilCost = AnvilMenu.cost), the repair-material consume count (anvilRepairUnits =
	// repairItemCountCost), the pure-rename flag (anvilOnlyRenaming), the pending rename
	// (anvilName/anvilNameSet = AnvilMenu.itemName; anvilNameSet distinguishes "" from unset), and the
	// anvil block position (anvilPos — for the 12% break roll on take). No block-entity (transient).
	anvilInput        component.SlotData
	anvilAdd          component.SlotData
	anvilResult       component.SlotData
	anvilCost         int
	anvilRepairUnits  int
	anvilOnlyRenaming bool
	anvilName         string
	anvilNameSet      bool
	anvilPos          pk.Position

	// enchantItem / enchantLapis / enchantSeed / enchantCosts / enchantClueEnch / enchantClueLevel /
	// enchantPos back the ENCHANTMENT-TABLE window (kind == containerKindEnchant): the two TRANSIENT
	// slots (enchantItem = EnchantmentMenu enchantSlots[0], enchantLapis = enchantSlots[1] — a
	// SimpleContainer(2) returned to the player on close), the per-open enchantmentSeed DataSlot
	// (enchantSeed), the three offer level-costs (enchantCosts = EnchantmentMenu.costs), the three
	// offered-enchant clues (enchantClueEnch = enchantClue, the wire enchantment id or -1) + level
	// clues (enchantClueLevel = levelClue), and the enchanting_table block position (enchantPos — for
	// the bookshelf scan + stillValid). No block-entity (transient).
	enchantItem      component.SlotData
	enchantLapis     component.SlotData
	enchantSeed      int32
	enchantCosts     [3]int
	enchantClueEnch  [3]int32
	enchantClueLevel [3]int32
	enchantPos       pk.Position

	// grind0/grind1/grindResult back the GRINDSTONE window (kind == containerKindGrindstone): the two
	// transient INPUT slots (GrindstoneMenu.repairSlots[0]/[1]) + the displayed RESULT (a virtual
	// ResultContainer entry, computeResult of the two inputs). On close the two inputs are returned to
	// the player (GrindstoneMenu.removed -> clearContainer over repairSlots). The result is virtual (not
	// returned). grindPos is the world position (ContainerLevelAccess reach + the levelEvent/XP spawn).
	grind0      component.SlotData
	grind1      component.SlotData
	grindResult component.SlotData
	grindPos    pk.Position

	// smithTemplate/smithBase/smithAddition/smithResult back the SMITHING window (kind ==
	// containerKindSmithing): the 3 transient INPUT slots (SmithingMenu inputSlots 0 template / 1 base /
	// 2 addition) + the displayed RESULT (a virtual ResultContainer entry, the matched recipe's
	// assemble()). On close the 3 inputs are returned to the player (ItemCombinerMenu.removed ->
	// clearContainer over inputSlots). smithPos is the world position (ContainerLevelAccess reach).
	smithTemplate component.SlotData
	smithBase     component.SlotData
	smithAddition component.SlotData
	smithResult   component.SlotData
	smithPos      pk.Position

	// loomBanner/loomDye/loomPattern/loomResult/loomPatterns/loomSelected/loomPos back the LOOM window
	// (kind == containerKindLoom): the three transient INPUT slots (LoomMenu bannerSlot 0 / dyeSlot 1 /
	// patternSlot 2 -- an inputContainer SimpleContainer(3) returned to the player on close), the displayed
	// RESULT (loomResult = the outputContainer entry, take-only, recomputed by setupResultSlot), the current
	// selectable-pattern name list (loomPatterns = LoomMenu.selectablePatterns), the selected index
	// (loomSelected = the selectedBannerPatternIndex DataSlot; -1 = none), and the loom block position
	// (loomPos -- for the ContainerLevelAccess reach + the take-sound seam). No block-entity (transient).
	loomBanner   component.SlotData
	loomDye      component.SlotData
	loomPattern  component.SlotData
	loomResult   component.SlotData
	loomPatterns []string
	loomSelected int
	loomPos      pk.Position
}

// containerKind discriminates an open non-inventory window.
type containerKind int

const (
	containerKindChest         containerKind = iota // a world chest (chestPos)
	containerKindCrafting                           // a transient crafting-table 3x3 (craftGrid)
	containerKindStonecutter                        // a transient stonecutter single-input picker (cutInput)
	containerKindMerchant                           // a villager merchant window (2 payment + 1 result)
	containerKindFurnace                            // a furnace/blast_furnace/smoker BE (furnacePos)
	containerKindBrewingStand                       // a brewing_stand BE (brewingStandPos)
	containerKindDispenser                          // a dispenser/dropper BE (dispenserPos)
	containerKindHopper                             // a hopper BE (hopperPos)
	containerKindBeacon                             // a beacon BE (beaconPos)
	containerKindMinecartChest                      // a chest/hopper minecart entity (minecartEntityID)
	containerKindAnvil                              // a transient anvil (2 input + 1 result + cost)
	containerKindEnchant                            // a transient enchanting-table (item + lapis + 3 offers)
	containerKindGrindstone                         // a transient grindstone 2-input combiner (grind0/grind1)
	containerKindSmithing                           // a transient smithing 3-input combiner (smithTemplate/Base/Addition)
	containerKindLoom                               // a transient loom (banner/dye/pattern -> layered banner)
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
func (t *TickLoop) useBlockInteraction(p *tickPlayer, hitPos pk.Position, direction int, cursorX, cursorY, cursorZ float32) bool {
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
	isFurnace := isAnyFurnaceBlock(state)
	isBrew := isBrewingStandBlock(state)
	// CORE REDSTONE: a lever/button right-click TOGGLES its POWERED state (LeverBlock/ButtonBlock
	// .useWithoutItem) and consumes the interaction so no block is placed. CITE: LeverBlock.useWithoutItem
	// (pull), ButtonBlock.useWithoutItem (press).
	isLever := block.IsLever(state)
	isButton := block.IsButton(state)
	// REDSTONE TIER-2: a repeater right-click cycles its DELAY (RepeaterBlock.useWithoutItem), a
	// comparator right-click toggles its MODE compare<->subtract (ComparatorBlock.useWithoutItem); both
	// consume the interaction so no block is placed. CITE: RepeaterBlock/ComparatorBlock.useWithoutItem.
	isRepeater := block.IsRepeater(state)
	isComparator := block.IsComparator(state)
	// REDSTONE TIER-4: a dispenser/dropper right-click OPENS its 9-slot (3x3) container menu
	// (DispenserBlock.useWithoutItem -> player.openMenu(dispenser)) and consumes the interaction so no block
	// is placed. CITE: DispenserBlock.useWithoutItem.
	isDispenser := block.IsDispenserFamily(state)
	// A hopper right-click OPENS its 5-slot container menu (HopperBlock.useWithoutItem ->
	// player.openMenu(hopper)) and consumes the interaction so no block is placed. CITE: HopperBlock.useWithoutItem.
	isHopper := block.IsHopper(state)
	// A beacon right-click OPENS its payment/effect-selection menu (BeaconBlock.useWithoutItem ->
	// player.openMenu(beacon)) and consumes the interaction so no block is placed. CITE: BeaconBlock.useWithoutItem.
	isBeacon := isBeaconBlock(state)
	// An anvil-family right-click OPENS the AnvilMenu; an enchanting_table opens the EnchantmentMenu;
	// a grindstone opens its 2-input repair/disenchant menu; a smithing_table opens its 3-input
	// transform/trim menu — all consume the interaction so no block is placed. CITE Anvil/EnchantingTable/
	// Grindstone/SmithingTableBlock.useWithoutItem.
	isAnvil := isAnyAnvilBlock(state)
	isEnchant := isEnchantingTableBlock(state)
	isGrindstone := isGrindstoneBlock(state)
	isSmithing := isSmithingTableBlock(state)
	// A loom right-click OPENS its banner/dye/pattern APPLY menu (LoomBlock.useWithoutItem ->
	// player.openMenu(loom)) and consumes the interaction so no block is placed. CITE LoomBlock.useWithoutItem.
	isLoom := isLoomBlock(state)
	// D-I1: a door / trapdoor / fence-gate right-click TOGGLES its OPEN (DoorBlock/TrapDoorBlock/
	// FenceGateBlock.useWithoutItem) and consumes the interaction so no block is placed. Iron doors/
	// trapdoors reject a hand click (canOpenByHand=false -> PASS) inside the dispatch. CITE:
	// DoorBlock/TrapDoorBlock/FenceGateBlock.useWithoutItem.
	isDoorFamily := block.IsDoor(state) || block.IsTrapdoor(state) || block.IsFenceGate(state)
	// A sign right-click re-opens its edit screen (unwaxed) or is a silent no-op (waxed); either way
	// it consumes the interaction so no block is placed. CITE: SignBlock.useWithoutItem.
	isSign := isSignBlock(state)
	// A campfire right-click with a campfire-cooking item PLACES the food into a cooking slot
	// (CampfireBlock.useItemOn -> placeFood); a non-food hand passes through. CITE CampfireBlock.useItemOn.
	isCampfire := isCampfireBlock(state)
	// A bell right-click RINGS it (BellBlock.useWithoutItem -> onHit -> attemptToRing) and consumes the
	// interaction so no block is placed. CITE BellBlock.useWithoutItem.
	isBell := isBellBlock(state)
	// A lectern right-click either OPENS the reading screen (has book -- menu DEFERRED) or PLACES a
	// #lectern_books item (LecternBlock.useItemOn/useWithoutItem). CITE LecternBlock.useItemOn.
	isLectern := block.IsLectern(state)
	// A jukebox right-click INSERTS a music disc (empty) or EJECTS the loaded disc (JukeboxBlock.useItemOn/
	// useWithoutItem). CITE JukeboxBlock.useItemOn / useWithoutItem.
	isJukebox := block.IsJukebox(state)
	// A chiseled bookshelf right-click ADDS a #bookshelf_books item to the clicked slot, or REMOVES the
	// book already in that slot (ChiseledBookShelfBlock.useItemOn/useWithoutItem). The slot is resolved
	// from the cursor hit-vector + clicked face (SelectableSlotContainer.getHitSlot). CITE
	// ChiseledBookShelfBlock.useItemOn / useWithoutItem.
	isBookshelf := block.IsChiseledBookshelf(state)
	if !isChest && !isCraft && !isCut && !isBed && !isFurnace && !isBrew && !isLever && !isButton &&
		!isRepeater && !isComparator && !isDispenser && !isHopper && !isBeacon && !isAnvil && !isEnchant &&
		!isGrindstone && !isSmithing && !isLoom && !isDoorFamily && !isSign &&
		!isCampfire && !isBell && !isLectern && !isJukebox && !isBookshelf {
		return false // not an interactive block: PASS → placement runs
	}
	// Reach-gate the interaction (the same server-authoritative reach the place/break paths use):
	// a far block is not openable. Vanilla gates the whole useItemOn behind the interaction
	// distance; reusing withinReach keeps the open under the same bound.
	if !t.withinReach(p, hitPos) {
		return false
	}
	if isDoorFamily {
		// tryDoorInteraction toggles OPEN 1:1 with vanilla (door double-half sync, fence-gate faces the
		// player, iron material hand-gate). Returns false for an iron door/trapdoor hand-reject (PASS),
		// so placement continues exactly as vanilla's useItemOn continuation. CITE: door_interact.go.
		if t.tryDoorInteraction(hitPos, state, p) {
			return true
		}
	}
	if isLever {
		return t.useLever(hitPos, state) // LeverBlock.pull: cycle POWERED + updateNeighbours.
	}
	if isButton {
		return t.pressButton(hitPos, state) // ButtonBlock.press: POWERED=true + scheduleTick(unpress).
	}
	if isRepeater {
		return t.useRepeater(hitPos, state) // RepeaterBlock.useWithoutItem: cycle DELAY.
	}
	if isComparator {
		return t.useComparator(hitPos, state) // ComparatorBlock.useWithoutItem: cycle MODE.
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
	if isFurnace {
		// AbstractFurnaceBlock.useWithoutItem -> player.openMenu(furnace/blast_furnace/smoker). The 3-slot
		// furnace menu opens on any right-click (the bl9 sneak guard collapses to false in v1, like the
		// chest path), so placement is skipped whenever the target is a furnace-family block.
		return t.openFurnace(p, hitPos)
	}
	if isBrew {
		// BrewingStandBlock.useWithoutItem -> player.openMenu(brewing_stand). The 5-slot brewing menu opens
		// on any right-click (the bl9 sneak guard collapses to false in v1, like the chest path), so
		// placement is skipped whenever the target is a brewing_stand.
		return t.openBrewingStand(p, hitPos)
	}
	if isDispenser {
		// DispenserBlock.useWithoutItem -> player.openMenu(dispenser/dropper). The 9-slot (3x3) menu opens
		// on any right-click (the bl9 sneak guard collapses to false in v1, like the chest path), so
		// placement is skipped whenever the target is a dispenser-family block.
		return t.openDispenser(p, hitPos)
	}
	if isHopper {
		// HopperBlock.useWithoutItem -> player.openMenu(hopper). The 5-slot hopper menu opens on any
		// right-click (the sneak guard collapses to false in v1, like the chest path), so placement is
		// skipped whenever the target is a hopper.
		return t.openHopper(p, hitPos)
	}
	if isBeacon {
		// BeaconBlock.useWithoutItem -> player.openMenu(beacon). The payment/effect-selection menu opens on
		// any right-click (the sneak guard collapses to false in v1, like the chest path), so placement is
		// skipped whenever the target is a beacon.
		return t.openBeacon(p, hitPos)
	}
	if isAnvil {
		// AnvilBlock.useWithoutItem -> player.openMenu(anvil). The 2-input + result menu opens on any
		// right-click (the sneak guard collapses to false in v1), so placement is skipped for an anvil.
		return t.openAnvil(p, hitPos)
	}
	if isEnchant {
		// EnchantingTableBlock.useWithoutItem -> player.openMenu(enchantment). The item + lapis + 3-offer
		// menu opens on any right-click (the sneak guard collapses to false in v1), so placement is skipped.
		return t.openEnchantTable(p, hitPos)
	}
	if isGrindstone {
		// GrindstoneBlock.useWithoutItem -> player.openMenu(grindstone). The 2-input repair/disenchant menu
		// opens on any right-click (the sneak guard collapses to false in v1, like the chest path).
		return t.openGrindstone(p, hitPos)
	}
	if isSmithing {
		// SmithingTableBlock.useWithoutItem -> player.openMenu(smithing). The 3-input transform menu opens on
		// any right-click (the sneak guard collapses to false in v1, like the chest path).
		return t.openSmithing(p, hitPos)
	}
	if isSign {
		// SignBlock.useWithoutItem: re-open the edit screen for an unwaxed sign (or a silent no-op for
		// a waxed one). Consumes the interaction either way so no block is placed. CITE
		// SignBlock.useWithoutItem.
		return t.reopenSignEdit(p, hitPos)
	}
	if isLoom {
		// LoomBlock.useWithoutItem -> player.openMenu(loom). The banner/dye/pattern apply menu opens on any
		// right-click (the sneak guard collapses to false in v1, like the chest path).
		return t.openLoom(p, hitPos)
	}
	if isCampfire {
		// CampfireBlock.useItemOn -> placeFood: place a campfire-cooking item into a slot (returns false for a
		// non-food hand so placement continues). CITE CampfireBlock.useItemOn.
		return t.useCampfire(p, hitPos, state)
	}
	if isBell {
		// BellBlock.useWithoutItem -> onHit: ring the bell (always consumes the interaction). CITE BellBlock.
		return t.useBell(p, hitPos, state)
	}
	if isLectern {
		// LecternBlock.useItemOn/useWithoutItem: place a book, or open the reader (menu DEFERRED). Returns
		// false when the empty lectern is clicked with a non-book hand (placement continues). CITE LecternBlock.
		return t.useLectern(p, hitPos, state)
	}
	if isJukebox {
		// JukeboxBlock.useItemOn/useWithoutItem: insert a disc, or eject the loaded one. Returns false when the
		// empty jukebox is clicked with a non-disc hand (placement continues). CITE JukeboxBlock.
		return t.useJukebox(p, hitPos, state)
	}
	if isBookshelf {
		// ChiseledBookShelfBlock.useItemOn/useWithoutItem: add/remove a book at the cursor-resolved slot.
		// Returns false (PASS) when the click misses the facing face's 2x3 grid (placement continues).
		// CITE ChiseledBookShelfBlock.useItemOn / useWithoutItem.
		return t.useChiseledBookshelf(p, hitPos, state, direction, cursorX, cursorY, cursorZ)
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
	if t.world() == nil {
		return
	}
	if isChestBlock(state) {
		// Empty bare compound: a placed chest has no LootTable and no Items yet.
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:chest"], empty, dimMinY)
		return
	}
	if block.IsHopper(state) {
		// A placed hopper gets its (empty, cooldown -1) HopperBlockEntity + an empty BE compound so the
		// open/transfer paths resolve it. HopperBlock.onPlace then runs checkPoweredState so the ENABLED
		// property matches the redstone environment at placement time. CITE: HopperBlock (EntityBlock,
		// newBlockEntity=HopperBlockEntity) + HopperBlock.onPlace -> checkPoweredState.
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:hopper"], empty, dimMinY)
		t.resolveHopper(pos, state)
		t.hopperCheckPoweredState(pos, state)
		return
	}
	if isBeaconBlock(state) {
		// A placed beacon gets its (level 0, no effect) BeaconBlockEntity + an empty BE compound so the open +
		// tick drives resolve it. BeaconBlock is a BaseEntityBlock; newBlockEntity = new BeaconBlockEntity(pos,
		// state). The beam scan + updateBase then run from the next tick. CITE: BeaconBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:beacon"], empty, dimMinY)
		t.resolveBeacon(pos)
		return
	}
	if isConduitBlock(state) {
		// A placed conduit gets its (inactive, no target) ConduitBlockEntity + an empty BE compound so the tick
		// drive resolves it. ConduitBlock is a BaseEntityBlock; newBlockEntity = new ConduitBlockEntity(pos,
		// state). The activation-frame scan then runs from the next %40 boundary. Unlike the beacon there is no
		// menu, so registration into t.conduits happens here (resolveConduit) — the tick driver picks it up.
		// CITE: ConduitBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:conduit"], empty, dimMinY)
		t.resolveConduit(pos)
		return
	}
	if isSpawnerBlock(state) {
		// A placed spawner gets its (default, empty mobName) SpawnerBlockEntity + an empty BE compound so
		// the tick drive resolves it. SpawnerBlock is a BaseEntityBlock; newBlockEntity = new
		// SpawnerBlockEntity(pos, state) whose BaseSpawner holds the ctor defaults (spawnDelay 20, etc). A
		// placed spawner is empty until configured (a spawn egg / NBT sets the entity), so mobName is "" and
		// the burst no-ops. Registration into t.spawners happens here (resolveSpawner). CITE SpawnerBlock.
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:mob_spawner"], empty, dimMinY)
		t.resolveSpawner(pos, "")
		return
	}
	if isSculkCatalystBlock(state) {
		// A placed SCULK CATALYST gets its (empty spreader) SculkCatalystBlockEntity + an empty BE compound
		// so the tick drive resolves it. SculkCatalystBlock is a BaseEntityBlock; newBlockEntity = new
		// SculkCatalystBlockEntity(pos, state). The spreader cursors run from the next tick (empty until a
		// nearby mob dies). CITE: SculkCatalystBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:sculk_catalyst"], empty, dimMinY)
		t.resolveSculkCatalyst(pos)
		return
	}
	if isSculkSensorBlock(state) {
		// A placed SCULK SENSOR / CALIBRATED SCULK SENSOR gets its (freq 0) SculkSensorBlockEntity + an
		// empty BE compound so the tick + listener drives resolve it. Both are BaseEntityBlocks;
		// newBlockEntity = new SculkSensorBlockEntity / CalibratedSculkSensorBlockEntity. CITE:
		// SculkSensorBlock / CalibratedSculkSensorBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		if block.IsCalibratedSculkSensor(state) {
			t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:calibrated_sculk_sensor"], empty, dimMinY)
		} else {
			t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:sculk_sensor"], empty, dimMinY)
		}
		t.resolveSculkSensor(pos)
		return
	}
	if isSculkShriekerBlock(state) {
		// A placed SCULK SHRIEKER gets its (warningLevel 0) SculkShriekerBlockEntity + an empty BE compound
		// so the tick + step drives resolve it. SculkShriekerBlock is a BaseEntityBlock; newBlockEntity =
		// new SculkShriekerBlockEntity(pos, state). CITE: SculkShriekerBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:sculk_shrieker"], empty, dimMinY)
		t.resolveSculkShrieker(pos)
		return
	}
	if isSignBlock(state) {
		// SignBlock is a BaseEntityBlock; newBlockEntity = new SignBlockEntity(pos, state) (empty:
		// default front/back SignText, not waxed). Write an empty BE compound so the open-editor +
		// ServerboundSignUpdate paths resolve it, and register the empty signBE in t.signs. CITE
		// SignBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:sign"], empty, dimMinY)
		t.resolveSignBE(pos)
		return
	}
	if isCampfireBlock(state) {
		// CampfireBlock is a BaseEntityBlock; newBlockEntity = new CampfireBlockEntity(pos, state) (empty:
		// 4 clear cooking slots). Write an empty BE compound so the cook drive resolves it, and register the
		// empty campfireBE in t.campfires. The cook drive runs from the next tick (a LIT campfire cooks any
		// placed food). CITE CampfireBlock (EntityBlock). Both campfire + soul_campfire use the campfire BE.
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:campfire"], empty, dimMinY)
		t.resolveCampfire(pos)
		return
	}
	if isBellBlock(state) {
		// BellBlock is a BaseEntityBlock; newBlockEntity = new BellBlockEntity(pos, state) (empty: not
		// shaking, no click). Write an empty BE compound so the ring + tick drives resolve it, and register
		// the empty bellBE in t.bells. CITE BellBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:bell"], empty, dimMinY)
		t.resolveBell(pos)
		return
	}
	if block.IsLectern(state) {
		// LecternBlock is a BaseEntityBlock; newBlockEntity = new LecternBlockEntity(pos, state) (empty: no
		// book). Write an empty BE compound so the place-book + comparator paths resolve it, and register the
		// empty lecternBE in t.lecterns. CITE LecternBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:lectern"], empty, dimMinY)
		t.resolveLectern(pos)
		return
	}
	if block.IsChiseledBookshelf(state) {
		// ChiseledBookShelfBlock is a BaseEntityBlock; newBlockEntity = new ChiseledBookShelfBlockEntity(
		// pos, state) (empty: 6 clear slots, lastInteractedSlot -1). Write an empty BE compound so the
		// add/remove-book + comparator paths resolve it, and register the empty chiseledBookshelfBE in
		// t.bookshelves. CITE ChiseledBookShelfBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:chiseled_bookshelf"], empty, dimMinY)
		t.resolveChiseledBookshelf(pos)
		return
	}
	if block.IsJukebox(state) {
		// JukeboxBlock is a BaseEntityBlock; newBlockEntity = new JukeboxBlockEntity(pos, state) (empty: no
		// disc). Write an empty BE compound so the insert-disc + comparator paths resolve it, and register
		// the empty jukeboxBE in t.jukeboxes. CITE JukeboxBlock (EntityBlock).
		empty := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
		t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:jukebox"], empty, dimMinY)
		t.resolveJukebox(pos)
		return
	}
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
		// ChestBlockEntity.loadAdditional: tryLoadLootTable XOR loadAllItems. A BE that still carries a
		// LootTable is an UN-rolled chest — it opens by rolling later (unpackLootTable), so it needs no
		// Items decode. A BE with NO LootTable (a rolled/edited chest saved via saveAllItems) has its
		// container in the "Items" list, which decodeChestItems reads back so a reloaded rolled chest
		// keeps its contents. CITE ChestBlockEntity.loadAdditional (if !tryLoadLootTable loadAllItems).
		cl := &chestLoot{LootTable: nbtData.LootTable, LootTableSeed: nbtData.LootTableSeed}
		if nbtData.LootTable == "" {
			cl.items = decodeChestItems(be.Data)
		}
		return cl
	}
	return nil
}

// decodeChestItems decodes a chest BlockEntity.Data compound's "Items" list back into the 27-slot
// []component.SlotData container (ContainerHelper.loadAllItems). Each present ItemStackWithSlot's disk id
// ("minecraft:<name>") resolves to the numeric wire item id; out-of-range slots are dropped by
// save.LoadAllItems (isValidInContainer). A compound with no "Items" key yields an all-empty container
// (listOrEmpty tolerance). Phase-A: only id+count round-trip (components were dropped on save).
//
// CITE: ContainerHelper.loadAllItems (temp/cache/26.2-inner.jar).
func decodeChestItems(data nbt.RawMessage) []component.SlotData {
	loaded, err := save.LoadItemsCompound(data, chestContainerSize)
	if err != nil {
		return nil // garbled Items list: open empty (never panic) — ensureContainer pads to 27
	}
	out := make([]component.SlotData, chestContainerSize)
	for i := 0; i < chestContainerSize && i < len(loaded); i++ {
		it := loaded[i]
		if it.IsEmpty() {
			continue
		}
		out[i] = component.SlotData{
			ItemID:        toItemID(int(itemNameToID(it.ID))),
			Count:         toVar(int(it.Count)),
			AddedCount:    toVar(it.WireAddedCount),   // Phase B: rebuilt from the disk components compound
			RemovedCount:  toVar(it.WireRemovedCount), //
			RawComponents: it.WireComponents,          //
		}
	}
	return out
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
	inv.incrementStateId()
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		chestMenuItems(cl, inv), inv.getCarried()))
}

// broadcastChestChange re-sends the authoritative content to any player whose open window is the chest
// at pos — the observable equivalent of ChestBlockEntity.setChanged when a hopper pushes/pulls an item
// through the chest (a viewer must see the slot change). A chest nobody is viewing changes silently (its
// state is still authoritative in the tick-owned chestLoot). Mirrors broadcastDispenserChange. Tick-owned.
func (t *TickLoop) broadcastChestChange(pos pk.Position, cl *chestLoot) {
	for _, p := range t.players {
		if p == nil || p.openContainer == nil {
			continue
		}
		if p.openContainer.kind == containerKindChest && p.openContainer.chestPos == pos {
			t.sendChestContent(p, cl)
		}
	}
}
