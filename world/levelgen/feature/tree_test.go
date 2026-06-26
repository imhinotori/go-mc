package feature

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// tree_test.go pins the PURE tree placers (StraightTrunkPlacer + BlobFoliagePlacer + the
// TrunkPlacer/FoliagePlacer bases) + the TreeConfiguration parse, draw-order pinned vs an
// independent in-test oracle that replays the same rng. A reorder of any draw fails these
// (T-13-01, the determinism contract). The placers run against map-backed set/read
// callbacks so the tests exercise them with NO world import.

// mapWorld is a tiny set/read backing for the pure placers: writes land in a map, reads
// return the stored state (air for an unwritten cell). It mirrors the Neighborhood seam
// the live body provides, but with zero deps.
type mapWorld struct {
	blocks map[[3]int]block.StateID
	air    block.StateID
}

func newMapWorld() *mapWorld {
	return &mapWorld{blocks: map[[3]int]block.StateID{}, air: block.ToStateID[block.Air{}]}
}

func (m *mapWorld) set(x, y, z int, st block.StateID) { m.blocks[[3]int{x, y, z}] = st }
func (m *mapWorld) read(x, y, z int) block.StateID {
	if st, ok := m.blocks[[3]int{x, y, z}]; ok {
		return st
	}
	return m.air
}

// oakConfig builds a TreeConfiguration for the embedded oak.json shape (used by the placer
// tests so the providers resolve real oak_log/oak_leaves states).
func oakConfig(t *testing.T) *TreeConfiguration {
	t.Helper()
	reg := NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:oak")
	if err != nil {
		t.Fatalf("ResolveConfigured(oak): %v", err)
	}
	cfg, err := ParseTreeConfiguration(cf.Config.Raw)
	if err != nil {
		t.Fatalf("ParseTreeConfiguration(oak): %v", err)
	}
	return cfg
}

// TestGetTreeHeight pins TrunkPlacer.getTreeHeight = base + nextInt(a+1) + nextInt(b+1)
// against an oracle replaying the same two draws, for the oak params (4,2,0).
func TestGetTreeHeight(t *testing.T) {
	base := trunkPlacerBase{baseHeight: 4, heightRandA: 2, heightRandB: 0}
	const seed = int64(0xA11CE)
	rng := levelgen.NewWorldgenRandom(seed)
	got := base.getTreeHeight(rng)

	// Oracle: replay the EXACT two draws (nextInt(3) then nextInt(1)).
	oracle := levelgen.NewWorldgenRandom(seed)
	want := 4 + int(oracle.NextIntN(3)) + int(oracle.NextIntN(1))
	if got != want {
		t.Fatalf("getTreeHeight = %d, want %d (base 4 + nextInt(3) + nextInt(1))", got, want)
	}
	// The post-call rng state must match the oracle (exactly two draws consumed).
	if rng.NextInt() != oracle.NextInt() {
		t.Fatalf("getTreeHeight consumed the wrong number of draws (expected exactly two nextInt)")
	}
}

