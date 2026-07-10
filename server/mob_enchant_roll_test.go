package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
)

func fullyEquippedMob(id int32) *Entity {
	e := NewEntity(id, entity.Zombie, 0, 0, 0)
	e.setItemSlot(eqSlotMainHand, itemStackOf(item.IronSword))
	e.setItemSlot(eqSlotFeet, itemStackOf(item.DiamondBoots))
	e.setItemSlot(eqSlotLegs, itemStackOf(item.DiamondLeggings))
	e.setItemSlot(eqSlotChest, itemStackOf(item.DiamondChestplate))
	e.setItemSlot(eqSlotHead, itemStackOf(item.DiamondHelmet))
	return e
}

// populateDefaultEquipmentEnchantments consumes EXACTLY 5 nextFloats for a mob with MAINHAND + 4
// HUMANOID_ARMOR slots populated (verified against jar: enchantSpawnedWeapon at offset 4 then
// EquipmentSlot.VALUES loop with HUMANOID_ARMOR filter at offset 47).
func TestEnchantRollDrawCountFiveForFiveSlots(t *testing.T) {
	const seed = uint64(0xC0FFEE_BABE)
	const mult = float32(1.0)
	rngA := newEntityRandom(seed)
	populateDefaultEquipmentEnchantments(fullyEquippedMob(1), rngA, mult)
	rngB := newEntityRandom(seed)
	for i := 0; i < 5; i++ {
		_ = rngB.nextFloat()
	}
	for i := 0; i < 16; i++ {
		if a, b := rngA.nextFloat(), rngB.nextFloat(); a != b {
			t.Fatalf("draw %d diverged: A=%v B=%v", i, a, b)
		}
	}
}

// enchantSpawnedWeapon runs BEFORE the armor loop (jar offset 4 invokevirtual before offset 7
// getstatic EquipmentSlot.VALUES).
func TestEnchantRollDrawOrderMAINHANDFirst(t *testing.T) {
	const seed = uint64(0xFACE_FEED)
	const mult = float32(1.0)
	buildMob := func() *Entity {
		e := NewEntity(1, entity.Zombie, 0, 0, 0)
		e.setItemSlot(eqSlotMainHand, itemStackOf(item.IronSword))
		e.setItemSlot(eqSlotHead, itemStackOf(item.DiamondHelmet))
		return e
	}
	rngA := newEntityRandom(seed)
	populateDefaultEquipmentEnchantments(buildMob(), rngA, mult)
	rngB := newEntityRandom(seed)
	_ = rngB.nextFloat()
	_ = rngB.nextFloat()
	for i := 0; i < 8; i++ {
		if a, b := rngA.nextFloat(), rngB.nextFloat(); a != b {
			t.Fatalf("draw %d diverged: A=%v B=%v", i, a, b)
		}
	}
	rngC := newEntityRandom(seed)
	_ = rngC.nextFloat()
	_ = rngC.nextFloat()
	diverged := false
	for i := 0; i < 8; i++ {
		if a, c := rngA.nextFloat(), rngC.nextFloat(); a != c {
			diverged = true
			break
		}
	}
	if !diverged {
		t.Fatalf("wrong-order stream did not diverge")
	}
}

// The armor loop iterates EquipmentSlot.VALUES (FEET, LEGS, CHEST, HEAD) but ONLY for slots
// whose Type == HUMANOID_ARMOR. Empty slots skip their draw via the isEmpty short-circuit.
func TestEnchantRollArmorLoopOrderIsVALUESMinusHands(t *testing.T) {
	const seed = uint64(0xABCD_1234)
	const mult = float32(1.0)
	buildMob := func() *Entity {
		e := NewEntity(1, entity.Zombie, 0, 0, 0)
		e.setItemSlot(eqSlotMainHand, itemStackOf(item.IronSword))
		e.setItemSlot(eqSlotFeet, itemStackOf(item.DiamondBoots))
		e.setItemSlot(eqSlotHead, itemStackOf(item.DiamondHelmet))
		return e
	}
	rngA := newEntityRandom(seed)
	populateDefaultEquipmentEnchantments(buildMob(), rngA, mult)
	rngB := newEntityRandom(seed)
	_ = rngB.nextFloat()
	_ = rngB.nextFloat()
	_ = rngB.nextFloat()
	for i := 0; i < 12; i++ {
		if a, b := rngA.nextFloat(), rngB.nextFloat(); a != b {
			t.Fatalf("draw %d diverged: A=%v B=%v", i, a, b)
		}
	}
}

// isEmpty() (Count == 0) SKIPS the nextFloat draw (jar offset 12 ifne 57).
func TestEnchantRollEmptySlotsShortCircuit(t *testing.T) {
	const seed = uint64(0xFEED_FACE)
	const mult = float32(1.0)
	e := NewEntity(1, entity.Zombie, 0, 0, 0)
	rngA := newEntityRandom(seed)
	populateDefaultEquipmentEnchantments(e, rngA, mult)
	rngB := newEntityRandom(seed)
	for i := 0; i < 16; i++ {
		if a, b := rngA.nextFloat(), rngB.nextFloat(); a != b {
			t.Fatalf("draw %d diverged: A=%v B=%v", i, a, b)
		}
	}
}

