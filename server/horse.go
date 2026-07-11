// horse.go -- the HORSE FAMILY (net.minecraft.world.entity.animal.equine.{AbstractHorse,Horse,Donkey,
// Mule,AbstractChestedHorse,Llama,TraderLlama}), a 1:1 port from the unobfuscated 26.2 jar. Rideable/
// tameable/breedable AbstractHorse tree: a Horse; the chested Donkey/Mule; the chested Llama + its
// wandering-trader TraderLlama. Lands the per-entity RANDOMIZED attributes (exact RNG draw order -- the
// load-bearing parity detail), taming + temper, the jump-strength launch, the inventory column model
// (saddle/armor/chest), breeding + mule sterility, and the Llama STRENGTH + spit + caravan hooks. The
// mount PACKET path and the container GUI screen are the DEFERRED behavior layer (they need the ride/
// dismount packet seam + the horse-inventory menu, neither built in this worktree base) -- each is a CITED
// stub wired as a vanilla-default hook, never baked away.
//
// VANILLA (verified javap this session -- package is ...animal.equine, NOT ...animal.horse):
//   AbstractHorse.createBaseHorseAttributes: Animal.createAnimalAttributes + JUMP_STRENGTH 0.7 + MAX_HEALTH
//     53.0 + MOVEMENT_SPEED 0.22499999403953552 + STEP_HEIGHT 1.0 + SAFE_FALL_DISTANCE 6.0 +
//     FALL_DAMAGE_MULTIPLIER 0.5 (level/attribute/defaults.go horseBaseAttributes).
//   AbstractChestedHorse.createBaseChestedHorseAttributes: createBaseHorseAttributes + MOVEMENT_SPEED
//     0.17499999701976776 + JUMP_STRENGTH 0.5 (chestedHorseSupplier).
//   generateMaxHealth(op)      = 15.0f + (float)op(8) + (float)op(9)          [op == RandomSource.nextInt]
//   generateSpeed(sup)         = (0.44999998807907104 + sup()*0.3 + sup()*0.3 + sup()*0.3) * 0.25
//   generateJumpStrength(sup)  = 0.4000000059604645 + sup()*0.2 + sup()*0.2 + sup()*0.2   [sup==nextDouble]
//   Horse.randomizeAttributes (verified draw ORDER): MAX_HEALTH via generateMaxHealth (2 nextInt: bound 8
//     then 9), THEN MOVEMENT_SPEED via generateSpeed (3 nextDouble), THEN JUMP_STRENGTH via
//     generateJumpStrength (3 nextDouble). AbstractChestedHorse.randomizeAttributes randomizes ONLY
//     MAX_HEALTH. AbstractHorse.randomizeAttributes is EMPTY (return) -- only subclasses randomize.
//   getMaxTemper: AbstractHorse 100; Llama 30. modifyTemper(n) = clamp(temper+n, 0, getMaxTemper()).
//   getPlayerJumpPendingScale(charge): charge>=90 -> 1.0f; else 0.4f + 0.4f*charge/90f. onPlayerJump: if
//     !isSaddled return; clamp charge>=0; playerJumpPendingScale = getPlayerJumpPendingScale(charge).
//     getJumpPower(scale) = (float)getAttributeValue(JUMP_STRENGTH)*scale*getBlockJumpFactor() +
//     getJumpBoostPower(). executeRidersJump: setDeltaMovement.y = getJumpPower(scale) (the launch).
//   getInventoryColumns: AbstractHorse 0; AbstractChestedHorse 5-if-hasChest; Llama strength-if-hasChest.
//   getBreedOffspring: Horse+Donkey -> Mule; Horse+Horse -> Horse; Donkey+Donkey -> Donkey; Mule -> Mule
//     (dead code -- Mule.canMate is the AbstractHorse false base, so Mule is STERILE). Llama+Llama -> Llama.
//   Llama.setRandomStrength(rng): i = rng.nextFloat()<0.04f ? 5 : 3; setStrength(1 + rng.nextInt(i));
//     setStrength clamps [1,5]. Llama.spit: LlamaSpit toward target, velocity 0.20000000298023224*dist.
//
// v1 STUBS (cited): the mount packet path (doPlayerRide startRiding + executeRidersJump physics apply) and
// the horse-inventory container GUI (openCustomInventoryScreen) are DEFERRED -- the taming/temper, the
// per-entity randomized stats, the jump-strength SCALAR (horseGetJumpPower), the inventory-column model,
// the breed dispatch + mule sterility, and the Llama strength/spit/caravan hooks are EXACT and wired so the
// deferred packet/GUI seams drop in without rework. The LlamaFollowCaravanGoal + the llama target goals are
// the DEFERRED autonomous-goal layer; the spit is the code-driven llamaSpit hook.

package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
)

// Horse-family constants (VERIFIED javap this session).
const (
	horseBaseMovementSpeed   = 0.22499999403953552 // createBaseHorseAttributes MOVEMENT_SPEED
	chestedBaseMovementSpeed = 0.17499999701976776 // createBaseChestedHorseAttributes MOVEMENT_SPEED
	horseBaseJumpStrength    = 0.7                 // createBaseHorseAttributes JUMP_STRENGTH
	chestedBaseJumpStrength  = 0.5                 // createBaseChestedHorseAttributes JUMP_STRENGTH

	horseGenHealthBase = 15.0                // generateMaxHealth 15.0f base
	horseGenSpeedBase  = 0.44999998807907104 // generateSpeed base
	horseGenSpeedStep  = 0.3                 // generateSpeed per-draw coefficient
	horseGenSpeedScale = 0.25                // generateSpeed final *0.25
	horseGenJumpBase   = 0.4000000059604645  // generateJumpStrength base
	horseGenJumpStep   = 0.2                 // generateJumpStrength per-draw coefficient

	zombieHorseGenJumpBase  = 0.5                 // generateZombieHorseJumpStrength base
	zombieHorseGenJumpStep  = 0.06666666666666667 // generateZombieHorseJumpStrength per-draw coefficient
	zombieHorseGenSpeedBase = 9.0                 // generateZombieHorseSpeed numerator base
	zombieHorseGenSpeedDiv  = 42.15999984741211   // generateZombieHorseSpeed divisor

	horseMaxTemper = 100 // AbstractHorse.getMaxTemper
	llamaMaxTemper = 30  // Llama.getMaxTemper

	chestedInventoryColumns = 5 // AbstractChestedHorse.getInventoryColumns (with chest)

	llamaMinStrength      = 1
	llamaMaxStrength      = 5
	llamaStrongChance     = 0.04 // nextFloat() < 0.04 -> bound 5 else 3
	llamaStrongBound      = 5
	llamaWeakBound        = 3
	llamaBreedStrongBonus = 0.03                // getBreedOffspring: nextFloat()<0.03 -> +1 strength
	llamaSpitVelocity     = 0.20000000298023224 // spit horizontal-velocity scale

	horsePanicSpeed   = 1.2                // RunAroundLikeCrazyGoal / MountPanicGoal / PanicGoal 1.2
	horseBreedSpeed   = 1.0                // BreedGoal 1.0
	horseTemptSpeed   = 1.25               // TemptGoal 1.25
	horseFollowSpeed  = 1.0                // FollowParentGoal 1.0
	horseStrollSpeed  = 0.7                // WaterAvoidingRandomStrollGoal 0.7
	horseLookDist     = 6.0                // LookAtPlayerGoal 6.0
	llamaCaravanSpeed = 2.0999999046325684 // LlamaFollowCaravanGoal 2.1 (DEFERRED goal; documents the pace)

	horseJumpChargeFull = 90  // charge >= 90 -> scale 1.0
	horseJumpScaleBase  = 0.4 // 0.4 + 0.4*charge/90
	horseJumpScaleSpan  = 0.4

	horseTemptItemsTag = "horse_tempt_items" // ItemTags.HORSE_TEMPT_ITEMS
	llamaTemptItemsTag = "llama_tempt_items" // ItemTags.LLAMA_TEMPT_ITEMS
)

