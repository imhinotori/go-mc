package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// kelp.go -- the KELP random-tick handler, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.level.block.KelpBlock / GrowingPlantHeadBlock.randomTick -- the shared
// head-grow). Wired into the random-tick driver's dispatchRandomTick (random_tick.go). Growth is
// driven by the world's random-tick pass -- this handler is CALLED, never called back.
//
// CITE (temp/cache/26.2-inner.jar, `javap -c -p` this session):
//   GrowingPlantHeadBlock.randomTick(state, level, pos, random):
//       if (state.getValue(AGE) < 25) {
//           if (random.nextDouble() < this.growPerTickProbability) {   // 0.14 for kelp
//               BlockPos target = pos.relative(this.growthDirection);  // UP for kelp
//               if (canGrowInto(level.getBlockState(target))) {        // is(Blocks.WATER) for kelp
//                   level.setBlockAndUpdate(target, getGrowIntoState(state, level.getRandom()));
//               }
//           }
//       }
//   GrowingPlantHeadBlock.getGrowIntoState(state, random):  // KelpBlock does NOT override
//       return state.cycle(AGE);   // AGE+1 (the AGE<25 gate prevents a wrap); no RNG draw
//   KelpBlock: growthDirection = UP, growPerTickProbability = 0.14, canGrowInto == is(Blocks.WATER).
//
// RNG DRAW ORDER (must match the jar exactly): the AGE < 25 gate draws NOTHING (IsRandomlyTicking
// already excludes a max head, but the guard is mirrored). Then ONE nextDouble() < 0.14 growth roll.
// The canGrowInto read and getGrowIntoState (state.cycle(AGE)) draw NOTHING further -- KelpBlock does
// not override getGrowIntoState, so the level.getRandom() it is handed is never consumed. CITE:
// GrowingPlantHeadBlock.randomTick; KelpBlock (0.14, UP, canGrowInto == is(WATER)).
//
// setBlockAndUpdate (flag 3 == UPDATE_CLIENTS | UPDATE_NEIGHBORS) -- mirrored as SetBlock +
// broadcastBlockUpdate (the neighbor reconcile is the same follow-up seam other flag-3 randomTick
// edits use). The old head at pos stays a head this tick; the head->body (KELP_PLANT) conversion is
// a separate updateShape path, NOT part of randomTick. CITE: GrowingPlantHeadBlock.randomTick.
func (t *TickLoop) kelpRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	age := block.KelpAge(state)
	if age < 0 {
		return // not a kelp head (defensive)
	}
	// `if (state.getValue(AGE) < 25)` -- gate FIRST (no draw). IsRandomlyTicking already excludes a
	// max head, but mirror the guard.
	if age >= block.KelpMaxAge {
		return
	}
	// `if (random.nextDouble() < 0.14)` -- the ONLY RNG draw. CITE: GrowingPlantHeadBlock.randomTick
	// (nextDouble() < growPerTickProbability); KelpBlock (0.14d).
	if r.levelRandom.NextDouble() >= block.KelpGrowChance {
		return
	}
	// target = pos.relative(growthDirection) -- growthDirection is UP for kelp.
	target := above(pos)
	// `if (canGrowInto(level.getBlockState(target)))` -- KelpBlock.canGrowInto == is(Blocks.WATER)
	// (the strict water BLOCK, any level; NOT a waterlogged cell). CITE: KelpBlock.canGrowInto.
	ts, ok := t.world().GetBlock(target, dimMinY)
	if !ok || !block.IsWaterBlock(ts) {
		return
	}
	// setBlockAndUpdate(target, getGrowIntoState(state) == state.cycle(AGE) == AGE+1).
	if grown, ok := block.KelpWithAge(state, age+1); ok {
		if t.world().SetBlock(target, grown, dimMinY) {
			t.broadcastBlockUpdate(target, grown)
		}
	}
}
