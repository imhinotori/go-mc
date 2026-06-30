---
phase: 33-aging-breeding-s3-pig-parity-dogfood-gate
reviewed: 2026-06-30T00:00:00Z
depth: standard
files_reviewed: 11
files_reviewed_list:
  - server/entity.go
  - server/combat_mob.go
  - server/entity_encode.go
  - server/attack_dispatch.go
  - server/ai_goals_breed.go
  - server/ai_goals_follow.go
  - server/ai_mob.go
  - server/plugin_entity.go
  - server/plugin_mob_decl.go
  - server/death_mob.go
  - plugins/vanilla_pig/main.star
findings:
  critical: 1
  warning: 4
  info: 2
  total: 7
status: issues_found
---

# Phase 33: Code Review Report — Aging + Breeding (S3) / Pig Parity / DOGFOOD GATE

**Reviewed:** 2026-06-30
**Depth:** standard (5-plan phase, the largest)
**Files Reviewed:** 11
**Status:** issues_found

## Summary

Phase 33 ports AgeableMob aging, the Animal in-love subsystem, the FEED interact path,
BreedGoal@3 + FollowParentGoal@5, the DATA_BABY_ID metadata, and the baby half-scale hitbox,
closing the full 9-goal pig oracle in lockstep with both `vanilla_pig/main.star` copies. The
jar-fidelity work is strong: the aging/in-love tick is correctly pure-int and runs OUTSIDE
serverAiStep (`tickMobAging`, combat_mob.go:677), the breed RNG draws fire in the jar's exact
order (variant `nextBoolean()` then XP `1+nextInt(7)`, ai_goals_breed.go:239/259), both draws
come from the initiator's per-mob RNG and only mid-breeding, `adjustedTickDelay` is a correct
n-identity helper distinct from the halving `reducedTickDelay`, `isPanicking` is the faithful
running-PanicGoal read (not a hurtTime proxy), and `breedAge`/`inLove`/`ageUp`/
`getSpeedUpSecondsWhenFeeding` match the bytecode op-order. The `.star` copies are byte-identical
(`diff` empty), the accessor-vs-method `.star` bug from 33-04 is fixed, and CGO=0 build is clean.

The oracle-gate integrity is sound: the breed/follow goals are canUse-gated (`isInLove` /
`isBaby`) so they draw zero RNG on the lone un-fed adult, the aging/in-love tail is dormant at
breedAge 0 / inLove 0, and the breed RNG is confined to the single `t.breed` both halves route
through — the dormant-oracle gate holds by construction.

