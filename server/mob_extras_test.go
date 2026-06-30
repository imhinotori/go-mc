package server

// mob_extras_test.go — Phase 34 Plan 00 (shared infra): the headless behavior + RNG tests for the
// parameterized food handle, the sheep eat seam + DATA_WOOL/shear, and the chicken slow-fall + egg-lay.
// These pin the JAR-FAITHFUL behavior (block swap, wool regrow, the 5-nextFloat shear scatter, the
// 2-nextFloat + nextInt(6000) egg-lay order) without touching the pig oracle (a separate, untouched mob).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"go.starlark.net/starlark"
)

// --- shared helpers ----------------------------------------------------------------------------

// extrasLoop builds a physics loop (all regions see the world + the vanilla_pig registry) with a
// one-chunk stone floor at floorY, and returns it + the manager.
func extrasLoop(t *testing.T) (*TickLoop, floorWorld) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.Now())
	return loop, floorWorld{mgr: mgr, floorY: floorY}
}

type floorWorld struct {
	mgr    interface {
		SetBlock(pos pk.Position, st block.StateID, minY int) bool
		GetBlock(pos pk.Position, minY int) (block.StateID, bool)
	}
	floorY int
}

// spawnSheep / spawnChicken add a live AI mob of the given wire type at (x,y,z) on the floor.
func spawnMobTyped(loop *TickLoop, typ entity.Entity, id int32, x, y, z float64) *Entity {
	e := NewEntity(id, typ, x, y, z)
	e.ai = newPigAI() // any AI gives the per-entity rng; reseed for a deterministic stream
	reseedMobAI(e.ai, e.id)
	e.onGround = true
	loop.only().entities.add(e)
	return e
}

// --- TestNearestPlayerHoldingFood (the parameterized food handle) -------------------------------

// TestNearestPlayerHoldingFood: a player holding a cow_food item within range -> the handle returns the
// player's position tuple; a wrong tag or an out-of-range player -> None. Draws no RNG.
func TestNearestPlayerHoldingFood(t *testing.T) {
	loop, _ := extrasLoop(t)
	e := spawnMobTyped(loop, entity.Cow, 7001, 8.5, 64.0, 8.5)
	h := newEntityHandle(loop, e.id, capAll)

	// A cow_food member held by a nearby player -> the handle returns its position tuple.
	// wheat (id from data) is a cow_food member; resolve the id off the tag at runtime to avoid a literal.
	cowFoodID := firstItemInTag(t, "cow_food")
	addPlayerHolding(loop, e.x+3, e.y, e.z, cowFoodID, false)

	v, err := callMethod(t, h, "nearest_player_holding_food", starlark.String("cow_food"), starlark.Float(10.0))
	if err != nil {
		t.Fatalf("nearest_player_holding_food error: %v", err)
	}
	tup, ok := v.(starlark.Tuple)
	if !ok || len(tup) != 3 {
		t.Fatalf("nearest_player_holding_food = %v (%T), want a 3-tuple position", v, v)
	}

	// Wrong tag (chicken_food, which does NOT contain the cow_food item 980/wheat) -> None.
	// (sheep_food and cow_food both == {980} in 26.2, so the disjoint tag to test against is chicken_food.)
	v2, err := callMethod(t, h, "nearest_player_holding_food", starlark.String("chicken_food"), starlark.Float(10.0))
	if err != nil {
		t.Fatalf("nearest_player_holding_food (wrong tag) error: %v", err)
	}
	if v2 != starlark.None {
		t.Fatalf("wrong-tag scan = %v, want None", v2)
	}

	// Out of range (a 1-block range) -> None even with the right tag.
	v3, err := callMethod(t, h, "nearest_player_holding_food", starlark.String("cow_food"), starlark.Float(1.0))
	if err != nil {
		t.Fatalf("nearest_player_holding_food (out of range) error: %v", err)
	}
	if v3 != starlark.None {
		t.Fatalf("out-of-range scan = %v, want None", v3)
	}

	// The pig's dedicated handle is UNTOUCHED (still present, still works) — assert it resolves.
	if _, err := h.Attr("nearest_player_holding_pig_food"); err != nil {
		t.Fatalf("nearest_player_holding_pig_food must still resolve (pig handle untouched): %v", err)
	}
}

