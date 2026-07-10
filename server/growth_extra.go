package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// growth_extra.go — CACTUS, BAMBOO (sapling + stalk), ICE, and SNOW-LAYER random-tick handlers,
// ported 1:1 from the unobfuscated 26.2 jar. They are the fifth growth family wired into the
// random-tick driver's dispatchRandomTick (random_tick.go). Growth is driven by the world's
// random-tick pass — these handlers are CALLED, never called back.
//
// CITE (temp/cache/26.2-inner.jar, `javap -c -p` this session):
//   CactusBlock.randomTick(state, level, pos, random):
//       BlockPos above = pos.above(); if (!level.isEmptyBlock(above)) return;
//       int i = 1; int j = state.getValue(AGE);
//       while (level.getBlockState(pos.below(i)).is(this)) {
//           if (++i == 3 && j == 15) return;     // 3-tall AND age 15: no growth possible
//       }
//       if (j == 8 && canSurvive(defaultBlockState, level, above)) {
//           double d = i >= 3 ? 0.25 : 0.10;
//           if (random.nextDouble() <= d)
//               level.setBlockAndUpdate(above, Blocks.CACTUS_FLOWER.defaultBlockState());
//       } else if (j == 15 && i < 3) {
//           level.setBlockAndUpdate(above, defaultBlockState());
//           level.setBlock(pos, state.setValue(AGE, 0), 260);
//           level.neighborChanged(state, above, this, null, false);
//       }
//       if (j < 15) level.setBlock(pos, state.setValue(AGE, j+1), 260);
//
//   CactusBlock.canSurvive(state, level, pos):
//       for (Direction d : Direction.Plane.HORIZONTAL) {
//           BlockState bs = level.getBlockState(pos.relative(d));
//           if (bs.isSolid()) return false;
//           if (level.getFluidState(pos.relative(d)).is(LAVA)) return false;
//       }
//       BlockState below = level.getBlockState(pos.below());
//       if (below.is(this) || below.is(SUPPORTS_CACTUS))
//           return !level.getBlockState(pos.above()).liquid();
//       return false;
//
//   BambooSaplingBlock.randomTick(state, level, pos, random):
//       if (random.nextInt(3) != 0) return;       // UNCONDITIONAL draw
//       if (!level.isEmptyBlock(pos.above())) return;
//       if (level.getRawBrightness(pos.above(), 0) < 9) return;
//       growBamboo(level, pos);                    // sapling: defaultBlockState + level random
//
//   BambooStalkBlock.randomTick(state, level, pos, random):
//       if (state.getValue(STAGE) != 0) return;   // only growing stalks tick
//       if (random.nextInt(3) != 0) return;       // UNCONDITIONAL draw
//       if (!level.isEmptyBlock(pos.above())) return;
//       if (level.getRawBrightness(pos.above(), 0) < 9) return;
//       int heightBelow = getHeightBelowUpToMax(level, pos) + 1;
//       if (heightBelow >= 16) return;
//       growBamboo(state, level, pos, random, heightBelow);
//
//   BambooStalkBlock.growBamboo(state, level, pos, random, heightBelow):
//       BlockState below1 = level.getBlockState(pos.below());
//       BlockPos below2 = pos.below(2);
//       BlockState below2State = level.getBlockState(below2);
//       BambooLeaves leaves = NONE;
//       if (heightBelow >= 1) {
//           if (below1.is(BAMBOO) && below1.getValue(LEAVES) == NONE) leaves = SMALL;
//           else if (below1.is(BAMBOO) && below1.getValue(LEAVES) != NONE) {
//               leaves = LARGE;
//               if (below2State.is(BAMBOO)) {
//                   level.setBlock(pos.below(), below1.setValue(LEAVES, SMALL), 3);
//                   level.setBlock(below2, below2State.setValue(LEAVES, NONE), 3);
//               }
//           }
//       }
//       boolean thickBamboo = state.getValue(AGE) == 1 || below2State.is(BAMBOO);
//       int newStage;
//       if (heightBelow >= 11) {
//           if (random.nextFloat() < 0.25f) newStage = 1;
//           else if (heightBelow != 15) newStage = 0;
//           else newStage = 1;
//       } else newStage = 0;
//       level.setBlock(pos.above(),
//           defaultBlockState().setValue(AGE, thickBamboo ? 1 : 0)
//                              .setValue(LEAVES, leaves)
//                              .setValue(STAGE, newStage), 3);
//
//   BambooStalkBlock.getHeightBelowUpToMax(level, pos):
//       for (int i = 0; i < 16 && level.getBlockState(pos.below(i+1)).is(BAMBOO); ++i);
//       return i;
//
//   IceBlock.randomTick(state, level, pos, random):
//       if (level.getBrightness(LightLayer.BLOCK, pos) > 11 - state.getLightDampening())
//           melt(state, level, pos);              // melt -> setBlockAndUpdate(WATER{Level:0})
//
//   SnowLayerBlock.randomTick(state, level, pos, random):
//       if (level.getBrightness(LightLayer.BLOCK, pos) > 11) {
//           dropResources(state, level, pos);     // DEFERRED (loot subsystem)
//           level.removeBlock(pos, false);
//       }
//
// PIG-ORACLE SAFETY: the random-tick driver never runs on the pig-oracle path (TestPluginPig
// EqualsGoNativePig drives serverAiStep directly), so these handlers do NOT pin the levelRandom
// stream the pig oracle uses. The draws below are deterministic for any seeded levelRandom so the
// fully-asserted Cactus / BambooSapling / BambooStalk tests can pin byte-identical growth from a
// deterministic seed. CITE: ServerChunkCache.tickChunks + ServerLevel.tickChunk (no pig-oracle
// caller).

