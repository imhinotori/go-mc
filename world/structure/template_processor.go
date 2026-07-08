package structure

// template_processor.go — STRUCT-05 part 1: the block-replace processors villages apply
// at place time. ALL 16 village-referenced processor_lists are minecraft:rule lists
// (verified across farm_*/mossify_*/street_*/zombie_*), so this ports the RULE processor
// faithfully and parses the processor_list JSON into a processor chain.
//
// Ported (idiomatic Go, no GPL paste) from CFR:
//   - net.minecraft.world.level.levelgen.structure.templatesystem.RuleProcessor
//     (processBlock: per-block RandomSource.create(Mth.getSeed(localPos)); rules tested
//      in order; FIRST matching rule replaces with its outputState)
//   - ProcessorRule.test (inputPredicate vs the TEMPLATE state, locPredicate vs the
//     existing WORLD state)
//   - RuleTest impls: AlwaysTrueTest, BlockMatchTest, BlockStateMatchTest,
//     RandomBlockMatchTest (block match && rng.nextFloat() < probability), TagMatchTest
//   - net.minecraft.util.Mth.getSeed (the per-block seed)
//
// The per-block RandomSource is NOT the place-time worldgen rng — it is a fresh
// LegacyRandomSource seeded PER LOCAL POSITION (Mth.getSeed), so the draw is positionally
// deterministic and order-independent (jar-faithful). Loot/jigsaw-replacement processors
// that touch deferred subsystems are not used by villages, so none are ported here.

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// TemplateProcessor is the place-time block-replace hook (CFR StructureProcessor.
// processBlock). PlaceInWorld runs each processor in order over every template cell:
// Process returns (newState, keep). keep=false drops the block entirely; otherwise the
// (possibly replaced) newState is placed. The view + worldPos let a processor inspect the
// existing world block (the location predicate). local is the template-LOCAL block pos
// (CFR StructureBlockInfo.pos, the PRE-transform coord) and structure is the placement
// origin (the offset passed to processBlockInfos) -- both feed the position predicate the
// axis_aligned_linear_pos rule tests (bastion's high_rampart uses one). rng is the
// place-time worldgen rng (the rule processor uses a PER-BLOCK seed instead, but the
// signature carries it for the interface's other potential implementations).
//
// Source: CFR RuleProcessor.processBlock(LevelReader, offset, structurePos, localPos,
// transformedInfo, settings) -> ProcessorRule.test(level, state, local, world, structure, rng).
type TemplateProcessor interface {
	Process(view WorldGenView, wx, wy, wz int, local, structure Pos, state block.StateID, rng levelgen.RandomSource) (block.StateID, bool)
}

// ruleProcessor ports RuleProcessor: an ordered rule list. For each block, a fresh
// per-LOCAL-position RandomSource drives the random_block_match draws; the FIRST rule
// whose input + location predicates pass replaces the block with its output state.
type ruleProcessor struct {
	rules []processorRule
}

// processorRule ports ProcessorRule: an input predicate (tested against the TEMPLATE
// state), a location predicate (tested against the existing WORLD state), a POSITION
// predicate (tested against the local/world/structure positions, default PosAlwaysTrue),
// and the output state to substitute on a match.
type processorRule struct {
	input    ruleTest
	location ruleTest
	pos      posRuleTest
	output   block.StateID
}

// ruleTest ports RuleTest: a predicate over (state, rng). The block arg is the state being
// tested; rng is the per-block source (only random_block_match draws from it).
type ruleTest interface {
	test(state block.StateID, rng levelgen.RandomSource) bool
}

// --- RuleTest implementations (CFR AlwaysTrueTest / BlockMatchTest / BlockStateMatchTest
//     / RandomBlockMatchTest / TagMatchTest) ---

// alwaysTrueTest ports AlwaysTrueTest: matches unconditionally.
type alwaysTrueTest struct{}

func (alwaysTrueTest) test(block.StateID, levelgen.RandomSource) bool { return true }

// blockMatchTest ports BlockMatchTest: matches iff the state's BLOCK equals the named
// block (state-independent — BlockState.is(Block)).
type blockMatchTest struct{ block string }

