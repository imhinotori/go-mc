package server

// mob_pickup_test.go — the MOB item-pickup tests (item_entity_mob.go): the Mob.aiStep looting scan
// (mobPickupItems), the canPickUpLoot gate that protects the pig oracle, the Fox mouth-food hold
// (Fox.canHoldItem / Fox.pickUpItem), and the FoxSearchForItemsGoal forage-walk. All deterministic:
// the fox goal RNG is seeded per-id (foxTestMob), and the scan/pickup draw NO RNG.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// foodStack is a single cooked_beef — an item that carries FOOD + CONSUMABLE (isConsumableFood true).
func foodStack() component.SlotData {
	return component.SlotData{Count: 1, ItemID: pk.VarInt(item.CookedBeef.ID)}
}

// TestMobIsConsumableFood locks Fox.isConsumableFood: a FOOD+CONSUMABLE item is food, a plain block is
// not, an empty stack is not.
func TestMobIsConsumableFood(t *testing.T) {
	if !mobIsConsumableFood(foodStack()) {
		t.Fatal("cooked_beef should be consumable food (has FOOD + CONSUMABLE)")
	}
	if mobIsConsumableFood(dropStack()) {
		t.Fatal("cobblestone should NOT be consumable food")
	}
	if mobIsConsumableFood(component.SlotData{}) {
		t.Fatal("empty stack should NOT be consumable food")
	}
}

// TestMobPickupGateSkipsNonPickupMob is the PIG ORACLE guard at the mobPickupItems level: a mob with
// canPickUpLoot=false (every Animal, the pig) never touches the item — the scan returns at the first
// gate. This is the invariant that keeps the pig's RNG stream byte-identical.
func TestMobPickupGateSkipsNonPickupMob(t *testing.T) {
	loop, _ := newDropLoop()

	pig := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, 64, 8.5)
	pig.health = 10.0
	pig.ai = &mobAI{}
	// canPickUpLoot defaults false (Mob ctor) — a pig never flips it.
	if pig.canPickUpLoot {
		t.Fatal("a pig must default canPickUpLoot=false (the oracle invariant)")
	}
	loop.only().entities.add(pig)

	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, foodStack())
	ie.pickupDelay = 0 // pickable
	loop.only().entities.add(ie)

	loop.mobPickupItems(pig)

	// The pig picked up nothing: the item still exists with its full count and the pig holds nothing.
	if _, ok := loop.only().entities.get(ie.id); !ok {
		t.Fatal("a non-pickup pig must NOT remove the item (oracle guard)")
	}
	if pig.getMainHandItem().Count > 0 {
		t.Fatal("a non-pickup pig must NOT hold the item")
	}
}

// TestFoxPicksUpFoodIntoMouth drives the full fox pickup: a fox (canPickUpLoot=true) standing on a food
// ItemEntity runs mobPickupItems, which routes to Fox.pickUpItem — the food ends up in the fox MAINHAND
// (its mouth), the item entity is discarded, and ticksSinceEaten is reset to 0.
func TestFoxPicksUpFoodIntoMouth(t *testing.T) {
	loop, _ := newDropLoop()

	fox := foxTestMob(5000, 8.5, 64, 8.5)
	fox.canPickUpLoot = true // Fox.<init> setCanPickUpLoot(true)
	fox.ticksSinceEaten = 20 // some chew time elapsed
	loop.only().entities.add(fox)

	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, foodStack())
	ie.pickupDelay = 0 // pickable (past the default 10-tick delay)
	loop.only().entities.add(ie)

	loop.mobPickupItems(fox)

	held := fox.getMainHandItem()
	if held.Count != 1 || item.ID(held.ItemID) != item.CookedBeef.ID {
		t.Fatalf("fox mouth = %+v, want 1× cooked_beef in MAINHAND after pickup", held)
	}
	if _, ok := loop.only().entities.get(ie.id); ok {
		t.Fatal("the item entity should be discarded after the whole stack was taken")
	}
	if fox.ticksSinceEaten != 0 {
		t.Fatalf("ticksSinceEaten = %d, want 0 (Fox.pickUpItem resets it)", fox.ticksSinceEaten)
	}
}

