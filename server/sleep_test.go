package server

// sleep_test.go -- SLEEP-01 (respawn + night skip): the ServerPlayer.startSleepInBed gate chain (respawn
// set, TOO_FAR_AWAY, day rejection, NOT_SAFE monster scan) and the ServerLevel.tick all-players-asleep
// night-skip + weather-clear. Behavior verified against temp/cache/26.2-inner.jar this session.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// redBedFacing is the FACING of redBedHeadState (north) -- the facing startSleepInBed reads.
func redBedFacing() block.Direction { return block.North }

// sleepLoop wires a loop with an all-air ready chunk at (0,0) and a survival player at (ownerX,Y,Z). The
// gametime seeds night (15000, isDarkEnoughToSpawn true) so the default is a sleepable world.
func sleepLoop(t *testing.T, ownerX, ownerY, ownerZ float64) (*TickLoop, *world.ChunkManager, *tickPlayer) {
	t.Helper()
	loop, mgr := newFluidLoop()
	loop.gametime = 15000 // night: BedRule.canSleep (isDarkEnoughToSpawn) true
	p := &tickPlayer{entityID: 7, x: ownerX, y: ownerY, z: ownerZ, health: maxHealth, lastPoseSent: -1}
	loop.players = append(loop.players, p)
	return loop, mgr, p
}

// TestSleepAtNightSetsRespawnAndPose: an awake, alive player clicking a valid bed at night sleeps -- the
// respawn point is set to the bed, the sleeping pose (sleepingPos) is recorded, and OCCUPIED is set.
func TestSleepAtNightSetsRespawnAndPose(t *testing.T) {
	loop, mgr, p := sleepLoop(t, 9.5, 64, 8.5)
	bedPos := pk.Position{X: 9, Y: 64, Z: 8}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)

	prob := loop.startSleepInBed(p, bedPos, redBedFacing())
	if prob != bedProblemNone {
		t.Fatalf("startSleepInBed at night in a valid bed = %d, want bedProblemNone", prob)
	}
	if !p.isSleeping() {
		t.Fatal("player not sleeping after a successful startSleepInBed")
	}
	if p.respawnPos == nil || *p.respawnPos != bedPos {
		t.Fatalf("respawnPos = %v, want %v (canSetSpawn -> setRespawnPosition)", p.respawnPos, bedPos)
	}
	if p.sleepCounter != 0 {
		t.Fatalf("sleepCounter = %d after startSleepInBed, want 0", p.sleepCounter)
	}
	s, _ := mgr.GetBlock(bedPos, dimMinY)
	if bp, ok := readBed(s); !ok || !bp.occupied {
		t.Fatalf("bed OCCUPIED not set after sleep (state %d)", s)
	}
}

// TestSleepDuringDayRejected: clicking a valid bed during the DAY is rejected with NOT_POSSIBLE (BedRule
// canSleep WHEN_DARK false) -- but the respawn point IS still set (canSetSpawn set runs BEFORE the
// canSleep return in ServerPlayer.startSleepInBed).
func TestSleepDuringDayRejected(t *testing.T) {
	loop, mgr, p := sleepLoop(t, 9.5, 64, 8.5)
	loop.gametime = 1000 // day: isDarkEnoughToSpawn false
	bedPos := pk.Position{X: 9, Y: 64, Z: 8}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)

	prob := loop.startSleepInBed(p, bedPos, redBedFacing())
	if prob != bedProblemNotPossible {
		t.Fatalf("day sleep = %d, want bedProblemNotPossible", prob)
	}
	if p.isSleeping() {
		t.Fatal("player sleeping after a DAY click -- sleep must be rejected")
	}
	if p.respawnPos == nil || *p.respawnPos != bedPos {
		t.Fatalf("respawnPos = %v, want %v (spawn set runs before the day rejection)", p.respawnPos, bedPos)
	}
}

// TestSleepTooFarRejected: a bed more than 3/2/3 blocks from the player feet is TOO_FAR_AWAY.
func TestSleepTooFarRejected(t *testing.T) {
	loop, mgr, p := sleepLoop(t, 0.5, 64, 0.5)
	bedPos := pk.Position{X: 12, Y: 64, Z: 12} // far (dx=11.5 > 3)
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)

	prob := loop.startSleepInBed(p, bedPos, redBedFacing())
	if prob != bedProblemTooFar {
		t.Fatalf("far bed = %d, want bedProblemTooFar", prob)
	}
	if p.isSleeping() {
		t.Fatal("player sleeping in a too-far bed -- must be rejected")
	}
}

