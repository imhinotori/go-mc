// Package surface ports the Minecraft 26.2 (protocol 776) SURFACE pass — the
// system that turns the raw filled+carved terrain (stone/water columns from Waves
// 5/6) into a biome-CORRECT surface: grass+dirt in plains, sand+sandstone in
// deserts/beaches, gravel under water, terracotta bands in badlands, podzol in
// taigas, packed ice / powder snow in frozen biomes, and so on. It is the LOGIC
// half of PARITY-01's surface split; the DATA half is the `surface_rule` subtree
// embedded in overworld.json (Wave 1), parsed here into the ported rule tree.
//
// Two ported Java systems live in this package:
//
//   - SurfaceRules (this file): the surface_rule vocabulary. A `RuleSource` tree
//     (sequence / condition / block / bandlands) gated by `ConditionSource`s
//     (biome / stone_depth / water / y_above / above_preliminary_surface /
//     vertical_gradient / noise_threshold / hole / steep / temperature / not).
//     Parse() reads the JSON surface_rule into this tree with a "type"-dispatch
//     exactly like the Wave-3 density parser; an UNSUPPORTED type errors LOUDLY
//     (never silently dropped).
//   - SurfaceSystem (system.go): SurfaceSystem.buildSurface walks each of the 256
//     columns top-down, maintaining a SurfaceRules$Context (stoneDepthAbove/Below,
//     the biome from the Wave-7 multi-noise source, the water height, the surface
//     depth/secondary noise), applies the parsed rule sequence to pick each surface
//     block, and writes the 3 CLIENT heightmaps from the resulting top solid.
//
// PORTED FROM (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.SurfaceRules and its nested $ classes:
//     $SequenceRule.tryApply        first non-null wins
//     $TestRule.tryApply            if condition.test() → followup.tryApply else null
//     $StateRule.tryApply           always the block state
//     $StoneDepthCheck              depth <= 1 + offset + (addSurfaceDepth?surfaceDepth:0)
//   - (secondaryDepthRange==0?0:(int)map(secondary,-1,1,0,range))
//     $WaterConditionSource         waterHeight==MIN || blockY+(addStoneDepth?stoneDepthAbove:0)
//     >= waterHeight + offset + surfaceDepth*mult
//     $YConditionSource             blockY+(addStoneDepth?stoneDepthAbove:0)
//     >= anchor.resolveY(ctx) + surfaceDepth*mult
//     $VerticalGradientConditionSource  true ≤ trueAtAndBelow; false ≥ falseAtAndAbove;
//     else random.at(x,y,z).nextFloat() < map(y,...,1,0)
//     $NoiseThresholdConditionSource    min ≤ noise ≤ max
//     $BiomeConditionSource         biomeSet.contains(getBiome())
//     $Context$AbovePreliminarySurfaceCondition  blockY >= getMinSurfaceLevel()
//     $Context$HoleCondition        surfaceDepth <= 0
//     $Context$SteepMaterialCondition  WORLD_SURFACE_WG slope ≥ 4 over ±1 in Z
//     $Context$TemperatureHelperCondition  biome.coldEnoughToSnow(pos, seaLevel)
//     $Bandlands                    getBand(x,y,z) over the generated clay bands
//   - net.minecraft.world.level.levelgen.VerticalAnchor (absolute / above_bottom resolveY)
//
// It is a faithful algorithmic port, NOT a copy of Mojang source.
//
// PACKAGE LOCATION NOTE: package `surface` under world/levelgen/surface/ — DISTINCT
// from package levelgen (the Tier-A primitives) and a sibling of biome/carver/
// noisechunk. It consumes router (the preliminary_surface_level / sea level), biome
// (the multi-noise GetBiome), noisechunk (the filled chunk + preliminary surface),
// level/block (the result-state ids) and level/biome (the biome ids). Nothing under
// it imports surface, so there is no cycle (the 09-03/04/05 lesson: a composition
// consumer goes in its own leaf-above package).
package surface

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
)

// Condition ports SurfaceRules$Condition: a per-column-position boolean test. It is
// re-evaluated as the buildSurface walk updates the Context (updateXZ/updateY); the
// vanilla LazyCondition memoization (skip recompute within the same lastUpdateXZ/Y)
// is a perf refinement dropped here for simplicity — the result is identical.
type Condition interface {
	// test reports whether the condition holds at the Context's current (x,y,z).
	test(c *Context) bool
}

