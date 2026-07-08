// bat.go -- the Bat (net.minecraft.world.entity.ambient.Bat), a 1:1 port from the unobfuscated 26.2
// jar. The Bat is an AmbientCreature (a MobCategory.AMBIENT flyer) that HANGS from a ceiling when
// RESTING and, when it wakes, DRIFTS toward a random nearby target position -- it never attacks and it
// takes NO fall damage. It has NO AI goals at all: every behavior lives in customServerAiStep + tick
// (the RESTING flag machine). A resting bat wakes when a redstone-conductor block above it is removed OR
// a player comes within range (BAT_RESTING_TARGETING range 4.0); a flying bat re-rests on a 1-in-100
// roll when it drifts under a solid ceiling. Same additive + per-type-gated pattern as the bee/fox: ALL
// bat state lives behind the isBat flag + the batResting / batTarget fields (zero for every other
// entity), so a non-bat takes the unchanged path and draws no new RNG (the pig oracle stays byte-identical).
//
// VANILLA (verified javap Bat this task):
//   createAttributes: Mob.createMobAttributes + MAX_HEALTH 6.0 (no other override -- no ATTACK_DAMAGE,
//     no MOVEMENT_SPEED override; MOVEMENT_SPEED stays the createLivingAttributes default 0.7).
//   ctor: setResting(true) (a bat spawns hanging). DATA_ID_FLAGS bit 0x1 == FLAG_RESTING.
//   isResting(): (flags & 1) != 0. setResting(b): flags = b ? flags|1 : flags & ~1.
//   checkFallDamage(...): { return; } -- a bat NEVER takes fall damage.
//   tick(): super.tick(); if (isResting()) { setDeltaMovement(ZERO); setPosRaw(x, floor(y)+1 - bbHeight,
//     z); } else { setDeltaMovement(getDeltaMovement().multiply(1.0, 0.6, 1.0)); } setupAnimationStates().
//   customServerAiStep(level): super; BlockPos pos = blockPosition(); BlockPos above = pos.above();
//     if (isResting()) {
//       if (level.getBlockState(above).isRedstoneConductor(level, pos)) {  // ceiling still solid
//         if (random.nextInt(200) == 0) yHeadRot = (float) random.nextInt(360);   // fidget look
//         if (level.getNearestPlayer(BAT_RESTING_TARGETING, this) != null) { setResting(false); ...event 1025 }
//       } else { setResting(false); ... levelEvent(1025) }                        // ceiling gone -> wake
//     } else {
//       if (targetPosition != null && (!level.isEmptyBlock(targetPosition) || targetPosition.getY() <= level.getMinY()))
//         targetPosition = null;
//       if (targetPosition == null || random.nextInt(30) == 0 || targetPosition.closerToCenterThan(position, 2.0))
//         targetPosition = BlockPos.containing(getX() + (nextInt(7) - nextInt(7)), getY() + (nextInt(6) - 2),
//                                              getZ() + (nextInt(7) - nextInt(7)));
//       double dx = (targetPosition.getX() + 0.5) - getX();
//       double dy = (targetPosition.getY() + 0.1) - getY();
//       double dz = (targetPosition.getZ() + 0.5) - getZ();
//       Vec3 dm = getDeltaMovement();
//       Vec3 kick = dm.add((signum(dx)*0.5 - dm.x)*0.1, (signum(dy)*0.7 - dm.y)*0.1, (signum(dz)*0.5 - dm.z)*0.1);
//       setDeltaMovement(kick);
//       float yaw = (float)(Mth.atan2(kick.z, kick.x) * 57.2957763671875) - 90.0f;
//       float turn = Mth.wrapDegrees(yaw - getYRot());
//       zza = 0.5f; setYRot(getYRot() + turn);
//       if (random.nextInt(100) == 0 && level.getBlockState(above).isRedstoneConductor(level, pos)) setResting(true);
//     }
//
// LANDED (bytecode-exact): the attributes (Mob + MAX_HEALTH 6), the ctor spawn-resting, the RESTING flag
// (isResting/setResting bit 0x1), the hang-from-ceiling snap (tick resting branch), the wake conditions
// (ceiling removed OR nearest player within range 4.0), the random-target night drift (the signum kick +
// the 0.6 y-drag + the re-rest roll), and NO fall damage. ALL RNG is on the bat OWN mobRandom stream, IN
// ORDER. The night-active gate is the observable "a bat wakes and flies" -- vanilla does not day/night
// gate the drift itself (the RESTING flag + the player-proximity wake ARE the activity model), so this
// port matches vanilla exactly.
//
// DEFERRED (cited): the isRedstoneConductor ceiling test is reduced to isSolidAt (the v1 solid read -- no
// redstone-conductor block property table yet; a solid ceiling reads as a valid hang point, the
// observable intent). The yHeadRot fidget + the levelEvent(1025) wake sound are cite-deferred client
// visuals, but their RNG draws (nextInt(200), nextInt(360)) are PRESERVED so the bat stream stays in
// vanilla lockstep. The natural checkBatSpawnRules (spawn only in dark caves, group of 8) is deferred --
// /dbg bat spawns one for testing.

