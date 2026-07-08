package server

// loom_menu.go -- the LOOM block + its banner-pattern APPLY menu (LoomMenu). A sibling of the
// grindstone/stonecutter transient menus: three INPUT slots (banner 0, dye 1, pattern 2), a virtual
// take-only RESULT slot (the layered banner recomputed from the inputs + the selected pattern), a
// DataSlot (selectedBannerPatternIndex), and a button-click that PICKS one of the selectable patterns.
// It clones the container-menu scaffolding (openContainer.kind, nextContainerCounter, openScreen,
// containerSetContent, containerSetData, the close-returns-inputs) exactly like grindstone_menu.go +
// stonecutter_menu.go.
//
// 1:1 jar chain (temp/cache/26.2-inner.jar, javap this session, cited per function):
//
//	LoomBlock.useWithoutItem: !clientSide -> player.openMenu(getMenuProvider(...)); return SUCCESS. // CONSUMES -> no place
//	LoomMenu ctor: DataSlot selectedBannerPatternIndex (standalone, -1); inputContainer size 3
//	  addSlot(bannerSlot=0, mayPlace = BannerItem), addSlot(dyeSlot=1, mayPlace = isDyeItem),
//	  addSlot(patternSlot=2, mayPlace = isPatternItem); outputContainer size 1 addSlot(resultSlot=0,
//	  mayPlace=false take-only via LoomMenu6.onTake); addStandardInventorySlots; addDataSlot(index).
//	LoomMenu.slotsChanged: recompute selectablePatterns + reconcile the selected index, then
//	  setupResultSlot (or clear); broadcastChanges. (the full algorithm, ported below.)
//	LoomMenu.clickMenuButton(player, buttonId): if 0<=buttonId<selectablePatterns.size {
//	  selectedBannerPatternIndex.set(buttonId); setupResultSlot(selectablePatterns[buttonId]); true; } else false.
//	LoomMenu.setupResultSlot(holder): if !banner.isEmpty && !dye.isEmpty && (dyeColor=dye.get(DYE))!=null
//	  { result = banner.copyWithCount(1); result.update(BANNER_PATTERNS, EMPTY, layers ->
//	  Builder().addAll(layers).add(holder, dyeColor).build()); } else result = EMPTY.
//	LoomMenu.getSelectablePatterns(pattern): empty -> NO_ITEM_REQUIRED patterns; else the pattern item
//	  PROVIDES_BANNER_PATTERNS set (or empty).
//	LoomMenu6.onTake: bannerSlot.remove(1); dyeSlot.remove(1); if (!banner.hasItem || !dye.hasItem)
//	  selectedBannerPatternIndex.set(-1); play take-sound.
//	LoomMenu.removed(player): access.execute((lvl,pos) -> clearContainer(player, inputContainer)).
//
// v1 subset (cited): no stats/sound/spectator, so awardStat + the UI_LOOM_TAKE_RESULT take-sound + the
// spectator branch are faithful no-ops; the ContainerLevelAccess reach re-check folds into the open-time
// reach gate. The BannerPattern holder is carried as its registry protocol-id (see loomPatternIDs), which
// the client receives (ClientboundConfigRegistryData, alphabetical order = protocol_id) as the id it
// renders -- the banner_patterns component PatternType is that registry ref (index+1; 0=inline).

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// loomMenuSize is the loom-window slot count: 3 inputs (banner 0, dye 1, pattern 2) + 1 result (3) +
// 27 main + 9 hotbar = 40. (LoomMenu: banner 0, dye 1, pattern 2, result 3, INV 4..30, USE_ROW 31..39.)
// Verified the LoomMenu ctor bytecode (addSlot banner/dye/pattern/result then addStandardInventorySlots).
const loomMenuSize = 3 + 1 + 27 + 9 // 40

// loomTitle is the loom window display name. Vanilla getDisplayName = "container.loom" -> "Loom". v1
// sends the plain literal (structured to become a translatable Component when chat grows one).
const loomTitle = "Loom"

// loomDataSelectedIndex is the DataSlot id of selectedBannerPatternIndex (the single addDataSlot).
const loomDataSelectedIndex = 0

