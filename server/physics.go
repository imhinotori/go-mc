package server

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/internal/bvh"
)

// physics.go — ENT-02: gravity + per-axis swept-AABB collision (the entity half) and the
// authoritative per-axis validation of a client-sent player position (the player half).
//
// THE LOAD-BEARING DISCIPLINE (06-RESEARCH Pattern 2 + Pitfall 4): collision is resolved
// PER AXIS, never as a full-vector-then-test. moveEntity clips Δy, applies it, re-derives
// the box, clips Δx, applies, then clips Δz, applies. Moving the whole motion vector then
// testing lets a fast entity tunnel a thin wall — the per-axis sweep clamps each component
// against the blocks actually in that axis's swept range, so a 10-block/tick velocity into
// a 1-block wall stops AT the wall (TestNoTunnel).
//
// All of this runs ONLY on the tick goroutine over tick-owned entity/player/chunk state
// (TICK-05): moveEntity routes its position change through entityStore.move so the per-
// section bucket the tracker's near() reads stays consistent (no stale bucket).

// --- Tunable physics constants (06-RESEARCH A1) ----------------------------------------
//
// These are [ASSUMED]/training-derived and DO NOT affect the wire — they shape only the
// VISIBLE behaviors (an entity falls, lands, is blocked). v1 targets those behaviors, not
// exact vanilla-constant parity; the values are documented here as the single tunable knob
// and may be revised against the jar Entity/LivingEntity if parity ever matters.
const (
	// gravityPerTick is the downward acceleration applied each tick (blocks/tick²). Vanilla
	// living entities ≈ 0.08 (items 0.04). [ASSUMED — 06-RESEARCH A1, wire-irrelevant.]
	gravityPerTick = 0.08

	// airDrag scales vertical velocity each tick AFTER gravity, so vertical speed converges
	// to a terminal velocity instead of growing unbounded. Vanilla ≈ 0.98. [ASSUMED.]
	airDrag = 0.98
)

// The auto step-up (Entity.collide's maxUpStep branch) IS applied now: moveEntity /
// collidePlayer read the PER-ENTITY value — entityMaxUpStep(e) for mobs (LivingEntity.
// maxUpStep == (float)getAttributeValue(attribute.StepHeight), registration default 0.6) and
// p.getAttributeValue(attrStepHeight) for players — so the attribute is the single source of
// truth (no separate constant). See collision.go collideMovement.

// dimMinY is the dimension floor used to map a world Y to its chunk-section index. The
// overworld floor is -64 (mirrors cmd/sulfur/main.go overworldMinY). It is a named const
// here (not hard-coded deep in the read) so a future multi-dimension wiring threads the
// real per-dimension floor; for v1 the single overworld value is correct.
const dimMinY = -64

// playerWidth / playerHeight are the player's collision AABB footprint (≈ 0.6 × 1.8 in
// vanilla). Used by collidePlayer to build the player box for the authoritative anti-clip
// validation. Plain tunable dims (the player is not yet a data/entity-table Entity).
const (
	playerWidth  = 0.6
	playerHeight = 1.8
)

// floorI is math.Floor returning an int — the negative-correct world-block index of a
// fractional coordinate (e.g. -0.3 → -1, not 0). Used to map an AABB edge to block coords.
func floorI(v float64) int { return int(math.Floor(v)) }

