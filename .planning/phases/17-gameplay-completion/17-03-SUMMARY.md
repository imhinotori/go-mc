---
phase: 17-gameplay-completion
plan: 03
subsystem: gameplay
tags: [combat, pvp, fall-damage, damage-dispatch, subtick, wave-2, jar-port]

# Dependency graph
requires:
  - phase: 17-gameplay-completion
    plan: 01
    provides: "lookupPlayerByEntityID reverse lookup (player_visibility.go) + tickPlayer fall fields (fallDistance/wasOnGround/lastY) + the fall_damage.go stub + the tickFallDamage call site wired into tickEntities"
  - phase: 06-entities
    provides: "applyDamage/die/performRespawn server-owned combat loop (combat.go)"
provides:
  - "GAMEPLAY-04: PvP attack dispatch (ServerboundAttack -> reach-gated server-authoritative applyDamage) + environmental fall damage (floor(fallDistance-3.0) on landing)"

affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "26.2 attack packet split: the entity ATTACK is ServerboundAttackPacket (VarInt entityId ONLY), separate from the right-click ServerboundInteractPacket — the old ServerboundInteract{Action} enum is gone"
    - "Parallel-wave verification via an isolated git worktree at HEAD (copy my files in, build+test there) — never git clean, never edit/stash sibling files mid-run"

key-files:
  created:
    - server/attack_dispatch.go
    - server/attack_dispatch_test.go
    - server/fall_damage_test.go
  modified:
    - server/subtick.go
    - server/fall_damage.go

key-decisions:
  - "26.2 entity ATTACK is carried by ServerboundAttackPacket = a single VarInt entityId (jar STREAM_CODEC = ByteBufCodecs.VAR_INT only). NOT ServerboundInteract — which is now the right-click interaction (entityId+InteractionHand+Vec3+Boolean, no Action enum). The javap output decided this against the plan's older 'Interact carries ATTACK' premise."
  - "baseAttackDamage = 1.0 HP (bare-hand Attributes.ATTACK_DAMAGE base); server-supplied, packet never sets amount (T-6-05/V4)"
  - "attackReach = 3.0 + 0.5 (entity-interaction range base 3.0 + jitter slack), distinct from blockReach=6.0"
  - "Fall damage = floor(fallDistance - 3.0) per LivingEntity.calculateFallDamage/calculateFallPower with SAFE_FALL_DISTANCE=3.0 and default multipliers; the jar 1.0E-6 epsilon is carried as a float-equality guard"
  - "ServerboundInteract handled as an explicit v1 no-op (never damages) so the wire is consumed deliberately and a future entity-interaction behavior has a named seam"

requirements-completed: [GAMEPLAY-04]

# Metrics
duration: 18min
completed: 2026-06-25
---

# Phase 17 Plan 03: Damage Dispatch + Fall Damage (GAMEPLAY-04) Summary

**Wired the two missing damage paths into the already-built combat loop: a PvP attack (ServerboundAttack, jar-confirmed as a VarInt-entityId-only packet in 26.2) now resolves the named target to a tickPlayer, reach-gates it, and applies server-authoritative bare-hand damage through the existing applyDamage->die flow; and a per-player fall-damage tick accumulates airborne descent and deals floor(fallDistance-3.0) on landing — all by overwriting the 17-01 fall_damage.go stub and adding two applyInput cases, with zero edits to any shared tick-pipeline file.**

## Performance
- **Duration:** ~18 min
- **Tasks:** 2 (both TDD: RED test commit -> GREEN impl commit)
- **Files:** 3 created, 2 modified

## Accomplishments
- GAMEPLAY-04 attack half: `applyInput` now has `ServerboundAttack` + `ServerboundInteract` cases after the teleport gate. `handleAttack` defensively decodes the target entity id, resolves it via the 17-01 `lookupPlayerByEntityID`, rejects nil/self/out-of-reach silently, and applies the server const `baseAttackDamage` (1.0). Death + respawn already worked, so a lethal attack drives `die()` and a subsequent respawn request still calls `performRespawn`.
- GAMEPLAY-04 environmental half: `tickFallDamage` (overwriting the 17-01 no-op stub) accumulates `fallDistance` from the per-tick descent while airborne and, on the `onGround` false->true landing edge, applies `floor(fallDistance - 3.0)` damage through the same `applyDamage` path, then resets.
- Both ported from `temp/cache/26.2-inner.jar` via `javap -c -p` (cited inline), idiomatic Go, no GPL paste.

## Task Commits
1. **Task 1 RED:** `22f49a12` — failing attack-dispatch tests
2. **Task 1 GREEN:** `7d649117` — `handleAttack`/`handleInteract`/`withinAttackReach` + subtick cases
3. **Task 2 RED:** `f4653081` — failing fall-damage tests
4. **Task 2 GREEN:** `75b8496a` — `tickFallDamage` real body (overwrites stub)

## JAR FINDINGS (port mandate)

### Which packet carries the entity attack — and why it is ServerboundAttack, not ServerboundInteract
`javap -c -p net.minecraft.network.protocol.game.ServerboundAttackPacket` shows a record with a **single field `int entityId`** and `STREAM_CODEC = StreamCodec.composite(ByteBufCodecs.VAR_INT, …)` — the entire wire body is one VarInt entity id. Its `handle()` dispatches to `ServerGamePacketListener.handleAttack`.

`javap -c -p net.minecraft.network.protocol.game.ServerboundInteractPacket` shows a DIFFERENT record: `int entityId`, `InteractionHand hand`, `Vec3 location`, `boolean usingSecondaryAction`, with `STREAM_CODEC = composite(VAR_INT, InteractionHand.STREAM_CODEC, Vec3.LP_STREAM_CODEC, BOOL)` and **no nested `Action`/`ActionType` class** (both `ServerboundInteractPacket$Action` and `$ActionType` are "class not found" in 26.2).

