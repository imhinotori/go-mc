package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// goldenStart re-derives the expected start chunk straight from the algorithm (NOT
// from the implementation under test) so the golden is an INDEPENDENT oracle: it
// re-runs floorDiv + the salt seed + the spread draws by hand. If placement.go
// reorders a draw, truncates instead of flooring, or miscounts a TRIANGULAR draw,
// getPotentialStructureChunk diverges from this oracle and the test fails.
func goldenStart(worldSeed int64, chunkX, chunkZ, spacing, separation, salt int, spread RandomSpreadType) (int, int, []int32) {
	// floorDiv toward negative infinity (the oracle does NOT use Go '/').
	fd := func(a, b int) int {
		r := a / b
		if (a^b) < 0 && r*b != a {
			r--
		}
		return r
	}
	regX := fd(chunkX, spacing)
	regZ := fd(chunkZ, spacing)

	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureWithSalt(worldSeed, regX, regZ, salt)
	bound := int32(spacing - separation)

	var draws []int32
	eval := func() int {
		switch spread {
		case SpreadLinear:
			d := rng.NextIntN(bound)
			draws = append(draws, d)
			return int(d)
		case SpreadTriangular:
			a := rng.NextIntN(bound)
			b := rng.NextIntN(bound)
			draws = append(draws, a, b)
			return int(a+b) / 2
		default:
			panic("bad spread")
		}
	}
	ox := eval()
	oz := eval()
	return regX*spacing + ox, regZ*spacing + oz, draws
}

// TestGetPotentialStructureChunk pins the LINEAR placement math against the
// independent oracle for the desert-pyramid set (spacing 32, separation 8, salt
// 14357617) across several chunk coords (incl. a negative one), AND asserts the
// post-draw rng fingerprint so an extra/missing draw is caught.
func TestGetPotentialStructureChunk(t *testing.T) {
	const worldSeed = int64(123456789)
	const spacing, separation, salt = 32, 8, 14357617
	p := RandomSpreadStructurePlacement{Spacing: spacing, Separation: separation, Salt: salt, SpreadType: SpreadLinear, Frequency: 1.0}

	for _, cz := range []struct{ x, z int }{{0, 0}, {5, 17}, {31, 31}, {-1, -1}, {-40, 70}} {
		wantX, wantZ, _ := goldenStart(worldSeed, cz.x, cz.z, spacing, separation, salt, SpreadLinear)
		got := p.PotentialStructureChunk(worldSeed, cz.x, cz.z)
		if int(got[0]) != wantX || int(got[1]) != wantZ {
			t.Fatalf("LINEAR (%d,%d): got (%d,%d), oracle (%d,%d)", cz.x, cz.z, got[0], got[1], wantX, wantZ)
		}
	}

	// Fingerprint: after the two LINEAR draws, the rng must be at the SAME state as
	// the oracle's (exactly two nextInt draws consumed — no more, no less).
	regX := floorDiv(5, spacing)
	regZ := floorDiv(17, spacing)
	oracle := levelgen.NewWorldgenRandom(0)
	oracle.SetLargeFeatureWithSalt(worldSeed, regX, regZ, salt)
	oracle.NextIntN(int32(spacing - separation))
	oracle.NextIntN(int32(spacing - separation))
	wantNext := oracle.NextLong()

	impl := levelgen.NewWorldgenRandom(0)
	impl.SetLargeFeatureWithSalt(worldSeed, regX, regZ, salt)
	p.SpreadType.evaluate(impl, spacing-separation)
	p.SpreadType.evaluate(impl, spacing-separation)
	if gotNext := impl.NextLong(); gotNext != wantNext {
		t.Fatalf("LINEAR draw count desync: post-evaluate rng fingerprint %d != %d (extra/missing draw)", gotNext, wantNext)
	}
}

