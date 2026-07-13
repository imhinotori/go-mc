package server

// respawn_anchor.go -- the RESPAWN ANCHOR utility block, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.level.block.RespawnAnchorBlock). Full vanilla lifecycle:
//
//   - CHARGE 0..4 IntegerProperty; useItemOn with GLOWSTONE on a CHARGE<4 anchor bumps CHARGE by 1 and
//     consumes one glowstone (charge()); an off-hand glowstone on a chargeable anchor is a PASS so the
//     main-hand click is the one that charges.
//   - useWithoutItem on a CHARGE==0 anchor is a PASS (empty anchor does nothing); on a CHARGED anchor:
//     if canSetSpawn (the dimension's gameplay/respawn_anchor_works env attribute -- TRUE only in the
//     Nether) it SETS the player's respawn point at the anchor (block center, yaw/pitch 0) unless it is
//     already the same point; otherwise (a charged anchor in a dimension where it does NOT work -- the
//     overworld / the End) it EXPLODES (remove the block, radius-5.0 fire BLOCK explosion).
//   - getAnalogOutputSignal == getScaledChargeLevel(state, 15) == Mth.floor((charge-0)/4.0f * 15) for
//     the comparator (0->0, 1->3, 2->7, 3->11, 4->15).
//
// VANILLA CALL CHAIN (RespawnAnchorBlock, javap -c -p this session):
//
//	useItemOn: if (isRespawnFuel(stack) && canBeCharged(state)) {
//	    charge(player, level, pos, state); stack.consume(1, player); return SUCCESS; }
//	    if (hand==MAIN_HAND && isRespawnFuel(getItemInHand(OFF_HAND)) && canBeCharged(state)) return PASS;
//	    return TRY_WITH_EMPTY_HAND;
//	useWithoutItem: if (getValue(CHARGE)==0) return PASS;
//	    if (!(level instanceof ServerLevel)) return CONSUME;
//	    if (canSetSpawn(level, pos)) {
//	        if (player instanceof ServerPlayer sp) {
//	            RespawnConfig cur = sp.getRespawnConfig();
//	            RespawnConfig cfg = new RespawnConfig(RespawnData.of(level.dimension(), pos, 0f, 0f), false);
//	            if (cur==null || !cur.isSamePosition(cfg)) {
//	                sp.setRespawnPosition(cfg, true);
//	                level.playSound(null, x+.5,y+.5,z+.5, RESPAWN_ANCHOR_SET_SPAWN, BLOCKS, 1,1);
//	                return SUCCESS_SERVER; } }
//	        return CONSUME; }
//	    explode(state, level, pos); return SUCCESS_SERVER;
//	isRespawnFuel: stack.is(Items.GLOWSTONE);
//	canBeCharged: getValue(CHARGE) < 4;
//	charge: ns = setValue(CHARGE, getValue(CHARGE)+1); setBlock(pos, ns, 3);
//	    gameEvent(BLOCK_CHANGE); playSound(null, center, RESPAWN_ANCHOR_CHARGE, BLOCKS, 1,1);
//	canSetSpawn: environmentAttributes().getValue(RESPAWN_ANCHOR_WORKS, pos);   // TRUE only in the Nether
//	explode: removeBlock(pos, false); inWater = HORIZONTAL any water-neighbor || above is water;
//	    explode(null, badRespawnPointExplosion(center), <calc>, atCenterOf(pos), 5.0f, true, BLOCK);
//	getAnalogOutputSignal: getScaledChargeLevel(state, 15);
//	getScaledChargeLevel(state, scale): Mth.floor((getValue(CHARGE) - 0) / 4.0f * scale);
//	hasAnalogOutputSignal = true.
//
// RNG: charge draws NOTHING. explode's calculateExplodedPositions draws the shell-ray nextFloats and
// createFire the per-pos nextInt(3), all on the region levelRandom (Level.random), never a per-entity
// stream. Both useItemOn and useWithoutItem are reached ONLY from a PLAYER right-click (useBlockInteraction),
// never from serverAiStep, so the pig oracle is untouched.
//
// CITED SIMPLIFICATIONS (all bytecode-verified, faithful to observable behavior):
//   - the explode() uses the shared explodeWith(0, ..., BLOCK, fire=true) exactly as bed_block.go's
//     nether/end bed explosion does. Vanilla passes a bad_respawn_point damage source + a custom
//     ExplosionDamageCalculator (RespawnAnchorBlock$1) that only raises the block explosion resistance AT
//     the anchor's own position when it is in water -- but the anchor block is already removeBlock'd before
//     the blast, so that position is air/water and the override is observably inert for destroying OTHER
//     blocks. bad_respawn_point damage math is IDENTICAL to explosion (same scaling/exhaustion; only the
//     Intentional-Game-Design death message differs -- a message-only concern). Same equivalence the
//     bed explosion already relies on (damage_source.go damageTypeBadRespawnPoint note).
//   - the RESPAWN_ANCHOR_SET_SPAWN / RESPAWN_ANCHOR_CHARGE sounds are client SFX -- cited no-ops (no
//     ClientboundSound wire here, same as composter's COMPOSTER_EMPTY/READY).

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

