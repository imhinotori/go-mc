// Command botmcp is a HAND-ROLLED MCP (Model Context Protocol) stdio server that exposes the
// headless verification bot (internal/botclient) as a set of MCP tools, so an AI agent can
// connect to a running Sulfur server, observe the world, move, spawn/attack mobs, and ASSERT
// gameplay WITHOUT a real Minecraft client. This is INTERNAL TOOLING — not gameplay; the
// 1:1-jar-port mandate does not apply.
//
// TRANSPORT: MCP over stdio (the spec's stdio transport): newline-delimited JSON-RPC 2.0
// messages on stdin/stdout (one JSON object per line, no embedded newlines), logs on stderr.
// We hand-roll it with stdlib encoding/json + bufio — NO MCP SDK, NO new go.mod deps, so the
// default CGO_ENABLED=0 pure-Go static binary stays clean.
//
// HANDSHAKE: initialize -> (server returns protocolVersion + capabilities.tools + serverInfo)
// -> notifications/initialized -> tools/list / tools/call. Requests carry an id; notifications
// (no id) get no response.
//
// SPAWN LEVER (verified against server/commands.go + server/commands_dbg.go): "/dbg pig" runs
// for EVERYONE — server playerHasPermission returns true (the v1 all-players-operator policy),
// so the bot needs NO op to spawn a pig. The dbg ack "[dbg] spawned pig eid=N at (...)" arrives
// as a SystemChat the bot records (read it via bot_recent_chat). The SULFUR_TEST_KIT spawn-egg
// path is an alternative but requires the server started with SULFUR_TEST_KIT=1 AND a UseItemOn
// on a block; "/dbg pig" is the simpler no-flag, no-op lever and the documented default.
//
// VERIFICATION FLOW the tools enable: bot_connect -> bot_chat {"/dbg pig"} -> bot_wait_ticks
// {ticks: 20} -> bot_list_entities -> assert a pig appears with MoveCount>0 (spawned + alive +
// moving via its AI). bot_recent_chat reads the "[dbg] spawned pig eid=..." ack.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/imhinotori/sulfur/internal/botclient"
)

// mcpProtocolVersion is the MCP protocol version this server advertises in the initialize
// response. The spec negotiates a version string; we report the version we implement.
const mcpProtocolVersion = "2025-06-18"

const (
	serverName    = "sulfur-botmcp"
	serverVersion = "0.1.0"
)

// defaultAddr / defaultName are the bot_connect defaults.
const (
	defaultAddr = "127.0.0.1:25565"
	defaultName = "verifierbot"
)

// ---- JSON-RPC 2.0 wire types ---------------------------------------------------------------

// rpcRequest is an inbound JSON-RPC message. A request has an id; a notification omits it.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // raw so we echo it verbatim (number or string)
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message is a notification (no id => no response expected).
func (r *rpcRequest) isNotification() bool { return len(r.ID) == 0 }

// rpcResponse is an outbound JSON-RPC result or error. Exactly one of Result/Error is set.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC standard error codes (the subset we emit).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// ---- MCP result shapes ---------------------------------------------------------------------

// toolDef is one entry in the tools/list result: the tool name, a human description, and a JSON
// Schema for its arguments.
type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// contentBlock is one MCP content item in a tools/call result. We only emit text blocks.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolResult is the tools/call result envelope: a content array plus an isError flag (a
// tool-level error is reported as isError=true with the message in a text block, NOT a
// JSON-RPC protocol error — that distinction lets the agent see tool failures as data).
type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// textResult builds a successful single-text-block tool result.
func textResult(text string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: text}}}
}

// errResult builds an isError single-text-block tool result.
func errResult(text string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: text}}, IsError: true}
}

// jsonResult builds a successful tool result whose single text block is the JSON encoding of v
// (pretty-printed for readability in the agent transcript).
func jsonResult(v any) toolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult("failed to encode result: " + err.Error())
	}
	return textResult(string(b))
}

// ---- the server -----------------------------------------------------------------------------

// server holds the single lazily-connected bot client and the stdout writer (serialized).
type server struct {
	out   *bufio.Writer
	outMu sync.Mutex // serializes writes to stdout (one JSON line at a time)

	botMu sync.Mutex // guards bot (connect/disconnect lifecycle)
	bot   *botclient.Client
}

