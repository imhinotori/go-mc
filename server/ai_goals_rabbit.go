package server

// ai_goals_rabbit.go — MOB-PASS-05 (rabbit hop): the visible HOP-vs-walk movement style of the Rabbit,
// ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//   - net.minecraft.world.entity.animal.rabbit.Rabbit$RabbitJumpControl (extends JumpControl): adds a
//     canJump flag on top of the base jump flag. wantJump() == jump; canJump()/setCanJump(b). Its
//     tick(): if (this.jump) { rabbit.startJumping(); this.jump = false; } — note it does NOT push
//     setJumping like the base JumpControl; it calls Rabbit.startJumping instead.
//   - Rabbit$RabbitMoveControl (extends MoveControl): holds nextJumpSpeed. tick(): when the rabbit is
//     onGround && getJumpDelayTicks()==0 (access$000) && !jumpControl.wantJump() -> setSpeedModifier(0.0)
//     (stop while grounded, no jump wanted); else if (hasWanted() || operation==JUMPING) ->
//     setSpeedModifier(nextJumpSpeed); then super.tick(). setWantedPosition(x,y,z,speed): if isInWater()
//     speed=1.5; super.setWantedPosition(...,speed); if (speed>0) nextJumpSpeed=speed.
//   - Rabbit.customServerAiStep(ServerLevel): the state machine (jumpDelayTicks countdown, moreCarrotTicks
//     decay, on-ground landing-delay + EVIL-attack lunge, wantJump/canJump/hasWanted -> startJumping /
//     enableJumpControl, wasOnGround refresh).
//   - Rabbit.aiStep(): super.aiStep() then the jumpTicks/jumpDuration counter that drives getJumpCompletion
//     (the render hop-arc) and clears jumping at the arc end.
//   - Rabbit.startJumping(): setJumping(true); jumpDuration=15; jumpTicks=0.
//   - Rabbit.getJumpPower()/jumpFromGround(): the rabbit's taller hop impulse (ADULT_JUMP_HEIGHT 1.5 /
//     BABY_JUMP_HEIGHT 0.5) plus a horizontal moveRelative(0.1, Vec3(0, height, 1)) launch. Wired via
//     rabbitJumpFromGround, which entityJumpStep (jump.go) routes to for a rabbit's LAND jump branch.
//   - Rabbit$RabbitPanicGoal.tick(): super.tick() then rabbit.setSpeedModifier(this.speedModifier) — the
//     PANIC hop-speed (FLEE_SPEED_MOD 2.2). Applied via the panic want speed feeding nextJumpSpeed.
//
// INTEGRATION with Ender's folded controls (ai_mob.go serverAiStep): Ender folds moveControl/lookControl
// into navigation.tick and runs a GENERIC jumpControl.tick + entityJumpStep at the serverAiStep tail. The
// rabbit hop layers ON TOP as a per-type hook (rabbitAiStep, the sibling of creeperAiStep/endermanAiStep),
// gated on typ == entity.Rabbit.ID and run AFTER serverAiStep — so every non-rabbit mob (incl. the pig
// oracle) is a zero-cost, zero-RNG skip. The vanilla intra-tick order is
// LivingEntity.aiStep jump-branch -> serverAiStep(nav.tick -> customServerAiStep -> jumpControl.tick);
// rabbitAiStep reproduces it as [serverAiStep already ran nav.tick + the GENERIC jump slot for WATER
// float] -> customServerAiStep state machine -> RabbitMoveControl speed -> RabbitJumpControl.tick ->
// aiStep jumpTicks counter. startJumping sets e.jumping this tick; the NEXT tick's entityJumpStep
// (serverAiStep tail) sees jumping && onGround and fires rabbitJumpFromGround — the vanilla one-tick lag
// between startJumping and the land jump impulse. The generic jumpControl.jump is never armed for a rabbit
// on land (no land goal arms it), so the generic entityJumpStep only ever runs the WATER float impulse for
// a rabbit; the land hop is owned here.
//
// DEFERRED (cited, never silently dropped): the EVIL (killer-bunny) attack-lunge branch of
// customServerAiStep is present but gated behind an EVIL-Variant read — a rabbit never becomes EVIL in v1
// (no Variant subsystem), so the branch is a cited const-false stub. Rabbit.getJumpPower's path
// look-ahead + horizontalCollision boost reads use a v1 const-false (no horizontalCollision field yet) —
// the base ADULT/BABY jump height is applied. moreCarrotTicks decay is DEFERRED with the RaidGardenGoal
// (no carrot-crop subsystem) so its nextInt(3) draw never fires. facePoint's cosmetic yaw is folded into
// navigation's existing turn-toward-node.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
)

