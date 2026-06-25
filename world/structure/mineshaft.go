package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// mineshaft.go ports net.minecraft.world.level.levelgen.structure.structures.MineshaftStructure
// (javap -c against temp/cache/26.2-inner.jar) as a StartGenerator.
//
// THE MINESHAFT IS PITFALL #4: its placement is the legacy frequency-reduction path, NOT the
// spacing-grid path the temples use. mineshafts.json = spacing 1 / separation 0 / salt 0 /
// frequency 0.004 / frequency_reduction_method legacy_type_3. spacing 1 makes EVERY chunk a
// placement candidate (IsStructureChunk is ALWAYS true: floorDiv(cx,1)=cx, nextInt(1)=0 ->
// start==chunk), so the DECISIVE gate is ApplyFrequencyReducer — the (now-CORRECTED, Task 1)
// legacy_type_3 -> legacyProbabilityReducerWithDouble draw: setLargeFeatureSeed(seed,cx,cz);
// nextDouble() < 0.004.
//
// findGenerationPoint: gate on ApplyFrequencyReducer at pos; on pass, run the structure_set
// weighted pick over [mineshaft(normal), mineshaft_mesa(mesa)] (both weight 1), gate on the
// REAL biome at the origin in the chosen structure's has_structure allow-set, seed the piece
// RNG via SetLargeFeatureSeed(seed,cx,cz), seed a builder with the root MineshaftRoom, drive
// the recursive addChildren assembly (mineshaft_pieces.go), RecomputeBBox, and return the
// StructureStart. Pure over (seed,pos).

// mineshaftStructure is one structure in the mineshafts set: its id, mineshaft type, and the
// has_structure biome allow-set the REAL biome gate consults.
type mineshaftStructure struct {
	id         string
	mType      mineshaftType
	biomeAllow map[string]bool
}

// mineshaftStartGen is the mineshaft StartGenerator. It owns the mineshafts structure_set
// placement (the legacy_type_3 reducer) + the two weighted structures (normal + mesa).
type mineshaftStartGen struct {
	placement   RandomSpreadStructurePlacement
	structures  []mineshaftStructure
	totalWeight int
}

// NewMineshaftStartGen builds the generator from the embedded mineshafts structure_set + the
// mineshaft / mineshaft_mesa has_structure biome tags (ALREADY embedded — this just confirms
// the existing loader resolves them; no new extractor work). A build-data error is an asset
// bug surfaced to the caller (the NoiseGenerator panics on it like its other loads).
func NewMineshaftStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:mineshafts")
	if err != nil {
		return nil, err
	}
	normalBiomes, err := HasStructureBiomes("mineshaft")
	if err != nil {
		return nil, err
	}
	mesaBiomes, err := HasStructureBiomes("mineshaft_mesa")
	if err != nil {
		return nil, err
	}
	g := &mineshaftStartGen{
		placement: set.Placement,
		structures: []mineshaftStructure{
			{id: "minecraft:mineshaft", mType: mineshaftNormal, biomeAllow: normalBiomes},
			{id: "minecraft:mineshaft_mesa", mType: mineshaftMesa, biomeAllow: mesaBiomes},
		},
	}
	for range g.structures {
		g.totalWeight++ // both weight 1
	}
	return g, nil
}

// GenerateStarts ports MineshaftStructure's start decision for chunk pos (pure over (seed,pos)).
//
//  1. ApplyFrequencyReducer(seed, cx, cz): the CORRECTED legacy_type_3 0.4% gate (NOT
//     IsStructureChunk — spacing 1 makes that always true). Fail -> no start.
//  2. weighted pick over [normal, mesa] (both weight 1) -> the chosen structure + its type.
//  3. biome gate: the REAL biome at the chunk-center surface must be in the chosen structure's
//     has_structure allow-set (NO accept-by-default). Out-of-biome -> no start.
//  4. seed the piece RNG via SetLargeFeatureSeed(seed, cx, cz).
//  5. seed a builder with the root MineshaftRoom; drive the recursive addChildren assembly.
//  6. RecomputeBBox (Encapsulate over the whole graph so the ±8 REFERENCES finds it); return.
func (g *mineshaftStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])

	// (1) The decisive gate: the legacy_type_3 frequency reducer (Pitfall #4). spacing 1 ->
	// IsStructureChunk is always true; the 0.4% draw is what gates the mineshaft.
	if !g.placement.ApplyFrequencyReducer(seed, cx, cz) {
		return nil
	}

	// (2) The structure_set weighted pick. The jar draws the weighted entry from a fresh
	// random seeded per (seed, set-salt, chunk) in StructureSet; with both weights 1 the choice
	// is a single nextInt(totalWeight). Seed it deterministically from (seed,cx,cz).
	pickRng := levelgen.NewWorldgenRandom(0)
	pickRng.SetLargeFeatureSeed(seed, cx, cz)
	choice := g.structures[int(pickRng.NextIntN(int32(g.totalWeight)))]

	// (3) The REAL biome gate at the chunk-center surface (NO accept-by-default).
	minBlockX := cx * 16
	minBlockZ := cz * 16
	centerX := minBlockX + 8
	centerZ := minBlockZ + 8
	centerSurfaceY := sampler.SampleSurfaceY(centerX, centerZ)
	if !choice.biomeAllow[biomeAt(centerX, centerSurfaceY, centerZ).String()] {
		return nil
	}

	// (4) The piece RNG = SetLargeFeatureSeed(seed, cx, cz) — re-derivable per (seed,ownerChunk),
	// so placeInChunk from ANY overlapping chunk redraws the SAME graph + clips to that chunk
	// (the cross-chunk idempotence seam).
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	// (5) Seed the builder with the root room; drive the recursive addChildren assembly. The
	// root sits at the chunk-center column, anchored to the mineshaft Y window (the room ctor
	// fixes y=50, the corridors descend/branch from there). vanilla buries the mineshaft; the
	// Y-anchor is the jar's start Y (the terrain move is not part of the underground placement).
	builder := &mineshaftBuilder{}
	root := newMineshaftRoom(0, rng, centerX, centerZ, choice.mType)
	builder.AddPiece(root)
	ctx := &pieceChildContext{acc: builder, rng: rng}
	root.AddChildren(ctx)

	start := &StructureStart{
		Structure: choice.id,
		ChunkPos:  pos,
		Pieces:    builder.pieces,
	}
	start.RecomputeBBox()
	return []*StructureStart{start}
}
