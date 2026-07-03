package server

// weather_test.go — port gates for the WORLD-GLOBAL rain/thunder cycle (weather.go /
// ServerLevel.advanceWeatherCycle). These are PORT-EXACT gates, not feel checks: the rolled durations
// are asserted against an INDEPENDENT LegacyRandomSource reference (same seed, same draw order) so a
// wrong UniformInt range or a swapped thunder/rain draw order fails here; the ramp/flip/broadcast and
// isRainingAt gate are asserted directly against the jar-derived behavior.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// scanGameEvent decodes a ClientboundGameEvent packet into (event id, param).
func scanGameEvent(t *testing.T, p pk.Packet) (int, float32) {
	t.Helper()
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundGameEvent {
		t.Fatalf("packet id = %d, want ClientboundGameEvent", p.ID)
	}
	var (
		event pk.UnsignedByte
		param pk.Float
	)
	if err := p.Scan(&event, &param); err != nil {
		t.Fatalf("GameEvent scan failed: %v", err)
	}
	return int(event), float32(param)
}

// TestWeatherFirstTickRollsDelaysFromJarRanges: a fresh clear world (all timers 0, flags false) on its
// FIRST tickWeather rolls the initial thunder-delay THEN rain-delay off levelRandom, in that exact draw
// order, from the jar UniformInt ranges (THUNDER_DELAY/RAIN_DELAY == UniformInt.of(12000, 180000) ->
// nextInt(168001)+12000). Asserted against an independent reference random with the same seed.
func TestWeatherFirstTickRollsDelaysFromJarRanges(t *testing.T) {
	const seed = 0x5EED
	loop := NewTickLoop(newFakeClock())
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(seed)

	// Independent reference: the jar draws THUNDER first, then RAIN (advanceWeatherCycle bytecode order).
	// UniformInt.of(12000, 180000).sample == nextInt(180000-12000+1)+12000 == nextInt(168001)+12000.
	ref := levelgen.NewLegacyRandomSource(seed)
	wantThunder := ref.NextIntN(168001) + 12000
	wantRain := ref.NextIntN(168001) + 12000

	loop.tickWeather()

	if loop.weather.thunderTime != wantThunder {
		t.Fatalf("thunderTime = %d, want %d (THUNDER_DELAY nextInt(168001)+12000, drawn FIRST)", loop.weather.thunderTime, wantThunder)
	}
	if loop.weather.rainTime != wantRain {
		t.Fatalf("rainTime = %d, want %d (RAIN_DELAY nextInt(168001)+12000, drawn SECOND)", loop.weather.rainTime, wantRain)
	}
	// Both delays land in [12000, 180000].
	if loop.weather.thunderTime < 12000 || loop.weather.thunderTime > 180000 {
		t.Fatalf("thunderTime %d outside jar range [12000,180000]", loop.weather.thunderTime)
	}
	if loop.weather.rainTime < 12000 || loop.weather.rainTime > 180000 {
		t.Fatalf("rainTime %d outside jar range [12000,180000]", loop.weather.rainTime)
	}
	// A fresh world is not raining/thundering after the first cycle (both delays counting down to a flip).
	if loop.weather.raining || loop.weather.thundering {
		t.Fatalf("fresh world should stay clear after first tick: raining=%v thundering=%v", loop.weather.raining, loop.weather.thundering)
	}
}

