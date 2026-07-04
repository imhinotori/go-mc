package server

// beacon_menu.go — the BEACON menu (BEACON-01): the 1-payment-slot + 3-data-slot container UI over the
// tick-owned beaconBE, plus the ServerboundSetBeacon effect-selection handler. A 1:1 port of
// net.minecraft.world.inventory.BeaconMenu + BeaconMenu.PaymentSlot + BeaconBlockEntity.validateEffects +
// ServerGamePacketListenerImpl.handleSetBeaconPacket over the 26.2 jar (CFR/javap this session). Opens on
// right-clicking a beacon block (chest_open.go useBlockInteraction).
//
// 1:1 jar chain:
//
//	BeaconBlock.useWithoutItem(state, level, pos, player, hit):
//	    player.openMenu(getMenuProvider(state, level, pos));   // the BeaconBlockEntity is the MenuProvider
//	    return InteractionResult.SUCCESS;                        // CONSUMES -> no place
//	BeaconMenu(id, inventory, beaconData, access):
//	    paymentSlot = new PaymentSlot(beacon, 0, 136, 110);  addSlot(paymentSlot);   // slot 0
//	    addDataSlots(beaconData);                             // 3 data slots: levels, primary, secondary
//	    addStandardInventorySlots(inventory, 36, 137);        // main 1..27 + hotbar 28..36
//	BeaconMenu.PaymentSlot.mayPlace(stack): stack.is(ItemTags.BEACON_PAYMENT_ITEMS) && super.mayPlace (max 1).
//	BeaconMenu.getLevels() = beaconData.get(0); encodeEffect(h) = h==null ? 0 : MOB_EFFECT.getId(h)+1;
//	    decodeEffect(v) = v==0 ? null : MOB_EFFECT.byId(v-1).
//	BeaconMenu.updateEffects(primary, secondary): if paymentSlot.hasItem() && validateEffects(...) {
//	    beaconData.set(1, encodeEffect(primary)); beaconData.set(2, encodeEffect(secondary));
//	    paymentSlot.remove(1); access.execute(Level::blockEntityChanged); return true; } return false.
//	ServerGamePacketListenerImpl.handleSetBeaconPacket(packet): if (containerMenu instanceof BeaconMenu menu)
//	    { if (!stillValid) return; if (!menu.updateEffects(primary, secondary)) disconnect(...); }
//	BeaconMenu.removed(player): itemStack = paymentSlot.remove(maxStackSize); if (!empty) player.drop(itemStack, false).
//
// v1 subset (cited): no stats/spectator; the ContainerLevelAccess reach folds into the open-time reach gate.
// The payment slot + the 3 data slots ARE the beacon block-entity's fields (NOT a transient copy) — a click
// mutates the same beaconBE the effect drive ticks, so the SetBeacon selection takes effect next 80-tick pass.
// A bad SetBeacon (validateEffects false) mirrors vanilla's disconnect by simply rejecting the selection
// (the disconnect is a client-abuse guard; v1 no-ops it — cited — rather than kicking, since the observable
// gameplay is identical: no invalid effect is ever set).

