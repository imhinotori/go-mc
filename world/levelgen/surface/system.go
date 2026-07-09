package surface

import (
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	lgrandom "github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/noisechunk"
	"github.com/imhinotori/sulfur/world/levelgen/router"
	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// BlockColumn ports net.minecraft.world.level.chunk.BlockColumn for the surface walk:
// the per-column read/write surface buildSurface operates on. getBlock/setBlock index
// by ABSOLUTE world Y (the same convention vanilla's BlockColumn uses). chunkColumn
// (below) adapts a *level.Chunk column to it.
type BlockColumn interface {
	getBlock(y int) block.StateID
	setBlock(y int, st block.StateID)
}

// SurfaceSystem ports net.minecraft.world.level.levelgen.SurfaceSystem: the seeded
// surface noises + the clay-band table + the per-column buildSurface driver. It is
// PURE over the world seed (Pitfall 7): every noise/random is forked from the
// RandomState's base positional factory.
type SurfaceSystem struct {
	defaultBlock block.StateID
	seaLevel     int

	noiseRandom lgrandom.PositionalRandomFactory // base.forkPositional() — the surfaceDepth + vertical_gradient draws

	surfaceNoise          *synth.NormalNoise // minecraft:surface (getSurfaceDepth)
	surfaceSecondaryNoise *synth.NormalNoise // minecraft:surface_secondary (getSurfaceSecondary)

	clayBandsOffsetNoise *synth.NormalNoise // minecraft:clay_bands_offset (getBand)
	clayBands            []block.StateID    // the 192-entry generated terracotta band table

	// noiseCache memoizes the seeded NormalNoise per registry id for the
	// noise_threshold conditions (surface / surface_swamp / calcite / gravel / ...).
	rstate     *router.RandomState
	noiseCache map[string]*synth.NormalNoise

	// randomFactoryCache memoizes the per-name positional factory the
	// vertical_gradient condition draws from (getOrCreateRandomFactory).
	randomFactoryCache map[string]lgrandom.PositionalRandomFactory

	// cacheMu guards noiseCache + randomFactoryCache. The SurfaceSystem is built ONCE per world
	// (NewNoiseGenerator) and SHARED across the parallel chunk-gen worker goroutines
	// (Worker.handleTerrain runs many chunks concurrently), which lazily populate these two maps
	// — an unsynchronized map read+write across goroutines is a `fatal error: concurrent map read
	// and map write` crash (observed in production). A plain mutex is correct here: the maps fill
	// quickly (one entry per distinct noise id / factory name) then become read-mostly, so the
	// lock is near-uncontended after warmup. The seeding itself (rstate.NormalNoise / fromHashOf)
	// is pure over (seed, id), so two goroutines racing to fill the same key produce identical
	// values — the lock only prevents the map-structure corruption, not a value divergence.
	cacheMu sync.Mutex
}

// randomFactory ports RandomState.getOrCreateRandomFactory(id) =
// base.fromHashOf(id).forkPositional(), memoized per name (the vertical_gradient
// surface rule's seeded random).
func (s *SurfaceSystem) randomFactory(name string) lgrandom.PositionalRandomFactory {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if f, ok := s.randomFactoryCache[name]; ok {
		return f
	}
	f := s.noiseRandom.FromHashOf(name).ForkPositional()
	s.randomFactoryCache[name] = f
	return f
}

// NewSurfaceSystem ports the SurfaceSystem constructor: it seeds surfaceNoise (SURFACE),
// surfaceSecondaryNoise (SURFACE_SECONDARY) and clayBandsOffsetNoise (CLAY_BANDS_OFFSET)
// via RandomState.getOrCreateNoise, takes the base positional factory as noiseRandom,
// and generates the clay-band table from noiseRandom.fromHashOf("clay_bands"). The
// defaultBlock is the settings default_block (stone) — buildSurface only rewrites
// columns whose top is THAT block (so it never clobbers ore veins/water).
func NewSurfaceSystem(r *router.Router) (*SurfaceSystem, error) {
	rstate := r.Random

	surfaceNoise, err := rstate.NormalNoise("minecraft:surface")
	if err != nil {
		return nil, fmt.Errorf("surface system: surface noise: %w", err)
	}
	surfaceSecondaryNoise, err := rstate.NormalNoise("minecraft:surface_secondary")
	if err != nil {
		return nil, fmt.Errorf("surface system: surface_secondary noise: %w", err)
	}
	clayBandsOffsetNoise, err := rstate.NormalNoise("minecraft:clay_bands_offset")
	if err != nil {
		return nil, fmt.Errorf("surface system: clay_bands_offset noise: %w", err)
	}

	defaultBlock, ok := block.FromID[r.Settings.DefaultBlock]
	if !ok {
		return nil, fmt.Errorf("surface system: unknown default_block %q", r.Settings.DefaultBlock)
	}
	defaultSID, ok := block.ToStateID[defaultBlock]
	if !ok {
		return nil, fmt.Errorf("surface system: no state id for default_block %q", r.Settings.DefaultBlock)
	}

	noiseRandom := rstate.BaseFactory()

	bands, err := generateBands(noiseRandom.FromHashOf("minecraft:clay_bands"))
	if err != nil {
		return nil, fmt.Errorf("surface system: generate clay bands: %w", err)
	}

	return &SurfaceSystem{
		defaultBlock:          defaultSID,
		seaLevel:              r.Settings.SeaLevel,
		noiseRandom:           noiseRandom,
		surfaceNoise:          surfaceNoise,
		surfaceSecondaryNoise: surfaceSecondaryNoise,
		clayBandsOffsetNoise:  clayBandsOffsetNoise,
		clayBands:             bands,
		rstate:                rstate,
		noiseCache:            make(map[string]*synth.NormalNoise),
		randomFactoryCache:    make(map[string]lgrandom.PositionalRandomFactory),
	}, nil
}

// getSurfaceDepth ports SurfaceSystem.getSurfaceDepth(x,z):
//
//	(int)(surfaceNoise.getValue(x,0,z)*2.75 + 3.0 + noiseRandom.at(x,0,z).nextDouble()*0.25)
//
// This is the thickness of the surface band (how deep grass→dirt→stone goes) at the
// column — the load-bearing input the stone_depth/y_above conditions add into their
// budget.
func (s *SurfaceSystem) getSurfaceDepth(x, z int) int {
	n := s.surfaceNoise.GetValue(float64(x), 0, float64(z))
	jitter := s.noiseRandom.At(x, 0, z).NextDouble()
	return int(n*2.75 + 3.0 + jitter*0.25)
}

// getSurfaceSecondary ports SurfaceSystem.getSurfaceSecondary(x,z):
// surfaceSecondaryNoise.getValue(x,0,z) — the secondary surface noise the stone_depth
// secondaryDepthRange maps into extra depth.
func (s *SurfaceSystem) getSurfaceSecondary(x, z int) float64 {
	return s.surfaceSecondaryNoise.GetValue(float64(x), 0, float64(z))
}

// getBand ports SurfaceSystem.getBand(x,y,z): the badlands terracotta band for the
// position. i = round(clayBandsOffsetNoise.getValue(x,0,z)*4); return
// clayBands[(y + i + len) % len].
func (s *SurfaceSystem) getBand(x, y, z int) block.StateID {
	off := int(roundHalfUp(s.clayBandsOffsetNoise.GetValue(float64(x), 0, float64(z)) * 4.0))
	n := len(s.clayBands)
	idx := ((y + off + n) % n)
	if idx < 0 {
		idx += n
	}
	return s.clayBands[idx]
}

// surfaceNoiseValue returns the named surface noise sampled 2-D at (x,0,z), seeding +
// caching the NormalNoise on first use (SurfaceRules$Context.createNoiseSampler2d).
func (s *SurfaceSystem) surfaceNoiseValue(noiseID string, x, z int) (float64, error) {
	s.cacheMu.Lock()
	n, ok := s.noiseCache[noiseID]
	if !ok {
		var err error
		n, err = s.rstate.NormalNoise(noiseID)
		if err != nil {
			s.cacheMu.Unlock()
			return 0, err
		}
		s.noiseCache[noiseID] = n
	}
	s.cacheMu.Unlock()
	// GetValue is a pure read on the seeded noise (no shared mutable state), so it runs OUTSIDE
	// the lock — only the map access needs protecting.
	return n.GetValue(float64(x), 0, float64(z)), nil
}

// roundHalfUp ports Math.round(double) = floor(d + 0.5).
func roundHalfUp(d float64) int64 {
	return int64(mthFloorF(d + 0.5))
}

func mthFloorF(d float64) float64 {
	i := float64(int64(d))
	if i > d {
		i--
	}
	return i
}

// ---- generateBands / makeBands (the badlands clay-band table) ----

// generateBands ports SurfaceSystem.generateBands(RandomSource): a 192-entry terracotta
// band table. Start all TERRACOTTA; sprinkle ORANGE_TERRACOTTA runs; lay yellow/brown/
// red bands; then a run of white (with optional light-gray edges). All draws flow from
// the seeded clay_bands RandomSource (deterministic). Resolved to StateIDs here.
func generateBands(rng lgrandom.RandomSource) ([]block.StateID, error) {
	terracotta, err := stateID("minecraft:terracotta")
	if err != nil {
		return nil, err
	}
	orange, err := stateID("minecraft:orange_terracotta")
	if err != nil {
		return nil, err
	}
	yellow, err := stateID("minecraft:yellow_terracotta")
	if err != nil {
		return nil, err
	}
	brown, err := stateID("minecraft:brown_terracotta")
	if err != nil {
		return nil, err
	}
	red, err := stateID("minecraft:red_terracotta")
	if err != nil {
		return nil, err
	}
	white, err := stateID("minecraft:white_terracotta")
	if err != nil {
		return nil, err
	}
	lightGray, err := stateID("minecraft:light_gray_terracotta")
	if err != nil {
		return nil, err
	}

	bands := make([]block.StateID, 192)
	for i := range bands {
		bands[i] = terracotta
	}
	// Orange terracotta runs. SurfaceSystem.generateBands bytecode 14-49: the loop body
	// INCREMENTS i FIRST (i += nextInt(5)+1 at offset 22-33) THEN assigns bands[i]=orange
	// (if in bounds) THEN i++ (iinc 2,1 at offset 46). So bands[0] is never assigned and
	// the effective per-iteration stride is nextInt(5)+2.
	for i := 0; i < len(bands); i++ {
		i += int(rng.NextIntN(5)) + 1
		if i < len(bands) {
			bands[i] = orange
		}
	}
	makeBands(rng, bands, 1, yellow)
	makeBands(rng, bands, 2, brown)
	makeBands(rng, bands, 1, red)

	// White band run (length 9..15) with optional light-gray edges.
	// SurfaceSystem.generateBands bytecode 90-184: idx advances by nextInt(16)+4 per
	// iteration (offset 169-182) and the light-gray edges use nextBoolean() (offset 121,
	// 148) -- NOT nextFloat()<0.5 (a different RandomSource draw). The lower guard is
	// (idx-1) > 0 (offset 114-118, strictly greater than zero).
	whiteCount := nextIntBetweenInclusive(rng, 9, 15)
	placed := 0
	for j := 0; placed < whiteCount && j < len(bands); j += int(rng.NextIntN(16)) + 4 {
		bands[j] = white
		if j-1 > 0 && rng.NextBoolean() {
			bands[j-1] = lightGray
		}
		if j+1 < len(bands) && rng.NextBoolean() {
			bands[j+1] = lightGray
		}
		placed++
	}
	return bands, nil
}

// makeBands ports SurfaceSystem.makeBands(rng, bands, minThickness, state): lay
// (nextIntBetweenInclusive(6,15)) runs of `state`, each at a random start with a random
// thickness (minThickness + nextInt(3)).
func makeBands(rng lgrandom.RandomSource, bands []block.StateID, minThickness int, state block.StateID) {
	runs := nextIntBetweenInclusive(rng, 6, 15)
	for i := 0; i < runs; i++ {
		thickness := minThickness + int(rng.NextIntN(3))
		start := int(rng.NextIntN(int32(len(bands))))
		for j := 0; start+j < len(bands) && j < thickness; j++ {
			bands[start+j] = state
		}
	}
}

// nextIntBetweenInclusive ports RandomSource.nextIntBetweenInclusive(min,max) =
// min + nextInt(max-min+1).
func nextIntBetweenInclusive(rng lgrandom.RandomSource, min, max int) int {
	return min + int(rng.NextIntN(int32(max-min+1)))
}

// stateID resolves a default block state id by registry name (band table helpers).
func stateID(name string) (block.StateID, error) {
	b, ok := block.FromID[name]
	if !ok {
		return 0, fmt.Errorf("surface: unknown band block %q", name)
	}
	sid, ok := block.ToStateID[b]
	if !ok {
		return 0, fmt.Errorf("surface: no state id for band block %q", name)
	}
	return sid, nil
}

// ---- Context: the per-column SurfaceRules$Context ----

// BiomeGetter resolves the biome holder at a block position — the multi-noise source's
// GetBiome (the surface rules' biome/temperature conditions key on it).
type BiomeGetter func(x, y, z int) biome.Type

// Context ports net.minecraft.world.level.levelgen.SurfaceRules$Context: the mutable
// per-column / per-block state the conditions read. updateXZ resets the column (block
// x/z + surfaceDepth); updateY sets the per-block depth/water/biome inputs.
type Context struct {
	system *SurfaceSystem

	chunk   *level.Chunk
	nc      *noisechunk.NoiseChunk
	biomeOf BiomeGetter
	minY    int
	// height is the dimension gen depth (nc.Height()) — needed by VerticalAnchor.BelowTop.resolveY
	// (getGenDepth-1+minGenY-offset), which the nether's bedrock-roof vertical_gradient uses.
	height int

	// per-XZ state
	blockX, blockZ int
	surfaceDepth   int

	surfaceSecondaryValid bool
	surfaceSecondary      float64

	minSurfaceValid bool
	minSurfaceLevel int

	// per-Y state
	biomeValid      bool
	biome           biome.Type
	blockY          int
	waterHeight     int
	stoneDepthBelow int
	stoneDepthAbove int
}

// updateXZ ports SurfaceRules$Context.updateXZ(x,z): set the column coords and the
// per-column surfaceDepth; invalidate the per-column caches (secondary, minSurface).
func (c *Context) updateXZ(x, z int) {
	c.blockX = x
	c.blockZ = z
	c.surfaceDepth = c.system.getSurfaceDepth(x, z)
	c.surfaceSecondaryValid = false
	c.minSurfaceValid = false
}

// updateY ports SurfaceRules$Context.updateY(stoneDepthAbove, stoneDepthBelow,
// waterHeight, blockY): set the per-block inputs and invalidate the biome cache.
func (c *Context) updateY(stoneDepthAbove, stoneDepthBelow, waterHeight, blockY int) {
	c.biomeValid = false
	c.blockY = blockY
	c.waterHeight = waterHeight
	c.stoneDepthBelow = stoneDepthBelow
	c.stoneDepthAbove = stoneDepthAbove
}

// getSurfaceSecondary ports Context.getSurfaceSecondary(): the per-column secondary
// surface noise, memoized per updateXZ.
func (c *Context) getSurfaceSecondary() float64 {
	if !c.surfaceSecondaryValid {
		c.surfaceSecondary = c.system.getSurfaceSecondary(c.blockX, c.blockZ)
		c.surfaceSecondaryValid = true
	}
	return c.surfaceSecondary
}

// getBiome ports Context.getBiome(): the biome at the current (x, blockY, z), memoized
// per updateY (the biome can change with Y at biome borders).
func (c *Context) getBiome() biome.Type {
	if !c.biomeValid {
		c.biome = c.biomeOf(c.blockX, c.blockY, c.blockZ)
		c.biomeValid = true
	}
	return c.biome
}

// getMinSurfaceLevel ports Context.getMinSurfaceLevel(): the bilinear-lerped preliminary
// surface level over the 4-block surface cell, + surfaceDepth - 8. Memoized per updateXZ.
//
//	cellX = x>>4 (SURFACE_CELL_BITS=4); the 4 corners are
//	  preliminarySurfaceLevel(cellToBlock(cellX{,+1}), cellToBlock(cellZ{,+1}))
//	floor(lerp2(frac(x), frac(z), c00, c10, c01, c11)) + surfaceDepth - 8
func (c *Context) getMinSurfaceLevel() int {
	if c.minSurfaceValid {
		return c.minSurfaceLevel
	}
	cx := c.blockX >> 4
	cz := c.blockZ >> 4
	x0 := cx << 4
	x1 := (cx + 1) << 4
	z0 := cz << 4
	z1 := (cz + 1) << 4
	c00 := float64(c.nc.PreliminarySurfaceLevel(x0, z0))
	c10 := float64(c.nc.PreliminarySurfaceLevel(x1, z0))
	c01 := float64(c.nc.PreliminarySurfaceLevel(x0, z1))
	c11 := float64(c.nc.PreliminarySurfaceLevel(x1, z1))
	dx := float64(c.blockX&15) / 16.0
	dz := float64(c.blockZ&15) / 16.0
	lvl := mthFloor(mthLerp2(dx, dz, c00, c10, c01, c11))
	c.minSurfaceLevel = lvl + c.surfaceDepth - 8
	c.minSurfaceValid = true
	return c.minSurfaceLevel
}

// surfaceNoiseValue returns the named 2-D surface noise at the current column.
func (c *Context) surfaceNoiseValue(noiseID string) float64 {
	v, err := c.system.surfaceNoiseValue(noiseID, c.blockX, c.blockZ)
	if err != nil {
		// A missing surface noise is a build-data error; surfacing it via a panic
		// here (off the build-trusted DATA path) keeps the loud-error discipline
		// without threading an error through every condition.test.
		panic(fmt.Sprintf("surface: noise %q: %v", noiseID, err))
	}
	return v
}

// worldSurfaceHeight reads the WORLD_SURFACE_WG heightmap for local (lx,lz) — the steep
// condition's slope input. The heightmap stores Y relative to minY.
func (c *Context) worldSurfaceHeight(lx, lz int) int {
	return c.chunk.HeightMaps.WorldSurfaceWG.Get(lz<<4|lx) + c.minY
}

// SeaLevel exposes the system sea level (the temperature condition uses it).
func (c *Context) SeaLevel() int { return c.system.seaLevel }

// SeaLevel exposes the surface system's sea level. The freeze_top_layer feature body
// (SnowAndFreezeFeature, via Biome.coldEnoughToSnow(pos, seaLevel)) needs it during
// decoration, so the generator reads it off the shared SurfaceSystem it already holds
// (WorldGenLevel.getSeaLevel() == the DimensionType/generator sea level).
func (s *SurfaceSystem) SeaLevel() int { return s.seaLevel }

// ---- chunkColumn: a *level.Chunk column adapter ----

// chunkColumn adapts one (lx,lz) column of a *level.Chunk to BlockColumn for buildSurface.
type chunkColumn struct {
	chunk  *level.Chunk
	lx, lz int
	minY   int
	air    block.StateID
	water  block.StateID
}

func (cc *chunkColumn) idx(y int) (sec, local int, ok bool) {
	sec = (y - cc.minY) >> 4
	if sec < 0 || sec >= len(cc.chunk.Sections) {
		return 0, 0, false
	}
	local = (y&15)<<8 | (cc.lz&15)<<4 | (cc.lx & 15)
	return sec, local, true
}

func (cc *chunkColumn) getBlock(y int) block.StateID {
	sec, local, ok := cc.idx(y)
	if !ok {
		return cc.air
	}
	return cc.chunk.Sections[sec].GetBlock(local)
}

func (cc *chunkColumn) setBlock(y int, st block.StateID) {
	sec, local, ok := cc.idx(y)
	if !ok {
		return
	}
	cc.chunk.Sections[sec].SetBlock(local, st)
}

// ---- buildSurface ----

// BuildSurface ports SurfaceSystem.buildSurface for one chunk: it walks each of the 256
// columns top-down from the WORLD_SURFACE_WG heightmap, maintains the SurfaceRules$Context
// (stoneDepthAbove/Below, water height, biome, surfaceDepth), applies the parsed rule
// sequence to each default-block (stone) cell, writes the resulting surface block, and
// finally rewrites the 3 CLIENT heightmaps from the new top solid (Pitfall 6).
//
// It MUTATES the passed *level.Chunk in place. nc supplies the preliminary surface level
// (above_preliminary_surface / getMinSurfaceLevel); biomeOf is the Wave-7 multi-noise
// GetBiome; rule is the parsed surface_rule tree.
func BuildSurface(s *SurfaceSystem, rule RuleSource, ch *level.Chunk, nc *noisechunk.NoiseChunk, biomeOf BiomeGetter) {
	pos := nc.Pos()
	minBlockX := int(pos[0]) * 16
	minBlockZ := int(pos[1]) * 16
	minY := nc.MinY()
	maxY := minY + nc.Height()

	air := block.ToStateID[block.Air{}]
	caveAir := block.ToStateID[block.CaveAir{}]
	water := block.ToStateID[block.Water{Level: 0}]

	ctx := &Context{
		system:  s,
		chunk:   ch,
		nc:      nc,
		biomeOf: biomeOf,
		minY:    minY,
		height:  nc.Height(),
	}

	// Make sure WORLD_SURFACE_WG reflects the filled+carved terrain before the walk
	// (buildSurface reads it as the column's top). Recompute from the current blocks.
	recomputeWorldSurfaceWG(ch, minY, maxY, air, caveAir)

	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			worldX := minBlockX + lx
			worldZ := minBlockZ + lz
			col := &chunkColumn{chunk: ch, lx: lx, lz: lz, minY: minY, air: air, water: water}

			// Start one above the highest non-air block (WORLD_SURFACE_WG height).
			top := ch.HeightMaps.WorldSurfaceWG.Get(lz<<4|lx) + minY

			ctx.updateXZ(worldX, worldZ)

			stoneDepthAbove := 0
			waterHeight := minInt32 // var25 — first fluid Y+1 above
			minStoneY := 2147483647 // var26 — bottom of the current solid run

			for y := top; y >= minY; y-- {
				st := col.getBlock(y)
				if isAirState(st, air, caveAir) {
					stoneDepthAbove = 0
					waterHeight = minInt32
					continue
				}
				if isFluidState(st, water) {
					if waterHeight == minInt32 {
						waterHeight = y + 1
					}
					continue
				}
				// solid: (re)compute the bottom of this stone run for stoneDepthBelow.
				if minStoneY >= y {
					minStoneY = wayBelowMinY
					for yy := y - 1; yy >= minY-1; yy-- {
						bs := air
						if yy >= minY {
							bs = col.getBlock(yy)
						}
						if !isStone(bs, air, caveAir, water) {
							minStoneY = yy + 1
							break
						}
					}
				}
				stoneDepthAbove++
				stoneDepthBelow := y - minStoneY + 1
				ctx.updateY(stoneDepthAbove, stoneDepthBelow, waterHeight, y)

				// Only rewrite cells that are the default block (stone) — the rule
				// places the surface cap on top of raw stone, leaving ore/water/etc.
				if st != s.defaultBlock {
					continue
				}
				if newSt, ok := rule.tryApply(ctx); ok {
					col.setBlock(y, newSt)
				}
			}
		}
	}

	// Rewrite the 3 CLIENT heightmaps from the post-surface top solid (Pitfall 6).
	writeClientHeightmaps(ch, minY, maxY, air, caveAir, water)
}

