// gen_block_light generates level/block/light.go from block_light.json.
//
// block_light.json comes from the GenBlockLight.java extractor: one row per registered
// BlockState carrying the data that drives net.minecraft.world.level.lighting.LightEngine 1:1:
//
//   - lb : BlockBehaviour$BlockStateBase.getLightDampening()  (the light "opacity"; the engine
//          uses LightEngine.getOpacity(state) = max(1, lb)).
//   - em : BlockBehaviour$BlockStateBase.getLightEmission()   (block-light emission 0..15).
//   - f  : the 6 per-direction (DOWN,UP,NORTH,SOUTH,WEST,EAST) occlusion classifications used by
//          LightEngine.shapeOccludes / getOcclusionShape:
//            -1 -> EMPTY  (isEmptyShape(state), i.e. !canOcclude || !useShapeForLightOcclusion,
//                          OR the per-face occlusion shape is empty)
//            -2 -> FULL   (getFaceOcclusionShape(dir) == Shapes.block())
//            >=0 -> PARTIAL, an index into the dedup mask pool (a 16x16 = 256-bit face coverage
//                   mask, exact because all partial occlusion shapes are 1/16-grid aligned)
//
// LightEngine.shapeOccludes(from,to,dir) = Shapes.faceShapeOccludes(getOcclusionShape(from,dir),
// getOcclusionShape(to,dir.getOpposite())). Shapes.faceShapeOccludes(a,b): true if a or b is the
// full cube; false if both empty; else OR(a,b) must cover the full face. With FULL acting as the
// full cube, EMPTY as empty, and PARTIAL as its 16x16 mask, "covers the full face" is
// (maskFrom | maskTo) == all-ones. The extractor SELF-CHECKED this reproduction against the live
// Shapes.faceShapeOccludes over 309996 state-pair*direction samples with 0 mismatches.
//
// The row `id` is the GLOBAL block-state id (Block.getId(state)) == the Go StateID, so the emitted
// tables are flat slices indexed directly by StateID.
package main

import (
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
)

type lightStateRow struct {
	ID int    `json:"id"`
	LB int    `json:"lb"`
	EM int    `json:"em"`
	ES int    `json:"es"` // 1 iff LightEngine.isEmptyShape(state)
	F  [6]int `json:"f"`
}

type blockLightJSON struct {
	Masks  [][4]int64      `json:"masks"`
	States []lightStateRow `json:"states"`
}

