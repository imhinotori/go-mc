package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// fluid_mob_test.go (Phase 30-01) gates MOB-SUB-05: the lava decode extension of decodeFluid/
// fluidState plus the four mob fluid predicates (mobInWater / mobInLava / mobFluidHeight /
// getFluidJumpThreshold). Every behavior asserted here mirrors a jar method verified via
// `javap -c -p temp/cache/26.2-inner.jar` (citations at each port site in fluid.go /
// fluid_physics.go). The tests are PORT-EXACT behavior gates, not feel checks.

// setLava writes lava at pos with the given legacy level (0 = source). Mirrors setWater.
// lavaStateID lives in fluid.go (mirroring waterStateID).
func setLava(mgr interface {
	SetBlock(pk.Position, block.StateID, int) bool
}, pos pk.Position, level int) {
	mgr.SetBlock(pos, lavaStateID(level), dimMinY)
}

// --- Task 1: decodeFluid lava extension (every .isWater consumer audited unperturbed) ---

// TestDecodeFluidLava locks the lava decode branch: the SAME level→amount inversion as water
// (FlowingFluid.getLegacyLevel is shared by Water and Lava), gated on block.Lava instead of
// block.Water. Source → {isLava, source, amount 8}; flowing → {isLava, amount 8-legacy}; the
// isWater flag stays FALSE for every lava cell (a cell is water XOR lava XOR neither).
func TestDecodeFluidLava(t *testing.T) {
	t.Run("lava source decodes to isLava source amount 8", func(t *testing.T) {
		fs := decodeFluid(lavaStateID(0))
		if !fs.isLava || fs.isWater || !fs.source || fs.amount != waterSourceAmount {
			t.Fatalf("lava source decode = %+v, want {isLava:true isWater:false source:true amount:8}", fs)
		}
	})

	t.Run("flowing lava decodes to isLava amount 8-legacy, not falling", func(t *testing.T) {
		fs := decodeFluid(lavaStateID(3)) // legacy 3 -> amount 5
		if !fs.isLava || fs.isWater || fs.source || fs.falling || fs.amount != waterSourceAmount-3 {
			t.Fatalf("flowing lava (legacy 3) decode = %+v, want {isLava:true amount:5}", fs)
		}
	})

	t.Run("falling lava decodes to isLava falling", func(t *testing.T) {
		fs := decodeFluid(lavaStateID(8)) // legacy 8..15 -> falling
		if !fs.isLava || fs.isWater || !fs.falling {
			t.Fatalf("falling lava (legacy 8) decode = %+v, want {isLava:true falling:true}", fs)
		}
	})
}

// TestDecodeFluidWaterUnchanged is the audit gate: the lava extension must NOT perturb any water
// decode. Water source still decodes to {isWater, source, amount 8} with isLava FALSE; flowing
// water unchanged. (The water flow-sim goldens in fluid_test.go are the broader regression.)
func TestDecodeFluidWaterUnchanged(t *testing.T) {
	src := decodeFluid(waterStateID(0))
	if !src.isWater || src.isLava || !src.source || src.amount != waterSourceAmount {
		t.Fatalf("water source decode = %+v, want {isWater:true isLava:false source:true amount:8}", src)
	}
	flowing := decodeFluid(waterStateID(2))
	if !flowing.isWater || flowing.isLava || flowing.source || flowing.amount != waterSourceAmount-2 {
		t.Fatalf("flowing water (legacy 2) decode = %+v, want {isWater:true isLava:false amount:6}", flowing)
	}
}

// --- Task 2: mob fluid predicates (EntityFluidInteraction.isInFluid / Entity.getFluidHeight) ---

// fluidTestMob builds an *Entity at (x,y,z) with the pig's AABB footprint (Width/Height 0.9 from
// data/entity), so the AABB fluid scan spans the pig's collision box exactly as the spawn path
// (NewEntity) would copy it.
func fluidTestMob(x, y, z float64) *Entity {
	return &Entity{
		x: x, y: y, z: z,
		width:  entity.Pig.Width,
		height: entity.Pig.Height,
	}
}

