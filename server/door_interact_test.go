package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// door_interact_test.go covers D-I1: DoorBlock / TrapDoorBlock / FenceGateBlock.useWithoutItem
// (right-click open/close), driven through useBlockInteraction (the useItemOn step-1 block hook).
// Each test sets a known door state in the tick-owned world, calls the interaction, and asserts
// (1) the resulting OPEN block-state and (2) the emitted ClientboundSound registry id. The
// double-door half sync, the iron material hand-gate, and the fence-gate face-the-player flip
// are each pinned.

// soundIDOf decodes the leading VarInt (soundID+1) of a ClientboundSound packet.
func soundIDOf(t *testing.T, p pk.Packet) int32 {
	t.Helper()
	var raw pk.VarInt
	if err := p.Scan(&raw); err != nil {
		t.Fatalf("scan sound id: %v", err)
	}
	return int32(raw) - 1
}

// firstSound returns the first ClientboundSound in the drained set, failing if none.
func firstSound(t *testing.T, ps []pk.Packet) pk.Packet {
	t.Helper()
	for _, p := range ps {
		if p.ID == int32(packetid.ClientboundSound) {
			return p
		}
	}
	t.Fatalf("no ClientboundSound packet emitted")
	return pk.Packet{}
}

// doorLoop builds a block loop + a player at the door column so reach passes.
func doorLoop(t *testing.T) (*TickLoop, *tickPlayer) {
	t.Helper()
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	return loop, p
}

// resetClient swaps in a fresh capturing client because drainPackets closes the previous one; a
// second interaction in the same test needs a live client to receive its broadcast/sound.
func resetClient(loop *TickLoop, p *tickPlayer) {
	old := p.client
	p.client = captureClient(64)
	if loop.clientIndex != nil {
		delete(loop.clientIndex, old)
		loop.clientIndex[p.client] = p
	}
}

// TestDoorOpenClose: right-click a closed wooden door -> both halves OPEN + WOODEN_DOOR_OPEN; again
// -> both closed + WOODEN_DOOR_CLOSE.
func TestDoorOpenClose(t *testing.T) {
	loop, p := doorLoop(t)
	mgr := loop.only().world

	lowerPos := pk.Position{X: 8, Y: 64, Z: 8}
	upperPos := pk.Position{X: 8, Y: 65, Z: 8}
	loSID := block.ToStateID[block.OakDoor{Facing: block.North, Half: block.DoubleBlockHalfLower, Hinge: block.DoorHingeSideLeft}]
	upSID := block.ToStateID[block.OakDoor{Facing: block.North, Half: block.DoubleBlockHalfUpper, Hinge: block.DoorHingeSideLeft}]
	mgr.SetBlock(lowerPos, loSID, dimMinY)
	mgr.SetBlock(upperPos, upSID, dimMinY)

	if !loop.useBlockInteraction(p, lowerPos, 1) {
		t.Fatalf("useBlockInteraction on closed door returned false, want consumed")
	}
	loNow, _ := mgr.GetBlock(lowerPos, dimMinY)
	upNow, _ := mgr.GetBlock(upperPos, dimMinY)
	if !block.DoorOpen(loNow) {
		t.Fatalf("lower half OPEN=false after open click, want true")
	}
	if !block.DoorOpen(upNow) {
		t.Fatalf("upper half OPEN=false after open click, want true (double-door half sync)")
	}
	if got := soundIDOf(t, firstSound(t, drainPackets(p.client))); got != 1846 {
		t.Fatalf("open sound id = %d, want 1846 (block.wooden_door.open)", got)
	}

	resetClient(loop, p)
	if !loop.useBlockInteraction(p, lowerPos, 1) {
		t.Fatalf("useBlockInteraction on open door returned false, want consumed")
	}
	loNow, _ = mgr.GetBlock(lowerPos, dimMinY)
	upNow, _ = mgr.GetBlock(upperPos, dimMinY)
	if block.DoorOpen(loNow) {
		t.Fatalf("lower half OPEN=true after close click, want false")
	}
	if block.DoorOpen(upNow) {
		t.Fatalf("upper half OPEN=true after close click, want false (double-door half sync)")
	}
	if got := soundIDOf(t, firstSound(t, drainPackets(p.client))); got != 1845 {
		t.Fatalf("close sound id = %d, want 1845 (block.wooden_door.close)", got)
	}
}

