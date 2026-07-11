package server

// horse_interact_test.go -- deterministic pins for the HORSE FAMILY mobInteract ride/tame/chest/feed
// (net.minecraft.world.entity.animal.equine.{Horse,Donkey,AbstractHorse}.mobInteract + handleEating +
// RunAroundLikeCrazyGoal, 1:1 javap this task). Verifies: feeding raises temper toward the tame threshold
// and the RunAroundLikeCrazyGoal roll tames on the threshold; a tamed adult horse mounts the player; a
// baby / untamed horse is NOT left mounted (the untamed mount bucks); a donkey with a chest opens its 5-
// column inventory when a CHEST is equipped.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// horseInteractLoop builds a single-region loop with a flat floor, ready to spawn horse-family mobs and
// register riders. Mirrors camelRideLoop's shape.
func horseInteractLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	_ = mgr
	return loop, floorY
}

// horseRider registers a tickPlayer holding itemID (main hand), at the given position, with entityID, and a
// capture client + tracked set so broadcastSetPassengers can send. itemID 0 means an empty hand.
func horseRider(loop *TickLoop, entityID int32, x, y, z float64, itemID int32) *tickPlayer {
	p := &tickPlayer{x: x, y: y, z: z, entityID: entityID, client: captureClient(64)}
	p.tracked = make(map[int32]bool)
	inv := ensureInventory(p)
	if itemID != 0 {
		inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(itemID)})
	}
	loop.players = append(loop.players, p)
	return p
}

// TestHorseFeedRaisesTemperAndTames: feeding an untamed adult horse WHEAT raises its temper (handleEating
// modifyTemper(3)); once temper is high, the RunAroundLikeCrazyGoal roll on a mounted untamed horse tames
// it when nextInt(getMaxTemper()) < getTemper(). We feed to max temper deterministically (no RNG in
// handleEating), then drive the goal with the temper AT max so the tame is guaranteed (nextInt(100) < 100
// is always true).
func TestHorseFeedRaisesTemperAndTames(t *testing.T) {
	loop, floorY := horseInteractLoop(t)
	h := loop.spawnHorse(8.5, float64(floorY+1), 8.5, false)
	h.onGround = true

	// Feed WHEAT (id 980): an untamed horse's temper rises by 3 per feed (handleEating temper branch).
	feeder := horseRider(loop, 1, 8.6, float64(floorY+1), 8.5, itemWheat)
	before := h.horseTemper
	if !loop.tryHorseFamilyInteract(feeder, h, false) {
		t.Fatal("feeding an untamed horse wheat did not consume the interact (tryHorseFamilyInteract false)")
	}
	if h.horseTemper != before+3 {
		t.Fatalf("horse temper after 1 wheat feed = %d, want %d (modifyTemper(3))", h.horseTemper, before+3)
	}
	if h.horseTamed {
		t.Fatal("feeding wheat directly tamed the horse (taming is the RunAroundLikeCrazyGoal roll, not the feed)")
	}

	// Drive the tame roll: set temper to max so nextInt(getMaxTemper()) < getTemper() is always true, mount a
	// rider, and tick the goal until the throttle draw hits 0 (it must tame, never buck, once it fires).
	h.horseTemper = h.horseMaxTemper() // 100
	rider := horseRider(loop, 2, 8.5, float64(floorY+1), 8.5, 0)
	if !loop.playerStartRiding(rider, h, false) {
		t.Fatal("could not mount the untamed horse to drive the RunAroundLikeCrazyGoal tame roll")
	}
	tamed := false
	for i := 0; i < 5000 && !tamed; i++ {
		loop.horseFamilyAiStep(h)
		tamed = h.horseTamed
	}
	if !tamed {
		t.Fatal("a max-temper untamed ridden horse never tamed via RunAroundLikeCrazyGoal (tame roll never hit)")
	}
	if len(h.passengers) == 0 {
		t.Fatal("a tamed horse should keep its rider (tameWithName does not eject) -- rider was bucked")
	}
}

// TestHorseTamedAdultMounts: a TAMED, ADULT horse right-clicked with an empty hand (no secondary) mounts the
// player (doPlayerRide -> startRiding); the player becomes the horse's first passenger.
func TestHorseTamedAdultMounts(t *testing.T) {
	loop, floorY := horseInteractLoop(t)
	h := loop.spawnHorse(8.5, float64(floorY+1), 8.5, false)
	h.onGround = true
	h.horseTamed = true // a tamed adult

	rider := horseRider(loop, 1, 8.5, float64(floorY+1), 8.5, 0)
	if !loop.tryHorseFamilyInteract(rider, h, false) {
		t.Fatal("tryHorseFamilyInteract false for a tamed adult horse empty-hand click (should mount)")
	}
	if rider.vehicleID != h.id {
		t.Fatalf("after mount, rider vehicleID = %d, want %d", rider.vehicleID, h.id)
	}
	if len(h.passengers) != 1 || h.passengers[0] != rider.entityID {
		t.Fatalf("after mount, horse passengers = %v, want [%d]", h.passengers, rider.entityID)
	}
}

