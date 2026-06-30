package server

// aging_mob_test.go — MOB-SUB-08 (Plan 33-01): the AgeableMob aging subsystem coverage. These tests
// pin the jar-exact behavior of the signed-int breedAge machine (AgeableMob.getAge/setAge/aiStep), the
// per-tick tickMobAging (the OUTSIDE-serverAiStep aging twin of tickMobIFrames), the baby HALF-SCALE
// hitbox (Pig.getDefaultDimensions -> BABY_DIMENSIONS == adult x 0.5, the load-bearing AABB the
// breed/follow distSqr checks read), and the -1 -> 0 grow-up transition that restores the adult AABB.
//
// JAR AUTHORITY (javap -c -p temp/cache/26.2-inner.jar, this session):
//   - AgeableMob.aiStep (server branch): age=getAge(); if(canAgeUp()) setAge(age+1); else if(age>0)
//     setAge(age-1). canAgeUp() == isBaby() && !isAgeLocked() == (age<0) (no age-lock in v1).
//   - AgeableMob.isBaby(): getAge() < 0 (server getAge() == this.age == breedAge).
//   - AgeableMob.BABY_START_AGE = -24000; setAge flips DATA_BABY_ID on the 0-crossing.
//   - Pig.BABY_DIMENSIONS = EntityType.PIG.getDimensions().scale(0.5f) == scalable(0.45, 0.45);
//     adult pig dims 0.9 x 0.9 (data/entity table) -> baby 0.45 x 0.45.

import (
	"math"
	"testing"
)

// agingEpsilon is the float64 compare slack for the AABB span assertions (adult 0.9 / baby 0.45 are
// exact in IEEE-754, but the multiply by babyDimensionScale and the half-width AABB arithmetic warrant
// a tiny tolerance).
const agingEpsilon = 1e-9

// TestAgingBabyTicksToAdult: a baby (breedAge<0) ages UP by +1 each tick toward 0 and becomes an adult
// at 0 (AgeableMob.aiStep canAgeUp branch + isBaby()==age<0).
func TestAgingBabyTicksToAdult(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.breedAge = -3

	for i := 0; i < 3; i++ {
		if !e.isBaby() {
			t.Fatalf("tick %d: isBaby() = false while breedAge=%d (<0), want true", i, e.breedAge)
		}
		loop.tickMobAging(e)
	}
	if e.breedAge != 0 {
		t.Fatalf("after 3 aging ticks breedAge = %d, want 0 (baby grew up)", e.breedAge)
	}
	if e.isBaby() {
		t.Fatalf("after grow-up isBaby() = true, want false (breedAge == 0 is an adult)")
	}
}

// TestAgingCooldownDecays: an adult on breeding cooldown (breedAge>0) ticks DOWN by -1 each tick toward
// 0 and never below (AgeableMob.aiStep `else if (age>0) setAge(age-1)`).
func TestAgingCooldownDecays(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.breedAge = 3

	for i := 0; i < 3; i++ {
		if e.isBaby() {
			t.Fatalf("tick %d: a cooldown adult (breedAge=%d>0) reported isBaby()", i, e.breedAge)
		}
		loop.tickMobAging(e)
	}
	if e.breedAge != 0 {
		t.Fatalf("after 3 aging ticks breedAge = %d, want 0 (cooldown decayed)", e.breedAge)
	}
	// One more tick must NOT push it negative (==0 is a no-op, not a re-baby).
	loop.tickMobAging(e)
	if e.breedAge != 0 {
		t.Fatalf("aging an adult-ready (breedAge=0) mob = %d, want 0 (no underflow into baby)", e.breedAge)
	}
}

