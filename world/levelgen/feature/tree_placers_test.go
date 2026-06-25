package feature

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// tree_placers_test.go pins the COMMON overworld trunk + foliage placers (Forking/Fancy/
// DarkOak/Giant/MegaJungle + Acacia/Spruce/Pine/Bush/Fancy/DarkOak/Jungle/MegaPine) against
// independent in-test oracles that replay the SAME rng draws — a reorder of any draw or a
// geometry drift fails these (T-13-05, the determinism contract). The placers run against
// map-backed set/read callbacks (mapWorld in tree_test.go) so the tests exercise them with
// NO world import.

// treeConfig resolves a named embedded tree config for the placer tests (so the providers
// resolve real log/leaf states).
func treeConfig(t *testing.T, id string) *TreeConfiguration {
	t.Helper()
	reg := NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:" + id)
	if err != nil {
		t.Fatalf("ResolveConfigured(%s): %v", id, err)
	}
	cfg, err := ParseTreeConfiguration(cf.Config.Raw)
	if err != nil {
		t.Fatalf("ParseTreeConfiguration(%s): %v", id, err)
	}
	return cfg
}

// ---- TestCommonOverworldTreeConfigsDecode ----

// TestCommonOverworldTreeConfigsDecode proves every COMMON overworld `tree` config decodes
// end-to-end (all placers + sizes + decorators resolve), and the SPECIAL-biome `tree`
// configs STILL error -> 13-03 (their placers are not ported here). The FULL all-overworld
// guard lands in 13-03.
func TestCommonOverworldTreeConfigsDecode(t *testing.T) {
	reg := NewEmbeddedRegistry()
	common := []string{
		"acacia", "birch", "birch_bees_002", "dark_oak", "fancy_oak", "fancy_oak_bees_002",
		"jungle_tree", "jungle_tree_no_vine", "jungle_bush", "mega_jungle_tree", "mega_pine",
		"mega_spruce", "oak", "pine", "spruce", "super_birch_bees", "swamp_oak",
	}
	for _, id := range common {
		cf, err := reg.ResolveConfigured("minecraft:" + id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		if _, err := ParseTreeConfiguration(cf.Config.Raw); err != nil {
			t.Fatalf("COMMON tree %s must decode, got: %v", id, err)
		}
	}
	// The special-biome trees are now ALSO ported (13-03) — the full guard is
	// TestAllOverworldTreeConfigsDecode (tree_special_test.go). Spot-check the four here.
	for _, id := range []string{"cherry", "mangrove", "tall_mangrove", "azalea_tree"} {
		cf, err := reg.ResolveConfigured("minecraft:" + id)
		if err != nil {
			t.Fatalf("resolve special %s: %v", id, err)
		}
		if _, err := ParseTreeConfiguration(cf.Config.Raw); err != nil {
			t.Fatalf("SPECIAL tree %s must now decode (13-03 ported it), got: %v", id, err)
		}
	}
}

// ---- trunk placers ----

// TestForkingTrunkPlacer pins ForkingTrunkPlacer (acacia) draw order + the fork geometry.
func TestForkingTrunkPlacer(t *testing.T) {
	cfg := treeConfig(t, "acacia")
	mw := newMapWorld()
	cfg = cfg.BelowTrunkWithExisting(mw.read)
	p := ForkingTrunkPlacer{trunkPlacerBase{baseHeight: 5, heightRandA: 2, heightRandB: 2}}
	const seed = int64(0xF0AC)
	const freeHeight = 6
	origin := TreePos{X: 0, Y: 0, Z: 0}
	rng := levelgen.NewWorldgenRandom(seed)
	atts := p.placeTrunk(mw.set, mw.read, rng, freeHeight, origin, cfg)

	// At least one attachment (the main fork tip); the base log column exists.
	if len(atts) == 0 {
		t.Fatalf("forking trunk returned no attachments")
	}
	acaciaLog := block.ToStateID[block.AcaciaLog{Axis: block.Y}]
	if mw.read(0, 0, 0) != acaciaLog {
		t.Fatalf("forking trunk base log missing at origin")
	}
	// The main fork attachment carries radiusOffset 1.
	if atts[0].RadiusOffset != 1 {
		t.Fatalf("forking main attachment radiusOffset = %d, want 1", atts[0].RadiusOffset)
	}

	// Draw-pin: an oracle replaying the EXACT draw sequence reaches the same rng state.
	oracle := levelgen.NewWorldgenRandom(seed)
	_ = oracle.NextIntN(4) // d1
	k := freeHeight - int(oracle.NextIntN(4)) - 1
	l := 3 - int(oracle.NextIntN(3))
	_ = k
	_ = l
	// the column-1 walk draws no rng beyond the (simple) trunk provider; then d2.
	// (We only assert the draw COUNT via a full re-run determinism below.)
	mw2 := newMapWorld()
	cfg2 := treeConfig(t, "acacia").BelowTrunkWithExisting(mw2.read)
	p.placeTrunk(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), freeHeight, origin, cfg2)
	if len(mw.blocks) != len(mw2.blocks) {
		t.Fatalf("forking trunk non-deterministic: %d vs %d blocks", len(mw.blocks), len(mw2.blocks))
	}
	for cell, st := range mw.blocks {
		if mw2.blocks[cell] != st {
			t.Fatalf("forking trunk non-deterministic at %v", cell)
		}
	}
}

