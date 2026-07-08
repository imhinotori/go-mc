package world

// feature_patch.go ports the SimpleBlockFeature ("simple_block") and RandomPatchFeature
// ("random_patch") bodies — the ground-cover leaf + the vegetation-scatter workhorse.
// Both register under their namespace-stripped type via registerFeatureBody from init().
//
// SimpleBlockFeature (JAR-CONFIRMED net.minecraft.world.level.levelgen.feature.
// SimpleBlockFeature.place, javap -c against temp/cache/26.2-inner.jar): it reads the
// to_place BlockStateProvider, draws ONE state at the origin (via 12-01's
// feature.ParseProvider + GetState), checks canSurvive (ported conservatively — see
// below), then writes through the Neighborhood. It is the leaf of most vegetation.
//
// RandomPatchFeature (random_patch): 26.2 removed the top-level RandomPatchFeature class
// (overworld vegetation now routes through simple_block + the random_offset placement
// modifier 12-01 bound), so there is NO RandomPatchFeature.class in the 26.2 jar to
// javap. The parser (12-01) still recognizes "random_patch", so the body is ported here
// for completeness + correctness from the STABLE, version-invariant RandomPatchFeature.
// place algorithm (unchanged since 1.18; the same constant-for-constant transcription
// 12-01 used for the absent TrapezoidalInt). The jar-faithful draw order is, per try:
// x = nextInt(xz+1) - nextInt(xz+1), y = nextInt(y+1) - nextInt(y+1),
// z = nextInt(xz+1) - nextInt(xz+1) (6 nextInt draws), then the inner PlacedFeature is
// placed at the jittered anchor (its own placement modifiers re-apply). Since 26.2 has
// ZERO top-level random_patch in the embed, the tests construct a synthetic config.
//
// The inner feature is routed through placeSubFeature (the shared sub-feature placement
// helper built alongside in feature_selector.go): an inner PlacedFeature's OWN modifier
// chain re-applies with the threaded rng (the same recursion the selectors use). For a
// typical random_patch the inner placement list is empty, so this reduces to a direct
// inner-body call at the jittered pos — but routing through the shared helper keeps the
// rng-threading + sub-feature semantics unified across 12-02/12-03 (no divergent path).
//
// Both bodies write ONLY through bctx.placeState / view.SetBlock (the 3x3 proxy,
// heightmap-live, cross-chunk aware), so a patch near a chunk edge spills into the
// neighbor (T-12-08).

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("simple_block", simpleBlockBody)
	registerFeatureBody("random_patch", randomPatchBody)
}

// ---- SimpleBlockFeature ----

// jsonSimpleBlockConfig is the SimpleBlockConfiguration JSON shape (verified
// brown_mushroom.json): {to_place: <provider>, schedule_ticks?: bool}.
type jsonSimpleBlockConfig struct {
	ToPlace       json.RawMessage `json:"to_place"`
	ScheduleTicks bool            `json:"schedule_ticks"`
}

// simpleBlockCache memoizes the decoded SimpleBlockConfiguration (the parsed JSON + the
// resolved provider) per ConfiguredFeature. simpleBlockBody runs once per placement (a
// chunk places many, and the 3x3 decoration neighborhood multiplies it), so the per-call
// json.Unmarshal + ParseProvider was re-parsing the SAME immutable config bytes on every
// placement. Keying by the *ConfiguredFeature pointer matches feature_ore.go's
// oreConfigCache exactly: the parser DAG returns one stable instance per feature id
// (registry dedup) and its config is immutable after parse, so the cached value is shared.
// A sync.Map keeps it -race clean regardless of caller. Byte-identical output (the decode
// is pure over the config bytes; providers themselves are stateless w.r.t. the cache).
var simpleBlockCache sync.Map // map[*feature.ConfiguredFeature]*simpleBlockDecoded

// simpleBlockDecoded is the memoized SimpleBlock config: the parsed provider (nil when the
// config carried no to_place — a no-op body). decodeErr defers a parse failure to the body
// so the panic (a build-data error) surfaces exactly where the uncached path panicked.
type simpleBlockDecoded struct {
	provider feature.BlockStateProvider
	err      error
}

