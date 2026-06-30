package server

// plugin_mob_decl.go — PLUGIN-03 (Wave 2): the declare_mob / goal LOAD-TIME capture + the spawn
// path. A plugin's .star body runs ONCE at load (the Phase-21 LoadWith / Phase-22 LoadDirWith
// discipline); declare_mob(name, base_type, attributes, goals) and goal(priority, flags, tick=…)
// are server-owned Starlark builtins (injected as `extra` into LoadDirWith) that CAPTURE the
// declaration into a tick-readable registry. The captured starlark.Callables FREEZE when Load
// returns, so they are safe to invoke from the tick goroutine on a fresh budget-bounded thread.
//
// THE DECLARE-ONCE INVARIANT (23-CONTEXT decision 1, locked): a declaration is captured exactly
// ONCE at load, NOT per spawned mob. spawnDeclaredMob builds a FRESH *mobAI per spawned mob (the
// newPigAI analogue) whose starlarkGoal structs (per-mob state) REFERENCE the SHARED frozen
// callables — buildAIFromDecl (plugin_mob_ai.go) never re-parses the plugin. The interpreter fires
// ONLY inside a RUNNING goal's tick (an idle declared mob makes ZERO starlark.Calls per tick); this
// file builds the registry + spawn path, plugin_mob_ai.go builds the goal adapter.
//
// THE CUSTOM=BEHAVIOR INVARIANT (23-CONTEXT decision 6): a "custom" mob is a custom BEHAVIOR, not a
// new wire entity type. base_type resolves to an EXISTING data/entity.Entity (pig/zombie/…), and
// spawnDeclaredMob copies that type's wire id + AABB dims (NewEntity), so the 26.2 client renders it
// as the existing wire id (e.typ == entity.Pig.ID for base_type "pig"). An unknown base_type is a
// LOUD load error (never a silent default-fallback), mirroring host's "unknown event errors at load".
//
// SINGLE-OWNER (TICK-05): the registry is WRITTEN only at load (single-threaded, before the tick
// owns it) and READ at spawn (on the tick goroutine). No locks; lock-free by construction — the
// same discipline host.Manager.hooks uses.

import (
	"bytes"
	"fmt"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"go.starlark.net/starlark"
)

// ----------------------------------------------------------------------------------------------
// The captured declaration types
// ----------------------------------------------------------------------------------------------

// goalDecl is one captured goal: its priority + claimed control flags + the frozen Starlark
// callables the starlarkGoal invokes. tickFn is required; canUseFn/startFn/stopFn are optional
// (nil => the baseGoal default). The callables are SHARED frozen values — every spawned mob's
// starlarkGoal references the same goalDecl callables (Pattern 4: shared frozen callables, per-mob
// struct state).
type goalDecl struct {
	priority int
	flags    goalFlag
	canUseFn starlark.Callable
	tickFn   starlark.Callable
	startFn  starlark.Callable
	stopFn   starlark.Callable
	// continueFn is the optional can_continue callable. When nil the starlarkGoal falls back to
	// canUse (the Go convention: a goal whose continue-condition equals its use-condition). A ported
	// vanilla goal (LookAtPlayerGoal/RandomLookAroundGoal) has a DISTINCT canContinueToUse (distance²
	// + lookTime>0, or lookTime>=0), so the declaration carries its own continue predicate.
	continueFn starlark.Callable
	// requiresUpdateEveryTick threads RandomLookAroundGoal.requiresUpdateEveryTick()==true (jar-
	// confirmed) through the declaration into the starlarkGoal (FIDELITY GAP 1, flagged by 24-01).
	// starlarkGoal embeds baseGoal whose default is false; without this thread a ported @8 would not
	// tick on the every-tick path vanilla guarantees, breaking behavior-identity. Default false.
	requiresUpdateEveryTick bool
}

// mobDecl is one captured mob declaration: its name, the resolved base entity type (the EXISTING
// wire id it renders as), the declared attribute overrides (friendly-name -> value), the goals, and
// the owning plugin's capability set (threaded into the handles every goal callback receives —
// 23-CONTEXT decision: buildAIFromDecl threads parseCapabilities(manifest.Capabilities) through).
type mobDecl struct {
	name     string
	baseType entity.Entity
	attrs    map[string]float64
	goals    []goalDecl
	caps     capSet
}

