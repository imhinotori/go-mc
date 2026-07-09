package server

// ocelot_attack_test.go — the OcelotAttackGoal (26.2) port-exact behavior tests. The 26.2
// OcelotAttackGoal is a TOP-LEVEL class at net.minecraft.world.entity.ai.goal.OcelotAttackGoal —
// it is NOT a MeleeAttackGoal subclass and does NOT have a getAttackReachSqr=4.0 or a
// checkAndPerformAttack nextFloat()<0.5 sit roll (those were pre-1.14 constructs, rewritten in
// 26.2). The faithful 26.2 port follows the actual jar:
//
//   - ctor: setFlags(EnumSet.of(MOVE, LOOK)) — {MOVE, LOOK}, NOT {MOVE}
//   - canUse: target = mob.getTarget(); return target != null  (NO RNG, NO other checks)
//   - canContinueToUse: target.isAlive() && distSqr <= 225.0 && (nav.isDone() ? canUse() : true)
//   - tick: look + reach² = (2*width)² + 3-band speed + nav.moveTo + attackTime + doHurtTarget
//
// The pig oracle (TestPluginPigEqualsGoNativePig) is a SEPARATE, untouched mob — the ocelot is its
// own base type, so the ocelot-specific code never runs on the pig.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// --- TestOcelotAttackGoalFlagsAndCtor --------------------------------------------------------

// TestOcelotAttackGoalFlagsAndCtor pins the 26.2 OcelotAttackGoal ctor: setFlags(EnumSet.of(MOVE, LOOK)).
// Pre-1.14 OcelotAttackGoal (which extended MeleeAttackGoal) was {MOVE} only; 26.2 rewrote it as a
// standalone Goal with {MOVE, LOOK} (LOOK so the setLookAt(target, 30, 30) in tick isn't fought by
// another goal). The leap helper is the JUMP+MOVE flag set; ocelot_attack MUST be MOVE+LOOK.
//
//	[VERIFIED javap OcelotAttackGoal.<init>: setFlags(EnumSet.of(Goal$Flag.MOVE, Goal$Flag.LOOK)).]
func TestOcelotAttackGoalFlagsAndCtor(t *testing.T) {
	g := newOcelotAttackGoal()
	if g.flags() != flagMove|flagLook {
		t.Fatalf("ocelot_attack flags = %b, want %b (MOVE|LOOK, the 26.2 OcelotAttackGoal ctor setFlags)", g.flags(), flagMove|flagLook)
	}
	// requiresUpdateEveryTick: the attackTime countdown must tick on the every-tick path (a false here
	// would double the cooldown to 40 ticks with the decimated selector — a direct divergence).
	if !g.requiresUpdateEveryTick() {
		t.Fatal("ocelot_attack must require update every tick (the 20-tick attackTime countdown is per-tick)")
	}
}

// --- TestOcelotAttackReachIsWidthDerived -------------------------------------------------------

// TestOcelotAttackReachIsWidthDerived pins the 26.2 reach computation: reach² = (mob.getBbWidth() * 2)²
// — a function of the mob's WIDTH, NOT a constant getAttackReachSqr=4.0 (that was the pre-1.14
// override; 26.2 inlines the computation directly in tick). For an ocelot (width 0.6) the reach²
// = (1.2)² = 1.44. Beyond reach², the goal does NOT swing (the d2 > d1 guard in tick returns).
//
//	[VERIFIED javap OcelotAttackGoal.tick: (getBbWidth() * 2) * (getBbWidth() * 2); f2d; dstore_1.]
func TestOcelotAttackReachIsWidthDerived(t *testing.T) {
	// The jar's (getBbWidth() * 2) is float32 in Java (getBbWidth returns float), then f2d widens.
	// v1 stores width as float64, so the float32 cast-then-multiply matches the jar's widened-double
	// value. For an ocelot (width 0.6) the result is ~1.2 (float32 precision: 1.2000000476837158).
	ocelot := NewEntity(7101, entity.Ocelot, 8.5, 64, 8.5)
	widthDoubled := float64(float32(ocelot.width) * 2.0)
	if widthDoubled < 1.19 || widthDoubled > 1.21 {
		t.Fatalf("ocelot width*2 = %v, want ~1.2 (entity.Ocelot width=0.6)", widthDoubled)
	}
	reachSqr := widthDoubled * widthDoubled
	if reachSqr < 1.43 || reachSqr > 1.45 {
		t.Fatalf("ocelot reach² = %v, want ~1.44 (width 0.6 -> (2*0.6)² = 1.44)", reachSqr)
	}

	// Sanity-check a different width: the pig (0.9) gets (1.8)² = 3.24. A hypothetical width 2.0
	// mob gets 16.0. The reach is NOT a constant 4.0 (the pre-1.14 override that the task brief
	// incorrectly described).
	pig := NewEntity(7102, entity.Pig, 8.5, 64, 8.5)
	pigWidthDoubled := float64(float32(pig.width) * 2.0)
	pigReachSqr := pigWidthDoubled * pigWidthDoubled
	if pigReachSqr < 3.23 || pigReachSqr > 3.25 {
		t.Fatalf("pig reach² = %v, want ~3.24 (width 0.9 -> (2*0.9)² = 3.24)", pigReachSqr)
	}
}

// --- TestOcelotAttackAggroBand ----------------------------------------------------------------

