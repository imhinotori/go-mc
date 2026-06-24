package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// chat_command_capture_test.go is the AUTHORITATIVE CMD-01/02 byte-diff (Plan 07-06): it
// loads golden packet bodies captured from a REAL vanilla 26.2 server/client (booted from
// temp/cache/26.2-server.jar, superflat, offline, port 25599; driven over RCON) and asserts
// Sulfur's command/chat encoders produce the SAME wire framing — AND that Sulfur's chat
// DECODER consumes a real vanilla ServerboundChat (the trailing signature/lastSeen layout)
// without mis-framing.
//
// A Go self-round-trip cannot prove vanilla-correctness for a protocol no published spec
// covers (the wiki documents <=773; the ClientboundCommands node-tree framing, the
// ClientboundSystemChat content+overlay, and the ServerboundChat readUtf256/instant/salt/
// nullable-sig/lastSeen trailing layout are jar-confirmed in SHAPE but their exact bytes
// drift). This is the producer-side proof; the real-client interactive run
// (07-CAPTURE-DIFF.md Task 2) is the consumer-side proof.
//
// The fixtures live under the phase dir so CI byte-diffs without booting Java every run.
// Each subtest SKIPS with a clear message if its fixture is absent (the capture is the
// gate, not a native-CI blocker). The reproducible capture method is recorded in
// .planning/phases/07-ai-pathfinding-commands-chat/07-CAPTURE-DIFF.md.
//
// LOAD-BEARING ASSERTION POLICY: vanilla's join ClientboundCommands carries the FULL vanilla
// command set (26 nodes); Sulfur's v1 graph carries the 2 registered literals (/say, /me) as
// 5 nodes. We therefore assert the FRAMING that causes a client to reject the command tree —
// the VarInt node-count prefix, the per-node (flags, child-index array, name, parser-id)
// layout, and the trailing VarInt root-index — NOT byte-equality of the command SET that
// legitimately differs. For the ServerboundChat we assert Sulfur's handleChat decoder recovers
// the message AND that a full client-style walk of the captured bytes (incl. the trailing
// instant/salt/nullable-sig/lastSeen-with-checksum) consumes to exactly zero trailing bytes —
// the captured bytes are a real vanilla client's wire (the vanilla server ACCEPTED them; the
// trailing-layout fix — the LastSeenMessages$Update checksum byte — was applied during capture).

const chatCmdFixtureDir = "../.planning/phases/07-ai-pathfinding-commands-chat/fixtures"

// loadChatCmdFixture reads a golden vanilla packet body, or skips the subtest if absent.
func loadChatCmdFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join(chatCmdFixtureDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("golden fixture %s absent (%v) — see 07-CAPTURE-DIFF.md to re-capture from the vanilla 26.2 jar", path, err)
	}
	return data
}

// command-node flag bits (jar: ClientboundCommandsPacket node stub flags). The low 2 bits are
// the node kind; the higher bits are the executable / redirect / suggestions markers.
const (
	nodeKindMask          = 0x03
	nodeKindRoot          = 0
	nodeKindLiteral       = 1
	nodeKindArgument      = 2
	nodeFlagExecutable    = 1 << 2
	nodeFlagHasRedirect   = 1 << 3
	nodeFlagHasSuggestion = 1 << 4
)

