package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// TestRegisterAndDispatch: a body registered for a synthetic type is invoked by
// newConfiguredPlacer with the bodyContext + the REAL ctx + rng + cf at the right pos,
// and its return is the placer's return. The body's rng draws advance the threaded rng
// (the determinism chain continues).
func TestRegisterAndDispatch(t *testing.T) {
	const synthType = "test_dispatch_synthetic"
	var (
		gotPos   placement.BlockPos
		gotCF    *feature.ConfiguredFeature
		gotRegOK bool
		drewN    int32
	)
	reg := feature.NewEmbeddedRegistry()
	registerFeatureBody(synthType, func(bctx *bodyContext, cf *feature.ConfiguredFeature, _ placement.PlacementContext, rng levelgen.RandomSource, pos placement.BlockPos) bool {
		gotPos = pos
		gotCF = cf
		gotRegOK = bctx.reg == reg
		drewN = rng.NextInt() // consume a draw to prove the rng is the real threaded one
		return true
	})
	t.Cleanup(func() { delete(featureBodies, synthType) })

	cf := &feature.ConfiguredFeature{ID: "minecraft:synthetic", Type: synthType}
	view := build3x3([2]int{0, 0}, -64, 384)
	placer := newConfiguredPlacer(cf, view, reg, block.StateID(0), false, nil, 63, 0)

	ctx := newPlacementContext(view, -64, 384, nil)
	rng := levelgen.NewLegacyRandomSource(99)
	pos := placement.BlockPos{X: 7, Y: 11, Z: 13}
	got := placer(ctx, rng, pos)

	if !got {
		t.Fatalf("registered body's true return was not propagated")
	}
	if gotPos != pos {
		t.Fatalf("body got pos %v, want %v", gotPos, pos)
	}
	if gotCF != cf {
		t.Fatalf("body did not receive the configured feature")
	}
	if !gotRegOK {
		t.Fatalf("bodyContext.reg was not the registry passed to newConfiguredPlacer")
	}
	// The body's NextInt must equal the first draw of a fresh seed-99 source — proving
	// the REAL threaded rng was passed (not a discarded copy).
	if drewN != levelgen.NewLegacyRandomSource(99).NextInt() {
		t.Fatalf("body's rng draw did not match the threaded rng")
	}
}

// TestBodyContextRegistryPlumbed: the body receives a non-nil reg it can ResolvePlaced
// on, using a REAL embedded id — proving the plumbing reaches a usable registry.
func TestBodyContextRegistryPlumbed(t *testing.T) {
	const synthType = "test_registry_plumbed_synthetic"
	var resolveErr error
	var resolvedID string
	reg := feature.NewEmbeddedRegistry()
	registerFeatureBody(synthType, func(bctx *bodyContext, _ *feature.ConfiguredFeature, _ placement.PlacementContext, _ levelgen.RandomSource, _ placement.BlockPos) bool {
		pf, err := bctx.reg.ResolvePlaced("minecraft:flower_default")
		if err != nil {
			resolveErr = err
			return false
		}
		resolvedID = pf.ID
		return true
	})
	t.Cleanup(func() { delete(featureBodies, synthType) })

	cf := &feature.ConfiguredFeature{Type: synthType}
	view := build3x3([2]int{0, 0}, -64, 384)
	placer := newConfiguredPlacer(cf, view, reg, block.StateID(0), false, nil, 63, 0)
	ctx := newPlacementContext(view, -64, 384, nil)
	placer(ctx, levelgen.NewLegacyRandomSource(1), placement.BlockPos{})

	if resolveErr != nil {
		t.Fatalf("body could not ResolvePlaced through the plumbed registry: %v", resolveErr)
	}
	if resolvedID != "minecraft:flower_default" {
		t.Fatalf("body resolved %q, want minecraft:flower_default", resolvedID)
	}
}

// TestUnregisteredIsNoOp: a type with NO registered body stays a recordable no-op
// (returns false, writes nothing, but is still recorded).
func TestUnregisteredIsNoOp(t *testing.T) {
	cf := &feature.ConfiguredFeature{Type: "tree"} // a real type with no body yet (parser strips the ns)
	view := build3x3([2]int{0, 0}, -64, 384)
	var inv []featureInvocation
	placer := newConfiguredPlacer(cf, view, feature.NewEmbeddedRegistry(), block.StateID(0), false, &inv, 63, 0)
	ctx := newPlacementContext(view, -64, 384, nil)
	if placer(ctx, levelgen.NewLegacyRandomSource(1), placement.BlockPos{X: 1, Y: 2, Z: 3}) {
		t.Fatalf("unregistered type returned true (should be a no-op)")
	}
	if len(inv) != 1 || inv[0].featureType != "tree" || inv[0].pos != (placement.BlockPos{X: 1, Y: 2, Z: 3}) {
		t.Fatalf("unregistered no-op did not record the invocation: %+v", inv)
	}
}

// TestTestSetBlockStillWorks: the test_set_block path writes through the view and takes
// precedence over the body registry (it is never a real registered type).
func TestTestSetBlockStillWorks(t *testing.T) {
	cf := &feature.ConfiguredFeature{Type: testSetBlockType}
	view := build3x3([2]int{0, 0}, -64, 384)
	stone := block.ToStateID[block.Stone{}]
	placer := newConfiguredPlacer(cf, view, nil, stone, true, nil, 63, 0)
	ctx := newPlacementContext(view, -64, 384, nil)
	pos := placement.BlockPos{X: 0, Y: 0, Z: 0}
	if !placer(ctx, levelgen.NewLegacyRandomSource(1), pos) {
		t.Fatalf("test_set_block did not return true")
	}
	if got := view.GetBlock(0, 0, 0); got != stone {
		t.Fatalf("test_set_block did not write through the view: got %v want stone", got)
	}
}

// TestDuplicateRegistrationPanics: registering two bodies for one type panics loudly
// (a programming error — 12-02/12-03 must own disjoint types).
func TestDuplicateRegistrationPanics(t *testing.T) {
	const synthType = "test_dup_synthetic"
	registerFeatureBody(synthType, func(*bodyContext, *feature.ConfiguredFeature, placement.PlacementContext, levelgen.RandomSource, placement.BlockPos) bool { return false })
	t.Cleanup(func() { delete(featureBodies, synthType) })
	defer func() {
		if recover() == nil {
			t.Fatalf("duplicate registerFeatureBody did not panic")
		}
	}()
	registerFeatureBody(synthType, func(*bodyContext, *feature.ConfiguredFeature, placement.PlacementContext, levelgen.RandomSource, placement.BlockPos) bool { return true })
}
