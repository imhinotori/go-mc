package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// item_use.go is the 1:1 port of the vanilla item-use / EATING chain: the ServerboundUseItem
// handler (ServerPlayer.useItem → ItemStack.use → Consumable.startConsuming), the per-tick
// use-duration decrement (LivingEntity.updatingUsingItem → updateUsingItem), and completion
// (LivingEntity.completeUsingItem → ItemStack.finishUsingItem → Item.finishUsingItem →
// Consumable.onConsume → FoodProperties.onConsume → FoodData.eat(FoodProperties) + ItemStack
// .consume). It closes the 17-19 hunger loop: holding right-click on a food item runs the eat
// animation for the food's use-duration, then refills the food bar (FoodData.eat) and shrinks
// the stack by 1.
//
// v1 SCOPE: only FOOD items are usable (eating). Non-food item.use behaviors (bows, blocks,
// buckets, …) are out of v1 scope — the handler gates on FOOD-component presence and is a no-op
// otherwise (cite: other ItemStack.use paths unimplemented in v1).
//
// ALL state is tick-owned (TICK-05): handleUseItem and tickUseItem run only on the tick goroutine
// over the tick-owned useItem/useItemRemaining/useItemHand fields, so they are -race clean by the
// same single-owner discipline as the food/breath/dig seams. Every method and constant is cited
// against the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), decompiled via `javap -c -p` this
// session.
//
// Cited bytecode (FQCN.method):
//
//	net.minecraft.world.item.component.Consumable.startConsuming(LivingEntity, ItemStack, InteractionHand):
//	    if (!canConsume(entity, stack)) return FAIL;
//	    if (consumeTicks() > 0) { entity.startUsingItem(hand); return CONSUME; }
//	    else { onConsume(level, entity, stack); return CONSUME; }
//	net.minecraft.world.item.component.Consumable.canConsume(LivingEntity, ItemStack):
//	    FoodProperties food = stack.get(FOOD); if (food == null) return true;
//	    if (!(entity instanceof Player p)) return true; return p.canEat(food.canAlwaysEat());
//	net.minecraft.world.item.component.Consumable.consumeTicks(): (int)(consumeSeconds * 20.0f).
//	net.minecraft.world.entity.player.Player.canEat(boolean canAlwaysEat):
//	    return abilities.invulnerable || canAlwaysEat || foodData.needsFood();
//	net.minecraft.world.entity.LivingEntity.startUsingItem(InteractionHand):
//	    stack = getItemInHand(hand); if (stack.isEmpty() || isUsingItem()) return;
//	    useItem = stack; useItemRemaining = stack.getUseDuration(this) (== Consumable.consumeTicks).
//	net.minecraft.world.entity.LivingEntity.updatingUsingItem():
//	    if (isUsingItem()) { if (ItemStack.isSameItem(getItemInHand(usedHand), useItem)) {
//	        useItem = getItemInHand(usedHand); updateUsingItem(useItem); } else stopUsingItem(); }
//	net.minecraft.world.entity.LivingEntity.updateUsingItem(ItemStack):
//	    stack.onUseTick(...); if (--useItemRemaining == 0 && !isClientSide && !stack.useOnRelease())
//	        completeUsingItem();
//	net.minecraft.world.entity.LivingEntity.completeUsingItem():
//	    hand = getUsedItemHand(); if (!useItem.equals(getItemInHand(hand))) { releaseUsingItem(); return; }
//	    if (!useItem.isEmpty() && isUsingItem()) { result = useItem.finishUsingItem(level, this);
//	        if (result != useItem) setItemInHand(hand, result); stopUsingItem(); }
//	net.minecraft.world.item.ItemStack.finishUsingItem(Level, LivingEntity) -> Item.finishUsingItem:
//	    Consumable c = stack.get(CONSUMABLE); if (c != null) return c.onConsume(level, entity, stack);
//	    else return stack.
//	net.minecraft.world.item.component.Consumable.onConsume(Level, LivingEntity, ItemStack):
//	    ... particles/sounds/stats (v1 no-op) ...; for each ConsumableListener: listener.onConsume(...)
//	    -> FoodProperties.onConsume: player.getFoodData().eat(this);
//	    ... onConsumeEffects (status effects — v1 no-op) ...; gameEvent EAT/DRINK (v1 no-op);
//	    stack.consume(1, entity); return stack.
//	net.minecraft.world.food.FoodData.eat(FoodProperties): add(food.nutrition(), food.saturation()).
//	    (the ABSOLUTE form — NOT FoodConstants.saturationByModifier; verified via javap on
//	    FoodData.eat(FoodProperties): invokevirtual FoodProperties.nutrition / .saturation; invokevirtual add.)
//	net.minecraft.world.item.ItemStack.consume(int, LivingEntity):
//	    if (entity == null || !entity.hasInfiniteMaterials()) shrink(count). (Creative does NOT shrink.)
//	net.minecraft.network.protocol.game.ServerboundUseItemPacket: VarInt hand, VarInt sequence,
//	    Float yRot, Float xRot (read constructor: readEnum(InteractionHand)=VarInt, readVarInt,
//	    readFloat, readFloat — jar-verified).

