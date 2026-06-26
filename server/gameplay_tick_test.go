package server

import (
	"context"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// gameplay_tick_test.go holds the Phase-3 MILESTONE assertion: a real piped
// connection stands in a single-owner ticking server. The world is still EMPTY
// (chunks/entities arrive in Phases 4-6), so the assertion is LIVENESS +
// OBSERVABILITY, not rendering: the connection is not disconnected, the tick advances
// with a live observable MSPT/TPS snapshot (TICK-01/TICK-06), keep-alive holds on its
// independent timer (TICK-04), and no game-state pointer crosses the network boundary
// (TICK-05 — proven structurally by the Docker -race gate). It is fully automated: the
// tick is internal telemetry, not pixels; the visual milestone is Phase 5 (PLAY-06).

// TestPlayerStandsInTickingWorld drives a connection to the real tick-driven GamePlay
// over the Phase-2 net.Pipe harness and asserts the four milestone properties within a
// bounded, deterministic window.
func TestPlayerStandsInTickingWorld(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// The real single-owner runtime: a TickLoop over the real system clock (it wakes
	// every 5ms, so a few hundred ms yields many logical ticks — no real-seconds wait),
	// and the independent KeepAlive on its own goroutine.
	loop := NewTickLoop(SystemClock())
	keep := NewKeepAlive()
	// Accelerate ONLY the keep-alive ping cadence so the test exercises a real ping ->
	// client-response -> ClientTick round trip in milliseconds instead of 15s. We do not
	// touch keepalive.go; we reset the timer the component already owns (same technique
	// as TestKeepAliveIndependentOfTick). The independence property is unchanged.
	keep.listTimer.Reset(10 * time.Millisecond)

	inbound := make(chan Intent, 64)
	go loop.Run(ctx, inbound)
	go keep.Run(ctx)

	g := NewGameTick(inbound, loop, keep, -48, SpawnPoint{X: 8.5, Y: -46, Z: 8.5})

	// A piped connection: the server end is handed to AcceptPlayer; the client end is
	// driven by this test as a (minimal) live client would be.
	server, client := newPipe(t)

	// AcceptPlayer BLOCKS until the connection closes (the gameplay.go contract), so run
	// it on its own goroutine and observe its return as the "player left" signal.
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		g.AcceptPlayer("stander", uuid.New(), nil, nil, ProtocolVersion, server)
	}()

	// ---- Property 2 + 1 prerequisite: the tick advances (TICK-01/TICK-06). ----
	// Wait (bounded) for the first published Stats() snapshot, then capture gametime.
	var first *TickStats
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := loop.Stats(); s != nil {
			first = s
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if first == nil {
		t.Fatal("tick never published a Stats() snapshot — the world is not ticking (TICK-06 broken)")
	}

	// ---- Property 1: the connection is NOT disconnected; the client also drives a
	// returning keep-alive so ClientTick is exercised end-to-end (TICK-04). ----
	// The client reads packets with a bounded deadline. The ONLY clientbound packets in
	// the empty Phase-3 world are Keep Alives; a Disconnect would mean the player was
	// kicked (failure). On a Keep Alive, echo it back (ServerboundKeepAlive) so the
	// response flows readLoop -> inbound -> dispatch -> KeepAlive.ClientTick.
	sawKeepAlive := make(chan struct{}, 1)
	clientErr := make(chan error, 1)
	go func() {
		for {
			var p pk.Packet
			if err := client.ReadPacket(&p); err != nil {
				clientErr <- err
				return
			}
			switch packetid.ClientboundPacketID(p.ID) {
			case packetid.ClientboundDisconnect:
				t.Errorf("player received a Play Disconnect while merely standing in the ticking world (id=%d) — keep-alive/tick wrongly kicked it", p.ID)
				clientErr <- nil
				return
			case packetid.ClientboundKeepAlive:
				// Decode the server id and echo it back as a returning keep-alive.
				var id pk.Long
				if err := p.Scan(&id); err != nil {
					clientErr <- err
					return
				}
				select {
				case sawKeepAlive <- struct{}{}:
				default:
				}
				if err := client.WritePacket(pk.Marshal(packetid.ServerboundKeepAlive, id)); err != nil {
					clientErr <- err
					return
				}
			default:
				// No other clientbound packets exist yet (empty world); ignore.
			}
		}
	}()

	// Within a bounded window the accelerated keep-alive must ping the client at least
	// once — proving keep-alive liveness runs on its own goroutine, and that the
	// returning response routes through the tick's dispatch without a disconnect.
	select {
	case <-sawKeepAlive:
		// A ping reached the client; its echo flows back through the tick (ClientTick).
	case err := <-clientErr:
		t.Fatalf("client read failed before any keep-alive ping (err=%v) — the connection did not stand in the world", err)
	case <-time.After(3 * time.Second):
		t.Fatal("no keep-alive ping reached the client within the window — keep-alive did not hold (TICK-04 broken)")
	}

	// ---- Property 2 (cont.): Stats() is LIVE — gametime strictly increases and TPS is
	// non-zero while the connection is active (TICK-01/TICK-06). ----
	var last *TickStats
	progressed := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := loop.Stats()
		if s != nil && s.GameTime > first.GameTime {
			last = s
			progressed = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !progressed {
		t.Fatalf("game-time did not advance (first=%d) — the tick is not running (TICK-01 broken)", first.GameTime)
	}
	if last.TPS <= 0 {
		t.Fatalf("TPS snapshot is not live (got %v) — MSPT/TPS not observable (TICK-06 broken)", last.TPS)
	}

	// ---- Property 1 (final): no disconnect fired during the whole window; the player
	// is still standing (AcceptPlayer has NOT returned). ----
	select {
	case <-accepted:
		t.Fatal("AcceptPlayer returned before the test closed the connection — the player was disconnected from the ticking world")
	default:
		// Still playing — correct.
	}

	// ---- Teardown: close the client end; AcceptPlayer must then unblock and return,
	// proving the leave path (unregister + KeepAlive.ClientLeft) runs cleanly. ----
	_ = client.Close()
	select {
	case <-accepted:
		// AcceptPlayer unblocked on disconnect and ran its leave path — correct.
	case <-time.After(3 * time.Second):
		t.Fatal("AcceptPlayer did not return after the connection closed — the leave path is stuck")
	}
}