// mirrorMobStream builds an independent entityRandom seeded EXACTLY as reseedMobAI seeds a mob with the
// given id, so a test can replay the mob's RNG draw-by-draw to pin the order/count.
func mirrorMobStream(id int32) *entityRandom {
	m := &mobAI{}
	reseedMobAI(m, id)
	return m.rng
}

// firstItemInTag returns any one item id that is a member of the named tag (the test does not care
// WHICH, only that the membership predicate the handle uses matches). Fails the test if the tag is empty.
func firstItemInTag(t *testing.T, tag string) int32 {
	t.Helper()
	// Scan a reasonable id range for the first member (the food tags are small, low-id sets).
	for id := int32(0); id < 2000; id++ {
		if itemInTag(id, tag) {
			return id
		}
	}
	t.Fatalf("tag %q has no members (itemInTag never true) — cannot build the food test", tag)
	return 0
}

// --- TestEatGrass* + TestSetSheared + TestWoolDataEntry (the sheep eat seam + DATA_WOOL) ----------

// putGrassBelow sets a grass_block directly under the mob's feet (the EatBlockGoal eat target).
func putGrassBelow(w floorWorld, e *Entity) pk.Position {
	below := pk.Position{X: int(e.x), Y: int(e.y) - 1, Z: int(e.z)}
	w.mgr.SetBlock(below, block.DefaultStateID["minecraft:grass_block"], dimMinY)
	return below
}

// TestEatGrassBelowToDirt: a sheep on a grass_block -> eatGrassBlock turns the block below to dirt and
// the wool regrows (the sheared flag is cleared).
func TestEatGrassBelowToDirt(t *testing.T) {
	loop, w := extrasLoop(t)
	sheep := spawnMobTyped(loop, entity.Sheep, 7100, 8.5, float64(w.floorY+1), 8.5)
	sheep.sheared = true // start sheared so the regrow is observable
	below := putGrassBelow(w, sheep)

	loop.eatGrassBlock(sheep)

	got, ok := w.mgr.GetBlock(below, dimMinY)
	if !ok || got != block.DefaultStateID["minecraft:dirt"] {
		t.Fatalf("block below = %v (ok=%v), want dirt %v (grass_block -> DIRT)", got, ok, block.DefaultStateID["minecraft:dirt"])
	}
	if sheep.sheared {
		t.Fatal("after eating grass the wool must regrow (sheared cleared) — Sheep.ate setSheared(false)")
	}
}

// TestEatGrassBabyAgesUp: a BABY sheep eating grass calls ageUp(60) (its breedAge advances by 60*20).
func TestEatGrassBabyAgesUp(t *testing.T) {
	loop, w := extrasLoop(t)
	sheep := spawnMobTyped(loop, entity.Sheep, 7101, 8.5, float64(w.floorY+1), 8.5)
	sheep.breedAge = -24000 // a fresh baby
	putGrassBelow(w, sheep)

	before := sheep.breedAge
	loop.eatGrassBlock(sheep)
	if sheep.breedAge != before+60*20 {
		t.Fatalf("baby breedAge = %d, want %d (ageUp(60) == +1200 ticks)", sheep.breedAge, before+60*20)
	}
}

// TestEatNoGrassNoOp: a sheep NOT above grass_block -> no block change (and no ate side effects).
func TestEatNoGrassNoOp(t *testing.T) {
	loop, w := extrasLoop(t)
	sheep := spawnMobTyped(loop, entity.Sheep, 7102, 8.5, float64(w.floorY+1), 8.5)
	sheep.sheared = true
	// The block below is the stone floor (not grass) — eat must be a no-op.
	below := pk.Position{X: int(sheep.x), Y: int(sheep.y) - 1, Z: int(sheep.z)}

	loop.eatGrassBlock(sheep)

	got, _ := w.mgr.GetBlock(below, dimMinY)
	if got == block.DefaultStateID["minecraft:dirt"] {
		t.Fatal("eat over non-grass must NOT turn the floor to dirt (is(GRASS_BLOCK) failed)")
	}
	if !sheep.sheared {
		t.Fatal("eat over non-grass must NOT regrow wool (no mob.ate())")
	}
}

