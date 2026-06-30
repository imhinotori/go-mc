# botmcp — headless Sulfur verification bot as an MCP server

`botmcp` is a hand-rolled [Model Context Protocol](https://modelcontextprotocol.io) **stdio**
server that exposes a headless proto-776 Minecraft bot (`internal/botclient`) as a set of MCP
tools. It lets an AI agent connect to a **running Sulfur server**, observe the world it streams,
move the bot, spawn/observe/attack mobs, and **assert gameplay — without a real Minecraft
client**.

This is **internal verification tooling**, not gameplay. The 1:1-jar-port mandate does not
apply here; it is normal idiomatic Go. There are **no new dependencies** — the MCP JSON-RPC is
hand-rolled with stdlib `encoding/json` + `bufio`, so the default `CGO_ENABLED=0` pure-Go
static binary stays clean.

## Build & run

```sh
CGO_ENABLED=0 go build -o botmcp ./cmd/botmcp
./botmcp            # speaks MCP over stdin/stdout; logs to stderr
```

`botmcp` reads newline-delimited JSON-RPC 2.0 messages on **stdin**, writes responses on
**stdout**, and logs on **stderr** (the MCP stdio transport). It does not connect to any server
on its own — the agent drives it via `bot_connect`.

## Point an MCP client at it

Most MCP clients launch the server as a subprocess. Example client config (Claude Desktop /
Cursor / any MCP host that supports a stdio command):

```json
{
  "mcpServers": {
    "sulfur-bot": {
      "command": "/absolute/path/to/botmcp"
    }
  }
}
```

If you prefer to run from source without building a binary:

```json
{
  "mcpServers": {
    "sulfur-bot": {
      "command": "go",
      "args": ["run", "./cmd/botmcp"],
      "env": { "CGO_ENABLED": "0" }
    }
  }
}
```

The handshake the client performs: `initialize` → `notifications/initialized` → `tools/list` /
`tools/call`. `botmcp` advertises `protocolVersion` `2025-06-18`, `capabilities.tools`, and its
`serverInfo`.

## Tools

| Tool | Arguments | What it does |
|------|-----------|--------------|
| `bot_connect` | `addr?` (default `127.0.0.1:25565`), `name?` (default `verifierbot`) | Full handshake → login → config → play bootstrap. Returns the post-connect state. **Call first.** |
| `bot_state` | — | Position (x,y,z), look (yaw,pitch), `on_ground`, `connected`. |
| `bot_move_to` | `x`, `y`, `z` | Walk toward the target (~0.2 blocks/tick) until within ~0.5 blocks. Returns the final state. |
| `bot_look` | `yaw`, `pitch` | Aim the view (MovePlayerRot). |
| `bot_chat` | `message` | Chat, or a command if it starts with `/`. `"/dbg pig"` spawns a pig; `"/tp x y z"` teleports. |
| `bot_use_item` | `hand?` (0 main, 1 off) | Right-click with the held item (eats food; spawn eggs need a block — prefer `/dbg pig`). |
| `bot_select_slot` | `slot` (0..8) | Select a hotbar slot. |
| `bot_attack` | `entity_id` | Attack an entity by id (Attack + Swing). Server reach-gates + applies authoritative damage. |
| `bot_list_entities` | — | The live entity table: `id`, `type`, `x/y/z`, `move_count`, `saw_head_rot`. **The core verification read.** |
| `bot_recent_chat` | `n?` | Last `n` system-chat texts (read `/dbg` acks + broadcasts). |
| `bot_wait_ticks` | `ticks` | Advance time `ticks*50ms` while the read loop keeps state current. |
| `bot_disconnect` | — | Tear down the connection. |

Each tool result is a single JSON **text content block** holding the structured result. A tool
failure is returned as an `isError` tool result with a human message (so the agent reads it as
data); only malformed JSON-RPC framing is a protocol-level error.

## Spawning a mob — the verified spawn lever

The simplest, **no-op-required** spawn lever is the `/dbg pig` debug command:

```
bot_chat       {"message": "/dbg pig"}
bot_wait_ticks {"ticks": 20}
bot_list_entities
```

Then assert: a pig appears in the entity table with the expected `type` and **`move_count > 0`**
(it spawned, is alive, and is moving under its AI). `bot_recent_chat` shows the server's ack,
e.g. `[dbg] spawned pig eid=42 at (...)`.

### Why no op is needed

`/dbg pig` is gated on the `command.tp` permission node, but the v1 permission policy
(`server/commands.go` → `playerHasPermission`) **returns `true` for every player** — an
all-players-operator model. There is no `ops.json` and no op flag to set; `/dbg pig` and `/tp`
run for any connected client. (When a real ops list lands, grant the bot's name op there.)

`/dbg` subcommands: `pig` (spawn a pig at the bot), `water` (fill a water box), `pig-in-water`.

### Alternative: the test-kit spawn egg

If the server is started with `SULFUR_TEST_KIT=1`, the join kit includes a custom-mob spawn egg
on hotbar slot 8 (`server/test_kit.go`). A spawn egg spawns on a **clicked block**, not on
right-click-air, so the egg path is:

```
bot_select_slot {"slot": 8}
# then UseItemOn a nearby block (handled by botclient.Client.UseItemOn; not yet a distinct MCP
# tool — /dbg pig is the documented default lever)
```

`/dbg pig` is preferred because it needs no server flag and no block targeting.

## Notes

- `botmcp` holds **one** bot client, lazily connected by `bot_connect` and kept alive by a
  background read loop (drains the server's bounded outbound queue, answers KeepAlive, acks
  chunk batches) plus a ~20Hz position-flush goroutine. State is read concurrently by tool calls
  under a mutex.
- Gate on `CGO_ENABLED=0 go build ./...` + `go vet` (gopls diagnostics are stale/false). The
  `-race` gate runs in the project's Docker CI (the race detector needs a C toolchain).
- End-to-end connection testing is left to the orchestrator (this package ships unit tests for
  the wire decoders, the entity table, and the JSON-RPC dispatch; it does not assume a live
  server).
