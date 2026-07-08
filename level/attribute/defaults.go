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

// wanderingTraderSupplier is the port of the WANDERING_TRADER DefaultAttributes registration. Unlike
// Villager (which OVERRIDES MOVEMENT_SPEED to 0.5), the wandering trader registers PLAIN
// Mob.createMobAttributes() with NO override (VERIFIED CFR DefaultAttributes: EntityType.WANDERING_TRADER
// -> Mob.createMobAttributes().build()). So it carries MAX_HEALTH 20.0 (living default) and the
// MOVEMENT_SPEED REGISTRATION DEFAULT 0.7 (createLivingAttributes .add(MOVEMENT_SPEED), no value) -- the
// jar bytecode is authoritative over the 0.5 figure. WanderingTrader.createAttributes does not exist
// (AbstractVillager has none either); the supplier lives entirely in the DefaultAttributes map.
func wanderingTraderSupplier() *Supplier {
	return createMobAttributes().
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

// enderDragonSupplier is the port of EnderDragon.createAttributes(): Mob.createMobAttributes() then
// .add(MAX_HEALTH 200.0).add(CAMERA_DISTANCE 16.0). The EnderDragon builds on createMobAttributes (NOT
// Monster -- its damage is the melee/fireball phase logic, not an ATTACK_DAMAGE attribute), so it has NO
// ATTACK_DAMAGE. MAX_HEALTH 200.0 is the boss health; CAMERA_DISTANCE 16.0 the render/hitbox distance.
// Cite net.minecraft.world.entity.boss.enderdragon.EnderDragon.createAttributes (javap this session:
// Mob.createMobAttributes().add(MAX_HEALTH, 200.0).add(CAMERA_DISTANCE, 16.0)).
func enderDragonSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 200.0).
		AddValue(CameraDistance, 16.0).
		Build()
}

// witherSupplier is the port of WitherBoss.createAttributes(): Monster.createMonsterAttributes() then
// .add(MAX_HEALTH 300.0).add(MOVEMENT_SPEED 0.6).add(FLYING_SPEED 0.6).add(FOLLOW_RANGE 40.0).add(ARMOR 4.0).
// It builds on createMonsterAttributes (so it carries the ATTACK_DAMAGE registration default 2.0). MAX_HEALTH
// 300.0 is the boss health; MOVEMENT_SPEED/FLYING_SPEED are the exact ldc2_w 0.6000000238418579 double. NO
// KNOCKBACK_RESISTANCE override (the jar's createAttributes does not add one -- it inherits the
// createLivingAttributes default 0.0). Cite net.minecraft.world.entity.boss.wither.WitherBoss.createAttributes
// (javap this task: createMonsterAttributes, ldc2_w 300.0d MAX_HEALTH, 0.6000000238418579d MOVEMENT_SPEED,
// 0.6000000238418579d FLYING_SPEED, 40.0d FOLLOW_RANGE, 4.0d ARMOR).
func witherSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 300.0).
		AddValue(MovementSpeed, 0.6000000238418579).
		AddValue(FlyingSpeed, 0.6000000238418579).
		AddValue(FollowRange, 40.0).
		AddValue(Armor, 4.0).
		Build()
}

// wardenSupplier is the port of Warden.createAttributes(): Monster.createMonsterAttributes() then
// .add(MAX_HEALTH 500.0).add(MOVEMENT_SPEED 0.30000001192092896).add(KNOCKBACK_RESISTANCE 1.0).add(
// ATTACK_KNOCKBACK 1.5).add(ATTACK_DAMAGE 30.0).add(FOLLOW_RANGE 24.0). The builder ORDER is exactly
// MAX_HEALTH, MOVEMENT_SPEED, KNOCKBACK_RESISTANCE, ATTACK_KNOCKBACK, ATTACK_DAMAGE, FOLLOW_RANGE
// (VERIFIED javap net.minecraft.world.entity.monster.warden.Warden.createAttributes: createMonster
// Attributes, ldc2_w 500.0d MAX_HEALTH, 0.30000001192092896d MOVEMENT_SPEED, dconst_1 KNOCKBACK_
// RESISTANCE, 1.5d ATTACK_KNOCKBACK, 30.0d ATTACK_DAMAGE, 24.0d FOLLOW_RANGE). The MOVEMENT_SPEED is
// the exact float64 bits of the 0.3f-widened-to-double 26.2 literal; ATTACK_KNOCKBACK 1.5 overrides
// the createLivingAttributes registration default 0.0. Cite Warden.createAttributes.
func wardenSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 500.0).
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(KnockbackResistance, 1.0).
		AddValue(AttackKnockback, 1.5).
		AddValue(AttackDamage, 30.0).
		AddValue(FollowRange, 24.0).
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

// phantomSupplier is the port of Phantom's DefaultAttributes registration: Monster.createMonsterAttributes()
// with NO extra overrides (MAX_HEALTH is the createLivingAttributes default 20.0; ATTACK_DAMAGE is the
// createMonsterAttributes registration default 2.0). updatePhantomSizeInfo() OVERRIDES ATTACK_DAMAGE to
// 6 + phantomSize at runtime (setPhantomSize in phantom.go), so the base 2.0 here is the pre-size-info
// value the size-info write replaces. Cite DefaultAttributes.PHANTOM -> Monster.createMonsterAttributes()
// (javap this task: PHANTOM entry is `Monster.createMonsterAttributes().build()`, no per-attribute add).
func phantomSupplier() *Supplier {
	return createMonsterAttributes().Build()
}

// shulkerSupplier is the port of Shulker.createAttributes(): Mob.createMobAttributes() + MAX_HEALTH
// 30.0 (jar: net.minecraft.world.entity.monster.Shulker.createAttributes ==
// createMobAttributes().add(MAX_HEALTH, 30.0d)). ARMOR is the createLivingAttributes registration
// default 0.0 (Shulker adds NO base ARMOR); the +20 "covered" ARMOR is a transient modifier
// (COVERED_ARMOR_MODIFIER = Identifier "covered", 20.0, ADD_VALUE) applied at spawn + removed while
// open (Shulker.onDataUpdated / setRawPeekAmount branch). Cite Shulker.createAttributes.
func shulkerSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 30.0).
		Build()
}

