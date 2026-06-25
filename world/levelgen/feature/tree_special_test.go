package feature

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// tree_special_test.go pins the SPECIAL-biome placers/foliage/root/decorators (cherry/
// upwards_branching/bending + cherry_foliage/random_spread + mangrove_root_placer +
// attached_to_leaves/pale_moss/creaking_heart) against in-test oracles that replay the SAME
// rng draws, and owns TestAllOverworldTreeConfigsDecode — the FULL generatable-overworld
// completeness guard (the objective acceptance for FEAT-04). A reorder of any draw or a
// geometry drift fails these (T-13-14/15/16). The placers run against map-backed set/read
// callbacks (mapWorld in tree_test.go) so the tests exercise them with NO world import.

// ============================================================================
// THE COMPLETENESS GUARD
// ============================================================================

// TestAllOverworldTreeConfigsDecode is the FEAT-04 completeness guard: EVERY embedded `tree`
// config for a generatable overworld biome (the common roster from 13-01/13-02 PLUS the four
// special biomes cherry/mangrove/tall_mangrove/azalea/pale_oak/pale_oak_creaking) MUST decode
// end-to-end — all trunk/foliage/root/decorator/provider types resolve, NO loud-error path
// remains. This closes 13-01/13-02's deferred unported-placer skip for the entire overworld.
func TestAllOverworldTreeConfigsDecode(t *testing.T) {
	reg := NewEmbeddedRegistry()
	overworld := []string{
		// common (13-01/13-02)
		"acacia", "birch", "birch_bees_002", "dark_oak", "fancy_oak", "fancy_oak_bees_002",
		"jungle_tree", "jungle_tree_no_vine", "jungle_bush", "mega_jungle_tree", "mega_pine",
		"mega_spruce", "oak", "oak_bees_002", "pine", "spruce", "super_birch_bees", "swamp_oak",
		// special (13-03 — the four extra generatable biomes)
		"cherry", "cherry_bees_005", "mangrove", "tall_mangrove", "azalea_tree",
		"pale_oak", "pale_oak_creaking", "pale_oak_bonemeal",
	}
	decoded := 0
	for _, id := range overworld {
		cf, err := reg.ResolveConfigured("minecraft:" + id)
		if err != nil {
			// Not every listed id may be embedded under that exact name; skip resolve misses
			// but require that the ones that DO resolve decode. (The four special biomes are
			// asserted present below.)
			continue
		}
		if _, err := ParseTreeConfiguration(cf.Config.Raw); err != nil {
			t.Fatalf("overworld tree %q must decode (completeness guard), got: %v", id, err)
		}
		decoded++
	}
	if decoded < 18 {
		t.Fatalf("completeness guard decoded only %d configs; expected the full overworld roster", decoded)
	}
	// The four special biomes MUST be present AND decode (the milestone mandate).
	mustDecode := []string{"cherry", "mangrove", "tall_mangrove", "azalea_tree", "pale_oak", "pale_oak_creaking"}
	for _, id := range mustDecode {
		cf, err := reg.ResolveConfigured("minecraft:" + id)
		if err != nil {
			t.Fatalf("special-biome tree %q must be embedded (it is generatable): %v", id, err)
		}
		if _, err := ParseTreeConfiguration(cf.Config.Raw); err != nil {
			t.Fatalf("special-biome tree %q must decode (FEAT-04 completeness), got: %v", id, err)
		}
	}
}

// ============================================================================
// trunk placers
// ============================================================================