func main() {
	s := &server{out: bufio.NewWriter(os.Stdout)}
	if err := s.run(os.Stdin); err != nil && err != io.EOF {
		fmt.Fprintln(os.Stderr, "[botmcp] fatal:", err)
		os.Exit(1)
	}
	// Best-effort disconnect on shutdown.
	s.botMu.Lock()
	if s.bot != nil {
		_ = s.bot.Close()
	}
	s.botMu.Unlock()
}

// run reads newline-delimited JSON-RPC messages from r, dispatches each, and writes responses
// to stdout. It returns on EOF (the client closed stdin) or a fatal read error. A long line is
// supported via a large bufio scanner buffer (chat/entity dumps can be sizeable).
func (s *server) run(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // up to 8 MiB per line
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue // skip blank lines between messages
		}
		s.handleLine(line)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

// handleLine parses one JSON-RPC message and dispatches it. A parse error on a message we
// cannot attribute to an id is reported with a null id (per JSON-RPC). Notifications produce no
// response.
func (s *server) handleLine(line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeError(nil, codeParseError, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		if !req.isNotification() {
			s.writeError(req.ID, codeInvalidRequest, "jsonrpc must be \"2.0\"")
		}
		return
	}

	switch req.Method {
	case "initialize":
		s.handleInitialize(&req)
	case "notifications/initialized", "initialized":
		// Notification: no response. (Some clients send "initialized" without the prefix.)
	case "ping":
		if !req.isNotification() {
			s.writeResult(req.ID, map[string]any{})
		}
	case "tools/list":
		s.handleToolsList(&req)
	case "tools/call":
		s.handleToolsCall(&req)
	default:
		if !req.isNotification() {
			s.writeError(req.ID, codeMethodNotFound, "method not found: "+req.Method)
		}
	}
}

// handleInitialize answers the MCP initialize handshake with our protocol version, the tools
// capability, and our server info.
func (s *server) handleInitialize(req *rpcRequest) {
	if req.isNotification() {
		return
	}
	s.writeResult(req.ID, map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
		"instructions": "Headless Sulfur verification bot. Call bot_connect first, then drive " +
			"the bot and read bot_list_entities / bot_recent_chat to assert gameplay. Spawn a mob " +
			"with bot_chat {\"message\": \"/dbg pig\"} (no op required), wait with bot_wait_ticks, " +
			"then bot_list_entities to confirm it spawned and has move_count>0 (alive + moving).",
	})
}

// handleToolsList returns the static tool catalog.
func (s *server) handleToolsList(req *rpcRequest) {
	if req.isNotification() {
		return
	}
	s.writeResult(req.ID, map[string]any{"tools": toolCatalog()})
}

// ---- tools/call dispatch -------------------------------------------------------------------

// callParams is the tools/call params envelope.
type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// handleToolsCall routes a tools/call to the named tool. A tool's own failure is returned as an
// isError tool result (data the agent can read), NOT a JSON-RPC protocol error; only a
// malformed call envelope is a protocol error.
func (s *server) handleToolsCall(req *rpcRequest) {
	if req.isNotification() {
		return
	}
	var cp callParams
	if err := json.Unmarshal(req.Params, &cp); err != nil {
		s.writeError(req.ID, codeInvalidParams, "invalid tools/call params: "+err.Error())
		return
	}
	// A 30s budget per tool call bounds a hung connect/move so the agent is never blocked
	// forever; the bot's background goroutines keep the connection alive across calls.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res := s.dispatchTool(ctx, cp.Name, cp.Arguments)
	s.writeResult(req.ID, res)
}

// dispatchTool executes one tool by name and returns its tool result.
func (s *server) dispatchTool(ctx context.Context, name string, rawArgs json.RawMessage) toolResult {
	switch name {
	case "bot_connect":
		return s.toolConnect(ctx, rawArgs)
	case "bot_state":
		return s.toolState()
	case "bot_move_to":
		return s.toolMoveTo(ctx, rawArgs)
	case "bot_look":
		return s.toolLook(rawArgs)
	case "bot_chat":
		return s.toolChat(rawArgs)
	case "bot_use_item":
		return s.toolUseItem(rawArgs)
	case "bot_select_slot":
		return s.toolSelectSlot(rawArgs)
	case "bot_attack":
		return s.toolAttack(rawArgs)
	case "bot_list_entities":
		return s.toolListEntities()
	case "bot_recent_chat":
		return s.toolRecentChat(rawArgs)
	case "bot_wait_ticks":
		return s.toolWaitTicks(ctx, rawArgs)
	case "bot_disconnect":
		return s.toolDisconnect()
	default:
		return errResult("unknown tool: " + name)
	}
}

