package server

// bee_test.go -- deterministic pins for the Bee (net.minecraft.world.entity.animal.bee.Bee, 1:1 javap
// this session). Verifies the spawn attributes (MAX_HEALTH 10 / FLYING_SPEED 0.6 / MOVEMENT_SPEED 0.3 /
// ATTACK_DAMAGE 2 / FOLLOW_RANGE 16) and the SIGNATURE sting-then-die-over-1200-ticks countdown.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// beeLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick bees.
func beeLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// beeLoopMgr is beeLoop but also hands back the chunk manager (needed to flood a cell for the drown test).
func beeLoopMgr(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestBeeSpawnDefaults: spawnBee builds a bee rendering as entity.Bee.ID with the jar attributes.
func TestBeeSpawnDefaults(t *testing.T) {
	loop, floorY := beeLoop(t)
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	if b.typ != entity.Bee.ID {
		t.Fatalf("bee typ = %d, want entity.Bee.ID %d", b.typ, entity.Bee.ID)
	}
	if !b.isBee {
		t.Fatal("bee not marked isBee")
	}
	if math.Abs(float64(b.health)-10.0) > 1e-6 {
		t.Fatalf("bee health = %v, want 10.0 (MAX_HEALTH)", b.health)
	}
	if got := b.getAttributeValue(attribute.MaxHealth); math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("bee MAX_HEALTH = %v, want 10.0", got)
	}
	if got := b.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.30000001192092896) > 1e-12 {
		t.Fatalf("bee MOVEMENT_SPEED = %v, want 0.30000001192092896", got)
	}
	if got := b.getAttributeValue(attribute.FlyingSpeed); math.Abs(got-0.6000000238418579) > 1e-12 {
		t.Fatalf("bee FLYING_SPEED = %v, want 0.6000000238418579", got)
	}
	if got := b.getAttributeValue(attribute.AttackDamage); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("bee ATTACK_DAMAGE = %v, want 2.0", got)
	}
	if got := b.getAttributeValue(attribute.FollowRange); math.Abs(got-16.0) > 1e-9 {
		t.Fatalf("bee FOLLOW_RANGE = %v, want 16.0 (createMobAttributes, no override)", got)
	}
	if b.ai == nil || b.ai.rng == nil {
		t.Fatal("bee has no minimal AI / rng")
	}
}

// TestBeeStingDeathCountdown: a bee that has stung eventually dies from its own sting -- beeAiStep only
// draws/damages on the (% 5) cadence, and the generic self-damage equals current health (a kill). Drive
// the countdown far enough that the rising-probability roll fires and the bee dies.
func TestBeeStingDeathCountdown(t *testing.T) {
	loop, floorY := beeLoop(t)
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	b.health = 10.0
	b.beeHasStung = true

	// A never-stung bee (fresh) draws nothing; a stung bee counts up. Off-cadence ticks are pure no-ops.
	loop.beeAiStep(b) // timeSinceSting 1 (not % 5) -> no roll
	if b.beeTimeSinceSting != 1 {
		t.Fatalf("timeSinceSting = %d after one tick, want 1", b.beeTimeSinceSting)
	}
	if b.health != 10.0 {
		t.Fatalf("bee took damage off the %% 5 cadence: health = %v", b.health)
	}

	// Drive until the bee dies (the death probability rises as timeSinceSting -> 1200). Bounded loop.
	died := false
	for i := 0; i < 20000 && !died; i++ {
		loop.beeAiStep(b)
		if b.dead || b.health <= 0 {
			died = true
		}
	}
	if !died {
		t.Fatalf("bee never died from its sting after 20000 ticks (timeSinceSting=%d, health=%v)", b.beeTimeSinceSting, b.health)
	}
}

