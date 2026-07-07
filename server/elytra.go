package server

import (
	"math"

	"github.com/imhinotori/sulfur/level/component"
)

const fallFlyingSharedFlagBit int8 = -128 // 1<<7 == 0x80

const compGlider = 34

// playerSharedFlags computes the player DATA_SHARED_FLAGS byte (index 0): the invisible bit
// (LivingEntity.updateInvisibilityStatus -> setInvisible(hasEffect(INVISIBILITY))) and the fall-flying
// bit (setSharedFlag(7, ...)). v1 players carry no other server-side shared flags, so the byte carries
// ONLY these two bits. Consolidating here lets the invisible + fall-flying broadcasters coexist without
// one overwriting the other bit. Cite Entity.setSharedFlag (flags = set/clear bit (1<<i)).
func playerSharedFlags(p *tickPlayer) int8 {
	var flags int8
	if playerHasEffect(p, effectInvisibility) {
		flags |= invisibleSharedFlagBit
	}
	if p.fallFlying {
		flags |= fallFlyingSharedFlagBit
	}
	return flags
}

// broadcastPlayerSharedFlags pushes the DATA_SHARED_FLAGS byte (index 0, BYTE serializer) to every
// player tracking p, carrying the current invisible + fall-flying bits. Mirrors broadcastEntityFireFlag.
func (t *TickLoop) broadcastPlayerSharedFlags(p *tickPlayer) {
	t.broadcastToTrackers(p.entityID, encodeSetEntityDataByID(p.entityID, sharedFlagsDataEntry(playerSharedFlags(p))))
}

// stackCanGlideUsing ports LivingEntity.canGlideUsing(ItemStack, EquipmentSlot) 1:1: the stack glides
// iff it carries the GLIDER component AND its EQUIPPABLE slot matches the worn slot AND it is not about
// to break (nextDamageWillBreak() is false). Cite LivingEntity.canGlideUsing.
//
//	[VERIFIED javap LivingEntity.canGlideUsing: stack.has(GLIDER) && (eq = stack.get(EQUIPPABLE)) != null
//	 && eq.slot() == slot && !stack.nextDamageWillBreak().]
func stackCanGlideUsing(s component.SlotData, slot int) bool {
	if stackEmpty(s) {
		return false
	}
	pt := component.DecodePatch(s)
	if !pt.Has(compGlider) {
		return false
	}
	eq, ok := pt.Get(compEquippable).(*component.Equippable)
	if !ok {
		return false
	}
	if int(eq.Slot) != slot {
		return false
	}
	return !stackNextDamageWillBreak(s)
}

// stackNextDamageWillBreak ports ItemStack.nextDamageWillBreak(): a damageable item is one durability
// point from breaking (getDamageValue() >= getMaxDamage() - 1). A non-damageable item never breaks.
//
//	[VERIFIED javap ItemStack.nextDamageWillBreak: isDamageableItem() && getDamageValue() >=
//	 getMaxDamage() - 1.]
func stackNextDamageWillBreak(s component.SlotData) bool {
	if !stackIsDamageableItem(s) {
		return false
	}
	return stackDamageValue(s) >= stackMaxDamage(s)-1
}

// playerCanGlide ports LivingEntity.canGlide() for a player: not on the ground, not a passenger, not
// levitating -- then a glide-capable item in ANY equipment slot (canGlideUsing). onGround is the player
// movement flag; isPassenger() is the ride state (vehicleID != 0); LEVITATION is a real effect read.
//
//	[VERIFIED javap LivingEntity.canGlide: onGround() || isPassenger() || hasEffect(LEVITATION) ? false;
//	 else for (slot in EquipmentSlot.VALUES) if canGlideUsing(getItemBySlot(slot), slot) return true; false.]
func (t *TickLoop) playerCanGlide(p *tickPlayer) bool {
	if p.onGround || p.vehicleID != 0 || playerHasEffect(p, effectLevitation) {
		return false
	}
	for _, slot := range playerEquipmentSlots {
		if stackCanGlideUsing(playerItemBySlot(p, slot), slot) {
			return true
		}
	}
	return false
}

