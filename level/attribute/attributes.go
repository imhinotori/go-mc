package attribute

// attributes.go — the port of the gameplay-relevant slice of
// net.minecraft.world.entity.ai.attributes.Attributes.<clinit>: each registered RangedAttribute
// with its EXACT (default, min, max) from the jar bytecode (javap -c -p
// net.minecraft.world.entity.ai.attributes.Attributes, this session — every RangedAttribute ctor
// argument read directly from the ldc2_w/dconst constants in the static initializer).
//
// AUTHORITATIVE REGISTRATION DEFAULTS (RangedAttribute(name, default, min, max)):
//
//	max_health               default 20.0   min  1.0   max 1024.0
//	movement_speed           default  0.7   min  0.0   max 1024.0
//	attack_damage            default  2.0   min  0.0   max 2048.0
//	attack_knockback         default  0.0   min  0.0   max    5.0
//	attack_speed             default  4.0   min  0.0   max 1024.0
//	armor                    default  0.0   min  0.0   max   30.0
//	armor_toughness          default  0.0   min  0.0   max   20.0
//	knockback_resistance     default  0.0   min -2.0   max    1.0
//	max_absorption           default  0.0   min  0.0   max 2048.0
//	follow_range             default 32.0   min  0.0   max 2048.0
//	entity_interaction_range default  3.0   min  0.0   max   64.0
//	sweeping_damage_ratio    default  0.0   min  0.0   max    1.0
//	tempt_range              default 10.0   min  0.0   max 2048.0
//
// This is the gameplay subset the entities here (Player, Witch, Cat, Villager, Zombie, Silverfish)
// touch — combat (attack_damage/attack_speed/attack_knockback/sweeping_damage_ratio), defense
// (armor/armor_toughness/knockback_resistance/max_absorption), health (max_health), movement
// (movement_speed), AI ranging (follow_range/tempt_range/entity_interaction_range). The full vanilla
// registry has ~40 attributes (step_height, scale, gravity, jump_strength, ... — see
// LivingEntity.createLivingAttributes); the non-gameplay ones are intentionally omitted here because
// no consumer reads them yet. Each one slots in as a one-line registration when its consumer lands,
// with no change to the fold/map machinery. CITED: those omitted attributes equal their vanilla
// registration defaults the moment they are added — none is "baked away".
//
// The variables are package-level singletons (the vanilla Holder<Attribute> analogue): every
// Supplier/Map references the SAME *Attribute, so the attribute name is the stable key and identity.

