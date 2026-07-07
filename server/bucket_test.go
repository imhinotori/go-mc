package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// bucket_test.go covers the WATER/LAVA BUCKET use path (net.minecraft.world.item.BucketItem.use):
// a right-click-air with a water/lava bucket empties a fluid SOURCE at the raycast target (and swaps
// the bucket to an empty Bucket), and an empty bucket over a fluid SOURCE picks it up (→ the filled
// bucket, source removed). Creative does NOT consume/swap the bucket (getEmptySuccessItem /
// createFilledResult gate on hasInfiniteMaterials). Deterministic (no RNG): the use is driven by a
// direct useItemInHand call with the player aiming straight down (pitch 90 → view (0,-1,0)).
//
// Reuses the block test harness (newBlockLoop / blockPlayer / setHeldItem).

// aimDown points the player straight down so the eye ray marches -Y into the block directly under it.
func aimDown(p *tickPlayer) {
	p.yaw = 0
	p.pitch = 90 // view vector (0,-1,0)
}

// isWaterSourceAt reports whether the world cell at pos holds a water SOURCE (level 0).
func isWaterSourceAt(t *TickLoop, pos pk.Position) bool {
	fs := t.fluidAt(pos)
	return fs.isWater && fs.source
}

// isLavaSourceAt reports whether the world cell at pos holds a lava SOURCE (level 0).
func isLavaSourceAt(t *TickLoop, pos pk.Position) bool {
	fs := t.fluidAt(pos)
	return fs.isLava && fs.source
}

// TestWaterBucketPlacesSourceAndEmpties: a survival player holding one water_bucket, aiming down at a
// stone floor, empties a water SOURCE into the cell above the floor (pos.relative(UP)) and the hand
// becomes an empty Bucket (BucketItem.use FILLED path → emptyContents + getEmptySuccessItem +
// createFilledResult).
func TestWaterBucketPlacesSourceAndEmpties(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	aimDown(p)

	// Stone floor at (1,64,1); the cell above (1,65,1) is air (where the water lands). Eye is at
	// 66+1.62=67.62; the ray marches down through air (1,66,1),(1,65,1) and hits the stone at (1,64,1),
	// entering through its UP face → relPos = (1,65,1).
	floor := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(floor, block.ToStateID[block.Stone{}], dimMinY)

	setHeldItem(p, item.WaterBucket.ID, 1)
	loop.useItemInHand(p, interactionHandMain)

	place := pk.Position{X: 1, Y: 65, Z: 1}
	if !isWaterSourceAt(loop, place) {
		id, _ := mgr.GetBlock(place, dimMinY)
		t.Fatalf("after water_bucket use: cell %v = state %d, want a water source", place, id)
	}
	held := ensureInventory(p).get(hotbarMenuSlotBase)
	if int32(held.ItemID) != int32(item.Bucket.ID) || held.Count != 1 {
		t.Fatalf("after empty: held = (id=%d, count=%d), want empty Bucket id=%d count=1",
			held.ItemID, held.Count, item.Bucket.ID)
	}
}

// TestWaterBucketPicksUpSource: an empty bucket aimed at a water SOURCE picks it up — the source cell
// becomes air and the hand becomes a water_bucket (BucketItem.use EMPTY path → SOURCE_ONLY raycast +
// BucketPickup.pickupBlock + createFilledResult).
func TestWaterBucketPicksUpSource(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	aimDown(p)

	// Water source at (1,64,1); air above it. The SOURCE_ONLY ray marches down through air and stops
	// at the source cell (1,64,1).
	src := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(src, waterStateID(0), dimMinY)
	if !isWaterSourceAt(loop, src) {
		t.Fatalf("setup: %v is not a water source", src)
	}

	setHeldItem(p, item.Bucket.ID, 1)
	loop.useItemInHand(p, interactionHandMain)

	if got, ok := mgr.GetBlock(src, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("after pickup: cell %v = (state %d, ok=%v), want air", src, got, ok)
	}
	held := ensureInventory(p).get(hotbarMenuSlotBase)
	if int32(held.ItemID) != int32(item.WaterBucket.ID) || held.Count != 1 {
		t.Fatalf("after pickup: held = (id=%d, count=%d), want water_bucket id=%d count=1",
			held.ItemID, held.Count, item.WaterBucket.ID)
	}
}

// TestLavaBucketPlacesSourceAndEmpties: the lava sibling of the water place test — a lava_bucket
// empties a lava SOURCE and becomes an empty Bucket.
func TestLavaBucketPlacesSourceAndEmpties(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	aimDown(p)

	floor := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(floor, block.ToStateID[block.Stone{}], dimMinY)

	setHeldItem(p, item.LavaBucket.ID, 1)
	loop.useItemInHand(p, interactionHandMain)

	place := pk.Position{X: 1, Y: 65, Z: 1}
	if !isLavaSourceAt(loop, place) {
		id, _ := mgr.GetBlock(place, dimMinY)
		t.Fatalf("after lava_bucket use: cell %v = state %d, want a lava source", place, id)
	}
	held := ensureInventory(p).get(hotbarMenuSlotBase)
	if int32(held.ItemID) != int32(item.Bucket.ID) || held.Count != 1 {
		t.Fatalf("after empty: held = (id=%d, count=%d), want empty Bucket id=%d count=1",
			held.ItemID, held.Count, item.Bucket.ID)
	}
}