// TestBeeUnStungDrowns pins the customServerAiStep fix: the underwater-drown block runs UNCONDITIONALLY of
// sting state. An un-stung bee submerged in water increments underWaterTicks every tick and, once it
// exceeds 20, takes 1.0F drown damage EVERY tick until it dies. (Regression: the old beeAiStep returned
// early for a never-stung bee, so it never drowned.) Cite Bee.customServerAiStep.
func TestBeeUnStungDrowns(t *testing.T) {
	loop, mgr, floorY := beeLoopMgr(t)
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	b.health = 10.0
	if b.beeHasStung {
		t.Fatal("fresh bee should not have stung")
	}
	// Flood the bee's feet cell so entityInWater is true.
	water := block.DefaultStateID["minecraft:water"]
	mgr.SetBlock(pk.Position{X: 8, Y: floorY + 1, Z: 8}, water, dimMinY)
	if !loop.entityInWater(b) {
		t.Fatal("submerged bee not detected in water")
	}

	// Ticks 1..20: underWaterTicks climbs but never exceeds 20 -> NO drown damage yet.
	for i := 1; i <= beeDrownThreshold; i++ {
		loop.beeAiStep(b)
		if b.beeUnderWaterTicks != i {
			t.Fatalf("underWaterTicks = %d after %d ticks, want %d", b.beeUnderWaterTicks, i, i)
		}
	}
	if b.health != 10.0 {
		t.Fatalf("bee took drown damage at underWaterTicks<=20: health = %v, want 10.0", b.health)
	}
	// Tick 21: underWaterTicks == 21 > 20 -> first 1.0F drown hit lands.
	loop.beeAiStep(b)
	if b.beeUnderWaterTicks != beeDrownThreshold+1 {
		t.Fatalf("underWaterTicks = %d, want %d", b.beeUnderWaterTicks, beeDrownThreshold+1)
	}
	if math.Abs(float64(10.0-b.health)-beeDrownDamage) > 1e-6 {
		t.Fatalf("first drown tick dealt %v, want %v (hurtServer(drown(), 1.0F))", 10.0-b.health, beeDrownDamage)
	}
	// Keep ticking: the un-stung bee drowns to death (no sting ever set). Drown deals 1.0F but the
	// invulnerableTime i-frame gate (LivingEntity.hurtServer) admits an equal-magnitude re-hit only once
	// the grace window falls out of its upper half, so ~1 HP per ~20-tick window -- a generous bound.
	died := false
	for i := 0; i < 5000 && !died; i++ {
		b.invulnerableTime = 0 // the main tick pipeline decrements the i-frame window each tick; do it here
		loop.beeAiStep(b)
		if b.dead || b.health <= 0 {
			died = true
		}
	}
	if !died {
		t.Fatalf("un-stung submerged bee never drowned (health=%v, underWaterTicks=%d)", b.health, b.beeUnderWaterTicks)
	}
	if b.beeHasStung {
		t.Fatal("bee should have drowned WITHOUT ever stinging")
	}
}

// TestBeeDryUnStungNoDamage: a dry, never-stung bee is a pure no-op each tick -- underWaterTicks stays 0 and
// it takes no damage (the water reset branch + the never-stung early return).
func TestBeeDryUnStungNoDamage(t *testing.T) {
	loop, floorY := beeLoop(t)
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	b.health = 10.0
	for i := 0; i < 100; i++ {
		loop.beeAiStep(b)
	}
	if b.beeUnderWaterTicks != 0 {
		t.Fatalf("dry bee underWaterTicks = %d, want 0", b.beeUnderWaterTicks)
	}
	if b.health != 10.0 {
		t.Fatalf("dry un-stung bee took damage: health = %v, want 10.0", b.health)
	}
}

// TestBeeAngerOnPlayerHit: a bee HIT by a player becomes angry (angerEndTime = gameTime + 400 + nextInt(381),
// angerTarget = attacker) via the MOB-NEUT-01 combat store-point -- the SAME UniformInt(400,780) draw the
// wolf/iron_golem/zombified_piglin use (Bee.PERSISTENT_ANGER_TIME). Then the anger-gated acquire targets it.
func TestBeeAngerOnPlayerHit(t *testing.T) {
	loop, floorY := beeLoop(t)
	loop.gametime = 1000
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	// Deterministic rng: capture the SAME nextInt(381) a reference draws (ONE draw for the anger timer).
	b.ai.rng = newEntityRandom(wolfAngerSeed)
	wantOffset := 400 + newEntityRandom(wolfAngerSeed).nextInt(381)
	if wantOffset < 400 || wantOffset > 780 {
		t.Fatalf("precondition: anger offset %d out of [400,780]", wantOffset)
	}
	attacker := addTestPlayer(loop, 61000, b.x, b.y, b.z+1)

	// THE HIT via the combat store-point (the same path a real player melee takes).
	loop.applyDamageEntity(b, damageSourcePlayerAttack(attacker.entityID), 1.0)

	wantEnd := int64(1000) + int64(wantOffset)
	if b.angerEndTime != wantEnd {
		t.Fatalf("angerEndTime = %d, want %d (gameTime 1000 + 400 + nextInt(381) = 1000 + %d)", b.angerEndTime, wantEnd, wantOffset)
	}
	if b.angerTarget != attacker.entityID {
		t.Fatalf("angerTarget = %d, want the attacker %d", b.angerTarget, attacker.entityID)
	}
	if !isAngryAt(loop, b, attacker.entityID) {
		t.Fatal("isAngryAt(attacker) false after the hit -- the bee is not angry")
	}
	// The angry acquire now targets the attacker (BeeBecomeAngryTargetGoal).
	loop.beeAcquireAngryTarget(b)
	if b.ai.attackTargetID != attacker.entityID {
		t.Fatalf("post-hit target = %d, want the attacker %d", b.ai.attackTargetID, attacker.entityID)
	}
}

// TestBeeNeutralUntilHit: a fresh bee next to a player acquires NO target (neutral-until-provoked). The
// keystone: beeAcquireAngryTarget only fires while isAngry().
func TestBeeNeutralUntilHit(t *testing.T) {
	loop, floorY := beeLoop(t)
	loop.gametime = 1000
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	addTestPlayer(loop, 61000, b.x, b.y, b.z) // overlapping, well within FOLLOW_RANGE

	loop.beeAiStep(b)
	if b.ai.attackTargetID != 0 {
		t.Fatalf("a NEUTRAL bee acquired a target on sight (id=%d) -- must stay neutral until hit", b.ai.attackTargetID)
	}
	if b.beeHasStung {
		t.Fatal("a neutral bee stung without being provoked")
	}
}

