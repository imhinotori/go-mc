---
phase: 36-wolf-neutral
plan: 02
subsystem: mob-interact
tags: [wolf, tamable, taming, bone, sit-toggle, owner, attribute, entity-event, go]

# Dependency graph
requires:
  - phase: 36-wolf-neutral (plan 01)
    provides: "the wolf TamableAnimal/NeutralMob state (tame/orderedToSit/inSittingPose/ownerUUID/angerEndTime/angerTarget), the DATA_FLAGS index-18 wolfFlagsByte/wolfFlagsDataEntry broadcast helpers, wolfSupplier (MAX_HEALTH 8.0 base), the entity.Wolf base_type + categoryCreature, mobRandom per-entity rng"
  - phase: 34-passive-extras
    provides: "the tryMilkCow / trySheepShear handleInteract sibling pattern + the broadcastHearts EntityEvent seam"
  - phase: 33-passive-breed
    provides: "tryFeedAnimal (Animal.mobInteract feed/breed == super.mobInteract) + the held-item server-side read"
provides:
  - "tryWolfInteract: the Wolf.mobInteract port (UNTAMED BONE-tame branch + TAMED owner empty-hand sit-toggle), wolf-gated into handleInteract before tryFeedAnimal"
  - "tryToTameWolf: the Wolf.tryToTame port — ONE mobRandom(e).nextInt(3); ==0 -> tame + applyTamingSideEffects + setOrderedToSit(true) + DATA_FLAGS flip + hearts(7); else smoke(6)"
  - "applyWolfTamingSideEffects: the Wolf.applyTamingSideEffects port — getAttribute(MAX_HEALTH).setBaseValue(40.0) + setHealth(40.0) on tame (8->40), setBaseValue(8.0) on untame"
  - "wolfIsAngry: the NeutralMob.isAngry gametime-endpoint helper (factored for the BONE-tame !isAngry gate)"
  - "entityEventWolfTameHearts(7)/entityEventWolfTameSmoke(6) EntityEvent status consts"
affects: [36-04 (the wolf gate/behavior tests can assert taming end-to-end)]

# Tech tracking
tech-stack:
  added: []  # no new Go dependency (go.mod / go.sum unchanged)
  patterns:
    - "Interact-sibling discipline: a held-item-gated, mob-gated tryXInteract that returns true ONLY when it consumes the interact (else falls through to tryFeedAnimal == super.mobInteract) — the tryMilkCow/trySheepShear shape extended to the wolf"
    - "Attribute base-value mutation at runtime via attributes.GetInstance(MaxHealth.Name()).SetBaseValue — the getAttribute(MAX_HEALTH).setBaseValue(d) port (the first runtime attribute override outside the spawn supplier)"
    - "Owner-gated command (T-36-05): the sit-toggle is ownerUUID == p.entityID gated; a non-owner cannot command a tamed wolf"

key-files:
  created:
    - server/wolf_taming_test.go
  modified:
    - server/attack_dispatch.go

key-decisions:
  - "tryWolfInteract lives ENTIRELY in attack_dispatch.go (the only Go production edit), per the wave-2 disjointness gate — setTame/setOwner/applyTamingSideEffects/isAngry are inlined as attack_dispatch.go-local helpers (applyWolfTamingSideEffects, wolfIsAngry, tryToTameWolf), NOT new *Entity methods, keeping the edit confined and 36-03 (.star) untouched"
  - "The TAMED branch models ONLY the sit-toggle (the dye/armor/heal-feed branches DEFERRED+CITED — no equipment/dye/health-feed system); r.consumesAction() is modeled by the held-item check (WOLF_FOOD in hand -> the feed branch consumes -> no sit-toggle -> fall through to tryFeedAnimal)"
  - "The hearts(7)/smoke(6) + the DATA_FLAGS flip ride the existing encodeEntityEvent / encodeSetEntityDataByID tracker fan-out (broadcastToTrackers), NOT new wire code — the broadcastHearts/setWolfInSittingPose precedent"