// blockSolidAt reports whether the world block at (x,y,z) is a solid collider. A non-air
// block is solid for v1 (slabs/stairs/fluids with partial boxes are a later refinement —
// v1 treats every solid as a unit cube). Returns false (non-solid / air) when there is no
// world wired or the column/section is not loaded, so physics in a world-less test and an
// entity over an ungenerated column simply do not collide (treated as empty air). This is
// the READ counterpart to the WRITE API Plan 06-04 adds; both mirror the generator's
// section/local mapping (world/generator.go sectionLocal).
func (t *TickLoop) blockSolidAt(x, y, z int) bool {
	if t.world() == nil {
		return false // no world: nothing to collide with (world-less unit tests)
	}
	// One mapping: the read goes through world.ChunkManager.GetBlock (Plan 06-04), which
	// owns the single pos->(column,section,local) mapping mirrored from the generator. An
	// unloaded column / out-of-range y returns ok=false -> treated as non-solid air (do not
	// collide / never block), exactly as the old inline read did. This is the READ side of
	// the same API the place/break handlers WRITE through, so physics and edits can never
	// disagree on where a block lives.
	s, ok := t.world().GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinY)
	if !ok {
		return false
	}
	// COLLISION = blocksMotion(), NOT "not air". Vanilla BlockStateBase.blocksMotion() is
	// `block != COBWEB && block != BAMBOO_SAPLING && legacySolid` (legacySolid == isSolid()).
	// block.IsSolid is the precomputed isSolid() table (level/block/support.go) — true for stone /
	// grass_block (the ground), FALSE for short_grass / flowers / ferns / saplings (their
	// getCollisionShape is empty). The old `!IsAir` test treated every non-air block as a collision
	// wall, so a mob/player stepped UP onto grass/flowers as if they were full blocks. Using IsSolid
	// restores the empty collision shape for non-colliding plants. (Cobweb/bamboo-sapling are
	// isSolid=false anyway, so the blocksMotion special-cases don't change this result; lava is not
	// yet extracted — water is handled below.) CITE: BlockBehaviour$BlockStateBase.blocksMotion.
	if !block.IsSolid(s) {
		return false
	}
	// FLUID is NOT a collision wall. Vanilla LiquidBlock.getCollisionShape returns Shapes.empty()
	// for a normal entity, so water/lava never block movement — an entity falls THROUGH the surface
	// and then swims/sinks via travelInFluid. (Water is isSolid=false so the IsSolid gate above
	// already excludes it, but keep the explicit guard for clarity + any solid-flagged fluid edge.)
	// CITE: LiquidBlock.getCollisionShape.
	if _, isWater := waterLevelOf(s); isWater {
		return false
	}
	return true
}

// floorDiv is a negative-correct integer floor-division (Go's / truncates toward zero, so
// -1/16 == 0 not -1). Maps a world block coord to its chunk-column index correctly for
// negative coordinates — the same negative-correctness columnOf relies on.
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// entityBoxAt builds the entity's AABB as if its feet were at (x,y,z), reusing the entity's
// data/entity Width/Height (centered on x/z, base at y, top at y+height) — the same feet-
// anchored box as Entity.AABB, but parameterized so the per-axis sweep can probe a proposed
// position WITHOUT mutating the entity. Returns the generic bvh.AABB primitive so the
// overlap test reuses the bvh box type (no second box type).
func entityBoxAt(e *Entity, x, y, z float64) bvh.AABB[float64, bvh.Vec3[float64]] {
	hw := e.width / 2
	return bvh.AABB[float64, bvh.Vec3[float64]]{
		Lower: bvh.Vec3[float64]{x - hw, y, z - hw},
		Upper: bvh.Vec3[float64]{x + hw, y + e.height, z + hw},
	}
}

// boxOverlapsSolid reports whether the given entity-space box overlaps ANY solid world
// block. It walks every block the box spans (floor of each lower edge .. ceil-1 of each
// upper edge) and, for each, builds that block's unit cube AABB and tests it against the
// box. The narrow-phase test uses bvh.AABB.Touch for the X/Y overlap and an explicit Z
// interval test: the bvh.Vec3 Less/More compare only components 0 and 1 (X,Y), so Touch
// alone is X/Y-only — the Z interval is added here so the overlap is correct on ALL THREE
// axes (a deliberate, documented complement to the reused bvh primitive, not a reimplemented
// box-overlap). A half-open edge (upper exactly on a block boundary) does not count as
// overlap, so an entity resting exactly on a floor top (y == floorTop) does not "touch" the
// block below it — which is what lets it settle.
func (t *TickLoop) boxOverlapsSolid(box bvh.AABB[float64, bvh.Vec3[float64]]) bool {
	const eps = 1e-9
	loX, loY, loZ := box.Lower[0], box.Lower[1], box.Lower[2]
	hiX, hiY, hiZ := box.Upper[0], box.Upper[1], box.Upper[2]

	for bx := floorI(loX); bx <= floorI(hiX-eps); bx++ {
		for by := floorI(loY); by <= floorI(hiY-eps); by++ {
			for bz := floorI(loZ); bz <= floorI(hiZ-eps); bz++ {
				if !t.blockSolidAt(bx, by, bz) {
					continue
				}
				blockBox := bvh.AABB[float64, bvh.Vec3[float64]]{
					Lower: bvh.Vec3[float64]{float64(bx), float64(by), float64(bz)},
					Upper: bvh.Vec3[float64]{float64(bx + 1), float64(by + 1), float64(bz + 1)},
				}
				// bvh.AABB.Touch covers the X/Y overlap (Vec3.Less/More compare [0],[1]
				// only); the Z interval is tested explicitly so all three axes are checked.
				if box.Touch(blockBox) && loZ < blockBox.Upper[2] && blockBox.Lower[2] < hiZ {
					return true
				}
			}
		}
	}
	return false
}

