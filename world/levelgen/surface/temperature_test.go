package surface

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level/biome"
)

// Goldens pinned from the real 26.2 jar: a Java probe replicated
// Biome.getHeightAdjustedTemperature + the NONE/FROZEN modifiers over the three
// static Biome noises (WorldgenRandom(LegacyRandomSource(1234/3456/2345))) and the
// per-biome `temperature` JSON floats, printing outputs with %.9g.

func mustBiome(t *testing.T, id string) biome.Type {
	t.Helper()
	var bt biome.Type
	if err := bt.UnmarshalText([]byte(id)); err != nil {
		t.Fatalf("unknown biome %q: %v", id, err)
	}
	return bt
}

func TestBiomeClimateTableLoads(t *testing.T) {
	m, err := ensureTemperatureModel()
	if err != nil {
		t.Fatalf("ensureTemperatureModel: %v", err)
	}
	// snowy_plains temperature=0.0, no modifier.
	sp := mustBiome(t, "minecraft:snowy_plains")
	if got := m.getBaseTemperature(sp); got != 0.0 {
		t.Fatalf("snowy_plains base temperature = %v, want 0.0", got)
	}
	if m.frozen[sp] {
		t.Fatalf("snowy_plains should not have the frozen modifier")
	}
	// desert temperature=2.0, no modifier.
	if got := m.getBaseTemperature(mustBiome(t, "minecraft:desert")); got != 2.0 {
		t.Fatalf("desert base temperature = %v, want 2.0", got)
	}
	// frozen_ocean temperature=0.0, frozen modifier.
	fo := mustBiome(t, "minecraft:frozen_ocean")
	if got := m.getBaseTemperature(fo); got != 0.0 {
		t.Fatalf("frozen_ocean base temperature = %v, want 0.0", got)
	}
	if !m.frozen[fo] {
		t.Fatalf("frozen_ocean must have the frozen modifier")
	}
}

func TestGetHeightAdjustedTemperature(t *testing.T) {
	m, err := ensureTemperatureModel()
	if err != nil {
		t.Fatalf("ensureTemperatureModel: %v", err)
	}
	const eps = 1e-6
	cases := []struct {
		name            string
		biomeID         string
		x, y, z, seaLvl int
		want            float32
	}{
		// snowy_plains temp 0.0, none — below snow level, so unadjusted 0.0.
		{"snowy_plains(0,63,0)", "minecraft:snowy_plains", 0, 63, 0, 63, 0.0},
		{"snowy_plains(100,70,100)", "minecraft:snowy_plains", 100, 70, 100, 63, 0.0},
		// desert temp 2.0, none.
		{"desert(0,63,0)", "minecraft:desert", 0, 63, 0, 63, 2.0},
		// frozen_ocean temp 0.0, frozen — the ice-patch carveout yields 0.2 at (0,0),
		// but a different column (50,50) falls outside the carveout back to base 0.0.
		{"frozen_ocean(0,63,0)", "minecraft:frozen_ocean", 0, 63, 0, 63, 0.200000003},
		{"frozen_ocean(50,63,50)", "minecraft:frozen_ocean", 50, 63, 50, 63, 0.0},
		{"frozen_ocean(1000,63,1000)", "minecraft:frozen_ocean", 1000, 63, 1000, 63, 0.200000003},
		// snowy_slopes temp -0.3, none, HIGH altitude (y=200 > snowLevel 80) — the
		// TEMPERATURE_NOISE height adjustment drives it colder: -0.45.
		{"snowy_slopes(0,200,0)", "minecraft:snowy_slopes", 0, 200, 0, 63, -0.450000018},
	}
	for _, c := range cases {
		bt := mustBiome(t, c.biomeID)
		got := m.getHeightAdjustedTemperature(bt, c.x, c.y, c.z, c.seaLvl)
		if math.Abs(float64(got-c.want)) > eps {
			t.Fatalf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestColdEnoughToSnow(t *testing.T) {
	sp := mustBiome(t, "minecraft:snowy_plains")
	desert := mustBiome(t, "minecraft:desert")

	// snowy_plains (temp 0.0) at sea level is cold enough to snow (0.0 < 0.15).
	if !coldEnoughToSnow(sp, 0, 63, 0, 63) {
		t.Fatalf("snowy_plains at sea level must be cold enough to snow")
	}
	// desert (temp 2.0) is NOT cold enough to snow (2.0 >= 0.15).
	if coldEnoughToSnow(desert, 0, 63, 0, 63) {
		t.Fatalf("desert must NOT be cold enough to snow")
	}
	// warmEnoughToRain is the exact complement (Biome.coldEnoughToSnow = !warmEnoughToRain).
	m, _ := ensureTemperatureModel()
	if m.warmEnoughToRain(sp, 0, 63, 0, 63) == coldEnoughToSnow(sp, 0, 63, 0, 63) {
		t.Fatalf("coldEnoughToSnow must be the negation of warmEnoughToRain")
	}
}

func TestFrozenModifierDeterminism(t *testing.T) {
	m, err := ensureTemperatureModel()
	if err != nil {
		t.Fatalf("ensureTemperatureModel: %v", err)
	}
	fo := mustBiome(t, "minecraft:frozen_ocean")
	// Two evaluations at the same column agree (pure over position).
	for _, p := range [][2]int{{0, 0}, {50, 50}, {1000, 1000}, {-321, 654}} {
		a := m.modifyTemperature(fo, p[0], p[1], 0.0)
		b := m.modifyTemperature(fo, p[0], p[1], 0.0)
		if a != b {
			t.Fatalf("FROZEN modifier not deterministic at %v: %v != %v", p, a, b)
		}
		// FROZEN only ever returns the ice-patch constant 0.2f or the base (0.0 here).
		if a != 0.2 && a != 0.0 {
			t.Fatalf("FROZEN modifier at %v = %v, want 0.2 or 0.0", p, a)
		}
	}
	// Pinned Java goldens: modFrozen(0,0,0.0)=0.2, modFrozen(1000,1000,0.0)=0.2.
	if got := m.modifyTemperature(fo, 0, 0, 0.0); got != 0.2 {
		t.Fatalf("FROZEN(0,0) = %v, want 0.2", got)
	}
	if got := m.modifyTemperature(fo, 1000, 1000, 0.0); got != 0.2 {
		t.Fatalf("FROZEN(1000,1000) = %v, want 0.2", got)
	}
	// The NONE modifier is a pass-through for a non-frozen biome.
	sp := mustBiome(t, "minecraft:snowy_plains")
	if got := m.modifyTemperature(sp, 0, 0, 0.0); got != 0.0 {
		t.Fatalf("NONE modifier must pass base through, got %v", got)
	}
}
