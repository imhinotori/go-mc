package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// conduit_test.go — CONDUIT block-entity validation gates. Each asserts the ported behaviour against the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, net.minecraft.world.level.block.entity.ConduitBlockEntity):
//   - a conduit whose 3x3x3 pocket is all water and whose -2..2 frame has >=16 prismarine blocks is ACTIVE
//     and grants CONDUIT_POWER (duration 260, amplifier 0) to a submerged player within (size/7*16) blocks;
//   - a conduit whose frame has <16 blocks is INACTIVE (no effect);
//   - a conduit whose pocket is not all-water is INACTIVE;
//   - a FULL 42-block frame attacks a hostile (in water, within 8) for 4.0 magic damage.

// conduitFrameCells is ConduitBlockEntity.updateShape's exact set of 42 frame-position offsets (the -2..2
// shell cells that survive the offset predicate). Computed from the same predicate the port uses; the count
// is exactly MIN_KILL_SIZE (42), the full frame. The test fills a prefix of this list to reach a target size.
var conduitFrameCells = func() [][3]int {
	var cells [][3]int
	for ox := -2; ox <= 2; ox++ {
		for oy := -2; oy <= 2; oy++ {
			for oz := -2; oz <= 2; oz++ {
				ax, ay, az := absInt(ox), absInt(oy), absInt(oz)
				skip := (ax <= 1 && ay <= 1 && az <= 1) ||
					((ox != 0 || (ay != 2 && az != 2)) &&
						(oy != 0 || (ax != 2 && az != 2)) &&
						(oz != 0 || (ax != 2 && ay != 2)))
				if skip {
					continue
				}
				cells = append(cells, [3]int{ox, oy, oz})
			}
		}
	}
	return cells
}()

// newConduitLoop wires a TickLoop with ready all-air chunks around the origin + a block-tick container.
// Mirrors newBeaconLoop. gametime starts at 0 so the first tickConduits is a 40-tick scan boundary.
func newConduitLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	for _, cp := range []level.ChunkPos{{0, 0}, {-1, 0}, {0, -1}, {-1, -1}, {1, 0}, {0, 1}, {1, 1}, {-1, 1}, {1, -1}} {
		ch := level.EmptyChunk(blockTestSecs)
		ch.Status = level.StatusFull
		mgr.Insert(cp, ch)
	}
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

func conduitBlockState() block.StateID { return block.DefaultStateID["minecraft:conduit"] }
func waterBlockState() block.StateID   { return block.DefaultStateID["minecraft:water"] }
func prismarineState() block.StateID   { return block.DefaultStateID["minecraft:prismarine"] }

// floodConduitPocket fills the 27-cell 3x3x3 water pocket around pos with water (the updateShape all-water
// requirement) and places the conduit block at pos.
func floodConduitPocket(mgr *world.ChunkManager, pos pk.Position) {
	water := waterBlockState()
	for ox := -1; ox <= 1; ox++ {
		for oy := -1; oy <= 1; oy++ {
			for oz := -1; oz <= 1; oz++ {
				mgr.SetBlock(pk.Position{X: pos.X + ox, Y: pos.Y + oy, Z: pos.Z + oz}, water, dimMinY)
			}
		}
	}
	mgr.SetBlock(pos, conduitBlockState(), dimMinY)
}

// placeConduitFrame places the first n frame-cell blocks (prismarine) from conduitFrameCells around pos.
func placeConduitFrame(mgr *world.ChunkManager, pos pk.Position, n int) {
	pris := prismarineState()
	for i := 0; i < n && i < len(conduitFrameCells); i++ {
		c := conduitFrameCells[i]
		mgr.SetBlock(pk.Position{X: pos.X + c[0], Y: pos.Y + c[1], Z: pos.Z + c[2]}, pris, dimMinY)
	}
}

// TestConduitFrameCellCountIs42 locks the ported offset predicate: exactly 42 frame positions survive it —
// MIN_KILL_SIZE, the full conduit frame. A drift in the predicate would change this count.
func TestConduitFrameCellCountIs42(t *testing.T) {
	if got := len(conduitFrameCells); got != conduitMinKillSize {
		t.Fatalf("conduit frame cell count = %d, want %d (MIN_KILL_SIZE, the full frame)", got, conduitMinKillSize)
	}
}

