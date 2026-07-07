// gen_block_collision generates level/block/collision_shapes.go from
// block_collision_shapes.json.
//
// block_collision_shapes.json comes from the GenBlockCollisionShapes.java extractor: the
// DEDUPLICATED discrete collision grids (per-axis boundary coordinate lists + full-cell bit
// set, i.e. the DiscreteVoxelShape the live collideX path reads) of every registered
// BlockState's state.getCollisionShape(EmptyBlockGetter.INSTANCE, BlockPos.ZERO), plus:
//   - the per-state shape index (dense global block-state ids == Go StateID),
//   - per-shape identity-equality with Shapes.block() (the BlockCollisions fast-path key),
//   - the per-state hasLargeCollisionShape() flag (the Cursor3D border-ring gate).
//
// The generated tables back the hand-written level/block/voxelshape.go engine (the 1:1
// VoxelShape.collideX port).
package main

import (
	"fmt"
	"go/format"
	"path/filepath"
	"strconv"
	"strings"
)

// collisionShapeRow is one unique shape of block_collision_shapes.json.
type collisionShapeRow struct {
	X     []float64 `json:"x"`     // getCoords(Axis.X): size+1 boundary coords
	Y     []float64 `json:"y"`     // getCoords(Axis.Y)
	Z     []float64 `json:"z"`     // getCoords(Axis.Z)
	Full  []int     `json:"full"`  // full cell indices, (x*ySize+y)*zSize+z order
	Block bool      `json:"block"` // identity-equal to Shapes.block()
}

// collisionShapesFile is the whole extractor output.
type collisionShapesFile struct {
	Shapes []collisionShapeRow `json:"shapes"`
	States []int               `json:"states"` // index == global state id -> shape index
	Large  []int               `json:"large"`  // state ids with hasLargeCollisionShape()
}