// mobRegistry is the tick-readable store of captured declarations. byName is WRITTEN only at load
// (by the declare_mob builtin, single-threaded) and READ at spawn (on the tick goroutine). The
// registry also carries the capSet the load is resolving under, so declare_mob can stamp each
// captured mobDecl with the owning plugin's capabilities at capture time.
type mobRegistry struct {
	byName map[string]*mobDecl
	caps   capSet // the capability set the current LoadDirWith is resolving under (set before load)
}

// newMobRegistry builds an empty registry. The caps default to capAll until a real per-plugin load
// stamps a narrower set via setLoadCaps (so a test that injects the builtins directly without a
// manifest still gets working handles).
func newMobRegistry() *mobRegistry {
	return &mobRegistry{byName: make(map[string]*mobDecl), caps: capAll}
}

// setLoadCaps records the capability set the NEXT declare_mob captures should be stamped with — the
// owning plugin's parsed manifest capabilities. The server sets this before injecting the builtins
// for a given plugin so the captured mobDecl carries the right least-privilege grant.
func (r *mobRegistry) setLoadCaps(c capSet) { r.caps = c }

// ----------------------------------------------------------------------------------------------
// base-type resolver (custom = behavior; renders as an EXISTING wire id)
// ----------------------------------------------------------------------------------------------

// baseTypeByName maps an allowed base_type string to its data/entity record (the EXISTING 776 wire
// id + AABB dims a declared mob renders as). Scoped to the LIVING types that have a real attribute
// supplier after the Wave-1 SUB-ATTRIB fix (pig + the 7 ported animal/monster suppliers) plus the
// originally-supplied living set, so a declared mob always gets real attributes. An id NOT in this
// map is a LOUD load error (declareMob below) — never a silent default — mirroring host's
// unknown-event-at-load rule. Kept an explicit small map (not a global name index) so the allowed
// base-type set is the exact intentional list and a typo fails closed.
var baseTypeByName = map[string]entity.Entity{
	"pig":        entity.Pig,
	"cow":        entity.Cow,
	"sheep":      entity.Sheep,
	"chicken":    entity.Chicken,
	"skeleton":   entity.Skeleton,
	"creeper":    entity.Creeper,
	"spider":     entity.Spider,
	"zombie":     entity.Zombie,
	"cat":        entity.Cat,
	"witch":      entity.Witch,
	"villager":   entity.Villager,
	"silverfish": entity.Silverfish,
}

// resolveBaseType resolves a base_type string to its data/entity record. (record, true) for an
// allowed base type, (zero, false) otherwise (declareMob turns false into a loud load error).
func resolveBaseType(name string) (entity.Entity, bool) {
	rec, ok := baseTypeByName[name]
	return rec, ok
}

// ----------------------------------------------------------------------------------------------
// friendly attribute-name alias map
// ----------------------------------------------------------------------------------------------

// attrAlias maps the friendly attribute names a plugin author writes to the real attribute registry
// name keys the attribute.Map expects. The friendly names happen to equal the registry names for
// the two the gate uses (max_health/movement_speed), but the alias indirection is kept explicit so a
// plugin-facing name can diverge from the internal key later without touching the seedAttributes
// logic.
var attrAlias = map[string]string{
	"max_health":     attribute.MaxHealth.Name(),     // "max_health"
	"movement_speed": attribute.MovementSpeed.Name(), // "movement_speed"
}

// seedAttributes overrides a freshly-built attribute.Map's base values from the declared overrides.
// For each declared name it resolves the friendly alias to the registry key, materializes the
// entity's local AttributeInstance (GetInstance clones the supplier template), and SetBaseValue
// overrides the base — the value fold/clamp stays vanilla-faithful. A nil map (a non-living base
// type with no supplier — never reached for the allowed base types, all living) is a no-op guard. An
// attribute the entity's supplier does not register (GetInstance == nil) is skipped (the override
// cannot apply to an attribute the type does not have) rather than erroring at spawn — the loud
// validation already happened at load (the base type is known + living).
func seedAttributes(m *attribute.Map, attrs map[string]float64) {
	if m == nil {
		return
	}
	for name, val := range attrs {
		key := name
		if alias, ok := attrAlias[name]; ok {
			key = alias
		}
		if inst := m.GetInstance(key); inst != nil {
			inst.SetBaseValue(val)
		}
	}
}

