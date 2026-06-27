package world

import (
	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/biome"
	"github.com/imhinotori/sulfur/world/levelgen/carver"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/noisechunk"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
	"github.com/imhinotori/sulfur/world/levelgen/router"
	"github.com/imhinotori/sulfur/world/levelgen/surface"
	"github.com/imhinotori/sulfur/world/structure"
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

	// deco is the per-world feature graph (parsed registry + FeatureSorter + per-biome
	// feature lists + the biome allowance), built ONCE here (memoized). applyBiomeDecoration
	// reads it; it is immutable at decoration time so Decorate stays pure over (seed, pos).
	deco *decorationData

	// STRUCT-01: the two-phase structure pipeline. structCache is the per-world
	// StructureStart cache (a pure, singleflight-deduped memoization keyed by packed
	// chunk pos — the ONLY cross-goroutine structure state). structGen is the
	// StartGenerator (an INERT no-op set this plan — 14-02 registers the desert pyramid),
	// fed the router-backed SurfaceSampler (heightmap-at-STARTS) + the real GetBiome seam
	// so 14-02/14-03 gate temples on a real biome test (no accept-by-default). Built ONCE
	// here so Decorate stays pure over (seed, pos).
	structCache *structure.Cache
	structGen   structure.StartGenerator

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
	// FEAT-02: parse the full feature roster + build the FeatureSorter (cross-biome
	// per-step global index ordering) ONCE, from the embedded biome `features` arrays.
	// A build-data mismatch (unknown feature type, unresolvable block-state ref, a missing
	// biome, or a cross-biome ordering cycle) is an asset bug, not runtime input, so it
	// panics here exactly like the router/biome-source/carver construction above — the
	// worker treats Decorate as infallible.
	deco, err := buildDecorationData()
	if err != nil {
		panic("world: NoiseGenerator: build feature/decoration data: " + err.Error())
	}

	// STRUCT-01: build the structure cache over a router-backed surface sampler (the
	// heightmap-at-STARTS column query — one PreliminarySurfaceLevel compute per quart
	// cell, NO chunk fill) and the REAL biome lookup (g.biomes.GetBiome, the load-bearing
	// half of "vanilla positions"). The StartGenerator is INERT this plan (NoopStartGenerator
	// owns zero structures), so STARTS/REFERENCES run but produce zero starts and the
	// placeStructures hook writes NOTHING — the chunk bytes stay identical to Phase 13.
	// 14-02 swaps in the desert-pyramid set.
	sampler := structure.NewRouterSurfaceSampler(r)
	biomeAt := func(wx, wy, wz int) levelbiome.Type { return bs.GetBiome(wx, wy, wz) }
	structCache := structure.NewCache(sampler, biomeAt)

	// STRUCT-02 + STRUCT-02 (14-03): register the four scattered-temple StartGenerators into a
	// CompositeStartGenerator (desert pyramid + jungle temple + igloo + swamp hut). Each loads
	// its embedded structure_set (cross-checked salt) + has_structure biome tag; a build-data
	// error is an asset bug (panic, like the loads above). The composite dispatches a chunk's
	// start decision to all four (each runs its own biome gate, no accept-by-default).
	desertGen, err := structure.NewDesertPyramidStartGen()
	if err != nil {
		panic("world: NoiseGenerator: build desert pyramid start generator: " + err.Error())
	}
	jungleGen, err := structure.NewJungleTempleStartGen()
	if err != nil {
		panic("world: NoiseGenerator: build jungle temple start generator: " + err.Error())
	}
	iglooGen, err := structure.NewIglooStartGen()
	if err != nil {
		panic("world: NoiseGenerator: build igloo start generator: " + err.Error())
	}
	swampGen, err := structure.NewSwampHutStartGen()
	if err != nil {
		panic("world: NoiseGenerator: build swamp hut start generator: " + err.Error())
	}
	// STRUCT-03 (15-01): the mineshaft — the FIRST true recursive multi-piece structure, on the
	// legacy_type_3 frequency-reduction placement path (Pitfall #4, NOT the spacing-grid path).
	mineshaftGen, err := structure.NewMineshaftStartGen()
	if err != nil {
		panic("world: NoiseGenerator: build mineshaft start generator: " + err.Error())
	}
	// STRUCT-04 (15-02): the stronghold — the UNIQUE concentric_rings placement (Pitfall #5).
	// Its ~128 ring positions are GLOBAL (precomputed once for the world, NOT a per-chunk
	// decision). The StrongholdRingState is OWNED here (constructed ONCE, fed the world seed +
	// the biomeAt seam + the embedded #stronghold_biased_to preferred set); the expensive
	// biome-validated spiral runs a SINGLE time via sync.Once, never per chunk/per worker. The
	// dependency stays one-directional (world -> world/structure). The placement-half generator
	// gates on isPlacementChunk; its pieces are STUBBED (byte-inert) this plan — 15-03 fills them.
	strongholdBiased, err := structure.LoadStrongholdBiasedTo()
	if err != nil {
		panic("world: NoiseGenerator: load stronghold_biased_to biome set: " + err.Error())
	}
	ringState := structure.NewStrongholdRingState(seed, biomeAt, strongholdBiased)
	strongholdGen := structure.NewStrongholdStartGen(ringState)
	// STRUCT-05 (16-02): villages — the data-driven jigsaw structure (the bounded-BFS Placer).
	// A standard random_spread StartGenerator (salt 10387312 / spacing 34 / separation 8), pure
	// over (seed,pos) — NO new worldgen-state plumbing (unlike the stronghold's ring state). The
	// 5 biome variants (plains/desert/savanna/snowy/taiga) are weight-picked + biome-gated per
	// chunk. Registered at the SAME composite site the temples + mineshaft + stronghold use.
	villageGen, err := structure.NewVillageStartGen()
	if err != nil {
		panic("world: NoiseGenerator: build village start generator: " + err.Error())
	}
	structGen := structure.NewCompositeStartGenerator(desertGen, jungleGen, iglooGen, swampGen, mineshaftGen, strongholdGen, villageGen)

	return &NoiseGenerator{
		seed:        seed,
		secs:        secs,
		minY:        minY,
		router:      r,
		biomes:      bs,
		surface:     ss,
		rule:        rule,
		carvers:     carvers,
		rep:         rep,
		deco:        deco,
		structCache: structCache,
		structGen:   structGen,
		air:         block.ToStateID[block.Air{}],
	}
}

