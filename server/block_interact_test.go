package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// block_interact_test.go covers ENT-03: the dispatch route for ServerboundPlayerAction
// (previously dropped at the default no-op), the place/break handlers, the
// BlockChangedAck + BlockUpdate reconciliation handshake, the broadcast to all chunk
// trackers, and the server-authoritative reject / defensive-decode paths.
//
// The serverbound packet builders below mirror the JAR-DERIVED proto-776 wire layouts
// (javap'd this session from temp/cache/26.2-inner.jar):
//   ServerboundPlayerAction = VarInt action + Position pos + UnsignedByte direction + VarInt sequence
//   ServerboundUseItemOn    = VarInt hand + (Position pos + VarInt direction + Float cursorX/Y/Z
//                              + Boolean insideBlock + Boolean worldBorderHit) + VarInt sequence
// (FriendlyByteBuf.readBlockHitResult reads BlockPos, Direction enum, 3 floats, then TWO
// booleans; ServerboundUseItemOnPacket reads hand FIRST, then the hit result, then sequence.)

// playerActionPacket builds a ServerboundPlayerAction with the jar-derived field order.
func playerActionPacket(action int32, pos pk.Position, direction uint8, seq int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundPlayerAction),
		pk.VarInt(action), pos, pk.UnsignedByte(direction), pk.VarInt(seq))
}

// useItemOnPacket builds a ServerboundUseItemOn with the jar-derived field order.
func useItemOnPacket(hand int32, pos pk.Position, direction int32, cx, cy, cz float32, inside, borderHit bool, seq int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundUseItemOn),
		pk.VarInt(hand),
		pos, pk.VarInt(direction),
		pk.Float(cx), pk.Float(cy), pk.Float(cz),
		pk.Boolean(inside), pk.Boolean(borderHit),
		pk.VarInt(seq))
}

// blockTestSecs is the overworld section count for the test world (Height/16 = 24).
const blockTestSecs = 24

// newBlockLoop wires a TickLoop with a tick-owned ChunkManager holding one ready, all-air
// chunk at column {0,0} (so SetBlock has a loaded column to mutate). No off-tick worker.
func newBlockLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	return loop, mgr
}

// blockPlayer registers a confirmed-teleport player at (x,y,z) with a capturing client,
// centered on {0,0} and tracking that column, so reach + broadcast checks have a basis.
func blockPlayer(loop *TickLoop, x, y, z float64) *tickPlayer {
	p := &tickPlayer{
		client:            captureClient(64),
		confirmedTeleport: true,
		x:                 x, y: y, z: z,
		center:     level.ChunkPos{0, 0},
		viewDist:   serverViewDistance,
		sentChunks: map[level.ChunkPos]bool{{0, 0}: true},
	}
	loop.players = append(loop.players, p)
	if loop.clientIndex == nil {
		loop.clientIndex = make(map[*Client]*tickPlayer)
	}
	loop.clientIndex[p.client] = p
	return p
}

// TestPlayerActionRouted: a ServerboundPlayerAction passed to dispatch is APPENDED to the
// player's subtick buffer (today it is dropped at the default no-op). UseItemOn stays
// routed too.
func TestPlayerActionRouted(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.0, 65.0, 1.0)

	if n := p.subtick.len(); n != 0 {
		t.Fatalf("fresh player subtick buffer = %d, want 0", n)
	}

	pa := playerActionPacket(2 /*STOP_DESTROY_BLOCK*/, pk.Position{X: 1, Y: 64, Z: 1}, 1, 7)
	loop.dispatch(p.client, pa)
	if n := p.subtick.len(); n != 1 {
		t.Fatalf("after dispatch(PlayerAction) subtick len = %d, want 1 (the route was missing)", n)
	}

	ui := useItemOnPacket(0, pk.Position{X: 1, Y: 64, Z: 1}, 1, 0.5, 1.0, 0.5, false, false, 8)
	loop.dispatch(p.client, ui)
	if n := p.subtick.len(); n != 2 {
		t.Fatalf("after dispatch(UseItemOn) subtick len = %d, want 2 (UseItemOn must remain routed)", n)
	}
}

