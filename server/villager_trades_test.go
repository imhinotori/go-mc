package server

// villager_trades_test.go -- the PROFESSION TRADE TABLES + listing-type helpers (VillagerTrades
// data-driven trade sets ported per (profession, level)). Complements villager_trade_test.go (the offer
// MODEL) by asserting the per-profession listing CONTENT, prices, maxUses, xp and discount 1:1 against the
// base villager_trade datapack (data/minecraft/villager_trade/<prof>/<level>/*.json, this session).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
)

// findOffer returns the first offer in the set whose result item + baseCostA item match, or nil.
func findOffer(offers merchantOffers, costItemID, resultItemID int32) *merchantOffer {
	for _, o := range offers {
		if int32(o.baseCostA.item.ID) == costItemID && int32(o.result.item.ID) == resultItemID {
			return o
		}
	}
	return nil
}

// TestLibrarianLevel1Offers asserts the LIBRARIAN novice set: paper x24 -> emerald (max_uses 16, xp 2,
// discount 0.05) AND the enchanted-book listing (emerald + book -> enchanted_book, max_uses 12, discount
// 0.2). VERIFIED datapack librarian/1/{paper_emerald,emerald_and_book_enchanted_book,emerald_bookshelf}.json.
func TestLibrarianLevel1Offers(t *testing.T) {
	offers := villagerOffersFor("librarian", 1)
	if offers.isEmpty() {
		t.Fatal("librarian/1 must have offers")
	}
	// paper x24 -> emerald
	paper := findOffer(offers, int32(item.Paper.ID), int32(item.Emerald.ID))
	if paper == nil {
		t.Fatal("librarian/1 missing paper->emerald")
	}
	if paper.baseCostA.count != 24 || paper.maxUses != 16 || paper.xp != 2 || paper.priceMultiplier != 0.05 {
		t.Fatalf("paper->emerald = cost %d mu %d xp %d disc %v, want 24/16/2/0.05",
			paper.baseCostA.count, paper.maxUses, paper.xp, paper.priceMultiplier)
	}
	// enchanted-book listing: emerald baseCostA + book costB -> enchanted_book
	book := findOffer(offers, int32(item.Emerald.ID), int32(item.EnchantedBook.ID))
	if book == nil {
		t.Fatal("librarian/1 missing enchanted-book listing")
	}
	if book.costB == nil || int32(book.costB.item.ID) != int32(item.Book.ID) || book.costB.count != 1 {
		t.Fatalf("enchanted-book costB = %v, want book x1", book.costB)
	}
	if book.maxUses != 12 || book.priceMultiplier != 0.2 {
		t.Fatalf("enchanted-book mu %d disc %v, want 12/0.2", book.maxUses, book.priceMultiplier)
	}
	if int32(book.result.item.ID) != int32(item.EnchantedBook.ID) || book.result.count != 1 {
		t.Fatalf("enchanted-book result = %d x%d, want enchanted_book x1", book.result.item.ID, book.result.count)
	}
}

// TestEnchantBookCostFormula pins the enchanted-book listing's cost SHAPE: baseCostA is emerald with the
// datapack wants count (0 in the base data), the enchant additional-cost is a DEFERRED stub (returns 0), and
// the effective modified cost clamps to the vanilla min of 1 (getModifiedCostCount clamp[1,maxStack]). When
// the enchant subsystem lands, enchantAdditionalCost supplies 2+random.nextInt(5+level*10) (doubled for a
// double_trade_price enchant), so baseCostA.count += that. VERIFIED VillagerTrade.getOffer + the librarian
// emerald_and_book_enchanted_book.json (wants emerald count 0).
func TestEnchantBookCostFormula(t *testing.T) {
	book := findOffer(villagerOffersFor("librarian", 1), int32(item.Emerald.ID), int32(item.EnchantedBook.ID))
	if book == nil {
		t.Fatal("no enchanted-book listing")
	}
	// datapack base wants emerald count == 0.
	if book.baseCostA.count != 0 {
		t.Fatalf("enchanted-book base emerald count = %d, want 0 (price from deferred additional cost)", book.baseCostA.count)
	}
	// The deferred enchant additional cost is 0 in v1.
	if got := enchantAdditionalCost(nil, 5); got != 0 {
		t.Fatalf("enchantAdditionalCost stub = %d, want 0 (deferred)", got)
	}
	// getModifiedCostCount clamps a 0 base to the vanilla minimum of 1.
	if got := book.getModifiedCostCount(book.baseCostA); got != 1 {
		t.Fatalf("modified enchanted-book cost = %d, want 1 (clamp min)", got)
	}
}

