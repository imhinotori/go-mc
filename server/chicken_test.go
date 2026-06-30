package server

// chicken_test.go — Phase 34 Plan 03 (MOB-PASS-03): the gate for the THIRD 1:1 vanilla-mob dogfood.
// It proves the bundled vanilla_chicken plugin (the 8-goal Chicken.registerGoals set re-expressed 1:1,
// reusing the proven Go goal runtime + the 34-00 parameterized chicken_food handle) spawns a live
// chicken and behaves:
//
//   - TestChickenBootLoads      — the on-disk plugin loads into a registry; a spawned chicken renders
//                                 entity.Chicken.ID, its AI holds the 8 goals at the jar priorities
//                                 @0..@7, and the hitbox is the smaller 0.4x0.7 chicken box.
//   - TestChickenBehavior       — the chicken walks (the stroll goal sets a wantTarget), tempts on
//                                 chicken_food (the @3 TemptGoal fires via the 34-00 food handle), and
//                                 is the CREATURE category (entity.Chicken.Type == "creature").
//   - TestChickenSlowFallsLive  — a PLUGIN-spawned chicken, dropped (onGround=false, vy<0), has its
//                                 fall velocity damped by *0.6 by the 34-00 chickenAiStep hook — proving
//                                 the hook applies to a real spawned chicken, not just the 34-00 unit
//                                 fixture (Chicken.aiStep slow-fall: deltaMovement.multiply(1,0.6,1)).
//   - TestChickenEggLaysLive    — a plugin-spawned chicken whose eggTime is forced to 1 lays an egg the
//                                 next chickenAiStep (Chicken.aiStep egg-lay: dropFromGiftLootTable ->
//                                 the eggTime reset to nextInt(6000)+6000), proving the egg-lay applies
//                                 to a real spawned chicken.
//
// The pig oracle (TestPluginPigEqualsGoNativePig) is a SEPARATE, untouched mob — this file touches no
// pig Go file; it only loads the chicken plugin + drives the already-live 34-00 chickenAiStep hook.

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// vanillaChickenMobName is the declare_mob name the chicken plugin captures; the package const now
// lives in vanilla_pig_embed.go (Plan 34-04 boot-load generalization), shared by the spawn levers.

// loadVanillaChickenRegistry materializes the on-disk vanilla_chicken plugin (the operator-facing copy
// under repo-root plugins/vanilla_chicken/, byte-identical to the server/assets embed copy) into a
// fresh temp plugin-dir layout (root/vanilla_chicken/{plugin.toml,main.star}) and loads it through the
// SAME loadMobRegistry harness the wandermob/declare_mob tests use — returning the registry holding the
// captured "vanilla_chicken" declaration. The on-disk plugins/ dir holds OTHER plugins (crafting,
// gate_events) that need different builtins, so we copy ONLY the chicken into an isolated temp root
// rather than pointing LoadDirWith at the whole plugins/ tree. The temp dir is removed by t.Cleanup.
func loadVanillaChickenRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, vanillaChickenMobName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir chicken plugin dir: %v", err)
	}
	// The repo-root operator copy (plugins/vanilla_chicken/) is the source loaded here; it is
	// byte-identical to the server/assets embed copy (asserted by the diff acceptance criterion), so
	// loading either is equivalent. The test package runs in server/, so the repo root is one dir up.
	src := filepath.Join("..", "plugins", vanillaChickenMobName)
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatalf("read on-disk chicken %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp chicken %s: %v", name, err)
		}
	}
	r := loadMobRegistry(t, root)
	if _, ok := r.byName[vanillaChickenMobName]; !ok {
		t.Fatalf("the vanilla_chicken plugin did not declare %q after load", vanillaChickenMobName)
	}
	return r
}

// spawnVanillaChicken merges the loaded chicken declaration into the loop's tick-owned registry (the
// single-owner-at-test-setup discipline registerWanderMob uses — no tick goroutine reads it yet) and
// spawns a live chicken via the shared spawnDeclaredMob path (real chicken attrs + the declared 1:1
// goals + the 34-00 eggTime spawn-init). Returns the live Entity.
func spawnVanillaChicken(t *testing.T, loop *TickLoop, x, y, z float64) *Entity {
	t.Helper()
	if loop.mobRegistry == nil {
		t.Fatal("loop has no mob registry installed (newPhysicsLoop installs the pig registry)")
	}
	if _, ok := loop.mobRegistry.byName[vanillaChickenMobName]; !ok {
		decl, ok := loadVanillaChickenRegistry(t).byName[vanillaChickenMobName]
		if !ok {
			t.Fatal("chicken decl missing after load")
		}
		loop.mobRegistry.byName[vanillaChickenMobName] = decl
	}
	return loop.spawnDeclaredMob(loop.mobRegistry.byName[vanillaChickenMobName], x, y, z)
}

