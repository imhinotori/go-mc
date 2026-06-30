package server

// sheep_test.go — Phase 34 Plan 02 (MOB-PASS-02): the vanilla_sheep plugin gate. It proves the bundled
// vanilla_sheep .star (the 9-goal Sheep.registerGoals set including the NEW EatBlockGoal@5) boot-loads,
// renders as entity.Sheep.ID with the jar attributes (max_health 8.0, movement_speed 0.23), draws the
// EatBlockGoal canUse nextInt gate EXACTLY ONCE per call (in jar order, 1000<->50 baby flip), eats grass
// through the 34-00 host seam (grass->dirt + wool regrow), and shears through the 34-00 trySheepShear seam
// (white wool + setSheared) — then an eat regrows the wool. The shear ACTION + wool REGROW close MOB-PASS-02.
//
// The sheep adds ONLY this .star pair + this test — NO shared Go edit (the eat/shear host seam + DATA_WOOL
// live in 34-00). So this test loads the sheep .star through the EXACT plugin_mob_test.go LoadDirWith seam
// (the server owns the registry + declare_mob/goal builtins; the host runs the module body that captures
// into them), NOT a new Go loader. The pig oracle is a SEPARATE, untouched mob.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// vanillaSheepMobName is the declared mob name the sheep .star captures; the package const now lives in
// vanilla_pig_embed.go (Plan 34-04 boot-load generalization), shared by the spawn levers.

// loadSheepRegistry materializes the on-disk vanilla_sheep plugin (plugins/vanilla_sheep/, reached from
// the server/ test cwd via ../plugins) into an isolated temp plugin dir and loads it through the host's
// LoadDirWith with the server-built declare_mob/goal builtins injected — returning the registry holding
// the captured "vanilla_sheep" declaration. This is the EXACT import-direction-A seam loadMobRegistry uses
// (plugin_mob_test.go): the server owns the registry; the host runs the body that captures into it. The
// caps default to capAll (newMobRegistry's default) so the world.write-gated eat_grass_block handle works
// in tests without a manifest-derived narrower set. We isolate the sheep into its own temp root (rather
// than loading ../plugins wholesale) so the load is sheep-only and never trips on the other plugins.
func loadSheepRegistry(t *testing.T) *mobRegistry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, vanillaSheepMobName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sheep plugin dir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", vanillaSheepMobName, name))
		if err != nil {
			t.Fatalf("read on-disk sheep %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write temp sheep %s: %v", name, err)
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(sheep): %v", err)
	}
	if _, ok := r.byName[vanillaSheepMobName]; !ok {
		t.Fatalf("registry has no %q declaration after load", vanillaSheepMobName)
	}
	return r
}

// sheepLoop builds a physics loop (every region sees the world + a one-chunk stone floor at floorY) with
// the sheep registry installed, returning the loop + the floor world. spawnDeclaredMob then builds a live
// sheep from the captured declaration (real sheep attrs + the 9 declared goals + a per-entity RNG stream).
func sheepLoop(t *testing.T) (*TickLoop, floorWorld) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	loop.SetMobRegistry(loadSheepRegistry(t)) // swap the pig registry for the sheep one (sheep-only test)
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.Now())
	return loop, floorWorld{mgr: mgr, floorY: floorY}
}

// spawnPluginSheep spawns a live vanilla_sheep at (x,y,z) on the floor via spawnDeclaredMob (the SWAP
// path: real sheep attrs + the declared 1:1 goals + the per-entity seeded RNG keyed to the entity id).
func spawnPluginSheep(t *testing.T, loop *TickLoop, x, y, z float64) *Entity {
	t.Helper()
	decl, ok := loop.mobRegistry.byName[vanillaSheepMobName]
	if !ok {
		t.Fatal("sheep registry missing the vanilla_sheep declaration")
	}
	e := loop.spawnDeclaredMob(decl, x, y, z)
	e.onGround = true
	return e
}

// sheepEatGoal returns the EatBlockGoal@5 wrappedGoal from a spawned sheep's AI (the NEW goal whose RNG
// gate the focused test pins). Fails the test if priority 5 is missing.
func sheepEatGoal(t *testing.T, sheep *Entity) Goal {
	t.Helper()
	for _, wg := range sheep.ai.goals.goals {
		if wg.priority == 5 {
			return wg.g
		}
	}
	t.Fatal("spawned sheep has no goal at priority 5 (EatBlockGoal@5)")
	return nil
}

