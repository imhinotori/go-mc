// Command testbot is a HEADLESS scripted Minecraft client for exercising the local
// Sulfur server's Play-state gameplay (especially water physics) WITHOUT a real
// vanilla client. The real 26.2 client keeps disconnecting underwater; this in-process
// bot reproduces player movement deterministically so the server's ULTRA_DEBUG firehose
// records a controllable "player".
//
// It speaks the exact offline handshake -> login -> configuration -> play sequence the
// Sulfur server expects (mirrored from server/handshake.go, server/login.go,
// server/configuration.go, server/play_join.go, server/subtick.go), reusing the fork's
// own net/packet primitives — NO new dependency, NO server code touched.
//
// Flow (each step verified against the SERVER side, the source of truth for wire shapes):
//
//	Handshake  C->S Handshake(protocol=776, addr, port, intention=2)
//	Login      C->S LoginHello(name, offlineUUID)
//	           S->C [LoginCompression(threshold)]   (server sets threshold=256 BEFORE LoginFinished)
//	           S->C LoginFinished(uuid, name, properties[], sessionId)
//	           C->S LoginAcknowledged
//	Config     S->C SelectKnownPacks                (server BLOCKS for the echo before RegistryData)
//	           C->S ClientInformation (optional)
//	           C->S SelectKnownPacks echo            (the server's SOLE drain-loop exit key)
//	           S->C UpdateEnabledFeatures, RegistryData x N, UpdateTags, FinishConfiguration
//	           C->S FinishConfiguration (acknowledge)
//	           (KeepAlive answered inline if it arrives in config)
//	Play       S->C Login(JoinGame), GameEvent, PlayerPosition(teleportId, x,y,z), abilities, ...
//	           C->S AcceptTeleportation(teleportId)  (WITHOUT this the subtick gate drops all movement)
//	           C->S MovePlayerPos(x,y,z,flags) @ ~20Hz, KeepAlive echoed
//
// CRITICAL protocol details that had to be exact:
//   - Compression: the server (cmd/sulfur compressionThreshold=256) sends
//     ClientboundLoginLoginCompression BEFORE LoginFinished. The bot MUST enable the same
//     threshold on its Conn (Conn.SetThreshold) the instant it sees that packet, or every
//     post-login read is garbage.
//   - Known Packs echo: the server sends SelectKnownPacks then BLOCKS in drainKnownPacksEcho
//     until the bot echoes ServerboundConfigSelectKnownPacks. The body is not consulted, so an
//     empty collection (VarInt 0) is accepted. Registry Data is only sent AFTER the echo.
//   - Teleport confirm: applyInput drops ALL movement until confirmedTeleport, which is set
//     ONLY when the bot echoes the bootstrap PlayerPosition's teleport id via
//     ServerboundAcceptTeleportation. Skipping it = every move packet silently dropped.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	mcnet "github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/offline"
)

const (
	// protocolVersion is the proto-776 the Sulfur login path asserts (server.ProtocolVersion).
	protocolVersion = 776
	// intentionLogin is the handshake Intention value selecting the login path (case 2 in
	// server.AcceptConn). 1 would be a status/list ping.
	intentionLogin = 2
	// tickInterval is the client send cadence: ~20 Hz, matching a real client's movement
	// packet rate (one MovePlayerPos every 50ms).
	tickInterval = 50 * time.Millisecond
)

func main() {
	addr := flag.String("addr", "localhost:25565", "Sulfur server address")
	name := flag.String("name", "TestBot", "offline player name (UUID derived via offline.NameToUUID)")
	mode := flag.String("mode", "hold", "script: hold | walk | swim | dive | goto | wander | gate (PLUGIN-07 visual gate)")
	ticks := flag.Int("ticks", 200, "number of movement ticks to run, then disconnect cleanly")
	tx := flag.Float64("x", 0, "goto target X (or spawn X override)")
	ty := flag.Float64("y", 0, "goto target Y (or spawn Y override)")
	tz := flag.Float64("z", 0, "goto target Z (or spawn Z override)")
	overrideSpawn := flag.Bool("override-spawn", false, "use -x/-y/-z as the starting position instead of the server spawn")
	onGroundFlag := flag.String("onground", "", "onGround flag to send: true|false (default: true for walk/hold, false for swim/dive)")
	cmdStr := flag.String("cmd", "", "a ServerboundChatCommand to send once on entering Play, e.g. 'tp -23 58 17' (no leading slash)")
	probe := flag.String("probe", "", "DIAGNOSTIC: world 'x z' column to watch; decodes received chunk + block-update packets and reports the WATER state at that column (e.g. -probe \"-23 17\")")
	flag.Parse()

	log.SetFlags(log.Ltime)
	log.SetPrefix("[testbot] ")

	b := &bot{
		name: *name,
		mode: *mode,
	}

	// -probe "x z" arms the client-side water DIAGNOSTIC: the reader decodes the chunk and
	// block-update packets it receives and reports what the CLIENT actually holds at this
	// world column. This is purely additive — it never alters movement or any send path.
	if *probe != "" {
		var px, pz int
		if _, err := fmt.Sscanf(*probe, "%d %d", &px, &pz); err != nil {
			log.Fatalf("invalid -probe %q (want \"x z\", e.g. \"-23 17\"): %v", *probe, err)
		}
		b.probeActive = true
		b.probeX, b.probeZ = int32(px), int32(pz)
		b.probeCX, b.probeCZ = int32(px)>>4, int32(pz)>>4
		log.Printf("probe armed: world column (%d,%d) -> chunk (%d,%d), watching Y=56..64",
			px, pz, b.probeCX, b.probeCZ)
	}

	if err := b.dial(*addr); err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer b.conn.Close()
	log.Printf("connected to %s", *addr)

	if err := b.handshake(*addr); err != nil {
		log.Fatalf("handshake: %v", err)
	}
	if err := b.login(); err != nil {
		log.Fatalf("login: %v", err)
	}
	if err := b.configure(); err != nil {
		log.Fatalf("configuration: %v", err)
	}
	if err := b.reachPlay(); err != nil {
		log.Fatalf("play bootstrap: %v", err)
	}

	log.Printf("reached play, teleportId=%d, spawn=(%.3f, %.3f, %.3f)", b.teleportID, b.x, b.y, b.z)

	// Send a one-shot ServerboundChatCommand (e.g. "tp -23 58 17") so the server-side /tp moves the
	// bot to a water column for the physics repro WITHOUT walking there. After the command the
	// server re-teleports the bot (ClientboundPlayerPosition); the background reader snaps b.x/y/z
	// to that and re-arms the confirm, then the movement loop runs from the new position.
	if *cmdStr != "" {
		if err := b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundChatCommand), pk.String(*cmdStr))); err != nil {
			log.Fatalf("send chat command: %v", err)
		}
		log.Printf("sent command: /%s", *cmdStr)
		time.Sleep(500 * time.Millisecond) // let the server process + re-teleport before moving
	}

	// Override the starting position if requested (e.g., to start INSIDE a water column for
	// the water-physics repro without walking there first).
	if *overrideSpawn {
		b.x, b.y, b.z = *tx, *ty, *tz
		log.Printf("spawn overridden to (%.3f, %.3f, %.3f)", b.x, b.y, b.z)
	}

	// PLUGIN-07 VISUAL GATE (-mode gate, cmd/testbot/gate.go): drive the 4-item plugin-system
	// checklist + assert on observed packets, then os.Exit(0) on all-pass / os.Exit(1) on any fail.
	// It owns its own reader + writer loop (runGate), so it bypasses the scripted movement loop below.
	if *mode == "gate" {
		b.runGate() // never returns (os.Exit)
		return
	}

	onGround := defaultOnGround(*mode)
	switch *onGroundFlag {
	case "true":
		onGround = true
	case "false":
		onGround = false
	case "":
		// keep mode default
	default:
		log.Fatalf("invalid -onground %q (want true|false)", *onGroundFlag)
	}

	if err := b.runMovement(*mode, *ticks, onGround, *tx, *ty, *tz); err != nil {
		log.Fatalf("movement loop: %v", err)
	}

	log.Printf("script complete after %d ticks, disconnecting cleanly", *ticks)
}

