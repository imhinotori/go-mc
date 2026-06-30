# Phase 31: PanicGoal (S2 consumer) - Pattern Map

**Mapped:** 2026-06-30
**Files analyzed:** 6 to edit + 1 to create
**Analogs found:** 7 / 7 (exact in-repo analogs for every surface)

PanicGoal@1 is a THIN consumer. Every surface it needs already exists in the repo from
Phase 29 (lastDamageSource keystone + `is(tag)` reads) and Phase 30.1 (the candidate/snap
machinery). This map names the exact reuse points, the closest analog, a concrete excerpt,
and the precise insertion/edit point for each.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `server/ai_goals_panic.go` (NEW) | AI goal | event-driven (reacts to damage) | `server/ai_goals_float.go` | exact (same goal pattern, sibling file) |
| `server/stroll_snap.go` (edit) | runtime snap | transform (candidates→target) | `snapStrollWant` (self) | exact (generalize or sibling) |
| `server/ai_mob.go` (edit) | registration + driver | request-response | `newPigAI` FloatGoal@0 reg | exact |
| `server/damage_source.go` (read only) | model | — | `damageSource.is(tag)` | exact (the keystone) |
| `server/plugin_entity.go` (edit) | plugin handle bridge | request-response | `was_hurt` / `last_damage_type` attrs | exact (add a sibling attr) |
| `plugins/vanilla_pig/main.star` (edit) | plugin goal decl | event-driven | `stroll_can_use` + `goal(...)` decl | exact |
| `server/assets/vanilla_pig/main.star` (edit) | plugin goal decl (mirror) | event-driven | byte-identical copy of above | exact |
| `server/plugin_pig_test.go` (oracle, read/extend) | test | — | `TestPluginPigEqualsGoNativePig` | exact |

## KEY DECISIONS (resolve in planning — both have a clear recommendation)

### Decision A — generalize `snapStrollWant(h,v,mode)` vs add `snapFleeWant`
**RECOMMENDATION: generalize the candidate GENERATION (already parameterized) and REUSE
`snapStrollWant` unchanged for the snap.**

Finding: the snap itself (`stroll_snap.go::snapStrollWant`) is ALREADY radius-agnostic — it
iterates `m.wantCands` (the 10 pre-computed candidate world positions) and applies
validity + ground-snap. It never sees `h`/`v`; the radius lives only in candidate
GENERATION (`generateRandomDirection(r, h, v)` in `ai_goals_passive.go`). PanicGoal calls
`generateRandomDirection(r, 5, 4)` instead of `(r, 10, 7)` — same function, different args.
So NO change to the snap is needed for the radius.

The ONE real fork: PanicGoal is pure DefaultRandomPos (NO up-snap, per CONTEXT
findRandomPosition). `snapStrollWant` is "consumed-as-Land" — it ALWAYS up-snaps regardless
of `wantLandMode` (see its doc comment, lines 113-135). On the flat world a v1 pig sees,
the up-snap is a near-no-op (CONTEXT confirms), so reusing the always-Land snap is faithful
for the only world that exists today. **Recommended: reuse `snapStrollWant` as-is; the
panic candidates flow through the SAME `setWantCandidates → snapStrollWant` path.** Do NOT
add `snapFleeWant`. If a future non-flat world needs the true no-up-snap path, that is the
same deferred work already flagged for stroll's `wantLandMode` — one fix covers both.

### Decision B — add a faithful `has_last_damage` handle vs reuse `was_hurt`
**RECOMMENDATION: add `has_last_damage` (Go + plugin) so the two halves express vanilla's
`getLastDamageSource() != null` IDENTICALLY.**

Vanilla `shouldPanic()` = `getLastDamageSource() != null && getLastDamageSource().is(PANIC_CAUSES)`.
- Go side: `e.lastDamageSource` is a plain VALUE struct (`damage_source.go:66`), so it is NEVER
  nil — the zero value has `typeTag == 0`. There is NO null sentinel today. **`typeTag == 0`
  is NOT a safe "unset" sentinel: id 0 is a real damage type and is a `panic_causes` member**
  (`tags.go:152` lists `{0: true, ...}`). So "no damage yet" must be tracked separately. Use
  `e.hurtTime > 0` as the freshness proxy OR add an explicit `e.hasLastDamage bool` set in the
  `flag2` block (`combat_mob.go:111`). PREFER the explicit bool — it is the faithful
  `lastDamageSource != null`; `hurtTime>0` is a NARROWER 10-tick window than vanilla's
  longer source persistence (CONTEXT <decisions> §shouldPanic).
