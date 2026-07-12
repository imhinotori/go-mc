package server

// ai_goals_enderman_carry.go — MOB-HOST-08 (Enderman block-carry): the EnderMan's block pick-up /
// put-down subsystem, ported 1:1 (idiomatic Go, no GPL paste) from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, read via javap -c -p / CFR this session). Lands the two inner goals
// EnderMan$EndermanTakeBlockGoal (goalSelector @11) + EnderMan$EndermanLeaveBlockGoal (goalSelector @10)
// plus the DATA_CARRY_STATE-backed carriedBlockState field (server/entity.go) they read/write.
//
// Ported classes (inner classes of net.minecraft.world.entity.monster.EnderMan):
//
//   - EnderMan.getCarriedBlock()/setCarriedBlock(BlockState): the DATA_CARRY_STATE (Optional<BlockState>)
//     accessor. getCarriedBlock() == null when not carrying. Backed here by e.carriedBlockState +
//     e.carriedBlockSet (block.StateID + a present bit — the Optional<BlockState> empty/present split).
//     Cite javap EnderMan.setCarriedBlock / getCarriedBlock over SynchedEntityData DATA_CARRY_STATE.
//
//   - EnderMan$EndermanTakeBlockGoal (goalSelector @11, flags {} — the ctor sets NO flags):
//       canUse():
//         if (getCarriedBlock() != null) return false;
//         if (!mobGriefing) return false;
//         return getRandom().nextInt(reducedTickDelay(20)) == 0;
//       tick():
//         int x = Mth.floor(getX() - 2.0 + nextDouble()*4.0);
//         int y = Mth.floor(getY()       + nextDouble()*3.0);
//         int z = Mth.floor(getZ() - 2.0 + nextDouble()*4.0);
//         state = level.getBlockState(pos);
//         reachable = level.clip(from,to).getBlockPos().equals(pos);
//         if (state.is(BlockTags.ENDERMAN_HOLDABLE) && reachable) {
//             level.removeBlock(pos, false);
//             level.gameEvent(BLOCK_DESTROY, pos, ...);
//             setCarriedBlock(state.getBlock().defaultBlockState());
//         }
//     The reachable clip raycast is CITE-DEFERRED (no clip/raycast subsystem in v1 — the same LoS/clip
//     deferral the gaze goal + the target goals take; treated as true so the block search proceeds), and
//     gameEvent(BLOCK_DESTROY) is a CITE-DEFERRED no-op (no game-event/sculk subsystem, exactly as
//     combat_mob.go's ENTITY_DAMAGE + the turtle egg BLOCK_PLACE defer it). The RNG draw order (canUse: one
//     nextInt; tick: nextDouble x, nextDouble y, nextDouble z IN THAT ORDER) is preserved EXACTLY.
//
//   - EnderMan$EndermanLeaveBlockGoal (goalSelector @10, flags {} — the ctor sets NO flags):
//       canUse():
//         if (getCarriedBlock() == null) return false;
//         if (!mobGriefing) return false;
//         return getRandom().nextInt(reducedTickDelay(2000)) == 0;
//       tick():
//         int x = Mth.floor(getX() - 1.0 + nextDouble()*2.0);
//         int y = Mth.floor(getY()       + nextDouble()*2.0);
//         int z = Mth.floor(getZ() - 1.0 + nextDouble()*2.0);
//         target = level.getBlockState(pos); below = pos.below(); belowState = level.getBlockState(below);
//         carried = getCarriedBlock(); if (carried == null) return;
//         carried = Block.updateFromNeighbourShapes(carried, level, pos);
//         if (canPlaceBlock(level, pos, carried, target, belowState, below)) {
//             level.setBlock(pos, carried, 3);
//             level.gameEvent(BLOCK_PLACE, pos, ...);
//             setCarriedBlock(null);
//         }
//       canPlaceBlock = target.isAir() && !belowState.isAir() && !belowState.is(BEDROCK)
//                       && belowState.isCollisionShapeFullBlock(level, below) && carried.canSurvive(level, pos)
//                       && level.getEntities(enderman, unitCube(pos)).isEmpty();
//     Block.updateFromNeighbourShapes is a CITE-DEFERRED identity in v1 (no neighbour-shape engine; the
//     carried default state is placed as-is — the common case for the HOLDABLE full-cube blocks).
//     carried.canSurvive is a CITE-DEFERRED true (no block-support engine; the below-is-full-collision
//     check guards the common floor-place). belowState.isCollisionShapeFullBlock reduces to the v1 isSolidAt
//     full-cube solidity read. The entity-overlap emptiness uses the live section entity buckets. RNG draw
//     order (canUse: one nextInt; tick: nextDouble x, y, z) preserved EXACTLY. gameEvent(BLOCK_PLACE) deferred.

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// blockTagEndermanHoldable is the bare name of net.minecraft.tags.BlockTags.ENDERMAN_HOLDABLE
// (BlockTags.create("enderman_holdable")). Pinned so the take goal reads the SAME flattened tag the Update
// Tags wire packet binds (server/registrydata/tags/block/enderman_holdable.json).
//
//	[VERIFIED CFR EndermanTakeBlockGoal.tick: blockState.is(BlockTags.ENDERMAN_HOLDABLE);
//	 BlockTags.ENDERMAN_HOLDABLE == BlockTags.create("enderman_holdable").]
const blockTagEndermanHoldable = "enderman_holdable"

