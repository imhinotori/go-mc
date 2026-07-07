package server

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// block_ticks.go — SUB-BLOCKTICK: the server-side wiring of the general scheduled-block-tick
// subsystem (level/ticks.LevelTicks) into the tick loop, plus its first real consumer
// (SugarCaneBlock). This is the Go port of the net.minecraft.server.level.ServerLevel side of
// the vanilla scheduled-tick flow:
//
//	ServerLevel.scheduleTick(pos, block, delay[, priority])  -> blockTicks.schedule(createTick(...))
//	ServerLevel.tick: blockTicks.tick(gameTime, 65536, this::tickBlock)
//	ServerLevel.tickBlock(pos, block): if (getBlockState(pos).is(block)) state.tick(level, pos, random)
//
// CITE: ServerLevel.scheduleTick / ServerLevel.tick / ServerLevel.tickBlock; Level.createTick /
// Level.nextSubTickCount; LevelTicks.tick.
//
// ============================================================================================
// DECISION — the existing fluid loop (fluid_schedule.go / fluid.go) COEXISTS, it is NOT migrated.
// ============================================================================================
// Sulfur already ships a self-contained one-off scheduled-fluid loop (a per-gametime bucket map,
// fluidScheduleQueue) that drains in tickFluids. That loop is a faithful slice of FlowingFluid
// behavior and currently WORKS. Migrating fluids onto this general LevelTicks would mean porting
// the fluid `i` type identity, the Fluid.tick dispatch, and re-validating every water-spread
// test — out of scope for v1 and a needless regression risk. So in v1 the two coexist:
//   - blockTicks (this file) is the GENERAL LevelTicks<Block> for scheduled BLOCK ticks
//     (sugar cane now; redstone/crops/etc later), the vanilla ServerLevel.blockTicks.
//   - fluidSchedule (fluid_schedule.go) remains the bespoke fluid loop, the de-facto
//     ServerLevel.fluidTicks, untouched.
// The on-disk format unifies them anyway (both serialize via save.SavedTickNBT into the chunk's
// block_ticks / fluid_ticks lists), so a future plan can migrate fluids onto LevelTicks without
// changing the save format. Until then: no fluid behavior changes, no broken water.

// blockTickType is the payload type T of the block-tick LevelTicks — a block's identity, keyed
// by its resource-location string (the vanilla ScheduledTick<Block> type is the Block singleton;
// the on-disk codec stores it as the block id, so the id IS the stable identity and the natural
// comparable Go key). A scheduled block tick says "re-evaluate the block of THIS kind at THIS
// pos at THIS time" — exactly vanilla's (Block, BlockPos) pair. CITE: ScheduledTick<Block> /
// SavedTick.codec `i` field.
type blockTickType string

