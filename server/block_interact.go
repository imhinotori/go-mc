package server

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
)

// block_interact.go — ENT-03: the on-tick place/break handlers behind the subtick route.
// dispatch (tick.go) appends ServerboundPlayerAction (NEW route) and ServerboundUseItemOn
// (already routed) into the bounded per-player subtick buffer; applyInput (subtick.go)
// drains them in sequence order and calls these handlers on the TICK goroutine. All world
// mutation goes through world.ChunkManager.SetBlock over the tick-owned manager (TICK-05 /
// T-6-08) and all client sends go through the bounded outbound queue (writeLoop stays the
// sole socket writer).
//
// THE RECONCILIATION CONTRACT (load-bearing, anti-ghost-block — 06-RESEARCH Pitfall 5):
// a VALID edit (1) mutates the tick-owned chunk via SetBlock, (2) sends
// ClientboundBlockChangedAck(sequence) to the EDITOR so its prediction reconciles, and
// (3) broadcasts ClientboundBlockUpdate(pos, newState) to EVERY player tracking that column
// (editor included, so a rejected/adjusted prediction snaps back). An INVALID edit
// (out of reach, unloaded column, or a defensive Scan failure) is a SILENT no-op: no
// mutation, no ack, no broadcast.

// PlayerAction.Action enum ordinals (jar: ServerboundPlayerActionPacket$Action, read as a
// VarInt enum index). Only the destroy stages matter for v1.
const (
	actionStartDestroyBlock = 0 // survival dig BEGIN (button pressed) — NOT a break; no-op for v1
	actionAbortDestroyBlock = 1 // dig cancelled — no-op for v1
	actionStopDestroyBlock  = 2 // survival dig FINISH — the break trigger
)

// blockReach is the v1 max edit distance (blocks) from the player's position to the target
// block CENTER. Vanilla's survival reach is ~4.5 blocks (creative ~6); v1 uses a single
// generous bound as the server-authoritative reach gate (T-6-01). A target beyond this is
// rejected as a silent no-op. Measured from the player feet position to the block center —
// good enough for the v1 "can't edit blocks across the map" guarantee.
const blockReach = 6.0

// directionNormal maps a jar Direction 3D-data value (from3DDataValue: 0=DOWN, 1=UP,
// 2=NORTH, 3=SOUTH, 4=WEST, 5=EAST) to its unit normal (dx,dy,dz). Placement offsets the
// hit block by this normal to land on the ADJACENT face. An unknown index yields the zero
// vector, which collapses placement onto the hit block — harmless and still validated.
func directionNormal(dir int) (dx, dy, dz int) {
	switch dir {
	case 0: // DOWN -Y
		return 0, -1, 0
	case 1: // UP +Y
		return 0, 1, 0
	case 2: // NORTH -Z
		return 0, 0, -1
	case 3: // SOUTH +Z
		return 0, 0, 1
	case 4: // WEST -X
		return -1, 0, 0
	case 5: // EAST +X
		return 1, 0, 0
	default:
		return 0, 0, 0
	}
}

// withinReach reports whether the block at pos is close enough to the player to edit. The
// distance is player-feet to block-center (pos + 0.5 on each axis). The server-authoritative
// reach gate (T-6-01): a far/cross-chunk target is rejected before any mutation.
func (t *TickLoop) withinReach(p *tickPlayer, pos pk.Position) bool {
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y) + 0.5
	cz := float64(pos.Z) + 0.5
	dx := cx - p.x
	dy := cy - p.y
	dz := cz - p.z
	return dx*dx+dy*dy+dz*dz <= blockReach*blockReach
}

