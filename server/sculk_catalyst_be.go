package server

// sculk_catalyst_be.go - the SCULK CATALYST block-entity: a 1:1 port of
// net.minecraft.world.level.block.entity.SculkCatalystBlockEntity.{serverTick,CatalystListener.
// handleGameEvent,CatalystListener.bloom} plus SculkCatalystBlock.tick over the unobfuscated 26.2
// jar (temp/cache/26.2-inner.jar, CFR + javap -c).
//
//   SculkCatalystBlockEntity.serverTick(level, pos, state, entity):
//       entity.catalystListener.getSculkSpreader().updateCursors(level, pos, level.getRandom(), true);
//     -> every tick the catalyst runs its SculkSpreader charge cursors (spreadVeins=true).
//
//   CatalystListener.handleGameEvent(level, gameEvent, ctx, pos): if gameEvent is ENTITY_DIE and the
//     source entity is a LivingEntity that has NOT consumed its XP:
//       int reward = deadEntity.getExperienceReward(level, lastAttacker);
//       if (deadEntity.shouldDropExperience() && reward > 0) {
//           sculkSpreader.addCursors(BlockPos.containing(eventPos.relative(UP, 0.5)), reward);
//           tryAwardItSpreadsAdvancement(level, deadEntity);
//       }
//       deadEntity.skipDropExperience();            // the catalyst CONSUMES the XP orb
//       positionSource.getPosition(level).ifPresent(v -> bloom(level, BlockPos.containing(v), ...));
//       return true;
//
//   CatalystListener.bloom(level, pos, state, random): setBlock(state.setValue(PULSE, true), 3);
//       level.scheduleTick(pos, block, 8);          // SculkCatalystBlock.tick clears PULSE after 8t
//       (SCULK_SOUL particle + SCULK_CATALYST_BLOOM sound: client cosmetic, deferred)
//
//   SculkCatalystBlock.tick(state, level, pos, random): if PULSE, setBlock(setValue(PULSE,false), 3).
//
// This is the sculk-SPREAD entry point: a mob dying within listener range (8, BY_DISTANCE) of a
// catalyst redirects its XP into the catalyst's spreader as charge, which then converts the ground
// around the catalyst to SCULK and grows SCULK_SENSOR/SCULK_SHRIEKER (sculk_spreader.go). Because the
// catalyst consumes the XP, no ExperienceOrb spawns (matching vanilla).
//
// The listener range gate (getListenerRadius 8, DeliveryMode BY_DISTANCE) is modeled directly: on a
// mob death dieEntity scans for catalysts whose BE position is within radius 8 of the death pos and
// delivers to the CLOSEST (BY_DISTANCE picks the single nearest listener). CITE: GameEventListener
// .DeliveryMode.BY_DISTANCE + CatalystListener.getListenerRadius (bipush 8).

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Catalyst constants (SculkCatalystBlockEntity / CatalystListener, VERIFIED javap).
const (
	sculkCatalystListenerRadius = 8 // CatalystListener.getListenerRadius (bipush 8)
	sculkCatalystBloomTicks     = 8 // bloom -> scheduleTick(pos, block, 8) (SculkCatalystBlock.tick clears PULSE)
)

// sculkCatalystTickType is the block id the catalyst bloom-clear tick (SculkCatalystBlock.tick) is
// scheduled/dispatched under. CITE: SculkCatalystBlockEntity.CatalystListener.bloom (scheduleTick).
const sculkCatalystTickType blockTickType = "minecraft:sculk_catalyst"

// sculkCatalystBE is the tick-owned state of one SculkCatalystBlockEntity: its persistent
// SculkSpreader (createLevelSpreader). The CatalystListener wrapper is folded in (the listener holds
// only the spreader + the block state + the position source, all reconstructable from pos).
type sculkCatalystBE struct {
	spreader *sculkLiveSpreader
}

