package world

import (
	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// placementContext adapts the live Phase-10 *Neighborhood (the 3x3 read/write proxy)
// + the NoiseGenerator's biome source to placement.PlacementContext — the ONLY surface
// the 11-02 placement modifiers read. It is the concrete Neighborhood-backed impl
// 11-02 deferred to this plan.
//
//   - GetHeight reads the LIVE worldgen heightmaps the Neighborhood maintains
//     (WorldSurfaceWG / OceanFloorWG / MotionBlocking) — kept live across feature
//     writes by Neighborhood.SetBlock, so a same-step feature sees an earlier
//     placement. The client variants (WorldSurface / OceanFloor) map to their _WG
//     counterparts here (mid-worldgen there is no separate "client" heightmap; the
//     surface_water_depth_filter difference is the same on either pair).
//   - MinY/Height come from the generator's Dims() (the 3x3 geometry).
//   - GetBlock/BiomeAt delegate to the Neighborhood + the biome source (per-block via
//     the per-Decorate biomeCache, the same quart-cell memoization Generate uses).
//
// It is touched ONLY on the worker's single scheduler goroutine (single-owner, no
// locking), exactly like the Neighborhood it wraps.
type placementContext struct {
	view   *Neighborhood
	minY   int
	height int
	biome  func(x, y, z int) levelbiome.Type
}

// newPlacementContext builds the adapter over the 3x3 view. biomeAt is the per-Decorate
// biome lookup (the biomeCache.get closure) so a feature's biome filter re-checks the
// biome at each candidate position.
func newPlacementContext(view *Neighborhood, minY, height int, biomeAt func(x, y, z int) levelbiome.Type) *placementContext {
	return &placementContext{view: view, minY: minY, height: height, biome: biomeAt}
}

// heightmapColumn returns the y-major column index into a heightmap BitStorage:
// (z&15)<<4 | (x&15) — the same column order BuildSurface / BuildWorldgenHeightmaps write.
func heightmapColumn(x, z int) int { return (z&15)<<4 | (x & 15) }

// GetHeight reads the live worldgen heightmap value as a WORLD Y. The BitStorage stores
// per column the first air-above-top relative to MinY (= (topBlockY - MinY) + 1), so the
// world Y vanilla's getHeight returns is bs.Get(col) + MinY. An absent heightmap or an
// out-of-3x3 column reports MinY (treated as "no terrain", which empties a heightmap
// placement — matching the Neighborhood's air-outside-3x3 read semantics).
func (c *placementContext) GetHeight(t placement.HeightmapType, x, z int) int {
	ch, ok := c.view.chunkAt(x, z)
	if !ok || ch == nil {
		return c.minY
	}
	var bs *level.BitStorage
	switch t {
	case placement.WorldSurfaceWG, placement.WorldSurface:
		bs = ch.HeightMaps.WorldSurfaceWG
	case placement.OceanFloorWG, placement.OceanFloor:
		bs = ch.HeightMaps.OceanFloorWG
	case placement.MotionBlocking:
		bs = ch.HeightMaps.MotionBlocking
	default:
		bs = ch.HeightMaps.WorldSurfaceWG
	}
	if bs == nil {
		return c.minY
	}
	return bs.Get(heightmapColumn(x, z)) + c.minY
}

// MinY is the world floor.
func (c *placementContext) MinY() int { return c.minY }

// Height is the gen depth (the section-stack block height) — with MinY it resolves
// VerticalAnchor.below_top for the overworld ore height_range placed_features.
func (c *placementContext) Height() int { return c.height }

// GetBlock reads the live block state at (x,y,z) through the 3x3 proxy (air outside).
func (c *placementContext) GetBlock(x, y, z int) block.StateID { return c.view.GetBlock(x, y, z) }

// BiomeAt reads the biome at (x,y,z) via the per-Decorate biome lookup.
func (c *placementContext) BiomeAt(x, y, z int) levelbiome.Type { return c.biome(x, y, z) }

// compile-time assertion: the adapter satisfies the 11-02 PlacementContext interface.
var _ placement.PlacementContext = (*placementContext)(nil)

// ---- configured-feature placer dispatch ----

// featureInvocation records ONE configured-feature body invocation: the feature type +
// the anchor position the orchestration ran it at. Phase 11 implements NO real bodies,
// so the recorder proves the orchestration drove the right feature to the right anchor;
// Phase 12+ drops the real bodies into the same dispatch.
type featureInvocation struct {
	featureType string
	pos         placement.BlockPos
}

// testSetBlockType is the namespace-stripped type of the single test-only feature that
// actually writes a block through the Neighborhood (proving a SetBlock flows through +
// updates the live worldgen heightmap). It is NOT one of the 226 real vanilla types and
// never appears in the embedded data, so production decoration writes nothing in
// Phase 11 — every real type is a recordable no-op stub (the bodies are Phase 12+).
const testSetBlockType = "test_set_block"

// newConfiguredPlacer builds the ConfiguredFeaturePlacer for one configured feature:
// it records each invocation, and dispatches on the parsed feature type to a registered
// featureBody (Phase 12+'s real bodies) — or, if none is registered, the recordable
// NO-OP (plus the single test-only type that writes testBlock). cf may be nil (an
// unresolved ref) — the placer then records an empty type and no-ops.
//
// reg is the live *feature.Registry (g.deco.registry in production) threaded into the
// bodyContext so a registered body (12-03's selectors) can resolve a nested
// sub-PlacedFeature via reg.ResolvePlaced / reg.ParsePlacedFeature. CRITICAL: the REAL
// ctx + rng are captured (NOT discarded as in Phase 11) and passed to the body, so the
// body's draws continue the deterministic decoration sequence.
//
// seaLevel is the generator sea level threaded into the bodyContext for the freeze_top_layer
// body (coldEnoughToSnow(pos, seaLevel)). A zero value falls back to the vanilla 63 there.
func newConfiguredPlacer(
	cf *feature.ConfiguredFeature,
	view *Neighborhood,
	reg *feature.Registry,
	testBlock block.StateID,
	hasTest bool,
	invocations *[]featureInvocation,
	seaLevel int,
	seed int64,
) placement.PlacerFunc {
	ftype := ""
	if cf != nil {
		ftype = cf.Type
	}
	bctx := &bodyContext{view: view, reg: reg, seaLevel: seaLevel, seed: seed}
	return func(ctx placement.PlacementContext, rng levelgen.RandomSource, pos placement.BlockPos) bool {
		if invocations != nil {
			*invocations = append(*invocations, featureInvocation{featureType: ftype, pos: pos})
		}
		// The test-only type writes through the view (proving SetBlock flows + updates
		// the live worldgen heightmap). It takes precedence and is never a real type.
		if hasTest && ftype == testSetBlockType && view != nil {
			view.SetBlock(pos.X, pos.Y, pos.Z, testBlock)
			return true
		}
		// Dispatch on the parsed feature type to a registered body (Phase 12+). The
		// body receives the bodyContext (view + registry) + the REAL ctx + rng + cf,
		// so its draws continue the deterministic sequence and it can resolve nested
		// sub-features through bctx.reg. An unregistered real type stays a no-op (the
		// tree/dungeon types Phase 13 owns).
		if body := lookupFeatureBody(ftype); body != nil {
			return body(bctx, cf, ctx, rng, pos)
		}
		return false
	}
}
