package command

import (
	"io"
	"unsafe"

	pk "github.com/imhinotori/sulfur/net/packet"
)

const (
	isExecutable = 1 << (iota + 2)
	hasRedirect
	hasSuggestionsType
)

func (g *Graph) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		pk.Array(g.nodes),
		pk.VarInt(0),
	}.WriteTo(w)
}

func (n Node) WriteTo(w io.Writer) (int64, error) {
	var flag byte
	flag |= n.kind & 0x03
	if n.Run != nil {
		flag |= isExecutable
	}
	// FLAG_CUSTOM_SUGGESTIONS (0x10): an argument node whose completions the server resolves
	// (ASK_SERVER). [VERIFIED javap ClientboundCommandsPacket$ArgumentNodeStub.write: when
	// suggestionId != null the node sets the custom-suggestions flag + writes the Identifier.]
	if n.kind&0x03 == ArgumentNode && n.SuggestKind != SuggestNone {
		flag |= hasSuggestionsType
	}
	return pk.Tuple{
		pk.Byte(flag),
		pk.Array((*[]pk.VarInt)(unsafe.Pointer(&n.Children))),
		pk.Opt{
			Has:   func() bool { return n.kind&hasRedirect != 0 },
			Field: nil, // TODO: send redirect node
		},
		pk.Opt{
			Has:   func() bool { return n.kind == ArgumentNode || n.kind == LiteralNode },
			Field: pk.String(n.Name),
		},
		pk.Opt{
			Has:   func() bool { return n.kind == ArgumentNode },
			Field: n.Parser, // Parser identifier and Properties
		},
		pk.Opt{
			Has:   func() bool { return flag&hasSuggestionsType != 0 },
			Field: pk.Identifier("minecraft:ask_server"), // ASK_SERVER: the client requests
			// ServerboundCommandSuggestion for this arg; the server answers from the live graph.
		},
	}.WriteTo(w)
}
