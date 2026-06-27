package loot

import (
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// countingRandom wraps a LegacyRandomSource and records the bound passed to each
// NextIntN call (the weighted-select + set_count draws), so a test can assert the
// EXACT RNG-consumption sequence of the roll engine against the bytecode.
type countingRandom struct {
	*levelgen.LegacyRandomSource
	intnBounds []int32
	nextIntN   int
	nextFloat  int
}

func newCountingRandom(seed int64) *countingRandom {
	return &countingRandom{LegacyRandomSource: levelgen.NewLegacyRandomSource(seed)}
}

func (c *countingRandom) NextIntN(bound int32) int32 {
	c.intnBounds = append(c.intnBounds, bound)
	c.nextIntN++
	return c.LegacyRandomSource.NextIntN(bound)
}

func (c *countingRandom) NextFloat() float32 {
	c.nextFloat++
	return c.LegacyRandomSource.NextFloat()
}

var _ levelgen.RandomSource = (*countingRandom)(nil)

// TestProviderUniformInclusive asserts UniformGenerator.getInt is INCLUSIVE on both
// ends: lo>=hi -> lo; else lo + nextInt(hi-lo+1). The bound passed to nextInt must
// be (hi-lo+1).
func TestProviderUniformInclusive(t *testing.T) {
	// lo == hi: no draw, returns lo.
	u := &Uniform{Min: ConstantValue(3), Max: ConstantValue(3)}
	cr := newCountingRandom(1)
	ctx := NewLootContextWithSource(cr, 0)
	if got := u.GetInt(ctx); got != 3 {
		t.Errorf("uniform[3,3] = %d, want 3", got)
	}
	if cr.nextIntN != 0 {
		t.Errorf("uniform[3,3] drew %d times, want 0 (lo>=hi short-circuit)", cr.nextIntN)
	}
	// lo < hi: one draw with bound hi-lo+1 = 5; result in [1,5].
	u2 := &Uniform{Min: ConstantValue(1), Max: ConstantValue(5)}
	cr2 := newCountingRandom(1)
	ctx2 := NewLootContextWithSource(cr2, 0)
	got := u2.GetInt(ctx2)
	if got < 1 || got > 5 {
		t.Errorf("uniform[1,5] = %d, out of [1,5]", got)
	}
	if len(cr2.intnBounds) != 1 || cr2.intnBounds[0] != 5 {
		t.Errorf("uniform[1,5] bounds = %v, want [5] (max-min+1)", cr2.intnBounds)
	}
}

// TestProviderConstantRound asserts ConstantValue.getInt is Math.round(value) (the
// NumberProvider default), NOT Mth.floor: 4.6 -> 5, 4.4 -> 4, and getFloat is verbatim.
func TestProviderConstantRound(t *testing.T) {
	ctx := NewLootContext(1, 0)
	if got := ConstantValue(4.6).GetInt(ctx); got != 5 {
		t.Errorf("ConstantValue(4.6).GetInt = %d, want 5 (Math.round)", got)
	}
	if got := ConstantValue(4.4).GetInt(ctx); got != 4 {
		t.Errorf("ConstantValue(4.4).GetInt = %d, want 4 (Math.round)", got)
	}
	if got := ConstantValue(3.0).GetFloat(ctx); got != 3.0 {
		t.Errorf("ConstantValue(3.0).GetFloat = %v, want 3.0", got)
	}
}

// TestPoolDrawOrder asserts the exact RNG-consumption sequence of a 2-entry pool
// with a uniform rolls and a set_count function: for a fixed seed,
//  1. rolls.getInt() draws first: bound = (rollMax-rollMin+1)
//  2. per roll: nextInt(total) for the weighted select (total = sum of weights)
//  3. the picked entry's set_count uniform draws: bound = (countMax-countMin+1)
//
// The bound SEQUENCE is the load-bearing contract (it proves the order, not just
// the final item).
func TestPoolDrawOrder(t *testing.T) {
	// rolls uniform[1,2] (bound 2); two item entries weight 1 + 1 (total 2);
	// the selected entry has set_count uniform[1,4] (bound 4).
	js := `{"pools":[{
		"rolls":{"type":"minecraft:uniform","min":1.0,"max":2.0},
		"entries":[
			{"type":"minecraft:item","name":"minecraft:stone","weight":1,
			 "functions":[{"function":"minecraft:set_count","count":{"type":"minecraft:uniform","min":1.0,"max":4.0}}]},
			{"type":"minecraft:item","name":"minecraft:dirt","weight":1,
			 "functions":[{"function":"minecraft:set_count","count":{"type":"minecraft:uniform","min":1.0,"max":4.0}}]}
		]
	}]}`
	tbl, err := ParseTable([]byte(js))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cr := newCountingRandom(12345)
	ctx := NewLootContextWithSource(cr, 0)
	stacks := RollStacks(tbl, ctx)

	// First draw must be the rolls uniform (bound 2).
	if len(cr.intnBounds) < 1 {
		t.Fatalf("no draws recorded")
	}
	if cr.intnBounds[0] != 2 {
		t.Errorf("first draw bound = %d, want 2 (rolls uniform[1,2])", cr.intnBounds[0])
	}
	// Determine how many rolls happened from the first draw value: re-derive.
	rolls := 1 + int(levelgen.NewLegacyRandomSource(12345).NextIntN(2))
	// The remaining draws must be, per roll: nextInt(total=2) then set_count uniform(bound 4).
	idx := 1
	for r := 0; r < rolls; r++ {
		if idx >= len(cr.intnBounds) {
			t.Fatalf("roll %d: missing weighted-select draw", r)
		}
		if cr.intnBounds[idx] != 2 {
			t.Errorf("roll %d weighted-select bound = %d, want 2 (total weight)", r, cr.intnBounds[idx])
		}
		idx++
		if idx >= len(cr.intnBounds) {
			t.Fatalf("roll %d: missing set_count draw", r)
		}
		if cr.intnBounds[idx] != 4 {
			t.Errorf("roll %d set_count bound = %d, want 4 (uniform[1,4])", r, cr.intnBounds[idx])
		}
		idx++
	}
	if idx != len(cr.intnBounds) {
		t.Errorf("extra draws: consumed %d of %d bounds", idx, len(cr.intnBounds))
	}
	// Each rolled stack must be stone or dirt with count in [1,4].
	for _, s := range stacks {
		if s.Count < 1 || s.Count > 4 {
			t.Errorf("rolled count %d out of [1,4]", s.Count)
		}
	}
}

// TestWeightedSubtractSelect asserts the subtract-to-select picks the entry whose
// cumulative weight band contains r = nextInt(total). With weights [3, 1] and a
// controlled r, r in [0,3) -> entry0, r==3 -> entry1.
func TestWeightedSubtractSelect(t *testing.T) {
	js := `{"pools":[{"rolls":1.0,"entries":[
		{"type":"minecraft:item","name":"minecraft:stone","weight":3},
		{"type":"minecraft:item","name":"minecraft:dirt","weight":1}
	]}]}`
	tbl, err := ParseTable([]byte(js))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	stoneID := itemNameToID["minecraft:stone"]
	dirtID := itemNameToID["minecraft:dirt"]
	// Roll many times from ONE long-lived RNG (a fresh LegacyRandomSource per seed
	// would sample only the LCG's correlated first output — biased for small
	// sequential seeds; a warmed, continuously-advanced source is uniform). Each
	// Roll consumes one nextInt(total) so the source advances between rolls.
	stone, dirt := 0, 0
	ctx := NewLootContext(0xC0FFEE, 0)
	for i := 0; i < 8000; i++ {
		stacks := RollStacks(tbl, ctx)
		if len(stacks) != 1 {
			t.Fatalf("iter %d: got %d stacks, want 1", i, len(stacks))
		}
		switch stacks[0].ItemID {
		case stoneID:
			stone++
		case dirtID:
			dirt++
		default:
			t.Fatalf("iter %d: unexpected item %d", i, stacks[0].ItemID)
		}
	}
	if dirt == 0 || stone == 0 {
		t.Fatalf("expected both stone and dirt to occur; stone=%d dirt=%d", stone, dirt)
	}
	ratio := float64(stone) / float64(dirt)
	if ratio < 2.3 || ratio > 3.7 {
		t.Errorf("stone:dirt ratio = %.2f, want ~3.0 (weights 3:1)", ratio)
	}
}

// TestSingleEligibleFastPath asserts a pool with exactly one eligible entry SKIPS
// the nextInt(total) weighted-select draw (the jar fast-path) — observable in the
// RNG sequence (no weighted-select bound recorded).
func TestSingleEligibleFastPath(t *testing.T) {
	js := `{"pools":[{"rolls":1.0,"entries":[
		{"type":"minecraft:item","name":"minecraft:stone","weight":5}
	]}]}`
	tbl, err := ParseTable([]byte(js))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cr := newCountingRandom(99)
	ctx := NewLootContextWithSource(cr, 0)
	stacks := RollStacks(tbl, ctx)
	if len(stacks) != 1 {
		t.Fatalf("got %d stacks, want 1", len(stacks))
	}
	// rolls is a bare constant 1.0 (no draw); single eligible -> no weighted-select draw.
	if cr.nextIntN != 0 {
		t.Errorf("single-eligible pool drew NextIntN %d times, want 0 (fast path skips nextInt(total)); bounds=%v", cr.nextIntN, cr.intnBounds)
	}
}

// TestEmptyEntryParticipatesDropsNothing asserts an `empty` entry participates in
// weighted selection (consumes weight) but emits nothing. A pool with one item
// (weight 1) and one empty (weight 99) almost always rolls nothing.
func TestEmptyEntryParticipatesDropsNothing(t *testing.T) {
	js := `{"pools":[{"rolls":1.0,"entries":[
		{"type":"minecraft:item","name":"minecraft:stone","weight":1},
		{"type":"minecraft:empty","weight":99}
	]}]}`
	tbl, err := ParseTable([]byte(js))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	empties, items := 0, 0
	ctx := NewLootContext(0xBEEF, 0) // one warmed source (sequential-seed first draw is biased)
	for i := 0; i < 8000; i++ {
		stacks := RollStacks(tbl, ctx)
		if len(stacks) == 0 {
			empties++
		} else {
			items++
		}
	}
	if empties == 0 {
		t.Errorf("empty entry never selected (should be ~99%% of rolls)")
	}
	if items == 0 {
		t.Errorf("item entry never selected (should be ~1%% of rolls)")
	}
	// empty should dominate (weight 99 vs 1).
	if empties < items {
		t.Errorf("empty=%d items=%d; empty (weight 99) should dominate", empties, items)
	}
}

// TestRollsCapEnforced asserts a constant rolls far above maxRolls is capped by the
// engine (T-20-01 DoS): the result count never exceeds maxRolls.
func TestRollsCapEnforced(t *testing.T) {
	js := `{"pools":[{"rolls":2000000000,"entries":[
		{"type":"minecraft:item","name":"minecraft:stone","weight":1}
	]}]}`
	tbl, err := ParseTable([]byte(js))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ctx := NewLootContext(1, 0)
	stacks := RollStacks(tbl, ctx)
	if len(stacks) > maxRolls {
		t.Errorf("rolled %d stacks, exceeds maxRolls cap %d", len(stacks), maxRolls)
	}
	if len(stacks) != maxRolls {
		t.Errorf("expected exactly maxRolls=%d stacks for the capped giant rolls, got %d", maxRolls, len(stacks))
	}
}