// endermanTakeBlockInterval is EndermanTakeBlockGoal.canUse's nextInt bound BEFORE reducedTickDelay:
// nextInt(reducedTickDelay(20)) == nextInt(10). The literal 20.
//
//	[VERIFIED CFR EndermanTakeBlockGoal.canUse: getRandom().nextInt(reducedTickDelay(20)) == 0.]
const endermanTakeBlockInterval = 20

// endermanLeaveBlockInterval is EndermanLeaveBlockGoal.canUse's nextInt bound BEFORE reducedTickDelay:
// nextInt(reducedTickDelay(2000)) == nextInt(1000). The literal 2000 — the enderman puts a block DOWN far
// more rarely than it picks one up.
//
//	[VERIFIED CFR EndermanLeaveBlockGoal.canUse: getRandom().nextInt(reducedTickDelay(2000)) == 0.]
const endermanLeaveBlockInterval = 2000

// --- endermanTakeBlockGoal (goalSelector @11, flags {}) --------------------------------------------

// endermanTakeBlockGoal ports EnderMan.EndermanTakeBlockGoal: while NOT carrying and mobGriefing is on, a
// ~1-in-10 per-tick roll makes the enderman look at a random block in a 4x3x4 box around it; if that block
// is #minecraft:enderman_holdable (and reachable — cite-deferred), it removes the block and carries that
// block's default state. NO flags (the ctor sets no flag set — it does not reserve MOVE/LOOK).
type endermanTakeBlockGoal struct {
	baseGoal
}

// newEndermanTakeBlockGoal builds the take goal with NO flags (EndermanTakeBlockGoal.<init> sets none).
//
//	[VERIFIED CFR EndermanTakeBlockGoal has no setFlags call — the flag set is empty.]
func newEndermanTakeBlockGoal() *endermanTakeBlockGoal {
	return &endermanTakeBlockGoal{baseGoal: newBaseGoal(0)}
}

// canUse ports EndermanTakeBlockGoal.canUse: not-carrying gate, mobGriefing gate, then the
// nextInt(reducedTickDelay(20)) == 0 roll (drawn LAST, only when both gates pass — the bytecode order).
//
//	[VERIFIED CFR EndermanTakeBlockGoal.canUse: if (getCarriedBlock()!=null) return false;
//	 if (!mobGriefing) return false; return getRandom().nextInt(reducedTickDelay(20))==0.]
func (g *endermanTakeBlockGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.carriedBlockSet {
		return false // getCarriedBlock() != null
	}
	if !t.gameRule(ruleMobGriefing) {
		return false // MOB_GRIEFING gamerule off
	}
	return mobRandom(e).nextInt(reducedTickDelay(endermanTakeBlockInterval)) == 0
}

