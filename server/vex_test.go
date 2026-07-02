package server

// vex_test.go -- deterministic pins for the Vex entity (net.minecraft.world.entity.monster.Vex, 1:1 CFR
// this session). Verifies the spawn defaults (MAX_HEALTH 14 / ATTACK_DAMAGE 4 / owner / bound origin /
// limited life), the limited-life starve (1.0 damage on the 20-tick cadence), the flight toward a wanted
// position (VexMoveControl accel), and the copy-owner-target acquisition.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// vexLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick vexes.
func vexLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestVexSpawnDefaults: spawnVex builds a vex rendering as entity.Vex.ID with the jar attributes
// (MAX_HEALTH 14.0 -> health 14, ATTACK_DAMAGE 4.0), the owner, the bound origin, and the limited life.
func TestVexSpawnDefaults(t *testing.T) {
	loop, floorY := vexLoop(t)
	v := loop.spawnVex(4242, 8.5, float64(floorY+1), 8.5, 8, floorY+1, 8, 600)
	if v.typ != entity.Vex.ID {
		t.Fatalf("vex typ = %d, want entity.Vex.ID %d", v.typ, entity.Vex.ID)
	}
	if !v.isVex {
		t.Fatal("vex not marked isVex")
	}
	if math.Abs(float64(v.health)-14.0) > 1e-6 {
		t.Fatalf("vex health = %v, want 14.0 (MAX_HEALTH)", v.health)
	}
	if got := v.getAttributeValue(attribute.MaxHealth); math.Abs(got-14.0) > 1e-9 {
		t.Fatalf("vex MAX_HEALTH = %v, want 14.0", got)
	}
	if got := v.getAttributeValue(attribute.AttackDamage); math.Abs(got-4.0) > 1e-9 {
		t.Fatalf("vex ATTACK_DAMAGE = %v, want 4.0", got)
	}
	if v.vexOwnerID != 4242 {
		t.Fatalf("vex owner = %d, want 4242", v.vexOwnerID)
	}
	if !v.vexHasLimitedLife || v.vexLimitedLifeTicks != 600 {
		t.Fatalf("vex limited life = (%v, %d), want (true, 600)", v.vexHasLimitedLife, v.vexLimitedLifeTicks)
	}
	if v.ai == nil || v.ai.rng == nil {
		t.Fatal("vex has no minimal AI / rng (mobRandom would not be per-entity seeded)")
	}
}

// TestVexLimitedLifeStarve: a vex with 1 tick of limited life left starves for 1.0 on the expiry tick and
// re-arms the countdown to 20 (Vex.tick: if (hasLimitedLife && --limitedLifeTicks <= 0) { =20; hurt(1.0) }).
func TestVexLimitedLifeStarve(t *testing.T) {
	loop, floorY := vexLoop(t)
	v := loop.spawnVex(0, 8.5, float64(floorY+1), 8.5, 8, floorY+1, 8, 1)
	start := v.health

	// One vexAiStep: --limitedLifeTicks -> 0 -> <=0 -> reset to 20 + starve 1.0.
	loop.vexAiStep(v)
	if got := start - v.health; math.Abs(float64(got)-vexStarveDamage) > 1e-6 {
		t.Fatalf("vex starve dealt %v, want %v", got, vexStarveDamage)
	}
	if v.vexLimitedLifeTicks != vexStarveResetTicks {
		t.Fatalf("vex limited-life not re-armed: got %d, want %d", v.vexLimitedLifeTicks, vexStarveResetTicks)
	}
}

// TestVexNoStarveBeforeExpiry: a vex with plenty of limited life left does NOT take starve damage; the
// counter just decrements.
func TestVexNoStarveBeforeExpiry(t *testing.T) {
	loop, floorY := vexLoop(t)
	v := loop.spawnVex(0, 8.5, float64(floorY+1), 8.5, 8, floorY+1, 8, 600)
	start := v.health
	loop.vexAiStep(v)
	if v.health != start {
		t.Fatalf("vex starved before expiry (health %v -> %v)", start, v.health)
	}
	if v.vexLimitedLifeTicks != 599 {
		t.Fatalf("vex limited-life = %d, want 599 (one decrement)", v.vexLimitedLifeTicks)
	}
}

