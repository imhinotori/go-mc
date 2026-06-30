# Phase 31: PanicGoal (S2 consumer) - Context

**Gathered:** 2026-06-30
**Status:** Ready for planning
**Mode:** Auto-generated (user AFK; jar-verified defaults — no grey areas, the goal is a thin consumer of existing keystone + Phase-30.1 machinery)

<domain>
## Phase Boundary

Wire **PanicGoal@1** onto the pig (Go-native `newPigAI` + BOTH `vanilla_pig/main.star` copies, in lockstep): a pig that takes panic-causing damage flees to a random position (or to water if on fire). It is a CONSUMER — it reads the Phase-29 `lastDamageSource`/`was_hurt` keystone and reuses the Phase-30.1 `RandomPos`/`DefaultRandomPos.getPos` candidate+snap machinery. NO new subsystem.

OUT OF SCOPE: any other goal; on-fire water search beyond the cited `lookForWater` (a pig is rarely on fire — port it faithfully but it is the rare branch); the `canContinueToUse` stuck-timeout hardening (still deferred).
</domain>

<decisions>
## Implementation Decisions (jar-verified — javap/CFR over temp/cache/26.2-inner.jar)

### The goal (net.minecraft.world.entity.ai.goal.PanicGoal), flags {MOVE}, priority 1
Constructed `new PanicGoal(mob, speedModifier)` → `this(mob, speedModifier, DamageTypeTags.PANIC_CAUSES)`. Pig uses speedModifier from `Pig.registerGoals` (verify: typically 1.25 — javap `Pig.registerGoals` to confirm the exact PanicGoal speed). priority 1 (after FloatGoal@0).

```java
public boolean canUse() {
    if (!shouldPanic()) return false;
    if (mob.isOnFire()) {
        BlockPos bp = lookForWater(mob.level(), mob, 5);
        if (bp != null) { posX=bp.getX(); posY=bp.getY(); posZ=bp.getZ(); return true; }
    }
    return findRandomPosition();
}
protected boolean shouldPanic() {
    return mob.getLastDamageSource() != null && mob.getLastDamageSource().is(panicCausingDamageTypes.apply(mob));
}
protected boolean findRandomPosition() {
    Vec3 pos = DefaultRandomPos.getPos(mob, 5, 4);   // radius h=5, v=4 — DefaultRandomPos (NO up-snap)
    if (pos == null) return false;
    posX=pos.x; posY=pos.y; posZ=pos.z; return true;
}
public void start() { mob.getNavigation().moveTo(posX, posY, posZ, speedModifier); isRunning = true; }
public void stop() { isRunning = false; }
public boolean canContinueToUse() { return !mob.getNavigation().isDone(); }   // == our hasTarget
protected BlockPos lookForWater(level, mob, xzDist=5) {
    BlockPos mp = mob.blockPosition();
    if (!level.getBlockState(mp).getCollisionShape(level,mp).isEmpty()) return null; // mob not in open space
    return BlockPos.findClosestMatch(mp, 5, 1, p -> level.getFluidState(p).is(WATER)).orElse(null);
}
```

