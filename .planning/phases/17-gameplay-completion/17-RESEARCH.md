# Phase 17: Gameplay Completion (the six unwired seams) - Research

**Researched:** 2026-06-25
**Domain:** Minecraft 26.2 server gameplay wiring (Go) — player-entity broadcast, position-load, inventory join-sync, damage dispatch, vanilla fluid simulation port, block-drop entities
**Confidence:** HIGH (five seam reconnects verified against live code + jar); MEDIUM (fluid-sim port — algorithm mapped from jar bytecode, exact constants verified, but it is a net-new subsystem)

## Summary

Phase 17 closes six gameplay defects a real vanilla 26.2 client surfaced against the shipped v1/v2 server. Five of the six share one pattern confirmed by reading the actual code this session: **the core logic exists, is unit-tested, and is correct — only the final wiring SEAM is missing.** The sixth (GAMEPLAY-05, fluid simulation) is the one genuinely net-new subsystem and must be ported directly from the unobfuscated jar per the standing mandate.

The five seam reconnects are small, surgical, and high-confidence. The verified seams: (01) players are never inserted into `t.entities`, so the (correct, working) tracker has no player data to broadcast — and a second subtlety the handoff missed: **the joining player's `PlayerInfoUpdate(ADD_PLAYER)` is only sent to itself, never broadcast to other players, so even after the AddEntity fix the avatar will not render until the tab-list entry is broadcast** `[VERIFIED: minecraft.wiki protocol FAQ + server/play_join.go:337-344]`; (02) the persisted position is loaded into `save.PlayerData` but explicitly not applied (`gameplay_tick.go:246-257`); (03) `sendContent` exists and works but is never called on join; (04) `applyInput` has no `ServerboundAttack`/`ServerboundInteract` case (they hit `default:`), and there is no environmental-damage tick; (06) block break sets air but never spawns an Item entity.

GAMEPLAY-05 requires porting `FlowingFluid.spread`/`getNewLiquid`/`tick` (verified present at `net.minecraft.world.level.material.FlowingFluid` in 26.2), a net-new **scheduled-block-tick queue** (the world `ChunkManager` has no tick scheduler — `world/manager.go` exposes only `Get`/`Set`/`GetBlock`/`SetBlock`), and player fluid physics (26.2 refactored this into `EntityFluidInteraction` + `LivingEntity.travel`/`getWaterSlowDown`).

**Primary recommendation:** Sequence GAMEPLAY-01 first (keystone: player-Entity-in-store + tab-list broadcast unblocks all visible-entity work including GAMEPLAY-06). Then the small seams 02/03/04 in parallel. GAMEPLAY-05 is the large standalone — build the scheduled-tick queue + `tickWorld` fluid pass + player fluid physics as its own wave. GAMEPLAY-06 depends on 01. NO new Go dependencies are needed; everything is a port-from-jar + existing-encoder reuse.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Player visibility broadcast (GAMEPLAY-01) | Tick loop (`server`) | — | Entity store + tracker are tick-owned; the tab-list broadcast goes through each player's bounded outbound queue |
| Position persistence-load (GAMEPLAY-02) | Accept goroutine + bootstrap (`server`) | Tick loop | Load runs off-tick at `AcceptPlayer`; the teleport that applies it is part of the bootstrap (pre-register) OR a first-tick re-teleport |
| Inventory join-sync (GAMEPLAY-03) | Tick loop (`server`) | — | `sendContent` must fire on the first tick after `AcceptPlayer` registers the player (the inventory is tick-owned) |
| Damage dispatch + fall damage (GAMEPLAY-04) | Tick loop (`server/subtick` + `server/combat`) | — | Attack/Interact resolve on-tick in `applyInput`; fall damage in `tickPhysics`/`tickEntities` |
| Fluid simulation (GAMEPLAY-05) | Tick loop (`server/tick_phases.tickWorld`) | World (`world` block state + new scheduled-tick queue) | Block mutation is tick-owned (`SetBlock`); player fluid physics is in the tick's movement/physics path |
| Block-break item drops (GAMEPLAY-06) | Tick loop (`server/block_interact` + entity store) | Loot tables (STRUCT-POLISH-01 overlap) | The Item entity is spawned into the tick-owned store; broadcast rides GAMEPLAY-01's path |
| Visual gate (GAMEPLAY-07) | Real vanilla 26.2 client | — | Human-verified end-to-end on `demo.trysulfur.net` / local |

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| (none new) | — | All six seams reuse existing in-repo packages | `[VERIFIED: go.mod + code read]` Phase 17 is wiring + a jar port; no new capability requires a dependency |

**Existing packages the plans use (already in the repo):**

| Package | Used for | Confidence |
|---------|----------|------------|
| `server` (TickLoop, tickPlayer, entityStore, Entity, tracker) | All six seams | HIGH `[VERIFIED]` |
| `data/entity` (`entity.Player` ID 156, `entity.Item` ID 71) | GAMEPLAY-01 player Entity, GAMEPLAY-06 item Entity | HIGH `[VERIFIED: data/entity/entity.go:1424, :659]` |
| `level/block` (`ToStateID`, `StateList`, `block.Water{Level}`, `block.Lava{Level}`) | GAMEPLAY-05 fluid state read/write | HIGH `[VERIFIED: level/block/blocks.go:68-73, block.go:24-27]` |
| `world` (`ChunkManager.GetBlock`/`SetBlock`) | GAMEPLAY-05 fluid block IO, GAMEPLAY-06 drop pos | HIGH `[VERIFIED: world/manager.go:163-189]` |
| `server/entity_encode.go` (`encodeAddEntity`/`encodeSetEntityData`/`encodeTeleportEntity`/`encodeRemoveEntities`) | GAMEPLAY-01 + 06 entity broadcast | HIGH `[VERIFIED]` — already jar-derived + capture-diff-sealed (06-07) |
| `server/combat.go` (`applyDamage`/`die`/`performRespawn`/`setHealth`) | GAMEPLAY-04 — already complete, just uncalled | HIGH `[VERIFIED: server/combat.go:64-174]` |
| `server/inventory.go` (`sendContent`/`ensureInventory`) | GAMEPLAY-03 — already complete, just uncalled on join | HIGH `[VERIFIED: server/inventory.go:80-85]` |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Net-new scheduled-block-tick queue for fluids | A "scan every fluid block every tick" brute force | `[ASSUMED]` Brute-force re-evaluates all loaded water each tick — simpler but O(loaded fluid blocks) per tick and does not reproduce vanilla's `getTickDelay`-spaced propagation. The vanilla-faithful approach is a scheduled-tick queue (`ServerLevel.scheduleTick`). The mandate is parity, so port the scheduled queue. |
| `entity.Player` Entity in the store for broadcast | A parallel "remote player" list separate from `entityStore` | Reusing `entityStore` + the existing tracker is far less code and the tracker already does the near()/diff/AddEntity/RemoveEntities work. Adding a second collection duplicates the broad-phase. Use the store. |

