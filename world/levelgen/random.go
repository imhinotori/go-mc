// Package levelgen ports the Minecraft 26.2 (protocol 776) world-generation
// seeding chain and noise primitives DIRECTLY from the unobfuscated server jar
// (temp/cache/26.2-inner.jar, read via `javap -c`). This file is the Tier-A
// foundation: the xoroshiro128++ random source, the RandomSupport seed mixing
// that maps a world seed + a noise registry name to that noise's seed, and the
// positional random factory the density-function graph seeds every noise from.
//
// It is the determinism + parity HINGE of PARITY-01: a wrong seed-mix yields a
// different-but-plausible world. Every bit operation here mirrors the bytecode
// exactly (Java `>>>` -> Go uint64 shift; Java `>>` -> Go signed shift; Java
// `Long.rotateLeft` -> math/bits.RotateLeft64). It is a faithful algorithmic
// port, not a copy of Mojang source.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.Xoroshiro128PlusPlus  (the rng core)
//   - net.minecraft.world.level.levelgen.XoroshiroRandomSource (nextLong/Int/Double/Bits, ctors, forkPositional)
//   - net.minecraft.world.level.levelgen.RandomSupport         (mixStafford13, upgradeSeedTo128bit, seedFromHashOf)
//   - net.minecraft.world.level.levelgen.XoroshiroRandomSource$XoroshiroPositionalRandomFactory (at/fromHashOf/fromSeed)
//   - net.minecraft.util.Mth.getSeed(int,int,int)              (the positional seed hash)
package levelgen

import (
	"crypto/md5"
	"math/bits"
)

// Mixing constants, read from the bytecode (RandomSupport / Xoroshiro128PlusPlus).
const (
	// GOLDEN_RATIO_64 = 0x9E3779B97F4A7C15 (Java long -7046029254386353131).
	goldenRatio64 = uint64(0x9E3779B97F4A7C15)
	// SILVER_RATIO_64 = 0x6A09E667F3BCC909 (Java long 7640891576956012809).
	silverRatio64 = uint64(0x6A09E667F3BCC909)

	// mixStafford13 multipliers (RandomSupport.mixStafford13).
	staffordMul1 = uint64(0xBF58476D1CE4E5B9) // Java long -4658895280553007687
	staffordMul2 = uint64(0x94D049BB133111EB) // Java long -7723592293110705685

	// nextDouble scale = 0x1.0p-53 (XoroshiroRandomSource.DOUBLE_UNIT).
	doubleUnit = 1.1102230246251565e-16
	// nextFloat scale = 0x1.0p-24 (XoroshiroRandomSource.FLOAT_UNIT).
	floatUnit = float32(5.9604645e-8)
)

// RandomSource is the subset of net.minecraft.util.RandomSource the noise stack
// consumes. All draws flow from the world seed through the ported xoroshiro128++
// state — no math/rand, no global mutable state (Pitfall 7).
type RandomSource interface {
	// NextLong returns the next uint64 (Java long) from the rng.
	NextLong() int64
	// NextInt returns the low 32 bits of NextLong as a signed int (Java (int)nextLong()).
	NextInt() int32
	// NextIntN returns a uniform value in [0,bound) (Java nextInt(int) Lemire reduction).
	NextIntN(bound int32) int32
	// NextDouble returns a value in [0,1) (Java nextBits(53)*0x1.0p-53).
	NextDouble() float64
	// NextFloat returns a value in [0,1) (Java nextBits(24)*0x1.0p-24).
	NextFloat() float32
	// ConsumeCount advances the rng by n draws (Java consumeCount(int)).
	ConsumeCount(n int)
	// Fork returns a new independent RandomSource seeded from two fresh draws.
	Fork() RandomSource
	// ForkPositional returns a positional factory seeded from two fresh draws.
	ForkPositional() PositionalRandomFactory
}

// PositionalRandomFactory mirrors net.minecraft.world.level.levelgen.PositionalRandomFactory:
// a deterministic source of per-position / per-name RandomSources. The density
// graph seeds every NormalNoise through FromHashOf(noiseName).
type PositionalRandomFactory interface {
	// At seeds a RandomSource from the block position (x,y,z).
	At(x, y, z int) RandomSource
	// FromHashOf seeds a RandomSource from a registry name (e.g. "minecraft:temperature").
	FromHashOf(name string) RandomSource
	// FromSeed seeds a RandomSource from a raw long.
	FromSeed(seed int64) RandomSource
}

// ---- RandomSupport: seed mixing (RandomSupport.class) ----