patterns-established:
  - "Jar-verify-before-write: every ported value (40.0d/40.0f bump, nextInt(3), status bytes 7/6, the consumesAction||!isOwnedBy tail, tame()=setTame(true,true)+setOwner) was confirmed via javap -c -p against temp/cache/26.2-inner.jar THIS session before the Go was written"

requirements-completed: [MOB-NEUT-01, MOB-NEUT-02]

# Metrics
duration: 30min
completed: 2026-06-30
---

# Phase 36 Plan 02: Wolf Taming Interact Summary

**The player-facing wolf taming half of MOB-NEUT-02: Wolf.mobInteract + tryToTame ported 1:1 into handleInteract as the wolf-gated `tryWolfInteract` sibling of tryMilkCow/trySheepShear — a BONE on an untamed non-angry wolf draws ONE nextInt(3) (1-in-3 tame), and on success sets isTame + bumps MAX_HEALTH 8->40 + full-heals + sets owner + setOrderedToSit(true) + broadcasts the DATA_FLAGS flip and the hearts EntityEvent (smoke on fail); a tamed wolf sit-toggles on its owner's empty-hand right-click. The pig oracle is byte-identical.**

## Performance

- **Duration:** ~30 min
- **Tasks:** 1/1 (+ the deterministic test suite)
- **Files modified:** 2 (1 created, 1 modified)
- **Commits:** 21bd0320 (Task 1 port), 44061b7a (the test suite)

## Accomplishments

### Task 1 — tryWolfInteract + tryToTameWolf + applyWolfTamingSideEffects + sit-toggle (commit 21bd0320)

**`server/attack_dispatch.go`** (the ONLY Go production edit — wave-2 disjointness preserved):

- **`tryWolfInteract(p, mob) bool`** — the `net.minecraft.world.entity.animal.wolf.Wolf.mobInteract` port. The held item is read SERVER-side (`inv.get(heldWindowSlot(inv.heldSlot))` — the tryMilkCow precedent, T-36-04 never from the packet).
  - **TAMED branch** (`mob.tame`): the dye/body-armor/armor-repair/heal-feed branches are DEFERRED+CITED (no equipment/dye/health-feed system). The load-bearing v1 path is the OWNER empty-hand sit-toggle: `if (r.consumesAction() || !isOwnedBy(player)) return r;` is modeled as — WOLF_FOOD in hand (the feed branch would consume) OR a non-owner -> return false (fall through to tryFeedAnimal); else `setOrderedToSit(!isOrderedToSit())` + `jumping=false` + `navigation.stop()` + `setTarget(null)` -> return true (SUCCESS.withoutItem()).
  - **UNTAMED branch**: `!stack.is(Items.BONE) || isAngry()` -> false (fall through to tryFeedAnimal == super.mobInteract); else `stack.consume(1)` (shrinkHeldItem) -> tryToTameWolf -> return true (SUCCESS_SERVER).
- **`tryToTameWolf(p, mob)`** — the `Wolf.tryToTame` port. ONE `mobRandom(mob).nextInt(3)`. On `==0`: `mob.tame = true` -> `applyWolfTamingSideEffects` -> `mob.ownerUUID = p.entityID` (tame() = setTame(true,true)+setOwner) -> `setTarget(0)` (navigation.stop+setTarget(null)) -> `mob.orderedToSit = true` -> broadcast the DATA_FLAGS flip (wolfFlagsDataEntry/encodeSetEntityDataByID) + the hearts EntityEvent(7). On `!=0`: broadcast the smoke EntityEvent(6).
- **`applyWolfTamingSideEffects(mob)`** — the `Wolf.applyTamingSideEffects` port: `mob.tame` -> `attributes.GetInstance(MaxHealth.Name()).SetBaseValue(40.0)` + `mob.health = 40.0`; else `SetBaseValue(8.0)`.
- **`wolfIsAngry(mob)`** — the `NeutralMob.isAngry` gametime-endpoint (`angerEndTime>0 && angerEndTime-gametime>0`), factored for the BONE-tame `!isAngry()` gate (the same predicate isAngryAt reads).
- **`entityEventWolfTameHearts byte = 7` / `entityEventWolfTameSmoke byte = 6`** — the EntityEvent status consts.
- **handleInteract gate**: `if mob.typ == entity.Wolf.ID && t.tryWolfInteract(p, mob) { return }`, placed BEFORE `tryFeedAnimal`, beside the cow/sheep gates.

