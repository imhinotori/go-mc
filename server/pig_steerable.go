package server

// pig_steerable.go -- the Pig ItemSteerable RIDE + STEER, a 1:1 port from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p this task). The Pig (net.minecraft.world.entity.animal.pig.Pig)
// implements net.minecraft.world.entity.ItemSteerable: a SADDLE saddles it, a saddled pig is RIDDEN and
// STEERED by a rider holding a carrot_on_a_stick, and its ridden speed is boosted by a carrot-on-a-stick
// USE (ItemBasedSteering.boost). This MIRRORS the fully-wired Strider (strider.go): the pig branch in
// getControllingPassenger (passenger.go), tryPigInteract (the mobInteract saddle/mount, dispatched in
// attack_dispatch.go), and the boost via ItemBasedSteering -- all the SAME ItemBasedSteering machinery the
// strider uses. Because a live Entity is exactly ONE type, the pig reuses the per-entity ItemBasedSteering
// state fields already carried for the strider (striderSaddled / striderBoosting / striderBoostTime /
// striderBoostTimeTotal == the Pig.DATA_BOOST_TIME + SADDLE-slot state); a strider and a pig never share an
// entity, so this is a non-conflicting reuse (NOT a new field), keeping the change additive.
//
// VANILLA (VERIFIED javap net.minecraft.world.entity.animal.pig.Pig + ItemSteerable + ItemBasedSteering
// this task):
//   Pig implements ItemSteerable; private final ItemBasedSteering steering; DATA_BOOST_TIME.
//   Pig.getControllingPassenger(): if (isSaddled()) { first = getFirstPassenger(); if (first instanceof
//     Player p && p.isHolding(CARROT_ON_A_STICK)) return p; } return super.getControllingPassenger().
//   Pig.mobInteract(player, hand): isFood = isFood(getItemInHand(hand)); if (!isFood && isSaddled() &&
//     !isVehicle() && !isSecondaryUseActive()) { if(!clientSide) player.startRiding(this); return SUCCESS; }
//     r = super.mobInteract(...); if (!r.consumesAction()) { stack = getItemInHand(hand); return
//     isEquippableInSlot(stack, SADDLE) ? stack.interactLivingEntity(...) : PASS; } return r.
//   Pig.getRiddenSpeed(player) = getAttributeValue(MOVEMENT_SPEED) * 0.225d * steering.boostFactor().
//   Pig.tickRidden(player, in): super.tickRidden; setRot(player.yRot, player.xRot*0.5f); yHeadRot=yBodyRot=
//     yRotO=getYRot(); steering.tickBoost().
//   Pig.boost() = steering.boost(getRandom()).
//   Pig.isFood(stack) = stack.is(ItemTags.PIG_FOOD).
//   ItemBasedSteering.boost(rng): if boosting return false; boosting=true; boostTime=0; boostTimeTotal =
//     rng.nextInt(841)+140; return true. boostFactor(): boosting ? 1.0 + 1.15f*sin(t/total*PI) : 1.0.
//     tickBoost(): if boosting { boostTime++; if boostTime > boostTimeTotal boosting=false }.
//
// PIG-ORACLE BYTE-IDENTITY (the load-bearing gate): every path here is guarded on the pig being SADDLED +
// RIDDEN. An un-saddled, un-ridden pig (the TestPluginPigEqualsGoNativePig oracle) NEVER enters a new code
// path and draws ZERO extra RNG -- striderIsSaddled(pig) is false (striderSaddled defaults false), so
// pigGetControllingPassenger returns 0 (super) and no pig-steer branch runs. The boost() RNG draw
// (nextInt(841)) fires ONLY on a real carrot-on-a-stick USE by a rider, never on the oracle.
//
// The Pig.getRiddenInput (Vec3(0,0,1)) + the ridden-travel physics apply are the client-authoritative steer
// path (like the strider/camel: getControllingPassenger makes it client-authoritative, so the client drives
// it via ServerboundMoveVehicle -- handleMoveVehicle). getRiddenSpeed is the SERVER-side steer speed seam.

import (
	"github.com/imhinotori/sulfur/level/attribute"
)

// Pig steerable constants (VERIFIED javap Pig this task).
const (
	// itemCarrotOnAStick is Items.CARROT_ON_A_STICK (item id 887): the control item a rider must hold for
	// Pig.getControllingPassenger to steer the pig, and the item whose USE calls Pig.boost(). Cite
	// Pig.getControllingPassenger + Items.CARROT_ON_A_STICK.
	itemCarrotOnAStick = 887
	// pigRiddenSpeedFactor is Pig.getRiddenSpeed's ldc2 0.225d: getAttributeValue(MOVEMENT_SPEED) * 0.225 *
	// boostFactor(). Distinct from the strider's suffocating/steering 0.35/0.55 (a pig has no suffocating
	// state). Cite Pig.getRiddenSpeed.
	pigRiddenSpeedFactor = 0.225
)

// pigIsSaddled ports Pig.isSaddled() (EquipmentSlot.SADDLE presence). v1 has no equipment-slot item store,
// so the SADDLE slot folds to the striderSaddled bool (the shared ItemBasedSteering-mount SADDLE state; a
// pig and a strider never share an entity). Set by tryPigInteract equipping a SADDLE. Structured to become
// a real hasItemInSlot(SADDLE) read once the equipment-slot API lands. Cite Pig.isSaddled (the shared
// isSaddled seam).
func pigIsSaddled(e *Entity) bool { return e.striderSaddled }

