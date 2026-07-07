// gen_item_attribute_modifiers generates data/item/attributemodifiers.go from items.json.
//
// items.json is the authoritative MC --all data-generator report (one entry per item, each
// carrying its default DataComponentMap). For every item that has a
// "minecraft:attribute_modifiers" component this generator emits the ItemAttributeModifiers
// entry list — the inputs to net.minecraft.world.item.ItemStack.forEachModifier(slot, consumer)
// (component ItemAttributeModifiers.forEach filters each entry by
// entry.slot().test(equipmentSlot) and hands (attribute, AttributeModifier) to the consumer).
//
// We read the report (NOT a separate reflective extractor) because the report's serializer is
// the authoritative codec (ItemAttributeModifiers.CODEC): each entry serializes the attribute
// type id, the modifier id, amount (double), operation (AttributeModifier$Operation), and the
// EquipmentSlotGroup key. The optional "display" sub-object is a TOOLTIP-only concern
// (ItemAttributeModifiers$Display — how the line renders client-side) and carries zero gameplay
// data, so it is intentionally not emitted.
//
// The generated table is a map[string][]ItemAttributeModifier keyed by the item resource id
// ("minecraft:diamond_sword"), so a numeric item id -> Item.Name -> "minecraft:<name>" lookup
// resolves each item's default attribute modifiers for the server-authoritative equipment port
// (LivingEntity.collectEquipmentChanges).
package main

import (
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// goFloat64 renders a double as a Go float64 literal with full round-trip precision (shortest
// representation that parses back to the exact bits — strconv 'g' precision -1). The report's
// amounts include float-widened doubles (e.g. -2.4000000953674316, knockback_resistance
// 0.10000000149011612) that %g would truncate, so precision matters here.
func goFloat64(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0" // make whole numbers obviously floats (e.g. "6" -> "6.0")
	}
	return s
}

// itemAttrModsReport is the slice of items.json fields this generator needs: the
// ATTRIBUTE_MODIFIERS component off each item's default component map. The component's report
// form is a plain JSON list of entries (the ItemAttributeModifiers list codec).
type itemAttrModsReport map[string]struct {
	Components struct {
		AttributeModifiers []struct {
			Type      string  `json:"type"`      // attribute resource id, e.g. "minecraft:attack_damage"
			ID        string  `json:"id"`        // modifier resource id, e.g. "minecraft:base_attack_damage"
			Amount    float64 `json:"amount"`    // AttributeModifier.amount (double)
			Operation string  `json:"operation"` // "add_value" | "add_multiplied_base" | "add_multiplied_total"
			Slot      string  `json:"slot"`      // EquipmentSlotGroup key, e.g. "mainhand", "head", "body"
		} `json:"minecraft:attribute_modifiers"`
	} `json:"components"`
}

// operationID maps the AttributeModifier$Operation serialized key to its enum id
// (ADD_VALUE=0, ADD_MULTIPLIED_BASE=1, ADD_MULTIPLIED_TOTAL=2 — the `id` field of the enum,
// verified javap AttributeModifier$Operation.<clinit>).
var operationID = map[string]int{
	"add_value":            0,
	"add_multiplied_base":  1,
	"add_multiplied_total": 2,
}

// genItemAttributeModifiers reads items.json and emits data/item/attributemodifiers.go.
func genItemAttributeModifiers(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "items.json")
	out := filepath.Join(goMCRoot, "data", "item", "attributemodifiers.go")

	var report itemAttrModsReport
	if err := readJSON(jsonPath, &report); err != nil {
		return fmt.Errorf("genItemAttributeModifiers: %w", err)
	}

	// Deterministic output: sort item keys so the generated file is reproducible regardless of
	// map iteration order. Entry order WITHIN an item is preserved from the report (the codec's
	// list order).
	var keys []string
	for key, item := range report {
		if len(item.Components.AttributeModifiers) == 0 {
			continue // no default attribute modifiers — absent from the table
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_item_attribute_modifiers.go", "items.json"))
	buf.WriteByte('\n')
	buf.WriteString("package item\n\n")

	buf.WriteString(`// ItemAttributeModifier is one entry of an item's default minecraft:attribute_modifiers
// component (net.minecraft.world.item.component.ItemAttributeModifiers$Entry): the attribute it
// targets, the AttributeModifier record (id, amount, operation) and the EquipmentSlotGroup the
// entry applies in. ItemStack.forEachModifier(slot, consumer) visits each entry whose
// Slot group test accepts the equipment slot. The tooltip-only "display" sub-object is omitted
// (zero gameplay data).
type ItemAttributeModifier struct {
	// Attribute is the target attribute's resource id (e.g. "minecraft:attack_damage").
	Attribute string
	// ID is the modifier's stable identity (e.g. "minecraft:base_attack_damage") — the
	// AttributeInstance modifier-map key.
	ID string
	// Amount is AttributeModifier.amount (a double; meaning depends on Operation).
	Amount float64
	// Operation is the AttributeModifier$Operation enum id: 0 ADD_VALUE, 1 ADD_MULTIPLIED_BASE,
	// 2 ADD_MULTIPLIED_TOTAL.
	Operation int32
	// Slot is the EquipmentSlotGroup serialized key ("any", "mainhand", "offhand", "hand",
	// "feet", "legs", "chest", "head", "armor", "body", "saddle").
	Slot string
}

// AttributeModifiers maps an item resource id (e.g. "minecraft:diamond_sword") to its default
// attribute-modifier entries. Only items that carry an ATTRIBUTE_MODIFIERS component appear here
// (a missing lookup means "no default modifiers" — ItemAttributeModifiers.EMPTY). Keyed by the
// registry resource string so a numeric item id -> Item.Name -> "minecraft:<name>" lookup
// resolves it (see server.itemAttributeModifiers).
var AttributeModifiers = map[string][]ItemAttributeModifier{
`)

	total := 0
	for _, key := range keys {
		fmt.Fprintf(&buf, "\t%q: {\n", key)
		for _, m := range report[key].Components.AttributeModifiers {
			op, ok := operationID[m.Operation]
			if !ok {
				return fmt.Errorf("genItemAttributeModifiers: %s: unknown operation %q", key, m.Operation)
			}
			fmt.Fprintf(&buf, "\t\t{Attribute: %q, ID: %q, Amount: %s, Operation: %d, Slot: %q},\n",
				m.Type, m.ID, goFloat64(m.Amount), op, m.Slot)
			total++
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genItemAttributeModifiers: gofmt: %w", err)
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genItemAttributeModifiers: %w", err)
	}
	logf("genItemAttributeModifiers: wrote %s (%d items, %d entries)", out, len(keys), total)
	return nil
}
