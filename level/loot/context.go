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
