package server

// sensing.go — the 1:1 Go port of net.minecraft.world.entity.ai.sensing.Sensing +
// LivingEntity.hasLineOfSight + the block-clip infra it rides (BlockGetter.traverseBlocks /
// BlockGetter.clip / VoxelShape.clip / AABB.clip), from the unobfuscated 26.2 jar. It closes
// divergence C-4: before this, attack/target/ranged goals fired THROUGH walls because no goal
// tested line of sight (the cited "always visible" stub in ai_goals_*.go).
//
// Sensing.hasLineOfSight(Entity e): memoize per tick via IntSet seen/unseen keyed by e.getId();
// on a miss run mob.hasLineOfSight(e) and record it. Sensing.tick() clears both sets once per tick.
// LivingEntity.hasLineOfSight builds from=(getX,getEyeY,getZ), to=(e.getX,e.getEyeY,e.getZ),
// rejects >128 blocks, and returns level.clip(ClipContext(from,to,COLLIDER,NONE,this)).getType()
// == MISS. Fluid.NONE => fluids never block; a MISS is a clear line.
//
// PIG ORACLE (C-4 gate): a plain pig declares NO attack/target/ranged goal, so no goal calls
// hasLineOfSight on it -> this raycast is NEVER reached on the pinned pig stream, draws ZERO
// random, and mutates only the lazily-created per-mob memo. The pig path stays byte-identical.

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// losMaxDistance is LivingEntity.hasLineOfSight's 128.0 cap (to.distanceTo(from) > 128.0 -> false).
const losMaxDistance = 128.0

// mobEyeHeightFactor is EntityDimensions.defaultEyeHeight height*0.85f: a mob getEyeY() == y+h*0.85.
const mobEyeHeightFactor = 0.85

// sensing is the per-mob Sensing: the per-tick memo of line-of-sight results (IntSet seen/unseen).
// epoch is the gametime the memo belongs to; when the tick advances the memo self-invalidates,
// the Go analogue of Sensing.tick() clearing seen/unseen once per tick. Tick-owned (TICK-05).
type sensing struct {
	epoch  int64
	seen   map[int32]bool
	unseen map[int32]bool
}

// sensingHasLineOfSight is Sensing.hasLineOfSight(Entity) for a player target: memo-check first
// (seen->true, unseen->false), else run the mob->target eye raycast and record it. Keyed by the
// target's entity id; invalidated when the gametime advances (Sensing.tick clear analogue).
//	[VERIFIED javap Sensing.hasLineOfSight: seen.contains(id)->true; unseen.contains(id)->false;
//	 r = mob.hasLineOfSight(e); r ? seen.add(id) : unseen.add(id); return r.]
func (t *TickLoop) sensingHasLineOfSight(e *Entity, target *tickPlayer) bool {
	if e == nil || e.ai == nil || target == nil {
		return false
	}
	s := &e.ai.sense
	if s.seen == nil || s.epoch != t.gametime {
		s.epoch = t.gametime
		s.seen = map[int32]bool{}
		s.unseen = map[int32]bool{}
	}
	id := target.entityID
	if s.seen[id] {
		return true
	}
	if s.unseen[id] {
		return false
	}
	r := t.mobHasLineOfSight(e, target)
	if r {
		s.seen[id] = true
	} else {
		s.unseen[id] = true
	}
	return r
}

// mobHasLineOfSight ports LivingEntity.hasLineOfSight(Entity) (COLLIDER/NONE/eyeY overload): build
// from=(mob.getX, mob.getEyeY, mob.getZ) and to=(target.getX, target.getEyeY, target.getZ), reject
// if farther than 128 blocks, else clip against block COLLIDER shapes and return true iff MISS.
//	[VERIFIED javap LivingEntity.hasLineOfSight: from eye; to at e.getEyeY(); dist>128 -> false;
//	 level.clip(ClipContext(from,to,COLLIDER,NONE,this)).getType() == MISS.]
func (t *TickLoop) mobHasLineOfSight(e *Entity, target *tickPlayer) bool {
	if t.world() == nil {
		return false
	}
	fromX, fromY, fromZ := e.x, e.y+e.height*mobEyeHeightFactor, e.z
	toX, toY, toZ := target.x, target.y+playerStandingEyeHeight, target.z
	ddx, ddy, ddz := toX-fromX, toY-fromY, toZ-fromZ
	if ddx*ddx+ddy*ddy+ddz*ddz > losMaxDistance*losMaxDistance {
		return false
	}
	return !t.clipBlocksCollider(fromX, fromY, fromZ, toX, toY, toZ)
}