- Plugin side: the entity handle exposes `was_hurt` (`e.hurtTime>0`) and `last_damage_type`
  (the type id) but NO `has_last_damage` and NO tag-membership read. Add a `has_last_damage`
  bool attr to mirror the Go bool exactly.

Either way the OBSERVABLE risk is zero: the oracle pig never takes damage, so `shouldPanic`
is always false → PanicGoal.canUse returns false on line 1 → ZERO draws → oracle stays green.

### Decision C (NEW — surfaced by this map) — how does the PLUGIN test `is(panic_causes)`?
The plugin reads `last_damage_type` as a RAW int id (`plugin_entity.go:200`). There is NO
plugin-side tag-membership read anywhere in the repo. The plugin cannot resolve a tag by
name today. **RECOMMENDATION: add an entity handle method `damage_in_tag("panic_causes")`
(or `last_damage_is(tag_name)`) that resolves `e.lastDamageSource.is(tag_name)` host-side**
(reusing `damageSource.is` — `damage_source.go:78`) and returns a bool. This is the plugin
analog of the Go `src.is("...")` read and keeps the membership table on the Go side (never
duplicating the id set into Starlark). Add it next to `was_hurt`/`last_damage_type` in
`entityHandle.Attr` and `AttrNames`.

## Pattern Assignments

### `server/ai_goals_panic.go` (NEW) — goal struct (event-driven AI goal)

**Analog:** `server/ai_goals_float.go` (the most recent goal, separate-file precedent).

The repo has TWO goal homes: `ai_goals_passive.go` (3 passive goals) and `ai_goals_float.go`
(one goal, its own file). FloatGoal is the freshest pattern and lives alone. **Recommend a
new `ai_goals_panic.go`** — it matches the FloatGoal precedent (one goal, its own file, a
header citing the jar) and keeps the panic logic isolated.

**Goal struct + ctor pattern** (`ai_goals_float.go:34-51`):
```go
const floatJumpProbability = 0.8

type floatGoal struct {
	baseGoal
}

func newFloatGoal() *floatGoal {
	return &floatGoal{baseGoal: newBaseGoal(flagJump)}
}
```
PanicGoal mirror: `type panicGoal struct { baseGoal; speedModifier float64;
wantX,wantY,wantZ float64 (or carry candidates) }`, `newPanicGoal(speed) =
&panicGoal{baseGoal: newBaseGoal(flagMove)}`. Speed 1.25 (CONTEXT — verify via
`Pig.registerGoals` javap; the .star header already says `PanicGoal(1.25)`).

**canUse pattern (NO-RNG short-circuit first, exactly like FloatGoal)** (`ai_goals_float.go:59-61`):
```go
func (g *floatGoal) canUse(t *TickLoop, e *Entity) bool {
	return (t.mobInWater(e) && t.mobFluidHeight(e, fluidWater) > t.getFluidJumpThreshold(e)) || t.mobInLava(e)
}
```
PanicGoal canUse: `if !g.shouldPanic(e) { return false }` FIRST (zero draws when false — the
oracle contract), then the on-fire `lookForWater` (RNG-free spiral) branch, else
`findRandomPosition` which draws the 30-nextInt DefaultRandomPos(5,4) stream. The
candidate-emit pattern is `randomStrollGoal.getPosition` (below).

**shouldPanic predicate** — reuse the Phase-29 keystone reads:
- `e.lastDamageSource` set point: `combat_mob.go:111` `e.lastDamageSource = src`.
- the tag test: `damageSource.is(tagName)` — `damage_source.go:78`:
```go
func (s damageSource) is(tagName string) bool {
	return tag.DamageTypeTags[tagName][int32(s.typeTag)]
}
```
So `shouldPanic = hasLastDamage(e) && e.lastDamageSource.is("panic_causes")`. The `is(...)`
read surface is identical to `combat_mob.go:81` `!src.is("bypasses_cooldown")` and
`death_mob.go:294` `src.is("is_player_attack")` — the established analog.

