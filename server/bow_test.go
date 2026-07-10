package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// bow_test.go covers the player bow + crossbow firing port (bow.go): the getPowerForTime curve,
// releaseUsing (spawn+shoot an arrow at power*3.0 with the full-draw crit + arrow consumption + bow
// durability), the low-draw abort, creative non-consumption, and the crossbow load-then-shoot.

func bowLoop() *TickLoop {
	loop, mgr := newBlockLoop()
	for _, r := range loop.regions {
		if r.entities == nil {
			r.entities = newEntityStore()
		}
		if r.world == nil {
			r.world = mgr
		}
	}
	return loop
}

func bowPlayer(loop *TickLoop, gameMode int32) *tickPlayer {
	p := blockPlayer(loop, 8.0, 65.0, 8.0)
	p.entityID = 7
	p.gameMode = gameMode
	p.yaw = 0
	p.pitch = 0
	return p
}

func giveSlot(p *tickPlayer, slot int16, id item.ID, count int32) {
	inv := ensureInventory(p)
	inv.set(slot, component.SlotData{Count: pk.VarInt(count), ItemID: pk.VarInt(id)})
}

func countArrows(p *tickPlayer) int {
	inv := ensureInventory(p)
	total := 0
	for i := int16(9); i < playerInventorySize; i++ {
		s := inv.get(i)
		if !slotIsEmpty(s) && item.ID(s.ItemID) == item.Arrow.ID {
			total += int(s.Count)
		}
	}
	return total
}

func firstArrow(loop *TickLoop) *Entity {
	for _, r := range loop.regions {
		if r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if e.isArrow {
				return e
			}
		}
	}
	return nil
}

func bowStart(loop *TickLoop, p *tickPlayer) bool {
	inv := ensureInventory(p)
	return loop.tryStartBowUse(p, inv, inv.get(hotbarMenuSlotBase), interactionHandMain)
}

// TestGetBowPowerForTime checks getPowerForTime at several charge values against the exact curve.
func TestGetBowPowerForTime(t *testing.T) {
	curve := func(charge int32) float32 {
		f := float32(charge) / 20.0
		f = (f*f + f*2.0) / 3.0
		if f > 1.0 {
			f = 1.0
		}
		return f
	}
	for _, charge := range []int32{0, 1, 5, 10, 15, 19, 20, 30, 72000} {
		if got, want := getBowPowerForTime(charge), curve(charge); got != want {
			t.Fatalf("getBowPowerForTime(%d) = %v, want %v", charge, got, want)
		}
	}
	if getBowPowerForTime(0) != 0 {
		t.Fatalf("power(0) = %v, want 0", getBowPowerForTime(0))
	}
	if getBowPowerForTime(20) != 1.0 {
		t.Fatalf("power(20) = %v, want 1.0", getBowPowerForTime(20))
	}
	if getBowPowerForTime(100) != 1.0 {
		t.Fatalf("power(100) = %v, want clamped 1.0", getBowPowerForTime(100))
	}
}

// TestReleaseFullDrawBowFiresArrow: a full draw (charge 20 -> power 1.0) spawns an arrow with velocity
// magnitude 3.0 along +Z, the crit flag set, and consumes exactly 1 arrow.
func TestReleaseFullDrawBowFiresArrow(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	giveSlot(p, hotbarMenuSlotBase, item.Bow.ID, 1)
	giveSlot(p, 9, item.Arrow.ID, 5)
	ensureInventory(p).heldSlot = 0

	if !bowStart(loop, p) {
		t.Fatalf("tryStartBowUse returned false for a bow")
	}
	if !isUsingItem(p) {
		t.Fatalf("player is not using the bow after tryStartBowUse")
	}
	p.useItemRemaining = bowUseDuration - 20 // full draw

	loop.releaseUsingItem(p)

	a := firstArrow(loop)
	if a == nil {
		t.Fatalf("no arrow spawned on full-draw bow release")
	}
	mag := math.Sqrt(a.vx*a.vx + a.vy*a.vy + a.vz*a.vz)
	if math.Abs(mag-3.0) > 1e-6 {
		t.Fatalf("arrow velocity magnitude = %.6f, want ~3.0", mag)
	}
	if a.vz <= 0 || math.Abs(a.vx) > 1e-6 || math.Abs(a.vy) > 1e-6 {
		t.Fatalf("arrow not fired straight along +Z: v=(%.4f,%.4f,%.4f)", a.vx, a.vy, a.vz)
	}
	if !a.arrowCrit {
		t.Fatalf("full-draw arrow is not crit")
	}
	if got := countArrows(p); got != 4 {
		t.Fatalf("arrows after shot = %d, want 4", got)
	}
	if isUsingItem(p) {
		t.Fatalf("use state not cleared after bow release")
	}
}

// TestReleaseLowDrawFiresNothing: power < 0.1 fires no arrow and consumes no ammo.
func TestReleaseLowDrawFiresNothing(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	giveSlot(p, hotbarMenuSlotBase, item.Bow.ID, 1)
	giveSlot(p, 9, item.Arrow.ID, 5)
	ensureInventory(p).heldSlot = 0

	bowStart(loop, p)
	p.useItemRemaining = bowUseDuration - 1 // charge 1 -> power ~0.034 < 0.1

	loop.releaseUsingItem(p)

	if a := firstArrow(loop); a != nil {
		t.Fatalf("low-draw bow release fired an arrow")
	}
	if got := countArrows(p); got != 5 {
		t.Fatalf("arrows after low-draw = %d, want 5", got)
	}
	if isUsingItem(p) {
		t.Fatalf("use state not cleared after low-draw release")
	}
}