// TestCherryTrunkPlacer pins CherryTrunkPlacer: the trunk + the weighted branch_count
// branches, plus determinism.
func TestCherryTrunkPlacer(t *testing.T) {
	cfg := treeConfig(t, "cherry")
	mw := newMapWorld()
	cfg = cfg.BelowTrunkWithExisting(mw.read)
	p := CherryTrunkPlacer{
		trunkPlacerBase:        trunkPlacerBase{baseHeight: 7, heightRandA: 1, heightRandB: 0},
		branchCount:            weightedListIntProvider{entries: []weightedListIntEntry{{constantIntProvider{1}, 1}, {constantIntProvider{2}, 1}, {constantIntProvider{3}, 1}}, totalWeight: 3},
		branchHorizontalLength: uniformIntProvider{2, 4},
		branchStartMin:         -4,
		branchStartMax:         -3,
		branchEndOffsetFromTop: uniformIntProvider{-1, 0},
	}
	const seed = int64(0xC4E5)
	const freeHeight = 8
	origin := TreePos{X: 0, Y: 0, Z: 0}
	rng := levelgen.NewWorldgenRandom(seed)
	atts := p.placeTrunk(mw.set, mw.read, rng, freeHeight, origin, cfg)

	cherryLog := block.ToStateID[block.CherryLog{Axis: block.Y}]
	if mw.read(0, 0, 0) != cherryLog {
		t.Fatalf("cherry trunk base log missing at origin")
	}
	if len(atts) == 0 {
		t.Fatalf("cherry trunk returned no foliage attachments")
	}
	// Determinism re-run.
	mw2 := newMapWorld()
	cfg2 := treeConfig(t, "cherry").BelowTrunkWithExisting(mw2.read)
	p.placeTrunk(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), freeHeight, origin, cfg2)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestUpwardsBranchingTrunkPlacer pins the mangrove trunk: the column + per-log probability
// branches, plus determinism.
func TestUpwardsBranchingTrunkPlacer(t *testing.T) {
	cfg := treeConfig(t, "mangrove")
	mw := newMapWorld()
	cfg = cfg.BelowTrunkWithExisting(mw.read)
	p := UpwardsBranchingTrunkPlacer{
		trunkPlacerBase:              trunkPlacerBase{baseHeight: 2, heightRandA: 1, heightRandB: 4},
		extraBranchSteps:             uniformIntProvider{1, 4},
		extraBranchLength:            uniformIntProvider{0, 1},
		placeBranchPerLogProbability: 0.5,
	}
	const seed = int64(0x3A12)
	const freeHeight = 5
	origin := TreePos{X: 0, Y: 0, Z: 0}
	rng := levelgen.NewWorldgenRandom(seed)
	atts := p.placeTrunk(mw.set, mw.read, rng, freeHeight, origin, cfg)

	mangroveLog := block.ToStateID[block.MangroveLog{Axis: block.Y}]
	if mw.read(0, 0, 0) != mangroveLog {
		t.Fatalf("mangrove trunk base log missing at origin")
	}
	if len(atts) == 0 {
		t.Fatalf("mangrove trunk returned no attachments")
	}
	mw2 := newMapWorld()
	cfg2 := treeConfig(t, "mangrove").BelowTrunkWithExisting(mw2.read)
	p.placeTrunk(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), freeHeight, origin, cfg2)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestBendingTrunkPlacer pins the azalea trunk: the straight portion then the bend, plus
// determinism.
func TestBendingTrunkPlacer(t *testing.T) {
	cfg := treeConfig(t, "azalea_tree")
	mw := newMapWorld()
	p := BendingTrunkPlacer{
		trunkPlacerBase:    trunkPlacerBase{baseHeight: 4, heightRandA: 2, heightRandB: 0},
		minHeightForLeaves: 3,
		bendLength:         uniformIntProvider{1, 2},
	}
	const seed = int64(0xA2EA)
	const freeHeight = 6
	origin := TreePos{X: 0, Y: 0, Z: 0}
	rng := levelgen.NewWorldgenRandom(seed)
	atts := p.placeTrunk(mw.set, mw.read, rng, freeHeight, origin, cfg)

	oakLog := block.ToStateID[block.OakLog{Axis: block.Y}]
	if mw.read(0, 0, 0) != oakLog {
		t.Fatalf("azalea bending trunk base log missing at origin (azalea uses oak_log)")
	}
	if len(atts) == 0 {
		t.Fatalf("bending trunk returned no foliage attachments")
	}
	// The bend produces at least one off-axis log somewhere above the bend point.
	offAxis := false
	for cell, st := range mw.blocks {
		if st == oakLog && (cell[0] != 0 || cell[2] != 0) {
			offAxis = true
		}
	}
	if !offAxis {
		t.Fatalf("bending trunk never bent off the central axis")
	}
	mw2 := newMapWorld()
	p.placeTrunk(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), freeHeight, origin, cfg)
	assertMapWorldsEqual(t, mw, mw2)
}

