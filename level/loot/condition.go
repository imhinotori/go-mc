package loot

// condition.go — the loot condition dispatch. The 5 chest groups use exactly one
// per-entry condition: abandoned_mineshaft's lone `location_check` (biome predicate
// on one music-disc entry). The dispatch is SHAPED so 20-02 adds the block-table
// condition delta (match_tool, survives_explosion, ...) DATA-ONLY.
//
// Source (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.storage.loot.predicates.LootItemCondition (Predicate<LootContext>)
//   - net.minecraft.world.level.storage.loot.predicates.LocationCheck (biome/structure predicate)
//   - net.minecraft.world.level.storage.loot.entries.LootPoolEntryContainer.canRun (compositeCondition.test)

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/data/registryid"
)

// allConditions mirrors the compositeCondition (Util.allOf): every condition must
// pass. An empty list passes (the all-of identity) — matching canRun for an entry
// with no conditions.
//
// Source: javap LootPoolEntryContainer.canRun (compositeCondition.test) / Util.allOf.
func allConditions(conds []LootCondition, ctx *LootContext) bool {
	for _, c := range conds {
		if !c.Test(ctx) {
			return false
		}
	}
	return true
}

// locationCheck mirrors net.minecraft.world.level.storage.loot.predicates.LocationCheck
// for the one chest use: a biome-membership predicate. The decompiled LocationCheck
// resolves the loot context's ORIGIN position and tests a LocationPredicate (here a
// biome set). The pure chest evaluator does not carry world geometry, so this reads
// the context's Biome field.
//
// CITED STUB (CLAUDE.md "stub behind a cited constant equal to the vanilla default,
// structured to become a real read later"): when the context Biome is unset (the
// pure golden path, and any pre-20-02 caller), the predicate returns TRUE — the
// permissive default that keeps the entry eligible. 20-02 populates ctx.Biome from
// the real chest-open LootParams, at which point this becomes the faithful
// biome-membership test against `biomes`. The golden table (simple_dungeon) has no
// conditions, so this stub never affects the seed-reproduction proof.
type locationCheck struct {
	biomes []string // the predicate's biome allow-set
}

// Test mirrors LocationCheck.test: biome membership at the loot origin (here the
// context Biome). Empty context Biome -> permissive TRUE (cited stub above).
func (l *locationCheck) Test(ctx *LootContext) bool {
	if ctx.Biome == "" {
		return true
	}
	for _, b := range l.biomes {
		if b == ctx.Biome {
			return true
		}
	}
	return false
}

// parseCondition decodes one condition object, dispatching on its "condition" type
// key (validated against registryid.LootConditionType). location_check is parsed
// faithfully; the block-table conditions (match_tool/survives_explosion/...) are
// shaped for 20-02 and error loudly until then. An unrecognized type errors loudly.
func parseCondition(rc rawCondition) (LootCondition, error) {
	typeStr, err := rawString(rc, "condition")
	if err != nil {
		return nil, err
	}
	if typeStr == "" {
		return nil, fmt.Errorf("condition missing \"condition\" type")
	}
	if !validType(typeStr, registryid.LootConditionType) {
		return nil, fmt.Errorf("unrecognized loot condition type %q", typeStr)
	}
	switch normalizeType(typeStr) {
	case "location_check":
		var pred struct {
			Predicate struct {
				// `biomes` is either a single tag/id string or a list of ids.
				Biomes json.RawMessage `json:"biomes"`
			} `json:"predicate"`
		}
		if raw, ok := rc["predicate"]; ok {
			// re-wrap: rc holds the WHOLE condition object; decode predicate.biomes.
			full, _ := json.Marshal(map[string]json.RawMessage{"predicate": raw})
			if err := json.Unmarshal(full, &pred); err != nil {
				return nil, fmt.Errorf("location_check predicate: %w", err)
			}
		}
		return &locationCheck{biomes: parseBiomeList(pred.Predicate.Biomes)}, nil
	case "match_tool":
		// The block tables' only match_tool form is a silk_touch enchantment gate:
		// predicate.predicates["minecraft:enchantments"] = [{enchantments:"minecraft:silk_touch", levels:{min:1}}].
		// Detect that gate so matchTool.Test reads ctx.ToolSilkTouch (the cited stub).
		// 20-02 Task 1 (javap MatchTool.test).
		return &matchTool{requiresSilkTouch: matchToolWantsSilkTouch(rc)}, nil
	case "survives_explosion":
		// ExplosionCondition: no fields. EXPLOSION_RADIUS absent -> true (the v1
		// break default). 20-02 Task 1 (javap ExplosionCondition.test).
		return &explosionCondition{}, nil
	default:
		return nil, fmt.Errorf("loot condition %q not ported (location_check/match_tool/survives_explosion in scope)", typeStr)
	}
}

// matchToolWantsSilkTouch reports whether a match_tool condition's predicate gates on a
// silk_touch enchantment (level>=1) — the only match_tool form the block tables use. It
// inspects predicate.predicates["minecraft:enchantments"][*].enchantments for
// "minecraft:silk_touch". A predicate without an enchantment gate returns false (the
// matchTool then defaults to the predicate.isEmpty()->true path under a real tool).
//
// Source: blocks/*.json match_tool predicate shape (verified against the datagen JSON).
func matchToolWantsSilkTouch(rc rawCondition) bool {
	predRaw, ok := rc["predicate"]
	if !ok {
		return false
	}
	var pred struct {
		Predicates struct {
			Enchantments []struct {
				Enchantments json.RawMessage `json:"enchantments"`
			} `json:"minecraft:enchantments"`
		} `json:"predicates"`
	}
	if err := json.Unmarshal(predRaw, &pred); err != nil {
		return false
	}
	for _, e := range pred.Predicates.Enchantments {
		// `enchantments` is either a single id string or a list of ids.
		for _, id := range parseBiomeList(e.Enchantments) { // reuse the single-or-list decoder
			if id == "minecraft:silk_touch" || id == "silk_touch" {
				return true
			}
		}
	}
	return false
}

// parseBiomeList accepts either a single biome/tag string or a JSON array of biome
// ids, returning the flat list. (A "#tag" reference is kept verbatim; the pure
// evaluator's permissive stub never needs it expanded.)
func parseBiomeList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	return nil
}
