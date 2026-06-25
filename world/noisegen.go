package world

import (
	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen/biome"
	"github.com/imhinotori/sulfur/world/levelgen/carver"
	"github.com/imhinotori/sulfur/world/levelgen/noisechunk"
	"github.com/imhinotori/sulfur/world/levelgen/router"
	"github.com/imhinotori/sulfur/world/levelgen/surface"
)

// NoiseGenerator is the full-parity world.Generator: it drives the whole ported
// Minecraft 26.2 (protocol 776) overworld pipeline — the bound NoiseRouter (Wave 3),
// the NoiseChunk cell-sampler (Wave 4), the Aquifer + OreVeinifier fill (Wave 5), the
// WorldCarver ravines + tunnel caves (Wave 6), and the multi-noise biome source +
// SurfaceSystem (Wave 7) — into a complete, valid, recognizable vanilla overworld
// chunk (hills, noise caves, carved tunnels + ravines, aquifers, ore veins, and
// biome-varied surfaces).
//
// It is a DROP-IN replacement for Superflat behind the UNCHANGED world.Generator
// interface: the off-tick world.Worker runs Generate(pos) exactly as it ran Superflat
// (zero concurrency/seam work — PARITY-01 is purely "a better Generate"). The
// world/worker.go reader, the tick, and the capture-diff-sealed level.Chunk wire
// (Phase 4) are all UNTOUCHED — the chunk format is sealed, the generator only fills it.
//
// PIPELINE ORDER (block sense): fill -> carve -> surface (heightmaps last). Vanilla's
// chunk-status pipeline (javap -p net.minecraft.world.level.chunk.status.ChunkStatusTasks,
// 26.2-inner.jar) runs generateBiomes -> generateNoise -> generateSurface ->
// generateCarvers -> generateFeatures, i.e. SURFACE precedes CARVERS as STATUS steps.
// But for a single-shot generator that must emit the FINAL client heightmaps in one
// pass, the block-level dependency is: the carve must cut the stone BEFORE the surface
// is applied (so a carved ravine gets a correct grass/dirt/gravel surface on its NEW
// top, not the pre-carve top), and the 3 CLIENT heightmaps must be written from the
// FINAL blocks (BuildSurface's last step) so a cave opening at the surface is reflected.
// Running fill -> carve -> surface achieves exactly that: surfaces land on carved tops
// and the heightmaps are truthful (Pitfall 6). This is the same fill->carve->surface
// ordering 09-RESEARCH specifies; the only divergence from vanilla's STATUS order is a
// surface-on-carved-top refinement, which is the correct behavior here.
//
// PURE (WORLD-04 / Pitfall 7): NewNoiseGenerator builds the router + biome source +
// surface system + carver configs ONCE; Generate(pos) reads only the seed + the bound
// graph + the clamped pos. No math/rand, no global mutable state, no map-iteration-order
// in the pipeline — same seed+pos -> identical *level.Chunk bytes.
type NoiseGenerator struct {
	seed int64
	secs int
	minY int

	router  *router.Router
	biomes  *biome.MultiNoiseBiomeSource
	surface *surface.SurfaceSystem
	rule    surface.RuleSource
	carvers []*carver.ConfiguredCarver
	rep     *carver.Replaceables

	// air state id, reused by the carve adapter's out-of-range Get (resolved once).
	air block.StateID
}