// GenerateTerrain drives the PURE single-chunk terrain pipeline into a fresh
// capture-diff-sealed level.Chunk and returns it at StatusCarvers. PURE over (seed, pos).
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
//     expose bare stone, matching vanilla. The carve footprint guard stays — terrain
//     is still single-chunk.
//
// The chunk is left at StatusCarvers; the worker stages it and the Decorate pass
// (below) promotes it to StatusFull once the 3x3 is carved (GEN2-02 seam).
func (g *NoiseGenerator) GenerateTerrain(pos level.ChunkPos) *level.Chunk {
	// (0) STRUCT-POLISH-03 — THE ORDERING HAZARD (Pitfall 3). The structure Beardifier
	// contributes to final_density at FILL time, but Sulfur places structures during
	// Decorate (post-fill). So the structure STARTS for C + its ±12-block window MUST be
	// computed BEFORE fillFromNoise. ComputeStarts is pure geometry over (seed,pos) +
	// singleflight-deduped + memoized (the surface sampler reads the router, NOT the chunk),
	// so computing it earlier is cheap, order-independent, and triggers NO neighbor chunk
	// generation. A piece can be within 12 blocks of C only from C itself or an immediately
	// adjacent chunk (12 < 16), so the ±1 chunk ring of starts is the complete candidate set;
	// ForStructuresInChunk's isCloseToChunk(12) gate then keeps only the truly-close pieces.
	// When NO adapting structure (village/stronghold) is near C, the Beardifier is EMPTY and
	// Compute returns 0 everywhere -> the fill is byte-identical (the NONE regression guard).
	beardifier := g.beardifierFor(pos)
	nc := noisechunk.NewNoiseChunkWithBeard(g.router, pos, beardifier.Compute)
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

	// GEN2-02: leave the chunk at StatusCarvers — the FINISH tail (worldgen heightmaps,
	// sky light, promote-to-full) now lives in Decorate, which the worker runs once the
	// 3x3 neighborhood is carved. GenerateTerrain stays pure over (seed, pos).
	ch.Status = level.StatusCarvers
	return ch
}

