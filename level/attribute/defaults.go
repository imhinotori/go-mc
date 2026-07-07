package attribute

import "github.com/imhinotori/sulfur/data/entity"

// defaults.go — the port of net.minecraft.world.entity.ai.attributes.DefaultAttributes plus the
// per-entity createAttributes builders, for the entities Sulfur spawns (Player, Witch, Cat,
// Villager, Zombie, Silverfish). Each builder chain is a method-for-method port of the jar bytecode
// (javap -c -p, this session) — the base-builder composition (createLivingAttributes ->
// createMobAttributes -> createMonsterAttributes / createAnimalAttributes) and the exact per-type
// `.add(holder, value)` overrides, with EVERY override value read directly from the ldc2_w double
// constants in the bytecode.
//
// EXACT PER-TYPE OVERRIDE VALUES (verified this session):
//
//	Player:     ATTACK_DAMAGE 1.0,  MOVEMENT_SPEED 0.10000000149011612, (ATTACK_SPEED default 4.0,
//	            SWEEPING_DAMAGE_RATIO default 0.0)         [Player.createAttributes]
//	Witch:      MAX_HEALTH 26.0, MOVEMENT_SPEED 0.25       [Witch.createAttributes : Monster]
//	Cat:        MAX_HEALTH 10.0, MOVEMENT_SPEED 0.30000001192092896, ATTACK_DAMAGE 3.0
//	            [Cat.createAttributes : Animal]
//	Villager:   MOVEMENT_SPEED 0.5                         [Villager.createAttributes : Mob]
//	Zombie:     FOLLOW_RANGE 35.0, MOVEMENT_SPEED 0.23000000417232513, ATTACK_DAMAGE 3.0, ARMOR 2.0
//	            [Zombie.createAttributes : Monster]
//	Silverfish: MAX_HEALTH 8.0, MOVEMENT_SPEED 0.25, ATTACK_DAMAGE 1.0
//	            [Silverfish.createAttributes : Monster]
//
// BASE-BUILDER CHAINS (the registration defaults flow through these unless overridden):
//
//	createLivingAttributes : MAX_HEALTH(20), KNOCKBACK_RESISTANCE(0), MOVEMENT_SPEED(0.7), ARMOR(0),
//	                         ARMOR_TOUGHNESS(0), MAX_ABSORPTION(0), STEP_HEIGHT(0.6),
//	                         SAFE_FALL_DISTANCE(3.0), ENTITY_INTERACTION_RANGE(3), ATTACK_KNOCKBACK(0),
//	                         ... (plus the non-gameplay set omitted here)
//	createMobAttributes    : + FOLLOW_RANGE override 16.0
//	createMonsterAttributes: + ATTACK_DAMAGE (registration default 2.0, no override)
//	createAnimalAttributes : + TEMPT_RANGE 10.0
//
// CITED NOTE on the omitted non-gameplay attributes: createLivingAttributes also adds SCALE, GRAVITY,
// JUMP_STRENGTH, FALL_DAMAGE_MULTIPLIER, OXYGEN_BONUS, BURNING_TIME, and ~15 others at their
// registration defaults. They are omitted from these builders because no consumer reads them yet (no
// scale, no gravity-attribute path). STEP_HEIGHT (0.6) and SAFE_FALL_DISTANCE (3.0) are NO LONGER
// omitted — they now have live consumers (Entity.maxUpStep and LivingEntity.calculateFallPower) and
// are added to createLivingAttributes below, exactly as vanilla does. Each still-omitted attribute
// equals its vanilla registration default and slots in as one `.Add(...)` line when its consumer
// lands — never baked away. The gameplay subset below is exactly what combat / health / movement /
// AI-ranging / step-up / fall-distance read today.

// createLivingAttributes is the port of LivingEntity.createLivingAttributes() (the gameplay subset):
// the base builder every living entity starts from. The full vanilla method adds ~25 attributes; we
// register the ones with a live consumer (health, defense, movement, interaction, knockback). The
// non-gameplay attributes are the CITED omission documented in the file header.
func createLivingAttributes() *Builder {
	return NewBuilder().
		Add(MaxHealth).              // registration default 20.0
		Add(KnockbackResistance).    // registration default 0.0
		Add(MovementSpeed).          // registration default 0.7 (every entity overrides this)
		Add(Armor).                  // registration default 0.0
		Add(ArmorToughness).         // registration default 0.0
		Add(MaxAbsorption).          // registration default 0.0
		Add(StepHeight).             // registration default 0.6 (createLivingAttributes .add(STEP_HEIGHT))
		Add(SafeFallDistance).       // registration default 3.0 (createLivingAttributes .add(SAFE_FALL_DISTANCE))
		Add(EntityInteractionRange). // registration default 3.0
		Add(AttackKnockback)         // registration default 0.0
}