// TestDoorHalfSyncFromUpper: clicking the UPPER half also syncs the LOWER half's OPEN.
func TestDoorHalfSyncFromUpper(t *testing.T) {
	loop, p := doorLoop(t)
	mgr := loop.only().world

	lowerPos := pk.Position{X: 8, Y: 64, Z: 8}
	upperPos := pk.Position{X: 8, Y: 65, Z: 8}
	mgr.SetBlock(lowerPos, block.ToStateID[block.OakDoor{Facing: block.North, Half: block.DoubleBlockHalfLower, Hinge: block.DoorHingeSideLeft}], dimMinY)
	mgr.SetBlock(upperPos, block.ToStateID[block.OakDoor{Facing: block.North, Half: block.DoubleBlockHalfUpper, Hinge: block.DoorHingeSideLeft}], dimMinY)

	if !loop.useBlockInteraction(p, upperPos, 1) {
		t.Fatalf("useBlockInteraction on upper half returned false")
	}
	loNow, _ := mgr.GetBlock(lowerPos, dimMinY)
	if !block.DoorOpen(loNow) {
		t.Fatalf("lower half not synced open when clicking upper half")
	}
}

// TestDoorIronRejectsHand: an iron door does NOT toggle on a hand click (canOpenByHand=false).
func TestDoorIronRejectsHand(t *testing.T) {
	loop, p := doorLoop(t)
	mgr := loop.only().world

	lowerPos := pk.Position{X: 8, Y: 64, Z: 8}
	upperPos := pk.Position{X: 8, Y: 65, Z: 8}
	loSID := block.ToStateID[block.IronDoor{Facing: block.North, Half: block.DoubleBlockHalfLower, Hinge: block.DoorHingeSideLeft}]
	upSID := block.ToStateID[block.IronDoor{Facing: block.North, Half: block.DoubleBlockHalfUpper, Hinge: block.DoorHingeSideLeft}]
	mgr.SetBlock(lowerPos, loSID, dimMinY)
	mgr.SetBlock(upperPos, upSID, dimMinY)

	if loop.useBlockInteraction(p, lowerPos, 1) {
		t.Fatalf("iron door hand-click was consumed, want false (PASS, not hand-openable)")
	}
	loNow, _ := mgr.GetBlock(lowerPos, dimMinY)
	if block.DoorOpen(loNow) {
		t.Fatalf("iron door toggled OPEN on a hand click, want unchanged (closed)")
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundSound); n != 0 {
		t.Fatalf("iron door emitted %d sounds on hand-reject, want 0", n)
	}
}

// TestTrapdoorToggle: a wooden trapdoor toggles OPEN + plays WOODEN_TRAPDOOR_OPEN/CLOSE.
func TestTrapdoorToggle(t *testing.T) {
	loop, p := doorLoop(t)
	mgr := loop.only().world

	pos := pk.Position{X: 8, Y: 64, Z: 8}
	sid := block.ToStateID[block.OakTrapdoor{Facing: block.North, Half: block.Bottom}]
	mgr.SetBlock(pos, sid, dimMinY)

	if !loop.useBlockInteraction(p, pos, 1) {
		t.Fatalf("trapdoor click returned false, want consumed")
	}
	now, _ := mgr.GetBlock(pos, dimMinY)
	if !block.DoorOpen(now) {
		t.Fatalf("trapdoor OPEN=false after click, want true")
	}
	if got := soundIDOf(t, firstSound(t, drainPackets(p.client))); got != 1848 {
		t.Fatalf("trapdoor open sound id = %d, want 1848 (block.wooden_trapdoor.open)", got)
	}

	resetClient(loop, p)
	if !loop.useBlockInteraction(p, pos, 1) {
		t.Fatalf("second trapdoor click returned false")
	}
	now, _ = mgr.GetBlock(pos, dimMinY)
	if block.DoorOpen(now) {
		t.Fatalf("trapdoor OPEN=true after second click, want false")
	}
	if got := soundIDOf(t, firstSound(t, drainPackets(p.client))); got != 1847 {
		t.Fatalf("trapdoor close sound id = %d, want 1847 (block.wooden_trapdoor.close)", got)
	}
}

