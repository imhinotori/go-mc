package server

// merchant_menu_test.go — VILLAGER-MENU: the villager MERCHANT MENU seam end-to-end. Verifies (1)
// right-clicking a FARMER villager opens the menu (ClientboundOpenScreen(minecraft:merchant) +
// ClientboundMerchantOffers with the 5 farmer offers) and sets the villager's tradingPlayer; (2) selecting
// trade 0 auto-fills the payment slot with the offer's cost; (3) taking the result increments the offer's
// uses, shrinks the payment, and awards XP (an experience_orb + villagerXp accrual).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// merchantTestLoop builds a single-region block loop with a FARMER level-1 villager at (8.5, 64, 8.5) in the
// sole region, plus a capturing player at the villager's column with entityID set (the tradingPlayer id).
func merchantTestLoop(t *testing.T) (*TickLoop, *Entity, *tickPlayer) {
	t.Helper()
	loop, _ := newBlockLoop()

	villager := NewEntity(6200, entity.Villager, 8.5, 64.0, 8.5)
	villager.villagerType = "plains"
	villager.villagerProfession = "farmer"
	villager.villagerLevel = 1
	loop.only().entities.add(villager)

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1 // the tradingPlayer id (non-zero) — stillValid checks villager.villagerTradingPlayer == p.entityID
	return loop, villager, p
}

// interactPacket builds a ServerboundInteract naming targetID (VarInt entityId, MAIN_HAND, Vec3, !secondary).
func interactPacket(targetID int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundInteract),
		pk.VarInt(targetID),
		pk.VarInt(0), // InteractionHand = MAIN_HAND
		pk.Double(0), pk.Double(0), pk.Double(0),
		pk.Boolean(false),
	)
}

// TestVillagerMenuOpens: right-clicking a FARMER villager opens the merchant menu — a windowId is allocated,
// OpenScreen carries minecraft:merchant, ClientboundMerchantOffers carries the 5 farmer offers, and the
// villager's tradingPlayer is set to the interacting player.
func TestVillagerMenuOpens(t *testing.T) {
	loop, villager, p := merchantTestLoop(t)

	loop.handleInteract(p, interactPacket(villager.id))

	if p.openContainer == nil {
		t.Fatal("openContainer is nil after villager interact")
	}
	if p.openContainer.kind != containerKindMerchant {
		t.Fatalf("openContainer kind = %d, want merchant (%d)", p.openContainer.kind, containerKindMerchant)
	}
	if p.openContainer.windowID < 1 || p.openContainer.windowID > 100 {
		t.Fatalf("windowId %d out of the vanilla 1..100 range", p.openContainer.windowID)
	}
	if villager.villagerTradingPlayer != p.entityID {
		t.Fatalf("villager tradingPlayer = %d, want %d (setTradingPlayer)", villager.villagerTradingPlayer, p.entityID)
	}

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundOpenScreen); n != 1 {
		t.Fatalf("ClientboundOpenScreen sent %d times, want 1", n)
	}
	if n := countID(got, packetid.ClientboundMerchantOffers); n != 1 {
		t.Fatalf("ClientboundMerchantOffers sent %d times, want 1", n)
	}

	wantMenu := menuTypeID(registryid.Menu, "minecraft:merchant")
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundOpenScreen) {
			continue
		}
		var win, menuID pk.VarInt
		if err := packet.Scan(&win, &menuID); err != nil {
			t.Fatalf("OpenScreen scan: %v", err)
		}
		if int32(menuID) != wantMenu {
			t.Fatalf("OpenScreen menu id = %d, want minecraft:merchant (%d)", menuID, wantMenu)
		}
		if int32(win) != int32(p.openContainer.windowID) {
			t.Fatalf("OpenScreen windowId = %d, want %d", win, p.openContainer.windowID)
		}
	}

	// ClientboundMerchantOffers: containerId (VarInt) + offers count (VarInt). The farmer/1 set is 5 offers.
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundMerchantOffers) {
			continue
		}
		var win, count pk.VarInt
		if err := packet.Scan(&win, &count); err != nil {
			t.Fatalf("MerchantOffers scan: %v", err)
		}
		if int32(win) != int32(p.openContainer.windowID) {
			t.Fatalf("MerchantOffers windowId = %d, want %d", win, p.openContainer.windowID)
		}
		if count != 5 {
			t.Fatalf("MerchantOffers offer count = %d, want 5 (farmer level-1 set)", count)
		}
	}
}

