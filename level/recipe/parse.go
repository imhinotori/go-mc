package recipe

// parse.go — the JSON -> model decoder + the name->id / #tag resolver.
//
// name->id: registryid.Item is a []string indexed by protocol item id
// (registryid.Item[id] == "minecraft:<name>"); there is NO ByName helper, so we
// build a map[string]int ONCE. An unknown item name is a LOUD fmt.Errorf
// (T-25-02 Tampering: a corrupt recipe never silently resolves to air/0).
//
// #tag resolution: an ingredient string "#minecraft:planks" expands to the
// member-id SET of that item tag (from the embedded data/item_tags tree), with
// tag-of-tag references ("#minecraft:other") expanded recursively. A bare item
// id is a one-element set; a list of ingredients is the union.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/imhinotori/sulfur/data/registryid"
)

// resolver holds the parse-time lookup tables: name->id (from registryid.Item)
// and the parsed item-tag tree (for #tag expansion).
type resolver struct {
	nameToID map[string]int
	tags     map[string]tagFile // tag name (sans "minecraft:") -> values
}

// tagFile mirrors the on-disk item-tag JSON shape: a list of member values, each
// a plain item id ("minecraft:oak_planks") or a tag reference
// ("#minecraft:other_tag").
type tagFile struct {
	Values []string `json:"values"`
}

// newResolver builds the name->id map from registryid.Item and parses every
// embedded item-tag file once.
func newResolver() (*resolver, error) {
	n2i := make(map[string]int, len(registryid.Item))
	for id, name := range registryid.Item {
		n2i[name] = id // registryid.Item already namespaced ("minecraft:stone")
	}

	tags := make(map[string]tagFile)
	walkErr := fs.WalkDir(recipeFS, "data/item_tags", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		b, readErr := recipeFS.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("recipe: read tag %s: %w", p, readErr)
		}
		var tf tagFile
		if err := json.Unmarshal(b, &tf); err != nil {
			return fmt.Errorf("recipe: parse tag %s: %w", p, err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(p, "data/item_tags/"), ".json")
		tags[name] = tf
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return &resolver{nameToID: n2i, tags: tags}, nil
}

// nameID resolves a namespaced item name to its protocol id, erroring loudly on
// an unknown name (T-25-02).
func (r *resolver) nameID(name string) (int, error) {
	// Accept both "minecraft:x" and a bare "x" (default to the minecraft ns).
	key := name
	if !strings.ContainsRune(key, ':') {
		key = "minecraft:" + key
	}
	id, ok := r.nameToID[key]
	if !ok {
		return 0, fmt.Errorf("unknown item name %q", name)
	}
	return id, nil
}

// expandTag resolves "#minecraft:planks" (already stripped to "planks") to the
// sorted set of member item ids, expanding tag-of-tag references recursively.
// seen guards against cyclic references.
func (r *resolver) expandTag(name string, into map[int]struct{}, seen map[string]bool) error {
	if seen[name] {
		return nil
	}
	seen[name] = true
	tf, ok := r.tags[name]
	if !ok {
		return fmt.Errorf("unknown item tag #minecraft:%s", name)
	}
	for _, v := range tf.Values {
		if strings.HasPrefix(v, "#") {
			ref := strings.TrimPrefix(strings.TrimPrefix(v, "#"), "minecraft:")
			if err := r.expandTag(ref, into, seen); err != nil {
				return err
			}
			continue
		}
		id, err := r.nameID(v)
		if err != nil {
			return fmt.Errorf("item tag #minecraft:%s: %w", name, err)
		}
		into[id] = struct{}{}
	}
	return nil
}

// resolveIngredientTerm resolves ONE ingredient term — a bare item id or a
// "#tag" — into the id set `into`.
func (r *resolver) resolveIngredientTerm(term string, into map[int]struct{}) error {
	if strings.HasPrefix(term, "#") {
		ref := strings.TrimPrefix(strings.TrimPrefix(term, "#"), "minecraft:")
		return r.expandTag(ref, into, map[string]bool{})
	}
	id, err := r.nameID(term)
	if err != nil {
		return err
	}
	into[id] = struct{}{}
	return nil
}

// parseIngredient decodes an ingredient value, which may be a single string
// (item or #tag) OR a JSON list of strings (the any-of union). The result is the
// member-id set.
func (r *resolver) parseIngredient(raw json.RawMessage) (Ingredient, error) {
	into := map[int]struct{}{}
	// Try a single string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if err := r.resolveIngredientTerm(s, into); err != nil {
			return Ingredient{}, err
		}
		return ingredientFromSet(into), nil
	}
	// Else a list of strings (any-of).
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return Ingredient{}, fmt.Errorf("ingredient: expected string or list, got %s", string(raw))
	}
	for _, term := range list {
		if err := r.resolveIngredientTerm(term, into); err != nil {
			return Ingredient{}, err
		}
	}
	return ingredientFromSet(into), nil
}

