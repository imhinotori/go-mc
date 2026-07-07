package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// elytraStack builds a glide-capable elytra: GLIDER + EQUIPPABLE (Slot=CHEST ordinal 4, Damageable=
// true) + MAX_DAMAGE/DAMAGE so it is a damageable item. maxDamage/damage set the durability state so a
// test can drive it to nearly-broken. Mirrors armor_durability_test.go equippableArmorStack.
func elytraStack(maxDamage, damage int) component.SlotData {
	s := component.SlotData{ItemID: 900, Count: 1}
	p := component.DecodePatch(s)
	p.Set(compMaxDamage, &component.MaxDamage{VarInt: pk.VarInt(maxDamage)})
	p.Set(compDamage, &component.Damage{VarInt: pk.VarInt(damage)})
	p.Set(compEquippable, &component.Equippable{Slot: pk.VarInt(eqSlotChest), Damageable: pk.Boolean(true)})
	p.Set(compGlider, &component.Glider{})
	return p.ApplyTo(s)
}

// gliderPlayer builds an airborne player wearing a fresh elytra in the CHEST slot (menu slot 6).
func gliderPlayer(maxDamage, damage int) (*TickLoop, *tickPlayer) {
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{entityID: 1, y: 100}
	p.onGround = false
	inv := ensureInventory(p)
	inv.set(6, elytraStack(maxDamage, damage)) // CHEST slot (menu 6)
	loop.players = append(loop.players, p)
	return loop, p
}

// TestFallFlyingStartWithElytra: a player airborne with an undamaged elytra double-jumps (the
// START_FALL_FLYING command -> tryToStartFallFlying) and the FALL_FLYING flag is set.
func TestFallFlyingStartWithElytra(t *testing.T) {
	loop, p := gliderPlayer(432, 0)
	if !loop.tryToStartFallFlying(p) {
		t.Fatalf("tryToStartFallFlying must succeed for an airborne player with an undamaged elytra")
	}
	if !p.fallFlying {
		t.Fatalf("FALL_FLYING flag not set after a successful start")
	}
	if playerSharedFlags(p)&fallFlyingSharedFlagBit == 0 {
		t.Fatalf("playerSharedFlags must carry the FALL_FLYING bit (0x80) after start")
	}
}

// TestFallFlyingNoElytraCannotStart: a player with no elytra cannot start fall-flying.
func TestFallFlyingNoElytraCannotStart(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{entityID: 1, y: 100}
	ensureInventory(p) // empty inventory, no elytra
	loop.players = append(loop.players, p)
	if loop.tryToStartFallFlying(p) {
		t.Fatalf("tryToStartFallFlying must fail without a glide-capable item")
	}
	if p.fallFlying {
		t.Fatalf("FALL_FLYING flag must stay clear without an elytra")
	}
}

// TestFallFlyingOnGroundCannotStart: an on-ground player cannot start (canGlide gate: onGround -> false).
func TestFallFlyingOnGroundCannotStart(t *testing.T) {
	loop, p := gliderPlayer(432, 0)
	p.onGround = true
	if loop.tryToStartFallFlying(p) {
		t.Fatalf("tryToStartFallFlying must fail on the ground (canGlide onGround gate)")
	}
	if p.fallFlying {
		t.Fatalf("FALL_FLYING flag must stay clear when starting on the ground")
	}
}

// TestFallFlyingLandingClearsFlag: once gliding, landing (onGround true -> canGlide false) clears the
// FALL_FLYING flag on the next updateFallFlying.
func TestFallFlyingLandingClearsFlag(t *testing.T) {
	loop, p := gliderPlayer(432, 0)
	loop.tryToStartFallFlying(p)
	if !p.fallFlying {
		t.Fatalf("precondition: player must be gliding")
	}
	p.onGround = true // landed
	loop.tickPlayerFallFlying(p)
	if p.fallFlying {
		t.Fatalf("landing must clear FALL_FLYING (canGlide onGround gate -> stopFallFlying)")
	}
	if p.fallFlyTicks != 0 {
		t.Fatalf("fallFlyTicks must reset to 0 when not fall-flying, got %d", p.fallFlyTicks)
	}
}

