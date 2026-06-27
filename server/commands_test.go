package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/command"
)

// commands_test.go covers CMD-01: server-side command dispatch built on the fork
// command.Graph. The graph is built once, sent per-connection at join as
// ClientboundCommands (so the client tab-completes), and a client's
// ServerboundChatCommand is decoded + routed to Graph.Execute ON THE TICK goroutine
// with a defensive decode, a length bound, and a permission gate (ASVS V4/V5).
//
// The ServerboundChatCommand wire is JAR-VERIFIED (javap'd from temp/cache/26.2-inner.jar
// this session): ServerboundChatCommandPacket is a record with a single `command` field
// decoded via FriendlyByteBuf.readUtf() / written via writeUtf() — NO signing, NO salt,
// NO timestamp. So the serverbound decode is a single pk.String.

// chatCommandPacket builds a ServerboundChatCommand carrying the single command String
// (the jar's `command` field) — the only field the packet has.
func chatCommandPacket(cmd string) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundChatCommand), pk.String(cmd))
}

// commandPlayer registers a minimal, fully-usable *tickPlayer on the loop with a capturing
// client, mirroring newTrackerPlayer (tracker_test.go): the player is appended to
// loop.players and indexed in loop.clientIndex so dispatch resolves it for its connection,
// and its client is a real bounded captureClient so sendCommandGraph can enqueue and a test
// can drainPackets to assert the ClientboundCommands send. No tracker/chunk fields are set —
// the command path only needs client + clientIndex registration. The entity id is a high,
// distinct value so it never collides with allocator-issued ids (allocator starts at 1).
func commandPlayer(loop *TickLoop) *tickPlayer {
	p := &tickPlayer{
		client:   captureClient(64),
		entityID: 2000,
	}
	loop.players = append(loop.players, p)
	if loop.clientIndex == nil {
		loop.clientIndex = make(map[*Client]*tickPlayer)
	}
	loop.clientIndex[p.client] = p
	return p
}

// TestCommandGraphBuilds asserts buildCommandGraph returns a *command.Graph with the v1
// literals registered and that it serializes (WriteTo) without error.
func TestCommandGraphBuilds(t *testing.T) {
	g := buildCommandGraph()
	if g == nil {
		t.Fatal("buildCommandGraph returned nil")
	}
	var sb strings.Builder
	if _, err := g.WriteTo(&sb); err != nil {
		t.Fatalf("graph WriteTo failed: %v", err)
	}
	if sb.Len() == 0 {
		t.Fatal("graph serialized to zero bytes")
	}
	// The registered v1 literals must be Executable on the graph: /say and /me run.
	ctx := withPermissionResolver(context.Background(), func(node string) bool { return true })
	if err := g.Execute(ctx, "say hi"); err != nil {
		t.Fatalf("/say hi should execute on the built graph: %v", err)
	}
	if err := g.Execute(ctx, "me waves"); err != nil {
		t.Fatalf("/me waves should execute on the built graph: %v", err)
	}
}

// TestCommandClientAdapter asserts the command.Client adapter over server.*Client
// forwards ClientJoin's write to Client.Send — a captured packet has the
// ClientboundCommands id.
func TestCommandClientAdapter(t *testing.T) {
	c := captureClient(8)
	g := buildCommandGraph()
	g.ClientJoin(commandClientAdapter{c})

	got := drainPackets(c)
	if countID(got, packetid.ClientboundCommands) != 1 {
		t.Fatalf("adapter forwarded %d ClientboundCommands packets, want 1 (got %d packets total)",
			countID(got, packetid.ClientboundCommands), len(got))
	}
}

// TestCommandGraphSentAtJoin asserts the join-time send helper enqueues a
// ClientboundCommands packet onto the joining client's outbound queue (the tab-complete
// graph reaches the client at join).
func TestCommandGraphSentAtJoin(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)

	loop.sendCommandGraph(p)

	got := drainPackets(p.client)
	if countID(got, packetid.ClientboundCommands) != 1 {
		t.Fatalf("sendCommandGraph enqueued %d ClientboundCommands packets, want 1",
			countID(got, packetid.ClientboundCommands))
	}
}

// TestCommandPermissionGate asserts a literal's handler consults the permission hook
// before acting: a DENIED permission does NOT run the handler body (ASVS V4 / T-7-02).
func TestCommandPermissionGate(t *testing.T) {
	ran := false
	// A command.HandlerFunc wrapped by the permission gate: when permission is denied
	// the body must not run.
	gated := permissionGated("test.denied", func(ctx context.Context, args []command.ParsedData) error {
		ran = true
		return nil
	})

	// Deny: the body must NOT run.
	ctxDeny := withPermissionResolver(context.Background(), func(node string) bool { return false })
	_ = gated(ctxDeny, nil)
	if ran {
		t.Fatal("permission-gated handler ran its body despite a DENIED permission (ASVS V4)")
	}

	// Grant: the body DOES run.
	ran = false
	ctxAllow := withPermissionResolver(context.Background(), func(node string) bool { return true })
	_ = gated(ctxAllow, nil)
	if !ran {
		t.Fatal("permission-gated handler did not run its body despite a GRANTED permission")
	}
}