import (
	"bytes"

	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// beaconMenuSize is the beacon-window slot count: 1 payment + 27 main + 9 hotbar = 37 (BeaconMenu: payment
// slot 0, then addStandardInventorySlots -> main 1..27, hotbar 28..36). Verified BeaconMenu ctor.
const beaconMenuSize = 1 + 27 + 9 // 37

// beaconSlotPayment is the BeaconMenu payment slot index (PaymentSlot at slot 0).
const beaconSlotPayment = 0

// Beacon data-slot ids (BeaconBlockEntity.DATA_*): the 3 ints the client's effect UI reads.
const (
	beaconDataLevels    = 0 // DATA_LEVELS    (levels)
	beaconDataPrimary   = 1 // DATA_PRIMARY   (encodeEffect(primaryPower))
	beaconDataSecondary = 2 // DATA_SECONDARY (encodeEffect(secondaryPower))
	beaconDataValues    = 3 // NUM_DATA_VALUES
)

// beaconPaymentItemsTag is the bare name of net.minecraft.tags.ItemTags.BEACON_PAYMENT_ITEMS
// (ItemTags.create("beacon_payment_items")) — {netherite_ingot, emerald, diamond, gold_ingot, iron_ingot}.
// PaymentSlot.mayPlace tests against it. CITE BeaconMenu.PaymentSlot.mayPlace.
const beaconPaymentItemsTag = "beacon_payment_items"

// beaconEffects mirrors BeaconBlockEntity.BEACON_EFFECTS: the effect lists PER LEVEL (index i == level i+1).
// L1 = speed/haste; L2 = resistance/jump_boost; L3 = strength; L4 = regeneration. getRequiredLevelsFor +
// the valid-effect filter read this. VERIFIED CFR BeaconBlockEntity.BEACON_EFFECTS.
var beaconEffects = [][]string{
	{effectSpeed, effectHaste},          // level 1
	{effectResistance, effectJumpBoost}, // level 2
	{effectStrength},                    // level 3
	{effectRegeneration},                // level 4
}

// beaconValidEffects is BeaconBlockEntity.VALID_EFFECTS: the flattened set of all beacon-grantable effects.
// filterEffect(effect) = VALID_EFFECTS.contains(effect) ? effect : null. Built once from beaconEffects.
var beaconValidEffects = func() map[string]bool {
	m := make(map[string]bool)
	for _, lvl := range beaconEffects {
		for _, id := range lvl {
			m[id] = true
		}
	}
	return m
}()

// beaconFilterEffect ports BeaconBlockEntity.filterEffect: keep the effect only if it is a valid beacon
// effect, else null (empty). CITE BeaconBlockEntity.filterEffect.
func beaconFilterEffect(id string) string {
	if beaconValidEffects[id] {
		return id
	}
	return ""
}

// beaconEncodeEffect ports BeaconMenu.encodeEffect(Holder<MobEffect>): null -> 0; else MOB_EFFECT registry id
// + 1 (the data slot reserves 0 for "none"). Uses registryid.MobEffect (the MOB_EFFECT registry order).
// CITE BeaconMenu.encodeEffect.
func beaconEncodeEffect(id string) int {
	if id == "" {
		return 0
	}
	for i, name := range registryid.MobEffect {
		if name == id {
			return i + 1
		}
	}
	return 0 // not in the registry -> treated as none (never happens for the valid effect set)
}

// beaconDecodeEffect ports BeaconMenu.decodeEffect(int): 0 -> null; else MOB_EFFECT.byId(value-1). Returns the
// empty string for an out-of-range id (a forged value decodes to "none", then filterEffect drops it anyway).
// CITE BeaconMenu.decodeEffect.
func beaconDecodeEffect(value int) string {
	if value == 0 {
		return ""
	}
	idx := value - 1
	if idx < 0 || idx >= len(registryid.MobEffect) {
		return ""
	}
	return registryid.MobEffect[idx]
}

// beaconRequiredLevelsFor ports BeaconBlockEntity.getRequiredLevelsFor(effect): 0 for null; the 1-based index
// of the first BEACON_EFFECTS level containing the effect; Integer.MAX_VALUE if the effect is not a beacon
// effect. CITE BeaconBlockEntity.getRequiredLevelsFor.
func beaconRequiredLevelsFor(id string) int {
	if id == "" {
		return 0
	}
	for i, lvl := range beaconEffects {
		for _, e := range lvl {
			if e == id {
				return i + 1
			}
		}
	}
	return int(^uint(0) >> 1) // Integer.MAX_VALUE
}

// beaconValidateEffects ports BeaconBlockEntity.validateEffects(primary, secondary, levels):
//
//	if (secondary != null && levels < 4) return false;
//	int primaryLevel = getRequiredLevelsFor(primary); int secondaryLevel = getRequiredLevelsFor(secondary);
//	if (primaryLevel > levels || secondaryLevel > levels) return false;
//	if (primaryLevel >= 4) return false;                       // primary can never be the level-4 (regen) slot
//	return secondaryLevel == 0 || secondaryLevel >= 4 || primary.equals(secondary);
//
// CITE BeaconBlockEntity.validateEffects.
func beaconValidateEffects(primary, secondary string, levels int) bool {
	if secondary != "" && levels < beaconLevelsForSecondary {
		return false
	}
	primaryLevel := beaconRequiredLevelsFor(primary)
	secondaryLevel := beaconRequiredLevelsFor(secondary)
	if primaryLevel > levels || secondaryLevel > levels {
		return false
	}
	if primaryLevel >= beaconLevelsForSecondary {
		return false
	}
	return secondaryLevel == 0 || secondaryLevel >= beaconLevelsForSecondary || primary == secondary
}

// resolveBeacon returns the tick-owned beaconBE for pos, creating an EMPTY one on first access — the analogue
// of a freshly-placed beacon's default BeaconBlockEntity (levels 0, no effect, lastCheckY unseeded). Returns
// nil only when pos is not a beacon block (or the world is unloaded). Tick-owned (t.beacons, openChests twin).
func (t *TickLoop) resolveBeacon(pos pk.Position) *beaconBE {
	if t.beacons == nil {
		t.beacons = make(map[pk.Position]*beaconBE)
	}
	if b, ok := t.beacons[pos]; ok {
		return b
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !isBeaconBlock(state) {
		return nil
	}
	b := &beaconBE{}
	t.beacons[pos] = b
	return b
}

// openBeacon ports BeaconBlock.useWithoutItem -> ServerPlayer.openMenu for a beacon at pos: resolve (or
// create) the beaconBE, close any prior window, allocate a windowId, send ClientboundOpenScreen(beacon) +
// the initial ContainerSetContent + the 3 data slots, and record the open-container state. Returns true (the
// action was consumed) once the menu is sent.
func (t *TickLoop) openBeacon(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil || t.world() == nil {
		return false
	}
	b := t.resolveBeacon(pos)
	if b == nil {
		return false
	}
	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}
	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindBeacon, beaconPos: pos}

	menuID := menuTypeID(registryid.Menu, "minecraft:beacon")
	p.client.Send(openScreen(int32(win), menuID, "Beacon"))

	t.sendBeaconContent(p, b)
	t.sendBeaconData(p, b)
	return true
}

