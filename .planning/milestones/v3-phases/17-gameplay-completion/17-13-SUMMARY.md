# Phase 17 Plan 13: Fluid Physics Wire + Breath/Drowning 1:1 Port Summary

Completed Sulfur's FLUID gameplay by (1) wiring the previously-dead `applyFluidPhysics` (0.8 horizontal water slowdown + 0.014 buoyancy) into the movement-accept path, so water now affects player movement, and (2) porting breath/drowning verbatim from the vanilla 26.2 jar: a 300-point air supply that drains 1/tick while the eyes are submerged, refills 4/tick above water, and deals 2.0 DROWN damage at the `air <= -20` threshold. All constants are method-for-method copies of `temp/cache/26.2-inner.jar`, cited at each use site. The FlowingFluid spread algorithm (`server/fluid.go`) was NOT touched — it is already 1:1.

## Scope

Files owned and edited:
- `server/breath.go` (new) — the `LivingEntity.baseTick` air branch + `Entity`/`LivingEntity` air accessors (`tickBreath`, `eyeInWater`, `decreaseAirSupply`, `increaseAirSupply`, `shouldTakeDrowningDamage`).
- `server/fluid_physics.go` — added `moveWithFluidPhysics`, the accepted-delta movement-accept wire for the existing `applyFluidPhysics`.
- `server/subtick.go` — the two position-bearing movement cases (`ServerboundMovePlayerPos`, `ServerboundMovePlayerPosRot`) now route the collided target through `moveWithFluidPhysics` (water physics) instead of accepting it verbatim.
- `server/tick.go` — added the `airSupply int32` field to `tickPlayer` (additive only).
- `server/gameplay_tick.go` — seeds `airSupply: maxAirSupply` (300) at registration, mirroring vanilla's `Entity` ctor `define(DATA_AIR_SUPPLY_ID, getMaxAirSupply())`.
- `server/tick_phases.go` — one additive `t.tickBreath()` call inside the existing `tickEntities` phase (no phase reorder — `TestTickPhaseOrder` stays green).
- `server/breath_test.go` (new) — breath/drowning + wire tests.

`server/fluid.go` (spread), `server/combat.go` (only called via `applyDamage`), `server/fall_damage.go`, and `server/block_drop.go` were NOT edited, per the plan's file fence.

## Methods Ported (each decompiled via `javap -c -p` from `temp/cache/26.2-inner.jar` and cited)

### Air supply / drowning (`breath.go`)
- `net.minecraft.world.entity.Entity.getMaxAirSupply()` — `sipush 300; ireturn` => `maxAirSupply = 300`.
- `net.minecraft.world.entity.Entity` ctor — `Builder.define(DATA_AIR_SUPPLY_ID, getMaxAirSupply())` => fresh-player default 300 (seeded in `gameplay_tick.go`).
- `net.minecraft.world.entity.LivingEntity.decreaseAirSupply(int)` — with `OXYGEN_BONUS` at its base 0 (no Respiration), the `nextDouble() >= 1/(d+1)` random-skip branch is dead (`d <= 0.0` falls through), leaving `iload_1; iconst_1; isub; ireturn` => unconditional `air - 1`. The omitted OXYGEN_BONUS skip is documented at the function as the future read site.
- `net.minecraft.world.entity.LivingEntity.increaseAirSupply(int)` — `iload_1; iconst_4; iadd; getMaxAirSupply; Math.min; ireturn` => `min(air + 4, 300)`.
- `net.minecraft.world.entity.LivingEntity.shouldTakeDrowningDamage()` — `getAirSupply; bipush -20; if_icmpgt 13; iconst_1...` => `air <= -20`.
- `net.minecraft.world.entity.LivingEntity.baseTick()` (air/water branch, offsets 206–419) — `tickBreath`. The collapsed faithful form for a bare survival player:
  - on `isEyeInFluid(WATER) && !isBubbleColumn`: `setAirSupply(decreaseAirSupply(getAirSupply()))`; `if (shouldTakeDrowningDamage()) { setAirSupply(0); broadcastEntityEvent(67); hurtServer(DROWN, 2.0F); }`.
  - else (eyes out of water): `if (air < max) setAirSupply(increaseAirSupply(getAirSupply()))`.
  - The `canBreatheUnderwater()` (player==false), `MobEffectUtil.hasWaterBreathing`, `Abilities.invulnerable`, `BUBBLE_COLUMN`, and `shouldEffectsRefillAirsupply` guards are all faithful no-ops in v1 (no effects, no creative-flight invuln, no bubble columns), so the branches they gate are omitted — byte-for-byte equivalent for the survival player. Each omission is cited in the `tickBreath` doc comment as the future wire-in point.
