package block

// strip.go -- AxeItem.STRIPPABLES, the log/wood/stem/hyphae -> stripped-variant map, rebuilt
// constant-for-constant from the unobfuscated 26.2 jar (net.minecraft.world.item.AxeItem.STRIPPABLES,
// an ImmutableMap.Builder of 23 source->stripped Block pairs).
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p net.minecraft.world.item.AxeItem static {}):
//   OAK_WOOD->STRIPPED_OAK_WOOD, OAK_LOG->STRIPPED_OAK_LOG, DARK_OAK_WOOD->STRIPPED_DARK_OAK_WOOD,
//   DARK_OAK_LOG->STRIPPED_DARK_OAK_LOG, PALE_OAK_WOOD->STRIPPED_PALE_OAK_WOOD,
//   PALE_OAK_LOG->STRIPPED_PALE_OAK_LOG, ACACIA_WOOD->..., ACACIA_LOG->..., CHERRY_WOOD/LOG->...,
//   BIRCH_WOOD/LOG->..., JUNGLE_WOOD/LOG->..., SPRUCE_WOOD/LOG->..., WARPED_STEM->STRIPPED_WARPED_STEM,
//   WARPED_HYPHAE->STRIPPED_WARPED_HYPHAE, CRIMSON_STEM/HYPHAE->..., MANGROVE_WOOD/LOG->...,
//   BAMBOO_BLOCK->STRIPPED_BAMBOO_BLOCK.
//
// AxeItem.getStripped(state): STRIPPABLES.get(state.getBlock()).map(b ->
//   b.defaultBlockState().setValue(RotatedPillarBlock.AXIS, state.getValue(AXIS))). So the stripped
// state is the target block's default carrying the SOURCE state's AXIS -- exactly Block.withPropertiesOf
// restricted to AXIS (every strippable + its stripped variant share the AXIS property). Reuse the
// reflective copperWithPropertiesOf (== Block.withPropertiesOf) so the AXIS (and any other shared
// property) transfers 1:1.

// strippableByBlockID maps a strippable block id ("minecraft:...") to its stripped-variant block id.
var strippableByBlockID = map[string]string{
	"minecraft:oak_wood":       "minecraft:stripped_oak_wood",
	"minecraft:oak_log":        "minecraft:stripped_oak_log",
	"minecraft:dark_oak_wood":  "minecraft:stripped_dark_oak_wood",
	"minecraft:dark_oak_log":   "minecraft:stripped_dark_oak_log",
	"minecraft:pale_oak_wood":  "minecraft:stripped_pale_oak_wood",
	"minecraft:pale_oak_log":   "minecraft:stripped_pale_oak_log",
	"minecraft:acacia_wood":    "minecraft:stripped_acacia_wood",
	"minecraft:acacia_log":     "minecraft:stripped_acacia_log",
	"minecraft:cherry_wood":    "minecraft:stripped_cherry_wood",
	"minecraft:cherry_log":     "minecraft:stripped_cherry_log",
	"minecraft:birch_wood":     "minecraft:stripped_birch_wood",
	"minecraft:birch_log":      "minecraft:stripped_birch_log",
	"minecraft:jungle_wood":    "minecraft:stripped_jungle_wood",
	"minecraft:jungle_log":     "minecraft:stripped_jungle_log",
	"minecraft:spruce_wood":    "minecraft:stripped_spruce_wood",
	"minecraft:spruce_log":     "minecraft:stripped_spruce_log",
	"minecraft:warped_stem":    "minecraft:stripped_warped_stem",
	"minecraft:warped_hyphae":  "minecraft:stripped_warped_hyphae",
	"minecraft:crimson_stem":   "minecraft:stripped_crimson_stem",
	"minecraft:crimson_hyphae": "minecraft:stripped_crimson_hyphae",
	"minecraft:mangrove_wood":  "minecraft:stripped_mangrove_wood",
	"minecraft:mangrove_log":   "minecraft:stripped_mangrove_log",
	"minecraft:bamboo_block":   "minecraft:stripped_bamboo_block",
}

// StrippedState is AxeItem.getStripped(state): the stripped variant of a log/wood/stem/hyphae/bamboo
// block, carrying the source state's AXIS (and any other shared property). Returns ok=false for a
// non-strippable block (STRIPPABLES.get == null -> Optional.empty). CITE: AxeItem.getStripped.
func StrippedState(s StateID) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	strippedID, ok := strippableByBlockID[StateList[s].ID()]
	if !ok {
		return s, false // not strippable
	}
	strippedDefault, ok := DefaultStateID[strippedID]
	if !ok {
		return s, false
	}
	// b.defaultBlockState().setValue(AXIS, state.getValue(AXIS)) == withPropertiesOf(state).
	return copperWithPropertiesOf(strippedDefault, s)
}
