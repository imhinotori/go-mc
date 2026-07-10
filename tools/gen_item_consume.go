// gen_item_consume generates data/item/consume.go from items.json.
//
// It emits the CONSUMABLE default-component data the eating/drinking chain needs beyond the
// bare FOOD nutrition/saturation (which gen_item_food already emits): the item
// on_consume_effects list (net.minecraft.world.item.component.Consumable.onConsumeEffects) and
// the OminousBottleAmplifier ConsumableListener value.
//
// The report (items.json) is the authoritative source (same rationale as gen_item_food: item
// component maps are not reflectively bound after Bootstrap, so the --all data-generator report
// is the correct source). Each ConsumeEffect record is emitted as a plain-data ConsumeEffect
// struct the server iterates in Consumable.onConsume order.
//
// Cited: net.minecraft.world.item.component.Consumable.onConsume iterates ConsumableListeners
// (FoodProperties.onConsume + OminousBottleAmplifier.onConsume) then, server-side, forEach
// onConsumeEffect.apply. The ConsumeEffect subtypes:
//   - minecraft:apply_effects   -> ApplyStatusEffectsConsumeEffect (effects list + probability)
//   - minecraft:remove_effects  -> RemoveStatusEffectsConsumeEffect (effect id set)
//   - minecraft:clear_all_effects -> ClearAllStatusEffectsConsumeEffect
//   - minecraft:teleport_randomly -> TeleportRandomlyConsumeEffect (diameter, default 16.0f)
//   - minecraft:play_sound      -> PlaySoundConsumeEffect (sound id; v1 no-op)
package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
)

// teleportDefaultDiameter is TeleportRandomlyConsumeEffect.DEFAULT_DIAMETER (== 16.0f; the no-arg
// ctor does init(16.0f), verified via javap). The report omits diameter when it equals the
// default, so a teleport_randomly with no explicit diameter uses 16.0f.
const teleportDefaultDiameter = 16.0

type itemConsumeReport map[string]struct {
	Components struct {
		Food *struct {
			CanAlwaysEat bool `json:"can_always_eat"`
		} `json:"minecraft:food"`
		Consumable *struct {
			ConsumeSeconds   *float64            `json:"consume_seconds"`
			OnConsumeEffects []consumeEffectJSON `json:"on_consume_effects"`
		} `json:"minecraft:consumable"`
		OminousBottleAmplifier *int `json:"minecraft:ominous_bottle_amplifier"`
	} `json:"components"`
}

type consumeEffectJSON struct {
	Type        string          `json:"type"`
	Effects     json.RawMessage `json:"effects"`
	Probability *float64        `json:"probability"`
	Diameter    *float64        `json:"diameter"`
	Sound       string          `json:"sound"`
}

type mobEffectInstanceJSON struct {
	ID            string `json:"id"`
	Amplifier     int    `json:"amplifier"`
	Duration      int    `json:"duration"`
	Ambient       bool   `json:"ambient"`
	ShowParticles *bool  `json:"show_particles"`
	ShowIcon      *bool  `json:"show_icon"`
}

type itemConsumeRow struct {
	Key                    string
	HasFood                bool
	CanAlwaysEat           bool
	ConsumeSeconds         float64
	Effects                []consumeEffectRow
	OminousBottleAmplifier *int
}

type consumeEffectRow struct {
	Kind        string
	ApplyList   []mobEffectInstanceRow
	Probability float64
	RemoveIDs   []string
	Diameter    float64
	Sound       string
}

type mobEffectInstanceRow struct {
	ID        string
	Amplifier int
	Duration  int
	Ambient   bool
	Visible   bool
	ShowIcon  bool
}

