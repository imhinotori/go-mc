// Package recipe is the shared, PURE recipe engine — the load-bearing 1:1 half
// of the Phase-25 crafting dogfood. It //go:embeds the vanilla Minecraft 26.2
// (protocol 776) recipe JSON tree (the level/loot/embed.go twin) and parses ALL
// vanilla recipe types into a Go model, then ports the recipe MATCH
// method-for-method from the unobfuscated server jar
// (temp/cache/26.2-inner.jar, read via `javap -c -p`).
//
// 1:1 VANILLA PORT (CLAUDE.md, ABSOLUTE): the match (match.go) is a literal,
// method-for-method copy of the decompiled bytecode — the bounding-box SHRINK
// (CraftingInput.ofPositioned), the MIRROR-then-unmirror try order
// (ShapedRecipePattern.matches), the shapeless multiset cover
// (ShapelessRecipe.matches / StackedItemContents.canCraft), and the
// single-ingredient cooking/stonecutting match (SingleItemRecipe.matches).
// Every ported method carries a jar citation.
//
// PURITY: this package has no tick state, no I/O, no network surface. It imports
// only stdlib (embed, encoding/json, io/fs, path, fmt, strings, sort) +
// data/registryid (the name->id index) + data/item (stack-size). NO
// server/level/world imports. CGO_ENABLED=0 stays clean; the recipe + item-tag
// JSON is //go:embed'd.
package recipe

// Type discriminates a parsed recipe. The MATCHABLE types are shaped, shapeless,
// cooking (smelting/blasting/smoking/campfire_cooking), and stonecutting; the
// SPECIAL types (crafting_special_*, crafting_dye, crafting_transmute,
// crafting_decorated_pot, crafting_imbue, smithing_*) have no plain
// pattern/ingredient shape and are recorded as markers (parsed but not matched)
// so ParseAll covers the whole tree without erroring.
type Type string

const (
	TypeShaped       Type = "shaped"
	TypeShapeless    Type = "shapeless"
	TypeCooking      Type = "cooking"
	TypeStonecutting Type = "stonecutting"
	TypeSpecial      Type = "special"
)

// Stack is a parsed result: an item id + a count. The count defaults to 1 when
// the JSON omits it (vanilla ItemStackTemplate default).
type Stack struct {
	ID    int
	Count int
}

// Ingredient is a SET of item ids — the resolution of a bare item id (a
// one-element set), a #tag (the tag's member-id set), or a list of items/tags
// (the union). Match tests SET MEMBERSHIP (Ingredient.test: the stack's item is
// IN the set). An EMPTY ingredient (nil/empty set) corresponds to an empty
// optional cell (an empty shaped key slot).
type Ingredient struct {
	ids map[int]struct{}
}

// newIngredient builds an Ingredient from a list of resolved item ids.
func newIngredient(ids []int) Ingredient {
	set := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return Ingredient{ids: set}
}

// Empty reports whether the ingredient has no members (the empty-optional cell).
func (in Ingredient) Empty() bool { return len(in.ids) == 0 }

// Test reports whether item id is a member of the ingredient set — the 1:1
// Ingredient.test (an empty ingredient matches nothing; the empty-CELL case is
// handled by testOptional in match.go).
//
// 1:1 net.minecraft.world.item.crafting.Ingredient.test
func (in Ingredient) Test(id int) bool {
	_, ok := in.ids[id]
	return ok
}

// IDs returns the sorted member ids (test/debug convenience; not on the match
// hot path).
func (in Ingredient) IDs() []int {
	out := make([]int, 0, len(in.ids))
	for id := range in.ids {
		out = append(out, id)
	}
	// sort for determinism
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Shaped is a crafting_shaped recipe. Width/Height are the PATTERN dims (the
// trimmed pattern, exactly as ShapedRecipePattern stores them). Ingredients is a
// row-major Width*Height slice of optional ingredients (an Empty() ingredient =
// an empty pattern cell). Symmetrical = the pattern equals its horizontal mirror
// (Util.isSymmetrical), computed at parse time. IngredientCount = the number of
// non-empty pattern cells.
type Shaped struct {
	Width           int
	Height          int
	Ingredients     []Ingredient // len == Width*Height, row-major
	Symmetrical     bool
	IngredientCount int
	Result          Stack
}

// Shapeless is a crafting_shapeless recipe: an unordered multiset of ingredients
// + a result.
type Shapeless struct {
	Ingredients []Ingredient
	Result      Stack
}

// Cooking is a smelting/blasting/smoking/campfire_cooking recipe — a single
// ingredient + result (+ the cook metadata, recorded for the furnace subsystem).
type Cooking struct {
	Ingredient Ingredient
	Result     Stack
	Subtype    string // "smelting" | "blasting" | "smoking" | "campfire_cooking"
}

// Stonecutting is a single-ingredient cut recipe.
type Stonecutting struct {
	Ingredient Ingredient
	Result     Stack
}

// Recipe is the discriminated union of every parsed recipe. Exactly one of the
// type-specific fields is populated per Type (Special carries only ID+Type, as a
// recorded-but-not-matchable marker).
type Recipe struct {
	ID           string // the recipe registry id (file path sans .json), e.g. "stick"
	Type         Type
	Shaped       *Shaped
	Shapeless    *Shapeless
	Cooking      *Cooking
	Stonecutting *Stonecutting
	SpecialType  string // the raw JSON "type" for a Special marker (e.g. "crafting_special_repairitem")
}