// useDurationTicksPerSecond is the Consumable.consumeTicks scale: consumeSeconds * 20.0f then f2i
// (`getfield consumeSeconds; ldc 20.0f; fmul; f2i`). 1.6s food => 32 ticks.
const useDurationTicksPerSecond float32 = 20.0

// interactionHandMain / interactionHandOff are the InteractionHand enum ordinals (the
// ServerboundUseItem `hand` VarInt): 0 == MAIN_HAND, 1 == OFF_HAND (net.minecraft.world.
// InteractionHand declaration order).
const (
	interactionHandMain int32 = 0
	interactionHandOff  int32 = 1
)

// offHandMenuSlot is the player-inventory menu slot index of the offhand (slot 45 in the 46-slot
// window: 0-4 craft, 5-8 armor, 9-35 main, 36-44 hotbar, 45 offhand). The mainhand is the selected
// hotbar slot at menu index 36+heldSlot (Inventory.getSelected). Mirrors test_kit.go's 36-44 hotbar
// mapping.
const offHandMenuSlot int16 = 45

// hotbarMenuSlotBase is the menu-slot index of hotbar slot 0 (36). mainhand == 36 + heldSlot.
const hotbarMenuSlotBase int16 = 36

// itemFood maps a numeric item id to its FOOD/CONSUMABLE data (data/item/food.go, generated from
// the jar's items.json default-component report). It resolves id -> Item.Name -> "minecraft:<name>"
// -> the Food table. A non-food item (or an unknown id) returns ok=false — the use chain treats that
// as "not eatable" and no-ops (cite: v1 only handles eating). Tick-owned read.
func itemFood(itemID int32) (item.ItemFood, bool) {
	it, ok := item.ByID[item.ID(itemID)]
	if !ok {
		return item.ItemFood{}, false
	}
	f, ok := item.Food["minecraft:"+it.Name]
	return f, ok
}

// heldMenuSlot returns the menu-slot index of the item in the given hand: 36+heldSlot for the
// mainhand, 45 for the offhand. Mirrors LivingEntity.getItemInHand(hand) -> Inventory.getSelected /
// offhand slot. Tick-owned.
func heldMenuSlot(p *tickPlayer, hand int32) int16 {
	inv := ensureInventory(p)
	if hand == interactionHandOff {
		return offHandMenuSlot
	}
	return hotbarMenuSlotBase + inv.heldSlot
}

// slotIsEmpty is the ItemStack.isEmpty() port for a component-slot: Count <= 0 (the empty sentinel,
// matching inventory.go's slotDataEqual emptiness rule).
func slotIsEmpty(s component.SlotData) bool { return s.Count <= 0 }

// isSameItem is net.minecraft.world.item.ItemStack.isSameItem(a, b): same item identity (both
// non-empty with equal ItemID), ignoring components. Used by updatingUsingItem to detect the hand
// still holding the use item. Two empties are NOT "same item" (vanilla: isSameItem requires the
// same Item, and EMPTY's item is AIR — but updatingUsingItem only reaches here while useItem is
// non-empty, so the empty-vs-empty case is moot).
func isSameItem(a, b component.SlotData) bool {
	if slotIsEmpty(a) || slotIsEmpty(b) {
		return false
	}
	return a.ItemID == b.ItemID
}

// slotDataIdentical is the ItemStack.equals(Object) port used by completeUsingItem's
// `!useItem.equals(getItemInHand(hand))` guard: count + item + components all equal (or both empty).
// Reuses inventory.go's slotDataEqual (same emptiness + item + raw-component-bytes comparison).
func slotDataIdentical(a, b component.SlotData) bool { return slotDataEqual(a, b) }

