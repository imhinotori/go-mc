package server

// explosion_block_test.go — verifies the BLOCK-DESTRUCTION half of the ServerExplosion port
// (explosion_blocks.go): the resistance-weighted toBlow collection + the MOB_GRIEFING gate.
//
// The RNG is made deterministic by reseeding the (fallback) region's levelRandom before each
// explode, exactly like TestPickNaturalCreatureMob reseeds it — so the 16^3 ray nextFloats +
// the Util.shuffle nextInts replay identically.

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// fillStoneCube lays solid stone in the world cube [x0,x1]*[y0,y1]*[z0,z1] (inclusive), the
// blast medium the ray-collection eats through.
func fillStoneCube(mgr interface {
	SetBlock(pk.Position, block.StateID, int) bool
}, x0, x1, y0, y1, z0, z1 int) {
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for z := z0; z <= z1; z++ {
				mgr.SetBlock(pk.Position{X: x, Y: y, Z: z}, block.ToStateID[block.Stone{}], dimMinY)
			}
		}
	}
}

// TestExplosionDestroysBlocks: a radius-3 (creeper) blast in a solid stone volume removes stone
// near the center (the resistance-attenuated rays overcome stone's 6.0 resistance for the first
// cells) while a high-resistance block (obsidian, 1200.0) at the center survives, and stone far
// outside the blast radius survives. With mobGriefing=false the SAME blast removes NOTHING (the
// ExplosionInteraction.MOB -> KEEP path).
func TestExplosionDestroysBlocks(t *testing.T) {
	// The blast center (block-integer coords chosen inside chunk 0,0's column at a mid height).
	const cx, cy, cz = 8, 70, 8

	run := func(t *testing.T, griefing bool) map[pk.Position]bool {
		loop, mgr := newPhysicsLoop()
		putChunk(mgr, level.ChunkPos{0, 0})
		// A 7x7x7 solid stone cube centered on the blast (radius*2+1 spans well past the blast)...
		fillStoneCube(mgr, cx-3, cx+3, cy-3, cy+3, cz-3, cz+3)
		// ...but clear the center cell so the blast originates in air (a creeper in the open), letting
		// the rays travel a step before hitting the stone shell (a ray that starts INSIDE obsidian dies
		// on cell 0 and destroys nothing — the vanilla behavior, but not what this test probes).
		center := pk.Position{X: cx, Y: cy, Z: cz}
		mgr.SetBlock(center, block.DefaultStateID["minecraft:air"], dimMinY)
		// Obsidian one cell +x from the center: resistance 1200 -> the +x ray dies on it (never added to
		// toBlow), so it must survive every blast (and it shields the cells behind it).
		obs := pk.Position{X: cx + 1, Y: cy, Z: cz}
		mgr.SetBlock(obs, block.ToStateID[block.Obsidian{}], dimMinY)
		// A far stone block well beyond radius*2 (=6): it must always survive.
		far := pk.Position{X: cx + 6, Y: cy, Z: cz}
		mgr.SetBlock(far, block.ToStateID[block.Stone{}], dimMinY)

		clock := loop.clock.(*fakeClock)
		loop.start(clock.Now())

		// Deterministic RNG: reseed the fallback region's levelRandom.
		loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(12345)
		mobGriefing = griefing
		defer func() { mobGriefing = true }() // restore the vanilla default for other tests.

		loop.withRegion(loop.only(), func() {
			// Blast at the block center (+0.5 so BlockPos.containing lands on the center block).
			loop.explode(-1, float64(cx)+0.5, float64(cy)+0.5, float64(cz)+0.5, 3.0)
		})

		// Snapshot which cells of the cube are now air.
		removed := map[pk.Position]bool{}
		for x := cx - 3; x <= cx+3; x++ {
			for y := cy - 3; y <= cy+3; y++ {
				for z := cz - 3; z <= cz+3; z++ {
					p := pk.Position{X: x, Y: y, Z: z}
					if p == center {
						continue // the center was pre-cleared to air (blast origin), not a removal.
					}
					st, ok := mgr.GetBlock(p, dimMinY)
					if ok && block.IsAir(st) {
						removed[p] = true
					}
				}
			}
		}

		// Invariants that hold for BOTH gamerule settings.
		if st, _ := mgr.GetBlock(obs, dimMinY); block.IsAir(st) {
			t.Fatalf("griefing=%v: obsidian at %v was destroyed (resistance 1200 must survive)", griefing, obs)
		}
		if st, _ := mgr.GetBlock(far, dimMinY); block.IsAir(st) {
			t.Fatalf("griefing=%v: stone at %v (beyond radius*2) was destroyed", griefing, far)
		}
		return removed
	}

	// mobGriefing=true: the blast removes a resistance-weighted set of stone near the center.
	removed := run(t, true)
	if len(removed) == 0 {
		t.Fatal("griefing=true: the blast removed no blocks (expected the resistance-weighted stone shell)")
	}
	// The removed set must be a strict subset near the center: every removed cell is within the
	// blast reach (|d| <= 3 by construction of the cube) — a sanity bound on the pattern.
	for p := range removed {
		dx, dy, dz := p.X-cx, p.Y-cy, p.Z-cz
		if dx*dx+dy*dy+dz*dz > 3*3+3*3+3*3 {
			t.Fatalf("griefing=true: removed a cell %v outside the cube reach", p)
		}
	}
	// At least one immediate neighbor of the center is removed (stone res 6 -> the first cell of a
	// radius-3 blast is overcome), proving the resistance-weighted collection actually destroys.
	adj := []pk.Position{
		{X: cx - 1, Y: cy, Z: cz}, // (+x is obsidian, excluded)
		{X: cx, Y: cy + 1, Z: cz}, {X: cx, Y: cy - 1, Z: cz},
		{X: cx, Y: cy, Z: cz + 1}, {X: cx, Y: cy, Z: cz - 1},
	}
	anyAdj := false
	for _, p := range adj {
		if removed[p] {
			anyAdj = true
			break
		}
	}
	if !anyAdj {
		t.Fatalf("griefing=true: no stone adjacent to the center was destroyed (removed=%d cells)", len(removed))
	}

	// mobGriefing=false: the SAME blast (same seed) removes NOTHING (the KEEP path).
	removedOff := run(t, false)
	if len(removedOff) != 0 {
		t.Fatalf("griefing=false: the blast removed %d blocks (MOB_GRIEFING off must destroy nothing)", len(removedOff))
	}
}

// TestExplosionResistanceTable spot-checks the codegen'd resistance table against the jar values
// (ExplosionDamageCalculator.getBlockExplosionResistance reads Block.getExplosionResistance).
func TestExplosionResistanceTable(t *testing.T) {
	cases := map[string]float32{
		"minecraft:stone":       6.0,
		"minecraft:dirt":        0.5,
		"minecraft:grass_block": 0.6,
		"minecraft:obsidian":    1200.0,
		"minecraft:bedrock":     3600000.0,
		"minecraft:oak_log":     2.0,
		"minecraft:cobblestone": 6.0,
	}
	for id, want := range cases {
		if got := block.ExplosionResistance[id]; got != want {
			t.Errorf("ExplosionResistance[%q] = %v, want %v", id, got, want)
		}
	}
	// A waterlogged/water block resistances via the fluid max (WaterFluid == 100.0f).
	if got := block.StateExplosionResistance(block.ToStateID[block.Water{Level: 0}]); got != 100.0 {
		t.Errorf("StateExplosionResistance(water) = %v, want 100 (WaterFluid.getExplosionResistance)", got)
	}
}