// canContinueToUse: EndermanTakeBlockGoal defines no override, so Goal.canContinueToUse delegates to canUse.
func (g *endermanTakeBlockGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}

// tick ports EndermanTakeBlockGoal.tick: draw a random cell in (x-2..x+2, y..y+3, z-2..z+2) (nextDouble x,
// then y, then z), read the block, and if HOLDABLE (and reachable — cite-deferred true) remove it and carry
// the block's default state. Cite EndermanTakeBlockGoal.tick.
func (g *endermanTakeBlockGoal) tick(t *TickLoop, e *Entity) {
	r := mobRandom(e)
	// The draws MUST be x, y, z in this order (the exact bytecode sequence).
	xt := int(math.Floor(e.x - 2.0 + r.nextDouble()*4.0))
	yt := int(math.Floor(e.y + r.nextDouble()*3.0))
	zt := int(math.Floor(e.z - 2.0 + r.nextDouble()*4.0))
	sid := t.blockStateAt(xt, yt, zt)
	// reachable = clip(...).getBlockPos().equals(pos): CITE-DEFERRED true (no clip/raycast subsystem in v1).
	if !blockInTag(sid, blockTagEndermanHoldable) {
		return
	}
	pos := pk.Position{X: xt, Y: yt, Z: zt}
	// level.removeBlock(pos, false) -> set to air + broadcast; the client sees the block vanish.
	if t.world() != nil && t.world().SetBlock(pos, 0, dimMinY) {
		t.broadcastBlockUpdate(pos, 0)
		// Light re-propagation fires CENTRALLY from ChunkManager.SetBlock (SetBlockChangeHook) -- the
		// SetBlock above already relit + broadcast. No per-site relight call.
	}
	// level.gameEvent(this.enderman, GameEvent.BLOCK_DESTROY, pos): posted with Context.of(enderman,
	// takenState) so a nearby sculk sensor / warden hears the enderman removing a block. CITE
	// EnderMan.EndermanTakeBlockGoal.tick (GameEvent.Context.of(enderman, state)).
	t.gameEventAt(geBlockDestroy, pos, gameEventContext{sourceEntityID: e.id, affectedState: int(sid)})
	// setCarriedBlock(state.getBlock().defaultBlockState()): carry the DEFAULT state of the taken block.
	setEndermanCarriedBlock(t, e, endermanDefaultStateOf(sid), true)
}

// --- endermanLeaveBlockGoal (goalSelector @10, flags {}) -------------------------------------------

// endermanLeaveBlockGoal ports EnderMan.EndermanLeaveBlockGoal: while carrying and mobGriefing is on, a
// ~1-in-1000 per-tick roll makes the enderman try to PLACE its carried block at a random cell in a 2x2x2
// box around it, provided the cell is air, sits on a full-collision non-bedrock block, and is entity-free.
// On success it places the block and stops carrying. NO flags (the ctor sets no flag set).
type endermanLeaveBlockGoal struct {
	baseGoal
}

// newEndermanLeaveBlockGoal builds the leave goal with NO flags (EndermanLeaveBlockGoal.<init> sets none).
//
//	[VERIFIED CFR EndermanLeaveBlockGoal has no setFlags call — the flag set is empty.]
func newEndermanLeaveBlockGoal() *endermanLeaveBlockGoal {
	return &endermanLeaveBlockGoal{baseGoal: newBaseGoal(0)}
}

// canUse ports EndermanLeaveBlockGoal.canUse: carrying gate, mobGriefing gate, then the
// nextInt(reducedTickDelay(2000)) == 0 roll (drawn LAST, only when both gates pass).
//
//	[VERIFIED CFR EndermanLeaveBlockGoal.canUse: if (getCarriedBlock()==null) return false;
//	 if (!mobGriefing) return false; return getRandom().nextInt(reducedTickDelay(2000))==0.]
func (g *endermanLeaveBlockGoal) canUse(t *TickLoop, e *Entity) bool {
	if !e.carriedBlockSet {
		return false // getCarriedBlock() == null
	}
	if !t.gameRule(ruleMobGriefing) {
		return false
	}
	return mobRandom(e).nextInt(reducedTickDelay(endermanLeaveBlockInterval)) == 0
}

