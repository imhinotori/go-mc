package server

// block_entity_persist.go — FURNACE block-entity NBT persistence (the chest twin), ported 1:1 from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session). Chest item persistence
// already round-trips through chunk_persist.go (flushChestItems, save) + chest_open.go (decodeChestBE,
// extended here). This file closes the FURNACE half: fold each live furnaceBE (t.furnaces) into the chunk's
// BlockEntity list as the {Items, cooking_time_spent, cooking_total_time, lit_time_remaining,
// lit_total_time, RecipesUsed} disk compound on save, and decode that compound back into a furnaceBE on
// first access (chunk load).
//
// 1:1 ANCHORS (VERIFIED javap net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity this session):
//
//	saveAdditional(ValueOutput out):
//	    super.saveAdditional(out);                                    // BaseContainerBlockEntity: CustomName (v1: none)
//	    out.putShort("cooking_time_spent",  (short) cookingTimer);
//	    out.putShort("cooking_total_time",  (short) cookingTotalTime);
//	    out.putShort("lit_time_remaining",  (short) litTimeRemaining);
//	    out.putShort("lit_total_time",      (short) litTotalTime);
//	    ContainerHelper.saveAllItems(out, items);                     // "Items" (keepEmptyTag = true, 2-arg form)
//	    out.store("RecipesUsed", RECIPES_USED_CODEC, recipesUsed);    // Codec.unboundedMap(Recipe.KEY_CODEC, INT)
//
//	loadAdditional(ValueInput in):
//	    super.loadAdditional(in);
//	    this.items = NonNullList.withSize(getContainerSize(), EMPTY);
//	    ContainerHelper.loadAllItems(in, items);                      // "Items"
//	    this.cookingTimer     = in.getShortOr("cooking_time_spent", 0);
//	    this.cookingTotalTime = in.getShortOr("cooking_total_time", 0);
//	    this.litTimeRemaining = in.getShortOr("lit_time_remaining", 0);
//	    this.litTotalTime     = in.getShortOr("lit_total_time", 0);
//	    recipesUsed.clear(); recipesUsed.putAll(in.read("RecipesUsed", RECIPES_USED_CODEC).orElse(Map.of()));
//
//	RECIPES_USED_CODEC = Codec.unboundedMap(Recipe.KEY_CODEC, Codec.INT)  — a string->int compound.
//
// CITED DEVIATION (the SAME one furnace_be.go documents for the live drive): v1's recipe.Cooking carries no
// registry ResourceKey, so the runtime keys recipesUsed by a SYNTHETIC per-recipe string (furnaceRecipeKey =
// "<subtype>:<result-item-id>") and carries that recipe's experience() in a parallel recipesXP map so the
// result-take XP award (floor(count*exp) + fractional orb) is byte-identical to createExperience. On DISK the
// RecipesUsed compound therefore stores {synthetic-key -> count}; recipesXP is NOT a vanilla field (vanilla
// re-resolves experience() from the recipe registry by the real ResourceKey on take), so on LOAD each restored
// key's experience is re-resolved from the recipe book via findCookingRecipeByResult — the same faithful
// re-derivation vanilla does by ResourceKey, just keyed by the synthetic id. This preserves the observable XP
// award exactly while staying inside v1's id-less recipe model.
//
// COMPONENTS: item components share the Phase-A drop of save.SaveItemsCompound (item id + count survive;
// components metered as droppedComponents). CITE save/item_nbt.go PHASE A/B header.

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// furnaceStateShape is the on-disk furnace block-entity compound beyond the "Items" list: the four cook
// progress shorts + the RecipesUsed map. It marshals ALONGSIDE the {Items:[...]} list into a single bare
// compound (BlockEntity.Data). The shorts use int16 so the NBT encoder emits TAG_Short (matching
// out.putShort). RecipesUsed is the Codec.unboundedMap(Recipe.KEY_CODEC, INT) string->int compound.
//
// CITE: AbstractFurnaceBlockEntity.saveAdditional field order (cooking_time_spent, cooking_total_time,
// lit_time_remaining, lit_total_time, RecipesUsed).
type furnaceStateShape struct {
	Items            []save.ItemStackWithSlotDisk `nbt:"Items,omitempty"`
	CookingTimeSpent int16                        `nbt:"cooking_time_spent"`
	CookingTotalTime int16                        `nbt:"cooking_total_time"`
	LitTimeRemaining int16                        `nbt:"lit_time_remaining"`
	LitTotalTime     int16                        `nbt:"lit_total_time"`
	RecipesUsed      map[string]int32             `nbt:"RecipesUsed,omitempty"`
}

