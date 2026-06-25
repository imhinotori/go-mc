package feature

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/imhinotori/sulfur/level/block"
)

// DataSource loads the raw JSON bytes for a configured_feature / placed_feature
// registry ref. The Wave-1 data package implements this (data.ConfiguredFeatureJSON
// / data.PlacedFeatureJSON via DataSourceFunc adapters); tests may supply an
// in-memory map. It is how a placed_feature's "feature" string ref resolves to the
// configured_feature file the parser then parses.
type DataSource interface {
	ConfiguredFeatureJSON(id string) ([]byte, error)
	PlacedFeatureJSON(id string) ([]byte, error)
}

// ConfiguredSourceFunc / PlacedSourceFunc adapt plain funcs (data.ConfiguredFeature
// / data.PlacedFeature from Wave 1) to a DataSource, so the registry and tests wire
// the embedded graph without a wrapper type — mirroring density.DataSourceFunc.
type (
	ConfiguredSourceFunc func(id string) ([]byte, error)
	PlacedSourceFunc     func(id string) ([]byte, error)
)

// funcSource adapts a pair of loader funcs to the DataSource interface.
type funcSource struct {
	configured func(id string) ([]byte, error)
	placed     func(id string) ([]byte, error)
}

func (f funcSource) ConfiguredFeatureJSON(id string) ([]byte, error) { return f.configured(id) }
func (f funcSource) PlacedFeatureJSON(id string) ([]byte, error)     { return f.placed(id) }

// NewFuncSource builds a DataSource from the two embedded-data accessor funcs
// (data.ConfiguredFeatureJSON, data.PlacedFeatureJSON).
func NewFuncSource(configured, placed func(id string) ([]byte, error)) DataSource {
	return funcSource{configured: configured, placed: placed}
}

// stripNS strips the "minecraft:" namespace from a type so dispatch is
// namespace-agnostic (mirrors density.stripNS).
func stripNS(t string) string {
	if i := strings.IndexByte(t, ':'); i >= 0 {
		return t[i+1:]
	}
	return t
}

// objNode is the common envelope: every configured_feature object carries a "type"
// (the feature type) + a "config" body; the placed_feature carries "feature" +
// "placement". Decoded lazily.
type objNode struct {
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config"`
	Feature   json.RawMessage `json:"feature"`
	Placement []json.RawMessage `json:"placement"`
}

// recognizedFeatureTypes is the SET of vanilla configured_feature "type" values,
// namespace-stripped, derived from the distinct "type" values across the 226
// configured_feature JSONs the 26.2 jar ships (computed once and embedded here so a
// genuinely-unknown type — one NOT in this set — errors loudly, while every real
// vanilla type parses to a typed-but-deferred node). Registry.LoadAll additionally
// cross-checks this set against the live embed so a jar bump that adds a type fails
// loudly rather than drifting silently.
//
// JAR-CONFIRMED (the distinct top-level "type" across configured_feature/*.json).
var recognizedFeatureTypes = map[string]struct{}{
	"bamboo":                         {},
	"basalt_columns":                 {},
	"basalt_pillar":                  {},
	"block_blob":                     {},
	"block_column":                   {},
	"block_pile":                     {},
	"blue_ice":                       {},
	"bonus_chest":                    {},
	"chorus_plant":                   {},
	// coral_claw/coral_mushroom/coral_tree appear ONLY inline inside the warm-ocean
	// coral simple_random_selector sub-features. 11-01 never parsed selector
	// sub-features (walkBlockStates resolved only block-state leaves), so these were
	// latent until 12-03's selector recursion forced the inline parse — JAR-CONFIRMED
	// real feature classes (CoralClaw/CoralMushroom/CoralTreeFeature.class).
	"coral_claw":                     {},
	"coral_mushroom":                 {},
	"coral_tree":                     {},
	"delta_feature":                  {},
	"desert_well":                    {},
	"disk":                           {},
	"end_gateway":                    {},
	"end_island":                     {},
	"end_platform":                   {},
	"end_spike":                      {},
	"fallen_tree":                    {},
	"fossil":                         {},
	"freeze_top_layer":               {},
	"geode":                          {},
	"glowstone_blob":                 {},
	"huge_brown_mushroom":            {},
	"huge_fungus":                    {},
	"huge_red_mushroom":              {},
	"iceberg":                        {},
	"kelp":                           {},
	"lake":                           {},
	"large_dripstone":                {},
	"monster_room":                   {},
	"multiface_growth":               {},
	"nether_forest_vegetation":       {},
	"netherrack_replace_blobs":       {},
	"no_op":                          {}, // present in the roster (empty-config feature)
	"ore":                            {},
	"random_boolean_selector":        {},
	"random_patch":                   {}, // vegetation workhorse
	"random_selector":                {},
	"root_system":                    {},
	"scattered_ore":                  {},
	"sculk_patch":                    {},
	"sea_pickle":                     {},
	"seagrass":                       {},
	"sequence":                       {},
	"simple_block":                   {},
	"simple_random_selector":         {},
	// speleothem appears inline inside pointed_dripstone's simple_random_selector
	// sub-features (distinct from speleothem_cluster). JAR-CONFIRMED SpeleothemFeature.
	"speleothem":                     {},
	"speleothem_cluster":             {},
	"spike":                          {},
	"spring_feature":                 {},
	// template appears inline inside selector sub-features (structure-template feature).
	// JAR-CONFIRMED TemplateFeature.class / TemplateFeatureConfiguration.
	"template":                       {},
	"tree":                           {},
	"twisting_vines":                 {},
	"underwater_magma":               {},
	"vegetation_patch":               {},
	"vines":                          {},
	"void_start_platform":            {},
	"waterlogged_vegetation_patch":   {},
	"weeping_vines":                  {},
	"weighted_random_selector":       {},
}

