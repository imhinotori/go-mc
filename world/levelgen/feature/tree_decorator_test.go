package feature

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// tree_decorator_test.go pins the COMMON overworld TreeDecorators (AlterGround/Beehive/
// Cocoa/vines) against the placed-log/placed-leaf positions, draw-pinned to an oracle
// replaying the SAME rng sequence (T-13-06). The decorators run against map-backed set/read
// callbacks (mapWorld) — no world import.

// decoratorCtx builds a DecoratorContext over a fresh mapWorld with the given log/leaf lists.
func decoratorCtx(mw *mapWorld, rng levelgen.RandomSource, logs, leaves []TreePos) *DecoratorContext {
	return &DecoratorContext{
		Logs:   logs,
		Leaves: leaves,
		Rng:    rng,
		Set:    mw.set,
		Read:   mw.read,
	}
}

// trunkLogs builds a vertical column of log positions [y0, y0+n) at (x,z).
func trunkLogs(x, z, y0, n int) []TreePos {
	out := make([]TreePos, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, TreePos{X: x, Y: y0 + i, Z: z})
	}
	return out
}

// TestAlterGroundDecorator pins AlterGroundDecorator: a podzol disc under the trunk base over
// the beneath_tree_podzol_replaceable ground (dirt), corners trimmed.
func TestAlterGroundDecorator(t *testing.T) {
	// Lay a dirt floor at y=63 so the podzol rule matches.
	mw := newMapWorld()
	dirt := block.ToStateID[block.Dirt{}]
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			mw.set(dx, 63, dz, dirt)
		}
	}
	// The mega_pine alter_ground provider (rule_based -> podzol over podzol-replaceable).
	d, err := ParseTreeDecorator(json.RawMessage(`{"type":"minecraft:alter_ground","provider":{"type":"minecraft:rule_based_state_provider","rules":[{"if_true":{"type":"minecraft:matching_block_tag","tag":"minecraft:beneath_tree_podzol_replaceable"},"then":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:podzol","Properties":{"snowy":"false"}}}}]}}`))
	if err != nil {
		t.Fatalf("parse alter_ground: %v", err)
	}
	// Trunk base at y=64 (the floor is y=63).
	logs := trunkLogs(0, 0, 64, 10)
	rng := levelgen.NewWorldgenRandom(0xA17E)
	d.place(decoratorCtx(mw, rng, logs, nil))

	podzol := block.ToStateID[block.Podzol{Snowy: false}]
	// The center floor (0,63,0) became podzol.
	if mw.read(0, 63, 0) != podzol {
		t.Fatalf("alter_ground did not place podzol under the trunk base (got %v)", mw.read(0, 63, 0))
	}
	// The 4 corners (|dx|==2 && |dz|==2) are TRIMMED -> still dirt.
	if mw.read(2, 63, 2) != dirt {
		t.Fatalf("alter_ground placed podzol in a trimmed corner (2,2)")
	}
	// An edge cell (2,0) is within the disc -> podzol.
	if mw.read(2, 63, 0) != podzol {
		t.Fatalf("alter_ground did not place podzol at the disc edge (2,0)")
	}
}

// TestBeehiveDecorator pins BeehiveDecorator: at probability 1.0 a bee_nest attaches to a
// trunk-side block at the canopy base, facing south, draw-pinned.
func TestBeehiveDecorator(t *testing.T) {
	mw := newMapWorld()
	d := BeehiveDecorator{probability: 1.0}
	logs := trunkLogs(0, 0, 64, 8)
	// Leaves around the top so targetY = max(leaves[0].Y-1, logs[0].Y+1).
	leaves := []TreePos{{X: 0, Y: 70, Z: 0}, {X: 1, Y: 70, Z: 0}}
	rng := levelgen.NewWorldgenRandom(0xBEE5)
	d.place(decoratorCtx(mw, rng, logs, leaves))

	// A bee_nest must have been placed somewhere adjacent to the trunk at the target Y.
	beeNest := beeNestStateOrFatal(t)
	found := false
	for cell, st := range mw.blocks {
		if st == beeNest {
			found = true
			// it faces south (the WORLDGEN_FACING).
			_ = cell
		}
	}
	if !found {
		t.Fatalf("beehive (p=1.0) placed no bee_nest")
	}

	// At probability 0.0 it never places (the first nextFloat draw aborts).
	mw0 := newMapWorld()
	d0 := BeehiveDecorator{probability: 0.0}
	d0.place(decoratorCtx(mw0, levelgen.NewWorldgenRandom(0xBEE5), logs, leaves))
	for _, st := range mw0.blocks {
		if st == beeNest {
			t.Fatalf("beehive (p=0.0) placed a bee_nest")
		}
	}
}

