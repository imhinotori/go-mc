package server

// bell_be.go -- the BELL BLOCK-ENTITY (BELL-01): a 1:1 port of
// net.minecraft.world.level.block.entity.BellBlockEntity.serverTick / triggerEvent / onHit over the 26.2
// jar (temp/cache/26.2-inner.jar, CFR/javap this session). The bell is a per-tick DRIVE (the campfires
// twin, t.bells keyed by world position). Ringing it (a right-click, projectile hit, or raid) fires a
// block event that sets shaking=true + ticks=0 + resonationTicks=0 + clickDirection; the serverTick then
// counts ticks up while shaking, stops shaking at DURATION (50), and -- at ticks>=5 with raiders nearby --
// enters the RESONATE state, counting resonationTicks up to MAX_RESONATION_TICKS (40) before running the
// resonation-end action (makeRaidersGlow on the server).
//
// 1:1 jar (net.minecraft.world.level.block.entity.BellBlockEntity), VERIFIED CFR (inlined static finals):
//   DURATION = 50; TICKS_BEFORE_RESONATION = 5; MAX_RESONATION_TICKS = 40; HEAR_BELL_RADIUS = 32;
//   SEARCH_RADIUS = 48 (updateEntities AABB inflate). (GLOW_DURATION/HIGHLIGHT_RAIDERS_RADIUS are used only
//   by the deferred glow.)
//
//   triggerEvent(id, arg): if (id == 1) { updateEntities(); resonationTicks = 0;
//       clickDirection = Direction.from3DDataValue(arg); ticks = 0; shaking = true; return true; }
//       else return super.triggerEvent(id, arg);
//   onHit(direction): clickDirection = direction; if (shaking) ticks = 0; else shaking = true;
//       level.blockEvent(pos, block, 1, direction.get3DDataValue());   // -> triggerEvent on all copies
//   tick(level, pos, state, entity, resonationEndAction):
//       if (shaking) ticks++;
//       if (ticks >= 50) { shaking = false; ticks = 0; }
//       if (ticks >= 5 && resonationTicks == 0 && areRaidersNearby(pos, nearbyEntities)) {
//           resonating = true; playSound(BELL_RESONATE);        // sound deferred
//       }
//       if (resonating) {
//           if (resonationTicks < 40) resonationTicks++;
//           else { resonationEndAction.run(level, pos, nearbyEntities); resonating = false; }
//       }
//
// The shaking/ticks countdown + the resonate machine are preserved tick-for-tick. updateEntities (the
// villager HEARD_BELL_TIME brain-memory scan) + makeRaidersGlow (the raider GLOWING effect) are
// cite-deferred: v1 has no villager brain-memory seam and no entity-glow flag seam. nearbyEntities is
// therefore left empty, so areRaidersNearby is always false and the resonate branch never fires -- the
// exact observable result in a world with no raid/raiders (the common case). The BELL_BLOCK / BELL_RESONATE
// sounds + the particle stream are cite-deferred (client cosmetic).

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Bell constants (BellBlockEntity static finals, VERIFIED CFR -- inlined in the tick bytecode).
const (
	bellDuration            = 50 // DURATION -- shaking stops at ticks >= 50
	bellTicksBeforeResonate = 5  // TICKS_BEFORE_RESONATION -- resonate can start at ticks >= 5
	bellMaxResonationTicks  = 40 // MAX_RESONATION_TICKS -- resonationTicks counts up to 40
)

// bellBE is the tick-owned state of one bell block-entity -- the Go analogue of BellBlockEntity narrowed to
// the fields serverTick + onHit read/write. clickDirection is the face rung (3D data value); the
// nearbyEntities scan (updateEntities) is DEFERRED (no villager brain-memory seam), so it is not modeled --
// areRaidersNearby is always false in v1, so resonating never engages.
type bellBE struct {
	ticks           int  // BellBlockEntity.ticks -- counts up while shaking
	shaking         bool // BellBlockEntity.shaking -- true from a ring until DURATION
	clickDirection  int  // BellBlockEntity.clickDirection (Direction.get3DDataValue); -1 == none
	resonating      bool // BellBlockEntity.resonating -- the 40-tick reveal-raiders window
	resonationTicks int  // BellBlockEntity.resonationTicks -- counts up to MAX_RESONATION_TICKS
}

