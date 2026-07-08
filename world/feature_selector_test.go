package world

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// These tests pin the composite-selector RECURSION (research Pitfall #9): each selector
// re-decodes its OPAQUE config from cf.Config.Raw, resolves nested sub-features through
// the plumbed registry (ref + inline), and places the jar-picked sub-feature via
// placeSubFeature with the rng threaded through the sub-feature's OWN modifiers. A wrong
// draw count/branch or a broken rng thread fails these (a forest would collapse to one
// type).
//
// The test leaf body reads the block-state resolved into its config (cf.Config.States[0]
// — walkBlockStates resolved the {Name,Properties} leaf at parse time) and places it at
// pos, so distinct sub-features place distinct, asserted blocks.
//
// It is registered under the recognized "no_op" feature type, which is a deliberate,
// SAFE choice: (a) "no_op" passes ParseConfiguredFeature's recognized-type gate (so an
// inline test leaf parses), (b) it is referenced by ZERO embedded configured_features (a
// no_op placed_feature with a body is never produced by the embed), and (c) for a REAL
// no_op config — which carries an EMPTY config and thus no resolved States — this body
// returns false (places nothing), exactly the semantics of the vanilla no_op feature. So
// the global registration is behaviorally a true no-op for production while giving the
// selector tests a deterministic, asserting leaf.
const testLeafType = "no_op"

func init() {
	registerFeatureBody(testLeafType, func(bctx *bodyContext, cf *feature.ConfiguredFeature, _ placement.PlacementContext, _ levelgen.RandomSource, pos placement.BlockPos) bool {
		if cf.Config == nil || len(cf.Config.States) == 0 {
			return false // a real no_op (empty config) -> places nothing, matching vanilla
		}
		return bctx.placeState(pos, cf.Config.States[0])
	})
}

// leafConfigJSON builds an inline {feature,placement} sub-feature object of the test leaf
// type whose config carries the given block-state name (so the leaf places that block).
// extraMods are extra placement modifiers (e.g. a count) to prove the sub-feature's OWN
// modifiers re-apply.
func leafConfigJSON(blockName string, placementMods string) json.RawMessage {
	cfg := fmt.Sprintf(`{"feature":{"type":"%s","config":{"to_place":{"Name":"%s"}}},"placement":[%s]}`,
		"minecraft:"+testLeafType, blockName, placementMods)
	return json.RawMessage(cfg)
}

func emptyNoOpJSON(placementMods string) json.RawMessage {
	cfg := fmt.Sprintf(`{"feature":{"type":"%s","config":{}},"placement":[%s]}`,
		"minecraft:"+testLeafType, placementMods)
	return json.RawMessage(cfg)
}

func weightedEntryJSON(weight int, placed json.RawMessage) string {
	return fmt.Sprintf(`{"data":%s,"weight":%d}`, placed, weight)
}

// newSelectorCF parses a configured_feature object (type + config) into a
// *ConfiguredFeature via the registry, the way buildDecorationData does, so the
// selector body re-decodes the SAME Raw it would in production.
func newSelectorCF(t *testing.T, reg *feature.Registry, selType string, configJSON string) *feature.ConfiguredFeature {
	t.Helper()
	obj := fmt.Sprintf(`{"type":"minecraft:%s","config":%s}`, selType, configJSON)
	cf, err := reg.ParseConfiguredFeature("minecraft:test_"+selType, json.RawMessage(obj))
	if err != nil {
		t.Fatalf("ParseConfiguredFeature(%s): %v", selType, err)
	}
	return cf
}

// runBody invokes a selector body directly with a fresh bodyContext (subDepth 0) over a
// 3x3 view, returning whether it placed + the view for block assertions.
func runBody(view *Neighborhood, reg *feature.Registry, cf *feature.ConfiguredFeature, rng levelgen.RandomSource, pos placement.BlockPos) bool {
	bctx := &bodyContext{view: view, reg: reg}
	body := lookupFeatureBody(cf.Type)
	ctx := newPlacementContext(view, -64, 384, nil)
	return body(bctx, cf, ctx, rng, pos)
}

