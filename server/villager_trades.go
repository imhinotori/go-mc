package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
)

// villager_trades.go — the villager TRADE OFFER model (net.minecraft.world.item.trading.MerchantOffer /
// MerchantOffers) plus a faithful slice of the FARMER trade DATA. A LITERAL port of the unobfuscated 26.2
// jar (temp/cache/26.2-inner.jar, javap -c -p / CFR this session).
//
// SCOPE: this file lands the OFFER DATA MODEL + a few concrete offers + the villager-side TRADING STATE
// (getOffers lazy build, setTradingPlayer, isTrading). The merchant MENU itself (mobInteract -> startTrading
// -> ClientboundMerchantOffers -> the container-menu click/take engine) lands in merchant_menu.go +
// merchant_click.go (mirroring the chest/crafting menu subsystems). The gossip/reputation price economy
// (specialPriceDiff/demand ADJUSTMENT via gossip; TRADE reputation events) remains DEFERRED — the menu emits
// the CURRENT offer numbers (specialPriceDiff/demand as stored, default 0). Cite MerchantOffer.updateDemand
// + Villager.updateSpecialPrices as the future price adjusters. Cite Villager.mobInteract / MerchantOffer /
// VillagerTrades.
//
//	[VERIFIED javap MerchantOffer: fields baseCostA(ItemCost), costB(Optional<ItemCost>), result(ItemStack),
//	 uses(int), maxUses(int), rewardExp(bool), specialPriceDiff(int), demand(int), priceMultiplier(float),
//	 xp(int). VERIFIED base datapack data/minecraft/villager_trade/farmer/1/*.json: EmeraldForItems +
//	 ItemsForEmeralds with the counts below, max_uses 16, reputation_discount 0.05f (== priceMultiplier).]

// itemCost is net.minecraft.world.item.trading.ItemCost (the modern replacement for a plain ItemStack cost):
// an item + a required count. costB is optional (an Optional<ItemCost>), modeled as a nil pointer.
type itemCost struct {
	item  item.Item
	count int
}

// itemStackResult is the offer's result (an ItemStack: item + count).
type itemStackResult struct {
	item  item.Item
	count int
}

// merchantOffer ports net.minecraft.world.item.trading.MerchantOffer — a single trade. The numeric fields
// are literal from the jar. uses/specialPriceDiff/demand are the mutable per-offer state (start at 0).
type merchantOffer struct {
	baseCostA        itemCost
	costB            *itemCost // Optional<ItemCost>: nil == Optional.empty()
	result           itemStackResult
	uses             int
	maxUses          int
	rewardExp        bool
	specialPriceDiff int
	demand           int
	priceMultiplier  float32
	xp               int
}

// merchantOffers ports net.minecraft.world.item.trading.MerchantOffers (a List<MerchantOffer>). isEmpty is
// the Villager.mobInteract getOffers().isEmpty() gate.
type merchantOffers []*merchantOffer

func (o merchantOffers) isEmpty() bool { return len(o) == 0 }

// getRecipeFor ports net.minecraft.world.item.trading.MerchantOffers.getRecipeFor(buyA, buyB,
// selectionHint): return the INDEX of the first offer satisfiedBy (buyA, buyB), or -1 if none. When
// selectionHint is a valid index (0 < hint < size), only that offer is considered (the client's picked
// trade). Otherwise scan in order. v1 returns the index (not a pointer) so the caller re-resolves the live
// offer through the same list. NOTE the vanilla `selectionHint > 0` guard: hint 0 falls through to the scan
// (index 0 is reachable only via the scan, exactly as vanilla).
//
//	[VERIFIED CFR MerchantOffers.getRecipeFor: if (hint>0 && hint<size) { offer=get(hint); return
//	 offer.satisfiedBy(a,b) ? offer : null }; for i: if get(i).satisfiedBy(a,b) return get(i); return null.]
func (o merchantOffers) getRecipeFor(buyA, buyB component.SlotData, selectionHint int) int {
	if selectionHint > 0 && selectionHint < len(o) {
		if o[selectionHint].satisfiedBy(buyA, buyB) {
			return selectionHint
		}
		return -1
	}
	for i := range o {
		if o[i].satisfiedBy(buyA, buyB) {
			return i
		}
	}
	return -1
}

// getModifiedCostCount ports MerchantOffer.getModifiedCostCount(cost): the demand-adjusted cost count.
//
//	basePrice = cost.count()
//	demandDiff = max(0, Mth.floor((float)(basePrice * demand) * priceMultiplier))
//	return Mth.clamp(basePrice + demandDiff + specialPriceDiff, 1, cost.itemStack().getMaxStackSize())
//
// With v1's default demand==0 and specialPriceDiff==0 this reduces to clamp(basePrice, 1, maxStack) ==
// basePrice for a valid offer — the price economy is deferred (updateDemand/updateSpecialPrices), so the
// numeric path is preserved but currently yields the base count. CITE MerchantOffer.getModifiedCostCount.
func (m *merchantOffer) getModifiedCostCount(cost itemCost) int {
	basePrice := cost.count
	// Mth.floor((float)(basePrice*demand) * priceMultiplier): the float32 product, then Mth.floor(float)
	// == (int)Math.floor. Compute in float32 to mirror the jar's single-precision arithmetic exactly.
	prod := float32(basePrice*m.demand) * m.priceMultiplier
	demandDiff := mthFloor(float64(prod))
	if demandDiff < 0 {
		demandDiff = 0
	}
	// Mth.clamp(basePrice + demandDiff + specialPriceDiff, 1, cost.itemStack().getMaxStackSize()). Reuses the
	// existing int32 mthClampI (food.go) — the jar's clamp bounds are 1..maxStackSize.
	return int(mthClampI(int32(basePrice+demandDiff+m.specialPriceDiff), 1, int32(cost.item.StackSize)))
}

// satisfiedBy ports MerchantOffer.satisfiedBy(buyA, buyB): the payment slots satisfy this offer.
//
//	if (!baseCostA.test(buyA) || buyA.getCount() < getModifiedCostCount(baseCostA)) return false
//	if (costB.isPresent()) return costB.get().test(buyB) && buyB.getCount() >= costB.get().count()
//	return buyB.isEmpty()
//
// ItemCost.test(stack) is stack.is(item) (v1: same item id; the EMPTY component predicate always passes).
// CITE MerchantOffer.satisfiedBy + ItemCost.test.
func (m *merchantOffer) satisfiedBy(buyA, buyB component.SlotData) bool {
	if !m.baseCostA.test(buyA) || int(buyA.Count) < m.getModifiedCostCount(m.baseCostA) {
		return false
	}
	if m.costB != nil {
		return m.costB.test(buyB) && int(buyB.Count) >= m.costB.count
	}
	return stackEmpty(buyB)
}

// test ports net.minecraft.world.item.trading.ItemCost.test(stack): stack.is(item) && components.test.
// v1: same item id + the EMPTY component predicate (always passes). CITE ItemCost.test.
func (c itemCost) test(stack component.SlotData) bool {
	return !stackEmpty(stack) && int(stack.ItemID) == int(c.item.ID)
}

// take ports MerchantOffer.take(buyA, buyB): if satisfiedBy, shrink buyA by getCostA().count and buyB by
// getCostB().count and return true, else false. getCostA() == baseCostA.copyWithCount(getModifiedCostCount).
// v1's demand/specialPrice defaults make getCostA().count == baseCostA.count. The stacks are mutated IN
// PLACE (pointer receivers) — the caller writes them back to the payment container.
//
//	buyA.shrink(getCostA().getCount()); if (!getCostB().isEmpty()) buyB.shrink(getCostB().getCount())
//
//	[VERIFIED CFR MerchantOffer.take: if (!satisfiedBy(a,b)) return false; a.shrink(getCostA().count);
//	 if (!getCostB().isEmpty()) b.shrink(getCostB().count); return true.]
func (m *merchantOffer) take(buyA, buyB *component.SlotData) bool {
	if !m.satisfiedBy(*buyA, *buyB) {
		return false
	}
	shrinkStack(buyA, m.getModifiedCostCount(m.baseCostA)) // buyA.shrink(getCostA().count)
	if m.costB != nil {
		shrinkStack(buyB, m.costB.count) // buyB.shrink(getCostB().count)
	}
	return true
}

// increaseUses ports MerchantOffer.increaseUses(): ++uses. isOutOfStock == uses >= maxUses.
func (m *merchantOffer) increaseUses() { m.uses++ }

