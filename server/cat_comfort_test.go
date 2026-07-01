package server

// cat_comfort_test.go — MOB-NEUT-03 (Task #9), the cat COMFORT-goal LANDING pass. This session built
// the two base subsystems the comfort goals were deferred on — the player-sleep state machine (SLEEP-01,
// player_sleep.go) and the runtime BlockTags.BEDS query (block_tags.go) — and landed the two now-buildable
// cat goals: Cat$CatRelaxOnOwnerGoal@3 (kind="cat_relax_on_owner") and CatLieOnBedGoal@5
// (kind="cat_lie_on_bed"), plus the morning-gift branch (never fires at the vanilla default
// CAT_WAKING_UP_GIFT_CHANCE == 0.0f). These tests exercise the now-LIVE goals deterministically.
//
// STILL DEFERRED (cited): CatSitOnBlockGoal@7 (needs the CHEST open-count / FURNACE-LIT block-entity
// queries), the prey goals (@8 leap / @9 ocelot / targetSelector rabbit+turtle — defer WITH the prey
// selector), and the collar-dye branch of Cat.mobInteract (needs ItemTags.CAT_COLLAR_DYES +
// DataComponents.DYE + the synched DATA_COLLAR_COLOR). TestCatCollarDyeDeferred still locks that one.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// redBedHeadState returns the state id of a red bed's HEAD part facing north (a valid placed bed state).
func redBedHeadState(t *testing.T) block.StateID {
	t.Helper()
	s, ok := block.ToStateID[block.RedBed{Facing: block.North, Part: block.BedPartHead}]
	if !ok {
		t.Fatal("red_bed north/head state id missing from block.ToStateID")
	}
	return s
}

// TestCatComfortGoalsLanded locks that the vanilla_cat now declares EXACTLY its 10 goalSelector goals
// (the 8 prior + the two landed comfort goals cat_relax_on_owner@3 + cat_lie_on_bed@5) and ZERO target
// goals (the rabbit/turtle prey goals stay cite-deferred). This is the executable mirror of the .star
// header: a future prey/sit-on-block landing MUST bump this count deliberately (and vanilla_mob_test).
func TestCatComfortGoalsLanded(t *testing.T) {
	r, err := loadVanillaMobRegistry()
	if err != nil {
		t.Fatalf("loadVanillaMobRegistry: %v", err)
	}
	decl, ok := r.byName[vanillaCatMobName]
	if !ok {
		t.Fatal("vanilla_cat declaration missing from the registry")
	}
	var goalSel, targetSel int
	haveRelax, haveLie := false, false
	for _, g := range decl.goals {
		if g.flags&flagTarget != 0 {
			targetSel++
		} else {
			goalSel++
		}
		switch g.nativeKind {
		case "cat_relax_on_owner":
			haveRelax = true
		case "cat_lie_on_bed":
			haveLie = true
		}
	}
	if goalSel != 10 {
		t.Fatalf("cat goalSelector goal count = %d, want 10 (the 8 prior + cat_relax_on_owner@3 + cat_lie_on_bed@5)", goalSel)
	}
	if targetSel != 0 {
		t.Fatalf("cat targetSelector goal count = %d, want 0 (the rabbit/turtle prey goals are cite-deferred)", targetSel)
	}
	if !haveRelax || !haveLie {
		t.Fatalf("cat comfort kinds present: relax=%v lie=%v, want both true", haveRelax, haveLie)
	}
}

// TestCatComfortGoalKindsBuild locks that buildNativeGoal instantiates both cat comfort kinds with their
// faithful flag sets (cat_relax_on_owner -> no flags; cat_lie_on_bed -> JUMP|MOVE), so the .star flag
// declarations (flags=[] and flags=["JUMP","MOVE"]) agree with the Go goals (buildAIFromDecl asserts it).
func TestCatComfortGoalKindsBuild(t *testing.T) {
	relax := buildNativeGoal("cat_relax_on_owner", goalDecl{}, &mobDecl{})
	if relax == nil {
		t.Fatal("buildNativeGoal(cat_relax_on_owner) == nil")
	}
	if relax.flags() != 0 {
		t.Fatalf("cat_relax_on_owner flags = %d, want 0 (no flags — the ctor sets no flag set)", relax.flags())
	}
	lie := buildNativeGoal("cat_lie_on_bed", goalDecl{}, &mobDecl{})
	if lie == nil {
		t.Fatal("buildNativeGoal(cat_lie_on_bed) == nil")
	}
	if lie.flags() != flagJump|flagMove {
		t.Fatalf("cat_lie_on_bed flags = %d, want JUMP|MOVE (%d)", lie.flags(), flagJump|flagMove)
	}
}

