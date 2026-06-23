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
			if s.Logger != nil {
				s.Logger.Printf("client %v rejected: protocol %d != %d",
					conn.Socket.RemoteAddr(), protocol, ProtocolVersion)
			}
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
			if s.Logger != nil {
				s.Logger.Printf("client %v login error: %v", conn.Socket.RemoteAddr(), err)
			}
			return
		}
		err = s.AcceptConfig(conn)
		if err != nil {
			var configErr ConfigFailErr
			if errors.As(err, &configErr) {
				_ = conn.WritePacket(pk.Marshal(
					packetid.ClientboundConfigDisconnect,
					configErr.reason,
				))
			}
			if s.Logger != nil {
				s.Logger.Printf("client %v config error: %v", conn.Socket.RemoteAddr(), err)
			}
			return
		}
		s.AcceptPlayer(name, id, profilePubKey, properties, protocol, conn)
	}
}
