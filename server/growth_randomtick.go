package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// growth_randomtick.go -- the NETHER-WART / CHORUS-FLOWER / TURTLE-EGG / BUDDING-AMETHYST /
// CAVE-VINES random-tick handlers, ported 1:1 from the unobfuscated 26.2 jar. Each is wired into
// the random-tick driver's dispatchRandomTick (random_tick.go) by block identity. Growth is driven
// by the world's random-tick pass -- these handlers are CALLED, never called back.
//
// PIG-ORACLE SAFETY: the random-tick driver never runs on the pig-oracle path
// (TestPluginPigEqualsGoNativePig drives serverAiStep directly, never tickRandomBlocks), so these
// handlers do NOT pin the levelRandom stream the pig oracle uses. The draws below are deterministic
// for any seeded levelRandom so the seeded growth tests pin byte-identical outcomes. CITE:
// ServerChunkCache.tickChunks + ServerLevel.tickChunk (no pig-oracle caller).

// ---- NETHER WART (NetherWartBlock.randomTick) ----

// netherWartRandomTick is NetherWartBlock.randomTick: a wart below MAX_AGE (3) advances AGE with a
// 1-in-10 chance. r is the owning region; r.levelRandom is `this.random`.
//
// RNG DRAW ORDER (must match the jar exactly): the age < 3 gate draws NOTHING (IsRandomlyTicking
// already excludes a max wart, but the guard is mirrored). Then ONE nextInt(10); only on a 0 roll
// is AGE advanced. There is NO light/moisture/neighbour gate. CITE: NetherWartBlock.randomTick.
//
//	int age = state.getValue(AGE);
//	if (age < 3 && random.nextInt(10) == 0) level.setBlock(pos, state.setValue(AGE, age+1), 2);
//
// setBlock flag 2 == UPDATE_CLIENTS (no neighbour notify) -- mirrored as SetBlock + broadcast.
func (t *TickLoop) netherWartRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	age := block.NetherWartAge(state)
	if age < 0 {
		return // not a nether wart (defensive)
	}
	// `age < 3` gate FIRST (no draw). IsRandomlyTicking already excludes a max wart, but mirror it.
	if age >= block.NetherWartMaxAge {
		return
	}
	// `random.nextInt(10) == 0` -- the ONLY RNG draw. CITE: NetherWartBlock.randomTick.
	if r.levelRandom.NextIntN(10) != 0 {
		return
	}
	if grown, ok := block.NetherWartWithAge(state, age+1); ok {
		if t.world().SetBlock(pos, grown, dimMinY) {
			t.broadcastBlockUpdate(pos, grown)
		}
	}
}

// ---- CHORUS FLOWER (ChorusFlowerBlock.randomTick) ----