// entityBoxOf builds the entity's current AABB as the block.Box the VoxelShape engine
// consumes (feet-anchored, half-width on X/Z — the same geometry as entityBoxAt).
func entityBoxOf(e *Entity) block.Box {
	hw := e.width / 2
	return block.Box{
		MinX: e.x - hw, MinY: e.y, MinZ: e.z - hw,
		MaxX: e.x + hw, MaxY: e.y + e.height, MaxZ: e.z + hw,
	}
}

// moveEntity resolves a desired motion (dx,dy,dz) for an entity against the REAL per-state
// collision VoxelShapes and applies it through the tick-owned store. This is the
// Entity.move(MoverType.SELF, movement) core (26.2):
//
//   - collideMovement == Entity.collide(Vec3): collect the block shapes in the motion-
//     expanded box, clip one axis at a time in Direction.axisStepOrder (Y first, then the
//     larger horizontal component) — see collision.go for the full ported chain. Slabs,
//     stairs, fences (1.5 tall), walls, snow layers, carpets now collide with their real
//     vanilla boxes instead of the old full-cube approximation.
//   - Flags, from Entity.move's "rest" section: horizontalCollision uses the Mth.equal
//     1e-5f tolerance; verticalCollision the exact != compare; onGround is
//     setOnGroundWithMovement(verticalCollisionBelow, …) — moving down AND clamped.
//     CITE: javap Entity.move — Mth.equal for x/z, dcmpl for y, verticalCollisionBelow =
//     verticalCollision && vec.y < 0.
//
// The single position write routes through entities.move so the per-section bucket stays
// consistent for the tracker's near() (TICK-05 bucket-consistency contract). The
// velocity-zeroing contract is preserved from v1 (callers — fishing bobber, minecart, item —
// detect wall/floor hits off a zeroed component; vanilla zeroes deltaMovement in the
// respective movers, so the observable result is identical). Runs only on the tick goroutine.
func (t *TickLoop) moveEntity(e *Entity, dx, dy, dz float64) {
	m := vec3d{dx, dy, dz}
	// e.onGround is read BEFORE the flags below reassign it — Entity.collide's step-up gate
	// consults this.onGround(), the PREVIOUS move's result. entityMaxUpStep dispatches the
	// vanilla maxUpStep(): 0 for non-living entities, (float)STEP_HEIGHT for mobs — so a
	// walking mob now auto-steps slabs/stairs/snow layers up to 0.6 exactly like vanilla.
	c := t.collideMovement(m, entityBoxOf(e), entityMaxUpStep(e), e.onGround)

	// setPos: one write through the store keeps the tracker bucket consistent.
	t.cur().entities.move(e, e.x+c.x, e.y+c.y, e.z+c.z)

	// Entity.move "rest": the collision flags.
	collidedX := !mthEqual(dx, c.x)
	collidedZ := !mthEqual(dz, c.z)
	e.horizontalCollision = collidedX || collidedZ
	e.verticalCollision = dy != c.y
	// setOnGroundWithMovement(verticalCollisionBelow, horizontalCollision, …): onGround is
	// exactly verticalCollisionBelow. CITE: javap Entity.setOnGroundWithMovement(ZZVec3).
	e.onGround = e.verticalCollision && dy < 0

	// Zero out a velocity component that was clamped so it does not accumulate into the
	// next tick (a blocked entity stops; landing kills downward velocity so onGround stays
	// stable instead of re-accelerating into the floor).
	if c.y != dy {
		e.vy = 0
	}
	if c.x != dx {
		e.vx = 0
	}
	if c.z != dz {
		e.vz = 0
	}
}

