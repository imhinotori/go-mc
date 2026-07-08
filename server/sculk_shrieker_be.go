package server

// sculk_shrieker_be.go - the SCULK SHRIEKER block-entity + block behavior + the per-player
// WardenSpawnTracker warning-level state machine: a 1:1 port of net.minecraft.world.level.block.
// SculkShriekerBlock.{stepOn,tick} + SculkShriekerBlockEntity.{tryShriek,tryToWarn,shriek,canRespond,
// tryRespond,trySummonWarden} + net.minecraft.world.entity.monster.warden.WardenSpawnTracker.{tryWarn,
// increaseWarningLevel,setWarningLevel,onCooldown,copyData}, over the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, CFR + javap -c).
//
//   SculkShriekerBlock.stepOn(level, pos, state, entity): if a ServerPlayer stepped on it, resolve the
//     SculkShriekerBlockEntity and call tryShriek(level, player).
//
//   SculkShriekerBlockEntity.tryShriek(level, player):
//     if (player == null) return; if (SHRIEKING) return; warningLevel = 0;
//     if (canRespond(level) && !tryToWarn(level, player)) return;   // failed warn -> no shriek
//     shriek(level, player);
//
//   tryToWarn(level, player): OptionalInt r = WardenSpawnTracker.tryWarn(level, blockPos, player);
//     r.ifPresent(v -> this.warningLevel = v); return r.isPresent();
//
//   WardenSpawnTracker.tryWarn(level, pos, player):
//     if (hasNearbyWarden(level, pos)) return empty;                // no Warden entity in v1 -> false
//     List<ServerPlayer> players = getNearbyPlayers(level, pos);    // within 16, non-spectator
//     if (!players.contains(player)) players.add(player);
//     if (players.stream().anyMatch(p -> p.tracker.map(onCooldown).orElse(false))) return empty;
//     Optional<WardenSpawnTracker> max = players.stream().flatMap(tracker).max(comparingInt(warningLevel));
//     if (max.isPresent()) { WardenSpawnTracker t = max.get(); t.increaseWarningLevel();
//         players.forEach(p -> p.tracker.ifPresent(x -> x.copyData(t))); return OptionalInt.of(t.warningLevel); }
//     return empty;
//
//   increaseWarningLevel(): if (!onCooldown()) { ticksSinceLastWarning=0; cooldownTicks=200;
//       setWarningLevel(getWarningLevel()+1); }
//   setWarningLevel(i): warningLevel = Mth.clamp(i, 0, MAX_WARNING_LEVEL=4).
//   onCooldown(): cooldownTicks > 0.
//
//   shriek(level, entity): setBlock(SHRIEKING=true, flag 2); scheduleTick(pos, block, 90);
//       levelEvent(3007, pos, 0); gameEvent(SHRIEK, pos, entity).  (levelEvent/gameEvent deferred)
//   SculkShriekerBlock.tick: if (SHRIEKING) { setBlock(SHRIEKING=false, flag 3); tryRespond(level). }
//   tryRespond(level): if (canRespond(level) && warningLevel > 0) { if (!trySummonWarden(level))
//       playWardenReplySound(level); Warden.applyDarknessAround(level, center, null, 40). }
//   canRespond(level): CAN_SUMMON && difficulty != PEACEFUL && gameRules.SPAWN_WARDENS.
//
//   trySummonWarden -> at warning level 4 (MAX) spawn a Warden: DEFERRED (no Warden mob entity in v1 --
//       data-only, see recon). The warning-level MACHINE (0..4, the tryToWarn increment, the shriek +
//       the level-4 summon TRIGGER) all land; the actual Warden spawn is a cited no-op stub. The
//       darkness effect + the SonicBoom are likewise DEFERRED (no MobEffect darkness / Warden). CITE:
//       SculkShriekerBlockEntity.trySummonWarden (Warden.trySpawn) + Warden.applyDarknessAround.

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Shrieker + WardenSpawnTracker constants (VERIFIED javap).
const (
	sculkShriekerMaxWarningLevel    = 4   // WardenSpawnTracker.MAX_WARNING_LEVEL (Mth.clamp 0..4)
	sculkShriekerWarnCooldownTicks  = 200 // increaseWarningLevel -> cooldownTicks = 200
	sculkShriekerShriekingTicks     = 90  // shriek -> scheduleTick(pos, block, 90)
	sculkShriekerPlayerSearchRadius = 16  // WardenSpawnTracker.getNearbyPlayers closerThan(pos, 16.0)
)

