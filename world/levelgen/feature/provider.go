package feature

// This file ports the BlockStateProvider hierarchy
// (net.minecraft.world.level.levelgen.feature.stateproviders.*) — the GetState(rng,
// pos) layer ore/patch/selector/tree configs reference via to_place /
// state_provider / ground_state / trunk_provider / foliage_provider. Each provider's
// GetState draw count IS a determinism contract (research Pitfall 4): a wrong draw
// (or a wrong cumulative-weight walk) desyncs every downstream feature seed.
//
// The providers are PARSED from the already-resolved config: 11-01's walkBlockStates
// resolved every {Name,Properties} leaf, but the provider parser RE-resolves its
// inner states through the SAME resolveBlockState path (feature/blockstate.go) rather
// than position-indexing ParsedConfig.States (brittle across nested providers).
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.stateproviders.BlockStateProvider
//   - SimpleStateProvider / WeightedStateProvider / RuleBasedBlockStateProvider
//   - NoiseProvider / DualNoiseProvider (extend NoiseBasedStateProvider)
//
// It is an algorithmic port, NOT a copy of Mojang source. The provider lives in the
// feature package (beside parse/blockstate); it imports world/levelgen (RandomSource)
// and world/levelgen/synth (NormalNoise) — both acyclic (synth does NOT import
// feature). It does NOT import placement.

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// BlockStateProvider is net.minecraft.world.level.levelgen.feature.stateproviders.
// BlockStateProvider: GetState(rng, x, y, z) -> the block state to place at (x,y,z).
// The (x,y,z) is the candidate world position — only the noise providers consume it
// (positionally, 0 rng draws); simple/weighted/rule_based ignore it. The rng-draw
// count per provider is JAR-exact.
type BlockStateProvider interface {
	// GetState returns the StateID this provider yields at (x,y,z), consuming rng
	// in the JAR-exact draw order for the provider kind.
	GetState(rng levelgen.RandomSource, x, y, z int) block.StateID
}

// ---- SimpleStateProvider (simple_state_provider) ----

// SimpleStateProvider is SimpleStateProvider: a single fixed state, ZERO rng draws.
type SimpleStateProvider struct {
	state block.StateID
}

// GetState returns the fixed state (SimpleStateProvider.getState — no draw).
func (p SimpleStateProvider) GetState(_ levelgen.RandomSource, _, _, _ int) block.StateID {
	return p.state
}

// ---- WeightedStateProvider (weighted_state_provider) ----

// weightedStateEntry is one (state, weight) pair of the SimpleWeightedRandomList.
type weightedStateEntry struct {
	state  block.StateID
	weight int
}

// WeightedStateProvider is WeightedStateProvider over a SimpleWeightedRandomList:
// getState draws ONCE (nextInt(totalWeight)) and walks the cumulative weight in
// ENTRY ORDER to pick a state. Entry order + the single-draw cumulative walk are the
// determinism contract.
type WeightedStateProvider struct {
	entries     []weightedStateEntry
	totalWeight int
}

// GetState ports WeightedStateProvider.getState =
// weightedList.getRandomValue(rng).orElseThrow:
// SimpleWeightedRandomList.getRandomValue draws nextInt(totalWeight) then subtracts
// each entry's weight in order until it goes negative — the selected entry. ONE draw.
func (p WeightedStateProvider) GetState(rng levelgen.RandomSource, _, _, _ int) block.StateID {
	// WeightedRandom.getRandomItem: int i = rng.nextInt(totalWeight); then for each
	// entry: i -= weight; if i < 0 return entry. Equivalent to the cumulative walk.
	i := int(rng.NextIntN(int32(p.totalWeight)))
	for _, e := range p.entries {
		i -= e.weight
		if i < 0 {
			return e.state
		}
	}
	// Unreachable for a positive totalWeight (the loop always selects); return the
	// last entry defensively (matches getRandomItem's total-weight invariant).
	return p.entries[len(p.entries)-1].state
}

// ---- RuleBasedStateProvider (rule_based_state_provider) ----

// stateRule is one RuleBasedBlockStateProvider.Rule: an ifTrue BlockPredicate over
// the position's EXISTING block + the provider to use when it matches. Phase 12-01
// ports the rule's predicate as a thin func over the existing-state read (the
// matching_block_tag / not composites the tree below_trunk providers use); a richer
// BlockPredicate set lives in placement (placement.BlockPredicate) but feature must
// not import placement, so the rule predicate here is a self-contained func.
type stateRule struct {
	// matches reports whether the rule applies given the existing StateID at the pos.
	matches func(existing block.StateID) bool
	// then is the provider used when matches is true.
	then BlockStateProvider
}

// RuleBasedStateProvider is RuleBasedBlockStateProvider: a fallback provider + an
// ordered rule list. getState walks the rules in order; the FIRST whose predicate
// matches the existing block wins, else the fallback. The rule predicate reads the
// EXISTING block at the position, so getState needs that read — supplied by the
// caller via the existingAt closure set at construction (the tree body passes the
// neighborhood read). With no existingAt the rules cannot match → fallback (safe).
type RuleBasedStateProvider struct {
	fallback   BlockStateProvider
	rules      []stateRule
	existingAt func(x, y, z int) block.StateID
}

