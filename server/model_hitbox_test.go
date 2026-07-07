package server

import (
	"math"
	"testing"
)

// model_hitbox_test.go covers MODEL-M5 (G.1 + H.2.3): native per-bone hitboxes and the hit_bone
// condition. It drives updateBoneAABBs / resolveHitBone / resolveMeleeHitBone directly on struct-literal
// Entities (no region/store fixture needed) and skillConditionHolds on a bare &TickLoop{}. The oracle
// invariant is asserted explicitly: a modelless mob (e.model == nil) resolves boneName "" always.
//
// The rig: a "head" bone pivoted at +2 above the base and a "body" bone at +0.9, both with explicit
// hitbox=(0.5, 0.5) so the boxes are well-separated and a segment can strike one but not the other.

func mkHitboxModel() *modelInstance {
	head := boneDecl{name: "head", pivotX: 0, pivotY: 2.0, pivotZ: 0, hitboxW: 0.5, hitboxH: 0.5, damageMult: 2.0}
	body := boneDecl{name: "body", pivotX: 0, pivotY: 0.9, pivotZ: 0, hitboxW: 0.5, hitboxH: 0.5}
	decl := &modelDecl{name: "test_rig", bones: []boneDecl{head, body}}
	inst := &modelInstance{decl: decl}
	inst.bones = []boneRuntime{
		{decl: &decl.bones[0]},
		{decl: &decl.bones[1]},
	}
	return inst
}

// TestModelHitboxUpdateAABBs: updateBoneAABBs centers each bone's box at base + pivot, sized by hitbox.
func TestModelHitboxUpdateAABBs(t *testing.T) {
	e := &Entity{x: 10, y: 64, z: 20}
	e.model = mkHitboxModel()
	updateBoneAABBs(e)

	head := &e.model.bones[0]
	// head center = (10, 66, 20), half-extent 0.25.
	if head.aabbMinX != 9.75 || head.aabbMaxX != 10.25 {
		t.Fatalf("head X box = [%g,%g], want [9.75,10.25]", head.aabbMinX, head.aabbMaxX)
	}
	if head.aabbMinY != 65.75 || head.aabbMaxY != 66.25 {
		t.Fatalf("head Y box = [%g,%g], want [65.75,66.25]", head.aabbMinY, head.aabbMaxY)
	}
	body := &e.model.bones[1]
	// body center = (10, 64.9, 20). pivotY is float32 0.9, so compare with a tolerance (the float32 ->
	// float64 widen is not bit-exact).
	if !approxEqF64(body.aabbMinY, 64.65) || !approxEqF64(body.aabbMaxY, 65.15) {
		t.Fatalf("body Y box = [%g,%g], want [64.65,65.15]", body.aabbMinY, body.aabbMaxY)
	}
}

// approxEqF64 compares two float64s within a small tolerance (float32 pivots do not widen bit-exactly;
// the package's approxEq is float32-typed and used elsewhere).
func approxEqF64(a, b float64) bool { return math.Abs(a-b) < 1e-5 }

// TestModelHitboxResolveHead: a segment through the head box resolves boneName "head" + its damage_mult.
func TestModelHitboxResolveHead(t *testing.T) {
	tl := &TickLoop{}
	e := &Entity{x: 0, y: 0, z: 0}
	e.model = mkHitboxModel()
	updateBoneAABBs(e)
	// A horizontal segment at y=2 along +x from x=-5 passes through the head box (centered (0,2,0)) and
	// misses the body box (centered (0,0.9,0)).
	name, mult := tl.resolveHitBone(e, -5, 2.0, 0, 5, 2.0, 0)
	if name != "head" {
		t.Fatalf("segment through head: boneName = %q, want head", name)
	}
	if mult != 2.0 {
		t.Fatalf("head damage_mult = %g, want 2.0", mult)
	}
}

// TestModelHitboxResolveBody: a segment through the body box resolves boneName "body" (mult defaults 1.0).
func TestModelHitboxResolveBody(t *testing.T) {
	tl := &TickLoop{}
	e := &Entity{x: 0, y: 0, z: 0}
	e.model = mkHitboxModel()
	updateBoneAABBs(e)
	name, mult := tl.resolveHitBone(e, -5, 0.9, 0, 5, 0.9, 0)
	if name != "body" {
		t.Fatalf("segment through body: boneName = %q, want body", name)
	}
	if mult != 1.0 {
		t.Fatalf("body damage_mult = %g, want 1.0 (unset)", mult)
	}
}