// magmaCubeSupplier is the port of MagmaCube.createAttributes(): Monster.createMonsterAttributes()
// + MOVEMENT_SPEED 0.20000000298023224 (jar: net.minecraft.world.entity.monster.cubemob.MagmaCube
// .createAttributes == createMonsterAttributes().add(MOVEMENT_SPEED, 0.20000000298023224d)). MAX_HEALTH
// stays the createLivingAttributes default 20.0 in the base supplier; ATTACK_DAMAGE the Monster default
// 2.0 -- BOTH are OVERRIDDEN at runtime by setSize (AbstractCubeMob.setSize: MAX_HEALTH = size*size,
// MOVEMENT_SPEED base = 0.2 + 0.1*size; MagmaCube.setSize: ATTACK_DAMAGE = size, ARMOR = size*3). Cite
// MagmaCube.createAttributes.
func magmaCubeSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MovementSpeed, 0.20000000298023224).
		Build()
}

// slimeSupplier is the port of the SLIME DefaultAttributes registration: DefaultAttributes maps
// EntityType.SLIME -> Monster.createMonsterAttributes().build() (jar: DefaultAttributes.<clinit>
// put(SLIME, Monster.createMonsterAttributes().build())). UNLIKE MagmaCube, Slime adds NO MOVEMENT_SPEED
// in the supplier -- MOVEMENT_SPEED comes from createLivingAttributes (registered, default 0.0) and is
// set to 0.2+0.1*size at runtime by AbstractCubeMob.setSize. MAX_HEALTH stays the createLivingAttributes
// default 20.0 in the supplier and ATTACK_DAMAGE the createMonsterAttributes default 2.0 -- BOTH are
// OVERRIDDEN at runtime by setSize (AbstractCubeMob.setSize: MAX_HEALTH = size*size; Slime.setSize:
// ATTACK_DAMAGE = size). Slime adds NO ARMOR (MagmaCube's size*3 is MagmaCube-only). FOLLOW_RANGE is the
// createMobAttributes 16.0 (the target selector range). Cite DefaultAttributes(SLIME) + Slime.setSize.
func slimeSupplier() *Supplier {
	return createMonsterAttributes().
		Build()
}

// striderSupplier is the port of Strider.createAttributes(): Animal.createAnimalAttributes() +
// MOVEMENT_SPEED 0.17499999701976776 (jar: net.minecraft.world.entity.monster.Strider.createAttributes
// == createAnimalAttributes().add(MOVEMENT_SPEED, 0.17499999701976776d)). MAX_HEALTH is the
// createLivingAttributes default 20.0 (Strider has NO MAX_HEALTH override); FOLLOW_RANGE the
// createMobAttributes default 16.0. The suffocating cold-state applies a transient MOVEMENT_SPEED
// modifier at runtime (SUFFOCATING_MODIFIER -0.34 ADD_MULTIPLIED_BASE). Cite Strider.createAttributes.
func striderSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MovementSpeed, 0.17499999701976776).
		Build()
}

// witherSkeletonSupplier is the port of WitherSkeleton's attribute supplier. WitherSkeleton has NO
// createAttributes override -- it inherits AbstractSkeleton.createAttributes == Monster.createMonsterAttributes
// + MOVEMENT_SPEED 0.25 (identical to skeletonSupplier). MAX_HEALTH is the createLivingAttributes default
// 20.0; FOLLOW_RANGE the createMonsterAttributes 16.0. ATTACK_DAMAGE is the createMonsterAttributes default
// 2.0 in the supplier -- WitherSkeleton.finalizeSpawn OVERRIDES it to 4.0 at runtime (spawnWitherSkeleton).
// Cite AbstractSkeleton.createAttributes + WitherSkeleton.finalizeSpawn.
func witherSkeletonSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MovementSpeed, 0.25).
		Build()
}

// hoglinSupplier is the port of Hoglin.createAttributes(): Monster.createMonsterAttributes() +
// MAX_HEALTH 40.0 + MOVEMENT_SPEED 0.30000001192092896 + KNOCKBACK_RESISTANCE 0.6000000238418579 +
// ATTACK_KNOCKBACK 1.0 + ATTACK_DAMAGE 6.0 (jar: net.minecraft.world.entity.monster.hoglin.Hoglin
// .createAttributes -- ldc2_w 40.0d, 0.30000001192092896d, 0.6000000238418579d, dconst_1, 6.0d). The
// ATTACK_DAMAGE 6.0 is the ADULT value; Hoglin.ageBoundaryReached sets a baby to 0.5 at runtime
// (setHoglinAgeAttack). Cite Hoglin.createAttributes.
func hoglinSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 40.0).
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(KnockbackResistance, 0.6000000238418579).
		AddValue(AttackKnockback, 1.0).
		AddValue(AttackDamage, 6.0).
		Build()
}

// piglinSupplier is the port of Piglin.createAttributes(): Monster.createMonsterAttributes() +
// MAX_HEALTH 16.0 + MOVEMENT_SPEED 0.3499999940395355 (0.35f widened) + ATTACK_DAMAGE 5.0. Cite
// net.minecraft.world.entity.monster.piglin.Piglin.createAttributes (javap this session:
// createMonsterAttributes, ldc2_w 16.0d MAX_HEALTH, 0.3499999940395355d MOVEMENT_SPEED, 5.0d ATTACK_DAMAGE).
// The MOVEMENT_SPEED literal is the vanilla float-widened double, preserved bit-for-bit.
func piglinSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 16.0).
		AddValue(MovementSpeed, 0.3499999940395355).
		AddValue(AttackDamage, 5.0).
		Build()
}

