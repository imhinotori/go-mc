package structure

// village.go — STRUCT-05 part 2: the village StartGenerator. It hangs the bounded-BFS
// JigsawPlacement.Placer (jigsaw_placement.go) on a random_spread placement gated on the 5
// biome variants (plains/desert/savanna/snowy/taiga), each in its own has_structure biome set.
// Villages are random_spread — pure over (seed,pos) — so they fit the standard StartGenerator
// directly (UNLIKE the stronghold's per-world ring state; NO new worldgen-state plumbing).
//
// Ported (idiomatic Go, no GPL paste) from CFR (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.chunk.ChunkGenerator.createStructures (the per-set flow:
//     isStructureChunk gate; for a multi-structure set, seed a WorldgenRandom via
//     setLargeFeatureSeed(seed,cx,cz), then weighted-WITHOUT-REPLACEMENT pick a variant —
//     nextInt(totalWeight), walk the weight prefix, try it; on fail remove it + redraw)
//   - net.minecraft.world.level.levelgen.structure.Structure.findValidGenerationPoint
//     (the biome gate: getBiome at the CHUNK-CENTER surface ∈ structure.biomes(), NO
//     accept-by-default; then findGenerationPoint)
//   - net.minecraft.world.level.levelgen.structure.structures.JigsawStructure.findGenerationPoint
//     (startPos = (minBlockX, startHeight=0, minBlockZ); JigsawPlacement.addPieces with
//     projectStartToHeightmap=WORLD_SURFACE_WG, maxDepth=size=6, maxDistance=80)
//   - the village structure JSON (start_pool, size, max_distance_from_center) + villages.json
//     structure_set (salt 10387312, spacing 34, separation 8 — VERIFIED from the embedded JSON)

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// villageVariant is one of the 5 biome variants in the villages structure_set: its structure
// id, its weight (all 1), its resolved start_pool + jigsaw config, and the has_structure biome
// allow-set the REAL biome gate consults (plains -> {plains, meadow}, etc).
type villageVariant struct {
	structureID string
	weight      int
	startPool   *StructureTemplatePool
	maxDepth    int // the structure JSON `size` (6)
	maxDistance int // max_distance_from_center (80)
	biomeAllow  map[string]bool
}

// villageStructureJSON is the jigsaw village structure body (CFR JigsawStructure codec): the
// start_pool, size (-> maxDepth), and max_distance_from_center. The biomes ref + step are
// resolved separately (the has_structure tag + the surface step).
type villageStructureJSON struct {
	StartPool             string `json:"start_pool"`
	Size                  int    `json:"size"`
	MaxDistanceFromCenter int    `json:"max_distance_from_center"`
}

// villageStartGen is the village StartGenerator: the villages structure_set placement (salt
// 10387312) + the 5 weighted variants. GenerateStarts decides WHERE a village lands, weighted-
// picks a variant, gates on the REAL biome, projects to the surface, and runs the Placer.
type villageStartGen struct {
	placement   RandomSpreadStructurePlacement
	variants    []villageVariant
	totalWeight int
}

// villageVariantIDs is the ordered variant list as it appears in villages.json (the iteration
// order is load-bearing for the weighted pick — the jar walks structures() in declaration
// order). Each maps to its village_<biome> structure + has_structure/village_<biome> tag.
var villageVariantIDs = []struct{ structureID, biomeTag string }{
	{"minecraft:village_plains", "village_plains"},
	{"minecraft:village_desert", "village_desert"},
	{"minecraft:village_savanna", "village_savanna"},
	{"minecraft:village_snowy", "village_snowy"},
	{"minecraft:village_taiga", "village_taiga"},
}

// NewVillageStartGen builds the generator from the embedded villages structure_set + each
// variant's structure JSON, start_pool, and has_structure biome tag. A build-data error
// (missing/garbled embed) is an asset bug surfaced to the caller; the NoiseGenerator panics on
// it like its other build-data loads. VERIFIES the structure_set salt/spacing/separation by
// decoding villages.json (not trusting the numbers blind).
func NewVillageStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:villages")
	if err != nil {
		return nil, err
	}
	// VERIFY the placement against the documented village constants (Pitfall: don't trust the
	// number blind — decode the JSON). villages.json: salt 10387312, spacing 34, separation 8.
	if set.Placement.Salt != 10387312 || set.Placement.Spacing != 34 || set.Placement.Separation != 8 {
		return nil, fmt.Errorf("structure: villages.json placement = salt %d / spacing %d / separation %d; want 10387312 / 34 / 8",
			set.Placement.Salt, set.Placement.Spacing, set.Placement.Separation)
	}

	g := &villageStartGen{placement: set.Placement}
	for _, v := range villageVariantIDs {
		raw, err := data.StructureJSON(v.structureID)
		if err != nil {
			return nil, fmt.Errorf("structure: village structure %q: %w", v.structureID, err)
		}
		var vj villageStructureJSON
		if err := json.Unmarshal(raw, &vj); err != nil {
			return nil, fmt.Errorf("structure: village structure %q json: %w", v.structureID, err)
		}
		pool, err := LoadTemplatePool(vj.StartPool)
		if err != nil {
			return nil, fmt.Errorf("structure: village %q start_pool %q: %w", v.structureID, vj.StartPool, err)
		}
		allow, err := HasStructureBiomes(v.biomeTag)
		if err != nil {
			return nil, fmt.Errorf("structure: village %q biome tag %q: %w", v.structureID, v.biomeTag, err)
		}
		g.variants = append(g.variants, villageVariant{
			structureID: v.structureID,
			weight:      1, // every variant is weight 1 in villages.json
			startPool:   pool,
			maxDepth:    vj.Size,
			maxDistance: vj.MaxDistanceFromCenter,
			biomeAllow:  allow,
		})
		g.totalWeight += 1
	}
	return g, nil
}

