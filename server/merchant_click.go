package server

// merchant_click.go — the ContainerClick engine for an OPEN merchant window (VILLAGER-MENU), plus the
// TRADE-TAKE chain (MerchantResultSlot.onTake -> MerchantOffer.take + AbstractVillager.notifyTrade +
// Villager.rewardTradeXp). A sibling of chest_click.go / crafting_click.go over the MerchantMenu slot
// layout (payment 0/1, result 2, player main 3..29, hotbar 30..38). The result slot (2) is take-only
// (MerchantResultSlot.mayPlace == false); a take fires the trade. Supported ContainerInputs: PICKUP,
// QUICK_MOVE, THROW — what a merchant menu actually receives.
//
// 1:1 jar ports (temp/cache/26.2-inner.jar, javap -c -p / CFR this session, cited per function):
//   onTakeMerchant : net.minecraft.world.inventory.MerchantResultSlot.onTake
//   quickMove      : net.minecraft.world.inventory.MerchantMenu.quickMoveStack
//   notifyTrade    : net.minecraft.world.entity.npc.villager.AbstractVillager.notifyTrade
//   rewardTradeXp  : net.minecraft.world.entity.npc.villager.Villager.rewardTradeXp
//
// Tick-owned (TICK-05): every method runs on the tick goroutine, so the payment/result mutation is
// race-clean. The villager is re-resolved through its owning region on each click (never cur()).

import (
	"github.com/imhinotori/sulfur/level/component"
)

// merchantSlotRef resolves a merchant-WINDOW slot index (0..38) to its backing + local index:
//
//	0      -> payment slot 0 (oc.mpay0)
//	1      -> payment slot 1 (oc.mpay1)
//	2      -> the result slot (oc.mresult, take-only)
//	3..38  -> the player inventory window (main 9..35, hotbar 36..44)
//
// An out-of-range index returns ok=false. This is the MerchantMenu slot->container mapping.
type merchantSlotRef struct {
	oc      *openContainer
	inv     *Inventory
	payment int   // 0 or 1 for a payment slot, -1 otherwise
	result  bool  // the result slot (2)
	invSlot int16 // player inventory window slot, -1 otherwise
	ok      bool
}

func merchantResolveSlot(oc *openContainer, inv *Inventory, menuIdx int) merchantSlotRef {
	switch {
	case menuIdx == 0:
		return merchantSlotRef{oc: oc, inv: inv, payment: 0, invSlot: -1, ok: true}
	case menuIdx == 1:
		return merchantSlotRef{oc: oc, inv: inv, payment: 1, invSlot: -1, ok: true}
	case menuIdx == 2:
		return merchantSlotRef{oc: oc, inv: inv, payment: -1, result: true, invSlot: -1, ok: true}
	case menuIdx >= merchantMenuMainStart && menuIdx < merchantMenuMainStart+27:
		// main: menu 3..29 -> window slots 9..35
		return merchantSlotRef{oc: oc, inv: inv, payment: -1, invSlot: int16(windowMainFirst + (menuIdx - merchantMenuMainStart)), ok: true}
	case menuIdx >= merchantMenuHotbarStart && menuIdx < merchantMenuSize:
		// hotbar: menu 30..38 -> window slots 36..44
		return merchantSlotRef{oc: oc, inv: inv, payment: -1, invSlot: int16(windowHotbarFirst + (menuIdx - merchantMenuHotbarStart)), ok: true}
	}
	return merchantSlotRef{payment: -1}
}

func (r merchantSlotRef) get() component.SlotData {
	switch {
	case r.result:
		return r.oc.mresult
	case r.payment == 0:
		return r.oc.mpay0
	case r.payment == 1:
		return r.oc.mpay1
	default:
		return r.inv.get(r.invSlot)
	}
}

// set writes the slot backing. The RESULT slot is take-only: a set into it is ignored (the result is
// owned by updateSellItem, never client-set).
func (r merchantSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	switch {
	case r.result:
		return // result is take-only (mayPlace == false)
	case r.payment == 0:
		r.oc.mpay0 = s
	case r.payment == 1:
		r.oc.mpay1 = s
	default:
		r.inv.set(r.invSlot, s)
	}
}

