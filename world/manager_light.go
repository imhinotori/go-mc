package world

import (
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// colOf maps a world block pos to its column, matching columnAndSection / SetBlock (floorDiv16).
func colOf(pos pk.Position) level.ChunkPos {
	return level.ChunkPos{int32(floorDiv16(pos.X)), int32(floorDiv16(pos.Z))}
}

// This file exposes the tick-side light READS backed by the per-section light arrays the
// LevelLightEngine computes at chunk finalize (world/light.go). It is the read half of the vanilla
// LevelLightEngine.getRawBrightness / getBrightness — the values ARE the engine's output, read from
// the stored chunk light rather than recomputed. CITE: LevelLightEngine.getRawBrightness /
// getBrightness(LightLayer, BlockPos).

// nibbleAt reads a 4-bit light value from a 2048-byte DataLayer array at local index
// (y&15)<<8 | (z&15)<<4 | (x&15). A nil array reads as 0. Mirrors DataLayer.get. CITE: DataLayer.get.
func nibbleAt(arr []byte, local int) int {
	if arr == nil {
		return 0
	}
	return int(arr[local>>1]>>(4*(local&1))) & 0xF
}

// SkyBrightness returns the SKY light at a world pos (LevelLightEngine.getBrightness(SKY, pos)).
// Above the loaded section column it is the open-sky default 15; an unloaded column reads 0. CITE:
// SkyLightSectionStorage.getLightValue (above-top => 15).
func (m *ChunkManager) SkyBrightness(pos pk.Position, minY int) int {
	col := colOf(pos)
	ch, loaded := m.Get(col)
	if !loaded {
		return 0
	}
	sec := (pos.Y - minY) >> 4
	if sec >= len(ch.Sections) {
		return 15 // above the top section: open sky
	}
	if sec < 0 {
		return 0 // below the world
	}
	local := (pos.Y&15)<<8 | (pos.Z&15)<<4 | (pos.X & 15)
	return nibbleAt(ch.Sections[sec].SkyLight, local)
}

// BlockBrightness returns the BLOCK light at a world pos (LevelLightEngine.getBrightness(BLOCK,
// pos)). An unloaded column or out-of-range y reads 0. CITE: BlockLightSectionStorage.getLightValue.
func (m *ChunkManager) BlockBrightness(pos pk.Position, minY int) int {
	col := colOf(pos)
	ch, loaded := m.Get(col)
	if !loaded {
		return 0
	}
	sec := (pos.Y - minY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return 0
	}
	local := (pos.Y&15)<<8 | (pos.Z&15)<<4 | (pos.X & 15)
	return nibbleAt(ch.Sections[sec].BlockLight, local)
}

// RawBrightness ports LevelLightEngine.getRawBrightness(pos, ambientDarkness) = max(blockLight,
// skyLight - ambientDarkness). ambientDarkness is the level's skyDarken (0 in full day, up to 11 at
// night for the crop/growth `getRawBrightness(pos, 0)` seam callers pass 0). CITE:
// LevelLightEngine.getRawBrightness.
func (m *ChunkManager) RawBrightness(pos pk.Position, ambientDarkness, minY int) int {
	sky := m.SkyBrightness(pos, minY) - ambientDarkness
	blk := m.BlockBrightness(pos, minY)
	if blk > sky {
		return blk
	}
	return sky
}

// MaxLocalRawBrightness ports Level.getMaxLocalRawBrightness(pos) = getRawBrightness(pos,
// ambientDarkness) with the level's CURRENT skyDarken. The crop/sapling/spread callers use it for
// the >= 9 growth gates. skyDarken is supplied by the caller (the tick's day/night state). CITE:
// net.minecraft.world.level.Level.getMaxLocalRawBrightness (getSkyDarken() as the ambient darkness).
func (m *ChunkManager) MaxLocalRawBrightness(pos pk.Position, skyDarken, minY int) int {
	return m.RawBrightness(pos, skyDarken, minY)
}
