package server

// villager_trade_test.go -- the villager TRADE OFFER MODEL unit surface (net.minecraft.world.item.trading
// .MerchantOffer / MerchantOffers + VillagerTrades FARMER level-1 data). Complements merchant_menu_test.go
// (which covers the menu SEAM end-to-end) by exercising the offer MODEL directly: the FARMER l1 listing
// content/counts/prices, satisfiedBy + take (shrink inputs, give result, increaseUses), the maxUses
// out-of-stock gate, and the demand/specialPriceDiff PRICE formula (getModifiedCostCount + updateDemand),
// all asserted 1:1 against the jar (temp/cache/26.2-inner.jar, javap -c -p this session).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// wheatStack builds a payment ItemStack of n wheat.
func wheatStack(n int) component.SlotData {
	return component.SlotData{Count: pk.VarInt(n), ItemID: pk.VarInt(item.Wheat.ID)}
}

// emeraldStack builds a payment ItemStack of n emeralds.
func emeraldStack(n int) component.SlotData {
	return component.SlotData{Count: pk.VarInt(n), ItemID: pk.VarInt(item.Emerald.ID)}
}

// TestOfferSatisfiedBy asserts MerchantOffer.satisfiedBy: enough of the right item satisfies; too few, the
// wrong item, or a non-empty buyB (for a single-cost offer) does not.
func TestOfferSatisfiedBy(t *testing.T) {
	o := newEmeraldForItems(item.Wheat, 20, 16, 2) // wheat x20 -> emerald, no costB
	empty := component.SlotData{Count: 0}

	if !o.satisfiedBy(wheatStack(20), empty) {
		t.Fatal("20 wheat should satisfy a wheat x20 offer")
	}
	if !o.satisfiedBy(wheatStack(64), empty) {
		t.Fatal("more than 20 wheat should satisfy (getCount >= modifiedCostCount)")
	}
	if o.satisfiedBy(wheatStack(19), empty) {
		t.Fatal("19 wheat must NOT satisfy a wheat x20 offer")
	}
	if o.satisfiedBy(emeraldStack(20), empty) {
		t.Fatal("emeralds must NOT satisfy a wheat offer (wrong item)")
	}
	// A single-cost offer requires buyB empty (satisfiedBy final branch: return buyB.isEmpty()).
	if o.satisfiedBy(wheatStack(20), emeraldStack(1)) {
		t.Fatal("a non-empty buyB must fail a single-cost offer (satisfiedBy buyB.isEmpty())")
	}
}

// TestOfferTakeShrinksAndGivesResult asserts MerchantOffer.take: it shrinks buyA by the modified cost and
// returns true (with the result available via the offer result), and that a too-small payment take fails.
func TestOfferTakeShrinksAndGivesResult(t *testing.T) {
	o := newEmeraldForItems(item.Wheat, 20, 16, 2)

	buyA := wheatStack(30)
	buyB := component.SlotData{Count: 0}
	if !o.take(&buyA, &buyB) {
		t.Fatal("take should succeed on a satisfied payment")
	}
	// buyA shrank by getCostA().count == getModifiedCostCount(baseCostA) == 20 (demand 0, specialPriceDiff 0).
	if int(buyA.Count) != 10 {
		t.Fatalf("buyA after take = %d, want 10 (30 - 20 cost)", buyA.Count)
	}
	// The offer result is 1 emerald (what the menu assembles into the result slot).
	if int(o.result.item.ID) != int(item.Emerald.ID) || o.result.count != 1 {
		t.Fatalf("offer result = %d x%d, want emerald x1", o.result.item.ID, o.result.count)
	}

	// A too-small payment: take fails and leaves the stack untouched.
	small := wheatStack(5)
	if o.take(&small, &buyB) {
		t.Fatal("take should fail on an unsatisfied (too-small) payment")
	}
	if int(small.Count) != 5 {
		t.Fatalf("failed take must not shrink the payment: got %d, want 5", small.Count)
	}
}

// TestOfferMaxUsesDisables asserts the isOutOfStock (uses >= maxUses) gate: increaseUses up to maxUses marks
// the offer out of stock.
func TestOfferMaxUsesDisables(t *testing.T) {
	o := newEmeraldForItems(item.Wheat, 20, 2, 2) // maxUses 2 for a fast exhaust
	if o.isOutOfStock() {
		t.Fatal("a fresh offer (uses 0) must not be out of stock")
	}
	o.increaseUses() // uses 1
	if o.isOutOfStock() {
		t.Fatal("uses 1 < maxUses 2: still in stock")
	}
	o.increaseUses() // uses 2 == maxUses
	if !o.isOutOfStock() {
		t.Fatalf("uses %d >= maxUses %d must be out of stock", o.uses, o.maxUses)
	}
}

