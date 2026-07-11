package server

// phantom.go -- the Phantom (net.minecraft.world.entity.monster.Phantom), a 1:1 port from the
// unobfuscated 26.2 jar. The Phantom is the flying night hostile that CIRCLES an anchor high above the
// ground, then periodically SWOOPS down at its target (a dive-bomb melee), climbs back, and repeats. It
// burns in daylight like the undead (it is in EntityTypeTags.BURN_IN_DAYLIGHT). Same additive +
// per-type-gated pattern as ghast/blaze/wither: ALL phantom state lives behind the single e.phantom
// pointer (nil for every other entity), so a non-phantom takes the unchanged path, touches no new fields,
// and draws no new RNG (the pig oracle stays byte-identical).
//
// VANILLA (verified javap Phantom + its inner classes this task):
//   ctor: moveTargetPoint = Vec3.ZERO; attackPhase = CIRCLE; xpReward = 5; PhantomMoveControl (speed 0.1).
//   attributes: Monster.createMonsterAttributes (MAX_HEALTH 20 default, ATTACK_DAMAGE default 2).
//     updatePhantomSizeInfo: setBaseValue(ATTACK_DAMAGE, 6 + size); refreshDimensions scale(1 + 0.15*size).
//     setPhantomSize: Mth.clamp(size, 0, 64).
//   travel(input): travelFlying(input, 0.2) -- NO gravity, deltaMovement *= 0.91 (the shared flyer branch).
//   Mob.aiStep -> is(BURN_IN_DAYLIGHT) burnUndead(): isSunBurnTick -> igniteForSeconds(8).
//   goalSelector: 1 AttackStrategyGoal; 2 SweepAttackGoal; 3 CircleAroundAnchorGoal.
//   targetSelector: 1 AttackPlayerTargetGoal (scan reducedTickDelay(60), AABB inflate(16,64,16), nearest).
//   PhantomMoveControl.tick: if horizontalCollision { yRot += 180; speed = 0.1 }; steer deltaMovement
//     toward moveTargetPoint (atan2 yaw/pitch approach + cos/sin decomposition lerped 0.2 into delta);
//     speed accelerates to 1.8 when settled (degreesDifferenceAbs < 3.0) else decays to 0.2.
//   AttackStrategyGoal: start -> nextSweepTick = adjustedTickDelay(10); phase = CIRCLE; anchorAboveTarget.
//     tick -> while CIRCLE, --nextSweepTick; at <= 0 flip SWOOP, re-anchor, nextSweepTick =
//     adjustedTickDelay((8 + nextInt(4))*20) + swoop sound. stop -> re-anchor heightmap + 10 + nextInt(20).
//   setAnchorAboveTarget: anchor = target.blockPos().above(20 + nextInt(20)); clamped to seaLevel+1.
//   SweepAttackGoal (flag MOVE): tick -> moveTargetPoint = (t.x, t.getY(0.5), t.z); if bb.inflate(0.2)
//     intersects target.bb -> doHurtTarget + phase = CIRCLE (levelEvent 1039 deferred); else if
//     horizontalCollision || hurtTime > 0 -> phase = CIRCLE. stop -> setTarget(null); phase = CIRCLE.
//   CircleAroundAnchorGoal (flag MOVE): start -> distance = 5 + nextFloat()*10; height = -4 + nextFloat()*9;
//     clockwise = nextBoolean() ? 1 : -1; selectNext. tick -> occasional nextInt(adjustedTickDelay(350))==0
//     new height; nextInt(adjustedTickDelay(250))==0 distance += 1 (wrap 15 -> 5, flip clockwise);
//     nextInt(adjustedTickDelay(450))==0 new angle + selectNext; touchingTarget -> selectNext; floor/ceiling
//     height nudges + selectNext. selectNext advances angle by clockwise*15deg, recomputes moveTargetPoint =
//     anchor + (distance*cos(angle), -4 + height, distance*sin(angle)). touchingTarget = distSqr < 4.0.
//
// LANDED (bytecode-exact): the attributes (MAX_HEALTH 20 / ATTACK_DAMAGE 6 at size 0), the size bbox +
// damage scaling, the flyer travel (no gravity, 0.91 drift), the daylight burn (shared sunBurnTick,
// phantom added to isSunSensitive), the CIRCLE/SWOOP attack-phase machine (strategy timer, circle orbit,
// swoop dive + melee-on-intersect + climb-back), the anchor point + altitude, the targeting (nearest
// player, 60-tick scan). ALL RNG is on the phantom's OWN mobRandom stream.
//
// DEFERRED (cited): the natural PhantomSpawner (every-night sleepless StatsCounter check spawning 1-4
// phantoms above a player whose STAT_TIME_SINCE_REST > 72000) is DEFERRED -- no per-player time-since-rest
// feed is wired into a nightly spawner pass, so /dbg phantom spawns one for testing (the mob + dive-swoop
// AI + daylight burn are the core, per the task). The client flap particles/sounds (Phantom.tick client
// branch), the swoop levelEvent 1039, and the cat-hiss sweep interrupt are cite-deferred visuals. The
// PhantomLookControl/BodyRotationControl smoothing folds into the move-control yaw write.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Phantom constants (VERIFIED javap Phantom + its inner classes -- every number read off the bytecode).
const (
	phantomXpReward        = 5    // ctor: xpReward = 5 (iconst_5)
	phantomBaseAttackDmg   = 6    // updatePhantomSizeInfo: setBaseValue(ATTACK_DAMAGE, 6 + size) (bipush 6)
	phantomSizeMax         = 64   // setPhantomSize: Mth.clamp(size, 0, 64) (bipush 64)
	phantomBoxScalePerSize = 0.15 // getDefaultDimensions: scale(1.0 + 0.15 * size) (ldc_w 0.15f)
	phantomTravelSpeed     = 0.2  // travel: travelFlying(input, 0.2f) (ldc 0.2f)

	// PhantomMoveControl.tick.
	phantomMoveSpeedInit   = 0.1                  // ctor: speed = 0.1f; horizontalCollision reset (ldc 0.1f)
	phantomMoveVertBias    = 0.699999988079071    // d9 = 1.0 - abs(dy*0.7)/d7 (ldc2_w 0.699999988079071d)
	phantomMoveEpsilon     = 9.999999747378752e-6 // abs(d7) > 1e-5 gate (ldc2_w)
	phantomMoveYawApproach = 4.0                  // Mth.approachDegrees(f15, f16, 4.0) (ldc 4.0f)
	phantomMoveSettledDeg  = 3.0                  // degreesDifferenceAbs < 3.0 -> accelerate (ldc 3.0f)
	phantomMoveSpeedFast   = 1.8                  // approach(speed, 1.8, 0.005*(1.8/speed)) (ldc 1.8f)
	phantomMoveAccelFast   = 0.005                // the fast-approach step base (ldc 0.005f)
	phantomMoveSpeedSlow   = 0.2                  // approach(speed, 0.2, 0.025) (ldc 0.2f)
	phantomMoveAccelSlow   = 0.025                // approach(speed, 0.2, 0.025) (ldc 0.025f)
	phantomMoveDeltaLerp   = 0.2                  // setDeltaMovement(dm + (goal - dm).scale(0.2)) (ldc2_w 0.2d)
	phantomDegPerRadPitch  = 57.2957763671875     // pitch: -(atan2(-dy,d7) * 57.2957763671875) (ldc2_w)
	phantomDegPerRadYaw    = 57.295776            // yaw: wrapDegrees(f14 * 57.295776) (ldc 57.295776f)
	phantomDeg2Rad         = 0.017453292          // f18*0.017453292 (ldc 0.017453292f)

	// PhantomAttackStrategyGoal.
	phantomStrategyStartTick = 10 // start: nextSweepTick = adjustedTickDelay(10) (bipush 10)
	phantomSwoopBaseSecs     = 8  // tick: nextSweepTick = adjustedTickDelay((8 + nextInt(4)) * 20) (bipush 8)
	phantomSwoopJitter       = 4  // ... nextInt(4) (iconst_4)
	phantomTicksPerSecond    = 20 // ... * 20 (bipush 20)

	phantomFinalizeAnchorAbove = 5 // finalizeSpawn: anchorPoint = blockPosition().above(5) (iconst_5)

	// setAnchorAboveTarget + stop() re-anchor.
	phantomAnchorAboveBase   = 20 // anchor = target.blockPosition().above(20 + nextInt(20)) (bipush 20)
	phantomAnchorAboveJitter = 20 // ... nextInt(20) (bipush 20)
	phantomStopAnchorBase    = 10 // stop: heightmapPos.above(10 + nextInt(20)) (bipush 10)
	phantomStopAnchorJitter  = 20 // ... nextInt(20) (bipush 20)

	// PhantomSweepAttackGoal.
	phantomSweepMidYFrac   = 0.5                 // moveTargetPoint y = target.getY(0.5) (ldc2_w 0.5d)
	phantomSweepBoxInflate = 0.20000000298023224 // bb.inflate(0.2) intersects target.bb (ldc2_w)
	phantomCatSearchDelay  = 20                  // canContinueToUse: catSearchTick = tickCount + 20 (bipush 20)
	phantomCatSearchRange  = 16.0                // canContinueToUse: getBoundingBox().inflate(16) Cat scan (ldc2_w 16.0d)

	// PhantomCircleAroundAnchorGoal.
	phantomCircleDistBase     = 5.0  // start: distance = 5 + nextFloat()*10 (ldc 5.0f)
	phantomCircleDistSpan     = 10.0 // ... nextFloat() * 10 (ldc 10.0f)
	phantomCircleHeightBase   = -4.0 // start/tick: height = -4 + nextFloat()*9 (ldc -4.0f)
	phantomCircleHeightSpan   = 9.0  // ... nextFloat() * 9 (ldc 9.0f)
	phantomCircleHeightTick   = 350  // tick: nextInt(adjustedTickDelay(350))==0 -> new height (sipush 350)
	phantomCircleDistTick     = 250  // tick: nextInt(adjustedTickDelay(250))==0 -> distance += 1 (sipush 250)
	phantomCircleDistMax      = 15.0 // ... if distance > 15 -> reset 5, flip clockwise (ldc 15.0f)
	phantomCircleAngleTick    = 450  // tick: nextInt(adjustedTickDelay(450))==0 -> new angle (sipush 450)
	phantomCircleAngleStepDeg = 15.0 // selectNext: angle += clockwise * 15 * deg2rad (ldc 15.0f)
	phantomTouchingSqr        = 4.0  // touchingTarget: distanceToSqr < 4.0 (ldc2_w 4.0d)

	// PhantomAttackPlayerTargetGoal.
	phantomScanCadence = 60   // canUse: nextScanTick = reducedTickDelay(60) (bipush 60)
	phantomScanRangeXZ = 16.0 // getNearbyPlayers AABB inflate(16, 64, 16) (ldc2_w 16.0d)
	phantomScanRangeY  = 64.0 // ... inflate(_, 64, _) (ldc2_w 64.0d)
)