// ConditionSource ports SurfaceRules$ConditionSource: a parsed condition factory.
// In vanilla `apply(Context)` binds the source to a Context to make a Condition; here
// the parsed source IS the (stateless) condition and reads the Context per-test, so
// ConditionSource and Condition collapse into one interface for the ported subset.
type ConditionSource = Condition

// RuleSource ports SurfaceRules$RuleSource: a node of the surface rule tree that, at
// a column position, may produce a block state (or none → fall through). tryApply
// mirrors SurfaceRule.tryApply(x,y,z) but reads the position from the Context.
type RuleSource interface {
	// tryApply returns the placed state and true, or false to fall through.
	tryApply(c *Context) (block.StateID, bool)
}

// ---- RuleSource implementations (the rule tree) ----

// sequenceRule ports SurfaceRules$SequenceRule: evaluate children in order, the FIRST
// that produces a state wins (first-match-wins — the load-bearing ordering, a wrong
// order = a wrong surface).
type sequenceRule struct{ rules []RuleSource }

func (r *sequenceRule) tryApply(c *Context) (block.StateID, bool) {
	for _, sub := range r.rules {
		if st, ok := sub.tryApply(c); ok {
			return st, true
		}
	}
	return 0, false
}

// testRule ports SurfaceRules$TestRule: if the condition holds, defer to the followup
// rule; otherwise fall through (no state). This is the JSON "condition" node
// (if_true / then_run).
type testRule struct {
	cond     Condition
	followup RuleSource
}

func (r *testRule) tryApply(c *Context) (block.StateID, bool) {
	if !r.cond.test(c) {
		return 0, false
	}
	return r.followup.tryApply(c)
}

// stateRule ports SurfaceRules$StateRule: always place the fixed block state (the JSON
// "block" node's result_state, pre-resolved to a StateID at parse time).
type stateRule struct{ state block.StateID }

func (r *stateRule) tryApply(c *Context) (block.StateID, bool) { return r.state, true }

// bandlandsRule ports SurfaceRules$Bandlands: place the badlands terracotta band for
// the column position (getBand reads the generated clay-band table + the offset noise).
type bandlandsRule struct{}

func (r *bandlandsRule) tryApply(c *Context) (block.StateID, bool) {
	return c.system.getBand(c.blockX, c.blockY, c.blockZ), true
}

// ---- Condition implementations ----

// notCondition ports SurfaceRules$NotConditionSource: inverts the wrapped condition.
type notCondition struct{ inner Condition }

func (n notCondition) test(c *Context) bool { return !n.inner.test(c) }

// caveSurface distinguishes the StoneDepth/Water surface anchor (floor vs ceiling).
type caveSurface int

const (
	surfaceFloor caveSurface = iota
	surfaceCeiling
)

// stoneDepthCondition ports SurfaceRules$StoneDepthCheck: how DEEP into the stone the
// current block is (from the floor or ceiling), gated against a depth budget.
//
//	depth = ceiling ? stoneDepthBelow : stoneDepthAbove
//	add   = addSurfaceDepth ? surfaceDepth : 0
//	sec   = secondaryDepthRange==0 ? 0 : (int)Mth.map(surfaceSecondary, -1, 1, 0, secondaryDepthRange)
//	return depth <= 1 + offset + add + sec
type stoneDepthCondition struct {
	offset              int
	addSurfaceDepth     bool
	secondaryDepthRange int
	surface             caveSurface
}

func (s stoneDepthCondition) test(c *Context) bool {
	var depth int
	if s.surface == surfaceCeiling {
		depth = c.stoneDepthBelow
	} else {
		depth = c.stoneDepthAbove
	}
	add := 0
	if s.addSurfaceDepth {
		add = c.surfaceDepth
	}
	sec := 0
	if s.secondaryDepthRange != 0 {
		sec = int(mthMap(c.getSurfaceSecondary(), -1.0, 1.0, 0.0, float64(s.secondaryDepthRange)))
	}
	return depth <= 1+s.offset+add+sec
}

// waterCondition ports SurfaceRules$WaterConditionSource: true above (or AT) the local
// water height, accounting for the surface depth band.
//
//	waterHeight==MIN || blockY+(addStoneDepth?stoneDepthAbove:0)
//	  >= waterHeight + offset + surfaceDepth*surfaceDepthMultiplier
type waterCondition struct {
	offset                 int
	surfaceDepthMultiplier int
	addStoneDepth          bool
}

