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

// isOutOfStock ports MerchantOffer.isOutOfStock(): uses >= maxUses.
func (m *merchantOffer) isOutOfStock() bool { return m.uses >= m.maxUses }

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
	if profession == "farmer" && level == 1 {
		return farmerLevel1Offers()
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
		e.offers = villagerOffersFor(e.villagerProfession, e.villagerLevel)
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
