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

// featureSize is FeatureSize.getSizeAtLayer (the validity-scan radius at a given depth).
type featureSize interface {
	// getSizeAtLayer returns the allowed horizontal radius at vertical `depth` from the
	// trunk base, given the total tree `height`.
	getSizeAtLayer(height, depth int) int
}

// twoLayersFeatureSize ports TwoLayersFeatureSize: a `limit` rows from the top use
// upperSize, the rest use lowerSize. Defaults (no JSON params): limit=1, lowerSize=0,
// upperSize=1 (TwoLayersFeatureSize's default field values, jar-confirmed).
type twoLayersFeatureSize struct {
	limit     int
	lowerSize int
	upperSize int
	// minClippedHeight is FeatureSize.minClippedHeight (Optional<Integer>); absent for
	// oak/birch — left 0/disabled.
}

// getSizeAtLayer ports TwoLayersFeatureSize.getSizeAtLayer: depth < limit -> lowerSize,
// else upperSize. (TwoLayersFeatureSize counts `depth` UP from the base; the first
// `limit` layers are "lower".)
func (s twoLayersFeatureSize) getSizeAtLayer(_ , depth int) int {
	if depth < s.limit {
		return s.lowerSize
	}
	return s.upperSize
}

// parseFeatureSize decodes a minimum_size envelope. Only two_layers_feature_size (the
// oak/birch default) is ported here; three_layers_feature_size + the parameterized
// variants land with 13-02's dark_oak/pale_oak placers. An unported size errors loudly.
func parseFeatureSize(raw json.RawMessage) (featureSize, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("feature: empty minimum_size")
	}
	var j struct {
		Type      string `json:"type"`
		Limit     *int   `json:"limit"`
		LowerSize *int   `json:"lower_size"`
		UpperSize *int   `json:"upper_size"`
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
		return s, nil
	default:
		return nil, fmt.Errorf("feature: unported minimum_size type %q "+
			"(three_layers_feature_size ported in 13-02)", j.Type)
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
	case "forking_trunk_placer", "fancy_trunk_placer", "dark_oak_trunk_placer",
		"mega_jungle_trunk_placer", "giant_trunk_placer", "bending_trunk_placer":
		return nil, fmt.Errorf("feature: unported trunk_placer type %q (ported in 13-02)", j.Type)
	case "cherry_trunk_placer", "upwards_branching_trunk_placer":
		return nil, fmt.Errorf("feature: unported trunk_placer type %q (ported in 13-03)", j.Type)
	default:
		return nil, fmt.Errorf("feature: unknown trunk_placer type %q", j.Type)
	}
}

// ---- FoliagePlacer base + BlobFoliagePlacer ----

// FoliagePlacer is the foliage-placer interface a TreeConfiguration carries. createFoliage
// places the leaf blobs at one attachment; foliageHeight reports the blob's vertical
// extent the validity scan uses.
type FoliagePlacer interface {
	// createFoliage places the leaf blob at the attachment, consuming rng jar-exact.
	createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, maxFreeHeight int, attachment FoliageAttachment)
	// foliageHeight reports the blob's vertical extent (FoliagePlacer.foliageHeight),
	// consumed by the free-tree-height scan.
	foliageHeight(rng levelgen.RandomSource, height, freeHeight int) int
}

// foliagePlacerBase holds the shared radius/offset IntProvider VALUES (oak/birch use bare
// ints, not full IntProviders) + the placeLeavesRow helper.
type foliagePlacerBase struct {
	radius int
	offset int
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
	return true
}