**Installation:** none.

**Version verification:** N/A — no new packages. Toolchain is Go 1.26.1 (confirmed in CLAUDE.md), jar is `temp/cache/26.2-inner.jar` (verified present, 24.9 MB, javap available at Zulu 25).

## Architecture Patterns

### System Architecture Diagram (the six seams in the tick pipeline)

```
                    network read goroutine
                            │ Intent{*Client, pk.Packet}
                            ▼
          ┌──────────────────────────────────────────┐
          │  TickLoop.tickOnce()  (single owner)      │
          │                                           │
   join → │  drainRegistrations()                     │  ◄── GAMEPLAY-01 seam A:
          │     t.players append                      │       t.entities.add(playerEntity)
          │     [ADD: create entity.Player(156),      │       + broadcast PlayerInfoUpdate ADD
          │      t.entities.add(it)]                   │       to OTHER players
          │                                           │  ◄── GAMEPLAY-03 seam:
          │     [ADD: sendContent(p) first tick]      │       sendContent on first tick
          │                                           │
          │  resolveSubtickInputs() → applyInput()    │  ◄── GAMEPLAY-04 seam:
          │     case Attack/Interact → applyDamage    │       add Attack/Interact cases
          │                                           │
          │  tickWorld()  [EMPTY STUB]                │  ◄── GAMEPLAY-05 seam:
          │     [ADD: drainScheduledFluidTicks();     │       port FlowingFluid.tick/spread
          │      FlowingFluid.tick per scheduled pos] │
          │                                           │
          │  tickEntities() → [pos-sync player        │  ◄── GAMEPLAY-01 seam B:
          │     Entity from tickPlayer each tick]      │       store.move(playerEntity, p.x/y/z)
          │     [ADD: fall-damage check]               │  ◄── GAMEPLAY-04 fall damage
          │                                           │
          │  tickPhysics() → [ADD: water buoyancy/    │  ◄── GAMEPLAY-05 player fluid physics
          │     drag for player movement]             │
          │                                           │
          │  tracker.Tick()  [WORKS — needs data]     │  ◄── GAMEPLAY-01 payoff + GAMEPLAY-06:
          │     near() → AddEntity/Teleport/Remove    │       now sees players + dropped items
          │                                           │
          │  flushOutbound()                          │
          └──────────────────────────────────────────┘
                            │
    block break ───────────┤  handlePlayerAction → SetBlock(air)
    (GAMEPLAY-06 seam:      │  [ADD: lookup drop, spawn entity.Item(71),
     after SetBlock)        │   t.entities.add(item) → tracker broadcasts]
                            ▼
                    network write goroutine (per client)
```

### Pattern 1: GAMEPLAY-01 — player as an Entity in the store + tab-list broadcast (KEYSTONE)

**What:** Two seams. (A) On join, create an `entity.Player` (ID 156) `Entity`, set its id to the player's already-allocated `p.entityID`, its uuid to `p.uuid`, position to spawn, and `t.entities.add(it)`. (B) Each tick, sync the player Entity's pos/angles from the authoritative `tickPlayer` fields via `entityStore.move`. THEN — the subtlety the handoff missed — broadcast the joining player's `PlayerInfoUpdate(ADD_PLAYER)` to every OTHER player (not just self), or their clients drop the AddEntity.

**Why the tab-list broadcast is mandatory:** `[VERIFIED: minecraft.wiki Java_Edition_protocol/FAQ]` "If the Player List Item [PlayerInfoUpdate ADD_PLAYER] for the player spawned by [AddEntity] is not present when this packet arrives, Notchian clients will not spawn the player entity." The current `writePlayerInfoUpdateAdd` (`play_join.go:337`) sends a **single self-entry only** — there is no broadcast to existing players, and existing players' entries are never sent to the newcomer either. The plan MUST add bidirectional tab-list sync at join/leave.

**When to use:** join (seam A + tab broadcast), every tick (seam B pos-sync), leave (RemoveEntities is already handled by the tracker when the Entity leaves the store, but the PlayerInfoUpdate REMOVE_PLAYER must also be broadcast).

**Player Entity construction (verified field shapes):**
```go
// Source: data/entity/entity.go:1424 (Player ID 156); server/entity.go:99 (NewEntity)
// NewEntity assigns a FRESH uuid + a passed id — but a player needs its OWN id+uuid,
// so construct directly (or extend NewEntity) so id == p.entityID and uuid == p.uuid:
pe := &Entity{
    id:     p.entityID,            // SAME id as the Login playerId (already allocated)
    typ:    entity.Player.ID,      // 156
    uuid:   p.uuid,                // the player's profile UUID (tab-list entry must match)
    x:      p.x, y: p.y, z: p.z,
    width:  entity.Player.Width,   // 0.6
    height: entity.Player.Height,  // 1.8
}
t.entities.add(pe)
```

**Pitfall (double-add):** `entityStore.add` overwrites on a duplicate id (`entity_store.go:83`). Add the player Entity exactly once in `drainRegistrations` (the same place `t.players` is appended), and `t.entities.remove(p.entityID)` in `removePlayer`. Do NOT add it in both `AcceptPlayer` and `drainRegistrations`.