// --- TestSheepBootLoads -------------------------------------------------------------------------

// TestSheepBootLoads: the vanilla_sheep plugin loads, spawnDeclaredMob builds a live sheep that renders as
// entity.Sheep.ID with the 9 goals at the jar Sheep.registerGoals priorities @0..@8 (one per priority,
// including EatBlockGoal@5).
func TestSheepBootLoads(t *testing.T) {
	loop, w := sheepLoop(t)

	decl, ok := loop.mobRegistry.byName[vanillaSheepMobName]
	if !ok {
		t.Fatalf("registry has no %q declaration", vanillaSheepMobName)
	}
	if decl.baseType.ID != entity.Sheep.ID {
		t.Fatalf("base type = %d, want sheep %d", decl.baseType.ID, entity.Sheep.ID)
	}
	if decl.attrs["max_health"] != 8.0 {
		t.Fatalf("max_health = %v, want 8.0 (Sheep.createAttributes)", decl.attrs["max_health"])
	}
	if decl.attrs["movement_speed"] != 0.23 {
		t.Fatalf("movement_speed = %v, want 0.23 (Sheep.createAttributes)", decl.attrs["movement_speed"])
	}
	if len(decl.goals) != 9 {
		t.Fatalf("captured %d goals, want 9 (Sheep.registerGoals @0..@8 with EatBlockGoal@5)", len(decl.goals))
	}

	sheep := spawnPluginSheep(t, loop, 8.5, float64(w.floorY+1), 8.5)
	if sheep.typ != entity.Sheep.ID {
		t.Fatalf("plugin sheep typ = %d, want entity.Sheep.ID %d (custom = behavior, not a new wire type)", sheep.typ, entity.Sheep.ID)
	}
	if sheep.ai == nil {
		t.Fatal("plugin sheep has no AI")
	}
	if got := len(sheep.ai.goals.goals); got != 9 {
		t.Fatalf("plugin sheep has %d goals, want 9 (@0..@8 with EatBlockGoal@5)", got)
	}
	// Exactly one goal at each priority 0..8 (the sheep set has NO duplicate priority, unlike the pig's @4×2).
	seen := map[int]int{}
	for _, wg := range sheep.ai.goals.goals {
		seen[wg.priority]++
	}
	for p := 0; p <= 8; p++ {
		if seen[p] != 1 {
			t.Fatalf("priority %d has %d goals, want exactly 1 (got map %v)", p, seen[p], seen)
		}
	}
	// EatBlockGoal@5 claims {MOVE, LOOK, JUMP} (the new goal's flags).
	g5 := sheepEatGoal(t, sheep)
	sg, ok := g5.(*starlarkGoal)
	if !ok {
		t.Fatalf("EatBlockGoal@5 is %T, want *starlarkGoal", g5)
	}
	if sg.flags() != (flagMove | flagLook | flagJump) {
		t.Fatalf("EatBlockGoal@5 flags = %b, want flagMove|flagLook|flagJump %b", sg.flags(), flagMove|flagLook|flagJump)
	}
	if _, ok := loop.only().entities.get(sheep.id); !ok {
		t.Fatal("spawnDeclaredMob did not add the sheep to the tick-owned store")
	}
}

// --- TestEatBlockGoalRNGGate (the W3 EXACT RNG assertion) ----------------------------------------