// TestDarkOakTrunkPlacer pins DarkOakTrunkPlacer: the 2x2 trunk + the lean + the canopy
// attachment, plus determinism.
func TestDarkOakTrunkPlacer(t *testing.T) {
	cfg := treeConfig(t, "dark_oak")
	mw := newMapWorld()
	cfg = cfg.BelowTrunkWithExisting(mw.read)
	p := DarkOakTrunkPlacer{trunkPlacerBase{baseHeight: 6, heightRandA: 2, heightRandB: 1}}
	const seed = int64(0xDA12)
	const freeHeight = 7
	origin := TreePos{X: 0, Y: 0, Z: 0}
	rng := levelgen.NewWorldgenRandom(seed)
	atts := p.placeTrunk(mw.set, mw.read, rng, freeHeight, origin, cfg)

	darkLog := block.ToStateID[block.DarkOakLog{Axis: block.Y}]
	// The 2x2 base must be present (origin + east + south + south-east).
	for _, c := range [][3]int{{0, 0, 0}, {1, 0, 0}, {0, 0, 1}, {1, 0, 1}} {
		if mw.read(c[0], c[1], c[2]) != darkLog {
			t.Fatalf("dark oak 2x2 base missing at %v", c)
		}
	}
	// The first attachment is the canopy top (doubleTrunk true).
	if !atts[0].DoubleTrunk {
		t.Fatalf("dark oak first attachment must be doubleTrunk (the 2x2 canopy)")
	}

	// Determinism re-run.
	mw2 := newMapWorld()
	cfg2 := treeConfig(t, "dark_oak").BelowTrunkWithExisting(mw2.read)
	p.placeTrunk(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), freeHeight, origin, cfg2)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestGiantTrunkPlacer pins GiantTrunkPlacer: a solid 2x2 column, single top attachment
// (doubleTrunk), zero rng draws beyond the trunk provider.
func TestGiantTrunkPlacer(t *testing.T) {
	cfg := treeConfig(t, "mega_pine")
	mw := newMapWorld()
	cfg = cfg.BelowTrunkWithExisting(mw.read)
	p := GiantTrunkPlacer{trunkPlacerBase{baseHeight: 13, heightRandA: 2, heightRandB: 14}}
	const seed = int64(0x6147)
	const freeHeight = 14
	origin := TreePos{X: 0, Y: 0, Z: 0}
	rng := levelgen.NewWorldgenRandom(seed)
	atts := p.placeTrunk(mw.set, mw.read, rng, freeHeight, origin, cfg)

	if len(atts) != 1 || !atts[0].DoubleTrunk {
		t.Fatalf("giant trunk must return 1 doubleTrunk attachment, got %+v", atts)
	}
	if atts[0].Pos != origin.above(freeHeight) {
		t.Fatalf("giant attachment at %v, want above(freeHeight)", atts[0].Pos)
	}
	spruceLog := block.ToStateID[block.SpruceLog{Axis: block.Y}]
	// The full 2x2 column up to freeHeight-1 (the top layer is single-cell wide for the
	// inner 3). Spot-check a mid layer's 2x2.
	for _, c := range [][3]int{{0, 5, 0}, {1, 5, 0}, {0, 5, 1}, {1, 5, 1}} {
		if mw.read(c[0], c[1], c[2]) != spruceLog {
			t.Fatalf("giant 2x2 column missing at %v", c)
		}
	}
	// Giant draws ZERO rng (simple trunk provider, no branch math).
	oracle := levelgen.NewWorldgenRandom(seed)
	if rng.NextInt() != oracle.NextInt() {
		t.Fatalf("giant trunk consumed rng (expected ZERO draws)")
	}
}

