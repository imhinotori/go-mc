package server

// vanilla_pig.go — PLUGIN-04 (Plan 24-02): the tick-owned mob registry + spawnVanillaPig, the SWAP
// target both newPigAI call sites (async.go natural spawn, debug.go SULFUR_DEBUG spawn) now call.
// The plugin pig becomes the ONLY pig: spawnVanillaPig looks up the boot-loaded "vanilla_pig"
// declaration and builds the entity through the Phase-23 spawnDeclaredMob path (NewEntity entity.Pig
// -> real pig attrs -> buildAIFromDecl over the declared 1:1 goals -> entities.add). It renders as
// entity.Pig.ID (custom = BEHAVIOR, not a new wire type).
//
// SINGLE-OWNER (TICK-05): the registry is WRITTEN once at boot (SetMobRegistry, before tick.Run) and
// READ at spawn on the tick goroutine. No locks — the same discipline t.plugins / the goalSelector use.

// LoadVanillaPigRegistry is the exported boot-load entry point cmd/sulfur/main.go calls: it
// materializes + loads the embedded vanilla_pig plugin and returns the populated registry (a typed
// *mobRegistry the caller hands to SetMobRegistry). It fails loudly if the declaration is absent
// (Pitfall 4). Kept a thin export over the package-internal loader so the registry type stays
// unexported (the caller treats it as an opaque handle).
func LoadVanillaPigRegistry() (*mobRegistry, error) { return loadVanillaPigRegistry() }

// SetMobRegistry installs the boot-loaded declared-mob registry on the tick. Called once at boot,
// BEFORE tick.Run (single-threaded), so the registry is tick-owned thereafter. The vanilla_pig
// declaration must be present (loadVanillaPigRegistry fails loudly otherwise) before the first pig
// can spawn (Pitfall 4).
func (t *TickLoop) SetMobRegistry(r *mobRegistry) { t.mobRegistry = r }

// spawnVanillaPig spawns the plugin-driven vanilla pig at (x,y,z) and returns the live Entity. It
// looks up the "vanilla_pig" declaration in the tick-owned registry and delegates to the Phase-23
// spawnDeclaredMob (real pig attrs + the declared 1:1 goals). reseedMobAI gives the mob its own
// per-entity deterministic RNG stream (the Mob.getRandom() seed) — the SAME reseed the old newPigAI
// sites did, so the plugin pig's draw stream matches the Go oracle for a given id.
//
// A nil registry or a missing declaration is a LOUD failure (Pitfall 4 / T-24-09): rather than a
// silent nil-deref or a pigless world, it panics with a clear message — the boot-load guarantees the
// declaration is present, so reaching this guard means the wiring is broken (a programmer error that
// must surface immediately, not a runtime condition to tolerate).
func (t *TickLoop) spawnVanillaPig(x, y, z float64) *Entity {
	if t.mobRegistry == nil {
		panic("spawnVanillaPig: no mob registry installed (SetMobRegistry must run at boot before any pig spawns)")
	}
	decl, ok := t.mobRegistry.byName[vanillaPigMobName]
	if !ok {
		panic("spawnVanillaPig: the vanilla_pig declaration is missing from the registry (boot-load did not capture it)")
	}
	pig := t.spawnDeclaredMob(decl, x, y, z)
	reseedMobAI(pig.ai, pig.id) // per-entity deterministic RNG stream (the Mob.getRandom() seed)
	return pig
}

// spawnVanillaPigWithID is spawnVanillaPig with a caller-supplied entity id instead of an allocated
// one — the behavior-identical proof (TestPluginPigEqualsGoNativePig) needs a plugin pig with the SAME
// id as the Go-native oracle so reseedMobAI derives the IDENTICAL RNG seed. It mirrors spawnDeclaredMob
// (NewEntity decl.baseType -> seedAttributes -> buildAIFromDecl -> entities.add) but pins the id.
// Test/internal use only (the live spawn always uses the allocator via spawnVanillaPig).
func (t *TickLoop) spawnVanillaPigWithID(id int32, x, y, z float64) *Entity {
	if t.mobRegistry == nil {
		panic("spawnVanillaPigWithID: no mob registry installed")
	}
	decl, ok := t.mobRegistry.byName[vanillaPigMobName]
	if !ok {
		panic("spawnVanillaPigWithID: the vanilla_pig declaration is missing from the registry")
	}
	e := NewEntity(id, decl.baseType, x, y, z)
	seedAttributes(e.attributes, decl.attrs)
	e.ai = buildAIFromDecl(t, decl)
	t.entities.add(e)
	reseedMobAI(e.ai, e.id)
	return e
}
