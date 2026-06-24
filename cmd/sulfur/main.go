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
	"os"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/level"
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

	// Overworld dimension shape. secs = height/16 (384/16 = 24), minY = -64. These feed
	// BOTH generators (the noise generator carries them for chunk geometry + the carve
	// bounds; sea level lives in the router settings). overworldSurfaceY is the Superflat
	// fallback's flat stone top (a 16-block lit floor above bedrock) — the noise generator
	// does NOT use it (its spawn surface is derived per-column from the generated terrain).
	overworldSecs     = 24
	overworldMinY     = -64
	overworldSurfaceY = -48

	// worldSeed is the fixed default overworld seed (WORLD-04). A fixed documented seed
	// makes the generated world fully reproducible run-to-run (Generate is pure over
	// (seed, pos) — TestNoiseGenDeterministic). Override with -seed; SULFUR_SUPERFLAT=1
	// falls back to the deterministic Superflat stub (which ignores the seed).
	worldSeed = int64(0x5EED_C0DE)

	// workerBuf sizes the off-tick worker's bounded request + results channels. It is
	// comfortably above the small clamped view ring ((2*serverViewDistance+1)^2 columns)
	// so a single player's first-tick request burst is absorbed without dropping; a
	// flood beyond it backpressures (Request drops) rather than growing memory.
	workerBuf = 256

	// worldDir is the persistent world directory (ENT-06). Player .dat files live under
	// worldDir/playerdata/<uuid>.dat and the entities region under worldDir/entities/. It is
	// the v1 persistent world root; the off-tick save loop writes player snapshots here on
	// leave, and AcceptPlayer loads them on join.
	worldDir = "world"
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
	seed := flag.Int64("seed", worldSeed, "overworld world seed (default is the fixed reproducible worldSeed; ignored when SULFUR_SUPERFLAT=1)")
	flag.Parse()

	// Construct the single-owner runtime: the network->tick seam (one bounded chan
	// Intent), the authoritative tick loop over the real system clock, and the
	// independent keep-alive component.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inbound := make(chan server.Intent, inboundCap)
	tick := server.NewTickLoop(server.SystemClock())
	keep := server.NewKeepAlive()

	// Build the off-tick world subsystem (Plan 04-03 / Plan 09-09 PARITY-01): the column
	// generator, the off-tick load/generate worker (regionDir "" => always generate for
	// v1), and the tick-owned chunk manager. SetWorld wires the worker's immutable results
	// into the tick's applyAsyncResults rejoin (WORLD-01) and must run BEFORE tick.Run so
	// asyncIn is non-nil when the loop starts.
	//
	// PARITY-01: the default generator is the full-parity NoiseGenerator (ported Mojang
	// 26.2 density-function pipeline — hills, noise caves, carved tunnels + ravines,
	// aquifers, ore veins, biome-varied surfaces) behind the UNCHANGED off-tick worker (the
	// worker is generator-agnostic — a pure "better Generate" swap, no concurrency/seam
	// work). SULFUR_SUPERFLAT=1 is the escape hatch back to the deterministic Superflat stub
	// (kept as a fallback + the determinism reference), mirroring the SULFUR_DEBUG pattern.
	//
	// spawnSurfaceY is the top solid/fluid block world-Y for the (0,0) spawn column. The
	// noise terrain's spawn surface VARIES per column, so it is derived from the GENERATED
	// spawn chunk's WorldSurface heightmap (NoiseGenerator.SpawnSurfaceY) — feeding the
	// fixed Superflat top would bury the player in a hill or float them in void over an
	// ocean (T-9-26). The Superflat fallback keeps its fixed flat top.
	var gen world.Generator
	spawnSurfaceY := overworldSurfaceY
	if os.Getenv("SULFUR_SUPERFLAT") == "1" {
		gen = world.NewSuperflat(overworldSecs, overworldMinY, overworldSurfaceY)
		log.Printf("SULFUR_SUPERFLAT=1: Superflat stub generator (flat top y=%d) — noise terrain disabled", overworldSurfaceY)
	} else {
		ng := world.NewNoiseGenerator(*seed, overworldSecs, overworldMinY)
		spawnSurfaceY = ng.SpawnSurfaceY(level.ChunkPos{0, 0})
		gen = ng
		log.Printf("full-parity NoiseGenerator armed (seed=%d): spawn-column surface y=%d", *seed, spawnSurfaceY)
	}
	worker := world.NewWorker(gen, "", workerBuf)
	mgr := world.NewChunkManager()
	tick.SetWorld(mgr, worker)
	// ENT-05: tell the tick where the world spawn surface is so an in-game respawn
	// re-teleports a player two blocks above it — the same placement the join bootstrap
	// uses (NewGameTick is handed the same spawnSurfaceY below). For the noise generator
	// this is the per-column derived surface, not the fixed Superflat top.
	tick.SetSpawn(spawnSurfaceY)

	// Plan 06-07 interactive gate: when SULFUR_DEBUG=1, arm the OFF-by-default debug triggers
	// so an operator running an unmodified vanilla 26.2 client can SEE the Phase-6 milestone —
	// a visible, vanilla-renderable pig spawns near spawn and PACES (the entityTracker's
	// Add/Teleport/Remove, ENT-01/02), and players take periodic damage so the on-screen health
	// bar drops and the death-screen -> respawn loop runs (ENT-05/06). Off by default, so a
	// normal `sulfur` run is unaffected; armed only for the interactive check.
	if os.Getenv("SULFUR_DEBUG") == "1" {
		// Spawn the debug entity at the real spawn surface so it sits ON the terrain (the
		// derived noise surface, or the Superflat fixed top), not buried under a noise hill.
		tick.SetDebug(spawnSurfaceY)
		log.Printf("SULFUR_DEBUG=1: debug AI-driven entity-spawn ARMED (interactive gate)")
		// Plan 07-06 interactive gate (AI-02): SULFUR_DEBUG_NAV=1 additionally makes the debug
		// pig deterministically pace a fixed line near spawn via the REAL ported A* navigation,
		// so an operator can wall the line and reliably SEE the pathfinder route around the
		// obstacle (random strolling alone rarely crosses a placed wall predictably).
		if os.Getenv("SULFUR_DEBUG_NAV") == "1" {
			tick.SetDebugNavObservable()
			log.Printf("SULFUR_DEBUG_NAV=1: observable fixed-point A* navigation ARMED (the pig paces a known line — wall it to watch A* route around)")
		}
		// SULFUR_DEBUG_DAMAGE=1 (opt-in) arms the periodic player-damage trigger for testing the
		// health-bar / death / respawn loop. OFF by default so normal movement/observation is not
		// interrupted by being killed every ~20s (which freezes the client on the death screen).
		if os.Getenv("SULFUR_DEBUG_DAMAGE") == "1" {
			tick.SetDebugDamage()
			log.Printf("SULFUR_DEBUG_DAMAGE=1: periodic player damage ARMED (health-bar/death/respawn test)")
		}
	}

	// Start the long-lived server goroutines: exactly one tick goroutine (the sole
	// owner/mutator of game state, consuming inbound), one independent keep-alive
	// goroutine (its own 15s/30s timers, never gated on tick cadence — TICK-04), and the
	// off-tick chunk worker (computes immutable ChunkResults; the tick is the sole
	// mutator of the manager — TICK-05 / T-4-05).
	// ENT-06: wire the off-tick player-save consumer. SetSaveSink arms removePlayer to emit an
	// immutable per-player snapshot on leave (taken on the owner goroutine — TICK-05 / T-6-15);
	// RunSaveLoop drains those snapshots and does the disk IO OFF the tick, so a leave never
	// blocks the tick on persistence and no live tick-owned state is read off-thread.
	tick.SetSaveSink()
	go tick.RunSaveLoop(ctx, worldDir)

	go tick.Run(ctx, inbound)
	go keep.Run(ctx)
	go worker.Run(ctx)

	// The real GamePlay bridges an accepted connection to the running tick + keep-alive.
	// spawnSurfaceY is the derived noise spawn surface (or the Superflat fixed top under
	// SULFUR_SUPERFLAT) so the join bootstrap places the player ON solid ground (T-9-26).
	gp := server.NewGameTick(inbound, tick, keep, spawnSurfaceY)
	gp.SetWorldDir(worldDir) // ENT-06: load/save player .dat under worldDir
	srv := newServer(gp)

	srv.Logger.Printf("Sulfur listening on %s (protocol %d, %s)",
		*addr, server.ProtocolVersion, server.ProtocolName)
	log.Fatal(srv.Listen(*addr))
}
