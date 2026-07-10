package server

// firework_rocket_test.go -- pins the FireworkRocketEntity port: the lifetime RNG formula + draw order
// (10*(1+flightDuration) + nextInt(6) + nextInt(7)), the elytra boost (accelerate a fall-flying rider along
// its look), and the detonation explosion damage (5 + 2*stars, falloff by distance).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestFireworkLifetimeRNGFormula: the lifetime is 10*(1+flightDuration) + nextInt(6) + nextInt(7), drawn on
// the firework's own per-entity stream in that ORDER (after the two init-velocity triangle draws). We
// reconstruct the exact draw sequence from the same seed (the entity id) and confirm the spawned lifetime.
func TestFireworkLifetimeRNGFormula(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	const flightDuration = 2
	fw := loop.spawnFreeFirework(0, 8.5, float64(floorY+1), 8.5, flightDuration, 1, false)

	// Reproduce the spawn-time draw order on a fresh stream seeded from the SAME entity id:
	//   triangle(0, 0.002297)  [vx: 2 nextDouble draws]
	//   triangle(0, 0.002297)  [vz: 2 nextDouble draws]
	//   nextInt(6)             [lifetime term a]
	//   nextInt(7)             [lifetime term b]
	r := newEntityRandom(uint64(fw.id))
	_ = arrowTriangle(r, 0, 0.002297) // vx
	_ = arrowTriangle(r, 0, 0.002297) // vz
	a := r.nextInt(6)
	b := r.nextInt(7)
	want := 10*(1+flightDuration) + a + b

	if fw.fireworkLifetime != want {
		t.Fatalf("firework lifetime = %d, want %d (10*(1+%d)+nextInt(6)=%d+nextInt(7)=%d)",
			fw.fireworkLifetime, want, flightDuration, a, b)
	}
	// The base term is 10*(1+flightDuration); the RNG adds 0..11.
	base := 10 * (1 + flightDuration)
	if fw.fireworkLifetime < base || fw.fireworkLifetime > base+11 {
		t.Fatalf("lifetime %d out of [%d, %d]", fw.fireworkLifetime, base, base+11)
	}
}

// TestFireworkInitVelocity: the initial deltaMovement is (triangle(0,0.002297), 0.05, triangle(0,0.002297))
// -- the small launch jitter with a fixed 0.05 upward component.
func TestFireworkInitVelocity(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	fw := loop.spawnFreeFirework(0, 8.5, float64(floorY+1), 8.5, 1, 0, false)

	if fw.vy != 0.05 {
		t.Fatalf("firework init vy = %v, want 0.05", fw.vy)
	}
	// The x/z jitter is tiny (|triangle(0, 0.002297)| < 0.0023).
	if math.Abs(fw.vx) > 0.0023 || math.Abs(fw.vz) > 0.0023 {
		t.Fatalf("firework init jitter too large: vx=%v vz=%v", fw.vx, fw.vz)
	}
}

// TestFireworkElytraBoost: a firework attached to a fall-flying player accelerates the rider's velocity
// along its look direction each tick (delta += look*0.1 + (look*1.5 - delta)*0.5). With the player looking
// straight along +Z (yaw 0, pitch 0 -> look (0,0,1)) starting from rest, the rider gains +Z velocity.
func TestFireworkElytraBoost(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	p := combatPlayer(loop, 1)
	p.x, p.y, p.z = 8.5, float64(floorY+20), 8.5
	p.yaw, p.pitch = 0, 0 // look = (sin0*cos0, -sin0, cos0*cos0) = (0, 0, 1)
	p.fallFlying = true
	p.playerEntity = &Entity{id: p.entityID, x: p.x, y: p.y, z: p.z}

	fw := loop.spawnAttachedFirework(p.entityID, p.entityID, p.x, p.y, p.z, 1, 0)

	beforeVZ := p.playerEntity.vz
	loop.tickFirework(fw)

	// look = (0,0,1); delta starts 0. dvz = 1*0.1 + (1*1.5 - 0)*0.5 = 0.1 + 0.75 = 0.85.
	wantVZ := beforeVZ + (1.0*fireworkBoostLookScale + (1.0*fireworkBoostAimScale-0.0)*fireworkBoostLerp)
	if math.Abs(p.playerEntity.vz-wantVZ) > 1e-9 {
		t.Fatalf("rider vz after boost = %v, want %v (0.85)", p.playerEntity.vz, wantVZ)
	}
	// The horizontal look components are 0, so vx/vy stay 0.
	if math.Abs(p.playerEntity.vx) > 1e-9 {
		t.Fatalf("rider vx after boost = %v, want ~0", p.playerEntity.vx)
	}
}

// TestFireworkDetonationDamageFalloff: at life > lifetime a firework with N stars detonates and damages a
// LivingEntity within 5 blocks for (5 + 2*N) * sqrt((5 - dist)/5). A firework with 0 stars deals NO damage.
func TestFireworkDetonationDamageFalloff(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	// Place a pig 1 block away (distance 1.0) with full health.
	pig := mobEffectTestEntity(loop, entity.Pig, 8.5, float64(floorY+1), 9.5)
	pig.health = 20.0

	// Spawn a free firework at the pig's neighbour cell, with 1 explosion star.
	fw := loop.spawnFreeFirework(0, 8.5, float64(floorY+1), 8.5, 1, 1, false)
	// Force detonation this tick.
	fw.fireworkLife = fw.fireworkLifetime + 1

	loop.dealFireworkExplosionDamage(fw)

	// baseDamage = 5 + 2*1 = 7. dist from (8.5, y, 8.5) center to pig center: dx=0, dz=1, dy=~small.
	if pig.health >= 20.0 {
		t.Fatalf("pig took no firework detonation damage (health %v)", pig.health)
	}
	took := 20.0 - float64(pig.health)
	if took <= 0 || took > 7.0 {
		t.Fatalf("pig firework damage = %v, want in (0, 7] (5+2*1 base with falloff)", took)
	}

	// A 0-star firework deals nothing.
	pig2 := mobEffectTestEntity(loop, entity.Pig, 20.5, float64(floorY+1), 20.5)
	pig2.health = 20.0
	fw0 := loop.spawnFreeFirework(0, 20.5, float64(floorY+1), 20.5, 1, 0, false)
	loop.dealFireworkExplosionDamage(fw0)
	if pig2.health != 20.0 {
		t.Fatalf("0-star firework dealt %v damage, want 0", 20.0-float64(pig2.health))
	}
}
