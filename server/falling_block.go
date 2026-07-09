package server

// falling_block.go — FALLING BLOCKS: a 1:1 port of net.minecraft.world.entity.item.FallingBlockEntity
// (the falling-block entity) + the net.minecraft.world.level.block.FallingBlock trigger seams
// (onPlace / updateShape schedule + FallingBlock.tick spawn), decompiled from temp/cache/26.2-inner.jar
// (javap -c -p this session). The FallingBlock family in scope here is sand / red_sand / gravel — the
// three FallingBlock subclasses that are plain gravity blocks (SAND/RED_SAND are SandBlock, GRAVEL is
// ColoredFallingBlock, all extend FallingBlock). anvil (AnvilBlock, hurts on land) and concrete_powder
// (ConcretePowderBlock, solidifies in water) are FallingBlock subclasses with extra behavior and are
// deferred follow-ups; suspicious_sand is a BrushableBlock (NOT a FallingBlock) and is excluded.
//
// A FallingBlockEntity is a NON-mob moving Entity (sibling of PrimedTnt / item drop / arrow): no AI — its
// whole behavior is the gravity+drag fall plus, at rest, either writing the carried block state back into
// the landing cell (setBlock) or, when the cell will not accept it, dropping the block as an item. It runs
// its OWN full physics in tickFallingBlocks (like tickPrimedTnt) and is therefore skipped by the generic
// MOB-ONLY physics pass (tick_phases.go non-mob skip list: "|| e.isFalling").
//
// TRIGGER (verified javap FallingBlock): onPlace/updateShape -> scheduleTick(pos, this,
// getDelayAfterPlace()==2); tick: if (isFree(getBlockState(pos.below())) && pos.getY() >= getMinY())
// FallingBlockEntity.fall(level, pos, state). isFree(state): isAir || is(FIRE) || liquid || canBeReplaced.
//
// ENTITY (verified javap FallingBlockEntity): fall -> new FallingBlockEntity(x+0.5,y,z+0.5,state);
// setBlock(pos, fluid.createLegacyBlock()==air, 3); addFreshEntity. getDefaultGravity 0.04; getAirDrag
// 0.98. tick: air-guard -> time++ -> applyGravity -> move(SELF) -> (server) if(!onGround) despawn>600 or
// >100 out-of-bounds -> drop+discard; else multiply(0.7,-0.5,0.7); if canBeReplaced(landOn) && canSurvive
// && !isFree(below) -> setBlock(state,3)+discard else drop+discard; finally scale(0.98).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// FallingBlock constants (verified javap — exact values).
const (
	// fallingBlockGravity is FallingBlockEntity.getDefaultGravity() (ldc2_w 0.04d).
	fallingBlockGravity = 0.04
	// fallingBlockAirDrag is Entity.getAirDrag() (0.98f). deltaMovement.scale(0.98) at the END of a tick.
	fallingBlockAirDrag = 0.98
	// fallingBlockDelayAfterPlace is FallingBlock.getDelayAfterPlace() (iconst_2 -> 2).
	fallingBlockDelayAfterPlace = 2
	// fallingBlockDespawnAgeLong is the 600-tick unconditional despawn (sipush 600: time > 600).
	fallingBlockDespawnAgeLong = 600
	// fallingBlockDespawnAgeShort is the 100-tick out-of-bounds despawn (bipush 100).
	fallingBlockDespawnAgeShort = 100
)

// fallingBlockEntityDrops mirrors GameRules.ENTITY_DROPS (default true): every drop-as-item gates on it.
var fallingBlockEntityDrops = true

// isFallingBlockKind reports whether a state is one of the in-scope FallingBlock kinds — sand, red_sand,
// or gravel. This is the ServerLevel.tickBlock "state.is(block)" stale-guard membership AND the trigger
// gate. It EXCLUDES suspicious_sand (a BrushableBlock, not a FallingBlock) even though the #minecraft:sand
// tag (block.IsSand) includes it — the falling behavior is FallingBlock-class-scoped, not tag-scoped.
// CITE: Blocks.SAND/RED_SAND (SandBlock) + Blocks.GRAVEL (ColoredFallingBlock), all extend FallingBlock.
func isFallingBlockKind(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	switch block.StateList[s].(type) {
	case block.Sand, block.RedSand, block.Gravel:
		return true
	default:
		return false
	}
}

