# Fluid Spread & Block-Update Propagation — 26.2 Jar Spec (protocol 776)

**Source of truth:** `temp/cache/26.2-inner.jar`, read with
`JAVAP="/c/Program Files/Zulu/zulu-25/bin/javap"; "$JAVAP" -c -p -classpath D:/ender/temp/cache/26.2-inner.jar <FQCN>`.
Every numeric constant, flag, call order, and method below is transcribed from the bytecode of that jar — not paraphrased. Re-express in idiomatic Go; do not change observable behavior.

This document answers two coupled bugs reported on `ender-776`:
- **(A)** Placed water does not expand on its own (only flows when blocks are hand-edited).
- **(B)** Breaking a block does not propagate the update to its 6 neighbors, so e.g. breaking the sugar-cane below does not cascade the cane above.

---

## 0. The flag bit field (the single most important fact)

`Level.setBlock(pos, state, flags)` semantics are entirely flag-driven. Decoded from `Level.setBlock(pos,state,flags,recursionLeft)` bytecode (see §1):

| Bit | Value | Name | Effect (from bytecode) |
|----:|------:|------|------------------------|
| 1 | `0x01` | `UPDATE_NEIGHBORS` | `iload_3; iconst_1; iand; ifeq` → if set, calls `updateNeighborsAt(pos, oldBlock)` (the 6-neighbor `neighborChanged` fan-out) + analog-output update. **This is what wakes fluids and topples plants.** |
| 2 | `0x02` | `UPDATE_CLIENTS` | `iload_3; iconst_2; iand; ifeq` → if set, `sendBlockUpdated(pos,old,new,flags)` (push to tracking clients). |
| 4 | `0x04` | `UPDATE_INVISIBLE` / client suppression | combined with bit 2 on the client side; server-irrelevant. |
| 8 | `0x08` | `UPDATE_KNOWN_SHAPE` | when set, SUPPRESSES the `updateNeighbourShapes` self/indirect shape recompute (the `bipush 16` block below also gates it). |
| 16 | `0x10` | `UPDATE_SKIP_SHAPE_UPDATE` (a.k.a. skip-shape) | `iload_3; bipush 16; iand; ifne 232` → if set, SKIP the `updateNeighbourShapes`/`updateIndirectNeighbourShapes` (the `updateShape` dispatch). |
| 32 | `0x20` | `UPDATE_SUPPRESS_DROPS` | read elsewhere (`updateOrDestroy`: `dropBlock = (flags & 32) == 0`). |
| 64 | `0x40` | `UPDATE_MOVE_BY_PISTON` | piston-specific. |
| 512 | `0x200` | `UPDATE_LIMIT` (recursion seed) | `setBlock(pos,state,flags)` calls `setBlock(pos,state,flags,512)` — 512 is the initial `recursionLeft` for shape-update recursion. |

Two flag values that matter for these bugs:
- **`3` = `UPDATE_NEIGHBORS|UPDATE_CLIENTS`** — `setBlockAndUpdate`, and what `FlowingFluid.tick`/`spreadTo` use (`iconst_3`).
- **`11` = `UPDATE_NEIGHBORS|UPDATE_CLIENTS|UPDATE_KNOWN_SHAPE`** — what `BucketItem.emptyContents` uses (`bipush 11`). Note bit 1 is SET → placing water from a bucket DOES notify neighbors.

---

# CÓMO EL AGUA SE EXPANDE (scheduling)

The water sim is a **scheduled-tick** system, not a per-tick scan. A water cell only re-evaluates when a tick has been *scheduled* for its position. The schedule is seeded from three places: `onPlace`, `neighborChanged`, and `tick` itself (re-arming). If none of those fire, the water never moves — exactly bug (A).

## 1. `Level.setBlock` — the entry point that arms everything

**`net.minecraft.world.level.Level`**

```
public boolean setBlock(BlockPos, BlockState, int flags):
    return setBlock(pos, state, flags, 512);          // 512 = UPDATE_LIMIT recursion seed
```