// sculkCatalystServerTick ports SculkCatalystBlockEntity.serverTick: run the spreader cursors once
// (spreadVeins=true). CITE: SculkCatalystBlockEntity.serverTick.
func (t *TickLoop) sculkCatalystServerTick(pos pk.Position, c *sculkCatalystBE) {
	if c.spreader == nil {
		c.spreader = &sculkLiveSpreader{}
	}
	t.sculkLiveUpdateCursors(c.spreader, pos, true)
}

// tickSculkCatalysts ticks every live sculk-catalyst BE once per tick (the ServerLevel-side
// blockEntityTicker fan-out for SculkCatalystBlockEntity.serverTick). A catalyst broken/replaced/
// unloaded is dropped. Called from tickWorld (the tickConduits twin). Tick-owned.
func (t *TickLoop) tickSculkCatalysts() {
	if len(t.sculkCatalysts) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, c := range t.sculkCatalysts {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !isSculkCatalystBlock(state) {
			delete(t.sculkCatalysts, pos)
			continue
		}
		t.sculkCatalystServerTick(pos, c)
	}
}

// resolveSculkCatalyst returns the tick-owned sculkCatalystBE for pos, creating an EMPTY one (an
// inactive spreader) on first access. Returns nil when pos is not a catalyst block (or unloaded).
// Tick-owned (the conduits twin).
func (t *TickLoop) resolveSculkCatalyst(pos pk.Position) *sculkCatalystBE {
	if t.sculkCatalysts == nil {
		t.sculkCatalysts = make(map[pk.Position]*sculkCatalystBE)
	}
	if c, ok := t.sculkCatalysts[pos]; ok {
		return c
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !isSculkCatalystBlock(state) {
		return nil
	}
	c := &sculkCatalystBE{spreader: &sculkLiveSpreader{}}
	t.sculkCatalysts[pos] = c
	return c
}

// isSculkCatalystBlock is the block-identity gate used by the tick loop + createBlockEntityOnPlace.
func isSculkCatalystBlock(state block.StateID) bool {
	return block.IsSculkCatalyst(state)
}

// sculkCatalystOnEntityDie ports SculkCatalystBlockEntity.CatalystListener.handleGameEvent for the
// ENTITY_DIE event: find the nearest sculk catalyst whose listener range (8, BY_DISTANCE) covers the
// death position; if one exists and the mob would drop XP (shouldDropExperience && reward > 0), feed
// the reward into that catalyst's SculkSpreader as charge at BlockPos.containing(deathPos.up(0.5)),
// bloom the catalyst, and CONSUME the XP (return true so the caller skips the orb). Returns false when
// no catalyst is in range (the normal XP-orb path runs). CITE: CatalystListener.handleGameEvent.
func (t *TickLoop) sculkCatalystOnEntityDie(e *Entity) bool {
	if t.world() == nil || len(t.sculkCatalysts) == 0 {
		return false
	}
	// deadEntity instanceof LivingEntity: every mob in v1 is a LivingEntity; a non-living projectile/
	// item never reaches dropMobExperience, so this is satisfied by construction.
	// BY_DISTANCE: pick the single NEAREST listener whose radius covers the source position. The event
	// position is the mob's position (Entity.position()); the listener source is the catalyst block
	// center. A catalyst at BE-pos P listens iff distance(P.center, deathPos) <= getListenerRadius(8).
	deathX, deathY, deathZ := e.x, e.y, e.z
	var best *sculkCatalystBE
	var bestPos pk.Position
	bestDistSq := math.MaxFloat64
	radiusSq := float64(sculkCatalystListenerRadius) * float64(sculkCatalystListenerRadius)
	for pos, c := range t.sculkCatalysts {
		st, ok := t.world().GetBlock(pos, dimMinY)
		if !ok || !block.IsSculkCatalyst(st) {
			continue
		}
		cx := float64(pos.X) + 0.5
		cy := float64(pos.Y) + 0.5
		cz := float64(pos.Z) + 0.5
		dx := cx - deathX
		dy := cy - deathY
		dz := cz - deathZ
		d := dx*dx + dy*dy + dz*dz
		if d > radiusSq {
			continue // outside listener range
		}
		if d < bestDistSq {
			bestDistSq = d
			best = c
			bestPos = pos
		}
	}
	if best == nil {
		return false
	}

	// int reward = deadEntity.getExperienceReward(level, lastAttacker). shouldDropExperience()==!isBaby.
	// v1 reward is the jar-verified base value (entityBaseExperienceReward: an Animal draws 1+nextInt(3)
	// on its OWN RNG at the death event, outside the oracle window). A baby (isBaby) drops no XP.
	if e.isBaby() {
		// deadEntity.shouldDropExperience() false: no charge fed, but the event is still handled (the
		// catalyst consumed the ENTITY_DIE) -> bloom + return true (skipDropExperience is a no-op for a
		// zero-reward mob, and the orb path is skipped either way). Fall through to the bloom.
	} else {
		reward := t.entityBaseExperienceReward(e)
		if reward > 0 {
			// sculkSpreader.addCursors(BlockPos.containing(eventPos.relative(UP, 0.5)), reward).
			cursorPos := pk.Position{X: floorInt(deathX), Y: floorInt(deathY + 0.5), Z: floorInt(deathZ)}
			if best.spreader == nil {
				best.spreader = &sculkLiveSpreader{}
			}
			best.spreader.addCursors(cursorPos, reward)
			// tryAwardItSpreadsAdvancement(level, deadEntity): the "It Spreads" advancement trigger. There
			// is no predicate-fed advancement trigger in v1 (only inventory_changed item ids parse), so this
			// is a cited no-op — the XP-redirect + bloom (the observable gameplay) land. CITE:
			// CatalystListener.tryAwardItSpreadsAdvancement (KILL_MOB_NEAR_SCULK_CATALYST trigger).
		}
	}

	// deadEntity.skipDropExperience(): the catalyst consumed the XP -> the caller skips the orb (this
	// method returns true). Bloom the catalyst (positionSource.getPosition().ifPresent(bloom)).
	t.sculkCatalystBloom(bestPos, best)
	return true
}

// sculkCatalystBloom ports CatalystListener.bloom: set PULSE (bloom) true and schedule the 8-tick
// clear (SculkCatalystBlock.tick). The SCULK_SOUL particle + SCULK_CATALYST_BLOOM sound are client
// cosmetic (deferred). CITE: SculkCatalystBlockEntity.CatalystListener.bloom.
func (t *TickLoop) sculkCatalystBloom(pos pk.Position, c *sculkCatalystBE) {
	if t.world() == nil {
		return
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsSculkCatalyst(state) {
		return
	}
	bloomed, ok := block.SculkCatalystWithBloom(state, true)
	if !ok {
		return
	}
	// setBlock(state.setValue(PULSE, true), 3) — flag 3 = UPDATE_NEIGHBORS|UPDATE_CLIENTS.
	if t.world().SetBlock(pos, bloomed, dimMinY) {
		t.broadcastBlockUpdate(pos, bloomed)
	}
	// level.scheduleTick(pos, block, 8): SculkCatalystBlock.tick clears PULSE after 8 ticks.
	t.scheduleBlockTick(pos, sculkCatalystTickType, sculkCatalystBloomTicks)
}

// sculkCatalystTick ports SculkCatalystBlock.tick: if PULSE (bloom) is set, clear it. Dispatched from
// the scheduled-block-tick drain (block_ticks.go tickBlock). CITE: SculkCatalystBlock.tick.
func (t *TickLoop) sculkCatalystTick(state block.StateID, pos pk.Position) {
	if !block.SculkCatalystBloom(state) {
		return // PULSE already false: nothing to do (the scheduled tick found it cleared).
	}
	cleared, ok := block.SculkCatalystWithBloom(state, false)
	if !ok {
		return
	}
	if t.world().SetBlock(pos, cleared, dimMinY) {
		t.broadcastBlockUpdate(pos, cleared)
	}
}
