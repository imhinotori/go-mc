package server

// plugin_model_decl.go — MODEL-M2 (the native-model declaration layer): the declare_model / bone
// Starlark builtins + the LOAD-TIME capture into plain Go decl structs, plus the per-mob runtime
// instance (modelInstance/boneRuntime) a spawned modeled mob carries. This is the second intentional
// plugin-layer extension BEYOND vanilla (design doc: plugin-mythicmobs-modelengine-design.md §2.3),
// built ON the M1 Display$ItemDisplay port (model_display.go). M2 = declare_model + a STATIC rig:
// a declared mob wears a rig of item_display bones mounted on the (invisible) base via the passenger
// seam, spawned at declare-mob spawn BEFORE the spawn trigger. NO animation yet (M3).
//
// MODELS-ARE-DATA INVARIANT (as skills): a model is captured as PURE DATA at load — no Starlark
// callable is retained. The rig is materialized on the hot path (spawnDeclaredMob -> attachModelRig)
// out of the frozen decl data; the tick burns zero starlark.Calls for the model, idle or active.
//
// LOAD-TIME CAPABILITY + VALIDATION (the LOCKED pattern, mirroring plugin_skill_decl.go): declare_model
// requires the models.declare capability (a loud LOAD error otherwise), bone parent references must
// resolve WITHIN the model at capture, and a declare_mob(model=) referencing an unknown model is a loud
// LOAD error. Load-order rule: declare_model must precede any declare_mob that references it (both are
// load-once registries; validated at declare_mob parse — exactly as declare_mob validates its base_type).
//
// SINGLE-OWNER (TICK-05): the modelRegistry is WRITTEN only at load (single-threaded, before the tick
// owns it) and READ at spawn (on the tick goroutine). No locks; lock-free by construction — the same
// discipline mobRegistry uses.

import (
	"fmt"
	"math"
	"strings"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"go.starlark.net/starlark"
)

// itemDisplayContextFixed is ItemDisplayContext.FIXED.getId() == 8 (the bone default per the M1 spec
// Section B.2 / Section E amendment (a): model bones render in the FIXED display context, like
// ModelEngine/BetterModel). Kept a named const so a future per-bone override can diverge from it.
//
//	[VERIFIED javap Display$ItemDisplayContext.<clinit>: FIXED = 8 (M1 spec Section B.2).]
const itemDisplayContextFixed int8 = 8

// ----------------------------------------------------------------------------------------------
// The captured declaration types (plain data — no Starlark values survive the load)
// ----------------------------------------------------------------------------------------------

// boneDecl is one captured bone: its name, the item stack it renders as (resolved to an item id at
// load), its pivot offset (the per-bone translation baked into the item_display's DATA_TRANSLATION),
// an optional parent bone name (M2 captures + validates the reference; the transform hierarchy is
// applied in M3's animator — a STATIC M2 rig uses the pivot as an absolute offset), and the
// ItemDisplayContext id (defaults FIXED=8). Immutable after load; shared by every spawned mob.
type boneDecl struct {
	name string
	// item is the resolved DATA_ITEM_STACK for this bone (component.SlotData, count 1). Built at load
	// from the bone's item="minecraft:..." string so the hot path never re-resolves.
	item component.SlotData
	// pivot is the bone's local offset, baked into DATA_TRANSLATION (VECTOR3, index 11) of its
	// item_display. For an M2 static rig it is the bone's fixed position relative to the base.
	pivotX, pivotY, pivotZ float32
	// parent is the parent bone's name ("" == mounted directly on the base). M2 validates the reference
	// resolves within the model; the parent-relative transform composition is an M3 animator concern.
	parent string
	// displayContext is the ItemDisplayContext id (BYTE, index 24). Default FIXED=8.
	displayContext int8
	// hitboxW, hitboxH are the bone's per-bone server-side hitbox size (width, height in blocks) -- the
	// MODEL-M5 (G.1) native-advantage payoff: a real server AABB per bone so a hit can resolve WHICH
	// bone it struck (headshots a Bukkit plugin cannot do). Optional hitbox=(w,h) on bone(); when
	// absent both are 0 and the runtime falls back to boneHitboxDefault (a small cube) so every bone is
	// still hittable. The box is centered at the bone's world position (base + pivot) each tick
	// (updateBoneAABBs). VANILLA PRECEDENT: EnderDragonPart carries its own EntityDimensions (a per-part
	// AABB) and isPickable()==true -- the multi-AABB entity pattern.
	//
	//	[VERIFIED javap EnderDragonPart: private final EntityDimensions size; isPickable(){iconst_1;ireturn}.]
	hitboxW, hitboxH float32
	// damageMult is the OPTIONAL per-bone damage multiplier applied BEFORE the hurt pipeline when a hit
	// resolves to this bone (MODEL-M5, spec G.1 item 5): bone(damage_mult=2.0) for a head gives a
	// server-authoritative headshot bonus. Default 0 == "unset" -> the runtime treats it as 1.0 (no
	// change), so a bone without the arg leaves damage untouched. VANILLA PRECEDENT: EnderDragon.hurt
	// applies a per-part damage transform (a non-head part takes damage/4.0f + Math.min(damage,1.0f)) --
	// a per-part damage scale is a vanilla pattern, not an invention; here it is a generic multiplier.
	//
	//	[VERIFIED javap EnderDragon.hurt (offsets 37-57): if (part != this.head) damage = damage/4.0f +
	//	 Math.min(damage, 1.0f); i.e. a per-part damage transform applied before reallyHurt.]
	damageMult float32
}

