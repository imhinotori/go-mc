// Package botclient is a reusable, headless proto-776 Minecraft client for the Sulfur
// server — INTERNAL VERIFICATION TOOLING, not gameplay. It lets an automated caller (the
// botmcp MCP server, a test, a CLI) connect to a running Sulfur server, observe the world
// it streams, move the bot, spawn/attack mobs, and read the entity table + recent chat —
// WITHOUT a real Minecraft client.
//
// The connect+readloop+state machinery is LIFTED verbatim (in wire shape) from
// cmd/testbot/main.go, the proven headless client. Every serverbound packet shape here was
// re-verified against the SERVER's own decoders (the source of truth):
//
//   - handshake / login / configuration / play bootstrap: server/handshake.go,
//     server/login.go, server/configuration.go, server/play_join.go, server/subtick.go.
//   - ServerboundMovePlayerPos: Double x,y,z + UnsignedByte flags (subtick.go applyInput).
//   - ServerboundMovePlayerRot: Float yaw,pitch + UnsignedByte flags (subtick.go).
//   - ServerboundMovePlayerStatusOnly: UnsignedByte flags only (subtick.go).
//   - ServerboundChat: leading String message; trailing signing fields ignored by the
//     server's defensive decode (server/chat.go handleChat) — so a bare String is accepted.
//   - ServerboundChatCommand: a single String (command MINUS the leading slash) — server/
//     commands.go runChatCommand. /dbg pig + /tp run for EVERYONE (server/commands.go
//     playerHasPermission returns true: an all-players-operator v1 policy — NO op needed).
//   - ServerboundUseItem: VarInt hand, VarInt sequence, Float yaw, Float pitch (item_use.go).
//   - ServerboundUseItemOn: VarInt hand, Position, VarInt face, Float cx,cy,cz, Boolean
//     insideBlock, Boolean worldBorderHit, VarInt sequence (block_place.go handleUseItemOn).
//   - ServerboundAttack: a SINGLE VarInt entityId (attack_dispatch.go handleAttack — in 26.2
//     ATTACK is its OWN packet, split out of the old Interact{Action}).
//   - ServerboundSwing: VarInt hand (subtick.go handleSwing).
//   - ServerboundSetCarriedItem: a Short slot 0..8 (inventory.go handleSetCarriedItem).
//
// State machinery: a background readLoop decodes clientbound packets and maintains, under a
// mutex, the live player position, an entity table (AddEntity/Move*/Teleport/RotateHead/
// RemoveEntities), and a recent-system-chat ring. A second goroutine flushes a stationary
// MovePlayerPos at ~20Hz (the server streams the chunk-view ring through a bounded outbound
// queue at join; a continuous read+flush keeps the bot from being dropped). Callers read the
// live state concurrently via the exported snapshot methods.
package botclient

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	mcnet "github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/offline"
)

const (
	// protocolVersion is the proto-776 the Sulfur login path asserts (server.ProtocolVersion).
	protocolVersion = 776
	// intentionLogin selects the login path in the Handshake (case 2 in server.AcceptConn).
	intentionLogin = 2
	// tickInterval is the ~20Hz client send cadence (one MovePlayerPos every 50ms), matching a
	// real client and the server's expected movement rate.
	tickInterval = 50 * time.Millisecond
	// movementFlagOnGround is bit 0x01 of the trailing packed flags byte of every
	// ServerboundMovePlayer* packet (server/subtick.go) — "on ground".
	movementFlagOnGround pk.UnsignedByte = 0x01
	// defaultRecentChat is how many recent system-chat lines the ring retains.
	defaultRecentChat = 64
)

// BotState is a point-in-time snapshot of the bot's player position + onGround flag.
type BotState struct {
	X, Y, Z    float64
	Yaw, Pitch float32
	OnGround   bool
	Connected  bool
	// Health is the bot's current health, updated from ClientboundSetHealth. Starts at 20 (full).
	// A hostile that hits the bot drops this — the live signal that a mob dealt real damage.
	Health float32
	// HealthSeen reports whether any ClientboundSetHealth has been observed (so a test can tell
	// "never updated" from "updated to 20").
	HealthSeen bool
}

// EntitySnapshot is one row of the live entity table the read loop maintains: the entity id,
// its wire type id (registry index), its last-known position, the number of move-class packets
// observed for it (a positive count proves the entity is MOVING / alive), and whether a
// head-rotation/metadata packet was seen for it.
type EntitySnapshot struct {
	ID         int32
	TypeID     int32
	X, Y, Z    float64
	MoveCount  int
	SawHeadRot bool
}

