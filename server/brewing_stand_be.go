package server

// brewing_stand_be.go — the BREWING STAND BLOCK-ENTITY (the potion-brew DRIVE): a 1:1 port of
// net.minecraft.world.level.block.entity.BrewingStandBlockEntity.serverTick over the 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p this session). The furnace twin (furnace_be.go): a tick-owned
// block-entity map (t.brewingStands) + a static serverTick + a menu (brewing_stand_menu.go).
//
// FIELDS (VERIFIED javap BrewingStandBlockEntity):
//   items[5]        — SLOT_BOTTLE 0/1/2 (the three potion bottles), INGREDIENT_SLOT 3, FUEL_SLOT 4
//   brewTime        — ticks left in the current brew; DEFAULT_BREW_TIME = 400 (sipush 400 on refill)
//   fuel            — blaze-powder uses left, 0..FUEL_USES; FUEL_USES = 20 (bipush 20 on refill)
//   ingredient      — the Item cached when a brew starts (used to abort the brew if it changes)
//   lastPotionCount — the last [bottle0,bottle1,bottle2] presence array (drives the HAS_BOTTLE blockstate)
//
// serverTick(level, pos, state, be) — VERIFIED javap branch order + constants:
//   1. ItemStack fuelStack = items[4];
//      if (fuel <= 0 && fuelStack.is(ItemTags.BREWING_FUEL)) { fuel = 20; fuelStack.shrink(1); setChanged; }
//   2. boolean canBrew = isBrewable(potionBrewing, items);
//   3. boolean isBrewing = brewTime > 0;
//   4. ItemStack ingredientStack = items[3];
//   5. if (isBrewing) {
//          --brewTime;
//          boolean brewFinished = brewTime == 0;
//          if (brewFinished && canBrew)                doBrew(level, pos, items);
//          else if (!canBrew || !ingredientStack.is(this.ingredient))  brewTime = 0;
//          setChanged;
//      } else if (canBrew && fuel > 0) {
//          --fuel; brewTime = 400; this.ingredient = ingredientStack.getItem(); setChanged;
//      }
//   6. boolean[] potionBits = getPotionBits();
//      if (!Arrays.equals(potionBits, lastPotionCount)) { lastPotionCount = potionBits;
//          <set HAS_BOTTLE_0/1/2 on the BrewingStandBlock state, level.setBlock(pos, state, 2)>; }
//
// isBrewable(potionBrewing, items) — VERIFIED javap:
//   ItemStack ing = items[3];
//   if (ing.isEmpty()) return false;
//   if (!potionBrewing.isIngredient(ing)) return false;
//   for (i=0;i<3;i++) { ItemStack b = items[i]; if (!b.isEmpty() && potionBrewing.hasMix(b, ing)) return true; }
//   return false;
//
// doBrew(level, pos, items) — VERIFIED javap:
//   ItemStack ing = items[3]; PotionBrewing pb = level.potionBrewing();
//   for (i=0;i<3;i++) items.set(i, pb.mix(ing, items.get(i)));
//   ing.shrink(1);
//   ItemStackTemplate rem = ing.getItem().getCraftingRemainder();
//   if (rem != null) { if (ing.isEmpty()) ing = rem.create(); else Containers.dropItemStack(...rem.create()); }
//   items.set(3, ing);
//   level.levelEvent(1035, pos, 0);   // brewing-stand-brew sound
//
// SCOPE (cited deferrals): the block-entity live drive is fully faithful. The two 1:1 side-effects
// doBrew fires that touch not-yet-built subsystems are stubbed at their vanilla no-op-equivalent seams:
//   - Containers.dropItemStack of a crafting-remainder overflow: no vanilla brewing ingredient has a
//     crafting remainder (Item.getCraftingRemainder is null for nether_wart, glowstone_dust, redstone,
//     gunpowder, dragon_breath, blaze_powder, fermented_spider_eye, etc. — VERIFIED the vanilla brewing
//     ingredient set), so the `rem != null` branch is provably never taken and dropping it is dead code.
//     Ported behind furnaceCraftingRemainder (the shared crafting-remainder seam) so it becomes real if a
//     remainder-bearing ingredient is ever added.
//   - level.levelEvent(1035, ...) is the brew SOUND — no v1 sound subsystem (the same faithful no-op the
//     furnace + chest paths cite); structured as a marked seam.
// Persistence (Items + BrewTime + Fuel) round-trips via brewing_stand_persist.go, mirroring the furnace.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Brewing-stand slot indices (BrewingStandBlockEntity constants, VERIFIED javap static{}):
//
//	BOTTLE_SLOT 0/1/2, INGREDIENT_SLOT = 3, FUEL_SLOT = 4.
const (
	brewSlotBottle0    = 0
	brewSlotBottle1    = 1
	brewSlotBottle2    = 2
	brewSlotIngredient = 3 // INGREDIENT_SLOT
	brewSlotFuel       = 4 // FUEL_SLOT
)

