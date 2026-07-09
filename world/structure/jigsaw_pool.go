package structure

// jigsaw_pool.go — STRUCT-05 part 2: the template_pool / pool-element model the
// JigsawPlacement.Placer (jigsaw_placement.go) drives. It parses a template_pool JSON
// (CFR StructureTemplatePool) into a weighted list of PoolElements, resolves each
// element's .nbt template + processor list via the 16-01 layer, and reproduces the
// jar's weighted-candidate selection (expand-by-weight then Fisher-Yates shuffle, the
// EXACT draw count — Pitfall #6 "piece-selection RNG draws desync").
//
// Ported (idiomatic Go, no GPL paste) from CFR (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.structure.pools.StructureTemplatePool
//     (the constructor's weight-expansion of `templates`, getShuffledTemplates =
//      Util.shuffledCopy(templates, rng), size(), getFallback())
//   - net.minecraft.world.level.levelgen.structure.pools.SinglePoolElement
//     (getShuffledJigsawBlocks = getJigsaws then Util.shuffle then sortBySelectionPriority;
//      getBoundingBox(ZERO, rot); place via the StructureTemplate + processors)
//   - net.minecraft.world.level.levelgen.structure.pools.LegacySinglePoolElement
//     (the village's element — same as Single but the jigsaw blocks place AIR, the 16-01
//      LegacySingle semantics already baked into StructureTemplate.PlaceInWorld)
//   - net.minecraft.world.level.levelgen.structure.pools.ListPoolElement (an ordered
//      sub-element list placed together — STUBBED to the first sub-element's geometry;
//      villages never use it, but it parses)
//   - net.minecraft.world.level.levelgen.structure.pools.FeaturePoolElement (places a
//      placed_feature via the package-world FeaturePoolElementPlacer adapter, and exposes
//      its bytecode-verified single bottom jigsaw)
//   - net.minecraft.world.level.levelgen.structure.pools.EmptyPoolElement (the terminator
//      INSTANCE — its getShuffledJigsawBlocks is empty and it never places)
//   - net.minecraft.util.Util.shuffle / shuffledCopy (the Fisher-Yates draw)

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// Projection ports StructureTemplatePool$Projection: RIGID (the element keeps its exact
// shape; villages are rigid) or TERRAIN_MATCHING (the element drapes over terrain — a v3
// refinement; treated as rigid here since the Placer's rigid path is the village path).
type Projection int

const (
	// ProjectionRigid (the default + the village projection): exact placement, the
	// VoxelShape collision uses the element's own box.
	ProjectionRigid Projection = iota
	// ProjectionTerrainMatching: terrain-draped placement (v3 refinement). Parsed +
	// recorded; the Placer's rigid height math is used for it here (villages still place
	// vanilla-correctly because the start projects to the surface heightmap).
	ProjectionTerrainMatching
)

// parseProjection maps the JSON projection string to the enum. An absent/unknown value
// defaults to RIGID (the codec default for pool elements).
func parseProjection(s string) Projection {
	switch s {
	case "terrain_matching", "minecraft:terrain_matching":
		return ProjectionTerrainMatching
	default:
		return ProjectionRigid
	}
}

// PoolElement is the StructurePoolElement abstraction (CFR StructurePoolElement): a placed
// template's geometry source. Single/Legacy delegate to the 16-01 StructureTemplate; List
// composes; Feature delegates through FeaturePoolElementPlacer; Empty terminates. The Placer
// (jigsaw_placement.go) consumes
// BoundingBox / Jigsaws / Place + Projection.
type PoolElement interface {
	// BoundingBox returns the element's WORLD bbox at (origin, rot) (pivot ZERO — the jar's
	// getBoundingBox(StructureTemplateManager, BlockPos, Rotation) uses the default pivot).
	BoundingBox(origin Pos, rot Rotation) BoundingBox
	// Jigsaws returns the element's jigsaw connectors at (origin, rot), SHUFFLED by rng then
	// stably sorted by selection priority (CFR getShuffledJigsawBlocks). Empty for terminators.
	Jigsaws(origin Pos, rot Rotation, rng levelgen.RandomSource) ([]JigsawBlockInfo, error)
	// Place writes the element's blocks into the writable box (CFR StructurePoolElement.place,
	// here via StructureTemplate.PlaceInWorld with the element's processors). A no-op for Empty.
	Place(view WorldGenView, origin Pos, rot Rotation, box BoundingBox, rng levelgen.RandomSource)
	// Projection is RIGID/TERRAIN_MATCHING (the Placer's height-band branch).
	Projection() Projection
	// IsEmpty reports the EmptyPoolElement terminator (the Placer breaks on it).
	IsEmpty() bool
}