// NewNoiseGenerator builds the per-world generator: it parses + seeds the router from the
// world seed (NewRouter), builds the multi-noise biome source, the surface system + the
// parsed surface_rule, and the overworld carver list + the replaceables tag — resolving
// everything ONCE so Generate stays pure and allocation-light. A build-data error
// (missing/unparseable embedded DATA) is a programming/asset bug, not runtime input, so
// it panics exactly like NewSuperflat (the worker treats Generate as infallible).
//
// secs/minY are passed by main.go exactly as Superflat (overworld secs=24, minY=-64);
// the sea level + noise dimensions live in the router settings (sea level 63), which the
// NoiseChunk/Aquifer/SurfaceSystem read directly — the generator only carries secs/minY
// for the chunk geometry + the carve adapter bounds.
func NewNoiseGenerator(seed int64, secs, minY int) *NoiseGenerator {
	r, err := router.NewRouter(seed)
	if err != nil {
		panic("world: NoiseGenerator: build router: " + err.Error())
	}
	bs, err := biome.NewMultiNoiseBiomeSource(r)
	if err != nil {
		panic("world: NoiseGenerator: build biome source: " + err.Error())
	}
	ss, err := surface.NewSurfaceSystem(r)
	if err != nil {
		panic("world: NoiseGenerator: build surface system: " + err.Error())
	}
	rule, err := surface.ParseRuleSource(r.Settings.SurfaceRule)
	if err != nil {
		panic("world: NoiseGenerator: parse surface_rule: " + err.Error())
	}
	carvers, err := carver.LoadOverworldCarvers()
	if err != nil {
		panic("world: NoiseGenerator: load carvers: " + err.Error())
	}
	rep, err := carver.ParseReplaceables()
	if err != nil {
		panic("world: NoiseGenerator: parse carver replaceables: " + err.Error())
	}

	return &NoiseGenerator{
		seed:    seed,
		secs:    secs,
		minY:    minY,
		router:  r,
		biomes:  bs,
		surface: ss,
		rule:    rule,
		carvers: carvers,
		rep:     rep,
		air:     block.ToStateID[block.Air{}],
	}
}

// Generate drives the full pipeline into a fresh capture-diff-sealed level.Chunk and
// returns it. PURE over (seed, pos).
//
// Order mirrors vanilla's ChunkStatus pipeline NOISE -> SURFACE -> CARVERS:
//
//  1. fill   : NoiseChunk cell-sample final_density -> Aquifer/OreVeinifier doFill
//     (FillChunk) places stone/deepslate/water/lava/air/ore into the sections.
//  2. surface: BuildSurface applies the biome-correct surface rules to each column's
//     stone top (grass/dirt/sand/gravel/...) on the UN-CARVED terrain and rewrites
//     the 3 CLIENT heightmaps; FillBiomes fills the per-section 4x4x4 biome
//     containers from the multi-noise source (varied, not single-plains).
//  3. carve  : ApplyCarvers runs the ravines + extra tunnel caves over the surfaced
//     terrain, aquifer-aware (a carve below the water table floods); carved openings
//     expose bare stone, matching vanilla.
//  4. finish: per-section sky light (mirroring Superflat's renderable finishing); the
//     FluidCount + biome container were set by the fill/biome steps. Status full.
func (g *NoiseGenerator) Generate(pos level.ChunkPos) *level.Chunk {
	// (1) FILL — cell-sample + aquifer/ore doFill into a renderable chunk.
	nc := noisechunk.NewNoiseChunk(g.router, pos)
	aq := noisechunk.NewAquifer(g.router, nc, pos)
	ov := noisechunk.NewOreVeinifier(
		g.router.NoiseRouter.VeinToggle,
		g.router.NoiseRouter.VeinRidged,
		g.router.NoiseRouter.VeinGap,
		g.router.Random,
	)
	ch := noisechunk.FillChunk(nc, aq, ov)

	// (2) SURFACE — biome-correct surface on the UN-CARVED terrain top + the 3 CLIENT
	// heightmaps, then the varied per-section biome containers. This mirrors vanilla's
	// ChunkStatus order NOISE -> SURFACE -> CARVERS: the surface rules cap the solid
	// terrain BEFORE carvers cut through it, so caves/ravines later expose BARE STONE
	// (carvers only convert the single block under a carved grass column to dirt) instead
	// of leaving grass/dirt/sand rims on every cave mouth.
	//
	// The biome is defined per QUART cell (4×4×4: GetBiome converts block→quart via >>2), but
	// BuildSurface samples it per-block along each column's surface AND FillBiomes samples it
	// again per quart cell — both repeatedly hit the SAME quart cells, and each sample runs the
	// six climate density functions + an RTree search (the post-RTree hotspot is exactly this
	// re-sampling, ~60% cum in the profile). A per-quart-cell cache over ONE chunk's generation
	// is behavior-identical (same quart key → same biome by construction) and collapses the
	// duplicate samples to one per distinct cell. Scoped to this Generate call (no shared state),
	// so Generate stays pure over (seed, pos).
	bc := newBiomeCache(g.biomes)
	biomeOf := bc.get
	surface.BuildSurface(g.surface, g.rule, ch, nc, biomeOf)
	surface.FillBiomes(ch, nc, biomeOf)

	// (3) CARVE — ravines + tunnel caves cut through the surfaced terrain, aquifer-aware
	// via the Wave-5 aquifer's CarveFluid seam (computeSubstance at density 0). Running
	// AFTER surface means carved openings reveal raw stone, matching vanilla.
	cc := &carveChunk{chunk: ch, pos: pos, minY: g.minY, height: g.secs * 16, air: g.air}
	carver.ApplyCarvers(g.seed, cc, aq, g.carvers, g.rep)

	// (4) FINISH — the pre-decoration WORLDGEN heightmaps + sky light.
	//
	// GEN2-03: build all 3 worldgen heightmaps (WORLD_SURFACE_WG / OCEAN_FLOOR_WG /
	// MOTION_BLOCKING) from the FINAL post-carve terrain, here in the finish step (after
	// ApplyCarvers) so they reflect carved openings and are live + correct before the
	// first feature would read them (Phase 11+ decoration does heightmap-relative
	// placement). This is the bulk pre-decoration build; the incremental
	// level.HeightmapUpdate keeps them live on each subsequent worldgen block write. The
	// 3 CLIENT heightmaps stay finalized by BuildSurface's writeClientHeightmaps (the wire
	// authority) and are not touched here. Pure top-down scan → Generate stays
	// deterministic over (seed, pos).
	surface.BuildWorldgenHeightmaps(ch, g.minY, g.minY+g.secs*16)

	// Sky light for rendering (mirrors Superflat / FillChunk finishing). FillChunk already
	// set FluidCount/biome defaults and the CLIENT heightmaps were rewritten by
	// BuildSurface; sky light is re-applied here defensively so every present section is
	// lit regardless of the fill path's finishing.
	for i := range ch.Sections {
		s := &ch.Sections[i]
		if len(s.SkyLight) != 2048 {
			s.SkyLight = fullSkyLight()
		}
	}
	ch.Status = level.StatusFull
	return ch
}

