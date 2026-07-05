package world

// feature_block_column.go ports BlockColumnFeature.place + BlockColumnFeature.truncate
// (net.minecraft.world.level.levelgen.feature.BlockColumnFeature, CFR + javap -c against
// temp/cache/26.2-inner.jar). It is the generic vertical-column placer the data uses for
// cave vines (cave_vine / cave_vine_in_moss, direction=down) and the big-dripleaf stems
// (dripleaf's block_column entries, direction=up). It was previously unregistered — a
// silent no-op that never generated.
//
// BlockColumnConfiguration (record fields):
//   - layers: List<Layer(IntProvider height, BlockStateProvider provider)>
//   - direction: Direction (down/up/...)
//   - allowed_placement: BlockPredicate (the scan gate)
//   - prioritize_tip: boolean (which end the truncate() shortens)
//
// Draw order (the determinism contract):
//   1. for each layer, in order: layerHeights[i] = layer.height().sample(random)  (IntProvider draws)
//   2. if totalHeight == 0: return false
//   3. scan from origin+direction for `totalHeight` cells; on the FIRST cell where
//      allowed_placement fails, truncate(layerHeights, totalHeight, y, prioritizeTip) and
//      stop scanning. (0 rng draws — allowed_placement is positional.)
//   4. place: for each layer (in order), for each of its (possibly-truncated) `count` cells,
//      setBlock(placePos, layer.provider.getState(level, random, placePos)); placePos.move(direction).
//      (Provider draws per cell — the tip layer's randomized_int_state_provider draws.)
//   5. return true (unconditionally, once totalHeight > 0).
//
// The `height` IntProvider is parsed by a self-contained sampler (vegIntProvider) that
// supports the exact kinds the block_column data carries: bare int (constant, 0 draws),
// {type:constant}, {type:uniform} (one draw), and {type:weighted_list} of inner providers
// (one draw to pick + the inner provider's draws). This mirrors the feature package's
// (unexported) IntProvider — same algorithm, kept in-package to stay file-disjoint. The
// `provider` BlockStateProvider is parsed by 12-01's feature.ParseProvider (which already
// handles simple/weighted/randomized_int the block_column data uses). The allowed_placement
// predicate is parsed by placement.ParsePredicate (matching_block_tag / matching_blocks /
// any_of — the kinds the cave_vine/dripleaf configs carry) and tested through the
// PlacementContext the body receives.

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("block_column", blockColumnBody)
}

// ---- config decode (memoized per ConfiguredFeature) ----

// blockColumnLayer is one parsed BlockColumnConfiguration.Layer: the height IntProvider +
// the provider. A layer whose provider failed to parse (a deferred/unported provider kind)
// carries providerErr=true and is skipped at place time (its cells consume no provider draw
// — matching the misc-body skip convention), but its height still contributes to the scan.
type blockColumnLayer struct {
	height      *vegIntProvider
	provider    feature.BlockStateProvider
	providerErr bool
}

// blockColumnDecoded is the memoized decode: the parsed layers, the direction step, the
// allowed_placement predicate, and prioritizeTip. A deferred config/height/predicate error
// is surfaced by the body (panic where the uncached decode would have failed).
type blockColumnDecoded struct {
	layers        []blockColumnLayer
	dir           dirEntry
	allowed       placement.BlockPredicate
	prioritizeTip bool
	err           error
}

var blockColumnCache sync.Map // map[*feature.ConfiguredFeature]*blockColumnDecoded

// jsonBlockColumnConfig is the BlockColumnConfiguration JSON envelope.
type jsonBlockColumnConfig struct {
	Layers []struct {
		Height   json.RawMessage `json:"height"`
		Provider json.RawMessage `json:"provider"`
	} `json:"layers"`
	Direction        string          `json:"direction"`
	AllowedPlacement json.RawMessage `json:"allowed_placement"`
	PrioritizeTip    bool            `json:"prioritize_tip"`
}