`setBlock(pos, state, flags, recursionLeft)` bytecode (transcribed):

```
if (!isInValidBounds(pos)) return false;
if (isClientSide() && isDebug()) return false;
LevelChunk chunk = getChunkAt(pos);
Block newBlock = state.getBlock();
BlockState old = chunk.setBlockState(pos, state, flags);   // <-- markAndNotifyBlock lives in here:
                                                           //     fires old.onRemove + new.onPlace
if (old == null) return false;                             // (no change)
BlockState current = getBlockState(pos);
if (current == state) {                                    // chunk actually applied our state
    if (old != current) setBlocksDirty(pos, old, current);

    // --- bit 2: UPDATE_CLIENTS ---
    if ((flags & 2) != 0 &&
        (!isClientSide() || (flags & 4) == 0) &&
        (isClientSide() || (chunk.getFullStatus()!=null &&
                            chunk.getFullStatus().isOrAfter(BLOCK_TICKING)))) {
        sendBlockUpdated(pos, old, state, flags);
    }

    // --- bit 1: UPDATE_NEIGHBORS ---  *** THE BLOCK-UPDATE FAN-OUT ***
    if ((flags & 1) != 0) {
        updateNeighborsAt(pos, old.getBlock());            // <-- 6-neighbor neighborChanged
        if (!isClientSide() && state.hasAnalogOutputSignal())
            updateNeighbourForOutputSignal(pos, newBlock);
    }

    // --- bit 16 clear: updateShape dispatch (the updateShape() call chain) ---
    if ((flags & 16) == 0 && recursionLeft > 0) {
        int f2 = flags & ~34;                              // clear bits 2 and 32 for recursion
        old.updateIndirectNeighbourShapes(this, pos, f2, recursionLeft-1);
        state.updateNeighbourShapes(this, pos, f2, recursionLeft-1);   // <-- 6-dir updateShape
        state.updateIndirectNeighbourShapes(this, pos, f2, recursionLeft-1);
    }

    updatePOIOnBlockStateChange(pos, old, state);
}
return true;
```

**Two distinct propagation mechanisms come out of `setBlock`:**
1. **bit 1 → `updateNeighborsAt`** → `Block.neighborChanged` on each neighbor (redstone, fluids re-scheduling, *some* plant survival — but NOT sugar cane; see §B).
2. **bit 16 clear → `updateNeighbourShapes`** → `BlockState.updateShape` on each neighbor (this is the path SugarCane/most plants actually use). `updateShape` returns a possibly-changed state and can `scheduleTick`.

`old.onPlace`/`new.onPlace` are NOT gated by any flag — they run inside `LevelChunk.setBlockState` unconditionally whenever the block at the cell becomes a new block. **That is why placing water flows even though onPlace is flag-independent: `LiquidBlock.onPlace` always runs.**

## 2. `LiquidBlock.onPlace` — arms the FIRST fluid tick on placement

**`net.minecraft.world.level.block.LiquidBlock`**

```
protected void onPlace(BlockState state, Level level, BlockPos pos, BlockState old, boolean moved):
    if (shouldSpreadLiquid(level, pos, state)) {
        level.scheduleTick(pos,
                           state.getFluidState().getType(),     // the Fluid
                           this.fluid.getTickDelay(level));      // WaterFluid.getTickDelay -> 5
    }
    if (shouldBubbleColumnOccupy(state)) {                       // (magma/soul-sand column; v1 skip)
        BlockState below = level.getBlockState(pos.below());
        tryScheduleBubbleBlockColumn(level, pos, below);
    }
```

**`LiquidBlock.neighborChanged` is byte-for-byte identical** to `onPlace` (same `shouldSpreadLiquid → scheduleTick(pos, fluid, getTickDelay)` + bubble-column block). So:

