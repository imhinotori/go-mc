package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// findBastionChunk locates a nether_complexes structure chunk whose set pick lands on the
// bastion (roll = NextIntN(5) >= fortress weight 2) plus a placement-failing chunk. This
// mirrors the nether_complexes weighted-without-replacement pick from the bastion side.
func findBastionChunk(t *testing.T, seed int64, g *bastionRemnantStartGen) (passCX, passCZ, failCX, failCZ int) {
	t.Helper()
	pass, fail := false, false
	for cx := 0; cx < 400 && !(pass && fail); cx++ {
		for cz := 0; cz < 400 && !(pass && fail); cz++ {
			place := g.placement.IsStructureChunk(seed, cx, cz)
			if place && !pass {
				pick := levelgen.NewWorldgenRandom(0)
				pick.SetLargeFeatureSeed(seed, cx, cz)
				if int(pick.NextIntN(5)) >= netherComplexFortressWeight {
					passCX, passCZ, pass = cx, cz, true
				}
			} else if !place && !fail {
				failCX, failCZ, fail = cx, cz, true
			}
		}
	}
	if !pass {
		t.Fatalf("no bastion-picking chunk found in 400x400 at seed %d", seed)
	}
	if !fail {
		t.Fatalf("no placement-failing chunk found")
	}
	return
}

// TestBastionRemnantRegisteredAndPlacement proves the bastion structure is registered (its
// nether_complexes placement + weights + biomes decode) and that it PLACES real jigsaw pieces
// at a known seed's bastion-picking chunk, at the fixed start Y (33).
func TestBastionRemnantRegisteredAndPlacement(t *testing.T) {
	const seed = int64(0x8A57)
	g, err := NewBastionRemnantStartGen()
	if err != nil {
		t.Fatalf("NewBastionRemnantStartGen: %v", err)
	}
	bg := g.(*bastionRemnantStartGen)

	// VERIFIED placement: nether_complexes salt 30084232 / spacing 27 / separation 4.
	if bg.placement.Salt != 30084232 || bg.placement.Spacing != 27 || bg.placement.Separation != 4 {
		t.Fatalf("placement mismatch: salt=%d spacing=%d separation=%d", bg.placement.Salt, bg.placement.Spacing, bg.placement.Separation)
	}
	// VERIFIED start config: size 6 -> maxDepth, max_distance 80, start_height absolute 33.
	if bg.maxDepth != 6 || bg.maxDistance != 80 || bg.startY != 33 {
		t.Fatalf("bastion config: maxDepth=%d maxDistance=%d startY=%d; want 6/80/33", bg.maxDepth, bg.maxDistance, bg.startY)
	}
	// The 4 bastion biomes (EXCLUDING basalt_deltas).
	for _, want := range []string{"minecraft:nether_wastes", "minecraft:crimson_forest", "minecraft:soul_sand_valley", "minecraft:warped_forest"} {
		if !bg.biomeAllow[want] {
			t.Fatalf("biome allow-set missing %q", want)
		}
	}
	if bg.biomeAllow["minecraft:basalt_deltas"] {
		t.Fatalf("bastion must NOT allow basalt_deltas")
	}
	// The start pool must be the 4-entry bastion/starts pool (units/hoglin/treasure/bridge).
	if bg.startPool == nil || bg.startPool.Size() != 4 {
		t.Fatalf("bastion start pool size = %d; want 4", bg.startPool.Size())
	}

	passCX, passCZ, failCX, failCZ := findBastionChunk(t, seed, bg)
	sampler := netherFortressSurfaceSampler{}
	biome := netherBiome(t, "minecraft:nether_wastes")

	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(starts) != 1 {
		t.Fatalf("expected 1 bastion start at (%d,%d), got %d", passCX, passCZ, len(starts))
	}
	ss := starts[0]
	if ss.Structure != "minecraft:bastion_remnant" {
		t.Fatalf("start structure = %q", ss.Structure)
	}
	if len(ss.Pieces) == 0 {
		t.Fatalf("bastion start has no pieces (jigsaw assembly produced nothing)")
	}
	// The bastion is fixed-Y (absolute 33), rigid, NO surface projection: the root sits at Y 33.
	if ss.BBox.MinY < 0 || ss.BBox.MinY > 60 {
		t.Fatalf("bastion bbox minY=%d not near start Y 33", ss.BBox.MinY)
	}

	none := g.GenerateStarts(seed, level.ChunkPos{int32(failCX), int32(failCZ)}, sampler, biome)
	if len(none) != 0 {
		t.Fatalf("placement-failing chunk produced %d bastion starts", len(none))
	}

	// Determinism: same inputs -> identical start.
	again := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(again) != 1 || len(again[0].Pieces) != len(ss.Pieces) || again[0].BBox != ss.BBox {
		t.Fatalf("non-deterministic bastion start")
	}
}

// TestBastionRemnantBasaltDeltasGate proves the bastion yields NOTHING in basalt_deltas (its
// biome gate excludes it, unlike the fortress' #is_nether), even on a bastion-picking chunk.
func TestBastionRemnantBasaltDeltasGate(t *testing.T) {
	const seed = int64(0x8A57)
	g, err := NewBastionRemnantStartGen()
	if err != nil {
		t.Fatalf("NewBastionRemnantStartGen: %v", err)
	}
	bg := g.(*bastionRemnantStartGen)
	passCX, passCZ, _, _ := findBastionChunk(t, seed, bg)
	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, netherFortressSurfaceSampler{}, netherBiome(t, "minecraft:basalt_deltas"))
	if len(starts) != 0 {
		t.Fatalf("basalt_deltas produced %d bastion starts; want 0", len(starts))
	}
}
