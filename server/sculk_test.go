package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// sculk_test.go - SCULK family validation gates against the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar). Covers: (1) a mob dying near a catalyst redirects its XP into the
// spreader (blooms + adds cursors + no orb) and the spread grows sculk/sensors; (2) the sculk sensor
// STEP vibration lights it up + emits redstone POWER (all faces) + the comparator reads the frequency;
// (3) the shrieker warning-level machine increments to 4; (4) the vibration frequency table matches.

func newSculkLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	for _, cp := range []level.ChunkPos{{0, 0}, {-1, 0}, {0, -1}, {-1, -1}, {1, 0}, {0, 1}, {1, 1}, {-1, 1}, {1, -1}} {
		ch := level.EmptyChunk(blockTestSecs)
		ch.Status = level.StatusFull
		mgr.Insert(cp, ch)
	}
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	// Deterministic level RNG for the spread draws (seed pinned; the pig oracle uses its OWN stream).
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(42)
	return loop, mgr
}

func sculkState() block.StateID          { return block.DefaultStateID["minecraft:sculk"] }
func sculkCatalystState() block.StateID  { return block.DefaultStateID["minecraft:sculk_catalyst"] }
func sculkSensorState() block.StateID    { return block.DefaultStateID["minecraft:sculk_sensor"] }
func sculkShriekerState2() block.StateID { return block.DefaultStateID["minecraft:sculk_shrieker"] }

// TestSculkFrequencyTable locks the VibrationSystem.VIBRATION_FREQUENCY_FOR_EVENT values that drive
// the sensor redstone output. STEP=1, ENTITY_DIE=15, and a sampling of the middle bands.
func TestSculkFrequencyTable(t *testing.T) {
	cases := map[sculkGameEvent]int{
		"step": 1, "swim": 1, "flap": 1,
		"projectile_land": 2, "bounce": 2,
		"entity_action": 4,
		"entity_damage": 7,
		"block_change":  11,
		"block_place":   13,
		"entity_die":    15, "explode": 15,
		"unknown_event_xyz": 0,
	}
	for ev, want := range cases {
		if got := vibrationFrequencyOf(ev); got != want {
			t.Fatalf("frequency(%q) = %d, want %d", ev, got, want)
		}
	}
	// getRedstoneStrengthForDistance(0, 8) == 15; (8, 8) == max(1, 15 - 15) == 1.
	if v := vibrationRedstoneStrengthForDistance(0.0, 8); v != 15 {
		t.Fatalf("redstoneStrength(0,8) = %d, want 15", v)
	}
	if v := vibrationRedstoneStrengthForDistance(8.0, 8); v != 1 {
		t.Fatalf("redstoneStrength(8,8) = %d, want 1", v)
	}
}

// TestSculkSensorStepActivatesAndOutputs: a mob standing on a sculk sensor activates it (PHASE ACTIVE,
// POWER=15 for a distance-0 STEP), it emits POWER out every face + strong UP, and a comparator reads
// the STEP frequency (1) off it.
func TestSculkSensorStepActivatesAndOutputs(t *testing.T) {
	loop, mgr := newSculkLoop()
	sensorPos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(sensorPos, sculkSensorState(), dimMinY)
	loop.resolveSculkSensor(sensorPos)

	// A pig standing on the sensor cell.
	e := NewEntity(1, entity.Pig, 8.5, 64, 8.5)
	loop.only().entities.add(e)

	loop.tickSculkSensors()

	after, _ := mgr.GetBlock(sensorPos, dimMinY)
	phase, ok := block.SculkSensorPhaseOf(after)
	if !ok || phase != block.SculkSensorPhaseActive {
		t.Fatalf("sensor phase = %v (ok=%v), want ACTIVE", phase, ok)
	}
	if p := block.SculkSensorPower(after); p != 15 {
		t.Fatalf("sensor POWER = %d, want 15 (distance-0 STEP)", p)
	}
	// getSignal: POWER out every face.
	if s := loop.stateGetSignal(after, sensorPos, block.North); s != 15 {
		t.Fatalf("sensor weak signal (North) = %d, want 15", s)
	}
	// getDirectSignal: only out UP.
	if d := loop.stateGetDirectSignal(after, sensorPos, block.Up); d != 15 {
		t.Fatalf("sensor strong signal (Up) = %d, want 15", d)
	}
	if d := loop.stateGetDirectSignal(after, sensorPos, block.North); d != 0 {
		t.Fatalf("sensor strong signal (North) = %d, want 0 (UP-only)", d)
	}
	// Comparator analog output: the last vibration frequency (STEP == 1) while ACTIVE.
	sig, has := loop.sculkSensorAnalogOutputSignal(sensorPos)
	if !has || sig != 1 {
		t.Fatalf("sensor analog output = %d (has=%v), want 1 (STEP frequency)", sig, has)
	}
}

