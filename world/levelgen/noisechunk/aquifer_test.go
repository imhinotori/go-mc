package noisechunk

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// buildAquifer is a shared helper: a Router from testSeed, a NoiseChunk at pos, and the
// Aquifer over them. The aquifer reads the bound barrier/fluid_level/lava router
// functions + the NoiseChunk's preliminary surface level, exactly as vanilla wires it.
func buildAquifer(t *testing.T, cx, cz int32) (*router.Router, *NoiseChunk, *Aquifer) {
	t.Helper()
	r, err := router.NewRouter(testSeed)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}
	nc := NewNoiseChunk(r, level.ChunkPos{cx, cz})
	aq := NewAquifer(r, nc, level.ChunkPos{cx, cz})
	return r, nc, aq
}

// TestAquiferSeaLevelWater: a NON-SOLID block below sea level (y<63), where the local
// aquifer pressure resolves to the global water fluid status, becomes water — oceans/
// lakes/flooded caves below the sea fill with water rather than blanket air. We scan a
// chunk's non-solid blocks below sea level and require at least one water result (the
// global sea fluid status reaches), proving the sea-level water path is live.
func TestAquiferSeaLevelWater(t *testing.T) {
	_, nc, aq := buildAquifer(t, 0, 0)
	water := block.ToStateID[block.Water{Level: 0}]

	sawWater := false
	for lx := 0; lx < 16 && !sawWater; lx++ {
		for lz := 0; lz < 16 && !sawWater; lz++ {
			for y := nc.SeaLevel() - 1; y >= nc.MinY()+1; y-- {
				d := nc.FinalDensity(lx, y, lz)
				if d > 0 {
					continue // solid; aquifer only runs on non-solid
				}
				st, isFluid := aq.computeSubstance(nc.WorldX(lx), y, nc.WorldZ(lz), d)
				if isFluid && st == water {
					sawWater = true
					break
				}
			}
		}
	}
	if !sawWater {
		t.Errorf("no water found below sea level across chunk (expected oceans/aquifers to fill)")
	}
}

// TestAquiferPerchedAndLava: the aquifer is genuinely noise-driven below sea level — caves
// below the local water table flood with water, deep pockets get lava (the deep lava
// table), and dry pockets above the local table are air. We scan a grid of chunks (deep
// lava lakes are intentionally rare, exactly as in vanilla) and assert all three substances
// appear AND that the below-sea fill is not a flat water sheet (water, air, and lava all
// occur — the perched/dry variation the floodedness/spread noise produces).
func TestAquiferPerchedAndLava(t *testing.T) {
	water := block.ToStateID[block.Water{Level: 0}]
	lava := block.ToStateID[block.Lava{Level: 0}]

	sawLava := false
	sawWater := false
	sawAir := false
	// Scan a 9x9 chunk area: enough to reliably hit a deep lava table (vanilla lava lakes
	// are sparse, ~50 lava blocks across ~80 chunks).
scan:
	for cx := int32(-4); cx <= 4; cx++ {
		for cz := int32(-4); cz <= 4; cz++ {
			_, nc, aq := buildAquifer(t, cx, cz)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := nc.MinY() + 1; y < nc.SeaLevel(); y++ {
						d := nc.FinalDensity(lx, y, lz)
						if d > 0 {
							continue
						}
						st, isFluid := aq.computeSubstance(nc.WorldX(lx), y, nc.WorldZ(lz), d)
						if !isFluid {
							sawAir = true // air pocket above the local table
							continue
						}
						switch st {
						case lava:
							sawLava = true
						case water:
							sawWater = true
						}
					}
				}
			}
			if sawLava && sawWater && sawAir {
				break scan
			}
		}
	}
	if !sawWater {
		t.Errorf("no water found below sea level (expected flooded caves/oceans)")
	}
	if !sawAir {
		t.Errorf("no air pockets below sea level (expected dry cave pockets above the table)")
	}
	if !sawLava {
		t.Errorf("no lava found across the scanned area (expected deep lava tables)")
	}
}

// TestAquiferAirAbove: a non-solid block ABOVE the global sea level whose local aquifer
// fluid level is below it resolves to air (cave air pockets above the water table) — the
// aquifer does NOT flood everything. We require at least one air (non-fluid) result among
// non-solid blocks above sea level.
func TestAquiferAirAbove(t *testing.T) {
	sawAir := false
	for _, cc := range [][2]int32{{0, 0}, {2, 2}, {5, 1}} {
		_, nc, aq := buildAquifer(t, cc[0], cc[1])
		for lx := 0; lx < 16 && !sawAir; lx++ {
			for lz := 0; lz < 16 && !sawAir; lz++ {
				for y := nc.SeaLevel() + 1; y < nc.MinY()+nc.Height(); y++ {
					d := nc.FinalDensity(lx, y, lz)
					if d > 0 {
						continue
					}
					_, isFluid := aq.computeSubstance(nc.WorldX(lx), y, nc.WorldZ(lz), d)
					if !isFluid {
						sawAir = true
						break
					}
				}
			}
		}
		if sawAir {
			break
		}
	}
	if !sawAir {
		t.Errorf("no air pocket found above sea level (expected dry cave air above the water table)")
	}
}

