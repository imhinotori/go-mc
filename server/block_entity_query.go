package server

// block_entity_query.go — the reusable block-entity / block-state queries the CatSitOnBlockGoal needs
// (MOB-NEUT-03 @7): the CHEST open-count and the FURNACE-LIT reads. Siblings of chest_open.go's
// isChestBlock. Both are 1:1 ports of the exact vanilla predicates CatSitOnBlockGoal.isValidTarget
// invokes (temp/cache/26.2-inner.jar, javap/CFR this session).
//
//	net.minecraft.world.entity.ai.goal.CatSitOnBlockGoal.isValidTarget(level, pos) (javap this session):
//	    if (!level.isEmptyBlock(pos.above())) return false;
//	    BlockState s = level.getBlockState(pos);
//	    if (s.is(Blocks.CHEST))   return ChestBlockEntity.getOpenCount(level, pos) < 1;
//	    if (s.is(Blocks.FURNACE)) return s.getValue(FurnaceBlock.LIT);
//	    return s.is(BlockTags.BEDS, s2 -> s2.getOptionalValue(BedBlock.PART).map(v -> v != HEAD).orElse(true));

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// isFurnaceBlock reports whether a block state is Blocks.FURNACE (the plain minecraft:furnace — NOT the
// blast_furnace / smoker subclasses, which are separate Blocks). Sibling of isChestBlock. CITE
// CatSitOnBlockGoal.isValidTarget `s.is(Blocks.FURNACE)`.
func isFurnaceBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[s].(block.Furnace)
	return ok
}

// furnaceLit ports the FurnaceBlock.LIT read `s.getValue(FurnaceBlock.LIT)`: the boolean LIT property of
// a minecraft:furnace state. Returns false for a non-furnace state (the caller gates on isFurnaceBlock
// first, so this only reads a real furnace). CITE CatSitOnBlockGoal.isValidTarget FurnaceBlock.LIT.
//
//	[VERIFIED javap CatSitOnBlockGoal.isValidTarget: getstatic FurnaceBlock.LIT; BlockState.getValue ->
//	 Boolean.booleanValue. level/block Furnace struct: {Facing Direction, Lit Boolean}.]
func furnaceLit(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	if f, ok := block.StateList[s].(block.Furnace); ok {
		return bool(f.Lit)
	}
	return false
}

// chestOpenCount ports ChestBlockEntity.getOpenCount(level, pos) == the chest's
// openersCounter.getOpenerCount(): the number of players currently VIEWING the chest at pos. In vanilla
// the ContainerOpenersCounter tracks every player with the chest menu open (it drives the lid animation
// + the CatSitOnBlockGoal <1 gate). Sulfur's faithful analog is the count of players whose open window
// is a chest at exactly pos (chest_open.go's openContainer{kind:chest, chestPos:pos}). A chest nobody is
// viewing returns 0; each viewer adds 1. Tick-owned (reads the tick-owned player windows).
//
//	[VERIFIED CFR ChestBlockEntity.getOpenCount: state.hasBlockEntity() && getBlockEntity instanceof
//	 ChestBlockEntity -> chestBlockEntity.openersCounter.getOpenerCount(); else 0.]
func (t *TickLoop) chestOpenCount(pos pk.Position) int {
	n := 0
	for _, p := range t.players {
		if p == nil || p.openContainer == nil {
			continue
		}
		oc := p.openContainer
		if oc.kind == containerKindChest && oc.chestPos == pos {
			n++
		}
	}
	return n
}

// isBlastFurnaceBlock / isSmokerBlock recognize the two AbstractFurnaceBlock subclasses that also drive a
// furnace block-entity (GAMEPLAY-05). Siblings of isFurnaceBlock. CITE BlastFurnaceBlock / SmokerBlock.
func isBlastFurnaceBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[s].(block.BlastFurnace)
	return ok
}

func isSmokerBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[s].(block.Smoker)
	return ok
}

// isAnyFurnaceBlock reports whether s is any of the three AbstractFurnaceBlock blocks (furnace,
// blast_furnace, smoker) — the open/tick gate for the furnace block-entity subsystem.
func isAnyFurnaceBlock(s block.StateID) bool {
	return isFurnaceBlock(s) || isBlastFurnaceBlock(s) || isSmokerBlock(s)
}

// isBeaconBlock reports whether a block state is Blocks.BEACON (the beacon block that drives a
// BeaconBlockEntity). The open/tick gate for the beacon block-entity subsystem (BEACON-01). Beacon is a
// single-state block (no properties), so the check is a plain state-id identity via block.StateList. CITE
// BeaconBlock (a BaseEntityBlock whose newBlockEntity is BeaconBlockEntity).
func isBeaconBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:beacon"
}