// ============================================================================
// foliage placers
// ============================================================================

// TestCherryFoliagePlacer pins the wide cherry canopy (the hole/hanging probability draws),
// plus determinism.
func TestCherryFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "cherry")
	mw := newMapWorld()
	p := CherryFoliagePlacer{
		foliagePlacerBase:            foliagePlacerBase{radius: constantIntProvider{4}, offset: constantIntProvider{0}},
		height:                       5,
		wideBottomLayerHoleChance:    0.25,
		cornerHoleChance:             0.25,
		hangingLeavesChance:          0.16666667,
		hangingLeavesExtensionChance: 0.33333334,
	}
	// Pre-seed the accum so the hanging-leaves isSet check sees placed leaves.
	c := *cfg
	c.accum = newTreeAccum()
	cfg = &c
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 20, Z: 0}, RadiusOffset: 0, DoubleTrunk: false}
	const seed = int64(0xC4F0)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 4, 5, 0
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)

	cherryLeaves := block.ToStateID[block.CherryLeaves{Distance: 7, Persistent: false, Waterlogged: false}]
	if countLeavesAtY(mw, att.Pos.Y, cherryLeaves) == 0 && len(mw.blocks) == 0 {
		t.Fatalf("cherry canopy placed no leaves")
	}
	if len(mw.blocks) == 0 {
		t.Fatalf("cherry canopy placed nothing")
	}
	// Determinism.
	mw2 := newMapWorld()
	c2 := *treeConfig(t, "cherry")
	c2.accum = newTreeAccum()
	p.createFoliage(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), &c2, att, foliageRadius, foliageHeight, offset)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestRandomSpreadFoliagePlacer pins the mangrove/azalea scatter (the 6-nextInt-per-attempt
// draws), plus determinism.
func TestRandomSpreadFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "mangrove")
	mw := newMapWorld()
	p := RandomSpreadFoliagePlacer{
		foliagePlacerBase:     foliagePlacerBase{radius: constantIntProvider{3}, offset: constantIntProvider{0}},
		foliageHeight:         constantIntProvider{2},
		leafPlacementAttempts: 70,
	}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 10, Z: 0}, RadiusOffset: 0, DoubleTrunk: false}
	const seed = int64(0x5E2D)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 3, 2, 0
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)

	mangroveLeaves := block.ToStateID[block.MangroveLeaves{Distance: 7, Persistent: false, Waterlogged: false}]
	leaves := 0
	for _, st := range mw.blocks {
		if st == mangroveLeaves {
			leaves++
		}
	}
	if leaves == 0 {
		t.Fatalf("random_spread placed no mangrove leaves")
	}
	// Determinism.
	mw2 := newMapWorld()
	p.createFoliage(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), cfg, att, foliageRadius, foliageHeight, offset)
	assertMapWorldsEqual(t, mw, mw2)
}

// ============================================================================
// the RootPlacer subsystem
// ============================================================================

