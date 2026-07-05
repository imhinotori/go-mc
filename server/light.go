package server

import (
	pk "github.com/imhinotori/sulfur/net/packet"
)

// server light READ seams — the tick-side getRawBrightness / getMaxLocalRawBrightness now backed by
// the real per-section light the LevelLightEngine computes at chunk finalize (world/light.go),
// replacing the former cited-constant 15. CITE: net.minecraft.world.level.lighting.LevelLightEngine
// .getRawBrightness + net.minecraft.world.level.Level.getMaxLocalRawBrightness.

// skyDarkenDay is the ambient-darkness term at full DAY: Level.getSkyDarken() = 15 -
// skyLightLevel(dimension env attribute); at day the sky light level is 15 so skyDarken == 0. The
// day/night env-attribute clock is a not-yet-built subsystem, so this is a CITED CONSTANT equal to
// the vanilla DAY default, structured to become a real getSkyDarken() read once that clock exists —
// never baked away. CITE: Level.updateSkyBrightness (skyDarken = 15 - SKY_LIGHT_LEVEL); at day == 0.
//
//	[DEFERRED: getSkyDarken() — no day/night sky-light-level env attribute in v1. CITE:
//	 Level.getMaxLocalRawBrightness(pos) = getRawBrightness(pos, getSkyDarken()). Follow-up: swap for
//	 the real skyDarken once the environment-attribute time clock lands.]
const skyDarkenDay = 0

// rawBrightness ports LevelLightEngine.getRawBrightness(pos, ambientDarkness) over the tick-owned
// world. A nil world (unit-test loop with no chunk manager) falls back to full brightness (15) so
// the light gates behave exactly as the previous cited-constant stub did in tests. CITE:
// LevelLightEngine.getRawBrightness.
func (t *TickLoop) rawBrightness(pos pk.Position, ambientDarkness int) int {
	w := t.world()
	if w == nil {
		return 15
	}
	return w.RawBrightness(pos, ambientDarkness, dimMinY)
}

// maxLocalRawBrightness ports Level.getMaxLocalRawBrightness(pos) = getRawBrightness(pos,
// getSkyDarken()). The X/Z far-bounds guard (>= 30000000 => 15) is mirrored for parity. CITE:
// Level.getMaxLocalRawBrightness.
func (t *TickLoop) maxLocalRawBrightness(pos pk.Position) int {
	if pos.X < -30000000 || pos.Z < -30000000 || pos.X >= 30000000 || pos.Z >= 30000000 {
		return 15
	}
	return t.rawBrightness(pos, skyDarkenDay)
}
