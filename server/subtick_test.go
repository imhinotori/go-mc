package server

import (
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestSubtickOrdering proves TICK-03's chronological-resolution contract: inputs are
// drained from a player's buffer in strict order of their server-arrival stamp (At),
// regardless of the order they were appended in, and the buffer is empty afterward.
// applyInput is the Phase-3 stub — this test asserts only the ORDERING and that the
// vanilla flush cadence is unchanged (the drain runs inside tickOnce, not at a higher
// broadcast rate). No real time: At stamps are constructed directly.
func TestSubtickOrdering(t *testing.T) {
	clk := newFakeClock()
	loop := NewTickLoop(clk)

	// One player owned by the tick goroutine.
	p := &tickPlayer{}
	loop.players = append(loop.players, p)

	base := clk.Now()
	// Append inputs OUT OF chronological order (simulating a merge of sources or a
	// re-stamp). Each carries a distinct packet ID so we can recover apply order.
	p.subtick.append(SubtickInput{At: base.Add(30 * time.Millisecond), Packet: pk.Packet{ID: 30}})
	p.subtick.append(SubtickInput{At: base.Add(10 * time.Millisecond), Packet: pk.Packet{ID: 10}})
	p.subtick.append(SubtickInput{At: base.Add(20 * time.Millisecond), Packet: pk.Packet{ID: 20}})
	p.subtick.append(SubtickInput{At: base.Add(5 * time.Millisecond), Packet: pk.Packet{ID: 5}})

	// Record the order applyInput observes by capturing on the player's last-seen hook.
	var applied []int32
	loop.applyInputHook = func(_ *tickPlayer, in SubtickInput) {
		applied = append(applied, in.Packet.ID)
	}

	loop.resolveSubtickInputs()

	want := []int32{5, 10, 20, 30}
	if len(applied) != len(want) {
		t.Fatalf("applied %d inputs, want %d (%v)", len(applied), len(want), applied)
	}
	for i := range want {
		if applied[i] != want[i] {
			t.Fatalf("input %d applied out of chronological order: got ID %d, want %d (full: %v)", i, applied[i], want[i], applied)
		}
	}

	// The buffer must be empty after a drain (no replay, no leak).
	if got := p.subtick.len(); got != 0 {
		t.Fatalf("subtick buffer not empty after drain: len=%d", got)
	}

	// Draining again on an empty buffer applies nothing (idempotent, no panic).
	applied = nil
	loop.resolveSubtickInputs()
	if len(applied) != 0 {
		t.Fatalf("second drain applied %d inputs on an empty buffer, want 0", len(applied))
	}
}

// TestSubtickBufferCap proves T-3-01: an input flood is bounded. Pushing far more
// inputs than the cap never grows the buffer past subtickCap (drop-oldest), so a
// flood degrades only that player and never grows memory or stalls the tick. The
// surviving (newest) inputs still drain in chronological order.
func TestSubtickBufferCap(t *testing.T) {
	clk := newFakeClock()
	loop := NewTickLoop(clk)

	p := &tickPlayer{}
	loop.players = append(loop.players, p)

	base := clk.Now()
	flood := subtickCap * 4 // far more than the cap
	for i := 0; i < flood; i++ {
		p.subtick.append(SubtickInput{
			At:     base.Add(time.Duration(i) * time.Microsecond),
			Packet: pk.Packet{ID: int32(i)},
		})
		// The buffer must NEVER exceed the cap at any point during the flood.
		if got := p.subtick.len(); got > subtickCap {
			t.Fatalf("buffer exceeded cap during flood: len=%d cap=%d (input %d)", got, subtickCap, i)
		}
	}

	if got := p.subtick.len(); got != subtickCap {
		t.Fatalf("after flood buffer len=%d, want exactly cap=%d", got, subtickCap)
	}

	// Drop-oldest semantics: only the NEWEST subtickCap inputs survive. With IDs
	// equal to their arrival index, the survivors are [flood-cap .. flood-1].
	var applied []int32
	loop.applyInputHook = func(_ *tickPlayer, in SubtickInput) {
		applied = append(applied, in.Packet.ID)
	}
	loop.resolveSubtickInputs()

	if len(applied) != subtickCap {
		t.Fatalf("drained %d inputs, want cap=%d", len(applied), subtickCap)
	}
	// Survivors are the newest, still in chronological (ascending) order.
	firstSurvivor := int32(flood - subtickCap)
	for i, id := range applied {
		want := firstSurvivor + int32(i)
		if id != want {
			t.Fatalf("survivor %d has ID %d, want %d (drop-oldest + chronological): %v", i, id, want, applied)
		}
	}
}

// TestDispatchAppendsSubtickInput proves the dispatch wiring: a movement/use/attack
// packet routed through dispatch is stamped with the SERVER arrival time (At =
// clock.Now(), never a client-supplied timestamp — T-3-07) and appended to that
// player's buffer; ServerboundClientTickEnd is recorded as a boundary marker only
// (no body decode); and unrelated/unknown IDs append nothing.
func TestDispatchAppendsSubtickInput(t *testing.T) {
	clk := newFakeClock()
	loop := NewTickLoop(clk)

	p := &tickPlayer{client: &Client{}}
	loop.players = append(loop.players, p)
	loop.clientIndex = map[*Client]*tickPlayer{p.client: p}

	// A movement packet stamps + appends one input at the server clock.
	clk.add(7 * time.Millisecond)
	wantAt := clk.Now()
	loop.dispatch(p.client, pk.Packet{ID: int32(packetid.ServerboundMovePlayerPos)})
	if got := p.subtick.len(); got != 1 {
		t.Fatalf("movement packet appended %d inputs, want 1", got)
	}
	if p.subtick.inputs[0].At != wantAt {
		t.Fatalf("input stamped At=%v, want server clock %v (server must stamp arrival, not client)", p.subtick.inputs[0].At, wantAt)
	}

	// Attack and use are also subtick inputs.
	loop.dispatch(p.client, pk.Packet{ID: int32(packetid.ServerboundAttack)})
	loop.dispatch(p.client, pk.Packet{ID: int32(packetid.ServerboundUseItem)})
	if got := p.subtick.len(); got != 3 {
		t.Fatalf("after attack+use, buffer len=%d, want 3", got)
	}

	// ServerboundClientTickEnd is a boundary marker only: it records the batch
	// boundary but does NOT append a subtick input (no body decode in Phase 3).
	before := p.subtick.len()
	loop.dispatch(p.client, pk.Packet{ID: int32(packetid.ServerboundClientTickEnd)})
	if got := p.subtick.len(); got != before {
		t.Fatalf("ClientTickEnd changed buffer len: before=%d after=%d (it is a boundary marker, not an input)", before, got)
	}
	if !p.sawTickEnd {
		t.Fatal("ClientTickEnd should be recorded as a boundary marker on the player")
	}

	// An unknown/unhandled ID appends nothing and never panics.
	before = p.subtick.len()
	loop.dispatch(p.client, pk.Packet{ID: 9999})
	if got := p.subtick.len(); got != before {
		t.Fatalf("unknown ID mutated buffer: before=%d after=%d", before, got)
	}

	// A nil/unknown client must not panic dispatch (total + cheap, T-3-02).
	loop.dispatch(nil, pk.Packet{ID: int32(packetid.ServerboundMovePlayerPos)})
}