// tryToStartFallFlying ports Player.tryToStartFallFlying() 1:1: !isFallFlying() && canGlide() &&
// !isInWater() -> setSharedFlag(7, true), broadcast, return true. Driven by the ServerboundPlayerCommand
// START_FALL_FLYING action (double-jump-in-air with an elytra). Cite Player.tryToStartFallFlying.
//
//	[VERIFIED javap Player.tryToStartFallFlying: !isFallFlying() && canGlide() && !isInWater() ->
//	 startFallFlying() [setSharedFlag(7, true)]; return true; else false.]
func (t *TickLoop) tryToStartFallFlying(p *tickPlayer) bool {
	if p.fallFlying || !t.playerCanGlide(p) || t.playerInWater(p) {
		return false
	}
	p.fallFlying = true
	t.broadcastPlayerSharedFlags(p)
	return true
}

// stopFallFlying ports LivingEntity.stopFallFlying(): clear the FALL_FLYING flag and broadcast. (Vanilla
// sets 7 true then false to force a resync; the observable end state is CLEARED.)
func (t *TickLoop) stopFallFlying(p *tickPlayer) {
	if !p.fallFlying {
		return
	}
	p.fallFlying = false
	t.broadcastPlayerSharedFlags(p)
}

// updateFallFlying ports LivingEntity.updateFallFlying() for a player (server-side branch; isClientSide
// always false). Runs once per tick in aiStep order (BEFORE the fallFlyTicks increment):
//   - if !canGlide(): clear the flag and return (landing / lost elytra / levitation).
//   - else every 20 ticks (i = fallFlyTicks+1; i%10==0 && (i/10)%2==0): hurt a random glide-capable slot
//     1 durability -- 1 per second. The ELYTRA_GLIDE game event is cite-deferred (no gameevent in v1).
// checkFallDistanceAccumulation() is folded into the existing airborne fall accumulation (fall_damage.go).
//
//	[VERIFIED javap LivingEntity.updateFallFlying: !isClientSide { if !canGlide() setSharedFlag(7,false),
//	 return; i = fallFlyTicks+1; if i%10==0 { if (i/10)%2==0 { random canGlideUsing slot;
//	 getItemBySlot(slot).hurtAndBreak(1, this, slot); } gameEvent(ELYTRA_GLIDE); } }.]
func (t *TickLoop) updateFallFlying(p *tickPlayer) {
	if !p.fallFlying {
		return
	}
	if !t.playerCanGlide(p) {
		t.stopFallFlying(p)
		return
	}
	i := p.fallFlyTicks + 1
	if i%10 != 0 {
		return
	}
	if (i/10)%2 == 0 {
		t.hurtGlideEquipment(p)
	}
}

// hurtGlideEquipment is updateFallFlying durability tail: filter glide-capable slots (canGlideUsing),
// pick one via Util.getRandom(list, random), hurt it 1. For a lone elytra in CHEST the list has one
// element so getRandom -> nextInt(1) == 0 picks CHEST deterministically (the RNG draw is faithful).
func (t *TickLoop) hurtGlideEquipment(p *tickPlayer) {
	var slots []int
	for _, slot := range playerEquipmentSlots {
		if stackCanGlideUsing(playerItemBySlot(p, slot), slot) {
			slots = append(slots, slot)
		}
	}
	if len(slots) == 0 {
		return
	}
	slot := slots[p.fallFlyRandom().nextInt(len(slots))]
	t.hurtEquipmentSlot(p, slot, 1)
}

// hurtEquipmentSlot applies amount durability to the item in an equipment slot (per-slot hurtHeldItem),
// writes it back into the 46-slot window, and broadcasts the change. Broken -> empty. Creative -> no wear.
func (t *TickLoop) hurtEquipmentSlot(p *tickPlayer, slot, amount int) {
	inv := ensureInventory(p)
	win := equipmentWindowSlot(inv, slot)
	if win < 0 {
		return
	}
	cur := inv.get(win)
	if stackEmpty(cur) {
		return
	}
	creative := p.gameMode == gameModeCreative
	if t.stackProcessDurabilityChange(cur, amount, creative) == 0 {
		return
	}
	next, _ := t.stackHurtAndBreak(cur, amount, creative)
	before := inv.snapshot()
	inv.set(win, next)
	t.broadcastInventoryChanges(p, inv, before)
}

// equipmentWindowSlot maps an EquipmentSlot ordinal to its 46-slot window index (inverse of
// playerItemBySlot): MAINHAND = selected hotbar, OFFHAND = 45, HEAD/CHEST/LEGS/FEET = 5/6/7/8. -1 = none.
func equipmentWindowSlot(inv *Inventory, slot int) int16 {
	switch slot {
	case eqSlotMainHand:
		return heldWindowSlot(inv.heldSlot)
	case eqSlotOffHand:
		return offhandWindowSlot
	case eqSlotHead:
		return 5
	case eqSlotChest:
		return 6
	case eqSlotLegs:
		return 7
	case eqSlotFeet:
		return 8
	}
	return -1
}