const sculkShriekerTickType blockTickType = "minecraft:sculk_shrieker"

// sculkShriekerBE is the tick-owned state of one SculkShriekerBlockEntity: its warningLevel. The
// VibrationSystem.Data (in-flight vibration) is DEFERRED (the shrieker is driven by the direct
// stepOn/tryShriek path, matching a player stepping on it). CITE: SculkShriekerBlockEntity.warningLevel.
type sculkShriekerBE struct {
	warningLevel int
}

// wardenSpawnTracker is the per-player net.minecraft.world.entity.monster.warden.WardenSpawnTracker:
// the warning-level state a player accumulates as they trigger shriekers. Kept in a tick-owned side
// table keyed by player entity id (t.wardenTrackers), the ServerPlayer.wardenSpawnTracker analogue.
// CITE: WardenSpawnTracker fields (warningLevel, ticksSinceLastWarning, cooldownTicks).
type wardenSpawnTracker struct {
	warningLevel          int
	ticksSinceLastWarning int
	cooldownTicks         int
}

// onCooldown ports WardenSpawnTracker.onCooldown: cooldownTicks > 0. CITE.
func (w *wardenSpawnTracker) onCooldown() bool { return w.cooldownTicks > 0 }

// setWarningLevel ports WardenSpawnTracker.setWarningLevel: clamp 0..MAX_WARNING_LEVEL(4). CITE.
func (w *wardenSpawnTracker) setWarningLevel(i int) {
	if i < 0 {
		i = 0
	}
	if i > sculkShriekerMaxWarningLevel {
		i = sculkShriekerMaxWarningLevel
	}
	w.warningLevel = i
}

// increaseWarningLevel ports WardenSpawnTracker.increaseWarningLevel: if not on cooldown, reset the
// since-last-warning counter, arm the 200-tick cooldown, and bump the warning level. CITE.
func (w *wardenSpawnTracker) increaseWarningLevel() {
	if w.onCooldown() {
		return
	}
	w.ticksSinceLastWarning = 0
	w.cooldownTicks = sculkShriekerWarnCooldownTicks
	w.setWarningLevel(w.warningLevel + 1)
}

// copyData ports WardenSpawnTracker.copyData: copy warningLevel + cooldownTicks + ticksSinceLastWarning
// from another tracker (the sync that lifts every nearby player to the max-warning tracker's state).
// CITE: WardenSpawnTracker.copyData.
func (w *wardenSpawnTracker) copyData(src *wardenSpawnTracker) {
	w.warningLevel = src.warningLevel
	w.cooldownTicks = src.cooldownTicks
	w.ticksSinceLastWarning = src.ticksSinceLastWarning
}

// isSculkShriekerBlock is the block-identity gate used by the tick loop + createBlockEntityOnPlace.
func isSculkShriekerBlock(state block.StateID) bool {
	return block.IsSculkShrieker(state)
}

