package lighting

import "github.com/imhinotori/sulfur/level/block"

// This file ports the bit-packing helpers the light engine relies on:
// net.minecraft.core.BlockPos (blockNode longs) and net.minecraft.core.SectionPos (sectionNode
// longs). Every op is a literal transcription of the jar's shift/mask math so the packed longs
// (used as map keys and BFS node ids) are bit-identical to vanilla.

// --- BlockPos packing. CITE: net.minecraft.core.BlockPos. ---
//
// PACKED_HORIZONTAL_LENGTH = 1 + log2(smallestEncompassingPowerOfTwo(30000000)) = 1 + 25 = 26.
// PACKED_Y_LENGTH = 64 - 2*26 = 12. Y_OFFSET=0, Z_OFFSET=12, X_OFFSET=38.
const (
	packedHorizontalLength = 26
	packedYLength          = 12
	packedXMask            = (int64(1) << packedHorizontalLength) - 1
	packedYMask            = (int64(1) << packedYLength) - 1
	packedZMask            = (int64(1) << packedHorizontalLength) - 1
	yOffset                = 0
	zOffset                = packedYLength
	xOffset                = packedYLength + packedHorizontalLength
)

// blockAsLong ports BlockPos.asLong(x,y,z). CITE: BlockPos.asLong(int,int,int).
func blockAsLong(x, y, z int) int64 {
	var node int64
	node |= (int64(x) & packedXMask) << xOffset
	node |= (int64(y) & packedYMask) << yOffset
	node |= (int64(z) & packedZMask) << zOffset
	return node
}

// blockGetX/Y/Z port BlockPos.getX/getY/getZ via the sign-extending shift pair. CITE:
// BlockPos.getX/getY/getZ.
func blockGetX(n int64) int { return int((n << (64 - xOffset - packedHorizontalLength)) >> (64 - packedHorizontalLength)) }
func blockGetY(n int64) int { return int((n << (64 - packedYLength)) >> (64 - packedYLength)) }
func blockGetZ(n int64) int { return int((n << (64 - zOffset - packedHorizontalLength)) >> (64 - packedHorizontalLength)) }

// blockOffset ports BlockPos.offset(blockNode, Direction). CITE: BlockPos.offset(long,Direction)
// = asLong(getX+stepX, getY+stepY, getZ+stepZ). Direction steps mirror Direction.getNormal.
func blockOffset(n int64, dir block.Direction) int64 {
	dx, dy, dz := dirStep(dir)
	return blockAsLong(blockGetX(n)+dx, blockGetY(n)+dy, blockGetZ(n)+dz)
}

// blockGetFlatIndex ports BlockPos.getFlatIndex: zero out the Y bits (keep X/Z). CITE:
// BlockPos.getFlatIndex.
func blockGetFlatIndex(n int64) int64 { return n & ^(packedYMask << yOffset) }

// dirStep is Direction.getUnitVec3i / getNormal steps for the 6 directions, in
// (Down,Up,North,South,West,East) ordinal order. CITE: net.minecraft.core.Direction.
func dirStep(dir block.Direction) (int, int, int) {
	switch dir {
	case block.Down:
		return 0, -1, 0
	case block.Up:
		return 0, 1, 0
	case block.North:
		return 0, 0, -1
	case block.South:
		return 0, 0, 1
	case block.West:
		return -1, 0, 0
	case block.East:
		return 1, 0, 0
	}
	return 0, 0, 0
}

// oppositeDir ports Direction.getOpposite. CITE: Direction.getOpposite.
func oppositeDir(d block.Direction) block.Direction {
	switch d {
	case block.Down:
		return block.Up
	case block.Up:
		return block.Down
	case block.North:
		return block.South
	case block.South:
		return block.North
	case block.West:
		return block.East
	case block.East:
		return block.West
	}
	return d
}

// allDirections is Direction.values() in ordinal order = LightEngine.PROPAGATION_DIRECTIONS.
// CITE: LightEngine.PROPAGATION_DIRECTIONS = Direction.values().
var allDirections = [...]block.Direction{block.Down, block.Up, block.North, block.South, block.West, block.East}

// --- SectionPos packing. CITE: net.minecraft.core.SectionPos. ---
//
// asLong(x,y,z): x in bits [42..63] (22b), z in [20..41] (22b), y in [0..19] (20b).

const sectionPackedXZMask = int64(0x3FFFFF) // PACKED_X_MASK == PACKED_Z_MASK
const sectionPackedYMask = int64(0xFFFFF)   // PACKED_Y_MASK

func sectionAsLong(x, y, z int) int64 {
	var node int64
	node |= (int64(x) & sectionPackedXZMask) << 42
	node |= (int64(y) & sectionPackedYMask) << 0
	node |= (int64(z) & sectionPackedXZMask) << 20
	return node
}

// sectionX/Y/Z port SectionPos.x/y/z (sign-extending shifts). CITE: SectionPos.x/y/z.
func sectionX(n int64) int { return int((n << 0) >> 42) }
func sectionY(n int64) int { return int((n << 44) >> 44) }
func sectionZ(n int64) int { return int((n << 22) >> 42) }

// blockToSectionCoord ports SectionPos.blockToSectionCoord(int) = blockCoord >> 4. CITE.
func blockToSectionCoord(c int) int { return c >> 4 }

// sectionRelative ports SectionPos.sectionRelative(int) = blockCoord & 15. CITE.
func sectionRelative(c int) int { return c & 0xF }

// sectionToBlockCoord ports SectionPos.sectionToBlockCoord(int) = sectionCoord << 4. CITE.
func sectionToBlockCoord(c int) int { return c << 4 }

// blockNodeToSection ports SectionPos.blockToSection(long). CITE: SectionPos.blockToSection.
func blockNodeToSection(blockNode int64) int64 {
	return sectionAsLong(blockToSectionCoord(blockGetX(blockNode)),
		blockToSectionCoord(blockGetY(blockNode)),
		blockToSectionCoord(blockGetZ(blockNode)))
}

// getZeroNode ports SectionPos.getZeroNode(long) = sectionNode & 0xFFFFFFFFFFF00000. CITE.
func getZeroNode(sectionNode int64) int64 { return sectionNode & int64(-1048576) } // -0x100000

// getZeroNodeXZ ports SectionPos.getZeroNode(int x, int z). CITE.
func getZeroNodeXZ(x, z int) int64 { return getZeroNode(sectionAsLong(x, 0, z)) }

// sectionOffsetDir ports SectionPos.offset(long, Direction). CITE: SectionPos.offset(long,Direction).
func sectionOffsetDir(n int64, dir block.Direction) int64 {
	dx, dy, dz := dirStep(dir)
	return sectionAsLong(sectionX(n)+dx, sectionY(n)+dy, sectionZ(n)+dz)
}

// sectionOffset ports SectionPos.offset(long, int, int, int). CITE.
func sectionOffset(n int64, dx, dy, dz int) int64 {
	return sectionAsLong(sectionX(n)+dx, sectionY(n)+dy, sectionZ(n)+dz)
}

// --- ChunkPos packing (for the 2-entry chunk cache key). CITE: net.minecraft.world.level.ChunkPos.pack. ---
func chunkPosPack(x, z int) int64 { return (int64(x) & 0xFFFFFFFF) | ((int64(z) & 0xFFFFFFFF) << 32) }

// invalidChunkPos ports ChunkPos.INVALID_CHUNK_POS = pack(1875066, 1875066). CITE.
var invalidChunkPos = chunkPosPack(1875066, 1875066)
