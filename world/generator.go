// Package world is the server-side world subsystem: a deterministic superflat
// generator, a tick-owned chunk manager, an off-tick load/generate worker, and
// the ClientboundLevelChunkWithLight packet assembly. It is deliberately
// decoupled from the tick loop — the worker computes immutable *level.Chunk
// values off-thread and the tick (Plan 04-03) is the sole mutator of the
// manager. It reuses the fork's level/save/region primitives wholesale.
package world

import (
	"math/bits"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
)

// Generator produces a chunk for a given column position. Implementations must
// be PURE: the same pos yields a chunk that serializes to identical bytes every
// call (no RNG, or seed-derived only).
type Generator interface {
	Generate(pos level.ChunkPos) *level.Chunk
}

// fullSkyLight is a full-brightness (level 15) sky-light array for one section:
// 4096 nibbles packed into 2048 bytes, every nibble = 0xF.
func fullSkyLight() []byte {
	b := make([]byte, 2048)
	for i := range b {
		b[i] = 0xFF
	}
	return b
}

// Superflat is a deterministic WORLD-04 stub generator. Every column is:
//
//	y == MinY                 -> bedrock (1 layer floor)
//	MinY < y <= SurfaceY      -> stone (solid fill)
//	y >  SurfaceY             -> air
//
// Biome is plains; the 3 CLIENT heightmaps report the surface height and the
// chunk Status is StatusFull. It is NOT vanilla-parity (that is Phase 9) — it
// is a stable, solid, lit floor a client can stand on.
//
// Section count (Secs) is DERIVED by the caller from dimType.Height/16 and
// passed in — it is never hard-coded to 24.
type Superflat struct {
	Secs     int           // = dimType.Height/16 (overworld 24)
	MinY     int           // = dimType.MinY (overworld -64)
	SurfaceY int           // top solid (stone) block world-Y; documented choice by the caller
	stone    block.StateID // resolved block.ToStateID[block.Stone{}]
	bedrock  block.StateID // resolved block.ToStateID[block.Bedrock{}]
	plains   biome.Type    // plains biome id
}

// NewSuperflat resolves the stone/bedrock state ids and the plains biome once.
func NewSuperflat(secs, minY, surfaceY int) *Superflat {
	var plains biome.Type
	if err := plains.UnmarshalText([]byte("minecraft:plains")); err != nil {
		// plains is a generated biome — absence is a build-data bug, not runtime input.
		panic("world: plains biome not found in registry: " + err.Error())
	}
	return &Superflat{
		Secs:     secs,
		MinY:     minY,
		SurfaceY: surfaceY,
		stone:    block.ToStateID[block.Stone{}],
		bedrock:  block.ToStateID[block.Bedrock{}],
		plains:   plains,
	}
}

// sectionLocal maps a world (x,y,z) to its section index and the in-section
// local index. Local index is y-major: (y&15)<<8 | (z&15)<<4 | (x&15).
func (g *Superflat) sectionLocal(x, y, z int) (sec, local int) {
	sec = (y - g.MinY) >> 4
	local = (y&15)<<8 | (z&15)<<4 | (x & 15)
	return
}

// Generate builds the deterministic superflat chunk for pos. Pure: no RNG.
func (g *Superflat) Generate(_ level.ChunkPos) *level.Chunk {
	ch := level.EmptyChunk(g.Secs)

	// Fill every column identically — the superflat profile is position-independent,
	// which is also why Generate is deterministic.
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			for y := g.MinY; y <= g.SurfaceY; y++ {
				sec, local := g.sectionLocal(x, y, z)
				if sec < 0 || sec >= len(ch.Sections) {
					continue
				}
				if y == g.MinY {
					ch.Sections[sec].SetBlock(local, g.bedrock)
				} else {
					ch.Sections[sec].SetBlock(local, g.stone)
				}
			}
		}
	}

	// Per-section finishing: FluidCount=0 explicitly (exercises the 04-01 two-short
	// layout), plains biome on every present section, full sky light for rendering.
	for i := range ch.Sections {
		s := &ch.Sections[i]
		s.FluidCount = 0
		// Set the whole 4x4x4 biome grid to plains.
		for bi := 0; bi < 4*4*4; bi++ {
			s.Biomes.Set(bi, g.plains)
		}
		s.SkyLight = fullSkyLight()
	}

	// Heightmaps. Vanilla stores per-column the Y of the first block ABOVE the
	// highest non-air block, encoded relative to the world floor (MinY). For a
	// flat top at SurfaceY that value is (SurfaceY - MinY) + 1, identical for all
	// 256 columns. WriteTo sends only WorldSurface / MotionBlocking /
	// MotionBlockingNoLeaves (the CLIENT heightmaps).
	bitsForHeight := bits.Len(uint(g.Secs)*16 + 1)
	surfaceVal := (g.SurfaceY - g.MinY) + 1
	ws := level.NewBitStorage(bitsForHeight, 16*16, nil)
	mb := level.NewBitStorage(bitsForHeight, 16*16, nil)
	mbnl := level.NewBitStorage(bitsForHeight, 16*16, nil)
	for col := 0; col < 16*16; col++ {
		ws.Set(col, surfaceVal)
		mb.Set(col, surfaceVal)
		mbnl.Set(col, surfaceVal)
	}
	ch.HeightMaps.WorldSurface = ws
	ch.HeightMaps.MotionBlocking = mb
	ch.HeightMaps.MotionBlockingNoLeaves = mbnl

	ch.Status = level.StatusFull
	return ch
}
