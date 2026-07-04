package server

// grindstone_test.go — GRINDSTONE MENU (GrindstoneMenu) tested 1:1 against the jar. The scenarios prove:
//   - open: right-clicking a grindstone opens a 39-slot window (menu id minecraft:grindstone).
//   - DISENCHANT: a single enchanted item -> the result strips non-curse enchants (keeps the item); on
//     take, the disenchant XP is returned as orbs (getExperienceAmount) and the input is consumed.
//   - REPAIR: two damaged items of the same type -> the result durability = the +5% bonus merge formula
//     (mergeItems), with enchants stripped.
//   - curse-keeping: a curse enchant survives the strip.
//
// All numeric assertions are checked against GrindstoneMenu.computeResult / mergeItems / getExperienceAmount
// (temp/cache/26.2-inner.jar).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

const (
	idDiamondSword = 964 // minecraft:diamond_sword
	idBook         = 1058 // minecraft:book
	idEnchantedBook = 1274 // minecraft:enchanted_book

	// enchantment WIRE ids (registrydata enchantment registry order == EnchantmentOrder index).
	enchSharpness   = 33 // min_cost {base 1, per_level 11} -> getMinCost(1)=1
	enchUnbreaking  = 40 // min_cost {base 5, per_level 8}  -> getMinCost(3)=21
	enchBindingCurse = 2 // #minecraft:enchantment/curse
)

// enchantedStack builds a SlotData carrying the ENCHANTMENTS component with the given (id,level) pairs.
func enchantedStack(itemID, count int, ench ...component.EnchantmentEntry) component.SlotData {
	s := component.SlotData{ItemID: pk.VarInt(itemID), Count: pk.VarInt(count)}
	var p component.Patch
	p.Set(compEnchantments, &component.Enchantments{Enchantments: ench})
	return p.ApplyTo(s)
}

// damageableStack builds a damageable SlotData: MAX_DAMAGE + DAMAGE components (isDamageableItem true),
// optionally carrying enchantments.
func damageableStack(itemID, count, maxDamage, damage int, ench ...component.EnchantmentEntry) component.SlotData {
	s := component.SlotData{ItemID: pk.VarInt(itemID), Count: pk.VarInt(count)}
	var p component.Patch
	p.Set(compMaxDamage, &component.MaxDamage{VarInt: pk.VarInt(maxDamage)})
	p.Set(compDamage, &component.Damage{VarInt: pk.VarInt(damage)})
	if len(ench) > 0 {
		p.Set(compEnchantments, &component.Enchantments{Enchantments: ench})
	}
	return p.ApplyTo(s)
}

// grindstoneLoop builds a block loop + a survival player with a grindstone block at the returned pos,
// and opens the grindstone window on that player.
func grindstoneLoop(t *testing.T) (*TickLoop, *tickPlayer, pk.Position) {
	t.Helper()
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	loop.only().world.SetBlock(pos, block.ToStateID[block.Grindstone{Face: block.AttachFaceFloor, Facing: 2}], dimMinY)
	return loop, p, pos
}

// TestGrindstoneOpen: right-clicking a grindstone opens its 39-slot menu.
func TestGrindstoneOpen(t *testing.T) {
	loop, p, pos := grindstoneLoop(t)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if p.openContainer == nil {
		t.Fatal("openContainer is nil after grindstone open")
	}
	if p.openContainer.kind != containerKindGrindstone {
		t.Fatalf("openContainer kind = %d, want grindstone", p.openContainer.kind)
	}
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundOpenScreen); n != 1 {
		t.Fatalf("ClientboundOpenScreen sent %d times, want 1", n)
	}
	wantMenu := menuTypeID(registryid.Menu, "minecraft:grindstone")
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundOpenScreen) {
			continue
		}
		var win, menuID pk.VarInt
		if err := packet.Scan(&win, &menuID); err != nil {
			t.Fatalf("OpenScreen scan: %v", err)
		}
		if int32(menuID) != wantMenu {
			t.Fatalf("OpenScreen menu id = %d, want grindstone (%d)", menuID, wantMenu)
		}
	}
}