// entityRec is the internal mutable entity-table entry (guarded by Client.mu).
type entityRec struct {
	typeID     int32
	x, y, z    float64
	moveCount  int
	sawHeadRot bool
}

// Client is a single headless bot connection. The zero value is NOT ready — use New (or just
// Connect, which initializes lazily). All exported methods are safe to call concurrently with
// the background read loop; shared state is guarded by mu.
type Client struct {
	conn *mcnet.Conn
	name string

	teleportID int32

	mu         sync.Mutex
	x, y, z    float64
	yaw, pitch float32
	onGround   bool
	health     float32
	healthSeen bool
	entities   map[int32]*entityRec
	chatRing   []string

	// fatal records the first read-loop error (server disconnect); closed signals the read +
	// keepalive goroutines to exit on a caller-initiated Close.
	fatal     atomic.Pointer[error]
	closed    atomic.Bool
	connected atomic.Bool

	wg sync.WaitGroup // tracks the background goroutines so Close can join them
}

// New returns an initialized Client. Connect also initializes lazily, so callers may use a
// zero &Client{} and call Connect directly; New exists for explicit construction.
func New() *Client {
	return &Client{
		entities: make(map[int32]*entityRec),
		chatRing: make([]string, 0, defaultRecentChat),
	}
}

func (c *Client) ensureInit() {
	if c.entities == nil {
		c.entities = make(map[int32]*entityRec)
	}
	if c.chatRing == nil {
		c.chatRing = make([]string, 0, defaultRecentChat)
	}
}

// Connect runs the full handshake -> login -> configuration -> play bootstrap and returns
// once the bot is in Play with the spawn teleport confirmed. It then starts the background
// read loop (which keeps the live state current) and a ~20Hz keepalive/position-flush
// goroutine (which keeps the connection alive against the server's bounded outbound queue).
//
// addr is "host:port" (port defaults to 25565). name is the offline player name; its UUID is
// derived via offline.NameToUUID. The ctx bounds the synchronous bootstrap (a slow/hung
// server is not waited on forever); after Connect returns the background goroutines run until
// Close or a server disconnect.
func (c *Client) Connect(ctx context.Context, addr, name string) error {
	c.ensureInit()
	if c.connected.Load() {
		return fmt.Errorf("botclient: already connected")
	}
	c.name = name

	// Run the blocking bootstrap in a goroutine so ctx cancellation/timeout can abort a hung
	// connect (mcnet reads are blocking with no per-call deadline here).
	done := make(chan error, 1)
	go func() { done <- c.bootstrap(addr, name) }()
	select {
	case <-ctx.Done():
		if c.conn != nil {
			_ = c.conn.Close()
		}
		return fmt.Errorf("botclient: connect aborted: %w", ctx.Err())
	case err := <-done:
		if err != nil {
			if c.conn != nil {
				_ = c.conn.Close()
			}
			return err
		}
	}

	c.connected.Store(true)
	// The bot joins at full health (20). The server only pushes ClientboundSetHealth on a CHANGE, so
	// without this default a test reading Health before any damage would see the 0 zero-value (a false
	// "dead"). HealthSeen stays false until a real SetHealth arrives.
	c.mu.Lock()
	c.health = 20.0
	c.mu.Unlock()
	// Start the continuous reader (owns ALL reads from here) and the keepalive/flush loop.
	c.wg.Add(2)
	go func() { defer c.wg.Done(); c.readLoop() }()
	go func() { defer c.wg.Done(); c.flushLoop() }()
	return nil
}

