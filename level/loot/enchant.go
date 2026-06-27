package loot

// enchant.go — the two enchantment functions (enchant_randomly: 3 uses;
// enchant_with_levels: 1 use, the jungle_temple book). Both draw RNG, so their
// draw order is load-bearing for per-seed reproduction of any pool that contains
// them. enchant_randomly is ported FULLY (the enchantment list + max levels are
// embedded data); enchant_with_levels' EnchantmentHelper.enchantItem cost-weighted
// selection is the heaviest vanilla piece and the per-enchantment cost/exclusivity
// subsystem is not yet ported — its result is stubbed behind a CITED constant
// (CLAUDE.md), structured to become a real read when that subsystem lands.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.storage.loot.functions.EnchantRandomlyFunction.run/enchantItem
//   - net.minecraft.util.Util.getRandomSafe (list.get(rng.nextInt(size)))
//   - net.minecraft.world.item.enchantment.Enchantment.getMinLevel (==1) / getMaxLevel
//   - net.minecraft.world.level.storage.loot.functions.EnchantWithLevelsFunction.run
//   - net.minecraft.world.item.enchantment.EnchantmentHelper.enchantItem/selectEnchantment

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// enchantMaxLevel resolves an enchantment id ("minecraft:mending" or bare) to its
// max_level from the embedded enchantment registry JSON. getMinLevel() is always 1
// in vanilla (Enchantment.getMinLevel -> iconst_1), so only max_level is needed for
// the enchant_randomly level draw. Loaded once.
var enchantMaxLevel = func() map[string]int {
	m := map[string]int{}
	ents, err := dataFS.ReadDir("data/enchantment")
	if err != nil {
		return m
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := dataFS.ReadFile("data/enchantment/" + e.Name())
		if err != nil {
			continue
		}
		var def struct {
			MaxLevel int `json:"max_level"`
		}
		if json.Unmarshal(b, &def) != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		m["minecraft:"+id] = def.MaxLevel
		m[id] = def.MaxLevel
	}
	return m
}()

