package advancement

// parse.go — the JSON -> model decoder for one advancement file. It mirrors the
// vanilla Advancement.CODEC / DisplayInfo.CODEC field names + defaults (frame
// defaults to "task", count defaults to 1, sends_telemetry_event defaults to
// false). Only the fields that cross the network (parent/display/requirements/
// telemetry) plus the wired-trigger conditions (trigger id + inventory item ids)
// are decoded; unmodeled trigger conditions are ignored.

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
		if c.Trigger == "minecraft:inventory_changed" && len(c.Conditions) > 0 {
			var ic jsonInvChanged
			if err := json.Unmarshal(c.Conditions, &ic); err == nil {
				for _, it := range ic.Items {
					// The "items" field is either a single id string or a list.
					var single string
					if err := json.Unmarshal(it.Items, &single); err == nil {
						cr.Items = append(cr.Items, single)
						continue
					}
					var many []string
					if err := json.Unmarshal(it.Items, &many); err == nil {
						cr.Items = append(cr.Items, many...)
					}
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