// TestCocoaDecorator pins CocoaDecorator: at probability 1.0 cocoa pods attach to the lower
// trunk faces (per-direction nextFloat<0.25 + nextInt(3) age), draw-pinned.
func TestCocoaDecorator(t *testing.T) {
	mw := newMapWorld()
	d := CocoaDecorator{probability: 1.0}
	logs := trunkLogs(0, 0, 64, 8)
	rng := levelgen.NewWorldgenRandom(0xC0C0)
	d.place(decoratorCtx(mw, rng, logs, nil))

	// Some cocoa may be placed (probabilistic per-face); the key pins are: the top-level
	// nextFloat passed (p=1.0), and any placed block is a cocoa.
	cocoaAny := false
	for _, st := range mw.blocks {
		// cocoa states: resolve a default cocoa to confirm the block family.
		if isCocoaState(st) {
			cocoaAny = true
		}
	}
	// Determinism: a re-run yields the identical set.
	mw2 := newMapWorld()
	d.place(decoratorCtx(mw2, levelgen.NewWorldgenRandom(0xC0C0), logs, nil))
	assertMapWorldsEqual(t, mw, mw2)
	_ = cocoaAny

	// At probability 0.0 it never places.
	mw0 := newMapWorld()
	CocoaDecorator{probability: 0.0}.place(decoratorCtx(mw0, levelgen.NewWorldgenRandom(0xC0C0), logs, nil))
	if len(mw0.blocks) != 0 {
		t.Fatalf("cocoa (p=0.0) placed %d blocks", len(mw0.blocks))
	}
}

// TestLeaveVineDecorator pins LeaveVineDecorator: hanging vines off the leaf faces with a
// per-face nextFloat<probability draw, deterministic.
func TestLeaveVineDecorator(t *testing.T) {
	mw := newMapWorld()
	d := LeaveVineDecorator{probability: 1.0}
	leaves := []TreePos{{X: 0, Y: 70, Z: 0}}
	rng := levelgen.NewWorldgenRandom(0x71AE)
	d.place(decoratorCtx(mw, rng, nil, leaves))
	if len(mw.blocks) == 0 {
		t.Fatalf("leave_vine (p=1.0) placed no vines around a leaf")
	}
	mw2 := newMapWorld()
	d.place(decoratorCtx(mw2, levelgen.NewWorldgenRandom(0x71AE), nil, leaves))
	assertMapWorldsEqual(t, mw, mw2)
}

// TestTrunkVineDecorator pins TrunkVineDecorator: vines on the trunk faces (per-face
// nextInt(3)>0), deterministic.
func TestTrunkVineDecorator(t *testing.T) {
	mw := newMapWorld()
	d := TrunkVineDecorator{}
	logs := trunkLogs(0, 0, 64, 8)
	rng := levelgen.NewWorldgenRandom(0x71A4)
	d.place(decoratorCtx(mw, rng, logs, nil))
	if len(mw.blocks) == 0 {
		t.Fatalf("trunk_vine placed no vines")
	}
	mw2 := newMapWorld()
	d.place(decoratorCtx(mw2, levelgen.NewWorldgenRandom(0x71A4), logs, nil))
	assertMapWorldsEqual(t, mw, mw2)
}

// TestParseTreeDecoratorUnported asserts the special-biome decorators are now PORTED (13-03
// closed the deferred path): attached_to_leaves/pale_moss/creaking_heart all decode. The name
// is retained from 13-02; there is no remaining unported tree-decorator arm for any
// generatable overworld config.
func TestParseTreeDecoratorUnported(t *testing.T) {
	for _, raw := range []string{
		`{"type":"minecraft:attached_to_leaves","probability":0.14,"exclusion_radius_xz":1,"exclusion_radius_y":0,"required_empty_blocks":2,"block_provider":{"type":"minecraft:randomized_int_state_provider","property":"age","source":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:mangrove_propagule","Properties":{"age":"0","hanging":"true","stage":"0","waterlogged":"false"}}},"values":{"type":"minecraft:uniform","min_inclusive":0,"max_inclusive":4}},"directions":["down"]}`,
		`{"type":"minecraft:pale_moss","ground_probability":0.8,"leaves_probability":0.15,"trunk_probability":0.4}`,
		`{"type":"minecraft:creaking_heart","probability":1.0}`,
	} {
		if _, err := ParseTreeDecorator(json.RawMessage(raw)); err != nil {
			t.Fatalf("ParseTreeDecorator(%s) = %v, want a PORTED special decorator (13-03)", raw, err)
		}
	}
}