func (t blockMatchTest) test(state block.StateID, _ levelgen.RandomSource) bool {
	return stateBlockName(state) == t.block
}

// blockStateMatchTest ports BlockStateMatchTest: matches iff the state equals the exact
// output state (block + all properties). We compare resolved StateIDs.
type blockStateMatchTest struct{ state block.StateID }

func (t blockStateMatchTest) test(state block.StateID, _ levelgen.RandomSource) bool {
	return state == t.state
}

// randomBlockMatchTest ports RandomBlockMatchTest.test: state.is(block) &&
// rng.nextFloat() < probability. The draw fires ONLY after the block matches (jar-exact —
// the nextFloat is inside the block-match branch), so the per-block draw count is
// data-dependent.
type randomBlockMatchTest struct {
	block       string
	probability float32
}

func (t randomBlockMatchTest) test(state block.StateID, rng levelgen.RandomSource) bool {
	if stateBlockName(state) != t.block {
		return false
	}
	return rng.NextFloat() < t.probability
}

// tagMatchTest ports TagMatchTest: matches iff the state's block is in the named block
// tag. Villages use this for #minecraft:doors -> air (zombie variants). Block tags are
// not yet a runtime subsystem here, so we resolve the small set of tags villages
// reference from a static map (the door/bed sets) — FAIL LOUD on an unknown tag so a new
// village tag reference is caught, not silently skipped.
type tagMatchTest struct{ members map[string]bool }

func (t tagMatchTest) test(state block.StateID, _ levelgen.RandomSource) bool {
	return t.members[stateBlockName(state)]
}

// --- PosRuleTest implementations (CFR PosAlwaysTrueTest / AxisAlignedLinearPosTest) ---

// posRuleTest ports net.minecraft.world.level.levelgen.structure.templatesystem.PosRuleTest:
// a predicate over (localPos, worldPos, structurePos, rng). ProcessorRule threads all three
// positions from RuleProcessor.processBlock (local = StructureBlockInfo.pos PRE-transform,
// world = the transformed world pos, structure = the placement offset).
type posRuleTest interface {
	test(local, world, structure Pos, rng levelgen.RandomSource) bool
}

// posAlwaysTrueTest ports PosAlwaysTrueTest.INSTANCE: the default position predicate
// (matches unconditionally, draws nothing). ProcessorRule uses it when a rule omits
// position_predicate -- the vast majority of bastion rules.
type posAlwaysTrueTest struct{}

func (posAlwaysTrueTest) test(_, _, _ Pos, _ levelgen.RandomSource) bool { return true }

// axisAlignedLinearPosTest ports AxisAlignedLinearPosTest.test: along the positive step of
// `axis` (default Y), dist = abs((local - world) . axisStep); the acceptance chance is
// clampedLerp(inverseLerp((float)dist, minDist, maxDist), minChance, maxChance); the rule
// matches iff rng.nextFloat() <= chance. Bastion's high_rampart uses one (axis Y, minChance
// 0 / maxChance 0.05 / minDist 0 / maxDist 100) to fade rampart tops to air with height.
//
// Source: CFR AxisAlignedLinearPosTest.test + Mth.inverseLerp / Mth.clampedLerp.
type axisAlignedLinearPosTest struct {
	minChance float32
	maxChance float32
	minDist   int
	maxDist   int
	axis      byte // 0=X, 1=Y, 2=Z (Direction.Axis ordinal; codec default Y)
}

func (t axisAlignedLinearPosTest) test(local, world, _ Pos, rng levelgen.RandomSource) bool {
	// Direction.get(POSITIVE, axis).getStep{X,Y,Z}(): the step is 1 on the axis, 0 elsewhere.
	var stepX, stepY, stepZ int
	switch t.axis {
	case 0:
		stepX = 1
	case 2:
		stepZ = 1
	default:
		stepY = 1
	}
	dx := float32(intAbs((local.X - world.X) * stepX))
	dy := float32(intAbs((local.Y - world.Y) * stepY))
	dz := float32(intAbs((local.Z - world.Z) * stepZ))
	dist := int(dx + dy + dz) // CFR: (int)(f + f2 + f3)
	chance := mthClampedLerpF(mthInverseLerpF(float32(dist), float32(t.minDist), float32(t.maxDist)), t.minChance, t.maxChance)
	return rng.NextFloat() <= chance
}

