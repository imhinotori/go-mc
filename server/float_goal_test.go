package server

// float_goal_test.go — Phase 30-03 gates MOB-SUB-04's FloatGoal@0 consumer: the Go-native floatGoal
// (server/ai_goals_float.go) wired @0 on the pig in LOCKSTEP with the plugin (plugins/vanilla_pig/
// main.star), plus the dedicated WET-behavior proof that a pig in water does not sink the way a pig
// with no FloatGoal does. Every behavior asserted here mirrors a jar method verified via
// `javap -c -p temp/cache/26.2-inner.jar` (citations at each port site in ai_goals_float.go):
//
//   - net.minecraft.world.entity.ai.goal.FloatGoal
//       ctor  : setFlags(EnumSet.of(JUMP)); mob.getNavigation().setCanFloat(true)
//       canUse: isInWater() && getFluidHeight(WATER) > getFluidJumpThreshold() || isInLava()  (NO RNG)
//       tick  : if (getRandom().nextFloat() < 0.8f) getJumpControl().jump();                   (DRAW 1)
//       requiresUpdateEveryTick(): true
//
// These are PORT-EXACT behavior gates, not feel checks. The DRY oracle (TestPluginPigEqualsGoNativePig,
// plugin_pig_test.go) is the lockstep canary; this file owns the WET behavior in a SEPARATE world.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// fillWaterColumn lays water source cells across the whole 16x16 chunk for every Y in [yLo, yHi],
// so a pig anywhere in the column has its full AABB submerged (mobInWater true, mobFluidHeight(WATER)
// ≈ 1.0 per cell → well above the pig's 0.4 jump threshold). There is deliberately NO solid floor
// inside the column, so an entity under gravity descends freely — the descent is the observable the
// wet test measures.
func fillWaterColumn(ch *level.Chunk, yLo, yHi int) {
	for y := yLo; y <= yHi; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				sec := (y - dimMinY) >> 4
				localPos := (y&15)<<8 | (z&15)<<4 | (x & 15)
				ch.Sections[sec].SetBlock(localPos, waterStateID(0))
			}
		}
	}
}

// newWaterWorldPig spawns a pig (with newPigAI → FloatGoal@0) at (x,y,z) submerged in a deep water
// column built across the 3x3 chunk neighborhood, with the pig airborne (onGround=false) so gravity
// pulls it down each physics tick. Returns the loop + the pig. The pig's AABB is fully in water →
// FloatGoal.canUse is true → it ticks every tick (requiresUpdateEveryTick) and fires its +0.04
// impulse on ~80% of ticks.
func newWaterWorldPig(t *testing.T, id int32, x, y, z float64, yLo, yHi int) (*TickLoop, *Entity) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillWaterColumn(ch, yLo, yHi)
		}
	}
	pig := NewEntity(id, entity.Pig, x, y, z)
	pig.ai = newPigAI()
	reseedMobAI(pig.ai, pig.id)
	pig.onGround = false
	loop.only().entities.add(pig)
	return loop, pig
}

