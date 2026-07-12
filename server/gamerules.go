package server

import "strconv"

// gamerules.go: GAME RULES -- a 1:1 port of net.minecraft.world.level.gamerules.GameRules, the per-level
// keyed store of boolean/integer rules with their vanilla default values (temp/cache/26.2-inner.jar, javap
// this session -- the static registration block of GameRules). It replaces the scattered hardcoded gamerule
// stubs (a var mobGriefing=true, a doFireTickGameRule returning true, etc.) with a single authoritative
// store read through typed accessors, so /gamerule (commands_vanilla.go) flips a real value and every
// consumer re-reads it with no refactor.
//
// FULL SET (verified against GameRules static init this session): 47 boolean rules + 12 integer rules = 59
// registered rules (matches the 59 GameRule<Boolean|Integer> static fields on the class). Every id below is
// the EXACT registry string loaded in the static init immediately before registerBoolean/registerInteger,
// and every default is the exact iconst_/bipush/sipush/ldc value pushed at that call site. Bytecode trace:
//
//	registerBoolean(id, CATEGORY, default)  -- default: iconst_1 => true, iconst_0 => false
//	registerInteger(id, CATEGORY, default [, min [, max [, featureflags]]])
//
// 26.2 renamed several rules from their historical names (the wire/registry ids are the new ones): the old
// doDaylightCycle is ADVANCE_TIME (advance_time), doWeatherCycle is ADVANCE_WEATHER (advance_weather),
// doMobLoot split into MOB_DROPS + ENTITY_DROPS, doTileDrops is BLOCK_DROPS, doMobSpawning is SPAWN_MOBS,
// and doFireTick was replaced by FIRE_SPREAD_RADIUS_AROUND_PLAYER (an int radius, -1 disables, default 128).
// This store keys on the NEW ids.

// Boolean gamerule ids (the exact 26.2 registry strings). Grouped by family for readability; the defaults
// live in the newGameRules table below.
const (
	ruleAdvanceTime           = "advance_time"                        // ADVANCE_TIME (ex-doDaylightCycle, default true)
	ruleAdvanceWeather        = "advance_weather"                     // ADVANCE_WEATHER (ex-doWeatherCycle, default true)
	ruleAllowNetherPortals    = "allow_entering_nether_using_portals" // ALLOW_ENTERING_NETHER_USING_PORTALS (default true)
	ruleBlockDrops            = "block_drops"                         // BLOCK_DROPS (ex-doTileDrops, default true)
	ruleBlockExplosionDecay   = "block_explosion_drop_decay"          // BLOCK_EXPLOSION_DROP_DECAY (default true)
	ruleCommandBlocksWork     = "command_blocks_work"                 // COMMAND_BLOCKS_WORK (default true)
	ruleCommandBlockOutput    = "command_block_output"                // COMMAND_BLOCK_OUTPUT (default true)
	ruleDrowningDamage        = "drowning_damage"                     // DROWNING_DAMAGE (default true)
	ruleElytraMovementCheck   = "elytra_movement_check"               // ELYTRA_MOVEMENT_CHECK (ex-disableElytraMovementCheck, default true)
	ruleEnderPearlsVanish     = "ender_pearls_vanish_on_death"        // ENDER_PEARLS_VANISH_ON_DEATH (default true)
	ruleEntityDrops           = "entity_drops"                        // ENTITY_DROPS (default true)
	ruleFallDamage            = "fall_damage"                         // FALL_DAMAGE (default true)
	ruleFireDamage            = "fire_damage"                         // FIRE_DAMAGE (default true)
	ruleForgiveDeadPlayers    = "forgive_dead_players"                // FORGIVE_DEAD_PLAYERS (default true)
	ruleFreezeDamage          = "freeze_damage"                       // FREEZE_DAMAGE (default true)
	ruleGlobalSoundEvents     = "global_sound_events"                 // GLOBAL_SOUND_EVENTS (default true)
	ruleImmediateRespawn      = "immediate_respawn"                   // IMMEDIATE_RESPAWN (default false)
	ruleKeepInventory         = "keep_inventory"                      // KEEP_INVENTORY (default false)
	ruleLavaSourceConversion  = "lava_source_conversion"              // LAVA_SOURCE_CONVERSION (default false)
	ruleLimitedCrafting       = "limited_crafting"                    // LIMITED_CRAFTING (default false)
	ruleLocatorBar            = "locator_bar"                         // LOCATOR_BAR (default true)
	ruleLogAdminCommands      = "log_admin_commands"                  // LOG_ADMIN_COMMANDS (default true)
	ruleMobDrops              = "mob_drops"                           // MOB_DROPS (default true)
	ruleMobExplosionDecay     = "mob_explosion_drop_decay"            // MOB_EXPLOSION_DROP_DECAY (default true)
	ruleMobGriefing           = "mob_griefing"                        // MOB_GRIEFING (default true)
	ruleNaturalRegen          = "natural_health_regeneration"         // NATURAL_HEALTH_REGENERATION (default true)
	rulePlayerMovementCheck   = "player_movement_check"               // PLAYER_MOVEMENT_CHECK (default true)
	ruleProjectilesBreak      = "projectiles_can_break_blocks"        // PROJECTILES_CAN_BREAK_BLOCKS (default true)
	rulePvP                   = "pvp"                                 // PVP (default true)
	ruleRaids                 = "raids"                               // RAIDS (ex-disableRaids, default true)
	ruleReducedDebugInfo      = "reduced_debug_info"                  // REDUCED_DEBUG_INFO (default false)
	ruleSendCommandFeedback   = "send_command_feedback"               // SEND_COMMAND_FEEDBACK (default true)
	ruleShowAdvancementMsgs   = "show_advancement_messages"           // SHOW_ADVANCEMENT_MESSAGES (default true)
	ruleShowDeathMessages     = "show_death_messages"                 // SHOW_DEATH_MESSAGES (default true)
	ruleSpawnerBlocksWork     = "spawner_blocks_work"                 // SPAWNER_BLOCKS_WORK (default true)
	ruleSpawnMobs             = "spawn_mobs"                          // SPAWN_MOBS (ex-doMobSpawning, default true)
	ruleSpawnMonsters         = "spawn_monsters"                      // SPAWN_MONSTERS (default true)
	ruleSpawnPatrols          = "spawn_patrols"                       // SPAWN_PATROLS (ex-doPatrolSpawning, default true)
	ruleSpawnPhantoms         = "spawn_phantoms"                      // SPAWN_PHANTOMS (ex-doInsomnia, default true)
	ruleSpawnTraders          = "spawn_wandering_traders"             // SPAWN_WANDERING_TRADERS (ex-doTraderSpawning, default true)
	ruleSpawnWardens          = "spawn_wardens"                       // SPAWN_WARDENS (ex-doWardenSpawning, default true)
	ruleSpectatorsGenChunks   = "spectators_generate_chunks"          // SPECTATORS_GENERATE_CHUNKS (default true)
	ruleSpreadVines           = "spread_vines"                        // SPREAD_VINES (default true)
	ruleTNTExplodes           = "tnt_explodes"                        // TNT_EXPLODES (default true)
	ruleTNTExplosionDecay     = "tnt_explosion_drop_decay"            // TNT_EXPLOSION_DROP_DECAY (default false)
	ruleUniversalAnger        = "universal_anger"                     // UNIVERSAL_ANGER (default false)
	ruleWaterSourceConversion = "water_source_conversion"             // WATER_SOURCE_CONVERSION (default true)
)

