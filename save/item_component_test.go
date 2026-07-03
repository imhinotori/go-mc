package save

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/registrydata"
)

// item_component_test.go — SUB-ITEMNBT Phase B: the wire↔disk component transcoder round-trip and
// jar-shape assertions. Verifies the DISK NBT tag names/shapes match DataComponentPatch.CODEC +
// each component's DATA codec, and that a supported component round-trips byte-stable while an
// unsupported one keeps the counted-drop behavior.

// buildWireSpan encodes an added-component list (typeId + streamCodec body per entry) into the raw
// wire span the way component.SlotData carries it, returning the span + its added count. Mirrors the
// SlotData.WriteTo layout (added bodies, no removed).
func buildWireSpan(t *testing.T, comps ...component.DataComponent) ([]byte, int) {
	t.Helper()
	var buf bytes.Buffer
	for _, c := range comps {
		id := componentWireID(t, c)
		if _, err := pk.VarInt(id).WriteTo(&buf); err != nil {
			t.Fatalf("write typeId: %v", err)
		}
		if _, err := c.WriteTo(&buf); err != nil {
			t.Fatalf("write component %T: %v", c, err)
		}
	}
	return buf.Bytes(), len(comps)
}

// componentWireID resolves a component's wire type id by scanning NewComponent (the same registry the
// transcoder uses). Kept test-local so the test asserts against the real registry, not a hardcode.
func componentWireID(t *testing.T, c component.DataComponent) int32 {
	t.Helper()
	for id := int32(0); id < 256; id++ {
		if p := component.NewComponent(id); p != nil && p.ID() == c.ID() {
			return id
		}
	}
	t.Fatalf("no wire id for component %s", c.ID())
	return -1
}

// TestTranscode_DamageAndCustomName_DiskShape proves a stack carrying minecraft:damage (Int) and
// minecraft:custom_name (Component) transcodes to the exact DataComponentPatch disk shape: a
// "components" compound keyed by the registry id strings, damage a TAG_Int, custom_name a Component
// NBT. CITE: DataComponentPatch.CODEC / PatchKey (id.toString() keys) + DataComponents.DAMAGE
// (NON_NEGATIVE_INT -> Int) + DataComponents.CUSTOM_NAME (ComponentSerialization.CODEC).
func TestTranscode_DamageAndCustomName_DiskShape(t *testing.T) {
	dmg := &component.Damage{VarInt: 42}
	name := &component.CustomName{Name: chat.Message{Text: "Excalibur"}}
	span, added := buildWireSpan(t, dmg, name)

	in := make([]DiskItem, 1)
	in[0] = DiskItem{
		ID:             "minecraft:diamond_sword",
		Count:          1,
		WireComponents: span,
		WireAddedCount: added,
	}
	items, dropped := SaveAllItems(in, false)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0 (both supported)", dropped)
	}
	if len(items) != 1 || items[0].Components == nil {
		t.Fatalf("expected 1 item with a components compound, got %+v", items)
	}

	// Encode to bytes and decode into a generic shape to assert the exact disk keys + tag types.
	raw, err := encodeItemsList(items)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var generic struct {
		Items []struct {
			Components map[string]nbt.RawMessage `nbt:"components"`
		} `nbt:"Items"`
	}
	if _, err := nbt.NewDecoder(bytes.NewReader(raw)).Decode(&generic); err != nil {
		t.Fatalf("decode generic: %v", err)
	}
	comps := generic.Items[0].Components
	dv, ok := comps["minecraft:damage"]
	if !ok {
		t.Fatalf("missing minecraft:damage key; keys=%v", keysOf(comps))
	}
	if dv.Type != nbt.TagInt {
		t.Errorf("damage tag type = %d, want TagInt(%d)", dv.Type, nbt.TagInt)
	}
	nv, ok := comps["minecraft:custom_name"]
	if !ok {
		t.Fatalf("missing minecraft:custom_name key; keys=%v", keysOf(comps))
	}
	// A text-only Component serializes as a bare TAG_String (chat.Message.TagType), matching vanilla.
	if nv.Type != nbt.TagString {
		t.Errorf("custom_name tag type = %d, want TagString(%d)", nv.Type, nbt.TagString)
	}
}

