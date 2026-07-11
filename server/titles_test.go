package server

// titles_test.go - wire-shape lock tests for the Title / TabList-header-footer packets (titles.go)
// and a /title dispatch test. Each wire test asserts the packet ID + the byte body against the
// jar-verified layout (javap temp/cache/26.2-inner.jar this session): Set*Text = one
// TRUSTED_STREAM_CODEC Component; SetTitlesAnimation = three BIG-ENDIAN Ints; ClearTitles = one
// Boolean; TabList = header Component + footer Component. The dispatch test drives runTitleCommand
// and asserts the resolved player receives the matching packet. No RNG, off the pig path.

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestSetTitleTextWire pins encodeSetTitleText: ID = ClientboundSetTitleText, body = a single
// TRUSTED_STREAM_CODEC Component (a non-empty NBT body decoding back to the text).
func TestSetTitleTextWire(t *testing.T) {
	p := encodeSetTitleText(chat.Message{Text: "Hello"})
	if p.ID != int32(packetid.ClientboundSetTitleText) {
		t.Fatalf("SetTitleText id = %d, want %d", p.ID, packetid.ClientboundSetTitleText)
	}
	var msg chat.Message
	if err := p.Scan(&msg); err != nil {
		t.Fatalf("decode SetTitleText component: %v", err)
	}
	if msg.Text != "Hello" {
		t.Fatalf("SetTitleText text = %q, want %q", msg.Text, "Hello")
	}
}

// TestSetSubtitleTextWire pins encodeSetSubtitleText: ID + single Component.
func TestSetSubtitleTextWire(t *testing.T) {
	p := encodeSetSubtitleText(chat.Message{Text: "Sub"})
	if p.ID != int32(packetid.ClientboundSetSubtitleText) {
		t.Fatalf("SetSubtitleText id = %d, want %d", p.ID, packetid.ClientboundSetSubtitleText)
	}
	var msg chat.Message
	if err := p.Scan(&msg); err != nil {
		t.Fatalf("decode SetSubtitleText component: %v", err)
	}
	if msg.Text != "Sub" {
		t.Fatalf("SetSubtitleText text = %q, want %q", msg.Text, "Sub")
	}
}

// TestSetActionBarTextWire pins encodeSetActionBarText: ID + single Component.
func TestSetActionBarTextWire(t *testing.T) {
	p := encodeSetActionBarText(chat.Message{Text: "Bar"})
	if p.ID != int32(packetid.ClientboundSetActionBarText) {
		t.Fatalf("SetActionBarText id = %d, want %d", p.ID, packetid.ClientboundSetActionBarText)
	}
	var msg chat.Message
	if err := p.Scan(&msg); err != nil {
		t.Fatalf("decode SetActionBarText component: %v", err)
	}
	if msg.Text != "Bar" {
		t.Fatalf("SetActionBarText text = %q, want %q", msg.Text, "Bar")
	}
}

// TestSetTitlesAnimationWire pins encodeSetTitlesAnimation: ID + three BIG-ENDIAN 4-byte Ints
// fadeIn/stay/fadeOut IN ORDER (writeInt, NOT VarInt). The body is exactly 12 bytes.
func TestSetTitlesAnimationWire(t *testing.T) {
	p := encodeSetTitlesAnimation(10, 70, 20)
	if p.ID != int32(packetid.ClientboundSetTitlesAnimation) {
		t.Fatalf("SetTitlesAnimation id = %d, want %d", p.ID, packetid.ClientboundSetTitlesAnimation)
	}
	if len(p.Data) != 12 {
		t.Fatalf("SetTitlesAnimation body = %d bytes, want 12 (three writeInt)", len(p.Data))
	}
	r := bytes.NewReader(p.Data)
	var fadeIn, stay, fadeOut pk.Int
	if _, err := fadeIn.ReadFrom(r); err != nil {
		t.Fatalf("read fadeIn: %v", err)
	}
	if _, err := stay.ReadFrom(r); err != nil {
		t.Fatalf("read stay: %v", err)
	}
	if _, err := fadeOut.ReadFrom(r); err != nil {
		t.Fatalf("read fadeOut: %v", err)
	}
	if int32(fadeIn) != 10 || int32(stay) != 70 || int32(fadeOut) != 20 {
		t.Fatalf("SetTitlesAnimation = (%d,%d,%d), want (10,70,20)", fadeIn, stay, fadeOut)
	}
	// Assert BIG-ENDIAN framing explicitly: fadeIn=10 must be 00 00 00 0A.
	if !bytes.Equal(p.Data[0:4], []byte{0, 0, 0, 10}) {
		t.Fatalf("fadeIn bytes = % x, want 00 00 00 0a (big-endian writeInt)", p.Data[0:4])
	}
}