// getBot returns the live bot client or an error if not yet connected.
func (s *server) getBot() (*botclient.Client, error) {
	s.botMu.Lock()
	defer s.botMu.Unlock()
	if s.bot == nil {
		return nil, fmt.Errorf("not connected — call bot_connect first")
	}
	return s.bot, nil
}

// ---- individual tools ----------------------------------------------------------------------

func (s *server) toolConnect(ctx context.Context, rawArgs json.RawMessage) toolResult {
	var args struct {
		Addr string `json:"addr"`
		Name string `json:"name"`
	}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return errResult("invalid arguments: " + err.Error())
		}
	}
	addr := args.Addr
	if addr == "" {
		addr = defaultAddr
	}
	name := args.Name
	if name == "" {
		name = defaultName
	}

	s.botMu.Lock()
	defer s.botMu.Unlock()
	if s.bot != nil {
		// Replace an existing (possibly dead) connection.
		_ = s.bot.Close()
		s.bot = nil
	}
	c := botclient.New()
	if err := c.Connect(ctx, addr, name); err != nil {
		return errResult(fmt.Sprintf("connect to %s as %q failed: %v", addr, name, err))
	}
	s.bot = c
	return jsonResult(map[string]any{
		"connected": true,
		"addr":      addr,
		"name":      name,
		"state":     stateMap(c.State()),
	})
}

func (s *server) toolState() toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(stateMap(c.State()))
}

func (s *server) toolMoveTo(ctx context.Context, rawArgs json.RawMessage) toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	var args struct {
		X *float64 `json:"x"`
		Y *float64 `json:"y"`
		Z *float64 `json:"z"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errResult("invalid arguments: " + err.Error())
	}
	if args.X == nil || args.Y == nil || args.Z == nil {
		return errResult("bot_move_to requires x, y, z")
	}
	if err := c.MoveTo(ctx, *args.X, *args.Y, *args.Z); err != nil {
		return errResult("move failed: " + err.Error())
	}
	return jsonResult(map[string]any{"moved": true, "state": stateMap(c.State())})
}

func (s *server) toolLook(rawArgs json.RawMessage) toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	var args struct {
		Yaw   *float32 `json:"yaw"`
		Pitch *float32 `json:"pitch"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errResult("invalid arguments: " + err.Error())
	}
	if args.Yaw == nil || args.Pitch == nil {
		return errResult("bot_look requires yaw, pitch")
	}
	if err := c.Look(*args.Yaw, *args.Pitch); err != nil {
		return errResult("look failed: " + err.Error())
	}
	return jsonResult(map[string]any{"looked": true, "yaw": *args.Yaw, "pitch": *args.Pitch})
}

func (s *server) toolChat(rawArgs json.RawMessage) toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	var args struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errResult("invalid arguments: " + err.Error())
	}
	if args.Message == "" {
		return errResult("bot_chat requires a non-empty message")
	}
	if err := c.Chat(args.Message); err != nil {
		return errResult("chat failed: " + err.Error())
	}
	isCmd := args.Message[0] == '/'
	return jsonResult(map[string]any{"sent": args.Message, "as_command": isCmd})
}

func (s *server) toolUseItem(rawArgs json.RawMessage) toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	hand := 0
	if len(rawArgs) > 0 {
		var args struct {
			Hand *int `json:"hand"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return errResult("invalid arguments: " + err.Error())
		}
		if args.Hand != nil {
			hand = *args.Hand
		}
	}
	if hand != 0 && hand != 1 {
		return errResult("hand must be 0 (main) or 1 (off)")
	}
	if err := c.UseItem(hand); err != nil {
		return errResult("use_item failed: " + err.Error())
	}
	return jsonResult(map[string]any{"used": true, "hand": hand})
}

func (s *server) toolSelectSlot(rawArgs json.RawMessage) toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	var args struct {
		Slot *int32 `json:"slot"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errResult("invalid arguments: " + err.Error())
	}
	if args.Slot == nil {
		return errResult("bot_select_slot requires slot (0..8)")
	}
	if err := c.SelectSlot(*args.Slot); err != nil {
		return errResult("select_slot failed: " + err.Error())
	}
	return jsonResult(map[string]any{"selected_slot": *args.Slot})
}

