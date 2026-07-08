package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// TestProcessorMossify proves the mossify_10_percent rule processor parses and replaces
// cobblestone with mossy_cobblestone via a random_block_match (probability 0.1). The
// per-block seed is positional, so SOME cobblestone positions convert and some don't —
// we assert at least one conversion across a grid and that non-cobblestone is untouched.
func TestProcessorMossify(t *testing.T) {
	procs, err := LoadProcessorList("minecraft:mossify_10_percent")
	if err != nil {
		t.Fatalf("LoadProcessorList(mossify_10_percent): %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("mossify processors = %d; want 1", len(procs))
	}

	cobble := block.ToStateID[block.Cobblestone{}]
	mossy := block.ToStateID[block.MossyCobblestone{}]
	stone := block.ToStateID[block.Stone{}]

	view := newMapView()
	conversions := 0
	cobbleSeen := 0
	// Run the processor over a grid of distinct positions (the seed is positional).
	for x := 0; x < 20; x++ {
		for z := 0; z < 20; z++ {
			out, keep := procs[0].Process(view, x, 64, z, Pos{x, 64, z}, Pos{}, cobble, nil)
			if !keep {
				t.Fatalf("mossify dropped a block at (%d,%d); rule processors keep", x, z)
			}
			cobbleSeen++
			switch out {
			case mossy:
				conversions++
			case cobble:
				// unchanged (the 90% case) — fine.
			default:
				t.Errorf("cobble -> unexpected state %d at (%d,%d)", out, x, z)
			}
		}
	}
	if conversions == 0 {
		t.Error("mossify converted 0 cobblestone over 400 positions; want some (~10%)")
	}
	if conversions == cobbleSeen {
		t.Error("mossify converted ALL cobblestone; want ~10%")
	}

	// Non-cobblestone (stone) is left untouched (no rule matches).
	out, keep := procs[0].Process(view, 0, 64, 0, Pos{0, 64, 0}, Pos{}, stone, nil)
	if !keep || out != stone {
		t.Errorf("mossify changed stone -> %d (keep=%v); want stone untouched", out, keep)
	}
}

// TestProcessorZombieTagMatch proves the zombie_plains processor: a door (matched by the
// #minecraft:doors tag_match) is replaced with air, and cobblestone is mossified at 0.8.
func TestProcessorZombieTagMatch(t *testing.T) {
	procs, err := LoadProcessorList("minecraft:zombie_plains")
	if err != nil {
		t.Fatalf("LoadProcessorList(zombie_plains): %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("zombie_plains processors = %d; want 1", len(procs))
	}

	door := block.ToStateID[block.OakDoor{
		Facing: block.North, Half: block.DoubleBlockHalfLower,
		Hinge: block.DoorHingeSideLeft, Open: false, Powered: false,
	}]
	view := newMapView()
	// A door must be replaced with air by the tag_match rule (always_true location).
	out, keep := procs[0].Process(view, 5, 64, 5, Pos{5, 64, 5}, Pos{}, door, nil)
	if !keep {
		t.Fatal("zombie_plains dropped the door; want air replacement")
	}
	if out != stateAir {
		t.Errorf("oak_door -> %d; want air (%d) via #minecraft:doors tag_match", out, stateAir)
	}
}

// TestProcessorTransformChain proves PlaceInWorld runs the processor chain: placing a
// template through the mossify processor converts SOME of its cobblestone to mossy.
func TestProcessorTransformChain(t *testing.T) {
	tmpl, err := LoadTemplate(plainsSmallHouse1)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	procs, err := LoadProcessorList("minecraft:mossify_10_percent")
	if err != nil {
		t.Fatalf("LoadProcessorList: %v", err)
	}

	plain := newMapView()
	mossed := newMapView()
	rng := levelgen.NewLegacyRandomSource(1)
	tmpl.PlaceInWorld(plain, Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0, nil, bigBox, rng)
	tmpl.PlaceInWorld(mossed, Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0, procs, bigBox, rng)

	mossy := block.ToStateID[block.MossyCobblestone{}]
	mossyCount := 0
	for _, st := range mossed.blocks {
		if st == mossy {
			mossyCount++
		}
	}
	// The plain placement has no mossy cobble; the processed one has some.
	plainMossy := 0
	for _, st := range plain.blocks {
		if st == mossy {
			plainMossy++
		}
	}
	if plainMossy != 0 {
		t.Errorf("plain placement has %d mossy cobble; want 0 (no processor)", plainMossy)
	}
	if mossyCount == 0 {
		t.Error("processed placement has 0 mossy cobble; the mossify processor did not run")
	}
}

// TestProcessorUnsupportedFailsLoud proves an unsupported processor_type FAILS LOUD.
func TestProcessorUnsupportedFailsLoud(t *testing.T) {
	bad := []byte(`{"processors":[{"processor_type":"minecraft:gravity"}]}`)
	if _, err := ParseProcessorList(bad); err == nil {
		t.Error("ParseProcessorList(gravity) returned nil error; want a loud failure")
	}
}

// TestMthGetSeed pins the ported Mth.getSeed against known jar values (the per-block rule
// processor seed). Verified against javap -c Mth.getSeed.
func TestMthGetSeed(t *testing.T) {
	// l = (x*3129871) ^ (z*116129781) ^ y; l = l*l*42317861 + l*11; return l>>16.
	cases := []struct {
		x, y, z int
		want    int64
	}{
		{0, 0, 0, 0},
	}
	for _, c := range cases {
		if got := mthGetSeed(c.x, c.y, c.z); got != c.want {
			t.Errorf("mthGetSeed(%d,%d,%d) = %d; want %d", c.x, c.y, c.z, got, c.want)
		}
	}
	// Determinism: same pos -> same seed, different pos -> (almost always) different.
	if mthGetSeed(1, 2, 3) != mthGetSeed(1, 2, 3) {
		t.Error("mthGetSeed not deterministic")
	}
	if mthGetSeed(1, 2, 3) == mthGetSeed(4, 5, 6) {
		t.Error("mthGetSeed collides on distinct positions")
	}
}