// modelDecl is one captured model: its name, the ordered bone rig, and the (captured-but-unused in M2)
// animation names M3 will consume. Immutable after load; shared by every spawned mob referencing it.
type modelDecl struct {
	name  string
	bones []boneDecl
	// animations is the declared clip set (MODEL-M3). M2 captured only names; M3 flesh­es them into
	// full animClips (per-bone-per-kind sorted keyframe lists), load-validated (channel bone exists,
	// keyframe times ascending and in [0,length]). Empty for an M2-only static rig.
	animations []animClip
	// states binds a nav-state (idle/walk/run/attack/death) to a default clip name (MODEL-M4). The
	// animator's state selector maps the mob's REAL AI state to a clip through it. Populated at
	// declare_model from the optional states={"walk": "walk_clip", ...} dict; when the dict is absent
	// (or omits a state) it falls back to the NAMING CONVENTION — a clip literally named "idle"/"walk"/
	// "run"/"attack"/"death" auto-binds to the same-named state. Nil/empty ⇒ the model has no state
	// machine (a static or purely-explicit rig): the selector never swaps a default clip in. Immutable.
	states map[modelState]string
	// caps is the owning plugin's capability set at capture time (mirrors mobDecl.caps). Unused in M2
	// (declare_model's own gate already fired at parse) but carried for the M4 animate-capability wiring.
	caps capSet
}

// boneOf returns the boneDecl with the given name, or (nil,false) — the load-time parent-resolution
// check and any M3 lookup use it.
func (m *modelDecl) boneOf(name string) (*boneDecl, bool) {
	for i := range m.bones {
		if m.bones[i].name == name {
			return &m.bones[i], true
		}
	}
	return nil, false
}

// clipOf returns the animClip with the given name, or (nil,false) — the play_animation mechanic and
// the animation_frame/animation_end trigger validation resolve clips against the rig through it.
func (m *modelDecl) clipOf(name string) (*animClip, bool) {
	for i := range m.animations {
		if m.animations[i].name == name {
			return &m.animations[i], true
		}
	}
	return nil, false
}

// modelRegistry is the tick-readable store of captured model declarations. byName is WRITTEN only at
// load (by the declare_model builtin, single-threaded) and READ at spawn (on the tick goroutine) and
// at declare_mob parse (the model= reference check). It carries the capSet the current load resolves
// under so declare_model can stamp each captured modelDecl (mirroring mobRegistry.caps).
type modelRegistry struct {
	byName map[string]*modelDecl
	caps   capSet
}

// newModelRegistry builds an empty registry. caps default to capAll until a real per-plugin load
// stamps a narrower set via setLoadCaps (so a test injecting builtins without a manifest still works).
func newModelRegistry() *modelRegistry {
	return &modelRegistry{byName: make(map[string]*modelDecl), caps: capAll}
}

// setLoadCaps records the capability set the NEXT declare_model captures should be stamped with (the
// owning plugin's parsed manifest capabilities) — the twin of mobRegistry.setLoadCaps.
func (r *modelRegistry) setLoadCaps(c capSet) { r.caps = c }

// Count returns the number of captured model declarations (the boot-load log / a coverage assert).
func (r *modelRegistry) Count() int { return len(r.byName) }

// ----------------------------------------------------------------------------------------------
// The per-mob runtime instance (materialized at spawn from the frozen decl — NOT part of the decl)
// ----------------------------------------------------------------------------------------------

// boneRuntime is one live bone of a spawned mob's rig: the item_display entity id it spawned as, the
// shared frozen decl it was built from, and the per-bone server-side AABB (G.1 data model, reserved
// NOW so native per-bone hitboxes are first-class, not retrofitted — the projectile/melee bone
// raycast in M5 fills + reads these). In M2 the AABB fields are unused (zero) but present from day one.
type boneRuntime struct {
	// id is the bone's item_display entity id (spawned via spawnItemDisplay). It is a PASSENGER of the
	// base (Entity.passengers), so the client positions it at the base and the DATA_TRANSLATION offset.
	id int32
	// decl points at the SHARED frozen boneDecl (Pattern 4: shared frozen decl, per-mob runtime state).
	decl *boneDecl
	// serverAABB is the bone's server-side hitbox (G.1). Reserved for M5's per-bone hit resolution
	// (projectile/melee bone raycast -> per-bone damage modifier). Unused (zero) in M2; kept a
	// first-class field so hitboxes attach, never retrofit. Min/Max corners in world space.
	aabbMinX, aabbMinY, aabbMinZ float64
	aabbMaxX, aabbMaxY, aabbMaxZ float64
}

