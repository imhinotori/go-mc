package placement

// This file ports the BlockPredicate set the FEAT-03 vegetation placed_features use
// (net.minecraft.world.level.levelgen.blockpredicates.BlockPredicate) — the gate
// block_predicate_filter evaluates over a candidate position. Each predicate carries
// an "offset" [dx,dy,dz] (default 0,0,0) applied to the test position, then reads the
// PlacementContext block/fluid there.
//
// The conservative ports (would_survive / solid / replaceable) are documented inline:
// the worldgen Neighborhood does not expose the full BlockBehaviour state-shape the
// jar's wouldSurvive/isSolidRender consult, so each is reduced to a faithful,
// conservative proxy over the StateID the context can read (air vs. not, fluid set
// membership). The set errors LOUDLY on an unported predicate type (T-12-03).
//
// Sources (javap -c, 26.2-inner.jar, package
// net.minecraft.world.level.levelgen.blockpredicates):
//   - MatchingBlockTagPredicate / MatchingBlocksPredicate / WouldSurvivePredicate
//   - AllOfPredicate / AnyOfPredicate / NotPredicate / StateTestingPredicate
//   - ReplaceablePredicate / SolidPredicate / MatchingFluidsPredicate
//
// It is an algorithmic port, NOT a copy of Mojang source.

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// BlockPredicate is the gate block_predicate_filter evaluates. Test reports whether
// the predicate holds at (x,y,z) (the candidate position BEFORE the predicate's own
// offset — each predicate applies its own offset internally). It reads terrain
// through the PlacementContext (GetBlock). 0 rng draws (predicates are positional).
type BlockPredicate interface {
	// Test reports whether the predicate holds at the candidate (x,y,z).
	Test(ctx PlacementContext, x, y, z int) bool
}

// ---- offset wrapper ----

// offset is the [dx,dy,dz] every state-testing predicate carries (default 0,0,0).
type offset struct{ dx, dy, dz int }

// ---- matching_block_tag ----

// matchingBlockTag is MatchingBlockTagPredicate: the block at offset(p) is in the
// resolved block tag's StateID set. The common flower/grass case is tag
// "minecraft:air" (== the block at p is air).
type matchingBlockTag struct {
	off offset
	set map[block.StateID]bool
}

func (p matchingBlockTag) Test(ctx PlacementContext, x, y, z int) bool {
	s := ctx.GetBlock(x+p.off.dx, y+p.off.dy, z+p.off.dz)
	return p.set[s]
}

// ---- matching_blocks ----

// matchingBlocks is MatchingBlocksPredicate: the block at offset(p) is in an explicit
// StateID set (a block id or list of ids, all states).
type matchingBlocks struct {
	off offset
	set map[block.StateID]bool
}

func (p matchingBlocks) Test(ctx PlacementContext, x, y, z int) bool {
	s := ctx.GetBlock(x+p.off.dx, y+p.off.dy, z+p.off.dz)
	return p.set[s]
}

// ---- matching_fluids ----

// matchingFluids is MatchingFluidsPredicate: the FLUID at offset(p) is in a set. The
// Neighborhood reads block states; water/lava blocks ARE the fluids at worldgen time,
// so fluid membership is the block-state-set membership over the fluid blocks.
type matchingFluids struct {
	off offset
	set map[block.StateID]bool
}

func (p matchingFluids) Test(ctx PlacementContext, x, y, z int) bool {
	s := ctx.GetBlock(x+p.off.dx, y+p.off.dy, z+p.off.dz)
	return p.set[s]
}

// ---- solid (a StateTestingPredicate variant) ----

// solidPredicate is SolidPredicate (a StateTestingPredicate): the block at offset(p)
// is solid. CONSERVATIVE PORT: the worldgen context exposes only the StateID, not the
// full isSolidRender material query, so "solid" is reduced to "not air" — faithful for
// the vegetation gate (a flower needs a non-air block below) and never a false keep on
// air. Documented as conservative.
type solidPredicate struct{ off offset }

func (p solidPredicate) Test(ctx PlacementContext, x, y, z int) bool {
	s := ctx.GetBlock(x+p.off.dx, y+p.off.dy, z+p.off.dz)
	return !block.IsAir(s)
}

