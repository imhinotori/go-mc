package lighting

import (
	"testing"

)

// full 3x3 so neighbor reads never hit an unloaded (bedrock) chunk.
func loaded3x3() [][2]int {
	var out [][2]int
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			out = append(out, [2]int{cx, cz})
		}
	}
	return out
}

// TestBlockLightTorchRadiates: a torch (emission 14) in open air radiates by taxicab distance,
// attenuating 1 per air step. CITE: BlockLightEngine.propagateIncrease (fromLevel - getOpacity).
func TestBlockLightTorchRadiates(t *testing.T) {
	w := newTestWorld(0, 4)
	for _, cc := range loaded3x3() {
		w.loaded[cc] = true
	}
	torch := stateOf(t, "torch")
	// place torch at (8, 8, 8)
	w.set(8, 8, 8, torch)
	eng := NewLevelLightEngine(w, true, false) // block-only
	w.initialLight(eng, loaded3x3())

	if got := eng.GetBrightness(LightLayerBlock, 8, 8, 8); got != 14 {
		t.Fatalf("torch cell = %d want 14", got)
	}
	// taxicab neighbors: 14 - dist (torch emission 14). Torch has empty shape so no occlusion.
	cases := []struct {
		x, y, z, want int
	}{
		{9, 8, 8, 13}, {7, 8, 8, 13}, {8, 9, 8, 13}, {8, 7, 8, 13}, {8, 8, 9, 13}, {8, 8, 7, 13},
		{10, 8, 8, 12}, {8, 8, 8 + 5, 9}, {8, 8 + 14, 8, 0}, // 14 blocks up -> 0
	}
	for _, c := range cases {
		if got := eng.GetBrightness(LightLayerBlock, c.x, c.y, c.z); got != c.want {
			t.Errorf("block light at (%d,%d,%d) = %d want %d", c.x, c.y, c.z, got, c.want)
		}
	}
}

// TestBlockLightGlowstone: glowstone (emission 15) — the source is 15 and a diagonal neighbor is
// 15 - taxicab. CITE: BlockLightEngine emission source.
func TestBlockLightGlowstone(t *testing.T) {
	w := newTestWorld(0, 4)
	for _, cc := range loaded3x3() {
		w.loaded[cc] = true
	}
	glow := stateOf(t, "glowstone")
	w.set(8, 8, 8, glow)
	eng := NewLevelLightEngine(w, true, false)
	w.initialLight(eng, loaded3x3())
	if got := eng.GetBrightness(LightLayerBlock, 8, 8, 8); got != 15 {
		t.Fatalf("glowstone cell = %d want 15", got)
	}
	// glowstone is a full opaque cube: light must exit into neighbors at 15 - opacity(air=1)?? No —
	// emission radiates from the source into neighbors: neighbor = 15 - getOpacity(neighborAir=1) =14.
	if got := eng.GetBrightness(LightLayerBlock, 9, 8, 8); got != 14 {
		t.Errorf("glowstone +x = %d want 14", got)
	}
}

// TestSkyLightAboveSurface: with a stone floor, sky light is 15 in open air above and drops under a
// solid cover. Verifies the top-down sky seeding + attenuation. CITE: SkyLightEngine.propagateLightSources.
func TestSkyLightSurfaceAndCover(t *testing.T) {
	w := newTestWorld(0, 4)
	for _, cc := range loaded3x3() {
		w.loaded[cc] = true
	}
	stone := stateOf(t, "stone")
	// solid stone floor at y=0..15 across all loaded chunks (so columns are opaque below y=16).
	for _, cc := range loaded3x3() {
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				for y := 0; y <= 15; y++ {
					w.set(cc[0]*16+lx, y, cc[1]*16+lz, stone)
				}
			}
		}
	}
	eng := NewLevelLightEngine(w, false, true) // sky-only
	w.rebuildSources()
	w.initialLight(eng, loaded3x3())

	// open air well above the floor -> full sky light 15
	if got := eng.GetBrightness(LightLayerSky, 8, 40, 8); got != 15 {
		t.Fatalf("open sky at y=40 = %d want 15", got)
	}
	if got := eng.GetBrightness(LightLayerSky, 8, 16, 8); got != 15 {
		t.Errorf("air just above floor = %d want 15", got)
	}
	// inside the stone floor -> 0 (opaque)
	if got := eng.GetBrightness(LightLayerSky, 8, 8, 8); got != 0 {
		t.Errorf("inside stone floor = %d want 0", got)
	}
}

// TestSkyLightGlassVsStoneCover: a glass block above a cell does NOT block sky-light-down the way a
// stone block does. Glass has lightBlock 0 and canOcclude=false (empty occlusion shape), so sky
// passes; stone (lightBlock 15) blocks. CITE: SkyLightEngine.propagatesSkylightDown / getOpacity +
// ChunkSkyLightSources.isEdgeOccluded.
func TestSkyLightGlassVsStoneCover(t *testing.T) {
	build := func(cover string) int {
		w := newTestWorld(0, 4)
		for _, cc := range loaded3x3() {
			w.loaded[cc] = true
		}
		coverState := stateOf(t, cover)
		// place a full 16x16 cover slab at y=32 across all loaded chunks, air below down to y=0.
		for _, cc := range loaded3x3() {
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					w.set(cc[0]*16+lx, 32, cc[1]*16+lz, coverState)
				}
			}
		}
		eng := NewLevelLightEngine(w, false, true)
		w.rebuildSources()
		w.initialLight(eng, loaded3x3())
		// read sky light in the air cell directly BELOW the cover.
		return eng.GetBrightness(LightLayerSky, 8, 31, 8)
	}
	glassBelow := build("glass")
	stoneBelow := build("stone")
	if glassBelow != 15 {
		t.Errorf("sky light below glass = %d want 15 (glass must not block sky-down)", glassBelow)
	}
	if stoneBelow != 0 {
		t.Errorf("sky light directly below solid stone cover = %d want 0", stoneBelow)
	}
	if !(glassBelow > stoneBelow) {
		t.Errorf("glass(%d) should pass more sky than stone(%d)", glassBelow, stoneBelow)
	}
}