// TestClericLevel1Offers asserts the CLERIC novice set: rotten_flesh x32 -> emerald (max_uses 16, xp 2) and
// emerald x1 -> redstone x2. VERIFIED datapack cleric/1/{rotten_flesh_emerald,emerald_redstone}.json.
func TestClericLevel1Offers(t *testing.T) {
	offers := villagerOffersFor("cleric", 1)
	rf := findOffer(offers, int32(item.RottenFlesh.ID), int32(item.Emerald.ID))
	if rf == nil {
		t.Fatal("cleric/1 missing rotten_flesh->emerald")
	}
	if rf.baseCostA.count != 32 || rf.maxUses != 16 || rf.xp != 2 {
		t.Fatalf("rotten_flesh->emerald = cost %d mu %d xp %d, want 32/16/2", rf.baseCostA.count, rf.maxUses, rf.xp)
	}
	rs := findOffer(offers, int32(item.Emerald.ID), int32(item.Redstone.ID))
	if rs == nil || rs.result.count != 2 {
		t.Fatalf("cleric/1 missing emerald->redstone x2 (got %v)", rs)
	}
}

// TestWeaponsmithIronSwordOffer asserts the WEAPONSMITH novice set offers an enchanted iron sword for
// emeralds (emerald x2 -> iron_sword, the enchant_with_levels listing, max_uses 12, discount 0.2) plus the
// plain iron axe (emerald x3 -> iron_axe). VERIFIED datapack weaponsmith/1/{emerald_enchanted_iron_sword,
// emerald_iron_axe}.json.
func TestWeaponsmithIronSwordOffer(t *testing.T) {
	offers := villagerOffersFor("weaponsmith", 1)
	sword := findOffer(offers, int32(item.Emerald.ID), int32(item.IronSword.ID))
	if sword == nil {
		t.Fatal("weaponsmith/1 missing emerald->iron_sword")
	}
	if sword.baseCostA.count != 2 || sword.maxUses != 12 || sword.priceMultiplier != 0.2 {
		t.Fatalf("iron_sword = cost %d mu %d disc %v, want 2/12/0.2", sword.baseCostA.count, sword.maxUses, sword.priceMultiplier)
	}
	axe := findOffer(offers, int32(item.Emerald.ID), int32(item.IronAxe.ID))
	if axe == nil || axe.baseCostA.count != 3 {
		t.Fatalf("weaponsmith/1 missing emerald x3 -> iron_axe (got %v)", axe)
	}
	// weaponsmith/2 = ONLY the shared #common_smith/level_2 tag (iron_ingot_emerald + emerald_bell); there
	// are no weaponsmith-specific L2 trades. VERIFIED data/minecraft/tags/villager_trade/weaponsmith/level_2.json.
	l2 := villagerOffersFor("weaponsmith", 2)
	if len(l2) != 2 {
		t.Fatalf("weaponsmith/2 want 2 common_smith offers, got %d", len(l2))
	}
	if ii := findOffer(l2, int32(item.IronIngot.ID), int32(item.Emerald.ID)); ii == nil || ii.baseCostA.count != 4 || ii.maxUses != 12 || ii.xp != 10 || ii.priceMultiplier != 0.05 {
		t.Fatalf("weaponsmith/2 iron_ingot x4 -> emerald wrong (got %v)", ii)
	}
	if b := findOffer(l2, int32(item.Emerald.ID), int32(item.Bell.ID)); b == nil || b.baseCostA.count != 36 || b.maxUses != 12 || b.xp != 5 || b.priceMultiplier != 0.2 {
		t.Fatalf("weaponsmith/2 emerald x36 -> bell wrong (got %v)", b)
	}
}

