package server

// player_sleep.go — SLEEP-01: the player-sleep subsystem, a 1:1 port of the LivingEntity/Player sleep
// state machine (temp/cache/26.2-inner.jar, javap -c -p + CFR this session). The base subsystem the cat
// comfort goals (CatRelaxOnOwnerGoal / CatLieOnBedGoal / the morning-gift) gate on: owner.isSleeping()
// and owner.getSleepTimer().
//
// Vanilla state ported onto tickPlayer (tick.go):
//   - LivingEntity.SLEEPING_POS_ID (Optional<BlockPos>)  -> tickPlayer.sleepingPos (nil == awake)
//   - Player.sleepCounter (int)                          -> tickPlayer.sleepCounter
//
// Vanilla methods ported here:
//   - LivingEntity.isSleeping()       == getSleepingPos().isPresent()        -> isSleeping (sleepingPos!=nil)
//   - Player.getSleepTimer()          == sleepCounter                        -> getSleepTimer
//   - LivingEntity.startSleeping(pos) sets OCCUPIED, pose, pos-to-bed, SLEEPING_POS
//   - Player.startSleepInBed(pos)     == startSleeping(pos); sleepCounter = 0
//   - LivingEntity.stopSleeping()     clears OCCUPIED + SLEEPING_POS
//   - Player.tick sleep branch        climbs sleepCounter to 100 while sleeping, unwinds to 0 while waking
//
// The bed right-click (BedBlock.useWithoutItem, chest_open.go useBed) drives startSleepInBed;
// tickPlayerSleep drives the per-tick sleepCounter advance. Both run only on the tick goroutine
// (TICK-05), so the sleep state is -race clean by the single-owner discipline the rest of tickPlayer
// follows.

import (
	pk "github.com/imhinotori/sulfur/net/packet"
)

// sleepDuration is Player.SLEEP_DURATION == 100 — the sleepCounter ceiling while sleeping AND the
// getSleepTimer() >= 100 threshold the cat morning-gift gate reads.
//
//	[VERIFIED CFR Player: SLEEP_DURATION = 100; javap Player.tick: bipush 100.]
const sleepDuration = 100

// sleepWakeThreshold is the sleepCounter value at which the waking unwind resets to 0 == SLEEP_DURATION
// (100) + WAKE_UP_DURATION (10). Verified in the bytecode as bipush 110.
//
//	[VERIFIED CFR Player: WAKE_UP_DURATION = 10; javap Player.tick waking branch: bipush 110 if_icmplt.]
const sleepWakeThreshold = 110

// isSleeping ports LivingEntity.isSleeping() == getSleepingPos().isPresent(). CatRelaxOnOwnerGoal.canUse
// gates on it (owner.isSleeping()).
//
//	[VERIFIED CFR LivingEntity.isSleeping: return this.getSleepingPos().isPresent().]
func (p *tickPlayer) isSleeping() bool { return p.sleepingPos != nil }

// getSleepTimer ports Player.getSleepTimer() == sleepCounter. The morning-gift gate reads it (>= 100).
//
//	[VERIFIED CFR Player.getSleepTimer: return this.sleepCounter.]
func (p *tickPlayer) getSleepTimer() int { return p.sleepCounter }

// startSleepInBed ports Player.startSleepInBed(pos): startSleeping(pos); sleepCounter = 0. The
// ServerPlayer.startSleepInBed BedSleepingProblem pre-checks are applied by the caller useBed BEFORE
// this, matching vanilla order (the block useWithoutItem + ServerPlayer.startSleepInBed run the gates,
// then reach super.startSleepInBed).
//
//	[VERIFIED javap Player.startSleepInBed: startSleeping(pos); sleepCounter = 0; return Either.right.]
func (t *TickLoop) startSleepInBed(p *tickPlayer, pos pk.Position) {
	t.startSleeping(p, pos)
	p.sleepCounter = 0
}

// startSleeping ports LivingEntity.startSleeping(pos): if the block at pos is a bed, set its OCCUPIED
// property true; set the sleeping pos; snap the player onto the bed (setPosToBed). The pose
// (Pose.SLEEPING) + deltaMovement-zero are set server-side; the pose/pos WIRE render is a cited follow-up
// (the observable gameplay this unblocks — the cat comfort goals — reads only isSleeping(),
// getSleepTimer() and the OCCUPIED block bit, all set here). isPassenger()/stopRiding() is a v1 no-op.
//
//	[VERIFIED javap LivingEntity.startSleeping: if getBlockState(pos).getBlock() instanceof BedBlock ->
//	 setBlock(pos, state.setValue(OCCUPIED,true),3); setPose(SLEEPING); setPosToBed(pos);
//	 setSleepingPos(pos); setDeltaMovement(ZERO). setPosToBed: setPos(x+0.5, y+0.6875, z+0.5).]
func (t *TickLoop) startSleeping(p *tickPlayer, pos pk.Position) {
	if t.world() != nil {
		if s, ok := t.world().GetBlock(pos, dimMinY); ok {
			if occ, ok := bedStateWithOccupied(s, true); ok {
				if t.world().SetBlock(pos, occ, dimMinY) {
					t.broadcastBlockUpdate(pos, occ)
				}
			}
		}
	}
	p.x = float64(pos.X) + 0.5
	p.y = float64(pos.Y) + 0.6875
	p.z = float64(pos.Z) + 0.5
	sp := pos
	p.sleepingPos = &sp
}

