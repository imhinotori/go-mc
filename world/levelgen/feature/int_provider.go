package feature

import (
	"encoding/json"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// int_provider.go exports the in-package IntProvider (constant / uniform / weighted_list)
// so the Phase-13 Nether feature bodies in package world can sample the ColumnFeature-
// Configuration (height/reach) and DeltaFeatureConfiguration (size/rim_size) IntProviders
// with the SAME jar-faithful sample() draw order the tree placers use. The underlying
// intProvider interface + parseIntProvider stay unexported (tree.go); this is a thin,
// zero-behaviour-change wrapper.
//
// Source: net.minecraft.util.valueproviders.IntProvider.sample(RandomSource).

// IntProvider is the exported IntProvider surface: Sample(rng) -> int, matching
// net.minecraft.util.valueproviders.IntProvider.sample. The draw count IS a determinism
// contract (constant = 0 draws; uniform = one nextInt(max-min+1); weighted_list = one
// pick draw + the inner provider's draws).
type IntProvider interface {
	Sample(rng levelgen.RandomSource) int
}

// exportedIntProvider adapts the unexported intProvider to the exported Sample name.
type exportedIntProvider struct{ p intProvider }

func (e exportedIntProvider) Sample(rng levelgen.RandomSource) int { return e.p.sample(rng) }

// ParseIntProvider decodes the bare-int OR {type:constant,value} OR
// {type:uniform,min_inclusive,max_inclusive} OR {type:weighted_list,...} IntProvider
// forms (delegating to the shared in-package parseIntProvider). An unported type errors
// LOUDLY, exactly like the tree path.
func ParseIntProvider(raw json.RawMessage) (IntProvider, error) {
	p, err := parseIntProvider(raw)
	if err != nil {
		return nil, err
	}
	return exportedIntProvider{p: p}, nil
}
