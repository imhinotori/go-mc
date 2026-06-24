package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// errTestCommand is a sentinel Execute error used to drive runCommand's reply path.
var errTestCommand = errors.New("test command failure")

// systemChatText decodes the FIRST ClientboundSystemChat packet in ps and returns its
// rendered Text (the chat.Message content). The wire is content (chat.Message NBT Component) +
// Boolean overlay (JAR-VERIFIED ClientboundSystemChatPacket field order).
func systemChatText(t *testing.T, ps []pk.Packet) string {
	t.Helper()
	for _, p := range ps {
		if p.ID != int32(packetid.ClientboundSystemChat) {
			continue
		}
		var msg chat.Message
		var overlay pk.Boolean
		if err := p.Scan(&msg, &overlay); err != nil {
			t.Fatalf("failed to decode ClientboundSystemChat content: %v", err)
		}
		return msg.Text
	}
	t.Fatal("no ClientboundSystemChat packet found")
	return ""
}

// systemChatContains reports whether the first SystemChat packet's rendered Text contains want.
func systemChatContains(t *testing.T, ps []pk.Packet, want string) bool {
	t.Helper()
	return strings.Contains(systemChatText(t, ps), want)
}

// chat_test.go covers CMD-02: receive + broadcast chat. A client's ServerboundChat is decoded
// DEFENSIVELY (the message kept, the signature/lastSeen ignored, a Scan error a no-op) and
// broadcast to every player as a server-attributed ClientboundSystemChat ("<name> message").
// The PlayerChat globalIndex layout is documented in chat.go (the literal CMD-02 ask) while v1
// ships SystemChat. The decode wire is JAR-VERIFIED (javap'd from temp/cache/26.2-inner.jar
// this session): ServerboundChatPacket = readUtf(256) message -> readInstant timeStamp ->
// readLong salt -> readNullable(MessageSignature) -> LastSeenMessages$Update.

// serverboundChatPacket builds a realistic ServerboundChat carrying the full jar field set:
// the message String, a timeStamp Long, a salt Long, a null-signature present-prefix
// (Boolean(false)), and an empty LastSeenMessages$Update (a VarInt(0) offset + no entries).
// handleChat decodes ONLY the leading message String, so the trailing fields are a superset
// the defensive decode tolerates — this proves the message is recovered from a well-formed
// packet that also carries the ignored signing fields.
func serverboundChatPacket(message string) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundChat),
		pk.String(message), // readUtf(256) message
		pk.Long(0),         // readInstant timeStamp (Long ms on the wire)
		pk.Long(0),         // readLong salt
		pk.Boolean(false),  // readNullable(MessageSignature): not present
		pk.VarInt(0),       // LastSeenMessages$Update: empty (offset 0, no entries)
	)
}

// chatPlayer registers a minimal *tickPlayer with a given name and a capturing client on the
// loop (mirrors commandPlayer in commands_test.go): appended to loop.players and indexed in
// loop.clientIndex so dispatch resolves it, with a real bounded captureClient so a test can
// drainPackets to assert the SystemChat fan-out. The name drives the "<name> message" format.
func chatPlayer(loop *TickLoop, name string) *tickPlayer {
	p := &tickPlayer{
		client:   captureClient(64),
		entityID: 3000 + int32(len(loop.players)),
		name:     name,
	}
	loop.players = append(loop.players, p)
	if loop.clientIndex == nil {
		loop.clientIndex = make(map[*Client]*tickPlayer)
	}
	loop.clientIndex[p.client] = p
	return p
}

// TestChatDecodeKeepsMessage asserts a well-formed ServerboundChat (message + timestamp +
// salt + null signature + empty lastSeen) decodes to the message string, with the
// signature/lastSeen ignored: the broadcast renders "<bob> hello" — i.e. the recovered
// message is "hello" attributed to the sender, not any client-supplied field.
func TestChatDecodeKeepsMessage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	loop.handleChat(bob, serverboundChatPacket("hello"))

	got := drainPackets(bob.client)
	if countID(got, packetid.ClientboundSystemChat) != 1 {
		t.Fatalf("handleChat produced %d ClientboundSystemChat packets, want 1",
			countID(got, packetid.ClientboundSystemChat))
	}
	if !systemChatContains(t, got, "<bob> hello") {
		t.Fatalf("broadcast did not render the decoded message %q (the signature/lastSeen must be ignored)", "<bob> hello")
	}
}

// TestSystemChatBroadcast asserts broadcastSystemChat fans a ClientboundSystemChat (content +
// overlay=false) to EVERY player in the tick-owned player set — each captured client receives
// exactly one SystemChat packet carrying the text.
func TestSystemChatBroadcast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	a := chatPlayer(loop, "alice")
	b := chatPlayer(loop, "bob")
	c := chatPlayer(loop, "carol")

	loop.broadcastSystemChat("<bob> hi")

	for _, p := range []*tickPlayer{a, b, c} {
		got := drainPackets(p.client)
		if countID(got, packetid.ClientboundSystemChat) != 1 {
			t.Fatalf("player %q received %d ClientboundSystemChat packets, want 1",
				p.name, countID(got, packetid.ClientboundSystemChat))
		}
		if !systemChatContains(t, got, "<bob> hi") {
			t.Fatalf("player %q did not receive the broadcast text", p.name)
		}
	}
}