// TestSmithCommonLevel2 asserts all three smith professions include the shared #common_smith/level_2 trades
// at level 2, and that armorer/2 ADDITIONALLY carries its two chainmail trades AFTER the common ones.
// VERIFIED data/minecraft/tags/villager_trade/{armorer,weaponsmith,toolsmith}/level_2.json +
// data/minecraft/tags/villager_trade/common_smith/level_2.json.
func TestSmithCommonLevel2(t *testing.T) {
	for _, prof := range []string{"armorer", "weaponsmith", "toolsmith"} {
		l2 := villagerOffersFor(prof, 2)
		if ii := findOffer(l2, int32(item.IronIngot.ID), int32(item.Emerald.ID)); ii == nil || ii.baseCostA.count != 4 || ii.xp != 10 {
			t.Fatalf("%s/2 missing iron_ingot x4 -> emerald (got %v)", prof, ii)
		}
		if b := findOffer(l2, int32(item.Emerald.ID), int32(item.Bell.ID)); b == nil || b.baseCostA.count != 36 || b.xp != 5 {
			t.Fatalf("%s/2 missing emerald x36 -> bell (got %v)", prof, b)
		}
	}
	// weaponsmith/2 and toolsmith/2 are ONLY the two common trades.
	if len(villagerOffersFor("toolsmith", 2)) != 2 {
		t.Fatalf("toolsmith/2 want exactly 2 offers, got %d", len(villagerOffersFor("toolsmith", 2)))
	}
	// armorer/2 = common (iron_ingot_emerald, emerald_bell) FIRST, then chainmail boots + leggings.
	a2 := villagerOffersFor("armorer", 2)
	if len(a2) != 4 {
		t.Fatalf("armorer/2 want 4 offers (2 common + 2 chainmail), got %d", len(a2))
	}
	if int32(a2[0].baseCostA.item.ID) != int32(item.IronIngot.ID) {
		t.Fatalf("armorer/2[0] want iron_ingot cost (common trade first), got %v", a2[0].baseCostA.item)
	}
	if findOffer(a2, int32(item.Emerald.ID), int32(item.ChainmailBoots.ID)) == nil {
		t.Fatal("armorer/2 missing emerald -> chainmail_boots")
	}
}

// TestFarmerLevels2Through5 asserts the FARMER levels 2..5 landed (extending the existing level-1 set) with
// representative listings + prices. VERIFIED datapack farmer/{2,3,4,5}/*.json.
func TestFarmerLevels2Through5(t *testing.T) {
	// level 2: pumpkin x6 -> emerald (mu 12, xp 10), emerald -> apple x4.
	l2 := villagerOffersFor("farmer", 2)
	if p := findOffer(l2, int32(item.Pumpkin.ID), int32(item.Emerald.ID)); p == nil || p.baseCostA.count != 6 || p.xp != 10 {
		t.Fatalf("farmer/2 pumpkin->emerald wrong (got %v)", p)
	}
	if a := findOffer(l2, int32(item.Emerald.ID), int32(item.Apple.ID)); a == nil || a.result.count != 4 {
		t.Fatalf("farmer/2 emerald->apple x4 missing (got %v)", a)
	}
	// level 3: melon x4 -> emerald (xp 20); emerald x3 -> cookie x18.
	l3 := villagerOffersFor("farmer", 3)
	if m := findOffer(l3, int32(item.Melon.ID), int32(item.Emerald.ID)); m == nil || m.baseCostA.count != 4 || m.xp != 20 {
		t.Fatalf("farmer/3 melon->emerald wrong (got %v)", m)
	}
	if c := findOffer(l3, int32(item.Emerald.ID), int32(item.Cookie.ID)); c == nil || c.baseCostA.count != 3 || c.result.count != 18 {
		t.Fatalf("farmer/3 emerald x3 -> cookie x18 wrong (got %v)", c)
	}
	// level 4: emerald x3 -> cake x1 (xp 15).
	l4 := villagerOffersFor("farmer", 4)
	if ck := findOffer(l4, int32(item.Emerald.ID), int32(item.Cake.ID)); ck == nil || ck.baseCostA.count != 3 || ck.xp != 15 {
		t.Fatalf("farmer/4 emerald x3 -> cake wrong (got %v)", ck)
	}
	// level 5: emerald x3 -> golden_carrot x3 (xp 30).
	l5 := villagerOffersFor("farmer", 5)
	if gc := findOffer(l5, int32(item.Emerald.ID), int32(item.GoldenCarrot.ID)); gc == nil || gc.baseCostA.count != 3 || gc.result.count != 3 || gc.xp != 30 {
		t.Fatalf("farmer/5 emerald x3 -> golden_carrot x3 wrong (got %v)", gc)
	}
}

