package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
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

	// WIND CHARGE (WindChargeItem.use): a right-click-air fires a WindCharge (a hurting projectile — dead
	// straight, no gravity) from the player's eye toward the look direction (shoot power 1.5), consuming 1.
	// Runs before the food gate (a wind charge is not food); a non-wind-charge falls through. Wind-charge-gated
	// (a cheap id compare, no RNG — the pig oracle is unperturbed). CITE WindChargeItem.use. Body in hurting_projectile.go.
	if t.tryUseWindCharge(p, inv, held, hand) {
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

	// BUCKET (BucketItem.use): a right-click-air with a water/lava bucket empties the fluid at the
	// raycast target (a source placed at pos.relative(face)), and an EMPTY bucket over a fluid SOURCE
	// picks it up (→ the filled bucket). Runs BEFORE the food gate (a bucket is not food); a non-bucket
	// item returns false and falls through. Bucket-gated (a cheap id compare — no RNG draw, so the pig
	// oracle is unperturbed). CITE BucketItem.use. Body below.
	if t.tryUseBucket(p, inv, held, hand) {
		return
	}

	// WATERLILY (PlaceOnWaterBlockItem.use): a right-click-air with a lily_pad raytraces to a water SOURCE
	// and places the lily_pad on the cell ABOVE it (consuming 1 in survival). Runs before the food gate (a
	// lily_pad is not food); a non-lily_pad falls through. Lily-pad-gated (a cheap id compare, no RNG draw
	// -- the pig oracle is unperturbed). CITE PlaceOnWaterBlockItem.use. Body in waterlily.go.
	if t.tryUseWaterlily(p, inv, held, hand) {
		return
	}

	// BOW / CROSSBOW (BowItem.use / CrossbowItem.use): a right-click with a bow/crossbow begins the draw
	// (startUsingItem), or -- for a charged crossbow -- fires the loaded bolt immediately. The release (the
	// RELEASE_USE_ITEM player action) fires the bow arrow. Runs before the food gate (a bow is not food); a
	// non-bow item returns false and falls through. Bow/crossbow-gated (a cheap id compare, no RNG draw -- the
	// pig oracle is unperturbed). CITE BowItem.use / CrossbowItem.use. Body in bow.go.
	if t.tryStartBowUse(p, inv, held, hand) {
		return
	}

	// TRIDENT (TridentItem.use): a right-click with a trident begins the draw (startUsingItem), gated on the
	// riptide water/rain condition (a riptide trident can only be drawn in water or rain) and the near-broken
	// durability guard. The release (RELEASE_USE_ITEM) throws the ThrownTrident (power 2.5) or, for a riptide
	// trident, launches the player. Runs before the food gate (a trident is not food); a non-trident item
	// returns false and falls through. Trident-gated (a cheap id compare, no RNG draw -- the pig oracle is
	// unperturbed). CITE TridentItem.use. Body in trident.go.
	if t.tryStartTridentUse(p, inv, held, hand) {
		return
	}

	// EMPTY MAP (EmptyMapItem.use): a right-click-air with an empty map (minecraft:map) creates a fresh
	// filled_map centered on the player, shrinks the empty map by 1, and puts the filled map in the held
	// slot (or the inventory). Runs before the food gate (a map is not food); a non-map item falls through.
	// Empty-map-gated (a cheap id compare, no RNG draw -- the pig oracle is unperturbed). CITE: EmptyMapItem.use.
	if t.tryUseEmptyMap(p, inv, held, hand) {
		return
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
	if t.releaseBowOrCrossbow(p) {
		return // the bow/crossbow handled the release (fired/loaded + cleared the use state)
	}
	if t.releaseTrident(p) {
		return // the trident handled the release (thrown / riptide-launched + cleared the use state)
	}
	t.stopUsingItem(p)
}

// bucketContent discriminates what a bucket item carries: EMPTY (pick up a fluid), WATER, or LAVA
// (empty out that fluid). It is the Go stand-in for BucketItem.content (Fluids.EMPTY / WATER / LAVA).
type bucketContent int

const (
	bucketEmpty bucketContent = iota // an empty bucket (Fluids.EMPTY) — picks up a fluid source
	bucketWater                      // a water bucket (Fluids.WATER) — empties a water source
	bucketLava                       // a lava bucket (Fluids.LAVA)  — empties a lava source
)

// bucketContentOf maps an item id to its BucketItem.content, reporting ok=false for a non-bucket
// item. Only the three fluid buckets (empty/water/lava) are handled — the mob/milk/powder-snow
// buckets are not BucketItem-with-a-fluid and fall through (ok=false). Tick-owned read.
func bucketContentOf(itemID int32) (bucketContent, bool) {
	switch item.ID(itemID) {
	case item.Bucket.ID:
		return bucketEmpty, true
	case item.WaterBucket.ID:
		return bucketWater, true
	case item.LavaBucket.ID:
		return bucketLava, true
	}
	return 0, false
}

// tryUseBucket is the BucketItem.use port for the right-click-air path (net.minecraft.world.item.
// BucketItem.use). It raycasts from the player's eyes (getPlayerPOVHitResult with the bucket's
// ClipContext.Fluid: SOURCE_ONLY for an empty bucket so the ray stops on a fluid source, NONE for a
// filled bucket so the ray passes through fluid to the first solid), and then:
//
//   - MISS -> PASS (no-op, but the use belongs to the bucket -> return true).
//   - EMPTY bucket over a fluid SOURCE at the hit block: BucketPickup.pickupBlock removes the source
//     (→ AIR) and yields the filled bucket; swap the hand via ItemUtils.createFilledResult.
//   - FILLED bucket: emptyContents places the fluid SOURCE at pos.relative(face) (v1 has no
//     LiquidBlockContainer, so the place pos is always the relative cell), then swap the hand to the
//     empty Bucket via ItemUtils.createFilledResult(getEmptySuccessItem(...)).
//
// Creative-gated exactly as vanilla: getEmptySuccessItem / createFilledResult only consume/swap the
// hand when !player.hasInfiniteMaterials() (creative keeps the bucket unchanged). Bucket-gated (a
// cheap id compare — no RNG draw, so the pig oracle is unperturbed). Returns true when the use
// belonged to a bucket (handled or a MISS/FAIL no-op), false to fall through. Tick-owned.
//
//	[VERIFIED javap BucketItem.use: hit = getPlayerPOVHitResult(level, player, getFluidContext());
//	 if hit.type==MISS return PASS; if hit.type!=BLOCK return PASS; blockPos=hit.getBlockPos();
//	 dir=hit.getDirection(); relPos=blockPos.relative(dir);
//	 // FILLED: if emptyContents(player, level, LiquidBlockContainer?blockPos:relPos, hit) ->
//	 //   checkExtraContent (no-op); awardStat; return createFilledResult(hand, player,
//	 //   getEmptySuccessItem(hand, player)).
//	 // EMPTY: if content==EMPTY { state=getBlockState(blockPos); if block instanceof BucketPickup:
//	 //   filled = pickupBlock(player, level, blockPos, state); if !filled.isEmpty() { awardStat;
//	 //   getPickupSound; gameEvent(FLUID_PICKUP); return createFilledResult(hand, player, filled); } }
//	 // else FAIL.  getFluidContext: content==EMPTY ? SOURCE_ONLY : NONE.
//	 // getEmptySuccessItem: hasInfiniteMaterials ? hand : new ItemStack(BUCKET).]
func (t *TickLoop) tryUseBucket(p *tickPlayer, inv *Inventory, held component.SlotData, hand int32) bool {
	content, ok := bucketContentOf(int32(held.ItemID))
	if !ok {
		return false // not a fluid bucket: fall through
	}
	if t.world() == nil {
		return false
	}

	// getPlayerPOVHitResult(getFluidContext()): SOURCE_ONLY for the empty bucket (stop on a fluid
	// source), NONE for a filled bucket (fluid is passable — stop on the first solid).
	fluidMode := clipFluidNone
	if content == bucketEmpty {
		fluidMode = clipFluidSourceOnly
	}
	blockPos, dir, hit := t.bucketPovHitResult(p, fluidMode)
	if !hit {
		return true // MISS -> PASS (the use is still "for" the bucket — no fall-through)
	}
	dx, dy, dz := directionNormal(dir)
	relPos := pk.Position{X: blockPos.X + dx, Y: blockPos.Y + dy, Z: blockPos.Z + dz}

	if content == bucketEmpty {
		// EMPTY bucket: BucketPickup.pickupBlock on the HIT block (the source). v1's fluids are
		// FlowingFluid (water/lava) whose block IS the BucketPickup (LiquidBlock.pickupBlock removes
		// a SOURCE only, returning the filled bucket; a flowing cell returns EMPTY). Mirror that:
		// only a SOURCE cell is pickupable.
		fs := t.fluidAt(blockPos)
		var filledID item.ID
		switch {
		case fs.isWater && fs.source:
			filledID = item.WaterBucket.ID
		case fs.isLava && fs.source:
			filledID = item.LavaBucket.ID
		default:
			return true // no source at the hit block -> FAIL (BucketPickup returns EMPTY / not a source)
		}
		// pickupBlock: remove the source (LiquidBlock.pickupBlock sets the cell to AIR).
		t.setFluidBlock(blockPos, airStateID())
		// A picked-up source opens a hole its fluid neighbors must re-flow into: kick the neighbors so
		// adjacent flowing water/lava re-evaluates (LiquidBlock neighborChanged -> scheduleTick).
		t.scheduleFluidNeighborsOnEdit(blockPos)
		// awardStat / getPickupSound / gameEvent(FLUID_PICKUP): v1 no-ops (no stats/sound/game-event).
		filled := component.SlotData{Count: 1, ItemID: pk.VarInt(filledID)}
		t.bucketCreateFilledResult(p, inv, hand, held, filled)
		return true
	}

	// FILLED bucket: emptyContents places the fluid source. v1 has no LiquidBlockContainer, so the
	// place pos is always relPos (the cell adjacent to the hit face). emptyContents places when the
	// target cell can be replaced (air or a fluid — never inside a solid).
	if !t.bucketCanEmptyInto(relPos) {
		return true // FAIL: cannot place here (target is a solid) — the interact is still the bucket's
	}
	var srcState block.StateID
	switch content {
	case bucketWater:
		srcState = waterStateID(0) // FlowingFluid.getSource(false).createLegacyBlock() -> level 0 source
	case bucketLava:
		srcState = lavaStateID(0)
	}
	// Level.setBlock(relPos, sourceState, UPDATE_CLIENTS): write + broadcast, then kick the fluid sim
	// so the placed source begins flowing (LiquidBlock.onPlace -> scheduleTick). setFluidBlock is the
	// shared write+broadcast primitive.
	t.setFluidBlock(relPos, srcState)
	t.scheduleFluidNeighborsOnEdit(relPos)
	// checkExtraContent (no-op) / awardStat (no-op). getEmptySuccessItem: creative keeps the filled
	// bucket, survival yields a new empty Bucket. createFilledResult then swaps/consumes the hand.
	empty := component.SlotData{Count: 1, ItemID: pk.VarInt(item.Bucket.ID)}
	t.bucketCreateFilledResult(p, inv, hand, held, empty)
	return true
}

// bucketCanEmptyInto is the v1 subset of BucketItem.emptyContents's replaceable test
// (blockState.canBeReplaced(content) || blockState.isAir()): a fluid source may be emptied into a
// cell that is AIR or already a fluid (water or lava) — never into a solid. Cite: BlockState
// .canBeReplaced for fluids/air is true, for solids false. Tick-owned.
func (t *TickLoop) bucketCanEmptyInto(pos pk.Position) bool {
	id, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false // unloaded: do not place (no column to mutate)
	}
	if block.IsAir(id) {
		return true
	}
	fs := decodeFluid(id)
	return fs.isWater || fs.isLava
}