// zombifiedPiglinSupplier is the port of ZombifiedPiglin.createAttributes(): Zombie.createAttributes()
// (createMonsterAttributes + FOLLOW_RANGE 35.0 + MOVEMENT_SPEED 0.23000000417232513 + ATTACK_DAMAGE 3.0 +
// ARMOR 2.0 + SPAWN_REINFORCEMENTS_CHANCE default 0.0) then .add(SPAWN_REINFORCEMENTS_CHANCE 0.0)
// .add(MOVEMENT_SPEED 0.23000000417232513) .add(ATTACK_DAMAGE 5.0). MAX_HEALTH is the createLivingAttributes
// default 20.0. So it folds MAX_HEALTH 20, FOLLOW_RANGE 35, MOVEMENT_SPEED 0.23, ATTACK_DAMAGE 5, ARMOR 2.
// SPAWN_REINFORCEMENTS_CHANCE (0.0) is the vanilla registration default and is not a wired attribute in v1
// (no reinforcement-spawn subsystem exists); it is omitted here exactly as zombieSupplier omits it, its
// value being the default 0.0 -- structured to become a real .AddValue(SpawnReinforcementsChance, 0.0) when
// the attribute is registered. Cite ZombifiedPiglin.createAttributes.
func zombifiedPiglinSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(FollowRange, 35.0).
		AddValue(MovementSpeed, 0.23000000417232513).
		AddValue(AttackDamage, 5.0).
		AddValue(Armor, 2.0).
		Build()
}

// zoglinSupplier is the port of Zoglin.createAttributes(): Monster.createMonsterAttributes() + MAX_HEALTH
// 40.0 + MOVEMENT_SPEED 0.30000001192092896 + KNOCKBACK_RESISTANCE 0.6000000238418579 + ATTACK_KNOCKBACK 1.0
// + ATTACK_DAMAGE 6.0. ATTACK_DAMAGE 6.0 is the ADULT value; a baby is 0.5 at runtime (setBaby ->
// setZoglinAgeAttack). Cite Zoglin.createAttributes.
func zoglinSupplier() *Supplier {
	return createMonsterAttributes().
		AddValue(MaxHealth, 40.0).
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(KnockbackResistance, 0.6000000238418579).
		AddValue(AttackKnockback, 1.0).
		AddValue(AttackDamage, 6.0).
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

// beeSupplier is the port of Bee.createAttributes() : Animal.createAnimalAttributes() + MAX_HEALTH
// 10.0 + FLYING_SPEED 0.6000000238418579 + MOVEMENT_SPEED 0.30000001192092896 + ATTACK_DAMAGE 2.0
// (jar: net.minecraft.world.entity.animal.bee.Bee.createAttributes -- javap this session: ldc2_w
// 10.0d MAX_HEALTH, 0.6000000238418579d FLYING_SPEED, 0.30000001192092896d MOVEMENT_SPEED, 2.0d
// ATTACK_DAMAGE). NO FOLLOW_RANGE override -- FOLLOW_RANGE stays the createMobAttributes 16.0. The
// MOVEMENT_SPEED + FLYING_SPEED literals are the vanilla float-widened doubles, preserved bit-for-bit.
func beeSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 10.0).
		AddValue(FlyingSpeed, 0.6000000238418579).
		AddValue(MovementSpeed, 0.30000001192092896).
		AddValue(AttackDamage, 2.0).
		Build()
}

// goatSupplier is the port of Goat.createAttributes() : Animal.createAnimalAttributes() + MAX_HEALTH
// 10.0 + MOVEMENT_SPEED 0.20000000298023224 + ATTACK_DAMAGE 2.0 (jar:
// net.minecraft.world.entity.animal.goat.Goat.createAttributes -- javap this session: ldc2_w 10.0d
// MAX_HEALTH, 0.20000000298023224d MOVEMENT_SPEED, 2.0d ATTACK_DAMAGE). The MOVEMENT_SPEED literal is
// the vanilla float-widened double, preserved bit-for-bit. NOTE the "screaming goat runs faster" is
// NOT a base-attribute override (there is only ONE MOVEMENT_SPEED add in createAttributes); the
// ram/long-jump speed is the brain LongJump machinery, DEFERRED.
func goatSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 10.0).
		AddValue(MovementSpeed, 0.20000000298023224).
		AddValue(AttackDamage, 2.0).
		Build()
}

// frogSupplier is the port of Frog.createAttributes() : Animal.createAnimalAttributes() +
// MOVEMENT_SPEED 1.0 (dconst_1) + MAX_HEALTH 10.0 + ATTACK_DAMAGE 10.0 + STEP_HEIGHT 1.0 (dconst_1)
// (jar: net.minecraft.world.entity.animal.frog.Frog.createAttributes -- javap this session: dconst_1
// MOVEMENT_SPEED, ldc2_w 10.0d MAX_HEALTH, ldc2_w 10.0d ATTACK_DAMAGE, dconst_1 STEP_HEIGHT). The frog
// MOVEMENT_SPEED 1.0 is large because the jump-heavy navigation reads it through a small per-jump
// scale (a hopping mob, like the rabbit); ATTACK_DAMAGE 10.0 is the tongue-eat kill (a slime/magma-cube
// is one-shot). STEP_HEIGHT 1.0 OVERRIDES the base createLivingAttributes default 0.6 (a frog hops a
// full block, like the turtle/enderman).
func frogSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MovementSpeed, 1.0).
		AddValue(MaxHealth, 10.0).
		AddValue(AttackDamage, 10.0).
		AddValue(StepHeight, 1.0).
		Build()
}

