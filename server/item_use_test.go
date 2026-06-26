package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// item_use_test.go covers Plan 17-22: the item-use / EATING chain (ServerboundUseItem ->
// Consumable.startConsuming -> LivingEntity.startUsingItem -> updatingUsingItem/updateUsingItem ->
// completeUsingItem -> Item.finishUsingItem -> Consumable.onConsume -> FoodProperties.onConsume ->
// FoodData.eat(FoodProperties) + ItemStack.consume). The tests are deterministic (no RNG): the
// use-duration is driven by direct tickUseItem calls, so the 32-tick eat completes exactly.
//
// Reuses the block test harness (newBlockLoop / blockPlayer / drainPackets / countID).

// useItemPacket builds a ServerboundUseItem with the jar-verified field order: VarInt hand,
// VarInt sequence, Float yRot, Float xRot.
func useItemPacket(hand, seq int32, yRot, xRot float32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundUseItem),
		pk.VarInt(hand), pk.VarInt(seq), pk.Float(yRot), pk.Float(xRot))
}

// giveHeld seeds count of the item with id into the player's mainhand (menu slot 36+heldSlot==36)
// and returns the inventory for assertions.
func giveHeld(p *tickPlayer, id item.ID, count int32) *Inventory {
	inv := ensureInventory(p)
	inv.heldSlot = 0
	inv.set(hotbarMenuSlotBase, component.SlotData{Count: pk.VarInt(count), ItemID: pk.VarInt(id)})
	return inv
}

// startEat issues a ServerboundUseItem (mainhand) through dispatch+applyInput, resolving the use
// on-tick. The player must be confirmed (blockPlayer is).
func startEat(loop *TickLoop, p *tickPlayer) {
	ui := useItemPacket(int32(interactionHandMain), 1, 0, 0)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
}

// TestUseItemRouted: a ServerboundUseItem passed to dispatch is APPENDED to the player's subtick
// buffer (so it is resolved on-tick like the other use/break/place packets).
func TestUseItemRouted(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)

	if n := p.subtick.len(); n != 0 {
		t.Fatalf("fresh player subtick buffer = %d, want 0", n)
	}
	loop.dispatch(p.client, useItemPacket(int32(interactionHandMain), 1, 0, 0))
	if n := p.subtick.len(); n != 1 {
		t.Fatalf("after dispatch(UseItem) subtick len = %d, want 1 (route missing)", n)
	}
}

// TestStartUsingCookedBeefSetsUseDuration: starting to eat cooked_beef (consume_seconds 1.6 => 32
// ticks) sets useItem to the held stack and useItemRemaining to 32 (Consumable.consumeTicks ==
// (int)(1.6 * 20.0f)). The player begins at non-full hunger so canEat passes.
func TestStartUsingCookedBeefSetsUseDuration(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 10 // needsFood() true
	giveHeld(p, item.CookedBeef.ID, 16)

	startEat(loop, p)

	if !isUsingItem(p) {
		t.Fatalf("after startEat: isUsingItem = false, want true")
	}
	if p.useItemRemaining != 32 {
		t.Fatalf("useItemRemaining = %d, want 32 (1.6s * 20)", p.useItemRemaining)
	}
	if int32(p.useItem.ItemID) != int32(item.CookedBeef.ID) {
		t.Fatalf("useItem.ItemID = %d, want %d (cooked_beef)", p.useItem.ItemID, item.CookedBeef.ID)
	}
	if p.useItemHand != interactionHandMain {
		t.Fatalf("useItemHand = %d, want %d (main)", p.useItemHand, interactionHandMain)
	}
}

// TestEatCookedBeefCompletes: after 32 tickUseItem calls, completeUsingItem fires — food rises by
// min(8, 20-food), saturation rises (absolute 12.8 clamped to the new food), and the stack shrinks
// by 1. Asserts exact values: food 10 -> 18, saturation 0 -> 12.8 (clamped to 18), count 16 -> 15.
func TestEatCookedBeefCompletes(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 10
	p.saturation = 0
	inv := giveHeld(p, item.CookedBeef.ID, 16)
	// Seed the dirty-send trackers so syncFood's change detection is meaningful.
	p.lastFoodSent = p.food
	p.lastSaturationSent = p.saturation
	p.lastHealthSent = p.health

	startEat(loop, p)
	if p.useItemRemaining != 32 {
		t.Fatalf("pre-tick useItemRemaining = %d, want 32", p.useItemRemaining)
	}

	// 31 ticks: still using, not complete (remaining counts 31..1).
	for i := 0; i < 31; i++ {
		loop.tickUseItem()
	}
	if !isUsingItem(p) {
		t.Fatalf("after 31 ticks: isUsingItem = false, want true (remaining=%d)", p.useItemRemaining)
	}
	if p.useItemRemaining != 1 {
		t.Fatalf("after 31 ticks: useItemRemaining = %d, want 1", p.useItemRemaining)
	}

	// 32nd tick: completeUsingItem fires.
	loop.tickUseItem()

	if isUsingItem(p) {
		t.Fatalf("after 32 ticks: still using, want stopped")
	}
	// FoodData.add(8, 12.8): food 10+8=18, saturation min(0+12.8, 18)=12.8.
	if p.food != 18 {
		t.Fatalf("food = %d, want 18 (10 + nutrition 8)", p.food)
	}
	if p.saturation != 12.8 {
		t.Fatalf("saturation = %v, want 12.8 (absolute, clamped to food 18)", p.saturation)
	}
	// Stack shrank by 1.
	got := inv.get(hotbarMenuSlotBase)
	if int32(got.Count) != 15 {
		t.Fatalf("held count = %d, want 15 (16 - 1)", got.Count)
	}
	if int32(got.ItemID) != int32(item.CookedBeef.ID) {
		t.Fatalf("held item changed: %d, want cooked_beef %d", got.ItemID, item.CookedBeef.ID)
	}

	// SYNC: the client received SetHealth (food bar) and SetSlot (shrunk stack).
	ps := drainPackets(p.client)
	if countID(ps, packetid.ClientboundSetHealth) == 0 {
		t.Fatalf("no ClientboundSetHealth sent after eat (food bar would not update)")
	}
	if countID(ps, packetid.ClientboundContainerSetSlot) == 0 {
		t.Fatalf("no ClientboundContainerSetSlot sent after eat (stack shrink not synced)")
	}
}