// TestConduitActiveAppliesConduitPower is the load-bearing gate: a conduit with an all-water pocket and a
// >=16-block frame is active and grants CONDUIT_POWER (duration 260, amplifier 0) to a submerged player
// within (size/7*16) blocks on a 40-tick boundary.
func TestConduitActiveAppliesConduitPower(t *testing.T) {
	loop, mgr := newConduitLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	floodConduitPocket(mgr, pos)
	placeConduitFrame(mgr, pos, 16) // exactly MIN_ACTIVE_SIZE -> active

	c := loop.resolveConduit(pos)

	// A submerged player 2 blocks away, in water (so isInWaterOrRain is true).
	p := &tickPlayer{x: 2, y: 64, z: 0, entityID: 1}
	loop.players = append(loop.players, p)
	// Flood the player's cell so playerInWater is true.
	mgr.SetBlock(pk.Position{X: 2, Y: 64, Z: 0}, waterBlockState(), dimMinY)

	// Land on a 40-tick boundary and tick once.
	loop.gametime = 40
	loop.tickConduits()

	if !c.isActive {
		t.Fatalf("a conduit with an all-water pocket + 16 frame blocks is not active")
	}
	e := p.activeEffects[effectConduitPower]
	if e == nil {
		t.Fatalf("no CONDUIT_POWER granted to a submerged in-range player by an active conduit")
	}
	if e.amplifier != 0 {
		t.Fatalf("CONDUIT_POWER amplifier = %d, want 0", e.amplifier)
	}
	if e.duration != conduitEffectDuration {
		t.Fatalf("CONDUIT_POWER duration = %d, want %d (EFFECT_DURATION*20)", e.duration, conduitEffectDuration)
	}
}

// TestConduitEffectRangeFormula pins effectRange = size/7*16: with a full 42-block frame the range is
// 42/7*16 == 96, so a player 90 blocks away (in water) still gets CONDUIT_POWER but one 100 away does not.
func TestConduitEffectRangeFormula(t *testing.T) {
	loop, mgr := newConduitLoop()
	// Widen the ready-chunk footprint so the far player's chunk exists.
	for cx := int32(-8); cx <= 8; cx++ {
		for cz := int32(-8); cz <= 8; cz++ {
			if _, ok := mgr.Get(level.ChunkPos{cx, cz}); ok {
				continue
			}
			ch := level.EmptyChunk(blockTestSecs)
			ch.Status = level.StatusFull
			mgr.Insert(level.ChunkPos{cx, cz}, ch)
		}
	}
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	floodConduitPocket(mgr, pos)
	placeConduitFrame(mgr, pos, 42) // full frame -> effectRange = 42/7*16 = 96

	c := loop.resolveConduit(pos)
	_ = c

	// Player at x=90 (inside 96) and player at x=100 (outside 96), both flooded so isInWaterOrRain holds.
	near := &tickPlayer{x: 90, y: 64, z: 0, entityID: 1}
	far := &tickPlayer{x: 100, y: 64, z: 0, entityID: 2}
	loop.players = append(loop.players, near, far)
	mgr.SetBlock(pk.Position{X: 90, Y: 64, Z: 0}, waterBlockState(), dimMinY)
	mgr.SetBlock(pk.Position{X: 100, Y: 64, Z: 0}, waterBlockState(), dimMinY)

	loop.gametime = 40
	loop.tickConduits()

	if near.activeEffects[effectConduitPower] == nil {
		t.Fatalf("player at 90 blocks (range 96) got no CONDUIT_POWER; want granted")
	}
	if far.activeEffects[effectConduitPower] != nil {
		t.Fatalf("player at 100 blocks (range 96) got CONDUIT_POWER; want none")
	}
}

// TestConduitIncompleteFrameInactive: a frame with <16 blocks is inactive (updateShape returns size<16).
func TestConduitIncompleteFrameInactive(t *testing.T) {
	loop, mgr := newConduitLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	floodConduitPocket(mgr, pos)
	placeConduitFrame(mgr, pos, 15) // one short of MIN_ACTIVE_SIZE

	c := loop.resolveConduit(pos)
	p := &tickPlayer{x: 2, y: 64, z: 0, entityID: 1}
	loop.players = append(loop.players, p)
	mgr.SetBlock(pk.Position{X: 2, Y: 64, Z: 0}, waterBlockState(), dimMinY)

	loop.gametime = 40
	loop.tickConduits()

	if c.isActive {
		t.Fatalf("a conduit with only 15 frame blocks is active; want inactive (<16)")
	}
	if p.activeEffects[effectConduitPower] != nil {
		t.Fatalf("an inactive (incomplete-frame) conduit granted CONDUIT_POWER; want none")
	}
}