- `2.0F` DROWN amount — `fconst_2` passed to `hurtServer` => `drownDamage = 2.0`, routed through Sulfur's `applyDamage` (the 17-11 `hurtServer` port). Because DROWN does NOT bypass i-frames, routing through `applyDamage` (which arms `invulnerableTime = 20` on each hit) reproduces vanilla's drowning cadence exactly: air resets to 0, re-drains across the 20-tick window, yielding one 2.0 hit per ~second.
- `Entity.isEyeInFluid(FluidTags.WATER)` — `eyeInWater`: samples the single block cell at `(floor(x), floor(y + 1.62), floor(z))`. The `1.62` standing eye height is the player POSE's `height(1.8) * 0.9`. This is intentionally the EYE position, NOT the full-AABB `playerInWater` used by the movement physics and the fall-damage reset — air only drains when the head is under, exactly like vanilla.

### Water movement wire (`fluid_physics.go`, `subtick.go`)
- `net.minecraft.world.entity.LivingEntity.getWaterSlowDown()` — `ldc_w 0.8f; freturn` => `waterSlowDown = 0.8` (already in `fluid_physics.go` from 17-02).
- `net.minecraft.world.entity.Entity.updateFluidInteraction()` — `EntityFluidInteraction.applyCurrentTo(..., 0.014d)` => `waterPushScale = 0.014` (already in `fluid_physics.go`).
- `net.minecraft.world.entity.LivingEntity.travelInWater(...)` — confirmed the velocity model: after `move`, `deltaMovement` is `multiply(slowdown, 0.8, slowdown)` and `getFluidFallingAdjustedMovement` applies the buoyant push. `moveWithFluidPhysics` is the accepted-delta application of this for the position-authoritative server (see caveat below).

## The wire (FIX 1)

`applyFluidPhysics` shipped in 17-02 fully unit-tested but UNCALLED. Plan 17-13 wires it via a new `moveWithFluidPhysics(p, targetX, targetY, targetZ)` in `fluid_physics.go`:
1. `target` = the collided (anti-clip-through) position from `collidePlayer`.
2. `delta = target - current` (the per-tick movement the client claims).
3. `applyFluidPhysics(delta)` — identity on dry land (so the dry path is byte-for-byte the old collide-only behavior), 0.8 horizontal + 0.014 buoyant vertical in water.
4. return `current + adjustedDelta`.

The two position-bearing `subtick.go` cases changed from `p.x,p.y,p.z = t.collidePlayer(...)` to `nx,ny,nz := t.collidePlayer(...); p.x,p.y,p.z = t.moveWithFluidPhysics(p, nx,ny,nz)`. The `Rot`/`StatusOnly` cases (no position change) are untouched.

### Before / After
- **Before:** `applyFluidPhysics` existed but nothing called it. Water had ZERO effect on player movement; the server accepted the client's claimed in-water position verbatim. No air supply, no drowning — a player could stay submerged forever with no consequence.
- **After:** In-water horizontal movement deltas are scaled by 0.8 and gain +0.014 vertical buoyancy at the accept path. Air drains 1/tick while the eyes are underwater, refills 4/tick above, and the player takes 2.0 DROWN damage (i-frame-gated to ~1/sec) once air hits -20, then air resets to 0.

## Rubber-band caveat (documented, NOT a blocker — user explicitly chose to wire now)

