package server

import (
	"errors"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/bot"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
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
		_, err := cfg.AcceptConfig(server)
		srvErr <- err
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

// TestConfigCapturesSkinParts proves BUG-4's CONFIG-state capture: a Client Information packet
// sent during configuration has its modelCustomisation (skin layers) byte captured and returned
// from AcceptConfig, so the joining player spawns with its overlay layers. The drain loop reads
// the ClientInformation (5 fields through modelCustomisation), then exits on the Known Packs echo.
func TestConfigCapturesSkinParts(t *testing.T) {
	server, client := newPipe(t)

	const wantParts uint8 = 0x7F // all skin layers on
	var skinParts uint8
	done := make(chan error, 1)
	go func() {
		done <- (&Configurations{}).drainKnownPacksEcho(server, &skinParts)
	}()

	// Client → server: Client Information (through modelCustomisation), then the Known Packs echo.
	ci := pk.Marshal(int32(packetid.ServerboundConfigClientInformation),
		pk.String("en_us"),       // language
		pk.Byte(10),              // viewDistance
		pk.VarInt(0),             // chatVisibility
		pk.Boolean(true),         // chatColors
		pk.UnsignedByte(wantParts), // modelCustomisation (the captured byte)
		pk.VarInt(1),             // mainHand (trailing fields tolerated, not read here)
		pk.Boolean(false),        // textFilter
		pk.Boolean(true),         // allowsListing
		pk.VarInt(0),             // particleStatus
	)
	if err := client.WritePacket(ci); err != nil {
		t.Fatalf("write client information: %v", err)
	}
	echo := pk.Marshal(int32(packetid.ServerboundConfigSelectKnownPacks), pk.Array([]bot.DataPack{}))
	if err := client.WritePacket(echo); err != nil {
		t.Fatalf("write known-packs echo: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("drainKnownPacksEcho returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("drainKnownPacksEcho deadlocked")
	}
	if skinParts != wantParts {
		t.Fatalf("captured skin parts = %#x, want %#x", skinParts, wantParts)
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
	_, err := cfg.AcceptConfig(server)
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
