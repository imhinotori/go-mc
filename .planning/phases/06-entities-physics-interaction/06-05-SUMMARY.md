---
phase: 06-entities-physics-interaction
plan: 05
subsystem: api
tags: [inventory, component-slot, itemstack, hashedstack, container-click, protocol-776, subtick, server-authoritative]

# Dependency graph
requires:
  - phase: 06-entities-physics-interaction (06-01)
    provides: the subtick dispatch/applyInput pipeline + tickPlayer tick-owned state
  - phase: 06-entities-physics-interaction (06-04)
    provides: the dispatch case-set + applyInput resolution pattern (ServerboundPlayerAction route) reused for the container packets
  - phase: 01-foundation-fork-codegen (01)
    provides: level/component.SlotData codec + NewComponent dispatch (the 111 component schemas)
provides:
  - "level/component.SlotData.WriteTo EXTENDED to round-trip real component-slot ItemStacks (was count+id+0+0 only)"
  - "the container packets (ContainerClick/SetCreativeModeSlot/ContainerClose/SetCarriedItem) routed into the subtick buffer (the previously-missing dispatch route)"
  - "server-owned tick-owned Inventory (46-slot component-slot array + held slot) on tickPlayer"
  - "containerSetContent/containerSetSlot authoritative encoders over SlotData"
  - "decodeHashedStack — the jar-derived 1.21.5+ HashedStack decoder (consume + discard hashes, no mis-framing)"
  - "handleContainerClick (server-authoritative re-send) + handleSetCreativeModeSlot/SetCarriedItem/ContainerClose"
affects: [06-06 inventory NBT persistence (disk codec, kept separate), 06-07 inventory wire byte-seal capture-diff]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Inverse codec via raw byte capture: SlotData.ReadFrom tees the added+removed component byte span verbatim into RawComponents; WriteTo re-emits it — ReadFrom∘WriteTo is provably byte-exact without re-serializing 111 schemas"
    - "HashedStack = CRC digest, NOT a full ItemStack: optional(ActualItem) = Boolean present + VarInt itemId + VarInt count + HashedPatchMap; the added-component VALUE is a FIXED 4-byte int (the hash), not a component body — reading it as a body mis-frames the packet"
    - "Server-authoritative inventory (T-6-02): the click's hashes are decoded-and-DISCARDED; the server never builds a slot from client-claimed data and re-sends authoritative ContainerSetContent"
    - "Defensive on-tick decode (T-6-04): a malformed/truncated click Scan-errors to a silent no-op (count bounded to 128) — never panics; mutates only tick-owned state (TICK-05 / T-6-08)"

key-files:
  created:
    - server/inventory.go
    - server/slot_encode.go
    - server/inventory_test.go
    - level/component/types_test.go
  modified:
    - level/component/types.go
    - server/tick.go
    - server/subtick.go
    - .planning/phases/06-entities-physics-interaction/06-CAPTURE-DIFF.md

key-decisions:
  - "SlotData inverse representation = verbatim RawComponents capture (ReadFrom tees the component span, WriteTo re-emits it). Chosen over re-serializing the 111 component schemas: byte-exact by construction, zero new code per schema, and the server (authoritative) never needs to interpret the values."
  - "HashedStack decoded-and-DISCARDED, not honored (A6): the server is authoritative for v1, so decodeHashedStack only consumes the bytes correctly (Boolean present + VarInt id + VarInt count + HashedPatchMap with fixed 4-byte INT hashes) and ignores the digests — the load-bearing requirement is NO mis-framing, not hash validation."
  - "v1 inventory model is minimal (46-slot array + creative-set + held slot); the WIRE is the requirement (ENT-04). Survival mechanics (crafting/shift-click) are beyond v1."
  - "changedSlots count bounded to 128 (vanilla SLOTS_STREAM_CODEC max) so a forged huge count cannot drive a long loop before EOF."

requirements-completed: [ENT-04]

duration: 24 min
completed: 2026-06-24
---

# Phase 6 Plan 05: Component-Based Slot Inventory (ENT-04) Summary

Implemented the post-1.20.5 component-based slot inventory — the phase's highest wire-risk surface. The wire `ItemStack` is the component-slot form (NO NBT-in-slot), which is exactly `level/component.SlotData`; its `WriteTo` was extended from the minimal `count+id+0+0` form to round-trip real components. A per-player server-owned tick-owned `Inventory` encodes the authoritative `ClientboundContainerSetContent`/`SetSlot`, and `ServerboundContainerClick` is decoded in the 1.21.5+ `HashedStack` form WITHOUT mis-framing — the server discards the client's hashes and re-sends authoritative content. The four container packets were routed into the subtick buffer (the previously-missing dispatch route).

## What was built

**Task 0 — jar-derive HashedStack/HashedPatchMap + extend SlotData.WriteTo:**
- Decompiled `HashedStack`, `HashedStack$ActualItem`, `HashedPatchMap`, `ServerboundContainerClickPacket`, `ClientboundContainerSetContentPacket`, `ClientboundContainerSetSlotPacket` from `temp/cache/26.2-inner.jar`.
- Extended `SlotData.ReadFrom` to tee the added+removed component byte span verbatim into `RawComponents`, and `SlotData.WriteTo` to re-emit `count+id+addedCount+removedCount+RawComponents` — provably inverse, byte-exact. A component-free stack still writes exactly `count+id+0+0`.
- `TestSlotEncode`: empty / component-free / one-added-component all round-trip byte-exact.