// updateDemand ports net.minecraft.world.item.trading.MerchantOffer.updateDemand(): the exponential-ish
// demand accumulator. Run once per restock (Villager.updateDemand -> for each offer offer.updateDemand()):
//
//	this.demand = this.demand + this.uses - (this.maxUses - this.uses)
//
// A heavily-used offer (uses near maxUses) RAISES demand (making it costlier via getModifiedCostCount's
// demandDiff); an unused offer (uses 0) LOWERS demand by maxUses. CITE MerchantOffer.updateDemand.
//
//	[VERIFIED CFR MerchantOffer.updateDemand: this.demand = this.demand + this.uses - (this.maxUses - this.uses).]
func (m *merchantOffer) updateDemand() {
	m.demand = m.demand + m.uses - (m.maxUses - m.uses)
}

// addToSpecialPriceDiff ports MerchantOffer.addToSpecialPriceDiff(add): specialPriceDiff += add. The single
// mutator Villager.updateSpecialPrices funnels the reputation/hero discount through (add is NEGATIVE for a
// discount). CITE MerchantOffer.addToSpecialPriceDiff.
func (m *merchantOffer) addToSpecialPriceDiff(add int) { m.specialPriceDiff += add }

// resetSpecialPriceDiff ports MerchantOffer.resetSpecialPriceDiff(): specialPriceDiff = 0. Called by
// Villager.resetSpecialPrices when trading STOPS (so the discount is recomputed fresh on the next open).
// CITE MerchantOffer.resetSpecialPriceDiff.
func (m *merchantOffer) resetSpecialPriceDiff() { m.specialPriceDiff = 0 }

// getPriceMultiplier ports MerchantOffer.getPriceMultiplier(): the reputation/demand discount factor
// (0.05f for the farmer trades). CITE MerchantOffer.getPriceMultiplier.
func (m *merchantOffer) getPriceMultiplier() float32 { return m.priceMultiplier }

// isOutOfStock ports MerchantOffer.isOutOfStock(): uses >= maxUses.
func (m *merchantOffer) isOutOfStock() bool { return m.uses >= m.maxUses }

// needsRestock ports MerchantOffer.needsRestock(): uses > 0 (any depletion at all). Villager.needsToRestock
// returns true if ANY offer needsRestock. CITE MerchantOffer.needsRestock (return this.uses > 0).
func (m *merchantOffer) needsRestock() bool { return m.uses > 0 }

// shrinkStack ports ItemStack.shrink(n): reduce the count by n, clearing to empty at <= 0.
func shrinkStack(s *component.SlotData, n int) {
	s.Count -= toVar(n)
	if s.Count <= 0 {
		*s = component.SlotData{Count: 0}
	}
}

// newEmeraldForItems builds an "N items -> 1 emerald" offer (VillagerTrades' EmeraldForItems shape):
// baseCostA = the crop, result = 1 emerald, priceMultiplier 0.05, rewardExp true. VERIFIED
// data/minecraft/villager_trade/farmer/1/<crop>_emerald.json.
func newEmeraldForItems(costItem item.Item, costCount, maxUses, xp int) *merchantOffer {
	return &merchantOffer{
		baseCostA:       itemCost{item: costItem, count: costCount},
		result:          itemStackResult{item: item.Emerald, count: 1},
		maxUses:         maxUses,
		rewardExp:       true,
		priceMultiplier: 0.05,
		xp:              xp,
	}
}

// newItemsForEmerald builds an "1 emerald -> M items" offer (VillagerTrades' ItemsForEmeralds shape):
// baseCostA = 1 emerald, result = M of the item. VERIFIED data/.../farmer/1/emerald_bread.json.
func newItemsForEmerald(emeralds int, resultItem item.Item, resultCount, maxUses, xp int) *merchantOffer {
	return &merchantOffer{
		baseCostA:       itemCost{item: item.Emerald, count: emeralds},
		result:          itemStackResult{item: resultItem, count: resultCount},
		maxUses:         maxUses,
		rewardExp:       xp > 0,
		priceMultiplier: 0.05,
		xp:              xp,
	}
}

// The base-datapack trades are data-driven VillagerTrade records (net.minecraft.world.item.trading.
// VillagerTrade), each { wants(TradeCost), additionalWants(Optional TradeCost), gives(ItemStackTemplate),
// maxUses, xp, reputationDiscount, givenItemModifiers, doubleTradePriceEnchantments }. VillagerTrade.getOffer
// builds a MerchantOffer as (VERIFIED javap net.minecraft.world.item.trading.VillagerTrade.getOffer):
//
//	stack = gives.create(); additional = 0
//	for fn in givenItemModifiers: stack = fn.apply(stack, ctx); if stack.isEmpty() return null
//	additional += stack.remove(ADDITIONAL_TRADE_COST)
//	if doubleTradePriceEnchantments matches a STORED_ENCHANTMENTS key: additional *= 2
//	baseCostA = wants.toItemCost(ctx, additional)  // count = wants.count + additional
//	if baseCostA.count < 1 return null
//	costB = additionalWants.map(tc to tc.toItemCost(ctx, 0)); if present and count < 1 return null
//	return new MerchantOffer(baseCostA, costB, stack, max(maxUses,1), max(xp,0), max(reputationDiscount,0))
//
// Each helper builds one MerchantOffer from a single VillagerTrade record constant data (the datapack
// numbers). The item-modifier DECORATIONS (enchant RNG, exploration-map target, dyed-armor color,
// tipped-arrow potion, stew effect) are DEFERRED behind their not-yet-built subsystems (cited per helper);
// the offer ECONOMICS (cost/costB/result-item/maxUses/xp/discount) are literal from the datapack.

// newEmeraldForItemsN builds an "N items -> 1 emerald" trade (wants=item xN, gives=emerald x1). The
// discount-parameterized form of newEmeraldForItems. rewardExp is xp>0. CITE VillagerTrade { wants:item }.
func newEmeraldForItemsN(costItem item.Item, costCount, maxUses, xp int, discount float32) *merchantOffer {
	return &merchantOffer{
		baseCostA:       itemCost{item: costItem, count: costCount},
		result:          itemStackResult{item: item.Emerald, count: 1},
		maxUses:         maxUses,
		rewardExp:       true,
		priceMultiplier: discount,
		xp:              xp,
	}
}

// newItemsForEmeraldN builds an "E emerald -> M items" trade (wants=emerald xE, gives=item xM). The
// discount-parameterized form of newItemsForEmerald. rewardExp is xp>0. CITE VillagerTrade { gives:item }.
func newItemsForEmeraldN(emeralds int, resultItem item.Item, resultCount, maxUses, xp int, discount float32) *merchantOffer {
	return &merchantOffer{
		baseCostA:       itemCost{item: item.Emerald, count: emeralds},
		result:          itemStackResult{item: resultItem, count: resultCount},
		maxUses:         maxUses,
		rewardExp:       xp > 0,
		priceMultiplier: discount,
		xp:              xp,
	}
}

// newProcessItemsForEmerald builds a two-input trade: PRIMARY cost = raw material, SECONDARY cost
// (additional_wants -> costB) = emeralds or a second item, giving the processed result (e.g. raw_cod x6 +
// emerald x1 -> cooked_cod x6; emerald x2 + arrow x5 -> tipped_arrow x5). baseCostA is the datapack wants;
// costB is the additional_wants. CITE VillagerTrade with additionalWants (getOffer costB =
// additionalWants.toItemCost). tipped_arrow set_random_potion result decoration DEFERRED (potion component).
func newProcessItemsForEmerald(costItem item.Item, costCount int, costBItem item.Item, costBCount int, resultItem item.Item, resultCount, maxUses, xp int, discount float32) *merchantOffer {
	return &merchantOffer{
		baseCostA:       itemCost{item: costItem, count: costCount},
		costB:           &itemCost{item: costBItem, count: costBCount},
		result:          itemStackResult{item: resultItem, count: resultCount},
		maxUses:         maxUses,
		rewardExp:       xp > 0,
		priceMultiplier: discount,
		xp:              xp,
	}
}

// newTradeItem builds a plain item-for-item trade (neither side emerald) -- the generic VillagerTrade record
// shape (faithful fallback for a { wants:itemA, gives:itemB } record). CITE VillagerTrade { wants, gives }.
func newTradeItem(costItem item.Item, costCount int, resultItem item.Item, resultCount, maxUses, xp int, discount float32) *merchantOffer {
	return &merchantOffer{
		baseCostA:       itemCost{item: costItem, count: costCount},
		result:          itemStackResult{item: resultItem, count: resultCount},
		maxUses:         maxUses,
		rewardExp:       xp > 0,
		priceMultiplier: discount,
		xp:              xp,
	}
}

