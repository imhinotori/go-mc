package levelgen

import (
	"math"
	"testing"
)

// The golden vectors below are derived by tracing the EXACT bit operations read
// from the unobfuscated 26.2 jar bytecode (javap -c) for:
//   - Xoroshiro128PlusPlus.nextLong   (the xoroshiro128++ step)
//   - RandomSupport.upgradeSeedTo128bit / mixStafford13 (the 64->128 seed upgrade)
//   - XoroshiroRandomSource.{nextInt,nextDouble} (the bit extraction)
//   - Mth.getSeed(x,y,z) (the positional seed hash)
//   - RandomSupport.seedFromHashOf (MD5(name) big-endian split)
//   - XoroshiroRandomSource$XoroshiroPositionalRandomFactory.{at,fromHashOf}
// They are bit-exact: a Go re-trace of the same ops, locked in so any future
// drift in the port is caught immediately. See random.go for the bytecode cites.

func TestXoroshiroSeedUpgrade(t *testing.T) {
	// upgradeSeedTo128bit(0) = upgradeUnmixed(0).mixed()
	lo, hi := upgradeSeedTo128bit(0)
	wantLo := uint64(0x3564b439cd1e1f16)
	wantHi := uint64(0x63cfc62a2b097592)
	if lo != wantLo || hi != wantHi {
		t.Fatalf("upgradeSeedTo128bit(0) = (%#016x,%#016x), want (%#016x,%#016x)", lo, hi, wantLo, wantHi)
	}
}

func TestXoroshiroNextLongGolden(t *testing.T) {
	// XoroshiroRandomSource(seed=0): nextLong sequence (bit-exact golden vectors).
	src := NewXoroshiro(0)
	want := []uint64{
		0x2a2ca488f66f517e,
		0xccbc22d72e97c372,
		0x404e64b826f4b9f4,
		0x1dfbe5a84fb8f31b,
		0x1986c1cae1d8f2c5,
		0xc1b5c37dfd2e8a4f,
		0xc5b7534314657ec2,
		0xf129fd17547d4fe1,
	}
	for i, w := range want {
		got := uint64(src.NextLong())
		if got != w {
			t.Fatalf("seed=0 nextLong[%d] = %#016x, want %#016x", i, got, w)
		}
	}
}

func TestXoroshiroNextIntGolden(t *testing.T) {
	// nextInt() = (int)nextLong() — the low 32 bits as a signed int32.
	src := NewXoroshiro(0)
	want := []int32{-160476802, 781697906, 653572596, 1337520923}
	for i, w := range want {
		if got := src.NextInt(); got != w {
			t.Fatalf("seed=0 nextInt[%d] = %d, want %d", i, got, w)
		}
	}
}

func TestXoroshiroNextDoubleGolden(t *testing.T) {
	// nextDouble() = nextBits(53) * 0x1.0p-53.
	src := NewXoroshiro(0)
	want := []float64{
		0.16474369376959186,
		0.79974572900263663,
		0.25119618888762119,
		0.11712489470639631,
	}
	for i, w := range want {
		got := src.NextDouble()
		if math.Abs(got-w) > 1e-15 {
			t.Fatalf("seed=0 nextDouble[%d] = %.17g, want %.17g", i, got, w)
		}
		if got < 0 || got >= 1 {
			t.Fatalf("seed=0 nextDouble[%d] = %.17g out of [0,1)", i, got)
		}
	}
}

func TestXoroshiroSeed42(t *testing.T) {
	src := NewXoroshiro(42)
	want := []uint64{
		0xbed4a3d469c5d91f,
		0x65e301cb50e8f4ab,
		0x9752d3d4db9a2abd,
		0x43d8d3137b6e0186,
	}
	for i, w := range want {
		if got := uint64(src.NextLong()); got != w {
			t.Fatalf("seed=42 nextLong[%d] = %#016x, want %#016x", i, got, w)
		}
	}
}

func TestXoroshiroDirectCtor(t *testing.T) {
	// The (lo,hi) ctor performs NO seed upgrade — it seeds the rng state directly.
	src := NewXoroshiroFromState(1, 2)
	want := []uint64{
		0x0000000000060001,
		0x000260c000660007,
		0x180acc04718606d3,
		0x9e226d35036fc4c7,
	}
	for i, w := range want {
		if got := uint64(src.NextLong()); got != w {
			t.Fatalf("ctor(1,2) nextLong[%d] = %#016x, want %#016x", i, got, w)
		}
	}
}

func TestXoroshiroZeroStateGuard(t *testing.T) {
	// Xoroshiro128PlusPlus ctor: if (lo|hi)==0 the state is reset to
	// (GOLDEN_RATIO_64, SILVER_RATIO_64) so it never degenerates to all-zero.
	zero := NewXoroshiroFromState(0, 0)
	guard := NewXoroshiroFromState(goldenRatio64, silverRatio64)
	for i := 0; i < 4; i++ {
		if a, b := zero.NextLong(), guard.NextLong(); a != b {
			t.Fatalf("zero-state guard[%d]: %#016x != %#016x", i, uint64(a), uint64(b))
		}
	}
}

