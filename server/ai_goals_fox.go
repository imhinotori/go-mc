package server

// ai_goals_fox.go — the Fox CHARACTER LAYER, ported 1:1 from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, CFR/javap this session): net.minecraft.world.entity.animal.fox.Fox and
// its inner goal classes. These are Go-NATIVE goals (the sibling of ai_goals_creeper.go swellGoal) — the
// fox goals are the SAME class for every fox, so the fox .star DECLARES each goal priority + flags + kind
// and these Go goals do the work, reading/writing the fox DATA_FLAGS state on the *Entity (foxFlags +
// crouchAmount + interestedAngle + ticksSinceEaten). The per-tick state animation lives in foxAiStep —
// the per-type hook (sibling of creeperAiStep/chickenAiStep), called from tickAI AFTER serverAiStep.
//
// GOALS PORTED (Fox.registerGoals order): FoxFloatGoal@0 (existing float kind), FaceplantGoal@1
// (fox_faceplant), StalkPreyGoal@5 (fox_stalk), FoxPounceGoal@6 (fox_pounce), SeekShelterGoal@6
// (fox_seek_shelter), SleepGoal@7 (fox_sleep), PerchAndSearchGoal@13 (fox_perch_search),
// DefendTrustedTargetGoal targetSelector@3 (fox_defend_trusted), landTarget Chicken/Rabbit
// NearestAttackableTargetGoal (fox_land_target).
//
// CITE-DEFERRED (recorded in the fox .star header ledger): FoxEatBerriesGoal@10 (no berry block),
// FoxSearchForItemsGoal@11 (no fox mainhand/item pickup), FoxStrollThroughVillageGoal@9 (no village POI),
// ClimbOnTopOfPowderSnowGoal@0 (no powder snow), AvoidEntityGoal@4 (no AvoidEntityGoal port — trust
// FIELDS land, the avoid MOVEMENT goal defers), fishTarget/turtleEggTarget + the RED-variant target
// ordering (only landTarget lands). Sub-stubs inside landed goals are cited AT the call-site.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// --- Fox DATA_FLAGS bit layout (jar-verified via the isX accessors) --------------------------------
const (
	foxFlagSitting    byte = 1   // Fox.FLAG_SITTING    (isSitting: getFlag(1))
	foxFlagCrouching  byte = 4   // Fox.FLAG_CROUCHING  (isCrouching: getFlag(4))
	foxFlagInterested byte = 8   // Fox.FLAG_INTERESTED (isInterested: getFlag(8))
	foxFlagPouncing   byte = 16  // Fox.FLAG_POUNCING   (isPouncing: getFlag(16))
	foxFlagSleeping   byte = 32  // Fox.FLAG_SLEEPING   (isSleeping: getFlag(32))
	foxFlagFaceplant  byte = 64  // Fox.FLAG_FACEPLANTED(isFaceplanted: getFlag(64))
	foxFlagDefending  byte = 128 // Fox.FLAG_DEFENDING  (isDefending: getFlag(128))
)

// --- Fox flag accessors (Fox.getFlag/setFlag + the named isX/setX helpers) -------------------------
// VERIFIED javap Fox.getFlag(i): (data(DATA_FLAGS_ID) & i) != 0. setFlag(i,b): flags |= i / &= ~i.
func foxGetFlag(e *Entity, bit byte) bool { return e.foxFlags&bit != 0 }
func foxSetFlag(e *Entity, bit byte, on bool) {
	if on {
		e.foxFlags |= bit
	} else {
		e.foxFlags &^= bit
	}
}

func foxIsSitting(e *Entity) bool     { return foxGetFlag(e, foxFlagSitting) }
func foxIsCrouching(e *Entity) bool   { return foxGetFlag(e, foxFlagCrouching) }
func foxIsInterested(e *Entity) bool  { return foxGetFlag(e, foxFlagInterested) }
func foxIsPouncing(e *Entity) bool    { return foxGetFlag(e, foxFlagPouncing) }
func foxIsSleeping(e *Entity) bool    { return foxGetFlag(e, foxFlagSleeping) }
func foxIsFaceplanted(e *Entity) bool { return foxGetFlag(e, foxFlagFaceplant) }
func foxIsDefending(e *Entity) bool   { return foxGetFlag(e, foxFlagDefending) }

func foxSetSitting(e *Entity, b bool)     { foxSetFlag(e, foxFlagSitting, b) }
func foxSetCrouching(e *Entity, b bool)   { foxSetFlag(e, foxFlagCrouching, b) }
func foxSetInterested(e *Entity, b bool)  { foxSetFlag(e, foxFlagInterested, b) }
func foxSetPouncing(e *Entity, b bool)    { foxSetFlag(e, foxFlagPouncing, b) }
func foxSetFaceplanted(e *Entity, b bool) { foxSetFlag(e, foxFlagFaceplant, b) }
func foxSetDefending(e *Entity, b bool)   { foxSetFlag(e, foxFlagDefending, b) }

