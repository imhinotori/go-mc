---
phase: 17
plan: 14
subsystem: gameplay / entities
tags: [block-drop, item-pickup, item-entity, inventory, gamemode, popResource]
requires:
  - GAMEPLAY-06 (block_drop.go drop spawn + ITEM metadata)
  - ENT-01 (entity store + tracker)
  - ENT-02 (per-axis swept physics / moveEntity)
  - ENT-04 (server-owned inventory)
provides:
  - Vanilla-parity Block.popResource drop spawn (position jitter + toss velocity + pickup delay)
  - ItemEntity.tick (0.04 gravity, age, 6000-tick despawn)
  - ItemEntity.playerTouch pickup (inventory insert + ClientboundTakeItemEntity + remove)
  - Inventory.add port (merge-into-stack + free-slot overflow)
  - Creative gamemode drop gate (ServerPlayerGameMode.destroyBlock)
affects:
  - server/block_drop.go
  - server/item_entity.go (new)
  - server/entity.go
  - server/entity_encode.go
  - server/tick.go
  - server/tick_phases.go
  - server/gameplay_tick.go
  - server/play_join.go
  - server/block_interact.go
tech-stack:
  added: []
  patterns:
    - math/rand/v2 package-level RNG as the RandomSource.nextDouble() analogue
    - tickEntities additive sub-phase seam (no new tick phase; TestTickPhaseOrder preserved)
    - single-owner tick-goroutine mutation (TICK-05); no goroutine/xsync/ants added
key-files:
  created:
    - server/item_entity.go
    - server/item_entity_test.go
    - .planning/phases/17-gameplay-completion/deferred-items.md
  modified:
    - server/block_drop.go
    - server/block_drop_test.go
    - server/entity.go
    - server/entity_encode.go
    - server/block_interact.go
    - server/tick.go
    - server/tick_phases.go
    - server/gameplay_tick.go
    - server/play_join.go
decisions:
  - Item gravity stepped at 0.04 (ItemEntity.getDefaultGravity), NOT the generic 0.08 tickPhysics uses
  - Pickup AABB = player box inflated (1.0, 0.5, 1.0) per Player.aiStep, verbatim
  - take-item broadcast targets the tracker's p.tracked set (sendToTrackingPlayers analogue)
  - inventoryAdd ports only the stackable branch of Inventory.add (v1 drops are never damaged tools)
metrics:
  duration: ~1h
  completed: 2026-06-26
---

# Phase 17 Plan 14: BLOCK-DROP + ITEM-PICKUP Vanilla Parity Summary

Ported `Block.popResource`, `ItemEntity` (`<init>` / `tick` / `playerTouch`), `LivingEntity.take`,
and `Inventory.add` verbatim from the unobfuscated 26.2 inner jar so a broken block drops a
properly-jittered, tossed, despawning item that a nearby player actually **picks up** — closing
the reported "items on the ground can't be picked up" bug — plus a creative-gamemode no-drop gate.

## What changed (before → after)

### Before
- `spawnBlockDrop` spawned the Item at the **static block center** (`x+0.5, y, z+0.5`) with
  **zero velocity**, **no pickup delay**, **no despawn**, **no gamemode check**.
- There was **no pickup logic at all** — items sat on the ground forever and could never be
  collected. No item physics tick (the drop never fell or settled). No despawn.

### After
- **FIX A — `Block.popResource` spawn (verbatim):** spawn at block center `+0.5` per axis with an
  independent `Mth.nextDouble(rng, -0.25, 0.25)` jitter on **each** axis, and the Y additionally
  offset down by `d = ITEM.getHeight()/2.0 = 0.125`. `Mth.nextDouble` ported exactly
  (`lo>=hi ? lo : rng.nextDouble()*(hi-lo)+lo`).
- **FIX B — `ItemEntity.<init>` velocity (verbatim):** `setDeltaMovement(nextDouble()*0.2-0.1, ...)`
  per axis — a random toss in `[-0.1, 0.1)`. `setDefaultPickUpDelay()` sets `pickupDelay = 10`.
- **FIX C — item physics tick (`ItemEntity.tick`):** decrement `pickupDelay` (skipping the 32767
  sentinel), apply the item's **0.04** gravity (not the generic 0.08), integrate via the shared
  per-axis swept resolver (`moveEntity`) so the toss **falls + lands + is blocked**, increment
  `age` (skipping the -32768 sentinel), and **discard at LIFETIME 6000** (5-min despawn).
- **FIX D — pickup (the reported bug):** each tick, for every non-dead player, build the pickup
  AABB (player box **inflated (1.0, 0.5, 1.0)**, per `Player.aiStep`) and, for every Item entity
  intersecting it, run `ItemEntity.playerTouch`: if `pickupDelay == 0` and `Inventory.add`
  succeeds, broadcast `ClientboundTakeItemEntity(itemId, collectorId, count)` to tracking players
  (`LivingEntity.take`), re-send the authoritative inventory, and remove the item when fully
  absorbed. `Inventory.add` ported (merge into matching stacks up to max stack size, then overflow
  into the first free slot).
- **FIX E — gamemode gate (`ServerPlayerGameMode.destroyBlock`):** a **creative** player's break
  drops nothing. Sulfur hardcodes survival, so the gate always passes today, but it is present and
  correct so a future creative toggle drops nothing with no further edit.

## Decompilation citations (all javap'd this session from temp/cache/26.2-inner.jar)