// TestChatMalformed asserts a malformed ServerboundChat payload Scan-errors to a no-op: no
// broadcast, no panic (T-7-06 / T-3-02). A truncated VarInt-length-prefixed String body fails
// the leading-String Scan.
func TestChatMalformed(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	// A ServerboundChat id with a body claiming a 10-byte String but supplying none:
	// pk.String.ReadFrom reads the VarInt length (10) then fails to read the bytes -> Scan
	// error -> no-op (never panics).
	malformed := pk.Packet{ID: int32(packetid.ServerboundChat), Data: []byte{10}}
	loop.handleChat(bob, malformed)

	got := drainPackets(bob.client)
	if countID(got, packetid.ClientboundSystemChat) != 0 {
		t.Fatalf("a malformed ServerboundChat produced %d broadcasts, want 0 (must Scan-error to a no-op)",
			countID(got, packetid.ClientboundSystemChat))
	}
}

// TestChatLengthBounded asserts an over-long chat message is bounded by maxChatLen: the
// broadcast text never exceeds the "<name> " prefix + maxChatLen body, so a hostile oversized
// message does no unbounded work (T-7-06 / ASVS V5).
func TestChatLengthBounded(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	long := strings.Repeat("a", maxChatLen+500)
	loop.handleChat(bob, serverboundChatPacket(long))

	got := drainPackets(bob.client)
	if countID(got, packetid.ClientboundSystemChat) != 1 {
		t.Fatalf("an over-long message produced %d broadcasts, want 1", countID(got, packetid.ClientboundSystemChat))
	}
	text := systemChatText(t, got)
	// The body (after the "<bob> " prefix) must be capped at maxChatLen.
	prefix := "<bob> "
	if !strings.HasPrefix(text, prefix) {
		t.Fatalf("broadcast %q missing the %q prefix", text, prefix)
	}
	body := strings.TrimPrefix(text, prefix)
	if len(body) > maxChatLen {
		t.Fatalf("broadcast body length %d exceeds the maxChatLen bound %d", len(body), maxChatLen)
	}
}

// --- Task 2: dispatch routing + name threading + command reply consolidation ---

// TestChatRouted asserts dispatch routes a ServerboundChat INLINE to handleChat (it was NOT
// routed before — it fell to the default no-op): every player receives the SystemChat.
func TestChatRouted(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	a := chatPlayer(loop, "alice")
	b := chatPlayer(loop, "bob")

	// alice types; both alice and bob must receive the broadcast.
	loop.dispatch(a.client, serverboundChatPacket("yo"))

	for _, p := range []*tickPlayer{a, b} {
		got := drainPackets(p.client)
		if countID(got, packetid.ClientboundSystemChat) != 1 {
			t.Fatalf("after dispatch, player %q received %d SystemChat packets, want 1 (ServerboundChat must route to handleChat)",
				p.name, countID(got, packetid.ClientboundSystemChat))
		}
	}
}

// TestChatRendersName asserts the broadcast renders the vanilla "<name> message" format from
// the tickPlayer.name threaded at registration: a sender named "bob" produces "<bob> ...".
func TestChatRendersName(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	loop.dispatch(bob.client, serverboundChatPacket("hi there"))

	got := drainPackets(bob.client)
	if !systemChatContains(t, got, "<bob> hi there") {
		t.Fatalf("broadcast did not render the vanilla %q format", "<bob> hi there")
	}
}

// TestCommandReplyViaSystemChat asserts runCommand delivers a command result to the player via
// sendSystemChat (consolidating 07-04's stubbed reply): an Execute error produces a SystemChat
// reply to the issuing player.
func TestCommandReplyViaSystemChat(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	// Force an Execute error so runCommand takes the reply path.
	withExecuteCommand(t, func(ctx context.Context, cmd string) error {
		return errTestCommand
	})

	loop.runCommand(bob, "say boom")

	got := drainPackets(bob.client)
	if countID(got, packetid.ClientboundSystemChat) != 1 {
		t.Fatalf("a failed command produced %d SystemChat replies, want 1 (the 07-04 reply stub must be consolidated)",
			countID(got, packetid.ClientboundSystemChat))
	}
}

// TestChatAckIgnored asserts a ServerboundChatAck / ServerboundChatSessionUpdate is a
// decode-and-ignore no-op: no broadcast, no panic.
func TestChatAckIgnored(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	loop.dispatch(bob.client, pk.Marshal(int32(packetid.ServerboundChatAck), pk.VarInt(0)))
	loop.dispatch(bob.client, pk.Marshal(int32(packetid.ServerboundChatSessionUpdate)))

	got := drainPackets(bob.client)
	if countID(got, packetid.ClientboundSystemChat) != 0 {
		t.Fatalf("a chat ack/session-update produced %d broadcasts, want 0 (must be a no-op)",
			countID(got, packetid.ClientboundSystemChat))
	}
}