// Rabbit constants (javap-verified, temp/cache/26.2-inner.jar).
const (
	// rabbitJumpDelayTicks is Rabbit.JUMP_DELAY_TICKS — the ground rest between hops after a normal
	// (non-flee) landing (setLandingDelay when speedModifier < FLEE_SPEED_MOD).
	//	[VERIFIED javap Rabbit: private static final int JUMP_DELAY_TICKS = 10;]
	rabbitJumpDelayTicks = 10
	// rabbitPanicJumpDelayTicks is Rabbit.PANIC_JUMP_DELAY_TICKS — the shorter rest between hops when
	// fleeing (setLandingDelay when speedModifier >= FLEE_SPEED_MOD 2.2).
	//	[VERIFIED javap Rabbit: private static final int PANIC_JUMP_DELAY_TICKS = 3;]
	rabbitPanicJumpDelayTicks = 3
	// rabbitJumpDurationInTicks is Rabbit.JUMP_DURATION_IN_TICKS — startJumping's jumpDuration (the
	// number of ticks the render hop-arc lasts; getJumpCompletion = jumpTicks/jumpDuration).
	//	[VERIFIED javap Rabbit: private static final int JUMP_DURATION_IN_TICKS = 15; startJumping bipush 15.]
	rabbitJumpDurationInTicks = 15
	// rabbitBabyJumpHeight / rabbitAdultJumpHeight are Rabbit.BABY_JUMP_HEIGHT / ADULT_JUMP_HEIGHT — the
	// z-forward magnitude fed to moveRelative(0.1, Vec3(0, height, 1)) in jumpFromGround.
	//	[VERIFIED javap Rabbit: BABY_JUMP_HEIGHT = 0.5d; ADULT_JUMP_HEIGHT = 1.5d.]
	rabbitBabyJumpHeight  = 0.5
	rabbitAdultJumpHeight = 1.5
	// rabbitFleeSpeedMod is Rabbit.FLEE_SPEED_MOD — the RabbitPanicGoal(2.2) speedModifier AND the
	// setLandingDelay threshold (>= 2.2 -> the 3-tick panic delay, else the 10-tick normal delay).
	//	[VERIFIED javap Rabbit: public static final double FLEE_SPEED_MOD = 2.2d; setLandingDelay ldc2_w 2.2.]
	rabbitFleeSpeedMod = 2.2
	// rabbitStrollSpeedMod is Rabbit.STROLL_SPEED_MOD — the WaterAvoidingRandomStrollGoal(0.6) speed the
	// getJumpPower low-speed test compares against (moveControl.getSpeedModifier() <= 0.6 -> 0.2f power).
	//	[VERIFIED javap Rabbit: public static final double STROLL_SPEED_MOD = 0.6d; getJumpPower ldc2_w 0.6.]
	rabbitStrollSpeedMod = 0.6
	// rabbitJumpMoveRelativeSpeed is the moveRelative speed arg in Rabbit.jumpFromGround (Vec3 launch).
	//	[VERIFIED javap Rabbit.jumpFromGround: ldc 0.1f; new Vec3(0, height, 1); moveRelative(0.1f, vec3).]
	rabbitJumpMoveRelativeSpeed = 0.1
	// rabbitJumpGroundImpulseEpsilon is the horizontalDistanceSqr gate before the moveRelative launch:
	// jumpFromGround only adds the horizontal launch when getDeltaMovement().horizontalDistanceSqr()<0.01
	// (i.e. the rabbit is nearly stationary — a launch from rest, not mid-glide).
	//	[VERIFIED javap Rabbit.jumpFromGround: getDeltaMovement().horizontalDistanceSqr() ldc2_w 0.01d; dcmpg; ifge.]
	rabbitJumpGroundImpulseEpsilon = 0.01
)

