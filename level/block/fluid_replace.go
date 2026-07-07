package block

import "strings"

// fluid_replace.go ports the FlowingFluid replaceability predicate that decides which blocks a
// flowing fluid DESTROYS (drops + flows through) versus which STOP it. Every branch is a literal
// 1:1 copy of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), read via javap -c -p this
// session. No GPL paste - the vanilla control flow re-expressed in idiomatic Go, cited per method.

// blocksMotion ports BlockBehaviour$BlockStateBase.blocksMotion() 1:1. VERIFIED bytecode:
//
//	Block b = getBlock();
//	return b != Blocks.COBWEB && b != Blocks.BAMBOO_SAPLING && isSolid();
//
// isSolid() == the legacySolid field, already extracted as bit 18 of the block-support table
// (block.IsSolid). COBWEB and BAMBOO_SAPLING are the two solid-but-non-blocking exceptions.
// CITE: net.minecraft.world.level.block.state.BlockBehaviour$BlockStateBase.blocksMotion.
func blocksMotion(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Cobweb, BambooSapling:
		return false
	}
	return IsSolid(s)
}

// CanHoldAnyFluid ports FlowingFluid.canHoldAnyFluid(BlockState) 1:1 - the type-only replaceability
// gate that FlowingFluid.canMaybePassThrough consults to decide whether a flowing fluid may enter a
// cell (and, in spreadTo, destroy the block that is there). VERIFIED bytecode (26.2 jar):
//
//	Block b = state.getBlock();
//	if (b instanceof LiquidBlockContainer) return true;              // waterloggable container
//	if (state.blocksMotion())            return false;               // a solid wall stops fluid
//	return !(b instanceof DoorBlock)
//	    && !state.is(BlockTags.SIGNS)
//	    && !state.is(Blocks.LADDER)
//	    && !state.is(Blocks.SUGAR_CANE)
//	    && !state.is(Blocks.BUBBLE_COLUMN)
//	    && !state.is(Blocks.NETHER_PORTAL)
//	    && !state.is(Blocks.END_PORTAL)
//	    && !state.is(Blocks.END_GATEWAY)
//	    && !state.is(Blocks.STRUCTURE_VOID);
//
// So a REPLACEABLE non-solid (tall grass, flowers, torches, redstone dust, snow layer, etc.) returns
// true - the fluid destroys it and flows in. A solid block, a waterloggable container, and the
// explicit non-solid exceptions above return the container/false result. The LiquidBlockContainer
// branch is TRUE here (canHoldAnyFluid); whether the SPECIFIC fluid fits is the separate
// canHoldSpecificFluid / LiquidBlockContainer.canPlaceLiquid check, handled by the caller.
// CITE: net.minecraft.world.level.material.FlowingFluid.canHoldAnyFluid.
func CanHoldAnyFluid(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if IsLiquidBlockContainer(s) {
		return true
	}
	if blocksMotion(s) {
		return false
	}
	if isDoorBlock(s) {
		return false
	}
	id := StateList[s].ID()
	if isSignsTag(id) {
		return false
	}
	switch id {
	case "minecraft:ladder",
		"minecraft:sugar_cane",
		"minecraft:bubble_column",
		"minecraft:nether_portal",
		"minecraft:end_portal",
		"minecraft:end_gateway",
		"minecraft:structure_void":
		return false
	}
	return true
}

// isDoorBlock reports `block instanceof DoorBlock` - the tall (two-tall) door blocks. TrapDoorBlock
// does NOT extend DoorBlock, so a "_trapdoor" is excluded. DoorBlock ids are exactly the "*_door"
// blocks (wooden + iron + copper family). CITE: net.minecraft.world.level.block.DoorBlock.
func isDoorBlock(s StateID) bool {
	id := StateList[s].ID()
	return strings.HasSuffix(id, "_door") && !strings.HasSuffix(id, "_trapdoor")
}

// isSignsTag reports state.is(BlockTags.SIGNS). SIGNS = #standing_signs + #wall_signs (26.2 datagen
// tags/block/signs.json), i.e. every "*_sign" and "*_wall_sign" but NOT the hanging-sign blocks
// (those live in ALL_HANGING_SIGNS, a separate tag not referenced by canHoldAnyFluid). So: ends with
// "_sign" AND does not contain "hanging". CITE: net.minecraft.tags.BlockTags.SIGNS.
func isSignsTag(id string) bool {
	return strings.HasSuffix(id, "_sign") && !strings.Contains(id, "hanging")
}

// IsLiquidBlockContainer reports `block instanceof LiquidBlockContainer` - a block that manages its
// own fluid content (waterloggable blocks: slabs/stairs/fences/... carrying a `waterlogged`
// property, plus special cases). The vanilla interface is implemented by SimpleWaterloggedBlock
// (every block with the WATERLOGGED property) and a handful of bespoke blocks. Sulfur has no
// waterlogging simulation yet, so a container cell is NOT freely destroyed by a passing fluid
// (spreadTo routes it to the container's placeLiquid, which for the unbuilt waterlog engine is a
// documented no-op - the cell keeps its current behavior, per scope). Detected via the generated
// `Waterlogged` bool field carried by the block state's Go struct. CITE:
// net.minecraft.world.level.block.LiquidBlockContainer / SimpleWaterloggedBlock.
func IsLiquidBlockContainer(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	return hasBoolField(StateList[s], "Waterlogged")
}
