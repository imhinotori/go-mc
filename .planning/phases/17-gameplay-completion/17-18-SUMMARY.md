---
phase: 17
plan: 17-18
subsystem: server / breath
tags: [gameplay, breath, entity-data, oxygen, bubble-bar, synched-data]
provides:
  - air-supply entity-data sync (DATA_AIR_SUPPLY_ID -> client bubble bar)
requires:
  - 17-13 (server-side air-supply / drowning tick: breath.go)
  - 17-01 (per-player store Entity: player_visibility.go)
  - 06-02 (SetEntityData encoder + entityDataEntry framing: entity_encode.go)
affects:
  - server/breath.go
  - server/entity_encode.go
  - server/tick.go
  - server/gameplay_tick.go
key-files:
  modified:
    - server/breath.go
    - server/entity_encode.go
    - server/tick.go
    - server/gameplay_tick.go
    - server/breath_test.go
    - server/entity_encode_test.go
decisions:
  - "DATA_AIR_SUPPLY_ID is a SYNCHED field; the server pushes it to the client. The client does NOT locally simulate air in multiplayer (corrects 17-13's wrong deferral)."
  - "Send dirty-only (lastAirSent tracker), mirroring vanilla SynchedEntityData; no per-tick resend."
  - "SELF send is the primary fix (the tracker self-skips, so the player never gets its own metadata via the tracker); observers covered via the tracked set."
metrics:
  duration: ~35m
  completed: 2026-06-26
---

# Phase 17 Plan 18: Oxygen Bubble Bar Air-Supply Sync Summary

Air supply (`DATA_AIR_SUPPLY_ID`) is now pushed to the client as a synched entity-data field, so the underwater oxygen bubble bar depletes and refills. One-liner: server-authoritative air is broadcast via `ClientboundSetEntityData` (index 1, INT/VAR_INT serializer) on change, self-send + observer-send, correcting Plan 17-13's false "client simulates air locally" assumption.

## Root cause (confirmed)

Plan 17-13 (`server/breath.go`) correctly decremented `p.airSupply` server-side every tick the player's eyes are underwater, but **never sent the value to the client**. In vanilla, air is a SYNCHED entity-data field — the server is authoritative and pushes `ClientboundSetEntityData` (including to the player about its OWN entity), which is what drives the bubble bar. The client does NOT locally simulate air in multiplayer. 17-13's SUMMARY wrongly deferred the send assuming a local client sim; that assumption was wrong and is now corrected.

## The 1:1 fix (bytecode-cited)

All wire facts were verified this session with `javap` over `temp/cache/26.2-inner.jar`:

1. **Index 1 = `DATA_AIR_SUPPLY_ID`.** `net.minecraft.world.entity.Entity.<clinit>` `defineId` order:
   - `getstatic EntityDataSerializers.BYTE; defineId -> DATA_SHARED_FLAGS_ID` (index 0)
   - `getstatic EntityDataSerializers.INT;  defineId -> DATA_AIR_SUPPLY_ID`   (index 1)
   - then CUSTOM_NAME (2), CUSTOM_NAME_VISIBLE (3), SILENT (4), NO_GRAVITY (5), POSE (6), TICKS_FROZEN (7).
   (Consistent with the existing `dataItemIndex == 8` for `ItemEntity.DATA_ITEM`.)

2. **Serializer id 1 = INT.** `net.minecraft.network.syncher.EntityDataSerializers.<clinit>` `registerSerializer` call order assigns the wire id: `getstatic BYTE; registerSerializer` (id 0), `getstatic INT; registerSerializer` (id 1), then LONG (2), FLOAT (3), STRING (4), COMPONENT (5), OPTIONAL_COMPONENT (6), ITEM_STACK (7). (Consistent with the existing `itemStackSerializerID == 7`.)

3. **INT value codec = `ByteBufCodecs.VAR_INT`.** In the same static init: `EntityDataSerializers.INT = EntityDataSerializer.forValueType(ByteBufCodecs.VAR_INT)` (vs `BYTE = forValueType(ByteBufCodecs.BYTE)`). So the air value writes as a **VarInt**, not a fixed big-endian Int. Implemented as `pk.VarInt(air)`.

4. **`ClientboundSetEntityData` wire** is already implemented and byte-sealed by Plan 06-07 (`encodeSetEntityData`: VarInt id + packed DataValue entries + mandatory `0xFF` terminator). The air fix only adds a new `entityDataEntry`; it does not touch the packet framing.

### Code