// newEnchantedItemForEmeralds builds an "E emerald -> 1 randomly-enchanted tool/armor" trade (the
// enchant_with_levels listing: iron/diamond tools, diamond armor, enchanted bow/crossbow/fishing rod). The
// datapack wants emerald count is the BASE price; getOffer ADDS the enchant ADDITIONAL_TRADE_COST component
// (see enchantAdditionalCost). The enchantment subsystem (enchant_with_levels uniform[5,19] level roll) is
// NOT built in v1, so the ADDITIONAL cost RNG and the enchant on the result are DEFERRED -- the offer carries
// the BASE emerald price + plain tool result, structured so a real roll adds enchantAdditionalCost to
// baseCostA.count and enchants the result with no caller change. CITE VillagerTrade.getOffer
// (ADDITIONAL_TRADE_COST) + EnchantWithLevelsFunction.
func newEnchantedItemForEmeralds(resultItem item.Item, emeralds, maxUses, xp int, discount float32) *merchantOffer {
	return &merchantOffer{
		baseCostA:       itemCost{item: item.Emerald, count: emeralds},
		result:          itemStackResult{item: resultItem, count: 1},
		maxUses:         maxUses,
		rewardExp:       xp > 0,
		priceMultiplier: discount,
		xp:              xp,
	}
}

// newEnchantedBookForEmeralds builds the librarian "emerald + book -> 1 enchanted book" trade (the
// enchant_randomly listing). baseCostA is emerald with the datapack wants count (0 in the base data) -- the
// REAL price comes entirely from the enchant ADDITIONAL_TRADE_COST component (getOffer: baseCostA.count =
// wants.count + additional). costB is the additional_wants book x1. The classic enchanted-book emerald price
// (2 + random.nextInt(5 + level*10) for the rolled level, DOUBLED when the rolled enchantment is in the
// double_trade_price tag -- this listing sets doubleTradePriceEnchantments) is DEFERRED behind the
// enchantment subsystem. The offer carries the BASE emerald count + book costB + enchanted_book result, so a
// real roll adds the additional cost to baseCostA.count with no caller change. maxUses 12, discount 0.2.
// CITE VillagerTrade.getOffer (ADDITIONAL_TRADE_COST + doubleTradePriceEnchantments) + EnchantRandomlyFunction.
func newEnchantedBookForEmeralds(baseEmeralds, maxUses, xp int, discount float32) *merchantOffer {
	return &merchantOffer{
		baseCostA:       itemCost{item: item.Emerald, count: baseEmeralds},
		costB:           &itemCost{item: item.Book, count: 1},
		result:          itemStackResult{item: item.EnchantedBook, count: 1},
		maxUses:         maxUses,
		rewardExp:       xp > 0,
		priceMultiplier: discount,
		xp:              xp,
	}
}

// enchantAdditionalCost documents the DEFERRED enchant-price component (getOffer ADDITIONAL_TRADE_COST) for
// the enchant_randomly / enchant_with_levels listings. The classic enchanted-book price is additional = 2 +
// random.nextInt(5 + enchantLevel*10) for the rolled enchantment level, DOUBLED when the rolled enchantment
// is in the double_trade_price tag (the librarian book listing doubleTradePriceEnchantments). The
// enchant_with_levels tools roll an experience-level cost (uniform[5,19]) that becomes the additional cost.
// The enchantment subsystem is not built, so this returns 0 -- a cited stub equal to "no enchant rolled yet",
// structured so a real roll replaces it (baseCostA.count += enchantAdditionalCost) with no caller change
// (CLAUDE.md: never bake the value away). CITE VillagerTrade.getOffer + EnchantRandomlyFunction.
func enchantAdditionalCost(_ *entityRandom, _ int) int { return 0 }

// farmerLevel1Offers ports the FARMER level-1 (NOVICE) trade set, DATA-verified from the jar's base
// villager_trade datapack (data/minecraft/villager_trade/farmer/1/*.json this session).
//
//	[VERIFIED base datapack farmer/1:
//	  wheat_emerald:   wants WHEAT x20   -> Emerald,  max_uses 16, xp 2, discount 0.05
//	  potato_emerald:  wants POTATO x26  -> Emerald,  max_uses 16, xp 2, discount 0.05
//	  carrot_emerald:  wants CARROT x22  -> Emerald,  max_uses 16, xp 2, discount 0.05
//	  beetroot_emerald:wants BEETROOT x15-> Emerald,  max_uses 16, xp 2, discount 0.05
//	  emerald_bread:   wants EMERALD x1  -> Bread x6, max_uses 16, xp 0, discount 0.05]
func farmerLevel1Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItems(item.Wheat, 20, 16, 2),
		newEmeraldForItems(item.Potato, 26, 16, 2),
		newEmeraldForItems(item.Carrot, 22, 16, 2),
		newEmeraldForItems(item.Beetroot, 15, 16, 2),
		newItemsForEmerald(1, item.Bread, 6, 16, 0),
	}
}

// villagerOffersFor returns the offers for a villager's (profession, level). Only FARMER level 1 is landed
// as the concrete sample; every other profession/level returns empty (the full trade tables are deferred
// with the merchant-menu subsystem). VERIFIED VillagerTrades trade-set-by-level structure.
func villagerOffersFor(profession string, level int) merchantOffers {
	switch profession {
	case "farmer":
		switch level {
		case 1:
			return farmerLevel1Offers()
		case 2:
			return farmerLevel2Offers()
		case 3:
			return farmerLevel3Offers()
		case 4:
			return farmerLevel4Offers()
		case 5:
			return farmerLevel5Offers()
		}
	case "fisherman":
		switch level {
		case 1:
			return fishermanLevel1Offers()
		case 2:
			return fishermanLevel2Offers()
		case 3:
			return fishermanLevel3Offers()
		case 4:
			return fishermanLevel4Offers()
		case 5:
			return fishermanLevel5Offers()
		}
	case "shepherd":
		switch level {
		case 1:
			return shepherdLevel1Offers()
		case 2:
			return shepherdLevel2Offers()
		case 3:
			return shepherdLevel3Offers()
		case 4:
			return shepherdLevel4Offers()
		case 5:
			return shepherdLevel5Offers()
		}
	case "fletcher":
		switch level {
		case 1:
			return fletcherLevel1Offers()
		case 2:
			return fletcherLevel2Offers()
		case 3:
			return fletcherLevel3Offers()
		case 4:
			return fletcherLevel4Offers()
		case 5:
			return fletcherLevel5Offers()
		}
	case "librarian":
		switch level {
		case 1:
			return librarianLevel1Offers()
		case 2:
			return librarianLevel2Offers()
		case 3:
			return librarianLevel3Offers()
		case 4:
			return librarianLevel4Offers()
		case 5:
			return librarianLevel5Offers()
		}
	case "cartographer":
		switch level {
		case 1:
			return cartographerLevel1Offers()
		case 2:
			return cartographerLevel2Offers()
		case 3:
			return cartographerLevel3Offers()
		case 4:
			return cartographerLevel4Offers()
		case 5:
			return cartographerLevel5Offers()
		}
	case "cleric":
		switch level {
		case 1:
			return clericLevel1Offers()
		case 2:
			return clericLevel2Offers()
		case 3:
			return clericLevel3Offers()
		case 4:
			return clericLevel4Offers()
		case 5:
			return clericLevel5Offers()
		}
	case "armorer":
		switch level {
		case 1:
			return armorerLevel1Offers()
		case 2:
			return armorerLevel2Offers()
		case 3:
			return armorerLevel3Offers()
		case 4:
			return armorerLevel4Offers()
		case 5:
			return armorerLevel5Offers()
		}
	case "weaponsmith":
		switch level {
		case 1:
			return weaponsmithLevel1Offers()
		case 2:
			return weaponsmithLevel2Offers()
		case 3:
			return weaponsmithLevel3Offers()
		case 4:
			return weaponsmithLevel4Offers()
		case 5:
			return weaponsmithLevel5Offers()
		}
	case "toolsmith":
		switch level {
		case 1:
			return toolsmithLevel1Offers()
		case 2:
			return toolsmithLevel2Offers()
		case 3:
			return toolsmithLevel3Offers()
		case 4:
			return toolsmithLevel4Offers()
		case 5:
			return toolsmithLevel5Offers()
		}
	case "butcher":
		switch level {
		case 1:
			return butcherLevel1Offers()
		case 2:
			return butcherLevel2Offers()
		case 3:
			return butcherLevel3Offers()
		case 4:
			return butcherLevel4Offers()
		case 5:
			return butcherLevel5Offers()
		}
	case "leatherworker":
		switch level {
		case 1:
			return leatherworkerLevel1Offers()
		case 2:
			return leatherworkerLevel2Offers()
		case 3:
			return leatherworkerLevel3Offers()
		case 4:
			return leatherworkerLevel4Offers()
		case 5:
			return leatherworkerLevel5Offers()
		}
	case "mason":
		switch level {
		case 1:
			return masonLevel1Offers()
		case 2:
			return masonLevel2Offers()
		case 3:
			return masonLevel3Offers()
		case 4:
			return masonLevel4Offers()
		case 5:
			return masonLevel5Offers()
		}
	}
	return nil
}