// decodeSimpleBlockCached returns the memoized decode for cf, parsing (and caching) it on
// first use. A nil provider with a nil error means "no to_place -> no-op" (the same skip
// the uncached body took). The decode is pure over the config bytes, so the value is shared.
func decodeSimpleBlockCached(cf *feature.ConfiguredFeature) *simpleBlockDecoded {
	if v, ok := simpleBlockCache.Load(cf); ok {
		return v.(*simpleBlockDecoded)
	}
	d := &simpleBlockDecoded{}
	var j jsonSimpleBlockConfig
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &j); err != nil {
			d.err = fmt.Errorf("world: simple_block config: %w", err)
			simpleBlockCache.Store(cf, d)
			return d
		}
	}
	if len(j.ToPlace) == 0 {
		// No provider -> nothing to place (a malformed/empty config); jar would NPE, here we
		// no-op (nil provider, nil err) rather than crash a parallel decoration pass.
		simpleBlockCache.Store(cf, d)
		return d
	}
	provider, err := feature.ParseProvider(j.ToPlace)
	if err != nil {
		d.err = fmt.Errorf("world: simple_block to_place: %w", err)
		simpleBlockCache.Store(cf, d)
		return d
	}
	d.provider = provider
	simpleBlockCache.Store(cf, d)
	return d
}

// simpleBlockBody ports SimpleBlockFeature.place: decode to_place, draw the provider
// state at the origin, canSurvive-gate it (conservative), then SetBlock. Returns true
// iff it wrote a block.
func simpleBlockBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	d := decodeSimpleBlockCached(cf)
	if d.err != nil {
		// A config/provider decode failure is a build-data error; panic exactly where the
		// uncached body did (the cache only defers the SAME error to the first placement).
		panic(d.err)
	}
	if d.provider == nil {
		// No provider (no/empty to_place) -> nothing to place; the uncached no-op.
		return false
	}
	provider := d.provider

	// SimpleBlockFeature.place: state = toPlace.getOptionalState(level, rng, origin),
	// which is just getState (0 extra draws over GetState). The (x,y,z) feeds the noise
	// providers; simple/weighted ignore it.
	st := provider.GetState(rng, pos.X, pos.Y, pos.Z)
	if st == 0 {
		// getOptionalState null -> return false (a provider that yields no state).
		return false
	}

	// canSurvive gate (SimpleBlockFeature.place checks state.canSurvive(level, origin)
	// at bytecode offset 47 and returns false at 160 if it fails — javap-confirmed,
	// net.minecraft.world.level.levelgen.feature.SimpleBlockFeature, 26.2-inner.jar).
	// For the overworld vegetation set (grass/flowers/...) the to-place state's block is
	// a VegetationBlock, whose canSurvive (VegetationBlock.canSurvive(state, level, pos))
	// reads the block BELOW (pos.below()) and returns mayPlaceOn(belowState), and
	// mayPlaceOn (VegetationBlock.mayPlaceOn) is exactly
	//   belowState.is(BlockTags.SUPPORTS_VEGETATION).
	// That sustaining-tag check is what rejects WATER below (BUG B: grass on water) AND
	// a plant below (BUG A: flower on flower) — neither is in #supports_vegetation. See
	// simpleBlockCanSurvive for the 1:1 port + the air-at-origin half of the gate.
	if !simpleBlockCanSurvive(bctx, st, pos) {
		return false
	}

	// DoublePlantBlock special case (SimpleBlockFeature.place, bytecode offset 53-91,
	// javap -c net.minecraft.world.level.levelgen.feature.SimpleBlockFeature, 26.2-inner.jar):
	// if the to-place block is a DoublePlantBlock (sunflower/lilac/rose_bush/peony/tall_grass/
	// large_fern), the feature places BOTH halves via DoublePlantBlock.placeAt -- but ONLY when
	// the cell ABOVE the origin is empty; otherwise it places NOTHING and returns false. Placing
	// only the drawn (single) state left the plant half-formed (a lone LOWER whose partner is
	// missing), which renders broken and is deleted by DoublePlantBlock.updateShape the moment a
	// neighbor update touches it. DoublePlantBlock.placeAt writes LOWER at pos and UPPER at
	// pos.above, each via setBlock(pos, state, flags) with flags == 2 (iconst_2 at bytecode
	// offset 83 -> BLOCK_UPDATE, no observer/neighbor shape update) -- the worldgen SetBlock
	// used here is exactly that raw write, so the pair lands atomically and survives updateShape
	// (each half's partner is present with the matching half).
	if block.IsDoublePlant(st) {
		above := placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
		if !block.IsAir(bctx.getState(above)) {
			// level.isEmptyBlock(origin.above()) is false -> place nothing, return false
			// (bytecode offset 90-91).
			return false
		}
		// DoublePlantBlock.placeAt: LOWER at pos, UPPER at pos.above (both with the drawn
		// block's other properties; these blocks carry only HALF, so the pair is fully
		// determined by the two halves).
		lower, okL := block.DoublePlantWithHalf(st, false)
		upper, okU := block.DoublePlantWithHalf(st, true)
		if !okL || !okU {
			// Unreachable for a real DoublePlantBlock state (both halves are registered); never
			// place a malformed single half if the state table were ever inconsistent.
			return false
		}
		bctx.placeState(pos, lower)
		bctx.placeState(above, upper)
		return true
	}

	bctx.placeState(pos, st)
	return true
}

