package server

// endermite_test.go - MOB-PREY (Task #9): the Endermite behavior test. Boot-load + spawn are covered by
// the shared allFourMobs table (TestAllFourMobsBootLoad / TestSpawnVanillaMobByName). Here we prove the
// Endermite's SIGNATURE mechanic - the despawn timer (Endermite.aiStep: ++life while non-persistent,
// discard at life >= MAX_LIFE 2400) - via the Go-native endermiteAiStep hook, plus its MONSTER category.
// The pig oracle is UNTOUCHED (a separate mob).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

func loadVanillaEndermiteRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_endermite")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir endermite plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_endermite", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_endermite/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp endermite %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_endermite): %v", err)
	}
	if _, ok := r.byName["vanilla_endermite"]; !ok {
		t.Fatal("vanilla_endermite declaration not captured after load")
	}
	return r
}

// TestEndermiteIsMonster pins the jar MobCategory.MONSTER classification.
func TestEndermiteIsMonster(t *testing.T) {
	if got := categoryOf(entity.Endermite.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Endermite) = %v, want categoryMonster", got)
	}
}

// TestEndermiteDespawnTimer: a spawned endermite ages one `life` per tick and is discarded (removed from
// the store) once life reaches MAX_LIFE (2400). We assert it is ALIVE at 2399 ticks and GONE at 2400 -
// the exact Endermite.aiStep threshold.
func TestEndermiteDespawnTimer(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaEndermiteRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_endermite"]
	em := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	em.onGround = true
	if em.typ != entity.Endermite.ID {
		t.Fatalf("endermite typ = %d, want entity.Endermite.ID %d", em.typ, entity.Endermite.ID)
	}
	id := em.id

	// Advance to just before the threshold: it must still be alive (life < 2400).
	for i := 0; i < endermiteMaxLife-1; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
	}
	if _, ok := loop.only().entities.byID[id]; !ok {
		t.Fatalf("endermite despawned early (life=%d) - it should live until MAX_LIFE %d", em.life, endermiteMaxLife)
	}

	// One more tick pushes life to MAX_LIFE -> discard(); the endermite leaves the store.
	clock.add(tickStep)
	loop.advance(clock.Now())
	if _, ok := loop.only().entities.byID[id]; ok {
		t.Fatalf("endermite still present at life>=%d - the Endermite.aiStep despawn discard did not fire", endermiteMaxLife)
	}
}