// FeaturePoolElementPlacer is the narrow cross-package seam for FeaturePoolElement.place.
// The bytecode delegates directly to PlacedFeature.place(level, generator, rng, origin);
// package structure cannot import package world without a cycle, so the live WorldGenView
// may provide this adapter. Test-only views that omit it deliberately make feature elements
// inert and consume no rng draws.
type FeaturePoolElementPlacer interface {
	PlaceFeaturePoolElement(feature string, origin Pos, rng levelgen.RandomSource) bool
}

// singlePoolElement ports SinglePoolElement / LegacySinglePoolElement: a single .nbt
// template + a processor chain + a projection. The 16-01 StructureTemplate.PlaceInWorld
// already implements the LegacySingle "jigsaw block -> air" semantics, so Single + Legacy
// share this Go type (the distinction is purely the jigsaw-block place behavior, which the
// template layer handles uniformly).
type singlePoolElement struct {
	location   string
	template   *StructureTemplate
	processors []TemplateProcessor
	projection Projection
}

func (e *singlePoolElement) Projection() Projection { return e.projection }
func (e *singlePoolElement) IsEmpty() bool          { return false }

func (e *singlePoolElement) BoundingBox(origin Pos, rot Rotation) BoundingBox {
	return e.template.BoundingBoxAt(origin, rot, MirrorNone, 0, 0)
}

// Jigsaws ports SinglePoolElement.getShuffledJigsawBlocks: getJigsaws(origin, rot) then
// Util.shuffle(list, rng) then sortBySelectionPriority (a STABLE highest-first sort).
func (e *singlePoolElement) Jigsaws(origin Pos, rot Rotation, rng levelgen.RandomSource) ([]JigsawBlockInfo, error) {
	js, err := e.template.Jigsaws(origin, rot, MirrorNone, 0, 0)
	if err != nil {
		return nil, err
	}
	shuffleJigsaws(js, rng)
	sortBySelectionPriority(js)
	return js, nil
}

func (e *singlePoolElement) Place(view WorldGenView, origin Pos, rot Rotation, box BoundingBox, rng levelgen.RandomSource) {
	e.template.PlaceInWorld(view, origin, rot, MirrorNone, 0, 0, e.processors, box, rng)
	// STRUCT-POLISH-02 Task 3: place the template's stored INHABITANTS (villager/cat — the village
	// .nbt entityInfoList) as SpawnRequests through the view (the tick adds them; the silverfish
	// is NOT here — it is a stronghold spawner block). A template with no entities is a no-op.
	// CFR StructurePoolElement.place runs StructureTemplate.placeInWorld which places blocks THEN
	// entities; mirror NONE + pivot ZERO match the village pool placement (the SinglePoolElement
	// default StructurePlaceSettings).
	e.template.PlaceEntities(view, origin, rot, MirrorNone, 0, 0, box, rng)
}

// listPoolElement ports ListPoolElement: an ordered list of sub-elements placed together.
// Villages never use it; it is parsed for completeness and STUBBED to the FIRST sub-element's
// geometry/jigsaws (the jar places all in order — a v3 refinement if a village ever adds one).
type listPoolElement struct {
	elements   []PoolElement
	projection Projection
}

func (e *listPoolElement) Projection() Projection { return e.projection }
func (e *listPoolElement) IsEmpty() bool          { return len(e.elements) == 0 }

func (e *listPoolElement) BoundingBox(origin Pos, rot Rotation) BoundingBox {
	if len(e.elements) == 0 {
		return BoundingBox{}
	}
	bb := e.elements[0].BoundingBox(origin, rot)
	for _, sub := range e.elements[1:] {
		bb = bb.Encapsulate(sub.BoundingBox(origin, rot))
	}
	return bb
}