// clickedMerchant ports AbstractContainerMenu.clicked for an open merchant window: snapshot the payments +
// result + player + cursor, run the merchant doClick under a panic-recover (the vanilla clicked() try block
// — no partial mutation leaks on a throw, T-6-04), recompute the result via updateSellItem, then re-send the
// authoritative content + sync the carried cursor. The whole window is re-sent (client snaps to
// authoritative), like the chest/crafting engines.
func (t *TickLoop) clickedMerchant(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	villager, offers := t.merchantOffersForWindow(oc)
	if villager == nil {
		t.sendMerchantContent(p) // villager gone: resend authoritative content, no mutation
		return
	}

	pay0Before, pay1Before, resultBefore := oc.mpay0, oc.mpay1, oc.mresult
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				oc.mpay0, oc.mpay1, oc.mresult = pay0Before, pay1Before, resultBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doMerchantClick(p, oc, inv, villager, offers, int(slotNum), button, int(input))
	}()

	// slotsChanged: recompute the result from the (post-click) payments (MerchantMenu.slotsChanged ->
	// tradeContainer.updateSellItem). A take already re-ran this inside onTakeMerchant, but a payment-only
	// change (place/move) needs it here. Idempotent.
	t.merchantUpdateSellItem(oc, offers)

	// broadcastChanges: re-send the full authoritative merchant window so the client reflects the move.
	t.sendMerchantContent(p)

	// synchronizeCarriedToRemote: sync the cursor on change (ClientboundContainerSetSlot(-1, ...)).
	if !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(containerSetSlot(-1, inv.stateID, -1, inv.getCarried()))
	}
}

// doMerchantClick ports the AbstractContainerMenu.doClick branches over the merchant window. PICKUP,
// QUICK_MOVE, THROW are supported; an unsupported/forged input is a no-op (the authoritative content is
// re-sent regardless). The result slot (2) is take-only — a place INTO it is rejected, and a take fires
// onTakeMerchant.
func (t *TickLoop) doMerchantClick(p *tickPlayer, oc *openContainer, inv *Inventory, villager *Entity, offers merchantOffers, i, j, input int) {
	switch input {
	case containerInputPickup, containerInputQuickMove:
		if inv.quickcraftStatus != 0 {
			inv.resetQuickCraft()
			return
		}
		if input == containerInputPickup {
			t.merchantPickup(p, oc, inv, villager, offers, i, j)
		} else {
			t.merchantQuickMove(p, oc, inv, villager, offers, i)
		}
	case containerInputThrow:
		t.merchantThrow(p, oc, inv, villager, offers, i, j)
	}
}