// phantomAttackPhase mirrors Phantom$AttackPhase (CIRCLE=0 orbit the anchor, SWOOP=1 dive at the target).
type phantomAttackPhase int32

const (
	phantomPhaseCircle phantomAttackPhase = iota // AttackPhase.CIRCLE (the ctor default)
	phantomPhaseSwoop                            // AttackPhase.SWOOP
)

// phantomState holds all Phantom-specific tick state behind the single e.phantom pointer (a non-phantom
// entity touches none of it). Cite Phantom fields + its three goals' fields.
type phantomState struct {
	moveTargetX, moveTargetY, moveTargetZ float64
	attackPhase                           phantomAttackPhase
	anchorX, anchorY                      int
	anchorZ                               int
	hasAnchor                             bool
	moveSpeed                             float64
	nextSweepTick                         int32
	strategyRunning                       bool
	angle                                 float32
	distance                              float32
	height                                float32
	clockwise                             float32
	circleRunning                         bool
	nextScanTick                          int32
	// PhantomSweepAttackGoal cat-scare state (canContinueToUse). catSearchCooldown models the vanilla
	// `if (tickCount > catSearchTick)` 20-tick cadence (catSearchTick = tickCount + 20) as a countdown that
	// fires on the first swoop check then every 20 ticks. isScaredOfCat mirrors the field of the same name.
	catSearchCooldown int32
	isScaredOfCat     bool
}