// ---- CACTUS (CactusBlock.randomTick) ----

// cactusRandomTick is CactusBlock.randomTick: age-gated grow/flower. r is the owning region; its
// levelRandom is `this.random` handed to the handler.
//
// RNG DRAW ORDER (must match the jar exactly): ONE nextDouble draw ONLY in the age==8 canSurvive
// branch (the flower-roll). The age-15 grow-up + AGE++ branches draw NO levelRandom. The vanilla
// call site is:
//	if (j == 8 && canSurvive(...)) {
//	    double d = i >= 3 ? 0.25 : 0.10;
//	    if (random.nextDouble() <= d) level.setBlockAndUpdate(above, CACTUS_FLOWER);
//	}
// CITE: CactusBlock.randomTick.
//
// neighborChanged on the age-15 grow path is DEFERRED (no neighbor-changed dispatcher seam in v1
// for a vanilla randomTick edit); the load-bearing behavior — a new cactus cell + AGE reset — is
// performed here. CITE: CactusBlock.randomTick (`level.neighborChanged(state, above, this, null,
// false)`).
func (t *TickLoop) cactusRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	if !block.IsCactus(state) {
		return // defensive: dispatch should already gate this
	}
	abovePos := above(pos)
	// `if (!level.isEmptyBlock(pos.above())) return;` — vanilla isEmptyBlock == state.isAir().
	aboveState, ok := t.world().GetBlock(abovePos, dimMinY)
	if !ok || !block.IsAir(aboveState) {
		return
	}
	// `int i = 1; int j = getAge(state);` — i is the height counter (this cell counts as 1), j is
	// the AGE property.
	age := block.CactusAge(state)
	if age < 0 {
		return // not a cactus (defensive)
	}
	height := 1
	for {
		bp := pk.Position{X: pos.X, Y: pos.Y - height, Z: pos.Z}
		bs, ok := t.world().GetBlock(bp, dimMinY)
		if !ok || !block.IsCactus(bs) {
			break
		}
		height++
		if height == 3 {
			// 3-tall: per the bytecode, when i hits 3 the loop continues iff age != 15; the grow
			// age (15) of a 3-tall cactus breaks out and the age==15 grow-up branch checks `i < 3`,
			// so a 3-tall age-15 cactus can NOT grow further. Mirror exactly: only break+return when
			// BOTH height==3 AND age==15. CITE: CactusBlock.randomTick (offsets 51-67).
			if age == 15 {
				return
			}
			break
		}
	}
	// `if (j == 8 && canSurvive(defaultBlockState, level, above))` — try to flower. The canSurvive
	// check uses the FLOWER's default state (a cactus with AGE 0), not the current state: the
	// canSurvive body does not read AGE; it checks the below-state (must be cactus or in
	// #supports_cactus) and the horizontal-neighbors (must not be solid or lava). Mirror that
	// with cactusCanSurvive passing the defaultBlockState.
	if age == 8 {
		cactusDefault := block.CactusDefaultState()
		if t.cactusCanSurvive(cactusDefault, abovePos) {
			// `double d = i >= 3 ? 0.25 : 0.10` — the doubled chance for a 3-tall cactus.
			var chance float64 = 0.10
			if height >= 3 {
				chance = 0.25
			}
			// `if (random.nextDouble() <= d)` — the ONLY RNG draw in the handler, gated on
			// canSurvive (i.e. the flower's would-be position survives). CITE: CactusBlock.randomTick.
			if r.levelRandom.NextDouble() <= chance {
				if flower, ok := block.CactusFlowerState(), true; ok {
					if t.world().SetBlock(abovePos, flower, dimMinY) {
						t.broadcastBlockUpdate(abovePos, flower)
					}
				}
			}
		}
	} else if age == 15 && height < 3 {
		// Age 15 + below-cap: place a new cactus above and reset this cell's AGE to 0.
		// `level.setBlockAndUpdate(pos.above(), defaultBlockState())` — flag 10 (UPDATE_CLIENTS |
		// UPDATE_NEIGHBORS); mirrored as SetBlock + broadcast (broadcast emits the block-update
		// packet; the neighbor reconcile is a follow-up handled by the same edit seam as other
		// flag-10 edits).
		cactusDefault := block.CactusDefaultState()
		if t.world().SetBlock(abovePos, cactusDefault, dimMinY) {
			t.broadcastBlockUpdate(abovePos, cactusDefault)
		}
		// `level.setBlock(pos, state.setValue(AGE, 0), 260)` — flag 260 (UPDATE_CLIENTS |
		// UPDATE_KNOWN_SHAPE, no neighbor notify).
		if reset, ok := block.CactusWithAge(state, 0); ok {
			if t.world().SetBlock(pos, reset, dimMinY) {
				t.broadcastBlockUpdate(pos, reset)
			}
		}
		// `level.neighborChanged(state, above, this, null, false)` — DEFERRED (see file doc).
	}
	// `if (j < 15) level.setBlock(pos, state.setValue(AGE, j+1), 260)` — the steady AGE advance.
	if age < 15 {
		if grown, ok := block.CactusWithAge(state, age+1); ok {
			if t.world().SetBlock(pos, grown, dimMinY) {
				t.broadcastBlockUpdate(pos, grown)
			}
		}
	}
}

