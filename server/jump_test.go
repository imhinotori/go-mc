package server

// jump_test.go — Phase 30-02 gates MOB-SUB-04: the mob jump chain (jumpControl + noJumpDelay +
// jumping/setJumping + the serverAiStep JUMP slot + the aiStep jump branch) and the plugin handle
// ops (entity.in_water/fluid_height/in_lava + nav.jump()). Every behavior asserted here mirrors a
// jar method verified via `javap -c -p temp/cache/26.2-inner.jar` (citations at each port site in
// ai_mob.go / entity.go / jump.go / plugin_entity.go):
//
//   - net.minecraft.world.entity.ai.control.JumpControl  (jump()=jump=true; tick()=setJumping(jump); jump=false)
//   - net.minecraft.world.entity.LivingEntity.setJumping(boolean)  (this.jumping = b)
//   - net.minecraft.world.entity.LivingEntity.aiStep  (the `if (jumping && isAffectedByFluids())` branch
//       + the noJumpDelay decrement-at-top and noJumpDelay=0 when not jumping/affected)
//   - net.minecraft.world.entity.LivingEntity.jumpInLiquid  (setDeltaMovement(dm + (0,0.03999999910593033,0)))
//   - net.minecraft.world.entity.LivingEntity.jumpFromGround  (vy = max(getJumpPower()=0.42, vy); sprint nudge)
//
// These are PORT-EXACT behavior gates, not feel checks.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
	"go.starlark.net/starlark"
)

// --- Task 1: jumpControl + setJumping + the RNG-free JUMP slot ---

// TestJumpControl pins JumpControl: jump() (doJump) arms the flag; tick() pushes it into
// e.jumping then clears the flag, so a second tick with no doJump() leaves jumping false.
// [javap JumpControl: jump(){jump=true}; tick(){mob.setJumping(jump); jump=false}.]
func TestJumpControl(t *testing.T) {
	e := NewEntity(1, entity.Pig, 0, 64, 0)

	var jc jumpControl
	jc.doJump()
	jc.tick(e)
	if !e.jumping {
		t.Fatal("after doJump + tick, e.jumping should be true")
	}
	if jc.jump {
		t.Fatal("tick must clear the internal jump flag")
	}

	// A second tick with no doJump() clears jumping (the flag was reset).
	jc.tick(e)
	if e.jumping {
		t.Fatal("a tick with no doJump must leave e.jumping false")
	}
}

// TestServerAiStepJumpSlot pins the JUMP slot: jumpControl.tick runs AFTER navigation.tick (jar
// order), and the slot draws ZERO RNG. We prove RNG-freeness by running serverAiStep with NO
// running goal and asserting the per-mob RNG stream is untouched (a reference rng seeded
// identically still produces the SAME next draw). [Pitfall: the only new RNG is FloatGoal.tick in
// Plan 03 — never the shared serverAiStep flow.]
func TestServerAiStepJumpSlot(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Pig, 100, 64, 200)
	m := newPigAI()
	e.ai = m

	// A reference rng seeded identically to the mob's: if serverAiStep draws nothing, the mob's
	// rng and the reference must still be in lockstep after the step.
	ref := newEntityRandom(defaultEntityRandomSeed)

	// No goal can fire this tick: dry world (no player, no water) → stroll/look/lookAround all
	// roll or probe to false EXCEPT they each draw their canUse roll. To isolate the JUMP slot's
	// RNG-freeness we run serverAiStep on a mob whose goals are all removed.
	m.goals = goalSelector{}

	m.serverAiStep(loop, e)

	if got, want := e.ai.rng.nextFloat(), ref.nextFloat(); got != want {
		t.Fatalf("serverAiStep with no goal drew RNG: mob rng=%v ref=%v (the JUMP slot must be PURE)", got, want)
	}
}

// TestNoJumpDelay pins the noJumpDelay decrement at the top of serverAiStep (the jar aiStep top
// `if (noJumpDelay > 0) noJumpDelay--;`). To isolate the DECREMENT from the jump branch's reset (the
// branch sets noJumpDelay=0 when the mob is not jumping / not in a fluid), the mob is jumping in a
// DEEP water column — it takes the jumpInLiquid path, which never touches noJumpDelay — so only the
// top-of-step decrement moves it. It counts toward 0 and never goes negative.
func TestNoJumpDelay(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 0, Y: 64, Z: 0}, 0)
	e := NewEntity(1, entity.Pig, 0.5, 64.0, 0.5)
	m := newPigAI()
	m.goals = goalSelector{} // no goals → isolate the slot
	e.ai = m
	loop.only().entities.add(e)
	m.noJumpDelay = 2
	e.onGround = false // floating, so the water branch fires (and never resets noJumpDelay)

	// jumpControl.tick will set e.jumping from the armed flag; arm it each step so the mob stays in the
	// jumpInLiquid branch (a goal would normally do this via doJump()).
	m.jumpControl.doJump()
	m.serverAiStep(loop, e)
	if m.noJumpDelay != 1 {
		t.Fatalf("noJumpDelay after 1 step = %d, want 1", m.noJumpDelay)
	}
	m.jumpControl.doJump()
	m.serverAiStep(loop, e)
	if m.noJumpDelay != 0 {
		t.Fatalf("noJumpDelay after 2 steps = %d, want 0", m.noJumpDelay)
	}
	m.jumpControl.doJump()
	m.serverAiStep(loop, e)
	if m.noJumpDelay != 0 {
		t.Fatalf("noJumpDelay must never go negative, got %d", m.noJumpDelay)
	}
}