// beaconMenuItems builds the 37-slot ContainerSetContent list: payment 0, then the player inventory (main
// 1..27 <- window 9..35, hotbar 28..36 <- window 36..44). Mirrors BeaconMenu's addSlot(payment) +
// addStandardInventorySlots.
func beaconMenuItems(b *beaconBE, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, beaconMenuSize)
	out[0] = b.payment
	for i := 0; i < 27; i++ {
		out[1+i] = inv.get(int16(windowMainFirst + i))
	}
	for i := 0; i < 9; i++ {
		out[1+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendBeaconContent pushes the authoritative ContainerSetContent for the open beacon window (initMenu ->
// broadcastChanges, and the resend after any click). Bumps the player inventory state id.
func (t *TickLoop) sendBeaconContent(p *tickPlayer, b *beaconBE) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindBeacon {
		return
	}
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		beaconMenuItems(b, inv), inv.getCarried()))
}

// sendBeaconData pushes the 3 data slots (levels, encodeEffect(primary), encodeEffect(secondary)) — the
// ContainerData the client's effect-selection UI reads (BeaconMenu.addDataSlots + broadcastChanges). v1 sends
// all 3 each change. CITE BeaconBlockEntity.dataAccess.get.
func (t *TickLoop) sendBeaconData(p *tickPlayer, b *beaconBE) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindBeacon {
		return
	}
	win := int32(p.openContainer.windowID)
	p.client.Send(containerSetData(win, beaconDataLevels, int16(b.levels)))
	p.client.Send(containerSetData(win, beaconDataPrimary, int16(beaconEncodeEffect(b.primaryPower))))
	p.client.Send(containerSetData(win, beaconDataSecondary, int16(beaconEncodeEffect(b.secondaryPower))))
}

// beaconPaymentMayPlace ports BeaconMenu.PaymentSlot.mayPlace(stack): stack.is(ItemTags.BEACON_PAYMENT_ITEMS)
// (an iron/gold/emerald/diamond/netherite ingot). CITE BeaconMenu.PaymentSlot.mayPlace.
func beaconPaymentMayPlace(stack component.SlotData) bool {
	if stackEmpty(stack) {
		return false
	}
	return itemInTag(int32(stack.ItemID), beaconPaymentItemsTag)
}

// beaconPaymentMaxStack ports BeaconMenu.PaymentSlot.getMaxStackSize() == 1 (PaymentSlot overrides it to 1).
// CITE BeaconMenu.PaymentSlot.getMaxStackSize.
const beaconPaymentMaxStack = 1

// clickedBeacon ports AbstractContainerMenu.clicked for an open beacon window: snapshot the payment slot +
// inventory + cursor, run the click under a panic-recover (no partial mutation on a throw, T-6-04), then
// re-send the authoritative content + cursor. The single payment slot (0) accepts only beacon-payment items,
// max 1; player cells are plain. PICKUP/QUICK_MOVE/THROW are supported.
func (t *TickLoop) clickedBeacon(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	b := t.beacons[oc.beaconPos]
	if b == nil {
		t.sendContent(p)
		return
	}
	inv := ensureInventory(p)

	paymentBefore := b.payment
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				b.payment = paymentBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doBeaconClick(p, b, inv, int(slotNum), button, int(input))
	}()

	t.sendBeaconContent(p, b)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(containerSetSlot(-1, inv.stateID, -1, inv.getCarried()))
	}
}

