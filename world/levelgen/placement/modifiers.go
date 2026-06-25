package placement

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// This file ports the 8 load-bearing placement modifiers with JAR-exact RNG-draw
// bodies (each transcribed from `javap -c`, cited inline). The draw sequence per
// modifier IS the determinism contract (research Pitfall 4): a wrong draw count
// desyncs the whole decoration stream vs vanilla.
//
// Sources (javap -c, 26.2-inner.jar, package
// net.minecraft.world.level.levelgen.placement):
//   - InSquarePlacement / HeightmapPlacement / HeightRangePlacement (direct getPositions)
//   - RarityFilter / BiomeFilter / SurfaceWaterDepthFilter (extend PlacementFilter)
//   - CountPlacement (extends RepeatingPlacement)

// ---- in_square ----

// InSquare ports InSquarePlacement (a stateless singleton in vanilla). getPositions
// jitters the input pos by nextInt(16) on X then Z, leaving Y. JAR-CONFIRMED draw
// order: X is drawn FIRST, then Z (two NextIntN(16) draws).
type InSquare struct{}

func (InSquare) getPositions(_ PlacementContext, rng levelgen.RandomSource, p BlockPos) []BlockPos {
	x := int(rng.NextIntN(16)) + p.X
	z := int(rng.NextIntN(16)) + p.Z
	return []BlockPos{{X: x, Y: p.Y, Z: z}}
}

// ---- heightmap ----

// Heightmap ports HeightmapPlacement: project (x,z) to the configured live worldgen
// heightmap top. JAR-CONFIRMED: emit {x, GetHeight(type,x,z), z} iff that height >
// MinY, else empty. Consumes 0 rng draws.
type Heightmap struct {
	heightmap HeightmapType
}

func (h Heightmap) getPositions(ctx PlacementContext, _ levelgen.RandomSource, p BlockPos) []BlockPos {
	y := ctx.GetHeight(h.heightmap, p.X, p.Z)
	if y <= ctx.MinY() {
		return nil
	}
	return []BlockPos{{X: p.X, Y: y, Z: p.Z}}
}

// ---- count ----

// Count ports CountPlacement (extends RepeatingPlacement): repeat the input pos
// count.Sample(rng) times. The IntProvider draws happen BEFORE the copies are
// emitted (RepeatingPlacement.getPositions evaluates count() first).
type Count struct {
	repeatingPlacement
	countProvider *intProvider
}

// count implements the RepeatingPlacement `counter`: CountPlacement.count(rng,pos) =
// the IntProvider's Sample(rng).
func (c *Count) count(rng levelgen.RandomSource, _ BlockPos) int {
	return c.countProvider.Sample(rng)
}

func newCount(p *intProvider) *Count {
	c := &Count{countProvider: p}
	c.repeatingPlacement.counter = c
	return c
}

// ---- rarity_filter ----

// RarityFilter ports RarityFilter (extends PlacementFilter): keep p iff
// nextFloat() < 1.0/chance. JAR-CONFIRMED: exactly one NextFloat per input pos.
type RarityFilter struct {
	placementFilter
	chance int
}

func (r *RarityFilter) shouldPlace(_ PlacementContext, rng levelgen.RandomSource, _ BlockPos) bool {
	return rng.NextFloat() < 1.0/float32(r.chance)
}

func newRarityFilter(chance int) *RarityFilter {
	r := &RarityFilter{chance: chance}
	r.placementFilter.predicate = r
	return r
}

// ---- biome ----

// BiomeFilter ports BiomeFilter (extends PlacementFilter): keep p iff the
// configured feature is allowed in the biome AT the candidate pos (re-checked per
// position — prevents a feature seeded in biome A from spilling into biome B).
// JAR-CONFIRMED: shouldPlace reads level.getBiome(pos) then checks
// biome.generationSettings.hasFeature(topFeature). Consumes 0 rng draws.
//
// For 11-02's self-contained design the allowed-biome check is a func(biome.Type)
// bool field (the placed_feature's biome allowance), supplied at bind time by
// 11-03/11-01. A nil predicate keeps every position (vanilla's BiomeFilter always
// has a registered feature; the nil-permissive default only matters to the
// in-package fold tests).
type BiomeFilter struct {
	placementFilter
	allowed func(biome.Type) bool
}

func (b *BiomeFilter) shouldPlace(ctx PlacementContext, _ levelgen.RandomSource, p BlockPos) bool {
	if b.allowed == nil {
		return true
	}
	return b.allowed(ctx.BiomeAt(p.X, p.Y, p.Z))
}

func newBiomeFilter(allowed func(biome.Type) bool) *BiomeFilter {
	b := &BiomeFilter{allowed: allowed}
	b.placementFilter.predicate = b
	return b
}

// ---- height_range ----

// HeightRange ports HeightRangePlacement: replace the input Y with
// heightProvider.Sample(rng, ctx), keeping X/Z (BlockPos.atY). JAR-CONFIRMED: one
// HeightProvider draw; never empty.
type HeightRange struct {
	height heightProvider
}

func (h HeightRange) getPositions(ctx PlacementContext, rng levelgen.RandomSource, p BlockPos) []BlockPos {
	y := h.height.sample(rng, ctx)
	return []BlockPos{{X: p.X, Y: y, Z: p.Z}}
}

// ---- surface_water_depth_filter ----

