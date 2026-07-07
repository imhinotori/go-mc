package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// fire_block.go — the BLOCK FIRE subsystem: the 1:1 port of net.minecraft.world.level.block
// .FireBlock (the SCHEDULED-tick spread / burn-out / age logic), distinct from server/fire.go
// (which is ENTITY fire — igniteForSeconds / on-fire damage). FireBlock is a SCHEDULED ticker:
// onPlace / tick reschedule the block via scheduleTick(pos, this, getFireTickDelay), so it wires
// into the scheduled-block-tick LevelTicks in block_ticks.go (NOT the random-tick driver).
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p this session): FireBlock.tick / checkBurnOut /
// getIgniteOdds / getBurnOdds / getStateForPlacement / isValidFireLocation / canSurvive /
// isNearRain / getFireTickDelay. The full decompiled pseudocode is inline at each ported method.

// fireTickDelayBase / fireTickDelayJitter are getFireTickDelay 30 + random.nextInt(10). CITE:
// FireBlock.getFireTickDelay (bipush 30 + nextInt(10)).
const (
	fireTickDelayBase   = 30
	fireTickDelayJitter = 10
)

// getFireTickDelay is FireBlock.getFireTickDelay(RandomSource): 30 + random.nextInt(10). One draw.
// CITE: FireBlock.getFireTickDelay.
func (t *TickLoop) getFireTickDelay(r *region) int {
	return fireTickDelayBase + int(r.levelRandom.NextIntN(fireTickDelayJitter))
}

// fireDifficultyID is Difficulty.getId() for the server difficulty (NORMAL == 2). serverDifficulty
// is the cited NORMAL stub (food.go); NORMAL id is 2 (PEACEFUL=0, EASY=1, NORMAL=2, HARD=3). CITE:
// FireBlock.tick (getDifficulty().getId()); Difficulty.NORMAL.getId() == 2.
const fireDifficultyID = int(serverDifficulty)

// fireNeighborOffsets is Direction.values() as (dx,dy,dz): DOWN, UP, NORTH, SOUTH, WEST, EAST — the
// scan order FireBlock.getIgniteOdds/isValidFireLocation use. CITE: net.minecraft.core.Direction.values().
var fireNeighborOffsets = []struct{ dx, dy, dz int }{
	{0, -1, 0}, // DOWN
	{0, 1, 0},  // UP
	{0, 0, -1}, // NORTH
	{0, 0, 1},  // SOUTH
	{-1, 0, 0}, // WEST
	{1, 0, 0},  // EAST
}

// east/west/north/south are the horizontal BlockPos offsets (below/above already exist in fluid.go).
func east(p pk.Position) pk.Position  { return pk.Position{X: p.X + 1, Y: p.Y, Z: p.Z} }
func west(p pk.Position) pk.Position  { return pk.Position{X: p.X - 1, Y: p.Y, Z: p.Z} }
func north(p pk.Position) pk.Position { return pk.Position{X: p.X, Y: p.Y, Z: p.Z - 1} }
func south(p pk.Position) pk.Position { return pk.Position{X: p.X, Y: p.Y, Z: p.Z + 1} }

