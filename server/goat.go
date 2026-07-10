// goat.go -- the Goat (net.minecraft.world.entity.animal.goat.Goat), a 1:1 port from the unobfuscated
// 26.2 jar. Goat is a mountain Animal that RAMS its target (the LongJumpToRandomPos / RamTarget brain
// behavior: lower head, charge, knockback, and drop a goat horn), jumps unusually high (the goat
// long-jump), and comes in a rare SCREAMING variant (louder, rams more often). Vanilla drives it with a
// BRAIN; for a bounded port this lands the attributes + spawn + the "visibly alive" passive goal subset
// (Float/Tempt/Breed/Follow/Stroll/Look) + the SCREAMING-variant spawn roll. The RAM (LongJump + charge +
// horn drop) and the high goat-jump are the DEFERRED behavior layer (they need the brain LongJump
// machinery + the horn item/drop table). Code-spawned (spawnGoat) with a *mobAI carrying the passive
// goals; its per-tick extra is goatAiStep from tickAI (per-type-gated on typ == entity.Goat.ID).
//
// VANILLA (verified javap Goat this session):
//   createAttributes: Animal.createAnimalAttributes + MAX_HEALTH 10.0 + MOVEMENT_SPEED 0.20000000298023224
//     + ATTACK_DAMAGE 2.0 (ONE MOVEMENT_SPEED add -- the ram/screaming speed is the brain LongJump machinery).
//   Goat is a BRAIN mob (makeBrain / customServerAiStep -> GoatAi): the goal set is the brain behaviors
//     (Swim, LookAtTargetSink, MoveToTargetSink, AnimalPanic, FollowTemptation(WHEAT), BabyFollowAdult,
//     LongJumpToRandomPos, PrepareRamNearestTarget, RamTarget, RandomStroll, ...). GOAT_SCREAMING_CHANCE 0.02.
//   finalizeSpawn: GoatAi.initMemories; setScreamingGoat(random.nextDouble() < 0.02); ageBoundaryReached();
//     if(!isBaby() && random.nextFloat() < 0.1) removeOneHorn (nextBoolean picks left/right).
//   isScreamingGoat(): DATA_IS_SCREAMING_GOAT (default false); a screaming goat uses the louder ambient/hurt
//     sounds + rams more frequently (RamTarget cooldown shorter).
//
// v1 STUBS (cited): the BRAIN (GoatAi sensors + activities: LongJumpToRandomPos, PrepareRamNearestTarget,
// RamTarget, FollowTemptation, BabyFollowAdult) is the DEFERRED behavior layer -- this port supplies the
// bounded passive goal walk (Float/Tempt(GOAT_FOOD)/Breed/Follow/Stroll/Look) + the faithful SCREAMING
// spawn roll (nextDouble() < 0.02) + the one-horn removal roll (nextFloat()<0.1 then nextBoolean).
// The RAM (lower-head charge + knockback + goat-horn drop) and the high goat-jump are DEFERRED. The
// observable attributes, the passive goal walk, the screaming-variant flag, and the horn state are EXACT.

package server

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
)

// Goat constants (VERIFIED javap Goat this session).
const (
	goatMaxHealth       = 10.0                // createAttributes MAX_HEALTH 10.0
	goatMovementSpeed   = 0.20000000298023224 // createAttributes MOVEMENT_SPEED (float-widened)
	goatTemptSpeed      = 1.25                // FollowTemptation speed (brain-deferred; the common animal tempt pace)
	goatFollowSpeed     = 1.25                // BabyFollowAdult speed (brain-deferred; the common follow pace)
	goatBreedSpeed      = 1.0                 // AnimalMakeLove/breed speed (brain-deferred; the common breed pace)
	goatStrollSpeed     = 1.0                 // RandomStroll speed (brain-deferred; the common stroll pace)
	goatLookDistance    = 8.0                 // LookAtTargetSink distance (the common animal look range)
	goatScreamingChance = 0.02                // GOAT_SCREAMING_CHANCE (ldc2_w 0.02d): nextDouble() < 0.02 -> screaming
	goatFoodTag         = "goat_food"         // GOAT_FOOD (WHEAT): the tempt predicate
	goatUnihornChance   = 0.10000000149011612 // UNIHORN_CHANCE (ldc 0.1f, f2d-widened): finalizeSpawn removeOneHorn roll
	// GoatAi long-jump + ram-sensor brain constants (VERIFIED javap GoatAi + LongJumpToRandomPos +
	// PrepareRamNearestTarget + RamTarget + LongJumpUtil this session -- ConstantValue attributes +
	// UniformInt.of(min,max) statics).
	goatMaxJumpVelocityMult = 3.5714288         // MAX_JUMP_VELOCITY_MULTIPLIER (ConstantValue float 3.5714288f)
	goatLongJumpPrepareTime = 40                // LongJumpToRandomPos PREPARE_JUMP_DURATION (ConstantValue int 40)
	goatRamPrepareTime      = 20                // RAM_PREPARE_TIME (ConstantValue int 20)
	goatRamMaxDistance      = 7                 // RAM_MAX_DISTANCE (ConstantValue int 7)
	goatJumpScaleFactor     = 0.949999988079071 // calculateJumpVectorForAngle final .scale(0.95f) (ldc2_w)
)

// goatAllowedJumpAngles is LongJumpToRandomPos.ALLOWED_ANGLES = [65, 70, 75, 80] (javap <clinit>: the
// four bipush'd Integers). calculateOptimalJumpVector shuffles a copy then returns the first angle that
// yields a valid ballistic solution. Cite LongJumpToRandomPos.ALLOWED_ANGLES.
var goatAllowedJumpAngles = [4]int{65, 70, 75, 80}

