package server

// plugin_animator_test.go — MODEL-M3 (the Go keyframe animator + animation triggers + play_animation):
// covers the clip data model capture + load validation, linear per-bone interpolation with the N-tick
// client-interp push, the pending-only play_animation mechanic (no synchronous advance / no recursion),
// and the animation_frame / animation_end triggers firing on the exact tick the animator clock crosses
// the declared frame / clip end. The pig oracle (TestPluginPigEqualsGoNativePig) is untouched: a mob
// with no model has a nil animator and the tick path is byte-identical (asserted here + by that test).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level"
)

// --- clip data model capture + load validation -------------------------------------------------

// TestDeclareModelCapturesAnimation: declare_model with an animation clip captures its channels and
// keyframes as pure data (name, loop, length, per-bone channel of sorted keyframes).
func TestDeclareModelCapturesAnimation(t *testing.T) {
	star := `
declare_model(name="rig", bones=[
    bone(name="arm", item="minecraft:stone"),
], animations=[
    animation(name="swing", length=10.0, loop=True, channels=[
        channel(bone="arm", kind="position", keyframes=[
            keyframe(time=0.0, value=(0.0, 0.0, 0.0)),
            keyframe(time=10.0, value=(0.0, 2.0, 0.0)),
        ]),
        channel(bone="arm", kind="rotation", keyframes=[
            keyframe(time=0.0, value=(0.0, 0.0, 0.0)),
        ]),
    ]),
])
`
	r := loadModelRegistry(t, star, capAll)
	md, ok := r.models.byName["rig"]
	if !ok {
		t.Fatalf("no modelDecl captured under %q", "rig")
	}
	if len(md.animations) != 1 {
		t.Fatalf("captured %d clips, want 1", len(md.animations))
	}
	clip := md.animations[0]
	if clip.name != "swing" || !clip.loop || clip.length != 10.0 {
		t.Fatalf("clip = {name:%q loop:%v length:%v}, want {swing true 10}", clip.name, clip.loop, clip.length)
	}
	if len(clip.channels) != 2 {
		t.Fatalf("clip has %d channels, want 2", len(clip.channels))
	}
	pos := clip.channels[0]
	if pos.bone != "arm" || pos.kind != channelPosition {
		t.Fatalf("channel[0] = {bone:%q kind:%d}, want {arm position}", pos.bone, pos.kind)
	}
	if len(pos.keyframes) != 2 || pos.keyframes[1].time != 10.0 || pos.keyframes[1].y != 2.0 {
		t.Fatalf("position keyframes mis-captured: %+v", pos.keyframes)
	}
	if clip.channels[1].kind != channelRotation {
		t.Fatalf("channel[1].kind = %d, want rotation", clip.channels[1].kind)
	}
	if _, ok := md.clipOf("swing"); !ok {
		t.Fatalf("clipOf(swing) not found in the captured clips")
	}
}

// TestDeclareModelRejectsUnknownChannelBone: a channel naming a bone not in the rig errors at LOAD.
func TestDeclareModelRejectsUnknownChannelBone(t *testing.T) {
	star := `
declare_model(name="rig", bones=[
    bone(name="arm", item="minecraft:stone"),
], animations=[
    animation(name="swing", length=5.0, channels=[
        channel(bone="leg", kind="position", keyframes=[keyframe(time=0.0, value=(0.0,0.0,0.0))]),
    ]),
])
`
	err := loadModelRegistryExpectErr(t, star, capAll)
	if !contains(err.Error(), "not a bone") {
		t.Fatalf("error %q does not mention the unknown channel bone", err.Error())
	}
}

// TestDeclareModelRejectsNonAscendingKeyframes: keyframe times must be strictly ascending — a
// descending/equal pair errors at LOAD.
func TestDeclareModelRejectsNonAscendingKeyframes(t *testing.T) {
	star := `
declare_model(name="rig", bones=[
    bone(name="arm", item="minecraft:stone"),
], animations=[
    animation(name="swing", length=10.0, channels=[
        channel(bone="arm", kind="position", keyframes=[
            keyframe(time=5.0, value=(0.0,0.0,0.0)),
            keyframe(time=3.0, value=(0.0,1.0,0.0)),
        ]),
    ]),
])
`
	err := loadModelRegistryExpectErr(t, star, capAll)
	if !contains(err.Error(), "ascending") {
		t.Fatalf("error %q does not mention the non-ascending keyframe times", err.Error())
	}
}

