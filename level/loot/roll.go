package loot

// roll.go — the LootTable/LootPool roll engine: the determinism core. Ported
// LITERALLY from the decompiled bytecode (the draw order is load-bearing for
// per-seed reproduction — any reordering breaks vanilla parity, 20-RESEARCH
// Pitfall 1). Roll is the single public entry point both 20-02's block_drop.go and
// the chest-open path call.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.storage.loot.LootTable.getRandomItemsRaw (pool loop + table compositeFunction)
//   - net.minecraft.world.level.storage.loot.LootPool.addRandomItems (rolls = rolls.getInt + floor(bonusRolls.getFloat*luck); for i<rolls addRandomItem)
//   - net.minecraft.world.level.storage.loot.LootPool.addRandomItem (eligible list + weight sum; size==0||total==0 return; size==1 fast path; else nextInt(total) subtract-to-select)
//   - net.minecraft.world.level.storage.loot.entries.LootPoolSingletonContainer$1.createItemStack (compositeFunction decorate then abstract createItemStack)
//   - net.minecraft.world.level.storage.loot.entries.LootItem.createItemStack (new ItemStack(item))
//   - net.minecraft.world.level.storage.loot.entries.EmptyLootItem.createItemStack (no-op)

import (
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/imhinotori/sulfur/level/component"
)

// Roll evaluates a loot table at a fixed lootTableSeed and returns the rolled
// stacks as level/component.SlotData (the result element type 20-02's block_drop.go
// and the chest BE write consume). It builds the LootContext (LegacyRandomSource
// seeded by seed; luck 0 for chests) and walks getRandomItemsRaw.
//
// This is the per-seed entry point: a given (table, seed) reproduces the EXACT
// vanilla item list because the LCG draws + the pool/weight selection are ported
// byte-for-byte from the bytecode.
//
// Source: javap LootTable.getRandomItems(LootParams, long) -> withOptionalRandomSeed(seed) -> getRandomItemsRaw.
func Roll(table *LootTable, seed int64, ctx *LootContext) []component.SlotData {
	if ctx == nil {
		ctx = NewLootContext(seed, 0)
	}
	var out []ItemStack
	getRandomItemsRaw(table, ctx, func(s ItemStack) {
		out = append(out, s)
	})
	res := make([]component.SlotData, 0, len(out))
	for _, s := range out {
		res = append(res, component.SlotData{
			Count:  pk.VarInt(s.Count),
			ItemID: pk.VarInt(s.ItemID),
		})
	}
	return res
}

// RollStacks is Roll without the SlotData conversion — it returns the raw in-flight
// ItemStacks (carrying Enchantments) so tests can assert item id + count + enchants
// directly, and so 20-02's chest BE write can read the enchantments component.
func RollStacks(table *LootTable, ctx *LootContext) []ItemStack {
	var out []ItemStack
	getRandomItemsRaw(table, ctx, func(s ItemStack) {
		out = append(out, s)
	})
	return out
}

// getRandomItemsRaw mirrors LootTable.getRandomItemsRaw(LootContext, Consumer):
// the table-level compositeFunction wraps the consumer (identity for the chest
// tables, which have no table-level functions), then each pool's addRandomItems
// runs in order. The vanilla visited-set infinite-loop guard is omitted (the chest
// tables have no self-referential loot_table entries; reference entries are 20-02).
//
// Source: javap LootTable.getRandomItemsRaw.
func getRandomItemsRaw(table *LootTable, ctx *LootContext, emit func(ItemStack)) {
	consumer := decorateTable(table.Functions, emit, ctx)
	for _, p := range table.Pools {
		addRandomItems(p, ctx, consumer)
	}
}

// decorateTable wraps the terminal consumer with the table-level functions (applied
// in order). For the chest tables this list is empty so the consumer is unchanged.
//
// Source: javap LootItemFunction.decorate (compositeFunction over the consumer).
func decorateTable(fns []LootFunction, emit func(ItemStack), ctx *LootContext) func(ItemStack) {
	if len(fns) == 0 {
		return emit
	}
	return func(s ItemStack) {
		st := applyFunctions(fns, &s, ctx)
		emit(*st)
	}
}

// addRandomItems mirrors LootPool.addRandomItems(Consumer, LootContext):
//
//	if (!compositeCondition.test(ctx)) return;
//	consumer = decorate(compositeFunction, consumer, ctx);     // pool-level functions
//	int rolls = rolls.getInt(ctx) + Mth.floor(bonusRolls.getFloat(ctx) * ctx.getLuck());
//	for (int i = 0; i < rolls; i++) addRandomItem(consumer, ctx);
//
// bonusRolls defaults to ConstantValue(0) and chests have luck 0, so the bonus term
// is 0. The rolls count is capped at maxRolls (T-20-01 DoS: a malformed huge
// `rolls` must not drive an unbounded loop).
//
// Source: javap LootPool.addRandomItems.
func addRandomItems(p *LootPool, ctx *LootContext, emit func(ItemStack)) {
	if !allConditions(p.Conditions, ctx) {
		return
	}
	consumer := emit
	if len(p.Functions) > 0 {
		fns := p.Functions
		consumer = func(s ItemStack) {
			st := applyFunctions(fns, &s, ctx)
			emit(*st)
		}
	}
	rolls := p.Rolls.GetInt(ctx) + mthFloor(p.BonusRolls.GetFloat(ctx)*ctx.Luck())
	if rolls > maxRolls {
		rolls = maxRolls // defensive cap (T-20-01); vanilla chest tables roll <= ~8
	}
	for i := 0; i < rolls; i++ {
		addRandomItem(p, ctx, consumer)
	}
}

