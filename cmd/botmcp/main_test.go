package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// runServerLines feeds the given newline-delimited JSON-RPC request lines through the server and
// returns the decoded responses (in order). It uses an in-memory stdin and captures stdout.
func runServerLines(t *testing.T, lines ...string) []rpcResponse {
	t.Helper()
	var outBuf bytes.Buffer
	s := &server{out: bufio.NewWriter(&outBuf)}
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := s.run(in); err != nil && err.Error() != "EOF" {
		// io.EOF is the normal end; anything else is unexpected.
		t.Fatalf("run returned %v", err)
	}
	// Tear down any bot the calls created.
	if s.bot != nil {
		_ = s.bot.Close()
	}

	var resps []rpcResponse
	sc := bufio.NewScanner(&outBuf)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("response not valid JSON: %q: %v", line, err)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestInitializeHandshake(t *testing.T) {
	resps := runServerLines(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	r := resps[0]
	if r.Error != nil {
		t.Fatalf("initialize returned error: %+v", r.Error)
	}
	result, ok := r.Result.(map[string]any)
	if !ok {
		t.Fatalf("initialize result not an object: %T", r.Result)
	}
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %s", result["protocolVersion"], mcpProtocolVersion)
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities missing/invalid: %v", result["capabilities"])
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("capabilities.tools missing: %v", caps)
	}
	si, ok := result["serverInfo"].(map[string]any)
	if !ok || si["name"] != serverName {
		t.Fatalf("serverInfo = %v, want name %s", result["serverInfo"], serverName)
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	resps := runServerLines(t,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	)
	if len(resps) != 0 {
		t.Fatalf("notification produced %d responses, want 0", len(resps))
	}
}

func TestToolsListReturnsCatalog(t *testing.T) {
	resps := runServerLines(t,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("tools/list response unexpected: %+v", resps)
	}
	result := resps[0].Result.(map[string]any)
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools not an array: %T", result["tools"])
	}
	want := map[string]bool{
		"bot_connect": false, "bot_state": false, "bot_move_to": false, "bot_look": false,
		"bot_chat": false, "bot_use_item": false, "bot_select_slot": false, "bot_attack": false,
		"bot_list_entities": false, "bot_recent_chat": false, "bot_wait_ticks": false,
		"bot_disconnect": false,
	}
	for _, tl := range tools {
		td := tl.(map[string]any)
		name, _ := td["name"].(string)
		if _, expected := want[name]; expected {
			want[name] = true
		}
		if td["description"] == "" || td["inputSchema"] == nil {
			t.Errorf("tool %q missing description/inputSchema", name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q missing from catalog", name)
		}
	}
}

func TestToolsCallBeforeConnectIsError(t *testing.T) {
	// Calling bot_state before bot_connect should yield an isError tool result (not a protocol error).
	resps := runServerLines(t,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"bot_state","arguments":{}}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("tools/call should not be a protocol error: %+v", resps[0].Error)
	}
	result := resps[0].Result.(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true tool result, got %v", result)
	}
	content := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("error result has no content")
	}
	block := content[0].(map[string]any)
	if !strings.Contains(block["text"].(string), "not connected") {
		t.Fatalf("error text = %q, want it to mention 'not connected'", block["text"])
	}
}

func TestUnknownToolIsError(t *testing.T) {
	resps := runServerLines(t,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"bot_nope","arguments":{}}}`,
	)
	result := resps[0].Result.(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("unknown tool should be isError, got %v", result)
	}
}

func TestUnknownMethodIsProtocolError(t *testing.T) {
	resps := runServerLines(t,
		`{"jsonrpc":"2.0","id":5,"method":"no/such/method"}`,
	)
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("unknown method should be a protocol error, got %+v", resps)
	}
	if resps[0].Error.Code != codeMethodNotFound {
		t.Fatalf("error code = %d, want %d", resps[0].Error.Code, codeMethodNotFound)
	}
}

func TestParseErrorHasNullID(t *testing.T) {
	resps := runServerLines(t, `{not valid json`)
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("parse error should produce an error response, got %+v", resps)
	}
	if resps[0].Error.Code != codeParseError {
		t.Fatalf("error code = %d, want %d", resps[0].Error.Code, codeParseError)
	}
	if string(resps[0].ID) != "null" {
		t.Fatalf("parse-error id = %q, want null", resps[0].ID)
	}
}

func TestRequestIDEchoedVerbatim(t *testing.T) {
	// A string id must be echoed as a string; a number id as a number.
	resps := runServerLines(t,
		`{"jsonrpc":"2.0","id":"abc","method":"ping"}`,
		`{"jsonrpc":"2.0","id":99,"method":"ping"}`,
	)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2", len(resps))
	}
	if string(resps[0].ID) != `"abc"` {
		t.Fatalf("string id = %q, want \"abc\"", resps[0].ID)
	}
	if string(resps[1].ID) != "99" {
		t.Fatalf("number id = %q, want 99", resps[1].ID)
	}
}
