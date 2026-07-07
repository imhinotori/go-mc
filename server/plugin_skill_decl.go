package server

// plugin_skill_decl.go — SKILLS-01 (the MythicMobs-class declaration layer, Slice 1): the
// skill/mechanic/targeter/condition Starlark builtins + the LOAD-TIME parse into plain Go decl
// structs. This is Sulfur's first intentional plugin-layer extension BEYOND vanilla (design doc:
// plugin-mythicmobs-modelengine-design.md) — the 1:1 mandate governs the SUBSYSTEMS a mechanic calls
// into (the ported applyDamage/applyDamageEntity/addPlayerEffect/addEntityEffect), not this layer.
//
// THE SKILLS-ARE-DATA INVARIANT (stronger than goals): a skill is captured as PURE DATA at load —
// no Starlark callable is retained, so the runtime (mob_skills.go) burns ZERO starlark.Calls per
// tick, idle OR active. Starlark is only the declaration language; Go interprets the decl structs
// on the hot path (the "plugins DECLARE behavior once; Go runs the hot path" charter).
//
// LOAD-TIME CAPABILITY ENFORCEMENT: because a skill is data known at load, the LOCKED capability
// pattern enforces EARLIER than the handle-op boundary — mechanic("damage") under a manifest
// without skills.damage is a LOUD LOAD ERROR (fail-closed), the parse-time twin of capError.
// r.caps carries the loading plugin's grant (setLoadCaps, per-plugin, exactly as declare_mob's
// attribute capture uses it).
//
// SINGLE-OWNER (TICK-05): everything here runs at load (single-threaded, before the tick owns the
// registry); the captured []skillDecl is immutable shared data every spawned mob's skillRunner
// references (the declare-once invariant, Pattern 4's data analogue).

import (
	"fmt"

	"go.starlark.net/starlark"
)

// ----------------------------------------------------------------------------------------------
// The captured declaration types (plain data — no Starlark values survive the load)
// ----------------------------------------------------------------------------------------------

// skillTrigger is the moment a skill fires (the MythicMobs ~trigger analogue). Slice 1 wires the
// four cheap seams; "attack"/"combat" are reserved (design doc P4).
type skillTrigger uint8

const (
	triggerTimer   skillTrigger = iota // every `interval` ticks (per-mob countdown, tickMobSkills)
	triggerSpawn                       // once, at spawnDeclaredMob
	triggerDamaged                     // after a hit LANDS and the mob SURVIVES (applyDamageEntity tail)
	triggerDeath                       // in dieEntity (after the loot + status-3 broadcast)
	// MODEL-M3 (H.2.1): the model drives skills. animation_frame fires when the animator clock crosses
	// the declared `frame` tick of the named clip; animation_end fires at that clip's end. Dispatched by
	// tickModelAnimator (BEFORE tickMobSkills, so a keyframe skill lands the SAME tick the frame lands).
	triggerAnimationFrame
	triggerAnimationEnd
)

// triggerByName maps the Starlark trigger string to its enum. Unknown = loud load error.
var triggerByName = map[string]skillTrigger{
	"timer":           triggerTimer,
	"spawn":           triggerSpawn,
	"damaged":         triggerDamaged,
	"death":           triggerDeath,
	"animation_frame": triggerAnimationFrame,
	"animation_end":   triggerAnimationEnd,
}

// targeterDecl is one captured targeter (the MythicMobs @targeter analogue). kind is one of the
// closed slice-1 set; radius is the scan radius for the radius kinds (0 for "self").
type targeterDecl struct {
	kind   string  // "self" | "nearest_player" | "players_in_radius" | "mobs_in_radius"
	radius float64 // > 0 for the radius kinds; unused (0) for self
}

// mechanicDecl is one captured mechanic (the MythicMobs mechanic analogue). Exactly one kind's
// field-set is populated (validated at load).
type mechanicDecl struct {
	kind string // "damage" | "effect"
	// damage
	amount float64 // damage: the raw amount fed to the ported hurt paths (pre-armor, like ATTACK_DAMAGE)
	// effect
	effect    string // effect: the registry id ("minecraft:poison") — validated against skillEffectIDs
	duration  int    // effect: duration ticks (>= 1)
	amplifier int    // effect: amplifier (>= 0)
	// play_animation (MODEL-M3): the clip to queue on the caster's animator + how it ends. CASTER-scoped
	// (dispatched before the per-target loop; targeter ignored). Effect: e.model.animator.pending = clip
	// (H.0 reentrancy — never advanced synchronously). clip is load-validated against the mob's rig when
	// the declaring mob names a model.
	animName string   // play_animation: the clip name
	animMode animMode // play_animation: once/loop/hold (default once)
}

