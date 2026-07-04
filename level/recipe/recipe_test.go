package recipe

import (
	"testing"

	"github.com/imhinotori/sulfur/data/registryid"
)

// idOf resolves a namespaced item name to its protocol id via the same
// registryid.Item index the parser uses (test oracle).
func idOf(t *testing.T, name string) int {
	t.Helper()
	for id, n := range registryid.Item {
		if n == name {
			return id
		}
	}
	t.Fatalf("item %q not in registryid.Item", name)
	return -1
}

// TestRecipeParse: ParseAll parses the WHOLE embedded tree without error and
// covers every type tag, with counts >= the datagen census (a >= tolerance so a
// Mojang data bump doesn't break it).
func TestRecipeParse(t *testing.T) {
	all, err := ParseAll()
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("ParseAll returned no recipes")
	}
	counts := map[Type]int{}
	cooking := map[string]int{}
	for _, r := range all {
		counts[r.Type]++
		if r.Type == TypeCooking {
			cooking[r.Cooking.Subtype]++
		}
	}
	// Census from temp/cache/26.2-datagen (>= tolerance).
	checks := []struct {
		name string
		got  int
		want int
	}{
		{"shaped", counts[TypeShaped], 733},
		{"shapeless", counts[TypeShapeless], 323},
		{"cooking", counts[TypeCooking], 73 + 25 + 9 + 9},
		{"stonecutting", counts[TypeStonecutting], 319},
		{"smelting", cooking["smelting"], 73},
		{"blasting", cooking["blasting"], 25},
		{"smoking", cooking["smoking"], 9},
		{"campfire_cooking", cooking["campfire_cooking"], 9},
	}
	for _, c := range checks {
		if c.got < c.want {
			t.Errorf("%s count = %d, want >= %d", c.name, c.got, c.want)
		}
	}
	if counts[TypeSpecial] == 0 {
		t.Error("expected at least one special-marker recipe (crafting_special_*, smithing_*, ...)")
	}
	// smithing_transform: the netherite upgrade set (sword/pickaxe/axe/shovel/hoe + the armor pieces) —
	// at least the 5 tools + boots/chestplate/helmet/leggings + horse armor. Assert a conservative floor.
	if counts[TypeSmithingTransform] < 5 {
		t.Errorf("smithing_transform count = %d, want >= 5 (the netherite upgrade set)", counts[TypeSmithingTransform])
	}
}

// TestSmithingTransformNetherite: the netherite_sword_smithing recipe parses to a SmithingTransform whose
// match accepts (netherite_upgrade_template, diamond_sword, netherite_ingot) and rejects a wrong addition
// or a missing template. VERIFIED against SmithingRecipe.matches over SmithingRecipeInput.
func TestSmithingTransformNetherite(t *testing.T) {
	all, err := ParseAll()
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	var sword *SmithingTransform
	for i := range all {
		if all[i].ID == "netherite_sword_smithing" && all[i].SmithingTransform != nil {
			sword = all[i].SmithingTransform
			break
		}
	}
	if sword == nil {
		t.Fatal("netherite_sword_smithing not parsed as a SmithingTransform")
	}
	const (
		diamondSword       = 964
		netheriteIngot     = 937
		upgradeTemplate    = 1458
		book               = 1058
	)
	tmpl := Stack{ID: upgradeTemplate, Count: 1}
	base := Stack{ID: diamondSword, Count: 1}
	add := Stack{ID: netheriteIngot, Count: 1}
	if !MatchSmithingTransform(sword, tmpl, base, add) {
		t.Fatal("MatchSmithingTransform(template, diamond_sword, netherite_ingot) = false, want true")
	}
	if MatchSmithingTransform(sword, tmpl, base, Stack{ID: book, Count: 1}) {
		t.Fatal("MatchSmithingTransform with a book addition = true, want false")
	}
	if MatchSmithingTransform(sword, Stack{}, base, add) {
		t.Fatal("MatchSmithingTransform with no template = true, want false (template is a present optional)")
	}
	if sword.Result.ID != 969 { // netherite_sword
		t.Fatalf("result id = %d, want netherite_sword (969)", sword.Result.ID)
	}
}