// GetState ports RuleBasedBlockStateProvider.getState: the rules consult the EXISTING
// block at (x,y,z); the first matching rule's provider is used, else the fallback.
// The chosen provider's own draws follow (simple = 0, weighted = 1).
func (p RuleBasedStateProvider) GetState(rng levelgen.RandomSource, x, y, z int) block.StateID {
	if p.existingAt != nil {
		existing := p.existingAt(x, y, z)
		for _, r := range p.rules {
			if r.matches != nil && r.matches(existing) {
				return r.then.GetState(rng, x, y, z)
			}
		}
		// No rule matched: the identity fallback (absent `fallback` in the 26.2 tree
		// data) keeps the EXISTING block; a concrete fallback yields its own state.
		if _, ident := p.fallback.(identityStateProvider); ident {
			return existing
		}
	}
	return p.fallback.GetState(rng, x, y, z)
}

// ---- RandomizedIntStateProvider (randomized_int_state_provider) ----

// RandomizedIntStateProvider is RandomizedIntStateProvider: it draws the source provider's
// state, then randomizes ONE int property (e.g. mangrove_propagule `age` 0..4) via an
// IntProvider draw. getState = source.getState(rng) (its draws) then values.sample(rng) then
// state.setValue(property, value). The two-stage draw order is the determinism contract
// (T-13-15). Setting the property re-resolves {Name, sourceProps + property=value}; the source
// is a simple_state_provider in the data, so we keep its literal {Name,Properties} for the
// re-resolution.
//
// Source: javap -c RandomizedIntStateProvider.getState.
type RandomizedIntStateProvider struct {
	source      BlockStateProvider
	property    string
	values      intProvider // the in-package IntProvider (tree.go: constant/uniform/weighted_list)
	sourceName  string
	sourceProps map[string]string
}

// GetState ports RandomizedIntStateProvider.getState: draw the source state (for its draw
// count), draw the int value, then re-resolve the source {Name,Properties} with `property`
// overridden. The source-state draw is consumed even though the re-resolution rebuilds from
// the literal source name/props (the draws must stay in lockstep).
func (p RandomizedIntStateProvider) GetState(rng levelgen.RandomSource, x, y, z int) block.StateID {
	_ = p.source.GetState(rng, x, y, z) // consume the source draw (jar order)
	v := p.values.sample(rng)
	props := make(map[string]string, len(p.sourceProps)+1)
	for k, val := range p.sourceProps {
		props[k] = val
	}
	props[p.property] = intToString(v)
	sid, err := resolveBlockState(blockStateJSON{Name: p.sourceName, Properties: props})
	if err != nil {
		// An out-of-range property value re-resolution failing is a build-data corruption;
		// fall back to the un-randomized source state (never panics mid-decoration).
		return p.source.GetState(rng, x, y, z)
	}
	return sid
}

// intToString is a tiny non-negative int -> decimal string (property values; age 0..4).
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// identityStateProvider is the implicit fallback for a rule_based provider with NO
// `fallback` field (the 26.2 tree below_trunk_providers): it yields the EXISTING block
// (a no-change). Its GetState is unreachable when bound via RuleBasedStateProvider (which
// special-cases it against existingAt); the 0-state return is the safe air default for an
// unbound use (no existingAt — the rules cannot match anyway, so the provider is inert).
type identityStateProvider struct{}

// GetState returns 0 (air) for the unbound case; the bound rule_based path returns the
// existing block before reaching here.
func (identityStateProvider) GetState(_ levelgen.RandomSource, _, _, _ int) block.StateID {
	return 0
}

// withExisting returns a copy of the rule-based provider bound to an existing-block
// reader (the tree/patch body supplies the neighborhood read so the rules evaluate).
func (p RuleBasedStateProvider) withExisting(existingAt func(x, y, z int) block.StateID) RuleBasedStateProvider {
	p.existingAt = existingAt
	return p
}

// getOptionalState ports RuleBasedBlockStateProvider.getOptionalState: returns (state, true)
// for the FIRST matching rule, else (0, false) — NO identity fallback. AlterGroundDecorator
// uses this to skip non-podzol-replaceable ground (the rule's tag predicate gates it).
func (p RuleBasedStateProvider) getOptionalState(rng levelgen.RandomSource, x, y, z int) (block.StateID, bool) {
	if p.existingAt == nil {
		return 0, false
	}
	existing := p.existingAt(x, y, z)
	for _, r := range p.rules {
		if r.matches != nil && r.matches(existing) {
			return r.then.GetState(rng, x, y, z), true
		}
	}
	return 0, false
}

// OptionalState is the provider-side optional-state seam the AlterGround decorator needs
// (BlockStateProvider.getOptionalState). For a rule_based provider it gates on the rule tag;
// for any other provider it always yields its state.
func OptionalState(p BlockStateProvider, rng levelgen.RandomSource, x, y, z int) (block.StateID, bool) {
	if rb, ok := p.(RuleBasedStateProvider); ok {
		return rb.getOptionalState(rng, x, y, z)
	}
	return p.GetState(rng, x, y, z), true
}