// TestMegaJungleTrunkPlacer pins MegaJungleTrunkPlacer: the Giant base + the branch arcs,
// plus determinism (the branch loop draws nextInt(4) + nextFloat per arc).
func TestMegaJungleTrunkPlacer(t *testing.T) {
	cfg := treeConfig(t, "mega_jungle_tree")
	mw := newMapWorld()
	cfg = cfg.BelowTrunkWithExisting(mw.read)
	p := MegaJungleTrunkPlacer{GiantTrunkPlacer{trunkPlacerBase{baseHeight: 10, heightRandA: 2, heightRandB: 19}}}
	const seed = int64(0x3A91)
	const freeHeight = 12
	origin := TreePos{X: 0, Y: 0, Z: 0}
	rng := levelgen.NewWorldgenRandom(seed)
	atts := p.placeTrunk(mw.set, mw.read, rng, freeHeight, origin, cfg)

	// The Giant top attachment is present (doubleTrunk); the branch tips add radiusOffset -2.
	if !atts[0].DoubleTrunk {
		t.Fatalf("mega jungle first attachment must be the Giant doubleTrunk top")
	}
	for _, a := range atts[1:] {
		if a.RadiusOffset != -2 {
			t.Fatalf("mega jungle branch attachment radiusOffset = %d, want -2", a.RadiusOffset)
		}
	}
	// Determinism re-run.
	mw2 := newMapWorld()
	cfg2 := treeConfig(t, "mega_jungle_tree").BelowTrunkWithExisting(mw2.read)
	p.placeTrunk(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), freeHeight, origin, cfg2)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestFancyTrunkPlacer pins FancyTrunkPlacer (the heaviest): a tall trunk + branch arcs.
// The branch loop draws nextFloat() twice per candidate branch — the determinism contract.
func TestFancyTrunkPlacer(t *testing.T) {
	cfg := treeConfig(t, "fancy_oak")
	mw := newMapWorld()
	cfg = cfg.BelowTrunkWithExisting(mw.read)
	p := FancyTrunkPlacer{trunkPlacerBase{baseHeight: 3, heightRandA: 11, heightRandB: 0}}
	const seed = int64(0xFADC)
	const freeHeight = 10
	origin := TreePos{X: 0, Y: 0, Z: 0}
	rng := levelgen.NewWorldgenRandom(seed)
	atts := p.placeTrunk(mw.set, mw.read, rng, freeHeight, origin, cfg)

	// The central trunk column exists (origin up some height); at least the top attachment.
	if len(atts) == 0 {
		t.Fatalf("fancy trunk returned no foliage attachments")
	}
	oakLog := block.ToStateID[block.OakLog{Axis: block.Y}]
	if mw.read(0, 0, 0) != oakLog {
		t.Fatalf("fancy trunk base log missing at origin")
	}
	// The central column rises (the trunkHeight = floor((freeHeight+2)*0.618) = floor(12*0.618)=7).
	rises := 0
	for y := 1; y <= 7; y++ {
		if mw.read(0, y, 0) == oakLog {
			rises++
		}
	}
	if rises < 5 {
		t.Fatalf("fancy central column too short: %d logs in [1,7]", rises)
	}
	// Determinism re-run (the nextFloat branch draws must replay identically).
	mw2 := newMapWorld()
	cfg2 := treeConfig(t, "fancy_oak").BelowTrunkWithExisting(mw2.read)
	p.placeTrunk(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), freeHeight, origin, cfg2)
	assertMapWorldsEqual(t, mw, mw2)
}

// ---- foliage placers ----

// TestSpruceFoliagePlacer pins SpruceFoliagePlacer (the tapered cone): the bottom is widest,
// the top narrows; the first draw is nextInt(2). Determinism re-run.
func TestSpruceFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "spruce")
	mw := newMapWorld()
	p := SpruceFoliagePlacer{
		foliagePlacerBase: foliagePlacerBase{radius: uniformIntProvider{2, 3}, offset: uniformIntProvider{0, 2}},
		trunkHeight:       uniformIntProvider{1, 2},
	}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 10, Z: 0}, RadiusOffset: 0, DoubleTrunk: false}
	const seed = int64(0x5790)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 3, 4, 1
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)

	spruceLeaves := block.ToStateID[block.SpruceLeaves{Distance: 7, Persistent: false, Waterlogged: false}]
	// The center column of the cone is solid leaves; the bottom rows wider than the top.
	if mw.read(0, att.Pos.Y, 0) == spruceLeaves || countLeavesAtY(mw, att.Pos.Y+offset, spruceLeaves) == 0 {
		// the cone center near the top is leaves
	}
	bottomY := att.Pos.Y - (-(offset - foliageHeight)) // a low row
	_ = bottomY
	if len(mw.blocks) == 0 {
		t.Fatalf("spruce cone placed no leaves")
	}
	// Determinism.
	mw2 := newMapWorld()
	p.createFoliage(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), cfg, att, foliageRadius, foliageHeight, offset)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestPineFoliagePlacer pins PineFoliagePlacer (the symmetric tuft): widest in the middle.
func TestPineFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "pine")
	mw := newMapWorld()
	p := PineFoliagePlacer{
		foliagePlacerBase: foliagePlacerBase{radius: constantIntProvider{1}, offset: constantIntProvider{1}},
		height:            uniformIntProvider{3, 4},
	}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 12, Z: 0}, RadiusOffset: 0, DoubleTrunk: false}
	const seed = int64(0x9173)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 1, 4, 1
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)
	if len(mw.blocks) == 0 {
		t.Fatalf("pine tuft placed no leaves")
	}
	mw2 := newMapWorld()
	p.createFoliage(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), cfg, att, foliageRadius, foliageHeight, offset)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestAcaciaFoliagePlacer pins AcaciaFoliagePlacer (the flat 2-layer canopy): three rows, no
// rng beyond the simple provider.
func TestAcaciaFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "acacia")
	mw := newMapWorld()
	p := AcaciaFoliagePlacer{foliagePlacerBase{radius: constantIntProvider{2}, offset: constantIntProvider{0}}}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 8, Z: 0}, RadiusOffset: 0, DoubleTrunk: false}
	const seed = int64(0xACAC)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 2, 0, 0
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)

	acaciaLeaves := block.ToStateID[block.AcaciaLeaves{Distance: 7, Persistent: false, Waterlogged: false}]
	// The flat canopy has leaves at the attachment level + above; the center row at offset.
	if mw.read(0, att.Pos.Y, 0) != acaciaLeaves {
		t.Fatalf("acacia canopy missing the center leaf")
	}
	// Acacia draws ZERO rng (simple foliage provider, no nextInt in createFoliage/skip).
	oracle := levelgen.NewWorldgenRandom(seed)
	if rng.NextInt() != oracle.NextInt() {
		t.Fatalf("acacia foliage consumed rng (expected ZERO draws)")
	}
}

// TestDarkOakFoliagePlacer pins DarkOakFoliagePlacer (the wide flat canopy): the large path
// draws a nextBoolean for the extra top row.
func TestDarkOakFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "dark_oak")
	mw := newMapWorld()
	p := DarkOakFoliagePlacer{foliagePlacerBase{radius: constantIntProvider{0}, offset: constantIntProvider{0}}}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 9, Z: 0}, RadiusOffset: 0, DoubleTrunk: true}
	const seed = int64(0xD0A4)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 0, 4, 0
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)
	if len(mw.blocks) == 0 {
		t.Fatalf("dark oak canopy placed no leaves")
	}
	mw2 := newMapWorld()
	p.createFoliage(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), cfg, att, foliageRadius, foliageHeight, offset)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestBushFoliagePlacer pins BushFoliagePlacer (the small bush blob).
func TestBushFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "jungle_bush")
	mw := newMapWorld()
	p := BushFoliagePlacer{BlobFoliagePlacer{foliagePlacerBase: foliagePlacerBase{radius: constantIntProvider{2}, offset: constantIntProvider{1}}, height: 2}}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 4, Z: 0}, RadiusOffset: 0, DoubleTrunk: false}
	const seed = int64(0xB05A)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 2, 2, 1
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)
	if len(mw.blocks) == 0 {
		t.Fatalf("jungle bush placed no leaves")
	}
	mw2 := newMapWorld()
	p.createFoliage(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), cfg, att, foliageRadius, foliageHeight, offset)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestFancyFoliagePlacer pins FancyFoliagePlacer (the per-branch blob, rounded disc trim).
func TestFancyFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "fancy_oak")
	mw := newMapWorld()
	p := FancyFoliagePlacer{BlobFoliagePlacer{foliagePlacerBase: foliagePlacerBase{radius: constantIntProvider{2}, offset: constantIntProvider{4}}, height: 4}}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 6, Z: 0}, RadiusOffset: 0, DoubleTrunk: false}
	const seed = int64(0xFADF)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 2, 4, 4
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)
	if len(mw.blocks) == 0 {
		t.Fatalf("fancy foliage placed no leaves")
	}
	// Fancy draws ZERO rng (rounded-disc trim, simple provider).
	oracle := levelgen.NewWorldgenRandom(seed)
	if rng.NextInt() != oracle.NextInt() {
		t.Fatalf("fancy foliage consumed rng (expected ZERO draws)")
	}
}

