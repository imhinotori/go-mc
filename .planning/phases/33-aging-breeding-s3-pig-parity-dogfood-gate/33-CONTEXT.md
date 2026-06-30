# Phase 33: Aging + Breeding (S3) — Pig Parity / DOGFOOD GATE - Context

**Gathered:** 2026-06-30
**Status:** Ready for planning
**Mode:** Auto-generated (user AFK; ALL bytecode pre-decompiled + jar-verified — see 33-JARNOTES.md for the full BreedGoal/Animal/AgeableMob/FollowParentGoal disassembly)

<domain>
## Phase Boundary — THE HARD GATE

Build the S3 aging + breeding subsystem AND wire the pig's last two deferred goals (BreedGoal@3,
FollowParentGoal@5), closing the FULL 8-goal pig {0,1,3,4,4,5,6,7,8}. This PROVES S1–S4 are 1:1 (the
8-goal pig oracle byte-identical) before ANY new mob (Phase 34) reuses them. **HARD GATE: the dogfood
must be green before Phase 34.**

Deliverables:
1. **AgeableMob aging**: a breed-age int (baby <0 → ticks up to 0 = adult; adult >0 = breeding cooldown
   ticking down; ==0 adult-ready), isBaby = age<0, the per-tick ageUp, the DATA_BABY_ID wire metadata
   (the baby renders small client-side).
2. **Animal in-love**: inLove int (setInLove → 600; the aiStep decrement adult-only + the `inLove%10==0`
   heart-particle emit; canFallInLove = inLove<=0; isInLove = inLove>0), NBT InLove + Age persist.
3. **isFood + the FEED interact path**: Pig.isFood(stack) = itemInTag(stack, "pig_food") (REUSE Phase 32).
   Wire the right-click-with-food path (handleInteract, attack_dispatch.go:725 — currently a v1 no-op):
   feed an ADULT (age==0 && canFallInLove) → usePlayerItem (shrink 1) + setInLove; feed a BABY (age<0) →
   age-up (ageUp toward 0). VERIFY the exact mobInteract bytecode (Animal.mobInteract / AgeableMob).
4. **BreedGoal@3 (speed 1.0)**: isInLove gate → getFreePartner (nearby same-class, canMate, !panicking) →
   navigate to partner → at loveTime>=adjustedTickDelay(60) && distSqr<9 → breed() = spawnChild via
   spawnDeclaredMob (baby age) + both parents setAge(6000) cooldown + reset inLove + XP orb + hearts.
5. **FollowParentGoal@5 (speed 1.1)**: a BABY (age<0) follows the nearest ADULT same-class within
   inflate(8,4,8), 3..16 blocks; re-path every adjustedTickDelay(10). EMPTY flags (no MOVE/LOOK lock —
   verify the selector handles an unflagged goal). NO RNG.

OUT OF SCOPE: cow/sheep/chicken (Phase 34 — they reuse this); golden-dandelion age-lock; the breeding
cooldown particle minutiae beyond hearts; non-pig isFood.
</domain>

<decisions>
## Implementation Decisions (jar-verified — see 33-JARNOTES.md for the verbatim bytecode of all 4 goals + Animal + AgeableMob)

### THE ORACLE / DOGFOOD-GATE STRATEGY (the make-or-break)
After this phase the pig has all 8 goals. `TestPluginPigEqualsGoNativePig` drives a Go-native pig vs the
plugin pig byte-identical. The new goals draw RNG ONLY in narrow conditions:
- **BreedGoal**: canUse gated `isInLove()` (inLove>0). The oracle pig is never fed → inLove==0 →
  canUse false on line 1 → ZERO draws. breed()'s child-spawn RNG only fires mid-breeding. KEEP the
  oracle pig un-fed → BreedGoal dormant → zero draws.
- **FollowParentGoal**: NO RNG at all (entity scan + int recalc timer). canUse gated `age<0` (baby). The
  oracle pig is an ADULT (age 0) → canUse false → zero effect. Even if it fired, no RNG.
- **Aging tick**: the per-tick ageUp is PURE INT (age += sign) — NO RNG. Adding it to baseTick perturbs
  NOTHING in the RNG stream. BUT it is STATEFUL — the oracle pig's age must be IDENTICAL on both halves
  (both start age 0 adult). Confirm the age field initializes identically (0) on Go-native + plugin spawn.