// ---- replaceable ----

// replaceableBlocks is the conservative "replaceable" set: air + the common plant /
// fluid blocks worldgen overwrites (the #minecraft:replaceable membership the
// vegetation features rely on, resolved constant-for-constant from the jar tag def,
// carver precedent). Kept small + cited.
var replaceableBlockIDs = []string{
	"minecraft:air", "minecraft:cave_air", "minecraft:void_air",
	"minecraft:water", "minecraft:lava",
	"minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern", "minecraft:large_fern",
	"minecraft:dead_bush", "minecraft:snow", "minecraft:vine",
}

// replaceablePredicate is ReplaceablePredicate: the block at offset(p) is replaceable.
// CONSERVATIVE PORT: membership in replaceableBlockIDs (air-or-plant-or-fluid), the
// subset the overworld vegetation features actually meet.
type replaceablePredicate struct {
	off offset
	set map[block.StateID]bool
}

func (p replaceablePredicate) Test(ctx PlacementContext, x, y, z int) bool {
	s := ctx.GetBlock(x+p.off.dx, y+p.off.dy, z+p.off.dz)
	return p.set[s]
}

// ---- would_survive ----

// wouldSurvive is WouldSurvivePredicate: the to-place state could survive at p.
// CONSERVATIVE PORT: the jar calls state.canSurvive(level, pos) which for the
// vegetation set (saplings/trees) reduces to "the block BELOW p is valid VEGETATION
// GROUND" (the #substrate_overworld tag = dirt/grass/podzol/mud/moss families + farmland,
// via VegetationBlock.mayPlaceOn) AND p itself is replaceable. The worldgen context cannot
// run the full canSurvive behaviour, so this gates on: the block directly below (p.y-1) is
// vegetation ground and the block at p is air/replaceable. The to-place state is captured
// for parity with the jar signature but the ground check is the substrate set here.
//
// BUGFIX (trees on trees): the prior port accepted ANY non-air block below as "ground", so a
// tree whose origin landed on another tree's LOG or LEAF column passed the filter and
// generated stacked on top. block.IsVegetationGround excludes logs/leaves, so a tree origin
// over a lower tree is now correctly rejected — matching vanilla, where a sapling cannot
// survive on a log/leaf (state.is(#dirt)-style ground only).
type wouldSurvive struct {
	off offset
}

func (p wouldSurvive) Test(ctx PlacementContext, x, y, z int) bool {
	px, py, pz := x+p.off.dx, y+p.off.dy, z+p.off.dz
	below := ctx.GetBlock(px, py-1, pz)
	here := ctx.GetBlock(px, py, pz)
	return block.IsVegetationGround(below) && block.IsAir(here)
}

// ---- all_of / any_of / not ----

type allOf struct{ subs []BlockPredicate }

func (p allOf) Test(ctx PlacementContext, x, y, z int) bool {
	for _, s := range p.subs {
		if !s.Test(ctx, x, y, z) {
			return false
		}
	}
	return true
}

type anyOf struct{ subs []BlockPredicate }

func (p anyOf) Test(ctx PlacementContext, x, y, z int) bool {
	for _, s := range p.subs {
		if s.Test(ctx, x, y, z) {
			return true
		}
	}
	return false
}

type notPredicate struct{ inner BlockPredicate }

func (p notPredicate) Test(ctx PlacementContext, x, y, z int) bool {
	return !p.inner.Test(ctx, x, y, z)
}

// ---- always true/false (Truepredicate, used as a no-op leaf) ----

type truePredicate struct{}

func (truePredicate) Test(PlacementContext, int, int, int) bool { return true }

// compile-time assertions.
var (
	_ BlockPredicate = matchingBlockTag{}
	_ BlockPredicate = matchingBlocks{}
	_ BlockPredicate = matchingFluids{}
	_ BlockPredicate = solidPredicate{}
	_ BlockPredicate = replaceablePredicate{}
	_ BlockPredicate = wouldSurvive{}
	_ BlockPredicate = allOf{}
	_ BlockPredicate = anyOf{}
	_ BlockPredicate = notPredicate{}
	_ BlockPredicate = truePredicate{}
)

