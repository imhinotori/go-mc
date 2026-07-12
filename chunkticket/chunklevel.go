// Package chunkticket is a 1:1 port of the vanilla 26.2 chunk ticket -> level ->
// status subsystem: net.minecraft.server.level.{TicketType,Ticket,ChunkLevel,
// FullChunkStatus,DistanceManager,ChunkTracker}, net.minecraft.world.level.
// TicketStorage, and the propagation core net.minecraft.world.level.lighting.
// DynamicGraphMinFixedPoint.
//
// A chunk's ticket level is the minimum, over all its tickets, of the ticket
// level; that minimum is propagated outward one chunk at a time, +1 level per
// chunk of Chebyshev distance, by a fixed-point min-graph (the same algorithm
// vanilla shares with the light engine). The resulting per-chunk level then maps
// to a FullChunkStatus (ENTITY_TICKING / BLOCK_TICKING / FULL / INACCESSIBLE) and,
// for lower levels, to a generation ChunkStatus.
//
// This package is authoritative for computing levels/statuses; the existing
// view-distance loader in server/ observes it so the observable loaded set matches
// vanilla's ticket model for a player at view distance N.
package chunkticket

import "github.com/imhinotori/sulfur/level"

// FullChunkStatus mirrors net.minecraft.server.level.FullChunkStatus. The ordinal
// order (INACCESSIBLE < FULL < BLOCK_TICKING < ENTITY_TICKING) matches the Java enum
// declaration order, so IsOrAfter is a simple >= on the ordinal.
// CITE: FullChunkStatus enum (INACCESSIBLE, FULL, BLOCK_TICKING, ENTITY_TICKING).
type FullChunkStatus int

const (
	Inaccessible FullChunkStatus = iota
	Full
	BlockTicking
	EntityTicking
)

// IsOrAfter mirrors FullChunkStatus.isOrAfter (ordinal comparison).
func (s FullChunkStatus) IsOrAfter(o FullChunkStatus) bool { return s >= o }

// ChunkLevel constants, CITE: net.minecraft.server.level.ChunkLevel.
//
//	FULL_CHUNK_LEVEL     = byStatus(FullChunkStatus.FULL)          = 33
//	BLOCK_TICKING_LEVEL  = byStatus(FullChunkStatus.BLOCK_TICKING) = 32
//	ENTITY_TICKING_LEVEL = byStatus(FullChunkStatus.ENTITY_TICKING)= 31
//	RADIUS_AROUND_FULL_CHUNK = FULL_CHUNK_STEP.accumulatedDependencies().getRadius() = 8
//	MAX_LEVEL = FULL_CHUNK_LEVEL(33) + RADIUS_AROUND_FULL_CHUNK(8) = 41
//
// RADIUS_AROUND_FULL_CHUNK is derived at runtime in Java from the ChunkPyramid
// GENERATION_PYRAMID step to FULL. The full ChunkPyramid/ChunkStep/ChunkDependencies
// dependency graph is not yet ported, so the accumulated radius (8) is pinned here as
// the bytecode-verified vanilla constant. STUB (cited): ChunkLevel.<clinit>
// RADIUS_AROUND_FULL_CHUNK = FULL_CHUNK_STEP.accumulatedDependencies().getRadius().
const (
	FullChunkLevel     = 33
	BlockTickingLevel  = 32
	EntityTickingLevel = 31

	RadiusAroundFullChunk = 8
	MaxLevel              = FullChunkLevel + RadiusAroundFullChunk // 41
)

// FullStatus maps a ticket level to a FullChunkStatus.
// CITE: ChunkLevel.fullStatus(int):
//
//	level <= 31 -> ENTITY_TICKING
//	level <= 32 -> BLOCK_TICKING
//	level <= 33 -> FULL
//	else        -> INACCESSIBLE
func FullStatus(lvl int) FullChunkStatus {
	switch {
	case lvl <= EntityTickingLevel:
		return EntityTicking
	case lvl <= BlockTickingLevel:
		return BlockTicking
	case lvl <= FullChunkLevel:
		return Full
	default:
		return Inaccessible
	}
}

// ByStatus maps a FullChunkStatus to the highest ticket level that still yields it.
// CITE: ChunkLevel.byStatus(FullChunkStatus):
//
//	ENTITY_TICKING -> 31
//	BLOCK_TICKING  -> 32
//	FULL           -> 33
//	INACCESSIBLE   -> MAX_LEVEL (41)
func ByStatus(s FullChunkStatus) int {
	switch s {
	case EntityTicking:
		return EntityTickingLevel
	case BlockTicking:
		return BlockTickingLevel
	case Full:
		return FullChunkLevel
	case Inaccessible:
		return MaxLevel
	default:
		return MaxLevel
	}
}

// IsEntityTicking mirrors ChunkLevel.isEntityTicking: level <= ENTITY_TICKING_LEVEL(31).
func IsEntityTicking(lvl int) bool { return lvl <= EntityTickingLevel }

// IsBlockTicking mirrors ChunkLevel.isBlockTicking: level <= BLOCK_TICKING_LEVEL(32).
func IsBlockTicking(lvl int) bool { return lvl <= BlockTickingLevel }

// IsLoaded mirrors ChunkLevel.isLoaded: level <= MAX_LEVEL(41).
func IsLoaded(lvl int) bool { return lvl <= MaxLevel }

// generationStatusByDistance is the ChunkStatus for a chunk at a given Chebyshev
// distance beyond the full chunk (index = level - FULL_CHUNK_LEVEL). This is the
// accumulated-dependency table of the GENERATION_PYRAMID step to FULL, i.e. the value
// of FULL_CHUNK_STEP.accumulatedDependencies().get(distance) for distance 1..RADIUS
// (distance 0 is FULL itself). CITE: ChunkLevel.getStatusAroundFullChunk +
// ChunkPyramid.GENERATION_PYRAMID. STUB (cited): the exact ChunkDependencies radius
// list is reproduced from the stable GENERATION_PYRAMID rather than recomputed from a
// full ChunkStep port. Index 0 => FULL, 1..8 => the surrounding generation rings.
var generationStatusByDistance = []level.ChunkStatus{
	level.StatusFull,            // 0
	level.StatusFeatures,        // 1
	level.StatusFeatures,        // 2
	level.StatusLight,           // 3
	level.StatusSurface,         // 4
	level.StatusSurface,         // 5
	level.StatusNoise,           // 6
	level.StatusNoise,           // 7
	level.StatusStructureStarts, // 8
}

// GenerationStatus maps a ticket level to the ChunkStatus a chunk at that level should
// be generated to. CITE: ChunkLevel.generationStatus(int) ==
// getStatusAroundFullChunk(level - 33, null): returns not-generated when the distance
// exceeds RADIUS_AROUND_FULL_CHUNK, FULL when distance <= 0, else the
// accumulated-dependency status at that distance.
func GenerationStatus(lvl int) (level.ChunkStatus, bool) {
	dist := lvl - FullChunkLevel
	if dist > RadiusAroundFullChunk {
		return "", false // vanilla returns the passed-in default (null) => not generated
	}
	if dist <= 0 {
		return level.StatusFull, true
	}
	return generationStatusByDistance[dist], true
}
