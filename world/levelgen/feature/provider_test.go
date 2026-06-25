package feature

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// --- LCG primitive oracles (hand-traced java.util.Random, seeds 42/12345) ---

// TestNextBooleanOneBit pins NextBoolean = next(1) != 0 against a hand-traced
// java.util.Random.nextBoolean sequence for seed 42 (the ONE-bit draw, NOT
// NextIntN(2) which would be a different bit extract over a wider draw).
func TestNextBooleanOneBit(t *testing.T) {
	r := levelgen.NewLegacyRandomSource(42)
	// Oracle (computed from the LCG: next(1) != 0 for seed 42).
	want := []bool{true, false, true, false, false, true, false, true, true, false, true, false}
	for i, w := range want {
		if got := r.NextBoolean(); got != w {
			t.Fatalf("NextBoolean draw %d: got %v want %v", i, got, w)
		}
	}

	// NextBoolean draws exactly ONE next() per call (next(1)): a parallel source that
	// instead does ConsumeCount(1) advances the LCG identically, so a fingerprint draw
	// after N booleans matches N ConsumeCount(1) advances. This pins the SINGLE-draw
	// width (a wrong-width primitive — e.g. drawing next(31) twice — would desync).
	a := levelgen.NewLegacyRandomSource(123)
	b := levelgen.NewLegacyRandomSource(123)
	for i := 0; i < 8; i++ {
		_ = a.NextBoolean()
		b.ConsumeCount(1)
	}
	if a.NextInt() != b.NextInt() {
		t.Fatalf("NextBoolean did not consume exactly one next() draw per call")
	}
}

// TestNextGaussianTwoDrawCache pins NextGaussian (the Marsaglia two-draw polar
// method with the cached second value): the first call draws + caches, the second
// returns the cache with ZERO new draws, the third draws fresh again. Values are
// hand-traced for seed 12345.
func TestNextGaussianTwoDrawCache(t *testing.T) {
	r := levelgen.NewLegacyRandomSource(12345)
	g1 := r.NextGaussian()
	g2 := r.NextGaussian() // the cached second normal: 0 new draws
	g3 := r.NextGaussian()

	const (
		wantG1 = -0.18780898965891199
		wantG2 = 0.58843630511547962
		wantG3 = 0.94880478044004257
	)
	if !approx(g1, wantG1) || !approx(g2, wantG2) || !approx(g3, wantG3) {
		t.Fatalf("NextGaussian sequence: got (%.17g, %.17g, %.17g) want (%.17g, %.17g, %.17g)",
			g1, g2, g3, wantG1, wantG2, wantG3)
	}

	// The cached call (g2) must consume ZERO draws: a parallel source that does the
	// same first call has identical post-g1 state, and after BOTH take their second
	// call the states must STILL match (g2 drew nothing).
	a := levelgen.NewLegacyRandomSource(12345)
	b := levelgen.NewLegacyRandomSource(12345)
	_ = a.NextGaussian()
	_ = b.NextGaussian()
	// Draw a raw int from a to fingerprint its state, then have b take its cached g2
	// FIRST and draw the same fingerprint — if g2 drew nothing the fingerprints match.
	_ = b.NextGaussian() // cached, should not advance the LCG
	fa := a.NextInt()
	fb := b.NextInt()
	if fa != fb {
		t.Fatalf("cached NextGaussian advanced the LCG: fingerprint %d != %d", fa, fb)
	}
}

func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-12
}

// --- BlockStateProvider hierarchy ---

func stateOf(t *testing.T, b block.Block) block.StateID {
	t.Helper()
	sid, ok := block.ToStateID[b]
	if !ok {
		t.Fatalf("no state id for %T", b)
	}
	return sid
}

// TestSimpleProviderNoDraw: SimpleStateProvider returns its single state with ZERO
// rng draws (the rng state is untouched across GetState).
func TestSimpleProviderNoDraw(t *testing.T) {
	p := SimpleStateProvider{state: stateOf(t, block.Stone{})}
	// Two sources at the SAME state: one calls GetState (must not draw), the other
	// does not. After, a fingerprint draw from each must match iff GetState drew 0.
	withCall := levelgen.NewLegacyRandomSource(1)
	noCall := levelgen.NewLegacyRandomSource(1)
	got := p.GetState(withCall, 3, 4, 5)
	if got != stateOf(t, block.Stone{}) {
		t.Fatalf("SimpleStateProvider returned %v want stone", got)
	}
	if withCall.NextInt() != noCall.NextInt() {
		t.Fatalf("SimpleStateProvider consumed an rng draw")
	}
}