// TestBreakBlock: a PlayerAction (STOP_DESTROY_BLOCK) on a loaded, reachable solid block
// sets it to air (world.GetBlock now air) and the editor receives BOTH
// ClientboundBlockChangedAck(sequence) AND ClientboundBlockUpdate(pos, airState).
func TestBreakBlock(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)

	// Place a stone block at (1,64,1) so there is something to break.
	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	const seq = 42
	pa := playerActionPacket(2 /*STOP_DESTROY_BLOCK*/, target, 1, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	// The world block is now air.
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("after break, GetBlock = (%v, ok=%v), want air", got, ok)
	}

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundBlockChangedAck); n != 1 {
		t.Fatalf("BlockChangedAck sent %d times, want exactly 1 (omitting it -> ghost block)", n)
	}
	if n := countID(got, packetid.ClientboundBlockUpdate); n != 1 {
		t.Fatalf("BlockUpdate sent %d times to the editor, want exactly 1", n)
	}
	// The ack carries the action's sequence.
	if seqGot := findAckSequence(t, got); seqGot != seq {
		t.Fatalf("BlockChangedAck sequence = %d, want %d", seqGot, seq)
	}
}

// setHeldItem puts a stack of `count` of the given item into the player's selected hotbar
// window slot (36+heldSlot) so handleUseItemOn's held-item resolution has something to place.
// Mirrors a survival player holding a block item in the active hotbar slot.
func setHeldItem(p *tickPlayer, itemID item.ID, count int) {
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{
		Count:  pk.VarInt(count),
		ItemID: pk.VarInt(itemID),
	})
}

// TestPlaceBlock: a UseItemOn while HOLDING a stone item, on a loaded reachable face, places the
// HELD block (stone) at the pos ADJACENT to the hit face and sends ack + BlockUpdate. (Plan 17-17:
// the placed block now comes from the held item, not a hardcoded stone.)
func TestPlaceBlock(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	setHeldItem(p, item.Stone.ID, 5) // holding a stack of 5 stone

	// Hit the TOP face (direction UP = 1) of the block at (1,64,1); placement lands at (1,65,1).
	hit := pk.Position{X: 1, Y: 64, Z: 1}
	placed := pk.Position{X: 1, Y: 65, Z: 1}
	// Give the hit block a solid (non-replaceable) stone so placement lands on the adjacent face.
	mgr.SetBlock(hit, block.ToStateID[block.Stone{}], dimMinY)
	const seq = 99
	ui := useItemOnPacket(0 /*main hand*/, hit, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// The adjacent position is now stone (the held block).
	got, ok := mgr.GetBlock(placed, dimMinY)
	if !ok || got != block.ToStateID[block.Stone{}] {
		t.Fatalf("after place, GetBlock(adjacent) = (%v, ok=%v), want stone %d", got, ok, block.ToStateID[block.Stone{}])
	}

	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundBlockChangedAck); n != 1 {
		t.Fatalf("place: BlockChangedAck sent %d times, want 1", n)
	}
	if n := countID(pkts, packetid.ClientboundBlockUpdate); n != 1 {
		t.Fatalf("place: BlockUpdate sent %d times, want 1", n)
	}
}

// TestPlaceEmptyHandNoBlock: a UseItemOn with an EMPTY main hand places NOTHING (the core bugfix —
// previously the server hardcoded a stone block regardless of the held item). No mutation, no ack.
func TestPlaceEmptyHandNoBlock(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5) // no setHeldItem -> empty hand

	hit := pk.Position{X: 1, Y: 64, Z: 1}
	placed := pk.Position{X: 1, Y: 65, Z: 1}
	mgr.SetBlock(hit, block.ToStateID[block.Stone{}], dimMinY)
	ui := useItemOnPacket(0, hit, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 1)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// Nothing placed: the adjacent block is still air.
	if got, ok := mgr.GetBlock(placed, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("empty-hand place mutated the world: GetBlock(adjacent) = (%v, ok=%v), want air", got, ok)
	}
	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundBlockChangedAck); n != 0 {
		t.Fatalf("empty-hand place acked (%d), want 0 (no placement)", n)
	}
	if n := countID(pkts, packetid.ClientboundBlockUpdate); n != 0 {
		t.Fatalf("empty-hand place broadcast (%d), want 0", n)
	}
}

// TestPlaceSurvivalShrinksStack: placing a held block in SURVIVAL shrinks the held stack by 1 and
// sends a ClientboundContainerSetSlot reflecting the new count (BlockItem.place -> stack.consume(1)).
func TestPlaceSurvivalShrinksStack(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.Stone.ID, 3) // 3 stone

	hit := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(hit, block.ToStateID[block.Stone{}], dimMinY)
	ui := useItemOnPacket(0, hit, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 7)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)); int(got.Count) != 2 {
		t.Fatalf("survival place: held count = %d, want 2 (consume(1))", got.Count)
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundContainerSetSlot); n < 1 {
		t.Fatalf("survival place: ContainerSetSlot sent %d times, want >=1 (slot sync)", n)
	}
}

