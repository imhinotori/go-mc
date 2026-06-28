package server

// recipe_embed.go — PLUGIN-05 (Plan 25-02): the BOOT-LOAD of the bundled vanilla `crafting` recipe
// plugin (the vanilla_pig_embed.go twin). A default Sulfur server must craft every vanilla recipe
// THROUGH the plugin API out-of-the-box, even with no operator plugins/ dir — so the recipe-provider
// plugin is //go:embed'd (always in the binary, an operator cannot delete it) and boot-loaded into the
// runtime *host.Manager BEFORE the first crafting click.
//
// The split (the same as the level/recipe Plan-01 seam): the recipe DATA + PARSE stays in Go
// (level/recipe.ParseAll over the embedded jar JSON — Starlark has no json/file builtin by sandbox
// design); the MATCH stays in the plugin (assets/crafting/main.star re-expresses the 1:1 jar match).
// LoadCraftingPlugin wires the two together: parse the table in Go, inject it via SetRecipeTable, then
// LoadDirWith the embedded plugin so its set_recipe_matcher captures. If the matcher did not register
// after load, it FAILS LOUDLY (mirroring vanilla_pig_embed's Pitfall-4 LOUD failure) — a silent
// no-matcher would leave a server that cannot craft.

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/imhinotori/sulfur/level/recipe"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// stonecutterOnce caches the parsed stonecutting recipe subset (PLUGIN-05 Plan 25-03). The
// StonecutterMenu's setupRecipeList (selectByInput) needs the per-input list of stonecutter results,
// which vanilla reads from RecipeAccess.stonecutterRecipes — Sulfur reads it from the SAME parsed
// recipe tree LoadCraftingPlugin uses. Parsed once (the recipe tree is immutable), tick-read.
var (
	stonecutterOnce  sync.Once
	stonecutterCache []recipe.Stonecutting
)

// stonecutterRecipes returns the parsed stonecutting recipes (cached). On a parse error it returns an
// empty list (a stonecutter with no recipes opens but offers nothing — never a panic). This is the
// Sulfur analogue of RecipeAccess.stonecutterRecipes().
func stonecutterRecipes() []recipe.Stonecutting {
	stonecutterOnce.Do(func() {
		recipes, err := recipe.ParseAll()
		if err != nil {
			return // leave the cache empty (open-but-offer-nothing, never a panic)
		}
		for i := range recipes {
			if recipes[i].Type == recipe.TypeStonecutting && recipes[i].Stonecutting != nil {
				stonecutterCache = append(stonecutterCache, *recipes[i].Stonecutting)
			}
		}
	})
	return stonecutterCache
}

// craftingFS embeds the bundled crafting plugin. The canonical operator-facing copy also ships at the
// repo-root plugins/crafting/; this embedded copy (server/assets/crafting/) is the source of truth for
// the default server (an operator with no plugins/ dir still crafts). Keep the two copies identical.
//
//go:embed assets/crafting/plugin.toml assets/crafting/main.star
var craftingFS embed.FS

// LoadCraftingPlugin parses the vanilla recipe table, injects it into mgr, materializes + loads the
// embedded crafting plugin, and FAILS LOUDLY if the matcher did not register. Called once at boot
// (before tick.Run — single-threaded, before the tick owns the Manager, TICK-05). The exported name
// lets cmd/sulfur/main.go hook the boot-load into the runtime Manager so a default server's t.plugins
// ALWAYS has the matcher (even when plugins/ is empty/absent).
func LoadCraftingPlugin(mgr *host.Manager) error {
	// (1) Parse the whole vanilla recipe tree in Go (level/recipe.ParseAll over the embedded jar JSON).
	recipes, err := recipe.ParseAll()
	if err != nil {
		return fmt.Errorf("crafting boot-load: parse recipes: %w", err)
	}

	// (2) Convert the []Recipe to the Starlark table shape the plugin matcher reads, and inject it via
	// SetRecipeTable (the Go->plugin recipe channel — recipes() returns it). MUST precede LoadDirWith.
	mgr.SetRecipeTable(recipeTable(recipes))

	// (3) Materialize the embedded plugin to a temp dir + LoadDirWith. The recipe seam builtins
	// (set_recipe_matcher / set_recipe_remaining / recipes) are already injected by LoadDirWith for
	// every plugin (manager.go predeclared set), so no `extra` is needed.
	dir, err := materializeCrafting()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := mgr.LoadDirWith(dir, nil); err != nil {
		return fmt.Errorf("crafting boot-load: %w", err)
	}

	// (4) LOUD failure if the matcher did not capture (the vanilla_pig Pitfall-4 twin): a server with
	// no recipe matcher cannot craft — a hard boot error, never a silent runtime no-op.
	if !mgr.HasRecipeMatcher() {
		return fmt.Errorf("crafting boot-load: the embedded plugin did not register a recipe matcher (the server cannot craft)")
	}
	return nil
}

