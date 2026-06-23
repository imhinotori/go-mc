package server

import (
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	netmc "github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/yggdrasil/user"
)

// sendHandshake writes a handshake packet (id 0x00) on the client end of the pipe
// in the 776 wire order: Protocol(VarInt), Host(String), Port(UnsignedShort),
// Intention(VarInt). Intention 1 = status, 2 = login.
func sendHandshake(t *testing.T, client *netmc.Conn, protocol, intention int32) {
	t.Helper()
	err := client.WritePacket(pk.Marshal(
		0x00,
		pk.VarInt(protocol),
		pk.String("localhost"),
		pk.UnsignedShort(25565),
		pk.VarInt(intention),
	))
	if err != nil {
		t.Fatalf("write handshake: %v", err)
	}
}

// readLoginDisconnect reads a single packet from the client end and asserts it is a
// Login Disconnect, returning the decoded readable reason.
func readLoginDisconnect(t *testing.T, client *netmc.Conn) chat.Message {
	t.Helper()
	var p pk.Packet
	if err := client.ReadPacket(&p); err != nil {
		t.Fatalf("read packet: %v", err)
	}
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundLoginLoginDisconnect {
		t.Fatalf("expected Login Disconnect (id %d), got id %d",
			packetid.ClientboundLoginLoginDisconnect, p.ID)
	}
	var reason chat.Message
	if err := p.Scan(&reason); err != nil {
		t.Fatalf("scan disconnect reason: %v", err)
	}
	return reason
}

// stubPing is a minimal ListPingHandler used to confirm the status path is reachable
// regardless of the client protocol number.
type stubPing struct{ reached chan struct{} }

func (s *stubPing) Name() string                  { return ProtocolName }
func (s *stubPing) Protocol(int32) int            { return ProtocolVersion }
func (s *stubPing) MaxPlayer() int                { return 20 }
func (s *stubPing) OnlinePlayer() int             { return 0 }
func (s *stubPing) PlayerSamples() []PlayerSample { return nil }
func (s *stubPing) Description() *chat.Message     { m := chat.Text("stub"); return &m }
func (s *stubPing) FavIcon() string               { return "" }

// recordLogin is a LoginHandler stub that records that AcceptLogin was reached. It
// fails the login immediately after, so AcceptConn returns without needing a full
// login round-trip — the point is only to prove the proto gate let it through.
type recordLogin struct{ reached chan struct{} }

func (r *recordLogin) AcceptLogin(conn *netmc.Conn, protocol int32) (name string, id uuid.UUID, profilePubKey *user.PublicKey, properties []user.Property, err error) {
	close(r.reached)
	return "", id, nil, nil, LoginFailErr{reason: chat.Text("stop here")}
}

// TestHandshakeProtocol covers NET-01: the login path asserts protocol == 776 and
// emits a readable Login Disconnect on mismatch, while the status path is ungated.
func TestHandshakeProtocol(t *testing.T) {
	t.Run("login mismatch -> readable disconnect", func(t *testing.T) {
		server, client := newPipe(t)
		srv := &Server{}
		done := runAcceptConn(t, srv, server)

		sendHandshake(t, client, 999, 2) // wrong protocol, login intent
		reason := readLoginDisconnect(t, client)

		text := reason.ClearString()
		if text == "" {
			t.Fatal("disconnect reason is empty (silent-ish drop)")
		}
		if !strings.Contains(text, ProtocolName) || !strings.Contains(text, strconv.Itoa(ProtocolVersion)) {
			t.Fatalf("reason %q does not mention server version %s (%d)", text, ProtocolName, ProtocolVersion)
		}
		<-done // AcceptConn must return (conn closed), not hang
	})

	t.Run("login match -> proceeds to AcceptLogin", func(t *testing.T) {
		server, client := newPipe(t)
		reached := make(chan struct{})
		srv := &Server{LoginHandler: &recordLogin{reached: reached}}
		_ = runAcceptConn(t, srv, server)

		sendHandshake(t, client, ProtocolVersion, 2) // correct protocol, login intent

		select {
		case <-reached:
			// AcceptLogin was called: gate let the matching protocol through.
		default:
			// Give the server goroutine a chance via a blocking read; recordLogin
			// fails login which makes AcceptConn send a disconnect we can drain.
			var p pk.Packet
			_ = client.ReadPacket(&p)
			select {
			case <-reached:
			default:
				t.Fatal("AcceptLogin was not reached for a protocol-776 login handshake")
			}
		}
	})

	t.Run("status ungated -> wrong protocol still reaches status", func(t *testing.T) {
		server, client := newPipe(t)
		reached := make(chan struct{})
		srv := &Server{ListPingHandler: &stubPing{reached: reached}}
		_ = runAcceptConn(t, srv, server)

		sendHandshake(t, client, 999, 1) // wrong protocol, status intent
		// Drive the status request so acceptListPing produces a response.
		if err := client.WritePacket(pk.Marshal(0x00)); err != nil { // Status Request
			t.Fatalf("write status request: %v", err)
		}
		var p pk.Packet
		if err := client.ReadPacket(&p); err != nil {
			t.Fatalf("read status response: %v", err)
		}
		if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundStatusStatusResponse {
			t.Fatalf("expected status response, got id %d", p.ID)
		}
	})
}
