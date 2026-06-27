package attribute

import (
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// scriptedRandom is a deterministic RandomSource that returns a pre-set sequence of doubles and
// floats, recording the ORDER in which NextDouble / NextFloat are called. It lets the finalizeSpawn
// test assert the EXACT draw order (two nextDouble() for the triangle minuend/subtrahend, THEN one
// nextFloat() for the left-handed roll) — the load-bearing fidelity property.
type scriptedRandom struct {
	doubles []float64
	floats  []float32
	di, fi  int
	// calls records each draw as "d" or "f" in call order.
	calls []string
}

func (s *scriptedRandom) NextDouble() float64 {
	s.calls = append(s.calls, "d")
	v := s.doubles[s.di]
	s.di++
	return v
}

func (s *scriptedRandom) NextFloat() float32 {
	s.calls = append(s.calls, "f")
	v := s.floats[s.fi]
	s.fi++
	return v
}

// TestTriangle_DrawOrderMinuendFirst pins RandomSource.triangle(d, d2) = d + d2*(nextDouble() -
// nextDouble()) with the MINUEND drawn FIRST. With doubles [0.8, 0.3]: 0.0 + 0.11485*(0.8-0.3).
func TestTriangle_DrawOrderMinuendFirst(t *testing.T) {
	r := &scriptedRandom{doubles: []float64{0.8, 0.3}}
	got := triangle(r, 0.0, randomSpawnBonusSpread)
	want := 0.0 + randomSpawnBonusSpread*(0.8-0.3)
	if got != want {
		t.Fatalf("triangle = %v, want %v", got, want)
	}
	if len(r.calls) != 2 || r.calls[0] != "d" || r.calls[1] != "d" {
		t.Fatalf("triangle draw order = %v, want two nextDouble() draws", r.calls)
	}
	// Confirm minuend-first: if subtrahend were drawn first the sign would flip.
	if got <= 0 {
		t.Fatalf("triangle with minuend 0.8 > subtrahend 0.3 should be positive, got %v", got)
	}
}

// TestFinalizeSpawn_DrawOrderAndBonus asserts the full Mob.finalizeSpawn attribute+RNG slice on a
// fresh mob map:
//  1. exactly two nextDouble() draws (the triangle) THEN one nextFloat() draw, in that order;
//  2. the FOLLOW_RANGE attribute gains a permanent ADD_MULTIPLIED_BASE modifier with amount ==
//     triangle(0, 0.11485) and id "minecraft:random_spawn_bonus";
//  3. the returned leftHanded == (nextFloat() < 0.05F).
func TestFinalizeSpawn_DrawOrderAndBonus(t *testing.T) {
	m := NewMapForEntity("zombie") // a Mob -> has follow_range (override 35.0)
	if m == nil {
		t.Fatal("zombie supplier missing")
	}
	// doubles drive the triangle (minuend 0.9, subtrahend 0.1 -> +0.8 spread); float 0.04 < 0.05 ->
	// left-handed true.
	r := &scriptedRandom{doubles: []float64{0.9, 0.1}, floats: []float32{0.04}}

	leftHanded := FinalizeSpawn(m, r)

	// (1) draw order: d, d, f.
	wantOrder := []string{"d", "d", "f"}
	if len(r.calls) != 3 || r.calls[0] != wantOrder[0] || r.calls[1] != wantOrder[1] || r.calls[2] != wantOrder[2] {
		t.Fatalf("finalizeSpawn draw order = %v, want %v", r.calls, wantOrder)
	}

	// (2) the permanent modifier on FOLLOW_RANGE.
	follow := m.GetInstance(FollowRange.Name())
	mod, ok := follow.GetModifier(randomSpawnBonusID)
	if !ok {
		t.Fatal("FOLLOW_RANGE missing random_spawn_bonus modifier after finalizeSpawn")
	}
	// Compute the expected amount through the SAME triangle() path so the comparison is bit-exact
	// (a hand-written `spread*(0.9-0.1)` would differ in the last ULP from `0.0 + spread*(m-s)`).
	wantAmount := triangle(&scriptedRandom{doubles: []float64{0.9, 0.1}}, 0.0, randomSpawnBonusSpread)
	if mod.Amount != wantAmount {
		t.Errorf("bonus amount = %v, want %v", mod.Amount, wantAmount)
	}
	if mod.Operation != AddMultipliedBase {
		t.Errorf("bonus operation = %v, want AddMultipliedBase", mod.Operation)
	}
	// The folded follow_range reflects the bonus: base 35 * (1 + amount) via ADD_MULTIPLIED_BASE
	// (d1=35, d3 = 35 + 35*amount).
	wantFollow := 35.0 + 35.0*wantAmount
	if got := m.GetValue(FollowRange.Name()); got != wantFollow {
		t.Errorf("folded follow_range = %v, want %v", got, wantFollow)
	}

	// (3) left-handed roll.
	if !leftHanded {
		t.Errorf("leftHanded = false, want true (nextFloat 0.04 < 0.05)")
	}
}

// TestFinalizeSpawn_LeftHandedThreshold confirms the 5% boundary: nextFloat() exactly 0.05 is NOT
// left-handed (strict <), 0.0499 IS.
func TestFinalizeSpawn_LeftHandedThreshold(t *testing.T) {
	for _, c := range []struct {
		f    float32
		want bool
	}{
		{0.0, true},
		{0.049, true},
		{0.05, false}, // strict < 0.05F
		{0.06, false},
		{1.0, false},
	} {
		m := NewMapForEntity("zombie")
		r := &scriptedRandom{doubles: []float64{0.5, 0.5}, floats: []float32{c.f}}
		if got := FinalizeSpawn(m, r); got != c.want {
			t.Errorf("leftHanded(nextFloat=%v) = %v, want %v", c.f, got, c.want)
		}
	}
}

// TestFinalizeSpawn_GuardSkipsOnRefinalize confirms the re-finalize guard: a second FinalizeSpawn on
// the same map does NOT add a duplicate bonus (hasModifier short-circuits), so it draws only the
// nextFloat() (no triangle re-draw).
func TestFinalizeSpawn_GuardSkipsOnRefinalize(t *testing.T) {
	m := NewMapForEntity("zombie")
	r1 := &scriptedRandom{doubles: []float64{0.7, 0.2}, floats: []float32{0.5}}
	FinalizeSpawn(m, r1)

	// Second finalize: the guard sees the existing modifier -> NO triangle draw, only the float.
	r2 := &scriptedRandom{doubles: []float64{0.1, 0.1}, floats: []float32{0.5}}
	FinalizeSpawn(m, r2)
	if len(r2.calls) != 1 || r2.calls[0] != "f" {
		t.Fatalf("re-finalize draws = %v, want only one nextFloat() (guard skips triangle)", r2.calls)
	}
	// The bonus modifier is unchanged (still the first roll's amount).
	follow := m.GetInstance(FollowRange.Name())
	mod, _ := follow.GetModifier(randomSpawnBonusID)
	wantAmount := triangle(&scriptedRandom{doubles: []float64{0.7, 0.2}}, 0.0, randomSpawnBonusSpread)
	if mod.Amount != wantAmount {
		t.Errorf("bonus after re-finalize = %v, want unchanged %v", mod.Amount, wantAmount)
	}
}

// TestFinalizeSpawn_DeterministicWithSeededLegacyRandom confirms the integration with the project's
// real LegacyRandomSource: the same seed yields the same finalizeSpawn outcome every run (determinism
// is the whole point of a faithful RNG port). It does NOT pin the magic numbers (those depend on the
// legacy RNG stream, exercised by levelgen's own tests) — it asserts reproducibility.
func TestFinalizeSpawn_DeterministicWithSeededLegacyRandom(t *testing.T) {
	run := func() (float64, bool) {
		m := NewMapForEntity("witch")
		r := levelgen.NewLegacyRandomSource(0xC0FFEE)
		lh := FinalizeSpawn(m, r)
		follow := m.GetInstance(FollowRange.Name())
		mod, _ := follow.GetModifier(randomSpawnBonusID)
		return mod.Amount, lh
	}
	a1, l1 := run()
	a2, l2 := run()
	if a1 != a2 || l1 != l2 {
		t.Fatalf("finalizeSpawn not deterministic: (%v,%v) vs (%v,%v)", a1, l1, a2, l2)
	}
	// The bonus must be within the triangular envelope [-spread, +spread].
	if a1 < -randomSpawnBonusSpread || a1 > randomSpawnBonusSpread {
		t.Errorf("bonus amount %v outside [-%v, %v]", a1, randomSpawnBonusSpread, randomSpawnBonusSpread)
	}
}

// TestFinalizeSpawn_NilMapStillDrawsLeftHanded confirms a nil/attribute-less map (a non-mob) skips the
// bonus but still performs the left-handed draw, keeping the RNG stream uniform across callers.
func TestFinalizeSpawn_NilMapStillDrawsLeftHanded(t *testing.T) {
	r := &scriptedRandom{floats: []float32{0.01}}
	got := FinalizeSpawn(nil, r)
	if !got {
		t.Errorf("nil-map leftHanded = false, want true")
	}
	if len(r.calls) != 1 || r.calls[0] != "f" {
		t.Fatalf("nil-map draws = %v, want only one nextFloat()", r.calls)
	}
}