// --- LivingEntity.travelInAir 1:1 (B-A1 / B-A2) ------------------------------------------------
//
// The air/ground travel chain, ported byte-exact from net.minecraft.world.entity.LivingEntity
// .travelInAir / handleRelativeFrictionAndCalculateMovement / getFrictionInfluencedSpeed /
// computeModifiedFriction (26.2, javap -c -p this session). The ORDER is the crux the audit
// (B-A1) flagged: vanilla is moveRelative -> move -> gravity -> drag. The pre-port code applied
// drag BEFORE the move and used the grounded horizontal friction (0.546) even when airborne (the
// airborne value is 0.91); it also let a navigating mob move()+friction TWICE per tick (B-A2).
// travelInAir is the SINGLE authority for a dry mob per-tick travel so both are impossible.
//
// The move itself routes through moveEntity (Entity.move == the per-axis swept collision), so the
// collision half is unchanged; this function only fixes the surrounding travel ORDER + constants.

// frictionModifierDefault is the FRICTION_MODIFIER attribute default (RangedAttribute default 1.0).
// airDragModifierDefault is the AIR_DRAG_MODIFIER attribute default (also 1.0). Both are cited
// constants standing in for getAttributeValue(...) until the attribute is a real per-entity read
// (CLAUDE.md: cite the vanilla default, never bake it away). computeModifiedFriction(f, 1.0) == f,
// so with the defaults the grounded horizontal drag is blockFriction*0.91 and the vertical is 0.98
// -- the exact vanilla numbers.
//
//	[VERIFIED javap Attributes clinit: new RangedAttribute(friction_modifier, 1.0, 0.0, 2048.0)
//	 and new RangedAttribute(air_drag_modifier, 1.0, 0.0, 2048.0) -> getAttributeValue default 1.0.]
const (
	frictionModifierDefault = float32(1.0)
	airDragModifierDefault  = float32(1.0)
)

// travelBlockFrictionDefault is BlockBehaviour.Properties default friction (0.6f) -- the value
// Block.getFriction() returns for a normal block (grass/stone/dirt). Sulfur has no per-block
// getFriction() table yet, so travelInAir reads this default (structured to become a real
// getBlockState(blockPosBelow).getBlock().getFriction() read later; ice/slime/honey then diverge).
//
//	[VERIFIED CFR Block.getFriction: return this.friction; -- Properties default friction 0.6f.]
const travelBlockFrictionDefault = float32(0.6)

// computeModifiedFriction ports LivingEntity.computeModifiedFriction(float, float) 1:1:
// Mth.clamp(1.0f - (1.0f - f) * mod, 0.0f, 1.0f). With mod == 1.0 (the attribute default) this is
// just clamp(f, 0, 1) == f.
//
//	[VERIFIED javap LivingEntity.computeModifiedFriction: fconst_1; (1.0 - f); * mod; 1.0 - that;
//	 Mth.clamp(v, 0.0f, 1.0f).]
func computeModifiedFriction(f, mod float32) float32 {
	v := float32(1.0) - (float32(1.0)-f)*mod
	if v < 0.0 {
		return 0.0
	}
	if v > 1.0 {
		return 1.0
	}
	return v
}

