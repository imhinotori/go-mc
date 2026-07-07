package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// shield_blocking.go is the 1:1 port of the SHIELD / minecraft:blocks_attacks damage-blocking path
// (Fable audit E-2). In 26.2 a shield has no dedicated ShieldItem class; blocking is driven entirely
// by the minecraft:blocks_attacks item component (BlocksAttacks). A player holding right-click with a
// blocks_attacks item enters the item-use state (Item.use: if the stack has BLOCKS_ATTACKS ->
// startUsingItem(hand) and return CONSUME); after the block delay the item blocks damage from the front.
//
// Literal port of:
//   - LivingEntity.hurtServer: offset 79 calls f6 = applyItemBlocking(level, source, amount); offset
//     84-88 subtracts it (amount -= f6). This runs BEFORE the invulnerableTime i-frame gate (offset 184,
//     (float) invulnerableTime > 10.0F). Blocking is resolved on EVERY hit.
//   - LivingEntity.getItemBlockingWith(): useItem iff isUsingItem() && useItem has BLOCKS_ATTACKS &&
//     (getUseDuration(useItem) - useItemRemaining) >= blockDelayTicks.
//   - LivingEntity.applyItemBlocking: bypassed_by / arrow-pierce guards, the front-cone angle check
//     (acos of the dot of the head look-vector vs the horizontal source direction),
//     BlocksAttacks.resolveBlockedDamage, BlocksAttacks.hurtBlockingItem, blockUsingItem.
//   - BlocksAttacks + DamageReduction + ItemDamageFunction.
//
// v1 has no item default-component prototype table (Item carries no components; a stack keeps its
// components only in its patch). getItemBlockingWith reads useItem.get(BLOCKS_ATTACKS), which in vanilla
// merges item defaults. shieldBlocksAttacks resolves it from (a) the stack patch if present, else (b)
// the cited default from temp/jsons/26.2/items.json for the item id.

// shieldItemID is minecraft:shield item registry id (data/item/item.go Shield.ID == 1325).
var shieldItemID = int32(item.Shield.ID)

// compBlocksAttacksWire is the minecraft:blocks_attacks data-component wire type id (level/component
// components table index 37 -> NewComponent(37) == *BlocksAttacks).
const compBlocksAttacksWire = 37

// blocksAttacksItemUseDuration is Item.getUseDuration for a blocks_attacks item: ldc 72000; ireturn.
const blocksAttacksItemUseDuration = int32(72000)

// shieldDefaultBlocksAttacks is the minecraft:blocks_attacks component the shield carries by default,
// transcribed from temp/jsons/26.2/items.json (minecraft:shield). block_delay_seconds 0.25; item_damage
// {base 1.0, factor 1.0, threshold 3.0}; damage_reductions ABSENT -> the BlocksAttacks CODEC default
// List.of(new DamageReduction(90.0F, Optional.empty(), 0.0F, 1.0F)) = one reduction (angle 90deg, no
// type, base 0, factor 1.0 == full front block). Cite BlocksAttacks.CODEC default + DamageReduction ctor.
var shieldDefaultBlocksAttacks = &component.BlocksAttacks{
	BlockDelaySeconds:    pk.Float(0.25),
	DisableCooldownScale: pk.Float(1.0),
	DamageReductions: []component.DamageReduction{{
		HorizontalBlockingAngle: pk.Float(90.0),
		Base:                    pk.Float(0.0),
		Factor:                  pk.Float(1.0),
	}},
	ItemDamage: component.ItemDamageFunction{
		Threshold: pk.Float(3.0),
		Base:      pk.Float(1.0),
		Factor:    pk.Float(1.0),
	},
}

// shieldBlocksAttacks resolves the blocks_attacks component the way ItemStack.get(BLOCKS_ATTACKS) does:
// a patch entry wins, else the item default. Cite ItemStack.get(DataComponentType).
func shieldBlocksAttacks(s component.SlotData) (*component.BlocksAttacks, bool) {
	if slotIsEmpty(s) {
		return nil, false
	}
	if c, ok := component.DecodePatch(s).Get(compBlocksAttacksWire).(*component.BlocksAttacks); ok && c != nil {
		return c, true
	}
	if int32(s.ItemID) == shieldItemID {
		return shieldDefaultBlocksAttacks, true
	}
	return nil, false
}

// blocksAttacksBlockDelayTicks ports BlocksAttacks.blockDelayTicks(): Math.round(blockDelaySeconds*20).
// For the shield (0.25s) this is round(5.0) == 5.
func blocksAttacksBlockDelayTicks(c *component.BlocksAttacks) int32 {
	return int32(math.Round(float64(float32(c.BlockDelaySeconds) * 20.0)))
}

