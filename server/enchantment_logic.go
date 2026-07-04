package server

// enchantment_logic.go — the shared ENCHANTMENT logic the AnvilMenu and EnchantmentMenu ports
// depend on: (1) reading/writing the item data components an enchanted stack carries on the WIRE
// (component.SlotData.RawComponents) — enchantments, damage/max_damage, repair_cost, custom_name,
// enchantable, repairable; (2) the wire-id <-> resource-id enchantment resolver (the same
// authoritative registrydata.EnchantmentOrder the save transcoder + wire packet use); and (3) the
// 1:1 ports of the net.minecraft.world.item.enchantment.EnchantmentHelper selection chain
// (getEnchantmentCost / selectEnchantment / getAvailableEnchantmentResults) plus Enchantment's
// cost/compatibility helpers and WeightedRandom.getRandomItem.
//
// 1:1 jar ports (temp/cache/26.2-inner.jar, CFR/javap this session, cited per function):
//   EnchantmentHelper.getEnchantmentCost / selectEnchantment / getAvailableEnchantmentResults /
//   filterCompatibleEnchantments; Enchantment.areCompatible / isPrimaryItem / isSupportedItem /
//   canEnchant / getMinCost / getMaxCost / getAnvilCost; WeightedRandom.getRandomItem.
//
// COMPONENT SEAM (cited): a v1 wire stack carries ONLY the components the client/plugin set on it
// (RawComponents) — there is no per-item DEFAULT component table yet (no items.json with
// max_damage / enchantable / repairable defaults). So isEnchantable/isDamageableItem read the
// ENCHANTABLE / MAX_DAMAGE / DAMAGE components off the stack exactly as vanilla's
// ItemStack.has(component) does: a stack that actually carries those components (an enchanted book,
// a damaged tool with its damage/max_damage components) behaves fully; a bare item with no such
// components is (faithfully) not enchantable / not damageable — matching ItemStack.isEnchantable()
// == has(ENCHANTABLE) and isDamageableItem() == has(MAX_DAMAGE)&&has(DAMAGE)&&!has(UNBREAKABLE).
// When a future plan adds per-item default components, these readers pick them up with no change.

import (
	"bytes"
	"sort"
	"sync"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/registrydata"
)

// ---- enchantment wire-id <-> resource-id resolver -----------------------------------------------
//
// The wire numeric id of an enchantment is its index in registrydata.EnchantmentOrder() — the same
// order the server sends the registry to the client (Enchantment.STREAM_CODEC = holderRegistry
// VarInt of the registry index) and the same order the save-layer transcoder resolves against.

var (
	enchOrderOnce sync.Once
	enchOrder     []string       // index == wire id -> resource id
	enchIndex     map[string]int // resource id -> wire id
)

func loadEnchantOrder() {
	order, err := registrydata.EnchantmentOrder()
	if err != nil {
		return
	}
	enchOrder = order
	enchIndex = make(map[string]int, len(order))
	for i, name := range order {
		enchIndex[name] = i
	}
}

// enchantWireName resolves a wire enchantment id to its resource id ("minecraft:sharpness"), or ""
// if out of range / the order is unavailable.
func enchantWireName(id int) string {
	enchOrderOnce.Do(loadEnchantOrder)
	if id < 0 || id >= len(enchOrder) {
		return ""
	}
	return enchOrder[id]
}

// enchantWireID resolves a resource id to its wire id, or -1 if unknown.
func enchantWireID(name string) int {
	enchOrderOnce.Do(loadEnchantOrder)
	if id, ok := enchIndex[name]; ok {
		return id
	}
	return -1
}

// ---- reading data components off a wire SlotData ------------------------------------------------

// stackComponent decodes the FIRST added component of the given wire type id from a stack's
// RawComponents, returning it as the concrete component.DataComponent (or nil if absent/undecodable
// or if the type was removed). It walks the added-component span (VarInt typeId + body) exactly as
// component.SlotData.ReadFrom captured it. CITE ItemStack.get(DataComponentType).
func stackComponent(s component.SlotData, wantType int32) component.DataComponent {
	if len(s.RawComponents) == 0 || s.AddedCount <= 0 {
		return nil
	}
	r := bytes.NewReader(s.RawComponents)
	for i := int32(0); i < int32(s.AddedCount); i++ {
		var typeID pk.VarInt
		if _, err := typeID.ReadFrom(r); err != nil {
			return nil
		}
		comp := component.NewComponent(int32(typeID))
		if comp == nil {
			return nil // unknown component: the body length is unknown, cannot advance
		}
		if _, err := comp.ReadFrom(r); err != nil {
			return nil
		}
		if int32(typeID) == wantType {
			return comp
		}
	}
	return nil
}