// TestGrindstoneDisenchant: a single sharpness-3 diamond sword in input slot 0 -> the result is the same
// sword with the enchant stripped; taking the result returns the disenchant XP as orbs + consumes the input.
func TestGrindstoneDisenchant(t *testing.T) {
	loop, p, pos := grindstoneLoop(t)
	inv := ensureInventory(p)
	oc := &openContainer{windowID: 1, kind: containerKindGrindstone, grindPos: pos}
	p.openContainer = oc

	// Place a sharpness-3 diamond sword in input slot 0.
	oc.grind0 = enchantedStack(idDiamondSword, 1, component.EnchantmentEntry{ID: enchSharpness, Level: 3})

	// createResult (computeResult with one enchanted item -> removeNonCursesFrom(copy)).
	loop.grindstoneCreateResult(oc)
	res := oc.grindResult
	if stackEmpty(res) {
		t.Fatal("disenchant result is empty; want the stripped sword")
	}
	if int(res.ItemID) != idDiamondSword {
		t.Fatalf("result item = %d, want diamond_sword (%d)", res.ItemID, idDiamondSword)
	}
	if stackHasAnyEnchantments(res) {
		t.Fatal("result still carries enchantments after disenchant; want them stripped")
	}

	// getExperienceFromItem for a lone sharpness-3 = getMinCost(sharpness,3) = 1 + 11*(3-1) = 23. This is
	// the DETERMINISTIC half; getExperienceAmount then returns half + nextInt(half) where half=ceil(23/2)=12.
	if got := grindstoneExperienceFromItem(oc.grind0); got != 23 {
		t.Fatalf("getExperienceFromItem(sharpness 3) = %d, want 23 (min_cost 1 + 11*2)", got)
	}
	orbsBefore := sumOrbXP(loop)

	// Take the result (PICKUP primary on the result slot 2).
	loop.grindstonePickup(p, oc, inv, 2, 0)
	loop.grindstoneCreateResult(oc)

	// The result went to the cursor.
	if c := inv.getCarried(); int(c.ItemID) != idDiamondSword || c.Count != 1 {
		t.Fatalf("carried after take = id=%d count=%d, want diamond_sword x1", c.ItemID, c.Count)
	}
	// The input was consumed.
	if !stackEmpty(oc.grind0) {
		t.Fatalf("input slot 0 not consumed after take: %+v", oc.grind0)
	}
	// The disenchant XP was awarded as orbs: getExperienceAmount = half + nextInt(half), half=12, so the
	// awarded total is in [12, 23] (the region's seeded levelRandom draws the bonus).
	got := sumOrbXP(loop) - orbsBefore
	if got < 12 || got > 23 {
		t.Fatalf("disenchant XP awarded = %d, want in [12,23] (half=ceil(23/2)=12 + nextInt(12))", got)
	}
}

// TestGrindstoneRepair: two damaged diamond swords merged -> the result durability follows the +5% bonus
// formula (mergeItems). VERIFIED against GrindstoneMenu.mergeItems.
func TestGrindstoneRepair(t *testing.T) {
	loop, p, pos := grindstoneLoop(t)
	oc := &openContainer{windowID: 1, kind: containerKindGrindstone, grindPos: pos}
	p.openContainer = oc

	// Two diamond swords (maxDamage 1561), each 500 damaged -> each has 1061 remaining durability.
	const maxDmg = 1561
	oc.grind0 = damageableStack(idDiamondSword, 1, maxDmg, 500)
	oc.grind1 = damageableStack(idDiamondSword, 1, maxDmg, 500)

	loop.grindstoneCreateResult(oc)
	res := oc.grindResult
	if stackEmpty(res) {
		t.Fatal("repair result is empty; want the merged sword")
	}
	if int(res.ItemID) != idDiamondSword {
		t.Fatalf("result item = %d, want diamond_sword", res.ItemID)
	}
	// mergeItems: durability = max(1561,1561) = 1561; remaining = (1561-500)+(1561-500)+1561*5/100 =
	// 1061+1061+78 = 2200; newItem damageValue = max(1561 - 2200, 0) = 0 (fully repaired).
	if got := stackMaxDamage(res); got != maxDmg {
		t.Fatalf("result max_damage = %d, want %d", got, maxDmg)
	}
	if got := stackDamageValue(res); got != 0 {
		t.Fatalf("result damage = %d, want 0 (remaining 2200 >= durability 1561 -> fully repaired)", got)
	}
}

