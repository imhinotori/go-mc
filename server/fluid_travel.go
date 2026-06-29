package server

// fluid_travel.go — LIVE-DEBUG A (the "mobs sink in water" fix): the SERVER-controlled mob water
// physics, the *Entity sibling of the deferred player water-travel path (fluid_physics.go header).
//
// THE BUG: tickPhysics (tick_phases.go) applied the DRY-land vertical physics to EVERY entity —
// `vy -= 0.08 (gravity); vy *= 0.98 (air drag)` — regardless of whether the mob was in water. A
// pig dropped in water therefore sank at the dry-air rate (~0.08/tick gravity), which FloatGoal's
// +0.04 swim impulse (jump.go: jumpInLiquid) could never overcome, so the pig swam briefly then
// sank to the floor. Vanilla LivingEntity, when in water, runs travelInWater INSTEAD of travelInAir:
// the vertical velocity is multiplied by 0.8 (water drag, NOT the 0.98 air drag) AND the gravity
// pull is baseGravity/16 ≈ 0.005 (NOT the full 0.08), so a vanilla mob sinks SLOWLY and FloatGoal's
// +0.04 wins — the mob bobs at the surface.
//
// LITERAL 1:1 PORT OF VANILLA JAVA 26.2 (protocol 776) — re-verified method-for-method against
// temp/cache/26.2-inner.jar via `javap -c -p` this session. No GPL source is pasted; the algorithm
// is re-expressed in Go with the IDENTICAL numeric ops, in the same order.
//
// The vanilla call chain (javap this session), the v1-relevant vertical slice:
//
//	net.minecraft.world.entity.LivingEntity.travelInFluid(Vec3 input):
//	  boolean isFalling = getDeltaMovement().y <= 0.0;     // var 2 (dcmpg; ifgt -> 0 else 1)
//	  double  oldY      = getY();                          // var 3 (unused on the v1 vertical path)
//	  double  baseGravity = getEffectiveGravity();         // var 5 == getGravity() == 0.08 (GRAVITY attr default)
//	  if (isInWater()) travelInWater(input, baseGravity, isFalling, oldY);
//	  else travelInLava(...);
//
//	net.minecraft.world.entity.LivingEntity.travelInWater(Vec3 input, double baseGravity, boolean isFalling, double oldY):
//	  float slowDown = isSprinting() ? 0.9f : getWaterSlowDown();   // getWaterSlowDown() == 0.8f (verified freturn)
//	  float speed    = 0.02f;
//	  float waterWalker = (float) getAttributeValue(WATER_MOVEMENT_EFFICIENCY);  // pig (no soul-speed boots) == 0
//	  if (!onGround()) waterWalker *= 0.5f;
//	  if (waterWalker > 0) { slowDown += (0.54600006f - slowDown) * waterWalker; speed += (getSpeed() - speed) * waterWalker; }
//	  // (waterWalker == 0 for a v1 pig -> this branch is skipped -> slowDown stays 0.8, speed stays 0.02)
//	  if (hasEffect(DOLPHINS_GRACE)) slowDown = 0.96f;     // v1: no mob effects -> skipped
//	  moveRelative(speed, input);  move(SELF, deltaMovement);     // the HORIZONTAL swim step (deferred, see below)
//	  Vec3 movement = getDeltaMovement();
//	  // (onClimbable horizontal-collision branch skipped — a pig in open water is not on a climbable)
//	  movement = movement.multiply(slowDown, 0.800000011920929d, slowDown);   // HORIZONTAL drag 0.8, VERTICAL drag 0.8
//	  setDeltaMovement(getFluidFallingAdjustedMovement(baseGravity, isFalling, movement));
//	  jumpOutOfFluid(oldY);                                // surface bob assist (deferred — FloatGoal covers the bob)
//
//	net.minecraft.world.entity.LivingEntity.getFluidFallingAdjustedMovement(double baseGravity, boolean isFalling, Vec3 movement):
//	  if (baseGravity != 0.0 && !isSprinting()) {
//	    double yd;
//	    if (isFalling && Math.abs(movement.y - 0.005) >= 0.003 && Math.abs(movement.y - baseGravity/16.0) < 0.003)
//	      yd = -0.003;                                     // the "stick at the neutral point" guard
//	    else
//	      yd = movement.y - baseGravity/16.0;              // baseGravity/16 == 0.08/16 == 0.005 (the REDUCED water gravity)
//	    return new Vec3(movement.x, yd, movement.z);
//	  }
//	  return movement;
//
// SCOPE (cited, faithful): this port applies travelInWater's VERTICAL ops — the 0.8 water drag and
// the getFluidFallingAdjustedMovement reduced-gravity adjustment — which is the minimum needed to
// stop the sink and let FloatGoal float the mob (the LIVE-DEBUG A deliverable). The HORIZONTAL swim
// step (moveRelative(0.02, input) + the 0.8 horizontal drag) is a CITED follow-on: a v1 mob's
// horizontal movement in water comes from its navigation/AI moveRelative, which is the deferred mob
// swim-pathing — the horizontal drag is applied here too (vx/vz *= 0.8) so a mob's existing
// horizontal velocity decays at the water rate, but the moveRelative swim-input is left to the AI
// path. WATER_MOVEMENT_EFFICIENCY (0 for a pig), DOLPHINS_GRACE (no effects), onClimbable, and
// jumpOutOfFluid are cited no-ops, structured to slot in unchanged when those subsystems land.

