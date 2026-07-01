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
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
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

// --- MOB-PREY (Task #9): the now-live water-nav + home-pos + egg-lay goals ------------------------
// These pin the goals BUILT this batch (ai_goals_turtle.go): homePos set at spawn (Turtle.finalizeSpawn),
// the TURTLE_EGG block placement (TurtleLayEggGoal.tick -> level.setBlock), and the GoHome home-bias.
// All are deterministic (fixed floor + seeded per-entity rng via reseedMobAI at spawn).

// TestTurtleHomePosSetAtSpawn: a spawned turtle records its spawn column as homePos (Turtle.finalizeSpawn
// setHomePos(blockPosition())). Cite Turtle.finalizeSpawn.
func TestTurtleHomePosSetAtSpawn(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaTurtleRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_turtle"]
	tr := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	if !tr.homePosSet {
		t.Fatal("turtle homePos was not set at spawn (Turtle.finalizeSpawn setHomePos)")
	}
	wantX, wantY, wantZ := floorI(8.5), floorI(float64(floorY+1)), floorI(8.5)
	if tr.homePosX != wantX || tr.homePosY != wantY || tr.homePosZ != wantZ {
		t.Fatalf("homePos = (%d,%d,%d), want spawn block (%d,%d,%d)", tr.homePosX, tr.homePosY, tr.homePosZ, wantX, wantY, wantZ)
	}
}

// TestPlaceTurtleEgg: placeTurtleEgg writes the minecraft:turtle_egg state (HATCH=0, EGGS=count) into the
// world at the given pos. Pins the codegen'd block + the state-id resolution (eggs 1..4). Cite
// TurtleLayEggGoal.tick level.setBlock(eggPos, TURTLE_EGG.defaultBlockState().setValue(EGGS, count), 3).
func TestPlaceTurtleEgg(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaTurtleRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	for count := 1; count <= 4; count++ {
		ex, ey, ez := 2+count, floorY+1, 3
		loop.placeTurtleEgg(ex, ey, ez, count)
		got, ok := mgr.GetBlock(pk.Position{X: ex, Y: ey, Z: ez}, dimMinY)
		if !ok {
			t.Fatalf("count=%d: turtle_egg block not readable after place", count)
		}
		want, _ := block.ToStateID[block.TurtleEgg{Eggs: block.Integer(count), Hatch: 0}]
		if got != want {
			t.Fatalf("count=%d: placed state %d, want TurtleEgg{eggs=%d} = %d", count, got, count, want)
		}
	}
}

// TestTurtleLayEggGoalPlacesEgg: a turtle carrying an egg (hasEgg), home nearby, standing ON sand, runs
// TurtleLayEggGoal to completion: it digs (layingEgg true, layEggCounter climbs) and after >200 ticks
// places a TURTLE_EGG one block above the sand, clearing hasEgg + layingEgg and arming the breed cooldown.
// Deterministic (fixed sand floor, seeded rng). Cite TurtleLayEggGoal.tick.
func TestTurtleLayEggGoalPlacesEgg(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	// Replace the floor cell under the turtle with SAND so isValidTarget (isSand + above-empty) passes.
	sandX, sandZ := 8, 8
	mgr.SetBlock(pk.Position{X: sandX, Y: floorY, Z: sandZ}, block.ToStateID[block.Sand{}], dimMinY)
	loop.SetMobRegistry(loadVanillaTurtleRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_turtle"]
	tr := loop.spawnDeclaredMob(decl, float64(sandX)+0.5, float64(floorY+1), float64(sandZ)+0.5)
	tr.onGround = true
	// Arm the egg-lay: hasEgg + home == the turtle's own column (within 9), so canUse fires immediately.
	tr.hasEgg = true
	setTurtleHomePos(tr, sandX, floorY+1, sandZ)

	eggPos := pk.Position{X: sandX, Y: floorY + 1, Z: sandZ}
	placed := false
	for i := 0; i < 400; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if got, ok := mgr.GetBlock(eggPos, dimMinY); ok {
			if _, isEgg := block.StateList[got].(block.TurtleEgg); isEgg {
				placed = true
				break
			}
		}
	}
	if !placed {
		t.Fatalf("TurtleLayEggGoal never placed a turtle_egg at %v (layEggCounter=%d layingEgg=%v hasEgg=%v)", eggPos, tr.layEggCounter, tr.layingEgg, tr.hasEgg)
	}
	if tr.hasEgg {
		t.Fatal("hasEgg should be cleared after laying")
	}
	if tr.layingEgg {
		t.Fatal("layingEgg should be cleared after laying")
	}
	if tr.inLove <= 0 {
		t.Fatal("setInLoveTime(600) should have armed the breed cooldown after laying")
	}
}

// TestTurtleGoHomeGoalHomesWithEgg: an egg-carrying turtle far from home engages TurtleGoHomeGoal
// (canUse returns true unconditionally for hasEgg), setting goingHome and a nav want-target toward home.
// Cite Turtle.registerGoals @4 TurtleGoHomeGoal / TurtleGoHomeGoal.canUse (hasEgg -> true).
func TestTurtleGoHomeGoalHomesWithEgg(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaTurtleRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_turtle"]
	tr := loop.spawnDeclaredMob(decl, 2.5, float64(floorY+1), 2.5)
	tr.onGround = true
	// Home far away (> 7 so canContinueToUse holds; NOT laying — the turtle should head home).
	tr.hasEgg = true
	setTurtleHomePos(tr, 13, floorY+1, 13)
	// No sand anywhere, so LayEgg cannot fire; GoHome (priority 4) should engage and set goingHome.

	homed := false
	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if tr.goingHome {
			homed = true
			break
		}
	}
	if !homed {
		t.Fatal("an egg-carrying turtle far from home never engaged TurtleGoHomeGoal (goingHome stayed false)")
	}
}