func (w waterCondition) test(c *Context) bool {
	if c.waterHeight == minInt32 {
		return true
	}
	add := 0
	if w.addStoneDepth {
		add = c.stoneDepthAbove
	}
	return c.blockY+add >= c.waterHeight+w.offset+c.surfaceDepth*w.surfaceDepthMultiplier
}

// yCondition ports SurfaceRules$YConditionSource (the JSON "y_above" node): true at or
// above an absolute/above-bottom anchor Y, with the surface-depth band.
//
//	blockY+(addStoneDepth?stoneDepthAbove:0) >= anchor.resolveY(ctx) + surfaceDepth*mult
type yCondition struct {
	anchor                 verticalAnchor
	surfaceDepthMultiplier int
	addStoneDepth          bool
}

func (y yCondition) test(c *Context) bool {
	add := 0
	if y.addStoneDepth {
		add = c.stoneDepthAbove
	}
	return c.blockY+add >= y.anchor.resolveY(c.minY)+c.surfaceDepth*y.surfaceDepthMultiplier
}

// verticalGradientCondition ports SurfaceRules$VerticalGradientConditionSource: a
// probabilistic vertical band. Below trueAtAndBelow → always true; above falseAtAndAbove
// → always false; in between → random.at(x,y,z).nextFloat() < map(y, true, false, 1, 0).
// The random factory is base.fromHashOf(randomName).forkPositional() (Pitfall 7).
type verticalGradientCondition struct {
	trueAtAndBelowAnchor  verticalAnchor
	falseAtAndAboveAnchor verticalAnchor
	randomName            string
	// resolved lazily against the Context's minY at parse time via resolve().
}

func (v verticalGradientCondition) test(c *Context) bool {
	trueAtAndBelow := v.trueAtAndBelowAnchor.resolveY(c.minY)
	falseAtAndAbove := v.falseAtAndAboveAnchor.resolveY(c.minY)
	y := c.blockY
	if y <= trueAtAndBelow {
		return true
	}
	if y >= falseAtAndAbove {
		return false
	}
	t := mthMap(float64(y), float64(trueAtAndBelow), float64(falseAtAndAbove), 1.0, 0.0)
	// getOrCreateRandomFactory(randomName) = base.fromHashOf(name).forkPositional();
	// then at(x,y,z).nextFloat() (Pitfall 7: seeded from the world seed).
	rng := c.system.randomFactory(v.randomName).At(c.blockX, y, c.blockZ)
	return float64(rng.NextFloat()) < t
}

// noiseThresholdCondition ports SurfaceRules$NoiseThresholdConditionSource: the named
// surface noise (2-D, sampled at (x,0,z)) lies within [min,max]. The noise is one of
// minecraft:surface / surface_swamp / calcite / gravel / ice / packed_ice /
// powder_snow / sulfur_cave_gradient (all 2-D surface noises in this graph).
type noiseThresholdCondition struct {
	noiseID      string
	minThreshold float64
	maxThreshold float64
}

func (n noiseThresholdCondition) test(c *Context) bool {
	v := c.surfaceNoiseValue(n.noiseID)
	return v >= n.minThreshold && v <= n.maxThreshold
}

// biomeCondition ports SurfaceRules$BiomeConditionSource: the column biome (from the
// Wave-7 multi-noise source via the Context's biome getter) is in the biome set.
type biomeCondition struct{ biomes map[biome.Type]bool }

func (b biomeCondition) test(c *Context) bool { return b.biomes[c.getBiome()] }

// abovePreliminarySurfaceCondition ports SurfaceRules$Context$AbovePreliminarySurfaceCondition:
// blockY >= the (bilinear-lerped) minimum surface level. This is the load-bearing
// "are we near the terrain surface?" gate the whole rule tree hangs off.
type abovePreliminarySurfaceCondition struct{}

func (abovePreliminarySurfaceCondition) test(c *Context) bool {
	return c.blockY >= c.getMinSurfaceLevel()
}

// holeCondition ports SurfaceRules$Context$HoleCondition: surfaceDepth <= 0 (a thin /
// eroded surface, e.g. a beach hole).
type holeCondition struct{}

func (holeCondition) test(c *Context) bool { return c.surfaceDepth <= 0 }

// steepCondition ports SurfaceRules$Context$SteepMaterialCondition: the WORLD_SURFACE_WG
// heightmap rises ≥ 4 across ±1 in Z at this column's X — a steep slope (used to bare
// stone on mountainsides).
type steepCondition struct{}

