package server

// plugin_mob_decl_test.go — Phase 35-01b: the goal-kind SEAM tests. 35-01 ported the combat goals as
// Go-NATIVE structs (newMeleeAttackGoal/newNearestAttackableTargetGoal/...) but added NO way for a
// .star declaration to reference them; the hostiles (zombie/skeleton/spider) therefore could not
// acquire a target or deal melee damage through a declaration. This plan added the `kind=` param on
// goal() + the `switch gd.nativeKind` route in buildAIFromDecl. These tests pin the SEAM:
//
//   - a goal(kind=...) with NO callbacks captures a goalDecl carrying nativeKind (no Starlark body);
//   - a goal(kind=...) that ALSO supplies a callback (or requires_update_every_tick) is a loud load error;
//   - buildAIFromDecl routes a TARGET kind-goal into the targetSelector and a non-TARGET one into goals,
//     instantiating the matching Go-native goal (the verbatim 35-01 port);
//   - an unknown kind fails LOUDLY (a panic, surfacing the disarmed-hostile bug — never a silent no-op).
//
// The pig oracle is untouched — a pig declares NO kind-goal, so the nativeKind branch is never taken for
// it (TestPluginPigEqualsGoNativePig stays byte-identical, pinned in plugin_pig_test.go).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// --- the goal() kind capture + exclusivity (load-time) -----------------------------------------

// TestGoalKindCaptures: goal(priority, flags, kind="melee_attack") with NO callback captures a
// goalDecl carrying nativeKind="melee_attack" and the declared priority + flags, and NO Starlark
// callable (a kind-goal carries no body).
func TestGoalKindCaptures(t *testing.T) {
	star := `
declare_mob(
    name = "x",
    base_type = "zombie",
    goals = [goal(priority = 3, flags = ["MOVE"], kind = "melee_attack")],
)
`
	r := loadMobRegistry(t, writeMobPlugin(t, star))
	decl, ok := r.byName["x"]
	if !ok {
		t.Fatal("no mobDecl captured under \"x\"")
	}
	if len(decl.goals) != 1 {
		t.Fatalf("captured %d goals, want 1", len(decl.goals))
	}
	g := decl.goals[0]
	if g.nativeKind != "melee_attack" {
		t.Fatalf("goal nativeKind = %q, want \"melee_attack\"", g.nativeKind)
	}
	if g.priority != 3 {
		t.Fatalf("goal priority = %d, want 3", g.priority)
	}
	if g.flags != flagMove {
		t.Fatalf("goal flags = %b, want flagMove (%b)", g.flags, flagMove)
	}
	// A kind-goal carries NO Starlark callable — the Go-native goal owns the behavior.
	if g.tickFn != nil || g.canUseFn != nil || g.startFn != nil || g.stopFn != nil || g.continueFn != nil {
		t.Fatal("a kind-goal must capture NO Starlark callables (the Go-native goal owns the behavior)")
	}
}

// TestGoalKindRejectsCallback: a goal that supplies BOTH kind and a callback is a loud load error (the
// callback would be dead — the Go-native goal owns the behavior).
func TestGoalKindRejectsCallback(t *testing.T) {
	star := `
def t(e, w, n):
    pass
declare_mob(
    name = "x",
    base_type = "zombie",
    goals = [goal(priority = 3, flags = ["MOVE"], kind = "melee_attack", tick = t)],
)
`
	err := loadMobRegistryExpectErr(t, star)
	if !contains(err.Error(), "must NOT also supply") {
		t.Fatalf("error %q does not reject the kind+callback contradiction", err.Error())
	}
}

// TestGoalKindRejectsUpdateEveryTick: a kind-goal that sets requires_update_every_tick is a loud load
// error (the Go-native goal owns its own update cadence).
func TestGoalKindRejectsUpdateEveryTick(t *testing.T) {
	star := `
declare_mob(
    name = "x",
    base_type = "spider",
    goals = [goal(priority = 1, flags = ["JUMP"], kind = "float", requires_update_every_tick = True)],
)
`
	err := loadMobRegistryExpectErr(t, star)
	if !contains(err.Error(), "requires_update_every_tick") {
		t.Fatalf("error %q does not reject a kind-goal carrying requires_update_every_tick", err.Error())
	}
}

// --- buildAIFromDecl routing (the seam's spawn-time half) --------------------------------------

