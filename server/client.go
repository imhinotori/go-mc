package server

import (
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/net/queue"
)

// Packet758 is a packet in protocol 757.
// We are using type system to force programmers to update packets.
type (
	Packet758 pk.Packet
	Packet757 pk.Packet
)

type WritePacketError struct {
	Err error
	ID  int32
}

func (s WritePacketError) Error() string {
	return "server: send packet " + strconv.FormatInt(int64(s.ID), 16) + " error: " + s.Err.Error()
}

func (s WritePacketError) Unwrap() error {
	return s.Err
}

type PacketQueue = queue.Queue[pk.Packet]

// Intent is what a connection's read goroutine produces: a raw, still-undecoded
// packet paired with the Client it arrived on. The read goroutine does NOT decode
// or act on the packet — dispatch happens later in the tick consumer (a stub in
// Phase 2, the real tick loop in Phase 3). Carrying only *Client + pk.Packet is the
// structural guarantee that no game-state pointer crosses the network->tick seam
// (NET-05 invariant / T-2-07).
type Intent struct {
	Client *Client
	Packet pk.Packet
}

// Client is a single network connection's plumbing. It owns exactly one outbound
// writer goroutine that drains a bounded outbound PacketQueue (nothing else writes
// to the socket) and one read goroutine that turns inbound packets into Intent
// values on an inbound channel. In Phase 2 the Client holds NO game-state pointer:
// it only moves bytes and Intent values. Phase 3 attaches the tick loop to the same
// inbound channel type without an API change.
type Client struct {
	conn     *net.Conn
	outbound PacketQueue   // bounded NewChannelQueue: exactly one writer (writeLoop) drains it
	closeOne sync.Once     // makes Close idempotent (Send-on-full and writeLoop exit may both close)
	closed   atomic.Bool   // set once on Close; gates Send so it never pushes on a closed queue
	quit     chan struct{} // closed by Close; lets readLoop abandon an in-flight inbound send
	// NOTE: deliberately no world/entity/game-state field here in Phase 2.
}

// NewClient builds a Client over conn with a bounded outbound queue of capacity
// outboundCap. The queue is a NewChannelQueue (bounded, non-blocking Push that drops
// on full) — never an unbounded LinkedQueue, which would let a slow client grow
// memory without limit (T-2-04).
func NewClient(conn *net.Conn, outboundCap int) *Client {
	return &Client{
		conn:     conn,
		outbound: queue.NewChannelQueue[pk.Packet](outboundCap),
		quit:     make(chan struct{}),
	}
}

// Start launches the two per-connection goroutines: exactly one writeLoop (the sole
// writer) and one readLoop producing Intents onto inbound. It returns immediately;
// the goroutines run until the conn errors or Close is called.
func (c *Client) Start(inbound chan<- Intent) {
	go c.writeLoop()
	go c.readLoop(inbound)
}

// writeLoop is the ONE and ONLY writer for this connection. Pull() blocks until a
// packet is available; it returns ok=false when the outbound queue is Closed, which
// is the loop's exit signal. Because this is the single goroutine calling
// WritePacket, outbound writes are serialized with no locking.
func (c *Client) writeLoop() {
	for {
		p, ok := c.outbound.Pull()
		if !ok {
			return // queue closed -> shut the writer down
		}
		if err := c.conn.WritePacket(p); err != nil {
			// Write failed (conn gone). Tear the client down; do not spin.
			c.Close()
			return
		}
	}
}

// readLoop decodes inbound packets into Intents and pushes them onto inbound. It
// NEVER touches game state: it only reads bytes and forwards an Intent carrying the
// raw pk.Packet. On any read error (closed conn, malformed frame rejected by the
// codec) it tears the client down and returns.
func (c *Client) readLoop(inbound chan<- Intent) {
	for {
		var p pk.Packet
		if err := c.conn.ReadPacket(&p); err != nil {
			c.Close()
			return
		}
		// Deliver the Intent, but abandon the send if Close fires first. This keeps
		// readLoop from parking on a full/closed inbound after the connection is gone:
		// the consumer that owns inbound can shut down without a send-on-closed panic.
		select {
		case inbound <- Intent{Client: c, Packet: p}:
		case <-c.quit:
			return
		}
	}
}

// Send enqueues p on the bounded outbound queue. Backpressure is bounded with a
// drop-and-disconnect policy (decided for Phase 2, T-2-04): if Push reports the queue
// is full it returns false, and the connection is disconnected rather than blocking
// the caller or letting the queue grow without bound. Phase 3 revisits flush timing
// once the tick owns it.
func (c *Client) Send(p pk.Packet) {
	if c.closed.Load() {
		return // already disconnected: drop silently, never push on a closed queue
	}
	// A concurrent Close can race between the check above and the Push below,
	// closing the underlying channel; pushing on a closed ChannelQueue panics.
	// Recover that narrow window into a no-op (the connection is already going
	// away), so a racing Close never crashes a producer.
	defer func() { _ = recover() }()
	if !c.outbound.Push(p) {
		c.Close() // queue full -> drop-and-disconnect
	}
}

// Close is idempotent. It closes the outbound queue (which makes writeLoop's Pull
// return ok=false and stops the single writer) and closes the underlying conn (which
// unblocks readLoop's ReadPacket). Safe to call from Send, writeLoop, readLoop, or a
// caller, concurrently.
func (c *Client) Close() {
	c.closeOne.Do(func() {
		c.closed.Store(true)
		close(c.quit)      // release readLoop from any in-flight inbound send
		c.outbound.Close() // stop the single writer (writeLoop Pull returns ok=false)
		_ = c.conn.Close() // unblock readLoop's ReadPacket
	})
}

// stubTickConsumer drains the inbound channel and discards every Intent. It is the
// Phase 2 placeholder for the Phase 3 tick loop's "drain inbound" phase: Phase 3
// replaces this function body with real per-packet dispatch, but the channel type
// (chan Intent) is the stable seam contract and does not change. It returns when
// inbound is closed.
func stubTickConsumer(inbound <-chan Intent) {
	for range inbound {
		// discard until Phase 3 attaches the real tick loop
	}
}
