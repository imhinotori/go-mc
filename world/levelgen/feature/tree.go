package feature

// tree.go ports the TreeConfiguration parse + the two CANONICAL tree placers —
// StraightTrunkPlacer (oak/birch/jungle/spruce trunks) and BlobFoliagePlacer (oak/birch
// foliage) — plus the TrunkPlacer/FoliagePlacer bases and the TwoLayersFeatureSize the
// oak/birch minimum_size uses. It is PURE: the placers run against a setBlock callback +
// a worldgen-read callback + the threaded RandomSource, so this file imports ONLY
// world/levelgen (RandomSource) — NOT placement, NOT world (acyclic, exactly how
// provider.go lives here). The live "tree" featureBody (world/feature_tree.go) builds
// those callbacks over the 3x3 Neighborhood.
//
// The rng draw order IS the determinism contract (T-13-01): getTreeHeight's two nextInt
// draws, the trunk/foliage provider draws (simple=0, weighted=1), and the BlobFoliage
// row/corner walk are transcribed jar-exact and pinned by tree_test.go vs an oracle.
//
// Sources (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.TreeFeature.place / doPlace /
//     getMaxFreeTreeHeight / isFree / validTreePos / placeTrunk-helpers
//   - net.minecraft.world.level.levelgen.feature.trunkplacers.TrunkPlacer
//     (getTreeHeight / placeLog / placeLogIfFree / setDirtAt) +
//     trunkplacers.StraightTrunkPlacer.placeTrunk
//   - net.minecraft.world.level.levelgen.feature.foliageplacers.FoliagePlacer
//     (foliageHeight / placeLeavesRow / placeLeavesRowAndAddContinuation) +
//     foliageplacers.BlobFoliagePlacer.createFoliage / shouldSkipLocation
//   - net.minecraft.world.level.levelgen.feature.featuresize.{FeatureSize,
//     TwoLayersFeatureSize}.getSizeAtLayer
//   - net.minecraft.world.level.levelgen.feature.rootplacers.RootPlacer (the optional
//     hook; the concrete mangrove_root_placer body lands in 13-03)
//
// It is an algorithmic port, NOT a copy of Mojang source. No GPL paste.

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// ---- the pure placement callbacks ----
//
// The placers never touch `world`; they express placement through these two closures the
// live body supplies over the Neighborhood (and the unit tests supply over a map).

// SetBlockFn writes a block state at world (x,y,z). The live body wires it to
// Neighborhood.SetBlock (cross-chunk, heightmap-live); a test wires it to a map.
type SetBlockFn func(x, y, z int, st block.StateID)

// ReadFn reads the live block state at world (x,y,z) (air outside the view). The
// validity scan + the leaves-replaceable gate + the rule_based below_trunk provider read
// the existing world through it.
type ReadFn func(x, y, z int) block.StateID

// ---- BlockPos (local, to keep feature acyclic) ----
//
// The placement package has its own BlockPos, but feature must NOT import placement, so
// the pure placers use this local TreePos. The live body converts at the seam.

// TreePos is a worldgen block position the pure placers operate on (feature-local so this
// package does not import placement). above/below/relative mirror BlockPos helpers.
type TreePos struct {
	X, Y, Z int
}

func (p TreePos) above(n int) TreePos  { return TreePos{p.X, p.Y + n, p.Z} }
func (p TreePos) below(n int) TreePos  { return TreePos{p.X, p.Y - n, p.Z} }
func (p TreePos) offset(dx, dy, dz int) TreePos {
	return TreePos{p.X + dx, p.Y + dy, p.Z + dz}
}

// ---- FoliageAttachment ----

// FoliageAttachment is FoliagePlacer.FoliageAttachment: the point a foliage blob attaches
// (the top of the trunk for StraightTrunkPlacer), the radiusOffset the foliage adds to
// its base radius, and the doubleTrunk flag (always false for the straight/blob pair).
type FoliageAttachment struct {
	Pos          TreePos
	RadiusOffset int
	DoubleTrunk  bool
}

// ---- TwoLayersFeatureSize (minimum_size) ----
//
// FeatureSize.getSizeAtLayer(height, depth) returns the max horizontal radius the trunk
// may have at vertical `depth` from the base; TreeFeature uses it to size the free-space
// validity scan column. oak/birch carry the BARE-DEFAULT two_layers_feature_size (no
// limit/lower/upper params), so the defaults below apply.

// featureSize is FeatureSize (the validity-scan radius + the clipped-height escape).
type featureSize interface {
	// getSizeAtLayer returns the allowed horizontal radius at vertical `depth` from the
	// trunk base, given the total tree `height`.
	getSizeAtLayer(height, depth int) int
	// minClippedHeight ports FeatureSize.minClippedHeight (Optional<Integer>): the
	// minimum free height at which a tree taller than the scanned room may still place a
	// clipped (shorter) tree. Returns (value, true) when present, (0, false) when empty.
	// Empty for the common overworld two_layers_feature_size trees -> a too-short column
	// aborts (the anti-stack guard); present only on the trees that allow clipping.
	minClippedHeight() (int, bool)
}

// twoLayersFeatureSize ports TwoLayersFeatureSize: a `limit` rows from the top use
// upperSize, the rest use lowerSize. Defaults (no JSON params): limit=1, lowerSize=0,
// upperSize=1 (TwoLayersFeatureSize's default field values, jar-confirmed).
type twoLayersFeatureSize struct {
	limit     int
	lowerSize int
	upperSize int
	// minClipped is FeatureSize.minClippedHeight (Optional<Integer>); absent for
	// oak/birch — clippedSet=false.
	minClipped    int
	minClippedSet bool
}

// getSizeAtLayer ports TwoLayersFeatureSize.getSizeAtHeight: depth < limit -> lowerSize,
// else upperSize. (TwoLayersFeatureSize counts `depth` UP from the base; the first
// `limit` layers are "lower".)
func (s twoLayersFeatureSize) getSizeAtLayer(_, depth int) int {
	if depth < s.limit {
		return s.lowerSize
	}
	return s.upperSize
}

func (s twoLayersFeatureSize) minClippedHeight() (int, bool) { return s.minClipped, s.minClippedSet }

// threeLayersFeatureSize ports ThreeLayersFeatureSize: lowerSize for the first `limit`
// layers, upperSize for the top `upperLimit` layers (depth >= height - upperLimit), and
// middleSize between. dark_oak/mega use it.
type threeLayersFeatureSize struct {
	limit      int
	upperLimit int
	lowerSize  int
	middleSize int
	upperSize  int
	// minClipped is FeatureSize.minClippedHeight (Optional<Integer>).
	minClipped    int
	minClippedSet bool
}

// getSizeAtLayer ports ThreeLayersFeatureSize.getSizeAtHeight: depth < limit -> lowerSize;
// depth >= height - upperLimit -> upperSize; else middleSize.
func (s threeLayersFeatureSize) getSizeAtLayer(height, depth int) int {
	if depth < s.limit {
		return s.lowerSize
	}
	if depth >= height-s.upperLimit {
		return s.upperSize
	}
	return s.middleSize
}

func (s threeLayersFeatureSize) minClippedHeight() (int, bool) { return s.minClipped, s.minClippedSet }

