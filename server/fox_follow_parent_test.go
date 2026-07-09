package server

// fox_follow_parent_test.go — the Fox CHARACTER-LAYER FoxFollowParentGoal tests (1:1 jar port).
// Each test drives the goal methods directly with a deterministic per-entity rng (reseeded by id)
// and asserts the jar-faithful gating + transitions. The pig oracle is byte-identically untouched.

import (
	"testing"
)

func TestFoxFollowParentCanUseBaby(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	// A baby fox + an adult fox in range.
	baby := foxTestMob(6000, 0, 64, 0)
	baby.breedAge = -100 // baby
	loop.only().entities.add(baby)
	adult := foxTestMob(6001, 3, 64, 0)
	adult.breedAge = 200 // adult
	loop.only().entities.add(adult)

	g := newFoxFollowParentGoal()
	if !g.canUse(loop, baby) {
		t.Fatal("FoxFollowParentGoal.canUse did not fire for a baby fox with a nearby adult")
	}
	// Verify the underlying followParentGoal captured the adult as the parent.
	if g.parent == nil || g.parent.id != adult.id {
		t.Fatalf("follow-parent parent = %v, want adult %d", g.parent, adult.id)
	}
}

func TestFoxFollowParentCanUseAdultRejected(t *testing.T) {
	// An adult (not baby) should NOT follow a parent (per jar FollowParentGoal.canUse age>=0 gate).
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	adult := foxTestMob(6010, 0, 64, 0)
	adult.breedAge = 200 // adult — can't follow
	loop.only().entities.add(adult)
	other := foxTestMob(6011, 3, 64, 0)
	other.breedAge = 200
	loop.only().entities.add(other)

	g := newFoxFollowParentGoal()
	if g.canUse(loop, adult) {
		t.Fatal("FoxFollowParentGoal.canUse fired for an ADULT fox (age>=0)")
	}
}

func TestFoxFollowParentStartHook(t *testing.T) {
	// When the fox enters follow-parent mode, fox.clearStates() runs — all six posture flags drop
	// (interested/crouching/sitting/sleeping/defending/faceplanted) but NOT pouncing.
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	baby := foxTestMob(6020, 0, 64, 0)
	baby.breedAge = -100
	foxSetSitting(baby, true)
	foxSetCrouching(baby, true)
	foxSetInterested(baby, true)
	foxSetSleeping(baby, true)
	foxSetDefending(baby, true)
	foxSetFaceplanted(baby, true)
	foxSetPouncing(baby, true) // pouncing survives clearStates
	loop.only().entities.add(baby)
	adult := foxTestMob(6021, 3, 64, 0)
	adult.breedAge = 200
	loop.only().entities.add(adult)

	g := newFoxFollowParentGoal()
	g.start(loop, baby)
	if foxIsSitting(baby) || foxIsCrouching(baby) || foxIsInterested(baby) ||
		foxIsSleeping(baby) || foxIsDefending(baby) || foxIsFaceplanted(baby) {
		t.Fatalf("startHook clearStates did not drop all posture flags: foxFlags=%d", baby.foxFlags)
	}
	if !foxIsPouncing(baby) {
		t.Fatal("pouncing should survive clearStates (jar-verified: no setIsPouncing in clearStates)")
	}
}

func TestFoxFollowParentRejectsWhenDefending(t *testing.T) {
	// FoxFollowParentGoal.canUse: !fox.isDefending && super.canUse. A defending fox ignores parent.
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	baby := foxTestMob(6030, 0, 64, 0)
	baby.breedAge = -100
	foxSetDefending(baby, true)
	loop.only().entities.add(baby)
	adult := foxTestMob(6031, 3, 64, 0)
	adult.breedAge = 200
	loop.only().entities.add(adult)

	g := newFoxFollowParentGoal()
	if g.canUse(loop, baby) {
		t.Fatal("FoxFollowParentGoal.canUse fired for a DEFENDING baby fox (the !isDefending gate)")
	}
	// And canContinueToUse: same gate.
	if g.canContinueToUse(loop, baby) {
		t.Fatal("FoxFollowParentGoal.canContinueToUse fired for a DEFENDING baby fox")
	}
}

func TestFoxFollowParentSpeedIsOnePointTwoFive(t *testing.T) {
	// FoxFollowParentGoal.<init>(fox, 1.25) — the speedModifier is 1.25, NOT the shared 1.1.
	// We verify via the followParentGoal seam (which the newFoxFollowParentGoal embeds).
	g := newFoxFollowParentGoal()
	if g.speedModifier != 1.25 {
		t.Fatalf("FoxFollowParentGoal speedModifier = %v, want 1.25 (jar-verified Fox$FoxFollowParentGoal.<init> ldc2_w 1.25d)", g.speedModifier)
	}
}