// bootstrap performs the synchronous connect chain (dial -> handshake -> login -> configure
// -> reachPlay). It blocks; Connect wraps it with ctx cancellation.
func (c *Client) bootstrap(addr, name string) error {
	conn, err := mcnet.DialMC(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	c.conn = conn

	if err := c.handshake(addr); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	if err := c.login(name); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if err := c.configure(); err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if err := c.reachPlay(); err != nil {
		return fmt.Errorf("play bootstrap: %w", err)
	}
	return nil
}

// handshake sends the state-local id 0 Handshake (server/handshake.go reads VarInt protocol,
// String address, UShort port, VarInt intention) selecting the login path.
func (c *Client) handshake(addr string) error {
	host, port := splitHostPort(addr)
	const handshakeID = 0x00
	return c.conn.WritePacket(pk.Marshal(
		handshakeID,
		pk.VarInt(protocolVersion),
		pk.String(host),
		pk.UnsignedShort(port),
		pk.VarInt(intentionLogin),
	))
}

// login walks the offline login exchange (server MojangLoginHandler, OnlineMode=false): the
// server sends LoginCompression(threshold) BEFORE LoginFinished (cmd/sulfur threshold=256),
// then LoginFinished, then blocks for LoginAcknowledged. The bot MUST enable compression at
// the server's threshold the instant it sees LoginCompression or every later read is garbage.
func (c *Client) login(name string) error {
	id := offline.NameToUUID(name)
	if err := c.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundLoginHello),
		pk.String(name),
		pk.UUID(id),
	)); err != nil {
		return fmt.Errorf("send LoginHello: %w", err)
	}

	for {
		var p pk.Packet
		if err := c.conn.ReadPacket(&p); err != nil {
			return fmt.Errorf("read login reply: %w", err)
		}
		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundLoginLoginCompression:
			var threshold pk.VarInt
			if err := p.Scan(&threshold); err != nil {
				return fmt.Errorf("scan LoginCompression: %w", err)
			}
			c.conn.SetThreshold(int(threshold))

		case packetid.ClientboundLoginLoginFinished:
			// GameProfile(uuid, name, properties[]) + sessionId; we only advance state.
			if err := c.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundLoginLoginAcknowledged),
			)); err != nil {
				return fmt.Errorf("send LoginAcknowledged: %w", err)
			}
			return nil

		case packetid.ClientboundLoginLoginDisconnect:
			return fmt.Errorf("login disconnect: %s", decodeText(p))
		}
	}
}

// configure drives the Configuration state. The server's drain loop sends SelectKnownPacks
// FIRST and BLOCKS until the bot echoes ServerboundConfigSelectKnownPacks (the sole exit key);
// only then does it stream RegistryData/Tags/FinishConfiguration. The echo body is not
// consulted, so an empty collection (VarInt 0) is accepted.
func (c *Client) configure() error {
	for {
		var p pk.Packet
		if err := c.conn.ReadPacket(&p); err != nil {
			return fmt.Errorf("read config packet: %w", err)
		}
		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundConfigSelectKnownPacks:
			if err := c.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundConfigSelectKnownPacks),
				pk.VarInt(0), // empty Array<KnownPack> — body not consulted by the server
			)); err != nil {
				return fmt.Errorf("send SelectKnownPacks echo: %w", err)
			}

		case packetid.ClientboundConfigKeepAlive:
			var id pk.Long
			if err := p.Scan(&id); err != nil {
				return fmt.Errorf("scan config keep-alive: %w", err)
			}
			if err := c.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundConfigKeepAlive), id,
			)); err != nil {
				return fmt.Errorf("answer config keep-alive: %w", err)
			}

		case packetid.ClientboundConfigPing:
			var id pk.Int
			if err := p.Scan(&id); err == nil {
				_ = c.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundConfigPong), id))
			}

		case packetid.ClientboundConfigFinishConfiguration:
			if err := c.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundConfigFinishConfiguration),
			)); err != nil {
				return fmt.Errorf("send FinishConfiguration ack: %w", err)
			}
			return nil

		case packetid.ClientboundConfigDisconnect:
			return fmt.Errorf("config disconnect: %s", decodeText(p))
		}
	}
}

// reachPlay reads the Play join bootstrap until ClientboundPlayerPosition, decodes the
// teleport id + position (writePlayerPositionPacket: VarInt id, Double x,y,z, Double dx,dy,dz,
// Float yaw,pitch, Int relativeFlags), stores them, and sends AcceptTeleportation. WITHOUT the
// confirm the server's subtick gate (confirmedTeleport) drops every later movement packet.
func (c *Client) reachPlay() error {
	for {
		var p pk.Packet
		if err := c.conn.ReadPacket(&p); err != nil {
			return fmt.Errorf("read play bootstrap: %w", err)
		}
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
			c.teleportID = int32(tpID)
			c.mu.Lock()
			c.x, c.y, c.z = float64(x), float64(y), float64(z)
			c.yaw, c.pitch = float32(yaw), float32(pitch)
			c.onGround = true
			c.mu.Unlock()
			if err := c.conn.WritePacket(pk.Marshal(
				int32(packetid.ServerboundAcceptTeleportation),
				pk.VarInt(c.teleportID),
			)); err != nil {
				return fmt.Errorf("send AcceptTeleportation: %w", err)
			}
			return nil

		case packetid.ClientboundKeepAlive:
			if err := c.answerPlayKeepAlive(p); err != nil {
				return err
			}

		case packetid.ClientboundDisconnect:
			return fmt.Errorf("disconnected during bootstrap: %s", decodeText(p))
		}
	}
}