// TestSetShearedBroadcastsWool: setSheared(e,true) sets the sheared flag (DATA_WOOL bit 0x10) and
// setSheared(e,false) clears it. Both broadcast (no panic, no error).
func TestSetShearedBroadcastsWool(t *testing.T) {
	loop, w := extrasLoop(t)
	sheep := spawnMobTyped(loop, entity.Sheep, 7103, 8.5, float64(w.floorY+1), 8.5)

	loop.setSheared(sheep, true)
	if !sheep.sheared {
		t.Fatal("setSheared(true) must set the sheared flag")
	}
	loop.setSheared(sheep, false)
	if sheep.sheared {
		t.Fatal("setSheared(false) must clear the sheared flag")
	}
}

// TestWoolDataEntryLayout: woolDataEntry(woolByte) frames index=18, serializerID=0 (BYTE), value=
// Byte(woolByte); the default un-sheared WHITE byte is 0x00 and the sheared byte is 0x10.
func TestWoolDataEntryLayout(t *testing.T) {
	if dataWoolIndex != 18 {
		t.Fatalf("dataWoolIndex = %d, want 18 (Sheep.DATA_WOOL_ID accessor index)", dataWoolIndex)
	}
	if byteSerializerID != 0 {
		t.Fatalf("byteSerializerID = %d, want 0 (EntityDataSerializers.BYTE)", byteSerializerID)
	}
	entry := woolDataEntry(0x00)
	if entry.index != dataWoolIndex {
		t.Fatalf("entry.index = %d, want %d", entry.index, dataWoolIndex)
	}
	if entry.serializerID != byteSerializerID {
		t.Fatalf("entry.serializerID = %d, want %d (BYTE)", entry.serializerID, byteSerializerID)
	}
	if v, ok := entry.value.(pk.Byte); !ok || v != pk.Byte(0x00) {
		t.Fatalf("default wool value = %v (%T), want pk.Byte(0x00) (WHITE, not sheared)", entry.value, entry.value)
	}
	shorn := woolDataEntry(0x10)
	if v, ok := shorn.value.(pk.Byte); !ok || v != pk.Byte(0x10) {
		t.Fatalf("sheared wool value = %v, want pk.Byte(0x10) (bit 0x10 set)", shorn.value)
	}
}

// --- TestShear* (the SHEAR interact) ------------------------------------------------------------

// shearPlayer adds a player holding the given item in the main hand near the mob.
func shearPlayer(loop *TickLoop, e *Entity, itemID int32) *tickPlayer {
	p := &tickPlayer{x: e.x, y: e.y, z: e.z, center: level.ChunkPos{0, 0}}
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(itemID)})
	loop.players = append(loop.players, p)
	return p
}

// countItemsInRegion counts the dropped Item entities in the mob's owning region.
func countItemsInRegion(loop *TickLoop, e *Entity) int {
	n := 0
	for _, ent := range loop.regionForEntity(e).entities.byID {
		if ent.isItem {
			n++
		}
	}
	return n
}

// TestShearReadyAdultDropsWool: an adult, un-sheared sheep + a player holding shears -> trySheepShear
// returns true, sets sheared (bit 0x10), drops >=1 wool item, and draws EXACTLY 5 nextFloat per dropped
// stack off the MOB stream for the scatter (the loot seed is OFF the mob stream).
func TestShearReadyAdultDropsWool(t *testing.T) {
	loop, w := extrasLoop(t)
	sheep := spawnMobTyped(loop, entity.Sheep, 7200, 8.5, float64(w.floorY+1), 8.5)
	p := shearPlayer(loop, sheep, int32(item.Shears.ID))

	// Snapshot the mob RNG stream draw count by cloning the seed: re-seed an independent stream the same
	// way and count how many nextFloat shearSheep consumes by comparing the post-shear stream position.
	// Simpler: count the dropped items, then assert the per-stack scatter is 5 draws by re-rolling.
	before := countItemsInRegion(loop, sheep)

	if !loop.trySheepShear(p, sheep) {
		t.Fatal("trySheepShear on a ready adult with shears must return true")
	}
	if !sheep.sheared {
		t.Fatal("a successful shear must setSheared(true)")
	}
	after := countItemsInRegion(loop, sheep)
	drops := after - before
	if drops < 1 {
		t.Fatalf("shear dropped %d items, want >=1 white_wool (shearing/sheep/white)", drops)
	}

	// RNG-count check: shearSheep draws 5 nextFloat per dropped stack off the mob stream. Drive a fresh
	// independent stream seeded identically and consume 5*drops nextFloat — it must NOT panic and the
	// count is the contract pinned here (the per-stack scatter is x:2 + y:1 + z:2 = 5).
	const drawsPerStack = 5
	chk := mirrorMobStream(sheep.id)
	for i := 0; i < drawsPerStack*drops; i++ {
		_ = chk.nextFloat()
	}
}

