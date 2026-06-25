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
	//   for i = offset(0); i >= -foliageHeight(3); i--:
	//     j = max(radius(2) + radiusOffset(0) - 1 - i/2, 0)
	//     row center y = -i (pos.below(i)); per cell |dx|<=j, |dz|<=j:
	//       corner (|dx|==j && |dz|==j): skip = nextInt(2)!=0 || i==0
	oracle := levelgen.NewWorldgenRandom(seed)
	want := map[[3]int]bool{}
	for i := 0; i >= -foliageHeight; i-- {
		j := foliageRadius + 0 - 1 - (i / 2)
		if j < 0 {
			j = 0
		}
		y := -i // pos.below(i).Y == -i (pos.Y is 0)
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

// TestParseTrunkPlacerUnported asserts the SPECIAL-biome placers STILL error LOUDLY pointing
// at 13-03 (the common roster — fancy/forking/dark_oak/giant/mega_jungle + their foliage —
// is now ported and asserted by the per-placer tests). cherry/bending/upwards trunk +
// random_spread/cherry foliage stay routed to 13-03.
func TestParseTrunkPlacerUnported(t *testing.T) {
	cases := []struct {
		raw  string
		want string // substring the error must mention
	}{
		{`{"type":"minecraft:cherry_trunk_placer","base_height":7,"height_rand_a":1,"height_rand_b":0}`, "13-03"},
		{`{"type":"minecraft:bending_trunk_placer","base_height":7,"height_rand_a":1,"height_rand_b":0}`, "13-03"},
		{`{"type":"minecraft:upwards_branching_trunk_placer","base_height":7,"height_rand_a":1,"height_rand_b":0}`, "13-03"},
	}
	for _, c := range cases {
		_, err := parseTrunkPlacer(json.RawMessage(c.raw))
		if err == nil {
			t.Fatalf("parseTrunkPlacer(%s) = nil error, want loud unported error", c.raw)
		}
		if !contains(err.Error(), c.want) {
			t.Fatalf("parseTrunkPlacer(%s) error %q does not mention %q", c.raw, err.Error(), c.want)
		}
	}

	// the special-biome foliage placers STILL error -> 13-03.
	for _, raw := range []string{
		`{"type":"minecraft:random_spread_foliage_placer","radius":3,"offset":1,"foliage_height":3}`,
		`{"type":"minecraft:cherry_foliage_placer","radius":4,"offset":0,"height":5}`,
	} {
		if _, err := parseFoliagePlacer(json.RawMessage(raw)); err == nil || !contains(err.Error(), "13-03") {
			t.Fatalf("parseFoliagePlacer(%s) = %v, want loud unported error -> 13-03", raw, err)
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
// nil, and ParseRootPlacer on mangrove_root_placer errors loudly (the field + hook are
// wired here; 13-03 fills the body).
func TestRootPlacerFieldOptional(t *testing.T) {
	// Absent -> nil, no error.
	rp, err := parseRootPlacer(nil)
	if err != nil || rp != nil {
		t.Fatalf("parseRootPlacer(absent) = (%v, %v), want (nil, nil)", rp, err)
	}
	// mangrove_root_placer -> loud unported error pointing at 13-03.
	_, err = parseRootPlacer(json.RawMessage(`{"type":"minecraft:mangrove_root_placer"}`))
	if err == nil {
		t.Fatalf("parseRootPlacer(mangrove) = nil error, want loud unported error")
	}
	if !contains(err.Error(), "13-03") {
		t.Fatalf("parseRootPlacer(mangrove) error %q does not mention 13-03", err.Error())
	}
}

// PlaceTree must no-op the root placer when nil (oak/birch) and place the trunk+foliage.
// It also proves PlaceTree's freeHeight<=0 guard (no room -> nothing placed).
func TestPlaceTreeRootHookNoOp(t *testing.T) {
	cfg := oakConfig(t)
	mw := newMapWorld()
	cfg = cfg.BelowTrunkWithExisting(mw.read)
	origin := TreePos{X: 4, Y: 64, Z: 4}

	// freeHeight 0 -> nothing.
	if PlaceTree(mw.set, mw.read, levelgen.NewWorldgenRandom(1), cfg, 5, 0, origin) {
		t.Fatalf("PlaceTree with freeHeight 0 placed something")
	}
	if len(mw.blocks) != 0 {
		t.Fatalf("PlaceTree with freeHeight 0 wrote %d blocks", len(mw.blocks))
	}

	// freeHeight 5 -> trunk + foliage; rootPlacer nil means no root blocks / no panic.
	if !PlaceTree(mw.set, mw.read, levelgen.NewWorldgenRandom(1), cfg, 5, 5, origin) {
		t.Fatalf("PlaceTree placed nothing for a valid tree")
	}
	oakLog := block.ToStateID[block.OakLog{Axis: block.Y}]
	if mw.read(origin.X, origin.Y, origin.Z) != oakLog {
		t.Fatalf("PlaceTree did not place the trunk base log")
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
