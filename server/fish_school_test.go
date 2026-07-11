package server

// fish_school_test.go -- pins for the ported FollowFlockLeaderGoal (AbstractSchoolingFish.registerGoals @5,
// verified javap this session). A leaderless Cod near a leadable school follows the leader; Pufferfish is
// NOT a schooling fish and never schools (the vanilla exemption).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// spawnFishAt is a tiny helper: spawn a fish and force its position (the flock scan is position-based).
func spawnFishAt(loop *TickLoop, typ entity.Entity, x, y, z float64) *Entity {
	f := loop.spawnFish(typ, x, y, z)
	f.x, f.y, f.z = x, y, z
	return f
}

// TestCodFollowsSchoolLeader: two leaderless cod within 8 blocks -> the FollowFlockLeaderGoal.canUse makes
// ONE the leader and the other its follower. The follower's leader id points at the leader and the leader's
// schoolSize grows to 2 (hasFollowers).
func TestCodFollowsSchoolLeader(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	y := float64(floorY + 1)

	// A "leader-to-be" cod that already has a follower, so it canBeFollowed (schoolSize 2 < max 8). We build
	// the school by hand: c1 leads c2, then a fresh leaderless c3 joins via canUse.
	c1 := spawnFishAt(loop, entity.Cod, 8.5, y, 8.5)
	c2 := spawnFishAt(loop, entity.Cod, 9.0, y, 8.5)
	fishStartFollowing(c2, c1) // c1 is now a leader with schoolSize 2 (canBeFollowed)
	if !fishCanBeFollowed(c1) {
		t.Fatal("c1 should be followable (schoolSize 2 < max 8)")
	}

	// A fresh leaderless cod within 8 blocks runs canUse -> it becomes a follower of the existing school.
	c3 := spawnFishAt(loop, entity.Cod, 10.0, y, 8.5)
	g := newFollowFlockLeaderGoal(c3)
	g.nextStartTick = 0 // no cooldown -> canUse takes the school-scan path this call
	if !g.canUse(loop, c3) {
		t.Fatal("a leaderless cod near a leadable school should acquire a leader (canUse true)")
	}
	if !loop.fishIsFollower(c3) {
		t.Fatal("c3 should be a follower after canUse")
	}
	// c3 should follow the leaderless fish in the school (c1, the only !isFollower one there).
	if c3.schoolLeaderID != c1.id {
		t.Fatalf("c3 leader = %d, want c1 %d", c3.schoolLeaderID, c1.id)
	}
	if !fishHasFollowers(c1) {
		t.Fatal("c1 should still have followers")
	}

	// canContinueToUse: c3 is a follower and within 11 blocks of c1 -> keep following.
	if !g.canContinueToUse(loop, c3) {
		t.Fatal("c3 in range of its leader should canContinueToUse")
	}
	// tick(): the follower paths toward its leader (navigation want at speed 1.0).
	g.start(loop, c3)         // timeToRecalcPath = 0
	g.timeToRecalcPath = 1    // so the pre-decrement lands at 0 and the path recalc runs this tick
	g.tick(loop, c3)
	if !c3.ai.hasTarget || c3.ai.wantX != c1.x || c3.ai.wantZ != c1.z {
		t.Fatalf("c3 should path toward leader c1 (%v,%v); got want (%v,%v)", c1.x, c1.z, c3.ai.wantX, c3.ai.wantZ)
	}
}

// TestPufferfishNeverSchools: a Pufferfish is a plain AbstractFish (NOT AbstractSchoolingFish) and carries
// NO FollowFlockLeaderGoal -- isSchoolingFish is false, spawnFish never adds the goal, and it never joins a
// school even surrounded by other pufferfish.
func TestPufferfishNeverSchools(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	y := float64(floorY + 1)

	if isSchoolingFish(entity.Pufferfish.ID) {
		t.Fatal("Pufferfish must NOT be classified as a schooling fish")
	}

	p1 := spawnFishAt(loop, entity.Pufferfish, 8.5, y, 8.5)
	spawnFishAt(loop, entity.Pufferfish, 9.0, y, 8.5)
	spawnFishAt(loop, entity.Pufferfish, 9.5, y, 8.5)

	// A pufferfish carries no flock goal -> schoolLeaderID stays 0 and schoolSize stays 0 (never seeded).
	if p1.schoolLeaderID != 0 || p1.schoolSize != 0 {
		t.Fatalf("pufferfish should have no school state: leader=%d size=%d", p1.schoolLeaderID, p1.schoolSize)
	}
	// The schooling-fish tick is gated by isSchoolingFish, so it is a no-op for a pufferfish.
	loop.schoolingFishTick(p1)
	if p1.schoolLeaderID != 0 {
		t.Fatal("schoolingFishTick must not give a pufferfish a leader")
	}
}

// TestSchoolingLeaderScatterResets: a leader (schoolSize 2) whose school has scattered (only itself nearby)
// resets schoolSize to 1 on the scatter branch (fishSchoolNearbyCount <= 1).
func TestSchoolingLeaderScatterResets(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	y := float64(floorY + 1)
	leader := spawnFishAt(loop, entity.Cod, 8.5, y, 8.5)
	leader.schoolSize = 2 // pretend it had a follower that has since despawned (no other cod nearby)

	// Only the leader is near itself -> count == 1 -> the scatter branch resets schoolSize to 1.
	if got := loop.fishSchoolNearbyCount(leader); got != 1 {
		t.Fatalf("nearby same-type count = %d, want 1 (only the leader)", got)
	}
}