// modelInstance is the per-mob live model state, the sibling of *skillRunner on Entity (both nil on
// every vanilla mob — the pig-oracle guarantee: a nil model is zero new code path, zero RNG). It holds
// the rig's live bones and reserves the M3 animator slot (nil in M2).
type modelInstance struct {
	// decl points at the SHARED frozen modelDecl (per-mob runtime state references shared decl data).
	decl *modelDecl
	// bones is the live rig: one boneRuntime per decl bone, in decl order. Their ids are the base's
	// passengers; their x/y/z mirror the base each tick (tickModelRig) so tracker distance + the G.1
	// AABBs stay correct with no wire traffic (the client positions passengers at the vehicle).
	bones []boneRuntime
	// animator is the M3 animator (clip clock + pending clip + keyframe dispatch). RESERVED slot — nil
	// in M2 (a static rig has no clock). Kept here so M3 attaches, never restructures modelInstance.
	animator *modelAnimator
}

// ----------------------------------------------------------------------------------------------
// MODEL-M3 clip data model — the captured animation (name, loop, length, per-bone-per-kind keyframes)
// ----------------------------------------------------------------------------------------------

// channelKind is the transform channel a keyframe channel drives. Each maps to one Display$ItemDisplay
// SynchedEntityData index the animator pushes: position->DATA_TRANSLATION (11), rotation->
// DATA_LEFT_ROTATION (13), scale->DATA_SCALE (12).
type channelKind uint8

const (
	channelPosition channelKind = iota // vec3 -> DATA_TRANSLATION
	channelRotation                    // euler xyz (radians) -> quaternion -> DATA_LEFT_ROTATION
	channelScale                       // vec3 -> DATA_SCALE
)

// channelKindByName maps the Starlark kind string to its enum. Unknown = loud load error.
var channelKindByName = map[string]channelKind{
	"position": channelPosition,
	"rotation": channelRotation,
	"scale":    channelScale,
}

// keyframe is one captured keyframe: a tick offset into the clip and a 3-component value. For
// position/scale the value is (x,y,z); for rotation it is the euler (x,y,z) in RADIANS the animator
// converts to a quaternion at interpolation time (H.1 amendment: authoring is euler xyz, the Go side
// builds the quaternion via JOML Quaternionf.rotationXYZ — cited in eulerXYZToQuat). Immutable.
type keyframe struct {
	time    float32 // tick offset into the clip, ascending within a channel, in [0, clip.length]
	x, y, z float32 // position/scale components, OR euler xyz radians for a rotation channel
}

// animChannel is one bone's one-kind keyframe track (e.g. bone "arm" position). keyframes are sorted
// ascending by time at load (load-validated: strictly ascending, each in [0,length]).
type animChannel struct {
	bone      string // the rig bone this channel drives (load-validated to exist in the model)
	kind      channelKind
	keyframes []keyframe
}

// animClip is one captured animation: its name, loop flag, length in ticks, and the channels. A
// modelDecl holds a slice of these (modelDecl.animations). Immutable after load; shared by every mob.
type animClip struct {
	name     string
	loop     bool
	length   float32 // clip length in ticks (> 0)
	channels []animChannel
}

// animMode is how a played clip ends (BetterModel AnimationIterator$Type analogue): once stops and
// clears the clip, loop restarts, hold clamps at the last frame. Each fires animation_end at length.
type animMode uint8

const (
	animModeOnce animMode = iota // play once, then stop (clip cleared) + fire animation_end
	animModeLoop                 // restart at length (t wraps to 0)
	animModeHold                 // clamp at the last frame + fire animation_end (once)
)

// animModeByName maps the play_animation mode string to its enum. Unknown = loud load error.
var animModeByName = map[string]animMode{
	"once": animModeOnce,
	"loop": animModeLoop,
	"hold": animModeHold,
}

// ----------------------------------------------------------------------------------------------
// MODEL-M4 nav-state machine — the animation state auto-selected from the mob's REAL AI state
// ----------------------------------------------------------------------------------------------

// modelState is the animation state the M4 selector computes from the mob's live AI each tick
// (spec H.1.4). It maps to a default clip via modelDecl.states; the highest-precedence active state
// wins (death > attack > run > walk > idle), exactly the priority the selector walks top-down.
type modelState uint8

const (
	modelStateIdle   modelState = iota // no target, not moving
	modelStateWalk                     // navigating / moving at the passive amble
	modelStateRun                      // navigating at a chase (faster-than-amble) pace
	modelStateAttack                   // has an attack target (getTarget() != 0)
	modelStateDeath                    // e.dead
)

// modelStateName maps a state to its canonical clip name for the naming-convention auto-bind (a clip
// literally named "idle"/"walk"/"run"/"attack"/"death" binds to that state when no states= dict does).
var modelStateName = map[modelState]string{
	modelStateIdle:   "idle",
	modelStateWalk:   "walk",
	modelStateRun:    "run",
	modelStateAttack: "attack",
	modelStateDeath:  "death",
}