// TestTranscode_RoundTripByteStable proves the supported set round-trips byte-identical: the wire span
// saved to disk and loaded back yields the SAME wire component bytes (added count + raw span). This is
// the faithful-inverse guarantee (item_components.go header). Covers damage, max_damage, repair_cost,
// custom_name, lore.
func TestTranscode_RoundTripByteStable(t *testing.T) {
	span, added := buildWireSpan(t,
		&component.Damage{VarInt: 7},
		&component.MaxDamage{VarInt: 1561},
		&component.RepairCost{VarInt: 3},
		&component.CustomName{Name: chat.Message{Text: "Named"}},
		&component.Lore{Lines: []chat.Message{{Text: "line one"}, {Text: "line two"}}},
	)

	in := []DiskItem{{ID: "minecraft:diamond_pickaxe", Count: 1, WireComponents: span, WireAddedCount: added}}
	items, dropped := SaveAllItems(in, false)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}

	raw, err := encodeItemsList(items)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := decodeItemsList(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := LoadAllItems(decoded, 1)
	if len(out) != 1 {
		t.Fatalf("loaded = %d, want 1", len(out))
	}
	got := out[0]
	if got.WireAddedCount != added {
		t.Errorf("added count = %d, want %d", got.WireAddedCount, added)
	}
	if got.WireRemovedCount != 0 {
		t.Errorf("removed count = %d, want 0", got.WireRemovedCount)
	}
	// Re-parse both spans into the same component set and compare; the wire byte order of a
	// DataComponentPatch map is not guaranteed stable across a Go-map round-trip, so compare by
	// decoding each span's components rather than raw bytes.
	assertSpanEqual(t, span, added, got.WireComponents, got.WireAddedCount)
}

// TestTranscode_UnsupportedCounted proves an UNSUPPORTED component (e.g. minecraft:unbreakable, no DATA
// transcode here) is COUNTED as dropped while the supported companion still round-trips — the residual
// gap is metered, never silent (item_components.go SEAM).
func TestTranscode_UnsupportedCounted(t *testing.T) {
	// Unbreakable (wire id 4) has no DATA transcode in the supported set. It MUST be decodable on the
	// wire (NewComponent(4) != nil) so the reader can advance past it to the supported damage after it.
	unbreak := component.NewComponent(4) // *Unbreakable
	if unbreak == nil {
		t.Fatal("NewComponent(4) nil; expected *Unbreakable")
	}
	// Order matters: put the supported (damage) FIRST so the unsupported one after it is still
	// reached (an unknown-LENGTH component would stop the scan, but Unbreakable has a known body).
	span, added := buildWireSpan(t, &component.Damage{VarInt: 5}, unbreak)

	in := []DiskItem{{ID: "minecraft:diamond_sword", Count: 1, WireComponents: span, WireAddedCount: added}}
	items, dropped := SaveAllItems(in, false)
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1 (the unsupported unbreakable)", dropped)
	}
	if items[0].Components == nil {
		t.Fatal("expected damage to still transcode to a components compound")
	}
	if _, ok := (*items[0].Components)["minecraft:damage"]; !ok {
		t.Errorf("supported damage missing from disk; keys=%v", keysOf(*items[0].Components))
	}
	if _, ok := (*items[0].Components)["minecraft:unbreakable"]; ok {
		t.Errorf("unsupported unbreakable should NOT be on disk")
	}
}

