# Phase 35: Hostiles + Spawn Rules (zombie / skeleton / spider) - Context

**Gathered:** 2026-06-30
**Status:** Ready for planning
**Mode:** Auto-generated (user AFK; all bytecode jar-verified in 35-JARNOTES.md; the FORCED light-gate decision pre-made — see Decisions)

<domain>
## Phase Boundary

A survival night exists: zombie/skeleton/spider hunt and attack the player, gated by a faithful
per-category cap and a day/night spawn rule. This is the FIRST phase to introduce the targetSelector
subsystem (combat targeting) — the second goalSelector on a mob, run in jar order, that sets the mob's
attack target which the goalSelector's attack goals consume.

REUSES (proven): the 8-goal passive runtime (Float/Stroll/Look — 30.1–33), the mob-as-plugin registry +
boot-load + /dbg + natural-spawn (34), the mob→entity damage keystone (Phase 29, lastDamageSource/
hasLastDamage from 31). The pig + cow/sheep/chicken stay GREEN (hostiles are separate mobs).

NEW (the real work):
- The targetSelector machinery: a second goalSelector instance per mob, TARGET-flag goal routing,
  attackTargetID thin-id field, jar-order tick (targetSelector.tick BEFORE goalSelector.tick).
- The shared combat/target goals built ONCE for reuse: MeleeAttackGoal, NearestAttackableTargetGoal,
  HurtByTargetGoal (+ the hostile-specific ZombieAttackGoal/SpiderAttackGoal melee subclasses).
- Per-category spawn cap (MONSTER 70 vs CREATURE 10, the /289 divisor) + MONSTER tally in countByCategory
  + checkDespawn port (the cap bounds SPAWNS, not population).
- The day/night light-gate (the FORCED decision — see Decisions).

OUT OF SCOPE (cite-deferred): Skeleton RANGED bow attack (melee-only subset OK for v1 per SC#2 — the
arrow projectile entity is a later phase); the 26.2-new SpearUseGoal (Zombie prio 2 — no weapon system)
+ ZombieAttackTurtleEggGoal; reinforcement spawns (Zombie SPAWN_REINFORCEMENTS_CHANCE); village/door goals
(MoveThroughVillageGoal); sun-avoidance nav (RestrictSunGoal/FleeSunGoal — skeleton, deferrable for v1);
full light-propagation cave spawning (deferred behind the gametime-darkness proxy — see Decisions).
</domain>

<decisions>
## Implementation Decisions (jar-verified — see 35-JARNOTES.md for the verbatim bytecode)

### THE FORCED LIGHT-GATE DECISION (SC#4 — pre-made, AFK-autonomous):
**SHIP the documented gametime-darkness proxy, NOT a real light read.** No light engine exists yet (the
v1 world is "flat stone" superflat with no light propagation — confirmed node_evaluator.go:27 /
path_region.go:29). The faithful Monster.isDarkEnoughToSpawn reads the block+sky light level at the spawn
pos; with no light engine that read is unavailable. So the gate uses WORLD GAMETIME (the day/night cycle
clock the server already ticks): hostiles spawn only when it is "night" by gametime (the dayTime-in-night
window), structured behind a clearly-cited `isDarkEnoughToSpawn` stub that EQUALS the vanilla night-time
default and becomes a real light read when the lighting engine lands. NEVER a silent daylight flood. The
deferral (full light-propagation + cave/block-light spawning) is RECORDED here + in the SUMMARY. This is
the smaller, jar-structured, cited choice matching the project's stub discipline. (If the planner finds a
trivial real sky-light read already available, it MAY take that instead — but the proxy is the accepted default.)

### Goal sets (registerGoals — all CONFIRMED in 35-JARNOTES.md):
- Zombie: goalSelector ZombieAttackGoal@3 + WaterAvoidingRandomStroll@7 + LookAtPlayer@8 + LookAround@8;
  targetSelector HurtByTargetGoal@1 + NearestAttackableTargetGoal<Player>@2 (mustSee). (Spear/turtle-egg/
  village goals cite-deferred.)
- Skeleton (AbstractSkeleton): goalSelector WaterAvoidingRandomStroll@5 + LookAtPlayer@6 + LookAround@6
  (+ MELEE subset for v1 — the bowGoal/meleeGoal swap deferred to ranged); targetSelector HurtByTargetGoal@1
  + NearestAttackableTargetGoal<Player>@2. (RestrictSun/FleeSun/AvoidWolf deferrable.)
- Spider: goalSelector FloatGoal@1 + LeapAtTargetGoal@3 + SpiderAttackGoal@4 + WaterAvoidingRandomStroll@5
  + LookAtPlayer@6 + LookAround@6; targetSelector HurtByTargetGoal@1 + SpiderTargetGoal<Player>@2
  (daylight-gated NearestAttackableTargetGoal subclass). (AvoidArmadillo deferrable.)
All three → MobCategory.MONSTER (categoryOf must add the 3 cases, like the Phase-34 CREATURE fix).

### Attributes (createAttributes — CONFIRMED): Monster.createMonsterAttributes() = Mob + ATTACK_DAMAGE; then
- Zombie: + FOLLOW_RANGE 35.0 + MOVEMENT_SPEED 0.23 + ATTACK_DAMAGE 3.0 + ARMOR 2.0 + SPAWN_REINFORCEMENTS_CHANCE
- Skeleton: + MOVEMENT_SPEED 0.25 (MAX_HEALTH 20 from Mob base)
- Spider: + MAX_HEALTH 16.0 + MOVEMENT_SPEED 0.3
Wire types zombie/skeleton/spider — confirm present in data/entity at phase open (26.2 reorg: monster.zombie.Zombie etc).

