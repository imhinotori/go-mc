package lighting

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
)

// --- test world -------------------------------------------------------------------------------
//
// testWorld is a minimal LightChunkGetter over a single loaded chunk column at (0,0) plus an
// arbitrary block grid, sized minSectionY..(minSectionY+sectionsCount). It reproduces exactly the
// contract the engine needs (block reads, sky-source heightmap, emissive-block enumeration).

type testWorld struct {
	minSectionY   int
	sectionsCount int
	// block state per (x,y,z) world coord; default air. keyed by packed block long.
	blocks map[int64]block.StateID
	// which chunks are "loaded" (return non-nil). default: only (0,0).
	loaded map[[2]int]bool

	chunks map[[2]int]*testChunk
}

func newTestWorld(minSectionY, sectionsCount int) *testWorld {
	w := &testWorld{
		minSectionY:   minSectionY,
		sectionsCount: sectionsCount,
		blocks:        make(map[int64]block.StateID),
		loaded:        map[[2]int]bool{{0, 0}: true},
		chunks:        make(map[[2]int]*testChunk),
	}
	return w
}

func (w *testWorld) minY() int { return w.minSectionY * 16 }
func (w *testWorld) maxY() int { return w.minY() + w.sectionsCount*16 - 1 }

func (w *testWorld) set(x, y, z int, s block.StateID) { w.blocks[blockAsLong(x, y, z)] = s }
func (w *testWorld) get(x, y, z int) block.StateID {
	if s, ok := w.blocks[blockAsLong(x, y, z)]; ok {
		return s
	}
	return airState
}

func (w *testWorld) MinSectionY() int   { return w.minSectionY }
func (w *testWorld) SectionsCount() int { return w.sectionsCount }

func (w *testWorld) GetChunkForLighting(cx, cz int) LightChunk {
	if !w.loaded[[2]int{cx, cz}] {
		return nil
	}
	c := w.chunks[[2]int{cx, cz}]
	if c == nil {
		c = &testChunk{w: w, cx: cx, cz: cz}
		c.buildSources()
		w.chunks[[2]int{cx, cz}] = c
	}
	return c
}

// rebuildSources recomputes every loaded chunk's sky-source heightmap (call after block edits).
func (w *testWorld) rebuildSources() {
	for k, c := range w.chunks {
		_ = k
		c.buildSources()
	}
}

type testChunk struct {
	w       *testWorld
	cx, cz  int
	sources *ChunkSkyLightSources
}

func (c *testChunk) GetBlockState(x, y, z int) block.StateID { return c.w.get(x, y, z) }

func (c *testChunk) SkyLightSources() *ChunkSkyLightSources { return c.sources }

func (c *testChunk) FindBlockLightSources(fn func(x, y, z int, state block.StateID)) {
	minX, minZ := c.cx*16, c.cz*16
	for by := c.w.minY(); by <= c.w.maxY(); by++ {
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				s := c.w.get(minX+lx, by, minZ+lz)
				if block.LightEmission(s) > 0 {
					fn(minX+lx, by, minZ+lz, s)
				}
			}
		}
	}
}

// buildSources computes the sky-source heightmap via ChunkSkyLightSources.FillFrom, mirroring the
// seal-time fill. topSectionY is the highest non-air section-Y; allAir true if none.
func (c *testChunk) buildSources() {
	minX, minZ := c.cx*16, c.cz*16
	read := func(x, y, z int) block.StateID { return c.w.get(x, y, z) }
	// find highest non-air section
	topSectionY := c.w.minSectionY
	allAir := true
	for sy := c.w.minSectionY + c.w.sectionsCount - 1; sy >= c.w.minSectionY; sy-- {
		any := false
		for by := sy * 16; by < sy*16+16 && !any; by++ {
			for lz := 0; lz < 16 && !any; lz++ {
				for lx := 0; lx < 16 && !any; lx++ {
					if !block.IsAir(c.w.get(minX+lx, by, minZ+lz)) {
						any = true
					}
				}
			}
		}
		if any {
			topSectionY = sy
			allAir = false
			break
		}
	}
	c.sources = NewChunkSkyLightSources(c.w.minSectionY)
	// FillFrom expects a world-coordinate reader; our read closure already uses world coords.
	c.sources.FillFrom(read, topSectionY, allAir)
}

// initialLight drives the vanilla initial-lighting sequence over the single (0,0) chunk (and the
// full 3x3 if requested): updateSectionStatus for every section, setLightEnabled, then
// propagateLightSources + runLightUpdates. Mirrors ThreadedLevelLightEngine's initializeLight +
// lightChunk. CITE: LevelLightEngine + LightEngine.propagateLightSources.
func (w *testWorld) initialLight(eng *LevelLightEngine, chunkCoords [][2]int) {
	for _, cc := range chunkCoords {
		cx, cz := cc[0], cc[1]
		for sy := w.minSectionY; sy < w.minSectionY+w.sectionsCount; sy++ {
			// section empty iff no non-air block in it.
			empty := true
			for by := sy * 16; by < sy*16+16 && empty; by++ {
				for lz := 0; lz < 16 && empty; lz++ {
					for lx := 0; lx < 16 && empty; lx++ {
						if !block.IsAir(w.get(cx*16+lx, by, cz*16+lz)) {
							empty = false
						}
					}
				}
			}
			eng.UpdateSectionStatus(cx, sy, cz, empty)
		}
	}
	for _, cc := range chunkCoords {
		eng.SetLightEnabled(cc[0], cc[1], true)
	}
	for _, cc := range chunkCoords {
		eng.PropagateLightSources(cc[0], cc[1])
	}
	eng.RunLightUpdates()
}

func stateOf(t *testing.T, name string) block.StateID {
	t.Helper()
	var s block.StateID
	if id, ok := block.DefaultStateID["minecraft:"+name]; ok {
		return id
	}
	t.Fatalf("state %q not found", name)
	return s
}

// --- DataLayer round-trip ---------------------------------------------------------------------

func TestDataLayerNibbleRoundTrip(t *testing.T) {
	d := NewDataLayer()
	// write a distinct value at every (x,y,z); read back.
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				v := (x + y + z) & 0xF
				d.Set(x, y, z, v)
			}
		}
	}
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				want := (x + y + z) & 0xF
				if got := d.Get(x, y, z); got != want {
					t.Fatalf("Get(%d,%d,%d)=%d want %d", x, y, z, got, want)
				}
			}
		}
	}
	if len(d.Data()) != 2048 {
		t.Fatalf("Data len = %d want 2048", len(d.Data()))
	}
	// copy is independent
	cp := d.Copy()
	cp.Set(0, 0, 0, 3)
	if d.Get(0, 0, 0) == cp.Get(0, 0, 0) {
		t.Fatal("Copy not independent")
	}
	// index order y<<8|z<<4|x
	if dataLayerIndex(1, 2, 3) != (2<<8 | 3<<4 | 1) {
		t.Fatal("dataLayerIndex order wrong")
	}
}
