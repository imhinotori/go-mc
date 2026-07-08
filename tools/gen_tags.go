// gen_tags generates data/tag/tags.go from tags.json (the GenTags.java extractor output).
//
// tags.json carries the recursively-flattened damage-type and item tag membership (GenTags.java
// resolves nested '#'-refs via net.minecraft.tags.TagLoader.build semantics before emitting it),
// plus the full damage_type registry element set.
//
// This generator emits the data/tag package read by the mob damage pipeline so
// damageSource.is("bypasses_armor") / .is("panic_causes") are GENUINE jar-data reads, NOT
// const-false stubs (PITFALLS Pitfall 3 — the keystone deliverable for MOB-SUB-03).
//
// Id resolution:
//   - Item member ids resolve through registries.json's minecraft:item registry (protocol_id ==
//     the slot index, exactly as gen_registryid builds data/registryid/item.go). An item member
//     that is absent from the registry is skipped (defensive; the jar's item tags only reference
//     real items).
//   - Damage-type ids have NO entry in registries.json — minecraft:damage_type is a DYNAMIC
//     (datapack) registry, so its protocol ids are assigned at server load, not in the static
//     report. We therefore assign each damage-type element a deterministic id = its index in the
//     sorted element list (GenTags emits damage_type_elements sorted). The id is internal server
//     state (DamageSource is constructed server-side; the damage-type id is never a wire registry
//     id), so a stable generated ordering is faithful AND reproducible. The generated DamageTypeIDs
//     map + DamageTypeNames slice expose the name<->id mapping so consumers (and tests) never hard-
//     code magic numbers.
//
// FRONT-LOAD NOTE (CONTEXT Grey Area 1): ItemTags is GENERATED here but has NO consumer in
// Phase 29 — the consumer is Phase 32 (TemptGoal). This is intentional shared-tooling front-
// loading: the data is real and tested headless (data/tag/tags_test.go), NOT a built-but-unwired
// violation.
//
// BLOCK-TAG NOTE: the current GenTags.java extractor emits ONLY damage_type + item tags into
// tags.json -- it does NOT yet emit block tags. The BlockTags table in data/tag/tags.go (the
// recursively-flattened mineable/pickaxe|axe|shovel|hoe membership that the Tool component mining
// rules read via BlockState.is) is therefore maintained OUT-OF-BAND: extracted directly from the
// jar's data/minecraft/tags/block/mineable/*.json (recursively resolving nested '#'-refs, the same
// TagLoader.build semantics GenTags applies to item/damage_type). Until GenTags is extended to add
// a "block" section to tags.json, a plain regen of this file does NOT reproduce BlockTags -- the
// block-tag block must be re-appended from the jar. This is a documented follow-up, not a silent
// gap: the data is real, jar-derived, and tested (data/tag/tags_test.go).
package main

import (
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
)

// tagsReport is the GenTags.java output shape (tags.json).
type tagsReport struct {
	DamageType          map[string][]string `json:"damage_type"`
	Item                map[string][]string `json:"item"`
	DamageTypeElements  []string            `json:"damage_type_elements"`
}

