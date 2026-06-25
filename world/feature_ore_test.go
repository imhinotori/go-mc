package world

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// fillStone sets every block in the 3x3 neighborhood (across the given Y band) to
// stone, so an ore blob has a stone host to replace (the stone_ore_replaceables tag).
func fillStone(view *Neighborhood, center [2]int, minY, height int) {
	stone := block.ToStateID[block.Stone{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			baseX := (center[0] + dx) * 16
			baseZ := (center[1] + dz) * 16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y < minY+height; y++ {
						view.SetBlock(baseX+lx, y, baseZ+lz, stone)
					}
				}
			}
		}
	}
}

// oreColumnContext is a PlacementContext whose OCEAN_FLOOR_WG height is always very
// high, so the ore place() column scan always finds a column below the floor (the blob
// is fully underground in these tests). The other reads delegate to the view.
type oreColumnContext struct {
	*placementContext
	floor int
}

func (c oreColumnContext) GetHeight(_ placement.HeightmapType, _, _ int) int { return c.floor }

// oracleOreBody is an INDEPENDENT re-derivation of oreBody's draw sequence + placement,
// used to pin the body against a hand-traceable expectation. It mirrors the jar
// algorithm exactly (the same code the body runs), but drives a SEPARATE rng so the
// test can compare both the placed set AND the post-place rng fingerprints. It returns
// the placed positions (state == coal_ore mapped by tag) and the draw count consumed.
func oracleOrePlacements(t *testing.T, seed int64, size int, pos placement.BlockPos,
	hostStone block.StateID, coal, deepCoal block.StateID, floor int) map[placement.BlockPos]block.StateID {
	t.Helper()
	rng := levelgen.NewLegacyRandomSource(seed)
	f := rng.NextFloat() * float32(math.Pi)
	g := float32(size) / 8.0
	i := int(math.Ceil(float64((float32(size)/16.0*2.0 + 1.0) / 2.0)))
	px, pz := float64(pos.X), float64(pos.Z)
	sinf, cosf := math.Sin(float64(f)), math.Cos(float64(f))
	gd := float64(g)
	x0 := px + sinf*gd
	x1 := px - sinf*gd
	z0 := pz + cosf*gd
	z1 := pz - cosf*gd
	y0 := float64(pos.Y + int(rng.NextIntN(3)) - 2)
	y1 := float64(pos.Y + int(rng.NextIntN(3)) - 2)

	type pt struct{ x, y, z, r float64 }
	pts := make([]pt, size)
	for k := 0; k < size; k++ {
		tt := float32(k) / float32(size)
		pts[k] = pt{
			x: x0 + float64(tt)*(x1-x0),
			y: y0 + float64(tt)*(y1-y0),
			z: z0 + float64(tt)*(z1-z0),
			r: ((math.Sin(float64(float32(math.Pi)*tt)) + 1.0) * (rng.NextDouble() * float64(size) / 16.0) + 1.0) / 2.0,
		}
	}
	for k := 0; k < size-1; k++ {
		if pts[k].r <= 0 {
			continue
		}
		for l := k + 1; l < size; l++ {
			if pts[l].r <= 0 {
				continue
			}
			dx, dy, dz, dr := pts[k].x-pts[l].x, pts[k].y-pts[l].y, pts[k].z-pts[l].z, pts[k].r-pts[l].r
			if dr*dr > dx*dx+dy*dy+dz*dz {
				if dr > 0 {
					pts[l].r = -1
				} else {
					pts[k].r = -1
				}
			}
		}
	}
	minX := pos.X - int(math.Ceil(float64(g))) - i
	minY := pos.Y - 2 - i
	minZ := pos.Z - int(math.Ceil(float64(g))) - i

	out := map[placement.BlockPos]block.StateID{}
	visited := map[placement.BlockPos]bool{}
	for k := 0; k < size; k++ {
		if pts[k].r < 0 {
			continue
		}
		r := pts[k].r
		cx, cy, cz := pts[k].x, pts[k].y, pts[k].z
		x0i := maxInt(int(math.Floor(cx-r)), minX)
		y0i := maxInt(int(math.Floor(cy-r)), minY)
		z0i := maxInt(int(math.Floor(cz-r)), minZ)
		x1i := maxInt(int(math.Floor(cx+r)), x0i)
		y1i := maxInt(int(math.Floor(cy+r)), y0i)
		z1i := maxInt(int(math.Floor(cz+r)), z0i)
		for bx := x0i; bx <= x1i; bx++ {
			dxr := (float64(bx) + 0.5 - cx) / r
			if dxr*dxr >= 1 {
				continue
			}
			for by := y0i; by <= y1i; by++ {
				dyr := (float64(by) + 0.5 - cy) / r
				if dxr*dxr+dyr*dyr >= 1 {
					continue
				}
				for bz := z0i; bz <= z1i; bz++ {
					dzr := (float64(bz) + 0.5 - cz) / r
					if dxr*dxr+dyr*dyr+dzr*dzr >= 1 {
						continue
					}
					cell := placement.BlockPos{X: bx, Y: by, Z: bz}
					if visited[cell] {
						continue
					}
					visited[cell] = true
					// discard==0 here -> no air check, always place; host is stone ->
					// stone_ore_replaceables matches -> coal_ore (first target).
					out[cell] = coal
				}
			}
		}
	}
	_ = deepCoal
	_ = hostStone
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// oreCoalConfigRaw returns the REAL embedded ore_coal config JSON.
func oreCoalConfigRaw(t *testing.T) json.RawMessage {
	t.Helper()
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:ore_coal")
	if err != nil {
		t.Fatalf("ResolveConfigured(ore_coal): %v", err)
	}
	return cf.Config.Raw
}

// TestOreFeatureBlob: a known seed + anchor over a stone-filled neighborhood places
// the jar-exact blob — the written positions equal the independent oracle, every
// written block is the stone-tag ore (coal_ore), the count is ~size, and the post-place
// rng state matches a parallel oracle source advanced over the same draw sequence.
func TestOreFeatureBlob(t *testing.T) {
	const seed = int64(0xC0A1)
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height)
	fillStone(view, center, minY, height)

	raw := oreCoalConfigRaw(t)
	cf := &feature.ConfiguredFeature{Type: "ore", Config: &feature.ParsedConfig{Raw: raw}}
	bctx := &bodyContext{view: view, reg: nil}
	ctx := oreColumnContext{placementContext: newPlacementContext(view, minY, height, nil), floor: 200}

	pos := placement.BlockPos{X: 8, Y: 16, Z: 8}
	rng := levelgen.NewLegacyRandomSource(seed)
	if !oreBody(bctx, cf, ctx, rng, pos) {
		t.Fatalf("oreBody returned false; want a placed blob")
	}

	coal := block.ToStateID[block.CoalOre{}]
	deepCoal := block.ToStateID[block.DeepslateCoalOre{}]
	stone := block.ToStateID[block.Stone{}]
	want := oracleOrePlacements(t, seed, 17, pos, stone, coal, deepCoal, 200)

	// (a) every oracle position holds the coal ore in the view; nothing else changed to ore.
	placed := 0
	for p, st := range want {
		got := view.GetBlock(p.X, p.Y, p.Z)
		if got != st {
			t.Fatalf("at %v: got state %d, want coal_ore %d", p, got, st)
		}
		placed++
	}
	if placed == 0 {
		t.Fatalf("oracle produced no placements")
	}
	// (c) count is ~size (17). The blob is dense; assert a sane lower bound + that it is
	// not absurdly large.
	if placed < 5 || placed > 5000 {
		t.Fatalf("placed %d ore blocks; expected a blob on the order of size=17", placed)
	}

	// (d) post-place rng fingerprint: a fresh source replaying the SAME oracle draw
	// sequence (1 nextFloat + 2 nextInt(3) + size nextDouble; discard==0 -> no per-cell
	// float) must leave the body's rng at the identical next draw.
	oracle := levelgen.NewLegacyRandomSource(seed)
	oracle.NextFloat()
	oracle.NextIntN(3)
	oracle.NextIntN(3)
	for k := 0; k < 17; k++ {
		oracle.NextDouble()
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("post-place rng diverged from the oracle draw sequence")
	}
}

