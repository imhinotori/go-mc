// gen_block_hardness generates level/block/hardness.go from block_hardness.json.
//
// block_hardness.json comes from the GenBlockHardness.java extractor: one row per
// registered Block carrying its BlockBehaviour destroyTime (hardness) and the
// requiresCorrectToolForDrops flag — the two HARDCODED-in-Java-constructor inputs to
// net.minecraft.world.level.block.state.BlockBehaviour.getDestroyProgress that are NOT
// present in the --all blocks.json report.
//
// The generated table is a map[string]BlockHardness keyed by Block.ID() ("minecraft:stone"),
// so a state -> Block.ID() -> hardness lookup drives the server-authoritative dig-time port.
// -1.0 destroy_speed means unbreakable (bedrock, barrier, …).
package main

import (
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
)

// hardnessRow is one entry of block_hardness.json (see GenBlockHardness.java).
type hardnessRow struct {
	Key          string  `json:"key"`           // "minecraft:stone"
	DestroySpeed float64 `json:"destroy_speed"` // BlockBehaviour destroyTime; -1.0 == unbreakable
	RequiresTool bool    `json:"requires_tool"` // requiresCorrectToolForDrops()
}

// genBlockHardness reads block_hardness.json and emits level/block/hardness.go.
func genBlockHardness(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "block_hardness.json")
	out := filepath.Join(goMCRoot, "level", "block", "hardness.go")

	var rows []hardnessRow
	if err := readJSON(jsonPath, &rows); err != nil {
		return fmt.Errorf("genBlockHardness: %w", err)
	}

	// Deterministic output: sort by key (the extractor already sorts, but make the Go side
	// independent of input order so the generated file is reproducible).
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_block_hardness.go", "block_hardness.json"))
	buf.WriteByte('\n')
	buf.WriteString("package block\n\n")

	buf.WriteString(`// BlockHardness carries the two BlockBehaviour break-time inputs reflected off each
// block's defaultBlockState() (BlockBehaviour.getDestroySpeed / requiresCorrectToolForDrops).
// DestroySpeed is the vanilla destroyTime (a.k.a. hardness); a value of -1.0 marks an
// unbreakable block (bedrock, barrier, …). RequiresCorrectTool selects getDestroyProgress's
// 30-vs-100 divisor.
type BlockHardness struct {
	DestroySpeed       float32
	RequiresCorrectTool bool
}

// Hardness maps a block resource id (Block.ID(), e.g. "minecraft:stone") to its break-time
// inputs. Keyed by the per-block id (NOT per-state) because BlockBehaviour.getDestroySpeed
// returns the constant destroyTime for every state of a block in vanilla 26.2.
var Hardness = map[string]BlockHardness{
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
		fmt.Fprintf(&buf, "\t%s:%s {DestroySpeed: %s, RequiresCorrectTool: %t},\n",
			quoted, pad, goFloat32(r.DestroySpeed), r.RequiresTool)
	}
	buf.WriteString("}\n")

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genBlockHardness: gofmt: %w", err)
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genBlockHardness: %w", err)
	}
	logf("genBlockHardness: wrote %s (%d blocks)", out, len(rows))
	return nil
}

// goFloat32 formats a float as a Go float32 literal that round-trips to the exact same bits.
// strconv via %g on the float64 we parsed from the JSON yields the shortest form; since the
// JSON value was produced by Java's Float.toString of a float32, the literal here parses back
// to the identical float32 in Go.
func goFloat32(f float64) string {
	s := fmt.Sprintf("%g", f)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0" // make whole numbers obviously floats (e.g. "50" -> "50.0")
	}
	return s
}