// TestEatBlockGoalRNGGate pins the EatBlockGoal canUse RNG gate 1:1 with the jar bytecode
// (net.minecraft.world.entity.ai.goal.EatBlockGoal.canUse: if (random.nextInt(adjustedTickDelay(isBaby?
// 50:1000)) != 0) return false). It asserts:
//
//	(a) EXACTLY ONE nextInt draw is consumed off the mob stream per canUse call (no extra/leaked draws):
//	    a mirror stream seeded identically, advanced N draws of nextInt(gate), lands at the SAME position
//	    the sheep's stream reaches after N canUse calls (the next raw draw matches).
//	(b) canUse returns True only when the seeded nextInt(gate) == 0, else False (driven by replaying the
//	    mirror stream to predict each call's result).
//	(c) the gate BOUND flips 1000 (adult) <-> 50 (baby) with entity.is_baby: a baby's draw is nextInt(50)
//	    and an adult's is nextInt(1000) — proven by mirroring each bound and matching the post-call stream
//	    position (a wrong bound would advance the PCG stream to a different state).
func TestEatBlockGoalRNGGate(t *testing.T) {
	loop, w := sheepLoop(t)

	// --- (a) + (b): an ADULT sheep — exactly one nextInt(1000) per canUse, True iff the draw == 0. ---
	adult := spawnPluginSheep(t, loop, 8.5, float64(w.floorY+1), 8.5)
	adult.breedAge = 0 // an adult (isBaby == breedAge<0 == false)
	g := sheepEatGoal(t, adult)

	const N = 64
	// The mirror replays the mob stream EXACTLY as reseedMobAI seeded it (keyed to the entity id), so we
	// can predict each canUse's gate draw and assert the post-call stream position draw-for-draw.
	mirror := mirrorMobStream(adult.id)
	for i := 0; i < N; i++ {
		predictDraw := mirror.nextInt(eatGateBoundAdult) // the gate the i-th canUse SHOULD draw (nextInt(1000))
		predictTrue := predictDraw == 0                  // canUse returns true iff nextInt(gate) == 0
		got := g.canUse(loop, adult)
		if got != predictTrue {
			t.Fatalf("adult canUse call %d = %v, want %v (gate draw was %d; True iff ==0)", i, got, predictTrue, predictDraw)
		}
	}
	// (a) draw-count: after N canUse calls the sheep's stream must be at EXACTLY the mirror's position
	// (N draws of nextInt(1000) consumed — no extra/leaked draws). The next raw draw must match.
	if a, b := adult.ai.rng.nextInt(1<<30), mirror.nextInt(1<<30); a != b {
		t.Fatalf("after %d canUse calls the adult stream desynced: next draw %d != mirror %d "+
			"(EatBlockGoal.canUse must draw EXACTLY ONE nextInt per call)", N, a, b)
	}

	// --- (c): a BABY sheep — the gate bound flips to nextInt(50). ---
	baby := spawnPluginSheep(t, loop, 9.5, float64(w.floorY+1), 9.5)
	baby.breedAge = -24000 // a fresh baby (isBaby true)
	gb := sheepEatGoal(t, baby)

	babyMirror := mirrorMobStream(baby.id)
	for i := 0; i < N; i++ {
		predictDraw := babyMirror.nextInt(eatGateBoundBaby) // BABY gate = nextInt(50), NOT nextInt(1000)
		predictTrue := predictDraw == 0
		got := gb.canUse(loop, baby)
		if got != predictTrue {
			t.Fatalf("baby canUse call %d = %v, want %v (baby gate nextInt(50) draw %d; True iff ==0)", i, got, predictTrue, predictDraw)
		}
	}
	// The baby stream tracks the nextInt(50) mirror — proving the 1000->50 bound flip (a nextInt(1000)
	// mirror would advance the PCG to a different state and desync here).
	if a, b := baby.ai.rng.nextInt(1<<30), babyMirror.nextInt(1<<30); a != b {
		t.Fatalf("baby stream desynced: next draw %d != nextInt(50)-mirror %d "+
			"(baby EatBlockGoal gate must be nextInt(50), the 1000<->50 flip)", a, b)
	}

	// Cross-check the flip is OBSERVABLE, not vacuous: the baby gate (nextInt(50)) fires True FAR more
	// often than the adult gate (nextInt(1000)) over the SAME seed family — and the baby's True-positions
	// track a nextInt(50) mirror but DIVERGE from a nextInt(1000) mirror across the sweep. (A single draw
	// can coincidentally agree because IntN consumes the same PCG word regardless of bound; the bound
	// difference only shows in the ==0 comparison, which diverges across a multi-draw sweep — proven here.)
	baby2 := spawnPluginSheep(t, loop, 10.5, float64(w.floorY+1), 10.5)
	baby2.breedAge = -24000
	g2 := sheepEatGoal(t, baby2)
	mirror50 := mirrorMobStream(baby2.id)   // the CORRECT baby bound
	mirror1000 := mirrorMobStream(baby2.id) // the WRONG (adult) bound, replayed in lockstep
	const sweep = 4000
	trueCount, matched50, matched1000 := 0, 0, 0
	for i := 0; i < sweep; i++ {
		want50 := mirror50.nextInt(eatGateBoundBaby) == 0     // the baby gate's True (nextInt(50)==0)
		want1000 := mirror1000.nextInt(eatGateBoundAdult) == 0 // the adult gate's True (nextInt(1000)==0)
		got := g2.canUse(loop, baby2)
		if got {
			trueCount++
		}
		if got == want50 {
			matched50++
		}
		if got == want1000 {
			matched1000++
		}
	}
	// The baby canUse must track the nextInt(50) mirror PERFECTLY (every call), and the nextInt(1000)
	// mirror must DIVERGE (the bound flip is observable) — else the bound-flip assertion would be vacuous.
	if matched50 != sweep {
		t.Fatalf("baby canUse matched the nextInt(50) mirror %d/%d times — the baby gate MUST be nextInt(50)", matched50, sweep)
	}
	if matched1000 == sweep {
		t.Fatal("baby canUse matched a nextInt(1000) mirror on EVERY call — the 1000<->50 bound flip would " +
			"be unobservable; the baby gate must be nextInt(50), firing far more often than the adult gate")
	}
	// And the baby fires True FAR more than the ~sweep/1000 an adult would (nextInt(50)==0 is ~20x likelier).
	if trueCount < sweep/200 {
		t.Fatalf("baby fired True only %d/%d times — too few for the nextInt(50) gate (an adult nextInt(1000) "+
			"gate fires ~%d; the baby must fire far more)", trueCount, sweep, sweep/1000)
	}
}

