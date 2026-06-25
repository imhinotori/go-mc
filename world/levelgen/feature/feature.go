// Package feature ports the DATA + parse half of the Minecraft 26.2 (protocol 776)
// feature/decoration pipeline (FEAT-02). It defines the typed AST for the
// ConfiguredFeature / PlacedFeature / PlacementModifier layering (all JAR-CONFIRMED
// java.lang.Records) and a polymorphic "type"-tagged JSON parser — modeled EXACTLY
// on world/levelgen/density's registry-dispatch decoder (Registry.Parse +
// parseObject + stripNS + the cache/parsing cycle guard).
//
// PHASE 11 IS ORCHESTRATION, NOT FEATURE BODIES. This package PARSES every
// configured_feature (226) + placed_feature (262) into a typed-but-body-deferred
// AST: it records the feature TYPE and captures its config (resolving block-state
// refs to block.StateID now), but the actual Feature.place body is Phase 12+'s job.
// A KNOWN feature type (the full vanilla roster, derived from the distinct "type"
// values across the 226 entries) parses without erroring; a GENUINELY unknown type
// errors LOUDLY naming it (the density-parser end_islands precedent — never a silent
// mis-evaluation, T-11-01).
//
// IT IS ITS OWN PACKAGE (import-cycle discipline, mirroring density/surface/carver):
// a `package levelgen` file could import it without a cycle. It MUST NOT import
// world/levelgen/placement (plan 11-02) — the placement-modifier list is captured
// here as a raw typed envelope (PlacementModifierRaw) that 11-02 binds to real
// modifier bodies, keeping 11-01 and 11-02 file- and package-disjoint and acyclic.
package feature

import (
	"encoding/json"

	"github.com/imhinotori/sulfur/level/block"
)

// ConfiguredFeature is the "WHAT to build and with what params" half (JAR:
// ConfiguredFeature<FC,F> = (Feature<FC> feature, FC config)). For Phase 11 it is
// typed-but-body-deferred: Type names the vanilla feature (e.g. "tree", "ore",
// "random_patch") and Config carries the captured/parsed config envelope. The
// Feature.place body that consumes Config is Phase 12+'s job.
type ConfiguredFeature struct {
	// ID is the registry id this configured_feature was loaded from
	// (e.g. "minecraft:oak"); empty for an inline/anonymous config.
	ID string
	// Type is the vanilla feature type, namespace-stripped (e.g. "tree", "ore").
	Type string
	// Config is the deferred config capture for this feature type. It is the raw
	// JSON of the "config" object plus any block-state references resolved during
	// the parse (see ParsedConfig). Phase 12+ binds it to the real Feature body.
	Config *ParsedConfig
}

// PlacedFeature is the "WHERE to build" half (JAR: PlacedFeature = (Holder<
// ConfiguredFeature> feature, List<PlacementModifier> placement)). The configured
// ref is resolved (cached/DAG-deduped) to its *ConfiguredFeature at parse time; the
// placement modifier list is captured as a raw typed list plan 11-02 binds to real
// PlacementModifier bodies.
type PlacedFeature struct {
	// ID is the registry id this placed_feature was loaded from
	// (e.g. "minecraft:oak"); empty for an inline ref.
	ID string
	// FeatureRef is the configured_feature registry id this placed_feature wraps
	// (the raw "feature" string ref, e.g. "minecraft:oak"); empty when the
	// "feature" field was an inline configured_feature object.
	FeatureRef string
	// Feature is the resolved configured_feature (cached/DAG-deduped via the
	// Registry). Non-nil after a successful Resolve.
	Feature *ConfiguredFeature
	// Placement is the ordered placement-modifier list, captured raw for plan
	// 11-02 to bind to real modifier bodies. The ORDER is the determinism contract
	// (the rng-draw sequence), so it is preserved verbatim.
	Placement []PlacementModifierRaw
}

// PlacementModifierRaw is the typed-but-unbound envelope for one placement modifier
// (JAR: PlacementModifier — count / in_square / heightmap / rarity_filter / ...).
// Phase 11 captures the type + the raw body; plan 11-02 binds it to the real
// PlacementModifier.getPositions implementation. Keeping it raw here is what makes
// 11-01 and 11-02 package-disjoint (this package must not import placement).
type PlacementModifierRaw struct {
	// Type is the modifier type, namespace-stripped (e.g. "count", "in_square").
	Type string
	// Raw is the full modifier object JSON (including "type"), for 11-02 to decode
	// into the bound modifier body.
	Raw json.RawMessage
}

// ParsedConfig is the typed-but-body-deferred capture of a feature's "config"
// object. Phase 11 captures the raw config JSON and resolves any block-state
// references it carries to block.StateID (so a parsed config already holds resolved
// state ids, not raw strings — the place() body in Phase 12+ consumes those). The
// provider/inner-feature bodies (BlockStateProvider.getState, sub-feature recursion)
// stay deferred.
type ParsedConfig struct {
	// Raw is the verbatim "config" object JSON, retained for Phase 12+ to decode
	// the full type-specific config.
	Raw json.RawMessage
	// States are the block-state references found in this config, resolved to
	// StateIDs at parse time (FEAT-02: "a parsed config carries resolved
	// block.StateID values, not raw strings"). A nil/empty slice means the config
	// had no bare {Name,Properties} block-state refs to resolve.
	States []block.StateID
}
