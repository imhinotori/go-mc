# Phase 33 — Accepted v5 Deviations (the dogfood gate roll-up)

**Finalized:** 2026-06-30 (Plan 33-05, the gate)
**Mandate:** CLAUDE.md — gameplay is a 1:1 port of the unobfuscated 26.2 jar; the ONLY permitted
deviation is an optimization that provably preserves identical observable gameplay. Anything that
reduces fidelity (a cut, a defer, a stub) is documented HERE — **never silent, never baked away**.
Each entry states: the vanilla behavior (cited to the jar class/method), what v5 ships, WHY, and the
path to full fidelity.

This rolls up every cited cut/defer from Plans 33-01..05 into one register the verifier and Phase 34
can read. Two of these (the same-region breeding cut, the baby eye-height defer) were pre-committed in
the ROADMAP / REQUIREMENTS as accepted v5 deviations; the rest are jar-fidelity corrections + cite-defers
discovered during execution.

---

## 1. SAME-REGION BREEDING CUT (the headline v5 deviation)

**Requirement:** MOB-SUB-09 — "Cross-region breeding ships the same-region cut (full-fidelity
barrier-resolved match documented as deferred)." Pre-accepted in ROADMAP.md (Deferred / Backlog →
v5-deferred: "Barrier-resolved full-fidelity cross-region breeding") and REQUIREMENTS.md MOB-SUB-09.

**Vanilla (jar):** `net.minecraft.world.entity.ai.goal.BreedGoal.getFreePartner` calls
`level.getNearbyEntities(partnerClass, PARTNER_TARGETING, animal, boundingBox.inflate(8.0))` — a
LEVEL-WIDE entity query: it returns every same-class animal within the 8-block inflated AABB regardless
of which internal region owns it. `FollowParentGoal.canUse` likewise does
`level.getEntitiesOfClass(animal.getClass(), boundingBox.inflate(8,4,8))` level-wide. Vanilla has no
"region" concept — the whole level is one entity space.

**v5 ships:** The partner scan (`findFreePartner`, ai_goals_breed.go) and the parent scan
(`findNearestAdultParent`, ai_goals_follow.go) both use the OWNING-region store
`t.cur().entities.near(e.x, e.z, 1)` — they find candidates only within the breeding/following mob's
OWN region. The same cut applies to the FEED path (`handleInteract`/`tryFeedAnimal`, attack_dispatch.go):
a feed targeting a mob in a foreign region is dropped, never an inline foreign-region mutation. So two
pigs whose columns fall in different internal regions will NOT find each other as partners/parents (or
be fed across the boundary) until one wanders into the other's region.

**Why:** Ender's tick model shards the world into regions ticked concurrently (the Leaf-parity
architecture). A goal callback runs on its region's tick goroutine over tick-owned state (TICK-05); it
may only read/mutate its own region's entity store. A cross-region partner scan + the cross-region child
spawn (which mutates BOTH parents' breedAge/inLove and adds the baby + XP orb to a region the goal does
not own) would race the other region's concurrent tick. The faithful full-fidelity path is the
**barrier-queue**: resolve the cross-region match through the same deferred-intent barrier the damage
keystone uses (`region_transfer.go` `queueDamageIntent`), so the foreign-region mutation is applied at a
safe tick boundary. That barrier plumbing for breeding (a cross-region partner-match intent + a
cross-region child-spawn intent) is a non-trivial build deferred past the gate.

**Observable impact:** In the common case (both pigs in the same region — the overwhelming majority of
adjacent-pig breeding, since regions are large and breeding requires the pair to be within ~3 blocks)
breeding is byte-faithful. The cut is only observable at a region SEAM, where two in-love pigs straddling
the boundary will pause until one crosses. The breed itself, once the pair shares a region, is 1:1.

**Path to full fidelity:** Build the cross-region breeding/follow intent on the existing barrier-queue
(`queueDamageIntent` is the precedent): the goal queues a "match/breed with partner id N in region R"
intent; the barrier resolves it at the tick boundary and routes the child-spawn + parent mutations to the
correct owning regions via the documented cross-region store-add discipline. `entitiesNearAcrossRegions`
(region_transfer.go) already provides the level-wide scan half; only the mutation-routing half is deferred.