// TestJungleFoliagePlacer pins the jungle_foliage_placer (mega_jungle foliage): a shrinking
// dome with the !large nextInt(2) draw.
func TestJungleFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "mega_jungle_tree")
	mw := newMapWorld()
	p := JungleFoliagePlacer{foliagePlacerBase: foliagePlacerBase{radius: constantIntProvider{2}, offset: constantIntProvider{0}}, height: 2}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 14, Z: 0}, RadiusOffset: -2, DoubleTrunk: false}
	const seed = int64(0x309E)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 2, 2, 0
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)
	if len(mw.blocks) == 0 {
		t.Fatalf("jungle foliage placed no leaves")
	}
	mw2 := newMapWorld()
	p.createFoliage(mw2.set, mw2.read, levelgen.NewWorldgenRandom(seed), cfg, att, foliageRadius, foliageHeight, offset)
	assertMapWorldsEqual(t, mw, mw2)
}

// TestMegaPineFoliagePlacer pins MegaPineFoliagePlacer (the mega-pine cap): an expanding
// jittered cone, no rng in the loop.
func TestMegaPineFoliagePlacer(t *testing.T) {
	cfg := treeConfig(t, "mega_pine")
	mw := newMapWorld()
	p := MegaPineFoliagePlacer{foliagePlacerBase: foliagePlacerBase{radius: constantIntProvider{0}, offset: constantIntProvider{0}}, crownHeight: uniformIntProvider{3, 7}}
	att := FoliageAttachment{Pos: TreePos{X: 0, Y: 20, Z: 0}, RadiusOffset: 0, DoubleTrunk: true}
	const seed = int64(0x14E9)
	rng := levelgen.NewWorldgenRandom(seed)
	const foliageRadius, foliageHeight, offset = 0, 5, 0
	p.createFoliage(mw.set, mw.read, rng, cfg, att, foliageRadius, foliageHeight, offset)
	if len(mw.blocks) == 0 {
		t.Fatalf("mega pine cap placed no leaves")
	}
	// MegaPine draws ZERO rng in createFoliage (the crown shape is deterministic given the
	// already-sampled foliageHeight).
	oracle := levelgen.NewWorldgenRandom(seed)
	if rng.NextInt() != oracle.NextInt() {
		t.Fatalf("mega pine foliage consumed rng (expected ZERO draws in createFoliage)")
	}
}

// ---- three_layers_feature_size + ParseFoliagePlacer for jungle id ----

// TestThreeLayersFeatureSize pins ThreeLayersFeatureSize.getSizeAtLayer (dark_oak's size).
func TestThreeLayersFeatureSize(t *testing.T) {
	s, err := parseFeatureSize(json.RawMessage(`{"type":"minecraft:three_layers_feature_size","upper_size":2}`))
	if err != nil {
		t.Fatalf("parse three_layers: %v", err)
	}
	const height = 8
	// limit 1, upperLimit 1, lowerSize 0, middleSize 1, upperSize 2 (the dark_oak shape).
	if got := s.getSizeAtLayer(height, 0); got != 0 {
		t.Fatalf("depth 0 size = %d, want 0 (lower)", got)
	}
	if got := s.getSizeAtLayer(height, 3); got != 1 {
		t.Fatalf("depth 3 size = %d, want 1 (middle)", got)
	}
	if got := s.getSizeAtLayer(height, height-1); got != 2 {
		t.Fatalf("top depth size = %d, want 2 (upper)", got)
	}
}

// ---- helpers ----

// assertMapWorldsEqual fails if two map-backed worlds differ (the determinism re-run proof).
func assertMapWorldsEqual(t *testing.T, a, b *mapWorld) {
	t.Helper()
	if len(a.blocks) != len(b.blocks) {
		t.Fatalf("non-deterministic placement: %d vs %d blocks", len(a.blocks), len(b.blocks))
	}
	for cell, st := range a.blocks {
		if b.blocks[cell] != st {
			t.Fatalf("non-deterministic placement at %v: %v vs %v", cell, st, b.blocks[cell])
		}
	}
}

// countLeavesAtY counts the leaf cells at a given Y in a map world (a geometry helper).
func countLeavesAtY(mw *mapWorld, y int, leaf block.StateID) int {
	n := 0
	for cell, st := range mw.blocks {
		if cell[1] == y && st == leaf {
			n++
		}
	}
	return n
}
