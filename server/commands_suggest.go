package server

// commands_suggest.go -- server-side command tab-completion (Part B). Handles
// ServerboundCommandSuggestion by walking the shared command.Graph and replies with
// ClientboundCommandSuggestions.
//
// WIRE (jar-verified from temp/cache/26.2-inner.jar):
//   ServerboundCommandSuggestionPacket: readVarInt id, readUtf(32500) command.
//   ClientboundCommandSuggestionsPacket: writeVarInt id, writeVarInt start, writeVarInt length,
//     then a list<Entry> (VarInt count + entries); Entry = String text + Optional<Component>
//     tooltip (ComponentSerialization.TRUSTED_OPTIONAL). We send no tooltips (empty Optional).
// [VERIFIED javap ServerboundCommandSuggestionPacket / ClientboundCommandSuggestionsPacket +
//  ClientboundCommandSuggestionsPacket$Entry.STREAM_CODEC.]

import (
	"io"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/command"
)

// runCommandSuggestion decodes a ServerboundCommandSuggestion and replies with the completions for
// the current partial token. Defensive: a Scan error is a silent no-op (T-3-02); NEVER panics.
// Runs on the tick goroutine (reads tick-owned player list + registries -- TICK-05).
func (t *TickLoop) runCommandSuggestion(p *tickPlayer, packet pk.Packet) {
	if p == nil || p.client == nil {
		return
	}
	var id pk.VarInt
	var cmdText pk.String
	if err := packet.Scan(&id, &cmdText); err != nil {
		return // malformed: silent no-op
	}
	cmd := string(cmdText)
	if len(cmd) > maxCommandLen {
		return // length bound before any parse work (ASVS V5)
	}
	// The client sends the full input with the leading '/'; the graph walks the command without it.
	body := cmd
	offset := 0
	if len(body) > 0 && body[0] == '/' {
		body = body[1:]
		offset = 1
	}
	start, cands := cmdGraph.Suggest(body, t.suggestionResolver(p))
	p.client.Send(writeCommandSuggestions(int32(id), start+offset, len(cmd)-(start+offset), cands))
}

// suggestionResolver supplies the live candidate lists for the dynamic suggestion kinds the
// command graph declares (players / gamerule names / registry ids / block / item / entity / etc.).
func (t *TickLoop) suggestionResolver(_ *tickPlayer) command.SuggestResolver {
	return func(kind command.SuggestKind, registry string) []string {
		switch kind {
		case command.SuggestPlayers:
			out := make([]string, 0, len(t.players))
			for _, pl := range t.players {
				out = append(out, pl.name)
			}
			return out
		case command.SuggestGamerule:
			gr := t.gamerules
			if gr == nil {
				gr = newGameRules()
			}
			out := make([]string, 0, len(gr.bools)+len(gr.ints))
			for k := range gr.bools {
				out = append(out, k)
			}
			for k := range gr.ints {
				out = append(out, k)
			}
			return out
		case command.SuggestBlock:
			out := make([]string, 0, len(block.DefaultStateID))
			for name := range block.DefaultStateID {
				out = append(out, name)
			}
			return out
		case command.SuggestItem:
			out := make([]string, 0, len(item.ByID))
			for idx := range item.ByID {
				if it := item.ByID[idx]; it != nil {
					out = append(out, "minecraft:"+it.Name)
				}
			}
			return out
		case command.SuggestEntity:
			if t.mobRegistry == nil {
				return nil
			}
			out := make([]string, 0, len(t.mobRegistry.byName))
			for name := range t.mobRegistry.byName {
				out = append(out, "minecraft:"+trimVanillaPrefix(name))
			}
			return out
		case command.SuggestObjective:
			if t.scoreboard == nil {
				return nil
			}
			out := make([]string, 0, len(t.scoreboard.objectives))
			for n := range t.scoreboard.objectives {
				out = append(out, n)
			}
			return out
		case command.SuggestTeam:
			if t.scoreboard == nil {
				return nil
			}
			out := make([]string, 0, len(t.scoreboard.teams))
			for n := range t.scoreboard.teams {
				out = append(out, n)
			}
			return out
		case command.SuggestResource:
			return resourceSuggestions(t, registry)
		}
		return nil
	}
}

// trimVanillaPrefix strips the internal "vanilla_" mob-registry name prefix for display ids.
func trimVanillaPrefix(name string) string {
	const p = "vanilla_"
	if len(name) >= len(p) && name[:len(p)] == p {
		return name[len(p):]
	}
	return name
}

// resourceSuggestions maps a registry Identifier to its candidate ids for the resource arg types.
func resourceSuggestions(t *TickLoop, registry string) []string {
	switch registry {
	case "minecraft:entity_type":
		return t.suggestionResolver(nil)(command.SuggestEntity, "")
	}
	return nil
}

// suggestionEntries encodes the Entry list of ClientboundCommandSuggestions: VarInt count then,
// per Entry, String text + Optional<Component> tooltip. We send no tooltip (Boolean false =
// absent), matching a server that answers ASK_SERVER with plain string candidates.
// [VERIFIED javap ClientboundCommandSuggestionsPacket$Entry.STREAM_CODEC: STRING_UTF8 text +
//
//	TRUSTED_OPTIONAL Component tooltip.]
type suggestionEntries []string

func (s suggestionEntries) WriteTo(w io.Writer) (int64, error) {
	fields := make(pk.Tuple, 0, 1+len(s)*2)
	fields = append(fields, pk.VarInt(len(s)))
	for _, c := range s {
		fields = append(fields, pk.String(c), pk.Boolean(false))
	}
	return fields.WriteTo(w)
}

// writeCommandSuggestions builds a ClientboundCommandSuggestions with the given absolute token
// start, replaced length, and candidate texts (no tooltips).
func writeCommandSuggestions(id int32, start, length int, cands []string) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundCommandSuggestions),
		pk.VarInt(id),
		pk.VarInt(int32(start)),
		pk.VarInt(int32(length)),
		suggestionEntries(cands),
	)
}