// genTags reads tags.json + registries.json and emits data/tag/tags.go.
func genTags(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "tags.json")
	out := filepath.Join(goMCRoot, "data", "tag", "tags.go")

	var report tagsReport
	if err := readJSON(jsonPath, &report); err != nil {
		return fmt.Errorf("genTags: %w", err)
	}

	// --- damage-type id assignment (sorted element index) -----------------------------------
	dtElements := append([]string(nil), report.DamageTypeElements...)
	sort.Strings(dtElements)
	dtID := make(map[string]int32, len(dtElements)) // "minecraft:fall" -> id
	for i, name := range dtElements {
		dtID[name] = int32(i)
	}

	// --- item id resolution via registries.json (protocol_id == slot index) -----------------
	var registries registriesJSON
	if err := readJSON(filepath.Join(jsonDir, "registries.json"), &registries); err != nil {
		return fmt.Errorf("genTags: reading registries.json: %w", err)
	}
	itemReg, ok := registries["minecraft:item"]
	if !ok {
		return fmt.Errorf("genTags: registries.json has no minecraft:item registry")
	}
	itemID := make(map[string]int32, len(itemReg.Entries)) // "minecraft:carrot" -> protocol id
	for name, v := range itemReg.Entries {
		itemID[name] = int32(v.ProtocolID)
	}

	// --- build sorted membership rows for deterministic output ------------------------------
	damageRows := buildTagRows(report.DamageType, dtID)
	itemRows := buildTagRows(report.Item, itemID)

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_tags.go", "tags.json", "registries.json"))
	buf.WriteByte('\n')
	buf.WriteString("package tag\n\n")

	buf.WriteString(`// DamageTypeNames maps a generated damage-type id back to its registry resource id
// ("minecraft:fall"). The id is the element's index in the sorted damage_type registry element
// set — a deterministic, internal-only ordering (minecraft:damage_type is a dynamic registry with
// no static protocol id; the id is never a wire registry id, only server-side DamageSource state).
var DamageTypeNames = []string{
`)
	for _, name := range dtElements {
		fmt.Fprintf(&buf, "\t%q,\n", name)
	}
	buf.WriteString("}\n\n")

	buf.WriteString(`// DamageTypeIDs maps a damage-type registry resource id ("minecraft:fall") to its generated id.
// Consumers resolve a DamageSource's type by name; tests assert membership by name (no magic ids).
var DamageTypeIDs = map[string]int32{
`)
	for _, name := range dtElements {
		fmt.Fprintf(&buf, "\t%q: %d,\n", name, dtID[name])
	}
	buf.WriteString("}\n\n")

	buf.WriteString(`// DamageTypeTags maps a damage-type tag name (e.g. "bypasses_armor") to the SET of damage-type
// ids in it, recursively flattened (nested #-tag refs resolved at extraction time, like vanilla
// net.minecraft.tags.TagLoader.build). damageSource.is(tag) is DamageTypeTags[tag][typeID].
var DamageTypeTags = map[string]map[int32]bool{
`)
	writeTagSets(&buf, damageRows)
	buf.WriteString("}\n\n")

	buf.WriteString(`// ItemTags maps an item tag name (e.g. "pig_food") to the SET of item ids in it (recursively
// flattened, same as DamageTypeTags). GENERATED-BUT-NOT-YET-WIRED in Phase 29 — the consumer is
// Phase 32 (TemptGoal). Intentional shared-tooling front-load (CONTEXT Grey Area 1); the data is
// real and tested headless, not a built-but-unwired violation.
var ItemTags = map[string]map[int32]bool{
`)
	writeTagSets(&buf, itemRows)
	buf.WriteString("}\n")

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genTags: gofmt: %w", err)
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genTags: %w", err)
	}
	logf("genTags: wrote %s (%d damage_type tags, %d item tags, %d damage_type elements)",
		out, len(damageRows), len(itemRows), len(dtElements))
	return nil
}

// tagSetRow is one tag's resolved member id set.
type tagSetRow struct {
	Name string
	IDs  []int32
}

// buildTagRows resolves each tag's member resource-locations to numeric ids via idOf, dropping
// members that don't resolve (defensive), and returns rows sorted by tag name with sorted ids.
func buildTagRows(tags map[string][]string, idOf map[string]int32) []tagSetRow {
	rows := make([]tagSetRow, 0, len(tags))
	for name, members := range tags {
		seen := make(map[int32]bool, len(members))
		ids := make([]int32, 0, len(members))
		for _, m := range members {
			id, ok := idOf[m]
			if !ok {
				continue // unresolved member (not in the registry) — skip
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		rows = append(rows, tagSetRow{Name: name, IDs: ids})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// writeTagSets emits the "name": {id: true, ...} body for a map[string]map[int32]bool literal.
func writeTagSets(buf *strings.Builder, rows []tagSetRow) {
	for _, r := range rows {
		fmt.Fprintf(buf, "\t%q: {", r.Name)
		for i, id := range r.IDs {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(buf, "%d: true", id)
		}
		buf.WriteString("},\n")
	}
}