// createMobAttributes is the port of Mob.createMobAttributes(): createLivingAttributes() with
// FOLLOW_RANGE OVERRIDDEN to 16.0 (`.add(FOLLOW_RANGE, 16.0)`). The registration default is 32.0;
// the generic mob lowers it to 16.0.
func createMobAttributes() *Builder {
	return createLivingAttributes().
		AddValue(FollowRange, 16.0)
}

// createMonsterAttributes is the port of Monster.createMonsterAttributes(): createMobAttributes()
// with ATTACK_DAMAGE added at its REGISTRATION DEFAULT 2.0 (`.add(ATTACK_DAMAGE)`, no value
// override).
func createMonsterAttributes() *Builder {
	return createMobAttributes().
		Add(AttackDamage) // registration default 2.0
}

// createAnimalAttributes is the port of Animal.createAnimalAttributes(): createMobAttributes() with
// TEMPT_RANGE added at 10.0 (`.add(TEMPT_RANGE, 10.0)`).
func createAnimalAttributes() *Builder {
	return createMobAttributes().
		AddValue(TemptRange, 10.0)
}

// playerSupplier is the port of Player.createAttributes() (gameplay subset): createLivingAttributes()
// + ATTACK_DAMAGE override 1.0, MOVEMENT_SPEED override 0.10000000149011612, ATTACK_SPEED (default
// 4.0), SWEEPING_DAMAGE_RATIO (default 0.0). The full vanilla Player builder also adds LUCK,
// BLOCK_INTERACTION_RANGE, BLOCK_BREAK_SPEED, SUBMERGED_MINING_SPEED, SNEAKING_SPEED,
// MINING_EFFICIENCY, WAYPOINT_TRANSMIT/RECEIVE_RANGE — omitted (no consumer), the CITED non-gameplay
// set. The MOVEMENT_SPEED literal 0.10000000149011612 is the vanilla float-widened double, preserved
// bit-for-bit.
func playerSupplier() *Supplier {
	return createLivingAttributes().
		AddValue(AttackDamage, 1.0).
		AddValue(MovementSpeed, 0.10000000149011612).
		Add(AttackSpeed).         // registration default 4.0
		Add(SweepingDamageRatio). // registration default 0.0
		Build()
}

// witchSupplier is the port of Witch.createAttributes() : Monster.createMonsterAttributes() +
// MAX_HEALTH 26.0 + MOVEMENT_SPEED 0.25.
func witchSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 26.0).
		AddValue(MovementSpeed, 0.25).
		Build()
}

// catSupplier is the port of Cat.createAttributes() : Animal.createAnimalAttributes() + MAX_HEALTH
// 10.0 + MOVEMENT_SPEED 0.30000001192092896 + ATTACK_DAMAGE 3.0. The MOVEMENT_SPEED literal is the
// vanilla float-widened double, preserved bit-for-bit.
func catSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 10.0).
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(AttackDamage, 3.0).
		Build()
}

// villagerSupplier is the port of Villager.createAttributes() : Mob.createMobAttributes() +
// MOVEMENT_SPEED 0.5. NOTE Villager builds on createMobAttributes (NOT Monster/Animal), so it has NO
// ATTACK_DAMAGE and NO TEMPT_RANGE — only the mob base + the movement override.
func villagerSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MovementSpeed, 0.5).
		Build()
}

// zombieSupplier is the port of Zombie.createAttributes() : Monster.createMonsterAttributes() +
// FOLLOW_RANGE 35.0 + MOVEMENT_SPEED 0.23000000417232513 + ATTACK_DAMAGE 3.0 + ARMOR 2.0 (+
// SPAWN_REINFORCEMENTS_CHANCE, omitted — no reinforcement spawning consumer yet; CITED). The
// MOVEMENT_SPEED literal is the vanilla float-widened double, preserved bit-for-bit.
func zombieSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(FollowRange, 35.0).
		AddValue(MovementSpeed, 0.23000000417232513).
		AddValue(AttackDamage, 3.0).
		AddValue(Armor, 2.0).
		Build()
}

// silverfishSupplier is the port of Silverfish.createAttributes() : Monster.createMonsterAttributes()
// + MAX_HEALTH 8.0 + MOVEMENT_SPEED 0.25 + ATTACK_DAMAGE 1.0.
func silverfishSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 8.0).
		AddValue(MovementSpeed, 0.25).
		AddValue(AttackDamage, 1.0).
		Build()
}

// vexSupplier is the port of Vex.createAttributes() : Monster.createMonsterAttributes() + MAX_HEALTH
// 14.0 + ATTACK_DAMAGE 4.0 (jar bytecode this session: net/minecraft/world/entity/monster/Vex
// .createAttributes -> Monster.createMonsterAttributes().add(MAX_HEALTH, 14.0).add(ATTACK_DAMAGE, 4.0)).
// NOTE Vex does NOT override MOVEMENT_SPEED (it flies via VexMoveControl, not the ground navigation), so
// MOVEMENT_SPEED stays at the createLivingAttributes registration default 0.7 -- the Vex flight math
// reads a per-request speedModifier (1.0 charge / 0.25 wander) times 0.05, NOT the MOVEMENT_SPEED attr.
func vexSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 14.0).
		AddValue(AttackDamage, 4.0).
		Build()
}