// TestStraightTrunkPlacer pins the dirt-below + the log column + the returned
// FoliageAttachment for a fixed freeHeight, draw-pinned vs an oracle. The trunk_provider
// is simple (0 draws) and the below_trunk provider rule resolves to simple dirt (0
// draws), so the WHOLE placeTrunk consumes ZERO rng — the strongest determinism pin.
func TestStraightTrunkPlacer(t *testing.T) {
	cfg := oakConfig(t)
	mw := newMapWorld()
	// Bind the rule_based below_trunk provider to the live read so its rule evaluates.
	cfg = cfg.BelowTrunkWithExisting(mw.read)

	origin := TreePos{X: 8, Y: 70, Z: 8}
	const freeHeight = 5
	const seed = int64(0xBEEF)
	rng := levelgen.NewWorldgenRandom(seed)

	stp := StraightTrunkPlacer{trunkPlacerBase: trunkPlacerBase{baseHeight: 4, heightRandA: 2, heightRandB: 0}}
	atts := stp.placeTrunk(mw.set, mw.read, rng, freeHeight, origin, cfg)

	oakLog := block.ToStateID[block.OakLog{Axis: block.Y}]
	dirt := block.ToStateID[block.Dirt{}]

	// dirt below the trunk origin.
	if got := mw.read(origin.X, origin.Y-1, origin.Z); got != dirt {
		t.Fatalf("below-trunk block = %v, want dirt %v", got, dirt)
	}
	// freeHeight logs up the column.
	for i := 0; i < freeHeight; i++ {
		if got := mw.read(origin.X, origin.Y+i, origin.Z); got != oakLog {
			t.Fatalf("log at +%d = %v, want oak_log %v", i, got, oakLog)
		}
	}
	// no log above the top of the column.
	if got := mw.read(origin.X, origin.Y+freeHeight, origin.Z); got == oakLog {
		t.Fatalf("unexpected log at +%d (above the column top)", freeHeight)
	}
	// the single foliage attachment at origin.above(freeHeight).
	if len(atts) != 1 {
		t.Fatalf("placeTrunk returned %d attachments, want 1", len(atts))
	}
	wantPos := TreePos{X: origin.X, Y: origin.Y + freeHeight, Z: origin.Z}
	if atts[0].Pos != wantPos || atts[0].RadiusOffset != 0 || atts[0].DoubleTrunk {
		t.Fatalf("attachment = %+v, want {pos %+v, radiusOffset 0, doubleTrunk false}", atts[0], wantPos)
	}

	// Draw-pin: simple trunk provider + dirt-rule below-trunk provider draw ZERO rng.
	oracle := levelgen.NewWorldgenRandom(seed)
	if rng.NextInt() != oracle.NextInt() {
		t.Fatalf("placeTrunk consumed rng (expected ZERO draws: simple+rule_based-to-simple)")
	}
}

// TestBlobFoliagePlacer pins the leaf-blob rows + the corner trim for the oak foliage
// (radius 2, offset 0, height 3) at a known attachment. The foliage_provider is simple (0
// draws), so the WHOLE blob consumes ZERO rng; the geometry is the contract. The expected
// cell set is computed by an independent in-test replication of the blob row math + the
// corner-trim rule (the oracle).
func TestBlobFoliagePlacer(t *testing.T) {
	cfg := oakConfig(t)
	mw := newMapWorld()

	bfp := BlobFoliagePlacer{foliagePlacerBase: foliagePlacerBase{radius: constantIntProvider{2}, offset: constantIntProvider{0}}, height: 3}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 0, Z: 0}, RadiusOffset: 0, DoubleTrunk: false}

	const seed = int64(0xF0)
	const foliageRadius, foliageHeight, offset = 2, 3, 0
	rng := levelgen.NewWorldgenRandom(seed)
	bfp.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)

	oakLeaves := block.ToStateID[block.OakLeaves{Distance: 7, Persistent: false, Waterlogged: false}]

	// Oracle: independently replicate the 26.2 blob math + the corner nextInt(2) draw, on a
	// FRESH rng with the SAME seed so the draw sequence matches cell-for-cell.
	//   for i = offset(0); i >= offset - foliageHeight(3); i--:
	//     j = max(radius(2) + radiusOffset(0) - 1 - i/2, 0)
	//     row center y = i (pos.above(i) == pos.Y + i, vanilla setWithOffset(pos.Y+localY)).
	//     per cell |dx|<=j, |dz|<=j:
	//       corner (|dx|==j && |dz|==j): skip = nextInt(2)!=0 || i==0
	// NOTE: the row Y is now +i (was -i, the upside-down bug). The widest rows (j=2 at
	// i=-2,-3) therefore sit at the LOWEST Y (-2,-3) — wide-at-bottom, narrow-at-top, the
	// correct vanilla blob orientation.
	oracle := levelgen.NewWorldgenRandom(seed)
	want := map[[3]int]bool{}
	for i := 0; i >= offset-foliageHeight; i-- {
		j := foliageRadius + 0 - 1 - (i / 2)
		if j < 0 {
			j = 0
		}
		y := i // pos.above(i).Y == i (pos.Y is 0); vanilla row.Y = pos.Y + localY
		for dx := -j; dx <= j; dx++ {
			for dz := -j; dz <= j; dz++ {
				if abs(dx) == j && abs(dz) == j {
					if oracle.NextIntN(2) != 0 || i == 0 {
						continue // skipped
					}
				}
				want[[3]int{dx, y, dz}] = true
			}
		}
	}

	// Every expected cell is an oak leaf; no cell outside the expected set was written.
	for cell := range want {
		if got := mw.read(cell[0], cell[1], cell[2]); got != oakLeaves {
			t.Fatalf("expected oak leaf at %v, got %v", cell, got)
		}
	}
	if len(mw.blocks) != len(want) {
		t.Fatalf("blob wrote %d cells, oracle expects %d (corner trim / row math drift)", len(mw.blocks), len(want))
	}
	for cell := range mw.blocks {
		if !want[cell] {
			t.Fatalf("blob wrote an unexpected cell %v (outside the oracle set)", cell)
		}
	}

	// Draw-pin: the blob's ONLY draws are the corner nextInt(2) calls — the oracle replayed
	// exactly those, so the two rng states must now match.
	if rng.NextInt() != oracle.NextInt() {
		t.Fatalf("createFoliage draw count diverged from the oracle (corner nextInt(2) drift)")
	}
}