const (
	// sugarCaneTickType is the block id sugar-cane ticks are scheduled/dispatched under.
	sugarCaneTickType blockTickType = "minecraft:sugar_cane"

	// redstoneTorchTickType / redstoneWallTorchTickType are the block ids the redstone-torch burnout
	// toggle (RedstoneTorchBlock.tick) is scheduled/dispatched under. A standing torch and a wall torch
	// are distinct block ids but share the same tick handler. CITE: RedstoneTorchBlock /
	// RedstoneWallTorchBlock.
	redstoneTorchTickType     blockTickType = "minecraft:redstone_torch"
	redstoneWallTorchTickType blockTickType = "minecraft:redstone_wall_torch"

	// repeaterTickType / comparatorTickType are the block ids the diode delayed output flip
	// (DiodeBlock.tick / ComparatorBlock.tick) is scheduled/dispatched under (REDSTONE TIER-2). CITE:
	// RepeaterBlock / ComparatorBlock.
	repeaterTickType   blockTickType = "minecraft:repeater"
	comparatorTickType blockTickType = "minecraft:comparator"

	// detectorRailTickType is the block id the detector-rail re-check (DetectorRailBlock.tick ->
	// checkPressed) is scheduled/dispatched under: a POWERED detector rail schedules a tick 20 later that
	// re-checks whether a minecart is still on it and, if not, clears POWERED. CITE: DetectorRailBlock.tick
	// (`if (!POWERED) return; checkPressed(...)`) + checkPressed's `level.scheduleTick(pos, this, 20)`.
	detectorRailTickType blockTickType = "minecraft:detector_rail"

	// redstoneLampTickType is the block id the redstone-lamp DELAYED UNLIGHT is scheduled/dispatched
	// under: a LIT lamp that loses its neighbor signal schedules a tick 4 later (RedstoneLampBlock
	// .neighborChanged) that, if still lit and still unpowered, clears LIT (RedstoneLampBlock.tick).
	redstoneLampTickType blockTickType = "minecraft:redstone_lamp"

	// targetTickType is the block id the TargetBlock output-power reset (TargetBlock.tick) is scheduled
	// under: a hit target schedules a tick (20 ticks for an arrow, 8 for other projectiles) that resets
	// OUTPUT_POWER to 0. CITE: TargetBlock.setOutputPower (scheduleTick(pos, this, activationTicks)).
	targetTickType blockTickType = "minecraft:target"

	// daylightDetectorTickType is the block id the DaylightDetector signal recompute (daylightTick ->
	// updateSignalStrength) is scheduled under. The detector self-reschedules onto the next game-time
	// multiple of 20, matching DaylightDetectorBlockEntity.tickEntity (gameTime % 20 == 0). CITE:
	// DaylightDetectorBlock.tickEntity.
	daylightDetectorTickType blockTickType = "minecraft:daylight_detector"

	// tripwireTickType is the block id both the tripwire (TripWireBlock.tick) and the tripwire-hook
	// (TripWireHookBlock.tick) recheck ticks are scheduled under. A pressed wire reschedules 10 ticks
	// out; a released wire schedules a 0-tick recheck. calculateState also schedules the changed wire.
	// Both blocks share the handler (routed by the state at pos). CITE: TripWireBlock.checkPressed /
	// TripWireHookBlock.calculateState (scheduleTick(pos, block, 10)).
	tripwireTickType blockTickType = "minecraft:tripwire"

	// fireTickType is the block id the FIRE spread/burn-out tick (FireBlock.tick) is scheduled/
	// dispatched under. Fire is a SCHEDULED ticker: onPlace / tick reschedule via scheduleTick(pos,
	// this, getFireTickDelay()==30+nextInt(10)), so it drains through this general LevelTicks (like
	// sugar cane / redstone), NOT the random-tick driver. CITE: FireBlock.onPlace / FireBlock.tick.
	fireTickType blockTickType = "minecraft:fire"

	// sandTickType / redSandTickType / gravelTickType are the block ids the FallingBlock tick
	// (FallingBlock.tick -> FallingBlockEntity.fall) is scheduled/dispatched under. Each FallingBlock
	// schedules under its OWN block id (FallingBlock.onPlace / updateShape -> scheduleTick(pos, this,
	// getDelayAfterPlace()==2)), so tickBlock must route all three to the shared fallingBlockTick
	// handler. sand/red_sand are SandBlock, gravel is ColoredFallingBlock -- all extend FallingBlock.
	// CITE: FallingBlock.onPlace / FallingBlock.tick; Blocks.SAND/RED_SAND/GRAVEL.
	sandTickType    blockTickType = "minecraft:sand"
	redSandTickType blockTickType = "minecraft:red_sand"
	gravelTickType  blockTickType = "minecraft:gravel"
)

// lightningRodTickTypes is the set of block ids the lightning-rod unpower tick (LightningRodBlock.tick)
// is scheduled/dispatched under — one per oxidation/wax variant. A rod struck by lightning schedules
// under its OWN block id (onLightningStrike -> scheduleTick(pos, this, 8)), so tickBlock must route any
// of them to lightningRodTick. All 8 variants share the same tick handler (POWERED->false + neighbor
// update). CITE: LightningRodBlock.onLightningStrike (scheduleTick(pos, this, ACTIVATION_TICKS=8)) /
// LightningRodBlock.tick.
var lightningRodTickTypes = map[blockTickType]struct{}{
	"minecraft:lightning_rod":                 {},
	"minecraft:exposed_lightning_rod":         {},
	"minecraft:weathered_lightning_rod":       {},
	"minecraft:oxidized_lightning_rod":        {},
	"minecraft:waxed_lightning_rod":           {},
	"minecraft:waxed_exposed_lightning_rod":   {},
	"minecraft:waxed_weathered_lightning_rod": {},
	"minecraft:waxed_oxidized_lightning_rod":  {},
}

// isLightningRodTickType reports whether a scheduled tick type is one of the lightning-rod block ids.
func isLightningRodTickType(typ blockTickType) bool {
	_, ok := lightningRodTickTypes[typ]
	return ok
}

