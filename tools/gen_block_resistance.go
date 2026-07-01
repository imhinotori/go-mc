// gen_block_resistance generates level/block/resistance.go from block_resistance.json.
//
// block_resistance.json comes from the GenBlockResistance.java extractor: one row per
// registered Block carrying its Block.getExplosionResistance() (the BlockBehaviour.Properties
// explosionResistance field). That field is HARDCODED in Java Block constructors, so — like
// destroyTime (see gen_block_hardness.go) — it is NOT present in the --all blocks.json report
// and must be reflected off each registered Block.
//
// The generated table is a map[string]float32 keyed by Block.ID() ("minecraft:stone"), so a
// state -> Block.ID() -> resistance lookup drives the 1:1 port of
// net.minecraft.world.level.ExplosionDamageCalculator.getBlockExplosionResistance (the block
// half; the port max's it with the fluid resistance separately).
package main

import (
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
)

// resistanceRow is one entry of block_resistance.json (see GenBlockResistance.java).
type resistanceRow struct {
	Key        string  `json:"key"`        // "minecraft:stone"
	Resistance float64 `json:"resistance"` // Block.getExplosionResistance()
}

// genBlockResistance reads block_resistance.json and emits level/block/resistance.go.
func genBlockResistance(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "block_resistance.json")
	out := filepath.Join(goMCRoot, "level", "block", "resistance.go")

	var rows []resistanceRow
	if err := readJSON(jsonPath, &rows); err != nil {
		return fmt.Errorf("genBlockResistance: %w", err)
	}

	// Deterministic output: sort by key (the extractor already sorts, but make the Go side
	// independent of input order so the generated file is reproducible).
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_block_resistance.go", "block_resistance.json"))
	buf.WriteByte('\n')
	buf.WriteString("package block\n\n")

	buf.WriteString(`// ExplosionResistance maps a block resource id (Block.ID(), e.g. "minecraft:stone") to its
// vanilla Block.getExplosionResistance() (the BlockBehaviour explosionResistance field). Keyed
// by the per-block id (NOT per-state) because Block.getExplosionResistance returns the constant
// explosionResistance for every state of a block in vanilla 26.2. The explosion block-collection
// port (ServerExplosion.calculateExplodedPositions) reads this via
// ExplosionDamageCalculator.getBlockExplosionResistance, max'd with the fluid resistance.
var ExplosionResistance = map[string]float32{
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
		fmt.Fprintf(&buf, "\t%s:%s %s,\n", quoted, pad, goFloat32(r.Resistance))
	}
	buf.WriteString("}\n")

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genBlockResistance: gofmt: %w", err)
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genBlockResistance: %w", err)
	}
	logf("genBlockResistance: wrote %s (%d blocks)", out, len(rows))
	return nil
}
