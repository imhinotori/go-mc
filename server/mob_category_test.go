package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestCategoryOfAllPassivesAreCreature is the Phase-34 gap regression (34-VERIFICATION SC#1): every
// vanilla passive mob (pig/cow/sheep/chicken) must map to categoryCreature so countByCategory tallies
// it and the natural-spawn anti-flood cap (counts[categoryCreature]) bounds it. A passive that falls
// through to categoryMisc would NOT consume the CREATURE budget — defeating the vanilla cap for that
// mob (the bug that slipped between the wave-2 sheep/chicken plans and the 34-04 gate). Each is
// MobCategory.CREATURE in vanilla (data/entity <Mob>.Type == "creature", the jar-derived codegen source).
func TestCategoryOfAllPassivesAreCreature(t *testing.T) {
	cases := []struct {
		name string
		id   entity.ID
	}{
		{"pig", entity.Pig.ID},
		{"cow", entity.Cow.ID},
		{"sheep", entity.Sheep.ID},
		{"chicken", entity.Chicken.ID},
	}
	for _, tc := range cases {
		if got := categoryOf(tc.id); got != categoryCreature {
			t.Errorf("categoryOf(%s) = %v, want categoryCreature (vanilla EntityType is MobCategory.CREATURE; data/entity %s.Type == %q)", tc.name, got, tc.name, "creature")
		}
	}
}