// ---- JSON parsing ----

// jsonPredicate is the union of every predicate envelope's fields.
type jsonPredicate struct {
	Type       string            `json:"type"`
	Offset     []int             `json:"offset"`
	Tag        string            `json:"tag"`        // matching_block_tag
	Blocks     json.RawMessage   `json:"blocks"`     // matching_blocks
	Fluids     json.RawMessage   `json:"fluids"`     // matching_fluids
	State      json.RawMessage   `json:"state"`      // would_survive (the to-place state)
	Predicate  json.RawMessage   `json:"predicate"`  // not
	Predicates []json.RawMessage `json:"predicates"` // all_of / any_of
}

// parseOffset reads the optional [dx,dy,dz] offset (default 0,0,0).
func parseOffset(o []int) (offset, error) {
	if len(o) == 0 {
		return offset{}, nil
	}
	if len(o) != 3 {
		return offset{}, fmt.Errorf("placement: predicate offset must be [dx,dy,dz], got %d ints", len(o))
	}
	return offset{dx: o[0], dy: o[1], dz: o[2]}, nil
}

// ParsePredicate decodes a BlockPredicate envelope by its "type", erroring LOUDLY on
// an unported type (T-12-03 — never a silent keep/drop). It is the entry point
// block_predicate_filter calls on its `predicate` field.
func ParsePredicate(raw json.RawMessage) (BlockPredicate, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("placement: empty block predicate")
	}
	var j jsonPredicate
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("placement: block predicate: %w", err)
	}
	off, err := parseOffset(j.Offset)
	if err != nil {
		return nil, err
	}
	switch stripNSPlacement(j.Type) {
	case "matching_block_tag":
		set, err := resolveBlockTagSet(j.Tag)
		if err != nil {
			return nil, err
		}
		return matchingBlockTag{off: off, set: set}, nil
	case "matching_blocks":
		set, err := blockStateSet(j.Blocks)
		if err != nil {
			return nil, fmt.Errorf("placement: matching_blocks: %w", err)
		}
		return matchingBlocks{off: off, set: set}, nil
	case "matching_fluids":
		set, err := blockStateSet(j.Fluids)
		if err != nil {
			return nil, fmt.Errorf("placement: matching_fluids: %w", err)
		}
		return matchingFluids{off: off, set: set}, nil
	case "solid":
		return solidPredicate{off: off}, nil
	case "replaceable":
		set := idSetToStateSet(replaceableBlockIDs)
		return replaceablePredicate{off: off, set: set}, nil
	case "would_survive":
		return wouldSurvive{off: off}, nil
	case "all_of":
		subs, err := parsePredicateList(j.Predicates)
		if err != nil {
			return nil, fmt.Errorf("placement: all_of: %w", err)
		}
		return allOf{subs: subs}, nil
	case "any_of":
		subs, err := parsePredicateList(j.Predicates)
		if err != nil {
			return nil, fmt.Errorf("placement: any_of: %w", err)
		}
		return anyOf{subs: subs}, nil
	case "not":
		inner, err := ParsePredicate(j.Predicate)
		if err != nil {
			return nil, fmt.Errorf("placement: not: %w", err)
		}
		return notPredicate{inner: inner}, nil
	case "true":
		return truePredicate{}, nil
	default:
		return nil, fmt.Errorf("placement: unported block predicate type %q "+
			"(add it jar-exact from the blockpredicates package — never silently keep/drop)", j.Type)
	}
}

// parsePredicateList parses a JSON array of predicates.
func parsePredicateList(raws []json.RawMessage) ([]BlockPredicate, error) {
	out := make([]BlockPredicate, 0, len(raws))
	for _, r := range raws {
		p, err := ParsePredicate(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// stripNSPlacement strips a "minecraft:" namespace (placement-local copy so this file
// is self-contained — mirrors the feature/density stripNS).
func stripNSPlacement(t string) string {
	for i := 0; i < len(t); i++ {
		if t[i] == ':' {
			return t[i+1:]
		}
	}
	return t
}

// blockStateSet resolves a "blocks"/"fluids" field (single id or array) to the set of
// all member state ids.
func blockStateSet(raw json.RawMessage) (map[block.StateID]bool, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing blocks/fluids")
	}
	var ids []string
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		ids = []string{one}
	} else if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("decoding blocks/fluids: %w", err)
	}
	return idSetToStateSet(ids), nil
}

