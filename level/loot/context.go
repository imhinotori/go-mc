package loot

// context.go — the LootContext: the carrier of the loot RNG + luck the roll engine
// threads through every getInt/getFloat/run/test call.
//
// Source (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.storage.loot.LootContext (getRandom, getLuck)
//   - net.minecraft.world.level.storage.loot.LootContext$Builder.withOptionalRandomSeed
//   - net.minecraft.util.RandomSource.create(long) == new LegacyRandomSource(seed)

import "github.com/imhinotori/sulfur/world/levelgen"

// LootContext mirrors net.minecraft.world.level.storage.loot.LootContext for the
// pure chest/block-drop path: it carries the RandomSource (the LegacyRandomSource
// LCG seeded by the lootTableSeed) and the luck value. Chests roll with luck = 0
// (LootParams.getLuck() default), so the bonusRolls term and the quality-scaled
// weight both vanish. The level/biome/tool params that a richer context would
// carry are not needed by the 5 chest groups; condition evaluators that need them
// (location_check) read Biome here, which 20-02 populates.
type LootContext struct {
	rng  levelgen.RandomSource
	luck float32
	// Biome is the loot context's biome id (for location_check). Empty until 20-02
	// wires the real chest-open context; an empty biome makes location_check a
	// permissive default (see condition.go).
	Biome string
	// HasTool reports whether the loot context carries a TOOL (the
	// LootContextParams.TOOL the block-break path supplies). The pure v1 block-break
	// path (server/block_drop.go) supplies NO tool, so this is false and match_tool /
	// apply_bonus take their decompiled `TOOL == null` branches (match_tool -> false,
	// apply_bonus -> no-op). This is structured to become a real tool/enchantment read
	// when the dig path threads the held tool through — never baked away.
	HasTool bool
	// ToolSilkTouch / ToolFortuneLevel are the cited-stub enchantment reads the block
	// delta needs when HasTool is true: match_tool's silk_touch predicate reads
	// ToolSilkTouch, apply_bonus's fortune formula reads ToolFortuneLevel. Both are 0/
	// false until the dig path supplies a real held tool (HasTool gates their use).
	ToolSilkTouch    bool
	ToolFortuneLevel int
	// ExplosionRadius is the LootContextParams.EXPLOSION_RADIUS the explosion drop path
	// supplies (>0 when a block is destroyed by a blast). A block BREAK supplies none
	// (HasExplosion false), so survives_explosion passes and explosion_decay is a no-op
	// — the decompiled `EXPLOSION_RADIUS == null` branch.
	HasExplosion    bool
	ExplosionRadius float32

	// --- ENTITY loot context (Phase 29 Plan 04, the A4 bounded extension) ----------------
	//
	// The 94 entity loot tables (the mob death-drop path) reference entity-context conditions
	// /functions the chest/block path never carried: entity_properties (the victim's is_on_fire
	// flag, the attacker's mainhand enchants), killed_by_player, furnace_smelt (gated by the
	// smelt predicate), enchanted_count_increase (the looting bonus). The fields below carry the
	// minimal LootContextParams (THIS_ENTITY's flags + the ATTACKING_ENTITY's enchant levels +
	// the killed-by-player bit) those handlers read. They are populated by NewEntityLootContext
	// from EntityLootParams; the chest/block path leaves them at the zero value (its conditions
	// never read them), so its golden seed-reproduction is untouched.
	//
	// CITED-STUB DEFAULTS (CLAUDE.md "stub behind a cited constant equal to the vanilla default,
	// structured to become a real read later"): v1 has no fire/effect/enchant subsystem, so
	// VictimOnFire/AttackerLootingLevel/AttackerSmeltsLoot default to the vanilla v1 state (not on
	// fire, no looting, no smelts_loot enchant). With those defaults the pig table's furnace_smelt
	// (gated by the any_of(entity_properties) smelt predicate -> FALSE) and enchanted_count_increase
	// (looting level 0 -> +0) are faithful NO-OPS, and the unconditional set_count[1,3] pool rolls
	// the raw porkchop — the CORRECT v1 drop. When a fire/effect/enchant subsystem lands, the death
	// caller fills these from the real entity state with NO change to the handlers.

	// IsEntityContext marks this context as an entity (death-loot) context (NewEntityLootContext).
	// The entity-context handlers (entity_properties / killed_by_player / furnace_smelt /
	// enchanted_count_increase) only ever appear in entity tables, so this flag is informational;
	// the handlers read the specific fields below.
	IsEntityContext bool
	// KilledByPlayer is the LootContextParams "killed by a player" bit (the death source is a direct
	// player attack — the v1 proxy for lastHurtByPlayerMemoryTime>0). killed_by_player reads it.
	KilledByPlayer bool
	// VictimOnFire is THIS_ENTITY's is_on_fire flag (the pig table's furnace_smelt any_of reads it).
	// v1 default false (no fire subsystem). When a mob can be set on fire, the death caller fills it.
	VictimOnFire bool
	// AttackerLootingLevel is the ATTACKING_ENTITY's minecraft:looting enchant level
	// (enchanted_count_increase reads it via EnchantmentHelper.getEnchantmentLevel). v1 default 0.
	AttackerLootingLevel int
	// AttackerSmeltsLoot reports whether the ATTACKING_ENTITY's mainhand carries a #smelts_loot
	// enchant (the pig table's furnace_smelt any_of's second term reads it). v1 default false.
	AttackerSmeltsLoot bool
}