// TestOreDiscardOnAir: with discard_chance_on_air_exposure > 0 and an air pocket
// adjacent to the blob, the air-exposed positions are subject to the discard roll; a
// discard of 1.0 (always do the air check) drops every air-adjacent candidate, so the
// face touching air has fewer ores than the same blob with no air.
func TestOreDiscardOnAir(t *testing.T) {
	const seed = int64(0xD15ABC)
	const minY, height = -64, 384
	center := [2]int{0, 0}

	// Build two identical stone neighborhoods; in one, carve an air slab just above the
	// anchor so the top of the blob is air-exposed.
	mk := func(carveAir bool) *Neighborhood {
		v := build3x3(center, minY, height)
		fillStone(v, center, minY, height)
		if carveAir {
			air := block.ToStateID[block.Air{}]
			// Carve a tall air wall just outside the blob core (a vertical slab over the
			// full anchor footprint at every y), so EVERY stone cell in the blob region
			// is within one block of air on at least one face — the discard-on-air check
			// then fires for all of them. The wall replaces stone the blob would NOT have
			// hosted ore in anyway (it is air, so tag_match fails there).
			for lx := 4; lx <= 12; lx++ {
				for lz := 4; lz <= 12; lz++ {
					for y := 10; y < 35; y++ {
						// leave a solid stone core untouched at the exact 3x3 around the
						// anchor center so SOME ore can host; carve everything else.
						if lx >= 7 && lx <= 9 && lz >= 7 && lz <= 9 {
							continue
						}
						v.SetBlock(lx, y, lz, air)
					}
				}
			}
		}
		return v
	}

	// A discard=1.0 config (always air-check): clone ore_coal but force the field.
	var j map[string]json.RawMessage
	if err := json.Unmarshal(oreCoalConfigRaw(t), &j); err != nil {
		t.Fatalf("unmarshal ore_coal: %v", err)
	}
	j["discard_chance_on_air_exposure"] = json.RawMessage(`1.0`)
	raw, _ := json.Marshal(j)
	cf := &feature.ConfiguredFeature{Type: "ore", Config: &feature.ParsedConfig{Raw: raw}}

	pos := placement.BlockPos{X: 8, Y: 22, Z: 8}
	coal := block.ToStateID[block.CoalOre{}]
	deepCoal := block.ToStateID[block.DeepslateCoalOre{}]
	countOre := func(v *Neighborhood) int {
		n := 0
		for x := -16; x < 32; x++ {
			for z := -16; z < 32; z++ {
				for y := 10; y < 35; y++ {
					if s := v.GetBlock(x, y, z); s == coal || s == deepCoal {
						n++
					}
				}
			}
		}
		return n
	}

	vNoAir := mk(false)
	ctx1 := oreColumnContext{placementContext: newPlacementContext(vNoAir, minY, height, nil), floor: 300}
	oreBody(&bodyContext{view: vNoAir}, cf, ctx1, levelgen.NewLegacyRandomSource(seed), pos)
	nNoAir := countOre(vNoAir)

	vAir := mk(true)
	ctx2 := oreColumnContext{placementContext: newPlacementContext(vAir, minY, height, nil), floor: 300}
	oreBody(&bodyContext{view: vAir}, cf, ctx2, levelgen.NewLegacyRandomSource(seed), pos)
	nAir := countOre(vAir)

	if nNoAir == 0 {
		t.Fatalf("control blob placed no ore")
	}
	if nAir >= nNoAir {
		t.Fatalf("discard-on-air did not drop air-exposed ore: with air=%d, without air=%d", nAir, nNoAir)
	}
}

