package server

import "github.com/imhinotori/sulfur/plugin/host"

// plugin_chat_bridge.go wires the plugin host's chat() builtin onto the player-facing wire
// (PLUGIN-07 / Plan 28-02). plugin/host owns the chat(msg) builtin but has NO server import
// (the one-direction layering: server depends on host, never the reverse). The bridge closes
// that gap from the SERVER side: it installs a sink on the Manager that fans the plugin's
// message to every player as a ClientboundSystemChat via the existing broadcastSystemChat seam.
//
// The result: a plugin event hook (e.g. gate_events' on_block_break) that calls chat("...")
// lands a ClientboundSystemChat on every connected client — the observable proof a hook fired,
// which the gate bot decodes (checklist item #4).
//
// CONCURRENCY (TICK-05 / cite chat.go::broadcastSystemChat): the sink is invoked from inside
// Manager.Emit, which the server calls at the discrete gameplay seams (block break / player
// join) ON THE TICK GOROUTINE. So the sink — and therefore broadcastSystemChat's fan over the
// tick-owned t.players slice — always runs on the owner goroutine, exactly like handleChat's
// inline broadcast. No new goroutine, no off-tick game-state access. (The python off-tick lane
// dispatches its hooks via a SEPARATE callback — pythonDispatch — never through this sink, so
// chat() from a python hook does not exist; only the on-tick starlark Emit reaches the sink.)
//
// SECURITY (T-28-08): the sink hands the Manager a plain func(string) — no entity/world handle,
// no capability bypass. chat() can ONLY emit server-controlled text through broadcastSystemChat
// (the same path player chat uses). T-28-06 (accept): a plugin choosing chat() opts into
// broadcasting to all players — the documented behavior of the builtin, not a leak.

// InstallChatSink installs the chat() output sink on the plugin Manager so a plugin's chat(msg)
// reaction fans to every player as a ClientboundSystemChat. Called ONCE at boot AFTER SetPlugins,
// before tick.Run (single-owner setup — TICK-05). A nil Manager is a no-op (a server with no
// plugin host). The captured `t.broadcastSystemChat(text)` runs on the tick goroutine because
// the Manager only calls the sink from Emit, which fires on the tick at the discrete seams.
func (t *TickLoop) InstallChatSink(m *host.Manager) {
	if m == nil {
		return
	}
	m.SetChatSink(func(text string) {
		t.broadcastSystemChat(text)
	})
}