// pigSupplier is the port of Pig.createAttributes() : Animal.createAnimalAttributes() + MAX_HEALTH
// 10.0 + MOVEMENT_SPEED 0.25 (jar bytecode this session:
// net/minecraft/world/entity/animal/pig/Pig.createAttributes — ldc2_w 10.0d, 0.25d).
func pigSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 10.0).
		AddValue(MovementSpeed, 0.25).
		Build()
}

// cowSupplier is the port of AbstractCow.createAttributes() : Animal.createAnimalAttributes() +
// MAX_HEALTH 10.0 + MOVEMENT_SPEED 0.20000000298023224 (jar:
// net/minecraft/world/entity/animal/cow/AbstractCow.createAttributes — ldc2_w 10.0d, then the
// float-widened double 0.20000000298023224d, preserved bit-for-bit). createAttributes lives on
// AbstractCow, which Cow extends.
func cowSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 10.0).
		AddValue(MovementSpeed, 0.20000000298023224).
		Build()
}

// mooshroomSupplier is MushroomCow's attribute supplier. MushroomCow extends AbstractCow and does NOT
// override createAttributes (javap-verified), so its supplier is IDENTICAL to the cow's (AbstractCow.
// createAttributes: MAX_HEALTH 10, MOVEMENT_SPEED 0.2). Cite MushroomCow (no createAttributes override).
func mooshroomSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 10.0).
		AddValue(MovementSpeed, 0.20000000298023224).
		Build()
}

// foxSupplier is Fox's attribute supplier. Fox.createAttributes = Animal.createAnimalAttributes()
// .add(MOVEMENT_SPEED 0.3).add(MAX_HEALTH 10).add(ATTACK_DAMAGE 2).add(SAFE_FALL_DISTANCE 5).add(
// FOLLOW_RANGE 32). Cite Fox.createAttributes (net/minecraft/world/entity/animal/fox/Fox — javap this
// session: MOVEMENT_SPEED ldc2_w 0.30000001192092896d, MAX_HEALTH 10.0d, ATTACK_DAMAGE 2.0d,
// SAFE_FALL_DISTANCE ldc2_w 5.0d, FOLLOW_RANGE 32.0d). SAFE_FALL_DISTANCE 5.0 OVERRIDES the base
// createLivingAttributes default 3.0 (calculateFallPower reads it — a fox survives a taller fall).
func foxSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(MaxHealth, 10.0).
		AddValue(AttackDamage, 2.0).
		AddValue(SafeFallDistance, 5.0).
		AddValue(FollowRange, 32.0).
		Build()
}

// endermiteSupplier is Endermite's attribute supplier. Endermite.createAttributes = Monster
// .createMonsterAttributes().add(MAX_HEALTH 8).add(MOVEMENT_SPEED 0.25).add(ATTACK_DAMAGE 2). Cite
// net.minecraft.world.entity.monster.Endermite.createAttributes (javap this session: MAX_HEALTH
// ldc2_w 8.0d, MOVEMENT_SPEED 0.25d, ATTACK_DAMAGE 2.0d).
func endermiteSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 8.0).
		AddValue(MovementSpeed, 0.25).
		AddValue(AttackDamage, 2.0).
		Build()
}

// turtleSupplier is Turtle's attribute supplier. Turtle.createAttributes = Animal.createAnimalAttributes()
// .add(MAX_HEALTH 30).add(MOVEMENT_SPEED 0.25).add(STEP_HEIGHT 1.0). Cite
// net.minecraft.world.entity.animal.turtle.Turtle.createAttributes (javap this session: MAX_HEALTH
// ldc2_w 30.0d, MOVEMENT_SPEED 0.25d, STEP_HEIGHT dconst_1 == 1.0d). STEP_HEIGHT 1.0 OVERRIDES the base
// createLivingAttributes default 0.6 (a turtle auto-steps a full block, like the enderman).
func turtleSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 30.0).
		AddValue(MovementSpeed, 0.25).
		AddValue(StepHeight, 1.0).
		Build()
}

// ocelotSupplier is Ocelot's attribute supplier. Ocelot.createAttributes = Animal.createAnimalAttributes()
// .add(MAX_HEALTH 10).add(MOVEMENT_SPEED 0.3).add(ATTACK_DAMAGE 3). Cite
// net.minecraft.world.entity.animal.feline.Ocelot.createAttributes (javap this session: MAX_HEALTH
// ldc2_w 10.0d, MOVEMENT_SPEED ldc2_w 0.30000001192092896d float-widened double, ATTACK_DAMAGE 3.0d).
func ocelotSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 10.0).
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(AttackDamage, 3.0).
		Build()
}

