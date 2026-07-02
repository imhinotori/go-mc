package server

// merchant_menu.go — the VILLAGER MERCHANT MENU (VILLAGER-MENU): right-click a villager with offers ->
// startTrading -> player.openMenu(MerchantMenu) -> ClientboundOpenScreen(minecraft:merchant) +
// ClientboundMerchantOffers -> the click/take engine (merchant_click.go) -> close (return the payment
// slots to the player). A sibling of the chest/crafting/stonecutter menu subsystems, over the 39-slot
// MerchantMenu layout.
//
// 1:1 jar chain (temp/cache/26.2-inner.jar, javap -c -p / CFR this session, cited per function):
//
//	Villager.mobInteract: !spawn_egg && isAlive && !isTrading && !isSleeping:
//	    isBaby -> setUnhappy + SUCCESS
//	    server: noOffers = getOffers().isEmpty(); MAIN_HAND -> (noOffers ? setUnhappy) + awardStat(TALKED_TO)
//	            noOffers -> CONSUME ; else startTrading(player)  -> SUCCESS
//	Villager.startTrading: updateSpecialPrices(player); setTradingPlayer(player);
//	    openTradingScreen(player, getDisplayName(), getVillagerData().level())
//	Merchant.openTradingScreen (default): id = player.openMenu(new SimpleMenuProvider(MerchantMenu, title));
//	    if (id.present && !offers.isEmpty()) player.sendMerchantOffers(id, offers, level, getVillagerXp(),
//	                                                                   showProgressBar(), canRestock())
//	MerchantMenu(id, inv, merchant): addSlot(pay0@0), addSlot(pay1@1), MerchantResultSlot(@2),
//	    addStandardInventorySlots(inv) -> menu slots 3..29 (main) + 30..38 (hotbar) = 39 slots total.
//
// v1 subset (cited): no stats/criteria (awardStat/CriteriaTriggers are faithful no-ops); the
// updateSpecialPrices gossip/hero-of-village price economy is DEFERRED (reputation == 0 stub + no
// hero-of-the-village effect => both loops are dead, so updateSpecialPrices is a no-op — the current v1
// default; cite Villager.updateSpecialPrices / MerchantOffer.updateDemand as the future adjuster). The
// ContainerLevelAccess/stillValid reach re-check is folded into the interact-time same-region gate.