// ---- NoiseProvider / DualNoiseProvider (noise_provider / dual_noise_provider) ----
//
// NoiseBasedStateProvider samples a NormalNoise at the SCALED position and maps the
// value to an index into the state list — a POSITIONAL pick (0 rng draws; the
// determinism is the seed + position, not the threaded rng).

// NoiseProvider is NoiseProvider (extends NoiseBasedStateProvider): getState samples
// the noise at (x*scale, 0, z*scale) — getState reads x/z only (NoiseProvider.
// getState calls getRandomState(states, x, z, scale)) — and maps |value| to a state.
type NoiseProvider struct {
	noise  *synth.NormalNoise
	scale  float64
	states []block.StateID
}

// getRandomState ports NoiseBasedStateProvider.getRandomState(states, noiseValue):
// idx = (int)(((1 + noiseValue) / 2) * (states.size()-1) + 0.5) clamped to
// [0, size-1]; return states[idx]. Vanilla rounds via the +0.5 then truncates.
func getRandomState(states []block.StateID, value float64) block.StateID {
	n := len(states)
	if n == 0 {
		return 0
	}
	d := (1.0 + value) / 2.0
	idx := int(math.Floor(d*float64(n-1) + 0.5))
	if idx < 0 {
		idx = 0
	}
	if idx > n-1 {
		idx = n - 1
	}
	return states[idx]
}

// GetState ports NoiseProvider.getState: sample at the scaled (x, 0, z) and map.
// NoiseProvider.getState(rng, pos) = getState(seed, noise, scale, pos) →
// getRandomState(states, getNoiseValue(pos, scale)) where getNoiseValue =
// noise.getValue(x*scale, y*scale, z*scale). NoiseProvider samples at y=0 (it ignores
// the y in its 2D variant — JAR getState uses pos.getX/getY/getZ; the overworld
// flower providers are effectively 2D since scale folds y). 0 rng draws.
func (p NoiseProvider) GetState(_ levelgen.RandomSource, x, y, z int) block.StateID {
	v := p.noise.GetValue(float64(x)*p.scale, float64(y)*p.scale, float64(z)*p.scale)
	return getRandomState(p.states, v)
}

// DualNoiseProvider is DualNoiseProvider (extends NoiseProvider): a SLOW noise picks
// a SUB-SLICE of the state list (a variety window), then the fast noise picks within
// it. getState: slowValue → the slice length via a [variety.min,variety.max] map,
// then the fast noise indexes the slice. 0 rng draws (positional). For the overworld
// flower_forest dual provider the variety params are absent in JSON (defaults), so
// this ports the full-list fallback the data exercises while keeping the slow/fast
// structure for completeness.
type DualNoiseProvider struct {
	NoiseProvider
	slowNoise *synth.NormalNoise
	slowScale float64
	// variety bounds (DualNoiseProvider.variety, an InclusiveRange<Integer>); when
	// absent the full state list is used.
	varietyMin, varietyMax int
}

// GetState ports DualNoiseProvider.getState: the slow noise selects how many states
// (the variety window length) participate; the fast noise then indexes that window.
// DualNoiseProvider.getState(rng, pos):
//
//	d = slowNoise.getValue(x*slowScale, y*slowScale, z*slowScale)
//	max = clampedMap(d, -1, 1, varietyMin, varietyMax) rounded
//	list = states[0:max]   (the first `max` states)
//	return getRandomState(list, getNoiseValue(pos, scale))
//
// With varietyMin==varietyMax==len(states) (the absent-variety default) the window is
// the full list, reducing to NoiseProvider. 0 rng draws.
func (p DualNoiseProvider) GetState(_ levelgen.RandomSource, x, y, z int) block.StateID {
	d := p.slowNoise.GetValue(float64(x)*p.slowScale, float64(y)*p.slowScale, float64(z)*p.slowScale)
	// clampedMap(d, -1, 1, min, max): t=(d-(-1))/(1-(-1)) clamped to [0,1];
	// max = min + round(t*(max-min)). Rounded count, then bounded to >=1, <=len.
	t := (d + 1.0) / 2.0
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	count := p.varietyMin + int(math.Floor(t*float64(p.varietyMax-p.varietyMin)+0.5))
	if count < 1 {
		count = 1
	}
	if count > len(p.states) {
		count = len(p.states)
	}
	window := p.states[:count]
	v := p.noise.GetValue(float64(x)*p.scale, float64(y)*p.scale, float64(z)*p.scale)
	return getRandomState(window, v)
}

