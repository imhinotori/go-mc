package structure

// trial_chambers.go -- the TRIAL CHAMBERS deep-underground jigsaw structure, ported 1:1 from the
// jar (javap -c, temp/cache/26.2-inner.jar). trial_chambers is a JigsawStructure whose start_pool
// is minecraft:trial_chambers/chamber/end; it hangs the existing bounded-BFS JigsawPlacement
// engine + the template_pool / .nbt machinery -- the SAME engine villages and the bastion use.
// This mirrors bastion_remnant.go's shape (a single-entry structure_set; weight 1, one structure,
// so no weighted-without-replacement pick is needed).
//
// Ported from the jar:
//   - ChunkGenerator.createStructures / tryGenerateStructure (isStructureChunk gate via the
//     trial_chambers structure_set random_spread placement, then a WorldgenRandom
//     setLargeFeatureSeed(seed,cx,cz) drives the assembly)
//   - Structure.findValidGenerationPoint (biome gate at the chunk-center at the start Y)
//   - JigsawStructure.findGenerationPoint: startY = startHeight.sample(rng, ctx) -- a
//     UniformHeight{min absolute -40, max absolute -20} which draws ONE rng int
//     (Mth.randomBetweenInclusive = rng.nextInt(max-min+1)+min); then startPos =
//     (minBlockX, startY, minBlockZ); JigsawPlacement.addPieces maxDepth=size=20,
//     maxDistance=max_distance_from_center=116, projectStartToHeightmap=Optional.empty -> NO
//     surface projection (rigid, fixed low Y).
//   - the trial_chambers structure JSON + trial_chambers structure_set + has_structure tag.
//
// STUBBED (cited): the structure JSON carries pool_aliases (a random_group/random alias table
// mapping the spawner CONTENTS placeholder pools -- ranged/melee/etc -- to concrete mob spawner
// pools via PoolAliasLookup.create(aliases, pos, seed)). Those aliases affect only WHICH mob a
// trial spawner block-entity spawns, NOT piece placement / bounding boxes; the mob-spawner
// block-entity contents are a deferred subsystem (like desert_pyramid loot). addPieces uses the
// pools verbatim (no alias remap). The chamber GEOMETRY -- rooms/corridors/connectors -- is exact.
// dimension_padding (10) and liquid_settings (ignore_waterlogging) are likewise carried but not
// modeled by addPieces (v3 placement refinements; they do not change which pieces are chosen or
// where their bounding boxes land).

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

const (
	trialChambersID         = "minecraft:trial_chambers"
	trialChambersSet        = "minecraft:trial_chambers"
	trialChambersSalt       = 94251327
	trialChambersSpacing    = 34
	trialChambersSeparation = 12
)

type trialChambersStructureJSON struct {
	StartPool             string `json:"start_pool"`
	Size                  int    `json:"size"`
	MaxDistanceFromCenter int    `json:"max_distance_from_center"`
	StartHeight           struct {
		Type         string `json:"type"`
		MinInclusive struct {
			Absolute int `json:"absolute"`
		} `json:"min_inclusive"`
		MaxInclusive struct {
			Absolute int `json:"absolute"`
		} `json:"max_inclusive"`
	} `json:"start_height"`
}

type trialChambersStartGen struct {
	placement   RandomSpreadStructurePlacement
	startPool   *StructureTemplatePool
	maxDepth    int
	maxDistance int
	startYMin   int
	startYMax   int
	biomeAllow  map[string]bool
}

func NewTrialChambersStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet(trialChambersSet)
	if err != nil {
		return nil, err
	}
	if set.Placement.Salt != trialChambersSalt || set.Placement.Spacing != trialChambersSpacing || set.Placement.Separation != trialChambersSeparation {
		return nil, fmt.Errorf("structure: trial_chambers placement = salt %d / spacing %d / separation %d; want %d / %d / %d",
			set.Placement.Salt, set.Placement.Spacing, set.Placement.Separation, trialChambersSalt, trialChambersSpacing, trialChambersSeparation)
	}

	raw, err := data.StructureJSON(trialChambersID)
	if err != nil {
		return nil, fmt.Errorf("structure: trial_chambers structure: %w", err)
	}
	var sj trialChambersStructureJSON
	if err := json.Unmarshal(raw, &sj); err != nil {
		return nil, fmt.Errorf("structure: trial_chambers structure json: %w", err)
	}
	pool, err := LoadTemplatePool(sj.StartPool)
	if err != nil {
		return nil, fmt.Errorf("structure: trial_chambers start_pool %q: %w", sj.StartPool, err)
	}
	allow, err := HasStructureBiomes("trial_chambers")
	if err != nil {
		return nil, fmt.Errorf("structure: trial_chambers biome tag: %w", err)
	}

	return &trialChambersStartGen{
		placement:   set.Placement,
		startPool:   pool,
		maxDepth:    sj.Size,
		maxDistance: sj.MaxDistanceFromCenter,
		startYMin:   sj.StartHeight.MinInclusive.Absolute,
		startYMax:   sj.StartHeight.MaxInclusive.Absolute,
		biomeAllow:  allow,
	}, nil
}

func (g *trialChambersStartGen) GenerateStarts(seed int64, pos level.ChunkPos, _ SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])

	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	startY := g.startYMin + int(rng.NextIntN(int32(g.startYMax-g.startYMin+1)))

	centerX := cx*16 + 8
	centerZ := cz*16 + 8
	if !g.biomeAllow[biomeAt(centerX, startY, centerZ).String()] {
		return nil
	}

	startPos := Pos{cx * 16, startY, cz * 16}
	pieces := addPieces(g.startPool, startPos, g.maxDepth, g.maxDistance, false, nil, rng)

	start := &StructureStart{
		Structure: trialChambersID,
		ChunkPos:  pos,
	}
	for _, p := range pieces {
		start.Pieces = append(start.Pieces, p)
	}
	start.RecomputeBBox()
	if !start.IsValid() {
		return nil
	}
	return []*StructureStart{start}
}