// withExecuteCommand swaps the dispatch executor seam for the duration of a test and
// restores it on cleanup, so a test can capture the exact command string runCommand routed
// to Execute (or inject an error/panic path) without mutating the read-only shared graph.
func withExecuteCommand(t *testing.T, fn func(ctx context.Context, cmd string) error) {
	t.Helper()
	prev := executeCommand
	executeCommand = fn
	t.Cleanup(func() { executeCommand = prev })
}

// TestChatCommandRouted asserts a ServerboundChatCommand carrying "say hi" passed to
// dispatch decodes the command string and routes it to Graph.Execute with exactly "say hi"
// (CMD-01: the serverbound decode + the on-tick Execute route). The captured arg proves the
// jar-verified single-String decode reached the dispatcher unaltered.
func TestChatCommandRouted(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)

	var got string
	called := false
	withExecuteCommand(t, func(ctx context.Context, cmd string) error {
		called = true
		got = cmd
		return nil
	})

	loop.dispatch(p.client, chatCommandPacket("say hi"))

	if !called {
		t.Fatal("dispatch did not route ServerboundChatCommand to Execute")
	}
	if got != "say hi" {
		t.Fatalf("Execute received %q, want %q", got, "say hi")
	}
}

// TestChatCommandMalformed asserts a malformed ServerboundChatCommand payload (not a valid
// String) Scan-errors to a no-op: Execute is NOT called and dispatch never panics (T-7-03 /
// T-3-02). A truncated VarInt-length-prefixed String body fails Scan.
func TestChatCommandMalformed(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)

	called := false
	withExecuteCommand(t, func(ctx context.Context, cmd string) error {
		called = true
		return nil
	})

	// A ServerboundChatCommand id with a body claiming a 10-byte String but supplying none:
	// pk.String.ReadFrom reads the VarInt length (10) then fails to read the bytes -> Scan
	// error -> no-op.
	malformed := pk.Packet{ID: int32(packetid.ServerboundChatCommand), Data: []byte{10}}
	loop.dispatch(p.client, malformed)

	if called {
		t.Fatal("malformed ServerboundChatCommand reached Execute (must Scan-error to a no-op)")
	}
}

// TestChatCommandLengthBounded asserts an over-long command string (beyond maxCommandLen) is
// rejected BEFORE Execute: no unbounded parsing work runs (T-7-03 / ASVS V5). The wire decode
// succeeds (readUtf caps at 32767), but runCommand's length bound stops it short of Execute.
func TestChatCommandLengthBounded(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)

	called := false
	withExecuteCommand(t, func(ctx context.Context, cmd string) error {
		called = true
		return nil
	})

	long := strings.Repeat("a", maxCommandLen+1)
	loop.dispatch(p.client, chatCommandPacket(long))

	if called {
		t.Fatalf("an over-long command (%d > %d) reached Execute (must be length-bounded first)",
			len(long), maxCommandLen)
	}
}

// TestChatCommandParseErrorNoPanic asserts a command string Execute cannot parse returns an
// error that runCommand handles (v1: a documented no-op) and NEVER panics (Pattern 5
// 'Critical'). Driven through the real shared graph: "nope" is not a registered literal, so
// the fork dispatcher returns an error; dispatch must absorb it without panicking.
func TestChatCommandParseErrorNoPanic(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)

	// No seam swap: drive the REAL cmdGraph.Execute so the parse error is genuine. A panic
	// here fails the test (Go reports an unrecovered panic as a test failure).
	loop.dispatch(p.client, chatCommandPacket("nope notacommand"))
}

// TestRunConsoleCommand asserts runConsoleCommand routes a typed console line through the
// EXISTING command graph (executeCommand) exactly once with the line unaltered, under a ctx
// whose permission resolver GRANTS any node (the console IS the operator) AND whose
// executorFrom returns ok=false (NO *tickPlayer issuer — issuer-acting commands no-op). This
// is the TUI-01 "typed commands dispatch through the EXISTING command system" seam with no
// player reply (the reply goes to the slog log stream, not a SystemChat).
func TestRunConsoleCommand(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	var got string
	called := 0
	var grantsAll, hasIssuer bool
	withExecuteCommand(t, func(ctx context.Context, cmd string) error {
		called++
		got = cmd
		// The console resolver grants any node (operator). Probe an arbitrary node.
		grantsAll = permissionResolverFrom(ctx)("command.anything")
		// No executor is installed for a console source: issuer-acting commands (/tp) no-op.
		_, hasIssuer = executorFrom(ctx)
		return nil
	})

	loop.runConsoleCommand("say hi")

	if called != 1 {
		t.Fatalf("runConsoleCommand called executeCommand %d times, want 1", called)
	}
	if got != "say hi" {
		t.Fatalf("executeCommand received %q, want %q", got, "say hi")
	}
	if !grantsAll {
		t.Fatal("console permission resolver must GRANT any node (the console is the operator)")
	}
	if hasIssuer {
		t.Fatal("console ctx must have NO executor (executorFrom ok=false) so issuer-acting commands no-op")
	}
}

