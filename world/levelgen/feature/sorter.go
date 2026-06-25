package feature

import (
	"fmt"
)

// FeatureSorter ports net.minecraft.world.level.levelgen.FeatureSorter — the
// cross-biome decoration ordering that gives every placed_feature ONE global
// per-step index. It is built ONCE at generator construction (memoized) and
// consumed by 11-03's applyBiomeDecoration:
//
//   - Per GenerationStep.Decoration step it produces a FLAT ordered []*PlacedFeature
//     (the per-step `features` list) plus an indexMapping (placed_feature -> its
//     global index within that step's flat list).
//   - A placed_feature referenced by MANY biomes at the same step is deduped to ONE
//     entry / ONE index (the IntSet dedup — the same feature gets the same per-feature
//     decoration seed regardless of which biome referenced it; research Pitfall 3a/3b).
//   - The relative order is a topological sort consistent with EVERY biome's per-step
//     list order: if biome A lists p1 before p2 and biome B lists p2 before p3, the
//     global order is p1, p2, p3. A CONTRADICTION (A: p1<p2, B: p2<p1) is a cycle and
//     errors LOUDLY — exactly as vanilla's FeatureSorter throws on a cycle.
//
// JAR-CONFIRMED: FeatureSorter.buildFeaturesPerStep builds, per step, a directed
// graph whose edges are the consecutive constraints (list[i] must precede list[i+1])
// from each biome's per-step feature list, then topologically sorts it; a cycle is an
// IllegalStateException. This is an algorithmic port, not a copy of Mojang source.
type FeatureSorter struct {
	// perStep[stepIndex] is the deduped, topologically-ordered flat list of
	// placed_features for that GenerationStep.Decoration step.
	perStep [][]*PlacedFeature
	// index maps a *PlacedFeature to its global index WITHIN its step's flat list.
	// The same *PlacedFeature pointer (DAG-deduped by the Registry) is a stable key.
	index map[*PlacedFeature]int
}

// DecorationStepCount is the number of GenerationStep.Decoration steps (the
// 11-element per-biome `features` array). JAR-CONFIRMED enum length.
const DecorationStepCount = 11

// BuildFeaturesPerStep ports FeatureSorter.buildFeaturesPerStep. biomeFeatures is the
// retained biome set's per-step feature lists: one entry per biome, each entry an
// 11-element (DecorationStepCount) slice of []*PlacedFeature (the placed_features that
// biome runs at that step, in the biome's declared order). A biome whose features
// array is shorter than 11 steps simply contributes nothing past its length.
//
// It returns a FeatureSorter whose PerStep(step) is the deduped + toposorted flat
// list and Index(pf) the global per-step index. A cross-biome ordering CYCLE (two
// biomes disagreeing on the relative order of two shared features) errors loudly.
//
// The result is INDEPENDENT of the biome iteration order: the toposort breaks ties by
// FIRST-SEEN insertion order, which is itself derived deterministically from the input
// (the caller passes biomes in a stable order — 11-03 sorts the retained biome set).
func BuildFeaturesPerStep(biomeFeatures [][][]*PlacedFeature) (*FeatureSorter, error) {
	fs := &FeatureSorter{
		perStep: make([][]*PlacedFeature, DecorationStepCount),
		index:   make(map[*PlacedFeature]int),
	}

	for step := 0; step < DecorationStepCount; step++ {
		ordered, err := sortStep(biomeFeatures, step)
		if err != nil {
			return nil, fmt.Errorf("feature: FeatureSorter step %d: %w", step, err)
		}
		fs.perStep[step] = ordered
		for i, pf := range ordered {
			fs.index[pf] = i
		}
	}
	return fs, nil
}

// sortStep builds + topologically sorts ONE step's cross-biome feature graph.
//
// Nodes = the distinct placed_features any biome references at this step, in FIRST-SEEN
// order (the deterministic tie-break). Edges = the consecutive ordering constraints
// list[i] -> list[i+1] from every biome's per-step list (a feature must come before the
// one that follows it in any biome). Kahn's algorithm produces the topological order,
// breaking ties by first-seen index (stable); a leftover node (a cycle) is an error.
func sortStep(biomeFeatures [][][]*PlacedFeature, step int) ([]*PlacedFeature, error) {
	// nodes: distinct placed_features in first-seen order; nodeIndex: pf -> node id.
	var nodes []*PlacedFeature
	nodeIndex := make(map[*PlacedFeature]int)
	addNode := func(pf *PlacedFeature) int {
		if id, ok := nodeIndex[pf]; ok {
			return id
		}
		id := len(nodes)
		nodes = append(nodes, pf)
		nodeIndex[pf] = id
		return id
	}

	// edges[a] is the SET of successors of node a (dedup edges so a constraint repeated
	// across biomes does not inflate the in-degree count).
	edges := make([]map[int]bool, 0)
	ensure := func(id int) {
		for len(edges) <= id {
			edges = append(edges, make(map[int]bool))
		}
	}

	for _, biome := range biomeFeatures {
		if step >= len(biome) {
			continue
		}
		list := biome[step]
		var prev = -1
		for _, pf := range list {
			if pf == nil {
				continue
			}
			cur := addNode(pf)
			ensure(cur)
			if prev >= 0 && prev != cur {
				ensure(prev)
				edges[prev][cur] = true
			}
			prev = cur
		}
	}

	// In-degree from the deduped edge sets.
	indeg := make([]int, len(nodes))
	for a := range edges {
		for b := range edges[a] {
			indeg[b]++
		}
	}

	// Kahn's algorithm with a first-seen (ascending node-id) ready set so the output is
	// deterministic regardless of biome iteration order: among nodes with in-degree 0 we
	// always emit the lowest node id (= earliest first-seen) next.
	out := make([]*PlacedFeature, 0, len(nodes))
	emitted := make([]bool, len(nodes))
	for len(out) < len(nodes) {
		next := -1
		for id := 0; id < len(nodes); id++ {
			if !emitted[id] && indeg[id] == 0 {
				next = id
				break
			}
		}
		if next < 0 {
			// No in-degree-0 node remains but nodes are unemitted -> a cycle: two biomes
			// disagree on the relative order of two shared features (vanilla throws here).
			return nil, fmt.Errorf("ordering cycle among cross-biome placed_features (contradictory relative order)")
		}
		emitted[next] = true
		out = append(out, nodes[next])
		if next < len(edges) {
			// Decrement successors' in-degree. emitted[next] guards `next` from re-scan, so
			// its own (now-stale) in-degree is irrelevant.
			for b := range edges[next] {
				indeg[b]--
			}
		}
	}
	return out, nil
}

// PerStep returns the deduped, toposorted flat placed_feature list for a step (nil for
// an out-of-range step). applyBiomeDecoration indexes into this by the global index.
func (fs *FeatureSorter) PerStep(step int) []*PlacedFeature {
	if step < 0 || step >= len(fs.perStep) {
		return nil
	}
	return fs.perStep[step]
}

// Index returns the global per-step index of a placed_feature and whether it is known.
// This is FeatureSorter.indexMapping: applyBiomeDecoration adds Index(pf) to the
// step's IntSet for each biome's referenced feature, then sorts those indices.
func (fs *FeatureSorter) Index(pf *PlacedFeature) (int, bool) {
	i, ok := fs.index[pf]
	return i, ok
}