// bellServerTick ports BellBlockEntity.tick(level, pos, state, entity, resonationEndAction) EXACTLY (the
// serverTick lambda binds resonationEndAction to makeRaidersGlow -- DEFERRED cited): the shaking/ticks
// countdown, the DURATION cut-off, the raiders-nearby resonate trigger, and the resonationTicks wind-up to
// the resonation-end action. Runs on the tick goroutine.
//
// 1:1 net.minecraft.world.level.block.entity.BellBlockEntity.tick
func (t *TickLoop) bellServerTick(pos pk.Position, b *bellBE) {
	// if (shaking) ticks++;
	if b.shaking {
		b.ticks++
	}
	// if (ticks >= 50) { shaking = false; ticks = 0; }
	if b.ticks >= bellDuration {
		b.shaking = false
		b.ticks = 0
	}
	// if (ticks >= 5 && resonationTicks == 0 && areRaidersNearby(pos, nearbyEntities)) { resonating = true;
	//     playSound(BELL_RESONATE); }
	// nearbyEntities is DEFERRED (no brain-memory scan) -> areRaidersNearby is always false in v1, so this
	// branch never fires. The gate structure is preserved so it becomes live once a raid/glow seam lands.
	if b.ticks >= bellTicksBeforeResonate && b.resonationTicks == 0 && t.bellAreRaidersNearby(pos, b) {
		b.resonating = true
		// playSound(BELL_RESONATE): cite-deferred (no BE sound-event emit seam in v1).
	}
	// if (resonating) { if (resonationTicks < 40) resonationTicks++; else { end.run(...); resonating=false; } }
	if b.resonating {
		if b.resonationTicks < bellMaxResonationTicks {
			b.resonationTicks++
		} else {
			// resonationEndAction.run(level, pos, nearbyEntities) == makeRaidersGlow: DEFERRED (no entity
			// GLOWING flag seam). The state transition (resonating -> false) is preserved.
			b.resonating = false
		}
	}
}

// bellAreRaidersNearby ports BellBlockEntity.areRaidersNearby(pos, nearbyEntities): true iff any alive,
// non-removed LivingEntity in the (deferred) nearbyEntities list is within HEAR_BELL_RADIUS (32) of the
// bell and is #minecraft:raiders. v1 does not build nearbyEntities (updateEntities is DEFERRED -- no
// villager brain-memory seam and no raider-glow seam), so this always returns false: a bell in a
// raider-free world (the common case) never resonates, the exact vanilla result. CITE
// BellBlockEntity.areRaidersNearby (reveal-raiders DEFERRED, cited).
func (t *TickLoop) bellAreRaidersNearby(_ pk.Position, _ *bellBE) bool {
	return false
}

// bellOnHit ports BellBlockEntity.onHit(direction): record the rung face, restart the shake (or reset ticks
// if already shaking), and fire the block event (level.blockEvent -> triggerEvent(1, direction) on every
// copy). v1 has no client-side BE copy to re-trigger, so onHit applies the triggerEvent(1) effect DIRECTLY
// (updateEntities DEFERRED; resonationTicks=0; clickDirection; ticks=0; shaking=true) -- the observable
// server state after a ring. CITE BellBlockEntity.onHit + triggerEvent.
//
// 1:1 net.minecraft.world.level.block.entity.BellBlockEntity.onHit / triggerEvent(1, arg)
func (t *TickLoop) bellOnHit(b *bellBE, direction int) {
	// onHit: clickDirection = direction; if (shaking) ticks = 0; else shaking = true;
	// triggerEvent(1, arg) (fired via blockEvent, applied here directly): updateEntities() [DEFERRED];
	//     resonationTicks = 0; clickDirection = from3DDataValue(arg); ticks = 0; shaking = true.
	// The two together set: shaking=true, ticks=0, resonationTicks=0, clickDirection=direction.
	b.clickDirection = direction
	b.resonationTicks = 0
	b.ticks = 0
	b.shaking = true
}