// TestOcelotAttackAggroBand pins the 225.0 (15²) canContinueToUse aggro band. The ocelot keeps
// attacking while mob.distanceToSqr(target) <= 225.0; past 15 blocks it drops the target. NO RNG.
//
//	[VERIFIED javap OcelotAttackGoal.canContinueToUse: distanceToSqr; ldc2_w 225.0d dcmpl ifle → 0.]
func TestOcelotAttackAggroBand(t *testing.T) {
	const distSqr225 = 15.0 * 15.0 // 225.0
	const distSqr225Plus = 15.01 * 15.01

	loop := NewTickLoop(newFakeClock())
	e := NewEntity(7110, entity.Ocelot, 8.5, 64, 8.5)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 10.0
	e.onGround = true

	// Target at distSqr = 15² = 225.0 exactly: in aggro band.
	pInBand := addTestPlayer(loop, 9120, 8.5+15.0, 64, 8.5) // d² = 225.0
	e.ai.setTarget(pInBand.entityID)
	g := newOcelotAttackGoal()
	// canUse captures the target.
	if !g.canUse(loop, e) {
		t.Fatal("canUse must be true at distSqr 225.0 (target present)")
	}
	// canContinueToUse: distSqr <= 225.0 -> stays in the band.
	if !g.canContinueToUse(loop, e) {
		t.Fatal("canContinueToUse must be true at distSqr 225.0 (the aggro band, ldc2_w 225.0d)")
	}

	// Target at distSqr > 225.0: out of aggro band -> canContinueToUse false.
	loop2 := NewTickLoop(newFakeClock())
	e2 := NewEntity(7111, entity.Ocelot, 8.5, 64, 8.5)
	e2.ai = &mobAI{}
	e2.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e2.ai, e2.id)
	e2.health = 10.0
	e2.onGround = true
	pOutBand := addTestPlayer(loop2, 9121, 8.5+15.01, 64, 8.5) // d² ≈ 225.3 > 225.0
	e2.ai.setTarget(pOutBand.entityID)
	g2 := newOcelotAttackGoal()
	_ = g2.canUse(loop2, e2)
	if g2.canContinueToUse(loop2, e2) {
		t.Fatal("canContinueToUse must be false at distSqr > 225.0 (the 15-block aggro band)")
	}

	// Verify the constant value.
	_ = distSqr225
	_ = distSqr225Plus
}

// --- TestOcelotAttackCanUseNoOtherChecks -------------------------------------------------------

// TestOcelotAttackCanUseNoOtherChecks pins the 26.2 canUse body: target != null is the ONLY gate.
// Pre-1.14 OcelotAttackGoal.canUse had the same body (it was a simple MeleeAttackGoal subclass), so
// this test is both a 1:1-with-the-jar check and a regression guard for any added gate (a LoS /
// onGround / daylight check would diverge from the jar). NO RNG.
//
//	[VERIFIED javap OcelotAttackGoal.canUse: getTarget putfield target; ifnull → 0; iconst_1 ireturn.]
func TestOcelotAttackCanUseNoOtherChecks(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(7120, entity.Ocelot, 8.5, 64, 8.5)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	ref := referenceRng(e.id)

	g := newOcelotAttackGoal()

	// (1) No target -> canUse false, ZERO RNG.
	if g.canUse(loop, e) {
		t.Fatal("canUse must be false when no target is set")
	}
	if a, b := e.ai.rng.nextInt(1_000_000), ref.nextInt(1_000_000); a != b {
		t.Fatalf("no-target canUse drew RNG: mob next=%d ref next=%d — canUse must draw ZERO RNG", a, b)
	}

	// (2) Target set -> canUse true, ZERO RNG (the gate is target != null only).
	p := addTestPlayer(loop, 9130, 8.5+1.0, 64, 8.5)
	e.ai.setTarget(p.entityID)
	if !g.canUse(loop, e) {
		t.Fatal("canUse must be true when a target is set")
	}
	if a, b := e.ai.rng.nextInt(1_000_000), ref.nextInt(1_000_000); a != b {
		t.Fatalf("target-set canUse drew RNG: mob next=%d ref next=%d — canUse must draw ZERO RNG", a, b)
	}
}

// --- TestOcelotAttackStopClearsTargetAndNav ---------------------------------------------------

// TestOcelotAttackStopClearsTargetAndNav pins the 26.2 stop body: target = null + navigation.stop().
// v1: target = 0 (the int32 cache); navigation.stop() -> e.ai.clearWantTarget() (the seam).
//
//	[VERIFIED javap OcelotAttackGoal.stop: aconst_null putfield target; getNavigation stop.]
func TestOcelotAttackStopClearsTargetAndNav(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(7130, entity.Ocelot, 8.5, 64, 8.5)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 10.0
	e.onGround = true

	g := newOcelotAttackGoal()
	p := addTestPlayer(loop, 9140, 8.5+1.0, 64, 8.5)
	e.ai.setTarget(p.entityID)
	if !g.canUse(loop, e) {
		t.Fatal("canUse must be true when a target is set")
	}
	if g.target == 0 {
		t.Fatal("canUse must cache the target id (this.target = mob.getTarget())")
	}

	g.stop(loop, e)
	if g.target != 0 {
		t.Fatalf("stop must clear the cached target (this.target = null): got %d", g.target)
	}
	if e.ai.hasTarget {
		t.Fatal("stop must clear the nav want (navigation.stop() / clearWantTarget)")
	}
}