// TestBlobFoliageWideAtBottom is the upside-down-foliage regression guard. Vanilla
// FoliagePlacer.placeLeavesRow uses setWithOffset(pos, dx, localY, dz) => row.Y = pos.Y +
// localY, and BlobFoliagePlacer walks i = offset .. offset-foliageHeight with j (the row
// radius) WIDEST at the most-negative i. So the widest leaf row must sit at the LOWEST Y
// (below the attachment / trunk top), the narrow rows at the top — a wide-bottom/narrow-top
// blob. A previous bug used pos.below(i) (= pos.Y - i), mirroring the blob vertically so the
// wide part grew ABOVE the trunk (a real client saw upside-down trees in the Phase 17 visual
// gate). This test asserts the orientation invariant so that regression cannot return.
func TestBlobFoliageWideAtBottom(t *testing.T) {
	cfg := oakConfig(t)
	mw := newMapWorld()

	bfp := BlobFoliagePlacer{foliagePlacerBase: foliagePlacerBase{radius: constantIntProvider{2}, offset: constantIntProvider{0}}, height: 3}
	// Attachment at Y=100 (the trunk top): rows must grow DOWNWARD from here.
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 100, Z: 0}, RadiusOffset: 0, DoubleTrunk: false}

	const seed = int64(0xF0)
	const foliageRadius, foliageHeight, offset = 2, 3, 0
	rng := levelgen.NewWorldgenRandom(seed)
	bfp.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)

	// Per row Y, find the half-width (max |dx| / |dz| of any written leaf) = that row's radius.
	widthByY := map[int]int{}
	minY, maxY := 1<<31, -(1 << 31)
	for cell := range mw.blocks {
		x, y, z := cell[0], cell[1], cell[2]
		w := abs(x)
		if abs(z) > w {
			w = abs(z)
		}
		if w > widthByY[y] {
			widthByY[y] = w
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}
	if len(widthByY) == 0 {
		t.Fatalf("blob wrote no leaves")
	}

	// Invariant 1: every leaf row sits at or below the attachment Y (the blob grows DOWN from
	// the trunk top, never above it). attachment.Y == 100, offset == 0 so the top row is Y=100.
	if maxY > att.Pos.Y {
		t.Fatalf("leaf row above the attachment Y=%d (maxY=%d): blob is upside-down", att.Pos.Y, maxY)
	}

	// Invariant 2: the WIDEST row is at the LOWEST Y. Compute the Y of the max width and assert
	// it is strictly below the Y of the min (narrowest) width — wide-at-bottom, narrow-at-top.
	widestW, widestY := -1, 0
	narrowW, narrowY := 1<<31, 0
	for y, w := range widthByY {
		if w > widestW || (w == widestW && y < widestY) {
			widestW, widestY = w, y
		}
		if w < narrowW || (w == narrowW && y > narrowY) {
			narrowW, narrowY = w, y
		}
	}
	if widestY >= narrowY {
		t.Fatalf("widest row (w=%d) at Y=%d is not below the narrowest row (w=%d) at Y=%d: foliage not wide-at-bottom",
			widestW, widestY, narrowW, narrowY)
	}
	// And the widest row must be the bottom-most row of the blob.
	if widestY != minY {
		t.Fatalf("widest row at Y=%d is not the bottom row (minY=%d): foliage not wide-at-bottom", widestY, minY)
	}
}

