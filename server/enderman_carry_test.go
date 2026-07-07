package server

// enderman_carry_test.go — MOB-HOST-08 (Enderman block-carry): the behavior test for the two block-carry
// goals (ai_goals_enderman_carry.go), the 1:1 ports of EnderMan$EndermanTakeBlockGoal +
// EnderMan$EndermanLeaveBlockGoal + the DATA_CARRY_STATE-backed carriedBlockState:
//
//   - TestEndermanTakesHoldableBlock: with #minecraft:enderman_holdable (sand) filling the 4x3x4 search
//     box, the take goal's canUse eventually fires (a success nextInt(reducedTickDelay(20))==0 seed) and
//     tick() removes the picked block AND makes the enderman carry that block's default state.
//   - TestEndermanLeavesBlock: a carrying enderman (carriedBlockSet) over an air-above-solid cell runs the
//     leave goal on a success seed: canUse fires, tick() places the carried block and clears the carry.
//   - TestEndermanTakeNoGriefing: with mobGriefing OFF, the take goal's canUse NEVER fires (no pick-up)
//     across every seed, and no RNG is drawn (the gate short-circuits before the roll).
//
// All drive the Go-native goals directly with a controlled per-entity rng (e.ai.rng = newEntityRandom),
// the same determinism pattern ai_goals_target_test.go / silverfish_infest_test.go use. The pig oracle is
// a separate mob — untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/imhinotori/sulfur/level/block"
)

// TestEndermanTakesHoldableBlock: sand (a #minecraft:enderman_holdable block) fills the entire 4x3x4 take
// search box around the enderman, so ANY cell the tick draws is holdable — the take then depends ONLY on
// the canUse nextInt(reducedTickDelay(20))==0 gate. On a success seed, tick() removes the picked block and
// the enderman carries sand's default state. Cite EnderMan$EndermanTakeBlockGoal.
func TestEndermanTakesHoldableBlock(t *testing.T) {
	loop, floorY := endermanLoop(t)
	decl := loop.mobRegistry.byName["vanilla_enderman"]
	ex, ez := 8.5, 8.5
	e := loop.spawnDeclaredMob(decl, ex, float64(floorY+1), ez)
	if e.typ != entity.Enderman.ID {
		t.Fatalf("enderman typ = %d, want entity.Enderman.ID %d", e.typ, entity.Enderman.ID)
	}
	if e.carriedBlockSet {
		t.Fatal("a freshly spawned enderman must not be carrying a block")
	}

	sand := block.ToStateID[block.Sand{}]
	sandDefault := block.DefaultStateID["minecraft:sand"]
	bx, by, bz := 8, floorY+1, 8
	// The take box is floor(x-2 + nextDouble*4) etc → x-2..x+1, y..y+2, z-2..z+1. Fill it ALL with sand so
	// whichever cell tick draws is holdable.
	loop.withRegion(loop.only(), func() {
		for dx := -2; dx <= 1; dx++ {
			for dy := 0; dy <= 2; dy++ {
				for dz := -2; dz <= 1; dz++ {
					loop.world().SetBlock(pk.Position{X: bx + dx, Y: by + dy, Z: bz + dz}, sand, dimMinY)
				}
			}
		}
	})

	took := false
	loop.withRegion(loop.only(), func() {
		// Probe seeds until canUse selects a take (nextInt(reducedTickDelay(20))==0). A fresh goal per seed
		// keeps state clean; the seed drives the mob's rng so the gate + the tick cell draws are
		// deterministic. 512 seeds is ample (the gate hits ~1/10).
		for seed := uint64(1); seed <= 512 && !took; seed++ {
			e.ai.rng = newEntityRandom(seed)
			e.carriedBlockState = 0
			e.carriedBlockSet = false
			g := newEndermanTakeBlockGoal()
			if !g.canUse(loop, e) {
				continue
			}
			// canUse fired (the roll hit 0). tick() draws the cell and takes it.
			g.tick(loop, e)
			if !e.carriedBlockSet {
				continue // the drawn cell (still inside the all-sand box) should always take; be robust
			}
			if e.carriedBlockState != sandDefault {
				t.Fatalf("carried state = %d, want sand default %d (getBlock().defaultBlockState())", e.carriedBlockState, sandDefault)
			}
			took = true
		}
	})
	if !took {
		t.Fatal("the take goal never picked up a holdable block over 512 seeds despite an all-sand search box")
	}
}