func nextInSquarePos(rng levelgen.RandomSource, origin placement.BlockPos) placement.BlockPos {
	return placement.BlockPos{
		X: origin.X + int(rng.NextIntN(16)),
		Y: origin.Y,
		Z: origin.Z + int(rng.NextIntN(16)),
	}
}

// TestRandomSelectorWeightedPick: a 2-entry weighted selector over two distinct leaf
// sub-features. For a fixed seed, the EXACT sub-feature picked (the first whose
// nextFloat() < chance) lands its leaf block, and the per-entry NextFloat draw count is
// pinned via a parallel oracle source.
func TestRandomSelectorWeightedPick(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	pos := placement.BlockPos{X: 1, Y: 5, Z: 1}

	// features[0] chance 1.0 (always passes on the first draw -> cobblestone),
	// features[1] cobblestone-distinct. With chance 1.0 the FIRST entry always wins.
	cobble := "minecraft:cobblestone"
	diorite := "minecraft:diorite"
	cfg := fmt.Sprintf(`{"default":%s,"features":[{"chance":1.0,"feature":%s},{"chance":1.0,"feature":%s}]}`,
		leafConfigJSON("minecraft:stone", ""), leafConfigJSON(cobble, ""), leafConfigJSON(diorite, ""))
	cf := newSelectorCF(t, reg, "random_selector", cfg)

	// Oracle: a parallel source. random_selector draws nextFloat for entry 0; 0 <= f < 1
	// so f < 1.0 is true -> picks entry 0 (cobblestone). Exactly ONE NextFloat consumed.
	rng := levelgen.NewWorldgenRandom(777)
	oracle := levelgen.NewWorldgenRandom(777)
	_ = oracle.NextFloat() // one entry tried, passes

	if !runBody(view, reg, cf, rng, pos) {
		t.Fatalf("random_selector placed nothing")
	}
	want := block.ToStateID[block.Cobblestone{}]
	if got := view.GetBlock(pos.X, pos.Y, pos.Z); got != want {
		t.Fatalf("random_selector picked wrong sub-feature: got state %v, want cobblestone %v", got, want)
	}
	// Draw count: the body's rng must now match the oracle (one NextFloat consumed, leaf
	// has no modifiers and 0 body draws).
	if rng.NextFloat() != oracle.NextFloat() {
		t.Fatalf("random_selector consumed the wrong number of draws (expected exactly one NextFloat for the first passing entry)")
	}
}

// TestRandomSelectorFallsToDefault: a seed where NO weighted entry passes (chance 0.0) ->
// the default sub-feature is placed.
func TestRandomSelectorFallsToDefault(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	pos := placement.BlockPos{X: 2, Y: 5, Z: 2}

	// Both entries chance 0.0 -> nextFloat() (always >= 0) is never < 0.0 -> default.
	cfg := fmt.Sprintf(`{"default":%s,"features":[{"chance":0.0,"feature":%s},{"chance":0.0,"feature":%s}]}`,
		leafConfigJSON("minecraft:andesite", ""), leafConfigJSON("minecraft:cobblestone", ""), leafConfigJSON("minecraft:diorite", ""))
	cf := newSelectorCF(t, reg, "random_selector", cfg)

	rng := levelgen.NewWorldgenRandom(5)
	if !runBody(view, reg, cf, rng, pos) {
		t.Fatalf("random_selector default placed nothing")
	}
	want := block.ToStateID[block.Andesite{}]
	if got := view.GetBlock(pos.X, pos.Y, pos.Z); got != want {
		t.Fatalf("random_selector did not fall to default: got %v, want andesite %v", got, want)
	}
	// Two entries tried (both fail), so exactly TWO NextFloat consumed before the default.
	oracle := levelgen.NewWorldgenRandom(5)
	_ = oracle.NextFloat()
	_ = oracle.NextFloat()
	if rng.NextFloat() != oracle.NextFloat() {
		t.Fatalf("random_selector default path consumed wrong draw count (want two NextFloat for two failing entries)")
	}
}