// TestTranscode_PotionContents_RoundTrip proves minecraft:potion_contents round-trips: a bottle whose
// only field is the potion Holder serializes to the vanilla bare-string alternative (Potion.CODEC),
// and a bottle with custom_effects serializes to the FULL_CODEC compound with the "custom_effects"
// list of MobEffectInstance. CITE: PotionContents.CODEC = withAlternative(FULL_CODEC, Potion.CODEC).
func TestTranscode_PotionContents_RoundTrip(t *testing.T) {
	healingID := potionID("minecraft:healing")
	if healingID < 0 {
		t.Fatal("healing potion not in registry")
	}
	// (a) potion-only bottle -> bare TAG_String alternative.
	pcOnly := &component.PotionContents{}
	pcOnly.PotionID.Has = true
	pcOnly.PotionID.Val = pk.VarInt(healingID)
	span, added := buildWireSpan(t, pcOnly)
	items, dropped := SaveAllItems([]DiskItem{{ID: "minecraft:potion", Count: 1, WireComponents: span, WireAddedCount: added}}, false)
	if dropped != 0 || items[0].Components == nil {
		t.Fatalf("potion-only: dropped=%d components=%v", dropped, items[0].Components)
	}
	pv := (*items[0].Components)["minecraft:potion_contents"]
	if pv.Type != nbt.TagString {
		t.Errorf("potion-only disk shape = tag %d, want bare TAG_String(%d) (withAlternative)", pv.Type, nbt.TagString)
	}
	assertPotionRoundTrip(t, span, added)

	// (b) bottle with a custom effect -> FULL_CODEC compound with a custom_effects list.
	regenEffectID := mobEffectID("minecraft:regeneration")
	if regenEffectID < 0 {
		t.Fatal("regeneration effect not in registry")
	}
	pcFull := &component.PotionContents{}
	pcFull.PotionID.Has = true
	pcFull.PotionID.Val = pk.VarInt(healingID)
	pcFull.CustomEffects = []component.ItemPotionEffect{{
		ID: pk.VarInt(regenEffectID),
		Details: component.ItemEffectDetail{
			Amplifier:     1,
			Duration:      600,
			Ambient:       false,
			ShowParticles: true,
			ShowIcon:      true,
		},
	}}
	span2, added2 := buildWireSpan(t, pcFull)
	items2, dropped2 := SaveAllItems([]DiskItem{{ID: "minecraft:potion", Count: 1, WireComponents: span2, WireAddedCount: added2}}, false)
	if dropped2 != 0 || items2[0].Components == nil {
		t.Fatalf("full potion: dropped=%d", dropped2)
	}
	pv2 := (*items2[0].Components)["minecraft:potion_contents"]
	if pv2.Type != nbt.TagCompound {
		t.Fatalf("full potion disk shape = tag %d, want TAG_Compound(%d)", pv2.Type, nbt.TagCompound)
	}
	var pcDisk struct {
		Potion        string `nbt:"potion"`
		CustomEffects []struct {
			ID       string `nbt:"id"`
			Duration int32  `nbt:"duration"`
		} `nbt:"custom_effects"`
	}
	if err := pv2.Unmarshal(&pcDisk); err != nil {
		t.Fatalf("unmarshal potion disk: %v", err)
	}
	if pcDisk.Potion != "minecraft:healing" {
		t.Errorf("potion key = %q, want minecraft:healing", pcDisk.Potion)
	}
	if len(pcDisk.CustomEffects) != 1 || pcDisk.CustomEffects[0].ID != "minecraft:regeneration" {
		t.Errorf("custom_effects = %+v, want one minecraft:regeneration", pcDisk.CustomEffects)
	}
	if pcDisk.CustomEffects[0].Duration != 600 {
		t.Errorf("effect duration = %d, want 600", pcDisk.CustomEffects[0].Duration)
	}
	assertPotionRoundTrip(t, span2, added2)
}

