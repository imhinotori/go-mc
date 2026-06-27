package server

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"github.com/imhinotori/sulfur/level"
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

// cmdExecutorKey carries the EXECUTING player + tick loop so a command that acts on the issuer
// (e.g. /tp) can reach them. runCommand installs it before Execute; a handler reads it back via
// executorFrom. Unexported struct key = collision-free, same pattern as permResolverKey.
type cmdExecutorKey struct{}

// cmdExecutor bundles the issuing player and the owning tick loop for a command handler that
// mutates the issuer (teleport, etc.). Both are tick-owned and the handler runs on the tick
// goroutine (runCommand → Execute is synchronous on the tick), so touching them is race-safe.
type cmdExecutor struct {
	t *TickLoop
	p *tickPlayer
}

// withExecutor returns ctx carrying the issuing player + loop for issuer-acting commands.
func withExecutor(ctx context.Context, t *TickLoop, p *tickPlayer) context.Context {
	return context.WithValue(ctx, cmdExecutorKey{}, cmdExecutor{t: t, p: p})
}

// executorFrom extracts the issuing player + loop. Returns ok=false if absent (a handler that
// needs the executor must no-op safely when called without one — e.g. a console/test path).
func executorFrom(ctx context.Context) (cmdExecutor, bool) {
	e, ok := ctx.Value(cmdExecutorKey{}).(cmdExecutor)
	return e, ok && e.t != nil && e.p != nil
}

// cmdLoopKey carries the owning tick loop WITHOUT an issuing player — the console path. A
// broadcast-style command (/say, /me) needs the loop to fan a message to all players but has
// no issuer, so executorFrom (which requires a player) is not enough. The player path also
// stores the loop here so a single accessor (commandLoop) reaches it from either source.
type cmdLoopKey struct{}

// withConsole returns ctx carrying just the tick loop (no issuing player) — installed by
// runConsoleCommand so console-sourced broadcast commands can reach broadcastSystemChat.
func withConsole(ctx context.Context, t *TickLoop) context.Context {
	return context.WithValue(ctx, cmdLoopKey{}, t)
}

// commandLoop returns the owning tick loop for a broadcast-style handler, from either the
// player executor (runCommand) or the console loop value (runConsoleCommand). nil if neither.
func commandLoop(ctx context.Context) *TickLoop {
	if e, ok := ctx.Value(cmdExecutorKey{}).(cmdExecutor); ok && e.t != nil {
		return e.t
	}
	if t, ok := ctx.Value(cmdLoopKey{}).(*TickLoop); ok {
		return t
	}
	return nil
}