// TestModelHitboxResolveMissAll: a segment missing every bone box resolves boneName "" (entity-level).
func TestModelHitboxResolveMissAll(t *testing.T) {
	tl := &TickLoop{}
	e := &Entity{x: 0, y: 0, z: 0}
	e.model = mkHitboxModel()
	updateBoneAABBs(e)
	// A segment at y=5 (well above both boxes) misses everything.
	name, mult := tl.resolveHitBone(e, -5, 5.0, 0, 5, 5.0, 0)
	if name != "" {
		t.Fatalf("segment missing all bones: boneName = %q, want \"\"", name)
	}
	if mult != 1.0 {
		t.Fatalf("miss-all mult = %g, want 1.0", mult)
	}
}

// TestModelHitboxNearestBoneWins: a segment crossing BOTH boxes resolves the nearer one (smallest entry t).
func TestModelHitboxNearestBoneWins(t *testing.T) {
	tl := &TickLoop{}
	e := &Entity{x: 0, y: 0, z: 0}
	e.model = mkHitboxModel()
	updateBoneAABBs(e)
	// A vertical segment from above (y=5) down through head (y~2) THEN body (y~0.9): the head is entered
	// first (smaller t), so it wins.
	name, _ := tl.resolveHitBone(e, 0, 5.0, 0, 0, -1.0, 0)
	if name != "head" {
		t.Fatalf("top-down segment: nearest bone = %q, want head (entered first)", name)
	}
}

// TestModelHitboxModellessMob: a mob with no model resolves boneName "" for ANY segment (the pig oracle).
func TestModelHitboxModellessMob(t *testing.T) {
	tl := &TickLoop{}
	e := &Entity{x: 0, y: 0, z: 0} // e.model == nil
	name, mult := tl.resolveHitBone(e, -5, 0, 0, 5, 0, 0)
	if name != "" {
		t.Fatalf("modelless mob: boneName = %q, want \"\"", name)
	}
	if mult != 1.0 {
		t.Fatalf("modelless mob: mult = %g, want 1.0", mult)
	}
}

// TestModelHitboxMeleeLookRay: resolveMeleeHitBone casts the attacker's eye-line and resolves the bone.
func TestModelHitboxMeleeLookRay(t *testing.T) {
	tl := &TickLoop{}
	mob := &Entity{x: 0, y: 0, z: 2}
	mob.model = mkHitboxModel()
	updateBoneAABBs(mob)

	// Attacker standing at z=0 looking straight along +z (yaw 0 -> dir +z) at the head's height. Eye at
	// y = pitch-adjusted; to look AT the head (y=2, base y=0), the eye is at 0+1.62=1.62 so pitch up
	// slightly. Compute the pitch that aims the eye ray at the head center (0,2,2) from (0,1.62,0).
	p := &tickPlayer{x: 0, y: 0, z: 0}
	// direction to head center: dy = 2-1.62 = 0.38 over dz = 2 -> pitch = -atan2(dy, dz) in degrees
	// (Minecraft pitch is negative when looking up). yaw 0 aims +z.
	dz := 2.0
	dy := 2.0 - playerStandingEyeHeight
	p.yaw = 0
	p.pitch = float32(-math.Atan2(dy, dz) * 180.0 / math.Pi)

	name, mult := tl.resolveMeleeHitBone(p, mob)
	if name != "head" {
		t.Fatalf("melee look ray at head: boneName = %q, want head", name)
	}
	if mult != 2.0 {
		t.Fatalf("melee head mult = %g, want 2.0", mult)
	}

	// A modelless mob: melee resolve is always "".
	plain := &Entity{x: 0, y: 0, z: 2}
	if n, _ := tl.resolveMeleeHitBone(p, plain); n != "" {
		t.Fatalf("melee vs modelless mob: boneName = %q, want \"\"", n)
	}
}

// TestHitBoneConditionPassesOnlyOnNamedBone: condition("hit_bone", value="head") holds iff the fire's
// ctx.boneName == "head"; a body hit or an entity-level ("") hit fails closed.
func TestHitBoneConditionPassesOnlyOnNamedBone(t *testing.T) {
	tl := &TickLoop{}
	e := &Entity{}
	cond := &conditionDecl{kind: "hit_bone", strValue: "head"}

	if !tl.skillConditionHolds(e, cond, skillTriggerCtx{boneName: "head"}) {
		t.Fatalf("hit_bone=head with ctx head: want hold")
	}
	if tl.skillConditionHolds(e, cond, skillTriggerCtx{boneName: "body"}) {
		t.Fatalf("hit_bone=head with ctx body: want fail")
	}
	if tl.skillConditionHolds(e, cond, skillTriggerCtx{boneName: ""}) {
		t.Fatalf("hit_bone=head with ctx \"\" (entity-level hit): want fail closed")
	}
}