func decodeBlockColumn(cf *feature.ConfiguredFeature) *blockColumnDecoded {
	if v, ok := blockColumnCache.Load(cf); ok {
		return v.(*blockColumnDecoded)
	}
	d := &blockColumnDecoded{}
	var j jsonBlockColumnConfig
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &j); err != nil {
			d.err = fmt.Errorf("world: block_column config %q: %w", cf.ID, err)
			blockColumnCache.Store(cf, d)
			return d
		}
	}
	dir, ok := directionByName(j.Direction)
	if !ok {
		d.err = fmt.Errorf("world: block_column %q: bad direction %q", cf.ID, j.Direction)
		blockColumnCache.Store(cf, d)
		return d
	}
	d.dir = dir
	d.prioritizeTip = j.PrioritizeTip
	for i, layer := range j.Layers {
		h, err := parseVegIntProvider(layer.Height)
		if err != nil {
			d.err = fmt.Errorf("world: block_column %q layer %d height: %w", cf.ID, i, err)
			blockColumnCache.Store(cf, d)
			return d
		}
		bl := blockColumnLayer{height: h}
		prov, err := feature.ParseProvider(layer.Provider)
		if err != nil {
			// A deferred/unported provider kind: record and skip its cells at place time
			// (no provider draw for that layer), matching the misc-body no-op convention.
			bl.providerErr = true
		} else {
			bl.provider = prov
		}
		d.layers = append(d.layers, bl)
	}
	pred, err := placement.ParsePredicate(j.AllowedPlacement)
	if err != nil {
		d.err = fmt.Errorf("world: block_column %q allowed_placement: %w", cf.ID, err)
		blockColumnCache.Store(cf, d)
		return d
	}
	d.allowed = pred
	blockColumnCache.Store(cf, d)
	return d
}

// blockColumnBody ports BlockColumnFeature.place (CFR, 26.2-inner.jar). See the file header
// for the draw order. Writes through bctx.placeState; reads the allowed_placement predicate
// through the PlacementContext ctx (Neighborhood-backed, same terrain the body writes to).
func blockColumnBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	d := decodeBlockColumn(cf)
	if d.err != nil {
		panic(d.err.Error())
	}
	layerCount := len(d.layers)
	if layerCount == 0 {
		return false
	}

	// 1. Sample each layer's height (in layer order) — the IntProvider draws.
	layerHeights := make([]int, layerCount)
	totalHeight := 0
	for i := 0; i < layerCount; i++ {
		layerHeights[i] = d.layers[i].height.sample(rng)
		totalHeight += layerHeights[i]
	}
	if totalHeight == 0 {
		return false
	}

	dir := d.dir
	// 2. Scan `totalHeight` cells from origin+direction; on the first disallowed cell,
	//    truncate(layerHeights, totalHeight, y, prioritizeTip) and stop. 0 rng draws.
	nx := pos.X + dir.dx
	ny := pos.Y + dir.dy
	nz := pos.Z + dir.dz
	for y := 0; y < totalHeight; y++ {
		if !d.allowed.Test(ctx, nx, ny, nz) {
			truncateLayers(layerHeights, totalHeight, y, d.prioritizeTip)
			break
		}
		nx += dir.dx
		ny += dir.dy
		nz += dir.dz
	}

	// 4. Place each layer's (truncated) cells; each cell draws the layer provider's state.
	px, py, pz := pos.X, pos.Y, pos.Z
	for i := 0; i < layerCount; i++ {
		count := layerHeights[i]
		if count == 0 {
			continue
		}
		layer := d.layers[i]
		for y := 0; y < count; y++ {
			if !layer.providerErr {
				st := layer.provider.GetState(rng, px, py, pz)
				bctx.placeState(placement.BlockPos{X: px, Y: py, Z: pz}, st)
			}
			px += dir.dx
			py += dir.dy
			pz += dir.dz
		}
	}
	return true
}

