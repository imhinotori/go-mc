package server

import (
	"testing"

	"go.starlark.net/starlark"
)

// model_physics_test.go covers MODEL-M6 (spec G.4): pose-driven REAL server-side entity dimensions.
// A modeled mob whose ACTIVE nav-state declares state_dims=(w,h) has its ACTUAL collision/hitbox AABB
// resized via refreshDimensions (not merely the cosmetic Display transform) -- the native-advantage
// payoff a Bukkit client-illusion plugin structurally cannot do. The tests assert: (1) entering an
// override state shrinks the real dims + AABB; (2) leaving it restores the default (adult) box; (3) a
// model WITHOUT state_dims keeps its default box (no change, byte-identical to pre-M6); (4) a modelless
// mob is entirely unaffected (the pig-oracle guarantee); plus the declare_model state_dims validation.
//
// The applier is driven on a bare &TickLoop{}: a modelInstance with zero bones makes tickModelRig a
// no-op, so no region/store fixture is needed (mirrors model_state_test.go).

func mkDimsModel(stateDims map[modelState][2]float32, states map[modelState]string, clips ...animClip) *modelInstance {
	decl := &modelDecl{name: "dims_rig", animations: clips, states: states, stateDims: stateDims}
	return &modelInstance{decl: decl}
}

func mkModeledMob() *Entity {
	e := &Entity{ai: &mobAI{}}
	e.adultWidth, e.adultHeight = 0.9, 0.9
	e.width, e.height = 0.9, 0.9
	return e
}

func TestPoseDimensionsShrinkOnStateChange(t *testing.T) {
	tl := &TickLoop{}
	e := mkModeledMob()
	e.model = mkDimsModel(
		map[modelState][2]float32{modelStateAttack: {0.9, 0.5}},
		map[modelState]string{modelStateIdle: "idle", modelStateAttack: "attack"},
		mkClip("idle", 4), mkClip("attack", 4),
	)

	tl.tickModelPoseDimensions(e)
	if e.width != 0.9 || e.height != 0.9 {
		t.Fatalf("idle: dims = %vx%v, want 0.9x0.9 (default, no override)", e.width, e.height)
	}
	h0 := e.AABB().Upper[1] - e.AABB().Lower[1]

	e.ai.attackTargetID = 42
	tl.tickModelPoseDimensions(e)
	if e.height != 0.5 {
		t.Fatalf("attack: height = %v, want 0.5 (real dimensions shrank)", e.height)
	}
	if e.width != float64(float32(0.9)) {
		t.Fatalf("attack: width = %v, want float32(0.9)", e.width)
	}
	h1 := e.AABB().Upper[1] - e.AABB().Lower[1]
	if !(h1 < h0) {
		t.Fatalf("attack AABB height %v not smaller than idle %v", h1, h0)
	}
	if h1 != 0.5 {
		t.Fatalf("attack AABB height = %v, want 0.5", h1)
	}
}

func TestPoseDimensionsRestoreOnLeave(t *testing.T) {
	tl := &TickLoop{}
	e := mkModeledMob()
	e.model = mkDimsModel(
		map[modelState][2]float32{modelStateAttack: {0.9, 0.5}},
		map[modelState]string{modelStateIdle: "idle", modelStateAttack: "attack"},
		mkClip("idle", 4), mkClip("attack", 4),
	)

	e.ai.attackTargetID = 42
	tl.tickModelPoseDimensions(e)
	if e.height != 0.5 {
		t.Fatalf("attack: height = %v, want 0.5", e.height)
	}
	e.ai.attackTargetID = 0
	tl.tickModelPoseDimensions(e)
	if e.width != 0.9 || e.height != 0.9 {
		t.Fatalf("post-leave: dims = %vx%v, want 0.9x0.9 (default restored)", e.width, e.height)
	}
}

