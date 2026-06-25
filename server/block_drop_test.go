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

// TestBlockDropLookup: blockDropFor maps a broken block state to its v1 drop. Stone drops
// cobblestone; dirt drops itself; air/unknown -> ok=false (no drop).
func TestBlockDropLookup(t *testing.T) {
	cases := []struct {
		name     string
		state    block.StateID
		wantOK   bool
		wantItem item.ID
	}{
		{"stone->cobblestone", block.ToStateID[block.Stone{}], true, item.Cobblestone.ID},
		{"dirt->dirt", block.ToStateID[block.Dirt{}], true, item.Dirt.ID},
		{"air->nothing", block.ToStateID[block.Air{}], false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			drop, ok := blockDropFor(c.state)
			if ok != c.wantOK {
				t.Fatalf("blockDropFor(%s) ok = %v, want %v", c.name, ok, c.wantOK)
			}
			if !ok {
				return
			}
			if item.ID(drop.ItemID) != c.wantItem {
				t.Fatalf("blockDropFor(%s) item = %d, want %d", c.name, drop.ItemID, c.wantItem)
			}
			if drop.Count <= 0 {
				t.Fatalf("blockDropFor(%s) count = %d, want > 0", c.name, drop.Count)
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
	loop.world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	return loop, mgr
}

// TestBlockDropSpawnsItem: breaking a stone block spawns an entity.Item (typ==71) in the
// store at the block center (x+0.5, y, z+0.5) carrying non-empty ITEM metadata.
func TestBlockDropSpawnsItem(t *testing.T) {
	loop, mgr := newDropLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	before := loop.entities.len()
	pa := playerActionPacket(2 /*STOP_DESTROY_BLOCK*/, target, 1, 7)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	if got := loop.entities.len(); got != before+1 {
		t.Fatalf("entity count = %d, want %d (one Item spawned)", got, before+1)
	}

	// Find the spawned Item: the one entity that is an Item type.
	var drop *Entity
	for _, e := range loop.entities.near(1.5, 1.5, trackRange) {
		if e.typ == entity.Item.ID {
			drop = e
			break
		}
	}
	if drop == nil {
		t.Fatalf("no Item entity (typ==%d) in the store after break", entity.Item.ID)
	}
	if drop.x != 1.5 || drop.z != 1.5 {
		t.Fatalf("item pos = (%v,_,%v), want block center (1.5,_,1.5)", drop.x, drop.z)
	}
	if len(drop.metadata) == 0 {
		t.Fatalf("item metadata empty — the Item would render INVISIBLE (Pitfall 5)")
	}
}

// TestBlockDropTracked: after a break, a nearby player's tracker tick emits an AddEntity for
// the new item id (the GAMEPLAY-01 broadcast path works for the dropped item).
func TestBlockDropTracked(t *testing.T) {
	loop, mgr := newDropLoop()
	editor := blockPlayer(loop, 1.5, 65.0, 1.5)
	editor.entityID = 1000 // distinct id so the tracker does not skip the item as "self"

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	pa := playerActionPacket(2, target, 1, 5)
	loop.applyInput(editor, SubtickInput{At: loop.clock.Now(), Packet: pa})

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
	before := loop.entities.len()
	target := pk.Position{X: 1, Y: 64, Z: 1}
	pa := playerActionPacket(2, target, 1, 7)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	if got := loop.entities.len(); got != before {
		t.Fatalf("entity count = %d, want %d (no drop for air)", got, before)
	}
}