// merchantPickup ports the PICKUP branch over the merchant window: left-click (j==0) takes/puts the whole
// stack, right-click (j==1) takes half / puts one. The RESULT slot (2) is take-only: a pickup of it takes
// the whole result onto the cursor (or merges) and fires onTakeMerchant (the trade); you cannot put INTO it.
// The payment + player slots behave like chest cells.
func (t *TickLoop) merchantPickup(p *tickPlayer, oc *openContainer, inv *Inventory, villager *Entity, offers merchantOffers, i, j int) {
	if j != 0 && j != 1 {
		return
	}
	if i < 0 {
		return
	}
	ref := merchantResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()

	// The RESULT slot: take-only. MerchantResultSlot.mayPlace == false, so you can only TAKE.
	if ref.result {
		res := oc.mresult
		if stackEmpty(res) {
			return // nothing to take
		}
		// Take onto the cursor: empty cursor -> take whole result; same-item cursor -> merge if room;
		// different/full item -> no-op (you cannot swap into a take-only slot).
		if stackEmpty(carried) {
			inv.setCarried(res)
		} else if stackSameItemSameComponents(res, carried) && int(carried.Count)+int(res.Count) <= stackMaxSize(carried) {
			c := carried
			c.Count += res.Count
			inv.setCarried(c)
		} else {
			return
		}
		// onTake: run the trade (shrink payments, notifyTrade, XP) + recompute the result.
		t.onTakeMerchant(p, oc, inv, villager, offers)
		return
	}

	primary := j == 0
	slotItem := ref.get()

	if stackEmpty(slotItem) {
		if !stackEmpty(carried) {
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			ref.set(merchantSafeInsert(ref, &c, place))
			inv.setCarried(c)
		}
		return
	}
	if stackEmpty(carried) {
		take := (int(slotItem.Count) + 1) / 2
		if primary {
			take = int(slotItem.Count)
		}
		rem := slotItem
		taken := stackSplit(&rem, take)
		ref.set(rem)
		inv.setCarried(taken)
		return
	}
	if stackSameItemSameComponents(slotItem, carried) {
		add := 1
		if primary {
			add = int(carried.Count)
		}
		c := carried
		ref.set(merchantSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if int(carried.Count) <= stackMaxSize(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// merchantSafeInsert ports Slot.safeInsert over a merchantSlotRef (payment/player cells; the result is never
// an insert target — merchantPickup guards it): place up to min(increment, stack.count, slotMax-existing) of
// *stack, shrink *stack, return the slot's new contents. Mirrors craftSafeInsert.
func merchantSafeInsert(ref merchantSlotRef, stack *component.SlotData, increment int) component.SlotData {
	existing := ref.get()
	if stackEmpty(*stack) {
		return existing
	}
	add := min(increment, int(stack.Count))
	if room := stackMaxSize(*stack) - int(existing.Count); room < add {
		add = room
	}
	if add <= 0 {
		return existing
	}
	if stackEmpty(existing) {
		return stackSplit(stack, add)
	}
	if stackSameItemSameComponents(existing, *stack) {
		stack.Count = toVar(int(stack.Count) - add)
		if stack.Count <= 0 {
			stack.Count = 0
		}
		existing.Count = toVar(int(existing.Count) + add)
		return existing
	}
	return existing
}

// merchantQuickMove ports MerchantMenu.quickMoveStack: shift-click moves a stack. Ranges (jar, verified):
//
//	slotIndex == 2 (result): moveItemStackTo(stack, 3, 39, true); onQuickCraft; playTradeSound
//	slotIndex == 0|1 (payment): moveItemStackTo(stack, 3, 39, false)
//	slotIndex 3..29 (main):     moveItemStackTo(stack, 30, 39, false)  (main -> hotbar)
//	slotIndex 30..38 (hotbar):  moveItemStackTo(stack, 3, 30, false)   (hotbar -> main)
//
// The menu ranges map to player inv WINDOW slots: menu [3,39) == window [9,45); menu [30,39) == window
// [36,45); menu [3,30) == window [9,36). For the RESULT shift, the take FIRST deposits the whole result into
// the inventory, THEN fires the trade (MerchantResultSlot.onTake via slot.onTake). CITE
// MerchantMenu.quickMoveStack.
func (t *TickLoop) merchantQuickMove(p *tickPlayer, oc *openContainer, inv *Inventory, villager *Entity, offers merchantOffers, i int) {
	if i < 0 {
		return
	}
	ref := merchantResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}

	if ref.result {
		res := oc.mresult
		if stackEmpty(res) {
			return
		}
		work := res
		// moveItemStackTo(stack, 3, 39, true) == window [9,45) reverse.
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, true) {
			return // no room: nothing traded
		}
		// The result moved (fully or partly); fire the trade once (one take). MerchantResultSlot.onTake.
		t.onTakeMerchant(p, oc, inv, villager, offers)
		return
	}

	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src
	switch {
	case ref.payment == 0 || ref.payment == 1:
		// payment -> player inventory: moveItemStackTo(stack, 3, 39, false) == window [9,45).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, false) {
			return
		}
	case ref.invSlot >= int16(windowMainFirst) && ref.invSlot < int16(windowMainFirst+27):
		// main (window 9..35) -> hotbar: moveItemStackTo(stack, 30, 39, false) == window [36,45).
		if !t.moveItemStackTo(inv, &work, windowHotbarFirst, offhandWindowSlot, false) {
			return
		}
	default:
		// hotbar (window 36..44) -> main: moveItemStackTo(stack, 3, 30, false) == window [9,36).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, windowHotbarFirst, false) {
			return
		}
	}
	ref.set(work)
	// A payment move changes the sell item; updateSellItem is re-run by clickedMerchant after this returns.
}

