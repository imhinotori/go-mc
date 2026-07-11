package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// block_drop_test.go covers GAMEPLAY-06: the v1 1:1 block->drop lookup, the ITEM
// data-value metadata entry (jar-derived DATA_ITEM index + EntityDataSerializers.ITEM_STACK
// id), and the on-break Item entity spawn + store insert that the GAMEPLAY-01 tracker
// broadcasts as AddEntity.

// TestItemMetadataEntry: the ITEM data-value entry built for a known stack encodes as
// Byte(dataItemIndex) + VarInt(itemStackSerializerID) + the ItemStack body — i.e. a
// well-formed entityDataEntry whose value bytes are exactly the slot encoder's ItemStack
// framing (the SAME ItemStack.OPTIONAL_STREAM_CODEC ContainerSetContent's carried item uses).
func TestItemMetadataEntry(t *testing.T) {
	stack := component.SlotData{Count: 1, ItemID: pk.VarInt(item.Cobblestone.ID)}
	entry := itemDataEntry(stack)

	if entry.index != dataItemIndex {
		t.Fatalf("entry.index = %d, want dataItemIndex %d (ItemEntity.DATA_ITEM)", entry.index, dataItemIndex)
	}
	if entry.serializerID != itemStackSerializerID {
		t.Fatalf("entry.serializerID = %d, want itemStackSerializerID %d (EntityDataSerializers.ITEM_STACK)", entry.serializerID, itemStackSerializerID)
	}

	// The full DataValue framing on the wire: Byte index, VarInt serializerId, then the
	// ItemStack body. The body MUST match the slot encoder's SlotData WriteTo verbatim.
	var got bytes.Buffer
	if _, err := entry.WriteTo(&got); err != nil {
		t.Fatalf("entry.WriteTo: %v", err)
	}

	var want bytes.Buffer
	_, _ = pk.UnsignedByte(dataItemIndex).WriteTo(&want)
	_, _ = pk.VarInt(itemStackSerializerID).WriteTo(&want)
	stackCopy := stack
	_, _ = stackCopy.WriteTo(&want) // the ItemStack body via the SAME SlotData codec

	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatalf("ITEM data-value bytes = % x, want % x", got.Bytes(), want.Bytes())
	}
}

// TestBlockDropViaLoot: blockDropsFor routes a broken block state through the SHARED
// level/loot evaluator (loot.Roll over minecraft:blocks/<name>), NOT the deleted
// hardcoded blockDropTable map. Stone -> cobblestone (the survives_explosion
// alternative, since a v1 hand break carries no tool so match_tool's silk_touch child
// fails); grass_block -> dirt; oak_log -> oak_log; diamond_ore -> diamond (one, since
// apply_bonus/explosion_decay no-op without a tool/explosion). air/unknown -> no drop.
func TestBlockDropViaLoot(t *testing.T) {
	cases := []struct {
		name     string
		state    block.StateID
		wantOK   bool
		wantItem item.ID
	}{
		{"stone->cobblestone", block.ToStateID[block.Stone{}], true, item.Cobblestone.ID},
		{"grass_block->dirt", block.ToStateID[block.GrassBlock{Snowy: false}], true, item.Dirt.ID},
		{"oak_log->oak_log", block.ToStateID[block.OakLog{Axis: block.Y}], true, item.OakLog.ID},
		{"diamond_ore->diamond", block.ToStateID[block.DiamondOre{}], true, item.Diamond.ID},
		{"air->nothing", block.ToStateID[block.Air{}], false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			drops := blockDropsFor(c.state, 12345, blockBreakLootContext(nil, 12345))
			if c.wantOK && len(drops) == 0 {
				t.Fatalf("blockDropsFor(%s) returned no drops, want a %d drop", c.name, c.wantItem)
			}
			if !c.wantOK {
				if len(drops) != 0 {
					t.Fatalf("blockDropsFor(%s) returned %d drops, want none", c.name, len(drops))
				}
				return
			}
			if len(drops) != 1 {
				t.Fatalf("blockDropsFor(%s) returned %d drops, want exactly 1", c.name, len(drops))
			}
			if item.ID(drops[0].ItemID) != c.wantItem {
				t.Fatalf("blockDropsFor(%s) item = %d, want %d", c.name, drops[0].ItemID, c.wantItem)
			}
			if drops[0].Count <= 0 {
				t.Fatalf("blockDropsFor(%s) count = %d, want > 0", c.name, drops[0].Count)
			}
		})
	}
}