// TestBowDoesNotConsumeInCreative: creative fires an arrow but consumes none.
func TestBowDoesNotConsumeInCreative(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeCreative)
	giveSlot(p, hotbarMenuSlotBase, item.Bow.ID, 1)
	giveSlot(p, 9, item.Arrow.ID, 5)
	ensureInventory(p).heldSlot = 0

	bowStart(loop, p)
	p.useItemRemaining = bowUseDuration - 20

	loop.releaseUsingItem(p)

	if a := firstArrow(loop); a == nil {
		t.Fatalf("creative bow release fired no arrow")
	}
	if got := countArrows(p); got != 5 {
		t.Fatalf("arrows after creative shot = %d, want 5", got)
	}
}

// TestBowNoAmmoNoStart: survival with no arrows cannot begin the draw.
func TestBowNoAmmoNoStart(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	giveSlot(p, hotbarMenuSlotBase, item.Bow.ID, 1)
	ensureInventory(p).heldSlot = 0

	if !bowStart(loop, p) {
		t.Fatalf("tryStartBowUse should return true even with no ammo")
	}
	if isUsingItem(p) {
		t.Fatalf("no-ammo survival bow should not begin a draw")
	}
}

// TestCrossbowLoadsAndShoots: a full crossbow draw (charge >= 25) loads the crossbow (consuming 1 arrow),
// and the next use fires the loaded bolt at power 3.15.
func TestCrossbowLoadsAndShoots(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	giveSlot(p, hotbarMenuSlotBase, item.Crossbow.ID, 1)
	giveSlot(p, 9, item.Arrow.ID, 5)
	ensureInventory(p).heldSlot = 0

	bowStart(loop, p)
	p.useItemRemaining = bowUseDuration - int32(crossbowChargeDuration) // full charge
	loop.releaseUsingItem(p)

	if !p.crossbowCharged {
		t.Fatalf("crossbow did not load after a full draw")
	}
	if got := countArrows(p); got != 4 {
		t.Fatalf("arrows after crossbow load = %d, want 4", got)
	}
	if firstArrow(loop) != nil {
		t.Fatalf("crossbow load should not spawn an arrow yet")
	}

	bowStart(loop, p) // charged crossbow use -> fire
	a := firstArrow(loop)
	if a == nil {
		t.Fatalf("charged crossbow use did not fire a bolt")
	}
	mag := math.Sqrt(a.vx*a.vx + a.vy*a.vy + a.vz*a.vz)
	if math.Abs(mag-crossbowShootPower) > 1e-6 {
		t.Fatalf("crossbow bolt velocity = %.6f, want %.6f", mag, crossbowShootPower)
	}
	if p.crossbowCharged {
		t.Fatalf("crossbow still charged after firing")
	}
}

// TestCrossbowShortDrawNoLoad: a short crossbow draw (<25) loads nothing.
func TestCrossbowShortDrawNoLoad(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	giveSlot(p, hotbarMenuSlotBase, item.Crossbow.ID, 1)
	giveSlot(p, 9, item.Arrow.ID, 5)
	ensureInventory(p).heldSlot = 0

	bowStart(loop, p)
	p.useItemRemaining = bowUseDuration - 5 // not full
	loop.releaseUsingItem(p)

	if p.crossbowCharged {
		t.Fatalf("short crossbow draw should not load")
	}
	if got := countArrows(p); got != 5 {
		t.Fatalf("arrows after short crossbow draw = %d, want 5", got)
	}
}

// TestReleaseArrowFromOffhand: offhand arrows are preferred and consumed there.
func TestReleaseArrowFromOffhand(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeSurvival)
	giveSlot(p, hotbarMenuSlotBase, item.Bow.ID, 1)
	giveSlot(p, offHandMenuSlot, item.Arrow.ID, 2)
	ensureInventory(p).heldSlot = 0

	bowStart(loop, p)
	p.useItemRemaining = bowUseDuration - 20
	loop.releaseUsingItem(p)

	if firstArrow(loop) == nil {
		t.Fatalf("no arrow fired with offhand ammo")
	}
	off := ensureInventory(p).get(offHandMenuSlot)
	if int(off.Count) != 1 {
		t.Fatalf("offhand arrows after shot = %d, want 1", off.Count)
	}
}

// TestArrowSpawnBaseDamageIsPlayerDefault: a player-fired arrow carries the default Arrow baseDamage 2.0.
func TestArrowSpawnBaseDamageIsPlayerDefault(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeCreative)
	a := loop.shootPlayerArrow(p, 3.0, true, component.SlotData{})
	if a.arrowBaseDamage != bowArrowBaseDamage {
		t.Fatalf("arrow baseDamage = %v, want %v", a.arrowBaseDamage, bowArrowBaseDamage)
	}
	if a.arrowShooterID != p.entityID {
		t.Fatalf("arrow shooter = %d, want %d", a.arrowShooterID, p.entityID)
	}
	if !a.arrowCrit {
		t.Fatalf("crit flag not set")
	}
	_ = bowLookVectorMagnitude(3.0)
}
