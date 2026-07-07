package server

// weather.go — the WORLD-GLOBAL rain/thunder cycle, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.server.level.ServerLevel.advanceWeatherCycle, the weather getters on
// net.minecraft.world.level.Level, and net.minecraft.world.level.saveddata.WeatherData). Read from
// the jar bytecode via javap -c -p this session.
//
// Weather is a WORLD-GLOBAL phase: the timers (clearWeatherTime/rainTime/thunderTime + the raining/
// thundering flags) live in vanilla on the WeatherData SavedData of the ServerLevel, and the
// rainLevel/thunderLevel ramp fields live on the ServerLevel itself. Sulfur's single world is the
// coordinator's globalRegion, so this whole struct is stored ONCE on the TickLoop (t.weather) and
// tickWeather runs on the coordinator BEFORE the region fan-out (like tickWorld) — single-threaded,
// no cross-region state. The RNG draws use the globalRegion's per-region levelRandom, which is the
// ServerLevel `this.random` analogue for the global world.
//
// PIG-ORACLE SAFETY: tickWeather is called ONLY from tickOnce (the full server tick). The pig oracle
// (TestPluginPigEqualsGoNativePig) drives serverAiStep directly, never tickOnce, so it never advances
// the weather cycle — and even if it did, weather draws off globalRegion.levelRandom while the pig's
// AI draws off its own reseeded per-entity stream, so the two streams never cross.

// Weather GameEvent type ids — the ClientboundGameEventPacket.Type constructor ints (jar-verified from
// ClientboundGameEventPacket static init: START_RAINING iconst_1, STOP_RAINING iconst_2,
// RAIN_LEVEL_CHANGE bipush 7, THUNDER_LEVEL_CHANGE bipush 8). CHANGE_GAME_MODE (3) is in
// commands_vanilla.go; LEVEL_CHUNKS_LOAD_START (13) is in play_join.go.
//
//	[VERIFIED javap net.minecraft.network.protocol.game.ClientboundGameEventPacket static init:
//	 START_RAINING==1, STOP_RAINING==2, RAIN_LEVEL_CHANGE==7, THUNDER_LEVEL_CHANGE==8.]
const (
	gameEventStartRaining       = 1
	gameEventStopRaining        = 2
	gameEventRainLevelChange    = 7
	gameEventThunderLevelChange = 8
)

// The UniformInt weather-duration providers, jar-verified from ServerLevel static init:
// UniformInt.of(min, max).sample(random) == Mth.randomBetweenInclusive(random, min, max) ==
// random.nextInt(max - min + 1) + min. So each provider's effective (bound, offset) draw is stored as
// the exact int arguments the jar UniformInt was constructed with (min, max), and sample() below turns
// them into the identical nextInt(max-min+1)+min.
//
//	[VERIFIED javap ServerLevel.<clinit>:
//	 RAIN_DELAY      = UniformInt.of(12000, 180000)   -> nextInt(168001)+12000
//	 RAIN_DURATION   = UniformInt.of(12000, 24000)    -> nextInt(12001)+12000
//	 THUNDER_DELAY   = UniformInt.of(12000, 180000)   -> nextInt(168001)+12000
//	 THUNDER_DURATION= UniformInt.of(3600, 15600)     -> nextInt(12001)+3600
//	 Mth.randomBetweenInclusive(r,min,max)=r.nextInt(max-min+1)+min; UniformInt.sample delegates to it.]
var (
	weatherRainDelay       = uniformInt{12000, 180000}
	weatherRainDuration    = uniformInt{12000, 24000}
	weatherThunderDelay    = uniformInt{12000, 180000}
	weatherThunderDuration = uniformInt{3600, 15600}
)

// uniformInt is the net.minecraft.util.valueproviders.UniformInt port used by the weather cycle. Only
// sample() is needed here (the durations are constructed statically above).
type uniformInt struct {
	minInclusive int32
	maxInclusive int32
}