// frictionInfluencedSpeed ports LivingEntity.getFrictionInfluencedSpeed(float) 1:1. On the ground
// with blockFriction f > 0.6 (compared AS DOUBLE -- the jar does f2d before the compare, so the
// default 0.6f == 0.6000000238418579d IS > 0.6d), walk speed is scaled by 0.21600002f / f^3 (a slippery
// block -- smaller f -- accelerates the mob faster; at the default 0.6f the factor is ~1.0 so the walk
// speed is ~getSpeed()); strictly at f <= 0.6d it is the bare getSpeed(); airborne it is getFlyingSpeed().
// Note the 26.2 f > 0.6 guard (double compare) and the exact literal 0.21600002f.
//
//	[VERIFIED javap LivingEntity.getFrictionInfluencedSpeed: onGround ? ((f > 0.6) ? getSpeed() *
//	 (0.21600002f / (f*f*f)) : getSpeed()) : getFlyingSpeed().]
func frictionInfluencedSpeed(onGround bool, f, speed, flyingSpeed float32) float32 {
	if onGround {
		// Vanilla WIDENS f to double before the compare (bytecode: fload_1; f2d; ldc2_w 0.6d; dcmpl;
		// ifle). The default block friction 0.6f widened to double is 0.6000000238418579, which IS > 0.6d,
		// so the >0.6 branch is TAKEN for a normal block -- a plain `f > 0.6` in float32 would be FALSE
		// (float32(0.6) == float32(0.6)) and take the wrong branch. Match the jar's f2d widening exactly.
		if float64(f) > 0.6 {
			return speed * (0.21600002 / (f * f * f))
		}
		return speed
	}
	return flyingSpeed
}

// travelInAir ports net.minecraft.world.entity.LivingEntity.travelInAir(Vec3) 1:1 for the DRY
// air/ground branch (water/lava is travelInFluid, owned elsewhere; flying is travelFlying). It is
// the SINGLE per-tick travel for a dry mob. speed is getSpeed() (speedModifier x MOVEMENT_SPEED),
// flyingSpeed is getFlyingSpeed(); (inX,inY,inZ) is the movement input (moveRelative Vec3) -- for
// a walking mob (0, 0, forward). The EXACT vanilla sequence:
//
//	f3   = onGround ? computeModifiedFriction(blockBelow.getFriction, FRICTION_MODIFIER) : 1.0
//	// handleRelativeFrictionAndCalculateMovement(input, f3):
//	moveRelative(getFrictionInfluencedSpeed(f3), input)   // deltaMovement += yaw-rotated(input*speed)
//	move(SELF, deltaMovement)                             // the ONE swept-collision move
//	d0   = deltaMovement.y                                // vec3.y AFTER the move
//	// gravity / levitation / slow-falling on d0 (getEffectiveGravity), preserving the ported hooks
//	// drag (only when !shouldDiscardFriction):
//	f9   = computeModifiedFriction(0.91, AIR_DRAG_MODIFIER)
//	f10  = f3 * f9                                        // horizontal drag (grounded 0.546, air 0.91)
//	f11  = omnidirectional ? f9 : computeModifiedFriction(0.98, AIR_DRAG_MODIFIER)  // vertical 0.98
//	setDeltaMovement(vec3.x * f10, d0 * f11, vec3.z * f10)
//
// The levitation / slow-falling gravity branches are the same jar-cited logic tickPhysics already
// ran; they land in the SAME order slot (gravity AFTER the move, BEFORE the drag). A mob with no
// effect takes the plain d5 -= getEffectiveGravity() path -- byte-identical to the old numbers,
// only the ORDER (move-then-gravity-then-drag) changed.
//
//	[VERIFIED javap LivingEntity.travelInAir (offsets 0-263), handleRelativeFrictionAndCalculateMovement,
//	 getFrictionInfluencedSpeed, computeModifiedFriction, getEffectiveGravity; Entity.moveRelative/
//	 getInputVector (input.normalize() if lengthSqr>1; scale(speed); rotate by yaw: x*cos-z*sin, z*cos+x*sin).]
func (t *TickLoop) travelInAir(e *Entity, inX, inY, inZ, speed, flyingSpeed float32) {
	// f3 = block friction below (onGround) or 1.0 (airborne). computeModifiedFriction(0.6, 1.0)==0.6.
	f3 := float32(1.0)
	if e.onGround {
		f3 = computeModifiedFriction(travelBlockFrictionDefault, frictionModifierDefault)
	}

	// handleRelativeFrictionAndCalculateMovement: moveRelative then the single move.
	// moveRelative(fricSpeed, input): deltaMovement += getInputVector(input, fricSpeed, yaw).
	fricSpeed := frictionInfluencedSpeed(e.onGround, f3, speed, flyingSpeed)
	ivX, ivY, ivZ := getInputVector(inX, inY, inZ, fricSpeed, e.yaw)
	e.vx += float64(ivX)
	e.vy += float64(ivY)
	e.vz += float64(ivZ)

	// move(SELF, deltaMovement): the ONE per-axis swept-collision move (Entity.move). This zeroes a
	// blocked velocity component + updates onGround (moveEntity), exactly as vanilla move() does.
	t.moveEntity(e, e.vx, e.vy, e.vz)

	// d0 = deltaMovement.y AFTER the move (vec3.y). Gravity / levitation / slow-falling applied here,
	// AFTER the move, BEFORE the drag (the B-A1 order fix). These are the ported effect hooks in the
	// exact vanilla slot; a no-effect mob takes the plain gravity subtraction.
	d0 := e.vy
	if amp, ok := entityEffectAmplifier(e, effectLevitation); ok {
		// travelInAir LEVITATION branch: upward drift overriding gravity.
		//   d0 += (0.05 * (amplifier + 1) - deltaMovement.y) * 0.2
		d0 += (0.05*float64(amp+1) - e.vy) * 0.2
	} else {
		// getEffectiveGravity(): SLOW_FALLING (while falling) clamps gravity to min(getGravity(), 0.01).
		grav := gravityPerTick // getGravity() == GRAVITY attribute default (0.08)
		if e.vy <= 0 && entityHasEffect(e, effectSlowFalling) {
			if grav > 0.01 {
				grav = 0.01
			}
		}
		d0 -= grav
	}
	// (The client-side / below-minY sub-branches of travelInAir do not apply on the server hot path:
	// isClientSide is always false here, so the vanilla else -> d5 -= getEffectiveGravity() is the
	// taken branch. The minY==getY floor-clamp is a client-prediction guard, not server logic.)

	// Drag: horizontal *= f3 * computeModifiedFriction(0.91, AIR_DRAG_MODIFIER); vertical *=
	// computeModifiedFriction(0.98, AIR_DRAG_MODIFIER). (shouldDiscardFriction -- knockback-resistance
	// edge -- is not yet wired; the default is false, the drag branch, so this is the vanilla path.)
	f9 := computeModifiedFriction(0.91, airDragModifierDefault)
	f10 := f3 * f9
	f11 := computeModifiedFriction(0.98, airDragModifierDefault)
	e.vx *= float64(f10)
	e.vy = d0 * float64(f11)
	e.vz *= float64(f10)
}

