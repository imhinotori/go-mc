package carver

import (
	"math"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// getRange ports WorldCarver.getRange() = 4 (constant). The carve loop iterates the
// source chunks in [-getRange()*2, getRange()*2] = [-8,8] in both x and z around the
// target chunk (applyCarvers), so a carve started up to 8 chunks away can reach the
// target — caves/ravines are continuous across chunk boundaries.
const carverRange = 4

// CarveChunk is the writable view the carver operates on: it reads the existing
// block at a world position (to gate on the replaceables tag) and writes the carved
// block. Writes outside the target chunk's 16x16 footprint are dropped by the
// implementation — a carve started in a neighbor source chunk only edits the blocks
// that fall inside the target chunk being generated (the carving range + the
// in-footprint write bound together make caves cross chunk borders without editing
// other chunks). MinY/Height bound the vertical carve range.
type CarveChunk interface {
	// Pos returns the target chunk position (the chunk being generated).
	Pos() level.ChunkPos
	// Get returns the current block state at a world position (for canReplaceBlock).
	Get(wx, wy, wz int) block.StateID
	// Set writes a carved block state at a world position. Implementations MUST drop
	// writes whose (wx,wz) fall outside the target chunk's 16x16 column.
	Set(wx, wy, wz int, state block.StateID)
	// MinY is the world floor (NoiseSettings.min_y).
	MinY() int
	// Height is the build height (NoiseSettings.height).
	Height() int
}

// FluidSource makes the carve aquifer-aware: at a carved position it returns the
// fluid block to place (water/lava) or (0,false) meaning air. The Generator (Wave 8)
// wires this to the Wave-5 Aquifer (computeSubstance at density 0); the lava-level
// rule in getCarveState short-circuits below the config's lava level. A carve below
// the local fluid level floods (water), above it is air — vanilla-faithful.
type FluidSource interface {
	// CarveFluid returns the aquifer fluid at a carved world position. ok=false means
	// air (the common case above the water table); ok=true with a water/lava state
	// floods the carved block.
	CarveFluid(wx, wy, wz int) (block.StateID, bool)
}

// carvingMask ports net.minecraft.world.level.chunk.CarvingMask: a per-target-chunk
// visited bitset over the chunk's columns x height, so a block is carved at most
// once across all carvers/segments. Indexed by (localX, worldY, localZ).
type carvingMask struct {
	minY   int
	height int
	bits   []bool
}

func newCarvingMask(minY, height int) *carvingMask {
	return &carvingMask{minY: minY, height: height, bits: make([]bool, 16*16*height)}
}

func (m *carvingMask) index(lx, y, lz int) int {
	return ((lx&15)*16+(lz&15))*m.height + (y - m.minY)
}

func (m *carvingMask) get(lx, y, lz int) bool {
	if y < m.minY || y >= m.minY+m.height {
		return true // out of range -> treat as already-carved (skip)
	}
	return m.bits[m.index(lx, y, lz)]
}

func (m *carvingMask) set(lx, y, lz int) {
	if y < m.minY || y >= m.minY+m.height {
		return
	}
	m.bits[m.index(lx, y, lz)] = true
}

// carveContext bundles the per-target-chunk carve state the WorldCarver methods
// thread (the ChunkAccess + CarvingMask + Aquifer in vanilla). air/caveAir/water are
// resolved once.
type carveContext struct {
	chunk CarveChunk
	mask  *carvingMask
	fluid FluidSource
	rep   *Replaceables

	air     block.StateID
	caveAir block.StateID
	water   block.StateID
	lava    block.StateID
	minGenY int
}

// skipChecker ports WorldCarver$CarveSkipChecker.shouldSkip(ctx, dx, dy, dz, y): the
// per-carver ellipsoid membership test. dx/dy/dz are the block's normalized offsets
// from the ellipsoid center; the cave/canyon supply their own shape test.
type skipChecker func(dx, dy, dz float64, y int) bool

// canReplaceBlock ports WorldCarver.canReplaceBlock(config, state) =
// state.is(config.replaceable). Only blocks in the overworld_carver_replaceables tag
// are carve-able.
func (cc *carveContext) canReplaceBlock(state block.StateID) bool {
	return cc.rep.Has(state)
}

// getCarveState ports WorldCarver.getCarveState(ctx, config, pos, aquifer): below the
// config's lava level -> lava; otherwise the aquifer substance at density 0 (water/
// lava if flooded, else air/cave_air). A nil (air) substance carves cave_air.
func (cc *carveContext) getCarveState(cfg *CarverConfig, wx, wy, wz int) block.StateID {
	if wy <= cfg.LavaLevel.resolveY(cc.minGenY) {
		return cc.lava
	}
	if st, ok := cc.fluid.CarveFluid(wx, wy, wz); ok {
		return st
	}
	return cc.caveAir
}

// carveBlock ports WorldCarver.carveBlock: gate on canReplaceBlock, resolve the carve
// state (aquifer-aware), write it. The grass/mycelium top-material restoration
// (vanilla replaces the block below with the biome top material when carving through
// grass) is a surface-cosmetic refinement deferred to the Wave-7 surface pass; the
// placement of air/water — the only thing the cave/ravine shape needs — is faithful.
// Returns true if a block was carved (for the carve()-result accumulation).
func (cc *carveContext) carveBlock(cfg *CarverConfig, wx, wy, wz int) bool {
	state := cc.chunk.Get(wx, wy, wz)
	if !cc.canReplaceBlock(state) {
		return false
	}
	carved := cc.getCarveState(cfg, wx, wy, wz)
	cc.chunk.Set(wx, wy, wz, carved)
	return true
}

// canReach ports WorldCarver.canReach(chunkPos, x, z, segment, segmentCount, radius):
// a cheap bounding test so a tunnel/canyon segment whose ellipse cannot intersect the
// target chunk is skipped. dx²+dz² - remaining² <= (radius+2+16)².
func canReach(pos level.ChunkPos, x, z float64, segment, segmentCount int, radius float32) bool {
	midX := float64(int(pos[0])*16 + 8)
	midZ := float64(int(pos[1])*16 + 8)
	dx := x - midX
	dz := z - midZ
	remaining := float64(segmentCount - segment)
	bound := float64(radius+2.0) + 16.0
	return dx*dx+dz*dz-remaining*remaining <= bound*bound
}

// carveEllipsoid ports WorldCarver.carveEllipsoid: carve every replaceable block in
// the ellipsoid centered at (x,y,z) with horizontal radius hr and vertical radius vr,
// bounded to the target chunk's 16x16 footprint and the carve y-range, gated by the
// per-carver skipChecker and the CarvingMask. Returns true if anything was carved.
func (cc *carveContext) carveEllipsoid(cfg *CarverConfig, x, y, z, hr, vr float64, skip skipChecker) bool {
	pos := cc.chunk.Pos()
	midX := float64(int(pos[0])*16 + 8)
	midZ := float64(int(pos[1])*16 + 8)
	rangeBlocks := 16.0 + hr*2.0

	// Early reject if the center is too far from the chunk to reach.
	if math.Abs(x-midX) > rangeBlocks || math.Abs(z-midZ) > rangeBlocks {
		return false
	}

	minBlockX := int(pos[0]) * 16
	minBlockZ := int(pos[1]) * 16

	x0 := max(mthFloor(x-hr)-minBlockX-1, 0)
	x1 := min(mthFloor(x+hr)-minBlockX, 15)
	y0 := max(mthFloor(y-vr)-1, cc.minGenY+1)
	// Vanilla caps the top at minGenY + genDepth - 1 - 7 (the upgrade margin is 7 for
	// a non-upgrading chunk). genDepth == Height here.
	y1 := min(mthFloor(y+vr)+1, cc.minGenY+cc.chunk.Height()-1-7)
	z0 := max(mthFloor(z-hr)-minBlockZ-1, 0)
	z1 := min(mthFloor(z+hr)-minBlockZ, 15)

	carvedAny := false
	for lx := x0; lx <= x1; lx++ {
		bx := minBlockX + lx
		ndx := (float64(bx) + 0.5 - x) / hr
		for lz := z0; lz <= z1; lz++ {
			bz := minBlockZ + lz
			ndz := (float64(bz) + 0.5 - z) / hr
			if ndx*ndx+ndz*ndz >= 1.0 {
				continue
			}
			for by := y1; by > y0; by-- {
				ndy := (float64(by) - 0.5 - y) / vr
				if skip(ndx, ndy, ndz, by) {
					continue
				}
				if cc.mask.get(lx, by, lz) {
					continue
				}
				cc.mask.set(lx, by, lz)
				if cc.carveBlock(cfg, bx, by, bz) {
					carvedAny = true
				}
			}
		}
	}
	return carvedAny
}

// worldCarver is the abstract carve(): cave.go and canyon.go implement it.
type worldCarver interface {
	// carve walks the carver's path from a source chunk, carving into the target
	// chunk (cc.chunk). rng is the per-source-chunk legacy random (already
	// probability-rolled by isStartChunk). Returns true if anything was carved.
	carve(cfg *CarverConfig, cc *carveContext, rng *legacyRandom, src level.ChunkPos) bool
	// isStartChunk ports the probability roll: rng.nextFloat() <= probability.
	isStartChunk(cfg *CarverConfig, rng *legacyRandom) bool
}

// carverFor returns the worldCarver implementation for a config kind.
func carverFor(cfg *CarverConfig) worldCarver {
	switch cfg.Kind {
	case KindCanyon:
		return canyonWorldCarver{}
	default:
		return caveWorldCarver{}
	}
}

// ConfiguredCarver pairs a carver shape with its parsed config (the
// ConfiguredWorldCarver in vanilla). The biome's carver list is an ordered slice of
// these; the carver INDEX in the list salts the per-chunk seed.
type ConfiguredCarver struct {
	cfg    *CarverConfig
	carver worldCarver
}

// NewConfiguredCarver builds a ConfiguredCarver from a parsed config.
func NewConfiguredCarver(cfg *CarverConfig) *ConfiguredCarver {
	return &ConfiguredCarver{cfg: cfg, carver: carverFor(cfg)}
}

// LoadOverworldCarvers parses the plains biome's carver list
// (cave, cave_extra_underground, canyon) into ConfiguredCarvers, in the biome's
// order (the order is load-bearing — it salts each carver's per-chunk seed).
func LoadOverworldCarvers() ([]*ConfiguredCarver, error) {
	ids := []string{"minecraft:cave", "minecraft:cave_extra_underground", "minecraft:canyon"}
	out := make([]*ConfiguredCarver, 0, len(ids))
	for _, id := range ids {
		cfg, err := ParseCarverConfig(id)
		if err != nil {
			return nil, err
		}
		out = append(out, NewConfiguredCarver(cfg))
	}
	return out, nil
}

// ApplyCarvers ports NoiseBasedChunkGenerator.applyCarvers: the cross-chunk carve
// driver. For each source chunk in the carving range ([-8,8] x [-8,8] around the
// target), for each configured carver (in biome order, index = the seed salt), it
// seeds a per-source-chunk legacy random from (worldSeed + carverIdx, srcX, srcZ),
// rolls the carver's probability (isStartChunk), and if it passes runs carve() into
// the target chunk. A single per-target-chunk CarvingMask de-dups overlapping carves.
//
// worldSeed is the world seed; chunk is the target chunk being generated; fluid is
// the aquifer-aware fluid source; carvers is the biome's ordered carver list; rep is
// the replaceables set. Returns the number of source-chunk carve passes that carved
// at least one block (for tests/metrics).
func ApplyCarvers(worldSeed int64, chunk CarveChunk, fluid FluidSource, carvers []*ConfiguredCarver, rep *Replaceables) int {
	mask := newCarvingMask(chunk.MinY(), chunk.Height())
	cc := &carveContext{
		chunk:   chunk,
		mask:    mask,
		fluid:   fluid,
		rep:     rep,
		air:     block.ToStateID[block.Air{}],
		caveAir: block.ToStateID[block.CaveAir{}],
		water:   block.ToStateID[block.Water{Level: 0}],
		lava:    block.ToStateID[block.Lava{Level: 0}],
		minGenY: chunk.MinY(),
	}

	rng := newLegacyRandom(0)
	pos := chunk.Pos()
	carvedPasses := 0

	for dx := -carverRange * 2; dx <= carverRange*2; dx++ {
		for dz := -carverRange * 2; dz <= carverRange*2; dz++ {
			src := level.ChunkPos{pos[0] + int32(dx), pos[1] + int32(dz)}
			for idx, conf := range carvers {
				// setLargeFeatureSeed(worldSeed + carverIdx, srcX, srcZ): the per-source-chunk
				// carver seed (applyCarvers passes worldSeed + (long)carverIndex).
				rng.setLargeFeatureSeed(worldSeed+int64(idx), int(src[0]), int(src[1]))
				if !conf.carver.isStartChunk(conf.cfg, rng) {
					continue
				}
				if conf.carver.carve(conf.cfg, cc, rng, src) {
					carvedPasses++
				}
			}
		}
	}
	return carvedPasses
}

// mthFloor ports net.minecraft.util.Mth.floor(double) = (int)Math.floor(d).
func mthFloor(d float64) int {
	i := int(d)
	if float64(i) > d {
		i--
	}
	return i
}

// randomBetween ports Mth.randomBetween(rng, lo, hi) = lo + rng.nextFloat()*(hi-lo).
func randomBetween(r *legacyRandom, lo, hi float32) float32 {
	return lo + r.nextFloat()*(hi-lo)
}