// bucketCreateFilledResult is the ItemUtils.createFilledResult(hand, player, filled) port for the
// bucket hand-swap (net.minecraft.world.item.ItemUtils.createFilledResult, the 3-arg overload that
// defaults putFilledIfInfinite=true):
//
//	if (player.hasInfiniteMaterials()) { if (!inventory.contains(filled)) inventory.add(filled); return hand; }
//	hand.consume(1, player);            // shrink the source bucket by 1
//	if (hand.isEmpty()) return filled;  // the whole stack was the bucket -> the hand becomes `filled`
//	if (!inventory.add(filled)) player.drop(filled, false); // else route `filled` to the inventory
//	return hand;
//
// v1 has no ItemEntity-from-player drop for a full inventory, so a failed add is a cited no-op (the
// item is lost only when the inventory is full — a rare edge; the drop is a follow-up). It writes the
// resulting hand stack back and broadcasts the changed slots. Creative (hasInfiniteMaterials) never
// consumes the source bucket and only tops up `filled` if absent. Tick-owned.
func (t *TickLoop) bucketCreateFilledResult(p *tickPlayer, inv *Inventory, hand int32, handStack, filled component.SlotData) {
	slot := heldMenuSlot(p, hand)
	before := inv.snapshot()

	if p.gameMode == gameModeCreative {
		// hasInfiniteMaterials: keep the source bucket; add `filled` only if not already present.
		if !inv.bucketContains(filled) {
			inv.bucketAdd(filled)
		}
		t.broadcastInventoryChanges(p, inv, before)
		return
	}

	// hand.consume(1): shrink the source bucket by 1.
	consumed := handStack
	consumed.Count--
	if consumed.Count <= 0 {
		// hand.isEmpty(): the hand becomes `filled` (the common single-bucket case).
		inv.set(slot, filled)
		t.broadcastInventoryChanges(p, inv, before)
		return
	}
	// The hand still holds >1 bucket: keep the shrunk stack in the hand, route `filled` to inventory.
	inv.set(slot, consumed)
	if !inv.bucketAdd(filled) {
		// inventory.add failed (full): vanilla drops it as an ItemEntity. No ItemEntity-from-player
		// drop in v1 -> cited no-op (the filled bucket is only lost when the inventory is full).
		_ = filled
	}
	t.broadcastInventoryChanges(p, inv, before)
}