// TestSequencePlacesSubFeaturesInOrder: sequence has no picker draw of its own; it
// places every sub-PlacedFeature in declared order and threads the same rng into each
// child's placement chain.
func TestSequencePlacesSubFeaturesInOrder(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	origin := placement.BlockPos{X: 0, Y: 5, Z: 0}
	inSquare := `{"type":"minecraft:in_square"}`
	cfg := fmt.Sprintf(`{"features":[%s,%s]}`,
		leafConfigJSON("minecraft:cobblestone", inSquare),
		leafConfigJSON("minecraft:diorite", inSquare))
	cf := newSelectorCF(t, reg, "sequence", cfg)

	seed := int64(0x51E0)
	var first, second placement.BlockPos
	var oracle *levelgen.WorldgenRandom
	for {
		oracle = levelgen.NewWorldgenRandom(seed)
		first = nextInSquarePos(oracle, origin)
		second = nextInSquarePos(oracle, origin)
		if first != second {
			break
		}
		seed++
	}

	rng := levelgen.NewWorldgenRandom(seed)
	if !runBody(view, reg, cf, rng, origin) {
		t.Fatalf("sequence placed nothing")
	}
	if got := view.GetBlock(first.X, first.Y, first.Z); got != block.ToStateID[block.Cobblestone{}] {
		t.Fatalf("sequence first child placed wrong state at %v: got %v, want cobblestone", first, got)
	}
	if got := view.GetBlock(second.X, second.Y, second.Z); got != block.ToStateID[block.Diorite{}] {
		t.Fatalf("sequence second child placed wrong state at %v: got %v, want diorite", second, got)
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("sequence consumed the wrong draw count/order for child placement modifiers")
	}
}

// TestSequenceStopsOnFirstFailedSubFeature: SequenceFeature.place returns false at
// the first child PlacedFeature that returns false, after that child's own modifiers
// have run, and does not place later children.
func TestSequenceStopsOnFirstFailedSubFeature(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	origin := placement.BlockPos{X: 2, Y: 5, Z: 2}
	inSquare := `{"type":"minecraft:in_square"}`
	cfg := fmt.Sprintf(`{"features":[%s,%s,%s]}`,
		leafConfigJSON("minecraft:cobblestone", ""),
		emptyNoOpJSON(inSquare),
		leafConfigJSON("minecraft:diorite", inSquare))
	cf := newSelectorCF(t, reg, "sequence", cfg)

	const seed = int64(0x5150)
	oracle := levelgen.NewWorldgenRandom(seed)
	_ = nextInSquarePos(oracle, origin) // failing no_op child still runs its placement
	future := levelgen.NewWorldgenRandom(seed)
	_ = nextInSquarePos(future, origin)
	thirdIfNotSkipped := nextInSquarePos(future, origin)

	rng := levelgen.NewWorldgenRandom(seed)
	if runBody(view, reg, cf, rng, origin) {
		t.Fatalf("sequence returned true after a failed child")
	}
	if got := view.GetBlock(origin.X, origin.Y, origin.Z); got != block.ToStateID[block.Cobblestone{}] {
		t.Fatalf("sequence first child did not place before failure: got %v, want cobblestone", got)
	}
	if got := view.GetBlock(thirdIfNotSkipped.X, thirdIfNotSkipped.Y, thirdIfNotSkipped.Z); got == block.ToStateID[block.Diorite{}] {
		t.Fatalf("sequence placed a child after the first failure at %v", thirdIfNotSkipped)
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("sequence short-circuit consumed the wrong draw count (wanted only the failing child's in_square draws)")
	}
}

