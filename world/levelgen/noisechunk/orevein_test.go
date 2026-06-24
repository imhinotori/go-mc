package noisechunk

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen/density"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// veinFn is a test density.Function returning a fixed value at every context (the vein_*
// router inputs are stubbed so the test drives the OreVeinifier deterministically).
type veinFn float64

func (v veinFn) Compute(density.Context) float64 { return float64(v) }
func (v veinFn) MinValue() float64               { return float64(v) }
func (v veinFn) MaxValue() float64               { return float64(v) }

// buildOreVein builds an OreVeinifier from testSeed with stubbed vein_* inputs.
func buildOreVein(t *testing.T, toggle, ridged, gap float64) *OreVeinifier {
	t.Helper()
	r, err := router.NewRouter(testSeed)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}
	return NewOreVeinifier(veinFn(toggle), veinFn(ridged), veinFn(gap), r.Random)
}

// TestVeinTypeSelection: vein_toggle's sign selects COPPER (toggle>0) / IRON (toggle<=0),
// and the VeinType y-range gates placement. COPPER lives y in [0,50], IRON y in [-60,-8].
// Ported from OreVeinifier.lambda$create$0 (d>0 ? COPPER : IRON) + the minY/maxY clamp.
func TestVeinTypeSelection(t *testing.T) {
	// toggle>0 selects COPPER (y-range 0..50). A position INSIDE the band with a strong
	// veininess should be eligible; a position OUTSIDE the band (y far above 50) must
	// never place ore (the minY/maxY guard returns the default => not present).
	copperIn := buildOreVein(t, 0.9, -1.0, 1.0) // ridged<0 (pass), gap>-0.3
	if _, ok := copperIn.vein(8, 25, 8); !ok {
		// A strongly veiny copper position deep in-band with ridged passing should place.
		t.Errorf("copper in-band y=25 toggle=0.9: expected ore present, got none")
	}
	copperOut := buildOreVein(t, 0.9, -1.0, 1.0)
	if _, ok := copperOut.vein(8, 120, 8); ok {
		t.Errorf("copper out-of-band y=120: expected NO ore (above maxY=50), got ore")
	}

	// toggle<=0 selects IRON (y-range -60..-8). In-band deep, out-of-band shallow.
	ironOut := buildOreVein(t, -0.9, -1.0, 1.0)
	if _, ok := ironOut.vein(8, 40, 8); ok {
		t.Errorf("iron out-of-band y=40: expected NO ore (above maxY=-8), got ore")
	}
}

// TestVeinPlacesOre: in a vein region (toggle on, |toggle|+roundoff>=0.4, ridged<0,
// random hits), the veinifier returns the VeinType ore/raw/filler; with veininess BELOW
// the 0.4 threshold it returns none. Ported from the VEININESS_THRESHOLD (0.4) +
// VEIN_SOLIDNESS (0.7) + the ridged>=0 reject.
func TestVeinPlacesOre(t *testing.T) {
	// veininess below threshold (toggle small) => never places (returns none).
	weak := buildOreVein(t, 0.1, -1.0, 1.0)
	if _, ok := weak.vein(8, 25, 8); ok {
		t.Errorf("weak veininess toggle=0.1 (<0.4): expected none, got ore")
	}

	// ridged >= 0 always rejects (the veinRidged.compute(ctx) >= 0 -> default branch).
	ridgedReject := buildOreVein(t, 0.9, 0.5, 1.0)
	if _, ok := ridgedReject.vein(8, 25, 8); ok {
		t.Errorf("ridged=0.5 (>=0): expected none, got ore")
	}

	// A strong copper vein that places must return a copper-family block (copper_ore,
	// raw_copper_block, or granite filler) — never an unrelated id.
	strong := buildOreVein(t, 0.9, -1.0, 1.0)
	copperOre := block.ToStateID[block.CopperOre{}]
	rawCopper := block.ToStateID[block.RawCopperBlock{}]
	granite := block.ToStateID[block.Granite{}]
	placedAny := false
	for y := 0; y <= 50; y++ {
		if st, ok := strong.vein(8, y, 8); ok {
			placedAny = true
			if st != copperOre && st != rawCopper && st != granite {
				t.Errorf("copper vein at y=%d placed id %d, not a copper-family block", y, st)
			}
		}
	}
	if !placedAny {
		t.Errorf("strong copper vein placed nothing across y=0..50 (expected some ore)")
	}
}

// TestVeinDeterministic: two veinifiers from the same seed place identical ore at fixed
// positions (Pitfall 7 — randomness flows from the world seed via the positional factory).
func TestVeinDeterministic(t *testing.T) {
	a := buildOreVein(t, 0.9, -1.0, 1.0)
	b := buildOreVein(t, 0.9, -1.0, 1.0)
	for y := 0; y <= 50; y++ {
		for _, x := range []int{3, 8, 13} {
			sa, oka := a.vein(x, y, x)
			sb, okb := b.vein(x, y, x)
			if oka != okb || sa != sb {
				t.Fatalf("nondeterministic at (%d,%d): a=(%d,%v) b=(%d,%v)", x, y, sa, oka, sb, okb)
			}
		}
	}
}
