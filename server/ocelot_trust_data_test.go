package server

// ocelot_trust_data_test.go - MOB-PREY (Task #9 follow-up): the Ocelot trust data-flag + the
// TemptGoal canScareOverride seam regression set. Covers:
//
//   - TestOcelotTrustingField:           Entity.setTrusting(b) / isTrusting() round-trip.
//   - TestOcelotTrustingSetBroadcasts:   setOcelotTrustingData PUSHES a ClientboundSetEntityData
//                                        to every tracker, exactly once, on a flip.
//   - TestOcelotTrustingStopsFlee:       the Ocelot's TemptGoal.canScare override = super.canScare()
//                                        && !isTrusting() - a TRUSTING ocelot never spook-flees, an
//                                        un-trusting one would (cite-DEAD in this port; the override
//                                        is structurally live, the canScare block is a dead skip).
//   - TestPigTemptCanScareOverrideNil:   the existing pig TemptGoal with canScare=false passes nil
//                                        for the canScareOverride (the 4-arg ctor's 4th arg) and
//                                        stays byte-identical to the prior 3-arg behavior - the
//                                        regression guard for the new signature.
//
// Jar authority (javap -c -p this session from temp/cache/26.2-inner.jar):
//   Ocelot.DATA_TRUSTING = defineId(Ocelot.class, BOOLEAN) [Ocelot.static{}].
//   Ocelot.isTrusting() = getEntityData().get(DATA_TRUSTING).
//   Ocelot.setTrusting(b) = entityData.set(DATA_TRUSTING, b) + reassessTrustingGoals.
//   OcelotTemptGoal.canScare() = super.canScare() && !this.ocelot.isTrusting().
//   Hierarchy index for DATA_TRUSTING: Entity(8)+LivingEntity(7)+Mob(1)+AgeableMob(2)+Animal(0)
//   +PathfinderMob(0) = 18, so Ocelot.DATA_TRUSTING lands at index 18 (TamableAnimal is NOT in
//   the Ocelot's chain - it extends Animal directly, not TamableAnimal like Cat).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
)

// TestOcelotTrustingField: a fresh ocelot is NOT trusting; setTrusting(true) flips isTrusting() to
// true; setTrusting(false) flips it back. A direct setTrusting is a pure field write (no broadcast -
// the broadcast lives on TickLoop.setOcelotTrustingData, matching the setInLove/broadcastHearts
// split for the FEED-path's love flag).
func TestOcelotTrustingField(t *testing.T) {
	oc := newTestOcelot(0)
	if oc.isTrusting() {
		t.Fatal("precondition: a fresh ocelot must be non-trusting")
	}

	oc.setTrusting(true)
	if !oc.isTrusting() {
		t.Fatal("setTrusting(true) on a fresh ocelot: isTrusting() still false, want true")
	}
	if !oc.ocelotTrusting {
		t.Fatal("setTrusting(true) did not flip the ocelotTrusting field")
	}

	oc.setTrusting(false)
	if oc.isTrusting() {
		t.Fatal("setTrusting(false): isTrusting() still true, want false")
	}
	if oc.ocelotTrusting {
		t.Fatal("setTrusting(false) did not flip the ocelotTrusting field back")
	}
}

// TestOcelotTrustingSetBroadcasts: TickLoop.setOcelotTrustingData(o, true) PUSHES a
// ClientboundSetEntityData to every tracking player, exactly once (dirty-only - a redundant set is
// a no-op). Mirrors the Cat setCollarColor + Wolf setInSittingPose broadcast patterns
// (ai_goals_cat.go:55, ai_goals_sit.go:130).
func TestOcelotTrustingSetBroadcasts(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	oc := newTestOcelot(0)

	// newTrackerPlayer (tracker_test.go) wires the player into loop.players AND loop.clientIndex so
	// broadcastToTrackers can route packets. Use it directly.
	viewer := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	viewer.tracked = map[int32]bool{oc.id: true} // declare the viewer is tracking this ocelot

	loop.setOcelotTrustingData(oc, true)
	if n := countID(drainPackets(viewer.client), packetid.ClientboundSetEntityData); n != 1 {
		t.Fatalf("setOcelotTrustingData(true) on a fresh ocelot broadcast %d SetEntityData, want 1", n)
	}
	if !oc.isTrusting() {
		t.Fatal("setOcelotTrustingData(true) did not flip isTrusting()")
	}

	// A redundant set: NO broadcast (dirty-only).
	viewer.client = captureClient(64)
	loop.clientIndex[viewer.client] = viewer
	loop.setOcelotTrustingData(oc, true)
	if n := countID(drainPackets(viewer.client), packetid.ClientboundSetEntityData); n != 0 {
		t.Fatalf("a redundant setOcelotTrustingData(true) broadcast %d SetEntityData, want 0 (dirty-only)", n)
	}

	// A flip (true -> false): one SetEntityData broadcast.
	viewer.client = captureClient(64)
	loop.clientIndex[viewer.client] = viewer
	loop.setOcelotTrustingData(oc, false)
	if n := countID(drainPackets(viewer.client), packetid.ClientboundSetEntityData); n != 1 {
		t.Fatalf("setOcelotTrustingData(false) flip broadcast %d SetEntityData, want 1", n)
	}
	if oc.isTrusting() {
		t.Fatal("setOcelotTrustingData(false) did not flip isTrusting() back")
	}
}