// foxSetSleeping ports Fox.setSleeping(setFlag(32,b)). foxWakeUp == Fox.wakeUp == setSleeping(false).
func foxSetSleeping(e *Entity, b bool) { foxSetFlag(e, foxFlagSleeping, b) }
func foxWakeUp(e *Entity)              { foxSetSleeping(e, false) }

// foxIsFullyCrouched ports Fox.isFullyCrouched(): crouchAmount == 5.0f.
func foxIsFullyCrouched(e *Entity) bool { return e.crouchAmount == foxMaxCrouchAmount }

// foxClearStates ports Fox.clearStates(): drop every posture flag.
// VERIFIED javap Fox.clearStates: setIsInterested/false; setIsCrouching/false; setSitting/false;
// setSleeping/false; setDefending/false; setFaceplanted/false.
func foxClearStates(e *Entity) {
	foxSetInterested(e, false)
	foxSetCrouching(e, false)
	foxSetSitting(e, false)
	foxSetSleeping(e, false)
	foxSetDefending(e, false)
	foxSetFaceplanted(e, false)
}

// foxCanMove ports Fox.canMove(): !isSleeping() && !isSitting() && !isFaceplanted().
func foxCanMove(e *Entity) bool {
	return !foxIsSleeping(e) && !foxIsSitting(e) && !foxIsFaceplanted(e)
}

// foxTrusts ports Fox.trusts(target): the v1 thin-id trust-list membership test.
func foxTrusts(e *Entity, id int32) bool {
	return id != 0 && (e.foxTrusted0 == id || e.foxTrusted1 == id)
}

// --- Fox constants (jar-verified) ------------------------------------------------------------------
const (
	foxMaxCrouchAmount     = 5.0  // Fox.MAX_CROUCH_AMOUNT
	foxCrouchStep          = 0.2  // Fox.tick: crouchAmount += 0.2f while crouching
	foxInterestedLerp      = 0.4  // Fox.tick: interestedAngle += (target - interestedAngle) * 0.4f
	foxSleepWaitTicks      = 140  // SleepGoal.WAIT_TIME_BEFORE_SLEEP = reducedTickDelay(140) (full-rate 140)
	foxFaceplantTicks      = 40   // FaceplantGoal.start: countdown = adjustedTickDelay(40)
	foxSeekShelterInterval = 100  // SeekShelterGoal: interval = reducedTickDelay(100) (full-rate 100)
	foxStalkPreyDistSqr    = 36.0 // StalkPreyGoal: distanceToSqr(target) > 36.0 stalk, <= 36.0 crouch
	foxPounceHurtDist      = 2.0  // FoxPounceGoal.tick: distanceTo(target) <= 2.0f -> doHurtTarget
	foxAlertRange          = 12.0 // FoxBehaviorGoal alertableTargeting.range(12) + inflate(12,6,12)
	foxLandTargetInterval  = 10   // landTargetGoal NearestAttackableTargetGoal(...,10,...) randomInterval
	foxPerchProbability    = 0.02 // PerchAndSearchGoal.canUse: nextFloat() < 0.02f
	foxDefendInterval      = 10   // DefendTrustedTargetGoal randomInterval (super ...,10,...)
	foxPounceUpBias        = 0.9  // FoxPounceGoal.start: deltaMovement.add(uv.x*0.8, 0.9, uv.z*0.8)
	foxPounceHorizBias     = 0.8  // FoxPounceGoal.start: the horizontal 0.8 impulse component
)

// foxResolveTarget resolves the fox attack target (mobAI.attackTargetID) to a world position + alive
// flag. The target may be a MOB (prey Chicken/Rabbit) in the entity store OR a player. Never holds a
// live pointer past the call (the Folia rule). Returns ok=false when there is no live target.
func foxResolveTarget(t *TickLoop, e *Entity) (tx, ty, tz float64, id int32, ok bool) {
	if e.ai == nil {
		return 0, 0, 0, 0, false
	}
	id = e.ai.getTarget()
	if id == 0 {
		return 0, 0, 0, 0, false
	}
	if other, got := t.cur().entities.get(id); got {
		if other.dead || !other.isAlive() {
			return 0, 0, 0, id, false
		}
		return other.x, other.y, other.z, id, true
	}
	if p := t.playerByEntityID(id); p != nil && !p.dead {
		return p.x, p.y, p.z, id, true
	}
	return 0, 0, 0, id, false
}

