package loot

// blockdelta.go — the block-table delta (20-02 Task 1): the conditions/functions the
// blocks/*.json tables use that the 5 chest groups never do. The `alternatives` entry
// (first-passing child wins) lives in roll.go's expand; this file ports the four
// block-only condition/function bodies, each a LITERAL port of the decompiled
// bytecode. The faithful no-tool / no-explosion defaults are the v1 hand-break path:
// a hand break with NO tool and NO explosion context drops the non-silk-touch
// alternative at base count.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.storage.loot.predicates.MatchTool.test
//       (TOOL == null -> false; else ItemPredicate.test(tool))
//   - net.minecraft.world.level.storage.loot.predicates.ExplosionCondition.test
//       (EXPLOSION_RADIUS == null -> true; else nextFloat() <= 1/radius)
//   - net.minecraft.world.level.storage.loot.functions.ApplyExplosionDecay.run
//       (EXPLOSION_RADIUS == null -> stack unchanged; else per-item nextFloat()<=1/radius keep)
//   - net.minecraft.world.level.storage.loot.functions.ApplyBonusCount.run
//       (TOOL == null -> stack unchanged; else formula.calculateNewCount(rng, count, enchLevel))

// matchTool mirrors net.minecraft.world.level.storage.loot.predicates.MatchTool: a
// predicate over the LootContext's TOOL parameter. The block tables use it ONLY for a
// silk_touch enchantment gate (the first `alternatives` child). Decompiled:
//
//	ItemInstance tool = ctx.getOptionalParameter(TOOL);
//	if (tool == null) return false;
//	if (predicate.isEmpty()) return true;
//	return predicate.get().test(tool);
//
// CITED STUB (CLAUDE.md): the pure v1 block-break path carries no tool (ctx.HasTool ==
// false), so this returns false — the decompiled `TOOL == null -> false` branch. When
// a held tool is threaded later (ctx.HasTool true), the predicate becomes the real
// silk_touch read off ctx.ToolSilkTouch. Structured to become a real read, never baked
// away. The block tables' only match_tool predicate is the silk_touch level>=1 gate.
type matchTool struct {
	// requiresSilkTouch is true when the predicate gates on a silk_touch enchantment
	// (the only match_tool form the block tables use). Parsed from the predicate JSON.
	requiresSilkTouch bool
}

// Test mirrors MatchTool.test. No TOOL in the context -> false (decompiled). With a
// tool, the silk_touch gate reads ctx.ToolSilkTouch (the cited stub).
func (m *matchTool) Test(ctx *LootContext) bool {
	if !ctx.HasTool {
		return false // decompiled: TOOL == null -> false
	}
	if m.requiresSilkTouch {
		return ctx.ToolSilkTouch
	}
	// A match_tool with no parsed predicate (predicate.isEmpty()) -> true. The block
	// tables always carry the silk_touch predicate, so this is the structural default.
	return true
}

// explosionCondition mirrors
// net.minecraft.world.level.storage.loot.predicates.ExplosionCondition
// (survives_explosion). Decompiled:
//
//	Float radius = ctx.getOptionalParameter(EXPLOSION_RADIUS);
//	if (radius == null) return true;
//	return ctx.getRandom().nextFloat() <= 1.0f / radius;
//
// A block BREAK (not an explosion) carries no EXPLOSION_RADIUS (ctx.HasExplosion
// false), so this returns true — the item survives. The float comparison is `<=`
// (fcmpg; ifgt -> false), matching the bytecode.
type explosionCondition struct{}

// Test mirrors ExplosionCondition.test.
func (e *explosionCondition) Test(ctx *LootContext) bool {
	if !ctx.HasExplosion {
		return true // decompiled: EXPLOSION_RADIUS == null -> true
	}
	return ctx.Random().NextFloat() <= 1.0/ctx.ExplosionRadius
}

