package server

import (
	"context"

	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/command"
)

// commands.go implements CMD-01: server-side command dispatch on the FORK command.Graph
// (server/command/*.go — the Brigadier dispatcher already on disk). We do NOT hand-roll a
// parser; we reuse NewGraph + the Literal/HandleFunc builders, the WriteTo serializer, and
// ClientJoin. This file builds the graph once, sends it per-connection at join as
// ClientboundCommands (so the client tab-completes), and routes a decoded
// ServerboundChatCommand to Graph.Execute on the tick goroutine (TICK-05).
//
// The reply path (telling the player a command failed) is delivered via the SystemChat helper
// from chat.go (07-05): an Execute parse/dispatch error replies to the issuing player as a
// ClientboundSystemChat, consolidating the reply 07-04 left as a documented stub. The
// unprivileged-client and malformed paths are silent no-ops by construction (the permission
// gate + the defensive decode).

// maxCommandLen bounds a command string before Execute (T-7-03, ASVS V5). The wire decode
// (FriendlyByteBuf.readUtf, jar-verified) caps at 32767, but a real Minecraft command never
// approaches that: the vanilla client command input is limited to 256 chars. We reject
// anything longer than that BEFORE Execute so a malicious oversized payload does no
// unbounded parsing work. This is the server-side assert of the realistic command bound.
const maxCommandLen = 256

// permissionResolver answers "may this connection run the command rooted at `node`?".
// v1's resolver (built in runCommand) grants the harmless /say,/me literals to everyone,
// but the HOOK is present and threaded through every handler via the context, so a future
// PRIVILEGED command is gated by construction (ASVS V4 / T-7-02) — adding it is a one-line
// permissionGated wrap, not a security refactor.
type permissionResolver func(node string) bool

// permResolverKey is the (unexported, collision-free) context key the permission resolver
// is carried under. Execute threads a context all the way to a node's HandlerFunc, so the
// gate reads the resolver off the context the dispatch installed for THIS command.
type permResolverKey struct{}

// withPermissionResolver returns ctx carrying r so permissionGated handlers can consult it.
// dispatch installs the per-command resolver before calling Graph.Execute.
func withPermissionResolver(ctx context.Context, r permissionResolver) context.Context {
	return context.WithValue(ctx, permResolverKey{}, r)
}

// permissionResolverFrom extracts the resolver installed by withPermissionResolver. A
// missing resolver DENIES by default (fail-closed: a handler invoked without a resolver
// must not run a privileged body), which keeps the gate safe even if a call path forgets
// to install one.
func permissionResolverFrom(ctx context.Context) permissionResolver {
	if r, ok := ctx.Value(permResolverKey{}).(permissionResolver); ok && r != nil {
		return r
	}
	return func(string) bool { return false }
}

// permissionGated wraps a command.HandlerFunc so its body runs ONLY when the per-command
// permission resolver (carried on the context) grants `node`. A denied permission returns
// nil WITHOUT running the body (the command is a silent no-op for an unauthorized client —
// ASVS V4 / T-7-02). This is the hook every privileged command must use; v1's resolver
// grants the harmless literals so /say,/me run for everyone, but the gate is structural.
func permissionGated(node string, h command.HandlerFunc) command.HandlerFunc {
	return func(ctx context.Context, args []command.ParsedData) error {
		if !permissionResolverFrom(ctx)(node) {
			return nil // unauthorized: no-op, never run the body
		}
		return h(ctx, args)
	}
}

// buildCommandGraph constructs the fork command.Graph ONCE with the v1 literals (/say, /me)
// registered via the fork builders, each handler permission-gated. The graph is read-only
// after build, so a single instance is shared across all joins (sent per-connection by
// ClientJoin). REUSE — not a new parser (07-RESEARCH Don't Hand-Roll).
func buildCommandGraph() *command.Graph {
	g := command.NewGraph()

	// Both v1 commands take a single trailing message argument (the vanilla shape: `/say
	// <message>` and `/me <action>`). The argument is a GREEDY string (StringParser(2)) so it
	// consumes the REST of the line — "hi there" is one message, not a parse error on the
	// trailing words. Registering the argument child is load-bearing: WITHOUT it the fork
	// dispatcher rejects "say hi" as "extra text: hi" (Execute descends to the literal, finds
	// no child to absorb "hi", and errors). The handler lives on the ARGUMENT node so it runs
	// only once the whole message is parsed; each handler is permission-gated so the privilege
	// model is in place from day one (ASVS V4 / T-7-02). v1's handler body is a no-op — the
	// broadcast/reply lands when the chat path (07-05) wires the SystemChat helper — but the
	// command now PARSES and dispatches end-to-end (the objective's `/say hi` runs).

	// /say <message>
	sayMsg := g.Argument("message", command.StringParser(2)).HandleFunc(permissionGated("command.say",
		func(ctx context.Context, args []command.ParsedData) error {
			return nil
		}))
	say := g.Literal("say").AppendArgument(sayMsg).Unhandle()
	g.AppendLiteral(say)

	// /me <action>
	meMsg := g.Argument("action", command.StringParser(2)).HandleFunc(permissionGated("command.me",
		func(ctx context.Context, args []command.ParsedData) error {
			return nil
		}))
	me := g.Literal("me").AppendArgument(meMsg).Unhandle()
	g.AppendLiteral(me)

	return g
}

