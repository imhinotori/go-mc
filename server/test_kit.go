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

// gateSpawnEggID is the item the gate-only custom-mob spawn trigger matches in the use seam
// (handleUseItem). It is a GATE-ONLY RE-SKIN of the vanilla pig spawn egg: right-clicking it spawns
// the embedded CUSTOM wander mob (PLUGIN-07 / Plan 28-01) via spawnDeclaredMob — but ONLY when
// SULFUR_TEST_KIT=1 (the egg is added to the kit only under that gate, and handleGateSpawnEgg early-
// returns when the env is unset). The default prod join keeps the vanilla EMPTY inventory, so the egg
// is never in any prod player's hand and the trigger is unreachable (threat T-28-03). The wander mob
// renders as the pig wire id (base_type pig — custom = behavior), so a pig spawn egg is the natural
// re-skin; the egg is NOT vanilla pig-spawning behavior, it is the bot's spawn seam.
// (A var, not a const: item.PigSpawnEgg.ID is a struct field, not a compile-time constant.)
var gateSpawnEggID = item.PigSpawnEgg.ID

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
	{slot: 43, id: gateSpawnEggID, count: 8},       // hotbar 8: PLUGIN-07 gate custom-mob spawn egg ON the hotbar (item #1 — held + UseItem; was a chest)
	{slot: 44, id: item.CraftingTable.ID, count: 1}, // hotbar 9: PLUGIN-07 gate crafting bench (item #3 — place + open the 3x3 grid; was sugar cane)
	{slot: 9, id: item.Cobblestone.ID, count: 64},  // main row 1: spare stack to shift-click/merge
	{slot: 10, id: item.Cobblestone.ID, count: 32}, // main row 1: partial stack for double-click collect
	{slot: 11, id: item.Sand.ID, count: 64},        // main row 1: sand (place sugar cane ON it, next to water)
	{slot: 12, id: item.WaterBucket.ID, count: 1},  // main row 1: water source for sugar-cane survival
	{slot: 35, id: gateSpawnEggID, count: 8},       // main row 3 (last slot): PLUGIN-07 gate-only custom-mob spawn egg
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

// handleGateSpawnEgg spawns the embedded CUSTOM wander mob (PLUGIN-07 / Plan 28-01) just in front of
// the player who right-clicked the gate spawn egg — the in-game seam Plan 02's bot uses to make a
// custom mob appear (checklist item #1). It is the test-kit analogue of a vanilla spawn-egg use, but
// it routes through spawnDeclaredMob (NOT vanilla pig spawning).
//
// GATE-ONLY (threat T-28-03): it EARLY-RETURNS unless SULFUR_TEST_KIT=1, so the default prod path is a
// no-op even if (impossibly) a prod player held the egg. A nil registry or a missing "wanderer" decl
// is a silent no-op (the boot-load guarantees the decl is present when the gate is on; a missing one
// means the operator ran the gate egg without the boot-load — never crash a player's right-click).
//
// Owner-goroutine only (TICK-05): called from the tick-side use path (handleUseItem), so it touches
// tick-owned state (the registry read, spawnDeclaredMob's id alloc + entities.add) with no locks.
func (t *TickLoop) handleGateSpawnEgg(p *tickPlayer) {
	// Air-path (right-click air) fallback: spawn ~2 blocks in front along +X so the mob does not
	// appear inside the player. The block-path (handleUseItemOn) is the primary, vanilla-faithful
	// trigger and passes an explicit clicked-face position via handleGateSpawnEggAt.
	t.handleGateSpawnEggAt(p, p.x+2.0, p.y, p.z)
}

// handleGateSpawnEggAt spawns a mob at an explicit position (the spawn-egg gate trigger — Plan 28-01).
// Gated by SULFUR_TEST_KIT (T-28-03): no-op when the kit is disabled or the decl is missing. Owner-
// goroutine only (the tick-side use path), no locks (TICK-05).
//
// v5 SWAP: the egg now spawns the VANILLA_PIG (the real mob with the full ported goal set — FloatGoal,
// stroll, look) instead of the wandermob (a custom mob with ONE MOVE goal and no FloatGoal). Spawning
// the wandermob made it look like FloatGoal was broken in water (the wandermob has no FloatGoal, so it
// sank), masking the working vanilla-pig float. The wandermob stays registered + naturally spawnable;
// the egg is the operator's "give me a REAL pig to test gameplay against" button.
func (t *TickLoop) handleGateSpawnEggAt(p *tickPlayer, x, y, z float64) {
	if !testKitEnabled() || t.mobRegistry == nil {
		return
	}
	decl, ok := t.mobRegistry.byName[vanillaPigMobName]
	if !ok {
		return // boot-load did not register the vanilla pig; no-op rather than panic on a right-click
	}
	t.spawnDeclaredMob(decl, x, y, z)
	udebugPlayer(p, "test-kit", "spawned vanilla pig via gate egg at (%.1f,%.1f,%.1f)", x, y, z)
}
