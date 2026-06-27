package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// block_survival_test.go covers BLOCK-SURVIVAL (vegetation): breaking a block that supports a
// plant above must destroy + drop the now-unsupported plant (the Phase-17 gate finding "rompe un
// bloque con flores arriba, las flores no se rompen"). Tests assert the air SetBlock at the plant
// pos, the spawned drop Item entity, the 2-tall cascade, the non-vegetation no-op, and the
// still-supported survival.

// countItemEntities returns how many Item-type entities are in the loop's store.
func countItemEntities(loop *TickLoop) int {
	n := 0
	for _, e := range loop.entities.all() {
		if e != nil && e.typ == entity.Item.ID {
			n++
		}
	}
	return n
}

// breakSupport mutates the world cell at pos to air (the broken support) and runs the survival
// neighbor check exactly as reconcileEdit does after a real break. It mirrors the production seam:
// SetBlock then updateVegetationOnEdit. Returns nothing; callers assert on the world + entity store.
func breakSupport(loop *TickLoop, pos pk.Position) {
	loop.world.SetBlock(pos, block.ToStateID[block.Air{}], dimMinY)
	loop.updateVegetationOnEdit(pos)
}

// TestVegetationCanSurvivePredicate unit-checks vegetationCanSurvive against the jar-cited rules:
// a single flower survives on dirt, not on air; a double-plant lower half survives on dirt, not on
// air; a double-plant upper half survives only over its matching lower half.
func TestVegetationCanSurvivePredicate(t *testing.T) {
	dirt := block.ToStateID[block.Dirt{}]
	air := block.ToStateID[block.Air{}]
	stone := block.ToStateID[block.Stone{}]
	poppy := block.ToStateID[block.Poppy{}]
	tallGrassLower := block.ToStateID[block.TallGrass{Half: block.DoubleBlockHalfLower}]
	tallGrassUpper := block.ToStateID[block.TallGrass{Half: block.DoubleBlockHalfUpper}]
	largeFernLower := block.ToStateID[block.LargeFern{Half: block.DoubleBlockHalfLower}]

	cases := []struct {
		name        string
		state       block.StateID
		below       block.StateID
		wantSurvive bool
	}{
		{"poppy on dirt survives", poppy, dirt, true},
		{"poppy on air dies", poppy, air, false},
		{"poppy on stone dies (stone not SUPPORTS_VEGETATION)", poppy, stone, false},
		{"tall_grass lower on dirt survives", tallGrassLower, dirt, true},
		{"tall_grass lower on air dies", tallGrassLower, air, false},
		{"tall_grass upper over its lower survives", tallGrassUpper, tallGrassLower, true},
		{"tall_grass upper over dirt dies (no matching lower)", tallGrassUpper, dirt, false},
		{"tall_grass upper over a DIFFERENT plant's lower dies", tallGrassUpper, largeFernLower, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vegetationCanSurvive(c.state, c.below); got != c.wantSurvive {
				t.Fatalf("vegetationCanSurvive(%s) = %v, want %v", c.name, got, c.wantSurvive)
			}
		})
	}
}

// TestBreakUnderFlowerDestroysAndDrops: breaking the dirt under a poppy destroys the poppy (the
// cell becomes air) and spawns its drop (a poppy item) — the core gate-finding fix.
func TestBreakUnderFlowerDestroysAndDrops(t *testing.T) {
	loop, mgr := newDropLoop()
	_ = blockPlayer(loop, 1.5, 65.0, 1.5) // a tracking player so the air BlockUpdate has a recipient

	support := pk.Position{X: 1, Y: 64, Z: 1}
	flowerPos := pk.Position{X: 1, Y: 65, Z: 1}
	mgr.SetBlock(support, block.ToStateID[block.Dirt{}], dimMinY)
	mgr.SetBlock(flowerPos, block.ToStateID[block.Poppy{}], dimMinY)

	beforeItems := countItemEntities(loop)
	breakSupport(loop, support)

	// The poppy must now be air (destroyed because its support was removed).
	if got, ok := mgr.GetBlock(flowerPos, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("poppy at %v = (state %d, ok=%v), want AIR after its support was broken", flowerPos, got, ok)
	}
	// Exactly one new Item entity (the poppy's own drop) was spawned.
	if got := countItemEntities(loop); got != beforeItems+1 {
		t.Fatalf("item entities = %d, want %d (the destroyed poppy drops itself)", got, beforeItems+1)
	}
}

