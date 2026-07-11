package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
)

const (
	bowUseDuration         int32 = 72000
	bowMinPower                  = 0.1
	bowFullPower                 = 1.0
	bowVelocityScale             = 3.0
	bowArrowBaseDamage           = 2.0
	bowDurabilityUse             = 1
	crossbowShootPower           = 3.15
	crossbowChargeDuration int32 = 25
)

const enchantInfinity = "minecraft:infinity"

func itemIsBow(itemID int32) bool      { return item.ID(itemID) == item.Bow.ID }
func itemIsCrossbow(itemID int32) bool { return item.ID(itemID) == item.Crossbow.ID }

func getBowPowerForTime(charge int32) float32 {
	f := float32(charge) / 20.0
	f = (f*f + f*2.0) / 3.0
	if f > 1.0 {
		f = 1.0
	}
	return f
}

// tryStartBowUse is the BowItem.use / CrossbowItem.use port for the right-click-air path: begin the draw.
// A charged crossbow fires immediately; a bow or uncharged crossbow gates on having a projectile
// (creative bypasses) and calls startUsingItem (useItem=held, useItemRemaining=72000). Returns true when
// the item was a bow/crossbow (so useItemInHand stops), false to fall through to the food path. Runs before
// the food gate; bow/crossbow-gated (a cheap id compare, no RNG -- the pig oracle is unperturbed).
// Cite BowItem.use / CrossbowItem.use.
func (t *TickLoop) tryStartBowUse(p *tickPlayer, inv *Inventory, held component.SlotData, hand int32) bool {
	isBow := itemIsBow(int32(held.ItemID))
	isCrossbow := itemIsCrossbow(int32(held.ItemID))
	if !isBow && !isCrossbow {
		return false
	}
	creative := p.gameMode == gameModeCreative
	if isCrossbow && p.crossbowCharged {
		// CrossbowItem.use: a CHARGED crossbow fires on use (performShooting at power 3.15).
		t.fireCrossbow(p, inv, held, hand)
		return true
	}
	// BowItem.use / uncharged CrossbowItem.use: gate on ammo (creative bypasses), else FAIL (no-op).
	_, hasAmmo := t.findArrowSlot(p, inv)
	if !creative && !hasAmmo {
		return true
	}
	// startUsingItem(hand): begin the draw.
	p.useItem = held
	p.useItemRemaining = bowUseDuration
	p.useItemHand = hand
	t.broadcastUsingItem(p, true, hand)
	return true
}

// releaseBowOrCrossbow dispatches LivingEntity.releaseUsingItem -> Item.releaseUsing for a bow/crossbow
// being used (from the RELEASE_USE_ITEM player action). Returns true when the used item was a bow/crossbow
// so the caller does not also clear via stopUsingItem. Cite BowItem.releaseUsing / CrossbowItem.
func (t *TickLoop) releaseBowOrCrossbow(p *tickPlayer) bool {
	if !isUsingItem(p) {
		return false
	}
	used := p.useItem
	hand := p.useItemHand
	if itemIsBow(int32(used.ItemID)) {
		t.bowReleaseUsing(p, used, hand)
		return true
	}
	if itemIsCrossbow(int32(used.ItemID)) {
		t.crossbowReleaseUsing(p, used, hand)
		return true
	}
	return false
}

// bowReleaseUsing is the 1:1 port of BowItem.releaseUsing: charge = 72000 - timeLeft; power =
// getPowerForTime(charge); abort if power < 0.1; consume 1 arrow (unless creative/infinity); shoot an Arrow
// at velocity power*3.0 in the look direction (crit at full draw, power==1.0); hurtAndBreak the bow by 1.
// Cite BowItem.releaseUsing + draw + createProjectile + shootProjectile + Projectile.shootFromRotation.
func (t *TickLoop) bowReleaseUsing(p *tickPlayer, stack component.SlotData, hand int32) {
	arrowSlot, hasAmmo := t.findArrowSlot(p, ensureInventory(p))
	creative := p.gameMode == gameModeCreative
	if !hasAmmo && !creative {
		t.stopUsingItem(p)
		return
	}
	charge := bowUseDuration - p.useItemRemaining
	power := getBowPowerForTime(charge)
	if float64(power) < bowMinPower {
		t.stopUsingItem(p)
		return
	}
	infinity := stackEnchantments(stack)[enchantInfinity] > 0
	if hasAmmo && !creative && !infinity {
		t.consumeArrow(p, arrowSlot)
	}
	crit := float64(power) == bowFullPower
	t.shootPlayerArrow(p, float64(power)*bowVelocityScale, crit, stack)
	t.hurtHandItem(p, hand, bowDurabilityUse)
	t.stopUsingItem(p)
}