// pillagerSupplier is Pillager's attribute supplier. Pillager.createAttributes = Monster.createMonster
// Attributes().add(MOVEMENT_SPEED 0.35f).add(FOLLOW_RANGE 32).add(MAX_HEALTH 24).add(ATTACK_DAMAGE 5).
// MOVEMENT_SPEED is the float-widened double 0.3499999940395355 (0.35f promoted). Cite
// net.minecraft.world.entity.monster.illager.Pillager.createAttributes.
func pillagerSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MovementSpeed, 0.3499999940395355).
		AddValue(FollowRange, 32.0).
		AddValue(MaxHealth, 24.0).
		AddValue(AttackDamage, 5.0).
		Build()
}

// vindicatorSupplier is Vindicator's attribute supplier. Vindicator.createAttributes = Monster.create
// MonsterAttributes().add(MOVEMENT_SPEED 0.35f).add(FOLLOW_RANGE 12).add(MAX_HEALTH 24).add(ATTACK_DAMAGE 5).
// MOVEMENT_SPEED is the float-widened double 0.3499999940395355. Cite
// net.minecraft.world.entity.monster.illager.Vindicator.createAttributes.
func vindicatorSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MovementSpeed, 0.3499999940395355).
		AddValue(FollowRange, 12.0).
		AddValue(MaxHealth, 24.0).
		AddValue(AttackDamage, 5.0).
		Build()
}

// evokerSupplier is Evoker's attribute supplier. Evoker.createAttributes = Monster.createMonster
// Attributes().add(MOVEMENT_SPEED 0.5).add(FOLLOW_RANGE 12).add(MAX_HEALTH 24). Evoker has NO ATTACK_DAMAGE
// override (its damage is the fangs spell). Cite net.minecraft.world.entity.monster.illager.Evoker
// .createAttributes.
func evokerSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MovementSpeed, 0.5).
		AddValue(FollowRange, 12.0).
		AddValue(MaxHealth, 24.0).
		Build()
}

// ravagerSupplier is Ravager's attribute supplier. Ravager.createAttributes = Monster.createMonster
// Attributes().add(MAX_HEALTH 100).add(MOVEMENT_SPEED 0.3).add(KNOCKBACK_RESISTANCE 0.75).add(ATTACK_DAMAGE
// 12).add(ATTACK_KNOCKBACK 1.5).add(FOLLOW_RANGE 32).add(STEP_HEIGHT 1.0). All plain doubles (no float
// widening). Cite net.minecraft.world.entity.monster.Ravager.createAttributes.
func ravagerSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 100.0).
		AddValue(MovementSpeed, 0.3).
		AddValue(KnockbackResistance, 0.75).
		AddValue(AttackDamage, 12.0).
		AddValue(AttackKnockback, 1.5).
		AddValue(FollowRange, 32.0).
		AddValue(StepHeight, 1.0).
		Build()
}

// ironGolemSupplier is IronGolem's attribute supplier. IronGolem.createAttributes = Mob.createMobAttributes()
// (NOT Monster/Animal — the golem has NO base ATTACK_DAMAGE 2.0 / TEMPT_RANGE; it ADDS ATTACK_DAMAGE 15.0
// explicitly) .add(MAX_HEALTH 100).add(MOVEMENT_SPEED 0.25).add(KNOCKBACK_RESISTANCE 1.0).add(ATTACK_DAMAGE
// 15.0).add(STEP_HEIGHT 1.0). No ATTACK_KNOCKBACK override (stays the createLivingAttributes default 0.0 —
// the golem's fling is the doHurtTarget vertical impulse, NOT an ATTACK_KNOCKBACK modifier). All plain
// doubles (no float widening). AbstractGolem has NO createAttributes override, so IronGolem builds directly
// on Mob.createMobAttributes. Cite net.minecraft.world.entity.animal.golem.IronGolem.createAttributes.
func ironGolemSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 100.0).
		AddValue(MovementSpeed, 0.25).
		AddValue(KnockbackResistance, 1.0).
		AddValue(AttackDamage, 15.0).
		AddValue(StepHeight, 1.0).
		Build()
}

// happyGhastSupplier is the port of HappyGhast.createAttributes(): Animal.createAnimalAttributes()
// (which adds TEMPT_RANGE 10.0) then .add(MAX_HEALTH 20.0).add(TEMPT_RANGE 16.0).add(FLYING_SPEED 0.05)
// .add(MOVEMENT_SPEED 0.05).add(FOLLOW_RANGE 16.0).add(CAMERA_DISTANCE 8.0). The later TEMPT_RANGE 16.0
// overrides the animal 10.0 (buildKeepingLast). Cite net.minecraft.world.entity.animal.happyghast.
// HappyGhast.createAttributes (javap: ldc2_w 20.0d MAX_HEALTH, 16.0d TEMPT_RANGE, 0.05d FLYING_SPEED,
// 0.05d MOVEMENT_SPEED, 16.0d FOLLOW_RANGE, 8.0d CAMERA_DISTANCE).
func happyGhastSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 20.0).
		AddValue(TemptRange, 16.0).
		AddValue(FlyingSpeed, 0.05).
		AddValue(MovementSpeed, 0.05).
		AddValue(FollowRange, 16.0).
		AddValue(CameraDistance, 8.0).
		Build()
}