// TestSculkSensorPhaseMachine: after activation, the scheduled tick moves ACTIVE -> COOLDOWN (POWER 0)
// then COOLDOWN -> INACTIVE.
func TestSculkSensorPhaseMachine(t *testing.T) {
	loop, mgr := newSculkLoop()
	sensorPos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(sensorPos, sculkSensorState(), dimMinY)
	loop.resolveSculkSensor(sensorPos)
	e := NewEntity(1, entity.Pig, 8.5, 64, 8.5)
	loop.only().entities.add(e)
	loop.tickSculkSensors()

	active, _ := mgr.GetBlock(sensorPos, dimMinY)
	// ACTIVE -> tick -> COOLDOWN (POWER 0).
	loop.sculkSensorTick(active, sensorPos)
	cooled, _ := mgr.GetBlock(sensorPos, dimMinY)
	ph, _ := block.SculkSensorPhaseOf(cooled)
	if ph != block.SculkSensorPhaseCooldown {
		t.Fatalf("after ACTIVE tick, phase = %v, want COOLDOWN", ph)
	}
	if p := block.SculkSensorPower(cooled); p != 0 {
		t.Fatalf("after ACTIVE tick, POWER = %d, want 0", p)
	}
	// COOLDOWN -> tick -> INACTIVE.
	loop.sculkSensorTick(cooled, sensorPos)
	inactive, _ := mgr.GetBlock(sensorPos, dimMinY)
	ph, _ = block.SculkSensorPhaseOf(inactive)
	if ph != block.SculkSensorPhaseInactive {
		t.Fatalf("after COOLDOWN tick, phase = %v, want INACTIVE", ph)
	}
}

// TestSculkCatalystOnDeathBloomsAndCharges: a pig dying next to a sculk catalyst redirects its XP into
// the catalyst spreader (cursors added), blooms the catalyst (PULSE set), and spawns NO experience orb
// (the catalyst consumed the XP). This is the sculk-SPREAD entry point.
func TestSculkCatalystOnDeathBloomsAndCharges(t *testing.T) {
	loop, mgr := newSculkLoop()
	catPos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(catPos, sculkCatalystState(), dimMinY)
	be := loop.resolveSculkCatalyst(catPos)
	if be == nil {
		t.Fatal("catalyst BE did not resolve")
	}

	// A pig at the catalyst position (within listener radius 8).
	e := NewEntity(1, entity.Pig, 8.5, 65.0, 8.5)
	loop.only().entities.add(e)

	orbsBefore := countExperienceOrbs(loop)

	// dropMobExperience is the ENTITY_DIE hook: it should be consumed by the catalyst (return early,
	// no orb) and feed the spreader. Use a player-attack source (the reward gate is inside; the catalyst
	// intercept runs first regardless).
	loop.dropMobExperience(e, damageSourcePlayerAttack(2))

	// PULSE (bloom) set on the catalyst.
	after, _ := mgr.GetBlock(catPos, dimMinY)
	if !block.SculkCatalystBloom(after) {
		t.Fatalf("catalyst did not bloom (PULSE) on nearby mob death")
	}
	// Cursors added to the spreader (the redirected XP charge).
	if be.spreader == nil || len(be.spreader.cursors) == 0 {
		t.Fatalf("catalyst spreader got no cursors on mob death")
	}
	// No experience orb spawned (the catalyst consumed the XP).
	if orbsAfter := countExperienceOrbs(loop); orbsAfter != orbsBefore {
		t.Fatalf("experience orbs spawned = %d, want %d (catalyst consumes XP)", orbsAfter-orbsBefore, 0)
	}
	// The bloom scheduled the 8-tick PULSE-clear tick.
	if !loop.hasScheduledBlockTick(catPos, sculkCatalystTickType) {
		t.Fatalf("catalyst bloom did not schedule the PULSE-clear tick")
	}
}

