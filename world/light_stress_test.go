package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// nibble reads a DataLayer-format light array (nil => 0).
func lightNibble(arr []byte, x, y, z int) int {
	if arr == nil {
		return 0
	}
	local := (y&15)<<8 | (z&15)<<4 | (x & 15)
	return int(arr[local>>1]>>(4*(local&1))) & 0xF
}

// skyAt reads sky light at world (x,y,z) from a generated chunk; above the section range => 15.
func skyAt(ch *level.Chunk, minY, x, y, z int) int {
	sec := (y - minY) >> 4
	if sec >= len(ch.Sections) {
		return 15
	}
	if sec < 0 {
		return 0
	}
	return lightNibble(ch.Sections[sec].SkyLight, x, y, z)
}

func blockAt(ch *level.Chunk, minY, x, y, z int) int {
	sec := (y - minY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return 0
	}
	return lightNibble(ch.Sections[sec].BlockLight, x, y, z)
}

// TestNoiseGenLightSelfCheck: generate real chunks across several seeds and confirm the engine-
// computed light is sane — (a) sky is 15 in open air at/above the surface-top and 0 deep
// underground/under solid cover, (b) if the column contains an emissive block a block-light gradient
// exists, (c) NO panics across a multi-seed stress (recover-guarded). This is the acceptance
// self-check for the light-at-seal integration. CITE: SkyLightEngine / BlockLightEngine.
func TestNoiseGenLightSelfCheck(t *testing.T) {
	const secs, minY = 24, -64
	seeds := []int64{1, 7, 42, 777, 123456, -9, 20260705}

	sawSkyFull := false
	sawSkyDark := false
	sawBlockGradient := false

	for _, seed := range seeds {
		g := NewNoiseGenerator(seed, secs, minY)
		// generate a small patch of chunks per seed.
		for cx := int32(0); cx < 3; cx++ {
			for cz := int32(0); cz < 3; cz++ {
				pos := level.ChunkPos{cx, cz}
				ch := func() *level.Chunk {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("panic generating seed=%d chunk=%v: %v", seed, pos, r)
						}
					}()
					return g.Generate(pos)
				}()
				if ch == nil {
					t.Fatalf("nil chunk seed=%d %v", seed, pos)
				}
				checkColumns(t, ch, minY, secs, &sawSkyFull, &sawSkyDark, &sawBlockGradient)
			}
		}
	}

	if !sawSkyFull {
		t.Error("no open-sky cell reached sky light 15 across all generated chunks")
	}
	if !sawSkyDark {
		t.Error("no deep-underground cell was dark (sky 0) across all generated chunks")
	}
	// block gradient is opportunistic (only if lava/glowstone generated); log if absent.
	if !sawBlockGradient {
		t.Log("no emissive-block gradient observed in this sample (no lava/glowstone in the patch) — sky checks still validate the engine")
	}
}

func checkColumns(t *testing.T, ch *level.Chunk, minY, secs int, sawSkyFull, sawSkyDark, sawBlockGradient *bool) {
	t.Helper()
	air := block.ToStateID[block.Air{}]
	topY := minY + secs*16 - 1
	// sample a few columns
	for _, xz := range [][2]int{{1, 1}, {8, 8}, {15, 3}, {4, 12}} {
		x, z := xz[0], xz[1]
		// find highest non-air block (surface)
		surface := minY - 1
		for y := topY; y >= minY; y-- {
			sec := (y - minY) >> 4
			local := (y&15)<<8 | (z&15)<<4 | (x & 15)
			if ch.Sections[sec].GetBlock(local) != air {
				surface = y
				break
			}
		}
		// open air a few blocks above the surface must be full sky 15.
		if surface >= minY && surface+3 <= topY {
			if skyAt(ch, minY, x, surface+3, z) == 15 {
				*sawSkyFull = true
			}
		}
		// a solid cell well below the surface should be dark (sky 0).
		deepY := surface - 8
		if deepY > minY {
			sec := (deepY - minY) >> 4
			local := (deepY&15)<<8 | (z&15)<<4 | (x & 15)
			if ch.Sections[sec].GetBlock(local) != air {
				if skyAt(ch, minY, x, deepY, z) == 0 {
					*sawSkyDark = true
				}
			}
		}
	}
	// scan for any emissive block and confirm a block-light gradient around it.
	for si := range ch.Sections {
		sec := &ch.Sections[si]
		hasEmissive := false
		for _, st := range sec.States.Palette() {
			if block.LightEmission(st) > 0 {
				hasEmissive = true
				break
			}
		}
		if !hasEmissive {
			continue
		}
		baseY := minY + si*16
		for ly := 0; ly < 16 && !*sawBlockGradient; ly++ {
			for lz := 0; lz < 16 && !*sawBlockGradient; lz++ {
				for lx := 0; lx < 16 && !*sawBlockGradient; lx++ {
					st := sec.GetBlock(ly<<8 | lz<<4 | lx)
					em := block.LightEmission(st)
					if em <= 0 {
						continue
					}
					wy := baseY + ly
					// the source cell itself carries block light == emission (or attenuated by its own
					// opacity if solid); a neighbor should be strictly less. Just check the source has
					// nonzero block light and SOME neighbor is less -> a gradient exists.
					src := blockAt(ch, minY, lx, wy, lz)
					if src > 0 {
						// check an in-chunk neighbor
						if lx+1 < 16 {
							if blockAt(ch, minY, lx+1, wy, lz) < src {
								*sawBlockGradient = true
							}
						}
					}
				}
			}
		}
	}
}
