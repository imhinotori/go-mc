package carver

import (
	"github.com/imhinotori/sulfur/level"
)

// caveWorldCarver ports net.minecraft.world.level.levelgen.carver.CaveWorldCarver
// (javap -c, temp/cache/26.2-inner.jar): the extra winding tunnel caves that run on
// top of the noise caves. carve() walks one or more tunnels (a random branch count),
// each a chain of ellipsoid segments stepped by a yaw/pitch random walk, carving the
// replaceable blocks along the path to air (or water/lava via the aquifer). It is an
// algorithmic port, NOT a copy of Mojang source.
//
// Source: CaveWorldCarver.{carve, createRoom, createTunnel, getThickness,
// getCaveBound, getYScale, shouldSkip} + WorldCarver.carveEllipsoid/canReach.
// caveWorldCarver carries the four hooks that differ between CaveWorldCarver and its NetherWorldCarver
// subclass: the tunnel-count bound (getCaveBound), the vertical stretch (getYScale), the per-branch
// thickness draw (getThickness), and whether carveBlock uses the nether lava-floor rule instead of the
// aquifer. The overworld cave uses caveWorldCarver{bound:15, yScale:1.0, thicknessFn:caveThickness};
// the nether uses {bound:10, yScale:5.0, thicknessFn:netherThickness, nether:true}.
type caveWorldCarver struct {
	bound       int
	yScale      float64
	thicknessFn func(*legacyRandom) float32
	nether      bool
}

// getCaveBound ports CaveWorldCarver.getCaveBound() = 15.
const caveBound = 15

// getYScale ports CaveWorldCarver.getYScale() = 1.0.
const caveYScale = 1.0

// newCaveWorldCarver is the overworld cave carver (bound 15, yScale 1.0, overworld thickness).
func newCaveWorldCarver() caveWorldCarver {
	return caveWorldCarver{bound: caveBound, yScale: caveYScale, thicknessFn: getThickness}
}

// newNetherWorldCarver is the NetherWorldCarver: getCaveBound()=10, getYScale()=5.0, the doubled
// getThickness, and the lava-floor carveBlock. CITE: NetherWorldCarver overrides.
func newNetherWorldCarver() caveWorldCarver {
	return caveWorldCarver{bound: 10, yScale: 5.0, thicknessFn: getNetherThickness, nether: true}
}

// isStartChunk ports CaveWorldCarver.isStartChunk = rng.nextFloat() <= probability.
func (caveWorldCarver) isStartChunk(cfg *CarverConfig, rng *legacyRandom) bool {
	return rng.nextFloat() <= float32(cfg.Probability)
}

// caveSkip ports CaveWorldCarver.shouldSkip(dx,dy,dz, floorLevel): below the floor
// level the carve is skipped; otherwise the ellipsoid membership dx²+dy²+dz² < 1.
func caveSkip(floorLevel float64) skipChecker {
	return func(dx, dy, dz float64, _ int) bool {
		if dy <= floorLevel {
			return true
		}
		return dx*dx+dy*dy+dz*dz >= 1.0
	}
}

// carve ports CaveWorldCarver.carve. rng is the per-source-chunk legacy random.
func (w caveWorldCarver) carve(cfg *CarverConfig, cc *carveContext, rng *legacyRandom, src level.ChunkPos) bool {
	// A nether carve uses the lava-floor carveBlock rule for every ellipsoid it writes.
	cc.netherCarve = w.nether
	// j = SectionPos.sectionToBlockCoord(getRange()*2 - 1) = (7) << 4 = 112.
	j := (carverRange*2 - 1) << 4

	// tunnelCount = nextInt(nextInt(nextInt(getCaveBound())+1)+1).
	tunnelCount := int(rng.nextIntN(int32(rng.nextIntN(int32(rng.nextIntN(int32(w.bound))+1)) + 1)))

	carvedAny := false
	for k := 0; k < tunnelCount; k++ {
		x := float64(blockXIn(src, int(rng.nextIntN(16))))
		y := float64(cfg.Y.sample(rng, cc.minGenY))
		z := float64(blockZIn(src, int(rng.nextIntN(16))))
		horizMul := float64(cfg.HorizontalRadiusMultiplier.sample(rng))
		vertMul := float64(cfg.VerticalRadiusMultiplier.sample(rng))
		floorLevel := float64(cfg.FloorLevel.sample(rng))
		skip := caveSkip(floorLevel)

		branchCount := 1
		if rng.nextIntN(4) == 0 {
			yScale := float64(cfg.YScaleFloat.sample(rng))
			caveRadius := float64(1.0 + rng.nextFloat()*6.0)
			if createRoom(cfg, cc, x, y, z, caveRadius, yScale, skip) {
				carvedAny = true
			}
			branchCount += int(rng.nextIntN(4))
		}

		for branch := 0; branch < branchCount; branch++ {
			// nextFloat() * 6.2831855F and (nextFloat() - 0.5F) / 4.0F -- float products
			// (bytecode ldc float 6.2831855f, 0.5f, 4.0f). CITE: CaveWorldCarver.carve.
			yaw := rng.nextFloat() * 6.2831855
			pitch := (rng.nextFloat() - 0.5) / 4.0
			thickness := w.thicknessFn(rng)
			branchStart := j - int(rng.nextIntN(int32(j/4)))
			seed := rng.nextLong()
			if createTunnel(cfg, cc, seed, x, y, z, horizMul, vertMul,
				thickness, float32(yaw), float32(pitch), 0, branchStart, w.yScale, skip) {
				carvedAny = true
			}
		}
	}
	return carvedAny
}