- **Place water** → `setBlock` → `LevelChunk.setBlockState` → `LiquidBlock.onPlace` → `scheduleTick(pos, water, 5)`.
- **A neighbor of a water cell changes** (any `setBlock` with bit 1) → `updateNeighborsAt` → `LiquidBlock.neighborChanged` → `scheduleTick(pos, water, 5)`.

`WaterFluid.getTickDelay` = `iconst_5; ireturn` → **5 ticks**.

## 3. `FlowingFluid.tick` — fires when a scheduled tick comes due

**`net.minecraft.world.level.material.FlowingFluid`**

```
public void tick(ServerLevel level, BlockPos pos, BlockState blockState, FluidState state):
    if (!state.isSource()) {                                  // sources skip straight to spread()
        FluidState newLiquid = getNewLiquid(level, pos, level.getBlockState(pos));
        int delay = getSpreadDelay(level, pos, state, newLiquid);
        if (newLiquid.isEmpty()) {
            state = newLiquid;
            blockState = AIR.defaultBlockState();
            level.setBlock(pos, blockState, 3);              // drained -> air, flags=3
            // fall through to spread() with the (now empty) state
        } else if (newLiquid != state) {
            state = newLiquid;
            blockState = newLiquid.createLegacyBlock();
            level.setBlock(pos, blockState, 3);              // re-level, flags=3
            level.scheduleTick(pos, newLiquid.getType(), delay);   // RE-ARM itself
        }
    }
    spread(level, pos, blockState, state);
}
```

Key: a non-source cell **re-arms its own next tick** (`scheduleTick(pos, fluid, delay)`) whenever its level changes. A source cell does not re-arm but still spreads every time something schedules it.

## 4. `FlowingFluid.spread` / `spreadTo` — the actual expansion

```
protected void spread(ServerLevel level, BlockPos pos, BlockState bs, FluidState state):
    if (state.isEmpty()) return;
    BlockPos below = pos.below();
    BlockState belowState = level.getBlockState(below);
    FluidState belowFluid = belowState.getFluidState();
    if (canMaybePassThrough(level, pos, bs, DOWN, below, belowState, belowFluid)) {
        FluidState newBelow = getNewLiquid(level, below, belowState);
        Fluid t = newBelow.getType();
        if (belowFluid.canBeReplacedWith(level, below, t, DOWN)
            && canHoldSpecificFluid(level, below, belowState, t)) {
            spreadTo(level, below, belowState, DOWN, newBelow);   // flow DOWN (always falling)
            if (sourceNeighborCount(level, pos) >= 3)
                spreadToSides(level, pos, state, bs);            // only when 3+ source neighbors
            return;
        }
    }
    if (state.isSource() || !isWaterHole(...))                  // can't go down -> go sideways
        spreadToSides(level, pos, state, bs);
```

```
protected void spreadTo(LevelAccessor level, BlockPos pos, BlockState bs, Direction dir, FluidState fluid):
    if (bs.getBlock() instanceof LiquidBlockContainer lbc) {
        lbc.placeLiquid(level, pos, bs, fluid);                  // waterlogging
    } else {
        if (!bs.isAir()) beforeDestroyingBlock(level, pos, bs); // break+drop the occupant
        level.setBlock(pos, fluid.createLegacyBlock(), 3);      // <-- setBlock(...,3) => onPlace => scheduleTick
    }
```

`spreadTo` uses **flags=3** → the freshly-placed flow's `onPlace` arms ITS next tick, so the wave keeps propagating one cell per 5 ticks until `getNewLiquid` returns empty (termination).

### Scheduling flow summary (place → flow):
```
emptyContents/place water
  -> Level.setBlock(pos, water_source, 11)             (bit1 set)
       -> LevelChunk.setBlockState -> LiquidBlock.onPlace
            -> shouldSpreadLiquid? -> scheduleTick(pos, water, 5)
       -> updateNeighborsAt(pos)                         (bit1)
            -> each LiquidBlock neighbor.neighborChanged -> scheduleTick(neighbor, water, 5)
  ... 5 ticks later ...
  -> FlowingFluid.tick(pos) -> spread -> spreadTo(down/side, ...,3)
       -> setBlock(...,3) -> onPlace -> scheduleTick(newCell, water, 5)
  ... repeats until getNewLiquid == empty
```