// --- Task 2 tests ---------------------------------------------------------------------

// newDropLoop wires a TickLoop with one ready all-air chunk (like newBlockLoop) so break +
// drop spawn have a loaded column.
func newDropLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	return loop, mgr
}

// TestBlockDropSpawnsItem: breaking a DIRT block (a non-tool-requiring block that drops itself
// bare-handed) spawns an entity.Item (typ==71) in the store, positioned with the vanilla
// Block.popResource jitter (block center ± 0.25 per axis, Y additionally offset down by the item
// half-height 0.125), carrying a random toss velocity in [-0.1, 0.1) per axis, the default 10-tick
// pickup delay, and non-empty ITEM metadata. (Stone is NOT used here: it is tagged
// requiresCorrectToolForDrops, so a bare-handed break drops nothing per
// ServerPlayerGameMode.destroyBlock's hasCorrectToolForDrops gate.)
func TestBlockDropSpawnsItem(t *testing.T) {
	loop, mgr := newDropLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Dirt{}], dimMinY)

	before := loop.only().entities.len()
	// Plan 17-21: a survival break is a dig-timer now (START -> elapse -> STOP at progress>=0.7),
	// not an instant STOP. completeSurvivalDig drives the full dig so the block breaks and drops.
	completeSurvivalDig(loop, p, target)

	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("entity count = %d, want %d (one Item spawned)", got, before+1)
	}

	// Find the spawned Item: the one entity that is an Item type.
	var drop *Entity
	for _, e := range loop.only().entities.near(1.5, 1.5, pickupMergeScanChunks) {
		if e.typ == entity.Item.ID {
			drop = e
			break
		}
	}
	if drop == nil {
		t.Fatalf("no Item entity (typ==%d) in the store after break", entity.Item.ID)
	}

	// FIX A — position jitter: block center (1.5, 64.5, 1.5) ± 0.25 per axis, with Y offset down
	// by the item half-height (0.125). So x/z ∈ [1.25, 1.75] and y ∈ [64.5-0.25-0.125, 64.5+0.25-0.125]
	// = [64.125, 64.625]. The spawn must be inside these vanilla ranges (NOT a fixed center).
	if drop.x < 1.25 || drop.x > 1.75 {
		t.Fatalf("item x = %v, want block center 1.5 ± 0.25 jitter [1.25,1.75]", drop.x)
	}
	if drop.z < 1.25 || drop.z > 1.75 {
		t.Fatalf("item z = %v, want block center 1.5 ± 0.25 jitter [1.25,1.75]", drop.z)
	}
	if drop.y < 64.125 || drop.y > 64.625 {
		t.Fatalf("item y = %v, want 64.5 ± 0.25 - 0.125 half-height [64.125,64.625]", drop.y)
	}

	// FIX B — toss velocity: each axis is rng.nextDouble()*0.2 - 0.1 ∈ [-0.1, 0.1).
	for _, vc := range []struct {
		name string
		v    float64
	}{{"vx", drop.vx}, {"vy", drop.vy}, {"vz", drop.vz}} {
		if vc.v < -0.1 || vc.v >= 0.1 {
			t.Fatalf("item %s = %v, want vanilla toss [-0.1, 0.1)", vc.name, vc.v)
		}
	}

	// Block.popResource → setDefaultPickUpDelay() == 10: a fresh drop is not pickable yet.
	if drop.pickupDelay != itemDefaultPickupDelay {
		t.Fatalf("item pickupDelay = %d, want %d (setDefaultPickUpDelay)", drop.pickupDelay, itemDefaultPickupDelay)
	}
	if !drop.isItem {
		t.Fatalf("spawned drop not flagged isItem — the item tick/pickup scan would skip it")
	}
	if len(drop.metadata) == 0 {
		t.Fatalf("item metadata empty — the Item would render INVISIBLE (Pitfall 5)")
	}
}

