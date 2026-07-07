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
	if g.getBool(ruleProjectilesBreak) {
		t.Fatal("projectiles_can_break_blocks default must be false")
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
