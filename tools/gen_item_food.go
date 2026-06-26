// gen_item_food generates data/item/food.go from items.json.
//
// items.json is the authoritative MC --all data-generator report (one entry per item, each
// carrying its default DataComponentMap). For every item that has a "minecraft:food"
// component this generator emits the FoodProperties (nutrition / saturation / canAlwaysEat)
// and the Consumable.consumeSeconds — the hardcoded-in-Item-constructor inputs to the eating
// chain (net.minecraft.world.food.FoodData.eat(FoodProperties) via Consumable.onConsume →
// FoodProperties.onConsume).
//
// We read the report (NOT a separate reflective extractor) because the report's serializer is
// the authoritative codec: it already applies the FOOD/CONSUMABLE default-omission semantics
// (it omits consume_seconds when it equals Consumable.DEFAULT_CONSUME_SECONDS == 1.6f). A
// reflective Item.components() extractor cannot run here — item component maps are not bound
// after Bootstrap.bootStrap() ("Components not bound yet"), so the report is the correct source.
//
// The generated table is a map[string]ItemFood keyed by the item resource id
// ("minecraft:cooked_beef"), so a numeric item id -> Item.Name -> "minecraft:<name>" lookup
// resolves each food's components for the server-authoritative eat port. saturation is the
// ABSOLUTE value (FoodData.eat(FoodProperties) calls add(nutrition, saturation) directly, NOT
// FoodConstants.saturationByModifier) and consumeSeconds*20 (f2i) is the use-duration in ticks
// (Consumable.consumeTicks).
package main

import (
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
)

// defaultConsumeSeconds is net.minecraft.world.item.component.Consumable.DEFAULT_CONSUME_SECONDS
// (== 1.6f, verified via javap on Consumable$Builder's ctor: `ldc 1.6f; putfield consumeSeconds`).
// The --all report OMITS consume_seconds when it equals this default, so a food item whose
// consumable carries no explicit consume_seconds (or has no consumable at all) uses 1.6f.
const defaultConsumeSeconds = 1.6

// itemFoodReport is the slice of items.json fields gen_item_food needs: the FOOD and
// CONSUMABLE components off each item's default component map. Fields absent in the report
// decode to their zero value; *float64 distinguishes "omitted" (nil -> default) from a real 0.
type itemFoodReport map[string]struct {
	Components struct {
		Food *struct {
			Nutrition    int32   `json:"nutrition"`
			Saturation   float64 `json:"saturation"`
			CanAlwaysEat bool    `json:"can_always_eat"`
		} `json:"minecraft:food"`
		Consumable *struct {
			ConsumeSeconds *float64 `json:"consume_seconds"`
		} `json:"minecraft:consumable"`
	} `json:"components"`
}

// itemFoodRow is one emitted row of the generated table.
type itemFoodRow struct {
	Key            string
	Nutrition      int32
	Saturation     float64
	CanAlwaysEat   bool
	ConsumeSeconds float64
}

// genItemFood reads items.json and emits data/item/food.go.
func genItemFood(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "items.json")
	out := filepath.Join(goMCRoot, "data", "item", "food.go")

	var report itemFoodReport
	if err := readJSON(jsonPath, &report); err != nil {
		return fmt.Errorf("genItemFood: %w", err)
	}

	var rows []itemFoodRow
	for key, item := range report {
		food := item.Components.Food
		if food == nil {
			continue // not a food item — skipped (v1 only handles eating)
		}
		// Consumable.consumeSeconds(): explicit if present in the report, else the 1.6f default
		// (the report omits it when it equals DEFAULT_CONSUME_SECONDS, and food items without an
		// explicit consumable also use the default).
		consumeSeconds := defaultConsumeSeconds
		if c := item.Components.Consumable; c != nil && c.ConsumeSeconds != nil {
			consumeSeconds = *c.ConsumeSeconds
		}
		rows = append(rows, itemFoodRow{
			Key:            key,
			Nutrition:      food.Nutrition,
			Saturation:     food.Saturation,
			CanAlwaysEat:   food.CanAlwaysEat,
			ConsumeSeconds: consumeSeconds,
		})
	}

	// Deterministic output: sort by key so the generated file is reproducible regardless of map
	// iteration order.
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_item_food.go", "items.json"))
	buf.WriteByte('\n')
	buf.WriteString("package item\n\n")

	buf.WriteString(`// ItemFood carries an item's FOOD + CONSUMABLE default-component data, taken from the item's
// default DataComponentMap (FoodProperties.nutrition()/saturation()/canAlwaysEat() +
// Consumable.consumeSeconds()). Saturation is the ABSOLUTE saturation value
// (net.minecraft.world.food.FoodData.eat(FoodProperties) calls add(nutrition, saturation)
// directly — NOT FoodConstants.saturationByModifier, so do NOT double it). ConsumeSeconds*20.0f
// (f2i) is the use-duration in ticks (net.minecraft.world.item.component.Consumable.consumeTicks).
type ItemFood struct {
	Nutrition      int32
	Saturation     float32
	CanAlwaysEat   bool
	ConsumeSeconds float32
}

// Food maps an item resource id (e.g. "minecraft:cooked_beef") to its FOOD/CONSUMABLE data.
// Only items that carry a FOOD component appear here (non-food items are absent, so a missing
// lookup means "not eatable"). Keyed by the registry resource string so a numeric item id ->
// Item.Name -> "minecraft:<name>" lookup resolves it (see server.itemFood).
var Food = map[string]ItemFood{
`)

	// Align the values for a readable generated table.
	maxKeyLen := 0
	for _, r := range rows {
		if l := len(r.Key) + 2; l > maxKeyLen { // +2 for the surrounding quotes
			maxKeyLen = l
		}
	}

	for _, r := range rows {
		quoted := fmt.Sprintf("%q", r.Key)
		pad := strings.Repeat(" ", maxKeyLen-len(quoted))
		fmt.Fprintf(&buf, "\t%s:%s {Nutrition: %d, Saturation: %s, CanAlwaysEat: %t, ConsumeSeconds: %s},\n",
			quoted, pad, r.Nutrition, goFloat32(r.Saturation), r.CanAlwaysEat, goFloat32(r.ConsumeSeconds))
	}
	buf.WriteString("}\n")

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genItemFood: gofmt: %w", err)
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genItemFood: %w", err)
	}
	logf("genItemFood: wrote %s (%d food items)", out, len(rows))
	return nil
}