// Decorate is the post-carve decoration pass over a 3x3 view, run by the worker once all
// 8 neighbors of view.center are carved. It is now LIVE (FEAT-02): after building the
// pre-decoration worldgen heightmaps it runs applyBiomeDecoration — the 11-step
// GenerationStep.Decoration loop over the retained 3x3 biome set, seeding each feature via
// WorldgenRandom.SetFeatureSeed(decoSeed, globalIndex, step) and placing it through the
// 3x3 Neighborhood (which keeps the worldgen heightmaps live across feature writes).
//
// Phase 11 ships the ORCHESTRATION without feature bodies: every parsed feature type
// dispatches to a recordable no-op placer (the real Feature.place bodies are Phase 12+),
// so production decoration writes NO blocks yet — but the per-feature seed/index discipline
// is exercised + verifiable now (the trace test). Because no blocks are written, the
// emitted chunk bytes are identical to Phase 10 here.
//
// OPEN DECISION D2 (late-neighbor-write-after-emit) is RESOLVED in the worker with Option Y
// (hold-until-neighborhood-complete, complete-on-first-send, no re-send): a center is
// decorated as soon as its 3x3 is carved (writing features into its neighbors) but emitted
// only once every WANTED neighbor that holds it is also decorated — so no write lands after
// the immutable handoff. See world/worker.go tryDecorate/tryEmit.
//
// Pure over (seed, center.pos) given the fully-carved neighborhood: each feature's rng is
// pure over (worldSeed, originX, originZ, globalIndex, stepIndex) — independent of WHICH
// center decorated first, so the SET or ORDER of decorated centers cannot affect this
// center's output (the determinism contract the 5x5 reorder test pins).
func (g *NoiseGenerator) Decorate(view *Neighborhood) {
	ch := view.chunks[packPos(view.center)]
	if ch == nil {
		return
	}

	// Build all 3 worldgen heightmaps (WORLD_SURFACE_WG / OCEAN_FLOOR_WG / MOTION_BLOCKING)
	// from the FINAL post-carve terrain so they reflect carved openings and are live before
	// the first feature reads them (heightmap-relative placement). The incremental
	// level.HeightmapUpdate (wired into Neighborhood.SetBlock) keeps them live on each
	// subsequent feature block write. The 3 CLIENT heightmaps stay finalized by BuildSurface's
	// writeClientHeightmaps (the wire authority) during GenerateTerrain.
	surface.BuildWorldgenHeightmaps(ch, g.minY, g.minY+g.secs*16)

	// LIVE decoration over the 3x3. The biome lookup is the same per-Decorate quart-cell
	// cache Generate uses (one evaluation per distinct quart cell, behavior-identical to the
	// uncached source). The retained biome set is the distinct biomes across the center + 8
	// neighbors. applyBiomeDecoration drives the 11 steps in seed/index order.
	bc := newBiomeCache(g.biomes)
	biomeAt := bc.get
	height := g.secs * 16
	ctx := newPlacementContext(view, g.minY, height, biomeAt)
	biomes := retainedBiomes(view)

	wg := levelgen.NewWorldgenRandom(g.seed)
	makePlacer := func(pf *feature.PlacedFeature) placement.PlacerFunc {
		var cf *feature.ConfiguredFeature
		if pf != nil {
			cf = pf.Feature
		}
		// Production: no test feature, no invocation recording. g.deco.registry is
		// threaded into the bodyContext so a registered body (12-03's selectors) can
		// resolve nested sub-features; an unregistered type stays a no-op.
		return newConfiguredPlacer(cf, view, g.deco.registry, g.air, false, nil)
	}
	applyBiomeDecoration(view, biomes, g.deco, ctx, wg, g.seed, makePlacer, nil)

	// STRUCT-01: the two-pass structure seam runs HERE — at the END of Decorate, AFTER
	// applyBiomeDecoration (vanilla's FEATURES order) so structures overwrite terrain +
	// features. The pipeline is fill->surface->carve->[STARTS(C)]->[REFERENCES(C)]->
	// [PLACE(C)]->finish:
	//
	//   (a) STARTS(C): compute + cache the structure starts OWNED by C. Pure geometry over
	//       (seed, C) (the surface sampler reads the router, NOT the chunk), singleflight-
	//       deduped, NO block writes — so it does not disturb the fill/surface/carve order.
	//   (b) REFERENCES(C): the +-8 bbox-intersect scan that on-demand-computes STARTS for
	//       every cell in C's +-8 window (each pure (seed,pos) + singleflight-deduped, so
	//       the radius-8 references read radius-8 data, not a radius-1 ring — no neighbor
	//       chunk GENERATION is triggered, only pure start geometry).
	//   (c) PLACE(C): the placeStructures hook — EMPTY this plan (14-02 fills it: gather
	//       StartsForChunk(C) + clip each piece to C's writable column). With the inert
	//       StartGenerator + the empty hook, ZERO blocks are written, so the emitted chunk
	//       bytes are IDENTICAL to Phase 13 (the 5x5 reorder + emit-once gates stay green).
	g.placeStructures(view)

	// Sky light for rendering (mirrors Superflat / FillChunk finishing). FillChunk set
	// FluidCount/biome defaults and BuildSurface rewrote the CLIENT heightmaps; sky light is
	// applied here defensively so every present section is lit regardless of the fill path.
	//
	// RECOUNT FluidCount HERE, at the END of the full pipeline. finishChunk (inside FillChunk)
	// counted fluids too early — the aquifer/carve/decoration passes write water AFTER FillChunk
	// returns, so a count taken in finishChunk misses that water and the section ships
	// FluidCount=0. The client uses nonEmptyFluidCount to decide whether a section has fluid to
	// SIMULATE, so a 0 made generated/aquifer water inert client-side (a player would not float in
	// it until a block update woke the cell). Recounting on the fully-built chunk fixes it.
	// Cite: net.minecraft.world.level.chunk.LevelChunkSection.nonEmptyFluidCount.
	for i := range ch.Sections {
		s := &ch.Sections[i]
		if len(s.SkyLight) != 2048 {
			s.SkyLight = fullSkyLight()
		}
		s.FluidCount = level.CountFluidBlocks(s)
	}
	ch.Status = level.StatusFull
}

