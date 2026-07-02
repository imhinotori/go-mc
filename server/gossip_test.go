package server

// gossip_test.go — VILLAGER GOSSIP / REPUTATION PRICE ECONOMY. Asserts the jar-verified numbers
// (temp/cache/26.2-inner.jar, CFR this session) of:
//   - GossipContainer.add / getReputation weighted sum + per-type caps (mergeValuesForAddition) + decay
//   - Villager.onReputationEventFrom(TRADE) raising a player's TRADING gossip (+2)
//   - Villager.updateSpecialPrices lowering an offer's cost for a high-reputation player
//   - MerchantOffer.updateDemand raising demand after repeated uses
//
// The constants under test (GossipType weight/max/decayPerDay, updateSpecialPrices floor formula,
// updateDemand shape) are all cited against the jar in gossip.go / villager_reputation.go / villager_trades.go.

import (
	"testing"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestGossipReputationWeightedSum: add builds a per-type weighted sum, and getReputation(all-types) totals
// value*weight across every type for the target.
//
//	MAJOR_POSITIVE(weight 5): value 20 -> +100
//	MINOR_NEGATIVE(weight -1): value 5 -> -5
//	total reputation = 95   (VERIFIED against GossipType weights + EntityGossips.weightedValue)
func TestGossipReputationWeightedSum(t *testing.T) {
	c := newGossipContainer()
	target := uuid.New()

	c.add(target, gossipMajorPositive, 20) // 20 (== max, un-clamped since 20 is not > 20)
	c.add(target, gossipMinorNegative, 5)  // 5

	if got := c.getReputation(target, func(*gossipType) bool { return true }); got != 95 {
		t.Fatalf("reputation = %d, want 95 (20*5 + 5*-1)", got)
	}

	// A different, ungossiped target has zero reputation.
	if got := c.getReputation(uuid.New(), func(*gossipType) bool { return true }); got != 0 {
		t.Fatalf("reputation of ungossiped target = %d, want 0", got)
	}
}

// TestGossipAdditionCap: mergeValuesForAddition never lets a value RISE past type.max. MAJOR_POSITIVE.max
// is 20; adding 25 clamps the stored value to 20 (weighted 100), and a further +30 stays at 20.
//
//	[VERIFIED CFR mergeValuesForAddition: sum > max ? Math.max(max, old) : sum; and
//	 makeSureValueIsntTooLowOrTooHigh: value > max -> put(max).]
func TestGossipAdditionCap(t *testing.T) {
	c := newGossipContainer()
	target := uuid.New()

	c.add(target, gossipMajorPositive, 25) // raw 25 -> makeSure clamps to max 20
	if got := c.gossips[target].entries[gossipMajorPositive]; got != 20 {
		t.Fatalf("MAJOR_POSITIVE after +25 = %d, want 20 (clamped to max)", got)
	}
	c.add(target, gossipMajorPositive, 30) // 20+30=50 > 20 -> max(20, old 20) = 20
	if got := c.gossips[target].entries[gossipMajorPositive]; got != 20 {
		t.Fatalf("MAJOR_POSITIVE after +30 = %d, want 20 (capped)", got)
	}
}

// TestGossipDecay: decay() subtracts decayPerDay; an entry falling below DISCARD_THRESHOLD (2) is removed,
// while a zero-decay type is untouched.
//
//	MAJOR_POSITIVE decayPerDay 0 -> value 20 unchanged.
//	MINOR_NEGATIVE decayPerDay 20 -> value 5 becomes -15 (< 2) -> removed.
//	TRADING        decayPerDay 2  -> value 25 becomes 23 (>= 2) -> kept.
func TestGossipDecay(t *testing.T) {
	c := newGossipContainer()
	target := uuid.New()
	c.add(target, gossipMajorPositive, 20)
	c.add(target, gossipMinorNegative, 5)
	c.add(target, gossipTrading, 25) // TRADING.max is 25, so 25 is kept

	c.decay()

	eg := c.gossips[target]
	if eg == nil {
		t.Fatal("target dropped entirely after decay, want MAJOR_POSITIVE + TRADING to survive")
	}
	if got, ok := eg.entries[gossipMajorPositive]; !ok || got != 20 {
		t.Fatalf("MAJOR_POSITIVE after decay = %d (present=%v), want 20 (decayPerDay 0)", got, ok)
	}
	if _, ok := eg.entries[gossipMinorNegative]; ok {
		t.Fatalf("MINOR_NEGATIVE survived decay, want removed (5 - 20 = -15 < 2)")
	}
	if got, ok := eg.entries[gossipTrading]; !ok || got != 23 {
		t.Fatalf("TRADING after decay = %d (present=%v), want 23 (25 - 2)", got, ok)
	}
}

// TestTradeEventRaisesReputation: onReputationEventFrom(TRADE, player) adds TRADING(+2), so the player's
// reputation rises by 2 (TRADING weight 1).
//
//	[VERIFIED CFR Villager.onReputationEventFrom: TRADE -> gossips.add(uuid, TRADING, 2).]
func TestTradeEventRaisesReputation(t *testing.T) {
	v := NewEntity(6300, entity.Villager, 8.5, 64.0, 8.5)
	v.villagerProfession = "farmer"
	v.villagerLevel = 1
	player := uuid.New()

	if got := villagerGetPlayerReputation(v, player); got != 0 {
		t.Fatalf("initial reputation = %d, want 0", got)
	}
	villagerOnReputationEventFrom(v, reputationTrade, player)
	if got := villagerGetPlayerReputation(v, player); got != 2 {
		t.Fatalf("reputation after 1 TRADE event = %d, want 2 (TRADING +2, weight 1)", got)
	}
	villagerOnReputationEventFrom(v, reputationTrade, player)
	if got := villagerGetPlayerReputation(v, player); got != 4 {
		t.Fatalf("reputation after 2 TRADE events = %d, want 4", got)
	}
}

// TestUpdateSpecialPricesDiscount: a high-reputation player gets a per-offer discount. With reputation 125
// and the farmer priceMultiplier 0.05f, the wheat->emerald offer (baseCost 20) gets
// specialPriceDiff -= floor(125 * 0.05f) = -6, so getModifiedCostCount drops from 20 to 14.
//
//	[VERIFIED CFR Villager.updateSpecialPrices: offer.addToSpecialPriceDiff(-Mth.floor((float)reputation *
//	 offer.getPriceMultiplier())); MerchantOffer.getModifiedCostCount clamp(base + demandDiff + specialDiff).]
func TestUpdateSpecialPricesDiscount(t *testing.T) {
	v := NewEntity(6301, entity.Villager, 8.5, 64.0, 8.5)
	v.villagerProfession = "farmer"
	v.villagerLevel = 1
	player := uuid.New()

	// Build reputation 125: MAJOR_POSITIVE 20 (weighted 100) + TRADING 25 (weighted 25).
	g := villagerEnsureGossips(v)
	g.add(player, gossipMajorPositive, 20)
	g.add(player, gossipTrading, 25)
	if rep := villagerGetPlayerReputation(v, player); rep != 125 {
		t.Fatalf("setup reputation = %d, want 125 (100 + 25)", rep)
	}

	offers := villagerGetOffers(v)
	wheat := offers[0] // newEmeraldForItems(Wheat, 20, 16, 2)
	baseCost := wheat.getModifiedCostCount(wheat.baseCostA)
	if baseCost != 20 {
		t.Fatalf("pre-discount wheat cost = %d, want 20", baseCost)
	}

	// heroAmplifier -1 == no HERO_OF_THE_VILLAGE effect (the hero loop is skipped).
	villagerUpdateSpecialPrices(v, player, -1)

	if wheat.specialPriceDiff != -6 {
		t.Fatalf("wheat specialPriceDiff = %d, want -6 (-floor(125 * 0.05f))", wheat.specialPriceDiff)
	}
	if got := wheat.getModifiedCostCount(wheat.baseCostA); got != 14 {
		t.Fatalf("discounted wheat cost = %d, want 14 (20 - 6)", got)
	}

	// resetSpecialPrices zeroes the discount (trade-stop path).
	villagerResetSpecialPrices(v)
	if wheat.specialPriceDiff != 0 {
		t.Fatalf("wheat specialPriceDiff after reset = %d, want 0", wheat.specialPriceDiff)
	}
	if got := wheat.getModifiedCostCount(wheat.baseCostA); got != 20 {
		t.Fatalf("wheat cost after reset = %d, want 20", got)
	}
}

// TestUpdateDemandRaisesDemand: updateDemand accumulates demand = demand + uses - (maxUses - uses).
//
//	uses 10, maxUses 16: demand 0 + 10 - 6 = 4.
//	then uses 16:        demand 4 + 16 - 0 = 20.
//
//	[VERIFIED CFR MerchantOffer.updateDemand: this.demand = this.demand + this.uses - (this.maxUses - this.uses).]
func TestUpdateDemandRaisesDemand(t *testing.T) {
	v := NewEntity(6302, entity.Villager, 8.5, 64.0, 8.5)
	v.villagerProfession = "farmer"
	v.villagerLevel = 1

	offers := villagerGetOffers(v)
	wheat := offers[0] // maxUses 16

	wheat.uses = 10
	villagerUpdateDemand(v)
	if wheat.demand != 4 {
		t.Fatalf("demand after uses=10 restock = %d, want 4 (0 + 10 - (16-10))", wheat.demand)
	}

	// villagerRestock's resetUses would zero uses; here simulate a second heavily-used cycle directly.
	wheat.uses = 16
	villagerUpdateDemand(v)
	if wheat.demand != 20 {
		t.Fatalf("demand after uses=16 restock = %d, want 20 (4 + 16 - 0)", wheat.demand)
	}

	// A raised demand raises the modified cost via demandDiff = floor(base*demand*priceMultiplier).
	// base 20, demand 20, 0.05f: floor(20*20 * 0.05f) = floor(20.0) = 20; clamp(20 + 20 + 0, 1, maxStack 64) = 40.
	if got := wheat.getModifiedCostCount(wheat.baseCostA); got != 40 {
		t.Fatalf("wheat cost at demand 20 = %d, want 40 (20 + floor(400*0.05f))", got)
	}
}