// TestSimpleRandomSelectorUniform: simple_random_selector draws ONE nextInt(N) and places
// features[i]. The picked index is pinned by an oracle nextInt.
func TestSimpleRandomSelectorUniform(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	pos := placement.BlockPos{X: 3, Y: 5, Z: 3}

	names := []string{"minecraft:stone", "minecraft:cobblestone", "minecraft:diorite", "minecraft:andesite"}
	entries := make([]string, len(names))
	for i, n := range names {
		entries[i] = string(leafConfigJSON(n, ""))
	}
	cfg := fmt.Sprintf(`{"features":[%s,%s,%s,%s]}`, entries[0], entries[1], entries[2], entries[3])
	cf := newSelectorCF(t, reg, "simple_random_selector", cfg)

	const seed = int64(0xC0FFEE)
	rng := levelgen.NewWorldgenRandom(seed)
	oracle := levelgen.NewWorldgenRandom(seed)
	wantIdx := int(oracle.NextIntN(int32(len(names))))

	if !runBody(view, reg, cf, rng, pos) {
		t.Fatalf("simple_random_selector placed nothing")
	}
	wantState, err := blockStateOf(names[wantIdx])
	if err != nil {
		t.Fatal(err)
	}
	if got := view.GetBlock(pos.X, pos.Y, pos.Z); got != wantState {
		t.Fatalf("simple_random_selector picked wrong index: got %v want %s (idx %d)", got, names[wantIdx], wantIdx)
	}
	if rng.NextFloat() != oracle.NextFloat() {
		t.Fatalf("simple_random_selector consumed wrong draw count (want exactly one NextIntN)")
	}
}

// TestWeightedRandomSelectorPickAndModifierDrawOrder: weighted_random_selector draws
// one nextInt(totalWeight), maps the flat index through entries in order, then runs
// the selected sub-feature's own placement modifiers on the same rng.
func TestWeightedRandomSelectorPickAndModifierDrawOrder(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	origin := placement.BlockPos{X: 0, Y: 5, Z: 0}
	inSquare := `{"type":"minecraft:in_square"}`
	names := []string{"minecraft:stone", "minecraft:cobblestone", "minecraft:diorite", "minecraft:andesite"}
	weights := []int{0, 2, 3, 4}
	entries := make([]string, len(names))
	for i, name := range names {
		entries[i] = weightedEntryJSON(weights[i], leafConfigJSON(name, inSquare))
	}
	cfg := fmt.Sprintf(`{"features":[%s,%s,%s,%s]}`, entries[0], entries[1], entries[2], entries[3])
	cf := newSelectorCF(t, reg, "weighted_random_selector", cfg)

	const seed = int64(0x776)
	oracle := levelgen.NewWorldgenRandom(seed)
	total := 0
	for _, weight := range weights {
		total += weight
	}
	pick := int(oracle.NextIntN(int32(total)))
	wantIdx := -1
	remaining := pick
	for i, weight := range weights {
		remaining -= weight
		if remaining < 0 {
			wantIdx = i
			break
		}
	}
	wantPos := nextInSquarePos(oracle, origin)

	rng := levelgen.NewWorldgenRandom(seed)
	if !runBody(view, reg, cf, rng, origin) {
		t.Fatalf("weighted_random_selector placed nothing")
	}
	wantState, err := blockStateOf(names[wantIdx])
	if err != nil {
		t.Fatal(err)
	}
	if got := view.GetBlock(wantPos.X, wantPos.Y, wantPos.Z); got != wantState {
		t.Fatalf("weighted_random_selector picked/placed wrong entry: flat pick %d idx %d got %v at %v want %s", pick, wantIdx, got, wantPos, names[wantIdx])
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("weighted_random_selector consumed the wrong draw order (want pick draw before selected child's in_square draws)")
	}
}