// catComfortLoop builds a loop with a world, a tamed cat added to the region store, and a player owner
// registered so playerByEntityID resolves. The cat sits at (catX, 64, catZ); the owner at ownerPos.
func catComfortLoop(t *testing.T, catX, catZ float64, ownerID int32, ownerX, ownerY, ownerZ float64) (*TickLoop, *world.ChunkManager, *Entity, *tickPlayer) {
	t.Helper()
	loop, mgr := newFluidLoop()
	cat := newTestCat(1)
	cat.x, cat.y, cat.z = catX, 64, catZ
	cat.tame = true
	cat.ownerUUID = ownerID
	loop.only().entities.add(cat)
	p := &tickPlayer{entityID: ownerID, x: ownerX, y: ownerY, z: ownerZ, health: maxHealth}
	loop.players = append(loop.players, p)
	return loop, mgr, cat, p
}

// TestCatRelaxCanUseGatedOnSleepingOwnerOnBed: the relax goal fires ONLY when the tamed, not-sitting cat
// has a Player owner who isSleeping, is within 100.0 (sqr), stands on a #minecraft:beds block, and the
// spot is unoccupied. Flipping any gate off makes canUse false.
func TestCatRelaxCanUseGatedOnSleepingOwnerOnBed(t *testing.T) {
	loop, mgr, cat, p := catComfortLoop(t, 8.5, 8.5, 42, 9.5, 64, 8.5)
	bedPos := pk.Position{X: 9, Y: 64, Z: 8} // the owner's block position
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)

	g := newCatRelaxOnOwnerGoal()

	// Owner NOT sleeping -> canUse false.
	if g.canUse(loop, cat) {
		t.Fatal("relax canUse true with an AWAKE owner, want false (owner.isSleeping() gate)")
	}
	// Put the owner to sleep on the bed.
	loop.startSleepInBed(p, bedPos)
	if !p.isSleeping() {
		t.Fatal("precondition: owner must be sleeping after startSleepInBed")
	}
	if !g.canUse(loop, cat) {
		t.Fatal("relax canUse false with a SLEEPING owner on a bed within range, want true")
	}

	// Not tame -> false.
	cat.tame = false
	if g.canUse(loop, cat) {
		t.Fatal("relax canUse true for an UNTAMED cat, want false")
	}
	cat.tame = true
	// Ordered to sit -> false.
	cat.orderedToSit = true
	if g.canUse(loop, cat) {
		t.Fatal("relax canUse true for an ORDERED-TO-SIT cat, want false")
	}
	cat.orderedToSit = false
	// Too far (owner moved > 10 blocks away) -> false.
	p.x = 8.5 + 50
	if g.canUse(loop, cat) {
		t.Fatal("relax canUse true with the owner > 10 blocks away, want false (distanceToSqr > 100)")
	}
}

// TestCatRelaxNotABed: a sleeping owner NOT standing on a bed block fails the BlockTags.BEDS gate.
func TestCatRelaxNotABed(t *testing.T) {
	loop, mgr, cat, p := catComfortLoop(t, 8.5, 8.5, 42, 9.5, 64, 8.5)
	// A stone block under the owner (not a bed). The recorded sleeping pos still gates the relax goal on
	// getBlockState(ownerPos).is(BEDS), which is false for stone.
	mgr.SetBlock(pk.Position{X: 9, Y: 64, Z: 8}, block.ToStateID[block.Stone{}], dimMinY)
	sp := pk.Position{X: 9, Y: 64, Z: 8}
	p.sleepingPos = &sp
	p.sleepCounter = 0
	g := newCatRelaxOnOwnerGoal()
	if g.canUse(loop, cat) {
		t.Fatal("relax canUse true with the owner on STONE (not a bed), want false (BlockTags.BEDS gate)")
	}
}

