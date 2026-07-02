package server

import "github.com/imhinotori/sulfur/data/item"

// villager_trades.go — the villager TRADE OFFER model (net.minecraft.world.item.trading.MerchantOffer /
// MerchantOffers) plus a faithful slice of the FARMER trade DATA. A LITERAL port of the unobfuscated 26.2
// jar (temp/cache/26.2-inner.jar, javap -c -p / CFR this session).
//
// SCOPE: this lands the OFFER DATA MODEL + a few concrete offers. The merchant MENU (startTrading ->
// ClientboundMerchantOffers -> the container-menu subsystem) is CITE-DEFERRED: there is no merchant/menu
// subsystem in v1 (grep confirmed — only crafting/stonecutter menus exist), so Villager.mobInteract's
// startTrading opens nothing yet; it lands as villagerStartTrading (a stub that resolves the offers so the
// model is exercised). The gossip/reputation price economy (specialPriceDiff/demand adjustment, TRADE
// reputation events) is deferred too. Cite Villager.mobInteract / MerchantOffer / VillagerTrades.
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

// villagerStartTrading is the DEFERRED stub for Villager.mobInteract -> startTrading(player): it resolves
// the offers for the villager's (profession, level) so the model is exercised, but opens NO merchant menu
// (no ClientboundMerchantOffers container-menu subsystem exists yet — only crafting/stonecutter menus).
// Returns the resolved offers (the caller would open the menu once the subsystem lands). Cite
// Villager.mobInteract (getOffers().isEmpty() gate -> startTrading).
func villagerStartTrading(e *Entity) merchantOffers {
	if e == nil {
		return nil
	}
	return villagerOffersFor(e.villagerProfession, e.villagerLevel)
}