// readLoop is the dedicated reader goroutine: it decodes every clientbound packet for the life
// of the Play session, answering KeepAlive, re-confirming + snapping to server re-teleports,
// acking chunk batches (so the flow-control releases the next batch), and maintaining the live
// entity table + recent-chat ring. Running reads on their own goroutine — decoupled from the
// 50ms flush cadence — keeps the server's bounded outbound queue drained (the cause of the
// otherwise-early drop-and-disconnect).
func (c *Client) readLoop() {
	for {
		var p pk.Packet
		if err := c.conn.ReadPacket(&p); err != nil {
			if c.closed.Load() {
				return // expected: caller initiated the clean disconnect
			}
			e := fmt.Errorf("server closed connection (read): %w", err)
			c.fatal.CompareAndSwap(nil, &e)
			c.connected.Store(false)
			return
		}

		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundKeepAlive:
			if err := c.answerPlayKeepAlive(p); err != nil {
				c.fatal.CompareAndSwap(nil, &err)
				c.connected.Store(false)
				return
			}

		case packetid.ClientboundPlayerPosition:
			// Server-initiated re-teleport (e.g. /tp, anti-cheat correction). Re-confirm so we
			// stay un-gated and snap our local position to the server's authoritative one.
			var tpID pk.VarInt
			var x, y, z pk.Double
			var dx, dy, dz pk.Double
			var yaw, pitch pk.Float
			var relFlags pk.Int
			if err := p.Scan(&tpID, &x, &y, &z, &dx, &dy, &dz, &yaw, &pitch, &relFlags); err == nil {
				c.mu.Lock()
				c.x, c.y, c.z = float64(x), float64(y), float64(z)
				c.mu.Unlock()
				_ = c.conn.WritePacket(pk.Marshal(
					int32(packetid.ServerboundAcceptTeleportation), tpID,
				))
			}

		case packetid.ClientboundSetHealth:
			// The server's authoritative health update (ClientboundSetHealth: Float health, VarInt food,
			// Float saturation). A hostile that lands a hit drops health here — the live proof a mob dealt
			// real damage to the bot.
			var hp pk.Float
			var food pk.VarInt
			var sat pk.Float
			if err := p.Scan(&hp, &food, &sat); err == nil {
				c.mu.Lock()
				c.health = float32(hp)
				c.healthSeen = true
				c.mu.Unlock()
			}

		case packetid.ClientboundChunkBatchFinished:
			// Ack the batch so PlayerChunkSender flow control releases the next one (without this
			// the server holds at maxUnacknowledgedBatches after the first batch). We report a
			// high desired rate so streaming runs at full speed.
			_ = c.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundChunkBatchReceived), pk.Float(64.0)))

		case packetid.ClientboundAddEntity:
			c.recordAddEntity(p)

		case packetid.ClientboundMoveEntityPos:
			c.recordMovePos(p)
		case packetid.ClientboundMoveEntityPosRot:
			c.recordMovePosRot(p)
		case packetid.ClientboundMoveEntityRot:
			c.recordMoveOnly(p)
		case packetid.ClientboundTeleportEntity, packetid.ClientboundEntityPositionSync:
			c.recordAbsoluteMove(p)

		case packetid.ClientboundRotateHead, packetid.ClientboundSetEntityData:
			c.recordHeadRot(p)

		case packetid.ClientboundRemoveEntities:
			c.recordRemoveEntities(p)

		case packetid.ClientboundSystemChat:
			c.recordSystemChat(p)

		case packetid.ClientboundDisconnect:
			e := fmt.Errorf("server disconnected: %s", decodeText(p))
			c.fatal.CompareAndSwap(nil, &e)
			c.connected.Store(false)
			return
		}
	}
}