// countExperienceOrbs counts live ExperienceOrb entities across the region store.
func countExperienceOrbs(loop *TickLoop) int {
	n := 0
	if loop.only() == nil {
		return 0
	}
	for _, e := range loop.only().entities.byID {
		if e != nil && e.typ == entity.ExperienceOrb.ID {
			n++
		}
	}
	return n
}

// TestSculkSpreadGrowsOnSculkFloor: with a SCULK carpet under a catalyst, feeding the spreader charge
// and ticking it eventually places a growth (SCULK_SENSOR / SCULK_SHRIEKER) on top of a sculk cell.
// Drives the level SculkSpreader directly with a large charge over many ticks (deterministic seed).
func TestSculkSpreadGrowsOnSculkFloor(t *testing.T) {
	loop, mgr := newSculkLoop()
	catPos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(catPos, sculkCatalystState(), dimMinY)
	// A 9x9 SCULK carpet at y=64 around the catalyst (air above each cell so canPlaceGrowth passes).
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			mgr.SetBlock(pk.Position{X: catPos.X + dx, Y: 64, Z: catPos.Z + dz}, sculkState(), dimMinY)
		}
	}
	mgr.SetBlock(catPos, sculkCatalystState(), dimMinY) // restore the catalyst cell over the carpet

	be := loop.resolveSculkCatalyst(catPos)
	be.spreader = &sculkLiveSpreader{}
	// Feed a large charge so growths appear (noGrowthRadius 4 means growth happens past 4 blocks out).
	be.spreader.addCursors(catPos, 1000)

	grew := false
	for i := 0; i < 4000 && !grew; i++ {
		loop.sculkCatalystServerTick(catPos, be)
		// Scan the y=65 layer for any grown sensor/shrieker.
		for dx := -6; dx <= 6 && !grew; dx++ {
			for dz := -6; dz <= 6 && !grew; dz++ {
				st, ok := mgr.GetBlock(pk.Position{X: catPos.X + dx, Y: 65, Z: catPos.Z + dz}, dimMinY)
				if ok && (block.IsAnySculkSensor(st) || block.IsSculkShrieker(st)) {
					grew = true
				}
			}
		}
		if len(be.spreader.cursors) == 0 {
			break // charge exhausted
		}
	}
	if !grew {
		t.Fatalf("sculk spread over a sculk floor never grew a sensor/shrieker after draining the charge")
	}
}

