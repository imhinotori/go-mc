package server

import (
	"testing"

	"github.com/imhinotori/sulfur/world"
)

// TestHurtingFliesStraightNoGravity: a spawned fireball flies in a STRAIGHT line — unlike an arrow/throwable
// it has NO gravity term, so its vy does not drift downward from a horizontal launch (it re-accelerates
// along its own heading and scales by inertia). Over enough ticks with no target it lands on a block or
// despawns — never runs forever.
func TestHurtingFliesStraightNoGravity(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	ow := world.NewChunkManager()
	for _, r := range loop.regions {
		r.entities = newEntityStore()
		r.world = ow
	}
	// Launch a small fireball horizontally from high up over open air (aim +X, level).
	e := loop.spawnHurtingProjectile(999, hurtSmallFireball, 8.0, 100.0, 8.0, 1.0, 0.0, 0.0)
	if e.vy != 0 {
		t.Fatalf("horizontal launch must have zero vertical velocity, got vy=%.6f", e.vy)
	}
	startY := e.y

	loop.withRegion(loop.regions[globalRegion], func() { loop.tickHurtingProjectile(e) })
	// After one tick with a level launch: vy stays 0 (no gravity), so the projectile has not dropped.
	if e.vy < -1e-9 {
		t.Fatalf("fireball gained downward velocity (gravity leaked): vy=%.6f", e.vy)
	}
	if e.y < startY-1e-6 {
		t.Fatalf("fireball dropped without gravity: startY=%.4f now=%.4f", startY, e.y)
	}

	// It must not exist forever: drive up to the despawn cap; it must be gone (landed or aged).
	present := true
	for i := 0; i < hurtDespawnTicks+5 && present; i++ {
		loop.withRegion(loop.regions[globalRegion], func() { loop.tickHurtingProjectile(e) })
		_, present = loop.regions[globalRegion].entities.get(e.id)
	}
	if present {
		t.Fatalf("fireball never landed or despawned after %d ticks", hurtDespawnTicks+5)
	}
}

// TestHurtingAcceleratesAlongHeading: applyInertia re-accelerates the projectile along its heading before
// scaling by inertia (0.95). With accelerationPower 0.1 on a unit-speed launch, the post-tick speed is
// (1 + 0.1) * 0.95 == 1.045 — strictly the accelerate-then-damp formula, not a plain drag.
func TestHurtingAcceleratesAlongHeading(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	ow := world.NewChunkManager()
	for _, r := range loop.regions {
		r.entities = newEntityStore()
		r.world = ow
	}
	// spawn normalizes the aim vector then scales by accelerationPower (0.1): initial speed == 0.1.
	e := loop.spawnHurtingProjectile(999, hurtSmallFireball, 8.0, 100.0, 8.0, 1.0, 0.0, 0.0)
	if d := e.vx - 0.1; d > 1e-9 || d < -1e-9 {
		t.Fatalf("initial vx should be accelerationPower 0.1, got %.6f", e.vx)
	}
	// One tick: v = (0.1 + 0.1) * 0.95 = 0.19 (accelerate by 0.1 along heading, then *inertia 0.95).
	loop.withRegion(loop.regions[globalRegion], func() { loop.tickHurtingProjectile(e) })
	want := (0.1 + 0.1) * 0.95
	if d := e.vx - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("applyInertia formula wrong: vx=%.6f want=%.6f", e.vx, want)
	}
}

// TestHurtingEntityType maps each kind to its wire entity type.
func TestHurtingEntityType(t *testing.T) {
	if hurtingEntityType(hurtSmallFireball).ID == hurtingEntityType(hurtLargeFireball).ID {
		t.Fatalf("small and large fireball must be distinct entity types")
	}
	if hurtingEntityType(hurtWitherSkull).DisplayName == "" {
		t.Fatalf("wither skull must resolve a real entity type")
	}
}

// TestShouldBurnKind: fireballs burn (shouldBurn true), a wither skull / wind charge do not.
func TestShouldBurnKind(t *testing.T) {
	if !shouldBurnKind(hurtSmallFireball) || !shouldBurnKind(hurtLargeFireball) {
		t.Fatalf("fireballs must shouldBurn")
	}
	if shouldBurnKind(hurtWitherSkull) {
		t.Fatalf("wither skull must NOT shouldBurn")
	}
	if shouldBurnKind(hurtWindCharge) {
		t.Fatalf("wind charge must NOT shouldBurn")
	}
}

// TestWindChargeNoDrag: a wind charge has inertia 1.0 (no drag in air or water) — its speed only grows by
// the acceleration term. A wind charge thrown at power 1.5 keeps at least its launch speed after a tick
// (1.5 -> (1.5 + 0.1)*1.0 = 1.6), unlike a fireball whose 0.95 inertia would shrink a fast launch.
func TestWindChargeNoDrag(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	ow := world.NewChunkManager()
	for _, r := range loop.regions {
		r.entities = newEntityStore()
		r.world = ow
	}
	// Player-throw variant: launch speed == power 1.5 (shootFromRotation overrides the accelPow delta).
	e := loop.spawnHurtingProjectileShot(999, hurtWindCharge, 8.0, 100.0, 8.0, 1.0, 0.0, 0.0, 1.5)
	if d := e.vx - 1.5; d > 1e-9 || d < -1e-9 {
		t.Fatalf("wind charge launch speed should be power 1.5, got vx=%.6f", e.vx)
	}
	loop.withRegion(loop.regions[globalRegion], func() { loop.tickHurtingProjectile(e) })
	// (1.5 + 0.1) * 1.0 == 1.6 — accelerate along heading, inertia 1.0 (no drag).
	want := (1.5 + 0.1) * 1.0
	if d := e.vx - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("wind charge inertia must be 1.0 (no drag): vx=%.6f want=%.6f", e.vx, want)
	}
	if e.vy < -1e-9 {
		t.Fatalf("wind charge must have NO gravity: vy=%.6f", e.vy)
	}
}