// idSetToStateSet expands a slice of block ids to the set of ALL their state ids.
func idSetToStateSet(ids []string) map[block.StateID]bool {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if want[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set
}

// predicateBlockTags maps the #block tags block_predicate_filter predicates reference
// to their member ids, resolved constant-for-constant from the jar tag defs (carver
// Replaceables precedent). "minecraft:air" is the dominant flower/grass case. Kept
// small + cited; an unlisted tag errors loudly.
var predicateBlockTags = map[string][]string{
	"minecraft:dirt": {
		"minecraft:dirt", "minecraft:grass_block", "minecraft:podzol",
		"minecraft:coarse_dirt", "minecraft:mycelium", "minecraft:rooted_dirt",
		"minecraft:moss_block", "minecraft:mud", "minecraft:muddy_mangrove_roots",
	},
	"minecraft:sand": {"minecraft:sand", "minecraft:red_sand"},
	// #minecraft:forest_rock_can_place_on = #substrate_overworld + #base_stone_overworld
	// (block_blob forest_rock's can_place_on). Flattened constant-for-constant from the jar
	// tag defs (data/minecraft/tags/block/*.json, 26.2):
	//   substrate_overworld = #dirt + #mud + #moss_blocks + #grass_blocks
	//   base_stone_overworld = stone/granite/diorite/andesite/tuff/deepslate
	"minecraft:forest_rock_can_place_on": {
		// #dirt
		"minecraft:dirt", "minecraft:coarse_dirt", "minecraft:rooted_dirt",
		// #mud
		"minecraft:mud", "minecraft:muddy_mangrove_roots",
		// #moss_blocks
		"minecraft:moss_block", "minecraft:pale_moss_block",
		// #grass_blocks
		"minecraft:grass_block", "minecraft:podzol", "minecraft:mycelium",
		// #base_stone_overworld
		"minecraft:stone", "minecraft:granite", "minecraft:diorite",
		"minecraft:andesite", "minecraft:tuff", "minecraft:deepslate",
	},
}

// resolveBlockTagSet resolves a #block tag id to the set of member state ids. Air is
// the air block ids; a handful of common tags resolve via the small predicateBlockTags
// map; anything else resolves from the AUTHORITATIVE embedded jar tag JSONs via
// data.BlockTag (recursive nested-tag expansion), so the membership matches vanilla
// exactly. Only a tag JSON that is genuinely absent from the embedded data errors loudly.
func resolveBlockTagSet(tag string) (map[block.StateID]bool, error) {
	if tag == "" {
		return nil, fmt.Errorf("placement: matching_block_tag missing tag")
	}
	switch tag {
	case "minecraft:air":
		return idSetToStateSet([]string{"minecraft:air", "minecraft:cave_air", "minecraft:void_air"}), nil
	default:
		if members, ok := predicateBlockTags[tag]; ok {
			return idSetToStateSet(members), nil
		}
		// Fall back to the embedded jar tag data (data.BlockTag resolves the id — with or
		// without the "minecraft:" prefix — to its flat block-id set, recursively expanding
		// nested "#..." references). This is the same authoritative tag data the feature
		// bodies use, so root_system's allowed_tree_position (#replaceable_by_trees /
		// #azalea_grows_on) and any other real tag resolves correctly.
		ids, err := data.BlockTag(tag)
		if err != nil {
			return nil, fmt.Errorf("placement: unported block tag %q in predicate "+
				"(no hardcoded members and not in embedded tag data): %w", tag, err)
		}
		set := map[block.StateID]bool{}
		for sid, b := range block.StateList {
			if ids[b.ID()] {
				set[block.StateID(sid)] = true
			}
		}
		return set, nil
	}
}