// TestGrindstoneRepairPartial: two lightly-repaired swords whose combined remaining does NOT exceed the
// durability -> the result carries a non-zero damage (durability - remaining), proving the exact formula.
func TestGrindstoneRepairPartial(t *testing.T) {
	loop, _, pos := grindstoneLoop(t)
	oc := &openContainer{windowID: 1, kind: containerKindGrindstone, grindPos: pos}

	const maxDmg = 1561
	// Each sword: damage 1400 -> remaining 161. remaining sum = 161+161 + 1561*5/100(=78) = 400.
	// damageValue = max(1561 - 400, 0) = 1161.
	oc.grind0 = damageableStack(idDiamondSword, 1, maxDmg, 1400)
	oc.grind1 = damageableStack(idDiamondSword, 1, maxDmg, 1400)
	loop.grindstoneCreateResult(oc)

	if got := stackDamageValue(oc.grindResult); got != 1161 {
		t.Fatalf("partial-repair damage = %d, want 1161 (1561 - (161+161+78))", got)
	}
}

// TestGrindstoneKeepsCurse: a diamond sword with sharpness + a binding curse -> the strip keeps the curse
// and removes sharpness. VERIFIED against removeNonCursesFrom (keep only #enchantment/curse).
func TestGrindstoneKeepsCurse(t *testing.T) {
	loop, _, pos := grindstoneLoop(t)
	oc := &openContainer{windowID: 1, kind: containerKindGrindstone, grindPos: pos}

	oc.grind0 = enchantedStack(idDiamondSword, 1,
		component.EnchantmentEntry{ID: enchSharpness, Level: 3},
		component.EnchantmentEntry{ID: enchBindingCurse, Level: 1})
	loop.grindstoneCreateResult(oc)

	got := stackEnchantmentsForCrafting(oc.grindResult)
	if len(got) != 1 {
		t.Fatalf("kept enchantments = %d, want 1 (only the curse survives)", len(got))
	}
	if int(got[0].ID) != enchBindingCurse {
		t.Fatalf("kept enchantment id = %d, want binding_curse (%d)", got[0].ID, enchBindingCurse)
	}
}

// TestGrindstoneEnchantedBookToBook: a fully-disenchantable enchanted book (no curse) -> the result
// transmutes to a plain book. VERIFIED against removeNonCursesFrom (transmuteCopy to BOOK when empty).
func TestGrindstoneEnchantedBookToBook(t *testing.T) {
	loop, _, pos := grindstoneLoop(t)
	oc := &openContainer{windowID: 1, kind: containerKindGrindstone, grindPos: pos}

	// An enchanted book carries STORED_ENCHANTMENTS.
	s := component.SlotData{ItemID: pk.VarInt(idEnchantedBook), Count: 1}
	var pt component.Patch
	pt.Set(compStoredEnchantments, &component.StoredEnchantments{
		Enchantments: []component.EnchantmentEntry{{ID: enchUnbreaking, Level: 3}}})
	oc.grind0 = pt.ApplyTo(s)

	loop.grindstoneCreateResult(oc)
	if int(oc.grindResult.ItemID) != idBook {
		t.Fatalf("disenchanted book result item = %d, want plain book (%d)", oc.grindResult.ItemID, idBook)
	}
	if stackHasAnyEnchantments(oc.grindResult) {
		t.Fatal("plain book still carries enchantments")
	}
}

// sumOrbXP sums the xpValue of every live ExperienceOrb across the loop's regions.
func sumOrbXP(loop *TickLoop) int {
	total := 0
	for _, r := range loop.regions {
		if r == nil {
			continue
		}
		for _, e := range r.entities.all() {
			if e != nil && e.isOrb {
				total += e.xpValue
			}
		}
	}
	return total
}
