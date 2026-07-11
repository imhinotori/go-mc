package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// commands_suggest_test.go -- Part B tab-complete round trip: a ServerboundCommandSuggestion
// produces a ClientboundCommandSuggestions whose wire matches the jar codec (id, start, length,
// list<Entry>), and the candidates come from the live command graph + server resolver.

// suggestionRequest builds a ServerboundCommandSuggestion (VarInt id + Utf command).
func suggestionRequest(id int32, cmd string) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundCommandSuggestion), pk.VarInt(id), pk.String(cmd))
}

// scanSuggestions decodes a ClientboundCommandSuggestions into (id, start, length, texts).
func scanSuggestions(t *testing.T, p pk.Packet) (id, start, length int32, texts []string) {
	t.Helper()
	var vid, vstart, vlen, count pk.VarInt
	buf := bytes.NewReader(p.Data)
	rd := func(f pk.FieldDecoder) {
		if _, err := f.ReadFrom(buf); err != nil {
			t.Fatalf("decode suggestions: %v", err)
		}
	}
	rd(&vid)
	rd(&vstart)
	rd(&vlen)
	rd(&count)
	for i := 0; i < int(count); i++ {
		var s pk.String
		var has pk.Boolean
		rd(&s)
		rd(&has)
		if bool(has) {
			t.Fatalf("entry %d carried a tooltip; server sends none", i)
		}
		texts = append(texts, string(s))
	}
	return int32(vid), int32(vstart), int32(vlen), texts
}

// TestCommandSuggestionRoundTrip: "/gamemode " asks the server, which returns the 4 gamemode
// literals? No -- gamemode is a client-local domain; our arg has SuggestPlayers, so it returns the
// online player names. The reply wire is exactly id/start/length + the player entries.
func TestCommandSuggestionRoundTrip(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	p.name = "Steve"
	other := commandPlayer(loop)
	other.name = "Alex"

	loop.runCommandSuggestion(p, suggestionRequest(7, "/gamemode creative "))

	pkts := drainPackets(p.client)
	if countID(pkts, packetid.ClientboundCommandSuggestions) != 1 {
		t.Fatalf("want 1 ClientboundCommandSuggestions, got %d", countID(pkts, packetid.ClientboundCommandSuggestions))
	}
	var reply pk.Packet
	for _, pk2 := range pkts {
		if pk2.ID == int32(packetid.ClientboundCommandSuggestions) {
			reply = pk2
		}
	}
	id, start, length, texts := scanSuggestions(t, reply)
	if id != 7 {
		t.Fatalf("echoed id = %d, want 7", id)
	}
	// "/gamemode " -> the token starts right after the trailing space (offset 10 including '/').
	if start != 19 || length != 0 {
		t.Fatalf("start=%d length=%d, want start=19 length=0", start, length)
	}
	if !containsAll(texts, []string{"Steve", "Alex"}) {
		t.Fatalf("player suggestions = %v, want Steve+Alex", texts)
	}
}

// TestCommandSuggestionLiteralFrontier: "/ga" completes to the "gamemode" literal.
func TestCommandSuggestionLiteralFrontier(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	loop.runCommandSuggestion(p, suggestionRequest(1, "/ga"))
	pkts := drainPackets(p.client)
	var reply pk.Packet
	for _, pk2 := range pkts {
		if pk2.ID == int32(packetid.ClientboundCommandSuggestions) {
			reply = pk2
		}
	}
	if reply.ID == 0 {
		t.Fatal("no suggestions reply")
	}
	_, start, _, texts := scanSuggestions(t, reply)
	if start != 1 {
		t.Fatalf("start = %d, want 1 (token begins after '/')", start)
	}
	if !containsAll(texts, []string{"gamemode"}) {
		t.Fatalf("literal frontier = %v, want gamemode", texts)
	}
}

// TestCommandSuggestionMalformed: a malformed request is a silent no-op (no reply, no panic).
func TestCommandSuggestionMalformed(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	loop.runCommandSuggestion(p, pk.Packet{ID: int32(packetid.ServerboundCommandSuggestion), Data: []byte{200}})
	if len(drainPackets(p.client)) != 0 {
		t.Fatal("malformed suggestion produced a reply (must no-op)")
	}
}

func containsAll(got, want []string) bool {
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
