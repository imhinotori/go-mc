package server

import (
	"errors"
	"testing"
	"time"

	"github.com/imhinotori/go-mc/bot"
)

// configuration_test.go is the NET-04 self-consistency integration test: it runs
// the rewritten AcceptConfig (the server side of the Configuration sequence)
// against the fork's own bot client decoder (bot.(*Client).JoinConfiguration) over
// the net.Pipe harness from pipe_test.go. Because the bot client is the
// authoritative wire reference for protocol 776 config, "both ends complete
// without deadlock or error" proves the full ordered sequence is internally
// correct: Known Packs first, Registry Data from the embedded 26.2 NBT decodes
// into the bot's registries, Update Tags is present, and the Finish→Acknowledge
// round-trip closes the leg. Vanilla byte-correctness is sealed separately by the
// Task 3 capture-diff.

// TestConfigSequence runs AcceptConfig on the server end of a net.Pipe and drives
// the client end with the fork's bot.JoinConfiguration. The full sequence must
// complete with no error AND without deadlock: AcceptConfig exits its drain loop
// on the Known Packs echo alone (the bot never sends Client Information), the bot
// decodes our Registry Data (registering minecraft:overworld and minecraft:plains)
// and the present Update Tags, and on Finish sends the Acknowledge which
// AcceptConfig reads before returning nil.
func TestConfigSequence(t *testing.T) {
	server, client := newPipe(t)

	cfg := &Configurations{}
	srvErr := make(chan error, 1)
	go func() {
		srvErr <- cfg.AcceptConfig(server)
	}()

	c := bot.NewClient()
	cliErr := make(chan error, 1)
	go func() {
		cliErr <- c.JoinConfiguration(client)
	}()

	// The test finishing before this deadline is itself the proof that the drain
	// loop does not block on the absent Client Information (the W2 loop-exit
	// contract). A regression that blocks would surface here as a timeout, not a
	// hung suite.
	deadline := time.After(30 * time.Second)

	var gotSrv, gotCli bool
	for !(gotSrv && gotCli) {
		select {
		case err := <-srvErr:
			if err != nil {
				t.Fatalf("AcceptConfig returned error: %v", err)
			}
			gotSrv = true
		case err := <-cliErr:
			if err != nil {
				t.Fatalf("bot JoinConfiguration returned error: %v", err)
			}
			gotCli = true
		case <-deadline:
			t.Fatalf("config sequence deadlocked: server done=%v client done=%v", gotSrv, gotCli)
		}
	}

	// The bot decoded our Registry Data into its registries: overworld (typed
	// dimension_type) and plains (worldgen/biome) must be present, proving the
	// embedded 26.2 NBT round-tripped through the authoritative decoder.
	if id, _ := c.Registries.DimensionType.Get("minecraft:overworld"); id < 0 {
		t.Errorf("bot did not decode minecraft:overworld from Registry Data")
	}
	if id, _ := c.Registries.WorldGenBiome.Get("minecraft:plains"); id < 0 {
		t.Errorf("bot did not decode minecraft:plains from Registry Data")
	}
}

// TestConfigNoSilentKickOnError asserts that a mid-sequence send failure yields a
// ConfigFailErr with readable reason text (NET-07 / T-2-05) — never a bare close.
// The client end of the pipe is closed before AcceptConfig runs, so the first
// WritePacket (Select Known Packs) fails.
func TestConfigNoSilentKickOnError(t *testing.T) {
	server, client := newPipe(t)
	// Close the client end so every server write/read fails immediately.
	_ = client.Close()

	cfg := &Configurations{}
	err := cfg.AcceptConfig(server)
	if err == nil {
		t.Fatal("expected AcceptConfig to fail when the peer is closed, got nil")
	}

	var cfgErr ConfigFailErr
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected a ConfigFailErr, got %T: %v", err, err)
	}
	if cfgErr.reason.ClearString() == "" {
		t.Error("ConfigFailErr carried an empty reason — a silent kick, not a readable disconnect")
	}
}