// parseFeatureSize decodes a minimum_size envelope. Only two_layers_feature_size (the
// oak/birch default) is ported here; three_layers_feature_size + the parameterized
// variants land with 13-02's dark_oak/pale_oak placers. An unported size errors loudly.
func parseFeatureSize(raw json.RawMessage) (featureSize, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("feature: empty minimum_size")
	}
	var j struct {
		Type             string `json:"type"`
		Limit            *int   `json:"limit"`
		UpperLimit       *int   `json:"upper_limit"`
		LowerSize        *int   `json:"lower_size"`
		MiddleSize       *int   `json:"middle_size"`
		UpperSize        *int   `json:"upper_size"`
		MinClippedHeight *int   `json:"min_clipped_height"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: minimum_size: %w", err)
	}
	switch stripNS(j.Type) {
	case "two_layers_feature_size":
		// TwoLayersFeatureSize default field values (jar): limit 1, lowerSize 0,
		// upperSize 1. The bare oak/birch minimum_size carries none, so these stand.
		s := twoLayersFeatureSize{limit: 1, lowerSize: 0, upperSize: 1}
		if j.Limit != nil {
			s.limit = *j.Limit
		}
		if j.LowerSize != nil {
			s.lowerSize = *j.LowerSize
		}
		if j.UpperSize != nil {
			s.upperSize = *j.UpperSize
		}
		if j.MinClippedHeight != nil {
			s.minClipped, s.minClippedSet = *j.MinClippedHeight, true
		}
		return s, nil
	case "three_layers_feature_size":
		// ThreeLayersFeatureSize default field values (jar codec): limit 1, upperLimit 1,
		// lowerSize 0, middleSize 1, upperSize 1. dark_oak overrides upper_size=2.
		s := threeLayersFeatureSize{limit: 1, upperLimit: 1, lowerSize: 0, middleSize: 1, upperSize: 1}
		if j.Limit != nil {
			s.limit = *j.Limit
		}
		if j.UpperLimit != nil {
			s.upperLimit = *j.UpperLimit
		}
		if j.LowerSize != nil {
			s.lowerSize = *j.LowerSize
		}
		if j.MiddleSize != nil {
			s.middleSize = *j.MiddleSize
		}
		if j.UpperSize != nil {
			s.upperSize = *j.UpperSize
		}
		if j.MinClippedHeight != nil {
			s.minClipped, s.minClippedSet = *j.MinClippedHeight, true
		}
		return s, nil
	default:
		return nil, fmt.Errorf("feature: unported minimum_size type %q", j.Type)
	}
}

// ---- TrunkPlacer base + StraightTrunkPlacer ----

// TrunkPlacer is the trunk-placer interface a TreeConfiguration carries. getTreeHeight
// draws the column height; placeTrunk places the column (+ below-trunk dirt) and returns
// the foliage attachments. The dirt-below + the log-replaceable gate read/write through
// the supplied callbacks so the placer stays pure.
type TrunkPlacer interface {
	// getTreeHeight ports TrunkPlacer.getTreeHeight: base + nextInt(a+1) + nextInt(b+1).
	getTreeHeight(rng levelgen.RandomSource) int
	// placeTrunk places the column and returns the foliage attachments. cfg supplies the
	// trunk + below-trunk providers; read gates log replacement; set writes.
	placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment
}

// trunkPlacerBase holds the shared base_height/height_rand fields + the getTreeHeight /
// placeLog / setDirtAt helpers every TrunkPlacer subclass uses (TrunkPlacer base).
type trunkPlacerBase struct {
	baseHeight  int
	heightRandA int
	heightRandB int
}

// getTreeHeight ports TrunkPlacer.getTreeHeight: baseHeight + nextInt(heightRandA+1) +
// nextInt(heightRandB+1). The TWO nextInt draws (in this order) are the determinism
// contract; nextInt(1) consumes a draw and returns 0 (matching vanilla, where rand_b=0
// for oak/birch still draws).
func (b trunkPlacerBase) getTreeHeight(rng levelgen.RandomSource) int {
	return b.baseHeight + int(rng.NextIntN(int32(b.heightRandA+1))) + int(rng.NextIntN(int32(b.heightRandB+1)))
}

// placeLog ports TrunkPlacer.placeLog -> placeLogIfFree: if the existing block is
// free/replaceable, write the trunk_provider's state. The trunk_provider.GetState draw
// (simple=0, weighted=1) happens jar-exact. Returns whether a log was placed.
func placeLog(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, pos TreePos, cfg *TreeConfiguration) bool {
	if !treePosFree(read, pos) {
		return false
	}
	st := cfg.trunkProvider.GetState(rng, pos.X, pos.Y, pos.Z)
	set(pos.X, pos.Y, pos.Z, st)
	if cfg.accum != nil {
		cfg.accum.addLog(pos)
	}
	return true
}

// setDirtAt ports TrunkPlacer.setDirtAt: place the below_trunk provider's state under the
// trunk IFF the existing block is dirt-fertile-or-replaceable. The below_trunk_provider
// for oak/birch is a rule_based_state_provider whose rule reads the existing block, so the
// body binds it via RuleBasedStateProvider.withExisting(read) before passing cfg here. A
// simple provider draws 0; the rule walk draws per its chosen provider (0 for the dirt
// rule). The block is written only when the position is not already a non-replaceable log.
func setDirtAt(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, pos TreePos, cfg *TreeConfiguration) {
	// TrunkPlacer.setDirtAt: if the block is already dirt-like, leave it; else set the
	// below-trunk provider state. We use the conservative readable-state test: place the
	// provider state when the existing block is air-or-replaceable OR plain non-log
	// ground (the rule_based provider itself decides the final state — its "not in
	// cannot_replace_below_tree_trunk" rule yields dirt over any non-trunk block).
	st := cfg.belowTrunkProvider.GetState(rng, pos.X, pos.Y, pos.Z)
	set(pos.X, pos.Y, pos.Z, st)
}

// StraightTrunkPlacer is StraightTrunkPlacer: a single vertical log column + the dirt
// below, returning ONE foliage attachment at origin.above(freeHeight). The canonical
// oak/birch/jungle/spruce trunk (research Code Examples, javap -c placeTrunk).
type StraightTrunkPlacer struct {
	trunkPlacerBase
}

// placeTrunk ports StraightTrunkPlacer.placeTrunk: setDirtAt(origin.below(1)), then
// freeHeight logs up the column via placeLog, returning a single FoliageAttachment at
// origin.above(freeHeight) {radiusOffset 0, doubleTrunk false}.
func (p StraightTrunkPlacer) placeTrunk(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, freeHeight int, origin TreePos, cfg *TreeConfiguration) []FoliageAttachment {
	// setDirtAt the block below the trunk origin (the dirt the tree roots on).
	setDirtAt(set, read, rng, origin.below(1), cfg)
	for i := 0; i < freeHeight; i++ {
		placeLog(set, read, rng, origin.above(i), cfg)
	}
	return []FoliageAttachment{{Pos: origin.above(freeHeight), RadiusOffset: 0, DoubleTrunk: false}}
}

// parseTrunkPlacer dispatches a trunk_placer envelope. Only straight_trunk_placer is
// ported here; forking/fancy/dark_oak/cherry/mangrove etc. error loudly and land in
// 13-02/13-03.
func parseTrunkPlacer(raw json.RawMessage) (TrunkPlacer, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("feature: empty trunk_placer")
	}
	var j struct {
		Type        string `json:"type"`
		BaseHeight  int    `json:"base_height"`
		HeightRandA int    `json:"height_rand_a"`
		HeightRandB int    `json:"height_rand_b"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: trunk_placer: %w", err)
	}
	base := trunkPlacerBase{baseHeight: j.BaseHeight, heightRandA: j.HeightRandA, heightRandB: j.HeightRandB}
	switch stripNS(j.Type) {
	case "straight_trunk_placer":
		return StraightTrunkPlacer{trunkPlacerBase: base}, nil
	case "forking_trunk_placer":
		return ForkingTrunkPlacer{trunkPlacerBase: base}, nil
	case "fancy_trunk_placer":
		return FancyTrunkPlacer{trunkPlacerBase: base}, nil
	case "dark_oak_trunk_placer":
		return DarkOakTrunkPlacer{trunkPlacerBase: base}, nil
	case "giant_trunk_placer":
		return GiantTrunkPlacer{trunkPlacerBase: base}, nil
	case "mega_jungle_trunk_placer":
		return MegaJungleTrunkPlacer{GiantTrunkPlacer{trunkPlacerBase: base}}, nil
	case "cherry_trunk_placer":
		return parseCherryTrunkPlacer(base, raw)
	case "upwards_branching_trunk_placer":
		return parseUpwardsBranchingTrunkPlacer(base, raw)
	case "bending_trunk_placer":
		return parseBendingTrunkPlacer(base, raw)
	default:
		return nil, fmt.Errorf("feature: unknown trunk_placer type %q", j.Type)
	}
}

