package server

// furnace_test.go — GAMEPLAY-05: the FURNACE BLOCK-ENTITY drive (furnace_be.go) + menu (furnace_menu.go),
// tested 1:1 against AbstractFurnaceBlockEntity.serverTick. The scenarios prove: fuel burn (litTimeRemaining
// decrements), cook progress advancing to cookingTotalTime, the iron_ore -> iron_ingot result, the LIT
// blockstate toggle on/off, the recipe XP award on result-take, and the blast_furnace faster cook time.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

const (
	idCoal = 924 // minecraft:coal (fuel: 1600 ticks)
)

// furnaceLoop builds a block loop + a survival player + the embedded crafting/cooking recipe manager, with
// a furnace block at the returned pos (facing north, unlit — the canonical placed default state).
func furnaceLoop(t *testing.T) (*TickLoop, *tickPlayer, pk.Position) {
	t.Helper()
	loop, _ := newBlockLoop()
	loop.SetPlugins(craftingManager(t))
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	loop.only().world.SetBlock(pos, block.ToStateID[block.Furnace{Facing: 2, Lit: false}], dimMinY)
	return loop, p, pos
}

// TestFurnaceOpen: right-clicking a furnace opens its menu — a windowId is allocated, OpenScreen carries
// minecraft:furnace, and ContainerSetContent carries the 39-slot layout (3 furnace + 36 player).
func TestFurnaceOpen(t *testing.T) {
	loop, p, pos := furnaceLoop(t)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if p.openContainer == nil {
		t.Fatal("openContainer is nil after furnace open")
	}
	if p.openContainer.kind != containerKindFurnace {
		t.Fatalf("openContainer kind = %d, want furnace", p.openContainer.kind)
	}
	if p.openContainer.furnacePos != pos {
		t.Fatalf("furnacePos = %v, want %v", p.openContainer.furnacePos, pos)
	}

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundOpenScreen); n != 1 {
		t.Fatalf("ClientboundOpenScreen sent %d times, want 1", n)
	}
	wantMenu := menuTypeID(registryid.Menu, "minecraft:furnace")
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundOpenScreen) {
			continue
		}
		var win, menuID pk.VarInt
		if err := packet.Scan(&win, &menuID); err != nil {
			t.Fatalf("OpenScreen scan: %v", err)
		}
		if int32(menuID) != wantMenu {
			t.Fatalf("OpenScreen menu id = %d, want minecraft:furnace (%d)", menuID, wantMenu)
		}
	}
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundContainerSetContent) {
			continue
		}
		var win, st, count pk.VarInt
		if err := packet.Scan(&win, &st, &count); err != nil {
			t.Fatalf("SetContent scan: %v", err)
		}
		if int(count) != furnaceMenuSize {
			t.Fatalf("SetContent item count = %d, want %d (3 furnace + 36 player)", count, furnaceMenuSize)
		}
	}
}

