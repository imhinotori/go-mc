package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// copper.go -- the COPPER OXIDATION random-tick handler, ported 1:1 from the unobfuscated 26.2 jar
// (ChangeOverTimeBlock.changeOverTime -> ChangeOverTimeBlock.getNextState -> WeatheringCopper.getNext).
// Wired into the random-tick driver's dispatchRandomTick (random_tick.go). Only UNWAXED, non-OXIDIZED
// copper is sampled (IsRandomlyTicking gates on CopperCanOxidize == getNext(block).isPresent()).
//
// RNG DRAW ORDER (must match the jar exactly):
//   1. changeOverTime: ONE nextFloat() < 0.05688889f (the per-tick attempt gate). If it fails, NO
//      further draw -- getNextState is not entered.
//   2. getNextState: after the Manhattan-distance-4 neighbour scan (which draws NOTHING), a SECOND
//      nextFloat() < chance, where chance = f*f*getChanceModifier(),
//      f = (moreOxidized+1)/(moreOxidized+lessOxidized+1), getChanceModifier == 0.75 for UNAFFECTED
//      else 1.0. On success getNext(state) resolves the next tier via NEXT_BY_BLOCK +
//      withPropertiesOf. A less-oxidized copper neighbour short-circuits getNextState to empty BEFORE
//      the second nextFloat (no second draw). CITE: ChangeOverTimeBlock.changeOverTime / getNextState;
//      WeatheringCopper.getChanceModifier / getNext.
//
// setBlockAndUpdate (flag 3) -- mirrored as SetBlock + broadcastBlockUpdate. CITE:
// ChangeOverTimeBlock.changeOverTime (Optional.ifPresent -> setBlockAndUpdate).

// copperChangeOverTimeChance is ChangeOverTimeBlock.changeOverTime's per-tick gate (0.05688889f).
// CITE: ChangeOverTimeBlock.changeOverTime (ldc float 0.05688889f).
const copperChangeOverTimeChance = 0.05688889

func (t *TickLoop) copperRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	if !block.IsWeatheringCopper(state) {
		return
	}
	// ChangeOverTimeBlock.changeOverTime: if (nextFloat() < 0.05688889f) getNextState(...).ifPresent(
	//   s -> setBlockAndUpdate(pos, s)). The 0.05688889f gate is the FIRST draw. CITE:
	// ChangeOverTimeBlock.changeOverTime.
	if r.levelRandom.NextFloat() >= copperChangeOverTimeChance {
		return
	}
	next, ok := t.copperGetNextState(r, state, pos)
	if !ok {
		return
	}
	// setBlockAndUpdate(pos, next) -- flag 3 (UPDATE_CLIENTS | UPDATE_NEIGHBORS); mirrored as SetBlock
	// + broadcastBlockUpdate. CITE: ChangeOverTimeBlock.changeOverTime (Optional.ifPresent ->
	// setBlockAndUpdate).
	if t.world().SetBlock(pos, next, dimMinY) {
		t.broadcastBlockUpdate(pos, next)
	}
}

func (t *TickLoop) copperGetNextState(r *region, state block.StateID, pos pk.Position) (block.StateID, bool) {
	// ChangeOverTimeBlock.getNextState(state, level, pos, random):
	//   int age = getAge().ordinal();
	//   int lessOxidized = 0, moreOxidized = 0;
	//   for (BlockPos p : withinManhattan(pos, 4, 4, 4)) {
	//       if (p.distManhattan(pos) > 4) break;
	//       if (p.equals(pos)) continue;
	//       Block nb = level.getBlockState(p).getBlock();
	//       if (nb instanceof ChangeOverTimeBlock c && c.getAge().getClass() == getAge().getClass()) {
	//           int o = c.getAge().ordinal();
	//           if (o < age) return Optional.empty();       // a LESS-oxidized neighbour halts oxidation
	//           if (o > age) moreOxidized++; else lessOxidized++;
	//       }
	//   }
	//   float f = (moreOxidized + 1.0f) / (moreOxidized + lessOxidized + 1.0f);
	//   float chance = f * f * getChanceModifier();
	//   return nextFloat() < chance ? getNext(state) : Optional.empty();
	age := block.CopperWeatherState(state)
	if age < 0 {
		return state, false
	}
	lessOxidized := 0
	moreOxidized := 0
	// withinManhattan(pos, 4, 4, 4): all cells with |dx|+|dy|+|dz| <= 4 within the 9x9x9 cube. The
	// vanilla iterator yields cells in a fixed order and breaks once distManhattan > 4; because every
	// neighbour with dist <= 4 is visited exactly once and the aggregate counts are order-independent
	// (except the early return on a less-oxidized neighbour, which fires for ANY such neighbour
	// regardless of visitation order), iterating the cube and skipping dist>4 is equivalent. The
	// less-oxidized early return is preserved. CITE: BlockPos.withinManhattan; ChangeOverTimeBlock.getNextState.
	halt := false
	for dx := -4; dx <= 4 && !halt; dx++ {
		for dy := -4; dy <= 4 && !halt; dy++ {
			for dz := -4; dz <= 4; dz++ {
				if copperAbs(dx)+copperAbs(dy)+copperAbs(dz) > 4 {
					continue
				}
				if dx == 0 && dy == 0 && dz == 0 {
					continue // p.equals(pos)
				}
				cell := pk.Position{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
				ns, ok := t.world().GetBlock(cell, dimMinY)
				if !ok {
					continue
				}
				o := block.CopperWeatherState(ns)
				if o < 0 {
					continue // not a weathering-copper (getAge class mismatch) -- ignored
				}
				if o < age {
					// A less-oxidized copper neighbour: getNextState returns empty (oxidation halts).
					return state, false
				}
				if o > age {
					moreOxidized++
				} else {
					lessOxidized++
				}
			}
		}
	}
	f := float32(moreOxidized+1) / float32(moreOxidized+lessOxidized+1)
	chance := f * f * block.CopperChanceModifier(state)
	if r.levelRandom.NextFloat() >= chance {
		return state, false
	}
	return block.CopperGetNext(state)
}

func copperAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