## 5. `BucketItem.emptyContents` — places water with flags=11

**`net.minecraft.world.item.BucketItem`** (`emptyContents(LivingEntity, Level, BlockPos, BlockHitResult)`):

```
if (!(content instanceof FlowingFluid)) return false;
BlockState bs = level.getBlockState(pos);
Block b = bs.getBlock();
boolean replaceable = bs.canBeReplaced(content);
boolean canPlace = bs.isAir() || replaceable
                   || (b instanceof LiquidBlockContainer lbc
                       && lbc.canPlaceLiquid(entity, level, pos, bs, content));
if (!canPlace)
    return hit != null && emptyContents(entity, level, hit.getBlockPos().relative(hit.getDirection()), null);
// (water-evaporates dimension branch omitted: plays FIRE_EXTINGUISH + LARGE_SMOKE, returns true)
if (b instanceof LiquidBlockContainer lbc && content.is(FluidTags.WATER)) {
    lbc.placeLiquid(level, pos, bs, content.defaultFluidState());   // waterlog
} else {
    if (!bs.isAir()) level.destroyBlock(pos, true);
    level.setBlock(pos, content.defaultFluidState().createLegacyBlock(), 11);   // <-- bipush 11
}
playEmptySound(...);
return true;
```

**`flags=11` = UPDATE_NEIGHBORS|UPDATE_CLIENTS|UPDATE_KNOWN_SHAPE.** Bit 1 is SET → the 6 neighbors get `neighborChanged`, AND the placed block's own `onPlace` runs (flag-independent). Both arm a `scheduleTick`. THIS is the answer to "does emptying a bucket schedule the fluid to flow?": **yes — via `onPlace` (always) and `updateNeighborsAt` (bit 1).** No explicit `scheduleTick` in `emptyContents` itself; it comes from `onPlace`/`neighborChanged`.

---

# CÓMO SE PROPAGA EL BLOCK-UPDATE A VECINOS

There are **two independent fan-outs** off `Level.setBlock`. Both must exist for full vanilla behavior.

## A. `updateNeighborsAt` → `neighborChanged` (the bit-1 path)

**`net.minecraft.server.level.ServerLevel`** (the `Level` base `updateNeighborsAt(pos,block,orientation)` is `{ return; }` — an empty stub; the real impl is on `ServerLevel`):

```
public void updateNeighborsAt(BlockPos pos, Block block):
    updateNeighborsAt(pos, block, ExperimentalRedstoneUtils.initialOrientation(this, null, null));

public void updateNeighborsAt(BlockPos pos, Block block, Orientation orientation):
    this.neighborUpdater.updateNeighborsAtExceptFromFacing(pos, block, null, orientation);
```

**`net.minecraft.world.level.redstone.CollectingNeighborUpdater`**:

```
public void updateNeighborsAtExceptFromFacing(BlockPos pos, Block block, Direction skip, Orientation o):
    addAndRun(pos, new MultiNeighborUpdate(pos.immutable(), block, o, skip));
```

`MultiNeighborUpdate.runNext(level)` iterates `NeighborUpdater.UPDATE_ORDER` (the 6 Directions in a fixed order), and for each direction `d != skip`:

```
BlockPos np = sourcePos.relative(d);
BlockState ns = level.getBlockState(np);
... orientation bookkeeping (REDSTONE_EXPERIMENTS feature flag) ...
NeighborUpdater.executeUpdate(level, ns, np, sourceBlock, orientation, false);
```

**`net.minecraft.world.level.redstone.NeighborUpdater.executeUpdate`** (static):

