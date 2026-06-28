// Package server provide a minecraft server framework.
// You can build the server you want by combining the various functional modules provided here.
// An example can be found in examples/frameworkServer.
//
// # This package is under rapid development, and any API may be subject to break changes
//
// A server is roughly divided into two parts: Gate and GamePlay
//
//	+------------------------------------------------------------------------------+
//	|                             Go-MC Server Framework                           |
//	|--------------------------------------+---------------------------------------|
//	|               Gate                   |                GamePlay               |
//	|--------------------+-----------------+---------------+-----------------------|
//	|    LoginHandler    |         ListPingHandler         |        Others..       |
//	|--------------------|------------+----+---------------|-----------------------+
//	| MojangLoginHandler |  PingInfo  |     PlayerList     |  [go-mc/server], etc. |
//	+--------------------+------------+--------------------+-----------------------+
//
// Gate, which is used to respond to the client login request, provide login verification,
// respond to the List Ping Request and providing the online players' information.
//
// Gameplay, which is used to handle all things after a player successfully logs in
// (that is, after the LoginSuccess package is sent),
// and is responsible for functions including player status, chunk management, keep alive, chat, etc.
//
// The implement of Gameplay is provided at [go-mc/server]. You can also write your version.
//
// [go-mc/server]: https://github.com/go-mc/server
package server

import (
	"errors"
	"log"
	"log/slog"
	"strconv"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
)

const (
	ProtocolName    = "26.2"
	ProtocolVersion = 776
)

type Server struct {
	*log.Logger
	ListPingHandler
	LoginHandler
	ConfigHandler
	GamePlay
}

func (s *Server) Listen(addr string) error {
	listener, err := net.ListenMC(addr)
	if err != nil {
		return err
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go s.AcceptConn(&conn)
	}
}

func (s *Server) AcceptConn(conn *net.Conn) {
	defer conn.Close()
	protocol, intention, err := s.handshake(conn)
	if err != nil {
		return
	}

	// Connection-level visibility (17-09 / TUI-02 foundation): log every handshaked
	// connection BEFORE login so even status pings and pre-login attempts are visible
	// (the join/leave lines in AcceptPlayer cover the post-login lifecycle). intention 1
	// = list ping, 2 = login. Logged after the handshake (not on raw TCP accept) so the
	// line carries the protocol + intention and malformed TCP probes that never complete
	// a handshake stay out of the log. One greppable line per connection.
	//
	// slog (TUI-02): emits through the default slog handler so the line appears in BOTH
	// the operator TUI viewport (TTY mode, 19-01) and plain stderr (headless). At this
	// pre-login stage the teardown is a raw conn (not yet a *Client with the atomic
	// disconnectReason), so any reject reason is carried as a log attr, not via
	// SetDisconnectReason. (Pitfall 6: this REPLACES the old s.Logger.Printf — no double-log.)
	slog.Info("connection",
		"addr", conn.Socket.RemoteAddr(), "protocol", protocol, "intention", intention)

	switch intention {
	case 1: // list ping
		// Status is intentionally ungated: a version-mismatched client must still
		// be able to read the server-list version label (T-2-01). acceptListPing
		// answers with ProtocolVersion regardless of the client's protocol.
		s.acceptListPing(conn, protocol)
	case 2: // login
		// NET-01 / T-2-01: assert the client speaks protocol 776 on the login path.
		// On mismatch, send a readable Login Disconnect (never a silent drop) and
		// return so the connection closes via the deferred conn.Close().
		if protocol != ProtocolVersion {
			_ = Disconnect(conn, StateLogin, chat.Text(
				"Unsupported protocol: server is "+ProtocolName+" ("+strconv.Itoa(ProtocolVersion)+")"))
			// TUI-02 taxonomy #2 (protocol mismatch): a login-stage reject. Reason token
			// carried as a log attr (pre-*Client raw conn).
			slog.Warn("login rejected",
				"addr", conn.Socket.RemoteAddr(), "reason", "protocol_mismatch",
				"got", protocol, "want", ProtocolVersion)
			return
		}
		name, id, profilePubKey, properties, err := s.AcceptLogin(conn, protocol)
		if err != nil {
			var loginErr LoginFailErr
			if errors.As(err, &loginErr) {
				_ = conn.WritePacket(pk.Marshal(
					packetid.ClientboundLoginLoginDisconnect,
					loginErr.reason,
				))
			}
			// TUI-02 taxonomy #1 (login failure): AcceptLogin returned an error (auth
			// fail, EOF mid-login — the Phase 18 path). Reason token + the wrapped err.
			slog.Warn("login failed",
				"addr", conn.Socket.RemoteAddr(), "reason", "login_failure", "err", err)
			return
		}
		skinParts, err := s.AcceptConfig(conn)
		if err != nil {
			var configErr ConfigFailErr
			if errors.As(err, &configErr) {
				_ = conn.WritePacket(pk.Marshal(
					packetid.ClientboundConfigDisconnect,
					configErr.reason,
				))
			}
			// TUI-02 taxonomy #3 (config failure): the config sequence errored. Reason
			// token + the wrapped err (the login name is known but not threaded here).
			slog.Warn("config failed",
				"addr", conn.Socket.RemoteAddr(), "reason", "config_failure", "err", err)
			return
		}
		s.AcceptPlayer(name, id, profilePubKey, properties, protocol, conn, skinParts)
	}
}