// TestEatRejectedAtFullHunger: cooked_beef (canAlwaysEat=false) is REJECTED when food==20
// (Player.canEat: !invulnerable, !canAlwaysEat, !needsFood -> false). No use begins.
func TestEatRejectedAtFullHunger(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 20 // needsFood() false
	giveHeld(p, item.CookedBeef.ID, 16)

	startEat(loop, p)

	if isUsingItem(p) {
		t.Fatalf("full-hunger non-canAlwaysEat food began use, want rejected")
	}
}

// TestGoldenAppleAcceptedAtFullHunger: golden_apple (canAlwaysEat=true) IS accepted at full food
// (Player.canEat short-circuits on canAlwaysEat). The use begins (32-tick duration).
func TestGoldenAppleAcceptedAtFullHunger(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 20 // full — needsFood() false, but golden_apple canAlwaysEat
	giveHeld(p, item.GoldenApple.ID, 4)

	startEat(loop, p)

	if !isUsingItem(p) {
		t.Fatalf("golden_apple (canAlwaysEat) rejected at full hunger, want accepted")
	}
	if p.useItemRemaining != 32 {
		t.Fatalf("golden_apple useItemRemaining = %d, want 32", p.useItemRemaining)
	}
}

// TestCreativeDoesNotShrinkStack: a creative player eating cooked_beef restores hunger but does NOT
// lose the item (ItemStack.consume: !hasInfiniteMaterials guard — creative is infinite). food rises,
// count stays 16.
func TestCreativeDoesNotShrinkStack(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeCreative
	p.food = 10
	p.saturation = 0
	inv := giveHeld(p, item.CookedBeef.ID, 16)
	p.lastFoodSent = p.food
	p.lastSaturationSent = p.saturation
	p.lastHealthSent = p.health

	startEat(loop, p)
	for i := 0; i < 32; i++ {
		loop.tickUseItem()
	}

	if isUsingItem(p) {
		t.Fatalf("creative eat did not complete")
	}
	if p.food != 18 {
		t.Fatalf("creative food = %d, want 18 (eat still applies)", p.food)
	}
	if got := inv.get(hotbarMenuSlotBase); int32(got.Count) != 16 {
		t.Fatalf("creative held count = %d, want 16 (no shrink)", got.Count)
	}
}

// TestEatCancelledIfHandChanges: if the held item changes mid-use (the hand no longer holds the use
// item), the use is cancelled (updatingUsingItem -> stopUsingItem) and no eat lands.
func TestEatCancelledIfHandChanges(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 10
	inv := giveHeld(p, item.CookedBeef.ID, 16)

	startEat(loop, p)
	// A few ticks in, swap the held item to apple (a different item identity).
	loop.tickUseItem()
	loop.tickUseItem()
	inv.set(hotbarMenuSlotBase, component.SlotData{Count: 16, ItemID: pk.VarInt(item.Apple.ID)})
	loop.tickUseItem() // updatingUsingItem sees a different item -> stopUsingItem

	if isUsingItem(p) {
		t.Fatalf("use not cancelled after hand item changed")
	}
	if p.food != 10 {
		t.Fatalf("food = %d, want 10 (no eat should have landed)", p.food)
	}
}

// TestEatAppleExactSaturation: apple nutrition 4, saturation 2.4 (absolute). From food 6 -> 10,
// saturation 0 -> 2.4 (clamped to food 10). Guards against the saturationByModifier doubling trap
// (the modifier form would give 4*2.4*2 = 19.2, clamped to 10 — a wrong value).
func TestEatAppleExactSaturation(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)
	p.gameMode = gameModeSurvival
	p.food = 6
	p.saturation = 0
	giveHeld(p, item.Apple.ID, 16)
	p.lastFoodSent = p.food
	p.lastSaturationSent = p.saturation
	p.lastHealthSent = p.health

	startEat(loop, p)
	for i := 0; i < 32; i++ {
		loop.tickUseItem()
	}

	if p.food != 10 {
		t.Fatalf("food = %d, want 10 (6 + 4)", p.food)
	}
	if p.saturation != 2.4 {
		t.Fatalf("saturation = %v, want 2.4 (ABSOLUTE, not the doubled modifier 19.2)", p.saturation)
	}
}