// TestParseTreeConfigurationOak asserts the REAL embedded oak.json decodes: oak_log
// trunk, oak_leaves foliage, below_trunk_provider as a rule_based provider, bare
// two_layers_feature_size, ignore_vines true, NO force_dirt, empty decorators, rootPlacer
// nil.
func TestParseTreeConfigurationOak(t *testing.T) {
	cfg := oakConfig(t)

	// trunk_provider -> oak_log{axis:y}.
	oakLog := block.ToStateID[block.OakLog{Axis: block.Y}]
	if st := cfg.trunkProvider.GetState(levelgen.NewWorldgenRandom(1), 0, 0, 0); st != oakLog {
		t.Fatalf("trunk_provider yields %v, want oak_log{axis:y} %v", st, oakLog)
	}
	// foliage_provider -> oak_leaves.
	oakLeaves := block.ToStateID[block.OakLeaves{Distance: 7, Persistent: false, Waterlogged: false}]
	if st := cfg.foliageProvider.GetState(levelgen.NewWorldgenRandom(1), 0, 0, 0); st != oakLeaves {
		t.Fatalf("foliage_provider yields %v, want oak_leaves %v", st, oakLeaves)
	}
	// below_trunk_provider is a rule_based provider (the dirt-under-trunk rule, NOT a
	// force_dirt key). Bound to a non-trunk existing block it yields dirt.
	if _, ok := cfg.belowTrunkProvider.(RuleBasedStateProvider); !ok {
		t.Fatalf("below_trunk_provider is %T, want RuleBasedStateProvider", cfg.belowTrunkProvider)
	}
	bound := cfg.BelowTrunkWithExisting(func(x, y, z int) block.StateID { return block.ToStateID[block.Stone{}] })
	dirt := block.ToStateID[block.Dirt{}]
	if st := bound.belowTrunkProvider.GetState(levelgen.NewWorldgenRandom(1), 0, 0, 0); st != dirt {
		t.Fatalf("below_trunk_provider over stone yields %v, want dirt %v (the rule's then-provider)", st, dirt)
	}
	// minimum_size is the bare two_layers_feature_size (defaults: limit 1, lower 0, upper 1).
	tl, ok := cfg.minimumSize.(twoLayersFeatureSize)
	if !ok {
		t.Fatalf("minimum_size is %T, want twoLayersFeatureSize", cfg.minimumSize)
	}
	if tl.limit != 1 || tl.lowerSize != 0 || tl.upperSize != 1 {
		t.Fatalf("two_layers defaults = {limit %d lower %d upper %d}, want {1 0 1}", tl.limit, tl.lowerSize, tl.upperSize)
	}
	if tl.getSizeAtLayer(7, 0) != 0 || tl.getSizeAtLayer(7, 1) != 1 {
		t.Fatalf("getSizeAtLayer wrong: depth0=%d (want 0), depth1=%d (want 1)", tl.getSizeAtLayer(7, 0), tl.getSizeAtLayer(7, 1))
	}
	// ignore_vines true, empty decorators, NO root placer.
	if !cfg.ignoreVines {
		t.Fatalf("ignore_vines = false, want true (oak.json)")
	}
	if len(cfg.decoratorsRaw) != 0 {
		t.Fatalf("decorators len = %d, want 0 (oak.json empty)", len(cfg.decoratorsRaw))
	}
	if cfg.rootPlacer != nil {
		t.Fatalf("rootPlacer = %v, want nil (oak has no root_placer)", cfg.rootPlacer)
	}
}

// TestParseTreeConfigurationBirch asserts birch.json decodes with birch_log/birch_leaves
// and base_height 5.
func TestParseTreeConfigurationBirch(t *testing.T) {
	reg := NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:birch")
	if err != nil {
		t.Fatalf("ResolveConfigured(birch): %v", err)
	}
	cfg, err := ParseTreeConfiguration(cf.Config.Raw)
	if err != nil {
		t.Fatalf("ParseTreeConfiguration(birch): %v", err)
	}
	birchLog := block.ToStateID[block.BirchLog{Axis: block.Y}]
	if st := cfg.trunkProvider.GetState(levelgen.NewWorldgenRandom(1), 0, 0, 0); st != birchLog {
		t.Fatalf("birch trunk_provider yields %v, want birch_log %v", st, birchLog)
	}
	birchLeaves := block.ToStateID[block.BirchLeaves{Distance: 7, Persistent: false, Waterlogged: false}]
	if st := cfg.foliageProvider.GetState(levelgen.NewWorldgenRandom(1), 0, 0, 0); st != birchLeaves {
		t.Fatalf("birch foliage_provider yields %v, want birch_leaves %v", st, birchLeaves)
	}
	stp, ok := cfg.trunkPlacer.(StraightTrunkPlacer)
	if !ok {
		t.Fatalf("birch trunk_placer is %T, want StraightTrunkPlacer", cfg.trunkPlacer)
	}
	if stp.baseHeight != 5 {
		t.Fatalf("birch base_height = %d, want 5", stp.baseHeight)
	}
}