// TestVexAcquiresNearestPlayer: a vex with no target and no owner-target acquires the nearest live player
// within follow range as its attack target.
func TestVexAcquiresNearestPlayer(t *testing.T) {
	loop, floorY := vexLoop(t)
	p := combatTestPlayer(loop, 10.5, float64(floorY+1), 8.5, 4242)
	v := loop.spawnVex(0, 8.5, float64(floorY+1), 8.5, 8, floorY+1, 8, 600)

	loop.vexAiStep(v)
	if v.ai.attackTargetID != p.entityID {
		t.Fatalf("vex target = %d, want the nearest player %d", v.ai.attackTargetID, p.entityID)
	}
}

// TestVexCopiesOwnerTarget: a vex whose OWNER (an evoker-like mob) has a target copies it, even when a
// closer other player exists -- the copy-owner goal (@2) runs before nearest-attackable (@3).
func TestVexCopiesOwnerTarget(t *testing.T) {
	loop, floorY := vexLoop(t)
	// The owner-target player (far) and a decoy player (near). The vex must copy the owner's target.
	ownerTarget := combatTestPlayer(loop, 20.5, float64(floorY+1), 8.5, 1111)
	_ = combatTestPlayer(loop, 9.0, float64(floorY+1), 8.5, 2222) // a nearer decoy

	// Build the owner mob (a vex used as a stand-in "owner" carrying a target on its ai). Any mob with an
	// ai.attackTargetID works -- the copy-owner goal reads owner.ai.attackTargetID.
	owner := loop.spawnVex(0, 8.5, float64(floorY+1), 8.5, 8, floorY+1, 8, 600)
	owner.ai.attackTargetID = ownerTarget.entityID

	v := loop.spawnVex(owner.id, 8.6, float64(floorY+1), 8.5, 8, floorY+1, 8, 600)
	loop.vexAiStep(v)
	if v.ai.attackTargetID != ownerTarget.entityID {
		t.Fatalf("vex target = %d, want the owner's target %d (copy-owner @2 before nearest @3)", v.ai.attackTargetID, ownerTarget.entityID)
	}
}

// TestVexFliesTowardWant: the VexMoveControl accelerates the vex toward its wanted position. After setting a
// want and ticking, the vex has non-zero velocity pointing toward the target and has moved from its origin.
func TestVexFliesTowardWant(t *testing.T) {
	loop, floorY := vexLoop(t)
	// Place a target far to the +X so a charge sets a wanted position and the move-control accelerates +X.
	p := combatTestPlayer(loop, 30.5, float64(floorY+5), 8.5, 4242)
	v := loop.spawnVex(0, 8.5, float64(floorY+5), 8.5, 8, floorY+5, 8, 600)
	v.ai.attackTargetID = p.entityID

	// Directly set a wanted position toward the target (bypassing the RNG charge roll) so the move-control
	// integration is deterministic. speed 1.0 (the charge speedModifier).
	v.vexWantX, v.vexWantY, v.vexWantZ = p.x, p.y+playerStandingEyeHeight, p.z
	v.vexWantSpeed = vexChargeSpeed
	v.vexHasWant = true

	startX := v.x
	loop.vexMoveControlTick(v)
	loop.vexTravel(v)

	if v.vx <= 0 {
		t.Fatalf("vex vx = %v, want > 0 (accelerating toward the +X target)", v.vx)
	}
	if v.x <= startX {
		t.Fatalf("vex x = %v, want > start %v (flew toward the target)", v.x, startX)
	}
}

// TestVexSkippedByPhysics: the vex is a flyer -- it must be skipped by the generic tickPhysics gravity path
// (Vex.tick noPhysics + noGravity). After a full tickPhysics pass a stationary vex has NOT gained downward
// velocity from gravity.
func TestVexSkippedByPhysics(t *testing.T) {
	loop, floorY := vexLoop(t)
	v := loop.spawnVex(0, 8.5, float64(floorY+20), 8.5, 8, floorY+20, 8, 600)
	v.vx, v.vy, v.vz = 0, 0, 0

	loop.tickPhysics()

	if v.vy != 0 {
		t.Fatalf("vex vy = %v after tickPhysics, want 0 (a flyer is skipped by gravity)", v.vy)
	}
}