// wayBelowMinY mirrors DimensionType.WAY_BELOW_MIN_Y (the stoneDepthBelow seed before
// the downward stone scan finds the run bottom).
const wayBelowMinY = -2032

// isAirState reports whether a state is air or cave air.
func isAirState(st, air, caveAir block.StateID) bool { return st == air || st == caveAir }

// isFluidState reports whether a state is a fluid (water — the only surface-relevant
// fluid the fill/aquifer produces above min). Lava is below the surface band and never
// reaches the top-down water bookkeeping.
func isFluidState(st, water block.StateID) bool { return st == water }

// isStone ports SurfaceSystem.isStone (bytecode 0-22): return true iff the state is
// NOT air AND its fluid state is empty -- i.e. !isAir(st) && !isFluid(st). It is the
// stoneDepthBelow scan's run predicate: any non-air, non-fluid block continues the
// stone run (ore, deepslate, packed_mud, etc. all count), only air/fluid terminate it.
func isStone(st, air, caveAir, water block.StateID) bool {
	return !isAirState(st, air, caveAir) && !isFluidState(st, water)
}

// stoneState / deepslateState resolve the two rock states the noise fill stratifies the
// column with (stone above y=0, deepslate below). Cached on first use.
var stoneStateCache, deepslateStateCache block.StateID
var stateCacheInit bool

