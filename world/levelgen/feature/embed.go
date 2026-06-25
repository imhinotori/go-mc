package feature

import (
	"fmt"

	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// NewEmbeddedRegistry builds a feature.Registry wired to the embedded
// configured_feature/placed_feature trees (world/levelgen/data). This is the
// canonical construction plans 11-02/11-03 consume: the parsed feature graph backed
// by the build-time-trusted embed. Pair it with LoadAllEmbedded to parse + validate
// the whole roster up front (so a build-data mismatch fails loudly at construction).
func NewEmbeddedRegistry() *Registry {
	return NewRegistry(NewFuncSource(data.ConfiguredFeatureJSON, data.PlacedFeatureJSON))
}

// LoadAllEmbedded lists every embedded configured_feature + placed_feature id and
// parses them all via Registry.LoadAll, returning the first genuinely-unknown type
// or unresolvable block-state ref. It is the FEAT-02 "the polymorphic parser loads
// the embedded data and binds them" acceptance — call it once at generator
// construction so a partial/incompatible embed fails LOUDLY, not mid-decoration.
func (r *Registry) LoadAllEmbedded() error {
	configuredIDs, err := data.ConfiguredFeatureIDs()
	if err != nil {
		return fmt.Errorf("feature: listing configured_feature ids: %w", err)
	}
	placedIDs, err := data.PlacedFeatureIDs()
	if err != nil {
		return fmt.Errorf("feature: listing placed_feature ids: %w", err)
	}
	return r.LoadAll(configuredIDs, placedIDs)
}
