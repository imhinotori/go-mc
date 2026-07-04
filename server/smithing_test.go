package server

// smithing_test.go — SMITHING MENU (SmithingMenu, an ItemCombinerMenu) tested 1:1 against the jar. The
// scenarios prove:
//   - open: right-clicking a smithing_table opens a 40-slot window (menu id minecraft:smithing).
//   - TRANSFORM: netherite_upgrade_template + diamond gear + netherite_ingot -> the netherite gear,
//     PRESERVING the diamond gear's enchantments + custom name + damage (createWithOriginalComponents).
//   - take: the 3 inputs each shrink by 1.
//   - no-match: a mismatched addition yields no result.

import (
	"testing"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

const (
	idNetheriteUpgradeTemplate = 1458 // minecraft:netherite_upgrade_smithing_template
	idNetheriteIngot           = 937  // minecraft:netherite_ingot
	idNetheriteSword           = 969  // minecraft:netherite_sword
	compCustomName             = 6    // minecraft:custom_name
)

// smithingLoop builds a block loop + a survival player with a smithing_table at the returned pos.
func smithingLoop(t *testing.T) (*TickLoop, *tickPlayer, pk.Position) {
	t.Helper()
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	loop.only().world.SetBlock(pos, block.ToStateID[block.SmithingTable{}], dimMinY)
	return loop, p, pos
}

// namedEnchantedDamagedSword builds a diamond sword carrying a custom name + sharpness-3 + damage 200,
// to prove all three component classes survive the netherite transform.
func namedEnchantedDamagedSword() component.SlotData {
	s := component.SlotData{ItemID: pk.VarInt(idDiamondSword), Count: 1}
	var p component.Patch
	p.Set(compMaxDamage, &component.MaxDamage{VarInt: 1561})
	p.Set(compDamage, &component.Damage{VarInt: 200})
	p.Set(compEnchantments, &component.Enchantments{
		Enchantments: []component.EnchantmentEntry{{ID: enchSharpness, Level: 3}}})
	p.Set(compCustomName, &component.CustomName{Name: chat.Text("Excalibur")})
	return p.ApplyTo(s)
}

// TestSmithingOpen: right-clicking a smithing_table opens its 40-slot menu.
func TestSmithingOpen(t *testing.T) {
	loop, p, pos := smithingLoop(t)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if p.openContainer == nil {
		t.Fatal("openContainer is nil after smithing open")
	}
	if p.openContainer.kind != containerKindSmithing {
		t.Fatalf("openContainer kind = %d, want smithing", p.openContainer.kind)
	}
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundOpenScreen); n != 1 {
		t.Fatalf("ClientboundOpenScreen sent %d times, want 1", n)
	}
	wantMenu := menuTypeID(registryid.Menu, "minecraft:smithing")
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundOpenScreen) {
			continue
		}
		var win, menuID pk.VarInt
		if err := packet.Scan(&win, &menuID); err != nil {
			t.Fatalf("OpenScreen scan: %v", err)
		}
		if int32(menuID) != wantMenu {
			t.Fatalf("OpenScreen menu id = %d, want smithing (%d)", menuID, wantMenu)
		}
	}
}

// TestSmithingNetheriteUpgrade: netherite_upgrade_template + a named/enchanted/damaged diamond sword +
// netherite_ingot -> a netherite_sword PRESERVING the base's custom name, enchantments, and damage.
func TestSmithingNetheriteUpgrade(t *testing.T) {
	loop, p, pos := smithingLoop(t)
	inv := ensureInventory(p)
	oc := &openContainer{windowID: 1, kind: containerKindSmithing, smithPos: pos}
	p.openContainer = oc

	base := namedEnchantedDamagedSword()
	oc.smithTemplate = component.SlotData{ItemID: pk.VarInt(idNetheriteUpgradeTemplate), Count: 1}
	oc.smithBase = base
	oc.smithAddition = component.SlotData{ItemID: pk.VarInt(idNetheriteIngot), Count: 1}

	// createResult: match the smithing_transform recipe + assemble (transform preserving base components).
	loop.smithingCreateResult(oc)
	res := oc.smithResult
	if stackEmpty(res) {
		t.Fatal("smithing result is empty; want a netherite_sword")
	}
	if int(res.ItemID) != idNetheriteSword {
		t.Fatalf("result item = %d, want netherite_sword (%d)", res.ItemID, idNetheriteSword)
	}
	// PRESERVATION: the result carries the base's damage, enchantments, and custom name verbatim.
	if got := stackDamageValue(res); got != 200 {
		t.Fatalf("result damage = %d, want 200 (base damage preserved)", got)
	}
	ench := stackEnchantmentsForCrafting(res)
	if len(ench) != 1 || int(ench[0].ID) != enchSharpness || int(ench[0].Level) != 3 {
		t.Fatalf("result enchantments = %+v, want [sharpness 3] (base enchants preserved)", ench)
	}
	resPatch := component.DecodePatch(res)
	if cn, ok := resPatch.Get(compCustomName).(*component.CustomName); !ok || cn.Name.Text != "Excalibur" {
		t.Fatalf("result custom name not preserved: %+v", resPatch.Get(compCustomName))
	}

	// Take the result (PICKUP primary on the result slot 3): the 3 inputs each shrink by 1.
	loop.smithingPickup(p, oc, inv, smithingResultSlot, 0)

	if c := inv.getCarried(); int(c.ItemID) != idNetheriteSword {
		t.Fatalf("carried after take = %d, want netherite_sword", c.ItemID)
	}
	if !stackEmpty(oc.smithTemplate) {
		t.Fatalf("template not consumed after take: %+v", oc.smithTemplate)
	}
	if !stackEmpty(oc.smithBase) {
		t.Fatalf("base not consumed after take: %+v", oc.smithBase)
	}
	if !stackEmpty(oc.smithAddition) {
		t.Fatalf("addition not consumed after take: %+v", oc.smithAddition)
	}
}

// TestSmithingNoMatch: a wrong addition (a book, not a netherite material) yields no result.
func TestSmithingNoMatch(t *testing.T) {
	loop, _, pos := smithingLoop(t)
	oc := &openContainer{windowID: 1, kind: containerKindSmithing, smithPos: pos}

	oc.smithTemplate = component.SlotData{ItemID: pk.VarInt(idNetheriteUpgradeTemplate), Count: 1}
	oc.smithBase = component.SlotData{ItemID: pk.VarInt(idDiamondSword), Count: 1}
	oc.smithAddition = component.SlotData{ItemID: pk.VarInt(idBook), Count: 1}
	loop.smithingCreateResult(oc)

	if !stackEmpty(oc.smithResult) {
		t.Fatalf("smithing result = %+v, want EMPTY (book is not a netherite material)", oc.smithResult)
	}
}

// TestSmithingMissingTemplate: no template (an empty template slot) with a diamond sword + netherite ingot
// yields no result — the transform recipe requires the netherite_upgrade template (present optional).
func TestSmithingMissingTemplate(t *testing.T) {
	loop, _, pos := smithingLoop(t)
	oc := &openContainer{windowID: 1, kind: containerKindSmithing, smithPos: pos}

	oc.smithBase = component.SlotData{ItemID: pk.VarInt(idDiamondSword), Count: 1}
	oc.smithAddition = component.SlotData{ItemID: pk.VarInt(idNetheriteIngot), Count: 1}
	loop.smithingCreateResult(oc)

	if !stackEmpty(oc.smithResult) {
		t.Fatalf("smithing result = %+v, want EMPTY (no template)", oc.smithResult)
	}
}