// camelSupplier is the port of Camel.createAttributes() : AbstractHorse.createBaseHorseAttributes() +
// MAX_HEALTH 32.0 + MOVEMENT_SPEED 0.09000000357627869 + JUMP_STRENGTH 0.41999998688697815 + STEP_HEIGHT
// 1.5 (jar: net.minecraft.world.entity.animal.camel.Camel.createAttributes -- javap this session: ldc2_w
// 32.0d MAX_HEALTH, 0.09000000357627869d MOVEMENT_SPEED, 0.41999998688697815d JUMP_STRENGTH, 1.5d
// STEP_HEIGHT). createBaseHorseAttributes = Animal.createAnimalAttributes() + JUMP_STRENGTH 0.7 + MAX_HEALTH
// 53.0 + MOVEMENT_SPEED 0.22499999403953552 + STEP_HEIGHT 1.0 + SAFE_FALL_DISTANCE 6.0 +
// FALL_DAMAGE_MULTIPLIER 0.5 (verified AbstractHorse.createBaseHorseAttributes this session). Under
// buildKeepingLast the Camel overrides win: the FINAL supplier is MAX_HEALTH 32.0, MOVEMENT_SPEED
// 0.09000000357627869, STEP_HEIGHT 1.5, SAFE_FALL_DISTANCE 6.0 (from the horse base, un-overridden), plus
// the createAnimalAttributes base (TEMPT_RANGE 10.0, FOLLOW_RANGE 16.0). JUMP_STRENGTH (0.42) and
// FALL_DAMAGE_MULTIPLIER (0.5) are NOT registered attributes in Sulfur (no consumer -- the CITED
// non-gameplay omission in the file header); each slots in as one .AddValue line the moment its consumer
// lands, never baked away. The sit/stand + dash + 2-seat rideable are the DEFERRED behavior layer
// (camel.go). Cite Camel.createAttributes + AbstractHorse.createBaseHorseAttributes.
func camelSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(SafeFallDistance, 6.0).              // createBaseHorseAttributes SAFE_FALL_DISTANCE 6.0 (over the living 3.0)
		AddValue(MaxHealth, 32.0).                    // Camel override (over the horse-base 53.0)
		AddValue(MovementSpeed, 0.09000000357627869). // Camel override (over the horse-base 0.225)
		AddValue(StepHeight, 1.5).                    // Camel override (over the horse-base 1.0)
		Build()
}

// pandaSupplier is the port of Panda.createAttributes() : Animal.createAnimalAttributes() +
// MOVEMENT_SPEED 0.15000000596046448 + ATTACK_DAMAGE 6.0 (jar:
// net.minecraft.world.entity.animal.panda.Panda.createAttributes -- javap this session: getstatic
// MOVEMENT_SPEED, ldc2_w 0.15000000596046448d, getstatic ATTACK_DAMAGE, ldc2_w 6.0d). There is NO
// MAX_HEALTH override, so MAX_HEALTH stays the createLivingAttributes registration default 20.0. The
// MOVEMENT_SPEED literal is the vanilla float-widened double, preserved bit-for-bit. NOTE the per-
// variant divergence (a WEAK panda's MAX_HEALTH 10, a LAZY panda's MOVEMENT_SPEED 0.07) is NOT a
// supplier override -- it is the live-instance Panda.setAttributes() setBaseValue applied per-entity at
// spawn/breed (panda.go), the two-tier local-divergence model (like the horse randomize). Cite
// Panda.createAttributes.
func pandaSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MovementSpeed, 0.15000000596046448).
		AddValue(AttackDamage, 6.0).
		Build()
}

// snowGolemSupplier is the port of SnowGolem.createAttributes() : Mob.createMobAttributes() +
// MAX_HEALTH 4.0 + MOVEMENT_SPEED 0.20000000298023224 (jar:
// net.minecraft.world.entity.animal.golem.SnowGolem.createAttributes -- javap this session: Mob.create
// MobAttributes, getstatic MAX_HEALTH, ldc2_w 4.0d, getstatic MOVEMENT_SPEED, ldc2_w 0.20000000298023224d).
// Built on Mob.createMobAttributes (AbstractGolem has NO createAttributes override) -- NOT Animal/Monster,
// so NO TEMPT_RANGE / base ATTACK_DAMAGE. The MOVEMENT_SPEED literal is the vanilla float-widened double.
// SnowGolem's registry Type is "misc" (like the iron_golem), so a dedicated supplier is REQUIRED -- a misc
// type gets NO living-fallback map, and without this the golem would read the bare registration defaults.
// Cite SnowGolem.createAttributes + AbstractGolem (no override).
func snowGolemSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 4.0).
		AddValue(MovementSpeed, 0.20000000298023224).
		Build()
}

// horseBaseAttributes is the port of AbstractHorse.createBaseHorseAttributes():
// Animal.createAnimalAttributes() + JUMP_STRENGTH 0.7 + MAX_HEALTH 53.0 + MOVEMENT_SPEED
// 0.22499999403953552 + STEP_HEIGHT 1.0 + SAFE_FALL_DISTANCE 6.0 + FALL_DAMAGE_MULTIPLIER 0.5 (verified
// javap AbstractHorse.createBaseHorseAttributes this session: ldc2_w 0.7d JUMP_STRENGTH, 53.0d MAX_HEALTH,
// 0.22499999403953552d MOVEMENT_SPEED, dconst_1 STEP_HEIGHT, 6.0d SAFE_FALL_DISTANCE, 0.5d
// FALL_DAMAGE_MULTIPLIER). These are the pre-randomize registration DEFAULTS -- Horse/Donkey/Mule/Llama
// then OVERWRITE the base values per-entity at finalizeSpawn via randomizeAttributes (setBaseValue on a
// live AttributeInstance; horse.go), which is the two-tier local-divergence model (a per-entity instance
// mutation, NOT a supplier change). JUMP_STRENGTH (0.7) and FALL_DAMAGE_MULTIPLIER (0.5) are NOT registered
// attributes in Sulfur (no consumer -- the same CITED non-gameplay omission as camelSupplier); the
// jump-launch reads JUMP_STRENGTH via the horseJumpStrength per-entity field (horse.go) and slots into a
// real .AddValue read the moment the attribute lands, never baked away. STEP_HEIGHT 1.0 is the living
// default so it is not re-added. Cite AbstractHorse.createBaseHorseAttributes.
func horseBaseAttributes() *Builder {
	return createAnimalAttributes().
		AddValue(SafeFallDistance, 6.0).             // createBaseHorseAttributes SAFE_FALL_DISTANCE 6.0 (over the living 3.0)
		AddValue(MaxHealth, 53.0).                   // createBaseHorseAttributes MAX_HEALTH 53.0 (pre-randomize default)
		AddValue(MovementSpeed, 0.22499999403953552) // createBaseHorseAttributes MOVEMENT_SPEED (float-widened)
}