However the review found one BLOCKER (the cross-0 baby render flag is not persisted into the
child's wire metadata, so a player who walks into range of an already-bred baby renders it
full-size — the gate's own "the baby renders small" criterion fails for late trackers), plus
four WARNINGs (the Go-native goals use a cached partner/parent while the `.star` goals re-scan,
an observable Go-vs-plugin lockstep divergence in multi-candidate scenarios; the breed XP orb is
split into multiple orbs where vanilla spawns one; both are undocumented in 33-deviations.md).

## Critical Issues

### CR-01: Bred baby's DATA_BABY_ID is not persisted into child metadata — late trackers render it full-size

**File:** `server/ai_goals_breed.go:231-247`, `server/tracker.go:79`, `server/plugin_mob_decl.go:413-418`

**Issue:** `breed()` spawns the child via `spawnVanillaPig(e.x, e.y, e.z)`, which inside
`spawnDeclaredMob` runs the spawn-time baby-carry block (plugin_mob_decl.go:413) and then
`entities.add(e)` — but at that moment the child is still `breedAge == 0` (an adult), so the
`if e.isBaby()` branch is SKIPPED: no `babyDataEntry` is spliced into `child.metadata`. Only
AFTER the add does `breed()` set `child.breedAge = babyStartAge`, call `refreshDimensions()`
(updates the in-memory AABB only), and `broadcastBabyFlag(child)` (a one-shot
`ClientboundSetEntityData` sent to the trackers present at breed time).

The tracker builds a newly-visible entity's metadata from the persisted `e.metadata` slot
(`tracker.go:79` → `encodeSetEntityData(e)` → reads `e.metadata`, entity_encode.go:447). Because
the bred baby's `metadata` slot never received the `babyDataEntry`, any player who begins
tracking the baby AFTER the breed event (walks into range, re-logs, or the baby wanders into a
new player's view) receives an AddEntity + SetEntityData with NO DATA_BABY_ID entry and renders
the pig at full adult size — a visible divergence from vanilla, where `SynchedEntityData.set`
persists the value so every subsequent metadata send carries it. This defeats the gate's
"the baby renders small" / "grows to full size" live criterion for all but the breed-time
trackers. (The AABB / distSqr gates are unaffected — `width/height` are persisted on the Entity;
this is a wire-render-flag-only defect, but still an observable 1:1 break.)

**Fix:** Persist the baby flag into the child's metadata at the moment `breedAge` is set, so any
later AddEntity/SetEntityData carries it. In `breed()` after `child.breedAge = babyStartAge`:

```go
child.breedAge = babyStartAge
child.refreshDimensions()
// Persist DATA_BABY_ID into the child's metadata slot so a LATE tracker (one that starts
// tracking after this breed) also renders it small — vanilla's SynchedEntityData.set persists
// the value for every subsequent metadata send, not just the one-shot broadcast below.
var buf bytes.Buffer
_, _ = babyDataEntry(child.isBaby()).WriteTo(&buf)
child.metadata = append(child.metadata, buf.Bytes()...)
t.broadcastBabyFlag(child) // still push the live update to current trackers
```

A cleaner alternative: have `spawnVanillaPig`/`spawnDeclaredMob` accept the initial `breedAge`
(or a `baby bool`) so the existing baby-carry block at plugin_mob_decl.go:413 runs with the
correct age BEFORE `entities.add`, keeping a single splice site. Either way, the metadata slot
must reflect `isBaby()` for the entity's whole baby lifetime, not just at breed time. Add a test
that spawns a baby via `breed()`, then runs `encodeSetEntityData(child)` and asserts the baby
DataValue (index 16, BOOLEAN, true) is present (the same assertion the spawn-time carry test
makes for an egg-spawned baby).

## Warnings

### WR-01: Go-native goals use a cached partner/parent; the `.star` goals re-scan — observable lockstep divergence

**File:** `server/ai_goals_breed.go:162-191`, `server/ai_goals_follow.go:154-164`, `plugins/vanilla_pig/main.star:279-282,313-325,337-340`

**Issue:** The Go-native goals cache the entity chosen at `canUse` time and operate on it for the
goal's lifetime:
- `breedGoal.canContinueToUse` (ai_goals_breed.go:162) reads `g.partner.isInLove()` /
  `!isPanicking(g.partner)` on the **cached** partner, and `tick` (line 189) breeds with
  `g.partner`.
- `followParentGoal.tick` (ai_goals_follow.go:163) re-paths to the **cached** `g.parent` —
  faithful to vanilla `FollowParentGoal.tick`'s `navigation.moveTo(this.parent, ...)`, which
  never re-scans.

The `.star` callbacks instead **re-scan** every time: `breed_continue` re-calls
`nearest_breeding_partner` (main.star:282), `follow_tick` re-calls `nearest_adult_parent` on
each recalc (main.star:319), and `follow_continue` re-scans (main.star:340). In a multi-candidate
scenario (≥2 in-love partners, or ≥2 adults), the plugin pig can switch to a different
partner/parent than the Go pig holds — an observable Go-vs-plugin divergence (e.g. the two parents
that get `breedAge=6000`/`inLove=0` reset differ; the followed adult differs). The `.star`
re-scan also makes the **plugin** pig diverge from vanilla `FollowParentGoal` (vanilla follows the
single canUse-bound parent, never switching mid-follow).

This passes the 9v9 oracle only because both goals are dormant on the lone-adult oracle (single
candidate, never reached). The RNG stream is unaffected (the scans draw no RNG; breed draws are
partner-independent, both on `e`), so it is not an oracle break — but it is a real behavioral
divergence the dogfood gate's "byte-identical Go vs plugin" claim does not actually cover for
multi-candidate live scenes.

**Fix:** Make the two halves agree. Either (a) cache the partner/parent host-side and expose a
`breed_partner_still_valid` / `follow_parent_pos` accessor that re-checks the CACHED entity (so
the `.star` mirrors the Go cached-entity semantics — preferred, and also makes the plugin
FollowParentGoal vanilla-faithful), or (b) document this as an accepted deviation in
33-deviations.md with the multi-candidate observable impact spelled out. Given the 1:1 mandate,
(a) is the correct path: the `.star` `follow_tick` should not re-scan — it should re-path to the
parent bound at `follow_can_use`.

### WR-02: Breed XP is split into multiple orbs where vanilla spawns exactly one

**File:** `server/ai_goals_breed.go:259`, `server/death_mob.go:347-360`

**Issue:** `breed()` awards the breeding XP via `t.awardExperienceOrbs(e, 1+mobRandom(e).nextInt(7))`.
`awardExperienceOrbs` (death_mob.go:349) loops `getExperienceValue` to SPLIT the reward into
multiple orbs — correct for the DEATH path (vanilla `ExperienceOrb.award` splits), but vanilla
breeding does NOT split: `Animal.finalizeSpawnChildFromBreeding` spawns a single orb via the
direct constructor `new ExperienceOrb(level, x, y, z, random.nextInt(7) + 1)`. For a breed value
of 4-6, `getExperienceValue` returns 3 first, leaving a remainder that spawns a SECOND orb (e.g.
6 → orb(3) + orb(3)), so a bred pig drops two orbs where vanilla drops one. The RNG draw is
unaffected (the split is value-driven, `AllocID` is off-stream), so the oracle stays green and
both halves match (both route through `t.breed`), but the orb count is an observable divergence
from vanilla and is not documented in 33-deviations.md.

**Fix:** Spawn a single orb for breeding (mirror the direct-constructor path), e.g. add a
`spawnSingleExperienceOrb(e, value)` that does NOT loop `getExperienceValue`, and call it from
`breed()`:

```go
// finalizeSpawnChildFromBreeding spawns ONE ExperienceOrb (direct ctor), NOT the split award().
t.spawnSingleExperienceOrb(e, 1+mobRandom(e).nextInt(7))
```

Keep `awardExperienceOrbs` (the splitting `ExperienceOrb.award` port) for the death path. If the
split is intentionally retained, document it in 33-deviations.md with the orb-count impact.

### WR-03: `tryBreed` re-finds the partner instead of using the goal's chosen partner — initiator-only safety, but a silent semantic mismatch

**File:** `server/plugin_entity.go:443-462`, `server/ai_goals_breed.go:177-191`

**Issue:** The Go-native `breedGoal.tick` breeds with `g.partner` (the partner chosen at
`canUse`). The plugin path's `tryBreed` (plugin_entity.go:456) re-runs `findFreePartner` at
breed time and breeds with whatever is nearest THEN. Because both RNG draws and the child-spawn
position are functions of the initiator `e` only (not the partner), the RNG stream and spawn are
identical regardless of which partner is selected — so the oracle and the single-candidate
scenario stay green. But the two parents that get reset (`breedAge=6000`, `inLove=0`) can differ
between the Go pig and the plugin pig if the nearest in-love partner changed between the
courting tick and the breed tick. This is the same cached-vs-rescan class as WR-01 and shares its
fix; called out separately because it lives at the lockstep seam (`try_breed`) the gate's
make-or-break claim rests on.

**Fix:** Thread the goal-chosen partner through `try_breed` (the `.star` already tracks the
partner position in `breed_px/py/pz`; pass it, and have `tryBreed` resolve the nearest partner
to that recorded position, or breed with the cached partner id), so both halves reset the SAME
two parents. Folds into the WR-01 cache-the-partner fix.

### WR-04: `getSpeedUpSecondsWhenFeeding` int-division-by-20 truncates the feed speedup for small ages — verify against bytecode intent

**File:** `server/entity.go:525-527`

**Issue:** `getSpeedUpSecondsWhenFeeding` is ported as `int(float32(ageDelta/20) * 0.1)` — the
integer `ageDelta / 20` truncates BEFORE the float multiply, faithfully mirroring the cited
bytecode `iload; bipush 20; idiv; i2f; ldc 0.1f; fmul; f2i`. This is correct per the jar, but the
truncation has a sharp behavioral edge the comment understates: for any `ageDelta < 200` the
result is 0 seconds (e.g. a baby only 199 ticks from adult → `199/20=9`, `9*0.1f=0.9f`,
`(int)0.9f=0` → `ageUp(0)` is a no-op, so feeding consumes the item but does NOT advance the
baby). This matches vanilla exactly, so it is not a defect — but the only test
(`tiny-baby-speedup-truncates`, inlove_feed_test.go) should assert the consume-with-no-advance
edge explicitly so a future "fix" doesn't accidentally change the truncation order. Flagged to
ensure the verified-faithful truncation is locked by an assertion, not just a comment.

**Fix:** Add an assertion that feeding a baby with `breedAge` in `(-200, 0)` consumes the item
(`shrinkHeldItem` ran) but leaves `breedAge` unchanged (`ageUp(0)` no-op), proving the integer
truncation is intentional and protected. No code change to the port itself.

## Info

### IN-01: `inheritFromInitiator` variant pick computed then discarded — keep, but ensure it is not flagged as dead code

**File:** `server/ai_goals_breed.go:239-241`

**Issue:** `breed()` draws `inheritFromInitiator := mobRandom(e).nextBoolean()` then assigns it to
`_` (the PigVariant assignment is cite-deferred per 33-deviations.md #4). This is correct — the
DRAW must be consumed for RNG lockstep even though the assignment is deferred — but the
`_ = inheritFromInitiator` pattern reads as dead code to a linter/reviewer. The cite-comment is
present and the deferral is documented, so this is acceptable; recorded only so it is not later
"cleaned up" by removing the draw (which would desync the oracle).

**Fix:** No change required. Optionally keep the draw but drop the named var:
`_ = mobRandom(e).nextBoolean() // DRAW 1 (variant pick) — consumed for lockstep; assignment cite-deferred`.

### IN-02: `nearestAdultParent` accepts a `range` arg it ignores (`_ = rng`)

**File:** `server/plugin_entity.go:411-430`

**Issue:** The `nearestAdultParent` host handle unpacks a positional `range` arg then discards it
(`_ = rng`, line 420) because `findNearestAdultParent` uses the fixed jar `inflate(8,4,8)` box.
The comment explains it is "for symmetry with nearestBreedingPartner." This is harmless but
mildly confusing — a `.star` author may believe passing a different range changes the scan. The
`.star` passes `FOLLOW_RANGE` (8.0), which happens to equal the box, so no observable issue.

**Fix:** Either drop the unused arg (call `entity.nearest_adult_parent()` with no range in the
`.star`) or add a brief `.star`-side comment that the range is informational. No behavioral change.

---

_Reviewed: 2026-06-30_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