// flushLoop sends a stationary MovePlayerPos at ~20Hz so the connection stays alive (the
// server expects continuous movement; KeepAlive alone is answered by the read loop). It uses
// the bot's CURRENT position so an in-flight MoveTo's writes and this heartbeat agree. It exits
// on Close (closed) or a fatal read error.
func (c *Client) flushLoop() {
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for range t.C {
		if c.closed.Load() || c.fatal.Load() != nil {
			return
		}
		c.mu.Lock()
		x, y, z := c.x, c.y, c.z
		og := c.onGround
		c.mu.Unlock()
		flags := pk.UnsignedByte(0)
		if og {
			flags |= movementFlagOnGround
		}
		if err := c.conn.WritePacket(pk.Marshal(
			int32(packetid.ServerboundMovePlayerPos),
			pk.Double(x), pk.Double(y), pk.Double(z), flags,
		)); err != nil {
			if !c.closed.Load() {
				c.fatal.CompareAndSwap(nil, &err)
				c.connected.Store(false)
			}
			return
		}
	}
}

// answerPlayKeepAlive echoes a play-state ClientboundKeepAlive (a single Long) back as
// ServerboundKeepAlive so the keep-alive watchdog does not time the bot out.
func (c *Client) answerPlayKeepAlive(p pk.Packet) error {
	var id pk.Long
	if err := p.Scan(&id); err != nil {
		return fmt.Errorf("scan play keep-alive: %w", err)
	}
	if err := c.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundKeepAlive), id)); err != nil {
		return fmt.Errorf("answer play keep-alive: %w", err)
	}
	return nil
}

// ---- entity-table recorders (read-loop goroutine; mutate under mu) -------------------------

// recordAddEntity decodes encodeAddEntity's leading fields (VarInt id, UUID, VarInt typeId,
// Double x,y,z, ...) and inserts/updates the entity-table entry with its type + spawn position.
func (c *Client) recordAddEntity(p pk.Packet) {
	r := bytes.NewReader(p.Data)
	var id, typ pk.VarInt
	var uuid pk.UUID
	var x, y, z pk.Double
	if _, err := id.ReadFrom(r); err != nil {
		return
	}
	if _, err := uuid.ReadFrom(r); err != nil {
		return
	}
	if _, err := typ.ReadFrom(r); err != nil {
		return
	}
	if _, err := x.ReadFrom(r); err != nil {
		return
	}
	if _, err := y.ReadFrom(r); err != nil {
		return
	}
	if _, err := z.ReadFrom(r); err != nil {
		return
	}
	c.mu.Lock()
	c.ensureInit()
	rec := c.entities[int32(id)]
	if rec == nil {
		rec = &entityRec{}
		c.entities[int32(id)] = rec
	}
	rec.typeID = int32(typ)
	rec.x, rec.y, rec.z = float64(x), float64(y), float64(z)
	c.mu.Unlock()
}

// recordMovePos applies a ClientboundMoveEntityPos delta (VarInt id, Short xa,ya,za scaled by
// 4096) to the entity's position and bumps its move count.
func (c *Client) recordMovePos(p pk.Packet) {
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	var xa, ya, za pk.Short
	if _, err := id.ReadFrom(r); err != nil {
		return
	}
	if _, err := xa.ReadFrom(r); err != nil {
		return
	}
	if _, err := ya.ReadFrom(r); err != nil {
		return
	}
	if _, err := za.ReadFrom(r); err != nil {
		return
	}
	c.applyDelta(int32(id), float64(xa)/4096.0, float64(ya)/4096.0, float64(za)/4096.0)
}

// recordMovePosRot applies a ClientboundMoveEntityPosRot delta (VarInt id, Short xa,ya,za,
// Byte yRot, Byte xRot, Boolean onGround) to the entity's position and bumps its move count.
func (c *Client) recordMovePosRot(p pk.Packet) {
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	var xa, ya, za pk.Short
	if _, err := id.ReadFrom(r); err != nil {
		return
	}
	if _, err := xa.ReadFrom(r); err != nil {
		return
	}
	if _, err := ya.ReadFrom(r); err != nil {
		return
	}
	if _, err := za.ReadFrom(r); err != nil {
		return
	}
	c.applyDelta(int32(id), float64(xa)/4096.0, float64(ya)/4096.0, float64(za)/4096.0)
}

// recordMoveOnly bumps the move count for a ClientboundMoveEntityRot (rotation-only; the
// leading field is the VarInt entity id, no position change).
func (c *Client) recordMoveOnly(p pk.Packet) {
	var id pk.VarInt
	if _, err := id.ReadFrom(bytes.NewReader(p.Data)); err != nil {
		return
	}
	c.applyDelta(int32(id), 0, 0, 0)
}