```
try { state.handleNeighborChanged(level, pos, sourceBlock, orientation, false); }
catch (Throwable t) { throw new ReportedException(CrashReport "Exception while updating neighbours"); }
```

**`BlockBehaviour$BlockStateBase.handleNeighborChanged`**:

```
public void handleNeighborChanged(Level level, BlockPos pos, Block sourceBlock, Orientation o, boolean movedByPiston):
    getBlock().neighborChanged(asState(), level, pos, sourceBlock, o, movedByPiston);
```

So bit 1 calls, per neighbor: **`<thatNeighborBlock>.neighborChanged(state, level, pos, sourceBlock, orientation, false)`**. The base `Block.neighborChanged` is essentially empty; only blocks that override it react (redstone components, `LiquidBlock` re-scheduling, falling blocks, etc.).

`Level.neighborChanged(state,pos,block,orientation,bool)` on the base class is also `{ return; }`; `ServerLevel.neighborChanged(...)` forwards to `this.neighborUpdater.neighborChanged(state,pos,block,orientation,bool)` (i.e. `CollectingNeighborUpdater`).

## B. `updateNeighbourShapes` → `updateShape` (the bit-16-clear path)

**`BlockBehaviour$BlockStateBase.updateNeighbourShapes(LevelAccessor, BlockPos, int flags, int recursionLeft)`**:

```
MutableBlockPos m = new MutableBlockPos();
for (Direction d : BlockBehaviour.UPDATE_SHAPE_ORDER) {     // WEST,EAST,NORTH,SOUTH,DOWN,UP order
    m.setWithOffset(pos, d);
    level.neighborShapeChanged(d.getOpposite(), m, pos, asState(), flags, recursionLeft);
}
```

`LevelAccessor.neighborShapeChanged → Level.neighborShapeChanged` calls
`neighborState.updateShape(direction, thisState, level, neighborPos, pos, random)` and then
`Block.updateOrDestroy(neighborState, newState, level, neighborPos, flags, recursionLeft)`:

**`net.minecraft.world.level.block.Block.updateOrDestroy`**:

```
if (newState != oldState) {
    if (newState.isAir()) {
        if (!level.isClientSide())
            level.destroyBlock(pos, (flags & 32) == 0 /*dropBlock*/, null, recursionLeft);
    } else {
        level.setBlock(pos, newState, (flags & ~33) | 4 /*…*/, recursionLeft);   // recurse
    }
}
```

`updateShape` is what most plants (incl. **SugarCane**) use to react to a support change — it can return a different state (often AIR, or schedule a destroy tick). It is the **shape** fan-out, distinct from the **neighbor** fan-out.

### The 6-neighbor order constants
- `NeighborUpdater.UPDATE_ORDER` — used by the bit-1 `neighborChanged` fan-out.
- `BlockBehaviour.UPDATE_SHAPE_ORDER` — used by the bit-16 `updateShape` fan-out. (Iterates all 6 directions; cane only cares about DOWN, so order is immaterial for cane.)

---

# POR QUÉ LA CAÑA NO CASCADEA

## SugarCane uses `updateShape`, NOT `neighborChanged`

**`net.minecraft.world.level.block.SugarCaneBlock`** — full method set:

```
protected void tick(BlockState state, ServerLevel level, BlockPos pos, RandomSource r):
    if (!state.canSurvive(level, pos))
        level.destroyBlock(pos, true);                      // dropBlock=true

protected BlockState updateShape(BlockState state, LevelReader level, ScheduledTickAccess ticks,
                                 BlockPos pos, Direction dir, BlockPos npos, BlockState nstate, RandomSource r):
    if (!state.canSurvive(level, pos))
        ticks.scheduleTick(pos, this, 1);                   // SCHEDULE a destroy tick, delay 1
    return super.updateShape(...);                          // returns state unchanged

protected boolean canSurvive(BlockState state, LevelReader level, BlockPos pos):
    BlockState below = level.getBlockState(pos.below());
    if (below.is(this)) return true;                        // cane on cane
    if (!below.is(BlockTags.SUPPORTS_SUGAR_CANE)) return false;   // sand/dirt/etc.
    for (Direction d : Direction.Plane.HORIZONTAL) {        // around pos.below()
        BlockState s = level.getBlockState(pos.below().relative(d));
        FluidState f = level.getFluidState(pos.below().relative(d));
        if (f.is(FluidTags.SUPPORTS_SUGAR_CANE_ADJACENTLY)  // water
            || s.is(BlockTags.SUPPORTS_SUGAR_CANE_ADJACENTLY)) // frosted ice
            return true;
    }
    return false;
```