const (
	// waterVerticalDrag is the vertical component of travelInWater's
	// `movement.multiply(slowDown, 0.800000011920929d, slowDown)` — the Y multiplier is the literal
	// 0.800000011920929d (ldc2_w in the bytecode), distinct from slowDown (which feeds X/Z). It is
	// the water vertical velocity drag, REPLACING the dry-air 0.98 drag for an in-water mob.
	//	[VERIFIED javap LivingEntity.travelInWater: ldc2_w #2952 // double 0.800000011920929d on the Y axis.]
	waterVerticalDrag = 0.800000011920929

	// waterGravityDivisor is getFluidFallingAdjustedMovement's `baseGravity / 16.0` divisor (ldc2_w
	// 16.0d): the in-water gravity is baseGravity/16 == 0.08/16 == 0.005, NOT the full dry 0.08. THIS
	// is why a vanilla mob sinks slowly enough for FloatGoal's +0.04 impulse to keep it afloat.
	//	[VERIFIED javap LivingEntity.getFluidFallingAdjustedMovement: ldc2_w #3160 // double 16.0d ; ddiv.]
	waterGravityDivisor = 16.0

	// fluidFallingNeutralOffset / fluidFallingNeutralBand are getFluidFallingAdjustedMovement's
	// "stick at the neutral point" guard literals: when falling and |movement.y - 0.005| >= 0.003 AND
	// |movement.y - baseGravity/16| < 0.003, the vertical velocity is pinned to -0.003 instead of the
	// normal `movement.y - baseGravity/16`. This keeps a near-neutral entity from oscillating.
	//	[VERIFIED javap LivingEntity.getFluidFallingAdjustedMovement: ldc2_w #3156 // double 0.005d ;
	//	 ldc2_w #3158 // double 0.003d ; ldc2_w #3162 // double -0.003d.]
	fluidFallingNeutralOffset = 0.005
	fluidFallingNeutralBand   = 0.003
	fluidFallingNeutralResult = -0.003
)

