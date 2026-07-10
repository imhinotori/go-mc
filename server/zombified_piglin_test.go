package server

// zombified_piglin_test.go -- deterministic pins for the ZombifiedPiglin (net.minecraft.world.entity.monster
// .zombie.ZombifiedPiglin, 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 20 /
// FOLLOW_RANGE 35 / MOVEMENT_SPEED 0.23 / ATTACK_DAMAGE 5 / ARMOR 2), NEUTRAL-until-hit (no target on sight),
// the anger-on-hit (angerEndTime = gameTime + 400 + nextInt(381), angerTarget = attacker), the ANGER-PACK
// spread to a nearby zombified piglin (alertOthers), fire immunity, and no sun-burn.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/world"
)

// zpiglinLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick zombified piglins.
func zpiglinLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestZombifiedPiglinSpawnDefaults: spawnZombifiedPiglin builds a mob rendering as entity.ZombifiedPiglin.ID
// with the folded jar attributes (MAX_HEALTH 20 -> health 20, FOLLOW_RANGE 35, MOVEMENT_SPEED 0.23,
// ATTACK_DAMAGE 5, ARMOR 2) and a minimal AI. It starts NEUTRAL (angerEndTime 0, no target).
func TestZombifiedPiglinSpawnDefaults(t *testing.T) {
	loop, _, floorY := zpiglinLoop(t)
	zp := loop.spawnZombifiedPiglin(8.5, float64(floorY+1), 8.5)
	if zp.typ != entity.ZombifiedPiglin.ID {
		t.Fatalf("zombified_piglin typ = %d, want entity.ZombifiedPiglin.ID %d", zp.typ, entity.ZombifiedPiglin.ID)
	}
	if !zp.isZombifiedPiglin {
		t.Fatal("zombified_piglin not marked isZombifiedPiglin")
	}
	if math.Abs(float64(zp.health)-20.0) > 1e-6 {
		t.Fatalf("zombified_piglin health = %v, want 20.0 (MAX_HEALTH)", zp.health)
	}
	if got := zp.getAttributeValue(attribute.FollowRange); math.Abs(got-35.0) > 1e-9 {
		t.Fatalf("zombified_piglin FOLLOW_RANGE = %v, want 35.0", got)
	}
	if got := zp.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.23000000417232513) > 1e-12 {
		t.Fatalf("zombified_piglin MOVEMENT_SPEED = %v, want 0.23000000417232513", got)
	}
	if got := zp.getAttributeValue(attribute.AttackDamage); math.Abs(got-5.0) > 1e-9 {
		t.Fatalf("zombified_piglin ATTACK_DAMAGE = %v, want 5.0", got)
	}
	if got := zp.getAttributeValue(attribute.Armor); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("zombified_piglin ARMOR = %v, want 2.0", got)
	}
	if zp.angerEndTime != 0 || zp.angerTarget != 0 {
		t.Fatalf("fresh zombified_piglin must be NEUTRAL: angerEndTime=%d angerTarget=%d", zp.angerEndTime, zp.angerTarget)
	}
	if zp.ai == nil || zp.ai.rng == nil {
		t.Fatal("zombified_piglin has no minimal AI / rng")
	}
}

// TestZombifiedPiglinNeutralUntilHit: a fresh zombified piglin standing next to a player acquires NO target
// (neutral-until-provoked) -- zombifiedPiglinAcquireAngryTarget only fires while isAngry(). The keystone.
func TestZombifiedPiglinNeutralUntilHit(t *testing.T) {
	loop, _, floorY := zpiglinLoop(t)
	loop.gametime = 1000
	zp := loop.spawnZombifiedPiglin(8.5, float64(floorY+1), 8.5)
	addTestPlayer(loop, 61000, zp.x, zp.y, zp.z+1) // ~1 block away, well within FOLLOW_RANGE

	loop.zombifiedPiglinAiStep(zp)
	if zp.ai.attackTargetID != 0 {
		t.Fatalf("a NEUTRAL zombified piglin acquired a target on sight (id=%d) -- must stay neutral until hit", zp.ai.attackTargetID)
	}
}

// TestZombifiedPiglinAngerOnHit: a zombified piglin HIT by a player becomes angry (angerEndTime = gameTime +
// 400 + nextInt(381), angerTarget = attacker), and the anger-gated acquire THEN targets the attacker.
func TestZombifiedPiglinAngerOnHit(t *testing.T) {
	loop, _, floorY := zpiglinLoop(t)
	loop.gametime = 1000
	zp := loop.spawnZombifiedPiglin(8.5, float64(floorY+1), 8.5)
	// Deterministic rng: capture the SAME nextInt(381) a reference draws (ONE draw for the anger timer).
	zp.ai.rng = newEntityRandom(wolfAngerSeed)
	wantOffset := 400 + newEntityRandom(wolfAngerSeed).nextInt(381)
	if wantOffset < 400 || wantOffset > 780 {
		t.Fatalf("precondition: anger offset %d out of [400,780]", wantOffset)
	}
	attacker := addTestPlayer(loop, 61000, zp.x, zp.y, zp.z+1)

	// THE HIT via the combat store-point (the same path a real player melee takes).
	loop.applyDamageEntity(zp, damageSourcePlayerAttack(attacker.entityID), 1.0)

	wantEnd := int64(1000) + int64(wantOffset)
	if zp.angerEndTime != wantEnd {
		t.Fatalf("angerEndTime = %d, want %d (gameTime 1000 + 400 + nextInt(381) = 1000 + %d)", zp.angerEndTime, wantEnd, wantOffset)
	}
	if zp.angerTarget != attacker.entityID {
		t.Fatalf("angerTarget = %d, want the attacker %d", zp.angerTarget, attacker.entityID)
	}
	if !isAngryAt(loop, zp, attacker.entityID) {
		t.Fatal("isAngryAt(attacker) false after the hit -- the zombified piglin is not angry")
	}
	// The angry acquire now targets the attacker.
	loop.zombifiedPiglinAcquireAngryTarget(zp)
	if zp.ai.attackTargetID != attacker.entityID {
		t.Fatalf("post-hit target = %d, want the attacker %d", zp.ai.attackTargetID, attacker.entityID)
	}
}