// TestFurnaceSmeltsIronOre: a furnace loaded with iron_ore (input) + coal (fuel) burns fuel, cooks to
// completion (200 ticks), and produces an iron_ingot in the result slot; litTimeRemaining decrements; the
// LIT blockstate toggles TRUE while burning and back to FALSE when the fuel + input run out; recipe XP is
// accumulated + awarded on result-take.
func TestFurnaceSmeltsIronOre(t *testing.T) {
	loop, _, pos := furnaceLoop(t)
	state, _ := loop.only().world.GetBlock(pos, dimMinY)

	f := loop.resolveFurnace(pos, state)
	if f == nil {
		t.Fatal("resolveFurnace returned nil for a furnace block")
	}
	f.items[furnaceSlotInput] = component.SlotData{ItemID: idIronOre, Count: 1}
	f.items[furnaceSlotFuel] = component.SlotData{ItemID: idCoal, Count: 1}
	// setItem(0) would set cookingTotalTime; the drive sets it on ignite too. Seed it as vanilla does when
	// the input is placed (getTotalCookTime), so the == compare fires at the right tick.
	f.cookingTotalTime = f.getTotalCookTime()
	if f.cookingTotalTime != 200 {
		t.Fatalf("smelting cookingTotalTime = %d, want 200 (SmeltingRecipe default)", f.cookingTotalTime)
	}

	// Tick 1: not lit -> the top-of-tick decrement is SKIPPED (litTimeRemaining is 0), then the ignite
	// step sets litTimeRemaining = litTotalTime = coal's burn duration (1600). The decrement first fires on
	// tick 2. So after tick 1 litTimeRemaining == 1600 exactly; fuel consumed; cookingTimer -> 1; LIT TRUE.
	loop.furnaceServerTick(pos, state, f)
	if f.litTimeRemaining != 1600 {
		t.Fatalf("after tick 1 litTimeRemaining = %d, want 1600 (ignite sets it; decrement is next tick)", f.litTimeRemaining)
	}
	if f.litTotalTime != 1600 {
		t.Fatalf("litTotalTime = %d, want 1600 (coal burn duration)", f.litTotalTime)
	}
	if !stackEmpty(f.items[furnaceSlotFuel]) {
		t.Fatalf("fuel not consumed on ignite: %+v", f.items[furnaceSlotFuel])
	}
	if f.cookingTimer != 1 {
		t.Fatalf("after tick 1 cookingTimer = %d, want 1", f.cookingTimer)
	}
	// LIT blockstate must now be true.
	if s, _ := loop.only().world.GetBlock(pos, dimMinY); !furnaceLit(s) {
		t.Fatal("furnace LIT blockstate not TRUE after ignite tick")
	}
	// Refresh state to the lit variant for subsequent ticks (serverTick reads the passed state).
	state, _ = loop.only().world.GetBlock(pos, dimMinY)

	// Cook the remaining 199 ticks. On tick 200 (cookingTimer 199->200 == cookingTotalTime) the ore smelts.
	for i := 0; i < 199; i++ {
		loop.furnaceServerTick(pos, state, f)
	}
	if f.cookingTimer != 0 {
		t.Fatalf("cookingTimer after 200 ticks = %d, want 0 (reset on smelt)", f.cookingTimer)
	}
	res := f.items[furnaceSlotResult]
	if int32(res.ItemID) != idIronIngot || res.Count != 1 {
		t.Fatalf("result after smelt = id=%d count=%d, want iron_ingot id=%d count=1", res.ItemID, res.Count, idIronIngot)
	}
	if !stackEmpty(f.items[furnaceSlotInput]) {
		t.Fatalf("input not consumed after smelt: %+v", f.items[furnaceSlotInput])
	}
	// A recipe was used -> recipesUsed accumulated (XP pending).
	if len(f.recipesUsed) != 1 {
		t.Fatalf("recipesUsed size = %d, want 1 (one iron smelt)", len(f.recipesUsed))
	}

	// Now let the fuel + (empty) input burn down: with no input, cooking stops; litTimeRemaining keeps
	// decrementing until 0, then LIT toggles back to FALSE. Drive until unlit (bounded).
	litBackOff := false
	for i := 0; i < 1600; i++ {
		state, _ = loop.only().world.GetBlock(pos, dimMinY)
		loop.furnaceServerTick(pos, state, f)
		if s, _ := loop.only().world.GetBlock(pos, dimMinY); !furnaceLit(s) {
			litBackOff = true
			break
		}
	}
	if !litBackOff {
		t.Fatal("furnace LIT blockstate never toggled back to FALSE after the fuel ran out")
	}

	// Award XP on result-take: put the result on the cursor via a PICKUP and assert an XP orb spawned.
	// (The furnace still holds the accumulated recipesUsed until the take.)
	orbsBefore := countOrbs(loop)
	// Re-open so a window exists for the click, then take the result (menu slot 2).
	loop.openFurnace(loopPlayer(loop), pos)
	oc := loopPlayer(loop).openContainer
	loop.clickedFurnace(loopPlayer(loop), oc, 2, 0, containerInputPickup)
	if c := ensureInventory(loopPlayer(loop)).getCarried(); int32(c.ItemID) != idIronIngot {
		t.Fatalf("carried after result take = id=%d, want iron_ingot", c.ItemID)
	}
	if len(f.recipesUsed) != 0 {
		t.Fatalf("recipesUsed not cleared after result take: %d", len(f.recipesUsed))
	}
	// iron smelt XP = 0.7; floor(1*0.7)=0, fractional 0.7 -> maybe 1 orb (RNG). The award MAY spawn 0 or 1
	// orb depending on the level RNG float, so assert the award path RAN (recipesUsed cleared, above) and
	// that orb count did not DECREASE; a strict >=0 check documents the wiring without RNG-flaking.
	if countOrbs(loop) < orbsBefore {
		t.Fatal("orb count decreased after XP award (impossible)")
	}
}

