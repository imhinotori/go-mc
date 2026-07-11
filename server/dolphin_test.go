package server

// dolphin_test.go -- pins for the ported Dolphin$DolphinSwimWithPlayerGoal (Dolphin.registerGoals @2,
// verified javap this session). A dolphin near a SWIMMING player grants DOLPHINS_GRACE (start) and trails
// the player (tick's navigation want). Verifies the canUse gate (swimming-only), the start-effect, and the
// follow want-target.

import (
	"testing"
)

// TestDolphinSwimWithPlayerGrantsGrace: a dolphin with a swimming player within 10 blocks -> canUse true,
// start() grants the player DOLPHINS_GRACE (100). A non-swimming player -> canUse false (no grace).
func TestDolphinSwimWithPlayerGrantsGrace(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	d := loop.spawnDolphin(8.5, float64(floorY+1), 8.5)

	// A NON-swimming player nearby: canUse must be false (isSwimming() gate), no grace.
	p := addTestPlayer(loop, 7001, 11.0, float64(floorY+1), 8.5) // ~2.5 blocks away, within range 10
	p.swimming = false
	g := newDolphinSwimWithPlayerGoal(dolphinSwimWithPlayerSpeed)
	if g.canUse(loop, d) {
		t.Fatal("dolphin canUse should be FALSE for a non-swimming player")
	}
	if p.activeEffects[effectDolphinsGrace] != nil {
		t.Fatal("non-swimming player should NOT have DOLPHINS_GRACE")
	}

	// Now the player is swimming: canUse true, start() grants DOLPHINS_GRACE 100.
	p.swimming = true
	if !g.canUse(loop, d) {
		t.Fatal("dolphin canUse should be TRUE for a swimming player within 10 blocks")
	}
	if g.playerID != p.entityID {
		t.Fatalf("goal captured playerID = %d, want %d", g.playerID, p.entityID)
	}
	g.start(loop, d)
	eff := p.activeEffects[effectDolphinsGrace]
	if eff == nil {
		t.Fatal("swimming player should have DOLPHINS_GRACE after start()")
	}
	if eff.duration != dolphinGraceDuration {
		t.Fatalf("DOLPHINS_GRACE duration = %d, want %d", eff.duration, dolphinGraceDuration)
	}
}

// TestDolphinSwimWithPlayerTrails: while following, tick() sets the dolphin's navigation want toward the
// player at speedModifier 4.0 (when farther than the 6.25 hold radius). A player OUT of range 10 -> canUse
// false (no follow).
func TestDolphinSwimWithPlayerTrails(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	d := loop.spawnDolphin(8.5, float64(floorY+1), 8.5)

	// Player ~5 blocks away (distSq 25 > the 6.25 stop radius) and swimming.
	p := addTestPlayer(loop, 7002, 13.5, float64(floorY+1), 8.5)
	p.swimming = true
	g := newDolphinSwimWithPlayerGoal(dolphinSwimWithPlayerSpeed)
	if !g.canUse(loop, d) {
		t.Fatal("canUse should be true for a swimming player in range")
	}
	g.start(loop, d)
	g.tick(loop, d)
	if !d.ai.hasTarget {
		t.Fatal("dolphin should have a navigation want toward the player after tick()")
	}
	if d.ai.wantX != p.x || d.ai.wantZ != p.z {
		t.Fatalf("dolphin follow want = (%v,%v), want player (%v,%v)", d.ai.wantX, d.ai.wantZ, p.x, p.z)
	}
	if d.ai.wantSpeedMod != dolphinSwimWithPlayerSpeed {
		t.Fatalf("dolphin follow speedMod = %v, want %v", d.ai.wantSpeedMod, dolphinSwimWithPlayerSpeed)
	}

	// A player OUTSIDE range 10 -> canUse false.
	far := addTestPlayer(loop, 7003, 8.5, float64(floorY+1), 8.5+20.0) // 20 blocks away
	far.swimming = true
	loop.players = loop.players[:0]
	loop.players = append(loop.players, far)
	g2 := newDolphinSwimWithPlayerGoal(dolphinSwimWithPlayerSpeed)
	if g2.canUse(loop, d) {
		t.Fatal("canUse should be false for a swimming player beyond 10 blocks")
	}
}

// TestDolphinSwimStopWithinHoldRadius: within the 6.25 hold radius, tick() stops the navigation (clears the
// want) rather than pushing toward the player.
func TestDolphinSwimStopWithinHoldRadius(t *testing.T) {
	loop, _, floorY := waterMobLoop(t)
	d := loop.spawnDolphin(8.5, float64(floorY+1), 8.5)
	p := addTestPlayer(loop, 7004, 9.5, float64(floorY+1), 8.5) // 1 block away, distSq 1 < 6.25
	p.swimming = true
	g := newDolphinSwimWithPlayerGoal(dolphinSwimWithPlayerSpeed)
	if !g.canUse(loop, d) {
		t.Fatal("canUse should be true")
	}
	g.start(loop, d)
	d.ai.setWantTargetMod(0, 0, 0, 1.0) // seed a stale want so we can see it cleared
	g.tick(loop, d)
	if d.ai.hasTarget {
		t.Fatal("within the 6.25 hold radius the dolphin should STOP navigation (want cleared)")
	}
}
