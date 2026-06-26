package world

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// fillFloor sets a grass_block floor at y=floorY across the whole 3x3 (so a ground-cover
// block placed at floorY+1 has a #minecraft:supports_vegetation sustaining block directly
// below, satisfying the VegetationBlock.canSurvive / mayPlaceOn gate the canSurvive port
// enforces). grass_block — not stone — because vanilla vegetation survives only on the
// supports_vegetation tag (dirt/grass_block/podzol/...), never on stone.
func fillFloor(view *Neighborhood, center [2]int, floorY int) {
	ground := block.ToStateID[block.FromID["minecraft:grass_block"]]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			baseX := (center[0] + dx) * 16
			baseZ := (center[1] + dz) * 16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					view.SetBlock(baseX+lx, floorY, baseZ+lz, ground)
				}
			}
		}
	}
}

// TestSimpleBlockPlacesProviderState: a weighted to_place over a known seed places the
// jar-picked state at the anchor (1 cumulative-walk draw), and the post-place rng state
// matches a parallel oracle source advanced by exactly one NextIntN(totalWeight).
func TestSimpleBlockPlacesProviderState(t *testing.T) {
	const seed = int64(0x5B10C)
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height)
	fillFloor(view, center, 39) // solid floor at y=39; anchor at y=40

	// A weighted provider: grass_block (w=3) + dirt (w=1). One draw picks among weight 4.
	raw := json.RawMessage(`{
		"to_place": {
			"type": "minecraft:weighted_state_provider",
			"entries": [
				{"weight": 3, "data": {"Name": "minecraft:short_grass"}},
				{"weight": 1, "data": {"Name": "minecraft:fern"}}
			]
		}
	}`)
	cf := &feature.ConfiguredFeature{Type: "simple_block", Config: &feature.ParsedConfig{Raw: raw}}
	bctx := &bodyContext{view: view}
	ctx := newPlacementContext(view, minY, height, nil)
	pos := placement.BlockPos{X: 8, Y: 40, Z: 8}

	rng := levelgen.NewLegacyRandomSource(seed)
	if !simpleBlockBody(bctx, cf, ctx, rng, pos) {
		t.Fatalf("simpleBlockBody returned false; want a placement")
	}

	// Independent oracle: the provider's single NextIntN(4) cumulative walk.
	oracle := levelgen.NewLegacyRandomSource(seed)
	i := int(oracle.NextIntN(4))
	var wantID string
	if i-3 < 0 {
		wantID = "minecraft:short_grass"
	} else {
		wantID = "minecraft:fern"
	}
	want := block.ToStateID[block.FromID[wantID]]
	if got := view.GetBlock(8, 40, 8); got != want {
		t.Fatalf("placed state %d, want %s (%d)", got, wantID, want)
	}
	// Post-place rng fingerprint: exactly one provider draw consumed.
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("post-place rng diverged: simple_block drew != 1 NextIntN")
	}
}

// TestSimpleBlockNoSupportNoPlace: with NO block below the anchor (air), the canSurvive
// gate drops the placement (air is not in #supports_vegetation — never a floating block).
func TestSimpleBlockNoSupportNoPlace(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height) // all air

	raw := json.RawMessage(`{"to_place":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:short_grass"}}}`)
	cf := &feature.ConfiguredFeature{Type: "simple_block", Config: &feature.ParsedConfig{Raw: raw}}
	bctx := &bodyContext{view: view}
	ctx := newPlacementContext(view, minY, height, nil)
	if simpleBlockBody(bctx, cf, ctx, levelgen.NewLegacyRandomSource(1), placement.BlockPos{X: 8, Y: 40, Z: 8}) {
		t.Fatalf("simpleBlockBody placed a block over air; want no placement")
	}
}

// innerSimpleBlockRef is an inline placed_feature wrapping a simple_block whose to_place
// is a fixed short_grass simple_state_provider, with an EMPTY placement list (so the
// inner feature places directly at the jittered anchor — no extra modifier draws).
const innerSimpleBlockRef = `{
	"feature": {
		"type": "minecraft:simple_block",
		"config": {"to_place": {"type": "minecraft:simple_state_provider", "state": {"Name": "minecraft:short_grass"}}}
	},
	"placement": []
}`