// fireTick is FireBlock.tick(state, level, pos, random) — the scheduled fire tick: reschedule,
// age-up, burn adjacent flammables out, and try to spread to nearby flammable cells. state is the
// current fire state at pos (IsFire, per the tickBlock stale guard); r is the region whose
// levelRandom is this.random. Every RNG draw is on r.levelRandom in the EXACT vanilla order.
//
// CITE: FireBlock.tick pseudocode —
//   scheduleTick(pos, this, getFireTickDelay(getRandom()));        // 30 + nextInt(10)
//   if (!canSpreadFireAround(pos)) return;                          // gamerule; v1 always true
//   if (!state.canSurvive(level, pos)) removeBlock(pos, false);     // NO return
//   below = getBlockState(pos.below()); infiniburn = below.is(infiniburn());
//   age = state.getValue(AGE);
//   if (!infiniburn && isRaining() && isNearRain(pos) && nextFloat() < 0.2 + age*0.03) { removeBlock; return; }
//   newAge = min(15, age + nextInt(3)/2); if (age != newAge) setBlock(setValue(AGE, newAge), 260);
//   if (!infiniburn) {
//     if (!isValidFireLocation(pos)) { if (!below.isFaceSturdy(UP) || age>3) removeBlock; return; }
//     if (age==15 && nextInt(4)==0 && !canBurn(below)) { removeBlock; return; }
//   }
//   boost = increasedFireBurnout ? -50 : 0;
//   checkBurnOut(east,300+b); checkBurnOut(west,300+b); checkBurnOut(below,250+b);
//   checkBurnOut(above,250+b); checkBurnOut(north,300+b); checkBurnOut(south,300+b);
//   for i in -1..1 for k in -1..1 for j in -1..4 { if i==0&&j==0&&k==0 continue;
//     l = 100; if (j>1) l += (j-1)*100; m = pos+(i,j,k); ig = getIgniteOdds(level,m); if (ig<=0) continue;
//     chance = (ig + 40 + difficulty.getId()*7) / (age + 30); if (increased) chance/=2;
//     if (chance>0 && nextInt(l) <= chance && (!isRaining || !isNearRain(m))) {
//       n = min(15, age + nextInt(5)/4); setBlock(m, getStateWithAge(level, m, n), 3); } }
func (t *TickLoop) fireTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}

	// scheduleTick(pos, this, getFireTickDelay(getRandom())) — ALWAYS first (one level-random int),
	// so fire keeps ticking even when the rest of the method returns early.
	t.scheduleBlockTick(pos, fireTickType, t.getFireTickDelay(r))

	// canSpreadFireAround(pos): gamerule fire_spread_radius_around_player default -1 => always true in
	// v1 (no player-proximity restriction). CITE: ServerLevel.canSpreadFireAround (default -1 => true).

	// if (!state.canSurvive(level, pos)) removeBlock(pos, false); — NOTE: no return in vanilla; the
	// method continues, using the age captured below from the ORIGINAL local state (the bytecode reads
	// AGE from the local, never re-reading after removeBlock). CITE: FireBlock.tick / canSurvive.
	if !t.fireCanSurvive(pos) {
		t.fireRemoveBlock(pos)
	}

	belowPos := below(pos)
	belowState, _ := t.world().GetBlock(belowPos, dimMinY) // air on unreadable (== not infiniburn)
	infiniburn := block.IsInfiniburnOverworld(belowState)

	age := block.FireAge(state)
	if age < 0 {
		return // not fire (defensive; the tickBlock guard should prevent this)
	}

	// Rain burn-out: !infiniburn && isRaining && isNearRain && nextFloat() < 0.2 + age*0.03 -> remove.
	// nextFloat() drawn ONLY when the first gates pass (Java && short-circuit). CITE: FireBlock.tick.
	if !infiniburn && t.isRaining() && t.fireIsNearRain(pos) {
		if r.levelRandom.NextFloat() < 0.2+float32(age)*0.03 {
			t.fireRemoveBlock(pos)
			return
		}
	}

	// newAge = min(15, age + nextInt(3)/2); if (age != newAge) setBlock(setValue(AGE, newAge), 260).
	newAge := age + int(r.levelRandom.NextIntN(3))/2
	if newAge > block.FireMaxAge {
		newAge = block.FireMaxAge
	}
	if age != newAge {
		if aged, ok := block.FireWithAge(state, newAge); ok {
			if t.world().SetBlock(pos, aged, dimMinY) {
				t.broadcastBlockUpdate(pos, aged)
			}
			state = aged
		}
	}

	if !infiniburn {
		// if (!isValidFireLocation(pos)) { below-check; return; }
		if !t.fireIsValidFireLocation(pos) {
			belowSt, _ := t.world().GetBlock(belowPos, dimMinY)
			// if (!below.isFaceSturdy(UP) || age > 3) removeBlock. CITE: FireBlock.tick.
			if !block.IsFaceSturdy(belowSt, block.Up, block.SupportFull) || age > 3 {
				t.fireRemoveBlock(pos)
			}
			return
		}
		// if (age == 15 && nextInt(4) == 0 && !canBurn(below)) { removeBlock; return; }
		if age == block.FireMaxAge {
			if r.levelRandom.NextIntN(4) == 0 {
				belowSt, _ := t.world().GetBlock(belowPos, dimMinY)
				if t.fireIgniteOdds(belowSt) <= 0 { // !canBurn(below)
					t.fireRemoveBlock(pos)
					return
				}
			}
		}
	}

	// increasedFireBurnout: v1 default false (no EnvironmentAttributeSystem wired; the vanilla default
	// for INCREASED_FIRE_BURNOUT is false). CITED STUB — becomes a real environmentAttributes().getValue
	// read later; boost stays 0 (300/250 chances). CITE: FireBlock.tick.
	const increasedFireBurnout = false
	boost := 0
	if increasedFireBurnout {
		boost = -50
	}

	// checkBurnOut over the 6 faces in EXACT vanilla order: east, west, below, above, north, south
	// (chances 300/300/250/250/300/300 + boost). CITE: FireBlock.tick.
	t.fireCheckBurnOut(r, east(pos), 300+boost, age)
	t.fireCheckBurnOut(r, west(pos), 300+boost, age)
	t.fireCheckBurnOut(r, belowPos, 250+boost, age)
	t.fireCheckBurnOut(r, above(pos), 250+boost, age)
	t.fireCheckBurnOut(r, north(pos), 300+boost, age)
	t.fireCheckBurnOut(r, south(pos), 300+boost, age)

	// Spread loop: for i in -1..1, k in -1..1, j in -1..4 (x=i, y=j, z=k). CITE: FireBlock.tick.
	for i := -1; i <= 1; i++ {
		for k := -1; k <= 1; k++ {
			for j := -1; j <= 4; j++ {
				if i == 0 && j == 0 && k == 0 {
					continue
				}
				l := 100
				if j > 1 {
					l += (j - 1) * 100
				}
				m := pk.Position{X: pos.X + i, Y: pos.Y + j, Z: pos.Z + k}
				igniteOdds := t.fireIgniteOddsAt(m)
				if igniteOdds <= 0 {
					continue
				}
				chance := (igniteOdds + 40 + fireDifficultyID*7) / (age + 30)
				if increasedFireBurnout {
					chance /= 2
				}
				// nextInt(l) is INSIDE the chance>0 gate (bytecode branches on chance<=0 BEFORE nextInt).
				if chance <= 0 {
					continue
				}
				if int(r.levelRandom.NextIntN(int32(l))) > chance {
					continue
				}
				if t.isRaining() && t.fireIsNearRain(m) {
					continue
				}
				n := age + int(r.levelRandom.NextIntN(5))/4
				if n > block.FireMaxAge {
					n = block.FireMaxAge
				}
				if newState, ok := t.fireStateWithAge(m, n); ok {
					if t.world().SetBlock(m, newState, dimMinY) {
						t.broadcastBlockUpdate(m, newState)
					}
				}
			}
		}
	}
}

