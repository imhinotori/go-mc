package server

// powder_snow_climb_test.go — gates the Go-native ClimbOnTopOfPowderSnowGoal (server/ai_goals_powder_snow.go),
// the goal that lets a POWDER_SNOW_WALKABLE_MOBS mob (rabbit/fox/silverfish/endermite) climb ON TOP of
// powder snow instead of sinking. Every behavior asserted mirrors a jar method verified via
// `javap -c -p temp/cache/26.2-inner.jar` this session (citations at the port site):
//
//   - net.minecraft.world.entity.ai.goal.ClimbOnTopOfPowderSnowGoal
//       ctor  : setFlags(EnumSet.of(JUMP))
//       canUse: (wasInPowderSnow || isInPowderSnow) && mob.is(POWDER_SNOW_WALKABLE_MOBS)
//               && (aboveState.is(POWDER_SNOW) || aboveState.getCollisionShape == Shapes.empty())   (NO RNG)
//       tick  : getJumpControl().jump()                                                              (NO RNG)
//       requiresUpdateEveryTick(): true
//
// The isInPowderSnow field is stubbed by the feet-block proxy (mobInPowderSnow) pending the powder-snow
// inside-block subsystem — the test drives that proxy by placing a powder_snow block at the mob's feet.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// setPowderSnow places a minecraft:powder_snow block at world (x,y,z) in the chunk (the same
// section/local mapping setBlock uses).
func setPowderSnow(ch *level.Chunk, x, y, z int) {
	sec := (y - dimMinY) >> 4
	local := (y&15)<<8 | (z&15)<<4 | (x & 15)
	if sec < 0 || sec >= len(ch.Sections) {
		panic("test block out of section range")
	}
	ch.Sections[sec].SetBlock(local, block.ToStateID[block.PowderSnow{}])
}

// newPowderSnowWorld builds a physics loop with a 3x3 chunk neighborhood and a mob of type typ standing
// with its feet in a powder_snow block at (bx,by,bz); the block above (by+1) is left AIR (empty
// collision shape) so canUse's above-block branch passes. Returns the loop + the mob (given a fresh
// mobAI with jumpControl). The mob is placed at the block center (bx+0.5, by, bz+0.5).
func newPowderSnowWorld(t *testing.T, id int32, typ entity.Entity, bx, by, bz int) (*TickLoop, *Entity) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			// powder snow at the feet cell (drives the isInPowderSnow proxy); air above.
			if cx == 0 && cz == 0 {
				setPowderSnow(ch, bx&15, by, bz&15)
			}
		}
	}
	mob := NewEntity(id, typ, float64(bx)+0.5, float64(by), float64(bz)+0.5)
	mob.ai = newPigAI() // mob-agnostic ai: supplies the jumpControl the goal arms
	reseedMobAI(mob.ai, mob.id)
	mob.onGround = true
	loop.only().entities.add(mob)
	return loop, mob
}