// merchantThrow ports the THROW branch over a merchant window: with an empty cursor, Q drops 1 (j==0) or the
// whole stack (j==1) from the slot under the cursor. The result slot drops the whole result + fires the
// trade (a Q on the result trades-and-drops).
func (t *TickLoop) merchantThrow(p *tickPlayer, oc *openContainer, inv *Inventory, villager *Entity, offers merchantOffers, i, j int) {
	if !stackEmpty(inv.getCarried()) {
		return
	}
	if i < 0 {
		return
	}
	ref := merchantResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
		return
	}
	if ref.result {
		// Q on the result: drop the whole result, then trade.
		t.playerDrop(p, cur, true)
		t.onTakeMerchant(p, oc, inv, villager, offers)
		return
	}
	amt := 1
	if j != 0 {
		amt = int(cur.Count)
	}
	taken := stackCopyWithCount(cur, amt)
	cur.Count = toVar(int(cur.Count) - amt)
	if cur.Count <= 0 {
		cur = component.SlotData{Count: 0}
	}
	ref.set(cur)
	t.playerDrop(p, taken, true)
}

// onTakeMerchant ports net.minecraft.world.inventory.MerchantResultSlot.onTake: when the player TAKES the
// result, run the active offer's take (shrink the payment slots by the offer's costs), notifyTrade the
// villager (uses++ + rewardTradeXp), award the trade stat (v1 no-op), write the shrunk payments back, and
// accumulate the villager XP. Then recompute the result (updateSellItem) so a still-satisfied payment
// repopulates the result (chained trading).
//
// The jar body (verified):
//
//	checkTakeAchievements(carried);   // onCraftedBy — v1 no-op (no crafted-by stat)
//	offer = slots.getActiveOffer();
//	if (offer != null) {
//	    buyA = getItem(0); buyB = getItem(1);
//	    if (offer.take(buyA, buyB) || offer.take(buyB, buyA)) {
//	        merchant.notifyTrade(offer);          // increaseUses + rewardTradeXp + TRADE criterion
//	        player.awardStat(TRADED_WITH_VILLAGER);
//	        setItem(0, buyA); setItem(1, buyB);
//	    }
//	    merchant.overrideXp(getVillagerXp() + offer.getXp());   // Villager.overrideXp is a NO-OP (see below)
//	}
//
//	[VERIFIED CFR MerchantResultSlot.onTake + AbstractVillager.overrideXp (empty body — the merchant xp
//	 override does nothing for a Villager; the real xp accrual is inside rewardTradeXp).]
func (t *TickLoop) onTakeMerchant(p *tickPlayer, oc *openContainer, inv *Inventory, villager *Entity, offers merchantOffers) {
	// checkTakeAchievements: onCraftedBy(player, removeCount) — v1 no-op (no crafted-by stat subsystem).

	if oc.mactiveOffer < 0 || oc.mactiveOffer >= len(offers) {
		return // no active offer (getActiveOffer() == null): nothing to trade
	}
	offer := offers[oc.mactiveOffer]

	buyA := oc.mpay0
	buyB := oc.mpay1
	// offer.take(buyA, buyB) || offer.take(buyB, buyA): try both payment orders (mirrors the jar).
	if offer.take(&buyA, &buyB) || offer.take(&buyB, &buyA) {
		t.notifyTrade(villager, offer) // increaseUses + rewardTradeXp
		// player.awardStat(TRADED_WITH_VILLAGER): v1 no-op (no stats subsystem).
		oc.mpay0 = buyA
		oc.mpay1 = buyB
	}
	// merchant.overrideXp(getVillagerXp() + offer.getXp()): AbstractVillager.overrideXp is an EMPTY body for
	// a Villager (the xp accrual happens inside rewardTradeXp via villagerXp +=), so this call is a faithful
	// no-op — NOT a second xp add. Preserved as a documented no-op. CITE AbstractVillager.overrideXp.

	// The chained re-match: after consuming, recompute the result so a still-satisfied payment repopulates
	// (MerchantMenu.slotsChanged after a take). clickedMerchant also re-runs updateSellItem, but do it here
	// too so the quick-move/throw take paths leave a correct result immediately.
	t.merchantUpdateSellItem(oc, offers)
}