**Pos-sync each tick:** the tracker reads `e.x/e.z` for `near()` and `e.x/y/z` for the AddEntity/TeleportEntity encoders. If the player Entity's pos is stale, other clients see the player frozen at spawn. Sync in `tickEntities` (which runs before `tracker.Tick`) via `t.entities.move(pe, p.x, p.y, p.z)` and copy `yaw/pitch/headYaw/onGround`.

**Verification that the tracker is player-agnostic:** `[VERIFIED: server/tracker.go:45-108]` `entityTracker.Tick` iterates `t.players`, calls `t.entities.near()`, skips `e.id == p.entityID` (so a player never tracks itself), and emits AddEntity/Teleport/Remove. It has NO mob-specific logic — it works for any Entity in the store. The handoff's "tracker only handles mobs" is imprecise: the tracker is generic; mobs are simply the only things currently IN the store. The fix is data (put players in the store), not tracker logic.

### Pattern 2: GAMEPLAY-02 — apply the persisted position

**What:** `loadPlayer` already returns `save.PlayerData` with `Pos [3]float64` (`persistence.go:55-73` writes it, `TestPlayerDataRoundTrip` proves the round-trip). The join bootstrap hardcodes spawn (`gameplay_tick.go:251-257` applies only health/food/saturation; `play_join.go:480-492` hardcodes `8.5/surfaceY+2/8.5`). Apply the loaded pos to the player AND the bootstrap teleport.

**When to use:** at `AcceptPlayer`, before `sendPlayBootstrap`, when `loadPlayer` returns `ok=true`.

**Recommended approach (cleanest):** thread the loaded pos into `bootstrapParams` so `sendPlayBootstrap` teleports the client to the persisted position directly (one teleport, no re-issue). The `tickPlayer.x/y/z` and `center` must also be set from the loaded pos so `flushOutbound` streams the correct ring. `[VERIFIED: gameplay_tick.go:168-257]` — the loaded data is available before the `player := &tickPlayer{...}` construction, so set `player.x/y/z`, `player.center = chunkCenterOf(...)`, and pass the pos into the bootstrap.

**Pitfall (teleport gate):** the bootstrap teleport id is stored in `awaitingTeleport` and the gate (`subtick.go:112`) drops movement until the client echoes it. Applying a persisted pos must reuse the SAME single bootstrap teleport — do NOT issue a second teleport after register (that would re-arm `confirmedTeleport=false` and risk a rubber-band). The `performRespawn` re-teleport pattern (`combat.go:155-162`) is the reference if a post-register teleport is ever needed, but for join the cleanest path is to feed the pos into the existing bootstrap teleport.

### Pattern 3: GAMEPLAY-03 — inventory join-sync

**What:** `sendContent(p)` (`inventory.go:80`) builds and sends the authoritative `ContainerSetContent`. It is called by the inventory handlers but NEVER at join, so the client's window 0 is empty until the player clicks. Call `sendContent(p)` once on the first tick after the player is registered.

**When to use:** first tick after `drainRegistrations` inserts the player. Use a per-player `bootstrapped bool` flag (or reuse an existing first-tick hook) so it fires exactly once.

**Where:** the cleanest seam is a first-tick block in the tick pipeline (e.g. at the top of `tickEntities` or a dedicated post-register step) guarded by a flag on `tickPlayer`. `[VERIFIED: inventory.go:80-85]` — `sendContent` calls `ensureInventory(p)` so it is safe even for a player with a nil inventory (sends an empty-but-valid window, which is what survival expects).

### Pattern 4: GAMEPLAY-04 — damage dispatch + fall damage

**What:** `applyInput` (`subtick.go:116-207`) routes movement/break/place/inventory packets but has NO case for `ServerboundAttack` or `ServerboundInteract` — they hit `default:` (no-op). Note `dispatch` (`tick.go:771-772`) ALREADY routes `ServerboundAttack`/`ServerboundInteract` into the subtick buffer, so the packets arrive — they are just dropped at `applyInput`'s default. Add cases that resolve the target entity + call `t.applyDamage`.

**When to use:** in `applyInput`, add `case packetid.ServerboundInteract:` (the attack-an-entity packet) and `case packetid.ServerboundAttack:`.

**Attack resolution:** decode the `ServerboundInteract` packet (jar: `ServerboundInteractPacket` = VarInt entityId + Action enum {INTERACT, ATTACK, INTERACT_AT} + optional fields + Boolean usingSecondaryAction). On `ATTACK`, resolve `entityId` to the target via `t.entities.get(id)`, validate reach, and apply the vanilla base attack damage. For PvP, the target Entity is a player Entity (GAMEPLAY-01 must land first so player Entities exist in the store), and damage must route to that player's `tickPlayer.applyDamage` — so the plan needs an Entity-id → tickPlayer reverse lookup (the `tickPlayer.entityID` field exists; build a small map or scan `t.players`).

**Fall damage (environmental tick):** `[VERIFIED: no fall-damage code exists]`. Vanilla fall damage = `floor(fallDistance - 3.0)` half-hearts when the entity lands (`onGround` transitions false→true with accumulated fall distance). Port `Entity.checkFallDamage`/`LivingEntity.causeFallDamage` from the jar. Track `fallDistance` per player in `tickPhysics` (accumulate `-vy` while airborne, reset + apply damage on landing). This is a tick-side environmental damage source feeding the same `applyDamage`.

```go
// In applyInput, after the teleport gate:
case packetid.ServerboundInteract:
    var targetID pk.VarInt
    var action pk.VarInt      // 0=INTERACT, 1=ATTACK, 2=INTERACT_AT (jar enum order)
    if err := in.Packet.Scan(&targetID, &action /*…*/); err != nil { return }
    if int(action) != interactActionAttack { return }
    victim := t.lookupPlayerByEntityID(int32(targetID)) // GAMEPLAY-01 reverse map
    if victim == nil || !t.withinAttackReach(p, victim) { return }
    t.applyDamage(victim, baseAttackDamage) // baseAttackDamage = 1.0 (bare-hand, jar)
```