// getInputVectorRotated ports net.minecraft.world.entity.Entity.getInputVector(Vec3 input, float speed,
// float yawDeg): the yaw-rotated, length-clamped movement input the rabbit's moveRelative adds to its
// delta-movement. Returns the (dx, dy, dz) to add. PURE (no RNG). Cite Entity.getInputVector.
//
//	[VERIFIED javap Entity.getInputVector: lensq = input.lengthSqr(); if (lensq < 1.0E-7) return ZERO;
//	 v = (lensq > 1.0 ? input.normalize() : input).scale(speed); f = sin(yaw*0.017453292); g =
//	 cos(yaw*0.017453292); return new Vec3(v.x*g - v.z*f, v.y, v.z*g + v.x*f).]
func getInputVectorRotated(ix, iy, iz float64, speed float32, yawDeg float32) (float64, float64, float64) {
	lenSq := ix*ix + iy*iy + iz*iz
	if lenSq < 1.0e-7 {
		return 0, 0, 0
	}
	if lenSq > 1.0 {
		inv := 1.0 / math.Sqrt(lenSq)
		ix, iy, iz = ix*inv, iy*inv, iz*inv
	}
	ix *= float64(speed)
	iy *= float64(speed)
	iz *= float64(speed)
	// f = Mth.sin(yaw*0.017453292f) computed as a float, then widened; likewise g = Mth.cos(...).
	rad := yawDeg * 0.017453292
	f := float64(float32(math.Sin(float64(rad))))
	g := float64(float32(math.Cos(float64(rad))))
	return ix*g - iz*f, iy, iz*g + ix*f
}

// rabbitHopState is the per-mob Rabbit hop machine (hung off mobAI.rabbit for a rabbit only). It carries
// the RabbitJumpControl flags, the RabbitMoveControl nextJumpSpeed, and the Rabbit fields the state
// machine + aiStep counter read/write. Tick-owned (TICK-05); NON-NIL only for a rabbit.
type rabbitHopState struct {
	// jumpControlWantJump is RabbitJumpControl.jump (== wantJump()). Armed by RabbitJumpControl.jump(),
	// consumed by rabbitJumpControlTick.
	jumpControlWantJump bool
	// jumpControlCanJump is RabbitJumpControl.canJump — enableJumpControl sets it true, checkLandingDelay
	// (disableJumpControl) sets it false. customServerAiStep gates enableJumpControl on !canJump().
	jumpControlCanJump bool
	// nextJumpSpeed is RabbitMoveControl.nextJumpSpeed — the speed setWantedPosition last carried; the
	// RabbitMoveControl.tick re-applies it as the mob's speedModifier while a want is active or JUMPING.
	nextJumpSpeed float64
	// jumpTicks / jumpDuration are Rabbit.jumpTicks / Rabbit.jumpDuration — the render hop-arc counter
	// (getJumpCompletion = jumpTicks/jumpDuration). aiStep advances jumpTicks toward jumpDuration then
	// clears both + setJumping(false) at the arc end.
	jumpTicks    int
	jumpDuration int
	// wasOnGround is Rabbit.wasOnGround — onGround at the END of the last customServerAiStep, so a fresh
	// landing (onGround && !wasOnGround) triggers setJumping(false) + checkLandingDelay.
	wasOnGround bool
	// jumpDelayTicks is Rabbit.jumpDelayTicks — the ground rest countdown between hops (10 normal / 3
	// panic). While > 0 the customServerAiStep does not re-arm a jump want.
	jumpDelayTicks int
	// speedModifier is the effective Rabbit speedModifier the last setSpeedModifier committed (the
	// RabbitMoveControl.getSpeedModifier() analogue) — read by getJumpPower + setLandingDelay. Mirrors
	// the nav want speed a MOVE goal set (m.wantSpeed) once a want commits; 0 while grounded+idle.
	speedModifier float64
}

