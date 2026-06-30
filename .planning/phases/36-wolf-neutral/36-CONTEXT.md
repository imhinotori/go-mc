# Phase 36: Wolf (Neutral/Tameable) - Context

**Gathered:** 2026-06-30
**Status:** Ready for planning
**Mode:** Auto-generated (user AFK; all bytecode jar-verified in 36-JARNOTES.md; scope decisions pre-made — see Decisions)

<domain>
## Phase Boundary

The flagship NEUTRAL mob: a wolf that lives wild, defends itself, and (second pass) can be tamed, sit,
and anger on hit. The most complex single mob — built LAST. It REUSES everything proven before:
- The passive 8-goal runtime (Float/Panic/Stroll/Look — 30.1–33) + the mob-as-plugin registry/boot-load/
  /dbg/natural-spawn (34) + the goal-kind seam (35-01b).
- The targetSelector + combat goals from Phase 35: NearestAttackableTargetGoal, HurtByTargetGoal,
  MeleeAttackGoal, LeapAtTargetGoal — all built once for reuse, the wolf consumes them via kind=.
- The mob→entity damage keystone (Phase 29) + lastHurtByMob/lastDamageSource (31/35) for anger-on-hit.

NEW (the real work) — the wolf is the first TAMEABLE + NEUTRAL mob:
- TAME/OWNER state (TamableAnimal): tame flag, owner UUID, setTame/setOwner/isOwnedBy/isTame.
- SIT: SitWhenOrderedToGoal + isOrderedToSit/isInSittingPose (a sitting wolf parks).
- The NEUTRAL anger model (NeutralMob): a wolf hit by a player becomes angry for a random timer
  (the persistent-anger subsystem), the pack alerts, and isAngryAt gates the player-target goal.
- Taming interaction (Wolf.mobInteract): BONE → tryToTame (nextInt(3) 1-in-3 chance) → setTame.
- Owner-defense goals: FollowOwnerGoal + OwnerHurtByTargetGoal + OwnerHurtTargetGoal.

OUT OF SCOPE (cite-deferred, per the no-subsystem-yet rule): wolf body-ARMOR (equip/repair — no equipment
system), COLLAR DYE (no DyeColor component on the wolf), wolf VARIANTS (biome textures), BegGoal head-tilt
(client visual), WolfAvoidEntityGoal<Llama> / NonTameRandomTargetGoal prey-hunting (needs llama/prey mobs
not in v1 — the untamed-hunts-animals behavior defers with its target mobs), the breed second pass beyond
the shared BreedGoal. The CORE must-have: wild goals + tame(BONE+nextInt(3)) + sit + follow-owner +
owner-defense + neutral anger-on-hit + the wolf melee combat.
</domain>

<decisions>
## Implementation Decisions (jar-verified — see 36-JARNOTES.md for the verbatim bytecode)

### HARD DEPENDENCY on Phase 35 (DONE): the targetSelector + the combat goals (NearestAttackableTargetGoal,
### HurtByTargetGoal, MeleeAttackGoal, LeapAtTargetGoal) + the goal-kind seam are all BUILT + proven (35-01b
### closed the wiring gap; all 3 hostiles deal real damage). The wolf reuses them via kind= — zero re-port.

### Wolf.registerGoals (CONFIRMED in 36-JARNOTES) — the SCOPED v1 subset:
| prio | goal | v1 status |
|------|------|-----------|
| 1 | FloatGoal | reuse (.star or kind="float") |
| 1 | TamableAnimalPanicGoal(1.5) | port (PanicGoal variant; or reuse panic) |
| 2 | SitWhenOrderedToGoal | NEW (sit subsystem) |
| 3 | WolfAvoidEntityGoal<Llama> | DEFER (no llama) |
| 4 | LeapAtTargetGoal(0.4) | reuse (kind="leap_at_target") |
| 5 | MeleeAttackGoal(1.0) | reuse (kind="melee_attack") |
| 6 | FollowOwnerGoal(1.0,10,2) | NEW (owner subsystem) |
| 7 | BreedGoal(1.0) | reuse (Phase 33) |
| 8 | WaterAvoidingRandomStrollGoal(1.0) | reuse |
| 9 | BegGoal(8.0) | DEFER (client visual) |
| 10 | LookAtPlayerGoal(8.0) + RandomLookAroundGoal | reuse |
targetSelector:
| 1 | OwnerHurtByTargetGoal | NEW (owner-defense) |
| 2 | OwnerHurtTargetGoal | NEW |
| 3 | HurtByTargetGoal | reuse (kind="hurt_by_target") |
| 4 | NearestAttackableTargetGoal<Player>(isAngryAt) | reuse + the anger gate |
| 5,6 | NonTameRandomTargetGoal<Animal/Turtle> | DEFER (prey mobs) |
| 7 | NearestAttackableTargetGoal<AbstractSkeleton> | reuse (wolves attack skeletons — skeleton exists from 35!) |
| 8 | ResetUniversalAngerTargetGoal | NEW (anger timer reset) |

### Attributes (Wolf.createAttributes — CONFIRMED): Animal base + MOVEMENT_SPEED 0.3 + MAX_HEALTH 8.0 +
### ATTACK_DAMAGE 4.0. (Tamed wolves get MAX_HEALTH 40 via setTame — port the bump; verify at exec.)
### Wire type wolf — confirm present in data/entity (animal.wolf.Wolf). categoryOf → CREATURE.