// TestNativeGoalRoutesToTargetSelector: a TARGET kind-goal (nearest_attackable_target / hurt_by_target)
// is instantiated as the Go-native goal and registered in the targetSelector (NOT goals) — exactly as
// Mob.registerGoals routes HurtByTargetGoal/NearestAttackableTargetGoal into mob.targetSelector. A
// non-TARGET kind-goal (melee_attack) routes into goals.
func TestNativeGoalRoutesToTargetSelector(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	decl := &mobDecl{
		name:     "x",
		baseType: entity.Zombie,
		goals: []goalDecl{
			{priority: 1, flags: flagTarget, nativeKind: "hurt_by_target"},
			{priority: 2, flags: flagTarget, nativeKind: "nearest_attackable_target"},
			{priority: 3, flags: flagMove, nativeKind: "melee_attack"},
		},
	}
	m := buildAIFromDecl(loop, decl)

	// The two TARGET kind-goals route into the targetSelector; the melee goal into goals.
	if got := len(m.targetSelector.goals); got != 2 {
		t.Fatalf("targetSelector has %d goals, want 2 (hurt_by_target@1 + nearest_attackable_target@2)", got)
	}
	if got := len(m.goals.goals); got != 1 {
		t.Fatalf("goals has %d goals, want 1 (melee_attack@3)", got)
	}

	// The targetSelector goals are the Go-native target ports (NOT starlarkGoals), carrying the TARGET
	// flag at the declared priorities.
	sawHurt, sawNearest := false, false
	for _, wg := range m.targetSelector.goals {
		if wg.g.flags() != flagTarget {
			t.Fatalf("targetSelector goal flags = %b, want flagTarget", wg.g.flags())
		}
		switch wg.g.(type) {
		case *hurtByTargetGoal:
			sawHurt = true
			if wg.priority != 1 {
				t.Fatalf("hurt_by_target priority = %d, want 1", wg.priority)
			}
		case *nearestAttackableTargetGoal:
			sawNearest = true
			if wg.priority != 2 {
				t.Fatalf("nearest_attackable_target priority = %d, want 2", wg.priority)
			}
		default:
			t.Fatalf("targetSelector holds an unexpected goal type %T (want the Go-native target ports)", wg.g)
		}
	}
	if !sawHurt || !sawNearest {
		t.Fatalf("targetSelector missing a Go-native target goal (hurt=%v nearest=%v)", sawHurt, sawNearest)
	}

	// The goals selector holds the Go-native meleeAttackGoal (the MOVE flag) at priority 3.
	mg := m.goals.goals[0]
	if _, ok := mg.g.(*meleeAttackGoal); !ok {
		t.Fatalf("goals holds %T, want *meleeAttackGoal (the melee_attack kind)", mg.g)
	}
	if mg.g.flags() != flagMove {
		t.Fatalf("melee goal flags = %b, want flagMove", mg.g.flags())
	}
}

// TestNativeGoalLeapAndSpiderAndFloat: the spider's kind-goals instantiate the right Go-native types
// with the right flags — leap_at_target → *leapAtTargetGoal {JUMP,MOVE}, spider_attack → *meleeAttackGoal
// (the daylight-gated subclass) {MOVE}, float → *floatGoal {JUMP}.
func TestNativeGoalLeapAndSpiderAndFloat(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	decl := &mobDecl{
		name:     "x",
		baseType: entity.Spider,
		goals: []goalDecl{
			{priority: 1, flags: flagJump, nativeKind: "float"},
			{priority: 3, flags: flagJump | flagMove, nativeKind: "leap_at_target"},
			{priority: 4, flags: flagMove, nativeKind: "spider_attack"},
		},
	}
	m := buildAIFromDecl(loop, decl)
	if got := len(m.goals.goals); got != 3 {
		t.Fatalf("goals has %d, want 3 (float@1 + leap@3 + spider_attack@4)", got)
	}

	byPrio := map[int]Goal{}
	for _, wg := range m.goals.goals {
		byPrio[wg.priority] = wg.g
	}
	if _, ok := byPrio[1].(*floatGoal); !ok {
		t.Fatalf("priority 1 is %T, want *floatGoal", byPrio[1])
	}
	leap, ok := byPrio[3].(*leapAtTargetGoal)
	if !ok {
		t.Fatalf("priority 3 is %T, want *leapAtTargetGoal", byPrio[3])
	}
	if leap.yd != spiderLeapYd {
		t.Fatalf("leap yd = %v, want %v (Spider.registerGoals LeapAtTargetGoal(this, 0.4))", leap.yd, spiderLeapYd)
	}
	spider, ok := byPrio[4].(*meleeAttackGoal)
	if !ok {
		t.Fatalf("priority 4 is %T, want *meleeAttackGoal (the spider_attack subclass)", byPrio[4])
	}
	if !spider.daylightGated {
		t.Fatal("spider_attack must be the daylight-gated melee (newSpiderAttackGoal sets daylightGated)")
	}
}

