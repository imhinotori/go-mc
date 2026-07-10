package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// vine.go -- the VINE random-tick spread handler, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.level.block.VineBlock.randomTick). Wired into the random-tick driver's
// dispatchRandomTick (random_tick.go). Growth is driven by the world's random-tick pass -- this
// handler is CALLED, never called back.
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p this session):
//   VineBlock.randomTick(state, level, pos, random):
//       if (!getGameRules().get(GameRules.SPREAD_VINES)) return;   // default TRUE (registerBoolean true)
//       if (random.nextInt(4) != 0) return;                        // 3/4 early-out (ALWAYS first draw)
//       Direction dir = Direction.getRandom(random);               // nextInt(6) into VALUES (2nd draw)
//       BlockPos above = pos.above();
//       if (dir.getAxis().isHorizontal() && !state.getValue(getPropertyForFace(dir)))
//                                                       -> horizontal-spread cascade (>=1 nextFloat<0.05)
//       else if (dir == UP && pos.getY() < getMaxY())   -> UP branch (4 nextBoolean clearing draws)
//       else if (pos.getY() > getMinY())                -> DOWN branch (copyRandomFaces, 4 nextBoolean)
//
// RNG DRAW ORDER (must match the jar exactly): nextInt(4) early-out, THEN Direction.getRandom ==
// nextInt(6); THEN per branch: horizontal = at most one nextFloat()<0.05; UP = one nextBoolean per
// HORIZONTAL face (4); DOWN (copyRandomFaces) = one nextBoolean per HORIZONTAL face (4). CITE:
// VineBlock.randomTick; Direction.getRandom (Util.getRandom == nextInt(VALUES.length==6));
// VineBlock.copyRandomFaces.
//
// GAMERULE: GameRules.SPREAD_VINES is not yet wired (no gamerule engine in v1); vineSpreadVines is
// the vanilla DEFAULT (registerBoolean("spread_vines", ..., true) -> TRUE), structured to become a
// real getGameRules().get(SPREAD_VINES) read later -- the same cited-default discipline as
// randomTickSpeed. CITE: GameRules.SPREAD_VINES (default true).
//
// isAcceptableNeighbour(level, pos, dir) == MultifaceBlock.canAttachTo(level, dir, pos,
// getBlockState(pos)): the block AT pos presents a FULL support face toward dir (isFaceFull of its
// support/collision shape toward dir.getOpposite()). Mirrored by block.IsFaceSturdy(state, dir,
// SupportFull). CITE: VineBlock.isAcceptableNeighbour; MultifaceBlock.canAttachTo.
//
// All setBlock calls use flag 2 == UPDATE_CLIENTS (no neighbor notify), mirrored as SetBlock +
// broadcastBlockUpdate. CITE: VineBlock.randomTick.

// vineSpreadVines is GameRules.SPREAD_VINES's vanilla default (true). CITE: GameRules.SPREAD_VINES.

const vineSpreadVines = true

var vineHorizontals = [4]block.Direction{block.North, block.East, block.South, block.West}

func (t *TickLoop) vineRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	if !block.IsVine(state) {
		return
	}
	if !vineSpreadVines {
		return
	}
	if r.levelRandom.NextIntN(4) != 0 {
		return
	}
	dir := block.Direction(r.levelRandom.NextIntN(6))
	abovePos := above(pos)
	if dirIsHorizontal(dir) && !block.VineFace(state, dir) {
		t.vineSpreadHorizontal(r, state, pos, dir)
		return
	}
	if dir == block.Up && pos.Y < dimMaxY {
		t.vineSpreadUp(r, state, pos, abovePos)
		return
	}
	if pos.Y > dimMinY {
		t.vineSpreadDown(r, state, pos)
	}
}

func (t *TickLoop) vineIsAcceptableNeighbour(pos pk.Position, dir block.Direction) bool {
	st, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	return block.IsFaceSturdy(st, dir, block.SupportFull)
}

func (t *TickLoop) vineSpreadHorizontal(r *region, state block.StateID, pos pk.Position, dir block.Direction) {
	if !t.vineCanSpread(pos) {
		return
	}
	target := relative(pos, dir)
	targetState, okT := t.world().GetBlock(target, dimMinY)
	if !okT {
		return
	}
	if block.IsAir(targetState) {
		cw := dirClockWise(dir)
		ccw := dirCounterClockWise(dir)
		hasCW := block.VineFace(state, cw)
		hasCCW := block.VineFace(state, ccw)
		targetCW := relative(target, cw)
		targetCCW := relative(target, ccw)
		if hasCW && t.vineIsAcceptableNeighbour(targetCW, cw) {
			t.vinePlaceFace(target, cw)
			return
		}
		if hasCCW && t.vineIsAcceptableNeighbour(targetCCW, ccw) {
			t.vinePlaceFace(target, ccw)
			return
		}
		opp := dirOpposite(dir)
		if hasCW && t.isEmptyBlockAt(targetCW) && t.vineIsAcceptableNeighbour(relative(pos, cw), opp) {
			t.vinePlaceFace(targetCW, opp)
			return
		}
		if hasCCW && t.isEmptyBlockAt(targetCCW) && t.vineIsAcceptableNeighbour(relative(pos, ccw), opp) {
			t.vinePlaceFace(targetCCW, opp)
			return
		}
		if r.levelRandom.NextFloat() < 0.05 && t.vineIsAcceptableNeighbour(above(target), block.Up) {
			t.vinePlaceFace(target, block.Up)
		}
		return
	}
	if t.vineIsAcceptableNeighbour(target, dir) {
		if next, ok := block.VineWithFace(state, dir, true); ok {
			t.vineSetBlock(pos, next)
		}
	}
}

