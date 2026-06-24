// Command sulfur is the runnable Sulfur server entrypoint. It assembles the Phase-2
// protocol gate over the fork's existing primitives and wires in the Phase-3
// authoritative tick loop:
//
//   - a proto-776 ListPingHandler (NET-02): version 26.2 / protocol 776 / MOTD / players
//   - an offline MojangLoginHandler with a compression threshold (NET-03)
//   - the Configurations handler (registry data sourced from embedded real 26.2 NBT)
//   - the real tick-driven GamePlay (gameTick): a single tick goroutine owns the game
//     loop, and an independent KeepAlive goroutine runs the 15s-ping/30s-timeout
//     liveness — replacing the Phase-2 stub GamePlay
//
// The NET-01 proto-776 assertion lives in server.AcceptConn (the login path rejects a
// mismatched protocol with a readable Login Disconnect). main() starts the single tick
// goroutine and the independent keep-alive goroutine, then listens: a logged-in
// connection is handed to gameTick.AcceptPlayer, which attaches it to the ticking world
// (Phase-3 milestone — the player stands in a live, empty, ticking server).
package main

import (
	"context"
	"flag"
	"log"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/server"
	"github.com/imhinotori/sulfur/world"
)

const (
	// defaultMOTD is the server-list description shown to clients (NET-02).
	defaultMOTD = "Sulfur — a Minecraft 26.2 server in Go"
	// compressionThreshold is the Set Compression threshold negotiated during login
	// (NET-03). 256 is the vanilla default; any value >= 0 enables compression.
	compressionThreshold = 256
	// maxPlayers is the advertised player cap.
	maxPlayers = 20
	// inboundCap bounds the network->tick seam (the shared chan Intent). Every
	// connection's readLoop produces Intents onto this one channel; the tick goroutine
	// drains it non-blockingly each wake (drainInbound). Bounded per the Phase-2
	// bounded-queue discipline (T-2-04): a flood backpressures the read goroutines
	// rather than growing server memory without limit. 1024 is ample headroom for the
	// drain cadence (the tick wakes every 5ms) while staying a trivial fixed buffer.
	inboundCap = 1024

	// Overworld superflat shape (Plan 04-03 / WORLD-04 stub). secs = height/16 (384/16
	// = 24), minY = -64, surfaceY = the top solid (stone) block world-Y. A surface at
	// y=-48 gives a 16-block-thick lit floor above the bedrock layer — a stable square a
	// client can stand on without falling through void (the real terrain is Phase 9).
	overworldSecs     = 24
	overworldMinY     = -64
	overworldSurfaceY = -48

	// workerBuf sizes the off-tick worker's bounded request + results channels. It is
	// comfortably above the small clamped view ring ((2*serverViewDistance+1)^2 columns)
	// so a single player's first-tick request burst is absorbed without dropping; a
	// flood beyond it backpressures (Request drops) rather than growing memory.
	workerBuf = 256
)

// pingHandler composes the version/MOTD reporting of PingInfo (Protocol fixed to 776,
// Name "26.2") with the player-count reporting of PlayerList, yielding a complete
// ListPingHandler for the status response (NET-02).
type pingHandler struct {
	*server.PingInfo
	*server.PlayerList
}

// newServer assembles the Phase-2 gate handlers and the real Phase-3 GamePlay. The
// shared runtime (the inbound seam, the single tick loop, the independent keep-alive)
// is constructed and started by the caller and handed in here as the GamePlay, so the
// server's goroutines are running before the listener accepts a connection.
func newServer(gameplay server.GamePlay) *server.Server {
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
		// server/registrydata, so Configurations needs no fields.
		ConfigHandler: &server.Configurations{},
		GamePlay:      gameplay,
	}
}

func main() {
	addr := flag.String("addr", ":25565", "address to listen on")
	flag.Parse()

	// Construct the single-owner runtime: the network->tick seam (one bounded chan
	// Intent), the authoritative tick loop over the real system clock, and the
	// independent keep-alive component.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inbound := make(chan server.Intent, inboundCap)
	tick := server.NewTickLoop(server.SystemClock())
	keep := server.NewKeepAlive()

	// Build the off-tick world subsystem (Plan 04-03): a deterministic superflat
	// generator, the off-tick load/generate worker (regionDir "" => always generate for
	// v1), and the tick-owned chunk manager. SetWorld wires the worker's immutable
	// results into the tick's applyAsyncResults rejoin (WORLD-01) and must run BEFORE
	// tick.Run so asyncIn is non-nil when the loop starts.
	gen := world.NewSuperflat(overworldSecs, overworldMinY, overworldSurfaceY)
	worker := world.NewWorker(gen, "", workerBuf)
	mgr := world.NewChunkManager()
	tick.SetWorld(mgr, worker)

	// Start the long-lived server goroutines: exactly one tick goroutine (the sole
	// owner/mutator of game state, consuming inbound), one independent keep-alive
	// goroutine (its own 15s/30s timers, never gated on tick cadence — TICK-04), and the
	// off-tick chunk worker (computes immutable ChunkResults; the tick is the sole
	// mutator of the manager — TICK-05 / T-4-05).
	go tick.Run(ctx, inbound)
	go keep.Run(ctx)
	go worker.Run(ctx)

	// The real GamePlay bridges an accepted connection to the running tick + keep-alive.
	srv := newServer(server.NewGameTick(inbound, tick, keep))

	srv.Logger.Printf("Sulfur listening on %s (protocol %d, %s)",
		*addr, server.ProtocolVersion, server.ProtocolName)
	log.Fatal(srv.Listen(*addr))
}