// TestConduitBrokenPocketInactive: a non-water cell in the 3x3x3 pocket fails updateShape immediately, so
// even a full 42-block frame is inactive.
func TestConduitBrokenPocketInactive(t *testing.T) {
	loop, mgr := newConduitLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	floodConduitPocket(mgr, pos)
	placeConduitFrame(mgr, pos, 42)
	// Break the pocket: put stone in a pocket cell.
	mgr.SetBlock(pk.Position{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, stoneBlockState(), dimMinY)

	c := loop.resolveConduit(pos)
	p := &tickPlayer{x: 2, y: 64, z: 0, entityID: 1}
	loop.players = append(loop.players, p)

	loop.gametime = 40
	loop.tickConduits()

	if c.isActive {
		t.Fatalf("a conduit with a broken (non-water) pocket is active; want inactive")
	}
	if p.activeEffects[effectConduitPower] != nil {
		t.Fatalf("a broken-pocket conduit granted CONDUIT_POWER; want none")
	}
}

// TestConduitFullFrameAttacksHostile: a full 42-block frame conduit selects a hostile (Zombie, Monster
// category) within 8 blocks that is in water, and deals 4.0 magic damage to it.
func TestConduitFullFrameAttacksHostile(t *testing.T) {
	loop, mgr := newConduitLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	floodConduitPocket(mgr, pos)
	placeConduitFrame(mgr, pos, 42) // full frame -> hunts

	// A zombie 3 blocks away, in water (isInWaterOrRain true).
	z := NewEntity(500, entity.Zombie, 3, 64, 0)
	z.health = 20.0
	loop.only().entities.add(z)
	mgr.SetBlock(pk.Position{X: 3, Y: 64, Z: 0}, waterBlockState(), dimMinY)

	c := loop.resolveConduit(pos)

	loop.gametime = 40
	loop.tickConduits()

	if !c.isHunting {
		t.Fatalf("a full 42-block frame conduit is not hunting")
	}
	if !c.destroyTargetSet || c.destroyTarget != z.id {
		t.Fatalf("conduit did not acquire the in-range hostile as its destroy target (set=%v id=%d want %d)", c.destroyTargetSet, c.destroyTarget, z.id)
	}
	// hurtServer(magic, 4.0): a full-health (20) zombie drops to 16 (no armor; magic bypasses armor).
	if z.health != 16.0 {
		t.Fatalf("zombie health after conduit attack = %.1f, want 16.0 (20 - 4.0 magic)", z.health)
	}
}

// TestConduitInactiveDoesNotHunt: an inactive conduit (size < 42) never acquires a destroy target even with a
// hostile in range (updateAndAttackTarget only runs when active, and updateDestroyTarget returns null when
// isActive==false).
func TestConduitInactiveDoesNotHunt(t *testing.T) {
	loop, mgr := newConduitLoop()
	pos := pk.Position{X: 0, Y: 64, Z: 0}
	floodConduitPocket(mgr, pos)
	placeConduitFrame(mgr, pos, 20) // active (>=16) but NOT full (<42) -> no hunt

	z := NewEntity(501, entity.Zombie, 3, 64, 0)
	z.health = 20.0
	loop.only().entities.add(z)
	mgr.SetBlock(pk.Position{X: 3, Y: 64, Z: 0}, waterBlockState(), dimMinY)

	c := loop.resolveConduit(pos)

	loop.gametime = 40
	loop.tickConduits()

	if c.isHunting {
		t.Fatalf("a 20-block-frame conduit is hunting; want not (needs >=42)")
	}
	if c.destroyTargetSet {
		t.Fatalf("a non-full conduit acquired a destroy target; want none")
	}
	if z.health != 20.0 {
		t.Fatalf("zombie took damage from a non-full conduit; health=%.1f want 20.0", z.health)
	}
}