// ---- NoiseThresholdProvider (noise_threshold_provider) ----
//
// NoiseThresholdProvider (extends NoiseBasedStateProvider) is the overworld flower picker
// (flower_plain / flower_default): it samples the NormalNoise at the SCALED position and, on
// a threshold split, chooses between a lowStates list (below threshold), a highStates list
// (above threshold, gated by a highChance float roll), and a defaultState. It is used by the
// plains/forest simple_block flower features -- so an unported crash here kills terrain
// decoration outright (the "Loading terrain" panic).
//
// getState (javap NoiseThresholdProvider.getState):
//
//	d = getNoiseValue(pos, scale)                    // noise.getValue(x*scale, y*scale, z*scale); 0 draws
//	if d < threshold:  return Util.getRandom(lowStates, rng)   // 1 draw: lowStates[nextInt(size)]
//	if rng.nextFloat() < highChance:                           // 1 draw
//	                   return Util.getRandom(highStates, rng)  // + 1 draw: highStates[nextInt(size)]
//	return defaultState                                        // (only the nextFloat draw consumed)
//
// The draw order (noise positional -> optional lowStates pick -> optional nextFloat +
// highStates pick) is the determinism contract. Util.getRandom(List,rng) =
// list.get(rng.nextInt(list.size())) (javap net.minecraft.util.Util.getRandom) -- exactly ONE
// nextInt draw.
type NoiseThresholdProvider struct {
	noise        *synth.NormalNoise
	scale        float64
	threshold    float32
	highChance   float32
	defaultState block.StateID
	lowStates    []block.StateID
	highStates   []block.StateID
}

// GetState ports NoiseThresholdProvider.getState 1:1 (draw order above). float32 is kept for
// the threshold/highChance compares so the widening matches the jar's f2d / fcmpg exactly.
func (p NoiseThresholdProvider) GetState(rng levelgen.RandomSource, x, y, z int) block.StateID {
	d := p.noise.GetValue(float64(x)*p.scale, float64(y)*p.scale, float64(z)*p.scale)
	if d < float64(p.threshold) { // dcmpg: d < (double)threshold
		return p.lowStates[int(rng.NextIntN(int32(len(p.lowStates))))]
	}
	if rng.NextFloat() < p.highChance { // fcmpg: nextFloat() < highChance
		return p.highStates[int(rng.NextIntN(int32(len(p.highStates))))]
	}
	return p.defaultState
}

// ---- RotatedBlockProvider (rotated_block_provider) ----
//
// RotatedBlockProvider (extends BlockStateProvider) picks a RANDOM pillar axis for a
// RotatedPillarBlock (hay_block, bone_block, etc.) -- used by block_pile features (pile_hay in
// villages, pile_snow, etc.). getState (javap RotatedBlockProvider.getState):
//
//	axis = Direction.Axis.getRandom(rng)             // VALUES[rng.nextInt(3)] over {X,Y,Z}; 1 draw
//	return block.defaultBlockState().trySetValue(AXIS, axis)
//
// Direction.Axis.getRandom = Util.getRandom(VALUES, rng) = VALUES[rng.nextInt(3)] with VALUES
// in declaration order {X, Y, Z} (javap Direction$Axis). trySetValue keeps the state unchanged
// if the block has no AXIS property (a non-pillar block) -- but the data only ever wires this
// provider to RotatedPillarBlocks. ONE draw.
type RotatedBlockProvider struct {
	// blockName is the block id (from the config `state.Name`); GetState re-resolves it with the
	// rolled axis via resolveBlockState (the shared property-encoding path), so a non-pillar
	// block (no `axis` prop) degrades to the property-less default exactly like trySetValue.
	blockName string
}

// rotatedAxisValues is Direction.Axis.VALUES in declaration order {X, Y, Z} -- the array
// Util.getRandom indexes. The order IS the determinism contract (nextInt(3) selects by index).
var rotatedAxisValues = [3]string{"x", "y", "z"}

// GetState ports RotatedBlockProvider.getState: one nextInt(3) selects the pillar axis, then
// the block's default state is re-resolved with axis=<rolled>.
func (p RotatedBlockProvider) GetState(rng levelgen.RandomSource, _, _, _ int) block.StateID {
	axis := rotatedAxisValues[int(rng.NextIntN(3))]
	sid, err := resolveBlockState(blockStateJSON{Name: p.blockName, Properties: map[string]string{"axis": axis}})
	if err != nil {
		// A block with no `axis` property (non-pillar): trySetValue is a no-op in the jar, so
		// fall back to the property-less default state (never panics mid-decoration).
		sid, err2 := resolveBlockState(blockStateJSON{Name: p.blockName})
		if err2 != nil {
			return 0
		}
		return sid
	}
	return sid
}

// ---- JSON parsing ----

// jsonBlockStateRef is the {Name,Properties} leaf shape (alias of the resolver's).
type jsonBlockStateRef = blockStateJSON

// jsonNoise is the {firstOctave, amplitudes} NormalNoise parameter object the
// noise/dual_noise providers carry inline.
type jsonNoise struct {
	FirstOctave int       `json:"firstOctave"`
	Amplitudes  []float64 `json:"amplitudes"`
}

