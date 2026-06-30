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
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
	"github.com/imhinotori/sulfur/server"
	"github.com/imhinotori/sulfur/server/tui"
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

	// workerBuf sizes the off-tick worker's bounded request + results channels. It MUST exceed
	// the clamped view ring ((2*serverViewDistance+1)^2 columns) so a single player's first-tick
	// request burst is absorbed WITHOUT dropping — at serverViewDistance=10 the ring is 21*21=441
	// columns, so the old 256 dropped ~185 requests every join (the "invisible chunk" bug: a
	// dropped request left its column Loading forever). 2048 covers a 21x21 ring with headroom for
	// a few players' overlapping first-load bursts; the stale-Loading retry (tickChunks ->
	// RetryStale) is the backstop for any drop beyond it, so a flood backpressures+recovers rather
	// than stranding a column. The SEND side is separately paced by the PlayerChunkSender flow
	// control (server/tick_phases.go sendNextChunks), so a large request buffer does NOT cause a
	// send flood — chunks are generated ahead but streamed at the client's acknowledged rate.
	workerBuf = 1024

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
func newServer(gameplay server.GamePlay, onlineMode bool) *server.Server {
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
			// OnlineMode gates auth.Encrypt (RSA/CFB8 + Yggdrasil hasJoined) vs
			// offline.NameToUUID in AcceptLogin. Threaded from the --online-mode operator
			// flag; default false keeps offline local-dev the byte-identical default.
			OnlineMode: onlineMode,
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
	onlineMode := flag.Bool("online-mode", false, "authenticate + encrypt logins against Mojang (default false = offline local-dev)")
	flag.Parse()

	// online-mode is controlled primarily by the --online-mode flag; SULFUR_ONLINE_MODE=1
	// is an OR'd env escape hatch mirroring the SULFUR_SUPERFLAT/SULFUR_DEBUG pattern in
	// this file. Default stays offline so a bare `sulfur` run is byte-identical to today.
	online := *onlineMode || os.Getenv("SULFUR_ONLINE_MODE") == "1"

	// SULFUR_PPROF=1 exposes net/http/pprof on :6060 for live CPU/heap profiling
	// (dev/diagnostic only — off by default, never reached in a bare prod run).
	if os.Getenv("SULFUR_PPROF") == "1" {
		go func() {
			slog.Info("pprof listening on :6060 (SULFUR_PPROF=1)")
			_ = http.ListenAndServe("localhost:6060", nil)
		}()
	}

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
	// spawnPoint is the SAFE fresh-spawn world position (the air cell the player's feet occupy,
	// ON a standable floor). For the noise generator it is the ported vanilla PlayerSpawnFinder
	// result over the FULLY-DECORATED spawn chunk (17-06 spawn-inside-a-block fix); for Superflat
	// it stays the center column at spawnSurfaceY+2. It is threaded into NewGameTick so BOTH the
	// join bootstrap and the in-game respawn place the player at the safe column (which may NOT be
	// (8,8) when a tree/structure occupies the center), instead of a blind (8.5, surfaceY+2, 8.5).
	spawnPoint := server.SpawnPoint{
		X: 8.5, // chunk (0,0) block center
		Y: float64(overworldSurfaceY + 2),
		Z: 8.5,
	}
	if os.Getenv("SULFUR_SUPERFLAT") == "1" {
		gen = world.NewSuperflat(overworldSecs, overworldMinY, overworldSurfaceY)
		log.Printf("SULFUR_SUPERFLAT=1: Superflat stub generator (flat top y=%d) — noise terrain disabled", overworldSurfaceY)
	} else {
		// The NoiseGenerator builds its decorationData ONCE (buildDecorationData) — the
		// parsed feature registry + FeatureSorter + per-biome feature lists with ALL
		// feature bodies registered via init(): ores + ground cover (Phase 12), the full
		// tree set incl. special biomes (13-01/02/03), and the monster_room dungeon
		// (13-04). The worker's tryDecorate runs Decorate(view) -> applyBiomeDecoration on
		// the carved 3x3 before sealing each streamed chunk, so the chunks a real client
		// receives are FULLY decorated (per-biome vegetation + decoration ores + occasional
		// dungeons), deterministically per *seed. This is the streamed-chunk path the
		// FEAT-06 visual gate evaluates — NO new wire surface, the v1-sealed chunk format
		// is unchanged (registering the dungeon body went live with no worker/wire rewire).
		ng := world.NewNoiseGenerator(*seed, overworldSecs, overworldMinY)
		// 17-06 spawn-inside-a-block fix: SpawnPos runs the ported vanilla PlayerSpawnFinder over
		// the FULLY-DECORATED spawn chunk (features + structures), returning the first STANDABLE
		// column (the air cell ON a non-fluid floor), which may not be the (8,8) center when a tree
		// or structure occupies it. We thread the full safe (x,y,z) into the bootstrap so the player
		// lands ON clear ground instead of embedded in decoration. spawnSurfaceY (the scalar fed to
		// the tick's SetSpawn for the legacy respawn-Y path) stays the standable floor block world-Y.
		sp := ng.SpawnPos(level.ChunkPos{0, 0})
		if sp.Found {
			spawnPoint = server.SpawnPoint{X: sp.X, Y: sp.Y, Z: sp.Z}
			spawnSurfaceY = int(sp.Y) - 1 // standable floor block (feet-1); SetSpawn/+2 lands feet above it
		} else {
			// Void/ocean spawn chunk: fall back to the terrain WorldSurface top at the center column.
			spawnSurfaceY = ng.SpawnSurfaceY(level.ChunkPos{0, 0})
			spawnPoint = server.SpawnPoint{
				X: float64(0<<4) + 8.5,
				Y: float64(spawnSurfaceY + 2),
				Z: float64(0<<4) + 8.5,
			}
		}
		gen = ng
		log.Printf("full-parity NoiseGenerator armed (seed=%d): SAFE spawn (%.1f, %.1f, %.1f) found=%v; full feature pipeline live (per-biome trees + ground cover + decoration ores + dungeons)", *seed, spawnPoint.X, spawnPoint.Y, spawnPoint.Z, sp.Found)
	}
	// SUB-PERSIST: chunk persistence is OPT-IN via SULFUR_PERSIST_CHUNKS=1 so the default run keeps
	// the v1 always-generate behaviour (regionDir "" => the worker never region-loads, every chunk
	// is freshly generated — the deterministic-MVP contract many tests rely on). When enabled, BOTH
	// the worker (READ) and the ChunkSaver (WRITE) target worldDir/region, so a saved chunk reloads
	// through the SAME tryRegion path with no extra wiring (load/save symmetric). The save loop is
	// started below alongside the player save loop.
	chunkRegionDir := ""
	if os.Getenv("SULFUR_PERSIST_CHUNKS") == "1" {
		chunkRegionDir = filepath.Join(worldDir, "region")
		log.Printf("SULFUR_PERSIST_CHUNKS=1: chunk persistence ENABLED (region dir %q) — edited chunks flush to disk and reload on revisit", chunkRegionDir)
	}
	worker := world.NewWorker(gen, chunkRegionDir, workerBuf)
	mgr := world.NewChunkManager()
	tick.SetWorld(mgr, worker)
	// SUB-PERSIST: wire the off-tick chunk-save consumer (a no-op disabled saver when chunkRegionDir
	// is "" — the default). It writes to the SAME region dir the worker reads, so the round-trip
	// closes through the existing load path. Started in its own goroutine below (RunChunkSaveLoop).
	chunkSaver := world.NewChunkSaver(chunkRegionDir)
	tick.SetChunkSaver(chunkSaver)
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

	// PLUGIN-02 (Plan 22): load the plugin host from plugins/ (TOML-manifest dirs, register-once
	// hooks) and wire it into the tick BEFORE Run, so the discrete gameplay seams (break/place/
	// join/leave/spawn/death/damage/tick) dispatch to plugin hooks. A missing/empty plugins/ dir is
	// a no-op (the seams stay nil-guarded no-ops). Then start the FULL hot-reload watcher: on a
	// .star/.toml change it rebuilds a fresh Manager OFF-tick and publishes it on the tick's swap
	// channel, which drainRegistrations installs ON the tick goroutine (TICK-05 — never a mid-Emit
	// map mutation). The watcher is closed on ctx cancel so it does not leak past shutdown.
	const pluginsDir = "plugins"
	// PLUGIN-05 (Plan 25-02): a SINGLE runtime Manager always carries the embedded vanilla `crafting`
	// recipe plugin so a DEFAULT server crafts out-of-the-box THROUGH the plugin path — even with no
	// operator plugins/ dir (the embedded-default decision, CONTEXT D-4). LoadCraftingPlugin parses the
	// vanilla recipe table, injects it, and loads the embedded matcher; a failure here is FATAL (a
	// server that cannot craft is broken, not degraded). The operator plugins/ dir then loads ON TOP of
	// the same Manager — an operator recipe plugin (e.g. customrecipe) registers the FINAL matcher
	// (last-loaded wins) that falls through to the vanilla table, so vanilla + custom both craft.
	pluginMgr := host.New()
	// PLUGIN-06 (Plan 26-02): wire the OPT-IN CPython runtime + off-tick dispatch
	// BEFORE LoadDir, so a runtime="python" plugin loads and its hooks fire OFF-TICK
	// via the tick's pluginPool. On the default (no `-tags python`) build WirePython
	// is a no-op (async_python_stub.go) → python manifests are skipped gracefully and
	// the binary stays pure-Go static (CGO=0). On `-tags python` it registers the
	// gopy-backed loader + routes hooks to TickLoop.submitPythonHook.
	server.WirePython(tick, pluginMgr)
	if err := server.LoadCraftingPlugin(pluginMgr); err != nil {
		log.Fatalf("crafting boot-load failed (the server cannot craft): %v", err)
	}
	log.Printf("crafting: bundled 1:1 vanilla recipe plugin boot-loaded (default crafting is plugin-driven)")
	if _, err := os.Stat(pluginsDir); err == nil {
		if err := pluginMgr.LoadDir(pluginsDir); err != nil {
			log.Printf("plugin host: load %s failed (continuing with the embedded crafting plugin only): %v", pluginsDir, err)
		} else {
			log.Printf("plugin host: loaded %d plugin(s) from %s/ (hot-reload watcher armed)", pluginMgr.PluginCount(), pluginsDir)
			if w, err := host.NewWatcher(pluginsDir, tick.PluginSwapChan(), nil); err != nil {
				log.Printf("plugin host: hot-reload watcher disabled: %v", err)
			} else {
				go func() {
					<-ctx.Done()
					w.Close()
				}()
			}
		}
	}
	tick.SetPlugins(pluginMgr)
	// PLUGIN-07 (Plan 28-02): install the chat() output sink so a plugin event hook's chat(msg)
	// reaction fans to every player as a ClientboundSystemChat (broadcastSystemChat). The sink
	// fires from Manager.Emit on the tick goroutine (the discrete break/join seams), so the fan
	// is tick-owned (TICK-05). Wired AFTER SetPlugins, before tick.Run. This is the observable-
	// event seam the gate bot decodes (checklist item #4); the bundled gate_events plugin (in
	// plugins/) subscribes on_block_break + on_player_join and reacts via chat().
	tick.InstallChatSink(pluginMgr)
	// PLUGIN-04 (Plan 24-02): BOOT-LOAD the bundled vanilla_pig plugin into a tick-owned mob registry
	// BEFORE tick.Run. The SWAP (server/async.go + debug.go) makes the plugin pig the ONLY pig, so the
	// "vanilla_pig" declaration MUST be live before the first pig can spawn (RESEARCH Pitfall 4). The
	// plugin is //go:embed'd into the server binary, so it is ALWAYS present; a load failure here is
	// FATAL (a swap with no pig is a broken server, not a degraded one — fail loudly at boot).
	if reg, err := server.LoadVanillaMobRegistry(); err != nil {
		log.Fatalf("vanilla mob boot-load failed (a swapped mob has no declaration): %v", err)
	} else {
		tick.SetMobRegistry(reg)
		log.Printf("vanilla mobs: bundled 1:1 pig/cow/sheep/chicken plugins boot-loaded (the only mobs are plugin-driven)")
		// v5: the wandermob (a v4 custom-mob API gate — base_type pig, ONE MOVE goal, no FloatGoal) is
		// NO LONGER boot-loaded. The dogfood is now the REAL vanilla mobs as plugins (vanilla_pig, then
		// cow/sheep/etc. in Phase 34), not a toy custom mob — and a second pig-looking mob with no
		// FloatGoal confused water testing (it sank while the real pig floats). The embed + the API-gate
		// tests remain for a dedicated cleanup; the live server boot-loads only the real vanilla mobs.
	}

	// SUB-PERSIST: the off-tick chunk-save consumer (its own goroutine, like the player save loop).
	// Disabled (SULFUR_PERSIST_CHUNKS != 1) it just waits on ctx — no disk IO. Enabled it drains the
	// tick's immutable chunk snapshots and writes them to region files OFF the tick.
	go chunkSaver.RunChunkSaveLoop(ctx, log.Printf)

	go tick.Run(ctx, inbound)
	go keep.Run(ctx)
	go worker.Run(ctx)

	// The real GamePlay bridges an accepted connection to the running tick + keep-alive.
	// spawnSurfaceY is the derived noise spawn surface (or the Superflat fixed top under
	// SULFUR_SUPERFLAT) so the join bootstrap places the player ON solid ground (T-9-26).
	gp := server.NewGameTick(inbound, tick, keep, spawnSurfaceY, spawnPoint)
	gp.SetWorldDir(worldDir) // ENT-06: load/save player .dat under worldDir
	srv := newServer(gp, online)

	// The "Sulfur listening" startup line is emitted via the embedded *log.Logger to stderr
	// BEFORE the TTY fork installs the slog handler, so in TUI mode it does NOT appear in the
	// viewport (the operator-checkpoint wording notes this is expected). It flows to stderr
	// either way — both branches keep stderr live.
	srv.Logger.Printf("Sulfur listening on %s (protocol %d, %s, online-mode=%v)",
		*addr, server.ProtocolVersion, server.ProtocolName, online)

	// TUI-01 fork (Plan 19-02): decide ONCE at boot whether stdout is an interactive terminal.
	// term.IsTerminal is pure-Go (golang.org/x/term, anchored behind tui.StdoutIsTerminal) so
	// CGO_ENABLED=0 stays clean. Do NOT rely on bubbletea to self-degrade on a non-TTY — the
	// guard is OURS (Pitfall 3).
	if tui.StdoutIsTerminal() {
		// Interactive: run the bubbletea operator console on the MAIN goroutine and the server
		// listener on a goroutine. The console's Enter dispatch hands each typed line to the
		// tick via EnqueueConsoleCommand (a non-blocking tick message — TICK-05 / Pitfall 7),
		// NEVER executing inline. slog now fans every record to BOTH the TUI viewport and stderr
		// (tui.NewHandler(prog)). On tea.Quit / Ctrl-C, prog.Run returns → cancel() unwinds every
		// server goroutine (tick/keepalive/worker/save) via the shared ctx.
		model := tui.New(func(line string) { tick.EnqueueConsoleCommand(line) })
		prog := tea.NewProgram(model, tea.WithContext(ctx))
		slog.SetDefault(slog.New(tui.NewHandler(prog)))
		go func() { _ = srv.Listen(*addr) }()
		if _, err := prog.Run(); err != nil {
			slog.Error("tui exited", "err", err)
		}
		cancel() // tea.Quit / Ctrl-C → stop every server goroutine
		return
	}

	// Headless (Docker/CI/piped stdout): NO bubbletea program. Install the plain-stderr slog
	// handler (tui.NewHandler(nil)) and keep the EXACT blocking log.Fatal(srv.Listen(*addr))
	// behavior this binary has today (Pitfall 4 — byte-for-behavior unchanged).
	slog.SetDefault(slog.New(tui.NewHandler(nil)))
	log.Fatal(srv.Listen(*addr))
}
