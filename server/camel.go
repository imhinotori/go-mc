// camel.go -- the Camel (net.minecraft.world.entity.animal.camel.Camel), a 1:1 port from the unobfuscated
// 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this task). Camel is a large desert AbstractHorse that
// SITS and STANDS (a game-time pose toggle), DASHES on a rider jump (a horizontal+vertical velocity burst
// with a 55-tick cooldown), and carries TWO seated riders. Vanilla drives it with a BRAIN; this port lands
// the attributes + spawn + the "visibly alive" passive goal walk PLUS the three signature behaviors:
//
//   1. DASH (Camel.executeRidersJump / onPlayerJump / handleStartJump / tick): the rider jump burst.
//      addDeltaMovement( lookAngle*(1,0,1).normalize() * 22.2222f*f*MOVEMENT_SPEED*blockSpeedFactor
//                        + (0, 1.4285f*f*jumpPower, 0) ); dashCooldown=55; setDashing(true). The tick()
//      dash-clear (once landed/dismounted past the DASH_MINIMUM window) + the cooldown decay + the
//      CAMEL_DASH_READY sound at cooldown==0 are camelAiStep (the tick() port).
//   2. SIT/STAND POSE (Camel.sitDown/standUp/standUpInstantly + CamelAi.RandomSitting minimalPoseTicks 400):
//      LAST_POSE_CHANGE_TICK is a signed game-time stamp -- negative while sitting. getPoseTime =
//      gameTime - abs(stamp). RandomSitting sits an idle, grounded, un-ridden, un-leashed camel once it has
//      held its pose >= 400 ticks (20*20). A sitting camel refuseToMove (blocks its own walk + the ride
//      steer). Water force-stands it (tick()).
//   3. TWO-PASSENGER RIDE (Camel.canAddPassenger size<=2 / doPlayerRide / getControllingPassenger /
//      getRiddenSpeed / getPassengerAttachmentPoint): a right-click mounts up to 2 players; the first is the
//      controlling passenger and steers client-authoritatively (passenger.go handleMoveVehicle), exactly as
//      the boat/happy-ghast ride. getRiddenSpeed reads MOVEMENT_SPEED (+0.1 sprint bonus when off cooldown).
//
// VANILLA (verified javap this session):
//   createAttributes: createBaseHorseAttributes + MAX_HEALTH 32.0 + MOVEMENT_SPEED 0.09000000357627869 +
//     JUMP_STRENGTH 0.41999998688697815 + STEP_HEIGHT 1.5. Under buildKeepingLast the observable finals are
//     MAX_HEALTH 32.0, MOVEMENT_SPEED 0.09, STEP_HEIGHT 1.5, SAFE_FALL_DISTANCE 6.0 (see camelSupplier in
//     level/attribute/defaults.go). isFood == CAMEL_FOOD (cactus). getMaxHeadYRot 30. canAddPassenger
//     size<=2 -> a camel seats TWO riders.
//   CamelAi speed multipliers (constants): PANICKING 4.0, IDLING 2.0, TEMPTED 2.5, FOLLOWING_ADULT 2.5,
//     MAKING_LOVE 1.0.
//
// v1 STUBS (cited): the BRAIN scheduling (the full activity graph) is still the classic-goal stand-in; the
// DATA wire encode of DASH/LAST_POSE_CHANGE_TICK/POSE onto the client metadata slot is the codebase-wide
// mob-metadata deferral (every mob emits an empty metadata body today -- entity_encode.go note) -- the
// SERVER-side pose/dash STATE is faithful and load-bearing (it gates refuseToMove, the ride steer, and the
// dash velocity), structured so the metadata splice slots in later. The JUMP_STRENGTH 0.42 attribute is a
// cite-omitted attribute read via camelJumpPower (the camelSupplier omission mirror). Mth.sin/cos is
// math.Sin/Cos at float32 precision -- the SAME documented deviation passenger.go / ai_goals_rabbit.go take
// (the dash direction is derived from the look vector; the tiny table-vs-libm delta is sub-quantization).