**The candidate-gen + emit pattern** (`ai_goals_passive.go:190-228`) — PanicGoal reuses
`generateRandomDirection` VERBATIM, only the radii change (5,4 not 10,7):
```go
func generateRandomDirection(r *entityRandom, h, v int) (xt, yt, zt int) {
	xt = r.nextInt(2*h+1) - h // DRAW order 1 of 3
	yt = r.nextInt(2*v+1) - v // DRAW order 2 of 3
	zt = r.nextInt(2*h+1) - h // DRAW order 3 of 3
	return
}
```
DO NOT duplicate this — call it with `(r, 5, 4)`. Add two consts mirroring
`strollHorizontalRadius=10 / strollVerticalRadius=7`: `panicHorizontalRadius=5 /
panicVerticalRadius=4`. The 10-candidate unconditional loop is `getPosition` lines 203-213.

**start / stop / canContinueToUse** — start hands candidates to the runtime via
`setWantCandidates` exactly like `randomStrollGoal.start` (`ai_goals_passive.go:242-249`):
```go
func (g *randomStrollGoal) start(_ *TickLoop, e *Entity) {
	if e.ai == nil || len(g.wantCandidates) != 10 { return }
	var arr [10][3]float64
	copy(arr[:], g.wantCandidates)
	e.ai.setWantCandidates(arr, g.wantLandMode) // PanicGoal: landMode=false (DefaultRandomPos)
}
```
canContinueToUse = `!navigation.isDone()` == `e.ai.hasTarget` (`ai_goals_passive.go:234-235`);
stop = `e.ai.clearWantTarget()` (`ai_goals_passive.go:252-256`). NOTE: PanicGoal speedModifier
1.25 differs from stroll's 1.0 — the want carries position; speed feeds `navigation.speed`.
(Faithful-scope: the v1 nav has a single `pigWalkSpeed` const — flag whether the panic speed
multiplier is wired or cited-deferred like the stroll speedModifier.)

---

### `server/stroll_snap.go` (edit — likely NO edit; reuse) — runtime snap (transform)

**Analog:** `snapStrollWant` itself (lines 136-160). It is radius-free and consumes
`m.wantCands`. Per Decision A, PanicGoal feeds it via the SAME `setWantCandidates` path.

```go
func (m *mobAI) snapStrollWant(t *TickLoop, e *Entity) (x, y, z float64, ok bool) {
	for _, c := range m.wantCands {
		bx, by, bz := floorI(c[0]), floorI(c[1]), floorI(c[2])
		if t.isOutsideLimits(by) || t.isRestricted(e, bx, by, bz) { continue }
		if !t.isStableDestination(bx, by, bz) { continue }
		by = t.moveUpOutOfSolid(bx, by, bz)
		if t.isWaterAt(bx, by, bz) || t.hasMalus(e, bx, by, bz) { continue }
		return float64(bx) + 0.5, float64(by), float64(bz) + 0.5, true
	}
	return 0, 0, 0, false
}
```
All helpers (`isStableDestination`, `moveUpOutOfSolid`, `isOutsideLimits`, `isRestricted`,
`isWaterAt`, `hasMalus`) are radius-independent and reused unchanged. **Edit point: none
expected** — if the planner instead wants a distinctly-named flee path for clarity, rename
to `snapWantCandidates` (mechanical, no behavior change). Recommended: leave as-is.

---

### `server/ai_mob.go` (edit) — goal registration + the want→snap driver

**Analog:** `newPigAI` FloatGoal@0 registration (lines 228-250).

**Edit point — add PanicGoal@1 in `newPigAI`, right after FloatGoal@0** (line 245-248):
```go
m.goals.addGoal(0, newFloatGoal())
m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(1.0))
m.goals.addGoal(7, newLookAtPlayerGoal(6.0))
m.goals.addGoal(8, newRandomLookAroundGoal())
```
becomes (insert PanicGoal@1, MOVE flag — it preempts stroll@6's MOVE via canBeReplacedBy,
priority 1 < 6, the load-bearing mechanic in `ai_goal.go:120`):
```go
m.goals.addGoal(0, newFloatGoal())
m.goals.addGoal(1, newPanicGoal(1.25)) // <- NEW (verify speed via Pig.registerGoals)
m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(1.0))
...
```
Update the header doc block (lines 212-227) which currently says "PanicGoal@1 ... DEFERRED".