// conditionDecl is one captured condition (AND-ed; all must hold for the skill to fire).
type conditionDecl struct {
	kind  string  // "health_below" | "hit_bone"
	value float64 // health_below: fraction of MaxHealth in (0, 1]
	// strValue is the string-typed condition value. hit_bone (MODEL-M5, H.2.3): the bone name a hit must
	// have resolved to for the skill to fire (the headshot gate -- condition("hit_bone", value="head")).
	// Empty for the numeric conditions.
	strValue string
}

// skillDecl is one captured skill: trigger + gates + targeter + ordered mechanics. Immutable after
// load; shared by every spawned mob of the declaring decl (per-mob state lives in skillRunner).
type skillDecl struct {
	trigger    skillTrigger
	interval   int     // timer: the fire period in ticks (>= 1)
	chance     float64 // (0, 1]; 1.0 == always (and draws NO RNG — the roll only fires when < 1.0)
	conditions []conditionDecl
	targeter   targeterDecl
	mechanics  []mechanicDecl
	// MODEL-M3 (H.2.1) coupled args for animation triggers: animClip is REQUIRED for both
	// animation_frame + animation_end (the clip whose frame/end fires this skill); animFrame is the
	// tick offset REQUIRED iff animation_frame (matched against the animator clock crossing). Zero for
	// every non-animation trigger.
	animClip  string
	animFrame int
}

// skillEffectIDs is the closed set of effect registry ids the "effect" mechanic accepts — exactly
// the ids the ported effect system implements (mob_effect.go / ai_goals_witch.go consts). A skill
// naming an effect outside this set is a LOUD load error (never a silently-inert buff). Player
// targets get the full modifier/tick semantics; mob targets get add + instant semantics (duration
// ticking for arbitrary mobs is design-doc P6).
var skillEffectIDs = map[string]struct{}{
	effectPoison:         {},
	effectRegeneration:   {},
	effectSpeed:          {},
	effectSlowness:       {},
	effectWeakness:       {},
	effectHaste:          {},
	effectResistance:     {},
	effectJumpBoost:      {},
	effectStrength:       {},
	effectWither:         {},
	effectFireResistance: {},
	effectWaterBreathing: {},
	effectInstantHealth:  {},
	effectInstantDamage:  {},
}

// normalizeEffectID accepts both "poison" and "minecraft:poison" (the plugin-author convenience) and
// returns the canonical "minecraft:"-prefixed id the effect system keys on.
func normalizeEffectID(id string) string {
	const ns = "minecraft:"
	if len(id) >= len(ns) && id[:len(ns)] == ns {
		return id
	}
	return ns + id
}

// ----------------------------------------------------------------------------------------------
// Opaque Starlark wrapper values (the goalValue pattern: type errors fire at the precise call site)
// ----------------------------------------------------------------------------------------------

type skillValue struct{ decl skillDecl }
type mechanicValue struct{ decl mechanicDecl }
type targeterValue struct{ decl targeterDecl }
type conditionValue struct{ decl conditionDecl }

var (
	_ starlark.Value = (*skillValue)(nil)
	_ starlark.Value = (*mechanicValue)(nil)
	_ starlark.Value = (*targeterValue)(nil)
	_ starlark.Value = (*conditionValue)(nil)
)

func (v *skillValue) String() string        { return "<skill>" }
func (v *skillValue) Type() string          { return "skill" }
func (v *skillValue) Freeze()               {}
func (v *skillValue) Truth() starlark.Bool  { return starlark.True }
func (v *skillValue) Hash() (uint32, error) { return uint32(v.decl.trigger), nil }

func (v *mechanicValue) String() string        { return "<mechanic " + v.decl.kind + ">" }
func (v *mechanicValue) Type() string          { return "mechanic" }
func (v *mechanicValue) Freeze()               {}
func (v *mechanicValue) Truth() starlark.Bool  { return starlark.True }
func (v *mechanicValue) Hash() (uint32, error) { return 0, nil }