// isUsingItem is net.minecraft.world.entity.LivingEntity.isUsingItem(): useItem is non-empty. (The
// vanilla flag is a SynchedEntityData bit set in startUsingItem and cleared in stopUsingItem; the
// observable predicate is `!useItem.isEmpty()`, which v1 reads directly off the tick-owned useItem.)
func isUsingItem(p *tickPlayer) bool { return !slotIsEmpty(p.useItem) }

// handleUseItem resolves a ServerboundUseItem on-tick (the right-click-to-eat path). It decodes the
// jar-verified wire (VarInt hand, VarInt sequence, Float yRot, Float xRot) defensively — a Scan
// error is a silent no-op — then runs the ServerPlayer.useItem → ItemStack.use → Consumable
// .startConsuming chain for a FOOD item: gate on canConsume, and (since food's consumeTicks is
// 32 > 0) begin the timed use via startUsingItem. Tick-owned.
func (t *TickLoop) handleUseItem(p *tickPlayer, pkt pk.Packet) {
	var hand pk.VarInt
	var sequence pk.VarInt
	var yRot, xRot pk.Float
	if err := pkt.Scan(&hand, &sequence, &yRot, &xRot); err != nil {
		return // malformed/short: no-op (defensive decode)
	}
	_ = sequence // the use-item sequence is the block-edit ack id; eating performs no block edit,
	_ = yRot     // so there is no predicted block change to ack (cite ServerboundUseItem.sequence —
	_ = xRot     // reused by the block-place predictive path, not by a pure consume). yRot/xRot are
	// the look angles at use time (decoded for wire correctness; the eat does not re-aim the player).

	// InteractionHand: only 0 (MAIN) / 1 (OFF) are valid; a forged ordinal is ignored.
	h := int32(hand)
	if h != interactionHandMain && h != interactionHandOff {
		return
	}

	// GATE-ONLY (SULFUR_TEST_KIT=1) egg AIR trigger: vanilla SpawnEggItem.useOn spawns on a clicked
	// BLOCK, never on right-click-air, so prod NEVER spawns here (the kit is off + no prod player holds
	// the egg — T-28-03). But the PLUGIN-07 bot (gateItem1CustomMob) fires the egg via ServerboundUseItem
	// (right-click air), so the gate-only air path must honor it: if the kit is on and the held item is
	// the gate spawn egg, spawn the declared mob in front and consume the use. handleGateSpawnEgg itself
	// early-returns when the kit is off, so this is a hard no-op in prod. Placed BEFORE useItemInHand so
	// the egg never falls through to the food/boat/rod resolution.
	if testKitEnabled() {
		inv := ensureInventory(p)
		held := inv.get(heldMenuSlot(p, h))
		if !slotIsEmpty(held) && item.ID(held.ItemID) == gateSpawnEggID {
			t.handleGateSpawnEgg(p)
			return
		}
	}

	t.useItemInHand(p, h)
}