// modelStateByName is the states= dict key parser: the Starlark state string -> modelState. Unknown
// keys are a loud load error (a typo'd state binds nothing, so reject it at declare_model).
var modelStateByName = map[string]modelState{
	"idle":   modelStateIdle,
	"walk":   modelStateWalk,
	"run":    modelStateRun,
	"attack": modelStateAttack,
	"death":  modelStateDeath,
}

// clipForState resolves a nav-state to its bound clip, or (nil,false) if the model binds no clip for
// it (the selector then leaves the current clip alone). Reads the frozen states map (built at load).
func (m *modelDecl) clipForState(st modelState) (*animClip, bool) {
	if m == nil || m.states == nil {
		return nil, false
	}
	name, ok := m.states[st]
	if !ok {
		return nil, false
	}
	return m.clipOf(name)
}

// modelAnimator is the MODEL-M3 keyframe animator, the per-mob live clip clock (a field on
// modelInstance, nil until a clip plays). H.0 reentrancy: play_animation only WRITES pending; the
// animator swaps pending into clip at the NEXT tickModelAnimator, so no synchronous clip advance and
// no animator recursion. Tick-owned.
type modelAnimator struct {
	// clip is the currently-playing clip (nil when idle). Shared frozen decl data (per-mob state is
	// only the clock t + mode + pending).
	clip *animClip
	// t is the tick clock: ticks elapsed since the clip started (0 on the tick the clip is applied).
	t int
	// mode is how the current clip ends (once/loop/hold).
	mode animMode
	// pending is a clip queued by play_animation to START at the next tickModelAnimator (H.0
	// reentrancy — never advanced synchronously). Nil when nothing is queued.
	pending     *animClip
	pendingMode animMode
	// endFired guards animation_end so a hold-clamped clip fires it exactly once.
	endFired bool
	// explicit marks the currently-playing clip as an EXPLICIT play_animation clip (MODEL-M4): the
	// nav-state selector must NOT override it until it ends. Set when applyPlayAnimation queues a clip;
	// a once/hold explicit clip clears it on animation_end (the H.2.4 handoff — control returns to the
	// nav-selected default), while a LOOP explicit clip stays explicit until another play_animation
	// replaces it (spec: "explicit overrides default until the clip ends"; a loop clip has no end, so
	// it holds until explicitly changed). Zero (false) on a clip the state selector itself installed —
	// that clip IS the default and is freely re-selected each tick.
	explicit bool
	// pendingExplicit is the explicit-flag to stamp when pending is swapped in (mirrors pendingMode).
	pendingExplicit bool
}

// eulerXYZToQuat builds a quaternion (x,y,z,w order — the Display DATA_LEFT_ROTATION wire order) from
// an euler xyz rotation in radians, a 1:1 port of JOML Quaternionf.rotationXYZ (joml 1.10.8, the exact
// method the vanilla Display transform stack uses). VERIFIED javap org.joml.Quaternionf.rotationXYZ:
//
//	sx=sin(x*0.5); cx=cosFromSin(sx, x*0.5);  (cosFromSin == cos, computed from the sin + angle)
//	sy=sin(y*0.5); cy=cosFromSin(sy, y*0.5);
//	sz=sin(z*0.5); cz=cosFromSin(sz, z*0.5);
//	cycz=cy*cz; sysz=sy*sz; sycz=sy*cz; cysz=cy*sz;
//	w = cx*cycz - sx*sysz;  x = sx*cycz + cx*sysz;  y = cx*sycz - sx*cysz;  z = cx*cysz + sx*sycz;
//
// We compute cos directly (math.Cos of the same half-angle) — cosFromSin(sin(a),a) == cos(a) exactly
// by JOML's contract, so the observable quaternion is identical.
func eulerXYZToQuat(ex, ey, ez float32) [4]float32 {
	hx, hy, hz := float64(ex)*0.5, float64(ey)*0.5, float64(ez)*0.5
	sx, cx := math.Sin(hx), math.Cos(hx)
	sy, cy := math.Sin(hy), math.Cos(hy)
	sz, cz := math.Sin(hz), math.Cos(hz)
	cycz := cy * cz
	sysz := sy * sz
	sycz := sy * cz
	cysz := cy * sz
	w := cx*cycz - sx*sysz
	x := sx*cycz + cx*sysz
	y := cx*sycz - sx*cysz
	z := cx*cysz + sx*sycz
	return [4]float32{float32(x), float32(y), float32(z), float32(w)}
}

// ----------------------------------------------------------------------------------------------
// bone item resolver (name -> item id), lazily indexed
// ----------------------------------------------------------------------------------------------

// itemNameIndex maps an unnamespaced item name ("stone") to its item id, built once from item.ByID.
// The vanilla item table has no name->id map; build a small index the first time a bone resolves an
// item so the load-time resolution is O(1) after the first call. Written once at load (single-threaded).
var itemNameIndex map[string]item.ID

