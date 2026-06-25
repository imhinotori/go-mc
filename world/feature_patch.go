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

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
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
	var j jsonSimpleBlockConfig
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &j); err != nil {
			panic(fmt.Errorf("world: simple_block config: %w", err))
		}
	}
	if len(j.ToPlace) == 0 {
		// No provider -> nothing to place (a malformed/empty config); jar would NPE,
		// here we no-op rather than crash a parallel decoration pass.
		return false
	}
	provider, err := feature.ParseProvider(j.ToPlace)
	if err != nil {
		panic(fmt.Errorf("world: simple_block to_place: %w", err))
	}

	// SimpleBlockFeature.place: state = toPlace.getOptionalState(level, rng, origin),
	// which is just getState (0 extra draws over GetState). The (x,y,z) feeds the noise
	// providers; simple/weighted ignore it.
	st := provider.GetState(rng, pos.X, pos.Y, pos.Z)
	if st == 0 {
		// getOptionalState null -> return false (a provider that yields no state).
		return false
	}

	// canSurvive gate (SimpleBlockFeature checks state.canSurvive(level, origin)).
	// Mid-worldgen the full BlockBehaviour.canSurvive state-shape is unavailable (12-01
	// precedent for would_survive/solid/replaceable), so this is a CONSERVATIVE port:
	// the placement requires (a) the origin itself is currently air/replaceable (we do
	// not overwrite solid terrain) AND (b) a solid support exists directly below — the
	// universal "a plant needs ground under it" rule that covers grass/flowers/mushrooms
	// /the FEAT-03 set. It NEVER places a floating block over air. A feature whose real
	// canSurvive is richer (e.g. waterlogged) is Phase-13 work; documented as
	// conservative, never a false keep.
	if !simpleBlockCanSurvive(bctx, st, pos) {
		return false
	}

	bctx.placeState(pos, st)
	return true
}

// simpleBlockCanSurvive is the conservative canSurvive gate (see simpleBlockBody). It
// keeps a placement iff the target cell is air/water (not overwriting solid terrain)
// and the block directly below is solid (a support). This is intentionally permissive
// enough to place the FEAT-03 ground cover yet never a floating block.
func simpleBlockCanSurvive(bctx *bodyContext, _ block.StateID, pos placement.BlockPos) bool {
	here := bctx.getState(pos)
	if !block.IsAir(here) {
		// Only place into air (the worldgen "empty block" the vanilla canSurvive +
		// setBlock(flag 2) effectively requires for ground cover). Overwriting solid
		// terrain mid-decoration is never correct for these leaves.
		return false
	}
	below := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	return !block.IsAir(below)
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
	var j jsonRandomPatchConfig
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &j); err != nil {
			panic(fmt.Errorf("world: random_patch config: %w", err))
		}
	}
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