// TestBlockDropCorrectToolGate: a survival player mining a requiresCorrectToolForDrops block
// (stone) BARE-HANDED via the full player-mining path (ServerPlayerGameMode.destroyBlock) drops
// NOTHING — the loot table only branches on silk_touch, so the tool-correctness gate in
// destroyBlock (`removed && player.hasCorrectToolForDrops(state)`) is the sole suppressor. A
// non-tool block (dirt) broken the same way DOES drop. Regression for the missing drop gate that
// previously let bare-handed stone/ore drops through. CITE: ServerPlayerGameMode.destroyBlock
// (offsets 217-269); Player.hasCorrectToolForDrops.
func TestBlockDropCorrectToolGate(t *testing.T) {
	// STONE bare-handed -> requiresCorrectToolForDrops, wrong tool -> NO drop.
	{
		loop, mgr := newDropLoop()
		p := blockPlayer(loop, 1.5, 65.0, 1.5)
		p.gameMode = gameModeSurvival
		target := pk.Position{X: 1, Y: 64, Z: 1}
		mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

		before := loop.only().entities.len()
		completeSurvivalDig(loop, p, target)

		// The block must have broken (air) but dropped nothing.
		if got, ok := mgr.GetBlock(target, dimMinY); !ok || !block.IsAir(got) {
			t.Fatalf("stone did not break: GetBlock=(%v, ok=%v), want air", got, ok)
		}
		if got := loop.only().entities.len(); got != before {
			t.Fatalf("bare-handed stone break spawned %d items, want 0 (wrong tool for a tool-requiring block)", got-before)
		}
	}
	// DIRT bare-handed -> not tool-requiring -> DOES drop (the gate passes).
	{
		loop, mgr := newDropLoop()
		p := blockPlayer(loop, 1.5, 65.0, 1.5)
		p.gameMode = gameModeSurvival
		target := pk.Position{X: 1, Y: 64, Z: 1}
		mgr.SetBlock(target, block.ToStateID[block.Dirt{}], dimMinY)

		before := loop.only().entities.len()
		completeSurvivalDig(loop, p, target)

		if got := loop.only().entities.len(); got != before+1 {
			t.Fatalf("bare-handed dirt break spawned %d items, want 1 (dirt does not require a tool)", got-before)
		}
	}
}

// TestBlockDropTracked: after a break, a nearby player's tracker tick emits an AddEntity for
// the new item id (the GAMEPLAY-01 broadcast path works for the dropped item).
func TestBlockDropTracked(t *testing.T) {
	loop, mgr := newDropLoop()
	editor := blockPlayer(loop, 1.5, 65.0, 1.5)
	editor.entityID = 1000 // distinct id so the tracker does not skip the item as "self"

	target := pk.Position{X: 1, Y: 64, Z: 1}
	// Dirt: non-tool block that drops itself bare-handed (stone would need a tool -> no drop).
	mgr.SetBlock(target, block.ToStateID[block.Dirt{}], dimMinY)

	// Plan 17-21: complete a survival dig (START -> elapse -> STOP) so the block breaks and drops.
	completeSurvivalDig(loop, editor, target)

	// Drive the SYNCHRONOUS golden-reference tracker (syncTrackerTick) so the emission is
	// deterministic — the live loop.tracker is the async OPT-02 executor whose diff lands a
	// tick later via applyAsyncResults; the sync tracker is the byte-identical reference the
	// other tracker tests drive (TestAsyncTrackerMatchesSync proves they match). It should
	// AddEntity + SetEntityData for the new item. NOTE: drainPackets CLOSES the queue, so we
	// drain exactly ONCE after the tracker runs (the break's ack/BlockUpdate share the buffer
	// but carry different ids, so they do not affect the AddEntity/SetEntityData counts).
	syncTrackerTick(loop)

	got := drainPackets(editor.client)
	if n := countID(got, packetid.ClientboundAddEntity); n < 1 {
		t.Fatalf("AddEntity emitted %d times for the dropped item, want >= 1 (the 17-01 path)", n)
	}
	if n := countID(got, packetid.ClientboundSetEntityData); n < 1 {
		t.Fatalf("SetEntityData emitted %d times, want >= 1 (the ITEM metadata)", n)
	}
}