func (v *targeterValue) String() string        { return "<targeter " + v.decl.kind + ">" }
func (v *targeterValue) Type() string          { return "targeter" }
func (v *targeterValue) Freeze()               {}
func (v *targeterValue) Truth() starlark.Bool  { return starlark.True }
func (v *targeterValue) Hash() (uint32, error) { return 0, nil }

func (v *conditionValue) String() string        { return "<condition " + v.decl.kind + ">" }
func (v *conditionValue) Type() string          { return "condition" }
func (v *conditionValue) Freeze()               {}
func (v *conditionValue) Truth() starlark.Bool  { return starlark.True }
func (v *conditionValue) Hash() (uint32, error) { return 0, nil }

// ----------------------------------------------------------------------------------------------
// The builtins (methods on mobRegistry so mechanic() can enforce the loading plugin's caps)
// ----------------------------------------------------------------------------------------------

// mechanicBuiltin returns the `mechanic(kind, amount=?, effect=?, duration=?, amplifier=?)` builtin.
// It validates the kind, the kind's exact argument set (extras are loud errors — never silently
// dead), and the LOADING PLUGIN'S CAPABILITY for the kind (r.caps — the load-time enforcement of
// the LOCKED pattern: a mechanic the manifest does not grant is rejected HERE, at load, fail-closed).
func (r *mobRegistry) mechanicBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("mechanic", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var kind string
		amount := 0.0
		var effect string
		duration := 0
		amplifier := 0
		amplifierSet := false
		var amplifierV starlark.Value
		var animName, modeName string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"kind", &kind,
			"amount?", &amount,
			"effect?", &effect,
			"duration?", &duration,
			"amplifier?", &amplifierV,
			"name?", &animName,
			"mode?", &modeName,
		); err != nil {
			return nil, err
		}
		if amplifierV != nil {
			n, err := starlark.AsInt32(amplifierV)
			if err != nil {
				return nil, fmt.Errorf("mechanic: amplifier must be an int, got %s", amplifierV.Type())
			}
			amplifier, amplifierSet = int(n), true
		}
		switch kind {
		case "damage":
			if !r.caps.has(capSkillDamage) {
				return nil, fmt.Errorf("mechanic %q: this plugin lacks the %q capability (declare it in plugin.toml)", kind, "skills.damage")
			}
			if amount <= 0 {
				return nil, fmt.Errorf("mechanic %q: amount must be > 0", kind)
			}
			if effect != "" || duration != 0 || amplifierSet || animName != "" || modeName != "" {
				return nil, fmt.Errorf("mechanic %q: effect/duration/amplifier/name/mode are not valid for a damage mechanic", kind)
			}
			return &mechanicValue{decl: mechanicDecl{kind: kind, amount: amount}}, nil
		case "effect":
			if !r.caps.has(capSkillEffects) {
				return nil, fmt.Errorf("mechanic %q: this plugin lacks the %q capability (declare it in plugin.toml)", kind, "skills.effects")
			}
			if amount != 0 || animName != "" || modeName != "" {
				return nil, fmt.Errorf("mechanic %q: amount/name/mode are not valid for an effect mechanic", kind)
			}
			if effect == "" {
				return nil, fmt.Errorf("mechanic %q: effect=<id> is required (e.g. \"poison\")", kind)
			}
			id := normalizeEffectID(effect)
			if _, ok := skillEffectIDs[id]; !ok {
				return nil, fmt.Errorf("mechanic %q: unknown effect %q (not in the implemented effect set)", kind, effect)
			}
			if duration < 1 {
				return nil, fmt.Errorf("mechanic %q: duration must be >= 1 tick", kind)
			}
			if amplifier < 0 {
				return nil, fmt.Errorf("mechanic %q: amplifier must be >= 0", kind)
			}
			return &mechanicValue{decl: mechanicDecl{kind: kind, effect: id, duration: duration, amplifier: amplifier}}, nil
		case "play_animation":
			// MODEL-M3: queue a clip on the caster's rig animator. Requires models.animate (enforced at
			// LOAD, fail-closed). CASTER-scoped — a targeter is irrelevant (documented). Only name/mode
			// are valid args; the damage/effect fields are loud errors here (never a silently-dead arg).
			if !r.caps.has(capModelsAnimate) {
				return nil, fmt.Errorf("mechanic %q: this plugin lacks the %q capability (declare it in plugin.toml)", kind, "models.animate")
			}
			if animName == "" {
				return nil, fmt.Errorf("mechanic %q: name=<clip> is required", kind)
			}
			if amount != 0 || effect != "" || duration != 0 || amplifierSet {
				return nil, fmt.Errorf("mechanic %q: amount/effect/duration/amplifier are not valid for a play_animation mechanic", kind)
			}
			mode := animModeOnce
			if modeName != "" {
				mv, ok := animModeByName[modeName]
				if !ok {
					return nil, fmt.Errorf("mechanic %q: unknown mode %q (valid: once, loop, hold)", kind, modeName)
				}
				mode = mv
			}
			return &mechanicValue{decl: mechanicDecl{kind: kind, animName: animName, animMode: mode}}, nil
		default:
			return nil, fmt.Errorf("mechanic: unknown kind %q (valid: damage, effect, play_animation)", kind)
		}
	})
}