// spawnPhantom creates a Phantom at (x,y,z) at phantom size 0, seeds ATTACK_DAMAGE to 6 (updatePhantomSize
// Info at size 0: 6 + 0) + scales the box (1.0 + 0.15*0 = 1.0, so the default 0.9x0.5), and adds it to the
// owner region's store (the tracker broadcasts AddEntity next tick). Minimal e.ai (per-entity rng + the
// attack-target slot); NO goalSelector (the behavior is the code-driven phantomAiStep, like spawnGhast/
// spawnBlaze). initSpawnHealth seeds health from the folded MAX_HEALTH (20.0). Cite Phantom(EntityType,
// Level) + updatePhantomSizeInfo + registerGoals.
func (t *TickLoop) spawnPhantom(x, y, z float64) *Entity {
	p := NewEntity(t.idAlloc.AllocID(), entity.Phantom, x, y, z)
	// Phantom ctor: moveTargetPoint = Vec3.ZERO; attackPhase = CIRCLE; moveControl speed = 0.1f.
	p.phantom = &phantomState{
		attackPhase: phantomPhaseCircle,
		moveSpeed:   phantomMoveSpeedInit,
	}
	_ = phantomXpReward    // ctor xpReward = 5 (no per-Entity xpReward field yet -- cited, like ghast/blaze)
	_ = phantomTravelSpeed // travel: travelFlying(input, 0.2f) -- the physics flyer branch applies 0.91 drag
	// finalizeSpawn: anchorPoint = blockPosition().above(5); setPhantomSize(0). The natural PhantomSpawner
	// (and every spawn egg) routes through finalizeSpawn, so the anchor is seeded here -- the phantom starts
	// with a real orbit anchor 5 blocks above its spawn column instead of lazily anchoring on the first
	// circle selectNext. Cite Phantom.finalizeSpawn.
	p.phantom.anchorX = int(math.Floor(x))
	p.phantom.anchorY = int(math.Floor(y)) + phantomFinalizeAnchorAbove
	p.phantom.anchorZ = int(math.Floor(z))
	p.phantom.hasAnchor = true
	t.setPhantomSize(p, 0) // setPhantomSize(0) -> updatePhantomSizeInfo (ATTACK_DAMAGE 6 + refreshDimensions)
	initSpawnHealth(p)     // LivingEntity ctor setHealth(getMaxHealth()) -> 20.0 (read AFTER the size info)
	p.ai = &mobAI{}
	reseedMobAI(p.ai, p.id)
	owner := t.regionForEntity(p)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(p)
	return p
}

// setPhantomSize ports Phantom.setPhantomSize(int) + updatePhantomSizeInfo: clamp the size to [0,64],
// refreshDimensions (scale the box by 1.0 + 0.15*size), then setBaseValue(ATTACK_DAMAGE, 6 + size). At
// size 0 (the /dbg + natural spawn default) the box is the type default (0.9x0.5) and ATTACK_DAMAGE is 6.
// Cite Phantom.setPhantomSize + updatePhantomSizeInfo.
func (t *TickLoop) setPhantomSize(e *Entity, size int) {
	if size < 0 {
		size = 0
	} else if size > phantomSizeMax {
		size = phantomSizeMax // Mth.clamp(size, 0, 64)
	}
	// refreshDimensions -> scale(1.0 + 0.15 * size). The adult (un-scaled) box is the type default; scale
	// it and re-anchor the adult dims (no baby half-scale on a Phantom -- it is a Monster, never a baby).
	scale := 1.0 + phantomBoxScalePerSize*float64(size)
	e.adultWidth = entity.Phantom.Width * scale
	e.adultHeight = entity.Phantom.Height * scale
	e.width = e.adultWidth
	e.height = e.adultHeight
	// setBaseValue(ATTACK_DAMAGE, 6 + size). The supplier seeded ATTACK_DAMAGE at the registration default
	// 2.0; this OVERRIDES the base to the phantom's size-scaled value (6 at size 0).
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attribute.AttackDamage.Name()); inst != nil {
			inst.SetBaseValue(float64(phantomBaseAttackDmg + size))
		}
	}
}

// phantomIsFlyer reports whether an entity is a Phantom (the tickPhysics flyer gate reads it to take the
// NO-gravity + 0.91-drift branch). Phantom.travel calls travelFlying(input, 0.2f), the same flyer physics
// the ghast uses. Cite Phantom.travel -> travelFlying.
func phantomIsFlyer(e *Entity) bool { return e.phantom != nil }

