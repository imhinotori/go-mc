// Package loot is the shared, PURE loot-table evaluator — the keystone
// STRUCT-POLISH-01 deliverable. It parses the embedded vanilla Minecraft 26.2
// (protocol 776) loot-table JSON into a Go model and reproduces the EXACT vanilla
// chest/block-drop contents per seed by porting the LootTable/LootPool roll engine
// method-for-method from the unobfuscated server jar (temp/cache/26.2-inner.jar,
// read via `javap -c -p`).
//
// 1:1 VANILLA PORT (CLAUDE.md, ABSOLUTE): every algorithm here is a literal,
// method-for-method copy of the decompiled bytecode. The RNG draw order, the
// weight-subtraction selection, the float casts, and the LegacyRandomSource LCG
// math are mirrored EXACTLY — that is what makes a chest reproduce vanilla loot
// for a given (world seed, chunk) lootTableSeed. Every ported method carries a
// `javap`/jar citation in a comment. Re-expressed idiomatically in Go (no GPL
// paste).
//
// SCOPE (per 20-RESEARCH function inventory): the 5 target chest groups
// (desert/jungle temple, igloo, mineshaft, dungeon, village) use a tiny element
// set — 3 functions (set_count, enchant_randomly, enchant_with_levels), 1 number
// provider (uniform, plus the implicit constant from a bare float), and 2 entry
// types (item, empty). Only that MUST-PORT set is ported here. The block-table
// delta (alternatives/match_tool/survives_explosion/apply_bonus/explosion_decay)
// is SHAPED for (the model carries Conditions + Children) but ported in 20-02.
//
// PURITY: this package has no tick state, no I/O, no network surface. It imports
// only world/levelgen (the LegacyRandomSource LCG), data/item (name->id), and
// data/registryid (type-string validation). CGO_ENABLED=0 stays clean; no new
// external deps; the loot JSON is //go:embed'd.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.storage.loot.LootTable.getRandomItemsRaw
//   - net.minecraft.world.level.storage.loot.LootPool.addRandomItems/addRandomItem
//   - net.minecraft.world.level.storage.loot.LootContext$Builder.withOptionalRandomSeed
//   - net.minecraft.world.level.storage.loot.providers.number.UniformGenerator / ConstantValue
//   - net.minecraft.world.level.storage.loot.functions.SetItemCountFunction /
//     EnchantRandomlyFunction / EnchantWithLevelsFunction
//   - net.minecraft.util.Mth.nextInt(RandomSource,int,int) / Math.round / Math.floor
//   - net.minecraft.world.level.levelgen.LegacyRandomSource (the LCG; world/levelgen/random.go)
package loot

import (
	"embed"
	"fmt"
	"path"
	"strings"
)

// dataFS embeds the FULL vanilla loot_table JSON tree (the whole loot_table/
// subtree, 1355 tables, ~2.9MB JSON). Embedding the whole tree (rather than a
// curated subset) gives 20-02 the block tables for free and avoids a curation
// step — per 20-RESEARCH "embed the whole loot_table/ tree once — it's small
// JSON". The tree was extracted from temp/cache/26.2-datagen (the jar's shipped
// data) into level/loot/data/loot_table.
//
//go:embed data/loot_table
//go:embed data/enchantment data/enchantment_tags
var dataFS embed.FS

// resolveID strips a "minecraft:" namespace from a loot-table registry id, leaving
// the path component (which may itself be nested, e.g. "chests/simple_dungeon" or
// "blocks/stone"). A bare id with no namespace is taken as-is.
func resolveID(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 {
		return id[i+1:]
	}
	return id
}

// TableJSON returns the embedded loot-table JSON bytes for a registry id, e.g.
// "minecraft:chests/simple_dungeon" -> data/loot_table/chests/simple_dungeon.json,
// "minecraft:blocks/stone" -> data/loot_table/blocks/stone.json. A missing id
// returns a clear error.
func TableJSON(id string) ([]byte, error) {
	rel := resolveID(id)
	if rel == "" {
		return nil, fmt.Errorf("loot: empty loot-table id")
	}
	p := path.Join("data", "loot_table", rel+".json")
	b, err := dataFS.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("loot: table %q not found (-> %s): %w", id, p, err)
	}
	return b, nil
}

// LoadTable resolves a loot-table registry id, reads its embedded JSON, and parses
// it into the *LootTable model (the parse validates every type string against the
// data/registryid loot lists and errors loudly on any unknown type).
func LoadTable(id string) (*LootTable, error) {
	b, err := TableJSON(id)
	if err != nil {
		return nil, err
	}
	t, err := ParseTable(b)
	if err != nil {
		return nil, fmt.Errorf("loot: parsing table %q: %w", id, err)
	}
	return t, nil
}