// furnaceItemsToDisk translates a furnaceBE's 3-slot [3]component.SlotData into the []save.DiskItem the disk
// codec consumes (the furnace twin of chestItemsToDisk): each non-empty slot's numeric wire item id → its
// "minecraft:<name>" string id, and a component-bearing stack is flagged so SaveItemsCompound meters the
// Phase-A drop. Empty slots stay empty DiskItems so saveAllItems skips them by index.
func furnaceItemsToDisk(items [3]component.SlotData) []save.DiskItem {
	out := make([]save.DiskItem, len(items))
	for i, s := range items {
		if s.Count <= 0 {
			continue
		}
		out[i] = save.DiskItem{
			ID:               itemName(int32(s.ItemID)),
			Count:            int32(s.Count),
			HasComponents:    len(s.RawComponents) > 0,
			WireComponents:   s.RawComponents, // Phase B: SUPPORTED components transcoded to disk
			WireAddedCount:   int(s.AddedCount),
			WireRemovedCount: int(s.RemovedCount),
		}
	}
	return out
}

// encodeFurnaceBE builds the bare BlockEntity.Data compound for a furnaceBE — saveAdditional's full field
// set (Items + the four cook shorts + RecipesUsed). saveAllItems here is the 2-arg (keepEmptyTag=true) form
// AbstractFurnaceBlockEntity uses, so an all-empty furnace still emits an (empty) "Items" list. Returns the
// compound payload + the count of component-bearing stacks whose components were dropped (Phase A).
//
// CITE: AbstractFurnaceBlockEntity.saveAdditional (temp/cache/26.2-inner.jar).
func encodeFurnaceBE(f *furnaceBE) (nbt.RawMessage, int, error) {
	// ContainerHelper.saveAllItems(out, items) — the 2-arg form (keepEmptyTag = true).
	items, dropped := save.SaveAllItems(furnaceItemsToDisk(f.items), true)

	// RecipesUsed: the synthetic-key -> count map (the cited deviation). Omitted (nil) when empty so a
	// never-used furnace writes no RecipesUsed tag — vanilla's store of an empty Reference2IntOpenHashMap
	// yields an empty compound; omitempty drops it, which reads back as Map.of() (the orElse default). Same
	// observable state.
	var recipes map[string]int32
	if len(f.recipesUsed) > 0 {
		recipes = make(map[string]int32, len(f.recipesUsed))
		for k, v := range f.recipesUsed {
			recipes[k] = int32(v)
		}
	}

	shape := furnaceStateShape{
		Items:            items,
		CookingTimeSpent: int16(f.cookingTimer),     // (short) cookingTimer
		CookingTotalTime: int16(f.cookingTotalTime), // (short) cookingTotalTime
		LitTimeRemaining: int16(f.litTimeRemaining), // (short) litTimeRemaining
		LitTotalTime:     int16(f.litTotalTime),     // (short) litTotalTime
		RecipesUsed:      recipes,
	}
	doc, err := nbt.Marshal(shape)
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}, dropped, err
	}
	// nbt.Marshal emits [0x0A tag][0x00 0x00 name-len][payload...]; strip the 3-byte root header so the
	// result is the bare compound payload (the BlockEntity.Data convention, chestLootNBT / SaveItemsCompound).
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}, dropped, nil
}