// goatBrainState groups the TRANSIENT Goat GoatAi brain phase state behind ONE Entity pointer (nil for
// non-goats). The two cooldown MEMORIES live on the Entity itself: RAM_COOLDOWN_TICKS is goatRamCooldownTicks
// (the flat field goatAiStep already counts down + goatFinishRam reseeds), LONG_JUMP_COOLDOWN_TICKS is
// longJumpCooldown here. This struct carries only the per-activity phase (ram prepare/charge, long-jump
// prepare/mid-jump). Cite GoatAi.initMemories + the LongJump/Ram activities.
type goatBrainState struct {
	longJumpCooldown int32 // MemoryModuleType.LONG_JUMP_COOLDOWN_TICKS (CountDownCooldownTicks decrements it)
	// Ram phase (PrepareRamNearestTarget -> RamTarget). ramTargetID != 0 while a ram is armed/charging.
	ramTargetID int32   // the selected victim (PrepareRamNearestTarget candidate / RamTarget target)
	ramPrepare  int32   // ticks remaining before the charge arms (RAM_PREPARE_TIME countdown)
	ramCharging bool    // RamTarget active (charging toward the victim, dealing the hit on contact)
	ramDirX     float64 // RamTarget.ramDirection.x (normalized start->target horizontal)
	ramDirZ     float64 // RamTarget.ramDirection.z
	// Long-jump phase (LongJumpToRandomPos). ljChosenValid gates the prepare->launch transition.
	ljPrepare        int32   // PREPARE_JUMP_DURATION countdown before the launch impulse
	ljChosenValid    bool    // a valid chosenJump velocity was solved (pickCandidate succeeded)
	ljVX, ljVY, ljVZ float64 // the solved chosenJump velocity vector (calculateOptimalJumpVector)
}