// ----------------------------------------------------------------------------------------------
// goalValue — the opaque Starlark wrapper goal() returns and declare_mob re-reads
// ----------------------------------------------------------------------------------------------

// goalValue is the tiny custom starlark.Value goal(...) returns: it wraps a captured goalDecl so a
// TYPE error (a non-callable tick, a bad flag) is caught at goal() rather than deferred to
// declare_mob(). declare_mob asserts each element of its goals list is a *goalValue and unwraps the
// goalDecl. It is opaque (no attributes) and frozen by construction (the goalDecl callables freeze
// with the module).
type goalValue struct {
	decl goalDecl
}

var _ starlark.Value = (*goalValue)(nil)

func (g *goalValue) String() string        { return fmt.Sprintf("<goal priority=%d>", g.decl.priority) }
func (g *goalValue) Type() string          { return "goal" }
func (g *goalValue) Freeze()               {} // the wrapped callables freeze with the module; no extra state
func (g *goalValue) Truth() starlark.Bool  { return starlark.True }
func (g *goalValue) Hash() (uint32, error) { return uint32(g.decl.priority), nil }

// ----------------------------------------------------------------------------------------------
// flag parsing
// ----------------------------------------------------------------------------------------------

// flagByName maps a control-flag string to its goalFlag bit (the EXACT four Goal$Flag values from
// ai_goal.go). An unknown flag is a loud load error.
var flagByName = map[string]goalFlag{
	"MOVE":   flagMove,
	"LOOK":   flagLook,
	"JUMP":   flagJump,
	"TARGET": flagTarget,
}

// parseGoalFlags ORs the bits for a Starlark list of flag strings, erroring loudly on an unknown
// flag (rejected at load, never silently ignored). An empty list yields the zero goalFlag (a goal
// that claims no control flag — always runnable, never arbitrated; allowed but unusual).
func parseGoalFlags(list *starlark.List) (goalFlag, error) {
	if list == nil {
		return 0, nil
	}
	var f goalFlag
	for i := 0; i < list.Len(); i++ {
		s, ok := starlark.AsString(list.Index(i))
		if !ok {
			return 0, fmt.Errorf("goal: flags must be strings, got %s", list.Index(i).Type())
		}
		bit, known := flagByName[s]
		if !known {
			return 0, fmt.Errorf("goal: unknown flag %q (valid: MOVE, LOOK, JUMP, TARGET)", s)
		}
		f |= bit
	}
	return f, nil
}

// ----------------------------------------------------------------------------------------------
// the builtins (methods on mobRegistry so they capture into byName)
// ----------------------------------------------------------------------------------------------

// goalBuiltin returns the `goal(priority, flags, tick=None, can_use=None, start=None, stop=None,
// can_continue=None, requires_update_every_tick=False)` builtin. It parses the priority + flags,
// captures the (frozen-after-load) callables + the requires_update_every_tick flag, and returns a
// goalValue wrapping the goalDecl. Type errors fire HERE (at goal()) so the author sees a precise
// call site. tick is OPTIONAL (an empty-tick goal like RandomStrollGoal carries its behavior in
// start/can_continue); can_continue threads a DISTINCT canContinueToUse (the ported vanilla goals
// have one); requires_update_every_tick threads the jar's requiresUpdateEveryTick (RandomLookAround).
func (r *mobRegistry) goalBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("goal", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var priority int
		var flagsList *starlark.List
		var tickFn starlark.Callable
		var canUseFn, startFn, stopFn, continueFn starlark.Callable
		var requiresUpdateEveryTick bool
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"priority", &priority,
			"flags", &flagsList,
			"tick?", &tickFn,
			"can_use?", &canUseFn,
			"start?", &startFn,
			"stop?", &stopFn,
			"can_continue?", &continueFn,
			"requires_update_every_tick?", &requiresUpdateEveryTick,
		); err != nil {
			return nil, err
		}
		flags, err := parseGoalFlags(flagsList)
		if err != nil {
			return nil, err
		}
		// tick is OPTIONAL when start/can_use carry the behavior (RandomStrollGoal.tick is empty —
		// its behavior is start()=navigation.moveTo + canContinue=!isDone; 24-RESEARCH Open-Q §7). A
		// goal with NO callable at all is degenerate (it would do nothing), so require at least one of
		// tick/start/can_use so a typo'd declaration still fails loudly rather than silently no-op.
		if tickFn == nil && startFn == nil && canUseFn == nil {
			return nil, fmt.Errorf("goal: at least one of tick/start/can_use is required")
		}
		return &goalValue{decl: goalDecl{
			priority:                priority,
			flags:                   flags,
			canUseFn:                canUseFn,
			tickFn:                  tickFn,
			startFn:                 startFn,
			stopFn:                  stopFn,
			continueFn:              continueFn,
			requiresUpdateEveryTick: requiresUpdateEveryTick,
		}}, nil
	})
}