// getInputVector ports net.minecraft.world.entity.Entity.getInputVector(Vec3, float, float) 1:1
// (via moveRelative, which calls it with getYRot()). It normalizes the input when lengthSqr > 1,
// scales by speed, and rotates by the yaw: (x*cos - z*sin, y, z*cos + x*sin) where the angle is
// yaw * (pi/180). An input with lengthSqr < 1e-7 yields the zero vector (an idle mob adds nothing).
//
//	[VERIFIED javap Entity.getInputVector: lengthSqr<1e-7 -> ZERO; (lengthSqr>1 ? normalize : self)
//	 .scale(speed); sin/cos of yaw*0.017453292f; new Vec3(s.x*cos - s.z*sin, s.y, s.z*cos + s.x*sin).]
func getInputVector(inX, inY, inZ, speed, yawDeg float32) (x, y, z float32) {
	lenSqr := float64(inX)*float64(inX) + float64(inY)*float64(inY) + float64(inZ)*float64(inZ)
	if lenSqr < 1.0e-7 {
		return 0, 0, 0
	}
	sx, sy, sz := float64(inX), float64(inY), float64(inZ)
	if lenSqr > 1.0 {
		inv := 1.0 / math.Sqrt(lenSqr)
		sx, sy, sz = sx*inv, sy*inv, sz*inv
	}
	sp := float64(speed)
	sx, sy, sz = sx*sp, sy*sp, sz*sp
	// Mth.sin/cos take a float radians arg (yaw * 0.017453292f). Compute the angle as a float32
	// product first (matching the jar float multiply) before the trig.
	rad := float64(yawDeg * 0.017453292)
	sin := math.Sin(rad)
	cos := math.Cos(rad)
	x = float32(sx*cos - sz*sin)
	y = float32(sy)
	z = float32(sz*cos + sx*sin)
	return x, y, z
}

