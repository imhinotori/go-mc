package server

// rabbit_test.go — MOB-PASS-05 (Task #9): the Rabbit behavior test. Boot-load + spawn are covered by the
// shared allFourMobs table (TestAllFourMobsBootLoad / TestSpawnVanillaMobByName). Here we prove the ambient
// TemptGoal works on the rabbit's own food tag: a player holding a rabbit_food item (a carrot) within
// range makes the rabbit's TemptGoal engage and set a want-target toward the player. Pig oracle untouched.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

func loadVanillaRabbitRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_rabbit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rabbit plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_rabbit", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_rabbit/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp rabbit %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_rabbit): %v", err)
	}
	if _, ok := r.byName["vanilla_rabbit"]; !ok {
		t.Fatal("vanilla_rabbit declaration not captured after load")
	}
	return r
}

// TestRabbitTemptedByCarrot: a rabbit near a player holding a carrot (rabbit_food id 1257) engages its
// TemptGoal and sets a nav want-target toward the player.
func TestRabbitTemptedByCarrot(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRabbitRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Rabbit.ID); got != categoryCreature {
		t.Fatalf("categoryOf(Rabbit) = %v, want categoryCreature", got)
	}

	decl := loop.mobRegistry.byName["vanilla_rabbit"]
	rab := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	rab.onGround = true
	if rab.typ != entity.Rabbit.ID {
		t.Fatalf("rabbit typ = %d, want entity.Rabbit.ID %d", rab.typ, entity.Rabbit.ID)
	}

	// Player 5 blocks away holding a carrot (rabbit_food). Within TEMPT_RANGE (10), beyond STOP_DISTANCE.
	addPlayerHolding(loop, 13.5, float64(floorY+1), 8.5, 1257, false)

	tempted := false
	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		// The TemptGoal sets a nav want toward the player when it engages.
		if rab.ai != nil && rab.ai.hasTarget {
			tempted = true
			break
		}
	}
	if !tempted {
		t.Fatal("the rabbit was never tempted by the carrot — the rabbit_food TemptGoal did not engage")
	}
}
