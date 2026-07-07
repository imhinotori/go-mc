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
}

// modelDecl is one captured model: its name, the ordered bone rig, and the (captured-but-unused in M2)
// animation names M3 will consume. Immutable after load; shared by every spawned mob referencing it.
type modelDecl struct {
	name  string
	bones []boneDecl
	// animations is the declared clip-name list. M2 CAPTURES it (so a model author writes the full
	// declaration once) but does NOT interpret it — M3's animator consumes it. Empty for an M2-only
	// static rig.
	animations []string
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

// modelAnimator is the M3 keyframe animator. RESERVED type — an empty placeholder in M2 so the
// modelInstance.animator field type exists (H.0 tick ordering / play_animation land in M3). No fields
// yet; M3 adds the clip clock, pending clip, and keyframe trigger dispatch.
type modelAnimator struct{}

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

// ----------------------------------------------------------------------------------------------
// The builtins (methods on modelRegistry so declare_model captures into byName + enforces caps)
// ----------------------------------------------------------------------------------------------

// boneBuiltin returns the `bone(name, item, pivot=(x,y,z), parent="", context=8)` builtin: it resolves
// the item (loud error on unknown), reads the optional pivot tuple + parent + display-context, and
// returns a boneValue wrapping the boneDecl. Type/item errors fire HERE (at bone()) so the author sees
// the precise call site. Parent references are validated at declare_model (the whole rig is known then).
func (r *modelRegistry) boneBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("bone", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name, itemName, parent string
		var pivotV starlark.Value
		context := int(itemDisplayContextFixed)
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"name", &name,
			"item", &itemName,
			"pivot?", &pivotV,
			"parent?", &parent,
			"context?", &context,
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
		return &boneValue{decl: boneDecl{
			name:           name,
			item:           stack,
			pivotX:         px,
			pivotY:         py,
			pivotZ:         pz,
			parent:         parent,
			displayContext: int8(context),
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
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"name", &name,
			"bones", &bonesList,
			"animations?", &animationsList,
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
		anims, err := parseStringList(animationsList)
		if err != nil {
			return nil, fmt.Errorf("declare_model %q: %w", name, err)
		}
		r.byName[name] = &modelDecl{
			name:       name,
			bones:      bones,
			animations: anims,
			caps:       r.caps,
		}
		return starlark.None, nil
	})
}

// parseStringList converts a Starlark list of strings to a Go slice. A nil list yields nil (no
// animations — the M2-only static rig). A non-string element is a loud error (rejected at load).
func parseStringList(list *starlark.List) ([]string, error) {
	if list == nil {
		return nil, nil
	}
	out := make([]string, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		s, ok := starlark.AsString(list.Index(i))
		if !ok {
			return nil, fmt.Errorf("animations[%d] must be a string, got %s", i, list.Index(i).Type())
		}
		out = append(out, s)
	}
	return out, nil
}