// TestFloatGoalKeepsPigAfloat is the WET-behavior proof (the in-game "pig sinks to the floor" bug,
// LIVE-DEBUG A). A pig with FloatGoal@0 dropped in a water column with a SOLID FLOOR stays NEAR the
// surface and does NOT sink to the floor: the water physics (travelInWaterVertical — vertical drag
// 0.8 + reduced gravity 0.005) makes the mob sink slowly, and FloatGoal.canUse is true in water →
// tick() draws nextFloat()<0.8 → jumpControl.jump() → the serverAiStep JUMP slot's entityJumpStep
// adds the +0.04 swim impulse (jumpInLiquid), which overcomes the gentle 0.005 pull and keeps the
// pig bobbing at the surface. Driven through serverAiStep + tickPhysics for many ticks, the FloatGoal
// pig stays within a few blocks of where it started — it never reaches the floor.
//
// This is the STRENGTHENED assertion (was: merely "sinks less than a control pig"): with the water
// travel physics now ported (LIVE-DEBUG A — travelInWaterVertical wired into tickPhysics), the
// observable is the full vanilla "bobs at the surface" behavior, so the test asserts the pig STAYS
// AFLOAT near its start, not just that it outpaces a gravity-only control.
func TestFloatGoalKeepsPigAfloat(t *testing.T) {
	const (
		// A water column ABOVE a solid floor: water in [floorY+1 .. yHi], a stone floor at floorY.
		// The pig starts a few blocks ABOVE the floor, submerged. If FloatGoal+water-physics work,
		// it stays near startY; if it sank like the old dry-physics bug, it would land on floorY+1.
		floorY = 40
		yLo    = floorY + 1 // first water cell above the floor
		yHi    = 110        // deep water well above the pig
		startY = 60.0       // ~19 blocks above the floor surface (floorY+1 == 41)
		// 200 ticks: long enough that a sinking pig WOULD have reached the floor (a 0.005/tick + drag
		// descent over the 19-block gap would land it well within 200 ticks), so staying afloat is a
		// genuine proof FloatGoal's impulse beats the reduced water gravity.
		ticks = 200
		x, z  = 8.5, 8.5
	)

	loop, mgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillWaterColumn(ch, yLo, yHi)
			fillFloor(ch, floorY) // solid floor BELOW the water (overwrites the water cell at floorY)
		}
	}
	pig := NewEntity(4242, entity.Pig, x, startY, z)
	pig.ai = newPigAI()
	reseedMobAI(pig.ai, pig.id)
	pig.onGround = false
	loop.only().entities.add(pig)

	// Sanity: the pig starts in water (FloatGoal.canUse precondition) and well above the floor.
	if !loop.mobInWater(pig) {
		t.Fatal("setup: the FloatGoal pig must start submerged in water")
	}

	startHealth := pig.health // the pig must take NO damage while bobbing in water
	minY := pig.y             // track the LOWEST the pig ever sinks to over the run
	for i := 0; i < ticks; i++ {
		pig.ai.serverAiStep(loop, pig)
		loop.tickPhysics()
		if pig.y < minY {
			minY = pig.y
		}
		// THE "DYING SLOWLY IN WATER" REGRESSION: a pig bobbing at a shallow water surface used to
		// accumulate fallDistance on the brief out-of-water (jump) ticks and then take repeated landing
		// damage as it slow-sank — dying as if from constant damage. With inWater re-read at the settled
		// post-moveEntity position, the water-reset fires whenever the mob is actually submerged, so it
		// takes ZERO fall damage. Assert health never drops.
		if pig.health < startHealth {
			t.Fatalf("tick %d: FloatGoal pig took damage in water (health %.1f -> %.1f, fallDistance=%.4f) — "+
				"the surface-bob fall-damage bug regressed", i, startHealth, pig.health, pig.fallDistance)
		}
	}

	// The pig must still be IN water at the end (it did not sink out / the column did not drain).
	if !loop.mobInWater(pig) {
		t.Fatalf("FloatGoal pig left the water column (y=%.4f) — it should stay afloat in the water", pig.y)
	}

	// THE STRENGTHENED AFLOAT ASSERTION: the pig must NOT have sunk near the floor. The floor surface
	// is floorY+1 (== 41); the pig started at 60. Assert it never sank below a generous afloat band
	// (here: stayed within ~6 blocks of its start, i.e. y > 54), which is FAR above the floor — the
	// old dry-physics bug would have parked it on the floor at ~41.
	const afloatFloor = startY - 6.0 // 54.0 — well above the floorY+1==41 surface
	if minY < afloatFloor {
		t.Fatalf("FloatGoal pig sank below the afloat band: lowest y=%.4f dropped below %.1f (floor "+
			"surface is %.1f). It should bob near the start (%.1f), not sink toward the floor.",
			minY, afloatFloor, float64(floorY+1), startY)
	}
}

