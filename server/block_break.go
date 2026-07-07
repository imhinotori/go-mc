package server

import (
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
)

// block_break.go — the 1:1 port of the server-authoritative block-break dig-time model from
// net.minecraft.server.level.ServerPlayerGameMode (the START/STOP/ABORT dispatch, the per-tick
// crack-overlay step, getDestroyProgress, incrementDestroyProgress, destroyAndAck, destroyBlock,
// and the ClientboundBlockDestruction broadcast). Replaces the old break-on-STOP-only stand-in in
// block_interact.go: a survival dig now begins a per-tick timer on START, the server streams the
// 0-9 crack overlay as progress accrues against the block's HARDNESS (Part A — level/block/hardness.go),
// instant-mine breaks on START when the 1-tick progress >= 1.0, creative breaks instantly on START,
// and STOP completes when accumulated progress >= 0.7 (else it schedules a delayed-destroy).
//
// ALL state is tick-owned (TICK-05): handleBlockBreakAction and tickBlockBreak run only on the tick
// goroutine over the tick-owned tickPlayer dig fields, so they are -race clean by the same
// single-owner discipline as the rest of the gameplay seams (food/breath/fall-damage). Every method
// and constant is cited against the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), decompiled
// via `javap -c -p` this session.
//
// Cited bytecode (FQCN.method):
//
//	net.minecraft.server.level.ServerPlayerGameMode.handleBlockBreakAction(BlockPos, Action, Direction, int, int):
//	    the START/STOP/ABORT dispatch. Pre-checks: isWithinBlockInteractionRange (reach), pos.getY()>getMaxY
//	    (too-high reject -> re-assert state), isUnderSpawnProtection + mayInteract + blockActionRestricted
//	    (v1 cited PASS). START: instabuild -> destroyAndAck "creative destroy"; else capture
//	    destroyProgressStart=gameTicks, compute progress=getDestroyProgress, progress>=1 -> destroyAndAck
//	    "insta mine", else begin per-tick destroying (isDestroyingBlock, destroyPos, send stage). STOP:
//	    if pos==destroyPos, elapsed=gameTicks-destroyProgressStart, progress=getDestroyProgress*(elapsed+1);
//	    >=0.7 -> destroyAndAck "destroyed"; else schedule delayed-destroy. ABORT: clear overlay.
//	net.minecraft.world.level.block.state.BlockBehaviour.getDestroyProgress(BlockState, Player, BlockGetter, BlockPos):
//	    hardness=getDestroySpeed; ==-1 -> 0; divisor = hasCorrectToolForDrops ? 30 : 100;
//	    return getDestroySpeed(player)/hardness/(float)divisor.
//	net.minecraft.server.level.ServerPlayerGameMode.incrementDestroyProgress(BlockState, BlockPos, int):
//	    elapsed=gameTicks-start; progress=getDestroyProgress*(elapsed+1); stage=(int)(progress*10);
//	    if stage!=lastSentState -> destroyBlockProgress(stage); lastSentState=stage; return progress.
//	net.minecraft.server.level.ServerPlayerGameMode.tick(): gameTicks++; delayed-destroy / isDestroyingBlock branches.
//	net.minecraft.server.level.ServerPlayerGameMode.destroyAndAck(BlockPos, int, String): destroyBlock then ack.
//	net.minecraft.server.level.ServerLevel.destroyBlockProgress(int, BlockPos, int): broadcast ClientboundBlockDestruction.
//	net.minecraft.world.entity.player.Player.getDestroySpeed(BlockState) / hasCorrectToolForDrops(BlockState).

// digInstaMineThreshold is getDestroyProgress's 1-tick instant-mine gate on START: a block whose
// single-tick progress is >= 1.0 breaks immediately (handleBlockBreakAction: `fload 6; fconst_1;
// fcmpl; iflt` — the `progress >= 1.0f` branch into destroyAndAck "insta mine"). Same constant the
// delayed-destroy uses to finish in tick() (`fconst_1; fcmpl; iflt`).
const digInstaMineThreshold float32 = 1.0

// digStopThreshold is STOP_DESTROY_BLOCK's completion gate: accumulated progress >= 0.7 breaks the
// block, anything less schedules a delayed-destroy (handleBlockBreakAction: `ldc_w 0.7f; fcmpl;
// iflt`). NOT 1.0 — vanilla trusts the client's STOP a little early to keep digging responsive.
const digStopThreshold float32 = 0.7

