package server

// shulker_box_be.go -- the SHULKER BOX block-entity: the tick-owned 27-slot container + the lid-open
// animation + the AABB push (opening a shulker box shoves entities out of the half-block it expands
// into) + the openCount opener animation + the WorldlyContainer nesting-reject. A 1:1 port of the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session).
//
// 1:1 ANCHORS (VERIFIED javap this session):
//
//	net.minecraft.world.level.block.entity.ShulkerBoxBlockEntity:
//	    CONTAINER_SIZE=27; OPENING_TICK_LENGTH=10; MAX_LID_HEIGHT=0.5f; EVENT_SET_OPEN_COUNT=1;
//	    fields: itemStacks(27), openCount(0), animationStatus(CLOSED), progress(0), progressOld(0), color.
//	    AnimationStatus ordinals: CLOSED=0 OPENING=1 OPENED=2 CLOSING=3.
//	    updateAnimation(level,pos,state): progressOld=progress; switch(status):
//	        CLOSED  -> progress=0.
//	        OPENING -> progress+=0.1f; if(progressOld==0) doNeighborUpdates;
//	                   if(progress>=1){ status=OPENED; progress=1; doNeighborUpdates; }
//	                   moveCollidedEntities(level,pos,state);
//	        OPENED  -> progress=1.
//	        CLOSING -> progress-=0.1f; if(progressOld==1) doNeighborUpdates;
//	                   if(progress<=0){ status=CLOSED; progress=0; doNeighborUpdates; }
//	    getProgress(partial)=Mth.lerp(partial, progressOld, progress).
//	    triggerEvent(1,type): openCount=type; type==0 -> CLOSING; type==1 -> OPENING.
//	    startOpen: if(openCount<0)openCount=0; openCount++; blockEvent(pos,block,1,openCount);
//	               if(openCount==1){ gameEvent(CONTAINER_OPEN); playSound(SHULKER_BOX_OPEN,BLOCKS,0.5,
//	               random.nextFloat()*0.1+0.9); }
//	    stopOpen: openCount--; blockEvent(pos,block,1,openCount);
//	              if(openCount<=0){ gameEvent(CONTAINER_CLOSE); playSound(SHULKER_BOX_CLOSE,...); }
//	    getSlotsForFace=SLOTS(0..26); canPlaceItemThroughFace= !(Block.byItem(item) instanceof ShulkerBoxBlock);
//	    canTakeItemThroughFace=true.
//	    moveCollidedEntities(level,pos,state): dir=FACING; box=Shulker.getProgressDeltaAabb(1,dir,
//	        progressOld,progress, atBottomCenterOf(pos)); for each entity in box: if
//	        getPistonPushReaction()==IGNORE skip; else e.move(SHULKER_BOX, ((box.xsize+0.01)*stepX,
//	        (box.ysize+0.01)*stepY, (box.zsize+0.01)*stepZ)).
//	net.minecraft.world.entity.monster.Shulker.getProgressDeltaAabb(f,dir,g,h,offset):
//	    base=AABB(-f*0.5,0,-f*0.5, f,f*0.5,f); max=Math.max(g,h); min=Math.min(g,h);
//	    box=base.expandTowards(stepX*max*f, stepY*max*f, stepZ*max*f)
//	           .expandTowards(-stepX*(1+min)*f, -stepY*(1+min)*f, -stepZ*(1+min)*f).move(offset).
//
// The lid block-event (ClientboundBlockEvent lid render sync) is a CLIENT COSMETIC cue: the codebase has
// no block-registry-id wire path and consistently applies such block-events server-side directly (the bell
// BE precedent, bell_be.go). We apply the openCount/animationStatus transitions to server state directly
// (which drives getProgress + the push AABB, the observable gameplay); the client-render block-event is a
// cited deferral. The OPEN/CLOSE sounds ARE played (playSound has a real positional broadcast).

