package server

import "testing"

// model_state_test.go covers MODEL-M4: the nav-state animation state machine. It asserts (1) the AI
// state -> clip mapping (idle when stationary, walk when navigating, run when chasing, attack when
// targeting, death when dead), (2) the priority rule (an explicit play_animation clip suppresses the
// selector until it ends), and (3) the H.2.4 post-end handoff (control returns to the nav-selected
// default). The animator is driven on a bare &TickLoop{}: an instance with zero bones + clips with
// zero channels make tickModelRig / pushModelFrame no-ops, so no region/store fixture is needed.

func mkClip(name string, length float32) animClip {
	return animClip{name: name, length: length, loop: true}
}

func mkStateModel(states map[modelState]string, clips ...animClip) *modelInstance {
	decl := &modelDecl{name: "test_rig", animations: clips, states: states}
	return &modelInstance{decl: decl}
}

func currentClipName(m *modelInstance) string {
	if m.animator == nil || m.animator.clip == nil {
		return ""
	}
	return m.animator.clip.name
}

// TestModelStateSelectorMapsAIState: the selector picks idle/walk/run/attack/death per the mob AI.
func TestModelStateSelectorMapsAIState(t *testing.T) {
	states := map[modelState]string{
		modelStateIdle:   "idle",
		modelStateWalk:   "walk",
		modelStateRun:    "run",
		modelStateAttack: "attack",
		modelStateDeath:  "death",
	}
	tl := &TickLoop{}

	e := &Entity{ai: &mobAI{}}
	e.model = mkStateModel(states, mkClip("idle", 4), mkClip("walk", 4), mkClip("run", 4), mkClip("attack", 4), mkClip("death", 4))
	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "idle" {
		t.Fatalf("stationary mob: clip = %q, want idle", got)
	}

	e.ai.hasTarget = true
	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "walk" {
		t.Fatalf("navigating mob: clip = %q, want walk", got)
	}

	e.ai.wantSpeed = 0.35
	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "run" {
		t.Fatalf("chasing mob: clip = %q, want run", got)
	}

	e.ai.attackTargetID = 42
	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "attack" {
		t.Fatalf("targeting mob: clip = %q, want attack", got)
	}

	e.dead = true
	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "death" {
		t.Fatalf("dead mob: clip = %q, want death", got)
	}
}

// TestModelStateMovingWithoutNav: a mob with velocity but no nav want reads as walk.
func TestModelStateMovingWithoutNav(t *testing.T) {
	states := map[modelState]string{modelStateIdle: "idle", modelStateWalk: "walk"}
	tl := &TickLoop{}
	e := &Entity{ai: &mobAI{}}
	e.vx, e.vz = 0.2, 0.0
	e.model = mkStateModel(states, mkClip("idle", 4), mkClip("walk", 4))
	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "walk" {
		t.Fatalf("moving mob: clip = %q, want walk", got)
	}
}

// TestModelStateNamingConvention: with NO states= dict, clips named idle/walk auto-bind by convention.
func TestModelStateNamingConvention(t *testing.T) {
	clips := []animClip{mkClip("idle", 4), mkClip("walk", 4)}
	states, err := buildModelStates(nil, clips)
	if err != nil {
		t.Fatalf("buildModelStates: %v", err)
	}
	if states[modelStateIdle] != "idle" || states[modelStateWalk] != "walk" {
		t.Fatalf("naming-convention bind = %v, want idle->idle walk->walk", states)
	}
	if _, ok := states[modelStateRun]; ok {
		t.Fatalf("run should be unbound (no run clip), got %q", states[modelStateRun])
	}
}

// TestModelStateNoStateMachine: a model with no state binding never installs an animator/clip.
func TestModelStateNoStateMachine(t *testing.T) {
	tl := &TickLoop{}
	e := &Entity{ai: &mobAI{}}
	e.model = &modelInstance{decl: &modelDecl{name: "static", states: nil}}
	tl.tickModelAnimator(e)
	if e.model.animator != nil {
		t.Fatalf("static rig should never create an animator, got %+v", e.model.animator)
	}
}

// TestPlayAnimationOverridesStateMachine: an explicit play_animation(once) clip suppresses the
// nav-state selector while it runs, then hands control back to the default at its end.
func TestPlayAnimationOverridesStateMachine(t *testing.T) {
	states := map[modelState]string{modelStateIdle: "idle", modelStateWalk: "walk"}
	tl := &TickLoop{}
	e := &Entity{ai: &mobAI{hasTarget: true}}
	attack := animClip{name: "attack", length: 2, loop: false}
	e.model = mkStateModel(states, mkClip("idle", 4), mkClip("walk", 4), attack)

	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "walk" {
		t.Fatalf("pre-override: clip = %q, want walk", got)
	}

	e.model.animator.pending = &attack
	e.model.animator.pendingMode = animModeOnce
	e.model.animator.pendingExplicit = true

	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "attack" {
		t.Fatalf("override tick0: clip = %q, want attack", got)
	}
	if !e.model.animator.explicit {
		t.Fatalf("override tick0: explicit flag should be set")
	}
	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "attack" {
		t.Fatalf("override tick1: clip = %q, want attack (selector must not override)", got)
	}
	tl.tickModelAnimator(e)
	if e.model.animator.explicit {
		t.Fatalf("post-end: explicit flag should be cleared (H.2.4 handoff)")
	}

	tl.tickModelAnimator(e)
	if got := currentClipName(e.model); got != "walk" {
		t.Fatalf("post-handoff: clip = %q, want walk (selector resumed)", got)
	}
}

// TestPlayAnimationLoopHoldsUntilReplaced: a loop-mode explicit clip stays explicit (no end) and
// keeps suppressing the selector until another play_animation replaces it.
func TestPlayAnimationLoopHoldsUntilReplaced(t *testing.T) {
	states := map[modelState]string{modelStateIdle: "idle", modelStateWalk: "walk"}
	tl := &TickLoop{}
	e := &Entity{ai: &mobAI{hasTarget: true}}
	dance := animClip{name: "dance", length: 2, loop: true}
	e.model = mkStateModel(states, mkClip("idle", 4), mkClip("walk", 4), dance)

	e.model.animator = &modelAnimator{pending: &dance, pendingMode: animModeLoop, pendingExplicit: true}
	for i := 0; i < 6; i++ {
		tl.tickModelAnimator(e)
		if got := currentClipName(e.model); got != "dance" {
			t.Fatalf("loop tick %d: clip = %q, want dance (loop explicit never yields to selector)", i, got)
		}
		if !e.model.animator.explicit {
			t.Fatalf("loop tick %d: explicit flag should stay set for a loop clip", i)
		}
	}
}