// TestParseTrunkPlacerUnported asserts the special-biome placers are now PORTED (13-03 closed
// the deferred path): cherry/bending/upwards trunk + random_spread/cherry foliage decode with
// their full embedded configs, and the common roster (fancy/forking/dark_oak/giant/mega_jungle
// + their foliage) still resolves. The name is retained from 13-02; there is no remaining
// unported tree-placer arm for any generatable overworld config.
func TestParseTrunkPlacerUnported(t *testing.T) {
	// the special-biome trunk placers now DECODE with their full configs (13-03 ported them).
	for _, raw := range []string{
		`{"type":"minecraft:cherry_trunk_placer","base_height":7,"height_rand_a":1,"height_rand_b":0,"branch_count":{"type":"minecraft:weighted_list","distribution":[{"data":1,"weight":1},{"data":2,"weight":1},{"data":3,"weight":1}]},"branch_horizontal_length":{"type":"minecraft:uniform","min_inclusive":2,"max_inclusive":4},"branch_start_offset_from_top":{"min_inclusive":-4,"max_inclusive":-3},"branch_end_offset_from_top":{"type":"minecraft:uniform","min_inclusive":-1,"max_inclusive":0}}`,
		`{"type":"minecraft:bending_trunk_placer","base_height":4,"height_rand_a":2,"height_rand_b":0,"min_height_for_leaves":3,"bend_length":{"type":"minecraft:uniform","min_inclusive":1,"max_inclusive":2}}`,
		`{"type":"minecraft:upwards_branching_trunk_placer","base_height":2,"height_rand_a":1,"height_rand_b":4,"extra_branch_steps":{"type":"minecraft:uniform","min_inclusive":1,"max_inclusive":4},"extra_branch_length":{"type":"minecraft:uniform","min_inclusive":0,"max_inclusive":1},"place_branch_per_log_probability":0.5,"can_grow_through":"#minecraft:mangrove_logs_can_grow_through"}`,
	} {
		if _, err := parseTrunkPlacer(json.RawMessage(raw)); err != nil {
			t.Fatalf("parseTrunkPlacer(%s) = %v, want a PORTED special placer (13-03)", raw, err)
		}
	}

	// the special-biome foliage placers now DECODE (13-03 ported them).
	for _, raw := range []string{
		`{"type":"minecraft:random_spread_foliage_placer","radius":3,"offset":0,"foliage_height":2,"leaf_placement_attempts":70}`,
		`{"type":"minecraft:cherry_foliage_placer","radius":4,"offset":0,"height":5,"wide_bottom_layer_hole_chance":0.25,"corner_hole_chance":0.25,"hanging_leaves_chance":0.16666667,"hanging_leaves_extension_chance":0.33333334}`,
	} {
		if _, err := parseFoliagePlacer(json.RawMessage(raw)); err != nil {
			t.Fatalf("parseFoliagePlacer(%s) = %v, want a PORTED special placer (13-03)", raw, err)
		}
	}

	// the now-ported common placers resolve (no error).
	for _, raw := range []string{
		`{"type":"minecraft:fancy_trunk_placer","base_height":3,"height_rand_a":11,"height_rand_b":0}`,
		`{"type":"minecraft:forking_trunk_placer","base_height":5,"height_rand_a":2,"height_rand_b":2}`,
		`{"type":"minecraft:dark_oak_trunk_placer","base_height":6,"height_rand_a":2,"height_rand_b":1}`,
		`{"type":"minecraft:giant_trunk_placer","base_height":13,"height_rand_a":2,"height_rand_b":14}`,
		`{"type":"minecraft:mega_jungle_trunk_placer","base_height":10,"height_rand_a":2,"height_rand_b":19}`,
	} {
		if _, err := parseTrunkPlacer(json.RawMessage(raw)); err != nil {
			t.Fatalf("parseTrunkPlacer(%s) = %v, want a ported placer", raw, err)
		}
	}
	if _, err := parseFoliagePlacer(json.RawMessage(`{"type":"minecraft:spruce_foliage_placer","radius":{"type":"uniform","min_inclusive":2,"max_inclusive":3},"offset":{"type":"uniform","min_inclusive":0,"max_inclusive":2},"trunk_height":{"type":"uniform","min_inclusive":1,"max_inclusive":2}}`)); err != nil {
		t.Fatalf("parseFoliagePlacer(spruce) = %v, want a ported placer", err)
	}
}

