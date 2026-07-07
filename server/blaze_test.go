package server

// blaze_test.go -- deterministic pins for the hostile Blaze (net.minecraft.world.entity.monster.Blaze,
// 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 20 / ATTACK_DAMAGE 6 / MOVEMENT_SPEED
// 0.23 / FOLLOW_RANGE 48), the BlazeAttackGoal 3-SmallFireball burst fired at the attack-step cadence
// toward a target, the CHARGED flag flip during the attack, the SmallFireball 5.0 fire damage on hit, and
// the water-sensitivity drown damage (isSensitiveToWater).

import (
	"math"
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world"
)

// blazeLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick blazes. Returns the
// loop, the chunk manager (for placing water), and the floor Y.
func blazeLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestBlazeSpawnDefaults: spawnBlaze builds a blaze rendering as entity.Blaze.ID with the jar attributes
// (MAX_HEALTH 20 -> health 20, ATTACK_DAMAGE 6.0, MOVEMENT_SPEED 0.23000000417232513, FOLLOW_RANGE 48.0)
// and a per-entity rng.
func TestBlazeSpawnDefaults(t *testing.T) {
	loop, _, floorY := blazeLoop(t)
	b := loop.spawnBlaze(8.5, float64(floorY+1), 8.5)
	if b.typ != entity.Blaze.ID {
		t.Fatalf("blaze typ = %d, want entity.Blaze.ID %d", b.typ, entity.Blaze.ID)
	}
	if !b.isBlaze {
		t.Fatal("blaze not marked isBlaze")
	}
	if math.Abs(float64(b.health)-20.0) > 1e-6 {
		t.Fatalf("blaze health = %v, want 20.0 (MAX_HEALTH)", b.health)
	}
	if got := b.getAttributeValue(attribute.MaxHealth); math.Abs(got-20.0) > 1e-9 {
		t.Fatalf("blaze MAX_HEALTH = %v, want 20.0", got)
	}
	if got := b.getAttributeValue(attribute.AttackDamage); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("blaze ATTACK_DAMAGE = %v, want 6.0", got)
	}
	if got := b.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.23000000417232513) > 1e-12 {
		t.Fatalf("blaze MOVEMENT_SPEED = %v, want 0.23000000417232513", got)
	}
	if got := b.getAttributeValue(attribute.FollowRange); math.Abs(got-48.0) > 1e-9 {
		t.Fatalf("blaze FOLLOW_RANGE = %v, want 48.0", got)
	}
	if b.ai == nil || b.ai.rng == nil {
		t.Fatal("blaze has no minimal AI / rng (mobRandom would not be per-entity seeded)")
	}
}

// smallFireballCount counts the live small-fireball hurting projectiles in the global region.
func smallFireballCount(loop *TickLoop) int {
	n := 0
	for _, e := range loop.regions[globalRegion].entities.byID {
		if e.isHurting && e.hurtingKind == hurtSmallFireball {
			n++
		}
	}
	return n
}

// TestBlazeFiresThreeFireballBurst: at range with line of sight, the BlazeAttackGoal cadence advances the
// attackStep; step 1 charges (setCharged true, NO fireball), and steps 2, 3, 4 each spawn ONE SmallFireball
// heading toward the target (the 3-fireball burst). Then step > 4 resets (attackStep 0, setCharged false).
// The test drives the cadence by zeroing attackTime before each advance (the faithful attackTime <= 0 gate).
func TestBlazeFiresThreeFireballBurst(t *testing.T) {
	loop, _, floorY := blazeLoop(t)
	by := float64(floorY + 30) // high, open air, clear LoS to a player at the same height
	b := loop.spawnBlaze(8.5, by, 8.5)
	p := combatTestPlayer(loop, 18.5, by, 8.5, 4242) // ~10 blocks +X, within FOLLOW_RANGE 48, outside melee 4
	b.ai.attackTargetID = p.entityID

	// STEP 1 (charge): attackTime 0 -> advance. setCharged(true), no fireball.
	b.blazeAttackTime = 0
	loop.blazeAttackGoalTick(b)
	if b.blazeAttackStep != 1 {
		t.Fatalf("after the first advance, attackStep = %d, want 1 (charge)", b.blazeAttackStep)
	}
	if !b.blazeCharged {
		t.Fatal("blaze not CHARGED after step 1 (setCharged(true) at attackStep==1)")
	}
	if smallFireballCount(loop) != 0 {
		t.Fatalf("blaze fired a fireball at the charge step (fireballs=%d, want 0)", smallFireballCount(loop))
	}

	// STEPS 2, 3, 4 (the burst): each advance spawns exactly ONE SmallFireball.
	for step := 2; step <= 4; step++ {
		b.blazeAttackTime = 0
		loop.blazeAttackGoalTick(b)
		if int(b.blazeAttackStep) != step {
			t.Fatalf("advance to step %d: attackStep = %d", step, b.blazeAttackStep)
		}
		if got := smallFireballCount(loop); got != step-1 {
			t.Fatalf("after step %d, fireballs = %d, want %d", step, got, step-1)
		}
	}
	if smallFireballCount(loop) != 3 {
		t.Fatalf("blaze burst fired %d fireballs, want 3", smallFireballCount(loop))
	}

	// A fired SmallFireball heads toward the +X target and is owned by the blaze.
	var fb *Entity
	for _, e := range loop.regions[globalRegion].entities.byID {
		if e.isHurting && e.hurtingKind == hurtSmallFireball {
			fb = e
			break
		}
	}
	if fb == nil {
		t.Fatal("no small fireball present after the burst")
	}
	if fb.hurtOwnerID != b.id {
		t.Fatalf("fireball owner = %d, want the blaze %d", fb.hurtOwnerID, b.id)
	}
	if fb.vx <= 0 {
		t.Fatalf("fireball vx = %v, want > 0 (heading toward the +X target)", fb.vx)
	}

	// STEP RESET: the 5th advance resets (attackStep 0, setCharged false, no new fireball beyond the 3).
	b.blazeAttackTime = 0
	loop.blazeAttackGoalTick(b)
	if b.blazeAttackStep != 0 {
		t.Fatalf("after the reset advance, attackStep = %d, want 0", b.blazeAttackStep)
	}
	if b.blazeCharged {
		t.Fatal("blaze still CHARGED after the reset (setCharged(false) at attackStep > 4)")
	}
}