func (s *server) toolAttack(rawArgs json.RawMessage) toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	var args struct {
		EntityID *int32 `json:"entity_id"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errResult("invalid arguments: " + err.Error())
	}
	if args.EntityID == nil {
		return errResult("bot_attack requires entity_id")
	}
	if err := c.Attack(*args.EntityID); err != nil {
		return errResult("attack failed: " + err.Error())
	}
	return jsonResult(map[string]any{"attacked": *args.EntityID})
}

func (s *server) toolListEntities() toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	ents := c.Entities()
	rows := make([]map[string]any, 0, len(ents))
	for _, e := range ents {
		rows = append(rows, map[string]any{
			"id":           e.ID,
			"type":         e.TypeID,
			"x":            e.X,
			"y":            e.Y,
			"z":            e.Z,
			"move_count":   e.MoveCount,
			"saw_head_rot": e.SawHeadRot,
		})
	}
	return jsonResult(map[string]any{"count": len(rows), "entities": rows})
}

func (s *server) toolRecentChat(rawArgs json.RawMessage) toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	n := 0
	if len(rawArgs) > 0 {
		var args struct {
			N *int `json:"n"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return errResult("invalid arguments: " + err.Error())
		}
		if args.N != nil {
			n = *args.N
		}
	}
	chat := c.RecentChat(n)
	return jsonResult(map[string]any{"count": len(chat), "chat": chat})
}

func (s *server) toolWaitTicks(ctx context.Context, rawArgs json.RawMessage) toolResult {
	c, err := s.getBot()
	if err != nil {
		return errResult(err.Error())
	}
	var args struct {
		Ticks *int `json:"ticks"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errResult("invalid arguments: " + err.Error())
	}
	if args.Ticks == nil || *args.Ticks <= 0 {
		return errResult("bot_wait_ticks requires ticks > 0")
	}
	if err := c.WaitTicks(ctx, *args.Ticks); err != nil {
		return errResult("wait failed: " + err.Error())
	}
	return jsonResult(map[string]any{"waited_ticks": *args.Ticks, "state": stateMap(c.State())})
}

func (s *server) toolDisconnect() toolResult {
	s.botMu.Lock()
	defer s.botMu.Unlock()
	if s.bot == nil {
		return jsonResult(map[string]any{"disconnected": false, "reason": "not connected"})
	}
	err := s.bot.Close()
	s.bot = nil
	if err != nil {
		return jsonResult(map[string]any{"disconnected": true, "close_error": err.Error()})
	}
	return jsonResult(map[string]any{"disconnected": true})
}

// stateMap renders a BotState as a JSON-friendly map.
func stateMap(st botclient.BotState) map[string]any {
	return map[string]any{
		"x":         st.X,
		"y":         st.Y,
		"z":         st.Z,
		"yaw":       st.Yaw,
		"pitch":     st.Pitch,
		"on_ground": st.OnGround,
		"connected": st.Connected,
	}
}

// ---- output (serialized one JSON line per message) -----------------------------------------

func (s *server) writeResult(id json.RawMessage, result any) {
	s.writeMessage(rpcResponse{JSONRPC: "2.0", ID: idOrNull(id), Result: result})
}

func (s *server) writeError(id json.RawMessage, code int, msg string) {
	s.writeMessage(rpcResponse{JSONRPC: "2.0", ID: idOrNull(id), Error: &rpcError{Code: code, Message: msg}})
}

// idOrNull returns the request id, or a JSON null when absent (parse errors / unattributable).
func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// writeMessage marshals one JSON-RPC message and writes it as a single newline-terminated line
// to stdout (the stdio transport frames messages by newline). Writes are serialized by outMu.
func (s *server) writeMessage(msg rpcResponse) {
	b, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[botmcp] marshal response failed:", err)
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Write(b)
	s.out.WriteByte('\n')
	s.out.Flush()
}