// Integer gamerule ids (the exact 26.2 registry strings).
const (
	ruleFireSpreadRadius    = "fire_spread_radius_around_player"     // default 128 (min -1; -1 disables fire spread)
	ruleMaxBlockMods        = "max_block_modifications"              // default 32768 (min 1)
	ruleMaxCommandForks     = "max_command_forks"                    // default 65536 (min 0)
	ruleMaxCmdSequenceLen   = "max_command_sequence_length"          // default 65536 (min 0)
	ruleMaxEntityCramming   = "max_entity_cramming"                  // default 24 (min 0)
	ruleMaxMinecartSpeed    = "max_minecart_speed"                   // default 8 (min 1, max 1000; feature-flag gated)
	ruleMaxSnowAccumulation = "max_snow_accumulation_height"         // default 1 (min 0, max 8)
	ruleNetherPortalCrDelay = "players_nether_portal_creative_delay" // default 0 (min 0)
	ruleNetherPortalDefDlay = "players_nether_portal_default_delay"  // default 80 (min 0)
	ruleSleepPercent        = "players_sleeping_percentage"          // default 100 (min 0)
	ruleRandomTickSpeed     = "random_tick_speed"                    // default 3 (min 0)
	ruleRespawnRadius       = "respawn_radius"                       // default 10 (min 0)
)

// gameRules is the per-level GameRules store: two typed maps keyed on the registry id. A rule not present
// in a map reads its zero value, but the store is always seeded with the full vanilla default set at
// construction (newGameRules), so every read hits a real, jar-verified default. Cite GameRules.
type gameRules struct {
	bools map[string]bool
	ints  map[string]int
}