// newGoatAI builds the Goat bounded passive AI. Goat is a BRAIN mob in vanilla (the RAM/long-jump/tempt
// behaviors live in GoatAi, DEFERRED per the file header); this supplies the "visibly alive" classic-goal
// stand-in (Float/Panic/Breed/Tempt/Follow/Stroll/Look), mirroring newPigAI shape (per-mob rng, navigation
// seed, canFloat, Animal pathfinding malus). The priorities follow the pig passive template (a faithful
// bounded ordering for the brain-deferred goat). Cite Goat.makeBrain (the brain deferral note).
func newGoatAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * goatMovementSpeed // seed with MOVEMENT_SPEED (0.2)
	m.navigation.canFloat = true                            // Swim brain behavior: navigation may float
	applyAnimalPathfindingMalus(m)                          // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	m.goals.addGoal(3, newBreedGoal(goatBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(goatTemptSpeed, func(id int32) bool { return itemInTag(id, goatFoodTag) }, false, nil))
	m.goals.addGoal(5, newFollowParentGoal(goatFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(goatStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(goatLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnGoat creates a Goat at (x,y,z) with the jar attributes and the passive goal AI, then adds it to the
// owner region store. It ports the Goat.finalizeSpawn SCREAMING roll: setScreamingGoat(random.nextDouble()
// < 0.02) on the goat OWN per-entity rng (drawn BEFORE reseedMobAI's own seed would matter -- we roll on a
// fresh spawn-seeded stream). baby toggles the AgeableMob baby age + half-scale box. The one-horn removal
// roll + ageBoundaryReached horn state are DEFERRED. initSpawnHealth seeds health from MAX_HEALTH (10.0).
// Cite Goat.createAttributes + Goat.finalizeSpawn (GOAT_SCREAMING_CHANCE 0.02).
func (t *TickLoop) spawnGoat(x, y, z float64, baby bool) *Entity {
	g := NewEntity(t.idAlloc.AllocID(), entity.Goat, x, y, z)
	g.isGoat = true
	if baby {
		g.breedAge = babyStartAge
		g.refreshDimensions()
	}
	initSpawnHealth(g) // setHealth(getMaxHealth()) -> 10.0
	g.ai = newGoatAI()
	reseedMobAI(g.ai, g.id)
	// Goat static defaults: DATA_HAS_LEFT_HORN / DATA_HAS_RIGHT_HORN default TRUE (a goat spawns with
	// both horns; finalizeSpawn may remove one below).
	g.goatHasLeftHorn = true
	g.goatHasRightHorn = true
	// finalizeSpawn draw order (bytecode 14-94), on the goat's own (id-reseeded) stream, matching the
	// codebase per-entity-rng convention (screaming distribution is identical to level.getRandom):
	//   (1) setScreamingGoat(random.nextDouble() < GOAT_SCREAMING_CHANCE 0.02).
	g.goatScreaming = mobRandom(g).nextDouble() < goatScreamingChance
	// defineSynchedData: DATA_HAS_LEFT_HORN / DATA_HAS_RIGHT_HORN default TRUE (a fresh goat has both horns).
	g.goatHasLeftHorn = true
	g.goatHasRightHorn = true
	// ageBoundaryReached(): ATTACK_DAMAGE base 2.0 adult / 1.0 baby + the baby-scale dims (setGoatAgeAttack).
	setGoatAgeAttack(g)
	// GoatAi.initMemories(this, level.getRandom()): seed LONG_JUMP_COOLDOWN_TICKS = TIME_BETWEEN_LONG_JUMPS
	// .sample(rng) FIRST, then RAM_COOLDOWN_TICKS = getTimeBetweenRams(goat).sample(rng), IN THAT ORDER, both
	// on the goat's own stream (finalizeSpawn order: after the screaming roll). sample == min+nextInt(span+1).
	// Cite GoatAi.initMemories + UniformInt.sample.
	g.goatBrain = &goatBrainState{}
	g.goatBrain.longJumpCooldown = int32(goatTimeBetweenLongJumpsMin + mobRandom(g).nextInt(goatTimeBetweenLongJumpsMax-goatTimeBetweenLongJumpsMin+1))
	g.goatRamCooldownTicks = int32(goatRamCooldownSample(g))
	// finalizeSpawn tail: if(!isBaby() && random.nextFloat() < 0.1) removeOneHorn (nextBoolean picks L/R).
	// UNIHORN_CHANCE 0.1. The draw ORDER (nextFloat gate, then nextBoolean side) is the faithful contract.
	if !g.isBaby() && float64(mobRandom(g).nextFloat()) < goatUnihornChance {
		if mobRandom(g).nextBoolean() {
			g.goatHasLeftHorn = false // removeOneHorn: nextBoolean() -> DATA_HAS_LEFT_HORN := false
		} else {
			g.goatHasRightHorn = false // else DATA_HAS_RIGHT_HORN := false
		}
	}
	owner := t.regionForEntity(g)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(g)
	return g
}

// goatAiStep is the Goat per-tick extra (Goat.customServerAiStep -> GoatAi brain). It ticks the two
// cooldowns (LONG_JUMP_COOLDOWN_TICKS / RAM_COOLDOWN_TICKS) and drives the RAM (PrepareRamNearestTarget
// -> RamTarget) and LONG_JUMP (LongJumpToRandomPos) activities. Per-type-gated on e.goatBrain != nil
// (nil for every non-goat, so the pig oracle is untouched). RNG on the goat OWN stream. RAM_COOLDOWN_TICKS
// is the flat goatRamCooldownTicks field; LONG_JUMP_COOLDOWN_TICKS lives on goatBrain. Cite
// Goat.customServerAiStep + GoatAi.getActivities.
func (t *TickLoop) goatAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	if e.goatBrain == nil {
		return
	}
	b := e.goatBrain
	// CORE activity: CountDownCooldownTicks ticks BOTH cooldowns down every tick (GoatAi initCoreActivity
	// registers CountDownCooldownTicks(LONG_JUMP_COOLDOWN_TICKS) + (RAM_COOLDOWN_TICKS)).
	if b.longJumpCooldown > 0 {
		b.longJumpCooldown--
	}
	if e.goatRamCooldownTicks > 0 {
		e.goatRamCooldownTicks--
	}
	// RAM activity (PrepareRamNearestTarget -> RamTarget) runs while a ram is armed/charging; else the
	// LONG_JUMP activity may start. The activities are mutually exclusive; RAM precedes LONG_JUMP in
	// GoatAi.getActivities. Cite GoatAi.getActivities.
	if b.ramCharging {
		t.goatRamCharge(e)
		return
	}
	if b.ramTargetID != 0 {
		t.goatPrepareRam(e)
		return
	}
	if b.ljChosenValid || b.ljPrepare > 0 {
		t.goatLongJumpTick(e)
		return
	}
	if e.goatRamCooldownTicks == 0 && t.goatTryStartRam(e) {
		return
	}
	if b.longJumpCooldown == 0 {
		t.goatTryStartLongJump(e)
	}
}

// ---------------------------------------------------------------------------------------------------
// RAM behavior + horn drop + fall/milk/age fixes (RamTarget / GoatAi / Goat.dropHorn / Goat.createHorn /
// Goat.mobInteract / Goat.calculateFallDamage / Goat.ageBoundaryReached), 1:1 from the 26.2 jar (javap
// this session). The goat is a BRAIN mob in vanilla; this codebase drives it with the bounded passive
// goal walk (newGoatAI). The RAM is expressed as a directly-callable port of the RamTarget.tick numeric
// pipeline (hurtServer(noAggroMobAttack, ATTACK_DAMAGE); the speed/effect knockback-force term;
// knockback(force*dir, ...); finishRam), plus the finishRam RAM_COOLDOWN reseed on the goat's own memory
// (goatRamCooldownTicks) and the dropHorn-on-a-#snaps_goat_horn-block. It is wired into goatAiStep so the
// brain LongJump/PrepareRam sensors can drive it the moment they land; today goatAiStep counts the ram
// cooldown down (CountDownCooldownTicks) so the memory stays live.

// Goat ram/jump constants (VERIFIED javap GoatAi static <clinit> + RamTarget + Goat this session).
const (
	// GoatAi.TIME_BETWEEN_RAMS = UniformInt.of(600, 6000); TIME_BETWEEN_RAMS_SCREAMER = UniformInt.of(100, 300).
	goatTimeBetweenRamsMin         = 600
	goatTimeBetweenRamsMax         = 6000
	goatTimeBetweenRamsScreamerMin = 100
	goatTimeBetweenRamsScreamerMax = 300
	// GoatAi.TIME_BETWEEN_LONG_JUMPS = UniformInt.of(600, 1200); MAX_LONG_JUMP_HEIGHT 5, MAX_LONG_JUMP_WIDTH 5.
	goatTimeBetweenLongJumpsMin = 600
	goatTimeBetweenLongJumpsMax = 1200
	goatMaxLongJumpHeight       = 5
	goatMaxLongJumpWidth        = 5
	// GoatAi ram lambdas: ADULT ram knockback force 2.5d, BABY 1.0d (lambda$initRamActivity$1: isBaby ? 1.0 : 2.5).
	goatAdultRamKnockbackForce = 2.5
	goatBabyRamKnockbackForce  = 1.0
	// RamTarget.tick force term: getSpeed()*1.65f clamped to [0.2, 3.0], plus 0.25*(speedAmp - slowAmp).
	goatRamSpeedFactor  = 1.65 // ldc 1.65f
	goatRamSpeedClampLo = 0.2  // ldc 0.2f
	goatRamSpeedClampHi = 3.0  // ldc 3.0f
	goatRamEffectFactor = 0.25 // ldc 0.25f
	// Goat.ageBoundaryReached: ATTACK_DAMAGE base = isBaby ? 1.0 : 2.0 (dconst_1 / ldc2_w 2.0d).
	goatAdultAttackDamage = 2.0
	goatBabyAttackDamage  = 1.0
	// Goat.GOAT_FALL_DAMAGE_REDUCTION = 10 (Goat.calculateFallDamage = super - 10).
	goatFallDamageReduction = 10
	// BlockTags.SNAPS_GOAT_HORN -- the ram-into block set that snaps a horn off (hasRammedHornBreakingBlock).
	goatSnapsGoatHornTag = "snaps_goat_horn"
	// Goat.dropHorn toss velocity bands: Mth.randomBetween(random, -0.2, 0.2) x / z; (0.3, 0.7) y.
	goatHornTossXZLo = -0.2 // ldc -0.2f
	goatHornTossXZHi = 0.2  // ldc 0.2f
	goatHornTossYLo  = 0.3  // ldc 0.3f
	goatHornTossYHi  = 0.7  // ldc 0.7f
)

// goatRamCooldownSample ports GoatAi's RamTarget getTimeBetweenRams lambda (lambda$initRamActivity$0):
// a SCREAMING goat samples TIME_BETWEEN_RAMS_SCREAMER (100..300), else TIME_BETWEEN_RAMS (600..6000).
// UniformInt.sample == minInclusive + nextInt(maxInclusive - minInclusive + 1). Drawn on the goat's OWN
// per-entity stream (the finishRam ServerLevel.getRandom draw is re-homed to the goat's stream here, off
// the pig oracle -- goat != pig). Cite GoatAi.TIME_BETWEEN_RAMS(_SCREAMER) + RamTarget.finishRam.
func goatRamCooldownSample(g *Entity) int {
	min, max := goatTimeBetweenRamsMin, goatTimeBetweenRamsMax
	if g.goatScreaming {
		min, max = goatTimeBetweenRamsScreamerMin, goatTimeBetweenRamsScreamerMax
	}
	return min + mobRandom(g).nextInt(max-min+1)
}

// goatRamKnockbackForce ports GoatAi.lambda$initRamActivity$1 (the RamTarget getKnockbackForce): a BABY
// goat rams with 1.0, an ADULT with 2.5. NO RNG. Cite GoatAi lambda$initRamActivity$1.
func goatRamKnockbackForce(g *Entity) float64 {
	if g.isBaby() {
		return goatBabyRamKnockbackForce
	}
	return goatAdultRamKnockbackForce
}

// goatRam ports the DAMAGE + KNOCKBACK pipeline of RamTarget.tick(ServerLevel, Goat, long) for a resolved
// victim, 1:1 from the jar. The brain's getNearbyEntities target scan + the ramDirection setup (start())
// are the DEFERRED sensor layer; this is the exact numeric core once a victim is chosen:
//
//	DamageSource src = damageSources().noAggroMobAttack(goat);
//	float f = (float) goat.getAttributeValue(ATTACK_DAMAGE);
//	target.hurtServer(level, src, f);   // + doPostAttackEffects (enchant, deferred)
//	int speedAmp = hasEffect(SPEED) ? getEffect(SPEED).getAmplifier()+1 : 0;
//	int slowAmp  = hasEffect(SLOWNESS) ? getEffect(SLOWNESS).getAmplifier()+1 : 0;
//	float effF = 0.25f * (speedAmp - slowAmp);
//	float force = Mth.clamp(goat.getSpeed()*1.65f, 0.2f, 3.0f) + effF;
//	float blockF = applyItemBlocking(...) > 0 ? 0.5f : 1.0f;   // shield halves (deferred -> 1.0)
//	target.knockback(blockF*force*getKnockbackForce(goat), ramDir.x, ramDir.z, mobAttack(goat), f);
//	finishRam(level, goat);
//
// speedAmp/slowAmp are 0 (the goat effect subsystem is a cited default -- no SPEED/SLOWNESS on a wild
// goat), so effF == 0. applyItemBlocking is deferred (no shield subsystem) -> blockF == 1.0 (a live
// victim without a raised shield, the vanilla common case). goat.getSpeed() is the runtime move speed;
// with no active navigation want it is 0 at the instant of contact, so the clamp yields the 0.2 floor --
// the same value the brain produces when the ram just barely lands. The knockback direction is the ram
// heading (goat -> victim), normalized; knockbackEntity(power, xd, zd) is the LivingEntity.knockback
// port. Cite RamTarget.tick + GoatAi getKnockbackForce.
func (t *TickLoop) goatRam(g, victim *Entity) {
	if g == nil || victim == nil || victim.dead || victim.health <= 0 {
		return
	}
	// float f = (float) goat.getAttributeValue(ATTACK_DAMAGE): the ram's raw damage.
	f := float32(g.getAttributeValue(attribute.AttackDamage))
	// target.hurtServer(level, noAggroMobAttack(goat), f). noAggroMobAttack == mobAttack minus the aggro
	// side effect (no numeric difference); damageSourceMobAttack is the faithful MOB_ATTACK source.
	t.applyDamageEntity(victim, damageSourceMobAttack(g.id), f)
	// speedAmp - slowAmp: 0 (no goat effect subsystem -> the cited vanilla-default 0). effF == 0.
	effF := float32(goatRamEffectFactor) * 0.0
	// force = Mth.clamp(getSpeed()*1.65f, 0.2f, 3.0f) + effF. getSpeed() is 0 at contact (no want) -> floor 0.2.
	force := mthClampF(float32(goatGetSpeed(g))*float32(goatRamSpeedFactor), float32(goatRamSpeedClampLo), float32(goatRamSpeedClampHi)) + effF
	// blockF: applyItemBlocking deferred (no shield) -> 1.0 (the un-shielded victim, the common case).
	blockF := float32(1.0)
	// ramDirection = normalize(goatStartBlockPos - ramTargetPos): the charge heading, target -> goat.
	// RamTarget passes it straight to LivingEntity.knockback(power, ramDir.x, ramDir.z), which SUBTRACTS
	// the impulse (vx = dm.x/2 - kv.x) -- so a (goat - victim) direction fed to knockbackEntity pushes the
	// victim in the goat -> victim (charge) direction, exactly as the jar knocks the rammed entity FORWARD.
	xd := g.x - victim.x
	zd := g.z - victim.z
	length := math.Sqrt(xd*xd + zd*zd)
	dirX, dirZ := 0.0, 0.0
	if length > 0 {
		dirX = xd / length
		dirZ = zd / length
	}
	power := float64(blockF*force) * goatRamKnockbackForce(g)
	// knockbackEntity normalizes (dirX,dirZ) again (a unit vector is idempotent) and applies the recoil.
	t.knockbackEntity(victim, power, dirX, dirZ)
	// finishRam(level, goat): broadcast event 59 (head-lower reset) + reseed the RAM_COOLDOWN + erase target.
	t.goatFinishRam(g)
}

// goatGetSpeed ports LivingEntity.getSpeed() for the ram-force term: the cached move speed the walk
// animation reads. With no active navigation want the goat is standing at the instant of contact, so the
// speed is 0 (the Mth.clamp floor 0.2 then dominates the ram force). A live navigation-speed cache is the
// exact-parity refinement once the goat brain LongJump/PrepareRam machinery drives a real approach speed.
// Cite LivingEntity.getSpeed (the ram force reads it via RamTarget.tick).
func goatGetSpeed(g *Entity) float64 {
	if g.ai != nil {
		return g.ai.navigation.speed
	}
	return 0
}

// goatFinishRam ports RamTarget.finishRam(ServerLevel, Goat): broadcastEntityEvent(goat, 59) (the client
// head-raise reset), then setMemory(RAM_COOLDOWN_TICKS, getTimeBetweenRams(goat).sample(random)) and
// eraseMemory(RAM_TARGET). The brain memories collapse to the goat's own goatRamCooldownTicks countdown
// (goatAiStep decrements it, the CountDownCooldownTicks(RAM_COOLDOWN_TICKS) core behavior). Cite
// RamTarget.finishRam.
func (t *TickLoop) goatFinishRam(g *Entity) {
	t.broadcastToTrackers(g.id, encodeEntityEvent(g.id, 59)) // event 59 == stop lowering head
	g.goatRamCooldownTicks = int32(goatRamCooldownSample(g)) // RAM_COOLDOWN_TICKS reseed
	if g.ai != nil {
		g.ai.setTarget(0) // eraseMemory(RAM_TARGET)
	}
	// eraseMemory(RAM_TARGET): also clear the transient brain ram phase so the sensor can re-arm cleanly.
	if g.goatBrain != nil {
		g.goatBrain.ramTargetID = 0
		g.goatBrain.ramPrepare = 0
		g.goatBrain.ramCharging = false
	}
}

// goatHasRammedHornBreakingBlock ports RamTarget.hasRammedHornBreakingBlock(ServerLevel, Goat): project a
// unit step from the goat's horizontal delta-movement (multiply(1,0,1).normalize()), take the block at
// position + that step, and report whether it OR the block above is in #minecraft:snaps_goat_horn. NO RNG.
//
//	Vec3 dir = getDeltaMovement().multiply(1,0,1).normalize();
//	BlockPos p = BlockPos.containing(position().add(dir));
//	return level.getBlockState(p).is(SNAPS_GOAT_HORN) || level.getBlockState(p.above()).is(SNAPS_GOAT_HORN);
//
// Cite RamTarget.hasRammedHornBreakingBlock + BlockTags.SNAPS_GOAT_HORN.
func (t *TickLoop) goatHasRammedHornBreakingBlock(g *Entity) bool {
	xd, zd := g.vx, g.vz // getDeltaMovement().multiply(1,0,1): zero the y component
	length := math.Sqrt(xd*xd + zd*zd)
	if length == 0 {
		// normalize() of the zero vector is (0,0,0) in vanilla Vec3; the projected pos is the goat's own
		// block, still a valid #snaps_goat_horn test (the goat standing on/against the block).
		xd, zd = 0, 0
	} else {
		xd, zd = xd/length, zd/length
	}
	px := int(math.Floor(g.x + xd))
	py := int(math.Floor(g.y)) // + dir.y (0) -> the goat's foot block
	pz := int(math.Floor(g.z + zd))
	if blockInTag(t.blockStateAt(px, py, pz), goatSnapsGoatHornTag) {
		return true
	}
	return blockInTag(t.blockStateAt(px, py+1, pz), goatSnapsGoatHornTag) // p.above()
}

// goatDropHorn ports Goat.dropHorn(): a BABY never drops (returns false). Pick which horn to snap off
// (only-left -> right stays; only-right -> left stays; both -> random left/right on the goat's stream),
// clear that horn's data flag, then spawn a goat_horn ItemEntity at the goat's position with the toss
// velocity Mth.randomBetween(random, -0.2/0.2) x, (0.3/0.7) y, (-0.2/0.2) z (all on the goat's OWN
// stream). Returns whether a horn was dropped (drives the GOAT_HORN_BREAK sound in the caller, deferred).
//
//	[VERIFIED javap Goat.dropHorn: isBaby -> false; l=hasLeftHorn, r=hasRightHorn; !l&&!r -> false;
//	 pick DATA (l?(!r?LEFT: random.nextBoolean()?LEFT:RIGHT):RIGHT); set that flag false; createHorn();
//	 rx=randomBetween(-0.2,0.2); ry=randomBetween(0.3,0.7); rz=randomBetween(-0.2,0.2);
//	 new ItemEntity(level, pos.x, pos.y, pos.z, horn, rx, ry, rz); addFreshEntity; return true.]
func (t *TickLoop) goatDropHorn(g *Entity) bool {
	if g.isBaby() {
		return false // Goat.dropHorn: `if (isBaby()) return false;`
	}
	hasLeft := g.goatHasLeftHorn
	hasRight := g.goatHasRightHorn
	if !hasLeft && !hasRight {
		return false // no horns left to snap
	}
	// Choose which horn to remove, MATCHING the jar's draw discipline (the random.nextBoolean() is drawn
	// ONLY when both horns are present).
	dropLeft := false
	if !hasLeft {
		dropLeft = false // only the right horn present -> drop the right
	} else if !hasRight {
		dropLeft = true // only the left horn present -> drop the left
	} else {
		dropLeft = mobRandom(g).nextBoolean() // both -> random.nextBoolean() ? LEFT : RIGHT
	}
	if dropLeft {
		g.goatHasLeftHorn = false
	} else {
		g.goatHasRightHorn = false
	}
	// The toss velocity: three Mth.randomBetween draws on the goat's OWN stream, in x, y, z ORDER.
	rng := mobRandom(g)
	rx := float64(mthRandomBetween(rng, float32(goatHornTossXZLo), float32(goatHornTossXZHi)))
	ry := float64(mthRandomBetween(rng, float32(goatHornTossYLo), float32(goatHornTossYHi)))
	rz := float64(mthRandomBetween(rng, float32(goatHornTossXZLo), float32(goatHornTossXZHi)))
	// new ItemEntity(level, pos, createHorn(), rx, ry, rz): the goat_horn drop. NewItemEntity draws its
	// own toss (off the GLOBAL stream, not the goat's); we OVERWRITE vx/vy/vz with the dropHorn toss so
	// the goat's per-entity draw order (the three randomBetween above) is the faithful contract.
	horn := goatCreateHorn(g)
	drop := NewItemEntity(t.idAlloc.AllocID(), g.x, g.y, g.z, horn)
	drop.vx, drop.vy, drop.vz = rx, ry, rz
	if owner := t.regionForEntity(g); owner != nil {
		owner.entities.add(drop)
	} else {
		t.cur().entities.add(drop)
	}
	return true
}

// goatCreateHorn ports Goat.createHorn(): a single goat_horn ItemStack. Vanilla picks a random instrument
// from #screaming_goat_horns / #regular_goat_horns (a thread-local RNG seeded by the goat UUID hash) and
// writes it as the horn's Instrument component; v1 has no per-item Instrument component store yet, so the
// bounded horn is the base goat_horn stack (the instrument tune is the cited deferral -- the drop, the
// observable gameplay, is exact). Cite Goat.createHorn.
func goatCreateHorn(g *Entity) component.SlotData {
	_ = g // the instrument selection (UUID-seeded random instrument component) is the cited deferral
	return component.SlotData{Count: 1, ItemID: pk.VarInt(item.GoatHorn.ID)}
}

// setGoatAgeAttack ports Goat.ageBoundaryReached: ATTACK_DAMAGE base = isBaby ? 1.0 : 2.0, plus the
// baby-halved / adult AABB dims (getDefaultDimensions baby-scale). NO RNG. This is the goat sibling of
// setHoglinAgeAttack. Cite Goat.ageBoundaryReached + getDefaultDimensions.
func setGoatAgeAttack(e *Entity) {
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attribute.AttackDamage.Name()); inst != nil {
			if e.isBaby() {
				inst.SetBaseValue(goatBabyAttackDamage) // dconst_1 -> 1.0
			} else {
				inst.SetBaseValue(goatAdultAttackDamage) // ldc2_w 2.0d
			}
		}
	}
	if e.isBaby() {
		e.width = e.adultWidth * babyDimensionScale
		e.height = e.adultHeight * babyDimensionScale
	} else {
		e.width = e.adultWidth
		e.height = e.adultHeight
	}
}

// tryMilkGoat ports the BUCKET branch of Goat.mobInteract(Player, InteractionHand): an empty BUCKET on an
// ADULT goat plays the milking sound (getMilkingSound, deferred), swaps the hand to a milk_bucket via
// ItemUtils.createFilledResult, and returns SUCCESS. A baby or a non-bucket falls through (returns false)
// to super.mobInteract (the feed/breed path). The goat sibling of tryMilkCow. The held item is read
// SERVER-side. NO RNG (getMilkingSound plays at a fixed 1.0/1.0; deferred anyway).
//
//	[VERIFIED javap Goat.mobInteract: is(Items.BUCKET) && !isBaby() -> playSound(getMilkingSound,1,1) +
//	 ItemUtils.createFilledResult(stack, player, MILK_BUCKET.getDefaultInstance()) + setItemInHand + SUCCESS;
//	 else super.mobInteract.]
func (t *TickLoop) tryMilkGoat(p *tickPlayer, mob *Entity) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if slotIsEmpty(held) {
		return false // empty hand -> super.mobInteract (feed)
	}
	if int32(held.ItemID) != int32(item.Bucket.ID) {
		return false // not an empty bucket -> super.mobInteract
	}
	if mob.isBaby() {
		return false // !isBaby() gate: a kid is not milkable -> feed path
	}
	// player.playSound(getMilkingSound(), 1.0f, 1.0f): GOAT_MILK / GOAT_SCREAMING_MILK, the goat's sound
	// table entry -- DEFERRED (no goat milk sound id wired; the cow path plays 449, the goat's is a distinct
	// id). No RNG regardless. The observable item swap is exact.
	r := t.createFilledResult(p, inv, held, component.SlotData{Count: 1, ItemID: pk.VarInt(item.MilkBucket.ID)})
	inv.set(heldWindowSlot(inv.heldSlot), r)
	t.sendContent(p)
	return true
}

