package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// piston_resolver.go — REDSTONE TIER-3: the 1:1 port of
// net.minecraft.world.level.block.piston.PistonStructureResolver from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar), decompiled via CFR this session. It computes the push list (the blocks a
// piston moves) and the destroy list (blocks it breaks), enforcing the 12-block MAX_PUSH_DEPTH limit,
// the immovable rules (via PistonBaseBlock.isPushable), and the slime/honey adjacent-drag branching for
// sticky blocks. resolve() returns false when the push is blocked or exceeds the limit.
//
// CITE (methods, jar-verified this session):
//   PistonStructureResolver.resolve / addBlockLine / addBranchingBlocks / reorderListAtCollision
//   PistonStructureResolver.isSticky (slime_block || honey_block)
//   PistonStructureResolver.canStickToEachOther (honey<->slime never stick; else either sticky)
//   PistonStructureResolver ctor (extending: pushDirection=direction, startPos=pistonPos.relative(dir);
//       retract: pushDirection=direction.opposite, startPos=pistonPos.relative(dir,2))
//   MAX_PUSH_DEPTH = 12

// pistonStructureResolver is the Go port of PistonStructureResolver. It is constructed with the level
// (via the TickLoop), the piston position, the piston direction, and the extending flag; init() derives
// pushDirection + startPos. CITE: PistonStructureResolver fields/ctor.
type pistonStructureResolver struct {
	t               *TickLoop
	pistonPos       pk.Position
	pistonDirection block.Direction
	extending       bool
	startPos        pk.Position
	pushDirection   block.Direction
	toPush          []pk.Position
	toDestroy       []pk.Position
}

// init is the PistonStructureResolver constructor tail: derive pushDirection + startPos. CITE:
// PistonStructureResolver(Level, BlockPos, Direction, boolean).
func (r *pistonStructureResolver) init(direction block.Direction) {
	r.pistonDirection = direction
	if r.extending {
		r.pushDirection = direction
		r.startPos = relative(r.pistonPos, direction)
	} else {
		r.pushDirection = dirOpposite(direction)
		r.startPos = relativeN(r.pistonPos, direction, 2)
	}
}

// stateAt reads the block state at p.
func (r *pistonStructureResolver) stateAt(p pk.Position) block.StateID {
	return r.t.redstoneBlockAt(p)
}

// pistonIsSticky is PistonStructureResolver.isSticky(state): slime_block || honey_block. CITE:
// PistonStructureResolver.isSticky.
func pistonIsSticky(state block.StateID) bool {
	id := block.StateList[state].ID()
	return id == "minecraft:slime_block" || id == "minecraft:honey_block"
}

// pistonCanStickToEachOther is PistonStructureResolver.canStickToEachOther(state1, state2): honey and
// slime NEVER stick to each other; otherwise they stick if either is sticky. CITE:
// PistonStructureResolver.canStickToEachOther.
func pistonCanStickToEachOther(state1, state2 block.StateID) bool {
	id1 := block.StateList[state1].ID()
	id2 := block.StateList[state2].ID()
	if id1 == "minecraft:honey_block" && id2 == "minecraft:slime_block" {
		return false
	}
	if id1 == "minecraft:slime_block" && id2 == "minecraft:honey_block" {
		return false
	}
	return pistonIsSticky(state1) || pistonIsSticky(state2)
}

// resolve is PistonStructureResolver.resolve():
//
//	toPush.clear(); toDestroy.clear();
//	BlockState nextState = getBlockState(startPos);
//	if (!isPushable(nextState, startPos, pushDirection, false, pistonDirection)) {
//	    if (extending && pushReaction == DESTROY) { toDestroy.add(startPos); return true; }
//	    return false;
//	}
//	if (!addBlockLine(startPos, pushDirection)) return false;
//	for (BlockPos pos : toPush) if (isSticky(getBlockState(pos)) && !addBranchingBlocks(pos)) return false;
//	return true;
//
// CITE: PistonStructureResolver.resolve.
func (r *pistonStructureResolver) resolve() bool {
	r.toPush = r.toPush[:0]
	r.toDestroy = r.toDestroy[:0]
	nextState := r.stateAt(r.startPos)
	if !pistonIsPushable(nextState, r.startPos, r.pushDirection, false, r.pistonDirection, r.t) {
		if r.extending && block.PistonPushReaction(nextState) == block.PushReactionDestroy {
			r.toDestroy = append(r.toDestroy, r.startPos)
			return true
		}
		return false
	}
	if !r.addBlockLine(r.startPos, r.pushDirection) {
		return false
	}
	for i := 0; i < len(r.toPush); i++ {
		p := r.toPush[i]
		if pistonIsSticky(r.stateAt(p)) && !r.addBranchingBlocks(p) {
			return false
		}
	}
	return true
}

