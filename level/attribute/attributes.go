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
)