// EntityLootParams carries the entity (death) loot-context inputs NewEntityLootContext threads into
// the LootContext for the entity-table handlers. All fields default to the vanilla v1 state, so an
// EntityLootParams{} (or one with only KilledByPlayer set) rolls the unconditional pools faithfully.
type EntityLootParams struct {
	KilledByPlayer       bool
	VictimOnFire         bool
	AttackerLootingLevel int
	AttackerSmeltsLoot   bool
}

// NewEntityLootContext builds a LootContext for an ENTITY (death) loot roll: the LegacyRandomSource
// seeded by seed (exactly like NewLootContext) plus the entity-context params the entity-table
// handlers read. The death path uses this so furnace_smelt / enchanted_count_increase /
// entity_properties / killed_by_player resolve against the real (v1-default) entity state.
//
// Source: javap LootContext$Builder + the entity-table LootContextParams (THIS_ENTITY,
// ATTACKING_ENTITY, LAST_DAMAGE_PLAYER).
func NewEntityLootContext(seed int64, luck float32, p EntityLootParams) *LootContext {
	c := NewLootContext(seed, luck)
	c.IsEntityContext = true
	c.KilledByPlayer = p.KilledByPlayer
	c.VictimOnFire = p.VictimOnFire
	c.AttackerLootingLevel = p.AttackerLootingLevel
	c.AttackerSmeltsLoot = p.AttackerSmeltsLoot
	return c
}

// NewLootContext mirrors LootContext$Builder.withOptionalRandomSeed(seed).create():
// `if (seed != 0L) random = RandomSource.create(seed)`, and RandomSource.create(long)
// is `new LegacyRandomSource(seed)` (the java.util.Random LCG). A zero seed in
// vanilla leaves the builder's random unset; here we always need a source, so a
// zero seed seeds the LCG with 0 (deterministic, matching new LegacyRandomSource(0))
// — the chest path never passes 0 (the piece draws nextLong()).
//
// Source: javap LootContext$Builder.withOptionalRandomSeed + RandomSource.create.
func NewLootContext(seed int64, luck float32) *LootContext {
	return &LootContext{
		rng:  levelgen.NewLegacyRandomSource(seed),
		luck: luck,
	}
}

// NewLootContextWithSource builds a context over an existing RandomSource (the
// vanilla withOptionalRandomSource path) — used by tests that need to assert the
// exact draw sequence against a hand-controlled LCG.
func NewLootContextWithSource(rng levelgen.RandomSource, luck float32) *LootContext {
	return &LootContext{rng: rng, luck: luck}
}

// Random mirrors LootContext.getRandom(): the RandomSource the providers/engine draw from.
func (c *LootContext) Random() levelgen.RandomSource { return c.rng }

// Luck mirrors LootContext.getLuck(): LootParams.getLuck() (0 for chests).
func (c *LootContext) Luck() float32 { return c.luck }
