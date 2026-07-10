package server

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// piston.go — REDSTONE TIER-3 (PISTON): the 1:1 port of the vanilla piston blocks
// (PistonBaseBlock + PistonStructureResolver + MovingPistonBlock + PistonMovingBlockEntity) from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), decompiled via CFR this session. A piston reads
// its power through the core redstone signal graph (redstone.go) — including the quasi-connectivity
// ("bud") rule vanilla 26.2 keeps — and, when its powered state crosses its EXTENDED state, posts a
// same-tick BLOCK EVENT (Level.blockEvent) that runBlockEvents fires as triggerEvent -> moveBlocks:
// extend places a piston_head + moving_piston(s) and shoves the push-list by 1; retract pulls the head
// (and, for sticky, the front block) back by 1. The transient moving_piston animation is carried by a
// PistonMovingBlockEntity that ticks progress 0 -> 1 over TICKS_TO_EXTEND(2) ticks and completes the
// move.
//
// CITE (methods, jar-verified this session):
//   PistonBaseBlock.checkIfExtend / getNeighborSignal (QC bud rule: neighbors of pos except pushDir,
//       DOWN of pos, and all neighbors of pos.above() except DOWN) / neighborChanged / onPlace / setPlacedBy
//   PistonBaseBlock.triggerEvent (b0=0 extend, b0=1|2 retract) / moveBlocks / isPushable
//   PistonBaseBlock TRIGGER_EXTEND=0 TRIGGER_CONTRACT=1 TRIGGER_DROP=2
//   PistonStructureResolver.resolve / addBlockLine / addBranchingBlocks / MAX_PUSH_DEPTH=12
//   MovingPistonBlock.newMovingBlockEntity
//   PistonMovingBlockEntity TICKS_TO_EXTEND=2 / tick (progress += 0.5) / finalTick
//
// SCOPE / DEFERRALS (each jar-cited):
//   - The moving-piston ANIMATION interpolation (the client-side smooth slide, getProgress lerp) is
//     cited-SIMPLIFIED to a faithful 2-tick block-state completion: the movingPistonBE ticks progress
//     0->0.5->1.0 exactly as PistonMovingBlockEntity.tick, and on completion writes the same END STATE
//     (block moved by 1, head placed/removed). The ENTITY-shove seam (moveCollidedEntities /
//     moveStuckEntities / moveEntityByPiston) IS NOW PORTED (pistonMoveCollidedEntities et al. below): a
//     player/mob/item in the swept push region is translated one block along the push axis over the
//     2-tick animation. The only shove sub-deferral is the collision SHAPE (Sulfur has no per-state
//     VoxelShape API, so the moved block is the full cube [0,1]^3 — exact for full-cube blocks + the
//     piston_head arm pistons carry) and getPistonPushReaction (all entities treated NORMAL). CITE:
//     PistonMovingBlockEntity.getProgress / moveCollidedEntities / moveStuckEntities.
//   - Slime/honey adjacent-drag in the push RESOLVER is ported (addBranchingBlocks + isSticky). The slime
//     bounce (client bob) is a cosmetic DEFERRED; the slime sideways delta-seed + honey top-drag ENTITY
//     effects ARE ported in pistonMoveCollidedEntities/pistonMoveStuckEntities. CITE: PistonStructureResolver.
//   - The client blockEntity render packet for the moving_piston (getUpdateTag) is DEFERRED (cosmetic);
//     the moving_piston BLOCK state is broadcast so the client shows the transient block. CITE:
//     PistonMovingBlockEntity.getUpdateTag.
//   - levelEvent(2001) break-particles, playSound (PISTON_EXTEND/CONTRACT), gameEvent are client
//     cosmetics, DEFERRED. CITE: PistonBaseBlock.moveBlocks/triggerEvent.

// ---------------------------------------------------------------------------------------------
// constants (PistonBaseBlock / PistonStructureResolver / PistonMovingBlockEntity)
// ---------------------------------------------------------------------------------------------

const (
	// pistonTriggerExtend / Contract / Drop are PistonBaseBlock.TRIGGER_EXTEND=0, TRIGGER_CONTRACT=1,
	// TRIGGER_DROP=2 — the b0 of the block event. CITE: PistonBaseBlock.TRIGGER_*.
	pistonTriggerExtend   = 0
	pistonTriggerContract = 1
	pistonTriggerDrop     = 2

	// pistonMaxPushDepth is PistonStructureResolver.MAX_PUSH_DEPTH — the 12-block push limit. CITE:
	// PistonStructureResolver.MAX_PUSH_DEPTH.
	pistonMaxPushDepth = 12

	// pistonTicksToExtend is PistonMovingBlockEntity.TICKS_TO_EXTEND (2) — the moving BE completes when
	// progress reaches 1.0 after two `progress += 0.5` ticks. CITE: PistonMovingBlockEntity.TICKS_TO_EXTEND.
	pistonTicksToExtend = 2
)

// pistonTickTypes is the set of block ids the piston checkIfExtend is scheduled/dispatched under (the
// piston has no scheduled tick of its own — its reaction is neighborChanged/onPlace — but the moving
// BE animation is driven by tickMovingPistons, not the scheduled-tick subsystem). This set is used only
// by tickBlock's stale guard should a future path schedule a piston tick; kept for symmetry with the
// diode/torch tick-type constants. CITE: PistonBaseBlock (minecraft:piston / minecraft:sticky_piston).
var pistonTickTypes = map[blockTickType]struct{}{
	"minecraft:piston":        {},
	"minecraft:sticky_piston": {},
}

// ---------------------------------------------------------------------------------------------
// moving-piston block-entity (PistonMovingBlockEntity) — region-owned animation state
// ---------------------------------------------------------------------------------------------

// movingPistonBE is the tick-owned PistonMovingBlockEntity animation state (redstone.go's region store):
// the moved block-state, the push direction, the extending/source flags, and the 0.0..1.0 progress the
// BE ticks over 2 ticks before finalTick completes the move. CITE: PistonMovingBlockEntity fields
// (movedState / direction / extending / isSourcePiston / progress / progressO).
type movingPistonBE struct {
	movedState     block.StateID // the block-state being carried (piston_head for the source arm)
	direction      block.Direction
	extending      bool
	isSourcePiston bool
	progress       float32
	progressO      float32
}

// ensureMovingPistons lazily builds the region's moving-piston store. Tick-owned.
func (t *TickLoop) ensureMovingPistons() map[pk.Position]*movingPistonBE {
	if t.cur().movingPistons == nil {
		t.cur().movingPistons = make(map[pk.Position]*movingPistonBE)
	}
	return t.cur().movingPistons
}

// newMovingBlockEntity is MovingPistonBlock.newMovingBlockEntity(pos, blockState, movedState, direction,
// extending, isSourcePiston): create + register the moving BE at pos (progress 0). CITE:
// MovingPistonBlock.newMovingBlockEntity / PistonMovingBlockEntity ctor.
func (t *TickLoop) newMovingBlockEntity(pos pk.Position, movedState block.StateID, direction block.Direction, extending, isSourcePiston bool) {
	t.ensureMovingPistons()[pos] = &movingPistonBE{
		movedState:     movedState,
		direction:      direction,
		extending:      extending,
		isSourcePiston: isSourcePiston,
		progress:       0,
		progressO:      0,
	}
}