// TestCatRelaxTickSettlesToLying: with the cat within 2.5 of the sleeping owner, the tick sets
// relaxStateOne for the first adjustedTickDelay(16) ticks, then flips to lying.
func TestCatRelaxTickSettlesToLying(t *testing.T) {
	loop, mgr, cat, p := catComfortLoop(t, 9.5, 8.5, 42, 9.5, 64, 8.5) // cat AT the owner (dist 0 < 2.5)
	bedPos := pk.Position{X: 9, Y: 64, Z: 8}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)
	loop.startSleepInBed(p, bedPos)

	g := newCatRelaxOnOwnerGoal()
	if !g.canUse(loop, cat) {
		t.Fatal("precondition: relax canUse must be true (sleeping owner on a bed, cat adjacent)")
	}
	g.start(loop, cat)
	// The first 16 ticks: relaxStateOne true, not lying yet (onBedTicks climbs 1..16, none > 16).
	for i := 0; i < catRelaxOnBedSettleTicks; i++ {
		g.tick(loop, cat)
	}
	if !cat.catRelaxStateOne || cat.catLying {
		t.Fatalf("after %d ticks: relaxStateOne=%v lying=%v, want relaxStateOne=true lying=false", catRelaxOnBedSettleTicks, cat.catRelaxStateOne, cat.catLying)
	}
	// One more tick pushes onBedTicks to 17 (> 16) -> lying.
	g.tick(loop, cat)
	if !cat.catLying || cat.catRelaxStateOne {
		t.Fatalf("after the settle tick: lying=%v relaxStateOne=%v, want lying=true relaxStateOne=false", cat.catLying, cat.catRelaxStateOne)
	}
}

// TestCatMorningGiftNeverFiresAtDefault: on stop, even with the owner's sleepTimer >= 100, the gift never
// fires because CAT_WAKING_UP_GIFT_CHANCE == 0.0f (nextFloat() < 0.0f is always false). The gate is
// structurally present (the nextFloat draw happens for lockstep) but giveMorningGift is unreachable.
func TestCatMorningGiftNeverFiresAtDefault(t *testing.T) {
	loop, mgr, cat, p := catComfortLoop(t, 9.5, 8.5, 42, 9.5, 64, 8.5)
	bedPos := pk.Position{X: 9, Y: 64, Z: 8}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)
	loop.startSleepInBed(p, bedPos)
	p.sleepCounter = 100 // getSleepTimer() >= 100 (the gift-eligible half of the gate)

	g := newCatRelaxOnOwnerGoal()
	if !g.canUse(loop, cat) {
		t.Fatal("precondition: relax canUse must be true")
	}
	g.start(loop, cat)
	before := loop.only().entities.len()
	g.stop(loop, cat)
	after := loop.only().entities.len()
	// giveMorningGift would addFreshEntity(new ItemEntity(...)) -> entity count grows. At the default
	// chance it NEVER runs, so the count is unchanged.
	if after != before {
		t.Fatalf("entity count changed on stop (%d -> %d): the morning-gift fired, but it must NEVER fire at CAT_WAKING_UP_GIFT_CHANCE == 0.0f", before, after)
	}
	// stop also clears the comfort pose.
	if cat.catLying || cat.catRelaxStateOne {
		t.Fatalf("after stop: lying=%v relaxStateOne=%v, want both false", cat.catLying, cat.catRelaxStateOne)
	}
}

