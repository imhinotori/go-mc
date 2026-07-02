package server

// brewing_stand_test.go — the BREWING STAND BLOCK-ENTITY drive (brewing_stand_be.go) + menu
// (brewing_stand_menu.go) + the PotionBrewing mix engine (potion_brewing.go), tested 1:1 against
// BrewingStandBlockEntity.serverTick. The scenarios prove: fuel refill from blaze_powder (fuel := 20, blaze
// powder consumed), the fuel-consume brew start, brewTime counting down from 400, doBrew producing awkward
// potions in the 3 bottle slots (WATER + NETHER_WART -> AWKWARD), and the HAS_BOTTLE blockstate flags.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// brewingStandLoop builds a block loop + a survival player, with a brewing_stand block at the returned pos.
func brewingStandLoop(t *testing.T) (*TickLoop, *tickPlayer, pk.Position) {
	t.Helper()
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	loop.only().world.SetBlock(pos, block.ToStateID[block.BrewingStand{}], dimMinY)
	return loop, p, pos
}

// waterBottle builds a minecraft:potion ItemStack carrying potion_contents = water (a water bottle).
func waterBottle() (int32, int32) { return itemNameToID("potion"), potionRegID("minecraft:water") }

// TestBrewingStandConstants asserts the ported constants against the jar (BrewingStandBlockEntity).
func TestBrewingStandConstants(t *testing.T) {
	if brewTimeTotal != 400 {
		t.Fatalf("brewTimeTotal = %d, want 400 (DEFAULT_BREW_TIME)", brewTimeTotal)
	}
	if brewFuelUses != 20 {
		t.Fatalf("brewFuelUses = %d, want 20 (FUEL_USES)", brewFuelUses)
	}
	if brewContainerSize != 5 {
		t.Fatalf("brewContainerSize = %d, want 5", brewContainerSize)
	}
	if brewSlotIngredient != 3 || brewSlotFuel != 4 {
		t.Fatalf("slot layout wrong: ingredient=%d fuel=%d, want 3/4", brewSlotIngredient, brewSlotFuel)
	}
}

// TestBrewingStandOpen: right-clicking a brewing stand opens its menu — a windowId is allocated, OpenScreen
// carries minecraft:brewing_stand, and ContainerSetContent carries the 41-slot layout (5 BE + 36 player).
func TestBrewingStandOpen(t *testing.T) {
	loop, p, pos := brewingStandLoop(t)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if p.openContainer == nil {
		t.Fatal("openContainer is nil after brewing-stand open")
	}
	if p.openContainer.kind != containerKindBrewingStand {
		t.Fatalf("openContainer kind = %d, want brewing_stand", p.openContainer.kind)
	}
	if p.openContainer.brewingStandPos != pos {
		t.Fatalf("brewingStandPos = %v, want %v", p.openContainer.brewingStandPos, pos)
	}

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundOpenScreen); n != 1 {
		t.Fatalf("ClientboundOpenScreen sent %d times, want 1", n)
	}
	wantMenu := menuTypeID(registryid.Menu, "minecraft:brewing_stand")
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundOpenScreen) {
			continue
		}
		var win, menuID pk.VarInt
		if err := packet.Scan(&win, &menuID); err != nil {
			t.Fatalf("OpenScreen scan: %v", err)
		}
		if int32(menuID) != wantMenu {
			t.Fatalf("OpenScreen menu id = %d, want minecraft:brewing_stand (%d)", menuID, wantMenu)
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
		if int(count) != brewMenuSize {
			t.Fatalf("SetContent item count = %d, want %d (5 BE + 36 player)", count, brewMenuSize)
		}
	}
}

// TestBrewingStandBrewsAwkward: a stand with 3 water bottles + nether_wart + blaze_powder brews over
// serverTick ticks — fuel refills from blaze_powder, brewTime counts down from 400, and doBrew produces 3
// awkward potions; the HAS_BOTTLE flags are TRUE while the bottles sit in place.
func TestBrewingStandBrewsAwkward(t *testing.T) {
	loop, _, pos := brewingStandLoop(t)
	state, _ := loop.only().world.GetBlock(pos, dimMinY)

	b := loop.resolveBrewingStand(pos, state)
	if b == nil {
		t.Fatal("resolveBrewingStand returned nil for a brewing_stand block")
	}
	potionItem, waterID := waterBottle()
	awkwardID := potionRegID("minecraft:awkward")
	b.items[brewSlotBottle0] = makePotionBottle(potionItem, waterID)
	b.items[brewSlotBottle1] = makePotionBottle(potionItem, waterID)
	b.items[brewSlotBottle2] = makePotionBottle(potionItem, waterID)
	b.items[brewSlotIngredient] = makeStack("nether_wart", 1)
	b.items[brewSlotFuel] = makeStack("blaze_powder", 1)

	// Tick 1: fuel refills (fuel <= 0 && blaze_powder) -> fuel = 20, blaze_powder consumed; then the brew
	// starts (canBrew && fuel>0) -> fuel-- (20->19), brewTime = 400, ingredient cached.
	loop.brewingStandServerTick(pos, state, b)
	if b.fuel != 19 {
		t.Fatalf("after tick 1 fuel = %d, want 19 (refill 20 then consume 1 to start brew)", b.fuel)
	}
	if !stackEmpty(b.items[brewSlotFuel]) {
		t.Fatalf("blaze_powder not consumed on refill: %+v", b.items[brewSlotFuel])
	}
	if b.brewTime != 400 {
		t.Fatalf("after tick 1 brewTime = %d, want 400", b.brewTime)
	}
	if !b.hasIngr || b.ingredID != itemNameToID("nether_wart") {
		t.Fatalf("ingredient not cached at brew start: hasIngr=%v id=%d", b.hasIngr, b.ingredID)
	}
	// HAS_BOTTLE flags TRUE (all three bottles present).
	if s, _ := loop.only().world.GetBlock(pos, dimMinY); !brewingStandBottleFlags(s) {
		t.Fatal("HAS_BOTTLE flags not set after bottles placed")
	}
	state, _ = loop.only().world.GetBlock(pos, dimMinY)

	// Ticks 2..401: brewTime decrements 400 -> 0. On the tick brewTime hits 0 (tick 401), doBrew fires.
	for i := 0; i < 400; i++ {
		loop.brewingStandServerTick(pos, state, b)
		state, _ = loop.only().world.GetBlock(pos, dimMinY)
	}
	if b.brewTime != 0 {
		t.Fatalf("brewTime after 400 brew ticks = %d, want 0", b.brewTime)
	}
	// Each bottle is now an AWKWARD potion.
	for i := brewSlotBottle0; i <= brewSlotBottle2; i++ {
		got, ok := readBottlePotion(b.items[i])
		if !ok {
			t.Fatalf("bottle %d has no potion after brew: %+v", i, b.items[i])
		}
		if got != awkwardID {
			t.Fatalf("bottle %d potion = %d, want awkward (%d)", i, got, awkwardID)
		}
	}
	// The ingredient (nether_wart) was consumed (shrink 1 -> empty).
	if !stackEmpty(b.items[brewSlotIngredient]) {
		t.Fatalf("nether_wart not consumed after brew: %+v", b.items[brewSlotIngredient])
	}
}