// declareMobBuiltin returns the `declare_mob(name, base_type, attributes=None, goals=None)` builtin.
// It resolves the base_type (loud error if unknown), parses the attributes dict (string -> float),
// collects the goalDecls from the goals list (each MUST be a goalValue), and stores the mobDecl in
// r.byName under name (loud error on a duplicate). The capture happens ONCE at load (the module body
// runs a single time), so the registry holds exactly one mobDecl per declare_mob call.
func (r *mobRegistry) declareMobBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("declare_mob", func(_ *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name, baseTypeName string
		var attrsDict *starlark.Dict
		var goalsList *starlark.List
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"name", &name,
			"base_type", &baseTypeName,
			"attributes?", &attrsDict,
			"goals?", &goalsList,
		); err != nil {
			return nil, err
		}

		if _, dup := r.byName[name]; dup {
			return nil, fmt.Errorf("declare_mob: duplicate mob name %q", name)
		}

		baseType, ok := resolveBaseType(baseTypeName)
		if !ok {
			return nil, fmt.Errorf("declare_mob %q: unknown base_type %q (allowed: pig, cow, sheep, chicken, skeleton, creeper, spider, zombie, cat, witch, villager, silverfish)", name, baseTypeName)
		}

		attrs, err := parseAttributesDict(attrsDict)
		if err != nil {
			return nil, fmt.Errorf("declare_mob %q: %w", name, err)
		}

		goals, err := collectGoalDecls(goalsList)
		if err != nil {
			return nil, fmt.Errorf("declare_mob %q: %w", name, err)
		}

		r.byName[name] = &mobDecl{
			name:     name,
			baseType: baseType,
			attrs:    attrs,
			goals:    goals,
			caps:     r.caps,
		}
		return starlark.None, nil
	})
}

// parseAttributesDict converts a Starlark attribute dict (string -> number) to a Go map. A nil dict
// yields an empty map. A non-string key or a non-numeric value is a loud error (rejected at load).
func parseAttributesDict(d *starlark.Dict) (map[string]float64, error) {
	out := make(map[string]float64)
	if d == nil {
		return out, nil
	}
	for _, item := range d.Items() {
		key, ok := starlark.AsString(item[0])
		if !ok {
			return nil, fmt.Errorf("attribute key must be a string, got %s", item[0].Type())
		}
		val, err := starlark.AsFloat(item[1])
		if !err {
			return nil, fmt.Errorf("attribute %q value must be a number, got %s", key, item[1].Type())
		}
		out[key] = val
	}
	return out, nil
}

// collectGoalDecls unwraps each element of the goals list, asserting it is a *goalValue (built by
// the goal() builtin), and returns the captured goalDecls. A non-goalValue element is a loud error.
// A nil list yields no goals (a mob with no AI behavior — allowed, it just stands).
func collectGoalDecls(list *starlark.List) ([]goalDecl, error) {
	if list == nil {
		return nil, nil
	}
	out := make([]goalDecl, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		gv, ok := list.Index(i).(*goalValue)
		if !ok {
			return nil, fmt.Errorf("goals[%d] must be a goal(...) value, got %s", i, list.Index(i).Type())
		}
		out = append(out, gv.decl)
	}
	return out, nil
}

// ----------------------------------------------------------------------------------------------
// the spawn path (the newPigAI analogue)
// ----------------------------------------------------------------------------------------------