---

## 2. BABY EYE-HEIGHT DEFER (the half-scale hitbox ships; the eye-height does not)

**Vanilla (jar):** `net.minecraft.world.entity.animal.Pig.BABY_DIMENSIONS =
EntityType.PIG.getDimensions().scale(0.5f).withEyeHeight(0.40625f).withAttachments(...)`. The baby pig's
dimensions are the adult's scaled to 0.5 (0.9×0.9 → 0.45×0.45) AND an explicit eye height of 0.40625.

**v5 ships:** The AABB half-scale (0.45×0.45, derived as adult × `babyDimensionScale` 0.5) IS shipped and
gated (`refreshDimensions`, entity.go; `TestBabyHitboxHalfScale` / the dogfood-gate E2E assert span 0.45
for a baby, 0.9 restored on grow-up). The eye-height 0.40625 is **cite-deferred**: our `Entity` has no
eye-height field today (eye height is used for look/ray-cast origin, NOT for the goal `distanceToSqr`
checks BreedGoal `distSqr<9` and FollowParentGoal `9..256` read — those read the AABB, which IS scaled).

**Why:** The load-bearing input to every breeding/following distance gate is the AABB (shipped, exact).
Eye height is only the look-vector origin; no v5 mob behavior reads a baby's eye height, so adding an
`Entity.eyeHeight` field now would be built-but-unwired speculation (the SC4-gap rule). The 0.40625
constant is recorded here so it slots in the moment an eye-height field lands.

**Path to full fidelity:** When an `Entity.eyeHeight` field is added (e.g. for mob ranged-attack aim or
faithful look targeting), scale it for a baby by `0.40625 / adultEyeHeight` (the same derive-from-adult
discipline used for the AABB scale), cited to `Pig.BABY_DIMENSIONS.withEyeHeight(0.40625f)`. Do NOT
hardcode 0.40625 for non-pig babies — derive per-species like the 0.5 dimension scale.

---

## 3. isPANICKING — the FAITHFUL read shipped (NOT a deviation; documented for completeness)

**Vanilla (jar):** `Mob.isPanicking()` returns whether the mob's running goal set currently contains a
running `PanicGoal` (the breeding/partner scan excludes a panicking partner:
`BreedGoal.getFreePartner` filters `!other.isPanicking()`).

**v5 ships:** `TickLoop.isPanicking(e)` (ai_goals_breed.go) scans the mob's `[]*wrappedGoal` selector for
the `*panicGoal` and returns its `running` bit — the FAITHFUL read, exactly as `Mob.isPanicking` checks
the running PanicGoal. It is NOT a `hurtTime>0` proxy (which would be wrong: hurtTime decays in ~10
ticks, a pig panics for the full flee duration, and hurtTime>0 also fires for non-panic damage —
`TestIsPanicking` explicitly asserts hurtTime does NOT drive it).

**This is NOT a deviation** — the faithful read shipped. The ONLY guard added is a nil-`ai` check: a mob
with no AI (`e.ai == nil`) returns `false` (it cannot be panicking — it has no PanicGoal). That guard is
correct vanilla behavior (a non-AI entity is never panicking), not a fidelity reduction. Recorded here so
the gate's "isPanicking" line item is explicitly accounted for: shipped faithful, no deviation.

---

## 4. PIG VARIANT ASSIGNMENT — cite-deferred (the RNG DRAW ships; the assignment does not)

**Vanilla (jar):** `Animal.spawnChildFromBreeding` → `Pig.getBreedOffspring` draws
`random.nextBoolean()` to pick which parent's `PigVariant` the baby inherits
(`baby.setVariant(nextBoolean() ? this.getVariant() : partner.getVariant())`), THEN
`finalizeSpawnChildFromBreeding` draws the XP orb `1 + random.nextInt(7)`.

**v5 ships:** `TickLoop.breed` (ai_goals_breed.go) draws BOTH RNG values in the **jar's exact order** —
variant `nextBoolean()` FIRST, then XP `1+nextInt(7)` SECOND, both on the breeding initiator's per-mob
RNG (the lockstep contract: the plugin pig routes through the SAME `t.breed` via `try_breed`, so both
halves draw identically). The variant `nextBoolean()` is CONSUMED (so the RNG stream + order stay
byte-identical — the oracle pins the DRAW, not the bit value), but the actual `child.variant = (...)`
assignment is **cite-deferred**: `Entity` has no `variant` field (no PigVariant subsystem exists yet).