**The snap-before-requestPath driver is already generic** (`serverAiStep` lines 177-184) —
it consumes `hasWantCands` regardless of which goal set them. PanicGoal needs ZERO driver
change because it routes through the SAME `setWantCandidates`:
```go
if m.hasWantCands {
	if wx, wy, wz, ok := m.snapStrollWant(t, e); ok {
		m.setWantTarget(wx, wy, wz)
	} else {
		m.hasTarget = false
	}
	m.hasWantCands = false
}
```
`setWantCandidates` (lines 139-143) is the shared handoff both goals use.

---

### `server/plugin_entity.go` (edit) — add the shouldPanic read seam(s)

**Analog:** the `was_hurt` + `last_damage_type` attrs (lines 187-200):
```go
case "was_hurt":
	return starlark.Bool(e.hurtTime > 0), nil
case "last_damage_type":
	return starlark.MakeInt(int(e.lastDamageSource.typeTag)), nil
```
**Edit points (per Decisions B + C):**
1. Add `case "has_last_damage": return starlark.Bool(<the Go null-sentinel>), nil` (mirrors
   the Go `hasLastDamage(e)`).
2. Add a `damage_in_tag` bound method (analog of the `navHandle` bound-method pattern,
   `plugin_entity.go:745-758`) that returns `e.lastDamageSource.is(tagName)` — the host-side
   tag membership so the plugin tests `is(panic_causes)` by name without an id set.
3. Add both names to `AttrNames()` (line 221-229) next to `was_hurt`, `last_damage_type`.

The `navHandle.pathTo` 31-float overload (lines 840-895) is the candidate handoff PanicGoal
reuses UNCHANGED — the plugin panic_can_use draws 30 offsets + landMode(=0.0) and calls
`nav.path_to(*flat)` exactly like `stroll_can_use`:
```go
case 31:
	...
	for i := 0; i < 30; i++ { cands[i/3][i%3] = f }
	e.ai.setWantCandidates(cands, landFlag != 0.0)
```

---

### `plugins/vanilla_pig/main.star` + `server/assets/vanilla_pig/main.star` (edit — LOCKSTEP, byte-identical)

**Confirmed byte-identical** (`diff -q` → identical). Every edit MUST be applied to BOTH in
the SAME plan.

**Analog — the stroll goal callbacks + declaration** (`main.star:83-102` + `223-229`):
```python
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW 1: the gate
        return False
    roll = entity.rand_float()   # DRAW 2: probability gate
    land_mode = 1.0 if roll >= STROLL_WATER_AVOID_PROBABILITY else 0.0
    flat = []
    for _ in range(10):
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # x (draw 1 of 3)
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V   # y (draw 2 of 3)
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # z (draw 3 of 3)
        flat.append(entity.x + xt); flat.append(entity.y + yt); flat.append(entity.z + zt)
    flat.append(land_mode)
    nav.path_to(*flat)
    return True
```
**panic_can_use mirror** (radius 5/4, DefaultRandomPos so land_mode=0.0, gate on shouldPanic
FIRST so a dry pig draws ZERO):
```python
PANIC_H = 5   # DefaultRandomPos.getPos(mob, 5, 4)
PANIC_V = 4
def panic_can_use(entity, world, nav):
    if not (entity.has_last_damage and entity.damage_in_tag("panic_causes")):  # shouldPanic: zero draws if false
        return False
    # (on-fire lookForWater branch: rare, RNG-free — port faithfully)
    flat = []
    for _ in range(10):   # DefaultRandomPos.getPos: 10 unconditional candidates
        xt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # x (draw 1 of 3)
        yt = entity.rand_int(2 * PANIC_V + 1) - PANIC_V   # y (draw 2 of 3)
        zt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # z (draw 3 of 3)
        flat.append(entity.x + xt); flat.append(entity.y + yt); flat.append(entity.z + zt)
    flat.append(0.0)   # landMode = 0.0 (DefaultRandomPos, no up-snap)
    nav.path_to(*flat)
    return True
```
**Goal declaration mirror** (`main.star:223-229` stroll block; PanicGoal flags {MOVE},
priority 1):
```python
goal(
    priority = 1,
    flags = ["MOVE"],
    can_use = panic_can_use,
    stop = panic_stop,        # nav.stop() — analog of stroll_stop (main.star:114-115)
    can_continue = panic_continue,  # nav.has_path() — analog of stroll_continue (main.star:109-110)
),
```
Add the goal to the `goals=[...]` list (line 210-250) between FloatGoal@0 and stroll@6, and
update the header deferral note (lines 9-15) which lists @1 as DEFERRED. NOTE: `last_damage_type`
exists on the handle but is NOT used here — the new `damage_in_tag` method (Decision C) does
the membership test host-side; if the planner instead keeps the raw-id read, the plugin would
need a panic_causes id-set predeclared, which this map recommends AGAINST.