// cactusCanSurvive is CactusBlock.canSurvive(state, level, pos). ported 1:1 from the jar:
//   - for each horizontal neighbour: if the neighbour block is SOLID or has a LAVA fluid, return
//     false;
//   - else check below: if below is THIS (cactus) OR in BlockTags.SUPPORTS_CACTUS, then check above:
//     if above is NOT a liquid, return true;
//   - else return false.
//
// state is unused in the vanilla body (the bytecode does not consult its AGE or any property),
// but the signature carries it for parity with the jar (the grow/can-survive paths pass the
// defaultBlockState). CITE: CactusBlock.canSurvive.
//
// SUPPORTS_CACTUS (BlockTags) is `#minecraft:sand` == {sand, red_sand, suspicious_sand} — the exact
// closure block.IsSand implements. A cactus survives on sand or on another cactus, and nowhere else
// (dirt/grass do NOT support cactus in vanilla). CITE: BlockTags.SUPPORTS_CACTUS
// (tags/block/supports_cactus.json -> #minecraft:sand); block.IsSand (tags/block/sand.json).
func (t *TickLoop) cactusCanSurvive(_ block.StateID, pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	// Direction.Plane.HORIZONTAL: the 4 cardinal horizontal neighbours.
	for _, d := range horizontalDirections {
		nb := pk.Position{X: pos.X + d.dx, Y: pos.Y, Z: pos.Z + d.dz}
		nbState, ok := t.world().GetBlock(nb, dimMinY)
		if !ok {
			continue // unreadable neighbour: not solid, not lava — neither branch fires
		}
		// bs.isSolid() -> return false.
		if block.IsSolid(nbState) {
			return false
		}
		// fs.is(FluidTags.LAVA) -> return false. Sulfur's lava block carries the lava fluid; an
		// out-of-range or unreadable neighbour we already skipped. CITE: CactusBlock.canSurvive
		// (fs.is(LAVA)).
		if block.StateFluidIsLava(nbState) {
			return false
		}
	}
	// below state.
	belowPos := below(pos)
	belowState, ok := t.world().GetBlock(belowPos, dimMinY)
	if !ok {
		return false
	}
	// `if (below.is(this) || below.is(SUPPORTS_CACTUS))` — bare this (cactus on cactus) OR
	// below-in-#minecraft:supports_cactus. That tag is `#minecraft:sand` (sand / red_sand /
	// suspicious_sand): a cactus survives ONLY on sand or another cactus, NEVER on dirt/grass.
	// CITE: BlockTags.SUPPORTS_CACTUS (tags/block/supports_cactus.json -> #minecraft:sand);
	// block.IsSand is that exact tag closure.
	if block.IsCactus(belowState) || block.IsSand(belowState) {
		// `return !getBlockState(pos.above()).liquid()` — above must not be a fluid (water/lava).
		// CITE: CactusBlock.canSurvive (state.liquid()).
		aboveState, ok := t.world().GetBlock(above(pos), dimMinY)
		if !ok {
			return true // above unreadable -> not a fluid -> survives
		}
		return !block.IsFluid(aboveState)
	}
	return false
}

