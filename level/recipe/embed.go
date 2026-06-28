package recipe

// embed.go — the level/loot/embed.go twin. It //go:embeds the WHOLE jar recipe/
// JSON tree (1585 files, small JSON) plus the item-tag JSON tree (for #tag
// ingredient resolution), exactly as level/loot embeds the whole loot_table/
// tree rather than a curated subset (per 25-RESEARCH "embed the whole recipe/
// tree once — it's small JSON"). The trees were extracted from the jar's shipped
// datagen output (temp/cache/26.2-datagen) + the vendored item tags
// (server/registrydata/tags/item) into level/recipe/data.

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"embed"
)

// recipeFS embeds the full vanilla recipe JSON tree. itemTagFS embeds the item
// tag tree used to resolve a #minecraft:<tag> ingredient to its member-id set.
//
//go:embed data/recipe
//go:embed data/item_tags
var recipeFS embed.FS

// resolveID strips a "minecraft:" namespace from a registry id, leaving the path
// component. A bare id with no namespace is taken as-is. (Mirrors
// level/loot/embed.go resolveID.)
func resolveID(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 {
		return id[i+1:]
	}
	return id
}

// TableJSON returns the embedded recipe JSON bytes for a registry id, e.g.
// "minecraft:stick" -> data/recipe/stick.json. A missing id returns a clear
// error. (Mirrors level/loot/embed.go TableJSON.)
func TableJSON(id string) ([]byte, error) {
	rel := resolveID(id)
	if rel == "" {
		return nil, fmt.Errorf("recipe: empty recipe id")
	}
	p := path.Join("data", "recipe", rel+".json")
	b, err := recipeFS.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("recipe: recipe %q not found (-> %s): %w", id, p, err)
	}
	return b, nil
}

// LoadAll walks the embedded recipe tree (io/fs.WalkDir over every *.json under
// data/recipe), parses each file into a Recipe, and returns them sorted by id
// for determinism. Any decode/parse error (unknown type, unknown item name)
// aborts the whole load LOUDLY — never a silent drop (T-25-02 Tampering).
func LoadAll() ([]Recipe, error) {
	res, err := newResolver()
	if err != nil {
		return nil, err
	}
	var out []Recipe
	walkErr := fs.WalkDir(recipeFS, "data/recipe", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		b, readErr := recipeFS.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("recipe: read %s: %w", p, readErr)
		}
		id := strings.TrimSuffix(strings.TrimPrefix(p, "data/recipe/"), ".json")
		r, parseErr := res.parseRecipe(id, b)
		if parseErr != nil {
			return fmt.Errorf("recipe: parse %s: %w", p, parseErr)
		}
		out = append(out, r)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	// Sort by id for deterministic ordering (insertion sort is fine; ~1585 ids
	// loaded once at boot).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].ID > out[j].ID; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, nil
}

// ParseAll is the public entry point: it loads + parses the whole embedded
// recipe tree.
func ParseAll() ([]Recipe, error) { return LoadAll() }
