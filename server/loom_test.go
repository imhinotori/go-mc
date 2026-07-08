package server

// loom_test.go -- LOOM MENU (LoomMenu) tested 1:1 against the jar. The scenarios prove:
//   - getSelectablePatterns: an EMPTY pattern slot yields the NO_ITEM_REQUIRED base patterns; a
//     creeper_banner_pattern item yields exactly ["creeper"] (its PROVIDES_BANNER_PATTERNS).
//   - setupResultSlot: banner + red dye + creeper pattern -> a 1-count banner carrying a banner_patterns
//     component whose appended layer is (PatternType = creeper registry ref = idx+1, ColorID = RED = 14).
//   - slotsChanged auto-selects the lone creeper option and computes the result; onTake consumes 1 banner
//     + 1 dye and clears the result.
//   - the 6-layer cap: a banner already carrying 6 layers yields no result.
//
// All values checked against LoomMenu.setupResultSlot / getSelectablePatterns / slotsChanged / LoomMenu6.onTake
// (temp/cache/26.2-inner.jar) + the DyeColor enum (RED == 14) + the BANNER_PATTERN registry order.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// bannerStack builds a plain white_banner SlotData (count 1).
func bannerStack() component.SlotData {
	return component.SlotData{ItemID: pk.VarInt(item.WhiteBanner.ID), Count: 1}
}

// redDyeStack builds a red_dye SlotData (DyeColor RED == 14).
func redDyeStack() component.SlotData {
	return component.SlotData{ItemID: pk.VarInt(item.RedDye.ID), Count: 1}
}

// TestLoomSelectablePatterns: an empty pattern slot exposes the NO_ITEM_REQUIRED base patterns; a
// creeper_banner_pattern exposes exactly its one provided pattern.
func TestLoomSelectablePatterns(t *testing.T) {
	base := loomGetSelectablePatterns(component.SlotData{Count: 0})
	if len(base) != len(loomNoItemRequired) {
		t.Fatalf("empty pattern slot: got %d selectable, want %d (NO_ITEM_REQUIRED)", len(base), len(loomNoItemRequired))
	}
	if base[0] != "square_bottom_left" {
		t.Fatalf("first base pattern = %q, want square_bottom_left", base[0])
	}
	creeper := loomGetSelectablePatterns(component.SlotData{ItemID: pk.VarInt(item.CreeperBannerPattern.ID), Count: 1})
	if len(creeper) != 1 || creeper[0] != "creeper" {
		t.Fatalf("creeper_banner_pattern selectable = %v, want [creeper]", creeper)
	}
}

// TestLoomSetupResultAppendsLayer: banner + red dye + the creeper pattern -> the result is a 1-count
// banner whose banner_patterns component has ONE appended layer (creeper ref, RED color).
func TestLoomSetupResultAppendsLayer(t *testing.T) {
	res := loomSetupResult(bannerStack(), redDyeStack(), "creeper")
	if stackEmpty(res) {
		t.Fatal("setupResultSlot produced an empty result; want a layered banner")
	}
	if int(res.Count) != 1 {
		t.Fatalf("result count = %d, want 1 (copyWithCount(1))", res.Count)
	}
	if int32(res.ItemID) != int32(item.WhiteBanner.ID) {
		t.Fatalf("result item = %d, want white_banner %d", res.ItemID, item.WhiteBanner.ID)
	}
	patch := component.DecodePatch(res)
	bp, ok := patch.Get(compBannerPatterns).(*component.BannerPatterns)
	if !ok {
		t.Fatal("result has no banner_patterns component")
	}
	if len(bp.Layers) != 1 {
		t.Fatalf("result has %d layers, want 1", len(bp.Layers))
	}
	wantPat := pk.VarInt(loomPatternIDs["creeper"] + 1)
	if bp.Layers[0].PatternType != wantPat {
		t.Fatalf("layer PatternType = %d, want %d (creeper idx+1)", bp.Layers[0].PatternType, wantPat)
	}
	if int(bp.Layers[0].ColorID) != 14 {
		t.Fatalf("layer ColorID = %d, want 14 (DyeColor.RED)", bp.Layers[0].ColorID)
	}
}

// TestLoomInputsChangedAndTake: with banner+dye+creeper-pattern in the inputs, slotsChanged auto-selects
// the lone option and computes the result; onTake consumes 1 banner + 1 dye and clears the result.
func TestLoomInputsChangedAndTake(t *testing.T) {
	loop := &TickLoop{}
	oc := &openContainer{windowID: 1, kind: containerKindLoom, loomSelected: -1}
	oc.loomBanner = component.SlotData{ItemID: pk.VarInt(item.WhiteBanner.ID), Count: 1}
	oc.loomDye = component.SlotData{ItemID: pk.VarInt(item.RedDye.ID), Count: 1}
	oc.loomPattern = component.SlotData{ItemID: pk.VarInt(item.CreeperBannerPattern.ID), Count: 1}

	loop.loomInputsChanged(oc)
	// a single provided pattern (creeper) -> auto-selected index 0 and a non-empty result.
	if oc.loomSelected != 0 {
		t.Fatalf("loomSelected = %d, want 0 (lone option auto-selected)", oc.loomSelected)
	}
	if stackEmpty(oc.loomResult) {
		t.Fatal("result empty after slotsChanged; want the layered banner")
	}
	if n := loomLayerCount(oc.loomResult); n != 1 {
		t.Fatalf("result layers = %d, want 1", n)
	}

	// onTake: consume 1 banner + 1 dye; both were count 1 -> both now empty; result cleared.
	loop.onTakeLoom(oc)
	if !stackEmpty(oc.loomBanner) {
		t.Fatalf("banner not consumed on take: %+v", oc.loomBanner)
	}
	if !stackEmpty(oc.loomDye) {
		t.Fatalf("dye not consumed on take: %+v", oc.loomDye)
	}
	if !stackEmpty(oc.loomResult) {
		t.Fatal("result not cleared after take (inputs empty -> setupResultSlot yields EMPTY)")
	}
	if oc.loomSelected != -1 {
		t.Fatalf("loomSelected = %d after take, want -1 (inputs empty)", oc.loomSelected)
	}
}

// TestLoomSixLayerCap: a banner already carrying 6 pattern layers yields NO result (the layers>=6 guard).
func TestLoomSixLayerCap(t *testing.T) {
	loop := &TickLoop{}
	oc := &openContainer{windowID: 1, kind: containerKindLoom, loomSelected: -1}
	// build a banner with 6 layers.
	six := component.SlotData{ItemID: pk.VarInt(item.WhiteBanner.ID), Count: 1}
	var layers []component.BannerPatternLayer
	for i := 0; i < 6; i++ {
		layers = append(layers, component.BannerPatternLayer{PatternType: pk.VarInt(i + 1), ColorID: 0})
	}
	var patch component.Patch
	patch.Set(compBannerPatterns, &component.BannerPatterns{Layers: layers})
	oc.loomBanner = patch.ApplyTo(six)
	oc.loomDye = component.SlotData{ItemID: pk.VarInt(item.RedDye.ID), Count: 1}
	oc.loomPattern = component.SlotData{ItemID: pk.VarInt(item.CreeperBannerPattern.ID), Count: 1}

	loop.loomInputsChanged(oc)
	if !stackEmpty(oc.loomResult) {
		t.Fatal("a 6-layer banner produced a result; want EMPTY (the layers>=6 cap)")
	}
}