// defaultOnGround picks the natural onGround flag per movement mode: swim/dive are airborne
// (the client is not standing on a floor) so they default false; walk/hold/goto default true.
func defaultOnGround(mode string) bool {
	switch mode {
	case "swim", "dive":
		return false
	default:
		return true
	}
}

// bot holds the live connection plus the decoded player identity/position.
//
// Once the bot reaches Play it runs TWO goroutines, exactly like a real client: the main
// goroutine WRITES movement at 20Hz, and a background reader goroutine continuously READS
// every clientbound packet (answering KeepAlive, snapping to server re-teleports, logging
// labels). The continuous reader is load-bearing: at join the server streams the full chunk
// view ring through a per-connection outbound queue bounded at 256 (server/gameplay_tick.go).
// If the bot does not drain that queue fast enough the server does drop-and-disconnect
// (Client.Send -> Close). A dedicated reader keeps the queue empty so the bot stays connected.
//
// mu guards the position fields (x,y,z,yaw,pitch) shared between the writer (movement loop)
// and the reader (server re-teleport snap). teleportID and identity are set before the reader
// starts and never mutated after, so they need no lock.
type bot struct {
	conn *mcnet.Conn
	name string
	mode string

	teleportID int32

	mu         sync.Mutex
	x, y, z    float64
	yaw, pitch float32

	// fatal records the first reader-goroutine error (e.g., server disconnect) so the
	// writer loop can stop and report it. closed signals the reader to exit.
	fatal  atomic.Pointer[error]
	closed atomic.Bool

	// probe* drive the client-side water DIAGNOSTIC (-probe "x z"). When probeActive, the
	// reader decodes ClientboundLevelChunkWithLight + ClientboundBlockUpdate and reports the
	// block state at the watched column. probeX/probeZ are the WORLD column; probeCX/probeCZ
	// are its chunk coords (used to match the chunk packet's Int x, Int z header). These are
	// set once before the reader starts and never mutated after, so they need no lock.
	probeActive      bool
	probeX, probeZ   int32
	probeCX, probeCZ int32

	// wander* drive the NPC random-walk (-mode wander). centerX/Z is the spawn anchor the NPC
	// roams around (set once on entering Play); tgtX/Z is the current wander target it drifts
	// toward; rng is the NPC's randomness (seeded from the clock so each run differs). heldSlot
	// is the hotbar slot it last selected. All mutated only on the writer goroutine inside the
	// movement loop, so they need no lock beyond the existing position mu they sit next to.
	wanderInit       bool
	centerX, centerZ float64
	tgtX, tgtZ       float64
	rng              *rand.Rand
	heldSlot         int32

	// blockSeq is the predicted-block-change sequence the client increments on each break/place
	// (ServerboundPlayerAction / ServerboundUseItemOn carry it; the server acks it back). digX/Y/Z
	// is the cell the NPC is currently breaking so the place phase re-targets the same spot. broke
	// gates the place: only re-place a cell we actually broke. Writer-goroutine only.
	blockSeq         int32
	digX, digY, digZ int
	broke            bool

	// gate* are the PLUGIN-07 visual-gate OBSERVATION state (-mode gate, cmd/testbot/gate.go). The
	// readLoop (reader goroutine) RECORDS what it decodes off the wire into these; runGate (the
	// main goroutine) READS them between scenario steps to assert each checklist item observed its
	// SPECIFIC effect (the false-pass guard — threat T-28-02). gateMu guards every field below
	// because the reader writes and runGate reads them concurrently. They are populated ONLY when
	// b.mode == "gate" (the cases are additive no-ops in every other mode).
	gateMu        sync.Mutex
	addEntities   map[int32]int32  // entity id -> entity type id (every AddEntity the bot observed)
	moveCounts    map[int32]int    // entity id -> count of move-entity packets (proves the mob MOVES)
	sawHeadRot    map[int32]bool   // entity id -> saw a SetEntityData / RotateHead (headrot/metadata)
	sawOpenScreen bool             // a crafting menu opened (ClientboundOpenScreen)
	craftWindow   int32            // the windowId the last ClientboundOpenScreen allocated (crafting menu)
	craftResults  []craftResultObs // each non-empty result slot the bot observed in the crafting window
	sawGateChat   bool             // observed a ClientboundSystemChat containing the "gate_events:" marker
	gateChatTexts []string         // every decoded SystemChat text (for the transcript)
	chunksSeen    int              // count of ClientboundLevelChunkWithLight observed (world-stream readiness)
}

