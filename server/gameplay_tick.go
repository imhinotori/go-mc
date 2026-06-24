package server

import (
	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/yggdrasil/user"

	"github.com/google/uuid"
)

// outboundCap bounds each connection's clientbound queue (NewClient's bounded
// NewChannelQueue, T-2-04). It mirrors the Phase-2 bounded-queue discipline: a slow
// client can never grow server memory — Send drops-and-disconnects on overflow rather
// than blocking the tick. 256 is generously above any single tick's clientbound burst
// for the empty Phase-3 world (chunks/entities, which inflate the burst, arrive in
// Phases 4-6) while staying a trivial fixed allocation.
const outboundCap = 256

// overworldSections is the overworld dimension's section count (height 384 / 16 = 24).
// It seeds each player's secs for chunk generation/empty sizing (Plan 04-03). Derived
// here rather than hard-coded deeper in the pipeline (04-RESEARCH anti-pattern); Phase 5
// will source it from the joined dimension type when multi-dimension support lands.
const overworldSections = 24

// gameTick is the real, tick-driven GamePlay that replaces the Phase-2 stubGamePlay.
// It owns no game state itself — it is the thin bridge between the network-accept
// goroutine (where AcceptPlayer runs) and the single tick goroutine (where game state
// lives). A connection is wired to the tick via the UNCHANGED Phase-2 Client API
// (Client.Start feeds the shared chan Intent) and to the independent KeepAlive
// component; the player is registered with the tick as a MESSAGE (never a direct
// cross-goroutine mutation — TICK-05). The empty world still ticks underneath the
// player: this is the Phase-3 milestone (a player "stands in a ticking server").
type gameTick struct {
	// inbound is the network->tick seam (the Phase-2 chan Intent). Client.Start writes
	// Intents onto it; the tick goroutine (TickLoop.Run) is its sole consumer. Carrying
	// only *Client + pk.Packet keeps any game-state pointer off this boundary (NET-05).
	inbound chan Intent

	// loop is the single-owner tick. AcceptPlayer never touches its fields directly; it
	// registers/unregisters the player through loop.register / loop.unregister, which
	// the tick goroutine drains on-thread (TICK-05).
	loop *TickLoop

	// keep is the independent keep-alive component (its own goroutine + timers, started
	// in main()). AcceptPlayer calls ClientJoin on entry and ClientLeft on exit; a
	// returning ServerboundKeepAlive is forwarded to ClientTick by the tick's dispatch.
	keep *KeepAlive

	// spawnSurfaceY is the superflat top-solid block world-Y (the generator's SurfaceY).
	// The Play bootstrap (sendPlayBootstrap) places the joining player two blocks above
	// it so the client spawns on solid ground rather than inside the floor or in the
	// void. Sourced from the same value cmd/sulfur hands the Superflat generator, so the
	// spawn height and the streamed terrain agree. Phase 5 derives the spawn from a real
	// spawn position / world data.
	spawnSurfaceY int
}

// NewGameTick constructs the real GamePlay over the shared inbound seam, the single
// tick loop, and the independent keep-alive. spawnSurfaceY is the superflat surface
// world-Y the Play bootstrap spawns the player above (the same value cmd/sulfur hands
// the Superflat generator). main() builds these, starts the tick and keep-alive
// goroutines, then sets srv.GamePlay = NewGameTick(...).
func NewGameTick(inbound chan Intent, loop *TickLoop, keep *KeepAlive, spawnSurfaceY int) *gameTick {
	return &gameTick{inbound: inbound, loop: loop, keep: keep, spawnSurfaceY: spawnSurfaceY}
}

// keepAliveClient adapts a *Client to the fork's KeepAliveClient interface
// (SendKeepAlive / SendDisconnect). The fork's KeepAlive component is reused verbatim;
// this thin adapter is the only glue it needs. Both methods enqueue via Client.Send
// (the bounded outbound queue drained by the single writeLoop) — the keep-alive
// goroutine never writes the socket directly, preserving the one-writer invariant.
type keepAliveClient struct{ c *Client }

// SendKeepAlive enqueues a Clientbound Keep Alive carrying the server-generated id.
// The id is server-chosen and incrementing (spoof-resistant, T-3-06); the client must
// echo it back, which the tick forwards to KeepAlive.ClientTick.
func (k keepAliveClient) SendKeepAlive(id int64) {
	k.c.Send(pk.Marshal(packetid.ClientboundKeepAlive, pk.Long(id)))
}