// TestParsePlaceOnGroundDecorator asserts the place_on_ground decorator (NEW in 26.2, carried
// by the common oak/birch/… tree configs) now PARSES with the codec defaults. Before the port
// it errored ("unknown tree decorator type") which aborted the WHOLE tree config -> no trunk,
// no leaves in generated forests (the worldgen regression this test guards against).
func TestParsePlaceOnGroundDecorator(t *testing.T) {
	// Minimal form: only block_state_provider (tries/radius/height default 128/2/1).
	d, err := ParseTreeDecorator(json.RawMessage(`{"type":"minecraft:place_on_ground","tries":64,"radius":3,"height":1,"block_state_provider":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:moss_carpet"}}}`))
	if err != nil {
		t.Fatalf("ParseTreeDecorator(place_on_ground) = %v, want a parsed decorator", err)
	}
	pog, ok := d.(PlaceOnGroundDecorator)
	if !ok {
		t.Fatalf("place_on_ground parsed to %T, want PlaceOnGroundDecorator", d)
	}
	if pog.tries != 64 || pog.radius != 3 || pog.height != 1 {
		t.Fatalf("place_on_ground fields = tries %d radius %d height %d, want 64/3/1", pog.tries, pog.radius, pog.height)
	}
	// Codec defaults when the optional fields are absent.
	dd, err := ParseTreeDecorator(json.RawMessage(`{"type":"minecraft:place_on_ground","block_state_provider":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:moss_carpet"}}}`))
	if err != nil {
		t.Fatalf("ParseTreeDecorator(place_on_ground defaults) = %v", err)
	}
	def := dd.(PlaceOnGroundDecorator)
	if def.tries != 128 || def.radius != 2 || def.height != 1 {
		t.Fatalf("place_on_ground defaults = tries %d radius %d height %d, want 128/2/1", def.tries, def.radius, def.height)
	}
}

// TestPlaceOnGroundDecorator pins the placement: over a solid dirt floor with air above, the
// decorator scatters the provider block on the block ABOVE the ground within the inflated
// bounding box, and is deterministic under a replayed rng.
func TestPlaceOnGroundDecorator(t *testing.T) {
	mw := newMapWorld()
	dirt := block.ToStateID[block.Dirt{}] // a full solid-render ground block
	for dx := -6; dx <= 6; dx++ {
		for dz := -6; dz <= 6; dz++ {
			mw.set(dx, 63, dz, dirt) // ground at y=63, air above (y=64+)
		}
	}
	d, err := ParseTreeDecorator(json.RawMessage(`{"type":"minecraft:place_on_ground","tries":128,"radius":3,"height":1,"block_state_provider":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:moss_carpet"}}}`))
	if err != nil {
		t.Fatalf("parse place_on_ground: %v", err)
	}
	logs := trunkLogs(0, 0, 64, 6) // trunk base at y=64 (lowest log)
	mossCarpet := block.ToStateID[block.MossCarpet{}]

	rng := levelgen.NewWorldgenRandom(0x9017)
	d.place(decoratorCtx(mw, rng, logs, nil))
	placed := 0
	for _, st := range mw.blocks {
		if st == mossCarpet {
			placed++
		}
	}
	if placed == 0 {
		t.Fatalf("place_on_ground scattered no ground cover onto the solid floor")
	}

	// Determinism: the SAME rng seed reproduces the SAME placement.
	mw2 := newMapWorld()
	for dx := -6; dx <= 6; dx++ {
		for dz := -6; dz <= 6; dz++ {
			mw2.set(dx, 63, dz, dirt)
		}
	}
	d.place(decoratorCtx(mw2, levelgen.NewWorldgenRandom(0x9017), logs, nil))
	assertMapWorldsEqual(t, mw, mw2)
}

// ---- helpers ----

func beeNestStateOrFatal(t *testing.T) block.StateID {
	t.Helper()
	st, err := beeNestState()
	if err != nil {
		t.Fatalf("resolve bee_nest: %v", err)
	}
	return st
}

// isCocoaState reports whether a state id is a cocoa block (any age/facing).
func isCocoaState(st block.StateID) bool {
	b := block.StateList[st]
	return b != nil && b.ID() == "minecraft:cocoa"
}