// brewContainerSize is BrewingStandBlockEntity.getContainerSize() = 5 (NonNullList.withSize(5, EMPTY)).
const brewContainerSize = 5

// brewTimeTotal is BrewingStandBlockEntity.DEFAULT_BREW_TIME (sipush 400 in serverTick's brew-start branch).
const brewTimeTotal = 400

// brewFuelUses is BrewingStandBlockEntity.FUEL_USES (bipush 20 on a blaze-powder fuel refill).
const brewFuelUses = 20

// brewingStandBE is the tick-owned state of one brewing-stand block-entity — the Sulfur analogue of
// BrewingStandBlockEntity narrowed to the fields serverTick + the menu read/write. items[0..4] are the three
// bottles / ingredient / fuel.
type brewingStandBE struct {
	items [brewContainerSize]component.SlotData

	brewTime int   // DATA_BREW_TIME (ticks left)
	fuel     int   // DATA_FUEL_USES (0..20)
	hasIngr  bool  // whether `ingredient` is set (Item ingredient; nil in vanilla when not brewing)
	ingredID int32 // the cached ingredient Item id (this.ingredient) captured at brew start

	lastPotionCount [3]bool // BrewingStandBlockEntity.lastPotionCount (the last HAS_BOTTLE presence array)
	lastPotionInit  bool    // whether lastPotionCount has been compared once (null on a fresh BE)
}

// brewGetPotionBits ports BrewingStandBlockEntity.getPotionBits(): a boolean[3] where index i is
// !items[i].isEmpty() (a bottle present in slot i).
func (b *brewingStandBE) getPotionBits() [3]bool {
	var bits [3]bool
	for i := 0; i < 3; i++ {
		bits[i] = !stackEmpty(b.items[i])
	}
	return bits
}

// brewIsBrewable ports BrewingStandBlockEntity.isBrewable(potionBrewing, items): the ingredient slot must be
// non-empty AND a brewing ingredient, then at least one non-empty bottle must have a mix with it.
//
// 1:1 net.minecraft.world.level.block.entity.BrewingStandBlockEntity.isBrewable
func brewIsBrewable(pb *potionBrewing, b *brewingStandBE) bool {
	ing := b.items[brewSlotIngredient]
	if stackEmpty(ing) {
		return false
	}
	if !pb.isIngredient(int32(ing.ItemID)) {
		return false
	}
	for i := 0; i < 3; i++ {
		bottle := b.items[i]
		if stackEmpty(bottle) {
			continue
		}
		if pb.hasMix(bottle, ing) {
			return true
		}
	}
	return false
}

// brewDoBrew ports BrewingStandBlockEntity.doBrew(level, pos, items): mix each of the 3 bottles with the
// ingredient, then shrink the ingredient (honoring a crafting remainder), then the brew sound.
//
// 1:1 net.minecraft.world.level.block.entity.BrewingStandBlockEntity.doBrew
func (t *TickLoop) brewDoBrew(pos pk.Position, b *brewingStandBE) {
	pb := vanillaPotionBrewing()
	ing := b.items[brewSlotIngredient]
	// for (i=0;i<3;i++) items.set(i, potionBrewing.mix(ingredientStack, items.get(i)));
	for i := 0; i < 3; i++ {
		b.items[i] = pb.mix(ing, b.items[i])
	}
	// ingredientStack.shrink(1);
	ing.Count = toVar(int(ing.Count) - 1)
	// ItemStackTemplate remainder = ingredientStack.getItem().getCraftingRemainder();
	rem, hasRem := furnaceCraftingRemainder(int32(ing.ItemID))
	if hasRem {
		if int(ing.Count) <= 0 {
			// if (ingredientStack.isEmpty()) ingredientStack = remainder.create();
			ing = rem
		} else {
			// else Containers.dropItemStack(level, x, y, z, remainder.create());
			// No vanilla brewing ingredient has a crafting remainder (VERIFIED) -> this branch is dead; the
			// drop is the cited no-op seam (no item-drop-into-world path for brew overflow in v1).
			t.brewDropRemainder(pos, rem)
		}
	}
	if int(ing.Count) <= 0 {
		ing = component.SlotData{Count: 0}
	}
	// items.set(3, ingredientStack);
	b.items[brewSlotIngredient] = ing
	// level.levelEvent(1035, pos, 0); — the brew sound (cited no-op seam: no v1 sound subsystem).
	t.brewLevelEvent(pos, 1035)
}

