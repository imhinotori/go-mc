package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
)

// wither_skeleton.go -- net.minecraft.world.entity.monster.skeleton.WitherSkeleton, a 1:1 port of the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session). Re-expressed in
// idiomatic Go (no GPL paste) over the SAME shared Go-native goal runtime the Skeleton/Zombie/Stray use.
//
// WitherSkeleton holds a STONE_SWORD, not a bow, so AbstractSkeleton.reassessWeaponGoal installs the
// MeleeAttackGoal (the else branch: held item is not Items.BOW) at priority 4 -- meleeGoal = new
// MeleeAttackGoal(this, 1.2, false) (AbstractSkeleton ctor: ldc2_w 1.2d, iconst_0). It CHASES at
// 1.2 x MOVEMENT_SPEED and swings in reach, dealing effective ATTACK_DAMAGE (4.0 base + the stone_sword
// +4 modifier == 8.0 on NORMAL) and applying WITHER 200 on a landed hit. Cite AbstractSkeleton
// .reassessWeaponGoal (else) + WitherSkeleton.finalizeSpawn + WitherSkeleton.doHurtTarget.
//
// Goal set (WitherSkeleton.registerGoals -> targetSelector @3 NAT<AbstractPiglin>(mustSee), THEN super
// == AbstractSkeleton.registerGoals). goalSelector: @2 RestrictSunGoal, @3 FleeSunGoal(1.0),
// @4 MeleeAttackGoal(1.2,false), @5 WaterAvoidingRandomStrollGoal(1.0), @6 LookAtPlayerGoal(Player,8.0),
// @6 RandomLookAroundGoal. targetSelector: @1 HurtByTargetGoal, @2 NAT<Player>, @3 NAT<AbstractPiglin>.
// DEFERRED (cited, not silently dropped): the @3 AvoidEntityGoal<Wolf> and the @3 NAT<IronGolem>/
// NAT<Turtle> variants -- the same cited-deferral posture the base Skeleton records (a mob-target
// acquire the shared player-victim melee goal cannot act on). HurtBy retaliation, NAT<Player>, wander/
// look, and piglin hostility are all LANDED. Cite WitherSkeleton.registerGoals + AbstractSkeleton.registerGoals.

const (
	witherSkeletonAttackDamageBase = 4.0 // WitherSkeleton.finalizeSpawn: getAttribute(ATTACK_DAMAGE).setBaseValue(4.0d)
	witherSkeletonMeleeSpeed       = 1.2 // AbstractSkeleton ctor: meleeGoal = new MeleeAttackGoal(this, 1.2, false)
	witherSkeletonFleeSunSpeed     = 1.0 // FleeSunGoal(this, 1.0) speedModifier (@3)
	witherSkeletonWitherDuration   = 200 // WitherSkeleton.doHurtTarget: new MobEffectInstance(WITHER, 200)
	witherSkeletonWitherAmplifier  = 0   // the single-arg MobEffectInstance ctor -> amplifier 0
	witherSkeletonLookDistance     = 8.0 // LookAtPlayerGoal(Player, 8.0) (AbstractSkeleton @6)
	witherSkeletonStrollSpeed      = 1.0 // WaterAvoidingRandomStrollGoal(this, 1.0) (@5)
)

// spawnWitherSkeleton creates a WitherSkeleton at (x,y,z): NewEntity seeds the AttributeMap
// (AbstractSkeleton.createAttributes: MOVEMENT_SPEED 0.25, FOLLOW_RANGE 16.0, MAX_HEALTH 20.0,
// ATTACK_DAMAGE 2.0); finalizeSpawn overrides ATTACK_DAMAGE base to 4.0; populateDefaultEquipmentSlots
// sets MAINHAND = STONE_SWORD; the equipment->attribute seam folds its +4 modifier; reassessWeaponGoal
// installs the MeleeAttackGoal (the sword-not-bow else branch). Cite WitherSkeleton ctor + finalizeSpawn
// + populateDefaultEquipmentSlots + reassessWeaponGoal.
func (t *TickLoop) spawnWitherSkeleton(x, y, z float64) *Entity {
	w := NewEntity(t.idAlloc.AllocID(), entity.WitherSkeleton, x, y, z)
	w.isWitherSkeleton = true
	// WitherSkeleton.finalizeSpawn: getAttribute(ATTACK_DAMAGE).setBaseValue(4.0) -- BEFORE initSpawnHealth.
	if w.attributes != nil {
		if inst := w.attributes.GetInstance(attribute.AttackDamage.Name()); inst != nil {
			inst.SetBaseValue(witherSkeletonAttackDamageBase) // ldc2_w 4.0d
		}
	}
	// populateDefaultEquipmentSlots: setItemSlot(MAINHAND, new ItemStack(STONE_SWORD)). No super armor roll.
	populateWitherSkeletonEquipment(w)
	// The mob equipment->attribute seam (reassessWeaponGoal effect + first-tick detectEquipmentUpdates):
	// fold the mainhand sword ATTRIBUTE_MODIFIERS so getAttributeValue(ATTACK_DAMAGE) == 4.0 + 4.0 == 8.0
	// BEFORE the first melee doHurtTarget reads it.
	applyMainHandAttributeModifiers(w)
	initSpawnHealth(w) // setHealth(getMaxHealth()) -> 20.0
	w.ai = buildWitherSkeletonAI(w)
	reseedMobAI(w.ai, w.id) // per-entity RNG stream (Mob.getRandom analogue), like every spawn path
	owner := t.regionForEntity(w)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(w)
	return w
}

