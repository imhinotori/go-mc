package server

import (
	"context"
	"testing"
)

// gameruleTestCtx builds a command context that authorizes everything and carries a real executor
// (loop + a clientless player), so a /gamerule dispatch runs its body and mutates the loop's store.
func gameruleTestCtx(loop *TickLoop, p *tickPlayer) context.Context {
	ctx := withPermissionResolver(context.Background(), func(string) bool { return true })
	ctx = withExecutor(ctx, loop, p) // executorFrom yields both loop + player for the handler
	return ctx
}

// TestGameruleCommandSetAndQuery: /gamerule mob_griefing false flips the store; a bare query reads it back.
func TestGameruleCommandSetAndQuery(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gamerules = newGameRules()
	p := &tickPlayer{entityID: 1, name: "tester"} // nil client: sendSystemChat nil-guards

	ctx := gameruleTestCtx(loop, p)

	// Set a boolean rule to false.
	if err := cmdGraph.Execute(ctx, "gamerule mob_griefing false"); err != nil {
		t.Fatalf("/gamerule set errored: %v", err)
	}
	if loop.gamerules.getBool(ruleMobGriefing) {
		t.Fatal("/gamerule mob_griefing false did not flip the store")
	}

	// Set it back to true.
	if err := cmdGraph.Execute(ctx, "gamerule mob_griefing true"); err != nil {
		t.Fatalf("/gamerule set true errored: %v", err)
	}
	if !loop.gamerules.getBool(ruleMobGriefing) {
		t.Fatal("/gamerule mob_griefing true did not set the store")
	}
}

// TestGameruleCommandInt: an integer rule parses a decimal value.
func TestGameruleCommandInt(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gamerules = newGameRules()
	p := &tickPlayer{entityID: 1, name: "tester"}
	ctx := gameruleTestCtx(loop, p)

	if err := cmdGraph.Execute(ctx, "gamerule random_tick_speed 12"); err != nil {
		t.Fatalf("/gamerule int set errored: %v", err)
	}
	if got := loop.gamerules.getInt(ruleRandomTickSpeed); got != 12 {
		t.Fatalf("random_tick_speed after set = %d, want 12", got)
	}
}

// TestGameruleCommandUnknownRule: an unknown rule id is rejected and mutates nothing.
func TestGameruleCommandUnknownRule(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gamerules = newGameRules()
	p := &tickPlayer{entityID: 1, name: "tester"}
	ctx := gameruleTestCtx(loop, p)

	err := cmdGraph.Execute(ctx, "gamerule not_a_rule true")
	if err == nil {
		t.Fatal("/gamerule with an unknown rule must error")
	}
}

// TestGameruleCommandInvalidBool: a non-true/false value for a boolean rule errors and does not change it.
func TestGameruleCommandInvalidBool(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gamerules = newGameRules()
	p := &tickPlayer{entityID: 1, name: "tester"}
	ctx := gameruleTestCtx(loop, p)

	before := loop.gamerules.getBool(ruleMobGriefing)
	if err := cmdGraph.Execute(ctx, "gamerule mob_griefing maybe"); err == nil {
		t.Fatal("/gamerule with an invalid boolean must error")
	}
	if loop.gamerules.getBool(ruleMobGriefing) != before {
		t.Fatal("an invalid /gamerule set must not change the rule")
	}
}