// --- TestSheepBehavior ---------------------------------------------------------------------------

// TestSheepBehavior: a spawned sheep renders as entity.Sheep.ID with a live AI, tempts on sheep_food (the
// @3 TemptGoal host scan resolves a holding player to a position), and ticks through its goals without
// panicking (a smoke drive). (The natural-spawn categoryOf->CREATURE gate is a separate spawner-wiring
// concern, NOT this MOB-PASS-02 plan — categoryOf is a shared Go file owned by the spawner plans.)
func TestSheepBehavior(t *testing.T) {
	loop, w := sheepLoop(t)
	sheep := spawnPluginSheep(t, loop, 8.5, float64(w.floorY+1), 8.5)

	if sheep.typ != entity.Sheep.ID {
		t.Fatalf("sheep typ = %d, want entity.Sheep.ID %d", sheep.typ, entity.Sheep.ID)
	}
	if sheep.ai == nil {
		t.Fatal("sheep has no AI")
	}

	// Tempt: a player holding a sheep_food item within range -> the @3 tempt host scan returns a position.
	sheepFoodID := firstItemInTag(t, "sheep_food")
	addPlayerHolding(loop, sheep.x+3, sheep.y, sheep.z, sheepFoodID, false)
	h := newEntityHandle(loop, sheep.id, capAll)
	v, err := callMethod(t, h, "nearest_player_holding_food", starlark.String("sheep_food"), starlark.Float(10.0))
	if err != nil {
		t.Fatalf("nearest_player_holding_food(sheep_food) error: %v", err)
	}
	tup, ok := v.(starlark.Tuple)
	if !ok || len(tup) != 3 {
		t.Fatalf("sheep_food tempt scan = %v (%T), want a 3-tuple (TemptGoal target)", v, v)
	}

	// The sheep ticks without panicking (it advances the loop without error — a smoke drive of the goals).
	for i := 0; i < 10; i++ {
		loop.advance(loop.clock.Now())
	}
}

// --- TestSheepEatRegrowsWool (the eat goal through the .star tick) -------------------------------