// TestOcelotTrustingStopsFlee: the OcelotTemptGoal.canScare override returns super.canScare() &&
// !isTrusting(). For an ocelot with canScare=true, the override flips the canScare gate OFF when
// isTrusting()==true. With the spook-flee block a cited dead skip in this port, the override is
// structurally live (the canScare gate is now override-aware) and observably no-op (the dead skip
// never runs). The test asserts the override's SEMANTIC correctness and the wiring through the Go
// TemptGoal.
func TestOcelotTrustingStopsFlee(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	oc := newTestOcelot(0)

	// The Ocelot's canScareOverride closure: the semantic equivalent of OcelotTemptGoal.canScare.
	// canScare=true (the Ocelot's ctor arg) ANDED with !isTrusting(e).
	override := func(e *Entity) bool { return true && !e.isTrusting() }

	// Un-trusting: override returns true.
	oc.setTrusting(false)
	if !override(oc) {
		t.Fatal("un-trusting ocelot: override returned false, want true (canScare && !false == true)")
	}

	// Trusting: override returns false.
	oc.setTrusting(true)
	if override(oc) {
		t.Fatal("trusting ocelot: override returned true, want false (canScare && !true == false)")
	}

	// The TemptGoal (Go) plumbs the override through canContinueToUse; with canScare=true and a
	// trusting ocelot, the canScare gate is skipped (so the dead-skip canScare block never runs).
	// canContinueToUse still returns canUse() (the re-scan).
	g := newTemptGoal(0.6, func(int32) bool { return true }, true, override)
	addPlayerHolding(loop, oc.x+4, oc.y, oc.z, 1086, false) // 1086 = cod (ocelot_food)
	if g.canUse(loop, oc) != true {
		t.Fatal("precondition: the OcelotTemptGoal must canUse on a cod-holding player within range")
	}
	if !g.canContinueToUse(loop, oc) {
		t.Fatal("trusting ocelot + override: canContinueToUse returned false, want true (the override skipped the canScare block)")
	}

	// Flip trust off: the same goal now sees the override return TRUE for the un-trusting ocelot.
	// The canScare block is a DEAD skip (the player-moved-too-much abort is not ported), so the
	// observable behavior is identical: canContinueToUse still returns canUse() == true.
	oc.setTrusting(false)
	if !g.canContinueToUse(loop, oc) {
		t.Fatal("un-trusting ocelot + override: canContinueToUse returned false, want true (the dead-skip canScare block does not abort)")
	}
}

// TestPigTemptCanScareOverrideNil: the existing pig TemptGoal callsites pass nil for the new
// canScareOverride 4th arg; the behavior is byte-identical to the prior 3-arg ctor (canScare=false
// -> canContinueToUse skips the spook-flee block, re-scans via canUse). This is the regression guard
// for the 4-arg signature - every other mob's TemptGoal callsite does the same.
func TestPigTemptCanScareOverrideNil(t *testing.T) {
	loop, pig := temptTestLoop(t)
	p := addPlayerHolding(loop, pig.x+4, pig.y, pig.z, 1257, false)

	g := newTemptGoal(1.2, pigFoodPred, false, nil)
	if !g.canUse(loop, pig) {
		t.Fatal("precondition: the pig TemptGoal must canUse on a carrot-holding player within range")
	}
	g.start(loop, pig)
	g.tick(loop, pig)

	if !pig.ai.hasTarget {
		t.Fatal("after tick the pig has no want-target (navigateTowards(player) did not set one)")
	}
	if pig.ai.wantX != p.x || pig.ai.wantY != p.y || pig.ai.wantZ != p.z {
		t.Fatalf("want-target (%v,%v,%v) is not the player's pos (%v,%v,%v)",
			pig.ai.wantX, pig.ai.wantY, pig.ai.wantZ, p.x, p.y, p.z)
	}
	if !g.canContinueToUse(loop, pig) {
		t.Fatal("canContinueToUse (nil override, canScare=false) returned false, want true (re-scan via canUse)")
	}
}