// ---- COCOA (CocoaBlock.randomTick) ----

// cocoaRandomTick is CocoaBlock.randomTick: a maturing cocoa pod advances AGE with a 1-in-5 chance.
// r is the owning region; r.levelRandom is `this.random`.
//
// RNG DRAW ORDER (must match the jar exactly): ONE unconditional nextInt(5) at the method head
// (offsets 0-10) -- drawn BEFORE any state read, so it always advances the stream when a cocoa cell
// is sampled. Only on a 0 roll is AGE read and (if AGE < MAX_AGE 2) advanced. There is NO light gate
// (CocoaBlock.randomTick has none). CITE: CocoaBlock.randomTick.
//
//	if (random.nextInt(5) == 0) {
//	    int age = state.getValue(AGE);
//	    if (age < 2) level.setBlock(pos, state.setValue(AGE, age + 1), 2);
//	}
//
// setBlock flag 2 == UPDATE_CLIENTS (no neighbor notify) -- mirrored as SetBlock + broadcast, the
// same flag-2 shape crop_block.go uses.
func (t *TickLoop) cocoaRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	// `if (random.nextInt(5) == 0)` -- the unconditional draw is FIRST, before any state read.
	if r.levelRandom.NextIntN(5) != 0 {
		return
	}
	age := block.CocoaAge(state)
	if age < 0 {
		return // not a cocoa (defensive; dispatch already gates this)
	}
	// `if (age < MAX_AGE) setBlock(state.setValue(AGE, age+1), 2)`. IsRandomlyTicking already excludes a
	// max-age pod, but mirror the guard for fidelity.
	if age >= block.CocoaMaxAge {
		return
	}
	if grown, ok := block.CocoaWithAge(state, age+1); ok {
		if t.world().SetBlock(pos, grown, dimMinY) {
			t.broadcastBlockUpdate(pos, grown)
		}
	}
}

