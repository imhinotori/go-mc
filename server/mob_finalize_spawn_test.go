package server

// mob_finalize_spawn_test.go -- deterministic pins for the TropicalFish / Salmon / Strider / Frog
// finalizeSpawn variant rolls (1:1 with the 26.2 jar). Rolls driven off a seeded RandomSource; expected
// values from a reference LegacyRandomSource in vanilla draw order (bit-exact vs java.util.Random).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/levelgen"
)

func finalizeLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

func TestTropicalFishCommonVariant(t *testing.T) {
	loop, _, floorY := finalizeLoop(t)
	const seed = 0x7A1E
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
	ref := levelgen.NewLegacyRandomSource(seed)
	if ref.NextFloat() >= 0.9 {
		t.Skip("seed not in common branch")
	}
	want := tropicalCommonVariants[ref.NextIntN(22)]
	f := loop.spawnFish(entity.TropicalFish, 8.5, float64(floorY+1), 8.5)
	if f.tropicalVariant != want {
		t.Fatalf("tropical common variant = %d, want %d", f.tropicalVariant, want)
	}
}

func TestTropicalFishRareVariant(t *testing.T) {
	loop, _, floorY := finalizeLoop(t)
	var seed int64 = 1
	for ; seed < 100000; seed++ {
		if levelgen.NewLegacyRandomSource(seed).NextFloat() >= 0.9 {
			break
		}
	}
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
	ref := levelgen.NewLegacyRandomSource(seed)
	_ = ref.NextFloat()
	patternPacked := tropicalPatternPackedIds[ref.NextIntN(12)]
	baseID := int(ref.NextIntN(16))
	patID := int(ref.NextIntN(16))
	want := tropicalPackVariant(patternPacked, baseID, patID)
	f := loop.spawnFish(entity.TropicalFish, 8.5, float64(floorY+1), 8.5)
	if f.tropicalVariant != want {
		t.Fatalf("tropical rare variant = %d, want %d", f.tropicalVariant, want)
	}
}

func TestTropicalPackVariant(t *testing.T) {
	if got := tropicalPackVariant(tropicalPatternPackedIds[0], tfWhite, tfWhite); got != 0 {
		t.Fatalf("packVariant(KOB,WHITE,WHITE) = %d, want 0", got)
	}
	want := 257 | (1 << 16) | (7 << 24)
	if got := tropicalPackVariant(tropicalPatternPackedIds[7], tfOrange, tfGray); got != want {
		t.Fatalf("packVariant(STRIPEY,ORANGE,GRAY) = %d, want %d", got, want)
	}
	if len(tropicalCommonVariants) != 22 {
		t.Fatalf("COMMON_VARIANTS len = %d, want 22", len(tropicalCommonVariants))
	}
	if len(tropicalPatternPackedIds) != 12 {
		t.Fatalf("Pattern.values() len = %d, want 12", len(tropicalPatternPackedIds))
	}
}

func TestSalmonVariantWeighted(t *testing.T) {
	loop, _, floorY := finalizeLoop(t)
	sawSmall, sawMed, sawLarge := false, false, false
	for i := 0; i < 200; i++ {
		f := loop.spawnFish(entity.Salmon, 8.5, float64(floorY+1), 8.5)
		switch f.salmonVariant {
		case salmonVariantSmall:
			if f.salmonScale != salmonScaleSmall {
				t.Fatalf("SMALL scale = %v, want 0.5", f.salmonScale)
			}
			sawSmall = true
		case salmonVariantMedium:
			if f.salmonScale != salmonScaleMedium {
				t.Fatalf("MEDIUM scale = %v, want 1.0", f.salmonScale)
			}
			sawMed = true
		case salmonVariantLarge:
			if f.salmonScale != salmonScaleLarge {
				t.Fatalf("LARGE scale = %v, want 1.5", f.salmonScale)
			}
			sawLarge = true
		default:
			t.Fatalf("salmon variant = %d, want 0/1/2", f.salmonVariant)
		}
	}
	if !sawSmall || !sawMed || !sawLarge {
		t.Fatalf("missing size: small=%v med=%v large=%v", sawSmall, sawMed, sawLarge)
	}
}

