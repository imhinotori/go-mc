package server

// happy_ghast_test.go — the HappyGhast behavior test. Boot-load + spawn + the 2-goal decl are covered by the
// shared allFourMobs table (TestAllFourMobsBootLoad). Here we prove the distinctive FLIGHT: (1) the ghast
// HOVERS — it does NOT fall even with no floor under it (travelFlying applies no gravity), (2) it WANDERS —
// the native RandomFloatAround + GhastMoveControl move it over time, and (3) the baby age scale is 0.2375 vs
// the adult 1.0 (HappyGhast.getAgeScale). Deterministic (the per-mob rng is seeded). Pig oracle untouched.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

func loadVanillaHappyGhastRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_happy_ghast")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir happy_ghast plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_happy_ghast", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_happy_ghast/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp happy_ghast %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_happy_ghast): %v", err)
	}
	if _, ok := r.byName["vanilla_happy_ghast"]; !ok {
		t.Fatal("vanilla_happy_ghast declaration not captured after load")
	}
	return r
}

func happyGhastLoop(t *testing.T) (*TickLoop, *fakeClock, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaHappyGhastRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, clock, floorY
}

// TestHappyGhastCategoryIsCreature: the ghast is a CREATURE (extends Animal), so it consumes the CREATURE
// count budget (it never joins the NATURAL pool — dried-ghast rehydration only).
func TestHappyGhastCategoryIsCreature(t *testing.T) {
	if got := categoryOf(entity.HappyGhast.ID); got != categoryCreature {
		t.Fatalf("categoryOf(HappyGhast) = %v, want categoryCreature", got)
	}
}

// TestHappyGhastHoversNoFall: a happy ghast spawned high in open air (NO floor beneath it) does NOT fall —
// travelFlying applies no gravity, so after many ticks its Y is essentially unchanged (it hovers/drifts, it
// does not plummet). A normal mob would drop dozens of blocks over 100 ticks.
func TestHappyGhastHoversNoFall(t *testing.T) {
	loop, clock, floorY := happyGhastLoop(t)
	decl := loop.mobRegistry.byName["vanilla_happy_ghast"]
	// Spawn WAY above the floor so any gravity would drop it toward the floor unmistakably.
	spawnY := float64(floorY + 100)
	g := loop.spawnDeclaredMob(decl, 8.5, spawnY, 8.5)
	if g.typ != entity.HappyGhast.ID {
		t.Fatalf("ghast typ = %d, want entity.HappyGhast.ID %d", g.typ, entity.HappyGhast.ID)
	}
	for i := 0; i < 100; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
	}
	// A gravity-bound mob would have fallen ~50+ blocks in 100 ticks. The ghast hovers: it wanders within a
	// small band but never plummets. Assert it stayed within a generous band of its spawn Y (the
	// RandomFloatAround drift can move it, but it does not fall toward the distant floor).
	drop := spawnY - g.y
	if drop > 20.0 {
		t.Fatalf("happy ghast fell %.2f blocks in 100 ticks (spawnY=%.1f, y=%.1f) — it should HOVER, not fall", drop, spawnY, g.y)
	}
	if g.y < float64(floorY+50) {
		t.Fatalf("happy ghast sank to y=%.1f (floor at %d) — travelFlying must apply NO gravity", g.y, floorY)
	}
}

// TestHappyGhastWanders: over time the native RandomFloatAround + GhastMoveControl move the ghast away from
// its spawn point (it does not sit perfectly still). Deterministic via the seeded per-mob rng.
func TestHappyGhastWanders(t *testing.T) {
	loop, clock, floorY := happyGhastLoop(t)
	decl := loop.mobRegistry.byName["vanilla_happy_ghast"]
	spawnX, spawnY, spawnZ := 8.5, float64(floorY+40), 8.5
	g := loop.spawnDeclaredMob(decl, spawnX, spawnY, spawnZ)
	moved := false
	for i := 0; i < 300; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		dx := g.x - spawnX
		dy := g.y - spawnY
		dz := g.z - spawnZ
		if dx*dx+dy*dy+dz*dz > 1.0 {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("happy ghast never wandered from its spawn — RandomFloatAround/GhastMoveControl did not fly it")
	}
	// It must still be airborne (not have fallen through into the void).
	if g.y < float64(floorY) {
		t.Fatalf("happy ghast wandered below the floor (y=%.1f, floor=%d) — it should stay airborne", g.y, floorY)
	}
}