// TestWeightedProviderOneDraw: WeightedStateProvider draws exactly once and picks by
// the cumulative-weight walk in entry order. For seed 7 the pick index is 1 (poppy
// has weight 2 covering indices 0..1), and the post-draw rng state is pinned.
func TestWeightedProviderOneDraw(t *testing.T) {
	poppy := stateOf(t, block.Poppy{})
	dandelion := stateOf(t, block.Dandelion{})
	p := WeightedStateProvider{
		entries: []weightedStateEntry{
			{state: poppy, weight: 2},
			{state: dandelion, weight: 1},
		},
		totalWeight: 3,
	}
	r := levelgen.NewLegacyRandomSource(7)
	got := p.GetState(r, 0, 0, 0)
	// Oracle: nextInt(3) for seed 7 = 1 -> cumulative walk: 1-2=-1 <0 -> poppy.
	if got != poppy {
		t.Fatalf("WeightedStateProvider picked %v want poppy (pick index 1)", got)
	}
	// Post-draw state pinned: exactly ONE NextIntN(3) was consumed.
	oracle := levelgen.NewLegacyRandomSource(7)
	_ = oracle.NextIntN(3)
	if r.NextInt() != oracle.NextInt() {
		t.Fatalf("WeightedStateProvider did not consume exactly one NextIntN draw")
	}
}

// TestRuleBasedFallback: with no matching rule the fallback provider is used; with a
// matching rule (existing block in the tag) the rule's provider wins.
func TestRuleBasedFallback(t *testing.T) {
	dirt := stateOf(t, block.Dirt{})
	stone := stateOf(t, block.Stone{})
	rooted := stateOf(t, block.RootedDirt{})

	// Rule: if existing block is in #dirt -> rooted_dirt; fallback -> stone.
	dirtSet, err := resolveBlockTagSet("minecraft:dirt")
	if err != nil {
		t.Fatalf("resolveBlockTagSet: %v", err)
	}
	p := RuleBasedStateProvider{
		fallback: SimpleStateProvider{state: stone},
		rules: []stateRule{
			{matches: func(s block.StateID) bool { return dirtSet[s] }, then: SimpleStateProvider{state: rooted}},
		},
	}

	r := levelgen.NewLegacyRandomSource(1)
	// No existingAt -> rules cannot match -> fallback.
	if got := p.GetState(r, 0, 0, 0); got != stone {
		t.Fatalf("rule-based with no existing reader: got %v want fallback stone", got)
	}
	// existing = dirt -> rule matches -> rooted_dirt.
	pd := p.withExisting(func(_, _, _ int) block.StateID { return dirt })
	if got := pd.GetState(r, 0, 0, 0); got != rooted {
		t.Fatalf("rule-based existing=dirt: got %v want rooted_dirt", got)
	}
	// existing = stone (not in #dirt) -> fallback.
	ps := p.withExisting(func(_, _, _ int) block.StateID { return stone })
	if got := ps.GetState(r, 0, 0, 0); got != stone {
		t.Fatalf("rule-based existing=stone: got %v want fallback stone", got)
	}
}