// SpawnSurfaceY derives the world-Y of the highest solid/fluid surface block for the
// spawn column (the block-center of chunk (cx,cz) at local x=8, z=8) from a freshly
// generated chunk's WorldSurface client heightmap. main.go feeds the returned value to
// tick.SetSpawn / NewGameTick exactly as it fed Superflat's fixed SurfaceY, so the join
// bootstrap (sendPlayBootstrap) places the player two blocks ABOVE this — standing on the
// generated terrain instead of inside a hill or in the void over an ocean (T-9-26).
//
// The WorldSurface heightmap stores, per column, the Y of the first block ABOVE the
// highest non-air block, encoded relative to MinY. So the top solid/fluid block's world-Y
// is (WorldSurface.Get(col) + MinY) - 1 — the same "top solid block world-Y" semantics
// Superflat's SurfaceY carried. Pure over (seed, pos): it reuses Generate.
func (g *NoiseGenerator) SpawnSurfaceY(pos level.ChunkPos) int {
	ch := g.Generate(pos)
	// Block-center column of the chunk: local x=8, z=8 (the (8.5, _, 8.5) spawn point
	// sendPlayBootstrap uses). Column index is (z&15)<<4 | (x&15) — matching the heightmap
	// column order written by BuildSurface.
	const spawnLocalX, spawnLocalZ = 8, 8
	col := (spawnLocalZ << 4) | spawnLocalX
	// first-air-above-top relative to MinY -> top solid/fluid block world-Y.
	topAir := ch.HeightMaps.WorldSurface.Get(col) + g.minY
	return topAir - 1
}