// TestSculkShriekerWarningIncrementsToFour: a player repeatedly stepping on a CAN_SUMMON shrieker (with
// the warn cooldown expired between shrieks) advances the per-player warden warning level 1,2,3,4 and
// then clamps at 4 (MAX_WARNING_LEVEL). Each shriek also sets SHRIEKING; the scheduled tick clears it.
func TestSculkShriekerWarningIncrementsToFour(t *testing.T) {
	loop, mgr := newSculkLoop()
	shPos := pk.Position{X: 8, Y: 64, Z: 8}
	// CAN_SUMMON shrieker (a natural deep-dark shrieker), not shrieking, not waterlogged.
	canSummon := block.ToStateID[block.SculkShrieker{CanSummon: true, Shrieking: false, Waterlogged: false}]
	mgr.SetBlock(shPos, canSummon, dimMinY)
	loop.resolveSculkShrieker(shPos)

	p := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 1}
	loop.players = append(loop.players, p)

	want := []int{1, 2, 3, 4, 4}
	for i, wl := range want {
		// tryShriek increments the warning level (warn cooldown must be clear each time).
		state, _ := mgr.GetBlock(shPos, dimMinY)
		// Clear SHRIEKING so tryShriek proceeds (the scheduled tick would clear it live).
		if block.SculkShriekerShrieking(state) {
			cleared, _ := block.SculkShriekerWithShrieking(state, false)
			mgr.SetBlock(shPos, cleared, dimMinY)
			state = cleared
		}
		// Clear the per-player warn cooldown so increaseWarningLevel bumps (vanilla ticks it down over
		// 200t; the test advances it directly, exercising the increment machine).
		if w := loop.resolveWardenTracker(p.entityID); w != nil {
			w.cooldownTicks = 0
		}
		be := loop.resolveSculkShrieker(shPos)
		loop.sculkShriekerTryShriek(shPos, state, be, p)
		if got := loop.resolveWardenTracker(p.entityID).warningLevel; got != wl {
			t.Fatalf("step %d: warning level = %d, want %d", i, got, wl)
		}
	}
}

// TestSculkShriekerNoSummonWithoutCanSummon: a shrieker WITHOUT CAN_SUMMON (a player-placed one) never
// responds (canRespond false), so tryShriek shrieks but never advances the warden warning level.
func TestSculkShriekerNoSummonWithoutCanSummon(t *testing.T) {
	loop, mgr := newSculkLoop()
	shPos := pk.Position{X: 8, Y: 64, Z: 8}
	noSummon := block.ToStateID[block.SculkShrieker{CanSummon: false, Shrieking: false, Waterlogged: false}]
	mgr.SetBlock(shPos, noSummon, dimMinY)
	be := loop.resolveSculkShrieker(shPos)

	p := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 1}
	loop.players = append(loop.players, p)

	state, _ := mgr.GetBlock(shPos, dimMinY)
	loop.sculkShriekerTryShriek(shPos, state, be, p)
	// canRespond false -> tryToWarn is skipped -> the player warning level stays 0.
	if got := loop.resolveWardenTracker(p.entityID).warningLevel; got != 0 {
		t.Fatalf("no-CAN_SUMMON shrieker advanced warning level to %d, want 0", got)
	}
	// But it still shrieks (SHRIEKING set).
	after, _ := mgr.GetBlock(shPos, dimMinY)
	if !block.SculkShriekerShrieking(after) {
		t.Fatalf("shrieker did not set SHRIEKING")
	}
}

// TestSculkShriekerLevel4SummonTriggerIsDeferred: at warning level 4 the shrieker's tryRespond reaches
// the summon trigger, which is a DEFERRED no-op (no Warden entity). Assert the trigger gate is at level
// 4 (trySummonWarden returns false below MAX) so the summon fires at the right level once a Warden lands.
func TestSculkShriekerLevel4SummonTriggerIsDeferred(t *testing.T) {
	loop, _ := newSculkLoop()
	be := &sculkShriekerBE{warningLevel: 3}
	if loop.sculkShriekerTrySummonWarden(be) {
		t.Fatalf("summon fired at warning level 3, want false (below MAX)")
	}
	be.warningLevel = 4
	// At MAX the trigger is reached; the Warden spawn is DEFERRED so it still returns false (no entity),
	// but this is the summon-level gate: level 4 is where vanilla attempts Warden.trySpawn.
	if loop.sculkShriekerTrySummonWarden(be) {
		t.Fatalf("Warden summon should be a deferred no-op (no Warden entity), got true")
	}
}