// TestFallFlyingDurabilityDrainsOncePerSecond: gliding drains the elytra exactly 1 durability every 20
// ticks (once per second) -- the i%10==0 && (i/10)%2==0 gate (i = fallFlyTicks+1). Over 40 ticks of
// gliding the damage value goes 0 -> 2 (hits at tick 20 and tick 40).
func TestFallFlyingDurabilityDrainsOncePerSecond(t *testing.T) {
	loop, p := gliderPlayer(432, 0)
	loop.tryToStartFallFlying(p)

	inv := ensureInventory(p)
	// Tick 40 times while gliding (kept airborne). The durability hit lands at i==20 and i==40.
	for tick := 1; tick <= 40; tick++ {
		p.onGround = false
		loop.tickPlayerFallFlying(p)
	}
	got := stackDamageValue(inv.get(6))
	if got != 2 {
		t.Fatalf("elytra damage after 40 ticks of gliding = %d, want 2 (1 per 20 ticks)", got)
	}
	// Spot-check the exact hit tick: fresh run, damage must still be 0 at tick 19 and become 1 at tick 20.
	loop2, p2 := gliderPlayer(432, 0)
	loop2.tryToStartFallFlying(p2)
	inv2 := ensureInventory(p2)
	for tick := 1; tick <= 19; tick++ {
		p2.onGround = false
		loop2.tickPlayerFallFlying(p2)
	}
	if d := stackDamageValue(inv2.get(6)); d != 0 {
		t.Fatalf("elytra damage at tick 19 = %d, want 0 (first hit is tick 20)", d)
	}
	p2.onGround = false
	loop2.tickPlayerFallFlying(p2) // tick 20
	if d := stackDamageValue(inv2.get(6)); d != 1 {
		t.Fatalf("elytra damage at tick 20 = %d, want 1 (the once-per-second drain)", d)
	}
}

// TestGlidePhysicsMatchesVanilla asserts updateFallFlyingMovement reproduces the ported vanilla vector
// formula for a known look + velocity. The expected value is computed by an INDEPENDENT re-derivation
// of the LivingEntity.updateFallFlyingMovement bytecode (not by calling the function under test), so a
// regression in the port is caught.
func TestGlidePhysicsMatchesVanilla(t *testing.T) {
	// A player looking slightly down (pitch +10 deg, yaw 30 deg), descending with some horizontal speed.
	yaw, pitch := float32(30.0), float32(10.0)
	inX, inY, inZ := 0.3, -0.5, 0.1
	grav := 0.08 // getEffectiveGravity default (GRAVITY attribute 0.08)

	gotX, gotY, gotZ := updateFallFlyingMovement(inX, inY, inZ, yaw, pitch, grav)

	wx, wy, wz := refUpdateFallFlyingMovement(inX, inY, inZ, yaw, pitch, grav)
	const eps = 1e-12
	if math.Abs(gotX-wx) > eps || math.Abs(gotY-wy) > eps || math.Abs(gotZ-wz) > eps {
		t.Fatalf("glide velocity = (%v,%v,%v), want (%v,%v,%v)", gotX, gotY, gotZ, wx, wy, wz)
	}
}

// TestGlidePhysicsPitchUpLift: looking UP (negative pitch) adds the upward lift term (d12*3.2 on Y), so
// the Y velocity is higher than the same run looking flat -- the observable "pull up to gain altitude".
func TestGlidePhysicsPitchUpLift(t *testing.T) {
	yaw := float32(0.0)
	inX, inY, inZ := 0.0, -0.4, 1.0
	grav := 0.08
	_, upY, _ := updateFallFlyingMovement(inX, inY, inZ, yaw, -40.0, grav) // looking up
	_, flatY, _ := updateFallFlyingMovement(inX, inY, inZ, yaw, 0.0, grav) // looking flat
	if !(upY > flatY) {
		t.Fatalf("looking up must yield more Y velocity than flat: up=%v flat=%v", upY, flatY)
	}
}

// refUpdateFallFlyingMovement is the INDEPENDENT reference re-derivation of
// LivingEntity.updateFallFlyingMovement (javap-cited), used only to check the port. It duplicates the
// bytecode ops so the test does not merely echo the function under test.
func refUpdateFallFlyingMovement(inX, inY, inZ float64, yaw, pitch float32, grav float64) (float64, float64, float64) {
	lookX, _, lookZ := playerViewVector(yaw, pitch)
	rad := pitch * 0.017453292
	d4 := math.Sqrt(lookX*lookX + lookZ*lookZ)
	d6 := math.Sqrt(inX*inX + inZ*inZ)
	cosF := float32(math.Cos(float64(rad)))
	d10 := float64(cosF) * float64(cosF)

	inY += grav * (-1.0 + d10*0.75)
	if inY < 0.0 && d4 > 0.0 {
		d12 := inY * -0.1 * d10
		inX += lookX * d12 / d4
		inY += d12
		inZ += lookZ * d12 / d4
	}
	if rad < 0.0 && d4 > 0.0 {
		sinF := float32(math.Sin(float64(rad)))
		d12 := d6 * float64(-sinF) * 0.04
		inX += -lookX * d12 / d4
		inY += d12 * 3.2
		inZ += -lookZ * d12 / d4
	}
	if d4 > 0.0 {
		inX += (lookX/d4*d6 - inX) * 0.1
		inZ += (lookZ/d4*d6 - inZ) * 0.1
	}
	return inX * 0.99, inY * 0.98, inZ * 0.99
}