// digStageScale is the crack-overlay quantization: stage = (int)(progress * 10.0f) — the 0-9 (and
// beyond) breaking-texture index (incrementDestroyProgress / START both `ldc 10.0f; fmul; f2i`).
const digStageScale float32 = 10.0

// digDivisorWithTool / digDivisorNoTool are getDestroyProgress's hardness divisors: 30 when the
// player has the correct tool for drops, 100 otherwise (`bipush 30` / `bipush 100`). v1 has no
// tools, so a tool-requiring block always uses 100 and a non-tool block always uses 30.
const (
	digDivisorWithTool float32 = 30.0
	digDivisorNoTool   float32 = 100.0
)

// digOverlayClear is the destroyBlockProgress "clear the overlay" sentinel (-1). The client treats
// a stage outside 0-9 as "no crack overlay"; vanilla sends a raw -1 to remove it (tick/STOP/ABORT
// all `iconst_m1; invokevirtual destroyBlockProgress`).
const digOverlayClear = -1

// maxBuildHeightY is Level.getMaxY() for the overworld — the HIGHEST VALID block Y, INCLUSIVE:
// getMinY() + getHeight() - 1 == -64 + 384 - 1 == 319 (javap LevelHeightAccessor.getMaxY:
// `getMinY; getHeight; iadd; iconst_1; isub`). NOT getMaxBuildHeight()/getHeight() (the exclusive 320).
// Both consumers want this inclusive 319: ServerGamePacketListenerImpl.handlePlayerAction passes
// `player.level().getMaxY()` to handleBlockBreakAction whose reject is `pos.getY() > maxY` (so 320 is
// correctly rejected — isOutsideBuildHeight(320) is true since `y > getMaxY()` with getMaxY==319), and
// RandomPos.moveUpOutOfSolid / GoalUtils.isOutsideLimits (stroll_snap.go) bound on the same getMaxY().
// v1 single-dimension overworld; a future multi-dimension wiring threads the real per-dimension getMaxY
// here (mirrors dimMinY's note).
const maxBuildHeightY = dimMinY + 384 - 1 // -64 + 384 - 1 = 319 == Level.getMaxY() (inclusive)

// blockHardness maps a state id to its block's break-time inputs (Part A — level/block/hardness.go,
// extracted from the jar's BlockBehaviour.getDestroySpeed / requiresCorrectToolForDrops). The
// generated table is keyed by Block.ID() ("minecraft:stone"), so a state -> StateList[state].ID() ->
// table lookup resolves it. A missing entry (an out-of-range id, or a block the extractor did not
// emit) is treated as UNBREAKABLE (speed -1.0, requiresTool false) — the safe default that makes an
// unknown block un-diggable rather than instantly shatter; this never fires in practice because the
// generated table covers every registered block. Cite BlockBehaviour.getDestroySpeed /
// requiresCorrectToolForDrops.
func blockHardness(stateID block.StateID) (speed float32, requiresTool bool) {
	if int(stateID) < 0 || int(stateID) >= len(block.StateList) {
		return -1.0, false // out-of-range: unbreakable-safe default
	}
	id := block.StateList[stateID].ID()
	h, ok := block.Hardness[id]
	if !ok {
		return -1.0, false // unknown block: unbreakable-safe default (should not occur — table is complete)
	}
	return h.DestroySpeed, h.RequiresCorrectTool
}

// playerDestroySpeed is net.minecraft.world.entity.player.Player.getDestroySpeed(BlockState): the
// player's tool dig-speed multiplier. Its base is inventory.getSelectedItem().getDestroySpeed(state)
// — 1.0f for an empty hand and for most non-tool items — then scaled by mining-efficiency enchant,
// dig-speed/haste/fatigue effects, in-water and not-on-ground penalties. v1 has NONE of those
// subsystems (no tools, enchants, effects), so this is a faithful CITED 1.0f. Structured as its own
// helper so a future tool/enchant/effect wiring slots in here with no getDestroyProgress change.
// Cite Player.getDestroySpeed.
func playerDestroySpeed(p *tickPlayer, stateID block.StateID) float32 {
	_ = p
	_ = stateID
	return 1.0 // bare-hand base speed; tools/enchants/effects absent in v1 (cited default)
}