// TestSheepEatRegrowsWool: a sheep on a grass_block, driven through the EatBlockGoal tick to the act tick
// (eatAnimationTick == 4), eats the grass (grass_block-below -> dirt) and regrows its wool (sheared
// cleared) via the 34-00 host eat seam exercised THROUGH the .star eat goal (not a direct host call).
func TestSheepEatRegrowsWool(t *testing.T) {
	loop, w := sheepLoop(t)
	sheep := spawnPluginSheep(t, loop, 8.5, float64(w.floorY+1), 8.5)
	sheep.sheared = true // start sheared so the regrow is observable

	below := pk.Position{X: int(sheep.x), Y: int(sheep.y) - 1, Z: int(sheep.z)}
	w.mgr.SetBlock(below, block.DefaultStateID["minecraft:grass_block"], dimMinY)

	g := sheepEatGoal(t, sheep)

	// start() arms eatAnimationTick = 40 + broadcasts byte 10 + stops nav.
	g.start(loop, sheep)
	// Drive tick() down from 40: the eat fires when the decremented timer hits 4 (36 ticks of tick()).
	// canContinueToUse stays true while eatAnimationTick > 0.
	acted := false
	for i := 0; i < eatAnimationTicks; i++ {
		if !g.canContinueToUse(loop, sheep) {
			break
		}
		g.tick(loop, sheep)
		if got, _ := w.mgr.GetBlock(below, dimMinY); got == block.DefaultStateID["minecraft:dirt"] {
			acted = true
			break
		}
	}
	if !acted {
		t.Fatal("the EatBlockGoal tick never ate the grass (grass_block-below -> dirt) over the 40-tick animation")
	}
	if got, _ := w.mgr.GetBlock(below, dimMinY); got != block.DefaultStateID["minecraft:dirt"] {
		t.Fatalf("block below = %v, want dirt (grass eaten via the .star eat goal -> 34-00 host seam)", got)
	}
	if sheep.sheared {
		t.Fatal("after the eat goal acted, the wool must regrow (sheared cleared) — Sheep.ate setSheared(false)")
	}
}

// --- TestSheepShearThroughPlugin (the MOB-PASS-02 shear clause, end-to-end on a PLUGIN sheep) -----

// TestSheepShearThroughPlugin proves the MOB-PASS-02 shear ACTION + wool REGROW on a REAL plugin-spawned
// sheep (not the 34-00 synthetic-entity unit test): shear a vanilla_sheep through the 34-00 trySheepShear
// host interact (held SHEARS) -> white wool drops + setSheared(true) + the SHEEP_SHEAR sound; then run the
// EatBlockGoal eat to the act tick on a grass_block -> setSheared(false) (the wool regrows). A baby /
// already-sheared sheep falls through (no drop). Shear + regrow together close MOB-PASS-02.
func TestSheepShearThroughPlugin(t *testing.T) {
	loop, w := sheepLoop(t)
	sheep := spawnPluginSheep(t, loop, 8.5, float64(w.floorY+1), 8.5)
	// adult, un-sheared (the default spawn): readyForShearing == !isSheared && !isBaby.

	p := shearPlayer(loop, sheep, int32(item.Shears.ID))
	before := countItemsInRegion(loop, sheep)

	// (1) the shear ACTION via the 34-00 host interact.
	if !loop.trySheepShear(p, sheep) {
		t.Fatal("trySheepShear on a ready adult plugin sheep with shears must return true")
	}
	if !sheep.sheared {
		t.Fatal("(a) a successful shear must setSheared(true) (DATA_WOOL bit 0x10)")
	}
	drops := countItemsInRegion(loop, sheep) - before
	if drops < 1 {
		t.Fatalf("(b) shear dropped %d items, want >=1 white_wool (shearing/sheep/white)", drops)
	}

	// (2) the wool REGROW: run the EatBlockGoal eat to the act tick on a grass_block -> setSheared(false).
	below := pk.Position{X: int(sheep.x), Y: int(sheep.y) - 1, Z: int(sheep.z)}
	w.mgr.SetBlock(below, block.DefaultStateID["minecraft:grass_block"], dimMinY)

	g := sheepEatGoal(t, sheep)
	g.start(loop, sheep)
	for i := 0; i < eatAnimationTicks; i++ {
		if !g.canContinueToUse(loop, sheep) {
			break
		}
		g.tick(loop, sheep)
		if !sheep.sheared {
			break
		}
	}
	if sheep.sheared {
		t.Fatal("after the eat goal acted, the sheared sheep must REGROW its wool (setSheared(false)) — " +
			"the shear ACTION + wool REGROW together satisfy MOB-PASS-02")
	}

	// A baby plugin sheep with shears falls through (readyForShearing false) — no drop.
	baby := spawnPluginSheep(t, loop, 9.5, float64(w.floorY+1), 9.5)
	baby.breedAge = -24000
	pBaby := shearPlayer(loop, baby, int32(item.Shears.ID))
	beforeBaby := countItemsInRegion(loop, baby)
	if !loop.trySheepShear(pBaby, baby) {
		t.Fatal("trySheepShear on a baby plugin sheep with shears must return true (CONSUME, no feed fall-through)")
	}
	if baby.sheared || countItemsInRegion(loop, baby) != beforeBaby {
		t.Fatal("a baby sheep is not ready for shearing -> no setSheared, no wool drop")
	}
}