**Conclusion (the javap output decided, per the plan's instruction):** in 26.2 the old `ServerboundInteractPacket` with the `{INTERACT, ATTACK, INTERACT_AT}` Action enum was **split** — ATTACK got its own `ServerboundAttackPacket` (VarInt entityId only) and `ServerboundInteractPacket` is now exclusively the right-click interaction. So `handleAttack` decodes only a VarInt entityId; `handleInteract` is a deliberate v1 no-op that never damages. This corrects the plan's older premise that "the entity attack is carried by ServerboundInteract(Action=ATTACK)".

### baseAttackDamage + attackReach sources
- `baseAttackDamage = 1.0` HP — the bare-hand `Attributes.ATTACK_DAMAGE` base a Player registers (half a heart before weapon/enchant/strength/critical/cooldown scaling, none of which v1 applies). Server-supplied; the packet only NAMES a target.
- `attackReach = 3.0 + 0.5` — vanilla `Attributes.ENTITY_INTERACTION_RANGE` base 3.0 blocks plus a small slack for tick-sample-vs-hit-frame position jitter. Kept distinct from `blockReach` (6.0) because entity reach is shorter than block reach.

### Fall-damage formula
- `Entity.checkFallDamage`: while not in water and `deltaY < 0`, `fallDistance -= (float)deltaY`; on `onGround`, if `fallDistance > 0` call `fallOn -> causeFallDamage`, then `resetFallDistance`.
- `LivingEntity.calculateFallDamage = Mth.floor(calculateFallPower(d) * damageMul * FALL_DAMAGE_MULTIPLIER)` and `calculateFallPower(d) = (d + 1.0E-6) - SAFE_FALL_DISTANCE`, with `SAFE_FALL_DISTANCE` attribute base = 3.0 and the default multipliers = 1.0 → landing damage = `floor(fallDistance - 3.0)` HP.

## 17-01 handoff consumed (as specified)
- Used `lookupPlayerByEntityID` (player_visibility.go) for PvP target resolution — not redeclared, player_visibility.go untouched.
- Used the `fallDistance`/`wasOnGround`/`lastY` tickPlayer fields declared in tick.go — **tick.go NOT edited**.
- Overwrote the `server/fall_damage.go` stub body, keeping the `func (t *TickLoop) tickFallDamage()` signature identical — the `tickEntities` call site in **tick_phases.go was NOT edited**.
- Verified: `git diff 096f7edd..HEAD -- server/tick.go server/tick_phases.go` is empty (no shared-pipeline edit).

## Deviations from Plan

### Auto-fixed Issues
**1. [Rule 1 - Bug] Packet-shape correction: attack is ServerboundAttack, not ServerboundInteract(Action=ATTACK)**
- **Found during:** Task 1 (javap of ServerboundInteractPacket — the plan-cited Action enum does not exist in 26.2).
- **Issue:** The plan's `<interfaces>`/Pattern-4 assumed the 26.2 entity attack rides `ServerboundInteractPacket` with an `{INTERACT, ATTACK, INTERACT_AT}` Action enum and a `const interactActionAttack = 1` guard. The jar shows that enum was removed and ATTACK was split into its own `ServerboundAttackPacket` (VarInt entityId only).
- **Fix:** `handleAttack` decodes a single VarInt entityId from `ServerboundAttack`; `handleInteract` (for `ServerboundInteract`) is a non-damaging v1 no-op. Both `applyInput` cases are present so both ids are consumed deliberately. The plan explicitly instructed "the javap output decides; do NOT guess" — this is that decision.
- **Files modified:** server/attack_dispatch.go, server/subtick.go
- **Commit:** 7d649117

### Parallel-run isolation note (no plan deviation, process detail)
Because Wave-2 runs three agents in ONE shared working tree, the sibling plans' uncommitted/untracked files (block_drop*.go, fluid*.go, an in-progress entity_encode.go import) intermittently broke `go build ./server/...` for the whole package while my own code was correct. I verified my work in an **isolated `git worktree` checked out at HEAD** (committed baseline only, no sibling uncommitted files), copying in my five owned files and running build/test/`-race` there. I never ran `git clean`, never edited or reverted any sibling file, and removed the worktree with `git worktree remove`. The shared tree's sibling work was left fully intact.

## Deferred (out of scope for this plan)
- Non-fall environmental damage (fire, lava, drowning, void, cactus) — A2: fall damage is the Phase-17 minimum.
- Water/lava fall cushioning, slow-falling, and the per-block `fallOn` multiplier (hay bale / slime) — basic `floor(fallDistance-3.0)` only.
- Held-item / critical-hit / attack-cooldown / strength damage scaling — bare-hand 1.0 only.
- Entity right-click behavior (mount, trade, leash) behind `ServerboundInteract` — named no-op seam left for a future plan.

## Verification
- `CGO_ENABLED=0 go build ./...` exit 0 (isolated worktree).
- `go test ./server/` passes (full package, isolated): all TestAttack*/TestFall* + combat_test + TestTickPhaseOrder.
- Docker `-race` over `./server/`: clean (no data races) — full suite 8.4s, no flake this run.
- `grep ServerboundAttack/ServerboundInteract server/subtick.go`: both cases present (no longer the default no-op).
- `git diff 096f7edd..HEAD -- server/tick.go server/tick_phases.go`: empty (no shared-pipeline edit).
- My four commits touched exactly: attack_dispatch.go, attack_dispatch_test.go, fall_damage.go, fall_damage_test.go, subtick.go.

## Self-Check: PASSED
- All 5 created/modified files verified present on disk.
- All 4 task commit hashes (22f49a12, 7d649117, f4653081, 75b8496a) verified in git log.