// TestRootPlacerFieldOptional asserts a config WITHOUT root_placer decodes with rootPlacer
// nil, and the mangrove_root_placer now decodes to a real MangroveRootPlacer (13-03 ported the
// RootPlacer subsystem; the optional field + hook from 13-01 are now filled).
func TestRootPlacerFieldOptional(t *testing.T) {
	// Absent -> nil, no error.
	rp, err := parseRootPlacer(nil)
	if err != nil || rp != nil {
		t.Fatalf("parseRootPlacer(absent) = (%v, %v), want (nil, nil)", rp, err)
	}
	// mangrove_root_placer -> a real MangroveRootPlacer (from the embedded mangrove config).
	mr, err := parseRootPlacer(rawRootPlacer(t, "mangrove"))
	if err != nil {
		t.Fatalf("parseRootPlacer(mangrove) = %v, want a ported MangroveRootPlacer", err)
	}
	if _, ok := mr.(*MangroveRootPlacer); !ok {
		t.Fatalf("parseRootPlacer(mangrove) = %T, want *MangroveRootPlacer", mr)
	}
}

// PlaceTree must no-op the root placer when nil (oak/birch) and place the trunk+foliage.
// It also proves the EXACT TreeFeature.doPlace abort: a column WITHOUT full vertical room
// (freeHeight < treeHeight, oak's minimum_size has no min_clipped_height) places NOTHING;
// a clear column places. PlaceTree runs the footprint scan internally (the jar doPlace
// order: foliage draws -> trunk_offset_y -> scan -> abort -> roots -> trunk).
func TestPlaceTreeRootHookNoOp(t *testing.T) {
	origin := TreePos{X: 4, Y: 64, Z: 4}

	// Blocked above: a solid block (stone is NOT isFree) low in the trunk column makes
	// getMaxFreeTreeHeight return i-2 < treeHeight -> oak (no min_clipped_height) aborts.
	{
		cfg := oakConfig(t)
		mw := newMapWorld()
		cfg = cfg.BelowTrunkWithExisting(mw.read)
		// Place a stone ceiling 3 layers up — within the treeHeight column.
		stone := block.ToStateID[block.Stone{}]
		mw.set(origin.X, origin.Y+3, origin.Z, stone)
		if PlaceTree(mw.set, mw.read, levelgen.NewWorldgenRandom(1), cfg, 5, origin) {
			t.Fatalf("PlaceTree with a blocked column placed something (should abort)")
		}
		// Only the pre-placed stone may remain — no tree blocks written.
		if len(mw.blocks) != 1 {
			t.Fatalf("PlaceTree on a blocked column wrote %d blocks (want 0 tree blocks)", len(mw.blocks)-1)
		}
	}

	// Clear column: full vertical room -> trunk + foliage; rootPlacer nil means no root
	// blocks / no panic.
	{
		cfg := oakConfig(t)
		mw := newMapWorld()
		cfg = cfg.BelowTrunkWithExisting(mw.read)
		if !PlaceTree(mw.set, mw.read, levelgen.NewWorldgenRandom(1), cfg, 5, origin) {
			t.Fatalf("PlaceTree placed nothing for a valid (clear) tree")
		}
		oakLog := block.ToStateID[block.OakLog{Axis: block.Y}]
		if mw.read(origin.X, origin.Y, origin.Z) != oakLog {
			t.Fatalf("PlaceTree did not place the trunk base log")
		}
	}
}