// resolveBoneItem resolves an item="minecraft:stone" (or "stone") bone item string to a count-1
// component.SlotData, or an error naming the unknown item (a LOUD load error — a bone that renders
// nothing is a declaration bug, not a silent default). The "minecraft:" namespace prefix is optional.
func resolveBoneItem(name string) (component.SlotData, error) {
	if itemNameIndex == nil {
		itemNameIndex = make(map[string]item.ID, len(item.ByID))
		for id, it := range item.ByID {
			itemNameIndex[it.Name] = id
		}
	}
	key := name
	if strings.HasPrefix(key, "minecraft:") {
		key = key[len("minecraft:"):]
	}
	id, ok := itemNameIndex[key]
	if !ok {
		return component.SlotData{}, fmt.Errorf("unknown item %q", name)
	}
	if id == item.Air.ID {
		// AIR renders nothing — a bone with an air item is a declaration bug (M1 spec Section B.2 note:
		// NONE/empty renders nothing). Reject loudly, never a silently-invisible bone.
		return component.SlotData{}, fmt.Errorf("bone item %q resolves to air (renders nothing)", name)
	}
	return component.SlotData{Count: 1, ItemID: pk.VarInt(id)}, nil
}

// ----------------------------------------------------------------------------------------------
// Opaque Starlark wrapper values (the goalValue/skillValue pattern: type errors at the call site)
// ----------------------------------------------------------------------------------------------

type boneValue struct{ decl boneDecl }

var _ starlark.Value = (*boneValue)(nil)

func (v *boneValue) String() string        { return "<bone " + v.decl.name + ">" }
func (v *boneValue) Type() string          { return "bone" }
func (v *boneValue) Freeze()               {}
func (v *boneValue) Truth() starlark.Bool  { return starlark.True }
func (v *boneValue) Hash() (uint32, error) { return 0, nil }

// MODEL-M3 clip wrapper values: keyframe()/channel()/animation() build these; declare_model unwraps
// them (the goalValue/boneValue pattern — type errors fire at the precise call site).
type keyframeValue struct{ kf keyframe }
type channelValue struct{ ch animChannel }
type animationValue struct{ clip animClip }

var (
	_ starlark.Value = (*keyframeValue)(nil)
	_ starlark.Value = (*channelValue)(nil)
	_ starlark.Value = (*animationValue)(nil)
)

func (v *keyframeValue) String() string        { return "<keyframe>" }
func (v *keyframeValue) Type() string          { return "keyframe" }
func (v *keyframeValue) Freeze()               {}
func (v *keyframeValue) Truth() starlark.Bool  { return starlark.True }
func (v *keyframeValue) Hash() (uint32, error) { return 0, nil }

func (v *channelValue) String() string        { return "<channel " + v.ch.bone + ">" }
func (v *channelValue) Type() string          { return "channel" }
func (v *channelValue) Freeze()               {}
func (v *channelValue) Truth() starlark.Bool  { return starlark.True }
func (v *channelValue) Hash() (uint32, error) { return 0, nil }

func (v *animationValue) String() string        { return "<animation " + v.clip.name + ">" }
func (v *animationValue) Type() string          { return "animation" }
func (v *animationValue) Freeze()               {}
func (v *animationValue) Truth() starlark.Bool  { return starlark.True }
func (v *animationValue) Hash() (uint32, error) { return 0, nil }

// ----------------------------------------------------------------------------------------------
// The builtins (methods on modelRegistry so declare_model captures into byName + enforces caps)
// ----------------------------------------------------------------------------------------------

// boneBuiltin returns the `bone(name, item, pivot=(x,y,z), parent="", context=8, hitbox=(w,h),
// damage_mult=0)` builtin: it resolves the item (loud error on unknown), reads the optional pivot
// tuple + parent + display-context + the MODEL-M5 per-bone hitbox size + damage multiplier, and
// returns a boneValue wrapping the boneDecl. Type/item errors fire HERE (at bone()) so the author sees
// the precise call site. Parent references are validated at declare_model (the whole rig is known then).
// hitbox=(w,h) is the per-bone server AABB size (a headshot-resolving box, G.1); damage_mult scales the
// damage of a hit that resolves to this bone (the EnderDragon.hurt per-part transform precedent).
func (r *modelRegistry) boneBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("bone", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name, itemName, parent string
		var pivotV, hitboxV starlark.Value
		context := int(itemDisplayContextFixed)
		damageMult := 0.0
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"name", &name,
			"item", &itemName,
			"pivot?", &pivotV,
			"parent?", &parent,
			"context?", &context,
			"hitbox?", &hitboxV,
			"damage_mult?", &damageMult,
		); err != nil {
			return nil, err
		}
		if name == "" {
			return nil, fmt.Errorf("bone: name must be non-empty")
		}
		stack, err := resolveBoneItem(itemName)
		if err != nil {
			return nil, fmt.Errorf("bone %q: %w", name, err)
		}
		px, py, pz, err := parsePivotTuple(pivotV)
		if err != nil {
			return nil, fmt.Errorf("bone %q: %w", name, err)
		}
		if context < -128 || context > 127 {
			return nil, fmt.Errorf("bone %q: context %d out of byte range", name, context)
		}
		// MODEL-M5: the optional (width, height) per-bone hitbox size. A nil hitbox leaves both 0 (the
		// runtime falls back to boneHitboxDefault). A malformed or non-positive size is a loud load error
		// (a zero-size explicit box would silently never be hit).
		hw, hh, err := parseHitboxTuple(hitboxV)
		if err != nil {
			return nil, fmt.Errorf("bone %q: %w", name, err)
		}
		// damage_mult must be >= 0 (a negative multiplier would heal on a hit — reject at load). 0 == unset.
		if damageMult < 0 {
			return nil, fmt.Errorf("bone %q: damage_mult must be >= 0", name)
		}
		return &boneValue{decl: boneDecl{
			name:           name,
			item:           stack,
			pivotX:         px,
			pivotY:         py,
			pivotZ:         pz,
			parent:         parent,
			displayContext: int8(context),
			hitboxW:        hw,
			hitboxH:        hh,
			damageMult:     float32(damageMult),
		}}, nil
	})
}

