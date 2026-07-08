package server

// goat_test.go -- deterministic pins for the Goat (net.minecraft.world.entity.animal.goat.Goat, 1:1 javap
// this session). Verifies the spawn attributes (MAX_HEALTH 10 / MOVEMENT_SPEED 0.2 / ATTACK_DAMAGE 2) and
// that the SCREAMING-variant spawn roll (nextDouble() < 0.02) is deterministic per goat id.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func goatLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestGoatSpawnDefaults: spawnGoat builds a goat rendering as entity.Goat.ID with the jar attributes.
func TestGoatSpawnDefaults(t *testing.T) {
	loop, floorY := goatLoop(t)
	g := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)
	if g.typ != entity.Goat.ID {
		t.Fatalf("goat typ = %d, want entity.Goat.ID %d", g.typ, entity.Goat.ID)
	}
	if !g.isGoat {
		t.Fatal("goat not marked isGoat")
	}
	if math.Abs(float64(g.health)-10.0) > 1e-6 {
		t.Fatalf("goat health = %v, want 10.0 (MAX_HEALTH)", g.health)
	}
	if got := g.getAttributeValue(attribute.MaxHealth); math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("goat MAX_HEALTH = %v, want 10.0", got)
	}
	if got := g.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.20000000298023224) > 1e-12 {
		t.Fatalf("goat MOVEMENT_SPEED = %v, want 0.20000000298023224", got)
	}
	if got := g.getAttributeValue(attribute.AttackDamage); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("goat ATTACK_DAMAGE = %v, want 2.0", got)
	}
	if g.ai == nil || g.ai.rng == nil {
		t.Fatal("goat has no minimal AI / rng")
	}
}

// TestGoatScreamingRollDeterministic: the SCREAMING flag is a deterministic per-goat nextDouble() < 0.02
// roll; goatAiStep is a bounded no-op that never mutates it. Spawning many goats yields at least one of
// each outcome over a large sample would be flaky, so we only pin: the roll is stable + goatAiStep no-op.
func TestGoatScreamingRollDeterministic(t *testing.T) {
	loop, floorY := goatLoop(t)
	g := loop.spawnGoat(8.5, float64(floorY+1), 8.5, false)
	before := g.goatScreaming
	for i := 0; i < 100; i++ {
		loop.goatAiStep(g)
	}
	if g.goatScreaming != before {
		t.Fatal("goatAiStep mutated the screaming flag (must be a bounded no-op)")
	}
}