### The deterministic test suite (commit 44061b7a)

**`server/wolf_taming_test.go`** (8 tests, all green): TestWolfTameSuccess (isTame + HP 8->40 + MAX_HEALTH base 40 + owner + orderedToSit + bone consumed), TestWolfTameFailure (no tame, HP stays 8, bone STILL consumed), TestWolfTameDrawsExactlyOneNextInt3 (lockstep rng proof of EXACTLY one nextInt(3)), TestWolfBoneRequiredToTame (non-BONE -> false + zero rng drawn), TestWolfAngryCannotTame (angry -> false + no consume + zero rng — the isAngry gate precedes the draw), TestWolfSitToggleByOwner (false->true->false), TestWolfSitToggleNonOwnerDenied (owner gate), TestWolfTamedFoodFallsThroughToFeed. The wolf rng is seeded directly; an in-test reference `newEntityRandom(seed)` self-validates the tame-success (9) / tame-fail (1) seeds.

## EXEC-TIME JAR VERIFICATION (verbatim, this session — `javap -c -p temp/cache/26.2-inner.jar`)

- **`Wolf.applyTamingSideEffects`**: `isTame() ifeq 30; getAttribute(MAX_HEALTH).setBaseValue(40.0d); ldc_w 40.0f; setHealth(40.0f); goto 43; (30) getAttribute(MAX_HEALTH).setBaseValue(8.0d); return.` — CONFIRMS the 8->40 bump + heal to 40 (the `double 40.0d` / `float 40.0f` split is faithful: setBaseValue takes a double, setHealth a float).
- **`Wolf.tryToTame`**: `random.nextInt(3) ifne 48; tame(player); navigation.stop(); setTarget(null); setOrderedToSit(true); bipush 7; Level.broadcastEntityEvent(this, 7); goto 58; (48) bipush 6; Level.broadcastEntityEvent(this, 6); return.` — CONFIRMS the single `nextInt(3)`, the `==0` success branch (ifne to the smoke), and the hearts(7)/smoke(6) statuses.
- **`Wolf.mobInteract`** (the two branches): TAMED tail `r=super.mobInteract(player,hand); if (r.consumesAction() ifne 325 || isOwnedBy ifeq 325) return r; setOrderedToSit(!isOrderedToSit()); jumping=false (putfield jumping:Z); navigation.stop(); setTarget(null); return SUCCESS.withoutItem().` UNTAMED `isClientSide ifne 370; stack.is(BONE) ifeq 370; isAngry() ifne 370; stack.consume(1,player); tryToTame(player); return SUCCESS_SERVER.` — CONFIRMS the branch structure, the `consumesAction() || !isOwnedBy` tail, and the BONE+!isAngry gate exactly.
- **`TamableAnimal.tame`**: `setTame(true, true); setOwner(player); (ServerPlayer -> CriteriaTriggers.TAME_ANIMAL.trigger).` — CONFIRMS tame() = setTame(true, includeSideEffects=true) + setOwner; the advancement trigger is DEFERRED (no advancement system).

## Deviations from Plan

### Deviation 1 — the tame()/setTame/applyTamingSideEffects/isAngry helpers live in attack_dispatch.go, not entity.go

- **Found during:** Task 1 design.
- **Issue:** The plan's action describes `setTame`/`setOwner`/`applyTamingSideEffects` (which in vanilla are *Entity-level TamableAnimal/Wolf methods). 36-01 added the wolf STATE fields but NO setter methods. The wave-2 disjointness gate (and the critical-rules) mandate the ONLY Go production edit be attack_dispatch.go.
- **Resolution:** Inlined the side-effect logic as attack_dispatch.go-LOCAL helpers — `applyWolfTamingSideEffects` (the 8->40 bump), `wolfIsAngry` (the isAngry read), and the tame()/setTame/setOwner logic folded into `tryToTameWolf` (direct field writes `mob.tame`/`mob.ownerUUID`/`mob.orderedToSit` + the applyTamingSideEffects call). This keeps the entire port in one file, preserves the disjointness with 36-03, and is a faithful re-expression (the bytecode call chain tame()->setTame(true,true)->applyTamingSideEffects is mirrored as the field-write + helper sequence). NOT a behavior change — purely a code-locality decision driven by the wave gate. No shared edit outside attack_dispatch.go was needed.