// mixStafford13 is the Stafford variant-13 finalizer.
// Java: x ^= x >>> 30; x *= -4658895280553007687; x ^= x >>> 27; x *= -7723592293110705685; x ^= x >>> 31
// (`>>>` is the unsigned right shift -> Go uint64 shift.)
func mixStafford13(x uint64) uint64 {
	x = (x ^ (x >> 30)) * staffordMul1
	x = (x ^ (x >> 27)) * staffordMul2
	return x ^ (x >> 31)
}

// upgradeSeedTo128bit maps a 64-bit world seed to the 128-bit xoroshiro state.
// Java RandomSupport.upgradeSeedTo128bitUnmixed: lo = seed ^ SILVER_RATIO_64; hi = lo + GOLDEN_RATIO_64.
// Then .mixed() applies mixStafford13 to each half.
func upgradeSeedTo128bit(seed int64) (lo, hi uint64) {
	lo = uint64(seed) ^ silverRatio64
	hi = lo + goldenRatio64
	return mixStafford13(lo), mixStafford13(hi)
}

// seedFromHashOf maps a noise registry name to a 128-bit seed via MD5.
// Java RandomSupport.seedFromHashOf: bytes = md5(name, UTF_8); lo = Longs.fromBytes(b0..b7);
// hi = Longs.fromBytes(b8..b15). Longs.fromBytes is big-endian (b0 is the MSB).
func seedFromHashOf(name string) (lo, hi uint64) {
	h := md5.Sum([]byte(name))
	for i := 0; i < 8; i++ {
		lo = lo<<8 | uint64(h[i])
		hi = hi<<8 | uint64(h[8+i])
	}
	return lo, hi
}

// ---- Mth.getSeed: positional seed hash (Mth.class) ----

// mthGetSeed mirrors net.minecraft.util.Mth.getSeed(int,int,int).
// Java:
//
//	long l = (long)(x * 3129871) ^ (long)z * 116129781L ^ (long)y;
//	l = l * l * 42317861L + l * 11L;
//	return l >> 16;
//
// NOTE: x*3129871 is INT multiplication (wraps in 32 bits) before widening; the
// final `>> 16` is a SIGNED shift.
func mthGetSeed(x, y, z int) int64 {
	l := int64(int32(int32(x)*3129871)) ^ (int64(int32(z)) * 116129781) ^ int64(int32(y))
	l = l*l*42317861 + l*11
	return l >> 16
}

// ---- Xoroshiro128PlusPlus: the rng core (Xoroshiro128PlusPlus.class) ----

// xoroshiro128pp holds the two-word state of xoroshiro128++.
type xoroshiro128pp struct {
	seedLo uint64
	seedHi uint64
}

// newXoroshiro128pp applies the all-zero state guard: Java sets the state to
// (GOLDEN_RATIO_64, SILVER_RATIO_64) when (lo|hi)==0 so it never degenerates.
func newXoroshiro128pp(lo, hi uint64) xoroshiro128pp {
	if lo|hi == 0 {
		lo = goldenRatio64
		hi = silverRatio64
	}
	return xoroshiro128pp{lo, hi}
}

// nextLong is the xoroshiro128++ step (Xoroshiro128PlusPlus.nextLong).
// Java:
//
//	long lo = seedLo, hi = seedHi;
//	long result = Long.rotateLeft(lo + hi, 17) + lo;
//	hi ^= lo;
//	seedLo = Long.rotateLeft(lo, 49) ^ hi ^ (hi << 21);
//	seedHi = Long.rotateLeft(hi, 28);
//	return result;
func (x *xoroshiro128pp) nextLong() uint64 {
	lo := x.seedLo
	hi := x.seedHi
	result := bits.RotateLeft64(lo+hi, 17) + lo
	hi ^= lo
	x.seedLo = bits.RotateLeft64(lo, 49) ^ hi ^ (hi << 21)
	x.seedHi = bits.RotateLeft64(hi, 28)
	return result
}

// ---- XoroshiroRandomSource (XoroshiroRandomSource.class) ----

// Xoroshiro is the ported XoroshiroRandomSource. The Gaussian source is omitted
// (the noise stack does not draw Gaussians); nextGaussian can be added if a
// later tier needs it.
type Xoroshiro struct {
	rng xoroshiro128pp
}

// NewXoroshiro mirrors XoroshiroRandomSource(long): the seed is upgraded to 128
// bits via RandomSupport.upgradeSeedTo128bit before seeding the rng.
func NewXoroshiro(seed int64) *Xoroshiro {
	lo, hi := upgradeSeedTo128bit(seed)
	return &Xoroshiro{rng: newXoroshiro128pp(lo, hi)}
}