// ---------------------------------------------------------------------------------------------------
// GoatAi long-jump + ram sensor brain (LongJumpToRandomPos / PrepareRamNearestTarget / RamTarget), 1:1 from
// the 26.2 jar (javap this session). The numeric RAM core (damage/knockback/horn drop) is goatRam above;
// this layer is the SENSOR + charge phasing + the high goat-jump that drives it. RAM_COOLDOWN_TICKS is the
// flat goatRamCooldownTicks field (goatFinishRam reseeds it); LONG_JUMP_COOLDOWN_TICKS is goatBrain.

// goatSampleUniform ports UniformInt.of(min,max).sample(rng) == min + rng.nextInt(max-min+1)
// (Mth.randomBetweenInclusive). Cite UniformInt.sample.
func goatSampleUniform(r *entityRandom, lo, hi int) int {
	return lo + r.nextInt(hi-lo+1)
}

// goatRamTimeBetween returns the ram-cooldown UniformInt endpoints for THIS goat: a screaming goat uses
// TIME_BETWEEN_RAMS_SCREAMER (100..300), else TIME_BETWEEN_RAMS (600..6000). Cite GoatAi.initRamActivity.
func (e *Entity) goatRamTimeBetween() (lo, hi int) {
	if e.goatScreaming {
		return goatTimeBetweenRamsScreamerMin, goatTimeBetweenRamsScreamerMax
	}
	return goatTimeBetweenRamsMin, goatTimeBetweenRamsMax
}