// handlePlayerAction resolves a ServerboundPlayerAction (the BREAK path) on-tick. Wire
// layout (jar-derived): VarInt action + Position pos + UnsignedByte direction + VarInt
// sequence. A Scan error is a silent no-op (T-3-02 / T-6-04: defensive decode, never panic).
//
// Plan 17-21 REPLACED the old break-on-STOP-only stand-in with the server-authoritative dig-time
// model: this method now decodes the packet and dispatches the destroy-stage ordinal
// (START=0/ABORT=1/STOP=2) into handleBlockBreakAction (block_break.go), the 1:1 port of
// ServerPlayerGameMode.handleBlockBreakAction. START begins a per-tick dig timer and streams the
// crack overlay, instant/creative mines break on START, and STOP completes against the block's
// hardness — so survival blocks no longer shatter instantly on first click. The non-destroy
// player actions (drop item, swap hand, finish using, swing, etc.) are not dig stages, so they fall
// through as no-ops, exactly as before.
func (t *TickLoop) handlePlayerAction(p *tickPlayer, pkt pk.Packet) {
	var action pk.VarInt
	var pos pk.Position
	var direction pk.UnsignedByte
	var sequence pk.VarInt
	if err := pkt.Scan(&action, &pos, &direction, &sequence); err != nil {
		return // malformed/short payload: no-op, never panic (defensive decode)
	}

	// Only the three destroy stages drive the dig-time model; any other player-action ordinal is not a
	// break and is a no-op here (the reach/too-high pre-checks and the START/STOP/ABORT arms all live
	// in handleBlockBreakAction). direction is decoded for wire correctness but unused by the break
	// model (vanilla passes it through but the base getDestroyProgress ignores it).
	switch int(action) {
	case actionStartDestroyBlock, actionAbortDestroyBlock, actionStopDestroyBlock:
		t.handleBlockBreakAction(p, pos, int(action), int32(sequence))
	}
}