---

### `server/plugin_pig_test.go` (oracle — extend in lockstep) — test

**Analog:** `TestPluginPigEqualsGoNativePig` (the header at lines 13-18; the dry-world setup
`fillFloor(ch, floorY)` at lines 37-40).

The oracle drives an UNATTACKED pig → `shouldPanic` always false → PanicGoal.canUse returns
on line 1 with zero draws → the goal is added to both halves but never fires → oracle stays
byte-identical. The plan MUST still add PanicGoal@1 to BOTH `newPigAI` and the `.star` decl
in the same plan (lockstep), and update `TestPluginPigBootLoads` (lines 55-65) which asserts
exactly 4 goals at `{0,6,7,8}` → becomes 5 goals at `{0,1,6,7,8}`.

**NEW tests to add** (CONTEXT <code_context> Verification): a panic-triggers-flee test (deal
a `panic_causes` hit, assert the pig acquires a flee target/path) and a non-panic-does-not-
trigger test (deal a non-panic damage type, assert no flee). The hit-injection analog is
`combat_mob_test.go:154-166 (TestLastDamageSource)` / `damageSourcePlayerAttack(attackerID)`
(`damage_source.go:85`), and the want-target assertion analog is `TestVanillaPigStrollSetsTarget`.

## Shared Patterns

### Tag membership read — `damageSource.is(tagName)`
**Source:** `server/damage_source.go:78` (`tag.DamageTypeTags[tagName][int32(s.typeTag)]`).
**Apply to:** Go `shouldPanic` AND (host-side) the new `damage_in_tag` plugin method.
Existing call sites to copy: `combat_mob.go:81,309,560,585,597`, `death_mob.go:294`.
The `panic_causes` table is present and verified: `data/tag/tags.go:152` +
`data/tag/tags_test.go:63-87` (it is a flattened superset of `is_player_attack`).

### The keystone damage read
**Source:** `combat_mob.go:111` (`e.lastDamageSource = src`), field `entity.go:205-208`
(plain value, no pointer). **Apply to:** `shouldPanic` (Go) + `has_last_damage`/`damage_in_tag`
(plugin). The zero-value `typeTag==0` is a REAL panic-causing id, so a separate not-null
signal is required (Decision B).

### Goal registration + flag arbitration
**Source:** `newPigAI` (`ai_mob.go:228-250`) + `canBeReplacedBy` (`ai_goal.go:120-122`).
**Apply to:** PanicGoal@1 MOVE — priority 1 preempts stroll@6 MOVE (smaller priority = higher
precedence). The driver `serverAiStep` (`ai_mob.go:155-210`) needs no change.

### Candidate → snap handoff (the Phase-30.1 split)
**Source:** goal `setWantCandidates` (`ai_mob.go:139-143`) → `snapStrollWant`
(`stroll_snap.go:136`), plugin via `navHandle.pathTo` 31-float overload
(`plugin_entity.go:867-890`). **Apply to:** PanicGoal uses the IDENTICAL path; only the
radius (5,4) and landMode (false) differ.

## No Analog Found

None. Every surface PanicGoal touches has an exact in-repo analog. The only genuinely NEW
code is (a) the `damage_in_tag` plugin handle method (host-side wrapper over the existing
`damageSource.is`), and (b) the `has_last_damage` not-null signal — both small additions
with the `was_hurt`/`last_damage_type` attrs (`plugin_entity.go:187-200`) as the shape analog.

## Metadata

**Analog search scope:** `server/` (ai_*, stroll_snap, combat_mob, damage_source, death_mob,
plugin_entity, plugin_capability, plugin_pig_test), `data/tag/`, both `vanilla_pig/main.star`.
**Files scanned:** ~20.
**Pattern extraction date:** 2026-06-30.
