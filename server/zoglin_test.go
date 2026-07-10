package server

// zoglin_test.go -- deterministic pins for the Zoglin (net.minecraft.world.entity.monster.Zoglin, 1:1 javap
// this session). Verifies the spawn attributes (MAX_HEALTH 40 / MOVEMENT_SPEED 0.3 / KNOCKBACK_RESISTANCE
// 0.6 / ATTACK_KNOCKBACK 1.0 / ATTACK_DAMAGE 6 adult, 0.5 baby), the INDISCRIMINATE hostility (it acquires a
// PLAYER and a MOB, but NOT another zoglin or a creeper), the knock-up toss on a landed hit, and that a
// converting hoglin produces a real zoglin (terminal -- never converts back).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/world"
)

// zoglinLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick zoglins.
func zoglinLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestZoglinSpawnDefaults: spawnZoglin builds an adult zoglin rendering as entity.Zoglin.ID with the jar
// attributes (MAX_HEALTH 40 -> health 40, ATTACK_DAMAGE 6.0, MOVEMENT_SPEED 0.3, KNOCKBACK_RESISTANCE 0.6,
// ATTACK_KNOCKBACK 1.0).
func TestZoglinSpawnDefaults(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	zg := loop.spawnZoglin(8.5, float64(floorY+1), 8.5, false)
	if zg.typ != entity.Zoglin.ID {
		t.Fatalf("zoglin typ = %d, want entity.Zoglin.ID %d", zg.typ, entity.Zoglin.ID)
	}
	if !zg.isZoglin {
		t.Fatal("zoglin not marked isZoglin")
	}
	if math.Abs(float64(zg.health)-40.0) > 1e-6 {
		t.Fatalf("zoglin health = %v, want 40.0 (MAX_HEALTH)", zg.health)
	}
	if got := zg.getAttributeValue(attribute.AttackDamage); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("zoglin ATTACK_DAMAGE = %v, want 6.0 (adult)", got)
	}
	if got := zg.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.30000001192092896) > 1e-12 {
		t.Fatalf("zoglin MOVEMENT_SPEED = %v, want 0.30000001192092896", got)
	}
	if got := zg.getAttributeValue(attribute.KnockbackResistance); math.Abs(got-0.6000000238418579) > 1e-12 {
		t.Fatalf("zoglin KNOCKBACK_RESISTANCE = %v, want 0.6000000238418579", got)
	}
	if got := zg.getAttributeValue(attribute.AttackKnockback); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("zoglin ATTACK_KNOCKBACK = %v, want 1.0", got)
	}
	if zg.ai == nil || zg.ai.rng == nil {
		t.Fatal("zoglin has no minimal AI / rng")
	}
}

// TestZoglinBabyDamage: a baby zoglin has ATTACK_DAMAGE 0.5 (Zoglin.setBaby) and the halved-scale dims.
func TestZoglinBabyDamage(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	zg := loop.spawnZoglin(8.5, float64(floorY+1), 8.5, true)
	if !zg.isBaby() {
		t.Fatal("baby zoglin not isBaby")
	}
	if got := zg.getAttributeValue(attribute.AttackDamage); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("baby zoglin ATTACK_DAMAGE = %v, want 0.5 (setBaby)", got)
	}
}

// TestZoglinIndiscriminateTargetsPlayer: a zoglin acquires a nearby PLAYER (indiscriminate hostility -- it
// does NOT need to be provoked, unlike the neutral zombified piglin).
func TestZoglinIndiscriminateTargetsPlayer(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	zg := loop.spawnZoglin(8.5, float64(floorY+1), 8.5, false)
	p := addTestPlayer(loop, 61000, zg.x, zg.y, zg.z+1)
	loop.zoglinAcquireNearestTarget(zg)
	if zg.ai.attackTargetID != p.entityID {
		t.Fatalf("zoglin did NOT acquire the player (target=%d, want %d) -- indiscriminate hostility failed", zg.ai.attackTargetID, p.entityID)
	}
}

