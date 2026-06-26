package server

import (
	"log"
	"math"
	"sync/atomic"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
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

	// spawnPoint is the SAFE fresh-spawn world position (the air cell the player's feet occupy,
	// ON a standable floor block) computed once at startup by the generator's ported vanilla
	// PlayerSpawnFinder (17-06 spawn-inside-a-block fix). Unlike the scalar spawnSurfaceY (which
	// only carries a Y and forces the blind (8.5, _, 8.5) center column), this carries the full
	// (x,y,z), so a FRESH join lands ON clear ground even when the (8,8) center is occupied by a
	// tree or structure. A reconnecting player (GAMEPLAY-02 persisted .dat) still uses its saved
	// position — only the fresh spawn consults spawnPoint.
	spawnPoint SpawnPoint

	// teleportSeq is the server-issued teleport-id producer (PLAY-02 / T-5-01). Each join
	// claims a fresh incrementing id via nextTeleportID(), threaded into the bootstrap
	// PlayerPosition AND the new tickPlayer.awaitingTeleport so the Plan-05-01 dispatch
	// gate confirms only on the client's matching Confirm Teleportation echo. The id is
	// SERVER-chosen and incrementing (never client-supplied), so it is spoof-resistant.
	// Atomic because AcceptPlayer runs on a per-connection accept goroutine — multiple
	// joins issue ids concurrently without crossing into tick-owned state.
	teleportSeq atomic.Uint64

	// worldDir is the persistent world directory (ENT-06): AcceptPlayer loads the joining
	// player's world/playerdata/<uuid>.dat from here BEFORE the bootstrap (or spawn defaults
	// if absent/corrupt — T-6-16). An empty worldDir disables persistence (loadPlayer just
	// returns defaults), so a stateless v1 deployment still works. Read-only after
	// construction; the load runs off-tick on the accept goroutine (disk IO before the player
	// is registered with the tick), so it crosses no tick-owned state.
	worldDir string
}

// SpawnPoint is the SAFE fresh-spawn world position (the air cell the player's feet occupy,
// ON a standable floor block, never embedded in terrain/decoration). It is the server-side
// mirror of world.NoiseGenerator.SpawnPos's result (the ported vanilla PlayerSpawnFinder over
// the FULLY-DECORATED spawn chunk — 17-06). cmd/sulfur computes it once at startup and threads
// it through NewGameTick; the join bootstrap and the in-game respawn both place a FRESH spawn
// here. Keeping the type in the server package avoids a server->world dependency (main.go
// converts the world.SpawnPoint into this).
type SpawnPoint struct {
	X, Y, Z float64
}

// NewGameTick constructs the real GamePlay over the shared inbound seam, the single tick loop,
// and the independent keep-alive. spawnSurfaceY is the standable-floor block world-Y carried
// for the legacy scalar spawn (and the Superflat fallback); spawnPoint is the full SAFE
// fresh-spawn (x,y,z) the join bootstrap places a fresh player at (17-06 spawn-inside-a-block
// fix). main() builds these, starts the tick and keep-alive goroutines, then sets
// srv.GamePlay = NewGameTick(...). It also forwards spawnPoint into the tick so an in-game
// respawn lands at the SAME safe column (SetSpawnPoint).
func NewGameTick(inbound chan Intent, loop *TickLoop, keep *KeepAlive, spawnSurfaceY int, spawnPoint SpawnPoint) *gameTick {
	loop.SetSpawnPoint(spawnPoint)
	return &gameTick{inbound: inbound, loop: loop, keep: keep, spawnSurfaceY: spawnSurfaceY, spawnPoint: spawnPoint}
}

// SetWorldDir wires the persistent world directory (ENT-06) so AcceptPlayer loads/saves each
// player's world/playerdata/<uuid>.dat. main() calls it after NewGameTick when a persistent
// world is configured; leaving it unset (empty) keeps the v1 stateless behavior (loadPlayer
// returns spawn defaults, no save). Set-once at setup.
func (g *gameTick) SetWorldDir(dir string) { g.worldDir = dir }