// getItemBlockingWith ports LivingEntity.getItemBlockingWith(): the used item iff isUsingItem() && it
// carries blocks_attacks && held past the delay ((getUseDuration - useItemRemaining) >= blockDelayTicks).
func getItemBlockingWith(p *tickPlayer) (component.SlotData, *component.BlocksAttacks, bool) {
	if !isUsingItem(p) {
		return component.SlotData{}, nil, false
	}
	c, ok := shieldBlocksAttacks(p.useItem)
	if !ok {
		return component.SlotData{}, nil, false
	}
	held := blocksAttacksItemUseDuration - p.useItemRemaining
	if held < blocksAttacksBlockDelayTicks(c) {
		return component.SlotData{}, nil, false
	}
	return p.useItem, c, true
}

// isBlocking ports LivingEntity.isBlocking(): getItemBlockingWith() != null.
func isBlocking(p *tickPlayer) bool {
	_, _, ok := getItemBlockingWith(p)
	return ok
}

// damageReductionResolve ports BlocksAttacks.DamageReduction.resolve(source, amount, angle):
// if (angle > 0.017453292f * horizontalBlockingAngle) return 0; if (type present and does not contain
// source.typeHolder()) return 0; return Mth.clamp(base + factor*amount, 0, amount). The type-filter
// HolderSet decode is a cited deferral (the shield default has no type). Cite DamageReduction.resolve.
func damageReductionResolve(dr component.DamageReduction, src damageSource, amount float32, angle float64) float32 {
	const degToRad = 0.017453292
	if angle > float64(degToRad*float32(dr.HorizontalBlockingAngle)) {
		return 0.0
	}
	if bool(dr.Type.Has) {
		return 0.0
	}
	v := float32(dr.Base) + float32(dr.Factor)*amount
	return clampFloat32(v, 0.0, amount)
}

// resolveBlockedDamage ports BlocksAttacks.resolveBlockedDamage: sum each reduction resolve, then
// Mth.clamp(sum, 0, amount). Cite BlocksAttacks.resolveBlockedDamage.
func resolveBlockedDamage(c *component.BlocksAttacks, src damageSource, amount float32, angle float64) float32 {
	var f float32
	for _, r := range c.DamageReductions {
		f += damageReductionResolve(r, src, amount, angle)
	}
	return clampFloat32(f, 0.0, amount)
}

// itemDamageApply ports BlocksAttacks.ItemDamageFunction.apply(amount): if (amount < threshold) return 0;
// return Mth.floor(base + factor*amount). Cite ItemDamageFunction.apply.
func itemDamageApply(f component.ItemDamageFunction, amount float32) int {
	if amount < float32(f.Threshold) {
		return 0
	}
	return int(math.Floor(float64(float32(f.Base) + float32(f.Factor)*amount)))
}

// clampFloat32 ports net.minecraft.util.Mth.clamp(float, float, float).
func clampFloat32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// applyItemBlocking is the 1:1 port of LivingEntity.applyItemBlocking(ServerLevel, DamageSource, float):
// the damage a shield absorbs from this hit. Called from applyDamage BEFORE the i-frame gate (hurtServer
// offset 79). Cite LivingEntity.applyItemBlocking.
func (t *TickLoop) applyItemBlocking(p *tickPlayer, src damageSource, amount float32) float32 {
	if amount <= 0.0 {
		return 0.0
	}
	blockStack, c, ok := getItemBlockingWith(p)
	if !ok {
		return 0.0
	}
	// bypassedBy: the shield default is #minecraft:bypasses_shield. A source in that tag ignores the
	// shield entirely. Cite applyItemBlocking bypassedBy check.
	if src.is("bypasses_shield") {
		return 0.0
	}
	// Arrow pierce guard (AbstractArrow.getPierceLevel > 0) is a cited deferral: v1 has no per-arrow
	// pierce level in damageSource (default pierce 0 blocks normally). Cite applyItemBlocking.

	// Angle check (offsets 102-178): look = calculateViewVector(0, yHeadRot); toSource = flatten(source
	// minus pos).normalize(); angle = acos(toSource.dot(look)). sourcePosition null -> angle = Mth.PI.
	angle := math.Pi
	sx, sz, hasPos := t.sourceHorizontalPosition(p, src)
	if hasPos {
		// calculateViewVector(0, yHeadRot): pitch 0 -> look = (-sin(yawRad), 0, cos(yawRad)),
		// yawRad = yHeadRot * 0.017453292. Cite Entity.calculateViewVector.
		const degToRad = 0.017453292
		yawRad := float64(p.headYaw * degToRad)
		lookX := -math.Sin(yawRad)
		lookZ := math.Cos(yawRad)
		dx := sx - p.x
		dz := sz - p.z
		mag := math.Sqrt(dx*dx + dz*dz)
		if mag >= 1.0e-4 {
			dx /= mag
			dz /= mag
		} else {
			dx, dz = 0, 0
		}
		dot := dx*lookX + dz*lookZ
		if dot > 1.0 {
			dot = 1.0
		} else if dot < -1.0 {
			dot = -1.0
		}
		angle = math.Acos(dot)
	}

	blocked := resolveBlockedDamage(c, src, amount, angle)

	// hurtBlockingItem: award-stat (deferred) + damage the shield by itemDamage.apply(blocked).
	t.hurtBlockingItem(p, blockStack, c, blocked)

	// if (blocked > 0 and not IS_PROJECTILE and directEntity instanceof LivingEntity) blockUsingItem(...).
	if blocked > 0.0 && !src.is("is_projectile") {
		if src.attacker != 0 && t.sourceDirectIsLiving(src) {
			t.blockUsingItem(p, src, amount)
		}
	}

	return blocked
}