// rabbitAiStep is the per-type hook that runs the Rabbit hop machine for a live rabbit (typ ==
// entity.Rabbit.ID), called from tickAI AFTER serverAiStep — the sibling of creeperAiStep/endermanAiStep.
// It reproduces the vanilla intra-tick order (see the file header): the customServerAiStep state machine,
// then the RabbitMoveControl speed, then RabbitJumpControl.tick, then the Rabbit.aiStep jumpTicks counter.
// NON-rabbit mobs never reach here (the tick_phases gate), so the pig oracle is untouched. Cite
// Rabbit.customServerAiStep + RabbitMoveControl.tick + RabbitJumpControl.tick + Rabbit.aiStep.
func (t *TickLoop) rabbitAiStep(e *Entity) {
	if e == nil || e.ai == nil || !e.isAlive() || e.dead {
		return
	}
	if e.ai.rabbit == nil {
		e.ai.rabbit = &rabbitHopState{} // lazy alloc (spawn does not set it), like the creeper's maxSwell
	}
	rb := e.ai.rabbit

	// customServerAiStep (the state machine), then the controls, then the aiStep counter.
	t.rabbitCustomServerAiStep(e, rb)
	t.rabbitMoveControlTick(e, rb) // RabbitMoveControl.tick — commit the speedModifier for this tick
	rabbitJumpControlTick(rb)      // RabbitJumpControl.tick — a wanted jump -> startJumping (arms e.jumping)
	t.rabbitAiStepCounter(e, rb)   // Rabbit.aiStep tail — advance the render hop-arc counter
}

