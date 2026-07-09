package world

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// village_pipeline_test.go drives the REAL NoiseGenerator.Decorate pipeline (router + GetBiome +
// STARTS + REFERENCES + PLACE) for a seed whose village lands in a real biome near spawn, and
// asserts the village's blocks appear in its owning chunk + the overlapping neighbors get their
// slice (mirroring worker_structure_test.go's TestDesertPyramidPlacesInPipeline). This is the
// village half of the structures-pipeline close: the data-driven jigsaw village (16-01 template
// system + 16-02 Placer + villageStartGen) generating through the full production path, NOT a
// structure-package unit slice.

// villagePipelineSeed/Chunk: world seed 25 places a SNOWY village whose start chunk is (1,6) —
// derived from the LIVE pipeline (the village placement salt 10387312 + the real GetBiome at the
// chunk-center surface landing in a snowy biome), NOT eyeballed. Its footprint spans neighbor
// chunks, so references + placeInChunk clipping must give adjacent chunks their own slices.
const (
	villagePipelineSeed   = int64(25)
	villagePipelineChunkX = 1
	villagePipelineChunkZ = 6
)

// countDirtPath counts dirt_path (village street) blocks in a chunk — a CLEAN village signature:
// dirt_path is placed ONLY by village street pieces, never by ambient snowy terrain (probed: 0
// in a far non-structure chunk, 89 in the village owner). A "did a village land here" probe over
// the real pipeline output, distinct from terrain (unlike a generic non-air count).
func countDirtPath(ch *level.Chunk) int {
	id := block.ToStateID[block.DirtPath{}]
	n := 0
	for si := range ch.Sections {
		s := &ch.Sections[si]
		for i := 0; i < 4096; i++ {
			if s.GetBlock(i) == id {
				n++
			}
		}
	}
	return n
}

// TestVillagePlacesInPipeline: the FULL production pipeline (real router/biome/STARTS/REFERENCES/
// PLACE) places the seed-25 snowy village into its owning chunk — the decorated chunk holds
// village street (dirt_path) blocks that a far non-structure chunk does not. Proves the village
// StartGenerator + the bounded-BFS Placer + the template placement are wired end-to-end through
// the production path (STRUCT-05 shaken down on the real jigsaw village).
func TestVillagePlacesInPipeline(t *testing.T) {
	g := NewNoiseGenerator(villagePipelineSeed, testSecs, testMinY)
	owner := decorateChunkVia(g, level.ChunkPos{villagePipelineChunkX, villagePipelineChunkZ})

	got := countDirtPath(owner)
	if got < 20 {
		t.Fatalf("owning chunk (%d,%d) holds only %d dirt_path blocks — the village did not place via the pipeline",
			villagePipelineChunkX, villagePipelineChunkZ, got)
	}

	// A chunk far from the village (and not a structure chunk) holds NO village streets —
	// confirming the dirt_path mass above is the village, not ambient terrain.
	far := countDirtPath(decorateChunkVia(NewNoiseGenerator(villagePipelineSeed, testSecs, testMinY), level.ChunkPos{villagePipelineChunkX + 39, villagePipelineChunkZ + 34}))
	if far != 0 {
		t.Fatalf("far chunk holds %d dirt_path blocks — the signature is not village-exclusive", far)
	}
}

// TestVillageCrossChunkIdempotent: the seed-25 village spans multiple chunks. Decorating the SAME
// owning chunk twice through the real pipeline yields byte-identical output (the re-derivable
// piece RNG + position-clipped writes make the placement order-independent + non-duplicating —
// Pitfall #2). An overlapping neighbor receives its OWN slice of the village (dirt_path present
// there too) without disturbing determinism (mirroring TestDesertPyramidCrossChunkIdempotent).
func TestVillageCrossChunkIdempotent(t *testing.T) {
	owner := level.ChunkPos{villagePipelineChunkX, villagePipelineChunkZ}

	var a, b bytes.Buffer
	if _, err := decorateChunkVia(NewNoiseGenerator(villagePipelineSeed, testSecs, testMinY), owner).WriteTo(&a); err != nil {
		t.Fatalf("decorate run A: %v", err)
	}
	if _, err := decorateChunkVia(NewNoiseGenerator(villagePipelineSeed, testSecs, testMinY), owner).WriteTo(&b); err != nil {
		t.Fatalf("decorate run B: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("re-decorating the village owner produced different bytes (%d vs %d) — placement not idempotent", a.Len(), b.Len())
	}

	// At least one adjacent chunk must receive its own village-street slice via the REFERENCES
	// + clip, not the owner's. The exact adjacent side is a property of the current deterministic
	// jigsaw graph, so this assertion follows the footprint instead of pinning the old +x edge.
	neighbors := []level.ChunkPos{
		{villagePipelineChunkX + 1, villagePipelineChunkZ},
		{villagePipelineChunkX - 1, villagePipelineChunkZ},
		{villagePipelineChunkX, villagePipelineChunkZ + 1},
		{villagePipelineChunkX, villagePipelineChunkZ - 1},
	}
	for _, npos := range neighbors {
		neighbor := decorateChunkVia(NewNoiseGenerator(villagePipelineSeed, testSecs, testMinY), npos)
		if countDirtPath(neighbor) > 0 {
			return
		}
	}
	t.Fatalf("no adjacent village chunk received a dirt_path slice — cross-chunk references+clip failed")
}