const (
	respawnAnchorMinCharges = 0 // RespawnAnchorBlock.MIN_CHARGES.
	respawnAnchorMaxCharges = 4 // RespawnAnchorBlock.MAX_CHARGES.
)

// respawnAnchorExplosionRadius is the radius RespawnAnchorBlock.explode passes to level.explode: 5.0f,
// fire=true, BLOCK interaction. CITE RespawnAnchorBlock.explode (ldc 5.0f; iconst_1; BLOCK).
const respawnAnchorExplosionRadius = 5.0

// respawnAnchorFuelItemID is Items.GLOWSTONE (id 395), the only isRespawnFuel(stack) item. CITE
// RespawnAnchorBlock.isRespawnFuel (stack.is(Items.GLOWSTONE)) + data/item Glowstone id 395.
var respawnAnchorFuelItemID = int32(item.Glowstone.ID)

// respawnAnchorCharge reads CHARGE (0..4) of a respawn_anchor state, or (0,false) for a non-anchor.
// Source: block.StateList (block.RespawnAnchor{Charges Integer}).
func respawnAnchorCharge(s block.StateID) (int, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return 0, false
	}
	if a, ok := block.StateList[s].(block.RespawnAnchor); ok {
		return int(a.Charges), true
	}
	return 0, false
}

// respawnAnchorStateAt returns the respawn_anchor state id for a CHARGE (0..4). Source:
// block.ToStateID[block.RespawnAnchor{Charges Integer}].
func respawnAnchorStateAt(charge int) (block.StateID, bool) {
	s, ok := block.ToStateID[block.RespawnAnchor{Charges: block.Integer(charge)}]
	return s, ok
}

// isRespawnAnchorBlock reports whether a state is a respawn_anchor (any CHARGE).
func isRespawnAnchorBlock(s block.StateID) bool {
	_, ok := respawnAnchorCharge(s)
	return ok
}

// respawnAnchorScaledChargeLevel ports RespawnAnchorBlock.getScaledChargeLevel(state, scale):
// Mth.floor((CHARGE - MIN_CHARGES) / (float)MAX_CHARGES * scale). With MIN=0, MAX=4 and scale=15 this is
// the comparator signal (0->0, 1->3, 2->7, 3->11, 4->15). CITE RespawnAnchorBlock.getScaledChargeLevel.
func respawnAnchorScaledChargeLevel(charge, scale int) int {
	// (float)(charge - MIN_CHARGES) / (float)MAX_CHARGES * (float)scale, then Mth.floor.
	f := float32(charge-respawnAnchorMinCharges) / float32(respawnAnchorMaxCharges) * float32(scale)
	return mthFloorF32(f)
}

// respawnAnchorWorks ports RespawnAnchorBlock.canSetSpawn: environmentAttributes().getValue(
// RESPAWN_ANCHOR_WORKS, pos). The gameplay/respawn_anchor_works attribute is TRUE only in the Nether
// (dimension_type data: the_nether=true, overworld/the_end=false), so v1 hard-maps it by dimension --
// the same shape as bedRuleFor. Becomes a real environmentAttributes().getValue read once that engine
// lands. CITE RespawnAnchorBlock.canSetSpawn + EnvironmentAttributes.RESPAWN_ANCHOR_WORKS
// (gameplay/respawn_anchor_works: the_nether true, overworld/the_end false).
func respawnAnchorWorks(dimension int) bool {
	return dimension == dimNether
}