### TAMING (Wolf.mobInteract + tryToTame — CONFIRMED, the RNG path):
- isFood = is(WOLF_FOOD). Untamed + held BONE + !isAngry → consume bone → tryToTame:
  tryToTame: `if (random.nextInt(3) == 0) { tame(player); setOrderedToSit(true); broadcastEntityEvent(7) }
  else broadcastEntityEvent(6)` — 1-in-3 tame chance, hearts(7)/smoke(6) via EntityEvent (the pig-breed
  hearts mechanism). tame() = setTame(true) + setOwner(player). RNG: ONE nextInt(3) per BONE feed (mob stream).
- Tamed + isFood + hurt → heal-feed (deferred-ok). Sit-toggle on tamed owner empty-hand interact.

### The NEUTRAL anger model (NeutralMob — the "neutral" behavior):
- remainingPersistentAngerTime + persistentAngerTarget UUID + getAngryAt/isAngryAt/startPersistentAngerTimer.
- A wolf hit by a player (HurtByTargetGoal reads lastHurtByMob from Phase 31/35) becomes angry for a random
  timer = UniformInt(20s, 39s).sample(random) = nextInt over the range (RNG — lockstep). isAngryAt gates the
  NearestAttackableTargetGoal<Player>. ResetUniversalAngerTargetGoal counts the timer down. The pack-alert
  (setAlertOthers) is a HurtByTargetGoal feature. Decompile NeutralMob.startPersistentAngerTimer at exec.

### SIT + OWNER (TamableAnimal): DATA flags (tame bit + sitting bit) + owner UUID (DATA_OWNERUUID_ID).
### SitWhenOrderedToGoal parks a sitting wolf (claims MOVE/JUMP). FollowOwnerGoal teleports/paths to the owner.
### OwnerHurtByTargetGoal/OwnerHurtTargetGoal: the wolf targets whoever hurt its owner / whoever its owner attacks.
### Decompile TamableAnimal (defineSynchedData indices), SitWhenOrderedToGoal, FollowOwnerGoal, the OwnerHurt
### goals at exec — these are the NEW machinery (analogous to how 35-01 built the targetSelector machinery).

### RNG / oracle discipline:
The PIG oracle stays byte-identical (the wolf is a separate mob; pig untouched). The NEW wolf RNG (tryToTame
nextInt(3), the anger-timer UniformInt sample) is wolf-only. A per-mob behavior test + focused RNG tests
(tame nextInt(3), anger timer) — NOT a full Go-vs-plugin oracle (the pig proved the subsystems). The wolf's
combat goals are the kind-routed Go-native goals (lockstep already proven in 35). Bot live-verify: a wolf
tames on BONE (eventually, the 1/3 chance), sits, follows; a hit wild wolf attacks back.
</decisions>

<canonical_refs>
## Canonical References
- `.planning/phases/36-wolf-neutral/36-JARNOTES.md` — the pre-decompiled verbatim bytecode (registerGoals,
  attributes, mobInteract taming, tryToTame nextInt(3), the new-subsystem inventory) + the OPEN exec-time list.
- `server/plugin_mob_ai.go` (buildAIFromDecl + the goal-kind seam from 35-01b) — the wolf declares its combat
  goals by kind=; add new kinds (sit/follow_owner/owner_hurt) OR new .star-callback goals for the wolf-specific ones.
- `server/ai_goals_target.go` + `ai_goals_attack.go` (the Phase-35 combat goals the wolf reuses).
- `server/mob_category.go` (categoryOf — add wolf → CREATURE, the Phase-34/35 pattern).
- `server/combat_mob.go` + lastHurtByMob (Phase 31/35) — the anger-on-hit read.
- plugins/vanilla_cow/main.star (the passive .star template) + the Phase-35 vanilla_zombie (the combat-kind .star template).
- `server/entity.go` (the entity state — add tame/owner/sit/anger fields, the Phase-33/35 field pattern).
- `server/attack_dispatch.go` handleInteract (add the wolf taming path, the cow-milk/sheep-shear sibling).
- level/attribute/defaults.go — the wolf attribute supplier (ROADMAP SC#1 names it as "the one missing supplier" — verify/add).
</canonical_refs>

<specifics>
## Specific Ideas
- Wolf reuses the 35 targetSelector + combat goals via kind=; the NEW work is tame/owner/sit/anger.
- tryToTame nextInt(3) 1-in-3 + hearts/smoke EntityEvent (the pig-breed mechanism); tame = setTame+setOwner.
- Neutral anger: hit → UniformInt(20s,39s) timer → isAngryAt gates the player-target goal; pack-alert.
- Wolves attack skeletons (NearestAttackableTargetGoal<AbstractSkeleton> — skeleton EXISTS from Phase 35).
- categoryOf wolf → CREATURE; the wolf attribute supplier in defaults.go (SC#1's "one missing supplier").
- DEFER (cited): armor, collar dye, variants, beg visual, llama-avoid, prey-hunting (no prey/llama mobs).
</specifics>

<deferred>
## Deferred Ideas
- Wolf body-armor (equip/repair) + collar dye + biome variants — need equipment/dye/variant subsystems.
- BegGoal head-tilt (client visual); WolfAvoidEntityGoal<Llama> + NonTameRandomTargetGoal prey-hunting
  (need the llama + prey mobs, not in v1 — defer WITH their target mobs).
- The heal-feed (tamed wolf + WOLF_FOOD restores health) — minor, deferrable.
- A full Go-vs-plugin oracle for the wolf (vs a per-mob behavior test) — the pig proved the subsystems.
</deferred>

---

*Phase: 36-wolf-neutral*
*Context gathered: 2026-06-30 (auto-generated, AFK-autonomous; scope + the wild/tame split pre-decided)*