// recordAbsoluteMove sets the entity's position from a ClientboundTeleportEntity /
// EntityPositionSync (VarInt id, Double x,y,z absolute, ...) and bumps its move count.
func (c *Client) recordAbsoluteMove(p pk.Packet) {
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	var x, y, z pk.Double
	if _, err := id.ReadFrom(r); err != nil {
		return
	}
	if _, err := x.ReadFrom(r); err != nil {
		return
	}
	if _, err := y.ReadFrom(r); err != nil {
		return
	}
	if _, err := z.ReadFrom(r); err != nil {
		return
	}
	c.mu.Lock()
	c.ensureInit()
	rec := c.entities[int32(id)]
	if rec == nil {
		rec = &entityRec{}
		c.entities[int32(id)] = rec
	}
	rec.x, rec.y, rec.z = float64(x), float64(y), float64(z)
	rec.moveCount++
	c.mu.Unlock()
}

// applyDelta bumps the move count and (for the delta movers) advances the entity's tracked
// position. A move for an unknown id creates a bare record (the AddEntity may have arrived in a
// missed/earlier frame) so the move signal is never lost.
func (c *Client) applyDelta(id int32, dx, dy, dz float64) {
	c.mu.Lock()
	c.ensureInit()
	rec := c.entities[id]
	if rec == nil {
		rec = &entityRec{}
		c.entities[id] = rec
	}
	rec.x += dx
	rec.y += dy
	rec.z += dz
	rec.moveCount++
	c.mu.Unlock()
}

// recordHeadRot flags a head-rotation / metadata observation for the leading entity id
// (ClientboundRotateHead / ClientboundSetEntityData both lead with the VarInt id).
func (c *Client) recordHeadRot(p pk.Packet) {
	var id pk.VarInt
	if _, err := id.ReadFrom(bytes.NewReader(p.Data)); err != nil {
		return
	}
	c.mu.Lock()
	c.ensureInit()
	rec := c.entities[int32(id)]
	if rec == nil {
		rec = &entityRec{}
		c.entities[int32(id)] = rec
	}
	rec.sawHeadRot = true
	c.mu.Unlock()
}

// recordRemoveEntities deletes every id in a ClientboundRemoveEntities (VarInt count, then
// count×VarInt id) from the entity table.
func (c *Client) recordRemoveEntities(p pk.Packet) {
	r := bytes.NewReader(p.Data)
	var count pk.VarInt
	if _, err := count.ReadFrom(r); err != nil {
		return
	}
	if count < 0 || count > 4096 {
		return // defensive bound
	}
	c.mu.Lock()
	c.ensureInit()
	for i := int32(0); i < int32(count); i++ {
		var id pk.VarInt
		if _, err := id.ReadFrom(r); err != nil {
			break
		}
		delete(c.entities, int32(id))
	}
	c.mu.Unlock()
}

// recordSystemChat decodes a ClientboundSystemChat (chat.Message content, Boolean overlay) and
// appends its text to the bounded recent-chat ring.
func (c *Client) recordSystemChat(p pk.Packet) {
	var msg chat.Message
	if _, err := msg.ReadFrom(bytes.NewReader(p.Data)); err != nil {
		return
	}
	c.mu.Lock()
	c.ensureInit()
	c.chatRing = append(c.chatRing, msg.Text)
	if len(c.chatRing) > defaultRecentChat {
		c.chatRing = c.chatRing[len(c.chatRing)-defaultRecentChat:]
	}
	c.mu.Unlock()
}

// ---- public API (caller goroutine; read state under mu) ------------------------------------

// State returns a snapshot of the bot's position + onGround + connected flag.
func (c *Client) State() BotState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return BotState{
		X: c.x, Y: c.y, Z: c.z,
		Yaw: c.yaw, Pitch: c.pitch,
		OnGround:   c.onGround,
		Connected:  c.connected.Load(),
		Health:     c.health,
		HealthSeen: c.healthSeen,
	}
}