// genItemConsume reads items.json and emits data/item/consume.go.
func genItemConsume(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "items.json")
	out := filepath.Join(goMCRoot, "data", "item", "consume.go")

	var report itemConsumeReport
	if err := readJSON(jsonPath, &report); err != nil {
		return fmt.Errorf("genItemConsume: %w", err)
	}

	var rows []itemConsumeRow
	for key, item := range report {
		c := item.Components.Consumable
		amp := item.Components.OminousBottleAmplifier
		if c == nil && amp == nil {
			continue
		}
		// Consumable.consumeSeconds(): explicit if present, else DEFAULT_CONSUME_SECONDS (1.6f). The
		// report omits it when it equals the default. A no-consumable item that only carries the ominous
		// listener still uses the 1.6f default (OminousBottleItem is a plain Consumable-less path? no --
		// ominous_bottle DOES carry a consumable, so c!=nil there; the 1.6 fallback is the safety net).
		consumeSeconds := 1.6
		if c != nil && c.ConsumeSeconds != nil {
			consumeSeconds = *c.ConsumeSeconds
		}
		row := itemConsumeRow{
			Key:                    key,
			HasFood:                item.Components.Food != nil,
			CanAlwaysEat:           item.Components.Food != nil && item.Components.Food.CanAlwaysEat,
			ConsumeSeconds:         consumeSeconds,
			OminousBottleAmplifier: amp,
		}
		if c != nil {
			for _, e := range c.OnConsumeEffects {
				er, err := normalizeConsumeEffect(e)
				if err != nil {
					return fmt.Errorf("genItemConsume: %s: %w", key, err)
				}
				row.Effects = append(row.Effects, er)
			}
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_item_consume.go", "items.json"))
	buf.WriteByte('\n')
	buf.WriteString("package item\n\n")
	buf.WriteString(consumeHeaderDoc)

	buf.WriteString("var Consumable = map[string]ItemConsumable{\n")
	for _, r := range rows {
		fmt.Fprintf(&buf, "\t%q: {\n", r.Key)
		fmt.Fprintf(&buf, "\t\tHasFood: %t, CanAlwaysEat: %t, ConsumeSeconds: %s,\n", r.HasFood, r.CanAlwaysEat, goFloat32(r.ConsumeSeconds))
		if r.OminousBottleAmplifier != nil {
			fmt.Fprintf(&buf, "\t\tOminousBottleAmplifier: intPtr(%d),\n", *r.OminousBottleAmplifier)
		}
		if len(r.Effects) > 0 {
			buf.WriteString("\t\tOnConsumeEffects: []ConsumeEffect{\n")
			for _, e := range r.Effects {
				writeConsumeEffect(&buf, e)
			}
			buf.WriteString("\t\t},\n")
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genItemConsume: gofmt: %w\n%s", err, buf.String())
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genItemConsume: %w", err)
	}
	logf("genItemConsume: wrote %s (%d consumables)", out, len(rows))
	return nil
}

func writeConsumeEffect(buf *strings.Builder, e consumeEffectRow) {
	switch e.Kind {
	case "apply":
		fmt.Fprintf(buf, "\t\t\t{Kind: ConsumeApplyEffects, Probability: %s, ApplyEffects: []EffectInstance{\n", goFloat32(e.Probability))
		for _, m := range e.ApplyList {
			fmt.Fprintf(buf, "\t\t\t\t{ID: %q, Amplifier: %d, Duration: %d, Ambient: %t, Visible: %t, ShowIcon: %t},\n",
				m.ID, m.Amplifier, m.Duration, m.Ambient, m.Visible, m.ShowIcon)
		}
		buf.WriteString("\t\t\t}},\n")
	case "remove":
		buf.WriteString("\t\t\t{Kind: ConsumeRemoveEffects, RemoveEffects: []string{")
		for i, id := range e.RemoveIDs {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(buf, "%q", id)
		}
		buf.WriteString("}},\n")
	case "clear_all":
		buf.WriteString("\t\t\t{Kind: ConsumeClearAllEffects},\n")
	case "teleport":
		fmt.Fprintf(buf, "\t\t\t{Kind: ConsumeTeleportRandomly, Diameter: %s},\n", goFloat32(e.Diameter))
	case "play_sound":
		fmt.Fprintf(buf, "\t\t\t{Kind: ConsumePlaySound, Sound: %q},\n", e.Sound)
	}
}

// normalizeConsumeEffect turns one report on_consume_effects entry into a flat row, resolving the
// polymorphic effects field per type.
func normalizeConsumeEffect(e consumeEffectJSON) (consumeEffectRow, error) {
	switch e.Type {
	case "minecraft:apply_effects":
		// probability defaults to 1.0 (ApplyStatusEffectsConsumeEffect DEFAULT_PROBABILITY == 1.0f).
		prob := 1.0
		if e.Probability != nil {
			prob = *e.Probability
		}
		var list []mobEffectInstanceJSON
		if err := json.Unmarshal(e.Effects, &list); err != nil {
			return consumeEffectRow{}, fmt.Errorf("apply_effects effects: %w", err)
		}
		var out []mobEffectInstanceRow
		for _, m := range list {
			// MobEffectInstance defaults: ambient=false, visible=true, showIcon=true (the report
			// omits show_particles/show_icon when they equal the codec default true).
			visible := true
			if m.ShowParticles != nil {
				visible = *m.ShowParticles
			}
			showIcon := true
			if m.ShowIcon != nil {
				showIcon = *m.ShowIcon
			}
			out = append(out, mobEffectInstanceRow{
				ID:        m.ID,
				Amplifier: m.Amplifier,
				Duration:  m.Duration,
				Ambient:   m.Ambient,
				Visible:   visible,
				ShowIcon:  showIcon,
			})
		}
		return consumeEffectRow{Kind: "apply", ApplyList: out, Probability: prob}, nil
	case "minecraft:remove_effects":
		ids, err := parseEffectIDs(e.Effects)
		if err != nil {
			return consumeEffectRow{}, fmt.Errorf("remove_effects effects: %w", err)
		}
		return consumeEffectRow{Kind: "remove", RemoveIDs: ids}, nil
	case "minecraft:clear_all_effects":
		return consumeEffectRow{Kind: "clear_all"}, nil
	case "minecraft:teleport_randomly":
		diam := teleportDefaultDiameter
		if e.Diameter != nil {
			diam = *e.Diameter
		}
		return consumeEffectRow{Kind: "teleport", Diameter: diam}, nil
	case "minecraft:play_sound":
		return consumeEffectRow{Kind: "play_sound", Sound: e.Sound}, nil
	}
	return consumeEffectRow{}, fmt.Errorf("unknown consume effect type %q", e.Type)
}

// parseEffectIDs resolves a remove_effects effects HolderSet, which in the report is either a
// single id string (minecraft:poison) or a list of id strings. Tag references (leading #) are not
// used by any vanilla remove_effects entry, so they are rejected rather than silently expanded.
func parseEffectIDs(raw json.RawMessage) ([]string, error) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if strings.HasPrefix(single, "#") {
			return nil, fmt.Errorf("tag-form remove_effects %q not supported", single)
		}
		return []string{single}, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	return nil, fmt.Errorf("effects is neither a string nor a string list")
}

const consumeHeaderDoc = `// ItemConsumable carries an item CONSUMABLE default-component data beyond the bare FOOD
// nutrition/saturation (data/item/food.go): the Consumable.onConsumeEffects list applied in
// Consumable.onConsume, and the OminousBottleAmplifier ConsumableListener value (bad_omen).
// Keyed by the item resource id ("minecraft:golden_apple"). An item present in this table is
// consumable (usable via right-click) even if it carries no FOOD component (milk_bucket,
// ominous_bottle). See server.finishUsingItem.
type ItemConsumable struct {
	// HasFood reports whether the item carries a FOOD component (FoodData.eat runs only then).
	HasFood bool
	// CanAlwaysEat is FoodProperties.canAlwaysEat() (false when HasFood is false). Consumable.canConsume
	// -> Player.canEat(canAlwaysEat): a canAlwaysEat item (or a no-food consumable) can be used at full hunger.
	CanAlwaysEat bool
	// ConsumeSeconds is Consumable.consumeSeconds() (DEFAULT_CONSUME_SECONDS 1.6f unless overridden;
	// honey_bottle 2.0f). consumeTicks = (int)(ConsumeSeconds * 20.0f).
	ConsumeSeconds float32
	OnConsumeEffects []ConsumeEffect
	// OminousBottleAmplifier is the OminousBottleAmplifier component value (nil when absent). When
	// set, Consumable.onConsume ConsumableListener adds MobEffects.BAD_OMEN (duration 120000,
	// amplifier == this value, ambient=false, visible=false, showIcon=true).
	OminousBottleAmplifier *int
}

// ConsumeEffectKind discriminates a ConsumeEffect (net.minecraft.world.item.consume_effects.*).
type ConsumeEffectKind int

const (
	ConsumeApplyEffects     ConsumeEffectKind = iota // ApplyStatusEffectsConsumeEffect
	ConsumeRemoveEffects                             // RemoveStatusEffectsConsumeEffect
	ConsumeClearAllEffects                           // ClearAllStatusEffectsConsumeEffect
	ConsumeTeleportRandomly                          // TeleportRandomlyConsumeEffect
	ConsumePlaySound                                 // PlaySoundConsumeEffect
)

// ConsumeEffect is one flattened Consumable.onConsumeEffects entry (a ConsumeEffect record).
type ConsumeEffect struct {
	Kind ConsumeEffectKind
	// ApplyEffects / Probability: ConsumeApplyEffects. Probability is the ApplyStatusEffects
	// DEFAULT_PROBABILITY 1.0f unless overridden (chicken 0.3, poisonous_potato 0.6, rotten_flesh 0.8).
	ApplyEffects []EffectInstance
	Probability  float32
	// RemoveEffects: ConsumeRemoveEffects (the effect ids to remove; honey_bottle -> poison).
	RemoveEffects []string
	// Diameter: ConsumeTeleportRandomly (DEFAULT_DIAMETER 16.0f).
	Diameter float32
	// Sound: ConsumePlaySound (the sound id; server-side v1 no-op).
	Sound string
}

// EffectInstance is a MobEffectInstance carried by an ApplyStatusEffectsConsumeEffect.
type EffectInstance struct {
	ID        string
	Amplifier int
	Duration  int
	Ambient   bool
	Visible   bool
	ShowIcon  bool
}

func intPtr(v int) *int { return &v }

`