// villagerGetOffers ports net.minecraft.world.entity.npc.villager.AbstractVillager.getOffers: the offers
// list is lazily built ONCE (offers == null -> new MerchantOffers + updateTrades) and then cached on the
// villager, so re-opens serve the SAME live list (with its mutable per-offer uses). The offersBuilt flag is
// the "offers != null" gate (a Go nil slice is a valid EMPTY offers).
//
// Vanilla's updateTrades randomly SELECTS a subset of the profession/level trade set (addOffersFromTradeSet
// -> addOffersFromItemListingsWithoutDuplicates picks `numberOfOffers` at random). v1 supplies the DETERMIN-
// ISTIC concrete set from villagerOffersFor (the landed farmer/1 sample) — a faithful data reduction, not a
// behavioral change to the menu: the wire/take/uses logic below is identical regardless of which offers the
// list holds. CITE Villager.updateTrades / AbstractVillager.addOffersFromTradeSet as the future randomized
// selection adjuster (it replaces villagerOffersFor with a jar-faithful weighted pick, no caller change).
//
//	[VERIFIED CFR AbstractVillager.getOffers: if (offers == null) { offers = new MerchantOffers(); updateTrades(level); } return offers.]
func villagerGetOffers(e *Entity) merchantOffers {
	if e == nil {
		return nil
	}
	if !e.offersBuilt {
		// WanderingTrader.updateTrades (VERIFIED CFR): getOffers() -> addOffersFromTradeSet for
		// WANDERING_TRADER_BUYING/UNCOMMON/COMMON. Its offer set is a DIFFERENT trade table than a
		// Villager's profession/level tables, so the WT branch builds wanderingTraderOffers() instead of
		// villagerOffersFor. Like the villager path this is a DETERMINISTIC concrete reduction of the
		// randomized addOffersFromTradeSet pick (a faithful data reduction, not a menu behavior change --
		// the wire/take/uses logic below is identical regardless of which offers the list holds). CITE
		// WanderingTrader.updateTrades / AbstractVillager.addOffersFromTradeSet.
		if e.isWanderingTrader {
			e.offers = wanderingTraderOffers()
		} else {
			e.offers = villagerOffersFor(e.villagerProfession, e.villagerLevel)
		}
		e.offersBuilt = true
	}
	return e.offers
}

// villagerIsTrading ports AbstractVillager.isTrading(): tradingPlayer != null. v1 reduces tradingPlayer to
// the thin entity id (0 == none).
func villagerIsTrading(e *Entity) bool { return e != nil && e.villagerTradingPlayer != 0 }

// villagerSetTradingPlayer ports AbstractVillager.setTradingPlayer(player) (via Villager.setTradingPlayer,
// which additionally stopTrading()s when a non-null trader is cleared). Reduced to storing the thin player
// entity id; 0 clears. The Villager.setTradingPlayer stopTrading() (offer restock trigger on close) is the
// cited close hook realized in the menu's removed() path (merchant_menu.go closeMerchantWindow), so this
// stays a faithful field write.
//
//	[VERIFIED CFR AbstractVillager.setTradingPlayer: this.tradingPlayer = player.]
func villagerSetTradingPlayer(e *Entity, playerID int32) {
	if e != nil {
		e.villagerTradingPlayer = playerID
	}
}

func farmerLevel2Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(1, item.Apple, 4, 16, 5, 0.05),      // emerald_apple
		newItemsForEmeraldN(1, item.PumpkinPie, 4, 12, 5, 0.05), // emerald_pumpkin_pie
		newEmeraldForItemsN(item.Pumpkin, 6, 12, 10, 0.05),      // pumpkin_emerald
	}
}

func farmerLevel3Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(3, item.Cookie, 18, 12, 10, 0.05), // emerald_cookie
		newEmeraldForItemsN(item.Melon, 4, 12, 20, 0.05),      // melon_emerald
	}
}

func farmerLevel4Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(3, item.Cake, 1, 12, 15, 0.05),           // emerald_cake
		newItemsForEmeraldN(1, item.SuspiciousStew, 1, 12, 15, 0.05), // emerald_suspicious_stew
	}
}

func farmerLevel5Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(4, item.GlisteringMelonSlice, 3, 12, 30, 0.05), // emerald_glistening_melon_slice
		newItemsForEmeraldN(3, item.GoldenCarrot, 3, 12, 30, 0.05),         // emerald_golden_carrot
	}
}

func fishermanLevel1Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Coal, 10, 16, 2, 0.05),                                         // coal_emerald
		newItemsForEmeraldN(3, item.CodBucket, 1, 16, 0, 0.05),                                  // emerald_cod_bucket
		newProcessItemsForEmerald(item.Cod, 6, item.Emerald, 1, item.CookedCod, 6, 16, 0, 0.05), // raw_cod_and_emerald_cooked_cod
		newEmeraldForItemsN(item.String, 20, 16, 2, 0.05),                                       // string_emerald
	}
}

func fishermanLevel2Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Cod, 15, 16, 10, 0.05),                                               // cod_emerald
		newItemsForEmeraldN(2, item.Campfire, 1, 12, 5, 0.05),                                         // emerald_campfire
		newProcessItemsForEmerald(item.Salmon, 6, item.Emerald, 1, item.CookedSalmon, 6, 16, 5, 0.05), // salmon_and_emerald_cooked_salmon
	}
}

func fishermanLevel3Offers() merchantOffers {
	return merchantOffers{
		newEnchantedItemForEmeralds(item.FishingRod, 3, 3, 10, 0.2), // emerald_enchanted_fishing_rod
		newEmeraldForItemsN(item.Salmon, 13, 16, 20, 0.05),          // salmon_emerald
	}
}

func fishermanLevel4Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.TropicalFish, 6, 12, 30, 0.05), // tropical_fish_emerald
	}
}

func fishermanLevel5Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.AcaciaBoat, 1, 12, 30, 0.05),  // acacia_boat_emerald
		newEmeraldForItemsN(item.DarkOakBoat, 1, 12, 30, 0.05), // dark_oak_boat_emerald
		newEmeraldForItemsN(item.JungleBoat, 1, 12, 30, 0.05),  // jungle_boat_emerald
		newEmeraldForItemsN(item.OakBoat, 1, 12, 30, 0.05),     // oak_boat_emerald
		newEmeraldForItemsN(item.Pufferfish, 4, 12, 30, 0.05),  // pufferfish_emerald
		newEmeraldForItemsN(item.SpruceBoat, 1, 12, 30, 0.05),  // spruce_boat_emerald
	}
}

func shepherdLevel1Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.BlackWool, 18, 16, 2, 0.05), // black_wool_emerald
		newEmeraldForItemsN(item.BrownWool, 18, 16, 2, 0.05), // brown_wool_emerald
		newItemsForEmeraldN(2, item.Shears, 1, 12, 0, 0.05),  // emerald_shears
		newEmeraldForItemsN(item.GrayWool, 18, 16, 2, 0.05),  // gray_wool_emerald
		newEmeraldForItemsN(item.WhiteWool, 18, 16, 2, 0.05), // white_wool_emerald
	}
}