// canContinueToUse: no override -> delegates to canUse.
func (g *endermanLeaveBlockGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}

// tick ports EndermanLeaveBlockGoal.tick: draw a random cell in (x-1..x+1, y..y+1, z-1..z+1) (nextDouble x,
// y, z in order), and if canPlaceBlock holds, place the carried block there and clear the carry.
// Block.updateFromNeighbourShapes is a CITE-DEFERRED identity (no neighbour-shape engine). Cite
// EndermanLeaveBlockGoal.tick.
func (g *endermanLeaveBlockGoal) tick(t *TickLoop, e *Entity) {
	r := mobRandom(e)
	// draws x, y, z in this exact order.
	xt := int(math.Floor(e.x - 1.0 + r.nextDouble()*2.0))
	yt := int(math.Floor(e.y + r.nextDouble()*2.0))
	zt := int(math.Floor(e.z - 1.0 + r.nextDouble()*2.0))
	if !e.carriedBlockSet {
		return // carried == null guard (canUse already gated)
	}
	carried := e.carriedBlockState
	// carried = Block.updateFromNeighbourShapes(carried, level, pos): CITE-DEFERRED identity in v1.
	if g.canPlaceBlock(t, e, xt, yt, zt) {
		pos := pk.Position{X: xt, Y: yt, Z: zt}
		if t.world() != nil && t.world().SetBlock(pos, carried, dimMinY) {
			t.broadcastBlockUpdate(pos, carried)
			// Light re-propagation fires CENTRALLY from ChunkManager.SetBlock (SetBlockChangeHook) -- the
			// SetBlock above already relit + broadcast. No per-site relight call.
		}
		// level.gameEvent(this.enderman, GameEvent.BLOCK_PLACE, pos): posted with Context.of(enderman,
		// placedState) so a nearby sculk sensor / warden hears the enderman placing its carried block.
		// CITE EnderMan.EndermanLeaveBlockGoal.tick (GameEvent.Context.of(enderman, state)).
		t.gameEventAt(geBlockPlace, pos, gameEventContext{sourceEntityID: e.id, affectedState: int(carried)})
		setEndermanCarriedBlock(t, e, 0, false) // setCarriedBlock(null)
	}
}

// canPlaceBlock ports EndermanLeaveBlockGoal.canPlaceBlock:
//
//	target.isAir() && !belowState.isAir() && !belowState.is(Blocks.BEDROCK)
//	  && belowState.isCollisionShapeFullBlock(level, below) && carried.canSurvive(level, pos)
//	  && level.getEntities(enderman, unitCube(pos)).isEmpty()
//
// v1 mappings: isAir() == blockStateAt==0; belowState full-collision == isSolidAt (the v1 full-cube
// solidity read — bedrock is solid so the explicit BEDROCK exclusion is still applied on the below state
// id); carried.canSurvive == CITE-DEFERRED true (no block-support engine). The entity-overlap emptiness
// uses the live entity buckets over the target cell's unit cube. NO RNG.
//
//	[VERIFIED CFR EndermanLeaveBlockGoal.canPlaceBlock per the excerpt above.]
func (g *endermanLeaveBlockGoal) canPlaceBlock(t *TickLoop, e *Entity, x, y, z int) bool {
	if t.blockStateAt(x, y, z) != 0 { // target.isAir()
		return false
	}
	belowSid := t.blockStateAt(x, y-1, z)
	if belowSid == 0 { // !belowState.isAir()
		return false
	}
	if belowSid == endermanBedrockStateID() { // !belowState.is(Blocks.BEDROCK)
		return false
	}
	// belowState.isCollisionShapeFullBlock(level, below): v1 full-cube solidity == isSolidAt.
	if !t.isSolidAt(pk.Position{X: x, Y: y - 1, Z: z}) {
		return false
	}
	// carried.canSurvive(level, pos): CITE-DEFERRED true (no canSurvive/support engine in v1).
	// level.getEntities(enderman, unitCube(pos)).isEmpty(): no OTHER entity overlaps the target cell.
	return endermanCellEntityFree(t, e, x, y, z)
}