// spawnDeclaredMob builds a live Entity from a captured declaration and adds it to the tick-owned
// store — the declared-mob analogue of spawnCandidatesReady.applyTo's Pig spawn (async.go) and
// newPigAI (ai_mob.go). It:
//
//  1. NewEntity(idAlloc.AllocID(), decl.baseType, …) — copies the base type's WIRE id + AABB dims +
//     attaches the per-type AttributeMap (the Wave-1 SUB-ATTRIB supplier: a pig base gets real pig
//     attributes, not the bare living default). So the mob renders as the EXISTING wire id.
//  2. seedAttributes — override the declared attributes onto that real base (max_health/movement_speed).
//  3. buildAIFromDecl — a FRESH *mobAI per spawned mob (plugin_mob_ai.go), its starlarkGoals
//     referencing the SHARED frozen callables (the declare-once invariant).
//  4. entities.add — the tick-owned store; the unchanged tracker broadcasts AddEntity next tick.
//
// Tick-owned (TICK-05): id allocation + store add run on the tick goroutine. The Phase-23 gate +
// debug seam call this directly; a plugin-facing rate-limited spawn builtin is OUT of scope
// (23-threat T-23-11, deferred to a future plan that respects the naturalSpawner CREATURE cap).
func (t *TickLoop) spawnDeclaredMob(decl *mobDecl, x, y, z float64) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), decl.baseType, x, y, z)
	seedAttributes(e.attributes, decl.attrs)
	// MOB-SUB-01 (Plan 29-02): initialize health to the mob's MaxHealth, the port of
	// LivingEntity.<init>'s `setHealth(getMaxHealth())`. seedAttributes has already applied the declared
	// max_health override (the vanilla pig is 10.0), so initSpawnHealth reads the FINAL folded value — a
	// mob born with health 0 would be instantly dead in applyDamageEntity's isDeadOrDying guard. This is
	// the ONE shared spawn-health helper (entity.go), called identically by every spawn path so no future
	// spawner can reintroduce the health-0 gap (CR-01 / WR-01).
	initSpawnHealth(e)
	e.ai = buildAIFromDecl(t, decl)
	// Per-entity RNG reseed (Mob.getRandom() analogue): buildAIFromDecl seeds the rng with the SHARED
	// defaultEntityRandomSeed, so WITHOUT this every declared mob would draw the IDENTICAL stream —
	// many wander mobs walked in a synchronized single-file line (same rand_int sequence). Reseed by
	// entity id here, in the ONE shared spawn path, so each declared mob (egg wander mob AND the
	// vanilla pig, which both route through here) gets its own deterministic stream and wanders
	// independently. Done before the store add (the mob is not yet ticking).
	reseedMobAI(e.ai, e.id)
	// MOB-SUB-08 (Plan 33-01): spawn-time DATA_BABY_ID carry. A mob spawned as a BABY (breedAge < 0 —
	// e.g. Plan C's breed() child, which sets breedAge = BABY_START_AGE before this add) must render
	// small client-side, so its half-scale hitbox AND the wire baby flag are present from the first
	// AddEntity/SetEntityData a tracker sends. Splice the babyDataEntry onto Entity.metadata (the slot
	// encodeSetEntityData splices verbatim, the same seam playerSkinMetadata uses) and shrink the AABB.
	// An ADULT (breedAge == 0 — the oracle pig spawns here) is skipped entirely: no metadata entry, full
	// dims, so the byte-identical oracle wire is unchanged. Babies are not spawned by any path yet (Plan C
	// adds breed()), so today this branch is inert for every live spawn; it is wired now so the breed child
	// renders correctly the moment Plan C sets the negative age.
	if e.isBaby() {
		e.refreshDimensions()
		var buf bytes.Buffer
		_, _ = babyDataEntry(e.isBaby()).WriteTo(&buf)
		e.metadata = append(e.metadata, buf.Bytes()...)
	}
	// Phase-27 (N=2): add the mob to the region that OWNS its column, NOT t.only().
	// only() resolves to the CALLING goroutine's region — globalRegion when spawned from the
	// coordinator (e.g. the SULFUR_TEST_KIT gate egg's use-packet path) — which orphans the mob
	// from its position's owning region. Its goal callbacks run during that region's fan-out and
	// re-resolve the store via regionForEntity(e) (plugin_mob_ai.go), so the store the mob is
	// ADDED to must be the SAME one: regionForEntity(e). This keeps every spawn caller correct
	// (egg + natural pig) without each having to wrap in withRegion. TICK-05: the add is a pure
	// store mutation on the owning region; spawn callers run single-threaded (coordinator use-path
	// or inside withRegion at the barrier-adjacent natural-spawn apply).
	t.regionForEntity(e).entities.add(e)
	return e
}
