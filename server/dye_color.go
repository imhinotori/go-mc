package server

// dye_color.go — the DyeColor id resolution the Cat.mobInteract collar-dye branch needs (MOB-NEUT-03):
// the Go analog of `stack.get(DataComponents.DYE)` for a dye ItemStack. A vanilla DyeItem carries a
// default DataComponents.DYE component whose value is the item's DyeColor; the generated item table
// registers the 16 dyes CONTIGUOUSLY in DyeColor id order, so the color id is a fixed offset from the
// item id. 1:1 with the jar (DyeColor enum + ItemTags.CAT_COLLAR_DYES == #minecraft:dyes).
//
//	[VERIFIED javap DyeColor: WHITE(0),ORANGE(1),MAGENTA(2),LIGHT_BLUE(3),YELLOW(4),LIME(5),PINK(6),
//	 GRAY(7),LIGHT_GRAY(8),CYAN(9),PURPLE(10),BLUE(11),BROWN(12),GREEN(13),RED(14),BLACK(15). DyeItem
//	 carries DataComponents.DYE = its DyeColor (DyeItem.interactLivingEntity: itemStack.get(DYE)).
//	 data/item: white_dye..black_dye = ids 1095..1110, same order as the DyeColor enum.]

import "github.com/imhinotori/sulfur/data/item"

// catDefaultCollarColor is Cat.DEFAULT_COLLAR_COLOR.getId() == DyeColor.RED.getId() == 14 — the
// define(DATA_COLLAR_COLOR, ...) default a fresh cat carries.
//
//	[VERIFIED javap Cat static{}: DEFAULT_COLLAR_COLOR = DyeColor.RED; DyeColor.RED id == 14.]
const catDefaultCollarColor = 14 // DyeColor.RED

// dyeColorIDOf ports `stack.get(DataComponents.DYE)` reduced to the DyeColor id for a dye ItemStack:
// the 16 dye items (white_dye..black_dye) map to their DyeColor id (WHITE 0 .. BLACK 15) by their
// contiguous item-id offset from white_dye. Returns (id, true) for a dye item, (0, false) for any
// non-dye item (the collar-dye branch treats a false as "no DYE component == null"). This is exact
// because the generated item table lists the dyes in DyeColor order with no gaps.
func dyeColorIDOf(itemID int32) (int, bool) {
	first := int32(item.WhiteDye.ID) // 1095 (DyeColor.WHITE)
	last := int32(item.BlackDye.ID)  // 1110 (DyeColor.BLACK)
	if itemID < first || itemID > last {
		return 0, false
	}
	return int(itemID - first), true
}