// TestClimbOnPowderSnowCanUse pins ClimbOnTopOfPowderSnowGoal.canUse: true for a walkable mob standing
// in powder snow with air (empty collision shape) above; false when the mob is NOT in powder snow.
// canUse draws NO RNG (the only draw-free predicate branch).
func TestClimbOnPowderSnowCanUse(t *testing.T) {
	g := newClimbOnTopOfPowderSnowGoal()

	t.Run("true: rabbit in powder snow, air above", func(t *testing.T) {
		loop, rabbit := newPowderSnowWorld(t, 1, entity.Rabbit, 8, 64, 8)
		if !loop.mobInPowderSnow(rabbit) {
			t.Fatal("setup: the rabbit must read as in powder snow (feet-block proxy)")
		}
		ref := cloneMobRNG(rabbit)
		if !g.canUse(loop, rabbit) {
			t.Fatal("ClimbOnTopOfPowderSnowGoal.canUse must be true for a walkable mob in powder snow with air above")
		}
		assertRNGUntouched(t, rabbit, ref, "ClimbOnTopOfPowderSnowGoal.canUse")
	})

	t.Run("true: powder snow above (aboveState.is(POWDER_SNOW))", func(t *testing.T) {
		loop, mgr := newPhysicsLoop()
		for cx := -1; cx <= 1; cx++ {
			for cz := -1; cz <= 1; cz++ {
				ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
				if cx == 0 && cz == 0 {
					setPowderSnow(ch, 8, 64, 8) // feet
					setPowderSnow(ch, 8, 65, 8) // above == powder snow -> the .is(POWDER_SNOW) branch
				}
			}
		}
		rabbit := NewEntity(2, entity.Rabbit, 8.5, 64.0, 8.5)
		rabbit.ai = newPigAI()
		reseedMobAI(rabbit.ai, rabbit.id)
		loop.only().entities.add(rabbit)
		if !g.canUse(loop, rabbit) {
			t.Fatal("canUse must be true when the block above is itself powder snow")
		}
	})

	t.Run("false: not in powder snow", func(t *testing.T) {
		loop, mgr := newPhysicsLoop()
		for cx := -1; cx <= 1; cx++ {
			for cz := -1; cz <= 1; cz++ {
				ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
				fillFloor(ch, 63) // solid floor, NO powder snow
			}
		}
		rabbit := NewEntity(3, entity.Rabbit, 8.5, 64.0, 8.5)
		rabbit.ai = newPigAI()
		reseedMobAI(rabbit.ai, rabbit.id)
		loop.only().entities.add(rabbit)
		ref := cloneMobRNG(rabbit)
		if g.canUse(loop, rabbit) {
			t.Fatal("canUse must be false when the mob is not in powder snow")
		}
		assertRNGUntouched(t, rabbit, ref, "ClimbOnTopOfPowderSnowGoal.canUse (dry)")
	})

	t.Run("false: solid block above (non-empty collision shape, not powder snow)", func(t *testing.T) {
		loop, mgr := newPhysicsLoop()
		for cx := -1; cx <= 1; cx++ {
			for cz := -1; cz <= 1; cz++ {
				ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
				if cx == 0 && cz == 0 {
					setPowderSnow(ch, 8, 64, 8) // feet in powder snow
					setBlock(ch, 8, 65, 8)      // solid stone above -> non-empty collision shape, not powder snow
				}
			}
		}
		rabbit := NewEntity(4, entity.Rabbit, 8.5, 64.0, 8.5)
		rabbit.ai = newPigAI()
		reseedMobAI(rabbit.ai, rabbit.id)
		loop.only().entities.add(rabbit)
		if g.canUse(loop, rabbit) {
			t.Fatal("canUse must be false when a solid (non-empty-shape, non-powder-snow) block is above")
		}
	})

	t.Run("false: non-walkable mob (pig) even in powder snow", func(t *testing.T) {
		loop, pig := newPowderSnowWorld(t, 5, entity.Pig, 8, 64, 8)
		if !loop.mobInPowderSnow(pig) {
			t.Fatal("setup: the pig must read as in powder snow (feet-block proxy)")
		}
		if g.canUse(loop, pig) {
			t.Fatal("canUse must be false for a mob NOT in POWDER_SNOW_WALKABLE_MOBS (pig)")
		}
	})
}

// TestClimbOnPowderSnowTickArmsJump pins ClimbOnTopOfPowderSnowGoal.tick: it arms the jumpControl
// (getJumpControl().jump() -> jumpControl.doJump()) and draws NO RNG.
// [javap ClimbOnTopOfPowderSnowGoal.tick: getJumpControl(); JumpControl.jump().]
func TestClimbOnPowderSnowTickArmsJump(t *testing.T) {
	g := newClimbOnTopOfPowderSnowGoal()
	loop, rabbit := newPowderSnowWorld(t, 6, entity.Rabbit, 8, 64, 8)

	rabbit.ai.jumpControl.jump = false
	ref := cloneMobRNG(rabbit)
	g.tick(loop, rabbit)

	if !rabbit.ai.jumpControl.jump {
		t.Fatal("ClimbOnTopOfPowderSnowGoal.tick must arm the jumpControl (jumpControl.doJump)")
	}
	assertRNGUntouched(t, rabbit, ref, "ClimbOnTopOfPowderSnowGoal.tick")
}

// TestClimbOnPowderSnowFlags pins the goal's flag set to {JUMP} (ctor: setFlags(EnumSet.of(JUMP))) and
// requiresUpdateEveryTick == true — the two static invariants the buildNativeGoal flag-assert relies on.
func TestClimbOnPowderSnowFlags(t *testing.T) {
	g := newClimbOnTopOfPowderSnowGoal()
	if g.flags() != flagJump {
		t.Fatalf("ClimbOnTopOfPowderSnowGoal flags = %v, want %v (JUMP)", g.flags(), flagJump)
	}
	if !g.requiresUpdateEveryTick() {
		t.Fatal("ClimbOnTopOfPowderSnowGoal.requiresUpdateEveryTick must be true")
	}
}

// TestPowderSnowWalkableTag pins isPowderSnowWalkableMob to the EXACT jar tag contents
// (powder_snow_walkable_mobs.json: rabbit, endermite, silverfish, fox) — creeper is NOT a member.
func TestPowderSnowWalkableTag(t *testing.T) {
	member := []entity.Entity{entity.Rabbit, entity.Endermite, entity.Silverfish, entity.Fox}
	for _, m := range member {
		if !isPowderSnowWalkableMob(m.ID) {
			t.Fatalf("%s must be in POWDER_SNOW_WALKABLE_MOBS", m.Name)
		}
	}
	nonMember := []entity.Entity{entity.Creeper, entity.Pig, entity.Cow}
	for _, m := range nonMember {
		if isPowderSnowWalkableMob(m.ID) {
			t.Fatalf("%s must NOT be in POWDER_SNOW_WALKABLE_MOBS", m.Name)
		}
	}
}