// TestWeightedRandomSelectorZeroTotalConsumesNoDraw: WeightedList.getRandom returns
// Optional.empty without calling nextInt when the total weight is zero.
func TestWeightedRandomSelectorZeroTotalConsumesNoDraw(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	origin := placement.BlockPos{X: 3, Y: 5, Z: 3}
	cfg := fmt.Sprintf(`{"features":[%s,%s]}`,
		weightedEntryJSON(0, leafConfigJSON("minecraft:cobblestone", "")),
		weightedEntryJSON(0, leafConfigJSON("minecraft:diorite", "")))
	cf := newSelectorCF(t, reg, "weighted_random_selector", cfg)

	const seed = int64(0x600D)
	rng := levelgen.NewWorldgenRandom(seed)
	oracle := levelgen.NewWorldgenRandom(seed)
	if runBody(view, reg, cf, rng, origin) {
		t.Fatalf("weighted_random_selector with zero total weight placed")
	}
	if got := view.GetBlock(origin.X, origin.Y, origin.Z); got != block.ToStateID[block.Air{}] {
		t.Fatalf("weighted_random_selector zero-weight path wrote state %v", got)
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("weighted_random_selector zero total weight consumed a draw")
	}
}

// TestRandomBooleanSelector: random_boolean_selector draws nextBoolean() (next(1), ONE
// bit) and places feature_true on true / feature_false on false. The branch is pinned by
// an oracle NextBoolean.
func TestRandomBooleanSelector(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()

	cfg := fmt.Sprintf(`{"feature_true":%s,"feature_false":%s}`,
		leafConfigJSON("minecraft:cobblestone", ""), leafConfigJSON("minecraft:diorite", ""))
	cf := newSelectorCF(t, reg, "random_boolean_selector", cfg)

	for _, seed := range []int64{1, 2, 7, 42, 100, 1000} {
		view := build3x3([2]int{0, 0}, -64, 384)
		pos := placement.BlockPos{X: 4, Y: 5, Z: 4}
		rng := levelgen.NewWorldgenRandom(seed)
		oracle := levelgen.NewWorldgenRandom(seed)
		wantTrue := oracle.NextBoolean()

		if !runBody(view, reg, cf, rng, pos) {
			t.Fatalf("seed %d: random_boolean_selector placed nothing", seed)
		}
		var wantName string
		if wantTrue {
			wantName = "minecraft:cobblestone"
		} else {
			wantName = "minecraft:diorite"
		}
		wantState, err := blockStateOf(wantName)
		if err != nil {
			t.Fatal(err)
		}
		if got := view.GetBlock(pos.X, pos.Y, pos.Z); got != wantState {
			t.Fatalf("seed %d: random_boolean_selector wrong branch: got %v want %s (nextBoolean=%v)", seed, got, wantName, wantTrue)
		}
		// One bit (next(1)) consumed.
		if rng.NextFloat() != oracle.NextFloat() {
			t.Fatalf("seed %d: random_boolean_selector consumed wrong draw count (want one next(1))", seed)
		}
	}
}

// TestSubFeatureModifiersReapply: a sub-feature with a count modifier proves the
// sub-feature's OWN draws happen (the recursion threads the selector rng through the
// sub-feature's modifier chain — Pitfall #9, T-12-09). A count(3) consumes the
// IntProvider draw; a parallel oracle that runs the same fold confirms the draw count.
func TestSubFeatureModifiersReapply(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	pos := placement.BlockPos{X: 5, Y: 5, Z: 5}

	// The single weighted entry (chance 1.0) is a leaf with a count(2) modifier. The
	// leaf body places at each produced anchor; count(2) makes it run TWICE. The
	// constant IntProvider draws 0, so the only draw is the selector's one NextFloat.
	countMod := `{"type":"minecraft:count","count":2}`
	cfg := fmt.Sprintf(`{"default":%s,"features":[{"chance":1.0,"feature":%s}]}`,
		leafConfigJSON("minecraft:stone", ""), leafConfigJSON("minecraft:cobblestone", countMod))
	cf := newSelectorCF(t, reg, "random_selector", cfg)

	rng := levelgen.NewWorldgenRandom(321)
	if !runBody(view, reg, cf, rng, pos) {
		t.Fatalf("selector with count-modified sub-feature placed nothing")
	}
	// Both count copies land at the same pos (count is a pure repeat, no offset), so the
	// block is present. The KEY assertion is the draw count: selector NextFloat (1) +
	// count's constant IntProvider (0 draws) = exactly one draw.
	oracle := levelgen.NewWorldgenRandom(321)
	_ = oracle.NextFloat() // selector entry 0 passes
	// count=constant(2) -> 0 draws; the leaf body draws 0.
	if rng.NextFloat() != oracle.NextFloat() {
		t.Fatalf("sub-feature modifier re-apply consumed wrong draw count: the rng did not thread through the sub-feature's modifiers as expected")
	}
	if got := view.GetBlock(pos.X, pos.Y, pos.Z); got != block.ToStateID[block.Cobblestone{}] {
		t.Fatalf("count-modified sub-feature did not place its leaf block")
	}
}