import (
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// merchantMenuSize is the MerchantMenu slot count: 2 payment + 1 result + 27 main + 9 hotbar = 39.
// (MerchantMenu: PAYMENT1=0, PAYMENT2=1, RESULT=2, INV 3..29, USE_ROW 30..38.) Verified MerchantMenu ctor.
const merchantMenuSize = 3 + 27 + 9 // 39

// merchantTitle is the merchant window's display name. Vanilla MerchantMenu's provider title is the
// villager's getDisplayName() (the entity's custom/type name). v1 sends a plain literal (no client-side
// i18n / custom-name plumbing here yet); structured to become the villager's real display name when the
// entity name layer grows one. CITE Merchant.openTradingScreen(player, getDisplayName(), level).
const merchantTitle = "Villager"

// merchantMenuMainStart / merchantMenuHotbarStart are the MENU slot indices where the standard inventory
// begins (addStandardInventorySlots after the 3 trade slots): main 3..29, hotbar 30..38. Verified
// MerchantMenu constants INV_SLOT_START=3, INV_SLOT_END=30, USE_ROW_SLOT_START=30, USE_ROW_SLOT_END=39.
const (
	merchantMenuMainStart   = 3  // menu slot 3 -> player inv window slot 9
	merchantMenuHotbarStart = 30 // menu slot 30 -> player inv window slot 36
)

// clientboundMerchantOffers builds ClientboundMerchantOffers. Jar-derived write
// (ClientboundMerchantOffersPacket.write, CFR'd this session):
//
//	VarInt      containerId   (writeContainerId — a VarInt alias)
//	List        offers        (MerchantOffers.STREAM_CODEC = MerchantOffer.STREAM_CODEC over
//	                           ByteBufCodecs.collection: a VarInt count prefix + N × MerchantOffer)
//	VarInt      villagerLevel (writeVarInt)
//	VarInt      villagerXp    (writeVarInt)
//	Boolean     showProgress  (writeBoolean)
//	Boolean     canRestock    (writeBoolean)
//
// Each MerchantOffer (MerchantOffer.writeToStream, verified) is, in order:
//
//	ItemCost    baseCostA     (ItemCost.STREAM_CODEC)
//	ItemStack   result        (ItemStack.STREAM_CODEC — non-empty component-slot stack)
//	Optional<ItemCost> costB  (ItemCost.OPTIONAL_STREAM_CODEC — Boolean present + ItemCost)
//	Boolean     outOfStock    (uses >= maxUses)
//	Int         uses          (writeInt — a FIXED 4-byte int, NOT a VarInt)
//	Int         maxUses       (writeInt)
//	Int         xp            (writeInt)
//	Int         specialPriceDiff (writeInt)
//	Float       priceMultiplier  (writeFloat)
//	Int         demand        (writeInt)
//
// LOAD-BEARING: uses/maxUses/xp/specialPriceDiff/demand are FIXED 4-byte ints (writeInt), priceMultiplier
// a 4-byte float (writeFloat) — NOT VarInts. The wire sends the BASE costs (baseCostA/costB) directly; the
// client recomputes the demand-modified display count from specialPriceDiff/demand/priceMultiplier
// (MerchantOffer.getModifiedCostCount). v1 emits the stored numbers (default specialPriceDiff/demand 0).
func clientboundMerchantOffers(containerID int32, offers merchantOffers, villagerLevel, villagerXp int, showProgress, canRestock bool) pk.Packet {
	fields := make([]pk.FieldEncoder, 0, 2+len(offers)*10+4)
	fields = append(fields,
		pk.VarInt(containerID),
		pk.VarInt(int32(len(offers))), // MerchantOffers collection count prefix
	)
	for _, o := range offers {
		fields = append(fields, merchantOfferFields(o)...)
	}
	fields = append(fields,
		pk.VarInt(int32(villagerLevel)),
		pk.VarInt(int32(villagerXp)),
		pk.Boolean(showProgress),
		pk.Boolean(canRestock),
	)
	return pk.Marshal(int32(packetid.ClientboundMerchantOffers), fields...)
}

// merchantOfferFields encodes ONE MerchantOffer per MerchantOffer.writeToStream (verified). The result is
// built with mustEncodeStack (a stack must be non-empty for the ItemStack STREAM_CODEC). costB is an
// Optional<ItemCost> (Boolean present + the ItemCost when non-nil).
func merchantOfferFields(o *merchantOffer) []pk.FieldEncoder {
	fields := make([]pk.FieldEncoder, 0, 12)
	// baseCostA: ItemCost.STREAM_CODEC.
	fields = append(fields, itemCostFields(o.baseCostA)...)
	// result: ItemStack.STREAM_CODEC (the assembled sell stack — always non-empty for a real offer).
	res := offerResultStack(o)
	fields = append(fields, encodableSlot(res))
	// costB: ItemCost.OPTIONAL_STREAM_CODEC = Boolean present + (present ? ItemCost).
	if o.costB != nil {
		fields = append(fields, pk.Boolean(true))
		fields = append(fields, itemCostFields(*o.costB)...)
	} else {
		fields = append(fields, pk.Boolean(false))
	}
	// outOfStock (uses >= maxUses); uses; maxUses; xp; specialPriceDiff; priceMultiplier; demand.
	fields = append(fields,
		pk.Boolean(o.uses >= o.maxUses),
		pk.Int(int32(o.uses)),
		pk.Int(int32(o.maxUses)),
		pk.Int(int32(o.xp)),
		pk.Int(int32(o.specialPriceDiff)),
		pk.Float(o.priceMultiplier),
		pk.Int(int32(o.demand)),
	)
	return fields
}

// itemCostFields encodes an ItemCost per ItemCost.STREAM_CODEC (verified):
//
//	Item        item        (Item.STREAM_CODEC = holderRegistry(ITEM) => a VarInt registry id — the raw
//	                         numeric item id, exactly as SlotData.ItemID is written elsewhere)
//	VarInt      count       (ByteBufCodecs.VAR_INT)
//	DataComponentExactPredicate components (STREAM_CODEC = list() => a VarInt count; EMPTY == a single 0)
//
// v1 offers carry no component predicate (the farmer trades are plain item costs), so the predicate is the
// EMPTY list -> a single VarInt 0. CITE ItemCost.STREAM_CODEC + DataComponentExactPredicate.STREAM_CODEC.
func itemCostFields(c itemCost) []pk.FieldEncoder {
	return []pk.FieldEncoder{
		pk.VarInt(int32(c.item.ID)), // holderRegistry(ITEM): the raw item registry id
		pk.VarInt(int32(c.count)),   // ByteBufCodecs.VAR_INT count
		pk.VarInt(0),                // DataComponentExactPredicate.EMPTY: an empty component list (count 0)
	}
}

// offerResultStack builds the SlotData for an offer's result ItemStack (MerchantOffer.assemble() ->
// result.copy()). A component-free (id,count) stack — the wire form ItemStack.STREAM_CODEC produces for a
// plain stack (count, id, 0 added, 0 removed).
func offerResultStack(o *merchantOffer) component.SlotData {
	return component.SlotData{Count: pk.VarInt(o.result.count), ItemID: pk.VarInt(o.result.item.ID)}
}

// encodableSlot returns a *SlotData whose WriteTo emits the ItemStack. Wrapping is needed because
// component.SlotData's encoder is pointer-receiver; a fresh pointer per call keeps the value stable for
// the deferred Marshal.
func encodableSlot(s component.SlotData) pk.FieldEncoder {
	sd := s
	return &sd
}

// openMerchantMenu ports Merchant.openTradingScreen -> ServerPlayer.openMenu(MerchantMenu) +
// sendMerchantOffers for a trading villager: close any prior window, allocate a windowId, send
// ClientboundOpenScreen(minecraft:merchant) + ClientboundMerchantOffers, and record the transient merchant
// window state (empty payment slots, no active offer). Returns true (the interact was consumed — a villager
// with offers always opens). The offers list is the villager's LIVE getOffers() (shared across re-opens, so
// uses depletion persists). villagerLevel/villagerXp come from the villager; showProgress=true
// (AbstractVillager.showProgressBar), canRestock=true (Villager.canRestock).
//
//	[VERIFIED CFR Merchant.openTradingScreen + ClientboundMerchantOffersPacket + AbstractVillager
//	 .showProgressBar (return true) + Villager.canRestock (return true).]
func (t *TickLoop) openMerchantMenu(p *tickPlayer, villager *Entity) bool {
	if p == nil || p.client == nil || villager == nil {
		return false
	}
	offers := villagerGetOffers(villager)
	if offers.isEmpty() {
		return false // Villager.mobInteract's noOffers branch returned CONSUME before startTrading (guarded by caller)
	}

	// Villager.startTrading: updateSpecialPrices(player) then setTradingPlayer(player). updateSpecialPrices
	// is a deferred no-op (reputation 0 + no hero-of-the-village effect => both price loops are dead), so
	// only setTradingPlayer runs. CITE Villager.updateSpecialPrices as the future price adjuster.
	villagerSetTradingPlayer(villager, p.entityID)

	// ServerPlayer.openMenu: close any previously-open non-inventory window first. v1 tracks one window at
	// a time; a stale window's cleanup is handled by its own close (the merchant close returns payments).
	if p.openContainer != nil {
		p.openContainer = nil
	}

	win := p.nextContainerCounter()
	p.openContainer = &openContainer{
		windowID:           win,
		kind:               containerKindMerchant,
		merchantVillagerID: villager.id,
		mselectionHint:     0,
		mactiveOffer:       -1, // no active offer until a payment matches
	}

	menuID := menuTypeID(registryid.Menu, "minecraft:merchant")
	p.client.Send(openScreen(int32(win), menuID, merchantTitle))

	// sendMerchantOffers: the offers + level + xp + progress/restock flags. Vanilla sends this only when
	// openMenu returned a containerId AND offers non-empty (both hold here).
	p.client.Send(clientboundMerchantOffers(int32(win), offers, villager.villagerLevel, villager.villagerXp, true, true))

	// initMenu -> broadcastChanges: push the full 39-slot content (payments 0/1 empty, result 2 empty, the
	// player inventory 3..38). The merchant-menu state id is the player inventory's state counter.
	t.sendMerchantContent(p)
	return true
}

// merchantMenuItems builds the 39-slot ContainerSetContent list for the merchant window: payment0 (0),
// payment1 (1), result (2), then the player inventory — main 3..29 (window slots 9..35), hotbar 30..38
// (window slots 36..44). Mirrors MerchantMenu's addSlot(0)+addSlot(1)+MerchantResultSlot(2)+
// addStandardInventorySlots order. The slot ORDER is the wire contract the client renders + the
// ContainerClick slot index maps back through (merchantResolveSlot).
func merchantMenuItems(oc *openContainer, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, merchantMenuSize)
	out[0] = oc.mpay0
	out[1] = oc.mpay1
	out[2] = oc.mresult
	// main: menu 3..29 <- inventory window slots 9..35
	for i := 0; i < 27; i++ {
		out[merchantMenuMainStart+i] = inv.get(int16(windowMainFirst + i))
	}
	// hotbar: menu 30..38 <- inventory window slots 36..44
	for i := 0; i < 9; i++ {
		out[merchantMenuHotbarStart+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendMerchantContent pushes the authoritative ContainerSetContent for the open merchant window (the
// initMenu -> broadcastChanges full-slot sync, and the resend after any merchant click). Bumps the player
// inventory's state id so the client tracks the authoritative state, exactly like sendChestContent.
func (t *TickLoop) sendMerchantContent(p *tickPlayer) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindMerchant {
		return
	}
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		merchantMenuItems(p.openContainer, inv), inv.getCarried()))
}

// merchantOffersForWindow resolves the LIVE offers of the villager backing an open merchant window, through
// its owning region (re-resolve by id; nil if the villager despawned/forged). NEVER cur() (the click path
// has no region registered). Returns nil when the villager is gone.
func (t *TickLoop) merchantOffersForWindow(oc *openContainer) (*Entity, merchantOffers) {
	if oc == nil || oc.merchantVillagerID == 0 {
		return nil, nil
	}
	owner := t.owningRegion(oc.merchantVillagerID)
	if owner == nil {
		return nil, nil
	}
	v, ok := owner.entities.get(oc.merchantVillagerID)
	if !ok {
		return nil, nil
	}
	return v, villagerGetOffers(v)
}

// merchantUpdateSellItem ports net.minecraft.world.inventory.MerchantContainer.updateSellItem: recompute the
// RESULT slot (2) + the active offer from the two payment inputs. Mirrors the jar exactly:
//
//	activeOffer = null
//	if (item[0].isEmpty()) { buyA = item[1]; buyB = EMPTY } else { buyA = item[0]; buyB = item[1] }
//	if (buyA.isEmpty()) { setItem(2, EMPTY); futureXp = 0; return }
//	offers = merchant.getOffers()
//	if (!offers.isEmpty()) {
//	    offer = offers.getRecipeFor(buyA, buyB, selectionHint)
//	    if (offer == null || offer.isOutOfStock()) { activeOffer = offer; offer = getRecipeFor(buyB, buyA, hint) }
//	    if (offer != null && !offer.isOutOfStock()) { activeOffer = offer; setItem(2, assemble()); futureXp = xp }
//	    else { setItem(2, EMPTY); futureXp = 0 }
//	}
//	merchant.notifyTradeUpdated(getItem(2))   // v1: a cited no-op (trade sound), see below
//
// v1 stores mactiveOffer as the offer's LIST INDEX (not a pointer) so the take path re-resolves the live
// offer through the same list. futureXp (the progress-bar preview) is a cite-deferred display value (no
// ClientboundContainerSetData wire for the merchant progress yet); notifyTradeUpdated's trade-updated SOUND
// is a faithful no-op (no sound subsystem here). CITE MerchantContainer.updateSellItem.
func (t *TickLoop) merchantUpdateSellItem(oc *openContainer, offers merchantOffers) {
	oc.mactiveOffer = -1

	var buyA, buyB component.SlotData
	if stackEmpty(oc.mpay0) {
		buyA = oc.mpay1
		buyB = component.SlotData{Count: 0}
	} else {
		buyA = oc.mpay0
		buyB = oc.mpay1
	}
	if stackEmpty(buyA) {
		oc.mresult = component.SlotData{Count: 0}
		return
	}
	if offers.isEmpty() {
		oc.mresult = component.SlotData{Count: 0}
		return
	}

	idx := offers.getRecipeFor(buyA, buyB, oc.mselectionHint)
	if idx < 0 || offers[idx].isOutOfStock() {
		// activeOffer = offer (even if out of stock — vanilla records it); then try the swapped order.
		oc.mactiveOffer = idx
		idx = offers.getRecipeFor(buyB, buyA, oc.mselectionHint)
	}
	if idx >= 0 && !offers[idx].isOutOfStock() {
		oc.mactiveOffer = idx
		oc.mresult = offerResultStack(offers[idx]) // assemble()
	} else {
		oc.mresult = component.SlotData{Count: 0}
	}
}

// handleSelectTrade resolves a ServerboundSelectTrade on-tick (VILLAGER-MENU). Ports
// ServerGamePacketListenerImpl.handleSelectTrade: if the open window is a MerchantMenu and stillValid,
// setSelectionHint(selection) + tryMoveItems(selection). v1 realizes setSelectionHint (store the hint +
// re-run updateSellItem) and the tryMoveItems payment auto-fill (move the picked offer's cost items from
// the player inventory into the payment slots). A forged/stale window or out-of-range index is a silent
// no-op. Decoded defensively (a short payload no-ops).
//
//	[VERIFIED CFR handleSelectTrade: menu.setSelectionHint(selection); menu.tryMoveItems(selection).]
func (t *TickLoop) handleSelectTrade(p *tickPlayer, pkt pk.Packet) {
	if p == nil || p.client == nil {
		return
	}
	var selection pk.VarInt
	if err := pkt.Scan(&selection); err != nil {
		return // short payload: no-op
	}
	oc := p.openContainer
	if oc == nil || oc.kind != containerKindMerchant {
		return
	}
	villager, offers := t.merchantOffersForWindow(oc)
	if villager == nil {
		return
	}
	// stillValid: the villager's tradingPlayer must be this player (MerchantContainer.stillValid).
	if villager.villagerTradingPlayer != p.entityID {
		return
	}

	// setSelectionHint(selection): store + re-run updateSellItem.
	oc.mselectionHint = int(selection)
	t.merchantUpdateSellItem(oc, offers)

	// tryMoveItems(selection): auto-fill the payment slots from the player inventory for the picked offer.
	t.merchantTryMoveItems(p, oc, offers, int(selection))

	// broadcastChanges: re-send the authoritative window (payments + result now reflect the pick).
	t.sendMerchantContent(p)
}

// merchantTryMoveItems ports MerchantMenu.tryMoveItems(newTradeIndex): if the index is valid, evacuate any
// current payment items back into the player inventory, then — if both payment slots are now empty — pull
// the picked offer's cost items from the player inventory into the payment slots. Mirrors the jar:
//
//	if (newTradeIndex < 0 || offers.size() <= newTradeIndex) return
//	oldCostA = tradeContainer.getItem(0); if (!empty) { if (!moveItemStackTo(oldCostA, 3, 39, true)) return; setItem(0, oldCostA) }
//	oldCostB = tradeContainer.getItem(1); if (!empty) { if (!moveItemStackTo(oldCostB, 3, 39, true)) return; setItem(1, oldCostB) }
//	if (item0.empty && item1.empty) { offer = offers.get(idx); moveFromInventoryToPaymentSlot(0, costA);
//	                                  offer.getItemCostB().ifPresent(costB -> moveFromInventoryToPaymentSlot(1, costB)) }
//
// The menu-slot ranges (3..39) map to player inventory WINDOW slots 9..45 (main 9..35 + hotbar 36..44),
// so moveItemStackTo(...,3,39,...) is moveItemStackTo over window [9,45). CITE MerchantMenu.tryMoveItems.
func (t *TickLoop) merchantTryMoveItems(p *tickPlayer, oc *openContainer, offers merchantOffers, idx int) {
	if idx < 0 || idx >= len(offers) {
		return
	}
	inv := ensureInventory(p)

	// Evacuate any current payment0/payment1 back into the player inventory (menu 3..39 == window 9..45).
	if !stackEmpty(oc.mpay0) {
		work := oc.mpay0
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, true) {
			return
		}
		oc.mpay0 = work
	}
	if !stackEmpty(oc.mpay1) {
		work := oc.mpay1
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, true) {
			return
		}
		oc.mpay1 = work
	}

	// Only auto-fill when both payment slots are empty (the vanilla guard).
	if !stackEmpty(oc.mpay0) || !stackEmpty(oc.mpay1) {
		t.merchantUpdateSellItem(oc, offers)
		return
	}

	o := offers[idx]
	t.merchantMoveToPayment(inv, oc, 0, o.baseCostA)
	if o.costB != nil {
		t.merchantMoveToPayment(inv, oc, 1, *o.costB)
	}
	t.merchantUpdateSellItem(oc, offers)
}