// addBlockLine is PistonStructureResolver.addBlockLine(start, direction) — the core line-walker that
// gathers a straight line of blocks (extending backward through a sticky run, then forward through the
// push line), enforcing the 12-block limit and handling collisions with an already-collected line via
// reorderListAtCollision. CITE: PistonStructureResolver.addBlockLine.
func (r *pistonStructureResolver) addBlockLine(start pk.Position, direction block.Direction) bool {
	nextState := r.stateAt(start)
	if block.IsAir(nextState) {
		return true
	}
	if !pistonIsPushable(nextState, start, r.pushDirection, false, direction, r.t) {
		return true
	}
	if start == r.pistonPos {
		return true
	}
	if r.containsPush(start) {
		return true
	}
	blockCount := 1
	if blockCount+len(r.toPush) > pistonMaxPushDepth {
		return false
	}
	// Walk backward through a contiguous sticky run (slime/honey chain) opposite the push direction.
	for pistonIsSticky(nextState) {
		pos := relativeN(start, dirOpposite(r.pushDirection), blockCount)
		previousState := nextState
		nextState = r.stateAt(pos)
		if block.IsAir(nextState) || !pistonCanStickToEachOther(previousState, nextState) ||
			!pistonIsPushable(nextState, pos, r.pushDirection, false, dirOpposite(r.pushDirection), r.t) ||
			pos == r.pistonPos {
			break
		}
		blockCount++
		if blockCount+len(r.toPush) > pistonMaxPushDepth {
			return false
		}
	}
	// Add the backward run (from the tail forward to `start`).
	blocksAdded := 0
	for i := blockCount - 1; i >= 0; i-- {
		r.toPush = append(r.toPush, relativeN(start, dirOpposite(r.pushDirection), i))
		blocksAdded++
	}
	// Walk forward from `start` through the push line.
	i := 1
	for {
		pos := relativeN(start, r.pushDirection, i)
		collisionPos := r.indexOfPush(pos)
		if collisionPos > -1 {
			r.reorderListAtCollision(blocksAdded, collisionPos)
			for j := 0; j <= collisionPos+blocksAdded; j++ {
				bp := r.toPush[j]
				if pistonIsSticky(r.stateAt(bp)) && !r.addBranchingBlocks(bp) {
					return false
				}
			}
			return true
		}
		nextState = r.stateAt(pos)
		if block.IsAir(nextState) {
			return true
		}
		if !pistonIsPushable(nextState, pos, r.pushDirection, true, r.pushDirection, r.t) || pos == r.pistonPos {
			return false
		}
		if block.PistonPushReaction(nextState) == block.PushReactionDestroy {
			r.toDestroy = append(r.toDestroy, pos)
			return true
		}
		if len(r.toPush) >= pistonMaxPushDepth {
			return false
		}
		r.toPush = append(r.toPush, pos)
		blocksAdded++
		i++
	}
}

// reorderListAtCollision is PistonStructureResolver.reorderListAtCollision(blocksAdded, collisionPos):
// re-splice toPush so the last-added line precedes the collided line. CITE:
// PistonStructureResolver.reorderListAtCollision.
func (r *pistonStructureResolver) reorderListAtCollision(blocksAdded, collisionPos int) {
	head := append([]pk.Position(nil), r.toPush[:collisionPos]...)
	lastLineAdded := append([]pk.Position(nil), r.toPush[len(r.toPush)-blocksAdded:]...)
	collisionToLine := append([]pk.Position(nil), r.toPush[collisionPos:len(r.toPush)-blocksAdded]...)
	r.toPush = r.toPush[:0]
	r.toPush = append(r.toPush, head...)
	r.toPush = append(r.toPush, lastLineAdded...)
	r.toPush = append(r.toPush, collisionToLine...)
}

// addBranchingBlocks is PistonStructureResolver.addBranchingBlocks(fromPos): for a sticky block, add the
// lines of every perpendicular-axis neighbor that can stick to it. CITE:
// PistonStructureResolver.addBranchingBlocks.
func (r *pistonStructureResolver) addBranchingBlocks(fromPos pk.Position) bool {
	fromState := r.stateAt(fromPos)
	for _, direction := range redstoneDirs {
		if directionAxis(direction) == directionAxis(r.pushDirection) {
			continue
		}
		neighbourPos := relative(fromPos, direction)
		neighbourState := r.stateAt(neighbourPos)
		if !pistonCanStickToEachOther(neighbourState, fromState) {
			continue
		}
		if !r.addBlockLine(neighbourPos, direction) {
			return false
		}
	}
	return true
}

// containsPush reports whether toPush already holds p (List.contains). CITE: toPush.contains(start).
func (r *pistonStructureResolver) containsPush(p pk.Position) bool {
	return r.indexOfPush(p) > -1
}

// indexOfPush is toPush.indexOf(p). CITE: toPush.indexOf(pos).
func (r *pistonStructureResolver) indexOfPush(p pk.Position) int {
	for i, q := range r.toPush {
		if q == p {
			return i
		}
	}
	return -1
}

// directionAxis is Direction.getAxis() reduced to a 0/1/2 (X/Y/Z) key for the perpendicular-axis test
// in addBranchingBlocks. CITE: Direction.getAxis.
func directionAxis(d block.Direction) int {
	switch d {
	case block.West, block.East:
		return 0 // X
	case block.Down, block.Up:
		return 1 // Y
	default:
		return 2 // Z (North/South)
	}
}