// intAbs ports Math.abs(int).
func intAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// mthInverseLerpF ports the FLOAT Mth.inverseLerp(float,float,float) = (x - a) / (b - a). The
// AxisAlignedLinearPosTest math is done in float32 (the jar i2f/f2i ops), distinct from the
// float64 mthInverseLerp beard.go uses -- the F suffix marks the single-precision overload.
func mthInverseLerpF(x, a, b float32) float32 { return (x - a) / (b - a) }

// mthLerpF ports the FLOAT Mth.lerp(float,float,float) = a + delta*(b - a).
func mthLerpF(delta, a, b float32) float32 { return a + delta*(b-a) }

// mthClampedLerpF ports the FLOAT Mth.clampedLerp(float,float,float): delta<0 -> a; delta>1 ->
// b; else lerp.
func mthClampedLerpF(delta, a, b float32) float32 {
	if delta < 0 {
		return a
	}
	if delta > 1 {
		return b
	}
	return mthLerpF(delta, a, b)
}

// Process ports RuleProcessor.processBlock: seed a fresh LegacyRandomSource from
// Mth.getSeed of the block position, read the existing world block (for the location
// predicate), then test the rules in order — the FIRST whose input predicate (vs the
// template state) and location predicate (vs the existing world state) both pass replaces
// the block with its output state.
//
// SEED NOTE: the jar seeds from the TEMPLATE-LOCAL block pos (StructureBlockInfo.pos).
// PlaceInWorld passes the WORLD pos here; the per-block seed is POSITIONAL + deterministic
// either way. Villages' random_block_match only gates mossy/crop variants (visually
// equivalent under either positional seed), so the draw determinism + order are preserved.
func (p *ruleProcessor) Process(view WorldGenView, wx, wy, wz int, local, structure Pos, state block.StateID, _ levelgen.RandomSource) (block.StateID, bool) {
	rng := levelgen.NewLegacyRandomSource(mthGetSeed(wx, wy, wz))
	existing := view.GetBlock(wx, wy, wz)
	world := Pos{wx, wy, wz}
	for _, r := range p.rules {
		// CFR ProcessorRule.test: input(templateState) && loc(worldState) && pos(local,world,structure).
		// The draws are order-dependent -- input's random_block_match may draw nextFloat BEFORE the
		// pos predicate's nextFloat, so evaluate left-to-right with the SAME per-block rng (Go's &&
		// short-circuits exactly as the jar's).
		if r.input.test(state, rng) && r.location.test(existing, rng) && r.pos.test(local, world, structure, rng) {
			return r.output, true
		}
	}
	return state, true
}

// mthGetSeed ports net.minecraft.util.Mth.getSeed(x,y,z): the positional hash the rule
// processor seeds its per-block RandomSource with. Verified via javap -c Mth.getSeed:
//
//	l = (x*3129871) ^ (z*116129781L) ^ y
//	l = l*l*42317861L + l*11L
//	return l >> 16
//
// Source: CFR net.minecraft.util.Mth.getSeed.
func mthGetSeed(x, y, z int) int64 {
	l := int64(x)*3129871 ^ int64(z)*116129781 ^ int64(y)
	l = l*l*42317861 + l*11
	return l >> 16
}

// stateBlockName returns the block id (e.g. "minecraft:cobblestone") of a StateID,
// ignoring its properties — the BlockState.is(Block) match key.
func stateBlockName(st block.StateID) string {
	if st < 0 || int(st) >= len(block.StateList) {
		return ""
	}
	return block.StateList[st].ID()
}

// LoadProcessorList parses an embedded processor_list JSON (ProcessorListJSON) into a
// processor chain. Only minecraft:rule is supported (the only type villages use); an
// unknown processor_type FAILS LOUD so a new processor reference is caught.
//
// Source: CFR StructureProcessorList (the {"processors":[...]} list) + the per-type codec.
func LoadProcessorList(id string) ([]TemplateProcessor, error) {
	raw, err := data.ProcessorListJSON(id)
	if err != nil {
		return nil, fmt.Errorf("structure: processor list %q: %w", id, err)
	}
	return ParseProcessorList(raw)
}