// hasCorrectToolForDrops is net.minecraft.world.entity.player.Player.hasCorrectToolForDrops(BlockState):
// `!state.requiresCorrectToolForDrops() || selectedItem.isCorrectToolForDrops(state)`. v1 has no tools,
// so selectedItem (an empty hand) is NEVER a correct tool. Therefore a non-tool-requiring block is
// always "correct" (the `!requiresCorrectToolForDrops()` short-circuit) and a tool-requiring block is
// always INCORRECT (bare hand) — which selects getDestroyProgress's 100 divisor, making e.g. stone
// slow-but-mineable bare-handed exactly like vanilla (1.0/1.5/100 per tick). Cite
// Player.hasCorrectToolForDrops. Structured so a future tool wiring evaluates isCorrectToolForDrops here.
func hasCorrectToolForDrops(p *tickPlayer, requiresTool bool) bool {
	_ = p
	if !requiresTool {
		return true // block does not require a correct tool: bare hand is always "correct"
	}
	return false // requires a tool, but v1 has none -> bare hand is incorrect (divisor 100)
}

// getDestroyProgress is BlockBehaviour.getDestroyProgress (via BlockState.getDestroyProgress) — the
// fraction of the block dug in ONE tick. EXACT 1:1:
//
//	float hardness = state.getDestroySpeed(level, pos);   // Part A hardness table
//	if (hardness == -1.0f) return 0.0f;                   // unbreakable
//	int divisor = player.hasCorrectToolForDrops(state) ? 30 : 100;
//	return player.getDestroySpeed(state) / hardness / (float)divisor;
//
// Cite BlockBehaviour.getDestroyProgress.
func (t *TickLoop) getDestroyProgress(p *tickPlayer, stateID block.StateID) float32 {
	hardness, requiresTool := blockHardness(stateID)
	if hardness == -1.0 {
		return 0.0 // unbreakable (bedrock, barrier, …): no progress ever
	}
	var divisor float32
	if hasCorrectToolForDrops(p, requiresTool) {
		divisor = digDivisorWithTool // 30
	} else {
		divisor = digDivisorNoTool // 100
	}
	return playerDestroySpeed(p, stateID) / hardness / divisor
}

// digBlockState reads the world state at pos, returning air for an unloaded/unreadable column. The
// vanilla level.getBlockState always returns a state (an unloaded chunk reads air), so an unreadable
// column collapsing to air is the faithful behavior: the dig stops and the overlay clears, exactly
// as if the player dug into air. Tick-owned (called on the tick goroutine).
func (t *TickLoop) digBlockState(p *tickPlayer, pos pk.Position) block.StateID {
	air := block.ToStateID[block.Air{}]
	mgr := t.dimWorld(p) // the player's-dimension world (nether for a nether player)
	if mgr == nil {
		return air
	}
	// A nil player (an AI caller, e.g. the fox) is the overworld (dimOverworld minY); a real player
	// uses its dimension's minY. playerDimOr guards the nil-p deref.
	if s, ok := mgr.GetBlock(pos, dimMinYFor(playerDimOr(p))); ok {
		return s
	}
	return air
}