// TestBreakAirNoDrop: breaking an already-air block (no drop) spawns no Item entity.
func TestBreakAirNoDrop(t *testing.T) {
	loop, _ := newDropLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)

	// (1,64,1) is air in the fresh chunk. A break there: SetBlock(air) over air returns
	// changed=false, so reconcileEdit never runs and no drop spawns. Even if it did, air
	// has no drop. Either way: no Item entity.
	before := loop.only().entities.len()
	target := pk.Position{X: 1, Y: 64, Z: 1}
	pa := playerActionPacket(2, target, 1, 7)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	if got := loop.only().entities.len(); got != before {
		t.Fatalf("entity count = %d, want %d (no drop for air)", got, before)
	}
}

// --- LOOT-CONTEXT TOOL THREADING (silk_touch / fortune) -------------------------------------
//
// These exercise the block-break TOOL thread (blockBreakLootContext): a held tool's silk_touch /
// fortune enchantments reach the loot context so match_tool (has_silk_touch) + apply_bonus
// (ore_drops fortune) resolve to real reads. 1:1 with ServerPlayerGameMode.destroyBlock ->
// Block.getDrops(... , tool) + EnchantmentHelper.getItemEnchantmentLevel.

// enchantedTool builds a SlotData for `itemID` carrying the given enchantment-id -> level map, via
// the same stackComponentEdit path the grindstone/anvil use, so stackEnchantments reads it back.
func enchantedTool(itemID int32, ench map[string]int) component.SlotData {
	base := component.SlotData{Count: 1, ItemID: pk.VarInt(itemID)}
	e := editStack(base)
	e.setEnchantments(ench)
	return e.materialize()
}

// toolPlayer builds a block-break player holding `tool` in the selected hotbar slot (server-side).
func toolPlayer(loop *TickLoop, tool component.SlotData) *tickPlayer {
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), tool)
	return p
}

// TestSilkTouchPicksGlassWhole: glass has ONE pool gated on match_tool(silk_touch>=1). A hand break
// (no tool) drops NOTHING (the pool condition fails); a silk-touch tool drops the glass block whole.
func TestSilkTouchPicksGlassWhole(t *testing.T) {
	glassState := block.ToStateID[block.Glass{}]
	const seed int64 = 424242

	// Hand break (no tool): the silk_touch pool condition fails -> no drop.
	handDrops := blockDropsFor(glassState, seed, blockBreakLootContext(nil, seed))
	if len(handDrops) != 0 {
		t.Fatalf("bare-hand glass break dropped %d stacks, want 0 (no silk_touch)", len(handDrops))
	}

	// Silk-touch tool: the pool fires -> one glass block.
	loop, _ := newDropLoop()
	tool := enchantedTool(int32(item.DiamondPickaxe.ID), map[string]int{"minecraft:silk_touch": 1})
	p := toolPlayer(loop, tool)
	silkDrops := blockDropsFor(glassState, seed, blockBreakLootContext(p, seed))
	if len(silkDrops) != 1 {
		t.Fatalf("silk-touch glass break dropped %d stacks, want 1 (glass whole)", len(silkDrops))
	}
	if item.ID(silkDrops[0].ItemID) != item.Glass.ID {
		t.Fatalf("silk-touch glass drop item = %d, want glass %d", silkDrops[0].ItemID, item.Glass.ID)
	}
	if silkDrops[0].Count != 1 {
		t.Fatalf("silk-touch glass count = %d, want 1", silkDrops[0].Count)
	}
}