func (t *TickLoop) vineSpreadUp(r *region, state block.StateID, pos, abovePos pk.Position) {
	if t.vineCanSupportAtFace(pos, block.Up) {
		if next, ok := block.VineWithFace(state, block.Up, true); ok {
			t.vineSetBlock(pos, next)
		}
		return
	}
	if !t.isEmptyBlockAt(abovePos) {
		return
	}
	if !t.vineCanSpread(pos) {
		return
	}
	next := state
	for _, d := range vineHorizontals {
		clear := !r.levelRandom.NextBoolean()
		if clear && t.vineIsAcceptableNeighbour(relative(abovePos, d), d) {
			if n, ok := block.VineWithFace(next, d, false); ok {
				next = n
			}
		}
	}
	if block.VineHasHorizontalConnection(next) {
		t.vineSetBlock(abovePos, next)
	}
}

func (t *TickLoop) vineSpreadDown(r *region, state block.StateID, pos pk.Position) {
	belowPos := below(pos)
	belowState, ok := t.world().GetBlock(belowPos, dimMinY)
	if !ok {
		return
	}
	if !block.IsAir(belowState) && !block.IsVine(belowState) {
		return
	}
	src := belowState
	if block.IsAir(belowState) {
		src = block.VineDefaultState()
	}
	out := t.vineCopyRandomFaces(r, state, src)
	if out != src && block.VineHasHorizontalConnection(out) {
		t.vineSetBlock(belowPos, out)
	}
}

// vineCopyRandomFaces is VineBlock.copyRandomFaces(from, to, random): for each HORIZONTAL direction,
// draw one nextBoolean; on true AND when from has that face set, copy it onto to. CITE:
// VineBlock.copyRandomFaces.
func (t *TickLoop) vineCopyRandomFaces(r *region, from, to block.StateID) block.StateID {
	out := to
	for _, d := range vineHorizontals {
		if r.levelRandom.NextBoolean() {
			if block.VineFace(from, d) {
				if n, ok := block.VineWithFace(out, d, true); ok {
					out = n
				}
			}
		}
	}
	return out
}

// vineCanSpread is VineBlock.canSpread(level, pos): scan the 9x3x9 box betweenClosed(pos+(-4,-1,-4),
// pos+(4,1,4)) counting vine blocks; the counter starts at 5 and decrements per vine, returning false
// the instant it reaches <= 0 (i.e. more than 4 surrounding vines block further spread). CITE:
// VineBlock.canSpread.
func (t *TickLoop) vineCanSpread(pos pk.Position) bool {
	remaining := 5
	for dx := -4; dx <= 4; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -4; dz <= 4; dz++ {
				cell := pk.Position{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
				cs, ok := t.world().GetBlock(cell, dimMinY)
				if !ok || !block.IsVine(cs) {
					continue
				}
				remaining--
				if remaining <= 0 {
					return false
				}
			}
		}
	}
	return true
}

// vineCanSupportAtFace is VineBlock.canSupportAtFace(level, pos, dir): DOWN never supports; if the cell
// in dir presents a full face toward dir, true; else (horizontal dir only) true iff the cell ABOVE is
// the same vine with that face set. CITE: VineBlock.canSupportAtFace.
func (t *TickLoop) vineCanSupportAtFace(pos pk.Position, dir block.Direction) bool {
	if dir == block.Down {
		return false
	}
	if t.vineIsAcceptableNeighbour(relative(pos, dir), dir) {
		return true
	}
	if dir == block.Up {
		return false
	}
	as, ok := t.world().GetBlock(above(pos), dimMinY)
	if !ok || !block.IsVine(as) {
		return false
	}
	return block.VineFace(as, dir)
}

func (t *TickLoop) vinePlaceFace(pos pk.Position, dir block.Direction) {
	if next, ok := block.VineWithFace(block.VineDefaultState(), dir, true); ok {
		t.vineSetBlock(pos, next)
	}
}

func (t *TickLoop) vineSetBlock(pos pk.Position, state block.StateID) {
	if t.world().SetBlock(pos, state, dimMinY) {
		t.broadcastBlockUpdate(pos, state)
	}
}

func dirIsHorizontal(d block.Direction) bool {
	switch d {
	case block.North, block.South, block.West, block.East:
		return true
	default:
		return false
	}
}