- **`server/entity_encode.go`** — added `dataAirSupplyIndex = 1`, `intSerializerID = 1`, and `airDataEntry(air int32) entityDataEntry` (value = `pk.VarInt(air)`), mirroring the existing `itemDataEntry` pattern with full bytecode citations.
- **`server/tick.go`** — added `lastAirSent int32` to `tickPlayer` (the dirty-tracker), seeded to `maxAirSupply` so the first send fires only on a real change (matching the client's registered air default).
- **`server/gameplay_tick.go`** — seed `lastAirSent: maxAirSupply` at registration alongside `airSupply: maxAirSupply`. (Air is not persisted/restored on join, so a fresh player always starts at 300; the seed is always correct.)
- **`server/breath.go`** — `tickBreath` now calls `syncAirSupply(p)` after the air branch. `syncAirSupply`:
  - returns immediately if `airSupply == lastAirSent` (dirty-only, like vanilla `SynchedEntityData`),
  - builds `encodeSetEntityData(p.playerEntity, airDataEntry(p.airSupply))`,
  - **SELF send** to `p.client` (the tracker self-skips `e.id == p.entityID`, so this direct send is the only path for the local bubble bar — the primary fix),
  - **OBSERVER send** via `broadcastSetEntityDataToTrackers`, which reuses the tracker's tick-owned `tracked` set (who-sees-whom) so a second player sees the first's bubbles deplete, with no new visibility logic.

## Initial value on join

No metadata edit to the join seam was needed: the Notchian client initializes `DATA_AIR_SUPPLY_ID` to its registered default — `Entity` ctor `define(DATA_AIR_SUPPLY_ID, getMaxAirSupply()==300)` (`sipush 300; ireturn`) — when it spawns the player entity, so the bar starts full (300) before any submersion. `lastAirSent` is seeded to `maxAirSupply` to match, so the first `SetEntityData` fires on the first real decrement (300 -> 299), not redundantly at spawn — exactly the vanilla dirty-field cadence.

## eyeInWater / worldgen-water gate (verified, no bug)

`eyeInWater(p)` samples `fluidAt(floor(x), floor(y+1.62), floor(z))` -> `decodeFluid`. Worldgen places `block.Water{Level:0}` (source water) for oceans/aquifers/carvers/surface (`world/levelgen/...`). `waterLevelOf` reads `block.StateList[id].(block.Water).Level`, and `decodeFluid(level 0)` returns `{isWater:true, source:true, amount:8}`. So the gate fires over worldgen water — **no `waterLevelOf`/`decodeFluid` bug**. The existing `setWater(..., 0)` test harness already writes the identical `waterStateID(0) == block.Water{Level:0}` state worldgen emits. A regression test was added to pin this (`TestDecodeFluidRecognizesWorldgenWater`).

## Before / after

- **Before:** player submerges -> `airSupply` drains server-side -> bubble bar stays full client-side (no air metadata ever sent) -> player perceives no air loss and never drowns visually.
- **After:** player submerges -> `airSupply` drains -> each change is pushed via `SetEntityData(index 1, INT, VarInt air)` to the player's own client (local bar depletes) and to observers (remote bars deplete) -> surfacing refills the bar (+4/tick) -> at air <= -20 the bar is empty and drowning damage applies (17-13 path).

## Tests added

- `TestAirDataEntryWire` (entity_encode_test.go): `airDataEntry(287)` frames as Byte(1) + VarInt(1) + VarInt(287) + 0xFF terminator.
- `TestTickBreathSendsAirOnChange` (breath_test.go): one underwater tick emits exactly one `ClientboundSetEntityData` to the player's own client carrying air=299 at index 1 / serializer 1; a no-change full-bar tick emits zero (dirty-only).
- `TestDecodeFluidRecognizesWorldgenWater` (breath_test.go): `decodeFluid(block.ToStateID[block.Water{Level:0}])` is `{isWater, source, amount==8}`.

## Observer-sync status

Covered. The observer send reuses the tracker's `tracked` set, so any player who already has an AddEntity'd avatar for the drowning player also receives the air updates. The self-send remains the load-bearing fix for the local bar (the tracker never sends a player its own entity-data).

## Deviations from Plan

None of the Rule-4 (architectural) kind. The investigation found the `eyeInWater` gate and `decodeFluid` were already correct (no `waterLevelOf` fix needed), so that conditional sub-task collapsed to a regression test only — as the prompt anticipated.

## Verification

- `CGO_ENABLED=0 go build ./...` -> exit 0.
- `CGO_ENABLED=0 go test ./...` -> all packages pass (full repo).
- `go vet ./server/` -> clean.
- `-race` could not be run in this environment (no C compiler / CGO toolchain). The new send path is entirely tick-goroutine-owned (TICK-05 single-owner: `airSupply`/`lastAirSent`/`playerEntity`/`tracked`/`t.players`), and `Client.Send` is documented safe from any producer — the same discipline as the existing `attack_dispatch.go` `client.Send(encodeSetEntityMotion(...))` tick-path send.

## Self-Check: PASSED

- server/breath.go: FOUND (syncAirSupply + broadcastSetEntityDataToTrackers + tickBreath call)
- server/entity_encode.go: FOUND (airDataEntry, dataAirSupplyIndex=1, intSerializerID=1)
- server/tick.go: FOUND (lastAirSent field)
- server/gameplay_tick.go: FOUND (lastAirSent seed)
- Tests: TestAirDataEntryWire, TestTickBreathSendsAirOnChange, TestDecodeFluidRecognizesWorldgenWater all pass.
