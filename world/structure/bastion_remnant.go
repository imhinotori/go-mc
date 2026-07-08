package structure

// bastion_remnant.go -- the BASTION REMNANT nether jigsaw structure, ported 1:1 from CFR
// (javap -c, temp/cache/26.2-inner.jar). The bastion shares the nether_complexes
// structure_set with the fortress (weight fortress:2 / bastion:3, salt 30084232 / spacing 27
// / separation 4) and is a JigsawStructure whose start_pool is minecraft:bastion/starts (4
// weight-1 root entries -> the four bastion TYPES: units / hoglin_stable / treasure / bridge).
// It hangs the existing bounded-BFS JigsawPlacement.Placer + the template_pool / .nbt
// machinery -- the SAME engine villages use. See git history for the full CFR citation block.
//
// Ported (idiomatic Go, no GPL paste) from CFR:
//   - net.minecraft.world.level.chunk.ChunkGenerator.createStructures / tryGenerateStructure
//     (the per-structure_set weighted-WITHOUT-replacement pick: setLargeFeatureSeed(seed,cx,cz),
//      build the list of set entries whose isStructureChunk passes, sum weights, then loop
//      nextInt(totalWeight) -> walk the weight prefix -> tryGenerateStructure; on fail remove
//      the entry + subtract its weight + redraw. tryGenerateStructure re-seeds a SEPARATE
//      WorldgenRandom setLargeFeatureSeed(seed,cx,cz) for the piece assembly)
//   - net.minecraft.world.level.levelgen.structure.Structure.findValidGenerationPoint (the
//      biome gate: getBiome at the CHUNK-CENTER at the start Y must be in structure.biomes())
//   - JigsawStructure.findGenerationPoint (startY = startHeight.sample(rng,ctx) -- a
//      ConstantHeight{absolute:33} that draws NO rng; startPos = (minBlockX, 33, minBlockZ);
//      JigsawPlacement.addPieces maxDepth=size=6, maxDistance=80, projectStartToHeightmap=
//      Optional.empty -> NO surface projection, rigid)
//   - the bastion_remnant structure JSON + nether_complexes.json + has_structure/bastion_remnant
//      (the 4 nether biomes EXCLUDING basalt_deltas -- unlike the fortress that uses #is_nether).
//
// SHARED-SET FAITHFULNESS: this is the BASTION half of nether_complexes; the fortress half lives
// in nether_fortress.go. Both seed the pick RNG identically and draw the SAME stream, so
// producing a bastion exactly when the vanilla loop winning entry is the bastion (and nil when
// it is the fortress) yields the jar-correct one-of-set mutual exclusion. In basalt_deltas the
// bastion biome gate fails, so the bastion yields nothing there (the loop re-rolls onto the
// fortress -- handled by the fortress generator).

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// bastionStructureJSON is the JigsawStructure body for bastion_remnant (CFR JigsawStructure
// codec): start_pool + size (-> maxDepth) + max_distance_from_center + start_height.absolute.
type bastionStructureJSON struct {
	StartPool             string `json:"start_pool"`
	Size                  int    `json:"size"`
	MaxDistanceFromCenter int    `json:"max_distance_from_center"`
	StartHeight           struct {
		Absolute int `json:"absolute"`
	} `json:"start_height"`
}

// The nether_complexes weighted entries, in JSON declaration order (load-bearing for the
// weighted pick: the jar walks StructureSet.structures() in declaration order). fortress is
// first (weight 2), bastion second (weight 3); total 5.
const (
	netherComplexFortressWeight = 2
	netherComplexBastionWeight  = 3
	netherComplexFortressID     = "minecraft:fortress"
	netherComplexBastionID      = "minecraft:bastion_remnant"
)

// bastionRemnantStartGen is the bastion half of the nether_complexes StartGenerator.
type bastionRemnantStartGen struct {
	placement   RandomSpreadStructurePlacement
	startPool   *StructureTemplatePool
	maxDepth    int // the structure JSON size (6)
	maxDistance int // max_distance_from_center (80)
	startY      int // start_height absolute (33)
	biomeAllow  map[string]bool
}

// NewBastionRemnantStartGen builds the generator from the embedded nether_complexes set, the
// bastion_remnant structure JSON, its start_pool, and the has_structure/bastion_remnant biome
// tag. It VERIFIES the shared placement (salt 30084232 / spacing 27 / separation 4) and the set
// weights ({fortress:2, bastion_remnant:3}) against the decoded JSON rather than trusting the
// numbers blind. A build-data error surfaces to the caller (the NetherGenerator panics on it).
func NewBastionRemnantStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:nether_complexes")
	if err != nil {
		return nil, err
	}
	if set.Placement.Salt != 30084232 || set.Placement.Spacing != 27 || set.Placement.Separation != 4 {
		return nil, fmt.Errorf("structure: nether_complexes placement = salt %d / spacing %d / separation %d; want 30084232 / 27 / 4",
			set.Placement.Salt, set.Placement.Spacing, set.Placement.Separation)
	}
	// VERIFY the shared-set weights match the fortress:2 / bastion:3 the pick relies on.
	var fw, bw int
	for _, e := range set.Structures {
		switch e.Structure {
		case netherComplexFortressID:
			fw = e.Weight
		case netherComplexBastionID:
			bw = e.Weight
		}
	}
	if fw != netherComplexFortressWeight || bw != netherComplexBastionWeight {
		return nil, fmt.Errorf("structure: nether_complexes weights = fortress %d / bastion %d; want %d / %d",
			fw, bw, netherComplexFortressWeight, netherComplexBastionWeight)
	}

	raw, err := data.StructureJSON(netherComplexBastionID)
	if err != nil {
		return nil, fmt.Errorf("structure: bastion_remnant structure: %w", err)
	}
	var bj bastionStructureJSON
	if err := json.Unmarshal(raw, &bj); err != nil {
		return nil, fmt.Errorf("structure: bastion_remnant structure json: %w", err)
	}
	pool, err := LoadTemplatePool(bj.StartPool)
	if err != nil {
		return nil, fmt.Errorf("structure: bastion_remnant start_pool %q: %w", bj.StartPool, err)
	}
	allow, err := HasStructureBiomes("bastion_remnant")
	if err != nil {
		return nil, fmt.Errorf("structure: bastion_remnant biome tag: %w", err)
	}

	return &bastionRemnantStartGen{
		placement:   set.Placement,
		startPool:   pool,
		maxDepth:    bj.Size,
		maxDistance: bj.MaxDistanceFromCenter,
		startY:      bj.StartHeight.Absolute,
		biomeAllow:  allow,
	}, nil
}

