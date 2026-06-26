package noisechunk

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// buildFillInputs builds a Router + NoiseChunk + Aquifer + OreVeinifier at testSeed for
// the given chunk position — the four inputs Fill assembles.
func buildFillInputs(t *testing.T, cx, cz int32) (*NoiseChunk, *Aquifer, *OreVeinifier) {
	t.Helper()
	r, err := router.NewRouter(testSeed)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}
	pos := level.ChunkPos{cx, cz}
	nc := NewNoiseChunk(r, pos)
	aq := NewAquifer(r, nc, pos)
	ov := NewOreVeinifier(r.NoiseRouter.VeinToggle, r.NoiseRouter.VeinRidged, r.NoiseRouter.VeinGap, r.Random)
	return nc, aq, ov
}

// collectFill runs Fill over a chunk and returns the placed state ids indexed by
// (localX, worldY, localZ) for assertions.
func collectFill(t *testing.T, cx, cz int32) (*NoiseChunk, map[[3]int]block.StateID) {
	t.Helper()
	nc, aq, ov := buildFillInputs(t, cx, cz)
	out := make(map[[3]int]block.StateID)
	Fill(nc, aq, ov, func(lx, y, lz int, st block.StateID) {
		out[[3]int{lx, y, lz}] = st
	}, nil)
	return nc, out
}

// TestFillStoneDeepslateBedrock: solid columns place stone above the deepslate transition,
// deepslate at/below it, and a bedrock floor at minY.
func TestFillStoneDeepslateBedrock(t *testing.T) {
	nc, out := collectFill(t, 0, 0)
	stone := block.ToStateID[block.Stone{}]
	deepslate := block.ToStateID[block.Deepslate{Axis: block.Y}]
	bedrock := block.ToStateID[block.Bedrock{}]

	// Bedrock floor: every column's minY block is bedrock.
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			if got := out[[3]int{lx, nc.MinY(), lz}]; got != bedrock {
				t.Fatalf("floor (%d,%d,%d): expected bedrock %d, got %d", lx, nc.MinY(), lz, bedrock, got)
			}
		}
	}

	// Some solid block above y=0 is stone, and some solid block below y=0 is deepslate.
	sawStone, sawDeepslate := false, false
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := nc.MinY() + 1; y < nc.MinY()+nc.Height(); y++ {
				st := out[[3]int{lx, y, lz}]
				if st == stone && y > 0 {
					sawStone = true
				}
				if st == deepslate && y <= 0 {
					sawDeepslate = true
				}
			}
		}
	}
	if !sawStone {
		t.Errorf("no stone placed above y=0 (expected solid stone terrain)")
	}
	if !sawDeepslate {
		t.Errorf("no deepslate placed at/below y=0 (expected the deepslate band)")
	}
}

// TestFillSolidUsesVein: where a solid block falls in an ore-vein region, Fill places the
// ore/raw/filler block from the OreVeinifier rather than plain stone. We scan a wide area
// and assert at least one vein block (copper or iron family) appears among solid blocks.
func TestFillSolidUsesVein(t *testing.T) {
	veinFamily := map[block.StateID]bool{
		block.ToStateID[block.CopperOre{}]:        true,
		block.ToStateID[block.RawCopperBlock{}]:   true,
		block.ToStateID[block.Granite{}]:          true,
		block.ToStateID[block.DeepslateIronOre{}]: true,
		block.ToStateID[block.RawIronBlock{}]:     true,
		block.ToStateID[block.Tuff{}]:             true,
	}
	sawVein := false
	for cx := int32(-3); cx <= 3 && !sawVein; cx++ {
		for cz := int32(-3); cz <= 3 && !sawVein; cz++ {
			_, out := collectFill(t, cx, cz)
			for _, st := range out {
				if veinFamily[st] {
					sawVein = true
					break
				}
			}
		}
	}
	if !sawVein {
		t.Errorf("no ore-vein block placed across the scanned area (expected copper/iron veins)")
	}
}

// TestFillNonSolidUsesAquifer: a non-solid block (density<=0) gets the aquifer substance —
// water/lava below the local table, air above — NOT blanket air. We assert that some
// non-solid block below sea level is water (cave-complete: caves flood, not all-air).
func TestFillNonSolidUsesAquifer(t *testing.T) {
	water := block.ToStateID[block.Water{Level: 0}]
	air := block.ToStateID[block.Air{}]

	sawWater, sawAir := false, false
	for cx := int32(-2); cx <= 2; cx++ {
		for cz := int32(-2); cz <= 2; cz++ {
			nc, out := collectFill(t, cx, cz)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := nc.MinY() + 1; y < nc.SeaLevel(); y++ {
						if nc.FinalDensity(lx, y, lz) > 0 {
							continue
						}
						st := out[[3]int{lx, y, lz}]
						if st == water {
							sawWater = true
						}
						if st == air {
							sawAir = true
						}
					}
				}
			}
			if sawWater && sawAir {
				break
			}
		}
		if sawWater && sawAir {
			break
		}
	}
	if !sawWater {
		t.Errorf("no water placed below sea level by the aquifer fill (caves should flood)")
	}
	if !sawAir {
		t.Errorf("no air placed below sea level (dry cave pockets should exist above the table)")
	}
}

// TestFillDeterministic: two fills from the same seed+pos place identical state ids at
// every block (Pitfall 7 — stone/water/lava/ore all flow from the world seed).
func TestFillDeterministic(t *testing.T) {
	_, a := collectFill(t, 1, -1)
	_, b := collectFill(t, 1, -1)
	if len(a) != len(b) {
		t.Fatalf("fill block count differs: a=%d b=%d", len(a), len(b))
	}
	for k, va := range a {
		if vb := b[k]; vb != va {
			t.Fatalf("nondeterministic at %v: a=%d b=%d", k, va, vb)
		}
	}
}
