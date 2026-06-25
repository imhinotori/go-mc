// Package structure ports the Minecraft 26.2 (protocol 776) structure PLACEMENT
// pipeline DIRECTLY from the unobfuscated server jar (temp/cache/26.2-inner.jar,
// read via `javap -c`). STRUCT-01 lands the integration-risk half: the placement
// math (where a structure's start chunk lands for a given world seed), the
// StructureStart cache (a pure, singleflight-deduped memoization), the 8-radius
// REFERENCES scan (cross-chunk discovery), and the heightmap-at-STARTS column
// sampler — with ZERO blocks placed. 14-02/14-03 hang temple geometry on it.
//
// This file is the placement math. Every bit operation mirrors the bytecode:
// Math.floorDiv (NOT Go truncating '/' for negatives), the already-ported
// WorldgenRandom.SetLargeFeatureWithSalt seed, and the RandomSpreadType draw
// counts (LINEAR = one nextInt; TRIANGULAR = two-draw average). A wrong draw
// count or a Go '/' on a negative chunk coord yields a different-but-plausible
// world — so it is pinned by a golden derived from the algorithm.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.structure.placement.RandomSpreadStructurePlacement
//     (getPotentialStructureChunk, isPlacementChunk)
//   - net.minecraft.world.level.levelgen.structure.placement.RandomSpreadType
//     (LINEAR/TRIANGULAR.evaluate)
//   - net.minecraft.world.level.levelgen.structure.placement.StructurePlacement
//     (isStructureChunk, the 4 probabilityReducer static methods)
//   - net.minecraft.world.level.levelgen.structure.placement.StructurePlacement$FrequencyReductionMethod
//     (the default/legacy_type_1/legacy_type_2/legacy_type_3 -> reducer mapping)
//
// It is a faithful algorithmic port, not a copy of Mojang source.
package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// floorDiv mirrors java.lang.Math.floorDiv(int,int): integer division that floors
// toward NEGATIVE infinity (Go '/' truncates toward zero). The structure region
// index is floorDiv(chunkX, spacing); for a negative chunk coord this MUST floor
// (chunkX=-1, spacing=32 -> region -1, NOT 0), or every negative-coordinate
// structure desyncs vs a vanilla seed (Pitfall #3 / #8).
//
// Java floorDiv: r = a/b; if ((a^b)<0 && r*b != a) r--; return r.
func floorDiv(a, b int) int {
	r := a / b
	if (a^b) < 0 && r*b != a {
		r--
	}
	return r
}

// RandomSpreadType is the structure-set spread distribution (RandomSpreadType enum).
// LINEAR is ordinal 0 (the codec default + the value all four temple structure_sets
// resolve to, since they omit the spread_type field). The ordinal MATTERS only for
// the draw count, which evaluate encodes.
type RandomSpreadType int

const (
	// SpreadLinear (ordinal 0): evaluate draws nextInt(bound) ONCE. The default.
	SpreadLinear RandomSpreadType = iota
	// SpreadTriangular (ordinal 1): evaluate draws nextInt(bound) TWICE and averages
	// (integer divide by 2) — TWO draws, so the rng stream advances differently.
	SpreadTriangular
)

// evaluate ports RandomSpreadType.evaluate(RandomSource, int bound).
//
//	LINEAR     -> rng.nextInt(bound)                                  (one draw)
//	TRIANGULAR -> (rng.nextInt(bound) + rng.nextInt(bound)) / 2       (two draws, int divide)
//
// The draw COUNT is load-bearing: an extra/missing nextInt desyncs every later
// piece-placement draw vs a vanilla seed (Pitfall #3). The integer divide matches
// the bytecode's iadd; iconst_2; idiv.
func (t RandomSpreadType) evaluate(rng levelgen.RandomSource, bound int) int {
	switch t {
	case SpreadLinear:
		return int(rng.NextIntN(int32(bound)))
	case SpreadTriangular:
		a := int(rng.NextIntN(int32(bound)))
		b := int(rng.NextIntN(int32(bound)))
		return (a + b) / 2
	default:
		panic("structure: unknown RandomSpreadType")
	}
}

// FrequencyReductionMethod selects which probabilityReducer the
// applyAdditionalChunkRestrictions gate uses when a structure_set has a frequency
// below 1.0 (the four temples are frequency 1.0 -> no reduction, but the method is
// parsed + the reducers ported for jar completeness + Phase-15 mineshaft/outpost
// reuse). The enum order matches the jar's $values bootstrap.
type FrequencyReductionMethod int