**Crucially, `SugarCaneBlock` has NO `neighborChanged` override** — `javap -p` lists only `tick`, `randomTick`, `updateShape`, `canSurvive`, `getShape`, `createBlockStateDefinition`. So the bit-1 `updateNeighborsAt`/`neighborChanged` path does **nothing** to sugar cane. The cascade runs ENTIRELY through the bit-16-clear `updateShape` path.

## The faithful cascade chain (break cane below → cane above breaks)

```
Player breaks cane at y               (some break code path)
  -> Level.setBlock(y, AIR, flags)     where flags has bit 1 set AND bit 16 CLEAR
       -> (bit 16 clear) state.updateNeighbourShapes(level, y, flags, recursionLeft)
            -> for DOWN..UP: neighborShapeChanged(...) on each neighbor
            -> the cell at y+1 (cane) gets updateShape(DOWN-from-above, ...):
                 canSurvive(y+1)? support (y) is now AIR -> false
                 -> scheduleTick(y+1, SugarCaneBlock, 1)        // delay 1 tick
  ... 1 tick later, ServerLevel.tick drains blockTicks ...
  -> ServerLevel.tickBlock(y+1, SugarCaneBlock):
       if (getBlockState(y+1).is(SugarCaneBlock))
         state.tick(level, y+1, random)
           -> SugarCaneBlock.tick: !canSurvive(y+1) -> destroyBlock(y+1, true)
                -> setBlock(y+1, AIR, 3)   (bit 1 set, bit 16 clear)
                     -> updateNeighbourShapes -> cane at y+2 schedules its tick ... (CASCADE)
```

So the cascade depends on **the break `setBlock` clearing bit 16** (so `updateNeighbourShapes` runs) and on **the scheduled-block-tick queue being drained each game tick**. If either is missing, cane never topples — bug (B).

`destroyBlock(pos, dropBlock)` itself is `setBlock(pos, AIR, 3)` (bit1|bit2) plus loot/sound; flag 3 has bit 16 clear, so destroying a block ALSO propagates `updateShape` to its neighbors → the recursive cascade.

---

# QUÉ FALTA EN SULFUR

Reviewed: `server/fluid.go`, `server/fluid_schedule.go`, `server/block_interact.go`
(`reconcileEdit` ~L331), `server/block_break.go` (~L370), `server/block_ticks.go`,
`server/sugar_cane.go`, `server/block_survival.go`, `server/tick_phases.go`, `server/block_place.go`.

## What is ALREADY correct and wired (do not "fix" these)
- **The block-update fan-out on edit exists.** `reconcileEdit(editor,pos,state,seq)` (block_interact.go:331) is called from BOTH the place path (block_interact.go:242) AND the break path (block_break.go:370), and it calls, in order:
  - `scheduleFluidNeighborsOnEdit(pos)` (fluid.go:552) — schedules the edited cell (if water) + all 6 neighbors that are water. This is Sulfur's port of `updateNeighborsAt → LiquidBlock.neighborChanged → scheduleTick`. **Correct.**
  - `updateVegetationOnEdit(pos)` (block_survival.go) — flower/sapling/grass survival above.
  - `onBlockTickEdit(pos)` (sugar_cane.go:141) — checks the cell ABOVE; if it is sugar cane and `!sugarCaneCanSurvive`, `scheduleBlockTick(above, sugarCaneTickType, 1)`. This is the port of the `updateShape → scheduleTick(pos,this,1)` half. **Correct.**