// sample ports UniformInt.sample -> Mth.randomBetweenInclusive(random, min, max) ->
// random.nextInt(max - min + 1) + min. It advances r's stream by exactly one nextInt draw, in the
// SAME order the jar draws it (the draw order inside advanceWeatherCycle is the load-bearing contract).
//
//	[VERIFIED javap UniformInt.sample: Mth.randomBetweenInclusive(r, minInclusive, maxInclusive);
//	 Mth.randomBetweenInclusive: r.nextInt(max - min + 1) + min.]
func (u uniformInt) sample(r interface{ NextIntN(int32) int32 }) int32 {
	return r.NextIntN(u.maxInclusive-u.minInclusive+1) + u.minInclusive
}

// weatherState is the WORLD-GLOBAL weather cycle state. The timers + flags mirror WeatherData's fields
// (all default 0/false — the vanilla WeatherData() no-arg ctor zeroes every field, so a fresh world
// starts clear with clearWeatherTime==0, which advanceWeatherCycle immediately re-rolls into the first
// rain/thunder delay). rainLevel/thunderLevel + their o* previous-frame twins are the ServerLevel ramp
// fields (Mth.lerp interpolation source for getRainLevel/getThunderLevel).
//
//	[VERIFIED javap WeatherData(): default ctor sets no fields -> clearWeatherTime=0, rainTime=0,
//	 thunderTime=0, raining=false, thundering=false. ServerLevel rainLevel/thunderLevel/oRainLevel/
//	 oThunderLevel default 0.0f.]
type weatherState struct {
	clearWeatherTime int32
	rainTime         int32
	thunderTime      int32
	raining          bool
	thundering       bool

	rainLevel     float32
	oRainLevel    float32
	thunderLevel  float32
	oThunderLevel float32
}

// canHaveWeather is Level.canHaveWeather: dimensionType.hasSkyLight && !dimensionType.hasCeiling &&
// dimension != END. Sulfur's single world is the overworld (skylight, no ceiling, not the End), so this
// is a cited constant true — every v1 world can have weather. Becomes a real dimension read when
// multiple dimensions (nether/end) are wired.
//
//	[VERIFIED javap Level.canHaveWeather: hasSkyLight && !hasCeiling && dimension != END. v1 overworld
//	 satisfies all three. DEFERRED: real dimensionType read pending nether/end.]
func (t *TickLoop) canHaveWeather() bool { return true }

// advanceWeatherCycleGameRule is the GameRules.get(ADVANCE_WEATHER) read (ex-doWeatherCycle). It routes
// through the real GameRules store (gamerules.go), so /gamerule advance_weather false freezes the whole
// cycle (timers stop counting down, no re-rolls, no flag flips) exactly as vanilla -- the ramp + broadcast
// tail still runs so a mid-storm freeze keeps draining the level toward its current flag target.
//
//	[VERIFIED javap ServerLevel.advanceWeatherCycle: the countdown/reroll block is guarded by
//	 getGameRules().getBoolean(GameRules.ADVANCE_WEATHER); default true (registerBoolean, 26.2 id
//	 "advance_weather").]
func (t *TickLoop) advanceWeatherCycleGameRule() bool { return t.gameRule(ruleAdvanceWeather) }