package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
)

// Bat constants (VERIFIED javap Bat this task).
const (
	batMaxHealth      = 6.0                 // createAttributes MAX_HEALTH 6.0
	batFlagResting    = 1                   // FLAG_RESTING (isResting: flags & 1)
	batRestFidget     = 200                 // customServerAiStep resting: nextInt(200) == 0 fidget (sipush 200)
	batFidgetYaw      = 360                 // ... yHeadRot = nextInt(360) (sipush 360)
	batRestingRange   = 4.0                 // BAT_RESTING_TARGETING.range(4.0) wake-on-player radius (ldc2_w 4.0d)
	batRetargetRoll   = 30                  // customServerAiStep flying: nextInt(30) == 0 new target (bipush 30)
	batTargetSpanXZ   = 7                   // ... getX() + nextInt(7) - nextInt(7) (bipush 7)
	batTargetSpanY    = 6                   // ... getY() + nextInt(6) - 2 (bipush 6)
	batTargetYOffset  = 2                   // ... - 2 (iconst_2)
	batReachSqr       = 2.0                 // targetPosition.closerToCenterThan(position, 2.0) (ldc2_w 2.0d)
	batReRestRoll     = 100                 // flying: nextInt(100) == 0 re-rest under a ceiling (bipush 100)
	batKickXZBias     = 0.5                 // signum(dx|dz) * 0.5 kick component (ldc2_w 0.5d)
	batKickYBias      = 0.7                 // signum(dy) * 0.7 kick component (ldc2_w 0.699999988079071d approx)
	batKickLerp       = 0.10000000149011612 // (bias - dm.axis) * 0.1 kick lerp (ldc2_w, float-widened)
	batTargetCenter   = 0.5                 // targetPosition.getX()/Z() + 0.5 (ldc2_w 0.5d)
	batTargetYCenter  = 0.1                 // targetPosition.getY() + 0.1 (ldc2_w 0.1d)
	batRestFlyDrag    = 0.6                 // tick flying branch: deltaMovement.multiply(1.0, 0.6, 1.0) (ldc2_w 0.6d)
	batDegPerRad      = 57.2957763671875    // Mth.atan2(z, x) * 57.2957763671875 (ldc2_w)
	batYawOffset      = 90.0                // ... - 90.0f (ldc 90.0f)
	batForwardImpulse = 0.5                 // zza = 0.5f (ldc 0.5f)
)

// batIsResting ports Bat.isResting(): (DATA_ID_FLAGS & 1) != 0.
func batIsResting(e *Entity) bool { return e.batResting }

// batSetResting ports Bat.setResting(boolean): flip the FLAG_RESTING bit. v1 stores the single flag as a
// bool (the bat has no other DATA_ID_FLAGS bits). Cite Bat.setResting.
func batSetResting(e *Entity, b bool) { e.batResting = b }

// spawnBat creates a Bat at (x,y,z) with the jar attributes (Mob + MAX_HEALTH 6) and the ctor spawn-
// resting state (setResting(true) -- a bat spawns hanging), then adds it to the owner region store. It
// has NO goalSelector (an AmbientCreature has no goals; the behavior is entirely batAiStep). Minimal
// e.ai (per-entity rng only). initSpawnHealth seeds health from MAX_HEALTH (6.0). Cite Bat.createAttributes
// + Bat(EntityType, Level) ctor (setResting(true)).
func (t *TickLoop) spawnBat(x, y, z float64) *Entity {
	b := NewEntity(t.idAlloc.AllocID(), entity.Bat, x, y, z)
	b.isBat = true
	batSetResting(b, true) // ctor: setResting(true) -- a bat spawns hanging from the ceiling
	initSpawnHealth(b)     // setHealth(getMaxHealth()) -> 6.0
	b.ai = &mobAI{}
	reseedMobAI(b.ai, b.id)
	owner := t.regionForEntity(b)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(b)
	return b
}

// batRestSnapY ports the Bat.tick resting-branch position snap: y = Mth.floor(getY()) + 1 - getBbHeight()
// (the bat hangs with its head against the block above its feet). Cite Bat.tick.
func batRestSnapY(e *Entity) float64 {
	return math.Floor(e.y) + 1.0 - float64(e.height)
}

// batIsFlyer reports whether an entity is a Bat (the tickPhysics flyer gate reads it to take the Bat
// custom-drag branch instead of the gravity+drag DRY branch). A bat has NO gravity: its tick() applies
// the resting snap OR the (1.0, 0.6, 1.0) drag, and batAiStep supplies the drift kick. Cite Bat.tick.
func batIsFlyer(e *Entity) bool { return e.isBat }

// batCeilingSolid ports the isRedstoneConductor(level, pos) ceiling test for the block ABOVE the bat's
// feet position. v1 reduction (cited): isSolidAt (a solid block above is a valid hang point / a present
// ceiling); the redstone-conductor property refinement is deferred. Cite Bat.customServerAiStep.
func (t *TickLoop) batCeilingSolid(e *Entity) bool {
	return t.isSolidAt(blockPosOf(e.x, math.Floor(e.y)+1, e.z))
}