// generateMaxHealth ports AbstractHorse.generateMaxHealth(IntUnaryOperator): 15.0f + (float)op(8) +
// (float)op(9). op is RandomSource.nextInt, so the two draws are nextInt(8) then nextInt(9) IN ORDER. The
// result is a float (vanilla f2d then setBaseValue). Cite AbstractHorse.generateMaxHealth.
func generateMaxHealth(rng *entityRandom) float64 {
	h := float32(horseGenHealthBase) + float32(rng.nextInt(8)) + float32(rng.nextInt(9))
	return float64(h)
}

// generateSpeed ports AbstractHorse.generateSpeed(DoubleSupplier): (0.44999998807907104 + s()*0.3 + s()*0.3
// + s()*0.3) * 0.25, s == RandomSource.nextDouble -- three draws in order. Cite AbstractHorse.generateSpeed.
func generateSpeed(rng *entityRandom) float64 {
	sum := horseGenSpeedBase
	sum += rng.nextDouble() * horseGenSpeedStep
	sum += rng.nextDouble() * horseGenSpeedStep
	sum += rng.nextDouble() * horseGenSpeedStep
	return sum * horseGenSpeedScale
}

// generateJumpStrength ports AbstractHorse.generateJumpStrength(DoubleSupplier): 0.4000000059604645 +
// s()*0.2 + s()*0.2 + s()*0.2, s == RandomSource.nextDouble -- three draws in order. Cite
// AbstractHorse.generateJumpStrength.
func generateJumpStrength(rng *entityRandom) float64 {
	v := horseGenJumpBase
	v += rng.nextDouble() * horseGenJumpStep
	v += rng.nextDouble() * horseGenJumpStep
	v += rng.nextDouble() * horseGenJumpStep
	return v
}

// generateZombieHorseJumpStrength ports ZombieHorse.generateZombieHorseJumpStrength(DoubleSupplier):
// 0.5 + s()*0.06666666666666667 + s()*0.06666666666666667 + s()*0.06666666666666667, s ==
// RandomSource.nextDouble -- three draws in order. Distinct from the base generateJumpStrength (0.4 base,
// 0.2 step). Cite ZombieHorse.generateZombieHorseJumpStrength.
func generateZombieHorseJumpStrength(rng *entityRandom) float64 {
	v := zombieHorseGenJumpBase
	v += rng.nextDouble() * zombieHorseGenJumpStep
	v += rng.nextDouble() * zombieHorseGenJumpStep
	v += rng.nextDouble() * zombieHorseGenJumpStep
	return v
}

// generateZombieHorseSpeed ports ZombieHorse.generateZombieHorseSpeed(DoubleSupplier): (9.0 + s()*1 +
// s()*1 + s()*1) / 42.15999984741211, s == RandomSource.nextDouble -- three draws in order. Cite
// ZombieHorse.generateZombieHorseSpeed.
func generateZombieHorseSpeed(rng *entityRandom) float64 {
	sum := zombieHorseGenSpeedBase
	sum += rng.nextDouble()
	sum += rng.nextDouble()
	sum += rng.nextDouble()
	return sum / zombieHorseGenSpeedDiv
}

// randomizeSkeletonHorseAttributes ports SkeletonHorse.randomizeAttributes(RandomSource): it OVERRIDES the
// AbstractHorse base so it randomizes ONLY JUMP_STRENGTH via generateJumpStrength (3 nextDouble). MAX_HEALTH
// (15.0) and MOVEMENT_SPEED (0.2) stay the fixed createAttributes supplier values -- NO draw. Cite
// SkeletonHorse.randomizeAttributes.
func randomizeSkeletonHorseAttributes(e *Entity, rng *entityRandom) {
	e.horseJumpStrength = generateJumpStrength(rng)
}

// randomizeZombieHorseAttributes ports ZombieHorse.randomizeAttributes(RandomSource): JUMP_STRENGTH via
// generateZombieHorseJumpStrength (3 nextDouble) THEN MOVEMENT_SPEED via generateZombieHorseSpeed (3
// nextDouble) -- the exact draw ORDER (jump first, then speed). MAX_HEALTH (25.0) stays the fixed supplier
// value. Cite ZombieHorse.randomizeAttributes.
func randomizeZombieHorseAttributes(e *Entity, rng *entityRandom) {
	e.horseJumpStrength = generateZombieHorseJumpStrength(rng)
	setHorseAttributeBase(e, attribute.MovementSpeed, generateZombieHorseSpeed(rng))
}

// randomizeHorseAttributes ports Horse.randomizeAttributes(RandomSource): MAX_HEALTH via generateMaxHealth,
// THEN MOVEMENT_SPEED via generateSpeed, THEN JUMP_STRENGTH via generateJumpStrength -- the exact draw ORDER
// (2 nextInt, then 6 nextDouble). MOVEMENT_SPEED writes the live AttributeInstance base (the two-tier local-
// divergence model). JUMP_STRENGTH has no registered attribute (cited omission) -> the horseJumpStrength
// field, read by horseGetJumpPower. Cite Horse.randomizeAttributes.
func randomizeHorseAttributes(e *Entity, rng *entityRandom) {
	setHorseAttributeBase(e, attribute.MaxHealth, generateMaxHealth(rng))
	setHorseAttributeBase(e, attribute.MovementSpeed, generateSpeed(rng))
	e.horseJumpStrength = generateJumpStrength(rng)
}

// randomizeChestedHorseAttributes ports AbstractChestedHorse.randomizeAttributes(RandomSource): randomize
// ONLY MAX_HEALTH (generateMaxHealth -- 2 nextInt draws). MOVEMENT_SPEED (0.175) and JUMP_STRENGTH (0.5)
// stay the chested base, no draws. Donkey/Mule/Llama use this. Cite AbstractChestedHorse.randomizeAttributes.
func randomizeChestedHorseAttributes(e *Entity, rng *entityRandom) {
	setHorseAttributeBase(e, attribute.MaxHealth, generateMaxHealth(rng))
	e.horseJumpStrength = chestedBaseJumpStrength
}

// setHorseAttributeBase ports getAttribute(holder).setBaseValue(v) -- the live per-entity base override
// randomizeAttributes performs (diverges THIS entity instance from the shared supplier). A nil map is a
// defensive no-op. Cite AttributeInstance.setBaseValue.
func setHorseAttributeBase(e *Entity, attr *attribute.Attribute, v float64) {
	if e.attributes == nil {
		return
	}
	if inst := e.attributes.GetInstance(attr.Name()); inst != nil {
		inst.SetBaseValue(v)
	}
}

// horseModifyTemper ports AbstractHorse.modifyTemper(int): temper = clamp(temper+n, 0, getMaxTemper());
// returns the new temper. Cite AbstractHorse.modifyTemper.
func (e *Entity) horseModifyTemper(n int) int {
	max := e.horseMaxTemper()
	t := e.horseTemper + n
	if t < 0 {
		t = 0
	}
	if t > max {
		t = max
	}
	e.horseTemper = t
	return t
}

// horseMaxTemper ports getMaxTemper(): 100 AbstractHorse, 30 Llama. Cite AbstractHorse/Llama.getMaxTemper.
func (e *Entity) horseMaxTemper() int {
	if e.isLlama {
		return llamaMaxTemper
	}
	return horseMaxTemper
}

// horseTameWithName ports AbstractHorse.tameWithName(Player): setTamed(true) (+ setOwner + tame event, the
// DEFERRED presentation layer). The load-bearing state is horseTamed = true. Cite AbstractHorse.tameWithName.
func (e *Entity) horseTameWithName() bool {
	e.horseTamed = true
	return true
}

// horseGetPlayerJumpPendingScale ports PlayerRideableJumping.getPlayerJumpPendingScale(int charge): charge
// >= 90 -> 1.0f; else 0.4f + 0.4f*charge/90f. Cite PlayerRideableJumping.getPlayerJumpPendingScale.
func horseGetPlayerJumpPendingScale(charge int) float32 {
	if charge >= horseJumpChargeFull {
		return 1.0
	}
	return float32(horseJumpScaleBase) + float32(horseJumpScaleSpan)*float32(charge)/float32(horseJumpChargeFull)
}