// biomeCache memoizes the multi-noise biome source per QUART cell for the lifetime of a single
// Generate call. The multi-noise biome is constant across a 4×4×4 block quart cell (GetBiome maps
// block→quart via >>2 before any sampling), so caching on the quart key (qx,qy,qz) returns the
// EXACT same biome the uncached source would — it removes only redundant work, not any variation.
// This is the per-chunk biome cache the perf fix layers on top of the RTree: BuildSurface (per
// surface block) and FillBiomes (per quart cell) both sample overlapping cells, and each sample
// is six climate density-function evaluations + an RTree search; the cache collapses those to one
// evaluation per distinct quart cell.
//
// NOT shared across chunks (a fresh cache per Generate) so Generate stays pure over (seed, pos)
// and the map needs no synchronization — it is touched only by the single goroutine running this
// Generate.
type biomeCache struct {
	src   *biome.MultiNoiseBiomeSource
	cache map[[3]int]levelbiome.Type
}

// newBiomeCache builds an empty per-chunk cache over the biome source.
func newBiomeCache(src *biome.MultiNoiseBiomeSource) *biomeCache {
	return &biomeCache{src: src, cache: make(map[[3]int]levelbiome.Type, 256)}
}

// get returns the biome at a BLOCK position, memoized by its quart cell. The key is the quart
// triple (x>>2, y>>2, z>>2) — the same conversion GetBiome does internally — so a hit returns the
// identical value GetBiome would compute for any block in that cell.
func (b *biomeCache) get(x, y, z int) levelbiome.Type {
	key := [3]int{x >> 2, y >> 2, z >> 2}
	if v, ok := b.cache[key]; ok {
		return v
	}
	v := b.src.GetBiome(x, y, z)
	b.cache[key] = v
	return v
}

// carveChunk adapts a *level.Chunk to carver.CarveChunk: world-coord Get/Set bounded to
// the target chunk's 16x16 footprint (Set drops out-of-footprint writes so a carve
// started in a neighbor source chunk only edits THIS chunk's blocks). MinY/Height bound
// the vertical carve range. Reads/writes are y-major-local, matching the section layout.
type carveChunk struct {
	chunk  *level.Chunk
	pos    level.ChunkPos
	minY   int
	height int
	air    block.StateID
}

func (c *carveChunk) Pos() level.ChunkPos { return c.pos }
func (c *carveChunk) MinY() int           { return c.minY }
func (c *carveChunk) Height() int         { return c.height }

// localIndex maps a world (wx,wy,wz) to its section + in-section local index, returning
// ok=false for an out-of-range Y. The x/z are taken modulo the chunk footprint (the
// carver passes world coords; only in-footprint writes are kept by Set).
func (c *carveChunk) localIndex(wx, wy, wz int) (sec, local int, ok bool) {
	sec = (wy - c.minY) >> 4
	if sec < 0 || sec >= len(c.chunk.Sections) {
		return 0, 0, false
	}
	local = (wy&15)<<8 | (wz&15)<<4 | (wx & 15)
	return sec, local, true
}

func (c *carveChunk) Get(wx, wy, wz int) block.StateID {
	sec, local, ok := c.localIndex(wx, wy, wz)
	if !ok {
		return c.air
	}
	return c.chunk.Sections[sec].GetBlock(local)
}

// inFootprint reports whether (wx,wz) falls inside the target chunk's 16x16 column.
func (c *carveChunk) inFootprint(wx, wz int) bool {
	baseX := int(c.pos[0]) * 16
	baseZ := int(c.pos[1]) * 16
	return wx >= baseX && wx < baseX+16 && wz >= baseZ && wz < baseZ+16
}

func (c *carveChunk) Set(wx, wy, wz int, state block.StateID) {
	if !c.inFootprint(wx, wz) {
		return // only the target chunk is edited (cross-chunk carves don't leak)
	}
	sec, local, ok := c.localIndex(wx, wy, wz)
	if !ok {
		return
	}
	c.chunk.Sections[sec].SetBlock(local, state)
}

// compile-time assertions: the generator is a drop-in world.Generator, and the adapter
// satisfies the carver's CarveChunk contract.
var (
	_ Generator          = (*NoiseGenerator)(nil)
	_ carver.CarveChunk  = (*carveChunk)(nil)
	_ carver.FluidSource = (*noisechunk.Aquifer)(nil)
)