// newGameRules builds a fresh GameRules seeded with the FULL vanilla default set (GameRules static init).
// Every default below is the exact value pushed to registerBoolean/registerInteger in the static init
// (verified against the bytecode this session): booleans default TRUE except immediate_respawn,
// keep_inventory, lava_source_conversion, limited_crafting, reduced_debug_info, tnt_explosion_drop_decay,
// universal_anger (all FALSE). Cite net.minecraft.world.level.gamerules.GameRules static init.
func newGameRules() *gameRules {
	return &gameRules{
		bools: map[string]bool{
			ruleAdvanceTime:           true,
			ruleAdvanceWeather:        true,
			ruleAllowNetherPortals:    true,
			ruleBlockDrops:            true,
			ruleBlockExplosionDecay:   true,
			ruleCommandBlocksWork:     true,
			ruleCommandBlockOutput:    true,
			ruleDrowningDamage:        true,
			ruleElytraMovementCheck:   true,
			ruleEnderPearlsVanish:     true,
			ruleEntityDrops:           true,
			ruleFallDamage:            true,
			ruleFireDamage:            true,
			ruleForgiveDeadPlayers:    true,
			ruleFreezeDamage:          true,
			ruleGlobalSoundEvents:     true,
			ruleImmediateRespawn:      false,
			ruleKeepInventory:         false,
			ruleLavaSourceConversion:  false,
			ruleLimitedCrafting:       false,
			ruleLocatorBar:            true,
			ruleLogAdminCommands:      true,
			ruleMobDrops:              true,
			ruleMobExplosionDecay:     true,
			ruleMobGriefing:           true,
			ruleNaturalRegen:          true,
			rulePlayerMovementCheck:   true,
			ruleProjectilesBreak:      true,
			rulePvP:                   true,
			ruleRaids:                 true,
			ruleReducedDebugInfo:      false,
			ruleSendCommandFeedback:   true,
			ruleShowAdvancementMsgs:   true,
			ruleShowDeathMessages:     true,
			ruleSpawnerBlocksWork:     true,
			ruleSpawnMobs:             true,
			ruleSpawnMonsters:         true,
			ruleSpawnPatrols:          true,
			ruleSpawnPhantoms:         true,
			ruleSpawnTraders:          true,
			ruleSpawnWardens:          true,
			ruleSpectatorsGenChunks:   true,
			ruleSpreadVines:           true,
			ruleTNTExplodes:           true,
			ruleTNTExplosionDecay:     false,
			ruleUniversalAnger:        false,
			ruleWaterSourceConversion: true,
		},
		ints: map[string]int{
			ruleFireSpreadRadius:    128,
			ruleMaxBlockMods:        32768,
			ruleMaxCommandForks:     65536,
			ruleMaxCmdSequenceLen:   65536,
			ruleMaxEntityCramming:   24,
			ruleMaxMinecartSpeed:    8,
			ruleMaxSnowAccumulation: 1,
			ruleNetherPortalCrDelay: 0,
			ruleNetherPortalDefDlay: 80,
			ruleSleepPercent:        100,
			ruleRandomTickSpeed:     3,
			ruleRespawnRadius:       10,
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

// gameRule reads a boolean rule off the loop GameRules store, seeding a default store on first use so a
// TickLoop constructed without an explicit gameRules (tests) still reads the vanilla defaults. This is the
// single accessor every gameplay consumer routes through (ServerLevel.getGameRules().getBoolean(rule)).
func (t *TickLoop) gameRule(id string) bool {
	if t.gamerules == nil {
		t.gamerules = newGameRules()
	}
	return t.gamerules.getBool(id)
}

// gameRuleInt reads an integer rule off the loop GameRules store (sibling of gameRule).
func (t *TickLoop) gameRuleInt(id string) int {
	if t.gamerules == nil {
		t.gamerules = newGameRules()
	}
	return t.gamerules.getInt(id)
}

// isSpawningMonsters is net.minecraft.server.level.ServerLevel.isSpawningMonsters(): SPAWN_MOBS AND
// SPAWN_MONSTERS (verified bytecode this session -- two getGameRules().get reads combined with a
// short-circuit ifeq). It is the hostile-spawn gate the natural spawner + Zombie.hurtServer reinforcement
// path read. CITE: ServerLevel.isSpawningMonsters.
func (t *TickLoop) isSpawningMonsters() bool {
	return t.gameRule(ruleSpawnMobs) && t.gameRule(ruleSpawnMonsters)
}

// toStringMap serializes the GameRules store into the CompoundTag string-map form level.dat uses
// (GameRules.createTag: each rule value is written as its string form -- bools as true/false, ints as
// their decimal string). Every registered rule is emitted so a reload restores the full set. CITE
// net.minecraft.world.level.gamerules.GameRules.createTag / GameRules.Value.serialize.
func (g *gameRules) toStringMap() map[string]string {
	out := make(map[string]string, len(g.bools)+len(g.ints))
	for k, v := range g.bools {
		if v {
			out[k] = "true"
		} else {
			out[k] = "false"
		}
	}
	for k, v := range g.ints {
		out[k] = strconv.Itoa(v)
	}
	return out
}

// applyStringMap restores rule values from the level.dat string map (GameRules.loadFromTag): for each
// key that is a REGISTERED rule (bool or int) it parses the string and sets it; an unknown/renamed key
// is ignored (setBool/setInt no-op on an unregistered id), so an older save never invents a rule. A
// malformed value leaves the default in place. CITE GameRules.loadFromTag / GameRules.Value.deserialize.
func (g *gameRules) applyStringMap(m map[string]string) {
	for k, v := range m {
		if _, ok := g.bools[k]; ok {
			g.setBool(k, v == "true")
			continue
		}
		if _, ok := g.ints[k]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				g.setInt(k, n)
			}
		}
	}
}