// TestPlaceTreeNoStacking is the regression for the WORLDGEN free-space abort bug: two
// trees attempted at overlapping positions — the SECOND, whose trunk column is now
// obstructed by solid terrain (a stone block the first tree's footprint left no room
// around), must ABORT (place NOTHING). The old minFree=2 floor let a 6-tall tree plant
// through only 2 free layers, stacking trees through one another; the vanilla
// `freeHeight >= treeHeight` abort forbids it.
func TestPlaceTreeNoStacking(t *testing.T) {
	origin := TreePos{X: 8, Y: 70, Z: 8}

	cfg := oakConfig(t)
	mw := newMapWorld()
	cfg = cfg.BelowTrunkWithExisting(mw.read)

	// First tree: a clear column -> a full oak places.
	if !PlaceTree(mw.set, mw.read, levelgen.NewWorldgenRandom(7), cfg, 5, origin) {
		t.Fatalf("first tree failed to place in a clear column")
	}

	// Second tree, overlapping the first but shifted, into a column now capped by solid
	// stone (NOT isFree) two layers up: getMaxFreeTreeHeight returns i-2 = 0 < treeHeight,
	// and oak has no min_clipped_height -> abort, nothing written.
	cfg2 := oakConfig(t)
	cfg2 = cfg2.BelowTrunkWithExisting(mw.read)
	stackOrigin := TreePos{X: 9, Y: 70, Z: 9}
	mw.set(stackOrigin.X, stackOrigin.Y+2, stackOrigin.Z, block.ToStateID[block.Stone{}])

	blocksBefore := len(mw.blocks)
	if PlaceTree(mw.set, mw.read, levelgen.NewWorldgenRandom(7), cfg2, 5, stackOrigin) {
		t.Fatalf("second (stacking) tree placed — trees must not stack through occupied space")
	}
	if len(mw.blocks) != blocksBefore {
		t.Fatalf("aborted stacking tree still wrote %d blocks", len(mw.blocks)-blocksBefore)
	}
}

// TestMaxFreeTreeHeightIMinus2 pins the EXACT TreeFeature.getMaxFreeTreeHeight numerics:
// the FIRST blocked layer returns i-2 (never i-1, no `i>=treeHeight` special case), and a
// vine blocks the layer (when !ignoreVines) even though vine reads as isFree.
func TestMaxFreeTreeHeightIMinus2(t *testing.T) {
	cfg := oakConfig(t)
	origin := TreePos{X: 0, Y: 64, Z: 0}

	// (a) fully clear column -> returns treeHeight.
	mw := newMapWorld()
	if got := maxFreeTreeHeight(mw.read, cfg, origin, 5); got != 5 {
		t.Fatalf("clear column: maxFreeTreeHeight = %d, want treeHeight 5", got)
	}

	// (b) solid stone at layer i=4 (origin.Y+4) -> first blocked layer is 4 -> i-2 = 2.
	mw = newMapWorld()
	mw.set(origin.X, origin.Y+4, origin.Z, block.ToStateID[block.Stone{}])
	if got := maxFreeTreeHeight(mw.read, cfg, origin, 5); got != 2 {
		t.Fatalf("stone at i=4: maxFreeTreeHeight = %d, want i-2 = 2", got)
	}

	// (c) solid stone at layer i=1 -> i-2 = -1 (negative is the literal jar result; the
	// doPlace abort then fires since -1 < treeHeight). Proves NO `i>=treeHeight` clamp.
	mw = newMapWorld()
	mw.set(origin.X, origin.Y+1, origin.Z, block.ToStateID[block.Stone{}])
	if got := maxFreeTreeHeight(mw.read, cfg, origin, 5); got != -1 {
		t.Fatalf("stone at i=1: maxFreeTreeHeight = %d, want i-2 = -1", got)
	}

	// (d) vine at layer i=3 with !ignoreVines -> vine blocks -> i-2 = 1. Oak's real config
	// has ignore_vines=true, so flip a copy to exercise the guard's blocking branch.
	mw = newMapWorld()
	mw.set(origin.X, origin.Y+3, origin.Z, block.ToStateID[block.Vine{}])
	cfgVines := *cfg
	cfgVines.ignoreVines = false
	if got := maxFreeTreeHeight(mw.read, &cfgVines, origin, 5); got != 1 {
		t.Fatalf("vine at i=3 (!ignoreVines): maxFreeTreeHeight = %d, want i-2 = 1", got)
	}

	// (e) same vine but ignoreVines=true (oak's real value) -> vine is isFree
	// (REPLACEABLE_BY_TREES) and the vine guard is skipped -> not blocked -> treeHeight.
	if !cfg.ignoreVines {
		t.Fatalf("oak config unexpectedly has ignore_vines=false")
	}
	if got := maxFreeTreeHeight(mw.read, cfg, origin, 5); got != 5 {
		t.Fatalf("vine at i=3 (ignoreVines): maxFreeTreeHeight = %d, want treeHeight 5", got)
	}
}

// contains is a tiny substring check (avoids a strings import noise in this test file).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