// handleUseItemOn resolves a ServerboundUseItemOn (the PLACE path) on-tick. Wire layout
// (jar-derived, javap'd this session): VarInt hand + [Position pos + VarInt direction +
// Float cursorX/Y/Z + Boolean insideBlock + Boolean worldBorderHit] + VarInt sequence.
// (FriendlyByteBuf.readBlockHitResult reads BlockPos, the Direction enum, three floats, then
// TWO booleans; ServerboundUseItemOnPacket reads hand FIRST, then the hit result, then the
// sequence.) A Scan error is a silent no-op.
//
// THE 1:1 PORT (Plan 17-17 — replaces the hardcoded-stone v1 stand-in). Decompiled this session
// from temp/cache/26.2-inner.jar:
//
//	net.minecraft.server.level.ServerPlayerGameMode.useItemOn(player, level, stack, hand, hit):
//	    // (1) BLOCK's own interaction first (chest open, lever toggle, ...). For v1 Sulfur has no
//	    //     interactive blocks, so blockState.useItemOn is a faithful PASS hook (no-op). When it
//	    //     consumesAction the method returns and NOTHING is placed.
//	    InteractionResult r = blockState.useItemOn(...); if (r.consumesAction()) return r;   // hook
//	    // (2) cooldown/empty short-circuit, then the ITEM's useOn:
//	    if (stack.isEmpty() || cooldown(stack)) return PASS;
//	    UseOnContext ctx = new UseOnContext(player, hand, hit);
//	    if (player.hasInfiniteMaterials()) {                 // CREATIVE guard (the count save/restore)
//	        int saved = stack.getCount();
//	        r = stack.useOn(ctx);                            // BlockItem.useOn -> place (consume shrinks)
//	        stack.setCount(saved);                           // restore: creative never loses the item
//	    } else { r = stack.useOn(ctx); }                     // SURVIVAL: place's consume(1) sticks
//
//	net.minecraft.world.item.BlockItem.useOn(ctx) -> place(new BlockPlaceContext(ctx)):
//	    if (!canPlace()) return FAIL;                        // target must be replaceable
//	    state = getPlacementState(ctx);                      // Block.getStateForPlacement -> default
//	    if (!placeBlock(ctx, state)) return FAIL;            // level.setBlock(getClickedPos, state)
//	    ... setPlacedBy / sound / gameEvent ...
//	    stack.consume(1, player);                            // ItemStack.consume: shrink(1) UNLESS
//	                                                         //   player.hasInfiniteMaterials()
//	    return SUCCESS;
//
//	net.minecraft.world.item.context.BlockPlaceContext:
//	    replaceClicked = level.getBlockState(hitPos).canBeReplaced(this);  // is the CLICKED block replaceable?
//	    getClickedPos() = replaceClicked ? hitPos : hitPos.relative(face); // replace-in-place vs adjacent
//	    canPlace()      = replaceClicked || getBlockState(getClickedPos()).canBeReplaced(this);
//
// So the placed block comes from the HELD ITEM (not a hardcoded stone), an EMPTY hand places
// nothing, the target must be replaceable (air/water/lava — never inside a solid), and in
// SURVIVAL the stack shrinks by one (CREATIVE keeps it).
func (t *TickLoop) handleUseItemOn(p *tickPlayer, pkt pk.Packet) {
	var hand pk.VarInt
	var pos pk.Position
	var direction pk.VarInt
	var cursorX, cursorY, cursorZ pk.Float
	var insideBlock, worldBorderHit pk.Boolean
	var sequence pk.VarInt
	if err := pkt.Scan(&hand, &pos, &direction,
		&cursorX, &cursorY, &cursorZ,
		&insideBlock, &worldBorderHit, &sequence); err != nil {
		return // malformed/short payload: no-op, never panic
	}

	// (1) ServerPlayerGameMode.useItemOn step 1 — the BLOCK's own interaction. Interactive blocks
	// (chests) consume the action via useBlockInteraction and we early-return; non-interactive
	// blocks pass through to placement.
	if t.useBlockInteraction(p, pos, int(direction)) {
		// The block consumed the interaction (e.g. a chest opened its menu) — NO block is placed.
		return
	}

	// Read the held MAIN-HAND item: the selected hotbar slot maps to window slot 36+heldSlot
	// (4 craft + 4 armor + 9..35 main + 36..44 hotbar). p.inventory is tick-owned.
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))

	// (2) ItemStack.isEmpty() short-circuit + Block.byItem resolution. An EMPTY hand (or a
	// non-block item like a tool) resolves to no block -> nothing is placed. THIS fixes the
	// empty-hand-stone bug (the old code hardcoded stone regardless of the held item).
	placeState, ok := blockStateForItem(held)
	if !ok {
		return // empty hand / non-block item: PASS, no placement
	}

	// BlockPlaceContext geometry (decompiled above). replaceClicked: is the CLICKED block itself
	// replaceable? If so, placement REPLACES it in place (getClickedPos == hitPos); otherwise it
	// lands on the ADJACENT face (hitPos + the face normal).
	var clickedState block.StateID
	clickedKnown := false
	if t.only().world != nil {
		if s, ok := t.only().world.GetBlock(pos, dimMinY); ok {
			clickedState, clickedKnown = s, true
		}
	}
	// BlockState.canBeReplaced(ctx): replaceable() && !held.is(this.asItem()). The self-replace
	// guard (can't replace a block with the SAME block) is honored: placeState == clickedState
	// means the held item IS this block, so it is not treated as replaceable.
	replaceClicked := clickedKnown && isReplaceableState(clickedState) && placeState != clickedState

	var placePos pk.Position
	if replaceClicked {
		placePos = pos // replace the clicked block in place
	} else {
		dx, dy, dz := directionNormal(int(direction))
		placePos = pk.Position{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
	}

	// Reach is validated against the resolved placement target (server-authoritative gate).
	if !t.withinReach(p, placePos) {
		return
	}

	// BlockPlaceContext.canPlace(): replaceClicked || target.canBeReplaced(ctx). When NOT
	// replacing the clicked block, the ADJACENT target must itself be replaceable (air/water/
	// lava) — placement never overwrites a solid block. An unloaded/unreadable target is treated
	// as not-replaceable (no-op), matching FAIL.
	if !replaceClicked {
		targetState, ok := t.only().world.GetBlock(placePos, dimMinY)
		if !ok || !(isReplaceableState(targetState) && placeState != targetState) {
			return // !canPlace() -> FAIL, silent no-op
		}
	}

	// BlockItem.canPlace tail: Level.isUnobstructed(state, pos, placementContext(player)). For a
	// full-cube block the collision shape is the 1×1×1 box at placePos; isUnobstructed REJECTS the
	// placement if that box overlaps any non-removed, blocksBuilding entity's AABB (the entity arg
	// is null in placementContext, so even the placer counts). Without this a player could place a
	// block inside another player/mob. v1 subset: the full-cube shape + the blocksBuilding filter
	// (players + mobs block; dropped items do NOT — ItemEntity.blocksBuilding is false). Cite:
	// net.minecraft.world.item.BlockItem.canPlace -> Level.isUnobstructed ->
	// EntityGetter.isUnobstructed(null, shape) [getEntities + !isRemoved && blocksBuilding && overlap].
	if t.placementObstructedByEntity(placePos) {
		return // obstructed by an entity -> !canPlace() -> FAIL, silent no-op
	}

	// placeBlock -> Level.setBlock(getClickedPos(), state). changed=false (unloaded / no-change)
	// -> no ack, no broadcast (matches placeBlock returning false -> FAIL).
	if t.only().world == nil || !t.only().world.SetBlock(placePos, placeState, dimMinY) {
		return
	}

	// LevelChunk.setBlockState hasBlockEntity() branch: a placed block that carries a BlockEntity
	// (chest) gets its (empty) BlockEntity created + registered synchronously on place. Sulfur's
	// SetBlock writes only the state, so we replicate the BE creation here — without it a placed
	// chest has no BE and never opens. CITE: LevelChunk.setBlockState -> EntityBlock.newBlockEntity.
	t.createBlockEntityOnPlace(placePos, placeState)

	t.reconcileEdit(p, placePos, placeState, int32(sequence))

	// PLUGIN-02 (Plan 22) on_block_place seam: fire ONCE here at the place call site, AFTER the
	// authoritative SetBlock+reconcileEdit — NOT from the shared broadcastBlockUpdate (Pitfall 2:
	// break ALSO routes block updates through that broadcaster, so emitting there would double-fire
	// place on every break). This site is reached only on a real, successful placement. Nil-guarded;
	// the payload carries the placed pos + state + placer entity id as plain frozen scalars.
	if t.plugins != nil {
		t.plugins.Emit(host.EventBlockPlace, host.BlockPlaceEvent{
			X: placePos.X, Y: placePos.Y, Z: placePos.Z,
			State:    int(placeState),
			PlayerID: int(p.entityID),
		})
	}

	// BlockItem.place tail -> stack.consume(1, player). ItemStack.consume shrinks the stack by 1
	// UNLESS player.hasInfiniteMaterials() (CREATIVE). ServerPlayerGameMode.useItemOn's creative
	// count save/restore wraps the same guard; both collapse to "shrink in survival, keep in
	// creative". v1's gameMode is the hasInfiniteMaterials() source.
	if p.gameMode != gameModeCreative {
		t.shrinkHeldItem(p, inv)
	}
}