// buttonTickTypes is the set of block ids the button-unpress tick (ButtonBlock.tick) is scheduled
// under — one per button variant. Each button schedules under its own id, so tickBlock must route
// any of them to buttonTick. CITE: ButtonBlock.press (scheduleTick(pos, this, ticksToStayPressed)).
var buttonTickTypes = map[blockTickType]struct{}{
	"minecraft:stone_button":               {},
	"minecraft:polished_blackstone_button": {},
	"minecraft:oak_button":                 {},
	"minecraft:spruce_button":              {},
	"minecraft:birch_button":               {},
	"minecraft:jungle_button":              {},
	"minecraft:acacia_button":              {},
	"minecraft:cherry_button":              {},
	"minecraft:dark_oak_button":            {},
	"minecraft:pale_oak_button":            {},
	"minecraft:mangrove_button":            {},
	"minecraft:bamboo_button":              {},
	"minecraft:crimson_button":             {},
	"minecraft:warped_button":              {},
}

// isButtonTickType reports whether a scheduled tick type is one of the button block ids.
func isButtonTickType(typ blockTickType) bool {
	_, ok := buttonTickTypes[typ]
	return ok
}

// maxAllowedBlockTicks is net.minecraft.server.level.ServerLevel's drain cap — the int 65536
// passed to blockTicks.tick(gameTime, 65536, this::tickBlock). It bounds how many scheduled
// ticks may FIRE in a single game-time; an overflowing backlog is rescheduled to the next
// game-time (LevelTicks.rescheduleLeftoverContainers) rather than firing unboundedly. CITE:
// ServerLevel.tick (`blockTicks.tick(gameTime, 65536, ...)`).
const maxAllowedBlockTicks = 65536

// ensureBlockTicks lazily constructs the level-wide block-tick manager. The per-chunk tick gate
// (vanilla ServerLevel::shouldTickBlocksAt) is "is this chunk loaded?" in v1 — a chunk with a
// container registered is by definition loaded/tickable, so the gate returns true (a future
// plan can tighten it to the real tick-distance check). Tick-owned.
func (t *TickLoop) ensureBlockTicks() *ticks.LevelTicks[blockTickType] {
	if t.cur().blockTicks == nil {
		t.cur().blockTicks = ticks.NewLevelTicks[blockTickType](func(chunkKey int64) bool {
			return true // v1: a chunk with a registered container is tickable (loaded)
		})
	}
	return t.cur().blockTicks
}

// nextSubTick is net.minecraft.world.level.Level.nextSubTickCount(): post-increment the level
// sub-tick counter, returning the value BEFORE the increment (so the first draw is 0). It feeds
// each ScheduledTick's subTickOrder. CITE: Level.nextSubTickCount (`return subTickCount++`).
func (t *TickLoop) nextSubTick() int64 {
	v := t.cur().blockTickSubCounter
	t.cur().blockTickSubCounter++
	return v
}

// registerChunkBlockTicks adds a chunk's per-chunk tick container to the level manager when the
// chunk becomes ready, and UNPACKS any on-disk pending ticks against the current game-time
// (LevelChunkTicks.unpack), so a loaded chunk's saved ticks become live. If the chunk had no
// saved ticks it registers an empty container. CITE: ServerLevel chunk-load wiring
// (addContainer + unpack). Tick-owned; safe to call once per chunk becoming ready.
func (t *TickLoop) registerChunkBlockTicks(pos level.ChunkPos, container *ticks.LevelChunkTicks[blockTickType]) {
	mgr := t.ensureBlockTicks()
	mgr.AddContainer(pos[0], pos[1], container)
	container.Unpack(t.gametime)
}

// ensureChunkBlockTicks registers an EMPTY per-chunk tick container for a chunk that does not
// already have one, so live scheduleTick calls inside that chunk are not dropped. Vanilla's
// ServerLevel registers a LevelChunkTicks container for EVERY loaded chunk (generated or read
// from disk) via ChunkHolder/chunk-load wiring — not only for chunks that carried saved ticks.
// Sulfur previously only registered a container on the persist-load path (loadChunkBlockTicks),
// so a freshly GENERATED/STREAMED chunk had no container and LevelTicks.Schedule silently
// dropped every tick scheduled in it (e.g. the sugar-cane cascade never fired). This is the
// missing generic registration. Idempotent: a chunk that already has a container (e.g. a
// persist-loaded one seeded with saved ticks) is left untouched. Tick-owned. CITE: ServerLevel
// chunk-load wiring (addContainer for every loaded chunk).
func (t *TickLoop) ensureChunkBlockTicks(pos level.ChunkPos) {
	mgr := t.ensureBlockTicks()
	if mgr.Container(pos[0], pos[1]) != nil {
		return // already registered (saved-tick container or a prior call) — keep it
	}
	mgr.AddContainer(pos[0], pos[1], ticks.NewLevelChunkTicks[blockTickType]())
}

