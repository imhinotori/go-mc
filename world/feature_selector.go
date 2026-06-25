package world

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// This file ports the COMPOSITE selector features (FEAT-03 criterion 3, research
// Pitfall #9): random_selector / simple_random_selector / random_boolean_selector.
// Each picks a nested sub-PlacedFeature and runs it — the sub-feature's OWN placement
// modifiers re-apply with the SAME threaded rng (so a biome's weighted feature choice,
// forest = mostly oak + some birch, threads one deterministic rng end-to-end). Get the
// recursion or the rng-threading wrong and every forest collapses to a single type.
//
// THE REVISED PREMISE (11-01 verified): a selector's nested sub-features are NOT
// pre-parsed. walkBlockStates (parse.go) resolved only the {Name,Properties} block-state
// LEAVES; the selector's config.features[].feature / config.default.feature /
// feature_true / feature_false sit OPAQUE in cf.Config.Raw. So each selector body
// RE-DECODES its config from cf.Config.Raw, then resolves each sub-entry's "feature"
// through the registry plumbed by 12-01 (bctx.reg): a ref STRING via reg.ResolvePlaced,
// an inline {feature,placement} OBJECT via reg.ParsePlacedFeature("", raw). This is the
// exact dependency that made 12-01's *feature.Registry plumbing into bodyContext
// mandatory — without it the recursion has nothing to resolve refs on.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.RandomSelectorFeature.place
//   - net.minecraft.world.level.levelgen.feature.SimpleRandomSelectorFeature.place
//   - net.minecraft.world.level.levelgen.feature.RandomBooleanSelectorFeature.place
//   - WeightedPlacedFeature.place / PlacedFeature.place (the sub-feature fold)

// maxSubFeatureDepth bounds the selector→selector recursion as a sanity backstop. The
// build-trusted feature graph is acyclic (the registry's 11-01 cycle guard rejects any
// placed→configured→placed loop on resolve, so a real cycle errors at resolution, not
// here), and vanilla nests selectors only a few levels deep. This depth cap is a
// belt-and-suspenders guard against a pathological (resolve-passing) chain — T-12-11.
const maxSubFeatureDepth = 64

// The selector recursion depth is threaded through bodyContext.subDepth (see
// feature_body.go): the featureBody signature is fixed (12-01), so placeSubFeature sets
// bctx.subDepth around the bound sub-feature Place and restores it, letting a selector
// nested inside a selector see the accumulated depth.

// placeSubFeature is the recursion seam (the load-bearing link, T-12-09). Given a
// resolved sub-*PlacedFeature, it binds the sub-feature's placement modifier chain via
// placement.Bind — the EXACT fold 11-03/applyBiomeDecoration uses per feature — with a
// configured placer that dispatches the sub-feature's configured body through the
// registry (subFeaturePlacer), then runs bound.Place(ctx, rng, pos). The rng IS the
// threaded selector rng, so the sub-feature's modifier draws AND its body draws continue
// the deterministic sequence (Pitfall #9 satisfied). A selector → weighted sub-feature →
// simple_block resolves end-to-end with one rng.
//
// deps.BiomeAllowed is left permissive (nil): the outer biome filter already gated the
// selector at the anchor, and a selector's empty-placement sub-features (the common case)
// carry no biome modifier of their own; a sub-feature that DOES list a biome modifier
// binds it with the permissive default, matching vanilla where the selector's sub-feature
// PlacedFeature.place runs its own modifier list without re-deriving the outer feature's
// biome allowance (sub-features are not registered top-level features, so BiomeFilter's
// allowed-set defaults to permissive — confirmed: the selector is the registered feature,
// its sub-features inherit the placement they declare).
func placeSubFeature(
	bctx *bodyContext,
	sub *feature.PlacedFeature,
	depth int,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	if sub == nil || sub.Feature == nil {
		return false
	}
	if depth > maxSubFeatureDepth {
		// Acyclic-by-construction guard backstop (T-12-11): a build-data bug, not a
		// runtime input. Stop recursing rather than overflow the stack.
		return false
	}
	placer := subFeaturePlacer(bctx, sub.Feature, depth)
	bound, err := placement.Bind(sub, placer, placement.ModifierDeps{})
	if err != nil {
		// A sub-feature whose modifier chain cannot bind (e.g. an as-yet-unported
		// Nether/cave modifier like environment_scan that a selector sub-feature lists)
		// is SKIPPED defensively — the SAME convention applyBiomeDecoration uses for a
		// top-level unbindable feature (decoration.go: "skip rather than crash
		// mid-decoration"). The selector's RECURSION + rng-threading is the contract this
		// plan owns; an unported leaf-placement is Phase-13/deferred-modifier work, not a
		// reason to crash the whole generator. The draws already consumed by the selector
		// (the pick) stand, matching vanilla's "the sub-feature ran but placed nothing".
		return false
	}
	return bound.Place(ctx, rng, pos)
}