// TestPlaceSurvivalLastItemEmptiesSlot: placing the LAST item in survival empties the slot.
func TestPlaceSurvivalLastItemEmptiesSlot(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.Stone.ID, 1) // the last stone

	hit := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(hit, block.ToStateID[block.Stone{}], dimMinY)
	ui := useItemOnPacket(0, hit, 1, 0.5, 1.0, 0.5, false, false, 8)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count > 0 {
		t.Fatalf("survival place of last item: held count = %d, want 0 (empty slot)", got.Count)
	}
}

// TestPlaceCreativeNoShrink: placing a held block in CREATIVE places it but does NOT shrink the
// stack (ItemStack.consume short-circuits on hasInfiniteMaterials()).
func TestPlaceCreativeNoShrink(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeCreative
	setHeldItem(p, item.Stone.ID, 64)

	hit := pk.Position{X: 1, Y: 64, Z: 1}
	placed := pk.Position{X: 1, Y: 65, Z: 1}
	mgr.SetBlock(hit, block.ToStateID[block.Stone{}], dimMinY)
	ui := useItemOnPacket(0, hit, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// Block placed.
	if got, ok := mgr.GetBlock(placed, dimMinY); !ok || got != block.ToStateID[block.Stone{}] {
		t.Fatalf("creative place: GetBlock(adjacent) = (%v, ok=%v), want stone", got, ok)
	}
	// Stack NOT shrunk.
	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)); int(got.Count) != 64 {
		t.Fatalf("creative place: held count = %d, want 64 (no shrink)", got.Count)
	}
}

// TestPlaceIntoSolidNoOp: a UseItemOn whose ADJACENT target is a SOLID (non-replaceable) block
// places nothing (BlockPlaceContext.canPlace() is false). No mutation, no ack.
func TestPlaceIntoSolidNoOp(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	setHeldItem(p, item.Stone.ID, 5)

	hit := pk.Position{X: 1, Y: 64, Z: 1}
	placed := pk.Position{X: 1, Y: 65, Z: 1}
	// Both the hit block AND the adjacent target are solid stone -> the target is not replaceable.
	mgr.SetBlock(hit, block.ToStateID[block.Stone{}], dimMinY)
	mgr.SetBlock(placed, block.ToStateID[block.Dirt{}], dimMinY) // a different solid so we can detect overwrite
	ui := useItemOnPacket(0, hit, 1, 0.5, 1.0, 0.5, false, false, 10)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// The solid adjacent target is untouched (still dirt, not overwritten by stone).
	if got, ok := mgr.GetBlock(placed, dimMinY); !ok || got != block.ToStateID[block.Dirt{}] {
		t.Fatalf("place into solid overwrote it: GetBlock = (%v, ok=%v), want unchanged dirt", got, ok)
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundBlockChangedAck); n != 0 {
		t.Fatalf("place into solid acked (%d), want 0 (canPlace false -> no-op)", n)
	}
}

// TestPlaceNonBlockItemNoOp: holding a NON-block item (e.g. a wooden sword) places nothing
// (Block.byItem -> Blocks.AIR for a non-BlockItem).
func TestPlaceNonBlockItemNoOp(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	setHeldItem(p, item.WoodenSword.ID, 1) // a tool, not a block item

	hit := pk.Position{X: 1, Y: 64, Z: 1}
	placed := pk.Position{X: 1, Y: 65, Z: 1}
	mgr.SetBlock(hit, block.ToStateID[block.Stone{}], dimMinY)
	ui := useItemOnPacket(0, hit, 1, 0.5, 1.0, 0.5, false, false, 11)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if got, ok := mgr.GetBlock(placed, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("non-block item placed something: GetBlock(adjacent) = (%v, ok=%v), want air", got, ok)
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundBlockChangedAck); n != 0 {
		t.Fatalf("non-block item acked (%d), want 0", n)
	}
}

// TestPlaceReplaceClicked: clicking a REPLACEABLE block (water) with a held block REPLACES it in
// place (BlockPlaceContext.replaceClicked -> getClickedPos == hitPos), not on the adjacent face.
func TestPlaceReplaceClicked(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	setHeldItem(p, item.Stone.ID, 5)

	hit := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(hit, block.ToStateID[block.Water{}], dimMinY) // clicked block is replaceable water
	ui := useItemOnPacket(0, hit, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 12)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// The CLICKED pos itself becomes stone (replaced in place), not the block above it.
	if got, ok := mgr.GetBlock(hit, dimMinY); !ok || got != block.ToStateID[block.Stone{}] {
		t.Fatalf("replace-clicked: GetBlock(hit) = (%v, ok=%v), want stone (replaced in place)", got, ok)
	}
}