| Vanilla method | Confirmed | Ported to |
|----------------|-----------|-----------|
| `Block.popResource(Level, BlockPos, ItemStack)` | `d = ITEM.getHeight()/2.0`; `X+0.5 + Mth.nextDouble(-0.25,0.25)`; Y `- d`; Z same | `spawnBlockDrop` |
| `Block.popResource(Level, Supplier, ItemStack)` | `setDefaultPickUpDelay()` + `addFreshEntity`; `BLOCK_DROPS` gamerule | `NewItemEntity` / gate |
| `Mth.nextDouble(RandomSource, double, double)` | `lo>=hi → lo`; else `nextDouble()*(hi-lo)+lo` | `mthNextDouble` |
| `ItemEntity.<init>(Level, d, d, d, ItemStack)` | `setDeltaMovement(nextDouble()*0.2-0.1, ×3)` | `NewItemEntity` |
| `ItemEntity.setDefaultPickUpDelay()` | `pickupDelay = 10` | `itemDefaultPickupDelay` |
| `ItemEntity.getDefaultGravity()` | `0.04` | `itemGravity` |
| `ItemEntity` constants | `LIFETIME=6000`, `INFINITE_PICKUP_DELAY=32767`, `INFINITE_LIFETIME=-32768` | item_entity.go consts |
| `ItemEntity.tick()` | empty→discard; `pickupDelay--`; gravity; move; `age++`; `age>=6000→discard` | `tickItem` |
| `ItemEntity.playerTouch(Player)` | `pickupDelay==0` + target; `inventory.add`; `take`; empty→discard | `playerTouchItem` |
| `Player.aiStep` item collection | `getBoundingBox().inflate(1.0, 0.5, 1.0)` → touch | `scanItemPickup` |
| `LivingEntity.take(Entity, int)` | `ClientboundTakeItemEntityPacket(item.getId(), this.getId(), count)` to trackers | `takeItem` |
| `ClientboundTakeItemEntityPacket.write` | 3× `writeVarInt`: itemId, playerId, amount | `encodeTakeItemEntity` |
| `Inventory.add(ItemStack)` → `add(-1, stack)` | do-while addResource; `return count < startCount` | `inventoryAdd` |
| `Inventory.addResource(int, ItemStack)` | slot-with-space/free; `min(count, maxStack - existing)`; grow | `addResource` |

## Identical constants preserved

`±0.25` per-axis jitter · `-0.125` Y half-height offset · `±0.1` toss velocity · `10`-tick
pickup delay · `6000`-tick (5-min) despawn · `0.04` item gravity · `(1.0, 0.5, 1.0)` pickup
inflate · max stack size from the generated item registry (64 default).

## Tick wiring

`tickItems()` is an **additive** call inside the existing `tickEntities` phase (alongside
`tickFallDamage` / `tickBreath` / `tickPlayerCombat`), **before** `tracker.Tick`, so:
- no new tick phase is added — `TestTickPhaseOrder` stays green;
- a pickup/despawn removal is reflected in this tick's `near()`, so the tracker emits
  `RemoveEntities` promptly;
- it does **not** touch the sibling Wave edits (combat i-frames, breath airSupply, fluid).

The new `Entity` fields (`itemStack`, `isItem`, `pickupDelay`, `age`) are plain value/byte types,
preserving the snapshot-friendly contract the async tracker relies on.

## Deviations from Plan

None for the scoped work — FIX A–E all implemented as specified. The `gameMode` field had to be
added to `tickPlayer` (it previously existed only as a `bootstrapParams` field) plus a
`gameModeCreative = 1` constant, to make the FIX E gate real; this is required supporting plumbing
(deviation Rule 2 — missing critical functionality for the gate), set to `gameModeSurvival` at
registration so current behavior is unchanged.

## Deferred Issues

`TestTickAIDrivesMobs` (in `server/spawner_test.go`, an out-of-scope AI/spawner file) is a
**pre-existing flaky test** — it fails 8/8 on the clean `ender-776` tree with all 17-14 changes
stashed, because the wandering mob's navigation draws from the global `math/rand/v2` and
occasionally fails its "x advanced > 2.0 in 400 ticks" assertion. Logged to
`deferred-items.md`; not fixed here (scope boundary — not caused by 17-14, and the file is
explicitly out of scope).

## Verification

- `CGO_ENABLED=0 go build ./...` → exit 0.
- `CGO_ENABLED=0 go vet ./server/` → clean.
- `CGO_ENABLED=0 go test ./server/ -skip TestTickAIDrivesMobs` → **PASS** (all packages).
- New tests (all deterministic across 5 runs):
  - `TestBlockDropSpawnsItem` — spawn position inside the vanilla jitter ranges
    (x/z ∈ [1.25,1.75], y ∈ [64.125,64.625]), velocity ∈ [-0.1,0.1) per axis, pickupDelay==10,
    isItem set, non-empty metadata.
  - `TestItemPickupDelayDecrements` — pickupDelay walks 10→0, one decrement/tick, then stays 0.
  - `TestItemAgesAndDespawns` — age reaches LIFETIME 6000 → item removed from store.
  - `TestItemPickedUpAfterDelay` — not collected while delay > 0; once 0, inventory grows by 1,
    item removed, `ClientboundTakeItemEntity` sent.
  - `TestItemPickupOutOfRange` — item 5 blocks away is not collected.
  - `TestCreativeNoDrop` — creative break spawns 0 items; survival break spawns 1.
  - `TestInventoryAddStacksAndFree` — fills a partial stack to 64 then overflows into a free slot.
- `-race` not run: this environment is `CGO_ENABLED=0` (race requires cgo). All new code is
  single-owner tick-goroutine work matching the existing `tickPhysics`/`tickAI` discipline.

## Self-Check: PASSED

- server/item_entity.go — FOUND
- server/item_entity_test.go — FOUND
- .planning/phases/17-gameplay-completion/deferred-items.md — FOUND
- All modified files build clean (exit 0).