// clipBlocksCollider is BlockGetter.clip(ClipContext) for Block.COLLIDER / Fluid.NONE: DDA-traverse
// the cells the segment (from->to) crosses (BlockGetter.traverseBlocks) and per cell test the
// COLLIDER shape (VoxelShape.clip). Returns true on the FIRST collider the ray enters (hit != MISS),
// false if the whole segment is clear. The fluid clip is omitted (Fluid.NONE -> empty).
//	[VERIFIED javap BlockGetter.traverseBlocks: -1E-7 lerp endpoint nudge, Mth.floor cell, sign/
//	 tDelta/tMax DDA step; per-block lambda: COLLIDER shape -> VoxelShape.clip, fluid NONE -> empty.]
func (t *TickLoop) clipBlocksCollider(fx, fy, fz, tx, ty, tz float64) (hit bool) {
	if fx == tx && fy == ty && fz == tz {
		return false // traverseBlocks: from.equals(to) -> miss supplier
	}
	// Mth.lerp(-1E-7, a, b) endpoint nudges: sx/sy/sz nudged FROM, ex/ey/ez nudged TO. The scan
	// starts at the TO cell and steps back toward FROM, exactly as traverseBlocks does.
	const nudge = -1.0e-7
	sx := mthLerpD(nudge, tx, fx)
	sy := mthLerpD(nudge, ty, fy)
	sz := mthLerpD(nudge, tz, fz)
	ex := mthLerpD(nudge, fx, tx)
	ey := mthLerpD(nudge, fy, ty)
	ez := mthLerpD(nudge, fz, tz)

	cx := floorI(ex)
	cy := floorI(ey)
	cz := floorI(ez)
	if t.cellClipsCollider(cx, cy, cz, fx, fy, fz, tx, ty, tz) {
		return true
	}

	dx := sx - ex
	dy := sy - ey
	dz := sz - ez
	stepX := mthSignD(dx)
	stepY := mthSignD(dy)
	stepZ := mthSignD(dz)

	tDeltaX := math.MaxFloat64
	tDeltaY := math.MaxFloat64
	tDeltaZ := math.MaxFloat64
	if stepX != 0 {
		tDeltaX = float64(stepX) / dx
	}
	if stepY != 0 {
		tDeltaY = float64(stepY) / dy
	}
	if stepZ != 0 {
		tDeltaZ = float64(stepZ) / dz
	}
	tMaxX := tDeltaX * boundaryFrac(stepX, ex)
	tMaxY := tDeltaY * boundaryFrac(stepY, ey)
	tMaxZ := tDeltaZ * boundaryFrac(stepZ, ez)

	for tMaxX <= 1.0 || tMaxY <= 1.0 || tMaxZ <= 1.0 {
		if tMaxX < tMaxY {
			if tMaxX < tMaxZ {
				cx += stepX
				tMaxX += tDeltaX
			} else {
				cz += stepZ
				tMaxZ += tDeltaZ
			}
		} else if tMaxY < tMaxZ {
			cy += stepY
			tMaxY += tDeltaY
		} else {
			cz += stepZ
			tMaxZ += tDeltaZ
		}
		if t.cellClipsCollider(cx, cy, cz, fx, fy, fz, tx, ty, tz) {
			return true
		}
	}
	return false
}

// boundaryFrac is the traverseBlocks per-axis tMax seed: step>0 ? 1-Mth.frac(coord) : Mth.frac(coord).
func boundaryFrac(step int, coord float64) float64 {
	if step == 0 {
		return 0
	}
	if step > 0 {
		return 1.0 - mthFracD(coord)
	}
	return mthFracD(coord)
}

// cellClipsCollider is the per-block clip lambda for Block.COLLIDER: read the state, take its
// COLLIDER VoxelShape, and clip the segment against it placed at the cell. An unloaded cell has no
// collider (null-chunk skip). Fluid clip omitted (Fluid.NONE).
//	[VERIFIED javap the per-block clip lambda: shape=ClipContext.getBlockShape(state) (COLLIDER ->
//	 state.getCollisionShape); VoxelShape.clip(from,to,pos).]
func (t *TickLoop) cellClipsCollider(bx, by, bz int, fx, fy, fz, tx, ty, tz float64) bool {
	sid, ok := t.world().GetBlock(pk.Position{X: bx, Y: by, Z: bz}, dimMinY)
	if !ok {
		return false
	}
	shape := block.CollisionShape(sid)
	return voxelShapeClips(shape, bx, by, bz, fx, fy, fz, tx, ty, tz)
}

// voxelShapeClips ports VoxelShape.clip(from, to, pos) to a hit/miss boolean: empty -> miss;
// delta.lengthSqr < 1E-7 -> miss; startPoint = from + delta*0.001, if its cell is FULL (isFullWide)
// -> hit; else AABB.clip over the shape's boxes (ClipSegment) -> hit iff any box is entered. The
// exact BlockHitResult location is not consulted by hasLineOfSight (only getType()==MISS).
//	[VERIFIED javap VoxelShape.clip: isEmpty->null; lengthSqr<1E-7->null; startPoint=from+delta*0.001;
//	 isFullWide(findIndex X/Y/Z)->hit; else AABB.clip(toAabbs(),from,to,pos).]
func voxelShapeClips(s *block.VoxelShape, bx, by, bz int, fx, fy, fz, tx, ty, tz float64) bool {
	if s.IsEmpty() {
		return false
	}
	dx, dy, dz := tx-fx, ty-fy, tz-fz
	if dx*dx+dy*dy+dz*dz < 1.0e-7 {
		return false
	}
	spx := fx + dx*0.001
	spy := fy + dy*0.001
	spz := fz + dz*0.001
	if s.ClipStartInside(float64(bx), float64(by), float64(bz), spx, spy, spz) {
		return true
	}
	return s.ClipSegment(float64(bx), float64(by), float64(bz), fx, fy, fz, tx, ty, tz)
}

// mthLerpD is Mth.lerp(delta, start, end): start + delta*(end-start).
func mthLerpD(delta, start, end float64) float64 { return start + delta*(end-start) }

// mthFracD is Mth.frac(v): v - floor(v).
func mthFracD(v float64) float64 { return v - math.Floor(v) }

// mthSignD is Mth.sign(v): -1, 0, or +1 by sign.
func mthSignD(v float64) int {
	if v > 0 {
		return 1
	}
	if v < 0 {
		return -1
	}
	return 0
}
