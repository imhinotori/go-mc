package server

import (
	"sort"
	"time"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// subtickCap bounds each player's per-tick subtick input buffer. CS2-style subtick
// timestamps inputs at high frequency but resolves them at the fixed tick; a tick is
// 50ms, so even an aggressive input stream queues only a handful of inputs per tick.
// The cap exists purely as the T-3-01 DoS bound: a flooding client can never grow
// this buffer (or the tick's drain cost) unbounded — its oldest queued inputs are
// dropped first. 256 is generously above any legitimate per-tick input count while
// staying a trivial fixed allocation.
const subtickCap = 256

// SubtickInput is a single µs-timestamped client input awaiting in-tick resolution
// (TICK-03). At is the SERVER arrival stamp captured from the tick's injectable clock
// — it is NEVER a client-supplied timestamp (T-3-07): the client does not get to
// reorder inputs via time-travel. Packet is the raw movement/use/attack intent; its
// body is decoded by applyInput in Phase 6, not here.
type SubtickInput struct {
	At     time.Time
	Packet pk.Packet
}

// subtickBuffer is a BOUNDED per-player input buffer owned by the tick goroutine
// (single-owner discipline — no mutex, no xsync; the read goroutine only sends an
// Intent, dispatch appends here on the owner goroutine). append drops the oldest
// input on overflow (T-3-01) so a flood degrades only that player and never grows
// memory or stalls the tick. drain returns inputs in chronological order (by At) and
// empties the buffer.
type subtickBuffer struct {
	inputs []SubtickInput
}

// append adds one server-stamped input. When the buffer is already at subtickCap it
// drops the OLDEST queued input (front) and appends the new one at the back, so the
// buffer length is hard-bounded by subtickCap regardless of input rate (T-3-01). A
// flooding client thus loses its own stale inputs first — it can never stall the tick
// or grow memory.
func (b *subtickBuffer) append(in SubtickInput) {
	if len(b.inputs) >= subtickCap {
		// Drop-oldest: shift the window forward by one. Reusing the backing array
		// (copy + reslice) keeps this allocation-free in steady state.
		copy(b.inputs, b.inputs[1:])
		b.inputs[len(b.inputs)-1] = in
		return
	}
	b.inputs = append(b.inputs, in)
}

// drain returns the buffered inputs in strict chronological order (by At) and clears
// the buffer. Inputs arriving on a single channel are already ascending by arrival,
// but drain sorts defensively (stable, by At) so a merge of sources or a re-stamp
// still resolves chronologically — the load-bearing TICK-03 contract. The returned
// slice is the buffer's own backing array detached from the buffer; the buffer is
// reset to empty (length 0, capacity retained) for the next tick.
func (b *subtickBuffer) drain() []SubtickInput {
	if len(b.inputs) == 0 {
		return nil
	}
	out := b.inputs
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	// Reset to a fresh empty slice so the drained backing array is not mutated by the
	// next tick's appends while a caller still reads `out`.
	b.inputs = nil
	return out
}

// len reports the current buffered input count (test/observability helper).
func (b *subtickBuffer) len() int { return len(b.inputs) }

// applyInput is the MINIMAL Phase-3 resolution of one subtick input. It does NOT run
// movement/collision/hit-detection/projectile math — that is Phase 6 (ENT-02). Today
// it is a documented STUB: it records the player's last-seen input stamp so the seam
// observably "resolves" each input in chronological order, proving TICK-03's contract
// (timestamped capture, chronological in-tick resolution, vanilla broadcast rate)
// without any physics. The applyInputHook test seam lets tests observe apply order
// without depending on physics that does not exist yet.
func (t *TickLoop) applyInput(p *tickPlayer, in SubtickInput) {
	if t.applyInputHook != nil {
		t.applyInputHook(p, in)
	}
	// Phase-3 stub: record the last input we resolved for this player. Phase 6 replaces
	// this body with real movement/collision/hit-detection against in.Packet's body.
	p.lastInputAt = in.At
}
