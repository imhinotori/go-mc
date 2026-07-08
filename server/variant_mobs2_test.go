package server

// variant_mobs2_test.go -- deterministic pins for the four MOB ports (CaveSpider/PiglinBrute/Illusioner/
// PolarBear/Giant): the spawn attributes, the INERT giant (zero goals), the cave-spider poison-on-hit, the
// piglin-brute always-hostile melee 7.0, and the illusioner blindness+invisibility cast. Cite
// CaveSpider.createCaveSpider/doHurtTarget + PiglinBrute.createAttributes + Illusioner.createAttributes/
// IllusionerBlindnessSpellGoal + PolarBear.createAttributes + Giant.createAttributes/registerGoals.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// variant2Loop builds a physics loop with a one-chunk stone floor + the full boot-loaded mob registry
// (newPhysicsLoop installs loadVanillaMobRegistry), ready to spawnVanillaMob the declared MOB mobs.
func variant2Loop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestGiantInert: the Giant spawns as entity.Giant.ID with health 100 and ZERO goals (Giant.registerGoals
// adds nothing -- it is inert). This is the critical assertion: a giant just stands.
func TestGiantInert(t *testing.T) {
	loop, floorY := variant2Loop(t)
	g := loop.spawnVanillaMob(vanillaGiantMobName, 8.5, float64(floorY+1), 8.5)
	if g == nil {
		t.Fatal("spawnVanillaMob(giant) returned nil")
	}
	if g.typ != entity.Giant.ID {
		t.Fatalf("giant typ = %d, want entity.Giant.ID %d", g.typ, entity.Giant.ID)
	}
	if math.Abs(float64(g.health)-100.0) > 1e-6 {
		t.Fatalf("giant health = %v, want 100.0 (MAX_HEALTH)", g.health)
	}
	if got := g.getAttributeValue(attribute.MaxHealth); math.Abs(got-100.0) > 1e-9 {
		t.Fatalf("giant MAX_HEALTH = %v, want 100.0", got)
	}
	if got := g.getAttributeValue(attribute.AttackDamage); math.Abs(got-50.0) > 1e-9 {
		t.Fatalf("giant ATTACK_DAMAGE = %v, want 50.0", got)
	}
	if got := g.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("giant MOVEMENT_SPEED = %v, want 0.5", got)
	}
	if g.ai == nil {
		t.Fatal("giant has no AI struct")
	}
	// THE inert assertion: zero goalSelector goals AND zero targetSelector goals.
	if n := len(g.ai.goals.goals); n != 0 {
		t.Fatalf("giant has %d goalSelector goals, want 0 (INERT -- Giant registers no goals)", n)
	}
	if n := len(g.ai.targetSelector.goals); n != 0 {
		t.Fatalf("giant has %d targetSelector goals, want 0 (INERT)", n)
	}
}

// TestCaveSpiderSpawnDefaults: the CaveSpider spawns as entity.CaveSpider.ID with health 12 (the MAX_HEALTH
// override) and the full spider goal set (>0 goalSelector goals).
func TestCaveSpiderSpawnDefaults(t *testing.T) {
	loop, floorY := variant2Loop(t)
	cs := loop.spawnVanillaMob(vanillaCaveSpiderMobName, 8.5, float64(floorY+1), 8.5)
	if cs == nil {
		t.Fatal("spawnVanillaMob(cave_spider) returned nil")
	}
	if cs.typ != entity.CaveSpider.ID {
		t.Fatalf("cave_spider typ = %d, want entity.CaveSpider.ID %d", cs.typ, entity.CaveSpider.ID)
	}
	if math.Abs(float64(cs.health)-12.0) > 1e-6 {
		t.Fatalf("cave_spider health = %v, want 12.0 (CaveSpider MAX_HEALTH override)", cs.health)
	}
	if got := cs.getAttributeValue(attribute.MaxHealth); math.Abs(got-12.0) > 1e-9 {
		t.Fatalf("cave_spider MAX_HEALTH = %v, want 12.0", got)
	}
	if got := cs.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.3) > 1e-9 {
		t.Fatalf("cave_spider MOVEMENT_SPEED = %v, want 0.3 (inherited Spider)", got)
	}
	if cs.ai == nil || len(cs.ai.goals.goals) == 0 {
		t.Fatal("cave_spider has no goalSelector goals (should inherit the spider goal set)")
	}
}