// commandSenderName returns the display name to attribute a broadcast command to: the issuing
// player's name, or "Server" for the console path (mirrors vanilla CommandSourceStack display
// name — a console/server source renders as the server name in the SAY_COMMAND/emote chat type).
func commandSenderName(ctx context.Context) string {
	if e, ok := ctx.Value(cmdExecutorKey{}).(cmdExecutor); ok && e.p != nil {
		return e.p.name
	}
	return "Server"
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

	// /say <message> — vanilla net.minecraft.server.commands.SayCommand: broadcast the message
	// to ALL players via ChatType.SAY_COMMAND (translatable chat.type.announcement = "[%s] %s",
	// source display name + message). v1 ships the SystemChat path (ClientboundSystemChat) which
	// renders the resolved "[sender] message" text identically. Console source name = "Server".
	sayMsg := g.Argument("message", command.StringParser(2)).HandleFunc(permissionGated("command.say",
		func(ctx context.Context, args []command.ParsedData) error {
			t := commandLoop(ctx)
			if t == nil || len(args) == 0 {
				return nil
			}
			msg, _ := args[len(args)-1].(string)
			t.broadcastSystemChat("[" + commandSenderName(ctx) + "] " + msg)
			return nil
		}))
	say := g.Literal("say").AppendArgument(sayMsg).Unhandle()
	g.AppendLiteral(say)

	// /me <action> — vanilla emote command: broadcast via ChatType.EMOTE_COMMAND (translatable
	// chat.type.emote = "* %s %s", source display name + action). v1 ships the SystemChat path
	// which renders the resolved "* sender action" text identically. Console source name = "Server".
	meMsg := g.Argument("action", command.StringParser(2)).HandleFunc(permissionGated("command.me",
		func(ctx context.Context, args []command.ParsedData) error {
			t := commandLoop(ctx)
			if t == nil || len(args) == 0 {
				return nil
			}
			action, _ := args[len(args)-1].(string)
			t.broadcastSystemChat("* " + commandSenderName(ctx) + " " + action)
			return nil
		}))
	me := g.Literal("me").AppendArgument(meMsg).Unhandle()
	g.AppendLiteral(me)

	// /tp <x> <y> <z> — DEV/gate teleport: move the issuing player to the given coordinates. The
	// args are a single greedy string ("x y z") parsed in the handler (v1 has only StringParser;
	// a real Vec3Argument lands with the brigadier coord parsers later). Gated on command.tp; the
	// v1 all-operator policy grants it. The handler re-uses the same authoritative re-teleport the
	// respawn path uses (writePlayerPositionPacket + the confirm-gate re-arm), so the client snaps
	// to the new position and the server gates movement until the client echoes the new teleport id.
	tpArgs := g.Argument("coords", command.StringParser(2)).HandleFunc(permissionGated("command.tp",
		func(ctx context.Context, args []command.ParsedData) error {
			e, ok := executorFrom(ctx)
			if !ok {
				return nil // no issuer (console/test path): nothing to teleport
			}
			if len(args) == 0 {
				return errTpUsage
			}
			raw, _ := args[len(args)-1].(string)
			x, y, z, perr := parseTpCoords(raw)
			if perr != nil {
				return perr
			}
			e.t.teleportPlayer(e.p, x, y, z)
			return nil
		}))
	tp := g.Literal("tp").AppendArgument(tpArgs).Unhandle()
	g.AppendLiteral(tp)

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
	// Carry the issuer + loop so an issuer-acting command (/tp) can move this player.
	ctx = withExecutor(ctx, t, p)

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

// EnqueueConsoleCommand hands an operator-console line to the tick via the bounded consoleCmd
// channel (mirrors the register/unregister discipline — TICK-05). It is called FROM THE TUI
// goroutine (the bubbletea console's Enter dispatch in Plan 19-02 closes over this), so it
// MUST NOT execute the command inline — that would mutate tick-owned game state off-thread
// (T-19-04 / Pitfall 7). The send is NON-BLOCKING drop-on-full: a busy tick (the channel is
// full) drops the line rather than parking the TUI goroutine — the console is best-effort,
// exactly like the log bridge's drop-on-full discipline. A nil channel (a TickLoop built
// without NewTickLoop) makes the select hit default → a cheap skipped no-op.
func (t *TickLoop) EnqueueConsoleCommand(line string) {
	select {
	case t.consoleCmd <- line: // drained on the tick goroutine in drainRegistrations
	default: // tick busy (channel full) → drop; the console is best-effort (T-19-04)
	}
}

// runConsoleCommand executes an operator-console line through the EXISTING command graph ON
// THE TICK goroutine (drained from consoleCmd in drainRegistrations — TICK-05). It is the
// console parallel to runCommand, with two deliberate differences:
//
//   - NO *tickPlayer issuer: it installs the permission resolver (console = operator → grant
//     every node) but NO executor, so executorFrom(ctx) returns ok=false and issuer-acting
//     commands (/tp) no-op safely (commands.go:84,146). The console is implicitly the operator
//     (local stdin) — the grant-all resolver is the structural hook a future ops-list model
//     plugs into (T-19-06 / ASVS V4), not a security hole.
//   - The reply goes to the LOG STREAM (slog) — which the Plan-19 handler fans to BOTH the TUI
//     viewport and stderr — instead of a player-bound SystemChat (there is no issuing player).
//
// The length bound (maxCommandLen, reused) is asserted BEFORE any parsing work (T-19-05 / ASVS
// V5). NEVER panics (executeCommand's error is logged, not propagated).
func (t *TickLoop) runConsoleCommand(line string) {
	// Length bound BEFORE any parsing work (T-19-05 / ASVS V5) — the same bound runCommand uses.
	if len(line) == 0 || len(line) > maxCommandLen {
		return
	}

	// Console = operator: grant every command node. NO withExecutor → executorFrom(ctx) ok=false
	// → issuer-acting commands no-op (commands.go:146 "no issuer (console/test path)"). The loop
	// IS carried via withConsole so broadcast commands (/say, /me) can fan to all players with the
	// "Server" source name — they need the loop but not a player issuer.
	ctx := withConsole(context.Background(), t)
	ctx = withPermissionResolver(ctx, func(string) bool { return true })

	if err := executeCommand(ctx, line); err != nil {
		// The reply path is the slog log stream (TUI + stderr), not a player SystemChat.
		slog.Warn("console command failed", "cmd", line, "err", err)
	} else {
		slog.Info("console command", "cmd", line)
	}
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

// errTpUsage is the /tp parse failure surfaced to the issuer (the runCommand reply path turns a
// non-nil handler error into a SystemChat). Kept as a sentinel so the usage text is one place.
var errTpUsage = errors.New("usage: /tp <x> <y> <z>")

// parseTpCoords parses a "<x> <y> <z>" coordinate triple (whitespace-separated floats) for /tp.
// It accepts extra surrounding whitespace and rejects a wrong arg count or a non-numeric field.
// Relative (~) and local (^) coords are NOT supported in v1 (a real Vec3Argument with the
// brigadier coord parsers lands later) — only absolute decimals.
func parseTpCoords(raw string) (x, y, z float64, err error) {
	f := strings.Fields(strings.TrimSpace(raw))
	if len(f) != 3 {
		return 0, 0, 0, errTpUsage
	}
	if x, err = strconv.ParseFloat(f[0], 64); err != nil {
		return 0, 0, 0, errTpUsage
	}
	if y, err = strconv.ParseFloat(f[1], 64); err != nil {
		return 0, 0, 0, errTpUsage
	}
	if z, err = strconv.ParseFloat(f[2], 64); err != nil {
		return 0, 0, 0, errTpUsage
	}
	return x, y, z, nil
}

// teleportPlayer moves p to (x,y,z) authoritatively — the DEV/gate /tp body, re-using the exact
// re-teleport contract performRespawn uses: set the tick-owned position, re-center the view ring,
// allocate a fresh teleport id, re-arm the confirm gate (so movement is gated until the client
// echoes the new id — no rubber-band, T-5-01), and send ClientboundPlayerPosition. The streamer
// reset (centerSent=false + a cleared sentChunks) makes flushOutbound re-emit the chunk-cache
// center and re-stream the ring around the destination so the client has the new area loaded.
// Tick-owned (called from runCommand on the tick goroutine). prevX/Y/Z are snapped to the
// destination so the next tick's movement-exhaustion delta is 0 (no spurious teleport exhaustion).
func (t *TickLoop) teleportPlayer(p *tickPlayer, x, y, z float64) {
	if p == nil || p.client == nil {
		return
	}
	p.x, p.y, p.z = x, y, z
	p.prevX, p.prevY, p.prevZ = x, y, z
	p.lastY = y
	p.center = chunkCenterOf(int32(math.Floor(x)), int32(math.Floor(z)))
	teleportID := t.nextTeleportID()
	p.awaitingTeleport = teleportID
	p.confirmedTeleport = false
	p.client.Send(writePlayerPositionPacket(teleportID, x, y, z, p.yaw, p.pitch))
	p.centerSent = false
	p.sentChunks = make(map[level.ChunkPos]bool)
}
