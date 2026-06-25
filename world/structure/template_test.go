package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// (mapView, newMapView and fingerprint are the shared test helpers from piece_test.go /
// desert_pyramid_test.go — reused here so the template placer is exercised through the
// SAME WorldGenView the pieces use. fingerprint returns (count, FNV-1a hash over the
// sorted (x,y,z,state) set).)

const plainsSmallHouse1 = "village/plains/houses/plains_small_house_1"

// bigBox is a large writable box that clips nothing (the round-trip fingerprint tests
// place the whole template).
var bigBox = BoundingBox{MinX: -200, MinY: -200, MinZ: -200, MaxX: 200, MaxY: 200, MaxZ: 200}

// TestTemplateParse pins the parse of a known village .nbt: size, palette length, block
// count. CFR StructureTemplate.load.
func TestTemplateParse(t *testing.T) {
	tmpl, err := LoadTemplate(plainsSmallHouse1)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	if tmpl.Size != [3]int{7, 7, 7} {
		t.Errorf("Size = %v; want [7 7 7]", tmpl.Size)
	}
	if len(tmpl.palette) != 24 {
		t.Errorf("palette len = %d; want 24", len(tmpl.palette))
	}
	if len(tmpl.blocks) != 343 {
		t.Errorf("blocks len = %d; want 343", len(tmpl.blocks))
	}
	if len(tmpl.entities) != 0 {
		t.Errorf("entities len = %d; want 0", len(tmpl.entities))
	}
}

// TestTemplatePlaceNone pins the placed-block fingerprint under (origin=0, NONE, NONE).
// A regression in the parser/placer/palette resolution changes this hash.
func TestTemplatePlaceNone(t *testing.T) {
	tmpl, err := LoadTemplate(plainsSmallHouse1)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	v := newMapView()
	rng := levelgen.NewLegacyRandomSource(1)
	tmpl.PlaceInWorld(v, Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0, nil, bigBox, rng)

	count, fp := fingerprint(v)
	if count != 343 {
		t.Errorf("placed %d blocks; want 343", count)
	}
	const wantFP = uint64(326779301212395908)
	if fp != wantFP {
		t.Errorf("NONE placement fingerprint = %d; want %d", fp, wantFP)
	}

	// The bbox under NONE is the template extent.
	bb := tmpl.BoundingBoxAt(Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0)
	if (bb != BoundingBox{MinX: 0, MinY: 0, MinZ: 0, MaxX: 6, MaxY: 6, MaxZ: 6}) {
		t.Errorf("NONE bbox = %+v; want {0,0,0..6,6,6}", bb)
	}
}

// TestTemplatePlaceCW90 proves a CW90 placement rotates BOTH the fingerprint and the
// bbox (the rotation is applied AT PLACE TIME, never pre-baked — Pitfall #7).
func TestTemplatePlaceCW90(t *testing.T) {
	tmpl, err := LoadTemplate(plainsSmallHouse1)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	none := newMapView()
	cw90 := newMapView()
	rng := levelgen.NewLegacyRandomSource(1)
	tmpl.PlaceInWorld(none, Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0, nil, bigBox, rng)
	tmpl.PlaceInWorld(cw90, Pos{0, 0, 0}, RotClockwise90, MirrorNone, 0, 0, nil, bigBox, rng)

	_, noneFP := fingerprint(none)
	_, cw90FP := fingerprint(cw90)
	if noneFP == cw90FP {
		t.Error("CW90 fingerprint equals NONE; rotation was not applied at place time")
	}
	const wantCW90FP = uint64(8660274261029316878)
	if cw90FP != wantCW90FP {
		t.Errorf("CW90 placement fingerprint = %d; want %d", cw90FP, wantCW90FP)
	}

	// The CW90 bbox is rotated about the pivot (X folds negative).
	bb := tmpl.BoundingBoxAt(Pos{0, 0, 0}, RotClockwise90, MirrorNone, 0, 0)
	if (bb != BoundingBox{MinX: -6, MinY: 0, MinZ: 0, MaxX: 0, MaxY: 6, MaxZ: 6}) {
		t.Errorf("CW90 bbox = %+v; want {-6,0,0..0,6,6}", bb)
	}
}

// TestTemplateJigsawToAir proves LegacySinglePoolElement semantics: a jigsaw block is
// placed as AIR (kept as air, not a data block). The Jigsaws extractor also surfaces the
// connector's pool/target for 16-02.
func TestTemplateJigsawToAir(t *testing.T) {
	tmpl, err := LoadTemplate(plainsSmallHouse1)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	v := newMapView()
	rng := levelgen.NewLegacyRandomSource(1)
	tmpl.PlaceInWorld(v, Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0, nil, bigBox, rng)

	js, err := tmpl.Jigsaws(Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0)
	if err != nil {
		t.Fatalf("Jigsaws: %v", err)
	}
	if len(js) != 2 {
		t.Fatalf("jigsaw count = %d; want 2", len(js))
	}
	for _, j := range js {
		got := v.blocks[[3]int{j.WorldPos.X, j.WorldPos.Y, j.WorldPos.Z}]
		if got != stateAir {
			t.Errorf("jigsaw at %v placed state %d; want air (%d)", j.WorldPos, got, stateAir)
		}
		if j.Pool == "" || j.Target == "" {
			t.Errorf("jigsaw at %v missing pool/target: %+v", j.WorldPos, j)
		}
	}
	// The plains small house jigsaws connect to the streets + villagers pools.
	pools := map[string]bool{}
	for _, j := range js {
		pools[j.Pool] = true
	}
	if !pools["minecraft:village/plains/streets"] {
		t.Errorf("expected a streets-pool jigsaw; got pools %v", pools)
	}
}

// TestTemplateUnknownBlockFailsLoud proves T-16-02: an unknown palette block name FAILS
// LOUD (returns an error), never silently placing air.
func TestTemplateUnknownBlockFailsLoud(t *testing.T) {
	ps := block.State{Name: "minecraft:not_a_real_block"}
	if _, err := resolveTemplateState(ps); err == nil {
		t.Error("resolveTemplateState(unknown) returned nil error; want a loud failure")
	}
}

// TestTemplateClipsToBox proves the place loop clips to the writable box (the cross-chunk
// mechanism): a tight box that excludes part of the template drops the outside cells.
func TestTemplateClipsToBox(t *testing.T) {
	tmpl, err := LoadTemplate(plainsSmallHouse1)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	// A box covering only the x in [0,2] slab of the 7x7x7 template.
	clip := BoundingBox{MinX: 0, MinY: -10, MinZ: -10, MaxX: 2, MaxY: 20, MaxZ: 20}
	v := newMapView()
	rng := levelgen.NewLegacyRandomSource(1)
	tmpl.PlaceInWorld(v, Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0, nil, clip, rng)

	if len(v.blocks) == 0 {
		t.Fatal("clip dropped everything; want the in-box slab")
	}
	for k := range v.blocks {
		if k[0] < 0 || k[0] > 2 {
			t.Errorf("placed block at x=%d outside the clip box [0,2]", k[0])
		}
	}
	// The full placement has 343 cells; the clipped one must be strictly fewer.
	full := newMapView()
	tmpl.PlaceInWorld(full, Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0, nil, bigBox, rng)
	if len(v.blocks) >= len(full.blocks) {
		t.Errorf("clipped placement %d >= full %d; clip did nothing", len(v.blocks), len(full.blocks))
	}
}