// GenerateStarts ports ChunkGenerator.createStructures' per-set flow for the villages set
// (pure over (seed,pos)):
//
//  1. isStructureChunk(seed, pos): is pos the region's potential start chunk? (else no village)
//  2. seed the variant-pick RNG via setLargeFeatureSeed(seed, cx, cz) (the determinism hinge).
//  3. weighted-WITHOUT-REPLACEMENT pick: draw nextInt(totalWeight), walk the weight prefix to
//     a variant, biome-gate it at the CHUNK-CENTER surface; on a biome miss REMOVE it +
//     re-draw over the reduced total (the jar's tryGenerateStructure-fail -> remove loop).
//  4. on a biome-passing variant: project the start to the surface, run JigsawPlacement.addPieces
//     (maxDepth=size, maxDistance=80), RecomputeBBox, return the populated StructureStart.
//
// The piece RNG is a FRESH WorldgenRandom re-seeded setLargeFeatureSeed(seed,cx,cz) (the jar's
// GenerationContext.makeRandom) — re-derivable, so placeInChunk from any overlapping chunk
// redraws the SAME piece graph + clips to that chunk (the cross-chunk idempotence seam).
func (g *villageStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])

	// (1) The placement gate (salt 10387312, spacing 34, separation 8).
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	minBlockX := cx * 16
	minBlockZ := cz * 16
	centerX := minBlockX + 8
	centerZ := minBlockZ + 8
	centerSurfaceY := sampler.SampleSurfaceY(centerX, centerZ)
	biomeID := biomeAt(centerX, centerSurfaceY, centerZ).String()

	// (2) The variant-pick RNG (setLargeFeatureSeed(seed,cx,cz)).
	pickRng := levelgen.NewWorldgenRandom(0)
	pickRng.SetLargeFeatureSeed(seed, cx, cz)

	// (3) Weighted-without-replacement variant pick + biome gate (CFR createStructures: draw
	// nextInt(total), walk to a variant; on biome-fail remove + redraw). Work on a mutable copy
	// of the variant indices so a removed variant is not re-picked.
	remaining := make([]int, len(g.variants))
	for i := range remaining {
		remaining[i] = i
	}
	total := g.totalWeight

	for len(remaining) > 0 {
		// nextInt(total) -> walk the weight prefix to the chosen position.
		r := int(pickRng.NextIntN(int32(total)))
		pickIdx := 0
		for pickIdx < len(remaining) {
			r -= g.variants[remaining[pickIdx]].weight
			if r < 0 {
				break
			}
			pickIdx++
		}
		variant := g.variants[remaining[pickIdx]]

		// The REAL biome gate at the chunk-center surface (NO accept-by-default). A pass means
		// this village variant generates here.
		if variant.biomeAllow[biomeID] {
			start := g.buildStart(seed, pos, variant, minBlockX, minBlockZ, sampler)
			if start.IsValid() {
				return []*StructureStart{start}
			}
			// An empty/invalid placement (the Placer produced no pieces) -> no village here.
			return nil
		}

		// Biome miss: remove this variant + reduce the total, then redraw (the jar's loop).
		total -= variant.weight
		remaining = append(remaining[:pickIdx], remaining[pickIdx+1:]...)
	}
	return nil // no variant's biome matched -> no village
}

// buildStart ports JigsawStructure.findGenerationPoint: startPos = (minBlockX, 0, minBlockZ);
// seed the piece RNG via setLargeFeatureSeed; run the Placer with the variant's start_pool +
// maxDepth + maxDistance, projecting the start to the WORLD_SURFACE_WG heightmap. The
// projection is baked here (the addPieces height math projects the root center to the surface).
func (g *villageStartGen) buildStart(seed int64, pos level.ChunkPos, variant villageVariant, minBlockX, minBlockZ int, sampler SurfaceSampler) *StructureStart {
	cx, cz := int(pos[0]), int(pos[1])

	// The piece RNG = GenerationContext.makeRandom(seed, pos) (setLargeFeatureSeed(seed,cx,cz)).
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	// startPos: the chunk MIN corner at the projected surface Y. The jar's start_height is
	// {absolute:0}; the WORLD_SURFACE_WG projection (project_start_to_heightmap) re-anchors the
	// whole village to the surface at the root center. We project here so the rigid pieces sit
	// on terrain (the addPieces RIGID-RIGID height math pins children to the root's floor).
	startSurfaceY := sampler.SampleSurfaceY(minBlockX+8, minBlockZ+8)
	startPos := Pos{minBlockX, startSurfaceY, minBlockZ}

	pieces := addPieces(variant.startPool, startPos, variant.maxDepth, variant.maxDistance, sampler, rng)

	start := &StructureStart{
		Structure: variant.structureID,
		ChunkPos:  pos,
	}
	for _, p := range pieces {
		start.Pieces = append(start.Pieces, p)
	}
	start.RecomputeBBox()
	return start
}