// placeStructures runs the STRUCT-01 two-pass structure seam for the center chunk C of
// view: (a) ComputeStarts(seed, C) caches C's owned starts (pure geometry, no blocks),
// (b) ComputeReferences(seed, C) runs the +-8 compute-on-demand scan recording which
// neighbor starts reach into C, and (c) the PLACE pass — EMPTY this plan.
//
// 14-02 fills the PLACE pass: gather g.structCache.StartsForChunk(C) (C's own + the
// referenced neighbor starts) and clip each piece to C's writable column via the
// Neighborhood proxy. This plan lands (a)+(b)+the empty hook, so with the inert
// StartGenerator the chunk bytes stay byte-identical to Phase 13 (ZERO blocks placed).
//
// Runs on the scheduler goroutine (called from Decorate via tryDecorate). The cache's
// xsync.Map is the ONLY cross-goroutine state and a pure memoization; the +-8 on-demand
// ComputeStarts are pure (seed,pos) geometry (the surface sampler reads the router, NOT
// the chunk) so they NEVER trigger neighbor chunk generation.
func (g *NoiseGenerator) placeStructures(view *Neighborhood) {
	center := view.center
	minY, height := g.minY, g.secs*16

	// (a) STARTS for C — pure geometry, singleflight-deduped, no block writes.
	g.structCache.ComputeStarts(g.seed, center, g.structGen)

	// (b) REFERENCES for C — the +-8 scan that on-demand-computes STARTS per cell, so a
	// start owned up to 8 chunks out reaching C is found (not a radius-1 truncation).
	g.structCache.ComputeReferences(g.seed, center, g.structGen, minY, height)

	// (c) PLACE (14-02, FILLED): gather C's own + referenced starts (StartsForChunk) and
	// placeInChunk each into C's writable column. Each piece is written CLIPPED to C's 16x16
	// slice (placeBlock's box.IsInside guard) AND to the 3x3 Neighborhood window (the
	// Neighborhood drops out-of-3x3 writes) — so a chunk-spanning pyramid is placed once per
	// overlapping chunk, idempotently (the piece RNG is re-derivable over (seed,ownerChunk),
	// Pitfall #2). Structures overwrite terrain + features (vanilla FEATURES order).
	g.structCache.PlaceStructures(view, center, g.seed, minY, height)
}

// beardifierFor builds the STRUCT-POLISH-03 structure Beardifier for chunk C: it gathers the
// structure STARTS owned by C and its ±1 chunk ring (the complete candidate set for a piece
// within 12 blocks of C, since 12 < 16), dedupes by owner, and hands them to
// structure.ForStructuresInChunk, which keeps only adapting structures (village=beard_thin /
// stronghold=bury) whose pieces are close to C (isCloseToChunk(12)) and inflates the union by
// 24 to form the affectedBox. NONE-adaptation structures (temples/igloo/mineshaft/swamp-hut)
// are NOT gathered -> EMPTY -> Compute returns 0 -> byte-identical terrain.
//
// PURE over (seed, C): ComputeStarts is pure (seed,pos) geometry, singleflight-deduped, and
// memoized — calling it here (pre-fill) reads the SAME cache the Decorate-time STARTS/REFERENCES
// pass uses (and the same cache 20-03's persistence may have seeded from region), and triggers
// NO neighbor chunk generation (only pure start geometry over a bounded ±1 ring).
func (g *NoiseGenerator) beardifierFor(pos level.ChunkPos) *structure.Beardifier {
	seen := make(map[level.ChunkPos]bool)
	var candidates []*structure.StructureStart
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			cell := level.ChunkPos{pos[0] + int32(dx), pos[1] + int32(dz)}
			if seen[cell] {
				continue
			}
			seen[cell] = true
			candidates = append(candidates, g.structCache.ComputeStarts(g.seed, cell, g.structGen)...)
		}
	}
	return structure.ForStructuresInChunk(candidates, pos)
}