// --- Task 2: the aiStep jump branch (jumpInLiquid +0.04 / jumpFromGround 0.42) ---

// jumpTestMob builds a jumping mob at (x,y,z) with the pig AABB and an AI (for noJumpDelay).
func jumpTestMob(x, y, z float64) *Entity {
	e := &Entity{
		x: x, y: y, z: z,
		width:  entity.Pig.Width,
		height: entity.Pig.Height,
	}
	e.ai = newPigAI()
	e.jumping = true
	return e
}

// TestEntityJumpStep_Water: a jumping mob in a water column (onGround) gains vy += 0.04
// (jumpInLiquid), NOT 0.42. [javap aiStep: inWaterAndHasFluidHeight && fluidHeight>threshold →
// jumpInLiquid(WATER).]
func TestEntityJumpStep_Water(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)
	e := jumpTestMob(8.5, 64.0, 8.5)
	e.onGround = false // floating in the column

	before := e.vy
	loop.entityJumpStep(e)
	if got := e.vy - before; !floatNear(got, fluidJumpImpulse, 1e-12) {
		t.Fatalf("jumping mob in water: Δvy = %v, want +%v (jumpInLiquid)", got, fluidJumpImpulse)
	}
}

// TestEntityJumpStep_Lava: a jumping mob in lava (not onGround) gains vy += 0.04 (jumpInLiquid LAVA).
func TestEntityJumpStep_Lava(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)
	e := jumpTestMob(8.5, 64.0, 8.5)
	e.onGround = false

	before := e.vy
	loop.entityJumpStep(e)
	if got := e.vy - before; !floatNear(got, fluidJumpImpulse, 1e-12) {
		t.Fatalf("jumping mob in lava: Δvy = %v, want +%v (jumpInLiquid LAVA)", got, fluidJumpImpulse)
	}
}

// TestEntityJumpStep_Land: a jumping mob on dry land (onGround, noJumpDelay==0) gains vy = 0.42
// (jumpFromGround) and sets noJumpDelay = 10. [javap aiStep: (onGround || …) && noJumpDelay==0 →
// jumpFromGround(); noJumpDelay = 10.]
func TestEntityJumpStep_Land(t *testing.T) {
	loop, _ := newFluidLoop()
	e := jumpTestMob(2.5, 64.0, 2.5) // dry
	e.onGround = true
	e.ai.noJumpDelay = 0

	loop.entityJumpStep(e)
	if !floatNear(e.vy, baseJumpPower, 1e-6) {
		t.Fatalf("jumping mob on land: vy = %v, want %v (jumpFromGround)", e.vy, baseJumpPower)
	}
	if e.ai.noJumpDelay != 10 {
		t.Fatalf("land jump must set noJumpDelay = 10, got %d", e.ai.noJumpDelay)
	}
}

// TestEntityJumpStep_LandDelayGate: a jumping mob on land with noJumpDelay > 0 does NOT jump (the
// delay gate blocks the land jump).
func TestEntityJumpStep_LandDelayGate(t *testing.T) {
	loop, _ := newFluidLoop()
	e := jumpTestMob(2.5, 64.0, 2.5)
	e.onGround = true
	e.ai.noJumpDelay = 5

	before := e.vy
	loop.entityJumpStep(e)
	if e.vy != before {
		t.Fatalf("land jump gated by noJumpDelay>0 must not change vy: %v -> %v", before, e.vy)
	}
}

// TestEntityJumpStep_NotJumping: a non-jumping mob is a no-op (vy unchanged).
func TestEntityJumpStep_NotJumping(t *testing.T) {
	loop, _ := newFluidLoop()
	e := jumpTestMob(2.5, 64.0, 2.5)
	e.jumping = false
	e.onGround = true

	before := e.vy
	loop.entityJumpStep(e)
	if e.vy != before {
		t.Fatalf("non-jumping mob: vy must be unchanged, %v -> %v", before, e.vy)
	}
}