// resolveSculkShrieker returns the tick-owned sculkShriekerBE for pos, creating an EMPTY one on first
// access. Returns nil when pos is not a shrieker (or unloaded). Tick-owned.
func (t *TickLoop) resolveSculkShrieker(pos pk.Position) *sculkShriekerBE {
	if t.sculkShriekers == nil {
		t.sculkShriekers = make(map[pk.Position]*sculkShriekerBE)
	}
	if s, ok := t.sculkShriekers[pos]; ok {
		return s
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsSculkShrieker(state) {
		return nil
	}
	s := &sculkShriekerBE{}
	t.sculkShriekers[pos] = s
	return s
}

// resolveWardenTracker returns the per-player wardenSpawnTracker for a player entity id, creating a
// fresh (level-0) tracker on first access. The ServerPlayer.getWardenSpawnTracker analogue. Tick-owned.
func (t *TickLoop) resolveWardenTracker(playerID int32) *wardenSpawnTracker {
	if t.wardenTrackers == nil {
		t.wardenTrackers = make(map[int32]*wardenSpawnTracker)
	}
	if w, ok := t.wardenTrackers[playerID]; ok {
		return w
	}
	w := &wardenSpawnTracker{}
	t.wardenTrackers[playerID] = w
	return w
}

// tickSculkShriekers is the per-tick STEP scan for shriekers: a player standing on a shrieker triggers
// tryShriek (SculkShriekerBlock.stepOn). Only PLAYERS trigger a shrieker (tryGetPlayer: the entity or,
// for a projectile, its player owner). Also decays each player's warden tracker cooldown once per tick
// (WardenSpawnTracker.tick: ++ticksSinceLastWarning; --cooldownTicks; every 500t decrease the level --
// the decay interval is DEFERRED as a cited no-op, the cooldown countdown is the load-bearing part).
// Tick-owned.
func (t *TickLoop) tickSculkShriekers() {
	if t.world() == nil {
		return
	}
	// WardenSpawnTracker.tick per player: decrement the warn cooldown so a later shriek can re-warn.
	// (The DECREASE_WARNING_LEVEL_EVERY_INTERVAL 500-tick level decay is DEFERRED -- cited no-op.)
	for _, w := range t.wardenTrackers {
		if w.cooldownTicks > 0 {
			w.cooldownTicks--
		}
		w.ticksSinceLastWarning++
	}
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		t.sculkShriekerStepOnAt(blockPosOf(p.x, p.y, p.z), p)
	}
}

// sculkShriekerStepOnAt ports SculkShriekerBlock.stepOn for a player standing at pos: if the block is a
// shrieker, resolve its BE and tryShriek(level, player). CITE: SculkShriekerBlock.stepOn.
func (t *TickLoop) sculkShriekerStepOnAt(pos pk.Position, p *tickPlayer) {
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsSculkShrieker(state) {
		return
	}
	be := t.resolveSculkShrieker(pos)
	if be == nil {
		return
	}
	t.sculkShriekerTryShriek(pos, state, be, p)
}

// sculkShriekerTryShriek ports SculkShriekerBlockEntity.tryShriek: reset warningLevel=0; if canRespond
// and tryToWarn FAILS, return (no shriek); else shriek. CITE: SculkShriekerBlockEntity.tryShriek.
func (t *TickLoop) sculkShriekerTryShriek(pos pk.Position, state block.StateID, be *sculkShriekerBE, p *tickPlayer) {
	if p == nil {
		return
	}
	// if (SHRIEKING) return;
	if block.SculkShriekerShrieking(state) {
		return
	}
	be.warningLevel = 0
	if t.sculkShriekerCanRespond(state) && !t.sculkShriekerTryToWarn(pos, be, p) {
		return
	}
	t.sculkShriekerShriek(pos, state)
}

// sculkShriekerTryToWarn ports SculkShriekerBlockEntity.tryToWarn: WardenSpawnTracker.tryWarn ->
// OptionalInt; on present set this.warningLevel = value; return isPresent. CITE.
func (t *TickLoop) sculkShriekerTryToWarn(pos pk.Position, be *sculkShriekerBE, p *tickPlayer) bool {
	level, ok := t.wardenTrackerTryWarn(pos, p)
	if ok {
		be.warningLevel = level
	}
	return ok
}