// goatFindRamVictim ports PrepareRamNearestTarget candidate pick (NEAREST_VISIBLE_LIVING_ENTITIES feed):
// the nearest live player within RAM_MAX_DISTANCE (7). Returns the victim id (0 if none). NO RNG. Cite
// PrepareRamNearestTarget.start + RAM_TARGET_CONDITIONS.
func (t *TickLoop) goatFindRamVictim(e *Entity) int32 {
	var best *tickPlayer
	bestSq := float64(goatRamMaxDistance * goatRamMaxDistance)
	for _, p := range t.players {
		if p == nil || p.dead || p.gameMode == gameModeSpectator || p.gameMode == gameModeCreative {
			continue
		}
		d := distanceToSqrPlayer(p, e)
		if d <= bestSq {
			bestSq = d
			best = p
		}
	}
	if best != nil {
		return best.entityID
	}
	return 0
}

// goatTryStartRam ports PrepareRamNearestTarget.start: pick a ram victim and arm the prepare phase
// (RAM_PREPARE_TIME). Returns true if a ram was armed. Cite PrepareRamNearestTarget.start.
func (t *TickLoop) goatTryStartRam(e *Entity) bool {
	victim := t.goatFindRamVictim(e)
	if victim == 0 {
		return false
	}
	e.goatBrain.ramTargetID = victim
	e.goatBrain.ramPrepare = goatRamPrepareTime
	e.goatBrain.ramCharging = false
	return true
}

