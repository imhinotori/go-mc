package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// damageableStack (defined in grindstone_test.go) builds a SlotData carrying MAX_DAMAGE + DAMAGE
// components (a vanilla-created tool), so stackIsDamageableItem is true — reused here.

// TestStackHurtAndBreakWears: a damageable item takes damage and does NOT break until it reaches max.
func TestStackHurtAndBreakWears(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := damageableStack(100, 1, 10, 0) // maxDamage 10, damage 0
	next, broke := loop.stackHurtAndBreak(s, 1, false)
	if broke {
		t.Fatalf("a tool at damage 1/10 must not break")
	}
	if got := stackDamageValue(next); got != 1 {
		t.Fatalf("damage value after 1 hit = %d, want 1", got)
	}
}

// TestStackHurtAndBreakBreaks: at max damage the item breaks (shrinks by 1 -> empty for the last one).
func TestStackHurtAndBreakBreaks(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := damageableStack(100, 1, 10, 9) // one hit from max
	next, broke := loop.stackHurtAndBreak(s, 1, false)
	if !broke {
		t.Fatalf("a tool reaching max damage must break")
	}
	if !stackEmpty(next) {
		t.Fatalf("the last item breaking must leave an empty stack, got count=%d", next.Count)
	}
}

// TestStackHurtAndBreakCreative: a creative player's gear never wears.
func TestStackHurtAndBreakCreative(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := damageableStack(100, 1, 10, 9)
	next, broke := loop.stackHurtAndBreak(s, 1, true) // creative == true
	if broke {
		t.Fatalf("creative gear must not break")
	}
	if got := stackDamageValue(next); got != 9 {
		t.Fatalf("creative gear must not wear: damage = %d, want 9", got)
	}
}

// TestStackHurtAndBreakUndamageable: an item with no MAX_DAMAGE/DAMAGE components never wears.
func TestStackHurtAndBreakUndamageable(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := component.SlotData{ItemID: 1, Count: 1} // a plain block, no durability components
	next, broke := loop.stackHurtAndBreak(s, 1, false)
	if broke {
		t.Fatalf("an undamageable item must not break")
	}
	if stackDamageValue(next) != 0 {
		t.Fatalf("an undamageable item must not accrue damage")
	}
}

// TestStackIsBroken: isBroken is true exactly when a damageable item's damage reaches its max.
func TestStackIsBroken(t *testing.T) {
	if stackIsBroken(damageableStack(100, 1, 10, 9)) {
		t.Fatalf("damage 9/10 is not broken")
	}
	if !stackIsBroken(damageableStack(100, 1, 10, 10)) {
		t.Fatalf("damage 10/10 must be broken")
	}
	if stackIsBroken(component.SlotData{ItemID: 1, Count: 1}) {
		t.Fatalf("an undamageable item is never broken")
	}
}

// TestStackToolDamagePerBlock reads the tool component's damage_per_block, absent on a non-tool.
func TestStackToolDamagePerBlock(t *testing.T) {
	s := damageableStack(100, 1, 100, 0)
	p := component.DecodePatch(s)
	p.Set(compTool, &component.Tool{DamagePerBlock: 2})
	s = p.ApplyTo(s)
	if dpb, ok := stackToolDamagePerBlock(s); !ok || dpb != 2 {
		t.Fatalf("tool damage_per_block = (%d,%v), want (2,true)", dpb, ok)
	}
	if _, ok := stackToolDamagePerBlock(component.SlotData{ItemID: 1, Count: 1}); ok {
		t.Fatalf("a non-tool item must have no tool damage_per_block")
	}
}

func TestStackToolDamagePerBlockUsesItemDefault(t *testing.T) {
	pick := plainTool(int32(item.DiamondPickaxe.ID))
	if dpb, ok := stackToolDamagePerBlock(pick); !ok || dpb != 1 {
		t.Fatalf("default pickaxe damage_per_block = (%d,%v), want (1,true)", dpb, ok)
	}
	sword := plainTool(int32(item.DiamondSword.ID))
	if dpb, ok := stackToolDamagePerBlock(sword); !ok || dpb != 2 {
		t.Fatalf("default sword damage_per_block = (%d,%v), want (2,true)", dpb, ok)
	}
}