// targeterBuiltin returns the `targeter(kind, r=?)` builtin: the closed slice-1 targeter set.
func (r *mobRegistry) targeterBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("targeter", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var kind string
		radius := 0.0
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"kind", &kind,
			"r?", &radius,
		); err != nil {
			return nil, err
		}
		switch kind {
		case "self":
			if radius != 0 {
				return nil, fmt.Errorf("targeter %q: r is not valid for self", kind)
			}
			return &targeterValue{decl: targeterDecl{kind: kind}}, nil
		case "nearest_player", "players_in_radius", "mobs_in_radius":
			if radius <= 0 {
				return nil, fmt.Errorf("targeter %q: r must be > 0", kind)
			}
			return &targeterValue{decl: targeterDecl{kind: kind, radius: radius}}, nil
		default:
			return nil, fmt.Errorf("targeter: unknown kind %q (valid: self, nearest_player, players_in_radius, mobs_in_radius)", kind)
		}
	})
}

// conditionBuiltin returns the `condition(kind, value)` builtin: the slice-1 condition set.
func (r *mobRegistry) conditionBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("condition", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var kind string
		var valueV starlark.Value
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"kind", &kind,
			"value", &valueV,
		); err != nil {
			return nil, err
		}
		switch kind {
		case "health_below":
			value, ok := starlark.AsFloat(valueV)
			if !ok {
				return nil, fmt.Errorf("condition %q: value must be a number, got %s", kind, valueV.Type())
			}
			if value <= 0 || value > 1 {
				return nil, fmt.Errorf("condition %q: value must be a MaxHealth fraction in (0, 1]", kind)
			}
			return &conditionValue{decl: conditionDecl{kind: kind, value: value}}, nil
		case "hit_bone":
			// MODEL-M5 (H.2.3): the headshot gate. value is the bone NAME the hit must have resolved to
			// (a string). A non-string or empty value is a loud load error (an empty bone name would gate
			// on an entity-level hit, which never carries a bone -- reject it at load).
			bone, ok := starlark.AsString(valueV)
			if !ok {
				return nil, fmt.Errorf("condition %q: value must be a bone name string, got %s", kind, valueV.Type())
			}
			if bone == "" {
				return nil, fmt.Errorf("condition %q: value must be a non-empty bone name", kind)
			}
			return &conditionValue{decl: conditionDecl{kind: kind, strValue: bone}}, nil
		default:
			return nil, fmt.Errorf("condition: unknown kind %q (valid: health_below, hit_bone)", kind)
		}
	})
}