### Pattern 5: GAMEPLAY-05 — vanilla fluid simulation port (the large one)

**What:** `tickWorld()` (`tick_phases.go:113`) is an empty stub. Port three things from the jar: (A) a **scheduled-block-tick queue** (the world has none), (B) the `FlowingFluid` flow algorithm, (C) player fluid physics.

**(A) Scheduled-block-tick queue (net-new infrastructure).** `[VERIFIED: world/manager.go has no scheduleTick]`. Vanilla `ServerLevel.scheduleTick(pos, fluid, delay)` enqueues a tick for `gametime + delay`. Build a tick-owned min-heap or per-tick bucket map keyed by `(gametime+delay) → []scheduledFluidTick{pos}`. In `tickWorld`, drain all entries due this `gametime` and call the fluid `tick` for each. When a fluid block is placed/updated, schedule its next tick with the fluid's `getTickDelay`.

**(B) `FlowingFluid` algorithm (verified bytecode this session).** The classes are at `net.minecraft.world.level.material.FlowingFluid` (abstract), `WaterFluid`, `LavaFluid` `[VERIFIED: javap]`. The methods to port, in order of importance:

- **`tick(level, pos, blockState, fluidState)`** `[VERIFIED: javap -c]` — if the fluid is NOT a source: compute `getNewLiquid`, and if it differs (or is empty → set AIR), apply it via `setBlock` + `scheduleTick(getSpreadDelay)`. THEN always call `spread`.
- **`spread(level, pos, state, fluid)`** `[VERIFIED: javap -c]` — the structure: first try to flow DOWN (`pos.below()`): if `canMaybePassThrough` down, compute the new liquid for the cell below, and if it can be replaced + the block can hold fluid, `spreadTo` DOWN (a falling column). If `sourceNeighborCount(pos) >= 3`, ALSO `spreadToSides`. If the down-cell can't take it, and (this fluid is a source OR the down-block is not a "water hole"), `spreadToSides`.
- **`getNewLiquid(level, pos, state)`** — the heart: examines the 4 horizontal neighbors + the block above, finds the highest fluid level that can reach this cell, decrements by `getDropOff` (water=1), applies the source-conversion rule (`canConvertToSource` + ≥2 source neighbors → becomes a source), and the falling rule (fluid above → falling, full level). 134 lines of bytecode — port faithfully; this is the determinism-critical method.
- **`getSlopeDistance` / `getSpread`** — the 8-direction slope-find that makes water flow toward the nearest drop-off within `getSlopeFindDistance` (water=4). Used by `spreadToSides` to bias flow.

**(C) Verified constants (port these exactly):**

| Constant | Water | Lava (overworld) | Source |
|----------|-------|------------------|--------|
| `getSlopeFindDistance` | 4 | (separate) | `[VERIFIED: javap WaterFluid → iconst_4]` |
| `getDropOff` | 1 | (2 typical) | `[VERIFIED: javap WaterFluid → iconst_1]` |
| `getTickDelay` | 5 | (30 typical) | `[VERIFIED: javap WaterFluid → iconst_5]` |
| `getAmount` (source) | 8 | 8 | `[VERIFIED: getLegacyLevel uses amount, source→8]` |
| `canConvertToSource` | gamerule `WATER_SOURCE_CONVERSION` | gamerule | `[VERIFIED: javap WaterFluid]` |

**(D) Level encoding (verified — the wire mapping for `block.Water{Level}`):**
```
// Source: javap FlowingFluid.getLegacyLevel (verified bytecode)
legacyLevel = isSource ? 0
            : (8 - min(amount, 8)) + (falling ? 8 : 0)
// block.Water{Level: N}: N=0 is source (full); N=1..7 flowing (decreasing); N=8..15 falling.
// amount 8 = source/full; each horizontal spread step: amount -= getDropOff (water:1).
```
Read the current level via `block.StateList[id]` type-asserted to `block.Water`; write via `block.ToStateID[block.Water{Level: n}]`. `[VERIFIED: level/block/block.go:24-27, blocks.go:68-70]`

**(E) Waterlogged handling.** `[VERIFIED: blocks.go:65,118,…]` — many blocks carry a `Waterlogged Boolean`. A waterlogged block's `getFluidState` is a full water source. For v1 fluid sim, treat a waterlogged block as a source for neighbor flow and do NOT overwrite the block (only its fluid state). Minimal faithful subset: handle waterlogging as a read-only source contributor; full waterlog place/break interaction can be a documented stub if scope demands.

**(F) Player fluid physics (26.2 refactor — IMPORTANT package move).** `[VERIFIED: javap]` The method the additional_context named `Entity.updateFluidHeightAndDoFluidPushing` was **renamed/refactored in 26.2** into `Entity.updateFluidInteraction()` which delegates to a new `EntityFluidInteraction.update(entity, …)` / `applyCurrentTo(tag, entity, scale)` class. The water push scale is `0.014` (verified: `ldc2_w double 0.014d` in `updateFluidInteraction`). Movement constants live in `LivingEntity`: `getWaterSlowDown()` returns `0.8` (verified bytecode), plus `WATER_DRAG`, `BASE_SWIM_SPEED`, `SWIMMING_VERTICAL_SPEED` static finals. Minimal faithful subset for the visual gate: (1) detect "in water" (eye/feet fluid height via the player AABB vs fluid blocks), (2) apply `0.8` horizontal slowdown + buoyancy/reduced gravity when submerged, (3) `goDownInWater`/jump-in-liquid for the swim-up impulse. The `travelInFluid` path in `LivingEntity.travel` is the reference. This is the one MEDIUM-confidence sub-area: the constants are verified but the exact integration into Sulfur's `collidePlayer`/`tickPhysics` path is a port-and-tune task.