// parsePivotTuple reads a Starlark (x,y,z) tuple/list of numbers into three float32s. A nil pivot
// yields (0,0,0) — a bone mounted at the base origin. A non-3-element or non-numeric pivot is a loud
// error (rejected at load).
func parsePivotTuple(v starlark.Value) (float32, float32, float32, error) {
	if v == nil {
		return 0, 0, 0, nil
	}
	var seq []starlark.Value
	switch t := v.(type) {
	case starlark.Tuple:
		seq = []starlark.Value(t)
	case *starlark.List:
		seq = make([]starlark.Value, t.Len())
		for i := 0; i < t.Len(); i++ {
			seq[i] = t.Index(i)
		}
	default:
		return 0, 0, 0, fmt.Errorf("pivot must be a (x,y,z) tuple, got %s", v.Type())
	}
	if len(seq) != 3 {
		return 0, 0, 0, fmt.Errorf("pivot must have exactly 3 elements, got %d", len(seq))
	}
	out := [3]float32{}
	for i, elem := range seq {
		f, ok := starlark.AsFloat(elem)
		if !ok {
			return 0, 0, 0, fmt.Errorf("pivot[%d] must be a number, got %s", i, elem.Type())
		}
		out[i] = float32(f)
	}
	return out[0], out[1], out[2], nil
}

// parseHitboxTuple reads a Starlark (width, height) tuple/list of two positive numbers into two
// float32s (the MODEL-M5 per-bone hitbox size). A nil hitbox yields (0,0) -> the runtime falls back to
// boneHitboxDefault. A non-2-element, non-numeric, or non-positive size is a loud load error (rejected
// at load — a zero/negative box would silently never resolve a hit).
func parseHitboxTuple(v starlark.Value) (float32, float32, error) {
	if v == nil {
		return 0, 0, nil
	}
	var seq []starlark.Value
	switch t := v.(type) {
	case starlark.Tuple:
		seq = []starlark.Value(t)
	case *starlark.List:
		seq = make([]starlark.Value, t.Len())
		for i := 0; i < t.Len(); i++ {
			seq[i] = t.Index(i)
		}
	default:
		return 0, 0, fmt.Errorf("hitbox must be a (width, height) tuple, got %s", v.Type())
	}
	if len(seq) != 2 {
		return 0, 0, fmt.Errorf("hitbox must have exactly 2 elements (width, height), got %d", len(seq))
	}
	out := [2]float32{}
	for i, elem := range seq {
		f, ok := starlark.AsFloat(elem)
		if !ok {
			return 0, 0, fmt.Errorf("hitbox[%d] must be a number, got %s", i, elem.Type())
		}
		if f <= 0 {
			return 0, 0, fmt.Errorf("hitbox[%d] must be > 0", i)
		}
		out[i] = float32(f)
	}
	return out[0], out[1], nil
}

