package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// consumable_blockitem_test.go covers the BlockItem.useOn CONSUMABLE fallback: a consumable BLOCK
// item (sweet_berries -> sweet_berry_bush, glow_berries -> cave_vines) right-clicked ON a block
// where its bush cannot be placed EATS instead of doing nothing. This is the 1:1 port of
// BlockItem.useOn (temp/cache/26.2-inner.jar):
//
//	result = place(ctx);
//	if (!result.consumesAction() && stack.has(DataComponents.CONSUMABLE))
//	    return Item.use(level, player, hand);   // start the eat/consume
//	return result;
//
// Before the fix, handleUseItemOn resolved blockStateForItem("sweet_berries") -> MISS (the item
// name != the block name sweet_berry_bush) and returned doing nothing: no place, no eat, no shrink.
// The client predicted the eat animation locally but the server never ran the use, so the stack
// never shrank and the food bar never moved (the two reported food bugs).

// startEatOnBlock issues a ServerboundUseItemOn (mainhand) at a block face through dispatch +
// applyInput, resolving the use on-tick. The clicked face is a block the bush cannot be placed on
// (an all-air test world at that cell), so place() fails and the CONSUMABLE fallback fires.
func startEatOnBlock(loop *TickLoop, p *tickPlayer) {
	// Click UP on the cell at the player's feet-ish; the test world is all-air so place() fails
	// (BlockPlaceContext.canPlace -> target not replaceable is false for air, but sweet_berries
	// never resolves to a block at all -> blockStateForItem MISS -> fallback).
	ui := useItemOnPacket(int32(interactionHandMain),
		pk.Position{X: 1, Y: 64, Z: 1}, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 1)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
}

// TestSweetBerriesUseItemOnStartsEat: right-clicking sweet_berries ON a block starts the consume
// (BlockItem.useOn place-fail -> Item.use), setting the use state (32-tick duration) exactly as the
// air-eat path does. Before the fix, isUsingItem stayed false (handleUseItemOn was a no-op).
func TestSweetBerriesUseItemOnStartsEat(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	p.food = 10 // needsFood() true so canEat passes
	giveHeld(p, item.SweetBerries.ID, 8)

	startEatOnBlock(loop, p)

	if !isUsingItem(p) {
		t.Fatalf("after UseItemOn(sweet_berries): isUsingItem = false, want true (BlockItem.useOn CONSUMABLE fallback did not start the eat)")
	}
	if p.useItemRemaining != 32 {
		t.Fatalf("useItemRemaining = %d, want 32 (1.6s * 20)", p.useItemRemaining)
	}
	if int32(p.useItem.ItemID) != int32(item.SweetBerries.ID) {
		t.Fatalf("useItem.ItemID = %d, want %d (sweet_berries)", p.useItem.ItemID, item.SweetBerries.ID)
	}
}

// TestSweetBerriesUseItemOnCompletesEat: driving the use to completion via UseItemOn restores
// hunger (+2 nutrition, 0.4 saturation) AND shrinks the stack by 1 AND broadcasts the cleared
// DATA_LIVING_ENTITY_FLAGS + syncs SetHealth/SetSlot. This is both reported bugs fixed end-to-end.
func TestSweetBerriesUseItemOnCompletesEat(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	p.food = 10
	p.saturation = 0
	inv := giveHeld(p, item.SweetBerries.ID, 8)
	p.lastFoodSent = p.food
	p.lastFoodSaturationZero = p.saturation == 0
	p.lastHealthSent = p.health

	startEatOnBlock(loop, p)
	if p.useItemRemaining != 32 {
		t.Fatalf("pre-tick useItemRemaining = %d, want 32", p.useItemRemaining)
	}
	// NOTE: do NOT drain here — drainPackets closes the outbound queue, which would drop the
	// completion sync. Drain once at the end.

	// 32 ticks: completeUsingItem fires on the last one.
	for i := 0; i < 32; i++ {
		loop.tickUseItem()
	}

	if isUsingItem(p) {
		t.Fatalf("after 32 ticks: still using, want stopped (animation would never end -> BUG 1)")
	}
	// FoodData.add(2, 0.4): food 10+2=12, saturation min(0+0.4, 12)=0.4.
	if p.food != 12 {
		t.Fatalf("food = %d, want 12 (10 + nutrition 2) -> BUG 2 (no hunger restore)", p.food)
	}
	if p.saturation != 0.4 {
		t.Fatalf("saturation = %v, want 0.4 (absolute)", p.saturation)
	}
	// Stack shrank by 1 (BUG 2: no shrink).
	got := inv.get(hotbarMenuSlotBase)
	if int32(got.Count) != 7 {
		t.Fatalf("held count = %d, want 7 (8 - 1) -> BUG 2 (stack not shrunk)", got.Count)
	}
	if int32(got.ItemID) != int32(item.SweetBerries.ID) {
		t.Fatalf("held item changed: %d, want sweet_berries %d", got.ItemID, item.SweetBerries.ID)
	}

	// The cleared using-item pose (DATA_LIVING_ENTITY_FLAGS) + food bar + shrunk slot were pushed.
	ps := drainPackets(p.client)
	if countID(ps, packetid.ClientboundSetHealth) == 0 {
		t.Fatalf("no ClientboundSetHealth sent after eat (food bar would not update)")
	}
	if countID(ps, packetid.ClientboundContainerSetSlot) == 0 {
		t.Fatalf("no ClientboundContainerSetSlot sent after eat (stack shrink not synced)")
	}
}

// TestGlowBerriesUseItemOnStartsEat: the sibling consumable BlockItem (glow_berries -> cave_vines)
// takes the same fallback path.
func TestGlowBerriesUseItemOnStartsEat(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	p.food = 10
	giveHeld(p, item.GlowBerries.ID, 4)

	startEatOnBlock(loop, p)

	if !isUsingItem(p) {
		t.Fatalf("after UseItemOn(glow_berries): isUsingItem = false, want true")
	}
	if int32(p.useItem.ItemID) != int32(item.GlowBerries.ID) {
		t.Fatalf("useItem.ItemID = %d, want %d (glow_berries)", p.useItem.ItemID, item.GlowBerries.ID)
	}
}

// TestCookedBeefUseItemOnDoesNotEat: a PLAIN (non-block) food item right-clicked ON a block does
// NOT eat via the UseItemOn path (its default Item.useOn returns PASS; only BlockItem.useOn has the
// CONSUMABLE fallback). Air-eating a plain food uses ServerboundUseItem instead. This guards the
// fallback from over-firing for every food.
func TestCookedBeefUseItemOnDoesNotEat(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	p.food = 10
	giveHeld(p, item.CookedBeef.ID, 8)

	startEatOnBlock(loop, p)

	if isUsingItem(p) {
		t.Fatalf("cooked_beef via UseItemOn started a use, want NO eat (non-BlockItem Item.useOn returns PASS)")
	}
}