// foxDistSqrTo is distanceToSqr(position) from the fox feet.
func foxDistSqrTo(e *Entity, tx, ty, tz float64) float64 {
	dx, dy, dz := tx-e.x, ty-e.y, tz-e.z
	return dx*dx + dy*dy + dz*dz
}

// foxHasShelter ports Fox.FoxBehaviorGoal.hasShelter(): !canSeeSky(headPos) && getWalkTargetValue >= 0.
// v1 reduction (cited): canSeeSky is the superflat sky stub (fire.go); getWalkTargetValue >= 0.0f is a
// cited constant-true (no path-malus subsystem; vanilla open-ground malus is 0.0, so >= 0.0 holds).
// So hasShelter == !canSeeSky at the fox head (the fox is under cover).
func foxHasShelter(t *TickLoop, e *Entity) bool {
	head := *e
	head.y = e.y + e.height // containing(x, boundingBox.maxY, z) — the block at the fox head
	return !t.canSeeSky(&head)
}

// foxAlertable ports Fox.FoxBehaviorGoal.alertable(): any live LivingEntity within inflate(12,6,12).
// v1 reduction (cited): scan the entity store + players for a live ai-bearing entity within 12 blocks
// (the FoxAlertableEntitiesSelector monster/can-attack filter is a cited stub-superset, matching the
// hostile target goals no-sensing reductions). Keeps a fox from sleeping in a crowd (the observable intent).
func foxAlertable(t *TickLoop, e *Entity) bool {
	rangeChunks := int(math.Ceil(foxAlertRange / 16.0))
	for _, other := range t.cur().entities.near(e.x, e.z, rangeChunks) {
		if other == e || other.dead || other.ai == nil {
			continue
		}
		if foxDistSqrTo(e, other.x, other.y, other.z) <= foxAlertRange*foxAlertRange {
			return true
		}
	}
	if _, ok := nearestPlayerIDAt(t, e.x, e.y, e.z, foxAlertRange); ok {
		return true
	}
	return false
}

// foxIsPathClear ports the static Fox.isPathClear(fox, target): a 6-step ray from the fox toward the
// target, requiring the 3 blocks above the ray be replaceable (air/water) — a clear low pounce corridor.
// VERIFIED javap Fox.isPathClear: zdiff=tz-fz; xdiff=tx-fx; slope=zdiff/xdiff; for i<6 { z=slope==0?0:
// zdiff*(i/6f); x=slope==0?xdiff*(i/6f):z/slope; for j=1..3 if !containing(fx+x,fy+j,fz+z).canBeReplaced
// return false } return true.
func foxIsPathClear(t *TickLoop, e *Entity, tx, tz float64) bool {
	zdiff := tz - e.z
	xdiff := tx - e.x
	slope := zdiff / xdiff
	for i := 0; i < 6; i++ {
		var z, x float64
		if slope == 0.0 {
			z = 0.0
			x = xdiff * float64(float32(i)/6.0)
		} else {
			z = zdiff * float64(float32(i)/6.0)
			x = z / slope
		}
		for j := 1; j < 4; j++ {
			bx := int(math.Floor(e.x + x))
			by := int(math.Floor(e.y + float64(j)))
			bz := int(math.Floor(e.z + z))
			if !isReplaceableState(t.digBlockState(pk.Position{X: bx, Y: by, Z: bz})) {
				return false
			}
		}
	}
	return true
}

// @1 FaceplantGoal — flags {LOOK, JUMP, MOVE}. Ports Fox.FaceplantGoal (stunned countdown). No RNG.
// VERIFIED javap Fox.FaceplantGoal: canUse=isFaceplanted; canContinue=canUse && countdown>0;
// start countdown=adjustedTickDelay(40); stop setFaceplanted(false); tick --countdown.
type foxFaceplantGoal struct {
	baseGoal
	countdown int
}

func newFoxFaceplantGoal() *foxFaceplantGoal {
	return &foxFaceplantGoal{baseGoal: newBaseGoal(flagLook | flagJump | flagMove)}
}

func (g *foxFaceplantGoal) canUse(_ *TickLoop, e *Entity) bool { return foxIsFaceplanted(e) }
func (g *foxFaceplantGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return foxIsFaceplanted(e) && g.countdown > 0
}
func (g *foxFaceplantGoal) start(_ *TickLoop, _ *Entity) { g.countdown = foxFaceplantTicks }
func (g *foxFaceplantGoal) stop(_ *TickLoop, e *Entity)  { foxSetFaceplanted(e, false) }
func (g *foxFaceplantGoal) tick(_ *TickLoop, _ *Entity)  { g.countdown-- }