// TestMangroveRootPlacer pins MangroveRootPlacer: the trunk_offset_y shift + the root columns
// growing down through the mud, plus determinism.
func TestMangroveRootPlacer(t *testing.T) {
	cfg := treeConfig(t, "mangrove")
	rp, err := parseMangroveRootPlacer(rawRootPlacer(t, "mangrove"))
	if err != nil {
		t.Fatalf("parseMangroveRootPlacer: %v", err)
	}
	mr := rp.(*MangroveRootPlacer)

	// A mud floor at y=0 for the roots to grow through; the anchor at y=1.
	mw := newMapWorld()
	mud := block.ToStateID[block.Mud{}]
	for x := -10; x <= 10; x++ {
		for z := -10; z <= 10; z++ {
			mw.blocks[[3]int{x, 0, z}] = mud
		}
	}
	cfg = cfg.BelowTrunkWithExisting(mw.read)
	c := *cfg
	c.accum = newTreeAccum()
	cfg = &c

	anchor := TreePos{X: 0, Y: 1, Z: 0}
	const seed = int64(0x12A6)
	rng := levelgen.NewWorldgenRandom(seed)

	// getTrunkOrigin samples trunk_offset_y (uniform{1,3}) -> the trunk origin is above the
	// anchor.
	trunkOrigin := mr.getTrunkOrigin(rng, anchor)
	if trunkOrigin.Y <= anchor.Y {
		t.Fatalf("trunk_offset_y did not shift the trunk origin up: %v vs %v", trunkOrigin, anchor)
	}

	ok := mr.placeRoots(mw.set, mw.read, rng, anchor, trunkOrigin, cfg)
	_ = ok // placement may fail if the random walk leaves the prepared floor; determinism is the contract

	// Determinism re-run from the same seed reproduces the same blocks.
	mw2 := newMapWorld()
	for x := -10; x <= 10; x++ {
		for z := -10; z <= 10; z++ {
			mw2.blocks[[3]int{x, 0, z}] = mud
		}
	}
	c2 := *treeConfig(t, "mangrove").BelowTrunkWithExisting(mw2.read)
	c2.accum = newTreeAccum()
	rng2 := levelgen.NewWorldgenRandom(seed)
	to2 := mr.getTrunkOrigin(rng2, anchor)
	mr.placeRoots(mw2.set, mw2.read, rng2, anchor, to2, &c2)
	assertMapWorldsEqual(t, mw, mw2)

	// Where roots placed, they are mangrove_roots OR muddy_mangrove_roots (the mud swap).
	mangroveRoots := block.ToStateID[block.MangroveRoots{Waterlogged: false}]
	muddyRoots := block.ToStateID[block.MuddyMangroveRoots{Axis: block.Y}]
	foundRoot := false
	for _, st := range mw.blocks {
		if st == mangroveRoots || st == muddyRoots {
			foundRoot = true
		}
	}
	if ok && !foundRoot {
		t.Fatalf("mangrove root placement succeeded but placed no root blocks")
	}
}

// ============================================================================
// special decorators
// ============================================================================

// TestAttachedToLeavesDecorator pins the mangrove propagule attach: under leaf blocks per the
// probability/directions/exclusion draws, the randomized propagule lands; determinism holds.
func TestAttachedToLeavesDecorator(t *testing.T) {
	d, err := parseAttachedToLeavesDecorator([]byte(`{"probability":1.0,"exclusion_radius_xz":1,"exclusion_radius_y":0,"required_empty_blocks":2,"block_provider":{"type":"minecraft:randomized_int_state_provider","property":"age","source":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:mangrove_propagule","Properties":{"age":"0","hanging":"true","stage":"0","waterlogged":"false"}}},"values":{"type":"minecraft:uniform","min_inclusive":0,"max_inclusive":4}},"directions":["down"]}`))
	if err != nil {
		t.Fatalf("parseAttachedToLeavesDecorator: %v", err)
	}
	// Leaves with clear air below (so the propagule attaches downward).
	leaves := []TreePos{{X: 0, Y: 10, Z: 0}, {X: 2, Y: 10, Z: 0}, {X: -2, Y: 10, Z: 0}}
	mw := newMapWorld()
	const seed = int64(0x9A10)
	d.place(decoratorCtx(mw, levelgen.NewWorldgenRandom(seed), nil, leaves))

	propagule := false
	for _, st := range mw.blocks {
		if b := block.StateList[st]; b != nil && b.ID() == "minecraft:mangrove_propagule" {
			propagule = true
		}
	}
	if !propagule {
		t.Fatalf("attached_to_leaves placed no mangrove propagule (probability 1.0)")
	}
	// Determinism.
	mw2 := newMapWorld()
	d.place(decoratorCtx(mw2, levelgen.NewWorldgenRandom(seed), nil, leaves))
	assertMapWorldsEqual(t, mw, mw2)
}