// Dims returns the generator's (minY, height) so the worker can size the Neighborhood
// proxy without re-deriving the chunk geometry.
func (g *NoiseGenerator) Dims() (minY, height int) { return g.minY, g.secs * 16 }

// StructureCache exposes the per-world StructureStart cache so the worker can seed it from a
// region-loaded chunk's persisted `structures` NBT (STRUCT-POLISH-04 read path) and serialize a
// chunk's OWN starts back into the `structures` compound on save (the write path). It is the SAME
// cache ComputeStarts/StartsForChunk read, so a start seeded here SHORT-CIRCUITS the recompute:
// ComputeStarts(seed,pos) returns the seeded slice (a cache hit) instead of regenerating. This is
// the accessor the worker's structureCacheHolder interface asserts; only NoiseGenerator carries a
// cache (Superflat owns no structures and returns the zero value, so the worker skips seeding).
func (g *NoiseGenerator) StructureCache() *structure.Cache { return g.structCache }

// Generate drives the full pipeline into a StatusFull level.Chunk via the concrete
// single-chunk path (= GenerateTerrain then Decorate over a freshly-terrain-generated
// 3x3). PURE over (seed, pos). It is NOT on the Generator interface — it is retained for
// tests / SpawnSurfaceY / single-chunk callers and is byte-identical to the old single-shot
// Generate (the no-op Decorate runs the same FINISH tail that used to live inline).
func (g *NoiseGenerator) Generate(pos level.ChunkPos) *level.Chunk {
	return decorateSingle(g, pos)
}

// SpawnSurfaceY derives the world-Y of the top standable block for the SAFE spawn column —
// it is the feet-Y of SpawnPos minus 1 (the block the player stands ON). main.go now prefers
// SpawnPos (the full safe x/y/z) for the join/respawn placement; SpawnSurfaceY is retained as
// the scalar fallback the tick's SetSpawn carries (and for any legacy single-Y caller).
//
// HISTORY (17-06): the OLD SpawnSurfaceY sampled the TERRAIN-ONLY WorldSurface heightmap
// (GenerateTerrain) at the fixed (8,8) column and returned (WorldSurface.Get-1) — computing
// the spawn Y BEFORE worldgen decoration/structures placed blocks in the spawn column, so a
// tree trunk / vine / village house at (8,8) buried the player. That terrain-only read was
// the spawn-inside-a-block bug. It now delegates to SpawnPos (the ported vanilla
// PlayerSpawnFinder over the FULLY-DECORATED chunk), so the returned Y is the safe standable
// floor; sendPlayBootstrap's "+2" then lands the player's feet two air blocks above it.
//
// If the whole spawn chunk is void/ocean (SpawnPos.Found == false), it falls back to the old
// terrain WorldSurface-top at (8,8) so callers always get a sane scalar (the player floats on
// the ocean surface rather than dropping into a nil result).
func (g *NoiseGenerator) SpawnSurfaceY(pos level.ChunkPos) int {
	if sp := g.SpawnPos(pos); sp.Found {
		// sp.Y is the FEET air cell; the standable floor block is one below. SetSpawn/+2 then
		// re-add 2, landing the player at sp.Y+1 — one above the original feet. To preserve the
		// "+2 above the surface block" contract exactly, return the floor block world-Y (feet-1).
		return int(sp.Y) - 1
	}
	// Void/ocean spawn chunk: fall back to the terrain WorldSurface top at the (8,8) center so
	// the scalar spawn-Y stays sane (player floats on the ocean surface, not a nil).
	ch := g.GenerateTerrain(pos)
	const spawnLocalX, spawnLocalZ = 8, 8
	col := (spawnLocalZ << 4) | spawnLocalX
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
