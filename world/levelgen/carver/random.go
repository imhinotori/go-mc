package carver

// legacyRandom ports net.minecraft.world.level.levelgen.LegacyRandomSource (the
// java.util.Random LCG) and the BitRandomSource default methods. The carver pass
// uses this LCG (NOT the xoroshiro source the noise stack uses): applyCarvers seeds
// a WorldgenRandom (a LegacyRandomSource) per source chunk via setLargeFeatureSeed,
// and the cave/canyon tunnel walks reseed a SingleThreadedRandomSource (the same
// LCG, minus the atomic guard) via createThreadLocalInstance(nextLong()).
//
// Every operation mirrors the bytecode constant-for-constant. It is an algorithmic
// port, NOT a copy of Mojang source.
//
// Sources (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.LegacyRandomSource  (setSeed/next)
//   - net.minecraft.world.level.levelgen.BitRandomSource     (nextInt/nextInt(bound)/nextLong/nextFloat)
//   - net.minecraft.world.level.levelgen.WorldgenRandom.setLargeFeatureSeed
//   - net.minecraft.util.RandomSource.createThreadLocalInstance(long)
type legacyRandom struct {
	seed uint64 // the 48-bit LCG state
}

// LCG constants (LegacyRandomSource ConstantValue attributes).
const (
	lcgMultiplier = uint64(0x5DEECE66D)             // 25214903917
	lcgIncrement  = uint64(0xB)                     // 11
	lcgMask       = uint64((1 << 48) - 1)           // 281474976710655
)

// newLegacyRandom mirrors LegacyRandomSource(long) -> setSeed.
func newLegacyRandom(seed int64) *legacyRandom {
	r := &legacyRandom{}
	r.setSeed(seed)
	return r
}

// setSeed ports LegacyRandomSource.setSeed: seed = (s ^ MULTIPLIER) & MASK.
func (r *legacyRandom) setSeed(s int64) {
	r.seed = (uint64(s) ^ lcgMultiplier) & lcgMask
}

// next ports BitRandomSource.next(bits) via the LCG step: seed = (seed*MUL + INC) &
// MASK; return (int)(seed >>> (48 - bits)). The shift is unsigned (the state is the
// low 48 bits) and the result is taken as a signed 32-bit int.
func (r *legacyRandom) next(bits int) int32 {
	r.seed = (r.seed*lcgMultiplier + lcgIncrement) & lcgMask
	return int32(r.seed >> (48 - bits))
}

// nextInt ports BitRandomSource.nextInt() = next(32).
func (r *legacyRandom) nextInt() int32 { return r.next(32) }

// nextIntN ports BitRandomSource.nextInt(bound) (java.util.Random.nextInt):
// power-of-two fast path, otherwise the rejection loop.
func (r *legacyRandom) nextIntN(bound int32) int32 {
	if bound <= 0 {
		panic("carver: nextIntN bound must be positive")
	}
	// Power-of-two fast path: (bound * (long)next(31)) >> 31.
	if bound&(bound-1) == 0 {
		return int32((int64(bound) * int64(r.next(31))) >> 31)
	}
	for {
		bits := r.next(31)
		val := bits % bound
		if bits-val+(bound-1) >= 0 {
			return val
		}
	}
}

// nextLong ports BitRandomSource.nextLong = ((long)next(32) << 32) + next(32).
func (r *legacyRandom) nextLong() int64 {
	hi := int64(r.next(32))
	lo := int64(r.next(32))
	return (hi << 32) + lo
}

// nextFloat ports BitRandomSource.nextFloat = next(24) * 0x1.0p-24.
func (r *legacyRandom) nextFloat() float32 {
	return float32(r.next(24)) * 5.9604645e-8
}

// setLargeFeatureSeed ports WorldgenRandom.setLargeFeatureSeed(seed, chunkX, chunkZ):
//
//	setSeed(seed);
//	long a = nextLong(); long b = nextLong();
//	long mixed = ((long)chunkX * a) ^ ((long)chunkZ * b) ^ seed;
//	setSeed(mixed);
//
// This is the per-source-chunk carver seed: deterministic over (worldSeed+carverIdx,
// sourceChunkX, sourceChunkZ).
func (r *legacyRandom) setLargeFeatureSeed(seed int64, chunkX, chunkZ int) {
	r.setSeed(seed)
	a := r.nextLong()
	b := r.nextLong()
	mixed := (int64(chunkX) * a) ^ (int64(chunkZ) * b) ^ seed
	r.setSeed(mixed)
}

// newThreadLocalLegacy ports RandomSource.createThreadLocalInstance(long): a fresh
// LCG seeded directly from the long (the tunnel-walk reseed). The
// SingleThreadedRandomSource shares the BitRandomSource LCG with LegacyRandomSource,
// so it is the same algorithm here.
func newThreadLocalLegacy(seed int64) *legacyRandom {
	return newLegacyRandom(seed)
}