// phantomTarget reads the phantom's current attack-target player (Mob.getTarget() via e.ai.attackTargetID),
// or nil. Tick-owned (mirrors ghastTarget/blazeTarget/vexTarget).
func (t *TickLoop) phantomTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// phantomAiStep ports the Phantom tick + its goals for ONE phantom, driven per-type from tickAI (gated on
// e.phantom != nil, AFTER serverAiStep -- the empty goalSelector no-op, like ghastAiStep/blazeAiStep). It
// runs in vanilla goal order: the targetSelector (AttackPlayerTargetGoal, the 60-tick nearest-player scan),
// then the goalSelector action goals (1 AttackStrategyGoal the CIRCLE->SWOOP timer, 2 SweepAttackGoal the
// dive+melee, 3 CircleAroundAnchorGoal the orbit), then the PhantomMoveControl deltaMovement steer. The
// daylight burn runs in the shared serverAiStep sun-burn limb (phantom added to isSunSensitive). All RNG is
// on the phantom's OWN mobRandom stream. Cite Phantom + its inner goals.
func (t *TickLoop) phantomAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	ps := e.phantom
	if ps == nil {
		return
	}
	t.phantomAcquireTarget(e)      // targetSelector @1 PhantomAttackPlayerTargetGoal
	t.phantomAttackStrategyGoal(e) // goalSelector @1: the CIRCLE->SWOOP timer (+ anchoring)
	switch ps.attackPhase {
	case phantomPhaseSwoop:
		ps.circleRunning = false // the circle goal cannot run while SWOOP (canUse: phase == CIRCLE)
		// goalSelector lifecycle: the running SweepAttackGoal is checked with canContinueToUse each tick; if
		// it returns false the goal stops (setTarget(null); phase = CIRCLE) and does not tick this frame.
		if !t.phantomSweepCanContinue(e) {
			t.phantomSweepStop(e)
		} else {
			t.phantomSweepAttackGoal(e)
		}
	default: // CIRCLE
		t.phantomCircleAroundAnchorGoal(e)
	}
	t.phantomMoveControlTick(e) // PhantomMoveControl.tick: steer deltaMovement toward moveTargetPoint
}

// phantomCanTargetPlayer ports the TargetingConditions.forCombat() test (== DEFAULT) as it applies to a
// PLAYER candidate in the phantom target scan. forCombat sets isCombat = true; test() then gates the
// player on canBeSeenByAnyone() (LivingEntity: !isSpectator() && isAlive()) AND, on the isCombat branch,
// canBeSeenAsEnemy() (!isInvulnerable() && canBeSeenByAnyone()) && difficulty != PEACEFUL. A creative
// player IS invulnerable (abilities.invulnerable), so canBeSeenAsEnemy is false; a spectator fails
// canBeSeenByAnyone. So a creative OR spectator player is EXCLUDED (the observable gate this v1 wires).
// The line-of-sight / invisibility-range-scaling limbs of test() are cite-deferred (no Sensing /
// getVisibilityPercent subsystem yet); the range-64 limb is subsumed by the inflate(16,64,16) box. Cite
// TargetingConditions.forCombat/test + LivingEntity.canBeSeenByAnyone/canBeSeenAsEnemy + Player
// (isSpectator via GameType.SPECTATOR; creative -> invulnerable).
func phantomCanTargetPlayer(p *tickPlayer) bool {
	if p == nil || p.dead {
		return false // canBeSeenByAnyone: isAlive()
	}
	// !isSpectator() (canBeSeenByAnyone) && !isInvulnerable()==!creative (canBeSeenAsEnemy).
	return p.gameMode != gameModeSpectator && p.gameMode != gameModeCreative
}

// phantomAcquireTarget ports Phantom$PhantomAttackPlayerTargetGoal (canUse/canContinueToUse folded).
// canContinueToUse: keep the current target only while canAttack(target, TargetingConditions.DEFAULT) holds
// (NOT merely "alive") -- a target that goes creative/spectator/dead is dropped. canUse: every
// reducedTickDelay(60) ticks, getNearbyPlayers(forCombat().range(64), this, bb.inflate(16,64,16)), sort the
// list by Comparator.comparing(Entity::getY).reversed() (HIGHEST Y first), then take the FIRST that passes
// canAttack(candidate, DEFAULT) and setTarget it. NOT nearest -- highest. RNG-free. Cite
// Phantom$PhantomAttackPlayerTargetGoal.canUse/canContinueToUse.
func (t *TickLoop) phantomAcquireTarget(e *Entity) {
	if e.ai == nil {
		return
	}
	ps := e.phantom
	if e.ai.attackTargetID != 0 { // canContinueToUse: keep only while canAttack(DEFAULT) holds
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p != nil && phantomCanTargetPlayer(p) {
			return
		}
		e.ai.attackTargetID = 0 // target gone / no longer attackable (creative/spectator/dead)
	}
	if ps.nextScanTick > 0 { // canUse cadence gate (a scan only fires at <= 0)
		ps.nextScanTick--
		return
	}
	ps.nextScanTick = int32(reducedTickDelay(phantomScanCadence)) // nextScanTick = reducedTickDelay(60)
	// getNearbyPlayers(forCombat().range(64), this, bb.inflate(16,64,16)), then sort by getY DESC and take the
	// FIRST canAttack(DEFAULT). The forCombat().range(64) prefilter and the per-candidate canAttack(DEFAULT)
	// are the SAME forCombat test (DEFAULT == forCombat()), realized once as phantomCanTargetPlayer; the
	// range-64 limb is subsumed by the inflate box. Picking the max-Y attackable player == sort-DESC-take-first.
	var best *tickPlayer
	for _, p := range t.players {
		if !phantomCanTargetPlayer(p) {
			continue
		}
		if math.Abs(p.x-e.x) > phantomScanRangeXZ || math.Abs(p.z-e.z) > phantomScanRangeXZ {
			continue
		}
		if math.Abs(p.y-e.y) > phantomScanRangeY {
			continue
		}
		if best == nil || p.y > best.y { // Comparator.comparing(Entity::getY).reversed() -> highest Y
			best = p
		}
	}
	if best != nil {
		e.ai.attackTargetID = best.entityID // setTarget(highest-Y attackable player)
	}
}