// ghastSupplier is the port of Ghast.createAttributes(): Mob.createMobAttributes() (NOT Monster --
// the hostile Ghast has NO ATTACK_DAMAGE; it attacks via a fireball projectile) then .add(MAX_HEALTH
// 10.0).add(FOLLOW_RANGE 100.0).add(CAMERA_DISTANCE 8.0).add(FLYING_SPEED 0.06). Cite
// net.minecraft.world.entity.monster.Ghast.createAttributes (javap: createMobAttributes, ldc2_w 10.0d
// MAX_HEALTH, 100.0d FOLLOW_RANGE, 8.0d CAMERA_DISTANCE, 0.06d FLYING_SPEED). The FOLLOW_RANGE 100.0
// override (over the createMobAttributes 16.0) is the ghast's long acquisition range.
func ghastSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 10.0).
		AddValue(FollowRange, 100.0).
		AddValue(CameraDistance, 8.0).
		AddValue(FlyingSpeed, 0.06).
		Build()
}

// blazeSupplier is the port of Blaze.createAttributes(): Monster.createMonsterAttributes() then
// .add(ATTACK_DAMAGE 6.0).add(MOVEMENT_SPEED 0.23000000417232513).add(FOLLOW_RANGE 48.0). MAX_HEALTH
// is the createLivingAttributes default 20.0 (Blaze has NO MAX_HEALTH override). Cite
// net.minecraft.world.entity.monster.Blaze.createAttributes (javap: createMonsterAttributes, ldc2_w
// 6.0d ATTACK_DAMAGE, 0.23000000417232513d MOVEMENT_SPEED, 48.0d FOLLOW_RANGE).
func blazeSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(AttackDamage, 6.0).
		AddValue(MovementSpeed, 0.23000000417232513).
		AddValue(FollowRange, 48.0).
		Build()
}

// endermanSupplier is EnderMan's attribute supplier. EnderMan.createAttributes = Monster
// .createMonsterAttributes().add(MAX_HEALTH 40).add(MOVEMENT_SPEED 0.3).add(ATTACK_DAMAGE 7)
// .add(FOLLOW_RANGE 64).add(STEP_HEIGHT 1.0). Cite EnderMan.createAttributes
// (net/minecraft/world/entity/monster/EnderMan — javap this session: MAX_HEALTH ldc2_w 40.0d,
// MOVEMENT_SPEED 0.30000001192092896d, ATTACK_DAMAGE 7.0d, FOLLOW_RANGE 64.0d, STEP_HEIGHT dconst_1
// == 1.0d). STEP_HEIGHT 1.0 OVERRIDES the base createLivingAttributes default 0.6 (an enderman
// auto-steps a full block).
func endermanSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 40.0).
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(AttackDamage, 7.0).
		AddValue(FollowRange, 64.0).
		AddValue(StepHeight, 1.0).
		Build()
}

// rabbitSupplier is Rabbit's attribute supplier. Rabbit.createAttributes = Animal.createAnimalAttributes()
// .add(MAX_HEALTH 3.0).add(MOVEMENT_SPEED 0.3).add(ATTACK_DAMAGE 3.0). Cite Rabbit.createAttributes.
func rabbitSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 3.0).
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(AttackDamage, 3.0).
		Build()
}

// huskSupplier is Husk's attribute supplier. Husk extends Zombie and does NOT override createAttributes
// (javap-verified), so its supplier is IDENTICAL to the zombie's (Zombie.createAttributes: FOLLOW_RANGE
// 35, MOVEMENT_SPEED 0.23, ATTACK_DAMAGE 3, ARMOR 2). Cite Husk (no createAttributes override).
func huskSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(FollowRange, 35.0).
		AddValue(MovementSpeed, 0.23000000417232513).
		AddValue(AttackDamage, 3.0).
		AddValue(Armor, 2.0).
		Build()
}