func TestPoseDimensionsNoOverrideKeepsDefault(t *testing.T) {
	tl := &TickLoop{}
	e := mkModeledMob()
	e.model = mkDimsModel(nil,
		map[modelState]string{modelStateIdle: "idle", modelStateAttack: "attack"},
		mkClip("idle", 4), mkClip("attack", 4),
	)

	tl.tickModelPoseDimensions(e)
	e.ai.attackTargetID = 42
	tl.tickModelPoseDimensions(e)
	if e.width != 0.9 || e.height != 0.9 {
		t.Fatalf("no state_dims: dims = %vx%v, want unchanged 0.9x0.9", e.width, e.height)
	}
	if _, _, ok := modelPoseDimensions(e); ok {
		t.Fatalf("no state_dims: modelPoseDimensions ok=true, want false")
	}
}

func TestPoseDimensionsModellessUnaffected(t *testing.T) {
	tl := &TickLoop{}
	e := mkModeledMob()

	tl.tickModelPoseDimensions(e)
	if e.width != 0.9 || e.height != 0.9 {
		t.Fatalf("modelless: dims = %vx%v, want unchanged 0.9x0.9", e.width, e.height)
	}
	if _, _, ok := modelPoseDimensions(e); ok {
		t.Fatalf("modelless: modelPoseDimensions ok=true, want false")
	}
	e.refreshDimensions()
	if e.width != 0.9 || e.height != 0.9 {
		t.Fatalf("modelless refreshDimensions: dims = %vx%v, want 0.9x0.9", e.width, e.height)
	}
}

func TestPoseDimensionsOverrideBeatsBaby(t *testing.T) {
	tl := &TickLoop{}
	e := mkModeledMob()
	e.breedAge = -1
	e.model = mkDimsModel(
		map[modelState][2]float32{modelStateAttack: {0.9, 0.5}},
		map[modelState]string{modelStateAttack: "attack"},
		mkClip("attack", 4),
	)
	e.ai.attackTargetID = 42
	tl.tickModelPoseDimensions(e)
	if e.height != 0.5 || e.width != float64(float32(0.9)) {
		t.Fatalf("baby+override: dims = %vx%v, want float32(0.9)x0.5 (override beats baby scale)", e.width, e.height)
	}
	e.ai.attackTargetID = 0
	tl.tickModelPoseDimensions(e)
	wantBaby := 0.9 * babyDimensionScale
	if e.width != wantBaby || e.height != wantBaby {
		t.Fatalf("baby no-override: dims = %vx%v, want %vx%v (baby scale)", e.width, e.height, wantBaby, wantBaby)
	}
}

func TestBuildModelStateDimsValidation(t *testing.T) {
	if m, err := buildModelStateDims(nil); err != nil || m != nil {
		t.Fatalf("nil dict: got (%v, %v), want (nil, nil)", m, err)
	}

	bad := starlark.NewDict(1)
	_ = bad.SetKey(starlark.String("crouch"), starlark.Tuple{starlark.Float(0.9), starlark.Float(0.5)})
	if _, err := buildModelStateDims(bad); err == nil {
		t.Fatalf("unknown state key: want error, got nil")
	}

	badTuple := starlark.NewDict(1)
	_ = badTuple.SetKey(starlark.String("attack"), starlark.Float(0.5))
	if _, err := buildModelStateDims(badTuple); err == nil {
		t.Fatalf("malformed dims tuple: want error, got nil")
	}

	negTuple := starlark.NewDict(1)
	_ = negTuple.SetKey(starlark.String("attack"), starlark.Tuple{starlark.Float(0.9), starlark.Float(0)})
	if _, err := buildModelStateDims(negTuple); err == nil {
		t.Fatalf("non-positive dims: want error, got nil")
	}

	good := starlark.NewDict(2)
	_ = good.SetKey(starlark.String("attack"), starlark.Tuple{starlark.Float(0.9), starlark.Float(0.5)})
	_ = good.SetKey(starlark.String("idle"), starlark.Tuple{starlark.Float(1.2), starlark.Float(1.8)})
	m, err := buildModelStateDims(good)
	if err != nil {
		t.Fatalf("well-formed state_dims: unexpected error %v", err)
	}
	if got := m[modelStateAttack]; got != [2]float32{0.9, 0.5} {
		t.Fatalf("attack dims = %v, want [0.9 0.5]", got)
	}
	if got := m[modelStateIdle]; got != [2]float32{1.2, 1.8} {
		t.Fatalf("idle dims = %v, want [1.2 1.8]", got)
	}
}