// compBannerPatterns is the minecraft:banner_patterns component wire type id (the index into
// data/registryid.DataComponentType == the wire id SlotData/Patch encode). Verified: NewComponent case
// 72 == BannerPatterns; datacomponenttype.go lists banner_patterns at index 72.
const compBannerPatterns = 72

// loomPatternIDs is the BANNER_PATTERN registry in protocol-id order -- the alphabetical file order of
// server/registrydata/registries/banner_pattern/*.json, which is exactly the order registrydata.Load
// sends the entries in, so the client assigns each pattern this protocol_id. The banner_patterns
// component PatternType is a registryEntryHolder: VarInt 0 = inline, else index+1. So a pattern wire
// ref = loomPatternIDs[name]+1.
//
//	[VERIFIED: ls server/registrydata/registries/banner_pattern | sort -> this order (0=base..42=triangles_top).]
var loomPatternIDs = func() map[string]int {
	names := []string{
		"base", "border", "bricks", "circle", "creeper", "cross", "curly_border",
		"diagonal_left", "diagonal_right", "diagonal_up_left", "diagonal_up_right",
		"flow", "flower", "globe", "gradient", "gradient_up", "guster",
		"half_horizontal", "half_horizontal_bottom", "half_vertical", "half_vertical_right",
		"mojang", "piglin", "rhombus", "skull", "small_stripes",
		"square_bottom_left", "square_bottom_right", "square_top_left", "square_top_right",
		"straight_cross", "stripe_bottom", "stripe_center", "stripe_downleft", "stripe_downright",
		"stripe_left", "stripe_middle", "stripe_right", "stripe_top",
		"triangle_bottom", "triangle_top", "triangles_bottom", "triangles_top",
	}
	m := make(map[string]int, len(names))
	for i, n := range names {
		m[n] = i
	}
	return m
}()

// loomNoItemRequired is BannerPatternTags.NO_ITEM_REQUIRED -- the patterns selectable when the loom
// PATTERN slot is EMPTY (the base craftable patterns). Mirrors no_item_required.json verbatim, in that
// file listed order (the order the client renders the picker).
//
//	[VERIFIED: server/registrydata/tags/banner_pattern/no_item_required.json values, in order.]
var loomNoItemRequired = []string{
	"square_bottom_left", "square_bottom_right", "square_top_left", "square_top_right",
	"stripe_bottom", "stripe_top", "stripe_left", "stripe_right", "stripe_center", "stripe_middle",
	"stripe_downright", "stripe_downleft", "small_stripes", "cross", "straight_cross",
	"triangle_bottom", "triangle_top", "triangles_bottom", "triangles_top",
	"diagonal_left", "diagonal_up_right", "diagonal_up_left", "diagonal_right",
	"circle", "rhombus", "half_vertical", "half_horizontal", "half_vertical_right",
	"half_horizontal_bottom", "border", "gradient", "gradient_up",
}

// loomPatternItemProvides maps a loom-PATTERN item id to the banner pattern(s) its
// PROVIDES_BANNER_PATTERNS component grants (the #banner_pattern/pattern_item/<x> tag resolved). Each
// vanilla pattern item tag lists exactly one pattern. Mirrors the per-item tags under
// registrydata/tags/banner_pattern/pattern_item/*.json.
//
//	[VERIFIED: pattern_item tags -> the single pattern each grants (field_masoned->bricks,
//	 bordure_indented->curly_border); loom_patterns item tag lists exactly these 10 items.]
var loomPatternItemProvides = func() map[int32][]string {
	m := map[int32][]string{}
	m[int32(item.FlowerBannerPattern.ID)] = []string{"flower"}
	m[int32(item.CreeperBannerPattern.ID)] = []string{"creeper"}
	m[int32(item.SkullBannerPattern.ID)] = []string{"skull"}
	m[int32(item.MojangBannerPattern.ID)] = []string{"mojang"}
	m[int32(item.GlobeBannerPattern.ID)] = []string{"globe"}
	m[int32(item.PiglinBannerPattern.ID)] = []string{"piglin"}
	m[int32(item.FlowBannerPattern.ID)] = []string{"flow"}
	m[int32(item.GusterBannerPattern.ID)] = []string{"guster"}
	m[int32(item.FieldMasonedBannerPattern.ID)] = []string{"bricks"}
	m[int32(item.BordureIndentedBannerPattern.ID)] = []string{"curly_border"}
	return m
}()

