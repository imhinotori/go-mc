package server

// enchant_wave_test.go -- the E-3 enchant-effect wave: MULTISHOT, PIERCING, VANISHING CURSE, BINDING CURSE.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// allArrows collects every arrow entity across all regions (multishot spawns 3).
func allArrows(loop *TickLoop) []*Entity {
	var out []*Entity
	for _, r := range loop.regions {
		if r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if e.isArrow {
				out = append(out, e)
			}
		}
	}
	return out
}

// TestEnchantMultishotFires3Arrows: a charged crossbow with Multishot I fires 3 arrows on use (vs 1 for a
// plain crossbow), and the three launch yaws are the center + the -10 / +10 side arrows. Cite multishot.json
// (projectile_count add 2+2/lvl -> 3; projectile_spread add 10 -> 10 deg) + ProjectileWeaponItem.shoot.
func TestEnchantMultishotFires3Arrows(t *testing.T) {
	loop := bowLoop()
	p := bowPlayer(loop, gameModeCreative)
	p.yaw = 0
	p.pitch = 0

	msCrossbow := enchantedStack(int(item.Crossbow.ID), 1, enchTestEntry(t, "minecraft:multishot", 1))
	p.crossbowCharged = true
	loop.fireCrossbow(p, ensureInventory(p), msCrossbow, interactionHandMain)

	arrows := allArrows(loop)
	if len(arrows) != 3 {
		t.Fatalf("multishot fired %d arrows, want 3", len(arrows))
	}

	near := func(yaws []float32, target float32) bool {
		for _, y := range yaws {
			if math.Abs(float64(y-target)) < 4.0 {
				return true
			}
		}
		return false
	}
	var yaws []float32
	for _, a := range arrows {
		yaws = append(yaws, a.yaw)
	}
	if !near(yaws, 0) || !near(yaws, -10) || !near(yaws, 10) {
		t.Fatalf("multishot arrow yaws = %v, want ~{0, -10, +10}", yaws)
	}

	loop2 := bowLoop()
	p2 := bowPlayer(loop2, gameModeCreative)
	p2.crossbowCharged = true
	plain := component.SlotData{ItemID: pk.VarInt(int(item.Crossbow.ID)), Count: 1}
	loop2.fireCrossbow(p2, ensureInventory(p2), plain, interactionHandMain)
	if got := len(allArrows(loop2)); got != 1 {
		t.Fatalf("plain crossbow fired %d arrows, want 1", got)
	}
}

// TestEnchantMultishotVolleyAngles: the fireCrossbowVolley angle math (ProjectileWeaponItem.shoot offsets
// 10-200) for count=3, spread=10 yields exactly the angle sequence 0, -10, +10 (before per-axis inaccuracy).
func TestEnchantMultishotVolleyAngles(t *testing.T) {
	count := 3
	spread := float32(10.0)
	var f2 float32
	if count == 1 {
		f2 = 0
	} else {
		f2 = 2.0 * spread / float32(count-1)
	}
	f3 := float32((count-1)%2) * f2 / 2.0
	f4 := float32(1.0)
	got := make([]float32, 0, count)
	for i := 0; i < count; i++ {
		got = append(got, f3+f4*float32((i+1)/2)*f2)
		f4 = -f4
	}
	want := []float32{0, -10, 10}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("volley angle[%d] = %v, want %v (angles %v)", i, got[i], want[i], got)
		}
	}
}

// TestEnchantPiercingPassesThroughEntities: a crossbow bolt with Piercing II gets pierceLevel 2 and passes
// THROUGH 3 mobs in a row (pierceLevel+1), hurting each once, then discards on the 4th. Cite piercing.json +
// AbstractArrow.getPiercingCount + onHitEntity.
func TestEnchantPiercingPassesThroughEntities(t *testing.T) {
	loop, floorY, _ := arrowLoop(t)

	crossbow := enchantedStack(int(item.Crossbow.ID), 1, enchTestEntry(t, "minecraft:piercing", 2))

	baseY := float64(floorY + 1)
	pigs := make([]*Entity, 4)
	for i := 0; i < 4; i++ {
		pigs[i] = NewEntity(int32(1000+i), entity.Pig, 9.5+float64(i), baseY+1.0, 8.5)
		pigs[i].health = 50.0
		loop.only().entities.add(pigs[i])
	}

	a := loop.spawnArrow(999, 8.0, baseY+1.0, 8.5, 8.0, 0.0, 0.0, 3.0)
	a.arrowWeapon = crossbow
	if pc := loop.enchGetPiercingCount(crossbow); pc > 0 {
		a.arrowPierceLevel = byte(pc)
	}
	if a.arrowPierceLevel != 2 {
		t.Fatalf("Piercing II pierceLevel = %d, want 2", a.arrowPierceLevel)
	}

	loop.tickArrow(a)

	hurt := 0
	for i, pg := range pigs {
		if pg.health < 50.0 {
			hurt++
			if i == 3 {
				t.Fatalf("Piercing II hit the 4th pig (health %v); should stop at pierceLevel+1 = 3", pg.health)
			}
		}
	}
	if hurt != 3 {
		t.Fatalf("Piercing II hurt %d pigs, want 3 (pierceLevel+1)", hurt)
	}
	if _, ok := loop.only().entities.byID[a.id]; ok {
		t.Fatalf("piercing arrow was not discarded after reaching the pierce limit")
	}
}