// ---- FoliagePlacer base + BlobFoliagePlacer ----

// FoliagePlacer is the foliage-placer interface a TreeConfiguration carries. createFoliage
// places the leaf blobs at one attachment; foliageHeight reports the blob's vertical
// extent the validity scan uses.
//
// The 26.2 FoliagePlacer.doPlace call shape (TreeFeature.doPlace) samples foliageHeight
// and foliageRadius BEFORE the trunk, then passes them (+ a per-placer `offset` draw) into
// createFoliage. So the interface carries those as explicit params (the determinism
// contract — the draw order getTreeHeight -> foliageHeight -> foliageRadius -> placeTrunk
// -> offset(in createFoliage) is what PlaceTree replays).
type FoliagePlacer interface {
	// createFoliage places the leaf shape at the attachment. foliageRadius/foliageHeight
	// are the (already-sampled) values; `offset` is this placer's offset(rng) draw the
	// public wrapper made. The protected createFoliage(...,radius,foliageHeight,offset).
	createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, attachment FoliageAttachment, foliageRadius, foliageHeight, offset int)
	// foliageHeightOf reports the blob's vertical extent (FoliagePlacer.foliageHeight),
	// drawing rng for the placers whose height is an IntProvider (spruce/pine/megapine).
	foliageHeightOf(rng levelgen.RandomSource, treeHeight int, cfg *TreeConfiguration) int
	// foliageRadiusOf reports the radius (FoliagePlacer.foliageRadius); pine overrides it
	// with an extra draw. The base samples the radius IntProvider.
	foliageRadiusOf(rng levelgen.RandomSource, depth int) int
	// offsetOf samples the offset IntProvider (FoliagePlacer.offset) — the draw the public
	// createFoliage wrapper makes just before the protected createFoliage.
	offsetOf(rng levelgen.RandomSource) int
}

// foliagePlacerBase holds the shared radius/offset IntProviders (FoliagePlacer.radius /
// .offset) + the placeLeavesRow helper. oak/birch carry constant providers (0 draws);
// spruce/pine/megapine carry uniform providers that draw.
type foliagePlacerBase struct {
	radius intProvider
	offset intProvider
}

// foliageRadiusOf ports FoliagePlacer.foliageRadius: sample the radius IntProvider.
func (b foliagePlacerBase) foliageRadiusOf(rng levelgen.RandomSource, _ int) int {
	return b.radius.sample(rng)
}

// offsetOf ports FoliagePlacer.offset: sample the offset IntProvider (the draw the public
// createFoliage wrapper makes before the protected createFoliage).
func (b foliagePlacerBase) offsetOf(rng levelgen.RandomSource) int {
	return b.offset.sample(rng)
}

// placeLeaf ports FoliagePlacer.placeLeaf -> tryPlaceLeaf: if the existing block is
// free/replaceable, write the foliage_provider's leaf state. One foliage_provider.GetState
// draw (simple=0). Returns whether a leaf was placed.
func placeLeaf(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, pos TreePos, cfg *TreeConfiguration) bool {
	if !treePosFree(read, pos) {
		return false
	}
	st := cfg.foliageProvider.GetState(rng, pos.X, pos.Y, pos.Z)
	set(pos.X, pos.Y, pos.Z, st)
	if cfg.accum != nil {
		cfg.accum.addLeaf(pos)
	}
	return true
}

// skipFn is FoliagePlacer.shouldSkipLocation(rng, localX, localY, localZ, range, large):
// each placer supplies its own corner/cone/disc trim rule on the FOLDED non-negative coords.
type skipFn func(rng levelgen.RandomSource, localX, localY, localZ, rangeR int, large bool) bool

// signedSkipFn is FoliagePlacer.shouldSkipLocationSigned(rng, dx, localY, dz, range, large):
// it receives the SIGNED dx/dz. The default (signedFromSkip) folds them and calls a skipFn;
// DarkOak overrides it directly (it needs the signed coords).
type signedSkipFn func(rng levelgen.RandomSource, dx, localY, dz, rangeR int, large bool) bool

// shouldSkipLocationSigned ports FoliagePlacer.shouldSkipLocationSigned: fold the signed
// (dx,dz) to the non-negative coords the per-placer shouldSkipLocation expects. For a
// doubleTrunk (large) row the fold is min(|c|, |c-1|) (the 2x2 trunk's two centers); else
// |c|. Then call the placer's shouldSkipLocation.
func shouldSkipLocationSigned(skip skipFn, rng levelgen.RandomSource, dx, localY, dz, rangeR int, large bool) bool {
	var lx, lz int
	if large {
		lx = min2(abs(dx), abs(dx-1))
		lz = min2(abs(dz), abs(dz-1))
	} else {
		lx = abs(dx)
		lz = abs(dz)
	}
	return skip(rng, lx, localY, lz, rangeR, large)
}

// signedFromSkip wraps a folded skipFn into the signed seam (the default for every placer
// except DarkOak).
func signedFromSkip(skip skipFn) signedSkipFn {
	return func(rng levelgen.RandomSource, dx, localY, dz, rangeR int, large bool) bool {
		return shouldSkipLocationSigned(skip, rng, dx, localY, dz, rangeR, large)
	}
}