// horseOnPlayerJump ports AbstractHorse.onPlayerJump(int charge): if !isSaddled return; clamp charge>=0
// (else set allowStandSliding + standIfPossible -- the rearing, DEFERRED); playerJumpPendingScale =
// getPlayerJumpPendingScale(charge). The saddle gate + stand/rear are DEFERRED (no saddle-slot item; the
// gate is a cited const via horseIsSaddled). The load-bearing state is the pending-scale set, consumed by
// horseGetJumpPower on the next ground tick. Cite AbstractHorse.onPlayerJump.
func (e *Entity) horseOnPlayerJump(charge int) {
	if !e.horseIsSaddled() {
		return
	}
	if charge < 0 {
		charge = 0
	}
	e.horsePlayerJumpPendingScale = horseGetPlayerJumpPendingScale(charge)
}

// horseIsSaddled ports AbstractHorse.isSaddled() (EquipmentSlot.SADDLE presence). No saddle-slot item is
// wired in this worktree base -> a CITED const-false (an un-saddled horse cannot jump/ride-with-input),
// structured to become a real hasItemInSlot(SADDLE) read. Cite AbstractHorse.isSaddled.
func (e *Entity) horseIsSaddled() bool {
	return false
}

// horseGetJumpPower ports LivingEntity.getJumpPower(float scale) for an AbstractHorse:
// (float)getAttributeValue(JUMP_STRENGTH)*scale*getBlockJumpFactor() + getJumpBoostPower(). JUMP_STRENGTH
// has no registered attribute (cited omission) -> reads the horseJumpStrength field (seeded by
// randomizeAttributes / createBase*Attributes). getBlockJumpFactor (1.0) and getJumpBoostPower (0.0) are
// vanilla-default cited consts. This is the launch SCALAR the deferred executeRidersJump applies as
// deltaMovement.y. Cite LivingEntity.getJumpPower + AbstractHorse.executeRidersJump.
func (e *Entity) horseGetJumpPower(scale float32) float32 {
	const blockJumpFactor = 1.0
	const jumpBoostPower = 0.0
	return float32(e.horseJumpStrength)*scale*float32(blockJumpFactor) + float32(jumpBoostPower)
}

// horseGetInventoryColumns ports getInventoryColumns(): 0 Horse; 5 chested Donkey/Mule (with chest); the
// llama STRENGTH for a chested Llama. Drives the inventory-size (AbstractMountInventoryMenu.getInventory
// Size(columns)); the GUI screen is DEFERRED. Cite AbstractHorse/AbstractChestedHorse/Llama.getInventoryColumns.
func (e *Entity) horseGetInventoryColumns() int {
	if e.isLlama {
		if e.horseHasChest {
			return e.llamaStrength
		}
		return 0
	}
	if e.isDonkey || e.isMule {
		if e.horseHasChest {
			return chestedInventoryColumns
		}
		return 0
	}
	return 0
}

// llamaSetStrength ports Llama.setStrength(int): clamp [1,5] (Math.max(1, Math.min(5, s))). Cite
// Llama.setStrength.
func (e *Entity) llamaSetStrength(s int) {
	if s > llamaMaxStrength {
		s = llamaMaxStrength
	}
	if s < llamaMinStrength {
		s = llamaMinStrength
	}
	e.llamaStrength = s
	e.horseInvColumns = e.horseGetInventoryColumns()
}

// llamaSetRandomStrength ports Llama.setRandomStrength(RandomSource): i = nextFloat()<0.04f ? 5 : 3;
// setStrength(1 + nextInt(i)). TWO draws in order (nextFloat then nextInt(i)). Cite Llama.setRandomStrength.
func (e *Entity) llamaSetRandomStrength(rng *entityRandom) {
	bound := llamaWeakBound
	if rng.nextFloat() < llamaStrongChance {
		bound = llamaStrongBound
	}
	e.llamaSetStrength(1 + rng.nextInt(bound))
}

// horseBreedOffspringType ports the getBreedOffspring species DISPATCH: Horse+Donkey -> Mule; Horse+Horse
// -> Horse; Donkey+Donkey -> Donkey; Donkey+Horse -> Mule; Llama+Llama -> Llama; Mule is STERILE (never an
// initiator -- Mule.canMate is the false base). Returns the offspring type + whether the pair is a valid
// match. Cite Horse.getBreedOffspring + Donkey.getBreedOffspring + Llama.getBreedOffspring +
// AbstractHorse.canMate (Mule base false == sterile).
func horseBreedOffspringType(initiator, partner *Entity) (entity.Entity, bool) {
	switch {
	case initiator.isMule || partner.isMule:
		return entity.Entity{}, false // Mule sterile (canMate false)
	case initiator.isLlama:
		if partner.isLlama {
			return entity.Llama, true
		}
		return entity.Entity{}, false
	case initiator.isHorse:
		if partner.isDonkey {
			return entity.Mule, true // Horse + Donkey -> MULE
		}
		if partner.isHorse {
			return entity.Horse, true
		}
		return entity.Entity{}, false
	case initiator.isDonkey:
		if partner.isHorse {
			return entity.Mule, true // Donkey + Horse -> MULE
		}
		if partner.isDonkey {
			return entity.Donkey, true
		}
		return entity.Entity{}, false
	}
	return entity.Entity{}, false
}

// horseCanMate ports the family canMate() species predicate: AbstractHorse base FALSE (Mule sterility);
// Donkey.canMate: partner Donkey or Horse; Horse/Llama same-species default. The full canParent gate (adult
// + not-in-love-cooldown + tamed) is applied by the breed goal; this is the species half. Cite
// Donkey.canMate + AbstractHorse.canMate + Horse/Llama.canMate.
func horseCanMate(a, b *Entity) bool {
	if a == b {
		return false
	}
	_, ok := horseBreedOffspringType(a, b)
	return ok
}

// llamaSpit ports Llama.spit(LivingEntity) / performRangedAttack: a LlamaSpit toward the target with a
// velocity scaled by 0.20000000298023224 * horizontal-distance. The LlamaSpit projectile + its shoot-with-
// uncertainty are the DEFERRED projectile layer (no LlamaSpit spawn path yet); this computes + documents
// the exact launch vector, the seam the projectile drops into. RNG-free. Cite Llama.spit +
// Llama.performRangedAttack.
func (e *Entity) llamaSpit(tx, ty, tz float64) (vx, vy, vz float64) {
	dx := tx - e.x
	dy := ty - (e.y + float64(e.height)*0.3333333333333333)
	dz := tz - e.z
	horiz := math.Sqrt(dx*dx+dz*dz) * llamaSpitVelocity
	return dx, dy + horiz, dz
}

