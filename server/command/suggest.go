package command

import "strings"

// suggest.go -- server-side command tab-completion over the Graph. Mirrors the vanilla flow of
// Commands.performSuggestion / CommandSuggestions: on a ServerboundCommandSuggestion the server
// parses the partial command along the command tree and, at the node the cursor sits in, returns
// the candidate completions for the current partial token. For a LITERAL frontier the candidates
// are the child literal names; for an ARGUMENT frontier whose parser has a dynamic domain
// (online players for entity/game_profile/score_holder, registry ids for resource, rule names for
// gamerule, ...) the candidates come from a server-supplied resolver keyed by the parser type.
//
// [VERIFIED javap net.minecraft.commands.Commands.performSuggestion +
//
//	net.minecraft.commands.arguments.* getSuggestions: the server answers ASK_SERVER by parsing
//	the command and building a SuggestionsBuilder at the cursor, then sends
//	ClientboundCommandSuggestionsPacket(id, start, length, entries).]

// SuggestKind classifies an argument node's dynamic suggestion domain so the server resolver can
// map it to live data. It is set on the argument node at build time; empty means "no server-side
// dynamic suggestions" (the client already has the local domain for gamemode/dimension/etc.).
type SuggestKind string

const (
	SuggestNone      SuggestKind = ""
	SuggestPlayers   SuggestKind = "players"     // online player names (entity/game_profile/score_holder)
	SuggestGamerule  SuggestKind = "gamerule"    // gamerule names
	SuggestResource  SuggestKind = "resource"    // registry ids (the parser's Registry)
	SuggestBlock     SuggestKind = "block"       // block ids (block_state/block_pos block)
	SuggestItem      SuggestKind = "item"        // item ids (item_stack)
	SuggestEntity    SuggestKind = "entity_type" // summonable entity ids (summon)
	SuggestObjective SuggestKind = "objective"   // scoreboard objective names
	SuggestTeam      SuggestKind = "team"        // team names
)

// SuggestResolver maps a SuggestKind (+ the parser's registry hint for SuggestResource) to the
// full candidate list; the walker filters by the current partial token prefix. Supplied by the
// server (which owns the live player list / registries).
type SuggestResolver func(kind SuggestKind, registry string) []string

// Suggest computes the tab-completion for cmd. It returns start (the byte offset in cmd where the
// current token begins) and the candidate completions (already prefix-filtered). The client
// replaces cmd[start:] with the chosen candidate. resolve may be nil (only literal candidates).
func (g *Graph) Suggest(cmd string, resolve SuggestResolver) (start int, candidates []string) {
	node := g.nodes[0] // root
	consumed := 0      // bytes of cmd consumed by fully-parsed tokens
	rest := cmd
	for {
		// Trim leading whitespace, tracking the offset so `start` is absolute in cmd.
		trimmed := strings.TrimLeft(rest, whitespaceCutset)
		consumed += len(rest) - len(trimmed)
		rest = trimmed

		// Is the remainder a single, still-being-typed token (no whitespace)? If so, this is the
		// frontier: suggest for `node`'s children against the partial token `rest`.
		if idx := strings.IndexAny(rest, whitespaceCutset); idx == -1 {
			return consumed, frontierCandidates(g, node, rest, resolve)
		}

		// Otherwise a full token is present -> descend to the matching child and continue.
		ws := strings.IndexAny(rest, whitespaceCutset)
		next := advance(g, node, rest)
		if next == 0 {
			return consumed + len(rest), nil // no child accepts this token: no suggestions
		}
		token := rest[:ws]
		consumed += len(token)
		rest = rest[ws:]
		node = g.nodes[next]
	}
}

// frontierCandidates gathers completions for the partial token at `node`'s children.
func frontierCandidates(g *Graph, node *Node, partial string, resolve SuggestResolver) []string {
	var out []string
	for _, ci := range node.Children {
		child := g.nodes[ci]
		switch child.kind & 0x03 {
		case LiteralNode:
			if strings.HasPrefix(child.Name, partial) {
				out = append(out, child.Name)
			}
		case ArgumentNode:
			if resolve != nil && child.SuggestKind != SuggestNone {
				for _, cand := range resolve(child.SuggestKind, child.SuggestRegistry) {
					if strings.HasPrefix(cand, partial) {
						out = append(out, cand)
					}
				}
			}
		}
	}
	return out
}

// advance returns the index of the child of `node` that accepts the leading token of `rest`, or 0.
// A literal child must name-match the token; an argument child accepts any token (the first one).
func advance(g *Graph, node *Node, rest string) int32 {
	idx := strings.IndexAny(rest, whitespaceCutset)
	token := rest
	if idx != -1 {
		token = rest[:idx]
	}
	var argChild int32
	for _, ci := range node.Children {
		child := g.nodes[ci]
		switch child.kind & 0x03 {
		case LiteralNode:
			if child.Name == token {
				return ci
			}
		case ArgumentNode:
			if argChild == 0 {
				argChild = ci
			}
		}
	}
	return argChild
}
