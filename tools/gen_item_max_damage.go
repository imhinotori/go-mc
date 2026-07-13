// gen_item_max_damage generates data/item/maxdamage.go from per-item
// temp/cache/<version>-datagen/generated/reports/minecraft/components/item/*.json reports.
//
// The reports are the authoritative MC --all data-generator output for each item's default
// DataComponentMap (Item.components()). Every item that carries a "minecraft:max_damage" component
// reports its DEFAULT max durability value here — the value net.minecraft.world.item.ItemStack.
// getMaxDamage() resolves via PatchedDataComponentMap.get(MAX_DAMAGE) overlaid on Item.components(),
// i.e. the per-item DEFAULT, falling back to the absent-component 0 only when the item has no
// max_damage component at all.
//
// We read the per-item REPORTS (NOT items.json) because:
//  1. The reports are exactly what the data-generator emits per item — byte-faithful to the
//     registered Item.components() defaults. items.json collapses many items into one line and
//     matches the same schema, but the per-item reports are the canonical --all output.
//  2. Reading every per-item report gives a faithful map keyed by the item resource id
//     ("minecraft:diamond_pickaxe") without depending on items.json's top-level key set.
//
// The generated table is a map[string]int keyed by the item resource id, so a numeric item id ->
// Item.Name -> "minecraft:<name>" lookup resolves each item's default max durability for the
// server-authoritative ItemStack.getMaxDamage() port. Only items that carry minecraft:max_damage
// appear here (a missing lookup means "no max_damage component" -> vanilla's 0 default).
package main

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// itemReportMaxDamage is the slice of per-item report fields this generator needs: only the
// minecraft:max_damage component off the item's default component map. The report's top-level shape
// is {"components": {<component name>: <component value>}}; the max_damage component serializes as
// a bare integer (DataComponents.MAX_DAMAGE = ExtraCodecs.POSITIVE_INT).
type itemReportMaxDamage struct {
	Components struct {
		MaxDamage *int `json:"minecraft:max_damage"`
	} `json:"components"`
}

// genItemMaxDamage reads every per-item component report and emits data/item/maxdamage.go.
//
// The reports live in temp/cache/<version>-datagen/generated/reports/minecraft/components/item/*.json
// (the --all data-generator output; this is the path the data-generator Docker run deposits them at).
// The generator reads each file in sorted order for a deterministic, reproducible build.
func genItemMaxDamage(jsonDir, goMCRoot string) error {
	// jsonDir is the version directory (e.g. "26.2"); the datagen output lives next to it under
	// temp/cache/<version>-datagen/generated/reports/minecraft/components/item/. The path is
	// derived from jsonDir rather than the legacy jsonDir-relative "reports/" layout because the
	// datagen pipeline writes its report tree outside temp/jsons.
	jsonVersion := filepath.Base(jsonDir)
	reportsDir := filepath.Join(goMCRoot, "temp", "cache", jsonVersion+"-datagen", "generated", "reports", "minecraft", "components", "item")
	out := filepath.Join(goMCRoot, "data", "item", "maxdamage.go")

	entries, err := os.ReadDir(reportsDir)
	if err != nil {
		return fmt.Errorf("genItemMaxDamage: reading %s: %w", reportsDir, err)
	}

	// Read every per-item report; keep only items whose component map carries minecraft:max_damage.
	// Sort by item key (the filename minus .json) for a deterministic generated table.
	var rows []struct {
		Key       string
		MaxDamage int
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// The report filenames are the unprefixed item ids ("diamond_pickaxe.json"); prefix with
		// "minecraft:" so the map keys match every other generated registry-keyed table (Food,
		// AttributeModifiers, etc.) — and resolve via Item.Name + "minecraft:" in the runtime.
		key := "minecraft:" + strings.TrimSuffix(name, ".json")
		var rep itemReportMaxDamage
		if err := readJSON(filepath.Join(reportsDir, name), &rep); err != nil {
			return fmt.Errorf("genItemMaxDamage: parsing %s: %w", name, err)
		}
		if rep.Components.MaxDamage == nil {
			continue // no minecraft:max_damage component -> absent from the table
		}
		rows = append(rows, struct {
			Key       string
			MaxDamage int
		}{Key: key, MaxDamage: *rep.Components.MaxDamage})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_item_max_damage.go", "reports/minecraft/components/item/*.json"))
	buf.WriteByte('\n')
	buf.WriteString("package item\n\n")

	buf.WriteString(`// DefaultMaxDamage maps an item resource id (e.g. "minecraft:diamond_pickaxe") to its DEFAULT
// minecraft:max_damage component value — the DataComponents.MAX_DAMAGE the item carries at
// registration (vanilla Item.components()). ItemStack.getMaxDamage() resolves via
// PatchedDataComponentMap.get(MAX_DAMAGE) overlaid on this default, then falls back to 0 when the
// item has no max_damage component. Only items that carry minecraft:max_damage appear here; an
// item absent from the map has no max_damage component (and so reads 0 unless a client patch
// explicitly sets one).
var DefaultMaxDamage = map[string]int{
`)

	for _, r := range rows {
		fmt.Fprintf(&buf, "\t%q: %s,\n", r.Key, strconv.Itoa(r.MaxDamage))
	}
	buf.WriteString("}\n")

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genItemMaxDamage: gofmt: %w", err)
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genItemMaxDamage: %w", err)
	}
	logf("genItemMaxDamage: wrote %s (%d items)", out, len(rows))
	return nil
}