// @7 SleepGoal — flags {MOVE, LOOK, JUMP}. Ports Fox.SleepGoal (extends FoxBehaviorGoal).
// The fox sleeps by day under shelter when nothing is alertable. ctor countdown = nextInt(140).
// VERIFIED javap Fox.SleepGoal: ctor countdown=nextInt(140), flags{MOVE,LOOK,JUMP}; canUse=(xxa==0 &&
// yya==0 && zza==0) && (canSleep || isSleeping); canContinue=canSleep; canSleep: if countdown>0
// {--countdown; false}; return isBrightOutside && hasShelter && !alertable && !isInPowderSnow; stop
// countdown=nextInt(140), clearStates; start setSitting/false, setIsCrouching/false, setIsInterested/false,
// setJumping/false, setSleeping/true, navigation.stop, moveControl.setWantedPosition(x,y,z,0).
type foxSleepGoal struct {
	baseGoal
	countdown int
	seeded    bool
}

func newFoxSleepGoal() *foxSleepGoal {
	return &foxSleepGoal{baseGoal: newBaseGoal(flagMove | flagLook | flagJump)}
}

// foxSleepSeed draws the ctor countdown = nextInt(140) lazily on first canUse from this fox per-mob rng
// (vanilla draws it in the goal ctor; v1 has no mob rng at goal-build, so it defers to the same per-mob
// stream every fox goal draws from — per-mob determinism preserved; no non-fox stream sees it).
func (g *foxSleepGoal) foxSleepSeed(e *Entity) {
	if !g.seeded {
		g.countdown = mobRandom(e).nextInt(foxSleepWaitTicks)
		g.seeded = true
	}
}

func (g *foxSleepGoal) canSleep(t *TickLoop, e *Entity) bool {
	if g.countdown > 0 {
		g.countdown--
		return false
	}
	// isBrightOutside via the isDarkEnoughToSpawn day/night proxy; hasShelter (under cover); !alertable;
	// !isInPowderSnow is a cited constant-false stub (no powder-snow subsystem).
	return !t.isDarkEnoughToSpawn() && foxHasShelter(t, e) && !foxAlertable(t, e)
}

func (g *foxSleepGoal) canUse(t *TickLoop, e *Entity) bool {
	g.foxSleepSeed(e)
	// (xxa==0 && yya==0 && zza==0): v1 has no strafe-input fields; the equivalent is "not currently
	// pathing" (no want-target) — keeps sleep from firing mid-walk (the intent of the input gate).
	if e.ai != nil && e.ai.hasTarget {
		return false
	}
	return g.canSleep(t, e) || foxIsSleeping(e)
}

func (g *foxSleepGoal) canContinueToUse(t *TickLoop, e *Entity) bool { return g.canSleep(t, e) }