// TestTranscode_PotionEffectDefaultOmission proves the potion custom_effects Details compound applies
// vanilla optionalFieldOf omission EXACTLY: show_particles is OMITTED when true (its default), show_icon
// is ALWAYS written, and amplifier/duration/ambient are omitted at their zero defaults. CITE:
// MobEffectInstance.Details.MAP_CODEC.
func TestTranscode_PotionEffectDefaultOmission(t *testing.T) {
	healingID := potionID("minecraft:healing")
	regenID := mobEffectID("minecraft:regeneration")
	pc := &component.PotionContents{}
	pc.PotionID.Has = true
	pc.PotionID.Val = pk.VarInt(healingID)
	pc.CustomEffects = []component.ItemPotionEffect{{
		ID: pk.VarInt(regenID),
		Details: component.ItemEffectDetail{
			Amplifier:     0,    // default -> omitted
			Duration:      0,    // default -> omitted
			Ambient:       false, // default -> omitted
			ShowParticles: true, // default TRUE -> omitted
			ShowIcon:      true, // always written
		},
	}}
	span, added := buildWireSpan(t, pc)
	items, _ := SaveAllItems([]DiskItem{{ID: "minecraft:potion", Count: 1, WireComponents: span, WireAddedCount: added}}, false)
	pv := (*items[0].Components)["minecraft:potion_contents"]

	var disk struct {
		CustomEffects []map[string]nbt.RawMessage `nbt:"custom_effects"`
	}
	if err := pv.Unmarshal(&disk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(disk.CustomEffects) != 1 {
		t.Fatalf("custom_effects = %d, want 1", len(disk.CustomEffects))
	}
	eff := disk.CustomEffects[0]
	if _, ok := eff["show_particles"]; ok {
		t.Errorf("show_particles present but should be OMITTED at its true default")
	}
	if _, ok := eff["show_icon"]; !ok {
		t.Errorf("show_icon MUST always be written")
	}
	if _, ok := eff["amplifier"]; ok {
		t.Errorf("amplifier present but should be omitted at 0")
	}
	if _, ok := eff["duration"]; ok {
		t.Errorf("duration present but should be omitted at 0")
	}
	if _, ok := eff["ambient"]; ok {
		t.Errorf("ambient present but should be omitted at false")
	}
	if _, ok := eff["id"]; !ok {
		t.Errorf("id MUST be present")
	}
	// And it must still round-trip byte-stable back to the wire.
	assertPotionRoundTrip(t, span, added)
}

// TestTranscode_RemovedComponent proves a REMOVED component (SlotData removed list) transcodes to the
// "!"+id key with an empty-compound value and round-trips back to a removed wire entry. CITE:
// PatchKey (REMOVED_PREFIX "!") + valueCodec() removed = Codec.EMPTY.
func TestTranscode_RemovedComponent(t *testing.T) {
	// Build a span with 0 added and 1 removed (damage removed).
	var buf bytes.Buffer
	dmgID := componentWireID(t, &component.Damage{})
	if _, err := pk.VarInt(dmgID).WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	in := []DiskItem{{ID: "minecraft:diamond_sword", Count: 1, WireComponents: buf.Bytes(), WireAddedCount: 0, WireRemovedCount: 1}}
	items, dropped := SaveAllItems(in, false)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0 (removals carry no value)", dropped)
	}
	if items[0].Components == nil {
		t.Fatal("expected a components compound with the removal")
	}
	rv, ok := (*items[0].Components)["!minecraft:damage"]
	if !ok {
		t.Fatalf("missing !minecraft:damage removal key; keys=%v", keysOf(*items[0].Components))
	}
	if rv.Type != nbt.TagCompound {
		t.Errorf("removal value tag = %d, want TAG_Compound(%d) (Codec.EMPTY {})", rv.Type, nbt.TagCompound)
	}
	// Round-trip: load back and confirm a removed entry (added 0, removed 1).
	raw, _ := encodeItemsList(items)
	decoded, _ := decodeItemsList(raw)
	out := LoadAllItems(decoded, 1)
	if out[0].WireRemovedCount != 1 || out[0].WireAddedCount != 0 {
		t.Errorf("round-trip counts = added %d removed %d, want added 0 removed 1", out[0].WireAddedCount, out[0].WireRemovedCount)
	}
}

// TestTranscode_PotionRegistryIndex sanity-checks the static registry index==protocol-id assumption
// the potion/mob_effect resolvers rely on (data/registryid).
func TestTranscode_PotionRegistryIndex(t *testing.T) {
	if len(registryid.Potion) == 0 || len(registryid.MobEffect) == 0 {
		t.Fatal("empty potion/mobeffect registry")
	}
	if got := potionName(potionID("minecraft:healing")); got != "minecraft:healing" {
		t.Errorf("potion round-trip name = %q", got)
	}
	if got := mobEffectName(mobEffectID("minecraft:speed")); got != "minecraft:speed" {
		t.Errorf("mob effect round-trip name = %q", got)
	}
}