// useBell ports BellBlock.useWithoutItem -> onHit(level, state, hit, player, canRing=true) ->
// attemptToRing: a right-click on the bell rings it (BellBlockEntity.onHit + the BELL_BLOCK sound +
// BLOCK_CHANGE gameEvent), consuming the interaction so no block is placed. The isProperHit hit-face check
// (the bell only rings when struck on the swinging face) is folded to always-ring in v1 (the reach-gated
// right-click is treated as a proper hit -- the common case; the precise voxel-face gate is a cited
// cosmetic refinement). The rung face is derived from the block FACING (attemptToRing uses FACING when the
// hit direction is null). CITE BellBlock.useWithoutItem / onHit / attemptToRing.
//
// 1:1 net.minecraft.world.level.block.BellBlock.useWithoutItem
func (t *TickLoop) useBell(p *tickPlayer, pos pk.Position, state block.StateID) bool {
	_ = p
	if t.world() == nil {
		return false
	}
	b := t.resolveBell(pos)
	if b == nil {
		return false
	}
	// attemptToRing: direction defaults to state.getValue(FACING) when the hit direction is null.
	dir := block.Down
	if f, ok := bellFacing(state); ok {
		dir = f
	}
	// BellBlockEntity.onHit(direction): start the shake + reset the counters.
	t.bellOnHit(b, bellDirection3D(dir))
	// playSound(BELL_BLOCK) + gameEvent(BLOCK_CHANGE): cite-deferred (client cosmetic / no BE gameEvent seam).
	// awardStat(BELL_RING): no stats subsystem (DEFERRED).
	return true // the bell consumed the interaction (rung) -- never a block-place fall-through.
}

// resolveBell returns the tick-owned bellBE for pos, creating an EMPTY one (not shaking, no click) on first
// access -- the analogue of a freshly-placed bell default BellBlockEntity. Returns nil when pos is not a
// bell block (or the world is unloaded). Tick-owned (t.bells, the t.campfires twin).
func (t *TickLoop) resolveBell(pos pk.Position) *bellBE {
	if t.bells == nil {
		t.bells = make(map[pk.Position]*bellBE)
	}
	if b, ok := t.bells[pos]; ok {
		return b
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !isBellBlock(state) {
		return nil
	}
	b := &bellBE{clickDirection: -1}
	t.bells[pos] = b
	return b
}

// bellFacing returns the FACING of a bell (the swing axis), or (Down, false) for a non-bell. CITE
// BellBlock.FACING (attemptToRing uses it as the default rung direction).
func bellFacing(s block.StateID) (block.Direction, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return block.Down, false
	}
	if b, ok := block.StateList[s].(block.Bell); ok {
		return b.Facing, true
	}
	return block.Down, false
}

// bellDirection3D ports Direction.get3DDataValue: DOWN=0, UP=1, NORTH=2, SOUTH=3, WEST=4, EAST=5. Used to
// stamp the rung face into clickDirection (the deferred bell-swing animation reads it). CITE
// Direction.get3DDataValue.
func bellDirection3D(d block.Direction) int {
	switch d {
	case block.Down:
		return 0
	case block.Up:
		return 1
	case block.North:
		return 2
	case block.South:
		return 3
	case block.West:
		return 4
	case block.East:
		return 5
	}
	return 0
}

// tickBells ticks every live bell block-entity once per tick (the blockEntityTicker fan-out for
// BellBlockEntity.serverTick). Called from tickWorld (the tickCampfires twin). A non-bell cell
// (broken/replaced) drops the BE from the store. An idle (not-shaking, not-resonating) bell ticks to a
// cheap no-op. A nil world leaves bells un-ticked (tests may drive bellServerTick directly). Tick-owned.
func (t *TickLoop) tickBells() {
	if len(t.bells) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, b := range t.bells {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !isBellBlock(state) {
			delete(t.bells, pos)
			continue
		}
		t.bellServerTick(pos, b)
	}
}