// JAR GROUND TRUTH (decompiled from StructurePlacement$FrequencyReductionMethod's
// BootstrapMethods, cross-checked against the embedded structure_set JSONs):
//
//	default       -> probabilityReducer                   (setLargeFeatureWithSalt(seed,x,z,salt), nextFloat()<freq)
//	legacy_type_1 -> legacyPillagerOutpostReducer          (i=x>>4,j=z>>4; setSeed((i^(j<<4))^seed); discard nextInt; nextInt(1/freq)==0)
//	legacy_type_2 -> legacyArbitrarySaltProbabilityReducer (setLargeFeatureWithSalt(seed,z,salt,10387320), nextFloat()<freq)
//	legacy_type_3 -> legacyProbabilityReducerWithDouble    (setLargeFeatureSeed(seed,chunkX,chunkZ), nextDouble()<(double)freq)
//
// Disambiguating fixtures (on disk): pillager_outposts.json declares legacy_type_1 and IS
// the structure legacyPillagerOutpostReducer is named for; mineshafts.json declares
// legacy_type_3 and resolves to legacyProbabilityReducerWithDouble (the mineshaft path).
const (
	// FreqDefault ("default"): probabilityReducer (setLargeFeatureWithSalt + nextFloat).
	FreqDefault FrequencyReductionMethod = iota
	// FreqLegacyType1 ("legacy_type_1"): legacyPillagerOutpostReducer (pillager outpost).
	FreqLegacyType1
	// FreqLegacyType2 ("legacy_type_2"): legacyArbitrarySaltProbabilityReducer.
	FreqLegacyType2
	// FreqLegacyType3 ("legacy_type_3"): legacyProbabilityReducerWithDouble (mineshaft).
	FreqLegacyType3
)

// ParseFrequencyReductionMethod maps a structure_set "frequency_reduction_method"
// string to the enum. An absent/empty field defaults to "default" (the codec
// default), matching the jar's StructurePlacement codec.
func ParseFrequencyReductionMethod(s string) FrequencyReductionMethod {
	switch s {
	case "", "default", "minecraft:default":
		return FreqDefault
	case "legacy_type_1", "minecraft:legacy_type_1":
		return FreqLegacyType1
	case "legacy_type_2", "minecraft:legacy_type_2":
		return FreqLegacyType2
	case "legacy_type_3", "minecraft:legacy_type_3":
		return FreqLegacyType3
	default:
		return FreqDefault
	}
}

// RandomSpreadStructurePlacement is the ported random_spread placement: a structure
// set lands at most one structure per spacing*spacing region, jittered within the
// (spacing-separation) sub-square by a salt-seeded RNG. spacing/separation/salt come
// from the structure_set JSON; spreadType defaults to LINEAR.
type RandomSpreadStructurePlacement struct {
	Spacing         int
	Separation      int
	Salt            int
	SpreadType      RandomSpreadType
	Frequency       float32
	FrequencyMethod FrequencyReductionMethod
}

// getPotentialStructureChunk ports RandomSpreadStructurePlacement.getPotentialStructureChunk
// (long worldSeed, int chunkX, int chunkZ): the candidate start chunk for the region
// containing (chunkX,chunkZ).
//
//	regX = floorDiv(chunkX, spacing)                            (floor, not truncate)
//	regZ = floorDiv(chunkZ, spacing)
//	rng  = WorldgenRandom(LegacyRandomSource(0))
//	rng.setLargeFeatureWithSalt(worldSeed, regX, regZ, salt)    (salt INSIDE the seed)
//	ox   = spreadType.evaluate(rng, spacing-separation)         (1 or 2 draws)
//	oz   = spreadType.evaluate(rng, spacing-separation)
//	start = (regX*spacing + ox, regZ*spacing + oz)
//
// The seed is the ALREADY-PORTED WorldgenRandom.SetLargeFeatureWithSalt (Phase 10);
// the salt is added inside that seed, NOT XORed after. JAR-EXACT draw order.
func (p RandomSpreadStructurePlacement) getPotentialStructureChunk(worldSeed int64, chunkX, chunkZ int) (sx, sz int) {
	regX := floorDiv(chunkX, p.Spacing)
	regZ := floorDiv(chunkZ, p.Spacing)

	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureWithSalt(worldSeed, regX, regZ, p.Salt)

	bound := p.Spacing - p.Separation
	ox := p.SpreadType.evaluate(rng, bound)
	oz := p.SpreadType.evaluate(rng, bound)

	return regX*p.Spacing + ox, regZ*p.Spacing + oz
}

// PotentialStructureChunk is the exported form of getPotentialStructureChunk: the
// candidate start ChunkPos for the region containing (chunkX,chunkZ). 14-02's STARTS
// generator calls this to decide whether THIS chunk owns a structure.
func (p RandomSpreadStructurePlacement) PotentialStructureChunk(worldSeed int64, chunkX, chunkZ int) level.ChunkPos {
	sx, sz := p.getPotentialStructureChunk(worldSeed, chunkX, chunkZ)
	return level.ChunkPos{int32(sx), int32(sz)}
}