// nextTeleportID claims a fresh, non-zero, incrementing teleport id for a joining player
// (PLAY-02). It is the producer side of the Plan-05-01 confirm gate: the returned id is
// carried by the bootstrap PlayerPosition and stored in tickPlayer.awaitingTeleport, so a
// client's Confirm Teleportation echo confirms the gate only on an exact match. The first
// issued id is 1 (the counter is pre-incremented), so the id is never 0 — distinguishing
// "no outstanding teleport" from a real one if that ever matters. Atomic so concurrent
// joins on separate accept goroutines never collide (T-5-01).
func (g *gameTick) nextTeleportID() int {
	return int(g.teleportSeq.Add(1))
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
// T-2-05). KeepAlive calls this ONLY on a timeout kick (keepalive.go kickPlayer), so it
// is the funnel where we record "timeout" as the disconnect reason BEFORE the connection
// tears down — the leave log line (AcceptPlayer) then attributes the kick correctly
// (17-09 / TUI-02 foundation). The reason reaches the client on the wire as well.
func (k keepAliveClient) SendDisconnect(reason chat.Message) {
	k.c.SetDisconnectReason("timeout")
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

	// Claim the server-issued incrementing teleport id for this join (PLAY-02). It is
	// carried by the bootstrap PlayerPosition AND stored in tickPlayer.awaitingTeleport
	// below, so the Plan-05-01 dispatch gate confirms only on the client's matching
	// Confirm Teleportation echo (T-5-01). The id is computed off-tick here; the
	// tickPlayer that records it is constructed off-tick and handed to the owner via
	// register, so no tick-owned state is mutated across the boundary.
	teleportID := g.nextTeleportID()

	// Claim the player's server-issued ENTITY id from the tick's monotonic allocator
	// (ENT-01). Like teleportID, this is computed OFF-tick here: the allocator's only state
	// is an atomic counter, so the claim crosses no tick-owned game state (exactly the
	// teleportSeq discipline) and stays -race clean by construction (T-6-08). The id is
	// carried into the bootstrap as the ClientboundLogin playerId — replacing the old
	// hard-coded joinEntityID=1 — and recorded on the tickPlayer below so players and
	// entities draw from one id space with no collision (06-RESEARCH Pitfall 7 / T-6-07).
	entityID := g.loop.idAlloc.AllocID()

	// GAMEPLAY-02 (Plan 17-01) position load: read the persisted .dat ONCE here, before the
	// bootstrap, so a reconnecting player's saved position drives the SINGLE bootstrap teleport
	// (Pitfall 3 — never a second post-register teleport that would re-arm the confirm gate).
	// Default to the hardcoded spawn column (chunk (center) block center, surfaceY+2). A missing
	// or corrupt .dat returns ok=false and the defaults stand (T-6-16). The loaded value is also
	// reused below to restore health/food/saturation and seed the live player's pos/center.
	// 17-06 spawn-inside-a-block fix: a FRESH player spawns at the SAFE column the ported vanilla
	// PlayerSpawnFinder found (the air cell ON a standable floor — may NOT be the (8,8) center when
	// a tree/structure occupies it), instead of the blind center column. spawnPoint.Y is already
	// the feet cell, so no "+2" is applied to it. A reconnecting player (persisted .dat below)
	// overrides this with its saved position (GAMEPLAY-02) — only the fresh spawn uses spawnPoint.
	spawnX := g.spawnPoint.X
	spawnY := g.spawnPoint.Y
	spawnZ := g.spawnPoint.Z
	var (
		loaded     save.PlayerData
		haveLoaded bool
	)
	if g.worldDir != "" {
		if data, ok := loadPlayer(g.worldDir, id); ok {
			loaded, haveLoaded = data, true
			spawnX, spawnY, spawnZ = data.Pos[0], data.Pos[1], data.Pos[2]
		}
	}

	// Full early-Play bootstrap (PLAY-01/02/05). Enqueue Login(JoinGame) ->
	// GameEvent(LEVEL_CHUNKS_LOAD_START) -> PlayerPosition -> PlayerAbilities ->
	// SetHeldSlot -> PlayerInfoUpdate(self tab list) -> SetDefaultSpawnPosition on the
	// connection's outbound queue BEFORE registering the player with the tick. The single
	// writeLoop drains the queue FIFO, so the WHOLE sequence lands on the wire ahead of any
	// SetChunkCacheCenter / LevelChunkWithLight the tick later enqueues for this player —
	// the load-bearing invariant that Login (which creates the client's ClientLevel)
	// precedes the chunk stream, now extended so the tail precedes it too. Without Login a
	// real 26.2 client NPEs in handleSetChunkCacheCenter ("this.level is null"). The real
	// login name/id feed the self tab-list entry (PLAY-05). Sending here mutates no
	// tick-owned state (only the connection's own queue), preserving TICK-05.
	sendPlayBootstrap(c, viewDist, spawnCenter, g.spawnSurfaceY, bootstrapParams{
		name:       name,
		id:         id,
		teleportID: teleportID,
		gameMode:   gameModeSurvival,
		entityID:   entityID,
		// spawnX/Y/Z is ALWAYS the authoritative spawn: the GAMEPLAY-02 persisted position when
		// reconnecting, else the 17-06 SAFE fresh-spawn (the ported PlayerSpawnFinder column). Either
		// way the bootstrap teleports the SINGLE PlayerPosition to it with the join teleport id (no
		// second teleport), so hasSpawn is always true now — the old blind (8.5, surfaceY+2, 8.5)
		// center-column recompute inside sendPlayBootstrap is no longer the fresh-spawn path.
		spawnX:   spawnX,
		spawnY:   spawnY,
		spawnZ:   spawnZ,
		hasSpawn: true,
	})

	// CMD-01 join-time send: serialize the shared command graph to THIS client as
	// ClientboundCommands so it tab-completes the registered commands. cmdGraph.ClientJoin
	// writes the packet to the command.Client adapter, which forwards to the connection's
	// bounded outbound queue (commandClientAdapter -> Client.Send); the single writeLoop is
	// still the sole socket writer, so this sends ON the accept goroutine without mutating any
	// tick-owned state (TICK-05) — exactly like the bootstrap above. The graph is built ONCE
	// (package-level cmdGraph) and read-only after build, so concurrent joins share one
	// immutable tree with no lock (proven -race clean). It is enqueued AFTER the play
	// bootstrap so Login (which creates the client's ClientLevel) lands first.
	cmdGraph.ClientJoin(commandClientAdapter{c})

	// GAMEPLAY-02: seed the live player's position + view-ring center from the (possibly
	// persisted) spawn coords so flushOutbound streams the correct ring around the saved
	// location. center is the chunk column of the spawn block; for a fresh player this resolves
	// to chunk (0,0) (the hardcoded spawn), matching the prior behavior.
	playerCenter := chunkCenterOf(int32(math.Floor(spawnX)), int32(math.Floor(spawnZ)))

	player := &tickPlayer{
		client:           c,
		keep:             g.keep,
		keepalive:        ka,
		x:                spawnX,
		y:                spawnY,
		z:                spawnZ,
		center:           playerCenter,
		viewDist:         viewDist,
		sentChunks:       make(map[level.ChunkPos]bool),
		secs:             overworldSections,
		awaitingTeleport: teleportID,
		entityID:         entityID,
		uuid:             id,
		// CMD-02: the login-profile name is the SERVER-authoritative chat attribution
		// ("<name> message"). It was accepted at AcceptPlayer but unstored before this plan;
		// threaded here as a value (crosses no tick-owned state), exactly like entityID/uuid.
		name: name,
		// ENT-05: a fresh player spawns at full survival health/food/saturation (the
		// server-owned defaults). These tick-owned fields drive the damage->death->respawn
		// loop; the client never sets them (T-6-05). A loaded .dat (ENT-06) overrides them
		// just below when a persisted player rejoins.
		health:     maxHealth,
		food:       maxFood,
		saturation: defaultSaturation,
		// Breath (Plan 17-13): a fresh player spawns with a full bubble bar. Vanilla's Entity ctor
		// seeds DATA_AIR_SUPPLY_ID to getMaxAirSupply()==maxAirSupply (300).
		airSupply: maxAirSupply,
	}

	// ENT-06 + GAMEPLAY-02 load-on-join: apply the persisted snapshot loaded above (the disk IO
	// already ran on this accept goroutine, BEFORE register, so it crosses no tick-owned state —
	// T-6-16). Health/food/saturation restore the survival state; the persisted ROTATION is
	// applied here, and the persisted POSITION was already fed into the single bootstrap teleport
	// (spawnX/Y/Z above) and the live player's x/y/z/center, so a reconnecting player spawns at
	// its saved location with no second teleport (Pitfall 3). A first join (haveLoaded=false)
	// keeps the spawn defaults.
	if haveLoaded {
		player.health = loaded.Health
		player.food = loaded.FoodLevel
		player.saturation = loaded.FoodSaturationLevel
		player.yaw = loaded.Rotation[0]
		player.pitch = loaded.Rotation[1]
	}

	g.loop.register <- player

	// JOIN log (17-09 / TUI-02 foundation): the player is now accepted, bootstrapped, and
	// handed to the tick — emit one greppable structured line so operators (and the Phase 17
	// visual gate) can SEE the join the moment it lands. Goes to the same stderr as the rest
	// of the server (log.Default()): AcceptPlayer holds no logger field, and threading one
	// here would touch the tick struct that sibling agents are editing, so log.Printf is the
	// non-invasive choice. name/id come from the login profile args, entityID/addr from the
	// just-registered player and its connection.
	log.Printf("player joined: name=%s uuid=%s entityID=%d addr=%s", name, id, entityID, c.RemoteAddr())

	// Block until the connection closes (the player is playing). The conn is torn down
	// when this returns, per the GamePlay contract; until then we keep the connection
	// alive in the ticking world. c.quit is closed by Client.Close (read error, write
	// error, or queue-full drop) — that is the player's leave signal.
	<-c.quit

	// LEAVE log (17-09 / TUI-02 foundation): c.quit unblocked, so the connection is gone.
	// Read the best-known reason: the keep-alive timeout kick set "timeout" via
	// SetDisconnectReason (keepAliveClient.SendDisconnect) BEFORE Close; any other teardown
	// (client closed the socket / read or write EOF / queue-full drop) left it unset and
	// DisconnectReason() reports "quit". This is the minimal viable taxonomy — the full
	// kick/protocol-reason set is TUI-02 (Phase 19). Emitted before unregister so the line
	// is greppable alongside the join even if a later step were to block.
	log.Printf("player left: name=%s uuid=%s reason=%s", name, id, c.DisconnectReason())

	// Leave: unregister from the tick (message; the owner removes it on-thread) and from
	// the independent keep-alive. Order is not load-bearing — both are idempotent no-ops
	// for an already-removed player.
	g.loop.unregister <- c
	g.keep.ClientLeft(ka)
}
