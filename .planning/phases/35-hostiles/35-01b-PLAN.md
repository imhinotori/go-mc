---
wave: 1
depends_on: [35-01, 35-02]
autonomous: true
gap_closure: true
requirements: [MOB-SUB-10, MOB-HOST-01, MOB-HOST-02, MOB-HOST-03]
files_modified:
  - server/plugin_mob_decl.go
  - server/plugin_mob_ai.go
  - server/plugin_mob_decl_test.go
  - plugins/vanilla_zombie/main.star
  - server/assets/vanilla_zombie/main.star
  - plugins/vanilla_skeleton/main.star
  - server/assets/vanilla_skeleton/main.star
  - plugins/vanilla_spider/main.star
  - server/assets/vanilla_spider/main.star
  - server/zombie_test.go
  - server/skeleton_test.go
  - server/spider_test.go
---

# 35-01b — Goal-kind seam: wire the Go-native combat goals into the plugin declaration path (GAP CLOSURE)

## Why this plan exists

35-01 ported the combat goals (NearestAttackableTargetGoal, HurtByTargetGoal, MeleeAttackGoal,
SpiderAttackGoal, LeapAtTargetGoal) as Go-NATIVE goal structs (newNearestAttackableTargetGoal(),
newMeleeAttackGoal(speed), etc.) but added NO way for a `.star` declaration to reference them. Result
(found during wave-2 execution):
- 35-03 (zombie) + 35-04 (skeleton): correctly BLOCKED — a `.star` cannot acquire a target / set
  attackTargetID / deal melee damage through the keystone; no handle, no goal-kind reference exists.
- 35-05 (spider): shipped a `.star` that LEAPS + faces but whose melee DAMAGE is unwired — its comment
  admits "the host melee-hit is a HOST-side op the Go-native meleeAttackGoal performs" but that goal is
  NEVER instantiated for a declared mob. The spider tests assert leap/daylight RNG + boot-load, NOT
  player damage. So the phase's CORE goal — "hostiles hunt and ATTACK the player" — is unwired across
  all 3. The Go-native combat goals are ORPHANED.

This plan adds the missing seam ONCE, then rewires all 3 hostiles to route their combat goals through
the Go-native goals (the 1:1 jar port), and asserts REAL player damage in each test. The pig oracle
stays byte-identical (a pig declares no kind-goal → the new branch is never taken for it).

## The design (chosen: native-goal-kind reference — both wave-2 executors independently recommended it)

A declared goal may name a Go-native goal by `kind=` INSTEAD of supplying Starlark callbacks. The
`.star` declares the goal's PRIORITY + FLAGS + KIND; the Go-native goal (the verbatim jar port 35-01
built) does the work. This is faithful to vanilla (zombie/skeleton/spider all use the SAME
MeleeAttackGoal/NearestAttackableTargetGoal CLASS — there is nothing per-mob to re-express in `.star`),
avoids reimplementing the combat RNG in Starlark (no lockstep-drift risk), and uses 35-01's goals
directly. A `.star` goal still uses callbacks for the per-mob-varying goals (stroll/look/float/panic).

<task>
### Task 1 — Add the goal-kind seam (the machinery)

<read_first>
- server/plugin_mob_decl.go (goalDecl struct ~line 45; goalBuiltin ~line 244 — the goal() builtin)
- server/plugin_mob_ai.go (buildAIFromDecl ~line 185 — where starlarkGoal adapters are built + TARGET routing)
- server/ai_goals_target.go (newNearestAttackableTargetGoal, newHurtByTargetGoal)
- server/ai_goals_attack.go (newMeleeAttackGoal(speed), newSpiderAttackGoal(speed))
- server/ai_goals_float.go (newFloatGoal — if float is kind-routed; else leave .star-expressed)
- 35-JARNOTES.md (the combat-goal bytecode + the LeapAtTargetGoal section — confirm constructor signatures)
</read_first>

<action>
1. Add `nativeKind string` to the `goalDecl` struct (server/plugin_mob_decl.go). Default "" (the existing
   starlarkGoal path).
2. Extend the `goal()` builtin (goalBuiltin) with an optional `kind?` string param:
   `"kind?", &nativeKind`. When `kind` is non-empty, the callback params (tick/can_use/start/stop/
   can_continue) MUST be absent (a kind-goal carries NO Starlark body) — error loudly if both are given.
   Relax the "at least one of tick/start/can_use" requirement when kind is set. Set goalDecl.nativeKind.
3. In buildAIFromDecl (server/plugin_mob_ai.go): for each goalDecl, if `gd.nativeKind != ""`, instantiate
   the matching Go-native goal instead of a starlarkGoal, via a switch:
     - "nearest_attackable_target" → newNearestAttackableTargetGoal()
     - "hurt_by_target"            → newHurtByTargetGoal()
     - "melee_attack"              → newMeleeAttackGoal(declaredWalkSpeed-or-the-goal-speed)
     - "spider_attack"             → newSpiderAttackGoal(speed)
     - "leap_at_target"            → newLeapAtTargetGoal(yd) [if 35-05 added it; else add a constructor]
     - "float"                     → newFloatGoal()   [optional — only if a hostile kind-routes float]
     - default                     → error "unknown goal kind %q" (fail loudly, never silent no-op)
   Route the instantiated goal to m.targetSelector (if gd.flags has flagTarget) or m.goals, EXACTLY as
   the starlarkGoal branch does. The Go goal's own flags() must still match the declared flags (assert).
4. The Go-native goals already draw via the mob's seeded RNG (mobRandom/the mobAI.rng) — confirm a
   kind-routed goal shares the SAME per-mob rng stream as the starlarkGoal goals (so lockstep holds).
   The NearestAttackableTargetGoal gate is nextInt(10) (un-halved, per 35-01), the leap nextInt(5).