// tickMovingPistons ticks every live PistonMovingBlockEntity once per tick (PistonMovingBlockEntity.tick):
//
//	entity.progressO = entity.progress;
//	if (progressO >= 1.0f) { removeBlockEntity; if (block is MOVING_PISTON) place the final movedState; }
//	else { progress += 0.5f; if (progress >= 1.0f) progress = 1.0f; }
//
// The moveCollidedEntities/moveStuckEntities entity-shove runs each tick as progress advances (see the
// f := be.progress + 0.5 seam below and pistonMoveCollidedEntities). On completion
// the moving_piston block becomes the movedState (an air-source arm becomes air; a pushed block becomes
// its moved state), then the surrounding graph is re-notified so redstone reacts to the moved block.
// Tick-owned (TICK-05); called from tickWorld after the block/fluid drains (the tickFurnaces twin).
// CITE: PistonMovingBlockEntity.tick / finalTick.
func (t *TickLoop) tickMovingPistons() {
	if t.world() == nil || t.cur().movingPistons == nil || len(t.cur().movingPistons) == 0 {
		return
	}
	// Snapshot the keys so a completion that mutates the map does not disturb this iteration; vanilla
	// ticks each block-entity once from the chunk's ticker list.
	positions := make([]pk.Position, 0, len(t.cur().movingPistons))
	for p := range t.cur().movingPistons {
		positions = append(positions, p)
	}
	for _, pos := range positions {
		be := t.cur().movingPistons[pos]
		if be == nil {
			continue
		}
		be.progressO = be.progress
		if be.progressO >= 1.0 {
			t.movingPistonComplete(pos, be)
			continue
		}
		// PistonMovingBlockEntity.tick (bytecode offsets 196-242): the NEW progress is computed as a
		// LOCAL (f = progress + 0.5f), the entity shove runs against f WHILE be.progress still holds the
		// OLD value (progressO) -- so moveCollidedEntities' `d0 = f - be.progress` == 0.5 -- and only
		// AFTER the shove is f stored back into progress (then clamped to 1.0). CITE:
		// PistonMovingBlockEntity.tick (progress+=0.5 -> moveCollidedEntities(f) -> moveStuckEntities(f)
		// -> progress = f -> clamp).
		f := be.progress + 0.5
		t.pistonMoveCollidedEntities(pos, be, f)
		t.pistonMoveStuckEntities(pos, be, f)
		be.progress = f
		if be.progress >= 1.0 {
			be.progress = 1.0
		}
	}
}

// ---------------------------------------------------------------------------------------------
// moveCollidedEntities / moveStuckEntities (PistonMovingBlockEntity) -- the ENTITY shove
// ---------------------------------------------------------------------------------------------
//
// This is the 1:1 port of PistonMovingBlockEntity.moveCollidedEntities / moveStuckEntities /
// moveEntityByPiston / fixEntityWithinPistonBase from the unobfuscated 26.2 jar (decompiled this
// session), the seam that shoves a player / mob / item out of a piston's swept push region. It runs
// from tickMovingPistons at the exact point PistonMovingBlockEntity.tick advances progress, and it is
// STRICTLY ADDITIVE: with NO entity in the swept box (getEntities returns empty) it does nothing, so a
// piston with a clear path behaves byte-identically to before and the pig-oracle (a pig nowhere near a
// piston) never enters this path.
//
// CITED DEFERRAL -- COLLISION SHAPE: vanilla builds the swept box from the moved block's
// VoxelShape.getCollisionShape (and iterates its sub-AABBs). Sulfur has no per-state VoxelShape API,
// so the moved block is approximated by the FULL-CUBE collision box [0,1]^3 (Shapes.block().bounds()),
// which is exact for the full-cube blocks + the piston_head arm that pistons overwhelmingly carry. The
// per-sub-AABB refinement loop (toAabbs) collapses to the single full-cube pass. CITE:
// BlockState.getCollisionShape / VoxelShape.toAabbs.
//
// CITED DEFERRAL -- getPistonPushReaction: base Entity.getPistonPushReaction() returns PushReaction.NORMAL
// (jar-verified); the few overrides (armor stand markers etc.) that return IGNORE are not modelled in
// Sulfur yet, so every entity is treated as NORMAL. Structured to become a real per-entity read when the
// push-reaction table lands. CITE: Entity.getPistonPushReaction.

// pistonMovementDirection is PistonMovingBlockEntity.getMovementDirection(): extending ? direction :
// direction.getOpposite(). It is the direction ENTITIES are shoved (a retract drags them the opposite
// way from a matching extend's direction field). CITE: PistonMovingBlockEntity.getMovementDirection.
func pistonMovementDirection(be *movingPistonBE) block.Direction {
	if be.extending {
		return be.direction
	}
	return dirOpposite(be.direction)
}

// pistonExtendedProgress is PistonMovingBlockEntity.getExtendedProgress(progress): extending ?
// progress-1 : 1-progress. Used by moveByPositionAndProgress to position the moved block's box at its
// CURRENT animated offset. CITE: PistonMovingBlockEntity.getExtendedProgress.
func pistonExtendedProgress(be *movingPistonBE, progress float32) float64 {
	if be.extending {
		return float64(progress) - 1.0
	}
	return 1.0 - float64(progress)
}

// pistonAABB is a plain min/max box mirroring net.minecraft.world.phys.AABB for the shove math. The
// full-cube moved box starts as {0,0,0, 1,1,1} (Shapes.block().bounds()). CITE: AABB.
type pistonAABB struct {
	minX, minY, minZ, maxX, maxY, maxZ float64
}

// pistonMoveByPositionAndProgress is PistonMovingBlockEntity.moveByPositionAndProgress(pos, box, be):
// translate the box by pos + getExtendedProgress(be.progress) * directionStep. It uses be.progress
// (the OLD/progressO value at call time), NOT the new f. CITE: PistonMovingBlockEntity.moveByPositionAndProgress.
func pistonMoveByPositionAndProgress(pos pk.Position, box pistonAABB, be *movingPistonBE) pistonAABB {
	d := pistonExtendedProgress(be, be.progress)
	sx, sy, sz := dirVec(be.direction)
	ox := float64(pos.X) + d*float64(sx)
	oy := float64(pos.Y) + d*float64(sy)
	oz := float64(pos.Z) + d*float64(sz)
	return pistonAABB{
		minX: box.minX + ox, minY: box.minY + oy, minZ: box.minZ + oz,
		maxX: box.maxX + ox, maxY: box.maxY + oy, maxZ: box.maxZ + oz,
	}
}

// pistonGetMovementArea is PistonMath.getMovementArea(box, direction, delta): the swept extension of
// box by `delta` blocks along `direction` (the leading face, extended by delta in the push sense). CITE:
// PistonMath.getMovementArea.
func pistonGetMovementArea(box pistonAABB, direction block.Direction, delta float64) pistonAABB {
	step := float64(dirAxisStep(direction))
	d := delta * step
	dMin := math.Min(d, 0.0)
	dMax := math.Max(d, 0.0)
	switch direction {
	case block.West:
		return pistonAABB{box.minX + dMin, box.minY, box.minZ, box.minX + dMax, box.maxY, box.maxZ}
	case block.East:
		return pistonAABB{box.maxX + dMin, box.minY, box.minZ, box.maxX + dMax, box.maxY, box.maxZ}
	case block.Down:
		return pistonAABB{box.minX, box.minY + dMin, box.minZ, box.maxX, box.minY + dMax, box.maxZ}
	case block.Up:
		return pistonAABB{box.minX, box.maxY + dMin, box.minZ, box.maxX, box.maxY + dMax, box.maxZ}
	case block.North:
		return pistonAABB{box.minX, box.minY, box.minZ + dMin, box.maxX, box.maxY, box.minZ + dMax}
	case block.South:
		return pistonAABB{box.minX, box.minY, box.maxZ + dMin, box.maxX, box.maxY, box.maxZ + dMax}
	default:
		return box
	}
}