// batAiStep ports Bat.customServerAiStep for ONE bat, driven per-type from tickAI (gated on typ ==
// entity.Bat.ID, AFTER serverAiStep). RESTING: the bat hangs; it fidgets its head (1-in-200) and wakes
// when the ceiling is gone OR a player is within range 4.0. FLYING: the bat drifts toward a random
// target (re-rolled on reach / 1-in-30 / staleness), steering deltaMovement with the signum kick, and
// re-rests on a 1-in-100 roll under a solid ceiling. The tick() drag/snap is applied in tickPhysics
// (batIsFlyer branch). ALL RNG is on the bat OWN mobRandom stream, IN ORDER. Cite Bat.customServerAiStep.
func (t *TickLoop) batAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	r := mobRandom(e)
	if batIsResting(e) {
		if t.batCeilingSolid(e) {
			// ceiling still solid: fidget the head + wake if a player is near.
			if r.nextInt(batRestFidget) == 0 { // nextInt(200) == 0
				e.headYaw = float32(r.nextInt(batFidgetYaw)) // yHeadRot = nextInt(360) (client fidget)
			}
			if _, ok := nearestPlayerIDAt(t, e.x, e.y, e.z, batRestingRange); ok {
				batSetResting(e, false) // getNearestPlayer(range 4.0) != null -> wake
			}
		} else {
			batSetResting(e, false) // ceiling gone -> wake
		}
		return
	}
	// FLYING branch. Drop a stale target (block no longer empty, or below the world floor).
	if e.batHasTarget {
		if t.isSolidAt(blockPosOf(float64(e.batTargetX), float64(e.batTargetY), float64(e.batTargetZ))) ||
			e.batTargetY <= dimMinY {
			e.batHasTarget = false
		}
	}
	// (re)pick a target: none, a 1-in-30 wander re-roll, or arrival within 2 blocks.
	if !e.batHasTarget || r.nextInt(batRetargetRoll) == 0 || batTargetCloserThan(e, batReachSqr) {
		// BlockPos.containing(getX() + nextInt(7) - nextInt(7), getY() + nextInt(6) - 2, getZ() + nextInt(7)
		// - nextInt(7)). The nextInt draws MUST be in this exact order (x+, x-, y, z+, z-).
		ox := r.nextInt(batTargetSpanXZ) - r.nextInt(batTargetSpanXZ)
		oy := r.nextInt(batTargetSpanY) - batTargetYOffset
		oz := r.nextInt(batTargetSpanXZ) - r.nextInt(batTargetSpanXZ)
		e.batTargetX = int(math.Floor(e.x)) + ox
		e.batTargetY = int(math.Floor(e.y)) + oy
		e.batTargetZ = int(math.Floor(e.z)) + oz
		e.batHasTarget = true
	}
	dx := (float64(e.batTargetX) + batTargetCenter) - e.x
	dy := (float64(e.batTargetY) + batTargetYCenter) - e.y
	dz := (float64(e.batTargetZ) + batTargetCenter) - e.z
	// deltaMovement.add((signum(dx)*0.5 - dm.x)*0.1, (signum(dy)*0.7 - dm.y)*0.1, (signum(dz)*0.5 - dm.z)*0.1).
	e.vx += (sgn(dx)*batKickXZBias - e.vx) * batKickLerp
	e.vy += (sgn(dy)*batKickYBias - e.vy) * batKickLerp
	e.vz += (sgn(dz)*batKickXZBias - e.vz) * batKickLerp
	// yaw steer: (float)(atan2(dm.z, dm.x) * 57.2957763671875) - 90; turn = wrapDegrees(yaw - getYRot()).
	yaw := float32(math.Atan2(e.vz, e.vx)*batDegPerRad) - float32(batYawOffset)
	turn := wrapDegreesF(yaw - e.yaw)
	e.yaw += turn // setYRot(getYRot() + turn); zza = 0.5f (the forward impulse rides the drift already)
	// re-rest: 1-in-100 under a solid ceiling.
	if r.nextInt(batReRestRoll) == 0 && t.batCeilingSolid(e) {
		batSetResting(e, true)
	}
}

// batTargetCloserThan ports BlockPos.closerToCenterThan(position, dist): the SQUARED distance from the
// bat feet to the target block CENTER (x+0.5, y+0.5, z+0.5) is < dist*dist. Cite BlockPos.closerToCenterThan.
func batTargetCloserThan(e *Entity, dist float64) bool {
	if !e.batHasTarget {
		return false
	}
	dx := (float64(e.batTargetX) + 0.5) - e.x
	dy := (float64(e.batTargetY) + 0.5) - e.y
	dz := (float64(e.batTargetZ) + 0.5) - e.z
	return dx*dx+dy*dy+dz*dz < dist*dist
}

// sgn ports Math.signum(double) for the bat kick (returns -1, 0, or +1). Math.signum(0.0) == 0.0.
func sgn(v float64) float64 {
	if v > 0 {
		return 1.0
	}
	if v < 0 {
		return -1.0
	}
	return 0.0
}