// simpleBlockCanSurvive is the canSurvive gate SimpleBlockFeature.place applies before
// writing a plant. It is a 1:1 port of the vanilla path for the overworld vegetation
// set (javap, 26.2-inner.jar):
//
//	SimpleBlockFeature.place  -> state.canSurvive(level, origin)
//	BlockStateBase.canSurvive -> Block.canSurvive(state, level, pos)
//	VegetationBlock.canSurvive(state, level, pos):
//	    BlockPos below = pos.below();
//	    return mayPlaceOn(level.getBlockState(below), level, below);
//	VegetationBlock.mayPlaceOn(state, getter, pos):
//	    return state.is(BlockTags.SUPPORTS_VEGETATION);
//
// So the keep condition is exactly: the block directly BELOW the origin is in the
// #minecraft:supports_vegetation tag (resolved authoritatively from the embedded jar
// tag JSONs via data.BlockTag — the same mechanism the 17-12 tree fix used for
// #replaceable_by_trees, never hand-transcribed). WATER and an existing PLANT are NOT
// in that tag, so both BUG A (flower-on-flower) and BUG B (grass-on-water) are
// rejected here.
//
// The additional air-at-origin guard is the faithful counterpart of the vanilla
// vegetation PIPELINE: the random_patch / simple_block placed_features each run a
// block_predicate_filter with a matching_block_tag #minecraft:air predicate at the
// origin BEFORE the inner feature places (see world/levelgen/placement/predicate.go),
// so a non-air origin (e.g. an already-placed plant within the same patch) never
// reaches a write. Keeping it here makes the leaf body self-consistent for the
// synthetic/direct call paths (tests, random_patch inner) that do not route through a
// filter. It is the air half of "BUG A" — a second flower cannot replace the first.
func simpleBlockCanSurvive(bctx *bodyContext, _ block.StateID, pos placement.BlockPos) bool {
	here := bctx.getState(pos)
	if !block.IsAir(here) {
		// The origin must be empty: vanilla's #air predicate gate, and the reason a
		// second plant never stacks on the first (BUG A).
		return false
	}
	below := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	// mayPlaceOn: belowState.is(#minecraft:supports_vegetation). Water/plants are not in
	// the tag, so grass-on-water (BUG B) and the flower-below case are rejected.
	return supportsVegetation(below)
}

// supportsVegetationSet is the lazily-resolved StateID set of every block in
// #minecraft:supports_vegetation (dirt/coarse_dirt/rooted_dirt + mud/muddy_mangrove_roots
// + moss_block/pale_moss_block + grass_block/podzol/mycelium + farmland). It is built
// once from data.BlockTag("supports_vegetation"), which recursively expands the nested
// jar tag chain (#substrate_overworld -> #dirt/#mud/#moss_blocks/#grass_blocks) — so the
// membership is the jar's, authoritative and never hand-listed.
var (
	supportsVegetationOnce sync.Once
	supportsVegetationSet  map[block.StateID]bool
)

// supportsVegetation reports whether st is a block whose id is in
// #minecraft:supports_vegetation — the VegetationBlock.mayPlaceOn predicate. Air is
// never in the tag, so it also rejects a floating placement over air.
func supportsVegetation(st block.StateID) bool {
	supportsVegetationOnce.Do(func() {
		ids, err := data.BlockTag("supports_vegetation")
		if err != nil {
			// The tag chain is embedded (tools/extract_worldgen.go); a failure here is a
			// build/extraction regression, not a runtime condition — fail loudly rather
			// than silently degrading the survival gate back to the old buggy behaviour.
			panic(fmt.Errorf("world: resolving #minecraft:supports_vegetation: %w", err))
		}
		set := make(map[block.StateID]bool)
		for sid, b := range block.StateList {
			if ids[b.ID()] {
				set[block.StateID(sid)] = true
			}
		}
		supportsVegetationSet = set
	})
	return supportsVegetationSet[st]
}

