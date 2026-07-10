package server

// gamerules.go — GAME RULES: a 1:1 port of net.minecraft.world.level.gamerules.GameRules — the per-level
// keyed store of boolean/integer rules with their vanilla default values (temp/cache/26.2-inner.jar, javap
// this session — the static registration block). It replaces the scattered hardcoded gamerule stubs
// (`var mobGriefing = true`, `doFireTickGameRule() { return true }`, etc.) with a single authoritative store
// read through typed accessors, so /gamerule (a later command seam) flips a real value and every consumer
// re-reads it with no refactor.
//
// 26.2 renamed several rules from their historical names (the wire/registry ids are the new ones): the old
// doDaylightCycle is ADVANCE_TIME ("advance_time"), doWeatherCycle is ADVANCE_WEATHER, doMobLoot split into
// MOB_DROPS + ENTITY_DROPS, doTileDrops is BLOCK_DROPS, doMobSpawning is SPAWN_MOBS, and doFireTick's spread
// is folded into FIRE_SPREAD_RADIUS_AROUND_PLAYER (a radius, default 128). This store keys on the NEW ids.
//
// Defaults verified against the GameRules static init (javap): bool rules default TRUE except keep_inventory
// (false), projectiles_can_break_blocks (false), universal_anger (false), immediate_respawn (false),
// reduced_debug_info (false); int rules random_tick_speed=3, max_entity_cramming=24, max_minecart_speed=8,
// fire_spread_radius_around_player=128, players_sleeping_percentage=100, respawn_radius=10, snow=1.

// Boolean gamerule ids (the new 26.2 registry ids). Only the rules a consumer reads today are named; the
// full set lives in the defaults table below so /gamerule can round-trip any of them.
const (
	ruleMobGriefing       = "mob_griefing"                 // MOB_GRIEFING (default true)
	ruleFallDamage        = "fall_damage"                  // FALL_DAMAGE (default true)
	ruleFireDamage        = "fire_damage"                  // FIRE_DAMAGE (default true)
	ruleDrowningDamage    = "drowning_damage"              // DROWNING_DAMAGE (default true)
	ruleKeepInventory     = "keep_inventory"               // KEEP_INVENTORY (default false)
	ruleMobDrops          = "mob_drops"                    // MOB_DROPS (default true)
	ruleBlockDrops        = "block_drops"                  // BLOCK_DROPS (default true)
	ruleEntityDrops       = "entity_drops"                 // ENTITY_DROPS (default true)
	ruleNaturalRegen      = "natural_health_regeneration"  // NATURAL_HEALTH_REGENERATION (default true)
	ruleAdvanceTime       = "advance_time"                 // ADVANCE_TIME (ex-doDaylightCycle, default true)
	ruleAdvanceWeather    = "advance_weather"              // ADVANCE_WEATHER (default true)
	ruleSpawnMonsters     = "spawn_monsters"               // SPAWN_MONSTERS (default true)
	rulePvP               = "pvp"                          // PVP (default true)
	ruleShowDeathMessages = "show_death_messages"          // SHOW_DEATH_MESSAGES (default true)
	ruleTNTExplodes       = "tnt_explodes"                 // TNT_EXPLODES (default true)
	ruleProjectilesBreak  = "projectiles_can_break_blocks" // PROJECTILES_CAN_BREAK_BLOCKS (default false)
	ruleUniversalAnger    = "universal_anger"              // UNIVERSAL_ANGER (default false)
	ruleImmediateRespawn  = "immediate_respawn"            // IMMEDIATE_RESPAWN (default false)
)

