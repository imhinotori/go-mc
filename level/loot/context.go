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

	// --- FISHING loot context (the fishing rod retrieve roll) --------------------------------------
	//
	// The gameplay/fishing table gates its TREASURE sub-table on an entity_properties condition over
	// THIS_ENTITY (the FishingHook): predicate minecraft:type_specific/fishing_hook.in_open_water ==
	// true. InOpenWater carries the hook's FishingHook.isOpenWaterFishing() at roll time (the 5x5x4
	// calculateOpenWater result). false => the treasure entry's condition fails => only junk+fish are
	// eligible (the vanilla "not in open water, no treasure" behavior). Populated by
	// NewFishingLootContext; every non-fishing context leaves it false (its tables never read it).
	// Source: javap FishingHookPredicate.matches (inOpenWater test) + FishingHook.isOpenWaterFishing.
	InOpenWater bool

	// --- CUBE-MOB (slime/magma_cube) loot context ------------------------------------------------
	//
	// The entities/slime + entities/magma_cube tables gate their per-size pools on an
	// entity_properties condition over THIS_ENTITY: predicate minecraft:type_specific/cube_mob.size
	// == N (the slimeball pool is size 1; magma_cream is size > 1). CubeMobSize carries the dying
	// cube's getSize() at roll time so the type_specific/cube_mob size term resolves. 0 for every
	// non-cube context (its tables never read it). Populated from EntityLootParams.CubeMobSize.
	// Source: javap CubeMobPredicate (size MinMaxBounds.Ints matches) + entities/slime.json.
	CubeMobSize int
}

// EntityLootParams carries the entity (death) loot-context inputs NewEntityLootContext threads into
// the LootContext for the entity-table handlers. All fields default to the vanilla v1 state, so an
// EntityLootParams{} (or one with only KilledByPlayer set) rolls the unconditional pools faithfully.
type EntityLootParams struct {
	KilledByPlayer       bool
	VictimOnFire         bool
	AttackerLootingLevel int
	AttackerSmeltsLoot   bool
	// CubeMobSize is THIS_ENTITY's getSize() for a cube-mob (slime/magma_cube) death; 0 otherwise.
	// The type_specific/cube_mob size condition reads it (slimeball pool: size == 1). Cite Slime.remove
	// -> dropFromLootTable roll while getSize() is still the dying cube's size.
	CubeMobSize int
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
	c.CubeMobSize = p.CubeMobSize
	return c
}

// NewFishingLootContext builds a LootContext for the FISHING loot roll (FishingHook.retrieve): the
// LegacyRandomSource seeded by seed, the fishing luck (this.luck + owner.getLuck() — the withLuck
// param), and the hook's open-water flag (isOpenWaterFishing) the treasure entry's entity_properties
// condition reads. Junk/fish weights are luck-independent (quality-scaled at luck 0); treasure is
// gated on inOpenWater.
//
// Source: javap FishingHook.retrieve (new LootParams.Builder(...).withParameter(ORIGIN/TOOL/THIS_ENTITY)
// .withLuck(luck + owner.getLuck()).create(FISHING)) + the fishing table's in_open_water condition.
func NewFishingLootContext(seed int64, luck float32, inOpenWater bool) *LootContext {
	c := NewLootContext(seed, luck)
	c.InOpenWater = inOpenWater
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