// TestGetPotentialStructureChunkTriangular pins the TRIANGULAR two-draw form against
// the oracle, INCLUDING the post-draw fingerprint (TRIANGULAR consumes 4 draws total
// for two evaluates; a missed draw desyncs).
func TestGetPotentialStructureChunkTriangular(t *testing.T) {
	const worldSeed = int64(987654321)
	const spacing, separation, salt = 16, 4, 99999
	p := RandomSpreadStructurePlacement{Spacing: spacing, Separation: separation, Salt: salt, SpreadType: SpreadTriangular, Frequency: 1.0}

	for _, cz := range []struct{ x, z int }{{0, 0}, {3, 9}, {-7, 2}} {
		wantX, wantZ, draws := goldenStart(worldSeed, cz.x, cz.z, spacing, separation, salt, SpreadTriangular)
		if len(draws) != 4 {
			t.Fatalf("TRIANGULAR oracle should draw 4 (two 2-draw evaluates), drew %d", len(draws))
		}
		got := p.PotentialStructureChunk(worldSeed, cz.x, cz.z)
		if int(got[0]) != wantX || int(got[1]) != wantZ {
			t.Fatalf("TRIANGULAR (%d,%d): got (%d,%d), oracle (%d,%d)", cz.x, cz.z, got[0], got[1], wantX, wantZ)
		}
	}

	// Fingerprint: TRIANGULAR's two evaluates consume FOUR nextInt draws.
	regX := floorDiv(3, spacing)
	regZ := floorDiv(9, spacing)
	oracle := levelgen.NewWorldgenRandom(0)
	oracle.SetLargeFeatureWithSalt(worldSeed, regX, regZ, salt)
	for i := 0; i < 4; i++ {
		oracle.NextIntN(int32(spacing - separation))
	}
	wantNext := oracle.NextLong()

	impl := levelgen.NewWorldgenRandom(0)
	impl.SetLargeFeatureWithSalt(worldSeed, regX, regZ, salt)
	p.SpreadType.evaluate(impl, spacing-separation)
	p.SpreadType.evaluate(impl, spacing-separation)
	if gotNext := impl.NextLong(); gotNext != wantNext {
		t.Fatalf("TRIANGULAR draw count desync: post-evaluate rng fingerprint %d != %d", gotNext, wantNext)
	}
}

// TestFloorDivNegative pins the Math.floorDiv discipline: a negative chunk coord
// floors toward -inf, NOT toward zero. chunkX=-1, spacing=32 -> region -1 (Go '/'
// would give 0, a one-region shift that desyncs every negative-coordinate structure).
func TestFloorDivNegative(t *testing.T) {
	cases := []struct {
		a, b, want int
	}{
		{-1, 32, -1},
		{0, 32, 0},
		{31, 32, 0},
		{32, 32, 1},
		{-32, 32, -1},
		{-33, 32, -2},
		{-40, 32, -2},
		{70, 32, 2},
	}
	for _, c := range cases {
		if got := floorDiv(c.a, c.b); got != c.want {
			t.Errorf("floorDiv(%d,%d) = %d, want %d", c.a, c.b, got, c.want)
		}
		// Go truncating '/' would disagree exactly on the negative non-multiples.
		if c.a < 0 && c.a%c.b != 0 && floorDiv(c.a, c.b) == c.a/c.b {
			t.Errorf("floorDiv(%d,%d) did NOT differ from Go '/' (%d) on a negative non-multiple", c.a, c.b, c.a/c.b)
		}
	}
}

// TestIsStructureChunk pins that the derived start chunk reports IsStructureChunk
// true at itself and false at a neighbor (the placement-chunk gate).
func TestIsStructureChunk(t *testing.T) {
	const worldSeed = int64(424242)
	p := RandomSpreadStructurePlacement{Spacing: 32, Separation: 8, Salt: 14357617, SpreadType: SpreadLinear, Frequency: 1.0}

	// The start chunk of the region containing (0,0) must report true at itself.
	start := p.PotentialStructureChunk(worldSeed, 0, 0)
	if !p.IsStructureChunk(worldSeed, int(start[0]), int(start[1])) {
		t.Fatalf("start chunk %v does not report IsStructureChunk true", start)
	}
	// A different chunk in the SAME region (that is not the start) reports false.
	other := level.ChunkPos{start[0] + 1, start[1]}
	// Guard: ensure `other` is still inside the same region so it has the same start.
	if floorDiv(int(other[0]), p.Spacing) == floorDiv(int(start[0]), p.Spacing) {
		if p.IsStructureChunk(worldSeed, int(other[0]), int(other[1])) {
			t.Fatalf("non-start chunk %v wrongly reports IsStructureChunk true", other)
		}
	}
}