// horseSupplier is the port of Horse.createAttributes(): Horse has NO createAttributes override, so it uses
// AbstractHorse.createBaseHorseAttributes() directly (verified javap Horse this session: no createAttributes
// method present). The per-horse MAX_HEALTH (15..30) / MOVEMENT_SPEED (0.1125..0.3375) / JUMP_STRENGTH
// (0.4..1.0) randomization runs at finalizeSpawn (Horse.randomizeAttributes -> setBaseValue), re-expressed
// as the per-entity spawn draw in horse.go; the supplier here is the pre-randomize base.
func horseSupplier() *Supplier {
	return horseBaseAttributes().Build()
}

// chestedHorseSupplier is the port of AbstractChestedHorse.createBaseChestedHorseAttributes():
// createBaseHorseAttributes() + MOVEMENT_SPEED 0.17499999701976776 + JUMP_STRENGTH 0.5 (verified javap
// AbstractChestedHorse.createBaseChestedHorseAttributes this session: ldc2_w 0.17499999701976776d
// MOVEMENT_SPEED, 0.5d JUMP_STRENGTH). Donkey/Mule use it unchanged; Llama.createAttributes() also returns
// it unchanged. AbstractChestedHorse.randomizeAttributes randomizes ONLY MAX_HEALTH (15..30) at
// finalizeSpawn (horse.go) -- MOVEMENT_SPEED/JUMP_STRENGTH stay the chested-base values. The JUMP_STRENGTH
// 0.5 is cite-omitted (no registered attribute; read via the horseJumpStrength field in horse.go). Cite
// AbstractChestedHorse.createBaseChestedHorseAttributes.
func chestedHorseSupplier() *Supplier {
	return horseBaseAttributes().
		AddValue(MovementSpeed, 0.17499999701976776). // createBaseChestedHorseAttributes MOVEMENT_SPEED (over horse-base 0.225)
		Build()
}

// snifferSupplier is the port of Sniffer.createAttributes() : Animal.createAnimalAttributes() +
// MOVEMENT_SPEED 0.10000000149011612 + MAX_HEALTH 14.0 (jar:
// net.minecraft.world.entity.animal.sniffer.Sniffer.createAttributes -- javap this session:
// createAnimalAttributes, ldc2_w 0.10000000149011612d MOVEMENT_SPEED, 14.0d MAX_HEALTH). The MOVEMENT_SPEED
// literal is the vanilla float-widened double, preserved bit-for-bit. The dig-for-seeds state machine is
// the DEFERRED behavior layer (sniffer.go). Cite Sniffer.createAttributes.
func snifferSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MovementSpeed, 0.10000000149011612).
		AddValue(MaxHealth, 14.0).
		Build()
}

// allaySupplier is the port of Allay.createAttributes() : Mob.createMobAttributes() (NOT Monster/Animal --
// the Allay builds on the mob base, so it has NO TEMPT_RANGE) + MAX_HEALTH 20.0 + FLYING_SPEED
// 0.10000000149011612 + MOVEMENT_SPEED 0.10000000149011612 + ATTACK_DAMAGE 2.0 (jar:
// net.minecraft.world.entity.animal.allay.Allay.createAttributes -- javap this session: createMobAttributes,
// ldc2_w 20.0d MAX_HEALTH, 0.10000000149011612d FLYING_SPEED, 0.10000000149011612d MOVEMENT_SPEED, 2.0d
// ATTACK_DAMAGE). The FLYING_SPEED + MOVEMENT_SPEED literals are the vanilla float-widened double, preserved
// bit-for-bit. Allay is a "misc"-category flyer; the item-pickup/follow-note behavior is the DEFERRED
// behavior layer (allay.go). Cite Allay.createAttributes.
func allaySupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 20.0).
		AddValue(FlyingSpeed, 0.10000000149011612).
		AddValue(MovementSpeed, 0.10000000149011612).
		AddValue(AttackDamage, 2.0).
		Build()
}

// axolotlSupplier is the port of Axolotl.createAttributes() : Animal.createAnimalAttributes() + MAX_HEALTH
// 14.0 + MOVEMENT_SPEED 1.0 (dconst_1) + ATTACK_DAMAGE 2.0 + STEP_HEIGHT 1.0 (dconst_1) (jar:
// net.minecraft.world.entity.animal.axolotl.Axolotl.createAttributes -- javap this session:
// createAnimalAttributes, ldc2_w 14.0d MAX_HEALTH, dconst_1 MOVEMENT_SPEED, 2.0d ATTACK_DAMAGE, dconst_1
// STEP_HEIGHT). MOVEMENT_SPEED 1.0 is large because the amphibious navigation reads it through the swim
// scale; STEP_HEIGHT 1.0 OVERRIDES the base createLivingAttributes default 0.6. The 5-color variant +
// play-dead are the DEFERRED behavior layer (axolotl.go). Cite Axolotl.createAttributes.
func axolotlSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 14.0).
		AddValue(MovementSpeed, 1.0).
		AddValue(AttackDamage, 2.0).
		AddValue(StepHeight, 1.0).
		Build()
}