// TestWeatherRainDurationRangeOnFlip: when the rain DELAY counts down to a raining flip and then rain
// begins, the next tick that consumes rainTime==0 while raining rolls RAIN_DURATION ==
// UniformInt.of(12000, 24000) -> nextInt(12001)+12000. This drives the timer to a flip and asserts the
// duration range that follows.
func TestWeatherRainDurationRangeOnFlip(t *testing.T) {
	const seed = 0xC0FFEE
	loop := NewTickLoop(newFakeClock())
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(seed)

	// Arm the world just before a rain flip: rainTime==1 (next tick decrements to 0 -> raining flips true),
	// thunderTime high so the thunder branch just decrements (no thunder draw this tick), clear==0.
	loop.weather.rainTime = 1
	loop.weather.thunderTime = 100000
	loop.weather.raining = false
	loop.weather.thundering = false

	// Reference: this tick draws NOTHING for thunder (thunderTime>0 just decrements) and NOTHING for rain
	// (rainTime>0 -> decrement to 0 -> flip, no sample). So the levelRandom is UNTOUCHED this tick.
	loop.tickWeather()
	if !loop.weather.raining {
		t.Fatalf("rainTime 1->0 must flip raining true; got raining=%v", loop.weather.raining)
	}

	// Next tick: rainTime is now 0 AND raining, so the rain branch rolls RAIN_DURATION. thunderTime still
	// high (just decrements). Reference draws exactly one RAIN_DURATION sample (nextInt(12001)+12000).
	ref := levelgen.NewLegacyRandomSource(seed)
	wantDuration := ref.NextIntN(12001) + 12000

	loop.tickWeather()
	if loop.weather.rainTime != wantDuration {
		t.Fatalf("rainTime = %d, want %d (RAIN_DURATION nextInt(12001)+12000)", loop.weather.rainTime, wantDuration)
	}
	if loop.weather.rainTime < 12000 || loop.weather.rainTime > 24000 {
		t.Fatalf("rainTime %d outside RAIN_DURATION range [12000,24000]", loop.weather.rainTime)
	}
}

// TestWeatherRainLevelRampAndBroadcast: while raining the rainLevel ramps up by +0.01 each tick (clamped
// [0,1]) and each change broadcasts a RAIN_LEVEL_CHANGE game event carrying the new level to every
// player. Also asserts isRaining() flips true once the ramp climbs past 0.2.
func TestWeatherRainLevelRampAndBroadcast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(1)

	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)

	// Force the raining flag on with high timers so the cycle just ramps (no flips/draws) each tick.
	loop.weather.raining = true
	loop.weather.rainTime = 100000
	loop.weather.thunderTime = 100000

	// One tick: rainLevel 0.0 -> 0.01, so oRainLevel(0) != rainLevel(0.01) -> one RAIN_LEVEL_CHANGE(0.01).
	loop.tickWeather()
	if loop.weather.rainLevel != 0.01 {
		t.Fatalf("rainLevel after 1 tick = %v, want 0.01 (+0.01 ramp)", loop.weather.rainLevel)
	}

	packets := drainPackets(p.client)
	var lastRainLevel float32 = -1
	rainLevelChanges := 0
	for _, pkt := range packets {
		if packetid.ClientboundPacketID(pkt.ID) != packetid.ClientboundGameEvent {
			continue
		}
		id, param := scanGameEvent(t, pkt)
		if id == gameEventRainLevelChange {
			rainLevelChanges++
			lastRainLevel = param
		}
	}
	if rainLevelChanges != 1 {
		t.Fatalf("RAIN_LEVEL_CHANGE broadcasts = %d, want 1 on the first ramp tick", rainLevelChanges)
	}
	if lastRainLevel != 0.01 {
		t.Fatalf("RAIN_LEVEL_CHANGE param = %v, want 0.01 (the new rainLevel)", lastRainLevel)
	}

	// Ramp until isRaining() (getRainLevel(1)>0.2). It starts at 0.01; ~20 more ticks push it past 0.2.
	for i := 0; i < 25; i++ {
		loop.tickWeather()
	}
	if !loop.isRaining() {
		t.Fatalf("after ramping ~26 ticks rainLevel=%v, isRaining() should be true (>0.2)", loop.weather.rainLevel)
	}
}