// hurtBlockingItem ports BlocksAttacks.hurtBlockingItem: dmg = itemDamage.apply(blocked); if (dmg > 0)
// useItem.hurtAndBreak(dmg, this, hand.asEquipmentSlot()). The Stats.ITEM_USED award is deferred. Cite
// BlocksAttacks.hurtBlockingItem.
func (t *TickLoop) hurtBlockingItem(p *tickPlayer, blockStack component.SlotData, c *component.BlocksAttacks, blocked float32) {
	dmg := itemDamageApply(c.ItemDamage, blocked)
	if dmg <= 0 {
		return
	}
	inv := ensureInventory(p)
	slot := heldMenuSlot(p, p.useItemHand)
	cur := inv.get(slot)
	if stackEmpty(cur) {
		return
	}
	next, broke := t.stackHurtAndBreak(cur, dmg, p.gameMode == gameModeCreative)
	if broke || next.ItemID != cur.ItemID || next.Count != cur.Count || stackDamageValue(next) != stackDamageValue(cur) {
		before := inv.snapshot()
		inv.set(slot, next)
		t.broadcastInventoryChanges(p, inv, before)
	}
	if broke {
		t.stopUsingItemShield(p)
	}
}

// blockUsingItem ports LivingEntity.blockUsingItem -> blockedByItem: the blocked victim knockback is
// applied to the attacker. Pushing an arbitrary attacker entity is a cited v1 deferral; the
// damage-negation, item-damage and axe-disable land. Cite LivingEntity.blockUsingItem/blockedByItem.
func (t *TickLoop) blockUsingItem(p *tickPlayer, src damageSource, amount float32) {
	// Axe disable: a vanilla axe attack calls BlocksAttacks.disable (ItemCooldowns entry + stopUsingItem).
	// v1 has no ItemCooldowns / per-weapon disable read; cited stub via stopUsingItem when the attacker
	// weapon disables blocking. Cite BlocksAttacks.disable.
	if t.attackerWeaponDisablesBlocking(src) {
		t.stopUsingItemShield(p)
	}
}

// stopUsingItemShield ports LivingEntity.stopUsingItem() for the shield path: clears the use state so
// getItemBlockingWith returns null. Cite LivingEntity.stopUsingItem.
func (t *TickLoop) stopUsingItemShield(p *tickPlayer) {
	p.useItem = component.SlotData{}
	p.useItemRemaining = 0
	p.useItemHand = 0
}

// attackerWeaponDisablesBlocking is the cited stub for the axe-disables-shield trigger. v1 has no
// weapon-component disable read yet, so it returns false until the axe weapon component is plumbed
// through damageSource. Cite the axe disable-blocking path (Weapon disableBlockingForSeconds).
func (t *TickLoop) attackerWeaponDisablesBlocking(src damageSource) bool {
	return false
}

// sourceHorizontalPosition resolves DamageSource.getSourcePosition (x, z), mirroring
// dealDefaultKnockbackPlayer: a player attacker first, then the region entity store (a mob). Returns
// false when the source has no position -> angle == PI. Cite DamageSource.getSourcePosition.
func (t *TickLoop) sourceHorizontalPosition(p *tickPlayer, src damageSource) (float64, float64, bool) {
	if src.attacker == 0 {
		return 0, 0, false
	}
	if attacker := t.playerByEntityID(src.attacker); attacker != nil {
		return attacker.x, attacker.z, true
	}
	if mob, ok := t.cur().entities.get(src.attacker); ok && mob != nil {
		return mob.x, mob.z, true
	}
	return 0, 0, false
}

// sourceDirectIsLiving reports whether the source direct entity is a LivingEntity, gating blockUsingItem.
// Cite applyItemBlocking (getDirectEntity() instanceof LivingEntity).
func (t *TickLoop) sourceDirectIsLiving(src damageSource) bool {
	if src.attacker == 0 {
		return false
	}
	if t.playerByEntityID(src.attacker) != nil {
		return true
	}
	if mob, ok := t.cur().entities.get(src.attacker); ok && mob != nil {
		return true
	}
	return false
}