func ingredientFromSet(set map[int]struct{}) Ingredient {
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return newIngredient(ids)
}

// rawResult mirrors the on-disk result shape: { "id": "minecraft:x", "count": N }.
type rawResult struct {
	ID    string `json:"id"`
	Count *int   `json:"count"`
}

func (r *resolver) parseResult(raw rawResult) (Stack, error) {
	id, err := r.nameID(raw.ID)
	if err != nil {
		return Stack{}, fmt.Errorf("result: %w", err)
	}
	count := 1
	if raw.Count != nil {
		count = *raw.Count
	}
	return Stack{ID: id, Count: count}, nil
}

// rawRecipe is the shared on-disk shape; type-specific fields are RawMessage so
// only the relevant decoder pulls them.
type rawRecipe struct {
	Type        string                     `json:"type"`
	Key         map[string]json.RawMessage `json:"key"`
	Pattern     []string                   `json:"pattern"`
	Ingredients []json.RawMessage          `json:"ingredients"`
	Ingredient  json.RawMessage            `json:"ingredient"`
	Result      *rawResult                 `json:"result"`
}

// parseRecipe decodes one recipe file into a Recipe, dispatching on the "type"
// string. An unknown type errors LOUDLY (T-25-02 / TestUnknownTypeLoud).
//
// The "type" is decoded FIRST (a lightweight pass) so the full type-specific
// shape (pattern []string, ingredient, ...) is only unmarshaled for the
// MATCHABLE types — the SPECIAL types (smithing_trim's "pattern" is a STRING,
// not a list; crafting_transmute carries an "input"/"material") have
// type-incompatible field shapes that would fail a single rigid rawRecipe
// decode.
func (r *resolver) parseRecipe(id string, data []byte) (Recipe, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Recipe{}, fmt.Errorf("decode type: %w", err)
	}
	t := resolveID(head.Type) // strip "minecraft:"

	// SPECIAL markers: recorded, never decoded into the rigid rawRecipe shape.
	switch t {
	case "crafting_special_armordye", "crafting_special_bannerduplicate",
		"crafting_special_bookcloning", "crafting_special_firework_rocket",
		"crafting_special_firework_star", "crafting_special_firework_star_fade",
		"crafting_special_mapcloning", "crafting_special_mapextending",
		"crafting_special_repairitem", "crafting_special_shielddecoration",
		"crafting_special_shulkerboxcoloring", "crafting_special_suspiciousstew",
		"crafting_special_tippedarrow", "crafting_decorated_pot", "crafting_dye",
		"crafting_imbue", "crafting_transmute", "smithing_transform", "smithing_trim":
		// Special types have no plain pattern/ingredient shape — recorded as a
		// marker (not matchable). The matcher (match.go) only matches
		// shaped/shapeless/cooking/stonecutting.
		return Recipe{ID: id, Type: TypeSpecial, SpecialType: t}, nil
	}

	var raw rawRecipe
	if err := json.Unmarshal(data, &raw); err != nil {
		return Recipe{}, fmt.Errorf("decode: %w", err)
	}
	switch t {
	case "crafting_shaped":
		s, err := r.parseShaped(raw)
		if err != nil {
			return Recipe{}, err
		}
		return Recipe{ID: id, Type: TypeShaped, Shaped: s}, nil
	case "crafting_shapeless":
		s, err := r.parseShapeless(raw)
		if err != nil {
			return Recipe{}, err
		}
		return Recipe{ID: id, Type: TypeShapeless, Shapeless: s}, nil
	case "smelting", "blasting", "smoking", "campfire_cooking":
		c, err := r.parseCooking(raw, t)
		if err != nil {
			return Recipe{}, err
		}
		return Recipe{ID: id, Type: TypeCooking, Cooking: c}, nil
	case "stonecutting":
		s, err := r.parseStonecutting(raw)
		if err != nil {
			return Recipe{}, err
		}
		return Recipe{ID: id, Type: TypeStonecutting, Stonecutting: s}, nil
	default:
		return Recipe{}, fmt.Errorf("unknown recipe type %q", head.Type)
	}
}