// TestPolarBearSpawnDefaults: the PolarBear spawns as entity.PolarBear.ID with health 30, ATTACK_DAMAGE 6,
// FOLLOW_RANGE 20, and a NEUTRAL goal set (float/melee/stroll/look/around + hurt_by_target only).
func TestPolarBearSpawnDefaults(t *testing.T) {
	loop, floorY := variant2Loop(t)
	pb := loop.spawnVanillaMob(vanillaPolarBearMobName, 8.5, float64(floorY+1), 8.5)
	if pb == nil {
		t.Fatal("spawnVanillaMob(polar_bear) returned nil")
	}
	if pb.typ != entity.PolarBear.ID {
		t.Fatalf("polar_bear typ = %d, want entity.PolarBear.ID %d", pb.typ, entity.PolarBear.ID)
	}
	if math.Abs(float64(pb.health)-30.0) > 1e-6 {
		t.Fatalf("polar_bear health = %v, want 30.0", pb.health)
	}
	if got := pb.getAttributeValue(attribute.AttackDamage); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("polar_bear ATTACK_DAMAGE = %v, want 6.0", got)
	}
	if got := pb.getAttributeValue(attribute.FollowRange); math.Abs(got-20.0) > 1e-9 {
		t.Fatalf("polar_bear FOLLOW_RANGE = %v, want 20.0", got)
	}
	if got := pb.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.25) > 1e-9 {
		t.Fatalf("polar_bear MOVEMENT_SPEED = %v, want 0.25", got)
	}
	if pb.ai == nil || len(pb.ai.goals.goals) == 0 {
		t.Fatal("polar_bear has no goalSelector goals")
	}
	// NEUTRAL: exactly one targetSelector goal (hurt_by_target only -- no un-provoked nearest_attackable).
	if n := len(pb.ai.targetSelector.goals); n != 1 {
		t.Fatalf("polar_bear has %d targetSelector goals, want 1 (hurt_by_target only -- NEUTRAL)", n)
	}
}

// TestIllusionerSpawnDefaults: the Illusioner spawns as entity.Illusioner.ID with health 32 and FOLLOW_RANGE 18.
func TestIllusionerSpawnDefaults(t *testing.T) {
	loop, floorY := variant2Loop(t)
	il := loop.spawnVanillaMob(vanillaIllusionerMobName, 8.5, float64(floorY+1), 8.5)
	if il == nil {
		t.Fatal("spawnVanillaMob(illusioner) returned nil")
	}
	if il.typ != entity.Illusioner.ID {
		t.Fatalf("illusioner typ = %d, want entity.Illusioner.ID %d", il.typ, entity.Illusioner.ID)
	}
	if math.Abs(float64(il.health)-32.0) > 1e-6 {
		t.Fatalf("illusioner health = %v, want 32.0", il.health)
	}
	if got := il.getAttributeValue(attribute.MaxHealth); math.Abs(got-32.0) > 1e-9 {
		t.Fatalf("illusioner MAX_HEALTH = %v, want 32.0", got)
	}
	if got := il.getAttributeValue(attribute.FollowRange); math.Abs(got-18.0) > 1e-9 {
		t.Fatalf("illusioner FOLLOW_RANGE = %v, want 18.0", got)
	}
	if got := il.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("illusioner MOVEMENT_SPEED = %v, want 0.5", got)
	}
	if il.ai == nil || len(il.ai.goals.goals) == 0 {
		t.Fatal("illusioner has no goalSelector goals")
	}
}

// TestPiglinBruteSpawnDefaults: spawnPiglinBrute builds a PiglinBrute with health 50, ATTACK_DAMAGE 7,
// MOVEMENT_SPEED 0.35, FOLLOW_RANGE 12, and a per-entity rng.
func TestPiglinBruteSpawnDefaults(t *testing.T) {
	loop, floorY := variant2Loop(t)
	pb := loop.spawnPiglinBrute(8.5, float64(floorY+1), 8.5)
	if pb == nil {
		t.Fatal("spawnPiglinBrute returned nil")
	}
	if pb.typ != entity.PiglinBrute.ID {
		t.Fatalf("piglin_brute typ = %d, want entity.PiglinBrute.ID %d", pb.typ, entity.PiglinBrute.ID)
	}
	if math.Abs(float64(pb.health)-50.0) > 1e-6 {
		t.Fatalf("piglin_brute health = %v, want 50.0 (MAX_HEALTH)", pb.health)
	}
	if got := pb.getAttributeValue(attribute.MaxHealth); math.Abs(got-50.0) > 1e-9 {
		t.Fatalf("piglin_brute MAX_HEALTH = %v, want 50.0", got)
	}
	if got := pb.getAttributeValue(attribute.AttackDamage); math.Abs(got-7.0) > 1e-9 {
		t.Fatalf("piglin_brute ATTACK_DAMAGE = %v, want 7.0", got)
	}
	if got := pb.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.3499999940395355) > 1e-12 {
		t.Fatalf("piglin_brute MOVEMENT_SPEED = %v, want 0.3499999940395355", got)
	}
	if got := pb.getAttributeValue(attribute.FollowRange); math.Abs(got-12.0) > 1e-9 {
		t.Fatalf("piglin_brute FOLLOW_RANGE = %v, want 12.0", got)
	}
	if pb.ai == nil || pb.ai.rng == nil {
		t.Fatal("piglin_brute has no minimal AI / rng")
	}
}

