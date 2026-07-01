package server

// enderman_gaze_test.go — MOB-HOST-08 (Task #9, gaze): the EnderMan GAZE behavior test. It proves the
// distinctive "look at the enderman to aggro it" mechanic ported in ai_goals_enderman_gaze.go:
//
//   - TestEndermanGazeAggroesWhenStaredAt: a player LOOKING directly at the enderman within FOLLOW_RANGE
//     is acquired by EndermanLookForPlayerGoal.canUse (pendingTarget set); after the 5-tick aggroTime
//     wind-up, tick COMMITS it as the attack target (setTarget). A player looking AWAY is NOT acquired.
//   - TestEndermanFreezeWhenLookedAt: with a staring player already the enderman's target within 16
//     blocks, EndermanFreezeWhenLookedAt.canUse is true (freeze); looking away or beyond 16 blocks is false.
//   - TestEndermanGazeConeMath: isBeingStaredBy honors the coneSize-0.025 distance-adjusted cone — dead-on
//     aim passes, a wide-off angle fails.
//
// The per-entity rng is seeded deterministically (reseedMobAI, the spawn-path pattern) so the goal is
// draw-stable; the gaze goals draw ZERO RNG (the enderman override REPLACES the base randomInterval gate
// with a plain nearest scan), so a reference rng is not needed here — the gate is purely geometric.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// lookAnglesToward is the test-only inverse of Entity.calculateViewVector: the (yaw,pitch) in degrees
// whose view vector points from the eye (ex,ey,ez) toward the target (tx,ty,tz). Derived from the jar
// formula vx=-sin(yaw)·cos(pitch), vy=-sin(pitch), vz=cos(yaw)·cos(pitch) (calculateViewVector with
// ry=-yaw·pi/180, rx=pitch·pi/180): yaw = atan2(-dx, dz), pitch = -atan2(dy, horiz). This is the standard
// Minecraft look-at, used ONLY to construct a "player staring at the enderman" fixture in this test.
func lookAnglesToward(ex, ey, ez, tx, ty, tz float64) (yaw, pitch float32) {
	dx := tx - ex
	dy := ty - ey
	dz := tz - ez
	horiz := math.Sqrt(dx*dx + dz*dz)
	yawRad := math.Atan2(-dx, dz)
	pitchRad := -math.Atan2(dy, horiz)
	return float32(yawRad * 180.0 / math.Pi), float32(pitchRad * 180.0 / math.Pi)
}

// gazeTestEnderman builds a live enderman with a goal-bearing AI + a deterministic per-entity rng
// (reseeded by id, exactly as the spawn path does). Mirrors targetTestMob but with the Enderman type so
// the FOLLOW_RANGE attribute (64) and the gaze eye-height are the real enderman values.
func gazeTestEnderman(id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Enderman, x, y, z)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 40.0
	return e
}

// addStaringPlayer registers a player at (x,y,z) with a yaw/pitch aimed at the enderman e (its view
// vector points at the enderman's eye), so isBeingStaredBy(player) is true. It picks the exact yaw/pitch
// the enderman gaze math expects by inverting calculateViewVector for the (enderman - player) direction.
func addStaringPlayer(loop *TickLoop, entityID int32, e *Entity, x, y, z float64) *tickPlayer {
	yaw, pitch := lookAnglesToward(x, y+playerStandingEyeHeight, z, e.x, endermanEyeY(e), e.z)
	p := &tickPlayer{x: x, y: y, z: z, entityID: entityID, yaw: yaw, pitch: pitch}
	loop.players = append(loop.players, p)
	return p
}

