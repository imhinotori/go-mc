package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// TestJigsawPoolParse pins the real plains town_centers template_pool parse: the weight-
// expanded element count (4 town_center variants @ weight 50 + 4 zombie variants @ weight 1
// = 204) and the fallback id. CFR StructureTemplatePool (the constructor's weight-expansion).
func TestJigsawPoolParse(t *testing.T) {
	p, err := LoadTemplatePool("minecraft:village/plains/town_centers")
	if err != nil {
		t.Fatalf("LoadTemplatePool: %v", err)
	}
	if got := p.Size(); got != 204 {
		t.Errorf("town_centers expanded size = %d; want 204 (4*50 + 4*1)", got)
	}
	if got := p.Fallback(); got != "minecraft:empty" {
		t.Errorf("town_centers fallback = %q; want minecraft:empty", got)
	}
}

// TestJigsawPoolEmptyTerminator pins the empty.json terminator (Pitfall #6): the
// minecraft:empty pool resolves through data.TemplatePoolJSON("empty") to a size-0 pool whose
// fallback is itself — it must NOT panic, and getRandomTemplate returns the EmptyPoolElement.
func TestJigsawPoolEmptyTerminator(t *testing.T) {
	p, err := LoadTemplatePool("minecraft:empty")
	if err != nil {
		t.Fatalf("LoadTemplatePool(empty): %v", err)
	}
	if p.Size() != 0 {
		t.Errorf("empty pool size = %d; want 0", p.Size())
	}
	rng := levelgen.NewLegacyRandomSource(1)
	el := p.getRandomTemplate(rng)
	if !el.IsEmpty() {
		t.Errorf("empty pool getRandomTemplate did not return the EmptyPoolElement terminator")
	}
}

// TestJigsawPoolWeightedExpansion proves the weighted-candidate selection draws over the
// EXPANDED list (Pitfall #6): a weight-50 element appears ~50x more often than a weight-1
// element across many getRandomTemplate draws. This pins the "weight IS the count" semantics.
func TestJigsawPoolWeightedExpansion(t *testing.T) {
	p, err := LoadTemplatePool("minecraft:village/plains/town_centers")
	if err != nil {
		t.Fatalf("LoadTemplatePool: %v", err)
	}
	// Count how many drawn elements are the high-weight (weight-50) town_center variants vs the
	// low-weight (weight-1) zombie variants. The high-weight set is 200/204 of the list, so the
	// vast majority of draws land there.
	rng := levelgen.NewLegacyRandomSource(7)
	high := 0
	const draws = 2000
	for i := 0; i < draws; i++ {
		el := p.getRandomTemplate(rng)
		se, ok := el.(*singlePoolElement)
		if !ok {
			t.Fatalf("draw %d: element is not a singlePoolElement", i)
		}
		// The zombie variants live under .../zombie/...; the high-weight ones do not.
		if !containsSub(se.location, "/zombie/") {
			high++
		}
	}
	// 200/204 ~= 98% expected; assert a wide band so the test is not flaky but still proves the
	// weighting (a non-expanded uniform draw over 8 distinct elements would give ~50%).
	if high < draws*9/10 {
		t.Errorf("high-weight draws = %d/%d; want >=90%% (weight-expansion not applied?)", high, draws)
	}
}

// TestJigsawPoolShuffleDrawCount proves getShuffledTemplates draws EXACTLY size-1 times
// (Util.shuffle Fisher-Yates) — the draw count is load-bearing (Pitfall #6 "piece-selection
// RNG draws desync"). We compare a counting rng's draw total against size-1.
func TestJigsawPoolShuffleDrawCount(t *testing.T) {
	p, err := LoadTemplatePool("minecraft:village/plains/streets")
	if err != nil {
		t.Fatalf("LoadTemplatePool: %v", err)
	}
	cr := &countingRandom{RandomSource: levelgen.NewLegacyRandomSource(3)}
	_ = p.getShuffledTemplates(cr)
	want := p.Size() - 1
	if cr.intNCount != want {
		t.Errorf("getShuffledTemplates drew %d nextInt; want size-1 = %d", cr.intNCount, want)
	}
}

// TestJigsawPoolListAndFeature confirms the list/feature element types parse without error
// where villages reference them (the common spawner pools use feature_pool_element / nested
// lists). A parse panic/error here would break village assembly when a town_center jigsaw
// targets one of those pools.
func TestJigsawPoolListAndFeature(t *testing.T) {
	// village/common/animals is a feature/list pool referenced by village villager pools.
	for _, id := range []string{
		"minecraft:village/common/animals",
		"minecraft:village/common/sheep",
		"minecraft:village/common/iron_golem",
	} {
		if _, err := LoadTemplatePool(id); err != nil {
			t.Errorf("LoadTemplatePool(%q): %v", id, err)
		}
	}
}

// --- test helpers ---

// countingRandom wraps a RandomSource and counts NextIntN draws (the shuffle draw count).
type countingRandom struct {
	levelgen.RandomSource
	intNCount int
}

func (c *countingRandom) NextIntN(bound int32) int32 {
	c.intNCount++
	return c.RandomSource.NextIntN(bound)
}

// containsSub reports whether sub occurs in s (avoids importing strings for one call).
func containsSub(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