// MoveTo walks the bot toward (x,y,z) at ~0.2 blocks/tick, flushing MovePlayerPos at ~20Hz
// until it is within ~0.5 blocks of the target or the ctx is done. The flush loop also sends a
// heartbeat from the same (mu-guarded) position, so the two never fight. Returns the ctx error
// on timeout/cancel, or a fatal read error if the connection dropped mid-walk.
func (c *Client) MoveTo(ctx context.Context, x, y, z float64) error {
	if err := c.fatalErr(); err != nil {
		return err
	}
	const step = 0.2
	const arrive = 0.5
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("botclient: MoveTo aborted: %w", ctx.Err())
		case <-t.C:
		}
		if err := c.fatalErr(); err != nil {
			return err
		}
		c.mu.Lock()
		c.x = stepToward(c.x, x, step)
		c.y = stepToward(c.y, y, step)
		c.z = stepToward(c.z, z, step)
		cx, cy, cz := c.x, c.y, c.z
		og := c.onGround
		c.mu.Unlock()

		flags := pk.UnsignedByte(0)
		if og {
			flags |= movementFlagOnGround
		}
		if err := c.conn.WritePacket(pk.Marshal(
			int32(packetid.ServerboundMovePlayerPos),
			pk.Double(cx), pk.Double(cy), pk.Double(cz), flags,
		)); err != nil {
			return fmt.Errorf("botclient: MoveTo write: %w", err)
		}

		if dist3(cx, cy, cz, x, y, z) <= arrive {
			return nil
		}
	}
}

// Look sends a ServerboundMovePlayerRot (Float yaw, Float pitch, UnsignedByte flags) and
// updates the bot's local look angles.
func (c *Client) Look(yaw, pitch float32) error {
	if err := c.fatalErr(); err != nil {
		return err
	}
	c.mu.Lock()
	c.yaw, c.pitch = yaw, pitch
	og := c.onGround
	c.mu.Unlock()
	flags := pk.UnsignedByte(0)
	if og {
		flags |= movementFlagOnGround
	}
	return c.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundMovePlayerRot),
		pk.Float(yaw), pk.Float(pitch), flags,
	))
}

// Chat sends a chat message or a command. A message beginning with "/" is sent as a
// ServerboundChatCommand with the command text MINUS the leading slash (server/commands.go
// runChatCommand decodes a single String); otherwise it is sent as a ServerboundChat (a bare
// leading String — the server's handleChat ignores the trailing signing fields in offline
// mode). NOTE: /dbg pig and /tp run for everyone (the v1 all-players-operator policy) — no op
// is required to spawn a pig via "/dbg pig".
func (c *Client) Chat(msg string) error {
	if err := c.fatalErr(); err != nil {
		return err
	}
	if len(msg) > 0 && msg[0] == '/' {
		return c.conn.WritePacket(pk.Marshal(
			int32(packetid.ServerboundChatCommand), pk.String(msg[1:]),
		))
	}
	return c.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundChat), pk.String(msg),
	))
}

// UseItem sends a ServerboundUseItem (VarInt hand, VarInt sequence, Float yaw, Float pitch) —
// the right-click-with-item path. hand is 0 (main) or 1 (off). NOTE: a spawn egg spawns on a
// clicked BLOCK (handleUseItemOn), not on right-click-air, so UseItem alone eats food but does
// NOT spawn the test-kit egg mob; use UseItemOn for the egg, or "/dbg pig" via Chat.
func (c *Client) UseItem(hand int) error {
	if err := c.fatalErr(); err != nil {
		return err
	}
	c.mu.Lock()
	yaw, pitch := c.yaw, c.pitch
	c.mu.Unlock()
	return c.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundUseItem),
		pk.VarInt(int32(hand)), pk.VarInt(0), pk.Float(yaw), pk.Float(pitch),
	))
}

// UseItemOn sends a ServerboundUseItemOn (VarInt hand, Position, VarInt face, Float
// cursorX,Y,Z, Boolean insideBlock, Boolean worldBorderHit, VarInt sequence) — the
// right-click-on-block path (place a block, open a container, spawn a held spawn egg). hand is
// 0 (main) or 1 (off); face is the clicked block face (0..5, e.g. 1 = UP). The cursor is set
// to the top-center of the clicked face and sequence is sent as 0 (the server applies
// authoritatively in v1).
func (c *Client) UseItemOn(hand int, x, y, z int, face int) error {
	if err := c.fatalErr(); err != nil {
		return err
	}
	return c.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundUseItemOn),
		pk.VarInt(int32(hand)),
		pk.Position{X: x, Y: y, Z: z},
		pk.VarInt(int32(face)),
		pk.Float(0.5), pk.Float(1.0), pk.Float(0.5),
		pk.Boolean(false), pk.Boolean(false), pk.VarInt(0),
	))
}

// Attack sends a ServerboundAttack (a single VarInt entityId — in 26.2 ATTACK is its own
// packet) followed by a ServerboundSwing (VarInt main hand) so the swing accompanies the hit,
// exactly as a real client does. The server resolves the named target, reach-gates it, and
// applies server-authoritative damage; the client never claims an amount.
func (c *Client) Attack(entityID int32) error {
	if err := c.fatalErr(); err != nil {
		return err
	}
	if err := c.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundAttack), pk.VarInt(entityID),
	)); err != nil {
		return err
	}
	return c.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundSwing), pk.VarInt(0), // 0 = main hand
	))
}

