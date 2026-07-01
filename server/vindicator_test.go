package server

// vindicator_test.go — RAIDER (Task): the per-mob behavior test for the Vindicator, the 1:1 vanilla
// Vindicator re-expressed as the vanilla_vindicator Starlark plugin (float@0 + Go-native melee@5 +
// hurt_by@1 + nearest@2 + patrol@4; the Raider/door/Johnny goals are cite-deferred). The CORE assertion is
// the phase goal "the vindicator HUNTS and MELEES the player": a spawned vindicator + a player drives ticks
// until the vindicator ACQUIRES the player AND the player TAKES the vindicator ATTACK_DAMAGE (5.0, via the
// kind-routed meleeAttackGoal -> doHurtTarget). The pig oracle is UNTOUCHED (a separate mob).

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

// loadVanillaRaiderRegistry materializes a repo-root plugins/mobs/vanilla_<name> plugin into a temp dir and
// loads it through the host with the server-built declare_mob/goal builtins injected (cwd=server/, so the
// repo-root copy sits at ../plugins/mobs/vanilla_<name>). Shared by the 4 raider behavior tests.
func loadVanillaRaiderRegistry(t *testing.T, name string) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s plugin dir: %v", name, err)
	}
	for _, f := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", name, f))
		if err != nil {
			t.Fatalf("read repo-root %s/%s: %v", name, f, err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatalf("write temp %s %s: %v", name, f, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(%s): %v", name, err)
	}
	if _, ok := r.byName[name]; !ok {
		t.Fatalf("%s declaration not captured after load", name)
	}
	return r
}

// TestVindicatorBehavior: a spawned vindicator next to a player ACQUIRES it AND deals it REAL ATTACK_DAMAGE
// (5.0) via the kind-routed meleeAttackGoal.
func TestVindicatorBehavior(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRaiderRegistry(t, "vanilla_vindicator"))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Vindicator.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Vindicator) = %v, want categoryMonster", got)
	}

	decl := loop.mobRegistry.byName["vanilla_vindicator"]
	v := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	v.onGround = true
	if v.typ != entity.Vindicator.ID {
		t.Fatalf("vindicator typ = %d, want %d", v.typ, entity.Vindicator.ID)
	}

	p := combatTestPlayer(loop, 9.5, float64(floorY+1), 8.5, 7401)
	start := p.health

	acquired := false
	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if v.ai.getTarget() == p.entityID {
			acquired = true
		}
		if p.health < start {
			break
		}
	}
	if !acquired {
		t.Fatal("the vindicator never ACQUIRED the player")
	}
	if p.health >= start {
		t.Fatalf("the player took NO melee damage (health %v >= %v)", p.health, start)
	}
	if dealt := start - p.health; dealt != float32(v.getAttributeValue(attribute.AttackDamage)) {
		t.Fatalf("player lost %v health, want %v (Vindicator ATTACK_DAMAGE 5.0)", dealt, v.getAttributeValue(attribute.AttackDamage))
	}
}
