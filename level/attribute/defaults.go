package attribute

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
//	                         ARMOR_TOUGHNESS(0), MAX_ABSORPTION(0), ENTITY_INTERACTION_RANGE(3),
//	                         ATTACK_KNOCKBACK(0), ... (plus the non-gameplay set omitted here)
//	createMobAttributes    : + FOLLOW_RANGE override 16.0
//	createMonsterAttributes: + ATTACK_DAMAGE (registration default 2.0, no override)
//	createAnimalAttributes : + TEMPT_RANGE 10.0
//
// CITED NOTE on the omitted non-gameplay attributes: createLivingAttributes also adds STEP_HEIGHT,
// SCALE, GRAVITY, JUMP_STRENGTH, SAFE_FALL_DISTANCE, FALL_DAMAGE_MULTIPLIER, OXYGEN_BONUS,
// BURNING_TIME, and ~15 others at their registration defaults. They are omitted from these builders
// because no consumer reads them yet (no fall-physics-from-attribute, no scale, no gravity-attribute
// path). Each equals its vanilla registration default and slots in as one `.Add(...)` line when its
// consumer lands — never baked away. The gameplay subset below is exactly what combat / health /
// movement / AI-ranging read today.

// createLivingAttributes is the port of LivingEntity.createLivingAttributes() (the gameplay subset):
// the base builder every living entity starts from. The full vanilla method adds ~25 attributes; we
// register the ones with a live consumer (health, defense, movement, interaction, knockback). The
// non-gameplay attributes are the CITED omission documented in the file header.
func createLivingAttributes() *Builder {
	return NewBuilder().
		Add(MaxHealth).             // registration default 20.0
		Add(KnockbackResistance).   // registration default 0.0
		Add(MovementSpeed).         // registration default 0.7 (every entity overrides this)
		Add(Armor).                 // registration default 0.0
		Add(ArmorToughness).        // registration default 0.0
		Add(MaxAbsorption).         // registration default 0.0
		Add(EntityInteractionRange). // registration default 3.0
		Add(AttackKnockback)        // registration default 0.0
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
// that type's DefaultAttributes supplier. Returns nil when the type has no registered supplier (an
// unported entity) — the caller must nil-check (a nil map means "no attributes", and every
// getAttributeValue helper falls back to a default for a nil map, exactly as a modifier-free read
// would). This is the single constructor the live entity uses to attach its attribute map at spawn.
func NewMapForEntity(entityName string) *Map {
	supplier, ok := GetSupplier(entityName)
	if !ok {
		return nil
	}
	return NewMap(supplier)
}