func shepherdLevel2Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.BlackDye, 12, 16, 10, 0.05),         // black_dye_emerald
		newItemsForEmeraldN(1, item.BlackCarpet, 4, 16, 5, 0.05),     // emerald_black_carpet
		newItemsForEmeraldN(1, item.BlackWool, 1, 16, 5, 0.05),       // emerald_black_wool
		newItemsForEmeraldN(1, item.BlueCarpet, 4, 16, 5, 0.05),      // emerald_blue_carpet
		newItemsForEmeraldN(1, item.BlueWool, 1, 16, 5, 0.05),        // emerald_blue_wool
		newItemsForEmeraldN(1, item.BrownCarpet, 4, 16, 5, 0.05),     // emerald_brown_carpet
		newItemsForEmeraldN(1, item.BrownWool, 1, 16, 5, 0.05),       // emerald_brown_wool
		newItemsForEmeraldN(1, item.CyanCarpet, 4, 16, 5, 0.05),      // emerald_cyan_carpet
		newItemsForEmeraldN(1, item.CyanWool, 1, 16, 5, 0.05),        // emerald_cyan_wool
		newItemsForEmeraldN(1, item.GrayCarpet, 4, 16, 5, 0.05),      // emerald_gray_carpet
		newItemsForEmeraldN(1, item.GrayWool, 1, 16, 5, 0.05),        // emerald_gray_wool
		newItemsForEmeraldN(1, item.GreenCarpet, 4, 16, 5, 0.05),     // emerald_green_carpet
		newItemsForEmeraldN(1, item.GreenWool, 1, 16, 5, 0.05),       // emerald_green_wool
		newItemsForEmeraldN(1, item.LightBlueCarpet, 4, 16, 5, 0.05), // emerald_light_blue_carpet
		newItemsForEmeraldN(1, item.LightBlueWool, 1, 16, 5, 0.05),   // emerald_light_blue_wool
		newItemsForEmeraldN(1, item.LightGrayCarpet, 4, 16, 5, 0.05), // emerald_light_gray_carpet
		newItemsForEmeraldN(1, item.LightGrayWool, 1, 16, 5, 0.05),   // emerald_light_gray_wool
		newItemsForEmeraldN(1, item.LimeCarpet, 4, 16, 5, 0.05),      // emerald_lime_carpet
		newItemsForEmeraldN(1, item.LimeWool, 1, 16, 5, 0.05),        // emerald_lime_wool
		newItemsForEmeraldN(1, item.MagentaCarpet, 4, 16, 5, 0.05),   // emerald_magenta_carpet
		newItemsForEmeraldN(1, item.MagentaWool, 1, 16, 5, 0.05),     // emerald_magenta_wool
		newItemsForEmeraldN(1, item.OrangeCarpet, 4, 16, 5, 0.05),    // emerald_orange_carpet
		newItemsForEmeraldN(1, item.OrangeWool, 1, 16, 5, 0.05),      // emerald_orange_wool
		newItemsForEmeraldN(1, item.PinkCarpet, 4, 16, 5, 0.05),      // emerald_pink_carpet
		newItemsForEmeraldN(1, item.PinkWool, 1, 16, 5, 0.05),        // emerald_pink_wool
		newItemsForEmeraldN(1, item.PurpleCarpet, 4, 16, 5, 0.05),    // emerald_purple_carpet
		newItemsForEmeraldN(1, item.PurpleWool, 1, 16, 5, 0.05),      // emerald_purple_wool
		newItemsForEmeraldN(1, item.RedCarpet, 4, 16, 5, 0.05),       // emerald_red_carpet
		newItemsForEmeraldN(1, item.RedWool, 1, 16, 5, 0.05),         // emerald_red_wool
		newItemsForEmeraldN(1, item.WhiteCarpet, 4, 16, 5, 0.05),     // emerald_white_carpet
		newItemsForEmeraldN(1, item.WhiteWool, 1, 16, 5, 0.05),       // emerald_white_wool
		newItemsForEmeraldN(1, item.YellowCarpet, 4, 16, 5, 0.05),    // emerald_yellow_carpet
		newItemsForEmeraldN(1, item.YellowWool, 1, 16, 5, 0.05),      // emerald_yellow_wool
		newEmeraldForItemsN(item.GrayDye, 12, 16, 10, 0.05),          // gray_dye_emerald
		newEmeraldForItemsN(item.LightBlueDye, 12, 16, 10, 0.05),     // light_blue_dye_emerald
		newEmeraldForItemsN(item.LimeDye, 12, 16, 10, 0.05),          // lime_dye_emerald
		newEmeraldForItemsN(item.WhiteDye, 12, 16, 10, 0.05),         // white_dye_emerald
	}
}

func shepherdLevel3Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(3, item.BlackBed, 1, 12, 10, 0.05),     // emerald_black_bed
		newItemsForEmeraldN(3, item.BlueBed, 1, 12, 10, 0.05),      // emerald_blue_bed
		newItemsForEmeraldN(3, item.BrownBed, 1, 12, 10, 0.05),     // emerald_brown_bed
		newItemsForEmeraldN(3, item.CyanBed, 1, 12, 10, 0.05),      // emerald_cyan_bed
		newItemsForEmeraldN(3, item.GrayBed, 1, 12, 10, 0.05),      // emerald_gray_bed
		newItemsForEmeraldN(3, item.GreenBed, 1, 12, 10, 0.05),     // emerald_green_bed
		newItemsForEmeraldN(3, item.LightBlueBed, 1, 12, 10, 0.05), // emerald_light_blue_bed
		newItemsForEmeraldN(3, item.LightGrayBed, 1, 12, 10, 0.05), // emerald_light_gray_bed
		newItemsForEmeraldN(3, item.LimeBed, 1, 12, 10, 0.05),      // emerald_lime_bed
		newItemsForEmeraldN(3, item.MagentaBed, 1, 12, 10, 0.05),   // emerald_magenta_bed
		newItemsForEmeraldN(3, item.OrangeBed, 1, 12, 10, 0.05),    // emerald_orange_bed
		newItemsForEmeraldN(3, item.PinkBed, 1, 12, 10, 0.05),      // emerald_pink_bed
		newItemsForEmeraldN(3, item.PurpleBed, 1, 12, 10, 0.05),    // emerald_purple_bed
		newItemsForEmeraldN(3, item.RedBed, 1, 12, 10, 0.05),       // emerald_red_bed
		newItemsForEmeraldN(3, item.WhiteBed, 1, 12, 10, 0.05),     // emerald_white_bed
		newItemsForEmeraldN(3, item.YellowBed, 1, 12, 10, 0.05),    // emerald_yellow_bed
		newEmeraldForItemsN(item.LightGrayDye, 12, 16, 20, 0.05),   // light_gray_dye_emerald
		newEmeraldForItemsN(item.OrangeDye, 12, 16, 20, 0.05),      // orange_dye_emerald
		newEmeraldForItemsN(item.PinkDye, 12, 16, 20, 0.05),        // pink_dye_emerald
		newEmeraldForItemsN(item.RedDye, 12, 16, 20, 0.05),         // red_dye_emerald
		newEmeraldForItemsN(item.YellowDye, 12, 16, 20, 0.05),      // yellow_dye_emerald
	}
}

func shepherdLevel4Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.BlueDye, 12, 16, 30, 0.05),           // blue_dye_emerald
		newEmeraldForItemsN(item.BrownDye, 12, 16, 30, 0.05),          // brown_dye_emerald
		newEmeraldForItemsN(item.CyanDye, 12, 16, 30, 0.05),           // cyan_dye_emerald
		newItemsForEmeraldN(3, item.BlackBanner, 1, 12, 15, 0.05),     // emerald_black_banner
		newItemsForEmeraldN(3, item.BlueBanner, 1, 12, 15, 0.05),      // emerald_blue_banner
		newItemsForEmeraldN(3, item.BrownBanner, 1, 12, 15, 0.05),     // emerald_brown_banner
		newItemsForEmeraldN(3, item.CyanBanner, 1, 12, 15, 0.05),      // emerald_cyan_banner
		newItemsForEmeraldN(3, item.GrayBanner, 1, 12, 15, 0.05),      // emerald_gray_banner
		newItemsForEmeraldN(3, item.GreenBanner, 1, 12, 15, 0.05),     // emerald_green_banner
		newItemsForEmeraldN(3, item.LightBlueBanner, 1, 12, 15, 0.05), // emerald_light_blue_banner
		newItemsForEmeraldN(3, item.LightGrayBanner, 1, 12, 15, 0.05), // emerald_light_gray_banner
		newItemsForEmeraldN(3, item.LimeBanner, 1, 12, 15, 0.05),      // emerald_lime_banner
		newItemsForEmeraldN(3, item.MagentaBanner, 1, 12, 15, 0.05),   // emerald_magenta_banner
		newItemsForEmeraldN(3, item.OrangeBanner, 1, 12, 15, 0.05),    // emerald_orange_banner
		newItemsForEmeraldN(3, item.PinkBanner, 1, 12, 15, 0.05),      // emerald_pink_banner
		newItemsForEmeraldN(3, item.PurpleBanner, 1, 12, 15, 0.05),    // emerald_purple_banner
		newItemsForEmeraldN(3, item.RedBanner, 1, 12, 15, 0.05),       // emerald_red_banner
		newItemsForEmeraldN(3, item.WhiteBanner, 1, 12, 15, 0.05),     // emerald_white_banner
		newItemsForEmeraldN(3, item.YellowBanner, 1, 12, 15, 0.05),    // emerald_yellow_banner
		newEmeraldForItemsN(item.GreenDye, 12, 16, 30, 0.05),          // green_dye_emerald
		newEmeraldForItemsN(item.MagentaDye, 12, 16, 30, 0.05),        // magenta_dye_emerald
		newEmeraldForItemsN(item.PurpleDye, 12, 16, 30, 0.05),         // purple_dye_emerald
	}
}