// pistonMinmax is AABB.minmax(other): the union that spans both boxes (min of mins, max of maxes). CITE:
// AABB.minmax.
func pistonMinmax(a, b pistonAABB) pistonAABB {
	return pistonAABB{
		minX: math.Min(a.minX, b.minX), minY: math.Min(a.minY, b.minY), minZ: math.Min(a.minZ, b.minZ),
		maxX: math.Max(a.maxX, b.maxX), maxY: math.Max(a.maxY, b.maxY), maxZ: math.Max(a.maxZ, b.maxZ),
	}
}

// pistonAABBIntersects is AABB.intersects(other): the two boxes overlap on all three axes (strict on the
// touching face, matching AABB.intersects's `<`/`>`). CITE: AABB.intersects.
func pistonAABBIntersects(a, b pistonAABB) bool {
	return a.minX < b.maxX && a.maxX > b.minX &&
		a.minY < b.maxY && a.maxY > b.minY &&
		a.minZ < b.maxZ && a.maxZ > b.minZ
}

// pistonGetMovement is PistonMovingBlockEntity.getMovement(area, direction, entityBox): the signed
// overlap distance to push the entity out of the movement area along `direction`. CITE:
// PistonMovingBlockEntity.getMovement.
func pistonGetMovement(area pistonAABB, direction block.Direction, entityBox pistonAABB) float64 {
	switch direction {
	case block.East:
		return area.maxX - entityBox.minX
	case block.West:
		return entityBox.maxX - area.minX
	case block.Up:
		return area.maxY - entityBox.minY
	case block.Down:
		return entityBox.maxY - area.minY
	case block.South:
		return area.maxZ - entityBox.minZ
	case block.North:
		return entityBox.maxZ - area.minZ
	default: // vanilla default arm falls to the Y-up case.
		return area.maxY - entityBox.minY
	}
}

// dirAxisStep is Direction.getAxisDirection().getStep(): +1 for UP/SOUTH/EAST (positive axis), -1 for
// DOWN/NORTH/WEST (negative axis). CITE: Direction.AxisDirection.getStep.
func dirAxisStep(d block.Direction) int {
	switch d {
	case block.Up, block.South, block.East:
		return 1
	case block.Down, block.North, block.West:
		return -1
	default:
		return 1
	}
}

// pistonEntityBox is the entity's world-space AABB as a pistonAABB (feet-anchored, half-width on X/Z).
// It reads the SAME width/height Entity.AABB() reads.
func pistonEntityBox(x, y, z, width, height float64) pistonAABB {
	hw := width / 2
	return pistonAABB{minX: x - hw, minY: y, minZ: z - hw, maxX: x + hw, maxY: y + height, maxZ: z + hw}
}

// pistonMoveCollidedEntities is PistonMovingBlockEntity.moveCollidedEntities(level, pos, progress, be):
//
//	Direction movementDirection = be.getMovementDirection();
//	double d0 = progress - be.progress;                                 // == 0.5 (progress is the new f)
//	VoxelShape shape = be.getCollisionRelatedBlockState().getCollisionShape(...); if (shape.isEmpty()) return;
//	AABB movedBox = moveByPositionAndProgress(pos, shape.bounds(), be);
//	List<Entity> ents = level.getEntities(null, getMovementArea(movedBox, movementDirection, d0).minmax(movedBox));
//	if (ents.isEmpty()) return;
//	boolean slime = be.movedState.is(SLIME_BLOCK);
//	for (Entity e : ents) {
//	    if (e.getPistonPushReaction() == IGNORE) continue;
//	    if (slime) { if (e is ServerPlayer) continue; else setDeltaMovement(step in axis); }  // slime drag
//	    double d1 = 0.0;
//	    for (AABB sub : shape.toAabbs()) {
//	        AABB area = getMovementArea(moveByPositionAndProgress(pos, sub, be), movementDirection, d0);
//	        AABB ebox = e.getBoundingBox();
//	        if (!area.intersects(ebox)) continue;
//	        d1 = max(d1, getMovement(area, movementDirection, ebox));
//	        if (d1 >= d0) break;
//	    }
//	    if (d1 <= 0.0) continue;
//	    d1 = min(d1, d0) + 0.01;
//	    moveEntityByPiston(movementDirection, e, d1, movementDirection);
//	    if (!be.extending && be.isSourcePiston) fixEntityWithinPistonBase(pos, e, movementDirection, d0);
//	}
//
// Sulfur uses the single full-cube box for both `shape.bounds()` and the toAabbs iteration (the cited
// collision-shape deferral), so the inner loop runs exactly once. CITE: PistonMovingBlockEntity.moveCollidedEntities.
func (t *TickLoop) pistonMoveCollidedEntities(pos pk.Position, be *movingPistonBE, progress float32) {
	movementDirection := pistonMovementDirection(be)
	d0 := float64(progress) - float64(be.progress) // 0.5
	// getCollisionRelatedBlockState().getCollisionShape: the source head arm reports the piston_head
	// shape; every other carried block reports its own. All are approximated by the full cube; an AIR
	// moved-state (a retract pulling nothing) has an EMPTY collision shape -> return (no shove). CITE:
	// getCollisionRelatedBlockState / VoxelShape.isEmpty.
	if block.IsAir(be.movedState) {
		return
	}
	fullCube := pistonAABB{0, 0, 0, 1, 1, 1}
	movedBox := pistonMoveByPositionAndProgress(pos, fullCube, be)
	sweep := pistonMinmax(pistonGetMovementArea(movedBox, movementDirection, d0), movedBox)

	ents := t.pistonEntitiesInBox(sweep)
	if len(ents) == 0 {
		return // STRICTLY ADDITIVE: a clear path shoves nothing (getEntities empty -> return).
	}

	slime := pistonIsSlimeBlock(be.movedState)
	sx, sy, sz := dirVec(movementDirection)
	for _, ent := range ents {
		// getPistonPushReaction == IGNORE -> skip. Deferred: all Sulfur entities are NORMAL.
		if slime {
			if ent.isPlayer() {
				continue // ServerPlayer is NOT drag-accelerated by a slime block.
			}
			// setDeltaMovement to the single push-axis step (the slime sideways drag seed). Vanilla
			// switches on movementDirection.getAxis() and overwrites ONLY that axis of the delta with the
			// direction step, leaving the other two axes untouched. CITE: moveCollidedEntities slime branch.
			vx, vy, vz := ent.vel()
			switch movementDirection {
			case block.West, block.East:
				ent.setVel(float64(sx), vy, vz)
			case block.Down, block.Up:
				ent.setVel(vx, float64(sy), vz)
			case block.North, block.South:
				ent.setVel(vx, vy, float64(sz))
			}
		}
		area := pistonGetMovementArea(pistonMoveByPositionAndProgress(pos, fullCube, be), movementDirection, d0)
		ebox := ent.box()
		if !pistonAABBIntersects(area, ebox) {
			continue
		}
		d1 := math.Max(0.0, pistonGetMovement(area, movementDirection, ebox))
		if d1 <= 0.0 {
			continue
		}
		d1 = math.Min(d1, d0) + 0.01
		t.pistonMoveEntityBy(ent, movementDirection, d1)
	}
}

