package server

import (
	"os"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// test_kit.go is a GATE-ONLY scaffold, NOT vanilla gameplay. Vanilla survival spawns a player
// with an EMPTY inventory; food/blocks come from playing the world. But to visually gate the
// hunger-eat loop (17-19), the container-click handlers (17-20), and block place/break (17-21)
// on a real client without first having to mine a whole meal, we seed a fixed starter kit when
// the operator opts in with SULFUR_TEST_KIT=1. It is GUARDED by that env var and applied only
// at join, so the default (prod) path is byte-for-byte the vanilla empty-inventory join — no
// non-vanilla behavior is baked into the real spawn. Delete this file (and its call site in
// gameplay_tick.go) once the gate pass is done, or once /give lands.

// testKitEnabled reports whether the operator opted into the gate-only starter kit. Read once
// per join from the environment so toggling needs only a server restart, never a recompile.
func testKitEnabled() bool {
	return os.Getenv("SULFUR_TEST_KIT") == "1"
}

// testKitStack is one kit entry: a stack of `count` of the item with registry id `id`, placed
// into the menu slot `slot` (9-35 main, 36-44 hotbar — storage only, never crafting/armor).
type testKitStack struct {
	slot  int16
	id    item.ID
	count int32
}

// testKit is the fixed gate kit: food to exercise the eat/hunger loop, and stackable blocks to
// exercise place/break + every container-click path (pickup, shift-click, swap, drop, drag,
// double-click). Hotbar slots 36-44 are filled first so the items are immediately in-hand.
var testKit = []testKitStack{
	{slot: 36, id: item.CookedBeef.ID, count: 16},  // hotbar 1: food (eat -> restore hunger)
	{slot: 37, id: item.Bread.ID, count: 16},       // hotbar 2: more food
	{slot: 38, id: item.Apple.ID, count: 16},       // hotbar 3: more food
	{slot: 39, id: item.Cobblestone.ID, count: 64}, // hotbar 4: place/break blocks
	{slot: 40, id: item.OakPlanks.ID, count: 64},   // hotbar 5: place/break blocks
	{slot: 41, id: item.Torch.ID, count: 64},       // hotbar 6: place
	{slot: 42, id: item.Dirt.ID, count: 64},        // hotbar 7: place/break
	{slot: 43, id: item.Chest.ID, count: 16},       // hotbar 8: place a chest (open-UI gate)
	{slot: 44, id: item.SugarCane.ID, count: 16},   // hotbar 9: sugar cane (scheduled-tick survival/growth)
	{slot: 9, id: item.Cobblestone.ID, count: 64},  // main row 1: spare stack to shift-click/merge
	{slot: 10, id: item.Cobblestone.ID, count: 32}, // main row 1: partial stack for double-click collect
	{slot: 11, id: item.Sand.ID, count: 64},        // main row 1: sand (place sugar cane ON it, next to water)
	{slot: 12, id: item.WaterBucket.ID, count: 1},  // main row 1: water source for sugar-cane survival
}

// applyTestKit seeds the gate-only starter kit into the player's tick-owned inventory. It is a
// no-op unless SULFUR_TEST_KIT=1. Called at join AFTER the inventory is ensured and BEFORE the
// join-sync (syncJoinInventories) sends the authoritative content, so the kit lands on the wire
// with the first ContainerSetContent. Owner-goroutine only (called from AcceptPlayer's tick-side
// registration), so it touches tick-owned state with no synchronization (TICK-05).
func applyTestKit(p *tickPlayer) {
	if !testKitEnabled() {
		return
	}
	inv := ensureInventory(p)
	for _, k := range testKit {
		inv.set(k.slot, component.SlotData{Count: pk.VarInt(k.count), ItemID: pk.VarInt(k.id)})
	}
}