// merchantMoveToPayment ports MerchantMenu.moveFromInventoryToPaymentSlot(paymentSlot, cost): scan the
// player inventory (menu 3..39 == window 9..45) for stacks matching the ItemCost and move them into the
// payment slot up to the item's max stack size. Mirrors the jar loop:
//
//	for (i = 3; i < 39; i++) {
//	    inv = slots.get(i).getItem();
//	    cur = tradeContainer.getItem(paymentSlot);
//	    if (inv.empty || !cost.test(inv) || (!cur.empty && !isSameItemSameComponents(inv, cur))) continue;
//	    move = min(inv.maxStackSize - cur.count, inv.count);
//	    newPay = inv.copyWithCount(cur.count + move); inv.shrink(move); setItem(paymentSlot, newPay);
//	    if (newPay.count >= maxStackSize) break;
//	}
//
// cost.test(inv) is ItemStack.is(item) (v1: same item id; the component predicate is EMPTY for the farmer
// trades so it always passes). CITE MerchantMenu.moveFromInventoryToPaymentSlot.
func (t *TickLoop) merchantMoveToPayment(inv *Inventory, oc *openContainer, paymentSlot int, cost itemCost) {
	// Iterate the player inventory window slots that back menu 3..38: main 9..35, then hotbar 36..44.
	windowSlots := merchantInvWindowSlots()
	for _, ws := range windowSlots {
		invStack := inv.get(ws)
		cur := merchantPaymentGet(oc, paymentSlot)
		if stackEmpty(invStack) {
			continue
		}
		if int(invStack.ItemID) != int(cost.item.ID) { // cost.test: same item id (EMPTY component predicate)
			continue
		}
		if !stackEmpty(cur) && !stackSameItemSameComponents(invStack, cur) {
			continue
		}
		maxStack := stackMaxSize(invStack)
		move := min(maxStack-int(cur.Count), int(invStack.Count))
		if move <= 0 {
			continue
		}
		newPay := stackCopyWithCount(invStack, int(cur.Count)+move)
		invStack.Count = pk.VarInt(int(invStack.Count) - move) // inv.shrink(move)
		if invStack.Count <= 0 {
			invStack = component.SlotData{Count: 0}
		}
		inv.set(ws, invStack)
		merchantPaymentSet(oc, paymentSlot, newPay)
		if int(newPay.Count) >= maxStack {
			break
		}
	}
}