// newHorseFamilyAI builds the bounded passive AI shared by the Horse/Donkey/Mule (the AbstractHorse
// registerGoals + addBehaviourGoals "visibly alive" subset). Mirrors newCamelAI shape (per-mob rng, nav
// seed, canFloat, Animal pathfinding malus) with the AbstractHorse goal priorities:
//
//	0  FloatGoal
//	1  PanicGoal(1.2)                          (the RunAroundLikeCrazyGoal/MountPanicGoal buck stand-in)
//	2  BreedGoal(1.0)
//	3  TemptGoal(1.25, HORSE_TEMPT_ITEMS, false)
//	4  FollowParentGoal(1.0)
//	6  WaterAvoidingRandomStrollGoal(0.7)
//	7  LookAtPlayerGoal(6.0)
//	8  RandomLookAroundGoal
//
// The RunAroundLikeCrazyGoal (the untamed-mount buck) + RandomStandGoal (the rear) are the DEFERRED mount-
// behavior layer; PanicGoal at @1 is the faithful panic stand-in (same 1.2 speed). The speed seed uses the
// base MOVEMENT_SPEED (0.225 horse / 0.175 chested); per-entity randomize adjusts the live attribute. Cite
// AbstractHorse.registerGoals + addBehaviourGoals.
func newHorseFamilyAI(baseSpeed float64) *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * baseSpeed
	m.navigation.canFloat = true
	applyAnimalPathfindingMalus(m)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(horsePanicSpeed))
	m.goals.addGoal(2, newBreedGoal(horseBreedSpeed))
	m.goals.addGoal(3, newTemptGoal(horseTemptSpeed, func(id int32) bool { return itemInTag(id, horseTemptItemsTag) }, false, nil))
	m.goals.addGoal(4, newFollowParentGoal(horseFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(horseStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(horseLookDist))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// newLlamaAI builds the bounded passive AI for a Llama/TraderLlama (Llama.registerGoals subset). Same shape
// as newHorseFamilyAI with the Llama goal priorities + the LLAMA_TEMPT_ITEMS predicate:
//
//	0  FloatGoal
//	1  PanicGoal(1.2)                          (RunAroundLikeCrazyGoal stand-in; the real @3 PanicGoal 1.2)
//	4  BreedGoal(1.0)
//	5  TemptGoal(1.25, LLAMA_TEMPT_ITEMS, false)
//	6  FollowParentGoal(1.0)
//	7  WaterAvoidingRandomStrollGoal(0.7)
//	8  LookAtPlayerGoal(6.0)
//	9  RandomLookAroundGoal
//
// The @2 LlamaFollowCaravanGoal(2.1), the @3 RangedAttackGoal(spit), and the target selectors are the
// DEFERRED autonomous-goal layer; the spit is llamaSpit. Cite Llama.registerGoals.
func newLlamaAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * chestedBaseMovementSpeed
	m.navigation.canFloat = true
	applyAnimalPathfindingMalus(m)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(horsePanicSpeed))
	// @3 RangedAttackGoal(this, 1.25, 40, 20.0f) [MOVE, LOOK] — the SPIT (GAP 4): the llama chases the
	// target (a wolf, or a mob attacker) into range and fires a LlamaSpit every 40 ticks. Cite
	// Llama.registerGoals @3 RangedAttackGoal + Llama.performRangedAttack -> Llama.spit.
	m.goals.addGoal(3, newLlamaRangedAttackGoal())
	m.goals.addGoal(4, newBreedGoal(horseBreedSpeed))
	m.goals.addGoal(5, newTemptGoal(horseTemptSpeed, func(id int32) bool { return itemInTag(id, llamaTemptItemsTag) }, false, nil))
	m.goals.addGoal(6, newFollowParentGoal(horseFollowSpeed))
	m.goals.addGoal(7, newWaterAvoidingRandomStrollGoal(horseStrollSpeed))
	m.goals.addGoal(8, newLookAtPlayerGoal(horseLookDist))
	m.goals.addGoal(9, newRandomLookAroundGoal())
	// targetSelector — Llama.registerGoals targetSelector EXACTLY:
	// @1 Llama$LlamaHurtByTargetGoal(this) [TARGET] — the base HurtByTargetGoal retaliate-at-attacker (v1
	// reuses newHurtByTargetGoal; the didSpit-drop refinement is cite-deferred, ai_goals_llama.go). So a
	// llama that is hit targets its attacker, then the RangedAttackGoal spits at it. Cite Llama.registerGoals
	// targetSelector @1 (Llama$LlamaHurtByTargetGoal extends HurtByTargetGoal).
	m.targetSelector.addGoal(1, newHurtByTargetGoal())
	// @2 Llama$LlamaAttackWolfGoal(this) [TARGET] — the nearest UNTAMED wolf within FOLLOW_RANGE*0.25. A
	// llama spits at nearby wild wolves. Cite Llama.registerGoals targetSelector @2 (Llama$LlamaAttackWolfGoal).
	m.targetSelector.addGoal(2, newLlamaAttackWolfTargetGoal())
	return m
}

// finalizeHorseSpawn ports AbstractHorse.finalizeSpawn -> randomizeAttributes(getRandom()) (Horse: full;
// Donkey/Mule/Llama: MAX_HEALTH-only). Draws on the mob's per-entity rng so each horse gets its own
// randomized stats, THEN seeds health from the randomized MaxHealth. MUST run AFTER e.ai is attached
// (mobRandom needs the rng) and BEFORE the caller reads health. Cite AbstractHorse.finalizeSpawn +
// Horse/AbstractChestedHorse.randomizeAttributes.
func finalizeHorseSpawn(e *Entity) {
	rng := mobRandom(e)
	if e.isHorse {
		randomizeHorseAttributes(e, rng)
	} else {
		randomizeChestedHorseAttributes(e, rng)
	}
	initSpawnHealth(e)
}

// spawnHorse creates a Horse: createBaseHorseAttributes supplier, passive goal AI, per-entity randomized
// MAX_HEALTH/MOVEMENT_SPEED/JUMP_STRENGTH. baby toggles the AgeableMob baby age + half-scale box. The
// saddle/armor inventory + rideable are DEFERRED. Cite Horse.createAttributes + Horse(EntityType, Level).
func (t *TickLoop) spawnHorse(x, y, z float64, baby bool) *Entity {
	h := NewEntity(t.idAlloc.AllocID(), entity.Horse, x, y, z)
	h.isHorse = true
	h.isHorseFamily = true
	if baby {
		h.breedAge = babyStartAge
		h.refreshDimensions()
	}
	h.ai = newHorseFamilyAI(horseBaseMovementSpeed)
	reseedMobAI(h.ai, h.id)
	finalizeHorseSpawn(h)
	owner := t.regionForEntity(h)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(h)
	return h
}

// spawnChestedHorse is the shared spawn for Donkey/Mule (the AbstractChestedHorse tree): chested-base
// attributes, MAX_HEALTH-only randomize, no chest by default (0 inventory columns until a chest is added).
// typ selects Donkey vs Mule; the isMule flag makes it STERILE (canMate false). Cite Donkey/Mule +
// AbstractChestedHorse.
func (t *TickLoop) spawnChestedHorse(typ entity.Entity, isMule bool, x, y, z float64, baby bool) *Entity {
	c := NewEntity(t.idAlloc.AllocID(), typ, x, y, z)
	c.isHorseFamily = true
	if isMule {
		c.isMule = true
	} else {
		c.isDonkey = true
	}
	if baby {
		c.breedAge = babyStartAge
		c.refreshDimensions()
	}
	c.ai = newHorseFamilyAI(chestedBaseMovementSpeed)
	reseedMobAI(c.ai, c.id)
	finalizeHorseSpawn(c)
	c.horseInvColumns = c.horseGetInventoryColumns()
	owner := t.regionForEntity(c)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(c)
	return c
}

// spawnDonkey / spawnMule are the concrete chested spawns. Cite Donkey / Mule.
func (t *TickLoop) spawnDonkey(x, y, z float64, baby bool) *Entity {
	return t.spawnChestedHorse(entity.Donkey, false, x, y, z, baby)
}
func (t *TickLoop) spawnMule(x, y, z float64, baby bool) *Entity {
	return t.spawnChestedHorse(entity.Mule, true, x, y, z, baby)
}

// spawnLlama creates a Llama (or TraderLlama): chested-base attributes, MAX_HEALTH-only randomize, the
// per-llama STRENGTH via setRandomStrength. Llama.finalizeSpawn draws STRENGTH FIRST (2 draws), THEN
// randomizeAttributes (MAX_HEALTH, 2 draws) -- the draw ORDER is the parity contract, both on the llama's
// per-entity rng. The caravan-follow + spit target goals are DEFERRED; the spit is llamaSpit. Cite
// Llama.finalizeSpawn + TraderLlama(EntityType, Level).
func (t *TickLoop) spawnLlama(x, y, z float64, baby, trader bool) *Entity {
	typ := entity.Llama
	if trader {
		typ = entity.TraderLlama
	}
	l := NewEntity(t.idAlloc.AllocID(), typ, x, y, z)
	l.isLlama = true
	l.isHorseFamily = true
	l.isTraderLlama = trader
	if baby {
		l.breedAge = babyStartAge
		l.refreshDimensions()
	}
	l.ai = newLlamaAI()
	reseedMobAI(l.ai, l.id)
	l.llamaSetRandomStrength(mobRandom(l)) // STRENGTH first (2 draws), then finalizeHorseSpawn (MAX_HEALTH)
	finalizeHorseSpawn(l)
	l.horseInvColumns = l.horseGetInventoryColumns()
	owner := t.regionForEntity(l)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(l)
	return l
}

// finalizeSkeletonHorseSpawn / finalizeZombieHorseSpawn are the AbstractHorse.finalizeSpawn analogues for
// the undead horses: run the type-specific randomizeAttributes (SkeletonHorse: JUMP only; ZombieHorse:
// JUMP + SPEED) on the per-entity rng, THEN seed health from the (fixed) MaxHealth. MUST run after e.ai is
// attached. Cite SkeletonHorse/ZombieHorse.randomizeAttributes + AbstractHorse.finalizeSpawn.
func finalizeSkeletonHorseSpawn(e *Entity) {
	randomizeSkeletonHorseAttributes(e, mobRandom(e))
	initSpawnHealth(e)
}