**Anti-pattern:** do NOT implement fluids as "scan all loaded water every tick and average neighbors." That neither matches vanilla propagation timing (`getTickDelay`/`getSpreadDelay`) nor is deterministic per the parity mandate. Use the scheduled-tick queue + `getNewLiquid`.

### Pattern 6: GAMEPLAY-06 — block-break item drops (depends on GAMEPLAY-01)

**What:** `handlePlayerAction` (`block_interact.go:81-116`) sets the broken block to air, acks, and broadcasts BlockUpdate — then returns. It never spawns a drop. After a successful break, look up the block's drop item, create an `entity.Item` (ID 71) Entity at the block center with a small random velocity, and `t.entities.add(it)` so the (GAMEPLAY-01-enabled) tracker broadcasts AddEntity to nearby clients.

**When to use:** in `handlePlayerAction` after `reconcileEdit`, before returning.

**Item entity construction:**
```go
// Source: data/entity/entity.go:659 (Item ID 71, w/h 0.25); server/entity.go
ie := NewEntity(t.idAlloc.AllocID(), entity.Item, cx, cy, cz) // block center + 0.5
// Item entities require SetEntityData carrying the ITEM slot metadata (the stack to render)
// — encodeSetEntityData currently emits the empty body (entity_encode.go:222). For an item to
// render its stack, the Item entity's metadata slot (index 8, the ITEM data accessor) must
// carry the SlotData. This is the one new metadata entry the plan must add.
t.entities.add(ie)
```

**Pitfall (Item metadata):** unlike a mob (which renders with empty metadata), an Item entity renders nothing without its ITEM data-value (the stack). `[VERIFIED: entity_encode.go:170-239]` — `encodeSetEntityData` already supports passing `entityDataEntry` values and splicing `e.metadata`; the plan must populate the Item's metadata with the ITEM serializer entry (jar: `Item` entity data accessor index, `EntityDataSerializers.ITEM_STACK`). Confirm the index + serializer id via javap on `net.minecraft.world.entity.item.ItemEntity` `DATA_ITEM`.

**Drop lookup:** for v1, a 1:1 block→item drop (e.g. stone → cobblestone, dirt → dirt) is acceptable. Full loot-table evaluation is STRUCT-POLISH-01 (shared evaluator) — the plan should note the overlap and either (a) ship a minimal hardcoded block→drop map now and wire the loot evaluator in STRUCT-POLISH-01, or (b) coordinate so GAMEPLAY-06 uses the loot evaluator if 01 lands first. Recommend (a): minimal map now, documented as superseded by STRUCT-POLISH-01.

### Anti-Patterns to Avoid

- **Re-issuing a second teleport for GAMEPLAY-02:** apply the persisted pos to the EXISTING bootstrap teleport, not a post-register second one (rubber-band / gate re-arm risk).
- **Double-adding the player Entity:** add it once in `drainRegistrations`, remove once in `removePlayer`. Never in `AcceptPlayer` (that crosses the tick boundary — TICK-05 violation).
- **Forgetting the tab-list broadcast (GAMEPLAY-01):** AddEntity alone does NOT render a player avatar without a preceding broadcast `PlayerInfoUpdate(ADD_PLAYER)`.
- **Brute-force fluid scan:** use the scheduled-tick queue, not a per-tick full-water scan.
- **Item entity with empty metadata:** it spawns invisible. Populate the ITEM data-value.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Fluid flow rules | A "plausible" water-spread heuristic | Port `FlowingFluid.getNewLiquid`/`spread`/`getSlopeDistance` from the jar | Vanilla fluid is a specific source-rule + slope-find + level-decrement algorithm; the plan-checker decompiles the jar to verify fidelity (mandate) |
| Fluid level → wire `level` property | A hand-guessed mapping | Port `getLegacyLevel` (`source?0 : (8-min(amount,8)) + (falling?8:0)`) | `[VERIFIED]` exact bytecode; guessing produces visually-wrong water heights |
| Entity broadcast (player + item) | A new broadcast path | The existing `entityTracker` + `encodeAddEntity`/etc. | `[VERIFIED]` already jar-derived + capture-diff-sealed in 06-07; the tracker is entity-type-agnostic |
| Damage/death/respawn | Re-implement health math | The existing `applyDamage`/`die`/`performRespawn` (`combat.go`) | Already complete, tested, and the wire packets are jar-verified — GAMEPLAY-04 is pure dispatch |
| Inventory window send | A new packet builder | The existing `sendContent` (`inventory.go:80`) | Already correct; GAMEPLAY-03 is a single missing call site |
| Player water physics constants | Tuned-by-feel drag/buoyancy | Port `getWaterSlowDown=0.8`, `WATER_DRAG`, `BASE_SWIM_SPEED`, push `0.014` | `[VERIFIED]` exact jar values; feel-tuning drifts from vanilla parity |

**Key insight:** Five of six seams are reconnect-the-wire, not build-new. The ONLY net-new logic is GAMEPLAY-05's fluid algorithm + scheduled-tick queue + player fluid physics — and all three are direct jar ports with verified entry points and constants.

## Runtime State Inventory

> Not a rename/refactor/migration phase. The persistence touchpoint below is the only stored-state interaction.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | Player `.dat` files (`world/playerdata/<uuid>.dat`) already store `Pos`/`Inventory`/`Health` `[VERIFIED: persistence.go:55-129]` | None — GAMEPLAY-02/03 READ existing fields; no migration. Existing `.dat` files are forward-compatible (already contain Pos). |
| Live service config | None | None |
| OS-registered state | None | None |
| Secrets/env vars | `SULFUR_DEBUG`/`SULFUR_DEBUG_DAMAGE` env triggers exist (`debug.go`) — GAMEPLAY-04 replaces the debug-only damage path with real dispatch | The debug damage trigger stays (off by default); real Attack/Interact dispatch is additive. |
| Build artifacts | None | None |

## Common Pitfalls