// TestHappyGhastBabyScale: getAgeScale is 1.0 for an adult and 0.2375 (BABY_SCALE) for a baby (breedAge<0).
func TestHappyGhastBabyScale(t *testing.T) {
	loop, _, floorY := happyGhastLoop(t)
	decl := loop.mobRegistry.byName["vanilla_happy_ghast"]
	adult := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+5), 8.5)
	if s := happyGhastAgeScale(adult); s != 1.0 {
		t.Fatalf("adult happy ghast age scale = %v, want 1.0 (MAX_SCALE)", s)
	}
	baby := loop.spawnDeclaredMob(decl, 10.5, float64(floorY+5), 10.5)
	baby.breedAge = babyStartAge // AgeableMob.BABY_START_AGE (-24000): a freshly-spawned baby
	if !baby.isBaby() {
		t.Fatal("setting breedAge = babyStartAge did not make the ghast a baby")
	}
	if s := happyGhastAgeScale(baby); s != happyGhastBabyScale {
		t.Fatalf("baby happy ghast age scale = %v, want %v (BABY_SCALE 0.2375)", s, happyGhastBabyScale)
	}
}

// TestHappyGhastFreezesWhenPlayerOnTop: a player standing on top of a happy ghast (scanPlayerAboveGhast)
// forces serverStillTimeout to 10 and marks it on-still-timeout (frozen: STAYS_STILL + not steerable).
func TestHappyGhastFreezesWhenPlayerOnTop(t *testing.T) {
	loop, _, floorY := happyGhastLoop(t)
	decl := loop.mobRegistry.byName["vanilla_happy_ghast"]
	gy := float64(floorY + 40)
	g := loop.spawnDeclaredMob(decl, 8.5, gy, 8.5)
	// A player perched on top of the ghast (within the top slab: maxY-1e-5 .. maxY+ysize/2, +/-1 in x/z).
	top := gy + float64(g.height) + 0.1
	addTestPlayer(loop, 71000, 8.5, top, 8.5)

	loop.happyGhastStillTimeoutTick(g)

	if g.ghastServerStillTimeout != happyGhastMaxStillTimeout {
		t.Fatalf("serverStillTimeout = %d, want %d after a player stood on top", g.ghastServerStillTimeout, happyGhastMaxStillTimeout)
	}
	if !happyGhastIsOnStillTimeout(g) {
		t.Fatal("ghast not on still timeout with a player on top")
	}
	if !happyGhastStaysStill(g) {
		t.Fatal("STAYS_STILL flag not set (syncStayStillFlag)")
	}
	if !g.ghastRequiresPrecisePosition {
		t.Fatal("requiresPrecisePosition not set while frozen")
	}
}

