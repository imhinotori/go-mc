package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// equippableArmorStack builds a damageable armor stack carrying an EQUIPPABLE component with the given
// damage_on_hurt flag (Damageable field), plus MAX_DAMAGE + DAMAGE so it is a damageable item.
func equippableArmorStack(maxDamage, damage int, damageOnHurt bool) component.SlotData {
	s := component.SlotData{ItemID: 800, Count: 1}
	p := component.DecodePatch(s)
	p.Set(compMaxDamage, &component.MaxDamage{VarInt: pk.VarInt(maxDamage)})
	p.Set(compDamage, &component.Damage{VarInt: pk.VarInt(damage)})
	p.Set(compEquippable, &component.Equippable{Damageable: pk.Boolean(damageOnHurt)})
	return p.ApplyTo(s)
}

// TestDoHurtEquipmentWears: a worn damage_on_hurt armor piece takes max(1, floor(dmg/4)) durability.
func TestDoHurtEquipmentWears(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{entityID: 1}
	inv := ensureInventory(p)
	inv.set(6, equippableArmorStack(100, 0, true)) // chest slot (menu 6)

	loop.doHurtEquipment(p, 8.0) // armorDamage = max(1, floor(8/4)) = 2
	got := stackDamageValue(inv.get(6))
	if got != 2 {
		t.Fatalf("chestplate damage after 8.0 hit = %d, want 2 (max(1, floor(8/4)))", got)
	}
}

// TestDoHurtEquipmentMinimumOne: a small hit still costs at least 1 durability (the max(1,...) floor).
func TestDoHurtEquipmentMinimumOne(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{entityID: 1}
	inv := ensureInventory(p)
	inv.set(5, equippableArmorStack(100, 0, true)) // head slot (menu 5)

	loop.doHurtEquipment(p, 1.0) // armorDamage = max(1, floor(1/4)=0) = 1
	if got := stackDamageValue(inv.get(5)); got != 1 {
		t.Fatalf("helmet damage after 1.0 hit = %d, want 1 (the max(1,...) floor)", got)
	}
}

// TestDoHurtEquipmentSkipsNonDamageOnHurt: an equippable WITHOUT damage_on_hurt is not worn down.
func TestDoHurtEquipmentSkipsNonDamageOnHurt(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{entityID: 1}
	inv := ensureInventory(p)
	inv.set(6, equippableArmorStack(100, 0, false)) // damage_on_hurt = false

	loop.doHurtEquipment(p, 20.0)
	if got := stackDamageValue(inv.get(6)); got != 0 {
		t.Fatalf("a non-damage_on_hurt equippable must not wear: damage = %d, want 0", got)
	}
}

// TestDoHurtEquipmentCreative: creative players' armor never wears.
func TestDoHurtEquipmentCreative(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{entityID: 1, gameMode: gameModeCreative}
	inv := ensureInventory(p)
	inv.set(6, equippableArmorStack(100, 0, true))

	loop.doHurtEquipment(p, 20.0)
	if got := stackDamageValue(inv.get(6)); got != 0 {
		t.Fatalf("creative armor must not wear: damage = %d, want 0", got)
	}
}