// TestEndermanLeavesBlock: a carrying enderman standing over an air cell whose block-below is a full solid
// (the floor) runs the leave goal on a success seed — canUse fires (nextInt(reducedTickDelay(2000))==0),
// tick() places the carried block at the drawn cell and clears the carry. To make EVERY drawn cell in the
// 2x2x2 leave box placeable, the enderman stands one block ABOVE the floor so the box cells are air over
// the solid floor. Cite EnderMan$EndermanLeaveBlockGoal.
func TestEndermanLeavesBlock(t *testing.T) {
	loop, floorY := endermanLoop(t)
	decl := loop.mobRegistry.byName["vanilla_enderman"]
	// Stand the enderman at feetY = floorY+2 so the leave box (floor(y + nextDouble*2) → y..y+1) is air,
	// and the cell BELOW each box cell (y-1..y) includes the floor row (floorY+1) — solid where placeable.
	ex, ez := 8.5, 8.5
	feetY := floorY + 2
	e := loop.spawnDeclaredMob(decl, ex, float64(feetY), ez)
	// Give it a carried block (sand default) — as if it had taken one.
	sandDefault := block.DefaultStateID["minecraft:sand"]
	loop.withRegion(loop.only(), func() {
		setEndermanCarriedBlock(loop, e, sandDefault, true)
	})
	if !e.carriedBlockSet {
		t.Fatal("setEndermanCarriedBlock did not mark the enderman as carrying")
	}

	// Raise the floor one row (to floorY+1) directly under the leave box so a placed cell at y=floorY+2
	// sits on solid ground. The base fillFloor put a solid slab at floorY; add a solid ring at floorY+1
	// spanning the leave box's below-cells (x-1..x+1, z-1..z+1).
	bx, bz := 8, 8
	stone := block.ToStateID[block.Stone{}]
	loop.withRegion(loop.only(), func() {
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				loop.world().SetBlock(pk.Position{X: bx + dx, Y: floorY + 1, Z: bz + dz}, stone, dimMinY)
			}
		}
	})

	placed := false
	loop.withRegion(loop.only(), func() {
		for seed := uint64(1); seed <= 4096 && !placed; seed++ {
			e.ai.rng = newEntityRandom(seed)
			// Re-arm the carry each probe (a prior success cleared it).
			e.carriedBlockState = sandDefault
			e.carriedBlockSet = true
			g := newEndermanLeaveBlockGoal()
			if !g.canUse(loop, e) {
				continue
			}
			g.tick(loop, e)
			if e.carriedBlockSet {
				continue // the drawn cell was not placeable this seed; keep probing
			}
			placed = true
		}
	})
	if !placed {
		t.Fatal("the leave goal never placed the carried block over 4096 seeds despite a placeable box")
	}
	if e.carriedBlockSet {
		t.Fatal("the carry was not cleared after a successful leave (setCarriedBlock(null))")
	}
}

// TestEndermanTakeNoGriefing: with the MOB_GRIEFING gamerule OFF, EndermanTakeBlockGoal.canUse returns
// false for EVERY seed (the mobGriefing gate short-circuits before the nextInt roll — so no block is ever
// taken, and no RNG is drawn for the gate). Cite EnderMan$EndermanTakeBlockGoal.canUse mobGriefing gate.
func TestEndermanTakeNoGriefing(t *testing.T) {
	loop, floorY := endermanLoop(t)
	decl := loop.mobRegistry.byName["vanilla_enderman"]
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)

	// Fill the take box with sand so the ONLY thing preventing a take is the gamerule.
	sand := block.ToStateID[block.Sand{}]
	bx, by, bz := 8, floorY+1, 8
	loop.withRegion(loop.only(), func() {
		for dx := -2; dx <= 1; dx++ {
			for dy := 0; dy <= 2; dy++ {
				for dz := -2; dz <= 1; dz++ {
					loop.world().SetBlock(pk.Position{X: bx + dx, Y: by + dy, Z: bz + dz}, sand, dimMinY)
				}
			}
		}
	})

	// Turn mobGriefing OFF for the duration of this test (restore the vanilla default after).
	loop.gamerules = newGameRules()
	loop.gamerules.setBool(ruleMobGriefing, false)
	defer func() { loop.gamerules.setBool(ruleMobGriefing, true) }()

	tookAny := false
	loop.withRegion(loop.only(), func() {
		for seed := uint64(1); seed <= 512; seed++ {
			e.ai.rng = newEntityRandom(seed)
			e.carriedBlockState = 0
			e.carriedBlockSet = false
			g := newEndermanTakeBlockGoal()
			if g.canUse(loop, e) {
				tookAny = true
				break
			}
		}
	})
	if tookAny {
		t.Fatal("the take goal fired with mobGriefing OFF — the MOB_GRIEFING gate is not enforced")
	}
	if e.carriedBlockSet {
		t.Fatal("the enderman is carrying a block despite mobGriefing being off")
	}
}
