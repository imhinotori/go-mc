package server

// wither_test.go -- deterministic pins for the Wither boss (net.minecraft.world.entity.boss.wither
// .WitherBoss + the reused WitherSkull, 1:1 javap this task). Verifies: (a) the spawn attributes
// (MAX_HEALTH 300 / MOVEMENT_SPEED 0.6 / FLYING_SPEED 0.6 / FOLLOW_RANGE 40 / ARMOR 4) + makeInvulnerable
// (220 invuln ticks + health = maxHealth/3 == 100 + progress 0); (b) the invulnerable charge-up rejects
// all damage (hurtServer gate) + the countdown reaches 0; (c) a WitherSkull deals 8.0 to a player + a
// dangerous skull carries the 0.73 inertia flag; (d) isPowered flips below 50 percent HP; (e) death drops
// a NETHER_STAR with the extended lifetime.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// witherLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick the wither.
func witherLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestWitherSpawnDefaults: spawnWither builds a wither rendering as entity.Wither.ID with the jar
// attributes, makeInvulnerable leaves health at maxHealth/3 == 100 + invulnerableTicks 220 + progress 0.
func TestWitherSpawnDefaults(t *testing.T) {
	loop, floorY := witherLoop(t)
	w := loop.spawnWither(8.5, float64(floorY+2), 8.5)
	if w.typ != entity.Wither.ID {
		t.Fatalf("wither typ = %d, want entity.Wither.ID %d", w.typ, entity.Wither.ID)
	}
	if w.wither == nil {
		t.Fatal("wither has no witherState (e.wither nil)")
	}
	if got := w.getAttributeValue(attribute.MaxHealth); math.Abs(got-300.0) > 1e-9 {
		t.Fatalf("wither MAX_HEALTH = %v, want 300.0", got)
	}
	if got := w.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.6000000238418579) > 1e-12 {
		t.Fatalf("wither MOVEMENT_SPEED = %v, want 0.6000000238418579", got)
	}
	if got := w.getAttributeValue(attribute.FlyingSpeed); math.Abs(got-0.6000000238418579) > 1e-12 {
		t.Fatalf("wither FLYING_SPEED = %v, want 0.6000000238418579", got)
	}
	if got := w.getAttributeValue(attribute.FollowRange); math.Abs(got-40.0) > 1e-9 {
		t.Fatalf("wither FOLLOW_RANGE = %v, want 40.0", got)
	}
	if got := w.getAttributeValue(attribute.Armor); math.Abs(got-4.0) > 1e-9 {
		t.Fatalf("wither ARMOR = %v, want 4.0", got)
	}
	if math.Abs(float64(w.health)-100.0) > 1e-4 {
		t.Fatalf("wither health = %v after makeInvulnerable, want 100.0 (maxHealth/3)", w.health)
	}
	if w.wither.invulnerableTicks != 220 {
		t.Fatalf("wither invulnerableTicks = %d, want 220", w.wither.invulnerableTicks)
	}
	if w.wither.bossProgress != 0.0 {
		t.Fatalf("wither bossProgress = %v, want 0.0", w.wither.bossProgress)
	}
}

// TestWitherInvulnerableChargeImmunity: while invulnerableTicks > 0, a normal player hit is REJECTED by the
// hurtServer gate (getInvulnerableTicks() > 0 && !BYPASSES_INVULNERABILITY -> false).
func TestWitherInvulnerableChargeImmunity(t *testing.T) {
	loop, floorY := witherLoop(t)
	w := loop.spawnWither(8.5, float64(floorY+2), 8.5)
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 8.5, 8001)
	before := w.health
	owner := loop.regionForEntity(w)
	loop.withRegion(owner, func() {
		loop.applyDamageEntity(w, damageSourcePlayerAttack(p.entityID), 50.0)
	})
	if math.Abs(float64(w.health)-float64(before)) > 1e-4 {
		t.Fatalf("wither took damage during the invuln charge-up (%v -> %v), want no change (immune)", before, w.health)
	}
}

// TestWitherChargeUpCompletes: driving the invuln countdown 220 ticks brings invulnerableTicks to 0 (the
// charge-up completes, the boss enters the fight arm) and the charge-up heal-10 keeps it at/above 100.
func TestWitherChargeUpCompletes(t *testing.T) {
	loop, floorY := witherLoop(t)
	w := loop.spawnWither(8.5, float64(floorY+2), 8.5)
	owner := loop.regionForEntity(w)
	loop.withRegion(owner, func() {
		for i := 0; i < 220; i++ {
			loop.gametime++
			loop.witherAiStep(w)
		}
	})
	if w.wither.invulnerableTicks != 0 {
		t.Fatalf("wither invulnerableTicks = %d after 220 ticks, want 0 (charge-up complete)", w.wither.invulnerableTicks)
	}
	if w.health < 100.0 {
		t.Fatalf("wither health = %v after the charge-up, want >= 100.0 (charge-up heal-10)", w.health)
	}
}