// useRespawnAnchor is the useBlockInteraction dispatch for a respawn_anchor (chest_open.go seam). It
// merges RespawnAnchorBlock.useItemOn (charge with glowstone) and useWithoutItem (set-spawn / explode),
// in vanilla order: try the fuel/charge path first, and only when it does NOT charge fall through to the
// use-without-item path. Returns true on SUCCESS/CONSUME (no block placed), false on PASS/TRY_WITH_EMPTY_HAND
// (placement continues). CITE RespawnAnchorBlock.useItemOn / useWithoutItem.
func (t *TickLoop) useRespawnAnchor(p *tickPlayer, pos pk.Position, state block.StateID) bool {
	charge, ok := respawnAnchorCharge(state)
	if !ok {
		return false
	}

	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))

	// useItemOn: isRespawnFuel(stack) && canBeCharged(state) -> charge + consume + SUCCESS.
	//   isRespawnFuel == stack.is(GLOWSTONE); canBeCharged == CHARGE < MAX_CHARGES.
	if !slotIsEmpty(held) && int32(held.ItemID) == respawnAnchorFuelItemID && charge < respawnAnchorMaxCharges {
		t.respawnAnchorChargeBlock(p, pos, charge)
		if p.gameMode != gameModeCreative {
			t.shrinkHeldItem(p, inv) // stack.consume(1, player): survival shrinks; creative keeps.
		}
		return true // SUCCESS
	}

	// The MAIN_HAND/OFF_HAND PASS guard (offset 34-66): if the MAIN hand is not fuel but the OFF hand IS
	// fuel and the anchor is chargeable, vanilla returns PASS so the OFF-hand useItemOn runs and charges.
	// v1 has no off-hand item subsystem (the interaction only carries the main-hand stack), so there is no
	// off-hand fuel to defer to -- this branch is unreachable in v1 and collapses into the fall-through to
	// useWithoutItem below, matching TRY_WITH_EMPTY_HAND. CITE RespawnAnchorBlock.useItemOn (offsets 34-66).

	// useWithoutItem: CHARGE == 0 -> PASS (placement continues; nothing happens to an empty anchor).
	if charge == 0 {
		return false
	}

	// canSetSpawn(level, pos): the dimension's respawn_anchor_works attribute (TRUE only in the Nether).
	if respawnAnchorWorks(p.dimension) {
		// RespawnConfig cfg = new RespawnConfig(RespawnData.of(dimension, pos, 0f, 0f), false); if the
		// player's current respawn config is null OR not the same position, setRespawnPosition(cfg, true).
		// isSamePosition compares the RespawnData pos + dimension (the yaw/pitch are ignored by the
		// position comparison), so re-clicking the SAME charged anchor is a no-op CONSUME (no re-set, no
		// sound), matching vanilla. v1's respawn state lives on the tickPlayer (sleep.go setRespawnPosition).
		if !t.respawnAnchorSamePosition(p, pos) {
			t.setRespawnPosition(p, pos, p.dimension, 0, 0, false)
			// playSound(RESPAWN_ANCHOR_SET_SPAWN): client SFX, cited no-op.
		}
		return true // SUCCESS_SERVER (new set) or CONSUME (same position) -- either way no placement.
	}

	// Not a working dimension (overworld / the End): a charged anchor EXPLODES.
	t.respawnAnchorExplode(p, pos)
	return true // SUCCESS_SERVER
}

// respawnAnchorChargeBlock ports RespawnAnchorBlock.charge 1:1: bump CHARGE by 1, setBlock (flag 3),
// emit the BLOCK_CHANGE game event, and play RESPAWN_ANCHOR_CHARGE (cited client no-op). No RNG.
// CITE RespawnAnchorBlock.charge.
func (t *TickLoop) respawnAnchorChargeBlock(p *tickPlayer, pos pk.Position, charge int) {
	ns, ok := respawnAnchorStateAt(charge + 1)
	if !ok {
		return
	}
	w := t.dimWorld(p)
	if w == nil {
		return
	}
	minY := dimMinYFor(p.dimension)
	if w.SetBlock(pos, ns, minY) {
		t.broadcastBlockUpdate(pos, ns)
		t.updateNeighborsAt(pos, updateShapeRecursionLimit)
	}
	// level.gameEvent(player, GameEvent.BLOCK_CHANGE, pos): the vibration emit for the state change.
	t.gameEventAt(geBlockChange, pos, gameEventContext{sourceEntityID: p.entityID, affectedState: int(ns)})
	// playSound(null, center, RESPAWN_ANCHOR_CHARGE, BLOCKS, 1, 1): client SFX, cited no-op.
}