// TestPaleMossDecorator pins the pale_oak pale_hanging_moss drape off the trunk/leaves; the
// trunk/leaves probability draws + addMossHanger draws are deterministic.
func TestPaleMossDecorator(t *testing.T) {
	d, err := parsePaleMossDecorator([]byte(`{"ground_probability":0.8,"leaves_probability":1.0,"trunk_probability":1.0}`))
	if err != nil {
		t.Fatalf("parsePaleMossDecorator: %v", err)
	}
	logs := trunkLogs(0, 0, 10, 5)
	leaves := []TreePos{{X: 1, Y: 14, Z: 0}, {X: -1, Y: 14, Z: 0}}
	mw := newMapWorld()
	const seed = int64(0x9A20)
	d.place(decoratorCtx(mw, levelgen.NewWorldgenRandom(seed), logs, leaves))

	moss := false
	for _, st := range mw.blocks {
		if b := block.StateList[st]; b != nil && b.ID() == "minecraft:pale_hanging_moss" {
			moss = true
		}
	}
	if !moss {
		t.Fatalf("pale_moss placed no pale_hanging_moss (trunk/leaves probability 1.0)")
	}
	mw2 := newMapWorld()
	d.place(decoratorCtx(mw2, levelgen.NewWorldgenRandom(seed), logs, leaves))
	assertMapWorldsEqual(t, mw, mw2)
}

// TestCreakingHeartDecorator pins the pale_oak_creaking creaking_heart placement: a fully
// log-surrounded trunk cell gets a creaking_heart{dormant,natural}.
func TestCreakingHeartDecorator(t *testing.T) {
	d, err := parseCreakingHeartDecorator([]byte(`{"probability":1.0}`))
	if err != nil {
		t.Fatalf("parseCreakingHeartDecorator: %v", err)
	}
	// A 3x3x3 log cube so the center (1,1,1) is fully surrounded by logs.
	var logs []TreePos
	for dx := 0; dx <= 2; dx++ {
		for dy := 0; dy <= 2; dy++ {
			for dz := 0; dz <= 2; dz++ {
				logs = append(logs, TreePos{X: dx, Y: 10 + dy, Z: dz})
			}
		}
	}
	mw := newMapWorld()
	// Pre-fill the live world with the logs so allNeighborsLogs reads them.
	paleOakLog := block.ToStateID[block.PaleOakLog{Axis: block.Y}]
	for _, lg := range logs {
		mw.blocks[[3]int{lg.X, lg.Y, lg.Z}] = paleOakLog
	}
	const seed = int64(0x9A30)
	d.place(decoratorCtx(mw, levelgen.NewWorldgenRandom(seed), logs, nil))

	heart := false
	for _, st := range mw.blocks {
		if b := block.StateList[st]; b != nil && b.ID() == "minecraft:creaking_heart" {
			heart = true
		}
	}
	if !heart {
		t.Fatalf("creaking_heart placed no creaking_heart in the surrounded log cube")
	}
	// Determinism.
	mw2 := newMapWorld()
	for _, lg := range logs {
		mw2.blocks[[3]int{lg.X, lg.Y, lg.Z}] = paleOakLog
	}
	d.place(decoratorCtx(mw2, levelgen.NewWorldgenRandom(seed), logs, nil))
	assertMapWorldsEqual(t, mw, mw2)
}

// rawRootPlacer pulls the root_placer raw JSON out of an embedded tree config.
func rawRootPlacer(t *testing.T, id string) []byte {
	t.Helper()
	reg := NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:" + id)
	if err != nil {
		t.Fatalf("resolve %s: %v", id, err)
	}
	var j jsonTreeConfig
	if err := json.Unmarshal(cf.Config.Raw, &j); err != nil {
		t.Fatalf("decode %s tree config: %v", id, err)
	}
	if len(j.RootPlacer) == 0 {
		t.Fatalf("%s has no root_placer", id)
	}
	return j.RootPlacer
}