// goatPrepareRam ports the PrepareRamNearestTarget prepare loop: walk toward the victim and, after
// RAM_PREPARE_TIME ticks, arm the RamTarget charge (RAM_TARGET set -> RamTarget.start computes
// ramDirection). The exact pathfind-to-start-position is DEFERRED (reduced to the prepare countdown; the
// goat homes on the victim so the charge faces it). On a lost victim it re-arms the min cooldown
// (getCooldownOnFail == TIME_BETWEEN_RAMS.minInclusive). Cite PrepareRamNearestTarget.tick + RamTarget.start.
func (t *TickLoop) goatPrepareRam(e *Entity) {
	b := e.goatBrain
	victim := t.playerByEntityID(b.ramTargetID)
	if victim == nil || victim.dead {
		e.goatRamCooldownTicks = int32(goatTimeBetweenRamsMin) // getCooldownOnFail == TIME_BETWEEN_RAMS.min
		b.ramTargetID = 0
		b.ramPrepare = 0
		return
	}
	if e.ai != nil {
		e.ai.setWantTargetMod(victim.x, victim.y, victim.z, 1.25) // PrepareRamNearestTarget walkSpeed 1.25f
	}
	if b.ramPrepare > 0 {
		b.ramPrepare--
		return
	}
	dx := victim.x - e.x
	dz := victim.z - e.z
	length := math.Sqrt(dx*dx + dz*dz)
	if length < 1e-9 {
		b.ramDirX, b.ramDirZ = 0, 0
	} else {
		b.ramDirX, b.ramDirZ = dx/length, dz/length
	}
	b.ramCharging = true
}

