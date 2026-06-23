package server

import (
	stdnet "net"
	"testing"

	netmc "github.com/imhinotori/go-mc/net"
)

// pipe_test.go provides the shared in-memory connection harness reused by every
// NET-01..04 integration test in Phase 2. It pairs a server-side *netmc.Conn with
// a client-side *netmc.Conn over Go's net.Pipe() (synchronous, in-memory, no OS
// socket), so handshake/login/config/play flows can be exercised end-to-end in a
// single process under -race without binding a real port.
//
// Both ends start at threshold -1 (compression off) — the pre-negotiation state.
// Tests that exercise the compressed path call SetThreshold on both ends after the
// (simulated) Set Compression packet, exactly as the live server does.

// newPipe returns a paired (server, client) *netmc.Conn over net.Pipe(). Both ends
// are wrapped as the fork's *netmc.Conn at threshold -1. A t.Cleanup closes both
// ends when the test finishes, so callers never leak the pipe.
func newPipe(t *testing.T) (server, client *netmc.Conn) {
	t.Helper()
	sc, cc := stdnet.Pipe()
	server = netmc.WrapConn(sc)
	client = netmc.WrapConn(cc)
	server.SetThreshold(-1)
	client.SetThreshold(-1)
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	return server, client
}

// runAcceptConn launches srv.AcceptConn(server) in a goroutine — the server side of
// a piped connection — and returns a channel that closes when AcceptConn returns.
// The caller drives the client end (handshake/login/config). t.Cleanup is registered
// to close the server end, which unblocks any ReadPacket parked in AcceptConn so the
// goroutine cannot outlive the test.
func runAcceptConn(t *testing.T, srv *Server, server *netmc.Conn) (done <-chan struct{}) {
	t.Helper()
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		srv.AcceptConn(server)
	}()
	t.Cleanup(func() { _ = server.Close() })
	return ch
}