## DEFERRALS (cited, recorded — never silently dropped)

- **The tamed feed-heal** (`isFood(stack) && getHealth() < getMaxHealth() -> feed(player,hand,stack,2.0f,2.0f)`): no health-feed/`feed` system in v1; a WOLF_FOOD interact on a tamed wolf falls through to tryFeedAnimal (the breed feed) rather than the heal-feed. CITED (JARNOTES:47).
- **The collar-dye** (`stack.is(WOLF_COLLAR_DYES) && isOwnedBy -> setCollarColor`): no DyeColor component / collar on the wolf in v1. CITED (JARNOTES:48).
- **The body-armor equip + armor-repair** (`isEquippableInSlot(stack, BODY) ...` / `armor.isValidRepairItem`): no equipment/armor system in v1. CITED (JARNOTES:49-51).
- **The TAME_ANIMAL advancement trigger** (`CriteriaTriggers.TAME_ANIMAL.trigger(serverPlayer, this)`): no advancement system in v1. CITED (TamableAnimal.tame).
- **The owner-UUID WIRE broadcast** (DATA_OWNERUUID index 19): the server-side `ownerUUID` THIN id drives the sit-toggle owner gate + the goals; the client owner-glow render is deferred (carried from 36-01). The tame DATA_FLAGS flip (index 18, the tame 0x4 + sit 0x1 bits) IS broadcast.
- **The race detector** (`go test -race`): requires CGO/gcc, not present on this Windows host (carried from 36-01). The gate is `CGO_ENABLED=0 go build/vet/test` (all pass). The new taming path is tick-owned (TICK-05), drawing on the per-entity mobRandom stream, with no shared mutable state introduced.

## Known Stubs

No goal-disabling stubs. The deferred tamed-interact branches (heal-feed/dye/armor) are FAITHFUL v1 reductions — a tamed wolf with food in hand correctly falls through to the feed path, and the dye/armor branches simply don't exist yet (a tamed wolf right-clicked with a dye/armor item falls to the sit-toggle or feed, which is the closest faithful v1 behavior). The CORE must-have — tame on BONE (1/3) with the 8->40 HP bump, and the owner sit-toggle — is fully live and tested.

## Verification

- `CGO_ENABLED=0 go build ./...` -> exit 0
- `CGO_ENABLED=0 go vet ./server/...` -> clean
- `CGO_ENABLED=0 go test ./server/ -count=1` -> ok (full suite, 18.8s)
- `CGO_ENABLED=0 go test ./level/... -count=1` -> ok
- `TestPluginPigEqualsGoNativePig` -> PASS (the pig oracle BYTE-IDENTICAL — the wolf path is entity.Wolf.ID-gated, zero new pig draws)
- `TestWolfTameSuccess / TestWolfTameFailure / TestWolfTameDrawsExactlyOneNextInt3 / TestWolfBoneRequiredToTame / TestWolfAngryCannotTame / TestWolfSitToggleByOwner / TestWolfSitToggleNonOwnerDenied / TestWolfTamedFoodFallsThroughToFeed` -> PASS
- Acceptance greps: `func (t *TickLoop) tryWolfInteract` present; `entity.Wolf.ID && t.tryWolfInteract` gate present; non-comment `nextInt(3)` count == 1; `SetBaseValue(40.0)` + `= 40.0` present; hearts(7)+smoke(6) consts+uses present
- `git diff go.mod go.sum` -> empty (no new dependency)

## Self-Check: PASSED

- Created files exist: server/wolf_taming_test.go (+ server/attack_dispatch.go modified).
- Commits exist: 21bd0320 (Task 1 port), 44061b7a (test suite).
- All verification gates green (build/vet/full-suite, pig oracle byte-identical, 8 wolf tests pass, go.mod empty).