// stopSleeping ports the observable core of LivingEntity.stopSleeping(): if the recorded bed still
// exists, clear its OCCUPIED bit; then clear the sleeping pos (wake). The stand-up reposition
// (BedBlock.findStandUpPosition + the look-direction yaw) is a cited follow-up — the sleep STATE machine
// (the cat-goal gate) needs only the wake + OCCUPIED clear. setPose(STANDING) is server-side.
//
//	[VERIFIED javap LivingEntity.stopSleeping: getSleepingPos().filter(hasChunkAt).ifPresent(bp -> if
//	 BedBlock -> setBlock(bp, setValue(OCCUPIED,false),3); findStandUpPosition ...); setPose(STANDING);
//	 clearSleepingPos().]
func (t *TickLoop) stopSleeping(p *tickPlayer) {
	if p.sleepingPos != nil && t.world() != nil {
		pos := *p.sleepingPos
		if s, ok := t.world().GetBlock(pos, dimMinY); ok {
			if unocc, ok := bedStateWithOccupied(s, false); ok {
				if t.world().SetBlock(pos, unocc, dimMinY) {
					t.broadcastBlockUpdate(pos, unocc)
				}
			}
		}
	}
	p.sleepingPos = nil
}

// tickPlayerSleep ports the sleep branch of Player.tick, run inside tickEntities (a fixed-phase per-
// player step, sibling of tickBreath/tickFood — no phase reorder). When sleeping: increment sleepCounter,
// clamp to SLEEP_DURATION (100), and (server-side) wake at dawn when BedRule.canSleep(level) turns false.
// When not sleeping but sleepCounter > 0: increment and reset to 0 once it reaches 110 (the waking
// unwind). BedRule.canSleep == CAN_SLEEP_WHEN_DARK == WHEN_DARK.test == level.isDarkOutside(); v1 uses
// the night-window proxy isDarkEnoughToSpawn (the spawner/fire day-phase, cited for the absent lighting
// engine). stopSleepInBed(false,false) sets sleepCounter = 100, so the waking-unwind then runs 100..110->0.
//
//	[VERIFIED javap Player.tick: isSleeping ifeq -> waking branch; sleeping branch ++sleepCounter,
//	 bipush 100 clamp, level.isClientSide + BedRule.canSleep guard -> stopSleepInBed(false,false);
//	 waking branch sleepCounter>0 ++, bipush 110 -> 0. CFR: stopSleepInBed sets sleepCounter = 100.]
func (t *TickLoop) tickPlayerSleep() {
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if p.isSleeping() {
			p.sleepCounter++
			if p.sleepCounter > sleepDuration {
				p.sleepCounter = sleepDuration
			}
			if !t.bedRuleCanSleep() {
				t.stopSleepInBed(p, false)
			}
		} else if p.sleepCounter > 0 {
			p.sleepCounter++
			if p.sleepCounter >= sleepWakeThreshold {
				p.sleepCounter = 0
			}
		}
	}
}

// stopSleepInBed ports Player.stopSleepInBed(wakeImmediately, updateLevelForSleepingPlayers):
// super.stopSleeping(); (updateSleepingPlayerList — v1 no-op, no sleep-vote subsystem); sleepCounter =
// wakeImmediately ? 0 : 100. The wake-at-dawn branch calls it (false,false) -> sleepCounter = 100.
//
//	[VERIFIED javap Player.stopSleepInBed: super.stopSleeping(); (ServerLevel updateSleepingPlayerList);
//	 sleepCounter = arg1 ? 0 : 100.]
func (t *TickLoop) stopSleepInBed(p *tickPlayer, wakeImmediately bool) {
	t.stopSleeping(p)
	if wakeImmediately {
		p.sleepCounter = 0
	} else {
		p.sleepCounter = sleepDuration
	}
}

// bedRuleCanSleep ports BedRule.CAN_SLEEP_WHEN_DARK.canSleep(level) (the default BED_RULE env attribute):
// Rule.WHEN_DARK.test(level) == level.isDarkOutside(). v1 uses the night-window gametime proxy
// (isDarkEnoughToSpawn) as the isDarkOutside analog — the SAME day-phase the spawner/fire subsystems cite
// for the absent lighting engine. CITE BedRule.CAN_SLEEP_WHEN_DARK + Level.isDarkOutside.
func (t *TickLoop) bedRuleCanSleep() bool {
	return t.isDarkEnoughToSpawn()
}