// TestWeatherRainingFlipBroadcastsStartRaining: crossing the isRaining() threshold (rainLevel ramps past
// 0.2) fires a START_RAINING game event to all players (the wasRaining != isRaining() diff branch of
// advanceWeatherCycle).
func TestWeatherRainingFlipBroadcastsStartRaining(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(1)
	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)

	loop.weather.raining = true
	loop.weather.rainTime = 100000
	loop.weather.thunderTime = 100000
	// Pre-ramp rainLevel to just below the 0.2 threshold so the NEXT tick crosses it. 0.195 is safely
	// below 0.2 (float32 rounding aside), and +0.01 -> ~0.205 which is safely above.
	loop.weather.rainLevel = 0.195
	loop.weather.oRainLevel = 0.195
	if loop.isRaining() {
		t.Fatal("precondition: rainLevel 0.195 must be below the 0.2 isRaining threshold")
	}

	// This tick: rainLevel 0.195 -> 0.205, isRaining() goes false->true -> START_RAINING broadcast.
	loop.tickWeather()
	if !loop.isRaining() {
		t.Fatalf("after ramp rainLevel=%v, isRaining() should be true", loop.weather.rainLevel)
	}

	packets := drainPackets(p.client)
	startRaining := 0
	for _, pkt := range packets {
		if packetid.ClientboundPacketID(pkt.ID) != packetid.ClientboundGameEvent {
			continue
		}
		if id, _ := scanGameEvent(t, pkt); id == gameEventStartRaining {
			startRaining++
		}
	}
	if startRaining != 1 {
		t.Fatalf("START_RAINING broadcasts = %d, want 1 when isRaining() flips true", startRaining)
	}
}

// TestIsRainingAtOpenSkyWhileRaining: with the weather cycle raining, a block at/above the open-sky
// surface returns true from isRainingAt (precipitationAt clears the isRaining + canSeeSky + heightmap
// gates -> overworld RAIN); a block far below the surface (shaded/under the top) returns false.
func TestIsRainingAtOpenSkyWhileRaining(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.regions[globalRegion].world = mgr
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(1)
	loop.spawnSurfaceY = 64 // open sky at/above y=64 (canSeeSkyAt)

	// Make it rain: force rainLevel above the 0.2 threshold so isRaining() is true.
	loop.weather.raining = true
	loop.weather.rainLevel = 0.5
	loop.weather.oRainLevel = 0.5
	if !loop.isRaining() {
		t.Fatal("precondition: isRaining() must be true")
	}

	// An all-air column (no solid blocks) has ghastMotionBlockingTop == dimMinY, so firstAvailableY ==
	// dimMinY+1, which is <= any surface y -> the heightmap gate passes. A cell at the open-sky surface
	// therefore rains.
	surface := pk.Position{X: 8, Y: 70, Z: 8}
	if !loop.isRainingAt(surface) {
		t.Fatalf("isRainingAt(open-sky surface %v) = false, want true while raining", surface)
	}

	// A cell BELOW the spawn surface is not sky-exposed (canSeeSkyAt false) -> no rain.
	shaded := pk.Position{X: 8, Y: 32, Z: 8}
	if loop.isRainingAt(shaded) {
		t.Fatalf("isRainingAt(shaded %v) = true, want false (below open sky)", shaded)
	}
}

// TestIsRainingAtFalseWhenClear: with no rain (rainLevel 0), isRainingAt is false everywhere, so the
// farmland dry-out path never spuriously hydrates from a clear sky.
func TestIsRainingAtFalseWhenClear(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.regions[globalRegion].world = mgr
	loop.spawnSurfaceY = 64

	if loop.isRaining() {
		t.Fatal("precondition: a fresh clear world must not be raining")
	}
	if loop.isRainingAt(pk.Position{X: 8, Y: 70, Z: 8}) {
		t.Fatal("isRainingAt must be false when the world is clear")
	}
}

// TestIsThunderingThreshold: isThundering() is true only once thunderLevel*rainLevel (getThunderLevel)
// climbs past 0.9 — the jar Level.isThundering gate (thunder is gated by rain).
func TestIsThunderingThreshold(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Full rain + full thunder -> getThunderLevel(1) == 1.0*1.0 == 1.0 > 0.9 -> thundering.
	loop.weather.rainLevel = 1.0
	loop.weather.oRainLevel = 1.0
	loop.weather.thunderLevel = 1.0
	loop.weather.oThunderLevel = 1.0
	if !loop.isThundering() {
		t.Fatalf("full rain+thunder: isThundering() = false, want true (getThunderLevel=%v)", loop.getThunderLevel(1.0))
	}

	// Full thunder but NO rain -> getThunderLevel == thunder*rain == 1.0*0.0 == 0.0 -> not thundering.
	loop.weather.rainLevel = 0.0
	loop.weather.oRainLevel = 0.0
	if loop.isThundering() {
		t.Fatal("thunder without rain: isThundering() must be false (thunder gated by rain)")
	}
}