// tickWeather is the 1:1 port of ServerLevel.advanceWeatherCycle. It runs on the COORDINATOR, once per
// tick, BEFORE the region fan-out (like tickWorld) — weather is world-global, so it must run exactly
// once outside the parallel section. It:
//
//  1. counts down clearWeatherTime, else thunderTime/rainTime, flipping the flags + re-rolling the next
//     duration off globalRegion.levelRandom (the ServerLevel this.random analogue) via the UniformInt
//     providers — in the EXACT draw order the jar uses (thunder branch then rain branch);
//
//  2. ramps thunderLevel then rainLevel toward 0 or 1 by ±0.01 each tick, clamped to [0,1];
//
//  3. broadcasts RAIN_LEVEL_CHANGE / THUNDER_LEVEL_CHANGE on a level change, and START_RAINING /
//     STOP_RAINING (+ a fresh RAIN/THUNDER_LEVEL_CHANGE pair) on a raining flip.
//
//     [VERIFIED javap ServerLevel.advanceWeatherCycle — the full countdown/flip/ramp/broadcast structure
//     mirrored below field-for-field, including the wasRaining capture BEFORE the cycle and the
//     isRaining()-diff broadcast AFTER it.]
func (t *TickLoop) tickWeather() {
	t.trace("tickWeather")
	w := &t.weather

	// boolean bl = isRaining();  — captured BEFORE the cycle so the flip-diff below is against the
	// pre-cycle raining state (advanceWeatherCycle local var 1).
	wasRaining := t.isRaining()

	if t.canHaveWeather() {
		if t.advanceWeatherCycleGameRule() {
			// Snapshot the WeatherData fields into locals, exactly as the jar loads them (istore 3..7).
			clear := w.clearWeatherTime
			thunder := w.thunderTime
			rain := w.rainTime
			thundering := w.thundering
			raining := w.raining

			if clear > 0 {
				// clearWeatherTime forces a full clear window: decrement it, park both other timers at
				// (flag ? 0 : 1) so they tick to a flip immediately after clear ends, and clear both flags.
				clear--
				if thundering {
					thunder = 0
				} else {
					thunder = 1
				}
				if raining {
					rain = 0
				} else {
					rain = 1
				}
				thundering = false
				raining = false
			} else {
				// THUNDER branch first (the jar draws thunder before rain — draw order is load-bearing).
				if thunder > 0 {
					thunder--
					if thunder == 0 {
						thundering = !thundering
					}
				} else if thundering {
					thunder = weatherThunderDuration.sample(t.regions[globalRegion].levelRandom)
				} else {
					thunder = weatherThunderDelay.sample(t.regions[globalRegion].levelRandom)
				}
				// RAIN branch second.
				if rain > 0 {
					rain--
					if rain == 0 {
						raining = !raining
					}
				} else if raining {
					rain = weatherRainDuration.sample(t.regions[globalRegion].levelRandom)
				} else {
					rain = weatherRainDelay.sample(t.regions[globalRegion].levelRandom)
				}
			}

			// Write the locals back (WeatherData setters, which also setDirty in vanilla — persistence
			// of WeatherData is a cited deferral, but the in-memory flip is exact).
			w.thunderTime = thunder
			w.rainTime = rain
			w.clearWeatherTime = clear
			w.thundering = thundering
			w.raining = raining
		}

		// oThunderLevel = thunderLevel; ramp thunderLevel by ±0.01 toward the thundering target; clamp.
		w.oThunderLevel = w.thunderLevel
		if w.thundering {
			w.thunderLevel += 0.01
		} else {
			w.thunderLevel -= 0.01
		}
		w.thunderLevel = mthClampF(w.thunderLevel, 0.0, 1.0)

		// oRainLevel = rainLevel; ramp rainLevel by ±0.01 toward the raining target; clamp.
		w.oRainLevel = w.rainLevel
		if w.raining {
			w.rainLevel += 0.01
		} else {
			w.rainLevel -= 0.01
		}
		w.rainLevel = mthClampF(w.rainLevel, 0.0, 1.0)
	}

	// --- BROADCASTS (after the cycle, gated on level/flag changes). Vanilla routes RAIN/THUNDER_LEVEL
	// changes through PlayerList.broadcastAll(packet, dimension) (this world's players only) and the
	// raining-flip packets through broadcastAll(packet) (all players); Sulfur has one world, so both
	// reduce to "every player". ---

	// if (oRainLevel != rainLevel) broadcast RAIN_LEVEL_CHANGE(rainLevel).
	if w.oRainLevel != w.rainLevel {
		t.broadcastGameEvent(gameEventRainLevelChange, w.rainLevel)
	}
	// if (oThunderLevel != thunderLevel) broadcast THUNDER_LEVEL_CHANGE(thunderLevel).
	if w.oThunderLevel != w.thunderLevel {
		t.broadcastGameEvent(gameEventThunderLevelChange, w.thunderLevel)
	}
	// if (wasRaining != isRaining()) { flip → START/STOP_RAINING(0), then a fresh RAIN + THUNDER level
	// pair so a client that just toggled rain re-syncs its levels }.
	if wasRaining != t.isRaining() {
		if wasRaining {
			t.broadcastGameEvent(gameEventStopRaining, 0)
		} else {
			t.broadcastGameEvent(gameEventStartRaining, 0)
		}
		t.broadcastGameEvent(gameEventRainLevelChange, w.rainLevel)
		t.broadcastGameEvent(gameEventThunderLevelChange, w.thunderLevel)
	}
}