// TestVillagerNoDoubleOpenWhileTrading: a second interact while the villager is already trading is a no-op
// consume (isTrading gate) — the villager stays bound to the first player, no new window churn.
func TestVillagerNoDoubleOpenWhileTrading(t *testing.T) {
	loop, villager, p := merchantTestLoop(t)
	loop.handleInteract(p, interactPacket(villager.id))
	firstWin := p.openContainer.windowID

	// A different player interacts while the villager is busy: isTrading() -> true, consumed, no re-open.
	other := blockPlayer(loop, 8.5, 64.0, 8.5)
	other.entityID = 2
	loop.handleInteract(other, interactPacket(villager.id))

	if other.openContainer != nil {
		t.Fatal("a second player must NOT open the menu while the villager is already trading (isTrading gate)")
	}
	if villager.villagerTradingPlayer != p.entityID {
		t.Fatalf("villager stayed bound to the wrong player: %d, want %d", villager.villagerTradingPlayer, p.entityID)
	}
	if p.openContainer.windowID != firstWin {
		t.Fatalf("the first player's window churned: %d, want %d", p.openContainer.windowID, firstWin)
	}
}

// TestVillagerTradeTakeIncrementsUsesAndXp: after opening, selecting trade 0 (wheat -> emerald) fills the
// payment slot, then taking the result (pickup slot 2) increments the offer's uses from 0 to 1, shrinks the
// payment by the cost (20 wheat), awards the villager XP (the offer's xp) and spawns an experience_orb.
func TestVillagerTradeTakeIncrementsUsesAndXp(t *testing.T) {
	loop, villager, p := merchantTestLoop(t)

	// Give the player 64 wheat in the first hotbar slot so tryMoveItems can fill the payment.
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(0), component.SlotData{Count: 64, ItemID: pk.VarInt(item.Wheat.ID)})

	// Open the menu.
	loop.handleInteract(p, interactPacket(villager.id))
	oc := p.openContainer
	if oc == nil {
		t.Fatal("menu did not open")
	}
	offers := villagerGetOffers(villager)
	wheatOffer := offers[0] // wheat_emerald: WHEAT x20 -> Emerald, xp 2
	if wheatOffer.uses != 0 {
		t.Fatalf("precondition: offer uses = %d, want 0", wheatOffer.uses)
	}

	// SelectTrade 0: auto-fill the payment slot with 20 wheat (the offer's baseCostA), set the result.
	loop.handleSelectTrade(p, pk.Marshal(int32(packetid.ServerboundSelectTrade), pk.VarInt(0)))

	if stackEmpty(oc.mpay0) || int(oc.mpay0.ItemID) != int(item.Wheat.ID) {
		t.Fatalf("after SelectTrade 0, payment0 = %+v, want wheat auto-filled", oc.mpay0)
	}
	if oc.mactiveOffer != 0 {
		t.Fatalf("after SelectTrade 0, activeOffer = %d, want 0 (wheat offer)", oc.mactiveOffer)
	}
	if stackEmpty(oc.mresult) || int(oc.mresult.ItemID) != int(item.Emerald.ID) {
		t.Fatalf("after SelectTrade 0, result = %+v, want 1 emerald", oc.mresult)
	}

	payBefore := int(oc.mpay0.Count)
	orbsBefore := countByType(loop.only(), entity.ExperienceOrb.ID)
	xpBefore := villager.villagerXp

	// Take the result: PICKUP (input 0) on the result slot (menu index 2), left-click (button 0), empty cursor.
	loop.clicked(p, int32(oc.windowID), 2, 0, containerInputPickup)

	// uses incremented 0 -> 1 (notifyTrade.increaseUses).
	if wheatOffer.uses != 1 {
		t.Fatalf("after taking the result, offer uses = %d, want 1 (increaseUses)", wheatOffer.uses)
	}
	// The cursor now holds the emerald result.
	if carried := inv.getCarried(); stackEmpty(carried) || int(carried.ItemID) != int(item.Emerald.ID) {
		t.Fatalf("after the take, cursor = %+v, want the emerald result", carried)
	}
	// The payment shrank by the cost (20 wheat): 20 (payment) -> 0.
	if int(oc.mpay0.Count) != payBefore-20 {
		t.Fatalf("payment after take = %d, want %d (shrunk by the 20-wheat cost)", oc.mpay0.Count, payBefore-20)
	}
	// villagerXp accrued the offer's xp (2).
	if villager.villagerXp != xpBefore+wheatOffer.xp {
		t.Fatalf("villagerXp after trade = %d, want %d (+offer.xp %d)", villager.villagerXp, xpBefore+wheatOffer.xp, wheatOffer.xp)
	}
	// An experience_orb spawned (rewardTradeXp -> awardExperienceOrbs, offer.rewardExp == true).
	orbsAfter := countByType(loop.only(), entity.ExperienceOrb.ID)
	if orbsAfter <= orbsBefore {
		t.Fatalf("no experience_orb spawned on a trade take (before=%d after=%d)", orbsBefore, orbsAfter)
	}
}