// placeLeavesRow ports FoliagePlacer.placeLeavesRow: iterate the square from dx=-range to
// range+(large?1:0) (z the same), in x-major then z order, skip the cells the signed-skip
// rule flags, and place a leaf at (center + (dx,0,dz)). `center` already carries the row's
// Y; `localY` is the row's vertical INDEX (passed to the skip rule). The square iteration
// order + the per-cell skip evaluation are the determinism contract. The upper bound is
// `range + extra` (extra = large?1:0) — the 2x2-trunk rows are one wider on +x/+z.
func placeLeavesRow(
	set SetBlockFn, read ReadFn, rng levelgen.RandomSource,
	cfg *TreeConfiguration, center TreePos, rangeR, localY int, large bool,
	skip skipFn,
) {
	placeLeavesRowSigned(set, read, rng, cfg, center, rangeR, localY, large, signedFromSkip(skip))
}

// placeLeavesRowSigned is placeLeavesRow with an explicit signed-skip seam (DarkOak routes
// through here with its own shouldSkipLocationSigned).
func placeLeavesRowSigned(
	set SetBlockFn, read ReadFn, rng levelgen.RandomSource,
	cfg *TreeConfiguration, center TreePos, rangeR, localY int, large bool,
	skip signedSkipFn,
) {
	extra := 0
	if large {
		extra = 1
	}
	for dx := -rangeR; dx <= rangeR+extra; dx++ {
		for dz := -rangeR; dz <= rangeR+extra; dz++ {
			if skip(rng, dx, localY, dz, rangeR, large) {
				continue
			}
			placeLeaf(set, read, rng, center.offset(dx, 0, dz), cfg)
		}
	}
}

// BlobFoliagePlacer is BlobFoliagePlacer: a stack of leaf rows around the attachment, the
// widest rows corner-trimmed (shouldSkipLocation). The canonical oak/birch foliage.
type BlobFoliagePlacer struct {
	foliagePlacerBase
	height int // BlobFoliagePlacer.height (max vertical foliage offset from the top)
}

// foliageHeightOf ports BlobFoliagePlacer.foliageHeight: just the configured height
// (BlobFoliagePlacer returns `this.height` — 3 for oak/birch). 0 rng draws.
func (p BlobFoliagePlacer) foliageHeightOf(_ levelgen.RandomSource, _ int, _ *TreeConfiguration) int {
	return p.height
}

// createFoliage ports BlobFoliagePlacer.createFoliage (the 26.2 protected shape):
//
//	for (int i = offset; i >= offset - foliageHeight; --i) {
//	    int j = Math.max(radius + att.radiusOffset() - 1 - i/2, 0);
//	    placeLeavesRow(att.pos(), j, i, att.doubleTrunk());   // setWithOffset => row.Y = pos.Y + i
//	}
//
// `radius` here is the already-sampled foliageRadius; `i` (NOT -i) is the localY passed to
// the skip rule. Vanilla FoliagePlacer.placeLeavesRow uses setWithOffset(pos, dx, localY, dz)
// => the row's world-Y is pos.Y + localY (verified via javap -c on FoliagePlacer +
// BlobFoliagePlacer). Sulfur's placeLeavesRow flattens the row to dy=0 (center.offset(dx,0,dz))
// and bakes localY into `center`, so the caller MUST pass att.Pos.above(i) (= pos.Y + i),
// NOT below(i) (= pos.Y - i) — below(i) is the negated mapping and produced an upside-down
// (wide-at-top) blob. The row walk (top-down) + the per-row radius math + the corner trim are
// the determinism contract.
func (p BlobFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, attachment FoliageAttachment, foliageRadius, foliageHeight, offset int) {
	for i := offset; i >= offset-foliageHeight; i-- {
		j := foliageRadius + attachment.RadiusOffset - 1 - javaDiv(i, 2)
		if j < 0 {
			j = 0
		}
		placeLeavesRow(set, read, rng, cfg, attachment.Pos.above(i), j, i, attachment.DoubleTrunk,
			p.shouldSkip)
	}
}

// shouldSkip ports BlobFoliagePlacer.shouldSkipLocation: trim the 4 corners of the widest
// rows. The jar condition (javap -c):
//
//	localX == range && localZ == range && (rng.nextInt(2) != 0 || localY == 0)
//
// i.e. on a corner cell: ALWAYS skip the top/bottom rows (localY==0), and on the middle
// rows skip 50% of the time (nextInt(2)!=0). The args are the FOLDED non-negative coords
// (shouldSkipLocationSigned already applied the fold), so compare against `range` directly.
// The nextInt(2) draw on every corner-of-widest-row cell IS a determinism contract.
func (p BlobFoliagePlacer) shouldSkip(rng levelgen.RandomSource, lx, localY, lz, rangeR int, _ bool) bool {
	if lx == rangeR && lz == rangeR {
		return rng.NextIntN(2) != 0 || localY == 0
	}
	return false
}