// genBlockLight reads block_light.json and emits level/block/light.go.
func genBlockLight(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "block_light.json")
	out := filepath.Join(goMCRoot, "level", "block", "light.go")

	var data blockLightJSON
	if err := readJSON(jsonPath, &data); err != nil {
		return fmt.Errorf("genBlockLight: %w", err)
	}

	rows := data.States
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	for i, r := range rows {
		if r.ID != i {
			return fmt.Errorf("genBlockLight: non-dense state ids: row %d has id %d", i, r.ID)
		}
	}

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_block_light.go", "block_light.json"))
	buf.WriteByte('\n')
	buf.WriteString("package block\n\n")

	buf.WriteString(`// Light-occlusion face classes packed per direction into blockLightFaces[stateID].
// Each of the 6 directions (Down,Up,North,South,West,East ordinal order) gets 10 bits:
// bits [0..1] = class (0=EMPTY, 1=FULL, 2=PARTIAL); bits [2..9] = mask-pool index (PARTIAL only).
// CITE: net.minecraft.world.level.lighting.LightEngine.getOcclusionShape / isEmptyShape and
// BlockBehaviour$BlockStateBase.getFaceOcclusionShape.
const (
	faceClassEmpty   = 0
	faceClassFull    = 1
	faceClassPartial = 2
	faceBits         = 10 // per-direction field width in the packed uint64
	faceClassMask    = 0x3
)

// lightFaceMask is a 16x16 (256-bit) face coverage mask for a PARTIAL occlusion face, packed as
// 4 uint64 (bit v*16+u). CITE: getFaceOcclusionShape projected on the 1/16 grid (exact — all
// partial occlusion shapes in 26.2 are 1/16-aligned).
type lightFaceMask [4]uint64

`)

	fmt.Fprintf(&buf, "var lightFaceMasks = [...]lightFaceMask{\n")
	for _, m := range data.Masks {
		fmt.Fprintf(&buf, "\t{0x%016x, 0x%016x, 0x%016x, 0x%016x},\n",
			uint64(m[0]), uint64(m[1]), uint64(m[2]), uint64(m[3]))
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// blockLightBlock[stateID] = BlockBehaviour$BlockStateBase.getLightDampening().\n")
	fmt.Fprintf(&buf, "var blockLightBlock = [...]uint8{\n")
	emitLightU8(&buf, rows, func(r lightStateRow) int { return r.LB })
	buf.WriteString("}\n\n")

	buf.WriteString("// blockLightEmission[stateID] = BlockBehaviour$BlockStateBase.getLightEmission().\n")
	fmt.Fprintf(&buf, "var blockLightEmission = [...]uint8{\n")
	emitLightU8(&buf, rows, func(r lightStateRow) int { return r.EM })
	buf.WriteString("}\n\n")

	// Emit the emptyShape bitset (1 bit per state, packed into uint64 words).
	buf.WriteString("// blockLightEmptyShape is a bitset: bit stateID set iff LightEngine.isEmptyShape(state)\n")
	buf.WriteString("// (!canOcclude || !useShapeForLightOcclusion) — the FLAG_FROM_EMPTY_SHAPE source bit.\n")
	fmt.Fprintf(&buf, "var blockLightEmptyShape = [...]uint64{\n")
	words := (len(rows) + 63) / 64
	bits := make([]uint64, words)
	for i, r := range rows {
		if r.ES != 0 {
			bits[i>>6] |= 1 << uint(i&63)
		}
	}
	for i, w := range bits {
		if i%4 == 0 {
			buf.WriteString("\t")
		}
		fmt.Fprintf(&buf, "0x%016x,", w)
		if i%4 == 3 || i == len(bits)-1 {
			buf.WriteByte('\n')
		} else {
			buf.WriteByte(' ')
		}
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// blockLightFaces[stateID] packs the 6 per-direction occlusion face classes (see above).\n")
	fmt.Fprintf(&buf, "var blockLightFaces = [...]uint64{\n")
	const perLine = 6
	for i, r := range rows {
		var packed uint64
		for d := 0; d < 6; d++ {
			c := r.F[d]
			var field uint64
			switch {
			case c == -1:
				field = faceClassEmptyVal
			case c == -2:
				field = faceClassFullVal
			default:
				field = uint64(faceClassPartialVal) | (uint64(c) << 2)
			}
			packed |= field << (uint(d) * 10)
		}
		if i%perLine == 0 {
			buf.WriteString("\t")
		}
		fmt.Fprintf(&buf, "0x%015x,", packed)
		if i%perLine == perLine-1 || i == len(rows)-1 {
			buf.WriteByte('\n')
		} else {
			buf.WriteByte(' ')
		}
	}
	buf.WriteString("}\n\n")

	buf.WriteString(`// LightBlock reports BlockBehaviour$BlockStateBase.getLightDampening() for the state — the raw
// light "opacity". The engine uses LightEngine.getOpacity(state) = max(1, LightBlock(s)); see
// LightOpacity. Out-of-range ids return 0. CITE: LightEngine.getOpacity.
func LightBlock(s StateID) int {
	if int(s) < 0 || int(s) >= len(blockLightBlock) {
		return 0
	}
	return int(blockLightBlock[s])
}

// LightOpacity reports LightEngine.getOpacity(state) = max(1, getLightDampening()). This is the
// per-step attenuation applied when light crosses INTO the block. CITE:
// net.minecraft.world.level.lighting.LightEngine.getOpacity.
func LightOpacity(s StateID) int {
	o := LightBlock(s)
	if o < 1 {
		return 1
	}
	return o
}

// LightEmission reports BlockBehaviour$BlockStateBase.getLightEmission() — the block-light a state
// radiates (0..15). Out-of-range ids return 0. CITE: BlockLightEngine.getEmission uses
// state.getLightEmission().
func LightEmission(s StateID) int {
	if int(s) < 0 || int(s) >= len(blockLightEmission) {
		return 0
	}
	return int(blockLightEmission[s])
}

// LightShapeIsEmpty ports net.minecraft.world.level.lighting.LightEngine.isEmptyShape(state) =
// !state.canOcclude() || !state.useShapeForLightOcclusion(). It is the FLAG_FROM_EMPTY_SHAPE source
// bit the increase BFS uses to treat the "from" block as air (skipping its shape-occlusion test).
// Out-of-range ids return true (air-like). CITE: LightEngine.isEmptyShape.
func LightShapeIsEmpty(s StateID) bool {
	if int(s) < 0 || int(s) >= len(blockLightEmptyShape)*64 {
		return true
	}
	return blockLightEmptyShape[s>>6]&(1<<uint(s&63)) != 0
}

// faceClass returns (class, maskIndex) for state s in direction dir.
func faceClass(s StateID, dir Direction) (int, int) {
	if int(s) < 0 || int(s) >= len(blockLightFaces) || dir > East {
		return faceClassEmpty, 0
	}
	field := (blockLightFaces[s] >> (uint(dir) * faceBits)) & ((1 << faceBits) - 1)
	return int(field & faceClassMask), int(field >> 2)
}

// faceMaskFor resolves the effective 16x16 face mask for (state,dir): all-ones for FULL, all-zero
// for EMPTY, or the stored partial mask. The second return is true iff the face is FULL.
func faceMaskFor(s StateID, dir Direction) (lightFaceMask, bool) {
	cls, idx := faceClass(s, dir)
	switch cls {
	case faceClassFull:
		return lightFaceMask{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)}, true
	case faceClassPartial:
		return lightFaceMasks[idx], false
	default:
		return lightFaceMask{}, false
	}
}

// ShapeOccludes ports net.minecraft.world.level.lighting.LightEngine.shapeOccludes(fromState,
// toState, direction) = Shapes.faceShapeOccludes(getOcclusionShape(from,dir),
// getOcclusionShape(to,dir.getOpposite())). Shapes.faceShapeOccludes(a,b): true if a or b is the
// full cube; false if both empty; otherwise OR(a,b) must cover the full face (all 256 face cells).
// The occlusion classes were extracted so this is EXACT (self-checked vs the live engine, 0
// mismatches over 309996 samples). CITE: LightEngine.shapeOccludes + Shapes.faceShapeOccludes.
func ShapeOccludes(from, to StateID, dir Direction) bool {
	mFrom, fullFrom := faceMaskFor(from, dir)
	mTo, fullTo := faceMaskFor(to, opposite(dir))
	if fullFrom || fullTo {
		return true
	}
	empty := true
	for i := 0; i < 4; i++ {
		if mFrom[i] != 0 || mTo[i] != 0 {
			empty = false
			break
		}
	}
	if empty {
		return false
	}
	for i := 0; i < 4; i++ {
		if (mFrom[i] | mTo[i]) != ^uint64(0) {
			return false
		}
	}
	return true
}
`)

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genBlockLight: gofmt: %w", err)
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genBlockLight: %w", err)
	}
	logf("genBlockLight: wrote %s (%d states, %d masks)", out, len(rows), len(data.Masks))
	return nil
}

// generator-side class constants (distinct names to avoid colliding with the emitted package-side
// faceClass* consts).
const (
	faceClassEmptyVal   = 0
	faceClassFullVal    = 1
	faceClassPartialVal = 2
)

func emitLightU8(buf *strings.Builder, rows []lightStateRow, sel func(lightStateRow) int) {
	const perLine = 16
	for i, r := range rows {
		if i%perLine == 0 {
			buf.WriteString("\t")
		}
		fmt.Fprintf(buf, "%d,", sel(r))
		if i%perLine == perLine-1 || i == len(rows)-1 {
			buf.WriteByte('\n')
		} else {
			buf.WriteByte(' ')
		}
	}
}
