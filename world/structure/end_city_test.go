package structure

// end_city_test.go -- deterministic pins for the End City structure (1:1 javap this task).
// Verifies the end_cities placement constants, that the recursive assembler produces a piece tree
// with the base tower, that a placement chunk generates a valid start, and that PostProcess emits an
// end_city_treasure chest BE + records a Shulker (Sentry) spawn.

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// endHighlandsType resolves the "minecraft:end_highlands" biome Type once (the gate allow-set value).
var endHighlandsType = func() levelbiome.Type {
	var t levelbiome.Type
	if err := t.UnmarshalText([]byte("minecraft:end_highlands")); err != nil {
		panic("test: cannot resolve minecraft:end_highlands biome type: " + err.Error())
	}
	return t
}()

// endCitySampler is a flat End surface at y 70 (>= the y>=60 gate).
type endCitySampler struct{}

func (endCitySampler) SampleSurfaceY(int, int) int { return 70 }

// endCityBiomeAt is the constant end_highlands stub (an End City is allowed everywhere here).
func endCityBiomeAt(int, int, int) levelbiome.Type { return endHighlandsType }

// TestEndCityRegistered: NewEndCityStartGen builds + exposes the end_cities placement (salt 10387313,
// spacing 20, separation 11). Cite end_cities.json.
func TestEndCityRegistered(t *testing.T) {
	g, err := NewEndCityStartGen()
	if err != nil {
		t.Fatalf("NewEndCityStartGen: %v", err)
	}
	ec := g.(*endCityStartGen)
	if ec.placement.Salt != endCitiesSalt || ec.placement.Spacing != endCitiesSpacing || ec.placement.Separation != endCitiesSeparation {
		t.Fatalf("end_cities placement = salt %d spacing %d sep %d; want %d/%d/%d",
			ec.placement.Salt, ec.placement.Spacing, ec.placement.Separation, endCitiesSalt, endCitiesSpacing, endCitiesSeparation)
	}
	if !ec.biomeAllow[endHighlandsType.String()] {
		t.Fatal("end_highlands not in the end_city has_structure biome allow-set")
	}
}

// TestEndCityAssembler: startEndCityHouseTower produces a non-empty piece tree whose FIRST piece is
// the base_floor tower. Cite EndCityPieces.startHouseTower.
func TestEndCityAssembler(t *testing.T) {
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(12345, 6, 0)
	_ = getRandomRotation(rng) // findGenerationPoint draws the rotation FIRST
	pieces, err := startEndCityHouseTower(Pos{100, 70, 0}, RotNone, rng)
	if err != nil {
		t.Fatalf("startEndCityHouseTower: %v", err)
	}
	if len(pieces) == 0 {
		t.Fatal("end city assembler produced no pieces")
	}
	first, ok := pieces[0].(*EndCityPiece)
	if !ok || first.name != "base_floor" {
		t.Fatalf("first piece = %v, want base_floor", pieces[0])
	}
}

// TestEndCityChestMarkerLoot: a chest-bearing End City template (third_floor_2, which carries a "Chest"
// data marker) placed via PostProcess emits an end_city_treasure chest BE on the block BELOW the marker.
// Cite EndCityPiece.handleDataMarker (the Chest branch -> BuiltInLootTables.END_CITY_TREASURE).
func TestEndCityChestMarkerLoot(t *testing.T) {
	p, err := newEndCityPiece("third_floor_2", Pos{0, 64, 0}, RotNone, false)
	if err != nil {
		t.Fatalf("newEndCityPiece(third_floor_2): %v", err)
	}
	view := newRecordingView()
	// A box large enough to contain the whole template + the below-marker cell.
	box := BoundingBox{MinX: -32, MinY: 0, MinZ: -32, MaxX: 64, MaxY: 128, MaxZ: 64}
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(12345, 0, 0)
	p.PostProcess(view, box, level.ChunkPos{0, 0}, rng)
	var treasure int
	for _, be := range view.bes {
		if be.lootTable == endCityLootTable {
			treasure++
		}
	}
	if treasure == 0 {
		t.Fatal("third_floor_2 placed no end_city_treasure chest (the Chest data marker did not fire)")
	}
}

// TestEndCitySentrySpawn: scan for a placed End City start + assert at least one Shulker (Sentry) spawn
// is recorded when its pieces PostProcess (every base_floor carries Sentry markers). Cite handleDataMarker.
func TestEndCitySentrySpawn(t *testing.T) {
	g, err := NewEndCityStartGen()
	if err != nil {
		t.Fatalf("NewEndCityStartGen: %v", err)
	}
	const seed = int64(12345)
	var starts []*StructureStart
	var found level.ChunkPos
	for cx := int32(0); cx < 40 && starts == nil; cx++ {
		for cz := int32(0); cz < 40; cz++ {
			s := g.GenerateStarts(seed, level.ChunkPos{cx, cz}, endCitySampler{}, endCityBiomeAt)
			if len(s) > 0 {
				starts = s
				found = level.ChunkPos{cx, cz}
				break
			}
		}
	}
	if starts == nil {
		t.Fatal("no end_city placement found in a 40x40 chunk scan")
	}
	view := newRecordingView()
	box := starts[0].BBox
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, int(found[0]), int(found[1]))
	for _, p := range starts[0].Pieces {
		p.PostProcess(view, box, found, rng)
	}
	var sentries int
	for _, sp := range view.mapView.spawns {
		if sp.EntityType == "minecraft:shulker" {
			sentries++
		}
	}
	if sentries == 0 {
		t.Fatal("end_city recorded no Shulker (Sentry) spawn")
	}
}

// TestEndCityDeterministic: the SAME (seed, pos) yields the SAME piece count (the start is PURE).
func TestEndCityDeterministic(t *testing.T) {
	g, err := NewEndCityStartGen()
	if err != nil {
		t.Fatalf("NewEndCityStartGen: %v", err)
	}
	const seed = int64(12345)
	var pos level.ChunkPos
	var a []*StructureStart
	for cx := int32(0); cx < 40 && a == nil; cx++ {
		for cz := int32(0); cz < 40; cz++ {
			s := g.GenerateStarts(seed, level.ChunkPos{cx, cz}, endCitySampler{}, endCityBiomeAt)
			if len(s) > 0 {
				a = s
				pos = level.ChunkPos{cx, cz}
				break
			}
		}
	}
	if a == nil {
		t.Skip("no placement found to test determinism")
	}
	b := g.GenerateStarts(seed, pos, endCitySampler{}, endCityBiomeAt)
	if len(b) != 1 || len(b[0].Pieces) != len(a[0].Pieces) {
		t.Fatalf("non-deterministic: first run %d pieces, second %d", len(a[0].Pieces), len(b[0].Pieces))
	}
}