func TestMthGetSeedGolden(t *testing.T) {
	cases := []struct {
		x, y, z int
		want    int64
	}{
		{0, 0, 0, 0},
		{1, 2, 3, -33674130277896},
		{-1, -1, -1, 60311958933234},
		{100, 64, -200, 33831745463433},
	}
	for _, c := range cases {
		if got := mthGetSeed(c.x, c.y, c.z); got != c.want {
			t.Fatalf("mthGetSeed(%d,%d,%d) = %d, want %d", c.x, c.y, c.z, got, c.want)
		}
	}
}

func TestSeedFromHashOfGolden(t *testing.T) {
	cases := []struct {
		name           string
		wantLo, wantHi uint64
	}{
		{"minecraft:temperature", 0x5c7e6b29735f0d7f, 0xf7d86f1bbc734988},
		{"minecraft:vegetation", 0x81bb4d22e8dc168e, 0xf1c8b4bea16303cd},
	}
	for _, c := range cases {
		lo, hi := seedFromHashOf(c.name)
		if lo != c.wantLo || hi != c.wantHi {
			t.Fatalf("seedFromHashOf(%q) = (%#016x,%#016x), want (%#016x,%#016x)",
				c.name, lo, hi, c.wantLo, c.wantHi)
		}
	}
}

func TestRandomSupportSeedMix(t *testing.T) {
	// The positional factory's fromHashOf maps a fixed (seed,name) deterministically.
	f := NewXoroshiro(0).ForkPositional()
	a := f.FromHashOf("minecraft:temperature")
	b := NewXoroshiro(0).ForkPositional().FromHashOf("minecraft:temperature")
	for i := 0; i < 8; i++ {
		if x, y := a.NextLong(), b.NextLong(); x != y {
			t.Fatalf("fromHashOf determinism[%d]: %#016x != %#016x", i, uint64(x), uint64(y))
		}
	}
	// Distinct names diverge.
	c := NewXoroshiro(0).ForkPositional().FromHashOf("minecraft:vegetation")
	d := NewXoroshiro(0).ForkPositional().FromHashOf("minecraft:temperature")
	if c.NextLong() == d.NextLong() {
		t.Fatalf("distinct noise names produced identical first draw")
	}
}

func TestPositionalFactoryGolden(t *testing.T) {
	// forkPositional draws two nextLong from the source as the factory's (lo,hi).
	f := NewXoroshiro(0).ForkPositional()
	// at(1,2,3): new Xoroshiro(Mth.getSeed(1,2,3) ^ factory.lo, factory.hi).
	at := f.At(1, 2, 3)
	if got := uint64(at.NextLong()); got != 0xa730510baef3ada4 {
		t.Fatalf("at(1,2,3) first nextLong = %#016x, want %#016x", got, uint64(0xa730510baef3ada4))
	}
	// fromHashOf("minecraft:temperature"): Seed128bit(hash).xor(factory.lo,factory.hi).
	fh := f.FromHashOf("minecraft:temperature")
	if got := uint64(fh.NextLong()); got != 0xb12effcb5327bf6f {
		t.Fatalf("fromHashOf(temperature) first nextLong = %#016x, want %#016x", got, uint64(0xb12effcb5327bf6f))
	}
}

func TestPositionalFactoryDeterministic(t *testing.T) {
	// at(x,y,z) and fromHashOf(name) are PURE: same inputs -> identical downstream
	// state across two constructions (Pitfall 7: no global/time leak).
	f1 := NewXoroshiro(123).ForkPositional()
	f2 := NewXoroshiro(123).ForkPositional()
	for _, c := range [][3]int{{0, 0, 0}, {15, 70, -33}, {-1000, -64, 1000}} {
		a := f1.At(c[0], c[1], c[2])
		b := f2.At(c[0], c[1], c[2])
		for i := 0; i < 4; i++ {
			if x, y := a.NextLong(), b.NextLong(); x != y {
				t.Fatalf("at%v determinism[%d]: %#016x != %#016x", c, i, uint64(x), uint64(y))
			}
		}
	}
}

func TestNoGlobalRNG(t *testing.T) {
	// Two factories from the same seed drawing the same sequence yield identical
	// results — proving no global/time state leaks in.
	a := NewXoroshiro(777)
	b := NewXoroshiro(777)
	for i := 0; i < 64; i++ {
		if x, y := a.NextLong(), b.NextLong(); x != y {
			t.Fatalf("no-global-rng[%d]: %#016x != %#016x", i, uint64(x), uint64(y))
		}
	}
}
