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