// TestZoglinIndiscriminateTargetsMob: a zoglin acquires a nearby MOB (a pig) -- it attacks ANY living entity,
// not just players. The keystone of the indiscriminate brain.
func TestZoglinIndiscriminateTargetsMob(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	by := float64(floorY + 1)
	zg := loop.spawnZoglin(8.5, by, 8.5, false)
	pig := NewEntity(5151, entity.Pig, 10.0, by, 8.5) // ~1.5 blocks away, within FOLLOW_RANGE
	pig.health = 10.0
	loop.only().entities.add(pig)
	loop.zoglinAcquireNearestTarget(zg)
	if zg.ai.attackTargetID != pig.id {
		t.Fatalf("zoglin did NOT acquire the pig mob (target=%d, want %d) -- indiscriminate mob hostility failed", zg.ai.attackTargetID, pig.id)
	}
}

// TestZoglinIgnoresZoglinAndCreeper: a zoglin does NOT target another zoglin or a creeper (the ONLY two
// exclusions in findNearestValidAttackTarget). With only those two nearby, it acquires nothing.
func TestZoglinIgnoresZoglinAndCreeper(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	by := float64(floorY + 1)
	zg := loop.spawnZoglin(8.5, by, 8.5, false)
	loop.spawnZoglin(9.5, by, 8.5, false)                    // another zoglin -- excluded
	creeper := NewEntity(7777, entity.Creeper, 9.5, by, 9.5) // a creeper -- excluded
	creeper.health = 20.0
	loop.only().entities.add(creeper)
	loop.zoglinAcquireNearestTarget(zg)
	if zg.ai.attackTargetID != 0 {
		t.Fatalf("zoglin targeted an excluded entity (id=%d) -- must ignore zoglins and creepers", zg.ai.attackTargetID)
	}
}

// TestZoglinKnockUpToss: an adult zoglin's melee hit FLINGS the player UPWARD (the shared HoglinBase
// throwTarget: vertical d17 = delta * nextFloat() * 0.5) and adds a horizontal impulse. delta =
// ATTACK_KNOCKBACK 1.0 - target KNOCKBACK_RESISTANCE 0.0 = 1.0 > 0, so the toss fires.
func TestZoglinKnockUpToss(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	by := float64(floorY + 1)
	zg := loop.spawnZoglin(8.5, by, 8.5, false)
	p := combatTestPlayer(loop, 9.1, by, 8.5, 5151)
	p.playerEntity = &Entity{id: p.entityID}
	zg.ai.attackTargetID = p.entityID
	zg.meleeCooldown = 0

	start := p.health
	loop.zoglinDoHurtTarget(zg, p.entityID)

	if start-p.health <= 0 {
		t.Fatalf("zoglin melee dealt %v damage, want > 0", start-p.health)
	}
	if p.playerEntity.vy <= 0 {
		t.Fatalf("zoglin toss vy = %v, want > 0 (the knock-up fling)", p.playerEntity.vy)
	}
	if p.playerEntity.vx == 0 && p.playerEntity.vz == 0 {
		t.Fatal("zoglin toss applied no horizontal impulse (throwTarget push missing)")
	}
	if zg.hoglinAttackAnimTicks != zoglinAttackAnimTicks {
		t.Fatalf("zoglin attackAnimationRemainingTicks = %d, want %d", zg.hoglinAttackAnimTicks, zoglinAttackAnimTicks)
	}
}

// TestZoglinMobKnockUpToss: a zoglin's melee on a MOB victim also FLINGS it upward (zoglinThrowTargetEntity
// via pushEntityImpulse) -- the indiscriminate toss reaches mobs, not just players.
func TestZoglinMobKnockUpToss(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	by := float64(floorY + 1)
	zg := loop.spawnZoglin(8.5, by, 8.5, false)
	pig := NewEntity(5151, entity.Pig, 9.1, by, 8.5)
	pig.health = 10.0
	loop.only().entities.add(pig)
	zg.ai.attackTargetID = pig.id
	zg.meleeCooldown = 0

	start := pig.health
	loop.zoglinDoHurtTarget(zg, pig.id)

	if start-pig.health <= 0 {
		t.Fatalf("zoglin melee on the pig dealt %v damage, want > 0", start-pig.health)
	}
	if pig.vy <= 0 {
		t.Fatalf("zoglin mob-toss vy = %v, want > 0 (the knock-up fling on a mob victim)", pig.vy)
	}
}