// notifyTrade ports net.minecraft.world.entity.npc.villager.AbstractVillager.notifyTrade(offer):
//
//	offer.increaseUses();
//	this.ambientSoundTime = -getAmbientSoundInterval();   // v1: no ambient-sound timer -> cited no-op
//	this.rewardTradeXp(offer);                            // Villager.rewardTradeXp (XP orb + villagerXp)
//	if (tradingPlayer instanceof ServerPlayer) CriteriaTriggers.TRADE.trigger(...);  // v1 no-op (no criteria)
//
//	[VERIFIED CFR AbstractVillager.notifyTrade + Villager.rewardTradeXp.]
func (t *TickLoop) notifyTrade(villager *Entity, offer *merchantOffer) {
	offer.increaseUses()
	// ambientSoundTime reset: no ambient-sound timer field on *Entity -> cited no-op.
	t.rewardTradeXp(villager, offer)
	// CriteriaTriggers.TRADE.trigger: no criteria subsystem in v1 -> cited no-op.
}

// rewardTradeXp ports net.minecraft.world.entity.npc.villager.Villager.rewardTradeXp(offer):
//
//	int popXp = 3 + this.random.nextInt(4);
//	this.villagerXp += offer.getXp();
//	this.lastTradedPlayer = getTradingPlayer();               // v1: no lastTradedPlayer field -> cited no-op
//	if (shouldIncreaseLevel()) { updateMerchantTimer=40; increaseProfessionLevelOnUpdate=true; popXp += 5; }
//	if (offer.shouldRewardExp()) level.addFreshEntity(new ExperienceOrb(level, x, y+0.5, z, popXp));
//
// The `3 + random.nextInt(4)` draw is on the VILLAGER's OWN per-entity RandomSource (mobRandom(e) ==
// e.ai.rng), NOT the process pool — type-gated off the pig oracle stream (a villager is not a pig, and the
// oracle pig is never traded with), so TestPluginPigEqualsGoNativePig is unperturbed. The level-up gate
// (shouldIncreaseLevel -> the +5 popXp bonus + profession-level scheduling) is DEFERRED (no
// updateMerchantTimer/level-up tick wired yet) — a cited stub returning false, so popXp stays 3+nextInt(4)
// and villagerXp still accumulates in the field (never baked away). The XP orb is spawned via
// awardExperienceOrbs (the established ExperienceOrb.award path) when offer.shouldRewardExp().
//
//	[VERIFIED CFR Villager.rewardTradeXp: popXp = 3 + random.nextInt(4); villagerXp += offer.getXp();
//	 shouldIncreaseLevel -> popXp += 5; shouldRewardExp -> addFreshEntity(ExperienceOrb(...popXp)).]
func (t *TickLoop) rewardTradeXp(villager *Entity, offer *merchantOffer) {
	popXp := 3 + mobRandom(villager).nextInt(4)
	villager.villagerXp += offer.xp
	// lastTradedPlayer = getTradingPlayer(): no field on *Entity -> cited no-op.
	if villagerShouldIncreaseLevel(villager) {
		// updateMerchantTimer=40 + increaseProfessionLevelOnUpdate=true: the level-up scheduling is DEFERRED
		// (no merchant-update tick). The +5 popXp bonus is preserved for when the gate is wired.
		popXp += 5
	}
	if offer.rewardExp { // shouldRewardExp()
		t.awardExperienceOrbs(villager, popXp)
	}
}

// villagerShouldIncreaseLevel is the DEFERRED stub for Villager.shouldIncreaseLevel(): vanilla checks
// `VillagerData.canLevelUp(level) && villagerXp >= getMaxXpPerLevel(level)`. The level-up TICK
// (updateMerchantTimer -> increaseMerchantCareer) is not wired in v1, so this returns false (no level-up) —
// a cited stub equal to the "cannot yet level" default, structured so a real canLevelUp + xp-threshold read
// replaces it with no caller change (CLAUDE.md: never bake the value away). CITE Villager.shouldIncreaseLevel.
func villagerShouldIncreaseLevel(_ *Entity) bool { return false }
