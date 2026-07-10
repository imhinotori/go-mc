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
		loop.gamerules = newGameRules()
		loop.gamerules.setBool(ruleMobGriefing, griefing)

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

// TestExplosionInteractionGateBlocksVsMob proves the interactsWithBlocks fix: a TNT- and BLOCK-
// interaction explosion destroys terrain EVEN WITH mobGriefing OFF (those interactions map to
// DESTROY_WITH_DECAY unconditionally), whereas the MOB interaction (a creeper) destroys NOTHING
// with mobGriefing off (the KEEP path). Before the fix explodeWith hardcoded the MOB gate for all
// callers, so a TNT blast wrongly spared terrain when mobGriefing was off. Cite ServerLevel.explode
// (BLOCK/TNT -> DESTROY_WITH_DECAY; MOB -> mobGriefing ? DESTROY_WITH_DECAY : KEEP).
func TestExplosionInteractionGateBlocksVsMob(t *testing.T) {
	const cx, cy, cz = 8, 70, 8

	run := func(interaction explosionInteraction) int {
		loop, mgr := newPhysicsLoop()
		putChunk(mgr, level.ChunkPos{0, 0})
		fillStoneCube(mgr, cx-3, cx+3, cy-3, cy+3, cz-3, cz+3)
		mgr.SetBlock(pk.Position{X: cx, Y: cy, Z: cz}, block.DefaultStateID["minecraft:air"], dimMinY)

		clock := loop.clock.(*fakeClock)
		loop.start(clock.Now())
		loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(12345)
		loop.gamerules = newGameRules()
		loop.gamerules.setBool(ruleMobGriefing, false) // mobGriefing OFF for every variant

		loop.withRegion(loop.only(), func() {
			loop.explodeWith(-1, float64(cx)+0.5, float64(cy)+0.5, float64(cz)+0.5, 3.0, interaction, false)
		})

		removed := 0
		for x := cx - 3; x <= cx+3; x++ {
			for y := cy - 3; y <= cy+3; y++ {
				for z := cz - 3; z <= cz+3; z++ {
					p := pk.Position{X: x, Y: y, Z: z}
					if p == (pk.Position{X: cx, Y: cy, Z: cz}) {
						continue
					}
					if st, ok := mgr.GetBlock(p, dimMinY); ok && block.IsAir(st) {
						removed++
					}
				}
			}
		}
		return removed
	}

	if got := run(explosionInteractionTNT); got == 0 {
		t.Fatal("TNT interaction with mobGriefing OFF destroyed nothing (should destroy -- DESTROY_WITH_DECAY)")
	}
	if got := run(explosionInteractionBlock); got == 0 {
		t.Fatal("BLOCK interaction with mobGriefing OFF destroyed nothing (should destroy -- DESTROY_WITH_DECAY)")
	}
	if got := run(explosionInteractionMob); got != 0 {
		t.Fatalf("MOB interaction with mobGriefing OFF destroyed %d blocks (should be KEEP -- 0)", got)
	}
	if got := run(explosionInteractionNone); got != 0 {
		t.Fatalf("NONE interaction destroyed %d blocks (should be KEEP -- 0)", got)
	}
}

// TestExplosionCreateFire proves ServerExplosion.createFire: with fire=true, a blast over an air cell
// that sits on a solid floor places a fire block (nextInt(3)==0 gate). We stack the deck so a fire
// site exists: solid stone floor with an air cell above it inside the blast, then assert at least one
// fire block appears. With fire=false NO fire is ever placed. Cite ServerExplosion.createFire.
func TestExplosionCreateFire(t *testing.T) {
	const cx, cy, cz = 8, 70, 8

	run := func(fire bool) int {
		loop, mgr := newPhysicsLoop()
		putChunk(mgr, level.ChunkPos{0, 0})
		// A solid stone FLOOR at cy-1 across the blast footprint; air above it (the fire sites).
		for x := cx - 3; x <= cx+3; x++ {
			for z := cz - 3; z <= cz+3; z++ {
				mgr.SetBlock(pk.Position{X: x, Y: cy - 1, Z: z}, block.ToStateID[block.Stone{}], dimMinY)
			}
		}
		clock := loop.clock.(*fakeClock)
		loop.start(clock.Now())
		loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(999)
		loop.gamerules = newGameRules()

		loop.withRegion(loop.only(), func() {
			// A radius-1 BLOCK blast (small footprint) with the fire flag; the toBlow air cells over the
			// stone floor are the fire candidates.
			loop.explodeWith(-1, float64(cx)+0.5, float64(cy)+0.5, float64(cz)+0.5, 3.0, explosionInteractionBlock, fire)
		})

		fires := 0
		for x := cx - 4; x <= cx+4; x++ {
			for y := cy - 1; y <= cy+4; y++ {
				for z := cz - 4; z <= cz+4; z++ {
					if st, ok := mgr.GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinY); ok {
						if int(st) >= 0 && int(st) < len(block.StateList) && block.StateList[st].ID() == "minecraft:fire" {
							fires++
						}
					}
				}
			}
		}
		return fires
	}

	if got := run(true); got == 0 {
		t.Fatal("fire=true blast placed no fire (createFire should light at least one air-over-solid cell)")
	}
	if got := run(false); got != 0 {
		t.Fatalf("fire=false blast placed %d fire blocks (createFire must not run)", got)
	}
}