// pigBoost ports Pig.boost() == steering.boost(getRandom()): start an ItemBasedSteering boost on the pig's
// OWN per-entity stream (nextInt(841)+140 draw). Identical to striderBoost -- the SAME ItemBasedSteering
// .boost. Returns true iff a new boost started (false if already boosting). Fires ONLY on a carrot-on-a-
// stick USE by the rider (never on the oracle pig). Cite Pig.boost + ItemBasedSteering.boost.
func pigBoost(e *Entity) bool {
	return striderBoost(e, mobRandom(e))
}

// pigTickBoost ports the steering.tickBoost() call inside Pig.tickRidden: advance the boost timer (identical
// to ItemBasedSteering.tickBoost, shared with the strider). NO RNG. Cite Pig.tickRidden + ItemBasedSteering
// .tickBoost.
func pigTickBoost(e *Entity) { striderTickBoost(e) }

// pigGetRiddenSpeed ports Pig.getRiddenSpeed(player): getAttributeValue(MOVEMENT_SPEED) * 0.225d *
// steering.boostFactor(), narrowed d2f. The boostFactor curve is the shared ItemBasedSteering.boostFactor
// (striderBoostFactor). Cite Pig.getRiddenSpeed.
func pigGetRiddenSpeed(e *Entity) float32 {
	base := e.getAttributeValue(attribute.MovementSpeed) // getAttributeValue(MOVEMENT_SPEED) (double)
	return float32(base * pigRiddenSpeedFactor * float64(striderBoostFactor(e)))
}

// pigRiderHoldingControlItem ports the Player.isHolding(CARROT_ON_A_STICK) check inside Pig.getControlling
// Passenger: a saddled pig is steerable ONLY while its rider holds the control item. isHolding checks
// MAINHAND (v1 reads the selected hotbar slot, the established held read). Cite Pig.getControllingPassenger
// + Player.isHolding.
func (t *TickLoop) pigRiderHoldingControlItem(p *tickPlayer) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	return !slotIsEmpty(held) && int32(held.ItemID) == itemCarrotOnAStick
}

// tryPigInteract ports Pig.mobInteract 1:1 for the v1 right-click saddle/mount. Vanilla:
//
//	boolean isFood = isFood(getItemInHand(hand));
//	if (!isFood && isSaddled() && !isVehicle() && !player.isSecondaryUseActive()) {
//	    if (!level().isClientSide()) player.startRiding(this);   // doPlayerRide-equivalent (Pig inlines it)
//	    return SUCCESS;
//	}
//	InteractionResult r = super.mobInteract(player, hand);       // Animal feed/breed path
//	if (!r.consumesAction()) {
//	    ItemStack stack = getItemInHand(hand);
//	    return isEquippableInSlot(stack, SADDLE) ? stack.interactLivingEntity(...) : PASS;   // SADDLE equip
//	}
//	return r;
//
// Returns true when the interact belongs to the pig (a MOUNT of a saddled pig, or a SADDLE equip); false to
// fall through to the shared feed path (handleInteract's tryFeedAnimal == super.mobInteract == the pig_food
// Animal.mobInteract) for the isFood case AND for an un-saddled pig clicked with a non-saddle item. The
// mount is the load-bearing v1 ride; the SADDLE equip folds to the striderSaddled bool (no equipment-slot
// item store), MIRRORING tryStriderInteract exactly. Pig-gated by the attack_dispatch caller (typ ==
// entity.Pig.ID), a zero-cost no-op for every other mob; the ONLY RNG the pig path can draw (the boost
// nextInt) is on a carrot USE, never here, so the pig oracle stream is unperturbed. Cite Pig.mobInteract.
func (t *TickLoop) tryPigInteract(p *tickPlayer, pig *Entity, usingSecondaryAction bool) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot)) // player.getItemInHand(hand)
	isFood := !slotIsEmpty(held) && itemInTag(int32(held.ItemID), "pig_food")

	// RIDE branch: !isFood && isSaddled() && !isVehicle() && !isSecondaryUseActive().
	if !isFood && pigIsSaddled(pig) && !pig.isVehicle() && !usingSecondaryAction {
		// player.startRiding(this). Broadcast the passenger list so the rider's client attaches and every
		// tracker renders the seated player.
		if t.playerStartRiding(p, pig, false) {
			t.broadcastSetPassengers(pig)
		}
		return true // SUCCESS -- the mount belongs to the pig
	}
	// SADDLE-equip branch (after super.mobInteract does not consume): a SADDLE item saddles the pig. v1 folds
	// the SADDLE equipment slot to the striderSaddled bool. isFood falls through to the shared feed path.
	if !isFood && !slotIsEmpty(held) && int32(held.ItemID) == itemSaddle && !pigIsSaddled(pig) {
		pig.striderSaddled = true
		// stack.interactLivingEntity == SaddleItem equip: consume 1 from the stack (server-side).
		held.Count--
		inv.set(heldWindowSlot(inv.heldSlot), held)
		return true // the saddle-equip belongs to the pig
	}
	// isFood (or an un-saddled pig with a non-saddle item) -> fall through to tryFeedAnimal (the shared
	// super.mobInteract == Animal.mobInteract pig_food feed/breed path). No RNG drawn here.
	return false
}
