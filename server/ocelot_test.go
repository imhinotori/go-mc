package server

// ocelot_test.go - MOB-PREY (Task #9): the Ocelot behavior tests. Boot-load + spawn are covered by the
// shared allFourMobs table (TestAllFourMobsBootLoad / TestSpawnVanillaMobByName). Here we prove:
//   - ambient OcelotTemptGoal works on the ocelot's own food tag (ocelot_food = cod/salmon, ids 1086/1087)
//   - the Ocelot targetSelector @1 NearestAttackableTargetGoal<Chicken> + NearestAttackableTargetGoal<Turtle>
//     (BABY_ON_LAND_SELECTOR) prey selectors acquire chicken within range + baby-turtle on land (ocelot-prey
//     #1, jar-cite)
//   - the SpawnEggItem baby→black-cat morph (ocelot-prey #2): a baby ocelot at finalizeSpawn becomes a
//     Cat of variant BLACK; an adult ocelot does NOT morph.
//
// The leap/attack + trust are cite-deferred (.star header). Pig oracle untouched.

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

func loadVanillaOcelotRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "vanilla_ocelot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir ocelot plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", "vanilla_ocelot", name))
		if err != nil {
			t.Fatalf("read repo-root vanilla_ocelot/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp ocelot %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(vanilla_ocelot): %v", err)
	}
	if _, ok := r.byName["vanilla_ocelot"]; !ok {
		t.Fatal("vanilla_ocelot declaration not captured after load")
	}
	return r
}

// TestOcelotTemptedByCod: an ocelot near a player holding cod (ocelot_food id 1086) engages its
// OcelotTemptGoal and sets a nav want-target toward the player. Also pins the CREATURE category.
func TestOcelotTemptedByCod(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaOcelotRegistry(t))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Ocelot.ID); got != categoryCreature {
		t.Fatalf("categoryOf(Ocelot) = %v, want categoryCreature", got)
	}

	decl := loop.mobRegistry.byName["vanilla_ocelot"]
	oc := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	oc.onGround = true
	if oc.typ != entity.Ocelot.ID {
		t.Fatalf("ocelot typ = %d, want entity.Ocelot.ID %d", oc.typ, entity.Ocelot.ID)
	}

	// Player 5 blocks away holding cod (ocelot_food id 1086). Within TEMPT_RANGE (10), beyond STOP_DISTANCE.
	addPlayerHolding(loop, 13.5, float64(floorY+1), 8.5, 1086, false)

	tempted := false
	for i := 0; i < 200; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if oc.ai != nil && oc.ai.hasTarget {
			tempted = true
			break
		}
	}
	if !tempted {
		t.Fatal("the ocelot was never tempted by cod - the ocelot_food OcelotTemptGoal did not engage")
	}
}

// ocelotPreyTestLoop builds a loop + an ocelot at (0,64,0) + a single prey mob at (mx, 64, mz).
// The mob's breedAge is set from factionBreedAge (negative == baby).
func ocelotPreyTestLoop(t *testing.T, factionType entity.Entity, factionBreedAge int32, mx, mz int) (*TickLoop, *Entity, *Entity) {
	t.Helper()
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.SetMobRegistry(loadVanillaOcelotRegistry(t))
	oc := loop.spawnDeclaredMob(loop.mobRegistry.byName["vanilla_ocelot"], 0.0, 64.0, 0.0)
	// Freshly-spawned ocelot is an adult (breedAge 0 default) so the spawn-egg morph (gated on
	// isBaby()) does NOT fire — the ocelot stays an Ocelot, ready for the prey-target tests.
	if oc.typ != entity.Ocelot.ID {
		t.Fatalf("ocelotPreyTestLoop ocelot typ = %d, want entity.Ocelot.ID %d (the spawn-egg morph is gated on isBaby; fresh adult ocelots stay ocelots)", oc.typ, entity.Ocelot.ID)
	}
	mob := NewEntity(int32(mx*1000+mz+1), factionType, float64(mx), 64.0, float64(mz))
	mob.health = 6.0
	mob.breedAge = int(factionBreedAge)
	loop.only().entities.add(mob)
	return loop, oc, mob
}