// doBeaconClick dispatches the supported click inputs over the beacon window (PICKUP/QUICK_MOVE/THROW). An
// unsupported input is a no-op (authoritative content re-sent regardless). Mirrors doDispenserClick.
func (t *TickLoop) doBeaconClick(p *tickPlayer, b *beaconBE, inv *Inventory, i, j, input int) {
	switch input {
	case containerInputPickup:
		t.beaconPickup(b, inv, i, j)
	case containerInputQuickMove:
		t.beaconQuickMove(b, inv, i)
	case containerInputThrow:
		t.beaconThrow(p, b, inv, i, j)
	}
}

// beaconSlotIsPayment reports whether a beacon-window slot index is the payment slot (0).
func beaconSlotIsPayment(i int) bool { return i == beaconSlotPayment }

// beaconPlayerWindowSlot maps a beacon-window slot index (1..36) to its player inventory window slot (main
// 9..35, hotbar 36..44). Returns (-1,false) for the payment slot / out-of-range.
func beaconPlayerWindowSlot(i int) (int16, bool) {
	switch {
	case i >= 1 && i < 1+27:
		return int16(windowMainFirst + (i - 1)), true
	case i >= 1+27 && i < beaconMenuSize:
		return int16(windowHotbarFirst + (i - 1 - 27)), true
	}
	return -1, false
}

// beaconPickup ports the PICKUP branch over the beacon window (payment slot 0 + player cells). The payment
// slot enforces PaymentSlot.mayPlace (beacon-payment items only) + max stack 1. Player cells are plain.
func (t *TickLoop) beaconPickup(b *beaconBE, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	primary := j == 0
	carried := inv.getCarried()

	if beaconSlotIsPayment(i) {
		slotItem := b.payment
		if stackEmpty(slotItem) {
			// place from cursor into payment: only if mayPlace, and clamped to max 1.
			if !stackEmpty(carried) && beaconPaymentMayPlace(carried) {
				c := carried
				one := stackSplit(&c, 1) // PaymentSlot max stack size 1
				b.payment = one
				inv.setCarried(c)
			}
			return
		}
		if stackEmpty(carried) {
			// take the payment onto the cursor (whole stack, which is at most 1).
			take := (int(slotItem.Count) + 1) / 2
			if primary {
				take = int(slotItem.Count)
			}
			rem := slotItem
			taken := stackSplit(&rem, take)
			b.payment = rem
			inv.setCarried(taken)
			return
		}
		// cursor + payment both non-empty: swap only if the cursor is a valid payment (max 1).
		if beaconPaymentMayPlace(carried) && stackSameItemSameComponents(slotItem, carried) {
			return // already a full (1) payment of the same item — nothing to merge (max 1)
		}
		if beaconPaymentMayPlace(carried) && int(carried.Count) <= beaconPaymentMaxStack {
			inv.setCarried(slotItem)
			b.payment = carried
		}
		return
	}

	// A player cell: plain pickup/place/merge/swap over the inventory slot.
	ws, ok := beaconPlayerWindowSlot(i)
	if !ok {
		return
	}
	slotItem := inv.get(ws)
	if stackEmpty(slotItem) {
		if !stackEmpty(carried) {
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			moved := stackSplit(&c, min(place, int(c.Count)))
			inv.set(ws, moved)
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
		inv.set(ws, rem)
		inv.setCarried(taken)
		return
	}
	if stackSameItemSameComponents(slotItem, carried) {
		add := 1
		if primary {
			add = int(carried.Count)
		}
		room := chestSlotMax(slotItem) - int(slotItem.Count)
		if add > room {
			add = room
		}
		if add > 0 {
			c := carried
			moved := stackSplit(&c, add)
			s := slotItem
			s.Count = toVar(int(s.Count) + int(moved.Count))
			inv.set(ws, s)
			inv.setCarried(c)
		}
	} else {
		inv.set(ws, carried)
		inv.setCarried(slotItem)
	}
}

// beaconQuickMove ports BeaconMenu.quickMoveStack: a payment slot (0) shift-moves into the player inventory
// (window 9..45); a player slot shift-moves — a beacon-payment item into the payment slot (if empty), else
// between main<->hotbar. CITE BeaconMenu.quickMoveStack (payment 0 -> [1,37); player -> payment if valid).
func (t *TickLoop) beaconQuickMove(b *beaconBE, inv *Inventory, i int) {
	if beaconSlotIsPayment(i) {
		if stackEmpty(b.payment) {
			return
		}
		work := b.payment
		if t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, true) {
			b.payment = work
			if b.payment.Count <= 0 {
				b.payment = component.SlotData{Count: 0}
			}
		}
		return
	}
	ws, ok := beaconPlayerWindowSlot(i)
	if !ok {
		return
	}
	src := inv.get(ws)
	if stackEmpty(src) {
		return
	}
	// A beacon-payment item shift-clicked from the inventory goes to the payment slot first (BeaconMenu
	// quickMoveStack: index in the inventory range -> moveItemStackTo(stack, 0, 1) [the payment slot]).
	if beaconPaymentMayPlace(src) && stackEmpty(b.payment) {
		one := src
		moved := stackSplit(&one, 1) // payment max 1
		b.payment = moved
		inv.set(ws, one)
		return
	}
	// Otherwise the standard main<->hotbar reshuffle over the player inventory window.
	work := src
	// main range (window 9..36) moves to hotbar (36..45), and vice-versa (BeaconMenu's addStandardInventory).
	if int(ws) < windowHotbarFirst {
		if t.moveItemStackTo(inv, &work, windowHotbarFirst, offhandWindowSlot, false) {
			inv.set(ws, work)
		}
	} else {
		if t.moveItemStackTo(inv, &work, windowMainFirst, windowHotbarFirst, false) {
			inv.set(ws, work)
		}
	}
}