// craftResultObs is one observed crafting result-slot population: the menu slot that received a
// non-empty stack + the item id + count. runGate asserts a result slot (menu slot 0) populated.
type craftResultObs struct {
	slot   int16
	itemID int32
	count  int32
}

func (b *bot) dial(addr string) error {
	conn, err := mcnet.DialMC(addr)
	if err != nil {
		return err
	}
	b.conn = conn
	return nil
}

// handshake sends the state-local id 0 Handshake (server/handshake.go reads
// VarInt protocol, String address, UShort port, VarInt intention) selecting the login path.
func (b *bot) handshake(addr string) error {
	host, port := splitHostPort(addr)
	const handshakeID = 0x00 // state-local id in the Handshake state
	return b.conn.WritePacket(pk.Marshal(
		handshakeID,
		pk.VarInt(protocolVersion),
		pk.String(host),
		pk.UnsignedShort(port),
		pk.VarInt(intentionLogin),
	))
}

// login walks the offline login exchange. The server (MojangLoginHandler, OnlineMode=false)
// sends LoginCompression(threshold) BEFORE LoginFinished because cmd/sulfur sets threshold=256,
// then LoginFinished(uuid, name, properties[], sessionId), then waits for LoginAcknowledged.
func (b *bot) login() error {
	id := offline.NameToUUID(b.name)
	// C->S LoginHello: String name, UUID profileId.
	if err := b.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundLoginHello),
		pk.String(b.name),
		pk.UUID(id),
	)); err != nil {
		return fmt.Errorf("send LoginHello: %w", err)
	}

	for {
		var p pk.Packet
		if err := b.conn.ReadPacket(&p); err != nil {
			return fmt.Errorf("read login reply: %w", err)
		}
		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundLoginLoginCompression:
			// Enable compression at the server's threshold IMMEDIATELY — every subsequent
			// read/write on this Conn is now in the compressed framing (Conn.SetThreshold).
			var threshold pk.VarInt
			if err := p.Scan(&threshold); err != nil {
				return fmt.Errorf("scan LoginCompression: %w", err)
			}
			b.conn.SetThreshold(int(threshold))
			log.Printf("compression enabled (threshold=%d)", int(threshold))

		case packetid.ClientboundLoginLoginFinished:
			// LoginFinished == GameProfile(uuid, name, properties[]) + sessionId UUID. We
			// only need to advance state; decode the leading uuid+name for the log and skip
			// the rest (the body is consumed by ReadPacket already).
			var gotID pk.UUID
			var gotName pk.String
			_ = p.Scan(&gotID, &gotName) // best-effort; ignore trailing properties/sessionId
			log.Printf("login success: name=%s uuid=%s", string(gotName), formatUUID(gotID))
			// C->S LoginAcknowledged — the server explicitly blocks reading this before
			// transitioning to Configuration.
			if err := b.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundLoginLoginAcknowledged),
			)); err != nil {
				return fmt.Errorf("send LoginAcknowledged: %w", err)
			}
			return nil

		case packetid.ClientboundLoginLoginDisconnect:
			return fmt.Errorf("login disconnect: %s", decodeText(p))

		default:
			log.Printf("login: ignoring clientbound id %d", p.ID)
		}
	}
}

// configure drives the Configuration state. The server's drain loop sends SelectKnownPacks
// FIRST and BLOCKS until the bot echoes ServerboundConfigSelectKnownPacks (the sole exit key),
// only then streaming RegistryData/Tags/FinishConfiguration. So the bot must: read until it
// sees the server's SelectKnownPacks, echo it back, then drain the registry stream until
// FinishConfiguration, then acknowledge.
func (b *bot) configure() error {
	echoed := false
	for {
		var p pk.Packet
		if err := b.conn.ReadPacket(&p); err != nil {
			return fmt.Errorf("read config packet: %w", err)
		}
		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundConfigSelectKnownPacks:
			// Echo back an EMPTY known-packs list (VarInt 0). The server does not consult the
			// body — the echo's mere arrival is its drain-loop exit key. Sending the echo
			// unblocks the server to stream RegistryData. ClientInformation is optional, so we
			// skip it to keep the exchange minimal.
			if err := b.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundConfigSelectKnownPacks),
				pk.VarInt(0), // empty Array<KnownPack> — body is not consulted by the server
			)); err != nil {
				return fmt.Errorf("send SelectKnownPacks echo: %w", err)
			}
			echoed = true
			log.Printf("config: echoed SelectKnownPacks (empty), draining registry stream")

		case packetid.ClientboundConfigKeepAlive:
			// Answer config-state keep-alive inline (echo the Long) so we are not kicked
			// mid-configuration.
			var id pk.Long
			if err := p.Scan(&id); err != nil {
				return fmt.Errorf("scan config keep-alive: %w", err)
			}
			if err := b.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundConfigKeepAlive), id,
			)); err != nil {
				return fmt.Errorf("answer config keep-alive: %w", err)
			}

		case packetid.ClientboundConfigPing:
			// Ping carries an Int id; Pong echoes it. (The server treats Pong as a no-op but
			// answering it is correct and harmless.)
			var id pk.Int
			if err := p.Scan(&id); err == nil {
				_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundConfigPong), id))
			}

		case packetid.ClientboundConfigFinishConfiguration:
			// Acknowledge FinishConfiguration — the server BLOCKS reading this before it
			// transitions the connection to Play.
			if !echoed {
				log.Printf("config: WARNING FinishConfiguration before SelectKnownPacks echo")
			}
			if err := b.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundConfigFinishConfiguration),
			)); err != nil {
				return fmt.Errorf("send FinishConfiguration ack: %w", err)
			}
			log.Printf("config: finished, entering play")
			return nil

		case packetid.ClientboundConfigDisconnect:
			return fmt.Errorf("config disconnect: %s", decodeText(p))

		case packetid.ClientboundConfigRegistryData,
			packetid.ClientboundConfigUpdateTags,
			packetid.ClientboundConfigUpdateEnabledFeatures,
			packetid.ClientboundConfigCustomPayload:
			// Registry/tags/features stream — consumed and discarded; the bot does not need a
			// real client registry to move.

		default:
			// Any other config packet (server links, custom report details, code of conduct,
			// etc.) is discarded.
		}
	}
}