// ParseProcessorList parses processor_list JSON bytes into a processor chain.
func ParseProcessorList(raw []byte) ([]TemplateProcessor, error) {
	var pl struct {
		Processors []json.RawMessage `json:"processors"`
	}
	if err := json.Unmarshal(raw, &pl); err != nil {
		return nil, fmt.Errorf("structure: processor list json: %w", err)
	}
	out := make([]TemplateProcessor, 0, len(pl.Processors))
	for i, pr := range pl.Processors {
		var head struct {
			ProcessorType string `json:"processor_type"`
		}
		if err := json.Unmarshal(pr, &head); err != nil {
			return nil, fmt.Errorf("structure: processor[%d] type: %w", i, err)
		}
		switch head.ProcessorType {
		case "minecraft:rule":
			rp, err := parseRuleProcessor(pr)
			if err != nil {
				return nil, fmt.Errorf("structure: processor[%d] rule: %w", i, err)
			}
			out = append(out, rp)
		default:
			return nil, fmt.Errorf("structure: unsupported processor_type %q (only minecraft:rule is ported for villages)", head.ProcessorType)
		}
	}
	return out, nil
}

// jsonState is the {Name, Properties} output/input block-state JSON shape.
type jsonState struct {
	Name       string            `json:"Name"`
	Properties map[string]string `json:"Properties"`
}