// scheduleBlockTick is net.minecraft.world.level.ScheduledTickAccess.scheduleTick(pos, block,
// delay) (the 3-arg form, NORMAL priority): build a ScheduledTick at triggerTick = gameTime +
// delay with a fresh subTickOrder and enqueue it. A tick for an unloaded chunk is dropped by
// LevelTicks.schedule (no container). CITE: ScheduledTickAccess.scheduleTick(pos, block, delay)
// -> Level.createTick -> LevelTicks.schedule.
func (t *TickLoop) scheduleBlockTick(pos pk.Position, typ blockTickType, delay int) {
	t.scheduleBlockTickWithPriority(pos, typ, delay, ticks.PriorityNormal)
}

// scheduleBlockTickWithPriority is the 4-arg scheduleTick(pos, block, delay, priority). CITE:
// ScheduledTickAccess.scheduleTick(pos, block, delay, priority).
func (t *TickLoop) scheduleBlockTickWithPriority(pos pk.Position, typ blockTickType, delay int, priority ticks.TickPriority) {
	mgr := t.ensureBlockTicks()
	tick := ticks.NewScheduledTick(typ, pos, t.gametime+int64(delay), priority, t.nextSubTick())
	mgr.Schedule(tick)
}

// hasScheduledBlockTick reports whether (pos, typ) already has a pending scheduled tick — the
// vanilla LevelTickAccess.hasScheduledTick guard a block uses before re-scheduling (so it does
// not pile up duplicate ticks). CITE: LevelTicks.hasScheduledTick.
func (t *TickLoop) hasScheduledBlockTick(pos pk.Position, typ blockTickType) bool {
	if t.cur().blockTicks == nil {
		return false
	}
	return t.cur().blockTicks.HasScheduledTick(pos, typ)
}

// willTickThisTick reports whether (pos, typ) is already selected to FIRE in the in-progress drain —
// the LevelTickAccess.willTickThisTick guard RedstoneTorchBlock.neighborChanged uses so it does not
// schedule a redundant toggle for a torch that is about to tick anyway. A nil manager (nothing ever
// scheduled) is false. CITE: LevelTicks.willTickThisTick.
func (t *TickLoop) willTickThisTick(pos pk.Position, typ blockTickType) bool {
	if t.cur().blockTicks == nil {
		return false
	}
	return t.cur().blockTicks.WillTickThisTick(pos, typ)
}

// packChunkBlockTicks serializes a chunk's pending block ticks to the on-disk SavedTickNBT list
// (the chunk's `block_ticks` field), packing each live tick's absolute triggerTick back to a
// relative delay against the current game-time (LevelChunkTicks.pack -> SavedTick.toSavedTick).
// A chunk with no registered container or no ticks yields nil (the save shape omits the field).
// This is the SAVE half of the chunk tick round-trip; a chunk-flush caller folds the result into
// the chunk's block_ticks before serializing. CITE: LevelChunkTicks.pack.
func (t *TickLoop) packChunkBlockTicks(pos level.ChunkPos) []save.SavedTickNBT {
	if t.cur().blockTicks == nil {
		return nil
	}
	container := t.cur().blockTicks.Container(pos[0], pos[1])
	if container == nil {
		return nil
	}
	saved := container.Pack(t.gametime)
	if len(saved) == 0 {
		return nil
	}
	out := make([]save.SavedTickNBT, 0, len(saved))
	for _, s := range saved {
		out = append(out, save.SavedTickNBT{
			ID:       string(s.Type),
			X:        int32(s.Pos.X),
			Y:        int32(s.Pos.Y),
			Z:        int32(s.Pos.Z),
			Delay:    s.Delay,
			Priority: int32(s.Priority.Value()),
		})
	}
	return out
}