// TestBrewingStandHasBottleTogglesOff: removing all bottles toggles the HAS_BOTTLE flags back to false.
func TestBrewingStandHasBottleTogglesOff(t *testing.T) {
	loop, _, pos := brewingStandLoop(t)
	state, _ := loop.only().world.GetBlock(pos, dimMinY)
	b := loop.resolveBrewingStand(pos, state)

	potionItem, waterID := waterBottle()
	b.items[brewSlotBottle0] = makePotionBottle(potionItem, waterID)
	loop.brewingStandServerTick(pos, state, b)
	if s, _ := loop.only().world.GetBlock(pos, dimMinY); !brewingStandBottleFlag0(s) {
		t.Fatal("HAS_BOTTLE_0 not TRUE after placing a bottle in slot 0")
	}

	// Remove the bottle -> flag toggles back to false.
	b.items[brewSlotBottle0] = zeroStack()
	state, _ = loop.only().world.GetBlock(pos, dimMinY)
	loop.brewingStandServerTick(pos, state, b)
	if s, _ := loop.only().world.GetBlock(pos, dimMinY); brewingStandBottleFlag0(s) {
		t.Fatal("HAS_BOTTLE_0 still TRUE after removing the bottle")
	}
}

// TestPotionBrewingMixEngine spot-checks the mix engine against known vanilla mixes (VERIFIED CFR
// addVanillaMixes): WATER + NETHER_WART -> AWKWARD (potion mix), and POTION + GUNPOWDER -> SPLASH_POTION
// (container mix, same potion).
func TestPotionBrewingMixEngine(t *testing.T) {
	pb := vanillaPotionBrewing()
	potionItem, waterID := waterBottle()
	awkwardID := potionRegID("minecraft:awkward")

	water := makePotionBottle(potionItem, waterID)
	netherWart := makeStack("nether_wart", 1)

	if !pb.isIngredient(itemNameToID("nether_wart")) {
		t.Fatal("nether_wart not recognized as a brewing ingredient")
	}
	if !pb.hasMix(water, netherWart) {
		t.Fatal("water + nether_wart has no mix (want awkward)")
	}
	out := pb.mix(netherWart, water)
	got, ok := readBottlePotion(out)
	if !ok || got != awkwardID {
		t.Fatalf("mix(water, nether_wart) potion = %d ok=%v, want awkward (%d)", got, ok, awkwardID)
	}

	// Container mix: a water bottle + gunpowder -> a SPLASH potion still holding water.
	gunpowder := makeStack("gunpowder", 1)
	if !pb.hasMix(water, gunpowder) {
		t.Fatal("water potion + gunpowder has no container mix (want splash)")
	}
	splash := pb.mix(gunpowder, water)
	if int32(splash.ItemID) != itemNameToID("splash_potion") {
		t.Fatalf("container mix item = %d, want splash_potion", splash.ItemID)
	}
	sp, ok := readBottlePotion(splash)
	if !ok || sp != waterID {
		t.Fatalf("splash potion potion = %d ok=%v, want water (%d)", sp, ok, waterID)
	}
}

// --- small local test helpers ---

func makeStack(name string, count int) component.SlotData {
	return component.SlotData{ItemID: pk.VarInt(itemNameToID(name)), Count: pk.VarInt(count)}
}

func zeroStack() component.SlotData { return component.SlotData{Count: 0} }

// brewingStandBottleFlags reports whether all three HAS_BOTTLE_N flags are TRUE on a brewing-stand state.
func brewingStandBottleFlags(s block.StateID) bool {
	bs, ok := block.StateList[s].(block.BrewingStand)
	if !ok {
		return false
	}
	return bool(bs.HasBottle0) && bool(bs.HasBottle1) && bool(bs.HasBottle2)
}

// brewingStandBottleFlag0 reports the HAS_BOTTLE_0 flag on a brewing-stand state.
func brewingStandBottleFlag0(s block.StateID) bool {
	bs, ok := block.StateList[s].(block.BrewingStand)
	if !ok {
		return false
	}
	return bool(bs.HasBottle0)
}
