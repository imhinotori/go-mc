package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// TestWaterChunkPersistRoundTrip locks the two reasons a reloaded water chunk "queda mal" (operator):
//   - FluidCount (the section's second short / nonEmptyFluidCount) must be recomputed on load — a
//     reloaded section left at 0 tells the client the section is fluid-free even though its palette
//     holds water, so the player cannot swim/float in reloaded water.
//   - PostProcessFluids (the aquifer/carver flow marks) must survive the save shape so reloaded
//     cave/ravine water still flows.
//
// Uses a known watery chunk (seed 777, chunk (12,-10) has thousands of water cells).
func TestWaterChunkPersistRoundTrip(t *testing.T) {
	g := NewNoiseGenerator(777, 24, -64)
	cp := level.ChunkPos{12, -10}
	ch := g.Generate(cp)

	genFluidCount := 0
	for si := range ch.Sections {
		genFluidCount += int(ch.Sections[si].FluidCount)
	}
	if genFluidCount == 0 {
		t.Fatal("test chunk has no fluid at gen time — pick a watery chunk")
	}

	data, err := SerializeChunkData(g.StructureCache(), cp, ch, -64)
	if err != nil {
		t.Fatalf("SerializeChunkData: %v", err)
	}
	ch2, _, _, err := decodeChunk(data)
	if err != nil {
		t.Fatalf("decodeChunk: %v", err)
	}

	reloadFluidCount := 0
	waterBlocks := 0
	for si := range ch2.Sections {
		reloadFluidCount += int(ch2.Sections[si].FluidCount)
		for i := 0; i < 4096; i++ {
			if _, ok := block.StateList[ch2.Sections[si].GetBlock(i)].(block.Water); ok {
				waterBlocks++
			}
		}
	}

	if reloadFluidCount != genFluidCount {
		t.Errorf("FluidCount changed on reload: gen %d -> reloaded %d", genFluidCount, reloadFluidCount)
	}
	if reloadFluidCount == 0 && waterBlocks > 0 {
		t.Errorf("FluidCount lost on reload (0) despite %d water blocks — the client will not let the player swim", waterBlocks)
	}
	if len(ch.PostProcessFluids) > 0 && len(ch2.PostProcessFluids) != len(ch.PostProcessFluids) {
		t.Errorf("PostProcessFluids lost on reload: gen %d -> reloaded %d", len(ch.PostProcessFluids), len(ch2.PostProcessFluids))
	}
}