// TestWitherIsPowered: isPowered() flips true below 50 percent HP (health <= maxHealth/2 == 150).
func TestWitherIsPowered(t *testing.T) {
	loop, floorY := witherLoop(t)
	w := loop.spawnWither(8.5, float64(floorY+2), 8.5)
	w.health = 151.0
	if witherIsPowered(w) {
		t.Fatalf("witherIsPowered true at health 151 (> 150), want false")
	}
	w.health = 150.0
	if !witherIsPowered(w) {
		t.Fatalf("witherIsPowered false at health 150 (== maxHealth/2), want true")
	}
}

// TestWitherSkullDamage: a WitherSkull fired by the wither at a player deals 8.0 damage on hit (the
// hurtWitherSkull owner-LivingEntity path).
func TestWitherSkullDamage(t *testing.T) {
	loop, floorY := witherLoop(t)
	w := loop.spawnWither(8.5, float64(floorY+2), 8.5)
	w.wither.invulnerableTicks = 0
	p := combatTestPlayer(loop, 8.5, float64(floorY+3), 12.5, 8002)
	startHP := p.health
	loop.witherPerformRangedAttackXYZ(w, 0, p.x, p.y+playerHeight*0.5, p.z, false)
	owner := loop.regionForEntity(w)
	var skull *Entity
	for _, e := range owner.entities.byID {
		if e.isHurting && e.hurtingKind == hurtWitherSkull {
			skull = e
		}
	}
	if skull == nil {
		t.Fatal("no wither skull spawned by witherPerformRangedAttackXYZ")
	}
	// Isolate the skull's DIRECT hit damage (hurtWitherSkull owner-LivingEntity path: 8.0) from the power-1
	// explosion the full onHit tail adds -- call hurtingOnHitEntity directly (the per-kind entity-hit port).
	loop.withRegion(owner, func() {
		loop.hurtingOnHitEntity(skull, p)
	})
	dealt := startHP - p.health
	if math.Abs(float64(dealt)-8.0) > 1e-4 {
		t.Fatalf("wither skull direct hit dealt %v to the player, want 8.0 (hurtWitherSkull)", dealt)
	}
}

// TestWitherDangerousSkullInertia: a DANGEROUS skull carries the hurtDangerous flag (0.73 inertia override).
func TestWitherDangerousSkullInertia(t *testing.T) {
	loop, floorY := witherLoop(t)
	w := loop.spawnWither(8.5, float64(floorY+2), 8.5)
	w.wither.invulnerableTicks = 0
	loop.witherPerformRangedAttackXYZ(w, 1, 20.0, float64(floorY+2), 8.5, true)
	owner := loop.regionForEntity(w)
	var skull *Entity
	for _, e := range owner.entities.byID {
		if e.isHurting && e.hurtingKind == hurtWitherSkull {
			skull = e
		}
	}
	if skull == nil {
		t.Fatal("no wither skull spawned")
	}
	if !skull.hurtDangerous {
		t.Fatal("dangerous skull not marked hurtDangerous (should get the 0.73 inertia)")
	}
}

// TestWitherDeathDropsNetherStar: killing the wither drops exactly one NETHER_STAR with the extended
// lifetime (age == -6000, ItemEntity.setExtendedLifetime).
func TestWitherDeathDropsNetherStar(t *testing.T) {
	loop, floorY := witherLoop(t)
	w := loop.spawnWither(8.5, float64(floorY+2), 8.5)
	w.wither.invulnerableTicks = 0
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 8.5, 8003)
	owner := loop.regionForEntity(w)
	loop.withRegion(owner, func() {
		loop.dieEntity(w, damageSourcePlayerAttack(p.entityID))
	})
	var star *Entity
	stars := 0
	for _, e := range owner.entities.byID {
		if e.isItem && item.ID(e.itemStack.ItemID) == item.NetherStar.ID {
			stars++
			star = e
		}
	}
	if stars != 1 {
		t.Fatalf("wither death dropped %d nether stars, want exactly 1", stars)
	}
	if star.age != itemExtendedLifetimeAge {
		t.Fatalf("nether star age = %d, want %d (setExtendedLifetime)", star.age, itemExtendedLifetimeAge)
	}
}
