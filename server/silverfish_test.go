package server

// silverfish_test.go — MOB-HOST-05 (Task #9): the per-hostile behavior test for the Silverfish, the 1:1
// vanilla Silverfish re-expressed as the vanilla_silverfish Starlark plugin (float@1 + Go-native melee@4
// + hurt_by@1 + nearest@2; the stone-infest goals are cite-deferred). The CORE assertion is the phase
// goal "hostiles HUNT and ATTACK the player": a spawned silverfish + a player drives ticks until the
// silverfish ACQUIRES the player AND the player TAKES the silverfish ATTACK_DAMAGE (1.0, via the
// kind-routed meleeAttackGoal → doHurtTarget). The pig oracle is UNTOUCHED (a separate mob).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// loadVanillaSilverfishRegistry materializes the repo-root plugins/mobs/vanilla_silverfish plugin into a
// temp dir and loads it through the host with the server-built declare_mob/goal builtins injected. Mirrors
// loadVanillaSkeletonRegistry (cwd=server/, so the repo-root copy sits at ../plugins/mobs/vanilla_silverfish).
func loadVanillaSilverfishRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_silverfish")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir silverfish plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_silverfish", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_silverfish/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp silverfish %s: %v", name, err)
		}
	}

	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_silverfish): %v", err)
	}
	if _, ok := r.byName["vanilla_silverfish"]; !ok {
		t.Fatal("vanilla_silverfish declaration not captured after load")
	}
	return r
}

// TestSilverfishBehavior: a spawned silverfish next to a player ACQUIRES it (attackTargetID == player.id)
// AND deals it REAL ATTACK_DAMAGE (1.0) via the kind-routed meleeAttackGoal.
func TestSilverfishBehavior(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaSilverfishRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Silverfish.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Silverfish) = %v, want categoryMonster", got)
	}

	decl := loop.mobRegistry.byName["vanilla_silverfish"]
	sf := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	sf.onGround = true
	if sf.typ != entity.Silverfish.ID {
		t.Fatalf("silverfish typ = %d, want entity.Silverfish.ID %d", sf.typ, entity.Silverfish.ID)
	}

	p := combatTestPlayer(loop, 9.5, float64(floorY+1), 8.5, 6363)
	start := p.health

	acquired := false
	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if sf.ai.getTarget() == p.entityID {
			acquired = true
		}
		if p.health < start {
			break
		}
	}
	if !acquired {
		t.Fatal("the silverfish never ACQUIRED the player — the nearestAttackableTargetGoal did not hunt")
	}
	if p.health >= start {
		t.Fatalf("the player took NO damage (health %v >= %v) — the meleeAttackGoal did not deal ATTACK_DAMAGE", p.health, start)
	}
	if dealt := start - p.health; dealt != float32(sf.getAttributeValue(attribute.AttackDamage)) {
		t.Fatalf("player lost %v health, want %v (the silverfish ATTACK_DAMAGE 1.0)", dealt, sf.getAttributeValue(attribute.AttackDamage))
	}
}