// tickPlayerFallFlying is the per-player fall-flying upkeep, once per tick in the player-tick pipeline:
// the aiStep updateFallFlying (canGlide gate + durability) THEN the tick() fallFlyTicks increment/reset,
// in that vanilla order. Cite LivingEntity.aiStep + LivingEntity.tick.
func (t *TickLoop) tickPlayerFallFlying(p *tickPlayer) {
	if p == nil || p.dead {
		return
	}
	t.updateFallFlying(p)
	if p.fallFlying {
		p.fallFlyTicks++
	} else {
		p.fallFlyTicks = 0
	}
}

// updateFallFlyingMovement ports LivingEntity.updateFallFlyingMovement(Vec3) 1:1 -- the elytra glide
// vector math (the crux). Given deltaMovement (in), the glider look (yaw/pitch degrees) and the
// effective gravity, returns the new deltaMovement after: the gravity/lift on Y (scaled by cos(pitch)^2),
// the downward-glide accel toward the look when descending, the pitch-up lift when looking up, the steer
// toward the look, and the (0.99, 0.98, 0.99) drag. PURE function so the math is verifiable in isolation.
// Runs client-side for the local player (client sends the glided position); ported for fidelity and for
// any server-authoritative glider, gated behind the FALL_FLYING flag by its callers.
//
//	[VERIFIED javap LivingEntity.updateFallFlyingMovement:
//	 look = getLookAngle(); rad = getXRot() * 0.017453292f;
//	 d4 = sqrt(look.x^2 + look.z^2); d6 = in.horizontalDistance(); d8 = getEffectiveGravity();
//	 d10 = Mth.square(cos((double)rad));
//	 in = in.add(0, d8 * (-1.0 + d10*0.75), 0);
//	 if (in.y < 0 && d4 > 0) { d12 = in.y * -0.1 * d10; in = in.add(look.x*d12/d4, d12, look.z*d12/d4); }
//	 if (rad < 0 && d4 > 0) { d12 = d6 * (double)(-Mth.sin((double)rad)) * 0.04;
//	                          in = in.add(-look.x*d12/d4, d12*3.2, -look.z*d12/d4); }
//	 if (d4 > 0) { in = in.add((look.x/d4*d6 - in.x)*0.1, 0, (look.z/d4*d6 - in.z)*0.1); }
//	 return in.multiply(0.99, 0.98, 0.99).]
func updateFallFlyingMovement(inX, inY, inZ float64, yaw, pitch float32, effectiveGravity float64) (x, y, z float64) {
	lookX, lookY, lookZ := playerViewVector(yaw, pitch)
	_ = lookY // look.y unused (only look.x/look.z + the horizontal length)

	rad := pitch * 0.017453292

	d4 := math.Sqrt(lookX*lookX + lookZ*lookZ)
	d6 := math.Sqrt(inX*inX + inZ*inZ)
	d8 := effectiveGravity
	cosF := float32(math.Cos(float64(rad)))
	d10 := float64(cosF) * float64(cosF)

	inY += d8 * (-1.0 + d10*0.75)

	if inY < 0.0 && d4 > 0.0 {
		d12 := inY * -0.1 * d10
		inX += lookX * d12 / d4
		inY += d12
		inZ += lookZ * d12 / d4
	}

	if rad < 0.0 && d4 > 0.0 {
		sinF := float32(math.Sin(float64(rad)))
		d12 := d6 * float64(-sinF) * 0.04
		inX += -lookX * d12 / d4
		inY += d12 * 3.2
		inZ += -lookZ * d12 / d4
	}

	if d4 > 0.0 {
		inX += (lookX/d4*d6 - inX) * 0.1
		inZ += (lookZ/d4*d6 - inZ) * 0.1
	}

	return inX * 0.99, inY * 0.98, inZ * 0.99
}

// fallFlyRandom returns the player lazily-seeded fall-flying RandomSource (Util.getRandom uses it to
// pick a glide-capable slot to wear). Seeded from entityID so it is deterministic per player and never
// touches the enchant stream. For a lone elytra in CHEST the list is size 1, so the draw is nextInt(1).
func (p *tickPlayer) fallFlyRandom() *entityRandom {
	if p.fallFlyRand == nil {
		p.fallFlyRand = newEntityRandom(uint64(uint32(p.entityID)))
	}
	return p.fallFlyRand
}
