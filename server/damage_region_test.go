package server

// damage_region_test.go — Wave 0 (TDD RED) scaffold for Phase 29 Plan 03: the attack ROUTING that
// delivers a player's hit to a mob, and the cross-region barrier-queue that makes it Folia-safe
// (MOB-SUB-01, MOB-SUB-02). The five behaviors below pin:
//   - the SAME-region fast path (handleAttack applies mob damage synchronously this tick),
//   - the CROSS-region path (a hit is queued as a damageIntent on the SOURCE region and applied at
//     the coordinator barrier — health drops AFTER the barrier, never mid-tick; strictRegion=true
//     does NOT panic, proving no cur() resolves the cross-region victim),
//   - the drop-if-gone guard (applyCrossRegionDamage drops the intent if the mob despawned),
//   - the reach gate (a hit beyond attackReach is rejected),
//   - the was_hurt / last_damage_type frozen-scalar handle attrs.
// RED until Tasks 1-4 land the routing + the attrs. The -race coverage runs via the Docker command
// in the Task 4 verify (CGO=1, strictRegion armed over the cross-region tests).
//
// The two-region fixture mirrors region_n2_regression_test.go (newN2Loop): the SHARED world is wired
// into every region; region 0 owns column {0,0}, region 1 owns column {1,0} (the (X^Z)&1 checkerboard).
// World X in [0,16) is chunk 0 (region 0); X in [16,32) is chunk 1 (region 1). A player at X≈8 attacks
// a mob at X≈24.5 to cross the region seam.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/tag"
	"go.starlark.net/starlark"
)

// newDamageRegionMob builds a Pig *Entity with the given health at (x,y,z), added to the OWNING
// region's store (regionForColumn) via withRegion so the store routing matches production. Returns
// the mob so a test can assert its health/timing.
func newDamageRegionMob(loop *TickLoop, id int32, x, y, z float64, health float32) *Entity {
	e := NewEntity(id, entity.Pig, x, y, z)
	e.health = health
	owner := loop.regionForColumn(columnOf(x, z))
	loop.withRegion(owner, func() { loop.cur().entities.add(e) })
	return e
}

// TestSameRegionDamage: a player and a mob in the SAME region (both in region 0's columns); a
// charged handleAttack within reach applies damage SYNCHRONOUSLY — the mob's health drops on the
// handleAttack call itself (the fast path), with NO barrier needed.
func TestSameRegionDamage(t *testing.T) {
	loop, _ := newN2Loop(t)

	// Attacker + mob both in region 0 (column {0,0}, world X in [0,16)).
	attacker := placeAttackPlayer(loop, 1000, 8.0, 64, 8.0)
	charge(attacker)
	mob := newDamageRegionMob(loop, 1, 8.5, 64, 8.0, 20.0) // ~0.5 block away, in reach, region 0

	if regionOf(columnOf(mob.x, mob.z)) != regionOf(columnOf(attacker.x, attacker.z)) {
		t.Fatalf("test precondition: attacker and mob must share a region")
	}

	before := mob.health
	loop.handleAttack(attacker, attackPacket(mob.id))

	if mob.health >= before {
		t.Fatalf("same-region hit: mob health did not drop synchronously (got %v, was %v)", mob.health, before)
	}
}

// TestCrossRegionDamage: a player in region 0 attacks a mob in region 1 (across the checkerboard
// seam). The hit must be QUEUED (health unchanged immediately after handleAttack) and applied only
// at the coordinator barrier (applyCrossRegionDamage). strictRegion=true must NOT panic — the
// cross-region victim is resolved via owningRegion, never cur().
func TestCrossRegionDamage(t *testing.T) {
	loop, _ := newN2Loop(t)
	loop.strictRegion = true // arm the loud guard: any unwrapped cur() on the cross-region path panics

	// Straddle the chunk-0/chunk-1 seam: the mob is at world X=16.5 (chunk 1 -> region 1), the attacker
	// at X=15.0 (chunk 0 -> region 0), same Z — 1.5 blocks apart (within attackReach=3.5) but owned by
	// DIFFERENT regions (the (X^Z)&1 checkerboard makes adjacent columns different regions).
	mob := newDamageRegionMob(loop, 1, 16.5, 64, 8.0, 20.0)  // region 1 (chunk X=1)
	attacker := placeAttackPlayer(loop, 1000, 15.0, 64, 8.0) // region 0 (chunk X=0), within reach
	charge(attacker)

	if regionOf(columnOf(mob.x, mob.z)) == regionOf(columnOf(attacker.x, attacker.z)) {
		t.Fatalf("test precondition: attacker and mob must be in DIFFERENT regions")
	}
	if !loop.withinAttackReachEntity(attacker, mob) {
		t.Fatalf("test precondition: the cross-region mob must be within reach")
	}

	before := mob.health
	loop.handleAttack(attacker, attackPacket(mob.id))

	// CROSS-REGION: the hit is queued, NOT applied mid-tick. Health must be unchanged here.
	if mob.health != before {
		t.Fatalf("cross-region hit applied mid-tick (health %v != %v) — must be barrier-queued", mob.health, before)
	}

	// Drain at the barrier — health must drop now.
	loop.applyCrossRegionDamage()
	if mob.health >= before {
		t.Fatalf("cross-region hit: mob health did not drop after the barrier drain (got %v, was %v)", mob.health, before)
	}
}

