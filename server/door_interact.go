package server

import (
	mrand "math/rand/v2"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// door_interact.go — the D-I1 port of DoorBlock / TrapDoorBlock / FenceGateBlock.useWithoutItem
// (right-click open/close). Hooked from chest_open.go useBlockInteraction (the
// ServerPlayerGameMode.useItemOn step-1 block interaction) BEFORE placement: a right-click on a
// door/trapdoor/fence-gate toggles its OPEN and consumes the interaction so no block is placed.
//
// All three are 1:1 with the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap-verified):
//
//	DoorBlock.useWithoutItem(state, level, pos, player, hit):
//	    if (!this.type.canOpenByHand()) return PASS;               // iron door: no hand open
//	    state = state.cycle(OPEN);                                 // flip OPEN
//	    level.setBlock(pos, state, 10);                            // flags 10 = UPDATE_CLIENTS|UPDATE_KNOWN_SHAPE
//	    this.playSound(player, level, pos, state.getValue(OPEN));  // wooden/copper door open/close
//	    level.gameEvent(player, OPEN?BLOCK_OPEN:BLOCK_CLOSE, pos); // (no gameEvent subsystem: deferred)
//	    return SUCCESS;
//	  The setBlock(flags 10) fires the neighbour half's updateShape, which copies the new OPEN onto the
//	  other half (updateShape returns neighborState.setValue(HALF, myHalf) — carrying the changed OPEN).
//	  Sulfur has no updateShape dispatcher for doors, so we do the direct neighbour-half OPEN write the
//	  door needs (find the other half by HALF: LOWER<->UP, UPPER<->DOWN) and broadcast it. CITE:
//	  DoorBlock.updateShape (the HALF-carries-OPEN sync).
//
//	TrapDoorBlock.useWithoutItem(state, level, pos, player, hit):
//	    if (!this.type.canOpenByHand()) return PASS;               // iron trapdoor: no hand open
//	    this.toggle(state, level, pos, player);                    // cycle(OPEN); setBlock(flags 2);
//	    return SUCCESS;                                            //   waterlog tick; playSound; gameEvent
//
//	FenceGateBlock.useWithoutItem(state, level, pos, player, hit):
//	    if (state.getValue(OPEN)) { state = state.setValue(OPEN, false); }        // close in place
//	    else {                                                                    // open: face the player
//	        Direction d = player.getDirection();
//	        if (state.getValue(FACING) == d.getOpposite()) state = state.setValue(FACING, d);
//	        state = state.setValue(OPEN, true);
//	    }
//	    level.setBlock(pos, state, 10);
//	    boolean open = state.getValue(OPEN);
//	    level.playSound(player, pos, open?fenceGateOpen:fenceGateClose, BLOCKS, 1, random.nextFloat()*0.1+0.9);
//	    level.gameEvent(player, open?BLOCK_OPEN:BLOCK_CLOSE, pos);
//	    return SUCCESS;
//
// The playSound in every case is Level.playSound(player, pos, sound, SoundSource.BLOCKS, 1.0f,
// level.getRandom().nextFloat()*0.1f + 0.9f). The pitch/seed here are DEDICATED non-gameplay draws
// (mrand — the sound-seed discipline the flint&steel/mob paths already use) so they never perturb any
// gameplay RNG stream (the pig oracle stays byte-identical). SoundSource.BLOCKS == ordinal 4. The
// gameEvent(BLOCK_OPEN/BLOCK_CLOSE) is a cite-deferred no-op (no game-event/sculk subsystem yet),
// exactly as every other gameEvent call site in the server.

// tryDoorInteraction dispatches a right-click on a door/trapdoor/fence-gate. Returns true when the
// clicked block is one of those and the interaction was consumed (matching InteractionResult.SUCCESS/
// PASS: an iron door/trapdoor rejected by hand still CONSUMES nothing placed — vanilla returns PASS,
// so placement would run, but a door is never replaceable, so nothing is placed either way; we return
// true to keep the "block handled" contract and skip the placement attempt). Returns false when the
// clicked block is not a door/trapdoor/fence-gate (placement continues).
func (t *TickLoop) tryDoorInteraction(pos pk.Position, state block.StateID, p *tickPlayer) bool {
	switch {
	case block.IsDoor(state):
		return t.useDoor(pos, state)
	case block.IsTrapdoor(state):
		return t.useTrapdoor(pos, state)
	case block.IsFenceGate(state):
		return t.useFenceGate(pos, state, p)
	default:
		return false
	}
}

// doorSoundPitch is the shared block-toggle pitch: random.nextFloat()*0.1f + 0.9f, drawn from a
// dedicated non-gameplay RandomSource (mrand) so it never perturbs a gameplay RNG stream.
func doorSoundPitch() float32 {
	return mrand.Float32()*0.1 + 0.9
}

// useDoor ports DoorBlock.useWithoutItem. Iron doors reject a hand click (canOpenByHand=false ->
// PASS): return false so the normal useItemOn continuation runs (nothing is placed on a non-
// replaceable door either way). Otherwise cycle OPEN on the clicked half, write it, sync the OTHER
// half's OPEN (the setBlock-flags-10 -> neighbour updateShape effect), play the sound, and consume.
func (t *TickLoop) useDoor(pos pk.Position, state block.StateID) bool {
	if !block.DoorOpenableByHand(state) {
		return false // BlockSetType.canOpenByHand()==false (iron): InteractionResult.PASS
	}
	if t.world() == nil {
		return true
	}
	newState, ok := block.DoorCycleOpen(state)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return true // consumed the click even if the write no-oped (InteractionResult.SUCCESS)
	}
	t.broadcastBlockUpdate(pos, newState)

	// Double-door half sync: the setBlock(flags 10) fires the neighbour half's updateShape, which
	// carries the new OPEN onto the other half. Find the other half by HALF (LOWER's neighbour is UP;
	// UPPER's is DOWN), and if it is a DoorBlock, set its OPEN to match. CITE: DoorBlock.updateShape.
	open := block.DoorOpen(newState)
	if half, ok := block.DoorHalf(newState); ok {
		var other pk.Position
		if half == block.DoubleBlockHalfLower {
			other = relative(pos, block.Up)
		} else {
			other = relative(pos, block.Down)
		}
		if os, ok := t.world().GetBlock(other, dimMinY); ok && block.IsDoor(os) {
			if ns, ok := block.DoorWithOpen(os, open); ok && ns != os {
				if t.world().SetBlock(other, ns, dimMinY) {
					t.broadcastBlockUpdate(other, ns)
				}
			}
		}
	}

	// this.playSound(player, level, pos, state.getValue(OPEN)): wooden/copper door open/close on BLOCKS.
	sound := block.DoorSoundID(newState, open)
	t.playSound(sound, soundSourceBlocks,
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
		1.0, doorSoundPitch(), mrand.Int64())
	// level.gameEvent(player, open?BLOCK_OPEN:BLOCK_CLOSE, pos): cite-deferred (no game-event subsystem).
	return true
}