// decodeFurnaceBE inverts encodeFurnaceBE: decode a furnace BlockEntity.Data compound into a fresh furnaceBE
// carrying its 3 items, the four cook shorts, and the RecipesUsed map (re-resolving each restored key's
// experience() from the recipe book so the take-time XP award is faithful). subtype + blastLike come from the
// CURRENT block state (the ctor's recipeType, not persisted — vanilla's BlockEntityType fixes it), threaded
// in by the caller. A garbled/empty compound yields an empty furnace (loadAllItems listOrEmpty tolerance) —
// never a panic (mirrors the tolerant chest decode).
//
// CITE: AbstractFurnaceBlockEntity.loadAdditional (temp/cache/26.2-inner.jar).
func decodeFurnaceBE(data nbt.RawMessage, sub cookSubtype, blastLike bool) *furnaceBE {
	f := &furnaceBE{subtype: sub, blastLike: blastLike}

	var shape furnaceStateShape
	if data.Type == nbt.TagCompound && len(data.Data) > 0 {
		// Tolerant decode: a corrupt BE loads empty (all-zero shorts, no items), never panics (T-20-04 twin).
		_ = data.Unmarshal(&shape)
	}

	// ContainerHelper.loadAllItems(in, items) into the 3-slot NonNullList.
	loaded := save.LoadAllItems(shape.Items, furnaceContainerSize)
	for i := 0; i < furnaceContainerSize && i < len(loaded); i++ {
		it := loaded[i]
		if it.IsEmpty() {
			continue // NonNullList.withSize left it EMPTY
		}
		f.items[i] = component.SlotData{
			ItemID:        toItemID(int(itemNameToID(it.ID))),
			Count:         toVar(int(it.Count)),
			AddedCount:    toVar(it.WireAddedCount),   // Phase B: rebuilt from the disk components compound
			RemovedCount:  toVar(it.WireRemovedCount), //
			RawComponents: it.WireComponents,          //
		}
	}

	// The four cook shorts (getShortOr(..., 0)).
	f.cookingTimer = int(shape.CookingTimeSpent)
	f.cookingTotalTime = int(shape.CookingTotalTime)
	f.litTimeRemaining = int(shape.LitTimeRemaining)
	f.litTotalTime = int(shape.LitTotalTime)

	// RecipesUsed: restore the synthetic-key -> count map, re-deriving each key's experience() from the
	// recipe book (the faithful analogue of vanilla re-resolving experience() by ResourceKey on take). A key
	// whose recipe is no longer resolvable keeps a 0 experience (its cooks award no XP — the same as a
	// removed recipe in vanilla, whose byKey lookup returns empty).
	if len(shape.RecipesUsed) > 0 {
		f.recipesUsed = make(map[string]int, len(shape.RecipesUsed))
		f.recipesXP = make(map[string]float64, len(shape.RecipesUsed))
		for k, v := range shape.RecipesUsed {
			f.recipesUsed[k] = int(v)
			f.recipesXP[k] = furnaceExperienceForKey(k)
		}
	}
	return f
}

// furnaceExperienceForKey re-derives a restored RecipesUsed key's experience() by re-matching the recipe that
// produces the key's result under the key's subtype — the load-time analogue of vanilla resolving a
// ResourceKey through recipeAccess().byKey to read experience(). furnaceRecipeKey builds the key as
// "<subtype>:<result-item-id>", so we split it back and look up the cooking recipe whose result is that item
// under that subtype. Returns 0 when no recipe matches (a removed/absent recipe awards no XP, faithful to a
// byKey miss).
func furnaceExperienceForKey(key string) float64 {
	sub, resultID, ok := splitFurnaceRecipeKey(key)
	if !ok {
		return 0
	}
	if r, ok := findCookingRecipeByResult(resultID, sub); ok {
		return r.Experience
	}
	return 0
}

// splitFurnaceRecipeKey inverts furnaceRecipeKey ("<subtype>:<result-item-id>"): the subtype is one of
// smelting/blasting/smoking; the remainder is the result item's "minecraft:<name>" id (which itself contains
// a ':'), so we split on the FIRST ':' only. Returns ok=false for an unrecognized subtype prefix.
func splitFurnaceRecipeKey(key string) (cookSubtype, int32, bool) {
	for _, sub := range []cookSubtype{cookSmelting, cookBlasting, cookSmoking} {
		prefix := string(sub) + ":"
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			return sub, itemNameToID(key[len(prefix):]), true
		}
	}
	return "", 0, false
}

// flushFurnaceItems ports AbstractFurnaceBlockEntity.saveAdditional for every live furnaceBE in the column at
// pos (the furnace twin of flushChestItems): each t.furnaces entry inside this column has its full state
// (Items + the four cook shorts + RecipesUsed) folded into the chunk's BlockEntity list, rewriting the
// matching furnace BE.Data in place (or appending a fresh BE for a placed-but-unrecorded furnace). Runs on
// the owner over tick-owned state; mutates only the chunk's own BE slice.
//
// CITE: AbstractFurnaceBlockEntity.saveAdditional (temp/cache/26.2-inner.jar).
func (t *TickLoop) flushFurnaceItems(pos level.ChunkPos, ch *level.Chunk) {
	if t.furnaces == nil {
		return
	}
	for fpos, f := range t.furnaces {
		if fpos.X>>4 != int(pos[0]) || fpos.Z>>4 != int(pos[1]) {
			continue // a furnace in a different column
		}
		data, dropped, err := encodeFurnaceBE(f)
		if err != nil {
			udebug("chunksave", "furnace state %v: %v", fpos, err)
			continue
		}
		if dropped > 0 {
			udebug("chunksave", "furnace %v: %d component-bearing stacks persisted without components (Phase A)", fpos, dropped)
		}
		lx, lz := fpos.X&15, fpos.Z&15
		setFurnaceBEData(ch, lx, fpos.Y, lz, furnaceBEType(f.subtype), data)
	}
}