func (g *foxSleepGoal) start(_ *TickLoop, e *Entity) {
	foxSetSitting(e, false)
	foxSetCrouching(e, false)
	foxSetInterested(e, false)
	e.setJumping(false)
	foxSetSleeping(e, true)
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

func (g *foxSleepGoal) stop(_ *TickLoop, e *Entity) {
	g.countdown = mobRandom(e).nextInt(foxSleepWaitTicks)
	foxClearStates(e)
}

// @6 SeekShelterGoal — flags {MOVE}. Ports Fox.SeekShelterGoal (extends FleeSunGoal).
// interval = reducedTickDelay(100). canUse: not sleeping, no target; (thunder&&canSeeSky -> setWantedPos)
// else interval gate, then isBrightOutside && canSeeSky && !isVillage && setWantedPos. getHidePos does the
// 10-candidate nextInt(20)-10 / nextInt(6)-3 / nextInt(20)-10 shade search. start clearStates + moveTo(hide).
type foxSeekShelterGoal struct {
	baseGoal
	interval            int
	wantX, wantY, wantZ float64
	haveWant            bool
}

func newFoxSeekShelterGoal() *foxSeekShelterGoal {
	return &foxSeekShelterGoal{baseGoal: newBaseGoal(flagMove), interval: foxSeekShelterInterval}
}

// foxGetHidePos ports FleeSunGoal.getHidePos: 10 candidates offset (nextInt(20)-10, nextInt(6)-3,
// nextInt(20)-10); the first NOT sky-exposed (getWalkTargetValue<0 is cited constant-false) wins, at
// bottom-center. Draws up to 30 nextInt.
func (g *foxSeekShelterGoal) foxGetHidePos(t *TickLoop, e *Entity) (x, y, z float64, ok bool) {
	r := mobRandom(e)
	px, py, pz := int(math.Floor(e.x)), int(math.Floor(e.y)), int(math.Floor(e.z))
	for i := 0; i < 10; i++ {
		ox := r.nextInt(20) - 10
		oy := r.nextInt(6) - 3
		oz := r.nextInt(20) - 10
		cx, cy, cz := px+ox, py+oy, pz+oz
		probe := *e
		probe.x, probe.y, probe.z = float64(cx), float64(cy), float64(cz)
		if t.canSeeSky(&probe) {
			continue
		}
		return float64(cx) + 0.5, float64(cy), float64(cz) + 0.5, true
	}
	return 0, 0, 0, false
}

func (g *foxSeekShelterGoal) setWantedPos(t *TickLoop, e *Entity) bool {
	x, y, z, ok := g.foxGetHidePos(t, e)
	if !ok {
		return false
	}
	g.wantX, g.wantY, g.wantZ, g.haveWant = x, y, z, true
	return true
}

func (g *foxSeekShelterGoal) canUse(t *TickLoop, e *Entity) bool {
	if foxIsSleeping(e) {
		return false
	}
	if e.ai != nil && e.ai.getTarget() != 0 {
		return false
	}
	// isThundering() && canSeeSky: a cited constant-false thunder stub (no weather subsystem).
	if g.interval > 0 {
		g.interval--
		return false
	}
	g.interval = 100
	// isVillage is a cited constant-false stub (no village POI) — a v1 fox is never in a village.
	return !t.isDarkEnoughToSpawn() && t.canSeeSky(e) && g.setWantedPos(t, e)
}

func (g *foxSeekShelterGoal) canContinueToUse(_ *TickLoop, e *Entity) bool {
	return e.ai != nil && e.ai.hasTarget
}

func (g *foxSeekShelterGoal) start(_ *TickLoop, e *Entity) {
	foxClearStates(e)
	if g.haveWant && e.ai != nil {
		e.ai.setWantTarget(g.wantX, g.wantY, g.wantZ)
	}
}

func (g *foxSeekShelterGoal) stop(_ *TickLoop, e *Entity) {
	g.haveWant = false
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// @5 StalkPreyGoal — flags {MOVE, LOOK}. Ports Fox.StalkPreyGoal.
type foxStalkPreyGoal struct {
	baseGoal
}

func newFoxStalkPreyGoal() *foxStalkPreyGoal {
	return &foxStalkPreyGoal{baseGoal: newBaseGoal(flagMove | flagLook)}
}

func (g *foxStalkPreyGoal) canUse(t *TickLoop, e *Entity) bool {
	if foxIsSleeping(e) {
		return false
	}
	tx, ty, tz, id, ok := foxResolveTarget(t, e)
	if !ok {
		return false
	}
	if !foxTargetIsStalkable(t, id) {
		return false
	}
	return foxDistSqrTo(e, tx, ty, tz) > foxStalkPreyDistSqr && !foxIsCrouching(e) && !foxIsInterested(e) && !e.jumping
}

func (g *foxStalkPreyGoal) start(_ *TickLoop, e *Entity) {
	foxSetSitting(e, false)
	foxSetFaceplanted(e, false)
}

func (g *foxStalkPreyGoal) stop(t *TickLoop, e *Entity) {
	tx, _, tz, _, ok := foxResolveTarget(t, e)
	if ok && foxIsPathClear(t, e, tx, tz) {
		foxSetInterested(e, true)
		foxSetCrouching(e, true)
		if e.ai != nil {
			e.ai.clearWantTarget()
		}
	} else {
		foxSetInterested(e, false)
		foxSetCrouching(e, false)
	}
}

func (g *foxStalkPreyGoal) tick(t *TickLoop, e *Entity) {
	tx, ty, tz, _, ok := foxResolveTarget(t, e)
	if !ok {
		return
	}
	if foxDistSqrTo(e, tx, ty, tz) <= foxStalkPreyDistSqr {
		foxSetInterested(e, true)
		foxSetCrouching(e, true)
		if e.ai != nil {
			e.ai.clearWantTarget()
		}
	} else if e.ai != nil {
		e.ai.setWantTargetSpeed(tx, ty, tz, foxStalkChaseSpeed)
	}
}

// @6 FoxPounceGoal — JumpGoal (no flags), isInterruptable false. Ports Fox.FoxPounceGoal.
// When fully crouched with an aligned target and a clear path, the fox springs (add uv*0.8 + 0.9 up)
// and, on landing within 2 blocks, doHurtTarget. No RNG.
type foxPounceGoal struct {
	baseGoal
}

func newFoxPounceGoal() *foxPounceGoal {
	g := &foxPounceGoal{baseGoal: newBaseGoal(0)}
	g.interruptable = false
	return g
}

func (g *foxPounceGoal) isInterruptable() bool { return false }

func (g *foxPounceGoal) canUse(t *TickLoop, e *Entity) bool {
	if !foxIsFullyCrouched(e) {
		return false
	}
	tx, ty, tz, _, ok := foxResolveTarget(t, e)
	if !ok {
		return false
	}
	// target.getMotionDirection() != target.getDirection(): v1 has no per-entity motion-vs-facing test;
	// the cited reduction treats prey as aligned (pass) and gates the pounce on the clear-path check below.
	hasClearPath := foxIsPathClear(t, e, tx, tz)
	if !hasClearPath {
		if e.ai != nil {
			e.ai.setWantTarget(tx, ty, tz) // nav.createPath(target, 0)
		}
		foxSetCrouching(e, false)
		foxSetInterested(e, false)
	}
	return hasClearPath
}

func (g *foxPounceGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	_, _, _, _, ok := foxResolveTarget(t, e)
	if !ok {
		return false
	}
	yd := e.vy
	settled := yd*yd < 0.05 && math.Abs(float64(e.pitch)) < 15.0 && e.onGround
	return !(settled || foxIsFaceplanted(e))
}

func (g *foxPounceGoal) start(t *TickLoop, e *Entity) {
	e.setJumping(true)
	foxSetPouncing(e, true)
	foxSetInterested(e, false)
	tx, ty, tz, _, ok := foxResolveTarget(t, e)
	if ok {
		dx, dy, dz := tx-e.x, ty-e.y, tz-e.z
		length := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if length > 0 {
			ux, uz := dx/length, dz/length
			e.vx += ux * foxPounceHorizBias
			e.vy += foxPounceUpBias
			e.vz += uz * foxPounceHorizBias
		}
	}
	if e.ai != nil {
		e.ai.clearWantTarget() // nav.stop()
	}
}

func (g *foxPounceGoal) stop(_ *TickLoop, e *Entity) {
	foxSetCrouching(e, false)
	e.crouchAmount = 0.0
	e.crouchAmountO = 0.0
	foxSetInterested(e, false)
	foxSetPouncing(e, false)
}

func (g *foxPounceGoal) tick(t *TickLoop, e *Entity) {
	tx, ty, tz, id, ok := foxResolveTarget(t, e)
	// The xRot flight-arc animation is a cited client-visual deferral; the GAMEPLAY is the landing hurt.
	if ok && foxDistTo(e, tx, ty, tz) <= foxPounceHurtDist {
		foxDoHurtTarget(t, e, id) // distanceTo(target) <= 2.0f -> doHurtTarget (prey is a MOB victim)
	}
	// The snow-miss faceplant (else-branch) needs a SNOW block landing: cited constant-false stub (no
	// snow-layer block in v1) — structured to fire when the SNOW block lands.
}

// @13 PerchAndSearchGoal — flags {MOVE, LOOK}. Ports Fox.PerchAndSearchGoal (extends FoxBehaviorGoal).
// The idle sit-and-scan: rarely (2%) an unhurt, awake, target-less, idle, non-alerted, non-pouncing,
// non-crouching fox sits and looks around a few times.
type foxPerchAndSearchGoal struct {
	baseGoal
	relX, relZ     float64
	lookTime       int
	looksRemaining int
}

func newFoxPerchAndSearchGoal() *foxPerchAndSearchGoal {
	return &foxPerchAndSearchGoal{baseGoal: newBaseGoal(flagMove | flagLook)}
}

func (g *foxPerchAndSearchGoal) resetLook(e *Entity) {
	r := mobRandom(e)
	rnd := math.Pi * 2 * r.nextDouble() // DRAW: 2*pi * nextDouble
	g.relX = math.Cos(rnd)
	g.relZ = math.Sin(rnd)
	g.lookTime = 80 + r.nextInt(20) // DRAW: adjustedTickDelay(80 + nextInt(20))
}

func (g *foxPerchAndSearchGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.lastHurtByMob != 0 { // getLastHurtByMob() == null
		return false
	}
	if mobRandom(e).nextFloat() >= foxPerchProbability { // nextFloat() < 0.02f
		return false
	}
	if foxIsSleeping(e) {
		return false
	}
	if e.ai != nil && (e.ai.getTarget() != 0 || e.ai.hasTarget) { // getTarget==null && nav.isDone
		return false
	}
	return !foxAlertable(t, e) && !foxIsPouncing(e) && !foxIsCrouching(e)
}

func (g *foxPerchAndSearchGoal) canContinueToUse(_ *TickLoop, _ *Entity) bool {
	return g.looksRemaining > 0
}

func (g *foxPerchAndSearchGoal) start(_ *TickLoop, e *Entity) {
	g.resetLook(e)
	g.looksRemaining = 2 + mobRandom(e).nextInt(3) // looksRemaining = 2 + nextInt(3)
	foxSetSitting(e, true)
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

func (g *foxPerchAndSearchGoal) stop(_ *TickLoop, e *Entity) { foxSetSitting(e, false) }

func (g *foxPerchAndSearchGoal) tick(_ *TickLoop, e *Entity) {
	g.lookTime--
	if g.lookTime <= 0 {
		g.looksRemaining--
		g.resetLook(e)
	}
	// setLookAt(x+relX, eyeY, z+relZ): a cited client-look deferral (no look-control yaw write in v1);
	// the SCAN GAMEPLAY (the countdown + the sit posture) lands here; the head aim is a visual.
}

// targetSelector @3 DefendTrustedTargetGoal — flags {TARGET}. Ports Fox.DefendTrustedTargetGoal.
// A fox retaliates against whoever last hurt one of its trusted entities. randomInterval-gated.
type foxDefendTrustedGoal struct {
	baseGoal
	randomInterval int
	target         int32
	timestamp      int32
}

func newFoxDefendTrustedGoal() *foxDefendTrustedGoal {
	return &foxDefendTrustedGoal{baseGoal: newBaseGoal(flagTarget), randomInterval: foxDefendInterval}
}

func (g *foxDefendTrustedGoal) canUse(t *TickLoop, e *Entity) bool {
	if g.randomInterval > 0 && mobRandom(e).nextInt(g.randomInterval) != 0 { // DRAW: nextInt(10) gate
		return false
	}
	for _, trustedID := range [2]int32{e.foxTrusted0, e.foxTrusted1} {
		if trustedID == 0 {
			continue
		}
		var lastHurtBy, ts int32
		if trusted, ok := t.cur().entities.get(trustedID); ok {
			// trusted.getLastHurtByMob() / getLastHurtByMobTimestamp(): who last hurt the trusted mob.
			lastHurtBy = trusted.lastHurtByMob
			ts = trusted.lastHurtByMobTimestamp
		} else {
			// A trusted PLAYER: v1 tickPlayer tracks lastHurtMob (who the player ATTACKED) but not
			// lastHurtByMob (who ATTACKED the player) — the hurt-by-side player bookkeeping does not exist
			// yet. Defending a trusted player against their attacker is a cited deferral until that field
			// lands; a trusted mob (a bred fox trusting another fox) still triggers the defend fully.
			continue
		}
		g.target = lastHurtBy
		g.timestamp = ts
		// ts != this.timestamp && canAttack(lastHurtBy): fire once per fresh hit against a live attacker.
		return ts != e.foxDefendSince && lastHurtBy != 0 && foxAttackerResolvable(t, lastHurtBy)
	}
	return false
}

func (g *foxDefendTrustedGoal) start(t *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(g.target) // setTarget(trustedLastHurtBy)
	}
	e.foxDefendSince = g.timestamp // timestamp = trustedLastHurt.getLastHurtByMobTimestamp()
	// playSound(FOX_AGGRO): a cited client-sound deferral.
	foxSetDefending(e, true)
	foxWakeUp(e)
}

func (g *foxDefendTrustedGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return e.ai != nil && e.ai.getTarget() != 0 && foxAttackerResolvable(t, e.ai.getTarget())
}

func (g *foxDefendTrustedGoal) stop(_ *TickLoop, e *Entity) {
	g.target = 0
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

// foxStalkChaseSpeed is StalkPreyGoal.tick nav.moveTo(target, 1.5) — the stalk approach speed modifier.
const foxStalkChaseSpeed = 1.5

// foxTargetIsStalkable ports STALKABLE_PREY.test(target): the target is a Chicken or a Rabbit (the
// landTarget prey classes). Resolves the target id to its entity type in the store.
func foxTargetIsStalkable(t *TickLoop, id int32) bool {
	if id == 0 {
		return false
	}
	other, ok := t.cur().entities.get(id)
	if !ok {
		return false
	}
	return other.typ == entity.Chicken.ID || other.typ == entity.Rabbit.ID
}

// foxDistTo is distanceTo(position) (the sqrt distance) from the fox feet — FoxPounceGoal.tick uses the
// non-squared distanceTo for its <= 2.0f reach check.
func foxDistTo(e *Entity, tx, ty, tz float64) float64 {
	return math.Sqrt(foxDistSqrTo(e, tx, ty, tz))
}

// foxAttackerResolvable reports whether an attacker id still resolves to a live entity or player — the v1
// stand-in for canAttack(target) (the target is a present, valid combat target).
func foxAttackerResolvable(t *TickLoop, id int32) bool {
	if id == 0 {
		return false
	}
	if other, ok := t.cur().entities.get(id); ok {
		return !other.dead && other.isAlive()
	}
	if p := t.playerByEntityID(id); p != nil {
		return !p.dead
	}
	return false
}

// foxDoHurtTarget ports Fox.doHurtTarget for the pounce landing: deal ATTACK_DAMAGE to the prey. The
// prey is a MOB (Chicken/Rabbit) so this routes through applyDamageEntity (the mob-victim path), with a
// mob_attack damage source carrying attacker = the fox id (host-set, never forgeable). A player-target
// pounce (rare) routes through the player hurt path. No RNG here (matches Mob.doHurtTarget core limb).
func foxDoHurtTarget(t *TickLoop, e *Entity, targetID int32) {
	dmg := float32(e.getAttributeValue(attribute.AttackDamage))
	src := damageSourceMobAttack(e.id)
	if other, ok := t.cur().entities.get(targetID); ok {
		t.broadcastMobSwing(e)
		t.applyDamageEntity(other, src, dmg)
		return
	}
	if p := t.playerByEntityID(targetID); p != nil {
		t.broadcastMobSwing(e)
		t.applyDamage(p, src, dmg)
	}
}

// foxAiStep ports Fox.tick + Fox.aiStep server extras (the per-type hook, sibling of creeperAiStep /
// chickenAiStep). Called from tickAI for a live fox AFTER serverAiStep. It advances the crouch/interested
// animation counters, ticks ticksSinceEaten, and applies the wake/sit-in-water/target-lost state clears.
// VERIFIED javap Fox.tick + Fox.aiStep (see the file header and the fox .star ledger).
func (t *TickLoop) foxAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	// --- Fox.aiStep server extras (isEffectiveAi branch) ---
	e.ticksSinceEaten++ // ++ticksSinceEaten (the mouth-food counter; the finishUsingItem eat needs the
	// fox mainhand inventory, a cited deferral — the counter still advances so the eat lands when equipment does)
	if tid := foxTargetGoneClears(t, e); tid {
		// (target == null || !target.isAlive()) -> setIsCrouching(false); setIsInterested(false).
		foxSetCrouching(e, false)
		foxSetInterested(e, false)
	}
	// if (isSleeping() || isImmobile()) { jumping=false; xxa=0; zza=0; } — sleep immobility. v1 zeroes the
	// jump intent + the horizontal velocity so a sleeping fox stays put (isImmobile==isDeadOrDying, handled
	// by the dead guard above).
	if foxIsSleeping(e) {
		e.setJumping(false)
		e.vx = 0
		e.vz = 0
	}
	// --- Fox.tick (isEffectiveAi branch) ---
	inWater := t.entityInWater(e)
	if inWater || (e.ai != nil && e.ai.getTarget() != 0) {
		foxWakeUp(e) // inWater || getTarget()!=null || isThundering() -> wakeUp() (thunder stub false)
	}
	if inWater || foxIsSleeping(e) {
		foxSetSitting(e, false) // inWater || isSleeping() -> setSitting(false)
	}
	// isFaceplanted() && nextFloat()<0.2 -> levelEvent(2001) block-break particles: a cited client-visual
	// deferral (no per-fox particle emit here); the faceplant GAMEPLAY (the FaceplantGoal countdown) lands.
	// --- the interested/crouch animation lerp (Fox.tick, ALWAYS) ---
	e.interestedAngleO = e.interestedAngle
	if foxIsInterested(e) {
		e.interestedAngle += (1.0 - e.interestedAngle) * foxInterestedLerp
	} else {
		e.interestedAngle += (0.0 - e.interestedAngle) * foxInterestedLerp
	}
	e.crouchAmountO = e.crouchAmount
	if foxIsCrouching(e) {
		e.crouchAmount += foxCrouchStep
		if e.crouchAmount > foxMaxCrouchAmount {
			e.crouchAmount = foxMaxCrouchAmount
		}
	} else {
		e.crouchAmount = 0.0
	}
}

// foxTargetGoneClears reports whether the fox target is absent/dead this tick (Fox.aiStep clears crouch/
// interested when getTarget()==null || !target.isAlive()).
func foxTargetGoneClears(t *TickLoop, e *Entity) bool {
	_, _, _, _, ok := foxResolveTarget(t, e)
	return !ok
}
