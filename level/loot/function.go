package loot

// function.go — the loot function dispatch + the workhorse SetItemCountFunction.
// The 5 chest groups use exactly 3 functions: set_count (148 uses),
// enchant_randomly (3 uses, enchant.go), enchant_with_levels (1 use, enchant.go).
// Every function is wrapped by LootItemConditionalFunction.apply: the per-function
// conditions gate whether run() is called (the chest functions carry no
// conditions, so the gate is always true).
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.storage.loot.functions.LootItemConditionalFunction.apply
//   - net.minecraft.world.level.storage.loot.functions.SetItemCountFunction.run
//   - net.minecraft.world.level.storage.loot.functions.LootItemFunction.decorate (apply-in-order)

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/data/registryid"
)

// conditionalFunction wraps a concrete function with its per-function conditions,
// mirroring LootItemConditionalFunction.apply: `if compositePredicates.test(ctx)
// return run(stack, ctx); else return stack;`. The chest functions have no
// conditions so the predicate is the empty all-of (always true).
//
// Source: javap LootItemConditionalFunction.apply.
type conditionalFunction struct {
	inner      LootFunction
	conditions []LootCondition
}

// Run mirrors LootItemConditionalFunction.apply: gate on the conditions, then run.
func (c *conditionalFunction) Run(stack *ItemStack, ctx *LootContext) *ItemStack {
	if !allConditions(c.conditions, ctx) {
		return stack
	}
	return c.inner.Run(stack, ctx)
}

// SetItemCountFunction mirrors
// net.minecraft.world.level.storage.loot.functions.SetItemCountFunction. run:
// `int base = add ? stack.getCount() : 0; stack.setCount(base + count.getInt(ctx));`.
// The chest tables use the non-add form (add=false) -> setCount(count.getInt(ctx)).
//
// Source: javap SetItemCountFunction.run.
type SetItemCountFunction struct {
	Count NumberProvider
	Add   bool
}

// Run mirrors SetItemCountFunction.run.
func (s *SetItemCountFunction) Run(stack *ItemStack, ctx *LootContext) *ItemStack {
	base := 0
	if s.Add {
		base = stack.Count
	}
	stack.Count = base + s.Count.GetInt(ctx)
	return stack
}

// applyFunctions runs a function list over the stack IN ORDER (the compositeFunction
// LootItemFunction.decorate folds them left-to-right). Order is load-bearing for
// per-seed reproduction: each function draws its own RNG in sequence.
//
// Source: javap LootItemFunction.decorate / LootItemFunctions.compose.
func applyFunctions(fns []LootFunction, stack *ItemStack, ctx *LootContext) *ItemStack {
	for _, fn := range fns {
		stack = fn.Run(stack, ctx)
	}
	return stack
}

// parseFunction decodes one function object, dispatching on its "function" type
// key (validated against registryid.LootFunctionType) and wrapping it in the
// per-function condition gate. An unrecognized function type errors loudly.
func parseFunction(rf rawFunction) (LootFunction, error) {
	typeStr, err := rawString(rf, "function")
	if err != nil {
		return nil, err
	}
	if typeStr == "" {
		return nil, fmt.Errorf("function missing \"function\" type")
	}
	if !validType(typeStr, registryid.LootFunctionType) {
		return nil, fmt.Errorf("unrecognized loot function type %q", typeStr)
	}

	// Per-function conditions (the LootItemConditionalFunction predicate list).
	var conds []LootCondition
	if raw, ok := rf["conditions"]; ok {
		var rcs []rawCondition
		if err := json.Unmarshal(raw, &rcs); err != nil {
			return nil, fmt.Errorf("function conditions: %w", err)
		}
		if conds, err = parseConditions(rcs); err != nil {
			return nil, err
		}
	}

	var inner LootFunction
	switch normalizeType(typeStr) {
	case "set_count":
		countRaw, ok := rf["count"]
		if !ok {
			return nil, fmt.Errorf("set_count missing count")
		}
		np, perr := parseNumberProvider(countRaw)
		if perr != nil {
			return nil, fmt.Errorf("set_count count: %w", perr)
		}
		add := false
		if raw, ok := rf["add"]; ok {
			if err := json.Unmarshal(raw, &add); err != nil {
				return nil, fmt.Errorf("set_count add: %w", err)
			}
		}
		inner = &SetItemCountFunction{Count: np, Add: add}
	case "enchant_randomly":
		inner, err = parseEnchantRandomly(rf)
		if err != nil {
			return nil, err
		}
	case "enchant_with_levels":
		inner, err = parseEnchantWithLevels(rf)
		if err != nil {
			return nil, err
		}
	case "apply_bonus":
		// The block-table fortune function. The ore tables use formula
		// "minecraft:ore_drops" + enchantment "minecraft:fortune". Without a TOOL it is
		// a no-op (the v1 break default). 20-02 Task 1 (javap ApplyBonusCount.run).
		inner, err = parseApplyBonus(rf)
		if err != nil {
			return nil, err
		}
	case "explosion_decay":
		// The block-table explosion drop-loss. Without an EXPLOSION_RADIUS it is a
		// no-op (the v1 break default). 20-02 Task 1 (javap ApplyExplosionDecay.run).
		inner = &applyExplosionDecay{}
	default:
		// A function type neither the chest groups nor the block tables use
		// (set_components, ...). Error loudly rather than silently drop the transform.
		return nil, fmt.Errorf("loot function %q not ported (set_count/enchant_*/apply_bonus/explosion_decay in scope)", typeStr)
	}
	if len(conds) == 0 {
		return inner, nil
	}
	return &conditionalFunction{inner: inner, conditions: conds}, nil
}

// parseApplyBonus decodes an apply_bonus function: the `enchantment` id (e.g.
// "minecraft:fortune") + the `formula` ("minecraft:ore_drops" for the ore tables).
// Without a TOOL the function is a no-op, so the formula only matters once a fortune
// tool is threaded (the cited stub). An unknown formula errors loudly.
//
// Source: javap ApplyBonusCount (the codec reads enchantment + formula).
func parseApplyBonus(rf rawFunction) (LootFunction, error) {
	ench, err := rawString(rf, "enchantment")
	if err != nil {
		return nil, err
	}
	formulaStr, err := rawString(rf, "formula")
	if err != nil {
		return nil, err
	}
	var formula applyBonusFormula
	switch normalizeType(formulaStr) {
	case "ore_drops", "": // bare/absent defaults to ore_drops (the only block-table form)
		formula = formulaOreDrops
	case "uniform_bonus_count":
		formula = formulaUniformBonus
	case "binomial_with_bonus_count":
		formula = formulaBinomialBonus
	default:
		return nil, fmt.Errorf("apply_bonus unknown formula %q", formulaStr)
	}
	return &applyBonusCount{formula: formula, enchantment: ench}, nil
}