// travelInWaterVertical is the port of the VERTICAL ops of LivingEntity.travelInWater (+ its
// getFluidFallingAdjustedMovement tail) for a mob *Entity, called from tickPhysics IN PLACE OF the
// dry `vy -= 0.08; vy *= 0.98` whenever mobInWater(e) is true. It mutates e.vx/vy/vz:
//
//   - horizontal: vx,vz *= 0.8 (slowDown == getWaterSlowDown(); the WATER_MOVEMENT_EFFICIENCY boost
//     is 0 for a v1 pig, so slowDown stays the bare 0.8).
//   - vertical:   vy *= 0.8 (waterVerticalDrag), then the getFluidFallingAdjustedMovement gravity
//     adjustment (vy -= baseGravity/16 == 0.005, with the neutral-point guard).
//
// baseGravity is gravityPerTick (0.08 == getEffectiveGravity() for a default living mob with no
// slow-falling — entity.go cites gravityPerTick as the GRAVITY-attribute default). isFalling is the
// `getDeltaMovement().y <= 0.0` test from travelInFluid, computed on the PRE-drag velocity exactly
// as vanilla reads it before travelInWater. PURE (no RNG draw): it cannot perturb the pig oracle's
// per-mob RNG stream — and the dry oracle world never enters water, so this branch never runs there.
//
//	Cite: net.minecraft.world.entity.LivingEntity.travelInWater / getFluidFallingAdjustedMovement /
//	getWaterSlowDown (0.8) / getEffectiveGravity (0.08); travelInFluid (isFalling = deltaMovement.y <= 0).
func travelInWaterVertical(e *Entity) {
	// isFalling = getDeltaMovement().y <= 0.0 — read on the CURRENT (pre-drag) velocity, exactly as
	// travelInFluid computes `var 2` before dispatching to travelInWater. The FloatGoal +0.04 impulse
	// (applied in tickAI, before tickPhysics) has already landed in e.vy, so a freshly-impulsed mob
	// reads isFalling=false (vy > 0) and the neutral-point guard does not fire on it.
	isFalling := e.vy <= 0.0

	// Horizontal water drag: slowDown == getWaterSlowDown() == 0.8 (the WATER_MOVEMENT_EFFICIENCY boost
	// is 0 for a v1 pig, so slowDown is the bare 0.8). This replaces the dry horizontalFriction for an
	// in-water mob — `movement.multiply(slowDown, _, slowDown)`.
	e.vx *= waterSlowDown
	e.vz *= waterSlowDown

	// Vertical water drag: `movement.multiply(_, 0.800000011920929d, _)` — the Y multiplier (distinct
	// from slowDown), applied BEFORE the gravity adjustment, exactly as the bytecode orders it.
	e.vy *= waterVerticalDrag

	// getFluidFallingAdjustedMovement(baseGravity, isFalling, movement): the reduced-gravity vertical
	// adjustment. baseGravity (0.08) != 0 and a v1 mob is never sprinting, so the adjustment always
	// applies (the `baseGravity != 0 && !isSprinting()` guard is true).
	e.vy = fluidFallingAdjustedY(gravityPerTick, isFalling, e.vy)
}

// fluidFallingAdjustedY is the Y-only port of LivingEntity.getFluidFallingAdjustedMovement: given the
// post-drag vertical velocity, return the gravity-adjusted vertical velocity. baseGravity/16 (== 0.005
// for the 0.08 default) is the reduced in-water gravity pull; the neutral-point guard pins a
// near-neutral falling velocity to -0.003 to stop oscillation. The `baseGravity != 0 && !isSprinting()`
// outer guard is always true for a v1 mob (gravity 0.08, never sprinting), so it is folded into the
// caller (travelInWaterVertical) — this helper is the inner `if (baseGravity != 0 && !isSprinting())`
// body, returning the adjusted Y. Cite getFluidFallingAdjustedMovement.
func fluidFallingAdjustedY(baseGravity float64, isFalling bool, movementY float64) float64 {
	reducedGravity := baseGravity / waterGravityDivisor // 0.08 / 16 == 0.005 (the REDUCED water gravity)
	// The neutral-point guard: isFalling && |movementY - 0.005| >= 0.003 && |movementY - baseGravity/16| < 0.003.
	if isFalling &&
		mathAbs(movementY-fluidFallingNeutralOffset) >= fluidFallingNeutralBand &&
		mathAbs(movementY-reducedGravity) < fluidFallingNeutralBand {
		return fluidFallingNeutralResult // pinned to -0.003 at the neutral point
	}
	return movementY - reducedGravity // the normal reduced-gravity pull
}

// mathAbs is math.Abs inlined to keep this file's import set minimal (it imports nothing else). It is
// the |x| the getFluidFallingAdjustedMovement guard uses (Math.abs(double) in the bytecode).
func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