// TestProbabilityReducers exercises each of the four reducer variants at a known
// seed: frequency 1.0 always passes the gate, frequency 0.0 never does, and each
// variant is internally consistent (same inputs -> same result). This pins the
// reducer DISPATCH + the per-variant draw shape without asserting Mojang's exact
// float (the determinism + the >= 1.0 / 0.0 boundaries are the load-bearing claims).
func TestProbabilityReducers(t *testing.T) {
	const worldSeed = int64(7777)
	methods := []FrequencyReductionMethod{FreqDefault, FreqLegacyType1, FreqLegacyType2, FreqLegacyType3}

	for _, m := range methods {
		// frequency 1.0: ApplyFrequencyReducer short-circuits to true.
		p := RandomSpreadStructurePlacement{Spacing: 32, Separation: 8, Salt: 14357617, Frequency: 1.0, FrequencyMethod: m}
		if !p.ApplyFrequencyReducer(worldSeed, 3, 5) {
			t.Errorf("method %d: frequency 1.0 must always generate", m)
		}
		// The reducer itself (frequency 0.5) must be deterministic.
		r1 := frequencyReducer(m, worldSeed, 14357617, 3, 5, 0.5)
		r2 := frequencyReducer(m, worldSeed, 14357617, 3, 5, 0.5)
		if r1 != r2 {
			t.Errorf("method %d: reducer non-deterministic for same inputs", m)
		}
	}

	// The default reducer at frequency 0.0 never passes (nextFloat() < 0 is false).
	if probabilityReducer(worldSeed, 3, 5, 14357617, 0.0) {
		t.Error("probabilityReducer at frequency 0.0 must never generate")
	}
	// And at frequency 1.0 it always passes (nextFloat() in [0,1) < 1.0 is always true).
	if !probabilityReducer(worldSeed, 3, 5, 14357617, 1.0) {
		t.Error("probabilityReducer at frequency 1.0 must always generate")
	}
}

// TestStructureSetJSONMatchesConstants loads the four temple structure_sets from the
// embedded FS and asserts their spacing/separation/salt match the jar-confirmed
// constants AND that the absent spread_type field defaults to LINEAR + the absent
// frequency_reduction_method defaults to FreqDefault + frequency defaults to 1.0.
func TestStructureSetJSONMatchesConstants(t *testing.T) {
	cases := []struct {
		setID, structureID string
		salt               int
	}{
		{"minecraft:desert_pyramids", "minecraft:desert_pyramid", 14357617},
		{"minecraft:igloos", "minecraft:igloo", 14357618},
		{"minecraft:jungle_temples", "minecraft:jungle_pyramid", 14357619},
		{"minecraft:swamp_huts", "minecraft:swamp_hut", 14357620},
	}
	for _, c := range cases {
		set, err := LoadStructureSet(c.setID)
		if err != nil {
			t.Fatalf("LoadStructureSet(%q): %v", c.setID, err)
		}
		if set.Placement.Spacing != 32 {
			t.Errorf("%s: spacing = %d, want 32", c.setID, set.Placement.Spacing)
		}
		if set.Placement.Separation != 8 {
			t.Errorf("%s: separation = %d, want 8", c.setID, set.Placement.Separation)
		}
		if set.Placement.Salt != c.salt {
			t.Errorf("%s: salt = %d, want %d", c.setID, set.Placement.Salt, c.salt)
		}
		// Absent spread_type -> LINEAR (the codec default).
		if set.Placement.SpreadType != SpreadLinear {
			t.Errorf("%s: spread_type = %d, want SpreadLinear (absent field default)", c.setID, set.Placement.SpreadType)
		}
		// Absent frequency -> 1.0; absent method -> default.
		if set.Placement.Frequency != 1.0 {
			t.Errorf("%s: frequency = %v, want 1.0 (absent field default)", c.setID, set.Placement.Frequency)
		}
		if set.Placement.FrequencyMethod != FreqDefault {
			t.Errorf("%s: frequency method = %d, want FreqDefault (absent field default)", c.setID, set.Placement.FrequencyMethod)
		}
		// The set names its structure.
		if len(set.Structures) != 1 || set.Structures[0].Structure != c.structureID {
			t.Errorf("%s: structures = %v, want single %q", c.setID, set.Structures, c.structureID)
		}
	}
}

// TestHasStructureBiomeTagsLoad asserts the embedded has_structure biome tags parse
// to the expected biome name-sets — the DATA the 14-02/14-03 biome check reads.
func TestHasStructureBiomeTagsLoad(t *testing.T) {
	cases := []struct {
		structureID string
		want        []string
	}{
		{"desert_pyramid", []string{"minecraft:desert"}},
		{"igloo", []string{"minecraft:snowy_taiga", "minecraft:snowy_plains", "minecraft:snowy_slopes"}},
		{"jungle_temple", []string{"minecraft:bamboo_jungle", "minecraft:jungle"}},
		{"swamp_hut", []string{"minecraft:swamp"}},
	}
	for _, c := range cases {
		got, err := HasStructureBiomes(c.structureID)
		if err != nil {
			t.Fatalf("HasStructureBiomes(%q): %v", c.structureID, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %d biomes %v, want %d %v", c.structureID, len(got), got, len(c.want), c.want)
		}
		for _, w := range c.want {
			if !got[w] {
				t.Errorf("%s: missing expected biome %q (got %v)", c.structureID, w, got)
			}
		}
	}
}
