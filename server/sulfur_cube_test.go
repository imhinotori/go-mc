package server

// sulfur_cube_test.go - MOB-CUBE (SulfurCube, a NEW 26.2 mob): the per-mob behavior test. The CORE
// assertions: (1) the SIZE MACHINE at spawn (SulfurCube.setSpawnSize -> size 2 -> MAX_HEALTH 8,
// MOVEMENT_SPEED 0.4, dims 0.98); (2) the JUMP-MOVE state machine (the CubeMobMoveControl arms a jump via
// the JumpControl on its delay countdown); (3) the SPLIT-ON-DEATH (a killed size-2 cube spawns 2 baby
// size-1 cubes with MAX_HEALTH 4). Deterministic via the seeded per-entity RNG (newEntityRandom pattern).
// The pig oracle is a SEPARATE mob and is untouched.

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

// loadVanillaSulfurCubeRegistry materializes the repo-root plugins/mobs/vanilla_sulfur_cube plugin into a
// temp dir and loads it with the server-built declare_mob/goal builtins injected (the creeper-loader shape).
func loadVanillaSulfurCubeRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_sulfur_cube")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sulfur_cube plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_sulfur_cube", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_sulfur_cube/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp sulfur_cube %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_sulfur_cube): %v", err)
	}
	if _, ok := r.byName["vanilla_sulfur_cube"]; !ok {
		t.Fatal("vanilla_sulfur_cube declaration not captured after load")
	}
	return r
}

// TestSulfurCubeSizeMachine: a spawned adult sulfur cube is size 2 with the size-scaled MAX_HEALTH (4*2=8),
// MOVEMENT_SPEED (0.2+0.1*2=0.4) and dims (0.49*2=0.98) - the SulfurCube.setSpawnSize + AbstractCubeMob.setSize
// / SulfurCube.setcubeMobHealth machine.
func TestSulfurCubeSizeMachine(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaSulfurCubeRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.SulfurCube.ID); got != categoryMonster {
		t.Fatalf("categoryOf(SulfurCube) = %v, want categoryMonster", got)
	}

	decl := loop.mobRegistry.byName["vanilla_sulfur_cube"]
	cube := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	if cube.typ != entity.SulfurCube.ID {
		t.Fatalf("cube typ = %d, want entity.SulfurCube.ID %d", cube.typ, entity.SulfurCube.ID)
	}
	if cube.cubeSize != 2 {
		t.Fatalf("spawn size = %d, want 2 (SulfurCube.setSpawnSize adult)", cube.cubeSize)
	}
	if got := cube.getAttributeValue(attribute.MaxHealth); got != 8 {
		t.Fatalf("MAX_HEALTH = %v, want 8 (SulfurCube.setcubeMobHealth 4*size)", got)
	}
	if cube.health != 8 {
		t.Fatalf("health = %v, want 8 (setSize updateHealth)", cube.health)
	}
	if got := cube.getAttributeValue(attribute.MovementSpeed); got < 0.399 || got > 0.401 {
		t.Fatalf("MOVEMENT_SPEED = %v, want ~0.4 (AbstractCubeMob.setSize 0.2+0.1*size)", got)
	}
	if cube.width < 0.979 || cube.width > 0.981 {
		t.Fatalf("width = %v, want ~0.98 (0.49*size)", cube.width)
	}
	if cube.isBaby() {
		t.Fatal("a size-2 cube must NOT be a baby (only size 1 is)")
	}
}

// TestSulfurCubeJumpMove: a grounded sulfur cube's CubeMobMoveControl arms a jump (via the JumpControl) on
// its jumpDelay countdown, and the serverAiStep JUMP slot turns it into a real upward impulse - so the cube
// HOPS (vy goes positive at least once) over a window. This exercises the jump-move state machine end to end.
func TestSulfurCubeJumpMove(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaSulfurCubeRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_sulfur_cube"]
	cube := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	cube.onGround = true

	hopped := false
	for i := 0; i < 120; i++ { // > the max jumpDelay (10+nextInt(20) <= 29)
		clock.add(tickStep)
		loop.advance(clock.Now())
		if cube.vy > 0.3 { // jumpFromGround sets vy = 0.42 on a hop
			hopped = true
			break
		}
	}
	if !hopped {
		t.Fatalf("the sulfur cube never HOPPED (vy never went positive over 120 ticks) - the CubeMobMoveControl jump-move state machine did not arm a jump")
	}
}

// TestSulfurCubeSplitOnDeath: a killed size-2 sulfur cube spawns SPLIT_COUNT(2) baby size-1 cubes with the
// size-scaled MAX_HEALTH 4 (SulfurCube.setcubeMobHealth 4*1) - the AbstractCubeMob.remove() split. Driven
// via dieEntity + the tickDeath countdown (the split fires at deathTime>=20, JUST BEFORE the store removal).
func TestSulfurCubeSplitOnDeath(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaSulfurCubeRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_sulfur_cube"]
	cube := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	parentID := cube.id

	// Kill the cube (dieEntity marks it dead + seeds the death-animation countdown). Run in the owner region.
	loop.withRegion(loop.only(), func() {
		loop.dieEntity(cube, damageSourceOf(damageTypeGeneric))
	})
	if !cube.dead {
		t.Fatal("dieEntity did not mark the cube dead")
	}

	// Advance until the parent is removed (tickDeath at deathTime>=20 runs the split then removes the parent).
	removed := false
	for i := 0; i < 40; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if _, ok := loop.only().entities.byID[parentID]; !ok {
			removed = true
			break
		}
	}
	if !removed {
		t.Fatal("the parent sulfur cube was never removed (tickDeath did not reach deathTime>=20)")
	}

	// Count the surviving sulfur cubes: exactly SPLIT_COUNT(2) baby size-1 cubes, each MAX_HEALTH 4.
	var children []*Entity
	for _, e := range loop.only().entities.byID {
		if e.typ == entity.SulfurCube.ID {
			children = append(children, e)
		}
	}
	if len(children) != sulfurCubeSplitCount {
		t.Fatalf("split spawned %d cubes, want %d (SulfurCube.SPLIT_COUNT)", len(children), sulfurCubeSplitCount)
	}
	for _, c := range children {
		if c.cubeSize != 1 {
			t.Fatalf("split child size = %d, want 1 (halfSize of 2)", c.cubeSize)
		}
		if !c.isBaby() {
			t.Fatal("split child must be a baby (SulfurCube.setUpSplitCube setBaby(true))")
		}
		if got := c.getAttributeValue(attribute.MaxHealth); got != 4 {
			t.Fatalf("split child MAX_HEALTH = %v, want 4 (setcubeMobHealth 4*1)", got)
		}
		if c.health != 4 {
			t.Fatalf("split child health = %v, want 4", c.health)
		}
	}
}