func shepherdLevel5Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(2, item.Painting, 3, 12, 30, 0.05), // emerald_painting
	}
}

func fletcherLevel1Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(1, item.Arrow, 16, 12, 0, 0.05),                                      // emerald_arrow
		newProcessItemsForEmerald(item.Gravel, 10, item.Emerald, 1, item.Flint, 10, 12, 0, 0.05), // gravel_and_emerald_flint
		newEmeraldForItemsN(item.Stick, 32, 16, 2, 0.05),                                         // stick_emerald
	}
}

func fletcherLevel2Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(2, item.Bow, 1, 12, 5, 0.05),  // emerald_bow
		newEmeraldForItemsN(item.Flint, 26, 12, 10, 0.05), // flint_emerald
	}
}

func fletcherLevel3Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(3, item.Crossbow, 1, 12, 10, 0.05), // emerald_crossbow
		newEmeraldForItemsN(item.String, 14, 16, 20, 0.05),     // string_emerald
	}
}

func fletcherLevel4Offers() merchantOffers {
	return merchantOffers{
		newEnchantedItemForEmeralds(item.Bow, 2, 3, 15, 0.05), // emerald_enchanted_bow
		newEmeraldForItemsN(item.Feather, 24, 16, 30, 0.05),   // feather_emerald
	}
}

func fletcherLevel5Offers() merchantOffers {
	return merchantOffers{
		newProcessItemsForEmerald(item.Emerald, 2, item.Arrow, 5, item.TippedArrow, 5, 12, 30, 0.05), // arrow_and_emerald_tipped_arrow
		newEnchantedItemForEmeralds(item.Crossbow, 3, 3, 15, 0.05),                                   // emerald_enchanted_crossbow
		newEmeraldForItemsN(item.TripwireHook, 8, 12, 30, 0.05),                                      // tripwire_hook_emerald
	}
}

func librarianLevel1Offers() merchantOffers {
	return merchantOffers{
		newEnchantedBookForEmeralds(0, 12, 0, 0.2),             // emerald_and_book_enchanted_book
		newItemsForEmeraldN(9, item.Bookshelf, 1, 12, 0, 0.05), // emerald_bookshelf
		newEmeraldForItemsN(item.Paper, 24, 16, 2, 0.05),       // paper_emerald
	}
}

func librarianLevel2Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Book, 4, 12, 10, 0.05),      // book_emerald
		newEnchantedBookForEmeralds(0, 12, 5, 0.2),           // emerald_and_book_enchanted_book
		newItemsForEmeraldN(1, item.Lantern, 1, 12, 5, 0.05), // emerald_lantern
	}
}

func librarianLevel3Offers() merchantOffers {
	return merchantOffers{
		newEnchantedBookForEmeralds(0, 12, 10, 0.2),         // emerald_and_book_enchanted_book
		newItemsForEmeraldN(1, item.Glass, 4, 12, 10, 0.05), // emerald_glass
		newEmeraldForItemsN(item.InkSac, 5, 12, 20, 0.05),   // ink_sac_emerald
	}
}

func librarianLevel4Offers() merchantOffers {
	return merchantOffers{
		newEnchantedBookForEmeralds(0, 12, 15, 0.2),             // emerald_book_and_enchanted_book
		newItemsForEmeraldN(5, item.Clock, 1, 12, 15, 0.05),     // emerald_clock
		newItemsForEmeraldN(4, item.Compass, 1, 12, 15, 0.05),   // emerald_compass
		newEmeraldForItemsN(item.WritableBook, 2, 12, 30, 0.05), // writable_book_emerald
	}
}

func librarianLevel5Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(3, item.RedCandle, 1, 12, 30, 0.05),    // emerald_red_candle
		newItemsForEmeraldN(3, item.YellowCandle, 1, 12, 30, 0.05), // emerald_yellow_candle
	}
}

func cartographerLevel1Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(7, item.Map, 1, 12, 0, 0.05), // emerald_map
		newEmeraldForItemsN(item.Paper, 24, 12, 2, 0.05), // paper_emerald
	}
}

func cartographerLevel2Offers() merchantOffers {
	return merchantOffers{
		newProcessItemsForEmerald(item.Emerald, 8, item.Compass, 1, item.Map, 1, 12, 5, 0.2), // emerald_and_compass_explorer_jungle_map
		newProcessItemsForEmerald(item.Emerald, 8, item.Compass, 1, item.Map, 1, 12, 5, 0.2), // emerald_and_compass_explorer_swamp_map
		newProcessItemsForEmerald(item.Emerald, 8, item.Compass, 1, item.Map, 1, 12, 5, 0.2), // emerald_and_compass_village_desert_map
		newProcessItemsForEmerald(item.Emerald, 8, item.Compass, 1, item.Map, 1, 12, 5, 0.2), // emerald_and_compass_village_plains_map
		newProcessItemsForEmerald(item.Emerald, 8, item.Compass, 1, item.Map, 1, 12, 5, 0.2), // emerald_and_compass_village_savanna_map
		newProcessItemsForEmerald(item.Emerald, 8, item.Compass, 1, item.Map, 1, 12, 5, 0.2), // emerald_and_compass_village_snowy_map
		newProcessItemsForEmerald(item.Emerald, 8, item.Compass, 1, item.Map, 1, 12, 5, 0.2), // emerald_and_compass_village_taiga_map
		newEmeraldForItemsN(item.GlassPane, 11, 12, 10, 0.05),                                // glass_pane_emerald
	}
}

func cartographerLevel3Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Compass, 1, 12, 20, 0.05),                                     // compass_emerald
		newProcessItemsForEmerald(item.Emerald, 13, item.Compass, 1, item.Map, 1, 12, 10, 0.2), // emerald_and_compass_ocean_explorer_map
		newProcessItemsForEmerald(item.Emerald, 12, item.Compass, 1, item.Map, 1, 12, 10, 0.2), // emerald_and_compass_trial_chamber_map
	}
}

func cartographerLevel4Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(2, item.BlackBanner, 1, 12, 15, 0.05),     // emerald_black_banner
		newItemsForEmeraldN(2, item.BlueBanner, 1, 12, 15, 0.05),      // emerald_blue_banner
		newItemsForEmeraldN(2, item.BrownBanner, 1, 12, 15, 0.05),     // emerald_brown_banner
		newItemsForEmeraldN(2, item.CyanBanner, 1, 12, 15, 0.05),      // emerald_cyan_banner
		newItemsForEmeraldN(2, item.GrayBanner, 1, 12, 15, 0.05),      // emerald_gray_banner
		newItemsForEmeraldN(2, item.GreenBanner, 1, 12, 15, 0.05),     // emerald_green_banner
		newItemsForEmeraldN(7, item.ItemFrame, 1, 12, 15, 0.05),       // emerald_item_frame
		newItemsForEmeraldN(2, item.LightBlueBanner, 1, 12, 15, 0.05), // emerald_light_blue_banner
		newItemsForEmeraldN(2, item.LimeBanner, 1, 12, 15, 0.05),      // emerald_lime_banner
		newItemsForEmeraldN(2, item.MagentaBanner, 1, 12, 15, 0.05),   // emerald_magenta_banner
		newItemsForEmeraldN(2, item.OrangeBanner, 1, 12, 15, 0.05),    // emerald_orange_banner
		newItemsForEmeraldN(2, item.PinkBanner, 1, 12, 15, 0.05),      // emerald_pink_banner
		newItemsForEmeraldN(2, item.PurpleBanner, 1, 12, 15, 0.05),    // emerald_purple_banner
		newItemsForEmeraldN(2, item.RedBanner, 1, 12, 15, 0.05),       // emerald_red_banner
		newItemsForEmeraldN(2, item.WhiteBanner, 1, 12, 15, 0.05),     // emerald_white_banner
		newItemsForEmeraldN(2, item.YellowBanner, 1, 12, 15, 0.05),    // emerald_yellow_banner
	}
}

func cartographerLevel5Offers() merchantOffers {
	return merchantOffers{
		newProcessItemsForEmerald(item.Emerald, 14, item.Compass, 1, item.Map, 1, 12, 30, 0.2), // emerald_and_compass_woodland_mansion_map
		newItemsForEmeraldN(8, item.GlobeBannerPattern, 1, 12, 30, 0.05),                       // emerald_globe_banner_pattern
	}
}

func clericLevel1Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(1, item.Redstone, 2, 12, 0, 0.05),  // emerald_redstone
		newEmeraldForItemsN(item.RottenFlesh, 32, 16, 2, 0.05), // rotten_flesh_emerald
	}
}

