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
	"github.com/imhinotori/sulfur/world/levelgen/surface"
)

// Generator is the SPLIT cross-chunk generation contract (GEN2-02). It is the
// two-phase lifecycle vanilla uses: a PURE single-chunk terrain pass that leaves
// the chunk at "carvers" status, followed by a decoration pass that runs ONCE all
// 8 neighbors are carved, over a 3x3 read/write proxy. The off-tick Worker drives
// the lifecycle: it stages carved chunks on a single scheduler goroutine and
// decorates each center once its neighborhood is complete.
//
// Implementations must be PURE: GenerateTerrain(pos) yields a chunk that serializes
// to identical bytes every call (seed-derived only, no RNG drift); Decorate is pure
// over (seed, center.pos) given a fully-carved neighborhood — never let the SET or
// ORDER of decorated centers affect a single center's output.
//
// Generate(pos) is NOT on this interface: it stays a CONCRETE method on each impl
// (= GenerateTerrain then Decorate over a freshly-terrain-generated 3x3) for the
// single-chunk / test / SpawnSurfaceY path. This preserves the ~13 existing concrete
// g.Generate(pos) call sites unchanged.
type Generator interface {
	// GenerateTerrain runs the PURE single-chunk pipeline (fill -> surface -> carve)
	// and returns a chunk left at StatusCarvers. The carve is footprint-guarded to the
	// target chunk, so terrain stays parallel + single-chunk.
	GenerateTerrain(pos level.ChunkPos) *level.Chunk
	// Decorate runs the post-carve pass over a 3x3 view, once all 8 neighbors of the
	// view's center are carved. For NoiseGenerator it is the LIVE feature decoration
	// (applyBiomeDecoration writes feature placements into the 3x3 + promotes the center to
	// StatusFull); for Superflat it is a no-op (no features). The late-neighbor-write-after-
	// emit rule is RESOLVED in the worker with Option Y (hold-until-neighborhood-complete):
	// a center decorates as soon as its 3x3 is carved but is emitted only once every wanted
	// neighbor that holds it is decorated, so no write lands after the immutable handoff.
	Decorate(view *Neighborhood)
	// Dims returns the generator's (minY, height) so the scheduler can size the
	// Neighborhood proxy without re-deriving the chunk geometry.
	Dims() (minY, height int)
}

// decorateSingle runs the concrete single-chunk Generate path shared by every
// generator: terrain-generate the center + its 8 neighbors into a 3x3 Neighborhood,
// then Decorate the center over that view (promoting it to StatusFull). The neighbors
// are freshly terrain-generated only to satisfy the decoration contract's 3x3 read
// window; for Phase 10's no-op Decorate they are discarded after promotion. Used by
// *Superflat.Generate and *NoiseGenerator.Generate so the single-chunk path mirrors
// the worker's two-phase lifecycle byte-for-byte.
func decorateSingle(g Generator, pos level.ChunkPos) *level.Chunk {
	center := g.GenerateTerrain(pos)
	minY, height := g.Dims()
	chunks := make(map[int64]*level.Chunk, 9)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			np := level.ChunkPos{pos[0] + int32(dx), pos[1] + int32(dz)}
			if dx == 0 && dz == 0 {
				chunks[packPos(np)] = center
				continue
			}
			chunks[packPos(np)] = g.GenerateTerrain(np)
		}
	}
	view := newNeighborhood(pos, chunks, minY, height)
	g.Decorate(view)
	return center
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

// GenerateTerrain builds the deterministic superflat chunk for pos, left at
// StatusCarvers (the pre-decoration status the worker stages). Pure: no RNG.
// Superflat has no carve and no decoration, so GenerateTerrain produces the
// complete block content; only the status differs from the old single-shot
// Generate (carvers vs full).
func (g *Superflat) GenerateTerrain(_ level.ChunkPos) *level.Chunk {
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
		// Biome is uniform plains across the whole 4x4x4 grid. Construct the
		// container as SINGLE-VALUED plains directly rather than Set-ing all 64
		// cells: a per-cell Set on the fresh single-value (default) container
		// resizes to a 2-entry linear palette carrying the stale default biome
		// (id 0) as a phantom entry, and emits a data long. Vanilla emits a
		// single-valued biome container (bits=0, one palette id, zero longs) for
		// a uniform section — matching it (NewBiomesPaletteContainer with plains
		// as the default value) keeps the wire byte-identical to vanilla here.
		s.Biomes = level.NewBiomesPaletteContainer(4*4*4, g.plains)
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

	// GEN2-03: also build the 3 WORLDGEN heightmaps (WORLD_SURFACE_WG / OCEAN_FLOOR_WG /
	// MOTION_BLOCKING) from the final superflat blocks so a superflat world carries a
	// complete, correct worldgen heightmap (matching the noise generator). Superflat has
	// no carve and no fluid, so OCEAN_FLOOR_WG == WORLD_SURFACE_WG here. This overwrites
	// the (zeroed) MotionBlocking BitStorage set above with the same surfaceVal, which is
	// consistent (the column top is the stone surface). The CLIENT heightmaps set above
	// remain the wire authority.
	surface.BuildWorldgenHeightmaps(ch, g.MinY, g.MinY+g.Secs*16)

	// GEN2-02: leave the chunk at StatusCarvers — the worker stages carved chunks and
	// promotes them to StatusFull during the (no-op) Decorate pass once the 3x3 is carved.
	ch.Status = level.StatusCarvers
	return ch
}

// Decorate is the Phase-10 NO-OP decoration pass: it writes NOTHING (superflat has
// no features) and only promotes the view's center to StatusFull. The Neighborhood
// proxy + heightmap-update wiring is built for the feature phase (Phase 11+), but
// superflat will never use it. The late-neighbor-write-after-emit rule is DEFERRED.
func (g *Superflat) Decorate(view *Neighborhood) {
	center := view.chunks[packPos(view.center)]
	if center != nil {
		center.Status = level.StatusFull
	}
}

// Dims returns the superflat (minY, height) so the worker can size the Neighborhood.
func (g *Superflat) Dims() (minY, height int) { return g.MinY, g.Secs * 16 }

// Generate builds the deterministic superflat chunk for pos at StatusFull. Pure: no
// RNG. It is the concrete single-chunk path (= GenerateTerrain then Decorate over a
// 3x3) retained for tests / SpawnSurfaceY / single-chunk callers; it is NOT on the
// Generator interface. For superflat this is byte-identical to the old single-shot
// Generate (the no-op Decorate only flips status carvers->full).
func (g *Superflat) Generate(pos level.ChunkPos) *level.Chunk {
	return decorateSingle(g, pos)
}

// compile-time assertion: *Superflat satisfies the split Generator interface.
var _ Generator = (*Superflat)(nil)