func (e *listPoolElement) Jigsaws(origin Pos, rot Rotation, rng levelgen.RandomSource) ([]JigsawBlockInfo, error) {
	var out []JigsawBlockInfo
	for _, sub := range e.elements {
		js, err := sub.Jigsaws(origin, rot, rng)
		if err != nil {
			return nil, err
		}
		out = append(out, js...)
	}
	return out, nil
}

func (e *listPoolElement) Place(view WorldGenView, origin Pos, rot Rotation, box BoundingBox, rng levelgen.RandomSource) {
	for _, sub := range e.elements {
		sub.Place(view, origin, rot, box, rng)
	}
}

// featurePoolElement ports FeaturePoolElement: a zero-size placed_feature pool element.
// Bytecode verified against net.minecraft.world.level.levelgen.structure.pools.
// FeaturePoolElement in the 26.2 jar:
//   - getSize returns Vec3i.ZERO;
//   - getBoundingBox builds [origin..origin+size], so this is the single origin cell;
//   - getShuffledJigsawBlocks returns List.of(one jigsaw at origin), with orientation
//     FrontAndTop.fromFrontAndTop(DOWN, SOUTH), default name minecraft:bottom, pool/target
//     minecraft:empty, final_state minecraft:air, joint rollable, and no shuffle/rng draw;
//   - place ignores rotation/box/secondary position and delegates to PlacedFeature.place.
type featurePoolElement struct {
	feature    string
	projection Projection
}

func (e *featurePoolElement) Projection() Projection { return e.projection }
func (e *featurePoolElement) IsEmpty() bool          { return false }
func (e *featurePoolElement) BoundingBox(origin Pos, _ Rotation) BoundingBox {
	return BoundingBox{MinX: origin.X, MinY: origin.Y, MinZ: origin.Z, MaxX: origin.X, MaxY: origin.Y, MaxZ: origin.Z}
}
func (e *featurePoolElement) Jigsaws(origin Pos, _ Rotation, _ levelgen.RandomSource) ([]JigsawBlockInfo, error) {
	return []JigsawBlockInfo{{
		WorldPos:    origin,
		LocalPos:    Pos{0, 0, 0},
		Name:        "minecraft:bottom",
		Pool:        "minecraft:empty",
		Target:      "minecraft:empty",
		FinalState:  "minecraft:air",
		Joint:       "rollable",
		FrontFacing: block.Down,
		TopFacing:   block.South,
	}}, nil
}
func (e *featurePoolElement) Place(view WorldGenView, origin Pos, _ Rotation, _ BoundingBox, rng levelgen.RandomSource) {
	if placer, ok := view.(FeaturePoolElementPlacer); ok {
		placer.PlaceFeaturePoolElement(e.feature, origin, rng)
	}
}

// emptyPoolElement ports EmptyPoolElement.INSTANCE: the terminator. Its bbox is empty, it
// has no jigsaws, and it never places. The Placer breaks its candidate loop on it.
type emptyPoolElement struct{}

// EmptyPoolElementInstance is the singleton terminator (CFR EmptyPoolElement.INSTANCE).
var EmptyPoolElementInstance PoolElement = emptyPoolElement{}

func (emptyPoolElement) Projection() Projection                { return ProjectionRigid }
func (emptyPoolElement) IsEmpty() bool                         { return true }
func (emptyPoolElement) BoundingBox(Pos, Rotation) BoundingBox { return BoundingBox{} }
func (emptyPoolElement) Jigsaws(Pos, Rotation, levelgen.RandomSource) ([]JigsawBlockInfo, error) {
	return nil, nil
}
func (emptyPoolElement) Place(WorldGenView, Pos, Rotation, BoundingBox, levelgen.RandomSource) {}

// StructureTemplatePool ports StructureTemplatePool: the weighted element list (already
// EXPANDED by weight, as the jar constructor does — each element appears `weight` times in
// `templates`) plus the fallback pool id. getShuffledTemplates draws over `templates`.
type StructureTemplatePool struct {
	id        string
	templates []PoolElement // EXPANDED by weight (CFR the constructor's per-weight add loop)
	fallback  string        // the fallback pool id ("minecraft:empty" terminates the chain)
}