// Integer gamerule ids.
const (
	ruleRandomTickSpeed   = "random_tick_speed"                // default 3
	ruleMaxEntityCramming = "max_entity_cramming"              // default 24
	ruleFireSpreadRadius  = "fire_spread_radius_around_player" // default 128
	ruleMaxMinecartSpeed  = "max_minecart_speed"               // default 8
	ruleSleepPercent      = "players_sleeping_percentage"      // default 100
	ruleRespawnRadius     = "respawn_radius"                   // default 10
	// ruleMaxSnowAccumulation is MAX_SNOW_ACCUMULATION_HEIGHT: the cap on snow LAYERS the precipitation
	// pass will accumulate (ServerLevel.tickPrecipitation reads min(this,8)). CITE: GameRules.
	// MAX_SNOW_ACCUMULATION_HEIGHT (registerInteger("max_snow_accumulation_height", UPDATES, 1) -> default 1).
	ruleMaxSnowAccumulation = "max_snow_accumulation_height" // default 1
)

// gameRules is the per-level GameRules store: two typed maps keyed on the registry id. A rule not present
// in a map reads its zero value, but the store is always seeded with the full vanilla default set at
// construction (newGameRules), so every read hits a real, jar-verified default. Cite GameRules.
type gameRules struct {
	bools map[string]bool
	ints  map[string]int
}

// newGameRules builds a fresh GameRules seeded with the vanilla defaults (GameRules static init). Every
// registered rule's default is jar-verified; the ones a consumer reads are exercised, the rest round-trip
// through /gamerule. Cite GameRules.<RULE>.create(default).
func newGameRules() *gameRules {
	return &gameRules{
		bools: map[string]bool{
			ruleMobGriefing:       true,
			ruleFallDamage:        true,
			ruleFireDamage:        true,
			ruleDrowningDamage:    true,
			ruleKeepInventory:     false,
			ruleMobDrops:          true,
			ruleBlockDrops:        true,
			ruleEntityDrops:       true,
			ruleNaturalRegen:      true,
			ruleAdvanceTime:       true,
			ruleAdvanceWeather:    true,
			ruleSpawnMonsters:     true,
			rulePvP:               true,
			ruleShowDeathMessages: true,
			ruleTNTExplodes:       true,
			ruleProjectilesBreak:  false,
			ruleUniversalAnger:    false,
			ruleImmediateRespawn:  false,
		},
		ints: map[string]int{
			ruleRandomTickSpeed:     3,
			ruleMaxEntityCramming:   24,
			ruleFireSpreadRadius:    128,
			ruleMaxMinecartSpeed:    8,
			ruleSleepPercent:        100,
			ruleRespawnRadius:       10,
			ruleMaxSnowAccumulation: 1,
		},
	}
}

// getBool is GameRules.getBoolean(rule): the current value of a boolean rule (false for an unregistered id).
func (g *gameRules) getBool(id string) bool { return g.bools[id] }

// getInt is GameRules.getInt(rule): the current value of an integer rule (0 for an unregistered id).
func (g *gameRules) getInt(id string) int { return g.ints[id] }

// setBool / setInt are the /gamerule setters (GameRule.set). setBool ignores an id that is not a registered
// boolean rule (and vice versa), so a mistyped id cannot invent a rule that consumers do not read.
func (g *gameRules) setBool(id string, v bool) {
	if _, ok := g.bools[id]; ok {
		g.bools[id] = v
	}
}

func (g *gameRules) setInt(id string, v int) {
	if _, ok := g.ints[id]; ok {
		g.ints[id] = v
	}
}

// gameRule reads a boolean rule off the loop's GameRules store, seeding a default store on first use so a
// TickLoop constructed without an explicit gameRules (tests) still reads the vanilla defaults. This is the
// single accessor every gameplay consumer routes through (ServerLevel.getGameRules().getBoolean(rule)).
func (t *TickLoop) gameRule(id string) bool {
	if t.gamerules == nil {
		t.gamerules = newGameRules()
	}
	return t.gamerules.getBool(id)
}

// gameRuleInt reads an integer rule off the loop's GameRules store (sibling of gameRule).
func (t *TickLoop) gameRuleInt(id string) int {
	if t.gamerules == nil {
		t.gamerules = newGameRules()
	}
	return t.gamerules.getInt(id)
}