// rabbitCustomServerAiStep ports Rabbit.customServerAiStep(ServerLevel). It counts down jumpDelayTicks,
// decays moreCarrotTicks (DEFERRED with RaidGardenGoal — always 0, so its nextInt(3) never fires),
// handles the on-ground landing delay + the EVIL attack-lunge (DEFERRED const-false — no Variant
// subsystem in v1), then the wantJump/hasWanted -> startJumping / enableJumpControl arbitration, and
// refreshes wasOnGround.
//
//	[VERIFIED javap Rabbit.customServerAiStep: if(jumpDelayTicks>0) jumpDelayTicks--; if(moreCarrotTicks>0)
//	 {moreCarrotTicks -= random.nextInt(3); if(moreCarrotTicks<0) moreCarrotTicks=0;} if(onGround()){ if
//	 (!wasOnGround){ setJumping(false); checkLandingDelay(); } if(getVariant()==EVIL && jumpDelayTicks==0){
//	 target=getTarget(); if(target!=null && distanceToSqr(target)<16){ facePoint(tx,tz); moveControl
//	 .setWantedPosition(...); startJumping(); wasOnGround=true; } } jc=(RabbitJumpControl)jumpControl; if
//	 (!jc.wantJump()){ if(moveControl.hasWanted() && jumpDelayTicks==0){ path=navigation.getPath();
//	 vec3=new Vec3(wantedX,wantedY,wantedZ); if(path!=null && !path.isDone()) vec3=path.getNextEntityPos(
//	 this); facePoint(vec3.x, vec3.z); startJumping(); } } else if(!jc.canJump()){ enableJumpControl(); } }
//	 wasOnGround = onGround();]
func (t *TickLoop) rabbitCustomServerAiStep(e *Entity, rb *rabbitHopState) {
	if rb.jumpDelayTicks > 0 {
		rb.jumpDelayTicks--
	}
	// moreCarrotTicks decay: DEFERRED with the RaidGardenGoal (no carrot-crop subsystem) — the field is
	// always 0 here, so the if (moreCarrotTicks > 0) block never runs and draws NO nextInt(3). Cited,
	// not baked (structured to become a real read when the garden subsystem lands).

	if e.onGround {
		if !rb.wasOnGround {
			e.setJumping(false)              // setJumping(false)
			t.rabbitCheckLandingDelay(e, rb) // setLandingDelay + disableJumpControl
		}

		// EVIL (killer-bunny) attack-lunge: DEFERRED — no Rabbit.Variant subsystem in v1, so getVariant()
		// is never EVIL. Const-false stub; the whole if (getVariant()==EVIL && jumpDelayTicks==0) branch
		// (target facePoint + setWantedPosition + startJumping) never runs. Cited, not dropped.
		const rabbitIsEvil = false
		if rabbitIsEvil && rb.jumpDelayTicks == 0 {
			// (target = getTarget(); if within 16 blocks: facePoint + setWantedPosition + startJumping +
			// wasOnGround=true) — unreachable in v1, structured to become the real EVIL lunge later.
			_ = rb
		}

		// RabbitJumpControl arbitration: if no jump is wanted yet and a MOVE goal has a want (hasWanted)
		// and the ground rest has elapsed (jumpDelayTicks==0), start a hop toward the want; else, if a
		// jump IS wanted but the control cannot yet jump, enable it (arm canJump for the RabbitJumpControl
		// tick to consume next).
		if !rb.jumpControlWantJump {
			if t.rabbitHasWanted(e) && rb.jumpDelayTicks == 0 {
				// facePoint(vec3.x, vec3.z): turn toward the next path node (or the raw want). The
				// navigation already curves the body yaw toward its next node each tick (MoveControl.MOVE_TO
				// rotlerp), so the facePoint yaw is folded there; the observable hop cadence is unchanged.
				// The hop itself is the startJumping below.
				t.rabbitStartJumping(e, rb) // startJumping()
			}
		} else if !rb.jumpControlCanJump {
			rb.jumpControlCanJump = true // enableJumpControl() -> RabbitJumpControl.setCanJump(true)
		}
	}

	rb.wasOnGround = e.onGround // wasOnGround = onGround()
}

// rabbitHasWanted is the RabbitMoveControl.hasWanted() analogue for Ender's folded navigation: a MOVE
// goal committed a want-target this/last tick (m.hasTarget) — the MoveControl.hasWanted() (operation !=
// WAIT) equivalent. When true, customServerAiStep starts a hop toward it. Cite MoveControl.hasWanted.
func (t *TickLoop) rabbitHasWanted(e *Entity) bool {
	return e.ai != nil && e.ai.hasTarget
}

// rabbitStartJumping ports Rabbit.startJumping(): setJumping(true); jumpDuration=15; jumpTicks=0. It arms
// e.jumping so the NEXT tick's entityJumpStep land branch (jump.go) fires rabbitJumpFromGround, and
// starts the render hop-arc counter. Cite Rabbit.startJumping.
//
//	[VERIFIED javap Rabbit.startJumping: setJumping(true); bipush 15; putfield jumpDuration; iconst_0;
//	 putfield jumpTicks.]
func (t *TickLoop) rabbitStartJumping(e *Entity, rb *rabbitHopState) {
	e.setJumping(true)
	rb.jumpDuration = rabbitJumpDurationInTicks
	rb.jumpTicks = 0
}

// rabbitCheckLandingDelay ports Rabbit.checkLandingDelay(): setLandingDelay() then disableJumpControl().
// Cite Rabbit.checkLandingDelay.
//
//	[VERIFIED javap Rabbit.checkLandingDelay: setLandingDelay(); disableJumpControl().]
func (t *TickLoop) rabbitCheckLandingDelay(e *Entity, rb *rabbitHopState) {
	t.rabbitSetLandingDelay(e, rb)
	rb.jumpControlCanJump = false // disableJumpControl() -> RabbitJumpControl.setCanJump(false)
}