// ---- BAMBOO SAPLING (BambooSaplingBlock.randomTick) ----

// bambooSaplingRandomTick is BambooSaplingBlock.randomTick: grow a sapling to a stalk. r is the
// owning region; r.levelRandom is `this.random`.
//
// RNG DRAW ORDER (must match the jar exactly): ONE unconditional nextInt(3) draw — drawn BEFORE
// any state read, so it always advances the stream when a bamboo sapling cell is sampled. Only on
// a 0 roll do the empty-above + light gates fire and growBamboo runs. CITE:
// BambooSaplingBlock.randomTick (offsets 0-42: `if (random.nextInt(3) != 0) return;`).
//
// The grow itself delegates to growBamboo with the SAPLING signature: defaultBlockState + level
// random + heightBelow = getHeightBelowUpToMax(level, pos) + 1 (the same stalk-side helper). The
// sapling has no state/random draws inside growBamboo that the stalk path does not have, so the
// single-call delegation is faithful (one setBlock at pos.above()). CITE:
// BambooSaplingBlock.growBamboo(Level, BlockPos).
func (t *TickLoop) bambooSaplingRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	_ = state
	// `if (random.nextInt(3) != 0) return;` — the unconditional draw is FIRST, before any state
	// check, so the levelRandom stream advances even when the sapling fails to grow.
	if r.levelRandom.NextIntN(3) != 0 {
		return
	}
	// `if (!level.isEmptyBlock(pos.above())) return;`
	abovePos := above(pos)
	if !t.isEmptyBlockAt(abovePos) {
		return
	}
	// `if (level.getRawBrightness(pos.above(), 0) < 9) return;` — REAL light read over the world,
	// sky-darkened with ambientDarkness=0 (a "max-sky" probe). CITE: BambooSaplingBlock.randomTick
	// (getRawBrightness(pos, 0) < 9).
	if t.rawBrightness(abovePos, 0) < 9 {
		return
	}
	// growBamboo(level, pos): uses this.defaultBlockState() (a Bamboo{Age:0, Leaves:NONE, Stage:0})
	// and the level's random; heightBelow is getHeightBelowUpToMax(level, pos) + 1.
	if bambooDefault, ok := block.BambooState(0, block.BambooLeavesNone, 0); ok {
		t.bambooGrowBamboo(bambooDefault, pos, r.levelRandom, t.bambooHeightBelow(pos)+1)
	}
}

// ---- BAMBOO STALK (BambooStalkBlock.randomTick / growBamboo) ----