// TestNativeGoalUnknownKindPanics: an unknown kind fails LOUDLY (a panic) — never a silent no-op that
// would ship a hostile with a MISSING combat goal (it would never hunt/attack).
func TestNativeGoalUnknownKindPanics(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	decl := &mobDecl{
		name:     "x",
		baseType: entity.Zombie,
		goals:    []goalDecl{{priority: 1, flags: flagMove, nativeKind: "bogus_kind"}},
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("buildAIFromDecl must PANIC on an unknown goal kind (a disarmed hostile is never silent)")
		}
		if msg, ok := r.(string); !ok || !contains(msg, "unknown goal kind") {
			t.Fatalf("panic value = %v, want a message mentioning the unknown goal kind", r)
		}
	}()
	buildAIFromDecl(loop, decl)
}

// TestNativeGoalFlagMismatchPanics: a kind-goal whose declared flags disagree with the Go-native goal's
// own flags() is a loud declaration bug (it would route into the wrong selector / claim the wrong flag).
func TestNativeGoalFlagMismatchPanics(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	decl := &mobDecl{
		name:     "x",
		baseType: entity.Zombie,
		// melee_attack is a {MOVE} goal; declaring it {TARGET} would mis-route it into the targetSelector.
		goals: []goalDecl{{priority: 1, flags: flagTarget, nativeKind: "melee_attack"}},
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("buildAIFromDecl must PANIC when the declared flags disagree with the Go-native goal's flags")
		}
		if msg, ok := r.(string); !ok || !contains(msg, "disagree") {
			t.Fatalf("panic value = %v, want a flag-disagreement message", r)
		}
	}()
	buildAIFromDecl(loop, decl)
}

// TestVyRejectsNonLeapKind: vy= is only valid with kind="leap_at_target" (the LeapAtTargetGoal
// ctor's yd arg). A vy= on any other kind is a loud load error (a silently-dead kwarg would ship
// a mob whose leap height was ignored).
func TestVyRejectsNonLeapKind(t *testing.T) {
	star := `
declare_mob(
    name = "x",
    base_type = "zombie",
    goals = [goal(priority = 1, flags = ["MOVE"], kind = "melee_attack", vy = 0.3)],
)
`
	err := loadMobRegistryExpectErr(t, star)
	if !contains(err.Error(), "vy=") {
		t.Fatalf("error %q does not reject vy= on a non-leap kind", err.Error())
	}
}

// TestOcelotAttackKindRoutesToOcelotAttackGoal: kind="ocelot_attack" at priority 8 with
// {MOVE, LOOK} flags instantiates the Go-native ocelotAttackGoal (the 26.2 standalone port). The
// declared flags MUST match the native goal's own flags() (MOVE|LOOK) or buildAIFromDecl panics.
func TestOcelotAttackKindRoutesToOcelotAttackGoal(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	decl := &mobDecl{
		name:     "x",
		baseType: entity.Ocelot,
		goals:    []goalDecl{{priority: 8, flags: flagMove | flagLook, nativeKind: "ocelot_attack"}},
	}
	m := buildAIFromDecl(loop, decl)
	if got := len(m.goals.goals); got != 1 {
		t.Fatalf("goals has %d, want 1 (ocelot_attack@8)", got)
	}
	goal, ok := m.goals.goals[0].g.(*ocelotAttackGoal)
	if !ok {
		t.Fatalf("priority 8 is %T, want *ocelotAttackGoal", m.goals.goals[0].g)
	}
	if goal.flags() != flagMove|flagLook {
		t.Fatalf("ocelot_attack flags = %b, want %b (MOVE|LOOK, the 26.2 OcelotAttackGoal ctor setFlags)", goal.flags(), flagMove|flagLook)
	}
}