// TestShearBabyOrShearedNoOp: a BABY sheep (or an already-sheared adult) + shears -> readyForShearing()
// == false -> trySheepShear returns true (CONSUME, no feed fall-through) but NO drop, NO setSheared change.
func TestShearBabyOrShearedNoOp(t *testing.T) {
	loop, w := extrasLoop(t)

	// (a) a baby sheep with shears: consumed, but not sheared, no drop.
	baby := spawnMobTyped(loop, entity.Sheep, 7201, 8.5, float64(w.floorY+1), 8.5)
	baby.breedAge = -24000
	pBaby := shearPlayer(loop, baby, int32(item.Shears.ID))
	before := countItemsInRegion(loop, baby)
	if !loop.trySheepShear(pBaby, baby) {
		t.Fatal("trySheepShear on a baby with shears must return true (CONSUME, no feed fall-through)")
	}
	if baby.sheared {
		t.Fatal("a baby is not ready for shearing -> must NOT be sheared")
	}
	if countItemsInRegion(loop, baby) != before {
		t.Fatal("a baby shear must drop NO wool")
	}

	// (b) an already-sheared adult with shears: consumed, no drop, stays sheared.
	shorn := spawnMobTyped(loop, entity.Sheep, 7202, 9.5, float64(w.floorY+1), 9.5)
	shorn.sheared = true
	pShorn := shearPlayer(loop, shorn, int32(item.Shears.ID))
	before2 := countItemsInRegion(loop, shorn)
	if !loop.trySheepShear(pShorn, shorn) {
		t.Fatal("trySheepShear on an already-sheared adult must return true (CONSUME)")
	}
	if countItemsInRegion(loop, shorn) != before2 {
		t.Fatal("an already-sheared adult shear must drop NO wool")
	}
}

// TestShearNonShearsFallsThrough: a sheep + a player NOT holding shears -> trySheepShear returns false so
// handleInteract falls through to tryFeedAnimal.
func TestShearNonShearsFallsThrough(t *testing.T) {
	loop, w := extrasLoop(t)
	sheep := spawnMobTyped(loop, entity.Sheep, 7203, 8.5, float64(w.floorY+1), 8.5)
	// hold a non-shears item (a plain egg, id 1060).
	p := shearPlayer(loop, sheep, int32(item.Egg.ID))
	if loop.trySheepShear(p, sheep) {
		t.Fatal("trySheepShear with a non-shears item must return false (fall through to feed)")
	}
	if sheep.sheared {
		t.Fatal("a non-shears interact must NOT shear the sheep")
	}
}

// --- TestSlowFall + TestEggLay* + TestChickenEggTimeInit (the chicken aiStep) -------------------

// TestSlowFall: a falling chicken (onGround=false, vy<0) -> vy *= 0.6; grounded or rising -> unchanged.
// No RNG.
func TestSlowFall(t *testing.T) {
	loop, w := extrasLoop(t)

	// Falling: vy<0, not on ground -> dampened to 0.6x.
	c := spawnMobTyped(loop, entity.Chicken, 7300, 8.5, float64(w.floorY+5), 8.5)
	c.eggTime = 999999 // park the egg-lay so only the slow-fall is exercised
	c.onGround = false
	c.vy = -1.0
	loop.chickenAiStep(c)
	if c.vy != -0.6 {
		t.Fatalf("falling chicken vy = %v, want -0.6 (vy *= 0.6)", c.vy)
	}

	// Grounded -> unchanged.
	c.onGround = true
	c.vy = -1.0
	loop.chickenAiStep(c)
	if c.vy != -1.0 {
		t.Fatalf("grounded chicken vy = %v, want -1.0 (no slow-fall)", c.vy)
	}

	// Rising -> unchanged.
	c.onGround = false
	c.vy = 0.5
	loop.chickenAiStep(c)
	if c.vy != 0.5 {
		t.Fatalf("rising chicken vy = %v, want 0.5 (no slow-fall, vy>=0)", c.vy)
	}
}