// isLoomBlock reports whether a block state is a loom (LoomBlock). CITE LoomBlock instanceof.
func isLoomBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:loom"
}

// isBannerItem ports stack.getItem() instanceof BannerItem for the loom banner slot -- the 16 colored
// banners are contiguous in the generated item table (white_banner..black_banner, DyeColor order).
// CITE BannerItem (the banner slot mayPlace).
func isBannerItem(s component.SlotData) bool {
	if stackEmpty(s) {
		return false
	}
	id := int32(s.ItemID)
	return id >= int32(item.WhiteBanner.ID) && id <= int32(item.BlackBanner.ID)
}

// isLoomDyeItem ports LoomMenu.isDyeItem: stack.is(ItemTags.LOOM_DYES) && stack.has(DataComponents.DYE).
// LOOM_DYES == #minecraft:dyes (the 16 dyes); the DYE-component presence is the dyeColorIDOf check.
// CITE LoomMenu.isDyeItem.
func isLoomDyeItem(s component.SlotData) bool {
	if stackEmpty(s) {
		return false
	}
	_, ok := dyeColorIDOf(int32(s.ItemID))
	return ok
}

// isLoomPatternItem ports LoomMenu.isPatternItem: stack.is(ItemTags.LOOM_PATTERNS) &&
// stack.has(DataComponents.PROVIDES_BANNER_PATTERNS). The 10 loom-pattern items carry the component.
// CITE LoomMenu.isPatternItem.
func isLoomPatternItem(s component.SlotData) bool {
	if stackEmpty(s) {
		return false
	}
	_, ok := loomPatternItemProvides[int32(s.ItemID)]
	return ok
}

// openLoom ports LoomBlock.useWithoutItem -> ServerPlayer.openMenu for a loom at pos: close any prior
// window, allocate a windowId, send OpenScreen(loom) + the initial content + the initial DataSlot
// (selectedBannerPatternIndex = -1), and record the open state. Returns true (the action was consumed).
func (t *TickLoop) openLoom(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil {
		return false
	}
	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}

	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindLoom, loomPos: pos, loomSelected: -1}

	menuID := menuTypeID(registryid.Menu, "minecraft:loom")
	p.client.Send(openScreen(int32(win), menuID, loomTitle))

	// initMenu -> broadcastChanges: push the full 40-slot list + the DataSlot (index=-1).
	t.sendLoomContent(p)
	p.client.Send(containerSetData(int32(win), loomDataSelectedIndex, -1))
	return true
}

// loomMenuItems builds the 40-slot ContainerSetContent list: banner 0, dye 1, pattern 2, result 3, then
// the player inventory -- main 4..30 (window 9..35), hotbar 31..39 (window 36..44). Mirrors the LoomMenu
// ctor addSlot order + addStandardInventorySlots.
func loomMenuItems(oc *openContainer, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, loomMenuSize)
	out[0] = oc.loomBanner
	out[1] = oc.loomDye
	out[2] = oc.loomPattern
	out[3] = oc.loomResult
	for i := 0; i < 27; i++ {
		out[4+i] = inv.get(int16(windowMainFirst + i))
	}
	for i := 0; i < 9; i++ {
		out[4+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendLoomContent pushes the authoritative ContainerSetContent for the open loom window (the initMenu ->
// broadcastChanges full-slot sync + the resend after any click). Bumps the player inventory state id.
func (t *TickLoop) sendLoomContent(p *tickPlayer) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindLoom {
		return
	}
	inv := ensureInventory(p)
	inv.incrementStateId()
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		loomMenuItems(p.openContainer, inv), inv.getCarried()))
}

