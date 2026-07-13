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
	// ToolItemID is the held TOOL's item resource id ("minecraft:shears",
	// "minecraft:diamond_pickaxe", ...), normalized to the "minecraft:"-prefixed form. It is
	// the ItemPredicate.items source: MatchTool.test -> ItemPredicate.test(tool) tests
	// tool.is(items) (a HolderSet<Item> membership check). The leaf tables gate their leaf-block
	// drop on any_of(match_tool{items:"minecraft:shears"}, match_tool{silk_touch}), so a bare
	// hand (ToolItemID == "") must NOT satisfy the shears predicate. Empty for a bare hand / a
	// non-player break. Cite ItemPredicate.test (items Optional<HolderSet<Item>> present ->
	// tool.is(items)).
	ToolItemID string
	// ToolEnchantments is the full enchantment-id -> level map of the block-break TOOL (the
	// EnchantmentHelper.getItemEnchantmentLevel source ApplyBonusCount reads). Populated by the
	// block-break drop path (server/block_drop.go) from the breaking player's held item; nil when no
	// tool (the v1 hand break / a non-player break) so apply_bonus reads level 0. ToolSilkTouch /
	// ToolFortuneLevel are the pre-existing fast-path fields the silk-touch predicate + the fortune
	// stub read; this map is the general read the ApplyBonusCount port uses for any named enchantment.
	ToolEnchantments map[string]int
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

	// --- BLOCK-INTERACT loot context (the harvest tables: sweet_berry_bush, cave_vine) ------------
	//
	// The block-interact harvest tables (BuiltInLootTables.HARVEST_*) gate their per-age pools on a
	// block_state_property condition over LootContextParams.BLOCK_STATE (e.g. sweet_berry_bush pool-1's
	// {age:"3"} bonus-berry entry). BlockID carries the clicked block's resource id (BlockState.is(block)
	// -> BlockID == condition.block) and BlockProperties carries its property name->string-value map
	// (StatePropertiesPredicate.matches, which compares each requested property against the state's
	// serialized string value). Both empty/nil for every non-block-interact context (its tables never
	// read them, so their golden seed-reproduction is untouched) -- mirroring the vanilla
	// getOptionalParameter(BLOCK_STATE)==null branch (block_state_property.test returns false). Populated
	// by NewBlockInteractLootContext. Source: javap LootItemBlockStatePropertyCondition.test +
	// StatePropertiesPredicate.matches + Block.dropFromBlockInteractLootTable (withParameter(BLOCK_STATE)).
	BlockID         string
	BlockProperties map[string]string

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

	// --- RAIDER (illager captain) loot context ---------------------------------------------------
	//
	// The entities/pillager (+ vindicator/evoker/…) tables gate their ominous_bottle pool on an
	// entity_properties condition over THIS_ENTITY: predicate minecraft:type_specific/raider.is_captain
	// == true (RaiderPredicate.isCaptain). RaiderIsCaptain carries the dying raider's Raider.isCaptain()
	// at roll time so that pool fires ONLY for a slain raid captain (a leader carrying the ominous banner).
	// false for every non-raider / non-captain context (its tables never read it). Populated from
	// EntityLootParams.RaiderIsCaptain. Source: javap RaiderPredicate (isCaptain match) + pillager.json.
	RaiderIsCaptain bool
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
	// RaiderIsCaptain is THIS_ENTITY's Raider.isCaptain() for a slain raider; false otherwise. The
	// type_specific/raider is_captain condition reads it (the pillager ominous_bottle pool). Cite
	// Raider.die -> dropFromLootTable roll while isCaptain() is still true (the banner slot is intact).
	RaiderIsCaptain bool
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
	c.RaiderIsCaptain = p.RaiderIsCaptain
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

// NewBlockInteractLootContext builds a LootContext for a BLOCK-INTERACT harvest roll (the
// Block.dropFromBlockInteractLootTable path: sweet_berry_bush / cave_vine right-click harvest): the
// LegacyRandomSource seeded by seed (the level.getRandom-derived per-roll seed, luck 0), plus the
// clicked block's resource id + property map the block_state_property condition reads (the
// LootContextParams.BLOCK_STATE the vanilla lambda supplies). Every non-block-interact context leaves
// BlockID/BlockProperties empty (its tables never read them).
//
// Source: javap Block.dropFromBlockInteractLootTable (withParameter(BLOCK_STATE)) +
// LootTable.getRandomItems(LootParams) (randomSequence-seeded LootContext).
func NewBlockInteractLootContext(seed int64, blockID string, props map[string]string) *LootContext {
	c := NewLootContext(seed, 0)
	c.BlockID = blockID
	c.BlockProperties = props
	return c
}

// NewBlockInteractLootContextWithSource builds a BLOCK-INTERACT LootContext over a caller-supplied
// RandomSource (the vanilla withOptionalRandomSource path) so the harvest handler can share the
// level's levelRandom (ServerLevel.getRandom) instead of seeding a fresh per-roll LCG -- the draw
// sequence stays byte-in-lockstep with the rest of the region's level-random draws (so a future
// parity oracle pinning that stream is preserved). Luck stays 0 (block-interact harvest is a
// vanilla luck-free path). The loot engine reads the rng through ctx.Random(); the Roll helper's
// `seed` argument is IGNORED when ctx is non-nil (see roll.go), so callers can pass any value (the
// zero in tests is the convention).
//
// Source: javap Block.dropFromBlockInteractLootTable (withParameter(BLOCK_STATE)) + the
// LootContext$Builder.withOptionalRandomSource overload that accepts an existing RandomSource.
func NewBlockInteractLootContextWithSource(rng levelgen.RandomSource, blockID string, props map[string]string) *LootContext {
	c := NewLootContextWithSource(rng, 0)
	c.BlockID = blockID
	c.BlockProperties = props
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