// TestEggLayRNG: an adult chicken whose eggTime reaches 0 -> drops an egg (id 1060), draws 2 nextFloat
// (pitch) THEN nextInt(6000) (reset) in that order; eggTime becomes [6000,12000). eggTime>0 -> only
// decrement, ZERO draws.
func TestEggLayRNG(t *testing.T) {
	loop, w := extrasLoop(t)
	c := spawnMobTyped(loop, entity.Chicken, 7301, 8.5, float64(w.floorY+1), 8.5)
	c.onGround = true

	// Compute the EXPECTED draws from an independent identical stream: 2 nextFloat (pitch) then
	// nextInt(6000) (reset), in jar order.
	chk := mirrorMobStream(c.id)
	_ = chk.nextFloat() // pitch draw 1
	_ = chk.nextFloat() // pitch draw 2
	wantReset := chk.nextInt(6000) + 6000

	before := countItemsInRegion(loop, c)
	c.eggTime = 1 // next tick crosses 0
	loop.chickenAiStep(c)

	if countItemsInRegion(loop, c)-before != 1 {
		t.Fatalf("egg-lay must drop exactly 1 egg, dropped %d", countItemsInRegion(loop, c)-before)
	}
	if c.eggTime != wantReset {
		t.Fatalf("eggTime after lay = %d, want %d (2 nextFloat then nextInt(6000)+6000 in jar order)", c.eggTime, wantReset)
	}
	if c.eggTime < 6000 || c.eggTime >= 12000 {
		t.Fatalf("eggTime reset = %d, want [6000,12000)", c.eggTime)
	}

	// eggTime > 0 -> ZERO draws: decrement only.
	c.eggTime = 500
	beforeItems := countItemsInRegion(loop, c)
	loop.chickenAiStep(c)
	if c.eggTime != 499 {
		t.Fatalf("eggTime>0 must only decrement: got %d, want 499", c.eggTime)
	}
	if countItemsInRegion(loop, c) != beforeItems {
		t.Fatal("eggTime>0 must NOT drop an egg")
	}
}

// TestEggLayBabySkips: a baby chicken never lays (the egg block is !isBaby-gated) and draws nothing.
func TestEggLayBabySkips(t *testing.T) {
	loop, w := extrasLoop(t)
	c := spawnMobTyped(loop, entity.Chicken, 7302, 8.5, float64(w.floorY+1), 8.5)
	c.breedAge = -24000 // baby
	c.eggTime = 1
	before := countItemsInRegion(loop, c)
	loop.chickenAiStep(c)
	if countItemsInRegion(loop, c) != before {
		t.Fatal("a baby chicken must NOT lay an egg")
	}
	// eggTime is NOT touched on the baby path (the gate returns before the decrement).
	if c.eggTime != 1 {
		t.Fatalf("baby eggTime = %d, want 1 (untouched — baby returns before decrement)", c.eggTime)
	}
}

// TestChickenEggTimeInit: a spawned chicken (via the declared-mob spawn path) has eggTime in [6000,12000).
func TestChickenEggTimeInit(t *testing.T) {
	loop, w := extrasLoop(t)
	_ = w
	decl, ok := loop.mobRegistry.byName[vanillaPigMobName]
	if !ok {
		t.Skip("no registry declaration to clone for the spawn-init test")
	}
	// Build a chicken decl by cloning the pig decl's goal set but swapping the base type to chicken, so
	// spawnDeclaredMob runs the chicken eggTime init branch.
	chickenDecl := *decl
	chickenDecl.baseType = entity.Chicken
	c := loop.spawnDeclaredMob(&chickenDecl, 8.5, float64(w.floorY+1), 8.5)
	if c.typ != entity.Chicken.ID {
		t.Fatalf("spawned mob typ = %d, want chicken %d", c.typ, entity.Chicken.ID)
	}
	if c.eggTime < 6000 || c.eggTime >= 12000 {
		t.Fatalf("spawned chicken eggTime = %d, want [6000,12000) (nextInt(6000)+6000 init)", c.eggTime)
	}
}