// TestDeclareModelRejectsKeyframeOutOfRange: a keyframe time beyond the clip length errors at LOAD.
func TestDeclareModelRejectsKeyframeOutOfRange(t *testing.T) {
	star := `
declare_model(name="rig", bones=[
    bone(name="arm", item="minecraft:stone"),
], animations=[
    animation(name="swing", length=4.0, channels=[
        channel(bone="arm", kind="position", keyframes=[
            keyframe(time=0.0, value=(0.0,0.0,0.0)),
            keyframe(time=9.0, value=(0.0,1.0,0.0)),
        ]),
    ]),
])
`
	err := loadModelRegistryExpectErr(t, star, capAll)
	if !contains(err.Error(), "out of") {
		t.Fatalf("error %q does not mention the out-of-range keyframe", err.Error())
	}
}

// --- linear interpolation + the N-tick client-interp push --------------------------------------

// animatedLoop builds a physics loop whose registry declares a 1-bone rig with a 10-tick position clip
// (arm y: 0 -> 2) and a mob wearing it, then spawns the mob and returns the loop + the base entity.
func animatedLoop(t *testing.T) (*TickLoop, *Entity) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	star := `
declare_model(name="rig", bones=[
    bone(name="arm", item="minecraft:stone"),
], animations=[
    animation(name="swing", length=10.0, channels=[
        channel(bone="arm", kind="position", keyframes=[
            keyframe(time=0.0, value=(0.0, 0.0, 0.0)),
            keyframe(time=10.0, value=(0.0, 2.0, 0.0)),
        ]),
    ]),
])
declare_mob(name="golem", base_type="zombie", model="rig")
`
	r := loadModelRegistry(t, star, capAll)
	loop.SetMobRegistry(r)
	e := loop.spawnDeclaredMob(r.byName["golem"], 8.5, float64(floorY+1), 8.5)
	return loop, e
}

// armBone resolves the sole "arm" bone entity of a modeled mob.
func armBone(t *testing.T, loop *TickLoop, e *Entity) *Entity {
	t.Helper()
	b := loop.modelBoneEntity(e.model, "arm")
	if b == nil {
		t.Fatal("arm bone entity not found")
	}
	return b
}

// TestAnimatorInterpolatesTranslationLinearly: with a clip whose arm position lerps y 0 -> 2 over 10
// ticks, sampleChannel at the exact midpoint (t=5) yields y=1.0, and after driving the animator the
// last EVEN-tick push set the bone translation with interp_duration == N.
func TestAnimatorInterpolatesTranslationLinearly(t *testing.T) {
	loop, e := animatedLoop(t)
	e.model.animator = &modelAnimator{pending: &e.model.decl.animations[0], pendingMode: animModeOnce}

	// The SPEC requirement: linear midpoint. sampleChannel is the interpolation core.
	gotX, gotY, gotZ := sampleChannel(&e.model.decl.animations[0].channels[0], 5.0)
	if gotX != 0 || gotZ != 0 || math.Abs(float64(gotY-1.0)) > 1e-6 {
		t.Fatalf("sampleChannel at t=5 = (%v,%v,%v), want (0,1,0) — linear midpoint", gotX, gotY, gotZ)
	}

	// Drive the animator 6 ticks: apply-tick t=0, then t=1..5; the animator clock ends at 6.
	for i := 0; i < 6; i++ {
		loop.tickModelAnimator(e)
	}
	if e.model.animator.t != 6 {
		t.Fatalf("animator.t = %d after 6 ticks, want 6", e.model.animator.t)
	}
	arm := armBone(t, loop, e)
	// The last push landed on an even tick (t=4): y = 4/10*2 = 0.8, with interp_duration == N.
	if arm.dispInterpDuration != modelAnimPushInterval {
		t.Fatalf("arm dispInterpDuration = %d after a push, want %d (client interp over N ticks)", arm.dispInterpDuration, modelAnimPushInterval)
	}
	if arm.dispInterpStartDelta != 0 {
		t.Fatalf("arm dispInterpStartDelta = %d, want 0 (start now)", arm.dispInterpStartDelta)
	}
	if math.Abs(float64(arm.dispTransY-0.8)) > 1e-6 {
		t.Fatalf("arm dispTransY = %v after the t=4 push, want 0.8", arm.dispTransY)
	}
}

