package main

// tools.go holds the static MCP tool catalog returned by tools/list. Each tool's inputSchema
// is a JSON Schema object describing its arguments; the dispatch in main.go validates +
// executes them. The catalog is data-only (no behavior), kept separate so the tool surface is
// readable at a glance.

// schema builds a JSON Schema object with the given properties and required field list.
func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	} else {
		s["required"] = []string{}
	}
	return s
}

// num / str / boolp / integer are JSON Schema property fragments with a description.
func num(desc string) map[string]any     { return map[string]any{"type": "number", "description": desc} }
func integer(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func str(desc string) map[string]any     { return map[string]any{"type": "string", "description": desc} }

// toolCatalog returns every tool botmcp exposes. The order is the natural verification flow:
// connect, observe, act, observe-results.
func toolCatalog() []toolDef {
	return []toolDef{
		{
			Name: "bot_connect",
			Description: "Connect the verification bot to a running Sulfur server (full proto-776 " +
				"handshake/login/config/play bootstrap). Defaults: addr 127.0.0.1:25565, name " +
				"verifierbot. Returns the post-connect bot state. Call this first.",
			InputSchema: schema(map[string]any{
				"addr": str("server address host:port (default 127.0.0.1:25565)"),
				"name": str("offline player name (default verifierbot)"),
			}),
		},
		{
			Name:        "bot_state",
			Description: "Return the bot's current position (x,y,z), look (yaw,pitch), on_ground, and connected flag.",
			InputSchema: schema(nil),
		},
		{
			Name: "bot_move_to",
			Description: "Walk the bot toward (x,y,z) at ~0.2 blocks/tick until within ~0.5 blocks or " +
				"timeout. Returns the final state.",
			InputSchema: schema(map[string]any{
				"x": num("target X"),
				"y": num("target Y"),
				"z": num("target Z"),
			}, "x", "y", "z"),
		},
		{
			Name:        "bot_look",
			Description: "Aim the bot's view (sends MovePlayerRot). yaw 0=+Z increasing clockwise; pitch -90=up..90=down.",
			InputSchema: schema(map[string]any{
				"yaw":   num("yaw degrees"),
				"pitch": num("pitch degrees (-90..90)"),
			}, "yaw", "pitch"),
		},
		{
			Name: "bot_chat",
			Description: "Send a chat message, or a command if it starts with '/'. Commands are sent as " +
				"ServerboundChatCommand (the slash is stripped). '/dbg pig' spawns a pig at the bot " +
				"(NO op required — the v1 all-players-operator policy grants it); its '[dbg] spawned " +
				"pig eid=...' ack appears in bot_recent_chat. '/tp <x> <y> <z>' moves the bot.",
			InputSchema: schema(map[string]any{
				"message": str("chat text, or a command beginning with '/' (e.g. '/dbg pig', '/tp 0 64 0')"),
			}, "message"),
		},
		{
			Name: "bot_use_item",
			Description: "Right-click with the held item (ServerboundUseItem). hand 0=main (default), 1=off. " +
				"Eats food; note a spawn egg spawns on a clicked BLOCK, not on air, so prefer '/dbg pig' " +
				"via bot_chat to spawn a mob.",
			InputSchema: schema(map[string]any{
				"hand": integer("0=main hand (default), 1=off hand"),
			}),
		},
		{
			Name:        "bot_select_slot",
			Description: "Select a hotbar slot 0..8 (ServerboundSetCarriedItem).",
			InputSchema: schema(map[string]any{
				"slot": integer("hotbar slot 0..8"),
			}, "slot"),
		},
		{
			Name: "bot_attack",
			Description: "Attack an entity by id (ServerboundAttack + Swing). The server reach-gates and " +
				"applies authoritative damage. Use bot_list_entities to find a target id.",
			InputSchema: schema(map[string]any{
				"entity_id": integer("the target entity id"),
			}, "entity_id"),
		},
		{
			Name: "bot_list_entities",
			Description: "List the live entity table the read loop maintains: id, type, position, " +
				"move_count (>0 means the entity is moving/alive), saw_head_rot. THIS is the core " +
				"verification read: spawn a mob, wait ticks, then list to assert it exists, has the " +
				"expected type, and move_count>0.",
			InputSchema: schema(nil),
		},
		{
			Name:        "bot_recent_chat",
			Description: "Return the last n decoded system-chat texts (default: all). Read /dbg acks and broadcasts here.",
			InputSchema: schema(map[string]any{
				"n": integer("how many recent lines (default all)"),
			}),
		},
		{
			Name: "bot_wait_ticks",
			Description: "Advance time by sleeping ticks*50ms while the read loop keeps state current, " +
				"so a just-spawned mob accumulates movement before you read it. Returns the state after waiting.",
			InputSchema: schema(map[string]any{
				"ticks": integer("number of 50ms game ticks to wait"),
			}, "ticks"),
		},
		{
			Name:        "bot_disconnect",
			Description: "Disconnect the bot and tear down the connection.",
			InputSchema: schema(nil),
		},
	}
}