func ensureStateCache() {
	if stateCacheInit {
		return
	}
	stoneStateCache = block.ToStateID[block.Stone{}]
	deepslateStateCache = block.ToStateID[block.Deepslate{Axis: block.Y}]
	stateCacheInit = true
}
func stoneState() block.StateID     { ensureStateCache(); return stoneStateCache }
func deepslateState() block.StateID { ensureStateCache(); return deepslateStateCache }

// recomputeWorldSurfaceWG rewrites the WORLD_SURFACE_WG heightmap (first Y above the
// highest non-air block) so buildSurface's top-down walk + the steep condition read a
// truthful column top. Y is stored relative to minY.
func recomputeWorldSurfaceWG(ch *level.Chunk, minY, maxY int, air, caveAir block.StateID) {
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			top := minY
			for y := maxY - 1; y >= minY; y-- {
				sec := (y - minY) >> 4
				if sec < 0 || sec >= len(ch.Sections) {
					continue
				}
				local := (y&15)<<8 | (lz&15)<<4 | (lx & 15)
				st := ch.Sections[sec].GetBlock(local)
				if !isAirState(st, air, caveAir) {
					top = y + 1
					break
				}
			}
			v := top - minY
			if v < 0 {
				v = 0
			}
			ch.HeightMaps.WorldSurfaceWG.Set(lz<<4|lx, v)
		}
	}
}