// jsonProvider is the union of every provider envelope's fields. The "type" tag
// selects which subset is meaningful.
type jsonProvider struct {
	Type    string          `json:"type"`
	State   json.RawMessage `json:"state"`    // simple
	Entries []struct {      // weighted
		Data   json.RawMessage `json:"data"`
		Weight int             `json:"weight"`
	} `json:"entries"`
	Fallback json.RawMessage `json:"fallback"` // rule_based
	Rules    []struct {      // rule_based
		IfTrue json.RawMessage `json:"if_true"`
		Then   json.RawMessage `json:"then"`
	} `json:"rules"`
	Noise     *jsonNoise      `json:"noise"`      // noise / dual_noise
	Scale     float64         `json:"scale"`      // noise / dual_noise
	Seed      int64           `json:"seed"`       // noise / dual_noise
	States    json.RawMessage `json:"states"`     // noise / dual_noise
	SlowNoise *jsonNoise      `json:"slow_noise"` // dual_noise
	SlowScale float64         `json:"slow_scale"` // dual_noise
	Variety json.RawMessage `json:"variety"` // dual_noise (optional); InclusiveRange either-codec

	Threshold    float32         `json:"threshold"`     // noise_threshold
	HighChance   float32         `json:"high_chance"`   // noise_threshold
	DefaultState json.RawMessage `json:"default_state"` // noise_threshold
	LowStates    json.RawMessage `json:"low_states"`    // noise_threshold
	HighStates   json.RawMessage `json:"high_states"`   // noise_threshold
}

// parseInclusiveRange ports the vanilla InclusiveRange<Integer> codec, which is an
// EITHER-codec: it accepts BOTH the object form {"min_inclusive":m,"max_inclusive":n}
// AND the list form [m, n]. The DualNoiseProvider `variety` field uses it, and some
// embedded configs (e.g. flower_meadow) serialize it as the 2-element array — parsing it
// only as the object form panicked ("cannot unmarshal array into ... variety") and crashed
// decoration. CITE: net.minecraft.util.valueproviders.InclusiveRange.CODEC.
func parseInclusiveRange(raw json.RawMessage) (min, max int, ok bool, err error) {
	if len(raw) == 0 {
		return 0, 0, false, nil
	}
	// List form [min, max].
	var arr []int
	if e := json.Unmarshal(raw, &arr); e == nil {
		if len(arr) != 2 {
			return 0, 0, false, fmt.Errorf("inclusive_range list form must have 2 elements, got %d", len(arr))
		}
		return arr[0], arr[1], true, nil
	}
	// Object form {min_inclusive, max_inclusive}.
	var obj struct {
		MinInclusive int `json:"min_inclusive"`
		MaxInclusive int `json:"max_inclusive"`
	}
	if e := json.Unmarshal(raw, &obj); e != nil {
		return 0, 0, false, fmt.Errorf("inclusive_range: neither list [min,max] nor {min_inclusive,max_inclusive}: %w", e)
	}
	return obj.MinInclusive, obj.MaxInclusive, true, nil
}

// ParseProvider decodes a BlockStateProvider envelope by its "type", resolving inner
// {Name,Properties} states via the in-package resolveBlockState (NOT position-indexing
// ParsedConfig.States). An unported provider type errors LOUDLY (T-12-03 — never a
// silent mis-provider). It is the entry point the Phase-12 feature bodies call on
// their to_place / *_provider config fields.
func ParseProvider(raw json.RawMessage) (BlockStateProvider, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("feature: empty block state provider")
	}
	var j jsonProvider
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: block state provider: %w", err)
	}
	switch stripNS(j.Type) {
	case "simple_state_provider":
		sid, err := parseProviderState(j.State)
		if err != nil {
			return nil, fmt.Errorf("feature: simple_state_provider: %w", err)
		}
		return SimpleStateProvider{state: sid}, nil

	case "weighted_state_provider":
		if len(j.Entries) == 0 {
			return nil, fmt.Errorf("feature: weighted_state_provider has no entries")
		}
		p := WeightedStateProvider{}
		for _, e := range j.Entries {
			sid, err := parseProviderState(e.Data)
			if err != nil {
				return nil, fmt.Errorf("feature: weighted_state_provider entry: %w", err)
			}
			if e.Weight <= 0 {
				return nil, fmt.Errorf("feature: weighted_state_provider entry has non-positive weight %d", e.Weight)
			}
			p.entries = append(p.entries, weightedStateEntry{state: sid, weight: e.Weight})
			p.totalWeight += e.Weight
		}
		return p, nil

	case "rule_based_state_provider":
		// The 26.2 tree below_trunk_providers (verified oak.json/birch.json) carry ONLY
		// `rules` and OMIT `fallback`. An absent fallback is the identity provider — it
		// yields the EXISTING block at the position (the rule_based provider only writes
		// when a rule matches; with no fallback a non-matching position keeps its block).
		// This is the faithful semantics for the dirt-under-trunk rule, whose "not in
		// cannot_replace_below_tree_trunk" predicate matches every non-trunk ground block,
		// so the fallback is reached only over an existing trunk (a no-change identity).
		var fb BlockStateProvider
		if len(j.Fallback) == 0 {
			fb = identityStateProvider{}
		} else {
			parsed, err := ParseProvider(j.Fallback)
			if err != nil {
				return nil, fmt.Errorf("feature: rule_based_state_provider fallback: %w", err)
			}
			fb = parsed
		}
		p := RuleBasedStateProvider{fallback: fb}
		for i, r := range j.Rules {
			pred, err := parseRulePredicate(r.IfTrue)
			if err != nil {
				return nil, fmt.Errorf("feature: rule_based_state_provider rule %d if_true: %w", i, err)
			}
			then, err := ParseProvider(r.Then)
			if err != nil {
				return nil, fmt.Errorf("feature: rule_based_state_provider rule %d then: %w", i, err)
			}
			p.rules = append(p.rules, stateRule{matches: pred, then: then})
		}
		return p, nil

	case "noise_provider":
		np, err := parseNoiseProvider(j)
		if err != nil {
			return nil, err
		}
		return np, nil

	case "dual_noise_provider":
		np, err := parseNoiseProvider(j)
		if err != nil {
			return nil, err
		}
		if j.SlowNoise == nil {
			return nil, fmt.Errorf("feature: dual_noise_provider missing slow_noise")
		}
		slowRng := levelgen.NewWorldgenRandom(j.Seed)
		slow := synth.NewNormalNoise(slowRng, j.SlowNoise.FirstOctave, j.SlowNoise.Amplitudes)
		dp := DualNoiseProvider{
			NoiseProvider: np,
			slowNoise:     slow,
			slowScale:     j.SlowScale,
			varietyMin:    len(np.states),
			varietyMax:    len(np.states),
		}
		vmin, vmax, ok, err := parseInclusiveRange(j.Variety)
		if err != nil {
			return nil, fmt.Errorf("feature: dual_noise_provider variety: %w", err)
		}
		if ok {
			dp.varietyMin = vmin
			dp.varietyMax = vmax
		}
		return dp, nil

	case "randomized_int_state_provider":
		return parseRandomizedIntStateProvider(raw)

	case "noise_threshold_provider":
		return parseNoiseThresholdProvider(j)

	case "rotated_block_provider":
		return parseRotatedBlockProvider(j)

	default:
		return nil, fmt.Errorf("feature: unported block state provider type %q", j.Type)
	}
}

