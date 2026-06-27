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
