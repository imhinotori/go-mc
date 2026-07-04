package registrydata

// enchantment_def.go exposes the per-enchantment DEFINITION fields (anvil_cost, max_level,
// weight, min_cost / max_cost curves, supported_items / primary_items / exclusive_set holder
// references) parsed from the SAME embedded registries/enchantment/<name>.json tree the server
// sends to the client (registryFS), plus the enchantment-registry TAG membership reads
// (in_enchanting_table, exclusive_set/*) from the embedded tags/enchantment tree (tagFS).
//
// WHY HERE: the AnvilMenu enchant-merge cost math and the EnchantmentMenu offer selection are
// 1:1 ports (net.minecraft.world.inventory.AnvilMenu.createResult /
// net.minecraft.world.item.enchantment.EnchantmentHelper.selectEnchantment) that read the
// enchantment DEFINITION (Enchantment.getAnvilCost / getMaxLevel / getWeight / getMinCost /
// getMaxCost, Enchantment.Cost.calculate = base + perLevelAboveFirst*(level-1)) and the
// exclusive-set / in_enchanting_table tags. Those numbers live in the embedded 26.2 registry
// content — the authoritative source (same bytes the wire packet emits) — so the server package
// reads them through this accessor rather than hardcoding a table (which would violate the
// datapack-registry 1:1 mandate). CITE Enchantment (record: definition, exclusiveSet) +
// EnchantmentDefinition (supportedItems, primaryItems, weight, maxLevel, minCost, maxCost,
// anvilCost) — 26.2 jar.

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
)

// EnchantCost is Enchantment.Cost (base + perLevelAboveFirst). Cost.calculate(level) =
// base + perLevelAboveFirst*(level-1). CITE Enchantment$Cost.calculate.
type EnchantCost struct {
	Base               int `json:"base"`
	PerLevelAboveFirst int `json:"per_level_above_first"`
}

// Calculate ports Enchantment.Cost.calculate: base + perLevelAboveFirst * (level - 1).
func (c EnchantCost) Calculate(level int) int {
	return c.Base + c.PerLevelAboveFirst*(level-1)
}

// EnchantmentDef is the parsed EnchantmentDefinition + the exclusive_set holder ref of one
// enchantment. The holder-reference fields (SupportedItems / PrimaryItems / ExclusiveSet) keep
// the RAW JSON reference — a "#minecraft:<tag>" string, a "minecraft:<id>" string, or a JSON
// list — resolved on demand by the server-side membership readers (item tags live in
// data/tag.ItemTags; enchantment exclusive_set tags live in tagFS). Absent fields are the
// vanilla defaults: primary_items defaults to supported_items (isPrimaryItem falls back), and an
// absent exclusive_set means "no exclusions" (HolderSet.empty()).
type EnchantmentDef struct {
	AnvilCost int         `json:"anvil_cost"`
	MaxLevel  int         `json:"max_level"`
	Weight    int         `json:"weight"`
	MinCost   EnchantCost `json:"min_cost"`
	MaxCost   EnchantCost `json:"max_cost"`
	// SupportedItems / PrimaryItems / ExclusiveSet are the raw JSON references, decoded to a
	// flat []string of "#tag" / "minecraft:id" tokens (a single string becomes one element, a
	// list becomes N). ExclusiveSet is nil/empty when the field is absent.
	SupportedItems []string `json:"-"`
	PrimaryItems   []string `json:"-"`
	ExclusiveSet   []string `json:"-"`
}

// rawEnchantmentDef mirrors the on-disk JSON, with the three holder references left as
// json.RawMessage so each can be a string OR a list.
type rawEnchantmentDef struct {
	AnvilCost      int             `json:"anvil_cost"`
	MaxLevel       int             `json:"max_level"`
	Weight         int             `json:"weight"`
	MinCost        EnchantCost     `json:"min_cost"`
	MaxCost        EnchantCost     `json:"max_cost"`
	SupportedItems json.RawMessage `json:"supported_items"`
	PrimaryItems   json.RawMessage `json:"primary_items"`
	ExclusiveSet   json.RawMessage `json:"exclusive_set"`
}

var (
	enchDefOnce sync.Once
	enchDefMap  map[string]*EnchantmentDef // key: "minecraft:<name>"
	enchDefErr  error
)

