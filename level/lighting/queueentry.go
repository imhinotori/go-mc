package lighting

import "github.com/imhinotori/sulfur/level/block"

// QueueEntry ports net.minecraft.world.level.lighting.LightEngine$QueueEntry: the bit-packed
// increase/decrease BFS entry (a long). Layout: bits[0..3] level, bits[4..9] the 6 direction flags,
// bit10 FLAG_FROM_EMPTY_SHAPE, bit11 FLAG_INCREASE_FROM_EMISSION. Every constant and op below is a
// literal transcription of the jar. CITE: LightEngine$QueueEntry.
const (
	qeDirectionsMask         = int64(1008) // bits 4..9 = 0b1111110000
	qeFlagFromEmptyShape     = int64(1024) // bit 10
	qeFlagIncreaseFromEmission = int64(2048) // bit 11
)

func qeDecreaseSkipOneDirection(oldFromLevel int, skipDir block.Direction) int64 {
	data := qeWithoutDirection(qeDirectionsMask, skipDir)
	return qeWithLevel(data, oldFromLevel)
}

func qeDecreaseAllDirections(oldFromLevel int) int64 {
	return qeWithLevel(qeDirectionsMask, oldFromLevel)
}

func qeIncreaseLightFromEmission(newFromLevel int, fromEmptyShape bool) int64 {
	data := qeDirectionsMask
	data |= qeFlagIncreaseFromEmission
	if fromEmptyShape {
		data |= qeFlagFromEmptyShape
	}
	return qeWithLevel(data, newFromLevel)
}

func qeIncreaseSkipOneDirection(newFromLevel int, fromEmptyShape bool, skipDir block.Direction) int64 {
	data := qeWithoutDirection(qeDirectionsMask, skipDir)
	if fromEmptyShape {
		data |= qeFlagFromEmptyShape
	}
	return qeWithLevel(data, newFromLevel)
}

func qeIncreaseOnlyOneDirection(newFromLevel int, fromEmptyShape bool, dir block.Direction) int64 {
	var data int64
	if fromEmptyShape {
		data |= qeFlagFromEmptyShape
	}
	data = qeWithDirection(data, dir)
	return qeWithLevel(data, newFromLevel)
}

func qeIncreaseSkySourceInDirections(down, north, south, west, east bool) int64 {
	data := qeWithLevel(0, 15)
	if down {
		data = qeWithDirection(data, block.Down)
	}
	if north {
		data = qeWithDirection(data, block.North)
	}
	if south {
		data = qeWithDirection(data, block.South)
	}
	if west {
		data = qeWithDirection(data, block.West)
	}
	if east {
		data = qeWithDirection(data, block.East)
	}
	return data
}

func qeGetFromLevel(entry int64) int { return int(entry & 0xF) }

func qeIsFromEmptyShape(entry int64) bool { return entry&qeFlagFromEmptyShape != 0 }

func qeIsIncreaseFromEmission(entry int64) bool { return entry&qeFlagIncreaseFromEmission != 0 }

// qeShouldPropagateInDirection ports shouldPropagateInDirection = (entry & 1<<(dir.ordinal()+4))!=0.
func qeShouldPropagateInDirection(entry int64, dir block.Direction) bool {
	return entry&(1<<(uint(dir)+4)) != 0
}

func qeWithLevel(entry int64, level int) int64 {
	return entry&^int64(0xF) | (int64(level) & 0xF)
}

func qeWithDirection(entry int64, dir block.Direction) int64 {
	return entry | (1 << (uint(dir) + 4))
}

func qeWithoutDirection(entry int64, dir block.Direction) int64 {
	return entry & ^(int64(1) << (uint(dir) + 4))
}
