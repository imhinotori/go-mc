package server

// sculk_gameevent.go - the minimal GameEvent -> vibration-frequency seam the sculk sensor listener
// needs, a 1:1 port of net.minecraft.world.level.gameevent.vibrations.VibrationSystem.{VIBRATION_
// FREQUENCY_FOR_EVENT (the Reference2IntOpenHashMap built in the static initializer), getGameEventFre
// quency, getRedstoneStrengthForDistance} over the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar,
// javap -c VibrationSystem.lambda$static$0).
//
// There is NO game-event bus in v1 (every gameEvent(...) call site is a cite-deferred no-op). This
// file supplies ONLY the frequency table + the redstone-strength formula the sculk sensor reads for a
// STEP vibration; a full game-event emit/listen bus (so every place/break/etc feeds a sensor) is a
// separate subsystem. The sensor's observable behavior (a player walking on it lights it up and it
// outputs redstone = frequency) is driven by the direct STEP source in sculk_sensor_be.go.
//
// FREQUENCY TABLE (VERIFIED javap VibrationSystem.lambda$static$0, the exact GameEvent -> 1..15 map;
// default 0 for any event not listed). This is the redstone output a comparator reads off an ACTIVE
// sculk sensor (getAnalogOutputSignal == lastVibrationFrequency).

// sculkGameEvent is a game-event identity (the subset the frequency table names). A string key keeps
// it decoupled from any (absent) GameEvent registry object; the sensor only ever posts STEP in v1.
type sculkGameEvent string

const (
	gameEventStep sculkGameEvent = "step"
)

// vibrationFrequencyTable is VibrationSystem.VIBRATION_FREQUENCY_FOR_EVENT: the GameEvent -> frequency
// (1..15) map, default 0. VERIFIED javap (every put in lambda$static$0, in order):
//
//	step=1, swim=1, flap=1, projectile_land=2, hit_ground=2, splash=2, bounce=2,
//	item_interact_finish=3, projectile_shoot=3, instrument_play=3, entity_action=4, elytra_glide=4,
//	unequip=4, entity_dismount=5, equip=5, entity_interact=6, shear=6, entity_mount=6, entity_damage=7,
//	drink=8, eat=8, container_close=9, block_close=9, block_deactivate=9, block_detach=9,
//	container_open=10, block_open=10, block_activate=10, block_attach=10, prime_fuse=10,
//	note_block_play=10, block_change=11, block_destroy=12, fluid_pickup=12, block_place=13,
//	fluid_place=13, entity_place=14, lightning_strike=14, teleport=14, entity_die=15, explode=15.
var vibrationFrequencyTable = map[sculkGameEvent]int{
	"step": 1, "swim": 1, "flap": 1,
	"projectile_land": 2, "hit_ground": 2, "splash": 2, "bounce": 2,
	"item_interact_finish": 3, "projectile_shoot": 3, "instrument_play": 3,
	"entity_action": 4, "elytra_glide": 4, "unequip": 4,
	"entity_dismount": 5, "equip": 5,
	"entity_interact": 6, "shear": 6, "entity_mount": 6,
	"entity_damage": 7,
	"drink":         8, "eat": 8,
	"container_close": 9, "block_close": 9, "block_deactivate": 9, "block_detach": 9,
	"container_open": 10, "block_open": 10, "block_activate": 10, "block_attach": 10, "prime_fuse": 10, "note_block_play": 10,
	"block_change":  11,
	"block_destroy": 12, "fluid_pickup": 12,
	"block_place": 13, "fluid_place": 13,
	"entity_place": 14, "lightning_strike": 14, "teleport": 14,
	"entity_die": 15, "explode": 15,
}

// vibrationFrequencyOf ports VibrationSystem.getGameEventFrequency(Holder<GameEvent>): the table
// lookup with a default of 0 (NO_VIBRATION_FREQUENCY). CITE: VibrationSystem.getGameEventFrequency.
func vibrationFrequencyOf(ev sculkGameEvent) int {
	return vibrationFrequencyTable[ev]
}

// vibrationRedstoneStrengthForDistance ports VibrationSystem.getRedstoneStrengthForDistance(distance,
// maxDistance): Math.max(1, 15 - Mth.floor(distance / maxDistance * 15.0)). VERIFIED javap:
//
//	double d = 15.0 / maxDistance; return Math.max(1, 15 - Mth.floor(d * distance)).
//
// A distance-0 vibration (a STEP on the sensor's own block) yields 15 - floor(0) = 15, clamped to at
// least 1. CITE: VibrationSystem.getRedstoneStrengthForDistance.
func vibrationRedstoneStrengthForDistance(distance float32, maxDistance int) int {
	d := 15.0 / float64(maxDistance)
	v := 15 - mthFloor(d*float64(distance))
	if v < 1 {
		return 1
	}
	return v
}