// useItemInHand is the ServerPlayer.useItem → ItemStack.use → Consumable.startConsuming port for the
// held stack, gated to FOOD items for v1. The held stack is read off the menu slot for the hand
// (mainhand 36+heldSlot, offhand 45). A non-food / empty held item is a no-op (v1 only eats).
// Tick-owned.
func (t *TickLoop) useItemInHand(p *tickPlayer, hand int32) {
	// Already using an item: startUsingItem's `isUsingItem()` guard would reject re-entry, and the
	// client only sends a fresh ServerboundUseItem to (re)start; bail to mirror startUsingItem's guard.
	if isUsingItem(p) {
		return
	}

	inv := ensureInventory(p)
	held := inv.get(heldMenuSlot(p, hand))
	if slotIsEmpty(held) {
		return // empty hand: nothing to use (ItemStack.use on AIR is a PASS no-op in v1 scope)
	}

	// BOAT ITEM (BoatItem.use): a right-click-air with a boat/raft item raytraces to the water surface
	// (Fluid.ANY) and spawns the boat there (facing the player), consuming 1 item. It runs BEFORE the food
	// gate (a boat item is not food) — a non-boat item returns false and falls through to the food path.
	// Boat-item-gated (zero-cost id switch for every other item — no RNG draw, so the pig oracle is
	// unperturbed). CITE BoatItem.use (getPlayerPOVHitResult + addFreshEntity + itemStack.consume(1)).
	if t.tryUseBoatItem(p, inv, held, hand) {
		return // the boat item handled the use (a boat spawned, or a MISS/FAIL no-op)
	}

	// THROWABLE ITEM (SnowballItem/EggItem/EnderpearlItem.use): a right-click-air throws the item as a
	// ThrowableProjectile from the player's eye toward the look direction (shoot power 1.5), consuming 1.
	// Runs before the food gate (a throwable is not food); a non-throwable falls through. Throwable-gated
	// (a cheap id compare, no RNG draw — the pig oracle is unperturbed). CITE: SnowballItem.use etc.
	if t.tryThrowItem(p, inv, held, hand) {
		return
	}

	// FISHING ROD (FishingRodItem.use): a right-click with a fishing rod casts a FishingHook (bobber)
	// toward the look direction, or — if a hook is already out — reels it in (retrieve: pull a hooked
	// entity or roll the FISHING loot table + spawn the caught item flying to the player). It runs
	// BEFORE the food gate (a rod is not food); a non-rod item returns false and falls through.
	// Fishing-rod-gated (a cheap id compare for every other item — no RNG draw, so the pig oracle is
	// unperturbed). CITE FishingRodItem.use. Body in fishing.go.
	if t.tryUseFishingRod(p, held, hand) {
		return // the rod handled the use (a cast or a reel)
	}

	// FOOD gate (v1): resolve the held item's FOOD/CONSUMABLE data. Non-food => not eatable => no-op
	// (cite: other ItemStack.use behaviors out of v1 scope).
	f, ok := itemFood(int32(held.ItemID))
	if !ok {
		return
	}

	// Consumable.canConsume → Player.canEat(canAlwaysEat):
	//   abilities.invulnerable || canAlwaysEat || foodData.needsFood()
	// abilities.invulnerable maps to creative in v1 (the same hasInfiniteMaterials gate the dig/place
	// paths use). needsFood() == food < 20 (FoodData.needsFood). If canEat is false, startConsuming
	// returns FAIL and nothing happens.
	invulnerable := p.gameMode == gameModeCreative
	if !invulnerable && !f.CanAlwaysEat && !p.foodNeedsFood() {
		return // canEat == false: cannot eat at full hunger (and not a canAlwaysEat food)
	}

	// consumeTicks() = (int)(consumeSeconds * 20.0f). For food this is 32 (> 0), so startConsuming
	// goes through startUsingItem (the timed-use path) rather than an immediate onConsume.
	consumeTicks := int32(f.ConsumeSeconds * useDurationTicksPerSecond)
	if consumeTicks <= 0 {
		// consumeTicks == 0: vanilla onConsume's run immediately. No v1 food has consumeTicks 0
		// (all are >= 16), but mirror the branch faithfully so a 0-tick consumable still eats at once.
		t.finishUsingItemNow(p, hand, held)
		return
	}

	// startUsingItem(hand): useItem = held; useItemRemaining = consumeTicks; record the hand.
	p.useItem = held
	p.useItemRemaining = consumeTicks
	p.useItemHand = hand
	// setLivingEntityFlag(USING_ITEM, true) + (OFFHAND, hand==OFF_HAND): push the using pose to
	// observers (LivingEntity.startUsingItem -> SynchedEntityData). The eater predicts its own
	// first-person animation; this is the 3rd-person pose other players see.
	t.broadcastUsingItem(p, true, hand)
}

// tickUseItem is the per-tick LivingEntity.updatingUsingItem step, run for every player inside
// tickEntities (ADDITIVE, no phase reorder). While the player is using an item: if the hand still
// holds the same item (ItemStack.isSameItem), re-sync useItem to the hand's stack and decrement the
// remaining ticks (updateUsingItem) — completeUsingItem fires when the counter reaches 0; if the
// hand changed item, the use is cancelled (stopUsingItem). Tick-owned.
func (t *TickLoop) tickUseItem() {
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if !isUsingItem(p) {
			continue
		}
		held := ensureInventory(p).get(heldMenuSlot(p, p.useItemHand))
		if isSameItem(held, p.useItem) {
			// updatingUsingItem: useItem = getItemInHand(usedHand); updateUsingItem(useItem).
			p.useItem = held
			t.updateUsingItem(p)
		} else {
			// hand no longer holds the use item -> cancel.
			t.stopUsingItem(p)
		}
	}
}

