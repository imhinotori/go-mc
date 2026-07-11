package block

// shulker.go -- the block-package half of the SHULKER BOX port: per-state predicates + FACING
// accessor the server-side shulker logic (server/shulker_box_be.go) reads. Every branch is a literal
// 1:1 read of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar) block-state shape.
//
// CITE (classes):
//   net.minecraft.world.level.block.ShulkerBoxBlock (FACING = DirectionalBlock.FACING; a per-DyeColor
//   Block singleton, plus the undyed minecraft:shulker_box). Each color is a distinct Block, so each is
//   a distinct Go struct here.

// IsShulkerBox reports whether a state id is any shulker box (undyed or any of the 16 dyed variants),
// in any FACING. CITE: ShulkerBoxBlock (one Block per color + the undyed base).
func IsShulkerBox(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case ShulkerBox, WhiteShulkerBox, OrangeShulkerBox, MagentaShulkerBox, LightBlueShulkerBox,
		YellowShulkerBox, LimeShulkerBox, PinkShulkerBox, GrayShulkerBox, LightGrayShulkerBox,
		CyanShulkerBox, PurpleShulkerBox, BlueShulkerBox, BrownShulkerBox, GreenShulkerBox,
		RedShulkerBox, BlackShulkerBox:
		return true
	default:
		return false
	}
}

// ShulkerBoxFacing returns the FACING of a shulker box (the direction its lid opens toward), or
// (Up, false) if not a shulker box. The vanilla default is Direction.UP. CITE: ShulkerBoxBlock.FACING
// (DirectionalBlock.FACING); registerDefaultState(... FACING, Direction.UP).
func ShulkerBoxFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Up, false
	}
	switch b := StateList[s].(type) {
	case ShulkerBox:
		return b.Facing, true
	case WhiteShulkerBox:
		return b.Facing, true
	case OrangeShulkerBox:
		return b.Facing, true
	case MagentaShulkerBox:
		return b.Facing, true
	case LightBlueShulkerBox:
		return b.Facing, true
	case YellowShulkerBox:
		return b.Facing, true
	case LimeShulkerBox:
		return b.Facing, true
	case PinkShulkerBox:
		return b.Facing, true
	case GrayShulkerBox:
		return b.Facing, true
	case LightGrayShulkerBox:
		return b.Facing, true
	case CyanShulkerBox:
		return b.Facing, true
	case PurpleShulkerBox:
		return b.Facing, true
	case BlueShulkerBox:
		return b.Facing, true
	case BrownShulkerBox:
		return b.Facing, true
	case GreenShulkerBox:
		return b.Facing, true
	case RedShulkerBox:
		return b.Facing, true
	case BlackShulkerBox:
		return b.Facing, true
	default:
		return Up, false
	}
}

// IsEnderChest reports whether a state id is an ender chest (any FACING/WATERLOGGED). CITE:
// EnderChestBlock.
func IsEnderChest(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(EnderChest)
	return ok
}