// Size ports StructureTemplatePool.size(): the expanded `templates` length (0 for empty.json).
func (p *StructureTemplatePool) Size() int { return len(p.templates) }

// Fallback returns the fallback pool registry id.
func (p *StructureTemplatePool) Fallback() string { return p.fallback }

// getShuffledTemplates ports StructureTemplatePool.getShuffledTemplates = Util.shuffledCopy(
// templates, rng): a Fisher-Yates shuffle of a COPY of the weight-expanded list. The draw
// count is len(templates)-1 (Pitfall #6 — exact). An empty pool draws nothing.
func (p *StructureTemplatePool) getShuffledTemplates(rng levelgen.RandomSource) []PoolElement {
	out := make([]PoolElement, len(p.templates))
	copy(out, p.templates)
	shuffleElements(out, rng)
	return out
}

// shuffleElements ports Util.shuffle(List, RandomSource) (Fisher-Yates, from the TOP):
// for i = size; i > 1; i-- { j = rng.nextInt(i); swap(list[i-1], list[j]) }. The draw count
// is size-1; matching it EXACTLY keeps the piece-selection RNG in lockstep with vanilla.
func shuffleElements(list []PoolElement, rng levelgen.RandomSource) {
	for i := len(list); i > 1; i-- {
		j := int(rng.NextIntN(int32(i)))
		list[i-1], list[j] = list[j], list[i-1]
	}
}

// shuffleJigsaws ports Util.shuffle over a jigsaw-block list (the same Fisher-Yates).
func shuffleJigsaws(list []JigsawBlockInfo, rng levelgen.RandomSource) {
	for i := len(list); i > 1; i-- {
		j := int(rng.NextIntN(int32(i)))
		list[i-1], list[j] = list[j], list[i-1]
	}
}

// sortBySelectionPriority ports SinglePoolElement.sortBySelectionPriority: a STABLE sort,
// HIGHEST selection priority first (CFR HIGHEST_SELECTION_PRIORITY_FIRST comparator). All
// village jigsaws are priority 0, so this is a no-op stable sort there — but it is ported so
// a non-zero-priority pool keeps the jar's ordering. Stable insertion sort preserves the
// post-shuffle order among equal-priority blocks (Pitfall #6 — the shuffle is the ordering).
func sortBySelectionPriority(list []JigsawBlockInfo) {
	for i := 1; i < len(list); i++ {
		v := list[i]
		j := i - 1
		for j >= 0 && list[j].SelectionPriority < v.SelectionPriority {
			list[j+1] = list[j]
			j--
		}
		list[j+1] = v
	}
}

// --- template_pool JSON parsing ---

// poolJSON is the template_pool JSON schema (CFR StructureTemplatePool codec): a weighted
// element list + a fallback pool id.
type poolJSON struct {
	Elements []weightedElementJSON `json:"elements"`
	Fallback string                `json:"fallback"`
}

// weightedElementJSON is one {element, weight} entry.
type weightedElementJSON struct {
	Element json.RawMessage `json:"element"`
	Weight  int             `json:"weight"`
}

// elementHeadJSON is the polymorphic element body. `processors` is EITHER a string (a
// processor_list ref) OR an inline {"processors":[...]} object — RawMessage captures both.
type elementHeadJSON struct {
	ElementType string          `json:"element_type"`
	Location    string          `json:"location"`
	Feature     string          `json:"feature"`
	Projection  string          `json:"projection"`
	Processors  json.RawMessage `json:"processors"`
	Elements    json.RawMessage `json:"elements"` // for list_pool_element (a nested element list)
}

// poolCache memoizes parsed pools by id (a pool's templates are immutable + pure over the
// embedded data, so the parse is a one-time cost shared across every village assembly).
var (
	poolCacheMu sync.Mutex
	poolCache   = map[string]*StructureTemplatePool{}
)

// LoadTemplatePool loads + parses an embedded template_pool by id (e.g.
// "minecraft:village/plains/town_centers" or the terminator "minecraft:empty"). The result
// is cached. Resolves each Single/Legacy element's .nbt + processors via the 16-01 layer.
//
// Source: CFR StructureTemplatePool codec + SinglePoolElement.template / processors resolve.
func LoadTemplatePool(id string) (*StructureTemplatePool, error) {
	key := resolvePoolID(id)
	poolCacheMu.Lock()
	if p, ok := poolCache[key]; ok {
		poolCacheMu.Unlock()
		return p, nil
	}
	poolCacheMu.Unlock()

	p, err := parseTemplatePool(key)
	if err != nil {
		return nil, err
	}
	poolCacheMu.Lock()
	poolCache[key] = p
	poolCacheMu.Unlock()
	return p, nil
}