// bambooStalkRandomTick is BambooStalkBlock.randomTick: a STAGE-0 stalk grows up. r is the
// owning region; r.levelRandom is `this.random`.
//
// RNG DRAW ORDER (must match the jar exactly):
//   - ONE unconditional nextInt(3) at the method head (offsets 17-25); short-circuited to return
//     on a non-zero result.
//   - ZERO draws inside the heightBelow scan (it walks pos.below(i+1) for i in 0..15 and counts
//     consecutive BAMBOO cells; no levelRandom is consulted).
//   - ONE gated nextFloat INSIDE growBamboo's STAGE-decision (offsets 198-207): drawn ONLY when
//     heightBelow >= 11; on a < 0.25f result STAGE advances to 1, else the heightBelow==15 short-
//     circuit to STAGE=1 applies (15 == max stalk height), otherwise STAGE stays 0.
//
// NOTE on the audit claim "ONE nextInt(2) inside the place loop's height accumulator":
// BambooStalkBlock.randomTick does NOT contain a place loop with nextInt(2); the inner
// 1+nextInt(2) loop lives in BambooStalkBlock.performBonemeal (the bonemeal path), NOT randomTick.
// randomTick ends with a SINGLE call to growBamboo which performs exactly ONE setBlock (offset
// 270-277). The nextInt(2) draw must NOT be added to the randomTick handler — it would perturb
// the randomTick stream vs the jar and break the byte-identical pig-oracle gate (it never runs
// here, but the seeded growth tests pin exact draw counts). CITE: BambooStalkBlock.randomTick
// (offsets 0-81).
func (t *TickLoop) bambooStalkRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	if !block.IsBamboo(state) {
		return // defensive: dispatch should already gate this
	}
	// `if (state.getValue(STAGE) != 0) return;` — only a still-growing stalk ticks. IsRandomlyTicking
	// already mirrors this (only STAGE-0 stalks return true), but the guard is mirrored for
	// fidelity.
	if block.BambooStage(state) != 0 {
		return
	}
	// `if (random.nextInt(3) != 0) return;` — UNCONDITIONAL draw before any state read.
	if r.levelRandom.NextIntN(3) != 0 {
		return
	}
	// `if (!level.isEmptyBlock(pos.above())) return;`
	abovePos := above(pos)
	if !t.isEmptyBlockAt(abovePos) {
		return
	}
	// `if (level.getRawBrightness(pos.above(), 0) < 9) return;`
	if t.rawBrightness(abovePos, 0) < 9 {
		return
	}
	// `int heightBelow = getHeightBelowUpToMax(level, pos) + 1; if (heightBelow >= 16) return;`
	heightBelow := t.bambooHeightBelow(pos) + 1
	if heightBelow >= 16 {
		return
	}
	// `growBamboo(state, level, pos, random, heightBelow)` — pass the CURRENT state (the stalk's
	// own AGE/STAGE; growBamboo only reads AGE here, and uses it for thickBamboo).
	t.bambooGrowBamboo(state, pos, r.levelRandom, heightBelow)
}

// bambooHeightBelow is BambooStalkBlock.getHeightBelowUpToMax(level, pos): count consecutive
// BAMBOO cells directly below pos, capped at 16 iterations (`for (int i = 0; i < 16 &&
// level.getBlockState(pos.below(i+1)).is(BAMBOO); ++i); return i;`). Unreadable below-iter cells
// count as not-bamboo (the test reads as air; the vanilla getBlockState on an unloaded chunk
// returns air; same non-destructive default). CITE: BambooStalkBlock.getHeightBelowUpToMax.
func (t *TickLoop) bambooHeightBelow(pos pk.Position) int {
	for i := 0; i < 16; i++ {
		bp := pk.Position{X: pos.X, Y: pos.Y - (i + 1), Z: pos.Z}
		bs, ok := t.world().GetBlock(bp, dimMinY)
		if !ok || !block.IsBamboo(bs) {
			return i
		}
	}
	return 16
}