// subFeaturePlacer builds the configured-feature placer for a sub-feature: it dispatches
// on the sub-feature's configured type to a registered featureBody (the same registry
// newConfiguredPlacer consults), so a selector picking a simple_block/ore/random_patch
// runs that leaf body with the threaded rng. depth+1 is captured so a selector body
// reached via this placer continues the recursion-depth accounting. An unregistered type
// (a tree/dungeon body Phase 13 owns, or a 12-02 leaf not yet landed mid-parallel-run) is
// a no-op — the selector's RECURSION + rng-threading is what this plan tests, not the
// leaf body.
func subFeaturePlacer(bctx *bodyContext, cf *feature.ConfiguredFeature, depth int) placement.PlacerFunc {
	return func(ctx placement.PlacementContext, rng levelgen.RandomSource, pos placement.BlockPos) bool {
		if cf == nil {
			return false
		}
		if body := lookupFeatureBody(cf.Type); body != nil {
			// A selector body reached here recurses; thread depth+1 via the bodyContext
			// so the nested call's placeSubFeature sees the accumulated depth.
			prev := bctx.subDepth
			bctx.subDepth = depth + 1
			res := body(bctx, cf, ctx, rng, pos)
			bctx.subDepth = prev
			return res
		}
		return false
	}
}

// ---- config decode shapes ----

// weightedFeatureRaw is one random_selector features[] entry: {chance, feature} where
// "feature" is a ref string OR an inline {feature,placement} object (both JAR-valid).
type weightedFeatureRaw struct {
	Chance  float32         `json:"chance"`
	Feature json.RawMessage `json:"feature"`
}

// randomSelectorRaw is the RandomFeatureConfiguration: an ordered features list +
// a default. (trees_plains.json: {"default":{...},"features":[{"chance":..,"feature":..}]}).
type randomSelectorRaw struct {
	Default  json.RawMessage      `json:"default"`
	Features []weightedFeatureRaw `json:"features"`
}

// simpleSelectorRaw is the CompositeFeatureConfiguration: a uniform features list
// (dripleaf.json: {"features":[{feature,placement}, ...]}).
type simpleSelectorRaw struct {
	Features []json.RawMessage `json:"features"`
}

// booleanSelectorRaw is the RandomBooleanFeatureConfiguration: feature_true /
// feature_false (lush_caves_clay.json). Each is a ref string or an inline object.
type booleanSelectorRaw struct {
	FeatureTrue  json.RawMessage `json:"feature_true"`
	FeatureFalse json.RawMessage `json:"feature_false"`
}

// resolveSubFeature resolves one "feature" sub-entry to a *PlacedFeature: a ref STRING
// via bctx.reg.ResolvePlaced (DAG-deduped + cycle-guarded), an inline {feature,placement}
// OBJECT via bctx.reg.ParsePlacedFeature("", raw). This is where the OPAQUE nested
// sub-feature (which 11-01 left in Raw) becomes a real placed_feature the recursion runs.
func resolveSubFeature(bctx *bodyContext, raw json.RawMessage) (*feature.PlacedFeature, error) {
	if bctx.reg == nil {
		return nil, fmt.Errorf("world: selector sub-feature resolve with no registry")
	}
	trimmed := jsonFirstByte(raw)
	switch trimmed {
	case '"':
		var ref string
		if err := json.Unmarshal(raw, &ref); err != nil {
			return nil, fmt.Errorf("world: selector sub-feature ref: %w", err)
		}
		return bctx.reg.ResolvePlaced(ref)
	case '{':
		return bctx.reg.ParsePlacedFeature("", raw)
	default:
		return nil, fmt.Errorf("world: selector sub-feature is neither a ref nor an object")
	}
}

// jsonFirstByte returns the first non-whitespace byte of a JSON value (0 if empty).
func jsonFirstByte(raw json.RawMessage) byte {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return b
		}
	}
	return 0
}

// ---- random_selector ----

