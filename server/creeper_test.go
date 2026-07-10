package server

// creeper_test.go — MOB-HOST-06 (Task #9): the per-hostile behavior test for the Creeper. The CORE
// assertions: (1) the creeper ACQUIRES the player and ARMS its fuse (swellDir=1) when the player is close,
// (2) the fuse advances to maxSwell and the creeper EXPLODES (removed from the store), (3) the explosion
// deals the player REAL damage (ServerExplosion entity-damage port). Plus a focused unit test of the
// explosion damage falloff. Pig oracle UNTOUCHED (a separate mob).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// loadVanillaCreeperRegistry materializes the repo-root plugins/mobs/vanilla_creeper plugin into a temp
// dir and loads it with the server-built declare_mob/goal builtins injected. Mirrors the skeleton loader.
func loadVanillaCreeperRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_creeper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir creeper plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_creeper", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_creeper/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp creeper %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_creeper): %v", err)
	}
	if _, ok := r.byName["vanilla_creeper"]; !ok {
		t.Fatal("vanilla_creeper declaration not captured after load")
	}
	return r
}

// TestCreeperSwellAndExplode: a creeper next to a player acquires it, ARMS the fuse, the fuse advances to
// maxSwell, the creeper EXPLODES (removed), and the player TAKES explosion damage.
func TestCreeperSwellAndExplode(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaCreeperRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Creeper.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Creeper) = %v, want categoryMonster", got)
	}

	decl := loop.mobRegistry.byName["vanilla_creeper"]
	cr := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	cr.onGround = true
	if cr.typ != entity.Creeper.ID {
		t.Fatalf("creeper typ = %d, want entity.Creeper.ID %d", cr.typ, entity.Creeper.ID)
	}

	// Player right next to the creeper (within the 3-block arm range) so the SwellGoal arms immediately.
	p := combatTestPlayer(loop, 9.5, float64(floorY+1), 8.5, 7373)
	start := p.health

	armed := false
	exploded := false
	for i := 0; i < 120; i++ { // >maxSwell(30) + acquire lead time
		clock.add(tickStep)
		loop.advance(clock.Now())
		if cr.swellDir > 0 {
			armed = true
		}
		if _, ok := loop.only().entities.byID[cr.id]; !ok {
			exploded = true
			break
		}
	}

	if !armed {
		t.Fatal("the creeper never ARMED its fuse (swellDir>0) — the SwellGoal did not engage the nearby player")
	}
	if !exploded {
		t.Fatalf("the creeper never EXPLODED (still in the store after 120 ticks; swell=%d/%d)", cr.swell, cr.maxSwell)
	}
	if p.health >= start {
		t.Fatalf("the player took NO explosion damage (health %v >= %v) — hurtEntities did not fire", p.health, start)
	}
}

// TestExplosionDamageFalloff: a direct explosion unit test — a player at the blast center takes MORE
// damage than one at the edge (the (1-dist)*exposure falloff), and a player out of range takes none.
func TestExplosionDamageFalloff(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	// radius 3 (creeper). doubleRadius 6 → in-range within 6 blocks.
	near := combatTestPlayer(loop, 8.7, float64(floorY+1), 8.5, 101)
	far := combatTestPlayer(loop, 13.0, float64(floorY+1), 8.5, 102) // ~4.3 blocks (in range, edge)
	out := combatTestPlayer(loop, 30.0, float64(floorY+1), 8.5, 103) // >6 blocks (out of range)
	n0, f0, o0 := near.health, far.health, out.health

	loop.withRegion(loop.only(), func() {
		loop.explode(-1, 8.5, float64(floorY+1), 8.5, 3.0)
	})

	nearDmg := n0 - near.health
	farDmg := f0 - far.health
	if nearDmg <= 0 {
		t.Fatalf("the near player took no explosion damage (%v)", nearDmg)
	}
	if farDmg < 0 {
		t.Fatalf("negative far damage %v", farDmg)
	}
	if nearDmg <= farDmg {
		t.Fatalf("near damage %v should exceed edge damage %v (the (1-dist) falloff)", nearDmg, farDmg)
	}
	if out.health != o0 {
		t.Fatalf("the out-of-range player took damage %v (should be 0 — beyond radius*2)", o0-out.health)
	}
}

// TestCreeperSwellDisarmsWhenLineOfSightBlocked: with a solid wall between the creeper and a nearby
// player, the SwellGoal.tick disarm branch (offsets 53-78: !getSensing().hasLineOfSight -> setSwellDir(-1))
// fires every tick, so the fuse NEVER arms (swellDir stays <=0) and the creeper NEVER explodes -- 1:1 with
// the jar SwellGoal.tick line-of-sight guard (previously a stubbed always-true no-op).
func TestCreeperSwellDisarmsWhenLineOfSightBlocked(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaCreeperRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_creeper"]
	cr := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	cr.onGround = true

	// Player 2 blocks north (within the 3-block arm range, dist^2 = 4 < 9), so canUse would arm --
	// but a solid stone wall at z=10 blocks the eye ray, so tick must disarm every tick.
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 10.5, 7374)
	_ = p
	fillWall(ch, 8, 9, floorY, floorY+4)

	for i := 0; i < 120; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if cr.swellDir > 0 {
			t.Fatalf("the creeper ARMED its fuse (swellDir=%d) despite a wall blocking line of sight -- SwellGoal.tick LoS disarm did not fire", cr.swellDir)
		}
		if _, ok := loop.only().entities.byID[cr.id]; !ok {
			t.Fatal("the creeper EXPLODED despite no line of sight -- the LoS disarm should keep the fuse at swellDir<=0")
		}
	}
}