// cmdGraph is the process-wide command graph, built ONCE at init and shared (read-only) by
// every connection. The graph is immutable after build, so concurrent ClientJoin sends and
// the tick goroutine's Execute calls all read it without a lock (no race — proven by the
// Docker -race gate over ./server/...).
var cmdGraph = buildCommandGraph()

// executeCommand is the dispatch seam runCommand routes a bounded, permission-installed
// command string through. It defaults to the shared fork graph's Execute (the production
// path: parse + dispatch on the tick goroutine). It is a package-level var ONLY so a test
// can capture the exact string that reached Execute (TestChatCommandRouted) and inject a
// parse-error path (TestChatCommandParseErrorNoPanic) without mutating the read-only shared
// graph. Production never reassigns it; the tick goroutine is the sole caller, so the var is
// not a concurrency hazard (it is set once at init and only swapped within a single-threaded
// test). Restored by every test that swaps it.
var executeCommand = func(ctx context.Context, cmd string) error {
	return cmdGraph.Execute(ctx, cmd)
}

// commandClientAdapter bridges server's *Client to the fork's command.Client interface
// (component.go: SendPacket(pk.Packet)). The fork's Graph.ClientJoin writes the serialized
// ClientboundCommands graph to command.Client.SendPacket; we forward that to the server
// connection's bounded outbound queue via Client.Send (writeLoop stays the sole socket
// writer — TICK-05). A tiny, allocation-free wrapper: REUSE the fork's send path, adapt the
// method name.
type commandClientAdapter struct{ c *Client }

// SendPacket satisfies command.Client by forwarding to the connection's bounded queue.
func (a commandClientAdapter) SendPacket(p pk.Packet) { a.c.Send(p) }

// sendCommandGraph sends the shared command graph to a joining player as ClientboundCommands
// so the client tab-completes the registered commands. Called from the AcceptPlayer
// bootstrap tail. The send goes through the connection's bounded outbound queue (the
// writeLoop is the sole socket writer); it mutates no tick-owned state.
func (t *TickLoop) sendCommandGraph(p *tickPlayer) {
	if p == nil || p.client == nil {
		return
	}
	cmdGraph.ClientJoin(commandClientAdapter{p.client})
}

// runCommand decodes a player's command string and dispatches it to the fork Graph.Execute
// ON THE TICK goroutine (commands mutate authoritative game state — TICK-05). It is
// DEFENSIVE: the string is length-bounded before Execute (T-7-03), and an Execute parse
// error is handled (v1: a documented no-op — the SystemChat reply helper lands in 07-05 and
// runCommand will then reply with the error). It NEVER panics (T-3-02).
//
// The per-command permission resolver is installed on the context here. v1 grants every
// node (the literals are harmless: an all-players-operator policy), but the resolver is the
// single point a future privileged command's policy plugs into — the handlers already
// consult it via permissionGated, so making a command operator-only is a resolver change,
// not a handler change (ASVS V4 / T-7-02).
func (t *TickLoop) runCommand(p *tickPlayer, cmd string) {
	if p == nil {
		return
	}
	// Length bound BEFORE any parsing work (T-7-03 / ASVS V5).
	if len(cmd) == 0 || len(cmd) > maxCommandLen {
		return
	}

	// v1 permission policy: every player is an operator for the harmless literals. The
	// resolver is the hook a future privileged command gates on.
	ctx := withPermissionResolver(context.Background(), func(node string) bool {
		return playerHasPermission(p, node)
	})

	// Execute runs the fork dispatcher on the tick goroutine. A parse/dispatch error is now
	// reported back to the issuing player via the SystemChat reply helper (07-05 consolidates
	// the reply 07-04 left as a documented stub). Never a panic (T-3-02).
	if err := executeCommand(ctx, cmd); err != nil {
		// CMD-02 reply path: the command failed to parse/dispatch. Deliver the outcome to the
		// issuing player as a server-attributed ClientboundSystemChat (the shared helper on the
		// tick goroutine — TICK-05). The error text is the fork dispatcher's own message; we do
		// NOT panic or block the tick.
		t.sendSystemChat(p, "Unknown or invalid command: "+err.Error())
	}
}

// playerHasPermission is the v1 permission policy: all players are operators for the
// harmless /say,/me literals (an all-players-operator model). It is the SINGLE place a
// future privileged-command policy is implemented — a real operator check (ops list, perm
// node table) replaces the body here and every permissionGated handler is gated by it
// automatically (ASVS V4 / T-7-02). Runs on the tick goroutine.
func playerHasPermission(p *tickPlayer, node string) bool {
	return true
}

// runChatCommand decodes a ServerboundChatCommand packet and routes it to runCommand. The
// wire is JAR-VERIFIED: ServerboundChatCommandPacket is a single `command` String
// (FriendlyByteBuf.readUtf, NO signing). The decode is DEFENSIVE: a Scan error is a silent
// no-op (T-3-02), never a panic. Called inline from dispatch on the tick goroutine.
func (t *TickLoop) runChatCommand(p *tickPlayer, packet pk.Packet) {
	if p == nil {
		return
	}
	var s pk.String
	if err := packet.Scan(&s); err != nil {
		return // malformed payload: silent no-op (T-3-02)
	}
	t.runCommand(p, string(s))
}
