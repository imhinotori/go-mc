package server

// ocelot_test.go - MOB-PREY (Task #9): the Ocelot behavior test. Boot-load + spawn are covered by the
// shared allFourMobs table (TestAllFourMobsBootLoad / TestSpawnVanillaMobByName). Here we prove the ambient
// OcelotTemptGoal works on the ocelot's own food tag (ocelot_food = cod/salmon, ids 1086/1087): a player
// holding cod within range makes the ocelot's TemptGoal engage and set a nav want-target toward the player.
// The leap/attack/prey-target + trust are cite-deferred (.star header). Pig oracle untouched.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

func loadVanillaOcelotRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_ocelot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir ocelot plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_ocelot", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_ocelot/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp ocelot %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_ocelot): %v", err)
	}
	if _, ok := r.byName["vanilla_ocelot"]; !ok {
		t.Fatal("vanilla_ocelot declaration not captured after load")
	}
	return r
}

// TestOcelotTemptedByCod: an ocelot near a player holding cod (ocelot_food id 1086) engages its
// OcelotTemptGoal and sets a nav want-target toward the player. Also pins the CREATURE category.
func TestOcelotTemptedByCod(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaOcelotRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Ocelot.ID); got != categoryCreature {
		t.Fatalf("categoryOf(Ocelot) = %v, want categoryCreature", got)
	}

	decl := loop.mobRegistry.byName["vanilla_ocelot"]
	oc := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	oc.onGround = true
	if oc.typ != entity.Ocelot.ID {
		t.Fatalf("ocelot typ = %d, want entity.Ocelot.ID %d", oc.typ, entity.Ocelot.ID)
	}

	// Player 5 blocks away holding cod (ocelot_food id 1086). Within TEMPT_RANGE (10), beyond STOP_DISTANCE.
	addPlayerHolding(loop, 13.5, float64(floorY+1), 8.5, 1086, false)

	tempted := false
	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if oc.ai != nil && oc.ai.hasTarget {
			tempted = true
			break
		}
	}
	if !tempted {
		t.Fatal("the ocelot was never tempted by cod - the ocelot_food OcelotTemptGoal did not engage")
	}
}