// bucketContains is the v1 Inventory.contains(stack) subset used by createFilledResult's creative
// top-up: any main/hotbar slot already holds an item with the same id. (Vanilla compares item
// identity; the bucket `filled` carries no distinguishing components.) Tick-owned.
func (inv *Inventory) bucketContains(stack component.SlotData) bool {
	for i := int16(9); i < playerInventorySize; i++ {
		s := inv.get(i)
		if s.Count > 0 && s.ItemID == stack.ItemID {
			return true
		}
	}
	return false
}

// bucketAdd is the v1 Inventory.add(stack) subset: place the single-count `filled` bucket into the
// first empty slot, hotbar (36..44) first then the main inventory (9..35), matching vanilla's
// hotbar-first fill preference. (Water/lava/empty buckets do not stack in a merge-relevant way here,
// so no partial-merge is attempted.) Returns false when there is no free main/hotbar slot (the
// caller then drops it — a v1 no-op). Tick-owned.
func (inv *Inventory) bucketAdd(filled component.SlotData) bool {
	for i := hotbarMenuSlotBase; i < playerInventorySize; i++ {
		if inv.get(i).Count <= 0 {
			inv.set(i, filled)
			return true
		}
	}
	for i := int16(9); i < hotbarMenuSlotBase; i++ {
		if inv.get(i).Count <= 0 {
			inv.set(i, filled)
			return true
		}
	}
	return false
}

