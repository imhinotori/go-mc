package server

import (
	stdnet "net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	netmc "github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// concWriter is an io.Writer that (a) detects any concurrent Write call (the
// single-writer invariant: exactly one goroutine may ever call WritePacket) and
// (b) records the order of packets written so queue order can be asserted. Each
// WritePacket -> Pack performs multiple Write calls for one packet, so we count
// distinct packets by detecting the length-prefix boundary is impractical here;
// instead we let the test drive whole packets and count Write batches via a small
// reassembly: writers Pack a complete frame per WritePacket through a buffered
// single Write only when using packWithoutCompression on a fresh buffer — go-mc's
// packWithoutCompression issues exactly one w.Write per packet, which we rely on.
type concWriter struct {
	mu        sync.Mutex
	inWrite   atomic.Int32
	concurred atomic.Bool
	frames    [][]byte
}

func (w *concWriter) Write(p []byte) (int, error) {
	if w.inWrite.Add(1) != 1 {
		w.concurred.Store(true)
	}
	defer w.inWrite.Add(-1)
	// Record the frame. packWithoutCompression issues exactly one Write per packet,
	// so each call here corresponds to one packet's full wire bytes.
	w.mu.Lock()
	cp := make([]byte, len(p))
	copy(cp, p)
	w.frames = append(w.frames, cp)
	w.mu.Unlock()
	return len(p), nil
}

// nopSocket is a stdnet.Conn whose only meaningful method is Close; the Client uses
// Socket only for Close() and RemoteAddr-free paths in these unit tests.
type nopSocket struct {
	closed atomic.Bool
}

func (n *nopSocket) Read([]byte) (int, error)         { return 0, stdnet.ErrClosed }
func (n *nopSocket) Write(b []byte) (int, error)      { return len(b), nil }
func (n *nopSocket) Close() error                     { n.closed.Store(true); return nil }
func (n *nopSocket) LocalAddr() stdnet.Addr           { return nil }
func (n *nopSocket) RemoteAddr() stdnet.Addr          { return nil }
func (n *nopSocket) SetDeadline(time.Time) error      { return nil }
func (n *nopSocket) SetReadDeadline(time.Time) error  { return nil }
func (n *nopSocket) SetWriteDeadline(time.Time) error { return nil }

// newFakeConn builds a *netmc.Conn whose write side is the concurrency-detecting
// concWriter and whose Close routes to a nopSocket. Threshold -1 (no compression)
// so packWithoutCompression is used (one Write per packet).
func newFakeConn() (*netmc.Conn, *concWriter, *nopSocket) {
	w := &concWriter{}
	sock := &nopSocket{}
	conn := &netmc.Conn{
		Socket: sock,
		Reader: nil, // not used by these write-side tests
		Writer: w,
	}
	conn.SetThreshold(-1)
	return conn, w, sock
}

// TestSingleWriter: many concurrent producers Send packets onto the Client's bounded
// outbound queue; exactly one writeLoop drains them; every packet reaches the conn
// with no concurrent WritePacket call. Run under -race. (NET-05)
func TestSingleWriter(t *testing.T) {
	const (
		producers       = 8
		perProducer     = 64
		total           = producers * perProducer
		outboundCap     = total + 16 // large enough that no Send is dropped
		settleTimeoutMS = 2000
	)

	conn, w, _ := newFakeConn()
	c := NewClient(conn, outboundCap)

	// Start ONLY the writeLoop (the single writer). We exercise the write seam in
	// isolation; the read seam is covered by TestInboundSeam.
	go c.writeLoop()

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				c.Send(pk.Marshal(0x01, pk.Byte(0)))
			}
		}()
	}
	wg.Wait()

	// Wait until all frames have been written or we time out.
	deadline := time.Now().Add(settleTimeoutMS * time.Millisecond)
	for {
		w.mu.Lock()
		n := len(w.frames)
		w.mu.Unlock()
		if n >= total || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}

	c.Close()

	if w.concurred.Load() {
		t.Fatalf("detected concurrent WritePacket: the single-writer invariant was violated")
	}
	w.mu.Lock()
	got := len(w.frames)
	w.mu.Unlock()
	if got != total {
		t.Fatalf("expected %d frames written by the single writer, got %d", total, got)
	}
}