func finalizeZombieHorseSpawn(e *Entity) {
	randomizeZombieHorseAttributes(e, mobRandom(e))
	initSpawnHealth(e)
}

// spawnSkeletonHorse creates a SkeletonHorse (the undead AbstractHorse). Fixed MAX_HEALTH 15.0 /
// MOVEMENT_SPEED 0.2 from the supplier; JUMP_STRENGTH randomized 0.4..1.0 (generateJumpStrength). The
// isTrap flag + SkeletonTrapGoal (a lightning strike on a trapped skeleton horse spawns 4 skeleton riders)
// is the DEFERRED trap-charge subsystem (see lightning.go) -- a fresh /dbg horse is a plain (non-trap)
// tameable undead mount. The saddle/rideable layer is the same DEFERRED packet/GUI as Horse. Cite
// SkeletonHorse(EntityType, Level) + createAttributes. Spawned via the horse-family passive goal AI.
func (t *TickLoop) spawnSkeletonHorse(x, y, z float64, baby bool) *Entity {
	h := NewEntity(t.idAlloc.AllocID(), entity.SkeletonHorse, x, y, z)
	h.isHorseFamily = true
	if baby {
		h.breedAge = babyStartAge
		h.refreshDimensions()
	}
	h.ai = newHorseFamilyAI(0.20000000298023224) // SkeletonHorse fixed MOVEMENT_SPEED
	reseedMobAI(h.ai, h.id)
	finalizeSkeletonHorseSpawn(h)
	owner := t.regionForEntity(h)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(h)
	return h
}

// spawnZombieHorse creates a ZombieHorse (the undead AbstractHorse). Fixed MAX_HEALTH 25.0 from the
// supplier; JUMP_STRENGTH randomized via generateZombieHorseJumpStrength (0.5 base) THEN MOVEMENT_SPEED via
// generateZombieHorseSpeed ((9+3s)/42.16) -- the exact draw ORDER. Tameable but has no natural spawn in
// vanilla (spawned via /dbg / spawn egg here). The saddle/rideable layer is the same DEFERRED packet/GUI as
// Horse. Cite ZombieHorse(EntityType, Level) + createAttributes + randomizeAttributes.
func (t *TickLoop) spawnZombieHorse(x, y, z float64, baby bool) *Entity {
	h := NewEntity(t.idAlloc.AllocID(), entity.ZombieHorse, x, y, z)
	h.isHorseFamily = true
	if baby {
		h.breedAge = babyStartAge
		h.refreshDimensions()
	}
	h.ai = newHorseFamilyAI(horseBaseMovementSpeed) // ZombieHorse keeps horse-base speed until randomize overwrites it
	reseedMobAI(h.ai, h.id)
	finalizeZombieHorseSpawn(h)
	owner := t.regionForEntity(h)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(h)
	return h
}

// horseFamilyAiStep is the AbstractHorse per-tick extra (AbstractHorse.aiStep/tick: the tail/mouth/eating
// animation counters, the playerJumpPendingScale -> executeRidersJump launch, and the untamed-mount buck).
// The launch + buck ride on the mount PACKET path (a rider double-jump sets the pending scale, then the
// on-ground tick applies getJumpPower as deltaMovement.y) which is DEFERRED -- so today this is a bounded
// no-op beyond the passive goal walk. It is wired + per-type-gated (isHorseFamily) so the jump launch +
// eating/tail counters slot in here the moment the mount machinery lands, never baked away. RNG-free. Cite
// AbstractHorse.aiStep + executeRidersJump.
func (t *TickLoop) horseFamilyAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// The RunAroundLikeCrazyGoal per-tick roll: while an UNTAMED horse carries a player rider, each tick it
	// rolls the tame-or-buck. This is the load-bearing taming contract (a mounted untamed horse either tames
	// on the temper threshold or bucks the rider off, raising temper). Gated on isHorseFamily && !tamed &&
	// isVehicle so an un-ridden or tamed horse (and every non-horse) does ZERO extra work / draws ZERO RNG.
	t.horseRunAroundLikeCrazyTick(e)
	// DEFERRED: the aiStep tail/mouth/eating counters + the playerJumpPendingScale -> executeRidersJump
	// launch (needs the rider mount packet path). No bounded per-tick work today beyond the above.
}

// horseRunAroundLikeCrazyTick ports RunAroundLikeCrazyGoal.tick() 1:1 (verified javap this task): while the
// horse is UNTAMED and carries a first-passenger Player, with a 1-in-adjustedTickDelay(50) chance each tick
// it resolves the mount:
//
//	if (isTamed()) return;
//	if (random.nextInt(adjustedTickDelay(50)) != 0) return;   // the throttle draw
//	firstPassenger = getFirstPassenger(); if (firstPassenger == null) return;
//	if (firstPassenger instanceof Player p) {
//	    temper = getTemper(); maxTemper = getMaxTemper();
//	    if (maxTemper > 0 && random.nextInt(maxTemper) < temper) { tameWithName(p); return; }   // TAME
//	    modifyTemper(5);                                                                          // else raise temper
//	}
//	ejectPassengers(); makeMad(); broadcastEntityEvent(this, 6);                                // BUCK off + rear
//
// The DRAW ORDER (throttle nextInt(50), then -- only if it hits 0 and a player rides -- nextInt(maxTemper))
// is the parity contract, on the horse's OWN per-entity stream (getRandom()). makeMad (the rear + angry
// sound) is a cited-deferred no-op (no stand-pose/sound subsystem); ejectPassengers is the real dismount.
// Runs ONLY for an untamed, player-ridden horse -- a tamed / un-ridden horse early-returns before any draw.
// Cite RunAroundLikeCrazyGoal.tick.
func (t *TickLoop) horseRunAroundLikeCrazyTick(e *Entity) {
	if e.horseTamed {
		return // isTamed() -> the goal does nothing
	}
	if len(e.passengers) == 0 {
		return // no passenger -> the goal never activates (RunAroundLikeCrazyGoal.canUse requires isVehicle)
	}
	rng := mobRandom(e)
	if rng.nextInt(adjustedTickDelay(50)) != 0 {
		return // the 1-in-50 throttle -- most ticks are a single draw then return
	}
	first := e.passengers[0] // getFirstPassenger()
	rp := t.playerByEntityID(first)
	if rp == nil {
		// a non-player first passenger: RunAroundLikeCrazyGoal still bucks (skips the tame branch).
		t.ejectPassengers(e)
		return
	}
	temper := e.horseTemper
	maxTemper := e.horseMaxTemper()
	if maxTemper > 0 && rng.nextInt(maxTemper) < temper { // nextInt(maxTemper) < getTemper() -> TAME
		e.horseTameWithName() // setTamed(true) (+ setOwner/advancement/broadcast: the cited presentation layer)
		return
	}
	e.horseModifyTemper(5) // else raise the temper toward the tame threshold
	// ejectPassengers() + makeMad() + broadcastEntityEvent(this, 6): buck the rider off (the rear + angry
	// sound is the cited-deferred presentation layer; the eject is the real observable dismount).
	t.ejectPassengers(e)
}