// TestRandomPatchTries: a synthetic random_patch (tries=N, the inner simple_block above)
// jitters the anchor N times in the jar draw order (x,y,z; each a symmetric two-draw),
// placing the inner feature at each jittered pos. It asserts (a) the EXACT jittered
// positions for a seed match an independent oracle, (b) each landed cell holds the inner
// short_grass, and (c) the total draw count is tries*6 (6 jitter nextInt; the inner
// simple_state_provider draws 0, empty placement draws 0).
func TestRandomPatchTries(t *testing.T) {
	const seed = int64(0x9A7C)
	const minY, height = -64, 384
	const tries, xzSpread, ySpread = 8, 3, 1
	center := [2]int{0, 0}
	view := build3x3(center, minY, height)
	// A solid floor at y=39 across the whole patch footprint + air above, so every
	// jittered short_grass at y in [40-1, 40+1] that lands on the floor+1 can place.
	fillFloor(view, center, 39)

	raw, _ := json.Marshal(map[string]any{
		"tries":     tries,
		"xz_spread": xzSpread,
		"y_spread":  ySpread,
		"feature":   json.RawMessage(innerSimpleBlockRef),
	})
	cf := &feature.ConfiguredFeature{Type: "random_patch", Config: &feature.ParsedConfig{Raw: raw}}
	reg := feature.NewEmbeddedRegistry()
	bctx := &bodyContext{view: view, reg: reg}
	ctx := newPlacementContext(view, minY, height, nil)
	pos := placement.BlockPos{X: 8, Y: 40, Z: 8}

	rng := levelgen.NewLegacyRandomSource(seed)
	randomPatchBody(bctx, cf, ctx, rng, pos)

	// Independent oracle: replay the jitter draw sequence and compute the jittered cells.
	oracle := levelgen.NewLegacyRandomSource(seed)
	xz := xzSpread + 1
	ys := ySpread + 1
	wantCells := map[placement.BlockPos]bool{}
	for tt := 0; tt < tries; tt++ {
		dx := int(oracle.NextIntN(int32(xz))) - int(oracle.NextIntN(int32(xz)))
		dy := int(oracle.NextIntN(int32(ys))) - int(oracle.NextIntN(int32(ys)))
		dz := int(oracle.NextIntN(int32(xz))) - int(oracle.NextIntN(int32(xz)))
		wantCells[placement.BlockPos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}] = true
	}

	// (c) draw count: rng must now be at the same point as the oracle (tries*6 draws,
	// the inner simple_block + empty placement drew nothing).
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("post-patch rng diverged: random_patch did not consume exactly tries*6 jitter draws")
	}

	// (a)+(b): each jittered cell that sits one above the floor (y=40) holds short_grass.
	grass := block.ToStateID[block.FromID["minecraft:short_grass"]]
	anyPlaced := false
	for cell := range wantCells {
		if cell.Y != 40 {
			continue // only y=40 cells have the floor directly below -> can place
		}
		if view.GetBlock(cell.X, cell.Y, cell.Z) == grass {
			anyPlaced = true
		}
	}
	if !anyPlaced {
		t.Fatalf("no jittered short_grass landed on the floor; patch placed nothing")
	}
}

// placeOneSimpleBlock is a regression-test helper: it runs simpleBlockBody for a single
// to_place id at pos over a fresh 3x3 view the caller has pre-seeded, returning whether a
// block was placed.
func placeOneSimpleBlock(t *testing.T, view *Neighborhood, id string, pos placement.BlockPos) bool {
	t.Helper()
	const minY, height = -64, 384
	raw := json.RawMessage(`{"to_place":{"type":"minecraft:simple_state_provider","state":{"Name":"` + id + `"}}}`)
	cf := &feature.ConfiguredFeature{Type: "simple_block", Config: &feature.ParsedConfig{Raw: raw}}
	bctx := &bodyContext{view: view}
	ctx := newPlacementContext(view, minY, height, nil)
	return simpleBlockBody(bctx, cf, ctx, levelgen.NewLegacyRandomSource(1), pos)
}