// GenerateStarts ports the nether_complexes half of ChunkGenerator.createStructures for the
// BASTION. The pick RNG is seeded setLargeFeatureSeed(seed,cx,cz) once; both set entries share
// the placement so both pass isStructureChunk together; the weighted-without-replacement loop
// draws nextInt(total), walks to an entry, and (for the bastion entry) biome-gates + assembles.
// The fortress entry is a no-op here (nether_fortress.go owns it): when it wins, the bastion
// produces nothing; when the bastion loses its biome gate it is removed and the loop redraws
// (landing on the fortress -> still nil here). This yields the exact one-of-set behavior.
func (g *bastionRemnantStartGen) GenerateStarts(seed int64, pos level.ChunkPos, _ SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])

	// The shared placement gate (salt 30084232 / spacing 27 / separation 4).
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	// The pick RNG (setLargeFeatureSeed(seed,cx,cz)) -- the SAME stream the fortress half uses.
	pickRng := levelgen.NewWorldgenRandom(0)
	pickRng.SetLargeFeatureSeed(seed, cx, cz)

	// The set entries in declaration order [fortress(2), bastion(3)]; both pass isStructureChunk
	// (shared placement), so the initial list is both, total 5. A removed entry is dropped from
	// the walk + its weight subtracted (weighted WITHOUT replacement).
	type entry struct {
		id     string
		weight int
	}
	remaining := []entry{
		{netherComplexFortressID, netherComplexFortressWeight},
		{netherComplexBastionID, netherComplexBastionWeight},
	}
	total := netherComplexFortressWeight + netherComplexBastionWeight

	centerX := cx*16 + 8
	centerZ := cz*16 + 8

	for len(remaining) > 0 {
		roll := int(pickRng.NextIntN(int32(total)))
		idx := 0
		for idx < len(remaining) {
			roll -= remaining[idx].weight
			if roll < 0 {
				break
			}
			idx++
		}
		chosen := remaining[idx]

		if chosen.id == netherComplexBastionID {
			// The bastion biome gate: getBiome at the chunk center at the start Y must be one of
			// the 4 nether biomes (EXCLUDING basalt_deltas). A pass assembles the bastion here.
			if g.biomeAllow[biomeAt(centerX, g.startY, centerZ).String()] {
				start := g.buildStart(seed, pos, cx, cz)
				if start.IsValid() {
					return []*StructureStart{start}
				}
				return nil
			}
			// Biome miss (basalt_deltas): remove the bastion, subtract its weight, redraw. The
			// loop then lands on the fortress -> nil here (the fortress generator handles it).
			total -= chosen.weight
			remaining = append(remaining[:idx], remaining[idx+1:]...)
			continue
		}

		// The fortress entry won: the chunk is a fortress, not a bastion. nether_fortress.go
		// assembles it; the bastion yields nothing. (The fortress biome #is_nether is always
		// valid in the nether, so the loop never removes the fortress and re-lands on bastion.)
		return nil
	}
	return nil
}

// buildStart ports JigsawStructure.findGenerationPoint for the bastion: startY = the
// ConstantHeight{absolute:33} (NO rng draw), startPos = (minBlockX, startY, minBlockZ); a
// SEPARATE piece RNG re-seeded setLargeFeatureSeed(seed,cx,cz) (CFR tryGenerateStructure own
// WorldgenRandom) drives JigsawPlacement.addPieces with projectStartToHeightmap=false (the
// bastion is fixed-Y rigid -- no surface projection), maxDepth=size, maxDistance=80.
func (g *bastionRemnantStartGen) buildStart(seed int64, pos level.ChunkPos, cx, cz int) *StructureStart {
	// A FRESH WorldgenRandom re-seeded setLargeFeatureSeed(seed,cx,cz) -- the jar
	// tryGenerateStructure makes its own WorldgenRandom for the assembly, DISTINCT from the
	// set-pick RNG (so the two draw streams do not interleave).
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	startPos := Pos{cx * 16, g.startY, cz * 16}
	pieces := addPieces(g.startPool, startPos, g.maxDepth, g.maxDistance, false, nil, rng)

	start := &StructureStart{
		Structure: netherComplexBastionID,
		ChunkPos:  pos,
	}
	for _, p := range pieces {
		start.Pieces = append(start.Pieces, p)
	}
	start.RecomputeBBox()
	return start
}