// TestGetRecipeForSelectionHint asserts MerchantOffers.getRecipeFor: a scan returns the first satisfied
// offer index, a valid selectionHint (>0) restricts to that offer, and an unsatisfiable payment returns -1.
func TestGetRecipeForSelectionHint(t *testing.T) {
	offers := farmerLevel1Offers()
	empty := component.SlotData{Count: 0}

	// Scan (hint 0): wheat x20 matches offer 0.
	if idx := offers.getRecipeFor(wheatStack(20), empty, 0); idx != 0 {
		t.Fatalf("getRecipeFor(wheat) = %d, want 0", idx)
	}
	// Hint 1 (potato offer) with a wheat payment: the hint restricts to offer 1, which wheat does not
	// satisfy -> -1 (vanilla: hint>0 considers ONLY that offer).
	if idx := offers.getRecipeFor(wheatStack(20), empty, 1); idx != -1 {
		t.Fatalf("getRecipeFor(wheat, hint=1 potato) = %d, want -1", idx)
	}
	// Hint 1 with a satisfying potato payment: returns 1.
	potato := component.SlotData{Count: 26, ItemID: pk.VarInt(item.Potato.ID)}
	if idx := offers.getRecipeFor(potato, empty, 1); idx != 1 {
		t.Fatalf("getRecipeFor(potato, hint=1) = %d, want 1", idx)
	}
	// Unsatisfiable payment (too little wheat): -1.
	if idx := offers.getRecipeFor(wheatStack(1), empty, 0); idx != -1 {
		t.Fatalf("getRecipeFor(1 wheat) = %d, want -1", idx)
	}
}

// TestPriceDemandAdjustment asserts MerchantOffer.getModifiedCostCount + updateDemand -- the vanilla price
// economy formula:
//
//	demandDiff = max(0, Mth.floor((float)(basePrice*demand) * priceMultiplier))
//	modifiedCount = Mth.clamp(basePrice + demandDiff + specialPriceDiff, 1, maxStackSize)
//	updateDemand: demand = demand + uses - (maxUses - uses)
//
// Verified against the getModifiedCostCount + updateDemand bytecode.
func TestPriceDemandAdjustment(t *testing.T) {
	o := newEmeraldForItems(item.Wheat, 20, 16, 2) // base 20, priceMultiplier 0.05

	// Default (demand 0, specialPriceDiff 0): modified count == base 20.
	if got := o.getModifiedCostCount(o.baseCostA); got != 20 {
		t.Fatalf("modified count (demand 0) = %d, want 20 (== base)", got)
	}

	// A positive demand raises the cost: demand 10 -> demandDiff = floor(20*10 * 0.05) = floor(10.0) = 10,
	// so modified = clamp(20 + 10 + 0, 1, 64) = 30.
	o.demand = 10
	if got := o.getModifiedCostCount(o.baseCostA); got != 30 {
		t.Fatalf("modified count (demand 10) = %d, want 30 (base 20 + demandDiff 10)", got)
	}

	// specialPriceDiff (a reputation discount is negative) lowers the cost. -5 -> clamp(20+10-5) = 25.
	o.specialPriceDiff = -5
	if got := o.getModifiedCostCount(o.baseCostA); got != 25 {
		t.Fatalf("modified count (demand 10, special -5) = %d, want 25", got)
	}

	// The clamp floor is 1: a huge discount cannot drop below 1.
	o.demand = 0
	o.specialPriceDiff = -1000
	if got := o.getModifiedCostCount(o.baseCostA); got != 1 {
		t.Fatalf("modified count (special -1000) = %d, want 1 (clamp floor)", got)
	}

	// updateDemand: demand = demand + uses - (maxUses - uses). Fully-used offer raises demand.
	adj := newEmeraldForItems(item.Wheat, 20, 16, 2)
	adj.demand = 0
	adj.uses = 16 // fully used
	adj.updateDemand()
	// 0 + 16 - (16 - 16) = 16.
	if adj.demand != 16 {
		t.Fatalf("updateDemand (uses 16/16) demand = %d, want 16", adj.demand)
	}
	// An unused offer LOWERS demand: demand 16, uses 0 -> 16 + 0 - (16 - 0) = 0.
	adj.uses = 0
	adj.updateDemand()
	if adj.demand != 0 {
		t.Fatalf("updateDemand (uses 0/16) demand = %d, want 0", adj.demand)
	}
}