package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Camel constants (VERIFIED javap -constants Camel + CamelAi this session).
const (
	camelMaxHealth     = 32.0                // createAttributes MAX_HEALTH 32.0
	camelMovementSpeed = 0.09000000357627869 // createAttributes MOVEMENT_SPEED (float-widened)
	camelStepHeight    = 1.5                 // createAttributes STEP_HEIGHT 1.5 (tall-block step-up)
	camelFoodTag       = "camel_food"        // CAMEL_FOOD (cactus): the tempt/breed predicate

	// CamelAi.SPEED_MULTIPLIER_WHEN_* -- the brain-activity walk paces (1:1 with the jar constants). The
	// classic-goal stand-in below uses these EXACT values (was Pig defaults 1.25/1.25/1.25/1.0 before this
	// port). PANICKING 4.0, TEMPTED 2.5, FOLLOWING_ADULT 2.5, IDLING(stroll) 2.0, MAKING_LOVE(breed) 1.0.
	camelPanicSpeed   = 4.0 // SPEED_MULTIPLIER_WHEN_PANICKING
	camelTemptSpeed   = 2.5 // SPEED_MULTIPLIER_WHEN_TEMPTED
	camelFollowSpeed  = 2.5 // SPEED_MULTIPLIER_WHEN_FOLLOWING_ADULT
	camelStrollSpeed  = 2.0 // SPEED_MULTIPLIER_WHEN_IDLING (RandomStroll)
	camelBreedSpeed   = 1.0 // SPEED_MULTIPLIER_WHEN_MAKING_LOVE
	camelLookDistance = 8.0 // LookAtTargetSink distance (the common animal look range)

	// Camel dash/pose constants (Camel.class -constants).
	camelDashCooldownTicks      = 55      // DASH_COOLDOWN_TICKS -- executeRidersJump sets dashCooldown = 55
	camelDashHorizontalMomentum = 22.2222 // DASH_HORIZONTAL_MOMENTUM (float)
	camelDashVerticalMomentum   = 1.4285  // DASH_VERTICAL_MOMENTUM (float)
	camelDashMinimumDuration    = 5       // DASH_MINIMUM_DURATION_TICKS -- the tick() dash-clear window
	camelSitdownDuration        = 40      // SITDOWN_DURATION_TICKS -- getPoseTime target while sitting
	camelStandupDuration        = 52      // STANDUP_DURATION_TICKS -- getPoseTime target while standing
	camelRunningSpeedBonus      = 0.1     // RUNNING_SPEED_BONUS -- getRiddenSpeed sprint add

	// Camel.getJumpPower reads the JUMP_STRENGTH attribute (0.41999998688697815). Sulfur registers no
	// JUMP_STRENGTH attribute, so camelJumpPower is a CITED const equal to the vanilla base (the camelSupplier
	// omission mirror) -- structured to become getAttributeValue(JUMP_STRENGTH) once the attribute lands.
	//	[VERIFIED javap Camel.createAttributes: JUMP_STRENGTH 0.41999998688697815.]
	camelJumpStrength = 0.41999998688697815

	// CamelAi.RandomSitting: minimalPoseTicks = arg * 20; the sole caller passes 20 -> 400. The idle camel
	// must hold its pose (getPoseTime) at least this long before RandomSitting flips it.
	//	[VERIFIED javap CamelAi$RandomSitting.<init>: minimalPoseTicks = arg * 20; caller arg 20 -> 400.]
	camelRandomSitMinimalPoseTicks = 20 * 20 // 400

	// Camel.canAddPassenger: getPassengers().size() <= 2 -> a camel seats TWO riders.
	//	[VERIFIED javap Camel.canAddPassenger: return getPassengers().size() <= 2.]
	camelMaxPassengers = 2
)