// crossbowReleaseUsing ports the crossbow charge-on-release: a full draw (charge>=25, getPowerForTime>=1
// over getChargeDuration) LOADS the crossbow (tryLoadProjectiles -> CHARGED, consuming 1 arrow); a short
// draw loads nothing. The next use fires the loaded bolt. Cite CrossbowItem.onUseTick + releaseUsing.
func (t *TickLoop) crossbowReleaseUsing(p *tickPlayer, stack component.SlotData, hand int32) {
	charge := bowUseDuration - p.useItemRemaining
	// CrossbowItem.getChargeDuration(stack, entity) = floor(modifyCrossbowChargingTime(stack, 1.25)*20):
	// Quick Charge shortens the required draw (I=20, II=15, III=10 ticks; unenchanted = 25). Cite
	// CrossbowItem.releaseUsing -> getChargeDuration + EnchantmentHelper.modifyCrossbowChargingTime.
	if charge < t.enchCrossbowChargeDuration(stack) {
		t.stopUsingItem(p)
		return
	}
	arrowSlot, hasAmmo := t.findArrowSlot(p, ensureInventory(p))
	creative := p.gameMode == gameModeCreative
	if !hasAmmo && !creative {
		t.stopUsingItem(p)
		return
	}
	infinity := stackEnchantments(stack)[enchantInfinity] > 0
	if hasAmmo && !creative && !infinity {
		t.consumeArrow(p, arrowSlot)
	}
	p.crossbowCharged = true
	t.stopUsingItem(p)
}

// fireCrossbow ports CrossbowItem.use charged branch -> performShooting: fire the loaded bolt at power 3.15
// (getShootingPower, non-firework) in the look direction, clear CHARGED, damage the crossbow by 1. Not crit.
// Multishot is a cited enchant hook. Cite CrossbowItem.use + performShooting + shootProjectile.
func (t *TickLoop) fireCrossbow(p *tickPlayer, _ *Inventory, stack component.SlotData, hand int32) {
	t.shootPlayerArrow(p, crossbowShootPower, false, stack)
	p.crossbowCharged = false
	t.hurtHandItem(p, hand, bowDurabilityUse)
}

// shootPlayerArrow builds the arrow launch vector from the player look (Projectile.shootFromRotation: the
// unit view vector scaled by velocity, zero spread in v1 -- the throwable.go simplification) and spawns the
// Arrow from the player eye via the shared spawnArrow infra. crit sets the crit flag (setCritArrow). Cite
// Projectile.shootFromRotation + spawnArrow + AbstractArrow.setCritArrow.
func (t *TickLoop) shootPlayerArrow(p *tickPlayer, velocity float64, crit bool, weapon component.SlotData) *Entity {
	// Spawn at rest, then Projectile.shootFromRotation(player, xRot, yRot, 0, velocity, 1.0): the look
	// vector with the 3 per-axis inaccuracy triangle draws (BowItem/CrossbowItem shoot at inaccuracy 1.0,
	// verified BowItem.releaseUsing offset 117 fconst_1) on the arrow's OWN arrowRNG, then the owner
	// known-movement inherit. A pig fires no arrow, so its stream is never perturbed. Cite
	// Projectile.shootFromRotation + BowItem.releaseUsing (shoot ... f7=1.0f).
	a := t.spawnArrow(p.entityID, p.x, p.y+playerStandingEyeHeight, p.z, 0, 0, 0, bowArrowBaseDamage)
	if a.arrowRNG == nil {
		a.arrowRNG = newEntityRandom(uint64(a.id))
	}
	mx, my, mz := t.playerKnownMovement(p)
	vx, vy, vz := shootVectorFromRotation(a.arrowRNG, p.yaw, p.pitch, 0, velocity, 1.0, mx, my, mz, p.onGround)
	a.vx, a.vy, a.vz = vx, vy, vz
	horiz := math.Sqrt(vx*vx + vz*vz)
	a.yaw = float32(mthAtan2(vx, vz) * float64(mthRadToDeg))
	a.pitch = float32(mthAtan2(vy, horiz) * float64(mthRadToDeg))
	a.headYaw = a.yaw
	if crit {
		a.arrowCrit = true
	}
	// ProjectileWeaponItem.createProjectile stores the firing weapon on the arrow (setSoundEvent path
	// + firedFromWeapon = weapon.copy()) so AbstractArrow.getWeaponItem() reads the bow's Power/Punch/
	// Fire Aspect at hit time. A dispenser/mob arrow with no weapon leaves this empty.
	a.arrowWeapon = weapon
	// ProjectileWeaponItem.shoot -> EnchantmentHelper.onProjectileSpawned(weapon, projectile): Flame's
	// PROJECTILE_SPAWNED Ignite lights the arrow (igniteForSeconds 100), so a burning arrow ignites what
	// it hits (AbstractArrow.onHitEntity isOnFire -> igniteForSeconds(5)). A plain bow does nothing.
	t.enchOnProjectileSpawned(weapon, a)
	return a
}

