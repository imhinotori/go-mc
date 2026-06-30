package server

// zombie_test.go — MOB-HOST-01 (Phase 35-03, completed in the 35-01b gap-closure): the per-hostile
// behavior test for the FIRST hostile, the 1:1 vanilla Zombie re-expressed as the vanilla_zombie
// Starlark plugin. The CORE assertion is the phase goal "hostiles HUNT and ATTACK the player": a
// spawned zombie + a player drives ticks until the zombie ACQUIRES the player as its target
// (attackTargetID == the player's id, via the kind-routed nearestAttackableTargetGoal's nextInt(10)
// acquire) AND the player TAKES ATTACK_DAMAGE (the kind-routed meleeAttackGoal fires doHurtTarget
// through the Phase-29 keystone). 35-01 built the combat goals as Go-native structs but added NO seam
// to reference them; 35-01b's kind= seam wires them — this test proves the seam delivers REAL damage,
// not just a boot-load.
//
// The combat lockstep RNG (nextInt(10) acquire) is owned + pinned by the Go-native goal tests
// (ai_goals_target_test.go); here we drive the END-TO-END declared-mob path. The pig oracle
// (TestPluginPigEqualsGoNativePig) is UNTOUCHED — the zombie is a separate mob.

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

// loadVanillaZombieRegistry materializes the repo-root plugins/vanilla_zombie plugin into a temp dir
// and loads it through the host with the server-built declare_mob/goal builtins injected, returning the
// registry holding the captured "vanilla_zombie" declaration. Mirrors loadVanillaCowRegistry. Tests run
// with cwd=server/, so the repo-root copy sits at ../plugins/vanilla_zombie.
func loadVanillaZombieRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_zombie")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir zombie plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "vanilla_zombie", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_zombie/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp zombie %s: %v", name, err)
		}
	}

	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_zombie): %v", err)
	}
	if _, ok := r.byName["vanilla_zombie"]; !ok {
		t.Fatal("vanilla_zombie declaration not captured after load")
	}
	return r
}

// zombieLoop builds a physics loop with a one-chunk stone floor and the vanilla_zombie registry
// installed (replacing the default pig-only registry so spawnDeclaredMob can build a zombie).
func zombieLoop(t *testing.T) (*TickLoop, int, *fakeClock) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaZombieRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, floorY, clock
}

// spawnZombie spawns the declared vanilla_zombie via the shared spawnDeclaredMob path.
func spawnZombie(loop *TickLoop, x, y, z float64) *Entity {
	decl := loop.mobRegistry.byName["vanilla_zombie"]
	return loop.spawnDeclaredMob(decl, x, y, z)
}

// combatTestPlayer registers a live player at (x,y,z) the hostiles can acquire + hit: it carries a
// capture client (actuallyHurt's SetHealth + broadcastPlayerDamageEvent need a non-nil client), a
// chunk center, full health/food (so a melee hit lowers a non-zero bar without instantly dying), and a
// unique entity id (playerByEntityID resolves it for the target scan + the melee victim lookup). Shared
// by the zombie/skeleton/spider behavior tests — the SAME player setup the phase-goal damage assertion
// needs. No armor is seeded, so the full ATTACK_DAMAGE lands (the assertion checks the exact amount).
func combatTestPlayer(loop *TickLoop, x, y, z float64, entityID int32) *tickPlayer {
	p := &tickPlayer{
		x: x, y: y, z: z,
		center:     level.ChunkPos{0, 0},
		client:     captureClient(64),
		entityID:   entityID,
		health:     20.0,
		food:       20,
		saturation: 5.0,
	}
	loop.players = append(loop.players, p)
	return p
}

// --- TestZombieBootLoads -----------------------------------------------------------------------