// TestAquiferDeterministic: two aquifers from the same seed resolve identical substance at
// every non-solid position in a chunk (Pitfall 7 — all randomness flows from the seed).
func TestAquiferDeterministic(t *testing.T) {
	_, ncA, aqA := buildAquifer(t, 0, 0)
	_, ncB, aqB := buildAquifer(t, 0, 0)
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := ncA.MinY() + 1; y < ncA.MinY()+ncA.Height(); y++ {
				d := ncA.FinalDensity(lx, y, lz)
				if d > 0 {
					continue
				}
				sa, oka := aqA.computeSubstance(ncA.WorldX(lx), y, ncA.WorldZ(lz), d)
				sb, okb := aqB.computeSubstance(ncB.WorldX(lx), y, ncB.WorldZ(lz), d)
				if oka != okb || sa != sb {
					t.Fatalf("nondeterministic at (%d,%d,%d): a=(%d,%v) b=(%d,%v)", lx, y, lz, sa, oka, sb, okb)
				}
			}
		}
	}
}

// TestGlobalComputeFluidLavaBelowMinus54 pins the global FluidPicker lambda
// (NoiseBasedChunkGenerator.createFluidPicker): `y < min(-54, seaLevel) ? lava : water`.
// REGRESSION: the prior port returned AIR below the threshold instead of LAVA, leaving deep open
// cells (and barrier-sampled carved-underwater cells) air where vanilla has the lava aquifer.
func TestGlobalComputeFluidLavaBelowMinus54(t *testing.T) {
	_, _, aq := buildAquifer(t, 0, 0)
	// Overworld seaLevel is 63, so min(-54, 63) = -54.
	// Below -54 -> the lava fluid status (level -54, lava).
	below := aq.globalComputeFluid(-60)
	if below.fluidType != aq.lava {
		t.Fatalf("globalComputeFluid(-60).fluidType = %v, want lava (the below-threshold branch is LAVA, not air)", below.fluidType)
	}
	if below.fluidType == aq.air {
		t.Fatal("globalComputeFluid below -54 returned AIR — the underwater-air-pocket bug")
	}
	// At/above the threshold -> water.
	above := aq.globalComputeFluid(54)
	if above.fluidType != aq.water {
		t.Fatalf("globalComputeFluid(54).fluidType = %v, want water", above.fluidType)
	}
	atSea := aq.globalComputeFluid(0)
	if atSea.fluidType != aq.water {
		t.Fatalf("globalComputeFluid(0).fluidType = %v, want water (y=0 >= -54)", atSea.fluidType)
	}
}

// TestAquiferNullVsAirDistinction is the BUG-2 fill-side regression: the aquifer must distinguish a
// null substance (LastWasNull -> keep the default SOLID block) from a real AIR substance
// (LastWasNull false -> place air). A SOLID cell (density>0) is vanilla's aconst_null short-circuit,
// so computeSubstance reports LastWasNull()==true; a non-solid dry cell resolves to a real AIR block,
// so LastWasNull()==false. Collapsing the two (the earlier bug) let fill place air where vanilla
// keeps the default block, and let carvers breach barriers. CITE: NoiseBasedAquifer.computeSubstance
// (density>0 -> aconst_null) + FluidStatus.at (>= level -> Blocks.AIR, a real state, not null).
func TestAquiferNullVsAirDistinction(t *testing.T) {
	_, nc, aq := buildAquifer(t, 0, 0)

	// A SOLID cell: density>0 -> computeSubstance returns null (keep default). LastWasNull must be true.
	sawSolidNull := false
	for lx := 0; lx < 16 && !sawSolidNull; lx++ {
		for lz := 0; lz < 16 && !sawSolidNull; lz++ {
			for y := nc.MinY() + 1; y < nc.MinY()+nc.Height(); y++ {
				d := nc.FinalDensity(lx, y, lz)
				if d <= 0 {
					continue
				}
				st, isFluid := aq.computeSubstance(nc.WorldX(lx), y, nc.WorldZ(lz), d)
				if isFluid || st != 0 {
					t.Fatalf("solid cell (d=%v) returned a fluid/state (%v,%v); want (0,false)", d, st, isFluid)
				}
				if !aq.LastWasNull() {
					t.Fatalf("solid cell (d=%v) LastWasNull()=false; a density>0 short-circuit is aconst_null (keep default)", d)
				}
				sawSolidNull = true
				break
			}
		}
	}
	if !sawSolidNull {
		t.Fatal("no solid cell found to exercise the null short-circuit")
	}

	// A non-solid DRY cell above the water table: computeSubstance resolves a real AIR block, so it is
	// NOT null -- LastWasNull()==false. (A null here would be a barrier-pressure branch, which fill
	// keeps solid; a real air here is placed as air. The two must not be conflated.)
	sawAirNotNull := false
	for lx := 0; lx < 16 && !sawAirNotNull; lx++ {
		for lz := 0; lz < 16 && !sawAirNotNull; lz++ {
			for y := nc.SeaLevel() + 1; y < nc.MinY()+nc.Height(); y++ {
				d := nc.FinalDensity(lx, y, lz)
				if d > 0 {
					continue
				}
				_, isFluid := aq.computeSubstance(nc.WorldX(lx), y, nc.WorldZ(lz), d)
				if isFluid {
					continue // a fluid cell; we want a dry (air) one
				}
				if !aq.LastWasNull() {
					sawAirNotNull = true // a real AIR substance (not a null barrier) -- correct
					break
				}
				// LastWasNull()==true here would be a genuine barrier-pressure null; skip it, we
				// are looking for a real-air cell to prove air != null.
			}
		}
	}
	if !sawAirNotNull {
		t.Fatal("no dry non-solid cell resolved to a real AIR substance (LastWasNull false) above sea level")
	}
}
