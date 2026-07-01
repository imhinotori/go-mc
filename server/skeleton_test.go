package server

// skeleton_test.go — MOB-HOST-02 (Phase 35-04, completed in the 35-01b gap-closure): the per-hostile
// behavior test for the SECOND hostile, the 1:1 vanilla Skeleton re-expressed as the vanilla_skeleton
// Starlark plugin (MELEE-ONLY v1; the bow/ranged path is documented-deferred). The CORE assertion is the
// phase goal "hostiles HUNT and ATTACK the player": a spawned skeleton + a player drives ticks until the
// skeleton ACQUIRES the player (attackTargetID == player.id, via the kind-routed nearestAttackableTarget
// Goal) AND the player TAKES ATTACK_DAMAGE (the kind-routed meleeAttackGoal fires doHurtTarget through
// the Phase-29 keystone). 35-04 BLOCKED because no seam existed to reference the Go-native combat goals;
// 35-01b's kind= seam wires them — this proves the seam delivers REAL melee damage.
//
// The combat lockstep RNG is owned + pinned by the Go-native goal tests (ai_goals_target_test.go); here
// we drive the END-TO-END declared-mob path. The pig oracle is UNTOUCHED (a separate mob).

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

// loadVanillaSkeletonRegistry materializes the repo-root plugins/vanilla_skeleton plugin into a temp dir
// and loads it through the host with the server-built declare_mob/goal builtins injected. Mirrors
// loadVanillaCowRegistry. Tests run with cwd=server/, so the repo-root copy sits at ../plugins/vanilla_skeleton.
func loadVanillaSkeletonRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_skeleton")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skeleton plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_skeleton", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_skeleton/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp skeleton %s: %v", name, err)
		}
	}

	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_skeleton): %v", err)
	}
	if _, ok := r.byName["vanilla_skeleton"]; !ok {
		t.Fatal("vanilla_skeleton declaration not captured after load")
	}
	return r
}

// skeletonLoop builds a physics loop with a one-chunk stone floor and the vanilla_skeleton registry installed.
func skeletonLoop(t *testing.T) (*TickLoop, int, *fakeClock) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaSkeletonRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, floorY, clock
}

// spawnSkeleton spawns the declared vanilla_skeleton via the shared spawnDeclaredMob path.
func spawnSkeleton(loop *TickLoop, x, y, z float64) *Entity {
	decl := loop.mobRegistry.byName["vanilla_skeleton"]
	return loop.spawnDeclaredMob(decl, x, y, z)
}

// --- TestSkeletonBootLoads ---------------------------------------------------------------------

// TestSkeletonBootLoads: the vanilla_skeleton plugin boot-loads, spawnSkeleton builds a live skeleton
// rendering as entity.Skeleton.ID with a non-nil AI holding the 6 goalSelector goals (restrict_sun@2 +
// flee_sun@3 + ranged_bow@4 + stroll@5 + look@6 + around@6) and the 2 targetSelector goals (hurt_by_target@1
// + nearest_attackable_target@2).
// Skeleton attribute movement_speed 0.25.
func TestSkeletonBootLoads(t *testing.T) {
	loop, floorY, _ := skeletonLoop(t)

	skel := spawnSkeleton(loop, 8.5, float64(floorY+1), 8.5)
	if skel.typ != entity.Skeleton.ID {
		t.Fatalf("skeleton typ = %d, want entity.Skeleton.ID %d (custom = behavior)", skel.typ, entity.Skeleton.ID)
	}
	if skel.ai == nil {
		t.Fatal("skeleton has no AI")
	}
	if got := len(skel.ai.goals.goals); got != 6 {
		t.Fatalf("skeleton has %d goalSelector goals, want 6 (restrict_sun@2 flee_sun@3 ranged_bow@4 stroll@5 look@6 around@6)", got)
	}
	if got := len(skel.ai.targetSelector.goals); got != 2 {
		t.Fatalf("skeleton has %d targetSelector goals, want 2 (hurt_by_target@1 + nearest_attackable_target@2)", got)
	}
	seen := map[int]int{}
	for _, wg := range skel.ai.goals.goals {
		seen[wg.priority]++
	}
	if seen[2] != 1 || seen[3] != 1 || seen[4] != 1 || seen[5] != 1 || seen[6] != 2 {
		t.Fatalf("skeleton goalSelector priorities = %v, want {2:1, 3:1, 4:1, 5:1, 6:2}", seen)
	}

	if ms := skel.attributes.GetValue(attribute.MovementSpeed.Name()); ms != 0.25 {
		t.Fatalf("skeleton movement_speed = %v, want 0.25 (AbstractSkeleton.createAttributes)", ms)
	}
}

// --- TestSkeletonBehavior (THE phase goal: hunt + attack) --------------------------------------

// TestSkeletonBehavior (THE phase goal: hunt + shoot): a spawned skeleton a few blocks from a player
// ACQUIRES the player (attackTargetID == player.id, via the kind-routed nearestAttackableTargetGoal) AND
// deals the player damage by FIRING AN ARROW (the kind-routed rangedBowAttackGoal charges 20 ticks then
// performRangedAttack spawns an AbstractArrow that flies and hits — the PROJECTILE-01 ranged path).
func TestSkeletonBehavior(t *testing.T) {
	loop, floorY, clock := skeletonLoop(t)

	if got := categoryOf(entity.Skeleton.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Skeleton) = %v, want categoryMonster (vanilla EntityType.SKELETON is MONSTER)", got)
	}

	// Place the skeleton and the player a few blocks apart so the bow goal closes to range, charges, and
	// fires an arrow along a real flight path (a 0-distance shot has no meaningful trajectory).
	skel := spawnSkeleton(loop, 8.5, float64(floorY+1), 8.5)
	skel.onGround = true

	p := combatTestPlayer(loop, 14.5, float64(floorY+1), 8.5, 7800)
	startHealth := p.health

	acquired := false
	sawArrow := false
	for i := 0; i < 400; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if skel.ai.getTarget() == p.entityID {
			acquired = true
		}
		for _, e := range loop.only().entities.byID {
			if e.isArrow {
				sawArrow = true
			}
		}
		if p.health < startHealth {
			break
		}
	}

	if !acquired {
		t.Fatal("the skeleton never ACQUIRED the player as its target — the kind-routed nearestAttackableTargetGoal did not hunt")
	}
	if !sawArrow {
		t.Fatal("the skeleton never fired an ARROW — the ranged_bow_attack goal did not performRangedAttack")
	}
	if p.health >= startHealth {
		t.Fatalf("the player took NO damage (health %v >= start %v) — the fired arrow did not hit / deal damage", p.health, startHealth)
	}
	// The arrow damage is ceil(deltaMovement.length() * baseDamage), where baseDamage = 1.0*2.0 +
	// triangle(NORMAL*0.11, 0.57425) — a small randomized positive amount. Assert the player took SOME
	// damage (a real arrow hit), not the exact melee value (the ranged path is stochastic by design).
	if dealt := startHealth - p.health; dealt <= 0 {
		t.Fatalf("player lost %v health, want > 0 (a real arrow hit)", dealt)
	}
}