// squidSupplier is the port of Squid.createAttributes() : Mob.createMobAttributes() + MAX_HEALTH 10.0
// (jar: net.minecraft.world.entity.animal.squid.Squid.createAttributes -- javap this session:
// createMobAttributes, ldc2_w 10.0d MAX_HEALTH). Squid extends AgeableWaterCreature -> PathfinderMob
// -> ... -> Mob (NOT Animal), so it has NO createAnimalAttributes TEMPT_RANGE. GlowSquid extends Squid
// with NO createAttributes override, so it inherits this exact supplier. Cite Squid.createAttributes.
func squidSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 10.0).
		Build()
}

// abstractFishSupplier is the port of AbstractFish.createAttributes() : Mob.createMobAttributes() +
// MAX_HEALTH 3.0 (jar: net.minecraft.world.entity.animal.fish.AbstractFish.createAttributes -- javap
// this session: createMobAttributes, ldc2_w 3.0d MAX_HEALTH). Cod, Salmon, TropicalFish (all extend
// AbstractSchoolingFish -> AbstractFish) and Pufferfish (extends AbstractFish) inherit this supplier
// unchanged -- none override createAttributes (javap-confirmed). AbstractFish builds on the Mob base
// (NOT Animal), so it has NO TEMPT_RANGE. Cite AbstractFish.createAttributes.
func abstractFishSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 3.0).
		Build()
}

// dolphinSupplier is the port of Dolphin.createAttributes() : Mob.createMobAttributes() + MAX_HEALTH
// 10.0 + MOVEMENT_SPEED 1.2000000476837158 (dconst-widened) + ATTACK_DAMAGE 3.0 (jar:
// net.minecraft.world.entity.animal.dolphin.Dolphin.createAttributes -- javap this session:
// createMobAttributes, ldc2_w 10.0d MAX_HEALTH, ldc2_w 1.2000000476837158d MOVEMENT_SPEED, ldc2_w 3.0d
// ATTACK_DAMAGE). The MOVEMENT_SPEED literal is the vanilla float-widened double, preserved bit-for-bit
// (a dolphin swims fast). Dolphin extends AgeableWaterCreature -> Mob (NOT Animal), so NO TEMPT_RANGE.
// The moistness-out-of-water damage + swim-with-player boost + treasure-find are the DEFERRED behavior
// layer (dolphin.go). Cite Dolphin.createAttributes.
func dolphinSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 10.0).
		AddValue(MovementSpeed, 1.2000000476837158).
		AddValue(AttackDamage, 3.0).
		Build()
}

// tadpoleSupplier is the port of Tadpole.createAttributes() : Animal.createAnimalAttributes() +
// MOVEMENT_SPEED 1.0 (dconst_1) + MAX_HEALTH 6.0 (jar:
// net.minecraft.world.entity.animal.frog.Tadpole.createAttributes -- javap this session:
// createAnimalAttributes, dconst_1 MOVEMENT_SPEED, ldc2_w 6.0d MAX_HEALTH). Tadpole is the only water
// mob built on Animal (it is the frog baby-stage that grows into a Frog), so it carries the
// createAnimalAttributes TEMPT_RANGE. MOVEMENT_SPEED 1.0 is large -- the swim navigation reads it
// through the swim scale. The age -> Frog growth (ticksToBeFrog 24000) is in tadpole.go. Cite
// Tadpole.createAttributes.
func tadpoleSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MovementSpeed, 1.0).
		AddValue(MaxHealth, 6.0).
		Build()
}

// parrotSupplier is the port of Parrot.createAttributes() : Animal.createAnimalAttributes() +
// MAX_HEALTH 6.0 + FLYING_SPEED 0.4000000059604645 + MOVEMENT_SPEED 0.20000000298023224 +
// ATTACK_DAMAGE 3.0 (jar: net.minecraft.world.entity.animal.parrot.Parrot.createAttributes -- javap
// this session: createAnimalAttributes, ldc2_w 6.0d MAX_HEALTH, 0.4000000059604645d FLYING_SPEED,
// 0.20000000298023224d MOVEMENT_SPEED, 3.0d ATTACK_DAMAGE). NO FOLLOW_RANGE override -- FOLLOW_RANGE
// stays the createMobAttributes 16.0. The FLYING_SPEED + MOVEMENT_SPEED literals are the vanilla
// float-widened doubles, preserved bit-for-bit. Cite Parrot.createAttributes.
func parrotSupplier() *Supplier {
	return createAnimalAttributes().
		AddValue(MaxHealth, 6.0).
		AddValue(FlyingSpeed, 0.4000000059604645).
		AddValue(MovementSpeed, 0.20000000298023224).
		AddValue(AttackDamage, 3.0).
		Build()
}