**Why:** The observable oracle contract is the RNG draw + its order (which IS shipped, keeping the
9-goal oracle byte-identical when breeding is driven). PigVariant is a cosmetic render attribute (the
baby renders + behaves identically regardless of variant) and is not yet a wire field; assigning it now
would require building a whole variant subsystem unwired. The draw is preserved so adding the assignment
later is a one-line wire at the cited seam.

**Path to full fidelity:** When a `PigVariant` field + its wire metadata land, assign
`child.variant = (variantPick ? e.variant : partner.variant)` at the cited `breed()` seam (the
`nextBoolean()` result is already computed there). Cite `Pig.getBreedOffspring`.

---

## 5. PIG EAT-SOUND — faithful no-op (jar-fidelity CORRECTION, not a reduction)

**Vanilla (jar):** `Pig` does NOT override `Animal.playEatingSound()`, and the base
`Animal.playEatingSound()` is empty (`return`). So feeding a pig emits NO eat sound.

**v5 ships:** The feed path (`tryFeedAnimal`, attack_dispatch.go) performs the `playEatingSound()` call as
a documented no-op for a pig — emitting NO sound packet, exactly as the bytecode does. (The Plan-33-02
plan/JARNOTES had assumed `GENERIC_EAT`; javap corrected it — emitting GENERIC_EAT would be an OBSERVABLE
divergence FROM vanilla.) This is a fidelity-PRESERVING correction, not a cut. The call site is preserved
so a sound-overriding animal (Phase 34, e.g. a cow/sheep that DOES override `playEatingSound`) attaches
its sound there.

---

## 6. IN-LOVE HEART TRANSPORT — EntityEvent-18 (jar-fidelity CORRECTION, not a reduction)

**Vanilla (jar):** On a dedicated server, `Animal.aiStep`'s `inLove%10` heart loop calls
`Level.addParticle`, which is the empty base no-op (`ServerLevel` does NOT override it) — so NO packet is
emitted from the tick tail. The client-facing hearts come from `Animal.setInLove`'s
`level.broadcastEntityEvent(this, (byte)18)`; the CLIENT's `handleEntityEvent(18)` spawns the 7 hearts
locally.

**v5 ships:** `broadcastHearts` (combat_mob.go) sends `ClientboundEntityEvent(id, 18)` to trackers — the
faithful dedicated-server heart path. The 3 `nextGaussian()*0.02` heart-velocity draws in the `inLove%10`
tail are STILL drawn (`emitInLoveHearts`) to keep the mob RNG stream lockstep with vanilla aiStep (dormant
on the un-fed oracle), even though they emit no packet — exactly as the server bytecode does (it draws the
velocities, then `addParticle` discards them). `encodeLevelParticles` (the genuine
`ServerLevel.sendParticles` wire-out) was also built (a real plan deliverable + reusable infra), byte-
tested, but is NOT the heart path. This is a fidelity-PRESERVING correction, not a cut.

---

## 7. Age / InLove NBT PERSIST — deferred (a known partial gap, not load-bearing for the gate)

**Vanilla (jar):** `AgeableMob.addAdditionalSaveData` writes `"Age"` (int) and `"ForcedAge"`;
`Animal.addAdditionalSaveData` writes `"InLove"` (int) + the love-cause UUID. On reload, a baby/in-love
pig restores its age/love state.

**v5 ships:** The entity-region Anvil IO infrastructure EXISTS (`saveEntities`/`loadEntities`,
persistence.go) but there is NO live `Entity → save.Entities` serialization that writes the mob-specific
`breedAge`/`inLove` fields into NBT — only `Pos`/`Motion` round-trip. So a baby or in-love pig that is
saved + reloaded comes back as an adult at age 0 / not-in-love.