// phantomAttackStrategyGoal ports Phantom$PhantomAttackStrategyGoal (canUse/start/stop/tick folded). canUse
// = target != null && canAttack. On target ACQUISITION it runs start (nextSweepTick = adjustedTickDelay(10),
// phase = CIRCLE, setAnchorAboveTarget). While it has a target and the phase is CIRCLE it decrements next
// SweepTick; at <= 0 it flips to SWOOP, re-anchors above the target, arms nextSweepTick = adjustedTickDelay(
// (8 + nextInt(4)) * 20), and plays the swoop sound (deferred). On target LOSS it runs stop (re-anchor to a
// fresh heightmap+10+nextInt(20) height). Cite Phantom$PhantomAttackStrategyGoal.
func (t *TickLoop) phantomAttackStrategyGoal(e *Entity) {
	ps := e.phantom
	target := t.phantomTarget(e)
	if target == nil {
		if ps.strategyRunning {
			t.phantomStrategyStop(e) // stop(): re-anchor to a fresh height
			ps.strategyRunning = false
		}
		return
	}
	// start() (once on acquisition): nextSweepTick = adjustedTickDelay(10); phase = CIRCLE; setAnchorAboveTarget.
	if !ps.strategyRunning {
		ps.nextSweepTick = int32(adjustedTickDelay(phantomStrategyStartTick, true)) // phantom is a FULL-RATE Go hook (phantomAiStep, empty goalSelector) -> identity
		ps.attackPhase = phantomPhaseCircle
		t.phantomSetAnchorAboveTarget(e, target)
		ps.strategyRunning = true
	}
	// tick(): if (phase == CIRCLE) { --nextSweepTick; if (nextSweepTick <= 0) { phase = SWOOP;
	//   setAnchorAboveTarget(); nextSweepTick = adjustedTickDelay((8 + nextInt(4)) * 20); playSound(SWOOP); } }
	if ps.attackPhase == phantomPhaseCircle {
		ps.nextSweepTick--
		if ps.nextSweepTick <= 0 {
			ps.attackPhase = phantomPhaseSwoop
			t.phantomSetAnchorAboveTarget(e, target)
			secs := phantomSwoopBaseSecs + int(mobRandom(e).nextInt(phantomSwoopJitter))  // 8 + nextInt(4)
			ps.nextSweepTick = int32(adjustedTickDelay(secs*phantomTicksPerSecond, true)) // * 20 (FULL-RATE hook -> identity)
			// playSound(PHANTOM_SWOOP, 10.0, 0.95 + nextFloat()*0.1): the swoop sound is a client-visual
			// (cite-deferred like the ghast/blaze level events), but the nextFloat() draw is PRESERVED so the
			// phantom's RNG stream stays in vanilla lockstep. Cite Phantom$PhantomAttackStrategyGoal.tick.
			_ = mobRandom(e).nextFloat()
		}
	}
}

// phantomSetAnchorAboveTarget ports setAnchorAboveTarget: anchor = target.blockPosition().above(20 +
// nextInt(20)), clamped so its Y >= seaLevel+1. v1 uses the world sea level (overworldSeaLevel). Cite
// Phantom$PhantomAttackStrategyGoal.setAnchorAboveTarget.
func (t *TickLoop) phantomSetAnchorAboveTarget(e *Entity, target *tickPlayer) {
	ps := e.phantom
	ax := int(math.Floor(target.x))
	ay := int(math.Floor(target.y)) + phantomAnchorAboveBase + int(mobRandom(e).nextInt(phantomAnchorAboveJitter))
	az := int(math.Floor(target.z))
	if ay < overworldSeaLevel { // if (anchor.getY() < level.getSeaLevel()) anchor.y = seaLevel + 1
		ay = overworldSeaLevel + 1
	}
	ps.anchorX, ps.anchorY, ps.anchorZ = ax, ay, az
	ps.hasAnchor = true
}

// phantomStrategyStop ports Phantom$PhantomAttackStrategyGoal.stop: if there is an anchor, re-anchor to the
// MOTION_BLOCKING heightmap column at the anchor XZ, raised by 10 + nextInt(20). v1 has no heightmap lookup,
// so it uses the phantom's current Y floor as the heightmap stand-in (the observable "the phantom drifts
// back up to a high idle anchor when it loses its target"). Cite Phantom$PhantomAttackStrategyGoal.stop.
func (t *TickLoop) phantomStrategyStop(e *Entity) {
	ps := e.phantom
	if !ps.hasAnchor {
		return
	}
	base := int(math.Floor(e.y)) // heightmap(MOTION_BLOCKING, anchor).getY() stand-in
	ps.anchorY = base + phantomStopAnchorBase + int(mobRandom(e).nextInt(phantomStopAnchorJitter))
}

// phantomSweepCanContinue ports Phantom$PhantomSweepAttackGoal.canContinueToUse (the goalSelector gate that
// keeps the swoop running). In vanilla order:
//  1. target == null                                          -> false
//  2. !target.isAlive()                                       -> false
//  3. target instanceof Player && (isSpectator || isCreative) -> false (a mid-swoop creative/spectator abort)
//  4. !canUse()   (canUse == target != null && phase == SWOOP)-> false
//  5. every 20 ticks (tickCount > catSearchTick): getEntitiesOfClass(Cat, bb.inflate(16), ENTITY_STILL_ALIVE),
//     hiss() each (cite-deferred visual), isScaredOfCat = !list.isEmpty()
//  6. return !isScaredOfCat
//
// The 20-tick cadence is modelled by a countdown (catSearchCooldown) that fires on the first swoop check then
// every 20 ticks -- observably identical to the "if (tickCount > catSearchTick) catSearchTick = tickCount + 20"
// cadence. Cite Phantom$PhantomSweepAttackGoal.canContinueToUse.
func (t *TickLoop) phantomSweepCanContinue(e *Entity) bool {
	ps := e.phantom
	target := t.phantomTarget(e) // getTarget(); the phantomTarget helper already drops null/dead players
	if target == nil {
		return false // target == null || !isAlive()
	}
	// target instanceof Player && (isSpectator() || isCreative()) -> false.
	if target.gameMode == gameModeSpectator || target.gameMode == gameModeCreative {
		return false
	}
	if ps.attackPhase != phantomPhaseSwoop { // !canUse() (canUse requires phase == SWOOP)
		return false
	}
	// if (tickCount > catSearchTick) { catSearchTick = tickCount + 20; isScaredOfCat = !cats.isEmpty(); }
	if ps.catSearchCooldown <= 0 {
		ps.catSearchCooldown = phantomCatSearchDelay
		ps.isScaredOfCat = t.phantomCatNearby(e)
	} else {
		ps.catSearchCooldown--
	}
	return !ps.isScaredOfCat // return !isScaredOfCat
}

