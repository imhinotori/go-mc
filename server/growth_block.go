package server

import (
	"encoding/json"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
)

// growth_block.go — SAPLING growth, LEAF decay, and GRASS/MYCELIUM spread random-tick handlers,
// ported 1:1 from the unobfuscated 26.2 jar. They are the third growth family (after sugar cane and
// crops) wired into the random-tick driver's dispatchRandomTick. Growth is driven by the world's
// random-tick pass (random_tick.go), NOT by a direct handler call — mirroring crop_block.go.
//
// CITE (temp/cache/26.2-inner.jar, `javap -c -p` this session):
//   SaplingBlock.randomTick(state, level, pos, random):
//       if (level.getMaxLocalRawBrightness(pos.above()) >= 9 && random.nextInt(7) == 0)
//           advanceTree(level, pos, state, random);
//   SaplingBlock.advanceTree(level, pos, state, random):
//       if (state.getValue(STAGE) == 0) level.setBlock(pos, state.cycle(STAGE), 260);
//       else treeGrower.growTree(level, level.getChunkSource().getGenerator(), pos, state, random);
//   LeavesBlock.randomTick(state, level, pos, random):
//       if (decaying(state)) { dropResources(state, level, pos); level.removeBlock(pos, false); }
//   LeavesBlock.decaying(state): !state.getValue(PERSISTENT) && state.getValue(DISTANCE) == 7
//   SpreadingSnowyBlock.randomTick(state, level, pos, random):
//       if base block absent -> return;
//       if (!canStayAlive(state, level, pos)) level.setBlockAndUpdate(pos, baseBlock.defaultState());
//       else if (level.getMaxLocalRawBrightness(pos.above()) >= 9) {
//           BlockState grassDefault = this.defaultBlockState();
//           for (int i = 0; i < 4; ++i) {
//               BlockPos p = pos.offset(random.nextInt(3)-1, random.nextInt(5)-3, random.nextInt(3)-1);
//               if (level.getBlockState(p).is(baseBlock) && canPropagate(grassDefault, level, p))
//                   level.setBlockAndUpdate(p, grassDefault.setValue(SNOWY, isSnowySetting(p.above())));
//           }
//       }

// growthRawBrightness is the getMaxLocalRawBrightness seam shared by SaplingBlock.randomTick (>=9
// gate on pos.above()) and SpreadingSnowyBlock.randomTick (>=9 gate on pos.above()). Sulfur has NO
// light engine yet (the same locked deferral cited for CropBlock.hasSufficientLight in crop_block.go,
// Spider.getLightLevelDependentMagicValue in ai_goals_attack.go, and the fire daylight gate), so this
// is a CITED CONSTANT equal to full brightness (15), which keeps the >=9 gates PASSING — structured
// to become a real level.getMaxLocalRawBrightness(pos.above()) read once a light engine exists, never
// baked away. Using 15 matches an unobstructed, fully-lit cell (the common case).
//
//	[DEFERRED: getMaxLocalRawBrightness — no light propagation in v1. CITE: SaplingBlock.randomTick /
//	 SpreadingSnowyBlock.randomTick (getMaxLocalRawBrightness(pos.above()) >= 9). Follow-up: real read.]
const growthRawBrightness = 15

// ---- SAPLING (SaplingBlock.randomTick / advanceTree) ----

// saplingRandomTick is SaplingBlock.randomTick: gated growth. r is the owning region; r.levelRandom
// is `this.random`. RNG DRAW ORDER (must match the jar exactly): the light gate (>=9, stubbed 15) is
// checked FIRST and short-circuits — the `random.nextInt(7)` is only drawn when the light gate passes
// (bytecode: `getMaxLocalRawBrightness >= 9 && random.nextInt(7) == 0`). advanceTree draws no further
// levelRandom (STAGE advance) or hands off to the tree grower. CITE: SaplingBlock.randomTick.
func (t *TickLoop) saplingRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	// `if (getMaxLocalRawBrightness(pos.above()) >= 9 && random.nextInt(7) == 0)` — the light gate
	// is the LEFT operand (short-circuit): with the cited-constant 15 it passes, then the nextInt(7)
	// is drawn. If a real light engine later makes the gate fail, the nextInt(7) must NOT be drawn.
	if growthRawBrightness < 9 {
		return
	}
	if r.levelRandom.NextIntN(7) != 0 {
		return
	}
	t.advanceTree(r, state, pos)
}