// rabbitSetLandingDelay ports Rabbit.setLandingDelay(): jumpDelayTicks = moveControl.getSpeedModifier()
// < FLEE_SPEED_MOD (2.2) ? JUMP_DELAY_TICKS (10) : PANIC_JUMP_DELAY_TICKS (3). A fleeing rabbit (speed
// >= 2.2) rests only 3 ticks between hops; a strolling rabbit rests 10. Cite Rabbit.setLandingDelay.
//
//	[VERIFIED javap Rabbit.setLandingDelay: moveControl.getSpeedModifier() ldc2_w 2.2 dcmpg; ifge ->
//	 bipush 10 else iconst_3; putfield jumpDelayTicks.]
func (t *TickLoop) rabbitSetLandingDelay(e *Entity, rb *rabbitHopState) {
	if rb.speedModifier < rabbitFleeSpeedMod {
		rb.jumpDelayTicks = rabbitJumpDelayTicks
	} else {
		rb.jumpDelayTicks = rabbitPanicJumpDelayTicks
	}
}

// rabbitJumpControlTick ports Rabbit$RabbitJumpControl.tick(): if (jump) { rabbit.startJumping();
// jump = false; }. It differs from the BASE JumpControl.tick (which pushes setJumping) — the rabbit's
// wanted jump routes through Rabbit.startJumping. Cite RabbitJumpControl.tick.
//
//	[VERIFIED javap RabbitJumpControl.tick: getfield jump; ifeq ret; rabbit.startJumping(); iconst_0;
//	 putfield jump.]
//
// In Ender's flow customServerAiStep already calls startJumping directly for the hasWanted path; this
// tick handles the case where jumpControlWantJump was armed externally (RabbitJumpControl.jump()). It is
// a faithful no-op when nothing armed the flag. startJumping's field writes are applied here to match.
func rabbitJumpControlTick(rb *rabbitHopState) {
	if rb.jumpControlWantJump {
		rb.jumpDuration = rabbitJumpDurationInTicks
		rb.jumpTicks = 0
		rb.jumpControlWantJump = false
	}
}

// rabbitMoveControlTick ports Rabbit$RabbitMoveControl.tick(): if (onGround() && !access$000() [==
// jumpDelayTicks==0] && !jumpControl.wantJump()) setSpeedModifier(0.0); else if (hasWanted() ||
// operation==JUMPING) setSpeedModifier(nextJumpSpeed); then super.tick(). Ender folds super.tick() (the
// MoveControl walk) into navigation.tick (already run in serverAiStep), so this method only commits the
// rabbit's speedModifier decision for the tick. Cite RabbitMoveControl.tick.
//
//	[VERIFIED javap RabbitMoveControl.tick: if (rabbit.onGround() && !Rabbit.access$000(rabbit) &&
//	 !jumpControl.wantJump()) rabbit.setSpeedModifier(0.0); else if (hasWanted() || operation==JUMPING)
//	 rabbit.setSpeedModifier(nextJumpSpeed); super.tick().
//	 access$000 returns (rabbit.jumpDelayTicks == 0) — a static synthetic accessor for the private field.]
func (t *TickLoop) rabbitMoveControlTick(e *Entity, rb *rabbitHopState) {
	// nextJumpSpeed tracks the want speed a MOVE goal committed (RabbitMoveControl.setWantedPosition sets
	// it from the goal's speedModifier). In Ender the goal's want speed lands in m.wantSpeed; mirror it.
	if e.ai.hasTarget && e.ai.wantSpeed > 0 {
		rb.nextJumpSpeed = e.ai.wantSpeed
	}
	// access$000 == (jumpDelayTicks == 0).
	jumpDelayZero := rb.jumpDelayTicks == 0
	switch {
	case e.onGround && jumpDelayZero && !rb.jumpControlWantJump:
		t.rabbitSetSpeedModifier(e, rb, 0.0) // setSpeedModifier(0.0) — stop while grounded, no jump wanted
	case t.rabbitHasWanted(e):
		// hasWanted() || operation==JUMPING -> setSpeedModifier(nextJumpSpeed). operation==JUMPING is
		// folded into hasWanted here (Ender has no MoveControl.Operation; a committed want is the JUMPING
		// analogue during the hop).
		t.rabbitSetSpeedModifier(e, rb, rb.nextJumpSpeed)
	}
}

