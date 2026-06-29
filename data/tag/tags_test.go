package tag

import (
	"testing"

	"github.com/imhinotori/sulfur/data/registryid"
)

// itemID resolves an item resource id ("minecraft:carrot") to its protocol id via the
// generated registryid.Item slot table (index == protocol id), failing the test if absent.
func itemID(t *testing.T, name string) int32 {
	t.Helper()
	for i, n := range registryid.Item {
		if n == name {
			return int32(i)
		}
	}
	t.Fatalf("item %q absent from registryid.Item", name)
	return 0
}

// Golden subsets read directly from temp/cache/26.2-inner.jar this session:
//
//	data/minecraft/tags/damage_type/bypasses_armor.json     (contains "minecraft:fall","minecraft:magic","minecraft:drown"; NOT "minecraft:player_attack")
//	data/minecraft/tags/damage_type/panic_causes.json       (contains nested "#minecraft:is_player_attack")
//	data/minecraft/tags/damage_type/is_player_attack.json   ("minecraft:player_attack","minecraft:spear","minecraft:mace_smash")
//	data/minecraft/tags/item/pig_food.json                  ("minecraft:carrot","minecraft:potato","minecraft:beetroot")
//
// These assertions are membership facts against the jar JSON (TagLoader.build semantics),
// keyed by the generated damage-type / item ids (resolved through DamageTypeIDs / data/registryid).

// id resolves a damage-type element name to its generated id, failing the test if absent.
func id(t *testing.T, name string) int32 {
	t.Helper()
	v, ok := DamageTypeIDs[name]
	if !ok {
		t.Fatalf("damage type %q absent from DamageTypeIDs (generation incomplete?)", name)
	}
	return v
}

// TestTagMembership: bypasses_armor contains the FALL/MAGIC/DROWN-class ids the jar JSON lists,
// and does NOT contain the player_attack id. (positive + negative — not a tautological len>0).
func TestTagMembership(t *testing.T) {
	set, ok := DamageTypeTags["bypasses_armor"]
	if !ok || len(set) == 0 {
		t.Fatalf("DamageTypeTags[\"bypasses_armor\"] missing or empty")
	}
	// POSITIVE: fall, magic, drown are listed in bypasses_armor.json.
	for _, name := range []string{"fall", "magic", "drown"} {
		if !set[id(t, name)] {
			t.Errorf("bypasses_armor should contain %q (jar bypasses_armor.json lists it)", name)
		}
	}
	// NEGATIVE: player_attack is NOT in bypasses_armor.json.
	if set[id(t, "player_attack")] {
		t.Errorf("bypasses_armor must NOT contain player_attack (absent from bypasses_armor.json)")
	}
}

// TestNestedTagFlatten: panic_causes is a SUPERSET of is_player_attack — proving the nested
// "#minecraft:is_player_attack" ref was recursively flattened (TagLoader.build), not stored
// as a literal "#..." string.
func TestNestedTagFlatten(t *testing.T) {
	panicCauses, ok := DamageTypeTags["panic_causes"]
	if !ok || len(panicCauses) == 0 {
		t.Fatalf("DamageTypeTags[\"panic_causes\"] missing or empty")
	}
	playerAttack, ok := DamageTypeTags["is_player_attack"]
	if !ok || len(playerAttack) == 0 {
		t.Fatalf("DamageTypeTags[\"is_player_attack\"] missing or empty")
	}
	// Every member of is_player_attack must appear in panic_causes (the nested ref was flattened).
	for memberID := range playerAttack {
		if !panicCauses[memberID] {
			t.Errorf("panic_causes is not a superset of is_player_attack: missing id %d (nested #-ref not flattened)", memberID)
		}
	}
	// Concretely: player_attack (a member of the nested tag) must be in panic_causes.
	if !panicCauses[id(t, "player_attack")] {
		t.Errorf("panic_causes must contain player_attack via flattened #minecraft:is_player_attack")
	}
	// And a direct (non-nested) member must also be present.
	if !panicCauses[id(t, "mob_attack")] {
		t.Errorf("panic_causes must contain mob_attack (direct member in panic_causes.json)")
	}
}

// TestItemTagsGenerated (front-load): ItemTags["pig_food"] is non-empty and contains the
// carrot/potato/beetroot ids — proving the item-tag family generated into the same package.
// (Consumer lands Phase 32; data is real + tested headless here.)
func TestItemTagsGenerated(t *testing.T) {
	set, ok := ItemTags["pig_food"]
	if !ok || len(set) == 0 {
		t.Fatalf("ItemTags[\"pig_food\"] missing or empty")
	}
	// Each pig_food member must resolve to a distinct id present in the set.
	for _, itemID := range []int32{itemID(t, "minecraft:carrot"), itemID(t, "minecraft:potato"), itemID(t, "minecraft:beetroot")} {
		if !set[itemID] {
			t.Errorf("pig_food should contain item id %d (jar pig_food.json lists carrot/potato/beetroot)", itemID)
		}
	}
}
