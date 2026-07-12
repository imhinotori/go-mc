package server

import "testing"

// TestGameRulesDefaults asserts the jar-verified vanilla defaults are seeded (the ones consumers read).
func TestGameRulesDefaults(t *testing.T) {
	g := newGameRules()
	if !g.getBool(ruleMobGriefing) {
		t.Fatal("mob_griefing default must be true")
	}
	if g.getBool(ruleKeepInventory) {
		t.Fatal("keep_inventory default must be false")
	}
	if !g.getBool(ruleFallDamage) || !g.getBool(ruleFireDamage) {
		t.Fatal("fall_damage/fire_damage default must be true")
	}
	if !g.getBool(ruleProjectilesBreak) {
		t.Fatal("projectiles_can_break_blocks default must be true (26.2 registerBoolean iconst_1)")
	}
	if g.getInt(ruleRandomTickSpeed) != 3 {
		t.Fatalf("random_tick_speed default = %d, want 3", g.getInt(ruleRandomTickSpeed))
	}
	if g.getInt(ruleMaxEntityCramming) != 24 {
		t.Fatalf("max_entity_cramming default = %d, want 24", g.getInt(ruleMaxEntityCramming))
	}
}

// TestGameRulesSet flips a rule and reads it back; an unregistered id is ignored (no invented rules).
func TestGameRulesSet(t *testing.T) {
	g := newGameRules()
	g.setBool(ruleMobGriefing, false)
	if g.getBool(ruleMobGriefing) {
		t.Fatal("setBool did not flip mob_griefing")
	}
	g.setInt(ruleRandomTickSpeed, 10)
	if g.getInt(ruleRandomTickSpeed) != 10 {
		t.Fatal("setInt did not update random_tick_speed")
	}
	// An unregistered id must not create a rule (guards against a mistyped /gamerule inventing state).
	g.setBool("not_a_real_rule", true)
	if g.getBool("not_a_real_rule") {
		t.Fatal("setBool invented a rule for an unregistered id")
	}
}

// TestGameRuleLazySeed: a TickLoop with no explicit gamerules still reads vanilla defaults (lazy seed).
func TestGameRuleLazySeed(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	if loop.gamerules != nil {
		t.Fatal("a fresh TickLoop should have a nil gamerules until first read")
	}
	if !loop.gameRule(ruleMobGriefing) {
		t.Fatal("lazy-seeded gameRule must read the vanilla mob_griefing default (true)")
	}
	if loop.gamerules == nil {
		t.Fatal("gameRule must seed the store on first read")
	}
}

// TestGameRulesFullSet asserts EVERY vanilla 26.2 gamerule is registered with its exact jar-verified
// default (GameRules static init: 47 boolean + 12 integer = 59 rules). Any drift from the bytecode
// defaults (or a missing/renamed id) fails here.
func TestGameRulesFullSet(t *testing.T) {
	g := newGameRules()

	wantBool := map[string]bool{
		"advance_time": true, "advance_weather": true, "allow_entering_nether_using_portals": true,
		"block_drops": true, "block_explosion_drop_decay": true, "command_blocks_work": true,
		"command_block_output": true, "drowning_damage": true, "elytra_movement_check": true,
		"ender_pearls_vanish_on_death": true, "entity_drops": true, "fall_damage": true,
		"fire_damage": true, "forgive_dead_players": true, "freeze_damage": true,
		"global_sound_events": true, "immediate_respawn": false, "keep_inventory": false,
		"lava_source_conversion": false, "limited_crafting": false, "locator_bar": true,
		"log_admin_commands": true, "mob_drops": true, "mob_explosion_drop_decay": true,
		"mob_griefing": true, "natural_health_regeneration": true, "player_movement_check": true,
		"projectiles_can_break_blocks": true, "pvp": true, "raids": true, "reduced_debug_info": false,
		"send_command_feedback": true, "show_advancement_messages": true, "show_death_messages": true,
		"spawner_blocks_work": true, "spawn_mobs": true, "spawn_monsters": true, "spawn_patrols": true,
		"spawn_phantoms": true, "spawn_wandering_traders": true, "spawn_wardens": true,
		"spectators_generate_chunks": true, "spread_vines": true, "tnt_explodes": true,
		"tnt_explosion_drop_decay": false, "universal_anger": false, "water_source_conversion": true,
	}
	wantInt := map[string]int{
		"fire_spread_radius_around_player": 128, "max_block_modifications": 32768,
		"max_command_forks": 65536, "max_command_sequence_length": 65536, "max_entity_cramming": 24,
		"max_minecart_speed": 8, "max_snow_accumulation_height": 1,
		"players_nether_portal_creative_delay": 0, "players_nether_portal_default_delay": 80,
		"players_sleeping_percentage": 100, "random_tick_speed": 3, "respawn_radius": 10,
	}

	if len(wantBool) != 47 {
		t.Fatalf("test table has %d boolean rules, want 47", len(wantBool))
	}
	if len(wantInt) != 12 {
		t.Fatalf("test table has %d integer rules, want 12", len(wantInt))
	}
	if len(g.bools) != len(wantBool) {
		t.Fatalf("store has %d boolean rules, want %d", len(g.bools), len(wantBool))
	}
	if len(g.ints) != len(wantInt) {
		t.Fatalf("store has %d integer rules, want %d", len(g.ints), len(wantInt))
	}
	for id, want := range wantBool {
		got, ok := g.bools[id]
		if !ok {
			t.Fatalf("boolean rule %q not registered", id)
		}
		if got != want {
			t.Fatalf("boolean rule %q default = %t, want %t", id, got, want)
		}
	}
	for id, want := range wantInt {
		got, ok := g.ints[id]
		if !ok {
			t.Fatalf("integer rule %q not registered", id)
		}
		if got != want {
			t.Fatalf("integer rule %q default = %d, want %d", id, got, want)
		}
	}
}

// TestIsSpawningMonsters mirrors ServerLevel.isSpawningMonsters: SPAWN_MOBS AND SPAWN_MONSTERS.
func TestIsSpawningMonsters(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gamerules = newGameRules()
	if !loop.isSpawningMonsters() {
		t.Fatal("defaults: spawn_mobs && spawn_monsters both true -> isSpawningMonsters true")
	}
	loop.gamerules.setBool(ruleSpawnMonsters, false)
	if loop.isSpawningMonsters() {
		t.Fatal("spawn_monsters false -> isSpawningMonsters false")
	}
	loop.gamerules.setBool(ruleSpawnMonsters, true)
	loop.gamerules.setBool(ruleSpawnMobs, false)
	if loop.isSpawningMonsters() {
		t.Fatal("spawn_mobs false -> isSpawningMonsters false even with spawn_monsters true")
	}
}