// stackHasComponent reports whether the stack carries the given added component (removed entries do
// not count as present). CITE ItemStack.has(DataComponentType).
func stackHasComponent(s component.SlotData, wantType int32) bool {
	return stackComponent(s, wantType) != nil
}

// stackEnchantments reads the item's minecraft:enchantments component into a resource-id->level
// map (EnchantmentHelper.getEnchantmentsForCrafting == getOrDefault(ENCHANTMENTS, EMPTY)). An
// absent component yields an empty map. Unresolvable wire ids are skipped (never fabricated).
func stackEnchantments(s component.SlotData) map[string]int {
	return readEnchantComponent(s, enchTypeEnchantments)
}

// stackStoredEnchantments reads minecraft:stored_enchantments (an enchanted book's carried
// enchantments) into a resource-id->level map.
func stackStoredEnchantments(s component.SlotData) map[string]int {
	return readEnchantComponent(s, enchTypeStoredEnchantments)
}

func readEnchantComponent(s component.SlotData, wantType int32) map[string]int {
	out := map[string]int{}
	comp := stackComponent(s, wantType)
	if comp == nil {
		return out
	}
	var entries []component.EnchantmentEntry
	switch c := comp.(type) {
	case *component.Enchantments:
		entries = c.Enchantments
	case *component.StoredEnchantments:
		entries = c.Enchantments
	default:
		return out
	}
	for _, e := range entries {
		name := enchantWireName(int(e.ID))
		if name == "" {
			continue
		}
		out[name] = int(e.Level)
	}
	return out
}

// stackInt reads an integer-valued component (damage / max_damage / repair_cost / enchantable) off
// the stack, returning (value, present). Each of these wire components is a bare pk.VarInt body.
func stackInt(s component.SlotData, wantType int32) (int, bool) {
	comp := stackComponent(s, wantType)
	if comp == nil {
		return 0, false
	}
	switch c := comp.(type) {
	case *component.Damage:
		return int(c.VarInt), true
	case *component.MaxDamage:
		return int(c.VarInt), true
	case *component.RepairCost:
		return int(c.VarInt), true
	case *component.Enchantable:
		return int(c.VarInt), true
	}
	return 0, false
}

// stackCustomName reads the item's custom_name text ("" if absent).
func stackCustomName(s component.SlotData) (string, bool) {
	comp := stackComponent(s, enchTypeCustomName)
	if comp == nil {
		return "", false
	}
	if c, ok := comp.(*component.CustomName); ok {
		return c.Name.String(), true
	}
	return "", false
}

// ---- writing data components onto a wire SlotData -----------------------------------------------
//
// setStackComponents rebuilds a stack's RawComponents from an ordered list of components + a set of
// removed type ids, keeping the added/removed counts consistent. The server never needs to preserve
// component ORDER on the wire (the client reconciles by type), so we re-emit a deterministic list.

// stackComponentEdit accumulates component add/remove edits for a stack, then materializes them.
type stackComponentEdit struct {
	base    component.SlotData
	added   map[int32]component.DataComponent // typeId -> component (present)
	removed map[int32]bool                    // typeId -> removed marker
	order   []int32                           // deterministic emit order (insertion order)
}

// editStack starts an edit from a base stack, seeding it with the stack's currently-present
// components so an unedited materialize round-trips them.
func editStack(base component.SlotData) *stackComponentEdit {
	e := &stackComponentEdit{
		base:    base,
		added:   map[int32]component.DataComponent{},
		removed: map[int32]bool{},
	}
	// Seed present components (decode the added span; a removed span is preserved as removed).
	if len(base.RawComponents) > 0 && base.AddedCount > 0 {
		r := bytes.NewReader(base.RawComponents)
		for i := int32(0); i < int32(base.AddedCount); i++ {
			var typeID pk.VarInt
			if _, err := typeID.ReadFrom(r); err != nil {
				break
			}
			comp := component.NewComponent(int32(typeID))
			if comp == nil {
				break
			}
			if _, err := comp.ReadFrom(r); err != nil {
				break
			}
			e.setRaw(int32(typeID), comp)
		}
		// Preserve removed entries (the tail after the added span).
		for i := int32(0); i < int32(base.RemovedCount); i++ {
			var typeID pk.VarInt
			if _, err := typeID.ReadFrom(r); err != nil {
				break
			}
			e.remove(int32(typeID))
		}
	}
	return e
}