// TestVillagerMenuCloseReturnsPayment: closing the merchant window returns the payment slot to the player
// inventory and clears the villager's tradingPlayer (MerchantMenu.removed).
func TestVillagerMenuCloseReturnsPayment(t *testing.T) {
	loop, villager, p := merchantTestLoop(t)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(0), component.SlotData{Count: 64, ItemID: pk.VarInt(item.Wheat.ID)})

	loop.handleInteract(p, interactPacket(villager.id))
	loop.handleSelectTrade(p, pk.Marshal(int32(packetid.ServerboundSelectTrade), pk.VarInt(0)))
	if stackEmpty(p.openContainer.mpay0) {
		t.Fatal("precondition: payment0 must be filled before close")
	}

	// Count the player's total wheat before close (hotbar + payment).
	wheatBefore := int(p.openContainer.mpay0.Count) + countPlayerItem(inv, item.Wheat.ID)

	loop.handleContainerClose(p, pk.Marshal(int32(packetid.ServerboundContainerClose)))

	if p.openContainer != nil {
		t.Fatal("openContainer must be nil after close")
	}
	if villager.villagerTradingPlayer != 0 {
		t.Fatalf("villager tradingPlayer = %d after close, want 0 (setTradingPlayer(null))", villager.villagerTradingPlayer)
	}
	if got := countPlayerItem(inv, item.Wheat.ID); got != wheatBefore {
		t.Fatalf("wheat after close = %d, want %d (the payment was returned)", got, wheatBefore)
	}
}

// countPlayerItem totals the count of itemID across the player inventory storage window slots (main+hotbar).
func countPlayerItem(inv *Inventory, itemID item.ID) int {
	total := 0
	for i := 0; i < 27; i++ {
		s := inv.get(int16(windowMainFirst + i))
		if !stackEmpty(s) && int(s.ItemID) == int(itemID) {
			total += int(s.Count)
		}
	}
	for i := 0; i < 9; i++ {
		s := inv.get(int16(windowHotbarFirst + i))
		if !stackEmpty(s) && int(s.ItemID) == int(itemID) {
			total += int(s.Count)
		}
	}
	return total
}
