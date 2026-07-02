package server

// potion_component.go — the POTION_CONTENTS data-component seam the brewing mix engine reads/writes on
// a potion bottle ItemStack. A 1:1 port of the exact vanilla reads PotionBrewing.hasPotionMix / mix use:
//
//	ItemStack.getOrDefault(DataComponents.POTION_CONTENTS, PotionContents.EMPTY).potion()   // Optional<Holder<Potion>>
//	PotionContents.createItemStack(Item, Holder<Potion>)                                     // build the result bottle
//
// In Sulfur a SlotData carries its components as verbatim wire bytes (component.SlotData.RawComponents:
// the added+removed component lists after the removedCount header — see level/component/types.go). A
// potion bottle is therefore an ItemStack of item minecraft:potion/splash_potion/lingering_potion (or
// glass_bottle) whose RawComponents encode a single minecraft:potion_contents component (wire type id 51,
// data/registryid/datacomponenttype.go index 51). We read the potion registry id out of that component and
// write a fresh one for the mix result — the same PotionContents.potion() Optional the jar branches on.
//
// This is the potion-component seam the task cites: the potion id round-trips faithfully (the observable
// brewing behavior — WATER + NETHER_WART -> AWKWARD etc. — is identical); the CustomColor / CustomEffects /
// CustomName sub-fields of PotionContents are not synthesized by brewing (the vanilla mix result is
// PotionContents.createItemStack(item, potionHolder) = a bottle carrying ONLY the potion Holder, empty
// color/effects/name — VERIFIED PotionContents.createItemStack), so a written result encodes exactly the
// potion id, matching createItemStack byte-for-byte.

import (
	"bytes"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// potionContentsComponentID is the wire type id of minecraft:potion_contents (data/registryid/
// datacomponenttype.go index 51; component.NewComponent(51) -> *PotionContents). It is the varint that
// prefixes the component in a SlotData's added-component list.
const potionContentsComponentID = 51

// readBottlePotion ports ItemStack.getOrDefault(DataComponents.POTION_CONTENTS, PotionContents.EMPTY).
// potion(): decode the bottle's added components, find the minecraft:potion_contents entry, and return its
// potion registry id if present. The bool is PotionContents.potion().isPresent() — false when the stack has
// no potion_contents component OR the component carries no potion holder (PotionContents.EMPTY.potion() is
// Optional.empty). A non-potion / empty stack returns (0, false). Only the ADDED-component list is scanned
// (a brewed/placed bottle carries the component as an added override), matching how these bottles are built.
func readBottlePotion(s component.SlotData) (int32, bool) {
	if stackEmpty(s) || len(s.RawComponents) == 0 || int(s.AddedCount) <= 0 {
		return 0, false
	}
	r := bytes.NewReader(s.RawComponents)
	for i := int32(0); i < int32(s.AddedCount); i++ {
		var compType pk.VarInt
		if _, err := compType.ReadFrom(r); err != nil {
			return 0, false
		}
		comp := component.NewComponent(int32(compType))
		if comp == nil {
			return 0, false // unknown component: cannot advance the reader safely
		}
		if _, err := comp.ReadFrom(r); err != nil {
			return 0, false
		}
		if int32(compType) == potionContentsComponentID {
			pc, ok := comp.(*component.PotionContents)
			if !ok {
				return 0, false
			}
			if !bool(pc.PotionID.Has) {
				return 0, false // PotionContents present but potion() Optional is empty
			}
			return int32(pc.PotionID.Val), true
		}
	}
	return 0, false
}

// makePotionBottle ports PotionContents.createItemStack(Item, Holder<Potion>): a fresh count-1 bottle of the
// given item (minecraft:potion / splash_potion / lingering_potion) carrying a single potion_contents
// component whose only set field is the potion Holder (color/effects/name unset — createItemStack sets just
// the Potion). Encodes the SlotData with AddedCount=1, RemovedCount=0, and RawComponents = the
// (typeID=51, PotionContents{potion}) wire pair, so stackSameItemSameComponents distinguishes potions by id.
func makePotionBottle(itemID int32, potionID int32) component.SlotData {
	pc := &component.PotionContents{}
	pc.PotionID.Has = true
	pc.PotionID.Val = pk.VarInt(potionID)
	// CustomColor / CustomEffects / CustomName stay at their zero (unset) values — createItemStack sets
	// only the potion Holder.

	var buf bytes.Buffer
	var tid pk.VarInt = potionContentsComponentID
	_, _ = tid.WriteTo(&buf)
	_, _ = pc.WriteTo(&buf)

	return component.SlotData{
		Count:         1,
		ItemID:        pk.VarInt(itemID),
		AddedCount:    1,
		RemovedCount:  0,
		RawComponents: buf.Bytes(),
	}
}

// setBottlePotion ports the mix result rebuild for a container mix (mix() -> PotionContents.createItemStack
// (mix.to.value(), potionHolder)): a bottle of a NEW item type (e.g. potion -> splash_potion) carrying the
// SAME potion the source bottle held. Reads the source's potion id and builds a fresh bottle of newItemID.
func setBottlePotion(src component.SlotData, newItemID int32) component.SlotData {
	pid, ok := readBottlePotion(src)
	if !ok {
		return src // no potion holder: PotionContents.potion() empty -> mix() returns the input unchanged
	}
	return makePotionBottle(newItemID, pid)
}