// TestTrapdoorIronRejectsHand: an iron trapdoor does not toggle on a hand click.
func TestTrapdoorIronRejectsHand(t *testing.T) {
	loop, p := doorLoop(t)
	mgr := loop.only().world

	pos := pk.Position{X: 8, Y: 64, Z: 8}
	sid := block.ToStateID[block.IronTrapdoor{Facing: block.North, Half: block.Bottom}]
	mgr.SetBlock(pos, sid, dimMinY)

	if loop.useBlockInteraction(p, pos, 1) {
		t.Fatalf("iron trapdoor hand-click was consumed, want false (PASS)")
	}
	now, _ := mgr.GetBlock(pos, dimMinY)
	if block.DoorOpen(now) {
		t.Fatalf("iron trapdoor toggled on a hand click, want unchanged")
	}
}

// TestFenceGateOpensFacingPlayer: a closed gate FACING the opposite of the player flips FACING to
// face the player on open, sets OPEN=true, and plays FENCE_GATE_OPEN; a second click closes it.
func TestFenceGateOpensFacingPlayer(t *testing.T) {
	loop, p := doorLoop(t)
	mgr := loop.only().world

	// yaw 0 -> getDirection() == SOUTH; gate FACING NORTH (== SOUTH.getOpposite()) must flip to SOUTH.
	p.yaw = 0
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	sid := block.ToStateID[block.OakFenceGate{Facing: block.North}]
	mgr.SetBlock(pos, sid, dimMinY)

	if !loop.useBlockInteraction(p, pos, 1) {
		t.Fatalf("fence gate click returned false, want consumed")
	}
	now, _ := mgr.GetBlock(pos, dimMinY)
	if !block.DoorOpen(now) {
		t.Fatalf("fence gate OPEN=false after click, want true")
	}
	if facing, ok := block.FenceGateFacing(now); !ok || facing != block.South {
		t.Fatalf("fence gate FACING = %v after opening, want south (faces the player)", facing)
	}
	if got := soundIDOf(t, firstSound(t, drainPackets(p.client))); got != 625 {
		t.Fatalf("fence gate open sound id = %d, want 625 (block.fence_gate.open)", got)
	}

	resetClient(loop, p)
	if !loop.useBlockInteraction(p, pos, 1) {
		t.Fatalf("second fence gate click returned false")
	}
	now, _ = mgr.GetBlock(pos, dimMinY)
	if block.DoorOpen(now) {
		t.Fatalf("fence gate OPEN=true after close click, want false")
	}
	if got := soundIDOf(t, firstSound(t, drainPackets(p.client))); got != 624 {
		t.Fatalf("fence gate close sound id = %d, want 624 (block.fence_gate.close)", got)
	}
}

// TestFenceGateKeepsFacingWhenAligned: a gate already facing the player does not flip FACING on open.
func TestFenceGateKeepsFacingWhenAligned(t *testing.T) {
	loop, p := doorLoop(t)
	mgr := loop.only().world

	p.yaw = 0 // getDirection() == SOUTH
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	sid := block.ToStateID[block.OakFenceGate{Facing: block.South}]
	mgr.SetBlock(pos, sid, dimMinY)

	if !loop.useBlockInteraction(p, pos, 1) {
		t.Fatalf("fence gate click returned false")
	}
	now, _ := mgr.GetBlock(pos, dimMinY)
	if !block.DoorOpen(now) {
		t.Fatalf("fence gate not opened")
	}
	if facing, ok := block.FenceGateFacing(now); !ok || facing != block.South {
		t.Fatalf("aligned fence gate FACING = %v, want south (unchanged)", facing)
	}
}