// wolfSupplier is the port of Wolf.createAttributes() : Animal.createAnimalAttributes() (Wolf extends
// TamableAnimal -> Animal) + MOVEMENT_SPEED 0.3 + MAX_HEALTH 8.0 + ATTACK_DAMAGE 4.0 (jar:
// net/minecraft/world/entity/animal/wolf/Wolf.createAttributes — MOVEMENT_SPEED 0.30000001192092896d,
// MAX_HEALTH 8.0d, ATTACK_DAMAGE 4.0d). The MOVEMENT_SPEED literal is the vanilla float-widened double,
// preserved bit-for-bit. NOTE the tamed-wolf MAX_HEALTH 40 + full heal is a RUNTIME side-effect of
// setTame (applyTamingSideEffects), NOT the base supplier — an untamed wolf has MAX_HEALTH 8.0 here, and
// Plan B applies the 8->40 bump on tame.
//
//	[VERIFIED javap Wolf.createAttributes: createAnimalAttributes; MOVEMENT_SPEED ldc2_w
//	 0.30000001192092896d; MAX_HEALTH ldc2_w 8.0d; ATTACK_DAMAGE ldc2_w 4.0d; build. applyTamingSideEffects
//	 sets MAX_HEALTH base 40.0 + setHealth(40) on tame.]
func wolfSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(MaxHealth, 8.0).
		AddValue(AttackDamage, 4.0).
		Build()
}

// sheepSupplier is the port of Sheep.createAttributes() : Animal.createAnimalAttributes() +
// MAX_HEALTH 8.0 + MOVEMENT_SPEED 0.23000000417232513 (jar:
// net/minecraft/world/entity/animal/sheep/Sheep.createAttributes — ldc2_w 8.0d, then the
// float-widened double 0.23000000417232513d, preserved bit-for-bit).
func sheepSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 8.0).
		AddValue(MovementSpeed, 0.23000000417232513).
		Build()
}

// chickenSupplier is the port of Chicken.createAttributes() : Animal.createAnimalAttributes() +
// MAX_HEALTH 4.0 + MOVEMENT_SPEED 0.25 (jar:
// net/minecraft/world/entity/animal/chicken/Chicken.createAttributes — ldc2_w 4.0d, 0.25d).
func chickenSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 4.0).
		AddValue(MovementSpeed, 0.25).
		Build()
}

// skeletonSupplier is the port of AbstractSkeleton.createAttributes() : Monster.createMonsterAttributes()
// + MOVEMENT_SPEED 0.25 (jar:
// net/minecraft/world/entity/monster/skeleton/AbstractSkeleton.createAttributes — ldc2_w 0.25d).
// createAttributes lives on AbstractSkeleton, which Skeleton extends (Skeleton has no own override).
func skeletonSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MovementSpeed, 0.25).
		Build()
}

// creeperSupplier is the port of Creeper.createAttributes() : Monster.createMonsterAttributes() +
// MOVEMENT_SPEED 0.25 (jar: net/minecraft/world/entity/monster/Creeper.createAttributes — ldc2_w
// 0.25d).
func creeperSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MovementSpeed, 0.25).
		Build()
}

// spiderSupplier is the port of Spider.createAttributes() : Monster.createMonsterAttributes() +
// MAX_HEALTH 16.0 + MOVEMENT_SPEED 0.30000001192092896 (jar:
// net/minecraft/world/entity/monster/spider/Spider.createAttributes — ldc2_w 16.0d, then the
// float-widened double 0.30000001192092896d, preserved bit-for-bit).
func spiderSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 16.0).
		AddValue(MovementSpeed, 0.30000001192092896).
		Build()
}

// sulfurCubeSupplier is the port of SulfurCube.createSulfurCubeAttributes() : Mob.createMobAttributes()
// + TEMPT_RANGE 8.0 (jar: net.minecraft.world.entity.monster.cubemob.SulfurCube.createSulfurCubeAttributes
// == createMobAttributes().add(Attributes.TEMPT_RANGE, 8.0)). NOTE the cube builds on createMobAttributes
// (NOT Monster), so it has NO ATTACK_DAMAGE by default — and SulfurCube.isDealsDamage() returns false, so
// the base cube never reads ATTACK_DAMAGE (contact damage is a body-item/archetype feature, cite-deferred).
// The MAX_HEALTH and MOVEMENT_SPEED here are only the createMobAttributes/createLivingAttributes base
// values (20.0 / 0.7); setSize OVERRIDES BOTH at runtime per the size machine (SulfurCube.setcubeMobHealth:
// MAX_HEALTH = 4*size; AbstractCubeMob.setSize: MOVEMENT_SPEED base = 0.2 + 0.1*size). Cite
// SulfurCube.createSulfurCubeAttributes.
func sulfurCubeSupplier() *Supplier {
	return createMobAttributes().
		AddValue(TemptRange, 8.0).
		Build()
}

// livingFallbackSupplier is the port of LivingEntity.createLivingAttributes() (the gameplay subset):
// the base attribute set EVERY LivingEntity has. Vanilla's DefaultAttributes registers a supplier for
// every living EntityType; Sulfur ports the common per-type suppliers above and leans on THIS fallback
// for any still-unported living type, so NewMapForEntity NEVER returns nil for a living entity (the
// operator-mandated "todos deberían estar disponibles" guarantee). It COMPOSES the real
// createLivingAttributes() builder (not a baked value) so a future per-type port simply adds to the
// `suppliers` map. Cite: net.minecraft.world.entity.LivingEntity.createLivingAttributes(). The full
// vanilla method also adds the non-gameplay attributes (STEP_HEIGHT, SCALE, GRAVITY, JUMP_STRENGTH,
// SAFE_FALL_DISTANCE, FALL_DAMAGE_MULTIPLIER, OXYGEN_BONUS, BURNING_TIME, …) — the CITED omission
// documented in the file header (no live consumer yet; each equals its registration default).
func livingFallbackSupplier() *Supplier {
	return createLivingAttributes().Build()
}