### Pitfall 1: Player avatar invisible even after AddEntity (GAMEPLAY-01)
**What goes wrong:** Players are added to the store, the tracker emits AddEntity, but other clients still don't render the avatar.
**Why it happens:** The client drops an AddEntity for a player type (156) if there is no `PlayerInfoUpdate(ADD_PLAYER)` tab-list entry for that UUID. `[VERIFIED: minecraft.wiki FAQ]`
**How to avoid:** Broadcast the joiner's `PlayerInfoUpdate(ADD_PLAYER)` to all existing players AND send all existing players' entries to the joiner, BEFORE/at the AddEntity. Mirror with `REMOVE_PLAYER` on leave.
**Warning signs:** the player shows in the tab list but not in-world (or vice versa).

### Pitfall 2: Fluid sim non-determinism / infinite spread (GAMEPLAY-05)
**What goes wrong:** Water flows forever, oscillates, or differs run-to-run.
**Why it happens:** Skipping the `getDropOff` level decrement (infinite spread), skipping `getTickDelay` scheduling (oscillation), or iterating the scheduled set in nondeterministic map order.
**How to avoid:** Port `getNewLiquid` faithfully (the decrement + source rule terminate spread); drain the scheduled-tick queue in a deterministic order (sort by packed pos within a tick); use the verified constants.
**Warning signs:** water never settles; chunk re-loads change water shape.

### Pitfall 3: Double teleport / rubber-band on position load (GAMEPLAY-02)
**What goes wrong:** A reconnecting player spawns at the persisted pos then snaps back to origin (or movement is frozen).
**Why it happens:** Issuing a second teleport after `register` re-arms `confirmedTeleport=false` while the client's echo targets the first teleport id.
**How to avoid:** Feed the persisted pos into the SINGLE bootstrap teleport (pre-register); set `tickPlayer.x/y/z` + `center` from it.
**Warning signs:** "Loading terrain" hang or position snap on reconnect.

### Pitfall 4: Player Entity tracked by itself / stale player pos (GAMEPLAY-01)
**What goes wrong:** A player sees a ghost of itself, or remote players appear frozen at spawn.
**Why it happens:** The player Entity's id ≠ `tickPlayer.entityID` (self-skip fails), or the Entity pos isn't synced each tick.
**How to avoid:** Construct the player Entity with `id == p.entityID`; sync pos via `store.move` in `tickEntities` (before `tracker.Tick`). The tracker already skips `e.id == p.entityID` (`tracker.go:68`).
**Warning signs:** self-ghost; frozen remote avatars.

### Pitfall 5: Item entity invisible (GAMEPLAY-06)
**What goes wrong:** Block breaks, a drop entity is created and tracked, but nothing renders on the ground.
**Why it happens:** `encodeSetEntityData` emits an empty metadata body; an Item entity needs its ITEM data-value (the stack) to render.
**How to avoid:** Populate the Item Entity's `metadata` with the ITEM serializer entry (verify index + serializer id via `javap net.minecraft.world.entity.item.ItemEntity`).
**Warning signs:** AddEntity reaches the client (entity id exists) but the model is invisible.

### Pitfall 6: Attack hits the wrong target / nil deref (GAMEPLAY-04)
**What goes wrong:** PvP attack does nothing or panics.
**Why it happens:** `ServerboundInteract` carries an ENTITY id, but `applyDamage` operates on a `tickPlayer`. Without an entity-id → tickPlayer reverse lookup the victim can't be resolved; a nil victim derefs.
**How to avoid:** Build a reverse lookup (scan `t.players` for `entityID == targetID`, or a small map); guard nil; validate reach. GAMEPLAY-01 must land first so player Entities exist in the store for the id space to be meaningful.
**Warning signs:** attacks no-op; tick panic recovered in logs.

## Code Examples

### GAMEPLAY-03: inventory join-sync (the one-line seam)
```go
// In a first-tick-after-register hook (guarded by a per-player flag):
// Source: server/inventory.go:80 (sendContent already exists and is correct)
if !p.bootstrapped {
    t.sendContent(p) // sends authoritative ContainerSetContent for window 0
    p.bootstrapped = true
}
```

### GAMEPLAY-05: fluid level read/write (verified mapping)
```go
// Source: javap FlowingFluid.getLegacyLevel + level/block/block.go:24-27
func waterLevelOf(id block.StateID) (level int, isWater bool) {
    if w, ok := block.StateList[id].(block.Water); ok {
        return int(w.Level), true // 0=source, 1..7 flowing, 8..15 falling
    }
    return 0, false
}
func waterStateID(level int) block.StateID {
    return block.ToStateID[block.Water{Level: level}]
}
```

### GAMEPLAY-05: scheduled-tick drain in tickWorld (the new pass)
```go
// Source: ports ServerLevel.scheduleTick + FlowingFluid.tick
func (t *TickLoop) tickWorld() {
    t.trace("tickWorld")
    if t.world == nil { return }
    for _, st := range t.drainScheduledFluidTicks(t.gametime) { // deterministic order
        state, ok := t.world.GetBlock(st.pos, dimMinY)
        if !ok { continue }
        t.fluidTick(st.pos, state) // port of FlowingFluid.tick: getNewLiquid → setBlock → spread
    }
}
```

## State of the Art

| Old Approach (≤1.20) | Current Approach (26.2) | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `Entity.updateFluidHeightAndDoFluidPushing` | `Entity.updateFluidInteraction()` + `EntityFluidInteraction` class | 26.x refactor | `[VERIFIED: javap]` The method named in the handoff/additional-context no longer exists; port the new class. Water push scale `0.014`. |
| Separate `SpawnPlayer` packet | `AddEntity` (type 156) + required `PlayerInfoUpdate` | 1.20.2 | `[VERIFIED]` Players spawn via the same AddEntity path as mobs, but the tab-list entry is mandatory. The existing `encodeAddEntity` works for players. |