// walkCommandGraphBody parses a ClientboundCommands body as a 26.2 client decoder would:
// VarInt nodeCount, then nodeCount × node stubs, then the trailing VarInt rootIndex — and
// asserts it consumes to exactly zero trailing bytes. It returns the parsed node count and the
// root index. The per-node layout (jar-verified, mirrored by server/command/serialize.go):
//
//	Byte   flags                    (kind in low 2 bits + executable/redirect/suggestion)
//	VarInt childCount, childCount × VarInt childIndex
//	if hasRedirect:  VarInt redirectNode
//	if literal|argument:  String name
//	if argument:  VarInt parserId + parser properties (brigadier:string => VarInt behavior)
//	if hasSuggestion:  Identifier suggestionType
func walkCommandGraphBody(t *testing.T, body []byte) (nodeCount, rootIndex int) {
	t.Helper()
	r := bytes.NewReader(body)
	read := func(f pk.FieldDecoder, what string) {
		t.Helper()
		if _, err := f.ReadFrom(r); err != nil {
			t.Fatalf("command graph %s decode: %v", what, err)
		}
	}

	var count pk.VarInt
	read(&count, "node count")
	if count <= 0 {
		t.Fatalf("command graph node count = %d, want > 0 (at least the root)", count)
	}

	for i := int32(0); i < int32(count); i++ {
		var flags pk.Byte
		read(&flags, "node flags")
		f := byte(flags)
		kind := f & nodeKindMask
		if kind > nodeKindArgument {
			t.Fatalf("node %d has invalid kind %d (low 2 bits of flags 0x%02x)", i, kind, f)
		}
		var childCount pk.VarInt
		read(&childCount, "child count")
		for c := int32(0); c < int32(childCount); c++ {
			var childIdx pk.VarInt
			read(&childIdx, "child index")
		}
		if f&nodeFlagHasRedirect != 0 {
			var redirect pk.VarInt
			read(&redirect, "redirect node")
		}
		if kind == nodeKindLiteral || kind == nodeKindArgument {
			var name pk.String
			read(&name, "node name")
		}
		if kind == nodeKindArgument {
			// Parser: an Identifier (the parser id, e.g. "brigadier:string") then parser
			// properties. The captured vanilla graph and Sulfur both use brigadier:string for
			// the message/action args (a single VarInt behavior). Other vanilla parsers carry
			// their own property encodings; we read the parser id Identifier and then the
			// brigadier:string behavior VarInt for those, and tolerate any other parser by
			// consuming the rest of THIS node's known-width fields. To stay robust across the
			// full vanilla parser set, we parse only the parser id Identifier here and let the
			// node boundary be re-established by the next node's flags — but since arbitrary
			// parser properties are variable-width, we instead assert the Sulfur-relevant
			// brigadier:string nodes precisely and accept that the vanilla graph's exotic
			// parsers are validated by the zero-trailing-bytes whole-body check below only for
			// graphs composed of brigadier:string args (Sulfur's). For the vanilla golden we
			// stop strict per-node parsing once we hit a non-brigadier:string parser.
			var parserID pk.Identifier
			read(&parserID, "parser id")
			switch string(parserID) {
			case "brigadier:string":
				var behavior pk.VarInt
				read(&behavior, "brigadier:string behavior")
			default:
				// A non-brigadier:string parser carries parser-specific properties of unknown
				// width. We cannot generically walk every vanilla parser, so we record that the
				// node framing up to the parser id is correct and stop the strict walk here.
				// The Sulfur graph (the encoder under test) uses ONLY brigadier:string, so its
				// walk always completes to zero trailing bytes (asserted below).
				return int(count), -1
			}
		}
		if f&nodeFlagHasSuggestion != 0 {
			var suggest pk.Identifier
			read(&suggest, "suggestion type")
		}
	}

	var root pk.VarInt
	read(&root, "root index")
	if r.Len() != 0 {
		t.Fatalf("command graph body has %d trailing bytes — node-tree framing mismatch", r.Len())
	}
	return int(count), int(root)
}