// randomSelectorBody ports RandomSelectorFeature.place (javap -c): iterate the weighted
// features in ORDER; for each, draw ONE nextFloat() — if it is < that entry's chance,
// place that sub-feature and RETURN (one NextFloat per entry tried, in order); if no
// entry passes, place the default. The picked sub-feature's own modifiers re-apply via
// placeSubFeature with the threaded rng.
func randomSelectorBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	var sel randomSelectorRaw
	if err := json.Unmarshal(cf.Config.Raw, &sel); err != nil {
		panic(fmt.Sprintf("world: random_selector config %q: %v", cf.ID, err))
	}
	depth := bctx.subDepth
	// One NextFloat per entry, in order, until one passes (< chance).
	for _, wp := range sel.Features {
		if rng.NextFloat() < wp.chanceOf() {
			return resolveAndPlaceSub(bctx, wp.Feature, depth, ctx, rng, pos)
		}
	}
	// None passed -> the default feature.
	if len(sel.Default) == 0 {
		return false
	}
	return resolveAndPlaceSub(bctx, sel.Default, depth, ctx, rng, pos)
}

// resolveAndPlaceSub resolves a selector sub-entry's "feature" (ref or inline) through
// the registry and places it via the recursion seam. A resolve failure is SKIPPED
// defensively (return false) — the SAME "skip rather than crash mid-decoration"
// convention applyBiomeDecoration uses (decoration.go); the selector's pick draw has
// already happened, so the selector's own determinism is preserved while an unresolvable
// sub-feature (a genuine build-data gap caught at construction by LoadAll) no-ops rather
// than crashing the live generator.
func resolveAndPlaceSub(bctx *bodyContext, raw json.RawMessage, depth int, ctx placement.PlacementContext, rng levelgen.RandomSource, pos placement.BlockPos) bool {
	sub, err := resolveSubFeature(bctx, raw)
	if err != nil {
		return false
	}
	return placeSubFeature(bctx, sub, depth, ctx, rng, pos)
}

// chanceOf returns the entry's chance (a tiny helper so the loop reads as the bytecode:
// nextFloat() < wp.chance).
func (w weightedFeatureRaw) chanceOf() float32 { return w.Chance }

// ---- simple_random_selector ----

// simpleRandomSelectorBody ports SimpleRandomSelectorFeature.place (javap -c): draw ONE
// nextInt(features.size()) and place features[i]. Uniform pick over N sub-features; the
// picked sub-feature's modifiers re-apply via placeSubFeature.
func simpleRandomSelectorBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	var sel simpleSelectorRaw
	if err := json.Unmarshal(cf.Config.Raw, &sel); err != nil {
		panic(fmt.Sprintf("world: simple_random_selector config %q: %v", cf.ID, err))
	}
	n := len(sel.Features)
	if n == 0 {
		return false
	}
	i := int(rng.NextIntN(int32(n)))
	return resolveAndPlaceSub(bctx, sel.Features[i], bctx.subDepth, ctx, rng, pos)
}

// ---- random_boolean_selector ----

// randomBooleanSelectorBody ports RandomBooleanSelectorFeature.place (javap -c): draw
// nextBoolean() (next(1), ONE bit — the 12-01 LCG primitive, NOT NextIntN(2)) and place
// feature_true on true, feature_false on false. The chosen sub-feature's modifiers
// re-apply via placeSubFeature.
func randomBooleanSelectorBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	var sel booleanSelectorRaw
	if err := json.Unmarshal(cf.Config.Raw, &sel); err != nil {
		panic(fmt.Sprintf("world: random_boolean_selector config %q: %v", cf.ID, err))
	}
	// nextBoolean is LegacyRandomSource-only (the decoration rng is always a
	// *WorldgenRandom embedding it); type-assert at the call site (12-01's interface-
	// membership choice — random.go is not edited here, keeping wave-2 file-disjoint).
	bs, ok := rng.(booleanSource)
	if !ok {
		panic("world: random_boolean_selector requires a LegacyRandomSource-backed rng (NextBoolean)")
	}
	var raw json.RawMessage
	if bs.NextBoolean() {
		raw = sel.FeatureTrue
	} else {
		raw = sel.FeatureFalse
	}
	if len(raw) == 0 {
		return false
	}
	return resolveAndPlaceSub(bctx, raw, bctx.subDepth, ctx, rng, pos)
}

// booleanSource is the LegacyRandomSource subset random_boolean_selector needs beyond
// the RandomSource interface (NextBoolean = next(1)). The decoration rng (*WorldgenRandom)
// embeds *LegacyRandomSource, which provides it; a non-legacy rng (a test xoroshiro)
// fails the type assertion loudly — random_boolean_selector must not run off the legacy
// chain (its draw is bit-exact only there). Mirrors placement.gaussianSource.
type booleanSource interface {
	NextBoolean() bool
}

// init registers the three composite selectors. Disjoint from 12-02's body files; a
// duplicate registration panics (12-01's contract).
func init() {
	registerFeatureBody("random_selector", randomSelectorBody)
	registerFeatureBody("simple_random_selector", simpleRandomSelectorBody)
	registerFeatureBody("random_boolean_selector", randomBooleanSelectorBody)
}
