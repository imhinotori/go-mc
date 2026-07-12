package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestSkyDarkenTimeline pins Level.getSkyDarken() (env_timeline.go) at the canonical day-time markers
// against the values the vanilla SKY_LIGHT_LEVEL timeline (data/minecraft/timeline/day.json) produces:
// full day skyDarken == 0, dusk ramps up, midnight == 11 (15 - 15*0.26666668 == 15 - 4 == 11), dawn
// ramps back down. Values verified against the KeyframeTrackSampler.sample port (LINEAR ease, floorMod
// period 24000). getSkyDarken is deterministic from gametime and draws NO RNG.
func TestSkyDarkenTimeline(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	for _, tc := range []struct {
		gametime   int64
		wantDarken int
		desc       string
	}{
		{1000, 0, "day marker (SKY_LIGHT_LEVEL clamped to 15)"},
		{6000, 0, "noon (SKY_LIGHT_LEVEL 15)"},
		{11867, 0, "last full-light keyframe"},
		{13000, 6, "dusk ramp (between 11867->13670 keyframes)"},
		{13670, 11, "night keyframe (SKY_LIGHT_LEVEL 4.0 -> skyDarken 11)"},
		{18000, 11, "midnight (SKY_LIGHT_LEVEL 4.0 -> skyDarken 11)"},
		{22330, 11, "last night keyframe"},
		{23000, 6, "dawn ramp (22330->wrap keyframes)"},
		{24000 + 18000, 11, "next-day midnight (floorMod wrap)"},
		{24000 + 6000, 0, "next-day noon (floorMod wrap)"},
	} {
		loop.gametime = tc.gametime
		if got := loop.getSkyDarken(); got != tc.wantDarken {
			t.Fatalf("getSkyDarken() at gametime=%d (%s) = %d, want %d (SKY_LIGHT_LEVEL=%.6f)",
				tc.gametime, tc.desc, got, tc.wantDarken, dimensionSkyLightLevel(tc.gametime))
		}
	}
}

// TestSkyDarkenMidnightIs11 pins the single most load-bearing value: at midnight the overworld skyDarken
// is exactly 11 (the historic Level.getSkyDarken night value), from SKY_LIGHT_LEVEL == 15 * 0.26666668
// == 4.0 -> (int)(15.0 - 4.0) == 11. CITE: data/minecraft/timeline/day.json sky_light_level keyframe.
func TestSkyDarkenMidnightIs11(t *testing.T) {
	if v := dimensionSkyLightLevel(18000); v != 4.0 {
		t.Fatalf("SKY_LIGHT_LEVEL at midnight = %.6f, want 4.0", v)
	}
}

// TestSpiderIsBrightDayNight drives the SpiderAttackGoal daylight gate (ai_goals_attack.go isBright ->
// getLightLevelDependentMagicValue >= 0.5) over a fully sky-lit (SKY 15) surface cell at day vs night.
// At DAY (skyDarken 0) the surface reads raw brightness 15 -> magic 1.0 >= 0.5 -> BRIGHT (flee branch
// runs). At MIDNIGHT (skyDarken 11) the surface reads raw brightness 4 -> f=4/15 -> f1 ~0.083 < 0.5 ->
// NOT bright (target retained). This exercises the real light engine + the skyDarken timeline together.
func TestSpiderIsBrightDayNight(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)
	lightAllSpawnColumns(loop, 15) // fully sky-lit surface columns

	// Place a spider at a lit column; getLightLevelDependentMagicValue samples floor(y+0.65).
	sp := NewEntity(loop.idAlloc.AllocID(), entity.Spider, 8.5, float64(floorY+1), 8.5)
	g := &meleeAttackGoal{daylightGated: true}

	loop.withRegion(loop.only(), func() {
		// DAY: skyDarken 0 -> magic 1.0 -> bright.
		loop.gametime = 6000
		if !g.isBright(loop, sp) {
			t.Fatalf("spider isBright at noon (SKY 15, skyDarken 0) = false, want true; magic=%.4f",
				loop.getLightLevelDependentMagicValue(sp))
		}
		// MIDNIGHT: skyDarken 11 -> raw sky 4 -> magic ~0.083 < 0.5 -> not bright.
		loop.gametime = 18000
		if g.isBright(loop, sp) {
			t.Fatalf("spider isBright at midnight (SKY 15, skyDarken 11) = true, want false; magic=%.4f",
				loop.getLightLevelDependentMagicValue(sp))
		}
	})
}

// TestGetLightLevelDependentMagicValueFormula pins the numeric getLightLevelDependentMagicValue port at
// the two endpoints of a sky-lit column: raw 15 -> 1.0, raw 4 (midnight) -> 4/(15*(4-3*(4/15))). It uses
// the same lit world so maxLocalRawBrightness is exercised end-to-end. CITE:
// LevelReader.getLightLevelDependentMagicValue (overworld ambientLight 0.0 -> lerp yields f1).
func TestGetLightLevelDependentMagicValueFormula(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)
	lightAllSpawnColumns(loop, 15)
	sp := NewEntity(loop.idAlloc.AllocID(), entity.Spider, 8.5, float64(floorY+1), 8.5)
	loop.withRegion(loop.only(), func() {
		loop.gametime = 6000 // day: raw 15
		if got := loop.getLightLevelDependentMagicValue(sp); got != 1.0 {
			t.Fatalf("magic value at day (raw 15) = %.6f, want 1.0", got)
		}
		loop.gametime = 18000 // midnight: raw 4
		// f = 4/15; f1 = f/(4 - 3f).
		f := float32(4.0) / 15.0
		want := f / (4.0 - 3.0*f)
		if got := loop.getLightLevelDependentMagicValue(sp); got != want {
			t.Fatalf("magic value at midnight (raw 4) = %.6f, want %.6f", got, want)
		}
	})
}