// fireCheckBurnOut is FireBlock.checkBurnOut(level, pos, chance, random, age): with probability
// burnOdds/chance, either re-ignite the burnt cell (bumping its fire age) when not raining, or
// remove it; a TNT that burns is primed. RNG order: nextInt(chance) FIRST (the gate); then only if
// it fires nextInt(age+10), and (if <5 and not raining) nextInt(5) for the new age. CITE:
// FireBlock.checkBurnOut:
//   burnOdds = getBurnOdds(getBlockState(pos));
//   if (nextInt(chance) < burnOdds) {
//     old = getBlockState(pos);
//     if (nextInt(age+10) < 5 && !isRainingAt(pos)) { m = min(age + nextInt(5)/4, 15);
//       setBlock(pos, getStateWithAge(level, pos, m), 3); } else { removeBlock(pos, false); }
//     if (old instanceof TntBlock) TntBlock.prime(level, pos);
//   }
func (t *TickLoop) fireCheckBurnOut(r *region, pos pk.Position, chance, age int) {
	if chance <= 0 {
		return // nextInt(chance) invalid; a non-positive chance never burns (defensive).
	}
	st, _ := t.world().GetBlock(pos, dimMinY)
	burnOdds := t.fireBurnOdds(st)
	if int(r.levelRandom.NextIntN(int32(chance))) >= burnOdds {
		return
	}
	old, _ := t.world().GetBlock(pos, dimMinY) // old = getBlockState(pos) (vanilla re-reads)
	if int(r.levelRandom.NextIntN(int32(age+10))) < 5 && !t.isRainingAt(pos) {
		m := age + int(r.levelRandom.NextIntN(5))/4
		if m > block.FireMaxAge {
			m = block.FireMaxAge
		}
		if newState, ok := t.fireStateWithAge(pos, m); ok {
			if t.world().SetBlock(pos, newState, dimMinY) {
				t.broadcastBlockUpdate(pos, newState)
			}
		}
	} else {
		t.fireRemoveBlock(pos)
	}
	// if (old instanceof TntBlock) TntBlock.prime(level, pos). primeTntBlock spawns the PrimedTnt; the
	// source TNT was replaced by fire/air above, matching vanilla prime-then-block-gone. CITE:
	// FireBlock.checkBurnOut (TntBlock.prime).
	if block.IsTntBlock(old) {
		t.primeTntBlock(pos)
	}
}

// fireIgniteOdds is FireBlock.getIgniteOdds(BlockState): WATERLOGGED -> 0, else the flammability
// table ignite odds (0 if absent). CITE: FireBlock.getIgniteOdds(state).
func (t *TickLoop) fireIgniteOdds(s block.StateID) int {
	if block.IsWaterloggedState(s) {
		return 0
	}
	return fireIgniteTable[fireBlockKind(s)]
}

// fireBurnOdds is FireBlock.getBurnOdds(BlockState): WATERLOGGED -> 0, else the flammability table
// burn odds (0 if absent). CITE: FireBlock.getBurnOdds(state).
func (t *TickLoop) fireBurnOdds(s block.StateID) int {
	if block.IsWaterloggedState(s) {
		return 0
	}
	return fireBurnTable[fireBlockKind(s)]
}

// fireIgniteOddsAt is FireBlock.getIgniteOdds(LevelReader, BlockPos): 0 unless the cell is empty
// (air); otherwise the MAX over the 6 Direction neighbours of getIgniteOdds(neighbour state). CITE:
// FireBlock.getIgniteOdds(LevelReader, BlockPos).
func (t *TickLoop) fireIgniteOddsAt(pos pk.Position) int {
	st, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsAir(st) { // isEmptyBlock: only air is empty
		return 0
	}
	maxOdds := 0
	for _, d := range fireNeighborOffsets {
		nb := pk.Position{X: pos.X + d.dx, Y: pos.Y + d.dy, Z: pos.Z + d.dz}
		nst, _ := t.world().GetBlock(nb, dimMinY) // unreadable -> air -> odds 0
		if o := t.fireIgniteOdds(nst); o > maxOdds {
			maxOdds = o
		}
	}
	return maxOdds
}