// resolvePoolID strips the minecraft: namespace from a pool id (the embed accessor keys are
// bare). data.TemplatePoolJSON also strips, but the cache key must be canonical.
func resolvePoolID(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' {
			return id[i+1:]
		}
	}
	return id
}

// parseTemplatePool reads + parses the pool JSON, building the weight-expanded element list.
func parseTemplatePool(id string) (*StructureTemplatePool, error) {
	raw, err := data.TemplatePoolJSON(id)
	if err != nil {
		return nil, err
	}
	var pj poolJSON
	if err := json.Unmarshal(raw, &pj); err != nil {
		return nil, fmt.Errorf("structure: template_pool %q json: %w", id, err)
	}
	p := &StructureTemplatePool{id: id, fallback: pj.Fallback}
	for i, we := range pj.Elements {
		el, err := parsePoolElement(we.Element)
		if err != nil {
			return nil, fmt.Errorf("structure: template_pool %q element[%d]: %w", id, i, err)
		}
		// Weight-expand: the jar adds the element `weight` times to `templates` (Pitfall #6 —
		// the shuffle draws over the expanded list, so the weight IS the count).
		for w := 0; w < we.Weight; w++ {
			p.templates = append(p.templates, el)
		}
	}
	return p, nil
}

// parsePoolElement dispatches on element_type to build a PoolElement, resolving the .nbt +
// processors at parse time (cached templates). FAIL LOUD on an unknown element_type so a new
// village element reference is caught, not silently dropped.
func parsePoolElement(raw json.RawMessage) (PoolElement, error) {
	var h elementHeadJSON
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("element head: %w", err)
	}
	proj := parseProjection(h.Projection)
	switch h.ElementType {
	case "minecraft:single_pool_element", "minecraft:legacy_single_pool_element",
		"single_pool_element", "legacy_single_pool_element":
		tmpl, err := LoadTemplate(h.Location)
		if err != nil {
			return nil, fmt.Errorf("single element %q template: %w", h.Location, err)
		}
		procs, err := parseElementProcessors(h.Processors)
		if err != nil {
			return nil, fmt.Errorf("single element %q processors: %w", h.Location, err)
		}
		return &singlePoolElement{location: h.Location, template: tmpl, processors: procs, projection: proj}, nil
	case "minecraft:list_pool_element", "list_pool_element":
		var subs []json.RawMessage
		if err := json.Unmarshal(h.Elements, &subs); err != nil {
			return nil, fmt.Errorf("list element sub-list: %w", err)
		}
		le := &listPoolElement{projection: proj}
		for j, sr := range subs {
			sub, err := parsePoolElement(sr)
			if err != nil {
				return nil, fmt.Errorf("list element sub[%d]: %w", j, err)
			}
			le.elements = append(le.elements, sub)
		}
		return le, nil
	case "minecraft:feature_pool_element", "feature_pool_element":
		return &featurePoolElement{feature: h.Feature, projection: proj}, nil
	case "minecraft:empty_pool_element", "empty_pool_element":
		return EmptyPoolElementInstance, nil
	default:
		return nil, fmt.Errorf("unsupported pool element_type %q", h.ElementType)
	}
}

// parseElementProcessors resolves an element's `processors` field, which is EITHER a string
// (a processor_list registry ref) OR an inline {"processors":[...]} object. An empty/absent
// field yields no processors. Reuses the 16-01 LoadProcessorList / ParseProcessorList.
func parseElementProcessors(raw json.RawMessage) ([]TemplateProcessor, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Try the string form first (a processor_list ref).
	var ref string
	if err := json.Unmarshal(raw, &ref); err == nil {
		if ref == "" {
			return nil, nil
		}
		return LoadProcessorList(ref)
	}
	// Otherwise it is an inline {"processors":[...]} object.
	return ParseProcessorList(raw)
}