// applyExplosionDecay mirrors
// net.minecraft.world.level.storage.loot.functions.ApplyExplosionDecay.run.
// Decompiled:
//
//	Float radius = ctx.getOptionalParameter(EXPLOSION_RADIUS);
//	if (radius == null) return stack;            // no explosion -> unchanged
//	float chance = 1.0f / radius;
//	int count = stack.getCount();
//	int kept = 0;
//	for (int i = 0; i < count; i++) if (rng.nextFloat() <= chance) kept++;
//	stack.setCount(kept);
//	return stack;
//
// For a block break (no EXPLOSION_RADIUS) this is a pure no-op — the decompiled early
// return. The per-item keep draw (when an explosion is present) is the explosion
// drop-loss.
type applyExplosionDecay struct{}

// Run mirrors ApplyExplosionDecay.run.
func (a *applyExplosionDecay) Run(stack *ItemStack, ctx *LootContext) *ItemStack {
	if !ctx.HasExplosion {
		return stack // decompiled: EXPLOSION_RADIUS == null -> stack unchanged
	}
	chance := 1.0 / ctx.ExplosionRadius
	count := stack.Count
	kept := 0
	for i := 0; i < count; i++ {
		if ctx.Random().NextFloat() <= chance {
			kept++
		}
	}
	stack.Count = kept
	return stack
}

// applyBonusFormula is the ApplyBonusCount.Formula the block tables use. The
// blocks/diamond_ore.json uses "minecraft:ore_drops" (OreDrops). The two other forms
// (binomial_with_bonus_count, uniform_bonus_count) are not used by the v1 ore set but
// the type is recorded so a future ore table parses; calculateNewCount dispatches.
type applyBonusFormula int

const (
	formulaOreDrops      applyBonusFormula = iota // OreDrops.calculateNewCount
	formulaUniformBonus                           // UniformBonusCount
	formulaBinomialBonus                          // BinomialWithBonusCount
)

// applyBonusCount mirrors
// net.minecraft.world.level.storage.loot.functions.ApplyBonusCount.run.
// Decompiled:
//
//	ItemInstance tool = ctx.getOptionalParameter(TOOL);
//	if (tool == null) return stack;                                   // no tool -> unchanged
//	int ench = EnchantmentHelper.getItemEnchantmentLevel(enchantment, tool);
//	int newCount = formula.calculateNewCount(ctx.getRandom(), stack.getCount(), ench);
//	stack.setCount(newCount);
//	return stack;
//
// For a v1 hand break (no TOOL) this is a pure no-op — the decompiled early return, so
// diamond_ore drops its base 1 diamond. When a fortune tool is threaded later
// (ctx.HasTool true, ctx.ToolFortuneLevel the level), the OreDrops formula applies.
type applyBonusCount struct {
	formula    applyBonusFormula
	enchantment string // the enchantment id (e.g. "minecraft:fortune") — for the cited read
}

// Run mirrors ApplyBonusCount.run.
func (a *applyBonusCount) Run(stack *ItemStack, ctx *LootContext) *ItemStack {
	if !ctx.HasTool {
		return stack // decompiled: TOOL == null -> stack unchanged
	}
	ench := 0
	if a.enchantment == "minecraft:fortune" {
		ench = ctx.ToolFortuneLevel
	}
	stack.Count = a.calculateNewCount(ctx, stack.Count, ench)
	return stack
}

// calculateNewCount mirrors the ApplyBonusCount.Formula.calculateNewCount dispatch.
// OreDrops.calculateNewCount (the only block-table form):
//
//	if (enchantmentLevel <= 0) return count;
//	int bonus = Math.max(0, rng.nextInt(enchantmentLevel + 2) - 1);
//	return count * (bonus + 1);
//
// Source: javap ApplyBonusCount$OreDrops.calculateNewCount.
func (a *applyBonusCount) calculateNewCount(ctx *LootContext, count, ench int) int {
	switch a.formula {
	case formulaOreDrops:
		if ench <= 0 {
			return count
		}
		bonus := int(ctx.Random().NextIntN(int32(ench+2))) - 1
		if bonus < 0 {
			bonus = 0
		}
		return count * (bonus + 1)
	default:
		// uniform/binomial not used by the v1 ore set; with no tool they never run.
		return count
	}
}