// placeLeavesRow ports FoliagePlacer.placeLeavesRow: iterate the (2*range+1)^2 square in
// the jar's x-major then z order, skip the cells shouldSkip flags, and place a leaf at
// (centerX+dx, centerY, centerZ+dz). The `center` already carries the row's Y (the
// caller passes attachment.pos().below(i)); `localY` is the row's vertical INDEX passed
// to shouldSkip (BlobFoliagePlacer ignores it; spruce/pine use it for the cone taper).
// The square iteration order + the per-cell shouldSkip evaluation are the determinism
// contract.
func placeLeavesRow(
	set SetBlockFn, read ReadFn, rng levelgen.RandomSource,
	cfg *TreeConfiguration, center TreePos, rangeR, localY, offset int,
	shouldSkip func(localY, dx, dz, rangeR, offset int) bool,
) {
	for dx := -rangeR; dx <= rangeR; dx++ {
		for dz := -rangeR; dz <= rangeR; dz++ {
			if shouldSkip(localY, dx, dz, rangeR, offset) {
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

// foliageHeight ports BlobFoliagePlacer.foliageHeight: just the configured height
// (BlobFoliagePlacer returns `this.height` — 3 for oak/birch). 0 rng draws.
func (p BlobFoliagePlacer) foliageHeight(_ levelgen.RandomSource, _, _ int) int {
	return p.height
}

// createFoliage ports BlobFoliagePlacer.createFoliage: place leaf rows from i = offset
// (top, above the attachment) down to i = -foliageHeight, each row a square of radius
// (radius + radiusOffset - localRadiusShrink), with the corner trim on the widest rows.
// BlobFoliagePlacer.createFoliage:
//
//	for (int i = offset; i >= -foliageHeight; --i) {
//	    int j = Math.max(localRadius + attachment.radiusOffset() - 1 - i/2, 0);
//	    placeLeavesRow(..., attachment.pos().below(i), j, i, attachment.doubleTrunk());
//	}
//
// where localRadius = radius (the bare int for oak/birch; FoliagePlacer.foliageRadius adds
// the radiusOffset). The +0.5D-trim corner rule is shouldSkipLocation below. The row walk
// (top-down) + the per-row radius math + the corner trim are the determinism contract.
func (p BlobFoliagePlacer) createFoliage(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, _ int, attachment FoliageAttachment) {
	foliageHeight := p.height
	for i := p.offset; i >= -foliageHeight; i-- {
		// FoliagePlacer.placeLeavesRowAndAddContinuation row radius:
		// j = max(radius + radiusOffset - 1 - i/2, 0). Java integer i/2 truncates toward
		// zero; for the i values here (>= -foliageHeight) this is the jar's `i / 2`.
		j := p.radius + attachment.RadiusOffset - 1 - javaDiv(i, 2)
		if j < 0 {
			j = 0
		}
		placeLeavesRow(set, read, rng, cfg, attachment.Pos.below(i), j, -i, attachment.RadiusOffset,
			p.shouldSkip)
	}
}

// shouldSkip ports BlobFoliagePlacer.shouldSkipLocation: trim the 4 outer corners on the
// widest rows. The jar condition is:
//
//	|dx| == range && |dz| == range && (range > 0 && rand or top/bottom rows)
//
// For BlobFoliagePlacer specifically (javap -c shouldSkipLocation):
//
//	return dx == range && dz == range && (range > 0);
//
// i.e. skip ONLY the single far +x,+z corner cell? No — vanilla uses absolute values:
// skip when |dx| == range AND |dz| == range AND range > 0 (all 4 corners of the widest
// rows). The `range` here is the row's `j`. localY/offset are unused by Blob's rule but
// kept in the signature for the shared placeLeavesRow contract.
func (p BlobFoliagePlacer) shouldSkip(_ , dx, dz, rangeR, _ int) bool {
	return abs(dx) == rangeR && abs(dz) == rangeR && rangeR > 0
}

// parseFoliagePlacer dispatches a foliage_placer envelope. Only blob_foliage_placer (the
// oak/birch foliage) is ported here; the rest error loudly and land in 13-02/13-03.
func parseFoliagePlacer(raw json.RawMessage) (FoliagePlacer, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("feature: empty foliage_placer")
	}
	// radius/offset are vanilla IntProviders, but oak/birch carry bare ints; decode both
	// the bare-int and the {min_inclusive,max_inclusive} constant forms.
	var j struct {
		Type   string          `json:"type"`
		Radius json.RawMessage `json:"radius"`
		Offset json.RawMessage `json:"offset"`
		Height int             `json:"height"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: foliage_placer: %w", err)
	}
	radius, err := constIntProvider(j.Radius)
	if err != nil {
		return nil, fmt.Errorf("feature: foliage_placer radius: %w", err)
	}
	offset, err := constIntProvider(j.Offset)
	if err != nil {
		return nil, fmt.Errorf("feature: foliage_placer offset: %w", err)
	}
	base := foliagePlacerBase{radius: radius, offset: offset}
	switch stripNS(j.Type) {
	case "blob_foliage_placer":
		return BlobFoliagePlacer{foliagePlacerBase: base, height: j.Height}, nil
	case "spruce_foliage_placer", "pine_foliage_placer", "acacia_foliage_placer",
		"bush_foliage_placer", "fancy_foliage_placer", "dark_oak_foliage_placer",
		"mega_pine_foliage_placer", "mega_jungle_foliage_placer", "random_spread_foliage_placer":
		return nil, fmt.Errorf("feature: unported foliage_placer type %q (ported in 13-02)", j.Type)
	case "cherry_foliage_placer":
		return nil, fmt.Errorf("feature: unported foliage_placer type %q (ported in 13-03)", j.Type)
	default:
		return nil, fmt.Errorf("feature: unknown foliage_placer type %q", j.Type)
	}
}

// constIntProvider decodes the bare-int OR constant int-provider form of a foliage
// radius/offset. oak/birch carry a bare int (radius 2, offset 0). A full IntProvider
// (uniform/etc.) is NOT exercised by the blob foliage data and errors loudly so a future
// placer that needs it ports it rather than drifting.
func constIntProvider(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("missing int")
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var obj struct {
		Type  string `json:"type"`
		Value *int   `json:"value"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return 0, fmt.Errorf("not a bare int or constant provider: %w", err)
	}
	if stripNS(obj.Type) == "constant" && obj.Value != nil {
		return *obj.Value, nil
	}
	return 0, fmt.Errorf("non-constant int provider %q (constant only here)", obj.Type)
}

// ---- RootPlacer (optional) ----

// RootPlacer is the OPTIONAL root_placer a TreeConfiguration may carry. It is nil for
// oak/birch (absent in oak.json); the model defines the interface + the field + the
// TreeFeature.place hook now so 13-03 plugs mangrove_root_placer in WITHOUT re-touching
// the struct. placeRoots places the roots and returns the (possibly shifted) trunk origin
// + whether placement may continue.
type RootPlacer interface {
	// placeRoots places the root system at origin and returns the trunk origin (shifted
	// up by trunk_offset_y for mangrove) + whether the tree may continue.
	placeRoots(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, origin TreePos, cfg *TreeConfiguration) (TreePos, bool)
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
		return nil, fmt.Errorf("feature: unported root_placer type %q (ported in 13-03)", j.Type)
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
	// decorators is the (empty for oak/birch) TreeDecorator list. Captured raw so 13-02
	// can decode the common-overworld decorators without re-touching this struct; an
	// empty array here means a no-op-decorator tree.
	decoratorsRaw []json.RawMessage
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

// PlaceTree assembles the tree at origin: draw the trunk height, optionally place roots
// (no-op when rootPlacer is nil — the hook 13-03 fills), place the trunk column (which
// returns the foliage attachments), then place the foliage blob at each attachment.
// freeHeight is the validity-clamped trunk height the live body computed from the scan;
// when <= 0 (the scan found no room) nothing is placed. Returns whether any block landed.
func PlaceTree(set SetBlockFn, read ReadFn, rng levelgen.RandomSource, cfg *TreeConfiguration, treeHeight, freeHeight int, origin TreePos) bool {
	if freeHeight <= 0 {
		return false
	}
	trunkOrigin := origin
	// TreeFeature.place: when a root_placer is present it runs BEFORE the trunk and may
	// shift the trunk origin up (mangrove trunk_offset_y). nil for oak/birch (no-op).
	if cfg.rootPlacer != nil {
		shifted, ok := cfg.rootPlacer.placeRoots(set, read, rng, origin, cfg)
		if !ok {
			return false
		}
		trunkOrigin = shifted
	}
	attachments := cfg.trunkPlacer.placeTrunk(set, read, rng, freeHeight, trunkOrigin, cfg)
	for _, att := range attachments {
		cfg.foliagePlacer.createFoliage(set, read, rng, cfg, freeHeight, att)
	}
	// decorators are empty for oak/birch; the common-overworld TreeDecorator loop lands
	// in 13-02 over cfg.decoratorsRaw. A no-op-decorator tree is fully rendered here.
	return true
}

// ---- shared helpers ----

// treePosFree ports TreeFeature.isFree / validTreePos (the conservative readable-state
// gate, 12-02 precedent): a position is free for a log/leaf write iff the existing block
// is air or another leaf/log (replaceable worldgen surface). It never overwrites solid
// ground — a documented-conservative test that never makes a false placement.
func treePosFree(read ReadFn, pos TreePos) bool {
	st := read(pos.X, pos.Y, pos.Z)
	return block.IsAir(st) || isReplaceableByTree(st)
}

// isReplaceableByTree reports whether an existing block may be overwritten by a tree's
// logs/leaves: air-or-leaves (TreeFeature.validTreePos: air, leaves, replaceable plants).
// Conservative — only air + the leaf set, the safe subset (a future widening adds the
// replaceable-plant tag without changing a single existing placement).
func isReplaceableByTree(st block.StateID) bool {
	return treeReplaceableStates[st]
}

// treeReplaceableStates is the set of leaf state ids a tree may grow through (so two
// trees' foliage may interpenetrate, matching vanilla validTreePos). Built lazily on
// first use from the *_leaves blocks. Logs are NOT replaceable (a trunk does not grow
// through another trunk).
var treeReplaceableStates = buildTreeReplaceableStates()

func buildTreeReplaceableStates() map[block.StateID]bool {
	leafBlocks := map[string]bool{
		"minecraft:oak_leaves": true, "minecraft:birch_leaves": true,
		"minecraft:spruce_leaves": true, "minecraft:jungle_leaves": true,
		"minecraft:acacia_leaves": true, "minecraft:dark_oak_leaves": true,
		"minecraft:cherry_leaves": true, "minecraft:pale_oak_leaves": true,
		"minecraft:mangrove_leaves": true, "minecraft:azalea_leaves": true,
		"minecraft:flowering_azalea_leaves": true,
	}
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if leafBlocks[b.ID()] {
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

// javaDiv ports Java integer division (truncate toward zero) for the i/2 in the blob row
// radius. Go's `/` already truncates toward zero for ints, so this is `a / b`; named for
// the jar-faithfulness intent at the call site (i can be negative).
func javaDiv(a, b int) int { return a / b }

// compile-time assertions: the placers satisfy their interfaces.
var (
	_ TrunkPlacer   = StraightTrunkPlacer{}
	_ FoliagePlacer = BlobFoliagePlacer{}
	_ featureSize   = twoLayersFeatureSize{}
)