// TestLavaBucketPicksUpSource: the lava sibling of the water pickup test — an empty bucket over a lava
// SOURCE picks it up (→ lava_bucket, source removed).
func TestLavaBucketPicksUpSource(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	aimDown(p)

	src := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(src, lavaStateID(0), dimMinY)
	if !isLavaSourceAt(loop, src) {
		t.Fatalf("setup: %v is not a lava source", src)
	}

	setHeldItem(p, item.Bucket.ID, 1)
	loop.useItemInHand(p, interactionHandMain)

	if got, ok := mgr.GetBlock(src, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("after pickup: cell %v = (state %d, ok=%v), want air", src, got, ok)
	}
	held := ensureInventory(p).get(hotbarMenuSlotBase)
	if int32(held.ItemID) != int32(item.LavaBucket.ID) || held.Count != 1 {
		t.Fatalf("after pickup: held = (id=%d, count=%d), want lava_bucket id=%d count=1",
			held.ItemID, held.Count, item.LavaBucket.ID)
	}
}

// TestCreativeBucketDoesNotConsume: a CREATIVE player emptying a water bucket places the source but
// KEEPS the water_bucket in hand (getEmptySuccessItem / createFilledResult gate on
// hasInfiniteMaterials — creative never consumes/swaps the source bucket).
func TestCreativeBucketDoesNotConsume(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeCreative
	aimDown(p)

	floor := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(floor, block.ToStateID[block.Stone{}], dimMinY)

	setHeldItem(p, item.WaterBucket.ID, 1)
	loop.useItemInHand(p, interactionHandMain)

	place := pk.Position{X: 1, Y: 65, Z: 1}
	if !isWaterSourceAt(loop, place) {
		t.Fatalf("creative empty: cell %v is not a water source (the fluid must still be placed)", place)
	}
	// The hand keeps the water_bucket unchanged.
	held := ensureInventory(p).get(hotbarMenuSlotBase)
	if int32(held.ItemID) != int32(item.WaterBucket.ID) || held.Count != 1 {
		t.Fatalf("creative empty: held = (id=%d, count=%d), want water_bucket UNCHANGED id=%d count=1",
			held.ItemID, held.Count, item.WaterBucket.ID)
	}
}

// TestCreativeBucketPickupDoesNotSwap: a CREATIVE player picking up a water source removes the source
// but does NOT swap the empty bucket for a water_bucket (creative keeps the empty bucket; the picked
// water_bucket is only added to the inventory if absent — and an empty bucket is present, so nothing
// is added). Asserts the hand stays an empty Bucket.
func TestCreativeBucketPickupDoesNotSwap(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeCreative
	aimDown(p)

	src := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(src, waterStateID(0), dimMinY)

	setHeldItem(p, item.Bucket.ID, 1)
	loop.useItemInHand(p, interactionHandMain)

	// The source is still removed (pickupBlock runs regardless of creative — matches vanilla).
	if got, ok := mgr.GetBlock(src, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("creative pickup: cell %v = (state %d, ok=%v), want air", src, got, ok)
	}
	held := ensureInventory(p).get(hotbarMenuSlotBase)
	if int32(held.ItemID) != int32(item.Bucket.ID) || held.Count != 1 {
		t.Fatalf("creative pickup: held = (id=%d, count=%d), want empty Bucket UNCHANGED id=%d count=1",
			held.ItemID, held.Count, item.Bucket.ID)
	}
}

// TestEmptyBucketOnAirIsNoOp: an empty bucket aimed at nothing (no source, no solid in range) is a
// PASS no-op — no source is picked up and the hand keeps the empty bucket.
func TestEmptyBucketOnAirIsNoOp(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 200.0, 1.5) // high up over all-air: the down-ray hits nothing in reach
	p.gameMode = gameModeSurvival
	aimDown(p)

	setHeldItem(p, item.Bucket.ID, 1)
	loop.useItemInHand(p, interactionHandMain)

	held := ensureInventory(p).get(hotbarMenuSlotBase)
	if int32(held.ItemID) != int32(item.Bucket.ID) || held.Count != 1 {
		t.Fatalf("empty bucket on air: held = (id=%d, count=%d), want empty Bucket UNCHANGED count=1",
			held.ItemID, held.Count)
	}
}

// TestWaterBucketStackOfTwoRoutesEmptyToInventory: a survival player holding TWO empty-source buckets
// path — hold a stack of 2 water_bucket... but water_bucket StackSize is 1, so this exercises the
// createFilledResult ">1 in hand" branch with the empty-bucket stack instead: an empty bucket stack
// of 2 picking up a source consumes one and routes the water_bucket to the inventory, keeping 1 empty
// bucket in hand. Asserts the hand stays at 1 empty bucket and a water_bucket landed in a free slot.
func TestBucketStackRoutesFilledToInventory(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	aimDown(p)

	src := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(src, waterStateID(0), dimMinY)

	// A stack of 2 empty buckets in the hand (empty Bucket StackSize is 16, so a 2-stack is valid).
	setHeldItem(p, item.Bucket.ID, 2)
	loop.useItemInHand(p, interactionHandMain)

	// hand.consume(1) leaves 1 empty bucket; the water_bucket is routed to a free inventory slot.
	held := ensureInventory(p).get(hotbarMenuSlotBase)
	if int32(held.ItemID) != int32(item.Bucket.ID) || held.Count != 1 {
		t.Fatalf("stack pickup: held = (id=%d, count=%d), want empty Bucket count=1", held.ItemID, held.Count)
	}
	// The water_bucket must be somewhere in the inventory.
	inv := ensureInventory(p)
	found := false
	for i := int16(9); i < playerInventorySize; i++ {
		s := inv.get(i)
		if s.Count > 0 && int32(s.ItemID) == int32(item.WaterBucket.ID) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stack pickup: the picked water_bucket was not routed to the inventory")
	}
}