// useTrapdoor ports TrapDoorBlock.useWithoutItem -> toggle. Iron trapdoors reject a hand click
// (canOpenByHand=false -> PASS): return false. Otherwise cycle OPEN, write it (flags 2 = UPDATE_CLIENTS),
// play the trapdoor sound, and consume. The waterlog scheduleTick on a waterlogged trapdoor toggle is a
// cite-deferred follow-up (the fluid schedule half); the OPEN toggle + sound are the observable use().
func (t *TickLoop) useTrapdoor(pos pk.Position, state block.StateID) bool {
	if !block.DoorOpenableByHand(state) {
		return false // iron trapdoor: InteractionResult.PASS
	}
	if t.world() == nil {
		return true
	}
	newState, ok := block.DoorCycleOpen(state)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return true
	}
	t.broadcastBlockUpdate(pos, newState)
	open := block.DoorOpen(newState)
	sound := block.TrapdoorSoundID(newState, open)
	t.playSound(sound, soundSourceBlocks,
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
		1.0, doorSoundPitch(), mrand.Int64())
	// gameEvent(BLOCK_OPEN/CLOSE) + waterlog scheduleTick: cite-deferred.
	return true
}

// playerFacing is Entity.getDirection() == Direction.fromYRot(yRot): the horizontal direction the
// player faces. idx = Mth.floor(yRot/90 + 0.5) & 3 maps 0->SOUTH, 1->WEST, 2->NORTH, 3->EAST.
// CITE: net.minecraft.core.Direction.fromYRot / Entity.getDirection.
func playerFacing(yaw float32) block.Direction {
	idx := int(mfloor(float64(yaw)/90.0+0.5)) & 3
	switch idx {
	case 0:
		return block.South
	case 1:
		return block.West
	case 2:
		return block.North
	default: // 3
		return block.East
	}
}

// mfloor is Mth.floor(double) == (int)Math.floor(d).
func mfloor(d float64) int {
	i := int(d)
	if d < float64(i) {
		return i - 1
	}
	return i
}

// useFenceGate ports FenceGateBlock.useWithoutItem. Fence gates are always wood (canOpenByHand=true).
// If OPEN, close in place. If closed, face the player on open: when the gate's FACING is the OPPOSITE
// of the player's facing, flip FACING to the player's facing so it swings toward them; then set
// OPEN=true. Write (flags 10), play the fence-gate sound, consume. CITE: FenceGateBlock.useWithoutItem.
func (t *TickLoop) useFenceGate(pos pk.Position, state block.StateID, p *tickPlayer) bool {
	if t.world() == nil {
		return true
	}
	newState := state
	if block.DoorOpen(state) {
		// state.setValue(OPEN, false)
		if ns, ok := block.DoorWithOpen(state, false); ok {
			newState = ns
		}
	} else {
		// direction = player.getDirection(); if (FACING == direction.getOpposite()) FACING = direction.
		dir := playerFacing(p.yaw)
		if facing, ok := block.FenceGateFacing(state); ok && facing == dirOpposite(dir) {
			if ns, ok := block.FenceGateWithFacing(state, dir); ok {
				newState = ns
			}
		}
		// state.setValue(OPEN, true)
		if ns, ok := block.DoorWithOpen(newState, true); ok {
			newState = ns
		}
	}
	if !t.world().SetBlock(pos, newState, dimMinY) {
		return true
	}
	t.broadcastBlockUpdate(pos, newState)
	open := block.DoorOpen(newState)
	sound := block.FenceGateSoundID(newState, open)
	t.playSound(sound, soundSourceBlocks,
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
		1.0, doorSoundPitch(), mrand.Int64())
	// gameEvent(BLOCK_OPEN/CLOSE): cite-deferred.
	return true
}