// TestFishermanProcessTrade asserts the FISHERMAN novice raw-cod process trade (cod x6 + emerald x1 ->
// cooked_cod x6) uses baseCostA=cod, costB=emerald (the additional_wants path). VERIFIED datapack
// fisherman/1/raw_cod_and_emerald_cooked_cod.json.
func TestFishermanProcessTrade(t *testing.T) {
	offers := villagerOffersFor("fisherman", 1)
	proc := findOffer(offers, int32(item.Cod.ID), int32(item.CookedCod.ID))
	if proc == nil {
		t.Fatal("fisherman/1 missing raw_cod process trade")
	}
	if proc.baseCostA.count != 6 || proc.costB == nil || int32(proc.costB.item.ID) != int32(item.Emerald.ID) || proc.costB.count != 1 {
		t.Fatalf("raw_cod trade = cod x%d + costB %v, want cod x6 + emerald x1", proc.baseCostA.count, proc.costB)
	}
	if proc.result.count != 6 {
		t.Fatalf("raw_cod trade result = %d, want cooked_cod x6", proc.result.count)
	}
}

// TestShepherdWoolAndDye asserts the SHEPHERD sets: level 1 white_wool x18 -> emerald (xp 2), level 2
// emerald -> white_wool x1 (dyed-wool listing). VERIFIED datapack shepherd/1/white_wool_emerald.json +
// shepherd/2/emerald_white_wool.json.
func TestShepherdWoolAndDye(t *testing.T) {
	l1 := villagerOffersFor("shepherd", 1)
	if w := findOffer(l1, int32(item.WhiteWool.ID), int32(item.Emerald.ID)); w == nil || w.baseCostA.count != 18 || w.xp != 2 {
		t.Fatalf("shepherd/1 white_wool->emerald wrong (got %v)", w)
	}
	l2 := villagerOffersFor("shepherd", 2)
	if w := findOffer(l2, int32(item.Emerald.ID), int32(item.WhiteWool.ID)); w == nil || w.baseCostA.count != 1 {
		t.Fatalf("shepherd/2 emerald->white_wool missing (got %v)", w)
	}
}

// TestProfessionCoverage asserts every priority profession returns a non-empty set for its populated levels
// and empty for an unknown profession/level. Pins the villagerOffersFor routing table.
func TestProfessionCoverage(t *testing.T) {
	populated := map[string][]int{
		"farmer": {1, 2, 3, 4, 5}, "fisherman": {1, 2, 3, 4, 5}, "shepherd": {1, 2, 3, 4, 5},
		"fletcher": {1, 2, 3, 4, 5}, "librarian": {1, 2, 3, 4, 5}, "cartographer": {1, 2, 3, 4, 5},
		"cleric": {1, 2, 3, 4, 5}, "armorer": {1, 2, 3, 4, 5}, "weaponsmith": {1, 2, 3, 4, 5},
		"toolsmith": {1, 2, 3, 4, 5}, "butcher": {1, 2, 3, 4, 5}, "leatherworker": {1, 2, 3, 4, 5},
		"mason": {1, 2, 3, 4, 5},
	}
	for prof, levels := range populated {
		for _, lvl := range levels {
			if villagerOffersFor(prof, lvl).isEmpty() {
				t.Fatalf("%s/%d must have offers", prof, lvl)
			}
		}
	}
	if !villagerOffersFor("nitwit", 1).isEmpty() {
		t.Fatal("unknown profession must be empty")
	}
	if !villagerOffersFor("weaponsmith", 2).isEmpty() {
		t.Fatal("weaponsmith/2 must be empty (no base data)")
	}
}