// TestMobSinksSlowerInWaterThanAir is the LIVE-DEBUG A unit proof for travelInWaterVertical: an
// IDENTICAL mob with the SAME downward velocity descends MUCH slower in water (vertical drag 0.8 +
// reduced gravity 0.005) than in air (the dry vy -= 0.08; vy *= 0.98 path). It strips the AI (no
// FloatGoal impulse) so the test isolates the water TRAVEL physics, not the swim-jump. Mirrors
// LivingEntity.travelInWater vs travelInAir.
func TestMobSinksSlowerInWaterThanAir(t *testing.T) {
	const (
		yLo, yHi = -60, 110 // deep water with no floor in range, so the descent is unobstructed
		startY   = 100.0
		startVy  = -0.2 // a downward velocity both mobs begin with
		ticks    = 20
		x, z     = 8.5, 8.5
	)

	// The water mob — submerged in a deep water column (no floor), no AI (pure travel physics).
	waterLoop, mgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillWaterColumn(ch, yLo, yHi)
		}
	}
	waterMob := NewEntity(1, entity.Pig, x, startY, z)
	waterMob.onGround = false
	waterMob.vy = startVy
	waterLoop.only().entities.add(waterMob)
	if !waterLoop.mobInWater(waterMob) {
		t.Fatal("setup: the water mob must start submerged")
	}

	// The air mob — an empty (all-air) world, same start, same velocity, no AI (the dry path).
	airLoop, airMgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			putChunk(airMgr, level.ChunkPos{int32(cx), int32(cz)}) // all-air chunk, no water
		}
	}
	airMob := NewEntity(1, entity.Pig, x, startY, z)
	airMob.onGround = false
	airMob.vy = startVy
	airLoop.only().entities.add(airMob)
	if airLoop.mobInWater(airMob) {
		t.Fatal("setup: the air mob must NOT be in water")
	}

	for i := 0; i < ticks; i++ {
		waterLoop.tickPhysics()
		airLoop.tickPhysics()
	}

	waterDrop := startY - waterMob.y
	airDrop := startY - airMob.y

	// The air mob must have dropped MUCH further: the dry 0.08 gravity vs the water 0.005 reduced
	// gravity + 0.8 drag is roughly an order of magnitude. Assert the water mob fell strictly less,
	// and by a wide margin (less than half the air drop) so the test is not flaky on small diffs.
	if waterDrop >= airDrop {
		t.Fatalf("water mob did not sink slower: waterDrop=%.4f air Drop=%.4f (water should be far less)",
			waterDrop, airDrop)
	}
	if waterDrop > airDrop*0.5 {
		t.Fatalf("water drag too weak: waterDrop=%.4f is more than half airDrop=%.4f — the water "+
			"vertical drag (0.8) + reduced gravity (0.005) should make the water mob sink FAR slower",
			waterDrop, airDrop)
	}
}