// BuildWorldgenHeightmaps populates the 3 WORLDGEN heightmaps (WORLD_SURFACE_WG,
// OCEAN_FLOOR_WG, MOTION_BLOCKING) from the chunk's FINAL (post-carve) terrain, each
// with its own jar-confirmed Heightmap$Types predicate:
//
//   - WORLD_SURFACE_WG = NOT_AIR                    (first Y above the highest non-air block)
//   - OCEAN_FLOOR_WG   = motion-blocking AND NOT fluid (first Y above the highest solid below water)
//   - MOTION_BLOCKING  = blocks-motion OR fluid     (first Y above the highest non-air-or-water)
//
// This is the pre-decoration worldgen-heightmap build GEN2-03 requires: it MUST run after
// carving (so it reflects carved openings) and before the first feature would read the
// worldgen heightmaps. The generator wires it into its FINISH step (after ApplyCarvers).
// It resolves the air/caveAir/water StateIDs itself so callers need only pass the chunk
// + its Y bounds. The 3 CLIENT heightmaps remain the job of writeClientHeightmaps.
//
// Pure: a deterministic top-down scan of the chunk's blocks, so it does not affect the
// noise generator's pureness over (seed, pos).
func BuildWorldgenHeightmaps(ch *level.Chunk, minY, maxY int) {
	air := block.ToStateID[block.Air{}]
	caveAir := block.ToStateID[block.CaveAir{}]
	water := block.ToStateID[block.Water{Level: 0}]
	recomputeWorldSurfaceWG(ch, minY, maxY, air, caveAir)
	recomputeOceanFloorWG(ch, minY, maxY, air, caveAir, water)
	recomputeMotionBlockingWG(ch, minY, maxY, air, caveAir, water)
}