// broadcastGameEvent sends a ClientboundGameEvent(id, param) to every connected player. It is the
// PlayerList.broadcastAll analogue for the single world (both broadcastAll overloads reduce to "all
// players" here). Owner-goroutine only (called from tickWeather on the coordinator + the join seam).
func (t *TickLoop) broadcastGameEvent(eventID int, param float32) {
	packet := writeGameEventPacket(eventID, param)
	for _, p := range t.players {
		if p.client == nil {
			continue
		}
		p.client.Send(packet)
	}
}

// getRainLevel ports Level.getRainLevel(float delta): Mth.lerp(delta, oRainLevel, rainLevel). At
// delta==1.0 (every gameplay read below) this is just rainLevel; the lerp form is kept for fidelity so
// a future partial-tick reader is exact.
//
//	[VERIFIED javap Level.getRainLevel(f): Mth.lerp(f, oRainLevel, rainLevel).]
func (t *TickLoop) getRainLevel(delta float32) float32 {
	return mthLerpF(delta, t.weather.oRainLevel, t.weather.rainLevel)
}

// getThunderLevel ports Level.getThunderLevel(float delta): Mth.lerp(delta, oThunderLevel,
// thunderLevel) * getRainLevel(delta). Thunder is gated by rain (no thunder without rain).
//
//	[VERIFIED javap Level.getThunderLevel(f): Mth.lerp(f, oThunderLevel, thunderLevel) *
//	 getRainLevel(f).]
func (t *TickLoop) getThunderLevel(delta float32) float32 {
	return mthLerpF(delta, t.weather.oThunderLevel, t.weather.thunderLevel) * t.getRainLevel(delta)
}

// isRaining ports Level.isRaining: canHaveWeather() && getRainLevel(1.0f) > 0.2. The >0.2 threshold
// (a double compare in the jar) means rain "counts" once the ramp has climbed past 0.2 (~20 ticks after
// the flag flips), matching vanilla's rain onset.
//
//	[VERIFIED javap Level.isRaining: canHaveWeather() && (double) getRainLevel(1.0f) > 0.2d.]
func (t *TickLoop) isRaining() bool {
	return t.canHaveWeather() && float64(t.getRainLevel(1.0)) > 0.2
}

// isThundering ports Level.isThundering: canHaveWeather() && getThunderLevel(1.0f) > 0.9. Consumed by
// the fox thunder-wake gate (ai_goals_fox.go).
//
//	[VERIFIED javap Level.isThundering: canHaveWeather() && (double) getThunderLevel(1.0f) > 0.9d.]
func (t *TickLoop) isThundering() bool {
	return t.canHaveWeather() && float64(t.getThunderLevel(1.0)) > 0.9
}

// mthLerpF ports net.minecraft.util.Mth.lerp(float delta, float start, float end): start + delta*(end -
// start). Sibling of the float64 lerp in explosion.go, kept float32 for the weather ramp fidelity.
//
//	[VERIFIED javap Mth.lerp(f,f,f): start + delta * (end - start).]
func mthLerpF(delta, start, end float32) float32 { return start + delta*(end-start) }

// resetWeatherCycle ports net.minecraft.server.level.ServerLevel.resetWeatherCycle: zero the rain +
// thunder timers and clear both flags on the WeatherData (a full clear). The vanilla method does
// setRainTime(0)/setRaining(false)/setThunderTime(0)/setThundering(false); the ramp fields
// (rainLevel/thunderLevel) are NOT touched here -- they decay back to 0 over the next ~100 ticks via
// tickWeather's +-0.01 ramp, so a clear-on-wake fades the rain out smoothly (matching vanilla, where
// resetWeatherCycle only clears the data and the level ramps down). The all-players-asleep night skip
// calls this (gated on ADVANCE_WEATHER && isRaining), so waking to a clear morning clears the storm.
//
//	[VERIFIED javap ServerLevel.resetWeatherCycle: getWeatherData() -> setRainTime(0); setRaining(false);
//	 setThunderTime(0); setThundering(false).]
func (t *TickLoop) resetWeatherCycle() {
	w := &t.weather
	w.rainTime = 0
	w.raining = false
	w.thunderTime = 0
	w.thundering = false
}
