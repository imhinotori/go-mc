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

// TestNewMapForEntity_NonLivingNil returns nil for a NON-LIVING entity type (a "misc" category
// type like an item/arrow/boat has no attributes in vanilla — DefaultAttributes has no supplier and
// the living-fallback must NOT fire for it). The caller's nil-check degrades to registration
// defaults / no-attributes, exactly as vanilla's "no DefaultAttributes registered" outcome.
func TestNewMapForEntity_NonLivingNil(t *testing.T) {
	// "item" and "arrow" are MobCategory "misc" (non-living) in data/entity — they must stay nil.
	for _, name := range []string{"item", "arrow", "boat", "acacia_boat", "area_effect_cloud"} {
		if m := NewMapForEntity(name); m != nil {
			t.Errorf("NewMapForEntity(%q) = %v, want nil (non-living gets no attribute map)", name, m)
		}
	}
}

// TestSupplierCoverage pins the SUB-ATTRIB coverage fix: NewMapForEntity returns a REAL (non-nil)
// attribute map for the newly-ported per-type suppliers (pig/cow/sheep/chicken/skeleton/creeper/
// spider), each with that type's exact jar createAttributes() overrides. Values are bit-exact
// (Float64bits) so the float-widened double literals (e.g. cow 0.20000000298023224) are caught.
func TestSupplierCoverage(t *testing.T) {
	cases := []struct {
		entity string
		attr   *Attribute
		want   float64
	}{
		// Pig.createAttributes : Animal + MAX_HEALTH 10.0, MOVEMENT_SPEED 0.25 (jar-verified).
		{"pig", MaxHealth, 10.0},
		{"pig", MovementSpeed, 0.25},
		{"pig", TemptRange, 10.0}, // Animal base
		{"pig", FollowRange, 16.0},
		// AbstractCow.createAttributes : Animal + MAX_HEALTH 10.0, MOVEMENT_SPEED 0.20000000298023224.
		{"cow", MaxHealth, 10.0},
		{"cow", MovementSpeed, 0.20000000298023224},
		// Sheep.createAttributes : Animal + MAX_HEALTH 8.0, MOVEMENT_SPEED 0.23000000417232513.
		{"sheep", MaxHealth, 8.0},
		{"sheep", MovementSpeed, 0.23000000417232513},
		// Chicken.createAttributes : Animal + MAX_HEALTH 4.0, MOVEMENT_SPEED 0.25.
		{"chicken", MaxHealth, 4.0},
		{"chicken", MovementSpeed, 0.25},
		// AbstractSkeleton.createAttributes : Monster + MOVEMENT_SPEED 0.25 (MAX_HEALTH living default 20).
		{"skeleton", MovementSpeed, 0.25},
		{"skeleton", MaxHealth, 20.0},
		{"skeleton", AttackDamage, 2.0}, // Monster registration default
		// Creeper.createAttributes : Monster + MOVEMENT_SPEED 0.25.
		{"creeper", MovementSpeed, 0.25},
		{"creeper", MaxHealth, 20.0},
		// Spider.createAttributes : Monster + MAX_HEALTH 16.0, MOVEMENT_SPEED 0.30000001192092896.
		{"spider", MaxHealth, 16.0},
		{"spider", MovementSpeed, 0.30000001192092896},
		// SkeletonHorse.createAttributes : baseHorse + MAX_HEALTH 15.0, MOVEMENT_SPEED 0.20000000298023224.
		{"skeleton_horse", MaxHealth, 15.0},
		{"skeleton_horse", MovementSpeed, 0.20000000298023224},
		// ZombieHorse.createAttributes : baseHorse + MAX_HEALTH 25.0 (MOVEMENT_SPEED stays horse-base 0.225).
		{"zombie_horse", MaxHealth, 25.0},
		{"zombie_horse", MovementSpeed, 0.22499999403953552},
		// AbstractNautilus.createAttributes : Animal + MAX_HEALTH 15, MOVEMENT_SPEED 1.0, ATTACK_DAMAGE 3.0,
		// KNOCKBACK_RESISTANCE 0.30000001192092896 (Nautilus uses it unchanged).
		{"nautilus", MaxHealth, 15.0},
		{"nautilus", MovementSpeed, 1.0},
		{"nautilus", AttackDamage, 3.0},
		{"nautilus", KnockbackResistance, 0.30000001192092896},
		// ZombieNautilus.createAttributes : AbstractNautilus + MOVEMENT_SPEED override 1.100000023841858.
		{"zombie_nautilus", MaxHealth, 15.0},
		{"zombie_nautilus", MovementSpeed, 1.100000023841858},
		{"zombie_nautilus", AttackDamage, 3.0},
	}
	for _, c := range cases {
		m := NewMapForEntity(c.entity)
		if m == nil {
			t.Fatalf("%s: NewMapForEntity returned nil (supplier not registered)", c.entity)
		}
		got := m.GetValue(c.attr.Name())
		if got != c.want || math.Float64bits(got) != math.Float64bits(c.want) {
			t.Errorf("%s.%s = %v (bits %#x), want %v (bits %#x)",
				c.entity, c.attr.Name(), got, math.Float64bits(got), c.want, math.Float64bits(c.want))
		}
	}
	// The pig MUST NOT degrade to the createLivingAttributes default 0.7 movement_speed — the whole
	// point of the coverage fix (operator: "todos deberían estar disponibles").
	if got := NewMapForEntity("pig").GetValue(MovementSpeed.Name()); got == 0.7 {
		t.Errorf("pig movement_speed = 0.7 (the living default) — coverage fix did not take effect")
	}
}

// TestLivingFallback pins the operator-mandated guarantee: a LIVING entity type with NO dedicated
// supplier (e.g. "fox", a creature) returns the createLivingAttributes() base set (non-nil), NOT
// nil — every living type gets at least the base living attributes.
func TestLivingFallback(t *testing.T) {
	// The living fallback = createLivingAttributes(): MAX_HEALTH 20, MOVEMENT_SPEED 0.7 (the living
	// registration defaults), and NO attack_damage (createLivingAttributes is below Monster). This test
	// pins that base set DIRECTLY via the fallback builder rather than via an entity name -- earlier the
	// pin used an unported living type (wolf/fox/bee/.../polar_bear), but by now the entire living-mob
	// roster has a dedicated supplier (full parity), so no name reaches the bare fallback anymore. The
	// contract being guarded is unchanged: createLivingAttributes yields the 20/0.7/no-attack base.
	m := NewMap(createLivingAttributes().Build())
	if m == nil {
		t.Fatalf("NewMap(createLivingAttributes().Build()) = nil, want the living fallback base set")
	}
	if got := m.GetValue(MaxHealth.Name()); got != 20.0 {
		t.Errorf("living fallback max_health = %v, want 20.0", got)
	}
	if got := m.GetValue(MovementSpeed.Name()); got != 0.7 {
		t.Errorf("living fallback movement_speed = %v, want 0.7 (living default)", got)
	}
	if m.HasAttribute(AttackDamage.Name()) {
		t.Errorf("living fallback unexpectedly has attack_damage")
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