// parseNoiseThresholdProvider builds a NoiseThresholdProvider from the envelope: the shared
// noise core (noise + scale + seed) plus threshold, high_chance, default_state, and the
// non-empty low_states / high_states lists (ExtraCodecs.nonEmptyList in the jar -> reject
// empty, else GetState's nextInt(0) would panic).
func parseNoiseThresholdProvider(j jsonProvider) (BlockStateProvider, error) {
	if j.Noise == nil {
		return nil, fmt.Errorf("feature: noise_threshold_provider missing noise params")
	}
	def, err := parseProviderState(j.DefaultState)
	if err != nil {
		return nil, fmt.Errorf("feature: noise_threshold_provider default_state: %w", err)
	}
	low, err := parseProviderStateList(j.LowStates)
	if err != nil {
		return nil, fmt.Errorf("feature: noise_threshold_provider low_states: %w", err)
	}
	high, err := parseProviderStateList(j.HighStates)
	if err != nil {
		return nil, fmt.Errorf("feature: noise_threshold_provider high_states: %w", err)
	}
	if len(low) == 0 || len(high) == 0 {
		return nil, fmt.Errorf("feature: noise_threshold_provider low_states/high_states must be non-empty (jar nonEmptyList)")
	}
	rng := levelgen.NewWorldgenRandom(j.Seed)
	noise := synth.NewNormalNoise(rng, j.Noise.FirstOctave, j.Noise.Amplitudes)
	return NoiseThresholdProvider{
		noise:        noise,
		scale:        j.Scale,
		threshold:    j.Threshold,
		highChance:   j.HighChance,
		defaultState: def,
		lowStates:    low,
		highStates:   high,
	}, nil
}

// parseRotatedBlockProvider builds a RotatedBlockProvider. The jar codec is BlockState.CODEC
// xmap'd to Block, so the config carries a `state` {Name,Properties}; we keep only the Name
// (GetState re-rolls the axis, discarding any serialized axis in the state). An absent Name is
// build-data corruption.
func parseRotatedBlockProvider(j jsonProvider) (BlockStateProvider, error) {
	if len(j.State) == 0 {
		return nil, fmt.Errorf("feature: rotated_block_provider missing state")
	}
	var ref jsonBlockStateRef
	if err := json.Unmarshal(j.State, &ref); err != nil {
		return nil, fmt.Errorf("feature: rotated_block_provider state: %w", err)
	}
	if ref.Name == "" {
		return nil, fmt.Errorf("feature: rotated_block_provider state missing Name")
	}
	return RotatedBlockProvider{blockName: ref.Name}, nil
}

// parseRandomizedIntStateProvider decodes a randomized_int_state_provider envelope: the
// inner `source` provider (a simple_state_provider in the data), the `property` to randomize,
// and the `values` IntProvider. It captures the source's literal {Name,Properties} so GetState
// can re-resolve with the property overridden.
func parseRandomizedIntStateProvider(raw json.RawMessage) (BlockStateProvider, error) {
	var j struct {
		Property string          `json:"property"`
		Source   json.RawMessage `json:"source"`
		Values   json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: randomized_int_state_provider: %w", err)
	}
	if j.Property == "" {
		return nil, fmt.Errorf("feature: randomized_int_state_provider missing property")
	}
	src, err := ParseProvider(j.Source)
	if err != nil {
		return nil, fmt.Errorf("feature: randomized_int_state_provider source: %w", err)
	}
	// Capture the source's literal {Name,Properties} (the source is simple_state_provider).
	var srcEnv struct {
		Type  string         `json:"type"`
		State blockStateJSON `json:"state"`
	}
	if err := json.Unmarshal(j.Source, &srcEnv); err != nil {
		return nil, fmt.Errorf("feature: randomized_int_state_provider source state: %w", err)
	}
	if stripNS(srcEnv.Type) != "simple_state_provider" {
		return nil, fmt.Errorf("feature: randomized_int_state_provider source must be simple_state_provider, got %q", srcEnv.Type)
	}
	vals, err := parseIntProvider(j.Values)
	if err != nil {
		return nil, fmt.Errorf("feature: randomized_int_state_provider values: %w", err)
	}
	props := map[string]string{}
	for k, v := range srcEnv.State.Properties {
		props[k] = v
	}
	return RandomizedIntStateProvider{
		source:      src,
		property:    j.Property,
		values:      vals,
		sourceName:  srcEnv.State.Name,
		sourceProps: props,
	}, nil
}