// beaconThrow ports the THROW branch (drop key over the window): drop the whole (primary) or one (secondary)
// item from the clicked slot into the world. CITE AbstractContainerMenu.clicked THROW.
func (t *TickLoop) beaconThrow(p *tickPlayer, b *beaconBE, inv *Inventory, i, j int) {
	if i < 0 {
		return
	}
	if beaconSlotIsPayment(i) {
		if stackEmpty(b.payment) {
			return
		}
		drop := 1
		if j == 0 {
			drop = int(b.payment.Count)
		}
		out := b.payment
		thrown := stackSplit(&out, drop)
		b.payment = out
		if b.payment.Count <= 0 {
			b.payment = component.SlotData{Count: 0}
		}
		t.playerDrop(p, thrown, true)
		return
	}
	ws, ok := beaconPlayerWindowSlot(i)
	if !ok {
		return
	}
	src := inv.get(ws)
	if stackEmpty(src) {
		return
	}
	drop := 1
	if j == 0 {
		drop = int(src.Count)
	}
	thrown := stackSplit(&src, drop)
	inv.set(ws, src)
	t.playerDrop(p, thrown, true)
}

// handleSetBeacon resolves a ServerboundSetBeacon on-tick (BEACON-01). Ports
// ServerGamePacketListenerImpl.handleSetBeaconPacket: if the open window is a BeaconMenu and stillValid,
// updateEffects(primary, secondary). updateEffects: if the payment slot has an item AND validateEffects
// passes -> set primary/secondary (filtered) + CONSUME the payment (remove 1). A short/forged payload, a
// stale window, or an invalid selection is a silent no-op (vanilla disconnects on invalid; v1 no-ops it —
// cited — the observable gameplay is identical: no invalid effect is set). Decoded per the packet STREAM_CODEC
// (two Optional<Holder<MobEffect>> = Boolean present + VarInt holder-id per field).
//
//	[VERIFIED CFR ServerboundSetBeaconPacket.STREAM_CODEC: MobEffect.STREAM_CODEC.apply(optional) ×2 —
//	 wire = bool present1, [VarInt id1], bool present2, [VarInt id2]; the holder id is the plain MOB_EFFECT
//	 registry index (ByteBufCodecs.holderRegistry — NO +1 offset; the +1 is only the ContainerData encoding).]
func (t *TickLoop) handleSetBeacon(p *tickPlayer, pkt pk.Packet) {
	if p == nil || p.client == nil {
		return
	}
	primaryID, secondaryID, ok := beaconDecodeSetBeacon(pkt)
	if !ok {
		return
	}

	oc := p.openContainer
	if oc == nil || oc.kind != containerKindBeacon {
		return
	}
	b := t.beacons[oc.beaconPos]
	if b == nil {
		return
	}
	// stillValid: the open window must be the beacon the player is standing near (reuse the open-time reach
	// gate — the beacon block must still exist at oc.beaconPos). BeaconMenu.stillValid = stillValid(access,
	// player, Blocks.BEACON).
	if t.world() != nil {
		if s, sok := t.world().GetBlock(oc.beaconPos, dimMinY); !sok || !isBeaconBlock(s) {
			return
		}
	}

	// updateEffects: if (paymentSlot.hasItem()) { ... }
	if stackEmpty(b.payment) {
		return
	}
	levels := b.levels
	primaryEffect := beaconDecodeEffect(primaryID)
	secondaryEffect := beaconDecodeEffect(secondaryID)
	if !beaconValidateEffects(primaryEffect, secondaryEffect, levels) {
		return // updateEffects returns false -> vanilla disconnects; v1 no-ops (no invalid effect ever set).
	}
	// beaconData.set(1/2, encodeEffect(...)) -> the ContainerData.set path runs filterEffect(decodeEffect(v)).
	b.primaryPower = beaconFilterEffect(primaryEffect)
	b.secondaryPower = beaconFilterEffect(secondaryEffect)
	// paymentSlot.remove(1): consume one payment item (the "pay" for the selection).
	b.payment = beaconRemoveOne(b.payment)

	// access.execute(Level::blockEntityChanged): re-send the authoritative window (data slots reflect the new
	// selection + the consumed payment).
	t.sendBeaconContent(p, b)
	t.sendBeaconData(p, b)
}

