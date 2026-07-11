// Package placement ports the Minecraft 26.2 (protocol 776) placement-modifier
// layer + the PlacedFeature.place ordered flatMap fold DIRECTLY from the
// unobfuscated server jar (temp/cache/26.2-inner.jar, read via `javap -c`).
//
// A PlacedFeature is "WHERE to build": an ordered list of PlacementModifiers that
// transform a single origin position into the set of anchor positions a feature
// body then runs at. Each modifier is getPositions(ctx, rng, pos) -> []BlockPos;
// the whole chain is threaded by ONE WorldgenRandom (seeded per-feature by
// WorldgenRandom.SetFeatureSeed upstream) in stream order. The RNG-draw sequence
// IS the determinism contract (research Pitfall 4): a wrong draw count, a goroutine,
// or a map iteration desyncs the entire decoration stream vs vanilla. Everything
// here is therefore an explicit single-threaded ordered port.
//
// This package is import-cycle clean: it imports level/block, level/biome, and
// world/levelgen (the RandomSource) and world/levelgen/feature (the parsed
// PlacedFeature). It does NOT import the world package — the reads the modifiers
// need over the live 3x3 worldgen neighborhood are abstracted behind the
// PlacementContext interface, whose concrete Neighborhood-backed impl plan 11-03
// supplies. That keeps placement fully buildable + testable against an in-memory
// fake context.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.placement.PlacementContext (getHeight/getMinY)
//   - net.minecraft.world.level.levelgen.Heightmap$Types            (WORLD_SURFACE_WG/OCEAN_FLOOR_WG/MOTION_BLOCKING)
//
// It is an algorithmic port, NOT a copy of Mojang source.
package placement

import (
	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
)

// HeightmapType selects which live worldgen heightmap a modifier reads. These are
// the subset of net.minecraft.world.level.levelgen.Heightmap$Types that the
// placement modifiers actually consume: the WORLDGEN (_WG) variants the
// Neighborhood maintains live mid-decoration (Phase 10), plus MOTION_BLOCKING.
//
// JAR-CONFIRMED: HeightmapPlacement reads the configured type; SurfaceWaterDepthFilter
// reads OCEAN_FLOOR and WORLD_SURFACE. The _WG vs client distinction is the
// generator's responsibility (11-03 backs these with the worldgen heightmaps).
type HeightmapType int

const (
	// WorldSurfaceWG is WORLD_SURFACE_WG: first Y above the highest non-air block.
	WorldSurfaceWG HeightmapType = iota
	// OceanFloorWG is OCEAN_FLOOR_WG: first Y above the highest motion-blocking,
	// non-fluid block (the floor beneath any water column).
	OceanFloorWG
	// MotionBlocking is MOTION_BLOCKING: first Y above the highest motion-blocking
	// OR fluid block (water counts).
	MotionBlocking
	// MotionBlockingNoLeaves is MOTION_BLOCKING_NO_LEAVES: like MOTION_BLOCKING but the
	// leaves layer is excluded from the top test (Heightmap$Types.MOTION_BLOCKING_NO_LEAVES).
	MotionBlockingNoLeaves
	// WorldSurface is WORLD_SURFACE (client variant); SurfaceWaterDepthFilter reads
	// it paired with OCEAN_FLOOR to compute the water-column depth.
	WorldSurface
	// OceanFloor is OCEAN_FLOOR (client variant); the partner read for
	// SurfaceWaterDepthFilter.
	OceanFloor
)

// String returns the vanilla heightmap id for debugging / JSON binding.
func (h HeightmapType) String() string {
	switch h {
	case WorldSurfaceWG:
		return "WORLD_SURFACE_WG"
	case OceanFloorWG:
		return "OCEAN_FLOOR_WG"
	case MotionBlocking:
		return "MOTION_BLOCKING"
	case MotionBlockingNoLeaves:
		return "MOTION_BLOCKING_NO_LEAVES"
	case WorldSurface:
		return "WORLD_SURFACE"
	case OceanFloor:
		return "OCEAN_FLOOR"
	default:
		return "<invalid heightmap>"
	}
}

// PlacementContext is the ONLY surface the placement modifiers need from the world
// / live worldgen Neighborhood. It is an INTERFACE so 11-02 stays free of a world
// import (no cycle, file-disjoint, testable with an in-memory fake); plan 11-03
// supplies the concrete Neighborhood-backed implementation.
//
// JAR mapping: GetHeight is PlacementContext.getHeight(Heightmap$Types,x,z); MinY
// is PlacementContext.getMinY() (== WorldGenerationContext.getMinGenY()); Height is
// the gen depth (WorldGenerationContext.getGenDepth()) — both are needed to resolve
// VerticalAnchor.below_top, which the overworld ore height_range placed_features use.
// GetBlock + BiomeAt back the two filters that read terrain/biome at a candidate.
type PlacementContext interface {
	// GetHeight reads the top Y of the live worldgen heightmap of type t at (x,z).
	GetHeight(t HeightmapType, x, z int) int
	// MinY is the world floor (WorldGenerationContext.getMinGenY()).
	MinY() int
	// Height is the gen depth (WorldGenerationContext.getGenDepth()); with MinY it
	// resolves below_top anchors: below_top(off) = (Height-1) + MinY - off.
	Height() int
	// GetBlock reads the live block state at (x,y,z) for terrain-aware filters.
	GetBlock(x, y, z int) block.StateID
	// BiomeAt reads the biome at (x,y,z) for the biome filter.
	BiomeAt(x, y, z int) biome.Type
}