// --- TestChickenBootLoads ----------------------------------------------------------------------

// TestChickenBootLoads: the vanilla_chicken plugin loads and a spawned chicken renders entity.Chicken.ID,
// its AI holds the 8 goals at the jar priorities @0..@7 (Chicken.registerGoals), and its hitbox is the
// smaller 0.4x0.7 chicken box (auto-applied via NewEntity(decl.baseType)).
func TestChickenBootLoads(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	r := loadVanillaChickenRegistry(t)
	decl, ok := r.byName[vanillaChickenMobName]
	if !ok {
		t.Fatalf("registry has no %q declaration after load", vanillaChickenMobName)
	}
	// The declaration resolves base_type "chicken" -> entity.Chicken, with the jar attributes.
	if decl.baseType.ID != entity.Chicken.ID {
		t.Fatalf("base type = %d, want chicken %d", decl.baseType.ID, entity.Chicken.ID)
	}
	if decl.attrs["max_health"] != 4.0 {
		t.Fatalf("max_health = %v, want 4.0 (Chicken.createAttributes)", decl.attrs["max_health"])
	}
	if decl.attrs["movement_speed"] != 0.25 {
		t.Fatalf("movement_speed = %v, want 0.25 (Chicken.createAttributes)", decl.attrs["movement_speed"])
	}
	if len(decl.goals) != 8 {
		t.Fatalf("captured %d goals, want 8 (Chicken.registerGoals @0..@7)", len(decl.goals))
	}

	chicken := spawnVanillaChicken(t, loop, 8.5, float64(floorY+1), 8.5)
	if chicken.typ != entity.Chicken.ID {
		t.Fatalf("plugin chicken typ = %d, want entity.Chicken.ID %d (custom = behavior, not a new wire type)", chicken.typ, entity.Chicken.ID)
	}
	if chicken.ai == nil {
		t.Fatal("plugin chicken has no AI")
	}
	if got := len(chicken.ai.goals.goals); got != 8 {
		t.Fatalf("plugin chicken has %d goals, want 8 (Chicken.registerGoals @0..@7)", got)
	}
	// The 8 goals at the EXACT jar priorities @0..@7 (consecutive — no gaps, no duplicate Tempt).
	priorities := map[int]bool{}
	for _, wg := range chicken.ai.goals.goals {
		priorities[wg.priority] = true
	}
	for _, p := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
		if !priorities[p] {
			t.Fatalf("plugin chicken missing a goal at priority %d (got %v)", p, priorities)
		}
	}
	// The hitbox is the smaller 0.4x0.7 chicken box (the data/entity Chicken Width/Height, copied at
	// NewEntity). An adult chicken (breedAge 0) carries the full dims.
	if chicken.width != 0.4 || chicken.height != 0.7 {
		t.Fatalf("chicken hitbox = %vx%v, want 0.4x0.7 (entity.Chicken Width/Height)", chicken.width, chicken.height)
	}
	// AABB() must reflect the 0.4-wide box (half-width 0.2 on both X and Z, height 0.7). bvh.Vec3 is a
	// [3]float64 array indexed X=0, Y=1, Z=2.
	box := chicken.AABB()
	if w := box.Upper[0] - box.Lower[0]; math.Abs(w-0.4) > 1e-9 {
		t.Fatalf("chicken AABB width = %v, want 0.4", w)
	}
	if h := box.Upper[1] - box.Lower[1]; math.Abs(h-0.7) > 1e-9 {
		t.Fatalf("chicken AABB height = %v, want 0.7", h)
	}
	if _, ok := loop.regionForEntity(chicken).entities.get(chicken.id); !ok {
		t.Fatal("spawnVanillaChicken did not add the chicken to the tick-owned store")
	}
}

// --- TestChickenBehavior -----------------------------------------------------------------------