// updateUsingItem is net.minecraft.world.entity.LivingEntity.updateUsingItem(ItemStack): decrement
// useItemRemaining, and when it reaches 0 (server-side, and the item is not a use-on-release item —
// food is not) complete the use. EXACT order: `if (--useItemRemaining == 0 && !isClientSide &&
// !useOnRelease()) completeUsingItem()` (the bytecode does `getfield; iconst_1; isub; dup_x1;
// putfield; ifne` — store the decremented value, then branch on == 0). onUseTick (per-tick item
// effects like the spyglass/bow) is a v1 no-op for food. Tick-owned.
func (t *TickLoop) updateUsingItem(p *tickPlayer) {
	// stack.onUseTick(...): v1 food has no per-tick use effect — faithful no-op (cite ItemStack.onUseTick).
	p.useItemRemaining--
	// useOnRelease() is false for food (only a few items like the trident return true); the
	// !isClientSide guard is always true server-side. So at 0 ticks remaining, complete the use.
	if p.useItemRemaining == 0 {
		t.completeUsingItem(p)
	}
}

// completeUsingItem is net.minecraft.world.entity.LivingEntity.completeUsingItem (server-side path):
//
//	hand = getUsedItemHand();
//	if (!useItem.equals(getItemInHand(hand))) { releaseUsingItem(); return; }
//	if (!useItem.isEmpty() && isUsingItem()) {
//	    result = useItem.finishUsingItem(level, this);   // Consumable.onConsume: eat + shrink
//	    if (result != useItem) setItemInHand(hand, result);
//	    stopUsingItem();
//	}
//
// The `useItem.equals(getItemInHand(hand))` guard is the FULL ItemStack.equals (count + item +
// components), so a hand whose stack count/components drifted from the captured useItem bails to
// releaseUsingItem (which, like stopUsingItem, just clears the use state in v1 — no in-progress
// projectile to release). Tick-owned.
func (t *TickLoop) completeUsingItem(p *tickPlayer) {
	hand := p.useItemHand
	slot := heldMenuSlot(p, hand)
	inv := ensureInventory(p)
	current := inv.get(slot)

	// `!useItem.equals(getItemInHand(hand))`: the hand item drifted from the captured useItem -> bail.
	if !slotDataIdentical(p.useItem, current) {
		t.releaseUsingItem(p)
		return
	}

	if !slotIsEmpty(p.useItem) && isUsingItem(p) {
		// result = useItem.finishUsingItem(level, this): apply the FOOD effect + consume the stack.
		result := t.finishUsingItem(p, p.useItem)
		// setItemInHand(hand, result): write the (shrunk) stack back. Vanilla gates on result !=
		// useItem (reference inequality); finishUsingItem always returns the mutated stack value here,
		// which differs from the pre-eat useItem (count shrank), so the write always lands. Writing it
		// unconditionally is observably identical (the slot already held the same item).
		inv.set(slot, result)
		t.stopUsingItem(p)
		udebugPlayer(p, "eat", "consumed item=%d -> food=%d sat=%.1f hp=%.1f", p.useItem.ItemID, p.food, p.saturation, p.health)
		// SYNC: food/saturation changed (FoodData.eat) and the held slot shrank — push both to the
		// client (the HUD reads food/saturation off SetHealth; the slot reads off SetSlot).
		t.syncAfterEat(p, inv, slot, result)
	}
}