// suppliers is the port of DefaultAttributes.SUPPLIERS: the EntityType-name -> Supplier table. Keyed
// by the entity registry name (data/entity.Entity.Name, e.g. "witch"), which is the stable identity
// the live entity carries. Only the entities Sulfur spawns are registered; getSupplier returns
// (nil, false) for an unregistered type (vanilla's DefaultAttributes.getSupplier would throw a
// missing-supplier error — here the caller decides, and a structure-spawn of an unported type simply
// gets no attribute map, which is the faithful "no DefaultAttributes registered" outcome).
//
// Built once at package init (the singletons are immutable after Build), mirroring the static
// SUPPLIERS map. Read-only after init, so concurrent reads from the tick are safe with no lock.
var suppliers = map[string]*Supplier{
	"player":     playerSupplier(),
	"witch":      witchSupplier(),
	"cat":        catSupplier(),
	"villager":   villagerSupplier(),
	"zombie":     zombieSupplier(),
	"silverfish": silverfishSupplier(),
	// SUB-ATTRIB coverage fix (Phase 23): the common animals/monsters a plugin would plausibly
	// spawn, each a 1:1 jar copy of that type's createAttributes() (verified bytecode this session).
	"pig":      pigSupplier(),
	"cow":      cowSupplier(),
	"sheep":    sheepSupplier(),
	"chicken":  chickenSupplier(),
	"skeleton": skeletonSupplier(),
	"creeper":  creeperSupplier(),
	"spider":   spiderSupplier(),
	// MOB-NEUT-01 (Phase 36): the wolf — the one missing supplier (the LAST v5 mob). A 1:1 jar copy of
	// Wolf.createAttributes() (verified bytecode this session). The tamed MAX_HEALTH 40 bump is a runtime
	// setTame side-effect (Plan B), not the base supplier.
	"wolf": wolfSupplier(),
	// MOB-VARIANT (Task #9): the zero-subsystem variants — Husk (extends Zombie) and Mooshroom (extends
	// AbstractCow), each a 1:1 inherited-attribute copy of its parent (no createAttributes override).
	"husk":      huskSupplier(),
	"mooshroom": mooshroomSupplier(),
	"rabbit":    rabbitSupplier(),
	"enderman":  endermanSupplier(),
	"fox":       foxSupplier(),
	// MOB-CUBE (SulfurCube): the size-scaled cube-mob base (createMobAttributes + TEMPT_RANGE 8.0). setSize
	// overrides MAX_HEALTH (4*size) + MOVEMENT_SPEED (0.2+0.1*size) at runtime. Cite SulfurCube.createSulfurCubeAttributes.
	"sulfur_cube": sulfurCubeSupplier(),
	// happy_ghast (Task): HappyGhast is a flying Animal; happyGhastSupplier is a 1:1 copy of
	// HappyGhast.createAttributes (FLYING_SPEED + CAMERA_DISTANCE + the 16.0 tempt/follow range).
	"happy_ghast": happyGhastSupplier(),
	// GHAST (Task): the hostile flying Ghast (Ghast.createAttributes: Mob.createMobAttributes +
	// MAX_HEALTH 10.0 + FOLLOW_RANGE 100.0 + CAMERA_DISTANCE 8.0 + FLYING_SPEED 0.06). Keyed by its
	// registry name so NewMapForEntity resolves it (the ghast MobCategory is monster, so the living
	// fallback would also apply -- but the dedicated supplier gives the faithful 10/100 values).
	"ghast": ghastSupplier(),
	// BLAZE (Task): the nether hostile that drops blaze rods. Blaze.createAttributes: Monster
	// .createMonsterAttributes + ATTACK_DAMAGE 6.0 + MOVEMENT_SPEED 0.23 + FOLLOW_RANGE 48.0 (MAX_HEALTH
	// is the createLivingAttributes default 20.0). Keyed by its registry name so NewMapForEntity resolves it.
	"blaze": blazeSupplier(),
	// MOB-PREY (Task #9): the 3 prey mobs. Endermite (Monster), Turtle + Ocelot (Animal), each a 1:1 jar
	// copy of its createAttributes (verified bytecode this session).
	"endermite": endermiteSupplier(),
	"turtle":    turtleSupplier(),
	"ocelot":    ocelotSupplier(),
	// RAIDER (Task): the 4 RaiderType mobs. All Monster.createMonsterAttributes with the jar overrides.
	"pillager":   pillagerSupplier(),
	"vindicator": vindicatorSupplier(),
	"evoker":     evokerSupplier(),
	"ravager":    ravagerSupplier(),
	// VEX + FANGS (Task): the Vex is the evoker's summoned flying Monster (Vex.createAttributes:
	// MAX_HEALTH 14.0 + ATTACK_DAMAGE 4.0). EvokerFangs is a NON-living projectile ("misc"), so it has
	// NO supplier -- it falls to nil (like arrow/potion) via isLivingType, the faithful outcome.
	"vex": vexSupplier(),
	// IRON GOLEM (Task): the village defender. IronGolem builds on Mob.createMobAttributes (not Monster/
	// Animal); ironGolemSupplier is a 1:1 copy of IronGolem.createAttributes. Keyed by registry name so
	// NewMapForEntity resolves it FIRST (before the living-fallback) even though the golem's MobCategory is
	// "misc" — a "misc"-category LivingEntity with a dedicated supplier still gets its faithful attributes.
	"iron_golem": ironGolemSupplier(),
}