// truncateLayers ports BlockColumnFeature.truncate. It removes (totalHeight-newHeight) cells
// from the layer heights, starting at the tip (index 0, forward) when prioritizeTip, else at
// the base (last index, backward). Each visited layer gives up min(itsHeight, remaining).
func truncateLayers(layerHeights []int, totalHeight, newHeight int, prioritizeTip bool) {
	amountToRemove := totalHeight - newHeight
	step := 1
	start := 0
	end := len(layerHeights)
	if !prioritizeTip {
		step = -1
		start = len(layerHeights) - 1
		end = -1
	}
	for i := start; i != end && amountToRemove > 0; i += step {
		thisLayer := layerHeights[i]
		toRemove := thisLayer
		if amountToRemove < toRemove {
			toRemove = amountToRemove
		}
		layerHeights[i] -= toRemove
		amountToRemove -= toRemove
	}
}

// directionByName maps a BlockColumnConfiguration `direction` string (down/up/north/...) to
// its dirEntry (the block.Direction + normal step). It reuses the Direction ordinal names.
func directionByName(name string) (dirEntry, bool) {
	switch name {
	case "down":
		return directionValues[0], true
	case "up":
		return directionValues[1], true
	case "north":
		return directionValues[2], true
	case "south":
		return directionValues[3], true
	case "west":
		return directionValues[4], true
	case "east":
		return directionValues[5], true
	}
	return dirEntry{}, false
}

// ---- vegIntProvider: the IntProvider kinds the block_column + sea_pickle data use ----
//
// A self-contained port of net.minecraft.util.valueproviders.IntProvider for the kinds the
// vegetation configs carry: ConstantInt (bare int / {type:constant}), UniformInt
// ({type:uniform}), and WeightedListInt ({type:weighted_list}). Same algorithm as the
// feature package's (unexported) intProvider, kept in-package so these bodies stay
// file-disjoint. The sample draw count per kind IS a determinism contract:
//   - constant: 0 draws
//   - uniform:  1 draw (min + nextInt(max-min+1))  [UniformInt.sample]
//   - weighted_list: 1 draw to pick (nextInt(totalWeight) + cumulative walk in ENTRY ORDER)
//     + the chosen inner provider's draws  [WeightedList.getRandom]

type vegIntProviderKind uint8

const (
	vegIntConstant vegIntProviderKind = iota
	vegIntUniform
	vegIntWeightedList
	vegIntBiasedToBottom
)

type vegIntWeightedEntry struct {
	provider *vegIntProvider
	weight   int
}

type vegIntProvider struct {
	kind           vegIntProviderKind
	value          int // constant
	minVal, maxVal int // uniform
	entries        []vegIntWeightedEntry
	totalWeight    int
}

// sample ports IntProvider.sample for the three supported kinds, JAR-exact draw order.
func (p *vegIntProvider) sample(rng levelgen.RandomSource) int {
	switch p.kind {
	case vegIntConstant:
		return p.value
	case vegIntUniform:
		return p.minVal + int(rng.NextIntN(int32(p.maxVal-p.minVal+1)))
	case vegIntWeightedList:
		// WeightedList.getRandom: i = nextInt(totalWeight); subtract each weight in entry
		// order until negative -> the chosen inner provider; then sample it.
		i := int(rng.NextIntN(int32(p.totalWeight)))
		for _, e := range p.entries {
			i -= e.weight
			if i < 0 {
				return e.provider.sample(rng)
			}
		}
		return p.entries[len(p.entries)-1].provider.sample(rng)
	case vegIntBiasedToBottom:
		// BiasedToBottomInt.sample: min + nextInt(nextInt(max-min+1) + 1). TWO nested draws
		// (the inner nextInt bounds the outer), biasing the result toward min. CITE:
		// net.minecraft.util.valueproviders.BiasedToBottomInt.sample.
		inner := int(rng.NextIntN(int32(p.maxVal-p.minVal+1))) + 1
		return p.minVal + int(rng.NextIntN(int32(inner)))
	}
	return 0
}

