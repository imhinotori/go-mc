package server

// enchant_table_test.go — EnchantmentMenu validation gates. Each asserts the ported offer roll +
// apply against the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, net.minecraft.world.inventory.
// EnchantmentMenu.slotsChanged / clickMenuButton / getEnchantmentList + EnchantmentHelper.
// getEnchantmentCost / selectEnchantment). The bookshelf power scan, the seeded 3-offer costs, and the
// level+lapis consume on apply are jar-verified.

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// enchantTestLoop wires a TickLoop with all-air chunks around origin, an enchanting_table at pos, and a
// creative-off player with an OPEN enchant window at a fixed level/seed.
func enchantTestLoop(t *testing.T, xpLevel int32, seed int32) (*TickLoop, *tickPlayer, *openContainer, *world.ChunkManager, pk.Position) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	for _, cp := range []level.ChunkPos{{0, 0}, {-1, 0}, {0, -1}, {-1, -1}, {1, 0}, {0, 1}, {1, 1}, {-1, 1}, {1, -1}} {
		ch := level.EmptyChunk(blockTestSecs)
		ch.Status = level.StatusFull
		mgr.Insert(cp, ch)
	}
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:enchanting_table"], dimMinY)

	p := &tickPlayer{entityID: 1, gameMode: gameModeSurvival, experienceLevel: xpLevel}
	p.enchantmentSeed = seed
	oc := &openContainer{windowID: 1, kind: containerKindEnchant, enchantPos: pos}
	oc.enchantSeed = seed
	oc.enchantClueEnch = [3]int32{-1, -1, -1}
	oc.enchantClueLevel = [3]int32{-1, -1, -1}
	p.openContainer = oc
	loop.players = append(loop.players, p)
	return loop, p, oc, mgr, pos
}

// placeBookshelves surrounds the enchanting table with `n` bookshelves at the valid BOOKSHELF_OFFSETS
// (with air in the gap so isValidBookShelf passes). Returns the number actually placed (<= 15).
func placeBookshelves(mgr *world.ChunkManager, pos pk.Position, n int) int {
	shelf := block.DefaultStateID["minecraft:bookshelf"]
	placed := 0
	for _, off := range enchantBookshelfOffsets() {
		if placed >= n {
			break
		}
		// The gap block (pos + offset/2) must be a transmitter (air is #replaceable) — the all-air chunk
		// already satisfies it. Place the bookshelf at pos+offset.
		mgr.SetBlock(pk.Position{X: pos.X + off.x, Y: pos.Y + off.y, Z: pos.Z + off.z}, shelf, dimMinY)
		placed++
	}
	return placed
}

// TestEnchantBookshelfPowerScan: the bookshelf scan counts a valid bookshelf (a bookshelf at the offset
// with an air gap between it and the table). CITE EnchantingTableBlock.isValidBookShelf.
func TestEnchantBookshelfPowerScan(t *testing.T) {
	loop, _, _, mgr, pos := enchantTestLoop(t, 30, 12345)
	n := placeBookshelves(mgr, pos, 15)
	got := loop.enchantBookshelfPower(pos)
	if got != n {
		t.Fatalf("bookshelf power = %d, want %d (all placed shelves valid)", got, n)
	}
	if got != 15 {
		t.Fatalf("expected 15 bookshelves counted, got %d", got)
	}
}