// reachPlay reads the Play-state join bootstrap until it sees ClientboundPlayerPosition,
// decodes the teleport id + x/y/z (mirroring writePlayerPositionPacket: VarInt id, Double
// x,y,z, Double dx,dy,dz, Float yaw,pitch, Int relativeFlags), stores them, and sends
// ServerboundAcceptTeleportation(teleportId). WITHOUT that confirm the server's subtick gate
// (confirmedTeleport) drops every movement packet the bot later sends.
func (b *bot) reachPlay() error {
	for {
		var p pk.Packet
		if err := b.conn.ReadPacket(&p); err != nil {
			return fmt.Errorf("read play bootstrap: %w", err)
		}
		b.logClientbound(p)
		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundPlayerPosition:
			var tpID pk.VarInt
			var x, y, z pk.Double
			var dx, dy, dz pk.Double
			var yaw, pitch pk.Float
			var relFlags pk.Int
			if err := p.Scan(&tpID, &x, &y, &z, &dx, &dy, &dz, &yaw, &pitch, &relFlags); err != nil {
				return fmt.Errorf("scan PlayerPosition: %w", err)
			}
			b.teleportID = int32(tpID)
			b.x, b.y, b.z = float64(x), float64(y), float64(z)
			b.yaw, b.pitch = float32(yaw), float32(pitch)
			// Confirm the teleport — opens the subtick movement gate.
			if err := b.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundAcceptTeleportation),
				pk.VarInt(b.teleportID),
			)); err != nil {
				return fmt.Errorf("send AcceptTeleportation: %w", err)
			}
			log.Printf("confirmed teleport id=%d", b.teleportID)
			return nil

		case packetid.ClientboundKeepAlive:
			if err := b.answerPlayKeepAlive(p); err != nil {
				return err
			}

		case packetid.ClientboundDisconnect:
			return fmt.Errorf("disconnected during bootstrap: %s", decodeText(p))
		}
	}
}

// runMovement runs the scripted movement loop on the MAIN goroutine while a background reader
// (started here) continuously drains everything the server pushes. Each tick it computes the
// next position per the mode, sends ServerboundMovePlayerPos (Double x,y,z + UnsignedByte
// flags, bit 0x01 = onGround, exactly as server/subtick.go decodes it), prints a per-tick
// line, and sleeps 50ms. The reader (readLoop) answers KeepAlive and snaps to re-teleports so
// the server's outbound queue never fills (the cause of the early drop-and-disconnect).
func (b *bot) runMovement(mode string, ticks int, onGround bool, gx, gy, gz float64) error {
	flags := pk.UnsignedByte(0)
	if onGround {
		flags |= movementFlagOnGround
	}

	// Start the continuous reader. It owns ALL reads from here on; the main goroutine only
	// writes. This is the architecture that keeps the bot alive: the server streams the chunk
	// view ring through a 256-deep per-connection queue at join, and only a continuous drain
	// keeps that queue from overflowing into a drop-and-disconnect.
	go b.readLoop()

	// swim raises Y for the first half of the run then holds (a client predicting buoyancy as
	// it surfaces); the swim-up duration is half the run, min 1.
	swimUpTicks := ticks / 2
	if swimUpTicks < 1 {
		swimUpTicks = 1
	}

	for i := 0; i < ticks; i++ {
		// Bail out immediately if the reader saw the server tear the connection down.
		if errp := b.fatal.Load(); errp != nil {
			return *errp
		}

		b.mu.Lock()
		switch mode {
		case "hold":
			// no position change — exercise idle sink/float
		case "walk":
			b.x += 0.2
		case "swim":
			if i < swimUpTicks {
				b.y += 0.04
			}
		case "dive":
			b.y -= 0.1
		case "goto":
			b.x = stepToward(b.x, gx, 0.2)
			b.y = stepToward(b.y, gy, 0.2)
			b.z = stepToward(b.z, gz, 0.2)
		case "wander":
			// Random-walk an "NPC" around its spawn center: drift toward a wander target, turning
			// the yaw to face the heading, picking a NEW target when close. The random ACTIONS
			// (swing / hotbar-select / place / break) are sent below so a real client sees the NPC
			// do things with its kit items.
			b.wanderStep()
		default:
			b.mu.Unlock()
			return fmt.Errorf("unknown -mode %q (want hold|walk|swim|dive|goto|wander)", mode)
		}
		x, y, z := b.x, b.y, b.z
		yaw, pitch := b.yaw, b.pitch
		b.mu.Unlock()

		if mode == "wander" {
			// PosRot so the NPC's body/head turn as it walks (others see it look around).
			if err := b.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundMovePlayerPosRot),
				pk.Double(x), pk.Double(y), pk.Double(z),
				pk.Float(yaw), pk.Float(pitch),
				flags,
			)); err != nil {
				return fmt.Errorf("send MovePlayerPosRot: %w", err)
			}
			b.wanderActions(i)
			if i%20 == 0 {
				fmt.Printf("npc tick %d: pos=(%.2f, %.2f, %.2f) yaw=%.0f\n", i, x, y, z, yaw)
			}
			time.Sleep(tickInterval)
			continue
		}

		// C->S MovePlayerPos: Double x,y,z + UnsignedByte flags.
		if err := b.conn.WritePacket(pk.Marshal(
			int32(packetid.ServerboundMovePlayerPos),
			pk.Double(x), pk.Double(y), pk.Double(z),
			flags,
		)); err != nil {
			return fmt.Errorf("send MovePlayerPos: %w", err)
		}
		fmt.Printf("bot tick %d: pos=(%.4f, %.4f, %.4f) onGround=%v\n", i, x, y, z, onGround)

		time.Sleep(tickInterval)
	}

	// Clean stop: signal the reader to exit and close the socket. A reader read error after
	// closed=true is expected and not reported as a failure.
	b.closed.Store(true)
	b.conn.Close()
	return nil
}