// parseVegIntProvider decodes a bare int OR {type:constant|uniform|weighted_list} IntProvider.
// A nil/empty raw is treated as constant 0 (the absent-optional default). An unsupported kind
// errors loudly so a data change surfaces rather than drifting the draw count.
func parseVegIntProvider(raw json.RawMessage) (*vegIntProvider, error) {
	if len(raw) == 0 {
		return &vegIntProvider{kind: vegIntConstant, value: 0}, nil
	}
	// Bare int (ConstantInt's inline form).
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return &vegIntProvider{kind: vegIntConstant, value: n}, nil
	}
	var obj struct {
		Type         string `json:"type"`
		Value        *int   `json:"value"`
		MinInclusive *int   `json:"min_inclusive"`
		MaxInclusive *int   `json:"max_inclusive"`
		Distribution []struct {
			Data   json.RawMessage `json:"data"`
			Weight int             `json:"weight"`
		} `json:"distribution"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("not a bare int or int provider: %w", err)
	}
	switch stripNSVeg(obj.Type) {
	case "constant":
		v := 0
		if obj.Value != nil {
			v = *obj.Value
		}
		return &vegIntProvider{kind: vegIntConstant, value: v}, nil
	case "uniform":
		if obj.MinInclusive == nil || obj.MaxInclusive == nil {
			return nil, fmt.Errorf("uniform int provider missing min/max_inclusive")
		}
		if *obj.MaxInclusive < *obj.MinInclusive {
			return nil, fmt.Errorf("uniform int: max %d < min %d", *obj.MaxInclusive, *obj.MinInclusive)
		}
		return &vegIntProvider{kind: vegIntUniform, minVal: *obj.MinInclusive, maxVal: *obj.MaxInclusive}, nil
	case "biased_to_bottom":
		// BiasedToBottomInt: min_inclusive/max_inclusive; sample biases toward min via a
		// nested nextInt (see sample). CITE: net.minecraft.util.valueproviders.BiasedToBottomInt.
		if obj.MinInclusive == nil || obj.MaxInclusive == nil {
			return nil, fmt.Errorf("biased_to_bottom int provider missing min/max_inclusive")
		}
		if *obj.MaxInclusive < *obj.MinInclusive {
			return nil, fmt.Errorf("biased_to_bottom int: max %d < min %d", *obj.MaxInclusive, *obj.MinInclusive)
		}
		return &vegIntProvider{kind: vegIntBiasedToBottom, minVal: *obj.MinInclusive, maxVal: *obj.MaxInclusive}, nil
	case "weighted_list":
		if len(obj.Distribution) == 0 {
			return nil, fmt.Errorf("weighted_list int provider has no distribution")
		}
		wp := &vegIntProvider{kind: vegIntWeightedList}
		for _, e := range obj.Distribution {
			inner, err := parseVegIntProvider(e.Data)
			if err != nil {
				return nil, fmt.Errorf("weighted_list int provider entry: %w", err)
			}
			if e.Weight <= 0 {
				return nil, fmt.Errorf("weighted_list int provider entry has non-positive weight %d", e.Weight)
			}
			wp.entries = append(wp.entries, vegIntWeightedEntry{provider: inner, weight: e.Weight})
			wp.totalWeight += e.Weight
		}
		return wp, nil
	default:
		return nil, fmt.Errorf("world: unported IntProvider type %q (block_column/sea_pickle use constant/uniform/weighted_list)", obj.Type)
	}
}

// stripNSVeg strips a "minecraft:" namespace (the in-package copy, mirroring stripNSMisc).
func stripNSVeg(t string) string {
	for i := 0; i < len(t); i++ {
		if t[i] == ':' {
			return t[i+1:]
		}
	}
	return t
}