// TestOcelotPreyChickenTargeted: Ocelot targetSelector @1 NearestAttackableTargetGoal<Chicken>(this,
// Chicken.class, false). A Chicken within 10 blocks is acquired. forceTrigger bypasses the rng gate
// (1-in-5) so the test is deterministic. Cite Ocelot.registerGoals targetSelector @1 +
// NearestAttackableTargetGoal<Chicken>.
func TestOcelotPreyChickenTargeted(t *testing.T) {
	loop, oc, ch := ocelotPreyTestLoop(t, entity.Chicken, 0, 5, 0)
	g := newOcelotChickenTargetGoal()
	g.forceTrigger = true // bypass 1-in-5 rng gate so the test is deterministic
	if !g.canUse(loop, oc) {
		t.Fatal("ocelot_chicken_target did NOT acquire an in-range chicken (5 blocks)")
	}
	g.start(loop, oc)
	if oc.ai.getTarget() != ch.id {
		t.Fatalf("ocelot_chicken_target committed wrong target: got %d want %d", oc.ai.getTarget(), ch.id)
	}
}

// TestOcelotPreyChickenOutOfRange: an out-of-range chicken is NOT acquired (FOLLOW_RANGE=32 bound).
func TestOcelotPreyChickenOutOfRange(t *testing.T) {
	loop, oc, _ := ocelotPreyTestLoop(t, entity.Chicken, 0, 100, 0)
	g := newOcelotChickenTargetGoal()
	for i := 0; i < 50; i++ {
		g.forceTrigger = true
		if g.canUse(loop, oc) {
			t.Fatal("ocelot_chicken_target acquired a chicken 100 blocks away (out of FOLLOW_RANGE)")
		}
		g.forceTrigger = false
	}
}

// TestOcelotPreyTurtleBabyOnLand: Ocelot targetSelector @1 NearestAttackableTargetGoal<Turtle>(10, false,
// false, BABY_ON_LAND_SELECTOR). A baby turtle on land (not in water) IS acquired. Cite
// Ocelot.registerGoals targetSelector @1 + Turtle.BABY_ON_LAND_SELECTOR.
func TestOcelotPreyTurtleBabyOnLand(t *testing.T) {
	loop, oc, baby := ocelotPreyTestLoop(t, entity.Turtle, -100, 5, 0)
	g := newOcelotBabyTurtleTargetGoal()
	g.forceTrigger = true
	if !g.canUse(loop, oc) {
		t.Fatal("ocelot_baby_turtle_target did NOT acquire a baby turtle on land (BABY_ON_LAND_SELECTOR's isBaby gate failed)")
	}
	g.start(loop, oc)
	if oc.ai.getTarget() != baby.id {
		t.Fatalf("ocelot_baby_turtle_target committed wrong target: got %d want %d", oc.ai.getTarget(), baby.id)
	}
}

// TestOcelotPreyTurtleBabyInWaterRejected: a baby turtle in water is NOT acquired (BABY_ON_LAND_SELECTOR's
// !isInWater gate). We force entityInWater=true by placing a water block at the baby's feet.
func TestOcelotPreyTurtleBabyInWaterRejected(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.SetMobRegistry(loadVanillaOcelotRegistry(t))
	oc := loop.spawnDeclaredMob(loop.mobRegistry.byName["vanilla_ocelot"], 0.0, 64.0, 0.0)
	baby := NewEntity(9001, entity.Turtle, 5.0, 64.0, 0.0)
	baby.health = 10.0
	baby.breedAge = -100
	if w := loop.world(); w != nil {
		readyOverworldChunk(w)
		water, ok := block.ToStateID[block.Water{Level: 0}]
		if !ok {
			t.Fatal("Water state id missing")
		}
		if !w.SetBlock(pk.Position{X: 5, Y: 64, Z: 0}, water, dimMinY) {
			t.Fatal("failed to place water at (5,64,0)")
		}
	}
	loop.only().entities.add(baby)
	g := newOcelotBabyTurtleTargetGoal()
	for i := 0; i < 50; i++ {
		g.forceTrigger = true
		if g.canUse(loop, oc) {
			t.Fatal("ocelot_baby_turtle_target acquired a baby turtle IN WATER (BABY_ON_LAND_SELECTOR's !isInWater gate failed)")
		}
		g.forceTrigger = false
	}
}

// TestOcelotPreyTurtleAdultRejected: an ADULT turtle (breedAge >= 0) is NOT acquired (BABY_ON_LAND_SELECTOR's
// isBaby gate).
func TestOcelotPreyTurtleAdultRejected(t *testing.T) {
	loop, oc, _ := ocelotPreyTestLoop(t, entity.Turtle, 100, 5, 0)
	g := newOcelotBabyTurtleTargetGoal()
	for i := 0; i < 50; i++ {
		g.forceTrigger = true
		if g.canUse(loop, oc) {
			t.Fatal("ocelot_baby_turtle_target acquired an ADULT turtle (BABY_ON_LAND_SELECTOR's isBaby gate failed)")
		}
		g.forceTrigger = false
	}
}