var (
	// MaxHealth is Attributes.MAX_HEALTH (RangedAttribute "max_health", 20.0, 1.0, 1024.0).
	MaxHealth = NewRangedAttribute("max_health", 20.0, 1.0, 1024.0)

	// MovementSpeed is Attributes.MOVEMENT_SPEED (RangedAttribute "movement_speed", 0.7, 0.0, 1024.0).
	// NOTE the registration default is 0.7, but EVERY entity overrides it in its supplier (Player
	// 0.1, Witch 0.25, Cat 0.30000001192092896, Villager 0.5, Zombie 0.23000000417232513, Silverfish
	// 0.25) — the 0.7 default is essentially never the live value, but it is the faithful registry
	// default so it is preserved exactly.
	MovementSpeed = NewRangedAttribute("movement_speed", 0.7, 0.0, 1024.0)

	// AttackDamage is Attributes.ATTACK_DAMAGE (RangedAttribute "attack_damage", 2.0, 0.0, 2048.0).
	AttackDamage = NewRangedAttribute("attack_damage", 2.0, 0.0, 2048.0)

	// AttackKnockback is Attributes.ATTACK_KNOCKBACK (RangedAttribute "attack_knockback", 0.0, 0.0, 5.0).
	AttackKnockback = NewRangedAttribute("attack_knockback", 0.0, 0.0, 5.0)

	// AttackSpeed is Attributes.ATTACK_SPEED (RangedAttribute "attack_speed", 4.0, 0.0, 1024.0).
	AttackSpeed = NewRangedAttribute("attack_speed", 4.0, 0.0, 1024.0)

	// Armor is Attributes.ARMOR (RangedAttribute "armor", 0.0, 0.0, 30.0).
	Armor = NewRangedAttribute("armor", 0.0, 0.0, 30.0)

	// ArmorToughness is Attributes.ARMOR_TOUGHNESS (RangedAttribute "armor_toughness", 0.0, 0.0, 20.0).
	ArmorToughness = NewRangedAttribute("armor_toughness", 0.0, 0.0, 20.0)

	// KnockbackResistance is Attributes.KNOCKBACK_RESISTANCE (RangedAttribute "knockback_resistance",
	// 0.0, -2.0, 1.0). NOTE the min is -2.0 (a negative knockback resistance amplifies knockback).
	KnockbackResistance = NewRangedAttribute("knockback_resistance", 0.0, -2.0, 1.0)

	// MaxAbsorption is Attributes.MAX_ABSORPTION (RangedAttribute "max_absorption", 0.0, 0.0, 2048.0).
	MaxAbsorption = NewRangedAttribute("max_absorption", 0.0, 0.0, 2048.0)

	// FollowRange is Attributes.FOLLOW_RANGE (RangedAttribute "follow_range", 32.0, 0.0, 2048.0).
	// NOTE the REGISTRATION default is 32.0; Mob.createMobAttributes OVERRIDES it to 16.0 for the
	// generic mob (so most mobs start at 16.0, Zombie at 35.0). finalizeSpawn adds the
	// random-spawn-bonus modifier to THIS attribute.
	FollowRange = NewRangedAttribute("follow_range", 32.0, 0.0, 2048.0)

	// EntityInteractionRange is Attributes.ENTITY_INTERACTION_RANGE (RangedAttribute
	// "entity_interaction_range", 3.0, 0.0, 64.0).
	EntityInteractionRange = NewRangedAttribute("entity_interaction_range", 3.0, 0.0, 64.0)

	// SweepingDamageRatio is Attributes.SWEEPING_DAMAGE_RATIO (RangedAttribute "sweeping_damage_ratio",
	// 0.0, 0.0, 1.0).
	SweepingDamageRatio = NewRangedAttribute("sweeping_damage_ratio", 0.0, 0.0, 1.0)

	// TemptRange is Attributes.TEMPT_RANGE (RangedAttribute "tempt_range", 10.0, 0.0, 2048.0).
	// Added by Animal.createAnimalAttributes (so Cat and other animals carry it at 10.0).
	TemptRange = NewRangedAttribute("tempt_range", 10.0, 0.0, 2048.0)

	// StepHeight is Attributes.STEP_HEIGHT (RangedAttribute "step_height", 0.6, 0.0, 10.0).
	// Registered in Attributes.<clinit> via `new RangedAttribute("attribute.name.step_height", 0.6d,
	// 0.0d, 10.0d).setSyncable(true)` (javap Attributes: ldc2_w 0.6d, dconst_0, ldc2_w 10.0d - this
	// session). Added to every LivingEntity by createLivingAttributes; EnderMan.createAttributes
	// OVERRIDES it to 1.0. Consumed by Entity.maxUpStep() (the auto-step-up height).
	StepHeight = NewRangedAttribute("step_height", 0.6, 0.0, 10.0)

	// SafeFallDistance is Attributes.SAFE_FALL_DISTANCE (RangedAttribute "safe_fall_distance", 3.0,
	// -1024.0, 1024.0). Registered in Attributes.<clinit> via `new RangedAttribute(
	// "attribute.name.safe_fall_distance", 3.0d, -1024.0d, 1024.0d).setSyncable(true)` (javap
	// Attributes: ldc2_w 3.0d, ldc2_w -1024.0d, ldc2_w 1024.0d - this session). Added to every
	// LivingEntity by createLivingAttributes; Fox.createAttributes OVERRIDES it to 5.0. Consumed by
	// LivingEntity.calculateFallPower (`(d + 1.0E-6) - getAttributeValue(SAFE_FALL_DISTANCE)`).
	SafeFallDistance = NewRangedAttribute("safe_fall_distance", 3.0, -1024.0, 1024.0)
	// FlyingSpeed is Attributes.FLYING_SPEED (RangedAttribute "flying_speed", 0.4, 0.0, 1024.0). The
	// registry default is 0.4; the HappyGhast supplier overrides it to 0.05 (HappyGhast.createAttributes).
	// Cite net.minecraft.world.entity.ai.attributes.Attributes.FLYING_SPEED.
	FlyingSpeed = NewRangedAttribute("flying_speed", 0.4, 0.0, 1024.0)

	// CameraDistance is Attributes.CAMERA_DISTANCE (RangedAttribute "camera_distance", 4.0, 0.0, 32.0).
	// The registry default is 4.0; the HappyGhast supplier overrides it to 8.0 (HappyGhast.createAttributes,
	// the mounted-camera pull-back). Cite net.minecraft.world.entity.ai.attributes.Attributes.CAMERA_DISTANCE.
	CameraDistance = NewRangedAttribute("camera_distance", 4.0, 0.0, 32.0)

	// Gravity is Attributes.GRAVITY (RangedAttribute "gravity", 0.08, -1.0, 1.0). Registered in
	// Attributes.<clinit> via `new RangedAttribute("attribute.name.gravity", 0.08d, -1.0d, 1.0d)
	// .setSyncable(true)` (javap Attributes: ldc2_w 0.08d, ldc2_w -1.0d, dconst_1 - this session).
	// Added to every LivingEntity by createLivingAttributes; consumed by Entity.getGravity() (the
	// per-tick downward pull) and by LongJumpUtil.calculateJumpVectorForAngle (the ballistic solve).
	Gravity = NewRangedAttribute("gravity", 0.08, -1.0, 1.0)

	// JumpStrength is Attributes.JUMP_STRENGTH (RangedAttribute "jump_strength", 0.41999998688697815,
	// 0.0, 32.0). Registered in Attributes.<clinit> via `new RangedAttribute(
	// "attribute.name.jump_strength", 0.41999998688697815d, 0.0d, 32.0d).setSyncable(true)` (javap
	// Attributes: ldc2_w 0.41999998688697815d, dconst_0, ldc2_w 32.0d - this session). Added to every
	// LivingEntity by createLivingAttributes (Goat does NOT override it); consumed by
	// LongJumpToRandomPos.calculateOptimalJumpVector (velocity = JUMP_STRENGTH * maxJumpVelocityMultiplier).
	JumpStrength = NewRangedAttribute("jump_strength", 0.41999998688697815, 0.0, 32.0)

	// SpawnReinforcementsChance is Attributes.SPAWN_REINFORCEMENTS_CHANCE (RangedAttribute
	// "spawn_reinforcements", 0.0, 0.0, 1.0). Registered in Attributes.<clinit> via `new RangedAttribute(
	// "attribute.name.spawn_reinforcements", 0.0d, 0.0d, 1.0d)` (javap Attributes: ldc_w spawn_reinforcements,
	// dconst_0, dconst_0, dconst_1 - this session). Added (no base override) by Zombie.createAttributes, so a
	// Zombie carries it at the registration default 0.0 unless randomizeReinforcementsChance sets a spawn base.
	// Consumed by Zombie.hurtServer (the HARD-difficulty reinforcement-spawn gate: `nextFloat() <
	// getAttributeValue(SPAWN_REINFORCEMENTS_CHANCE)`) and by the reinforcement caller/callee -0.05
	// addPermanentModifier charges. Cite net.minecraft.world.entity.ai.attributes.Attributes.SPAWN_REINFORCEMENTS_CHANCE.
	SpawnReinforcementsChance = NewRangedAttribute("spawn_reinforcements", 0.0, 0.0, 1.0)

	// ---- The remaining vanilla Attributes.<clinit> registrations. Each default/min/max is read
	// directly from the RangedAttribute ctor operands in the static initializer this session
	// (`new RangedAttribute("attribute.name.<name>", DEFAULT, MIN, MAX)[.setSyncable(true)]`); these
	// complete the 40-attribute registry. A definition is inert until a modifier/consumer reads it,
	// and each equals its vanilla registration default the moment it is added -- none is baked away.
	// CITE net.minecraft.world.entity.ai.attributes.Attributes.<clinit>. ----

	// AirDragModifier is Attributes.AIR_DRAG_MODIFIER ("air_drag_modifier", 1.0, 0.0, 2048.0, syncable)
	// -- the in-air horizontal drag scale. (javap: dconst_1, dconst_0, ldc2_w 2048.0d.)
	AirDragModifier = NewRangedAttribute("air_drag_modifier", 1.0, 0.0, 2048.0)

	// BelowNameDistance is Attributes.BELOW_NAME_DISTANCE ("below_name_distance", 10.0, 0.0, 512.0,
	// syncable) -- max distance the below-name scoreboard number renders. (javap: ldc2_w 10.0d,
	// dconst_0, ldc2_w 512.0d.)
	BelowNameDistance = NewRangedAttribute("below_name_distance", 10.0, 0.0, 512.0)

	// BlockBreakSpeed is Attributes.BLOCK_BREAK_SPEED ("block_break_speed", 1.0, 0.0, 1024.0, syncable).
	// (javap: dconst_1, dconst_0, ldc2_w 1024.0d.) Consumed by Player.getDestroySpeed
	// (`f *= (float) getAttributeValue(BLOCK_BREAK_SPEED)`) -- the unconditional dig-speed multiplier.
	BlockBreakSpeed = NewRangedAttribute("block_break_speed", 1.0, 0.0, 1024.0)

	// BlockInteractionRange is Attributes.BLOCK_INTERACTION_RANGE ("block_interaction_range", 4.5, 0.0,
	// 64.0, syncable). (javap: ldc2_w 4.5d, dconst_0, ldc2_w 64.0d.) Feeds the block reach gate
	// (Player.blockInteractionRange -> getAttributeValue(BLOCK_INTERACTION_RANGE)).
	BlockInteractionRange = NewRangedAttribute("block_interaction_range", 4.5, 0.0, 64.0)

	// Bounciness is Attributes.BOUNCINESS ("bounciness", 0.0, 0.0, 1.0, syncable) -- the entity's bounce
	// factor on landing. (javap: dconst_0, dconst_0, dconst_1.)
	Bounciness = NewRangedAttribute("bounciness", 0.0, 0.0, 1.0)

	// BurningTime is Attributes.BURNING_TIME ("burning_time", 1.0, 0.0, 1024.0, syncable) -- scales how
	// long the entity stays on fire. (javap: dconst_1, dconst_0, ldc2_w 1024.0d.)
	BurningTime = NewRangedAttribute("burning_time", 1.0, 0.0, 1024.0)

	// ExplosionKnockbackResistance is Attributes.EXPLOSION_KNOCKBACK_RESISTANCE
	// ("explosion_knockback_resistance", 0.0, 0.0, 1.0, syncable). (javap: dconst_0, dconst_0, dconst_1.)
	ExplosionKnockbackResistance = NewRangedAttribute("explosion_knockback_resistance", 0.0, 0.0, 1.0)

	// FallDamageMultiplier is Attributes.FALL_DAMAGE_MULTIPLIER ("fall_damage_multiplier", 1.0, 0.0,
	// 100.0, syncable). (javap: dconst_1, dconst_0, ldc2_w 100.0d.) Consumed by the fall-damage scale
	// (LivingEntity.calculateFallDamage) -- 1.0 leaves vanilla fall damage unchanged.
	FallDamageMultiplier = NewRangedAttribute("fall_damage_multiplier", 1.0, 0.0, 100.0)

	// FrictionModifier is Attributes.FRICTION_MODIFIER ("friction_modifier", 1.0, 0.0, 1024.0, syncable)
	// -- scales the movement friction the entity experiences. (javap: dconst_1, dconst_0, ldc2_w 1024.0d.)
	FrictionModifier = NewRangedAttribute("friction_modifier", 1.0, 0.0, 1024.0)

	// Luck is Attributes.LUCK ("luck", 0.0, -1024.0, 1024.0, syncable). (javap: dconst_0, ldc2_w -1024.0d,
	// ldc2_w 1024.0d.) Consumed by loot-table quality/luck rolls.
	Luck = NewRangedAttribute("luck", 0.0, -1024.0, 1024.0)

	// MiningEfficiency is Attributes.MINING_EFFICIENCY ("mining_efficiency", 0.0, 0.0, 1024.0, syncable).
	// (javap: dconst_0, dconst_0, ldc2_w 1024.0d.) Consumed by Player.getDestroySpeed
	// (`if (f > 1.0f) f += (float) getAttributeValue(MINING_EFFICIENCY)`); the Efficiency enchant grants
	// level^2+1 to it.
	MiningEfficiency = NewRangedAttribute("mining_efficiency", 0.0, 0.0, 1024.0)

	// MovementEfficiency is Attributes.MOVEMENT_EFFICIENCY ("movement_efficiency", 0.0, 0.0, 1.0,
	// syncable). (javap: dconst_0, dconst_0, dconst_1.) The Soul Speed enchant grants it; it lerps the
	// entity's ground movement toward its no-friction speed.
	MovementEfficiency = NewRangedAttribute("movement_efficiency", 0.0, 0.0, 1.0)

	// NameTagDistance is Attributes.NAME_TAG_DISTANCE ("name_tag_distance", 64.0, 0.0, 512.0, syncable)
	// -- max distance the entity's name tag renders. (javap: ldc2_w 64.0d, dconst_0, ldc2_w 512.0d.)
	NameTagDistance = NewRangedAttribute("name_tag_distance", 64.0, 0.0, 512.0)

	// OxygenBonus is Attributes.OXYGEN_BONUS ("oxygen_bonus", 0.0, 0.0, 1024.0, syncable). (javap:
	// dconst_0, dconst_0, ldc2_w 1024.0d.) Consumed by LivingEntity.decreaseAirSupply (the
	// `d = getAttributeValue(OXYGEN_BONUS); if (d > 0.0 && nextDouble() >= 1/(d+1)) return air` skip);
	// the Respiration enchant grants it.
	OxygenBonus = NewRangedAttribute("oxygen_bonus", 0.0, 0.0, 1024.0)

	// Scale is Attributes.SCALE ("scale", 1.0, 0.0625, 16.0, syncable). (javap: dconst_1, ldc2_w 0.0625d,
	// ldc2_w 16.0d.) Scales the entity's rendered/collision size; 1.0 is the vanilla default (unchanged).
	Scale = NewRangedAttribute("scale", 1.0, 0.0625, 16.0)

	// SneakingSpeed is Attributes.SNEAKING_SPEED ("sneaking_speed", 0.3, 0.0, 1.0, syncable). (javap:
	// ldc2_w 0.3d, dconst_0, dconst_1.) The fraction of walk speed applied while sneaking; the Swift
	// Sneak enchant grants it (raising the sneak speed cap).
	SneakingSpeed = NewRangedAttribute("sneaking_speed", 0.3, 0.0, 1.0)

	// SubmergedMiningSpeed is Attributes.SUBMERGED_MINING_SPEED ("submerged_mining_speed", 0.2, 0.0, 20.0,
	// syncable). (javap: ldc2_w 0.2d, dconst_0, ldc2_w 20.0d.) Consumed by Player.getDestroySpeed
	// (`if (isEyeInFluid(WATER)) f *= (float) getAttribute(SUBMERGED_MINING_SPEED).getValue()`) -- the
	// 0.2 default is the vanilla 5x underwater dig penalty; the Aqua Affinity enchant raises it to 1.0.
	SubmergedMiningSpeed = NewRangedAttribute("submerged_mining_speed", 0.2, 0.0, 20.0)

	// WaterMovementEfficiency is Attributes.WATER_MOVEMENT_EFFICIENCY ("water_movement_efficiency", 0.0,
	// 0.0, 1.0, syncable). (javap: dconst_0, dconst_0, dconst_1.) Consumed by the in-water travel drag
	// lerp (LivingEntity.travel*); the Depth Strider enchant grants it.
	WaterMovementEfficiency = NewRangedAttribute("water_movement_efficiency", 0.0, 0.0, 1.0)

	// WaypointTransmitRange is Attributes.WAYPOINT_TRANSMIT_RANGE ("waypoint_transmit_range", 0.0, 0.0,
	// 6.0E7). (javap: dconst_0, dconst_0, ldc2_w 6.0E7d, then setSentiment -- NOT setSyncable, so this
	// attribute is not synced.) Locator-bar broadcast range; definition-only (waypoint subsystem not modeled).
	WaypointTransmitRange = NewRangedAttribute("waypoint_transmit_range", 0.0, 0.0, 6.0e7)

	// WaypointReceiveRange is Attributes.WAYPOINT_RECEIVE_RANGE ("waypoint_receive_range", 0.0, 0.0,
	// 6.0E7, setSentiment -- not syncable). (javap: dconst_0, dconst_0, ldc2_w 6.0E7d.) Locator-bar
	// receive range; definition-only (waypoint subsystem not modeled).
	WaypointReceiveRange = NewRangedAttribute("waypoint_receive_range", 0.0, 0.0, 6.0e7)
)