// merchantInvWindowSlots returns the 36 player-inventory WINDOW slots that back MerchantMenu slots 3..38,
// in MENU order (main 3..29 -> window 9..35, then hotbar 30..38 -> window 36..44). The tryMoveItems/
// moveFromInventoryToPaymentSlot loops iterate the menu slots 3..38 in ascending order.
func merchantInvWindowSlots() []int16 {
	out := make([]int16, 0, 36)
	for i := 0; i < 27; i++ {
		out = append(out, int16(windowMainFirst+i))
	}
	for i := 0; i < 9; i++ {
		out = append(out, int16(windowHotbarFirst+i))
	}
	return out
}

// merchantPaymentGet / merchantPaymentSet address the two transient payment slots of a merchant window.
func merchantPaymentGet(oc *openContainer, slot int) component.SlotData {
	if slot == 0 {
		return oc.mpay0
	}
	return oc.mpay1
}

func merchantPaymentSet(oc *openContainer, slot int, s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	if slot == 0 {
		oc.mpay0 = s
	} else {
		oc.mpay1 = s
	}
}

// closeMerchantWindow ports MerchantMenu.removed(player): clear the villager's tradingPlayer
// (trader.setTradingPlayer(null)) and return the two payment inputs to the player inventory
// (placeItemBackInInventory over tradeContainer slots 0,1). The result slot is virtual (never returned).
// Called from handleContainerClose when the open window is a merchant.
//
//	[VERIFIED CFR MerchantMenu.removed: super.removed(player); trader.setTradingPlayer(null);
//	 live ServerPlayer -> placeItemBackInInventory(removeItemNoUpdate(0)) + placeItemBackInInventory(1).]
func (t *TickLoop) closeMerchantWindow(p *tickPlayer, oc *openContainer) {
	// trader.setTradingPlayer(null): re-resolve the villager (best-effort) and clear its trading player.
	if villager, _ := t.merchantOffersForWindow(oc); villager != nil {
		villagerSetTradingPlayer(villager, 0)
	}
	inv := ensureInventory(p)
	for _, s := range []component.SlotData{oc.mpay0, oc.mpay1} {
		if stackEmpty(s) {
			continue
		}
		// placeItemBackInInventory: add to the inventory, else drop as an ItemEntity (its overflow path).
		stack := s
		if !t.invAdd(inv, &stack) {
			t.playerDrop(p, stack, false)
		}
	}
	oc.mpay0 = component.SlotData{Count: 0}
	oc.mpay1 = component.SlotData{Count: 0}
	oc.mresult = component.SlotData{Count: 0}
}