// recognizedProviderTypes is the SET of BlockStateProvider "type" values
// (namespace-stripped) the feature configs nest (JAR-CONFIRMED, research Wave D).
// Phase 11 parses the provider envelope (capturing its inner state(s) resolved to
// StateID); the getState() body is Phase 13's job. A provider type NOT in this set
// is not errored on directly here — the config capture treats provider objects
// generically — but the set documents the known roster for 11-02/11-03.
var recognizedProviderTypes = map[string]struct{}{
	"simple_state_provider":         {},
	"weighted_state_provider":       {},
	"rule_based_state_provider":     {},
	"noise_provider":                {},
	"dual_noise_provider":           {},
	"noise_threshold_provider":      {},
	"rotated_block_provider":        {},
	"randomized_int_state_provider": {},
}

// ParseConfiguredFeature parses a configured_feature object into a typed-but-
// deferred ConfiguredFeature. It dispatches on the "type" field: a type in the
// recognized set parses to a generic typed node (the config captured + its
// block-state refs resolved); a GENUINELY unknown type errors LOUDLY naming it
// (the density end_islands precedent, T-11-01). id is the registry id (for error
// context + the node's ID), or "" for an inline config.
func (r *Registry) ParseConfiguredFeature(id string, raw json.RawMessage) (*ConfiguredFeature, error) {
	var head objNode
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("feature: bad configured_feature object %q: %w", id, err)
	}
	if head.Type == "" {
		return nil, fmt.Errorf("feature: configured_feature %q missing \"type\": %s", id, truncate(string(raw)))
	}
	typ := stripNS(head.Type)
	if _, ok := recognizedFeatureTypes[typ]; !ok {
		// Unknown type — error loudly naming it (T-11-01). Never a silent mis-parse.
		return nil, fmt.Errorf("feature: unknown configured_feature type %q in %q "+
			"(not in the 26.2 vanilla roster; a build-data mismatch — fix the recognized set or the embed)", head.Type, id)
	}

	// Capture the config (deferred to Phase 12+) and resolve its block-state refs.
	cfg := &ParsedConfig{Raw: head.Config}
	if len(head.Config) > 0 {
		states, err := r.resolveConfigStates(head.Config)
		if err != nil {
			return nil, fmt.Errorf("feature: configured_feature %q (type %s): %w", id, typ, err)
		}
		cfg.States = states
	}

	return &ConfiguredFeature{ID: id, Type: typ, Config: cfg}, nil
}