**Task 1 — route container packets + inventory + encoders + HashedStack decode:**
- `server/tick.go` dispatch: added `ServerboundContainerClick/SetCreativeModeSlot/ContainerClose/SetCarriedItem` to the subtick-relevant case set (they fell through to the `default:` no-op before — the inventory was a silent dead feature).
- `server/subtick.go` applyInput: added the four resolution cases (after the teleport gate, preserving the hook-first ordering) → the inventory handlers.
- `server/inventory.go`: the `Inventory` type (46-slot component-slot array + held slot + stateId) on `tickPlayer`; `handleContainerClick` (decode HashedStack, discard hashes, re-send authoritative `ContainerSetContent`), `handleSetCreativeModeSlot` (store a full ItemStack + echo), `handleSetCarriedItem` (held slot, bounded 0..8), `handleContainerClose` (v1 no-op).
- `server/slot_encode.go`: `containerSetContent`/`containerSetSlot` over `SlotData`, and `decodeHashedStack` (consume + discard).
- `server/inventory_test.go`: `TestContainerClickRouted` (dispatch route), `TestContainerSetContent` (encode framing), `TestContainerClickDecode` (no mis-frame + truncated no-op), `TestContainerClickAuthoritative` (forged item discarded + authoritative re-send), `TestCreativeSetSlot` (visible item).

## The jar-derived wire layout (the most important finding)

`HashedStack.STREAM_CODEC = ByteBufCodecs.optional(ActualItem.STREAM_CODEC)`:

```
HashedStack:
  Boolean present
  if present:
    VarInt itemId      (ActualItem.item: holderRegistry(Registries.ITEM) => VarInt)
    VarInt count       (ActualItem.count: ByteBufCodecs.VAR_INT)
    HashedPatchMap:
      VarInt addedCount,   addedCount   × ( VarInt typeId + Int32 hash )   <-- VALUE IS A FIXED 4-BYTE INT (CRC), NOT A COMPONENT
      VarInt removedCount, removedCount × ( VarInt typeId )
```

`ServerboundContainerClickPacket.STREAM_CODEC` (7-field composite, exact order):

```
1. containerId    ByteBufCodecs.CONTAINER_ID   (VarInt alias)
2. stateId        ByteBufCodecs.VAR_INT        (VarInt)
3. slotNum        ByteBufCodecs.SHORT          (Short)
4. buttonNum      ByteBufCodecs.BYTE           (Byte)
5. containerInput ContainerInput.STREAM_CODEC  (idMapper => VarInt enum 0..6)
6. changedSlots   map<Short slot -> HashedStack>  (VarInt count + N × (Short + HashedStack), max 128)
7. carriedItem    HashedStack.STREAM_CODEC
```

`ClientboundContainerSetContent` (4-field composite): `CONTAINER_ID, VAR_INT stateId, OPTIONAL_LIST<ItemStack> items (VarInt count + N × SlotData), OPTIONAL<ItemStack> carriedItem`.
`ClientboundContainerSetSlot` (manual write): `writeContainerId (VarInt), writeVarInt stateId, writeShort slot (Short!), OPTIONAL<ItemStack> itemStack`.

**THE LOAD-BEARING FACT:** the HashedPatchMap added-component *value* is `ByteBufCodecs.INT` — a fixed 4-byte big-endian int (the CRC hash), NOT a component value. This is precisely what distinguishes `HashedStack` from a full `ItemStack` (whose added value is the real component payload). Decoding the added value as a component body mis-frames every subsequent byte → the panic/garbage risk the plan flagged. `decodeHashedStack` consumes `VarInt typeId + 4 raw bytes` per added component and discards both.

Confirmed corrections vs. the research/wiki guess: field 5 is `ContainerInput` (jar name for the click-type enum, still a VarInt); the changedSlots map key is a **Short** (not a VarInt slot); the map/collection length prefixes are VarInt counts. Recorded in `06-CAPTURE-DIFF.md §ENT-04`.

## Test results

- `go test ./level/component/ -run TestSlotEncode -count=1` — PASS (empty / component-free / one-added all round-trip).
- `go test ./server/ -run 'TestContainerClickRouted|TestContainerSetContent|TestContainerClickDecode|TestContainerClickAuthoritative|TestCreativeSetSlot' -count=1` — all 5 PASS.
- Full `./server/... ./level/component/...` suites — PASS (no regression; movement/teleport/phase-order/06-04 block tests green).
- `go vet ./...` + `go build ./...` — clean.
- Docker `-race` over `./server/... ./level/...` — clean (inventory mutates only tick-owned state, TICK-05 / T-6-08).
- `go.mod`/`go.sum` unchanged — zero new dependencies.

## Deviations from Plan

None - plan executed exactly as written. (The TDD RED was demonstrated in-session — the implementation symbols were undefined before the handlers were written; the test and implementation were committed together because the test references those symbols and a test-only commit would be a non-compiling tree.)

## Capture-diff status (deferred to 06-07)

JAR-DERIVED this plan (no mis-framing). The byte-level seal is deferred to Plan 06-07: (1) the component-slot added-component value codecs in a real component-carrying `ContainerSetContent` (Sulfur re-emits raw bytes — byte-exact by construction, but the per-schema value layout is seal-proven only when diffed against a live inventory), and (2) the `HashedStack` fixed-4-byte hash width against a real vanilla-client click round-trip.

## Self-Check: PASSED

- Created files exist: server/inventory.go, server/slot_encode.go, server/inventory_test.go, level/component/types_test.go — all present.
- Commits exist: 730ea5db (feat: SlotData.WriteTo), d28cfa26 (docs: HashedStack framing), b26d5e89 (feat: routing + inventory) — all in git log.
- All acceptance criteria + verification commands pass (above).