// loomGetSelectablePatterns ports LoomMenu.getSelectablePatterns(patternStack): an EMPTY pattern slot
// yields BannerPatternTags.NO_ITEM_REQUIRED (the base patterns); a pattern item yields its
// PROVIDES_BANNER_PATTERNS set. Returns the pattern NAMES in the vanilla-listed order (the picker order).
//
// 1:1 net.minecraft.world.inventory.LoomMenu.getSelectablePatterns
func loomGetSelectablePatterns(patternStack component.SlotData) []string {
	if stackEmpty(patternStack) {
		// patternGetter.get(NO_ITEM_REQUIRED) -- the base patterns (a fresh copy so callers never alias).
		out := make([]string, len(loomNoItemRequired))
		copy(out, loomNoItemRequired)
		return out
	}
	if names, ok := loomPatternItemProvides[int32(patternStack.ItemID)]; ok {
		out := make([]string, len(names))
		copy(out, names)
		return out
	}
	return nil // no PROVIDES_BANNER_PATTERNS: empty list (ImmutableList.of()).
}

// loomInputsChanged ports LoomMenu.slotsChanged(container): recompute the selectable pattern list from
// the pattern slot, reconcile the selected index (keep the same selected HOLDER if it survives;
// auto-select a lone option; else clear), then setupResultSlot for the resolved holder (or clear the
// result if banner/dye is missing, the list is empty, or the banner already has 6 layers).
//
// 1:1 net.minecraft.world.inventory.LoomMenu.slotsChanged
func (t *TickLoop) loomInputsChanged(oc *openContainer) {
	banner := oc.loomBanner
	dye := oc.loomDye
	pattern := oc.loomPattern

	// if (banner.isEmpty() || dye.isEmpty()) { result=EMPTY; selectablePatterns=of(); selected=-1; return; }
	if stackEmpty(banner) || stackEmpty(dye) {
		oc.loomResult = component.SlotData{Count: 0}
		oc.loomPatterns = oc.loomPatterns[:0]
		oc.loomSelected = -1
		return
	}

	// prevSelected + validPrev; prevPatterns; selectablePatterns = getSelectablePatterns(pattern).
	prevSelected := oc.loomSelected
	validPrev := prevSelected >= 0 && prevSelected < len(oc.loomPatterns)
	prevPatterns := oc.loomPatterns
	oc.loomPatterns = loomGetSelectablePatterns(pattern)

	var holder string // the resolved BannerPattern name (Holder), "" == null
	hasHolder := false
	switch {
	case len(oc.loomPatterns) == 1:
		// size==1: selectedBannerPatternIndex.set(0); holder = selectablePatterns.get(0).
		oc.loomSelected = 0
		holder, hasHolder = oc.loomPatterns[0], true
	case !validPrev:
		// !validPrev: selectedBannerPatternIndex.set(-1); holder = null.
		oc.loomSelected = -1
	default:
		// keep the previously-selected HOLDER if it still appears in the new list (indexOf), else clear.
		prevHolder := prevPatterns[prevSelected]
		newIdx := loomIndexOf(oc.loomPatterns, prevHolder)
		if newIdx != -1 {
			holder, hasHolder = prevHolder, true
			oc.loomSelected = newIdx
		} else {
			oc.loomSelected = -1
		}
	}

	if !hasHolder {
		// holder == null: resultSlot = EMPTY.
		oc.loomResult = component.SlotData{Count: 0}
		return
	}
	// banner.getOrDefault(BANNER_PATTERNS, EMPTY).layers().size() >= 6: too many layers -> clear.
	if loomLayerCount(banner) >= 6 {
		oc.loomSelected = -1
		oc.loomResult = component.SlotData{Count: 0}
		return
	}
	oc.loomResult = loomSetupResult(banner, dye, holder)
}

// loomIndexOf returns the index of name in list, or -1 (List.indexOf).
func loomIndexOf(list []string, name string) int {
	for i, n := range list {
		if n == name {
			return i
		}
	}
	return -1
}

// loomLayerCount ports banner.getOrDefault(BANNER_PATTERNS, EMPTY).layers().size(): the current number
// of pattern layers on the banner stack (0 for a plain banner). Reads the banner_patterns component off
// the stack component patch.
func loomLayerCount(banner component.SlotData) int {
	patch := component.DecodePatch(banner)
	if bp, ok := patch.Get(compBannerPatterns).(*component.BannerPatterns); ok {
		return len(bp.Layers)
	}
	return 0
}