- **The scheduled-block-tick queue is drained every tick:** `tick_phases.go:132 t.tickScheduledBlocks()` then `:133 t.tickFluids()`. `sugarCaneTick` (sugar_cane.go:45) re-runs `onBlockTickEdit(pos)` after destroying, so the cane column DOES cascade upward in the current code.
- **The fluid scheduler** (`fluidScheduleQueue`, `scheduleFluidTick` = `gametime + waterTickDelay(=5)`, `spreadTo` → `scheduleFluidTick`) faithfully mirrors `getTickDelay=5` and the spread re-arm.

So the cane-cascade machinery and the fluid-neighbor scheduling are present **for block-edits that route through `reconcileEdit`**. A plain block break/place SHOULD already cascade cane and wake adjacent water.

## The real gaps

### GAP 1 (bug A, primary) — Water bucket placement is entirely unimplemented
- `server/item_use.go:19` explicitly states buckets are "out of v1 scope"; the use handler gates on FOOD-component presence and is a no-op for non-food items.
- `blockStateForItem(stack)` (block_place.go:40) only resolves **block items** to a `StateID`. A `water_bucket` is not a block item, so `handleUseItemOn` returns at block_interact.go:182 (`!ok`) — **placing a water bucket does nothing**: no water block is ever written, so nothing is ever scheduled, so water never appears (let alone flows).
- **Missing port:** `BucketItem.emptyContents` → `Level.setBlock(pos, water.defaultFluidState().createLegacyBlock(), 11)`. Because Sulfur's `reconcileEdit`/`scheduleFluidNeighborsOnEdit` already replicate the bit-1 effects (onPlace+neighbor scheduling), the correct implementation is:
  1. Detect a water-bucket use-on (a new branch before/beside `blockStateForItem`).
  2. Resolve the target cell exactly as `emptyContents` does (clicked cell if replaceable/air/waterloggable, else `hit.relative(direction)`).
  3. `world.SetBlock(target, waterSourceState, …)`.
  4. Call `reconcileEdit(p, target, waterSourceState, seq)` — which runs `scheduleFluidNeighborsOnEdit`, scheduling the placed source's own tick (it is water) and waking neighbors. **This is the flag-11 / onPlace behavior re-expressed.**
  5. Consume the bucket → empty bucket (the `ItemUtils.createFilledResult` tail).
- Without GAP 1, the user's "agua colocada no se expande sola" is fully explained: **there is no water being placed at all** through the bucket.

### GAP 2 (bug A, secondary) — Confirm worldgen/`createLegacyBlock` water gets scheduled
- `postProcessChunkFluids` (fluid.go:166) is meant to schedule pre-existing flowing water on chunk load (`FlowingFluid.tick` re-derivation). Verify it actually runs for loaded chunks and seeds the schedule; otherwise water that exists from worldgen/region load sits frozen until hand-edited. (The `scheduleFluidTick` plumbing is correct; this is a wiring check.)

### GAP 3 (bug B verification) — Confirm the break/place `setBlock` flags & predicates
The cane cascade code is correct, so if cane still does not topple in-game, the failure is one of:
- **Predicate mismatch:** `block.IsSugarCane`, `block.IsSupportsSugarCane`, `block.IsSupportsSugarCaneAdjacentlyFluid/Block` must resolve to the right 26.2 state IDs. If `IsSugarCane(aboveState)` is false for the real cane state, `onBlockTickEdit` (sugar_cane.go:150) never schedules the destroy tick. Verify these predicates against the generated block-state table.
- **`reconcileEdit` not reached:** confirm the in-game break actually completes through `block_break.go` `tickBlockBreak`→`reconcileEdit` (instant-break vs. staged-dig). If a creative/instant break uses a different SetBlock path that skips `reconcileEdit`, neither the cane nor the fluid neighbors get scheduled. **All world mutations must funnel through `reconcileEdit` (or an equivalent that runs the 3 propagation hooks), mirroring vanilla's unconditional `updateNeighborsAt` + `updateNeighbourShapes` on every `setBlock` with bit1 set / bit16 clear.**

