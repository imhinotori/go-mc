package world

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// This file establishes the configured-feature body DISPATCH seam Phase 12+ fills:
// a registry of featureBody functions keyed by the parsed feature type, plus the
// bodyContext that carries the shared deps a body needs — the 3x3 write proxy AND the
// *feature.Registry (so a selector body in 12-03 can resolve a nested sub-PlacedFeature
// ref via reg.ResolvePlaced / reg.ParsePlacedFeature and recurse).
//
// Plans 12-02 (ore + simple_block/random_patch) and 12-03 (selectors + pile/fallen/
// vegetation_patch) register their bodies from SEPARATE files via registerFeatureBody
// in init() — NO edit to a shared switch — so they stay file-disjoint and run in
// parallel. An unregistered real type stays a recordable no-op (the tree/dungeon types
// Phase 13 owns); the test_set_block path + the invocation recorder are preserved
// (see placement_context.go newConfiguredPlacer, which consults this registry).
//
// Source mapping: this is the Go realization of ConfiguredFeature.place dispatch —
// vanilla resolves the Feature singleton from the configured feature's type and calls
// feature.place(FeaturePlaceContext). Here featureBodies[type] is that resolution.

// featureBody is one configured-feature place() body. It receives the shared
// bodyContext (the 3x3 view + the registry), the parsed configured feature, the REAL
// PlacementContext + threaded rng (NOT discarded — the body's draws continue the
// deterministic sequence), and the anchor position. It returns true iff it placed at
// least one block (matching ConfiguredFeaturePlacer.place semantics).
type featureBody func(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool

// bodyContext carries the shared dependencies a feature body (especially 12-03's
// selector recursion) needs that are NOT in its config: the 3x3 write proxy and the
// *feature.Registry it resolves nested sub-features through.
type bodyContext struct {
	// view is the 3x3 read/write proxy the body places blocks through.
	view *Neighborhood
	// reg is the live feature registry a selector/pile body resolves a nested
	// sub-PlacedFeature ref on (reg.ResolvePlaced / reg.ParsePlacedFeature). It is
	// g.deco.registry in production (threaded through makePlacer → newConfiguredPlacer).
	reg *feature.Registry
}

// featureBodies is the type→body registry. It is populated at init() time by the
// wave-2 body files (12-02/12-03) via registerFeatureBody, before any generator runs,
// so it is effectively immutable during decoration (no concurrent write — read-only on
// the single scheduler goroutine, matching the rest of the decoration path).
var featureBodies = map[string]featureBody{}

// registerFeatureBody registers a body for a namespace-stripped feature type. The
// wave-2 plans call it from init() in their own files. A duplicate registration for
// the same type panics loudly (a programming error — two plans must not own one type).
func registerFeatureBody(t string, b featureBody) {
	if _, dup := featureBodies[t]; dup {
		panic("world: duplicate featureBody registration for type " + t)
	}
	featureBodies[t] = b
}

// lookupFeatureBody returns the registered body for a feature type (nil if none — the
// recordable no-op path). Exposed (lower-case, package-internal) so newConfiguredPlacer
// in placement_context.go dispatches through it.
func lookupFeatureBody(t string) featureBody { return featureBodies[t] }

// placeState is the SetBlock wrapper a body uses to write one block through the 3x3
// view (keeping the live worldgen heightmaps current via Neighborhood.SetBlock). It
// is a thin shared helper genuinely used by BOTH wave-2 plans' bodies; body-specific
// placement logic stays in their own files. It is a no-op (returns false) when the
// view is nil (a unit test with no neighborhood).
func (b *bodyContext) placeState(pos placement.BlockPos, st block.StateID) bool {
	if b.view == nil {
		return false
	}
	b.view.SetBlock(pos.X, pos.Y, pos.Z, st)
	return true
}

// getState reads the live block at pos through the 3x3 view (air outside / nil view).
// A shared helper the rule-based provider's existing-block reads + the patch bodies
// consume. It returns air (StateID 0) when the view is nil.
func (b *bodyContext) getState(pos placement.BlockPos) block.StateID {
	if b.view == nil {
		return 0
	}
	return b.view.GetBlock(pos.X, pos.Y, pos.Z)
}