// recipeTable converts the Go-parsed recipes into the Starlark table the plugin matcher consumes — a
// starlark.List of per-recipe dicts. The shape is the contract between recipe_embed.go (producer) and
// assets/crafting/main.star (consumer):
//
//	shaped:       {"type","w","h","ings":[ingSet,...],"sym","icount","rid","rcount"}
//	shapeless:    {"type","ings":[ingSet,...],"rid","rcount"}
//	cooking:      {"type","ing":ingSet,"rid","rcount"}
//	stonecutting: {"type","ing":ingSet,"rid","rcount"}
//
// An ingredient set is a Starlark dict (id->True) used as a membership set; an EMPTY dict is the
// empty-optional pattern cell. Special markers (crafting_special_*, smithing_*) are NOT matchable and
// are skipped (the plugin never sees them). The table is frozen by Starlark on first Call (load-time
// write, tick-time read — lock-free, TICK-05).
func recipeTable(recipes []recipe.Recipe) starlark.Value {
	out := make([]starlark.Value, 0, len(recipes))
	for i := range recipes {
		r := &recipes[i]
		switch r.Type {
		case recipe.TypeShaped:
			s := r.Shaped
			ings := make([]starlark.Value, len(s.Ingredients))
			for j := range s.Ingredients {
				ings[j] = ingredientSet(s.Ingredients[j])
			}
			d := starlark.NewDict(8)
			_ = d.SetKey(starlark.String("type"), starlark.String("shaped"))
			_ = d.SetKey(starlark.String("w"), starlark.MakeInt(s.Width))
			_ = d.SetKey(starlark.String("h"), starlark.MakeInt(s.Height))
			_ = d.SetKey(starlark.String("ings"), starlark.NewList(ings))
			_ = d.SetKey(starlark.String("sym"), starlark.Bool(s.Symmetrical))
			_ = d.SetKey(starlark.String("icount"), starlark.MakeInt(s.IngredientCount))
			_ = d.SetKey(starlark.String("rid"), starlark.MakeInt(s.Result.ID))
			_ = d.SetKey(starlark.String("rcount"), starlark.MakeInt(resultCount(s.Result)))
			out = append(out, d)
		case recipe.TypeShapeless:
			s := r.Shapeless
			ings := make([]starlark.Value, len(s.Ingredients))
			for j := range s.Ingredients {
				ings[j] = ingredientSet(s.Ingredients[j])
			}
			d := starlark.NewDict(5)
			_ = d.SetKey(starlark.String("type"), starlark.String("shapeless"))
			_ = d.SetKey(starlark.String("ings"), starlark.NewList(ings))
			_ = d.SetKey(starlark.String("rid"), starlark.MakeInt(s.Result.ID))
			_ = d.SetKey(starlark.String("rcount"), starlark.MakeInt(resultCount(s.Result)))
			out = append(out, d)
		case recipe.TypeCooking:
			c := r.Cooking
			d := starlark.NewDict(4)
			_ = d.SetKey(starlark.String("type"), starlark.String("cooking"))
			_ = d.SetKey(starlark.String("ing"), ingredientSet(c.Ingredient))
			_ = d.SetKey(starlark.String("rid"), starlark.MakeInt(c.Result.ID))
			_ = d.SetKey(starlark.String("rcount"), starlark.MakeInt(resultCount(c.Result)))
			out = append(out, d)
		case recipe.TypeStonecutting:
			c := r.Stonecutting
			d := starlark.NewDict(4)
			_ = d.SetKey(starlark.String("type"), starlark.String("stonecutting"))
			_ = d.SetKey(starlark.String("ing"), ingredientSet(c.Ingredient))
			_ = d.SetKey(starlark.String("rid"), starlark.MakeInt(c.Result.ID))
			_ = d.SetKey(starlark.String("rcount"), starlark.MakeInt(resultCount(c.Result)))
			out = append(out, d)
		default:
			// Special markers are not matchable — skip (the plugin never matches them).
		}
	}
	return starlark.NewList(out)
}

// resultCount returns the result count, defaulting a 0/omitted count to 1 (the vanilla
// ItemStackTemplate default — level/recipe parses count<=0 as the default-1 result).
func resultCount(s recipe.Stack) int {
	if s.Count <= 0 {
		return 1
	}
	return s.Count
}

// ingredientSet builds the Starlark membership set (a dict id->True) for an Ingredient. An EMPTY
// ingredient (the empty-optional pattern cell) yields an empty dict — the plugin's _ing_empty checks
// len(set)==0, matching Ingredient.Empty().
func ingredientSet(in recipe.Ingredient) starlark.Value {
	ids := in.IDs()
	d := starlark.NewDict(len(ids))
	for _, id := range ids {
		_ = d.SetKey(starlark.MakeInt(id), starlark.True)
	}
	return d
}

// materializeCrafting writes the embedded plugin (plugin.toml + main.star) into a fresh temp plugin
// dir layout (root/crafting/{plugin.toml,main.star}) that LoadDirWith can scan, returning the root.
// The caller removes the dir after load. Mirrors materializeVanillaPig.
func materializeCrafting() (string, error) {
	root, err := os.MkdirTemp("", "sulfur-crafting-*")
	if err != nil {
		return "", fmt.Errorf("crafting boot-load: temp dir: %w", err)
	}
	dir := filepath.Join(root, "crafting")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		os.RemoveAll(root)
		return "", fmt.Errorf("crafting boot-load: mkdir: %w", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := craftingFS.ReadFile("assets/crafting/" + name)
		if err != nil {
			os.RemoveAll(root)
			return "", fmt.Errorf("crafting boot-load: read embedded %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			os.RemoveAll(root)
			return "", fmt.Errorf("crafting boot-load: write %s: %w", name, err)
		}
	}
	return root, nil
}
