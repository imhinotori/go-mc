package server

// enderman_test.go — MOB-HOST-08 (Task #9): the Enderman behavior test. Boot-load + spawn are covered by
// the shared allFourMobs table. Here we prove the distinctive TELEPORT: (1) endermanTeleport relocates the
// enderman to a valid ground landing, and (2) endermanHurtTeleport dodges a PROJECTILE hit (the enderman is
// somewhere else after an arrow-source hit). Pig oracle untouched.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

func loadVanillaEndermanRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_enderman")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir enderman plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_enderman", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_enderman/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp enderman %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_enderman): %v", err)
	}
	if _, ok := r.byName["vanilla_enderman"]; !ok {
		t.Fatal("vanilla_enderman declaration not captured after load")
	}
	return r
}

func endermanLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaEndermanRegistry(t))
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestEndermanTeleports: endermanTeleport relocates the enderman to a valid ground landing on the floor.
func TestEndermanTeleports(t *testing.T) {
	loop, floorY := endermanLoop(t)
	if got := categoryOf(entity.Enderman.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Enderman) = %v, want categoryMonster", got)
	}
	decl := loop.mobRegistry.byName["vanilla_enderman"]
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	if e.typ != entity.Enderman.ID {
		t.Fatalf("enderman typ = %d, want entity.Enderman.ID %d", e.typ, entity.Enderman.ID)
	}
	ox, oz := e.x, e.z

	moved := false
	loop.withRegion(loop.only(), func() {
		// A 32x32 floor exists; several teleport attempts should land somewhere on it.
		for i := 0; i < 50; i++ {
			if loop.endermanTeleport(e) {
				moved = true
				break
			}
		}
	})
	if !moved {
		t.Fatal("endermanTeleport never succeeded over 50 attempts on a full floor")
	}
	if e.x == ox && e.z == oz {
		t.Fatal("endermanTeleport reported success but the enderman did not move")
	}
	// Landed feet must be at floorY+1 (standing on the floor block at floorY).
	if int(e.y) != floorY+1 {
		t.Fatalf("enderman landed at y=%v, want feet at floorY+1=%d (standing on the floor)", e.y, floorY+1)
	}
}

// TestEndermanDodgesProjectile: a projectile-source hit makes the enderman teleport away (hurt-dodge).
func TestEndermanDodgesProjectile(t *testing.T) {
	loop, floorY := endermanLoop(t)
	decl := loop.mobRegistry.byName["vanilla_enderman"]
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	initSpawnHealth(e)
	ox, oz := e.x, e.z

	loop.withRegion(loop.only(), func() {
		// An arrow-source hit (minecraft:arrow is IS_PROJECTILE) → the enderman dodges via teleport.
		loop.applyDamageEntity(e, damageSourceArrow(0), 1.0)
	})
	if e.x == ox && e.z == oz {
		t.Fatal("the enderman did not dodge a projectile hit — endermanHurtTeleport did not fire")
	}
}