// TestSubFeatureModifiersDrawThread: a sub-feature with a UNIFORM count provider proves
// the sub-feature's modifier ACTUALLY draws (and advances the shared rng). If the rng
// were forked/reset for the sub-feature (broken thread), the post-run fingerprint would
// diverge from an oracle that consumes the same draws in order.
func TestSubFeatureModifiersDrawThread(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	pos := placement.BlockPos{X: 6, Y: 5, Z: 6}

	// count = uniform[1,3] -> ONE NextIntN draw inside the sub-feature's modifier fold.
	countMod := `{"type":"minecraft:count","count":{"type":"minecraft:uniform","min_inclusive":1,"max_inclusive":3}}`
	cfg := fmt.Sprintf(`{"default":%s,"features":[{"chance":1.0,"feature":%s}]}`,
		leafConfigJSON("minecraft:stone", ""), leafConfigJSON("minecraft:cobblestone", countMod))
	cf := newSelectorCF(t, reg, "random_selector", cfg)

	rng := levelgen.NewWorldgenRandom(999)
	runBody(view, reg, cf, rng, pos)

	// Oracle consumes: selector NextFloat (1) + uniform count NextIntN(3) (1). The leaf
	// body draws 0 per anchor. If the sub-feature's modifier did NOT thread the rng, this
	// fingerprint would differ.
	oracle := levelgen.NewWorldgenRandom(999)
	_ = oracle.NextFloat()
	_ = oracle.NextIntN(3) // uniform[1,3] = 1 + nextInt(3)
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("the sub-feature's count modifier did not advance the SHARED rng (broken Pitfall-#9 thread)")
	}
}

// TestNestedSelectorTerminates: a selector whose sub-feature is ANOTHER selector recurses
// and terminates, threading one rng. inner is a boolean selector over two leaves; outer is
// a random_selector whose chance-1.0 entry is the inner selector.
func TestNestedSelectorTerminates(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	view := build3x3([2]int{0, 0}, -64, 384)
	pos := placement.BlockPos{X: 7, Y: 5, Z: 7}

	innerCfg := fmt.Sprintf(`{"type":"minecraft:random_boolean_selector","config":{"feature_true":%s,"feature_false":%s}}`,
		leafConfigJSON("minecraft:cobblestone", ""), leafConfigJSON("minecraft:diorite", ""))
	inner := fmt.Sprintf(`{"feature":%s,"placement":[]}`, innerCfg)
	outerCfg := fmt.Sprintf(`{"default":%s,"features":[{"chance":1.0,"feature":%s}]}`,
		leafConfigJSON("minecraft:stone", ""), inner)
	cf := newSelectorCF(t, reg, "random_selector", outerCfg)

	const seed = int64(0xBEEF)
	rng := levelgen.NewWorldgenRandom(seed)
	if !runBody(view, reg, cf, rng, pos) {
		t.Fatalf("nested selector placed nothing (did it terminate?)")
	}
	// Oracle: outer NextFloat (entry passes) -> inner NextBoolean -> branch leaf.
	oracle := levelgen.NewWorldgenRandom(seed)
	_ = oracle.NextFloat()
	wantTrue := oracle.NextBoolean()
	var wantName string
	if wantTrue {
		wantName = "minecraft:cobblestone"
	} else {
		wantName = "minecraft:diorite"
	}
	wantState, _ := blockStateOf(wantName)
	if got := view.GetBlock(pos.X, pos.Y, pos.Z); got != wantState {
		t.Fatalf("nested selector wrong leaf: got %v want %s", got, wantName)
	}
	// One outer NextFloat + one inner next(1); fingerprint after must match the oracle.
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("nested selector consumed the wrong draw count across the recursion")
	}
}