// parseFoliagePlacer dispatches a foliage_placer envelope. Only blob_foliage_placer (the
// oak/birch foliage) is ported here; the rest error loudly and land in 13-02/13-03.
func parseFoliagePlacer(raw json.RawMessage) (FoliagePlacer, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("feature: empty foliage_placer")
	}
	// radius/offset are vanilla IntProviders (oak/birch carry bare ints -> constant).
	var j struct {
		Type        string          `json:"type"`
		Radius      json.RawMessage `json:"radius"`
		Offset      json.RawMessage `json:"offset"`
		Height      json.RawMessage `json:"height"`
		TrunkHeight json.RawMessage `json:"trunk_height"`
		CrownHeight json.RawMessage `json:"crown_height"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: foliage_placer: %w", err)
	}
	radius, err := parseIntProvider(j.Radius)
	if err != nil {
		return nil, fmt.Errorf("feature: foliage_placer radius: %w", err)
	}
	offset, err := parseIntProvider(j.Offset)
	if err != nil {
		return nil, fmt.Errorf("feature: foliage_placer offset: %w", err)
	}
	base := foliagePlacerBase{radius: radius, offset: offset}
	switch stripNS(j.Type) {
	case "blob_foliage_placer":
		h, err := intFromHeight(j.Height)
		if err != nil {
			return nil, fmt.Errorf("feature: blob_foliage_placer height: %w", err)
		}
		return BlobFoliagePlacer{foliagePlacerBase: base, height: h}, nil
	case "bush_foliage_placer":
		h, err := intFromHeight(j.Height)
		if err != nil {
			return nil, fmt.Errorf("feature: bush_foliage_placer height: %w", err)
		}
		return BushFoliagePlacer{BlobFoliagePlacer{foliagePlacerBase: base, height: h}}, nil
	case "fancy_foliage_placer":
		h, err := intFromHeight(j.Height)
		if err != nil {
			return nil, fmt.Errorf("feature: fancy_foliage_placer height: %w", err)
		}
		return FancyFoliagePlacer{BlobFoliagePlacer{foliagePlacerBase: base, height: h}}, nil
	case "spruce_foliage_placer":
		th, err := parseIntProvider(j.TrunkHeight)
		if err != nil {
			return nil, fmt.Errorf("feature: spruce_foliage_placer trunk_height: %w", err)
		}
		return SpruceFoliagePlacer{foliagePlacerBase: base, trunkHeight: th}, nil
	case "pine_foliage_placer":
		h, err := parseIntProvider(j.Height)
		if err != nil {
			return nil, fmt.Errorf("feature: pine_foliage_placer height: %w", err)
		}
		return PineFoliagePlacer{foliagePlacerBase: base, height: h}, nil
	case "acacia_foliage_placer":
		return AcaciaFoliagePlacer{foliagePlacerBase: base}, nil
	case "dark_oak_foliage_placer":
		return DarkOakFoliagePlacer{foliagePlacerBase: base}, nil
	case "jungle_foliage_placer":
		// The JSON id "jungle_foliage_placer" registers the MegaJungleFoliagePlacer class
		// (used by mega_jungle_tree; NOT jungle_tree, which uses blob). Verified in
		// FoliagePlacerType: the registered string is jungle_foliage_placer.
		h, err := intFromHeight(j.Height)
		if err != nil {
			return nil, fmt.Errorf("feature: jungle_foliage_placer height: %w", err)
		}
		return JungleFoliagePlacer{foliagePlacerBase: base, height: h}, nil
	case "mega_pine_foliage_placer":
		ch, err := parseIntProvider(j.CrownHeight)
		if err != nil {
			return nil, fmt.Errorf("feature: mega_pine_foliage_placer crown_height: %w", err)
		}
		return MegaPineFoliagePlacer{foliagePlacerBase: base, crownHeight: ch}, nil
	case "random_spread_foliage_placer":
		return parseRandomSpreadFoliagePlacer(base, raw)
	case "cherry_foliage_placer":
		return parseCherryFoliagePlacer(base, raw)
	default:
		return nil, fmt.Errorf("feature: unknown foliage_placer type %q", j.Type)
	}
}

// intFromHeight decodes a foliage `height` field (a bare int for blob/bush/fancy/jungle)
// into a plain int via the int-provider parse (constant only here).
func intFromHeight(raw json.RawMessage) (int, error) {
	p, err := parseIntProvider(raw)
	if err != nil {
		return 0, err
	}
	c, ok := p.(constantIntProvider)
	if !ok {
		return 0, fmt.Errorf("height must be a constant int")
	}
	return c.value, nil
}

// ---- IntProvider (net.minecraft.util.valueproviders.IntProvider) ----
//
// The foliage radius/offset/height + the spruce trunk_height + the mega-pine crown_height
// are vanilla IntProviders. oak/birch carry bare ints (constant, 0 draws); spruce/pine/
// megapine carry uniform providers that DRAW nextInt(max-min+1). The sample draw count IS
// a determinism contract.

// intProvider is the minimal IntProvider surface the tree placers need: sample(rng) -> int.
type intProvider interface {
	sample(rng levelgen.RandomSource) int
}

// constantIntProvider is ConstantInt: returns value, 0 draws.
type constantIntProvider struct{ value int }

func (p constantIntProvider) sample(_ levelgen.RandomSource) int { return p.value }

// uniformIntProvider is UniformInt: nextInt(max-min+1) + min — ONE draw. (UniformInt.sample
// = min + rng.nextInt(max - min + 1).)
type uniformIntProvider struct{ min, max int }

func (p uniformIntProvider) sample(rng levelgen.RandomSource) int {
	return p.min + int(rng.NextIntN(int32(p.max-p.min+1)))
}

// weightedListIntEntry is one (provider, weight) of a WeightedListInt's distribution.
type weightedListIntEntry struct {
	provider intProvider
	weight   int
}

// weightedListIntProvider is WeightedListInt (cherry's branch_count): sample draws ONCE
// (WeightedList.getRandom -> nextInt(totalWeight) + cumulative walk in ENTRY ORDER) to pick
// an inner IntProvider, then samples it. The cherry branch_count entries are bare ints
// (constant -> 0 inner draws), so the net draw is one nextInt(totalWeight). The entry order
// + the single-draw cumulative walk are the determinism contract.
//
// Source: javap -c WeightedListInt.sample -> WeightedList.getRandom.
type weightedListIntProvider struct {
	entries     []weightedListIntEntry
	totalWeight int
}

// sample ports WeightedListInt.sample: nextInt(totalWeight), walk the cumulative weight in
// entry order, then sample the chosen inner provider.
func (p weightedListIntProvider) sample(rng levelgen.RandomSource) int {
	i := int(rng.NextIntN(int32(p.totalWeight)))
	for _, e := range p.entries {
		i -= e.weight
		if i < 0 {
			return e.provider.sample(rng)
		}
	}
	return p.entries[len(p.entries)-1].provider.sample(rng)
}

// parseIntProvider decodes the bare-int OR {type:constant,value} OR
// {type:uniform,min_inclusive,max_inclusive} IntProvider forms. A bare int -> constant.
// Other provider types (biased_to_bottom/clamped/weighted_list) are not used by the common
// tree data and error loudly so a future placer ports them rather than drifting.
func parseIntProvider(raw json.RawMessage) (intProvider, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing int provider")
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return constantIntProvider{value: n}, nil
	}
	var obj struct {
		Type         string `json:"type"`
		Value        *int   `json:"value"`
		MinInclusive *int   `json:"min_inclusive"`
		MaxInclusive *int   `json:"max_inclusive"`
		Distribution []struct {
			Data   json.RawMessage `json:"data"`
			Weight int             `json:"weight"`
		} `json:"distribution"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("not a bare int or int provider: %w", err)
	}
	switch stripNS(obj.Type) {
	case "constant":
		if obj.Value == nil {
			return nil, fmt.Errorf("constant int provider missing value")
		}
		return constantIntProvider{value: *obj.Value}, nil
	case "uniform":
		if obj.MinInclusive == nil || obj.MaxInclusive == nil {
			return nil, fmt.Errorf("uniform int provider missing min/max_inclusive")
		}
		return uniformIntProvider{min: *obj.MinInclusive, max: *obj.MaxInclusive}, nil
	case "weighted_list":
		// cherry's branch_count: a weighted list of inner IntProviders (here bare-int
		// constants). One draw to pick + the inner provider's draws.
		if len(obj.Distribution) == 0 {
			return nil, fmt.Errorf("weighted_list int provider has no distribution")
		}
		wp := weightedListIntProvider{}
		for _, e := range obj.Distribution {
			inner, err := parseIntProvider(e.Data)
			if err != nil {
				return nil, fmt.Errorf("weighted_list int provider entry: %w", err)
			}
			if e.Weight <= 0 {
				return nil, fmt.Errorf("weighted_list int provider entry has non-positive weight %d", e.Weight)
			}
			wp.entries = append(wp.entries, weightedListIntEntry{provider: inner, weight: e.Weight})
			wp.totalWeight += e.Weight
		}
		return wp, nil
	default:
		return nil, fmt.Errorf("unported int provider type %q", obj.Type)
	}
}

// ---- RootPlacer (optional) ----