// bambooGrowBamboo is BambooStalkBlock.growBamboo(state, level, pos, random, heightBelow): the
// single setBlock that grows the stalk by one cell at pos.above(). The function is shared by
// BambooStalkBlock.randomTick (with the stalk's own state + the region's levelRandom) and the
// sapling path (with the sapling's defaultBlockState + the region's levelRandom + heightBelow =
// getHeightBelowUpToMax + 1 — the same one-cell grow, no inner loop).
//
// LEAVES-COMPUTATION (offset 25-155 of growBamboo):
//   - leaves starts as NONE.
//   - if heightBelow >= 1:
//       * if below1.is(BAMBOO) and below1.LEAVES == NONE: leaves = SMALL.
//       * else if below1.is(BAMBOO) and below1.LEAVES != NONE:
//           leaves = LARGE;
//           if below2.is(BAMBOO):
//             level.setBlock(pos.below(), below1.setValue(LEAVES, SMALL), 3);
//             level.setBlock(below2, below2.setValue(LEAVES, NONE), 3).
//       * else: leaves stays NONE.
//
// thickBamboo = (state.AGE == 1) || (below2State.is(BAMBOO)).
//
// newStage: if heightBelow >= 11:
//     if random.nextFloat() < 0.25f -> newStage = 1
//     else if heightBelow != 15      -> newStage = 0
//     else                           -> newStage = 1
//   else: newStage = 0.
//
// setBlock(pos.above(),
//   defaultBlockState().setValue(AGE, thickBamboo ? 1 : 0)
//                      .setValue(LEAVES, leaves)
//                      .setValue(STAGE, newStage), 3).
//
// CITE: BambooStalkBlock.growBamboo.
func (t *TickLoop) bambooGrowBamboo(state block.StateID, pos pk.Position, r *levelgen.LegacyRandomSource, heightBelow int) {
	// below1 = level.getBlockState(pos.below()); below2Pos = pos.below(2); below2 = getBlockState(below2Pos).
	below1Pos := below(pos)
	below1, ok1 := t.world().GetBlock(below1Pos, dimMinY)
	below2Pos := pk.Position{X: pos.X, Y: pos.Y - 2, Z: pos.Z}
	below2, ok2 := t.world().GetBlock(below2Pos, dimMinY)
	_ = ok2

	// leaves starts as NONE.
	var leaves block.BambooLeaves = block.BambooLeavesNone
	if heightBelow >= 1 {
		if ok1 && block.IsBamboo(below1) && block.BambooLeavesOf(below1) == block.BambooLeavesNone {
			leaves = block.BambooLeavesSmall
		} else if ok1 && block.IsBamboo(below1) && block.BambooLeavesOf(below1) != block.BambooLeavesNone {
			leaves = block.BambooLeavesLarge
			// If the cell TWO below is also bamboo, fix the leaves of pos.below() + below2:
			// pos.below().LEAVES := SMALL; below2.LEAVES := NONE.
			if ok2 && block.IsBamboo(below2) {
				if adj, ok := block.BambooState(int(block.BambooAge(below1)), block.BambooLeavesSmall, int(block.BambooStage(below1))); ok {
					if t.world().SetBlock(below1Pos, adj, dimMinY) {
						t.broadcastBlockUpdate(below1Pos, adj)
					}
				}
				if adj2, ok := block.BambooState(int(block.BambooAge(below2)), block.BambooLeavesNone, int(block.BambooStage(below2))); ok {
					if t.world().SetBlock(below2Pos, adj2, dimMinY) {
						t.broadcastBlockUpdate(below2Pos, adj2)
					}
				}
			}
		}
	}

	// thickBamboo = (state.AGE == 1) || (below2State.is(BAMBOO)). Both reads are non-fatal: a
	// non-bamboo below2 reads as -1 AGE/Stage (via bambooOf) and as not-Bamboo (via IsBamboo).
	thickBamboo := false
	if age := block.BambooAge(state); age == 1 {
		thickBamboo = true
	} else if ok2 && block.IsBamboo(below2) {
		thickBamboo = true
	}

	// newStage decision: gated nextFloat when heightBelow >= 11.
	newStage := 0
	if heightBelow >= 11 {
		if r.NextFloat() < 0.25 {
			newStage = 1
		} else if heightBelow != 15 {
			newStage = 0
		} else {
			newStage = 1
		}
	}

	// setBlock(pos.above(), defaultBlockState().setValue(AGE, thickBamboo ? 1 : 0)
	//                                           .setValue(LEAVES, leaves)
	//                                           .setValue(STAGE, newStage), 3).
	abovePos := above(pos)
	thickAge := 0
	if thickBamboo {
		thickAge = 1
	}
	if next, ok := block.BambooState(thickAge, leaves, newStage); ok {
		if t.world().SetBlock(abovePos, next, dimMinY) {
			t.broadcastBlockUpdate(abovePos, next)
		}
	}
}

// ---- ICE (IceBlock.randomTick / melt) ----