// IsStructureChunk ports StructurePlacement.isStructureChunk's placement half
// (isPlacementChunk): (chunkX,chunkZ) is a candidate start chunk iff it IS its own
// region's potential start chunk. (The full isStructureChunk also applies the
// frequency reducer + exclusion zones; the temples are frequency 1.0 with no
// exclusion zone, so the placement-chunk test is the decisive gate. The reducer is
// exposed separately via ApplyFrequencyReducer for non-1.0 sets / Phase-15 reuse.)
func (p RandomSpreadStructurePlacement) IsStructureChunk(worldSeed int64, chunkX, chunkZ int) bool {
	sx, sz := p.getPotentialStructureChunk(worldSeed, chunkX, chunkZ)
	return sx == chunkX && sz == chunkZ
}

// ApplyFrequencyReducer ports StructurePlacement.applyAdditionalChunkRestrictions:
// for a set with frequency < 1.0 it consults the selected reducer (a salt-seeded
// nextFloat/nextDouble/nextInt gate); a frequency >= 1.0 always generates. The
// temples are frequency 1.0 (always true here), but the reducers are ported for
// completeness + Phase-15 (mineshaft uses legacy_type_2, pillager outpost
// legacy_type_3).
func (p RandomSpreadStructurePlacement) ApplyFrequencyReducer(worldSeed int64, chunkX, chunkZ int) bool {
	if p.Frequency >= 1.0 {
		return true
	}
	return frequencyReducer(p.FrequencyMethod, worldSeed, p.Salt, chunkX, chunkZ, p.Frequency)
}

// frequencyReducer dispatches to the ported probabilityReducer variant for the
// selected FrequencyReductionMethod. The arg shapes mirror the jar exactly.
func frequencyReducer(m FrequencyReductionMethod, worldSeed int64, salt, chunkX, chunkZ int, frequency float32) bool {
	switch m {
	case FreqDefault:
		return probabilityReducer(worldSeed, chunkX, chunkZ, salt, frequency)
	case FreqLegacyType1:
		return legacyPillagerOutpostReducer(worldSeed, chunkX, chunkZ, salt, frequency)
	case FreqLegacyType2:
		return legacyArbitrarySaltProbabilityReducer(worldSeed, chunkX, chunkZ, salt, frequency)
	case FreqLegacyType3:
		return legacyProbabilityReducerWithDouble(worldSeed, chunkX, chunkZ, salt, frequency)
	default:
		return probabilityReducer(worldSeed, chunkX, chunkZ, salt, frequency)
	}
}

// probabilityReducer ports StructurePlacement.probabilityReducer (the "default"
// method): seed a WorldgenRandom(0) via setLargeFeatureWithSalt(worldSeed, x, z,
// salt), then generate iff nextFloat() < frequency.
func probabilityReducer(worldSeed int64, x, z, salt int, frequency float32) bool {
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureWithSalt(worldSeed, x, z, salt)
	return rng.NextFloat() < frequency
}

// legacyProbabilityReducerWithDouble ports StructurePlacement.legacyProbabilityReducerWithDouble
// ("legacy_type_3", the MINESHAFT path): seed via setLargeFeatureSeed(worldSeed, chunkX,
// chunkZ) — the jar bytecode loads iload_3=chunkX, iload_4=chunkZ (NOT (z,salt); the salt is
// 0 for the mineshaft set anyway, but the slot is the chunk coords) — then generate iff
// nextDouble() < (double)frequency. Source: javap -c StructurePlacement$FrequencyReductionMethod.
func legacyProbabilityReducerWithDouble(worldSeed int64, chunkX, chunkZ, _ int, frequency float32) bool {
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(worldSeed, chunkX, chunkZ)
	return rng.NextDouble() < float64(frequency)
}

// legacyArbitrarySaltProbabilityReducer ports StructurePlacement.legacyArbitrarySaltProbabilityReducer
// ("legacy_type_2", the mineshaft path): seed via setLargeFeatureWithSalt(worldSeed,
// z, salt, 10387320) (the hardcoded arbitrary salt 10387320; bytecode iload_3=z,
// iload_4=salt, ldc 10387320), then generate iff nextFloat() < frequency.
func legacyArbitrarySaltProbabilityReducer(worldSeed int64, _, z, salt int, frequency float32) bool {
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureWithSalt(worldSeed, z, salt, 10387320)
	return rng.NextFloat() < frequency
}

// legacyPillagerOutpostReducer ports StructurePlacement.legacyPillagerOutpostReducer
// ("legacy_type_3", the pillager outpost path): i=x>>4, j=z>>4; seed via
// setSeed((i ^ (j<<4)) ^ worldSeed); DISCARD one nextInt(); then generate iff
// nextInt((int)(1/frequency)) == 0.
func legacyPillagerOutpostReducer(worldSeed int64, x, z, _ int, frequency float32) bool {
	i := x >> 4
	j := z >> 4
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetSeed(int64(i^(j<<4)) ^ worldSeed)
	rng.NextInt() // discarded draw
	return rng.NextIntN(int32(float32(1.0)/frequency)) == 0
}