// --- shared carry helpers -------------------------------------------------------------------------

// setEndermanCarriedBlock ports EnderMan.setCarriedBlock: store the carried state (or clear it) and push
// DATA_CARRY_STATE to trackers so the client renders the carried block (or clears it). present=false clears
// (setCarriedBlock(null)); present=true carries sid. Enderman-gated by the callers.
//
//	[VERIFIED javap EnderMan.setCarriedBlock: entityData.set(DATA_CARRY_STATE, Optional.ofNullable(state)).]
func setEndermanCarriedBlock(t *TickLoop, e *Entity, sid block.StateID, present bool) {
	e.carriedBlockState = sid
	e.carriedBlockSet = present
	// Push DATA_CARRY_STATE to trackers (the client render of the held block). SetEntityData by id.
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, carriedBlockDataEntry(sid, present)))
}

// endermanDefaultStateOf ports state.getBlock().defaultBlockState(): map an arbitrary state id to its
// block's DEFAULT state id (block name via block.StateList, default via block.DefaultStateID). An out-of-
// range / unknown state falls back to the input (the taken block is always a resolvable HOLDABLE block).
//
//	[VERIFIED CFR EndermanTakeBlockGoal.tick: setCarriedBlock(blockState.getBlock().defaultBlockState()).]
func endermanDefaultStateOf(sid block.StateID) block.StateID {
	if int(sid) < 0 || int(sid) >= len(block.StateList) {
		return sid
	}
	name := block.StateList[sid].ID()
	if def, ok := block.DefaultStateID[name]; ok {
		return def
	}
	return sid
}

// endermanBedrockStateID is Blocks.BEDROCK's default state id — the below-block exclusion in canPlaceBlock
// (an enderman never places a carried block on bedrock). Resolved from the codegen'd default map.
//
//	[VERIFIED CFR EndermanLeaveBlockGoal.canPlaceBlock: !belowState.is(Blocks.BEDROCK).]
func endermanBedrockStateID() block.StateID {
	return block.DefaultStateID["minecraft:bedrock"]
}

// endermanCellEntityFree ports level.getEntities(enderman, AABB.unitCubeFromLowerCorner(pos)).isEmpty():
// no entity OTHER than the enderman overlaps the target cell's unit cube [x,x+1)x[y,y+1)x[z,z+1). v1 scans
// the live section buckets (near, 1-chunk radius safely covers the unit cube), excluding the enderman, and
// tests each candidate's AABB against the unit cube.
//
//	[VERIFIED CFR EndermanLeaveBlockGoal.canPlaceBlock: level.getEntities(this.enderman,
//	 AABB.unitCubeFromLowerCorner(Vec3.atLowerCornerOf(pos))).isEmpty().]
func endermanCellEntityFree(t *TickLoop, e *Entity, x, y, z int) bool {
	minX, minY, minZ := float64(x), float64(y), float64(z)
	maxX, maxY, maxZ := minX+1.0, minY+1.0, minZ+1.0
	for _, o := range t.cur().entities.near(float64(x), float64(z), 1) {
		if o == nil || o.id == e.id {
			continue
		}
		// The entity AABB is [x-w/2, x+w/2] x [y, y+height] x [z-w/2, z+w/2] (Entity.AABB).
		hw := o.width / 2
		oMinX, oMaxX := o.x-hw, o.x+hw
		oMinZ, oMaxZ := o.z-hw, o.z+hw
		oMinY, oMaxY := o.y, o.y+o.height
		if oMaxX > minX && oMinX < maxX && oMaxY > minY && oMinY < maxY && oMaxZ > minZ && oMinZ < maxZ {
			return false
		}
	}
	return true
}
