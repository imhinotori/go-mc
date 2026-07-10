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

// setItemDamageFunction mirrors
// net.minecraft.world.level.storage.loot.functions.SetItemDamageFunction. run (the
// isDamageableItem branch):
//
//	int maxDamage = stack.getMaxDamage();
//	float base = add ? 1 - (float)stack.getDamageValue()/maxDamage : 0;
//	float pct = 1 - Mth.clamp(damage.getFloat(context) + base, 0, 1);
//	stack.setDamageValue(Mth.floor(pct * maxDamage));
//
// The RNG draw (damage.getFloat) is the load-bearing part for per-seed reproduction and is
// performed faithfully. The durability WRITE is a CITED STUB (the roll ItemStack has no
// max_damage/damage component yet — see the set_damage dispatch note), so setDamageValue is a
// no-op; the fishing junk/treasure items carrying set_damage are all damageable, so the draw
// always happens exactly as the jar's isDamageableItem branch.
//
// Source: javap SetItemDamageFunction.run.
type setItemDamageFunction struct {
	damage NumberProvider
	add    bool
}

func (s *setItemDamageFunction) Run(stack *ItemStack, ctx *LootContext) *ItemStack {
	// isDamageableItem: the fishing set_damage items are all damageable, so draw the float
	// (the pct/maxDamage math and setDamageValue are the durability-component stub above).
	_ = s.damage.GetFloat(ctx) // the load-bearing draw (order-faithful); result feeds the stubbed write.
	_ = s.add
	return stack
}

// setPotionFunction mirrors SetPotionFunction.run: a pure component set (POTION_CONTENTS) with NO
// RNG draw. A faithful no-op over the roll ItemStack until the potion-component subsystem lands.
// Source: javap SetPotionFunction.run.
type setPotionFunction struct{}