// tryHorseFamilyInteract ports the horse-family mobInteract 1:1 (Horse.mobInteract / AbstractChestedHorse
// .mobInteract wrapping AbstractHorse.mobInteract; SkeletonHorse/ZombieHorse use the AbstractHorse base
// directly). Verified javap this task. The unified flow (Horse & chested share the identical wrapper; the
// base tail differs only in the chest-equip branch):
//
//	flag = !isBaby() && isTamed() && player.isSecondaryUseActive();
//	if (isVehicle() || flag || (isBaby() && !isHolding(GOLDEN_DANDELION)))
//	    return <AbstractHorse.mobInteract>;                     // the base tail (openInv / equip / ride)
//	stack = getItemInHand(hand);
//	if (!stack.isEmpty()) {
//	    if (isFood(stack)) return fedFood(player, stack);       // FEED (handleEating: temper/heal/age/love)
//	    if (!isTamed()) { makeMad(); return SUCCESS; }          // untamed + non-food -> buck/rear
//	    if (chested && !hasChest() && stack.is(CHEST)) { equipChest(); return SUCCESS; }   // chested only
//	}
//	return <AbstractHorse.mobInteract>;                         // the base tail
//
// AbstractHorse.mobInteract base tail (offsets verified):
//	if (isVehicle() || isBaby()) return super(Animal).mobInteract;   // (guarded above; re-checked faithfully)
//	if (isTamed() && player.isSecondaryUseActive()) { openCustomInventoryScreen(player); return SUCCESS; }
//	stack = getItemInHand(hand);
//	if (!stack.isEmpty()) {
//	    r = stack.interactLivingEntity(...); if (r.consumesAction()) return r;   // (v1 item-use DEFERRED)
//	    if (isEquippableInSlot(stack, BODY) && !isWearingBodyArmor()) { equipBodyArmor(player, stack); SUCCESS; }
//	}
//	doPlayerRide(player); return SUCCESS;                        // MOUNT (startRiding)
//
// Returns true when the interact belongs to the horse (a mount, a chest/armor equip, a buck, an inventory
// open, or a fed-food consume) so handleInteract does NOT fall through to tryFeedAnimal; returns false ONLY
// for the isFood-but-untamed-and-baby-holding-golden-dandelion path that vanilla routes to the base (which
// still mounts, not feeds -- so this in practice always consumes). horse-family-gated by the caller (mob
// .isHorseFamily); the ONLY RNG this can draw (fedFood -> handleEating setInLove path) is on the horse's own
// per-entity stream, so the pig oracle is unperturbed. Cite Horse/AbstractChestedHorse/AbstractHorse.mobInteract.
func (t *TickLoop) tryHorseFamilyInteract(p *tickPlayer, mob *Entity, usingSecondaryAction bool) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot)) // player.getItemInHand(hand)
	empty := slotIsEmpty(held)
	itemID := int32(0)
	if !empty {
		itemID = int32(held.ItemID)
	}
	chested := mob.isDonkey || mob.isMule || mob.isLlama // AbstractChestedHorse (Donkey/Mule) + Llama

	// SkeletonHorse.mobInteract OVERRIDE: `if (!isTamed()) return PASS; return AbstractHorse.mobInteract(...)`.
	// An UNTAMED skeleton horse does nothing (PASS -> return false, no feed/mount/tame); a TAMED one goes
	// straight to the base tail (openInv / equip / ride). NO feed, NO golden-dandelion, NO makeMad -- the
	// undead horse tames only via the SkeletonTrapGoal / commands (setTamed), not by feeding. Cite
	// SkeletonHorse.mobInteract.
	if mob.typ == entity.SkeletonHorse.ID {
		if !mob.horseTamed {
			return false // PASS -> fall through (an untamed skeleton horse is inert to right-click)
		}
		return t.horseBaseMobInteract(p, mob, usingSecondaryAction)
	}

	// ZombieHorse.mobInteract OVERRIDE: the Horse-shaped wrapper MINUS the golden-dandelion baby exception and
	// the chest branch, with isFood == stack.is(ZOMBIE_HORSE_FOOD): `flag = !isBaby && isTamed && secondary;
	// if (isVehicle() || flag) return base; stack = getItemInHand(hand); if (!stack.isEmpty()) { if (isFood)
	// return fedFood; if (!isTamed) { makeMad; return SUCCESS; } } return base`. Cite ZombieHorse.mobInteract.
	if mob.typ == entity.ZombieHorse.ID {
		flag := !mob.isBaby() && mob.horseTamed && usingSecondaryAction
		if mob.isVehicle() || flag {
			return t.horseBaseMobInteract(p, mob, usingSecondaryAction)
		}
		if !empty {
			if itemInTag(itemID, "zombie_horse_food") { // ZombieHorse.isFood == ZOMBIE_HORSE_FOOD tag
				t.horseFedFood(p, mob, inv, held)
				return true
			}
			if !mob.horseTamed {
				return true // makeMad(): untamed + non-food -> rear (deferred presentation), SUCCESS
			}
		}
		return t.horseBaseMobInteract(p, mob, usingSecondaryAction)
	}

	// flag = !isBaby() && isTamed() && isSecondaryUseActive(). The Horse/Chested wrapper routes to the base
	// tail when isVehicle() || flag || (isBaby() && !isHolding(GOLDEN_DANDELION)). GOLDEN_DANDELION is the
	// baby-feed item (a baby is fed a golden dandelion via fedFood in the wrapper's non-base branch); v1 has
	// no golden-dandelion baby-feed food value wired, so a baby always routes to the base tail here (which
	// mounts nothing for a baby -- doPlayerRide on a baby still startRides, but a baby's getControlling
	// Passenger is null; the observable is a consumed no-op). The isHolding(GOLDEN_DANDELION) exception is
	// preserved faithfully (a baby holding a golden dandelion falls INTO the item branch to be fed).
	holdingGoldenDandelion := itemID == itemGoldenDandelion
	flag := !mob.isBaby() && mob.horseTamed && usingSecondaryAction
	if mob.isVehicle() || flag || (mob.isBaby() && !holdingGoldenDandelion) {
		return t.horseBaseMobInteract(p, mob, usingSecondaryAction)
	}

	// The wrapper item branch (an adult, non-secondary, non-vehicle horse -- or a baby holding golden dandelion).
	if !empty {
		if t.horseIsFood(mob, itemID) {
			// fedFood(player, stack): handleEating (temper/heal/age/love), then consume 1 on success. Returns
			// SUCCESS -> the interact belongs to the horse.
			t.horseFedFood(p, mob, inv, held)
			return true
		}
		if !mob.horseTamed {
			// makeMad(): an untamed horse right-clicked with a non-food item rears + angry-sounds (the cited-
			// deferred presentation); SUCCESS -- the interact belongs to the horse. No mount.
			return true
		}
		if chested && !mob.horseHasChest && itemID == itemChest {
			// AbstractChestedHorse.mobInteract: if (!hasChest() && stack.is(CHEST)) equipChest -> setChest(true),
			// consume 1, createInventory. Donkey/Mule get 5 columns; a Llama chests via the SAME branch with its
			// strength as the column count (horseGetInventoryColumns). Cite AbstractChestedHorse.mobInteract.
			t.horseEquipChest(mob, inv, held)
			return true
		}
	}
	// No item branch consumed -> the base tail (openInv / equip-armor / doPlayerRide mount).
	return t.horseBaseMobInteract(p, mob, usingSecondaryAction)
}

// horseBaseMobInteract ports AbstractHorse.mobInteract's body (the "super" tail the Horse/Chested wrapper
// falls into): the tamed-secondary inventory open, the BODY-armor equip, and the doPlayerRide MOUNT. The
// item-use interactLivingEntity branch (offsets 57-78) is the DEFERRED item-use layer (no server item-use
// dispatch on a mob in v1); the BODY-armor equip folds to a cited-deferred no-op (no BODY equipment slot),
// and the mount is the load-bearing v1 ride. Always returns true (the base tail always consumes: SUCCESS).
// Cite AbstractHorse.mobInteract.
func (t *TickLoop) horseBaseMobInteract(p *tickPlayer, mob *Entity, usingSecondaryAction bool) bool {
	// if (isTamed() && isSecondaryUseActive()) { openCustomInventoryScreen(player); return SUCCESS; }
	if mob.horseTamed && usingSecondaryAction {
		t.horseOpenInventory(p, mob) // openCustomInventoryScreen: the horse-inventory GUI (DEFERRED screen)
		return true
	}
	// stack.interactLivingEntity + isEquippableInSlot(BODY) armor equip: DEFERRED (no item-use dispatch / no
	// BODY equipment slot). A cited no-op that becomes a real equip once the equipment-slot API lands.
	// doPlayerRide(player): setEating(false); clearStanding(); if(!clientSide) player.startRiding(this). The
	// MOUNT is the load-bearing ride -- a tamed adult horse seats the player; an untamed horse also seats the
	// player (RunAroundLikeCrazyGoal then rolls tame-or-buck each tick), matching vanilla doPlayerRide.
	if t.playerStartRiding(p, mob, false) {
		t.broadcastSetPassengers(mob)
	}
	return true // doPlayerRide always returns SUCCESS
}