// chorusFlowerRandomTick is ChorusFlowerBlock.randomTick: a living flower (AGE < DEAD_AGE 5) either
// grows UP (converting itself to a chorus-plant stem + a new flower above at the SAME age), branches
// sideways (converting itself to a stem + branch flowers at age+1), or dies. r is the owning region;
// r.levelRandom is `this.random`. CITE: ChorusFlowerBlock.randomTick.
//
// RNG DRAW ORDER (must match the jar exactly):
//   - draw #1: random.nextInt(fromPlant ? 5 : 4) -- ONLY in the below.is(CHORUS_PLANT) branch AND
//     only when stemHeight >= 2 (the `stemHeight < 2 ||` short-circuit skips the draw when < 2).
//   - draw #2: random.nextInt(4) -- in the sideways-branch phase (age < 4, could not grow up).
//   - draw #3: HORIZONTAL.getRandomDirection(random) == faces[nextInt(4)] -- once PER branch loop
//     iteration (spread times, spread+1 when fromPlant).
//
// The grow-up path consumes only draw #1 (already spent while deciding canGrowUp); the branch path
// consumes draws #2 then #3xN. CITE: ChorusFlowerBlock.randomTick (offsets 182, 273, 305).
//
// All setBlock edits use flag 2 (UPDATE_CLIENTS). The ChorusPlantBlock.getStateWithConnections
// reconcile of the self->stem conversion is a cited deferral (the default stem state is placed;
// connection reconcile is a follow-up). levelEvent grow/death FX (1033/1034) are cosmetic deferrals.
func (t *TickLoop) chorusFlowerRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	if !block.IsChorusFlower(state) {
		return // defensive: dispatch should already gate this
	}
	abovePos := above(pos)
	// `if (level.isEmptyBlock(above) && above.getY() > level.getMaxY()) return;` -- refuse to grow
	// off the top of the world.
	if t.isEmptyBlockAt(abovePos) && abovePos.Y > dimMaxY {
		return
	}
	age := block.ChorusFlowerAge(state)
	if age < 0 {
		return // not a chorus flower (defensive)
	}
	// `if (age >= DEAD_AGE) return;` -- a dead flower does not grow. IsRandomlyTicking already excludes
	// it, but mirror the guard.
	if age >= block.ChorusFlowerDeadAge {
		return
	}

	canGrowUp := false
	fromPlant := false

	// belowState = level.getBlockState(pos.below()).
	belowPos := below(pos)
	belowState, belowOK := t.world().GetBlock(belowPos, dimMinY)

	switch {
	case belowOK && block.IsEndStone(belowState):
		// `if (below.is(SUPPORTS_CHORUS_FLOWER)) canGrowUp = true;` -- grounded on end stone.
		canGrowUp = true
	case belowOK && block.IsChorusPlant(belowState):
		// On top of a chorus-plant stem: count the stem height (up to 4 below), detect an end-stone
		// base (fromPlant), then roll growth. `int stemHeight = 1;`.
		stemHeight := 1
		for j := 0; j < 4; j++ {
			// under = level.getBlockState(pos.below(stemHeight + 1)).
			underPos := pk.Position{X: pos.X, Y: pos.Y - (stemHeight + 1), Z: pos.Z}
			under, underOK := t.world().GetBlock(underPos, dimMinY)
			if underOK && block.IsChorusPlant(under) {
				stemHeight++
			} else {
				if underOK && block.IsEndStone(under) {
					fromPlant = true
				}
				break
			}
		}
		// `if (stemHeight < 2 || stemHeight <= random.nextInt(fromPlant ? 5 : 4)) canGrowUp = true;`
		// -- the nextInt is drawn ONLY when stemHeight >= 2 (Java && short-circuit on the `||`).
		if stemHeight < 2 {
			canGrowUp = true
		} else {
			bound := int32(4)
			if fromPlant {
				bound = 5
			}
			if int32(stemHeight) <= r.levelRandom.NextIntN(bound) {
				canGrowUp = true
			}
		}
	case belowOK && block.IsAir(belowState):
		// `else if (below.isAir()) canGrowUp = true;` -- floating over air.
		canGrowUp = true
	}

	// `if (canGrowUp && allNeighborsEmpty(level, above, null) && level.isEmptyBlock(pos.above(2)))`.
	twoAbove := pk.Position{X: pos.X, Y: pos.Y + 2, Z: pos.Z}
	if canGrowUp && t.chorusAllNeighborsEmpty(abovePos, -1) && t.isEmptyBlockAt(twoAbove) {
		// GROW UP: convert self to a chorus-plant stem (flag 2), then place a new flower above at the
		// SAME age.
		if stem, ok := block.ChorusPlantDefaultState(), true; ok {
			if t.world().SetBlock(pos, stem, dimMinY) {
				t.broadcastBlockUpdate(pos, stem)
			}
		}
		t.chorusPlaceGrownFlower(abovePos, age)
		return
	}

	if age < 4 {
		// SIDEWAYS BRANCH PHASE.
		// `int spread = random.nextInt(4); if (fromPlant) spread++;`.
		spread := int(r.levelRandom.NextIntN(4))
		if fromPlant {
			spread++
		}
		placedAny := false
		for k := 0; k < spread; k++ {
			// `Direction dir = HORIZONTAL.getRandomDirection(random) == faces[nextInt(4)]` -- one draw
			// per iteration. faces order NORTH, SOUTH, WEST, EAST (horizontalDirections).
			idx := r.levelRandom.NextIntN(4)
			dir := horizontalDirections[idx]
			side := pk.Position{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz}
			sideBelow := pk.Position{X: side.X, Y: side.Y - 1, Z: side.Z}
			// `if (isEmptyBlock(side) && isEmptyBlock(side.below()) && allNeighborsEmpty(level, side,
			// dir.getOpposite()))` -- the opposite direction (the way we came) is exempted from the
			// empty-neighbour scan.
			if t.isEmptyBlockAt(side) && t.isEmptyBlockAt(sideBelow) && t.chorusAllNeighborsEmpty(side, chorusOppositeHorizontal(int(idx))) {
				t.chorusPlaceGrownFlower(side, age+1)
				placedAny = true
			}
		}
		if placedAny {
			// Some branch placed: convert self to a stem (flag 2).
			if stem, ok := block.ChorusPlantDefaultState(), true; ok {
				if t.world().SetBlock(pos, stem, dimMinY) {
					t.broadcastBlockUpdate(pos, stem)
				}
			}
		} else {
			// No branch placed: this flower dies.
			t.chorusPlaceDeadFlower(pos)
		}
	} else {
		// age >= 4 and could not grow up: the flower dies.
		t.chorusPlaceDeadFlower(pos)
	}
}