- **inLove decrement**: pure int, adult-only, only when inLove>0 → dormant on the un-fed oracle pig.
- **Heart particles** (`inLove%10==0`): a broadcast, NOT an RNG draw — but only when inLove>0 (dormant).
RULE: add EVERY new goal + the aging/inLove tick to BOTH the Go newPigAI/baseTick AND both byte-identical
vanilla_pig/main.star copies IN LOCKSTEP. The oracle pig stays an un-fed lone adult → Breed/Follow dormant,
aging is a pure-int no-op on a stable age-0 → byte-identical. GATE ON THE ORACLE after every change.

### THIS IS A MULTI-PLAN PHASE — let the planner split it
The scope is large (a whole subsystem + 2 goals + the feed path + wire metadata + the gate). Suggested split
(the planner decides via the source-audit):
- **Plan A — Aging + AgeableMob**: the age int + isBaby + ageUp tick + DATA_BABY_ID wire metadata + Age NBT.
- **Plan B — Animal in-love + feed**: inLove int + setInLove/canFallInLove/isInLove + the aiStep decrement +
  hearts + isFood(pig_food) + the handleInteract FEED path (adult→love, baby→age-up) + InLove NBT.
- **Plan C — BreedGoal@3 + FollowParentGoal@5 + breed()/spawnChild + canMate/getFreePartner**, lockstep both
  pigs, closing the 8-goal set + updating the goal-count tests (5→... wait, 7→9? the pig is currently 7
  goals {0,1,4,4,6,7,8}; adding @3 + @5 → 9 goals {0,1,3,4,4,5,6,7,8}). Update ALL FOUR goal-count tests.
- **Plan D (the GATE)**: the full 8-goal oracle byte-identical + breeding/aging/follow scenario tests +
  -race + the LIVE bot breeding test (feed two pigs, watch a baby spawn).
Each plan keeps the oracle green (it stays dormant). Sequential waves (the goals depend on the subsystem).

### Key jar values (from 33-JARNOTES.md — port verbatim)
- BreedGoal: PARTNER_TARGETING range 8.0; canUse isInLove→getFreePartner; canContinueToUse partner alive &&
  inLove && loveTime<60 && !panicking; tick lookAt+moveTo(1.0)+ ++loveTime, breed at loveTime>=adjustedTickDelay(60)
  && distSqr<9.0; getFreePartner: nearby same-class in inflate(8.0), canMate && !panicking, nearest. priority 3, speed 1.0, {MOVE,LOOK}.
- FollowParentGoal: canUse age<0 → nearest ADULT (age>=0) in inflate(8,4,8), not if <9 (DONT_FOLLOW<3²); continue age<0 && parent alive && 9<distSqr<256; tick recalc every adjustedTickDelay(10), moveTo(parent,1.1). priority 5, speed 1.1, EMPTY flags. NO RNG.
- Animal: inLove setInLove→600; aiStep `if age!=0 inLove=0; if inLove>0 {--inLove; if %10==0 hearts}`; canFallInLove inLove<=0; canMate = other!=this && sameClass && both isInLove.
- AgeableMob: BABY_START_AGE -24000; age<0 baby (→0), age>0 cooldown (→0), age==0 adult; isBaby age<0; finalizeSpawnChildFromBreeding child setAge(-24000), parents setAge(6000); DATA_BABY_ID synched bool.
- Pig.registerGoals (confirmed): BreedGoal@3(1.0), FollowParentGoal@5(1.1). Pig.isFood = is(PIG_FOOD).