// horseOpenInventory ports AbstractHorse.openCustomInventoryScreen(player): open the horse-inventory
// container menu (a 2-slot saddle/armor row + getInventoryColumns() storage columns). The container GUI +
// its ClientboundHorseScreenOpen packet are the DEFERRED screen layer (no horse-inventory menu built in this
// worktree base); the load-bearing state (the horse IS a valid inventory-open target, its column count
// computed by horseGetInventoryColumns) is present. Structured to wire the real menu open once the horse-
// inventory menu lands, never baked away. Cite AbstractHorse.openCustomInventoryScreen.
func (t *TickLoop) horseOpenInventory(p *tickPlayer, mob *Entity) {
	mob.horseInvColumns = mob.horseGetInventoryColumns()
	// DEFERRED: openMenu(HorseInventoryMenu) + ClientboundHorseScreenOpen. No screen packet in v1.
}

// horseEquipChest ports AbstractChestedHorse.equipChest(player, stack): setChest(true), playChestEquipsSound
// (DEFERRED sound), consume 1 chest, createInventory (recompute the storage columns). Cite
// AbstractChestedHorse.equipChest.
func (t *TickLoop) horseEquipChest(mob *Entity, inv *Inventory, held component.SlotData) {
	mob.horseHasChest = true            // setChest(true)
	held.Count--                        // stack.consume(1, player)
	inv.set(heldWindowSlot(inv.heldSlot), held)
	mob.horseInvColumns = mob.horseGetInventoryColumns() // createInventory: 5 (donkey/mule) or strength (llama)
	// playChestEquipsSound(): DEFERRED (no sound subsystem).
}

// horseIsFood ports the family isFood(stack): AbstractHorse.isFood == stack.is(HORSE_FOOD); Llama.isFood ==
// stack.is(LLAMA_FOOD). Donkey/Mule inherit the AbstractHorse HORSE_FOOD tag. Cite AbstractHorse.isFood +
// Llama.isFood.
func (t *TickLoop) horseIsFood(mob *Entity, itemID int32) bool {
	if mob.isLlama {
		return itemInTag(itemID, "llama_food")
	}
	return itemInTag(itemID, "horse_food")
}

// horseFedFood ports AbstractHorse.fedFood(player, stack): boolean b = handleEating(player, stack); if (b)
// stack.consume(1, player); return b || clientSide ? SUCCESS_SERVER : PASS. v1 consumes 1 on a true
// handleEating (the observable feed). Cite AbstractHorse.fedFood.
func (t *TickLoop) horseFedFood(p *tickPlayer, mob *Entity, inv *Inventory, held component.SlotData) {
	if t.horseHandleEating(p, mob, int32(held.ItemID)) {
		held.Count-- // stack.consume(1, player)
		inv.set(heldWindowSlot(inv.heldSlot), held)
	}
}

// horseHandleEating ports AbstractHorse.handleEating(player, stack) 1:1 (verified javap this task): match the
// food item to its (healAmount, ageUpSeconds, temperBonus) triple, apply the golden-carrot / golden-apple
// tamed-adult-not-in-love setInLove, the heal-if-below-max, the baby ageUp (+ HAPPY_VILLAGER particle), and
// the temper modify (untamed, temper < maxTemper). Returns whether any effect applied (the fedFood consume
// gate). The per-item table:
//
//	WHEAT:   heal 2,  ageUp 20,  temper 3
//	SUGAR:   heal 1,  ageUp 30,  temper 3
//	HAY_BLOCK: heal 20, ageUp 180, temper 0
//	APPLE:   heal 3,  ageUp 60,  temper 3
//	RED_MUSHROOM: heal 3, ageUp 0, temper 3
//	CARROT:  heal 3,  ageUp 60,  temper 3
//	GOLDEN_CARROT: heal 4, ageUp 60, temper 5 (+ tamed-adult-not-in-love -> setInLove)
//	GOLDEN_APPLE / ENCHANTED_GOLDEN_APPLE: heal 10, ageUp 240, temper 10 (+ tamed-adult-not-in-love -> setInLove)
//
// The temper modify (temperBonus > 0 && (applied || !isTamed()) && getTemper() < getMaxTemper()) is the
// taming contribution -- each non-golden feed of an untamed horse raises its temper toward the tame
// threshold. NO RNG (the particle/sound are DEFERRED). Cite AbstractHorse.handleEating.
func (t *TickLoop) horseHandleEating(p *tickPlayer, mob *Entity, itemID int32) bool {
	applied := false
	var heal float32
	ageUpSeconds := 0
	temperBonus := 0
	switch itemID {
	case itemWheat:
		heal, ageUpSeconds, temperBonus = 2.0, 20, 3
	case itemSugar:
		heal, ageUpSeconds, temperBonus = 1.0, 30, 3
	case itemHayBlock:
		heal, ageUpSeconds, temperBonus = 20.0, 180, 0
	case itemApple:
		heal, ageUpSeconds, temperBonus = 3.0, 60, 3
	case itemRedMushroom:
		heal, ageUpSeconds, temperBonus = 3.0, 0, 3
	case itemCarrot:
		heal, ageUpSeconds, temperBonus = 3.0, 60, 3
	case itemGoldenCarrot:
		heal, ageUpSeconds, temperBonus = 4.0, 60, 5
		if mob.horseTamed && mob.breedAge == 0 && !mob.isInLove() { // isTamed && getAge()==0 && !isInLove
			applied = true
			mob.setInLove()
			t.broadcastHearts(mob)
		}
	case itemGoldenApple, itemEnchantedGoldenApple:
		heal, ageUpSeconds, temperBonus = 10.0, 240, 10
		if mob.horseTamed && mob.breedAge == 0 && !mob.isInLove() {
			applied = true
			mob.setInLove()
			t.broadcastHearts(mob)
		}
	}
	// heal-if-below-max: if (getHealth() < getMaxHealth() && healAmount > 0) { heal(healAmount); applied = true }.
	maxHealth := float32(mob.getAttributeValue(attribute.MaxHealth))
	if mob.health < maxHealth && heal > 0 {
		mob.health += heal
		if mob.health > maxHealth {
			mob.health = maxHealth
		}
		applied = true
	}
	// baby ageUp: if (isBaby() && ageUpSeconds > 0 && !isAgeLocked()) { HAPPY_VILLAGER particle (DEFERRED);
	// if (!clientSide) { ageUp(ageUpSeconds); applied = true } }. isAgeLocked is a v1 const-false stub.
	if mob.isBaby() && ageUpSeconds > 0 {
		wasBaby := mob.isBaby()
		mob.ageUp(ageUpSeconds)
		if wasBaby && !mob.isBaby() {
			t.onGrewUp(mob)
		}
		applied = true
	}
	// temper modify: if (temperBonus > 0 && (applied || !isTamed()) && getTemper() < getMaxTemper()) {
	// modifyTemper(temperBonus); applied = true }.
	if temperBonus > 0 && (applied || !mob.horseTamed) && mob.horseTemper < mob.horseMaxTemper() {
		mob.horseModifyTemper(temperBonus)
		applied = true
	}
	// if (applied) { eating(); gameEvent(EAT); } -- the eating animation + game event are DEFERRED no-ops.
	return applied
}

// Horse-family interact item ids (VERIFIED data/item/item.go this task) -- the handleEating food table +
// the equip/dispatch items.
const (
	itemGoldenDandelion      = 257  // Items.GOLDEN_DANDELION (the baby-feed exception in the wrapper)
	itemRedMushroom          = 276  // Items.RED_MUSHROOM
	itemChest                = 359  // Items.CHEST (AbstractChestedHorse equipChest)
	itemHayBlock             = 532  // Items.HAY_BLOCK
	itemApple                = 921  // Items.APPLE
	itemWheat                = 980  // Items.WHEAT
	itemGoldenApple          = 1014 // Items.GOLDEN_APPLE
	itemEnchantedGoldenApple = 1015 // Items.ENCHANTED_GOLDEN_APPLE
	itemSugar                = 1113 // Items.SUGAR
	itemCarrot               = 1257 // Items.CARROT
	itemGoldenCarrot         = 1262 // Items.GOLDEN_CARROT
)

