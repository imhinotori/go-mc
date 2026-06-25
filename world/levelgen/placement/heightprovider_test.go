package placement

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// TestTrapezoidIntSample pins the TrapezoidalInt (PLAIN-INT min/max/plateau) sample —
// the PRIMARY random_offset spread. The flower_default xz_spread {min:-7,max:7,
// plateau:0} for seed 99 yields 1 (two nextInt(8) draws: 5 + 3, then -7). The
// plateau>=range uniform-fallback path is also exercised.
func TestTrapezoidIntSample(t *testing.T) {
	raw := json.RawMessage(`{"type":"minecraft:trapezoid","min":-7,"max":7,"plateau":0}`)
	ip, err := parseIntProvider(raw)
	if err != nil {
		t.Fatalf("parse trapezoid: %v", err)
	}
	if ip.kind != intTrapezoid {
		t.Fatalf("trapezoid parsed as kind %d, want intTrapezoid", ip.kind)
	}

	rng := newCounter(99)
	got := ip.Sample(rng)
	if got != 1 {
		t.Fatalf("trapezoid(seed99,min-7,max7,plateau0) = %d, want 1", got)
	}
	// TWO nextInt draws in the plateau<range path.
	if rng.draws != 2 {
		t.Fatalf("trapezoid two-draw path consumed %d draws, want 2", rng.draws)
	}
	// Post-draw state pinned: exactly two NextIntN(8) consumed (a reorder/wrong-width
	// draw would desync the fingerprint).
	if rng.NextInt() != fingerprintAfter(99, 2, 8) {
		t.Fatalf("trapezoid post-draw state mismatch")
	}

	// plateau >= range -> uniform fallback (ONE draw): min..max with plateau huge.
	rawUni := json.RawMessage(`{"type":"minecraft:trapezoid","min":0,"max":4,"plateau":100}`)
	ipUni, err := parseIntProvider(rawUni)
	if err != nil {
		t.Fatalf("parse trapezoid uniform-fallback: %v", err)
	}
	rngU := newCounter(99)
	_ = ipUni.Sample(rngU)
	if rngU.draws != 1 {
		t.Fatalf("trapezoid uniform-fallback consumed %d draws, want 1", rngU.draws)
	}
}

// fingerprintAfter advances a fresh seed by n NextIntN(bound) draws then returns the
// next NextInt — the post-draw state fingerprint a sample must match.
func fingerprintAfter(seed int64, n int, bound int32) int32 {
	r := levelgen.NewLegacyRandomSource(seed)
	for i := 0; i < n; i++ {
		_ = r.NextIntN(bound)
	}
	return r.NextInt()
}

// TestClampedNormalSample pins the ClampedNormalInt sample (mean + dev*NextGaussian,
// clamped, f2i truncated). mean=0 dev=3 min=-5 max=5 seed 12345 -> 0 (g=-0.1878,
// v=-0.5634 truncates to 0). Uses NextGaussian on the legacy source.
func TestClampedNormalSample(t *testing.T) {
	raw := json.RawMessage(`{"type":"minecraft:clamped_normal","mean":0.0,"deviation":3.0,"min_inclusive":-5,"max_inclusive":5}`)
	ip, err := parseIntProvider(raw)
	if err != nil {
		t.Fatalf("parse clamped_normal: %v", err)
	}
	if ip.kind != intClampedNormal {
		t.Fatalf("clamped_normal parsed as kind %d", ip.kind)
	}
	rng := levelgen.NewLegacyRandomSource(12345)
	got := ip.Sample(rng)
	if got != 0 {
		t.Fatalf("clamped_normal(12345,mean0,dev3,min-5,max5) = %d, want 0", got)
	}

	// Clamping: a huge positive deviation must clamp to max. mean=100 dev=0 -> clamp(100)->max.
	rawHi := json.RawMessage(`{"type":"minecraft:clamped_normal","mean":100.0,"deviation":0.0,"min_inclusive":-5,"max_inclusive":5}`)
	ipHi, _ := parseIntProvider(rawHi)
	if got := ipHi.Sample(levelgen.NewLegacyRandomSource(1)); got != 5 {
		t.Fatalf("clamped_normal clamp-to-max = %d, want 5", got)
	}
}

// TestVeryBiasedToBottomSample pins the VeryBiasedToBottomInt three-draw biased pick.
// min 0 max 10 seed 555 -> 4 (i=9, j=5, result=4).
func TestVeryBiasedToBottomSample(t *testing.T) {
	raw := json.RawMessage(`{"type":"minecraft:very_biased_to_bottom","min_inclusive":0,"max_inclusive":10}`)
	ip, err := parseIntProvider(raw)
	if err != nil {
		t.Fatalf("parse very_biased_to_bottom: %v", err)
	}
	if ip.kind != intVeryBiasedToBottom {
		t.Fatalf("very_biased_to_bottom parsed as kind %d", ip.kind)
	}
	rng := newCounter(555)
	got := ip.Sample(rng)
	if got != 4 {
		t.Fatalf("very_biased_to_bottom(555,0,10) = %d, want 4", got)
	}
	if rng.draws != 3 {
		t.Fatalf("very_biased_to_bottom consumed %d draws, want 3", rng.draws)
	}
}

// TestParseIntProviderUnknownErrors: an unported int provider type errors loudly.
func TestParseIntProviderUnknownErrors(t *testing.T) {
	if _, err := parseIntProvider(json.RawMessage(`{"type":"minecraft:made_up"}`)); err == nil {
		t.Fatalf("parseIntProvider accepted an unported type silently")
	}
}
