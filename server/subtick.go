package server

import (
	"math"
	"sort"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
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

// movementFlagOnGround / movementFlagHorizontalCollision are the bit masks of the
// PACKED FLAGS BYTE that terminates every ServerboundMovePlayer* packet in 26.2 — NOT a
// Boolean onGround (the ≤773 wiki is wrong; this is a 1.21.3+ shift). Jar-verified:
// ServerboundMovePlayerPacket.unpackOnGround masks &1 and unpackHorizontalCollision
// masks &2; all four subclasses end their read() in readUnsignedByte. Decoding the
// trailing field as a Boolean would mis-frame the stream (rubber-band / Scan EOF).
const (
	movementFlagOnGround            = 0x01
	movementFlagHorizontalCollision = 0x02
)

// applyInput resolves one subtick movement input on the tick goroutine (PLAY-04). It
// decodes the four jar-confirmed ServerboundMovePlayer* layouts into tick-owned position
// and, on a chunk-column crossing, re-centers the view ring so the world follows the
// player. The applyInputHook test seam runs FIRST (before the teleport gate) so the
// Phase-3 subtick-ordering tests — which drive an unconfirmed player and assert the hook
// fires for every input — keep observing apply order. Movement is then GATED on the
// confirmed teleport (PLAY-02 / T-5-03): an unconfirmed player's movement is dropped.
//
// Every Scan returns on error WITHOUT mutating position (T-5-02): a malformed/short
// payload is a no-op, never a panic. Runs only on the owner goroutine over tick-owned
// state (TICK-05 / T-5-06).
func (t *TickLoop) applyInput(p *tickPlayer, in SubtickInput) {
	// Hook FIRST (before the gate): preserves the Phase-3 subtick-ordering observability
	// for unconfirmed players (the existing TestSubtickOrdering/TestSubtickBufferCap rely
	// on the hook firing for every input regardless of confirmedTeleport).
	if t.applyInputHook != nil {
		t.applyInputHook(p, in)
	}
	p.lastInputAt = in.At

	// Teleport gate (PLAY-02): drop movement until the client has acknowledged the
	// bootstrap spawn teleport, so pre-confirm packets cannot fight the authoritative
	// spawn (no rubber-band — T-5-03).
	if !p.confirmedTeleport {
		return
	}

	switch packetid.ServerboundPacketID(in.Packet.ID) {
	case packetid.ServerboundMovePlayerPos:
		// Double x,y,z + UnsignedByte flags.
		var x, y, z pk.Double
		var flags pk.UnsignedByte
		if err := in.Packet.Scan(&x, &y, &z, &flags); err != nil {
			return // malformed/short: no mutation (T-5-02)
		}
		// Authoritative anti-clip-through (ENT-02 / T-6-06): collide the client-CLAIMED
		// position per-axis against solid world blocks BEFORE accepting it. The server
		// corrects a clip-through claim rather than trusting the raw position. In an empty
		// world (no solid blocks in the path) the claim is returned verbatim.
		nx, ny, nz := t.collidePlayer(p, float64(x), float64(y), float64(z))
		// Water physics (GAMEPLAY-05 / Plan 17-13): apply LivingEntity.travelInWater's
		// getWaterSlowDown (0.8 horizontal) + Entity.updateFluidInteraction's buoyant push
		// (0.014 vertical) to the ACCEPTED movement delta. See moveWithFluidPhysics.
		p.x, p.y, p.z = t.moveWithFluidPhysics(p, nx, ny, nz)
		p.onGround = flags&movementFlagOnGround != 0
		t.maybeRecenter(p)

	case packetid.ServerboundMovePlayerPosRot:
		// Double x,y,z + Float yaw,pitch + UnsignedByte flags.
		var x, y, z pk.Double
		var yaw, pitch pk.Float
		var flags pk.UnsignedByte
		if err := in.Packet.Scan(&x, &y, &z, &yaw, &pitch, &flags); err != nil {
			return
		}
		// Authoritative anti-clip-through (ENT-02 / T-6-06): collide the claimed position
		// per-axis before accepting it (same as the Pos variant). Look angles are accepted
		// as sent — only the POSITION is collided.
		nx, ny, nz := t.collidePlayer(p, float64(x), float64(y), float64(z))
		// Water physics (GAMEPLAY-05 / Plan 17-13): same accepted-delta fluid pass as the
		// Pos variant (0.8 horizontal slowdown + 0.014 buoyant push when in water).
		p.x, p.y, p.z = t.moveWithFluidPhysics(p, nx, ny, nz)
		p.yaw, p.pitch = float32(yaw), float32(pitch)
		p.onGround = flags&movementFlagOnGround != 0
		t.maybeRecenter(p)

	case packetid.ServerboundMovePlayerRot:
		// Float yaw,pitch + UnsignedByte flags. No position change → no re-center.
		var yaw, pitch pk.Float
		var flags pk.UnsignedByte
		if err := in.Packet.Scan(&yaw, &pitch, &flags); err != nil {
			return
		}
		p.yaw, p.pitch = float32(yaw), float32(pitch)
		p.onGround = flags&movementFlagOnGround != 0

	case packetid.ServerboundMovePlayerStatusOnly:
		// UnsignedByte flags only. No position change → no re-center.
		var flags pk.UnsignedByte
		if err := in.Packet.Scan(&flags); err != nil {
			return
		}
		p.onGround = flags&movementFlagOnGround != 0

	case packetid.ServerboundPlayerAction:
		// BREAK (ENT-03): resolved on-tick, sequence-ordered. The handler decodes
		// defensively, validates reach + loaded column, mutates the tick-owned chunk to
		// air, then acks + broadcasts (the reconciliation contract). It sits AFTER the
		// teleport gate — a legitimately-editing player is confirmed, and a malformed or
		// out-of-reach action is a silent no-op inside the handler (T-6-01 / T-6-04).
		t.handlePlayerAction(p, in.Packet)

	case packetid.ServerboundUseItemOn:
		// PLACE (ENT-03): resolved on-tick, sequence-ordered. Same defensive/validated
		// path as the break handler; sets the v1 place-state at the adjacent face and
		// acks + broadcasts.
		t.handleUseItemOn(p, in.Packet)

	case packetid.ServerboundContainerClick:
		// INVENTORY (ENT-04): resolved on-tick. The 1.21.5+ HashedStack click is decoded
		// WITHOUT mis-framing (jar-derived framing); the server is AUTHORITATIVE — it
		// DISCARDS the client's hashes and re-sends authoritative ContainerSetContent. A
		// malformed/truncated click is a silent no-op inside the handler (T-6-04), never a
		// panic. Sits after the teleport gate — an editing player is confirmed.
		t.handleContainerClick(p, in.Packet)

	case packetid.ServerboundSetCreativeModeSlot:
		// INVENTORY (ENT-04): a creative player sets a slot directly (full component-slot
		// ItemStack, server-bound). The simple path to a visible item; decoded defensively
		// and stored into the tick-owned inventory.
		t.handleSetCreativeModeSlot(p, in.Packet)

	case packetid.ServerboundSetCarriedItem:
		// INVENTORY (ENT-04): the player's selected hotbar slot (held item). Decoded
		// defensively; updates the tick-owned held slot.
		t.handleSetCarriedItem(p, in.Packet)

	case packetid.ServerboundContainerClose:
		// INVENTORY (ENT-04): the client closed a container window. v1 cleanup/no-op.
		t.handleContainerClose(p, in.Packet)

	case packetid.ServerboundAttack:
		// COMBAT (GAMEPLAY-04): the entity ATTACK. In 26.2 the attack is its OWN packet —
		// ServerboundAttackPacket = a single VarInt entityId (jar-verified) — split out of the
		// old ServerboundInteract{Action} (where ATTACK used to live). The handler resolves the
		// NAMED target to a tickPlayer, reach-gates it, and applies SERVER-supplied damage into
		// the existing applyDamage->die flow; the client never claims a damage amount (T-6-05).
		// A forged/out-of-reach/self/malformed input is a silent no-op inside the handler.
		t.handleAttack(p, in.Packet)

	case packetid.ServerboundInteract:
		// COMBAT (GAMEPLAY-04): the RIGHT-CLICK entity interaction in 26.2 (entityId + hand +
		// Vec3 location + Boolean — NO Action enum; ATTACK is the separate ServerboundAttack
		// above). v1 has no entity right-click behavior, so this is a defensive no-op that
		// NEVER deals damage (only the attack path does).
		t.handleInteract(p, in.Packet)

	default:
		// Non-movement subtick input with no resolver yet: the hook already observed it;
		// nothing to apply here.
	}
}

// maybeRecenter re-centers the player's view ring when a position update crosses a
// chunk-column boundary (PLAY-04). The crossing test (newC != p.center) is the thrash
// control (T-5-05): within-column jitter is a cheap no-op, so a flood of movement
// packets re-centers at most once per real boundary crossing. math.Floor makes the
// block→chunk mapping negative-correct.
func (t *TickLoop) maybeRecenter(p *tickPlayer) {
	newC := chunkCenterOf(int32(math.Floor(p.x)), int32(math.Floor(p.z)))
	if newC != p.center {
		t.recenterRing(p, newC)
	}
}