// RootPlacer is the OPTIONAL root_placer a TreeConfiguration may carry. It is nil for
// oak/birch (absent in oak.json); the model defines the interface + the field + the
// TreeFeature.place hook now so 13-03 plugs mangrove_root_placer in WITHOUT re-touching
// the struct. placeRoots places the roots and returns the (possibly shifted) trunk origin
// + whether placement may continue.
type RootPlacer interface {
	// getTrunkOrigin ports RootPlacer.getTrunkOrigin: pos.above(trunk_offset_y.sample(rng)).
	// The jar samples this BEFORE the validity scan (the trunk grows from the shifted
	// origin); placeRoots then runs AFTER the scan at the ORIGINAL pos. ONE IntProvider draw.
	getTrunkOrigin(rng levelgen.RandomSource, pos TreePos) TreePos
	// placeRoots places the root system growing DOWN from `pos` (the original anchor, NOT
	// the shifted trunk origin) and returns whether the tree may continue. Called AFTER the
	// validity scan (jar doPlace order). Draws per the root simulation.
	placeRoots(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, pos, trunkOrigin TreePos, cfg *TreeConfiguration) bool
}

// parseRootPlacer decodes an OPTIONAL root_placer envelope. Returns (nil, nil) when the
// field is absent (oak/birch). The only real type, mangrove_root_placer, errors LOUDLY —
// 13-03 ports the concrete body; the field + hook are wired here.
func parseRootPlacer(raw json.RawMessage) (RootPlacer, error) {
	if len(raw) == 0 {
		return nil, nil // absent -> no root placer (oak/birch)
	}
	var j struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: root_placer: %w", err)
	}
	switch stripNS(j.Type) {
	case "mangrove_root_placer":
		return parseMangroveRootPlacer(raw)
	default:
		return nil, fmt.Errorf("feature: unknown root_placer type %q", j.Type)
	}
}

// ---- TreeConfiguration ----

// TreeConfiguration is the decoded TreeConfiguration (the REAL oak.json field set): the
// trunk/foliage/below-trunk providers, the trunk + foliage placers, the OPTIONAL root
// placer (nil for oak/birch), the minimum_size, ignore_vines, and the (empty for
// oak/birch) decorators list. There is NO force_dirt/dirt_provider key (verified absent
// in oak.json — the dirt is the below_trunk_provider, a rule_based provider).
type TreeConfiguration struct {
	trunkProvider      BlockStateProvider
	foliageProvider    BlockStateProvider
	belowTrunkProvider BlockStateProvider
	trunkPlacer        TrunkPlacer
	foliagePlacer      FoliagePlacer
	rootPlacer         RootPlacer // OPTIONAL — nil for oak/birch
	minimumSize        featureSize
	ignoreVines        bool
	// decorators is the (empty for oak/birch) TreeDecorator list. Captured raw so the
	// decode lands in ParseTreeConfiguration; an empty array means a no-op-decorator tree.
	decoratorsRaw []json.RawMessage
	// decorators is the PARSED common-overworld TreeDecorator list (AlterGround/Beehive/
	// Cocoa/vines). Empty for oak/birch. Run AFTER trunk+foliage in PlaceTree.
	decorators []TreeDecorator
	// accum collects the placed log/leaf positions during placeLog/placeLeaf for the
	// TreeDecorator.Context. Non-nil only during a single PlaceTree call (set on a copy).
	accum *treeAccum
	// heightmapMBNL reports the live MOTION_BLOCKING_NO_LEAVES heightmap Y at world (x,z),
	// bound by the caller (treeBody) to the 3x3 view. It feeds PlaceOnGroundDecorator's
	// buried-position gate. nil for callers with no live heightmap (tests) — the gate then
	// passes (a faithful degrade: the surface-Y read only rejects buried cells).
	heightmapMBNL func(x, z int) int
}

// WithHeightmapMBNL returns a copy of cfg carrying the live MOTION_BLOCKING_NO_LEAVES
// heightmap accessor the PlaceOnGroundDecorator gate reads. The caller (treeBody) binds it
// to the 3x3 view before PlaceTree.
func (cfg *TreeConfiguration) WithHeightmapMBNL(h func(x, z int) int) *TreeConfiguration {
	c := *cfg
	c.heightmapMBNL = h
	return &c
}

// treeAccum collects the placed log + leaf positions (insertion order, deduplicated) for
// the TreeDecorator.Context — vanilla TreeFeature collects logsCollector/leavesCollector
// Sets; we keep insertion order (the first log is the trunk base, which the decorators'
// getFirst()/min-Y logic relies on — more faithful than HashSet iteration order). roots is
// always empty for the common overworld trees (no root_placer).
type treeAccum struct {
	logs   []TreePos
	leaves []TreePos
	roots  []TreePos
	logSet map[TreePos]bool
	leafSet map[TreePos]bool
}

func newTreeAccum() *treeAccum {
	return &treeAccum{logSet: map[TreePos]bool{}, leafSet: map[TreePos]bool{}}
}

func (a *treeAccum) addLog(p TreePos) {
	if a.logSet[p] {
		return
	}
	a.logSet[p] = true
	a.logs = append(a.logs, p)
}

func (a *treeAccum) addLeaf(p TreePos) {
	if a.leafSet[p] {
		return
	}
	a.leafSet[p] = true
	a.leaves = append(a.leaves, p)
}

// jsonTreeConfig is the on-disk TreeConfiguration "config" object shape (verified
// oak.json/birch.json).
type jsonTreeConfig struct {
	TrunkProvider      json.RawMessage   `json:"trunk_provider"`
	FoliageProvider    json.RawMessage   `json:"foliage_provider"`
	BelowTrunkProvider json.RawMessage   `json:"below_trunk_provider"`
	TrunkPlacer        json.RawMessage   `json:"trunk_placer"`
	FoliagePlacer      json.RawMessage   `json:"foliage_placer"`
	RootPlacer         json.RawMessage   `json:"root_placer"` // OPTIONAL (absent for oak/birch)
	MinimumSize        json.RawMessage   `json:"minimum_size"`
	IgnoreVines        bool              `json:"ignore_vines"`
	Decorators         []json.RawMessage `json:"decorators"`
}

// ParseTreeConfiguration decodes a tree "config" object (the resolved cf.Config.Raw) into
// a TreeConfiguration. The 3 providers parse via ParseProvider; the placers via
// parseTrunkPlacer/parseFoliagePlacer; the optional root_placer via parseRootPlacer
// (nil when absent); the minimum_size via parseFeatureSize. An unported placer/provider
// errors loudly (T-13-03) so the body skips this tree rather than mis-placing.
func ParseTreeConfiguration(raw json.RawMessage) (*TreeConfiguration, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("feature: empty tree config")
	}
	var j jsonTreeConfig
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: tree config: %w", err)
	}
	trunkP, err := ParseProvider(j.TrunkProvider)
	if err != nil {
		return nil, fmt.Errorf("feature: tree trunk_provider: %w", err)
	}
	foliageP, err := ParseProvider(j.FoliageProvider)
	if err != nil {
		return nil, fmt.Errorf("feature: tree foliage_provider: %w", err)
	}
	belowP, err := ParseProvider(j.BelowTrunkProvider)
	if err != nil {
		return nil, fmt.Errorf("feature: tree below_trunk_provider: %w", err)
	}
	trunkPlacer, err := parseTrunkPlacer(j.TrunkPlacer)
	if err != nil {
		return nil, err
	}
	foliagePlacer, err := parseFoliagePlacer(j.FoliagePlacer)
	if err != nil {
		return nil, err
	}
	rootPlacer, err := parseRootPlacer(j.RootPlacer)
	if err != nil {
		return nil, err
	}
	size, err := parseFeatureSize(j.MinimumSize)
	if err != nil {
		return nil, err
	}
	// Decode the common-overworld decorators[] (AlterGround/Beehive/Cocoa/vines). An
	// unported type a COMMON config references errors LOUDLY (-> port here); the special-
	// biome decorators stay routed to 13-03 by ParseTreeDecorator.
	decs := make([]TreeDecorator, 0, len(j.Decorators))
	for i, draw := range j.Decorators {
		d, err := ParseTreeDecorator(draw)
		if err != nil {
			return nil, fmt.Errorf("feature: tree decorators[%d]: %w", i, err)
		}
		decs = append(decs, d)
	}
	return &TreeConfiguration{
		trunkProvider:      trunkP,
		foliageProvider:    foliageP,
		belowTrunkProvider: belowP,
		trunkPlacer:        trunkPlacer,
		foliagePlacer:      foliagePlacer,
		rootPlacer:         rootPlacer,
		minimumSize:        size,
		ignoreVines:        j.IgnoreVines,
		decoratorsRaw:      j.Decorators,
		decorators:         decs,
	}, nil
}

