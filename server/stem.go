package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// stem.go -- the STEM (pumpkin + melon) random-tick handler, ported 1:1 from the unobfuscated 26.2
// jar (net.minecraft.world.level.block.StemBlock.randomTick). Wired into the random-tick driver's
// dispatchRandomTick (random_tick.go). Growth is driven by the world's random-tick pass -- this
// handler is CALLED, never called back.
//
// CITE (temp/cache/26.2-inner.jar, `javap -c -p` this session):
//   StemBlock.randomTick(state, level, pos, random):
//       if (level.getRawBrightness(pos, 0) < 9) return;
//       float speed = CropBlock.getGrowthSpeed(this, level, pos);
//       if (random.nextInt((int)(25.0F / speed) + 1) == 0) {
//           int age = state.getValue(AGE);
//           if (age < 7) {
//               state = state.setValue(AGE, age + 1);
//               level.setBlock(pos, state, 2);
//           } else {
//               Direction dir = Direction.Plane.HORIZONTAL.getRandomDirection(random); // SECOND draw
//               BlockPos target = pos.relative(dir);
//               BlockState below = level.getBlockState(target.below());
//               if (level.getBlockState(target).isAir() && below.is(fruitSupportBlocks)) {
//                   Optional<Block> fruit = registry.getOptional(this.fruit);
//                   Optional<Block> attached = registry.getOptional(this.attachedStem);
//                   if (fruit.isPresent() && attached.isPresent()) {
//                       level.setBlockAndUpdate(target, fruit.get().defaultBlockState());
//                       level.setBlockAndUpdate(pos, attached.get().defaultBlockState()
//                           .setValue(HorizontalDirectionalBlock.FACING, dir));
//                   }
//               }
//           }
//       }
//
// RNG DRAW ORDER (must match the jar exactly):
//   - ONE nextInt((int)(25/speed)+1) growth roll (after the light gate), shared with CropBlock.
//   - AT AGE 7 AND ONLY when the growth roll is 0: a SECOND draw --
//     getRandomDirection(random) == Util.getRandom(faces, random) == ONE nextInt(4) into the
//     HORIZONTAL faces array [NORTH, EAST, SOUTH, WEST]. The AGE<7 grow path draws NOTHING beyond
//     the growth roll. CITE: StemBlock.randomTick; Direction.Plane.HORIZONTAL.getRandomDirection;
//     Util.getRandom(T[], RandomSource) (nextInt(array.length)).
//
// getGrowthSpeed is the SHARED CropBlock.getGrowthSpeed -- the same neighbor-farmland-bonus math the
// crop handler already ports (cropGrowthSpeed in crop_block.go). Its `block` argument is the crop's
// Block; for a stem the same-crop-neighbor `.is(block)` compares are same-stem, so we pass the stem's
// own state to cropGrowthSpeed (whose sameCropAt uses SameCropBlock -- stems are not in that crop
// family, so the neighbor penalty never fires for stems, matching vanilla where two different stems
// are different blocks and the penalty only halves for like-blocks). CITE: CropBlock.getGrowthSpeed.
//
// setBlock flag 2 == UPDATE_CLIENTS (no neighbor notify) for the AGE advance; setBlockAndUpdate
// (flag 3 == UPDATE_CLIENTS | UPDATE_NEIGHBORS) for the fruit + attached-stem placement. Both are
// mirrored as SetBlock + broadcastBlockUpdate (the neighbor reconcile of setBlockAndUpdate is the
// same follow-up seam other flag-3 randomTick edits use).

// stemHorizontalFaces is Direction.Plane.HORIZONTAL's backing faces array, in the EXACT order the
// jar builds it: [NORTH, EAST, SOUTH, WEST]. getRandomDirection indexes it with nextInt(4), so the
// index->direction mapping MUST match this order for the fruit to spawn in the same cell the jar
// picks for a given RNG draw. CITE: Direction$Plane static init (HORIZONTAL faces = NORTH, EAST,
// SOUTH, WEST); Util.getRandom(T[], RandomSource).
var stemHorizontalFaces = [4]struct {
	dir    block.Direction
	dx, dz int
}{
	{block.North, 0, -1}, // NORTH (-z)
	{block.East, 1, 0},   // EAST  (+x)
	{block.South, 0, 1},  // SOUTH (+z)
	{block.West, -1, 0},  // WEST  (-x)
}