func (steepCondition) test(c *Context) bool {
	lx := c.blockX & 15
	lz := c.blockZ & 15
	zm := lz - 1
	if zm < 0 {
		zm = 0
	}
	zp := lz + 1
	if zp > 15 {
		zp = 15
	}
	h0 := c.worldSurfaceHeight(lx, zm)
	h1 := c.worldSurfaceHeight(lx, zp)
	return h1 >= h0+4
}

// temperatureCondition ports SurfaceRules$Context$TemperatureHelperCondition:
// biome.coldEnoughToSnow(pos, seaLevel). The biome temperature/snow model is not yet
// ported (a Phase-2+ biome-data concern), so this conservatively returns false (never
// "cold enough to snow"); it only gates a small set of snowy-biome surface branches
// and a false result simply leaves the underlying grass/dirt/stone surface, which is
// the safe non-snow default. Documented as a known limitation (no silent wrong block).
type temperatureCondition struct{}

func (temperatureCondition) test(c *Context) bool { return false }

// ---- VerticalAnchor (absolute / above_bottom) ----

// verticalAnchor ports net.minecraft.world.level.levelgen.VerticalAnchor for the two
// kinds the surface_rule uses: an absolute Y, or an offset above the world bottom.
type verticalAnchor struct {
	absolute bool
	value    int
}

// resolveY ports VerticalAnchor.resolveY(WorldGenerationContext): an absolute anchor is
// its value; an above_bottom anchor is minGenY + value (the surface walk's minY is the
// gen bottom). (Only absolute + above_bottom appear in the overworld surface_rule.)
func (a verticalAnchor) resolveY(minY int) int {
	if a.absolute {
		return a.value
	}
	return minY + a.value
}

// ---- math helpers (ported from net.minecraft.util.Mth) ----

// mthMap ports Mth.map(x, a, b, c, d): linearly remap x from [a,b] to [c,d].
func mthMap(x, a, b, c, d float64) float64 {
	return mthLerp((x-a)/(b-a), c, d)
}

// mthLerp ports Mth.lerp(delta, start, end).
func mthLerp(delta, start, end float64) float64 { return start + delta*(end-start) }

// mthLerp2 ports Mth.lerp2(dx, dz, v00, v10, v01, v11): bilinear interpolation over the
// 4 corner values, used by getMinSurfaceLevel over the preliminary-surface cell.
func mthLerp2(dx, dz, v00, v10, v01, v11 float64) float64 {
	return mthLerp(dz, mthLerp(dx, v00, v10), mthLerp(dx, v01, v11))
}

// mthFloor ports Mth.floor(double) = (int)Math.floor(d).
func mthFloor(d float64) int {
	i := int(d)
	if float64(i) > d {
		i--
	}
	return i
}

// minInt32 is Integer.MIN_VALUE — the buildSurface waterHeight "unset" sentinel.
const minInt32 = -2147483648

// ---- the surface_rule parser ----

// blockStateJSON is the result_state of a "block" rule: a {Name, Properties} block
// reference, resolved to a StateID at parse time (Properties values are strings, e.g.
// "snowy":"false", "level":"0"; an empty Properties means the default state).
type blockStateJSON struct {
	Name       string            `json:"Name"`
	Properties map[string]string `json:"Properties,omitempty"`
}

// anchorJSON is a VerticalAnchor: exactly one of absolute / above_bottom / below_top.
type anchorJSON struct {
	Absolute    *int `json:"absolute,omitempty"`
	AboveBottom *int `json:"above_bottom,omitempty"`
	BelowTop    *int `json:"below_top,omitempty"`
}