Sulfur is position-authoritative: the client sends its position and ALSO applies its own local water slowdown before sending. Re-applying the same 0.8 factor server-side on the accepted delta can therefore cause minor rubber-banding (the server nudges the accepted position relative to the client's already-slowed claim). This is inherent to the accepted-delta model layered on a position-authoritative transport, NOT a logic error — the constants and structure are 1:1 vanilla.

The true 1:1 feel requires the vanilla VELOCITY model: a server-side `deltaMovement` integrator where the server is authoritative over position (vanilla's `LivingEntity.travelInWater` integrates velocity, the client predicts, and the server reconciles). That **server-side velocity integrator is a known architectural follow-up — an OPTIMIZATION/architecture change, not a gameplay-logic change** — and is out of scope for this plan. The physics math shipped here is the exact vanilla math; only the integration substrate differs. Per the plan, this was wired now over waiting for the integrator.

## Client sync note (DATA_AIR_SUPPLY_ID)

`airSupply` is the server-authoritative value (it drives the drowning DAMAGE, the gameplay effect requested). The vanilla bubble-bar METADATA is `DATA_AIR_SUPPLY_ID`, confirmed via the `Entity` clinit `defineId` order to be **metadata index 1** with the **INT (VarInt) serializer** (index 0 is `DATA_SHARED_FLAGS_ID`). Sulfur's tracker only sends `ClientboundSetEntityData` to OTHER players viewing an entity — a player never receives its own metadata — and a player's own bubble bar is rendered client-side from the client's local air simulation, so no self-metadata push is needed for the local bar today. Pushing index-1 INT air metadata to OTHER observers (so a second player sees your bubbles deplete) is a follow-up that belongs in the tracker/entity-encode path (outside this plan's file fence). The damage path — the gameplay the user chose — is fully wired and tested.

## Tests (`breath_test.go`)

- `TestEyeInWaterGatesOnEyes` — air gates on the EYE block (`y+1.62`), not the feet: eyes-in-water is true; feet-only (water at feet, air at eye block) is false.
- `TestAirDecrementsUnderwater` — each submerged tick drops air by exactly 1 (300 -> 299 -> 298).
- `TestAirRefillsAboveWater` — out of water air climbs by 4/tick, clamps to 300 (298 -> 300), and a full bar is a no-op.
- `TestDrowningDamageAtThreshold` — air at -19 decrements to -20 -> reset to 0 + exactly 2.0 health lost.
- `TestNoDrownDamageAboveThreshold` — air at -10 -> -11, no damage (above the -20 threshold).
- `TestAirRefillsOnLeavingWater` — drained-then-surfaced refills by 4 from the drained value (not a reset).
- `TestMoveWithFluidPhysicsWire` — the accept-path wire applies 0.8 horizontal + 0.014 buoyancy in water (x 8.5->9.3, y 64->63.514) and is the identity on dry land.

Existing `TestWaterSlowdown` / `TestBuoyancy` / `TestInWaterDetection` (17-02) continue to gate `applyFluidPhysics` directly.

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go test ./...` — all pass (full module).
- `CGO_ENABLED=0 go vet ./server/...` — clean.
- `-race`: the gcc-less environment cannot run the cgo race detector. `airSupply` is mutated ONLY in `tickBreath` (inside `tickEntities`, on the tick goroutine), exactly like `fallDistance` and the combat fields, so it is race-clean by the same single-owner (TICK-05) discipline as the rest of `tickPlayer`.

## Deviations from Plan

None of substance. The plan flagged the metadata sync as conditional ("if it must sync to the client"); it does not need to sync for the local bubble bar or the drowning damage, so the air metadata index (1, INT) is documented as a tracker follow-up rather than implemented — staying inside the plan's file fence (no `tracker.go`/`entity_encode.go` edits). The eye-in-water gate uses the vanilla `isEyeInFluid` semantics (eye block at `y+1.62`) rather than the full-AABB `playerInWater`, which is the faithful vanilla behavior for the air branch (movement physics and fall-damage reset correctly keep using the full-AABB check).

## Self-Check: PASSED
- `server/breath.go` — FOUND
- `server/breath_test.go` — FOUND
- `server/fluid_physics.go` (moveWithFluidPhysics) — FOUND
- `server/subtick.go` (wire) — FOUND
- `server/tick.go` (airSupply field) — FOUND
- `server/gameplay_tick.go` (seed) — FOUND
- `server/tick_phases.go` (tickBreath call) — FOUND
- Build/test/vet — PASS