// TestBeeStingsThenDies: an angered bee adjacent to its target STINGS (beeDoSting: POISON + setHasStung +
// stopBeingAngry) on the melee cooldown/in-range/LOS gate, then dies from its own sting (the countdown).
// Drives beeAiStep: acquire -> melee -> sting -> death countdown, end to end.
func TestBeeStingsThenDies(t *testing.T) {
	loop, floorY := beeLoop(t)
	loop.levelDifficulty = difficultyNormal
	loop.gametime = 1000
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	b.ai.rng = newEntityRandom(wolfAngerSeed)
	attacker := combatPlayer(loop, 61000) // capturing client (the sting applies damage to the player)
	attacker.x, attacker.y, attacker.z = b.x, b.y, b.z // overlapping -> melee range + LOS trivially

	// Provoke: give live anger + the attacker as anger target (as anger-on-hit would).
	b.angerEndTime = loop.gametime + 500
	b.angerTarget = attacker.entityID

	// First beeAiStep: acquire the target + sting (meleeCooldown starts at 0).
	loop.beeAiStep(b)
	if !b.beeHasStung {
		t.Fatalf("bee did not sting an adjacent anger target (meleeCooldown=%d, target=%d)", b.meleeCooldown, b.ai.attackTargetID)
	}
	if loop.beeIsAngry(b) {
		t.Fatal("bee still angry after a landed sting -- stopBeingAngry must clear the anger")
	}
	if b.angerEndTime != 0 || b.angerTarget != 0 || b.ai.attackTargetID != 0 {
		t.Fatalf("stopBeingAngry did not clear anger/target: end=%d target=%d attackTarget=%d", b.angerEndTime, b.angerTarget, b.ai.attackTargetID)
	}
	if attacker.activeEffects[effectPoison] == nil {
		t.Fatal("sting did not apply POISON to the victim")
	}
	if got := attacker.activeEffects[effectPoison].duration; got != beePoisonSeconds*20 {
		t.Fatalf("NORMAL sting POISON duration = %d, want %d (10s*20)", got, beePoisonSeconds*20)
	}

	// The bee no longer attacks (hasStung) and dies from the sting over the countdown.
	died := false
	for i := 0; i < 20000 && !died; i++ {
		loop.beeAiStep(b)
		if b.dead || b.health <= 0 {
			died = true
		}
	}
	if !died {
		t.Fatalf("bee never died from its sting after 20000 ticks (timeSinceSting=%d, health=%v)", b.beeTimeSinceSting, b.health)
	}
}

// TestBeeStingPoisonByDifficulty: the sting POISON duration is LIVE-difficulty-scaled -- NORMAL 10s*20,
// HARD 18s*20 (Bee.doHurtTarget: level().getDifficulty() == NORMAL -> 10, == HARD -> 18). Drive beeDoSting
// directly under each difficulty against a fresh victim.
func TestBeeStingPoisonByDifficulty(t *testing.T) {
	cases := []struct {
		diff difficulty
		want int
	}{
		{difficultyNormal, beePoisonSeconds * 20},   // 10s * 20 = 200
		{difficultyHard, beePoisonSecondsHard * 20}, // 18s * 20 = 360
	}
	for _, c := range cases {
		loop, floorY := beeLoop(t)
		loop.levelDifficulty = c.diff
		b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
		victim := combatPlayer(loop, 61000) // capturing client (the sting applies damage)
		victim.x, victim.y, victim.z = b.x, b.y, b.z
		victim.health = 20

		loop.beeDoSting(b, victim)

		eff := victim.activeEffects[effectPoison]
		if eff == nil {
			t.Fatalf("difficulty %d: sting applied no POISON", c.diff)
		}
		if eff.duration != c.want {
			t.Fatalf("difficulty %d: POISON duration %d, want %d", c.diff, eff.duration, c.want)
		}
		if !b.beeHasStung {
			t.Fatalf("difficulty %d: bee not marked hasStung after a landed sting", c.diff)
		}
	}
}

// TestBeeStingNoPoisonOnEasy: on EASY (and PEACEFUL) the sting deals damage + sets hasStung but adds NO
// POISON (p == 0 -> the ifle skip). Cite Bee.doHurtTarget (else -> p=0).
func TestBeeStingNoPoisonOnEasy(t *testing.T) {
	loop, floorY := beeLoop(t)
	loop.levelDifficulty = difficultyEasy
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	victim := combatPlayer(loop, 61000) // capturing client (the sting applies damage)
	victim.x, victim.y, victim.z = b.x, b.y, b.z
	victim.health = 20

	loop.beeDoSting(b, victim)

	if victim.activeEffects[effectPoison] != nil {
		t.Fatal("EASY sting must add NO POISON (p == 0)")
	}
	if !b.beeHasStung {
		t.Fatal("EASY sting still sets hasStung (the sting landed)")
	}
}