func clericLevel2Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(1, item.LapisLazuli, 1, 12, 5, 0.05), // emerald_lapis_lazuli
		newEmeraldForItemsN(item.GoldIngot, 3, 12, 10, 0.05),     // gold_ingot_emerald
	}
}

func clericLevel3Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(4, item.Glowstone, 1, 12, 10, 0.05), // emerald_glowstone
		newEmeraldForItemsN(item.RabbitFoot, 2, 12, 20, 0.05),   // rabbit_foot_emerald
	}
}

func clericLevel4Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(5, item.EnderPearl, 1, 12, 15, 0.05), // emerald_ender_pearl
		newEmeraldForItemsN(item.GlassBottle, 9, 12, 30, 0.05),   // glass_bottle_emerald
		newEmeraldForItemsN(item.TurtleScute, 4, 12, 30, 0.05),   // turtle_scute_emerald
	}
}

func clericLevel5Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(3, item.ExperienceBottle, 1, 12, 30, 0.05), // emerald_experience_bottle
		newEmeraldForItemsN(item.NetherWart, 22, 12, 30, 0.05),         // nether_wart_emerald
	}
}

func armorerLevel1Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(4, item.IronBoots, 1, 12, 0, 0.2),      // emerald_iron_boots
		newItemsForEmeraldN(9, item.IronChestplate, 1, 12, 0, 0.2), // emerald_iron_chestplate
		newItemsForEmeraldN(5, item.IronHelmet, 1, 12, 0, 0.2),     // emerald_iron_helmet
		newItemsForEmeraldN(7, item.IronLeggings, 1, 12, 0, 0.2),   // emerald_iron_leggings
	}
}

// commonSmithLevel2Offers ports the shared #minecraft:common_smith/level_2 trade tag included by every smith
// profession's level_2 tag (armorer, weaponsmith, toolsmith). The tag values in declaration order are
// smith/2/iron_ingot_emerald then smith/2/emerald_bell. VERIFIED datapack
// data/minecraft/tags/villager_trade/common_smith/level_2.json + data/minecraft/villager_trade/smith/2/*.json:
//
//	iron_ingot_emerald: wants iron_ingot x4 -> gives emerald x1, max_uses 12, xp 10, reputation_discount 0.05
//	emerald_bell:       wants emerald x36  -> gives bell x1,    max_uses 12, xp 5,  reputation_discount 0.2
func commonSmithLevel2Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.IronIngot, 4, 12, 10, 0.05), // smith/2/iron_ingot_emerald
		newItemsForEmeraldN(36, item.Bell, 1, 12, 5, 0.2),    // smith/2/emerald_bell
	}
}

func armorerLevel2Offers() merchantOffers {
	// armorer/level_2 tag order: #common_smith/level_2 FIRST, then the two chainmail trades.
	// VERIFIED data/minecraft/tags/villager_trade/armorer/level_2.json.
	return append(commonSmithLevel2Offers(),
		newItemsForEmeraldN(1, item.ChainmailBoots, 1, 12, 5, 0.2),    // emerald_chainmail_boots
		newItemsForEmeraldN(3, item.ChainmailLeggings, 1, 12, 5, 0.2), // emerald_chainmail_leggings
	)
}

func armorerLevel3Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Diamond, 1, 12, 20, 0.05),               // diamond_emerald
		newItemsForEmeraldN(4, item.ChainmailChestplate, 1, 12, 10, 0.2), // emerald_chainmail_chestplate
		newItemsForEmeraldN(1, item.ChainmailHelmet, 1, 12, 10, 0.2),     // emerald_chainmail_helmet
		newItemsForEmeraldN(5, item.Shield, 1, 12, 10, 0.2),              // emerald_shield
		newEmeraldForItemsN(item.LavaBucket, 1, 12, 20, 0.05),            // lava_bucket_emerald
	}
}

func armorerLevel4Offers() merchantOffers {
	return merchantOffers{
		newEnchantedItemForEmeralds(item.DiamondBoots, 8, 3, 15, 0.2),     // emerald_enchanted_diamond_boots
		newEnchantedItemForEmeralds(item.DiamondLeggings, 14, 3, 15, 0.2), // emerald_enchanted_diamond_leggings
	}
}

func armorerLevel5Offers() merchantOffers {
	return merchantOffers{
		newEnchantedItemForEmeralds(item.DiamondChestplate, 16, 3, 30, 0.2), // emerald_enchanted_diamond_chestplate
		newEnchantedItemForEmeralds(item.DiamondHelmet, 8, 3, 30, 0.2),      // emerald_enchanted_diamond_helmet
	}
}

func weaponsmithLevel1Offers() merchantOffers {
	return merchantOffers{
		newEnchantedItemForEmeralds(item.IronSword, 2, 12, 0, 0.2), // emerald_enchanted_iron_sword
		newItemsForEmeraldN(3, item.IronAxe, 1, 12, 0, 0.2),        // emerald_iron_axe
	}
}

// weaponsmithLevel2Offers ports the weaponsmith/level_2 tag = ONLY #common_smith/level_2 (no
// profession-specific L2 trades). VERIFIED data/minecraft/tags/villager_trade/weaponsmith/level_2.json.
func weaponsmithLevel2Offers() merchantOffers {
	return commonSmithLevel2Offers()
}

func weaponsmithLevel3Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Flint, 24, 12, 20, 0.05), // flint_emerald
	}
}

func weaponsmithLevel4Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Diamond, 1, 12, 30, 0.05),           // diamond_emerald
		newEnchantedItemForEmeralds(item.DiamondAxe, 12, 3, 15, 0.2), // emerald_enchanted_diamond_axe
	}
}

func weaponsmithLevel5Offers() merchantOffers {
	return merchantOffers{
		newEnchantedItemForEmeralds(item.DiamondSword, 8, 3, 30, 0.2), // emerald_enchanted_diamond_sword
	}
}

func toolsmithLevel1Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(1, item.StoneAxe, 1, 12, 0, 0.2),     // emerald_stone_axe
		newItemsForEmeraldN(1, item.StoneHoe, 1, 12, 0, 0.2),     // emerald_stone_hoe
		newItemsForEmeraldN(1, item.StonePickaxe, 1, 12, 0, 0.2), // emerald_stone_pickaxe
		newItemsForEmeraldN(1, item.StoneShovel, 1, 12, 0, 0.2),  // emerald_stone_shovel
	}
}

// toolsmithLevel2Offers ports the toolsmith/level_2 tag = ONLY #common_smith/level_2 (no
// profession-specific L2 trades). VERIFIED data/minecraft/tags/villager_trade/toolsmith/level_2.json.
func toolsmithLevel2Offers() merchantOffers {
	return commonSmithLevel2Offers()
}

func toolsmithLevel3Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(4, item.DiamondHoe, 1, 3, 10, 0.2),       // emerald_diamond_hoe
		newEnchantedItemForEmeralds(item.IronAxe, 1, 3, 10, 0.2),     // emerald_enchanted_iron_axe
		newEnchantedItemForEmeralds(item.IronPickaxe, 3, 3, 10, 0.2), // emerald_enchanted_iron_pickaxe
		newEnchantedItemForEmeralds(item.IronShovel, 2, 3, 10, 0.2),  // emerald_enchanted_iron_shovel
		newEmeraldForItemsN(item.Flint, 30, 12, 20, 0.05),            // flint_emerald
	}
}

func toolsmithLevel4Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Diamond, 1, 12, 30, 0.05),             // diamond_emerald
		newEnchantedItemForEmeralds(item.DiamondAxe, 12, 3, 15, 0.2),   // emerald_enchanted_diamond_axe
		newEnchantedItemForEmeralds(item.DiamondShovel, 5, 3, 15, 0.2), // emerald_enchanted_diamond_shovel
	}
}

func toolsmithLevel5Offers() merchantOffers {
	return merchantOffers{
		newEnchantedItemForEmeralds(item.DiamondPickaxe, 13, 3, 30, 0.2), // emerald_enchanted_diamond_pickaxe
	}
}

func butcherLevel1Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Chicken, 14, 16, 2, 0.05),      // chicken_emerald
		newItemsForEmeraldN(1, item.RabbitStew, 1, 12, 0, 0.05), // emerald_rabbit_stew
		newEmeraldForItemsN(item.Porkchop, 7, 16, 2, 0.05),      // porkchop_emerald
		newEmeraldForItemsN(item.Rabbit, 4, 16, 2, 0.05),        // rabbit_emerald
	}
}

func butcherLevel2Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Coal, 15, 16, 2, 0.05),             // coal_emerald
		newItemsForEmeraldN(1, item.CookedChicken, 8, 16, 5, 0.05),  // emerald_cooked_chicken
		newItemsForEmeraldN(1, item.CookedPorkchop, 5, 16, 5, 0.05), // emerald_cooked_porkchop
	}
}

