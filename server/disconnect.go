package server

import (
	"github.com/imhinotori/go-mc/chat"
	"github.com/imhinotori/go-mc/data/packetid"
	"github.com/imhinotori/go-mc/net"
	pk "github.com/imhinotori/go-mc/net/packet"
)

// ConnState identifies which protocol state a connection is in when it must be
// disconnected. The Disconnect packet has a DIFFERENT packet id per state, so the
// reason can only be delivered if we pick the state-correct id.
type ConnState int

const (
	// StateLogin is the Login state (before configuration). Uses the Login
	// Disconnect packet.
	StateLogin ConnState = iota
	// StateConfig is the Configuration state. Uses the Config Disconnect packet.
	StateConfig
	// StatePlay is the Play state (in-game). Uses the Play Disconnect packet.
	StatePlay
)

// Disconnect sends the state-correct Disconnect packet carrying a readable
// chat.Message reason, then returns any write error. It centralizes the
// state -> Disconnect-packet-id mapping so no connection is ever closed silently
// where a reason is sendable (NET-07 / T-2-05).
//
// The three ids are the generated 776 Disconnect packets:
//   - Login:  ClientboundLoginLoginDisconnect
//   - Config: ClientboundConfigDisconnect
//   - Play:   ClientboundDisconnect
//
// This is the shared path for the rewritten Configuration sequence (Plan 02-04)
// and for Play-state disconnects in Phase 3+. The existing AcceptConn LoginFailErr/
// ConfigFailErr mapping already sends the right Login/Config packets and is left
// unchanged; this helper is the reusable, state-aware equivalent.
func Disconnect(conn *net.Conn, state ConnState, reason chat.Message) error {
	var id packetid.ClientboundPacketID
	switch state {
	case StateLogin:
		id = packetid.ClientboundLoginLoginDisconnect
	case StateConfig:
		id = packetid.ClientboundConfigDisconnect
	case StatePlay:
		id = packetid.ClientboundDisconnect
	default:
		// Unknown state: fall back to the Play Disconnect id rather than emitting a
		// silent close — a readable reason on a best-effort id beats no reason.
		id = packetid.ClientboundDisconnect
	}
	return conn.WritePacket(pk.Marshal(id, reason))
}