// TestChatCommandBytesVsVanillaCapture is the authoritative CMD-01/02 byte-diff: it asserts
// Sulfur's ClientboundCommands node framing matches vanilla, that Sulfur's SystemChat
// content+overlay framing matches vanilla, and that Sulfur's handleChat decoder consumes a
// real vanilla ServerboundChat (the trailing signature/lastSeen layout) without mis-framing.
func TestChatCommandBytesVsVanillaCapture(t *testing.T) {
	t.Run("ClientboundCommands", func(t *testing.T) {
		golden := loadChatCmdFixture(t, "vanilla-commands.bin")

		// 1. The vanilla golden parses as a client would: VarInt node count, the node stubs,
		//    then the trailing VarInt root index. Vanilla's join graph carries the full command
		//    set (26 nodes). Its exotic parsers (entity, score-holder, …) carry variable-width
		//    properties we don't exhaustively walk, but the leading node-count prefix and the
		//    first nodes' framing (root + brigadier:string-arg literals) are the load-bearing
		//    shape Sulfur shares — a wrong node-count width or a wrong flag layout rejects the tree.
		vCount, _ := walkCommandGraphBody(t, golden)
		if vCount < 2 {
			t.Fatalf("vanilla ClientboundCommands node count = %d, want >= 2 (root + commands)", vCount)
		}
		// The leading byte after the count is the ROOT node's flags: kind == root (low 2 bits 0),
		// not executable. This pins the node framing at offset 0.
		rRoot := bytes.NewReader(golden)
		var vNodes pk.VarInt
		if _, err := vNodes.ReadFrom(rRoot); err != nil {
			t.Fatalf("vanilla node count decode: %v", err)
		}
		var rootFlags pk.Byte
		if _, err := rootFlags.ReadFrom(rRoot); err != nil {
			t.Fatalf("vanilla root flags decode: %v", err)
		}
		if byte(rootFlags)&nodeKindMask != nodeKindRoot {
			t.Fatalf("vanilla first node kind = %d, want %d (root)", byte(rootFlags)&nodeKindMask, nodeKindRoot)
		}

		// 2. Sulfur's buildCommandGraph().WriteTo produces a STRUCTURALLY-matching node tree:
		//    the same VarInt-count prefix, the same per-node (flags, child-array, name, parser)
		//    framing, and the same trailing VarInt root index — walking to exactly zero trailing
		//    bytes. Sulfur's graph uses ONLY brigadier:string args, so the strict walk completes.
		var sulBuf bytes.Buffer
		if _, err := buildCommandGraph().WriteTo(&sulBuf); err != nil {
			t.Fatalf("Sulfur graph WriteTo: %v", err)
		}
		sulBody := sulBuf.Bytes()
		sCount, sRoot := walkCommandGraphBody(t, sulBody)
		if sRoot != 0 {
			t.Fatalf("Sulfur ClientboundCommands root index = %d, want 0 (root is node 0, matching vanilla)", sRoot)
		}
		if sCount < 2 {
			t.Fatalf("Sulfur ClientboundCommands node count = %d, want >= 2 (root + at least one command)", sCount)
		}
		// Sulfur's first node must ALSO be the root (kind 0), the same offset-0 framing as vanilla.
		rSul := bytes.NewReader(sulBody)
		var sNodes pk.VarInt
		_, _ = sNodes.ReadFrom(rSul)
		var sRootFlags pk.Byte
		_, _ = sRootFlags.ReadFrom(rSul)
		if byte(sRootFlags)&nodeKindMask != nodeKindRoot {
			t.Fatalf("Sulfur first node kind = %d, want %d (root) — node framing diverges from vanilla", byte(sRootFlags)&nodeKindMask, nodeKindRoot)
		}

		// 3. The graph is sent via ClientboundCommands at join with the SAME packet id the
		//    vanilla server uses — the adapter wraps WriteTo under packetid.ClientboundCommands.
		c := captureClient(8)
		buildCommandGraph().ClientJoin(commandClientAdapter{c})
		got := drainPackets(c)
		if countID(got, packetid.ClientboundCommands) != 1 {
			t.Fatalf("ClientJoin sent %d ClientboundCommands packets, want 1", countID(got, packetid.ClientboundCommands))
		}
		// And the sent packet body is exactly the WriteTo body (the framing diffed above).
		for _, p := range got {
			if p.ID == int32(packetid.ClientboundCommands) {
				if !bytes.Equal(p.Data, sulBody) {
					t.Fatalf("ClientboundCommands packet body != graph WriteTo body\n  packet: %x\n  write:  %x", p.Data, sulBody)
				}
			}
		}
	})

	t.Run("ClientboundSystemChat", func(t *testing.T) {
		golden := loadChatCmdFixture(t, "vanilla-system-chat.bin")

		// 1. The vanilla golden is a ClientboundSystemChat = Component content (chat.Message NBT)
		//    + Boolean overlay. Walk it as a client does to exactly zero trailing bytes.
		r := bytes.NewReader(golden)
		var vMsg chat.Message
		var vOverlay pk.Boolean
		if _, err := vMsg.ReadFrom(r); err != nil {
			t.Fatalf("vanilla SystemChat content (NBT Component) decode: %v", err)
		}
		if _, err := vOverlay.ReadFrom(r); err != nil {
			t.Fatalf("vanilla SystemChat overlay decode: %v", err)
		}
		if r.Len() != 0 {
			t.Fatalf("vanilla SystemChat body has %d trailing bytes — content+overlay framing mismatch", r.Len())
		}
		// The vanilla /tellraw text is the captured marker; overlay is false (the chat box, not
		// the action-bar). These pin the two-field record shape Sulfur must reproduce.
		vText := vMsg.ClearString()
		if vText == "" {
			t.Fatalf("vanilla SystemChat decoded to empty text (content framing wrong): %x", golden)
		}
		if bool(vOverlay) {
			t.Fatalf("vanilla SystemChat overlay = true, want false (chat box)")
		}

		// 2. Sulfur's broadcastSystemChat marshals chat.Message{Text} + Boolean(false) under
		//    packetid.ClientboundSystemChat — the SAME two-field content+overlay framing. Feed it
		//    the SAME text as the vanilla golden and assert the marshalled body is BYTE-IDENTICAL.
		loop := NewTickLoop(newFakeClock())
		sysPlayer := chatPlayer(loop, "cap")
		loop.broadcastSystemChat(vText)
		got := drainPackets(sysPlayer.client)
		if countID(got, packetid.ClientboundSystemChat) != 1 {
			t.Fatalf("broadcastSystemChat produced %d SystemChat packets, want 1", countID(got, packetid.ClientboundSystemChat))
		}
		var sulBody []byte
		for _, p := range got {
			if p.ID == int32(packetid.ClientboundSystemChat) {
				sulBody = p.Data
			}
		}
		// With the SAME text content and overlay=false, Sulfur's SystemChat body is
		// byte-identical to the vanilla golden: the NBT Component content encoding (TAG_String
		// network-format body) + the trailing Boolean(false) match vanilla exactly.
		if !bytes.Equal(sulBody, golden) {
			t.Fatalf("Sulfur SystemChat body != vanilla golden for identical text %q\n  sulfur:  %x\n  vanilla: %x", vText, sulBody, golden)
		}
	})

	t.Run("ServerboundChatDecode", func(t *testing.T) {
		golden := loadChatCmdFixture(t, "vanilla-serverbound-chat.bin")

		// The vanilla golden is the SERVERBOUND ServerboundChat body the vanilla 26.2 wire form
		// carries — message String + Instant(Long) timeStamp + Long salt + nullable
		// MessageSignature + LastSeenMessages$Update (VarInt offset + FixedBitSet(20)=3 bytes +
		// Byte checksum). The vanilla server ACCEPTED these exact bytes (no decoder kick) when
		// the capture harness sent them — so this is the real, vanilla-valid trailing layout.

		// 1. Sulfur's handleChat DECODER consumes the golden WITHOUT mis-framing and recovers
		//    the leading message. handleChat reads ONLY the leading message String and ignores
		//    the trailing fields (server-authoritative, offline), so it must broadcast the
		//    recovered message attributed to the sender.
		loop := NewTickLoop(newFakeClock())
		bob := chatPlayer(loop, "bob")
		raw := pk.Packet{ID: int32(packetid.ServerboundChat), Data: golden}
		loop.handleChat(bob, raw)
		got := drainPackets(bob.client)
		if countID(got, packetid.ClientboundSystemChat) != 1 {
			t.Fatalf("handleChat over the real vanilla ServerboundChat produced %d broadcasts, want 1 (the leading message must decode cleanly)", countID(got, packetid.ClientboundSystemChat))
		}
		// The captured message text is "sulfur capture chat"; the broadcast renders "<bob> sulfur capture chat".
		if !systemChatContains(t, got, "<bob> sulfur capture chat") {
			t.Fatalf("handleChat did not recover the vanilla message from the captured bytes; broadcast = %q", systemChatText(t, got))
		}

		// 2. A FULL client-style walk of the captured bytes (the entire jar-verified trailing
		//    layout) consumes to EXACTLY zero trailing bytes — proving the trailing
		//    signature/lastSeen-with-checksum layout Sulfur documents is the real vanilla wire
		//    (a wrong width here would leave bytes over, which is exactly what mis-frames the
		//    NEXT packet in the stream — the A6 / T-7-14 risk).
		r := bytes.NewReader(golden)
		read := func(f pk.FieldDecoder, what string) {
			t.Helper()
			if _, err := f.ReadFrom(r); err != nil {
				t.Fatalf("vanilla ServerboundChat %s decode: %v", what, err)
			}
		}
		var msg pk.String
		read(&msg, "message")
		var timeStamp pk.Long
		read(&timeStamp, "timeStamp (Instant)")
		var salt pk.Long
		read(&salt, "salt")
		var sigPresent pk.Boolean
		read(&sigPresent, "signature present flag")
		if sigPresent {
			// A present MessageSignature is a fixed 256-byte blob; the offline capture sends none.
			sig := make([]byte, 256)
			if _, err := r.Read(sig); err != nil {
				t.Fatalf("vanilla ServerboundChat signature bytes decode: %v", err)
			}
		}
		// LastSeenMessages$Update: VarInt offset, FixedBitSet(20) = ceil(20/8)=3 bytes, Byte checksum.
		var offset pk.VarInt
		read(&offset, "lastSeen offset")
		bitset := make([]byte, 3)
		if _, err := r.Read(bitset); err != nil {
			t.Fatalf("vanilla ServerboundChat lastSeen FixedBitSet(20) decode: %v", err)
		}
		var checksum pk.Byte
		read(&checksum, "lastSeen checksum")
		if r.Len() != 0 {
			t.Fatalf("vanilla ServerboundChat full walk left %d trailing bytes — the trailing signature/lastSeen layout is WRONG (mis-frames the next packet)", r.Len())
		}
		if string(msg) != "sulfur capture chat" {
			t.Errorf("vanilla ServerboundChat message = %q, want %q", string(msg), "sulfur capture chat")
		}
	})
}
