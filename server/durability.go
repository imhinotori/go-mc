package server

// durability.go — ITEM DURABILITY: a 1:1 port of net.minecraft.world.item.ItemStack.hurtAndBreak /
// processDurabilityChange / applyDamage / isBroken (temp/cache/26.2-inner.jar, javap this session). Every
// tool/weapon/armor/utility use that vanilla decrements durability on (flint&steel ignite, shears, fishing
// rod cast, a sword/tool hit, armor damage) routes through here so the item wears and BREAKS at max.
//
// The vanilla call chain (ItemStack.hurtAndBreak(int, ServerLevel, ServerPlayer, Consumer<Item>)):
//   int change = processDurabilityChange(amount, level, player);   // creative/undamageable => 0; else Unbreaking roll
//   if (change != 0) applyDamage(getDamageValue() + change, player, onBreak);
// processDurabilityChange (verified javap):
//   if (!isDamageableItem()) return 0;
//   if (player != null && player.hasInfiniteMaterials()) return 0;   // creative never wears gear
//   if (amount > 0) return EnchantmentHelper.processDurabilityChange(level, stack, amount);  // Unbreaking
//   return amount;
// applyDamage (verified javap): setDamageValue(newDamage); if (isBroken()) { item = getItem(); shrink(1);
//   onBreak.accept(item); }   (ITEM_DURABILITY_CHANGED advancement trigger is cite-deferred — no advancements.)
// isBroken == isDamageableItem() && getDamageValue() >= getMaxDamage().
//
// The Unbreaking reduction (EnchantmentHelper.processDurabilityChange, a per-enchant DigDurability visitor
// that probabilistically drops the damage) is CITE-DEFERRED to a passthrough: with no durability enchant on
// the stack the visitor returns amount unchanged, so a bare tool wears at the vanilla rate. Structured as a
// seam (enchantDurabilityChange) so it becomes a real read when the enchant-effect layer lands — never baked away.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
)

// compTool / compWeapon are the minecraft:tool / minecraft:weapon data-component wire type ids
// (level/component components table indices 28 and 29).
const (
	compTool   = 28
	compWeapon = 29
)

// enchantDurabilityChange is the EnchantmentHelper.processDurabilityChange seam: the Unbreaking-style
// reduction of a positive durability hit. v1 has no durability enchantment effect wired, so it returns the
// amount unchanged (the vanilla result when the stack carries no DigDurabilityEnchantment). CITE:
// EnchantmentHelper.processDurabilityChange (runIterationOnItem DigDurability visitor). A real read later
// rolls the Unbreaking probability off the stack's enchantments component.
func (t *TickLoop) enchantDurabilityChange(s component.SlotData, amount int) int {
	return amount
}

// stackProcessDurabilityChange ports ItemStack.processDurabilityChange: 0 for a non-damageable item or a
// creative player (gear never wears in creative); for a positive hit, the Unbreaking-reduced amount; else
// the raw amount. Cite ItemStack.processDurabilityChange.
func (t *TickLoop) stackProcessDurabilityChange(s component.SlotData, amount int, creative bool) int {
	if !stackIsDamageableItem(s) {
		return 0
	}
	if creative {
		return 0 // player.hasInfiniteMaterials()
	}
	if amount > 0 {
		return t.enchantDurabilityChange(s, amount)
	}
	return amount
}

// stackIsBroken ports ItemStack.isBroken(): a damageable item whose damage value has reached its max.
func stackIsBroken(s component.SlotData) bool {
	return stackIsDamageableItem(s) && stackDamageValue(s) >= stackMaxDamage(s)
}

// stackHurtAndBreak ports ItemStack.hurtAndBreak + applyDamage as a PURE stack transform: it returns the
// stack after applying `amount` durability damage, and broke=true when the item broke (reached max damage,
// so it shrinks by 1 — an empty stack when the last one broke). creative gates the wear entirely. The
// ITEM_DURABILITY_CHANGED advancement trigger is cite-deferred. Cite ItemStack.hurtAndBreak/applyDamage.
func (t *TickLoop) stackHurtAndBreak(s component.SlotData, amount int, creative bool) (component.SlotData, bool) {
	change := t.stackProcessDurabilityChange(s, amount, creative)
	if change == 0 {
		return s, false
	}
	// applyDamage: setDamageValue(getDamageValue() + change).
	s = setStackDamageValue(s, stackDamageValue(s)+change)
	if !stackIsBroken(s) {
		return s, false
	}
	// isBroken(): shrink(1) — the broken item is consumed (an empty stack if it was the last one). The
	// onBreak Consumer (break sound / equipment-slot broadcast) is cite-deferred to the caller.
	s.Count--
	if s.Count <= 0 {
		s = component.SlotData{Count: 0}
	}
	return s, true
}

