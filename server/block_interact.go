package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// block_interact.go — ENT-03: the on-tick place/break handlers behind the subtick route.
// dispatch (tick.go) appends ServerboundPlayerAction (NEW route) and ServerboundUseItemOn
// (already routed) into the bounded per-player subtick buffer; applyInput (subtick.go)
// drains them in sequence order and calls these handlers on the TICK goroutine. All world
// mutation goes through world.ChunkManager.SetBlock over the tick-owned manager (TICK-05 /
// T-6-08) and all client sends go through the bounded outbound queue (writeLoop stays the
// sole socket writer).
//
// THE RECONCILIATION CONTRACT (load-bearing, anti-ghost-block — 06-RESEARCH Pitfall 5):
// a VALID edit (1) mutates the tick-owned chunk via SetBlock, (2) sends
// ClientboundBlockChangedAck(sequence) to the EDITOR so its prediction reconciles, and
// (3) broadcasts ClientboundBlockUpdate(pos, newState) to EVERY player tracking that column
// (editor included, so a rejected/adjusted prediction snaps back). An INVALID edit
// (out of reach, unloaded column, or a defensive Scan failure) is a SILENT no-op: no
// mutation, no ack, no broadcast.

// PlayerAction.Action enum ordinals (jar: ServerboundPlayerActionPacket$Action, read as a
// VarInt enum index). Only the destroy stages matter for v1.
const (
	actionStartDestroyBlock = 0 // survival dig BEGIN (button pressed) — NOT a break; no-op for v1
	actionAbortDestroyBlock = 1 // dig cancelled — no-op for v1
	actionStopDestroyBlock  = 2 // survival dig FINISH — the break trigger
)

// blockReach is the v1 max edit distance (blocks) from the player's position to the target
// block CENTER. Vanilla's survival reach is ~4.5 blocks (creative ~6); v1 uses a single
// generous bound as the server-authoritative reach gate (T-6-01). A target beyond this is
// rejected as a silent no-op. Measured from the player feet position to the block center —
// good enough for the v1 "can't edit blocks across the map" guarantee.
const blockReach = 6.0

// directionNormal maps a jar Direction 3D-data value (from3DDataValue: 0=DOWN, 1=UP,
// 2=NORTH, 3=SOUTH, 4=WEST, 5=EAST) to its unit normal (dx,dy,dz). Placement offsets the
// hit block by this normal to land on the ADJACENT face. An unknown index yields the zero
// vector, which collapses placement onto the hit block — harmless and still validated.
func directionNormal(dir int) (dx, dy, dz int) {
	switch dir {
	case 0: // DOWN -Y
		return 0, -1, 0
	case 1: // UP +Y
		return 0, 1, 0
	case 2: // NORTH -Z
		return 0, 0, -1
	case 3: // SOUTH +Z
		return 0, 0, 1
	case 4: // WEST -X
		return -1, 0, 0
	case 5: // EAST +X
		return 1, 0, 0
	default:
		return 0, 0, 0
	}
}

// withinReach reports whether the block at pos is close enough to the player to edit. The
// distance is player-feet to block-center (pos + 0.5 on each axis). The server-authoritative
// reach gate (T-6-01): a far/cross-chunk target is rejected before any mutation.
func (t *TickLoop) withinReach(p *tickPlayer, pos pk.Position) bool {
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y) + 0.5
	cz := float64(pos.Z) + 0.5
	dx := cx - p.x
	dy := cy - p.y
	dz := cz - p.z
	return dx*dx+dy*dy+dz*dz <= blockReach*blockReach
}