// TestPiglinBruteAlwaysHostileMelee: an adult piglin brute adjacent to a player ACQUIRES it (always
// hostile -- no gold-armor neutrality) and deals ATTACK_DAMAGE (7.0) on the melee swing.
func TestPiglinBruteAlwaysHostileMelee(t *testing.T) {
	loop, floorY := variant2Loop(t)
	py := float64(floorY + 1)
	victim := combatTestPlayer(loop, 8.9, py, 8.5, 7110) // ~0.4 away -> within melee range (d < 4.0)
	victim.health = 20.0
	pb := loop.spawnPiglinBrute(8.5, py, 8.5)

	loop.withRegion(loop.regions[globalRegion], func() {
		loop.piglinBruteAcquireNearestPlayer(pb)
	})
	if pb.ai.attackTargetID != victim.entityID {
		t.Fatalf("piglin brute did not acquire the bare player (target=%d, want %d -- always hostile)", pb.ai.attackTargetID, victim.entityID)
	}
	pb.piglinAttackTime = 0 // swing ready
	loop.withRegion(loop.regions[globalRegion], func() {
		loop.piglinBruteMeleeGoalTick(pb)
	})
	dealt := 20.0 - float64(victim.health)
	if math.Abs(dealt-7.0) > 1e-6 {
		t.Fatalf("piglin brute melee dealt %v damage, want 7.0 (ATTACK_DAMAGE)", dealt)
	}
}

// TestCaveSpiderPoisonOnHit: caveSpiderApplyPoison applies POISON i*20 / 0 to the victim (NORMAL -> i=7 ->
// 140 ticks). Mirrors the husk hunger test (direct signature call).
func TestCaveSpiderPoisonOnHit(t *testing.T) {
	loop, floorY := variant2Loop(t)
	py := float64(floorY + 1)
	victim := combatTestPlayer(loop, 8.9, py, 8.5, 7120)
	victim.health = 20.0
	cs := loop.spawnVanillaMob(vanillaCaveSpiderMobName, 8.5, py, 8.5)

	loop.withRegion(loop.regions[globalRegion], func() {
		loop.caveSpiderApplyPoison(cs, victim)
	})
	e := victim.activeEffects[effectPoison]
	if e == nil {
		t.Fatal("cave spider hit did not apply POISON")
	}
	// serverDifficulty is NORMAL -> i=7 -> 140 ticks, amplifier 0.
	if e.duration != caveSpiderPoisonSecondsNormal*20 || e.amplifier != caveSpiderPoisonAmplifier {
		t.Fatalf("POISON = dur %d amp %d, want dur %d amp 0", e.duration, e.amplifier, caveSpiderPoisonSecondsNormal*20)
	}
}

// TestIllusionerBlindnessCast: an illusioner with a target in FOLLOW_RANGE, once the cooldown elapses,
// casts BLINDNESS 400/0 on the target AND turns itself invisible (INVISIBILITY effect for the warmup).
func TestIllusionerBlindnessCast(t *testing.T) {
	loop, floorY := variant2Loop(t)
	py := float64(floorY + 1)
	victim := combatTestPlayer(loop, 12.5, py, 8.5, 7130) // ~4 blocks away, well within FOLLOW_RANGE 18
	victim.health = 20.0
	il := loop.spawnVanillaMob(vanillaIllusionerMobName, 8.5, py, 8.5)
	il.ai.attackTargetID = victim.entityID
	il.illusionerBlindnessCooldown = 0 // ready to cast

	loop.withRegion(loop.regions[globalRegion], func() {
		loop.illusionerAiStep(il)
	})
	b := victim.activeEffects[effectBlindness]
	if b == nil {
		t.Fatal("illusioner did not apply BLINDNESS to its target")
	}
	if b.duration != illusionerBlindnessDuration || b.amplifier != illusionerBlindnessAmplifier {
		t.Fatalf("BLINDNESS = dur %d amp %d, want dur %d amp 0", b.duration, b.amplifier, illusionerBlindnessDuration)
	}
	// The illusioner is invisible while casting (INVISIBILITY on itself).
	if !entityHasEffect(il, effectInvisibility) {
		t.Fatal("illusioner is not invisible while casting (Illusioner.aiStep setInvisible)")
	}
	// The cast reset the cooldown to the casting interval.
	if il.illusionerBlindnessCooldown != illusionerBlindnessInterval {
		t.Fatalf("illusioner cooldown = %d after cast, want %d (getCastingInterval)", il.illusionerBlindnessCooldown, illusionerBlindnessInterval)
	}
}