// fallingBlockTickType maps a FallingBlock state to the block-tick type it schedules under (its own block
// id). Returns ("", false) for a non-FallingBlock state. CITE: FallingBlock.onPlace scheduleTick(pos,
// this, ...) — "this" is the block singleton, keyed by its id.
func fallingBlockTickType(s block.StateID) (blockTickType, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return "", false
	}
	switch block.StateList[s].(type) {
	case block.Sand:
		return sandTickType, true
	case block.RedSand:
		return redSandTickType, true
	case block.Gravel:
		return gravelTickType, true
	default:
		return "", false
	}
}

// fallingBlockIsFree is the port of FallingBlock.isFree(BlockState): "state.isAir() ||
// state.is(BlockTags.FIRE) || state.liquid() || state.canBeReplaced()". isReplaceableState (block_place.go)
// models air + water + lava (the v1 replaceable set), covering the isAir + liquid + canBeReplaced arms;
// the FIRE tag arm is folded in explicitly for fidelity. Kept as a distinct function so the vanilla
// four-arm structure is legible.
//
//	[VERIFIED javap FallingBlock.isFree: isAir || is(FIRE) || liquid || canBeReplaced.]
func fallingBlockIsFree(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	// state.is(BlockTags.FIRE): fire / soul_fire.
	switch block.StateList[s].(type) {
	case block.Fire, block.SoulFire:
		return true
	}
	// state.isAir() || state.liquid() || state.canBeReplaced(): air/water/lava in the v1 world.
	return isReplaceableState(s)
}

// onFallingBlockEdit is the FALLING-BLOCK slice of Block.onPlace + Level.updateNeighborsAt: after a cell
// at "pos" changes, (1) if the cell at "pos" itself is now a FallingBlock, schedule its fall tick
// (FallingBlock.onPlace); and (2) if the cell ABOVE "pos" is a FallingBlock whose floor just changed,
// schedule ITS fall tick (FallingBlock.updateShape reacting to the neighbor-below change — the case that
// makes a pillar of sand collapse when its support is broken). Both schedule a 2-tick tick under the
// block's own id; fallingBlockTick spawns the entity if the cell below is free. Mirrors sugar_cane.go's
// onBlockTickEdit seam. CITE: FallingBlock.onPlace / updateShape -> scheduleTick(pos, this, 2).
func (t *TickLoop) onFallingBlockEdit(pos pk.Position) {
	if t.world() == nil {
		return
	}
	if state, ok := t.world().GetBlock(pos, dimMinY); ok && isFallingBlockKind(state) {
		t.scheduleFallingBlockTick(pos, state)
	}
	abovePos := above(pos)
	if aboveState, ok := t.world().GetBlock(abovePos, dimMinY); ok && isFallingBlockKind(aboveState) {
		t.scheduleFallingBlockTick(abovePos, aboveState)
	}
}

// scheduleFallingBlockTick schedules the FallingBlock.tick 2 ticks out under the block's own id, dedup'd.
// CITE: FallingBlock.onPlace/updateShape -> scheduleTick(pos, this, getDelayAfterPlace()).
func (t *TickLoop) scheduleFallingBlockTick(pos pk.Position, state block.StateID) {
	typ, ok := fallingBlockTickType(state)
	if !ok {
		return
	}
	if t.hasScheduledBlockTick(pos, typ) {
		return
	}
	t.scheduleBlockTick(pos, typ, fallingBlockDelayAfterPlace)
}

// fallingBlockTick is the port of FallingBlock.tick(state, serverLevel, pos, random): if the cell below is
// free AND we are at/above the world floor, spawn the FallingBlockEntity (fall). A supported FallingBlock
// (below not free) does nothing. Dispatched from tickBlock (block_ticks.go) after the ServerLevel.tickBlock
// "state.is(block)" stale guard.
//
//	[VERIFIED javap FallingBlock.tick: if (isFree(getBlockState(pos.below())) && pos.getY() >= getMinY())
//	 FallingBlockEntity.fall(level, pos, state).]
func (t *TickLoop) fallingBlockTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	belowState, ok := t.world().GetBlock(below(pos), dimMinY)
	if !ok {
		return // unloaded floor: cannot safely spawn/remove — treat as not free (no fall)
	}
	if !fallingBlockIsFree(belowState) {
		return // supported: FallingBlock.tick returns, no entity spawned
	}
	if pos.Y < dimMinY {
		return // pos.getY() < getMinY()
	}
	t.spawnFallingBlock(pos, state)
}