// declareModelBuiltin returns the `declare_model(name, bones=[bone(...)], animations=[])` builtin. It
// enforces the models.declare capability (a loud LOAD error otherwise — the LOCKED pattern), collects
// the boneDecls (each MUST be a boneValue), validates bone names are unique + every parent reference
// resolves within the rig, captures the animation-name list (unused in M2), and stores the modelDecl
// under name (loud error on a duplicate). The capture happens ONCE at load.
func (r *modelRegistry) declareModelBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("declare_model", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		var bonesList, animationsList *starlark.List
		var statesDict *starlark.Dict
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"name", &name,
			"bones", &bonesList,
			"animations?", &animationsList,
			"states?", &statesDict,
		); err != nil {
			return nil, err
		}
		if !r.caps.has(capModelsDeclare) {
			return nil, fmt.Errorf("declare_model %q: this plugin lacks the %q capability (declare it in plugin.toml)", name, "models.declare")
		}
		if name == "" {
			return nil, fmt.Errorf("declare_model: name must be non-empty")
		}
		if _, dup := r.byName[name]; dup {
			return nil, fmt.Errorf("declare_model: duplicate model name %q", name)
		}
		if bonesList == nil || bonesList.Len() == 0 {
			return nil, fmt.Errorf("declare_model %q: bones must be a non-empty list of bone(...) values", name)
		}
		bones := make([]boneDecl, 0, bonesList.Len())
		seen := make(map[string]struct{}, bonesList.Len())
		for i := 0; i < bonesList.Len(); i++ {
			bv, ok := bonesList.Index(i).(*boneValue)
			if !ok {
				return nil, fmt.Errorf("declare_model %q: bones[%d] must be a bone(...) value, got %s", name, i, bonesList.Index(i).Type())
			}
			if _, dup := seen[bv.decl.name]; dup {
				return nil, fmt.Errorf("declare_model %q: duplicate bone name %q", name, bv.decl.name)
			}
			seen[bv.decl.name] = struct{}{}
			bones = append(bones, bv.decl)
		}
		// Parent-reference validation: every non-empty parent must name another bone in THIS rig (a
		// dangling parent = a declaration bug, a LOUD load error). A bone cannot be its own parent.
		for i := range bones {
			p := bones[i].parent
			if p == "" {
				continue
			}
			if p == bones[i].name {
				return nil, fmt.Errorf("declare_model %q: bone %q lists itself as parent", name, bones[i].name)
			}
			if _, ok := seen[p]; !ok {
				return nil, fmt.Errorf("declare_model %q: bone %q parent %q is not a bone in this model", name, bones[i].name, p)
			}
		}
		clips, err := collectAnimClips(animationsList, seen)
		if err != nil {
			return nil, fmt.Errorf("declare_model %q: %w", name, err)
		}
		states, err := buildModelStates(statesDict, clips)
		if err != nil {
			return nil, fmt.Errorf("declare_model %q: %w", name, err)
		}
		r.byName[name] = &modelDecl{
			name:       name,
			bones:      bones,
			animations: clips,
			states:     states,
			caps:       r.caps,
		}
		return starlark.None, nil
	})
}

// collectAnimClips unwraps each element of the declare_model animations list (each MUST be an
// animation(...) value) and load-VALIDATES it against the rig (MODEL-M3 H.0.1): every channel bone
// must exist in the model (boneNames), keyframe times must be strictly ascending, and each time must
// lie in [0, length]. A nil list yields nil (an M2-only static rig). Loud error on any violation.
func collectAnimClips(list *starlark.List, boneNames map[string]struct{}) ([]animClip, error) {
	if list == nil {
		return nil, nil
	}
	out := make([]animClip, 0, list.Len())
	seen := make(map[string]struct{}, list.Len())
	for i := 0; i < list.Len(); i++ {
		av, ok := list.Index(i).(*animationValue)
		if !ok {
			return nil, fmt.Errorf("animations[%d] must be an animation(...) value, got %s", i, list.Index(i).Type())
		}
		clip := av.clip
		if _, dup := seen[clip.name]; dup {
			return nil, fmt.Errorf("duplicate animation name %q", clip.name)
		}
		seen[clip.name] = struct{}{}
		for ci := range clip.channels {
			ch := &clip.channels[ci]
			if _, ok := boneNames[ch.bone]; !ok {
				return nil, fmt.Errorf("animation %q: channel bone %q is not a bone in this model", clip.name, ch.bone)
			}
			var prev float32 = -1
			for ki := range ch.keyframes {
				kf := ch.keyframes[ki]
				if kf.time < 0 || kf.time > clip.length {
					return nil, fmt.Errorf("animation %q: bone %q keyframe time %g out of [0, %g]", clip.name, ch.bone, kf.time, clip.length)
				}
				if ki > 0 && kf.time <= prev {
					return nil, fmt.Errorf("animation %q: bone %q keyframe times must be strictly ascending (got %g after %g)", clip.name, ch.bone, kf.time, prev)
				}
				prev = kf.time
			}
		}
		out = append(out, clip)
	}
	return out, nil
}

