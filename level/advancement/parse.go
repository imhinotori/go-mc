package advancement

// parse.go — the JSON -> model decoder for one advancement file. It mirrors the
// vanilla Advancement.CODEC / DisplayInfo.CODEC field names + defaults (frame
// defaults to "task", count defaults to 1, sends_telemetry_event defaults to
// false). Only the fields that cross the network (parent/display/requirements/
// telemetry) plus the wired-trigger conditions (trigger id + the item ids, entity
// types, block ids, and dimension keys the wired triggers read) are decoded;
// unmodeled trigger conditions are ignored.

import (
	"encoding/json"
	"fmt"
)

// jsonAdvancement mirrors the on-disk advancement JSON shape.
type jsonAdvancement struct {
	Parent       string                     `json:"parent"`
	Display      *jsonDisplay               `json:"display"`
	Criteria     map[string]jsonCriterion   `json:"criteria"`
	Requirements [][]string                 `json:"requirements"`
	Telemetry    bool                       `json:"sends_telemetry_event"`
}

type jsonDisplay struct {
	Title       json.RawMessage `json:"title"`
	Description json.RawMessage `json:"description"`
	Icon        jsonIcon        `json:"icon"`
	Frame       string          `json:"frame"`
	Background  string          `json:"background"`
	ShowToast   *bool           `json:"show_toast"`
	Hidden      *bool           `json:"hidden"`
}

type jsonIcon struct {
	ID    string `json:"id"`
	Count *int   `json:"count"`
}

type jsonCriterion struct {
	Trigger    string          `json:"trigger"`
	Conditions json.RawMessage `json:"conditions"`
}

// jsonTranslatable extracts the "translate" key from a title/description
// component. Every vanilla advancement title/description is {"translate": key},
// so we keep only that; a raw-string or {"text":...} form falls back to "".
type jsonTranslatable struct {
	Translate string `json:"translate"`
}

// jsonInvChanged is the minecraft:inventory_changed condition shape (partial):
// items is a list of ItemPredicate, each with an "items" field that is a single
// id string or a #tag reference.
type jsonInvChanged struct {
	Items []struct {
		Items json.RawMessage `json:"items"`
	} `json:"items"`
}

// jsonItemCond is the minecraft:consume_item / minecraft:fishing_rod_hooked condition
// shape (partial): a SINGLE "item" ItemPredicate whose "items" field is a single id
// string or a #tag reference. Mirrors ConsumeItemTrigger.TriggerInstance.item /
// FishingRodHookedTrigger.TriggerInstance.item (Optional<ItemPredicate>).
type jsonItemCond struct {
	Item *struct {
		Items json.RawMessage `json:"items"`
	} `json:"item"`
}

// jsonKilledCond is the minecraft:player_killed_entity condition shape (partial): an
// "entity" list of EntityPredicate wrappers, each an entity_properties condition with
// a "predicate" carrying "minecraft:entity_type" (the killed entity's type id).
// Mirrors KilledTrigger.TriggerInstance.entityPredicate -> ContextAwarePredicate ->
// EntityPredicate.entityType. Only the single-id entity_type form the vanilla tree
// uses (kill_a_mob et al.) is parsed; a tag/other-condition form yields no EntityTypes
// (the criterion becomes wildcard for that entry -- documented).
type jsonKilledCond struct {
	Entity []struct {
		Predicate *struct {
			EntityType json.RawMessage `json:"minecraft:entity_type"`
		} `json:"predicate"`
	} `json:"entity"`
}

// jsonPlacedCond is the minecraft:placed_block condition shape (partial): a "location"
// list of LocationPredicate wrappers, each a block_state_property condition carrying a
// "block" id. Mirrors ItemUsedOnLocationTrigger.TriggerInstance.location ->
// LootItemBlockStatePropertyCondition.block (the placed block's id).
type jsonPlacedCond struct {
	Location []struct {
		Block string `json:"block"`
	} `json:"location"`
}

// jsonChangedDim is the minecraft:changed_dimension condition shape: the optional "to"
// and "from" dimension keys. Mirrors ChangeDimensionTrigger.TriggerInstance to/from
// Optional<ResourceKey<Level>>.
type jsonChangedDim struct {
	To   string `json:"to"`
	From string `json:"from"`
}