// spawnFallingBlock is the port of FallingBlockEntity.fall(Level, BlockPos, BlockState): create the entity
// CENTERED at (x+0.5, y, z+0.5) carrying "state", remove the source block (setBlock to the fluid legacy
// block == air for a dry cell), and add the entity to the owning region's store (the tracker broadcasts
// AddEntity next tick). WATERLOGGED->false is a no-op for sand/gravel. Returns the entity.
//
//	[VERIFIED javap FallingBlockEntity.fall: new FallingBlockEntity(level, x+0.5, y, z+0.5, state);
//	 level.setBlock(pos, state.getFluidState().createLegacyBlock(), 3); level.addFreshEntity(e).]
func (t *TickLoop) spawnFallingBlock(pos pk.Position, state block.StateID) *Entity {
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y)
	cz := float64(pos.Z) + 0.5

	e := NewEntity(t.idAlloc.AllocID(), entity.FallingBlock, cx, cy, cz)
	e.isFalling = true
	e.fallingBlockState = state
	e.fallingDropItem = true // FallingBlockEntity.dropItem default true
	e.fallingTime = 0        // FallingBlockEntity.time default 0
	// setDeltaMovement(Vec3.ZERO): starts at rest; NewEntity leaves vx/vy/vz zero.

	air := t.airState()
	owner := t.regionForColumn(columnOf(cx, cz))
	t.withRegion(owner, func() {
		if t.world().SetBlock(pos, air, dimMinY) {
			t.broadcastBlockUpdate(pos, air)
		}
		t.cur().entities.add(e) // store insert -> the tracker broadcasts AddEntity next tick
	})
	return e
}

// tickFallingBlocks drives every FallingBlockEntity in every region, the sibling of tickPrimedTnt /
// tickItems / tickArrows. Runs on the coordinator; processes each region WITH that region registered
// (withRegion) so t.cur() (the moveEntity re-bucket + the discard remove + the land setBlock) resolves to
// the entity's OWN store. A per-region snapshot keeps the loop stable across an in-loop discard.
func (t *TickLoop) tickFallingBlocks() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, r.entities.len())
		for _, e := range r.entities.all() {
			if e.isFalling {
				snapshot = append(snapshot, e)
			}
		}
		if len(snapshot) == 0 {
			continue
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickOneFallingBlock(e)
			}
		})
	}
}