// parseNoiseProvider builds the shared NoiseProvider core (noise + scale + states)
// from the envelope. The NormalNoise is seeded from the provider's own `seed`
// (NoiseBasedStateProvider uses a WorldgenRandom over the seed, distinct from the
// decoration rng).
func parseNoiseProvider(j jsonProvider) (NoiseProvider, error) {
	if j.Noise == nil {
		return NoiseProvider{}, fmt.Errorf("feature: noise_provider missing noise params")
	}
	states, err := parseProviderStateList(j.States)
	if err != nil {
		return NoiseProvider{}, fmt.Errorf("feature: noise_provider states: %w", err)
	}
	if len(states) == 0 {
		return NoiseProvider{}, fmt.Errorf("feature: noise_provider has no states")
	}
	rng := levelgen.NewWorldgenRandom(j.Seed)
	noise := synth.NewNormalNoise(rng, j.Noise.FirstOctave, j.Noise.Amplitudes)
	return NoiseProvider{noise: noise, scale: j.Scale, states: states}, nil
}

// parseProviderState resolves a single {Name,Properties} provider state to a StateID.
func parseProviderState(raw json.RawMessage) (block.StateID, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("missing state")
	}
	var ref jsonBlockStateRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return 0, fmt.Errorf("decoding state ref: %w", err)
	}
	return resolveBlockState(ref)
}

// parseProviderStateList resolves a JSON array of {Name,Properties} refs to StateIDs.
func parseProviderStateList(raw json.RawMessage) ([]block.StateID, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("decoding state list: %w", err)
	}
	out := make([]block.StateID, 0, len(arr))
	for _, r := range arr {
		sid, err := parseProviderState(r)
		if err != nil {
			return nil, err
		}
		out = append(out, sid)
	}
	return out, nil
}

// ruleBlockPredicate is the minimal RuleBasedBlockStateProvider rule-predicate set
// (a SEPARATE, self-contained predicate from placement.BlockPredicate so feature does
// not import placement). The tree below-trunk providers use matching_block_tag (over
// the existing block) and not; that minimal set is ported here. An unported rule
// predicate type errors loudly.
type jsonRulePredicate struct {
	Type      string          `json:"type"`
	Tag       string          `json:"tag"`       // matching_block_tag
	Blocks    json.RawMessage `json:"blocks"`     // matching_blocks
	Predicate json.RawMessage `json:"predicate"`  // not
	Predicates json.RawMessage `json:"predicates"` // all_of/any_of
}

// parseRulePredicate builds a func(existing) bool for a rule's if_true predicate. The
// rule predicates evaluate over the EXISTING block at the position (RuleBasedProvider
// rules gate on what is already there, e.g. "not in cannot_replace_below_tree_trunk").
func parseRulePredicate(raw json.RawMessage) (func(block.StateID) bool, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing if_true predicate")
	}
	var j jsonRulePredicate
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("decoding rule predicate: %w", err)
	}
	switch stripNS(j.Type) {
	case "matching_block_tag":
		set, err := resolveBlockTagSet(j.Tag)
		if err != nil {
			return nil, err
		}
		return func(s block.StateID) bool { return set[s] }, nil
	case "matching_blocks":
		set, err := parseBlockStateSet(j.Blocks)
		if err != nil {
			return nil, err
		}
		return func(s block.StateID) bool { return set[s] }, nil
	case "not":
		inner, err := parseRulePredicate(j.Predicate)
		if err != nil {
			return nil, fmt.Errorf("not: %w", err)
		}
		return func(s block.StateID) bool { return !inner(s) }, nil
	case "all_of":
		subs, err := parseRulePredicateList(j.Predicates)
		if err != nil {
			return nil, fmt.Errorf("all_of: %w", err)
		}
		return func(s block.StateID) bool {
			for _, p := range subs {
				if !p(s) {
					return false
				}
			}
			return true
		}, nil
	case "any_of":
		subs, err := parseRulePredicateList(j.Predicates)
		if err != nil {
			return nil, fmt.Errorf("any_of: %w", err)
		}
		return func(s block.StateID) bool {
			for _, p := range subs {
				if p(s) {
					return true
				}
			}
			return false
		}, nil
	case "true":
		return func(block.StateID) bool { return true }, nil
	default:
		return nil, fmt.Errorf("feature: unported rule predicate type %q", j.Type)
	}
}