// loadEnchantmentDefs parses every registries/enchantment/<name>.json into an EnchantmentDef,
// once. The map key is the full resource id ("minecraft:<name>"), matching Load()'s Entry.Key.
func loadEnchantmentDefs() {
	base := path.Join("registries", "enchantment")
	names, err := entryFiles(base)
	if err != nil {
		enchDefErr = fmt.Errorf("registrydata: list enchantment defs: %w", err)
		return
	}
	m := make(map[string]*EnchantmentDef, len(names))
	for _, name := range names {
		data, err := registryFS.ReadFile(path.Join(base, name))
		if err != nil {
			enchDefErr = fmt.Errorf("registrydata: read enchantment %s: %w", name, err)
			return
		}
		var raw rawEnchantmentDef
		if err := json.Unmarshal(data, &raw); err != nil {
			enchDefErr = fmt.Errorf("registrydata: parse enchantment %s: %w", name, err)
			return
		}
		def := &EnchantmentDef{
			AnvilCost:      raw.AnvilCost,
			MaxLevel:       raw.MaxLevel,
			Weight:         raw.Weight,
			MinCost:        raw.MinCost,
			MaxCost:        raw.MaxCost,
			SupportedItems: decodeHolderRef(raw.SupportedItems),
			PrimaryItems:   decodeHolderRef(raw.PrimaryItems),
			ExclusiveSet:   decodeHolderRef(raw.ExclusiveSet),
		}
		key := "minecraft:" + strings.TrimSuffix(name, ".json")
		m[key] = def
	}
	enchDefMap = m
}

// decodeHolderRef normalizes a HolderSet JSON reference into a flat []string of tokens: a bare
// string ("#minecraft:tag" or "minecraft:id") -> one element; a JSON list -> N elements. An
// absent/empty ref -> nil. CITE RegistryCodecs.homogeneousList (a HolderSet is either a "#tag",
// a single id, or a list of ids).
func decodeHolderRef(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single == "" {
			return nil
		}
		return []string{single}
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	return nil
}

// EnchantmentDefinition returns the parsed definition of an enchantment by resource id
// ("minecraft:<name>", the "#"/namespace-optional forms are normalized). Returns (nil, false)
// for an unknown id. The parse runs once and is cached.
func EnchantmentDefinition(id string) (*EnchantmentDef, bool) {
	enchDefOnce.Do(loadEnchantmentDefs)
	if enchDefErr != nil {
		return nil, false
	}
	def, ok := enchDefMap[normalizeEnchantmentID(id)]
	return def, ok
}

// EnchantmentInTag reports whether an enchantment resource id is a member of an enchantment
// registry tag (e.g. "in_enchanting_table", "exclusive_set/damage"), flattening any nested
// "#tag" references. It reads the SAME embedded tags/enchantment tree the Update Tags wire
// packet uses (tagFS). An absent tag -> false (empty-tag semantics).
func EnchantmentInTag(enchantID, tagName string) (bool, error) {
	members, err := enchantmentTagMembers(normalizeTagName(tagName))
	if err != nil {
		return false, err
	}
	_, ok := members[normalizeEnchantmentID(enchantID)]
	return ok, nil
}

// EnchantmentTagMembers returns the flattened set of enchantment resource ids in an enchantment
// tag (e.g. "in_enchanting_table"), expanding nested "#tag" references. The order is not
// meaningful (a set); callers that need a deterministic order sort it.
func EnchantmentTagMembers(tagName string) ([]string, error) {
	members, err := enchantmentTagMembers(normalizeTagName(tagName))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(members))
	for id := range members {
		out = append(out, id)
	}
	return out, nil
}

// enchantmentTagMembers flattens tags/enchantment/<tag>.json into a member set, recursively
// expanding "#tag" values. Mirrors blockTagMembers over the enchantment tag directory.
func enchantmentTagMembers(tag string) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	if err := expandEnchantmentTag(tag, set, map[string]bool{}); err != nil {
		return nil, err
	}
	return set, nil
}

func expandEnchantmentTag(tag string, set map[string]struct{}, seen map[string]bool) error {
	if seen[tag] {
		return nil
	}
	seen[tag] = true
	file := path.Join("tags", "enchantment", tag+".json")
	data, err := tagFS.ReadFile(file)
	if err != nil {
		// An absent tag file is not an error: empty-tag semantics (no members).
		return nil
	}
	var doc struct {
		Values []json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("registrydata: parse enchantment tag %s: %w", tag, err)
	}
	for _, raw := range doc.Values {
		var v string
		// A value may be a bare id/#tag string, or an object {id, required}.
		if err := json.Unmarshal(raw, &v); err != nil {
			var obj struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &obj); err != nil {
				continue
			}
			v = obj.ID
		}
		if strings.HasPrefix(v, "#") {
			if err := expandEnchantmentTag(normalizeTagName(v), set, seen); err != nil {
				return err
			}
			continue
		}
		set[normalizeEnchantmentID(v)] = struct{}{}
	}
	return nil
}

// normalizeEnchantmentID canonicalizes an enchantment reference to "minecraft:<name>": strips a
// leading "#" (a tag ref used as an id is not expected here but tolerated) and adds the default
// namespace when absent.
func normalizeEnchantmentID(id string) string {
	id = strings.TrimPrefix(id, "#")
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}