**Deprecated/outdated:**
- Any community-wiki fluid doc for ≤1.21.10 may predate the `EntityFluidInteraction` split — trust the jar.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | A minimal block→drop map (stone→cobblestone etc.) is acceptable for GAMEPLAY-06 v1, with full loot tables deferred to STRUCT-POLISH-01 | Pattern 6 | Low — drops still appear; just not loot-table-accurate. Planner/user can confirm scope. |
| A2 | Fall damage (`floor(fallDist - 3.0)` half-hearts) is the minimum environmental damage for GAMEPLAY-04; fire/drowning/etc. are out of scope for Phase 17 | Pattern 4 | Low — REQUIREMENTS.md says "fall damage at minimum"; broader env damage is implicitly later. |
| A3 | Waterlogged blocks can be treated as read-only source contributors for the v1 fluid sim (no full waterlog place/break interaction) | Pattern 5 (E) | Medium — if the visual gate requires waterlogging interaction (e.g. placing in water), this is a gap. Confirm with the gate scope. |
| A4 | Lava simulation can reuse the same `FlowingFluid` port with Lava's constants, but is lower priority than water for the GAMEPLAY-05 gate (REQUIREMENTS.md says "water simulates") | Pattern 5 | Low — the algorithm is shared; lava is mostly a constant swap. |
| A5 | The Item entity ITEM metadata index/serializer can be obtained from `javap net.minecraft.world.entity.item.ItemEntity` at plan time (not yet decompiled this session) | Pattern 6 | Medium — if the index is mis-set the item renders invisibly (Pitfall 5). The plan MUST decompile ItemEntity.DATA_ITEM. |

## Open Questions

1. **Exact Item entity metadata layout (ITEM data-value index + serializer id)**
   - What we know: `encodeSetEntityData` supports custom entries; Item is ID 71.
   - What's unclear: the precise SynchedEntityData index for the ITEM accessor in 26.2 and the `EntityDataSerializers.ITEM_STACK` id.
   - Recommendation: the plan's GAMEPLAY-06 task MUST run `javap -c -p net.minecraft.world.entity.item.ItemEntity` (find `DATA_ITEM` / `defineSynchedData`) before implementing — capture-diff against a real client if uncertain.

2. **Tab-list broadcast scope for GAMEPLAY-01 (skins/properties)**
   - What we know: an empty properties array is acceptable (default skin by UUID); offline-mode sends 0 properties already (`play_join.go:379`).
   - What's unclear: whether the visual gate (GAMEPLAY-07) requires real skins (which need ONLINE-01 textures) or accepts default skins.
   - Recommendation: ship default-skin tab broadcast now (0 properties); real skins arrive with ONLINE-01 (Phase 18). This is consistent with offline-mode being the Phase 17 default.