// handleBlockBreakAction is the 1:1 port of ServerPlayerGameMode.handleBlockBreakAction — the
// START/STOP/ABORT dispatch behind a ServerboundPlayerAction. action is the packet's destroy-stage
// ordinal (0=START, 1=ABORT, 2=STOP); sequence is the predictive-edit ack id. Runs on the tick
// goroutine; all sends go through the bounded outbound queue.
func (t *TickLoop) handleBlockBreakAction(p *tickPlayer, pos pk.Position, action int, sequence int32) {
	// ACK THE SEQUENCE FIRST (vanilla acks the block-change sequence per action so the client's
	// predicted dig reconciles even on a REJECTED action — out of reach, above build height, an abort).
	// Without acking on those fail paths the client's predicted break lingers as a ghost. The success
	// arms (destroyBlock/destroyAndAck) historically re-acked via reconcileEdit; that ack moved to the
	// packet entry (handleUseItemOn / here), so it fires exactly once per packet regardless of outcome.
	if p.client != nil {
		p.client.Send(blockChangedAck(sequence))
	}

	// Pre-check 1 — reach: ServerPlayer.isWithinBlockInteractionRange(pos, 1.0). Reuse Sulfur's
	// existing server-authoritative reach gate (withinReach). Out of range -> "too far" no-op return.
	if !t.withinReach(p, pos) {
		return
	}

	// Pre-check 2 — too high: `pos.getY() > maxY` where the caller (handlePlayerAction) passes
	// `player.level().getMaxY()` == 319 (inclusive top valid Y). Reject by
	// re-asserting the current authoritative state to the breaker (a BlockUpdate, so the client's
	// predicted edit snaps back) and returning. We broadcast the current state to the tracking column,
	// which includes the breaker — the same snap-back reconciliation broadcastBlockUpdate provides.
	if pos.Y > maxBuildHeightY {
		t.broadcastBlockUpdate(pos, t.digBlockState(p, pos))
		return
	}

	// Pre-check 3 — spawn-protection / mayInteract / blockActionRestricted: v1 has no spawn protection,
	// no per-region interaction rules, and no game-mode block restriction beyond creative (handled
	// below), so these are a faithful CITED PASS (always allowed). Structured to become real region/
	// permission checks later; cite ServerPlayerGameMode.handleBlockBreakAction (isUnderSpawnProtection
	// / ServerLevel.mayInteract / ServerPlayer.blockActionRestricted).

	switch action {
	case actionStartDestroyBlock:
		t.startDestroyBlock(p, pos, sequence)
	case actionStopDestroyBlock:
		t.stopDestroyBlock(p, pos, sequence)
	case actionAbortDestroyBlock:
		t.abortDestroyBlock(p, pos)
	}
}

// startDestroyBlock is handleBlockBreakAction's START_DESTROY_BLOCK arm. Creative breaks instantly;
// otherwise it captures the dig start, computes the 1-tick progress, instant-mines a >=1.0 block, and
// begins a per-tick dig (sending the first crack-overlay stage) for everything else.
func (t *TickLoop) startDestroyBlock(p *tickPlayer, pos pk.Position, sequence int32) {
	// CREATIVE (abilities.instabuild): break on START immediately. hasInfiniteMaterials()/instabuild
	// is gameMode==creative in v1. destroyAndAck "creative destroy".
	if p.gameMode == gameModeCreative {
		t.destroyAndAck(p, pos, sequence)
		return
	}

	// destroyProgressStart = gameTicks. The loop's gametime is the per-tick counter (vanilla's
	// per-instance gameTicks); cast to int32 to match the field width and the elapsed subtraction.
	p.destroyProgressStart = int32(t.gametime)

	// progress = 1.0f by default; if the target is not air, progress = getDestroyProgress(state).
	// (EnchantmentHelper.onHitBlock + BlockState.attack between the read and getDestroyProgress are v1
	// no-ops — no enchants, no per-block attack behavior — so they are omitted as a CITED no-op; cite
	// handleBlockBreakAction's EnchantmentHelper.onHitBlock / BlockState.attack.)
	progress := digInstaMineThreshold // 1.0f
	state := t.digBlockState(p, pos)
	air := block.ToStateID[block.Air{}]
	if state != air && !block.IsAir(state) {
		progress = t.getDestroyProgress(p, state)
	}

	// INSTANT-MINE on START: a non-air block whose 1-tick progress is already >= 1.0 (e.g. a
	// zero/near-zero-hardness block like tall_grass) breaks immediately. destroyAndAck "insta mine".
	if !block.IsAir(state) && state != air && progress >= digInstaMineThreshold {
		t.destroyAndAck(p, pos, sequence)
		return
	}

	// Begin a per-tick dig: vanilla would first warn-clear a stale in-progress dig at a DIFFERENT pos
	// (the "abort destroying since another started" debug branch only re-asserts a block state for
	// debug; it does not clear the overlay), then sets isDestroyingBlock, records destroyPos, and sends
	// the first crack-overlay stage.
	p.isDestroyingBlock = true
	p.destroyPos = pos // pos.immutable() — pk.Position is a value type, already immutable
	stage := int(progress * digStageScale)
	t.destroyBlockProgress(p.entityID, pos, stage)
	p.lastSentDestroyStage = int32(stage)
}