// respawnAnchorSamePosition ports the RespawnConfig.isSamePosition check for the anchor set-spawn: the
// player's current respawn point is the SAME anchor when it is set AND its dimension + block pos match
// this anchor. RespawnConfig.isSamePosition compares the RespawnData (dimension + BlockPos), ignoring
// yaw/pitch. Returns false when the player has no respawn point (cur==null) so the first click always
// sets it. CITE ServerPlayer$RespawnConfig.isSamePosition.
func (t *TickLoop) respawnAnchorSamePosition(p *tickPlayer, pos pk.Position) bool {
	if p.respawnPos == nil {
		return false // cur == null -> not same -> will set
	}
	return p.respawnDimension == p.dimension &&
		p.respawnPos.X == pos.X && p.respawnPos.Y == pos.Y && p.respawnPos.Z == pos.Z
}

// respawnAnchorExplode ports RespawnAnchorBlock.explode: removeBlock(pos, false), compute the inWater
// flag (any HORIZONTAL water neighbor or water above -> getFluidState(...).is(FluidTags.WATER)), then
// a radius-5.0 fire-carrying BLOCK-interaction explosion centered on the anchor block center
// (Vec3.atCenterOf). The custom ExplosionDamageCalculator RespawnAnchorBlock$1 overrides
// getBlockExplosionResistance: when inWater it returns Optional.of(Blocks.WATER.getExplosionResistance())
// AT the blast center (pos), and falls through to the super-class (the standard StateExplosionResistance)
// everywhere else. So a wet anchor blast applies the water resistance (100.0f) at the center cell —
// the (now air) cell that was the anchor — which makes the rays hitting that cell attenuate harder
// and shortens the affected set BEHIND the center. A dry anchor blast passes nil (generic calculator),
// which is byte-identical to the vanilla super path. Tick-owned (called from useRespawnAnchor on the
// owner goroutine). CITE RespawnAnchorBlock.explode + RespawnAnchorBlock$1
// .getBlockExplosionResistance + ExplosionDamageCalculator.getBlockExplosionResistance.
func (t *TickLoop) respawnAnchorExplode(p *tickPlayer, pos pk.Position) {
	w := t.dimWorld(p)
	if w == nil {
		return
	}
	minY := dimMinYFor(p.dimension)

	// level.removeBlock(pos, false): remove the anchor block (broadcast the air update to trackers) BEFORE
	// the blast, exactly as vanilla (so the explosion never has to chew through the anchor's own hardness).
	air := block.DefaultStateID["minecraft:air"]
	if w.SetBlock(pos, air, minY) {
		t.broadcastBlockUpdate(pos, air)
		t.updateNeighborsAt(pos, updateShapeRecursionLimit)
	}

	// RespawnAnchorBlock.explode: inWater = (any HORIZONTAL neighbor is water) || (cell above is water).
	// getFluidState(...).is(FluidTags.WATER) — IsWaterFluid covers water blocks (any level) AND waterlogged
	// blocks, exactly matching the tag closure. LAVA does NOT count (vanilla explicitly tests WATER).
	var resistanceOverride explosionResistanceOverride
	if t.respawnAnchorInWater(w, pos, minY) {
		// RespawnAnchorBlock$1.getBlockExplosionResistance: pos == explosion.center() && inWater ->
		// Optional.of(Blocks.WATER.getExplosionResistance()) (= 100.0f); super otherwise. The override
		// applies ONLY at the blast center; everywhere else the standard path runs unchanged.
		waterRes := explosionWaterResistance
		resistanceOverride = func(p pk.Position) (float32, bool) {
			if p.X == pos.X && p.Y == pos.Y && p.Z == pos.Z {
				return waterRes, true
			}
			return 0, false
		}
	}

	// Vec3 center = Vec3.atCenterOf(pos): the anchor block CENTER (x+0.5, y+0.5, z+0.5). explode(null,
	// badRespawnPointExplosion(center), <calc>, center, 5.0f, true, BLOCK). No source entity (srcID 0).
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y) + 0.5
	cz := float64(pos.Z) + 0.5
	t.explodeWith(0, cx, cy, cz, float64(respawnAnchorExplosionRadius), explosionInteractionBlock, true, resistanceOverride)
}