// NewXoroshiroFromState mirrors XoroshiroRandomSource(long,long): NO seed upgrade
// — the two words seed the rng state directly (modulo the zero-state guard).
func NewXoroshiroFromState(lo, hi uint64) *Xoroshiro {
	return &Xoroshiro{rng: newXoroshiro128pp(lo, hi)}
}

// NextLong returns the next draw.
func (x *Xoroshiro) NextLong() int64 { return int64(x.rng.nextLong()) }

// nextBits returns the top `bits` bits of nextLong: Java nextLong() >>> (64 - bits).
func (x *Xoroshiro) nextBits(b int) uint64 { return x.rng.nextLong() >> (64 - b) }

// NextInt returns (int)nextLong() — the low 32 bits as a signed int32.
func (x *Xoroshiro) NextInt() int32 { return int32(x.rng.nextLong()) }

// NextIntN mirrors XoroshiroRandomSource.nextInt(int): a Lemire reduction with
// rejection so the result is uniform in [0,bound).
func (x *Xoroshiro) NextIntN(bound int32) int32 {
	if bound <= 0 {
		panic("levelgen: NextIntN bound must be positive")
	}
	// m = (nextInt as unsigned 32) * bound  (64-bit product)
	m := uint64(uint32(x.NextInt())) * uint64(uint32(bound))
	low := m & 0xFFFFFFFF
	if low < uint64(uint32(bound)) {
		// threshold = (-bound) mod bound, computed unsigned over 32 bits.
		threshold := uint64(uint32(-bound) % uint32(bound))
		for low < threshold {
			m = uint64(uint32(x.NextInt())) * uint64(uint32(bound))
			low = m & 0xFFFFFFFF
		}
	}
	return int32(m >> 32)
}

// NextDouble returns nextBits(53) * 0x1.0p-53 in [0,1).
func (x *Xoroshiro) NextDouble() float64 { return float64(x.nextBits(53)) * doubleUnit }

// NextFloat returns nextBits(24) * 0x1.0p-24 in [0,1).
func (x *Xoroshiro) NextFloat() float32 { return float32(x.nextBits(24)) * floatUnit }

// ConsumeCount advances the rng by n draws.
func (x *Xoroshiro) ConsumeCount(n int) {
	for i := 0; i < n; i++ {
		x.rng.nextLong()
	}
}

// Fork mirrors XoroshiroRandomSource.fork(): a new source seeded from two fresh draws.
func (x *Xoroshiro) Fork() RandomSource {
	lo := x.rng.nextLong()
	hi := x.rng.nextLong()
	return NewXoroshiroFromState(lo, hi)
}

// ForkPositional mirrors XoroshiroRandomSource.forkPositional(): a factory seeded
// from two fresh draws.
func (x *Xoroshiro) ForkPositional() PositionalRandomFactory {
	lo := x.rng.nextLong()
	hi := x.rng.nextLong()
	return &xoroshiroPositionalFactory{seedLo: lo, seedHi: hi}
}

var _ RandomSource = (*Xoroshiro)(nil)

// ---- XoroshiroPositionalRandomFactory (inner class) ----

type xoroshiroPositionalFactory struct {
	seedLo uint64
	seedHi uint64
}

// At mirrors at(int,int,int): new XoroshiroRandomSource(Mth.getSeed(x,y,z) ^ seedLo, seedHi).
func (f *xoroshiroPositionalFactory) At(x, y, z int) RandomSource {
	posSeed := uint64(mthGetSeed(x, y, z))
	return NewXoroshiroFromState(posSeed^f.seedLo, f.seedHi)
}

// FromHashOf mirrors fromHashOf(String): Seed128bit(md5(name)).xor(seedLo,seedHi),
// fed to the (Seed128bit) ctor (which does NOT upgrade — it seeds directly).
func (f *xoroshiroPositionalFactory) FromHashOf(name string) RandomSource {
	lo, hi := seedFromHashOf(name)
	return NewXoroshiroFromState(lo^f.seedLo, hi^f.seedHi)
}

// FromSeed mirrors fromSeed(long): new XoroshiroRandomSource(seed ^ seedLo, seed ^ seedHi).
func (f *xoroshiroPositionalFactory) FromSeed(seed int64) RandomSource {
	return NewXoroshiroFromState(uint64(seed)^f.seedLo, uint64(seed)^f.seedHi)
}

var _ PositionalRandomFactory = (*xoroshiroPositionalFactory)(nil)