// TestStackMaxDamageOverridePatch: an added MAX_DAMAGE patch overrides the item's registered default.
// The vanilla reading is ItemStack.getMaxDamage = getOrDefault(MAX_DAMAGE, prototype.MAX_DAMAGE):
// an explicit patch wins, the default is the fallback. Cited: ItemStack.getMaxDamage.
func TestStackMaxDamageOverridePatch(t *testing.T) {
	pick := plainTool(int32(item.DiamondPickaxe.ID)) // default 1561
	override := component.Patch{}
	override.Set(compMaxDamage, &component.MaxDamage{VarInt: pk.VarInt(7)})
	pick = override.ApplyTo(pick)
	if got := stackMaxDamage(pick); got != 7 {
		t.Fatalf("overridden max_damage = %d, want 7 (patch value wins over default 1561)", got)
	}
}

// TestStackMaxDamageRemovedPatch: a removed MAX_DAMAGE marker means the component is absent (the
// patch drops the per-item default entirely). The faithful reading is 0 — the item is no longer
// damageable. Cited: PatchedDataComponentMap + DataComponentPatch.removed.
func TestStackMaxDamageRemovedPatch(t *testing.T) {
	pick := plainTool(int32(item.DiamondPickaxe.ID)) // default 1561
	removed := component.Patch{Removed: []int32{compMaxDamage}}.ApplyTo(pick)
	if got := stackMaxDamage(removed); got != 0 {
		t.Fatalf("removed max_damage = %d, want 0 (absent -> 0, default dropped)", got)
	}
	if stackIsDamageableItem(removed) {
		t.Fatal("a stack with a removed MAX_DAMAGE marker must not be damageable")
	}
}

// TestStackMaxDamageFallsThroughToItemDefault: a stack carrying no MAX_DAMAGE patch reads the
// item's per-item default (Item.components().MAX_DAMAGE), exactly as a vanilla-created diamond
// pickaxe on the wire does. Cited: PatchedDataComponentMap.get + Item.components().
func TestStackMaxDamageFallsThroughToItemDefault(t *testing.T) {
	for _, tc := range []struct {
		name string
		it   item.Item
		want int
	}{
		{"diamond_pickaxe", item.DiamondPickaxe, 1561},
		{"diamond_sword", item.DiamondSword, 1561},
		{"flint_and_steel", item.FlintAndSteel, 64},
		{"elytra", item.Elytra, 432},
		{"shield", item.Shield, 336},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := plainTool(int32(tc.it.ID))
			if got := stackMaxDamage(s); got != tc.want {
				t.Fatalf("default max_damage for %s = %d, want %d", tc.name, got, tc.want)
			}
			if !stackIsDamageableItem(s) {
				t.Fatalf("default %s must be damageable", tc.name)
			}
			if !isDamageableItem(s) {
				t.Fatalf("enchant/anvil predicate must treat default %s as damageable", tc.name)
			}
		})
	}
}

// TestStackMaxDamageAbsentForNonDamageableItem: a non-damageable item (no item-level max_damage
// default) reads 0 and is not damageable. Cited: PatchedDataComponentMap.get (no patch + no
// default -> null -> 0).
func TestStackMaxDamageAbsentForNonDamageableItem(t *testing.T) {
	stone := plainTool(int32(item.Stone.ID))
	if got := stackMaxDamage(stone); got != 0 {
		t.Fatalf("stone max_damage = %d, want 0 (no default)", got)
	}
	if stackIsDamageableItem(stone) {
		t.Fatal("stone must not be damageable")
	}
}

// TestStackDamageValueDefaultZero: absent or removed DAMAGE resolves to 0 (no per-item default).
// Cited: ItemStack.getDamageValue (getOrDefault(DAMAGE, 0)).
func TestStackDamageValueDefaultZero(t *testing.T) {
	pick := plainTool(int32(item.DiamondPickaxe.ID)) // has max_damage but no DAMAGE patch
	if got := stackDamageValue(pick); got != 0 {
		t.Fatalf("default damage value = %d, want 0", got)
	}
	removed := component.Patch{Removed: []int32{compDamage}}.ApplyTo(pick)
	if got := stackDamageValue(removed); got != 0 {
		t.Fatalf("removed-DAMAGE damage value = %d, want 0", got)
	}
}