// wanderStep advances the NPC one tick of its random walk: lazily anchor the roam center at the
// spawn, drift toward the current wander target, and pick a fresh target (within a radius of the
// center) when close. The yaw is turned to face the heading so the body/head visibly track the
// walk. Called under b.mu (the writer holds it across the position update), so it mutates the
// position + wander state directly. The NPC stays near spawn (radius-bounded) so a watching player
// can see it the whole time. Held under b.mu by the caller.
func (b *bot) wanderStep() {
	if b.rng == nil {
		b.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if !b.wanderInit {
		b.centerX, b.centerZ = b.x, b.z
		b.tgtX, b.tgtZ = b.x, b.z
		b.wanderInit = true
	}
	const wanderRadius = 8.0 // how far from the spawn anchor the NPC roams
	const stepSpeed = 0.15   // blocks per tick (a calm stroll)
	dx := b.tgtX - b.x
	dz := b.tgtZ - b.z
	dist := math.Hypot(dx, dz)
	if dist < 0.5 {
		// Reached the target — pick a new random point within the roam radius of the center.
		ang := b.rng.Float64() * 2 * math.Pi
		r := b.rng.Float64() * wanderRadius
		b.tgtX = b.centerX + math.Cos(ang)*r
		b.tgtZ = b.centerZ + math.Sin(ang)*r
		return
	}
	b.x += dx / dist * stepSpeed
	b.z += dz / dist * stepSpeed
	// Face the heading: yaw is degrees, 0 = +Z, increasing clockwise (Minecraft convention:
	// yaw = atan2(-dx, dz) in degrees).
	b.yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
}

// wanderActions sends the NPC's random ACTIONS for tick i (outside the position lock): an arm
// swing now and then, a hotbar reselect (so it visibly switches items), and an occasional
// use/place + dig so a watching player sees it do things with its kit. All are best-effort: a
// write error is swallowed (the movement loop's fatal check tears down on a real disconnect).
func (b *bot) wanderActions(i int) {
	if b.rng == nil {
		return
	}
	// ~every 15 ticks: swing the main hand (ClientboundAnimate to observers, once that lands).
	if i%15 == 0 {
		_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundSwing), pk.VarInt(0))) // 0 = main hand
	}
	// ~every 40 ticks: reselect a random hotbar slot (kit fills hotbar 0..6) so it switches items.
	if i%40 == 7 {
		b.heldSlot = int32(b.rng.Intn(7))
		_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundSetCarriedItem), pk.Short(b.heldSlot)))
	}
	// ~every 60 ticks: a "use item" (right-click in the air) — drinks/eats if the held slot is food.
	if i%60 == 23 {
		// ServerboundUseItem: VarInt hand, VarInt sequence, Float yaw, Float pitch.
		b.mu.Lock()
		yaw, pitch := b.yaw, b.pitch
		b.mu.Unlock()
		_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundUseItem),
			pk.VarInt(0), pk.VarInt(0), pk.Float(yaw), pk.Float(pitch)))
	}

	// BREAK -> (auto-pickup) -> PLACE cycle on a 100-tick period so a watcher sees the full loop.
	// START and STOP are SEPARATED by 15 ticks so the server's dig-time model accrues real
	// destroy progress (a same-tick START+STOP only schedules a delayed-destroy; spacing them
	// lets the block break cleanly and an observer sees the crack overlay advance):
	//   %100 == 30: select a BLOCK hotbar slot (kit slots 4..7: cobble/planks/torch/dirt) + START
	//               digging the cell one block under the NPC's feet (reachable, re-placeable).
	//   %100 == 31..45: keep swinging (the dig continues server-side; the swing is the visible arm).
	//   %100 == 45: STOP digging (completes the break for a low-hardness floor block).
	//   %100 == 80: place a block back into the broken cell (the auto-picked-up drop is in hand).
	switch i % 100 {
	case 30:
		// Hotbar slots 3..6 hold the kit's block items (cobble/planks/torch/dirt at menu 39..42);
		// pick a placeable BLOCK so the re-place has something in hand. (Slots 0..2 are food, 7 is
		// empty — both excluded.)
		b.heldSlot = int32(3 + b.rng.Intn(4))
		_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundSetCarriedItem), pk.Short(b.heldSlot)))
		b.startBreakBelow()
	case 33, 36, 39, 42:
		// Keep the arm swinging while the dig is in progress (cosmetic + matches a real client's
		// continuous-swing while holding left-click).
		_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundSwing), pk.VarInt(0)))
	case 45:
		b.stopBreak()
	case 80:
		if b.broke {
			b.placeBlockBack()
		}
	}
}

// startBreakBelow sends START_DESTROY for a HORIZONTAL-NEIGHBOR floor block (the cell one east
// of where the NPC stands, at the same ground level — i.e. the block under the feet, offset +1
// in X). Breaking a neighbor (not the cell under the feet) keeps the NPC from falling into the
// hole AND keeps the broken cell clear of the player AABB so the re-place is not rejected for
// intersecting the placer. The server begins dig-time accrual; stopBreak finishes it. Records
// the cell so placeBlockBack re-fills it. ServerboundPlayerAction wire: VarInt action, Position
// pos, UByte direction, VarInt sequence.
func (b *bot) startBreakBelow() {
	b.mu.Lock()
	fx, fy, fz := b.x, b.y, b.z
	b.mu.Unlock()
	bx := int(math.Floor(fx)) + 1 // ONE EAST: a neighbor floor cell, not the one we stand on
	by := int(math.Floor(fy)) - 1 // ground level (the block our feet rest on, shifted east)
	bz := int(math.Floor(fz))
	b.digX, b.digY, b.digZ = bx, by, bz
	pos := pk.Position{X: bx, Y: by, Z: bz}
	const faceUp = 1 // Direction.UP — hit the top face

	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundSwing), pk.VarInt(0)))
	b.blockSeq++
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundPlayerAction),
		pk.VarInt(0), pos, pk.UnsignedByte(faceUp), pk.VarInt(b.blockSeq))) // START_DESTROY=0
	b.broke = true
	log.Printf("[npc] start breaking block at (%d,%d,%d)", bx, by, bz)
}