// TestCrossRegionDamage_DropIfGone: if the mob despawns before the barrier, applyCrossRegionDamage
// drops the queued intent (no panic, no phantom write).
func TestCrossRegionDamage_DropIfGone(t *testing.T) {
	loop, _ := newN2Loop(t)
	loop.strictRegion = true

	mob := newDamageRegionMob(loop, 1, 16.5, 64, 8.0, 20.0)  // region 1
	attacker := placeAttackPlayer(loop, 1000, 15.0, 64, 8.0) // region 0, within reach
	charge(attacker)

	loop.handleAttack(attacker, attackPacket(mob.id))

	// Despawn the mob from region 1's store BEFORE the barrier drains.
	owner := loop.regionForColumn(columnOf(mob.x, mob.z))
	loop.withRegion(owner, func() { loop.cur().entities.remove(mob.id) })

	// Must not panic, must not phantom-write.
	loop.applyCrossRegionDamage()
}

// TestReachGate: a player attacks a mob beyond attackReach -> no damage (silent no-op).
func TestReachGate(t *testing.T) {
	loop, _ := newN2Loop(t)

	attacker := placeAttackPlayer(loop, 1000, 8.0, 64, 8.0)
	charge(attacker)
	mob := newDamageRegionMob(loop, 1, 8.5, 64, 100.0, 20.0) // far away in Z (out of reach), region 0

	before := mob.health
	loop.handleAttack(attacker, attackPacket(mob.id))

	if mob.health != before {
		t.Fatalf("out-of-reach hit changed mob health to %v, want %v (no-op)", mob.health, before)
	}
}

// TestMobVictimSprintExtraKnockback pins the sprint-extra melee knockback tail wired into
// handleMobAttack: a player attacking a mob applies the BASE 0.4 knockback (inside applyMobAttackDamage
// -> dealDefaultKnockbackEntity) PLUS, when the attacker is sprinting at full strength (scale>0.9), the
// causeExtraKnockback +0.5 impulse (Player.attack offsets 252-283:
// causeExtraKnockback(target, getKnockback + (sprintKb?0.5:0), ...)). A sprinting-full-strength hit
// must recoil the mob HARDER than an identical non-sprint hit.
//
// Geometry: attacker at z=8.0 with yaw=0 (facing -Z: extra dir dx=sin0=0, dz=-cos0=-1); mob at z=8.5, so
// the base knockback direction (attacker.pos - mob.pos = -Z) and the yaw-derived extra direction (-Z)
// align, both pushing the mob +Z. So the sprint case's extra +0.5 stacks coherently and vz grows.
func TestMobVictimSprintExtraKnockback(t *testing.T) {
	hitVZ := func(sprint bool) float64 {
		loop, _ := newN2Loop(t)
		attacker := placeAttackPlayer(loop, 1000, 8.0, 64, 8.0)
		attacker.yaw = 0 // facing -Z: extra knockback dir (sin0, -cos0) = (0, -1)
		attacker.sprinting = sprint
		charge(attacker) // scale = 1.0 -> fullStrength (scale > 0.9), so sprintKb = sprinting && true
		mob := newDamageRegionMob(loop, 1, 8.0, 64, 8.5, 20.0)
		mob.onGround = true
		if regionOf(columnOf(mob.x, mob.z)) != regionOf(columnOf(attacker.x, attacker.z)) {
			t.Fatalf("test precondition: attacker and mob must share a region")
		}
		loop.handleAttack(attacker, attackPacket(mob.id))
		return mob.vz
	}

	noSprint := hitVZ(false)
	sprint := hitVZ(true)

	// The base knockback must have fired in both cases (the mob recoils +Z).
	if noSprint <= 0 {
		t.Fatalf("non-sprint hit: mob vz = %v, want > 0 (base knockback +Z)", noSprint)
	}
	// The sprint-full-strength hit stacks the +0.5 extra impulse on the base, so it must recoil harder.
	if sprint <= noSprint {
		t.Fatalf("sprint hit vz = %v must exceed non-sprint vz = %v (the +0.5 causeExtraKnockback impulse)", sprint, noSprint)
	}
	// Exact 1:1 check of the stacked math: base knockback sets vz to +knockbackDefaultPower (dir -Z
	// normalized, power 0.4000000059604645 the double-nearest-float 0.4, from cur 0 -> 0/2 - (-power) =
	// power); the extra +0.5 impulse then reads cur.vz=power -> power/2 - (-0.5) = power/2 + 0.5.
	// getKnockback(attacker) = ATTACK_KNOCKBACK(0)/2 = 0, so extra strength = 0 + 0.5 = 0.5.
	wantSprintVZ := knockbackDefaultPower/2.0 + 0.5
	if math.Abs(sprint-wantSprintVZ) > 1e-9 {
		t.Fatalf("sprint hit vz = %v, want %v (base %v then stacked extra 0.5)", sprint, wantSprintVZ, knockbackDefaultPower)
	}
}