// pistonIsSlimeBlock is BlockState.is(Blocks.SLIME_BLOCK) for the moveCollidedEntities slime-drag gate.
// CITE: PistonMovingBlockEntity.moveCollidedEntities (be.movedState.is(SLIME_BLOCK)).
func pistonIsSlimeBlock(state block.StateID) bool {
	return block.StateList[state].ID() == "minecraft:slime_block"
}

// pistonMoveStuckEntities is PistonMovingBlockEntity.moveStuckEntities(level, pos, progress, be): the
// HONEY-block sticky-drag that carries entities standing ON TOP of a horizontally-moving honey block
// along with it.
//
//	if (!be.isStickyForEntities()) return;                                  // movedState.is(HONEY_BLOCK)
//	Direction movementDirection = be.getMovementDirection();
//	if (!movementDirection.getAxis().isHorizontal()) return;
//	double top = be.movedState.getCollisionShape(...).max(Axis.Y);
//	AABB dragBox = moveByPositionAndProgress(pos, new AABB(0, top, 0, 1, 1.5000010000000001, 1), be);
//	double d0 = progress - be.progress;                                     // 0.5
//	for (Entity e : level.getEntities(null, dragBox, matchesStickyCritera(dragBox, pos)))
//	    moveEntityByPiston(movementDirection, e, d0, movementDirection);
//
// CITE: PistonMovingBlockEntity.moveStuckEntities / isStickyForEntities / matchesStickyCritera.
func (t *TickLoop) pistonMoveStuckEntities(pos pk.Position, be *movingPistonBE, progress float32) {
	if !block.IsHoneyBlock(be.movedState) {
		return // isStickyForEntities(): only a HONEY_BLOCK drags entities standing on it.
	}
	movementDirection := pistonMovementDirection(be)
	if !(movementDirection == block.North || movementDirection == block.South ||
		movementDirection == block.West || movementDirection == block.East) {
		return // getAxis().isHorizontal(): vertical honey does not top-drag.
	}
	// top = collisionShape.max(Y) == 1.0 for the full cube (cited shape deferral).
	top := 1.0
	dragBox := pistonMoveByPositionAndProgress(pos, pistonAABB{0, top, 0, 1, 1.5000010000000001, 1}, be)
	d0 := float64(progress) - float64(be.progress) // 0.5
	for _, ent := range t.pistonEntitiesInBox(dragBox) {
		if !pistonMatchesStickyCriteria(dragBox, ent) {
			continue
		}
		t.pistonMoveEntityBy(ent, movementDirection, d0)
	}
}

// pistonMatchesStickyCritera is PistonMovingBlockEntity.matchesStickyCritera(box, entity, pos):
//
//	return e.getPistonPushReaction() == NORMAL && e.onGround()
//	    && (e.isSupportedBy(pos) || (e.getX() >= box.minX && e.getX() <= box.maxX
//	                                && e.getZ() >= box.minZ && e.getZ() <= box.maxZ));
//
// The isSupportedBy(pos) branch (Sulfur has no supporting-pos query) is DEFERRED to the AABB-center
// containment branch, which is the same predicate vanilla falls through to for an entity standing on
// the honey block. All entities are NORMAL (push-reaction deferral). CITE:
// PistonMovingBlockEntity.matchesStickyCritera.
func pistonMatchesStickyCriteria(box pistonAABB, ent pistonEntity) bool {
	if !ent.onGround() {
		return false
	}
	x, _, z := ent.pos()
	return x >= box.minX && x <= box.maxX && z >= box.minZ && z <= box.maxZ
}

// pistonMoveEntityBy is PistonMovingBlockEntity.moveEntityByPiston(pushDirection, entity, movement,
// movementDirection): translate the entity by `movement` along the push axis via a MoverType.PISTON
// move (which vanilla runs under a NOCLIP thread-local so the entity slides THROUGH the moving block).
// Sulfur has no NOCLIP-aware collision pass, so the translation is applied directly to the entity
// position (the observable "shoved by one block" result) -- a cited simplification of the noclip move.
// CITE: PistonMovingBlockEntity.moveEntityByPiston (entity.move(MoverType.PISTON, dir.step()*movement)).
func (t *TickLoop) pistonMoveEntityBy(ent pistonEntity, direction block.Direction, movement float64) {
	sx, sy, sz := dirVec(direction)
	ent.translate(t, float64(sx)*movement, float64(sy)*movement, float64(sz)*movement)
}

// ---------------------------------------------------------------------------------------------
// pistonEntity -- the uniform push handle over BOTH t.players and the entity store
// ---------------------------------------------------------------------------------------------
//
// Vanilla's Level.getEntities returns players AND non-player entities uniformly; Sulfur keeps players in
// t.players and mobs/items in the region entity store, so the shove adapts both behind this small
// interface. A player translate is an authoritative resync (ClientboundPlayerPosition, exactly the
// teleportPlayer contract); a non-player entity translate is a store move() the tracker broadcasts.

type pistonEntity interface {
	pos() (x, y, z float64)
	box() pistonAABB
	onGround() bool
	isPlayer() bool
	vel() (vx, vy, vz float64)
	setVel(vx, vy, vz float64)
	translate(t *TickLoop, dx, dy, dz float64)
}

// pistonEntitiesInBox is the Level.getEntities(null, box) analogue: every player (t.players) and every
// stored entity whose world AABB intersects `box`. It queries the entity store's per-column buckets
// around the box (never a full scan) and the global player list. CITE: Level.getEntities(Entity, AABB).
func (t *TickLoop) pistonEntitiesInBox(box pistonAABB) []pistonEntity {
	var out []pistonEntity
	// Players (t.players): the global player list; a player's box is playerWidth x playerHeight.
	for _, p := range t.players {
		if p == nil {
			continue
		}
		pb := pistonEntityBox(p.x, p.y, p.z, playerWidth, playerHeight)
		if pistonAABBIntersects(box, pb) {
			out = append(out, pistonPlayerHandle{p})
		}
	}
	// Stored entities (mobs/items): query the buckets that can hold an entity overlapping the box. The
	// bucket radius spans the box's XZ column extent (in chunks) plus 1 for entities straddling a border.
	if store := t.cur().entities; store != nil {
		cx := (box.minX + box.maxX) / 2
		cz := (box.minZ + box.maxZ) / 2
		spanX := (box.maxX - box.minX) / 2
		spanZ := (box.maxZ - box.minZ) / 2
		span := spanX
		if spanZ > span {
			span = spanZ
		}
		rangeChunks := int(span/16.0) + 1
		for _, e := range store.near(cx, cz, rangeChunks) {
			if e == nil {
				continue
			}
			eb := pistonEntityBox(e.x, e.y, e.z, e.width, e.height)
			if pistonAABBIntersects(box, eb) {
				out = append(out, pistonEntityHandle{e})
			}
		}
	}
	return out
}