// TestNoiseProviderDeterministic: NoiseProvider.GetState is positional (0 rng draws)
// and deterministic — the same position yields the same state across calls and does
// not consume the rng.
func TestNoiseProviderDeterministic(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"minecraft:noise_provider",
		"noise":{"amplitudes":[1.0],"firstOctave":0},
		"scale":0.0208,
		"seed":2345,
		"states":[{"Name":"minecraft:dandelion"},{"Name":"minecraft:poppy"},{"Name":"minecraft:allium"}]
	}`)
	p, err := ParseProvider(raw)
	if err != nil {
		t.Fatalf("ParseProvider noise: %v", err)
	}
	withCall := levelgen.NewLegacyRandomSource(99)
	noCall := levelgen.NewLegacyRandomSource(99)
	a := p.GetState(withCall, 10, 0, 20)
	b := p.GetState(withCall, 10, 0, 20)
	if a != b {
		t.Fatalf("NoiseProvider not deterministic at fixed pos: %v != %v", a, b)
	}
	if withCall.NextInt() != noCall.NextInt() {
		t.Fatalf("NoiseProvider consumed an rng draw (must be positional)")
	}
	// A different position should be allowed to differ (sanity: at least exercise it).
	_ = p.GetState(withCall, 1000, 0, 2000)
}

// TestParseProviderUnknownErrors: an unported provider type errors LOUDLY naming it.
func TestParseProviderUnknownErrors(t *testing.T) {
	raw := json.RawMessage(`{"type":"minecraft:noise_threshold_provider"}`)
	if _, err := ParseProvider(raw); err == nil {
		t.Fatalf("ParseProvider accepted an unported type silently")
	}
}

// TestParseRealWeightedProvider parses the REAL embedded flower_default to_place
// (a weighted poppy(2)/dandelion(1) provider) and confirms the pick + weights.
func TestParseRealWeightedProvider(t *testing.T) {
	raw, err := readConfigToPlace(t, "flower_default")
	if err != nil {
		t.Fatalf("read flower_default to_place: %v", err)
	}
	p, err := ParseProvider(raw)
	if err != nil {
		t.Fatalf("ParseProvider flower_default to_place: %v", err)
	}
	wp, ok := p.(WeightedStateProvider)
	if !ok {
		t.Fatalf("flower_default to_place parsed as %T, want WeightedStateProvider", p)
	}
	if wp.totalWeight != 3 || len(wp.entries) != 2 {
		t.Fatalf("flower_default weights: total=%d entries=%d want total=3 entries=2", wp.totalWeight, len(wp.entries))
	}
	// Entry order = determinism: poppy first (weight 2), dandelion second (weight 1).
	if wp.entries[0].state != stateOf(t, block.Poppy{}) || wp.entries[1].state != stateOf(t, block.Dandelion{}) {
		t.Fatalf("flower_default entry order/states wrong")
	}
}

// TestRandomizedIntStateProvider pins the 12-01-deferred randomized_int_state_provider (now
// ported): it draws the source state, then randomizes the configured int property
// (mangrove_propagule `age` via uniform{0,4}). The two-stage draw order + the exact resolved
// age are the determinism contract (T-13-15).
func TestRandomizedIntStateProvider(t *testing.T) {
	raw := `{"type":"minecraft:randomized_int_state_provider","property":"age","source":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:mangrove_propagule","Properties":{"age":"0","hanging":"true","stage":"0","waterlogged":"false"}}},"values":{"type":"minecraft:uniform","min_inclusive":0,"max_inclusive":4}}`
	p, err := ParseProvider(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("ParseProvider(randomized_int_state_provider): %v", err)
	}
	const seed = int64(0xA6E0)
	rng := levelgen.NewWorldgenRandom(seed)
	got := p.GetState(rng, 0, 0, 0)

	// Oracle: the source draws 0 (simple), then values.sample = nextInt(5). The result is the
	// mangrove_propagule with that age.
	oracle := levelgen.NewWorldgenRandom(seed)
	wantAge := int(oracle.NextIntN(5)) // the source simple provider draws nothing
	wantState, err := resolveBlockState(blockStateJSON{
		Name:       "minecraft:mangrove_propagule",
		Properties: map[string]string{"age": intToString(wantAge), "hanging": "true", "stage": "0", "waterlogged": "false"},
	})
	if err != nil {
		t.Fatalf("resolve oracle propagule: %v", err)
	}
	if got != wantState {
		t.Fatalf("randomized_int_state_provider state = %v, want propagule age %d (%v)", got, wantAge, wantState)
	}
	// The result must be a mangrove_propagule (the source block, re-resolved with the new age).
	if b := block.StateList[got]; b == nil || b.ID() != "minecraft:mangrove_propagule" {
		t.Fatalf("randomized_int_state_provider yielded %v, not a mangrove_propagule", got)
	}
	// Determinism: a re-run yields the identical state.
	if p.GetState(levelgen.NewWorldgenRandom(seed), 0, 0, 0) != got {
		t.Fatalf("randomized_int_state_provider non-deterministic")
	}
}

// readConfigToPlace pulls a configured_feature's config.to_place raw JSON from the
// embedded registry data source (the real 26.2 embed).
func readConfigToPlace(t *testing.T, id string) (json.RawMessage, error) {
	t.Helper()
	reg := NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:" + id)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		ToPlace json.RawMessage `json:"to_place"`
	}
	if err := json.Unmarshal(cf.Config.Raw, &cfg); err != nil {
		return nil, err
	}
	return cfg.ToPlace, nil
}
