package block

import "reflect"

// copperTiers is the WeatheringCopper.NEXT_BY_BLOCK BiMap, rebuilt constant-for-constant from the
// jar's copper families. Each inner slice is one family's four weather tiers in oxidation order:
// [UNAFFECTED, EXPOSED, WEATHERED, OXIDIZED]. NEXT_BY_BLOCK maps tier i -> tier i+1 (OXIDIZED has no
// next). Only the WAXABLE-UNWAXED families oxidize; the waxed_* copies are absent (waxing removes the
// ChangeOverTimeBlock behavior). The `copper` family is the one irregular naming case: its UNAFFECTED
// member is `copper_block` (not `copper`). CITE: WeatheringCopper.NEXT_BY_BLOCK (progressMapping over
// COPPER_BLOCK, CUT_COPPER, CHISELED_COPPER, CUT_COPPER_SLAB, CUT_COPPER_STAIRS, COPPER_DOOR,
// COPPER_TRAPDOOR, COPPER_BARS, COPPER_GRATE, COPPER_BULB, COPPER_LANTERN, COPPER_CHEST,
// COPPER_GOLEM_STATUE, LIGHTNING_ROD, COPPER_CHAIN).
var copperTiers = [][4]string{
	{"minecraft:copper_block", "minecraft:exposed_copper", "minecraft:weathered_copper", "minecraft:oxidized_copper"},
	{"minecraft:cut_copper", "minecraft:exposed_cut_copper", "minecraft:weathered_cut_copper", "minecraft:oxidized_cut_copper"},
	{"minecraft:cut_copper_slab", "minecraft:exposed_cut_copper_slab", "minecraft:weathered_cut_copper_slab", "minecraft:oxidized_cut_copper_slab"},
	{"minecraft:cut_copper_stairs", "minecraft:exposed_cut_copper_stairs", "minecraft:weathered_cut_copper_stairs", "minecraft:oxidized_cut_copper_stairs"},
	{"minecraft:chiseled_copper", "minecraft:exposed_chiseled_copper", "minecraft:weathered_chiseled_copper", "minecraft:oxidized_chiseled_copper"},
	{"minecraft:copper_bars", "minecraft:exposed_copper_bars", "minecraft:weathered_copper_bars", "minecraft:oxidized_copper_bars"},
	{"minecraft:copper_bulb", "minecraft:exposed_copper_bulb", "minecraft:weathered_copper_bulb", "minecraft:oxidized_copper_bulb"},
	{"minecraft:copper_chain", "minecraft:exposed_copper_chain", "minecraft:weathered_copper_chain", "minecraft:oxidized_copper_chain"},
	{"minecraft:copper_chest", "minecraft:exposed_copper_chest", "minecraft:weathered_copper_chest", "minecraft:oxidized_copper_chest"},
	{"minecraft:copper_door", "minecraft:exposed_copper_door", "minecraft:weathered_copper_door", "minecraft:oxidized_copper_door"},
	{"minecraft:copper_golem_statue", "minecraft:exposed_copper_golem_statue", "minecraft:weathered_copper_golem_statue", "minecraft:oxidized_copper_golem_statue"},
	{"minecraft:copper_grate", "minecraft:exposed_copper_grate", "minecraft:weathered_copper_grate", "minecraft:oxidized_copper_grate"},
	{"minecraft:copper_lantern", "minecraft:exposed_copper_lantern", "minecraft:weathered_copper_lantern", "minecraft:oxidized_copper_lantern"},
	{"minecraft:copper_trapdoor", "minecraft:exposed_copper_trapdoor", "minecraft:weathered_copper_trapdoor", "minecraft:oxidized_copper_trapdoor"},
	{"minecraft:lightning_rod", "minecraft:exposed_lightning_rod", "minecraft:weathered_lightning_rod", "minecraft:oxidized_lightning_rod"},
}

// copperWeatherByBlockID maps a copper block id -> its WeatherState ordinal (0..3:
// UNAFFECTED/EXPOSED/WEATHERED/OXIDIZED). copperNextByBlockID maps a copper block id -> the next
// (more-oxidized) block id (absent for OXIDIZED). Both are built from copperTiers at init.
var (
	copperWeatherByBlockID = map[string]int{}
	copperNextByBlockID    = map[string]string{}
	copperPrevByBlockID    = map[string]string{}
)

func init() {
	for _, tier := range copperTiers {
		for i, id := range tier {
			copperWeatherByBlockID[id] = i
			if i < 3 {
				copperNextByBlockID[id] = tier[i+1]
			}
			if i > 0 {
				copperPrevByBlockID[id] = tier[i-1]
			}
		}
	}
}