// wardenTrackerTryWarn ports WardenSpawnTracker.tryWarn: no nearby Warden (always true in v1 -- no
// Warden entity); gather non-spectator players within 16 of pos (always include the triggering
// player); if ANY is on cooldown, abort (empty); else find the max-warning-level tracker, increase it,
// sync every nearby player to it, and return its warning level. CITE: WardenSpawnTracker.tryWarn.
func (t *TickLoop) wardenTrackerTryWarn(pos pk.Position, trigger *tickPlayer) (int, bool) {
	// hasNearbyWarden(level, pos): a Warden within 48 blocks. No Warden entity exists in v1, so this is
	// a cited constant FALSE (the warn always proceeds). CITE: WardenSpawnTracker.hasNearbyWarden.

	// getNearbyPlayers(level, pos): non-spectator ServerPlayers within closerThan(pos.center, 16.0).
	nearby := t.wardenNearbyPlayers(pos)
	// if (!players.contains(trigger)) players.add(trigger).
	found := false
	for _, pl := range nearby {
		if pl == trigger {
			found = true
			break
		}
	}
	if !found {
		nearby = append(nearby, trigger)
	}
	// anyMatch(onCooldown): if any nearby player's tracker is on cooldown, abort.
	for _, pl := range nearby {
		w := t.resolveWardenTracker(pl.entityID)
		if w.onCooldown() {
			return 0, false
		}
	}
	// max by warningLevel.
	var best *wardenSpawnTracker
	for _, pl := range nearby {
		w := t.resolveWardenTracker(pl.entityID)
		if best == nil || w.warningLevel > best.warningLevel {
			best = w
		}
	}
	if best == nil {
		return 0, false
	}
	best.increaseWarningLevel()
	// players.forEach(p -> p.tracker.copyData(best)).
	for _, pl := range nearby {
		w := t.resolveWardenTracker(pl.entityID)
		if w != best {
			w.copyData(best)
		}
	}
	return best.warningLevel, true
}

// wardenNearbyPlayers ports WardenSpawnTracker.getNearbyPlayers: non-spectator players within 16 of
// the shrieker (closerThan(pos.center, 16.0)). CITE.
func (t *TickLoop) wardenNearbyPlayers(pos pk.Position) []*tickPlayer {
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y) + 0.5
	cz := float64(pos.Z) + 0.5
	rSq := float64(sculkShriekerPlayerSearchRadius) * float64(sculkShriekerPlayerSearchRadius)
	var out []*tickPlayer
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		// isSpectator(): gamemode spectator. v1 has no spectator gate on tickPlayer here; a future
		// gamemode read slots in. All live players count (the common survival case).
		dx := p.x - cx
		dy := p.y - cy
		dz := p.z - cz
		if dx*dx+dy*dy+dz*dz <= rSq {
			out = append(out, p)
		}
	}
	return out
}

// sculkShriekerCanRespond ports SculkShriekerBlockEntity.canRespond: CAN_SUMMON && difficulty !=
// PEACEFUL && gameRules.SPAWN_WARDENS. serverDifficulty is the cited NORMAL stub (never PEACEFUL) and
// SPAWN_WARDENS defaults true (cited), so this reduces to CAN_SUMMON on the server. Structured so a
// real difficulty/gamerule read slots in later. CITE: SculkShriekerBlockEntity.canRespond.
func (t *TickLoop) sculkShriekerCanRespond(state block.StateID) bool {
	if !block.SculkShriekerCanSummon(state) {
		return false
	}
	if serverDifficulty == difficultyPeaceful {
		return false
	}
	const spawnWardensGamerule = true // GameRules.SPAWN_WARDENS default true (cited).
	return spawnWardensGamerule
}

// sculkShriekerShriek ports SculkShriekerBlockEntity.shriek: setBlock(SHRIEKING=true, flag 2);
// scheduleTick(pos, block, 90). The levelEvent(3007) shriek animation + the gameEvent(SHRIEK) are
// deferred (client cosmetic / no game-event bus). CITE: SculkShriekerBlockEntity.shriek.
func (t *TickLoop) sculkShriekerShriek(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	shrieking, ok := block.SculkShriekerWithShrieking(state, true)
	if !ok {
		return
	}
	// setBlock(..., flag 2 == UPDATE_CLIENTS only, no neighbor notify). Mirrored as SetBlock + broadcast.
	if t.world().SetBlock(pos, shrieking, dimMinY) {
		t.broadcastBlockUpdate(pos, shrieking)
	}
	t.scheduleBlockTick(pos, sculkShriekerTickType, sculkShriekerShriekingTicks)
}