// TestHappyGhastStillTimeoutDecrements: with no player above, serverStillTimeout counts DOWN to 0 (past the
// 60-tick on-load grace), un-freezing the ghast. Verifies the tickCount>60 grace gate.
func TestHappyGhastStillTimeoutDecrements(t *testing.T) {
	loop, _, floorY := happyGhastLoop(t)
	decl := loop.mobRegistry.byName["vanilla_happy_ghast"]
	g := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+40), 8.5)
	// Arm the timeout as if a player had just been on top, and clear the load grace so it can decrement.
	happyGhastSetServerStillTimeout(g, happyGhastMaxStillTimeout)
	g.ghastTickCount = happyGhastStillLoadGrace + 1
	if !happyGhastIsOnStillTimeout(g) {
		t.Fatal("ghast should start frozen")
	}
	// No player above: it should count down to 0 within MAX_STILL_TIMEOUT ticks.
	for i := 0; i < happyGhastMaxStillTimeout+2; i++ {
		loop.happyGhastStillTimeoutTick(g)
	}
	if g.ghastServerStillTimeout != 0 {
		t.Fatalf("serverStillTimeout = %d, want 0 after counting down with no player above", g.ghastServerStillTimeout)
	}
	if happyGhastIsOnStillTimeout(g) {
		t.Fatal("ghast still frozen after the timeout decremented to 0")
	}
	if g.ghastStaysStill {
		t.Fatal("STAYS_STILL flag still set after un-freeze")
	}
}

// TestHappyGhastStillTimeoutLoadGrace: a ghast loaded WITH a still_timeout holds it for the first 60 ticks
// (STILL_TIMEOUT_ON_LOAD_GRACE_PERIOD) -- tick() only decrements once tickCount > 60.
func TestHappyGhastStillTimeoutLoadGrace(t *testing.T) {
	loop, _, floorY := happyGhastLoop(t)
	decl := loop.mobRegistry.byName["vanilla_happy_ghast"]
	g := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+40), 8.5)
	// Load path: read still_timeout = 10 (readAdditionalSaveData -> setServerStillTimeout).
	happyGhastLoadStillTimeout(g, map[string]int32{"still_timeout": happyGhastMaxStillTimeout})
	if g.ghastServerStillTimeout != happyGhastMaxStillTimeout {
		t.Fatalf("loaded serverStillTimeout = %d, want %d", g.ghastServerStillTimeout, happyGhastMaxStillTimeout)
	}
	// First 60 ticks (fresh ghast, tickCount climbing 1..60): NO decrement (still grace).
	for i := 0; i < happyGhastStillLoadGrace; i++ {
		loop.happyGhastStillTimeoutTick(g)
	}
	if g.ghastServerStillTimeout != happyGhastMaxStillTimeout {
		t.Fatalf("serverStillTimeout decremented during the 60-tick load grace: got %d, want %d", g.ghastServerStillTimeout, happyGhastMaxStillTimeout)
	}
	// The 61st tick (tickCount now 61 > 60): the first decrement.
	loop.happyGhastStillTimeoutTick(g)
	if g.ghastServerStillTimeout != happyGhastMaxStillTimeout-1 {
		t.Fatalf("serverStillTimeout = %d, want %d after the grace period ended", g.ghastServerStillTimeout, happyGhastMaxStillTimeout-1)
	}
}

// TestHappyGhastSaveStillTimeoutRoundTrip: addAdditionalSaveData writes still_timeout; readAdditionalSaveData
// funnels it back through setServerStillTimeout (re-syncing STAYS_STILL).
func TestHappyGhastSaveStillTimeoutRoundTrip(t *testing.T) {
	loop, _, floorY := happyGhastLoop(t)
	decl := loop.mobRegistry.byName["vanilla_happy_ghast"]
	src := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+40), 8.5)
	happyGhastSetServerStillTimeout(src, 7)
	out := map[string]int32{}
	happyGhastSaveStillTimeout(src, out)
	if out["still_timeout"] != 7 {
		t.Fatalf("saved still_timeout = %d, want 7", out["still_timeout"])
	}
	dst := loop.spawnDeclaredMob(decl, 20.5, float64(floorY+40), 20.5)
	happyGhastLoadStillTimeout(dst, out)
	if dst.ghastServerStillTimeout != 7 {
		t.Fatalf("loaded serverStillTimeout = %d, want 7", dst.ghastServerStillTimeout)
	}
	if !dst.ghastStaysStill {
		t.Fatal("STAYS_STILL not re-synced on load (setServerStillTimeout should have flipped it)")
	}
}