// parseRulePredicateList parses a JSON array of rule predicates.
func parseRulePredicateList(raw json.RawMessage) ([]func(block.StateID) bool, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("decoding predicate list: %w", err)
	}
	out := make([]func(block.StateID) bool, 0, len(arr))
	for _, r := range arr {
		p, err := parseRulePredicate(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// parseBlockStateSet resolves a "blocks" field (a single block id or an array of ids)
// to the set of ALL their state ids.
func parseBlockStateSet(raw json.RawMessage) (map[block.StateID]bool, error) {
	ids := map[string]bool{}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		ids[one] = true
	} else {
		var many []string
		if err := json.Unmarshal(raw, &many); err != nil {
			return nil, fmt.Errorf("decoding blocks: %w", err)
		}
		for _, m := range many {
			ids[m] = true
		}
	}
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if ids[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set, nil
}

// ruleProviderTags maps the #block tags the rule_based providers reference to their
// member block ids, resolved constant-for-constant from the vanilla 26.2 block-tag
// defs (data/minecraft/tags/block/*.json), the carver Replaceables precedent. Only
// the handful the tree below-trunk providers actually use are listed; an unlisted tag
// errors loudly (so a jar bump that adds one fails rather than drifting).
var ruleProviderTags = map[string][]string{
	// #minecraft:dirt — the dirt-like blocks a below-trunk provider may sit on.
	"minecraft:dirt": {
		"minecraft:dirt", "minecraft:grass_block", "minecraft:podzol",
		"minecraft:coarse_dirt", "minecraft:mycelium", "minecraft:rooted_dirt",
		"minecraft:moss_block", "minecraft:mud", "minecraft:muddy_mangrove_roots",
	},
	// #minecraft:cannot_replace_below_tree_trunk (acacia rule_based_state_provider
	// gates on NOT-in this tag). The vanilla tag lists the blocks a trunk must not
	// overwrite below it.
	"minecraft:cannot_replace_below_tree_trunk": {
		"minecraft:spruce_log", "minecraft:spruce_leaves",
		"minecraft:oak_log", "minecraft:oak_leaves",
		"minecraft:birch_log", "minecraft:birch_leaves",
		"minecraft:jungle_log", "minecraft:jungle_leaves",
		"minecraft:acacia_log", "minecraft:acacia_leaves",
		"minecraft:dark_oak_log", "minecraft:dark_oak_leaves",
		"minecraft:cherry_log", "minecraft:cherry_leaves",
		"minecraft:pale_oak_log", "minecraft:pale_oak_leaves",
		"minecraft:mangrove_log", "minecraft:mangrove_leaves", "minecraft:mangrove_roots",
	},
	// #minecraft:beneath_tree_podzol_replaceable (alter_ground decorator's rule gate). It
	// flattens #substrate_overworld -> #dirt + #mud + #moss_blocks + #grass_blocks (jar tag
	// chain, verified constant-for-constant): the ground a spruce/mega-spruce podzol disk
	// may replace.
	"minecraft:beneath_tree_podzol_replaceable": {
		"minecraft:dirt", "minecraft:coarse_dirt", "minecraft:rooted_dirt",
		"minecraft:mud", "minecraft:muddy_mangrove_roots",
		"minecraft:moss_block", "minecraft:pale_moss_block",
		"minecraft:grass_block", "minecraft:podzol", "minecraft:mycelium",
	},
}

// resolveBlockTagSet resolves a #block tag id to the set of all member state ids. The
// special "minecraft:air" tag is the common flower/grass case — but as a RULE
// predicate over an existing block, an air membership test is just block.IsAir, so it
// is handled by a synthesized set is unnecessary; instead air is resolved through the
// air block ids. Other tags resolve constant-for-constant via ruleProviderTags.
func resolveBlockTagSet(tag string) (map[block.StateID]bool, error) {
	if tag == "" {
		return nil, fmt.Errorf("matching_block_tag missing tag")
	}
	var ids map[string]bool
	switch tag {
	case "minecraft:air":
		ids = map[string]bool{"minecraft:air": true, "minecraft:cave_air": true, "minecraft:void_air": true}
	default:
		members, ok := ruleProviderTags[tag]
		if !ok {
			return nil, fmt.Errorf("feature: unported block tag %q in rule predicate "+
				"(add it constant-for-constant from the jar tag def, carver precedent)", tag)
		}
		ids = make(map[string]bool, len(members))
		for _, m := range members {
			ids[m] = true
		}
	}
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if ids[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set, nil
}

// compile-time assertions: every provider satisfies BlockStateProvider.
var (
	_ BlockStateProvider = SimpleStateProvider{}
	_ BlockStateProvider = WeightedStateProvider{}
	_ BlockStateProvider = RuleBasedStateProvider{}
	_ BlockStateProvider = NoiseProvider{}
	_ BlockStateProvider = DualNoiseProvider{}
	_ BlockStateProvider = NoiseThresholdProvider{}
	_ BlockStateProvider = RotatedBlockProvider{}
	_ BlockStateProvider = identityStateProvider{}
)