// TestFoxCanHoldItemSemantics locks Fox.canHoldItem: an empty mouth accepts anything; a full mouth
// rejects unless (ticksSinceEaten>0 && new is food && held is NOT food).
func TestFoxCanHoldItemSemantics(t *testing.T) {
	loop, _ := newDropLoop()
	fox := foxTestMob(5001, 0, 64, 0)

	// Empty mouth: accepts food AND non-food.
	if !loop.mobCanHoldItem(fox, foodStack()) || !loop.mobCanHoldItem(fox, dropStack()) {
		t.Fatal("empty-mouth fox must accept any item")
	}

	// Mouth holds cobblestone (NOT food), ticksSinceEaten>0, new item is food -> upgrade allowed.
	fox.setItemSlot(eqSlotMainHand, dropStack())
	fox.ticksSinceEaten = 5
	if !loop.mobCanHoldItem(fox, foodStack()) {
		t.Fatal("fox holding non-food should upgrade to food when ticksSinceEaten>0")
	}
	// New item is non-food -> rejected (already holding something).
	if loop.mobCanHoldItem(fox, dropStack()) {
		t.Fatal("fox holding an item must reject a non-food pickup")
	}
	// ticksSinceEaten == 0 -> rejected even for food.
	fox.ticksSinceEaten = 0
	if loop.mobCanHoldItem(fox, foodStack()) {
		t.Fatal("fox must reject a pickup when ticksSinceEaten==0 and mouth is full")
	}
	// Mouth holds FOOD already -> reject another food (the !isConsumableFood(main) clause).
	fox.setItemSlot(eqSlotMainHand, foodStack())
	fox.ticksSinceEaten = 5
	if loop.mobCanHoldItem(fox, foodStack()) {
		t.Fatal("fox holding food must reject another food (no food-for-food swap)")
	}
}

// TestFoxDoesNotPickupWithPickupDelay: an item still within its pickup delay (hasPickUpDelay) is skipped
// by the looting scan (ALLOWED_ITEMS / the aiStep hasPickUpDelay guard).
func TestFoxDoesNotPickupWithPickupDelay(t *testing.T) {
	loop, _ := newDropLoop()
	fox := foxTestMob(5002, 8.5, 64, 8.5)
	fox.canPickUpLoot = true
	loop.only().entities.add(fox)

	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, foodStack())
	// Fresh item: pickupDelay == 10 (NewItemEntity sets the default) -> not pickable.
	if ie.pickupDelay == 0 {
		t.Fatal("fresh item should have a non-zero pickup delay")
	}
	loop.only().entities.add(ie)

	loop.mobPickupItems(fox)

	if fox.getMainHandItem().Count > 0 {
		t.Fatal("fox must NOT pick up an item that still has a pickup delay")
	}
	if _, ok := loop.only().entities.get(ie.id); !ok {
		t.Fatal("the delayed item must still exist")
	}
}

// TestFoxSearchForItemsGoalGating drives FoxSearchForItemsGoal.canUse deterministically: with the mouth
// empty, no target, canMove, and a food item in range, canUse eventually fires (the nextInt(5) forage
// roll is seeded); with the mouth FULL it never fires.
func TestFoxSearchForItemsGoalGating(t *testing.T) {
	loop, _ := newDropLoop()

	fox := foxTestMob(5003, 8.5, 64, 8.5)
	loop.only().entities.add(fox)

	ie := NewItemEntity(loop.idAlloc.AllocID(), 9.5, 64.0, 8.5, foodStack())
	ie.pickupDelay = 0 // ALLOWED_ITEMS: !hasPickUpDelay
	loop.only().entities.add(ie)

	g := newFoxSearchForItemsGoal()
	fired := false
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			fired = true
			break
		}
	}
	if !fired {
		t.Fatal("FoxSearchForItemsGoal.canUse never fired with an empty mouth + a food item in range")
	}

	// Mouth full -> canUse must be false regardless of the roll.
	fox.setItemSlot(eqSlotMainHand, foodStack())
	for i := 0; i < 50; i++ {
		if g.canUse(loop, fox) {
			t.Fatal("FoxSearchForItemsGoal.canUse fired while the mouth was full")
		}
	}
}

// TestFoxSearchForItemsGoalMovesToItem: start()/tick() set the mob's want-target toward the nearest item
// at the 1.2 forage speed.
func TestFoxSearchForItemsGoalMovesToItem(t *testing.T) {
	loop, _ := newDropLoop()

	fox := foxTestMob(5004, 8.5, 64, 8.5)
	loop.only().entities.add(fox)

	ie := NewItemEntity(loop.idAlloc.AllocID(), 11.5, 64.0, 8.5, foodStack())
	ie.pickupDelay = 0
	loop.only().entities.add(ie)

	g := newFoxSearchForItemsGoal()
	g.start(loop, fox)

	if !fox.ai.hasTarget {
		t.Fatal("FoxSearchForItemsGoal.start should set a want-target toward the item")
	}
	if fox.ai.wantX != ie.x || fox.ai.wantY != ie.y || fox.ai.wantZ != ie.z {
		t.Fatalf("want-target = (%v,%v,%v), want the item at (%v,%v,%v)",
			fox.ai.wantX, fox.ai.wantY, fox.ai.wantZ, ie.x, ie.y, ie.z)
	}
	if fox.ai.wantSpeed != foxSearchMoveSpeed {
		t.Fatalf("want-speed = %v, want %v (moveTo 1.2)", fox.ai.wantSpeed, foxSearchMoveSpeed)
	}
}