// pistonPlayerHandle adapts a *tickPlayer to pistonEntity. isPlayer is true (the slime-drag ServerPlayer
// skip).
type pistonPlayerHandle struct{ p *tickPlayer }

func (h pistonPlayerHandle) pos() (float64, float64, float64) { return h.p.x, h.p.y, h.p.z }
func (h pistonPlayerHandle) box() pistonAABB {
	return pistonEntityBox(h.p.x, h.p.y, h.p.z, playerWidth, playerHeight)
}
func (h pistonPlayerHandle) onGround() bool { return h.p.onGround }
func (h pistonPlayerHandle) isPlayer() bool { return true }

// vel/setVel are unused for players (the slime-drag branch skips ServerPlayer before touching delta),
// so they are inert -- Sulfur has no tick-owned server-side player velocity to overwrite here.
func (h pistonPlayerHandle) vel() (float64, float64, float64) { return 0, 0, 0 }
func (h pistonPlayerHandle) setVel(vx, vy, vz float64)        {}
func (h pistonPlayerHandle) translate(t *TickLoop, dx, dy, dz float64) {
	// A player IS translated (position set), not merely nudged. When the player has a live connection the
	// move is an authoritative resync via teleportPlayer (ClientboundPlayerPosition, the same contract /tp
	// uses) so the client snaps to the piston-pushed position; teleportPlayer early-returns on a nil
	// client (a headless/test player), so the server-side position is set directly first to keep the
	// tick-owned position authoritative in every case.
	nx, ny, nz := h.p.x+dx, h.p.y+dy, h.p.z+dz
	if h.p.client != nil {
		t.teleportPlayer(h.p, nx, ny, nz)
		return
	}
	h.p.x, h.p.y, h.p.z = nx, ny, nz
	h.p.prevX, h.p.prevY, h.p.prevZ = nx, ny, nz
}

// pistonEntityHandle adapts a stored *Entity to pistonEntity. isPlayer is false.
type pistonEntityHandle struct{ e *Entity }

func (h pistonEntityHandle) pos() (float64, float64, float64) { return h.e.x, h.e.y, h.e.z }
func (h pistonEntityHandle) box() pistonAABB {
	return pistonEntityBox(h.e.x, h.e.y, h.e.z, h.e.width, h.e.height)
}
func (h pistonEntityHandle) onGround() bool                   { return h.e.onGround }
func (h pistonEntityHandle) isPlayer() bool                   { return false }
func (h pistonEntityHandle) vel() (float64, float64, float64) { return h.e.vx, h.e.vy, h.e.vz }
func (h pistonEntityHandle) setVel(vx, vy, vz float64)        { h.e.vx, h.e.vy, h.e.vz = vx, vy, vz }
func (h pistonEntityHandle) translate(t *TickLoop, dx, dy, dz float64) {
	// A store move() re-buckets and updates position; the entity tracker broadcasts the new position on
	// its next movement pass (the mob/item IS translated by the piston).
	t.cur().entities.move(h.e, h.e.x+dx, h.e.y+dy, h.e.z+dz)
}

// movingPistonComplete is the progressO>=1.0 branch of the STATIC PistonMovingBlockEntity.tick: remove
// the BE and, if the block at pos is still MOVING_PISTON, replace it with updateFromNeighbourShapes(movedState).
// The tick() completion NEVER consults isSourcePiston -- the source head arm carries movedState=piston_head
// and settles to it, a pushed block settles to its own moved state. (finalTick is the DIFFERENT path -- see
// finalTickMovingPistonAt, which DOES clear a source piston to AIR.) Then re-notify neighbors so redstone /
// observers react to the settled block. The updateFromNeighbourShapes()/updateOrDestroy waterlog-fix step
// is DEFERRED (no arbitrary-block shape query in v1); the settled block-state is faithful for full-cube and
// piston_head arms, which is what pistons carry. CITE: PistonMovingBlockEntity.tick (static, offsets 51-195).
func (t *TickLoop) movingPistonComplete(pos pk.Position, be *movingPistonBE) {
	delete(t.cur().movingPistons, pos)
	cur := t.redstoneBlockAt(pos)
	if !block.IsMovingPiston(cur) {
		return // the transient block was already replaced (a re-triggered piston finalTick'd it early)
	}
	final := be.movedState
	if block.IsAir(final) {
		// An air moved-state (a retract that pulled nothing) settles to air.
		if t.world().SetBlock(pos, t.airState(), dimMinY) {
			t.broadcastBlockUpdate(pos, t.airState())
		}
	} else if t.world().SetBlock(pos, final, dimMinY) {
		t.broadcastBlockUpdate(pos, final)
	}
	// level.neighborChanged(pos, newState, ...) -- wake the redstone graph + observers around the settled
	// block so a wire/torch/piston/observer next to the moved block recomputes.
	t.onRedstoneEdit(pos)
	t.onObserverEdit(pos)
}

// finalTickMovingPistonAt is PistonMovingBlockEntity.finalTick invoked out-of-band (triggerEvent's
// retract path calls finalTick on the arm/front BE to force-complete an in-flight animation before
// starting a new move). It completes the BE immediately at its current position. UNLIKE the static
// tick() completion, finalTick branches on isSourcePiston:
//
//	removeBlockEntity(pos); setRemoved();
//	if (getBlockState(pos).is(MOVING_PISTON)) {
//	    BlockState result = isSourcePiston ? AIR.defaultBlockState()
//	                                       : updateFromNeighbourShapes(movedState, level, pos);
//	    level.setBlock(pos, result, 3);
//	    level.neighborChanged(pos, result.getBlock(), ...);
//	}
//
// So a force-completed SOURCE arm (the piston_head being yanked back by a fast retract within the 2-tick
// window) clears to AIR -- NOT to its movedState (piston_head). Reusing movingPistonComplete here would
// leave a stray piston_head block. CITE: PistonMovingBlockEntity.finalTick (offsets 74-120, the
// isSourcePiston ? AIR : movedState branch). The updateFromNeighbourShapes shape-fix on the non-source
// branch is DEFERRED (same seam as movingPistonComplete).
func (t *TickLoop) finalTickMovingPistonAt(pos pk.Position) {
	if t.cur().movingPistons == nil {
		return
	}
	be := t.cur().movingPistons[pos]
	if be == nil {
		return
	}
	delete(t.cur().movingPistons, pos)
	cur := t.redstoneBlockAt(pos)
	if !block.IsMovingPiston(cur) {
		return // already replaced
	}
	// isSourcePiston ? AIR : movedState.
	final := be.movedState
	if be.isSourcePiston {
		final = t.airState()
	}
	if block.IsAir(final) {
		if t.world().SetBlock(pos, t.airState(), dimMinY) {
			t.broadcastBlockUpdate(pos, t.airState())
		}
	} else if t.world().SetBlock(pos, final, dimMinY) {
		t.broadcastBlockUpdate(pos, final)
	}
	t.onRedstoneEdit(pos)
	t.onObserverEdit(pos)
}

// ---------------------------------------------------------------------------------------------
// checkIfExtend (PistonBaseBlock.checkIfExtend) — the neighborChanged/onPlace trigger
// ---------------------------------------------------------------------------------------------

