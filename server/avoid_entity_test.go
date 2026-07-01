package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// avoid_entity_test.go — the AvoidEntityGoal behavior pair (net.minecraft.world.entity.ai.goal
// .AvoidEntityGoal, ai_goals_avoid.go). The generic flee goal: a mob with an avoid_entity goal + a
// nearby avoided-class entity acquires a flee want that points AWAY from the threat; with NO avoided
// entity in range the goal stays inactive (canUse false, zero want).
//
//   - TestAvoidEntityFleesNearbyThreat  — a Creeper-like mob with avoid_type=Cat and a Cat within
//                                         maxDist (6.0) acquires a flee want (hasTarget) whose XZ
//                                         direction from the mob points away from the Cat, and canUse
//                                         captures the Cat as toAvoid.
//   - TestAvoidEntityIgnoresNoThreat    — with NO Cat in range, canUse returns false and sets no want.

// avoidTestMob builds a live mob (Creeper class) with a goal-bearing AI + a deterministic per-entity rng
// (reseeded by id, as the spawn path does), mirroring targetTestMob (ai_goals_target_test.go).
func avoidTestMob(id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Creeper, x, y, z)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 20.0
	e.onGround = true
	return e
}

// TestAvoidEntityFleesNearbyThreat: a Cat within maxDist makes AvoidEntityGoal.canUse acquire the Cat as
// toAvoid and commit a flee want (start -> hasTarget) that points AWAY from the Cat. The getPosAway
// acceptance gate (toAvoid.distanceToSqr(posAway) >= toAvoid.distanceToSqr(mob)) guarantees the chosen
// pos is no closer to the threat than the mob — so the mob->want XZ vector has a non-negative projection
// onto the away-from-Cat direction (it does not flee toward the Cat).
func TestAvoidEntityFleesNearbyThreat(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	const mobX, mobY, mobZ = 8.5, 64.0, 8.5
	e := avoidTestMob(9001, mobX, mobY, mobZ)

	// A Cat 2 blocks to the +X of the mob — well inside maxDist (6.0). Add it to the store so the
	// nearestEntityOfTypeAt broad-phase (t.cur().entities.near) buckets + finds it.
	cat := NewEntity(9002, entity.Cat, mobX+2.0, mobY, mobZ)
	cat.health = 10.0
	loop.only().entities.add(cat)

	g := newAvoidEntityGoal(entity.Cat.ID, avoidDefaultMaxDist, avoidWalkSpeedModifier, avoidSprintSpeedModifier)
	if !g.canUse(loop, e) {
		t.Fatal("avoidEntityGoal.canUse must fire with a Cat inside maxDist (toAvoid found + getPosAway accepted)")
	}
	if g.toAvoid != cat.id {
		t.Fatalf("toAvoid = %d, want the Cat id %d", g.toAvoid, cat.id)
	}
	if !g.haveWant {
		t.Fatal("canUse must capture a flee want (the createPath != null analogue)")
	}

	// start() commits the flee want to the nav (hasTarget), mirroring pathNav.moveTo(path, walk).
	g.start(loop, e)
	if !e.ai.hasTarget {
		t.Fatal("start() must set a nav want (hasTarget) — the mob flees")
	}

	// The flee want must NOT point toward the Cat. The away direction is (mob - cat); the chosen
	// want relative to the mob must have a non-negative projection onto it (the getPosAway acceptance
	// gate guarantees the pos is no closer to the Cat than the mob). Vanilla flees to a farther-or-equal
	// pos; here the Cat is on +X, so the away direction is -X and the want should not move toward +X past
	// the mob toward the Cat.
	awayX := e.x - cat.x // negative (Cat is +X of the mob)
	awayZ := e.z - cat.z
	wantDX := e.ai.wantX - e.x
	wantDZ := e.ai.wantZ - e.z
	proj := wantDX*awayX + wantDZ*awayZ
	if proj < 0 {
		t.Fatalf("flee want points TOWARD the Cat (projection onto away-dir = %v < 0): want=(%v,%v) mob=(%v,%v) cat=(%v,%v)",
			proj, e.ai.wantX, e.ai.wantZ, e.x, e.z, cat.x, cat.z)
	}

	// The acceptance gate itself: the committed want must be no closer to the Cat than the mob is.
	dWant := sqrDist(cat.x, cat.y, cat.z, e.ai.wantX, e.ai.wantY, e.ai.wantZ)
	dMob := sqrDist(cat.x, cat.y, cat.z, e.x, e.y, e.z)
	if dWant < dMob {
		t.Fatalf("flee want is CLOSER to the Cat than the mob (dWant=%v < dMob=%v) — the acceptance gate failed", dWant, dMob)
	}
}

// TestAvoidEntityIgnoresNoThreat: with NO avoided-class entity in range, AvoidEntityGoal.canUse returns
// false (toAvoid == null -> return before any RNG) and leaves no want. A lone Pig (not the avoided Cat
// class) in range must not trigger the flee either.
func TestAvoidEntityIgnoresNoThreat(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	const mobX, mobY, mobZ = 8.5, 64.0, 8.5
	e := avoidTestMob(9010, mobX, mobY, mobZ)

	// A Pig 2 blocks away — a WRONG-class entity (the goal avoids Cat, not Pig): must not trigger.
	pig := NewEntity(9011, entity.Pig, mobX+2.0, mobY, mobZ)
	pig.health = 10.0
	loop.only().entities.add(pig)

	g := newAvoidEntityGoal(entity.Cat.ID, avoidDefaultMaxDist, avoidWalkSpeedModifier, avoidSprintSpeedModifier)
	if g.canUse(loop, e) {
		t.Fatal("avoidEntityGoal.canUse must NOT fire with no Cat in range (a Pig is the wrong class)")
	}
	if e.ai.hasTarget {
		t.Fatal("no want must be set when the goal does not fire")
	}
	if g.toAvoid != 0 {
		t.Fatalf("toAvoid must stay 0 (no threat), got %d", g.toAvoid)
	}
}

// TestAvoidEntityOutOfRange: a Cat BEYOND maxDist (6.0) is not acquired — the goal stays inactive.
func TestAvoidEntityOutOfRange(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	const mobX, mobY, mobZ = 8.5, 64.0, 8.5
	e := avoidTestMob(9020, mobX, mobY, mobZ)

	// A Cat 10 blocks away — beyond maxDist 6.0.
	cat := NewEntity(9021, entity.Cat, mobX+10.0, mobY, mobZ)
	cat.health = 10.0
	loop.only().entities.add(cat)

	g := newAvoidEntityGoal(entity.Cat.ID, avoidDefaultMaxDist, avoidWalkSpeedModifier, avoidSprintSpeedModifier)
	if g.canUse(loop, e) {
		t.Fatal("avoidEntityGoal.canUse must NOT fire for a Cat beyond maxDist (6.0)")
	}
}