// TrunkHeight draws the trunk column height via the trunk placer's getTreeHeight
// (base + nextInt(a+1) + nextInt(b+1)) — the TWO nextInt draws are the determinism
// contract. Exported for the live body (the placer + the height draw stay in this package).
func (cfg *TreeConfiguration) TrunkHeight(rng levelgen.RandomSource) int {
	return cfg.trunkPlacer.getTreeHeight(rng)
}

// SizeAtLayer exposes minimum_size.getSizeAtLayer(height, depth) for the live body's
// validity scan (the per-layer trunk-footprint radius).
func (cfg *TreeConfiguration) SizeAtLayer(height, depth int) int {
	return cfg.minimumSize.getSizeAtLayer(height, depth)
}

// PosFree exposes the conservative validTreePos test (air-or-replaceable) for the live
// body's footprint scan, so the scan logic lives in `world` while the pure free-test stays
// here (one source of truth for "a tree may grow through this block").
func (cfg *TreeConfiguration) PosFree(read ReadFn, pos TreePos) bool {
	return treePosFree(read, pos)
}

// BelowTrunkWithExisting returns a copy of cfg whose below_trunk_provider is bound to the
// live existing-block read (so its rule_based rules evaluate). The live body calls this
// before placeTrunk; a SimpleStateProvider below-trunk provider is unaffected.
func (cfg *TreeConfiguration) BelowTrunkWithExisting(read ReadFn) *TreeConfiguration {
	if rb, ok := cfg.belowTrunkProvider.(RuleBasedStateProvider); ok {
		c := *cfg
		c.belowTrunkProvider = rb.withExisting(func(x, y, z int) block.StateID { return read(x, y, z) })
		return &c
	}
	return cfg
}

// ---- TreeFeature.place (the pure assembly) ----
//
// PlaceTree is the PURE core of TreeFeature.place -> doPlace, expressed over the set/read
// callbacks + the threaded rng. The live body (world/feature_tree.go) supplies the
// callbacks over the Neighborhood, runs the canPlace validity scan, then calls this. The
// rng draw order — getTreeHeight, (root placer), placeTrunk, createFoliage — is the
// determinism contract.

// PlaceTree assembles the tree at the anchor `pos` following the EXACT 26.2
// TreeFeature.doPlace rng draw order (the determinism contract):
//
//  1. getTreeHeight(rng)                         (already done by the caller -> treeHeight)
//  2. foliageHeight(rng, treeHeight)             (DRAWS for spruce/pine/megapine)
//  3. foliageRadius(rng, treeHeight-foliageHt)   (DRAWS; pine adds an extra draw)
//  4. trunkOrigin = rootPlacer.getTrunkOrigin    (DRAWS trunk_offset_y for mangrove; else pos)
//  5. getMaxFreeTreeHeight(treeHeight, trunkOrigin)  (NO rng — the read scan)
//  6. rootPlacer.placeRoots(pos, trunkOrigin)    (DRAWS the root simulation; mangrove only)
//  7. placeTrunk(rng, freeHeight, trunkOrigin)   (DRAWS per trunk placer)
//  8. for each attachment: createFoliage         (offset(rng) draw + the per-placer rows)
//  9. decorators[].place(rng)                    (AlterGround/Beehive/Cocoa/vines/special)
//
// NOTE the foliageHeight/foliageRadius/trunk_offset_y draws happen BEFORE the scan — the JAR
// truth (TreeFeature.doPlace). For oak/birch (constant providers, no root) these are 0-draw
// so the oak sequence is unchanged. The abort uses the EXACT doPlace condition
// (freeHeight >= treeHeight, else the minClippedHeight escape) — the draws have already
// happened when an abort returns false, so the selector per-feature seed is unaffected.
// Returns whether any block landed.
func PlaceTree(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, treeHeight int, pos TreePos) bool {
	// Collect placed log/leaf positions for the decorators (a per-call accum on a copy so
	// the shared cfg stays immutable).
	c := *cfg
	c.accum = newTreeAccum()
	cfg = &c

	// (2) foliageHeight, (3) foliageRadius — the pre-trunk draws (jar order).
	foliageHeight := cfg.foliagePlacer.foliageHeightOf(rng, treeHeight, cfg)
	foliageRadius := cfg.foliagePlacer.foliageRadiusOf(rng, treeHeight-foliageHeight)

	// (4) trunk origin: the root placer samples trunk_offset_y here (BEFORE the scan); the
	// trunk grows from the shifted origin. Absent root placer -> the anchor pos.
	trunkOrigin := pos
	if cfg.rootPlacer != nil {
		trunkOrigin = cfg.rootPlacer.getTrunkOrigin(rng, pos)
	}

	// (5) the validity scan from the trunk origin (NO rng).
	freeHeight := maxFreeTreeHeight(read, cfg, trunkOrigin, treeHeight)

	// EXACT TreeFeature.doPlace abort condition (javap, offsets 154-180):
	//
	//	if (freeHeight >= treeHeight) { place full-height }
	//	else if (minClippedHeight.isEmpty() || freeHeight < minClippedHeight) return false;
	//	// else: place a clipped (shorter) tree of height freeHeight
	//
	// For the common overworld trees minClippedHeight is EMPTY, so any freeHeight <
	// treeHeight aborts — this is THE anti-stack guard: a column blocked above (by solid
	// ground, or a vine without ignore_vines) yields freeHeight < treeHeight and places
	// NOTHING, instead of stacking a partial tree.
	if freeHeight < treeHeight {
		clip, ok := cfg.minimumSize.minClippedHeight()
		if !ok || freeHeight < clip {
			return false
		}
	}

	// (6) root placer: present -> runs AFTER the scan, growing roots DOWN from the anchor
	// pos (NOT the shifted trunk origin). nil for the common overworld trees.
	if cfg.rootPlacer != nil {
		if !cfg.rootPlacer.placeRoots(set, read, rng, pos, trunkOrigin, cfg) {
			return false
		}
	}
	// (7) trunk.
	attachments := cfg.trunkPlacer.placeTrunk(set, read, rng, freeHeight, trunkOrigin, cfg)
	// (6) foliage: the public createFoliage wrapper samples offset(rng) then calls the
	// protected createFoliage(radius, foliageHeight, offset).
	for _, att := range attachments {
		offset := cfg.foliagePlacer.offsetOf(rng)
		cfg.foliagePlacer.createFoliage(set, read, rng, cfg, att, foliageRadius, foliageHeight, offset)
	}
	// (7) decorators run AFTER trunk+foliage on the SAME threaded rng, fed the accumulated
	// placed-log/placed-leaf positions (the TreeDecorator.Context). Empty for oak/birch.
	if len(cfg.decorators) > 0 {
		dctx := &DecoratorContext{
			Logs:          cfg.accum.logs,
			Leaves:        cfg.accum.leaves,
			Roots:         cfg.accum.roots,
			Rng:           rng,
			Set:           set,
			Read:          read,
			HeightmapMBNL: cfg.heightmapMBNL,
		}
		for _, d := range cfg.decorators {
			d.place(dctx)
		}
	}
	return true
}

