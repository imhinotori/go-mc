package world

import (
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
	"github.com/imhinotori/sulfur/world/structure"
)

// structureFeaturePoolView is the package-world half of FeaturePoolElement.place.
// structure.FeaturePoolElement cannot import package world, so NoiseGenerator wraps the
// live Neighborhood with this adapter during the structure PLACE pass.
type structureFeaturePoolView struct {
	*Neighborhood
	registry *feature.Registry
	seaLevel int
	biomeAt  func(x, y, z int) levelbiome.Type
}

// PlaceFeaturePoolElement ports the delegate reached by FeaturePoolElement.place:
// PlacedFeature.place(level, generator, rng, origin), i.e. a normal placed_feature fold
// over the supplied origin and threaded rng. Unlike biome decoration, vanilla constructs
// PlacementContext with Optional.empty() for topFeature here; village feature-pool refs
// do not carry biome filters, so no biome allowance is supplied.
func (v structureFeaturePoolView) PlaceFeaturePoolElement(featureID string, origin structure.Pos, rng levelgen.RandomSource) bool {
	if v.Neighborhood == nil || v.registry == nil || featureID == "" {
		return false
	}
	if _, deferred := deferredStructureFeaturePoolRef(featureID); deferred {
		return false
	}
	pf, err := v.registry.ResolvePlaced(featureID)
	if err != nil {
		panic("world: structure feature pool resolve " + featureID + ": " + err.Error())
	}
	if pf == nil || pf.Feature == nil {
		return false
	}
	placer := newConfiguredPlacer(pf.Feature, v.Neighborhood, v.registry, v.air, false, nil, v.seaLevel)
	bound, err := placement.Bind(pf, placer, placement.ModifierDeps{})
	if err != nil {
		panic("world: structure feature pool bind " + featureID + ": " + err.Error())
	}
	ctx := newPlacementContext(v.Neighborhood, v.minY, v.height, v.biomeAt)
	return bound.Place(ctx, rng, placement.BlockPos{X: origin.X, Y: origin.Y, Z: origin.Z})
}

var _ structure.FeaturePoolElementPlacer = structureFeaturePoolView{}

// deferredStructureFeaturePoolRef names village feature-pool refs that currently bottom
// out in provider bodies this repo still marks explicitly deferred. Returning before
// binding preserves the RNG stream and avoids pretending those visible decor features were
// faithfully placed.
func deferredStructureFeaturePoolRef(id string) (string, bool) {
	switch id {
	case "minecraft:flower_plain", "flower_plain":
		return "simple_block noise_threshold_provider is not ported", true
	case "minecraft:pile_hay", "pile_hay":
		return "block_pile rotated_block_provider is not ported", true
	default:
		return "", false
	}
}