// TestParseShapedStick: the stick recipe parses to a Shaped with pattern ["#","#"]
// -> a 1x2 grid, key "#" -> the #minecraft:planks member set (contains oak_planks),
// result {stick, 4}, symmetrical true (a single column is its own mirror).
func TestParseShapedStick(t *testing.T) {
	res, err := newResolver()
	if err != nil {
		t.Fatalf("newResolver: %v", err)
	}
	b, err := TableJSON("minecraft:stick")
	if err != nil {
		t.Fatalf("TableJSON stick: %v", err)
	}
	r, err := res.parseRecipe("stick", b)
	if err != nil {
		t.Fatalf("parseRecipe stick: %v", err)
	}
	if r.Type != TypeShaped || r.Shaped == nil {
		t.Fatalf("stick: type=%v shaped=%v, want shaped", r.Type, r.Shaped)
	}
	s := r.Shaped
	if s.Width != 1 || s.Height != 2 {
		t.Errorf("stick dims = %dx%d, want 1x2", s.Width, s.Height)
	}
	if s.IngredientCount != 2 {
		t.Errorf("stick ingredientCount = %d, want 2", s.IngredientCount)
	}
	if !s.Symmetrical {
		t.Error("stick should be symmetrical (single column)")
	}
	oak := idOf(t, "minecraft:oak_planks")
	if !s.Ingredients[0].Test(oak) {
		t.Errorf("stick cell 0 should contain oak_planks (id %d); ids=%v", oak, s.Ingredients[0].IDs())
	}
	if s.Result.ID != idOf(t, "minecraft:stick") || s.Result.Count != 4 {
		t.Errorf("stick result = %+v, want {stick,4}", s.Result)
	}
}

// TestParseShapelessTag: oak_planks (shapeless, #minecraft:oak_logs) resolves the
// ingredient to a member set containing oak_log.
func TestParseShapelessTag(t *testing.T) {
	res, err := newResolver()
	if err != nil {
		t.Fatalf("newResolver: %v", err)
	}
	b, err := TableJSON("minecraft:oak_planks")
	if err != nil {
		t.Fatalf("TableJSON oak_planks: %v", err)
	}
	r, err := res.parseRecipe("oak_planks", b)
	if err != nil {
		t.Fatalf("parseRecipe oak_planks: %v", err)
	}
	if r.Type != TypeShapeless || r.Shapeless == nil {
		t.Fatalf("oak_planks: type=%v, want shapeless", r.Type)
	}
	if len(r.Shapeless.Ingredients) != 1 {
		t.Fatalf("oak_planks ingredients = %d, want 1", len(r.Shapeless.Ingredients))
	}
	oakLog := idOf(t, "minecraft:oak_log")
	if !r.Shapeless.Ingredients[0].Test(oakLog) {
		t.Errorf("oak_planks ingredient should contain oak_log (id %d); ids=%v",
			oakLog, r.Shapeless.Ingredients[0].IDs())
	}
	if r.Shapeless.Result.ID != idOf(t, "minecraft:oak_planks") || r.Shapeless.Result.Count != 4 {
		t.Errorf("oak_planks result = %+v, want {oak_planks,4}", r.Shapeless.Result)
	}
}

// TestNameToID: the name->id map resolves stick and stone; an unknown name errors
// loudly (never a silent 0).
func TestNameToID(t *testing.T) {
	res, err := newResolver()
	if err != nil {
		t.Fatalf("newResolver: %v", err)
	}
	for _, name := range []string{"minecraft:stick", "minecraft:stone"} {
		id, err := res.nameID(name)
		if err != nil {
			t.Errorf("nameID(%q): %v", name, err)
		}
		if id != idOf(t, name) {
			t.Errorf("nameID(%q) = %d, want %d", name, id, idOf(t, name))
		}
	}
	if _, err := res.nameID("minecraft:definitely_not_an_item_xyz"); err == nil {
		t.Error("nameID of an unknown item should error loudly, got nil")
	}
}

// TestUnknownTypeLoud: a synthetic recipe with an unknown "type" errors at parse.
func TestUnknownTypeLoud(t *testing.T) {
	res, err := newResolver()
	if err != nil {
		t.Fatalf("newResolver: %v", err)
	}
	bad := []byte(`{"type":"minecraft:not_a_real_recipe_type","result":{"id":"minecraft:stick"}}`)
	if _, err := res.parseRecipe("synthetic", bad); err == nil {
		t.Error("parseRecipe of an unknown type should error loudly, got nil")
	}
}
