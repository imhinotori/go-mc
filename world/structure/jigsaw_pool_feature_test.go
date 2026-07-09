package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

func TestFeaturePoolElementBytecodeShape(t *testing.T) {
	pool, err := LoadTemplatePool("minecraft:village/plains/trees")
	if err != nil {
		t.Fatalf("LoadTemplatePool(village/plains/trees): %v", err)
	}
	if len(pool.templates) == 0 {
		t.Fatal("village/plains/trees has no templates")
	}
	el, ok := pool.templates[0].(*featurePoolElement)
	if !ok {
		t.Fatalf("village/plains/trees first element = %T, want *featurePoolElement", pool.templates[0])
	}
	if el.feature != "minecraft:oak" {
		t.Fatalf("feature = %q, want minecraft:oak", el.feature)
	}

	origin := Pos{X: 11, Y: 64, Z: -7}
	wantBB := BoundingBox{MinX: 11, MinY: 64, MinZ: -7, MaxX: 11, MaxY: 64, MaxZ: -7}
	if got := el.BoundingBox(origin, RotClockwise90); got != wantBB {
		t.Fatalf("BoundingBox = %+v, want %+v", got, wantBB)
	}

	rng := levelgen.NewLegacyRandomSource(99)
	control := levelgen.NewLegacyRandomSource(99)
	js, err := el.Jigsaws(origin, RotClockwise90, rng)
	if err != nil {
		t.Fatalf("Jigsaws: %v", err)
	}
	if got, want := rng.NextLong(), control.NextLong(); got != want {
		t.Fatalf("Jigsaws consumed rng: nextLong=%d want %d", got, want)
	}
	if len(js) != 1 {
		t.Fatalf("jigsaw count = %d, want 1", len(js))
	}
	j := js[0]
	if j.WorldPos != origin || j.LocalPos != (Pos{}) {
		t.Fatalf("jigsaw pos = world %+v local %+v, want world %+v local zero", j.WorldPos, j.LocalPos, origin)
	}
	if j.Name != "minecraft:bottom" || j.Pool != "minecraft:empty" || j.Target != "minecraft:empty" ||
		j.FinalState != "minecraft:air" || j.Joint != "rollable" {
		t.Fatalf("jigsaw default nbt fields mismatch: %+v", j)
	}
	if j.FrontFacing != block.Down || j.TopFacing != block.South {
		t.Fatalf("jigsaw orientation = front %v top %v, want down/south", j.FrontFacing, j.TopFacing)
	}
}

func TestFeaturePoolElementPlaceAdapterAndNoAdapterRNG(t *testing.T) {
	el := &featurePoolElement{feature: "minecraft:pile_melon", projection: ProjectionRigid}
	origin := Pos{X: 2, Y: 70, Z: 3}

	noAdapterRNG := levelgen.NewLegacyRandomSource(1234)
	noAdapterControl := levelgen.NewLegacyRandomSource(1234)
	el.Place(newMapView(), origin, RotClockwise180, bigBox, noAdapterRNG)
	if got, want := noAdapterRNG.NextLong(), noAdapterControl.NextLong(); got != want {
		t.Fatalf("Place without adapter consumed rng: nextLong=%d want %d", got, want)
	}

	view := &featurePoolRecordingView{mapView: newMapView()}
	rng := levelgen.NewLegacyRandomSource(5678)
	control := levelgen.NewLegacyRandomSource(5678)
	wantAdapterDraw := control.NextLong()
	wantNext := control.NextLong()
	el.Place(view, origin, RotClockwise90, BoundingBox{MinX: 999, MinY: 999, MinZ: 999, MaxX: 999, MaxY: 999, MaxZ: 999}, rng)

	if view.calls != 1 {
		t.Fatalf("adapter calls = %d, want 1", view.calls)
	}
	if view.feature != "minecraft:pile_melon" || view.origin != origin {
		t.Fatalf("adapter saw feature=%q origin=%+v, want minecraft:pile_melon %+v", view.feature, view.origin, origin)
	}
	if view.draw != wantAdapterDraw {
		t.Fatalf("adapter rng draw = %d, want %d", view.draw, wantAdapterDraw)
	}
	if got := rng.NextLong(); got != wantNext {
		t.Fatalf("post-adapter rng nextLong=%d, want %d", got, wantNext)
	}
}

type featurePoolRecordingView struct {
	*mapView
	calls   int
	feature string
	origin  Pos
	draw    int64
}

func (v *featurePoolRecordingView) PlaceFeaturePoolElement(feature string, origin Pos, rng levelgen.RandomSource) bool {
	v.calls++
	v.feature = feature
	v.origin = origin
	v.draw = rng.NextLong()
	return true
}

var _ FeaturePoolElementPlacer = (*featurePoolRecordingView)(nil)