// TestZombieBootLoads: the vanilla_zombie plugin boot-loads, spawnZombie builds a live zombie rendering
// as entity.Zombie.ID with a non-nil AI holding the 4 goalSelector goals (melee@3 + stroll@7 + look@8 +
// around@8) and the 2 targetSelector goals (hurt_by_target@1 + nearest_attackable_target@2). Zombie
// attributes follow_range 35 + movement_speed 0.23 + attack_damage 3.0 + armor 2.0.
func TestZombieBootLoads(t *testing.T) {
	loop, floorY, _ := zombieLoop(t)

	zombie := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
	if zombie.typ != entity.Zombie.ID {
		t.Fatalf("zombie typ = %d, want entity.Zombie.ID %d (custom = behavior)", zombie.typ, entity.Zombie.ID)
	}
	if zombie.ai == nil {
		t.Fatal("zombie has no AI")
	}
	// 4 goalSelector goals (the TARGET goals route to the targetSelector, NOT goals).
	if got := len(zombie.ai.goals.goals); got != 4 {
		t.Fatalf("zombie has %d goalSelector goals, want 4 (melee@3 stroll@7 look@8 around@8)", got)
	}
	// 2 targetSelector goals (hurt_by_target@1 + nearest_attackable_target@2).
	if got := len(zombie.ai.targetSelector.goals); got != 2 {
		t.Fatalf("zombie has %d targetSelector goals, want 2 (hurt_by_target@1 + nearest_attackable_target@2)", got)
	}
	// The goalSelector priorities are the jar registerGoals indices (3,7,8,8).
	seen := map[int]int{}
	for _, wg := range zombie.ai.goals.goals {
		seen[wg.priority]++
	}
	if seen[3] != 1 || seen[7] != 1 || seen[8] != 2 {
		t.Fatalf("zombie goalSelector priorities = %v, want {3:1, 7:1, 8:2}", seen)
	}

	// Attributes seeded from the declaration (Zombie.createAttributes).
	if fr := zombie.getAttributeValue(attribute.FollowRange); fr != 35.0 {
		t.Fatalf("zombie follow_range = %v, want 35.0 (Zombie.createAttributes FOLLOW_RANGE)", fr)
	}
	if ad := zombie.getAttributeValue(attribute.AttackDamage); ad != 3.0 {
		t.Fatalf("zombie attack_damage = %v, want 3.0 (Zombie.createAttributes ATTACK_DAMAGE)", ad)
	}
	if ms := zombie.attributes.GetValue(attribute.MovementSpeed.Name()); ms != 0.23 {
		t.Fatalf("zombie movement_speed = %v, want 0.23", ms)
	}
}

// --- TestZombieBehavior (THE phase goal: hunt + attack) ----------------------------------------

// TestZombieBehavior: a spawned zombie next to a player ACQUIRES the player as its target
// (attackTargetID == player.id, via the kind-routed nearestAttackableTargetGoal) AND deals the player
// REAL ATTACK_DAMAGE (the kind-routed meleeAttackGoal fires doHurtTarget through the Phase-29 keystone).
// This is the must-have the phase goal demands — the kind= seam delivering hunt + melee, not boot-load.
func TestZombieBehavior(t *testing.T) {
	loop, floorY, clock := zombieLoop(t)

	if got := categoryOf(entity.Zombie.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Zombie) = %v, want categoryMonster (vanilla EntityType.ZOMBIE is MONSTER)", got)
	}

	zombie := spawnZombie(loop, 8.5, float64(floorY+1), 8.5)
	zombie.onGround = true

	// A player flush against the zombie (within melee reach) so the melee goal can hit once acquired.
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 8.5, 7700)
	startHealth := p.health

	acquired := false
	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if zombie.ai.getTarget() == p.entityID {
			acquired = true
		}
		if p.health < startHealth {
			break // the melee landed — the must-have is met
		}
	}

	if !acquired {
		t.Fatal("the zombie never ACQUIRED the player as its target (attackTargetID == player.id) — the kind-routed nearestAttackableTargetGoal did not hunt")
	}
	if p.health >= startHealth {
		t.Fatalf("the player took NO damage (health %v >= start %v) — the kind-routed meleeAttackGoal did not deal ATTACK_DAMAGE through the keystone", p.health, startHealth)
	}
	// The damage dealt is the zombie's ATTACK_DAMAGE (3.0), the player has no armor (the full hit lands).
	if dealt := startHealth - p.health; dealt != float32(zombie.getAttributeValue(attribute.AttackDamage)) {
		t.Fatalf("player lost %v health, want %v (the zombie ATTACK_DAMAGE, no armor on the player)", dealt, zombie.getAttributeValue(attribute.AttackDamage))
	}
}