// useBlockInteraction (the ServerPlayerGameMode.useItemOn step-1 hook — blockState.useItemOn) now
// lives in chest_open.go: it dispatches a right-click on a chest to the chest-OPEN path (returning
// true so placement is skipped) and PASSes (false) for every other block. handleUseItemOn calls it
// unchanged.

// heldWindowSlot maps a hotbar index (0..8) to its player-inventory WINDOW slot. The vanilla
// player inventory window lays out 4 craft + 4 armor + 27 main (9..35) + 9 hotbar (36..44), so the
// selected hotbar slot's window index is 36+heldSlot (verified against inventory.go's 46-slot /
// 36..44 hotbar layout). An out-of-range hotbar index falls back to slot 36 — harmless, since
// handleSetCarriedItem already bounds heldSlot to 0..8.
func heldWindowSlot(heldSlot int16) int16 {
	if heldSlot < 0 || heldSlot > 8 {
		return 36
	}
	return 36 + heldSlot
}

// shrinkHeldItem ports BlockItem.place's stack.consume(1, player) for the SURVIVAL path: it
// decrements the held stack by one and sends the authoritative ClientboundContainerSetSlot for
// the changed slot so the client reflects the new count (reusing the inventory.go diff/broadcast
// machinery). A stack that reaches 0 becomes an empty slot. Tick-owned (called on the tick
// goroutine via handleUseItemOn). The creative guard is the caller's responsibility (mirroring
// ItemStack.consume's hasInfiniteMaterials() short-circuit).
func (t *TickLoop) shrinkHeldItem(p *tickPlayer, inv *Inventory) {
	slot := heldWindowSlot(inv.heldSlot)
	before := inv.snapshot()

	cur := inv.get(slot)
	cur.Count--
	if cur.Count <= 0 {
		cur = component.SlotData{Count: 0} // ItemStack.shrink to 0 -> EMPTY
	}
	inv.set(slot, cur)

	// AbstractContainerMenu.broadcastChanges -> synchronizeSlotToRemote: send a SetSlot for the
	// changed slot only (the diff helper already does exactly this).
	t.broadcastInventoryChanges(p, inv, before)
}