// TestZombifiedPiglinAngerSpread: alertOthers propagates a provoked zombified piglin's target to a nearby
// (within FOLLOW_RANGE) zombified piglin that has NO target -- the ANGER-PACK keystone. We provoke one, set
// its alert throttle to 0 + LOS true, drive maybeAlertOthers, and assert the neighbor adopts the target and
// becomes angry-at the same attacker.
func TestZombifiedPiglinAngerSpread(t *testing.T) {
	loop, _, floorY := zpiglinLoop(t)
	loop.gametime = 1000
	by := float64(floorY + 1)
	lead := loop.spawnZombifiedPiglin(8.5, by, 8.5)
	pack := loop.spawnZombifiedPiglin(10.5, by, 8.5) // 2 blocks away, within FOLLOW_RANGE 35 and ALERT_RANGE_Y 10
	attacker := addTestPlayer(loop, 61000, lead.x, by, lead.z+1)

	// Provoke the LEAD and give it the attacker as target + live anger (as anger-on-hit would).
	lead.angerEndTime = loop.gametime + 500
	lead.angerTarget = attacker.entityID
	lead.ai.attackTargetID = attacker.entityID
	// Fire the alert immediately: throttle 0 + LOS true (the player is adjacent, clear air).
	lead.zombifiedPiglinAlertCooldown = 0

	// Precondition: the pack member is neutral (no target).
	if pack.ai.attackTargetID != 0 {
		t.Fatal("precondition: the pack member already had a target")
	}

	loop.zombifiedPiglinMaybeAlertOthers(lead)

	if pack.ai.attackTargetID != attacker.entityID {
		t.Fatalf("anger did NOT spread: pack member target = %d, want the attacker %d", pack.ai.attackTargetID, attacker.entityID)
	}
	if !isAngryAt(loop, pack, attacker.entityID) {
		t.Fatal("the alerted pack member is not angry-at the attacker (setTarget must adopt the anger)")
	}
}

// TestZombifiedPiglinFireImmune: a zombified piglin is fire/lava immune (nether type registered fireImmune)
// -- entityFireImmune returns true. This is also why (inheriting Zombie.isSunSensitive true) it does NOT
// burn in daylight: the sun-burn ignite is a no-op under fire immunity.
func TestZombifiedPiglinFireImmune(t *testing.T) {
	loop, _, floorY := zpiglinLoop(t)
	zp := loop.spawnZombifiedPiglin(8.5, float64(floorY+1), 8.5)
	if !entityFireImmune(zp) {
		t.Fatal("zombified piglin is NOT fire-immune -- it must take no fire/lava damage and not sun-burn")
	}
}

// TestZombifiedPiglinSetTargetDrawOrder: ZombifiedPiglin.setTarget (bytecode 11-36) draws TWO samples
// on a fresh acquisition, IN ORDER: (1) FIRST_ANGER_SOUND_DELAY = 0 + nextInt(21) -> playFirstAngerSoundIn,
// then (2) ALERT_INTERVAL = 80 + nextInt(41) -> ticksUntilNextAlert. The port previously drew only the
// ALERT_INTERVAL sample, dropping draw (1) and desyncing every downstream RNG consumer. This replays the
// mob's exact seeded stream and asserts both fields land the expected draws in the expected order.
func TestZombifiedPiglinSetTargetDrawOrder(t *testing.T) {
	loop, _, floorY := zpiglinLoop(t)
	by := float64(floorY + 1)
	zp := loop.spawnZombifiedPiglin(8.5, by, 8.5)
	p := addTestPlayer(loop, 62000, zp.x, by, zp.z+1)

	// Replay the mob's exact (id-reseeded) stream to predict the two draws, in the vanilla order.
	replay := newEntityRandom(0)
	replay.reseed(uint64(uint32(zp.id)) ^ defaultEntityRandomSeed)
	wantFirstAnger := zombifiedPiglinFirstAngerSoundMin + replay.nextInt(zombifiedPiglinFirstAngerSoundSpan)
	wantAlert := zombifiedPiglinAlertIntervalMin + replay.nextInt(zombifiedPiglinAlertIntervalSpan)

	loop.zombifiedPiglinSetTarget(zp, p)

	if zp.zombifiedPiglinFirstAngerSound != wantFirstAnger {
		t.Fatalf("playFirstAngerSoundIn = %d, want %d (FIRST_ANGER_SOUND_DELAY = 0+nextInt(21), drawn FIRST)", zp.zombifiedPiglinFirstAngerSound, wantFirstAnger)
	}
	if zp.zombifiedPiglinAlertCooldown != wantAlert {
		t.Fatalf("ticksUntilNextAlert = %d, want %d (ALERT_INTERVAL = 80+nextInt(41), drawn AFTER the anger-sound) -- draw order/count desync", zp.zombifiedPiglinAlertCooldown, wantAlert)
	}
}