### GAP 4 (architectural, faithful-port note) — Sulfur splits one vanilla mechanism into three hooks
Vanilla has ONE `setBlock` that fans out to (a) `neighborChanged` (bit 1) and (b) `updateShape` (bit 16 clear). Sulfur re-expresses this as three explicit hooks inside `reconcileEdit` (`scheduleFluidNeighborsOnEdit`, `updateVegetationOnEdit`, `onBlockTickEdit`). This is acceptable as long as **every** code path that mutates a block calls `reconcileEdit`. The risk is divergence: any future `SetBlock` that forgets to call `reconcileEdit` silently drops both fan-outs. Recommend funneling all mutations through a single `setBlockWithUpdates(pos,state,flags)` that internally honors the bit-1 / bit-16 flags, so the flag semantics (e.g. flag 11 vs 3, or a flag-16 set that SHOULD suppress shape updates) are modeled rather than hard-coded per call site.

## One-line diagnosis
- **(A)** Water never expands on placement because **water buckets are not implemented** — `emptyContents`/`placeLiquid` is missing, so no `Level.setBlock(...,11)` ever runs and `LiquidBlock.onPlace`'s `scheduleTick(pos, water, 5)` never fires. (The fluid scheduler/spread itself is faithful and would work once a source is actually placed + `reconcileEdit`'d.)
- **(B)** The cane-cascade chain (`updateShape → scheduleTick(pos,this,1) → tick → canSurvive → destroyBlock → recurse`) is correctly ported and wired into `reconcileEdit` + `tickScheduledBlocks`. If cane still doesn't cascade in-game, the cause is upstream of the cascade: either the break doesn't route through `reconcileEdit`, or the `IsSugarCane`/support state-ID predicates are wrong — NOT the cascade logic.

## Verified jar citations
- `Level.setBlock(pos,state,flags)` → `setBlock(...,512)`; `setBlock(...,flags,recursionLeft)` flag decoding (bits 1/2/16, recursionLeft) — `net.minecraft.world.level.Level`.
- `ServerLevel.updateNeighborsAt(pos,block)` → `(pos,block,orientation)` → `CollectingNeighborUpdater.updateNeighborsAtExceptFromFacing` → `MultiNeighborUpdate.runNext` (UPDATE_ORDER) → `NeighborUpdater.executeUpdate` → `BlockStateBase.handleNeighborChanged` → `Block.neighborChanged`.
- `BlockStateBase.updateNeighbourShapes` (UPDATE_SHAPE_ORDER) → `neighborShapeChanged` → `updateShape` + `Block.updateOrDestroy` (`(flags&32)==0` dropBlock).
- `FlowingFluid.tick/spread/spreadTo/getNewLiquid`, `getSpreadDelay`, `WaterFluid.getTickDelay = 5` — `net.minecraft.world.level.material.FlowingFluid` / `WaterFluid`.
- `LiquidBlock.onPlace` / `LiquidBlock.neighborChanged` → `shouldSpreadLiquid` → `Level.scheduleTick(pos, fluid, fluid.getTickDelay(level))` — `net.minecraft.world.level.block.LiquidBlock`.
- `BucketItem.emptyContents` → `Level.setBlock(pos, content.defaultFluidState().createLegacyBlock(), 11)` (`bipush 11`) — `net.minecraft.world.item.BucketItem`.
- `SugarCaneBlock.tick/updateShape/canSurvive` (no `neighborChanged` override) — `net.minecraft.world.level.block.SugarCaneBlock`.