// TestChickenBehavior: the chicken WALKS (the @5 WaterAvoidingRandomStrollGoal eventually sets a
// wantTarget within ±10/±7), TEMPTS on chicken_food (the @3 TemptGoal fires via the 34-00 food handle
// and faces the player), and is the CREATURE category (the vanilla MobCategory — read from the
// authoritative data/entity Chicken.Type, which is "creature").
func TestChickenBehavior(t *testing.T) {
	// Category: the chicken is a CREATURE, like the pig (the data/entity table is the authoritative
	// vanilla category source — Chicken.Type == "creature").
	if entity.Chicken.Type != "creature" {
		t.Fatalf("entity.Chicken.Type = %q, want \"creature\" (the CREATURE MobCategory)", entity.Chicken.Type)
	}

	// WALK: drive serverAiStep until the stroll goal's 1-in-120 gate fires and a wantTarget is set.
	t.Run("walks", func(t *testing.T) {
		loop, mgr := newPhysicsLoop()
		const floorY = 64
		for cx := -1; cx <= 1; cx++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), 0})
			fillFloor(ch, floorY)
		}
		chicken := spawnVanillaChicken(t, loop, 8.5, float64(floorY+1), 8.5)
		startX, startY, startZ := chicken.x, chicken.y, chicken.z

		var fired bool
		for i := 0; i < 4000 && !fired; i++ {
			chicken.ai.serverAiStep(loop, chicken)
			if chicken.ai.hasTarget {
				fired = true
			}
		}
		if !fired {
			t.Fatal("the plugin stroll goal never set a wantTarget over 4000 ticks (the chicken never walks)")
		}
		if math.Abs(chicken.ai.wantX-startX) > 10 || math.Abs(chicken.ai.wantZ-startZ) > 10 || math.Abs(chicken.ai.wantY-startY) > 7 {
			t.Fatalf("chicken stroll wantTarget (%v,%v,%v) outside ±10/±7 of start (%v,%v,%v)",
				chicken.ai.wantX, chicken.ai.wantY, chicken.ai.wantZ, startX, startY, startZ)
		}
	})

	// TEMPT: a player holding chicken_food within range -> the @3 TemptGoal fires and the chicken faces
	// the player (the 34-00 nearest_player_holding_food("chicken_food", range) handle).
	t.Run("tempts_on_chicken_food", func(t *testing.T) {
		loop, mgr := newPhysicsLoop()
		const floorY = 64
		ch := putChunk(mgr, level.ChunkPos{0, 0})
		fillFloor(ch, floorY)

		chicken := spawnVanillaChicken(t, loop, 8.5, float64(floorY+1), 8.5)
		// A player due east holding chicken_food (seeds) within the 10.0 tempt range, but > the 2.5
		// stop distance so the chicken navigates/faces toward (not already stopped at) the player.
		chickenFoodID := firstItemInTag(t, "chicken_food")
		addPlayerHolding(loop, chicken.x+4, chicken.y, chicken.z, chickenFoodID, false)

		want := yawTowardDeg(4, 0) // facing +X toward the player
		var faced bool
		for i := 0; i < 4000 && !faced; i++ {
			chicken.ai.serverAiStep(loop, chicken)
			if math.Abs(float64(chicken.headYaw-want)) <= 0.001 && chicken.headYaw != 0 {
				faced = true
			}
		}
		if !faced {
			t.Fatalf("the chicken never faced the chicken_food player over 4000 ticks (headYaw=%v, want %v) — the @3 TemptGoal did not fire via nearest_player_holding_food", chicken.headYaw, want)
		}
	})
}

// --- TestChickenSlowFallsLive ------------------------------------------------------------------