// TestAgingAdultIsNoOp: an un-fed lone adult (breedAge==0 — the oracle pig's state) is a pure no-op
// every tick: breedAge stays 0, no transition. This is the byte-identical gate's premise.
func TestAgingAdultIsNoOp(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.breedAge = 0

	for i := 0; i < 10; i++ {
		loop.tickMobAging(e)
		if e.breedAge != 0 {
			t.Fatalf("tick %d: aging an adult-ready mob moved breedAge to %d, want 0 (no-op)", i, e.breedAge)
		}
		if e.isBaby() {
			t.Fatalf("tick %d: an adult-ready mob (breedAge=0) reported isBaby()", i)
		}
	}
}

// aabbSpanX / aabbSpanY return the AABB horizontal (Upper.X-Lower.X == width) and vertical
// (Upper.Y-Lower.Y == height) spans the goal distSqr / collision checks read.
func aabbSpanX(e *Entity) float64 { b := e.AABB(); return b.Upper[0] - b.Lower[0] }
func aabbSpanY(e *Entity) float64 { b := e.AABB(); return b.Upper[1] - b.Lower[1] }

// TestBabyHitboxHalfScale (the BLOCKER coverage, MOB-SUB-08 + SC#1): a baby pig's AABB is HALF-SCALE —
// width/height span 0.45 (adult 0.9 x babyDimensionScale 0.5) — while an adult pig's span is the full
// 0.9. refreshDimensions (Pig.getDefaultDimensions -> BABY_DIMENSIONS) is the toggle.
func TestBabyHitboxHalfScale(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0) // entity.Pig: adult 0.9 x 0.9

	// Adult (breedAge==0): full 0.9 span.
	e.breedAge = 0
	e.refreshDimensions()
	if spanX := aabbSpanX(e); math.Abs(spanX-0.9) > agingEpsilon {
		t.Fatalf("adult pig AABB X span = %v, want 0.9 (full size)", spanX)
	}
	if spanY := aabbSpanY(e); math.Abs(spanY-0.9) > agingEpsilon {
		t.Fatalf("adult pig AABB Y span = %v, want 0.9 (full size)", spanY)
	}

	// Baby (breedAge<0): half 0.45 span.
	e.breedAge = -100
	e.refreshDimensions()
	if spanX := aabbSpanX(e); math.Abs(spanX-0.45) > agingEpsilon {
		t.Fatalf("baby pig AABB X span = %v, want 0.45 (adult 0.9 x 0.5 half-scale)", spanX)
	}
	if spanY := aabbSpanY(e); math.Abs(spanY-0.45) > agingEpsilon {
		t.Fatalf("baby pig AABB Y span = %v, want 0.45 (adult 0.9 x 0.5 half-scale)", spanY)
	}
}

// TestGrowUpRestoresHitbox (the cross-0 coverage): a baby one tick from growing up (breedAge=-1) with a
// half-scale box, aged once, crosses to 0 and tickMobAging's onGrewUp must restore the FULL adult AABB
// (0.9 span) via refreshDimensions. (The DATA_BABY_ID broadcast is exercised at the integration level in
// Plan D; here the dims prove onGrewUp ran.)
func TestGrowUpRestoresHitbox(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)

	e.breedAge = -1
	e.refreshDimensions() // baby -> half-scale
	if spanX := aabbSpanX(e); math.Abs(spanX-0.45) > agingEpsilon {
		t.Fatalf("pre-grow-up baby AABB X span = %v, want 0.45 (half-scale)", spanX)
	}

	loop.tickMobAging(e) // -1 -> 0: onGrewUp restores the adult box
	if e.breedAge != 0 {
		t.Fatalf("after grow-up tick breedAge = %d, want 0", e.breedAge)
	}
	if e.isBaby() {
		t.Fatalf("after grow-up isBaby() = true, want false")
	}
	if spanX := aabbSpanX(e); math.Abs(spanX-0.9) > agingEpsilon {
		t.Fatalf("grown-up pig AABB X span = %v, want 0.9 (onGrewUp must restore adult dims)", spanX)
	}
	if spanY := aabbSpanY(e); math.Abs(spanY-0.9) > agingEpsilon {
		t.Fatalf("grown-up pig AABB Y span = %v, want 0.9 (onGrewUp must restore adult dims)", spanY)
	}
}