// furnaceBEType maps a furnaceBE cook subtype back to its block-entity type id (the ctor's BlockEntityType).
// smelting -> minecraft:furnace, blasting -> minecraft:blast_furnace, smoking -> minecraft:smoker.
func furnaceBEType(sub cookSubtype) block.EntityType {
	switch sub {
	case cookBlasting:
		return block.EntityTypes["minecraft:blast_furnace"]
	case cookSmoking:
		return block.EntityTypes["minecraft:smoker"]
	default:
		return block.EntityTypes["minecraft:furnace"]
	}
}

// setFurnaceBEData rewrites the furnace BlockEntity's Data compound at the LOCAL (lx,y,lz) cell in ch to the
// supplied furnace-state payload, preserving/setting the BE type (furnace/blast_furnace/smoker). If no
// furnace-family BE exists at that cell, it appends a fresh one (a placed furnace whose BE was not in the gen
// list). The furnace twin of setChestBEData.
func setFurnaceBEData(ch *level.Chunk, lx, y, lz int, typ block.EntityType, data nbt.RawMessage) {
	for i := range ch.BlockEntity {
		be := &ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == y && isFurnaceEntityType(be.Type) {
			be.Type = typ // ensure the type matches (a placed furnace may have had a bare/empty BE)
			be.Data = data
			return
		}
	}
	var be level.BlockEntity
	be.PackXZ(lx, lz)
	be.Y = int16(y)
	be.Type = typ
	be.Data = data
	ch.BlockEntity = append(ch.BlockEntity, be)
}

// isFurnaceEntityType reports whether a block-entity type is one of the three furnace-family BE types.
func isFurnaceEntityType(t block.EntityType) bool {
	return t == block.EntityTypes["minecraft:furnace"] ||
		t == block.EntityTypes["minecraft:blast_furnace"] ||
		t == block.EntityTypes["minecraft:smoker"]
}

// loadFurnaceBE looks up the furnace BlockEntity at pos in the owning chunk's BlockEntity list and decodes
// its state into a fresh furnaceBE (subtype/blastLike from the passed-in current block state's cook type), or
// returns nil if no furnace BE is recorded there (a freshly-placed furnace with no persisted state → the
// caller synthesizes an empty one). The furnace twin of decodeChestBE. Tolerant decode (a garbled BE loads
// empty). Tick-owned.
func (t *TickLoop) loadFurnaceBE(pos pk.Position, sub cookSubtype, blastLike bool) *furnaceBE {
	if t.world() == nil {
		return nil
	}
	col := level.ChunkPos{int32(pos.X >> 4), int32(pos.Z >> 4)}
	ch, ok := t.world().Get(col)
	if !ok {
		return nil
	}
	lx, lz := pos.X&15, pos.Z&15
	for i := range ch.BlockEntity {
		be := ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx != lx || bz != lz || int(be.Y) != pos.Y {
			continue
		}
		if !isFurnaceEntityType(be.Type) {
			return nil // a non-furnace BE at this exact cell is not a furnace state
		}
		return decodeFurnaceBE(be.Data, sub, blastLike)
	}
	return nil
}

// markFurnaceDirty flags the column owning a furnace position as dirty so its state is flushed on the next
// save pass (the furnace twin of markChestDirty). The furnace cook drive + menu clicks call it after
// mutating a furnaceBE so a cook step / item move is persisted. A no-op when persistence is off or the column
// is not Ready. Runs on the owner (the furnace path is tick-owned).
func (t *TickLoop) markFurnaceDirty(furnacePos pk.Position) {
	if t.world() == nil {
		return
	}
	t.world().MarkDirty(level.ChunkPos{int32(furnacePos.X >> 4), int32(furnacePos.Z >> 4)})
}
