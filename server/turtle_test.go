package server

// turtle_test.go - MOB-PREY (Task #9): the Turtle behavior test. Boot-load + spawn are covered by the
// shared allFourMobs table (TestAllFourMobsBootLoad / TestSpawnVanillaMobByName). Here we prove the ambient
// TemptGoal works on the turtle's own food tag (turtle_food = seagrass, id 238): a player holding seagrass
// within range makes the turtle's TemptGoal engage and set a nav want-target toward the player. The
// water-nav trio + egg-lay are cite-deferred (.star header). Pig oracle untouched.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

func loadVanillaTurtleRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_turtle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir turtle plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_turtle", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_turtle/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp turtle %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_turtle): %v", err)
	}
	if _, ok := r.byName["vanilla_turtle"]; !ok {
		t.Fatal("vanilla_turtle declaration not captured after load")
	}
	return r
}

// TestTurtleTemptedBySeagrass: a turtle near a player holding seagrass (turtle_food id 238) engages its
// TemptGoal and sets a nav want-target toward the player. Also pins the CREATURE category.
func TestTurtleTemptedBySeagrass(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaTurtleRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Turtle.ID); got != categoryCreature {
		t.Fatalf("categoryOf(Turtle) = %v, want categoryCreature", got)
	}

	decl := loop.mobRegistry.byName["vanilla_turtle"]
	tr := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	tr.onGround = true
	if tr.typ != entity.Turtle.ID {
		t.Fatalf("turtle typ = %d, want entity.Turtle.ID %d", tr.typ, entity.Turtle.ID)
	}

	// Player 5 blocks away holding seagrass (turtle_food id 238). Within TEMPT_RANGE (10), beyond STOP_DISTANCE.
	addPlayerHolding(loop, 13.5, float64(floorY+1), 8.5, 238, false)

	tempted := false
	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if tr.ai != nil && tr.ai.hasTarget {
			tempted = true
			break
		}
	}
	if !tempted {
		t.Fatal("the turtle was never tempted by seagrass - the turtle_food TemptGoal did not engage")
	}
}