// ParsePlacedFeature parses a placed_feature object into a PlacedFeature: its
// "feature" ref resolves (cached/DAG-deduped, cycle-guarded) to the configured_
// feature, and the "placement" array is captured as an ordered []PlacementModifierRaw
// for plan 11-02 to bind. The "feature" field may be a string ref OR an inline
// configured_feature object (both JAR-valid).
func (r *Registry) ParsePlacedFeature(id string, raw json.RawMessage) (*PlacedFeature, error) {
	var head objNode
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("feature: bad placed_feature object %q: %w", id, err)
	}
	if len(head.Feature) == 0 {
		return nil, fmt.Errorf("feature: placed_feature %q missing \"feature\"", id)
	}

	pf := &PlacedFeature{ID: id}

	// The "feature" field: a string ref (resolve via Registry) or an inline object.
	trimmed := strings.TrimSpace(string(head.Feature))
	switch {
	case len(trimmed) > 0 && trimmed[0] == '"':
		var ref string
		if err := json.Unmarshal(head.Feature, &ref); err != nil {
			return nil, fmt.Errorf("feature: placed_feature %q bad feature ref: %w", id, err)
		}
		cf, err := r.ResolveConfigured(ref)
		if err != nil {
			return nil, fmt.Errorf("feature: placed_feature %q feature ref %q: %w", id, ref, err)
		}
		pf.FeatureRef = ref
		pf.Feature = cf
	case len(trimmed) > 0 && trimmed[0] == '{':
		cf, err := r.ParseConfiguredFeature("", head.Feature)
		if err != nil {
			return nil, fmt.Errorf("feature: placed_feature %q inline feature: %w", id, err)
		}
		pf.Feature = cf
	default:
		return nil, fmt.Errorf("feature: placed_feature %q feature field is neither a ref nor an object: %s", id, truncate(string(head.Feature)))
	}

	// Capture the placement modifier list verbatim (ordered) for plan 11-02.
	for i, mraw := range head.Placement {
		var mh objNode
		if err := json.Unmarshal(mraw, &mh); err != nil {
			return nil, fmt.Errorf("feature: placed_feature %q placement[%d]: %w", id, i, err)
		}
		if mh.Type == "" {
			return nil, fmt.Errorf("feature: placed_feature %q placement[%d] missing \"type\"", id, i)
		}
		pf.Placement = append(pf.Placement, PlacementModifierRaw{Type: stripNS(mh.Type), Raw: mraw})
	}

	return pf, nil
}

// resolveConfigStates walks a config JSON value and resolves every bare
// {Name, Properties} block-state reference it carries to a block.StateID (FEAT-02:
// "a parsed config carries resolved block.StateID values"). It recurses through
// objects and arrays so block-state refs nested inside provider envelopes
// (simple/weighted/rule_based_state_provider "state"/"entries"/"rules") and feature
// configs are all resolved. An unknown block/property errors loudly. It does NOT
// invent semantics for the providers — it only resolves the {Name,Properties}
// leaves; the provider getState() body stays deferred to Phase 13.
func (r *Registry) resolveConfigStates(raw json.RawMessage) ([]block.StateID, error) {
	var states []block.StateID
	if err := walkBlockStates(raw, &states); err != nil {
		return nil, err
	}
	return states, nil
}

// walkBlockStates recursively descends a JSON value, resolving any object that is a
// {Name, Properties} block-state reference (an object with a "Name" string key) to a
// StateID and appending it to out. Other objects/arrays are descended into. A JSON
// object carrying a "Name" key is treated as a block-state leaf (and NOT descended
// further, since its Properties values are bare strings, not refs).
func walkBlockStates(raw json.RawMessage, out *[]block.StateID) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil
	}
	switch trimmed[0] {
	case '{':
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("descending object: %w", err)
		}
		if nameRaw, ok := m["Name"]; ok {
			// A {Name, Properties} block-state leaf. Resolve it.
			var name string
			if err := json.Unmarshal(nameRaw, &name); err == nil && name != "" {
				var bs blockStateJSON
				if err := json.Unmarshal(raw, &bs); err != nil {
					return fmt.Errorf("decoding block-state ref %q: %w", name, err)
				}
				sid, err := resolveBlockState(bs)
				if err != nil {
					return err
				}
				*out = append(*out, sid)
				return nil
			}
		}
		// Not a block-state leaf — descend each field.
		for _, v := range m {
			if err := walkBlockStates(v, out); err != nil {
				return err
			}
		}
		return nil
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return fmt.Errorf("descending array: %w", err)
		}
		for _, v := range arr {
			if err := walkBlockStates(v, out); err != nil {
				return err
			}
		}
		return nil
	default:
		// Scalar (string/number/bool/null) — nothing to resolve.
		return nil
	}
}

// truncate shortens a JSON snippet for error messages (mirrors density.truncate).
func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
