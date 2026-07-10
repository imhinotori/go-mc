package server

// goat_brain_test.go -- behavior pins for the GoatAi long-jump + ram-sensor brain (LongJumpToRandomPos /
// PrepareRamNearestTarget / RamTarget / LongJumpUtil, 1:1 javap this session). Complements goat_test.go
// (spawn-attribute + screaming-roll pins) and goat_behavior_test.go (the RAM numeric core + horn/fall/milk).
// RAM_COOLDOWN_TICKS is the flat goatRamCooldownTicks field; LONG_JUMP_COOLDOWN_TICKS lives on goatBrain.

import (
	"math"
	"testing"
)

// TestGoatLongJumpLaunchesAfterPrepare: a goat off both cooldowns with no ram victim solves a long jump
// (LongJumpToRandomPos.start), counts down PREPARE_JUMP_DURATION (40), then LAUNCHES with deltaMovement ==
// the solved chosenJump (jumpBoost 0 -> scale 1.0). Cite LongJumpToRandomPos.start + tick.
func TestGoatLongJumpLaunchesAfterPrepare(t *testing.T) {
	loop, floorY := goatLoop(t)
	g := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)
	// Force both activities off cooldown; suppress rams (no players -> goatTryStartRam fails anyway).
	g.goatBrain.longJumpCooldown = 0
	g.goatRamCooldownTicks = 0
	g.vx, g.vy, g.vz = 0, 0, 0

	// Tick once: no ram victim (no players) -> goatTryStartLongJump solves a jump + arms the prepare.
	loop.goatAiStep(g)
	if !g.goatBrain.ljChosenValid {
		t.Fatal("goat did not choose a long-jump (ljChosenValid false after start)")
	}
	if g.goatBrain.ljPrepare != goatLongJumpPrepareTime {
		t.Fatalf("goat ljPrepare = %d, want %d (PREPARE_JUMP_DURATION)", g.goatBrain.ljPrepare, goatLongJumpPrepareTime)
	}
	solvedX, solvedY, solvedZ := g.goatBrain.ljVX, g.goatBrain.ljVY, g.goatBrain.ljVZ
	if solvedX == 0 && solvedY == 0 && solvedZ == 0 {
		t.Fatal("goat solved a zero jump vector (the ballistic solve produced no velocity)")
	}

	// Run the prepare to completion + the launch tick (prepare decrements to 0 across PREPARE_JUMP_DURATION
	// ticks, then the following tick launches). Ticking one past the prepare fires the launch.
	for i := 0; i <= goatLongJumpPrepareTime; i++ {
		loop.goatAiStep(g)
	}
	// The launch tick sets deltaMovement to the solved chosenJump (scale 1.0 with jumpBoost 0).
	if math.Abs(g.vx-solvedX) > 1e-9 || math.Abs(g.vy-solvedY) > 1e-9 || math.Abs(g.vz-solvedZ) > 1e-9 {
		t.Fatalf("goat launch velocity = (%v,%v,%v), want the solved chosenJump (%v,%v,%v)", g.vx, g.vy, g.vz, solvedX, solvedY, solvedZ)
	}
	if g.goatBrain.ljChosenValid {
		t.Fatal("goat long-jump not cleared after launch (ljChosenValid still true)")
	}
	if g.goatBrain.longJumpCooldown < goatTimeBetweenLongJumpsMin || g.goatBrain.longJumpCooldown > goatTimeBetweenLongJumpsMax {
		t.Fatalf("goat long-jump cooldown = %d, want within [%d, %d]", g.goatBrain.longJumpCooldown, goatTimeBetweenLongJumpsMin, goatTimeBetweenLongJumpsMax)
	}
	// The launch went UP (an ascending ballistic arc: cy = spd*sin(rad) with rad in [65,80] deg > 0).
	if g.vy <= 0 {
		t.Fatalf("goat long-jump vy = %v, want > 0 (an upward ballistic arc)", g.vy)
	}
}