// iceRandomTick is IceBlock.randomTick: when BLOCK-light at pos exceeds 11 - state.lightDampening,
// melt to water (default state). r is unused (no RNG draw). CITE: IceBlock.randomTick +
// IceBlock.melt.
//
// `level.getBrightness(LightLayer.BLOCK, pos)` is the raw stored block-light at pos. Sulfur's
// ChunkManager.BlockBrightness (server/light.go's getBrightnessBlock seam) IS the BLOCK-light
// read; we use that here. The state.getLightDampening() value is block.LightBlock(state) —
// IceBlock's registered dampening (typically 3 for vanilla ice; an out-of-range state reads as 0,
// which makes the threshold 11 and keeps the melt conservative). CITE: IceBlock.randomTick
// (getBrightness(BLOCK, pos) > 11 - state.getLightDampening()).
//
// melt(state, level, pos) in vanilla is: read environmentAttribute WATER_EVAPORATES; if true,
// removeBlock(pos, false); else setBlockAndUpdate(pos, WATER{Level:0}). The env-attribute read is
// a DEFERRED (no env-attribute clock in v1); v1 mirrors the vanilla DEFAULT (WATER_EVAPORATES ==
// false -> always melt to water). The load-bearing behavior — block -> water — IS performed here.
// CITE: IceBlock.melt (`level.setBlockAndUpdate(pos, meltsInto())`).
func (t *TickLoop) iceRandomTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	_ = state
	// BLOCK-light brightness > 11 - state.getLightDampening(). Use the real per-section BLOCK-light
	// read (server/light.go's getBrightnessBlock seam); out-of-range / unloaded positions read 0
	// (no block light -> does not melt, the conservative default).
	brightness := t.getBrightnessBlock(pos)
	dampening := block.LightBlock(state)
	threshold := 11 - dampening
	if brightness <= threshold {
		return
	}
	// melt: setBlockAndUpdate(pos, WATER{Level:0}). Mirrored as SetBlock + broadcastBlockUpdate
	// (the same flag-10 shape other setBlockAndUpdate callers use).
	water := block.WaterState()
	if t.world().SetBlock(pos, water, dimMinY) {
		t.broadcastBlockUpdate(pos, water)
	}
}

// ---- SNOW LAYER (SnowLayerBlock.randomTick) ----

// snowLayerRandomTick is SnowLayerBlock.randomTick: when BLOCK-light at pos exceeds 11, melt to
// air (dropResources is DEFERRED; the load-bearing behavior — block -> air — is performed here).
// r is unused (no RNG draw). CITE: SnowLayerBlock.randomTick.
//
// `level.getBrightness(LightLayer.BLOCK, pos) > 11` is the literal jar gate. Sulfur's
// getBrightnessBlock seam provides the read (same as ice). CITE: SnowLayerBlock.randomTick.
//
// DEFERRAL: `Block.dropResources(state, level, pos)` rolls the snow-layer loot table (snowball
// drop) and is a cited follow-up (the loot-on-decay subsystem is not yet wired); the
// load-bearing behavior — block -> air — IS performed via removeBlock(pos, false). The same
// deferral pattern as LeavesBlock.randomTick (growth_block.go's leavesRandomTick). CITE:
// SnowLayerBlock.randomTick; Block.dropResources (snow LAYERS -> snowball).
func (t *TickLoop) snowLayerRandomTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	// `if (level.getBrightness(LightLayer.BLOCK, pos) > 11)` — the literal gate. No levelRandom
	// draw. CITE: SnowLayerBlock.randomTick.
	if t.getBrightnessBlock(pos) <= 11 {
		return
	}
	// dropResources(state, level, pos) — DEFERRED (loot subsystem). Mirrored as a no-op here; the
	// snowball drop is a cited follow-up.
	_ = state
	// removeBlock(pos, false): setBlock(pos, air) (flag 16 == UPDATE_NEIGHBORS only; no drop).
	air := t.airState()
	if t.world().SetBlock(pos, air, dimMinY) {
		t.broadcastBlockUpdate(pos, air)
	}
}