func (s *setPotionFunction) Run(stack *ItemStack, _ *LootContext) *ItemStack { return stack }

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
	case "furnace_smelt":
		// SmeltItemFunction (Phase 29-04, entity loot): replaces the stack with its SMELTING
		// recipe result. It is WRAPPED by the pig table's any_of(entity_properties) gate (victim
		// on fire OR attacker smelts_loot), so in v1 the conditionalFunction gate is FALSE and run
		// is NEVER called — the porkchop is dropped RAW. The smelting-recipe lookup
		// (RecipeManager.getRecipeFor at loot time) is not wired in v1, so run is a CITED NO-OP
		// (the gate guarantees it never executes; when a fire/recipe-at-loot subsystem lands, the
		// recipe lookup slots in here with the gate already correct). javap SmeltItemFunction.run.
		inner = &smeltItemFunction{}
	case "enchanted_count_increase":
		// EnchantedCountIncreaseFunction (the looting bonus): grow the stack by round(lootingLevel *
		// count.getFloat(ctx)). Reads the ATTACKING_ENTITY's looting enchant level
		// (EnchantmentHelper.getEnchantmentLevel) — in v1 the attacker has no enchants, so the level
		// is 0 and the function returns the stack unchanged (the `if (level == 0) return stack` early
		// return, bytecode 35-41). javap EnchantedCountIncreaseFunction.run.
		countRaw, ok := rf["count"]
		if !ok {
			return nil, fmt.Errorf("enchanted_count_increase missing count")
		}
		np, perr := parseNumberProvider(countRaw)
		if perr != nil {
			return nil, fmt.Errorf("enchanted_count_increase count: %w", perr)
		}
		limit := -1 // hasLimit() false when absent (no clamp)
		if raw, ok := rf["limit"]; ok {
			if err := json.Unmarshal(raw, &limit); err != nil {
				return nil, fmt.Errorf("enchanted_count_increase limit: %w", err)
			}
		}
		inner = &enchantedCountIncrease{count: np, limit: limit}
	case "set_damage":
		// SetItemDamageFunction (the fishing junk/treasure tables' worn-tool durability): for a
		// DAMAGEABLE item it draws `this.damage.getFloat(context)` once and sets the item's damage
		// value to floor((1 - clamp(damageFrac + base, 0, 1)) * maxDamage). The junk/treasure items
		// carrying this (leather_boots/leggings, fishing_rod, bow, tripwire_hook) are ALL damageable,
		// so the float draw ALWAYS happens — the draw-order-faithful behavior. The durability WRITE
		// is a CITED STUB: the roll ItemStack carries no max_damage/damage component yet, so the
		// setDamageValue is a no-op here (structured to write a Damage component when durability lands).
		// The RNG draw — the load-bearing part for seed reproduction — is faithful.
		// Source: javap SetItemDamageFunction.run (isDamageableItem branch; damage.getFloat draw).
		damageRaw, ok := rf["damage"]
		if !ok {
			return nil, fmt.Errorf("set_damage missing damage")
		}
		np, perr := parseNumberProvider(damageRaw)
		if perr != nil {
			return nil, fmt.Errorf("set_damage damage: %w", perr)
		}
		add := false
		if raw, ok := rf["add"]; ok {
			if err := json.Unmarshal(raw, &add); err != nil {
				return nil, fmt.Errorf("set_damage add: %w", err)
			}
		}
		inner = &setItemDamageFunction{damage: np, add: add}
	case "set_potion":
		// SetPotionFunction (the fishing junk table's water bottle -> water potion): sets the
		// stack's POTION_CONTENTS component to the named potion. It draws NO RNG (a pure component
		// set), so for seed reproduction it is a faithful NO-OP over the roll ItemStack (which has
		// no potion component yet). The item id (potion) is unchanged; only the component differs.
		// Structured to write a PotionContents component when that component subsystem lands.
		// Source: javap SetPotionFunction.run (itemStack.update(POTION_CONTENTS, ...) — no rng).
		inner = &setPotionFunction{}
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
	fn := &applyBonusCount{enchantment: ench, bonusMultiplier: 1}
	switch normalizeType(formulaStr) {
	case "ore_drops", "": // bare/absent defaults to ore_drops (the only block-table form)
		fn.formula = formulaOreDrops
	case "uniform_bonus_count":
		// UniformBonusCount codec: the nested `parameters.bonusMultiplier` int.
		fn.formula = formulaUniformBonus
		if bm, ok, perr := applyBonusIntParam(rf, "bonusMultiplier"); perr != nil {
			return nil, perr
		} else if ok {
			fn.bonusMultiplier = bm
		}
	case "binomial_with_bonus_count":
		// BinomialWithBonusCount codec: the nested `parameters.extraRounds` int + `parameters.probability` float.
		fn.formula = formulaBinomialBonus
		if er, ok, perr := applyBonusIntParam(rf, "extraRounds"); perr != nil {
			return nil, perr
		} else if ok {
			fn.extraRounds = er
		}
		if p, ok, perr := applyBonusFloatParam(rf, "probability"); perr != nil {
			return nil, perr
		} else if ok {
			fn.probability = p
		}
	default:
		return nil, fmt.Errorf("apply_bonus unknown formula %q", formulaStr)
	}
	return fn, nil
}

// applyBonusIntParam reads an int formula parameter, checking both the nested `parameters.<name>`
// object (the vanilla ApplyBonusCount codec dispatches the formula's fields under "parameters") and
// the flat top-level `<name>` form. Returns (value, present, error).
func applyBonusIntParam(rf rawFunction, name string) (int, bool, error) {
	if raw, ok := rf["parameters"]; ok {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(raw, &params); err != nil {
			return 0, false, fmt.Errorf("apply_bonus parameters: %w", err)
		}
		if v, ok := params[name]; ok {
			var n int
			if err := json.Unmarshal(v, &n); err != nil {
				return 0, false, fmt.Errorf("apply_bonus %s: %w", name, err)
			}
			return n, true, nil
		}
	}
	if raw, ok := rf[name]; ok {
		var n int
		if err := json.Unmarshal(raw, &n); err != nil {
			return 0, false, fmt.Errorf("apply_bonus %s: %w", name, err)
		}
		return n, true, nil
	}
	return 0, false, nil
}