// SurfaceWaterDepthFilter ports SurfaceWaterDepthFilter (extends PlacementFilter):
// keep p iff the water-column depth at (x,z) <= maxWaterDepth.
//
// JAR-CONFIRMED: the depth is NOT a block scan — it is the difference of two
// heightmap reads: depth = GetHeight(WORLD_SURFACE,x,z) - GetHeight(OCEAN_FLOOR,x,z)
// (worldSurface counts the water top; oceanFloor is the solid floor beneath it).
// Consumes 0 rng draws. (Research described a block-scan; the bytecode is the
// authoritative heightmap-difference form ported here.)
type SurfaceWaterDepthFilter struct {
	placementFilter
	maxWaterDepth int
}

func (s *SurfaceWaterDepthFilter) shouldPlace(ctx PlacementContext, _ levelgen.RandomSource, p BlockPos) bool {
	oceanFloor := ctx.GetHeight(OceanFloor, p.X, p.Z)
	worldSurface := ctx.GetHeight(WorldSurface, p.X, p.Z)
	return worldSurface-oceanFloor <= s.maxWaterDepth
}

func newSurfaceWaterDepthFilter(maxWaterDepth int) *SurfaceWaterDepthFilter {
	s := &SurfaceWaterDepthFilter{maxWaterDepth: maxWaterDepth}
	s.placementFilter.predicate = s
	return s
}

// ---- compile-time interface assertions ----

var (
	_ PlacementModifier = InSquare{}
	_ PlacementModifier = Heightmap{}
	_ PlacementModifier = (*Count)(nil)
	_ PlacementModifier = (*RarityFilter)(nil)
	_ PlacementModifier = (*BiomeFilter)(nil)
	_ PlacementModifier = HeightRange{}
	_ PlacementModifier = (*SurfaceWaterDepthFilter)(nil)
)

// ---- JSON binding ----

// parseHeightmapType maps the placed_feature `heightmap` string to a HeightmapType,
// erroring loudly on an unknown id.
func parseHeightmapType(s string) (HeightmapType, error) {
	switch s {
	case "WORLD_SURFACE_WG":
		return WorldSurfaceWG, nil
	case "OCEAN_FLOOR_WG":
		return OceanFloorWG, nil
	case "MOTION_BLOCKING":
		return MotionBlocking, nil
	case "WORLD_SURFACE":
		return WorldSurface, nil
	case "OCEAN_FLOOR":
		return OceanFloor, nil
	default:
		return 0, fmt.Errorf("placement: unsupported heightmap type %q", s)
	}
}

// ModifierDeps carries the per-placed-feature dependencies a modifier needs at bind
// time that are NOT in its own JSON. Currently only the biome filter's allowed-biome
// predicate (supplied by 11-03 from the placed_feature's configured-feature biome
// allowance). A nil predicate makes the biome filter permissive.
type ModifierDeps struct {
	BiomeAllowed func(biome.Type) bool
}

// BindModifier turns one parsed PlacementModifierRaw envelope (type + raw config)
// into a concrete PlacementModifier. 11-03 calls this per modifier when binding a
// placed_feature. It errors loudly on an unported modifier type (the Nether/cave set
// is deferred per research) — never silently dropping a modifier (T-11-05).
func BindModifier(modType string, raw json.RawMessage, deps ModifierDeps) (PlacementModifier, error) {
	switch modType {
	case "minecraft:in_square", "in_square":
		return InSquare{}, nil

	case "minecraft:heightmap", "heightmap":
		var cfg struct {
			Heightmap string `json:"heightmap"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: heightmap modifier: %w", err)
		}
		ht, err := parseHeightmapType(cfg.Heightmap)
		if err != nil {
			return nil, err
		}
		return Heightmap{heightmap: ht}, nil

	case "minecraft:count", "count":
		var cfg struct {
			Count json.RawMessage `json:"count"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: count modifier: %w", err)
		}
		ip, err := parseIntProvider(cfg.Count)
		if err != nil {
			return nil, fmt.Errorf("placement: count modifier: %w", err)
		}
		return newCount(ip), nil

	case "minecraft:rarity_filter", "rarity_filter":
		var cfg struct {
			Chance int `json:"chance"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: rarity_filter modifier: %w", err)
		}
		if cfg.Chance <= 0 {
			return nil, fmt.Errorf("placement: rarity_filter chance must be positive, got %d", cfg.Chance)
		}
		return newRarityFilter(cfg.Chance), nil

	case "minecraft:biome", "biome":
		return newBiomeFilter(deps.BiomeAllowed), nil

	case "minecraft:height_range", "height_range":
		var cfg struct {
			Height json.RawMessage `json:"height"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: height_range modifier: %w", err)
		}
		hp, err := parseHeightProvider(cfg.Height)
		if err != nil {
			return nil, fmt.Errorf("placement: height_range modifier: %w", err)
		}
		return HeightRange{height: hp}, nil

	case "minecraft:surface_water_depth_filter", "surface_water_depth_filter":
		var cfg struct {
			MaxWaterDepth int `json:"max_water_depth"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: surface_water_depth_filter modifier: %w", err)
		}
		return newSurfaceWaterDepthFilter(cfg.MaxWaterDepth), nil

	default:
		return nil, fmt.Errorf("placement: unported placement modifier type %q (defer the Nether/cave set per research)", modType)
	}
}