// collidePlayer is the AUTHORITATIVE validation of a client-sent player position (06-RESEARCH
// Pattern 2 'When to use' / threat T-6-06). The client SENDS the position it claims to be at;
// the server collides the claimed DELTA (from the last accepted position) against the real
// collision VoxelShapes and returns a position clamped OUT of any collider — so a
// malicious/buggy client claiming a spot inside/through a wall is CORRECTED, not trusted.
// This mirrors what vanilla's ServerGamePacketListenerImpl.handleMovePlayer does when it runs
// player.move(MoverType.PLAYER, submitted - lastGood): the Entity.collide chain
// (collideMovement in collision.go) with the player's box.
//
// Fast path: if the claimed box intersects NO collision shape (the BlockCollisions iterator
// yields nothing — vanilla Level.noCollision), the claim is accepted verbatim, so open-air
// movement (the overwhelming common case) is never nudged by floating-point noise and
// world-less tests see the decoded position unchanged. Runs on the tick goroutine.
func (t *TickLoop) collidePlayer(p *tickPlayer, newX, newY, newZ float64) (x, y, z float64) {
	if t.world() == nil {
		return newX, newY, newZ // no world: nothing to collide against, accept as-is
	}
	if len(t.collectBlockCollisions(playerBoxD(newX, newY, newZ))) == 0 {
		return newX, newY, newZ // claimed box is clear of every collision shape
	}
	// The claimed position intersects a collider. Clip the claimed delta from the player's
	// CURRENT accepted position through the vanilla collide chain (Y then larger-horizontal
	// axis order, real shapes) and accept the clipped result.
	// The player's maxUpStep is LivingEntity.maxUpStep(): (float)getAttributeValue(
	// STEP_HEIGHT) (player default 0.6) — the same collide the vanilla server runs for the
	// player entity; onGround is the client-reported flag the last accepted packet set.
	m := vec3d{newX - p.x, newY - p.y, newZ - p.z}
	c := t.collideMovement(m, playerBoxD(p.x, p.y, p.z), float32(p.getAttributeValue(attrStepHeight)), p.onGround)
	x, y, z = p.x+c.x, p.y+c.y, p.z+c.z
	// ULTRA_DEBUG: a clamp fired — the claimed position was inside a collider and got corrected.
	// The most useful single line for a "stuck on water surface / can't swim up" report: it shows
	// whether the server is overriding the client's submitted Y. No-op unless SULFUR_ULTRA_DEBUG=1.
	udebugPlayer(p, "collide", "claimed=(%.3f,%.3f,%.3f) -> accepted=(%.3f,%.3f,%.3f)", newX, newY, newZ, x, y, z)
	return x, y, z
}

// playerBoxD builds the player's collision AABB (0.6 × 1.8) as the block.Box the VoxelShape
// engine consumes — same geometry as playerBoxAt (which stays for the bvh-typed callers).
func playerBoxD(x, y, z float64) block.Box {
	hw := playerWidth / 2
	return block.Box{
		MinX: x - hw, MinY: y, MinZ: z - hw,
		MaxX: x + hw, MaxY: y + playerHeight, MaxZ: z + hw,
	}
}

// playerBoxAt builds the player's collision AABB (≈ 0.6 × 1.8) centered on x/z with its base
// at y — the same feet-anchored shape as the entity box, for the player's fixed dims.
func playerBoxAt(x, y, z float64) bvh.AABB[float64, bvh.Vec3[float64]] {
	hw := playerWidth / 2
	return bvh.AABB[float64, bvh.Vec3[float64]]{
		Lower: bvh.Vec3[float64]{x - hw, y, z - hw},
		Upper: bvh.Vec3[float64]{x + hw, y + playerHeight, z + hw},
	}
}