// TestZoglinNeverConverts (TestConvert family): a zoglin is TERMINAL -- there is no conversion tick. Ticking
// it many times leaves it a zoglin. Complements the hoglin->zoglin conversion test (which lands the real mob).
func TestZoglinNeverConverts(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	zg := loop.spawnZoglin(8.5, float64(floorY+1), 8.5, false)
	for i := 0; i < 500; i++ {
		loop.zoglinAiStep(zg)
	}
	if zg.typ != entity.Zoglin.ID || !zg.isZoglin {
		t.Fatalf("zoglin converted or lost its type after 500 ticks (typ=%d isZoglin=%v) -- it must be TERMINAL", zg.typ, zg.isZoglin)
	}
}

// TestConvertHoglinToZoglin: hoglinFinishConversion produces a REAL, functioning zoglin -- the type flips,
// the flags are set, ATTACK_DAMAGE is the zoglin adult 6.0, and it has AI so zoglinAiStep drives it.
func TestConvertHoglinToZoglin(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	h := loop.spawnHoglin(8.5, float64(floorY+1), 8.5, false, dimOverworld)
	loop.hoglinFinishConversion(h)
	if h.typ != entity.Zoglin.ID {
		t.Fatalf("hoglin->zoglin conversion type = %d, want %d", h.typ, entity.Zoglin.ID)
	}
	if !h.isZoglin || h.isHoglin {
		t.Fatalf("post-conversion flags wrong: isZoglin=%v isHoglin=%v", h.isZoglin, h.isHoglin)
	}
	if got := h.getAttributeValue(attribute.AttackDamage); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("converted zoglin ATTACK_DAMAGE = %v, want 6.0 (adult)", got)
	}
	if h.ai == nil {
		t.Fatal("converted zoglin has no AI -- it would be inert")
	}
	// It now behaves as a zoglin: indiscriminately acquires a nearby player.
	p := addTestPlayer(loop, 61000, h.x, h.y, h.z+1)
	loop.zoglinAcquireNearestTarget(h)
	if h.ai.attackTargetID != p.entityID {
		t.Fatalf("the converted zoglin did not acquire a player (target=%d, want %d)", h.ai.attackTargetID, p.entityID)
	}
}

// TestConvertPiglinToZombifiedPiglin: piglinFinishConversion produces a REAL, functioning zombified piglin
// (the type-swap + AI wiring) -- the returned entity is marked isZombifiedPiglin, has AI, and starts NEUTRAL.
func TestConvertPiglinToZombifiedPiglin(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	pig := loop.spawnPiglin(8.5, float64(floorY+1), 8.5, false)
	zp := loop.piglinFinishConversion(pig)
	if zp == nil {
		t.Fatal("piglin conversion returned nil")
	}
	if zp.typ != entity.ZombifiedPiglin.ID || !zp.isZombifiedPiglin {
		t.Fatalf("piglin->zombified_piglin conversion wrong: typ=%d isZombifiedPiglin=%v", zp.typ, zp.isZombifiedPiglin)
	}
	if zp.ai == nil {
		t.Fatal("converted zombified piglin has no AI -- it would be inert")
	}
	if zp.angerEndTime != 0 {
		t.Fatalf("converted zombified piglin must start NEUTRAL, got angerEndTime=%d", zp.angerEndTime)
	}
}

// TestZoglinRetaliatesOnHurt: a zoglin hit by a LivingEntity latches ONTO that attacker (Zoglin.hurtServer
// -> setAttackTarget), even a would-be excluded type is irrelevant here (the attacker is a plain mob).
// canAttack + not-much-further -> the 200-tick ATTACK_TARGET grudge is set.
func TestZoglinRetaliatesOnHurt(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	by := float64(floorY + 1)
	zg := loop.spawnZoglin(8.5, by, 8.5, false)
	// The attacker: a wither skeleton mob standing next to the zoglin (a valid, non-ghast LivingEntity).
	atk := NewEntity(9001, entity.WitherSkeleton, 9.5, by, 8.5)
	atk.health = 20.0
	loop.only().entities.add(atk)
	loop.gametime = 1000

	if zg.ai.attackTargetID != 0 {
		t.Fatalf("zoglin started with a target %d", zg.ai.attackTargetID)
	}
	src := damageSourceMobAttack(atk.id)
	loop.applyDamageEntity(zg, src, 3.0)

	if zg.ai.attackTargetID != atk.id {
		t.Fatalf("zoglin did NOT retaliate: target=%d, want attacker %d", zg.ai.attackTargetID, atk.id)
	}
	if want := loop.gametime + int64(zoglinAttackTargetDuration); zg.zoglinAttackTargetExpiry != want {
		t.Fatalf("zoglin latch expiry = %d, want %d (gametime + 200)", zg.zoglinAttackTargetExpiry, want)
	}
}