// SendDisconnect enqueues a readable Play Disconnect (never a silent close — NET-07 /
// T-2-05). KeepAlive calls this on a timeout kick; the reason reaches the client.
func (k keepAliveClient) SendDisconnect(reason chat.Message) {
	k.c.Send(pk.Marshal(packetid.ClientboundDisconnect, reason))
}

// AcceptPlayer attaches a logged-in connection to the running server and BLOCKS until
// the player disconnects (the gameplay.go contract: returning closes the conn). It:
//
//  1. builds a Client over conn and Start()s its two Phase-2 goroutines (one writeLoop,
//     one readLoop producing Intents onto the shared inbound channel — API unchanged);
//  2. wires the player into the independent KeepAlive (ClientJoin) so the 15s-ping /
//     30s-timeout liveness runs on KeepAlive's own goroutine (TICK-04);
//  3. registers the player with the tick as a MESSAGE (loop.register), so the tick
//     goroutine — not this accept goroutine — inserts it into its player collection
//     (TICK-05: no game-state pointer is mutated across the boundary);
//  4. blocks on the Client's close signal (the player leaving);
//  5. on exit, unregisters from the tick (loop.unregister) and from KeepAlive
//     (ClientLeft), then returns so the gate closes the conn.
//
// The login profile (name/id/pub key/properties/protocol) is accepted but not yet
// consumed — player identity/profile state is wired into the tick in Phase 4+; Phase 3
// proves only that a connection LIVES in the ticking world.
func (g *gameTick) AcceptPlayer(
	name string,
	id uuid.UUID,
	profilePubKey *user.PublicKey,
	properties []user.Property,
	protocol int32,
	conn *net.Conn,
) {
	c := NewClient(conn, outboundCap)
	c.Start(g.inbound) // Phase-2 API UNCHANGED: one writeLoop + one readLoop -> inbound

	ka := keepAliveClient{c}
	g.keep.ClientJoin(ka) // independent keep-alive timer (TICK-04)

	// Build the tick-owned player off-thread and hand it to the owner as a message. The
	// tick goroutine performs the actual players/clientIndex insert in drainRegistrations
	// — AcceptPlayer never mutates that collection itself (TICK-05 / T-3-03). The
	// keep/keepalive fields let the tick's dispatch forward a returning keep-alive to
	// ClientTick for THIS player.
	// The chunk-streaming fields (Plan 04-03, WORLD-05) default sensibly for Phase 4:
	// the center is {0,0} (a chunk square exists around origin so the player stands on
	// solid ground), the view distance is the SERVER clamp (bounds the needed ring
	// against an untrusted client — threat T-4-01), the sent-set starts empty, and secs
	// is the overworld section count. Phase 5 (PLAY-01/03) overwrites center from the
	// real spawn. All fields are tick-owned; the tick goroutine performs the insert.
	spawnCenter := level.ChunkPos{0, 0}
	viewDist := clampViewDistance(serverViewDistance)

	// MINIMAL Play-state bootstrap (a forward slice of PLAY-01/02/03 for the Phase-4
	// visual milestone). Enqueue Login(JoinGame) -> GameEvent(LEVEL_CHUNKS_LOAD_START)
	// -> PlayerPosition on the connection's outbound queue BEFORE registering the
	// player with the tick. The single writeLoop drains the queue FIFO, so these land
	// on the wire ahead of any SetChunkCacheCenter / LevelChunkWithLight the tick later
	// enqueues for this player — the load-bearing invariant that Login (which creates
	// the client's ClientLevel) precedes the chunk stream. Without it a real 26.2 client
	// NPEs in handleSetChunkCacheCenter ("this.level is null"). Sending here mutates no
	// tick-owned state (only the connection's own queue), preserving TICK-05.
	sendPlayBootstrap(c, viewDist, spawnCenter, g.spawnSurfaceY)

	player := &tickPlayer{
		client:     c,
		keep:       g.keep,
		keepalive:  ka,
		center:     spawnCenter,
		viewDist:   viewDist,
		sentChunks: make(map[level.ChunkPos]bool),
		secs:       overworldSections,
	}
	g.loop.register <- player

	// Block until the connection closes (the player is playing). The conn is torn down
	// when this returns, per the GamePlay contract; until then we keep the connection
	// alive in the ticking world. c.quit is closed by Client.Close (read error, write
	// error, or queue-full drop) — that is the player's leave signal.
	<-c.quit

	// Leave: unregister from the tick (message; the owner removes it on-thread) and from
	// the independent keep-alive. Order is not load-bearing — both are idempotent no-ops
	// for an already-removed player.
	g.loop.unregister <- c
	g.keep.ClientLeft(ka)
}