// stemRandomTick is StemBlock.randomTick: light-gated growth roll; below AGE 7 it advances AGE, at
// AGE 7 it attempts to spawn the fruit in a random horizontal direction. r is the owning region;
// r.levelRandom is `this.random`. CITE: StemBlock.randomTick.
func (t *TickLoop) stemRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	if !block.IsStem(state) {
		return // defensive: dispatch should already gate this
	}
	// `if (level.getRawBrightness(pos, 0) < 9) return;` -- REAL light read, ambient darkness 0 (the
	// literal the vanilla call passes). CITE: StemBlock.randomTick (getRawBrightness(pos, 0) >= 9).
	if t.rawBrightness(pos, 0) < 9 {
		return
	}
	// `float speed = CropBlock.getGrowthSpeed(this, level, pos)` -- the shared neighbor-farmland-bonus
	// math (cropGrowthSpeed). CITE: CropBlock.getGrowthSpeed.
	speed := t.cropGrowthSpeed(state, pos)
	// `random.nextInt((int)(25.0F / speed) + 1) == 0` -- the (int) cast TRUNCATES toward zero (Java
	// f2i); speed is always >= 1.0f so the bound is >= 1. Grow iff the roll is 0. Same shape as
	// CropBlock.randomTick's roll. CITE: StemBlock.randomTick (ldc 25.0f, fdiv, f2i, iadd 1, nextInt).
	bound := int32(float32(cropGrowthSpeedDivisor)/speed) + 1
	if r.levelRandom.NextIntN(bound) != 0 {
		return
	}
	age := block.StemAge(state)
	if age < 0 {
		return // not a stem (defensive)
	}
	if age < block.StemMaxAge {
		// `state = state.setValue(AGE, age+1); level.setBlock(pos, state, 2)`.
		if grown, ok := block.StemWithAge(state, age+1); ok {
			if t.world().SetBlock(pos, grown, dimMinY) {
				t.broadcastBlockUpdate(pos, grown)
			}
		}
		return
	}
	// AGE == 7: fruit-spawn path.
	// `Direction dir = Direction.Plane.HORIZONTAL.getRandomDirection(random)` == Util.getRandom(faces,
	// random) == ONE nextInt(4) into [NORTH, EAST, SOUTH, WEST]. This is the SECOND levelRandom draw,
	// taken ONLY at AGE 7 after a 0 growth roll. CITE: StemBlock.randomTick; getRandomDirection.
	idx := r.levelRandom.NextIntN(4)
	if idx < 0 || idx >= 4 {
		return // defensive (NextIntN(4) is always 0..3)
	}
	face := stemHorizontalFaces[idx]
	target := pk.Position{X: pos.X + face.dx, Y: pos.Y, Z: pos.Z + face.dz}
	// `BlockState below = level.getBlockState(target.below());` then
	// `if (level.getBlockState(target).isAir() && below.is(fruitSupportBlocks))`. The bytecode reads
	// the below-state into a slot first, then checks target.isAir(), then below.is(fruitSupportBlocks);
	// evaluation order does not change the outcome (both must hold), and neither read draws RNG.
	targetState, okT := t.world().GetBlock(target, dimMinY)
	if !okT || !block.IsAir(targetState) {
		return // target cell not air -> no fruit
	}
	belowState, okB := t.world().GetBlock(below(target), dimMinY)
	if !okB || !block.StemFruitSupport(belowState) {
		return // below not in #supports_stem_fruit (== #supports_vegetation) -> no fruit
	}
	// fruit.isPresent() && attachedStem.isPresent() -- always true for the vanilla registry; the Go
	// resolvers return ok=false only for a bad state, which we already excluded. Place both:
	//   level.setBlockAndUpdate(target, fruit.defaultBlockState());
	//   level.setBlockAndUpdate(pos, attachedStem.defaultBlockState().setValue(FACING, dir));
	fruit, okF := block.StemFruitState(state)
	attached, okA := block.StemAttachedState(state, face.dir)
	if !okF || !okA {
		return
	}
	if t.world().SetBlock(target, fruit, dimMinY) {
		t.broadcastBlockUpdate(target, fruit)
	}
	if t.world().SetBlock(pos, attached, dimMinY) {
		t.broadcastBlockUpdate(pos, attached)
	}
}
