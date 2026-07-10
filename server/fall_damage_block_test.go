package server

// fall_damage_block_test.go -- the Block.fallOn damageMultiplier pins (HayBlock 0.2, BedBlock 0.5,
// SlimeBlock 0.0, default 1.0), 1:1 javap this session. A player landing on these surfaces takes the
// scaled (or zero) fall damage instead of the full amount.

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// fallMultiplierFor drops a player onto the block placed at floorY and returns the fall multiplier the
// landing dispatch resolves (via fallOnMultiplierAt at the standing position floorY+1).
func fallMultiplierFor(t *testing.T, blockName string) float64 {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	sid, ok := block.DefaultStateID[blockName]
	if !ok {
		t.Fatalf("no default state for %s", blockName)
	}
	mgr.SetBlock(pk.Position{X: 8, Y: floorY, Z: 8}, sid, dimMinY)
	p := &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 8.5, entityID: 8801}
	return loop.fallOnMultiplierAt(p)
}

// TestFallOnMultiplierHay: landing on a hay bale resolves the 0.2 multiplier (HayBlock.fallOn).
func TestFallOnMultiplierHay(t *testing.T) {
	if got := fallMultiplierFor(t, "minecraft:hay_block"); got != 0.2 {
		t.Fatalf("hay_block fall multiplier = %v, want 0.2 (HayBlock.fallOn)", got)
	}
}

// TestFallOnMultiplierSlime: landing on a slime block resolves 0.0 -- fall damage cancelled (SlimeBlock.fallOn).
func TestFallOnMultiplierSlime(t *testing.T) {
	if got := fallMultiplierFor(t, "minecraft:slime_block"); got != 0.0 {
		t.Fatalf("slime_block fall multiplier = %v, want 0.0 (SlimeBlock.fallOn cancels)", got)
	}
}

// TestFallOnMultiplierBed: landing on a bed resolves 0.5 (BedBlock.fallOn super.fallOn(..., d*0.5)).
func TestFallOnMultiplierBed(t *testing.T) {
	if got := fallMultiplierFor(t, "minecraft:red_bed"); got != 0.5 {
		t.Fatalf("red_bed fall multiplier = %v, want 0.5 (BedBlock.fallOn)", got)
	}
}

// TestFallOnMultiplierDefault: landing on stone resolves the Entity.fallOn default 1.0.
func TestFallOnMultiplierDefault(t *testing.T) {
	if got := fallMultiplierFor(t, "minecraft:stone"); got != 1.0 {
		t.Fatalf("stone fall multiplier = %v, want 1.0 (Entity.fallOn default)", got)
	}
}