// TestFortuneMultipliesOreDrops: diamond_ore's fortune branch (apply_bonus ore_drops) multiplies the
// single diamond by (bonus+1) where bonus = max(0, nextInt(fortuneLevel+2)-1). A Fortune-III tool must
// (across seeds) sometimes yield >1 diamond, and a silk-touch tool instead picks the ore block whole.
func TestFortuneMultipliesOreDrops(t *testing.T) {
	oreState := block.ToStateID[block.DiamondOre{}]

	loop, _ := newDropLoop()
	fortuneTool := enchantedTool(int32(item.DiamondPickaxe.ID), map[string]int{"minecraft:fortune": 3})
	fp := toolPlayer(loop, fortuneTool)

	sawDiamond := false
	sawMultiple := false
	for s := int64(0); s < 200; s++ {
		drops := blockDropsFor(oreState, s, blockBreakLootContext(fp, s))
		for _, d := range drops {
			if item.ID(d.ItemID) != item.Diamond.ID {
				t.Fatalf("fortune diamond_ore drop item = %d, want diamond %d", d.ItemID, item.Diamond.ID)
			}
			if d.Count >= 1 {
				sawDiamond = true
			}
			if d.Count > 1 {
				sawMultiple = true
			}
		}
	}
	if !sawDiamond {
		t.Fatal("fortune diamond_ore never dropped a diamond across 200 seeds")
	}
	if !sawMultiple {
		t.Fatal("fortune-III diamond_ore never multiplied past 1 across 200 seeds (apply_bonus ore_drops not applied)")
	}

	// Silk-touch on the ore: the FIRST alternatives child (match_tool silk_touch) wins -> diamond_ore block.
	loop2, _ := newDropLoop()
	silkTool := enchantedTool(int32(item.DiamondPickaxe.ID), map[string]int{"minecraft:silk_touch": 1})
	sp := toolPlayer(loop2, silkTool)
	const seed int64 = 9
	silkDrops := blockDropsFor(oreState, seed, blockBreakLootContext(sp, seed))
	if len(silkDrops) != 1 || item.ID(silkDrops[0].ItemID) != item.DiamondOre.ID {
		t.Fatalf("silk-touch diamond_ore dropped %v, want one diamond_ore block", silkDrops)
	}
}

// TestBlockDropLevelZeroUnchanged: a hand break (no tool) and an enchant-less tool BOTH take the base
// no-tool/level-0 path — diamond_ore -> exactly one diamond. This pins that threading a tool with no
// relevant enchant does not change the base drop (the level-0 invariant).
func TestBlockDropLevelZeroUnchanged(t *testing.T) {
	oreState := block.ToStateID[block.DiamondOre{}]
	for s := int64(0); s < 50; s++ {
		hand := blockDropsFor(oreState, s, blockBreakLootContext(nil, s))
		if len(hand) != 1 || item.ID(hand[0].ItemID) != item.Diamond.ID || hand[0].Count != 1 {
			t.Fatalf("seed %d: hand diamond_ore = %v, want exactly one diamond", s, hand)
		}
		loop, _ := newDropLoop()
		plain := enchantedTool(int32(item.DiamondPickaxe.ID), map[string]int{}) // no relevant enchant
		p := toolPlayer(loop, plain)
		tooled := blockDropsFor(oreState, s, blockBreakLootContext(p, s))
		if len(tooled) != 1 || item.ID(tooled[0].ItemID) != item.Diamond.ID || tooled[0].Count != 1 {
			t.Fatalf("seed %d: enchant-less-tool diamond_ore = %v, want exactly one diamond", s, tooled)
		}
	}
}