// phantomCatNearby ports the canContinueToUse Cat scan: getEntitiesOfClass(Cat.class, getBoundingBox()
// .inflate(16), EntitySelector.ENTITY_STILL_ALIVE) -- is there at least one live Cat within the inflate(16)
// box. The hiss() side effect on each cat is a cite-deferred client visual (no per-cat sound event wired).
// The broad-phase uses the OWNING-region store (t.cur().entities.near -- the same seam the cat/breed goals
// use) at a 2-chunk radius (covers the +/-16-block inflate across a chunk boundary), then an exact inflated-
// AABB + type re-check per candidate. Cite Phantom$PhantomSweepAttackGoal.canContinueToUse (Cat scan).
func (t *TickLoop) phantomCatNearby(e *Entity) bool {
	hw := e.width/2 + phantomCatSearchRange // getBoundingBox().inflate(16): half-width + 16 on X/Z
	loX, hiX := e.x-hw, e.x+hw
	loY, hiY := e.y-phantomCatSearchRange, e.y+e.height+phantomCatSearchRange
	loZ, hiZ := e.z-hw, e.z+hw
	for _, other := range t.cur().entities.near(e.x, e.z, 2) {
		if other == e || other.typ != entity.Cat.ID || other.dead { // Cat.class + ENTITY_STILL_ALIVE (isAlive)
			continue
		}
		if other.x < loX || other.x > hiX || other.y < loY || other.y > hiY || other.z < loZ || other.z > hiZ {
			continue // outside the inflated AABB
		}
		return true // hiss() (cite-deferred); list is non-empty -> scared
	}
	return false
}

// phantomSweepStop ports Phantom$PhantomSweepAttackGoal.stop: setTarget(null); attackPhase = CIRCLE. After a
// swoop ends (target lost/dead/creative/spectator, or a cat scare) the phantom DROPS its target and re-anchors
// -- the re-anchor is the STRATEGY goal stop firing next tick once it, too, sees a null target
// (phantomAttackStrategyGoal -> phantomStrategyStop re-anchors to a fresh heightmap+10+nextInt(20)). Cite
// Phantom$PhantomSweepAttackGoal.stop.
func (t *TickLoop) phantomSweepStop(e *Entity) {
	ps := e.phantom
	if e.ai != nil {
		e.ai.attackTargetID = 0 // setTarget(null)
	}
	ps.attackPhase = phantomPhaseCircle
}

// phantomSweepAttackGoal ports Phantom$PhantomSweepAttackGoal.tick. The phase is SWOOP: set moveTargetPoint to
// (target.x, target.getY(0.5), target.z) so the move-control dives at the target; if the phantom box
// (inflated 0.2) intersects the target box -> doHurtTarget (the dive-bomb melee, ATTACK_DAMAGE 6) + flip back
// to CIRCLE (levelEvent 1039 deferred); else if the phantom hit a wall (horizontalCollision) or was itself
// hurt (hurtTime > 0) -> flip back to CIRCLE. Flipping to CIRCLE ends the goal: NEXT tick canContinueToUse
// returns false (phase != SWOOP) and stop() clears the target + re-anchors. Cite
// Phantom$PhantomSweepAttackGoal.tick.
func (t *TickLoop) phantomSweepAttackGoal(e *Entity) {
	ps := e.phantom
	target := t.phantomTarget(e)
	if target == nil {
		ps.attackPhase = phantomPhaseCircle
		return
	}
	ps.moveTargetX = target.x
	ps.moveTargetY = target.y + phantomSweepMidYFrac*playerHeight // target.getY(0.5)
	ps.moveTargetZ = target.z
	if t.phantomIntersectsPlayer(e, target, phantomSweepBoxInflate) {
		t.phantomDoHurtTarget(e, target)
		ps.attackPhase = phantomPhaseCircle
		return // level.levelEvent(1039, ...): the swoop-attack sound (client-visual, cite-deferred)
	}
	if e.horizontalCollision || e.hurtTime > 0 { // else if (horizontalCollision || hurtTime > 0) phase = CIRCLE
		ps.attackPhase = phantomPhaseCircle
	}
}

