package structure

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// concentric_rings.go ports the STRONGHOLD PLACEMENT — the UNIQUE concentric_rings model
// (Pitfall #5, the architectural risk this plan isolates). Unlike every other structure
// (temples = the spacing grid; mineshaft = the legacy frequency reducer), the stronghold's
// ~128 chunk positions are NOT a per-chunk decision: they are precomputed ONCE for the
// whole world by a seeded biome-validated spiral, and isPlacementChunk(pos) is the
// precomputed-list-contains test.
//
// Sources (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.chunk.ChunkGeneratorStructureState.generateRingPositions
//     (the seeded spiral: ring/count distribution, the radius/angle/round math, the fork
//     per position, the count-redistribution per ring)
//   - net.minecraft.world.level.chunk.ChunkGeneratorStructureState.createForNormal
//     (concentricRingsSeed == the RAW world seed — lload_1 passed for BOTH levelSeed and
//     concentricRingsSeed; NO salt, NO hash; the ring RNG = RandomSource.create() = the
//     legacy java.util.Random LCG, then setSeed(worldSeed))
//   - net.minecraft.world.level.biome.BiomeSource.findBiomeHorizontal (the quart-grid
//     spiral search out to radius 112 block / 28 quart, step 1, reservoir-sampled accept
//     of the first/random predicate match — the #stronghold_biased_to adjustment)
//   - net.minecraft.world.level.levelgen.structure.placement.ConcentricRingsStructurePlacement
//     (count/distance/spread/preferred_biomes/salt; isPlacementChunk = getRingPositionsFor
//     .contains)

// ConcentricRingsStructurePlacement is the parsed concentric_rings placement body (the
// strongholds structure_set). count = the number of ring positions (128), distance = the
// base ring radius factor in chunks (32), spread = how many positions share an angular
// group before the ring index advances (3), salt = the placement salt (0 for strongholds;
// unused by the ring spiral, which seeds off the raw world seed).
type ConcentricRingsStructurePlacement struct {
	Count           int
	Distance        int
	Spread          int
	Salt            int
	PreferredBiomes string
}

// concentricRingsJSON is the placement body for type concentric_rings.
type concentricRingsJSON struct {
	Type            string `json:"type"`
	Count           int    `json:"count"`
	Distance        int    `json:"distance"`
	Spread          int    `json:"spread"`
	PreferredBiomes string `json:"preferred_biomes"`
	Salt            int    `json:"salt"`
}

// LoadConcentricRingsPlacement loads + parses an embedded structure_set whose placement is
// concentric_rings (e.g. "minecraft:strongholds"). It REQUIRES the concentric_rings type
// (a random_spread set is rejected here — that path is LoadStructureSet).
func LoadConcentricRingsPlacement(id string) (ConcentricRingsStructurePlacement, error) {
	raw, err := data.StructureSetJSON(id)
	if err != nil {
		return ConcentricRingsStructurePlacement{}, err
	}
	var js struct {
		Placement concentricRingsJSON `json:"placement"`
	}
	if err := json.Unmarshal(raw, &js); err != nil {
		return ConcentricRingsStructurePlacement{}, fmt.Errorf("structure: parsing concentric_rings set %q: %w", id, err)
	}
	pj := js.Placement
	if pj.Type != "minecraft:concentric_rings" && pj.Type != "concentric_rings" {
		return ConcentricRingsStructurePlacement{}, fmt.Errorf("structure: structure_set %q placement type %q is not concentric_rings", id, pj.Type)
	}
	return ConcentricRingsStructurePlacement{
		Count:           pj.Count,
		Distance:        pj.Distance,
		Spread:          pj.Spread,
		Salt:            pj.Salt,
		PreferredBiomes: pj.PreferredBiomes,
	}, nil
}