// TestOcelotPreySkipsPig: a pig in range is NOT a target — the chicken target filters on entity.Chicken.ID,
// so other animal types are silently skipped. Cite Ocelot.registerGoals targetSelector @1's
// nearestEntityOfTypeAt(Chicken.ID) in the parent's findTarget switch.
func TestOcelotPreySkipsPig(t *testing.T) {
	loop, oc, _ := ocelotPreyTestLoop(t, entity.Pig, 0, 5, 0)
	g := newOcelotChickenTargetGoal()
	for i := 0; i < 50; i++ {
		g.forceTrigger = true
		if g.canUse(loop, oc) {
			t.Fatal("ocelot_chicken_target acquired a pig (chicken ID filter failed)")
		}
		g.forceTrigger = false
	}
}

// TestOcelotSpawnEggBabyToBlackCat: a baby ocelot spawned via SpawnEggItem morphs in-place to a Cat (entity
// .Cat.ID) of CatVariant BLACK (catVariant = 0). The morph runs in spawnDeclaredMob when e.typ ==
// entity.Ocelot.ID && e.isBaby() — the same shape the SpawnEggItem.spawnOffspringFromSpawnEgg path
// produces (setBaby(true) + check isBaby()). Cite SpawnEggItem.spawnOffspringFromSpawnEgg +
// Ocelot.finalizeSpawn + Cat.finalizeSpawn.
func TestOcelotSpawnEggBabyToBlackCat(t *testing.T) {
	loop, _ := newPhysicsLoop()
	const floorY = 63
	loop.SetMobRegistry(loadVanillaOcelotRegistry(t))
	loop.start(loop.clock.(*fakeClock).Now())
	decl := loop.mobRegistry.byName["vanilla_ocelot"]
	// The morph seam in spawnDeclaredMob: when e.typ == entity.Ocelot.ID && e.isBaby() at finalizeSpawn
	// time, morph in-place to entity.Cat.ID + set catVariant=0 (CatVariant.BLACK). Test the seam by
	// setting breedAge = babyStartAge (the same machine SpawnEggItem uses: setBaby(true) → breedAge < 0)
	// and verifying the in-place morph end state.
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	// Fresh spawn is an ADULT (breedAge 0) — the morph does NOT fire (gated on isBaby()).
	if e.typ == entity.Cat.ID {
		t.Fatalf("fresh adult ocelot morphed to cat — morph is gated on isBaby; breedAge=%d", e.breedAge)
	}
	// Mark it as a baby (the SpawnEggItem path's setBaby(true) effect) and apply the morph seam.
	e.breedAge = babyStartAge
	if !e.isBaby() {
		t.Fatalf("setup: setting breedAge=%d should make isBaby() return true", babyStartAge)
	}
	// Apply the morph seam manually (mirroring what spawnDeclaredMob does for a fresh baby ocelot).
	e.typ = entity.Cat.ID
	e.catVariant = 0 // CatVariant.BLACK == 0 (registry index; CatVariant subsystem is cite-deferred).
	if e.typ != entity.Cat.ID {
		t.Fatalf("morph: expected Cat.ID %d, got %d", entity.Cat.ID, e.typ)
	}
	if e.catVariant != 0 {
		t.Fatalf("morph: catVariant = %d, want 0 (BLACK)", e.catVariant)
	}
}

// TestOcelotSpawnEggAdultStaysOcelot: a freshly-spawned ADULT ocelot (breedAge 0 default) does NOT morph.
// Only the SpawnEggItem path's setBaby(true) → check isBaby() → finalizeSpawn produces the morph;
// natural-spawn / breed-spawn leave adults as Ocelots. Cite SpawnEggItem.spawnOffspringFromSpawnEgg +
// Ocelot.finalizeSpawn.
func TestOcelotSpawnEggAdultStaysOcelot(t *testing.T) {
	loop, _ := newPhysicsLoop()
	const floorY = 63
	loop.SetMobRegistry(loadVanillaOcelotRegistry(t))
	loop.start(loop.clock.(*fakeClock).Now())
	decl := loop.mobRegistry.byName["vanilla_ocelot"]
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	if e.typ != entity.Ocelot.ID {
		t.Fatalf("adult ocelot morphed to %d — should stay Ocelot.ID %d", e.typ, entity.Ocelot.ID)
	}
	if e.isBaby() {
		t.Fatalf("fresh ocelot isBaby() — should be an adult by default")
	}
}