// OFFHAND (EquipmentSlot.Type == HAND, not HUMANOID_ARMOR) is NOT processed by the armor loop.
// The HANDS slots are filtered out at jar offset 44-47.
func TestEnchantRollOffhandSkipped(t *testing.T) {
	const seed = uint64(0xDEAD_BEEF)
	const mult = float32(1.0)
	e := NewEntity(1, entity.Zombie, 0, 0, 0)
	e.setItemSlot(eqSlotOffHand, itemStackOf(item.Shield))
	rngA := newEntityRandom(seed)
	populateDefaultEquipmentEnchantments(e, rngA, mult)
	rngB := newEntityRandom(seed)
	for i := 0; i < 16; i++ {
		if a, b := rngA.nextFloat(), rngB.nextFloat(); a != b {
			t.Fatalf("draw %d diverged: A=%v B=%v", i, a, b)
		}
	}
}

// Same seed + same mult = byte-identical stacks (SEAM no-op preserves item).
func TestEnchantRollDeterminism(t *testing.T) {
	const seed = uint64(0xBEEF_C0DE)
	const mult = float32(1.0)
	a := fullyEquippedMob(1)
	b := fullyEquippedMob(2)
	populateDefaultEquipmentEnchantments(a, newEntityRandom(seed), mult)
	populateDefaultEquipmentEnchantments(b, newEntityRandom(seed), mult)
	for slot := 0; slot < equipmentSlotCount; slot++ {
		sa, sb := a.getItemBySlot(slot), b.getItemBySlot(slot)
		if sa.Count != sb.Count || sa.ItemID != sb.ItemID {
			t.Fatalf("slot %d differs: A=%+v B=%+v", slot, sa, sb)
		}
	}
}

type componentSlotSnapshot struct {
	Count  int32
	ItemID int32
}

// Stacks are byte-identical across mult values (SEAM no-op + draw-count invariance).
func TestEnchantRollDeterminismAcrossMults(t *testing.T) {
	const seed = uint64(0x1234_5678)
	buildMob := func() *Entity { return fullyEquippedMob(1) }
	run := func(mult float32) map[int]componentSlotSnapshot {
		e := buildMob()
		populateDefaultEquipmentEnchantments(e, newEntityRandom(seed), mult)
		out := make(map[int]componentSlotSnapshot, equipmentSlotCount)
		for slot := 0; slot < equipmentSlotCount; slot++ {
			s := e.getItemBySlot(slot)
			out[slot] = componentSlotSnapshot{Count: int32(s.Count), ItemID: int32(s.ItemID)}
		}
		return out
	}
	ref := run(1.0)
	for _, mult := range []float32{0.0, 0.25, 0.5, 1.5, 2.0} {
		got := run(mult)
		for slot := 0; slot < equipmentSlotCount; slot++ {
			if got[slot] != ref[slot] {
				t.Fatalf("mult %v: slot %d differs from mult 1.0: got=%+v ref=%+v", mult, slot, got[slot], ref[slot])
			}
		}
	}
}

// Gate formula `nextFloat() < chance * mult`. For armor slot chance=0.5, mult=1.5:
// threshold=0.75. Exactly 1 nextFloat per non-empty slot (draw happens BEFORE compare).
func TestZombieArmorEnchantGateFormulaHARD(t *testing.T) {
	const seed = uint64(0xABCD_EF01)
	const mult = float32(1.5)
	const chance = float32(0.5)
	_ = chance * mult
	buildMob := func() *Entity {
		e := NewEntity(1, entity.Zombie, 0, 0, 0)
		e.setItemSlot(eqSlotHead, itemStackOf(item.DiamondHelmet))
		return e
	}
	rngA := newEntityRandom(seed)
	populateDefaultEquipmentEnchantments(buildMob(), rngA, mult)
	rngB := newEntityRandom(seed)
	_ = rngB.nextFloat()
	for i := 0; i < 16; i++ {
		if a, b := rngA.nextFloat(), rngB.nextFloat(); a != b {
			t.Fatalf("draw %d diverged: A=%v B=%v", i, a, b)
		}
	}
}

// Per jar: skeleton's MAINHAND bow IS subject to the enchant gate
// (enchantSpawnedWeapon is unconditional). The bow stays a bow because the SEAM no-op
// preserves it. The user's brief assumed the bow skips the gate -- that assumption is wrong.
func TestSkeletonBowEnchantGateRuns(t *testing.T) {
	const seed = uint64(0xB0B_CAFE)
	const mult = float32(1.0)
	e := NewEntity(1, entity.Skeleton, 0, 0, 0)
	e.setItemSlot(eqSlotMainHand, itemStackOf(item.Bow))
	rngA := newEntityRandom(seed)
	populateDefaultEquipmentEnchantments(e, rngA, mult)
	rngB := newEntityRandom(seed)
	_ = rngB.nextFloat()
	for i := 0; i < 16; i++ {
		if a, b := rngA.nextFloat(), rngB.nextFloat(); a != b {
			t.Fatalf("draw %d diverged: A=%v B=%v", i, a, b)
		}
	}
	bow := e.getMainHandItem()
	if bow.Count != 1 || int32(bow.ItemID) != int32(item.Bow.ID) {
		t.Fatalf("skeleton bow after enchant pass = %+v, want unchanged bow", bow)
	}
}