// LoadStrongholdBiasedTo loads + parses the embedded #stronghold_biased_to biome tag (the
// ~38-biome preferred set the ring spiral biome-validates each candidate against). The tag
// is FLAT (no nested #-refs — all 38 entries are concrete biome ids), so a direct parse
// suffices; resolveBiomeTagValues is reused defensively in case a future tag nests refs.
func LoadStrongholdBiasedTo() (map[string]bool, error) {
	raw, err := data.StrongholdBiasedTo()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	visited := make(map[string]bool)
	if err := resolveBiomeTagValues(raw, set, visited, "stronghold_biased_to"); err != nil {
		return nil, err
	}
	return set, nil
}

// generateRingPositions ports ChunkGeneratorStructureState.generateRingPositions: the
// seeded biome-validated spiral that computes the ~count ring chunk positions for a world.
// It is PURE over (worldSeed, placement). biomeAt is the (block-coord) biome seam the
// #stronghold_biased_to adjustment queries; preferred is the namespaced-id preferred set.
//
// The draw order is bit-exact to the bytecode (a divergence moves every stronghold):
//   - ring RNG = the legacy LCG seeded with the RAW world seed (concentricRingsSeed).
//   - angle0 = nextDouble() * PI * 2.
//   - for each of count positions:
//     dist = double(4*distance + distance*ring*6) + (nextDouble()-0.5) * double(distance) * 2.5
//     x = round(cos(angle)*dist); z = round(sin(angle)*dist)
//     fork = rng.fork(); pos = biome-adjust(x, z, preferred, fork)
//     angle += 2*PI / spread
//   - per spread-group: ring++; spread = min(spread + 2*spread/(ring+1), count-index);
//     angle = nextDouble() * PI * 2.
func generateRingPositions(worldSeed int64, p ConcentricRingsStructurePlacement, biomeAt BiomeAt, preferred map[string]bool) []level.ChunkPos {
	if p.Count == 0 {
		return nil
	}
	distance := p.Distance
	count := p.Count
	spread := p.Spread

	out := make([]level.ChunkPos, 0, count)

	// RandomSource.create().setSeed(concentricRingsSeed) — the legacy java.util.Random LCG
	// seeded with the RAW world seed (createForNormal passes the world seed verbatim).
	rng := levelgen.NewLegacyRandomSource(0)
	rng.SetSeed(worldSeed)

	angle := rng.NextDouble() * math.Pi * 2.0

	groupIdx := 0 // var12: index within the current spread group
	ring := 0     // var13: ring number

	for i := 0; i < count; i++ {
		// dist (var15): the per-position radius in chunks.
		dist := float64(4*distance+distance*ring*6) + (rng.NextDouble()-0.5)*float64(distance)*2.5
		x := int(math.Round(math.Cos(angle) * dist))
		z := int(math.Round(math.Sin(angle) * dist))

		// fork the RNG per position (the biome-search reservoir uses it for tie-breaks).
		fork := rng.Fork()
		pos := adjustToPreferredBiome(x, z, preferred, biomeAt, fork)
		out = append(out, pos)

		angle += (math.Pi * 2.0) / float64(spread)
		groupIdx++
		if groupIdx == spread {
			ring++
			groupIdx = 0
			spread = spread + 2*spread/(ring+1)
			if rem := count - i; spread > rem {
				spread = rem
			}
			angle = rng.NextDouble() * math.Pi * 2.0
		}
	}
	return out
}