// sculkShriekerTick ports SculkShriekerBlock.tick: if SHRIEKING, clear it (flag 3) and tryRespond.
// Dispatched from the scheduled-block-tick drain. CITE: SculkShriekerBlock.tick.
func (t *TickLoop) sculkShriekerTick(state block.StateID, pos pk.Position) {
	if !block.SculkShriekerShrieking(state) {
		return
	}
	cleared, ok := block.SculkShriekerWithShrieking(state, false)
	if !ok {
		return
	}
	if t.world().SetBlock(pos, cleared, dimMinY) {
		t.broadcastBlockUpdate(pos, cleared)
	}
	// getBlockEntity(pos).ifPresent(be -> be.tryRespond(level)).
	if be := t.resolveSculkShrieker(pos); be != nil {
		t.sculkShriekerTryRespond(pos, cleared, be)
	}
}

// sculkShriekerTryRespond ports SculkShriekerBlockEntity.tryRespond: if canRespond && warningLevel > 0,
// try to summon a Warden (DEFERRED -> always "failed") then play the reply sound (deferred) and apply
// darkness (DEFERRED). The WARNING-LEVEL machine + the level-4 summon TRIGGER land; the Warden spawn +
// darkness are cited no-ops (no Warden entity / darkness MobEffect in v1). CITE:
// SculkShriekerBlockEntity.tryRespond + trySummonWarden + Warden.applyDarknessAround.
func (t *TickLoop) sculkShriekerTryRespond(pos pk.Position, state block.StateID, be *sculkShriekerBE) {
	if !t.sculkShriekerCanRespond(state) || be.warningLevel <= 0 {
		return
	}
	// trySummonWarden(level): at warningLevel == MAX_WARNING_LEVEL(4) spawn a Warden. DEFERRED: no
	// Warden mob entity in v1 (data-only). This is the summon TRIGGER seam -- when a Warden entity
	// lands, replace this stub with Warden.trySpawn(...) at warningLevel 4. Returns false (no summon),
	// so the reply-sound branch runs (also deferred). CITE: SculkShriekerBlockEntity.trySummonWarden.
	summoned := t.sculkShriekerTrySummonWarden(pos, be)
	_ = summoned
	// playWardenReplySound (deferred). Warden.applyDarknessAround(level, center, null, 40): DEFERRED
	// (no darkness MobEffect). Secondary to the warning-level state machine + the now-live summon.
}

// sculkShriekerTrySummonWarden ports SculkShriekerBlockEntity.trySummonWarden: at warning level 4 (MAX)
// spawn a WARDEN. Vanilla runs Warden.trySpawn over WARDEN_SPAWN_ATTEMPTS in a WARDEN_SPAWN_RANGE_XZ/_Y
// box (a random valid nearby pos); v1 spawns the warden at the block above the shrieker (wardenSummonPos,
// the deterministic stand-in for the random box search) via the TRIGGERED emerge path (spawnWarden emerging=
// true -> the 134-tick EMERGE lock). Below warning level 4 it is a no-op (return false), so tryRespond falls
// to the reply-sound path. Returns true when a warden was summoned. CITE: SculkShriekerBlockEntity.trySummonWarden
// (warningLevel < MAX_WARNING_LEVEL -> false; else Warden.trySpawn -> emerge).
func (t *TickLoop) sculkShriekerTrySummonWarden(pos pk.Position, be *sculkShriekerBE) bool {
	if be.warningLevel < sculkShriekerMaxWarningLevel {
		return false // trySummonWarden: below max warning -> no summon attempt.
	}
	// warningLevel == 4: Warden.trySpawn at a valid nearby pos (the block above the shrieker), emerging.
	sx, sy, sz := wardenSummonPos(pos)
	w := t.spawnWarden(sx, sy, sz, true)
	return w != nil
}
