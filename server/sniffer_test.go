package server

// sniffer_test.go -- deterministic pins for the Sniffer (net.minecraft.world.entity.animal.sniffer.Sniffer,
// 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 14 / MOVEMENT_SPEED 0.10000000149011612).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func snifferLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestSnifferSpawnDefaults: spawnSniffer builds a sniffer rendering as entity.Sniffer.ID with the jar
// attributes (MAX_HEALTH 14, MOVEMENT_SPEED 0.1 bit-exact).
func TestSnifferSpawnDefaults(t *testing.T) {
	loop, floorY := snifferLoop(t)
	s := loop.spawnSniffer(8.5, float64(floorY+1), 8.5, false)
	if s.typ != entity.Sniffer.ID {
		t.Fatalf("sniffer typ = %d, want entity.Sniffer.ID %d", s.typ, entity.Sniffer.ID)
	}
	if !s.isSniffer {
		t.Fatal("sniffer not marked isSniffer")
	}
	if math.Abs(float64(s.health)-14.0) > 1e-6 {
		t.Fatalf("sniffer health = %v, want 14.0 (MAX_HEALTH)", s.health)
	}
	if got := s.getAttributeValue(attribute.MaxHealth); math.Abs(got-14.0) > 1e-9 {
		t.Fatalf("sniffer MAX_HEALTH = %v, want 14.0", got)
	}
	if got := s.getAttributeValue(attribute.MovementSpeed); math.Float64bits(got) != math.Float64bits(0.10000000149011612) {
		t.Fatalf("sniffer MOVEMENT_SPEED = %v, want 0.10000000149011612 (bit-exact)", got)
	}
	if s.ai == nil || s.ai.rng == nil {
		t.Fatal("sniffer has no minimal AI / rng")
	}
}