// newCamelAI builds the Camel bounded passive AI. Camel is a BRAIN mob in vanilla (the sit/dash/2-seat
// behaviors are ported explicitly below + in camelAiStep/passenger.go); this supplies the classic-goal
// stand-in walk with the CamelAi speed multipliers (Float/Panic 4.0/Breed 1.0/Tempt 2.5/Follow 2.5/Stroll
// 2.0/Look), mirroring newFrogAI shape (per-mob rng, navigation seed, canFloat, Animal pathfinding malus).
// Cite Camel.makeBrain + CamelAi speed constants.
func newCamelAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * camelMovementSpeed // seed with MOVEMENT_SPEED (0.09)
	m.navigation.canFloat = true                             // Camel ctor setCanFloat(true)
	applyAnimalPathfindingMalus(m)                           // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(camelPanicSpeed)) // CamelPanic(4.0)
	m.goals.addGoal(3, newBreedGoal(camelBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(camelTemptSpeed, func(id int32) bool { return itemInTag(id, camelFoodTag) }, false, nil))
	m.goals.addGoal(5, newFollowParentGoal(camelFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(camelStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(camelLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnCamel creates a Camel at (x,y,z) with the jar attributes and the passive goal AI, then adds it to the
// owner region store. baby toggles the AgeableMob baby age + half-scale box. finalizeSpawn's
// resetLastPoseChangeTickToFullStand seeds LAST_POSE_CHANGE_TICK to a fully-stood value at the current game
// time (a fresh camel stands). initSpawnHealth seeds health from MAX_HEALTH (32.0). Cite Camel.finalizeSpawn
// (resetLastPoseChangeTickToFullStand) + Camel.createAttributes.
func (t *TickLoop) spawnCamel(x, y, z float64, baby bool) *Entity {
	c := NewEntity(t.idAlloc.AllocID(), entity.Camel, x, y, z)
	c.isCamel = true
	if baby {
		c.breedAge = babyStartAge
		c.refreshDimensions() // AgeableMob baby half-scale box (getDefaultDimensions baby-scale)
	}
	initSpawnHealth(c) // setHealth(getMaxHealth()) -> 32.0
	c.ai = newCamelAI()
	reseedMobAI(c.ai, c.id)
	// finalizeSpawn: resetLastPoseChangeTickToFullStand(getGameTime()). Vanilla seeds a fully-stood pose so
	// getPoseTime is already past the standup window on the first tick. Cite Camel.finalizeSpawn.
	c.camelResetLastPoseChangeTickToFullStand(t.gametime)
	owner := t.regionForEntity(c)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(c)
	return c
}

// --- SIT/STAND POSE ------------------------------------------------------------------------------------

// camelResetLastPoseChangeTick ports Camel.resetLastPoseChangeTick(long): store the signed game-time stamp
// into LAST_POSE_CHANGE_TICK. A NEGATIVE stamp marks a sitting camel (isCamelSitting).
//
//	[VERIFIED javap Camel.resetLastPoseChangeTick: entityData.set(LAST_POSE_CHANGE_TICK, Long.valueOf(l)).]
func (e *Entity) camelResetLastPoseChangeTick(stamp int64) { e.camelLastPoseChangeTick = stamp }

// camelResetLastPoseChangeTickToFullStand ports Camel.resetLastPoseChangeTickToFullStand(long gameTime):
// resetLastPoseChangeTick( max(0, gameTime - 52 - 1) ) -- a positive stamp far enough in the past that
// getPoseTime already exceeds the STANDUP window (the camel is fully stood, not mid-transition).
//
//	[VERIFIED javap Camel.resetLastPoseChangeTickToFullStand: resetLastPoseChangeTick(Math.max(0L, l-52L-1L)).]
func (e *Entity) camelResetLastPoseChangeTickToFullStand(gameTime int64) {
	stamp := gameTime - 52 - 1
	if stamp < 0 {
		stamp = 0
	}
	e.camelResetLastPoseChangeTick(stamp)
}

// camelIsSitting ports Camel.isCamelSitting(): the LAST_POSE_CHANGE_TICK stamp is negative.
//
//	[VERIFIED javap Camel.isCamelSitting: entityData.get(LAST_POSE_CHANGE_TICK) < 0L.]
func (e *Entity) camelIsSitting() bool { return e.camelLastPoseChangeTick < 0 }

// camelGetPoseTime ports Camel.getPoseTime(): gameTime - abs(LAST_POSE_CHANGE_TICK) -- the ticks elapsed
// since the last pose change (a positive value once the transition/hold has begun).
//
//	[VERIFIED javap Camel.getPoseTime: level().getGameTime() - Math.abs(entityData.get(LAST_POSE_CHANGE_TICK)).]
func (e *Entity) camelGetPoseTime(gameTime int64) int64 {
	stamp := e.camelLastPoseChangeTick
	if stamp < 0 {
		stamp = -stamp
	}
	return gameTime - stamp
}

// camelIsInPoseTransition ports Camel.isInPoseTransition(): getPoseTime < (isCamelSitting ? 40 : 52) -- the
// camel is still animating into its new pose (SITDOWN_DURATION / STANDUP_DURATION).
//
//	[VERIFIED javap Camel.isInPoseTransition: getPoseTime() < (isCamelSitting()? 40L : 52L).]
func (e *Entity) camelIsInPoseTransition(gameTime int64) bool {
	limit := int64(camelStandupDuration)
	if e.camelIsSitting() {
		limit = int64(camelSitdownDuration)
	}
	return e.camelGetPoseTime(gameTime) < limit
}

// camelRefuseToMove ports Camel.refuseToMove(): isCamelSitting() || isInPoseTransition() -- a sitting or
// mid-transition camel does not move (its own walk is zeroed and the ride steer is blocked). Consumed by
// getControllingPassenger (the ride).
//
//	[VERIFIED javap Camel.refuseToMove: isCamelSitting() || isInPoseTransition().]
func (e *Entity) camelRefuseToMove(gameTime int64) bool {
	return e.camelIsSitting() || e.camelIsInPoseTransition(gameTime)
}

// camelSitDown ports Camel.sitDown(): if already sitting, no-op; else set POSE=SITTING (cited render pose)
// and resetLastPoseChangeTick(-gameTime) (a NEGATIVE stamp -> isCamelSitting true). The CAMEL_SIT sound +
// ENTITY_ACTION game event are cited no-ops (no sound/gameevent subsystem for a mob here).
//
//	[VERIFIED javap Camel.sitDown: if isCamelSitting return; makeSound(CAMEL_SIT); setPose(SITTING);
//	 gameEvent(ENTITY_ACTION); resetLastPoseChangeTick(-level().getGameTime()).]
func (t *TickLoop) camelSitDown(e *Entity) {
	if e.camelIsSitting() {
		return
	}
	e.camelResetLastPoseChangeTick(-t.gametime)
}

// camelStandUp ports Camel.standUp(): if not sitting, no-op; else set POSE=STANDING (cited) and
// resetLastPoseChangeTick(+gameTime). The CAMEL_STAND sound + ENTITY_ACTION game event are cited no-ops.
//
//	[VERIFIED javap Camel.standUp: if !isCamelSitting return; makeSound(CAMEL_STAND); setPose(STANDING);
//	 gameEvent(ENTITY_ACTION); resetLastPoseChangeTick(level().getGameTime()).]
func (t *TickLoop) camelStandUp(e *Entity) {
	if !e.camelIsSitting() {
		return
	}
	e.camelResetLastPoseChangeTick(t.gametime)
}

// camelStandUpInstantly ports Camel.standUpInstantly(): set POSE=STANDING (cited) + ENTITY_ACTION (cited
// no-op) + resetLastPoseChangeTickToFullStand(gameTime) -- a hard stand with no transition (used by the
// in-water force-stand + actuallyHurt).
//
//	[VERIFIED javap Camel.standUpInstantly: setPose(STANDING); gameEvent(ENTITY_ACTION);
//	 resetLastPoseChangeTickToFullStand(level().getGameTime()).]
func (t *TickLoop) camelStandUpInstantly(e *Entity) {
	e.camelResetLastPoseChangeTickToFullStand(t.gametime)
}

// camelCanChangePose ports Camel.canCamelChangePose(): wouldNotSuffocateAtTargetPose(sitting? STANDING :
// SITTING). Sulfur has no per-pose suffocation box check for the camel (the sit/stand boxes both fit the
// same footprint on a flat floor), so this is a CITED const-true -- structured to become a real
// wouldNotSuffocateAtTargetPose collision test once per-pose dims land. Cite Camel.canCamelChangePose.
func (e *Entity) camelCanChangePose() bool { return true }

// --- DASH ----------------------------------------------------------------------------------------------

// camelSetDashing ports Camel.setDashing(boolean): the DASH EntityDataAccessor write.
//
//	[VERIFIED javap Camel.setDashing: entityData.set(DASH, Boolean.valueOf(b)).]
func (e *Entity) camelSetDashing(b bool) { e.camelDashing = b }

// camelIsDashing ports Camel.isDashing(): the DASH EntityDataAccessor read.
//
//	[VERIFIED javap Camel.isDashing: entityData.get(DASH).]
func (e *Entity) camelIsDashing() bool { return e.camelDashing }

// camelJumpPower ports Camel.getJumpPower() -> LivingEntity.getJumpPower() reading the JUMP_STRENGTH
// attribute. Sulfur registers no JUMP_STRENGTH attribute -> the CITED camelJumpStrength const (the vanilla
// base). LivingEntity.getJumpPower is getAttributeValue(JUMP_STRENGTH) * blockJumpFactor; blockJumpFactor
// is 1.0 off honey/soul-sand (no consumer here) -> just the strength. Cite LivingEntity.getJumpPower.
func (e *Entity) camelJumpPower() float64 { return camelJumpStrength }

// camelExecuteRidersJump ports Camel.executeRidersJump(float f, Vec3 riderInput): the dash burst. f is the
// player-jump pending scale (1.0 at a full charge). The added velocity is:
//
//	d = getJumpPower()                                   // JUMP_STRENGTH (0.42)
//	horiz = lookAngle.multiply(1,0,1).normalize()
//	         .scale( 22.2222f*f * MOVEMENT_SPEED * blockSpeedFactor )
//	burst = horiz.add( 0, 1.4285f*f * d, 0 )
//	addDeltaMovement(burst); dashCooldown = 55; setDashing(true); needsSync = true
//
// blockSpeedFactor is Entity.getBlockSpeedFactor() (1.0 off soul-sand/honey; no consumer here -> 1.0). The
// float32 casts on 22.2222f/1.4285f and the f multiply match the jar's f-arithmetic exactly. needsSync is a
// cited no-op (the tracker re-reads velocity each tick -- entity_collision.go note). Cite
// Camel.executeRidersJump.
//
//	[VERIFIED javap Camel.executeRidersJump: d = getJumpPower(); addDeltaMovement( getLookAngle()
//	 .multiply(1,0,1).normalize().scale(22.2222f*f * getAttributeValue(MOVEMENT_SPEED) * getBlockSpeedFactor())
//	 .add(0, 1.4285f*f * d, 0) ); dashCooldown = 55; setDashing(true); needsSync = true.]
func (e *Entity) camelExecuteRidersJump(f float32) {
	d := e.camelJumpPower()
	// lookAngle = calculateViewVector(xRot, yRot) (unit vector). multiply(1,0,1) drops the Y; normalize.
	lx, _, lz := playerViewVector(e.yaw, e.pitch)
	horizLen := math.Sqrt(lx*lx + lz*lz)
	var nx, nz float64
	if horizLen > 1.0e-4 { // Vec3.normalize returns ZERO for a ~zero-length vector (looking straight up/down)
		nx = lx / horizLen
		nz = lz / horizLen
	}
	blockSpeedFactor := 1.0 // getBlockSpeedFactor() -- 1.0 off soul-sand/honey (cited no consumer)
	scale := float64(float32(camelDashHorizontalMomentum)*f) * e.getAttributeValue(attribute.MovementSpeed) * blockSpeedFactor
	burstX := nx * scale
	burstY := float64(float32(camelDashVerticalMomentum)*f) * d
	burstZ := nz * scale
	// addDeltaMovement(burst)
	e.vx += burstX
	e.vy += burstY
	e.vz += burstZ
	e.camelDashCooldown = camelDashCooldownTicks // = 55
	e.camelSetDashing(true)
	// needsSync = true: cited no-op (the tracker re-reads velocity each tick).
}

// camelIsSaddled is the v1 stub for Camel.isSaddled() (AbstractHorse.isSaddled -> EquipmentSlot.SADDLE
// presence). Sulfur wires no saddle-slot item, and the camel ride is allowed unconditionally by right-click
// (tryCamelRide, the same reduction the happy-ghast harness takes via happyGhastHasHarness) -- so this is a
// CITED const-true: a mounted camel can dash in v1. It is structured to become a real hasItemInSlot(SADDLE)
// read once the equipment surface lands -- never baking the dash away. Cite AbstractHorse.isSaddled +
// Camel.onPlayerJump (the isSaddled gate).
func camelIsSaddled(e *Entity) bool { return true }

// camelOnPlayerJump ports Camel.onPlayerJump(int charge). The camel override GUARDS: return early unless
// isSaddled() && dashCooldown <= 0 && onGround(). Sulfur has no saddle-slot item -> horseIsSaddled() is the
// shared cited const (see horse.go); a camel is treated as ride-capable so a mounted rider can dash. When
// the guard passes it calls AbstractHorse.onPlayerJump(charge), which (for a saddled mount) sets
// playerJumpPendingScale = getPlayerJumpPendingScale(charge). That pending scale is consumed by
// executeRidersJump on the next ground jump tick (camelAiStep). Cite Camel.onPlayerJump +
// AbstractHorse.onPlayerJump.
//
//	[VERIFIED javap Camel.onPlayerJump: if (isSaddled() && dashCooldown <= 0 && onGround()) super.onPlayerJump
//	 (charge); (the bytecode short-circuits: !isSaddled -> return; dashCooldown>0 && !onGround -> return).]
func (e *Entity) camelOnPlayerJump(charge int) {
	if !camelIsSaddled(e) {
		return
	}
	if e.camelDashCooldown > 0 {
		return
	}
	if !e.onGround {
		return
	}
	e.horsePlayerJumpPendingScale = horseGetPlayerJumpPendingScale(charge)
}

// --- camelAiStep: the customServerAiStep + tick() dash/pose per-tick work ------------------------------

// camelAiStep is the Camel per-tick extra: the tick() dash-clear + dash-cooldown decay + the in-water
// force-stand, plus the CamelAi.RandomSitting brain behavior (the idle sit/stand toggle) and the
// executeRidersJump pending-dash launch. Per-type-gated (typ == entity.Camel.ID). RNG-free.
//
// tick() (Camel.tick, minus the client-only setupAnimationStates / clampHeadRotation render branches):
//
//	if (isDashing() && dashCooldown < 50 && (onGround() || isInLiquid() || isPassenger())) setDashing(false);
//	if (dashCooldown > 0) { if (--dashCooldown == 0) playSound(CAMEL_DASH_READY); }   // cited sound no-op
//	if (isCamelSitting() && isInWater()) standUpInstantly();
//
// Then the pending-dash launch: if a rider armed a dash (horsePlayerJumpPendingScale > 0) and the camel is
// grounded, fire executeRidersJump(scale) and clear the pending scale (the AbstractHorse.aiStep
// playerJumpPendingScale -> executeRidersJump consume). Then RandomSitting (CamelAi): an idle, grounded,
// un-ridden, un-leashed camel that has held its pose >= 400 ticks flips it (a sitting camel stands; a
// standing camel that is not panicking sits).
//
// Cite Camel.tick + Camel.executeRidersJump + AbstractHorse.aiStep (playerJumpPendingScale consume) +
// CamelAi.RandomSitting.
func (t *TickLoop) camelAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	inWater := t.entityInWater(e)
	// --- Camel.tick dash-clear: a dash that has landed / is in liquid / is now a passenger, once the
	// DASH_MINIMUM window has passed (dashCooldown < 50, i.e. 55-5), is cleared.
	if e.camelIsDashing() && e.camelDashCooldown < (camelDashCooldownTicks-camelDashMinimumDuration) &&
		(e.onGround || inWater || e.vehicle != 0) {
		e.camelSetDashing(false)
	}
	// --- Camel.tick dash-cooldown decay: --dashCooldown while > 0; at 0 play CAMEL_DASH_READY (cited no-op).
	if e.camelDashCooldown > 0 {
		e.camelDashCooldown--
		// if e.camelDashCooldown == 0 { playSound(CAMEL_DASH_READY) } -- cited sound no-op.
	}
	// --- Camel.tick in-water force-stand: a sitting camel that ends up in water instantly stands.
	if e.camelIsSitting() && inWater {
		t.camelStandUpInstantly(e)
	}
	// --- AbstractHorse.aiStep pending-dash launch: a rider armed a jump (onPlayerJump set the pending
	// scale); once the camel is grounded the launch fires as executeRidersJump and the pending scale clears.
	if e.horsePlayerJumpPendingScale > 0 && e.onGround {
		e.camelExecuteRidersJump(e.horsePlayerJumpPendingScale)
		e.horsePlayerJumpPendingScale = 0
	}
	// --- CamelAi.RandomSitting (minimalPoseTicks 400): the idle sit/stand toggle. checkExtraStartConditions:
	// !isInWater && getPoseTime >= 400 && !isLeashed && onGround && !hasControllingPassenger && canChangePose.
	// (isLeashed is a cited const-false -- no leash subsystem gates the camel here.)
	if !inWater &&
		e.camelGetPoseTime(t.gametime) >= int64(camelRandomSitMinimalPoseTicks) &&
		e.onGround &&
		t.getControllingPassenger(e) == 0 &&
		e.camelCanChangePose() {
		// start: if isCamelSitting -> standUp; else if !isPanicking -> sitDown.
		if e.camelIsSitting() {
			t.camelStandUp(e)
		} else if !t.isPanicking(e) {
			t.camelSitDown(e)
		}
	}
}
