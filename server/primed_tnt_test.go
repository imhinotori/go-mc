package server

// primed_tnt_test.go — verifies the PRIMED TNT port (primed_tnt.go) + the TntBlock ignite seams:
//   1. flint&steel used ON a placed TNT block PRIMES it (a PrimedTnt entity spawns, the block is removed).
//   2. the PrimedTnt counts its fuse down from 80 and, at 0, DETONATES (calls explode radius 4.0 — asserted
//      via the entity's removal + a nearby destructible block turned to air by the radius-4 blast).
//   3. a redstone-powered TNT block primes (neighborChanged: hasNeighborSignal -> prime + removeBlock).
//   4. an explosion CHAIN-primes a neighboring TNT block with the random SHORT fuse (getRandomShortFuse:
//      nextInt(20)+10 for the default fuse 80 -> a 10..29 tick fuse), asserted against the jar formula.
//
// The fuse 80 + radius 4.0 constants are the jar-verified values (PrimedTnt.DEFAULT_FUSE_TIME=80,
// DEFAULT_EXPLOSION_POWER=4.0F); the tests assert them directly.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// tntStateID is the placed TNT block's default state id.
func tntStateID() block.StateID { return block.DefaultStateID["minecraft:tnt"] }

// countPrimedTnt returns how many PrimedTnt entities live in the region's store.
func countPrimedTnt(loop *TickLoop) (n int, first *Entity) {
	for _, r := range loop.regions {
		if r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if e.isTnt {
				n++
				if first == nil {
					first = e
				}
			}
		}
	}
	return n, first
}

// TestFlintAndSteelPrimesTntBlock: a UseItemOn while HOLDING flint&steel, on a loaded reachable TNT block,
// PRIMES the TNT — a PrimedTnt entity spawns (fuse 80) and the source TNT block is removed (set to air).
// Ports TntBlock.useItemOn: (stack.is(FLINT_AND_STEEL)) && prime -> setBlock(AIR, 11).
func TestFlintAndSteelPrimesTntBlock(t *testing.T) {
	loop, mgr := newBlockLoop()
	// A seeded region levelRandom so the ctor's nextDouble() pop draw is deterministic (any seed).
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(777)

	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	setHeldItem(p, item.FlintAndSteel.ID, 1)

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, tntStateID(), dimMinY)

	ui := useItemOnPacket(0 /*main hand*/, target, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 5)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// The TNT block is removed (now air).
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("after flint&steel on TNT, GetBlock = (%v, ok=%v), want air (block must be removed)", got, ok)
	}
	// Exactly one PrimedTnt spawned, with the default fuse 80 (DEFAULT_FUSE_TIME) and radius 4.0.
	n, tnt := countPrimedTnt(loop)
	if n != 1 || tnt == nil {
		t.Fatalf("PrimedTnt count = %d, want exactly 1", n)
	}
	if tnt.tntFuse != 80 {
		t.Fatalf("primed fuse = %d, want 80 (PrimedTnt.DEFAULT_FUSE_TIME)", tnt.tntFuse)
	}
	if tnt.tntExplosionPower != 4.0 {
		t.Fatalf("explosion power = %v, want 4.0 (DEFAULT_EXPLOSION_POWER)", tnt.tntExplosionPower)
	}
	// Spawned centered on the block (x+0.5, z+0.5) at the block's y.
	if tnt.x != 1.5 || tnt.y != 64.0 || tnt.z != 1.5 {
		t.Fatalf("primed pos = (%v,%v,%v), want (1.5,64,1.5) (block-centered)", tnt.x, tnt.y, tnt.z)
	}
}

// TestPrimedTntCountsDownAndExplodes: a PrimedTnt with fuse 80 ticks down one per tickPrimedTnt call and,
// at 0, discards itself and detonates (explode radius 4.0) — asserted by the entity's removal AND a nearby
// destructible block (dirt, resistance 0.5) turned to air by the radius-4 blast.
func TestPrimedTntCountsDownAndExplodes(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0})
	// Deterministic RNG for the ctor pop + the blast rays.
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(12345)

	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	const cx, cy, cz = 8, 70, 8
	// A stone floor one cell below the spawn so the TNT settles at cy instead of free-falling for 80
	// ticks (a fall would carry the blast center far from the victim). The TNT rests ON this floor.
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			mgr.SetBlock(pk.Position{X: cx + dx, Y: cy - 1, Z: cz + dz}, block.ToStateID[block.Stone{}], dimMinY)
		}
	}
	// A dirt block (resistance 0.5) right next to the blast center at the TNT's rest height: the radius-4
	// TNT blast must destroy it.
	victim := pk.Position{X: cx + 1, Y: cy, Z: cz}
	mgr.SetBlock(victim, block.ToStateID[block.Dirt{}], dimMinY)

	var tnt *Entity
	loop.withRegion(loop.only(), func() {
		tnt = loop.spawnPrimedTnt(float64(cx)+0.5, float64(cy), float64(cz)+0.5, 80)
	})
	if tnt == nil || tnt.tntFuse != 80 {
		t.Fatalf("spawn: got %+v, want a PrimedTnt with fuse 80", tnt)
	}
	id := tnt.id

	// Drive the primed-tnt tick 80 times; it should detonate on the 80th (fuse 80 -> ... -> 0).
	exploded := false
	for i := 0; i < 80; i++ {
		loop.tickPrimedTnt()
		if _, ok := loop.only().entities.byID[id]; !ok {
			exploded = true
			// It detonated this tick; the block must now be air.
			if got, ok := mgr.GetBlock(victim, dimMinY); !ok || !block.IsAir(got) {
				t.Fatalf("after detonation (tick %d), victim block = (%v, ok=%v), want air (radius-4 blast)", i+1, got, ok)
			}
			break
		}
	}
	if !exploded {
		t.Fatal("PrimedTnt never detonated within 80 ticks (fuse 80 must reach 0 and explode)")
	}
}