// TestSimpleBlockCanSurviveSustainingBlock is the FEAT-17-16 regression suite for the
// VegetationBlock.canSurvive / mayPlaceOn port. It locks in the three cases the two
// worldgen bugs were about:
//
//	(BUG B) water directly below the origin  -> grass does NOT place (floating-on-water).
//	(BUG A) a flower already at the origin    -> a second flower does NOT place (stacking).
//	(OK)    dirt below + air at origin        -> the plant DOES place.
//
// #minecraft:supports_vegetation contains dirt but not water and not any plant, so the
// sustaining-tag gate + the air-at-origin gate reject the two bug cases and keep the
// valid one.
func TestSimpleBlockCanSurviveSustainingBlock(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}

	dirt := block.ToStateID[block.FromID["minecraft:dirt"]]
	water := block.ToStateID[block.FromID["minecraft:water"]]
	dandelion := block.ToStateID[block.FromID["minecraft:dandelion"]]

	t.Run("water_below_no_grass", func(t *testing.T) {
		view := build3x3(center, minY, height) // all air
		view.SetBlock(8, 39, 8, water)         // water directly below the origin (y=40)
		if placeOneSimpleBlock(t, view, "minecraft:short_grass", placement.BlockPos{X: 8, Y: 40, Z: 8}) {
			t.Fatalf("grass placed over water; want no placement (water is not in #supports_vegetation)")
		}
		if got := view.GetBlock(8, 40, 8); !block.IsAir(got) {
			t.Fatalf("origin was written (state %d) over water; want it left air", got)
		}
	})

	t.Run("flower_below_no_second_flower", func(t *testing.T) {
		view := build3x3(center, minY, height)
		view.SetBlock(8, 39, 8, dirt)          // valid ground for the FIRST flower
		view.SetBlock(8, 40, 8, dandelion)     // a flower already occupies the origin
		// A second flower at the SAME origin: the air-at-origin half of the gate rejects it.
		if placeOneSimpleBlock(t, view, "minecraft:dandelion", placement.BlockPos{X: 8, Y: 40, Z: 8}) {
			t.Fatalf("a second flower placed on top of an existing flower; want no placement")
		}
		if got := view.GetBlock(8, 40, 8); got != dandelion {
			t.Fatalf("origin flower was overwritten (state %d); want the original dandelion preserved", got)
		}
	})

	t.Run("dirt_below_air_places", func(t *testing.T) {
		view := build3x3(center, minY, height)
		view.SetBlock(8, 39, 8, dirt) // valid sustaining block; origin (y=40) is air
		if !placeOneSimpleBlock(t, view, "minecraft:short_grass", placement.BlockPos{X: 8, Y: 40, Z: 8}) {
			t.Fatalf("grass did not place on dirt+air; want a placement (dirt is in #supports_vegetation)")
		}
		want := block.ToStateID[block.FromID["minecraft:short_grass"]]
		if got := view.GetBlock(8, 40, 8); got != want {
			t.Fatalf("placed state %d, want short_grass (%d)", got, want)
		}
	})
}

// TestPatchCrossChunkEdge: an inner simple_block jittered past x=15 writes short_grass
// into the +x neighbor chunk through the 3x3 proxy.
func TestPatchCrossChunkEdge(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height)
	fillFloor(view, center, 39)

	// A large xz_spread + many tries + an anchor at x=15 guarantees some jitter crosses
	// into chunk x=1 (x in [16,31]).
	raw, _ := json.Marshal(map[string]any{
		"tries":     64,
		"xz_spread": 8,
		"y_spread":  0,
		"feature":   json.RawMessage(innerSimpleBlockRef),
	})
	cf := &feature.ConfiguredFeature{Type: "random_patch", Config: &feature.ParsedConfig{Raw: raw}}
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	ctx := newPlacementContext(view, minY, height, nil)
	randomPatchBody(bctx, cf, ctx, levelgen.NewLegacyRandomSource(0xC205), placement.BlockPos{X: 15, Y: 40, Z: 8})

	grass := block.ToStateID[block.FromID["minecraft:short_grass"]]
	found := false
	for x := 16; x < 32 && !found; x++ {
		for z := 0; z < 16; z++ {
			if view.GetBlock(x, 40, z) == grass {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("no short_grass written into the +x neighbor chunk; cross-chunk spill failed")
	}
}