// TestGetRawBrightnessCombines: getRawBrightness = max(block, sky - dampen). CITE:
// LevelLightEngine.getRawBrightness.
func TestGetRawBrightnessCombines(t *testing.T) {
	w := newTestWorld(0, 4)
	for _, cc := range loaded3x3() {
		w.loaded[cc] = true
	}
	torch := stateOf(t, "torch")
	w.set(8, 8, 8, torch)
	eng := NewLevelLightEngine(w, true, true)
	w.rebuildSources()
	w.initialLight(eng, loaded3x3())
	// open column: sky = 15 everywhere in air; block from torch = 14 at source.
	if got := eng.GetRawBrightness(8, 8, 8, 0); got != 15 {
		t.Errorf("raw brightness at torch (sky15,block14) = %d want 15", got)
	}
	// with skyDampen 15 (night), only block light remains -> 14 at source, 13 next to it.
	if got := eng.GetRawBrightness(8, 8, 8, 15); got != 14 {
		t.Errorf("raw brightness at torch, night = %d want 14", got)
	}
	if got := eng.GetRawBrightness(9, 8, 8, 15); got != 13 {
		t.Errorf("raw brightness next to torch, night = %d want 13", got)
	}
	// far from torch at night -> 0
	if got := eng.GetRawBrightness(8, 40, 8, 15); got != 0 {
		t.Errorf("raw brightness far, night = %d want 0", got)
	}
}

// TestIncrementalPlaceBreakGlowstone: placing a glowstone raises neighbor block light; breaking it
// (back to air) lowers it. CITE: LevelLightEngine.checkBlock + runLightUpdates.
func TestIncrementalPlaceBreakGlowstone(t *testing.T) {
	w := newTestWorld(0, 4)
	for _, cc := range loaded3x3() {
		w.loaded[cc] = true
	}
	eng := NewLevelLightEngine(w, true, false)
	w.initialLight(eng, loaded3x3())
	// initially dark
	if got := eng.GetBrightness(LightLayerBlock, 9, 8, 8); got != 0 {
		t.Fatalf("pre-place neighbor light = %d want 0", got)
	}
	// place glowstone at (8,8,8)
	glow := stateOf(t, "glowstone")
	w.set(8, 8, 8, glow)
	// section (0,0,0) transitions air->non-empty: the server calls updateSectionStatus before
	// checkBlock (LevelChunk.setBlockState -> LevelLightEngine). Mirror that here.
	eng.UpdateSectionStatus(0, 0, 0, false)
	eng.CheckBlock(8, 8, 8)
	eng.RunLightUpdates()
	if got := eng.GetBrightness(LightLayerBlock, 9, 8, 8); got != 14 {
		t.Errorf("post-place neighbor light = %d want 14", got)
	}
	if got := eng.GetBrightness(LightLayerBlock, 8, 8, 8); got != 15 {
		t.Errorf("post-place source light = %d want 15", got)
	}
	// break it (air) -> section (0,0,0) becomes empty again.
	w.set(8, 8, 8, airState)
	eng.CheckBlock(8, 8, 8)
	eng.RunLightUpdates()
	eng.UpdateSectionStatus(0, 0, 0, true)
	eng.RunLightUpdates()
	if got := eng.GetBrightness(LightLayerBlock, 9, 8, 8); got != 0 {
		t.Errorf("post-break neighbor light = %d want 0", got)
	}
	if got := eng.GetBrightness(LightLayerBlock, 8, 8, 8); got != 0 {
		t.Errorf("post-break source light = %d want 0", got)
	}
}

// TestBlockLightOpaqueBlocksPropagation: a wall of stone between a torch and a cell blocks the
// direct path (light routes around, attenuated). CITE: BlockLightEngine.propagateIncrease occlusion.
func TestBlockLightWallBlocks(t *testing.T) {
	w := newTestWorld(0, 4)
	for _, cc := range loaded3x3() {
		w.loaded[cc] = true
	}
	torch := stateOf(t, "torch")
	stone := stateOf(t, "stone")
	// torch at (8,8,8); stone wall at x=9 (the whole y/z plane slice near it)
	w.set(8, 8, 8, torch)
	w.set(9, 8, 8, stone)
	eng := NewLevelLightEngine(w, true, false)
	w.initialLight(eng, loaded3x3())
	// the stone cell itself gets 14-opacity(stone=15) -> clamped, stays 0 (opaque, no light stored)
	if got := eng.GetBrightness(LightLayerBlock, 9, 8, 8); got != 0 {
		t.Errorf("stone cell light = %d want 0", got)
	}
	// cell behind the wall at (10,8,8): light must route around (not straight through) -> < 13.
	got := eng.GetBrightness(LightLayerBlock, 10, 8, 8)
	if got >= 13 {
		t.Errorf("cell behind wall = %d want < 13 (routed around, not through opaque stone)", got)
	}
}