</action>

<acceptance_criteria>
- server/plugin_mob_decl.go: goalDecl has a `nativeKind string` field; goalBuiltin accepts `kind?`.
- A goal() with BOTH kind and a callback errors (grep the error string in the test).
- server/plugin_mob_ai.go buildAIFromDecl has a `switch gd.nativeKind` instantiating the 5+ Go goals;
  an unknown kind errors (not a silent no-op).
- CGO_ENABLED=0 go build ./... exit 0; go vet ./server/ clean.
- TestPluginPigEqualsGoNativePig byte-identical (the pig declares no kind-goal → branch never taken).
- A new plugin_mob_decl_test.go test declares a kind-goal mob + asserts the Go-native goal is in the
  right selector (targetSelector for a TARGET kind, goals otherwise).
</acceptance_criteria>
</task>

<task>
### Task 2 — Rewire the 3 hostile .star plugins to kind-goals + assert REAL player damage

<read_first>
- plugins/vanilla_zombie/main.star + server/assets/vanilla_zombie/main.star (35-03 left these UNWRITTEN — create them)
- plugins/vanilla_skeleton/main.star + server/assets/vanilla_skeleton/main.star (35-04 left these UNWRITTEN — create them)
- plugins/vanilla_spider/main.star + server/assets/vanilla_spider/main.star (35-05 wrote these — REWIRE the combat goals to kinds)
- plugins/vanilla_cow/main.star (the passive-goal callback template: stroll/look/float/panic stay .star)
- server/zombie_test.go / skeleton_test.go (35-03/04 left these unwritten — create) + server/spider_test.go (35-05 wrote — extend)
- 35-JARNOTES.md (the per-hostile goal sets + attributes)
</read_first>

<action>
For EACH hostile, the combat goals become kind-goals; the passive goals stay .star callbacks:
- Zombie (vanilla_zombie): goalSelector ZombieAttackGoal@3 → goal(priority=3, flags=["MOVE"], kind="melee_attack")
  (the setAggressive bit is a cite-deferred client visual); stroll@7 + look@8 + around@8 stay .star.
  targetSelector: goal(priority=1, flags=["TARGET"], kind="hurt_by_target") + goal(priority=2,
  flags=["TARGET"], kind="nearest_attackable_target"). base_type zombie, attrs FOLLOW_RANGE 35/spd 0.23/
  ATK 3.0/ARMOR 2.0.
- Skeleton (vanilla_skeleton): MELEE-ONLY v1 → goal(kind="melee_attack")@? + stroll@5 + look@6; targetSelector
  hurt_by_target@1 + nearest_attackable_target@2. base_type skeleton, spd 0.25. Bow DEFERRED (documented).
- Spider (vanilla_spider): float@1 (.star or kind="float") + goal(priority=3, flags=["JUMP","MOVE"],
  kind="leap_at_target") + goal(priority=4, flags=["MOVE"], kind="spider_attack") + stroll@5 + look@6;
  targetSelector hurt_by_target@1 + nearest_attackable_target@2 (the SpiderTargetGoal<Player> = a
  NearestAttackableTargetGoal subclass — use kind="nearest_attackable_target"). attrs MH16/spd0.3.
  REMOVE the hand-rolled .star leap_can_use/spider_attack_can_use bodies (the kind-goals replace them);
  the daylight-flee is now in the Go-native newSpiderAttackGoal (already ported in 35-05's a61aeff7/6a73700f).
Both .star copies of EACH hostile BYTE-IDENTICAL (diff empty).

Tests: each hostile test (TestZombieBehavior / TestSkeletonBehavior / TestSpiderBehavior) must spawn the
mob + a player, drive ticks, and ASSERT the REAL phase goal: e.ai.attackTargetID == the player's id (the
target was acquired) AND the player's health DROPS by ATTACK_DAMAGE after the mob reaches melee range (the
melee fired through the Phase-29 keystone). This is the must-have the phase goal demands — not just boot-load.
Plus the boot-load (correct goal set/count) + the focused RNG tests (nearest_attackable nextInt(10), leap
nextInt(5), spider daylight nextInt(100)) — keep the ones 35-05 already wrote that pass.
</action>

<acceptance_criteria>
- All 3 vanilla_<mob>/main.star + their server/assets/ embeds exist + are byte-identical (diff empty each).
- Each hostile .star declares its combat goals via kind= (grep `kind="melee_attack"` etc.); no hand-rolled
  .star combat-RNG body remains in the hostiles (the kind-goals own it).
- TestZombieBehavior / TestSkeletonBehavior / TestSpiderBehavior each assert attackTargetID == player.id
  AND the player takes ATTACK_DAMAGE (real melee through the keystone). All pass.
- CGO_ENABLED=0 go build ./... exit 0; go vet ./server/ clean; full go test ./server/ ok.
- TestPluginPigEqualsGoNativePig byte-identical.
</acceptance_criteria>
</task>

## must_haves (goal-backward)
- A declared hostile ACQUIRES a player target (attackTargetID == player) and DEALS melee damage through the
  Phase-29 keystone (player health drops by ATTACK_DAMAGE) — the real "hunt and attack" goal, test-asserted.
- The combat goals are the Go-native 1:1 jar ports (kind-routed), NOT re-expressed Starlark (no lockstep drift).
- The pig (+ cow/sheep/chicken) oracle stays byte-identical.
- All 3 hostile .star pairs byte-identical.

## verification
- CGO_ENABLED=0 go build ./... + go vet ./server/ + full go test ./server/.
- TestPluginPigEqualsGoNativePig byte-identical; the 3 hostile behavior tests assert real player damage.
- Docker -race deferred to the gate plan (35-06).