// ---- helpers ------------------------------------------------------------------------------------

func keysOf(m map[string]nbt.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// assertSpanEqual decodes two wire spans into their component sets and asserts equality by re-encoding
// each component deterministically (avoids Go-map iteration-order noise in the raw byte comparison).
func assertSpanEqual(t *testing.T, spanA []byte, addedA int, spanB []byte, addedB int) {
	t.Helper()
	if addedA != addedB {
		t.Fatalf("added counts differ: %d vs %d", addedA, addedB)
	}
	a := decodeSpanToMap(t, spanA, addedA)
	b := decodeSpanToMap(t, spanB, addedB)
	if len(a) != len(b) {
		t.Fatalf("component counts differ: %d vs %d", len(a), len(b))
	}
	for id, ba := range a {
		bb, ok := b[id]
		if !ok {
			t.Errorf("component %s missing after round-trip", id)
			continue
		}
		if !bytes.Equal(ba, bb) {
			t.Errorf("component %s body differs: %x vs %x", id, ba, bb)
		}
	}
}

// decodeSpanToMap parses an added-only span into id->body bytes.
func decodeSpanToMap(t *testing.T, span []byte, added int) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	r := bytes.NewReader(span)
	for i := 0; i < added; i++ {
		var typeID pk.VarInt
		if _, err := typeID.ReadFrom(r); err != nil {
			t.Fatalf("read typeId: %v", err)
		}
		comp := component.NewComponent(int32(typeID))
		if comp == nil {
			t.Fatalf("unknown component id %d", int32(typeID))
		}
		var body bytes.Buffer
		// Re-read the body then re-encode it to normalize (WriteTo is deterministic per component).
		if _, err := comp.ReadFrom(r); err != nil {
			t.Fatalf("read component %d: %v", int32(typeID), err)
		}
		if _, err := comp.WriteTo(&body); err != nil {
			t.Fatalf("write component %d: %v", int32(typeID), err)
		}
		out[comp.ID()] = body.Bytes()
	}
	return out
}

func assertPotionRoundTrip(t *testing.T, span []byte, added int) {
	t.Helper()
	in := []DiskItem{{ID: "minecraft:potion", Count: 1, WireComponents: span, WireAddedCount: added}}
	items, _ := SaveAllItems(in, false)
	raw, _ := encodeItemsList(items)
	decoded, _ := decodeItemsList(raw)
	out := LoadAllItems(decoded, 1)
	assertSpanEqual(t, span, added, out[0].WireComponents, out[0].WireAddedCount)
}

// ---- enchantment transcode (SUB-ITEMNBT enchantment resolver) -----------------------------------

// armEnchantmentRegistry injects the ordered ENCHANTMENT resource-id list (the same embedded content
// the server sends to the client) and returns the wire numeric id for a resource id so the test can
// build a wire span WITHOUT hardcoding the datapack index. It restores an EMPTY resolver on cleanup so
// a test that relies on the DEFERRED fallback is not polluted by injection order.
func armEnchantmentRegistry(t *testing.T) []string {
	t.Helper()
	order, err := registrydata.EnchantmentOrder()
	if err != nil {
		t.Fatalf("EnchantmentOrder: %v", err)
	}
	if len(order) == 0 {
		t.Fatal("empty enchantment registry order")
	}
	SetEnchantmentRegistry(order)
	t.Cleanup(func() { SetEnchantmentRegistry(nil) })
	return order
}

// enchantWireID resolves an enchantment resource id to its wire/protocol id via the injected resolver
// (index in the ordered list). Fails the test on an unknown id.
func enchantWireID(t *testing.T, order []string, id string) int32 {
	t.Helper()
	for i, n := range order {
		if n == id {
			return int32(i)
		}
	}
	t.Fatalf("enchantment %q not in registry order", id)
	return -1
}

