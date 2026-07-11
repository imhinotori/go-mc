package server

// sleep.go -- SLEEP-01 (respawn + night skip): the ServerPlayer.startSleepInBed BedSleepingProblem
// gates, the setRespawnPosition on a successful sleep, the sleeping-pose broadcast so observers see the
// player lying down, and the ServerLevel.tick all-players-asleep night-skip + weather-clear handling.
// It sits on top of player_sleep.go (the per-player sleep STATE machine). Ported 1:1 from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//   - ServerPlayer.startSleepInBed(BlockPos) -- the gate chain (OTHER_PROBLEM, BedRule
//     canSleep/canSetSpawn, TOO_FAR_AWAY, OBSTRUCTED, the canSetSpawn respawn set, NOT_SAFE monster
//     scan), then super.startSleepInBed(pos).
//   - SleepStatus -- sleepersNeeded/areEnoughSleeping/areEnoughDeepSleeping.
//   - ServerLevel.tick sleep block -- players_sleeping_percentage, areEnoughSleeping &&
//     areEnoughDeepSleeping -> skip to dawn + wakeUpAllPlayers + resetWeatherCycle.
//
// PIG-ORACLE SAFETY: sleep is a PLAYER mechanic. tickSleep runs on the coordinator (like tickWeather),
// only from tickOnce -- the pig oracle drives serverAiStep directly, never tickOnce. It draws NO RNG,
// and a pig is CREATURE, not a Monster preventing rest. The pig never sleeps and is never touched.