func butcherLevel3Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Beef, 10, 16, 20, 0.05),  // beef_emerald
		newEmeraldForItemsN(item.Mutton, 7, 16, 20, 0.05), // mutton_emerald
	}
}

func butcherLevel4Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.DriedKelpBlock, 10, 12, 30, 0.05), // dried_kelp_block_emerald
	}
}

func butcherLevel5Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.SweetBerries, 10, 12, 30, 0.05), // sweet_berries_emerald
	}
}

func leatherworkerLevel1Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(7, item.LeatherChestplate, 1, 12, 0, 0.2), // emerald_dyed_leather_chestplate
		newItemsForEmeraldN(3, item.LeatherLeggings, 1, 12, 0, 0.2),   // emerald_dyed_leather_leggings
		newEmeraldForItemsN(item.Leather, 6, 16, 2, 0.05),             // leather_emerald
	}
}

func leatherworkerLevel2Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(4, item.LeatherBoots, 1, 12, 5, 0.2),  // emerald_dyed_leather_boots
		newItemsForEmeraldN(5, item.LeatherHelmet, 1, 12, 5, 0.2), // emerald_dyed_leather_helmet
		newEmeraldForItemsN(item.Flint, 26, 12, 10, 0.05),         // flint_emerald
	}
}

func leatherworkerLevel3Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(7, item.LeatherChestplate, 1, 12, 0, 0.2), // emerald_dyed_leather_chestplate
		newEmeraldForItemsN(item.RabbitHide, 9, 12, 20, 0.05),         // rabbit_hide_emerald
	}
}

func leatherworkerLevel4Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(6, item.LeatherHorseArmor, 1, 12, 15, 0.2), // emerald_dyed_leather_horse_armor
		newEmeraldForItemsN(item.TurtleScute, 4, 12, 30, 0.05),         // turtle_scute_emerald
	}
}

func leatherworkerLevel5Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(5, item.LeatherHelmet, 1, 12, 5, 0.2), // emerald_dyed_leather_helmet
		newItemsForEmeraldN(6, item.Saddle, 1, 12, 30, 0.2),       // emerald_saddle
	}
}

func masonLevel1Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.ClayBall, 10, 16, 2, 0.05), // clay_ball_emerald
		newItemsForEmeraldN(1, item.Brick, 10, 16, 0, 0.05), // emerald_brick
	}
}

func masonLevel2Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(1, item.ChiseledStoneBricks, 4, 16, 5, 0.05), // emerald_chiseled_stone_bricks
		newEmeraldForItemsN(item.Stone, 20, 16, 10, 0.05),                // stone_emerald
	}
}

func masonLevel3Offers() merchantOffers {
	return merchantOffers{
		newEmeraldForItemsN(item.Andesite, 16, 16, 20, 0.05),           // andesite_emerald
		newEmeraldForItemsN(item.Diorite, 16, 16, 20, 0.05),            // diorite_emerald
		newItemsForEmeraldN(1, item.DripstoneBlock, 4, 16, 10, 0.05),   // emerald_dripstone_block
		newItemsForEmeraldN(1, item.PolishedAndesite, 4, 16, 10, 0.05), // emerald_polished_andesite
		newItemsForEmeraldN(1, item.PolishedDiorite, 4, 16, 10, 0.05),  // emerald_polished_diorite
		newItemsForEmeraldN(1, item.PolishedGranite, 4, 16, 10, 0.05),  // emerald_polished_granite
		newEmeraldForItemsN(item.Granite, 16, 16, 20, 0.05),            // granite_emerald
	}
}

func masonLevel4Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(1, item.BlackGlazedTerracotta, 1, 12, 15, 0.05),     // emerald_black_glazed_terracotta
		newItemsForEmeraldN(1, item.BlackTerracotta, 1, 12, 15, 0.05),           // emerald_black_terracotta
		newItemsForEmeraldN(1, item.BlueGlazedTerracotta, 1, 12, 15, 0.05),      // emerald_blue_glazed_terracotta
		newItemsForEmeraldN(1, item.BlueTerracotta, 1, 12, 15, 0.05),            // emerald_blue_terracotta
		newItemsForEmeraldN(1, item.BrownGlazedTerracotta, 1, 12, 15, 0.05),     // emerald_brown_glazed_terracotta
		newItemsForEmeraldN(1, item.BrownTerracotta, 1, 12, 15, 0.05),           // emerald_brown_terracotta
		newItemsForEmeraldN(1, item.CyanGlazedTerracotta, 1, 12, 15, 0.05),      // emerald_cyan_glazed_terracotta
		newItemsForEmeraldN(1, item.CyanTerracotta, 1, 12, 15, 0.05),            // emerald_cyan_terracotta
		newItemsForEmeraldN(1, item.GrayGlazedTerracotta, 1, 12, 15, 0.05),      // emerald_gray_glazed_terracotta
		newItemsForEmeraldN(1, item.GrayTerracotta, 1, 12, 15, 0.05),            // emerald_gray_terracotta
		newItemsForEmeraldN(1, item.GreenGlazedTerracotta, 1, 12, 15, 0.05),     // emerald_green_glazed_terracotta
		newItemsForEmeraldN(1, item.GreenTerracotta, 1, 12, 15, 0.05),           // emerald_green_terracotta
		newItemsForEmeraldN(1, item.LightBlueGlazedTerracotta, 1, 12, 15, 0.05), // emerald_light_blue_glazed_terracotta
		newItemsForEmeraldN(1, item.LightBlueTerracotta, 1, 12, 15, 0.05),       // emerald_light_blue_terracotta
		newItemsForEmeraldN(1, item.LightGrayGlazedTerracotta, 1, 12, 15, 0.05), // emerald_light_gray_glazed_terracotta
		newItemsForEmeraldN(1, item.LightGrayTerracotta, 1, 12, 15, 0.05),       // emerald_light_gray_terracotta
		newItemsForEmeraldN(1, item.LimeGlazedTerracotta, 1, 12, 15, 0.05),      // emerald_lime_glazed_terracotta
		newItemsForEmeraldN(1, item.LimeTerracotta, 1, 12, 15, 0.05),            // emerald_lime_terracotta
		newItemsForEmeraldN(1, item.MagentaGlazedTerracotta, 1, 12, 15, 0.05),   // emerald_magenta_glazed_terracotta
		newItemsForEmeraldN(1, item.MagentaTerracotta, 1, 12, 15, 0.05),         // emerald_magenta_terracotta
		newItemsForEmeraldN(1, item.OrangeGlazedTerracotta, 1, 12, 15, 0.05),    // emerald_orange_glazed_terracotta
		newItemsForEmeraldN(1, item.OrangeTerracotta, 1, 12, 15, 0.05),          // emerald_orange_terracotta
		newItemsForEmeraldN(1, item.PinkGlazedTerracotta, 1, 12, 15, 0.05),      // emerald_pink_glazed_terracotta
		newItemsForEmeraldN(1, item.PinkTerracotta, 1, 12, 15, 0.05),            // emerald_pink_terracotta
		newItemsForEmeraldN(1, item.PurpleGlazedTerracotta, 1, 12, 15, 0.05),    // emerald_purple_glazed_terracotta
		newItemsForEmeraldN(1, item.PurpleTerracotta, 1, 12, 15, 0.05),          // emerald_purple_terracotta
		newItemsForEmeraldN(1, item.RedGlazedTerracotta, 1, 12, 15, 0.05),       // emerald_red_glazed_terracotta
		newItemsForEmeraldN(1, item.RedTerracotta, 1, 12, 15, 0.05),             // emerald_red_terracotta
		newItemsForEmeraldN(1, item.WhiteGlazedTerracotta, 1, 12, 15, 0.05),     // emerald_white_glazed_terracotta
		newItemsForEmeraldN(1, item.WhiteTerracotta, 1, 12, 15, 0.05),           // emerald_white_terracotta
		newItemsForEmeraldN(1, item.YellowGlazedTerracotta, 1, 12, 15, 0.05),    // emerald_yellow_glazed_terracotta
		newItemsForEmeraldN(1, item.YellowTerracotta, 1, 12, 15, 0.05),          // emerald_yellow_terracotta
		newEmeraldForItemsN(item.Quartz, 12, 12, 30, 0.05),                      // quartz_emerald
	}
}

func masonLevel5Offers() merchantOffers {
	return merchantOffers{
		newItemsForEmeraldN(1, item.QuartzBlock, 1, 12, 30, 0.05),  // emerald_quartz_block
		newItemsForEmeraldN(1, item.QuartzPillar, 1, 12, 30, 0.05), // emerald_quartz_pillar
	}
}
