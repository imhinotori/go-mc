package block

import "testing"

// vegetation_test.go covers the BLOCK-SURVIVAL (vegetation) predicates: IsVegetation (the
// single-cell support-needing family whose canSurvive == IsVegetationGround(below)), IsDoublePlant
// + DoublePlantLowerHalf + SameDoublePlant (the 2-tall family with half-dependent survival), and
// the deliberate EXCLUSIONS (dead bush / leaf litter / mangrove / seagrass — a different ground
// predicate, scoped to a follow-up). Each case is jar-cited in utilfuncs.go.

func TestIsVegetation(t *testing.T) {
	in := []Block{
		Dandelion{}, GoldenDandelion{}, Torchflower{}, Poppy{}, BlueOrchid{}, Allium{},
		AzureBluet{}, RedTulip{}, OrangeTulip{}, WhiteTulip{}, PinkTulip{}, OxeyeDaisy{},
		Cornflower{}, WitherRose{}, LilyOfTheValley{}, OpenEyeblossom{}, ClosedEyeblossom{},
		ShortGrass{}, Fern{}, Bush{}, FireflyBush{},
		OakSapling{}, SpruceSapling{}, BirchSapling{}, JungleSapling{}, AcaciaSapling{},
		CherrySapling{}, DarkOakSapling{}, PaleOakSapling{},
	}
	for _, b := range in {
		s := ToStateID[b]
		if !IsVegetation(s) {
			t.Fatalf("IsVegetation(%T) = false, want true (base VegetationBlock.canSurvive family)", b)
		}
	}

	// EXCLUSIONS: not single-cell SUPPORTS_VEGETATION vegetation (different ground predicate or not a plant).
	out := []Block{
		Stone{}, Dirt{}, Air{}, GrassBlock{},
		DeadBush{}, ShortDryGrass{}, TallDryGrass{}, // DryVegetationBlock -> #dry_vegetation_may_place_on
		Seagrass{}, // SeagrassBlock override
		// double plants are NOT IsVegetation (they are IsDoublePlant):
		TallGrass{Half: DoubleBlockHalfLower}, Sunflower{Half: DoubleBlockHalfUpper},
	}
	for _, b := range out {
		s := ToStateID[b]
		if IsVegetation(s) {
			t.Fatalf("IsVegetation(%T) = true, want false (excluded from the single-cell vegetation set)", b)
		}
	}
}

func TestIsDoublePlantAndHalf(t *testing.T) {
	doubles := []Block{Sunflower{}, Lilac{}, RoseBush{}, Peony{}, TallGrass{}, LargeFern{}}
	for _, b := range doubles {
		// LOWER half.
		lower := setHalf(b, DoubleBlockHalfLower)
		ls := ToStateID[lower]
		if !IsDoublePlant(ls) {
			t.Fatalf("IsDoublePlant(%T lower) = false, want true", b)
		}
		if !DoublePlantLowerHalf(ls) {
			t.Fatalf("DoublePlantLowerHalf(%T lower) = false, want true", b)
		}
		// UPPER half.
		upper := setHalf(b, DoubleBlockHalfUpper)
		us := ToStateID[upper]
		if !IsDoublePlant(us) {
			t.Fatalf("IsDoublePlant(%T upper) = false, want true", b)
		}
		if DoublePlantLowerHalf(us) {
			t.Fatalf("DoublePlantLowerHalf(%T upper) = true, want false", b)
		}
	}

	// A non-double-plant is neither.
	if IsDoublePlant(ToStateID[Poppy{}]) {
		t.Fatalf("IsDoublePlant(Poppy) = true, want false")
	}
	if DoublePlantLowerHalf(ToStateID[Dirt{}]) {
		t.Fatalf("DoublePlantLowerHalf(Dirt) = true, want false")
	}
}

func TestSameDoublePlant(t *testing.T) {
	tgL := ToStateID[TallGrass{Half: DoubleBlockHalfLower}]
	tgU := ToStateID[TallGrass{Half: DoubleBlockHalfUpper}]
	lfL := ToStateID[LargeFern{Half: DoubleBlockHalfLower}]

	if !SameDoublePlant(tgU, tgL) {
		t.Fatalf("SameDoublePlant(tall_grass upper, tall_grass lower) = false, want true (same block, any half)")
	}
	if SameDoublePlant(tgU, lfL) {
		t.Fatalf("SameDoublePlant(tall_grass upper, large_fern lower) = true, want false (different blocks)")
	}
	if SameDoublePlant(ToStateID[Poppy{}], tgL) {
		t.Fatalf("SameDoublePlant(poppy, tall_grass lower) = true, want false (poppy is not a double plant)")
	}
}

// setHalf returns a copy of a double-plant block value with its Half field set. Mirrors the
// per-type Half field the generated double-plant structs carry.
func setHalf(b Block, half DoubleBlockHalf) Block {
	switch b.(type) {
	case Sunflower:
		return Sunflower{Half: half}
	case Lilac:
		return Lilac{Half: half}
	case RoseBush:
		return RoseBush{Half: half}
	case Peony:
		return Peony{Half: half}
	case TallGrass:
		return TallGrass{Half: half}
	case LargeFern:
		return LargeFern{Half: half}
	default:
		return b
	}
}