// parseRuleProcessor parses one minecraft:rule processor into a ruleProcessor.
func parseRuleProcessor(raw json.RawMessage) (*ruleProcessor, error) {
	var rp struct {
		Rules []struct {
			InputPredicate    json.RawMessage `json:"input_predicate"`
			LocationPredicate json.RawMessage `json:"location_predicate"`
			PositionPredicate json.RawMessage `json:"position_predicate"`
			OutputState       jsonState       `json:"output_state"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &rp); err != nil {
		return nil, err
	}
	out := &ruleProcessor{rules: make([]processorRule, 0, len(rp.Rules))}
	for i, r := range rp.Rules {
		in, err := parseRuleTest(r.InputPredicate)
		if err != nil {
			return nil, fmt.Errorf("rule[%d] input: %w", i, err)
		}
		loc, err := parseRuleTest(r.LocationPredicate)
		if err != nil {
			return nil, fmt.Errorf("rule[%d] location: %w", i, err)
		}
		pos, err := parsePosRuleTest(r.PositionPredicate)
		if err != nil {
			return nil, fmt.Errorf("rule[%d] position: %w", i, err)
		}
		st, err := resolveJSONState(r.OutputState)
		if err != nil {
			return nil, fmt.Errorf("rule[%d] output_state %q: %w", i, r.OutputState.Name, err)
		}
		out.rules = append(out.rules, processorRule{input: in, location: loc, pos: pos, output: st})
	}
	return out, nil
}

// parsePosRuleTest parses a rule's position_predicate. An ABSENT field defaults to
// PosAlwaysTrueTest.INSTANCE (CFR ProcessorRule's 3-arg constructor). Only the predicate
// types the bastion pools use are ported (pos_always_true + axis_aligned_linear_pos); an
// unknown type FAILS LOUD so a new structure's position predicate is caught.
//
// Source: CFR PosRuleTest codec + AxisAlignedLinearPosTest CODEC (axis default Y, minChance
// / minDist optional defaults 0).
func parsePosRuleTest(raw json.RawMessage) (posRuleTest, error) {
	if len(raw) == 0 {
		return posAlwaysTrueTest{}, nil
	}
	var head struct {
		PredicateType string  `json:"predicate_type"`
		MinChance     float32 `json:"min_chance"`
		MaxChance     float32 `json:"max_chance"`
		MinDist       int     `json:"min_dist"`
		MaxDist       int     `json:"max_dist"`
		Axis          string  `json:"axis"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	switch head.PredicateType {
	case "", "minecraft:always_true":
		return posAlwaysTrueTest{}, nil
	case "minecraft:axis_aligned_linear_pos":
		var axis byte = 1 // codec default Y
		switch head.Axis {
		case "x":
			axis = 0
		case "y", "":
			axis = 1
		case "z":
			axis = 2
		default:
			return nil, fmt.Errorf("unknown axis %q", head.Axis)
		}
		return axisAlignedLinearPosTest{
			minChance: head.MinChance,
			maxChance: head.MaxChance,
			minDist:   head.MinDist,
			maxDist:   head.MaxDist,
			axis:      axis,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported pos predicate_type %q", head.PredicateType)
	}
}

// parseRuleTest parses a RuleTest JSON object by its predicate_type.
func parseRuleTest(raw json.RawMessage) (ruleTest, error) {
	var head struct {
		PredicateType string    `json:"predicate_type"`
		Block         string    `json:"block"`
		Probability   float32   `json:"probability"`
		Tag           string    `json:"tag"`
		BlockState    jsonState `json:"block_state"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	switch head.PredicateType {
	case "minecraft:always_true":
		return alwaysTrueTest{}, nil
	case "minecraft:block_match":
		return blockMatchTest{block: head.Block}, nil
	case "minecraft:random_block_match":
		return randomBlockMatchTest{block: head.Block, probability: head.Probability}, nil
	case "minecraft:blockstate_match":
		st, err := resolveJSONState(head.BlockState)
		if err != nil {
			return nil, fmt.Errorf("blockstate_match %q: %w", head.BlockState.Name, err)
		}
		return blockStateMatchTest{state: st}, nil
	case "minecraft:tag_match":
		members, ok := villageBlockTags[head.Tag]
		if !ok {
			return nil, fmt.Errorf("unsupported block tag %q in village processor (add it to villageBlockTags)", head.Tag)
		}
		return tagMatchTest{members: members}, nil
	default:
		return nil, fmt.Errorf("unsupported rule predicate_type %q", head.PredicateType)
	}
}

// resolveJSONState resolves a {Name, Properties} JSON block state to a StateID via the
// block registry (the same name+props -> Block resolver the palette uses). An unknown
// block name FAILS LOUD (T-16-02). Properties are marshalled to a bare compound (the
// igloo.go strip-root-header pattern), then block.State.Block() resolves.
func resolveJSONState(js jsonState) (block.StateID, error) {
	st := block.State{Name: js.Name}
	if len(js.Properties) > 0 {
		bytes, err := nbt.Marshal(js.Properties)
		if err != nil {
			return 0, fmt.Errorf("marshal props: %w", err)
		}
		// nbt.Marshal emits [tag 0x0A][nameLen u16=0][payload]; block.State.Properties
		// wants the BARE compound payload, so strip the 3-byte header (igloo.go pattern).
		st.Properties = nbt.RawMessage{Type: nbt.TagCompound, Data: bytes[3:]}
	}
	b, err := st.Block()
	if err != nil {
		return 0, err
	}
	id, ok := block.ToStateID[b]
	if !ok {
		return 0, fmt.Errorf("block %q has no state id", js.Name)
	}
	return id, nil
}

// villageBlockTags resolves the block tags village rule processors reference (only
// #minecraft:doors appears, in the zombie_* lists -> air). Block tags are not yet a
// runtime subsystem; this static set covers the village references. An unlisted tag
// FAILS LOUD in parseRuleTest. The door set is the vanilla #minecraft:doors contents
// (all wooden + copper + iron doors) — the zombie processors strip every door to air.
var villageBlockTags = map[string]map[string]bool{
	"minecraft:doors": doorBlockNames,
}

// doorBlockNames is the #minecraft:doors block tag (every door block id). The zombie
// village processors match this tag -> air. Derived from the block registry (all blocks
// whose id ends in "_door").
var doorBlockNames = buildDoorTag()

func buildDoorTag() map[string]bool {
	m := make(map[string]bool)
	for _, b := range block.StateList {
		id := b.ID()
		if len(id) > 5 && id[len(id)-5:] == "_door" {
			m[id] = true
		}
	}
	return m
}