import (
	"io"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// bedSleepingProblem is net.minecraft.world.entity.player.Player$BedSleepingProblem (the result of the
// gate chain), or bedProblemNone on success. v1 has no overlay-message subsystem, so the message
// component is a cited follow-up; the RESULT is the observable contract the tests assert.
//
//	[VERIFIED javap Player$BedSleepingProblem: TOO_FAR_AWAY, OBSTRUCTED, OTHER_PROBLEM, NOT_SAFE;
//	 NOT_POSSIBLE_NOW carried via BedRule.asProblem.]
type bedSleepingProblem int

const (
	bedProblemNone        bedSleepingProblem = iota // Either.right(Unit): sleep started
	bedProblemOther                                 // OTHER_PROBLEM: not sleeping-capable / not alive
	bedProblemNotPossible                           // BedRule.asProblem: canSleep false (NOT_POSSIBLE_NOW)
	bedProblemTooFar                                // TOO_FAR_AWAY
	bedProblemObstructed                            // OBSTRUCTED
	bedProblemNotSafe                               // NOT_SAFE: a Monster is preventing rest nearby
)

// Sleeping-pose entity metadata indices + serializer ids + Pose ordinals. A sleeping player renders via
// two synched values pushed by SetEntityData: DATA_POSE (Entity index 6, POSE serializer 20) =
// Pose.SLEEPING (ordinal 2), and DATA_SLEEPING_POS_ID (LivingEntity index 14, OPTIONAL_BLOCK_POS
// serializer 11) = the bed pos.
//
//	[VERIFIED javap Entity static{}: index 6 = DATA_POSE (POSE serializer). LivingEntity: index 14 =
//	 SLEEPING_POS_ID (OPTIONAL_BLOCK_POS). EntityDataSerializers registerSerializer order:
//	 OPTIONAL_BLOCK_POS=11, POSE=20. Pose ordinals: STANDING=0, FALL_FLYING=1, SLEEPING=2.]
const (
	dataPoseIndex         uint8 = 6
	dataSleepingPosIndex  uint8 = 14
	poseSerializerID      int32 = 20
	optionalBlockPosSerID int32 = 11
	poseStanding          int32 = 0
	poseSleeping          int32 = 2
)

// poseValue is the pk.FieldEncoder for a DATA_POSE synched value: the Pose enum id as a VarInt (the POSE
// STREAM_CODEC is the by-ordinal enum codec).
type poseValue struct{ ordinal int32 }

func (v poseValue) WriteTo(w io.Writer) (int64, error) { return pk.VarInt(v.ordinal).WriteTo(w) }

// optionalBlockPosValue is the pk.FieldEncoder for an Optional<BlockPos> synched value -- the
// OPTIONAL_BLOCK_POS codec == ByteBufCodecs.optional(BLOCK_POS): a Boolean present flag, then (only if
// present) the packed-long BlockPos (pk.Position). Empty == a single Boolean(false). Mirrors
// optionalBlockStateValue (entity_encode.go).
type optionalBlockPosValue struct {
	pos     pk.Position
	present bool
}

func (v optionalBlockPosValue) WriteTo(w io.Writer) (int64, error) {
	var n int64
	c, err := pk.Boolean(v.present).WriteTo(w)
	n += c
	if err != nil {
		return n, err
	}
	if v.present {
		c, err = v.pos.WriteTo(w)
		n += c
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// broadcastSleepingPose pushes the player DATA_POSE + DATA_SLEEPING_POS_ID to its own client AND every
// tracker, so a remote viewer renders the sleeper lying in the bed (and standing on wake). It mirrors
// syncAirSupply exactly (encodeSetEntityData over the store Entity, self send + broadcastSetEntityData
// ToTrackers), firing only on a real change via the lastPoseSent/lastSleepingPosSent dirty twins.
// Tick-owned (called from startSleepInBed / wakeUpAllPlayers on the owner goroutine).
//
//	[VERIFIED javap LivingEntity.startSleeping: setPose(SLEEPING); setSleepingPos(pos); stopSleeping:
//	 setPose(STANDING); clearSleepingPos() -- both synched values the server pushes.]
func (t *TickLoop) broadcastSleepingPose(p *tickPlayer) {
	if p == nil {
		return
	}
	pose := poseStanding
	var sleepEntry optionalBlockPosValue
	if p.sleepingPos != nil {
		pose = poseSleeping
		sleepEntry = optionalBlockPosValue{pos: *p.sleepingPos, present: true}
	}
	posChanged := (p.lastSleepingPosSent == nil) != (p.sleepingPos == nil)
	if !posChanged && p.lastSleepingPosSent != nil && p.sleepingPos != nil {
		posChanged = *p.lastSleepingPosSent != *p.sleepingPos
	}
	if int(pose) == p.lastPoseSent && !posChanged {
		return
	}
	p.lastPoseSent = int(pose)
	if p.sleepingPos != nil {
		sp := *p.sleepingPos
		p.lastSleepingPosSent = &sp
	} else {
		p.lastSleepingPosSent = nil
	}
	if p.playerEntity == nil {
		return // no store Entity yet (pre-join seam)
	}
	pkt := encodeSetEntityData(p.playerEntity,
		entityDataEntry{index: dataPoseIndex, serializerID: poseSerializerID, value: poseValue{ordinal: pose}},
		entityDataEntry{index: dataSleepingPosIndex, serializerID: optionalBlockPosSerID, value: sleepEntry},
	)
	if p.client != nil {
		p.client.Send(pkt)
	}
	t.broadcastSetEntityDataToTrackers(p, pkt)
}

// setRespawnPosition ports ServerPlayer.setRespawnPosition(RespawnConfig, notify): record the
// RespawnConfig{RespawnData{dimension, pos, yRot, xRot}, forced}. v1 records it on the tickPlayer (the
// death/respawn flow still world-spawns in ENT-05, but the bed respawn point is now authoritatively
// stored so the ENT-05 read is a drop-in). The notify chat is a cited no-op. Tick-owned.
//
//	[VERIFIED javap ServerPlayer.setRespawnPosition: this.respawnConfig = config; (notify chat).]
func (t *TickLoop) setRespawnPosition(p *tickPlayer, pos pk.Position, dimension int, yaw, pitch float32, forced bool) {
	rp := pos
	p.respawnPos = &rp
	p.respawnDimension = dimension
	p.respawnYaw = yaw
	p.respawnPitch = pitch
	p.respawnForced = forced
}

// isReachableBedBlock ports ServerPlayer.isReachableBedBlock(pos): |dx|<=3 && |dy|<=2 && |dz|<=3 from the
// bed bottom-center (Vec3.atBottomCenterOf == x+0.5, y, z+0.5), using the player feet position.
//
//	[VERIFIED javap ServerPlayer.isReachableBedBlock: Vec3 v = atBottomCenterOf(pos); Math.abs(getX()-
//	 v.x) <= 3 && Math.abs(getY()-v.y) <= 2 && Math.abs(getZ()-v.z) <= 3.]
func (t *TickLoop) isReachableBedBlock(p *tickPlayer, pos pk.Position) bool {
	vx := float64(pos.X) + 0.5
	vy := float64(pos.Y)
	vz := float64(pos.Z) + 0.5
	return absF(p.x-vx) <= 3.0 && absF(p.y-vy) <= 2.0 && absF(p.z-vz) <= 3.0
}

// bedInRange ports ServerPlayer.bedInRange(pos, facing): reachable at the head OR at the foot one step
// back along facing.getOpposite().
//
//	[VERIFIED javap ServerPlayer.bedInRange: isReachableBedBlock(pos) || isReachableBedBlock(pos.relative(
//	 facing.getOpposite())).]
func (t *TickLoop) bedInRange(p *tickPlayer, pos pk.Position, facing block.Direction) bool {
	if t.isReachableBedBlock(p, pos) {
		return true
	}
	dx, dz, ok := bedFacingDelta(facing)
	if !ok {
		return false
	}
	foot := pk.Position{X: pos.X - dx, Y: pos.Y, Z: pos.Z - dz} // relative(facing.getOpposite())
	return t.isReachableBedBlock(p, foot)
}

// bedBlocked ports ServerPlayer.bedBlocked(pos, facing): OBSTRUCTED when the block ABOVE the head is not
// free OR the block above the foot half is not free. freeAt == !isSuffocating; v1 approximates it with
// !blockSolidAt (a full solid cube suffocates), the same solidity read physics uses.
//
//	[VERIFIED javap ServerPlayer.bedBlocked: above = pos.above(); return !(freeAt(above) && freeAt(
//	 above.relative(facing.getOpposite()))).]
func (t *TickLoop) bedBlocked(pos pk.Position, facing block.Direction) bool {
	above := pk.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if t.blockSolidAt(above.X, above.Y, above.Z) {
		return true
	}
	dx, dz, ok := bedFacingDelta(facing)
	if !ok {
		return false
	}
	footAbove := pk.Position{X: above.X - dx, Y: above.Y, Z: above.Z - dz}
	return t.blockSolidAt(footAbove.X, footAbove.Y, footAbove.Z)
}

// monstersPreventingRest ports the NOT_SAFE scan: getEntitiesOfClass(Monster, AABB(center +- (8,5,8)),
// m -> m.isPreventingPlayerRest(level, this)). center == atBottomCenterOf(pos) == (x+0.5, y, z+0.5);
// Monster.isPreventingPlayerRest returns true unconditionally, so this reduces to "any Monster-category
// mob in the 8x5x8 box". categoryOf == categoryMonster is the established Enemy/Monster proxy. A CREATURE
// (the pig) is never counted.
//
//	[VERIFIED javap ServerPlayer.startSleepInBed: AABB centered at atBottomCenterOf(pos) inflated (8,5,8);
//	 getEntitiesOfClass(Monster, aabb, isPreventingPlayerRest); !isEmpty -> NOT_SAFE. Monster
//	 .isPreventingPlayerRest: return true.]
func (t *TickLoop) monstersPreventingRest(pos pk.Position) bool {
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y)
	cz := float64(pos.Z) + 0.5
	minX, maxX := cx-8.0, cx+8.0
	minY, maxY := cy-5.0, cy+5.0
	minZ, maxZ := cz-8.0, cz+8.0
	if t.cur() == nil || t.cur().entities == nil {
		return false
	}
	for _, e := range t.cur().entities.near(cx, cz, 2) {
		if e == nil || e.dead || e.health <= 0 {
			continue
		}
		if categoryOf(e.typ) != categoryMonster { // getEntitiesOfClass(Monster, ...) proxy
			continue
		}
		hw := e.width / 2
		if !(minX < e.x+hw && maxX > e.x-hw && minY < e.y+e.height && maxY > e.y && minZ < e.z+hw && maxZ > e.z-hw) {
			continue
		}
		return true // isPreventingPlayerRest == true for every Monster
	}
	return false
}

// startSleepInBed ports ServerPlayer.startSleepInBed(BlockPos) -- the full gate chain, then
// super.startSleepInBed (player_sleep.go). It REPLACES the old bare TickLoop.startSleepInBed so useBed
// drives the real ServerPlayer gates. The caller passes the resolved bed HEAD pos + its facing. Returns
// the BedSleepingProblem (bedProblemNone on a started sleep); on success it broadcasts the sleeping pose.
//
//	[VERIFIED javap ServerPlayer.startSleepInBed: facing = state.getValue(FACING); if not-sleeping and
//	 alive else OTHER_PROBLEM; rule = BED_RULE; canSleep=rule.canSleep; canSetSpawn=rule.canSetSpawn;
//	 if !canSetSpawn and !canSleep -> asProblem; if !bedInRange -> TOO_FAR_AWAY; if bedBlocked ->
//	 OBSTRUCTED; if canSetSpawn -> setRespawnPosition; if !canSleep -> asProblem; if !isCreative ->
//	 monster AABB scan -> NOT_SAFE; super.startSleepInBed(pos).]
func (t *TickLoop) startSleepInBed(p *tickPlayer, pos pk.Position, facing block.Direction) bedSleepingProblem {
	if p.isSleeping() || p.dead { // if not-sleeping and alive else OTHER_PROBLEM
		return bedProblemOther
	}
	// BedRule default CAN_SLEEP_WHEN_DARK: canSleep == WHEN_DARK.test == isDarkOutside (v1 proxy
	// bedRuleCanSleep), canSetSpawn == ALWAYS.test == true.
	canSleep := t.bedRuleCanSleep()
	canSetSpawn := true
	if !canSetSpawn && !canSleep { // rule.asProblem (NOT_POSSIBLE_NOW default)
		return bedProblemNotPossible
	}
	if !t.bedInRange(p, pos, facing) { // TOO_FAR_AWAY
		return bedProblemTooFar
	}
	if t.bedBlocked(pos, facing) { // OBSTRUCTED
		return bedProblemObstructed
	}
	if canSetSpawn { // setRespawnPosition: the sleeping-in-a-valid-bed sets-your-respawn-point observable
		t.setRespawnPosition(p, pos, p.dimension, p.yaw, p.pitch, false)
	}
	if !canSleep { // rule.asProblem AFTER the respawn set (daytime rejected, spawn still updated)
		return bedProblemNotPossible
	}
	if t.monstersPreventingRest(pos) { // !isCreative survival path -> NOT_SAFE
		return bedProblemNotSafe
	}
	// super.startSleepInBed(pos): startSleeping; sleepCounter = 0 (player_sleep.go); broadcast pose.
	t.startSleeping(p, pos)
	p.sleepCounter = 0
	t.broadcastSleepingPose(p)
	t.updateSleepingPlayerList()
	// ADVANCEMENTS (advancements.go): minecraft:slept_in_bed — the ServerPlayer.startSleepInBed success
	// path awards Stats.SLEEP_IN_BED then fires CriteriaTriggers.SLEPT_IN_BED.trigger(this). slept_in_bed
	// is a bare PlayerTrigger (empty predicate), so this unconditionally grants adventure/sleep_in_bed.
	// Reached only on the success return (all gates passed), matching the vanilla ordering.
	//	[VERIFIED javap ServerPlayer.startSleepInBed success: awardStat(Stats.SLEEP_IN_BED);
	//	 CriteriaTriggers.SLEPT_IN_BED.trigger(this).]
	t.triggerSleptInBed(p)
	return bedProblemNone
}

// sleepStatusCounts returns (activePlayers, sleepingPlayers) as SleepStatus.update tallies them:
// activePlayers = non-spectator players, sleepingPlayers = the isSleeping subset. v1 has no spectator
// mode, so every player is active (isSpectator is constant-false).
//
//	[VERIFIED javap SleepStatus.update: per player, if !isSpectator ++activePlayers, then if isSleeping
//	 ++sleepingPlayers.]
func (t *TickLoop) sleepStatusCounts() (active, sleeping int) {
	for _, p := range t.players {
		if p == nil {
			continue
		}
		active++ // !isSpectator (constant true in v1)
		if p.isSleeping() {
			sleeping++
		}
	}
	return active, sleeping
}

// sleepersNeeded ports SleepStatus.sleepersNeeded(pct): max(1, Mth.ceil(activePlayers * pct / 100.0f)).
//
//	[VERIFIED javap SleepStatus.sleepersNeeded: Math.max(1, Mth.ceil(activePlayers * pct / 100.0f)).]
func sleepersNeeded(active, pct int) int {
	need := mthCeil(float64(active*pct) / 100.0)
	if need < 1 {
		return 1
	}
	return need
}

// areEnoughSleeping ports SleepStatus.areEnoughSleeping(pct): sleepingPlayers >= sleepersNeeded(pct).
//
//	[VERIFIED javap SleepStatus.areEnoughSleeping: sleepingPlayers >= sleepersNeeded(pct).]
func (t *TickLoop) areEnoughSleeping(pct int) bool {
	active, sleeping := t.sleepStatusCounts()
	return sleeping >= sleepersNeeded(active, pct)
}

// areEnoughDeepSleeping ports SleepStatus.areEnoughDeepSleeping(pct, players): count of players
// isSleepingLongEnough (sleepCounter >= SLEEP_DURATION 100) >= sleepersNeeded(pct) -- the SECOND gate the
// night skip requires (asleep ~5s, not just entered the bed).
//
//	[VERIFIED javap SleepStatus.areEnoughDeepSleeping: (int) players.stream().filter(ServerPlayer::
//	 isSleepingLongEnough).count() >= sleepersNeeded(pct). Player.isSleepingLongEnough: isSleeping() and
//	 sleepCounter >= 100.]
func (t *TickLoop) areEnoughDeepSleeping(pct int) bool {
	active, _ := t.sleepStatusCounts()
	deep := 0
	for _, p := range t.players {
		if p == nil {
			continue
		}
		if p.isSleeping() && p.sleepCounter >= sleepDuration {
			deep++
		}
	}
	return deep >= sleepersNeeded(active, pct)
}

// updateSleepingPlayerList ports ServerLevel.updateSleepingPlayerList(): recompute + announce the sleep
// status. v1 derives the counts live in areEnoughSleeping, so the announce is a cited no-op (no sleep-vote
// chat/boss-bar subsystem). Kept as the seam startSleepInBed / stopSleepInBed call.
//
//	[VERIFIED javap ServerLevel.updateSleepingPlayerList: if !players.isEmpty and sleepStatus.update(
//	 players) announceSleepStatus().]
func (t *TickLoop) updateSleepingPlayerList() {}

// wakeUpAllPlayers ports ServerLevel.wakeUpAllPlayers(): removeAllSleepers, then wake every sleeping
// player via stopSleepInBed(false, false) (sleepCounter=100 waking unwind), clearing OCCUPIED +
// broadcasting the standing pose.
//
//	[VERIFIED javap ServerLevel.wakeUpAllPlayers: sleepStatus.removeAllSleepers(); players.stream()
//	 .filter(ServerPlayer::isSleeping).forEach(p -> p.stopSleepInBed(false, false)).]
func (t *TickLoop) wakeUpAllPlayers() {
	for _, p := range t.players {
		if p == nil || !p.isSleeping() {
			continue
		}
		t.stopSleepInBed(p, false)
		t.broadcastSleepingPose(p)
	}
}

// dawnTimeAfterSleep ports the classic ServerLevel sleep time-skip (the pre-clock-manager setDayTime the
// 26.2 WAKE_UP_FROM_SLEEP marker computes to): round the day-time UP to the next morning (day-time 0 ==
// dawn). newTime = (dt + 24000) - (dt + 24000) mod 24000. Sulfur has no separate dayTime clock (the
// gametime monotonic counter IS the day-phase signal, spawner.go), so the skip lands gametime mod 24000
// == 0 (dawn, isDarkEnoughToSpawn false) -- the observable wake-to-morning.
//
//	[VERIFIED (classic) ServerLevel sleep: long l = getDayTime() + 24000L; setDayTime(l - l % 24000L).
//	 26.2 abstracts this behind ClockTimeMarkers.WAKE_UP_FROM_SLEEP -> the same next-dawn observable.]
func dawnTimeAfterSleep(gametime int64) int64 {
	l := gametime + dayLengthTicks
	return l - l%dayLengthTicks
}

// tickSleep ports the sleep block of ServerLevel.tick: read players_sleeping_percentage; if
// areEnoughSleeping and areEnoughDeepSleeping then (ADVANCE_TIME) skip to the next dawn, wakeUpAllPlayers,
// and (ADVANCE_WEATHER and isRaining) resetWeatherCycle. Runs on the COORDINATOR once per tick (like
// tickWeather) -- sleep is world-global. A world with no sleeping player is a cheap early-out. Draws NO
// RNG.
//
//	[VERIFIED javap ServerLevel.tick: int pct = getGameRules().get(PLAYERS_SLEEPING_PERCENTAGE); if
//	 sleepStatus.areEnoughSleeping(pct) and areEnoughDeepSleeping(pct, players) { if get(ADVANCE_TIME)
//	 and defaultClock.isPresent() clockManager.moveToTimeMarker(..., WAKE_UP_FROM_SLEEP);
//	 wakeUpAllPlayers(); if get(ADVANCE_WEATHER) and isRaining() resetWeatherCycle(); }.]
func (t *TickLoop) tickSleep() {
	t.trace("tickSleep")
	pct := t.gameRuleInt(ruleSleepPercent)
	if !t.areEnoughSleeping(pct) || !t.areEnoughDeepSleeping(pct) {
		return
	}
	if t.gameRule(ruleAdvanceTime) { // ADVANCE_TIME: skip the day-time to the next dawn.
		t.gametime = dawnTimeAfterSleep(t.gametime)
	}
	t.wakeUpAllPlayers()
	if t.gameRule(ruleAdvanceWeather) && t.isRaining() { // clear the storm on waking.
		t.resetWeatherCycle()
	}
}

// absF is Math.abs(double) for the bed-range checks (one call; avoids the math import).
func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