// stopDestroyBlock is handleBlockBreakAction's STOP_DESTROY_BLOCK arm. It completes the dig when the
// accumulated progress (per-tick progress scaled by elapsed+1 ticks) is >= 0.7, otherwise it schedules
// a delayed-destroy that tick() finishes once progress reaches 1.0.
func (t *TickLoop) stopDestroyBlock(p *tickPlayer, pos pk.Position, sequence int32) {
	if pos != p.destroyPos {
		return // STOP for a block we are not digging: no-op (vanilla only acts when pos.equals(destroyPos))
	}
	elapsed := int32(t.gametime) - p.destroyProgressStart
	state := t.digBlockState(p, pos)
	if block.IsAir(state) {
		return // already air: nothing to finish
	}
	progress := t.getDestroyProgress(p, state) * float32(elapsed+1)
	if progress >= digStopThreshold { // 0.7f
		p.isDestroyingBlock = false
		t.destroyBlockProgress(p.entityID, pos, digOverlayClear) // clear overlay
		t.destroyAndAck(p, pos, sequence)
		return
	}
	// Not done yet -> schedule a delayed-destroy (only if one is not already pending). tick() finishes
	// it once incrementDestroyProgress reaches 1.0.
	if !p.hasDelayedDestroy {
		p.isDestroyingBlock = false
		p.hasDelayedDestroy = true
		p.delayedDestroyPos = pos
		p.delayedTickStart = p.destroyProgressStart
	}
}

// abortDestroyBlock is handleBlockBreakAction's ABORT_DESTROY_BLOCK arm: cancel the dig and clear the
// crack overlay. If the abort pos differs from the tracked destroyPos, vanilla also clears the overlay
// at the stale destroyPos (the "Mismatch in destroy block pos" warn path) before clearing it at pos.
func (t *TickLoop) abortDestroyBlock(p *tickPlayer, pos pk.Position) {
	p.isDestroyingBlock = false
	if p.destroyPos != pos {
		// Mismatch: clear the overlay at the previously-tracked block too (vanilla warns + clears it).
		t.destroyBlockProgress(p.entityID, p.destroyPos, digOverlayClear)
	}
	t.destroyBlockProgress(p.entityID, pos, digOverlayClear) // clear overlay at the aborted block
}

// tickBlockBreak is the 1:1 port of ServerPlayerGameMode.tick() — runs every server tick for every
// player. It advances the delayed-destroy (finishing the break at progress>=1.0) or refreshes the
// in-progress crack overlay. gameTicks++ is NOT done here: the loop's gametime is the shared per-tick
// counter (incremented once per tick in tickOnce), so reading t.gametime in startDestroy/stopDestroy/
// incrementDestroyProgress already reflects the same monotonic tick advance the vanilla gameTicks++
// provides. Slotted ADDITIVELY into tickEntities (no phase reorder). Tick-owned.
func (t *TickLoop) tickBlockBreak() {
	air := block.ToStateID[block.Air{}]
	for _, p := range t.players {
		if p == nil {
			continue
		}
		if p.hasDelayedDestroy {
			s := t.digBlockState(p, p.delayedDestroyPos)
			if block.IsAir(s) {
				p.hasDelayedDestroy = false
				continue
			}
			progress := t.incrementDestroyProgress(p, s, p.delayedDestroyPos, p.delayedTickStart)
			if progress >= digInstaMineThreshold { // 1.0f
				p.hasDelayedDestroy = false
				t.destroyBlock(p, p.delayedDestroyPos, air, 0, false)
			}
		} else if p.isDestroyingBlock {
			s := t.digBlockState(p, p.destroyPos)
			if block.IsAir(s) {
				t.destroyBlockProgress(p.entityID, p.destroyPos, digOverlayClear)
				p.lastSentDestroyStage = -1
				p.isDestroyingBlock = false
			} else {
				t.incrementDestroyProgress(p, s, p.destroyPos, p.destroyProgressStart) // refresh overlay
			}
		}
	}
}

// incrementDestroyProgress is ServerPlayerGameMode.incrementDestroyProgress: accumulate progress over
// (gameTicks - startTick + 1) ticks, send the crack-overlay stage only when it CHANGES, and return the
// accumulated progress. EXACT 1:1.
func (t *TickLoop) incrementDestroyProgress(p *tickPlayer, state block.StateID, pos pk.Position, startTick int32) float32 {
	elapsed := int32(t.gametime) - startTick
	progress := t.getDestroyProgress(p, state) * float32(elapsed+1)
	stage := int(progress * digStageScale)
	if int32(stage) != p.lastSentDestroyStage {
		t.destroyBlockProgress(p.entityID, pos, stage)
		p.lastSentDestroyStage = int32(stage)
	}
	return progress
}

