package attribute

import (
	"math"
	"testing"
)

// TestSuppliers_ExactBaseValues pins the EXACT per-entity base values the DefaultAttributes builders
// register, read directly from the jar bytecode (the createAttributes ldc2_w constants). A drift in
// any builder override is caught here. Values are compared bit-exactly (==) because the float-widened
// double literals (e.g. Cat speed 0.30000001192092896) MUST be reproduced bit-for-bit.
func TestSuppliers_ExactBaseValues(t *testing.T) {
	cases := []struct {
		entity string
		attr   *Attribute
		want   float64
	}{
		// Player.createAttributes: ATTACK_DAMAGE override 1.0, MOVEMENT_SPEED 0.10000000149011612,
		// ATTACK_SPEED default 4.0, plus living defaults.
		{"player", AttackDamage, 1.0},
		{"player", MovementSpeed, 0.10000000149011612},
		{"player", AttackSpeed, 4.0},
		{"player", MaxHealth, 20.0},
		{"player", Armor, 0.0},
		{"player", EntityInteractionRange, 3.0},
		{"player", SweepingDamageRatio, 0.0},

		// Witch : Monster + MAX_HEALTH 26.0, MOVEMENT_SPEED 0.25. Monster -> ATTACK_DAMAGE default 2.0.
		{"witch", MaxHealth, 26.0},
		{"witch", MovementSpeed, 0.25},
		{"witch", AttackDamage, 2.0}, // Monster registration default (no Witch override)
		{"witch", FollowRange, 16.0}, // Mob override

		// Cat : Animal + MAX_HEALTH 10.0, MOVEMENT_SPEED 0.30000001192092896, ATTACK_DAMAGE 3.0.
		// Animal -> TEMPT_RANGE 10.0.
		{"cat", MaxHealth, 10.0},
		{"cat", MovementSpeed, 0.30000001192092896},
		{"cat", AttackDamage, 3.0},
		{"cat", TemptRange, 10.0},
		{"cat", FollowRange, 16.0},

		// Villager : Mob + MOVEMENT_SPEED 0.5. NO attack_damage (Mob, not Monster), NO tempt_range.
		{"villager", MovementSpeed, 0.5},
		{"villager", MaxHealth, 20.0},
		{"villager", FollowRange, 16.0},

		// Zombie : Monster + FOLLOW_RANGE 35.0, MOVEMENT_SPEED 0.23000000417232513, ATTACK_DAMAGE 3.0,
		// ARMOR 2.0.
		{"zombie", FollowRange, 35.0},
		{"zombie", MovementSpeed, 0.23000000417232513},
		{"zombie", AttackDamage, 3.0},
		{"zombie", Armor, 2.0},
		{"zombie", MaxHealth, 20.0},

		// Silverfish : Monster + MAX_HEALTH 8.0, MOVEMENT_SPEED 0.25, ATTACK_DAMAGE 1.0.
		{"silverfish", MaxHealth, 8.0},
		{"silverfish", MovementSpeed, 0.25},
		{"silverfish", AttackDamage, 1.0},
	}
	for _, c := range cases {
		m := NewMapForEntity(c.entity)
		if m == nil {
			t.Fatalf("%s: no supplier registered", c.entity)
		}
		// GetValue with no modifiers == base; bit-exact compare (no epsilon) so widened-double
		// literals are caught.
		got := m.GetValue(c.attr.Name())
		if got != c.want || math.Float64bits(got) != math.Float64bits(c.want) {
			t.Errorf("%s.%s base = %v (bits %#x), want %v (bits %#x)",
				c.entity, c.attr.Name(), got, math.Float64bits(got), c.want, math.Float64bits(c.want))
		}
	}
}

// TestVillager_HasNoAttackDamage confirms Villager builds on createMobAttributes (NOT Monster), so it
// does NOT register attack_damage — a read falls back to 0.0 (no supplier instance), distinguishing
// the Mob vs Monster base chain.
func TestVillager_HasNoAttackDamage(t *testing.T) {
	m := NewMapForEntity("villager")
	if m == nil {
		t.Fatal("villager: no supplier")
	}
	if m.HasAttribute(AttackDamage.Name()) {
		t.Error("villager unexpectedly has attack_damage (should build on Mob, not Monster)")
	}
	if got := m.GetValue(AttackDamage.Name()); got != 0.0 {
		t.Errorf("villager attack_damage = %v, want 0.0 (unregistered)", got)
	}
}

// TestNewMapForEntity_UnknownType returns nil for an unported entity type (no DefaultAttributes
// supplier), so the caller's nil-check degrades to registration defaults.
func TestNewMapForEntity_UnknownType(t *testing.T) {
	if m := NewMapForEntity("ender_dragon"); m != nil {
		t.Errorf("NewMapForEntity(unported) = %v, want nil", m)
	}
	if HasSupplier("ender_dragon") {
		t.Error("HasSupplier(unported) = true, want false")
	}
}

// TestMap_LocalDivergence confirms the two-tier model: mutating an instance via GetInstance diverges
// the entity locally without touching the shared supplier (a second map of the same type still reads
// the supplier default).
func TestMap_LocalDivergence(t *testing.T) {
	m1 := NewMapForEntity("zombie")
	m2 := NewMapForEntity("zombie")
	// Diverge m1's attack_damage via a local modifier.
	inst := m1.GetInstance(AttackDamage.Name())
	if inst == nil {
		t.Fatal("zombie attack_damage instance nil")
	}
	inst.AddTransientModifier(AttributeModifier{ID: "buff", Amount: 10.0, Operation: AddValue})
	if got := m1.GetValue(AttackDamage.Name()); got != 13.0 { // 3.0 base + 10
		t.Errorf("m1 attack_damage = %v, want 13.0", got)
	}
	// m2 must be unaffected (shared supplier, separate local maps).
	if got := m2.GetValue(AttackDamage.Name()); got != 3.0 {
		t.Errorf("m2 attack_damage = %v, want 3.0 (supplier default, unaffected)", got)
	}
}