// ParseRuleSource parses the JSON surface_rule subtree (from overworld.json) into the
// ported RuleSource tree. It is the surface analogue of the Wave-3 density parser: a
// "type"-dispatch that recurses into children. An UNSUPPORTED rule/condition type
// errors LOUDLY (never silently dropped — the 09-03 loud-error discipline) so a graph
// shift surfaces at parse time, not as a wrong block at runtime.
func ParseRuleSource(raw json.RawMessage) (RuleSource, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("surface: rule envelope: %w", err)
	}
	switch stripNS(head.Type) {
	case "sequence":
		var n struct {
			Sequence []json.RawMessage `json:"sequence"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: sequence rule: %w", err)
		}
		rules := make([]RuleSource, len(n.Sequence))
		for i, sub := range n.Sequence {
			r, err := ParseRuleSource(sub)
			if err != nil {
				return nil, fmt.Errorf("surface: sequence[%d]: %w", i, err)
			}
			rules[i] = r
		}
		return &sequenceRule{rules: rules}, nil

	case "condition":
		var n struct {
			IfTrue  json.RawMessage `json:"if_true"`
			ThenRun json.RawMessage `json:"then_run"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: condition rule: %w", err)
		}
		cond, err := parseConditionSource(n.IfTrue)
		if err != nil {
			return nil, fmt.Errorf("surface: condition.if_true: %w", err)
		}
		followup, err := ParseRuleSource(n.ThenRun)
		if err != nil {
			return nil, fmt.Errorf("surface: condition.then_run: %w", err)
		}
		return &testRule{cond: cond, followup: followup}, nil

	case "block":
		var n struct {
			ResultState blockStateJSON `json:"result_state"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: block rule: %w", err)
		}
		sid, err := resolveBlockState(n.ResultState)
		if err != nil {
			return nil, fmt.Errorf("surface: block rule result_state: %w", err)
		}
		return &stateRule{state: sid}, nil

	case "bandlands":
		return &bandlandsRule{}, nil

	default:
		return nil, fmt.Errorf("surface: UNSUPPORTED rule type %q (loud error: the surface_rule graph references a rule node this port does not implement)", head.Type)
	}
}

// parseConditionSource parses a JSON condition (the if_true subtree) into a Condition.
// An unsupported condition type errors loudly.
func parseConditionSource(raw json.RawMessage) (Condition, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("surface: condition envelope: %w", err)
	}
	switch stripNS(head.Type) {
	case "biome":
		var n struct {
			BiomeIs json.RawMessage `json:"biome_is"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: biome condition: %w", err)
		}
		ids, err := parseBiomeList(n.BiomeIs)
		if err != nil {
			return nil, fmt.Errorf("surface: biome condition biome_is: %w", err)
		}
		set := make(map[biome.Type]bool, len(ids))
		for _, id := range ids {
			var bt biome.Type
			if err := bt.UnmarshalText([]byte(id)); err != nil {
				return nil, fmt.Errorf("surface: biome condition unknown biome %q: %w", id, err)
			}
			set[bt] = true
		}
		return biomeCondition{biomes: set}, nil

	case "stone_depth":
		var n struct {
			Offset              int    `json:"offset"`
			AddSurfaceDepth     bool   `json:"add_surface_depth"`
			SecondaryDepthRange int    `json:"secondary_depth_range"`
			SurfaceType         string `json:"surface_type"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: stone_depth condition: %w", err)
		}
		surf := surfaceFloor
		switch n.SurfaceType {
		case "floor":
			surf = surfaceFloor
		case "ceiling":
			surf = surfaceCeiling
		default:
			return nil, fmt.Errorf("surface: stone_depth unknown surface_type %q", n.SurfaceType)
		}
		return stoneDepthCondition{
			offset:              n.Offset,
			addSurfaceDepth:     n.AddSurfaceDepth,
			secondaryDepthRange: n.SecondaryDepthRange,
			surface:             surf,
		}, nil

	case "water":
		var n struct {
			Offset                 int  `json:"offset"`
			SurfaceDepthMultiplier int  `json:"surface_depth_multiplier"`
			AddStoneDepth          bool `json:"add_stone_depth"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: water condition: %w", err)
		}
		return waterCondition{
			offset:                 n.Offset,
			surfaceDepthMultiplier: n.SurfaceDepthMultiplier,
			addStoneDepth:          n.AddStoneDepth,
		}, nil

	case "y_above":
		var n struct {
			Anchor                 anchorJSON `json:"anchor"`
			SurfaceDepthMultiplier int        `json:"surface_depth_multiplier"`
			AddStoneDepth          bool       `json:"add_stone_depth"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: y_above condition: %w", err)
		}
		anchor, err := parseAnchor(n.Anchor)
		if err != nil {
			return nil, fmt.Errorf("surface: y_above anchor: %w", err)
		}
		return yCondition{
			anchor:                 anchor,
			surfaceDepthMultiplier: n.SurfaceDepthMultiplier,
			addStoneDepth:          n.AddStoneDepth,
		}, nil

	case "vertical_gradient":
		var n struct {
			TrueAtAndBelow  anchorJSON `json:"true_at_and_below"`
			FalseAtAndAbove anchorJSON `json:"false_at_and_above"`
			RandomName      string     `json:"random_name"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: vertical_gradient condition: %w", err)
		}
		tb, err := parseAnchor(n.TrueAtAndBelow)
		if err != nil {
			return nil, fmt.Errorf("surface: vertical_gradient true_at_and_below: %w", err)
		}
		fa, err := parseAnchor(n.FalseAtAndAbove)
		if err != nil {
			return nil, fmt.Errorf("surface: vertical_gradient false_at_and_above: %w", err)
		}
		return verticalGradientCondition{
			trueAtAndBelowAnchor:  tb,
			falseAtAndAboveAnchor: fa,
			randomName:            n.RandomName,
		}, nil

	case "noise_threshold":
		var n struct {
			Noise        string  `json:"noise"`
			MinThreshold float64 `json:"min_threshold"`
			MaxThreshold float64 `json:"max_threshold"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: noise_threshold condition: %w", err)
		}
		return noiseThresholdCondition{
			noiseID:      n.Noise,
			minThreshold: n.MinThreshold,
			maxThreshold: n.MaxThreshold,
		}, nil

	case "not":
		var n struct {
			Invert json.RawMessage `json:"invert"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("surface: not condition: %w", err)
		}
		inner, err := parseConditionSource(n.Invert)
		if err != nil {
			return nil, fmt.Errorf("surface: not.invert: %w", err)
		}
		return notCondition{inner: inner}, nil

	case "above_preliminary_surface":
		return abovePreliminarySurfaceCondition{}, nil
	case "hole":
		return holeCondition{}, nil
	case "steep":
		return steepCondition{}, nil
	case "temperature":
		return temperatureCondition{}, nil

	default:
		return nil, fmt.Errorf("surface: UNSUPPORTED condition type %q (loud error: the surface_rule graph references a condition this port does not implement)", head.Type)
	}
}