**Why:** This needs NEW wiring (a live entityStore → `save.Entities` snapshot emitting the `Age`/`InLove`
int tags), not a copy of an existing pattern — and it is NOT load-bearing for the gate: the oracle is
in-memory, and the live bot dogfood spawns fresh pigs without a server restart. Per the no-built-but-
unwired rule, the persist wiring is deferred until a path requires a mob to survive a save/load cycle with
its age/love intact.

**Path to full fidelity:** Add an `Entity → save.Entities` snapshot in the chunk-unload/save path that
emits `Age` (=breedAge), `ForcedAge`, and `InLove` (=inLove) int NBT tags (cite
`AgeableMob.addAdditionalSaveData` + `Animal.addAdditionalSaveData`), wired symmetrically into
`loadEntities`. This is its own follow-up (a persistence plan), independent of the mob-AI track.

---

## Summary table

| # | Deviation | Vanilla | v5 ships | Kind | Path to fidelity |
|---|-----------|---------|----------|------|------------------|
| 1 | Same-region breeding | level-wide partner/parent scan | owning-region scan only | **CUT** (pre-accepted) | barrier-queue cross-region intent |
| 2 | Baby eye-height | 0.40625 + 0.45 AABB | 0.45 AABB only (eye-height deferred) | **DEFER** (pre-accepted) | scale 0.40625 when an eye-height field lands |
| 3 | isPanicking | running-PanicGoal read | running-PanicGoal read (+nil-ai guard) | **faithful (no deviation)** | n/a |
| 4 | Pig variant | nextBoolean() pick + assign | draw shipped, assign deferred | **DEFER (draw preserved)** | assign when a variant field lands |
| 5 | Pig eat-sound | empty no-op | empty no-op | **faithful (correction)** | n/a (sound-overriding animal attaches in P34) |
| 6 | In-love heart wire | EntityEvent-18 | EntityEvent-18 | **faithful (correction)** | n/a |
| 7 | Age/InLove NBT | persisted | not persisted | **DEFER** | add the live Entity→NBT snapshot (persistence plan) |

The only true fidelity REDUCTIONS are #1 (same-region cut — pre-accepted), #2 (eye-height — pre-accepted,
not goal-load-bearing), #4 (variant assign — draw preserved so the oracle is unaffected), and #7 (NBT
persist — not gate-load-bearing). #3/#5/#6 are faithful ports (recorded so the gate's line items are not
silent). None reduce the OBSERVABLE breeding/aging/follow dogfood the gate proves.

## Code-review additions (33-REVIEW.md — cited, masked by the single-candidate dormant oracle)

| # | Item | Vanilla | Sulfur | Kind | Upgrade path |
|---|------|---------|--------|------|--------------|
| 8 | BreedGoal/FollowParentGoal partner-cache | Go-native CACHES the partner/parent from canUse | the `.star` goals RE-SCAN every tick (host scan) | **divergence (masked)** | route the `.star` goals through a cached partner handle, OR have the Go-native re-scan too; observable only with MULTIPLE candidate partners/parents in range (the oracle + scenario tests use a single candidate, so byte-identical there). WR-01/WR-03. |
| 9 | Breed XP orb count | `finalizeSpawnChildFromBreeding` spawns exactly ONE orb of value 1+nextInt(7) | routes through `awardExperienceOrbs` (the death path) which may SPLIT into multiple orbs summing the value | **divergence (RNG-neutral)** | give breed() a single-orb spawn (not the splitting death path); the RNG draw (1+nextInt(7)) + its order are already correct, only the orb COUNT differs. Lockstep-safe (same draw on both halves). WR-02. |
| 10 | Bred-baby DATA_BABY_ID metadata | child carries the baby flag in its synched data from the first AddEntity | **FIXED (CR-01)**: breed() now splices babyDataEntry onto child.metadata after setting breedAge, so late-trackers render it small | **faithful (fix)** | n/a — fixed + regression-tested (TestBreed asserts the baby metadata carries the entry) |

#8/#9 are observable only in multi-candidate / orb-count edge cases the dogfood gate does not exercise;
they are RNG-lockstep-safe (the oracle stays byte-identical) and recorded here so they are CITED, not
silent, per the CLAUDE.md mandate. #10 (CR-01) was a real wire-render-flag break for late-trackers —
FIXED in this pass, not deferred.