// createRoom ports CaveWorldCarver.createRoom: a single large ellipsoid (a cave room)
// with a fixed shape. Returns true if anything was carved.
func createRoom(cfg *CarverConfig, cc *carveContext, x, y, z, caveRadius, yScale float64, skip skipChecker) bool {
	// hr = 1.5 + Mth.sin(1.5707963705062866)*caveRadius; vr = hr * yScale.
	// CaveWorldCarver.createRoom carves at (x + 1.0, y, z): the room center is offset on
	// the X axis, NOT y+1.0 (bytecode: dload 6 [=x], dconst_1, dadd; then dload 8 [=y];
	// then dload 10 [=z]). Mth.sin (the 65536-entry table) not math.Sin. CITE:
	// CaveWorldCarver.createRoom.
	hr := 1.5 + float64(mthSin(1.5707963705062866))*caveRadius
	vr := hr * yScale
	return cc.carveEllipsoid(cfg, x+1.0, y, z, hr, vr, skip)
}

// getThickness ports CaveWorldCarver.getThickness: f = nextFloat()*2 + nextFloat();
// if nextInt(10)==0: f *= nextFloat()*nextFloat()*3 + 1.
func getThickness(rng *legacyRandom) float32 {
	f := rng.nextFloat()*2.0 + rng.nextFloat()
	if rng.nextIntN(10) == 0 {
		f *= rng.nextFloat()*rng.nextFloat()*3.0 + 1.0
	}
	return f
}

// getNetherThickness ports NetherWorldCarver.getThickness: (nextFloat()*2 + nextFloat()) * 2. NOTE
// the nether override does NOT include the base's nextInt(10)==0 fatten branch — it draws exactly two
// nextFloat()s (RNG draw order preserved). CITE: NetherWorldCarver.getThickness.
func getNetherThickness(rng *legacyRandom) float32 {
	return (rng.nextFloat()*2.0 + rng.nextFloat()) * 2.0
}

// createTunnel ports CaveWorldCarver.createTunnel: the ellipsoid-segment random walk.
// seed reseeds a thread-local LCG (the tunnel's own RNG). x/y/z is the start; horizMul/
// vertMul scale the per-segment radius; thickness drives the radius along the path; yaw/
// pitch are the walk direction; segment/segmentCount index the walk; verticalScale
// stretches the ellipsoid. Returns true if anything was carved.
func createTunnel(
	cfg *CarverConfig, cc *carveContext, seed int64,
	x, y, z, horizMul, vertMul float64,
	thickness, yaw, pitch float32,
	segment, segmentCount int, verticalScale float64,
	skip skipChecker,
) bool {
	rng := newThreadLocalLegacy(seed)

	// branchPoint = nextInt(segmentCount/2) + segmentCount/4 — where the tunnel may fork.
	branchPoint := int(rng.nextIntN(int32(segmentCount/2))) + segmentCount/4
	steepBranch := rng.nextIntN(6) == 0

	var yawDelta, pitchDelta float32
	carvedAny := false

	for seg := segment; seg < segmentCount; seg++ {
		// radius = 1.5 + Mth.sin((double)(3.1415927F*seg/segmentCount))*thickness. Vanilla
		// uses the FLOAT pi 3.1415927F and Mth.sin (the table). CITE: CaveWorldCarver.createTunnel.
		radius := 1.5 + float64(mthSin(float64(float32(3.1415927)*float32(seg)/float32(segmentCount)))*thickness)
		vrad := radius * verticalScale

		// Walk step: cosP = Mth.cos(pitch); d0 += Mth.cos(yaw)*cosP; d1 += Mth.sin(pitch);
		// d2 += Mth.sin(yaw)*cosP. All through the Mth SIN table (not math.Cos/math.Sin).
		// CITE: CaveWorldCarver.createTunnel.
		cosP := mthCos(float64(pitch))
		x += float64(mthCos(float64(yaw))) * float64(cosP)
		y += float64(mthSin(float64(pitch)))
		z += float64(mthSin(float64(yaw))) * float64(cosP)

		if steepBranch {
			pitch *= 0.92
		} else {
			pitch *= 0.7
		}
		pitch += pitchDelta * 0.1
		yaw += yawDelta * 0.1
		pitchDelta *= 0.9
		yawDelta *= 0.75
		pitchDelta += (rng.nextFloat() - rng.nextFloat()) * rng.nextFloat() * 2.0
		yawDelta += (rng.nextFloat() - rng.nextFloat()) * rng.nextFloat() * 4.0

		if seg == branchPoint && thickness > 1.0 {
			// Fork two sub-tunnels at +/-π/2 yaw, halved thickness.
			createTunnel(cfg, cc, rng.nextLong(), x, y, z, horizMul, vertMul,
				rng.nextFloat()*0.5+0.5, yaw-1.5707964, pitch/3.0, seg, segmentCount, 1.0, skip)
			createTunnel(cfg, cc, rng.nextLong(), x, y, z, horizMul, vertMul,
				rng.nextFloat()*0.5+0.5, yaw+1.5707964, pitch/3.0, seg, segmentCount, 1.0, skip)
			return carvedAny
		}

		// 3-in-4 segments are skipped from carving (just stepped) — the gappy look.
		if rng.nextIntN(4) == 0 {
			continue
		}
		if !canReach(cc.chunk.Pos(), x, z, seg, segmentCount, thickness) {
			return carvedAny
		}
		if cc.carveEllipsoid(cfg, x, y, z, radius*horizMul, vrad*vertMul, skip) {
			carvedAny = true
		}
	}
	return carvedAny
}

// blockXIn ports ChunkPos.getBlockX(i) = (chunkX << 4) + i.
func blockXIn(pos level.ChunkPos, i int) int { return int(pos[0])*16 + i }

// blockZIn ports ChunkPos.getBlockZ(i) = (chunkZ << 4) + i.
func blockZIn(pos level.ChunkPos, i int) int { return int(pos[1])*16 + i }
