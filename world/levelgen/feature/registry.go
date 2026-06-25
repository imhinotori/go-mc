package feature

import (
	"fmt"
)

// Registry caches parsed configured_feature + placed_feature by id — the
// HolderHolder dedup: a configured_feature shared by many placed_features (oak is
// referenced by dozens) is parsed ONCE and the same *ConfiguredFeature instance is
// returned, so the graph is a DAG, not a re-parsed tree. It also detects cycles (a
// malformed graph would otherwise recurse forever — T-11-02) via a parsing set,
// ported from density.Registry. This is the parse-and-bind foundation plans 11-02
// (modifier bodies) and 11-03 (applyBiomeDecoration) consume.
type Registry struct {
	data DataSource

	configuredCache map[string]*ConfiguredFeature
	placedCache     map[string]*PlacedFeature

	// parsing tracks ids currently being resolved (cycle guard). Keys are
	// namespaced "cf:<id>" / "pf:<id>" so a placed->configured->placed loop is
	// caught regardless of which kind re-enters.
	parsing map[string]bool
}

// NewRegistry creates a feature registry over a DataSource (the embedded
// configured_feature/placed_feature accessors, or an in-memory test source).
func NewRegistry(data DataSource) *Registry {
	return &Registry{
		data:            data,
		configuredCache: make(map[string]*ConfiguredFeature),
		placedCache:     make(map[string]*PlacedFeature),
		parsing:         make(map[string]bool),
	}
}

// ResolveConfigured parses (or returns the cached) configured_feature for a registry
// ref id. It lazily loads the file via the DataSource, parses it, and caches the
// result so a shared configured_feature is built once (dedup). A cyclic reference is
// rejected by the parsing-set guard (T-11-02).
func (r *Registry) ResolveConfigured(id string) (*ConfiguredFeature, error) {
	if cf, ok := r.configuredCache[id]; ok {
		return cf, nil
	}
	key := "cf:" + id
	if r.parsing[key] {
		return nil, fmt.Errorf("feature: cyclic configured_feature reference %q", id)
	}
	if r.data == nil {
		return nil, fmt.Errorf("feature: cannot resolve configured_feature %q: no data source", id)
	}
	raw, err := r.data.ConfiguredFeatureJSON(id)
	if err != nil {
		return nil, fmt.Errorf("feature: loading configured_feature %q: %w", id, err)
	}
	r.parsing[key] = true
	cf, err := r.ParseConfiguredFeature(id, raw)
	delete(r.parsing, key)
	if err != nil {
		return nil, fmt.Errorf("feature: parsing configured_feature %q: %w", id, err)
	}
	r.configuredCache[id] = cf
	return cf, nil
}

// ResolvePlaced parses (or returns the cached) placed_feature for a registry ref id.
// It lazily loads the file, parses it (resolving its configured-feature ref through
// ResolveConfigured), and caches the result. The parsing-set guard rejects a cyclic
// placed->configured->placed reference (T-11-02).
func (r *Registry) ResolvePlaced(id string) (*PlacedFeature, error) {
	if pf, ok := r.placedCache[id]; ok {
		return pf, nil
	}
	key := "pf:" + id
	if r.parsing[key] {
		return nil, fmt.Errorf("feature: cyclic placed_feature reference %q", id)
	}
	if r.data == nil {
		return nil, fmt.Errorf("feature: cannot resolve placed_feature %q: no data source", id)
	}
	raw, err := r.data.PlacedFeatureJSON(id)
	if err != nil {
		return nil, fmt.Errorf("feature: loading placed_feature %q: %w", id, err)
	}
	r.parsing[key] = true
	pf, err := r.ParsePlacedFeature(id, raw)
	delete(r.parsing, key)
	if err != nil {
		return nil, fmt.Errorf("feature: parsing placed_feature %q: %w", id, err)
	}
	r.placedCache[id] = pf
	return pf, nil
}

// PlacedByID returns the cached parsed *PlacedFeature for an id (nil if not yet
// resolved). Plans 11-02/11-03 consume the parsed graph through this accessor.
func (r *Registry) PlacedByID(id string) *PlacedFeature { return r.placedCache[id] }

// ConfiguredByID returns the cached parsed *ConfiguredFeature for an id (nil if not
// yet resolved).
func (r *Registry) ConfiguredByID(id string) *ConfiguredFeature { return r.configuredCache[id] }

// LoadAll parses EVERY embedded configured_feature + placed_feature entry (the
// 226 + 262 the 26.2 jar ships), caching the parsed graph for plans 11-02/11-03. It
// returns the FIRST error — a genuinely-unknown feature type or an unresolvable
// block-state ref — so a build-data mismatch fails LOUDLY at generator-construction
// time, NOT mid-decoration. Every placed_feature's configured ref is resolved to a
// non-nil ConfiguredFeature (DAG-deduped via the caches). idLister supplies the two
// id lists (data.ConfiguredFeatureIDs / data.PlacedFeatureIDs).
func (r *Registry) LoadAll(configuredIDs, placedIDs []string) error {
	// 1. Resolve every configured_feature (so an unknown type / bad block-state ref
	//    surfaces even for a configured_feature no placed_feature references).
	for _, id := range configuredIDs {
		ref := "minecraft:" + id
		if _, err := r.ResolveConfigured(ref); err != nil {
			return fmt.Errorf("feature: LoadAll configured_feature %q: %w", id, err)
		}
	}
	// 2. Resolve every placed_feature and assert its configured ref resolved.
	for _, id := range placedIDs {
		ref := "minecraft:" + id
		pf, err := r.ResolvePlaced(ref)
		if err != nil {
			return fmt.Errorf("feature: LoadAll placed_feature %q: %w", id, err)
		}
		if pf.Feature == nil {
			return fmt.Errorf("feature: LoadAll placed_feature %q resolved to a nil configured_feature", id)
		}
	}
	return nil
}