// clipFluidMode is the ClipContext.Fluid subset the bucket raycast needs: NONE (fluid is passable —
// stop on the first solid) or SOURCE_ONLY (stop on a fluid SOURCE cell). (ANY, used by the boat, is
// a third mode not needed here.) Cite: net.minecraft.world.level.ClipContext$Fluid.
type clipFluidMode int

const (
	clipFluidNone       clipFluidMode = iota // fluid passable (filled bucket: ray hits the first solid)
	clipFluidSourceOnly                      // stop on a fluid source (empty bucket: ray hits a source)
)

// bucketPovHitResult is the v1 port of Item.getPlayerPOVHitResult(level, player, ClipContext.Fluid)
// for the bucket use: a voxel-stepped raytrace from the player's eye along the view vector for the
// interaction range, returning the first block cell the ray enters that stops it (per the fluid
// mode) AND the face Direction the ray crossed to enter it (needed for pos.relative(face)). Vanilla
// runs a precise voxel-DDA ClipContext clip; v1 uses a fine fixed-step march (0.02-block steps) and
// derives the entered face from the axis whose integer cell changed on the crossing step — the
// observable result (which block + which face the player is aiming at) is identical for the block
// grid a bucket interacts with. A finer DDA is a cited follow-up if a partial-height fluid edge ever
// matters.
//
//	[VERIFIED javap Item.getPlayerPOVHitResult: from = getEyePosition(); to = from + viewVector *
//	 blockInteractionRange(); return level.clip(new ClipContext(from, to, OUTLINE, fluidMode, player))
//	 -> BlockHitResult with getBlockPos() and getDirection().]
func (t *TickLoop) bucketPovHitResult(p *tickPlayer, mode clipFluidMode) (pos pk.Position, dir int, hit bool) {
	ex := p.x
	ey := p.y + playerStandingEyeHeight
	ez := p.z
	vx, vy, vz := playerViewVector(p.yaw, p.pitch)

	const step = 0.02
	reach := float64(blockReach)
	prevX, prevY, prevZ := mthFloor(ex), mthFloor(ey), mthFloor(ez)
	for d := step; d <= reach; d += step {
		cx := ex + vx*d
		cy := ey + vy*d
		cz := ez + vz*d
		bx, by, bz := mthFloor(cx), mthFloor(cy), mthFloor(cz)
		if bx == prevX && by == prevY && bz == prevZ {
			continue // still in the same cell as the last step
		}

		// Determine what stops the ray at this cell.
		stops := false
		if t.blockSolidAt(bx, by, bz) {
			stops = true // OUTLINE: a solid block always stops the ray.
		} else if mode == clipFluidSourceOnly {
			// SOURCE_ONLY: a fluid SOURCE cell (water or lava source) stops the ray.
			fs := t.fluidAt(pk.Position{X: bx, Y: by, Z: bz})
			if (fs.isWater || fs.isLava) && fs.source {
				stops = true
			}
		}
		if !stops {
			prevX, prevY, prevZ = bx, by, bz
			continue
		}

		// The ray entered (bx,by,bz) from (prevX,prevY,prevZ). The crossed face is the axis whose cell
		// changed on this step; the Direction is the face the ray entered THROUGH (Direction pointing
		// back toward the ray origin). Map the changed axis to a Direction 3D-data value (0 DOWN,1 UP,
		// 2 NORTH,3 SOUTH,4 WEST,5 EAST): the face normal points opposite the ray's travel on that axis.
		face := 1 // default UP (degenerate: ray started already inside the cell)
		switch {
		case bx != prevX:
			if bx > prevX {
				face = 4 // ray moving +X entered through the WEST face
			} else {
				face = 5 // ray moving -X entered through the EAST face
			}
		case by != prevY:
			if by > prevY {
				face = 0 // ray moving +Y entered through the DOWN face
			} else {
				face = 1 // ray moving -Y entered through the UP face
			}
		case bz != prevZ:
			if bz > prevZ {
				face = 2 // ray moving +Z entered through the NORTH face
			} else {
				face = 3 // ray moving -Z entered through the SOUTH face
			}
		}
		return pk.Position{X: bx, Y: by, Z: bz}, face, true
	}
	return pk.Position{}, 0, false
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
		inv.incrementStateId()
		p.client.Send(containerSetSlot(playerContainerID, inv.stateID, slot, result))
	}
}