3. **Fluid sim trigger: how does already-placed worldgen water start flowing?**
   - What we know: worldgen places water at sea level (aquifers); those blocks are never scheduled for a fluid tick.
   - What's unclear: whether to schedule fluid ticks for worldgen water on chunk-load (could be expensive) or only on neighbor changes (break/place adjacent).
   - Recommendation: schedule a fluid tick when a fluid block is created/updated AND when an adjacent block changes (break/place) — matching vanilla `neighborChanged`. Pre-existing ocean stays static until disturbed (vanilla behavior — oceans don't re-flow on load). Confirm this matches the gate expectation.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `javap` (jar decompile) | GAMEPLAY-05/06 port verification | ✓ | Zulu 25 (`/c/Program Files/Zulu/zulu-25/bin/javap`) | — |
| `temp/cache/26.2-inner.jar` | port-from-jar mandate | ✓ | 26.2 (24.9 MB, unobfuscated) | — |
| Go toolchain | build/test | ✓ | 1.26.1 | — |
| Docker | `-race` gate (host CGO=0) | ✓ (per CLAUDE.md) | 29 | `go test ./...` (non-race) locally |
| Real vanilla 26.2 client | GAMEPLAY-07 visual gate | ✓ (operator's PrismLauncher) | 26.2 / proto 776 | — |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** `-race` requires Docker on this CGO=0 host (per HANDOFF constraint #4: `docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race -timeout 1800s ./...`).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (table tests + `_test.go` siblings) |
| Config file | none (Go convention; CI in `.github/workflows/go.yml`) |
| Quick run command | `go test ./server/...` |
| Full suite command | `go test ./...` (CI: line 59); race: `go test -race ./...` (CI line 66, run via Docker on this host) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| GAMEPLAY-01 | Player added to store on join; tracker emits AddEntity for a second player; PlayerInfoUpdate broadcast to others; removed on leave | unit | `go test ./server/ -run TestPlayerVisibility -x` | ❌ Wave 0 |
| GAMEPLAY-02 | Persisted Pos applied to the bootstrap teleport + tickPlayer.x/y/z/center | unit | `go test ./server/ -run TestPositionLoadApplied -x` | ❌ Wave 0 (round-trip `TestPlayerDataRoundTrip` exists; the APPLY test is new) |
| GAMEPLAY-03 | `sendContent` fires exactly once on first tick after register | unit | `go test ./server/ -run TestInventoryJoinSync -x` | ❌ Wave 0 |
| GAMEPLAY-04 | Attack/Interact dispatch calls applyDamage; fall damage on landing | unit | `go test ./server/ -run 'TestAttackDispatch|TestFallDamage' -x` | ❌ Wave 0 (`combat_test.go` covers applyDamage/die/respawn; the DISPATCH test is new) |
| GAMEPLAY-05 | `getNewLiquid`/`getLegacyLevel` parity; water spreads + settles; level decrement; scheduled-tick ordering deterministic; player slowed in water | unit | `go test ./server/ -run 'TestFluid' -x` | ❌ Wave 0 |
| GAMEPLAY-06 | Break spawns an Item entity at block center; tracker broadcasts it; Item metadata carries the stack | unit | `go test ./server/ -run TestBlockDrop -x` | ❌ Wave 0 |
| GAMEPLAY-07 | Two clients see each other move; pos/inventory survive reconnect; attacks damage + death/respawn; water flows + affects movement; broken blocks drop pickable items | manual (VISUAL GATE) | human — real vanilla 26.2 client | N/A (human gate) |

### Sampling Rate
- **Per task commit:** `go test ./server/...` (the package all six seams touch)
- **Per wave merge:** `go test ./...`
- **Phase gate:** full suite green + Docker `-race ./server/... ./world/...` clean, THEN the GAMEPLAY-07 visual gate (human-confirmed, like Phases 5/13/16)

### Wave 0 Gaps
- [ ] `server/player_visibility_test.go` — covers GAMEPLAY-01 (store add, tracker emits, tab broadcast, leave-remove)
- [ ] `server/position_load_test.go` — covers GAMEPLAY-02 (apply, not just round-trip)
- [ ] `server/inventory_join_test.go` — covers GAMEPLAY-03 (fires once)
- [ ] `server/attack_dispatch_test.go` + fall-damage test — covers GAMEPLAY-04 dispatch (extends existing `combat_test.go`)
- [ ] `server/fluid_test.go` + `server/fluid_schedule_test.go` — covers GAMEPLAY-05 (`getNewLiquid` parity table, settle, deterministic drain, player slowdown)
- [ ] `server/block_drop_test.go` — covers GAMEPLAY-06 (Item spawned + metadata + tracked)
- [ ] Determinism note: the fluid `getNewLiquid` parity test should be a table of (neighbor levels → expected new level) lifted from the jar's algorithm — the worldgen-style PORT-EXACT gate.

*(Existing infra: `combat_test.go` (damage math), `TestPlayerDataRoundTrip` (persistence), `TestTickPhaseOrder` (pipeline order — MUST stay green; the new `tickWorld` fluid body + tab broadcast must NOT reorder phases).)*

## Security Domain

> `security_enforcement` not present in config → treat as enabled. Phase 17 is server-internal gameplay; the relevant controls are server-authority (anti-cheat), already established.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Offline-mode in Phase 17; auth is ONLINE-01 (Phase 18) |
| V3 Session Management | no | Connection lifecycle unchanged |
| V4 Access Control | yes | Server-authoritative damage/inventory/edits: client cannot set its own health (T-6-05), inventory hashes are discarded (T-6-02), edits are reach-gated (T-6-01). GAMEPLAY-04 must keep applyDamage server-driven; the attack packet only NAMES a target, never sets damage. |
| V5 Input Validation | yes | Every new packet decode (`ServerboundInteract`) must be defensive: Scan-error → silent no-op (the established `applyInput` contract, T-6-04), bound the target id, validate reach. |
| V6 Cryptography | no | None in Phase 17 |

### Known Threat Patterns for {Go MC server, gameplay tick}

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Forged attack target / self-damage spoof | Tampering/Elevation | Server resolves the victim by entity id + reach-validates; client never supplies damage amount (reuse `applyDamage`) |
| Attack-rate flood (DoS) | Denial of Service | Attack packets already route through the bounded subtick buffer (`subtickCap=256`, drop-oldest) — no new unbounded path |
| Fluid-sim work amplification (place water → unbounded scheduled ticks) | Denial of Service | The scheduled-tick queue is bounded by loaded chunks + `getTickDelay` spacing; cap per-tick fluid work and only schedule on real changes (Open Question 3) |
| Reach/teleport exploit on position load | Tampering | Reuse the single bootstrap teleport + confirm gate; do not trust client-claimed position |

## Sources

### Primary (HIGH confidence)
- `temp/cache/26.2-inner.jar` via `javap -c -p` — `FlowingFluid` (spread/tick/getNewLiquid/getLegacyLevel/getSlopeDistance), `WaterFluid` (getSlopeFindDistance=4, getDropOff=1, getTickDelay=5, canConvertToSource), `LavaFluid`, `Entity.updateFluidInteraction` (push 0.014), `LivingEntity.getWaterSlowDown=0.8` + water-drag/swim constants, `EntityFluidInteraction` — all decompiled this session
- Repo code (verified by direct read): `server/tick.go` (drainRegistrations:699, dispatch:760, tickPlayer:264), `server/tracker.go` (generic entityTracker), `server/tick_phases.go:113` (tickWorld stub), `server/subtick.go:116` (applyInput dispatch — no Attack/Interact case), `server/combat.go` (complete, uncalled), `server/inventory.go:80` (sendContent), `server/block_interact.go:81` (break, no drop), `server/persistence.go` (Pos round-trips), `server/gameplay_tick.go:246` (pos-load explicitly skipped), `server/play_join.go:337` (self-only PlayerInfoUpdate), `server/entity_store.go`, `server/entity_encode.go`, `data/entity/entity.go:659,1424`, `level/block/block.go`+`blocks.go`, `world/manager.go` (no scheduler)
- `.github/workflows/go.yml` (test + race commands)

### Secondary (MEDIUM confidence)
- minecraft.wiki Java_Edition_protocol/FAQ — "PlayerInfoUpdate (Player List Item) must precede AddEntity or Notchian clients won't spawn the player entity" (the GAMEPLAY-01 tab-list subtlety) `[CITED: https://minecraft.wiki/w/Java_Edition_protocol/FAQ]`

### Tertiary (LOW confidence)
- (none — all critical claims verified against jar or live code)

## Metadata

**Confidence breakdown:**
- GAMEPLAY-01/02/03/04/06 (the five seam reconnects): HIGH — verified the seam locations, the existing-but-uncalled logic, and the exact field shapes against live code; the one non-obvious risk (tab-list broadcast for 01, Item metadata for 06) is surfaced and cited.
- GAMEPLAY-05 fluid algorithm: MEDIUM-HIGH — methods + constants verified via jar bytecode; it is a net-new subsystem (scheduled-tick queue + algorithm port + player physics), so execution risk is real even though the source is authoritative.
- GAMEPLAY-05 player fluid physics: MEDIUM — constants verified, but 26.2 refactored the class (`EntityFluidInteraction`) and integration into Sulfur's existing `collidePlayer`/`tickPhysics` is a port-and-tune.

**Research date:** 2026-06-25
**Valid until:** stable for this milestone (the jar is pinned; the only volatility is if 26.3 retargets the fluid package — re-verify via javap at that point).