// TestVillagerXpThresholds pins VillagerData.canLevelUp + get{Min,Max}XpPerLevel against the jar
// NEXT_LEVEL_XP_THRESHOLDS = {0,10,70,150,250}. VERIFIED VillagerData static init + canLevelUp/getMaxXpPerLevel.
func TestVillagerXpThresholds(t *testing.T) {
	// canLevelUp: true for levels 1..4, false for <1 and >=5.
	for lvl, want := range map[int]bool{0: false, 1: true, 2: true, 3: true, 4: true, 5: false, 6: false} {
		if villagerCanLevelUp(lvl) != want {
			t.Fatalf("canLevelUp(%d) = %v, want %v", lvl, villagerCanLevelUp(lvl), want)
		}
	}
	// getMaxXpPerLevel(level) = canLevelUp ? NEXT_LEVEL_XP_THRESHOLDS[level] : 0.
	maxWant := map[int]int{1: 10, 2: 70, 3: 150, 4: 250, 5: 0}
	for lvl, want := range maxWant {
		if got := villagerGetMaxXpPerLevel(lvl); got != want {
			t.Fatalf("getMaxXpPerLevel(%d) = %d, want %d", lvl, got, want)
		}
	}
	// getMinXpPerLevel(level) = canLevelUp ? NEXT_LEVEL_XP_THRESHOLDS[level-1] : 0.
	minWant := map[int]int{1: 0, 2: 10, 3: 70, 4: 150, 5: 0}
	for lvl, want := range minWant {
		if got := villagerGetMinXpPerLevel(lvl); got != want {
			t.Fatalf("getMinXpPerLevel(%d) = %d, want %d", lvl, got, want)
		}
	}
}

// TestVillagerShouldIncreaseLevel pins Villager.shouldIncreaseLevel: canLevelUp(level) && villagerXp >=
// getMaxXpPerLevel(level). A level-1 villager needs 10 XP; a level-5 villager never levels.
func TestVillagerShouldIncreaseLevel(t *testing.T) {
	e := &Entity{villagerProfession: "farmer", villagerLevel: 1, villagerXp: 9}
	if villagerShouldIncreaseLevel(e) {
		t.Fatal("level 1 with 9 XP (<10) must NOT level up")
	}
	e.villagerXp = 10
	if !villagerShouldIncreaseLevel(e) {
		t.Fatal("level 1 with 10 XP (>=10) must be eligible to level up")
	}
	// level 5 (max) never levels even with huge XP.
	e5 := &Entity{villagerProfession: "farmer", villagerLevel: 5, villagerXp: 9999}
	if villagerShouldIncreaseLevel(e5) {
		t.Fatal("level 5 (max) must never level up")
	}
}

// TestVillagerIncreaseMerchantCareer pins increaseMerchantCareer: level+1 (clamped) and the next level's
// trade set APPENDED to the existing offers (offers accumulate, matching addOffersFromTradeSet).
func TestVillagerIncreaseMerchantCareer(t *testing.T) {
	e := &Entity{villagerProfession: "farmer", villagerLevel: 1}
	l1 := villagerGetOffers(e) // build the initial farmer/1 set
	l1n := len(l1)
	if l1n == 0 {
		t.Fatal("farmer/1 must build a non-empty offer set")
	}
	villagerIncreaseMerchantCareer(e)
	if e.villagerLevel != 2 {
		t.Fatalf("level after career-up = %d, want 2", e.villagerLevel)
	}
	// offers must have GROWN by the farmer/2 count (appended, not rebuilt).
	l2Count := len(farmerLevel2Offers())
	if len(e.offers) != l1n+l2Count {
		t.Fatalf("offers after level-up = %d, want %d (l1 %d + l2 %d)", len(e.offers), l1n+l2Count, l1n, l2Count)
	}
	// level 5 cap: repeated career-ups never exceed 5.
	e.villagerLevel = 5
	villagerIncreaseMerchantCareer(e)
	if e.villagerLevel != 5 {
		t.Fatalf("career-up past max = %d, want clamp 5", e.villagerLevel)
	}
}