// recomputeOceanFloorWG rewrites OCEAN_FLOOR_WG (first Y above the highest
// motion-blocking-NO-FLUID block — i.e. the highest solid that is not water), mirroring
// recomputeWorldSurfaceWG with the ocean-floor predicate. Y is stored relative to minY.
func recomputeOceanFloorWG(ch *level.Chunk, minY, maxY int, air, caveAir, water block.StateID) {
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			top := minY
			for y := maxY - 1; y >= minY; y-- {
				sec := (y - minY) >> 4
				if sec < 0 || sec >= len(ch.Sections) {
					continue
				}
				local := (y&15)<<8 | (lz&15)<<4 | (lx & 15)
				st := ch.Sections[sec].GetBlock(local)
				// motion-blocking AND NOT fluid: non-air and not water.
				if !isAirState(st, air, caveAir) && !isFluidState(st, water) {
					top = y + 1
					break
				}
			}
			v := top - minY
			if v < 0 {
				v = 0
			}
			ch.HeightMaps.OceanFloorWG.Set(lz<<4|lx, v)
		}
	}
}

// recomputeMotionBlockingWG rewrites the WORLDGEN MOTION_BLOCKING heightmap (first Y above
// the highest blocks-motion-OR-fluid block — non-air OR water, since for the noise terrain
// water blocks motion), mirroring recomputeWorldSurfaceWG with the motion-blocking
// predicate. Y is stored relative to minY. NOTE: this is the SERVER-ONLY worldgen
// MOTION_BLOCKING; the CLIENT MOTION_BLOCKING (on the wire) is still finalized by
// writeClientHeightmaps.
func recomputeMotionBlockingWG(ch *level.Chunk, minY, maxY int, air, caveAir, water block.StateID) {
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			top := minY
			for y := maxY - 1; y >= minY; y-- {
				sec := (y - minY) >> 4
				if sec < 0 || sec >= len(ch.Sections) {
					continue
				}
				local := (y&15)<<8 | (lz&15)<<4 | (lx & 15)
				st := ch.Sections[sec].GetBlock(local)
				// blocks-motion OR fluid: non-air OR water → any non-air (water is non-air).
				if !isAirState(st, air, caveAir) || isFluidState(st, water) {
					top = y + 1
					break
				}
			}
			v := top - minY
			if v < 0 {
				v = 0
			}
			ch.HeightMaps.MotionBlocking.Set(lz<<4|lx, v)
		}
	}
}

