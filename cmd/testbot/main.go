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
	"flag"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
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
	mode := flag.String("mode", "hold", "movement script: hold | walk | swim | dive | goto")
	ticks := flag.Int("ticks", 200, "number of movement ticks to run, then disconnect cleanly")
	tx := flag.Float64("x", 0, "goto target X (or spawn X override)")
	ty := flag.Float64("y", 0, "goto target Y (or spawn Y override)")
	tz := flag.Float64("z", 0, "goto target Z (or spawn Z override)")
	overrideSpawn := flag.Bool("override-spawn", false, "use -x/-y/-z as the starting position instead of the server spawn")
	onGroundFlag := flag.String("onground", "", "onGround flag to send: true|false (default: true for walk/hold, false for swim/dive)")
	flag.Parse()

	log.SetFlags(log.Ltime)
	log.SetPrefix("[testbot] ")

	b := &bot{
		name: *name,
		mode: *mode,
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

	// Override the starting position if requested (e.g., to start INSIDE a water column for
	// the water-physics repro without walking there first).
	if *overrideSpawn {
		b.x, b.y, b.z = *tx, *ty, *tz
		log.Printf("spawn overridden to (%.3f, %.3f, %.3f)", b.x, b.y, b.z)
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
		default:
			b.mu.Unlock()
			return fmt.Errorf("unknown -mode %q (want hold|walk|swim|dive|goto)", mode)
		}
		x, y, z := b.x, b.y, b.z
		b.mu.Unlock()

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
		case packetid.ClientboundDisconnect:
			e := fmt.Errorf("server disconnected: %s", decodeText(p))
			b.fatal.CompareAndSwap(nil, &e)
			return
		}
	}
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