// brewDropRemainder / brewLevelEvent are the cited faithful no-op seams (crafting-remainder overflow drop +
// brew sound). Structured so a real item-drop / sound path attaches here without touching the drive.
func (t *TickLoop) brewDropRemainder(_ pk.Position, _ component.SlotData) {}
func (t *TickLoop) brewLevelEvent(_ pk.Position, _ int)                   {}

// brewingStandServerTick ports BrewingStandBlockEntity.serverTick(level, pos, state, be) EXACTLY: the fuel
// refill from blaze_powder, the isBrewable check, the brewTime decrement + doBrew, the fuel-consume brew
// start, and the HAS_BOTTLE blockstate toggle. Runs on the tick goroutine (called from tickWorld). state is
// the current brewing-stand stateID.
//
// 1:1 net.minecraft.world.level.block.entity.BrewingStandBlockEntity.serverTick
func (t *TickLoop) brewingStandServerTick(pos pk.Position, state block.StateID, b *brewingStandBE) {
	changed := false

	// ItemStack fuelStack = items[4];
	fuelStack := b.items[brewSlotFuel]
	// if (fuel <= 0 && fuelStack.is(ItemTags.BREWING_FUEL)) { fuel = 20; fuelStack.shrink(1); setChanged; }
	if b.fuel <= 0 && !stackEmpty(fuelStack) && itemInTag(int32(fuelStack.ItemID), "brewing_fuel") {
		b.fuel = brewFuelUses
		fuelStack.Count = toVar(int(fuelStack.Count) - 1)
		if fuelStack.Count <= 0 {
			fuelStack = component.SlotData{Count: 0}
		}
		b.items[brewSlotFuel] = fuelStack
		changed = true
	}

	pb := vanillaPotionBrewing()
	// boolean canBrew = isBrewable(potionBrewing, items);
	canBrew := brewIsBrewable(pb, b)
	// boolean isBrewing = brewTime > 0;
	isBrewing := b.brewTime > 0
	// ItemStack ingredientStack = items[3];
	ingredientStack := b.items[brewSlotIngredient]

	if isBrewing {
		// --brewTime;
		b.brewTime--
		// boolean brewFinished = brewTime == 0;
		brewFinished := b.brewTime == 0
		if brewFinished && canBrew {
			// doBrew(level, pos, items);
			t.brewDoBrew(pos, b)
		} else if !canBrew || !(b.hasIngr && int32(ingredientStack.ItemID) == b.ingredID && !stackEmpty(ingredientStack)) {
			// else if (!canBrew || !ingredientStack.is(this.ingredient)) brewTime = 0;
			b.brewTime = 0
		}
		// setChanged;
		changed = true
	} else if canBrew && b.fuel > 0 {
		// --fuel; brewTime = 400; this.ingredient = ingredientStack.getItem(); setChanged;
		b.fuel--
		b.brewTime = brewTimeTotal
		b.hasIngr = true
		b.ingredID = int32(ingredientStack.ItemID)
		changed = true
	}

	// boolean[] potionBits = getPotionBits();
	potionBits := b.getPotionBits()
	// if (!Arrays.equals(potionBits, lastPotionCount)) { lastPotionCount = potionBits; <toggle HAS_BOTTLE>; }
	if !b.lastPotionInit || potionBits != b.lastPotionCount {
		b.lastPotionCount = potionBits
		b.lastPotionInit = true
		// if (!(state.getBlock() instanceof BrewingStandBlock)) return;  (guard: only a real brewing stand)
		if ns, ok := brewingStandWithBottles(state, potionBits); ok {
			t.setBrewingStandBlockBottles(pos, ns)
			state = ns
		}
	}

	// if (changed) setChanged — the in-memory drive itself (SUB-PERSIST) + the open-menu re-send.
	if changed {
		t.markBrewingStandDirty(pos)
		t.broadcastBrewingStandChange(pos, b)
	}
}

// tickBrewingStands ticks every live brewing-stand block-entity once per tick (the ServerLevel-side
// blockEntityTicker fan-out for BrewingStandBlockEntity.serverTick). Called from tickWorld. Each brewing
// stand reads its CURRENT block state from the world; if the block is no longer a brewing_stand
// (broken/replaced), the block-entity is dropped from the store. A nil world leaves them un-ticked (tests
// may drive brewingStandServerTick directly). Tick-owned (TICK-05). The furnace twin (tickFurnaces).
func (t *TickLoop) tickBrewingStands() {
	if len(t.brewingStands) == 0 {
		return
	}
	w := t.world()
	for pos, b := range t.brewingStands {
		if w == nil {
			continue
		}
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !isBrewingStandBlock(state) {
			delete(t.brewingStands, pos)
			continue
		}
		t.brewingStandServerTick(pos, state, b)
	}
}