// rabbitSetSpeedModifier ports Rabbit.setSpeedModifier(double): getNavigation().setSpeedModifier(d);
// moveControl.setWantedPosition(getWantedX,getWantedY,getWantedZ, d). In Ender the navigation speed is
// n.speed and the want is (m.wantX/Y/Z); setting the speed re-issues the want at the new pace. It also
// records the effective speedModifier rb reads (getJumpPower/setLandingDelay). Cite Rabbit.setSpeedModifier.
//
//	[VERIFIED javap Rabbit.setSpeedModifier: getNavigation().setSpeedModifier(d); moveControl
//	 .setWantedPosition(moveControl.getWantedX(), moveControl.getWantedY(), moveControl.getWantedZ(), d).]
func (t *TickLoop) rabbitSetSpeedModifier(e *Entity, rb *rabbitHopState, d float64) {
	rb.speedModifier = d
	if e.ai == nil {
		return
	}
	if d > 0 {
		e.ai.wantSpeed = d        // navigation.setSpeedModifier(d) — the nav walk pace
		e.ai.navigation.speed = d // re-issue the current want at the new pace (moveControl.setWantedPosition)
	}
}

// rabbitAiStepCounter ports the Rabbit.aiStep() tail (after super.aiStep()): advance jumpTicks toward
// jumpDuration; at the arc end (jumpTicks == jumpDuration && jumpDuration != 0) reset jumpTicks =
// jumpDuration = 0 and setJumping(false). This drives getJumpCompletion (the render hop-arc); the
// clear ends the hop. Cite Rabbit.aiStep.
//
//	[VERIFIED javap Rabbit.aiStep: super.aiStep(); if (jumpTicks != jumpDuration) jumpTicks++; else if
//	 (jumpDuration != 0) { jumpTicks = 0; jumpDuration = 0; setJumping(false); }.]
func (t *TickLoop) rabbitAiStepCounter(e *Entity, rb *rabbitHopState) {
	if rb.jumpTicks != rb.jumpDuration {
		rb.jumpTicks++
	} else if rb.jumpDuration != 0 {
		rb.jumpTicks = 0
		rb.jumpDuration = 0
		e.setJumping(false)
	}
}

// rabbitGetJumpPower ports Rabbit.getJumpPower(): base 0.3f, dropped to 0.2f when the speedModifier is a
// slow stroll (<= STROLL_SPEED_MOD 0.6), raised to 0.5f when the path looks ahead/up or the mob is not
// horizontally colliding while jumping toward a higher want; then divided by 0.42f and fed to
// Animal.getJumpPower(f) (== LivingEntity.getJumpPower * f). The path look-ahead + horizontalCollision
// boost reads are v1 const-false stubs (no horizontalCollision field / the path-node y read is cosmetic),
// so the base/stroll power is applied. Returns the FINAL jump power (the LivingEntity.getJumpPower(f)
// product, == baseJumpPower * f). Cite Rabbit.getJumpPower.
//
//	[VERIFIED javap Rabbit.getJumpPower: f=0.3f; if(moveControl.getSpeedModifier()<=0.6) f=0.2f; path=
//	 navigation.getPath(); if(path!=null && !path.isDone()){ v=path.getNextEntityPos(this); if(v.y>getY()
//	 +0.5) f=0.5f; } if(!horizontalCollision && (!jumping || (moveControl.getWantedY()>getY()+0.5))) f=0.5f;
//	 return Animal.getJumpPower(f / 0.42f).]
func (t *TickLoop) rabbitGetJumpPower(e *Entity, rb *rabbitHopState) float64 {
	f := 0.3
	if rb.speedModifier <= rabbitStrollSpeedMod {
		f = 0.2
	}
	// path look-ahead boost (v.y > getY()+0.5 -> 0.5f): the next-node y read is a cosmetic path lookahead;
	// with the navigation folded and no per-node y jump-boost wired, this is a cited const-false (the base
	// stroll/normal power is applied). Structured to become the real path-node read later.
	// horizontalCollision boost (!horizontalCollision && (!jumping || wantedY>getY()+0.5) -> 0.5f): no
	// horizontalCollision field in v1 -> const-false stub. Cited, not baked.

	// Animal.getJumpPower(f) == LivingEntity.getJumpPower(f) == (float)JUMP_STRENGTH * f * blockJumpFactor
	// + jumpBoost. With the cited defaults (baseJumpPower 0.42, block factor 1.0, boost 0), the product is
	// baseJumpPower * (f / 0.42). getJumpPower divides f by 0.42 before the call, so the final power is
	// exactly f (the 0.42 cancels): 0.42 * (f/0.42) == f. Keep the explicit chain for the future attribute
	// read (never bake the 0.42 away).
	return baseJumpPower * (f / 0.42)
}