// TestMobVictimSweepHitsSecondMob pins the mob-inclusive sweep tail: doSweepAttackMob scans the owner
// region store for other LivingEntity mobs within the 3-block sweep radius (distanceToSqr < 9.0) around
// the attacker, hurts each via applyMobAttackDamage, and knocks it back (Player.doSweepAttack offsets
// 46-227: getEntitiesOfClass(LivingEntity, ...) -> hurtServer + knockback(0.4, ...)). A second nearby
// mob must take the sweep hit (health drops) AND get knocked back.
//
// isSweepAttack short-circuits false in v1 (no sword items), so the sweep gate would not fire through
// handleAttack. This test drives doSweepAttackMob DIRECTLY (the sweep effect), the same way a sword-item
// port will reach it — proving the mob-scan half of the port lands the hit + knockback.
func TestMobVictimSweepHitsSecondMob(t *testing.T) {
	loop, _ := newN2Loop(t)
	attacker := placeAttackPlayer(loop, 1000, 8.0, 64, 8.0)
	attacker.yaw = 0
	charge(attacker)

	// The primary mob the player struck, plus a SECOND mob 1 block away (within the 3-block sweep radius
	// of the attacker: distanceToSqr(attacker, second) = 1 + 0 + 0.25 < 9). Both in region 0.
	primary := newDamageRegionMob(loop, 1, 8.0, 64, 8.5, 20.0)
	second := newDamageRegionMob(loop, 2, 9.0, 64, 8.5, 20.0)
	second.onGround = true
	primary.ai = newPigAI()
	reseedMobAI(primary.ai, primary.id)
	second.ai = newPigAI()
	reseedMobAI(second.ai, second.id)

	// distanceToSqr(attacker, second) = (9-8)^2 + 0 + (8.5-8)^2 = 1.25 < 9.0 (within sweep radius).
	dsq := (second.x-attacker.x)*(second.x-attacker.x) + (second.y-attacker.y)*(second.y-attacker.y) + (second.z-attacker.z)*(second.z-attacker.z)
	if dsq >= 9.0 {
		t.Fatalf("test precondition: second mob must be within the sweep radius (dsq=%v, want <9)", dsq)
	}

	beforeHealth := second.health
	beforeVZ := second.vz

	// scale 1.0 (charged), damage 1.0 (bare-hand full) -> sweepDamage 1.0 * scale 1.0 = 1.0 to each.
	loop.doSweepAttackMob(attacker, primary, 1.0, 1.0)

	// The second mob took the sweep hit.
	if second.health >= beforeHealth {
		t.Fatalf("sweep: second mob health = %v, want < %v (sweep hit landed)", second.health, beforeHealth)
	}
	// ...and was knocked back (0.4 sweep impulse along the attacker facing, +Z with yaw 0). vz must change.
	if second.vz == beforeVZ {
		t.Fatalf("sweep: second mob vz unchanged (%v) — the 0.4 sweep knockback did not fire", second.vz)
	}
	if second.vz <= 0 {
		t.Fatalf("sweep: second mob vz = %v, want > 0 (knocked +Z away from the attacker)", second.vz)
	}
	// The primary mob is skipped by the sweep (skipID == primary.id): its health is untouched here.
	if primary.health != 20.0 {
		t.Fatalf("sweep: primary mob health = %v, want 20 (the primary is skipped, not swept)", primary.health)
	}
}

// TestWasHurtHandleAttr: after a hit, entity.was_hurt is true and entity.last_damage_type returns
// the frozen damage-type id scalar (the player_attack id from the data/tag table).
func TestWasHurtHandleAttr(t *testing.T) {
	loop, _ := newN2Loop(t)

	mob := newDamageRegionMob(loop, 1, 8.5, 64, 8.0, 20.0) // region 0
	owner := loop.regionForColumn(columnOf(mob.x, mob.z))

	// Apply a player-attack hit directly (the handle attr reads lastDamageSource + the i-frame state).
	src := damageSourcePlayerAttack(42)
	loop.withRegion(owner, func() { loop.applyDamageEntity(mob, src, 6.0) })

	h := newEntityHandleInRegion(loop, owner, mob.id, capAll)

	wasHurt, err := h.Attr("was_hurt")
	if err != nil {
		t.Fatalf("was_hurt attr error: %v", err)
	}
	if wasHurt.Truth() != true {
		t.Fatalf("was_hurt = %v, want true after a fresh hit", wasHurt)
	}

	dt, err := h.Attr("last_damage_type")
	if err != nil {
		t.Fatalf("last_damage_type attr error: %v", err)
	}
	wantID := int64(tag.DamageTypeIDs["minecraft:player_attack"])
	gotInt, ok := dt.(starlark.Int)
	if !ok {
		t.Fatalf("last_damage_type returned %s, want a starlark.Int", dt.Type())
	}
	got, ok := gotInt.Int64()
	if !ok || got != wantID {
		t.Fatalf("last_damage_type = %s, want %d (minecraft:player_attack id)", gotInt, wantID)
	}
}