// TestBlazeChargedFlagFlips: the CHARGED flag (blazeCharged, DATA_FLAGS_ID bit 1) is set at attackStep 1
// and cleared on the step reset (> 4) and on losing the target (BlazeAttackGoal.stop). Drive the cadence
// and watch the flip.
func TestBlazeChargedFlagFlips(t *testing.T) {
	loop, _, floorY := blazeLoop(t)
	by := float64(floorY + 30)
	b := loop.spawnBlaze(8.5, by, 8.5)
	p := combatTestPlayer(loop, 18.5, by, 8.5, 4242)
	b.ai.attackTargetID = p.entityID

	if b.blazeCharged {
		t.Fatal("blaze CHARGED before any attack step")
	}
	// Step 1 -> charged true.
	b.blazeAttackTime = 0
	loop.blazeAttackGoalTick(b)
	if !b.blazeCharged {
		t.Fatal("blaze not CHARGED at attackStep 1")
	}
	// Advance through steps 2..4 (still charged) then the reset (charged false).
	for step := 2; step <= 5; step++ {
		b.blazeAttackTime = 0
		loop.blazeAttackGoalTick(b)
	}
	if b.blazeCharged {
		t.Fatal("blaze still CHARGED after the step reset (> 4)")
	}

	// Re-charge, then lose the target: BlazeAttackGoal.stop clears CHARGED.
	b.blazeAttackTime = 0
	loop.blazeAttackGoalTick(b)
	if !b.blazeCharged {
		t.Fatal("blaze not re-CHARGED at step 1")
	}
	p.dead = true
	loop.blazeAcquireNearestPlayer(b) // target lost -> setTarget(null) + stop()
	if b.blazeCharged {
		t.Fatal("blaze still CHARGED after losing the target (stop must setCharged(false))")
	}
}

// TestSmallFireballDealsFireDamage: a SmallFireball fired at a player deals 5.0 fire damage on impact
// (SmallFireball.onHitEntity: igniteForSeconds(5) [player fire deferred]; hurt(fireball, 5.0)) and is
// discarded after the hit. The damage source is the fireball (an is_fire source).
func TestSmallFireballDealsFireDamage(t *testing.T) {
	loop, _, floorY := blazeLoop(t)
	py := float64(floorY + 5)
	victim := combatTestPlayer(loop, 8.5, py, 12.5, 4242)
	victim.health = 20.0
	start := victim.health

	// Fire straight at the player (a small fireball, owner 999).
	fb := loop.spawnHurtingProjectile(999, hurtSmallFireball, 8.5, py+playerHeight*0.5, 11.4, 0.0, 0.0, 1.0)

	present := true
	for i := 0; i < 40 && present; i++ {
		loop.withRegion(loop.regions[globalRegion], func() { loop.tickHurtingProjectile(fb) })
		_, present = loop.regions[globalRegion].entities.get(fb.id)
	}
	if present {
		t.Fatal("small fireball never hit / discarded (it should hit the player)")
	}
	dealt := start - victim.health
	if math.Abs(float64(dealt)-smallFireballDamage) > 1e-6 {
		t.Fatalf("victim took %v damage, want %v (SmallFireball.onHitEntity hurt 5.0)", dealt, smallFireballDamage)
	}
}

// TestBlazeWaterSensitivity: the Blaze overrides isSensitiveToWater() to true, so standing in water deals
// 1.0 drown damage per tick (LivingEntity.aiStep tail: if (isSensitiveToWater && isInWaterOrRain)
// hurtServer(drown(), 1.0F)). A dry blaze takes NO such damage.
func TestBlazeWaterSensitivity(t *testing.T) {
	loop, mgr, floorY := blazeLoop(t)

	// A dry blaze on the floor takes no water damage.
	dry := loop.spawnBlaze(8.5, float64(floorY+1), 8.5)
	dry.health = 20.0
	loop.blazeWaterSensitivity(dry)
	if math.Abs(float64(dry.health)-20.0) > 1e-6 {
		t.Fatalf("dry blaze took water damage: health = %v, want 20.0", dry.health)
	}

	// Flood the blaze's feet cell with water; now isInWaterOrRain is true -> 1.0 drown damage per tick.
	wet := loop.spawnBlaze(5.5, float64(floorY+1), 5.5)
	wet.health = 20.0
	water := block.DefaultStateID["minecraft:water"]
	mgr.SetBlock(pk.Position{X: 5, Y: floorY + 1, Z: 5}, water, dimMinY)
	if !loop.entityIsInWaterOrRain(wet) {
		t.Fatal("wet blaze not detected in water (entityIsInWaterOrRain false)")
	}
	loop.blazeWaterSensitivity(wet)
	dealt := 20.0 - float64(wet.health)
	if math.Abs(dealt-blazeWaterDamage) > 1e-6 {
		t.Fatalf("wet blaze took %v water damage, want %v (hurtServer(drown(), 1.0F))", dealt, blazeWaterDamage)
	}
}