// maxFreeTreeHeight ports TreeFeature.getMaxFreeTreeHeight: scan from the base up to
// treeHeight+1 layers; at each layer the trunk footprint is a (2*size+1)^2 square where
// size = minimum_size.getSizeAtLayer(treeHeight, depth). The scan returns the number of free
// layers below the FIRST blocked one (capped at treeHeight). A position is "free" if the
// existing block is air-or-replaceable (the conservative validTreePos test). NO rng — a pure
// read scan, matching the jar. Lives here (not in `world`) so the trunk_offset_y draw can
// sit BETWEEN foliageRadius and the scan (the jar doPlace order).
func maxFreeTreeHeight(read ReadFn, cfg *TreeConfiguration, origin TreePos, treeHeight int) int {
	// EXACT TreeFeature.getMaxFreeTreeHeight (javap): loop i = 0..treeHeight+1, scan the
	// (2*size+1)^2 trunk footprint at each layer, and on the FIRST position that is not
	// free — OR is a vine when !ignoreVines — return i-2 (always i-2: no `i>=treeHeight`
	// special case). If nothing blocks through treeHeight+1, return treeHeight.
	for depth := 0; depth <= treeHeight+1; depth++ {
		size := cfg.SizeAtLayer(treeHeight, depth)
		baseY := origin.Y + depth
		for dx := -size; dx <= size; dx++ {
			for dz := -size; dz <= size; dz++ {
				p := TreePos{X: origin.X + dx, Y: baseY, Z: origin.Z + dz}
				// !isFree(p) || (!ignoreVines && isVine(p)) -> blocked layer.
				if !treeIsFree(read, p) || (!cfg.ignoreVines && isVine(read, p)) {
					return depth - 2
				}
			}
		}
	}
	return treeHeight
}

// ---- shared helpers ----

// treePosFree ports TreeFeature.validTreePos (javap TreeFeature.lambda$validTreePos$0):
//
//	validTreePos(pos) = state.isAir() || state.is(BlockTags.REPLACEABLE_BY_TREES)
//
// This is the predicate placeLog / tryPlaceLeaf gate on (TrunkPlacer.placeLog calls
// validTreePos; FoliagePlacer.tryPlaceLeaf calls validTreePos). It is NOT the scan's
// isFree (that one ALSO allows logs — see treeIsFree). REPLACEABLE_BY_TREES is the
// vanilla tag (leaves, small_flowers, grass/ferns, vine, water, ...) resolved from the
// embedded data/minecraft/tags/block/replaceable_by_trees.json — authoritative, not
// hand-transcribed.
func treePosFree(read ReadFn, pos TreePos) bool {
	st := read(pos.X, pos.Y, pos.Z)
	return block.IsAir(st) || treeReplaceableStates[st]
}

// treeIsFree ports TrunkPlacer.isFree (javap TrunkPlacer.isFree):
//
//	isFree(pos) = validTreePos(pos) || state.is(BlockTags.LOGS)
//
// This is the predicate getMaxFreeTreeHeight uses for its column scan. It is strictly
// WIDER than validTreePos: an existing log counts as free (so a trunk may grow up
// alongside / through logs), but leaves are free only because REPLACEABLE_BY_TREES
// contains #minecraft:leaves. Solid ground (dirt, stone, planks, ...) is NOT free, so a
// column blocked by solid material short-circuits the scan -> i-2 -> abort.
func treeIsFree(read ReadFn, pos TreePos) bool {
	st := read(pos.X, pos.Y, pos.Z)
	return block.IsAir(st) || treeReplaceableStates[st] || treeLogStates[st]
}

// isVine ports TreeFeature.isVine (javap TreeFeature.lambda$isVine$0):
//
//	isVine(pos) = state.is(Blocks.VINE)
//
// getMaxFreeTreeHeight treats a vine as BLOCKING (unless cfg.ignoreVines) even though
// vine is in REPLACEABLE_BY_TREES and would otherwise read as free — the exact
// `!isFree(pos) || (!ignoreVines && isVine(pos))` scan guard.
func isVine(read ReadFn, pos TreePos) bool {
	return treeVineStates[read(pos.X, pos.Y, pos.Z)]
}

// treeReplaceableStates is the flat set of state ids in #minecraft:replaceable_by_trees,
// treeLogStates the flat set in #minecraft:logs, treeVineStates the minecraft:vine
// states. All three are built once from the embedded vanilla block tags so membership is
// jar-authoritative (TreeFeature.validTreePos / TrunkPlacer.isFree / TreeFeature.isVine).
var (
	treeReplaceableStates = buildTagStateSet("replaceable_by_trees")
	treeLogStates         = buildTagStateSet("logs")
	treeVineStates        = buildBlockStateSet(map[string]bool{"minecraft:vine": true})
)

// buildTagStateSet resolves the named block tag (recursively, via the embedded tag
// JSONs) to the set of every block-state id whose block id is a tag member. A missing
// tag is a build-data corruption -> panic at init (loud, not a silent wrong placement).
func buildTagStateSet(tag string) map[block.StateID]bool {
	members, err := data.BlockTag(tag)
	if err != nil {
		panic("tree: resolve block tag " + tag + ": " + err.Error())
	}
	return buildBlockStateSet(members)
}

// buildBlockStateSet maps a set of block ids to the set of all their state ids.
func buildBlockStateSet(blockIDs map[string]bool) map[block.StateID]bool {
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if blockIDs[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set
}

// abs is a tiny int abs (avoids a math import for the corner-trim rule).
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// min2 is a tiny int min (for the doubleTrunk fold in shouldSkipLocationSigned).
func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// javaDiv ports Java integer division (truncate toward zero) for the i/2 in the blob row
// radius. Go's `/` already truncates toward zero for ints, so this is `a / b`; named for
// the jar-faithfulness intent at the call site (i can be negative).
func javaDiv(a, b int) int { return a / b }

// compile-time assertions: the placers satisfy their interfaces.
var (
	_ TrunkPlacer   = StraightTrunkPlacer{}
	_ FoliagePlacer = BlobFoliagePlacer{}
	_ featureSize   = twoLayersFeatureSize{}
	_ featureSize   = threeLayersFeatureSize{}
)