// TestAnimatorPushesEveryNTicks: the animator pushes a bone's transform only on ticks where t % N == 0
// (t=0,2,4,...), so the arm translation advances in N-tick steps, not every tick.
func TestAnimatorPushesEveryNTicks(t *testing.T) {
	loop, e := animatedLoop(t)
	e.model.animator = &modelAnimator{pending: &e.model.decl.animations[0], pendingMode: animModeOnce}
	arm := armBone(t, loop, e)

	loop.tickModelAnimator(e) // apply-tick t=0 -> push y=0, then t=1
	if math.Abs(float64(arm.dispTransY-0.0)) > 1e-6 {
		t.Fatalf("after apply tick arm.dispTransY = %v, want 0.0", arm.dispTransY)
	}
	loop.tickModelAnimator(e) // t=1 (odd) -> NO push, then t=2
	if math.Abs(float64(arm.dispTransY-0.0)) > 1e-6 {
		t.Fatalf("after odd tick arm.dispTransY = %v, want unchanged 0.0 (no push on t=1)", arm.dispTransY)
	}
	loop.tickModelAnimator(e) // t=2 (even) -> push y = 2/10*2 = 0.4, then t=3
	if math.Abs(float64(arm.dispTransY-0.4)) > 1e-6 {
		t.Fatalf("after t=2 push arm.dispTransY = %v, want 0.4", arm.dispTransY)
	}
}

// --- play_animation: pending-only (no recursion) -----------------------------------------------

// TestPlayAnimationSetsPendingNotAppliedSameTick: the play_animation mechanic sets animator.pending; the
// clip is NOT applied synchronously — clip stays nil until the NEXT tickModelAnimator swaps pending in.
// This proves the H.0 reentrancy contract (no animator recursion).
func TestPlayAnimationSetsPendingNotAppliedSameTick(t *testing.T) {
	loop, e := animatedLoop(t)
	m := &mechanicDecl{kind: "play_animation", animName: "swing", animMode: animModeLoop}

	loop.applyPlayAnimation(e, m)

	if e.model.animator == nil {
		t.Fatal("play_animation did not create the animator")
	}
	if e.model.animator.pending == nil {
		t.Fatal("play_animation did not set animator.pending")
	}
	if e.model.animator.clip != nil {
		t.Fatal("play_animation applied the clip SYNCHRONOUSLY — must be pending-only (H.0 reentrancy)")
	}
	if e.model.animator.pendingMode != animModeLoop {
		t.Fatalf("pendingMode = %d, want loop", e.model.animator.pendingMode)
	}

	loop.tickModelAnimator(e) // swaps pending into clip, clears pending (deferred, non-recursive apply)
	if e.model.animator.clip == nil {
		t.Fatal("the pending clip was not applied on the next tickModelAnimator")
	}
	if e.model.animator.pending != nil {
		t.Fatal("pending was not cleared after being applied")
	}
	if e.model.animator.clip.name != "swing" {
		t.Fatalf("applied clip = %q, want swing", e.model.animator.clip.name)
	}
}

// --- animation_frame / animation_end triggers --------------------------------------------------

// triggeredLoop builds a loop whose mob declares a clip AND two skills: an animation_frame skill (frame
// 3) and an animation_end skill, both dealing damage to self so the fire is observable via a health drop
// on the exact tick. Returns the loop + the spawned base entity.
func triggeredLoop(t *testing.T) (*TickLoop, *Entity) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	star := `
declare_model(name="rig", bones=[
    bone(name="arm", item="minecraft:stone"),
], animations=[
    animation(name="swing", length=6.0, channels=[
        channel(bone="arm", kind="position", keyframes=[
            keyframe(time=0.0, value=(0.0,0.0,0.0)),
            keyframe(time=6.0, value=(0.0,1.0,0.0)),
        ]),
    ]),
])
declare_mob(name="golem", base_type="zombie",
    model="rig",
    skills=[
        skill(trigger="animation_frame", animation="swing", frame=3,
              targeter=targeter(kind="self"), mechanics=[mechanic(kind="damage", amount=1.0)]),
        skill(trigger="animation_end", animation="swing",
              targeter=targeter(kind="self"), mechanics=[mechanic(kind="damage", amount=2.0)]),
    ])
`
	r := loadModelRegistry(t, star, capAll)
	loop.SetMobRegistry(r)
	e := loop.spawnDeclaredMob(r.byName["golem"], 8.5, float64(floorY+1), 8.5)
	return loop, e
}