// TestInboundSeam: readLoop decodes packets off the conn into Intents on the inbound
// channel; the stub consumer drains them. Asserts the read goroutine forwards only
// *Client + pk.Packet (structurally: Intent has no game-state field) and that the
// raw packet round-trips through the seam unmodified. (NET-05 / T-2-07)
func TestInboundSeam(t *testing.T) {
	server, client := newPipe(t)

	c := NewClient(server, 16)
	inbound := make(chan Intent, 8)

	go c.readLoop(inbound)

	const n = 5
	// Drive the client end: write n packets the server's readLoop will decode.
	go func() {
		for i := 0; i < n; i++ {
			_ = client.WritePacket(pk.Marshal(0x2A, pk.VarInt(i)))
		}
	}()

	for i := 0; i < n; i++ {
		select {
		case got := <-inbound:
			if got.Client != c {
				t.Fatalf("intent %d: Client pointer mismatch", i)
			}
			if got.Packet.ID != 0x2A {
				t.Fatalf("intent %d: ID = %d, want 0x2A", i, got.Packet.ID)
			}
			var v pk.VarInt
			if err := got.Packet.Scan(&v); err != nil {
				t.Fatalf("intent %d: scan: %v", i, err)
			}
			if int(v) != i {
				t.Fatalf("intent %d: payload = %d, want %d", i, v, i)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for intent %d on the inbound seam", i)
		}
	}

	c.Close()
}

// TestDisconnectReason verifies the NET-07 state-aware Disconnect helper: for each
// state (Login, Config, Play) it writes a packet whose ID is that state's Disconnect
// id and whose payload round-trips back to the given chat.Message reason text. A
// reason is always present — never a silent close. (NET-07 / T-2-05)
func TestDisconnectReason(t *testing.T) {
	cases := []struct {
		name   string
		state  ConnState
		wantID packetid.ClientboundPacketID
		reason string
	}{
		{"login", StateLogin, packetid.ClientboundLoginLoginDisconnect, "login refused: bad protocol"},
		{"config", StateConfig, packetid.ClientboundConfigDisconnect, "config refused: missing pack"},
		{"play", StatePlay, packetid.ClientboundDisconnect, "kicked: you have been removed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := newPipe(t)

			// Write the disconnect from the server side; read the raw packet on the
			// client side and assert id + reason.
			writeErr := make(chan error, 1)
			go func() {
				writeErr <- Disconnect(server, tc.state, chat.Text(tc.reason))
			}()

			var p pk.Packet
			if err := client.ReadPacket(&p); err != nil {
				t.Fatalf("read disconnect packet: %v", err)
			}
			if err := <-writeErr; err != nil {
				t.Fatalf("Disconnect write: %v", err)
			}

			if p.ID != int32(tc.wantID) {
				t.Fatalf("packet id = %d, want %d (%v)", p.ID, int32(tc.wantID), tc.wantID)
			}

			var got chat.Message
			if err := p.Scan(&got); err != nil {
				t.Fatalf("scan reason: %v", err)
			}
			if got.Text != tc.reason {
				t.Fatalf("reason text = %q, want %q", got.Text, tc.reason)
			}
		})
	}
}

// TestStubConsumerSeam wires the full Phase-2 shape: Start launches the single
// writer + the intent-producing reader, and stubTickConsumer drains the inbound
// channel and discards. It proves the stable seam contract (chan Intent) end-to-end
// — Phase 3 swaps the stub body for the real tick loop without touching this wiring.
func TestStubConsumerSeam(t *testing.T) {
	server, client := newPipe(t)

	c := NewClient(server, 16)
	inbound := make(chan Intent, 8)

	// The stub consumer owns inbound and drains until inbound is closed. Per the
	// ownership rule, inbound is closed ONLY after every producer (readLoop) has
	// fully stopped. We therefore hold readLoop's own completion signal (readDone)
	// and close inbound strictly after readLoop has returned — no timing guesses.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		c.readLoop(inbound) // the lone producer on inbound
	}()
	// Single outbound writer started via Start's contract (exercised here directly).
	go c.writeLoop()

	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		stubTickConsumer(inbound) // returns when inbound is closed
	}()

	const n = 4
	for i := 0; i < n; i++ {
		if err := client.WritePacket(pk.Marshal(0x10, pk.VarInt(i))); err != nil {
			t.Fatalf("client write %d: %v", i, err)
		}
	}

	// Tear down. Close() closes c.quit and the conn, releasing readLoop (via the
	// ReadPacket error or the quit branch of its send select) with no panic.
	c.Close()

	// Wait for the lone producer to actually exit BEFORE closing inbound — this is
	// the deterministic ownership handoff that prevents send-on-closed-channel.
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("readLoop did not exit after Close()")
	}

	close(inbound) // safe: no producers remain
	select {
	case <-consumerDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("stub consumer did not return after inbound was closed")
	}
}

// TestBackpressureDisconnect: fill the bounded outbound queue past capacity with no
// writer draining it; the over-capacity Send must trigger drop-and-disconnect
// (Close), not block. (NET-05 / T-2-04)
func TestBackpressureDisconnect(t *testing.T) {
	const cap = 4

	conn, _, sock := newFakeConn()
	c := NewClient(conn, cap)

	// Deliberately do NOT start writeLoop: nothing drains the queue, so it fills.
	done := make(chan struct{})
	go func() {
		defer close(done)
		// cap successful pushes fill the queue; the next Send finds it full and
		// must disconnect (Close) instead of blocking.
		for i := 0; i < cap+1; i++ {
			c.Send(pk.Marshal(0x01, pk.Byte(0)))
		}
	}()

	select {
	case <-done:
		// Send returned for all calls without blocking — good.
	case <-time.After(2 * time.Second):
		t.Fatalf("Send blocked on a full outbound queue: backpressure is not bounded")
	}

	if !sock.closed.Load() {
		t.Fatalf("expected drop-and-disconnect (conn closed) when the outbound queue overflowed")
	}
}