// buildWitherSkeletonAI assembles the WitherSkeleton goal + target selectors over the shared Go-native
// goals in the jar-confirmed priorities. It mirrors newPigAI mobAI setup (rng, wantSpeedMod,
// navigation.speed seed) with the wither skeleton MOVEMENT_SPEED 0.25 and hostile goal set. Cite
// WitherSkeleton.registerGoals + AbstractSkeleton.registerGoals + reassessWeaponGoal.
func buildWitherSkeletonAI(w *Entity) *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed) // reseeded per id by spawnWitherSkeleton
	m.wantSpeedMod = 1.0                              // RandomStrollGoal default (stroll getSpeed = 1.0 x MOVEMENT_SPEED)
	ms := w.getAttributeValue(attribute.MovementSpeed) // 0.25 (AbstractSkeleton.createAttributes)
	m.navigation.speed = m.wantSpeedMod * ms           // pre-first-want stroll seed
	// FloatGoal is NOT registered (AbstractSkeleton.registerGoals adds none; the goal walk starts at @2).

	m.goals.addGoal(2, newRestrictSunGoal())                                 // @2 RestrictSunGoal(this) []
	m.goals.addGoal(3, newFleeSunGoal(witherSkeletonFleeSunSpeed*ms))        // @3 FleeSunGoal(this, 1.0) [MOVE]
	m.goals.addGoal(4, newMeleeAttackGoal(witherSkeletonMeleeSpeed))         // @4 MeleeAttackGoal(this, 1.2, false) [MOVE]
	m.goals.addGoal(5, newWaterAvoidingRandomStrollGoal(witherSkeletonStrollSpeed)) // @5 stroll [MOVE]
	m.goals.addGoal(6, newLookAtPlayerGoal(witherSkeletonLookDistance))      // @6 LookAtPlayerGoal(Player, 8.0) [LOOK]
	m.goals.addGoal(6, newRandomLookAroundGoal())                            // @6 RandomLookAroundGoal(this) [MOVE, LOOK]

	m.targetSelector.addGoal(1, newHurtByTargetGoal())               // @1 HurtByTargetGoal(this) [TARGET]
	m.targetSelector.addGoal(2, newNearestAttackableTargetGoal())    // @2 NAT<Player>(this) [TARGET]
	m.targetSelector.addGoal(3, newWitherSkeletonPiglinTargetGoal()) // @3 NAT<AbstractPiglin>(this, mustSee) [TARGET]
	return m
}

// witherSkeletonApplyWither ports the WitherSkeleton.doHurtTarget tail: on a landed hit against a
// LivingEntity (a player here), le.addEffect(new MobEffectInstance(WITHER, 200), this). Gated on the
// landed flag in checkAndPerformAttack, matching the bytecode super.doHurtTarget guard. Cite
// WitherSkeleton.doHurtTarget (offset 23-41).
func (t *TickLoop) witherSkeletonApplyWither(e *Entity, target *tickPlayer) {
	t.addPlayerEffect(target, e.id, effectWither, witherSkeletonWitherDuration, witherSkeletonWitherAmplifier, 1.0)
}

// witherSkeletonApplyWitherEntity is the mob-victim sibling of witherSkeletonApplyWither (R1): WITHER 200
// (amplifier 0) on a landed hit against a MOB victim via addEntityEffectWithSource. Cite
// WitherSkeleton.doHurtTarget (target instanceof LivingEntity).
func (t *TickLoop) witherSkeletonApplyWitherEntity(e *Entity, victim *Entity) {
	t.addEntityEffectWithSource(victim, e.id, effectWither, witherSkeletonWitherDuration, witherSkeletonWitherAmplifier, 1.0)
}

// populateWitherSkeletonEquipment ports WitherSkeleton.populateDefaultEquipmentSlots: MAINHAND =
// STONE_SWORD. It does NOT call super (no armor roll). RNG-FREE. Cite WitherSkeleton.populateDefaultEquipmentSlots.
func populateWitherSkeletonEquipment(e *Entity) {
	e.setItemSlot(eqSlotMainHand, itemStackOf(item.StoneSword)) // new ItemStack(Items.STONE_SWORD)
}
