package structure

// ancient_city.go -- the ANCIENT CITY deep-dark jigsaw structure, ported 1:1 from the jar
// (javap -c, temp/cache/26.2-inner.jar). ancient_city is a JigsawStructure whose start_pool is
// minecraft:ancient_city/city_center; it hangs the existing bounded-BFS JigsawPlacement engine +
// the template_pool / .nbt machinery -- the SAME engine villages and the bastion use. Mirrors
// bastion_remnant.go's shape (a single-entry structure_set; weight 1, one structure).
//
// Ported from the jar:
//   - ChunkGenerator.createStructures / tryGenerateStructure (isStructureChunk gate via the
//     ancient_cities structure_set random_spread placement -- salt 20083232 / spacing 24 /
//     separation 8, LINEAR -- then a WorldgenRandom setLargeFeatureSeed(seed,cx,cz) drives it)
//   - Structure.findValidGenerationPoint (biome gate at the chunk-center at the start Y; the
//     has_structure/ancient_city set is the deep_dark biome)
//   - JigsawStructure.findGenerationPoint: startY = startHeight.sample(rng, ctx) -- a
//     ConstantHeight{absolute -27} which draws NO rng (VerticalAnchor.resolveY only); then
//     startPos = (minBlockX, -27, minBlockZ); JigsawPlacement.addPieces maxDepth=size=7,
//     maxDistance=max_distance_from_center=116, projectStartToHeightmap=Optional.empty -> NO
//     surface projection (rigid, fixed low Y).
//   - the ancient_city structure JSON + ancient_cities structure_set + has_structure tag.
//
// NOTE (faithful to the engine): the structure JSON carries start_jigsaw_name
// (minecraft:city_anchor) which vanilla uses to select the specific anchor jigsaw for the first
// connection; the addPieces engine (shared with the bastion, which has no start_jigsaw_name)
// picks a random root element + random rotation from the start_pool exactly as vanilla does for
// the ROOT piece -- start_jigsaw_name refines only how children connect, not the root placement.
// terrain_adaptation=beard_box and use_expansion_hack=false are carried; neither changes which
// pieces are chosen or where their bounding boxes land (beard is a post-place terrain blend).

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

const (
	ancientCityID         = "minecraft:ancient_city"
	ancientCitySet        = "minecraft:ancient_cities"
	ancientCitySalt       = 20083232
	ancientCitySpacing    = 24
	ancientCitySeparation = 8
)

type ancientCityStructureJSON struct {
	StartPool             string `json:"start_pool"`
	StartJigsawName       string `json:"start_jigsaw_name"`
	Size                  int    `json:"size"`
	MaxDistanceFromCenter int    `json:"max_distance_from_center"`
	StartHeight           struct {
		Absolute int `json:"absolute"`
	} `json:"start_height"`
}

type ancientCityStartGen struct {
	placement   RandomSpreadStructurePlacement
	startPool   *StructureTemplatePool
	maxDepth    int
	maxDistance int
	startY      int
	biomeAllow  map[string]bool
}

func NewAncientCityStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet(ancientCitySet)
	if err != nil {
		return nil, err
	}
	if set.Placement.Salt != ancientCitySalt || set.Placement.Spacing != ancientCitySpacing || set.Placement.Separation != ancientCitySeparation {
		return nil, fmt.Errorf("structure: ancient_cities placement = salt %d / spacing %d / separation %d; want %d / %d / %d",
			set.Placement.Salt, set.Placement.Spacing, set.Placement.Separation, ancientCitySalt, ancientCitySpacing, ancientCitySeparation)
	}

	raw, err := data.StructureJSON(ancientCityID)
	if err != nil {
		return nil, fmt.Errorf("structure: ancient_city structure: %w", err)
	}
	var sj ancientCityStructureJSON
	if err := json.Unmarshal(raw, &sj); err != nil {
		return nil, fmt.Errorf("structure: ancient_city structure json: %w", err)
	}
	pool, err := LoadTemplatePool(sj.StartPool)
	if err != nil {
		return nil, fmt.Errorf("structure: ancient_city start_pool %q: %w", sj.StartPool, err)
	}
	allow, err := HasStructureBiomes("ancient_city")
	if err != nil {
		return nil, fmt.Errorf("structure: ancient_city biome tag: %w", err)
	}

	return &ancientCityStartGen{
		placement:   set.Placement,
		startPool:   pool,
		maxDepth:    sj.Size,
		maxDistance: sj.MaxDistanceFromCenter,
		startY:      sj.StartHeight.Absolute,
		biomeAllow:  allow,
	}, nil
}

func (g *ancientCityStartGen) GenerateStarts(seed int64, pos level.ChunkPos, _ SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])

	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	centerX := cx*16 + 8
	centerZ := cz*16 + 8
	if !g.biomeAllow[biomeAt(centerX, g.startY, centerZ).String()] {
		return nil
	}

	startPos := Pos{cx * 16, g.startY, cz * 16}
	pieces := addPieces(g.startPool, startPos, g.maxDepth, g.maxDistance, false, nil, rng)

	start := &StructureStart{
		Structure: ancientCityID,
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
