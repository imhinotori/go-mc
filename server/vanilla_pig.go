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

// LoadVanillaMobRegistry is the exported boot-load entry point cmd/sulfur/main.go calls: it
// materializes + loads ALL FOUR embedded vanilla mob plugins (pig/cow/sheep/chicken) into ONE registry
// and returns it (a typed *mobRegistry the caller hands to SetMobRegistry). It fails loudly if ANY
// declaration is absent (Pitfall 4 / T-34-10). Kept a thin export over the package-internal loader so
// the registry type stays unexported (the caller treats it as an opaque handle).
//
// Plan 34-04 (THE GATE) generalized this from the pig-only LoadVanillaPigRegistry: the swap now spawns
// any of the 4 declared mobs by name (spawnVanillaMob), so all 4 must boot-load into the shared byName.
func LoadVanillaMobRegistry() (*mobRegistry, error) { return loadVanillaMobRegistry() }

// loadVanillaPigRegistry is the legacy package-internal name kept as a thin alias over the generalized
// loadVanillaMobRegistry so the existing test harnesses (physics_test.go installVanillaPigRegistry,
// plugin_pig_test.go, cow_test.go) stay BYTE-IDENTICAL and the pig oracle is untouched by Plan 34-04.
// It returns the SAME 4-mob registry production uses — the pig declaration in it is identical, so
// spawnVanillaPig/spawnVanillaPigWithID over this registry produce the exact pig stream the oracle
// pins (the extra cow/sheep/chicken declarations never perturb the pig path, T-34-13).
func loadVanillaPigRegistry() (*mobRegistry, error) { return loadVanillaMobRegistry() }

// SetMobRegistry installs the boot-loaded declared-mob registry on the tick. Called once at boot,
// BEFORE tick.Run (single-threaded), so the registry is tick-owned thereafter. The vanilla_pig
// declaration must be present (loadVanillaPigRegistry fails loudly otherwise) before the first pig
// can spawn (Pitfall 4).
func (t *TickLoop) SetMobRegistry(r *mobRegistry) { t.mobRegistry = r }

// spawnVanillaMob spawns the named plugin-driven vanilla mob at (x,y,z) and returns the live Entity. It
// looks up the declaration in the tick-owned registry and delegates to the Phase-23 spawnDeclaredMob
// (real per-mob attrs + the declared 1:1 goals). spawnDeclaredMob reseeds the per-entity deterministic
// RNG stream by entity id (the Mob.getRandom() seed) — so every declared mob gets an independent stream
// matching the jar for a given id.
//
// Plan 34-04 (THE GATE) generalized the pig-only spawnVanillaPig to this name-parameterized form: the
// 4 vanilla mobs (pig/cow/sheep/chicken) all boot-load into ONE registry, and the spawn levers (/dbg,
// natural spawn) pick a mob by name. spawnVanillaPig is now a thin wrapper over this (below).
//
// A nil registry or a missing declaration is a LOUD failure (Pitfall 4 / T-24-09 / T-34-10): rather
// than a silent nil-deref or a mob-less world, it panics with a clear message — the boot-load
// guarantees all 4 declarations are present, so reaching this guard means the wiring is broken (a
// programmer error that must surface immediately, not a runtime condition to tolerate).
func (t *TickLoop) spawnVanillaMob(name string, x, y, z float64) *Entity {
	if t.mobRegistry == nil {
		panic("spawnVanillaMob: no mob registry installed (SetMobRegistry must run at boot before any mob spawns)")
	}
	decl, ok := t.mobRegistry.byName[name]
	if !ok {
		panic("spawnVanillaMob: the " + name + " declaration is missing from the registry (boot-load did not capture it)")
	}
	// spawnDeclaredMob reseeds the per-entity RNG by entity id (the Mob.getRandom() per-mob stream),
	// so no separate reseedMobAI is needed here — every declared mob gets an independent stream.
	return t.spawnDeclaredMob(decl, x, y, z)
}

// spawnVanillaPig spawns the plugin-driven vanilla pig at (x,y,z) and returns the live Entity. It is
// now a THIN WRAPPER over spawnVanillaMob(vanillaPigMobName, ...) — its guards live in spawnVanillaMob.
// Kept as a named helper so the pig call sites (and the pig oracle's spawnVanillaPigWithID below) read
// identically and the pig path is unperturbed by the Plan 34-04 generalization (T-34-13).
func (t *TickLoop) spawnVanillaPig(x, y, z float64) *Entity {
	return t.spawnVanillaMob(vanillaPigMobName, x, y, z)
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
	// MOB-SUB-01 (WR-01): LivingEntity.<init> setHealth(getMaxHealth()) — mirror spawnDeclaredMob's
	// health init (the line this test-only helper previously DROPPED, leaving every oracle-spawned pig
	// at health 0 and the damage/death path untestable on the canonical fixture). Shared helper so this
	// path stays in lockstep with the production spawners. Read AFTER seedAttributes (final folded 10.0).
	initSpawnHealth(e)
	e.ai = buildAIFromDecl(t, decl)
	t.cur().entities.add(e)
	reseedMobAI(e.ai, e.id)
	return e
}