// TestMobInWater: a mob whose AABB overlaps a water block reads mobInWater==true and
// mobFluidHeight(WATER)>0; a dry mob reads false / 0. Mirrors EntityFluidInteraction.isInFluid
// (getFluidHeight(WATER) > 0).
func TestMobInWater(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	wet := fluidTestMob(8.5, 64.0, 8.5)
	if !loop.mobInWater(wet) {
		t.Fatalf("mob standing in a water block should read mobInWater==true")
	}
	if h := loop.mobFluidHeight(wet, fluidWater); h <= 0 {
		t.Fatalf("mob in water should have mobFluidHeight(WATER) > 0, got %v", h)
	}
	if loop.mobInLava(wet) {
		t.Fatalf("a mob in water must NOT read mobInLava==true")
	}

	dry := fluidTestMob(2.5, 64.0, 2.5)
	if loop.mobInWater(dry) {
		t.Fatalf("dry mob should read mobInWater==false")
	}
	if h := loop.mobFluidHeight(dry, fluidWater); h != 0 {
		t.Fatalf("dry mob should have mobFluidHeight(WATER) == 0, got %v", h)
	}
}

// TestMobInLava: a mob in a lava column reads mobInLava==true, mobInWater==false, and
// mobFluidHeight(LAVA)>0 (the full-lava decision — lava is a real read, never const-false).
func TestMobInLava(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	hot := fluidTestMob(8.5, 64.0, 8.5)
	if !loop.mobInLava(hot) {
		t.Fatalf("mob standing in a lava block should read mobInLava==true")
	}
	if loop.mobInWater(hot) {
		t.Fatalf("a mob in lava must NOT read mobInWater==true")
	}
	if h := loop.mobFluidHeight(hot, fluidLava); h <= 0 {
		t.Fatalf("mob in lava should have mobFluidHeight(LAVA) > 0, got %v", h)
	}
	// The WATER tag must read 0 for a lava-only cell (the tag filter is real).
	if h := loop.mobFluidHeight(hot, fluidWater); h != 0 {
		t.Fatalf("mob in lava-only column should have mobFluidHeight(WATER) == 0, got %v", h)
	}
}

// TestMobFluidHeight pins Entity.getFluidHeight(TagKey): a mob in a water source column reads a
// positive WATER height and a zero LAVA height (tag filter), and the mirror for lava. A dry mob
// reads 0 for both. This is the height read FloatGoal.canUse compares against the jump threshold.
func TestMobFluidHeight(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)
	setLava(mgr, pk.Position{X: 12, Y: 64, Z: 12}, 0)

	inWater := fluidTestMob(8.5, 64.0, 8.5)
	if h := loop.mobFluidHeight(inWater, fluidWater); h <= 0 {
		t.Fatalf("mob in water: mobFluidHeight(WATER) = %v, want > 0", h)
	}
	if h := loop.mobFluidHeight(inWater, fluidLava); h != 0 {
		t.Fatalf("mob in water: mobFluidHeight(LAVA) = %v, want 0 (tag filter)", h)
	}

	inLava := fluidTestMob(12.5, 64.0, 12.5)
	if h := loop.mobFluidHeight(inLava, fluidLava); h <= 0 {
		t.Fatalf("mob in lava: mobFluidHeight(LAVA) = %v, want > 0", h)
	}
	if h := loop.mobFluidHeight(inLava, fluidWater); h != 0 {
		t.Fatalf("mob in lava: mobFluidHeight(WATER) = %v, want 0 (tag filter)", h)
	}

	dry := fluidTestMob(2.5, 64.0, 2.5)
	if h := loop.mobFluidHeight(dry, fluidWater); h != 0 {
		t.Fatalf("dry mob: mobFluidHeight(WATER) = %v, want 0", h)
	}
}

// TestFluidJumpThreshold pins getFluidJumpThreshold to the jar formula
// (Entity.getFluidJumpThreshold = getEyeHeight() < 0.4 ? 0.0 : 0.4). The pig's default eye
// height is height*0.85 = 0.765 (> 0.4), so the pig's threshold is 0.4 — NOT 0.0.
func TestFluidJumpThreshold(t *testing.T) {
	loop, _ := newFluidLoop()
	pig := fluidTestMob(0.5, 64.0, 0.5) // pig dims: height 0.9 -> eye 0.765 -> threshold 0.4
	if th := loop.getFluidJumpThreshold(pig); !floatNear(th, 0.4, 1e-9) {
		t.Fatalf("pig getFluidJumpThreshold = %v, want 0.4 (eyeHeight 0.765 >= 0.4)", th)
	}

	// A short entity (eye height < 0.4) takes the 0.0 branch. Height 0.4 -> eye 0.34 < 0.4.
	short := &Entity{x: 0.5, y: 64.0, z: 0.5, width: 0.4, height: 0.4}
	if th := loop.getFluidJumpThreshold(short); !floatNear(th, 0.0, 1e-9) {
		t.Fatalf("short entity getFluidJumpThreshold = %v, want 0.0 (eyeHeight 0.34 < 0.4)", th)
	}
}