// stopBreak sends STOP_DESTROY for the cell startBreakBelow began digging, completing the break
// for a low-hardness floor block (the server's getDestroyProgress * (elapsed+1) >= 0.7f gate).
func (b *bot) stopBreak() {
	if !b.broke {
		return
	}
	pos := pk.Position{X: b.digX, Y: b.digY, Z: b.digZ}
	const faceUp = 1
	b.blockSeq++
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundPlayerAction),
		pk.VarInt(2), pos, pk.UnsignedByte(faceUp), pk.VarInt(b.blockSeq))) // STOP_DESTROY=2
	log.Printf("[npc] stop breaking block at (%d,%d,%d)", b.digX, b.digY, b.digZ)
}

// placeBlockBack re-places a block into the broken neighbor cell by clicking the floor one below
// it (the block at digY-1) on its UP face, so the placement lands back in the now-air broken cell.
// ServerboundUseItemOn wire: VarInt hand, Position pos, VarInt direction, Float cursorX/Y/Z,
// Boolean insideBlock, Boolean worldBorderHit, VarInt sequence.
func (b *bot) placeBlockBack() {
	below := pk.Position{X: b.digX, Y: b.digY - 1, Z: b.digZ}
	const faceUp = 1 // place onto the top face of the block beneath the hole -> fills the hole

	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundSwing), pk.VarInt(0)))
	b.blockSeq++
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundUseItemOn),
		pk.VarInt(0), below, pk.VarInt(faceUp),
		pk.Float(0.5), pk.Float(1.0), pk.Float(0.5), // cursor at the top-center of the clicked face
		pk.Boolean(false), pk.Boolean(false), pk.VarInt(b.blockSeq)))
	b.broke = false
	log.Printf("[npc] place block back at (%d,%d,%d)", b.digX, b.digY, b.digZ)
}

// movementFlagOnGround mirrors server/subtick.go: bit 0x01 of the trailing packed flags byte
// of every ServerboundMovePlayer* packet means "on ground".
const movementFlagOnGround pk.UnsignedByte = 0x01