// goatRamCharge ports RamTarget.tick: charge toward the victim (SPEED_MULTIPLIER_WHEN_RAMMING 3.0) and,
// on contact, run the numeric RAM core (goatRam: ATTACK_DAMAGE + the ram knockback + finishRam reseed +
// the horn-drop on a #snaps_goat_horn block). Cite RamTarget.tick.
func (t *TickLoop) goatRamCharge(e *Entity) {
	b := e.goatBrain
	victim := t.playerByEntityID(b.ramTargetID)
	if victim == nil || victim.dead {
		t.goatFinishRam(e)
		return
	}
	if e.ai != nil {
		e.ai.setWantTargetMod(victim.x, victim.y, victim.z, 3.0)
	}
	if !goatTouchesVictim(e, victim) {
		return
	}
	// RamTarget.tick contact: hurtServer(ATTACK_DAMAGE) + the speed/knockback force term along ramDirection,
	// then finishRam. goatRamPlayer is the tickPlayer twin of goatRam (same numeric pipeline); it reads the
	// frozen ramDirection from goatBrain. A ram into a #snaps_goat_horn block snaps a horn (dropHorn).
	t.goatRamPlayer(e, victim)
}

// goatTouchesVictim reports whether the charging goat footprint overlaps the victim (the bounded
// RamTarget contact broad-phase). A full AABB-intersect is the exact-parity refinement once the entity
// broad-phase lands. Cite RamTarget.getNearbyEntities(goat.getBoundingBox()).
func goatTouchesVictim(e *Entity, victim *tickPlayer) bool {
	hw := e.width/2.0 + playerWidth/2.0
	if math.Abs(victim.x-e.x) > hw || math.Abs(victim.z-e.z) > hw {
		return false
	}
	return math.Abs(victim.y-e.y) <= 2.0
}

// goatRamPlayer ports the RamTarget.tick numeric pipeline against a tickPlayer victim (the goatRam twin;
// goatRam hits an *Entity, this hits a player). hurtServer(noAggroMobAttack, ATTACK_DAMAGE) + the
// clamp(getSpeed()*1.65,0.2,3.0)+effF force * blockF(1.0) * getKnockbackForce along the frozen ramDirection,
// the #snaps_goat_horn horn-drop, then finishRam. Cite RamTarget.tick.
func (t *TickLoop) goatRamPlayer(g *Entity, victim *tickPlayer) {
	if g == nil || victim == nil || victim.dead {
		return
	}
	f := float32(g.getAttributeValue(attribute.AttackDamage))
	t.applyDamage(victim, damageSourceMobAttack(g.id), f)
	effF := float32(goatRamEffectFactor) * 0.0 // speedAmp - slowAmp == 0 (cited default)
	force := mthClampF(float32(goatGetSpeed(g))*float32(goatRamSpeedFactor), float32(goatRamSpeedClampLo), float32(goatRamSpeedClampHi)) + effF
	blockF := float32(1.0)
	power := float64(blockF*force) * goatRamKnockbackForce(g)
	// The charge heading is the frozen ramDirection (start->target); knockback drives the victim forward.
	t.knockback(victim, power, g.goatBrain.ramDirX, g.goatBrain.ramDirZ)
	// RamTarget.tick: if hasRammedHornBreakingBlock -> dropHorn (+ GOAT_HORN_BREAK sound, deferred).
	if t.goatHasRammedHornBreakingBlock(g) {
		t.goatDropHorn(g)
	}
	t.goatFinishRam(g)
}