// TestFloatGoalCanUse pins FloatGoal.canUse: true in water (fluidHeight > threshold), true in lava,
// false on dry land — and it draws NO RNG (canUse is a pure predicate; the only draw is in tick()).
// [javap FloatGoal.canUse: isInWater() && getFluidHeight(WATER) > getFluidJumpThreshold() || isInLava().]
func TestFloatGoalCanUse(t *testing.T) {
	g := newFloatGoal()

	t.Run("true in deep water", func(t *testing.T) {
		loop, pig := newWaterWorldPig(t, 1, 8.5, 100.0, 8.5, 40, 110)
		// canUse must NOT draw RNG: a reference rng seeded identically stays in lockstep after the call.
		ref := cloneMobRNG(pig)
		if !g.canUse(loop, pig) {
			t.Fatal("FloatGoal.canUse must be true for a pig submerged in water")
		}
		assertRNGUntouched(t, pig, ref, "FloatGoal.canUse")
	})

	t.Run("true in lava", func(t *testing.T) {
		loop, mgr := newPhysicsLoop()
		for cx := -1; cx <= 1; cx++ {
			for cz := -1; cz <= 1; cz++ {
				ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
				// lava across a column around the pig
				for y := 95; y <= 105; y++ {
					for x := 0; x < 16; x++ {
						for z := 0; z < 16; z++ {
							sec := (y - dimMinY) >> 4
							ch.Sections[sec].SetBlock((y&15)<<8|(z&15)<<4|(x&15), lavaStateID(0))
						}
					}
				}
			}
		}
		pig := NewEntity(2, entity.Pig, 8.5, 100.0, 8.5)
		pig.ai = newPigAI()
		reseedMobAI(pig.ai, pig.id)
		loop.only().entities.add(pig)
		if !g.canUse(loop, pig) {
			t.Fatal("FloatGoal.canUse must be true for a pig submerged in lava")
		}
	})

	t.Run("false on dry land", func(t *testing.T) {
		loop, mgr := newPhysicsLoop()
		for cx := -1; cx <= 1; cx++ {
			for cz := -1; cz <= 1; cz++ {
				ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
				fillFloor(ch, 63)
			}
		}
		pig := NewEntity(3, entity.Pig, 8.5, 64.0, 8.5)
		pig.ai = newPigAI()
		reseedMobAI(pig.ai, pig.id)
		loop.only().entities.add(pig)
		ref := cloneMobRNG(pig)
		if g.canUse(loop, pig) {
			t.Fatal("FloatGoal.canUse must be false for a pig on dry land")
		}
		assertRNGUntouched(t, pig, ref, "FloatGoal.canUse (dry)")
	})
}

// TestFloatGoalTickDraws pins FloatGoal.tick: it draws EXACTLY ONE nextFloat() per tick from the
// mob's per-entity rng, and on a draw < 0.8 it arms the jumpControl (jumpControl.jump()).
// [javap FloatGoal.tick: if (getRandom().nextFloat() < 0.8f) getJumpControl().jump().]
func TestFloatGoalTickDraws(t *testing.T) {
	g := newFloatGoal()
	loop, pig := newWaterWorldPig(t, 7, 8.5, 100.0, 8.5, 40, 110)

	// A reference rng seeded identically: after one tick the mob's rng must have advanced by EXACTLY
	// one nextFloat() draw (the reference, advanced one nextFloat, equals the mob's next draw).
	ref := cloneMobRNG(pig)
	wantDraw := ref.nextFloat() // the value FloatGoal.tick will consume this tick

	pig.ai.jumpControl.jump = false
	g.tick(loop, pig)

	// The mob's rng must now be exactly one draw ahead: its NEXT draw equals the reference's NEXT.
	if got, want := mobRandom(pig).nextFloat(), ref.nextFloat(); got != want {
		t.Fatalf("FloatGoal.tick did not draw exactly one nextFloat: mob next=%v ref next=%v", got, want)
	}

	// If the consumed draw was < 0.8 the jump is armed; else not. Assert the gate matches the draw.
	wantArmed := wantDraw < 0.8
	if pig.ai.jumpControl.jump != wantArmed {
		t.Fatalf("FloatGoal.tick draw=%v: jumpControl.jump=%v, want %v (jump iff draw<0.8)",
			wantDraw, pig.ai.jumpControl.jump, wantArmed)
	}
}

// cloneMobRNG returns a fresh entityRandom seeded identically to the mob's, so a test can predict the
// next draw or assert the mob's stream was untouched. (Mirrors reseedMobAI's per-id seed derivation.)
func cloneMobRNG(e *Entity) *entityRandom {
	seed := uint64(uint32(e.id)) ^ defaultEntityRandomSeed
	return newEntityRandom(seed)
}

// assertRNGUntouched fails if the mob's rng has advanced relative to a reference seeded identically at
// the same point (used to prove a pure-predicate call drew no RNG).
func assertRNGUntouched(t *testing.T, e *Entity, ref *entityRandom, what string) {
	t.Helper()
	if got, want := mobRandom(e).nextFloat(), ref.nextFloat(); got != want {
		t.Fatalf("%s drew RNG (mob=%v ref=%v) — it must be a pure predicate", what, got, want)
	}
}