// placementObstructedByEntity is the v1 port of the entity half of Level.isUnobstructed(state,
// pos, placementContext(player)) for a FULL-CUBE block: the block's collision shape is the 1×1×1
// box at pos; the placement is obstructed if that box overlaps any non-removed, blocksBuilding
// entity's AABB. Mirrors EntityGetter.isUnobstructed(null, shape): iterate the entities whose
// bounding box intersects the shape and reject (return true = obstructed) on the first one that
// blocksBuilding and actually overlaps. The entity arg is null in placementContext, so EVERY
// blocksBuilding entity counts — including the placer (a player standing in the target cell
// blocks their own placement, as in vanilla). blocksBuilding is true for players + mobs and
// FALSE for dropped items (ItemEntity), so a drop lying in the cell does NOT block the place;
// Sulfur reads that off Entity.isItem. The overlap is the half-open AABB intersection
// (Shapes.joinIsNotEmpty with AND: interiors must overlap, edge-touching does not count).
func (t *TickLoop) placementObstructedByEntity(pos pk.Position) bool {
	if t.only().entities == nil {
		return false
	}
	// The full-cube collision shape at pos: the unit box [pos, pos+1].
	bx0, by0, bz0 := float64(pos.X), float64(pos.Y), float64(pos.Z)
	bx1, by1, bz1 := bx0+1, by0+1, bz0+1
	for _, e := range t.only().entities.all() {
		if e == nil || e.isItem {
			continue // dropped items have blocksBuilding=false: they never obstruct a placement
		}
		// e's feet-anchored AABB (centered on x/z, base at y, top at y+height).
		hw := e.width / 2
		ex0, ey0, ez0 := e.x-hw, e.y, e.z-hw
		ex1, ey1, ez1 := e.x+hw, e.y+e.height, e.z+hw
		// Half-open interior overlap on all three axes (AABB.intersects: lower < other.upper &&
		// other.lower < upper). Edge-flush (a box exactly atop the cell face) does NOT overlap.
		if bx0 < ex1 && ex0 < bx1 && by0 < ey1 && ey0 < by1 && bz0 < ez1 && ez0 < bz1 {
			return true // a blocksBuilding entity occupies the cell -> obstructed
		}
	}
	return false
}

// reconcileEdit runs the post-mutation reconciliation contract for a VALID edit: ack the
// action's sequence to the editor (so its prediction commits) and broadcast the new state
// to every player tracking the edited column (editor included). Called only after SetBlock
// reported changed=true. Runs on the tick goroutine; all sends go through the bounded queue.
func (t *TickLoop) reconcileEdit(editor *tickPlayer, pos pk.Position, state block.StateID, sequence int32) {
	if editor.client != nil {
		editor.client.Send(blockChangedAck(sequence))
	}
	t.broadcastBlockUpdate(pos, state)
	udebugPlayer(editor, "edit", "pos=(%d,%d,%d) -> state=%d seq=%d", pos.X, pos.Y, pos.Z, state, sequence)
	// Level.updateNeighborsAt → LiquidBlock.neighborChanged: a break/place re-schedules every
	// neighboring fluid so adjacent water flows into the freshly-changed cell (e.g. breaking a
	// block under standing water makes the water fall in). Without this the world mutated but the
	// fluid sim never woke, leaving a permanent air gap next to water.
	t.scheduleFluidNeighborsOnEdit(pos)
	// Level.updateNeighborsAt → VegetationBlock.updateShape → Block.updateOrDestroy: a break/place
	// also re-checks the support of the block ABOVE the changed cell. If that block is a vegetation
	// feature (flower/sapling/grass/fern/bush/double-plant) that can no longer survive on the
	// now-changed ground, it is destroyed + dropped, cascading up a 2-tall plant / stacked column.
	// This closes the Phase-17 gate finding: "rompe un bloque con flores arriba, las flores no se
	// rompen" — vanilla destroys an unsupported plant the instant its support is removed.
	t.updateVegetationOnEdit(pos)
	// SUB-BLOCKTICK: Level.updateNeighborsAt -> SugarCaneBlock.updateShape — a break/place also
	// re-checks the support of a sugar cane in the cell ABOVE the changed cell; if it can no
	// longer survive, a destroy tick is SCHEDULED (delay 1) instead of an immediate destroy, and
	// the block-tick subsystem fires it next tick (tickBlock -> sugarCaneTick). This is the schedule
	// half of the scheduled-tick round-trip the subsystem exists to drive.
	t.onBlockTickEdit(pos)
}

// broadcastBlockUpdate sends a ClientboundBlockUpdate(pos, state) to every player whose view
// covers the edited column — for v1, every player whose center/sentChunks includes
// chunkCenterOf(pos). The editor is included so its own predictive edit reconciles against
// the authoritative state (a rejected/adjusted prediction snaps back). Runs on the tick
// owner over tick-owned player state; p.client.Send goes through the bounded outbound queue.
func (t *TickLoop) broadcastBlockUpdate(pos pk.Position, state block.StateID) {
	col := chunkCenterOf(int32(pos.X), int32(pos.Z))
	packet := blockUpdate(pos, state)
	for _, pl := range t.players {
		if pl.client == nil {
			continue
		}
		// A player tracks the column if it is its current center or in its sent set.
		if pl.center == col || (pl.sentChunks != nil && pl.sentChunks[col]) {
			pl.client.Send(packet)
		}
	}
}
