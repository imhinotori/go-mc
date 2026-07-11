package carver

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// TestCreateRoomOffsetsOnX verifies CaveWorldCarver.createRoom carves at (x + 1.0, y, z)
// -- the room center is offset on the X axis, NOT y+1.0. Bytecode: carveEllipsoid is
// invoked with dload 6 [=x], dconst_1, dadd (x+1.0); then dload 8 [=y]; then dload 10
// [=z]. CITE: CaveWorldCarver.createRoom.
func TestCreateRoomOffsetsOnX(t *testing.T) {
	rep, err := ParseReplaceables()
	if err != nil {
		t.Fatalf("parse replaceables: %v", err)
	}
	target := level.ChunkPos{0, 0}
	ch := newSolidChunk(target, -64, 384)
	mask := newCarvingMask(ch.MinY(), ch.Height())
	cfg, err := ParseCarverConfig("minecraft:cave")
	if err != nil {
		t.Fatalf("parse cave config: %v", err)
	}
	cc := &carveContext{
		chunk: ch, mask: mask, rep: rep, fluid: dryFluid{},
		air:     block.ToStateID[block.Air{}],
		caveAir: block.ToStateID[block.CaveAir{}],
		minGenY: ch.MinY(),
	}

	// Room center at chunk-local (8, y0, 8); a floorLevel far below so nothing is skipped.
	const cx, cy, cz = 8.0, 100.0, 8.0
	skip := caveSkip(-1e9)
	createRoom(cfg, cc, cx, cy, cz, 3.0 /*caveRadius*/, 1.0 /*yScale*/, skip)

	if len(ch.blocks) == 0 {
		t.Fatal("createRoom carved nothing")
	}
	bb := boundingBox(ch)
	midX := float64(bb.minX+bb.maxX) / 2.0
	midZ := float64(bb.minZ+bb.maxZ) / 2.0

	// The X midpoint of the carved ellipsoid must be ~cx+1 (offset on X), and the Z
	// midpoint ~cz. If the offset were on Y (the bug), midX would be ~cx.
	if d := midX - (cx + 1.0); d < -1.0 || d > 1.0 {
		t.Fatalf("carved X midpoint=%v want ~%v (cx+1); offset is not on X", midX, cx+1.0)
	}
	if d := midX - cx; d > -0.25 && d < 0.25 {
		t.Fatalf("carved X midpoint=%v equals cx=%v (X NOT offset -- the y+1 bug is back)", midX, cx)
	}
	if d := midZ - cz; d < -1.0 || d > 1.0 {
		t.Fatalf("carved Z midpoint=%v want ~%v (cz)", midZ, cz)
	}
}
