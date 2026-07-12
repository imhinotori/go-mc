package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// lightTestMinSectionY / lightTestSecs mirror the overworld light geometry the server threads into
// RelightEdit (minY>>4 == -4, height>>4 == 24). Same values as blockTest* in manager_test.go, expressed
// in section units.
const (
	lightTestMinSectionY = blockTestMinY >> 4 // -4
	lightTestSecs        = blockTestSecs      // 24
)

// nibbleGet reads a 4-bit light value out of a 2048-byte DataLayer array at local index
// (y&15)<<8 | (z&15)<<4 | (x&15). A nil array reads 0.
func nibbleGet(arr []byte, x, y, z int) int {
	if arr == nil {
		return 0
	}
	local := (y&15)<<8 | (z&15)<<4 | (x & 15)
	return int(arr[local>>1]>>(4*(local&1))) & 0xF
}

// blockLightAt reads the BLOCK light at a world pos from the column's stored per-section arrays.
func blockLightAt(m *ChunkManager, pos pk.Position) int {
	col := colOf(pos)
	ch, ok := m.Get(col)
	if !ok {
		return -1
	}
	sec := (pos.Y - blockTestMinY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return -1
	}
	return nibbleGet(ch.Sections[sec].BlockLight, pos.X, pos.Y, pos.Z)
}

// TestLightPropertiesDiffer verifies the hasDifferentLightProperties gate: same state -> false; a change
// in emission (air<->glowstone) or dampening (air<->stone) -> true; a same-block property flip that does
// not touch light (not exercised here, covered by the identity guard) -> false.
func TestLightPropertiesDiffer(t *testing.T) {
	air := block.ToStateID[block.Air{}]
	stone := block.ToStateID[block.Stone{}]
	glow := block.ToStateID[block.Glowstone{}]

	if LightPropertiesDiffer(stone, stone) {
		t.Fatalf("LightPropertiesDiffer(stone,stone) = true, want false (identity)")
	}
	if !LightPropertiesDiffer(air, glow) {
		t.Fatalf("LightPropertiesDiffer(air,glowstone) = false, want true (emission 0 -> 15)")
	}
	if !LightPropertiesDiffer(air, stone) {
		t.Fatalf("LightPropertiesDiffer(air,stone) = false, want true (dampening 0 -> 15 + occlusion)")
	}
}

// TestRelightEditGlowstonePlaceBreak drives the incremental relight end to end: seal a 3x3 of all-air
// chunks (block light 0 everywhere), place a glowstone at the center column's surface (RelightEdit must
// report the center column changed and light must radiate), then break it back to air (light must return
// to 0 and the column reports changed again).
func TestRelightEditGlowstonePlaceBreak(t *testing.T) {
	m := NewChunkManager()
	// Seal a 3x3 so the center has real neighbors (cross-border light is correct).
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			readyChunk(m, level.ChunkPos{int32(dx), int32(dz)})
		}
	}
	air := block.ToStateID[block.Air{}]
	// Initial light seal for the whole 3x3 (each column computed over its neighbors).
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			c := level.ChunkPos{int32(dx), int32(dz)}
			neighbors := map[[2]int]*level.Chunk{}
			for ex := -1; ex <= 1; ex++ {
				for ez := -1; ez <= 1; ez++ {
					nc := level.ChunkPos{c[0] + int32(ex), c[1] + int32(ez)}
					if ch, ok := m.Get(nc); ok {
						neighbors[[2]int{int(nc[0]), int(nc[1])}] = ch
					}
				}
			}
			ComputeChunkLight(c, neighbors, lightTestMinSectionY, lightTestSecs, air, true)
		}
	}

	pos := pk.Position{X: 8, Y: 70, Z: 8} // center column
	if bl := blockLightAt(m, pos); bl != 0 {
		t.Fatalf("initial block light at %v = %d, want 0 (all-air world)", pos, bl)
	}

	// Place glowstone (emission 15) and relight.
	glow := block.ToStateID[block.Glowstone{}]
	if !m.SetBlock(pos, glow, blockTestMinY) {
		t.Fatalf("SetBlock(glowstone) = false, want true")
	}
	changed := m.RelightEdit(pos, lightTestMinSectionY, lightTestSecs, air, true)
	if len(changed) == 0 {
		t.Fatalf("RelightEdit after glowstone place reported 0 changed columns, want >=1 (center)")
	}
	centerReported := false
	for _, cl := range changed {
		if cl.Pos == (level.ChunkPos{0, 0}) {
			centerReported = true
		}
	}
	if !centerReported {
		t.Fatalf("RelightEdit did not report the center column {0,0} as changed; got %v", changed)
	}
	// The glowstone cell itself is emission 15.
	if bl := blockLightAt(m, pos); bl != 15 {
		t.Fatalf("block light AT glowstone = %d, want 15 (LightEmission)", bl)
	}
	// A cell one block away attenuates to 14.
	if bl := blockLightAt(m, pk.Position{X: 9, Y: 70, Z: 8}); bl != 14 {
		t.Fatalf("block light 1 away from glowstone = %d, want 14 (radiate -1)", bl)
	}

	// Break it back to air and relight — light returns to 0.
	if !m.SetBlock(pos, air, blockTestMinY) {
		t.Fatalf("SetBlock(air) = false, want true")
	}
	changed = m.RelightEdit(pos, lightTestMinSectionY, lightTestSecs, air, true)
	if len(changed) == 0 {
		t.Fatalf("RelightEdit after glowstone break reported 0 changed columns, want >=1")
	}
	if bl := blockLightAt(m, pos); bl != 0 {
		t.Fatalf("block light after break = %d, want 0 (emitter removed)", bl)
	}
	if bl := blockLightAt(m, pk.Position{X: 9, Y: 70, Z: 8}); bl != 0 {
		t.Fatalf("block light 1 away after break = %d, want 0", bl)
	}
}

// TestWriteLightUpdatePacket verifies WriteLightUpdate assembles a non-empty ClientboundLightUpdate for a
// column that carries block-light sections (mask bit set, array carried).
func TestWriteLightUpdatePacket(t *testing.T) {
	sky := make([][]byte, lightTestSecs)
	blk := make([][]byte, lightTestSecs)
	// One block-light section present.
	blk[6] = make([]byte, 2048)
	blk[6][0] = 0x0F
	cl := ColumnLight{Pos: level.ChunkPos{2, -3}, Sky: sky, Block: blk}
	p := WriteLightUpdate(cl)
	if len(p.Data) == 0 {
		t.Fatalf("WriteLightUpdate produced empty packet data")
	}
}