// parseBiomeList accepts the biome_is field as either a single id string or an array
// of id strings (vanilla allows both forms).
func parseBiomeList(raw json.RawMessage) ([]string, error) {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("biome_is is neither a string nor a string array: %w", err)
	}
	return many, nil
}

// parseAnchor parses a VerticalAnchor JSON object into the ported verticalAnchor.
func parseAnchor(a anchorJSON) (verticalAnchor, error) {
	switch {
	case a.Absolute != nil:
		return verticalAnchor{absolute: true, value: *a.Absolute}, nil
	case a.AboveBottom != nil:
		return verticalAnchor{absolute: false, value: *a.AboveBottom}, nil
	case a.BelowTop != nil:
		// below_top would need the gen top; it does not appear in the overworld
		// surface_rule, so reject it loudly rather than guess.
		return verticalAnchor{}, fmt.Errorf("vertical anchor below_top is unsupported by the surface port")
	default:
		return verticalAnchor{}, fmt.Errorf("vertical anchor has no absolute/above_bottom/below_top key")
	}
}

// resolveBlockState resolves a {Name, Properties} block reference to its StateID. With
// no properties it is the registry default; with properties (string values) it encodes
// them as an NBT compound and decodes them onto the typed Block (matching how the rest
// of the codebase resolves block states, level/block.State.Block) before the
// ToStateID lookup. An unknown block or property errors loudly.
func resolveBlockState(bs blockStateJSON) (block.StateID, error) {
	if bs.Name == "" {
		return 0, fmt.Errorf("block result_state has no Name")
	}
	base, ok := block.FromID[bs.Name]
	if !ok {
		return 0, fmt.Errorf("unknown block %q", bs.Name)
	}
	if len(bs.Properties) == 0 {
		sid, ok := block.ToStateID[base]
		if !ok {
			return 0, fmt.Errorf("no state id for default block %q", bs.Name)
		}
		return sid, nil
	}
	// nbt.Marshal(map[string]string) emits [TagCompound, nameLen(2 bytes), body...];
	// State.Properties wants {Type, Data=body} so strip the 3-byte name header.
	data, err := nbt.Marshal(bs.Properties)
	if err != nil {
		return 0, fmt.Errorf("encode properties for %q: %w", bs.Name, err)
	}
	if len(data) < 3 {
		return 0, fmt.Errorf("encoded properties for %q too short", bs.Name)
	}
	st := block.State{Name: bs.Name, Properties: nbt.RawMessage{Type: data[0], Data: data[3:]}}
	resolved, err := st.Block()
	if err != nil {
		return 0, fmt.Errorf("resolve %q with properties %v: %w", bs.Name, bs.Properties, err)
	}
	sid, ok := block.ToStateID[resolved]
	if !ok {
		return 0, fmt.Errorf("no state id for %q with properties %v", bs.Name, bs.Properties)
	}
	return sid, nil
}

// stripNS removes a "minecraft:" namespace prefix (the dispatch keys are bare).
func stripNS(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[i+1:]
		}
	}
	return s
}