// readLoop is the bot's dedicated reader goroutine. It blocks reading every clientbound packet
// for the life of the Play session: answering KeepAlive (so the watchdog never times the bot
// out), re-confirming + snapping to server re-teleports, logging health, and recording the
// first fatal error (a server disconnect) for the writer to observe. Running reads on their
// own goroutine — decoupled from the 50ms write cadence — is what keeps the server's bounded
// outbound queue drained and the connection alive.
func (b *bot) readLoop() {
	for {
		var p pk.Packet
		if err := b.conn.ReadPacket(&p); err != nil {
			if b.closed.Load() {
				return // expected: we initiated the clean disconnect
			}
			e := fmt.Errorf("server closed connection (read): %w", err)
			b.fatal.CompareAndSwap(nil, &e)
			return
		}
		b.logClientbound(p)

		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundKeepAlive:
			if err := b.answerPlayKeepAlive(p); err != nil {
				b.fatal.CompareAndSwap(nil, &err)
				return
			}
		case packetid.ClientboundPlayerPosition:
			// A server-initiated re-teleport (e.g., anti-cheat correction). Re-confirm so we
			// stay un-gated and snap our local position to the server's authoritative one.
			var tpID pk.VarInt
			var x, y, z pk.Double
			var dx, dy, dz pk.Double
			var yaw, pitch pk.Float
			var relFlags pk.Int
			if err := p.Scan(&tpID, &x, &y, &z, &dx, &dy, &dz, &yaw, &pitch, &relFlags); err == nil {
				b.mu.Lock()
				b.x, b.y, b.z = float64(x), float64(y), float64(z)
				b.mu.Unlock()
				_ = b.conn.WritePacket(pk.Marshal(
					int32(packetid.ServerboundAcceptTeleportation), tpID,
				))
				log.Printf("server re-teleport id=%d -> snap to (%.3f, %.3f, %.3f)", int32(tpID), float64(x), float64(y), float64(z))
			}
		case packetid.ClientboundSetHealth:
			// SetHealth: Float health, VarInt food, Float saturation.
			var health pk.Float
			var food pk.VarInt
			var sat pk.Float
			if err := p.Scan(&health, &food, &sat); err == nil {
				log.Printf("health=%.1f food=%d saturation=%.1f", float32(health), int32(food), float32(sat))
			}
		case packetid.ClientboundLevelChunkWithLight:
			// DIAGNOSTIC: decode the chunk the client just received and, if it is the probe
			// column's chunk, dump the block state at the watched column for Y=56..64. This
			// proves whether the water blocks actually arrive in the CLIENT's ClientLevel.
			if b.probeActive {
				b.probeChunk(p)
			}
			// GATE: count streamed chunks so runGate can wait for the world to settle before
			// triggering (the entity tracker only sends AddEntity once chunks are loaded).
			if b.mode == "gate" {
				b.gateMu.Lock()
				b.chunksSeen++
				b.gateMu.Unlock()
			}
		case packetid.ClientboundChunkBatchFinished:
			// Acknowledge the batch so the server's PlayerChunkSender flow control releases the next
			// one (without this the server holds at maxUnacknowledgedBatches and stops streaming
			// after the first batch). ServerboundChunkBatchReceived = one Float desiredChunksPerTick;
			// a real client reports its sustainable rate. We report a high rate so streaming runs at
			// full speed in the test.
			_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundChunkBatchReceived), pk.Float(64.0)))
		case packetid.ClientboundBlockUpdate:
			// DIAGNOSTIC: a single-block change. If it lands in the probe column, report the new
			// state — this catches the server OVERWRITING the water after the chunk was sent
			// (the fluid-broadcast path setFluidBlock -> broadcastBlockUpdate).
			if b.probeActive {
				b.probeBlockUpdate(p)
			}
		case packetid.ClientboundSetEquipment:
			// STRICT validate: VarInt id, then 1+ (Byte slotFlag, ItemStack) pairs. We decode the
			// FULL body and assert no trailing bytes / no short read — a real Notchian client crashes
			// on a malformed equipment body, so this catches the framing bug the lax logger misses.
			if err := validateSetEquipment(p); err != nil {
				log.Printf("[DECODE-FAIL] SetEquipment: %v (%d bytes)", err, len(p.Data))
			}
		case packetid.ClientboundSetEntityData:
			if err := validateSetEntityData(p); err != nil {
				log.Printf("[DECODE-FAIL] SetEntityData: %v (%d bytes)", err, len(p.Data))
			}
		case packetid.ClientboundAddEntity:
			// GATE (item #1/#2): a mob became visible. Decode the leading id + type from
			// encodeAddEntity's wire (VarInt id, UUID, VarInt typeId, ...) — enough to track
			// which ids appeared and their wire type. Additive: only records in gate mode.
			if b.mode == "gate" {
				b.recordAddEntity(p)
			}
		case packetid.ClientboundMoveEntityPos,
			packetid.ClientboundMoveEntityPosRot,
			packetid.ClientboundMoveEntityRot,
			packetid.ClientboundTeleportEntity,
			packetid.ClientboundEntityPositionSync:
			// GATE (item #1/#2): the mob MOVED. The leading field of every one of these is the
			// VarInt entity id; increment its move count (the wander mob walks via the Go nav,
			// a vanilla pig drifts via its stroll goal). Counts the absolute-position packets too
			// (the tracker uses TeleportEntity/EntityPositionSync for moved entities in v1).
			if b.mode == "gate" {
				b.recordMove(p)
			}
		case packetid.ClientboundRotateHead:
			// GATE (item #2 supporting): headrot. Leading field is the VarInt entity id.
			if b.mode == "gate" {
				b.recordHeadRot(p)
			}
		case packetid.ClientboundOpenScreen:
			// GATE (item #3): the crafting menu opened. Capture the windowId (leading VarInt) so the
			// gate's ContainerClick targets the open crafting window (handleContainerClick routes by id).
			if b.mode == "gate" {
				var win, menuID pk.VarInt
				_ = p.Scan(&win, &menuID)
				b.gateMu.Lock()
				b.sawOpenScreen = true
				b.craftWindow = int32(win)
				b.gateMu.Unlock()
				log.Printf("[gate] observed OpenScreen windowId=%d menu=%d", int32(win), int32(menuID))
			}
		case packetid.ClientboundContainerSetContent:
			// GATE (item #3): the full crafting-window content — if menu slot 0 (the result) is a
			// non-empty stack, the plugin matcher populated it.
			if b.mode == "gate" {
				b.recordContainerContent(p)
			}
		case packetid.ClientboundContainerSetSlot:
			// GATE (item #3): a single-slot update — record a non-empty result slot (menu slot 0).
			if b.mode == "gate" {
				b.recordContainerSlot(p)
			}
		case packetid.ClientboundSystemChat:
			// GATE (item #4): the on_block_break / on_player_join hook's chat() reaction. Decode the
			// NBT Component text and flag it if it carries the gate_events marker.
			if b.mode == "gate" {
				b.recordSystemChat(p)
			}
		case packetid.ClientboundDisconnect:
			e := fmt.Errorf("server disconnected: %s", decodeText(p))
			b.fatal.CompareAndSwap(nil, &e)
			return
		}
	}
}

// validateSetEquipment strictly decodes a ClientboundSetEquipment body and verifies it consumes
// EXACTLY the packet bytes (no short read, no trailing). Returns an error describing any mismatch.
func validateSetEquipment(p pk.Packet) error {
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	if _, err := id.ReadFrom(r); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	for i := 0; ; i++ {
		var slot pk.Byte
		if _, err := slot.ReadFrom(r); err != nil {
			if i == 0 {
				return fmt.Errorf("first slot byte: %w", err)
			}
			return fmt.Errorf("slot byte after %d entries: %w", i, err)
		}
		var item component.SlotData
		if _, err := item.ReadFrom(r); err != nil {
			return fmt.Errorf("itemstack entry %d (slotFlag=0x%02x): %w", i, byte(slot), err)
		}
		if byte(slot)&0x80 == 0 {
			break // last entry (no continuation bit)
		}
	}
	if r.Len() != 0 {
		return fmt.Errorf("%d trailing bytes after the equipment list", r.Len())
	}
	return nil
}

// validateSetEntityData strictly decodes a ClientboundSetEntityData body: VarInt id, then a
// sequence of (UByte index, VarInt serializerId, value...) ending in 0xFF. We only decode the
// serializers we emit (BYTE id 0, INT id 1) and assert clean framing to the 0xFF terminator.
func validateSetEntityData(p pk.Packet) error {
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	if _, err := id.ReadFrom(r); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	for {
		var index pk.UnsignedByte
		if _, err := index.ReadFrom(r); err != nil {
			return fmt.Errorf("index byte: %w", err)
		}
		if index == 0xFF {
			break
		}
		var ser pk.VarInt
		if _, err := ser.ReadFrom(r); err != nil {
			return fmt.Errorf("serializerId at index %d: %w", index, err)
		}
		switch int32(ser) {
		case 0: // BYTE
			var v pk.Byte
			if _, err := v.ReadFrom(r); err != nil {
				return fmt.Errorf("BYTE value at index %d: %w", index, err)
			}
		case 1: // INT (VarInt)
			var v pk.VarInt
			if _, err := v.ReadFrom(r); err != nil {
				return fmt.Errorf("INT value at index %d: %w", index, err)
			}
		default:
			return fmt.Errorf("unhandled serializerId %d at index %d (testbot cannot validate)", int32(ser), index)
		}
	}
	if r.Len() != 0 {
		return fmt.Errorf("%d trailing bytes after the 0xFF terminator", r.Len())
	}
	return nil
}

