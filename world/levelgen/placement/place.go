package placement

import (
	"fmt"

	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
)

// This file ports PlacedFeature.placeWithContext: the single-threaded ordered
// flatMap FOLD over the modifier chain that threads ONE WorldgenRandom through every
// modifier (in stream order) then calls the configured-feature body at each resulting
// anchor. The fold order IS the determinism contract (research Pitfall 4, T-11-04):
// it is an explicit sequential loop — NO goroutines, NO map iteration, NO reordering.
//
// Source (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.placement.PlacedFeature.placeWithContext

// ConfiguredFeaturePlacer is the configured-feature body the fold runs at each anchor
// position. 11-02 defines only the SIGNATURE; the real dispatch on the parsed feature
// type is 11-03's, and the type-specific place() bodies are Phase 12's. place returns
// true iff a block was actually placed at pos. The SAME threaded rng is passed, in
// stream order, so a placer body's draws continue the deterministic sequence.
type ConfiguredFeaturePlacer interface {
	place(ctx PlacementContext, rng levelgen.RandomSource, pos BlockPos) bool
}

// PlacerFunc adapts an exported function to the (unexported-method)
// ConfiguredFeaturePlacer interface so a DIFFERENT package (11-03's world package) can
// supply the configured-feature body. The fold calls place() at each anchor; this
// forwards to f. It is the cross-package seam: ConfiguredFeaturePlacer's method is
// unexported (so only this package may implement the interface directly), and PlacerFunc
// is the sanctioned exported bridge for the real dispatch + Phase-12 bodies that live
// outside this package.
type PlacerFunc func(ctx PlacementContext, rng levelgen.RandomSource, pos BlockPos) bool

// place satisfies ConfiguredFeaturePlacer by forwarding to the wrapped function.
func (f PlacerFunc) place(ctx PlacementContext, rng levelgen.RandomSource, pos BlockPos) bool {
	return f(ctx, rng, pos)
}

// compile-time assertion: PlacerFunc is a ConfiguredFeaturePlacer.
var _ ConfiguredFeaturePlacer = PlacerFunc(nil)

// BoundPlacedFeature is a placed_feature with its modifier chain bound to real
// PlacementModifier bodies + its configured feature bound to a placer. It is the
// runtime form 11-03's applyBiomeDecoration calls place() on per feature (after
// WorldgenRandom.SetFeatureSeed).
type BoundPlacedFeature struct {
	// Placement is the ordered, bound modifier chain. ORDER is load-bearing.
	Placement []PlacementModifier
	// Configured is the configured-feature body run at each produced anchor.
	Configured ConfiguredFeaturePlacer
}

// place ports PlacedFeature.placeWithContext: fold the modifier chain over the
// origin to produce the anchor positions, then run the configured feature at each.
// ONE rng is threaded through every modifier AND the body, in stream order.
//
// Determinism contract (T-11-04): the outer loop walks pf.Placement in order; the
// inner loop flatMaps each surviving position SEQUENTIALLY (so a count -> in_square
// chain consumes the rng as: count's draws, then per-copy the in_square 2 draws).
// Never parallelize or reorder either loop.
func (pf *BoundPlacedFeature) place(ctx PlacementContext, rng levelgen.RandomSource, origin BlockPos) bool {
	positions := []BlockPos{origin}
	for _, mod := range pf.Placement {
		var next []BlockPos
		for _, p := range positions {
			next = append(next, mod.getPositions(ctx, rng, p)...)
		}
		positions = next
	}

	placed := false
	for _, p := range positions {
		if pf.Configured != nil && pf.Configured.place(ctx, rng, p) {
			placed = true
		}
	}
	return placed
}

// Place is the exported entry point 11-03 calls per feature. It runs the ordered
// fold (see place) and reports whether the configured feature placed at >=1 anchor.
func (pf *BoundPlacedFeature) Place(ctx PlacementContext, rng levelgen.RandomSource, origin BlockPos) bool {
	return pf.place(ctx, rng, origin)
}

// Bind turns a parsed 11-01 *feature.PlacedFeature (the ordered []PlacementModifierRaw
// + the resolved configured ref) into a BoundPlacedFeature by binding each modifier
// via BindModifier. This is the seam where 11-01's parsed data meets 11-02's modifier
// bodies; it imports world/levelgen/feature (acyclic — feature does NOT import
// placement). The configured placer is supplied by the caller (11-03 builds the real
// dispatch; Phase 12 fills the bodies), since 11-02 has no feature-body logic.
//
// deps carries the per-feature bind-time dependencies the modifier JSON lacks (the
// biome filter's allowed-biome predicate). An unported modifier type fails loudly
// here (T-11-05) — a placed_feature with any unbindable modifier is rejected whole,
// never silently degraded.
func Bind(pf *feature.PlacedFeature, configured ConfiguredFeaturePlacer, deps ModifierDeps) (*BoundPlacedFeature, error) {
	if pf == nil {
		return nil, fmt.Errorf("placement: Bind got a nil placed_feature")
	}
	bound := &BoundPlacedFeature{Configured: configured}
	for i, raw := range pf.Placement {
		// PlacementModifierRaw.Type is namespace-stripped; Raw is the full object.
		mod, err := BindModifier(raw.Type, raw.Raw, deps)
		if err != nil {
			return nil, fmt.Errorf("placement: bind %q modifier %d/%d: %w", pf.ID, i+1, len(pf.Placement), err)
		}
		bound.Placement = append(bound.Placement, mod)
	}
	return bound, nil
}
