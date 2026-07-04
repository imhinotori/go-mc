package loot

// parse.go — the JSON -> model decoder. It uses stdlib encoding/json (per
// 20-RESEARCH: the JSON is flat and the type set is tiny; no codec lib). Every
// `type` string is VALIDATED against the generated data/registryid loot lists and
// an unrecognized type errors LOUDLY (no silent skip) — mirroring the Phase-11
// feature-parser discipline (T-20-02 Tampering: a corrupted/unknown type fails
// the build, it never silently mis-rolls).

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/registryid"
)

// maxRolls bounds a single pool's roll/count integer so a malformed or hostile
// `rolls`/`count` (e.g. a constant 2_000_000_000) cannot drive an unbounded
// allocation / OOM (T-20-01 DoS, V5 Input Validation). The vanilla chest tables
// roll at most ~8; 4096 is far above any real table while capping the blast
// radius. A constant provider exceeding this errors at parse time; a uniform
// provider is bounded by its own max at parse time too.
const maxRolls = 4096

// itemNameToID resolves a "minecraft:diamond" (or bare "diamond") item id to its
// numeric item.ID. It is built once from item.ByID (the generated id->Item map has
// no reverse index). A miss errors loudly so an unknown item id in a (trusted,
// vendored) table fails the parse rather than rolling a phantom stack.
var itemNameToID = func() map[string]int32 {
	m := make(map[string]int32, len(item.ByID))
	for id, it := range item.ByID {
		m["minecraft:"+it.Name] = int32(id)
		m[it.Name] = int32(id)
	}
	return m
}()

// validType reports whether typeStr is a member of the registry list `valid`. Both
// the namespaced ("minecraft:item") and bare ("item") forms are accepted, matching
// how the JSON sometimes omits the namespace.
func validType(typeStr string, valid []string) bool {
	for _, v := range valid {
		if v == typeStr || v == "minecraft:"+typeStr {
			return true
		}
	}
	return false
}

// rawTable / rawPool / rawEntry / rawFunction / rawCondition mirror the on-disk
// JSON shape. NumberProvider fields are json.RawMessage so a bare float and a
// {type,min,max} object both decode (parseNumberProvider dispatches).
type rawTable struct {
	Pools     []rawPool     `json:"pools"`
	Functions []rawFunction `json:"functions"`
}

type rawPool struct {
	Rolls      json.RawMessage `json:"rolls"`
	BonusRolls json.RawMessage `json:"bonus_rolls"`
	Entries    []rawEntry      `json:"entries"`
	Conditions []rawCondition  `json:"conditions"`
	Functions  []rawFunction   `json:"functions"`
}

type rawEntry struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Value      string         `json:"value"` // NestedLootTable reference id (a "loot_table" entry)
	Weight     *int           `json:"weight"`
	Quality    *int           `json:"quality"`
	Functions  []rawFunction  `json:"functions"`
	Conditions []rawCondition `json:"conditions"`
	Children   []rawEntry     `json:"children"`
}

// rawFunction keeps the whole object so each function decoder can pull its own
// fields (set_count's count, enchant_*'s options/levels). The function-type key in
// the JSON is "function" (not "type").
type rawFunction map[string]json.RawMessage

// rawCondition keeps the whole object; the condition-type key is "condition".
type rawCondition map[string]json.RawMessage

// ParseTable decodes loot-table JSON bytes into the *LootTable model, validating
// every type string and erroring loudly on any unrecognized type.
func ParseTable(data []byte) (*LootTable, error) {
	var rt rawTable
	if err := json.Unmarshal(data, &rt); err != nil {
		return nil, fmt.Errorf("loot: table decode: %w", err)
	}
	t := &LootTable{}
	var err error
	if t.Functions, err = parseFunctions(rt.Functions); err != nil {
		return nil, fmt.Errorf("loot: table functions: %w", err)
	}
	for i, rp := range rt.Pools {
		p, perr := parsePool(rp)
		if perr != nil {
			return nil, fmt.Errorf("loot: pool %d: %w", i, perr)
		}
		t.Pools = append(t.Pools, p)
	}
	return t, nil
}

// parsePool decodes a single pool. `rolls` defaults to nothing (a pool always has
// rolls in vanilla, but we tolerate absence as ConstantValue(0)); `bonus_rolls`
// defaults to ConstantValue(0) (the codec default — 20-RESEARCH).
func parsePool(rp rawPool) (*LootPool, error) {
	p := &LootPool{}
	var err error
	if len(rp.Rolls) == 0 {
		p.Rolls = ConstantValue(0)
	} else if p.Rolls, err = parseNumberProvider(rp.Rolls); err != nil {
		return nil, fmt.Errorf("rolls: %w", err)
	}
	if len(rp.BonusRolls) == 0 {
		p.BonusRolls = ConstantValue(0) // ConstantValue.exactly(0) codec default
	} else if p.BonusRolls, err = parseNumberProvider(rp.BonusRolls); err != nil {
		return nil, fmt.Errorf("bonus_rolls: %w", err)
	}
	if p.Conditions, err = parseConditions(rp.Conditions); err != nil {
		return nil, fmt.Errorf("pool conditions: %w", err)
	}
	if p.Functions, err = parseFunctions(rp.Functions); err != nil {
		return nil, fmt.Errorf("pool functions: %w", err)
	}
	for i, re := range rp.Entries {
		e, eerr := parseEntry(re)
		if eerr != nil {
			return nil, fmt.Errorf("entry %d: %w", i, eerr)
		}
		p.Entries = append(p.Entries, e)
	}
	return p, nil
}