// TestStackDamageValueClampedToMax: a DAMAGE patch above maxDamage clamps to maxDamage. Cited:
// ItemStack.getDamageValue (Mth.clamp(value, 0, getMaxDamage)).
func TestStackDamageValueClampedToMax(t *testing.T) {
	s := damageableStack(int(item.DiamondPickaxe.ID), 1, 100, 50)
	if got := stackDamageValue(s); got != 50 {
		t.Fatalf("damage value in range = %d, want 50", got)
	}
	s = setStackDamageValue(s, 99999)
	if got := stackDamageValue(s); got != 100 {
		t.Fatalf("damage value above max clamped to %d, want 100", got)
	}
}

// TestPlainDiamondPickaxeMiningCreatesDamagePatch: mining a block with a plain (no patch) diamond
// pickaxe creates a DAMAGE patch incremented by the tool's damage_per_block (1). The patch is the
// first write — there is no DAMAGE on the stack before mining. Cited: Item.mineBlock ->
// ItemStack.hurtAndBreak + applyDamage + setDamageValue.
func TestPlainDiamondPickaxeMiningCreatesDamagePatch(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := toolPlayer(loop, plainTool(int32(item.DiamondPickaxe.ID)))
	p.gameMode = gameModeSurvival

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)
	completeSurvivalDig(loop, p, target)

	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if got := stackDamageValue(held); got != 1 {
		t.Fatalf("plain diamond_pickaxe damage after mining stone = %d, want 1 (damage_per_block)", got)
	}
}

// TestPlainDiamondSwordAttackCreatesDamagePatch: a plain diamond sword attack creates a DAMAGE
// patch incremented by the weapon's item_damage_per_attack (2). The plain stack carries no patch
// at all (no DAMAGE), so the first hit must materialize a fresh DAMAGE=2 patch. Cited:
// ItemStack.postHurtEnemy -> hurtAndBreak + setDamageValue.
func TestPlainDiamondSwordAttackCreatesDamagePatch(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := plainTool(int32(item.DiamondSword.ID))
	// Diamond sword weapon.item_damage_per_attack == 2 (verified in the per-item report).
	next, broke := loop.stackHurtAndBreak(s, 2, false)
	if broke {
		t.Fatal("a single hit on a 1561-durability sword must not break")
	}
	if got := stackDamageValue(next); got != 2 {
		t.Fatalf("plain diamond_sword damage after one hit = %d, want 2", got)
	}
}

// TestHurtHeldItemCreativeNoWear: mining a block with a plain diamond pickaxe in creative mode
// applies NO wear (ItemStack.processDurabilityChange returns 0 for a creative player, and
// mineBlockDurability short-circuits on creative). Cited: ItemStack.processDurabilityChange
// (creative -> 0) + Item.mineBlock.
func TestHurtHeldItemCreativeNoWear(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := toolPlayer(loop, plainTool(int32(item.DiamondPickaxe.ID)))
	p.gameMode = gameModeCreative

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)
	// Creative breaks on START (no STOP needed) — matching the TestCreativeToolDestroyPolicy
	// pattern. completeSurvivalDig forces survival gameMode, which is wrong here.
	startDig(loop, p, target, 1)

	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if got := stackDamageValue(held); got != 0 {
		t.Fatalf("creative pickaxe damage after breaking stone = %d, want 0", got)
	}
}

// TestMineBlockZeroHardnessNoWear: mining a zero-hardness block (an air-like state with no
// destroySpeed) applies NO wear, regardless of the tool's damage_per_block. Cited: Item.mineBlock
// (getDestroySpeed(state) == 0 -> no hurtAndBreak).
func TestMineBlockZeroHardnessNoWear(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := toolPlayer(loop, plainTool(int32(item.DiamondPickaxe.ID)))
	p.gameMode = gameModeSurvival

	// An air block: destroySpeed 0. The bare mining flow must not wear the pickaxe.
	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Air{}], dimMinY)
	completeSurvivalDig(loop, p, target)

	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if got := stackDamageValue(held); got != 0 {
		t.Fatalf("diamond_pickaxe damage after mining zero-hardness block = %d, want 0", got)
	}
}