// ---- RandomPatchFeature ----

// jsonRandomPatchConfig is the RandomPatchConfiguration JSON shape (stable since 1.18):
// {tries, xz_spread, y_spread, feature: <placed_feature ref or inline>}.
type jsonRandomPatchConfig struct {
	Tries    int             `json:"tries"`
	XZSpread int             `json:"xz_spread"`
	YSpread  int             `json:"y_spread"`
	Feature  json.RawMessage `json:"feature"`
}

// randomPatchCache memoizes the parsed RandomPatchConfiguration per ConfiguredFeature so
// randomPatchBody parses the immutable config bytes ONCE instead of on every placement
// (oreConfigCache pattern; keyed by the stable *ConfiguredFeature the parser DAG dedups).
// Only the JSON parse is cached — the inner sub-feature resolve depends on the per-view
// bctx and stays in the body. A sync.Map keeps it -race clean; byte-identical output.
var randomPatchCache sync.Map // map[*feature.ConfiguredFeature]*randomPatchDecoded

// randomPatchDecoded is the memoized parsed RandomPatchConfiguration (the four fields) plus
// a deferred parse error surfaced by the body (so the panic lands where the uncached body's
// did).
type randomPatchDecoded struct {
	cfg jsonRandomPatchConfig
	err error
}

// decodeRandomPatchCached returns the memoized parse for cf, decoding (and caching) it on
// first use. The parse is pure over the config bytes, so the cached value is shared.
func decodeRandomPatchCached(cf *feature.ConfiguredFeature) *randomPatchDecoded {
	if v, ok := randomPatchCache.Load(cf); ok {
		return v.(*randomPatchDecoded)
	}
	d := &randomPatchDecoded{}
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &d.cfg); err != nil {
			d.err = fmt.Errorf("world: random_patch config: %w", err)
		}
	}
	randomPatchCache.Store(cf, d)
	return d
}

// randomPatchBody ports RandomPatchFeature.place (stable algorithm; the class is absent
// from the 26.2 jar — see the file header). For each of `tries`, it jitters the origin
// by the symmetric two-draw form per axis (x, then y, then z — 6 nextInt draws) and
// places the inner PlacedFeature at the jittered anchor via placeSubFeature (the inner
// feature's modifiers re-apply on the threaded rng). Returns true iff >=1 inner place
// succeeded.
func randomPatchBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	d := decodeRandomPatchCached(cf)
	if d.err != nil {
		panic(d.err)
	}
	j := d.cfg
	if j.Tries <= 0 || len(j.Feature) == 0 {
		return false
	}

	// Resolve the inner placed_feature ONCE (a ref string or inline object), via the
	// shared sub-feature resolver (DAG-deduped + cycle-guarded). Resolved before the
	// loop so a resolve failure surfaces immediately and the loop's draws are not wasted.
	inner, err := resolveSubFeature(bctx, j.Feature)
	if err != nil {
		panic(fmt.Errorf("world: random_patch inner feature: %w", err))
	}

	xz := j.XZSpread + 1
	yspread := j.YSpread + 1
	placed := 0
	for t := 0; t < j.Tries; t++ {
		// RandomPatchFeature jitter (jar-faithful draw order: x, y, z; each a symmetric
		// nextInt(spread+1) - nextInt(spread+1)). The x draws happen before the y draws
		// before the z draws — the determinism contract.
		dx := int(rng.NextIntN(int32(xz))) - int(rng.NextIntN(int32(xz)))
		dy := int(rng.NextIntN(int32(yspread))) - int(rng.NextIntN(int32(yspread)))
		dz := int(rng.NextIntN(int32(xz))) - int(rng.NextIntN(int32(xz)))
		at := placement.BlockPos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}

		// Place the inner PlacedFeature at the jittered anchor; its OWN placement
		// modifiers re-apply with the threaded rng (placeSubFeature, depth from bctx).
		if placeSubFeature(bctx, inner, bctx.subDepth+1, ctx, rng, at) {
			placed++
		}
	}
	return placed > 0
}
