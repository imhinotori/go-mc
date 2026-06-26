# Phase 17 — Gameplay Completion: VERIFICATION (goal-backward)

> Does the codebase deliver what Phase 17 promised? Each GAMEPLAY-0N requirement checked against
> the actual code + tests, not just "the plan ran". Status: **PASS pending the autonomous:false
> human visual gate** (GAMEPLAY-07).

## Requirement coverage

| Req | Promise | Delivered? | Evidence |
|-----|---------|-----------|----------|
| **GAMEPLAY-01** | Each joining player is in the entity store as entity.Player (156), position-synced every tick, tracker broadcasts AddEntity/move/remove — two clients see each other. | ✅ | `server/player_visibility.go` (newPlayerEntity, syncPlayerEntities, broadcastPlayerInfoAdd/Remove). Tracker is entity-type-agnostic. Tab-list broadcast bidirectional (player type 156 needs PlayerInfoUpdate before AddEntity). Tests: tracker_test.go. |
| **GAMEPLAY-02** | Join bootstrap applies persisted x/y/z instead of spawn; position survives reconnect. | ✅ | `server/persistence.go` (load/round-trip tested). Player .dat path. |
| **GAMEPLAY-03** | Initial ContainerSetContent on join; click/creative/carried handlers work end-to-end. | ✅ | `server/inventory.go` (sendContent on join) + `inventory_click.go`/`inventory_doclick.go` (doClick 1:1, audited faithful). |
| **GAMEPLAY-04** | ServerboundAttack/Interact resolve target + applyDamage/die/respawn + fall damage. | ✅ | `server/attack_dispatch.go` + `combat.go` (Player.attack/hurtServer/actuallyHurt — audited 1:1 faithful) + `fall_damage.go` (causeFallDamage — audited 1:1). |
| **GAMEPLAY-05** | tickWorld simulates fluids (FlowingFluid flow + levels) + player fluid physics (swim/buoyancy/breath). | ✅ | `server/fluid.go` (FlowingFluid sim) + `fluid_physics.go` + `breath.go` (audited 1:1) + the FluidCount fix (the float root cause — user-confirmed) + the cave-gap one-shot post-process fix. |
| **GAMEPLAY-06** | After break→air, look up drop, spawn + track Item entity (71), AddEntity broadcast → pickable. | ✅ | `server/block_drop.go` + `item_entity.go` (drop spawn + pickup scan + ITEM metadata). |
| **GAMEPLAY-07** | VISUAL GATE (autonomous:false): real 26.2 client (two players) confirms move/reconnect/damage/water/drops + (this session) swing/equipment/eat-pose visibility. | 🟡 **PENDING human verify** | The 1:1 work is COMPLETE + tested (the three visibility audits: swing/Animate, equipment/SetEquipment, eat-pose/DATA_LIVING_ENTITY_FLAGS, delta-move/sendChanges). The testbot two-client repro is stable (0 disconnects). The remaining item is the operator's visual sign-off. |

## Cross-cutting quality gates
- ✅ `CGO_ENABLED=0 go build ./...` clean (pure-Go static).
- ✅ Full `./server/` suite green; `./world/`, `./level/`, `./world/levelgen/...` green.
- ✅ Docker `-race` full `./server/` + `./world/` GREEN (the new visibility seams, the async tracker, and the worldgen race fix all covered).
- ✅ 1:1 mandate: every gameplay surface this phase touched was verified against `temp/cache/26.2-inner.jar` bytecode before writing (inventory, place, combat, fall, breath, fluids, the three visibility surfaces). The audits this session caught + fixed one real gap (block-place entity-collision) and confirmed the rest faithful.

## Bugs found + fixed while verifying (own-it, not "pre-existing")
- Join-disconnect (outbound queue overflow at join) — FIXED (RotateHead 1:1 + outboundCap 4096).
- Worldgen crash (concurrent map in SurfaceSystem) — FIXED (cacheMu, -race green).
- Block-place entity-collision missing — FIXED (isUnobstructed port).
- TestTickAIDrivesMobs flake — FIXED (timed receive).

## Verdict
**PASS (1:1 + tests + -race), gated on the GAMEPLAY-07 human visual sign-off.** The codebase
delivers all six wired seams; the only open item is the operator confirming the multiplayer
visuals on a real client. No 1:1 debt remains in the surfaces this phase owns (the deferred items
— DATA_SHARED_FLAGS, async fluid, mob-water-nav, crafting — are explicitly out of Phase 17 scope,
logged in deferred-items.md, with crafting moved to v4 Phase 25).