// batSupplier is the port of Bat.createAttributes() : Mob.createMobAttributes() + MAX_HEALTH 6.0 (jar:
// net.minecraft.world.entity.ambient.Bat.createAttributes -- javap this session: createMobAttributes,
// ldc2_w 6.0d MAX_HEALTH; no other override). Bat is an AmbientCreature -> Mob (NOT Animal), so it has
// NO TEMPT_RANGE and NO ATTACK_DAMAGE (a bat never attacks). MOVEMENT_SPEED stays the createLiving
// Attributes registration default 0.7 (the bat drifts via customServerAiStep deltaMovement steering,
// NOT the ground navigation); FOLLOW_RANGE the createMobAttributes 16.0. Cite Bat.createAttributes.
func batSupplier() *Supplier {
	return createMobAttributes().
		AddValue(MaxHealth, 6.0).
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
	"player":           playerSupplier(),
	"witch":            witchSupplier(),
	"cat":              catSupplier(),
	"villager":         villagerSupplier(),
	"wandering_trader": wanderingTraderSupplier(),
	"zombie":           zombieSupplier(),
	"silverfish":       silverfishSupplier(),
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
	// PHANTOM (Task): the flying night hostile that dive-bombs sleepless players. Phantom registers
	// Monster.createMonsterAttributes() (MAX_HEALTH 20.0 default, ATTACK_DAMAGE default 2.0);
	// updatePhantomSizeInfo sets ATTACK_DAMAGE = 6 + size at spawn (setPhantomSize). Keyed by its
	// registry name so NewMapForEntity resolves it (its MobCategory is monster). Cite DefaultAttributes.PHANTOM.
	"phantom": phantomSupplier(),
	// SHULKER (Task): the End box-turret hostile. Shulker.createAttributes: Mob.createMobAttributes
	// + MAX_HEALTH 30.0 (ARMOR base 0.0; the +20 "covered" ARMOR is a transient modifier applied while
	// closed). Keyed by its registry name so NewMapForEntity resolves it. Cite Shulker.createAttributes.
	"shulker": shulkerSupplier(),
	// MAGMA CUBE (Task): the nether cube-mob (MagmaCube.createAttributes: Monster.createMonsterAttributes
	// + MOVEMENT_SPEED 0.20000000298023224). setSize OVERRIDES MAX_HEALTH (size*size), MOVEMENT_SPEED
	// (0.2+0.1*size), ATTACK_DAMAGE (size), ARMOR (size*3) at runtime. Keyed by its registry name so
	// NewMapForEntity resolves it. Cite MagmaCube.createAttributes.
	"magma_cube": magmaCubeSupplier(),
	// SLIME (Task): the overworld/swamp cube-mob (Slime's DefaultAttributes registration:
	// Monster.createMonsterAttributes().build() -- NO MOVEMENT_SPEED add, unlike MagmaCube). setSize
	// OVERRIDES MAX_HEALTH (size*size), MOVEMENT_SPEED (0.2+0.1*size), ATTACK_DAMAGE (size) at runtime;
	// Slime adds NO ARMOR. Keyed by its registry name so NewMapForEntity resolves it. Cite
	// DefaultAttributes(SLIME) + Slime.setSize.
	"slime": slimeSupplier(),
	// STRIDER (Task): the nether lava-walking Animal (Strider.createAttributes: Animal.createAnimalAttributes
	// + MOVEMENT_SPEED 0.17499999701976776; MAX_HEALTH the createLivingAttributes default 20.0). The
	// suffocating cold-state applies a transient -0.34 ADD_MULTIPLIED_BASE MOVEMENT_SPEED modifier. Cite
	// Strider.createAttributes.
	"strider": striderSupplier(),
	// WITHER SKELETON (GAP, nether roster): WitherSkeleton shares AbstractSkeleton.createAttributes
	// (Monster + MOVEMENT_SPEED 0.25); ATTACK_DAMAGE 2.0->4.0 is the finalizeSpawn runtime override
	// (spawnWitherSkeleton). Keyed by registry name so NewMapForEntity resolves it.
	"wither_skeleton": witherSkeletonSupplier(),
	// HOGLIN (GAP, nether roster): Hoglin.createAttributes (Monster + MAX_HEALTH 40 + MOVEMENT_SPEED 0.3 +
	// KNOCKBACK_RESISTANCE 0.6 + ATTACK_KNOCKBACK 1.0 + ATTACK_DAMAGE 6.0). ATTACK_DAMAGE 6.0 is the adult
	// value; a baby is 0.5 at runtime (ageBoundaryReached). Keyed by registry name.
	"hoglin": hoglinSupplier(),
	// PIGLIN (Task): the flagship nether hostile. Piglin.createAttributes: Monster.createMonsterAttributes
	// + MAX_HEALTH 16.0 + MOVEMENT_SPEED 0.3499999940395355 + ATTACK_DAMAGE 5.0. Keyed by its registry name
	// so NewMapForEntity resolves it. Cite Piglin.createAttributes.
	"piglin": piglinSupplier(),
	// ZOMBIFIED PIGLIN (GAP): the neutral nether undead (a NeutralMob) + piglin conversion target.
	// ZombifiedPiglin.createAttributes = Zombie base + SPAWN_REINFORCEMENTS_CHANCE 0 + MOVEMENT_SPEED 0.23 +
	// ATTACK_DAMAGE 5 (folds MAX_HEALTH 20, FOLLOW_RANGE 35, ARMOR 2). Keyed by registry name. Cite
	// ZombifiedPiglin.createAttributes.
	"zombified_piglin": zombifiedPiglinSupplier(),
	// ZOGLIN (GAP): the terminal undead a hoglin becomes off-nether. Zoglin.createAttributes: MAX_HEALTH 40
	// + MOVEMENT_SPEED 0.3 + KNOCKBACK_RESISTANCE 0.6 + ATTACK_KNOCKBACK 1.0 + ATTACK_DAMAGE 6.0 (adult;
	// baby 0.5 at runtime). Keyed by registry name. Cite Zoglin.createAttributes.
	"zoglin": zoglinSupplier(),
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
	// ENDER DRAGON (Task): the boss of the_end. EnderDragon.createAttributes: Mob.createMobAttributes
	// + MAX_HEALTH 200.0 + CAMERA_DISTANCE 16.0 (no ATTACK_DAMAGE -- its damage is the phase melee/fireball
	// logic). Keyed by registry name so NewMapForEntity resolves the faithful 200hp. The dragon is a
	// "misc"-category LivingEntity, so the dedicated supplier (resolved FIRST) gives it the boss health.
	"ender_dragon": enderDragonSupplier(),
	// WITHER BOSS (Task): the nether-built boss. WitherBoss.createAttributes: Monster.createMonsterAttributes
	// + MAX_HEALTH 300.0 + MOVEMENT_SPEED 0.6 + FLYING_SPEED 0.6 + FOLLOW_RANGE 40.0 + ARMOR 4.0. Keyed by
	// registry name so NewMapForEntity resolves the faithful 300hp boss map (its category is monster).
	"wither": witherSupplier(),
	// WARDEN (Task): the sculk-summoned boss-tier hostile. Warden.createAttributes: Monster.create
	// MonsterAttributes + MAX_HEALTH 500 + MOVEMENT_SPEED 0.3 + KNOCKBACK_RESISTANCE 1.0 + ATTACK_
	// KNOCKBACK 1.5 + ATTACK_DAMAGE 30 + FOLLOW_RANGE 24. Keyed by registry name so NewMapForEntity
	// resolves the faithful 500hp warden map (its category is monster).
	"warden": wardenSupplier(),
	// BEE + GOAT + FROG (Task): the three passive animals. Each a 1:1 jar copy of its createAttributes
	// (verified bytecode this session). Bee (Animal + MAX_HEALTH 10 + FLYING_SPEED 0.6 + MOVEMENT_SPEED
	// 0.3 + ATTACK_DAMAGE 2), Goat (Animal + MAX_HEALTH 10 + MOVEMENT_SPEED 0.2 + ATTACK_DAMAGE 2), Frog
	// (Animal + MOVEMENT_SPEED 1.0 + MAX_HEALTH 10 + ATTACK_DAMAGE 10 + STEP_HEIGHT 1.0). Keyed by registry name.
	"bee":  beeSupplier(),
	"goat": goatSupplier(),
	"frog": frogSupplier(),
	// CAMEL + SNIFFER + ALLAY + AXOLOTL (Task): four passive animals. Each a 1:1 jar copy of its
	// createAttributes (verified bytecode this session). Camel (createBaseHorseAttributes + MAX_HEALTH 32 +
	// MOVEMENT_SPEED 0.09 + STEP_HEIGHT 1.5 + SAFE_FALL_DISTANCE 6; JUMP_STRENGTH/FALL_DAMAGE_MULTIPLIER
	// cite-omitted, no consumer), Sniffer (Animal + MOVEMENT_SPEED 0.1 + MAX_HEALTH 14), Allay (Mob +
	// MAX_HEALTH 20 + FLYING_SPEED 0.1 + MOVEMENT_SPEED 0.1 + ATTACK_DAMAGE 2), Axolotl (Animal + MAX_HEALTH
	// 14 + MOVEMENT_SPEED 1.0 + ATTACK_DAMAGE 2 + STEP_HEIGHT 1.0). Keyed by registry name.
	"camel":   camelSupplier(),
	"sniffer": snifferSupplier(),
	"allay":   allaySupplier(),
	"axolotl": axolotlSupplier(),
	// PARROT + BAT (Task): the flying passive Parrot (creature) + the ambient Bat. Each a 1:1 jar copy
	// of its createAttributes (verified bytecode this session). Parrot (Animal + MAX_HEALTH 6 + FLYING_SPEED
	// 0.4 + MOVEMENT_SPEED 0.2 + ATTACK_DAMAGE 3), Bat (Mob + MAX_HEALTH 6 only). Keyed by registry name.
	"parrot": parrotSupplier(),
	"bat":    batSupplier(),
	// HORSE FAMILY (Task): the AbstractHorse tree. Horse (createBaseHorseAttributes, pre-randomize base
	// MAX_HEALTH 53 / MOVEMENT_SPEED 0.225 / JUMP_STRENGTH 0.7); Donkey/Mule/Llama/TraderLlama
	// (createBaseChestedHorseAttributes: base + MOVEMENT_SPEED 0.175 + JUMP_STRENGTH 0.5). The per-entity
	// MAX_HEALTH/MOVEMENT_SPEED/JUMP_STRENGTH randomization runs at finalizeSpawn (horse.go) on the live
	// instance -- the supplier here is the pre-randomize base. Keyed by registry name. Cite Horse.create
	// Attributes (none -> createBaseHorseAttributes) + AbstractChestedHorse.createBaseChestedHorseAttributes.
	// PANDA + SNOW_GOLEM (Task): the Panda (bamboo-jungle Animal, gene/variant) + the SnowGolem
	// (snow-trail ranged golem). Each a 1:1 jar copy of its createAttributes (verified bytecode this
	// session). Panda (Animal + MOVEMENT_SPEED 0.15 + ATTACK_DAMAGE 6; MAX_HEALTH stays the living
	// default 20; the WEAK/LAZY per-variant setBaseValue is a live-instance divergence, panda.go).
	// SnowGolem (Mob + MAX_HEALTH 4 + MOVEMENT_SPEED 0.2; misc type, so a dedicated supplier is
	// required like iron_golem). Keyed by registry name.
	"panda":        pandaSupplier(),
	"snow_golem":   snowGolemSupplier(),
	"horse":        horseSupplier(),
	"donkey":       chestedHorseSupplier(),
	"mule":         chestedHorseSupplier(),
	"llama":        chestedHorseSupplier(),
	"trader_llama": chestedHorseSupplier(),
	// WATER MOBS (Task): the 8 aquatic mobs. Each a 1:1 jar copy of its createAttributes (verified
	// bytecode this session). Squid + GlowSquid (Mob + MAX_HEALTH 10; GlowSquid inherits Squid unchanged).
	// Cod/Salmon/Pufferfish/TropicalFish (all AbstractFish + MAX_HEALTH 3; none override createAttributes).
	// Dolphin (Mob + MAX_HEALTH 10 + MOVEMENT_SPEED 1.2 + ATTACK_DAMAGE 3). Tadpole (Animal + MOVEMENT_SPEED
	// 1.0 + MAX_HEALTH 6; the frog baby-stage). Keyed by registry name so NewMapForEntity resolves each.
	"squid":         squidSupplier(),
	"glow_squid":    squidSupplier(),
	"cod":           abstractFishSupplier(),
	"salmon":        abstractFishSupplier(),
	"pufferfish":    abstractFishSupplier(),
	"tropical_fish": abstractFishSupplier(),
	"dolphin":       dolphinSupplier(),
	"tadpole":       tadpoleSupplier(),
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