// goatTryStartLongJump ports LongJumpToRandomPos.start + pickCandidate: choose a random landing pos within
// MAX_LONG_JUMP_WIDTH/HEIGHT (5) and solve the ballistic jump velocity toward it. On success the goat
// enters PREPARE_JUMP_DURATION (40). The WeightedRandom candidate list + Path.canReach check is DEFERRED
// (reduced to a bounded random offset); the ballistic solve is EXACT. RNG on the goat OWN stream. Cite
// LongJumpToRandomPos.start.
func (t *TickLoop) goatTryStartLongJump(e *Entity) bool {
	b := e.goatBrain
	r := mobRandom(e)
	for tries := 0; tries < goatMaxLongJumpWidth*2+1; tries++ {
		ox := r.nextInt(goatMaxLongJumpWidth*2+1) - goatMaxLongJumpWidth
		oz := r.nextInt(goatMaxLongJumpWidth*2+1) - goatMaxLongJumpWidth
		if ox == 0 && oz == 0 {
			continue
		}
		oy := r.nextInt(goatMaxLongJumpHeight*2+1) - goatMaxLongJumpHeight
		tx := math.Floor(e.x) + float64(ox) + 0.5
		ty := math.Floor(e.y) + float64(oy)
		tz := math.Floor(e.z) + float64(oz) + 0.5
		vx, vy, vz, ok := t.goatCalcJumpVector(e, tx, ty, tz)
		if ok {
			b.ljVX, b.ljVY, b.ljVZ = vx, vy, vz
			b.ljChosenValid = true
			b.ljPrepare = goatLongJumpPrepareTime
			return true
		}
	}
	return false
}

// goatLongJumpTick ports LongJumpToRandomPos.tick: after PREPARE_JUMP_DURATION (40) ticks, LAUNCH:
// deltaMovement = chosenJump.scale((length + jumpBoostPower)/length). jumpBoostPower (JUMP_BOOST) is
// DEFERRED (== 0) so the launch velocity == chosenJump exactly. After the launch the activity ends and
// LONG_JUMP_COOLDOWN_TICKS re-samples. Cite LongJumpToRandomPos.tick + LongJumpMidJump.
func (t *TickLoop) goatLongJumpTick(e *Entity) {
	b := e.goatBrain
	if !b.ljChosenValid {
		b.ljPrepare = 0
		return
	}
	if b.ljPrepare > 0 {
		b.ljPrepare--
		return
	}
	length := math.Sqrt(b.ljVX*b.ljVX + b.ljVY*b.ljVY + b.ljVZ*b.ljVZ)
	if length > 0 {
		jumpBoost := 0.0 // getJumpBoostPower() -- JUMP_BOOST effect DEFERRED == 0
		scale := (length + jumpBoost) / length
		e.vx = b.ljVX * scale
		e.vy = b.ljVY * scale
		e.vz = b.ljVZ * scale
	}
	b.ljChosenValid = false
	b.ljPrepare = 0
	b.longJumpCooldown = int32(goatSampleUniform(mobRandom(e), goatTimeBetweenLongJumpsMin, goatTimeBetweenLongJumpsMax))
}

// goatCalcJumpVector ports LongJumpToRandomPos.calculateOptimalJumpVector -> LongJumpUtil
// .calculateJumpVectorForAngle: try ALLOWED_ANGLES [65,70,75,80] with maxVelocity = JUMP_STRENGTH *
// MAX_JUMP_VELOCITY_MULTIPLIER; return the first angle whose ballistic velocity is real and within
// maxVelocity. The per-step arc collision check (isClearTransition) is DEFERRED (needs LONG_JUMPING pose
// dims + Level.noCollision); a clear arc yields the SAME scaled vector, so the returned velocity is EXACT.
// Vanilla shuffles the angle copy; the bounded port keeps the fixed order (the first valid angle wins
// either way for a clear arc). Cite LongJumpUtil.calculateJumpVectorForAngle.
func (t *TickLoop) goatCalcJumpVector(e *Entity, targetX, targetY, targetZ float64) (vx, vy, vz float64, ok bool) {
	maxVel := float64(float32(e.getAttributeValue(attribute.JumpStrength) * goatMaxJumpVelocityMult))
	gravity := e.getAttributeValue(attribute.Gravity)
	for _, angle := range goatAllowedJumpAngles {
		vx, vy, vz, ok = goatJumpVectorForAngle(e.x, e.y, e.z, targetX, targetY, targetZ, maxVel, angle, gravity)
		if ok {
			return vx, vy, vz, true
		}
	}
	return 0, 0, 0, false
}

// goatJumpVectorForAngle ports LongJumpUtil.calculateJumpVectorForAngle ballistic core (clear-arc result).
// Every op mirrors the bytecode; rad is computed in float32 (i2f; fmul 3.1415927f; fdiv 180.0f) then
// widened. Cite LongJumpUtil.calculateJumpVectorForAngle.
func goatJumpVectorForAngle(px, py, pz, targetX, targetY, targetZ, maxVel float64, angle int, gravity float64) (vx, vy, vz float64, ok bool) {
	dirX, _, dirZ := normalizeVec3(targetX-px, 0, targetZ-pz)
	dirX *= 0.5
	dirZ *= 0.5
	v7x, v7y, v7z := targetX-dirX, targetY-0.0, targetZ-dirZ
	v8x, v8y, v8z := v7x-px, v7y-py, v7z-pz
	rad := float64(float32(angle) * 3.1415927 / 180.0)
	at := math.Atan2(v8z, v8x)
	hSq := v8x*v8x + v8z*v8z
	h := math.Sqrt(hSq)
	dy := v8y
	num := hSq * gravity
	den := h*math.Sin(2.0*rad) - 2.0*dy*math.Pow(math.Cos(rad), 2.0)
	t32 := num / den
	if t32 < 0 {
		return 0, 0, 0, false
	}
	spd := math.Sqrt(t32)
	if spd > maxVel {
		return 0, 0, 0, false
	}
	cx := spd * math.Cos(rad)
	cy := spd * math.Sin(rad)
	return cx*math.Cos(at) * goatJumpScaleFactor, cy * goatJumpScaleFactor, cx*math.Sin(at) * goatJumpScaleFactor, true
}