func (e *stackComponentEdit) setRaw(typeID int32, comp component.DataComponent) {
	if _, seen := e.added[typeID]; !seen {
		if !e.removed[typeID] {
			e.order = append(e.order, typeID)
		}
	}
	delete(e.removed, typeID)
	e.added[typeID] = comp
}

// set adds/overwrites a present component (ItemStack.set).
func (e *stackComponentEdit) set(typeID int32, comp component.DataComponent) {
	e.setRaw(typeID, comp)
}

// remove marks a component removed (ItemStack.remove); an added entry is dropped.
func (e *stackComponentEdit) remove(typeID int32) {
	delete(e.added, typeID)
	e.removed[typeID] = true
}

// materialize rebuilds the SlotData with the accumulated components. The added span is emitted in
// insertion order; the removed span follows.
func (e *stackComponentEdit) materialize() component.SlotData {
	out := e.base
	var buf bytes.Buffer
	added := 0
	for _, typeID := range e.order {
		comp, ok := e.added[typeID]
		if !ok {
			continue
		}
		_, _ = pk.VarInt(typeID).WriteTo(&buf)
		_, _ = comp.WriteTo(&buf)
		added++
	}
	removed := 0
	// Deterministic removed order (sorted type ids).
	remIDs := make([]int32, 0, len(e.removed))
	for typeID := range e.removed {
		remIDs = append(remIDs, typeID)
	}
	sort.Slice(remIDs, func(i, j int) bool { return remIDs[i] < remIDs[j] })
	for _, typeID := range remIDs {
		_, _ = pk.VarInt(typeID).WriteTo(&buf)
		removed++
	}
	out.AddedCount = pk.VarInt(added)
	out.RemovedCount = pk.VarInt(removed)
	out.RawComponents = buf.Bytes()
	return out
}