// TestEnchantOffersSeeded: with an enchantable item + lapis + 15 bookshelves, slotsChanged produces 3
// non-zero offer costs whose values match the jar getEnchantmentCost formula for the seeded RNG stream.
// CITE EnchantmentMenu.slotsChanged + EnchantmentHelper.getEnchantmentCost.
func TestEnchantOffersSeeded(t *testing.T) {
	loop, _, oc, mgr, pos := enchantTestLoop(t, 30, 0xABCDEF)
	placeBookshelves(mgr, pos, 15)
	oc.enchantItem = mkEnchantableSword(0, nil) // an enchantable item (has ENCHANTABLE, empty ENCHANTMENTS)
	oc.enchantLapis = component.SlotData{ItemID: pk.VarInt(itemNameToID("lapis_lazuli")), Count: 3}

	loop.enchantSlotsChanged(oc)

	// Independently recompute the 3 costs via the jar formula from the SAME seed, and compare.
	random := newLegacyRandom(int64(oc.enchantSeed))
	for i := 0; i < 3; i++ {
		want := getEnchantmentCost(random, i, 15, oc.enchantItem)
		if want < i+1 {
			want = 0
		}
		if oc.enchantCosts[i] != want {
			t.Fatalf("offer %d cost = %d, want %d (seeded getEnchantmentCost)", i, oc.enchantCosts[i], want)
		}
	}
	// With 15 bookshelves the slot-2 cost is max(selected, 30) >= 30, so all three offers are non-zero.
	for i := 0; i < 3; i++ {
		if oc.enchantCosts[i] <= 0 {
			t.Fatalf("offer %d cost = %d, want > 0 (15 bookshelves)", i, oc.enchantCosts[i])
		}
	}
	if oc.enchantCosts[2] < 30 {
		t.Fatalf("slot-2 cost = %d, want >= 30 (max(selected, 15*2) with 15 bookshelves)", oc.enchantCosts[2])
	}
}

// TestEnchantApplyConsumesLevelsAndLapis: clicking offer 2 (the highest) applies the offered enchant(s)
// to the item and consumes exactly (buttonId+1) = 3 experience levels AND 3 lapis. CITE
// EnchantmentMenu.clickMenuButton (onEnchantmentPerformed(-cost=3) + currency.consume(3)).
func TestEnchantApplyConsumesLevelsAndLapis(t *testing.T) {
	loop, p, oc, mgr, pos := enchantTestLoop(t, 30, 0xABCDEF)
	placeBookshelves(mgr, pos, 15)
	oc.enchantItem = mkEnchantableSword(0, nil)
	oc.enchantLapis = component.SlotData{ItemID: pk.VarInt(itemNameToID("lapis_lazuli")), Count: 5}
	loop.enchantSlotsChanged(oc)

	button := 2
	if oc.enchantCosts[button] <= 0 {
		t.Fatalf("precondition: offer %d has no cost", button)
	}
	// Ensure the player has enough levels (>= button+1 and >= costs[button]).
	p.experienceLevel = int32(oc.enchantCosts[button]) + 5
	levelBefore := p.experienceLevel

	loop.enchantClickButton(p, oc, button)

	// levels consumed = button+1 = 3 (onEnchantmentPerformed).
	if p.experienceLevel != levelBefore-3 {
		t.Fatalf("level after enchant = %d, want %d (consumed button+1 = 3)", p.experienceLevel, levelBefore-3)
	}
	// lapis consumed = button+1 = 3 (from 5 -> 2).
	if oc.enchantLapis.Count != 2 {
		t.Fatalf("lapis after enchant = %d, want 2 (5 - 3)", oc.enchantLapis.Count)
	}
	// the item now carries at least one enchantment.
	applied := stackEnchantments(oc.enchantItem)
	if len(applied) == 0 {
		t.Fatal("no enchantment applied to the item after clicking a valid offer")
	}
}

// TestEnchantNotEnchantableNoOffers: an item with no ENCHANTABLE component (the v1 component seam)
// offers nothing (isEnchantable == has(ENCHANTABLE)). CITE EnchantmentMenu.slotsChanged
// (!isEnchantable -> clear costs).
func TestEnchantNotEnchantableNoOffers(t *testing.T) {
	loop, _, oc, mgr, pos := enchantTestLoop(t, 30, 0xABCDEF)
	placeBookshelves(mgr, pos, 15)
	// A bare stone with no ENCHANTABLE component: not enchantable.
	oc.enchantItem = component.SlotData{ItemID: pk.VarInt(itemNameToID("stone")), Count: 1}

	loop.enchantSlotsChanged(oc)

	for i := 0; i < 3; i++ {
		if oc.enchantCosts[i] != 0 {
			t.Fatalf("offer %d cost = %d, want 0 (item not enchantable)", i, oc.enchantCosts[i])
		}
	}
}