// handlePlayerAction resolves a ServerboundPlayerAction (the BREAK path) on-tick. Wire
// layout (jar-derived): VarInt action + Position pos + UnsignedByte direction + VarInt
// sequence. A Scan error is a silent no-op (T-3-02 / T-6-04: defensive decode, never panic).
// Only the destroy-FINISH (STOP) and creative-instant (START) stages break; ABORT and the
// non-destroy actions are no-ops. A valid in-reach break on a loaded column sets the target
// to air and runs the reconciliation contract.
func (t *TickLoop) handlePlayerAction(p *tickPlayer, pkt pk.Packet) {
	var action pk.VarInt
	var pos pk.Position
	var direction pk.UnsignedByte
	var sequence pk.VarInt
	if err := pkt.Scan(&action, &pos, &direction, &sequence); err != nil {
		return // malformed/short payload: no-op, never panic (defensive decode)
	}

	// v1 break stage: STOP_DESTROY_BLOCK (action 2) is the survival dig FINISH — the moment the
	// block actually breaks. START_DESTROY_BLOCK (action 0) is the dig BEGIN: in survival the
	// client sends it the instant the player presses the attack button, BEFORE the block is
	// mined. Breaking on START makes every block shatter instantly on first click — the
	// "creative instant-break" the operator saw in a survival session. So v1 breaks ONLY on
	// STOP; START and ABORT (and every other action) are no-ops here. (A future survival dig
	// model would time the START→STOP interval against the block's hardness; v1 trusts the
	// client's FINISH, which is acceptable for an offline single-player-style world.)
	if int(action) != actionStopDestroyBlock {
		return
	}

	// Server-authoritative reach gate (T-6-01): reject an out-of-range target silently.
	if !t.withinReach(p, pos) {
		return
	}

	// BREAK: set the target to air on the tick-owned chunk. SetBlock returns changed=false
	// for an unloaded column / out-of-range y or an already-air target — in which case we
	// neither ack nor broadcast (no ghost, nothing to reconcile).
	//
	air := block.ToStateID[block.Air{}]

	// GAMEPLAY-06: capture the BROKEN block's state BEFORE SetBlock overwrites it with air, so
	// spawnBlockDrop can look up the right drop (reading after SetBlock would always see air).
	// A failed read leaves brokenState at air's id (no drop), which is the safe default.
	brokenState := air
	if t.world != nil {
		if s, ok := t.world.GetBlock(pos, dimMinY); ok {
			brokenState = s
		}
	}

	if t.world == nil || !t.world.SetBlock(pos, air, dimMinY) {
		return
	}

	t.reconcileEdit(p, pos, air, int32(sequence))

	// GAMEPLAY-06: spawn the dropped Item entity for the broken block. Rides the GAMEPLAY-01
	// tracker broadcast (the store-add path). A block with no v1 drop is a no-op inside.
	t.spawnBlockDrop(pos, brokenState)
}

// handleUseItemOn resolves a ServerboundUseItemOn (the PLACE path) on-tick. Wire layout
// (jar-derived, javap'd this session): VarInt hand + [Position pos + VarInt direction +
// Float cursorX/Y/Z + Boolean insideBlock + Boolean worldBorderHit] + VarInt sequence.
// (FriendlyByteBuf.readBlockHitResult reads BlockPos, the Direction enum, three floats, then
// TWO booleans; ServerboundUseItemOnPacket reads hand FIRST, then the hit result, then the
// sequence.) A Scan error is a silent no-op. The placed block is a v1 STAND-IN (stone) since
// real held-item -> block resolution lands with ENT-04 (06-05); the load-bearing behavior is
// the WIRE handshake (ack + update at the adjacent face), not the exact block.
func (t *TickLoop) handleUseItemOn(p *tickPlayer, pkt pk.Packet) {
	var hand pk.VarInt
	var pos pk.Position
	var direction pk.VarInt
	var cursorX, cursorY, cursorZ pk.Float
	var insideBlock, worldBorderHit pk.Boolean
	var sequence pk.VarInt
	if err := pkt.Scan(&hand, &pos, &direction,
		&cursorX, &cursorY, &cursorZ,
		&insideBlock, &worldBorderHit, &sequence); err != nil {
		return // malformed/short payload: no-op, never panic
	}

	// Placement lands on the block ADJACENT to the hit face (hit pos + the face normal).
	dx, dy, dz := directionNormal(int(direction))
	placePos := pk.Position{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}

	// Reach is validated against the placement target.
	if !t.withinReach(p, placePos) {
		return
	}

	// PLACE the v1 stand-in (stone). changed=false (unloaded/occupied/no-change) -> no ack,
	// no broadcast.
	placeState := block.ToStateID[block.Stone{}]
	if t.world == nil || !t.world.SetBlock(placePos, placeState, dimMinY) {
		return
	}

	t.reconcileEdit(p, placePos, placeState, int32(sequence))
}

// reconcileEdit runs the post-mutation reconciliation contract for a VALID edit: ack the
// action's sequence to the editor (so its prediction commits) and broadcast the new state
// to every player tracking the edited column (editor included). Called only after SetBlock
// reported changed=true. Runs on the tick goroutine; all sends go through the bounded queue.
func (t *TickLoop) reconcileEdit(editor *tickPlayer, pos pk.Position, state block.StateID, sequence int32) {
	if editor.client != nil {
		editor.client.Send(blockChangedAck(sequence))
	}
	t.broadcastBlockUpdate(pos, state)
}

// broadcastBlockUpdate sends a ClientboundBlockUpdate(pos, state) to every player whose view
// covers the edited column — for v1, every player whose center/sentChunks includes
// chunkCenterOf(pos). The editor is included so its own predictive edit reconciles against
// the authoritative state (a rejected/adjusted prediction snaps back). Runs on the tick
// owner over tick-owned player state; p.client.Send goes through the bounded outbound queue.
func (t *TickLoop) broadcastBlockUpdate(pos pk.Position, state block.StateID) {
	col := chunkCenterOf(int32(pos.X), int32(pos.Z))
	packet := blockUpdate(pos, state)
	for _, pl := range t.players {
		if pl.client == nil {
			continue
		}
		// A player tracks the column if it is its current center or in its sent set.
		if pl.center == col || (pl.sentChunks != nil && pl.sentChunks[col]) {
			pl.client.Send(packet)
		}
	}
}