// TestSleepMonsterNearbyNotSafe: a Monster-category mob within the 8x5x8 box makes the sleep NOT_SAFE.
func TestSleepMonsterNearbyNotSafe(t *testing.T) {
	loop, mgr, p := sleepLoop(t, 9.5, 64, 8.5)
	bedPos := pk.Position{X: 9, Y: 64, Z: 8}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)

	z := NewEntity(loop.idAlloc.AllocID(), entity.Zombie, float64(bedPos.X)+3.0, float64(bedPos.Y), float64(bedPos.Z))
	z.health = 20
	loop.only().entities.add(z)

	prob := loop.startSleepInBed(p, bedPos, redBedFacing())
	if prob != bedProblemNotSafe {
		t.Fatalf("sleep with a monster nearby = %d, want bedProblemNotSafe", prob)
	}
	if p.isSleeping() {
		t.Fatal("player sleeping with a monster nearby -- must be NOT_SAFE")
	}
}

// TestNightSkipToDawnAndClearWeather: with the only player deep-asleep and the sleep percentage met,
// tickSleep skips the day-time to the next dawn (gametime becomes 24000), wakes the player, clears rain.
func TestNightSkipToDawnAndClearWeather(t *testing.T) {
	loop, mgr, p := sleepLoop(t, 9.5, 64, 8.5)
	loop.gametime = 15000
	bedPos := pk.Position{X: 9, Y: 64, Z: 8}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)

	loop.startSleeping(p, bedPos)
	p.sleepCounter = sleepDuration // deep-asleep (areEnoughDeepSleeping needs >= SLEEP_DURATION)

	loop.weather.raining = true
	loop.weather.rainLevel = 1.0
	if !loop.isRaining() {
		t.Fatal("precondition: world must be raining before the night skip")
	}

	loop.tickSleep()

	if loop.gametime != 24000 {
		t.Fatalf("gametime = %d after the night skip, want 24000 (next dawn)", loop.gametime)
	}
	if loop.gametime%dayLengthTicks != 0 {
		t.Fatalf("gametime mod 24000 = %d, want 0 (dawn)", loop.gametime%dayLengthTicks)
	}
	if p.isSleeping() {
		t.Fatal("player still sleeping after the night skip -- wakeUpAllPlayers must wake it")
	}
	if loop.weather.raining {
		t.Fatal("weather still raining after the night skip -- resetWeatherCycle must clear it")
	}
}

// TestNightSkipRequiresDeepSleep: a player who just entered the bed (sleepCounter 0) does NOT trigger the
// night skip -- areEnoughDeepSleeping is false until sleepCounter >= 100.
func TestNightSkipRequiresDeepSleep(t *testing.T) {
	loop, mgr, p := sleepLoop(t, 9.5, 64, 8.5)
	loop.gametime = 15000
	bedPos := pk.Position{X: 9, Y: 64, Z: 8}
	mgr.SetBlock(bedPos, redBedHeadState(t), dimMinY)
	loop.startSleeping(p, bedPos)
	p.sleepCounter = 50 // asleep but not long enough

	loop.tickSleep()

	if loop.gametime != 15000 {
		t.Fatalf("gametime = %d, want 15000 (no skip: not deep-sleeping)", loop.gametime)
	}
	if !p.isSleeping() {
		t.Fatal("player woken without a completed night skip -- must stay asleep")
	}
}

// TestBedInRangeFootHalf: a player next to the FOOT half of a two-block bed is in range even when the
// HEAD is one block further (bedInRange checks the facing-opposite step).
func TestBedInRangeFootHalf(t *testing.T) {
	loop, _ := newFluidLoop()
	p := &tickPlayer{x: 9.5, y: 64, z: 9.4}
	head := pk.Position{X: 9, Y: 64, Z: 8} // north-facing head; foot one step south at z=9
	if !loop.bedInRange(p, head, redBedFacing()) {
		t.Fatal("bedInRange false for a player at the FOOT half, want true (facing-opposite fallback)")
	}
}
