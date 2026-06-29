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
	pk "github.com/imhinotori/sulfur/net/packet"
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

// TestFloatGoalKeepsPigAfloat is the WET-behavior proof (the TDD RED→GREEN driver). A pig with
// FloatGoal@0 dropped in a deep water column does NOT sink the way an identical pig WITHOUT FloatGoal
// does: FloatGoal.canUse is true in water → tick() draws nextFloat()<0.8 → jumpControl.jump() → the
// serverAiStep JUMP slot's entityJumpStep adds the +0.04 swim impulse (jumpInLiquid), counteracting
// gravity. Driven through serverAiStep + tickPhysics for N ticks, the FloatGoal pig ends up
// MEASURABLY HIGHER (less sunk) than the control pig whose goals are stripped (pure gravity).
//
// NOTE (cited, faithful scope): vanilla water also applies buoyancy/fluid-push movement physics
// (LivingEntity.travel's water branch — isPushedByFluid + the 0.8 water drag) which is DEFERRED
// (30-01/30-02 cited it: FloatGoal only READS the fluid; the water travel physics is a later port).
// So this test measures FloatGoal's OWN observable — the +0.04 jump impulse counteracting gravity,
// making the FloatGoal pig descend slower than a no-FloatGoal pig — NOT the full vanilla "bobs at the
// surface" behavior, which needs the deferred water travel physics. The differential is the honest,
// non-stub observable of FloatGoal as built this phase.
func TestFloatGoalKeepsPigAfloat(t *testing.T) {
	const (
		startY     = 100.0
		yLo, yHi   = 40, 110 // deep water spanning well above + below the pig, no floor in range
		ticks      = 200
		x, z       = 8.5, 8.5
	)

	// The FloatGoal pig — full newPigAI (FloatGoal@0 active in water).
	floatLoop, floatPig := newWaterWorldPig(t, 4242, x, startY, z, yLo, yHi)

	// The control pig — identical world + id (same seed), but its goals are STRIPPED so FloatGoal
	// never fires. It descends under pure gravity (the impulse-free baseline).
	ctrlLoop, ctrlPig := newWaterWorldPig(t, 4242, x, startY, z, yLo, yHi)
	ctrlPig.ai.goals = goalSelector{}

	// Sanity: both pigs start in water (FloatGoal.canUse precondition).
	if !floatLoop.mobInWater(floatPig) {
		t.Fatal("setup: the FloatGoal pig must start submerged in water")
	}

	for i := 0; i < ticks; i++ {
		floatPig.ai.serverAiStep(floatLoop, floatPig)
		ctrlPig.ai.serverAiStep(ctrlLoop, ctrlPig)
		floatLoop.tickPhysics()
		ctrlLoop.tickPhysics()
	}

	// FloatGoal's impulses must leave its pig HIGHER than the impulse-free control: it sank less.
	if floatPig.y <= ctrlPig.y {
		t.Fatalf("FloatGoal pig did not stay afloat: floatPig.y=%.4f must be > ctrlPig.y=%.4f "+
			"(FloatGoal's +0.04 impulses should counteract gravity)", floatPig.y, ctrlPig.y)
	}

	// And the FloatGoal pig must still be IN water at the end (it did not sink out of the column).
	if !floatLoop.mobInWater(floatPig) {
		t.Fatalf("FloatGoal pig sank out of the water column (y=%.4f) — it should stay afloat in the water",
			floatPig.y)
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

// (silence the unused import if the lava sub-test is the only pk user)
var _ = pk.Position{}