// TestGoatJumpVectorForAngleBallistic: the LongJumpUtil.calculateJumpVectorForAngle ballistic core solves
// a real upward velocity toward a nearby higher target, scaled by 0.95, and rejects an angle whose
// required speed exceeds maxVelocity. Cite LongJumpUtil.calculateJumpVectorForAngle.
func TestGoatJumpVectorForAngleBallistic(t *testing.T) {
	// A 3-block-away, 1-block-up target with the goat gravity (0.08) + a generous maxVel: solvable at 70deg.
	vx, vy, vz, ok := goatJumpVectorForAngle(0, 0, 0, 3.0, 1.0, 0, 5.0, 70, 0.08)
	if !ok {
		t.Fatal("expected a valid ballistic solution for a reachable target")
	}
	if vy <= 0 {
		t.Fatalf("ballistic vy = %v, want > 0 (upward arc)", vy)
	}
	// The horizontal heading points toward +X (the target is at +X): vx > 0, vz ~ 0.
	if vx <= 0 || math.Abs(vz) > 1e-9 {
		t.Fatalf("ballistic horizontal = (%v,%v), want vx>0 and vz~0 toward +X target", vx, vz)
	}
	// maxVelocity 0.0 rejects every angle (any real speed exceeds it).
	if _, _, _, ok2 := goatJumpVectorForAngle(0, 0, 0, 3.0, 1.0, 0, 0.0, 70, 0.08); ok2 {
		t.Fatal("expected rejection when the required speed exceeds maxVelocity 0")
	}
}

// TestGoatRamsSelectedNearbyTarget: a goat off ram cooldown selects the nearest player within
// RAM_MAX_DISTANCE (7) (PrepareRamNearestTarget), prepares RAM_PREPARE_TIME (20) ticks, then charges
// (RamTarget) and, on contact, hurts + knocks back the victim and re-arms the ram cooldown. Cite
// PrepareRamNearestTarget + RamTarget.
func TestGoatRamsSelectedNearbyTarget(t *testing.T) {
	loop, floorY := goatLoop(t)
	gy := float64(floorY + 1)
	g := loop.spawnGoat(8.5, gy, 8.5, false)
	// Suppress the long-jump so the ram path is exercised deterministically.
	g.goatBrain.longJumpCooldown = 5000
	g.goatRamCooldownTicks = 0
	// A player 3 blocks away (within RAM_MAX_DISTANCE 7).
	p := combatTestPlayer(loop, 11.5, gy, 8.5, 9100)
	hpBefore := p.health

	// Tick 1: PrepareRamNearestTarget.start selects the victim + arms the prepare.
	loop.goatAiStep(g)
	if g.goatBrain.ramTargetID != p.entityID {
		t.Fatalf("goat did not select the nearby player as ram target (ramTargetID=%d, want %d)", g.goatBrain.ramTargetID, p.entityID)
	}
	if g.goatBrain.ramPrepare != goatRamPrepareTime {
		t.Fatalf("goat ramPrepare = %d, want %d (RAM_PREPARE_TIME)", g.goatBrain.ramPrepare, goatRamPrepareTime)
	}

	// Run the prepare to completion + the arm tick (prepare decrements to 0, the following tick arms the
	// charge). Ticking one past the prepare arms ramCharging.
	for i := 0; i <= goatRamPrepareTime; i++ {
		loop.goatAiStep(g)
	}
	if !g.goatBrain.ramCharging {
		t.Fatal("goat did not begin charging after the prepare (ramCharging false)")
	}

	// Move the goat onto the victim so the contact test fires, then tick the charge.
	g.x, g.z = p.x, p.z
	loop.goatAiStep(g)
	if p.health >= hpBefore {
		t.Fatalf("ram did not damage the victim (hp %v -> %v)", hpBefore, p.health)
	}
	// finishRam cleared the ram target + re-armed the cooldown within the goat interval.
	if g.goatBrain.ramTargetID != 0 || g.goatBrain.ramCharging {
		t.Fatal("goat ram not finished (ramTargetID/ramCharging not cleared)")
	}
	lo, hi := g.goatRamTimeBetween()
	if int(g.goatRamCooldownTicks) < lo || int(g.goatRamCooldownTicks) > hi {
		t.Fatalf("goat ram cooldown = %d, want within [%d, %d]", g.goatRamCooldownTicks, lo, hi)
	}
}
