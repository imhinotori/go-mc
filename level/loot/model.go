package loot

// model.go — the flat Go model of a vanilla loot table, shaped to mirror the
// decompiled net.minecraft.world.level.storage.loot record graph:
//
//	LootTable { pools[], functions[] }                       (LootTable)
//	  LootPool { rolls, bonusRolls, entries[], conditions[], functions[] }  (LootPool)
//	    Entry  { type, name, weight, quality, functions[], conditions[], children[] } (LootPoolEntryContainer / LootPoolSingletonContainer)
//
// The model is shaped (Conditions on pool+entry; Children on entry) so 20-02 adds
// the block-table delta (alternatives entries, match_tool/survives_explosion
// conditions) DATA-ONLY, without re-touching the parser. The chest groups use only
// item/empty entries; abandoned_mineshaft has exactly one entry-level
// location_check condition (the lone per-entry condition in the 5 groups), so the
// entry-condition path is real, not dead.

// LootTable mirrors net.minecraft.world.level.storage.loot.LootTable: an ordered
// list of pools plus an optional table-level function list (the chest tables have
// no table-level functions; the composite is identity). getRandomItemsRaw loops
// the pools in order.
type LootTable struct {
	Pools     []*LootPool
	Functions []LootFunction // table-level compositeFunction (identity for chests)
}

// LootPool mirrors net.minecraft.world.level.storage.loot.LootPool. addRandomItems
// computes rolls = rolls.getInt(ctx) + Mth.floor(bonusRolls.getFloat(ctx)*luck)
// and calls addRandomItem that many times. bonusRolls defaults to the
// ConstantValue(0) codec default; chests set no luck so the bonus term is 0.
type LootPool struct {
	Rolls      NumberProvider
	BonusRolls NumberProvider // defaults to ConstantValue(0)
	Entries    []*Entry
	Conditions []LootCondition // pool-level compositeCondition (none in chests)
	Functions  []LootFunction  // pool-level compositeFunction (none in chests)
}

// Entry mirrors a LootPoolSingletonContainer (item/empty) or a composite container
// (alternatives/group/sequence — shaped for 20-02 via Children). Weight defaults to
// 1 when absent; Quality defaults to 0. getWeight(luck) = quality==0 ? weight :
// weight + Math.round(quality*luck); with chest luck=0 and quality=0 it is weight.
type Entry struct {
	Type       string // "minecraft:item" | "minecraft:empty" | (20-02: "minecraft:alternatives" ...)
	Name       string // item id for an "item" entry, e.g. "minecraft:diamond"
	itemID     int32  // resolved at parse time from Name (item entries only)
	Weight     int    // default 1
	Quality    int    // default 0
	Functions  []LootFunction
	Conditions []LootCondition // per-entry compositeCondition (location_check in mineshaft)
	Children   []*Entry        // composite entries (alternatives/group/sequence) — 20-02
}

// NumberProvider mirrors
// net.minecraft.world.level.storage.loot.providers.number.NumberProvider. getInt's
// default (used by ConstantValue) is Math.round(getFloat(ctx)); UniformGenerator
// overrides getInt with Mth.nextInt. See provider.go for the ported bodies.
type NumberProvider interface {
	GetInt(ctx *LootContext) int
	GetFloat(ctx *LootContext) float32
}

// LootFunction mirrors net.minecraft.world.level.storage.loot.functions.LootItemFunction:
// a stack transformer applied (in JSON order) over a rolled ItemStack. The concrete
// ports (SetItemCount, EnchantRandomly, EnchantWithLevels) live in function.go /
// enchant.go. Run carries the per-function condition gate (LootItemConditionalFunction).
type LootFunction interface {
	// Run transforms stack in place (vanilla mutates the ItemStack) and returns it.
	Run(stack *ItemStack, ctx *LootContext) *ItemStack
}

// LootCondition mirrors net.minecraft.world.level.storage.loot.predicates.LootItemCondition:
// a predicate over the LootContext. The concrete ports live in condition.go.
type LootCondition interface {
	Test(ctx *LootContext) bool
}

// ItemStack is the in-flight rolled stack the roll engine mutates: an item id plus
// a count (the vanilla ItemStack carries components too; the chest groups only set
// count + enchantments, the latter recorded on Enchantments for the BE write in
// 20-02). The terminal Roll result is converted to level/component.SlotData.
type ItemStack struct {
	ItemID int32
	Count  int
	// Enchantments records (enchantment id -> level) applied by enchant_randomly /
	// enchant_with_levels. Recorded here so 20-02 can emit the enchantments
	// component on the chest BE; the count-only chest entries leave this nil.
	Enchantments map[string]int
}