// TestBreakUnderTwoTallGrassCascades: breaking the dirt under a 2-tall tall_grass destroys BOTH
// halves (lower loses ground support -> destroyed; upper loses its matching lower half -> destroyed
// via the bounded recursion), and each rolls its own drop.
func TestBreakUnderTwoTallGrassCascades(t *testing.T) {
	loop, mgr := newDropLoop()
	_ = blockPlayer(loop, 1.5, 67.0, 1.5)

	support := pk.Position{X: 1, Y: 64, Z: 1}
	lowerPos := pk.Position{X: 1, Y: 65, Z: 1}
	upperPos := pk.Position{X: 1, Y: 66, Z: 1}
	mgr.SetBlock(support, block.ToStateID[block.Dirt{}], dimMinY)
	mgr.SetBlock(lowerPos, block.ToStateID[block.TallGrass{Half: block.DoubleBlockHalfLower}], dimMinY)
	mgr.SetBlock(upperPos, block.ToStateID[block.TallGrass{Half: block.DoubleBlockHalfUpper}], dimMinY)

	breakSupport(loop, support)

	if got, ok := mgr.GetBlock(lowerPos, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("tall_grass LOWER at %v = (state %d, ok=%v), want AIR", lowerPos, got, ok)
	}
	if got, ok := mgr.GetBlock(upperPos, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("tall_grass UPPER at %v = (state %d, ok=%v), want AIR (cascade)", upperPos, got, ok)
	}
}

// TestBreakUnderNonVegetationNoOp: breaking a block whose neighbor above is NOT vegetation (a solid
// stone block) destroys nothing above and spawns no spurious drop.
func TestBreakUnderNonVegetationNoOp(t *testing.T) {
	loop, mgr := newDropLoop()
	_ = blockPlayer(loop, 1.5, 65.0, 1.5)

	support := pk.Position{X: 1, Y: 64, Z: 1}
	abovePos := pk.Position{X: 1, Y: 65, Z: 1}
	mgr.SetBlock(support, block.ToStateID[block.Dirt{}], dimMinY)
	mgr.SetBlock(abovePos, block.ToStateID[block.Stone{}], dimMinY)

	beforeItems := countItemEntities(loop)
	breakSupport(loop, support)

	// The stone above is NOT vegetation: it must remain untouched.
	if got, ok := mgr.GetBlock(abovePos, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("stone above a broken block = (state %d, ok=%v), want UNCHANGED (not vegetation)", got, ok)
	}
	if got := countItemEntities(loop); got != beforeItems {
		t.Fatalf("item entities = %d, want %d (no spurious destroy/drop)", got, beforeItems)
	}
}

// TestStillSupportedVegetationSurvives: an unrelated break NEXT TO a flower (not its support) leaves
// the flower intact — the survival check only destroys when the support directly below is removed.
func TestStillSupportedVegetationSurvives(t *testing.T) {
	loop, mgr := newDropLoop()
	_ = blockPlayer(loop, 1.5, 65.0, 1.5)

	support := pk.Position{X: 1, Y: 64, Z: 1}
	flowerPos := pk.Position{X: 1, Y: 65, Z: 1}
	// An UNRELATED block one cell to the east, broken instead of the flower's own support.
	unrelated := pk.Position{X: 2, Y: 64, Z: 1}
	mgr.SetBlock(support, block.ToStateID[block.Dirt{}], dimMinY)
	mgr.SetBlock(flowerPos, block.ToStateID[block.Poppy{}], dimMinY)
	mgr.SetBlock(unrelated, block.ToStateID[block.Stone{}], dimMinY)

	beforeItems := countItemEntities(loop)
	breakSupport(loop, unrelated)

	// The flower's own support (1,64,1) is intact, so the poppy survives.
	if got, ok := mgr.GetBlock(flowerPos, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("poppy at %v = (state %d, ok=%v), want STILL-POPPY (its support was not broken)", flowerPos, got, ok)
	}
	if got := countItemEntities(loop); got != beforeItems {
		t.Fatalf("item entities = %d, want %d (no destroy for an unrelated break)", got, beforeItems)
	}
}

// TestBreakUnderFlowerViaFullDigPath: the full survival-dig break path (START -> STOP through
// reconcileEdit) also triggers the vegetation survival check — proving the wiring is in reconcileEdit
// (so both place + break paths are covered), not only the direct helper.
func TestBreakUnderFlowerViaFullDigPath(t *testing.T) {
	loop, mgr := newDropLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.entityID = 1000

	support := pk.Position{X: 1, Y: 64, Z: 1}
	flowerPos := pk.Position{X: 1, Y: 65, Z: 1}
	mgr.SetBlock(support, block.ToStateID[block.Dirt{}], dimMinY)
	mgr.SetBlock(flowerPos, block.ToStateID[block.Dandelion{}], dimMinY)

	// Break the SUPPORT (dirt) via the real dig path; reconcileEdit then runs updateVegetationOnEdit.
	completeSurvivalDig(loop, p, support)

	if got, ok := mgr.GetBlock(flowerPos, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("dandelion at %v = (state %d, ok=%v), want AIR after the full dig break of its support", flowerPos, got, ok)
	}
}