// TestRedstonePrimesTntBlock: a redstone_block placed adjacent to a TNT block powers it; the redstone
// neighborChanged reaction (hasNeighborSignal -> prime + removeBlock) spawns a PrimedTnt and removes the
// block. Ports TntBlock.neighborChanged.
func TestRedstonePrimesTntBlock(t *testing.T) {
	loop, mgr := newRedstoneLoop()
	// Seed the region levelRandom for the ctor pop draw (deterministic).
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(999)

	tntPos := pk.Position{X: 5, Y: 64, Z: 5}
	mgr.SetBlock(tntPos, tntStateID(), dimMinY)

	// A redstone_block (constant-15 source) directly next to the TNT: it powers the TNT cell.
	powerPos := pk.Position{X: 6, Y: 64, Z: 5}
	mgr.SetBlock(powerPos, block.ToStateID[block.RedstoneBlock{}], dimMinY)

	// Fire the redstone edit at the power source (Level.updateNeighborsAt): the TNT neighbor reacts.
	loop.withRegion(loop.only(), func() {
		loop.onRedstoneEdit(powerPos)
	})

	if got, ok := mgr.GetBlock(tntPos, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("after redstone power, TNT block = (%v, ok=%v), want air (primed + removed)", got, ok)
	}
	n, tnt := countPrimedTnt(loop)
	if n != 1 || tnt == nil {
		t.Fatalf("redstone-primed PrimedTnt count = %d, want 1", n)
	}
	if tnt.tntFuse != 80 {
		t.Fatalf("redstone-primed fuse = %d, want 80", tnt.tntFuse)
	}
}

// TestExplosionChainPrimesNeighborTnt: an explosion whose destroyed set includes a TNT block CHAIN-primes
// it — the wasExploded seam spawns a PrimedTnt with the random SHORT fuse getRandomShortFuse(80, rng) =
// rng.nextInt(20) + 10 (a 10..29 tick fuse). Asserts the fuse lands in that jar-derived range.
func TestExplosionChainPrimesNeighborTnt(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0})
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(54321)

	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	const cx, cy, cz = 8, 70, 8
	// A TNT block adjacent to the blast center: the radius-4 blast reaches it, and interactWithBlocks'
	// wasExploded seam chain-primes it instead of merely dropping it.
	tntPos := pk.Position{X: cx + 1, Y: cy, Z: cz}
	mgr.SetBlock(tntPos, tntStateID(), dimMinY)

	loop.withRegion(loop.only(), func() {
		// A radius-4 TNT-style blast at the center (mobGriefing on so interactWithBlocks runs).
		loop.explode(-1, float64(cx)+0.5, float64(cy)+0.5, float64(cz)+0.5, 4.0)
	})

	// The TNT block was consumed by the blast; a PrimedTnt should have been chain-spawned.
	n, tnt := countPrimedTnt(loop)
	if n < 1 || tnt == nil {
		t.Fatalf("chain-primed PrimedTnt count = %d, want >= 1 (the neighbor TNT must prime)", n)
	}
	// getRandomShortFuse(80, rng) = rng.nextInt(max(1,80/4)) + 80/8 = nextInt(20) + 10 -> 10..29.
	if tnt.tntFuse < 10 || tnt.tntFuse > 29 {
		t.Fatalf("chain-primed fuse = %d, want in [10,29] (getRandomShortFuse: nextInt(20)+10)", tnt.tntFuse)
	}
}

// TestTntGetRandomShortFuseFormula locks the jar formula getRandomShortFuse(fuse, rng) =
// rng.nextInt(max(1, fuse/4)) + fuse/8 for the default fuse 80 (== nextInt(20) + 10).
func TestTntGetRandomShortFuseFormula(t *testing.T) {
	// A seeded LCG; replay it independently to predict nextInt(20).
	r := levelgen.NewLegacyRandomSource(2024)
	pred := levelgen.NewLegacyRandomSource(2024)
	for i := 0; i < 50; i++ {
		got := tntGetRandomShortFuse(80, r)
		want := int(pred.NextIntN(20)) + 10
		if got != want {
			t.Fatalf("getRandomShortFuse(80) draw %d = %d, want %d (nextInt(20)+10)", i, got, want)
		}
		if got < 10 || got > 29 {
			t.Fatalf("getRandomShortFuse(80) = %d out of [10,29]", got)
		}
	}
}