// genBlockCollision reads block_collision_shapes.json and emits
// level/block/collision_shapes.go.
func genBlockCollision(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "block_collision_shapes.json")
	out := filepath.Join(goMCRoot, "level", "block", "collision_shapes.go")

	var data collisionShapesFile
	if err := readJSON(jsonPath, &data); err != nil {
		return fmt.Errorf("genBlockCollision: %w", err)
	}
	if len(data.Shapes) == 0 || len(data.States) == 0 {
		return fmt.Errorf("genBlockCollision: empty shapes/states in %s", jsonPath)
	}
	if len(data.Shapes) > 0xFFFF {
		return fmt.Errorf("genBlockCollision: %d shapes overflow the uint16 state table", len(data.Shapes))
	}

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_block_collision.go", "block_collision_shapes.json"))
	buf.WriteByte('\n')
	buf.WriteString("package block\n\n")

	// --- shape table -------------------------------------------------------------------
	buf.WriteString(`// collisionShapes holds every unique collision VoxelShape (deduplicated across the
// per-state table below). Index 0 onward is extractor first-seen order. See voxelshape.go
// for the engine that consumes these grids.
var collisionShapes = [...]VoxelShape{
`)
	for i, s := range data.Shapes {
		words := fullWords(s)
		fmt.Fprintf(&buf, "\t%d: {Coords: [3][]float64{%s, %s, %s}, Full: %s, Block: %t, Empty: %t},\n",
			i, goFloatSlice(s.X), goFloatSlice(s.Y), goFloatSlice(s.Z), goWordSlice(words), s.Block, len(s.Full) == 0)
	}
	buf.WriteString("}\n\n")

	// --- state -> shape index ------------------------------------------------------------
	buf.WriteString(`// stateCollisionShape maps a StateID (dense global block-state id) to its index in
// collisionShapes — the baked result of state.getCollisionShape(EmptyBlockGetter.INSTANCE,
// BlockPos.ZERO) (the no-context Cache read the live vanilla collision path returns for
// every non-dynamic-shape block).
var stateCollisionShape = [...]uint16{
`)
	const perLine = 16
	for i, sh := range data.States {
		if i%perLine == 0 {
			buf.WriteString("\t")
		}
		fmt.Fprintf(&buf, "%d,", sh)
		if i%perLine == perLine-1 || i == len(data.States)-1 {
			buf.WriteByte('\n')
		} else {
			buf.WriteByte(' ')
		}
	}
	buf.WriteString("}\n\n")

	// --- hasLargeCollisionShape bit set ---------------------------------------------------
	largeWords := make([]uint64, (len(data.States)+63)/64)
	for _, id := range data.Large {
		if id < 0 || id >= len(data.States) {
			return fmt.Errorf("genBlockCollision: large state id %d out of range", id)
		}
		largeWords[id>>6] |= 1 << uint(id&63)
	}
	buf.WriteString(`// stateLargeCollision packs BlockStateBase.hasLargeCollisionShape() per StateID (bit
// id&63 of word id>>6): true when the shape extends outside the unit cube on any axis OR
// the state has a dynamic shape (cache == null). The BlockCollisions Cursor3D consults it
// for the ±1 border ring around the query box. CITE: javap BlockCollisions.computeNext.
var stateLargeCollision = [...]uint64{
`)
	const wordsPerLine = 8
	for i, w := range largeWords {
		if i%wordsPerLine == 0 {
			buf.WriteString("\t")
		}
		fmt.Fprintf(&buf, "0x%016x,", w)
		if i%wordsPerLine == wordsPerLine-1 || i == len(largeWords)-1 {
			buf.WriteByte('\n')
		} else {
			buf.WriteByte(' ')
		}
	}
	buf.WriteString("}\n\n")

	// --- API -------------------------------------------------------------------------------
	buf.WriteString(`// emptyCollisionShape is returned for out-of-range state ids (never queried by vanilla).
var emptyCollisionShape = VoxelShape{
	Coords: [3][]float64{{0}, {0}, {0}},
	Empty:  true,
}

// CollisionShape returns the collision VoxelShape of a block state — the baked
// BlockStateBase.getCollisionShape(EmptyBlockGetter.INSTANCE, BlockPos.ZERO) (== the
// per-state Cache.collisionShape the live vanilla path reads). The shape is block-local;
// world placement is the offset parameter of its query methods. Out-of-range ids return the
// empty shape.
func CollisionShape(s StateID) *VoxelShape {
	if int(s) < 0 || int(s) >= len(stateCollisionShape) {
		return &emptyCollisionShape
	}
	return &collisionShapes[stateCollisionShape[s]]
}

// HasLargeCollisionShape reports BlockStateBase.hasLargeCollisionShape() for the state:
// whether its collision shape may extend outside the unit cube (fences/walls 1.5-high,
// dynamic-shape blocks). Gates the ±1 border ring of the block-collision scan.
func HasLargeCollisionShape(s StateID) bool {
	if int(s) < 0 || int(s) >= len(stateCollisionShape) {
		return false
	}
	return stateLargeCollision[s>>6]&(1<<uint(s&63)) != 0
}
`)

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genBlockCollision: gofmt: %w", err)
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genBlockCollision: %w", err)
	}
	logf("genBlockCollision: wrote %s (%d states, %d shapes)", out, len(data.States), len(data.Shapes))
	return nil
}

// fullWords packs a shape's full-cell indices into the []uint64 bit set voxelshape.go reads
// (bit (x*sy+y)*sz+z — already the extractor's index order).
func fullWords(s collisionShapeRow) []uint64 {
	cells := (len(s.X) - 1) * (len(s.Y) - 1) * (len(s.Z) - 1)
	if cells <= 0 {
		return nil
	}
	words := make([]uint64, (cells+63)/64)
	for _, idx := range s.Full {
		words[idx>>6] |= 1 << uint(idx&63)
	}
	return words
}

// goFloatSlice renders a []float64 literal whose elements round-trip to the exact same
// float64 bits (strconv shortest form; the JSON values are Java Double.toString output,
// which Go's decoder already parsed to identical bits).
func goFloatSlice(vals []float64) string {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, v := range vals {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	}
	sb.WriteByte('}')
	return sb.String()
}

// goWordSlice renders the Full bit-set words ([]uint64) literal; nil for empty shapes.
func goWordSlice(words []uint64) string {
	if len(words) == 0 {
		return "nil"
	}
	var sb strings.Builder
	sb.WriteString("[]uint64{")
	for i, w := range words {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "0x%x", w)
	}
	sb.WriteByte('}')
	return sb.String()
}