// fireCanSurvive is FireBlock.canSurvive(state, level, pos): the block below has a sturdy up-face,
// OR some neighbour is burnable (isValidFireLocation). CITE: FireBlock.canSurvive.
func (t *TickLoop) fireCanSurvive(pos pk.Position) bool {
	belowSt, _ := t.world().GetBlock(below(pos), dimMinY)
	if block.IsFaceSturdy(belowSt, block.Up, block.SupportFull) {
		return true
	}
	return t.fireIsValidFireLocation(pos)
}

// fireIsValidFireLocation is FireBlock.isValidFireLocation(level, pos): any of the 6 Direction
// neighbours canBurn (getIgniteOdds(state) > 0). CITE: FireBlock.isValidFireLocation.
func (t *TickLoop) fireIsValidFireLocation(pos pk.Position) bool {
	for _, d := range fireNeighborOffsets {
		nb := pk.Position{X: pos.X + d.dx, Y: pos.Y + d.dy, Z: pos.Z + d.dz}
		nst, _ := t.world().GetBlock(nb, dimMinY)
		if t.fireIgniteOdds(nst) > 0 { // canBurn
			return true
		}
	}
	return false
}

// fireStateWithAge is FireBlock.getStateWithAge(level, pos, age) == BaseFireBlock.getState(level,
// pos) then, if fire, setValue(AGE, age). BaseFireBlock.getState picks SOUL_FIRE (over soul
// soil/sand) else FIRE via getStateForPlacement. v1 spreads only overworld FIRE (soul fire is a
// nether mechanic; SoulFireBlock.canSurviveOnBlock cells are absent overworld). CITE:
// FireBlock.getStateWithAge / BaseFireBlock.getState.
func (t *TickLoop) fireStateWithAge(pos pk.Position, age int) (block.StateID, bool) {
	sid, ok := t.fireStateForPlacement(pos)
	if !ok {
		return 0, false
	}
	return block.FireWithAge(sid, age)
}

// fireStateForPlacement is FireBlock.getStateForPlacement(BlockGetter, BlockPos): the default fire
// state with up/horizontal attach flags set from canBurn of each neighbour, UNLESS the block below
// is neither burnable nor up-sturdy — then the plain default state. CITE:
// FireBlock.getStateForPlacement.
func (t *TickLoop) fireStateForPlacement(pos pk.Position) (block.StateID, bool) {
	belowSt, _ := t.world().GetBlock(below(pos), dimMinY)
	// if (!canBurn(below) && !below.isFaceSturdy(UP)) return defaultBlockState();
	if t.fireIgniteOdds(belowSt) <= 0 && !block.IsFaceSturdy(belowSt, block.Up, block.SupportFull) {
		return block.FireDefaultState()
	}
	up, _ := t.world().GetBlock(above(pos), dimMinY)
	nSt, _ := t.world().GetBlock(north(pos), dimMinY)
	sSt, _ := t.world().GetBlock(south(pos), dimMinY)
	wSt, _ := t.world().GetBlock(west(pos), dimMinY)
	eSt, _ := t.world().GetBlock(east(pos), dimMinY)
	return block.FireStateForPlacement(up, nSt, sSt, wSt, eSt, t.fireIgniteOdds)
}

// fireIsNearRain is FireBlock.isNearRain(level, pos): the cell OR any of its four horizontal
// neighbours isRainingAt. CITE: FireBlock.isNearRain (pos | west | east | north | south).
func (t *TickLoop) fireIsNearRain(pos pk.Position) bool {
	return t.isRainingAt(pos) ||
		t.isRainingAt(west(pos)) ||
		t.isRainingAt(east(pos)) ||
		t.isRainingAt(north(pos)) ||
		t.isRainingAt(south(pos))
}

// fireRemoveBlock is level.removeBlock(pos, false): set the cell to air, broadcast, and re-run the
// edit-time neighbor reconciliation (updateNeighborsAt). Mirrors the removeBlock seam used by
// sugar_cane.go / redstone.go. CITE: Level.removeBlock(pos, false).
func (t *TickLoop) fireRemoveBlock(pos pk.Position) {
	if !t.world().SetBlock(pos, t.airState(), dimMinY) {
		return
	}
	t.broadcastBlockUpdate(pos, t.airState())
	t.onBlockTickEdit(pos)
}

// fireBlockKind returns the block id string of a state (the key into the flammability tables). It is
// the analogue of BlockState.getBlock() used as the key of FireBlock's Object2IntMap<Block> ignite/
// burn maps. Out-of-range ids return "" (absent -> odds 0). CITE: FireBlock.getIgniteOdds/getBurnOdds
// (this.igniteOdds.getInt(state.getBlock())).
func fireBlockKind(s block.StateID) string {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return ""
	}
	return block.StateList[s].ID()
}