// TestTranscode_Enchantments_DiskShape proves an item carrying minecraft:enchantments (sharpness 3 +
// unbreaking 2) transcodes to the exact ItemEnchantments.CODEC disk shape — a bare compound keyed by
// the enchantment RESOURCE ID with a TAG_Int level (1..255), no wrapper, no show_in_tooltip — and
// round-trips byte-stable to the identical wire numeric ids. CITE: ItemEnchantments.CODEC =
// unboundedMap(Enchantment.CODEC (resource id), Codec.intRange(1,255)); STREAM_CODEC = VarInt(id,level).
func TestTranscode_Enchantments_DiskShape(t *testing.T) {
	order := armEnchantmentRegistry(t)
	sharpID := enchantWireID(t, order, "minecraft:sharpness")
	unbrID := enchantWireID(t, order, "minecraft:unbreaking")

	ench := &component.Enchantments{Enchantments: []component.EnchantmentEntry{
		{ID: pk.VarInt(sharpID), Level: 3},
		{ID: pk.VarInt(unbrID), Level: 2},
	}}
	// buildWireSpan re-derives the component's wire typeId via NewComponent, so the span is exactly
	// what SlotData would carry for this component.
	span, added := buildWireSpan(t, ench)

	in := []DiskItem{{ID: "minecraft:diamond_sword", Count: 1, WireComponents: span, WireAddedCount: added}}
	items, dropped := SaveAllItems(in, false)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0 (enchantments now supported with the resolver armed)", dropped)
	}
	if len(items) != 1 || items[0].Components == nil {
		t.Fatalf("expected 1 item with a components compound, got %+v", items)
	}

	raw, err := encodeItemsList(items)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// Assert the exact disk shape: components -> "minecraft:enchantments" -> compound{ id_string: Int }.
	var generic struct {
		Items []struct {
			Components map[string]nbt.RawMessage `nbt:"components"`
		} `nbt:"Items"`
	}
	if _, err := nbt.NewDecoder(bytes.NewReader(raw)).Decode(&generic); err != nil {
		t.Fatalf("decode generic: %v", err)
	}
	comps := generic.Items[0].Components
	ev, ok := comps["minecraft:enchantments"]
	if !ok {
		t.Fatalf("missing minecraft:enchantments key; keys=%v", keysOf(comps))
	}
	if ev.Type != nbt.TagCompound {
		t.Fatalf("enchantments value tag = %d, want TAG_Compound(%d) (unboundedMap)", ev.Type, nbt.TagCompound)
	}
	// The compound must be keyed by RESOURCE IDs with TAG_Int levels — NOT by numeric ids, and with
	// NO wrapper compound / show_in_tooltip (ItemEnchantments.CODEC in 26.2).
	var levels map[string]nbt.RawMessage
	if err := ev.Unmarshal(&levels); err != nil {
		t.Fatalf("unmarshal enchantment map: %v", err)
	}
	if len(levels) != 2 {
		t.Fatalf("enchantment map has %d keys, want 2; keys=%v", len(levels), keysOf(levels))
	}
	for _, key := range []string{"minecraft:sharpness", "minecraft:unbreaking"} {
		lv, ok := levels[key]
		if !ok {
			t.Errorf("missing enchantment key %q; keys=%v", key, keysOf(levels))
			continue
		}
		if lv.Type != nbt.TagInt {
			t.Errorf("level for %q tag = %d, want TAG_Int(%d)", key, lv.Type, nbt.TagInt)
		}
	}
	// No accidental show_in_tooltip / levels wrapper leaked into the compound.
	if _, bad := levels["show_in_tooltip"]; bad {
		t.Error("ItemEnchantments.CODEC (26.2) has no show_in_tooltip field; must not be written")
	}
	if _, bad := levels["levels"]; bad {
		t.Error("ItemEnchantments.CODEC (26.2) is a bare map; must not have a 'levels' wrapper key")
	}

	// Round-trip: load back and confirm the identical wire numeric ids + levels.
	decoded, err := decodeItemsList(raw)
	if err != nil {
		t.Fatalf("decode items: %v", err)
	}
	out := LoadAllItems(decoded, 1)
	assertSpanEqual(t, span, added, out[0].WireComponents, out[0].WireAddedCount)
}

