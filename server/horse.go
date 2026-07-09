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
	m.goals.addGoal(4, newBreedGoal(horseBreedSpeed))
	m.goals.addGoal(5, newTemptGoal(horseTemptSpeed, func(id int32) bool { return itemInTag(id, llamaTemptItemsTag) }, false, nil))
	m.goals.addGoal(6, newFollowParentGoal(horseFollowSpeed))
	m.goals.addGoal(7, newWaterAvoidingRandomStrollGoal(horseStrollSpeed))
	m.goals.addGoal(8, newLookAtPlayerGoal(horseLookDist))
	m.goals.addGoal(9, newRandomLookAroundGoal())
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
	// DEFERRED: the aiStep tail/mouth/eating counters + the playerJumpPendingScale -> executeRidersJump
	// launch (needs the rider mount packet path). No bounded per-tick work today beyond the passive goals.
}

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