// fireIgniteTable / fireBurnTable are the ported FireBlock flammability tables — the igniteOdds and
// burnOdds Object2IntMap<Block> populated by FireBlock.bootStrap via setFlammable(block, ignite,
// burn). A block absent from the map has odds 0 (Object2IntMap default). Keyed by block id string
// (fireBlockKind). Every entry is transcribed 1:1 from the bootStrap bytecode this session; the two
// ColorCollection.forEach calls (WOOL -> 60/20 via lambda$bootStrap$1, CARPET -> 30/60 via
// lambda$bootStrap$0) are expanded to all 16 dyed colors below.
//
// CITE: FireBlock.bootStrap / setFlammable (temp/cache/26.2-inner.jar, javap -c -p this session).

// fireIgniteTable maps a block id to its setFlammable ignite odds. CITE: FireBlock.bootStrap.
var fireIgniteTable = map[string]int{
	"minecraft:oak_planks": 5,
	"minecraft:spruce_planks": 5,
	"minecraft:birch_planks": 5,
	"minecraft:jungle_planks": 5,
	"minecraft:acacia_planks": 5,
	"minecraft:cherry_planks": 5,
	"minecraft:dark_oak_planks": 5,
	"minecraft:pale_oak_planks": 5,
	"minecraft:mangrove_planks": 5,
	"minecraft:bamboo_planks": 5,
	"minecraft:bamboo_mosaic": 5,
	"minecraft:oak_slab": 5,
	"minecraft:spruce_slab": 5,
	"minecraft:birch_slab": 5,
	"minecraft:jungle_slab": 5,
	"minecraft:acacia_slab": 5,
	"minecraft:cherry_slab": 5,
	"minecraft:dark_oak_slab": 5,
	"minecraft:pale_oak_slab": 5,
	"minecraft:mangrove_slab": 5,
	"minecraft:bamboo_slab": 5,
	"minecraft:bamboo_mosaic_slab": 5,
	"minecraft:oak_fence_gate": 5,
	"minecraft:spruce_fence_gate": 5,
	"minecraft:birch_fence_gate": 5,
	"minecraft:jungle_fence_gate": 5,
	"minecraft:acacia_fence_gate": 5,
	"minecraft:cherry_fence_gate": 5,
	"minecraft:dark_oak_fence_gate": 5,
	"minecraft:pale_oak_fence_gate": 5,
	"minecraft:mangrove_fence_gate": 5,
	"minecraft:bamboo_fence_gate": 5,
	"minecraft:oak_fence": 5,
	"minecraft:spruce_fence": 5,
	"minecraft:birch_fence": 5,
	"minecraft:jungle_fence": 5,
	"minecraft:acacia_fence": 5,
	"minecraft:cherry_fence": 5,
	"minecraft:dark_oak_fence": 5,
	"minecraft:pale_oak_fence": 5,
	"minecraft:mangrove_fence": 5,
	"minecraft:bamboo_fence": 5,
	"minecraft:oak_stairs": 5,
	"minecraft:birch_stairs": 5,
	"minecraft:spruce_stairs": 5,
	"minecraft:jungle_stairs": 5,
	"minecraft:acacia_stairs": 5,
	"minecraft:cherry_stairs": 5,
	"minecraft:dark_oak_stairs": 5,
	"minecraft:pale_oak_stairs": 5,
	"minecraft:mangrove_stairs": 5,
	"minecraft:bamboo_stairs": 5,
	"minecraft:bamboo_mosaic_stairs": 5,
	"minecraft:oak_log": 5,
	"minecraft:spruce_log": 5,
	"minecraft:birch_log": 5,
	"minecraft:jungle_log": 5,
	"minecraft:acacia_log": 5,
	"minecraft:cherry_log": 5,
	"minecraft:pale_oak_log": 5,
	"minecraft:dark_oak_log": 5,
	"minecraft:mangrove_log": 5,
	"minecraft:bamboo_block": 5,
	"minecraft:stripped_oak_log": 5,
	"minecraft:stripped_spruce_log": 5,
	"minecraft:stripped_birch_log": 5,
	"minecraft:stripped_jungle_log": 5,
	"minecraft:stripped_acacia_log": 5,
	"minecraft:stripped_cherry_log": 5,
	"minecraft:stripped_dark_oak_log": 5,
	"minecraft:stripped_pale_oak_log": 5,
	"minecraft:stripped_mangrove_log": 5,
	"minecraft:stripped_bamboo_block": 5,
	"minecraft:stripped_oak_wood": 5,
	"minecraft:stripped_spruce_wood": 5,
	"minecraft:stripped_birch_wood": 5,
	"minecraft:stripped_jungle_wood": 5,
	"minecraft:stripped_acacia_wood": 5,
	"minecraft:stripped_cherry_wood": 5,
	"minecraft:stripped_dark_oak_wood": 5,
	"minecraft:stripped_pale_oak_wood": 5,
	"minecraft:stripped_mangrove_wood": 5,
	"minecraft:oak_wood": 5,
	"minecraft:spruce_wood": 5,
	"minecraft:birch_wood": 5,
	"minecraft:jungle_wood": 5,
	"minecraft:acacia_wood": 5,
	"minecraft:cherry_wood": 5,
	"minecraft:pale_oak_wood": 5,
	"minecraft:dark_oak_wood": 5,
	"minecraft:mangrove_wood": 5,
	"minecraft:mangrove_roots": 5,
	"minecraft:oak_leaves": 30,
	"minecraft:spruce_leaves": 30,
	"minecraft:birch_leaves": 30,
	"minecraft:jungle_leaves": 30,
	"minecraft:acacia_leaves": 30,
	"minecraft:cherry_leaves": 30,
	"minecraft:dark_oak_leaves": 30,
	"minecraft:pale_oak_leaves": 30,
	"minecraft:mangrove_leaves": 30,
	"minecraft:bookshelf": 30,
	"minecraft:tnt": 15,
	"minecraft:short_grass": 60,
	"minecraft:fern": 60,
	"minecraft:dead_bush": 60,
	"minecraft:short_dry_grass": 60,
	"minecraft:tall_dry_grass": 60,
	"minecraft:sunflower": 60,
	"minecraft:lilac": 60,
	"minecraft:rose_bush": 60,
	"minecraft:peony": 60,
	"minecraft:tall_grass": 60,
	"minecraft:large_fern": 60,
	"minecraft:dandelion": 60,
	"minecraft:golden_dandelion": 60,
	"minecraft:poppy": 60,
	"minecraft:open_eyeblossom": 60,
	"minecraft:closed_eyeblossom": 60,
	"minecraft:blue_orchid": 60,
	"minecraft:allium": 60,
	"minecraft:azure_bluet": 60,
	"minecraft:red_tulip": 60,
	"minecraft:orange_tulip": 60,
	"minecraft:white_tulip": 60,
	"minecraft:pink_tulip": 60,
	"minecraft:oxeye_daisy": 60,
	"minecraft:cornflower": 60,
	"minecraft:lily_of_the_valley": 60,
	"minecraft:torchflower": 60,
	"minecraft:pitcher_plant": 60,
	"minecraft:wither_rose": 60,
	"minecraft:pink_petals": 60,
	"minecraft:wildflowers": 60,
	"minecraft:leaf_litter": 60,
	"minecraft:cactus_flower": 60,
	"minecraft:white_wool": 60,
	"minecraft:orange_wool": 60,
	"minecraft:magenta_wool": 60,
	"minecraft:light_blue_wool": 60,
	"minecraft:yellow_wool": 60,
	"minecraft:lime_wool": 60,
	"minecraft:pink_wool": 60,
	"minecraft:gray_wool": 60,
	"minecraft:light_gray_wool": 60,
	"minecraft:cyan_wool": 60,
	"minecraft:purple_wool": 60,
	"minecraft:blue_wool": 60,
	"minecraft:brown_wool": 60,
	"minecraft:green_wool": 60,
	"minecraft:red_wool": 60,
	"minecraft:black_wool": 60,
	"minecraft:vine": 15,
	"minecraft:coal_block": 5,
	"minecraft:hay_block": 60,
	"minecraft:target": 15,
	"minecraft:white_carpet": 30,
	"minecraft:orange_carpet": 30,
	"minecraft:magenta_carpet": 30,
	"minecraft:light_blue_carpet": 30,
	"minecraft:yellow_carpet": 30,
	"minecraft:lime_carpet": 30,
	"minecraft:pink_carpet": 30,
	"minecraft:gray_carpet": 30,
	"minecraft:light_gray_carpet": 30,
	"minecraft:cyan_carpet": 30,
	"minecraft:purple_carpet": 30,
	"minecraft:blue_carpet": 30,
	"minecraft:brown_carpet": 30,
	"minecraft:green_carpet": 30,
	"minecraft:red_carpet": 30,
	"minecraft:black_carpet": 30,
	"minecraft:pale_moss_block": 5,
	"minecraft:pale_moss_carpet": 5,
	"minecraft:pale_hanging_moss": 5,
	"minecraft:dried_kelp_block": 30,
	"minecraft:bamboo": 60,
	"minecraft:scaffolding": 60,
	"minecraft:lectern": 30,
	"minecraft:composter": 5,
	"minecraft:sweet_berry_bush": 60,
	"minecraft:beehive": 5,
	"minecraft:bee_nest": 30,
	"minecraft:azalea_leaves": 30,
	"minecraft:flowering_azalea_leaves": 30,
	"minecraft:cave_vines": 15,
	"minecraft:cave_vines_plant": 15,
	"minecraft:spore_blossom": 60,
	"minecraft:azalea": 30,
	"minecraft:flowering_azalea": 30,
	"minecraft:big_dripleaf": 60,
	"minecraft:big_dripleaf_stem": 60,
	"minecraft:small_dripleaf": 60,
	"minecraft:hanging_roots": 30,
	"minecraft:glow_lichen": 15,
	"minecraft:firefly_bush": 60,
	"minecraft:bush": 60,
	"minecraft:acacia_shelf": 30,
	"minecraft:bamboo_shelf": 30,
	"minecraft:birch_shelf": 30,
	"minecraft:cherry_shelf": 30,
	"minecraft:dark_oak_shelf": 30,
	"minecraft:jungle_shelf": 30,
	"minecraft:mangrove_shelf": 30,
	"minecraft:oak_shelf": 30,
	"minecraft:pale_oak_shelf": 30,
	"minecraft:spruce_shelf": 30,
}