// pistonCheckIfExtend is PistonBaseBlock.checkIfExtend(level, pos, state):
//
//	Direction direction = state.getValue(FACING);
//	boolean extend = getNeighborSignal(level, pos, direction);
//	if (extend && !EXTENDED) { if (new PistonStructureResolver(...).resolve()) blockEvent(pos, this, 0, dir3D); }
//	else if (!extend && EXTENDED) { ... blockEvent(pos, this, event, dir3D); }
//
// The retract-event selection (event 1 vs 2) mirrors the vanilla check for a still-extending moving
// piston in front; with our simplified 2-tick BE, the fast-cancel (event 2) collapses to the normal
// retract (event 1) whenever no in-flight extend-arm is present, which is the common case. CITE:
// PistonBaseBlock.checkIfExtend.
func (t *TickLoop) pistonCheckIfExtend(pos pk.Position, state block.StateID) {
	facing, ok := block.PistonFacing(state)
	if !ok {
		return
	}
	extend := t.pistonGetNeighborSignal(pos, facing)
	extended := block.PistonExtended(state)
	if extend && !extended {
		r := &pistonStructureResolver{t: t, pistonPos: pos, extending: true}
		r.init(facing)
		if r.resolve() {
			t.pistonBlockEventPush(pos, pistonTriggerExtend, facing)
		}
		return
	}
	if !extend && extended {
		event := pistonTriggerContract
		// The event-2 fast-cancel path checks for an isExtending moving-piston 2 cells in front whose
		// progress < 0.5. Our BE completes in 2 ticks; if such an in-flight extend-arm exists we use
		// event 2 (drop) exactly as vanilla, else the normal retract (event 1).
		pushedPos := relativeN(pos, facing, 2)
		if pushedState := t.redstoneBlockAt(pushedPos); block.IsMovingPiston(pushedState) {
			if mf, okf := block.MovingPistonFacing(pushedState); okf && mf == facing {
				if be := t.movingPistonBEAt(pushedPos); be != nil && be.extending && be.progress < 0.5 {
					event = pistonTriggerDrop
				}
			}
		}
		t.pistonBlockEventPush(pos, event, facing)
	}
}

// movingPistonBEAt returns the moving-piston BE at pos, or nil.
func (t *TickLoop) movingPistonBEAt(pos pk.Position) *movingPistonBE {
	if t.cur().movingPistons == nil {
		return nil
	}
	return t.cur().movingPistons[pos]
}

