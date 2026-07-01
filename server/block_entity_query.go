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