### targetSelector machinery (SC#1 — the core new subsystem):
- mobAI gains a second goalSelector instance = the targetSelector. serverAiStep ticks it in jar order:
  sensing → targetSelector.tick → goalSelector.tick → targetSelector.tickRunningGoals(true) →
  goalSelector.tickRunningGoals(true). (ai_mob.go already has a comment seam for this order.)
- buildAIFromDecl routes TARGET-flagged goals (HurtByTarget/NearestAttackableTarget) into the targetSelector,
  the rest into the goalSelector.
- An attackTargetID thin-id field on the entity carries the acquired target (the analog of Mob.getTarget()).
- The shared MeleeAttackGoal / NearestAttackableTargetGoal / HurtByTargetGoal built ONCE for reuse (the wolf
  in Phase 36 reuses them).

### Combat (SC#2): each hostile acquires a player target (NearestAttackableTargetGoal, mustSee LoS scan via
TargetingConditions + FOLLOW_RANGE), paths to it, and deals melee damage through the Phase-29 keystone
(MeleeAttackGoal: in-reach getMeleeAttackRangeSqr → attack cooldown → hurt(attacker, ATTACK_DAMAGE)).

### Spawn gating (SC#3): per-category cap MONSTER 70 / CREATURE 10 (the /289 divisor kept), MONSTER tallied in
countByCategory (categoryOf → MONSTER for the 3), checkDespawn ported (the cap bounds spawns not population —
the natural-spawn loop re-checks counts[categoryMonster] < cap). The day/night proxy gates WHEN monsters spawn.

### RNG / oracle discipline (SC#5): the pig oracle stays GREEN (hostiles are separate mobs, the pig untouched).
The NEW RNG goals (NearestAttackableTargetGoal nextInt(randomInterval) gate; LeapAtTargetGoal nextFloat;
MeleeAttackGoal cooldown) are on the new mobs only — lockstep discipline ONLY where they touch a shared pig
path (they shouldn't). adjustedTickDelay STAYS identity (the Phase-34 RESOLVED analysis carries forward — the
Go driver ticks every tick, so the NearestAttackableTargetGoal randomInterval bound is the full reducedTickDelay-
source value run every tick; CONFIRM the exact ctor value at exec). Per-hostile behavior test + focused RNG tests
(NOT a full byte-identical Go-vs-plugin oracle per hostile — the pig proved the subsystems).
</decisions>

<canonical_refs>
## Canonical References
- `.planning/phases/35-hostiles/35-JARNOTES.md` — the pre-decompiled verbatim bytecode (registerGoals,
  attributes, HurtByTargetGoal/NearestAttackableTargetGoal canUse, the spawn-rule surface) + the OPEN
  exec-time decompile list (MeleeAttackGoal, ZombieAttackGoal, SpiderAttackGoal, LeapAtTargetGoal,
  isDarkEnoughToSpawn, checkMonsterSpawnRules, checkDespawn, NearestAttackableTargetGoal ctor randomInterval).
- `server/mob_category.go` — the Phase-34 categoryOf pattern (add the 3 MONSTER cases the same way).
- `server/spawner.go` / `server/async.go` — countByCategory + the natural-spawn cap re-check (extend for MONSTER).
- `server/ai_mob.go` — the serverAiStep order comment (the targetSelector seam) + the goalSelector to copy.
- `server/ai_goal.go` — the goalSelector port (the targetSelector is a second instance).
- `server/combat_mob.go` / the Phase-29 damage keystone — melee damage application + lastDamageSource (P31).
- Phase 36 (wolf) REUSES this phase's targetSelector + MeleeAttackGoal + NearestAttackableTargetGoal +
  HurtByTargetGoal + LeapAtTargetGoal — build them shared/reusable, not zombie-private.
</canonical_refs>

<specifics>
## Specific Ideas
- The targetSelector = a second goalSelector instance; jar-order tick already commented in ai_mob.go.
- All 3 hostiles → MONSTER (categoryOf, the Phase-34 CREATURE-fix pattern); MONSTER cap 70, /289 divisor.
- Light gate = documented gametime-darkness proxy (FORCED), full light spawning deferred + recorded.
- Skeleton melee-only for v1 (bow/arrow deferred); shared combat goals built once for the wolf to reuse.
- Per-hostile behavior test + focused RNG tests; the pig oracle stays byte-identical (untouched).
</specifics>

<deferred>
## Deferred Ideas
- Skeleton ranged bow + Arrow projectile entity (its own phase); reassessWeaponGoal swap.
- Zombie SpearUseGoal (26.2-new, no weapon system) + ZombieAttackTurtleEggGoal + reinforcement spawns
  (SPAWN_REINFORCEMENTS_CHANCE) + MoveThroughVillageGoal/door-breaking.
- Skeleton RestrictSunGoal/FleeSunGoal (sun-avoidance nav) + AvoidEntityGoal<Wolf>; Spider AvoidEntityGoal<Armadillo>.
- Full light-propagation engine + block-light/cave hostile spawning (behind the gametime-darkness proxy).
- Wither skeleton / cave spider / husk / stray / variants.
</deferred>

---

*Phase: 35-hostiles*
*Context gathered: 2026-06-30 (auto-generated, AFK-autonomous; the FORCED light-gate decision pre-made)*