// adjustToPreferredBiome ports the generateRingPositions lambda (findBiomeHorizontal): the
// rounded spiral chunk (x,z) is snapped to its block-center (SectionPos.sectionToBlockCoord
// = chunk*16 + 8) and a quart-grid spiral search out to radius 112 block / 28 quart (step 1)
// finds the nearest #stronghold_biased_to biome; on a hit the chunk is moved to the found
// biome's chunk (blockToSectionCoord = block>>4). On no hit the original (x,z) chunk stands.
//
// The search is reservoir-sampled exactly like findBiomeHorizontal: the FIRST match is kept,
// each later match replaces it with probability 1/(matchCount+1) (rng.NextIntN(matchCount+1)
// == 0), so the result is a uniformly-random nearest-ring match — bit-exact given the fork.
func adjustToPreferredBiome(chunkX, chunkZ int, preferred map[string]bool, biomeAt BiomeAt, rng levelgen.RandomSource) level.ChunkPos {
	const radiusBlock = 112
	centerBlockX := chunkX<<4 + 8
	centerBlockZ := chunkZ<<4 + 8

	// Quart coords (block>>2). The search iterates quart cells; samples convert back to
	// block via quart<<2 for the biomeAt seam (which keys on block coords -> quart internally).
	cqx := centerBlockX >> 2
	cqz := centerBlockZ >> 2
	maxQuart := radiusBlock >> 2 // 28
	step := 1

	var found *level.ChunkPos
	matchCount := 0

	for radius := 0; radius <= maxQuart; radius += step {
		for dz := -radius; dz <= radius; dz += step {
			onBorderZ := abs(dz) == radius
			for dx := -radius; dx <= radius; dx += step {
				// findClosest=false branch: only sample the ring BORDER cells.
				if !onBorderZ && abs(dx) != radius {
					continue
				}
				qx := cqx + dx
				qz := cqz + dz
				sampleBlockX := qx << 2
				sampleBlockZ := qz << 2
				bt := biomeAt(sampleBlockX, 0, sampleBlockZ)
				if !preferred[bt.String()] {
					continue
				}
				if found == nil || rng.NextIntN(int32(matchCount+1)) == 0 {
					cp := level.ChunkPos{int32(qx << 2 >> 4), int32(qz << 2 >> 4)}
					found = &cp
				}
				matchCount++
			}
		}
	}

	if found != nil {
		return *found
	}
	return level.ChunkPos{int32(chunkX), int32(chunkZ)}
}

// abs is the int absolute value (Go has no builtin for int).
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// StrongholdRingState is the per-world home of the precomputed ring positions (the
// concentric-rings worldgen-state decision, locked here). The NoiseGenerator OWNS it
// (constructed ONCE in NewNoiseGenerator, fed the world seed + the biomeAt seam + the
// embedded #stronghold_biased_to set), keeping world -> world/structure one-directional.
//
// The expensive biome-validated spiral (~128 positions, each a many-GetBiome search) runs
// EXACTLY ONCE for the world via sync.Once — never per chunk, never per worker goroutine
// (Pitfall #5 / T-15-09). After the Once the cached []ChunkPos + the packed-pos membership
// set are IMMUTABLE and read lock-free; isPlacementChunk is the O(1) contains-test.
type StrongholdRingState struct {
	seed      int64
	biomeAt   BiomeAt
	preferred map[string]bool

	once      sync.Once
	positions []level.ChunkPos
	member    map[int64]bool

	// computeProbe counts the number of times the spiral actually runs (test seam; nil in
	// production). The Once guarantees it reaches exactly 1.
	computeProbe *int64
}

// NewStrongholdRingState constructs the state WITHOUT computing (lazy): the spiral runs on
// the first isPlacementChunk / RingPositions / Ensure under the sync.Once.
func NewStrongholdRingState(seed int64, biomeAt BiomeAt, preferred map[string]bool) *StrongholdRingState {
	return &StrongholdRingState{seed: seed, biomeAt: biomeAt, preferred: preferred}
}

// newStrongholdRingStateInstrumented is the test ctor: it threads a compute-count probe so
// TestStrongholdRingState can assert the spiral runs exactly once under concurrent access.
func newStrongholdRingStateInstrumented(seed int64, biomeAt BiomeAt, preferred map[string]bool, probe *int64) *StrongholdRingState {
	rs := NewStrongholdRingState(seed, biomeAt, preferred)
	rs.computeProbe = probe
	return rs
}

