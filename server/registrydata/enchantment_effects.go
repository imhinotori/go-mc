package registrydata

// enchantment_effects.go exposes the per-enchantment EFFECT COMPONENTS ("effects" map) and the
// equipment-slot-group list ("slots") parsed from the SAME embedded registries/enchantment/<name>.json
// tree the server sends to the client (registryFS) — the data half of the enchantment-effect RUNTIME
// (E-3). The effect payloads are kept as raw JSON here; the server-side interpreter
// (server/enchant_effects.go) parses them into the EnchantmentValueEffect / EnchantmentEntityEffect /
// LevelBasedValue model, mirroring how vanilla's Enchantment record carries a DataComponentMap of
// effect components decoded from the same datapack JSON.
//
// The slice is indexed by the enchantment WIRE id — the index of the entry in the alphabetically
// sorted registries/enchantment tree, which IS the numeric protocol id the RegistryData send order
// assigns (see enchantment_order.go). CITE net.minecraft.world.item.enchantment.Enchantment
// (record component `DataComponentMap effects` + `EnchantmentDefinition definition.slots()`).

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
)

// EnchantmentEffectDef is one enchantment's runtime-effect data: its resource id, the
// EquipmentSlotGroup keys of definition.slots() (the Enchantment.matchingSlot filter), and the raw
// "effects" component map (component id -> raw JSON payload, usually a list of conditional effects).
type EnchantmentEffectDef struct {
	ID      string                     // "minecraft:<name>"
	Slots   []string                   // definition.slots() group keys ("mainhand", "armor", "any", ...)
	Effects map[string]json.RawMessage // "minecraft:damage" -> raw conditional-effect list, etc.
}

var (
	enchEffOnce sync.Once
	enchEffDefs []EnchantmentEffectDef
	enchEffErr  error
)

// EnchantmentEffectDefs returns the effect data of every enchantment, indexed by WIRE id (the sorted
// registry order — the same indexing EnchantmentCosts uses). Parsed once and cached; any parse error
// is returned loudly on every call (never a silent empty table).
func EnchantmentEffectDefs() ([]EnchantmentEffectDef, error) {
	enchEffOnce.Do(func() {
		base := path.Join("registries", "enchantment")
		names, err := entryFiles(base)
		if err != nil {
			enchEffErr = fmt.Errorf("registrydata: list enchantment effects: %w", err)
			return
		}
		defs := make([]EnchantmentEffectDef, len(names))
		for i, name := range names {
			data, err := registryFS.ReadFile(path.Join(base, name))
			if err != nil {
				enchEffErr = fmt.Errorf("registrydata: read enchantment %s: %w", name, err)
				return
			}
			var raw struct {
				Slots   []string                   `json:"slots"`
				Effects map[string]json.RawMessage `json:"effects"`
			}
			if err := json.Unmarshal(data, &raw); err != nil {
				enchEffErr = fmt.Errorf("registrydata: parse enchantment %s: %w", name, err)
				return
			}
			defs[i] = EnchantmentEffectDef{
				ID:      "minecraft:" + strings.TrimSuffix(name, ".json"),
				Slots:   raw.Slots,
				Effects: raw.Effects,
			}
		}
		enchEffDefs = defs
	})
	return enchEffDefs, enchEffErr
}

// EntityTypeInTag reports whether an entity-type resource id is a member of an entity_type registry
// tag (e.g. "sensitive_to_smite"), flattening nested "#tag" references. It reads the SAME embedded
// tags/entity_type tree the Update Tags wire packet uses (tagFS). An absent tag -> false (empty-tag
// semantics). The Smite/Bane-of-Arthropods damage conditions (LootItemEntityPropertyCondition over
// EntityPredicate.entityType) resolve their "#minecraft:sensitive_to_*" refs through this.
func EntityTypeInTag(typeID, tagName string) (bool, error) {
	members, err := registryTagMembers("entity_type", normalizeTagName(tagName))
	if err != nil {
		return false, err
	}
	_, ok := members[normalizeEnchantmentID(typeID)]
	return ok, nil
}

// registryTagMembers flattens tags/<dir>/<tag>.json into a member set, recursively expanding "#tag"
// values — the generic form of enchantmentTagMembers (enchantment_def.go), shared by the entity_type
// reads above.
func registryTagMembers(dir, tag string) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	if err := expandRegistryTag(dir, tag, set, map[string]bool{}); err != nil {
		return nil, err
	}
	return set, nil
}

func expandRegistryTag(dir, tag string, set map[string]struct{}, seen map[string]bool) error {
	if seen[tag] {
		return nil
	}
	seen[tag] = true
	file := path.Join("tags", dir, tag+".json")
	data, err := tagFS.ReadFile(file)
	if err != nil {
		// An absent tag file is not an error: empty-tag semantics (no members).
		return nil
	}
	var doc struct {
		Values []json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("registrydata: parse %s tag %s: %w", dir, tag, err)
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
			if err := expandRegistryTag(dir, normalizeTagName(v), set, seen); err != nil {
				return err
			}
			continue
		}
		set[normalizeEnchantmentID(v)] = struct{}{}
	}
	return nil
}