// parseEntry decodes a single entry, validating its type against
// registryid.LootPoolEntryType. weight defaults to 1, quality to 0. An "item"
// entry resolves its Name to a numeric item id at parse time (so the roll path is
// allocation-free); "empty" needs no item.
func parseEntry(re rawEntry) (*Entry, error) {
	if re.Type == "" {
		return nil, fmt.Errorf("entry missing type")
	}
	if !validType(re.Type, registryid.LootPoolEntryType) {
		return nil, fmt.Errorf("unrecognized loot entry type %q", re.Type)
	}
	e := &Entry{Type: re.Type, Name: re.Name, Weight: 1}
	if re.Weight != nil {
		e.Weight = *re.Weight
	}
	if re.Quality != nil {
		e.Quality = *re.Quality
	}
	switch normalizeType(re.Type) {
	case "item":
		id, ok := itemNameToID[re.Name]
		if !ok {
			return nil, fmt.Errorf("unknown item %q in loot entry", re.Name)
		}
		e.itemID = id
	case "empty":
		// no item to resolve
	case "loot_table":
		// NestedLootTable: a singleton container whose createItemStack recursively rolls a
		// REFERENCED loot table (Either<ResourceKey,LootTable>). The 26.2 fishing table uses
		// the by-id form (`value` = "minecraft:gameplay/fishing/junk" ...). Resolve + parse the
		// referenced table eagerly (all sub-tables are embedded), storing it on Ref so the roll
		// engine's createItemStack calls getRandomItemsRaw over it with the SAME context (the
		// same LegacyRandomSource — vanilla threads context through).
		// Source: javap NestedLootTable.createItemStack (contents.map(...).getRandomItemsRaw(context, output)).
		if re.Value == "" {
			return nil, fmt.Errorf("loot_table entry missing \"value\" reference")
		}
		ref, err := LoadTable(re.Value)
		if err != nil {
			return nil, fmt.Errorf("loot_table ref %q: %w", re.Value, err)
		}
		e.Ref = ref
	default:
		// alternatives/group/sequence/tag/dynamic/slots: shaped for 20-02.
		// The 5 chest groups never reach here (only item+empty); a non-chest table
		// that does will have its children parsed below but no roll-engine path yet.
	}
	var err error
	if e.Functions, err = parseFunctions(re.Functions); err != nil {
		return nil, err
	}
	if e.Conditions, err = parseConditions(re.Conditions); err != nil {
		return nil, err
	}
	for i, rc := range re.Children {
		c, cerr := parseEntry(rc)
		if cerr != nil {
			return nil, fmt.Errorf("child %d: %w", i, cerr)
		}
		e.Children = append(e.Children, c)
	}
	return e, nil
}

// parseNumberProvider dispatches a `rolls`/`count`/`min`/`max` value: a bare JSON
// number decodes to ConstantValue; a {type,...} object dispatches on its type
// (uniform | constant), validated against registryid.LootNumberProviderType.
// Per 20-RESEARCH only uniform + the implicit constant are used by the 5 groups;
// any other (binomial/score/...) errors loudly.
func parseNumberProvider(raw json.RawMessage) (NumberProvider, error) {
	// Bare number -> ConstantValue.
	var f float32
	if err := json.Unmarshal(raw, &f); err == nil {
		return ConstantValue(f), nil
	}
	var obj struct {
		Type  string          `json:"type"`
		Value *float32        `json:"value"`
		Min   json.RawMessage `json:"min"`
		Max   json.RawMessage `json:"max"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("number provider decode: %w", err)
	}
	if obj.Type == "" {
		return nil, fmt.Errorf("number provider missing type")
	}
	if !validType(obj.Type, registryid.LootNumberProviderType) {
		return nil, fmt.Errorf("unrecognized number provider type %q", obj.Type)
	}
	switch normalizeType(obj.Type) {
	case "constant":
		if obj.Value == nil {
			return nil, fmt.Errorf("constant number provider missing value")
		}
		return ConstantValue(*obj.Value), nil
	case "uniform":
		lo, err := parseNumberProvider(obj.Min)
		if err != nil {
			return nil, fmt.Errorf("uniform min: %w", err)
		}
		hi, err := parseNumberProvider(obj.Max)
		if err != nil {
			return nil, fmt.Errorf("uniform max: %w", err)
		}
		return &Uniform{Min: lo, Max: hi}, nil
	default:
		return nil, fmt.Errorf("unsupported number provider type %q (only uniform/constant ported)", obj.Type)
	}
}

// parseFunctions decodes a function list, validating each type. The function-type
// key is "function".
func parseFunctions(raws []rawFunction) ([]LootFunction, error) {
	var out []LootFunction
	for _, rf := range raws {
		fn, err := parseFunction(rf)
		if err != nil {
			return nil, err
		}
		out = append(out, fn)
	}
	return out, nil
}

// parseConditions decodes a condition list, validating each type. The
// condition-type key is "condition".
func parseConditions(raws []rawCondition) ([]LootCondition, error) {
	var out []LootCondition
	for _, rc := range raws {
		c, err := parseCondition(rc)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// rawString unmarshals a json.RawMessage into a string, returning "" if absent.
func rawString(raws map[string]json.RawMessage, key string) (string, error) {
	v, ok := raws[key]
	if !ok {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return s, nil
}

// normalizeType strips the "minecraft:" namespace from a type string for switching.
func normalizeType(t string) string {
	const ns = "minecraft:"
	if len(t) >= len(ns) && t[:len(ns)] == ns {
		return t[len(ns):]
	}
	return t
}