// skillBuiltin returns the `skill(trigger, interval=?, chance=1.0, conditions=?, targeter=,
// mechanics=[...])` builtin. It validates the trigger + its argument coupling (interval REQUIRED for
// timer, FORBIDDEN otherwise — never a silently-dead arg), unwraps the targeter/mechanics/conditions
// wrapper values, and captures the whole skill as plain data.
func (r *mobRegistry) skillBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("skill", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var triggerName string
		interval := 0
		chance := 1.0
		var conditionsList *starlark.List
		var targeterV starlark.Value
		var mechanicsList *starlark.List
		var animation string
		var frameV starlark.Value
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"trigger", &triggerName,
			"interval?", &interval,
			"chance?", &chance,
			"conditions?", &conditionsList,
			"targeter", &targeterV,
			"mechanics", &mechanicsList,
			"animation?", &animation,
			"frame?", &frameV,
		); err != nil {
			return nil, err
		}

		trig, ok := triggerByName[triggerName]
		if !ok {
			return nil, fmt.Errorf("skill: unknown trigger %q (valid: timer, spawn, damaged, death, animation_frame, animation_end)", triggerName)
		}
		if trig == triggerTimer {
			if interval < 1 {
				return nil, fmt.Errorf("skill: trigger \"timer\" requires interval >= 1 (ticks)")
			}
		} else if interval != 0 {
			return nil, fmt.Errorf("skill: interval is only valid with trigger \"timer\"")
		}
		if chance <= 0 || chance > 1 {
			return nil, fmt.Errorf("skill: chance must be in (0, 1]")
		}

		// MODEL-M3 (H.2.1) coupled args, validated like interval-iff-timer: animation is REQUIRED for
		// animation_frame + animation_end and FORBIDDEN otherwise; frame is REQUIRED iff animation_frame
		// and FORBIDDEN otherwise (never a silently-dead arg).
		frameSet := frameV != nil
		frame := 0
		if frameSet {
			n, err := starlark.AsInt32(frameV)
			if err != nil {
				return nil, fmt.Errorf("skill: frame must be an int, got %s", frameV.Type())
			}
			frame = int(n)
		}
		isAnimTrig := trig == triggerAnimationFrame || trig == triggerAnimationEnd
		if isAnimTrig {
			if animation == "" {
				return nil, fmt.Errorf("skill: trigger %q requires animation=<clip>", triggerName)
			}
		} else if animation != "" {
			return nil, fmt.Errorf("skill: animation is only valid with trigger \"animation_frame\"/\"animation_end\"")
		}
		if trig == triggerAnimationFrame {
			if !frameSet || frame < 0 {
				return nil, fmt.Errorf("skill: trigger \"animation_frame\" requires frame >= 0 (a tick offset into the clip)")
			}
		} else if frameSet {
			return nil, fmt.Errorf("skill: frame is only valid with trigger \"animation_frame\"")
		}

		tv, ok := targeterV.(*targeterValue)
		if !ok {
			return nil, fmt.Errorf("skill: targeter must be a targeter(...) value, got %s", targeterV.Type())
		}

		if mechanicsList == nil || mechanicsList.Len() == 0 {
			return nil, fmt.Errorf("skill: mechanics must be a non-empty list of mechanic(...) values")
		}
		mechs := make([]mechanicDecl, 0, mechanicsList.Len())
		for i := 0; i < mechanicsList.Len(); i++ {
			mv, ok := mechanicsList.Index(i).(*mechanicValue)
			if !ok {
				return nil, fmt.Errorf("skill: mechanics[%d] must be a mechanic(...) value, got %s", i, mechanicsList.Index(i).Type())
			}
			mechs = append(mechs, mv.decl)
		}

		var conds []conditionDecl
		if conditionsList != nil {
			conds = make([]conditionDecl, 0, conditionsList.Len())
			for i := 0; i < conditionsList.Len(); i++ {
				cv, ok := conditionsList.Index(i).(*conditionValue)
				if !ok {
					return nil, fmt.Errorf("skill: conditions[%d] must be a condition(...) value, got %s", i, conditionsList.Index(i).Type())
				}
				conds = append(conds, cv.decl)
			}
		}

		return &skillValue{decl: skillDecl{
			trigger:    trig,
			interval:   interval,
			chance:     chance,
			conditions: conds,
			targeter:   tv.decl,
			mechanics:  mechs,
			animClip:   animation,
			animFrame:  frame,
		}}, nil
	})
}

// collectSkillDecls unwraps each element of the declare_mob skills list, asserting it is a
// *skillValue (built by the skill() builtin). A nil list yields no skills (the common vanilla-mob
// case: decl.skills stays nil, the spawned mob carries NO skillRunner, and the runtime adds ZERO
// work — the pig oracle is untouched).
func collectSkillDecls(list *starlark.List) ([]skillDecl, error) {
	if list == nil {
		return nil, nil
	}
	out := make([]skillDecl, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		sv, ok := list.Index(i).(*skillValue)
		if !ok {
			return nil, fmt.Errorf("skills[%d] must be a skill(...) value, got %s", i, list.Index(i).Type())
		}
		out = append(out, sv.decl)
	}
	return out, nil
}