// TestRunConsoleCommandBounds asserts runConsoleCommand is a no-op for an empty line and for a
// line beyond maxCommandLen — executeCommand is NOT called (T-19-05 / ASVS V5: the same bound
// runCommand enforces, reused before any parsing work).
func TestRunConsoleCommandBounds(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	called := 0
	withExecuteCommand(t, func(ctx context.Context, cmd string) error {
		called++
		return nil
	})

	loop.runConsoleCommand("")                                  // empty: no-op
	loop.runConsoleCommand(strings.Repeat("a", maxCommandLen+1)) // over-long: no-op

	if called != 0 {
		t.Fatalf("runConsoleCommand reached executeCommand %d times for out-of-bounds input, want 0", called)
	}
}

// TestEnqueueConsoleCommandNonBlocking asserts EnqueueConsoleCommand is non-blocking
// drop-on-full (T-19-04 / Pitfall 7: the TUI goroutine NEVER executes inline and NEVER blocks
// the tick). Filling consoleCmd to capacity then a further Enqueue does NOT block; draining the
// channel through the tick's drainRegistrations delivers a queued line to runConsoleCommand on
// the OWNER goroutine (the line crosses to the tick via the bounded message channel — TICK-05).
func TestEnqueueConsoleCommandNonBlocking(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Fill the consoleCmd channel to capacity, then one more Enqueue must return immediately
	// (drop-on-full) rather than block. registerBuffer is the channel capacity.
	for i := 0; i < registerBuffer; i++ {
		loop.EnqueueConsoleCommand("filler")
	}
	done := make(chan struct{})
	go func() {
		loop.EnqueueConsoleCommand("overflow") // must NOT block on a full channel
		close(done)
	}()
	select {
	case <-done:
		// good: returned without blocking
	case <-time.After(time.Second):
		t.Fatal("EnqueueConsoleCommand blocked on a full consoleCmd channel (must drop-on-full)")
	}

	// A FRESH loop: enqueue one line and prove drainRegistrations delivers it to
	// runConsoleCommand on the owner goroutine (the tick drain executes the console line).
	loop2 := NewTickLoop(newFakeClock())
	var got string
	called := 0
	withExecuteCommand(t, func(ctx context.Context, cmd string) error {
		called++
		got = cmd
		return nil
	})
	loop2.EnqueueConsoleCommand("me waves")
	loop2.drainRegistrations() // owner-goroutine drain; the consoleCmd case runs the line

	if called != 1 {
		t.Fatalf("drainRegistrations ran the console line %d times, want 1", called)
	}
	if got != "me waves" {
		t.Fatalf("the drained console line reached Execute as %q, want %q", got, "me waves")
	}
}

// TestSayBroadcastsToAll asserts /say is no longer a no-op: through the REAL command graph,
// a console "say hi" broadcasts a ClientboundSystemChat carrying "[Server] hi" to every
// player, and a player "say hi" broadcasts "[<name>] hi". This is the vanilla SayCommand
// shape (ChatType.SAY_COMMAND = "[%s] %s") rendered via the v1 SystemChat path — closing the
// 07-05-deferred no-op that made the TUI console (and player /say) produce no visible output.
func TestSayBroadcastsToAll(t *testing.T) {
	// --- console source: name "Server" ---
	loop := NewTickLoop(newFakeClock())
	recipient := commandPlayer(loop)
	loop.runConsoleCommand("say hi")
	pkts := drainPackets(recipient.client)
	if !containsSystemChat(pkts, "[Server] hi") {
		t.Fatalf("console /say did not broadcast [Server] hi to the recipient; got %d packets", len(pkts))
	}

	// --- player source: name is the issuer's ---
	loop2 := NewTickLoop(newFakeClock())
	issuer := commandPlayer(loop2)
	issuer.name = "Steve"
	other := commandPlayer(loop2) // a second player who must also receive it
	loop2.runCommand(issuer, "say hello")
	got := drainPackets(other.client)
	if !containsSystemChat(got, "[Steve] hello") {
		t.Fatalf("player /say did not broadcast [Steve] hello to other players; got %d packets", len(got))
	}
}

// TestMeBroadcastsToAll asserts /me broadcasts the vanilla emote shape "* <name> <action>".
func TestMeBroadcastsToAll(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	r := commandPlayer(loop)
	loop.runConsoleCommand("me waves")
	if !containsSystemChat(drainPackets(r.client), "* Server waves") {
		t.Fatal("console /me did not broadcast '* Server waves'")
	}
}

// containsSystemChat reports whether any packet is a ClientboundSystemChat whose body carries
// text (the NBT Component encodes the string literally, so a byte-substring match suffices).
func containsSystemChat(pkts []pk.Packet, text string) bool {
	for _, p := range pkts {
		if p.ID == int32(packetid.ClientboundSystemChat) && strings.Contains(string(p.Data), text) {
			return true
		}
	}
	return false
}