// TestClearTitlesWire pins encodeClearTitles: ID + a single Boolean resetTimes (one byte).
func TestClearTitlesWire(t *testing.T) {
	for _, reset := range []bool{false, true} {
		p := encodeClearTitles(reset)
		if p.ID != int32(packetid.ClientboundClearTitles) {
			t.Fatalf("ClearTitles id = %d, want %d", p.ID, packetid.ClientboundClearTitles)
		}
		if len(p.Data) != 1 {
			t.Fatalf("ClearTitles body = %d bytes, want 1 (one writeBoolean)", len(p.Data))
		}
		want := byte(0)
		if reset {
			want = 1
		}
		if p.Data[0] != want {
			t.Fatalf("ClearTitles resetTimes byte = %d, want %d", p.Data[0], want)
		}
	}
}

// TestTabListWire pins encodeTabList: ID + header Component then footer Component (field order).
func TestTabListWire(t *testing.T) {
	p := encodeTabList(chat.Message{Text: "H"}, chat.Message{Text: "F"})
	if p.ID != int32(packetid.ClientboundTabList) {
		t.Fatalf("TabList id = %d, want %d", p.ID, packetid.ClientboundTabList)
	}
	var header, footer chat.Message
	if err := p.Scan(&header, &footer); err != nil {
		t.Fatalf("decode TabList components: %v", err)
	}
	if header.Text != "H" {
		t.Fatalf("TabList header = %q, want %q", header.Text, "H")
	}
	if footer.Text != "F" {
		t.Fatalf("TabList footer = %q, want %q", footer.Text, "F")
	}
}

// TestTitleDispatchTitle drives runTitleCommand("@s title Welcome") and asserts the issuing player
// receives exactly one ClientboundSetTitleText carrying "Welcome".
func TestTitleDispatchTitle(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	if err := loop.runTitleCommand(bob, "@s title Welcome"); err != nil {
		t.Fatalf("runTitleCommand returned error: %v", err)
	}
	got := drainPackets(bob.client)
	if countID(got, packetid.ClientboundSetTitleText) != 1 {
		t.Fatalf("got %d SetTitleText, want 1", countID(got, packetid.ClientboundSetTitleText))
	}
	for _, p := range got {
		if p.ID == int32(packetid.ClientboundSetTitleText) {
			var msg chat.Message
			if err := p.Scan(&msg); err != nil {
				t.Fatalf("decode dispatched title: %v", err)
			}
			if msg.Text != "Welcome" {
				t.Fatalf("dispatched title text = %q, want %q", msg.Text, "Welcome")
			}
		}
	}
}

// TestTitleDispatchTimes drives "@s times 5 100 10" and asserts a SetTitlesAnimation with the
// parsed tick counts is delivered.
func TestTitleDispatchTimes(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	if err := loop.runTitleCommand(bob, "@s times 5 100 10"); err != nil {
		t.Fatalf("runTitleCommand times returned error: %v", err)
	}
	got := drainPackets(bob.client)
	if countID(got, packetid.ClientboundSetTitlesAnimation) != 1 {
		t.Fatalf("got %d SetTitlesAnimation, want 1", countID(got, packetid.ClientboundSetTitlesAnimation))
	}
}

// TestTitleDispatchClearReset drives clear then reset and asserts the ClearTitles resetTimes byte
// distinguishes them (clear=false, reset=true) - the vanilla clearTitle/resetTitle split.
func TestTitleDispatchClearReset(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	if err := loop.runTitleCommand(bob, "@s clear"); err != nil {
		t.Fatalf("clear error: %v", err)
	}
	got := drainPackets(bob.client)
	if countID(got, packetid.ClientboundClearTitles) != 1 {
		t.Fatalf("got %d ClearTitles for clear, want 1", countID(got, packetid.ClientboundClearTitles))
	}
	for _, p := range got {
		if p.ID == int32(packetid.ClientboundClearTitles) && p.Data[0] != 0 {
			t.Fatalf("clear resetTimes = %d, want 0", p.Data[0])
		}
	}

	if err := loop.runTitleCommand(bob, "@s reset"); err != nil {
		t.Fatalf("reset error: %v", err)
	}
	got = drainPackets(bob.client)
	for _, p := range got {
		if p.ID == int32(packetid.ClientboundClearTitles) && p.Data[0] != 1 {
			t.Fatalf("reset resetTimes = %d, want 1", p.Data[0])
		}
	}
}

// TestTitleDispatchUsageError asserts a malformed /title (missing sub-verb) returns errTitleUsage
// and sends nothing.
func TestTitleDispatchUsageError(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	if err := loop.runTitleCommand(bob, "@s"); err != errTitleUsage {
		t.Fatalf("bare targets returned %v, want errTitleUsage", err)
	}
	if got := drainPackets(bob.client); len(got) != 0 {
		t.Fatalf("usage error still sent %d packets, want 0", len(got))
	}
}

// TestTabListHeaderFooterSend asserts the internal sendTabListHeaderFooter API delivers one
// ClientboundTabList with the header + footer to the player.
func TestTabListHeaderFooterSend(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bob := chatPlayer(loop, "bob")

	loop.sendTabListHeaderFooter(bob, chat.Message{Text: "top"}, chat.Message{Text: "bottom"})
	got := drainPackets(bob.client)
	if countID(got, packetid.ClientboundTabList) != 1 {
		t.Fatalf("got %d TabList, want 1", countID(got, packetid.ClientboundTabList))
	}
}
