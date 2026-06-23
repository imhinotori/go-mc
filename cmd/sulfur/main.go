// Command sulfur is the runnable Sulfur server entrypoint. It assembles the Phase-2
// protocol gate over the fork's existing primitives:
//
//   - a proto-776 ListPingHandler (NET-02): version 26.2 / protocol 776 / MOTD / players
//   - an offline MojangLoginHandler with a compression threshold (NET-03)
//   - the Configurations handler (the registry-data body is finished in Plan 02-04)
//   - a Phase-2 stub GamePlay that does NOT start a tick loop or touch game state
//
// The NET-01 proto-776 assertion lives in server.AcceptConn (the login path rejects a
// mismatched protocol with a readable Login Disconnect). This binary just wires the
// handlers and listens; the tick loop and a real GamePlay arrive in Phase 3.
package main

import (
	"flag"
	"log"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/net"
	"github.com/imhinotori/sulfur/server"
	"github.com/imhinotori/sulfur/yggdrasil/user"

	"github.com/google/uuid"
)

const (
	// defaultMOTD is the server-list description shown to clients (NET-02).
	defaultMOTD = "Sulfur — a Minecraft 26.2 server in Go"
	// compressionThreshold is the Set Compression threshold negotiated during login
	// (NET-03). 256 is the vanilla default; any value >= 0 enables compression.
	compressionThreshold = 256
	// maxPlayers is the advertised player cap.
	maxPlayers = 20
)

// pingHandler composes the version/MOTD reporting of PingInfo (Protocol fixed to 776,
// Name "26.2") with the player-count reporting of PlayerList, yielding a complete
// ListPingHandler for the status response (NET-02).
type pingHandler struct {
	*server.PingInfo
	*server.PlayerList
}

// stubGamePlay is the Phase-2 GamePlay seam. The login/config gate hands off here once
// a player has logged in and finished configuration. Phase 2 has no tick loop yet, so
// this stub only proves the gate reaches the Play handoff: it sends a readable Play
// Disconnect and returns. It starts no goroutines and accesses no game state. Phase 3
// replaces it with the NET-05 Client read/write loops and the real tick.
type stubGamePlay struct{}

func (stubGamePlay) AcceptPlayer(
	name string,
	id uuid.UUID,
	profilePubKey *user.PublicKey,
	properties []user.Property,
	protocol int32,
	conn *net.Conn,
) {
	_ = server.Disconnect(conn, server.StatePlay,
		chat.Text("Server is not yet playable — gameplay arrives in Phase 3."))
}

func newServer() *server.Server {
	return &server.Server{
		Logger: log.Default(),
		ListPingHandler: &pingHandler{
			PingInfo: server.NewPingInfo(
				server.ProtocolName,    // "26.2"
				server.ProtocolVersion, // 776
				chat.Text(defaultMOTD),
				nil, // no favicon
			),
			PlayerList: server.NewPlayerList(maxPlayers),
		},
		LoginHandler: &server.MojangLoginHandler{
			OnlineMode: false,                // offline: UUID derived from username
			Threshold:  compressionThreshold, // Set Compression negotiated before LoginSuccess
		},
		// The Configuration sequence (Known Packs → Feature Flags → Registry Data →
		// Update Tags → Finish → Acknowledge) is implemented in AcceptConfig; the
		// registry payload is sourced from the embedded real 26.2 NBT in
		// server/registrydata, so Configurations needs no fields (Plan 02-04).
		ConfigHandler: &server.Configurations{},
		GamePlay: stubGamePlay{},
	}
}

func main() {
	addr := flag.String("addr", ":25565", "address to listen on")
	flag.Parse()

	srv := newServer()
	srv.Logger.Printf("Sulfur listening on %s (protocol %d, %s)",
		*addr, server.ProtocolVersion, server.ProtocolName)
	log.Fatal(srv.Listen(*addr))
}