// TestAnimationFrameTriggerFiresOnExactTick: the animation_frame skill (frame=3) fires exactly when the
// animator clock crosses t=3 — the mob's health drops on that tick and no earlier.
func TestAnimationFrameTriggerFiresOnExactTick(t *testing.T) {
	loop, e := triggeredLoop(t)
	e.model.animator = &modelAnimator{pending: &e.model.decl.animations[0], pendingMode: animModeOnce}

	hpStart := e.health

	for i := 0; i < 3; i++ { // animator clock reaches 0,1,2 (before frame 3)
		loop.tickModelAnimator(e)
	}
	if e.health != hpStart {
		t.Fatalf("health dropped before frame 3 (health %v, was %v)", e.health, hpStart)
	}
	loop.tickModelAnimator(e) // evaluates t=3 -> fires the frame-3 damage skill
	if e.health >= hpStart {
		t.Fatalf("animation_frame skill did not fire at t=3 (health %v, was %v)", e.health, hpStart)
	}
	// The skill fired on the EXACT tick (a health drop). The absolute drop is the vanilla-faithful
	// post-armor/i-frame amount (applyDamageEntity), not the raw mechanic amount — asserting the fire
	// tick is the M3 contract, not the damage math (that is the untouched vanilla port).
}

// endOnlyLoop builds a loop whose mob declares a clip + ONLY an animation_end skill (self-damage), so
// the end fire is observable in isolation (no frame-skill i-frame interaction).
func endOnlyLoop(t *testing.T) (*TickLoop, *Entity) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	star := `
declare_model(name="rig", bones=[
    bone(name="arm", item="minecraft:stone"),
], animations=[
    animation(name="swing", length=6.0, channels=[
        channel(bone="arm", kind="position", keyframes=[
            keyframe(time=0.0, value=(0.0,0.0,0.0)),
            keyframe(time=6.0, value=(0.0,1.0,0.0)),
        ]),
    ]),
])
declare_mob(name="golem", base_type="zombie",
    model="rig",
    skills=[
        skill(trigger="animation_end", animation="swing",
              targeter=targeter(kind="self"), mechanics=[mechanic(kind="damage", amount=2.0)]),
    ])
`
	r := loadModelRegistry(t, star, capAll)
	loop.SetMobRegistry(r)
	e := loop.spawnDeclaredMob(r.byName["golem"], 8.5, float64(floorY+1), 8.5)
	return loop, e
}

// TestAnimationEndTriggerFiresAtClipEnd: the animation_end skill fires when the animator reaches the
// clip end (t == length) in once mode — the mob's health drops exactly then and the clip is cleared.
func TestAnimationEndTriggerFiresAtClipEnd(t *testing.T) {
	loop, e := endOnlyLoop(t)
	e.model.animator = &modelAnimator{pending: &e.model.decl.animations[0], pendingMode: animModeOnce}

	hpStart := e.health
	// length=6: apply-tick t=0, then t=1..5 (6 ticks) — the end has NOT been reached yet.
	for i := 0; i < 6; i++ {
		loop.tickModelAnimator(e)
	}
	if e.health != hpStart {
		t.Fatalf("health dropped before the clip end (health %v, was %v)", e.health, hpStart)
	}
	// The 7th tick evaluates t=6 == length -> fires animation_end + clears the once-mode clip.
	loop.tickModelAnimator(e)
	if e.health >= hpStart {
		t.Fatalf("animation_end skill did not fire at t=length (health %v, was %v)", e.health, hpStart)
	}
	if e.model.animator.clip != nil {
		t.Fatal("once-mode clip was not cleared at end")
	}
}

// --- pig oracle: a mob with no model has a nil animator + an unchanged tick path -----------------

// TestVanillaPigHasNilAnimator: a plain vanilla pig has no model, so no animator — the animator tick
// path is never entered and the byte-identical oracle (TestPluginPigEqualsGoNativePig) is untouched.
func TestVanillaPigHasNilAnimator(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	pig := loop.spawnVanillaPig(8.5, float64(floorY+1), 8.5)
	if pig.model != nil {
		t.Fatal("the vanilla pig must have e.model == nil (the animator path is never entered)")
	}
	loop.tickModelAnimator(pig) // modeless mob -> pure nil-check no-op (no panic, no state change)
}