// applyBonusFloatParam reads a float formula parameter (nested `parameters.<name>` or flat top-level).
func applyBonusFloatParam(rf rawFunction, name string) (float32, bool, error) {
	if raw, ok := rf["parameters"]; ok {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(raw, &params); err != nil {
			return 0, false, fmt.Errorf("apply_bonus parameters: %w", err)
		}
		if v, ok := params[name]; ok {
			var fl float32
			if err := json.Unmarshal(v, &fl); err != nil {
				return 0, false, fmt.Errorf("apply_bonus %s: %w", name, err)
			}
			return fl, true, nil
		}
	}
	if raw, ok := rf[name]; ok {
		var fl float32
		if err := json.Unmarshal(raw, &fl); err != nil {
			return 0, false, fmt.Errorf("apply_bonus %s: %w", name, err)
		}
		return fl, true, nil
	}
	return 0, false, nil
}

// smeltItemFunction is the port of net.minecraft.world.level.storage.loot.functions.SmeltItemFunction
// run: it replaces the stack with its SMELTING-recipe result. In the entity tables it is ALWAYS wrapped
// by an any_of(entity_properties) gate (the victim on fire OR the attacker's weapon smelting loot), so
// the conditionalFunction gate decides whether it runs; in v1 (pig not on fire, attacker no smelts_loot)
// the gate is FALSE and Run is never reached. The smelting-recipe lookup at loot time (RecipeManager
// .getRecipeFor) is not wired in v1 — Run is a CITED NO-OP guaranteed unreachable by the gate; when a
// loot-time recipe lookup lands it slots in here with the gate already correct (never baked away).
//
//	[VERIFIED javap SmeltItemFunction.run: if stack.isEmpty return; recipe = recipeAccess.getRecipeFor(
//	 SMELTING, SingleRecipeInput(stack), level); if present -> result.copyWithCount(stack.count) else stack.]
type smeltItemFunction struct{}

func (s *smeltItemFunction) Run(stack *ItemStack, _ *LootContext) *ItemStack {
	// CITED NO-OP: in v1 the wrapping any_of gate is false (so this is unreachable), and no
	// loot-time smelting-recipe table is wired. Returning the stack unchanged is the exact
	// behavior when no smelting recipe matches (SmeltItemFunction.run's else branch).
	return stack
}

// enchantedCountIncrease is the port of EnchantedCountIncreaseFunction.run: the looting count bonus.
//
//	LivingEntity attacker = ctx.getOptionalParameter(ATTACKING_ENTITY) as LivingEntity;
//	int level = EnchantmentHelper.getEnchantmentLevel(this.enchantment, attacker);  // e.g. looting
//	if (level == 0) return stack;                                                    // the v1 path
//	float bonus = (float) level * this.count.getFloat(ctx);
//	stack.grow(Math.round(bonus));
//	if (hasLimit() && stack.getCount() > limit) stack.setCount(limit);
//	return stack;
//
// In v1 the attacker carries no enchantments, so AttackerLootingLevel is 0 and the function returns
// the stack unchanged (the `if (level == 0) return` early return). The level read is the cited stub
// (ctx.AttackerLootingLevel, v1 default 0) — structured to become a real EnchantmentHelper read when
// an enchantment subsystem lands, with the grow formula already exact.
//
//	[VERIFIED javap EnchantedCountIncreaseFunction.run: getEnchantmentLevel; ifne; (level==0)->areturn;
//	 i2f; count.getFloat; fmul; Math.round; ItemStack.grow; hasLimit -> clamp.]
type enchantedCountIncrease struct {
	count NumberProvider
	limit int // -1 == no limit (hasLimit() false)
}

func (e *enchantedCountIncrease) Run(stack *ItemStack, ctx *LootContext) *ItemStack {
	level := ctx.AttackerLootingLevel
	if level == 0 {
		return stack // the v1 path: no looting enchant on the attacker.
	}
	bonus := float32(level) * e.count.GetFloat(ctx)
	stack.Count += mathRound(bonus)
	if e.limit >= 0 && stack.Count > e.limit {
		stack.Count = e.limit
	}
	return stack
}