// TestHorseBabyDoesNotMount: a BABY horse right-clicked with an empty hand routes to the base tail (isBaby()
// && !isHolding(GOLDEN_DANDELION)); doPlayerRide startRides but a baby has no controlling passenger and the
// mount is not a rideable adult. We assert the interact is consumed (SUCCESS) but the observable ride is not
// a steerable one: a baby's getControllingPassenger is 0 (isSaddled/baby gate). Also an UNTAMED adult
// mounted is bucked by the RunAroundLikeCrazyGoal (temper 0 -> nextInt(100) < 0 is never true -> buck).
func TestHorseBabyDoesNotMount(t *testing.T) {
	loop, floorY := horseInteractLoop(t)

	// A baby horse: the interact is consumed but it is not a steerable ride.
	baby := loop.spawnHorse(8.5, float64(floorY+1), 8.5, true)
	baby.onGround = true
	baby.horseTamed = true // even a tamed baby is not rideable
	rider := horseRider(loop, 1, 8.5, float64(floorY+1), 8.5, 0)
	loop.tryHorseFamilyInteract(rider, baby, false)
	if loop.getControllingPassenger(baby) != 0 {
		t.Fatal("a baby horse has a controlling passenger (a baby is not a steerable ride)")
	}

	// An UNTAMED adult with temper 0: mount it, then the RunAroundLikeCrazyGoal must BUCK (never tame), since
	// nextInt(getMaxTemper()) < 0 is impossible. Drive many ticks; the rider must be ejected and not tamed.
	wild := loop.spawnHorse(20.5, float64(floorY+1), 20.5, false)
	wild.onGround = true
	wild.horseTemper = 0
	rider2 := horseRider(loop, 2, 20.5, float64(floorY+1), 20.5, 0)
	if !loop.playerStartRiding(rider2, wild, false) {
		t.Fatal("could not mount the untamed adult to test the buck")
	}
	bucked := false
	for i := 0; i < 5000 && !bucked; i++ {
		loop.horseFamilyAiStep(wild)
		bucked = len(wild.passengers) == 0
	}
	if !bucked {
		t.Fatal("a temper-0 untamed ridden horse never bucked its rider (RunAroundLikeCrazyGoal buck never hit)")
	}
	if wild.horseTamed {
		t.Fatal("a temper-0 horse tamed itself (nextInt(100) < 0 is impossible -- it must never tame)")
	}
}

// TestDonkeyOpensChestInventory: a DONKEY right-clicked with a CHEST equips the chest (setChest(true)) and
// gains its 5-column storage inventory (getInventoryColumns() == 5 with a chest). A second right-click by a
// TAMED donkey owner while sneaking opens that inventory (the tamed-secondary base branch).
func TestDonkeyOpensChestInventory(t *testing.T) {
	loop, floorY := horseInteractLoop(t)
	d := loop.spawnDonkey(8.5, float64(floorY+1), 8.5, false)
	d.onGround = true
	d.horseTamed = true // a tamed donkey (chest-equip works untamed too, but a real donkey is usually tamed)

	if d.typ != entity.Donkey.ID || !d.isDonkey {
		t.Fatalf("spawnDonkey typ = %d isDonkey = %v, want Donkey", d.typ, d.isDonkey)
	}
	if d.horseHasChest {
		t.Fatal("a fresh donkey already has a chest")
	}
	if got := d.horseGetInventoryColumns(); got != 0 {
		t.Fatalf("a chestless donkey inventory columns = %d, want 0", got)
	}

	// Equip a CHEST (id 359): the chest-equip branch consumes the chest and sets 5 columns.
	chestHolder := horseRider(loop, 1, 8.6, float64(floorY+1), 8.5, itemChest)
	if !loop.tryHorseFamilyInteract(chestHolder, d, false) {
		t.Fatal("equipping a chest on a donkey did not consume the interact")
	}
	if !d.horseHasChest {
		t.Fatal("after a chest-equip the donkey has no chest (setChest not applied)")
	}
	if got := d.horseGetInventoryColumns(); got != chestedInventoryColumns {
		t.Fatalf("chested donkey inventory columns = %d, want %d", got, chestedInventoryColumns)
	}
	held := ensureInventory(chestHolder).get(heldWindowSlot(ensureInventory(chestHolder).heldSlot))
	if held.Count != 0 {
		t.Fatalf("chest not consumed: held count = %d, want 0", held.Count)
	}

	// A tamed owner sneak-click opens the inventory (the base tail tamed-secondary branch): the interact is
	// consumed and the horse inventory columns are recomputed to 5.
	opener := horseRider(loop, 2, 8.5, float64(floorY+1), 8.5, 0)
	if !loop.tryHorseFamilyInteract(opener, d, true) { // usingSecondaryAction == sneak
		t.Fatal("a tamed donkey sneak-click did not consume the interact (openCustomInventoryScreen)")
	}
	if d.horseInvColumns != chestedInventoryColumns {
		t.Fatalf("after open, donkey horseInvColumns = %d, want %d", d.horseInvColumns, chestedInventoryColumns)
	}
	// The sneak-open must NOT mount the opener (the inventory branch returns before doPlayerRide).
	if opener.vehicleID != 0 {
		t.Fatalf("a tamed sneak-click mounted the donkey (vehicleID %d); it should open the inventory", opener.vehicleID)
	}
}