// setEnchantmentsComponent writes the minecraft:enchantments component from a resource-id->level
// map onto the edit (unresolvable ids are dropped — never fabricated). An empty map removes the
// component. Entries are sorted by wire id for a stable span. CITE EnchantmentHelper.setEnchantments
// (== set(ENCHANTMENTS, mutable.toImmutable())).
func (e *stackComponentEdit) setEnchantments(m map[string]int) {
	// EnchantmentHelper.setEnchantments == set(type, immutable): ALWAYS present, even empty (an
	// enchantable item carries a present-but-empty ENCHANTMENTS component). So an empty map writes a
	// present component with no entries — never a remove.
	entries := make([]component.EnchantmentEntry, 0, len(m))
	for name, level := range m {
		wid := enchantWireID(name)
		if wid < 0 || level <= 0 {
			continue
		}
		entries = append(entries, component.EnchantmentEntry{ID: pk.VarInt(wid), Level: pk.VarInt(level)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	e.set(enchTypeEnchantments, &component.Enchantments{Enchantments: entries})
}

// setStoredEnchantments writes the minecraft:stored_enchantments component (used when a plain BOOK
// becomes an ENCHANTED_BOOK in the table). Same shape as setEnchantments.
func (e *stackComponentEdit) setStoredEnchantments(m map[string]int) {
	entries := make([]component.EnchantmentEntry, 0, len(m))
	for name, level := range m {
		wid := enchantWireID(name)
		if wid < 0 || level <= 0 {
			continue
		}
		entries = append(entries, component.EnchantmentEntry{ID: pk.VarInt(wid), Level: pk.VarInt(level)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	e.set(enchTypeStoredEnchantments, &component.StoredEnchantments{Enchantments: entries})
}

// setInt writes an integer-valued component (damage / repair_cost) onto the edit.
func (e *stackComponentEdit) setInt(typeID int32, v int) {
	switch typeID {
	case enchTypeDamage:
		e.set(typeID, &component.Damage{VarInt: pk.VarInt(v)})
	case enchTypeRepairCost:
		e.set(typeID, &component.RepairCost{VarInt: pk.VarInt(v)})
	case enchTypeMaxDamage:
		e.set(typeID, &component.MaxDamage{VarInt: pk.VarInt(v)})
	}
}

// setCustomName writes the minecraft:custom_name component from a literal string
// (Component.literal(name)).
func (e *stackComponentEdit) setCustomName(name string) {
	e.set(enchTypeCustomName, &component.CustomName{Name: chat.Text(name)})
}

// ---- wire type ids of the components the enchant subsystems touch --------------------------------
// (level/component/components.go NewComponent switch; kept as named constants so the readers/writers
// stay legible.)
const (
	enchTypeMaxDamage          int32 = 2
	enchTypeDamage             int32 = 3
	enchTypeCustomName         int32 = 6
	enchTypeEnchantments       int32 = 13
	enchTypeRepairCost         int32 = 19
	enchTypeEnchantable        int32 = 31
	enchTypeRepairable         int32 = 33
	enchTypeStoredEnchantments int32 = 42
)

// ---- item / stack predicates (ItemStack.* the enchant chain calls) ------------------------------

// isDamageableItem ports ItemStack.isDamageableItem: has(MAX_DAMAGE) && !has(UNBREAKABLE) &&
// has(DAMAGE). (Unbreakable wire type is 4.)
func isDamageableItem(s component.SlotData) bool {
	return stackHasComponent(s, enchTypeMaxDamage) &&
		!stackHasComponent(s, 4 /* unbreakable */) &&
		stackHasComponent(s, enchTypeDamage)
}

// stackMaxDamage ports ItemStack.getMaxDamage (getOrDefault(MAX_DAMAGE, 0)).
func stackMaxDamage(s component.SlotData) int {
	v, _ := stackInt(s, enchTypeMaxDamage)
	return v
}

// stackDamageValue ports ItemStack.getDamageValue (Mth.clamp(getOrDefault(DAMAGE,0),0,getMaxDamage)).
func stackDamageValue(s component.SlotData) int {
	v, _ := stackInt(s, enchTypeDamage)
	max := stackMaxDamage(s)
	if v < 0 {
		v = 0
	}
	if v > max {
		v = max
	}
	return v
}

// stackRepairCost ports getOrDefault(REPAIR_COST, 0).
func stackRepairCost(s component.SlotData) int {
	v, _ := stackInt(s, enchTypeRepairCost)
	return v
}

// stackEnchantableValue ports the ENCHANTABLE component's value (the item enchantability the
// enchant-table cost math reads: enchantable.value()). Returns (value, present).
func stackEnchantableValue(s component.SlotData) (int, bool) {
	return stackInt(s, enchTypeEnchantable)
}

// isEnchantable ports ItemStack.isEnchantable: has(ENCHANTABLE) && (ENCHANTMENTS is empty). An
// item with no ENCHANTABLE component is not enchantable (the v1 component seam).
func isEnchantable(s component.SlotData) bool {
	if !stackHasComponent(s, enchTypeEnchantable) {
		return false
	}
	return len(stackEnchantments(s)) == 0
}

// canStoreEnchantments ports EnchantmentHelper.canStoreEnchantments: has(getComponentType(stack)),
// where getComponentType is STORED_ENCHANTMENTS for an enchanted book, else ENCHANTMENTS. v1 selects
// the type by whether the stack currently carries STORED_ENCHANTMENTS (an enchanted book) — matching
// the vanilla item-class branch for the common inventory set.
func canStoreEnchantments(s component.SlotData) bool {
	if enchantComponentTypeIsStored(s) {
		return stackHasComponent(s, enchTypeStoredEnchantments)
	}
	return stackHasComponent(s, enchTypeEnchantments)
}

// enchantComponentTypeIsStored reports whether EnchantmentHelper.getComponentType(stack) is
// STORED_ENCHANTMENTS (an enchanted book) rather than ENCHANTMENTS. Vanilla keys on the item class
// (EnchantedBookItem); v1 keys on the stack carrying a stored_enchantments component OR being the
// enchanted_book item id.
func enchantComponentTypeIsStored(s component.SlotData) bool {
	if stackHasComponent(s, enchTypeStoredEnchantments) {
		return true
	}
	return int32(s.ItemID) == itemNameToID("enchanted_book")
}

// enchantmentsForCrafting ports EnchantmentHelper.getEnchantmentsForCrafting: the enchantments held
// under the stack's active component type (ENCHANTMENTS, or STORED_ENCHANTMENTS for a book).
func enchantmentsForCrafting(s component.SlotData) map[string]int {
	if enchantComponentTypeIsStored(s) {
		return stackStoredEnchantments(s)
	}
	return stackEnchantments(s)
}

// ---- Enchantment definition helpers (Enchantment.* over registrydata) ---------------------------

// enchantAnvilCost ports Enchantment.getAnvilCost. Unknown id -> 0.
func enchantAnvilCost(id string) int {
	if def, ok := registrydata.EnchantmentDefinition(id); ok {
		return def.AnvilCost
	}
	return 0
}

// enchantMaxLevelOf ports Enchantment.getMaxLevel. Unknown id -> 1 (defensive; getMinLevel is 1).
func enchantMaxLevelOf(id string) int {
	if def, ok := registrydata.EnchantmentDefinition(id); ok {
		return def.MaxLevel
	}
	return 1
}

// enchantWeightOf ports Enchantment.getWeight. Unknown id -> 0.
func enchantWeightOf(id string) int {
	if def, ok := registrydata.EnchantmentDefinition(id); ok {
		return def.Weight
	}
	return 0
}

// enchantMinCostOf ports Enchantment.getMinCost(level) = minCost.calculate(level).
func enchantMinCostOf(id string, level int) int {
	if def, ok := registrydata.EnchantmentDefinition(id); ok {
		return def.MinCost.Calculate(level)
	}
	return 0
}

// enchantMaxCostOf ports Enchantment.getMaxCost(level) = maxCost.calculate(level).
func enchantMaxCostOf(id string, level int) int {
	if def, ok := registrydata.EnchantmentDefinition(id); ok {
		return def.MaxCost.Calculate(level)
	}
	return 0
}

// enchantAreCompatible ports Enchantment.areCompatible(a, b): !a.equals(b) &&
// !a.exclusiveSet.contains(b) && !b.exclusiveSet.contains(a). The exclusiveSet is resolved through
// the enchantment's exclusive_set holder reference (a "#tag" or a list of ids).
func enchantAreCompatible(a, b string) bool {
	if a == b {
		return false
	}
	if enchantExclusiveContains(a, b) || enchantExclusiveContains(b, a) {
		return false
	}
	return true
}

// enchantExclusiveContains reports whether `owner`'s exclusive_set contains `other`. The set may be
// a "#tag" (resolved via registrydata.EnchantmentInTag), a single id, or a list.
func enchantExclusiveContains(owner, other string) bool {
	def, ok := registrydata.EnchantmentDefinition(owner)
	if !ok || len(def.ExclusiveSet) == 0 {
		return false
	}
	otherNorm := normalizeEnchantResource(other)
	for _, ref := range def.ExclusiveSet {
		if len(ref) > 0 && ref[0] == '#' {
			in, err := registrydata.EnchantmentInTag(other, ref)
			if err == nil && in {
				return true
			}
			continue
		}
		if normalizeEnchantResource(ref) == otherNorm {
			return true
		}
	}
	return false
}

// enchantCanEnchant ports Enchantment.canEnchant(itemStack): the item type is in the enchantment's
// supported_items set. v1 resolves supported_items through the item-tag membership (data/tag.ItemTags
// for "#minecraft:enchantable/*" refs) or a direct id compare.
func enchantCanEnchant(id string, s component.SlotData) bool {
	def, ok := registrydata.EnchantmentDefinition(id)
	if !ok {
		return false
	}
	return itemInHolderRefs(def.SupportedItems, s)
}

// enchantIsPrimaryItem ports Enchantment.isPrimaryItem(itemStack): isSupportedItem(item) &&
// (primaryItems empty || item.is(primaryItems)).
func enchantIsPrimaryItem(id string, s component.SlotData) bool {
	def, ok := registrydata.EnchantmentDefinition(id)
	if !ok {
		return false
	}
	if !itemInHolderRefs(def.SupportedItems, s) {
		return false
	}
	if len(def.PrimaryItems) == 0 {
		return true
	}
	return itemInHolderRefs(def.PrimaryItems, s)
}

// itemInHolderRefs reports whether the stack's item id is a member of any of the holder references
// (a "#minecraft:enchantable/foo" item tag or a bare "minecraft:id"). Item tags are read from
// data/tag.ItemTags via itemInTag. An empty ref list -> false (an empty HolderSet contains nothing).
func itemInHolderRefs(refs []string, s component.SlotData) bool {
	itemID := int32(s.ItemID)
	for _, ref := range refs {
		if len(ref) > 0 && ref[0] == '#' {
			// "#minecraft:enchantable/sharp_weapon" -> tag name "enchantable/sharp_weapon".
			tag := ref[1:]
			if len(tag) >= len("minecraft:") && tag[:len("minecraft:")] == "minecraft:" {
				tag = tag[len("minecraft:"):]
			}
			if itemInTag(itemID, tag) {
				return true
			}
			continue
		}
		if int32(itemNameToID(stripNamespace(ref))) == itemID {
			return true
		}
	}
	return false
}

func normalizeEnchantResource(s string) string {
	if len(s) > 0 && s[0] == '#' {
		s = s[1:]
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s
		}
	}
	return "minecraft:" + s
}

// ---- EnchantmentInstance (the selectEnchantment result element) ---------------------------------

// enchantInstance is net.minecraft.world.item.enchantment.EnchantmentInstance (a (holder, level)
// pair) reduced to the resource id + level. weight() delegates to the definition weight (the
// WeightedRandom.getRandomItem selector).
type enchantInstance struct {
	id    string
	level int
}

// ---- EnchantmentHelper ports --------------------------------------------------------------------

// getEnchantmentCost ports EnchantmentHelper.getEnchantmentCost(random, slot, bookcases, itemStack):
//
//	Enchantable enchantable = itemStack.get(ENCHANTABLE); if (enchantable == null) return 0;
//	if (bookcases > 15) bookcases = 15;
//	int selected = random.nextInt(8) + 1 + (bookcases >> 1) + random.nextInt(bookcases + 1);
//	slot 0 -> Math.max(selected/3, 1);  slot 1 -> selected*2/3 + 1;  slot 2 -> Math.max(selected, bookcases*2).
//
// CITE EnchantmentHelper.getEnchantmentCost.
func getEnchantmentCost(random *legacyRandom, slot, bookcases int, s component.SlotData) int {
	if _, ok := stackEnchantableValue(s); !ok {
		return 0 // enchantable == null
	}
	if bookcases > 15 {
		bookcases = 15
	}
	selected := int(random.nextIntN(8)) + 1 + (bookcases >> 1) + int(random.nextIntN(int32(bookcases+1)))
	switch slot {
	case 0:
		return max(selected/3, 1)
	case 1:
		return selected*2/3 + 1
	default:
		return max(selected, bookcases*2)
	}
}

// selectEnchantment ports EnchantmentHelper.selectEnchantment(random, itemStack, cost, source):
//
//	Enchantable enchantable = itemStack.get(ENCHANTABLE); if (enchantable == null) return [];
//	cost += 1 + random.nextInt(v/4+1) + random.nextInt(v/4+1);
//	float randomSpan = (random.nextFloat() + random.nextFloat() - 1) * 0.15f;
//	cost = Mth.clamp(Math.round(cost + cost*randomSpan), 1, MAX);
//	List enchantments = getAvailableEnchantmentResults(cost, itemStack, source);
//	if (!empty) { WeightedRandom.getRandomItem(random, enchantments, weight).ifPresent(add);
//	    while (random.nextInt(50) <= cost) {
//	        if (!results.empty) filterCompatibleEnchantments(enchantments, results.last());
//	        if (enchantments.empty) break;
//	        WeightedRandom.getRandomItem(random, enchantments, weight).ifPresent(add);
//	        cost /= 2;
//	    } }
//
// `source` is the ordered candidate enchantment id list (the IN_ENCHANTING_TABLE tag, sorted for a
// stable holderset order). CITE EnchantmentHelper.selectEnchantment.
func selectEnchantment(random *legacyRandom, s component.SlotData, cost int, source []string) []enchantInstance {
	results := []enchantInstance{}
	v, ok := stackEnchantableValue(s)
	if !ok {
		return results // enchantable == null
	}
	cost += 1 + int(random.nextIntN(int32(v/4+1))) + int(random.nextIntN(int32(v/4+1)))
	randomSpan := (random.nextFloat() + random.nextFloat() - 1.0) * 0.15
	cost = mthRoundToInt(float32(cost) + float32(cost)*randomSpan)
	cost = mthClampInt(cost, 1, int(int32(^uint32(0)>>1)))

	enchantments := getAvailableEnchantmentResults(cost, s, source)
	if len(enchantments) == 0 {
		return results
	}
	if pick, ok := weightedRandomEnchant(random, enchantments); ok {
		results = append(results, pick)
	}
	for int(random.nextIntN(50)) <= cost {
		if len(results) > 0 {
			enchantments = filterCompatibleEnchantments(enchantments, results[len(results)-1])
		}
		if len(enchantments) == 0 {
			break
		}
		if pick, ok := weightedRandomEnchant(random, enchantments); ok {
			results = append(results, pick)
		}
		cost /= 2
	}
	return results
}

// getAvailableEnchantmentResults ports EnchantmentHelper.getAvailableEnchantmentResults(value,
// itemStack, source): for each candidate enchantment where isPrimaryItem(itemStack) || isBook, take
// the HIGHEST level (maxLevel..minLevel) whose [minCost, maxCost] band contains `value`, adding one
// EnchantmentInstance for it. CITE EnchantmentHelper.getAvailableEnchantmentResults.
func getAvailableEnchantmentResults(value int, s component.SlotData, source []string) []enchantInstance {
	results := []enchantInstance{}
	isBook := int32(s.ItemID) == itemNameToID("book")
	for _, id := range source {
		if !(enchantIsPrimaryItem(id, s) || isBook) {
			continue
		}
		maxLvl := enchantMaxLevelOf(id)
		for level := maxLvl; level >= 1; level-- { // getMinLevel() == 1
			if value < enchantMinCostOf(id, level) || value > enchantMaxCostOf(id, level) {
				continue
			}
			results = append(results, enchantInstance{id: id, level: level})
			break
		}
	}
	return results
}

// filterCompatibleEnchantments ports EnchantmentHelper.filterCompatibleEnchantments: remove every
// candidate incompatible with `target`. Returns the filtered slice (a fresh slice, preserving order).
func filterCompatibleEnchantments(enchants []enchantInstance, target enchantInstance) []enchantInstance {
	out := enchants[:0:0]
	for _, e := range enchants {
		if enchantAreCompatible(target.id, e.id) {
			out = append(out, e)
		}
	}
	return out
}

// weightedRandomEnchant ports WeightedRandom.getRandomItem(random, items, weight): total = sum of
// weights; if total == 0 -> none; selection = random.nextInt(total); walk subtracting weights until
// negative. CITE WeightedRandom.getRandomItem + getWeightedItem.
func weightedRandomEnchant(random *legacyRandom, items []enchantInstance) (enchantInstance, bool) {
	total := 0
	for _, e := range items {
		total += enchantWeightOf(e.id)
	}
	if total <= 0 {
		return enchantInstance{}, false
	}
	sel := int(random.nextIntN(int32(total)))
	for _, e := range items {
		sel -= enchantWeightOf(e.id)
		if sel < 0 {
			return e, true
		}
	}
	return enchantInstance{}, false
}

// inEnchantingTableSource returns the SORTED candidate enchantment id list for the enchanting table
// (the IN_ENCHANTING_TABLE tag, flattened). The sort gives a deterministic holderset iteration order
// (the vanilla registry holderset order is itself fixed; sorting is the faithful stand-in the loot
// enchant path already uses — level/loot/enchant.go resolveEnchantTag). CITE EnchantmentTags.
// IN_ENCHANTING_TABLE + EnchantmentMenu.getEnchantmentList's ((HolderSet.Named)tag).stream().
func inEnchantingTableSource() []string {
	members, err := registrydata.EnchantmentTagMembers("in_enchanting_table")
	if err != nil {
		return nil
	}
	sort.Strings(members)
	return members
}

// ---- Mth helpers --------------------------------------------------------------------------------

// mthRoundToInt ports Math.round(float) -> int: floor(f + 0.5).
func mthRoundToInt(f float32) int {
	return int(mthFloorF32(f + 0.5))
}