// advanceTree is SaplingBlock.advanceTree: STAGE 0 -> cycle to STAGE 1 (setBlock flag 260); STAGE 1 ->
// treeGrower.growTree (grow the tree, clearing the sapling). Flag 260 (0x104 ==
// UPDATE_CLIENTS|UPDATE_KNOWN_SHAPE, no neighbor notify) is mirrored with SetBlock + broadcast (the
// same flag-shape crop/sugar-cane use). CITE: SaplingBlock.advanceTree.
func (t *TickLoop) advanceTree(r *region, state block.StateID, pos pk.Position) {
	stage := block.SaplingStage(state)
	if stage < 0 {
		return // not a base sapling (defensive)
	}
	if stage == 0 {
		// STAGE 0 -> setBlock(state.cycle(STAGE), 260): advance to STAGE 1.
		if next, ok := block.SaplingCycleStage(state); ok {
			if t.world().SetBlock(pos, next, dimMinY) {
				t.broadcastBlockUpdate(pos, next)
			}
		}
		return
	}
	// STAGE 1 -> treeGrower.growTree(level, generator, pos, state, random). v1 wires the OAK grower
	// to the real "oak" configured_feature; other saplings are a cited grower-registry follow-up.
	t.growTree(r, state, pos)
}

// growTree is the SaplingBlock.advanceTree grow branch (STAGE 1) -> TreeGrower.growTree, scoped to
// the OAK grower in v1. It ports the single-sapling path of TreeGrower.growTree (oak has no mega
// 2x2 feature, so isTwoByTwoSapling is false and only the i=j=0 branch runs):
//
//	BlockState air = Blocks.AIR.defaultBlockState();
//	level.setBlock(pos, air, 260);                       // clear the sapling first
//	if (configuredFeature.place(level, generator, random, pos)) return true;  // grow succeeded
//	level.setBlock(pos, saplingState, 260);              // place failed -> restore the sapling
//
// The tree FEATURE placement is the ported feature.PlaceTree (world/levelgen/feature/tree.go — the
// StraightTrunkPlacer + BlobFoliagePlacer oak assembly), run over live-world set/read closures with
// the region levelRandom.
//
// DEFERRALS (cited):
//   - Non-oak growers: only OakTreeGrower is wired. The other saplings' STAGE advance is fully ported;
//     their grow step falls through here as a no-op (the sapling stays at STAGE 1) until their grower
//     registry entry (spruce/birch/… configured_features + the mega/fancy variant selection) is
//     ported. CITE: TreeGrower.OAK vs SPRUCE/BIRCH/… (getConfiguredFeature / getConfiguredMegaFeature).
//   - RNG parity vs the jar's tree placement: vanilla routes ConfiguredFeature.place through a
//     WorldGenLevel/WorldgenRandom seam (NOT the bare level random), so the exact draw sequence of the
//     grown tree differs from feeding r.levelRandom straight into PlaceTree. The tree GROWS faithfully
//     (same placers, same config); pinning the placement RNG to the vanilla WorldGenLevel seam is a
//     follow-up. This handler is NOT on the pig-oracle path (the driver never runs in that test), so
//     the byte-identical gate is unaffected. CITE: TreeGrower.growTree (ConfiguredFeature.place(
//     WorldGenLevel, ChunkGenerator, RandomSource, BlockPos)).
func (t *TickLoop) growTree(r *region, state block.StateID, pos pk.Position) {
	if !block.IsOakSapling(state) {
		return // only the oak grower is wired in v1 (cited follow-up for the rest)
	}
	cfg := oakTreeConfig()
	if cfg == nil {
		return // oak config unavailable (build-data issue) — leave the sapling untouched
	}
	// setBlock(pos, air, 260): clear the sapling BEFORE placing the feature (so the trunk base cell is
	// free). We snapshot the sapling state to restore it if placement fails.
	air := t.airState()
	if t.world().SetBlock(pos, air, dimMinY) {
		t.broadcastBlockUpdate(pos, air)
	}

	// The pure placers run against closures over the live world. set writes + broadcasts each cell;
	// read reads the live world (air outside a loaded column, exactly like the worldgen live body).
	set := func(x, y, z int, st block.StateID) {
		bp := pk.Position{X: x, Y: y, Z: z}
		if t.world().SetBlock(bp, st, dimMinY) {
			t.broadcastBlockUpdate(bp, st)
		}
	}
	read := func(x, y, z int) block.StateID {
		bp := pk.Position{X: x, Y: y, Z: z}
		if st, ok := t.world().GetBlock(bp, dimMinY); ok {
			return st
		}
		return air // outside a loaded column reads as air (the worldgen-view convention)
	}
	// Bind the rule_based below_trunk provider to the live read so its "not in
	// cannot_replace_below_tree_trunk" rule evaluates the existing block (mirrors feature_tree.go).
	bound := cfg.BelowTrunkWithExisting(read)

	origin := feature.TreePos{X: pos.X, Y: pos.Y, Z: pos.Z}
	// TreeFeature.place: draw the trunk height (getTreeHeight — the TWO nextInt draws), then PlaceTree
	// runs the exact jar doPlace order. r.levelRandom is the threaded RandomSource (see the RNG-parity
	// deferral above).
	treeHeight := bound.TrunkHeight(r.levelRandom)
	if feature.PlaceTree(set, read, r.levelRandom, bound, treeHeight, origin) {
		return // grow succeeded
	}
	// place failed -> restore the sapling (setBlock(pos, saplingState, 260)).
	if t.world().SetBlock(pos, state, dimMinY) {
		t.broadcastBlockUpdate(pos, state)
	}
}