// finishUsingItem is the ItemStack.finishUsingItem(Level, LivingEntity) -> Item.finishUsingItem ->
// Consumable.onConsume -> FoodProperties.onConsume port for a FOOD item:
//
//	player.getFoodData().eat(food);      // == foodData.add(nutrition, saturation) — ABSOLUTE form
//	stack.consume(1, player);            // shrink(1) UNLESS player.hasInfiniteMaterials() (creative)
//	return stack;                        // the modified (shrunk) stack
//
// It returns the post-eat stack (a copy of useItem with the count decremented in survival, unchanged
// in creative). The eat uses foodAdd (FoodData.add) with the food component's ABSOLUTE nutrition +
// saturation — NOT foodEat/saturationByModifier (that would DOUBLE the saturation). Cited stubs:
// onConsumeEffects (golden_apple regen/absorption — v1 has no status-effect system), the burp/eat
// sound, and the EAT/DRINK gameEvent are all faithful no-ops, structured to become real later.
// Tick-owned.
func (t *TickLoop) finishUsingItem(p *tickPlayer, stack component.SlotData) component.SlotData {
	f, ok := itemFood(int32(stack.ItemID))
	if ok {
		// FoodProperties.onConsume -> FoodData.eat(FoodProperties) -> add(nutrition, saturation).
		// ABSOLUTE saturation (the FOOD component value), applied via the ported FoodData.add
		// (foodAdd: clamp food to [0,20], saturation to [0, food]). NOT saturationByModifier.
		p.foodAdd(f.Nutrition, f.Saturation)
		// onConsumeEffects (status effects), the burp sound, and the EAT/DRINK gameEvent: v1 no-ops
		// (no effects/sound/game-event systems yet) — cited, structured to become real later.
	}

	// ItemStack.consume(1, player): shrink by 1 UNLESS the player has infinite materials (creative).
	result := stack
	if p.gameMode != gameModeCreative {
		result.Count--
		if result.Count <= 0 {
			// shrink to empty: the slot becomes the empty stack (ItemStack.EMPTY).
			result = component.SlotData{Count: 0}
		}
	}
	return result
}

// finishUsingItemNow is the consumeTicks==0 immediate-onConsume branch of Consumable.startConsuming
// (no timed use): apply the eat and shrink right away, then sync. No v1 food reaches this (all have
// consumeTicks >= 16), but it mirrors the vanilla branch faithfully. Tick-owned.
func (t *TickLoop) finishUsingItemNow(p *tickPlayer, hand int32, held component.SlotData) {
	inv := ensureInventory(p)
	slot := heldMenuSlot(p, hand)
	result := t.finishUsingItem(p, held)
	inv.set(slot, result)
	t.syncAfterEat(p, inv, slot, result)
}

// stopUsingItem is net.minecraft.world.entity.LivingEntity.stopUsingItem(): clear the use state
// (useItem = EMPTY) and clear the SynchedEntityData USING flag for observers (DATA_LIVING_ENTITY_FLAGS
// -> bit 0x01 false), so the 3rd-person eat pose ends. Tick-owned.
func (t *TickLoop) stopUsingItem(p *tickPlayer) {
	wasUsing := isUsingItem(p)
	p.useItem = component.SlotData{Count: 0}
	p.useItemRemaining = 0
	// setLivingEntityFlag(USING_ITEM, false): clear the using pose for observers. Only broadcast
	// when the player WAS using (avoids a spurious clear for a no-op stop). Cite:
	// LivingEntity.stopUsingItem -> setLivingEntityFlag(1, false).
	if wasUsing {
		t.broadcastUsingItem(p, false, p.useItemHand)
	}
}

// releaseUsingItem is net.minecraft.world.entity.LivingEntity.releaseUsingItem(): in vanilla it lets
// the item handle an early release (e.g. firing a bow). For food there is nothing to release, so it
// reduces to clearing the use state — identical to stopUsingItem in v1 (cite: no charge-release items
// in v1 scope). Tick-owned.
func (t *TickLoop) releaseUsingItem(p *tickPlayer) {
	t.stopUsingItem(p)
}

// syncAfterEat pushes the authoritative state the client needs after a completed eat: the food bar
// (food/saturation changed by FoodData.eat — reuse the dirty-send SetHealth via syncFood) and the
// shrunk held slot (ClientboundContainerSetSlot for the consumed item). The 3rd-person eat pose
// (the DATA_LIVING_ENTITY_FLAGS IS_USING bit observers read) is cleared by stopUsingItem, which
// completeUsingItem calls right before this; the FIRST-person animation is the eater's own
// prediction off the ServerboundUseItem it sent. Tick-owned.
func (t *TickLoop) syncAfterEat(p *tickPlayer, inv *Inventory, slot int16, result component.SlotData) {
	// Food/saturation/health dirty-send (reuses the same SetHealth carrier tickFood uses).
	t.syncFood(p)
	// The held slot shrank: send an authoritative SetSlot so the client reflects the consumed item.
	if p.client != nil {
		inv.stateID = (inv.stateID + 1) & 0x7FFF
		p.client.Send(containerSetSlot(playerContainerID, inv.stateID, slot, result))
	}
}