// probeChunk decodes a ClientboundLevelChunkWithLight and, if it is the probe column's chunk,
// dumps the block at every Y from 56..64 of that column. The wire layout (world/packet.go
// WriteLevelChunkWithLight) is: Int x, Int z, then exactly what level.Chunk.WriteTo emits
// (heightmaps + section blob + block entities + light). So we read the two Int coords off the
// front and feed the REMAINDER to level.Chunk.ReadFrom, which consumes the chunk body AND the
// trailing light data symmetrically (it is the inverse of WriteTo) — no manual body/light split
// is needed.
func (b *bot) probeChunk(p pk.Packet) {
	r := bytes.NewReader(p.Data)
	var cx, cz pk.Int
	if _, err := cx.ReadFrom(r); err != nil {
		log.Printf("[chunk] probe: read x failed: %v", err)
		return
	}
	if _, err := cz.ReadFrom(r); err != nil {
		log.Printf("[chunk] probe: read z failed: %v", err)
		return
	}
	if int32(cx) != b.probeCX || int32(cz) != b.probeCZ {
		return // not the watched chunk
	}

	// 24 sections, minY=-64 — the overworld dimension shape this server serves.
	ch := level.EmptyChunk(24)
	if _, err := ch.ReadFrom(r); err != nil {
		// The sections are parsed before the light, so block data is already populated even if
		// a later (light) field trips. Report the error but still attempt the dump.
		log.Printf("[chunk] probe: ReadFrom error (attempting dump anyway): %v", err)
	}

	lx := int(b.probeX & 15)
	lz := int(b.probeZ & 15)
	var sb bytes.Buffer
	fmt.Fprintf(&sb, "[chunk] recv (cx=%d,cz=%d) column world(x=%d,z=%d):", int32(cx), int32(cz), b.probeX, b.probeZ)
	for y := 64; y >= 56; y-- {
		secIdx := (y + 64) >> 4
		ly := (y + 64) & 15
		name := "<out-of-range-section>"
		if secIdx >= 0 && secIdx < len(ch.Sections) {
			localIndex := ly*256 + lz*16 + lx
			name = describeState(ch.Sections[secIdx].GetBlock(localIndex))
		}
		fmt.Fprintf(&sb, " Y=%d %s", y, name)
	}
	log.Print(sb.String())
}

// probeBlockUpdate decodes a ClientboundBlockUpdate (Position pos, VarInt stateId) and, if the
// updated block is in the probe column, reports the new block name. This reveals a post-chunk
// overwrite of the water.
func (b *bot) probeBlockUpdate(p pk.Packet) {
	var pos pk.Position
	var stateID pk.VarInt
	if err := p.Scan(&pos, &stateID); err != nil {
		log.Printf("[blockupdate] probe: scan failed: %v", err)
		return
	}
	if int32(pos.X) != b.probeX || int32(pos.Z) != b.probeZ {
		return // not in the watched column
	}
	log.Printf("[blockupdate] (%d,%d,%d) -> %s", pos.X, pos.Y, pos.Z, describeState(block.StateID(stateID)))
}

// describeState maps a global block-state id to a human label, flagging water (with its fluid
// level) explicitly so the diagnostic output reads at a glance. Out-of-range ids print raw.
func describeState(s block.StateID) string {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return fmt.Sprintf("<state-id=%d out-of-range>", int(s))
	}
	blk := block.StateList[s]
	if w, ok := blk.(block.Water); ok {
		return fmt.Sprintf("water(level=%d)", int(w.Level))
	}
	return blk.ID()
}

// answerPlayKeepAlive echoes a play-state ClientboundKeepAlive (a single Long) back as
// ServerboundKeepAlive so the keep-alive watchdog does not time the bot out.
func (b *bot) answerPlayKeepAlive(p pk.Packet) error {
	var id pk.Long
	if err := p.Scan(&id); err != nil {
		return fmt.Errorf("scan play keep-alive: %w", err)
	}
	if err := b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundKeepAlive), id)); err != nil {
		return fmt.Errorf("answer play keep-alive: %w", err)
	}
	return nil
}

// logClientbound prints a short label for every clientbound packet the bot receives, so the
// developer can SEE what the server pushes back and correlate it with the firehose. The label
// comes from the generated stringer over the Game-state ClientboundPacketID space.
func (b *bot) logClientbound(p pk.Packet) {
	log.Printf("recv <- %s (id=%d, %d bytes)", packetid.ClientboundPacketID(p.ID).String(), p.ID, len(p.Data))
}

// stepToward advances cur toward target by at most step, never overshooting.
func stepToward(cur, target, step float64) float64 {
	if cur < target {
		if target-cur <= step {
			return target
		}
		return cur + step
	}
	if cur > target {
		if cur-target <= step {
			return target
		}
		return cur - step
	}
	return cur
}

// splitHostPort splits "host:port" into host + numeric port, defaulting to 25565.
func splitHostPort(addr string) (string, uint16) {
	host := addr
	port := uint16(mcnet.DefaultPort)
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			host = addr[:i]
			var p int
			if _, err := fmt.Sscanf(addr[i+1:], "%d", &p); err == nil && p > 0 && p <= 65535 {
				port = uint16(p)
			}
			break
		}
	}
	if host == "" {
		host = "localhost"
	}
	return host, port
}

// formatUUID renders a pk.UUID for logging.
func formatUUID(u pk.UUID) string {
	return fmt.Sprintf("%x", [16]byte(u))
}

// decodeText best-effort decodes a leading String/Chat from a disconnect packet for logging.
// Disconnect reasons in Play/Config are a length-prefixed (S)NBT or JSON text component; we
// fall back to the raw byte length if it does not decode as a plain String.
func decodeText(p pk.Packet) string {
	var s pk.String
	if err := p.Scan(&s); err == nil && len(s) > 0 {
		return string(s)
	}
	return fmt.Sprintf("(%d-byte reason component)", len(p.Data))
}