// loadChunkBlockTicks builds a per-chunk tick container seeded from the on-disk SavedTickNBT list
// (a freshly-LOADED chunk's `block_ticks`), then registers it with the level manager and unpacks
// it against the current game-time so the saved ticks become live. An empty/nil list registers an
// empty container (so the chunk can still receive live schedules). This is the LOAD half of the
// chunk tick round-trip. CITE: LevelChunkTicks(List) + addContainer + unpack.
func (t *TickLoop) loadChunkBlockTicks(pos level.ChunkPos, savedTicks []save.SavedTickNBT) {
	saved := make([]ticks.SavedTick[blockTickType], 0, len(savedTicks))
	for _, s := range savedTicks {
		saved = append(saved, ticks.SavedTick[blockTickType]{
			Type:     blockTickType(s.ID),
			Pos:      pk.Position{X: int(s.X), Y: int(s.Y), Z: int(s.Z)},
			Delay:    s.Delay,
			Priority: ticks.PriorityByValue(int(s.Priority)),
		})
	}
	container := ticks.NewLevelChunkTicksFromSaved(saved)
	t.registerChunkBlockTicks(pos, container)
}

// tickScheduledBlocks is the SUB-BLOCKTICK drain phase — the Go port of the ServerLevel.tick
// "tickPending"/"blockTicks" section: blockTicks.tick(gameTime, 65536, this::tickBlock). It
// drains every block tick due at the current game-time (bounded at 65536) in the vanilla
// deterministic order and dispatches each to tickBlock. A nil manager (no chunk ever registered
// a container) is a cheap no-op. Tick-owned; called from tickWorld (no new tick phase — keeps
// TestTickPhaseOrder green), BEFORE the fluid pass so block ticks precede fluid ticks exactly as
// ServerLevel.tick drains blockTicks before fluidTicks. CITE: ServerLevel.tick block/fluid drain
// order; LevelTicks.tick.
func (t *TickLoop) tickScheduledBlocks() {
	if t.cur().blockTicks == nil {
		return
	}
	t.cur().blockTicks.Tick(t.gametime, maxAllowedBlockTicks, t.tickBlock)
}