// buildModelStates builds the frozen state->clip binding (MODEL-M4) for a declared model. It merges
// two sources, EXPLICIT-WINS:
//
//  1. the optional states={"walk": "walk_clip", ...} dict — the primary, explicit API. Each key MUST
//     be a known state (idle/walk/run/attack/death) and each value MUST name a clip declared in the
//     model's animations list. An unknown state key or a dangling clip reference is a loud load error
//     (a mis-bound state would silently animate nothing — reject it at declare_model).
//  2. the NAMING-CONVENTION fallback — for any state NOT bound by the dict, a clip literally named
//     "idle"/"walk"/"run"/"attack"/"death" auto-binds to the same-named state.
//
// Returns nil (no state machine) when neither source binds anything — a static or purely-explicit rig
// whose selector never swaps a default clip. clips is the already-collected animation set (the clip
// names to validate against); statesDict may be nil.
func buildModelStates(statesDict *starlark.Dict, clips []animClip) (map[modelState]string, error) {
	clipByName := make(map[string]struct{}, len(clips))
	for i := range clips {
		clipByName[clips[i].name] = struct{}{}
	}
	out := make(map[modelState]string)
	// (1) explicit states= dict (validated, wins over the convention).
	if statesDict != nil {
		for _, item := range statesDict.Items() {
			key, ok := starlark.AsString(item[0])
			if !ok {
				return nil, fmt.Errorf("states: key must be a string, got %s", item[0].Type())
			}
			st, ok := modelStateByName[key]
			if !ok {
				return nil, fmt.Errorf("states: unknown state %q (valid: idle, walk, run, attack, death)", key)
			}
			clipName, ok := starlark.AsString(item[1])
			if !ok {
				return nil, fmt.Errorf("states[%q]: value must be a clip name string, got %s", key, item[1].Type())
			}
			if _, ok := clipByName[clipName]; !ok {
				return nil, fmt.Errorf("states[%q]: clip %q is not an animation in this model", key, clipName)
			}
			out[st] = clipName
		}
	}
	// (2) naming-convention fallback for any state the dict left unbound.
	for st, convName := range modelStateName {
		if _, bound := out[st]; bound {
			continue
		}
		if _, ok := clipByName[convName]; ok {
			out[st] = convName
		}
	}
	if len(out) == 0 {
		return nil, nil // no state machine — static / explicit-only rig
	}
	return out, nil
}

// keyframeBuiltin returns the `keyframe(time, value=(x,y,z))` builtin. value is a 3-tuple: for a
// position/scale channel it is (x,y,z); for a rotation channel it is the euler (x,y,z) in RADIANS the
// animator converts to a quaternion (eulerXYZToQuat). Non-numeric/non-3-element values are loud errors.
func (r *modelRegistry) keyframeBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("keyframe", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var timeF float64
		var valueV starlark.Value
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"time", &timeF,
			"value", &valueV,
		); err != nil {
			return nil, err
		}
		if timeF < 0 {
			return nil, fmt.Errorf("keyframe: time must be >= 0")
		}
		x, y, z, err := parsePivotTuple(valueV)
		if err != nil {
			return nil, fmt.Errorf("keyframe: value %w", err)
		}
		return &keyframeValue{kf: keyframe{time: float32(timeF), x: x, y: y, z: z}}, nil
	})
}

// channelBuiltin returns the `channel(bone, kind, keyframes=[keyframe(...)])` builtin. kind is one of
// position/rotation/scale; keyframes must be a non-empty list of keyframe(...) values. The bone
// reference + keyframe ascending/range checks happen at declare_model (the whole rig is known then).
func (r *modelRegistry) channelBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("channel", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var bone, kindName string
		var kfList *starlark.List
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"bone", &bone,
			"kind", &kindName,
			"keyframes", &kfList,
		); err != nil {
			return nil, err
		}
		if bone == "" {
			return nil, fmt.Errorf("channel: bone must be non-empty")
		}
		kind, ok := channelKindByName[kindName]
		if !ok {
			return nil, fmt.Errorf("channel %q: unknown kind %q (valid: position, rotation, scale)", bone, kindName)
		}
		if kfList == nil || kfList.Len() == 0 {
			return nil, fmt.Errorf("channel %q: keyframes must be a non-empty list of keyframe(...) values", bone)
		}
		kfs := make([]keyframe, 0, kfList.Len())
		for i := 0; i < kfList.Len(); i++ {
			kv, ok := kfList.Index(i).(*keyframeValue)
			if !ok {
				return nil, fmt.Errorf("channel %q: keyframes[%d] must be a keyframe(...) value, got %s", bone, i, kfList.Index(i).Type())
			}
			kfs = append(kfs, kv.kf)
		}
		return &channelValue{ch: animChannel{bone: bone, kind: kind, keyframes: kfs}}, nil
	})
}

// animationBuiltin returns the `animation(name, loop=False, length, channels=[channel(...)])` builtin.
// length is the clip length in ticks (> 0); channels a non-empty list of channel(...) values. The
// bone/keyframe validation is deferred to declare_model (collectAnimClips), where the rig is known.
func (r *modelRegistry) animationBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("animation", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		loop := false
		var lengthF float64
		var chList *starlark.List
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"name", &name,
			"length", &lengthF,
			"loop?", &loop,
			"channels", &chList,
		); err != nil {
			return nil, err
		}
		if name == "" {
			return nil, fmt.Errorf("animation: name must be non-empty")
		}
		if lengthF <= 0 {
			return nil, fmt.Errorf("animation %q: length must be > 0 ticks", name)
		}
		if chList == nil || chList.Len() == 0 {
			return nil, fmt.Errorf("animation %q: channels must be a non-empty list of channel(...) values", name)
		}
		chs := make([]animChannel, 0, chList.Len())
		for i := 0; i < chList.Len(); i++ {
			cv, ok := chList.Index(i).(*channelValue)
			if !ok {
				return nil, fmt.Errorf("animation %q: channels[%d] must be a channel(...) value, got %s", name, i, chList.Index(i).Type())
			}
			chs = append(chs, cv.ch)
		}
		return &animationValue{clip: animClip{name: name, loop: loop, length: float32(lengthF), channels: chs}}, nil
	})
}