### Wire / interact surfaces (reuse)
- `server/entity_encode.go` entityDataEntry/SetEntityData (the DATA_BABY_ID bool metadata — find the next free accessor index for the pig/AgeableMob; the baby flag makes the client render a small pig).
- `server/attack_dispatch.go:725 handleInteract` — the v1 no-op right-click; wire the FEED path here (read the player's held item, if isFood + adult → setInLove, if baby → age-up; consume 1 via shrinkHeldItem).
- `server/plugin_mob_decl.go:385 spawnDeclaredMob` — breed() spawns the child here (set its age to BABY_START_AGE after spawn).
- `server/ai_mob.go` newPigAI (add BreedGoal@3 + FollowParentGoal@5); baseTick (add the aging + inLove decrement). The serverAiStep/setWantTarget nav seam for the goals' moveTo(partner/parent).
- `server/ai_goal.go` the selector (verify an EMPTY-flag goal like FollowParentGoal is handled — it locks no flag).
- Phase 32: itemInTag(id, "pig_food") = Pig.isFood; the Carrot in the test kit (slot 38) for the live feed test.
- xp_orb.go (the breed XP orb), the heart-particle broadcast (find the particle emit seam).
</decisions>

<code_context>
## Existing surfaces
- `server/entity.go` — the Entity struct (add `breedAge int` + `inLove int`; NOTE the existing `age int` is ItemEntity/XP-orb ticks-since-spawn — DIFFERENT, do NOT reuse it; name the new one breedAge or animalAge to avoid the collision).
- `server/entity_encode.go:182-250` — entityDataEntry + the metadata wire (DATA_ITEM is the model; add a DATA_BABY_ID bool accessor for the pig).
- `server/attack_dispatch.go:717-727` — handleInteract (the feed path goes here).
- `server/inventory.go` shrinkHeldItem / heldWindowSlot — consume the fed item.
- `server/plugin_mob_decl.go:385` spawnDeclaredMob — the breed child spawn.
- `server/ai_mob.go` — newPigAI, baseTick, serverAiStep.
- `server/ai_goal.go` — WrappedGoal selector (empty-flag handling for FollowParentGoal).
- `server/combat_mob.go` baseTick (where i-frame/hurtTime decrement — the aging + inLove decrement go here, mirror LivingEntity.aiStep/Animal.aiStep tail).
- data/tag ItemTags["pig_food"], itemInTag (Phase 32). data/item Carrot (test kit).
- The plugin: plugin_entity.go handles (add is_in_love/is_baby/age handles if the .star goals need them; OR host-compute like tempt — the breed/follow scans are host-side). Both vanilla_pig/main.star copies (lockstep).
- `server/plugin_pig_test.go` + ai_mob_test.go — the FOUR goal-count tests (7→9 goals {0,1,3,4,4,5,6,7,8}).

## Verification
- `CGO_ENABLED=0 go build ./...` + `go vet ./server/` (gopls STALE).
- `CGO_ENABLED=0 go test ./server/ -run 'TestBreed|TestFollowParent|TestAging|TestInLove|TestPluginPigEqualsGoNativePig|TestPluginPigBootLoads|TestVanillaPig|TestPigGoalSet|TestTempt|TestPanic|TestFloat|TestPigStrolls' -v`.
- NEW tests: aging (baby ticks to adult), feed-sets-love (adult), feed-ages-up (baby), breed (two in-love pigs → a baby spawns + cooldown + inLove reset), follow-parent (baby paths to adult). The 8-goal oracle byte-identical (the dogfood gate). boot-load 9 goals.
- Docker -race; oracle GREEN (the gate).
- LIVE bot: spawn 2 pigs, feed both carrots (right-click with carrot held), watch a 3rd (baby) pig appear in the entity table; a baby pig follows an adult. (The bot's UseItemOn/Interact + carrot in slot 38.)
- All jar bytecode in 33-JARNOTES.md — verify each before writing.
</code_context>

<specifics>
## Specific Ideas
- breedAge/animalAge int on Entity (NOT the existing ItemEntity age). isBaby = breedAge < 0. DATA_BABY_ID bool wire.
- Feed path in handleInteract: isFood(pig_food) → adult: setInLove(600)+consume; baby: ageUp+consume.
- BreedGoal@3, FollowParentGoal@5 — the LAST two pig goals → 9-goal pig, the FULL oracle.
- Multi-plan; oracle stays green by keeping the gate-test pig an un-fed lone adult (Breed/Follow dormant, aging a pure-int no-op).
- Live breeding test via the bot (feed 2 pigs → baby spawns).
</specifics>

<deferred>
## Deferred Ideas
- golden-dandelion age-lock; the canScare TemptGoal flee branch (still N/A for the pig).
- canContinueToUse stuck-timeout hardening (ongoing carryover).
- Non-pig isFood / other animals' breeding (Phase 34 reuses this subsystem).
</deferred>