// findArrowSlot ports the ammo search Player.getProjectile drives: OFF_HAND (slot 45) then MAIN_HAND then
// the inventory scan (slots 9..45) for the first ARROW stack. Returns the menu-slot index and ok. v1 accepts
// the plain Arrow (tipped/spectral are a cited follow-up). Cite Player.getProjectile + getHeldProjectile.
func (t *TickLoop) findArrowSlot(_ *tickPlayer, inv *Inventory) (int16, bool) {
	if isArrowStack(inv.get(offHandMenuSlot)) {
		return offHandMenuSlot, true
	}
	main := hotbarMenuSlotBase + inv.heldSlot
	if isArrowStack(inv.get(main)) {
		return main, true
	}
	for i := int16(9); i < playerInventorySize; i++ {
		if isArrowStack(inv.get(i)) {
			return i, true
		}
	}
	return 0, false
}

// isArrowStack is the ARROW_ONLY predicate: a non-empty stack of minecraft:arrow. Cite ProjectileWeaponItem.ARROW_ONLY.
func isArrowStack(s component.SlotData) bool {
	return !slotIsEmpty(s) && item.ID(s.ItemID) == item.Arrow.ID
}

// consumeArrow ports ProjectileWeaponItem.useAmmo shrink(1) for the found arrow slot: decrement by 1 (empty
// when the last arrow leaves) and push the changed slot. Creative/Infinity are gated by the caller. Cite
// ProjectileWeaponItem.useAmmo.
func (t *TickLoop) consumeArrow(p *tickPlayer, slot int16) {
	inv := ensureInventory(p)
	before := inv.snapshot()
	s := inv.get(slot)
	s.Count--
	if s.Count <= 0 {
		s = component.SlotData{Count: 0}
	}
	inv.set(slot, s)
	t.broadcastInventoryChanges(p, inv, before)
}

// hurtHandItem is the hand-aware ItemStack.hurtAndBreak for the bow/crossbow shot (hurtAndBreak(1, player,
// hand.asEquipmentSlot())): apply amount durability to the item in hand (mainhand or offhand), write back
// (a broken bow shrinks to empty), broadcast the slot. Creative gear never wears. Mirrors hurtHeldItem but
// keys off the used hand slot. Cite ItemStack.hurtAndBreak(int, LivingEntity, EquipmentSlot).
func (t *TickLoop) hurtHandItem(p *tickPlayer, hand int32, amount int) bool {
	inv := ensureInventory(p)
	slot := heldMenuSlot(p, hand)
	cur := inv.get(slot)
	if stackEmpty(cur) {
		return false
	}
	creative := p.gameMode == gameModeCreative
	if t.stackProcessDurabilityChange(cur, amount, creative) == 0 {
		return false
	}
	next, broke := t.stackHurtAndBreak(cur, amount, creative)
	before := inv.snapshot()
	inv.set(slot, next)
	t.broadcastInventoryChanges(p, inv, before)
	return broke
}

// bowLookVectorMagnitude is a test helper: the launch-vector magnitude for a given velocity (the look
// vector is unit-length, so this is velocity). Not used on the hot path.
func bowLookVectorMagnitude(velocity float64) float64 { return math.Abs(velocity) }
