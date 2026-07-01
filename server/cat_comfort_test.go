package server

// cat_comfort_test.go — MOB-NEUT-03 (Task #9), the cat COMFORT-goal landing pass. This session
// jar-verified the three cat comfort goals (Cat$CatRelaxOnOwnerGoal@3, CatLieOnBedGoal@5,
// CatSitOnBlockGoal@7), the morning-gift (Cat$CatRelaxOnOwnerGoal.stop -> giveMorningGift) and the
// collar-dye branch of Cat.mobInteract down to their exact constants and RNG draw order (recorded in the
// vanilla_cat/main.star DEFERRED header). NONE of them are landable in v1: every gate is an unbuilt
// subsystem (player-sleep state; a runtime BlockTags.BEDS block-state membership map; the CHEST
// open-count / FURNACE-LIT block-entity queries; the dye tag + DataComponents.DYE + the synched
// DATA_COLLAR_COLOR field). The 1:1 mandate forbids stubbing a whole GATE to a constant (constant-false
// = a silently disabled goal; constant-true = a wrong-firing goal). So these tests LOCK the deferral: the
// cat still declares exactly its 8 goalSelector goals (no comfort goal silently slipped in), and the
// collar-dye branch is inert (an owner holding a dye still falls through to the plain sit-toggle exactly
// as the wolf does). When the named subsystem lands, the goal drops in verbatim from the header citations
// and THESE tests flip to assert the real behavior.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
)

// TestCatComfortGoalsStillDeferred locks that the vanilla_cat declaration carries EXACTLY the 8 buildable
// goalSelector goals (float/panic/sit/tempt/follow_owner/breed/stroll/look) and ZERO target goals — i.e.
// none of the deferred comfort goals (CatRelaxOnOwner@3 / CatLieOnBed@5 / CatSitOnBlock@7) nor the prey
// goals (@8 leap / @9 ocelot / targetSelector prey) silently slipped into the declaration. This is the
// executable mirror of the .star DEFERRED header: a future landing MUST bump this count deliberately
// (and the vanilla_mob_test cat row with it).
func TestCatComfortGoalsStillDeferred(t *testing.T) {
	r, err := loadVanillaMobRegistry()
	if err != nil {
		t.Fatalf("loadVanillaMobRegistry: %v", err)
	}
	decl, ok := r.byName[vanillaCatMobName]
	if !ok {
		t.Fatal("vanilla_cat declaration missing from the registry")
	}
	var goalSel, targetSel int
	for _, g := range decl.goals {
		if g.flags&flagTarget != 0 {
			targetSel++
		} else {
			goalSel++
		}
	}
	if goalSel != 8 {
		t.Fatalf("cat goalSelector goal count = %d, want 8 (a comfort goal slipped in or was dropped — "+
			"update the .star DEFERRED header AND vanilla_mob_test cat row if this is intentional)", goalSel)
	}
	if targetSel != 0 {
		t.Fatalf("cat targetSelector goal count = %d, want 0 (the rabbit/turtle prey goals are cite-deferred)", targetSel)
	}
}

// TestCatCollarDyeDeferred locks that the collar-dye branch of Cat.mobInteract is INERT in v1. Per the jar
// (CFR Cat.mobInteract), the FIRST tamed+owned check is `if stack.is(ItemTags.CAT_COLLAR_DYES) { … }`
// BEFORE the feed/sit-toggle. There is no CAT_COLLAR_DYES runtime tag / DataComponents.DYE read / synched
// DATA_COLLAR_COLOR in v1, so tryCatInteract does NOT special-case a dye: an owner holding a dye on a
// tamed cat falls THROUGH to the plain empty-hand-equivalent sit-toggle (the dye is not consumed as a
// collar recolor). When the dye subsystem lands, this must flip: the dye is consumed, the collar recolors,
// and the sit does NOT toggle. Cite Cat.mobInteract (collar-dye branch).
func TestCatCollarDyeDeferred(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	cat := newTestCat(1)
	cat.tame = true
	cat.ownerUUID = 42
	p := newTestPlayerHolding(loop, 42, int32(item.RedDye.ID))

	if cat.orderedToSit {
		t.Fatal("precondition: the tamed cat starts NOT ordered to sit")
	}
	// Because the dye branch is deferred (no CAT_COLLAR_DYES tag), the held dye is treated as a non-food
	// non-collar item → the tamed+owned path runs the sit-toggle (parent.consumesAction() == false).
	if !loop.tryCatInteract(p, cat) {
		t.Fatal("owner dye interact on a tamed cat returned false, want true (falls through to sit-toggle)")
	}
	if !cat.orderedToSit {
		t.Fatal("cat did not sit after a dye interact — the collar-dye branch is deferred, so the interact " +
			"MUST fall through to the sit-toggle (when the dye subsystem lands, flip this assertion)")
	}
	// The dye MUST still be in hand — the deferred collar-dye branch does NOT consume it (only the real
	// setCollarColor branch calls stack.consume(1,player), which does not exist yet).
	held := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot))
	if slotIsEmpty(held) || int32(held.ItemID) != int32(item.RedDye.ID) {
		t.Fatalf("held slot after a deferred dye interact = %+v, want the dye intact (dye branch must not "+
			"consume it in v1)", held)
	}
}