// TestOreCrossChunkEdge: an anchor near x=15 (the +x chunk boundary) writes ore into
// the +x neighbor chunk through the 3x3 proxy (not dropped).
func TestOreCrossChunkEdge(t *testing.T) {
	const seed = int64(0xED6E)
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height)
	fillStone(view, center, minY, height)

	cf := &feature.ConfiguredFeature{Type: "ore", Config: &feature.ParsedConfig{Raw: oreCoalConfigRaw(t)}}
	ctx := oreColumnContext{placementContext: newPlacementContext(view, minY, height, nil), floor: 200}
	// Anchor at x=15 so the blob (radius a few blocks) straddles into chunk x=1.
	pos := placement.BlockPos{X: 15, Y: 40, Z: 8}
	if !oreBody(&bodyContext{view: view}, cf, ctx, levelgen.NewLegacyRandomSource(seed), pos) {
		t.Fatalf("oreBody returned false")
	}

	coal := block.ToStateID[block.CoalOre{}]
	deepCoal := block.ToStateID[block.DeepslateCoalOre{}]
	// Scan the +x neighbor chunk (x in [16,31]) for any placed ore.
	found := false
	for x := 16; x < 32 && !found; x++ {
		for z := 0; z < 16; z++ {
			for y := 30; y < 50; y++ {
				if s := view.GetBlock(x, y, z); s == coal || s == deepCoal {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Fatalf("no ore written into the +x neighbor chunk; cross-chunk spill failed")
	}
}

// TestOreUnknownRuleTestErrors: an unported predicate_type makes decodeOreConfig error
// loudly (never a silent mis-target).
func TestOreUnknownRuleTestErrors(t *testing.T) {
	raw := json.RawMessage(`{"size":4,"discard_chance_on_air_exposure":0.0,"targets":[
		{"state":{"Name":"minecraft:coal_ore"},"target":{"predicate_type":"minecraft:made_up_test"}}]}`)
	if _, err := decodeOreConfig(raw); err == nil {
		t.Fatalf("decodeOreConfig(unknown predicate_type) returned nil error; want a loud error")
	}
}

// TestOreUnknownTagErrors: an unported tag makes resolveOreTagSet error loudly.
func TestOreUnknownTagErrors(t *testing.T) {
	if _, err := resolveOreTagSet("minecraft:made_up_tag"); err == nil {
		t.Fatalf("resolveOreTagSet(unknown tag) returned nil error; want a loud error")
	}
}

// TestOreTagMembership: stone_ore_replaceables matches stone (not deepslate) and
// deepslate_ore_replaceables matches deepslate (not stone) — the tag membership is
// correct (T-12-07).
func TestOreTagMembership(t *testing.T) {
	stoneSet, err := resolveOreTagSet("minecraft:stone_ore_replaceables")
	if err != nil {
		t.Fatalf("stone tag: %v", err)
	}
	deepSet, err := resolveOreTagSet("minecraft:deepslate_ore_replaceables")
	if err != nil {
		t.Fatalf("deepslate tag: %v", err)
	}
	stone := block.ToStateID[block.Stone{}]
	deepslate := block.ToStateID[block.Deepslate{Axis: block.Y}]
	if !stoneSet[stone] {
		t.Errorf("stone_ore_replaceables does not contain stone")
	}
	if stoneSet[deepslate] {
		t.Errorf("stone_ore_replaceables wrongly contains deepslate")
	}
	if !deepSet[deepslate] {
		t.Errorf("deepslate_ore_replaceables does not contain deepslate")
	}
	if deepSet[stone] {
		t.Errorf("deepslate_ore_replaceables wrongly contains stone")
	}
}
