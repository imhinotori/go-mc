package world

import (
	"encoding/json"
	"fmt"
	"sort"

	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// decorationData is the per-world feature graph applyBiomeDecoration consumes, built
// ONCE at NewNoiseGenerator construction (memoized — loud on a build-data error). It is
// read-only at decoration time (pure over (seed, pos)).
type decorationData struct {
	registry *feature.Registry
	sorter   *feature.FeatureSorter
	// biomeFeatures maps a biome to its 11-step []*PlacedFeature lists (the biome JSON
	// `features` array, resolved through the registry). The retained 3x3 biome set indexes
	// this to gather the step's referenced global indices.
	biomeFeatures map[levelbiome.Type][][]*feature.PlacedFeature
	// allowedBiomes[pf] is the set of biomes whose generation settings reference pf — the
	// BiomeFilter allowance (a feature seeded in biome A may not place in a candidate's
	// biome B unless B also lists it). Vanilla re-checks biome.hasFeature(pf) per position.
	allowedBiomes map[*feature.PlacedFeature]map[levelbiome.Type]bool
}

// decorationTrace records the per-feature seed/index discipline for the FEAT-02
// acceptance trace: each entry is what applyBiomeDecoration computed for ONE placed
// feature (the (step, globalIndex, decoSeed, featureSeed) the hand-derived oracle must
// match). nil disables tracing (production path).
type decorationTrace struct {
	entries []traceEntry
}

// traceEntry is one (step, globalIndex, decoSeed, featureSeed, featureID) record.
type traceEntry struct {
	step        int
	globalIndex int
	decoSeed    int64
	featureSeed int64
	featureID   string
}

// applyBiomeDecoration is the JAR-CONFIRMED outer decoration loop
// (net.minecraft.world.level.chunk.ChunkGenerator.applyBiomeDecoration), ported EXACTLY:
//
//	decoSeed = wg.SetDecorationSeed(worldSeed, originX=center.x*16, originZ=center.z*16)
//	for stepIndex in 0..11 (GenerationStep.Decoration):
//	    indices = IntSet
//	    for biome in retained 3x3 biome set:
//	        for pf in biome.features[stepIndex]: indices.add( sorter.Index(pf) )   // GLOBAL index
//	    for idx in sort(indices ascending):
//	        pf = sorter.PerStep(stepIndex)[idx]
//	        wg.SetFeatureSeed(decoSeed, idx, stepIndex)
//	        pf.placeWithBiomeCheck(view, wg, origin)   // bound modifier fold + biome re-check
//
// Subtleties (research Pitfall 3, all JAR-CONFIRMED):
//   - origin is the BLOCK origin (chunkX*16, chunkZ*16), NOT chunk coords.
//   - idx is the GLOBAL cross-biome index (FeatureSorter), NOT a per-biome counter — so a
//     feature shared by two biomes gets ONE seed.
//   - the IntSet+sort dedups + orders deterministically.
//   - placeWithBiomeCheck binds the `biome` modifier's allowance so a feature seeded in
//     biome A does not spill into biome B (re-checked per candidate position).
//
// SINGLE-THREADED ordered fold: no goroutines, no map iteration over the index set (the
// rng draw order is the determinism contract). Writes land through view.SetBlock (the
// 3x3 proxy keeps the worldgen heightmaps live). `trace` may be nil (production).
//
// makePlacer builds the ConfiguredFeaturePlacer for a placed_feature's resolved
// configured feature (11-03 supplies the recordable no-op dispatch; Phase 12 the bodies).
func applyBiomeDecoration(
	view *Neighborhood,
	biomes []levelbiome.Type,
	data *decorationData,
	ctx placement.PlacementContext,
	wg *levelgen.WorldgenRandom,
	worldSeed int64,
	makePlacer func(pf *feature.PlacedFeature) placement.PlacerFunc,
	trace *decorationTrace,
) {
	originX := int(view.center[0]) * 16
	originZ := int(view.center[1]) * 16
	decoSeed := wg.SetDecorationSeed(worldSeed, originX, originZ)
	origin := placement.BlockPos{X: originX, Y: 0, Z: originZ}

	for step := 0; step < feature.DecorationStepCount; step++ {
		// Gather the IntSet of GLOBAL feature indices any retained biome references at
		// this step (dedup: a feature shared by overlapping biomes counts once).
		indexSet := make(map[int]bool)
		for _, b := range biomes {
			perStep := data.biomeFeatures[b]
			if step >= len(perStep) {
				continue
			}
			for _, pf := range perStep[step] {
				if pf == nil {
					continue
				}
				if idx, ok := data.sorter.Index(pf); ok {
					indexSet[idx] = true
				}
			}
		}
		if len(indexSet) == 0 {
			continue
		}
		// Sort ascending — deterministic ordered fold (NOT map iteration).
		sorted := make([]int, 0, len(indexSet))
		for idx := range indexSet {
			sorted = append(sorted, idx)
		}
		sort.Ints(sorted)

		stepFeatures := data.sorter.PerStep(step)
		for _, idx := range sorted {
			if idx < 0 || idx >= len(stepFeatures) {
				continue
			}
			pf := stepFeatures[idx]

			// Per-feature seed: PURE over (worldSeed, originX, originZ, idx, step) — the
			// global index, NOT a per-biome counter. This is the determinism hinge.
			wg.SetFeatureSeed(decoSeed, idx, step)
			if trace != nil {
				trace.entries = append(trace.entries, traceEntry{
					step:        step,
					globalIndex: idx,
					decoSeed:    decoSeed,
					featureSeed: decoSeed + int64(idx) + int64(10000*step),
					featureID:   pf.ID,
				})
			}

			// Bind the placed_feature to its modifier bodies + the configured placer, then
			// place with the biome re-check (the `biome` modifier's allowance). A bind error
			// is a build-data bug (every embedded modifier is a ported type) — skip the
			// feature defensively rather than crash mid-decoration; LoadAll already validated
			// the roster at construction.
			allowed := data.allowedBiomes[pf]
			deps := placement.ModifierDeps{
				BiomeAllowed: func(b levelbiome.Type) bool {
					if allowed == nil {
						return true
					}
					return allowed[b]
				},
			}
			bound, err := placement.Bind(pf, makePlacer(pf), deps)
			if err != nil {
				continue
			}
			bound.Place(ctx, wg, origin)
		}
	}
}

// buildDecorationData parses the full feature roster + builds the FeatureSorter over ALL
// biomes ONCE (memoized at construction). It returns a loud error on any build-data
// mismatch (an unknown feature type, an unresolvable block-state ref, a missing biome
// reference, or a cross-biome ordering cycle) so NewNoiseGenerator panics at construction
// time, never mid-decoration. The biome `features` arrays come from the embedded biome
// JSONs (the 11-element placed_feature HolderSet per GenerationStep).
func buildDecorationData() (*decorationData, error) {
	reg := feature.NewEmbeddedRegistry()
	if err := reg.LoadAllEmbedded(); err != nil {
		return nil, err
	}

	biomeIDs, err := data.BiomeIDs()
	if err != nil {
		return nil, fmt.Errorf("world: decoration: listing biome ids: %w", err)
	}

	biomeFeatures := make(map[levelbiome.Type][][]*feature.PlacedFeature, len(biomeIDs))
	allowed := make(map[*feature.PlacedFeature]map[levelbiome.Type]bool)

	// A stable, deterministic biome iteration order for the sorter input: the biome ids
	// are listed in a fixed order by the embed lister, so the sorter's first-seen tie-break
	// is reproducible.
	var sorterInput [][][]*feature.PlacedFeature

	for _, id := range biomeIDs {
		var bt levelbiome.Type
		if err := bt.UnmarshalText([]byte("minecraft:" + id)); err != nil {
			// A biome present in the worldgen data but absent from the protocol biome table
			// is a build-data mismatch — surface it loudly.
			return nil, fmt.Errorf("world: decoration: biome %q has no protocol biome.Type: %w", id, err)
		}
		steps, err := loadBiomeFeatures(reg, "minecraft:"+id)
		if err != nil {
			return nil, err
		}
		biomeFeatures[bt] = steps
		sorterInput = append(sorterInput, steps)

		// Record the biome's allowance for every placed_feature it references.
		for _, stepList := range steps {
			for _, pf := range stepList {
				if pf == nil {
					continue
				}
				if allowed[pf] == nil {
					allowed[pf] = make(map[levelbiome.Type]bool)
				}
				allowed[pf][bt] = true
			}
		}
	}

	sorter, err := feature.BuildFeaturesPerStep(sorterInput)
	if err != nil {
		return nil, err
	}

	return &decorationData{
		registry:      reg,
		sorter:        sorter,
		biomeFeatures: biomeFeatures,
		allowedBiomes: allowed,
	}, nil
}

// retainedBiomes returns the DISTINCT biomes across the center + its 8 neighbors — the
// "retained possible biome set" applyBiomeDecoration iterates. It reads the biomes ALREADY
// stored in each chunk's per-section 4×4×4 biome palette containers (FillBiomes wrote them
// during terrain gen from the same multi-noise source), so it does NOT re-evaluate the
// climate density functions + the RTree per quart cell — that re-sampling of the whole
// 96-level vertical column × 16 columns × 9 chunks was the dominant decoration cost
// (~79% of Decorate). Reading the palettes is the identical SET (the palette holds exactly
// the biomes FillBiomes placed) at a fraction of the cost. The result is sorted by biome id
// so the gather order is reproducible.
func retainedBiomes(view *Neighborhood) []levelbiome.Type {
	seen := make(map[levelbiome.Type]bool)
	var out []levelbiome.Type
	for _, ch := range view.chunks {
		if ch == nil {
			continue
		}
		for si := range ch.Sections {
			for _, b := range ch.Sections[si].Biomes.Palette() {
				if !seen[b] {
					seen[b] = true
					out = append(out, b)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// loadBiomeFeatures reads a biome's embedded JSON `features` array (the 11-element
// list-of-lists of placed_feature ids, one inner list per GenerationStep.Decoration) and
// resolves every id to its parsed *feature.PlacedFeature through the registry (DAG-deduped
// — the SAME pointer the FeatureSorter keys on). The result is a per-step []*PlacedFeature
// in the biome's declared order (the order the FeatureSorter toposorts against). A biome's
// features array may be shorter or longer than 11; it is normalized to exactly
// DecorationStepCount steps (extra steps, if any, are appended — vanilla iterates
// max(11, size)).
func loadBiomeFeatures(reg *feature.Registry, biomeID string) ([][]*feature.PlacedFeature, error) {
	raw, err := data.BiomeJSON(biomeID)
	if err != nil {
		return nil, fmt.Errorf("world: decoration: load biome %q: %w", biomeID, err)
	}
	var doc struct {
		Features [][]string `json:"features"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("world: decoration: parse biome %q features: %w", biomeID, err)
	}

	nSteps := feature.DecorationStepCount
	if len(doc.Features) > nSteps {
		nSteps = len(doc.Features)
	}
	steps := make([][]*feature.PlacedFeature, nSteps)
	for i := range steps {
		steps[i] = nil
	}
	for step, ids := range doc.Features {
		for _, pfID := range ids {
			pf, err := reg.ResolvePlaced(pfID)
			if err != nil {
				return nil, fmt.Errorf("world: decoration: biome %q step %d: resolve placed_feature %q: %w", biomeID, step, pfID, err)
			}
			steps[step] = append(steps[step], pf)
		}
	}
	return steps, nil
}
