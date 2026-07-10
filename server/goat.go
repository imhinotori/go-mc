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
)

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
	// GoatAi.initMemories: RAM_COOLDOWN_TICKS sampled from TIME_BETWEEN_RAMS(_SCREAMER) at spawn (the goat
	// is on cooldown from birth). Drawn on the goat's own stream (finalizeSpawn order: after the screaming
	// roll). LONG_JUMP_COOLDOWN_TICKS is the DEFERRED long-jump memory (no long-jump machinery yet).
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

// goatAiStep is the Goat per-tick extra (Goat.customServerAiStep). Vanilla runs the GoatAi BRAIN here (the
// RAM/long-jump/tempt behaviors), which is the DEFERRED behavior layer -- so this is a bounded no-op today
// (the passive goals in newGoatAI drive the visible movement). It is wired + per-type-gated (typ ==
// entity.Goat.ID) so the ram/long-jump slots in here the moment the brain LongJump machinery lands, never
// baked away. RNG-free. Cite Goat.customServerAiStep + GoatAi (the brain deferral note).
func (t *TickLoop) goatAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// GoatAi CORE activity: CountDownCooldownTicks(RAM_COOLDOWN_TICKS) -- tick the ram cooldown down toward
	// 0 every tick (the memory RamTarget.checkExtraStartConditions gates on being ABSENT/expired). This keeps
	// the goat's own goatRamCooldownTicks live so the ram can re-arm; when the brain PrepareRamNearestTarget
	// sensor lands it will select a victim and call goatRam (which reseeds this via goatFinishRam). The
	// screaming variant (e.goatScreaming) shortens the reseed (goatRamCooldownSample). No RNG in the count-down.
	if e.goatRamCooldownTicks > 0 {
		e.goatRamCooldownTicks--
	}
	// DEFERRED: the brain PrepareRamNearestTarget target sensor + LongJumpToRandomPos high goat-jump. The
	// numeric RAM core (damage/knockback/horn drop) is ported and callable as goatRam/goatDropHorn -- it
	// slots in here the moment the sensor selects a victim, never baked away.
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