// chorusAllNeighborsEmpty is ChorusFlowerBlock.allNeighborsEmpty(level, pos, except): every
// horizontal neighbour of pos (skipping the `except` direction, an index into horizontalDirections
// or -1 for none) must be empty (air). CITE: ChorusFlowerBlock.allNeighborsEmpty.
func (t *TickLoop) chorusAllNeighborsEmpty(pos pk.Position, exceptIdx int) bool {
	for i, d := range horizontalDirections {
		if i == exceptIdx {
			continue
		}
		nb := pk.Position{X: pos.X + d.dx, Y: pos.Y, Z: pos.Z + d.dz}
		if !t.isEmptyBlockAt(nb) {
			return false
		}
	}
	return true
}

// chorusOppositeHorizontal returns the horizontalDirections index of the opposite of idx. Order is
// NORTH(0), SOUTH(1), WEST(2), EAST(3); NORTH<->SOUTH, WEST<->EAST. CITE: Direction.getOpposite.
func chorusOppositeHorizontal(idx int) int {
	switch idx {
	case 0:
		return 1 // NORTH -> SOUTH
	case 1:
		return 0 // SOUTH -> NORTH
	case 2:
		return 3 // WEST -> EAST
	case 3:
		return 2 // EAST -> WEST
	default:
		return -1
	}
}

// chorusPlaceGrownFlower is ChorusFlowerBlock.placeGrownFlower(level, pos, age): setBlock a fresh
// chorus flower at the given AGE (flag 2). levelEvent(1033) grow FX is a cosmetic deferral. CITE:
// ChorusFlowerBlock.placeGrownFlower.
func (t *TickLoop) chorusPlaceGrownFlower(pos pk.Position, age int) {
	if flower, ok := block.ChorusFlowerWithAge(block.ChorusFlowerDefaultState(), age); ok {
		if t.world().SetBlock(pos, flower, dimMinY) {
			t.broadcastBlockUpdate(pos, flower)
		}
	}
}

// chorusPlaceDeadFlower is ChorusFlowerBlock.placeDeadFlower(level, pos): setBlock a chorus flower at
// AGE DEAD_AGE (5) (flag 2). levelEvent(1034) death FX is a cosmetic deferral. CITE:
// ChorusFlowerBlock.placeDeadFlower.
func (t *TickLoop) chorusPlaceDeadFlower(pos pk.Position) {
	if dead, ok := block.ChorusFlowerWithAge(block.ChorusFlowerDefaultState(), block.ChorusFlowerDeadAge); ok {
		if t.world().SetBlock(pos, dead, dimMinY) {
			t.broadcastBlockUpdate(pos, dead)
		}
	}
}