// TestJumpFromGround pins jumpFromGround in isolation: vy = max(0.42, vy) and needsSync/hasImpulse.
// [javap jumpFromGround: f=getJumpPower()=0.42; setDeltaMovement(x, max(f, y), z).]
func TestJumpFromGround(t *testing.T) {
	e := jumpTestMob(0, 64, 0)
	e.vy = -1.0 // falling → max(0.42, -1.0) = 0.42
	jumpFromGround(e)
	if !floatNear(e.vy, baseJumpPower, 1e-6) {
		t.Fatalf("jumpFromGround over a falling mob: vy = %v, want %v (max with the impulse)", e.vy, baseJumpPower)
	}
}

// TestJumpInLiquid pins jumpInLiquid in isolation: vy += 0.04 (the exact jar double).
func TestJumpInLiquid(t *testing.T) {
	e := jumpTestMob(0, 64, 0)
	e.vy = 0.1
	jumpInLiquid(e)
	if !floatNear(e.vy, 0.1+fluidJumpImpulse, 1e-12) {
		t.Fatalf("jumpInLiquid: vy = %v, want %v", e.vy, 0.1+fluidJumpImpulse)
	}
}

// --- Task 3: plugin handle attrs (entity.in_water/fluid_height/in_lava + nav.jump()) ---

// TestHandleInWater: a handle for a mob in water reads entity.in_water==True, fluid_height>0; a dry
// mob reads False/0.0. Frozen scalars, re-resolved via h.store(), gated on capEntitiesRead.
func TestHandleInWater(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	wet := NewEntity(1, entity.Pig, 8.5, 64.0, 8.5)
	wet.ai = newPigAI()
	loop.only().entities.add(wet)
	h := newEntityHandle(loop, wet.id, capAll)

	if v, _ := h.Attr("in_water"); v != starlark.Bool(true) {
		t.Fatalf("in_water for a wet mob = %v, want True", v)
	}
	if fh := attrFloat(t, h, "fluid_height"); fh <= 0 {
		t.Fatalf("fluid_height for a wet mob = %v, want > 0", fh)
	}
	if v, _ := h.Attr("in_lava"); v != starlark.Bool(false) {
		t.Fatalf("in_lava for a wet mob = %v, want False", v)
	}

	dry := NewEntity(2, entity.Pig, 2.5, 64.0, 2.5)
	dry.ai = newPigAI()
	loop.only().entities.add(dry)
	hd := newEntityHandle(loop, dry.id, capAll)
	if v, _ := hd.Attr("in_water"); v != starlark.Bool(false) {
		t.Fatalf("in_water for a dry mob = %v, want False", v)
	}
	if fh := attrFloat(t, hd, "fluid_height"); fh != 0 {
		t.Fatalf("fluid_height for a dry mob = %v, want 0", fh)
	}
}

// TestHandleInLava: a handle for a mob in lava reads entity.in_lava==True (and in_water==False).
func TestHandleInLava(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	hot := NewEntity(1, entity.Pig, 8.5, 64.0, 8.5)
	hot.ai = newPigAI()
	loop.only().entities.add(hot)
	h := newEntityHandle(loop, hot.id, capAll)

	if v, _ := h.Attr("in_lava"); v != starlark.Bool(true) {
		t.Fatalf("in_lava for a mob in lava = %v, want True", v)
	}
	if v, _ := h.Attr("in_water"); v != starlark.Bool(false) {
		t.Fatalf("in_water for a mob in lava = %v, want False", v)
	}
}

// TestHandleInWaterCapGate: in_water/fluid_height/in_lava require capEntitiesRead.
func TestHandleInWaterCapGate(t *testing.T) {
	loop, _ := newFluidLoop()
	e := NewEntity(1, entity.Pig, 0, 64, 0)
	e.ai = newPigAI()
	loop.only().entities.add(e)
	denied := newEntityHandle(loop, e.id, capAll&^capEntitiesRead)
	if _, err := denied.Attr("in_water"); err == nil {
		t.Fatal("in_water without capEntitiesRead must error")
	}
}

// TestNavJump: nav.jump() on an AI mob arms e.ai.jumpControl.jump; on a removed/non-AI entity it
// errors cleanly (no panic); without capNav it errors with capError("nav").
func TestNavJump(t *testing.T) {
	loop, _ := newFluidLoop()
	e := NewEntity(1, entity.Pig, 0, 64, 0)
	e.ai = newPigAI()
	loop.only().entities.add(e)

	nav := newNavHandle(loop, e.id, capAll)
	if _, err := callMethod(t, nav, "jump"); err != nil {
		t.Fatalf("nav.jump() error: %v", err)
	}
	if !e.ai.jumpControl.jump {
		t.Fatal("nav.jump() must arm e.ai.jumpControl.jump")
	}

	// capNav gate.
	noNav := newNavHandle(loop, e.id, capAll&^capNav)
	if _, err := callMethod(t, noNav, "jump"); err == nil {
		t.Fatal("nav.jump() without capNav must error")
	}

	// Removed entity errors cleanly.
	loop.only().entities.remove(e.id)
	if _, err := callMethod(t, nav, "jump"); err == nil {
		t.Fatal("nav.jump() on a removed entity must error, not panic")
	}
}