// parseShaped builds the trimmed pattern, resolves each key char to an
// Ingredient, computes Symmetrical (the pattern equals its horizontal mirror —
// Util.isSymmetrical), and the ingredient count.
//
// 1:1 net.minecraft.world.item.crafting.ShapedRecipePattern (Data.unpack +
// Util.isSymmetrical): the on-disk pattern is already the trimmed grid the jar
// stores (vanilla's shrink() trims leading/trailing empty space at datagen
// time, so the embedded pattern dims ARE the recipe's width/height).
func (r *resolver) parseShaped(raw rawRecipe) (*Shaped, error) {
	if len(raw.Pattern) == 0 {
		return nil, fmt.Errorf("shaped: empty pattern")
	}
	if raw.Result == nil {
		return nil, fmt.Errorf("shaped: missing result")
	}
	height := len(raw.Pattern)
	width := len(raw.Pattern[0])
	for _, row := range raw.Pattern {
		if len(row) != width {
			return nil, fmt.Errorf("shaped: ragged pattern row %q (width %d)", row, width)
		}
	}
	// Resolve the key map: char -> Ingredient.
	keyIng := make(map[rune]Ingredient, len(raw.Key))
	for k, v := range raw.Key {
		if len(k) != 1 {
			return nil, fmt.Errorf("shaped: key %q is not a single char", k)
		}
		ing, err := r.parseIngredient(v)
		if err != nil {
			return nil, fmt.Errorf("shaped: key %q: %w", k, err)
		}
		keyIng[rune(k[0])] = ing
	}
	// Build the row-major ingredient grid. ' ' (space) -> empty optional cell.
	ings := make([]Ingredient, 0, width*height)
	count := 0
	for _, row := range raw.Pattern {
		for _, ch := range row {
			if ch == ' ' {
				ings = append(ings, Ingredient{}) // empty optional
				continue
			}
			ing, ok := keyIng[ch]
			if !ok {
				return nil, fmt.Errorf("shaped: pattern char %q not in key", string(ch))
			}
			ings = append(ings, ing)
			count++
		}
	}
	result, err := r.parseResult(*raw.Result)
	if err != nil {
		return nil, fmt.Errorf("shaped: %w", err)
	}
	return &Shaped{
		Width:           width,
		Height:          height,
		Ingredients:     ings,
		Symmetrical:     isSymmetrical(width, height, ings),
		IngredientCount: count,
		Result:          result,
	}, nil
}

// isSymmetrical reports whether the pattern equals its horizontal mirror — a 1:1
// port of net.minecraft.util.Util.isSymmetrical (compare each cell (col,row)
// against (width-1-col, row); an empty cell equals an empty cell, else compare
// the ingredient member sets).
//
// 1:1 net.minecraft.util.Util.isSymmetrical
func isSymmetrical(width, height int, ings []Ingredient) bool {
	for row := 0; row < height; row++ {
		for col := 0; col < width/2; col++ {
			a := ings[col+row*width]
			b := ings[(width-1-col)+row*width]
			if !sameIngredient(a, b) {
				return false
			}
		}
	}
	return true
}

// sameIngredient reports set equality of two ingredients (used by
// isSymmetrical). Empty == empty.
func sameIngredient(a, b Ingredient) bool {
	if a.Empty() != b.Empty() {
		return false
	}
	if len(a.ids) != len(b.ids) {
		return false
	}
	for id := range a.ids {
		if _, ok := b.ids[id]; !ok {
			return false
		}
	}
	return true
}

func (r *resolver) parseShapeless(raw rawRecipe) (*Shapeless, error) {
	if len(raw.Ingredients) == 0 {
		return nil, fmt.Errorf("shapeless: empty ingredients")
	}
	if raw.Result == nil {
		return nil, fmt.Errorf("shapeless: missing result")
	}
	ings := make([]Ingredient, 0, len(raw.Ingredients))
	for i, ri := range raw.Ingredients {
		ing, err := r.parseIngredient(ri)
		if err != nil {
			return nil, fmt.Errorf("shapeless: ingredient %d: %w", i, err)
		}
		ings = append(ings, ing)
	}
	result, err := r.parseResult(*raw.Result)
	if err != nil {
		return nil, fmt.Errorf("shapeless: %w", err)
	}
	return &Shapeless{Ingredients: ings, Result: result}, nil
}

func (r *resolver) parseCooking(raw rawRecipe, subtype string) (*Cooking, error) {
	if len(raw.Ingredient) == 0 {
		return nil, fmt.Errorf("cooking: missing ingredient")
	}
	if raw.Result == nil {
		return nil, fmt.Errorf("cooking: missing result")
	}
	ing, err := r.parseIngredient(raw.Ingredient)
	if err != nil {
		return nil, fmt.Errorf("cooking: %w", err)
	}
	result, err := r.parseResult(*raw.Result)
	if err != nil {
		return nil, fmt.Errorf("cooking: %w", err)
	}
	return &Cooking{Ingredient: ing, Result: result, Subtype: subtype}, nil
}

func (r *resolver) parseStonecutting(raw rawRecipe) (*Stonecutting, error) {
	if len(raw.Ingredient) == 0 {
		return nil, fmt.Errorf("stonecutting: missing ingredient")
	}
	if raw.Result == nil {
		return nil, fmt.Errorf("stonecutting: missing result")
	}
	ing, err := r.parseIngredient(raw.Ingredient)
	if err != nil {
		return nil, fmt.Errorf("stonecutting: %w", err)
	}
	result, err := r.parseResult(*raw.Result)
	if err != nil {
		return nil, fmt.Errorf("stonecutting: %w", err)
	}
	return &Stonecutting{Ingredient: ing, Result: result}, nil
}

