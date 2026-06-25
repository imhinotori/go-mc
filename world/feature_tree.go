package world

// feature_tree.go is the LIVE "tree" featureBody — it wires the PURE tree placers
// (world/levelgen/feature/tree.go: StraightTrunkPlacer + BlobFoliagePlacer + the
// TreeConfiguration parse + the TreeFeature.place assembly) to the 3x3 Neighborhood, so
// an oak/birch biome's tree random_selector (Phase 12) now grows a REAL tree instead of
// the Phase-12 no-op. It registers under "tree" via registerFeatureBody from init() (the
// type is already in the recognized roster — no parse.go edit).
//
// The seam (T-13-02): the body builds a setBlock closure over bctx.view.SetBlock + a read
// closure over bctx.view.GetBlock, binds the rule_based below_trunk provider to the read,
// runs the TreeFeature.getMaxFreeTreeHeight validity scan over the live worldgen view, and
// on success runs feature.PlaceTree (getTreeHeight -> placeTrunk -> createFoliage) — the
// writes go through the cross-chunk view so a tree near a chunk edge spills logs/leaves
// into the neighbor and the live worldgen heightmap tracks the placement (Phase 10/11
// seam). A blocked footprint aborts the WHOLE tree (return false) — never a partial/
// floating tree. An unported placer in a tree config (a 13-02 large-oak the selector may
// pick mid-parallel-run) makes ParseTreeConfiguration error; the body then SKIPS (return
// false) — the SAME "skip rather than crash" convention applyBiomeDecoration uses, so the
// selector pick draw stands and the unported tree lands in 13-02/13-03 (T-13-03).
//
// Source mapping (all javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.TreeFeature.place / doPlace /
//     getMaxFreeTreeHeight / isFree / validTreePos
//   - the pure placers live in world/levelgen/feature (ported by 13-01 Task 1)

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("tree", treeBody) }

// treeBody ports TreeFeature.place. It decodes the TreeConfiguration, runs the
// getMaxFreeTreeHeight validity scan over the live 3x3 view, and on success assembles the
// tree (trunk column + foliage blobs) through the cross-chunk Neighborhood. Returns true
// iff at least one block was placed. The rng is the THREADED selector rng — the trunk/
// foliage draws continue the deterministic sequence (the determinism contract).
func treeBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg, err := feature.ParseTreeConfiguration(configRaw(cf))
	if err != nil {
		// A config naming an unported 13-02/13-03 placer (a large oak / cherry the
		// selector may pick mid-parallel-run) is SKIPPED, not crashed — the same
		// "skip rather than crash mid-decoration" convention applyBiomeDecoration /
		// placeSubFeature use. The selector's pick draw stands; the tree lands when its
		// placer ports. (A genuine build-data corruption would surface in the embed-all
		// decode test, not here.)
		return false
	}

	// The pure placers run against these closures over the live 3x3 view. setBlock writes
	// through the cross-chunk proxy (heightmap-live); read reads the live world (air
	// outside the 3x3).
	set := func(x, y, z int, st block.StateID) { bctx.view.SetBlock(x, y, z, st) }
	read := func(x, y, z int) block.StateID { return bctx.view.GetBlock(x, y, z) }

	// Bind the rule_based below_trunk provider to the live read so its
	// "not in cannot_replace_below_tree_trunk" rule evaluates the existing block (a simple
	// below-trunk provider is unaffected).
	cfg = cfg.BelowTrunkWithExisting(read)

	origin := feature.TreePos{X: pos.X, Y: pos.Y, Z: pos.Z}

	// TreeFeature.place: draw the trunk height (getTreeHeight — the TWO nextInt draws), then
	// hand off to PlaceTree, which runs the EXACT jar doPlace order — foliageHeight,
	// foliageRadius, trunk_offset_y (root placer), the footprint scan (getMaxFreeTreeHeight),
	// then roots -> trunk -> foliage -> decorators. PlaceTree internalises the scan so the
	// trunk_offset_y draw sits between foliageRadius and the scan (the jar order); it aborts
	// (no blocks placed) when the clamped free height is below minTreeHeight — the draws have
	// already happened (jar-faithful), so the selector's per-feature seed is unaffected.
	treeHeight := cfg.TrunkHeight(rng)
	return feature.PlaceTree(set, read, rng, cfg, treeHeight, minTreeHeight, origin)
}

// minTreeHeight is the conservative floor below which the body declines to place a tree
// (a 1-log stub is not a tree). Vanilla aborts when getMaxFreeTreeHeight < treeHeight only
// for the strict case; the overworld trees always have full room on flat ground, so this
// floor only triggers against a ceiling/overhang — where a no-tree is the right outcome.
const minTreeHeight = 2