// addRandomItem mirrors LootPool.addRandomItem(Consumer, LootContext) — the
// weighted-selection determinism core:
//
//	RandomSource rng = ctx.getRandom();
//	List eligible = []; MutableInt total = 0;
//	for (entry : entries) entry.expand(ctx, e -> { int w = e.getWeight(luck); if (w>0){ eligible.add(e); total += w; } });
//	int size = eligible.size();
//	if (total.intValue() == 0 || size == 0) return;
//	if (size == 1) { eligible[0].createItemStack(consumer, ctx); return; }
//	int r = rng.nextInt(total);
//	for (e : eligible) { r -= e.getWeight(luck); if (r < 0) { e.createItemStack(consumer, ctx); return; } }
//
// The single-eligible fast path SKIPS the nextInt(total) draw (the jar fast-path) —
// this is observable in the RNG sequence and must be preserved.
//
// Source: javap LootPool.addRandomItem.
func addRandomItem(p *LootPool, ctx *LootContext, emit func(ItemStack)) {
	rng := ctx.Random()
	var eligible []*Entry
	total := 0
	for _, e := range p.Entries {
		// LootPoolEntryContainer.expand: canRun(ctx) ? accept(entry) : skip.
		// For a singleton (item/empty) the expanded entry is the entry itself.
		expand(e, ctx, func(en *Entry) {
			w := getWeight(en, ctx.Luck())
			if w > 0 {
				eligible = append(eligible, en)
				total += w
			}
		})
	}
	size := len(eligible)
	if total == 0 || size == 0 {
		return
	}
	if size == 1 {
		createItemStack(eligible[0], ctx, emit)
		return
	}
	r := int(rng.NextIntN(int32(total)))
	for _, en := range eligible {
		r -= getWeight(en, ctx.Luck())
		if r < 0 {
			createItemStack(en, ctx, emit)
			return
		}
	}
}

// expand mirrors LootPoolEntryContainer.expand. For a singleton (item/empty):
// `if (canRun(ctx)) accept(entry); return canRun;`. For a composite entry
// (alternatives/group/sequence — CompositeEntryBase.expand): `if (!canRun) return
// false; return composedChildren.expand(ctx, accept)`. The composed form differs per
// container:
//
//   - alternatives (OR): the first child whose own expand returns true wins; later
//     children are NOT visited (AlternativesEntry.compose -> ComposableEntryContainer.or
//     short-circuit). This is the block-table silk-touch-or-drop semantics.
//
// canRun tests the entry's compositeCondition (per-entry conditions, e.g. mineshaft's
// location_check or a block child's match_tool/survives_explosion).
//
// Sources: javap LootPoolSingletonContainer.expand / canRun; CompositeEntryBase.expand;
// AlternativesEntry.compose (the OR over children, first-passing wins).
func expand(e *Entry, ctx *LootContext, accept func(*Entry)) bool {
	if !allConditions(e.Conditions, ctx) { // canRun
		return false
	}
	switch normalizeType(e.Type) {
	case "alternatives":
		// CompositeEntryBase.expand -> the composed OR: visit children in order, the
		// FIRST whose expand returns true wins and we stop (return true). If none
		// expand, return false (the alternatives entry contributes nothing).
		for _, child := range e.Children {
			if expand(child, ctx, accept) {
				return true
			}
		}
		return false
	default:
		// A singleton (item/empty): accept self.
		accept(e)
		return true
	}
}

// getWeight mirrors LootPoolSingletonContainer.getWeight(luck): the vanilla formula
// is `Math.max(Mth.floor(weight + quality*luck), 0)`. With chest luck 0 and quality
// 0 this is just `weight`. The general form is kept so a luck/quality table rolls
// faithfully.
//
// Source: javap LootPoolSingletonContainer.getWeight(F).
func getWeight(e *Entry, luck float32) int {
	w := mthFloor(float32(e.Weight) + float32(e.Quality)*luck)
	if w < 0 {
		return 0
	}
	return w
}

// createItemStack mirrors the entry's createItemStack chain
// (LootPoolSingletonContainer$1.createItemStack): wrap the consumer with the
// entry's compositeFunction (functions applied IN JSON ORDER via decorate), then
// call the abstract createItemStack (LootItem makes ItemStack(item, 1) and accepts;
// EmptyLootItem is a no-op).
//
// The function order is load-bearing: each function draws its own RNG in sequence
// (20-RESEARCH Pitfall 1). DO NOT reorder.
//
// Source: javap LootPoolSingletonContainer$1.createItemStack + LootItem/EmptyLootItem.createItemStack.
func createItemStack(e *Entry, ctx *LootContext, emit func(ItemStack)) {
	// EmptyLootItem.createItemStack: return (no-op). An empty entry participates in
	// weighted selection but emits nothing — its functions (none in vanilla) would
	// still not run because the abstract createItemStack never produces a stack.
	if normalizeType(e.Type) == "empty" {
		return
	}
	// LootItem.createItemStack: new ItemStack(item) (count defaults to 1).
	stack := ItemStack{ItemID: e.itemID, Count: 1}
	// Apply the entry's functions in order (the compositeFunction decorate wrapper).
	st := applyFunctions(e.Functions, &stack, ctx)
	emit(*st)
}