// phantomCircleAroundAnchorGoal ports Phantom$PhantomCircleAroundAnchorGoal (canUse/start/tick folded). The
// phase is CIRCLE: on entry it runs start (distance = 5 + nextFloat()*10; height = -4 + nextFloat()*9;
// clockwise = nextBoolean()?1:-1; selectNext). Each tick it rolls the three occasional re-parameterizations
// (new height / distance-step-and-wrap / new-angle), nudges the height when it would dip below the ground or
// rise into a ceiling, and calls selectNext when it reaches the current orbit point (touchingTarget). ALL
// RNG is on the phantom's OWN stream, IN ORDER. Cite Phantom$PhantomCircleAroundAnchorGoal.
func (t *TickLoop) phantomCircleAroundAnchorGoal(e *Entity) {
	ps := e.phantom
	r := mobRandom(e)
	if !ps.circleRunning { // start() (once when CIRCLE begins)
		ps.distance = phantomCircleDistBase + r.nextFloat()*phantomCircleDistSpan            // 5 + nextFloat()*10
		ps.height = float32(phantomCircleHeightBase) + r.nextFloat()*phantomCircleHeightSpan // -4 + nextFloat()*9
		if r.nextBoolean() {
			ps.clockwise = 1.0
		} else {
			ps.clockwise = -1.0 // nextBoolean() ? 1.0f : -1.0f
		}
		t.phantomSelectNext(e)
		ps.circleRunning = true
		return // start does not also run tick this frame (start THIS tick, tick NEXT)
	}
	// if (random.nextInt(adjustedTickDelay(350)) == 0) height = -4 + nextFloat()*9.
	if r.nextInt(adjustedTickDelay(phantomCircleHeightTick, true)) == 0 { // FULL-RATE hook -> identity
		ps.height = float32(phantomCircleHeightBase) + r.nextFloat()*phantomCircleHeightSpan
	}
	// if (random.nextInt(adjustedTickDelay(250)) == 0) { ++distance; if (distance > 15) { distance = 5;
	//   clockwise = -clockwise; } }.
	if r.nextInt(adjustedTickDelay(phantomCircleDistTick, true)) == 0 { // FULL-RATE hook -> identity
		ps.distance += 1.0
		if ps.distance > float32(phantomCircleDistMax) {
			ps.distance = float32(phantomCircleDistBase)
			ps.clockwise = -ps.clockwise
		}
	}
	// if (random.nextInt(adjustedTickDelay(450)) == 0) { angle = nextFloat()*2*PI; selectNext(); }.
	if r.nextInt(adjustedTickDelay(phantomCircleAngleTick, true)) == 0 { // FULL-RATE hook -> identity
		ps.angle = r.nextFloat() * 2.0 * float32(math.Pi)
		t.phantomSelectNext(e)
	}
	if t.phantomTouchingTarget(e) { // if (touchingTarget()) selectNext()
		t.phantomSelectNext(e)
	}
	// if (moveTargetPoint.y < getY() && !isEmptyBlock(below)) { height = max(1.0, height); selectNext(); }.
	if ps.moveTargetY < e.y && !t.phantomIsEmptyBlockBelow(e) {
		ps.height = float32(math.Max(1.0, float64(ps.height)))
		t.phantomSelectNext(e)
	}
	// if (moveTargetPoint.y > getY() && !isEmptyBlock(above)) { height = min(-1.0, height); selectNext(); }.
	if ps.moveTargetY > e.y && !t.phantomIsEmptyBlockAbove(e) {
		ps.height = float32(math.Min(-1.0, float64(ps.height)))
		t.phantomSelectNext(e)
	}
}

// phantomSelectNext ports Phantom$PhantomCircleAroundAnchorGoal.selectNext: if the anchor is null set it to
// the phantom's block position; advance angle += clockwise * 15 * deg2rad; recompute moveTargetPoint =
// atLowerCornerOf(anchor).add(distance*cos(angle), -4 + height, distance*sin(angle)). Cite selectNext.
func (t *TickLoop) phantomSelectNext(e *Entity) {
	ps := e.phantom
	if !ps.hasAnchor {
		ps.anchorX = int(math.Floor(e.x))
		ps.anchorY = int(math.Floor(e.y))
		ps.anchorZ = int(math.Floor(e.z))
		ps.hasAnchor = true
	}
	ps.angle += ps.clockwise * float32(phantomCircleAngleStepDeg) * float32(phantomDeg2Rad)
	// atLowerCornerOf(BlockPos) == (x, y, z) as doubles. cos/sin are Mth.cos/Mth.sin (mthCosf/mthSinf).
	ps.moveTargetX = float64(ps.anchorX) + float64(ps.distance)*float64(mthCosf(ps.angle))
	ps.moveTargetY = float64(ps.anchorY) + float64(phantomCircleHeightBase) + float64(ps.height)
	ps.moveTargetZ = float64(ps.anchorZ) + float64(ps.distance)*float64(mthSinf(ps.angle))
}

// phantomTouchingTarget ports Phantom$PhantomMoveTargetGoal.touchingTarget: moveTargetPoint.distanceToSqr(
// getX(), getY(), getZ()) < 4.0 (the phantom has reached its current orbit point). Cite touchingTarget.
func (t *TickLoop) phantomTouchingTarget(e *Entity) bool {
	ps := e.phantom
	dx := ps.moveTargetX - e.x
	dy := ps.moveTargetY - e.y
	dz := ps.moveTargetZ - e.z
	return dx*dx+dy*dy+dz*dz < phantomTouchingSqr
}