// livingCategories is the set of data/entity.Entity.Type values that correspond to a vanilla
// MobCategory whose members are LivingEntity (and therefore carry a DefaultAttributes supplier). It
// mirrors vanilla's MobCategory enum minus MISC: every living mob falls in one of these categories,
// while "misc" (items, projectiles, boats, area-effect clouds, …) are non-living Entity subclasses
// with NO attribute map. Derived from the distinct Type values in data/entity (creature, monster,
// ambient, axolotls, water_creature, water_ambient, underground_water_creature) — "misc" is the only
// non-living category and is deliberately ABSENT here.
var livingCategories = map[string]bool{
	"creature":                   true,
	"monster":                    true,
	"ambient":                    true,
	"axolotls":                   true,
	"water_creature":             true,
	"water_ambient":              true,
	"underground_water_creature": true,
}

// entityByName indexes data/entity.ByID by registry name, so NewMapForEntity can resolve a type's
// MobCategory (Type) from its name to gate the living-fallback. Built once at init from the generated
// table (read-only after init; concurrent tick reads are safe with no lock).
var entityByName = func() map[string]*entity.Entity {
	m := make(map[string]*entity.Entity, len(entity.ByID))
	for _, e := range entity.ByID {
		m[e.Name] = e
	}
	return m
}()

// isLivingType reports whether the entity registry name denotes a LIVING entity type (a member of a
// living MobCategory), which in vanilla always has a DefaultAttributes supplier. An unknown name, or a
// "misc"-category type (item/arrow/boat/…), is NOT living and gets no fallback attribute map.
func isLivingType(entityName string) bool {
	e, ok := entityByName[entityName]
	if !ok {
		return false
	}
	return livingCategories[e.Type]
}

// GetSupplier is the port of DefaultAttributes.getSupplier(EntityType): the default attribute
// supplier for an entity type (keyed by registry name). Returns (nil, false) when no supplier is
// registered for the type — the caller (NewMapForEntity) treats that as "this entity has no
// attribute map yet".
func GetSupplier(entityName string) (*Supplier, bool) {
	s, ok := suppliers[entityName]
	return s, ok
}

// HasSupplier is the port of DefaultAttributes.hasSupplier(EntityType).
func HasSupplier(entityName string) bool {
	_, ok := suppliers[entityName]
	return ok
}

// NewMapForEntity builds a per-entity AttributeMap for an entity type (by registry name), backed by
// that type's DefaultAttributes supplier. This is the single constructor the live entity uses to
// attach its attribute map at spawn.
//
// SUB-ATTRIB coverage fix (Phase 23): vanilla's DefaultAttributes registers a supplier for EVERY
// LivingEntity type, so a pig (or any living mob) always gets faithful, type-correct attributes. To
// match that guarantee without porting every single type up front:
//
//   - A type with a dedicated supplier (the table above) gets that supplier's exact createAttributes
//     values (pig -> Pig.createAttributes, etc.).
//   - A LIVING type with NO dedicated supplier falls back to livingFallbackSupplier()
//     (LivingEntity.createLivingAttributes — the base set every living entity has), so the map is
//     NEVER nil for a living type. This is the operator-mandated "todos deberían estar disponibles".
//   - A NON-LIVING type ("misc": item/arrow/boat/…) returns nil — it has no attributes in vanilla, and
//     the caller's nil-check degrades to "no attribute map", exactly the faithful outcome.
func NewMapForEntity(entityName string) *Map {
	if supplier, ok := GetSupplier(entityName); ok {
		return NewMap(supplier)
	}
	if isLivingType(entityName) {
		// Faithful fallback: every LivingEntity has at least the base createLivingAttributes set.
		return NewMap(livingFallbackSupplier())
	}
	// Non-living (or unknown) type: no attributes (vanilla has no DefaultAttributes supplier).
	return nil
}