// TestTranscode_StoredEnchantments_RoundTrip proves minecraft:stored_enchantments (the enchanted-book
// component, wire typeId 42) transcodes through the SAME ItemEnchantments.CODEC shape and round-trips
// byte-stable. CITE: DataComponents.STORED_ENCHANTMENTS = ItemEnchantments.CODEC.
func TestTranscode_StoredEnchantments_RoundTrip(t *testing.T) {
	order := armEnchantmentRegistry(t)
	mendID := enchantWireID(t, order, "minecraft:mending")

	stored := &component.StoredEnchantments{Enchantments: []component.EnchantmentEntry{
		{ID: pk.VarInt(mendID), Level: 1},
	}}
	span, added := buildWireSpan(t, stored)

	in := []DiskItem{{ID: "minecraft:enchanted_book", Count: 1, WireComponents: span, WireAddedCount: added}}
	items, dropped := SaveAllItems(in, false)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	sv, ok := (*items[0].Components)["minecraft:stored_enchantments"]
	if !ok {
		t.Fatalf("missing minecraft:stored_enchantments key; keys=%v", keysOf(*items[0].Components))
	}
	if sv.Type != nbt.TagCompound {
		t.Fatalf("stored_enchantments value tag = %d, want TAG_Compound(%d)", sv.Type, nbt.TagCompound)
	}
	raw, _ := encodeItemsList(items)
	decoded, _ := decodeItemsList(raw)
	out := LoadAllItems(decoded, 1)
	assertSpanEqual(t, span, added, out[0].WireComponents, out[0].WireAddedCount)
}

// TestTranscode_Enchantments_DeferredWhenNoResolver proves the DEFERRED counted-drop fallback is
// preserved when the enchantment resolver is NOT injected: an enchantments component is counted as
// dropped (never fabricated) rather than written with a made-up id. This guards the datapack-registry
// 1:1 mandate for any code path that runs without a wired registry.
func TestTranscode_Enchantments_DeferredWhenNoResolver(t *testing.T) {
	SetEnchantmentRegistry(nil) // explicitly empty resolver
	t.Cleanup(func() { SetEnchantmentRegistry(nil) })

	ench := &component.Enchantments{Enchantments: []component.EnchantmentEntry{
		{ID: 0, Level: 1}, // any id — with no resolver it cannot resolve to a resource id
	}}
	span, added := buildWireSpan(t, ench)
	in := []DiskItem{{ID: "minecraft:diamond_sword", Count: 1, WireComponents: span, WireAddedCount: added}}
	items, dropped := SaveAllItems(in, false)
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1 (no resolver -> counted-drop, never fabricated)", dropped)
	}
	// The dropped component must NOT appear on disk.
	if len(items) == 1 && items[0].Components != nil {
		if _, present := (*items[0].Components)["minecraft:enchantments"]; present {
			t.Error("enchantments must not be written to disk without a resolver (would be a fabricated id)")
		}
	}
}

// TestEnchantmentRegistry_RoundTrip sanity-checks the injected resolver: name->id->name is stable and
// index == wire id (mirrors TestTranscode_PotionRegistryIndex for the datapack enchantment registry).
func TestEnchantmentRegistry_RoundTrip(t *testing.T) {
	order := armEnchantmentRegistry(t)
	for _, id := range []string{"minecraft:sharpness", "minecraft:mending", order[0], order[len(order)-1]} {
		wire := enchantmentID(id)
		if wire < 0 {
			t.Errorf("enchantmentID(%q) = -1, want >= 0", id)
			continue
		}
		if got := enchantmentName(wire); got != id {
			t.Errorf("round-trip name for %q via id %d = %q", id, wire, got)
		}
		if int(wire) >= len(order) || order[wire] != id {
			t.Errorf("wire id %d for %q is not its index in the registry order", wire, id)
		}
	}
	// An out-of-range id and an unknown name resolve to the empty/-1 sentinels.
	if got := enchantmentName(int32(len(order))); got != "" {
		t.Errorf("out-of-range enchantmentName = %q, want \"\"", got)
	}
	if got := enchantmentID("minecraft:not_an_enchantment"); got != -1 {
		t.Errorf("unknown enchantmentID = %d, want -1", got)
	}
}