// Interact sends a ServerboundInteract (the RIGHT-CLICK on an entity — the FEED path). Wire layout
// (server/attack_dispatch.go handleInteract): VarInt entityId ; VarInt hand ; Vec3 location (3 doubles)
// ; Boolean usingSecondaryAction. The server reads only the entityId + the player's held item
// server-side (the held-read mirrors TemptGoal), so the trailing fields just form a clean frame.
// Right-clicking a pig with a pig_food item held makes it fall in love (P33 breeding).
func (c *Client) Interact(entityID int32) error {
	if err := c.fatalErr(); err != nil {
		return err
	}
	return c.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundInteract),
		pk.VarInt(entityID),
		pk.VarInt(0),                             // InteractionHand: MAIN_HAND (the server reads entityId; rest is frame)
		pk.Double(0), pk.Double(0), pk.Double(0), // Vec3 location (defensively consumed, unused)
		pk.Boolean(false),                        // usingSecondaryAction
	))
}

// SelectSlot sends a ServerboundSetCarriedItem (Short slot) selecting a hotbar slot 0..8.
func (c *Client) SelectSlot(slot int32) error {
	if err := c.fatalErr(); err != nil {
		return err
	}
	if slot < 0 || slot > 8 {
		return fmt.Errorf("botclient: hotbar slot %d out of range 0..8", slot)
	}
	return c.conn.WritePacket(pk.Marshal(
		int32(packetid.ServerboundSetCarriedItem), pk.Short(slot),
	))
}

// Entities returns a snapshot of the live entity table (id, type, position, move count, head
// rotation flag). The slice and its elements are copies — safe to read after return.
func (c *Client) Entities() []EntitySnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]EntitySnapshot, 0, len(c.entities))
	for id, rec := range c.entities {
		out = append(out, EntitySnapshot{
			ID:         id,
			TypeID:     rec.typeID,
			X:          rec.x,
			Y:          rec.y,
			Z:          rec.z,
			MoveCount:  rec.moveCount,
			SawHeadRot: rec.sawHeadRot,
		})
	}
	return out
}

// RecentChat returns the last n decoded system-chat texts (most recent last). n<=0 or n larger
// than the ring returns the whole ring.
func (c *Client) RecentChat(n int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n <= 0 || n > len(c.chatRing) {
		n = len(c.chatRing)
	}
	out := make([]string, n)
	copy(out, c.chatRing[len(c.chatRing)-n:])
	return out
}

// WaitTicks sleeps n game ticks (n*50ms) while the background read loop keeps the live state
// current, so a caller can spawn a mob, wait, then read its accumulated movement. It returns
// early with the ctx error if ctx is cancelled, or with a fatal read error if the connection
// dropped while waiting.
func (c *Client) WaitTicks(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("botclient: WaitTicks aborted: %w", ctx.Err())
		case <-t.C:
		}
		if err := c.fatalErr(); err != nil {
			return err
		}
	}
	return nil
}

// Close signals the background goroutines to stop, closes the socket, and waits for the
// goroutines to exit. It is idempotent. A read error after Close is expected and not reported.
func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil // already closed
	}
	c.connected.Store(false)
	var err error
	if c.conn != nil {
		err = c.conn.Close()
	}
	c.wg.Wait()
	return err
}

// fatalErr returns the first fatal read-loop error (server disconnect) if any, so the public
// write methods fail fast instead of writing to a dead connection.
func (c *Client) fatalErr() error {
	if errp := c.fatal.Load(); errp != nil {
		return *errp
	}
	if !c.connected.Load() {
		return fmt.Errorf("botclient: not connected")
	}
	return nil
}

// ---- small helpers -------------------------------------------------------------------------

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

// dist3 is the Euclidean distance between two points.
func dist3(ax, ay, az, bx, by, bz float64) float64 {
	dx, dy, dz := ax-bx, ay-by, az-bz
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
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

// decodeText best-effort decodes a leading String from a disconnect packet for the error text.
func decodeText(p pk.Packet) string {
	var s pk.String
	if err := p.Scan(&s); err == nil && len(s) > 0 {
		return string(s)
	}
	return fmt.Sprintf("(%d-byte reason component)", len(p.Data))
}
