package world

import (
	"bytes"
	"strings"
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
)

// netherSecs / netherMinY are the nether geometry (nether.json: min_y 0, height 128 -> 8 sections).
const (
	netherSecs = 8
	netherMinY = 0
)

// TestNetherGenImplementsGenerator: *NoiseGenerator built via NewNetherGenerator is a drop-in
// world.Generator with the nether geometry (8 sections, minY 0) and reaches StatusFull.
func TestNetherGenImplementsGenerator(t *testing.T) {
	var _ Generator = (*NoiseGenerator)(nil)

	g := NewNetherGenerator(noiseGenSeed, netherSecs, netherMinY)
	if g == nil {
		t.Fatal("NewNetherGenerator returned nil")
	}
	if minY, height := g.Dims(); minY != netherMinY || height != netherSecs*16 {
		t.Fatalf("Dims() = (%d,%d), want (%d,%d)", minY, height, netherMinY, netherSecs*16)
	}
	ch := g.Generate(level.ChunkPos{0, 0})
	if ch == nil {
		t.Fatal("Generate returned nil chunk")
	}
	if got := len(ch.Sections); got != netherSecs {
		t.Fatalf("section count = %d, want %d", got, netherSecs)
	}
	if ch.Status != level.StatusFull {
		t.Fatalf("status = %q, want %q", ch.Status, level.StatusFull)
	}
}

// TestNetherGenProducesNetherrack: the nether generator fills solid terrain with netherrack (the
// nether default_block), not overworld stone — proof the router/settings are the nether ones.
func TestNetherGenProducesNetherrack(t *testing.T) {
	g := NewNetherGenerator(noiseGenSeed, netherSecs, netherMinY)
	// Scan every column across several chunks for netherrack; the nether is dense netherrack, so it
	// must appear. Overworld stone must NOT be the fill (a wrong-settings smoke guard).
	sawNetherrack := false
	sawStone := false
	for cx := 0; cx < 3 && !sawNetherrack; cx++ {
		for cz := 0; cz < 3 && !sawNetherrack; cz++ {
			ch := g.Generate(level.ChunkPos{int32(cx), int32(cz)})
			for y := netherMinY; y < netherMinY+netherSecs*16; y++ {
				for lx := 0; lx < 16; lx++ {
					for lz := 0; lz < 16; lz++ {
						name := stateNameOf(blockAtWorld(ch, lx, y, lz, netherMinY))
						if name == "minecraft:netherrack" {
							sawNetherrack = true
						}
						if name == "minecraft:stone" {
							sawStone = true
						}
					}
				}
			}
		}
	}
	if !sawNetherrack {
		t.Fatalf("no netherrack found in 3x3 nether chunks — the nether default_block is not filling")
	}
	if sawStone {
		t.Fatalf("overworld stone found in nether terrain — the generator is bound to overworld settings")
	}
}

// TestNetherGenBiomes: the per-section biome palettes carry ONLY nether biomes (the 5-biome NETHER
// preset), never overworld plains — proof the nether MultiNoiseBiomeSource is wired.
func TestNetherGenBiomes(t *testing.T) {
	g := NewNetherGenerator(noiseGenSeed, netherSecs, netherMinY)
	netherSet := map[string]bool{
		"minecraft:nether_wastes": true, "minecraft:soul_sand_valley": true,
		"minecraft:crimson_forest": true, "minecraft:warped_forest": true,
		"minecraft:basalt_deltas": true,
	}
	// Read actual biome CELLS (0..63 per section), not Palette() — the palette container seeds a
	// default entry (biome Type 0 == badlands) that lists in Palette() even when no cell uses it, so
	// asserting over Palette would flag a false badlands. Get(i) returns the biome actually stored.
	sawNether := false
	for cx := 0; cx < 3; cx++ {
		for cz := 0; cz < 3; cz++ {
			ch := g.Generate(level.ChunkPos{int32(cx), int32(cz)})
			for si := range ch.Sections {
				for i := 0; i < 4*4*4; i++ {
					name := biomeName(ch.Sections[si].Biomes.Get(i))
					if netherSet[name] {
						sawNether = true
					} else if strings.HasPrefix(name, "minecraft:") && !netherSet[name] {
						t.Fatalf("non-nether biome %q in nether chunk cell", name)
					}
				}
			}
		}
	}
	if !sawNether {
		t.Fatalf("no nether biome found in 3x3 nether chunk cells")
	}
}

// TestNetherGenDeterministic: NewNetherGenerator is PURE over (seed, pos).
func TestNetherGenDeterministic(t *testing.T) {
	g := NewNetherGenerator(noiseGenSeed, netherSecs, netherMinY)
	pos := level.ChunkPos{1, -2}
	var a, b bytes.Buffer
	if _, err := g.Generate(pos).WriteTo(&a); err != nil {
		t.Fatalf("encode a: %v", err)
	}
	if _, err := g.Generate(pos).WriteTo(&b); err != nil {
		t.Fatalf("encode b: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("nether Generate not deterministic for the same pos")
	}
}

// biomeName resolves a biome Type back to its registry name for assertions.
func biomeName(b levelbiome.Type) string {
	txt, err := b.MarshalText()
	if err != nil {
		return "<invalid>"
	}
	return string(txt)
}