// TestEndermanGazeAggroesWhenStaredAt: a staring player is acquired (pendingTarget) and, after the
// 5-tick wind-up, committed as the attack target; a player looking away is never acquired.
func TestEndermanGazeAggroesWhenStaredAt(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := gazeTestEnderman(9500, 8.5, 64, 20.5)
	follow := e.getAttributeValue(attribute.FollowRange)
	if follow <= 0 {
		t.Fatalf("enderman FOLLOW_RANGE = %v, want > 0 (64.0)", follow)
	}

	// A player ~12 blocks away, LOOKING directly at the enderman (well within FOLLOW_RANGE 64).
	p := addStaringPlayer(loop, 9600, e, 8.5, 64, 8.5)

	g := newEndermanLookForPlayerGoal()
	if !g.canUse(loop, e) {
		t.Fatal("canUse must acquire a player STARING at the enderman within FOLLOW_RANGE")
	}
	if g.pendingTarget != p.entityID {
		t.Fatalf("pendingTarget = %d, want the staring player id %d", g.pendingTarget, p.entityID)
	}

	// start() begins the 5-tick wind-up; the target is NOT committed until aggroTime elapses.
	g.start(loop, e)
	if e.ai.getTarget() != 0 {
		t.Fatalf("target must NOT be committed before the aggroTime wind-up: getTarget()=%d, want 0", e.ai.getTarget())
	}
	// Tick down the aggroTime (adjustedTickDelay(5) == 5 in v1). On the 5th tick it commits.
	for i := 0; i < endermanLookAggroDelay; i++ {
		g.tick(loop, e)
	}
	if e.ai.getTarget() != p.entityID {
		t.Fatalf("after the %d-tick wind-up the staring player must be committed as the target: getTarget()=%d, want %d",
			endermanLookAggroDelay, e.ai.getTarget(), p.entityID)
	}

	// A player LOOKING AWAY (yaw 180° from the enderman) is not acquired.
	loop2 := NewTickLoop(newFakeClock())
	e2 := gazeTestEnderman(9501, 8.5, 64, 20.5)
	away := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 9601, yaw: 180, pitch: 0} // facing -z, away from +z enderman
	loop2.players = append(loop2.players, away)
	g2 := newEndermanLookForPlayerGoal()
	if g2.canUse(loop2, e2) {
		t.Fatal("canUse must NOT acquire a player looking AWAY from the enderman")
	}
}

// TestEndermanFreezeWhenLookedAt: the freeze goal fires only when the current player target is staring
// at the enderman within 16 blocks.
func TestEndermanFreezeWhenLookedAt(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := gazeTestEnderman(9700, 8.5, 64, 18.5)

	// A staring player 10 blocks away (distSqr 100 <= 256), already the enderman's target.
	p := addStaringPlayer(loop, 9800, e, 8.5, 64, 8.5)
	e.ai.setTarget(p.entityID)

	g := newEndermanFreezeWhenLookedAtGoal()
	if g.flags() != (flagJump | flagMove) {
		t.Fatalf("freeze goal flags = %v, want {JUMP, MOVE}", g.flags())
	}
	if !g.canUse(loop, e) {
		t.Fatal("freeze canUse must be true for a staring player target within 16 blocks")
	}

	// A player looking AWAY (still the target, still in range) does not freeze the enderman.
	loop2 := NewTickLoop(newFakeClock())
	e2 := gazeTestEnderman(9701, 8.5, 64, 18.5)
	away := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 9801, yaw: 180, pitch: 0}
	loop2.players = append(loop2.players, away)
	e2.ai.setTarget(away.entityID)
	g2 := newEndermanFreezeWhenLookedAtGoal()
	if g2.canUse(loop2, e2) {
		t.Fatal("freeze canUse must be false for a target looking AWAY (not staring)")
	}

	// A staring player BEYOND 16 blocks (distSqr > 256) does not freeze the enderman.
	loop3 := NewTickLoop(newFakeClock())
	e3 := gazeTestEnderman(9702, 8.5, 64, 50.5) // 42 blocks from the player -> distSqr 1764 > 256
	far := addStaringPlayer(loop3, 9802, e3, 8.5, 64, 8.5)
	e3.ai.setTarget(far.entityID)
	g3 := newEndermanFreezeWhenLookedAtGoal()
	if g3.canUse(loop3, e3) {
		t.Fatal("freeze canUse must be false for a staring player BEYOND 16 blocks (distSqr > 256)")
	}
}

// TestEndermanGazeConeMath: isBeingStaredBy passes for dead-on aim and fails for a wide-off angle,
// exercising the coneSize-0.025 distance-adjusted threshold directly.
func TestEndermanGazeConeMath(t *testing.T) {
	e := gazeTestEnderman(9900, 8.5, 64, 20.5)

	// Dead-on: a player whose view vector points exactly at the enderman eye -> stared.
	yaw, pitch := lookAnglesToward(8.5, 64+playerStandingEyeHeight, 8.5, e.x, endermanEyeY(e), e.z)
	on := &tickPlayer{x: 8.5, y: 64, z: 8.5, yaw: yaw, pitch: pitch}
	if !endermanIsBeingStaredBy(e, on) {
		t.Fatal("isBeingStaredBy must be true for a player aiming dead-on at the enderman eye")
	}

	// Wide-off: rotate the yaw 45° away -> the dot falls below 1 - 0.025/dist -> not stared.
	off := &tickPlayer{x: 8.5, y: 64, z: 8.5, yaw: yaw + 45, pitch: pitch}
	if endermanIsBeingStaredBy(e, off) {
		t.Fatal("isBeingStaredBy must be false for a player aiming 45° off the enderman")
	}
}