func TestSalmonFinalizeVariantWeights(t *testing.T) {
	mk := func(seed uint64) *Entity {
		e := &Entity{}
		e.ai = &mobAI{rng: newEntityRandom(seed)}
		return e
	}
	counts := map[int]int{}
	for s := uint64(0); s < 3000; s++ {
		v, sc := salmonFinalizeVariant(mk(s))
		counts[v]++
		switch v {
		case salmonVariantSmall:
			if sc != salmonScaleSmall {
				t.Fatalf("SMALL scale %v", sc)
			}
		case salmonVariantMedium:
			if sc != salmonScaleMedium {
				t.Fatalf("MEDIUM scale %v", sc)
			}
		case salmonVariantLarge:
			if sc != salmonScaleLarge {
				t.Fatalf("LARGE scale %v", sc)
			}
		default:
			t.Fatalf("bad variant %d", v)
		}
	}
	if !(counts[salmonVariantMedium] > counts[salmonVariantSmall] && counts[salmonVariantSmall] > counts[salmonVariantLarge]) {
		t.Fatalf("weight ordering off: %v", counts)
	}
}

func TestStriderFinalizeAlwaysSet(t *testing.T) {
	loop, _, floorY := finalizeLoop(t)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(12345)
	saddled, total := 0, 0
	for i := 0; i < 400; i++ {
		s := loop.spawnStrider(8.5, float64(floorY+1), 8.5)
		total++
		if !s.striderFinalized {
			t.Fatalf("strider not finalized")
		}
		if s.breedAge >= 0 && s.striderSaddled {
			saddled++
		}
	}
	if saddled == 0 {
		t.Fatal("no saddled jockey strider over 400 spawns (1-in-30 expected)")
	}
	if saddled > total/2 {
		t.Fatalf("too many saddled: %d/%d", saddled, total)
	}
}

func TestStriderJockeyDrawOrder(t *testing.T) {
	loop, _, floorY := finalizeLoop(t)
	var seed int64 = 1
	for ; seed < 200000; seed++ {
		if levelgen.NewLegacyRandomSource(seed).NextIntN(30) == 0 {
			break
		}
	}
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
	s := loop.spawnStrider(8.5, float64(floorY+1), 8.5)
	if !s.striderSaddled {
		t.Fatalf("seed %d jockey branch but striderSaddled not set", seed)
	}
	ref := levelgen.NewLegacyRandomSource(seed)
	_ = ref.NextIntN(30)
	_ = ref.NextFloat()
	if got, want := loop.only().levelRandom.NextIntN(1000), ref.NextIntN(1000); got != want {
		t.Fatalf("strider draw order mismatch: level=%d ref=%d", got, want)
	}
}

func TestFrogSpawnTemperateDefault(t *testing.T) {
	loop, _, floorY := finalizeLoop(t)
	f := loop.spawnFrog(8.5, float64(floorY+1), 8.5, false)
	if f.frogVariant != frogVariantTemperate {
		t.Fatalf("frog variant = %d, want temperate %d", f.frogVariant, frogVariantTemperate)
	}
	if !f.isFrog {
		t.Fatal("frog not marked isFrog")
	}
}

func TestFrogFinalizeConsumesInitMemoriesDraw(t *testing.T) {
	loop, _, floorY := finalizeLoop(t)
	const seed = 0xF209
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
	_ = loop.spawnFrog(8.5, float64(floorY+1), 8.5, false)
	ref := levelgen.NewLegacyRandomSource(seed)
	_ = ref.NextIntN(frogTimeBetweenLongJumpsSpan)
	if got, want := loop.only().levelRandom.NextIntN(1000), ref.NextIntN(1000); got != want {
		t.Fatalf("frog initMemories draw not 1:1: level=%d ref=%d", got, want)
	}
}

func TestTadpoleGrowFrogNoReRoll(t *testing.T) {
	loop, _, floorY := finalizeLoop(t)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(999)
	before := loop.only().levelRandom.NextIntN(1000)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(999)
	tp := loop.spawnTadpole(8.5, float64(floorY+1), 8.5)
	tp.tadpoleAge = tadpoleTicksToBeFrog
	loop.tadpoleGrowIntoFrog(tp)
	after := loop.only().levelRandom.NextIntN(1000)
	if before != after {
		t.Fatalf("tadpole grow consumed a level draw (before=%d after=%d)", before, after)
	}
}