// phantomMoveControlTick ports Phantom$PhantomMoveControl.tick: steer deltaMovement toward moveTargetPoint.
// On a wall (horizontalCollision) it flips 180deg and resets speed to 0.1. It then decomposes the vector to
// the target (the vertical-bias horizontal shrink, the atan2 yaw approach, the pitch, the cos/sin velocity
// components), accelerates speed to 1.8 when the heading is settled (else decays to 0.2), and lerps the
// deltaMovement 0.2 toward the computed goal velocity. RNG-free. Cite Phantom$PhantomMoveControl.tick.
func (t *TickLoop) phantomMoveControlTick(e *Entity) {
	ps := e.phantom
	if e.horizontalCollision {
		e.yaw += 180.0 // setYRot(getYRot() + 180.0f)
		ps.moveSpeed = phantomMoveSpeedInit
	}
	dx := ps.moveTargetX - e.x
	dy := ps.moveTargetY - e.y
	dz := ps.moveTargetZ - e.z
	d7 := math.Sqrt(dx*dx + dz*dz) // horizontal distance
	if math.Abs(d7) <= phantomMoveEpsilon {
		return // abs(d7) > 1e-5 gate: too close horizontally -> no steer this tick
	}
	// d9 = 1.0 - abs(dy * 0.7) / d7; dx *= d9; dz *= d9 (the vertical-bias horizontal shrink).
	d9 := 1.0 - math.Abs(dy*phantomMoveVertBias)/d7
	dx *= d9
	dz *= d9
	d7 = math.Sqrt(dx*dx + dz*dz)           // recomputed horizontal
	d11 := math.Sqrt(dx*dx + dz*dz + dy*dy) // total distance
	f13 := e.yaw                            // pre-approach yaw (for the settled-heading test)
	f14 := float32(math.Atan2(dz, dx))      // (float) Mth.atan2(dz, dx)
	f15 := wrapDegreesF(e.yaw + 90.0)       // wrapDegrees(getYRot() + 90)
	f16 := wrapDegreesF(f14 * float32(phantomDegPerRadYaw))
	e.yaw = mthApproachDegrees(f15, f16, float32(phantomMoveYawApproach)) - 90.0 // approachDegrees - 90
	e.headYaw = e.yaw                                                            // yBodyRot = getYRot()
	// if (degreesDifferenceAbs(f13, getYRot()) < 3.0) speed -> 1.8 (fast) else speed -> 0.2 (slow).
	if mthDegreesDifferenceAbs(f13, e.yaw) < float32(phantomMoveSettledDeg) {
		step := float32(phantomMoveAccelFast) * (float32(phantomMoveSpeedFast) / float32(ps.moveSpeed))
		ps.moveSpeed = float64(mthApproach(float32(ps.moveSpeed), float32(phantomMoveSpeedFast), step))
	} else {
		ps.moveSpeed = float64(mthApproach(float32(ps.moveSpeed), float32(phantomMoveSpeedSlow), float32(phantomMoveAccelSlow)))
	}
	// f17 = (float)(-(Mth.atan2(-dy, d7) * 57.2957763671875)); setXRot(f17).
	f17 := float32(-(math.Atan2(-dy, d7) * phantomDegPerRadPitch))
	e.pitch = f17
	f18 := e.yaw + 90.0 // f18 = getYRot() + 90; the velocity-component angles
	spd := ps.moveSpeed
	d19 := (spd * float64(mthCosf(f18*float32(phantomDeg2Rad)))) * math.Abs(dx/d11) // x component
	d21 := (spd * float64(mthSinf(f18*float32(phantomDeg2Rad)))) * math.Abs(dz/d11) // z component
	d23 := (spd * float64(mthSinf(f17*float32(phantomDeg2Rad)))) * math.Abs(dy/d11) // y component
	// setDeltaMovement(getDeltaMovement().add(new Vec3(d19, d23, d21).subtract(dm).scale(0.2))).
	e.vx += (d19 - e.vx) * phantomMoveDeltaLerp
	e.vy += (d23 - e.vy) * phantomMoveDeltaLerp
	e.vz += (d21 - e.vz) * phantomMoveDeltaLerp
}

// phantomDoHurtTarget ports the swoop-bite doHurtTarget(getServerLevel(this), target): deal ATTACK_DAMAGE
// (6.0 at size 0) to the player through the shared player hurt path (the same shape as blazeDoHurtTarget).
// The damageSource carries attacker = e.id. NO RNG. Cite Phantom(Mob).doHurtTarget.
func (t *TickLoop) phantomDoHurtTarget(e *Entity, target *tickPlayer) {
	dmg := float32(e.getAttributeValue(attribute.AttackDamage)) // (float) getAttributeValue(ATTACK_DAMAGE) == 6.0
	src := damageSourceMobAttack(e.id)                          // getWeaponItem().getDamageSource(this) -> mob_attack
	t.applyDamage(target, src, dmg)                             // target.hurtServer(level, src, f) -- the PLAYER path
}

// phantomIntersectsPlayer reports whether the phantom's feet-anchored AABB (width x height), inflated by
// `inflate` on all sides, intersects the player's collision box. PhantomSweepAttackGoal.tick uses
// getBoundingBox().inflate(0.2).intersects(target.getBoundingBox()). Cite Phantom$PhantomSweepAttackGoal.tick.
func (t *TickLoop) phantomIntersectsPlayer(e *Entity, p *tickPlayer, inflate float64) bool {
	hw := e.width/2 + inflate
	loX, hiX := e.x-hw, e.x+hw
	loY, hiY := e.y-inflate, e.y+e.height+inflate
	loZ, hiZ := e.z-hw, e.z+hw
	return boxIntersectsPlayer(p, loX, loY, loZ, hiX, hiY, hiZ)
}

// phantomIsEmptyBlockBelow / phantomIsEmptyBlockAbove port Level.isEmptyBlock(blockPosition().below(1)) /
// .above(1) for the CircleAroundAnchorGoal ceiling/floor nudge. v1 reuses !isSolidAt (air/water reads as
// empty). Cite Phantom$PhantomCircleAroundAnchorGoal.tick (isEmptyBlock).
func (t *TickLoop) phantomIsEmptyBlockBelow(e *Entity) bool {
	return !t.isSolidAt(blockPosOf(e.x, e.y-1, e.z))
}

func (t *TickLoop) phantomIsEmptyBlockAbove(e *Entity) bool {
	return !t.isSolidAt(blockPosOf(e.x, e.y+1, e.z))
}

// --- Mth float helpers (VERIFIED javap net.minecraft.util.Mth this task) -----------------------------

// mthApproach ports Mth.approach(float value, float target, float delta): step value toward target by
// |delta|, clamped so it never overshoots. VERIFIED: delta = abs(delta); if (value < target) return
// clamp(value + delta, value, target); else return clamp(value - delta, target, value).
func mthApproach(value, target, delta float32) float32 {
	delta = float32(math.Abs(float64(delta)))
	if value < target {
		return mthClampF(value+delta, value, target)
	}
	return mthClampF(value-delta, target, value)
}

// mthDegreesDifference ports Mth.degreesDifference(float from, float to) = wrapDegrees(to - from).
func mthDegreesDifference(from, to float32) float32 {
	return wrapDegreesF(to - from)
}

// mthDegreesDifferenceAbs ports Mth.degreesDifferenceAbs(float from, float to) = abs(degreesDifference).
func mthDegreesDifferenceAbs(from, to float32) float32 {
	return float32(math.Abs(float64(mthDegreesDifference(from, to))))
}

// mthApproachDegrees ports Mth.approachDegrees(float from, float to, float max): approach(from, from +
// degreesDifference(from, to), max). VERIFIED javap Mth.approachDegrees.
func mthApproachDegrees(from, to, max float32) float32 {
	diff := mthDegreesDifference(from, to)
	return mthApproach(from, from+diff, max)
}