// itemsField decodes an ItemPredicate "items" field that is either a single id string
// or a list of id strings, appending each to out.
func itemsField(raw json.RawMessage, out []string) []string {
	if len(raw) == 0 {
		return out
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return append(out, single)
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return append(out, many...)
	}
	return out
}

// parseAdvancement decodes one advancement file into the model.
func parseAdvancement(id string, b []byte) (Advancement, error) {
	var j jsonAdvancement
	if err := json.Unmarshal(b, &j); err != nil {
		return Advancement{}, fmt.Errorf("json: %w", err)
	}
	a := Advancement{
		ID:           id,
		Parent:       j.Parent,
		Requirements: j.Requirements,
		Telemetry:    j.Telemetry,
		Criteria:     make(map[string]Criterion, len(j.Criteria)),
	}
	for name, c := range j.Criteria {
		cr := Criterion{Trigger: c.Trigger}
		if len(c.Conditions) > 0 {
			switch c.Trigger {
			case "minecraft:inventory_changed":
				// InventoryChangeTrigger: a list of ItemPredicate under "items".
				var ic jsonInvChanged
				if err := json.Unmarshal(c.Conditions, &ic); err == nil {
					for _, it := range ic.Items {
						cr.Items = itemsField(it.Items, cr.Items)
					}
				}
			case "minecraft:consume_item", "minecraft:fishing_rod_hooked":
				// ConsumeItemTrigger / FishingRodHookedTrigger: a SINGLE "item" ItemPredicate.
				var ic jsonItemCond
				if err := json.Unmarshal(c.Conditions, &ic); err == nil && ic.Item != nil {
					cr.Items = itemsField(ic.Item.Items, cr.Items)
				}
			case "minecraft:player_killed_entity":
				// KilledTrigger: entity_properties -> minecraft:entity_type (single id).
				var kc jsonKilledCond
				if err := json.Unmarshal(c.Conditions, &kc); err == nil {
					for _, e := range kc.Entity {
						if e.Predicate == nil {
							continue
						}
						var single string
						if err := json.Unmarshal(e.Predicate.EntityType, &single); err == nil && single != "" {
							cr.EntityTypes = append(cr.EntityTypes, single)
						}
					}
				}
			case "minecraft:placed_block":
				// ItemUsedOnLocationTrigger: location -> block_state_property.block (single id).
				var pc jsonPlacedCond
				if err := json.Unmarshal(c.Conditions, &pc); err == nil {
					for _, l := range pc.Location {
						if l.Block != "" {
							cr.Blocks = append(cr.Blocks, l.Block)
						}
					}
				}
			case "minecraft:changed_dimension":
				// ChangeDimensionTrigger: optional to/from dimension keys.
				var cd jsonChangedDim
				if err := json.Unmarshal(c.Conditions, &cd); err == nil {
					cr.DimTo = cd.To
					cr.DimFrom = cd.From
				}
			}
		}
		a.Criteria[name] = cr
	}
	if j.Display != nil {
		a.HasDisplay = true
		count := 1
		if j.Display.Icon.Count != nil {
			count = *j.Display.Icon.Count
		}
		showToast := true
		if j.Display.ShowToast != nil {
			showToast = *j.Display.ShowToast
		}
		hidden := false
		if j.Display.Hidden != nil {
			hidden = *j.Display.Hidden
		}
		a.Display = Display{
			Title:       translateKey(j.Display.Title),
			Description: translateKey(j.Display.Description),
			Icon:        Icon{ItemID: j.Display.Icon.ID, Count: count},
			Frame:       TypeFromString(j.Display.Frame),
			Background:  j.Display.Background,
			ShowToast:   showToast,
			Hidden:      hidden,
		}
	}
	return a, nil
}

// translateKey pulls the "translate" key from a component raw message. A missing
// key returns "" (rendered as an empty component — never a parse failure).
func translateKey(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var t jsonTranslatable
	if err := json.Unmarshal(raw, &t); err == nil {
		return t.Translate
	}
	return ""
}
