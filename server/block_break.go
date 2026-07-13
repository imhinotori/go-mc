package server

import (
	"strings"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/data/tag"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
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

// playerDestroySpeed is net.minecraft.world.entity.player.Player.getDestroySpeed(BlockState), ported
// 1:1 from the jar bytecode (javap this session):
//
//	float f = inventory.getSelectedItem().getDestroySpeed(state);          // 1.0f for an empty hand / non-tool
//	if (f > 1.0f) f += (float) getAttributeValue(MINING_EFFICIENCY);       // Efficiency enchant (level^2+1)
//	if (MobEffectUtil.hasDigSpeed(this)) f *= 1 + (getDigSpeedAmplification+1)*0.2f;   // Haste  [effect seam]
//	if (hasEffect(MINING_FATIGUE)) f *= {0.3, 0.09, 0.0027, 8.1E-4}[amp];  // Mining Fatigue [effect seam]
//	f *= (float) getAttributeValue(BLOCK_BREAK_SPEED);                     // unconditional multiplier (default 1.0)
//	if (isEyeInFluid(WATER)) f *= (float) getAttribute(SUBMERGED_MINING_SPEED).getValue();  // 0.2 default = 5x penalty
//	if (!onGround()) f /= 5.0f;                                            // not-on-ground penalty [movement seam]
//	return f;
//
// The base f is now the REAL ItemStack.getDestroySpeed(state): the held item's minecraft:tool
// component's getMiningSpeed(state) (heldToolMiningSpeed) — 8.0 for a diamond_shovel on dirt, 1.0
// for an empty hand or a non-matching block. The MINING_EFFICIENCY branch is gated on f > 1.0f
// (only a real tool exceeds 1.0), wired at the exact bytecode point so an Efficiency-enchanted tool
// composes once the enchant-effect layer lands. BLOCK_BREAK_SPEED (default 1.0) and
// SUBMERGED_MINING_SPEED (default 0.2, the vanilla 5x underwater dig penalty; Aqua Affinity raises it
// to 1.0) read through the holder so their enchant modifiers compose. The Haste/Mining-Fatigue effect
// scales and the not-on-ground /5.0 penalty are CITED SEAMS (no effect source / no destroy-progress
// ground read wired here yet) — they slot in at the marked points with no caller change. Cite
// Player.getDestroySpeed / ItemStack.getDestroySpeed / Tool.getMiningSpeed.
func (t *TickLoop) playerDestroySpeed(p *tickPlayer, stateID block.StateID) float32 {
	f := t.heldToolMiningSpeed(p, stateID) // ItemStack.getDestroySpeed(state): held tool's Tool.getMiningSpeed
	if f > 1.0 {
		// `f += (float) getAttributeValue(MINING_EFFICIENCY)` — Efficiency enchant grants level^2+1.
		f += float32(p.getAttributeValue(attrMiningEfficiency))
	}
	// Haste / Mining Fatigue effect scales (MobEffectUtil.hasDigSpeed / MINING_FATIGUE) are the effect
	// SEAM — no dig-speed/fatigue effect source is wired to the player holder yet, so f is unscaled here.
	// `f *= (float) getAttributeValue(BLOCK_BREAK_SPEED)` — unconditional multiplier (base 1.0 → no change).
	f *= float32(p.getAttributeValue(attrBlockBreakSpeed))
	// `if (isEyeInFluid(WATER)) f *= (float) getAttribute(SUBMERGED_MINING_SPEED).getValue()` — the
	// 0.2 base is the vanilla 5x underwater dig penalty; the Aqua Affinity enchant raises it toward 1.0.
	if t.eyeInWater(p) {
		f *= float32(p.getAttributeValue(attrSubmergedMiningSpeed))
	}
	// `if (!onGround()) f /= 5.0f` is the not-on-ground penalty SEAM (no ground read wired into the
	// destroy-progress path yet) — slots in here with no caller change.
	return f
}

// hasCorrectToolForDrops is net.minecraft.world.entity.player.Player.hasCorrectToolForDrops(BlockState):
// `!state.requiresCorrectToolForDrops() || selectedItem.isCorrectToolForDrops(state)`. A block that does
// not require a correct tool is always "correct" (the `!requiresCorrectToolForDrops()` short-circuit).
// Otherwise the held stack's ItemStack.isCorrectToolForDrops(state) — the minecraft:tool component's
// Tool.isCorrectForDrops(state) — decides: a diamond_pickaxe is correct for iron_ore (mineable/pickaxe
// rule, correct_for_drops true, and NOT in incorrect_for_diamond_tool) so drops flow and the 30 divisor
// applies; a wooden_pickaxe on iron_ore matches incorrect_for_wooden_tool FIRST (correct_for_drops false)
// so it is INCORRECT (100 divisor, no drop). A bare hand has no tool component -> isCorrectForDrops false.
// Cite Player.hasCorrectToolForDrops / ItemStack.isCorrectToolForDrops / Tool.isCorrectForDrops.
func (t *TickLoop) hasCorrectToolForDrops(p *tickPlayer, stateID block.StateID, requiresTool bool) bool {
	if !requiresTool {
		return true // !state.requiresCorrectToolForDrops(): bare hand is always "correct"
	}
	td, ok := t.heldEffectiveTool(p)
	if !ok {
		return false // empty hand / non-tool item: never a correct tool
	}
	return toolIsCorrectForDrops(td, toolBlockResourceID(stateID))
}

// toolBlockResourceID resolves a block state to its block's resource id ("minecraft:stone") — the
// key form Tool rules match against (BlockState.is(HolderSet)). An out-of-range state resolves to ""
// (matches no rule), the same unbreakable-safe fallback blockHardness uses. Cite BlockState.is.
func toolBlockResourceID(stateID block.StateID) string {
	if int(stateID) < 0 || int(stateID) >= len(block.StateList) {
		return ""
	}
	return block.StateList[stateID].ID()
}

// heldEffectiveTool returns the player's held-item EFFECTIVE minecraft:tool component and whether one
// is present — the ItemStack.get(DataComponents.TOOL) resolution: the item's DEFAULT Tool component
// (component.DefaultTool[itemName], vanilla Item.components()) OVERLAID by a client-sent Tool component
// PATCH (which replaces the default at component granularity, exactly like PatchedDataComponentMap.get
// returns the patch value over the prototype). Returns (_, false) for an empty hand or a non-tool item
// (no default and no patch). Tick-owned (reads the tick-owned inventory). Cite
// ItemStack.get(DataComponents.TOOL) / PatchedDataComponentMap.get.
func (t *TickLoop) heldEffectiveTool(p *tickPlayer) (component.ToolData, bool) {
	if p == nil {
		return component.ToolData{}, false
	}
	inv := ensureInventory(p)
	s := inv.get(heldWindowSlot(inv.heldSlot))
	if stackEmpty(s) {
		return component.ToolData{}, false
	}
	// A client-sent Tool component PATCH replaces the default entirely (component-granular override).
	if wt, ok := component.DecodePatch(s).Get(compTool).(*component.Tool); ok {
		return wireToolToData(wt), true
	}
	// Otherwise the item's registration default (the common case: a plain, unedited tool).
	if int(s.ItemID) >= 0 && int(s.ItemID) < len(registryid.Item) {
		if td, ok := component.DefaultTool[registryid.Item[s.ItemID]]; ok {
			return td, true
		}
	}
	return component.ToolData{}, false
}

// heldToolMiningSpeed is ItemStack.getDestroySpeed(state): the held item's Tool.getMiningSpeed(state),
// or 1.0f for an empty hand / non-tool item (the `tool != null ? tool.getMiningSpeed(state) : 1.0f`
// branch). Cite ItemStack.getDestroySpeed.
func (t *TickLoop) heldToolMiningSpeed(p *tickPlayer, stateID block.StateID) float32 {
	td, ok := t.heldEffectiveTool(p)
	if !ok {
		return 1.0 // empty hand / non-tool: the fconst_1 default
	}
	return toolGetMiningSpeed(td, toolBlockResourceID(stateID))
}

// wireToolToData converts a client-sent wire component.Tool (HolderSet-as-IDSet blocks + pk.Option
// speed/correctForDrops) into the resolved component.ToolData form. An IDSet tag (Type==0) becomes a
// "#tag" ref; inline block ids (Type>0) resolve through registryid.Block to "minecraft:x" refs. This is
// only hit for an NBT-edited stack that carries an explicit Tool patch; a plain tool uses DefaultTool.
// Cite Tool.STREAM_CODEC (rules/defaultMiningSpeed/damagePerBlock/canDestroyBlocksInCreative).
func wireToolToData(wt *component.Tool) component.ToolData {
	td := component.ToolData{
		DefaultMiningSpeed:         float32(wt.DefaultMiningSpeed),
		DamagePerBlock:             int(wt.DamagePerBlock),
		CanDestroyBlocksInCreative: bool(wt.CanDestroyBlocksInCreative),
	}
	for i := range wt.Rules {
		r := &wt.Rules[i]
		var blocks []string
		if r.Blocks.Type == 0 {
			blocks = []string{"#" + string(r.Blocks.Tag)}
		} else {
			for _, id := range r.Blocks.IDs {
				if int(id) >= 0 && int(id) < len(registryid.Block) {
					blocks = append(blocks, registryid.Block[id])
				}
			}
		}
		rd := component.ToolRuleData{Blocks: blocks}
		if r.Speed.Has {
			rd.HasSpeed = true
			rd.Speed = float32(r.Speed.Val)
		}
		if r.CorrectDropForBlocks.Has {
			rd.HasCorrectForDrops = true
			rd.CorrectForDrops = bool(r.CorrectDropForBlocks.Val)
		}
		td.Rules = append(td.Rules, rd)
	}
	return td
}

// toolRuleMatches is BlockState.is(HolderSet) for a Tool rule's block references: true when the block
// resource id is a member of ANY of the rule's refs — a "#tag" ref (membership via data/tag.BlockTags,
// stripping the "#minecraft:" prefix to the short tag key) or a bare "minecraft:x" concrete-block ref.
// Cite BlockState.is / Tool.Rule.blocks (HolderSet).
func toolRuleMatches(refs []string, resID string) bool {
	if resID == "" {
		return false
	}
	for _, ref := range refs {
		if strings.HasPrefix(ref, "#") {
			key := strings.TrimPrefix(strings.TrimPrefix(ref, "#"), "minecraft:")
			if tag.BlockTags[key][resID] {
				return true
			}
		} else if ref == resID {
			return true
		}
	}
	return false
}

// toolGetMiningSpeed is net.minecraft.world.item.component.Tool.getMiningSpeed(BlockState): the FIRST
// rule that both specifies a speed (Optional present) AND whose block set contains the state returns
// that speed; otherwise defaultMiningSpeed (1.0 for every vanilla tool). Cite Tool.getMiningSpeed.
func toolGetMiningSpeed(td component.ToolData, resID string) float32 {
	for i := range td.Rules {
		r := &td.Rules[i]
		if r.HasSpeed && toolRuleMatches(r.Blocks, resID) {
			return r.Speed
		}
	}
	return td.DefaultMiningSpeed
}

// toolIsCorrectForDrops is net.minecraft.world.item.component.Tool.isCorrectForDrops(BlockState): the
// FIRST rule that both specifies correctForDrops (Optional present) AND whose block set contains the
// state returns that flag; otherwise false. The incorrect_for_X_tool rule is ordered BEFORE the
// mineable/* rule, so a too-low-tier tool matches it first and returns false. Cite Tool.isCorrectForDrops.
func toolIsCorrectForDrops(td component.ToolData, resID string) bool {
	for i := range td.Rules {
		r := &td.Rules[i]
		if r.HasCorrectForDrops && toolRuleMatches(r.Blocks, resID) {
			return r.CorrectForDrops
		}
	}
	return false
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
	if t.hasCorrectToolForDrops(p, stateID, requiresTool) {
		divisor = digDivisorWithTool // 30
	} else {
		divisor = digDivisorNoTool // 100
	}
	return t.playerDestroySpeed(p, stateID) / hardness / divisor
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

	// Pre-check 3 — spawn-protection / mayInteract: v1 has no spawn protection and no per-region
	// interaction rules, so those are a faithful CITED PASS (always allowed). Structured to become real
	// region checks later; cite ServerPlayerGameMode.handleBlockBreakAction (isUnderSpawnProtection /
	// ServerLevel.mayInteract).
	//
	// blockActionRestricted (F-G2): a spectator (or an adventure player without item break permissions —
	// unported in v1, so all adventure) cannot break blocks. Vanilla's ServerPlayerGameMode
	// .handleBlockBreakAction returns early (and re-asserts the block to the client) when
	// blockActionRestricted is true. Re-send the true block state so the client's predicted break rolls
	// back, then return.
	if blockActionRestricted(p) {
		t.broadcastBlockUpdate(pos, t.digBlockState(p, pos))
		return
	}

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
		// BlockState.attack(level, pos, player): the per-block left-click hook vanilla fires here (between
		// EnchantmentHelper.onHitBlock and getDestroyProgress, guarded by !state.isAir()). For a note block
		// this PLAYS the note (NoteBlock.attack -> playNote) — punching a note block plays its current note
		// without tuning it. A no-op for every other block (the base BlockState.attack is empty), so this
		// stays a faithful hook. Reached only in SURVIVAL (creative already returned via destroyAndAck).
		// CITE ServerPlayerGameMode.handleBlockBreakAction (blockState.attack) + NoteBlock.attack.
		t.noteBlockAttack(p, pos, state)
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

	// GAME-EVENT: Level.destroyBlock -> gameEvent(GameEvent.Entity(player), pos, BLOCK_DESTROY).
	// Emitted at the CENTER of the broken cell with the breaker as source so a nearby Warden
	// vibration listener gains anger. Draws no RNG. p may be nil (world-driven removal): source id 0.
	// Cite Level.destroyBlock (gameEvent GameEvent.BLOCK_DESTROY).
	{
		var src int32
		if p != nil {
			src = p.entityID
		}
		t.gameEventAt(geBlockDestroy, pos, gameEventContext{sourceEntityID: src, affectedState: int(brokenState)})
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

		// LevelChunk.setBlockState light hook now fires CENTRALLY from ChunkManager.SetBlock (see
		// SetBlockChangeHook / relightChanged): destroyBlock's SetBlock-to-air already re-propagated light
		// and broadcast the ClientboundLightUpdate. No per-site relight call here.

		// D-B1: the general Level.setBlock flag-1+2 neighbour-update dispatch for the CrossCollisionBlock
		// + attachment slices - a break to air makes an adjacent fence/pane/bar drop its connection toward
		// the now-empty cell (updateNeighbourShapes), and an attachment (torch) whose support was just
		// removed pops off + drops (neighborChanged canSurvive -> false). Single MINIMAL call so the
		// break-path integration stays trivial. Cite Level.setBlock -> updateNeighbourShapes / updateNeighborsAt.
		t.updateShapeOnEdit(pos, air)

		// Spawn the dropped Item entity (ServerPlayerGameMode.destroyBlock's loot path). Creative drops
		// nothing (gated inside spawnBlockDrop). Lands in the OWNING region's store (cur().entities.add).
		//
		// CORRECT-TOOL-FOR-DROPS GATE (ServerPlayerGameMode.destroyBlock offsets 217-269): the flag
		// `boolean flag = player.hasCorrectToolForDrops(state)` is computed up front, and drops are only
		// rolled when `removed && flag` (`iload removed; ifeq skip; iload flag; ifeq skip;
		// Block.playerDestroy -> dropResources`). A block tagged requiresCorrectToolForDrops (stone,
		// every ore, deepslate, metal blocks, ...) broken with the WRONG tool (a bare hand in v1) drops
		// NOTHING — the loot TABLE does not re-check the tool (it only branches on silk_touch), so this
		// gate is the sole place tool-correctness suppresses the drop. Without it, punching stone/ore
		// bare-handed wrongly yielded cobblestone/raw ore. Applied only for a player break (real p);
		// a nil-breaker (a non-player Block.destroyBlock with entity=null: support cascade, fluid, piston)
		// drops regardless of tool, so it is unaffected. CITE: ServerPlayerGameMode.destroyBlock;
		// Player.hasCorrectToolForDrops.
		dropAllowed := true
		if p != nil {
			_, requiresTool := blockHardness(brokenState)
			dropAllowed = t.hasCorrectToolForDrops(p, brokenState, requiresTool)
		}
		if dropAllowed {
			t.spawnBlockDrop(p, pos, brokenState)
		}

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