// tickBlock is net.minecraft.server.level.ServerLevel.tickBlock(pos, block): re-read the block
// state at pos and, ONLY if it is still the scheduled block kind (`state.is(block)`), invoke the
// block's tick handler. A block that changed/was removed since the tick was scheduled fires
// nothing (the stale-tick guard). CITE: ServerLevel.tickBlock (`BlockState s = getBlockState(
// pos); if (s.is(block)) s.tick(this, pos, this.random)`).
func (t *TickLoop) tickBlock(pos pk.Position, typ blockTickType) {
	if t.world() == nil {
		return
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return // unloaded/out-of-range: nothing to tick (getBlockState would be air, not `block`)
	}
	switch typ {
	case sugarCaneTickType:
		if !block.IsSugarCane(state) {
			return // state.is(SUGAR_CANE) false: stale tick, fire nothing
		}
		t.sugarCaneTick(state, pos)
	case redstoneTorchTickType, redstoneWallTorchTickType:
		// ServerLevel.tickBlock stale guard: only tick if still a redstone torch of the scheduled kind.
		if !block.IsRedstoneTorch(state) && !block.IsRedstoneWallTorch(state) {
			return
		}
		t.redstoneTorchTick(state, pos)
	case repeaterTickType:
		// ServerLevel.tickBlock stale guard: only tick if still a repeater (REDSTONE TIER-2).
		if !block.IsRepeater(state) {
			return
		}
		t.repeaterTick(state, pos)
	case comparatorTickType:
		if !block.IsComparator(state) {
			return
		}
		t.comparatorTick(state, pos)
	case observerTickType:
		// ServerLevel.tickBlock stale guard: only tick if still an observer (REDSTONE TIER-3, observer.go).
		if !block.IsObserver(state) {
			return
		}
		t.observerTick(state, pos)
	case detectorRailTickType:
		// ServerLevel.tickBlock stale guard: only tick if still a detector rail (minecart.go). The scheduled
		// tick re-checks whether a cart is still on the rail and clears POWERED if not. CITE: DetectorRailBlock.tick.
		if !block.IsDetectorRailBlock(state) {
			return
		}
		t.detectorRailCheckPressed(pos, state)
	case dispenserTickType, dropperTickType:
		// ServerLevel.tickBlock stale guard: only tick if still a dispenser-family block (REDSTONE TIER-4,
		// dispenser.go). A dispenser/dropper broken/replaced since the TRIGGERED tick was scheduled fires
		// nothing. CITE: ServerLevel.tickBlock (`state.is(block)`).
		if !t.tickBlockDispenserGuard(state) {
			return
		}
		t.dispenserTick(state, pos)
	case redstoneLampTickType:
		// RedstoneLampBlock.tick (the scheduled 4-tick delayed unlight): `if (LIT && !hasNeighborSignal)
		// setBlock(cycle(LIT), 3)`. The stale guard is IsRedstoneLamp; a lamp re-powered within the 4 ticks
		// (still lit, now has signal) leaves LIT alone. CITE: RedstoneLampBlock.tick.
		if !block.IsRedstoneLamp(state) {
			return
		}
		if block.LampLit(state) && !t.hasNeighborSignal(pos) {
			if off, ok := block.LampWithLit(state, false); ok {
				if t.world().SetBlock(pos, off, dimMinY) {
					t.broadcastBlockUpdate(pos, off)
				}
			}
		}
	case targetTickType:
		// TargetBlock.tick stale guard: only tick if still a target block. The scheduled tick resets
		// OUTPUT_POWER to 0 (redstone_blocks.go). CITE: ServerLevel.tickBlock / TargetBlock.tick.
		if !block.IsTargetBlock(state) {
			return
		}
		t.targetTick(state, pos)
	case daylightDetectorTickType:
		// DaylightDetector stale guard: only tick if still a daylight detector. The tick recomputes POWER
		// from the sky brightness and self-reschedules (redstone_blocks.go). CITE: DaylightDetectorBlock.tickEntity.
		if !block.IsDaylightDetector(state) {
			return
		}
		t.daylightTick(state, pos)
	case tripwireTickType:
		// TripWire/TripWireHook stale guard: the scheduled tick is shared by both blocks (both schedule
		// under minecraft:tripwire); route by the state at pos. A wire recheck (TripWireBlock.tick) or a
		// hook recalc (TripWireHookBlock.tick) fires depending on which block is there now. CITE:
		// ServerLevel.tickBlock / TripWireBlock.tick / TripWireHookBlock.tick.
		if block.IsTripwire(state) {
			t.tripwireTick(state, pos)
			return
		}
		if block.IsTripwireHook(state) {
			t.tripwireHookCalculateState(pos, state, false, -1, 0, false)
			return
		}
		return
	case fireTickType:
		// ServerLevel.tickBlock stale guard: only tick if still fire (any age/attach flags). A fire that
		// burned out / was extinguished since the tick was scheduled fires nothing. Routes to the FireBlock
		// spread/burn-out handler (fire_block.go). The scheduled-block drain runs on the coordinator, so the
		// handler resolves the world-global region for its level random. CITE: ServerLevel.tickBlock.
		if !block.IsFire(state) {
			return
		}
		t.fireTick(t.only(), state, pos)
	case sandTickType, redSandTickType, gravelTickType:
		// ServerLevel.tickBlock stale guard: only tick if still a FallingBlock kind (sand/red_sand/gravel).
		// A FallingBlock broken/replaced since the tick was scheduled fires nothing. Routes to the shared
		// FallingBlock.tick handler (falling_block.go). CITE: ServerLevel.tickBlock (`state.is(block)`).
		if !isFallingBlockKind(state) {
			return
		}
		t.fallingBlockTick(state, pos)
	default:
		// Buttons schedule under their own block id (13 variants). Route any button tick to the unpress
		// handler; the IsButton guard is the tickBlock `state.is(block)` stale check.
		if isButtonTickType(typ) {
			if !block.IsButton(state) {
				return
			}
			t.buttonTick(state, pos)
			return
		}
		// Lightning rods schedule their unpower under their own block id (8 oxidation/wax variants). Route
		// any of them to the rod unpower handler; the IsLightningRod guard is the tickBlock `state.is(block)`
		// stale check (a rod broken/replaced since the strike fires nothing). CITE: LightningRodBlock.tick.
		if isLightningRodTickType(typ) {
			if !block.IsLightningRod(state) {
				return
			}
			t.lightningRodTick(state, pos)
			return
		}
		// Leaves schedule their DISTANCE-recompute tick under their own block id (11 leaf variants). Route
		// any of them to the leaves tick handler; the IsLeaves guard is the tickBlock state.is(block) stale
		// check (a leaf broken/replaced since updateShape scheduled the tick fires nothing). CITE:
		// LeavesBlock.updateShape (scheduleTick(pos, this, TICK_DELAY)) / LeavesBlock.tick.
		if isLeavesTickType(typ) {
			if !block.IsLeaves(state) {
				return
			}
			t.leavesTick(state, pos)
			return
		}
		// Unknown scheduled type (a future block whose handler is not yet ported): no-op. The
		// tick was still dequeued, matching vanilla's `is(block)` guard failing for a stale type.
	}
}