// TestEnchantPiercingZeroConsumesOnFirstHit: a pierceLevel-0 arrow is consumed on its first entity hit
// exactly as before (regression guard for the pre-pierce path).
func TestEnchantPiercingZeroConsumesOnFirstHit(t *testing.T) {
	loop, floorY, _ := arrowLoop(t)
	baseY := float64(floorY + 1)
	pig := NewEntity(2000, entity.Pig, 9.5, baseY+1.0, 8.5)
	pig.health = 50.0
	loop.only().entities.add(pig)

	a := loop.spawnArrow(999, 8.0, baseY+1.0, 8.5, 8.0, 0.0, 0.0, 3.0)
	if a.arrowPierceLevel != 0 {
		t.Fatalf("plain arrow pierceLevel = %d, want 0", a.arrowPierceLevel)
	}
	loop.tickArrow(a)
	if pig.health >= 50.0 {
		t.Fatalf("plain arrow dealt no damage")
	}
	if _, ok := loop.only().entities.byID[a.id]; ok {
		t.Fatalf("plain (pierce-0) arrow was not consumed on its first hit")
	}
}

// TestEnchantVanishingCurseVanishesOnDeath: an inventory stack with Vanishing Curse is DELETED on death (no
// ItemEntity dropped); a normal stack drops. Cite Player.destroyVanishingCursedItems.
func TestEnchantVanishingCurseVanishesOnDeath(t *testing.T) {
	loop, mgr := newBlockLoop()
	for _, r := range loop.regions {
		if r.entities == nil {
			r.entities = newEntityStore()
		}
		if r.world == nil {
			r.world = mgr
		}
	}
	p := blockPlayer(loop, 8.0, 65.0, 8.0)
	p.entityID = 11
	p.gameMode = gameModeSurvival

	inv := ensureInventory(p)
	cursed := enchantedStack(int(item.DiamondSword.ID), 1, enchTestEntry(t, "minecraft:vanishing_curse", 1))
	inv.set(9, cursed)
	inv.set(10, component.SlotData{ItemID: pk.VarInt(int(item.Diamond.ID)), Count: 1})

	loop.dropPlayerEquipment(p)

	if !stackEmpty(inv.get(9)) {
		t.Fatalf("vanishing-cursed stack still present after death (slot 9 = %+v)", inv.get(9))
	}
	if !stackEmpty(inv.get(10)) {
		t.Fatalf("plain stack should have been dropped-and-cleared (slot 10 = %+v)", inv.get(10))
	}

	drops := 0
	var droppedID int32
	for _, r := range loop.regions {
		if r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if e.isItem {
				drops++
				droppedID = int32(e.itemStack.ItemID)
			}
		}
	}
	if drops != 1 {
		t.Fatalf("death dropped %d item entities, want 1 (the cursed sword must vanish)", drops)
	}
	if item.ID(droppedID) != item.Diamond.ID {
		t.Fatalf("dropped item id = %d, want the plain diamond (the sword vanished)", droppedID)
	}
}

// TestEnchantBindingCurseBlocksUnequip: a Binding-Curse armor piece in an armor slot cannot be picked up by
// a non-creative player (mayPickup false); a creative player can; a plain piece is pickable. Cite
// ArmorSlot.mayPickup (PREVENT_ARMOR_CHANGE gate).
func TestEnchantBindingCurseBlocksUnequip(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 8.0, 65.0, 8.0)
	p.gameMode = gameModeSurvival
	inv := ensureInventory(p)

	bound := enchantedStack(int(item.DiamondChestplate.ID), 1, enchTestEntry(t, "minecraft:binding_curse", 1))
	inv.set(6, bound)

	chestSlot := menuSlot{inv: inv, index: 6}
	if chestSlot.mayPickup(p) {
		t.Fatalf("Binding Curse armor should NOT be pickable by a survival player")
	}

	p.gameMode = gameModeCreative
	if !chestSlot.mayPickup(p) {
		t.Fatalf("Binding Curse armor should be pickable by a creative player (creative bypass)")
	}
	p.gameMode = gameModeSurvival

	inv.set(6, component.SlotData{ItemID: pk.VarInt(int(item.DiamondChestplate.ID)), Count: 1})
	if !chestSlot.mayPickup(p) {
		t.Fatalf("un-cursed armor must be pickable")
	}
}