// stackToolDamagePerBlock reads the held stack's minecraft:tool component's damage_per_block (the durability
// a tool loses per block mined). Returns (value, present) — present=false when the stack carries no TOOL
// component (a non-tool item, which never wears on a mine). Cite Item.mineBlock (tool.damagePerBlock()).
func stackToolDamagePerBlock(s component.SlotData) (int, bool) {
	if stackEmpty(s) {
		return 0, false
	}
	p := component.DecodePatch(s)
	if c, ok := p.Get(compTool).(*component.Tool); ok {
		return int(c.DamagePerBlock), true
	}
	return 0, false
}

// mineBlockDurability ports Item.mineBlock's durability rule: when a player breaks a block whose destroySpeed
// is non-zero with a tool whose TOOL component has damagePerBlock > 0, the tool takes that much durability
// (hurtAndBreak(damagePerBlock, player, MAINHAND)). A creative player, a zero-hardness block (destroySpeed 0),
// or a non-tool held item wears nothing. Called from destroyBlock after the block is removed. Cite
// Item.mineBlock (getDestroySpeed(state) != 0 && tool.damagePerBlock() > 0 -> hurtAndBreak).
func (t *TickLoop) mineBlockDurability(p *tickPlayer, brokenState block.StateID) {
	if p == nil || p.gameMode == gameModeCreative {
		return // hurtAndBreak short-circuits on creative anyway; skip the component decode entirely
	}
	// getDestroySpeed(state) != 0: a zero-hardness block (air-like / instant) does not wear a tool.
	speed, _ := blockHardness(brokenState)
	if speed == 0 {
		return
	}
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	dpb, ok := stackToolDamagePerBlock(held)
	if !ok || dpb <= 0 {
		return // no TOOL component, or damage_per_block 0 -> no wear
	}
	t.hurtHeldItem(p, inv, dpb) // hurtAndBreak(tool.damagePerBlock(), player, MAINHAND)
}

// postHurtEnemyDurability ports ItemStack.postHurtEnemy's weapon-durability tail: after a landed melee hit,
// if the held stack carries a minecraft:weapon component, it takes weapon.itemDamagePerAttack() durability
// (hurtAndBreak(itemDamagePerAttack, attacker, MAINHAND)) — a sword/axe wears 1 per hit and breaks at max.
// A non-weapon held item (fist / block) wears nothing. Creative wears nothing (gated in hurtHeldItem).
// Cite ItemStack.postHurtEnemy (WEAPON component -> hurtAndBreak).
func (t *TickLoop) postHurtEnemyDurability(p *tickPlayer) {
	if p == nil || p.gameMode == gameModeCreative {
		return
	}
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if stackEmpty(held) {
		return
	}
	pt := component.DecodePatch(held)
	w, ok := pt.Get(compWeapon).(*component.Weapon)
	if !ok {
		return // no WEAPON component -> not a weapon, no wear
	}
	dmg := int(w.ItemDamagePerAttack)
	if dmg <= 0 {
		return
	}
	t.hurtHeldItem(p, inv, dmg) // hurtAndBreak(weapon.itemDamagePerAttack(), attacker, MAINHAND)
}

// hurtHeldItem is the player-facing hurtAndBreak for the MAIN-HAND stack (the common caller: flint&steel,
// shears, fishing rod, a tool hit). It reads the held slot, applies `amount` durability damage, writes the
// result back (a broken item shrinks by 1), and broadcasts the single changed slot to the client. Returns
// broke=true when the item broke this call (so the caller can play the break sound seam). Creative players'
// gear never wears (processDurabilityChange returns 0). Cite ItemStack.hurtAndBreak(int, LivingEntity, hand).
func (t *TickLoop) hurtHeldItem(p *tickPlayer, inv *Inventory, amount int) bool {
	slot := heldWindowSlot(inv.heldSlot)
	cur := inv.get(slot)
	if stackEmpty(cur) {
		return false
	}
	// No-op fast path: undamageable item or a creative player -> processDurabilityChange is 0, so the stack
	// is untouched and no slot broadcast is needed (SlotData carries a slice, so compare via the change amount).
	if t.stackProcessDurabilityChange(cur, amount, p.gameMode == gameModeCreative) == 0 {
		return false
	}
	next, broke := t.stackHurtAndBreak(cur, amount, p.gameMode == gameModeCreative)
	before := inv.snapshot()
	inv.set(slot, next)
	t.broadcastInventoryChanges(p, inv, before)
	return broke
}