// TestInlineVsRefSubFeature: both an inline {feature,placement} entry AND a ref-string
// entry resolve + place. The ref points at a placed_feature this test injects into the
// registry by parsing it under an id first (so ResolvePlaced finds it cached).
func TestInlineVsRefSubFeature(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()

	// Inline entry: chance 1.0 -> always first. Assert it resolves (the inline path).
	view1 := build3x3([2]int{0, 0}, -64, 384)
	pos := placement.BlockPos{X: 8, Y: 5, Z: 8}
	inlineCfg := fmt.Sprintf(`{"default":%s,"features":[{"chance":1.0,"feature":%s}]}`,
		leafConfigJSON("minecraft:stone", ""), leafConfigJSON("minecraft:cobblestone", ""))
	cfInline := newSelectorCF(t, reg, "random_selector", inlineCfg)
	if !runBody(view1, reg, cfInline, levelgen.NewWorldgenRandom(11), pos) {
		t.Fatalf("inline sub-feature placed nothing")
	}
	if view1.GetBlock(pos.X, pos.Y, pos.Z) != block.ToStateID[block.Cobblestone{}] {
		t.Fatalf("inline sub-feature placed the wrong block")
	}

	// Ref entry: a registry over a custom DataSource that serves a test placed_feature by
	// id. The selector references it by STRING, proving the ResolvePlaced (ref-string)
	// path resolves the OPAQUE ref through the plumbed registry.
	const refID = "minecraft:test_ref_leaf"
	refPF := string(leafConfigJSON("minecraft:diorite", ""))
	refReg := feature.NewRegistry(feature.NewFuncSource(
		data.ConfiguredFeatureJSON,
		func(id string) ([]byte, error) {
			if id == refID {
				return []byte(refPF), nil
			}
			return data.PlacedFeatureJSON(id)
		},
	))
	view2 := build3x3([2]int{0, 0}, -64, 384)
	refCfg := fmt.Sprintf(`{"default":%s,"features":[{"chance":1.0,"feature":"%s"}]}`,
		leafConfigJSON("minecraft:stone", ""), refID)
	cfRef := newSelectorCF(t, refReg, "random_selector", refCfg)
	if !runBody(view2, refReg, cfRef, levelgen.NewWorldgenRandom(11), pos) {
		t.Fatalf("ref sub-feature placed nothing")
	}
	if view2.GetBlock(pos.X, pos.Y, pos.Z) != block.ToStateID[block.Diorite{}] {
		t.Fatalf("ref sub-feature placed the wrong block")
	}
}

// blockStateOf resolves a "minecraft:foo" block id to its default StateID for assertions.
func blockStateOf(name string) (block.StateID, error) {
	switch name {
	case "minecraft:stone":
		return block.ToStateID[block.Stone{}], nil
	case "minecraft:cobblestone":
		return block.ToStateID[block.Cobblestone{}], nil
	case "minecraft:diorite":
		return block.ToStateID[block.Diorite{}], nil
	case "minecraft:andesite":
		return block.ToStateID[block.Andesite{}], nil
	default:
		return 0, fmt.Errorf("test: unknown block %q", name)
	}
}