// isConduitBlock reports whether a block state is Blocks.CONDUIT (the conduit block that drives a
// ConduitBlockEntity). The tick gate for the conduit block-entity subsystem (CONDUIT-01). Conduit carries a
// WATERLOGGED property (multiple states), so the check is a block-id identity via block.StateList (not a
// single state-id compare). CITE ConduitBlock (a BaseEntityBlock whose newBlockEntity is ConduitBlockEntity).
func isConduitBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:conduit"
}

// isBrewingStandBlock reports whether a block state is Blocks.BREWING_STAND (the brewing-stand block that
// drives a BrewingStandBlockEntity). The open/tick gate for the brewing-stand block-entity subsystem. CITE
// BrewingStandBlock (an EntityBlock whose newBlockEntity is BrewingStandBlockEntity).
func isBrewingStandBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[s].(block.BrewingStand)
	return ok
}

// brewingStandWithBottles ports the serverTick HAS_BOTTLE toggle: state.setValue(HAS_BOTTLE[i], bits[i]) for
// i in 0..2 (BrewingStandBlock.HAS_BOTTLE = {HAS_BOTTLE_0, HAS_BOTTLE_1, HAS_BOTTLE_2}, VERIFIED CFR
// BrewingStandBlock). Reads the state's BrewingStand struct, writes its three has_bottle_N booleans, and
// resolves the resulting stateID via block.ToStateID. Returns (s, false) if s is not a brewing-stand state.
//
// 1:1 net.minecraft.world.level.block.entity.BrewingStandBlockEntity.serverTick HAS_BOTTLE setValue loop.
func brewingStandWithBottles(s block.StateID, bits [3]bool) (block.StateID, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return s, false
	}
	bs, ok := block.StateList[s].(block.BrewingStand)
	if !ok {
		return s, false
	}
	bs.HasBottle0 = block.Boolean(bits[0])
	bs.HasBottle1 = block.Boolean(bits[1])
	bs.HasBottle2 = block.Boolean(bits[2])
	if id, ok := block.ToStateID[bs]; ok {
		return id, true
	}
	return s, false
}

// setBrewingStandBlockBottles writes the HAS_BOTTLE-toggled brewing-stand state at pos into the world
// (level.setBlock(pos, state, 2) in serverTick). A nil world (tests without a world) is a cheap no-op.
func (t *TickLoop) setBrewingStandBlockBottles(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	t.world().SetBlock(pos, state, dimMinY)
}

// furnaceSubtypeOf returns the cook RecipeType a block state's furnace block-entity uses: smelting for a
// furnace, blasting for a blast_furnace, smoking for a smoker (the AbstractFurnaceBlockEntity ctor's
// recipeType per subclass). The second return is whether s is a furnace-family block at all.
func furnaceSubtypeOf(s block.StateID) (cookSubtype, bool) {
	switch {
	case isFurnaceBlock(s):
		return cookSmelting, true
	case isBlastFurnaceBlock(s):
		return cookBlasting, true
	case isSmokerBlock(s):
		return cookSmoking, true
	}
	return "", false
}

// furnaceWithLit ports state.setValue(AbstractFurnaceBlock.LIT, lit) for any of the three furnace-family
// blocks: read the state's struct, flip its Lit boolean preserving Facing, and resolve the resulting
// stateID via block.ToStateID. Returns (newState, true) on success, or (s, false) if s is not a
// furnace-family state (the caller gates on isAnyFurnaceBlock, so false only guards a corrupt id).
//
// 1:1 net.minecraft.world.level.block.AbstractFurnaceBlock LIT property setValue.
func furnaceWithLit(s block.StateID, lit bool) (block.StateID, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return s, false
	}
	switch b := block.StateList[s].(type) {
	case block.Furnace:
		b.Lit = block.Boolean(lit)
		if id, ok := block.ToStateID[b]; ok {
			return id, true
		}
	case block.BlastFurnace:
		b.Lit = block.Boolean(lit)
		if id, ok := block.ToStateID[b]; ok {
			return id, true
		}
	case block.Smoker:
		b.Lit = block.Boolean(lit)
		if id, ok := block.ToStateID[b]; ok {
			return id, true
		}
	}
	return s, false
}

// setFurnaceBlockLit writes the LIT-toggled furnace state at pos into the world (level.setBlock(pos, state,
// 3) in serverTick's wasLit != isLit branch). A nil world (tests without a world) is a cheap no-op.
func (t *TickLoop) setFurnaceBlockLit(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	t.world().SetBlock(pos, state, dimMinY)
}