// TestCatLieOnBedValidTarget: the lie-on-bed goal's isValidTarget accepts a bed with empty space above
// and rejects a bed with a solid block above (level.isEmptyBlock(pos.above()) && is(BEDS)).
func TestCatLieOnBedValidTarget(t *testing.T) {
	loop, mgr := newFluidLoop()
	cat := newTestCat(1)
	cat.x, cat.y, cat.z = 8.5, 64, 8.5
	cat.tame = true

	bedPos := pk.Position{X: 8, Y: 64, Z: 10}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)

	g := newCatLieOnBedGoal(catLieOnBedSpeed)
	if !g.validTarget(loop, bedPos) {
		t.Fatal("lie-on-bed validTarget false for a bed with empty space above, want true")
	}
	// A solid block directly above the bed -> not valid (space above not empty).
	mgr.SetBlock(pk.Position{X: 8, Y: 65, Z: 10}, block.ToStateID[block.Stone{}], dimMinY)
	if g.validTarget(loop, bedPos) {
		t.Fatal("lie-on-bed validTarget true with a solid block above the bed, want false")
	}
	// A non-bed block is never a valid target.
	stonePos := pk.Position{X: 8, Y: 64, Z: 12}
	mgr.SetBlock(stonePos, block.ToStateID[block.Stone{}], dimMinY)
	if g.validTarget(loop, stonePos) {
		t.Fatal("lie-on-bed validTarget true for a STONE block, want false (not a bed)")
	}
}

// TestCatLieOnBedCanUseGates: canUse requires isTame && !isOrderedToSit && !isLying (the CatLieOnBed
// canUseHook) BEFORE the base MoveToBlockGoal.canUse ring scan.
func TestCatLieOnBedCanUseGates(t *testing.T) {
	loop, mgr := newFluidLoop()
	cat := newTestCat(1)
	cat.x, cat.y, cat.z = 8.5, 64, 8.5
	cat.tame = true
	// The MoveToBlockGoal ring scan uses verticalSearchStart=-2, so it scans y-offsets {-3,+2,-4,+3,...}
	// from the cat (pos.y = mob.y + y - 1) — a same-Y bed is never scanned. Place the bed 2 blocks ABOVE
	// the cat (the first positive scanned offset) so findNearestBlock reaches it.
	mgr.SetBlock(pk.Position{X: 8, Y: 66, Z: 9}, redBedHeadState(t), dimMinY)

	g := newCatLieOnBedGoal(catLieOnBedSpeed)
	if !g.canUse(loop, cat) {
		t.Fatal("lie-on-bed canUse false for a tamed cat near a bed (2 above), want true")
	}
	// isLying -> false (a cat already lying does not re-seek a bed).
	g2 := newCatLieOnBedGoal(catLieOnBedSpeed)
	cat.catLying = true
	if g2.canUse(loop, cat) {
		t.Fatal("lie-on-bed canUse true for an already-LYING cat, want false")
	}
	cat.catLying = false
	// not tame -> false.
	g3 := newCatLieOnBedGoal(catLieOnBedSpeed)
	cat.tame = false
	if g3.canUse(loop, cat) {
		t.Fatal("lie-on-bed canUse true for an UNTAMED cat, want false")
	}
}

// TestCatCollarDyeDeferred locks that the collar-dye branch of Cat.mobInteract is INERT in v1. Per the jar
// (CFR Cat.mobInteract), the FIRST tamed+owned check is `if stack.is(ItemTags.CAT_COLLAR_DYES) { … }`
// BEFORE the feed/sit-toggle. There is no CAT_COLLAR_DYES runtime tag / DataComponents.DYE read / synched
// DATA_COLLAR_COLOR in v1, so tryCatInteract does NOT special-case a dye: an owner holding a dye on a
// tamed cat falls THROUGH to the plain sit-toggle. When the dye subsystem lands, this must flip.
func TestCatCollarDyeDeferred(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	cat := newTestCat(1)
	cat.tame = true
	cat.ownerUUID = 42
	p := newTestPlayerHolding(loop, 42, int32(item.RedDye.ID))

	if cat.orderedToSit {
		t.Fatal("precondition: the tamed cat starts NOT ordered to sit")
	}
	if !loop.tryCatInteract(p, cat) {
		t.Fatal("owner dye interact on a tamed cat returned false, want true (falls through to sit-toggle)")
	}
	if !cat.orderedToSit {
		t.Fatal("cat did not sit after a dye interact — the collar-dye branch is deferred, so the interact " +
			"MUST fall through to the sit-toggle (when the dye subsystem lands, flip this assertion)")
	}
	held := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot))
	if slotIsEmpty(held) || int32(held.ItemID) != int32(item.RedDye.ID) {
		t.Fatalf("held slot after a deferred dye interact = %+v, want the dye intact (dye branch must not "+
			"consume it in v1)", held)
	}
}