// CopperGetPrevious is WeatheringCopper.getPrevious(state): the LESS-oxidized (one tier down) copper
// state, carrying the source state's shared properties (withPropertiesOf). ok=false for an UNAFFECTED
// tier (no previous) or a non-weathering-copper block. This is the AxeItem SCRAPE target. CITE:
// WeatheringCopper.getPrevious (PREVIOUS_BY_BLOCK, the inverse of NEXT_BY_BLOCK).
func CopperGetPrevious(s StateID) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	prevID, ok := copperPrevByBlockID[StateList[s].ID()]
	if !ok {
		return s, false // UNAFFECTED (no previous) or not copper
	}
	prevDefault, ok := DefaultStateID[prevID]
	if !ok {
		return s, false
	}
	return copperWithPropertiesOf(prevDefault, s)
}

// IsWeatheringCopper reports whether s is an UNWAXED weathering-copper block (a ChangeOverTimeBlock
// implementor). Waxed copper is NOT a ChangeOverTimeBlock and never oxidizes. CITE:
// WeatheringCopperFullBlock/StairBlock/SlabBlock/... implements WeatheringCopper; waxed variants do not.
func IsWeatheringCopper(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := copperWeatherByBlockID[StateList[s].ID()]
	return ok
}

// CopperWeatherState is the WeatherState ordinal (0..3) of a weathering-copper state, or -1 for a
// non-copper. This is getAge().ordinal() in getNextState. CITE: ChangeOverTimeBlock.getNextState
// (getAge().ordinal()); WeatheringCopper$WeatherState (UNAFFECTED=0..OXIDIZED=3).
func CopperWeatherState(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if o, ok := copperWeatherByBlockID[StateList[s].ID()]; ok {
		return o
	}
	return -1
}

// CopperUnaffected reports getChanceModifier() == 0.75f (UNAFFECTED, ordinal 0) vs 1.0f (else).
// CITE: WeatheringCopper.getChanceModifier (UNAFFECTED ? 0.75 : 1.0).
func CopperChanceModifier(s StateID) float32 {
	if CopperWeatherState(s) == 0 {
		return 0.75
	}
	return 1.0
}

// CopperGetNext is WeatheringCopper.getNext(state) == NEXT_BY_BLOCK.get(block).map(nb ->
// nb.withPropertiesOf(state)): resolve the next (more-oxidized) block and copy all shared properties
// from the source state. ok=false when there is no next (OXIDIZED) or s is not weathering copper.
// CITE: WeatheringCopper.getNext; Block.withPropertiesOf.
func CopperGetNext(s StateID) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	nextID, ok := copperNextByBlockID[StateList[s].ID()]
	if !ok {
		return s, false // OXIDIZED (no next) or not copper
	}
	nextDefault, ok := DefaultStateID[nextID]
	if !ok {
		return s, false
	}
	return copperWithPropertiesOf(nextDefault, s)
}

// copperWithPropertiesOf is Block.withPropertiesOf(state): return the target block's state carrying
// every property (struct field) it SHARES (by name + kind) with the source state. Because each
// weather tier of a copper family is the SAME shape (a door tier is a door, a slab tier is a slab,
// etc.), all properties transfer 1:1 -- FACING/HALF/HINGE/OPEN/POWERED (doors), TYPE/WATERLOGGED
// (slabs), FACING/HALF/SHAPE/WATERLOGGED (stairs), LIT (bulbs), etc. Read reflectively so every
// waterloggable/directional copper variant is covered without enumerating them. CITE:
// Block.withPropertiesOf (copies each shared Property value onto the target block's default state).
func copperWithPropertiesOf(target, src StateID) (StateID, bool) {
	tb := StateList[target]
	sv := reflect.ValueOf(StateList[src])
	if sv.Kind() != reflect.Struct {
		return target, true
	}
	out := reflect.New(reflect.TypeOf(tb)).Elem()
	out.Set(reflect.ValueOf(tb))
	if out.Kind() != reflect.Struct {
		return target, true
	}
	for i := 0; i < out.NumField(); i++ {
		f := out.Type().Field(i)
		sf := sv.FieldByName(f.Name)
		if !sf.IsValid() || sf.Kind() != out.Field(i).Kind() {
			continue
		}
		if !out.Field(i).CanSet() {
			continue
		}
		out.Field(i).Set(sf)
	}
	id, ok := ToStateID[out.Interface().(Block)]
	return id, ok
}

// CopperCanOxidize reports WeatheringCopperFullBlock.isRandomlyTicking(state) ==
// WeatheringCopper.getNext(block).isPresent(): true iff the block has a more-oxidized next tier (so
// an OXIDIZED copper block is NOT randomly ticked). This is the exact per-state random-tick flag for
// the weathering-copper family. CITE: WeatheringCopperFullBlock/StairBlock/SlabBlock.isRandomlyTicking.
func CopperCanOxidize(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := copperNextByBlockID[StateList[s].ID()]
	return ok
}