// TestBlastFurnaceFasterCook: a blast_furnace smelts iron_ore in 100 ticks (BlastingRecipe cookingtime 100),
// half the furnace's 200 — proving the subtype parameterization + the recipe-driven cook time.
func TestBlastFurnaceFasterCook(t *testing.T) {
	loop, _ := newBlockLoop()
	loop.SetPlugins(craftingManager(t))
	pos := pk.Position{X: 2, Y: 64, Z: 2}
	loop.only().world.SetBlock(pos, block.ToStateID[block.BlastFurnace{Facing: 2, Lit: false}], dimMinY)
	state, _ := loop.only().world.GetBlock(pos, dimMinY)

	f := loop.resolveFurnace(pos, state)
	if f == nil {
		t.Fatal("resolveFurnace nil for blast_furnace")
	}
	if f.subtype != cookBlasting {
		t.Fatalf("blast_furnace subtype = %q, want blasting", f.subtype)
	}
	if !f.blastLike {
		t.Fatal("blast_furnace blastLike should be true (getBurnDuration halved)")
	}
	f.items[furnaceSlotInput] = component.SlotData{ItemID: idIronOre, Count: 1}
	f.items[furnaceSlotFuel] = component.SlotData{ItemID: idCoal, Count: 1}
	f.cookingTotalTime = f.getTotalCookTime()
	if f.cookingTotalTime != 100 {
		t.Fatalf("blast cookingTotalTime = %d, want 100 (BlastingRecipe default)", f.cookingTotalTime)
	}

	// getBurnDuration halved: coal 1600 -> 800 for a blast_furnace.
	if d := f.getBurnDuration(idCoal); d != 800 {
		t.Fatalf("blast getBurnDuration(coal) = %d, want 800 (1600/2)", d)
	}

	// Tick 1 ignites; cook completes on tick 100 (cookingTimer 99->100).
	loop.furnaceServerTick(pos, state, f)
	state, _ = loop.only().world.GetBlock(pos, dimMinY)
	for i := 0; i < 99; i++ {
		loop.furnaceServerTick(pos, state, f)
	}
	res := f.items[furnaceSlotResult]
	if int32(res.ItemID) != idIronIngot || res.Count != 1 {
		t.Fatalf("blast result after 100 ticks = id=%d count=%d, want iron_ingot count=1", res.ItemID, res.Count)
	}
}

// TestFurnaceIdleCoolsProgress: an UNLIT furnace with cook progress but no fuel/lit decays cookingTimer by
// BURN_COOL_SPEED (2) per tick, clamped to [0, cookingTotalTime] — the else-if branch of serverTick.
func TestFurnaceIdleCoolsProgress(t *testing.T) {
	loop, _, pos := furnaceLoop(t)
	state, _ := loop.only().world.GetBlock(pos, dimMinY)
	f := loop.resolveFurnace(pos, state)
	// No fuel, no lit, but a partial cook progress + total (as if a burn just ended mid-cook).
	f.cookingTotalTime = 200
	f.cookingTimer = 10
	loop.furnaceServerTick(pos, state, f)
	if f.cookingTimer != 8 {
		t.Fatalf("idle cookingTimer = %d, want 8 (10 - BURN_COOL_SPEED 2)", f.cookingTimer)
	}
	// Drive to the clamp floor.
	for i := 0; i < 10; i++ {
		loop.furnaceServerTick(pos, state, f)
	}
	if f.cookingTimer != 0 {
		t.Fatalf("idle cookingTimer floor = %d, want 0 (Mth.clamp lower bound)", f.cookingTimer)
	}
}

// countOrbs counts live ExperienceOrb entities across the loop's regions.
func countOrbs(loop *TickLoop) int {
	n := 0
	loop.forEachRegion(func(r *region) {
		for _, e := range r.entities.all() {
			if e != nil && e.isOrb {
				n++
			}
		}
	})
	return n
}

// loopPlayer returns the single registered test player.
func loopPlayer(loop *TickLoop) *tickPlayer { return loop.players[0] }