// respawnAnchorInWater computes RespawnAnchorBlock.explode's inWater flag 1:1:
//
//	inWater = HORIZONTAL.stream().map(pos::relative).anyMatch(p -> isWaterThatWouldFlow(p, level))
//	          || level.getFluidState(pos.above()).is(FluidTags.WATER);
//
// The HORIZONTAL neighbors use isWaterThatWouldFlow (NOT the plain WATER tag) — a neighbor counts only
// if its water WOULD FLOW into the anchor cell, so a weak flowing edge does not spuriously mark
// "in water"; the cell ABOVE uses the plain FluidTags.WATER test. Reads at minY. CITE
// RespawnAnchorBlock.explode + RespawnAnchorBlock.isWaterThatWouldFlow + FluidTags.WATER.
func (t *TickLoop) respawnAnchorInWater(w *world.ChunkManager, pos pk.Position, minY int) bool {
	neighbors := []pk.Position{
		{X: pos.X + 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X, Y: pos.Y, Z: pos.Z + 1},
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1},
	}
	for _, n := range neighbors {
		if t.isWaterThatWouldFlow(w, n, minY) {
			return true
		}
	}
	// pos.above(): the plain FluidTags.WATER test (any water level / waterlogged).
	if st, ok := w.GetBlock(pk.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z}, minY); ok && block.IsWaterFluid(st) {
		return true
	}
	return false
}

// isWaterThatWouldFlow ports RespawnAnchorBlock.isWaterThatWouldFlow(pos, level) — the HORIZONTAL-
// neighbor water gate for the explode inWater flag. Verified bytecode:
//
//	FluidState f = level.getFluidState(pos);
//	if (!f.is(FluidTags.WATER)) return false;   // not water -> no
//	if (f.isSource()) return true;              // a source always would flow
//	if ((float) f.getAmount() < 2.0f) return false;  // amount 1 is too weak to flow
//	return !level.getFluidState(pos.below()).is(FluidTags.WATER); // flows down UNLESS water below
//
// i.e. flowing water counts only when it is amount>=2 AND the cell below is not itself water (so it
// would spill downward into/around the anchor). fluidAt decodes the (isWater, source, amount) trio.
// CITE net.minecraft.world.level.block.RespawnAnchorBlock.isWaterThatWouldFlow; FluidTags.WATER.
func (t *TickLoop) isWaterThatWouldFlow(w *world.ChunkManager, pos pk.Position, minY int) bool {
	st, ok := w.GetBlock(pos, minY)
	if !ok || !block.IsWaterFluid(st) {
		return false // !f.is(WATER)
	}
	f := decodeFluid(st)
	if f.source {
		return true // f.isSource()
	}
	if float32(f.amount) < 2.0 {
		return false // getAmount() < 2.0f
	}
	// return !getFluidState(below).is(WATER)
	below, okB := w.GetBlock(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, minY)
	return !(okB && block.IsWaterFluid(below))
}

// respawnAnchorAnalogOutputSignal ports RespawnAnchorBlock.getAnalogOutputSignal (hasAnalogOutputSignal
// true): getScaledChargeLevel(state, 15). Returns (signal, true) for a respawn_anchor; (0, false)
// otherwise so the comparator falls to its container/super path. CITE
// RespawnAnchorBlock.getAnalogOutputSignal.
func (t *TickLoop) respawnAnchorAnalogOutputSignal(pos pk.Position) (int, bool) {
	if t.world() == nil {
		return 0, false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return 0, false
	}
	charge, isAnchor := respawnAnchorCharge(s)
	if !isAnchor {
		return 0, false
	}
	return respawnAnchorScaledChargeLevel(charge, 15), true
}