// TestVillagerRestockScheduling pins the twice-a-day restock cadence: needsToRestock (any offer uses>0),
// allowedToRestock (1st free, 2nd needs >2400t gap, 3rd never), shouldRestock (12000t window / day-boundary
// reset), and restock (updateDemand + resetUses + scheduling bookkeeping). VERIFIED Villager.shouldRestock/
// allowedToRestock/needsToRestock/restock + MerchantOffer.needsRestock.
func TestVillagerRestockScheduling(t *testing.T) {
	e := &Entity{villagerProfession: "farmer", villagerLevel: 1}
	offers := villagerGetOffers(e)
	if len(offers) == 0 {
		t.Fatal("farmer/1 must have offers")
	}

	// needsToRestock: false with all uses 0; true once any offer has uses>0.
	if villagerNeedsToRestock(e) {
		t.Fatal("fresh offers (uses 0) must NOT need restock")
	}
	offers[0].uses = 1
	if !villagerNeedsToRestock(e) {
		t.Fatal("an offer with uses>0 must need restock")
	}

	// allowedToRestock: numberOfRestocksToday==0 -> always allowed.
	if !villagerAllowedToRestock(e, 5000) {
		t.Fatal("0 restocks today must be allowed")
	}
	// After 1 restock: a 2nd is allowed only >2400t after lastRestockGameTime.
	e.numberOfRestocksToday = 1
	e.lastRestockGameTime = 5000
	if villagerAllowedToRestock(e, 5000+2400) {
		t.Fatal("2nd restock at exactly +2400 must NOT be allowed (strict >)")
	}
	if !villagerAllowedToRestock(e, 5000+2401) {
		t.Fatal("2nd restock at +2401 must be allowed")
	}
	// After 2 restocks: never allowed (2x/day cap).
	e.numberOfRestocksToday = 2
	if villagerAllowedToRestock(e, 5000+99999) {
		t.Fatal("3rd restock must never be allowed (cap 2)")
	}

	// restock: resets uses to 0, stamps lastRestockGameTime, increments numberOfRestocksToday.
	e.numberOfRestocksToday = 0
	offers[0].uses = 3
	villagerRestock(e, 8000)
	if offers[0].uses != 0 {
		t.Fatalf("restock must resetUses to 0, got %d", offers[0].uses)
	}
	if e.lastRestockGameTime != 8000 || e.numberOfRestocksToday != 1 {
		t.Fatalf("restock bookkeeping wrong: lastRestock %d, count %d (want 8000/1)", e.lastRestockGameTime, e.numberOfRestocksToday)
	}
}

// TestVillagerShouldRestockWindow pins shouldRestock's 12000t window + day-boundary reset side effects.
func TestVillagerShouldRestockWindow(t *testing.T) {
	e := &Entity{villagerProfession: "farmer", villagerLevel: 1}
	offers := villagerGetOffers(e)
	offers[0].uses = 1 // needsToRestock == true

	// First call: lastRestockGameTime 0, gameTime 100 (< 12000 window). lastRestockCheckDay is 0 so the
	// day-boundary OR is suppressed on the very first check. flag == (100 > 12000) == false -> no reset.
	// allowedToRestock (0 restocks) && needsToRestock -> true.
	if !villagerShouldRestock(e, 100) {
		t.Fatal("first shouldRestock with a depleted offer must be true")
	}
	if e.lastRestockCheckDay != 0 { // day(100) = 100/24000 = 0
		t.Fatalf("lastRestockCheckDay after gameTime 100 = %d, want 0", e.lastRestockCheckDay)
	}

	// Simulate a restock, then a gameTime PAST the 12000 window -> flag true -> numberOfRestocksToday reset.
	e.numberOfRestocksToday = 2
	e.lastRestockGameTime = 100
	if !villagerShouldRestock(e, 100+12001) { // 12001 > 100+12000
		t.Fatal("shouldRestock past the 12000t window must reset + return true")
	}
	if e.numberOfRestocksToday != 0 {
		t.Fatalf("window-cross must reset numberOfRestocksToday to 0, got %d", e.numberOfRestocksToday)
	}

	// Day-boundary reset: prime lastRestockCheckDay to a prior day, then a later day triggers reset even
	// inside the 12000t window.
	e.numberOfRestocksToday = 2
	e.lastRestockGameTime = 24100
	e.lastRestockCheckDay = 1 // day 1
	// gameTime in day 2 (48001/24000 = 2) but within 12000t of lastRestockGameTime(24100): window says no,
	// day-boundary says yes.
	if !villagerShouldRestock(e, 25000+24000) { // day = 49000/24000 = 2 > 1
		t.Fatal("crossing a day boundary must reset + allow restock")
	}
	if e.numberOfRestocksToday != 0 {
		t.Fatalf("day-boundary must reset numberOfRestocksToday to 0, got %d", e.numberOfRestocksToday)
	}
}