// fireBurnTable maps a block id to its setFlammable burn odds. CITE: FireBlock.bootStrap.
var fireBurnTable = map[string]int{
	"minecraft:oak_planks": 20,
	"minecraft:spruce_planks": 20,
	"minecraft:birch_planks": 20,
	"minecraft:jungle_planks": 20,
	"minecraft:acacia_planks": 20,
	"minecraft:cherry_planks": 20,
	"minecraft:dark_oak_planks": 20,
	"minecraft:pale_oak_planks": 20,
	"minecraft:mangrove_planks": 20,
	"minecraft:bamboo_planks": 20,
	"minecraft:bamboo_mosaic": 20,
	"minecraft:oak_slab": 20,
	"minecraft:spruce_slab": 20,
	"minecraft:birch_slab": 20,
	"minecraft:jungle_slab": 20,
	"minecraft:acacia_slab": 20,
	"minecraft:cherry_slab": 20,
	"minecraft:dark_oak_slab": 20,
	"minecraft:pale_oak_slab": 20,
	"minecraft:mangrove_slab": 20,
	"minecraft:bamboo_slab": 20,
	"minecraft:bamboo_mosaic_slab": 20,
	"minecraft:oak_fence_gate": 20,
	"minecraft:spruce_fence_gate": 20,
	"minecraft:birch_fence_gate": 20,
	"minecraft:jungle_fence_gate": 20,
	"minecraft:acacia_fence_gate": 20,
	"minecraft:cherry_fence_gate": 20,
	"minecraft:dark_oak_fence_gate": 20,
	"minecraft:pale_oak_fence_gate": 20,
	"minecraft:mangrove_fence_gate": 20,
	"minecraft:bamboo_fence_gate": 20,
	"minecraft:oak_fence": 20,
	"minecraft:spruce_fence": 20,
	"minecraft:birch_fence": 20,
	"minecraft:jungle_fence": 20,
	"minecraft:acacia_fence": 20,
	"minecraft:cherry_fence": 20,
	"minecraft:dark_oak_fence": 20,
	"minecraft:pale_oak_fence": 20,
	"minecraft:mangrove_fence": 20,
	"minecraft:bamboo_fence": 20,
	"minecraft:oak_stairs": 20,
	"minecraft:birch_stairs": 20,
	"minecraft:spruce_stairs": 20,
	"minecraft:jungle_stairs": 20,
	"minecraft:acacia_stairs": 20,
	"minecraft:cherry_stairs": 20,
	"minecraft:dark_oak_stairs": 20,
	"minecraft:pale_oak_stairs": 20,
	"minecraft:mangrove_stairs": 20,
	"minecraft:bamboo_stairs": 20,
	"minecraft:bamboo_mosaic_stairs": 20,
	"minecraft:oak_log": 5,
	"minecraft:spruce_log": 5,
	"minecraft:birch_log": 5,
	"minecraft:jungle_log": 5,
	"minecraft:acacia_log": 5,
	"minecraft:cherry_log": 5,
	"minecraft:pale_oak_log": 5,
	"minecraft:dark_oak_log": 5,
	"minecraft:mangrove_log": 5,
	"minecraft:bamboo_block": 5,
	"minecraft:stripped_oak_log": 5,
	"minecraft:stripped_spruce_log": 5,
	"minecraft:stripped_birch_log": 5,
	"minecraft:stripped_jungle_log": 5,
	"minecraft:stripped_acacia_log": 5,
	"minecraft:stripped_cherry_log": 5,
	"minecraft:stripped_dark_oak_log": 5,
	"minecraft:stripped_pale_oak_log": 5,
	"minecraft:stripped_mangrove_log": 5,
	"minecraft:stripped_bamboo_block": 5,
	"minecraft:stripped_oak_wood": 5,
	"minecraft:stripped_spruce_wood": 5,
	"minecraft:stripped_birch_wood": 5,
	"minecraft:stripped_jungle_wood": 5,
	"minecraft:stripped_acacia_wood": 5,
	"minecraft:stripped_cherry_wood": 5,
	"minecraft:stripped_dark_oak_wood": 5,
	"minecraft:stripped_pale_oak_wood": 5,
	"minecraft:stripped_mangrove_wood": 5,
	"minecraft:oak_wood": 5,
	"minecraft:spruce_wood": 5,
	"minecraft:birch_wood": 5,
	"minecraft:jungle_wood": 5,
	"minecraft:acacia_wood": 5,
	"minecraft:cherry_wood": 5,
	"minecraft:pale_oak_wood": 5,
	"minecraft:dark_oak_wood": 5,
	"minecraft:mangrove_wood": 5,
	"minecraft:mangrove_roots": 20,
	"minecraft:oak_leaves": 60,
	"minecraft:spruce_leaves": 60,
	"minecraft:birch_leaves": 60,
	"minecraft:jungle_leaves": 60,
	"minecraft:acacia_leaves": 60,
	"minecraft:cherry_leaves": 60,
	"minecraft:dark_oak_leaves": 60,
	"minecraft:pale_oak_leaves": 60,
	"minecraft:mangrove_leaves": 60,
	"minecraft:bookshelf": 20,
	"minecraft:tnt": 100,
	"minecraft:short_grass": 100,
	"minecraft:fern": 100,
	"minecraft:dead_bush": 100,
	"minecraft:short_dry_grass": 100,
	"minecraft:tall_dry_grass": 100,
	"minecraft:sunflower": 100,
	"minecraft:lilac": 100,
	"minecraft:rose_bush": 100,
	"minecraft:peony": 100,
	"minecraft:tall_grass": 100,
	"minecraft:large_fern": 100,
	"minecraft:dandelion": 100,
	"minecraft:golden_dandelion": 100,
	"minecraft:poppy": 100,
	"minecraft:open_eyeblossom": 100,
	"minecraft:closed_eyeblossom": 100,
	"minecraft:blue_orchid": 100,
	"minecraft:allium": 100,
	"minecraft:azure_bluet": 100,
	"minecraft:red_tulip": 100,
	"minecraft:orange_tulip": 100,
	"minecraft:white_tulip": 100,
	"minecraft:pink_tulip": 100,
	"minecraft:oxeye_daisy": 100,
	"minecraft:cornflower": 100,
	"minecraft:lily_of_the_valley": 100,
	"minecraft:torchflower": 100,
	"minecraft:pitcher_plant": 100,
	"minecraft:wither_rose": 100,
	"minecraft:pink_petals": 100,
	"minecraft:wildflowers": 100,
	"minecraft:leaf_litter": 100,
	"minecraft:cactus_flower": 100,
	"minecraft:white_wool": 20,
	"minecraft:orange_wool": 20,
	"minecraft:magenta_wool": 20,
	"minecraft:light_blue_wool": 20,
	"minecraft:yellow_wool": 20,
	"minecraft:lime_wool": 20,
	"minecraft:pink_wool": 20,
	"minecraft:gray_wool": 20,
	"minecraft:light_gray_wool": 20,
	"minecraft:cyan_wool": 20,
	"minecraft:purple_wool": 20,
	"minecraft:blue_wool": 20,
	"minecraft:brown_wool": 20,
	"minecraft:green_wool": 20,
	"minecraft:red_wool": 20,
	"minecraft:black_wool": 20,
	"minecraft:vine": 100,
	"minecraft:coal_block": 5,
	"minecraft:hay_block": 20,
	"minecraft:target": 20,
	"minecraft:white_carpet": 60,
	"minecraft:orange_carpet": 60,
	"minecraft:magenta_carpet": 60,
	"minecraft:light_blue_carpet": 60,
	"minecraft:yellow_carpet": 60,
	"minecraft:lime_carpet": 60,
	"minecraft:pink_carpet": 60,
	"minecraft:gray_carpet": 60,
	"minecraft:light_gray_carpet": 60,
	"minecraft:cyan_carpet": 60,
	"minecraft:purple_carpet": 60,
	"minecraft:blue_carpet": 60,
	"minecraft:brown_carpet": 60,
	"minecraft:green_carpet": 60,
	"minecraft:red_carpet": 60,
	"minecraft:black_carpet": 60,
	"minecraft:pale_moss_block": 100,
	"minecraft:pale_moss_carpet": 100,
	"minecraft:pale_hanging_moss": 100,
	"minecraft:dried_kelp_block": 60,
	"minecraft:bamboo": 60,
	"minecraft:scaffolding": 60,
	"minecraft:lectern": 20,
	"minecraft:composter": 20,
	"minecraft:sweet_berry_bush": 100,
	"minecraft:beehive": 20,
	"minecraft:bee_nest": 20,
	"minecraft:azalea_leaves": 60,
	"minecraft:flowering_azalea_leaves": 60,
	"minecraft:cave_vines": 60,
	"minecraft:cave_vines_plant": 60,
	"minecraft:spore_blossom": 100,
	"minecraft:azalea": 60,
	"minecraft:flowering_azalea": 60,
	"minecraft:big_dripleaf": 100,
	"minecraft:big_dripleaf_stem": 100,
	"minecraft:small_dripleaf": 100,
	"minecraft:hanging_roots": 60,
	"minecraft:glow_lichen": 100,
	"minecraft:firefly_bush": 100,
	"minecraft:bush": 100,
	"minecraft:acacia_shelf": 20,
	"minecraft:bamboo_shelf": 20,
	"minecraft:birch_shelf": 20,
	"minecraft:cherry_shelf": 20,
	"minecraft:dark_oak_shelf": 20,
	"minecraft:jungle_shelf": 20,
	"minecraft:mangrove_shelf": 20,
	"minecraft:oak_shelf": 20,
	"minecraft:pale_oak_shelf": 20,
	"minecraft:spruce_shelf": 20,
}