// rabbitJumpFromGround ports Rabbit.jumpFromGround(): super.jumpFromGround() (the LivingEntity land jump,
// using the rabbit's getJumpPower), then — if the move speed is active and the rabbit is nearly stationary
// (horizontalDistanceSqr < 0.01) — a horizontal launch moveRelative(0.1f, Vec3(0, jumpHeight, 1)) where
// jumpHeight is BABY_JUMP_HEIGHT (0.5) for a baby else ADULT_JUMP_HEIGHT (1.5); then broadcast the jump
// entity-event (the client hop animation trigger). entityJumpStep (jump.go) routes a rabbit's LAND jump
// branch here instead of the generic jumpFromGround. Cite Rabbit.jumpFromGround.
//
//	[VERIFIED javap Rabbit.jumpFromGround: super.jumpFromGround(); d = moveControl.getSpeedModifier();
//	 if (d > 0.0 && getDeltaMovement().horizontalDistanceSqr() < 0.01) moveRelative(0.1f, new Vec3(0.0,
//	 isBaby()?0.5:1.5, 1.0)); if (!level().isClientSide) level().broadcastEntityEvent(this, (byte)1).]
func (t *TickLoop) rabbitJumpFromGround(e *Entity) {
	rb := e.ai.rabbit
	if rb == nil {
		rb = &rabbitHopState{}
		e.ai.rabbit = rb
	}

	// super.jumpFromGround(): the LivingEntity land jump with the rabbit's getJumpPower. f = getJumpPower();
	// if (f <= 1.0E-5f) return; setDeltaMovement(x, max(f, y), z); (sprint branch const-false as jump.go).
	f := t.rabbitGetJumpPower(e, rb)
	if f > jumpPowerEpsilon {
		e.vy = math.Max(f, e.vy)
	}

	// Horizontal launch: only when a move speed is active and the rabbit is nearly stationary.
	d := rb.speedModifier
	horizSq := e.vx*e.vx + e.vz*e.vz
	if d > 0.0 && horizSq < rabbitJumpGroundImpulseEpsilon {
		jumpHeight := rabbitAdultJumpHeight
		if e.isBaby() {
			jumpHeight = rabbitBabyJumpHeight
		}
		// moveRelative(0.1f, Vec3(0, jumpHeight, 1)): add the yaw-rotated input to delta-movement.
		dx, dy, dz := getInputVectorRotated(0.0, jumpHeight, 1.0, rabbitJumpMoveRelativeSpeed, e.yaw)
		e.vx += dx
		e.vy += dy
		e.vz += dz
	}

	// broadcastEntityEvent(this, (byte)1): the client hop-animation trigger — a cite-deferred client
	// visual (no entity-event wire hook for the rabbit hop yet; the movement itself is the gameplay).
}

// ensure the entity import is referenced even while the EVIL/Variant branch stays deferred.
var _ = entity.Rabbit
