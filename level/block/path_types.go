package block

import "strings"

// path_types.go -- block-family predicates used by the server pathfinder (WalkNodeEvaluator
// getPathTypeFromState / isBurningBlock port). Each is the 1:1 discriminator the jar uses:
// an instanceof / Blocks.X identity / tag membership, re-expressed as a StateID predicate.
// CITE per function.

// IsCactus reports Blocks.CACTUS (getPathTypeFromState -> DAMAGING). CITE: WalkNodeEvaluator.
func IsCactus(s StateID) bool { return isType[Cactus](s) }

// IsSweetBerryBush reports Blocks.SWEET_BERRY_BUSH (-> DAMAGING). CITE: WalkNodeEvaluator.
func IsSweetBerryBush(s StateID) bool { return isType[SweetBerryBush](s) }

// IsHoneyBlock reports Blocks.HONEY_BLOCK (-> STICKY_HONEY). CITE: WalkNodeEvaluator.
func IsHoneyBlock(s StateID) bool { return isType[HoneyBlock](s) }

// IsCocoa reports Blocks.COCOA (-> COCOA). CITE: WalkNodeEvaluator.
func IsCocoa(s StateID) bool { return isType[Cocoa](s) }

// IsWitherRose reports Blocks.WITHER_ROSE (-> DAMAGE_CAUTIOUS). CITE: WalkNodeEvaluator.
func IsWitherRose(s StateID) bool { return isType[WitherRose](s) }

// IsPowderSnow reports Blocks.POWDER_SNOW (-> POWDER_SNOW). CITE: WalkNodeEvaluator.
func IsPowderSnow(s StateID) bool { return isType[PowderSnow](s) }

// IsBigDripleaf reports Blocks.BIG_DRIPLEAF (-> TRAPDOOR). CITE: WalkNodeEvaluator.
func IsBigDripleaf(s StateID) bool { return isType[BigDripleaf](s) }

// IsMagmaBlock reports Blocks.MAGMA_BLOCK (isBurningBlock). CITE: NodeEvaluator.isBurningBlock.
func IsMagmaBlock(s StateID) bool { return isType[MagmaBlock](s) }

// IsLavaCauldron reports Blocks.LAVA_CAULDRON (isBurningBlock). CITE: NodeEvaluator.isBurningBlock.
func IsLavaCauldron(s StateID) bool { return isType[LavaCauldron](s) }

// IsSpeleothem reports BlockTags.SPELEOTHEMS membership. The tag content is the dripstone family
// (pointed_dripstone); classified DAMAGE_CAUTIOUS. CITE: WalkNodeEvaluator (BlockTags.SPELEOTHEMS).
func IsSpeleothem(s StateID) bool {
	return isType[PointedDripstone](s) || isType[DripstoneBlock](s)
}

// IsLitCampfire ports CampfireBlock.isLitCampfire(state): a Campfire/SoulCampfire with LIT=true.
// CITE: CampfireBlock.isLitCampfire (state.getBlock() instanceof CampfireBlock && state.getValue(LIT)).
func IsLitCampfire(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case Campfire:
		return bool(b.Lit)
	case SoulCampfire:
		return bool(b.Lit)
	}
	return false
}

// IsFence reports BlockTags.FENCES membership. In vanilla the tag is every FenceBlock (ID suffix
// "_fence") EXCLUDING fence gates (a separate FenceGateBlock, its own getPathTypeFromState branch).
// CITE: BlockTags.FENCES / WalkNodeEvaluator (state.is(BlockTags.FENCES)).
func IsFence(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	id := StateList[s].ID()
	return strings.HasSuffix(id, "_fence") && !strings.HasSuffix(id, "_fence_gate")
}

// IsWall reports BlockTags.WALLS membership (ID suffix "_wall"), excluding the wall-mounted sign
// family ("_wall_sign" / "_wall_hanging_sign") which are not WallBlocks. CITE: BlockTags.WALLS.
func IsWall(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	id := StateList[s].ID()
	return strings.HasSuffix(id, "_wall") && !strings.HasSuffix(id, "_wall_sign") &&
		!strings.HasSuffix(id, "_wall_hanging_sign")
}

// isType is the shared instanceof discriminator: does the state id resolve to concrete block type T.
func isType[T Block](s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(T)
	return ok
}