// resolveEnchantTag expands an enchantment tag (e.g. "#minecraft:on_random_loot")
// or a bare enchantment id into a SORTED list of concrete enchantment ids. Sorting
// gives a deterministic iteration order so the Util.getRandomSafe nextInt(size)
// index maps to a stable enchantment per (tag, draw) — the vanilla registry
// holderset order is itself deterministic; sorting is our faithful stand-in for
// that fixed order. Nested "#tag" references are expanded recursively.
func resolveEnchantTag(ref string, seen map[string]bool) []string {
	if !strings.HasPrefix(ref, "#") {
		return []string{ref}
	}
	name := strings.TrimPrefix(ref, "#")
	name = strings.TrimPrefix(name, "minecraft:")
	if seen[name] {
		return nil
	}
	seen[name] = true
	b, err := dataFS.ReadFile("data/enchantment_tags/" + name + ".json")
	if err != nil {
		return nil
	}
	var doc struct {
		Values []string `json:"values"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	var out []string
	for _, v := range doc.Values {
		if strings.HasPrefix(v, "#") {
			out = append(out, resolveEnchantTag(v, seen)...)
			continue
		}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// EnchantRandomlyFunction mirrors
// net.minecraft.world.level.storage.loot.functions.EnchantRandomlyFunction. run:
// pick one enchantment from `options` via Util.getRandomSafe(list, rng) (one
// nextInt(size) draw), then enchantItem draws the level via Mth.nextInt(rng, 1,
// maxLevel) and applies it (a BOOK becomes an ENCHANTED_BOOK).
//
// Source: javap EnchantRandomlyFunction.run + enchantItem.
type EnchantRandomlyFunction struct {
	// options is the resolved, sorted list of candidate enchantment ids (the
	// #on_random_loot holderset for the chest books). nil means "all enchantments"
	// in vanilla; the chest uses always specify options, so nil is not exercised.
	options []string
}

// Run mirrors EnchantRandomlyFunction.run for the chest path (onlyCompatible /
// includeAdditionalCostComponent are false for the chest uses, so the compatibility
// filter is identity and no trade-cost component is set). The draw order is:
//  1. Util.getRandomSafe(options, rng): if empty -> no-op; else pick =
//     options[rng.nextInt(len(options))].
//  2. enchantItem: level = Mth.nextInt(rng, 1, maxLevel(pick)); record (pick->level).
//
// A BOOK item id becoming ENCHANTED_BOOK is recorded by upgrading the stack item
// id when the registry has that mapping; the enchantment itself is recorded on
// stack.Enchantments for the 20-02 BE write (the pure evaluator does not own the
// enchantments component schema).
//
// Source: javap EnchantRandomlyFunction.run + Util.getRandomSafe + Enchantment.getMaxLevel.
func (e *EnchantRandomlyFunction) Run(stack *ItemStack, ctx *LootContext) *ItemStack {
	rng := ctx.Random()
	opts := e.options
	if len(opts) == 0 {
		// vanilla: options=empty -> stream over ALL enchantments; not exercised by
		// the chest uses (they always pass #on_random_loot). Faithful no-op here.
		return stack
	}
	// Util.getRandomSafe(list, rng): list.get(rng.nextInt(list.size())).
	idx := int(rng.NextIntN(int32(len(opts))))
	pick := opts[idx]
	// enchantItem: level = Mth.nextInt(rng, getMinLevel()=1, getMaxLevel()).
	maxLvl := enchantMaxLevel[pick]
	if maxLvl < 1 {
		maxLvl = 1
	}
	level := mthNextInt(rng, 1, maxLvl)
	if stack.Enchantments == nil {
		stack.Enchantments = map[string]int{}
	}
	stack.Enchantments[pick] = level
	// BOOK -> ENCHANTED_BOOK (vanilla swaps the item before stack.enchant).
	if bookID, ok := itemNameToID["minecraft:book"]; ok && stack.ItemID == bookID {
		if encBook, ok := itemNameToID["minecraft:enchanted_book"]; ok {
			stack.ItemID = encBook
		}
	}
	return stack
}

// parseEnchantRandomly decodes an enchant_randomly function. `options` is an
// enchantment id, a "#tag" holderset (the chest books use "#minecraft:on_random_loot"),
// or a list; it resolves to the sorted candidate list.
func parseEnchantRandomly(rf rawFunction) (LootFunction, error) {
	fn := &EnchantRandomlyFunction{}
	if raw, ok := rf["options"]; ok {
		// options is either a string (id or #tag) or a list of ids.
		var single string
		if err := json.Unmarshal(raw, &single); err == nil {
			fn.options = resolveEnchantTag(single, map[string]bool{})
		} else {
			var list []string
			if err := json.Unmarshal(raw, &list); err != nil {
				return nil, fmt.Errorf("enchant_randomly options: %w", err)
			}
			seen := map[string]bool{}
			for _, v := range list {
				fn.options = append(fn.options, resolveEnchantTag(v, seen)...)
			}
			sort.Strings(fn.options)
		}
	}
	return fn, nil
}

// EnchantWithLevelsFunction mirrors
// net.minecraft.world.level.storage.loot.functions.EnchantWithLevelsFunction. run:
// `int budget = levels.getInt(ctx); stack = EnchantmentHelper.enchantItem(rng,
// stack, budget, registryAccess, options);`. The single chest use is the
// jungle_temple book at levels:30.
//
// Source: javap EnchantWithLevelsFunction.run.
type EnchantWithLevelsFunction struct {
	Levels  NumberProvider
	options []string // optional candidate set (jungle_temple book passes none)
}

// Run mirrors EnchantWithLevelsFunction.run.
//
// CITED STUB (CLAUDE.md "stub behind a cited constant equal to the vanilla
// default, structured so it becomes a real read later — never bake the value
// away"): the budget draw `levels.getInt(ctx)` IS performed faithfully (it consumes
// RNG and must, to keep any downstream pool draw aligned). The downstream
// EnchantmentHelper.enchantItem(rng, stack, budget, ...) — the cost-weighted
// selection over the full enchantment registry with exclusivity sets — is NOT yet
// ported (the per-enchantment min/max-cost + exclusivity subsystem does not exist).
// Until that subsystem lands, the selection result is recorded as a single
// placeholder entry keyed by the cited helper, with the budget level, so the
// roll's item is produced and the enchantment intent is preserved (never silently
// dropped). The golden table (simple_dungeon) has NO enchant_with_levels entry, so
// this stub never affects the seed-reproduction proof. A later plan replaces the
// recordEnchantWithLevels body with the real EnchantmentHelper.selectEnchantment
// draw chain.
//
// Source: javap EnchantWithLevelsFunction.run -> EnchantmentHelper.enchantItem.
func (e *EnchantWithLevelsFunction) Run(stack *ItemStack, ctx *LootContext) *ItemStack {
	budget := e.Levels.GetInt(ctx) // faithful: consumes the levels NumberProvider's RNG draws
	if stack.Enchantments == nil {
		stack.Enchantments = map[string]int{}
	}
	// STUB: real selection deferred (see doc). Record the budget under the cited
	// helper key so the intent is not lost; do NOT fabricate a specific enchant.
	stack.Enchantments["__enchant_with_levels_budget__"] = budget
	// BOOK -> ENCHANTED_BOOK (vanilla EnchantmentHelper.enchantItem swaps a book).
	if bookID, ok := itemNameToID["minecraft:book"]; ok && stack.ItemID == bookID {
		if encBook, ok := itemNameToID["minecraft:enchanted_book"]; ok {
			stack.ItemID = encBook
		}
	}
	return stack
}

// parseEnchantWithLevels decodes an enchant_with_levels function: the `levels`
// number provider (uniform or constant) plus optional `options`.
func parseEnchantWithLevels(rf rawFunction) (LootFunction, error) {
	fn := &EnchantWithLevelsFunction{}
	raw, ok := rf["levels"]
	if !ok {
		return nil, fmt.Errorf("enchant_with_levels missing levels")
	}
	np, err := parseNumberProvider(raw)
	if err != nil {
		return nil, fmt.Errorf("enchant_with_levels levels: %w", err)
	}
	fn.Levels = np
	if raw, ok := rf["options"]; ok {
		var single string
		if err := json.Unmarshal(raw, &single); err == nil {
			fn.options = resolveEnchantTag(single, map[string]bool{})
		}
	}
	return fn, nil
}