// loomSetupResult ports the setupResultSlot result compute (the non-clear branch): result =
// banner.copyWithCount(1); result.update(BANNER_PATTERNS, EMPTY, layers -> Builder().addAll(layers).
// add(holder, dyeColor).build()). The new layer is appended AFTER the existing layers (Builder.addAll
// then add). The PatternType is the registry ref (loomPatternIDs[holder]+1); the ColorID is the dye
// DyeColor.
//
// 1:1 net.minecraft.world.inventory.LoomMenu.setupResultSlot + lambda
func loomSetupResult(banner, dye component.SlotData, holder string) component.SlotData {
	dyeColor, ok := dyeColorIDOf(int32(dye.ItemID))
	if !ok {
		// dye.get(DYE) == null -> result stays EMPTY (the setupResultSlot guard).
		return component.SlotData{Count: 0}
	}
	patID, known := loomPatternIDs[holder]
	if !known {
		return component.SlotData{Count: 0} // unknown pattern (never for a vanilla holder).
	}

	result := stackCopyWithCount(banner, 1) // banner.copyWithCount(1) -- carries the banner components.
	patch := component.DecodePatch(result)

	// Builder().addAll(existing).add(holder, dyeColor).build(): existing layers first, the new layer last.
	var layers []component.BannerPatternLayer
	if bp, ok := patch.Get(compBannerPatterns).(*component.BannerPatterns); ok {
		layers = append(layers, bp.Layers...)
	}
	layers = append(layers, component.BannerPatternLayer{
		PatternType: pk.VarInt(patID + 1), // registryEntryHolder: index+1 (0 reserved for inline).
		ColorID:     pk.VarInt(dyeColor),
	})
	patch.Set(compBannerPatterns, &component.BannerPatterns{Layers: layers})
	return patch.ApplyTo(result)
}

// loomClickButton ports LoomMenu.clickMenuButton(player, buttonId): if buttonId is a valid index into
// the current selectablePatterns, set the selected index DataSlot and rebuild the result from that
// pattern; then re-send the authoritative content + the DataSlot. Out-of-range returns false (ignored).
//
// 1:1 net.minecraft.world.inventory.LoomMenu.clickMenuButton
func (t *TickLoop) loomClickButton(p *tickPlayer, oc *openContainer, buttonID int) {
	if buttonID < 0 || buttonID >= len(oc.loomPatterns) {
		return // !(buttonId >= 0 && buttonId < selectablePatterns.size()): return false (no-op).
	}
	oc.loomSelected = buttonID
	// setupResultSlot(selectablePatterns.get(buttonId)): recompute the result for the chosen holder.
	// setupResultSlot re-checks banner+dye+dyeColor (loomSetupResult returns EMPTY if any is missing); the
	// 6-layer guard mirrors slotsChanged so a full banner never yields a result.
	if stackEmpty(oc.loomBanner) || stackEmpty(oc.loomDye) || loomLayerCount(oc.loomBanner) >= 6 {
		oc.loomResult = component.SlotData{Count: 0}
	} else {
		oc.loomResult = loomSetupResult(oc.loomBanner, oc.loomDye, oc.loomPatterns[buttonID])
	}

	t.sendLoomContent(p)
	if p.client != nil {
		p.client.Send(containerSetData(int32(oc.windowID), loomDataSelectedIndex, int16(buttonID)))
	}
}

// closeLoomWindow ports LoomMenu.removed -> clearContainer(player, inputContainer): the three inputs
// (banner/dye/pattern) are returned to the player inventory (invAdd; overflow -> playerDrop), NOT
// persisted. The result is virtual (never returned). Called from handleContainerClose.
//
// 1:1 net.minecraft.world.inventory.LoomMenu.removed
func (t *TickLoop) closeLoomWindow(p *tickPlayer, oc *openContainer) {
	inv := ensureInventory(p)
	for _, s := range []component.SlotData{oc.loomBanner, oc.loomDye, oc.loomPattern} {
		if stackEmpty(s) {
			continue
		}
		stack := s
		if !t.invAdd(inv, &stack) {
			t.playerDrop(p, stack, false)
		}
	}
	oc.loomBanner = component.SlotData{Count: 0}
	oc.loomDye = component.SlotData{Count: 0}
	oc.loomPattern = component.SlotData{Count: 0}
	oc.loomResult = component.SlotData{Count: 0}
}