// destroyAndAck is ServerPlayerGameMode.destroyAndAck: break the block, and (in vanilla) on a failed
// break re-assert the state to the client. Sulfur's reconcileEdit already does the SetBlock-to-air +
// BlockChangedAck(sequence) + tracking-column broadcast that constitutes the successful-break ack, so
// destroyBlock routes through it. The ack is the sequence echo the client's predictive edit needs.
func (t *TickLoop) destroyAndAck(p *tickPlayer, pos pk.Position, sequence int32) {
	air := block.ToStateID[block.Air{}]
	t.destroyBlock(p, pos, air, sequence, true)
}

// destroyBlock is the actual break — ServerPlayerGameMode.destroyBlock (level.removeBlock to air +
// the drop spawn). It captures the BROKEN state BEFORE the SetBlock so spawnBlockDrop looks up the
// right drop, sets the target to air on the tick-owned chunk, and — when ack is true (the START/STOP
// completion path that carries a client sequence) — runs reconcileEdit (BlockChangedAck + tracking
// broadcast). The delayed-destroy tick path passes ack=false: the client already moved on (it predicted
// the break ticks ago), so there is no sequence to echo; the tracking-column BlockUpdate is still sent
// so OTHER players (and the breaker's authoritative state) reflect the air. Creative drops nothing
// (spawnBlockDrop gates on gameMode==creative). Reused by both destroyAndAck and tickBlockBreak.
func (t *TickLoop) destroyBlock(p *tickPlayer, pos pk.Position, air block.StateID, sequence int32, ack bool) {
	// Capture the broken state BEFORE SetBlock overwrites it with air (reading after would see air, so
	// no drop). A failed read leaves brokenState at air (no drop) — the safe default.
	// NETHER: read/write the BREAKER's-dimension world (dimWorld(p)) at that dimension's minY so a
	// nether break lands in the nether world, not the overworld. A nil breaker (a non-player break,
	// e.g. a test / a future world-driven removal) is the overworld (playerDimOr).
	mgr := t.dimWorld(p)
	minY := dimMinYFor(playerDimOr(p))
	brokenState := air
	if mgr != nil {
		if s, ok := mgr.GetBlock(pos, minY); ok {
			brokenState = s
		}
	}

	// level.removeBlock(pos, false) -> SetBlock to air. changed=false (unloaded column / already air)
	// means nothing broke: no ack, no broadcast, no drop (matches destroyBlock returning false).
	if mgr == nil || !mgr.SetBlock(pos, air, minY) {
		return
	}

	// PLUGIN-02 (Plan 22) on_block_break seam: fire ONCE here, AFTER the block is actually removed
	// (past the changed=false early-return), so a hook fires exactly once per REAL break — and
	// because destroyBlock is the single funnel BOTH destroyAndAck (insta/STOP) and the
	// tickBlockBreak delayed-destroy route through, every break path is covered exactly once.
	// destroyBlock is NOT a per-entity loop, so the count is independent of entity count (THE GATE).
	// Nil-guarded; the payload carries the PRE-air brokenState + the breaker's entity id as plain
	// frozen scalars (no live handles — Phase 23).
	if t.plugins != nil {
		t.plugins.Emit(host.EventBlockBreak, host.BlockBreakEvent{
			X: pos.X, Y: pos.Y, Z: pos.Z,
			State:    int(brokenState),
			PlayerID: int(p.entityID),
		})
	}

	// Phase-27 N=2: the break's per-region effects must land in the region that OWNS this column.
	// destroyBlock is the single funnel for EVERY break path — the instant/STOP break (destroyAndAck,
	// on the dispatch goroutine) and the delayed-destroy (tickBlockBreak, on the coordinator) — and
	// both run with NO specific region registered, so cur() would fall back to region 0 and strand a
	// region-1 break's fluid kicks (reconcileEdit → scheduleFluidNeighborsOnEdit → cur().fluidSchedule),
	// support-cascade block ticks (cur().blockTicks), and dropped item (spawnBlockDrop → cur().entities)
	// all in region 0. Wrap the per-region effect tail in the owning region. The SetBlock + the global
	// broadcastBlockUpdate above are on the SHARED world / global player list (correct on any goroutine).
	t.withRegion(t.regionForColumn(columnOf(float64(pos.X)+0.5, float64(pos.Z)+0.5)), func() {
		if ack {
			t.reconcileEdit(p, pos, air, sequence) // BlockChangedAck(sequence) + tracking-column BlockUpdate
		} else {
			t.broadcastBlockUpdate(pos, air) // delayed-destroy: no sequence to ack, still broadcast the air
			udebugPlayer(p, "edit", "break(delayed) pos=(%d,%d,%d) -> air (was state=%d)", pos.X, pos.Y, pos.Z, brokenState)
			// CORE REDSTONE: the ack path funnels this through reconcileEdit -> onRedstoneEdit; the
			// delayed-destroy path skips reconcileEdit, so wake the redstone graph explicitly here so a
			// slow-broken source/wire still recomputes its neighbours (Level.updateNeighborsAt on removal).
			t.onRedstoneEdit(pos)
			// REDSTONE TIER-3 (OBSERVER): the delayed-destroy path also skips reconcileEdit's onObserverEdit,
			// so wake any observer WATCHING this cell explicitly (its FACING neighbor was removed). CITE:
			// ObserverBlock.updateShape.
			t.onObserverEdit(pos)
		}

		// LevelChunk.setBlockState light hook: a break to air raises the cell's light (removes a shadow-
		// caster) or removes an emitter (a broken torch darkens the room). Recompute the affected columns'
		// light and push a ClientboundLightUpdate to their trackers. Covers BOTH the ack (insta/STOP) and
		// the delayed-destroy paths — destroyBlock is the single break funnel. Gated inside relightOnEdit.
		t.relightOnEdit(p, pos, brokenState, air)

		// Spawn the dropped Item entity (ServerPlayerGameMode.destroyBlock's loot path). Creative drops
		// nothing (gated inside spawnBlockDrop). Lands in the OWNING region's store (cur().entities.add).
		t.spawnBlockDrop(p, pos, brokenState)

		// TOOL DURABILITY (ItemStack.mineBlock -> Item.mineBlock): a survival break with a tool whose TOOL
		// component has damage_per_block>0 on a non-zero-hardness block wears the tool by that much (and
		// breaks it at max). Creative / non-tool / zero-hardness wears nothing (gated inside). Covers BOTH
		// the ack and delayed-destroy paths — destroyBlock is the single break funnel. Cite Item.mineBlock.
		t.mineBlockDurability(p, brokenState)

		// POI-01: deregister the broken block's Point of Interest (a bed HOME / bell MEETING) — the
		// LevelChunk.setBlockState -> ServerLevel.updatePOIOnBlockStateChange hook for the break-to-air
		// transition. A no-op for a non-POI block. Runs in the owning region's context (t.cur()).
		t.updatePoiOnBlockStateChange(pos, brokenState, air)
	})
}

// destroyBlockProgress is ServerLevel.destroyBlockProgress(entityId, pos, stage): broadcast a
// ClientboundBlockDestruction (the 0-9 crack overlay; stage outside that range — e.g. -1 — clears it).
// The overlay is visible to OTHERS too, so it is sent to every player tracking the block's column
// (the breaker included), mirroring vanilla broadcasting to the chunk's tracking player set. Wire
// layout JAR-VERIFIED (ClientboundBlockDestructionPacket: writeVarInt(id), writeBlockPos(pos),
// writeByte(progress)): VarInt entityId, Position pos, Byte stage. Cite ServerLevel.destroyBlockProgress.
func (t *TickLoop) destroyBlockProgress(entityID int32, pos pk.Position, stage int) {
	packet := pk.Marshal(int32(packetid.ClientboundBlockDestruction),
		pk.VarInt(entityID), pos, pk.Byte(stage))
	col := chunkCenterOf(int32(pos.X), int32(pos.Z))
	for _, pl := range t.players {
		if pl.client == nil {
			continue
		}
		if pl.center == col || (pl.sentChunks != nil && pl.sentChunks[col]) {
			pl.client.Send(packet)
		}
	}
}