// TestChickenSlowFallsLive: a PLUGIN-spawned chicken, set falling (onGround=false, vy<0), has its
// fall velocity damped by *0.6 when the 34-00 chickenAiStep hook runs — the SAME hook tick_phases.go
// invokes for a live entity.Chicken every tick (Chicken.aiStep: if (!onGround && m.y < 0)
// setDeltaMovement(m.multiply(1.0, 0.6, 1.0))). This proves the hook applies to a real plugin-spawned
// chicken, not just the synthetic entity in the 34-00 TestSlowFall unit fixture.
func TestChickenSlowFallsLive(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	chicken := spawnVanillaChicken(t, loop, 8.5, float64(floorY+10), 8.5) // up in the air
	if chicken.typ != entity.Chicken.ID {
		t.Fatalf("spawn produced typ %d, not a chicken", chicken.typ)
	}

	// Falling: not on the ground, moving down. The slow-fall scales ONLY the y component by 0.6.
	chicken.onGround = false
	chicken.vx = 0.123
	chicken.vy = -0.5
	chicken.vz = -0.456
	wantVY := chicken.vy * 0.6
	wantVX, wantVZ := chicken.vx, chicken.vz

	loop.chickenAiStep(chicken) // the EXACT call the tick loop makes for a live entity.Chicken

	if math.Abs(chicken.vy-wantVY) > 1e-9 {
		t.Fatalf("falling chicken vy = %v, want %v (slow-fall vy *= 0.6)", chicken.vy, wantVY)
	}
	// x/z are multiplied by 1.0 (unchanged) — only y is damped.
	if chicken.vx != wantVX || chicken.vz != wantVZ {
		t.Fatalf("slow-fall altered x/z: got vx=%v vz=%v, want vx=%v vz=%v (multiply(1.0, 0.6, 1.0) — only y scaled)", chicken.vx, chicken.vz, wantVX, wantVZ)
	}

	// A SECOND falling tick damps again (compounding *0.6 each tick while falling).
	want2 := chicken.vy * 0.6
	loop.chickenAiStep(chicken)
	if math.Abs(chicken.vy-want2) > 1e-9 {
		t.Fatalf("second falling tick vy = %v, want %v (slow-fall compounds *0.6 per tick)", chicken.vy, want2)
	}

	// Rising or on-ground: NO slow-fall (the gate is !onGround && vy < 0).
	chicken.onGround = false
	chicken.vy = 0.2 // moving UP
	loop.chickenAiStep(chicken)
	if chicken.vy != 0.2 {
		t.Fatalf("rising chicken vy = %v, want 0.2 unchanged (slow-fall only on vy < 0)", chicken.vy)
	}
	chicken.onGround = true
	chicken.vy = -0.5 // falling but ON ground
	loop.chickenAiStep(chicken)
	if chicken.vy != -0.5 {
		t.Fatalf("on-ground chicken vy = %v, want -0.5 unchanged (slow-fall only when !onGround)", chicken.vy)
	}
}

// --- TestChickenEggLaysLive --------------------------------------------------------------------

// TestChickenEggLaysLive: a plugin-spawned ADULT chicken whose eggTime is forced to 1 lays exactly one
// egg the next chickenAiStep, and the eggTime resets to the 6000..12000 window — proving the 34-00
// egg-lay applies to a real plugin-spawned chicken (Chicken.aiStep: --eggTime <= 0 ->
// dropFromGiftLootTable(CHICKEN_LAY) [v1 direct minecraft:egg] -> eggTime = nextInt(6000) + 6000).
func TestChickenEggLaysLive(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	chicken := spawnVanillaChicken(t, loop, 8.5, float64(floorY+1), 8.5)
	chicken.breedAge = 0 // an ADULT (not a baby) — the egg-lay gate requires !isBaby
	region := loop.regionForEntity(chicken)
	before := countEggItems(region)

	// Force the timer to fire on the next aiStep (vanilla decrements eggTime every tick; setting it to
	// 1 means this tick's --eggTime hits 0 and lays). The reset must land in the 6000..12000 window.
	chicken.eggTime = 1
	loop.chickenAiStep(chicken)

	after := countEggItems(region)
	if after != before+1 {
		t.Fatalf("egg count went %d -> %d, want +1 (the chicken laid exactly one egg)", before, after)
	}
	if chicken.eggTime < 6000 || chicken.eggTime > 12000 {
		t.Fatalf("eggTime reset to %d, want 6000..12000 (nextInt(6000) + 6000)", chicken.eggTime)
	}

	// A BABY chicken never lays (the !isBaby gate): force its timer and assert no new egg.
	chicken.breedAge = -24000 // a fresh baby
	chicken.eggTime = 1
	baseline := countEggItems(region)
	loop.chickenAiStep(chicken)
	if got := countEggItems(region); got != baseline {
		t.Fatalf("a baby chicken laid an egg (count %d -> %d) — the !isBaby gate is broken", baseline, got)
	}
}

// countEggItems counts the live minecraft:egg ItemEntities in a region's tick-owned store (the egg-lay
// drop is a NewItemEntity added via regionForEntity(e).entities.add — dropChickenEgg).
func countEggItems(r *region) int {
	n := 0
	for _, e := range r.entities.byID {
		if e.isItem && e.itemStack.ItemID == pk.VarInt(item.Egg.ID) {
			n++
		}
	}
	return n
}