// tickOneFallingBlock is the port of FallingBlockEntity.tick for one falling-block entity. The vanilla
// ORDER is preserved exactly: air-guard -> time++ -> applyGravity -> move -> (server) land-or-break /
// despawn -> air-drag scale (0.98). The concrete water-clip branch, WATERLOGGED-in-water re-state, the
// block-entity blockData restore, Fallable.onLand (empty for sand/gravel), and callOnBrokenAfterFall
// (empty for sand/gravel) are cite-deferred — none apply to sand/red_sand/gravel.
//
//	[VERIFIED javap FallingBlockEntity.tick — see file header for the full decompiled call chain.]
func (t *TickLoop) tickOneFallingBlock(e *Entity) {
	// if (blockState.isAir()) { discard(); return; }
	if block.IsAir(e.fallingBlockState) {
		t.cur().entities.remove(e.id)
		return
	}

	// time++.
	e.fallingTime++

	// applyGravity(): deltaMovement.y -= getDefaultGravity() (0.04).
	e.vy -= fallingBlockGravity

	// move(SELF, deltaMovement): per-axis swept resolver; sets onGround / zeroes blocked velocity.
	t.moveEntity(e, e.vx, e.vy, e.vz)

	// applyEffectsFromBlocks() / handlePortal(): cite-deferred.

	// blockPosition() == the integer block the entity's feet occupy.
	p := blockPosOfEntity(e)

	if !e.onGround {
		// !onGround (and !inWater — concrete deferred): still falling. Only the despawn guards apply:
		// "if (time > 100 && (y < minY || y > maxY) || time > 600)".
		outOfBounds := e.fallingTime > fallingBlockDespawnAgeShort && (p.Y < dimMinY || p.Y > dimMaxY)
		if outOfBounds || e.fallingTime > fallingBlockDespawnAgeLong {
			if e.fallingDropItem && fallingBlockEntityDrops {
				t.spawnFallingBlockItem(e, p)
			}
			t.cur().entities.remove(e.id) // discard()
			return                        // discarded; the trailing scale runs on a live entity only
		}
	} else {
		// onGround: setDeltaMovement(deltaMovement.multiply(0.7, -0.5, 0.7)).
		e.vx *= 0.7
		e.vy *= -0.5
		e.vz *= 0.7

		landState, _ := t.world().GetBlock(p, dimMinY)
		// if (!landOn.is(MOVING_PISTON)) — not a v1 block, always true. !cancelDrop — default false.

		// canBeReplaced (held-item guard EMPTY -> landOn.canBeReplaced()).
		canReplace := isReplaceableState(landState)

		// freeBelow == FallingBlock.isFree(getBlockState(p.below())) && (!isConcrete || !inWater).
		belowLand, okBelow := t.world().GetBlock(below(p), dimMinY)
		freeBelow := okBelow && fallingBlockIsFree(belowLand)

		// canSurvive: sand/gravel add no constraint (base canSurvive == true). canPlace = canSurvive && !freeBelow.
		canPlace := !freeBelow

		if canReplace && canPlace {
			// level.setBlock(p, blockState, 3): write the carried state. On success, broadcast + discard.
			if t.world().SetBlock(p, e.fallingBlockState, dimMinY) {
				t.broadcastBlockUpdate(p, e.fallingBlockState)
				t.cur().entities.remove(e.id) // discard()
				// A landed FallingBlock is a support edit for the cell above — wake any FallingBlock stacked
				// on top so a pillar collapses one layer at a time (the onPlace half of the loop).
				t.onFallingBlockEdit(p)
				return
			}
			t.cur().entities.remove(e.id) // discard()
			if e.fallingDropItem && fallingBlockEntityDrops {
				t.spawnFallingBlockItem(e, p)
			}
			return
		}

		// Can't place: break into an item.
		t.cur().entities.remove(e.id) // discard()
		if e.fallingDropItem && fallingBlockEntityDrops {
			t.spawnFallingBlockItem(e, p)
		}
		return
	}

	// setDeltaMovement(deltaMovement.scale(getAirDrag())): 0.98 on all three axes, at the END of the tick.
	e.vx *= fallingBlockAirDrag
	e.vy *= fallingBlockAirDrag
	e.vz *= fallingBlockAirDrag
}

// spawnFallingBlockItem is the port of FallingBlockEntity.spawnAtLocation(block): drop ONE item of the
// carried block's item form at the entity's position (spawnAtLocation offset 0.0 -> at getX/getY/getZ),
// with the ItemEntity ctor's random toss velocity. For sand/red_sand/gravel the item name == the block
// name. Inserted into the owning store; the tracker broadcasts it. CITE: FallingBlockEntity.tick ->
// spawnAtLocation(block) -> spawnAtLocation(new ItemStack(block), 0.0F).
func (t *TickLoop) spawnFallingBlockItem(e *Entity, p pk.Position) {
	name := fallingBlockItemName(e.fallingBlockState)
	if name == "" {
		return // no item form (never for sand/red_sand/gravel)
	}
	id := itemNameToID(name)
	if id == 0 {
		return // unresolved item id: drop nothing
	}
	stack := component.SlotData{Count: 1, ItemID: pk.VarInt(id)}
	ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y, e.z, stack)
	t.cur().entities.add(ie)
}

// fallingBlockItemName maps a FallingBlock state to the bare registry name of its item form. Returns ""
// for a non-FallingBlock state. CITE: Block.asItem (sand/red_sand/gravel -> the same-named item).
func fallingBlockItemName(s block.StateID) string {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return ""
	}
	switch block.StateList[s].(type) {
	case block.Sand:
		return "sand"
	case block.RedSand:
		return "red_sand"
	case block.Gravel:
		return "gravel"
	default:
		return ""
	}
}

// blockPosOfEntity is Entity.blockPosition() for a non-player entity: the integer block the entity's feet
// occupy (int(math.Floor) of x/y/z). CITE: Entity.blockPosition (BlockPos.containing(x,y,z)).
func blockPosOfEntity(e *Entity) pk.Position {
	return pk.Position{
		X: int(math.Floor(e.x)),
		Y: int(math.Floor(e.y)),
		Z: int(math.Floor(e.z)),
	}
}