// --- BREEDING OFFSPRING (C2): AbstractHorse/Horse/Llama getBreedOffspring inheritance ----------------
//
// These port the horse-family getBreedOffspring so a foal INHERITS parent-averaged stats (+ Horse
// variant/markings, Llama strength) instead of running the WILD finalize-spawn randomize roll. Verified
// javap this session (net.minecraft.world.entity.animal.equine.*).

// createOffspring attribute MIN/MAX bounds (AbstractHorse compile-time constants: each MIN uses the
// generate* supplier at 0, each MAX at 1). Verified: generateMaxHealth 15+op(8)+op(9) -> [15,17];
// generateJumpStrength 0.4+3*(s*0.2) -> [0.4,1.0]; generateSpeed (0.45+3*(s*0.3))*0.25 -> [0.1125,0.3375].
const (
	horseOffMinHealth = 15.0
	horseOffMaxHealth = 17.0
	horseOffMinJump   = 0.4000000059604645
	horseOffMaxJump   = 1.0000000178813934
	horseOffMinSpeed  = 0.1125
	horseOffMaxSpeed  = 0.3375
	horseOffSpread    = 0.15 // createOffspringAttribute spread factor (0.15 * (max-min))
)

// createOffspringAttribute ports AbstractHorse.createOffspringAttribute(d0, d1, min, max, random): clamp
// both parents into [min,max], spread=0.15*(max-min), range=abs(d0-d1)+spread*2, mean=(d0+d1)/2, then
// factor=(n1+n2+n3)/3 - 0.5 (THREE nextDouble in order), result=mean+range*factor, reflected into bounds.
// Cite AbstractHorse.createOffspringAttribute.
func createOffspringAttribute(d0, d1, minV, maxV float64, rng *entityRandom) float64 {
	d0 = mthClampD(d0, minV, maxV)
	d1 = mthClampD(d1, minV, maxV)
	spread := horseOffSpread * (maxV - minV)
	rng2 := math.Abs(d0-d1) + spread*2.0
	mean := (d0 + d1) / 2.0
	factor := (rng.nextDouble()+rng.nextDouble()+rng.nextDouble())/3.0 - 0.5
	result := mean + rng2*factor
	if result > maxV {
		return maxV - (result - maxV)
	}
	if result < minV {
		return minV + (minV - result)
	}
	return result
}


// setOffspringAttributes ports AbstractHorse.setOffspringAttributes(other, child): MAX_HEALTH, then
// JUMP_STRENGTH, then MOVEMENT_SPEED -- each createOffspringAttribute (3 nextDouble) on the INITIATOR
// stream (this.random). MAX_HEALTH/MOVEMENT_SPEED write the child attribute base; JUMP_STRENGTH writes the
// horseJumpStrength field (Sulfur has no JUMP_STRENGTH attribute -- the cited omission). 9 nextDouble
// total, in this exact order. Cite AbstractHorse.setOffspringAttributes + createOffspringAttribute.
func setOffspringAttributes(e, partner, child *Entity, rng *entityRandom) {
	// MAX_HEALTH
	h := createOffspringAttribute(horseAttrBase(e, attribute.MaxHealth, horseOffMinHealth), horseAttrBase(partner, attribute.MaxHealth, horseOffMinHealth), horseOffMinHealth, horseOffMaxHealth, rng)
	setHorseAttributeBase(child, attribute.MaxHealth, h)
	// JUMP_STRENGTH (field, not a registered attribute)
	child.horseJumpStrength = createOffspringAttribute(e.horseJumpStrength, partner.horseJumpStrength, horseOffMinJump, horseOffMaxJump, rng)
	// MOVEMENT_SPEED
	sp := createOffspringAttribute(horseAttrBase(e, attribute.MovementSpeed, horseOffMinSpeed), horseAttrBase(partner, attribute.MovementSpeed, horseOffMinSpeed), horseOffMinSpeed, horseOffMaxSpeed, rng)
	setHorseAttributeBase(child, attribute.MovementSpeed, sp)
}

// horseAttrBase reads getAttributeBaseValue(attr) with a fallback when the instance is absent.
func horseAttrBase(e *Entity, attr *attribute.Attribute, fallback float64) float64 {
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attr.Name()); inst != nil {
			return inst.BaseValue()
		}
	}
	return fallback
}

// spawnHorseFamilyOffspring ports the horse-family getBreedOffspring (C2). Dispatches the offspring
// species via horseBreedOffspringType (Horse+Donkey->Mule sterile-mule handled by canMate gate; Horse->
// Horse; Donkey->Donkey; Llama->Llama), spawns a BABY of that type, then applies the exact inheritance:
//   - Horse x Horse: nextInt(9) variant + nextInt(5) markings (both cite-deferred, no field) THEN
//     setOffspringAttributes (9 nextDouble). Horse+Donkey->Mule takes the setOffspringAttributes-only
//     path (0 variant/markings draws), matching Horse.getBreedOffspring.
//   - Llama x Llama: setOffspringAttributes (9 nextDouble) THEN nextInt(max(strA,strB))+1 strength +
//     nextFloat()<0.03 bonus + nextBoolean variant (variant cite-deferred).
//   - Donkey x Donkey / chested: setOffspringAttributes only.
// All draws on the INITIATOR stream (this.random == mobRandom(e)). Cite Horse/Llama/Donkey.getBreedOffspring
// + AbstractHorse.setOffspringAttributes.
func (t *TickLoop) spawnHorseFamilyOffspring(e, partner *Entity) *Entity {
	typ, ok := horseBreedOffspringType(e, partner)
	if !ok {
		return nil // no valid offspring (e.g. a sterile mule) -- getBreedOffspring returns null.
	}
	rng := mobRandom(e)
	var child *Entity
	switch {
	case typ.ID == entity.Horse.ID:
		child = t.spawnHorse(e.x, e.y, e.z, true)
		// Horse x Horse: variant + markings draws BEFORE setOffspringAttributes (both cite-deferred).
		vr := rng.nextInt(9)
		if vr == 8 {
			_ = rng.nextInt(horseVariantCount) // Util.getRandom(Variant.values())
		}
		mr := rng.nextInt(5)
		if mr == 4 {
			_ = rng.nextInt(horseMarkingCount) // Util.getRandom(Markings.values())
		}
		setOffspringAttributes(e, partner, child, rng)
	case typ.ID == entity.Mule.ID:
		// Horse+Donkey -> Mule: setOffspringAttributes only, 0 variant/markings draws.
		child = t.spawnMule(e.x, e.y, e.z, true)
		setOffspringAttributes(e, partner, child, rng)
	case typ.ID == entity.Donkey.ID:
		child = t.spawnDonkey(e.x, e.y, e.z, true)
		setOffspringAttributes(e, partner, child, rng)
	case typ.ID == entity.Llama.ID:
		child = t.spawnLlama(e.x, e.y, e.z, true, false)
		// Llama: attributes FIRST, then strength (nextInt(max)+1, nextFloat<0.03 bonus), then variant.
		setOffspringAttributes(e, partner, child, rng)
		maxStr := e.llamaStrength
		if partner.llamaStrength > maxStr {
			maxStr = partner.llamaStrength
		}
		if maxStr < 1 {
			maxStr = 1 // guard nextInt bound (a fresh llama has strength >=1)
		}
		strength := rng.nextInt(maxStr) + 1
		if rng.nextFloat() < llamaBreedStrongBonus {
			strength++
		}
		child.llamaSetStrength(strength)
		_ = rng.nextBoolean() // LlamaVariant coin-flip (cite-deferred -- no variant field)
	default:
		return nil
	}
	// reset the WILD randomize the spawn helper applied: setOffspringAttributes has overwritten the child
	// health base, so re-seed health from the inherited MaxHealth (the spawn helper seeded it from the wild
	// roll). AbstractHorse.getBreedOffspring does NOT re-run finalizeSpawn; the inherited stats stand.
	initSpawnHealth(child)
	return child
}

// horseVariantCount / horseMarkingCount are the enum sizes for the Horse.Variant / Horse.Markings
// Util.getRandom fallback draws (7 coat variants, 5 markings). Cite Horse.Variant + Horse.Markings.
const (
	horseVariantCount = 7
	horseMarkingCount = 5
)