// oakTreeConfigOnce/oakTreeConfigCache lazily parse the embedded "oak" configured_feature's tree
// config ONCE (it is immutable; the placers take it by pointer and never mutate it — PlaceTree copies
// it per call for the accum). A parse failure caches nil (the grow step then no-ops).
var (
	oakTreeConfigOnce  sync.Once
	oakTreeConfigCache *feature.TreeConfiguration
)

// oakTreeConfig returns the parsed oak TreeConfiguration (StraightTrunkPlacer base_height 4 +2, blob
// foliage radius 2 height 3, oak_log trunk, oak_leaves foliage, rule_based dirt below-trunk), loaded
// from the embedded configured_feature/oak.json. CITE: minecraft:oak configured_feature.
func oakTreeConfig() *feature.TreeConfiguration {
	oakTreeConfigOnce.Do(func() {
		raw, err := data.ConfiguredFeatureJSON("oak")
		if err != nil {
			return
		}
		// The file is { "type": "minecraft:tree", "config": { ... } }; ParseTreeConfiguration wants
		// the inner "config" object.
		var env struct {
			Config json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(raw, &env); err != nil || len(env.Config) == 0 {
			return
		}
		cfg, err := feature.ParseTreeConfiguration(env.Config)
		if err != nil {
			return
		}
		oakTreeConfigCache = cfg
	})
	return oakTreeConfigCache
}

// ---- LEAVES (LeavesBlock.randomTick / decaying) ----

// leavesRandomTick is LeavesBlock.randomTick: if the leaf is decaying (== !PERSISTENT && DISTANCE==7),
// drop its resources and remove it (-> air). It draws NO levelRandom (the decay is deterministic off
// the state's DISTANCE/PERSISTENT), so it leaves the levelRandom stream untouched. r is unused (no
// RNG). CITE: LeavesBlock.randomTick / decaying.
//
// DEFERRAL (cited): dropResources(state, level, pos) rolls the leaves loot table (sapling/stick/apple
// with the fortune/shears context) and spawns the item entities. Sulfur's loot-on-decay path is a
// follow-up; the load-bearing behavior is the block -> air removal (the leaf disappears), which IS
// performed here. CITE: LeavesBlock.randomTick (Block.dropResources); Block.getDrops (leaves loot).
func (t *TickLoop) leavesRandomTick(r *region, state block.StateID, pos pk.Position) {
	_ = r
	if t.world() == nil {
		return
	}
	// `if (decaying(state))` — only a non-persistent leaf at DISTANCE 7 decays. IsRandomlyTicking
	// already gates this (only decaying leaves are sampled), but mirror the guard for fidelity.
	if !block.LeavesDecaying(state) {
		return
	}
	// dropResources(...) — DEFERRED (see the deferral note). removeBlock(pos, false) -> set air.
	air := t.airState()
	if t.world().SetBlock(pos, air, dimMinY) {
		t.broadcastBlockUpdate(pos, air)
	}
}

// leavesUpdateDistance is LeavesBlock.updateDistance(state, level, pos): recompute DISTANCE as the
// min over the 6 orthogonal neighbours of getDistanceAt(neighbour)+1, starting at 7, short-circuiting
// as soon as it reaches 1 (a neighbour that is a log gives 0 -> 1, the minimum possible). This is the
// SCHEDULED-tick distance maintenance (LeavesBlock.tick calls it then setBlock flag 3). Sulfur does
// not yet schedule leaf ticks on block updates, so this is exposed for the decay handler + tests to
// compute a correct DISTANCE from the live world (a leaf whose nearest log was removed recomputes to
// 7 and then decays). Returns the new leaves state id (ok=false if not leaves). An unreadable
// neighbour reads as air -> contributes getDistanceAt(air)==7 -> +1 clamps to 7 (no spurious anchor).
// CITE: LeavesBlock.updateDistance / getDistanceAt.
func (t *TickLoop) leavesUpdateDistance(state block.StateID, pos pk.Position) (block.StateID, bool) {
	if t.world() == nil || !block.IsLeaves(state) {
		return 0, false
	}
	dist := 7 // int i = 7;
	// Direction.values(): the 6 orthogonal neighbours. The order does not affect the min result.
	for _, d := range sixDirections {
		nb := pk.Position{X: pos.X + d.dx, Y: pos.Y + d.dy, Z: pos.Z + d.dz}
		ns, ok := t.world().GetBlock(nb, dimMinY)
		var contrib int
		if !ok {
			contrib = 7 // unreadable -> getDistanceAt(air)==7 (Optional.empty -> orElse 7)
		} else {
			contrib = block.LeafDistanceAt(ns)
		}
		if c := contrib + 1; c < dist {
			dist = c // i = Math.min(i, getDistanceAt(neighbor) + 1)
		}
		if dist == 1 {
			break // `if (i == 1) break;` — 1 is the minimum (a log neighbour); no need to scan further
		}
	}
	return block.LeavesWithDistance(state, dist)
}

// sixDirection is one of the 6 orthogonal Direction.values() offsets used by updateDistance's
// neighbour scan. CITE: Direction.values() (DOWN, UP, NORTH, SOUTH, WEST, EAST).
type sixDirection struct{ dx, dy, dz int }

var sixDirections = []sixDirection{
	{dx: 0, dy: -1, dz: 0}, // DOWN
	{dx: 0, dy: 1, dz: 0},  // UP
	{dx: 0, dy: 0, dz: -1}, // NORTH (-z)
	{dx: 0, dy: 0, dz: 1},  // SOUTH (+z)
	{dx: -1, dy: 0, dz: 0}, // WEST (-x)
	{dx: 1, dy: 0, dz: 0},  // EAST (+x)
}

// ---- GRASS / MYCELIUM (SpreadingSnowyBlock.randomTick) ----

// grassRandomTick is SpreadingSnowyBlock.randomTick (GrassBlock + MyceliumBlock): die-to-dirt when
// the block can no longer stay alive, else spread to up to 4 random adjacent dirt cells when the light
// gate passes. r is the owning region; r.levelRandom is `this.random`.
//
// RNG DRAW ORDER (must match the jar exactly):
//   - The die-to-dirt branch (!canStayAlive) draws NO levelRandom.
//   - The spread branch runs a 4-iteration loop; EACH iteration draws THREE nextInts, in this order,
//     as the args to pos.offset(int, int, int) (Java evaluates arguments left-to-right):
//         dx = random.nextInt(3) - 1
//         dy = random.nextInt(5) - 3
//         dz = random.nextInt(3) - 1
//     ALL 4 iterations draw their 3 ints UNCONDITIONALLY (the loop body always samples the position
//     first, then tests it) — so the spread branch draws exactly 12 levelRandom ints, regardless of
//     how many cells actually convert.
// CITE: SpreadingSnowyBlock.randomTick.
func (t *TickLoop) grassRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	// `if (!canStayAlive(state, level, pos)) setBlockAndUpdate(pos, baseBlock.defaultState());`
	if !t.grassCanStayAlive(pos) {
		if dirt, ok := block.SpreadingBaseBlock(state); ok {
			if t.world().SetBlock(pos, dirt, dimMinY) {
				t.broadcastBlockUpdate(pos, dirt)
			}
		}
		return
	}
	// `else if (getMaxLocalRawBrightness(pos.above()) >= 9)` — cited-constant light (15) so this passes.
	if growthRawBrightness < 9 {
		return
	}
	grassDefault, ok := block.SpreadingDefault(state)
	if !ok {
		return // not grass/mycelium (defensive)
	}
	// for (int i = 0; i < 4; ++i) — 4 spread attempts.
	for i := 0; i < 4; i++ {
		// blockpos1 = pos.offset(nextInt(3)-1, nextInt(5)-3, nextInt(3)-1). The three draws happen in
		// this exact left-to-right order EVERY iteration (the pig-oracle-safe determinism contract).
		dx := int(r.levelRandom.NextIntN(3)) - 1
		dy := int(r.levelRandom.NextIntN(5)) - 3
		dz := int(r.levelRandom.NextIntN(3)) - 1
		p := pk.Position{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
		// `if (level.getBlockState(p).is(baseBlock) && canPropagate(grassDefault, level, p))`
		ps, ok := t.world().GetBlock(p, dimMinY)
		if !ok || !block.IsDirt(ps) {
			continue // target not dirt (or unreadable) -> no spread there
		}
		if !t.grassCanPropagate(grassDefault, p) {
			continue
		}
		// setBlockAndUpdate(p, grassDefault.setValue(SNOWY, isSnowySetting(p.above()))).
		snowy := false
		if as, ok := t.world().GetBlock(above(p), dimMinY); ok {
			snowy = block.IsSnowySetting(as)
		}
		if spread, ok := block.SpreadingWithSnowy(state, snowy); ok {
			if t.world().SetBlock(p, spread, dimMinY) {
				t.broadcastBlockUpdate(p, spread)
			}
		}
	}
}

// grassCanStayAlive is SpreadingSnowyBlock.canStayAlive(state, level, pos):
//   - above == SNOW layer with LAYERS==1 -> true (a thin snow layer does not kill grass);
//   - fluidState(above).isFull() -> false (a full fluid over the cell kills it);
//   - else LightEngine.getLightDampeningInto(above, UP) < 15 -> true (light reaches the top face).
//
// The numeric light-dampening read is the one part with no engine yet. Vanilla returns 15 (fully
// dampened -> grass dies) ONLY when the block above is an OPAQUE, light-occluding full cube (dirt,
// stone, planks, …); a transparent or non-full block above dampens < 15 -> grass survives. The
// server-authoritative proxy for "opaque full cube that blocks skylight" is IsSuffocating
// (blocksMotion && isCollisionShapeFullBlock) — true for dirt/stone/full cubes, false for
// leaves/glass/air/slabs/snow-layer/water. So: dampens-fully == above IsSuffocating. This keeps the
// REAL behavior the mandate calls out (a solid opaque block placed over grass -> grass dies) while the
// numeric light VALUE stays stubbed. An unreadable above reads as air -> not full, not opaque -> alive.
//
//	[DEFERRED: getLightDampeningInto numeric value — no light engine in v1. The opaque-block-above
//	 kill IS real (via IsSuffocating). CITE: SpreadingSnowyBlock.canStayAlive; LightEngine.
//	 getLightDampeningInto. Follow-up: swap the IsSuffocating proxy for the real occlusion/light read.]
func (t *TickLoop) grassCanStayAlive(pos pk.Position) bool {
	abovePos := above(pos)
	as, ok := t.world().GetBlock(abovePos, dimMinY)
	if !ok {
		return true // above unreadable -> air -> light reaches -> alive
	}
	// above.is(Blocks.SNOW) && LAYERS==1 -> alive.
	if block.IsSnowLayerOne(as) {
		return true
	}
	// getFluidState(above).isFull() -> dead.
	if block.FluidIsFull(as) {
		return false
	}
	// getLightDampeningInto(above, UP) < 15 -> alive. Proxy: an opaque full cube dampens fully (== 15,
	// so >= 15 -> dead); anything else dampens < 15 -> alive.
	return !block.IsSuffocating(as)
}

// grassCanPropagate is SpreadingSnowyBlock.canPropagate(grassDefault, level, pos):
// canStayAlive(grassDefault, level, pos) && !getFluidState(pos.above()).is(FluidTags.WATER). I.e. the
// candidate dirt cell must (a) be able to keep grass alive (its own light/above check) and (b) NOT
// have water directly above it. CITE: SpreadingSnowyBlock.canPropagate.
func (t *TickLoop) grassCanPropagate(_ block.StateID, pos pk.Position) bool {
	if !t.grassCanStayAlive(pos) {
		return false
	}
	// !getFluidState(pos.above()).is(FluidTags.WATER) — any water (any level) above blocks propagation.
	if as, ok := t.world().GetBlock(above(pos), dimMinY); ok {
		if block.IsWaterFluid(as) {
			return false
		}
	}
	return true
}