// writeClientHeightmaps writes the 3 CLIENT heightmaps (WorldSurface, MotionBlocking,
// MotionBlockingNoLeaves) from the post-surface column top. WorldSurface = first Y above
// the highest non-air block; MotionBlocking(/NoLeaves) = first Y above the highest
// motion-blocking block (non-air OR fluid) — for the noise terrain those coincide with
// the highest non-air (water blocks motion), so all three share the top-of-column scan.
// Pitfall 6: a wrong heightmap mis-renders/mis-spawns.
func writeClientHeightmaps(ch *level.Chunk, minY, maxY int, air, caveAir, water block.StateID) {
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			worldSurface := minY
			motionBlocking := minY
			for y := maxY - 1; y >= minY; y-- {
				sec := (y - minY) >> 4
				if sec < 0 || sec >= len(ch.Sections) {
					continue
				}
				local := (y&15)<<8 | (lz&15)<<4 | (lx & 15)
				st := ch.Sections[sec].GetBlock(local)
				if isAirState(st, air, caveAir) {
					continue
				}
				// First non-air from the top.
				if motionBlocking == minY {
					motionBlocking = y + 1
				}
				if st != water {
					worldSurface = y + 1
					break
				}
			}
			ws := worldSurface - minY
			if ws < 0 {
				ws = 0
			}
			mb := motionBlocking - minY
			if mb < 0 {
				mb = 0
			}
			ch.HeightMaps.WorldSurface.Set(lz<<4|lx, ws)
			ch.HeightMaps.MotionBlocking.Set(lz<<4|lx, mb)
			ch.HeightMaps.MotionBlockingNoLeaves.Set(lz<<4|lx, mb)
		}
	}
}

// FillBiomes fills the chunk's per-section 4×4×4 biome paletted containers with the real
// (varied) biome ids from the Wave-7 multi-noise source (the client renders grass color/
// fog/etc. from these). Each section holds 64 biome cells (a 4×4×4 grid at quart
// resolution); cell (bx,by,bz) maps to the block at the cell's corner.
func FillBiomes(ch *level.Chunk, nc *noisechunk.NoiseChunk, biomeOf BiomeGetter) {
	pos := nc.Pos()
	minBlockX := int(pos[0]) * 16
	minBlockZ := int(pos[1]) * 16
	minY := nc.MinY()
	for si := range ch.Sections {
		sec := &ch.Sections[si]
		secMinY := minY + si*16
		bc := level.NewBiomesPaletteContainer(4*4*4, 0)
		for by := 0; by < 4; by++ {
			for bz := 0; bz < 4; bz++ {
				for bx := 0; bx < 4; bx++ {
					wx := minBlockX + bx*4
					wy := secMinY + by*4
					wz := minBlockZ + bz*4
					bt := biomeOf(wx, wy, wz)
					bc.Set((by*4+bz)*4+bx, bt)
				}
			}
		}
		sec.Biomes = bc
	}
}