import (
	"math/rand/v2"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// shulkerContainerSize is ShulkerBoxBlockEntity.CONTAINER_SIZE (27 = 3 rows x 9). CITE.
const shulkerContainerSize = 27

// shulkerBoxOpenSoundID / shulkerBoxCloseSoundID are SoundEvents.SHULKER_BOX_OPEN / _CLOSE
// (data/soundid/soundid.go: 1465 block.shulker_box.open, 1464 block.shulker_box.close). Played on
// SoundSource.BLOCKS. CITE ShulkerBoxBlockEntity.startOpen/stopOpen.
const (
	shulkerBoxOpenSoundID  int32 = 1465
	shulkerBoxCloseSoundID int32 = 1464
)

// shulkerAnimStatus mirrors ShulkerBoxBlockEntity.AnimationStatus (ordinals CLOSED=0 OPENING=1 OPENED=2
// CLOSING=3 -- the tableswitch order in updateAnimation). CITE.
type shulkerAnimStatus int

const (
	shulkerClosed  shulkerAnimStatus = iota // 0
	shulkerOpening                          // 1
	shulkerOpened                           // 2
	shulkerClosing                          // 3
)

// shulkerBE is the tick-owned state of one shulker_box block-entity -- the Go analogue of
// ShulkerBoxBlockEntity narrowed to the 27-slot container the menu/hopper/comparator read/write plus the
// lid animation the tick drives. items is the NonNullList of size 27. animationStatus/progress/progressOld
// drive the lid + the push AABB. openCount is the viewer count (a shulker box uses a plain int openCount,
// NOT the ContainerOpenersCounter the chest/enderchest use). CITE ShulkerBoxBlockEntity fields.
type shulkerBE struct {
	items           [shulkerContainerSize]component.SlotData
	animationStatus shulkerAnimStatus
	progress        float32
	progressOld     float32
	openCount       int
}

// resolveShulker returns the tick-owned shulkerBE for pos, restoring persisted state from the chunk's
// BlockEntity list on first access, or synthesizing an EMPTY one (CLOSED, 27 empty slots) for a freshly
// placed shulker box. Returns nil only when pos is not a shulker box (or the world is unloaded). Tick-owned
// (t.shulkers, the t.dispensers twin).
func (t *TickLoop) resolveShulker(pos pk.Position) *shulkerBE {
	if t.shulkers == nil {
		t.shulkers = make(map[pk.Position]*shulkerBE)
	}
	if s, ok := t.shulkers[pos]; ok {
		return s
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsShulkerBox(state) {
		return nil
	}
	s := t.loadShulkerBE(pos)
	if s == nil {
		s = &shulkerBE{animationStatus: shulkerClosed}
	}
	t.shulkers[pos] = s
	return s
}

// isEmpty ports Container.isEmpty over the 27 slots (used by playerWillDestroy's creative branch). CITE.
func (s *shulkerBE) isEmpty() bool {
	for i := range s.items {
		if !stackEmpty(s.items[i]) {
			return false
		}
	}
	return true
}

// getProgress ports ShulkerBoxBlockEntity.getProgress(partial) = Mth.lerp(partial, progressOld, progress).
// For a server-side AABB read the partial is 1.0f (the full-tick position), so this returns progress. CITE.
func (s *shulkerBE) getProgress(partial float32) float32 {
	return s.progressOld + partial*(s.progress-s.progressOld)
}

// tickShulkers ticks every live shulker_box block-entity once per tick (the blockEntityTicker fan-out for
// ShulkerBoxBlockEntity.tick -> updateAnimation). Called from tickWorld (the tickCampfires twin). A shulker
// whose block was broken/replaced since registration is dropped from the store. Tick-owned.
func (t *TickLoop) tickShulkers() {
	if len(t.shulkers) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, s := range t.shulkers {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !block.IsShulkerBox(state) {
			delete(t.shulkers, pos)
			continue
		}
		t.shulkerUpdateAnimation(pos, state, s)
	}
}

// shulkerUpdateAnimation ports ShulkerBoxBlockEntity.updateAnimation(level, pos, state): step the lid
// progress by 0.1f per tick along the CLOSED/OPENING/OPENED/CLOSING state machine, running the neighbor
// updates at the transition frames and pushing collided entities every OPENING tick. CITE.
func (t *TickLoop) shulkerUpdateAnimation(pos pk.Position, state block.StateID, s *shulkerBE) {
	s.progressOld = s.progress
	switch s.animationStatus {
	case shulkerClosed:
		s.progress = 0.0
	case shulkerOpening:
		s.progress += 0.1
		if s.progressOld == 0.0 {
			t.shulkerDoNeighborUpdates(pos, state)
		}
		if s.progress >= 1.0 {
			s.animationStatus = shulkerOpened
			s.progress = 1.0
			t.shulkerDoNeighborUpdates(pos, state)
		}
		t.shulkerMoveCollidedEntities(pos, state, s)
	case shulkerOpened:
		s.progress = 1.0
	case shulkerClosing:
		s.progress -= 0.1
		if s.progressOld == 1.0 {
			t.shulkerDoNeighborUpdates(pos, state)
		}
		if s.progress <= 0.0 {
			s.animationStatus = shulkerClosed
			s.progress = 0.0
			t.shulkerDoNeighborUpdates(pos, state)
		}
	}
}

// shulkerDoNeighborUpdates ports ShulkerBoxBlockEntity.doNeighborUpdates: updateNeighbourShapes(3) +
// updateNeighborsAt. The shulker open lid changes its support/collision shape, so adjacent blocks
// re-evaluate. Sulfur wires the redstone neighbour wake (the observable part) + the shape recompute seam.
// CITE ShulkerBoxBlockEntity.doNeighborUpdates.
func (t *TickLoop) shulkerDoNeighborUpdates(pos pk.Position, state block.StateID) {
	t.updateShapeOnEdit(pos, state)
	t.onRedstoneEdit(pos)
}

// shulkerMoveCollidedEntities ports ShulkerBoxBlockEntity.moveCollidedEntities(level, pos, state): compute
// the box the lid sweeps through this tick (Shulker.getProgressDeltaAabb over the old+new progress) and
// SHOVE every entity inside it along the FACING by the box size + 0.01 (unless its push reaction is IGNORE).
// CITE ShulkerBoxBlockEntity.moveCollidedEntities + Shulker.getProgressDeltaAabb.
func (t *TickLoop) shulkerMoveCollidedEntities(pos pk.Position, state block.StateID, s *shulkerBE) {
	if !block.IsShulkerBox(state) {
		return // if (!(state.getBlock() instanceof ShulkerBoxBlock)) return;
	}
	dir, ok := block.ShulkerBoxFacing(state)
	if !ok {
		return
	}
	// box = Shulker.getProgressDeltaAabb(1.0f, dir, progressOld, progress, Vec3.atBottomCenterOf(pos)).
	minX, minY, minZ, maxX, maxY, maxZ := shulkerProgressDeltaAABB(1.0, dir, s.progressOld, s.progress,
		float64(pos.X)+0.5, float64(pos.Y), float64(pos.Z)+0.5)
	xsize := maxX - minX
	ysize := maxY - minY
	zsize := maxZ - minZ
	stepX, stepY, stepZ := dirVec(dir)
	// e.move(SHULKER_BOX, ((xsize+0.01)*stepX, (ysize+0.01)*stepY, (zsize+0.01)*stepZ)) for each entity in
	// the box whose push reaction is not IGNORE. The 0.01 fudge is exact (Shulker box push).
	moveX := (xsize + 0.01) * float64(stepX)
	moveY := (ysize + 0.01) * float64(stepY)
	moveZ := (zsize + 0.01) * float64(stepZ)
	if moveX == 0 && moveY == 0 && moveZ == 0 {
		return
	}
	cx := (minX + maxX) / 2
	cz := (minZ + maxZ) / 2
	for _, e := range t.entitiesNearAcrossRegions(cx, cz, 1) {
		if e == nil {
			continue
		}
		// getPistonPushReaction()==IGNORE -> skip. Sulfur has no per-entity push-reaction override yet
		// (only ARMOR_STAND marker / a few return IGNORE); every live-mob/player entity is NORMAL, so the
		// faithful v1 read is "not IGNORE" for all present entity kinds. Structured to read a real
		// getPistonPushReaction when it lands. CITE ShulkerBoxBlockEntity.moveCollidedEntities.
		ex, ey, ez := e.x, e.y, e.z
		// AABB.intersects proxy: the entity position must lie within the swept box (the shared entitiesNear
		// range already bounds XZ; this narrows to the exact box the lid sweeps).
		if ex < minX || ex > maxX || ey < minY || ey > maxY || ez < minZ || ez > maxZ {
			continue
		}
		t.moveEntity(e, moveX, moveY, moveZ)
	}
}

// shulkerProgressDeltaAABB ports net.minecraft.world.entity.monster.Shulker.getProgressDeltaAabb(f, dir, g,
// h, offset): the base half-block box expanded along dir by max(g,h) and shrunk from the back by
// (1+min(g,h)), then translated by the bottom-center offset. Returns (minX,minY,minZ,maxX,maxY,maxZ).
//
//	base   = AABB(-f*0.5, 0, -f*0.5, f, f*0.5, f);
//	max=Math.max(g,h); min=Math.min(g,h);
//	box = base.expandTowards(stepX*max*f, stepY*max*f, stepZ*max*f)
//	         .expandTowards(-stepX*(1+min)*f, -stepY*(1+min)*f, -stepZ*(1+min)*f)
//	         .move(offset).
//
// CITE Shulker.getProgressDeltaAabb.
func shulkerProgressDeltaAABB(f float64, dir block.Direction, g, h float32, ox, oy, oz float64) (minX, minY, minZ, maxX, maxY, maxZ float64) {
	// base AABB.
	minX, minY, minZ = -f*0.5, 0, -f*0.5
	maxX, maxY, maxZ = f, f*0.5, f
	gg, hh := float64(g), float64(h)
	mx := max(gg, hh)
	mn := min(gg, hh)
	stepX, stepY, stepZ := dirVec(dir)
	// first expandTowards(step*max*f).
	minX, minY, minZ, maxX, maxY, maxZ = aabbExpandTowards(minX, minY, minZ, maxX, maxY, maxZ,
		float64(stepX)*mx*f, float64(stepY)*mx*f, float64(stepZ)*mx*f)
	// second expandTowards(-step*(1+min)*f).
	minX, minY, minZ, maxX, maxY, maxZ = aabbExpandTowards(minX, minY, minZ, maxX, maxY, maxZ,
		-float64(stepX)*(1.0+mn)*f, -float64(stepY)*(1.0+mn)*f, -float64(stepZ)*(1.0+mn)*f)
	// move(offset).
	minX += ox
	maxX += ox
	minY += oy
	maxY += oy
	minZ += oz
	maxZ += oz
	return
}

// aabbExpandTowards ports net.minecraft.world.phys.AABB.expandTowards(x,y,z): a negative component pushes
// the min out, a positive one pushes the max out (the box grows toward the sign). CITE AABB.expandTowards.
func aabbExpandTowards(minX, minY, minZ, maxX, maxY, maxZ, x, y, z float64) (float64, float64, float64, float64, float64, float64) {
	if x < 0 {
		minX += x
	} else if x > 0 {
		maxX += x
	}
	if y < 0 {
		minY += y
	} else if y > 0 {
		maxY += y
	}
	if z < 0 {
		minZ += z
	} else if z > 0 {
		maxZ += z
	}
	return minX, minY, minZ, maxX, maxY, maxZ
}

// shulkerStartOpen ports ShulkerBoxBlockEntity.startOpen(user): clamp a negative openCount to 0, increment
// it, and on the FIRST opener (openCount==1) fire the CONTAINER_OPEN gameEvent + the SHULKER_BOX_OPEN sound;
// the openCount block-event (client lid sync) is applied to server state directly (animationStatus). CITE.
func (t *TickLoop) shulkerStartOpen(pos pk.Position, s *shulkerBE) {
	if s.openCount < 0 {
		s.openCount = 0
	}
	s.openCount++
	// level.blockEvent(pos, block, 1, openCount) -> triggerEvent(1, openCount): openCount>0 -> OPENING.
	t.shulkerTriggerEvent(s, s.openCount)
	if s.openCount == 1 {
		// gameEvent(CONTAINER_OPEN): cite-deferred (no BE gameEvent seam).
		// playSound(SHULKER_BOX_OPEN, BLOCKS, 0.5f, random.nextFloat()*0.1f + 0.9f).
		pitch := rand.Float32()*0.1 + 0.9
		t.playSound(shulkerBoxOpenSoundID, soundSourceBlocks, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, 0.5, pitch, rand.Int64())
	}
}

// shulkerStopOpen ports ShulkerBoxBlockEntity.stopOpen(user): decrement openCount, and when it reaches 0
// (or below) fire the CONTAINER_CLOSE gameEvent + the SHULKER_BOX_CLOSE sound; the openCount block-event is
// applied to server state directly. CITE.
func (t *TickLoop) shulkerStopOpen(pos pk.Position, s *shulkerBE) {
	s.openCount--
	t.shulkerTriggerEvent(s, s.openCount)
	if s.openCount <= 0 {
		pitch := rand.Float32()*0.1 + 0.9
		t.playSound(shulkerBoxCloseSoundID, soundSourceBlocks, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, 0.5, pitch, rand.Int64())
	}
}

// shulkerTriggerEvent ports ShulkerBoxBlockEntity.triggerEvent(1, type): openCount=type; type==0 -> CLOSING;
// type==1 -> OPENING. (type>1 leaves the status unchanged -- vanilla only branches on 0 and 1.) This is the
// server-side application of the openCount block-event that drives the lid animation. CITE.
func (t *TickLoop) shulkerTriggerEvent(s *shulkerBE, typ int) {
	s.openCount = typ
	if typ == 0 {
		s.animationStatus = shulkerClosing
	} else if typ == 1 {
		s.animationStatus = shulkerOpening
	}
}

// shulkerCanOpen ports ShulkerBoxBlock.canOpen(state, level, pos, be): if the box is not CLOSED it is
// already open (return true); otherwise the half-block it would expand into must be collision-free
// (level.noCollision over the deflated progress-1 delta box). Sulfur's v1 has no full block-collision query
// over an arbitrary AABB, so the "space toward FACING is free" test is the block-occupancy check on the
// single cell the lid expands into (the common case: a solid block directly in the FACING direction blocks
// opening). CITE ShulkerBoxBlock.canOpen (Shulker.getProgressDeltaAabb(1,facing,0,0.5,...).deflate ->
// level.noCollision).
func (t *TickLoop) shulkerCanOpen(pos pk.Position, state block.StateID, s *shulkerBE) bool {
	if s.animationStatus != shulkerClosed {
		return true
	}
	dir, ok := block.ShulkerBoxFacing(state)
	if !ok {
		return true
	}
	// The lid sweeps a half-block toward FACING; the adjacent cell in that direction must be free of a
	// collision shape. v1 proxy: the neighbour block cell in the FACING direction is non-solid.
	stepX, stepY, stepZ := dirVec(dir)
	nb := pk.Position{X: pos.X + stepX, Y: pos.Y + stepY, Z: pos.Z + stepZ}
	ns, ok := t.world().GetBlock(nb, dimMinY)
	if !ok {
		return true // unloaded: treat as free (vanilla noCollision over an unloaded cell is true)
	}
	return !block.IsRedstoneConductor(ns)
}
