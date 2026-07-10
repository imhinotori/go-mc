package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// hoe_shovel_test.go covers HoeItem.useOn (hoe.go) and ShovelItem.useOn (shovel.go) via the
// handleUseItemOn hook: tilling the dirt family to farmland, flattening to dirt_path, dowsing a lit
// campfire, and the guard branches (DOWN face, non-air above, wrong tool) that must PASS to placement.

func stateOf(name string) block.StateID { return block.DefaultStateID[name] }

func applyUseOn(loop *TickLoop, p *tickPlayer, pos pk.Position, direction int32) {
	ui := useItemOnPacket(0 /*main hand*/, pos, direction, 0.5, 1.0, 0.5, false, false, 90)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
}

// TestHoeTillsGrassToFarmland: a hoe on grass_block (air above, face UP) becomes farmland and the hoe stays
// (durability, not stack shrink -- count unchanged). CITE: HoeItem.useOn TILLABLES(GRASS_BLOCK->FARMLAND).
func TestHoeTillsGrassToFarmland(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.WoodenHoe.ID, 1)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, stateOf("minecraft:grass_block"), dimMinY)
	// above is air (empty chunk default) -> onlyIfAirAbove holds.

	applyUseOn(loop, p, pos, 1 /*UP*/)

	got := mustGet(t, mgr, pos)
	if !block.IsFarmland(got) {
		t.Fatalf("hoe on grass_block gave state %d, want farmland", got)
	}
	if c := heldCount(p); c != 1 {
		t.Fatalf("hoe count = %d, want 1 (durability consume, not stack shrink)", c)
	}
}

// TestHoeTillsDirtToFarmland: dirt -> farmland (air above).
func TestHoeTillsDirtToFarmland(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.IronHoe.ID, 1)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, stateOf("minecraft:dirt"), dimMinY)
	applyUseOn(loop, p, pos, 1)

	if !block.IsFarmland(mustGet(t, mgr, pos)) {
		t.Fatalf("hoe on dirt did not become farmland")
	}
}

// TestHoeCoarseDirtToDirt: coarse_dirt -> dirt (NOT farmland) per the TILLABLES map. air above.
func TestHoeCoarseDirtToDirt(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.DiamondHoe.ID, 1)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, stateOf("minecraft:coarse_dirt"), dimMinY)
	applyUseOn(loop, p, pos, 1)

	got := mustGet(t, mgr, pos)
	if got != stateOf("minecraft:dirt") {
		t.Fatalf("hoe on coarse_dirt gave state %d, want dirt (%d)", got, stateOf("minecraft:dirt"))
	}
}

// TestHoeBlockedByNonAirAbove: a hoe on grass_block with a solid block ABOVE is onlyIfAirAbove==false, so
// the crop is NOT tilled -- but the hoe still consumed the interaction (no block placed). CITE: onlyIfAirAbove.
func TestHoeBlockedByNonAirAbove(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.WoodenHoe.ID, 1)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, stateOf("minecraft:grass_block"), dimMinY)
	mgr.SetBlock(pk.Position{X: 1, Y: 65, Z: 1}, block.ToStateID[block.Stone{}], dimMinY) // non-air above
	applyUseOn(loop, p, pos, 1)

	if got := mustGet(t, mgr, pos); got != stateOf("minecraft:grass_block") {
		t.Fatalf("hoe with non-air above changed the block to %d, want unchanged grass_block", got)
	}
}

// TestHoeDownFaceNoTill: onlyIfAirAbove requires the clicked face != DOWN. A DOWN click (dir 0) fails the
// predicate -> no till. CITE: HoeItem.onlyIfAirAbove (getClickedFace != DOWN).
func TestHoeDownFaceNoTill(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.WoodenHoe.ID, 1)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, stateOf("minecraft:grass_block"), dimMinY)
	applyUseOn(loop, p, pos, 0 /*DOWN*/)

	if got := mustGet(t, mgr, pos); got != stateOf("minecraft:grass_block") {
		t.Fatalf("hoe on DOWN face tilled the block to %d, want unchanged grass_block", got)
	}
}

// TestShovelFlattensGrassToPath: a shovel on grass_block (air above, face UP) becomes dirt_path.
// CITE: ShovelItem.useOn FLATTENABLES(GRASS_BLOCK->DIRT_PATH).
func TestShovelFlattensGrassToPath(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.WoodenShovel.ID, 1)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, stateOf("minecraft:grass_block"), dimMinY)
	applyUseOn(loop, p, pos, 1)

	got := mustGet(t, mgr, pos)
	if got != stateOf("minecraft:dirt_path") {
		t.Fatalf("shovel on grass_block gave state %d, want dirt_path (%d)", got, stateOf("minecraft:dirt_path"))
	}
	if c := heldCount(p); c != 1 {
		t.Fatalf("shovel count = %d, want 1 (durability, not shrink)", c)
	}
}

// TestShovelDownFaceNoFlatten: a DOWN click PASSes (no flatten). CITE: ShovelItem.useOn (face == DOWN -> PASS).
func TestShovelDownFaceNoFlatten(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.WoodenShovel.ID, 1)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, stateOf("minecraft:grass_block"), dimMinY)
	applyUseOn(loop, p, pos, 0 /*DOWN*/)

	if got := mustGet(t, mgr, pos); got != stateOf("minecraft:grass_block") {
		t.Fatalf("shovel on DOWN face flattened the block to %d, want unchanged grass_block", got)
	}
}

// TestShovelDowsesLitCampfire: a shovel on a LIT campfire sets LIT=false (preserving other properties).
// CITE: ShovelItem.useOn (campfire dowse -> LIT=false).
func TestShovelDowsesLitCampfire(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.IronShovel.ID, 1)

	// Find any lit campfire state (the default campfire is unlit; a lit one has Lit=true with the
	// same facing/signal_fire/waterlogged as some registered state).
	var litID block.StateID = -1
	for sid, b := range block.StateList {
		if c, ok := b.(block.Campfire); ok && bool(c.Lit) {
			litID = block.StateID(sid)
			break
		}
	}
	if litID < 0 {
		t.Fatalf("could not resolve a lit campfire state")
	}
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, litID, dimMinY)
	// Non-air above must NOT block a dowse (only the FLATTENABLES branch checks air-above); a campfire is
	// not flattenable so the dowse branch fires regardless.
	applyUseOn(loop, p, pos, 1)

	got := mustGet(t, mgr, pos)
	if block.IsLitCampfire(got) {
		t.Fatalf("shovel on lit campfire left it lit (state %d)", got)
	}
	if _, isCamp := block.StateList[got].(block.Campfire); !isCamp {
		t.Fatalf("dowsed state %d is not a campfire", got)
	}
}

// TestWrongToolFallsThrough: a non-hoe/non-shovel held item does NOT trigger till/flatten (the block is
// unchanged, and the item-place path handles it -- here a non-block item so it stays a no-op).
func TestWrongToolFallsThrough(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.Stick.ID, 1)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, stateOf("minecraft:grass_block"), dimMinY)
	applyUseOn(loop, p, pos, 1)

	if got := mustGet(t, mgr, pos); got != stateOf("minecraft:grass_block") {
		t.Fatalf("a stick tilled/flattened the block to %d, want unchanged grass_block", got)
	}
}