// TestBlockBroadcastToTrackers: a second player tracking the edited chunk ALSO receives the
// ClientboundBlockUpdate (broadcast hits every tracker, editor included).
func TestBlockBroadcastToTrackers(t *testing.T) {
	loop, mgr := newBlockLoop()
	editor := blockPlayer(loop, 1.5, 65.0, 1.5)
	observer := blockPlayer(loop, 2.5, 65.0, 2.5) // also tracks {0,0}

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	pa := playerActionPacket(2, target, 1, 5)
	loop.applyInput(editor, SubtickInput{At: loop.clock.Now(), Packet: pa})

	gotEditor := drainPackets(editor.client)
	gotObserver := drainPackets(observer.client)
	if n := countID(gotEditor, packetid.ClientboundBlockUpdate); n != 1 {
		t.Fatalf("editor BlockUpdate = %d, want 1", n)
	}
	if n := countID(gotObserver, packetid.ClientboundBlockUpdate); n != 1 {
		t.Fatalf("observer BlockUpdate = %d, want 1 (broadcast must reach all trackers)", n)
	}
	// The observer is NOT the editor: it must NOT receive an ack (the ack is editor-only).
	if n := countID(gotObserver, packetid.ClientboundBlockChangedAck); n != 0 {
		t.Fatalf("observer BlockChangedAck = %d, want 0 (ack is editor-only)", n)
	}
}

// TestBlockReachRejected: an out-of-reach edit performs NO mutation and NO ack. A
// not-tracking observer does not receive the broadcast either.
func TestBlockReachRejected(t *testing.T) {
	loop, mgr := newBlockLoop()
	// Player far from the target (well beyond reach).
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	target := pk.Position{X: 12, Y: 64, Z: 12} // ~15 blocks away
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	pa := playerActionPacket(2, target, 1, 11)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	// No mutation: still stone.
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("out-of-reach break mutated the world: GetBlock = (%v, ok=%v), want still-solid", got, ok)
	}
	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundBlockChangedAck); n != 0 {
		t.Fatalf("out-of-reach edit acked (%d), want 0 (reject is a silent no-op)", n)
	}
	if n := countID(pkts, packetid.ClientboundBlockUpdate); n != 0 {
		t.Fatalf("out-of-reach edit broadcast (%d), want 0", n)
	}
}

// TestBlockMalformed: a truncated PlayerAction/UseItemOn payload Scan-errors to a no-op
// (no mutation, no ack, no panic). Also covers an edit on an UNLOADED column.
func TestBlockMalformed(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)

	// A truncated PlayerAction (only the action varint, missing pos/dir/seq).
	bad := pk.Marshal(int32(packetid.ServerboundPlayerAction), pk.VarInt(2))
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: bad}) // must not panic

	// An edit targeting an UNLOADED column: reachable distance but no chunk there.
	unloaded := pk.Position{X: 200, Y: 64, Z: 200}
	farP := blockPlayer(loop, 200.5, 65.0, 200.5) // near the unloaded target so reach passes
	pa := playerActionPacket(2, unloaded, 1, 3)
	loop.applyInput(farP, SubtickInput{At: loop.clock.Now(), Packet: pa})

	// No ack from either (malformed -> no-op; unloaded -> SetBlock changed=false).
	if n := countID(drainPackets(p.client), packetid.ClientboundBlockChangedAck); n != 0 {
		t.Fatalf("malformed PlayerAction produced an ack (%d), want 0", n)
	}
	if n := countID(drainPackets(farP.client), packetid.ClientboundBlockChangedAck); n != 0 {
		t.Fatalf("unloaded-column edit produced an ack (%d), want 0", n)
	}
	// Nothing got placed in the unloaded column.
	if _, ok := mgr.GetBlock(unloaded, dimMinY); ok {
		t.Fatalf("unloaded-column edit created a block, want none")
	}
}

// findAckSequence decodes the first ClientboundBlockChangedAck's VarInt sequence.
func findAckSequence(t *testing.T, ps []pk.Packet) int32 {
	t.Helper()
	for _, p := range ps {
		if p.ID == int32(packetid.ClientboundBlockChangedAck) {
			var seq pk.VarInt
			if err := p.Scan(&seq); err != nil {
				t.Fatalf("decode BlockChangedAck sequence: %v", err)
			}
			return int32(seq)
		}
	}
	t.Fatalf("no BlockChangedAck packet found")
	return 0
}