// TestZoglinLatchHoldsAcrossReacquire: the 200-tick retaliation latch HOLDS the attacker as the target even
// when the acquire scan would otherwise re-derive a nearer valid target -- the grudge memory wins for its
// 200-tick life, then the plain nearest-scan resumes after it expires.
func TestZoglinLatchHoldsAcrossReacquire(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	by := float64(floorY + 1)
	zg := loop.spawnZoglin(8.5, by, 8.5, false)
	// The FAR attacker (a wither skeleton) that hurt the zoglin.
	far := NewEntity(9001, entity.WitherSkeleton, 20.0, by, 8.5)
	far.health = 20.0
	loop.only().entities.add(far)
	// A NEARER pig the plain scan would prefer.
	near := NewEntity(9002, entity.Pig, 9.0, by, 8.5)
	near.health = 10.0
	loop.only().entities.add(near)
	loop.gametime = 1000

	loop.applyDamageEntity(zg, damageSourceMobAttack(far.id), 3.0)
	if zg.ai.attackTargetID != far.id {
		t.Fatalf("zoglin did not latch the far attacker: target=%d, want %d", zg.ai.attackTargetID, far.id)
	}
	// Within the 200-tick window: the acquire scan must NOT swap to the nearer pig.
	loop.gametime = 1000 + 199
	loop.zoglinAcquireNearestTarget(zg)
	if zg.ai.attackTargetID != far.id {
		t.Fatalf("zoglin dropped the latched grudge early: target=%d, want %d (latch active)", zg.ai.attackTargetID, far.id)
	}
	// After expiry (>= 200 ticks): the latch lapses, and the scan re-derives the nearer target.
	loop.gametime = 1000 + 200
	loop.zoglinAcquireNearestTarget(zg)
	if zg.zoglinAttackTargetExpiry != 0 {
		t.Fatalf("zoglin latch not cleared after 200 ticks (expiry=%d)", zg.zoglinAttackTargetExpiry)
	}
	if zg.ai.attackTargetID != near.id {
		t.Fatalf("after latch expiry, zoglin should scan to the nearer pig: target=%d, want %d", zg.ai.attackTargetID, near.id)
	}
}

// TestZoglinMeleeInterval: an ADULT zoglin's swing sets a 40-tick cooldown (MeleeAttack.create(40)); a BABY's
// sets 15 (MeleeAttack.create(15)) -- NOT the shared MeleeAttackGoal.resetAttackCooldown(20).
func TestZoglinMeleeInterval(t *testing.T) {
	loop, _, floorY := zoglinLoop(t)
	by := float64(floorY + 1)

	adult := loop.spawnZoglin(8.5, by, 8.5, false)
	pa := combatTestPlayer(loop, 9.1, by, 8.5, 5151)
	pa.playerEntity = &Entity{id: pa.entityID}
	adult.ai.attackTargetID = pa.entityID
	adult.meleeCooldown = 0
	loop.zoglinAiStep(adult)
	if adult.meleeCooldown != zoglinMeleeCooldownAdult {
		t.Fatalf("adult zoglin melee cooldown = %d, want %d (MeleeAttack.create(40))", adult.meleeCooldown, zoglinMeleeCooldownAdult)
	}

	baby := loop.spawnZoglin(30.5, by, 30.5, true)
	pb := combatTestPlayer(loop, 31.1, by, 30.5, 5152)
	pb.playerEntity = &Entity{id: pb.entityID}
	baby.ai.attackTargetID = pb.entityID
	baby.meleeCooldown = 0
	loop.zoglinAiStep(baby)
	if baby.meleeCooldown != zoglinMeleeCooldownBaby {
		t.Fatalf("baby zoglin melee cooldown = %d, want %d (MeleeAttack.create(15))", baby.meleeCooldown, zoglinMeleeCooldownBaby)
	}
}