// ensure runs the spiral once (under the sync.Once) and builds the immutable membership set.
func (s *StrongholdRingState) ensure() {
	s.once.Do(func() {
		if s.computeProbe != nil {
			atomic.AddInt64(s.computeProbe, 1)
		}
		p, err := LoadConcentricRingsPlacement("minecraft:strongholds")
		if err != nil {
			// The strongholds set is a build-time-trusted embed; a parse error is an asset
			// bug. Surface it loudly rather than silently producing zero strongholds.
			panic(fmt.Sprintf("structure: stronghold ring state: %v", err))
		}
		s.positions = generateRingPositions(s.seed, p, s.biomeAt, s.preferred)
		s.member = make(map[int64]bool, len(s.positions))
		for _, pos := range s.positions {
			s.member[packPos(pos)] = true
		}
	})
}

// isPlacementChunk reports whether pos is one of the precomputed ring positions (the GLOBAL
// contains-test that REPLACES the per-chunk spacing/frequency math). Triggers the one-time
// compute on first call; thereafter a lock-free map read.
func (s *StrongholdRingState) isPlacementChunk(pos level.ChunkPos) bool {
	s.ensure()
	return s.member[packPos(pos)]
}

// RingPositions returns the cached immutable ring-position list (computing it once if
// needed). The slice is never mutated after the Once — callers must not mutate it.
func (s *StrongholdRingState) RingPositions() []level.ChunkPos {
	s.ensure()
	return s.positions
}

// strongholdStartGen is the PLACEMENT-HALF stronghold StartGenerator (STRUCT-04). It gates
// on StrongholdRingState.isPlacementChunk INSTEAD of any spacing/frequency math; on a ring
// chunk it emits a single ANCHOR start (Structure minecraft:stronghold, ChunkPos==pos) with
// the piece set STUBBED (byte-inert this plan — 15-03 fills the recursive pieces, mirroring
// 14-01's empty PLACE hook). On a non-ring chunk it returns nil.
type strongholdStartGen struct {
	ringState *StrongholdRingState
}

// NewStrongholdStartGen builds the placement-half generator over a ring state.
func NewStrongholdStartGen(ringState *StrongholdRingState) StartGenerator {
	return &strongholdStartGen{ringState: ringState}
}

// GenerateStarts produces the stronghold anchor for pos iff pos is a ring chunk (pure over
// (seed,pos)). The start RNG is seeded via SetLargeFeatureSeed(seed,cx,cz) — the per-chunk
// structure-piece seed 15-03's recursive assembly will consume; this plan leaves the piece
// set empty (byte-inert) so the start is a placement-only anchor.
func (g *strongholdStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	if !g.ringState.isPlacementChunk(pos) {
		return nil
	}

	cx, cz := int(pos[0]), int(pos[1])

	// The piece RNG = SetLargeFeatureSeed(seed,cx,cz) (re-derivable per (seed,ownerChunk)), so
	// placeInChunk from ANY overlapping chunk redraws the SAME graph + clips to that chunk (the
	// cross-chunk idempotence seam — a stronghold spans MANY chunks deep underground).
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	// Sample the surface Y at the chunk-center column. Vanilla buries the stronghold well below
	// the surface; the StartPiece anchors its spiral StairsDown a fixed depth under it. Clamp to
	// a sane underground band so the recursive Y bound (10..200) admits the graph.
	centerX := cx*16 + 8
	centerZ := cz*16 + 8
	surfaceY := sampler.SampleSurfaceY(centerX, centerZ)
	anchorY := surfaceY - 24
	if anchorY < 20 {
		anchorY = 20
	}

	// 15-03: build the REAL recursive stronghold piece tree (the StartPiece spiral + the
	// twisting corridors/stairs/crossings + exactly one PortalRoom + the Library), reusing the
	// 15-01 mineshaft addChildren/FindCollisionPiece/genDepth-bounded recursion verbatim.
	pieces := assembleStronghold(rng, centerX, anchorY, centerZ)
	if len(pieces) == 0 {
		return nil
	}

	start := &StructureStart{
		Structure: "minecraft:stronghold",
		ChunkPos:  pos,
		Pieces:    pieces,
	}
	start.RecomputeBBox()
	return []*StructureStart{start}
}