// pistonGetNeighborSignal is PistonBaseBlock.getNeighborSignal(level, pos, pushDirection) — the
// quasi-connectivity ("bud") power read vanilla 26.2 KEEPS:
//
//	for (Direction d : Direction.values()) if (d != pushDirection && hasSignal(pos.relative(d), d)) return true;
//	if (hasSignal(pos, DOWN)) return true;
//	BlockPos above = pos.above();
//	for (Direction d : Direction.values()) if (d != DOWN && hasSignal(above.relative(d), d)) return true;
//	return false;
//
// The second loop (over the block ABOVE the piston) is the quasi-connectivity that lets a redstone
// signal reaching the block above the piston power it. Verified present in 26.2 bytecode. CITE:
// PistonBaseBlock.getNeighborSignal.
func (t *TickLoop) pistonGetNeighborSignal(pos pk.Position, pushDirection block.Direction) bool {
	for _, d := range redstoneDirs {
		if d == pushDirection {
			continue
		}
		if t.hasSignal(relative(pos, d), d) {
			return true
		}
	}
	if t.hasSignal(pos, block.Down) {
		return true
	}
	above := relative(pos, block.Up)
	for _, d := range redstoneDirs {
		if d == block.Down {
			continue
		}
		if t.hasSignal(relative(above, d), d) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------------
// block-event queue (Level.blockEvent / ServerLevel.runBlockEvents -> triggerEvent)
// ---------------------------------------------------------------------------------------------

// pistonBlockEvent is one enqueued Level.blockEvent(pos, block, b0, b1) for a piston: the piston's
// position, the trigger id (b0: 0 extend / 1 contract / 2 drop), and the facing (b1 = direction 3D
// data value; carried as the Direction). isSticky is captured so triggerEvent knows the piston kind
// even if the block state changed. CITE: ServerLevel.BlockEventData / Level.blockEvent.
type pistonBlockEvent struct {
	pos      pk.Position
	trigger  int
	facing   block.Direction
	isSticky bool
}

// pistonBlockEventPush is Level.blockEvent(pos, this, b0, direction.get3DDataValue()): enqueue a piston
// block event onto the region's ServerLevel.blockEvents queue. Drained at the end of tickWorld by
// drainPistonBlockEvents. CITE: Level.blockEvent.
func (t *TickLoop) pistonBlockEventPush(pos pk.Position, trigger int, facing block.Direction) {
	sticky := block.IsStickyPiston(t.redstoneBlockAt(pos))
	t.cur().pistonBlockEvents = append(t.cur().pistonBlockEvents, pistonBlockEvent{
		pos: pos, trigger: trigger, facing: facing, isSticky: sticky,
	})
}

// drainPistonBlockEvents is ServerLevel.runBlockEvents(): fire every queued piston block event this
// tick as triggerEvent. A block event whose piston is no longer there (state changed since the event
// was posted) fires nothing (the `getBlockState(pos).is(block)` guard inside triggerEvent). New events
// posted DURING a triggerEvent (a chain reaction) are appended and drained in the same pass, mirroring
// vanilla's single-tick drain of the blockEvents set. Tick-owned; called once per tick from tickWorld
// AFTER the scheduled block/fluid drains. CITE: ServerLevel.runBlockEvents / BlockState.triggerEvent.
func (t *TickLoop) drainPistonBlockEvents() {
	if t.world() == nil {
		return
	}
	// Process FIFO; new events append to the tail. Bounded to avoid a pathological self-retriggering
	// loop (a converging piston chain settles well within this).
	budget := 1 << 16
	for len(t.cur().pistonBlockEvents) > 0 {
		budget--
		if budget < 0 {
			return
		}
		ev := t.cur().pistonBlockEvents[0]
		t.cur().pistonBlockEvents = t.cur().pistonBlockEvents[1:]
		t.pistonTriggerEvent(ev)
	}
}

// ---------------------------------------------------------------------------------------------
// triggerEvent (PistonBaseBlock.triggerEvent) — the actual extend / retract
// ---------------------------------------------------------------------------------------------

// pistonTriggerEvent is PistonBaseBlock.triggerEvent(state, level, pos, b0, b1):
//
//	Direction direction = FACING; BlockState extendedState = state.setValue(EXTENDED, true);
//	boolean extend = getNeighborSignal(...);
//	if (extend && (b0==1||b0==2)) { setBlock(pos, extendedState, 2); return false; }   // re-powered mid-retract
//	if (!extend && b0==0) return false;                                                 // un-powered mid-extend
//	if (b0 == 0) { if (!moveBlocks(extending=true)) return; setBlock(pos, extendedState, 67); }
//	else /* 1|2 */ { finalTick front arm; place moving_piston(source head) BE; setBlock(pos, moving, ...);
//	                 if sticky pull front block via moveBlocks(extending=false) else removeBlock(front); }
//
// CITE: PistonBaseBlock.triggerEvent.
func (t *TickLoop) pistonTriggerEvent(ev pistonBlockEvent) {
	state := t.redstoneBlockAt(ev.pos)
	if !block.IsPiston(state) {
		return // triggerEvent's implicit `getBlockState(pos).is(this)` guard: stale event, fire nothing.
	}
	direction := ev.facing
	extendedState, _ := block.PistonWithExtended(state, true)
	extend := t.pistonGetNeighborSignal(ev.pos, direction)
	if extend && (ev.trigger == pistonTriggerContract || ev.trigger == pistonTriggerDrop) {
		// Re-powered before the retract executes: just re-assert EXTENDED (setBlock flag 2), no move.
		if t.world().SetBlock(ev.pos, extendedState, dimMinY) {
			t.broadcastBlockUpdate(ev.pos, extendedState)
		}
		return
	}
	if !extend && ev.trigger == pistonTriggerExtend {
		return // un-powered before the extend executes: nothing happens.
	}

	if ev.trigger == pistonTriggerExtend {
		if !t.pistonMoveBlocks(ev.pos, direction, true) {
			return
		}
		// setBlock(pos, extendedState, 67): mark the piston EXTENDED. The head arm was placed by moveBlocks.
		if t.world().SetBlock(ev.pos, extendedState, dimMinY) {
			t.broadcastBlockUpdate(ev.pos, extendedState)
		}
		t.onRedstoneEdit(ev.pos)
		t.onObserverEdit(ev.pos)
		return
	}

	// Retract (b0 == 1 or 2).
	armPos := relative(ev.pos, direction)
	// finalTick any in-flight extend animation on the arm cell.
	t.finalTickMovingPistonAt(armPos)
	// setBlock(pos, movingPistonState, 276) + the source-piston moving BE: the piston base itself becomes
	// a moving_piston carrying the retracted piston (its defaultBlockState with FACING) as the source.
	pistonType := block.PistonTypeNormal
	if ev.isSticky {
		pistonType = block.PistonTypeSticky
	}
	movingState, okm := block.MovingPistonState(direction, pistonType)
	// The moved (source) state the base carries: the piston's own default (un-extended) block.
	baseDefault, _ := block.PistonWithExtended(state, false)
	if okm && t.world().SetBlock(ev.pos, movingState, dimMinY) {
		t.broadcastBlockUpdate(ev.pos, movingState)
		t.newMovingBlockEntity(ev.pos, baseDefault, direction, false /*extending*/, true /*source*/)
		t.onRedstoneEdit(ev.pos)
	}

	if ev.isSticky {
		// Sticky: pull the block 2 cells in front (pos + 2*dir) back toward the piston via
		// moveBlocks(extending=false), unless that cell holds an in-flight extend arm (pistonPiece).
		twoPos := relativeN(ev.pos, direction, 2)
		movingState2 := t.redstoneBlockAt(twoPos)
		pistonPiece := false
		if block.IsMovingPiston(movingState2) {
			if be := t.movingPistonBEAt(twoPos); be != nil && be.direction == direction && be.extending {
				t.finalTickMovingPistonAt(twoPos)
				pistonPiece = true
			}
		}
		if !pistonPiece {
			pushable := !block.IsAir(movingState2) &&
				pistonIsPushable(movingState2, twoPos, dirOpposite(direction), false, direction, t) &&
				(block.PistonPushReaction(movingState2) == block.PushReactionNormal ||
					block.IsPiston(movingState2))
			if ev.trigger == pistonTriggerContract && pushable {
				t.pistonMoveBlocks(ev.pos, direction, false)
			} else {
				// removeBlock(pos.relative(direction)): clear the head arm cell (the sticky head retracts
				// with nothing to pull).
				t.pistonRemoveHead(relative(ev.pos, direction))
			}
		}
	} else {
		// Non-sticky: just remove the head arm.
		t.pistonRemoveHead(relative(ev.pos, direction))
	}
}

// pistonRemoveHead is Level.removeBlock(pos.relative(direction), false) for the head-arm cell during
// retract: clear the piston_head (or its moving stand-in) so the arm vanishes. CITE:
// PistonBaseBlock.triggerEvent (level.removeBlock(pos.relative(direction), false)).
func (t *TickLoop) pistonRemoveHead(pos pk.Position) {
	cur := t.redstoneBlockAt(pos)
	if block.IsPistonHead(cur) || block.IsMovingPiston(cur) {
		if t.world().SetBlock(pos, t.airState(), dimMinY) {
			t.broadcastBlockUpdate(pos, t.airState())
		}
		if block.IsMovingPiston(cur) && t.cur().movingPistons != nil {
			delete(t.cur().movingPistons, pos)
		}
		t.onRedstoneEdit(pos)
		t.onObserverEdit(pos)
	}
}

// ---------------------------------------------------------------------------------------------
// moveBlocks (PistonBaseBlock.moveBlocks) — the actual block shove
// ---------------------------------------------------------------------------------------------

// pistonMoveBlocks is PistonBaseBlock.moveBlocks(level, pistonPos, direction, extending): resolve the
// push list, then move every pushed block by 1 in the push direction (placing a moving_piston + BE at
// the DESTINATION and clearing the SOURCE), destroy every to-destroy block, and (when extending) place
// the head arm. Returns false if the resolver fails (the push is blocked / exceeds 12). The moving
// animation is cited-simplified: the destination gets a moving_piston block whose BE completes to the
// moved state in 2 ticks; the observable END STATE is identical to vanilla. CITE:
// PistonBaseBlock.moveBlocks.
func (t *TickLoop) pistonMoveBlocks(pistonPos pk.Position, direction block.Direction, extending bool) bool {
	armPos := relative(pistonPos, direction)
	if !extending {
		// A retract first clears a leftover piston_head at the arm cell (setBlock air, flag 276).
		if block.IsPistonHead(t.redstoneBlockAt(armPos)) {
			if t.world().SetBlock(armPos, t.airState(), dimMinY) {
				t.broadcastBlockUpdate(armPos, t.airState())
			}
		}
	}
	r := &pistonStructureResolver{t: t, pistonPos: pistonPos, extending: extending}
	r.init(direction)
	if !r.resolve() {
		return false
	}
	sticky := block.IsStickyPiston(t.redstoneBlockAt(pistonPos))
	pistonType := block.PistonTypeNormal
	if sticky {
		pistonType = block.PistonTypeSticky
	}
	pushDirection := direction
	if !extending {
		pushDirection = dirOpposite(direction)
	}

	toPush := r.toPush
	toDestroy := r.toDestroy

	// deleteAfterMove starts as every source cell of a pushed block; cells that become a move DESTINATION
	// are removed from it, leaving only the cells that must be set to air (the tails of each moved line).
	// CITE: PistonBaseBlock.moveBlocks (Map<BlockPos,BlockState> deleteAfterMove).
	deleteAfterMove := make(map[pk.Position]bool, len(toPush))
	pushedStates := make([]block.StateID, len(toPush))
	for i, p := range toPush {
		pushedStates[i] = t.redstoneBlockAt(p)
		deleteAfterMove[p] = true
	}

	// Destroy pass (reverse order): drop + set air for each to-destroy block. CITE: moveBlocks toDestroy loop.
	for i := len(toDestroy) - 1; i >= 0; i-- {
		p := toDestroy[i]
		st := t.redstoneBlockAt(p)
		t.spawnBlockDrop(nil, p, st)
		if t.world().SetBlock(p, t.airState(), dimMinY) {
			t.broadcastBlockUpdate(p, t.airState())
		}
	}

	// Push pass (reverse order): each pushed block's DESTINATION cell (p + pushDir) becomes a moving_piston
	// carrying the pushed block's state. CITE: moveBlocks toPush loop.
	for i := len(toPush) - 1; i >= 0; i-- {
		src := toPush[i]
		dst := relative(src, pushDirection)
		delete(deleteAfterMove, dst) // a destination is not a to-air tail
		movingState, ok := block.MovingPistonState(direction, block.PistonTypeNormal)
		if !ok {
			continue
		}
		if t.world().SetBlock(dst, movingState, dimMinY) {
			t.broadcastBlockUpdate(dst, movingState)
		}
		t.newMovingBlockEntity(dst, pushedStates[i], direction, extending, false /*not source*/)
	}

	// Extending: place the head arm as a moving_piston carrying the piston_head (source). CITE: moveBlocks
	// extending branch (armPos <- moving_piston BE(head, isSource=true)).
	if extending {
		headState, okh := block.PistonHeadState(direction, pistonType, false)
		movingArm, okm := block.MovingPistonState(direction, pistonType)
		if okh && okm {
			delete(deleteAfterMove, armPos)
			if t.world().SetBlock(armPos, movingArm, dimMinY) {
				t.broadcastBlockUpdate(armPos, movingArm)
			}
			t.newMovingBlockEntity(armPos, headState, direction, true /*extending*/, true /*source*/)
		}
	}

	// Every remaining source cell (a moved-line tail) is set to air. CITE: moveBlocks deleteAfterMove loop.
	for p := range deleteAfterMove {
		if t.world().SetBlock(p, t.airState(), dimMinY) {
			t.broadcastBlockUpdate(p, t.airState())
		}
	}

	// updateNeighborsAt for each destroyed/pushed cell + the arm — wake the redstone graph + observers
	// around every touched cell. CITE: moveBlocks trailing updateNeighborsAt loops.
	for _, p := range toDestroy {
		t.onRedstoneEdit(p)
		t.onObserverEdit(p)
	}
	for _, p := range toPush {
		t.onRedstoneEdit(p)
		t.onObserverEdit(p)
		dst := relative(p, pushDirection)
		t.onRedstoneEdit(dst)
		t.onObserverEdit(dst)
	}
	if extending {
		t.onRedstoneEdit(armPos)
		t.onObserverEdit(armPos)
	}
	return true
}

// ---------------------------------------------------------------------------------------------
// isPushable (PistonBaseBlock.isPushable)
// ---------------------------------------------------------------------------------------------

// pistonIsPushable is PistonBaseBlock.isPushable(state, level, pos, direction, allowDestroyable,
// connectionDirection):
//
//	if (out of world / border) return false;
//	if (isAir) return true;
//	if (obsidian|crying_obsidian|respawn_anchor|reinforced_deepslate) return false;
//	if (direction==DOWN && y==minY) return false;
//	if (direction==UP   && y==maxY) return false;
//	if (piston|sticky_piston) { if (EXTENDED) return false; }
//	else { if (destroySpeed == -1) return false;
//	       switch (pushReaction) { BLOCK -> false; DESTROY -> allowDestroyable; PUSH_ONLY -> dir==connectionDirection; } }
//	return !hasBlockEntity();
//
// CITE: PistonBaseBlock.isPushable. hasBlockEntity is approximated by "is one of the block-entity blocks
// Sulfur tracks" — for the piston push scope the relevant BE blocks are chests/furnaces/etc, which are
// not pushable; a plain block has no BE. The world-border check is DEFERRED (no world border in v1; the
// isWithinBounds always-true). CITE header notes.
func pistonIsPushable(state block.StateID, pos pk.Position, direction block.Direction, allowDestroyable bool, connectionDirection block.Direction, t *TickLoop) bool {
	if pos.Y < dimMinY || pos.Y > dimMaxY {
		return false
	}
	if block.IsAir(state) {
		return true
	}
	if pistonIsImmovableSpecial(state) {
		return false
	}
	if direction == block.Down && pos.Y == dimMinY {
		return false
	}
	if direction == block.Up && pos.Y == dimMaxY {
		return false
	}
	if block.IsPiston(state) {
		if block.PistonExtended(state) {
			return false
		}
		return true // a non-extended piston has no relevant BE; pushable.
	}
	// destroySpeed == -1 (unbreakable: bedrock, barrier) -> not pushable. CITE: state.getDestroySpeed == -1.
	if id := block.StateList[state].ID(); block.Hardness[id].DestroySpeed == -1.0 {
		return false
	}
	switch block.PistonPushReaction(state) {
	case block.PushReactionBlock:
		return false
	case block.PushReactionDestroy:
		return allowDestroyable
	case block.PushReactionPushOnly:
		return direction == connectionDirection
	}
	// NORMAL: pushable iff no block-entity. Sulfur's BE-carrying blocks (chest/furnace/etc) are not
	// pushable; a plain block is. CITE: return !state.hasBlockEntity().
	return !pistonHasBlockEntity(state)
}

// pistonIsImmovableSpecial is the obsidian family hard-block list from isPushable: obsidian,
// crying_obsidian, respawn_anchor, reinforced_deepslate. CITE: PistonBaseBlock.isPushable.
func pistonIsImmovableSpecial(state block.StateID) bool {
	switch block.StateList[state].ID() {
	case "minecraft:obsidian", "minecraft:crying_obsidian",
		"minecraft:respawn_anchor", "minecraft:reinforced_deepslate":
		return true
	default:
		return false
	}
}

// pistonHasBlockEntity approximates BlockState.hasBlockEntity() for the isPushable NORMAL tail: a block
// that carries a block-entity is NOT pushable. Sulfur has no per-state hasBlockEntity flag baked yet, so
// this checks the block-entity-bearing block ids in the piston push scope (containers/mechanisms). A
// block absent from this set has no BE and is pushable. CITE: BlockState.hasBlockEntity (structured to
// become a baked per-state read when the block-entity flag table lands).
func pistonHasBlockEntity(state block.StateID) bool {
	switch block.StateList[state].ID() {
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:ender_chest",
		"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:brewing_stand", "minecraft:hopper", "minecraft:dropper", "minecraft:dispenser",
		"minecraft:barrel", "minecraft:beacon", "minecraft:comparator", "minecraft:moving_piston",
		"minecraft:jukebox", "minecraft:lectern", "minecraft:bell", "minecraft:conduit",
		"minecraft:enchanting_table", "minecraft:sign", "minecraft:campfire", "minecraft:soul_campfire":
		return true
	default:
		return false
	}
}

// dimMaxY is the dimension max build height (overworld -64..319). Used by isPushable's DOWN/UP edge
// checks. CITE: Level.getMaxY.
const dimMaxY = 319

// relativeN offsets pos by n steps in direction d (BlockPos.relative(direction, n)). CITE:
// BlockPos.relative(Direction, int).
func relativeN(p pk.Position, d block.Direction, n int) pk.Position {
	dx, dy, dz := dirVec(d)
	return pk.Position{X: p.X + dx*n, Y: p.Y + dy*n, Z: p.Z + dz*n}
}