`DefaultRandomPos.getPos(mob, 5, 4)` = `RandomPos.generateRandomPos(mob, supplier)` where the supplier =
`generateRandomDirection(random, 5, 4)` (3 nextInt: x=nextInt(11)-5, y=nextInt(9)-4, z=nextInt(11)-5, ORDER x,y,z)
→ `DefaultRandomPos.generateRandomPosTowardDirection(mob, 5, restrict, dir)` (the same isOutsideLimits/isRestricted/
isNotStable rejects as stroll's DefaultRandomPos mode; NO moveUpOutOfSolid, NO isWater). best-of-10 unconditional loop,
weights all 0.0 → first non-null wins. THIS IS THE SAME machinery Phase 30.1 built — reuse `generateRandomDirection`
and the DefaultRandomPos-mode branch of `snapStrollWant` (radius 5/4 instead of 10/7).

### RNG draws (THE oracle care item — but trivially safe here)
canUse draws RNG ONLY when `shouldPanic()` is TRUE (a real lastDamageSource in panic_causes). On a fresh hit:
the on-fire branch (rare) does a `lookForWater` block scan (NO RNG — `findClosestMatch` is deterministic spiral),
else `findRandomPosition` draws the DefaultRandomPos.getPos(5,4) stream (1 supplier × up-to-10 × 3 nextInt =
UNCONDITIONAL 30 nextInt, x/y/z order, like stroll but radius 5/4). The pig ORACLE pig NEVER takes damage
(`TestPluginPigEqualsGoNativePig` drives a dry, unattacked pig) → `shouldPanic` is always false → PanicGoal.canUse
returns false on the FIRST line → ZERO draws → the oracle stays byte-identical with NO lockstep risk. STILL: add the
canUse + the 30-draw flee-pos selection to BOTH the Go goal AND both `.star` copies IN THE SAME PLAN, identical
order, so that IF a panic ever fires both halves draw identically. Gate on the oracle.

### shouldPanic predicate (reuse the keystone reads)
- Go-native: `e.lastDamageSource` is set (combat_mob.go:111); the panic check = `e.hurtTime>0`-style freshness is NOT it —
  vanilla `shouldPanic` is `getLastDamageSource()!=null && is(PANIC_CAUSES)`. Use the genuine `lastDamageSource` +
  a `damageSourceIs(src, panicCausesTag)` read against data/tag (the Phase-29 tag table). Confirm `lastDamageSource`
  has a "set / not-null" sentinel (a zero-value typeTag vs a real one) so "null" is representable.
- Plugin: reads `entity.was_hurt` (e.hurtTime>0, plugin_entity.go:187) AND `entity.last_damage_type` (the type id,
  :195) and tests `is(panic_causes)` by NAME via data/tag. NOTE: vanilla shouldPanic does NOT gate on hurtTime — it
  gates on lastDamageSource != null. `was_hurt` (hurtTime>0) is a 10-tick window; lastDamageSource persists longer.
  RESOLVE in the plan: the faithful predicate is lastDamageSource-not-null + is(panic_causes). If the plugin only has
  `was_hurt` (hurtTime>0) as the freshness proxy, that is a NARROWER window than vanilla — either expose a
  `has_last_damage` (lastDamageSource != null) handle, OR accept was_hurt as the cited proxy (document the deviation:
  the pig flees only during the 10-tick hurt window vs vanilla's longer source persistence). PREFER adding a faithful
  `has_last_damage` bool handle so Go and plugin agree exactly. Gate on the oracle (which never triggers it anyway).

### panic_causes tag
`DamageTypeTags.PANIC_CAUSES` must be in the jar-extracted damage-type tag table (data/tag, from Phase 29). VERIFY it
was extracted: grep data/tag for "panic". If absent, the GenTags extractor must include it (it extracts ALL damage-type
tags, so it should be present). The `is(panic_causes)` read is the genuine tag membership, NOT const false.
</decisions>

<code_context>
## Existing Code Insights (reuse, do not re-add)
- `server/combat_mob.go:111` — `e.lastDamageSource = src` (the keystone set).
- `server/plugin_entity.go:187-199` — `was_hurt` (e.hurtTime>0) + `last_damage_type` (e.lastDamageSource.typeTag) handles.
- `server/stroll_snap.go` — `generateRandomDirection` (in ai_goals_passive.go), the snap helpers (isStableDestination,
  moveUpOutOfSolid, isOutsideLimits, isRestricted, isWaterAt, hasMalus), `snapStrollWant` with a DefaultRandomPos vs
  LandRandomPos mode. PanicGoal reuses the DefaultRandomPos mode at radius 5/4. Generalize `snapStrollWant` /
  `getPosition`'s candidate gen to take (h,v) radii + mode, OR add a sibling `snapFleeWant`.
- `server/ai_mob.go` — `newPigAI` (add PanicGoal@1), `serverAiStep` (the snap runs before requestPath; the flee
  candidates go through the SAME setWantCandidates→snap path, just sourced from PanicGoal instead of stroll).
- `server/ai_goals_passive.go` — the goal pattern (canUse/canContinueToUse/start/stop, baseGoal flags). PanicGoal is a
  new goal struct here or a new ai_goals_panic.go.
- `plugins/vanilla_pig/main.star` + `server/assets/vanilla_pig/main.star` — add panic_can_use/panic_start (lockstep,
  byte-identical). The flee candidates cross via the SAME `path_to(31 floats)` handoff Phase 30.1 added.
- data/tag — the panic_causes damage-type tag (Phase 29 extractor).

## Verification
- `CGO_ENABLED=0 go build ./...` + `go vet ./server/` (gopls STALE — trust the compiler).
- `CGO_ENABLED=0 go test ./server/ -run 'TestPluginPigEqualsGoNativePig|TestPanic|TestNavigation|TestPigStrolls' -v`.
- A NEW test: spawn a pig, deal panic-causing damage, assert it acquires a flee target + moves AWAY (or at least
  acquires a path) — the PanicGoal analog of TestPigStrollsWithoutWedging. AND a test that a NON-panic damage type
  does NOT trigger panic.
- Docker -race: `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/`.
- Oracle MUST stay green (the dry pig never panics → zero draws).
- javap: `Pig.registerGoals` for the exact PanicGoal priority+speed; `PanicGoal` (done above); `DefaultRandomPos.getPos` (done).
</code_context>

<specifics>
## Specific Ideas
- PanicGoal@1 priority (FloatGoal is @0). Confirm via `Pig.registerGoals` javap.
- Reuse Phase-30.1's candidate machinery at radius (5,4), DefaultRandomPos mode. Do NOT duplicate generateRandomDirection.
- Prefer a faithful `has_last_damage` bool over the narrower `was_hurt` proxy for shouldPanic, so Go+plugin agree.
- New regression tests: panic-triggers-flee + non-panic-does-not-trigger.
</specifics>

<deferred>
## Deferred Ideas
- `canContinueToUse` stuck-timeout hardening (still deferred, Phase 30.1 carryover).
- On-fire water-flee is ported faithfully but is the rare branch (pigs rarely burn); the `lookForWater` spiral scan is
  RNG-free so it carries no oracle risk.
</deferred>