// closeBeaconWindow ports BeaconMenu.removed(player): the payment slot is emptied and, if it held an item,
// DROPPED into the world (player.drop(itemStack, false)). The beacon's selected effect + level persist in the
// tick-owned beaconBE. CITE BeaconMenu.removed.
func (t *TickLoop) closeBeaconWindow(p *tickPlayer, oc *openContainer) {
	b := t.beacons[oc.beaconPos]
	if b == nil {
		return
	}
	if !stackEmpty(b.payment) {
		drop := b.payment
		b.payment = component.SlotData{Count: 0}
		t.playerDrop(p, drop, false) // player.drop(itemStack, false)
	}
}

// beaconRemoveOne shrinks a stack by one (Slot.remove(1)); an emptied stack becomes the empty stack.
func beaconRemoveOne(s component.SlotData) component.SlotData {
	s.Count = toVar(int(s.Count) - 1)
	if s.Count <= 0 {
		return component.SlotData{Count: 0}
	}
	return s
}

// beaconDecodeSetBeacon decodes the ServerboundSetBeacon payload (two Optional<Holder<MobEffect>> fields) into
// the two effect DATA ids (encodeEffect form: 0 == absent, holder-registry-id+1 == present). The wire is:
// Boolean present1, [VarInt holderId1], Boolean present2, [VarInt holderId2]. A present holder id is the plain
// MOB_EFFECT registry index; we convert it to the +1 data-slot form so beaconDecodeEffect can reuse it. A
// short/truncated payload returns ok=false (the handler no-ops — T-6-04).
func beaconDecodeSetBeacon(pkt pk.Packet) (primaryDataID, secondaryDataID int, ok bool) {
	r := bytes.NewReader(pkt.Data)
	p, pok := beaconReadOptionalHolder(r)
	if !pok {
		return 0, 0, false
	}
	s, sok := beaconReadOptionalHolder(r)
	if !sok {
		return 0, 0, false
	}
	return p, s, true
}

// beaconReadOptionalHolder reads one Optional<Holder<MobEffect>>: a Boolean present-flag, then (if present) a
// VarInt holder registry id. Returns the DATA-slot form (0 == absent, registry-id+1 == present) so the caller
// can reuse beaconDecodeEffect. Returns ok=false on a short read. CITE ByteBufCodecs.optional +
// ByteBufCodecs.holderRegistry(MOB_EFFECT).
func beaconReadOptionalHolder(r *bytes.Reader) (dataID int, ok bool) {
	var present pk.Boolean
	if _, err := present.ReadFrom(r); err != nil {
		return 0, false
	}
	if !bool(present) {
		return 0, true // absent -> encodeEffect(null) == 0
	}
	var holderID pk.VarInt
	if _, err := holderID.ReadFrom(r); err != nil {
		return 0, false
	}
	if holderID < 0 {
		return 0, false
	}
	return int(holderID) + 1, true // present -> the +1 DATA-slot form (decodeEffect(v) = byId(v-1))
}
