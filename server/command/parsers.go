package command

import (
	"io"
	"strconv"
	"strings"

	pk "github.com/imhinotori/sulfur/net/packet"
)

type Parser interface {
	Parse(cmd string) (left string, value ParsedData, err error)
}

type StringParser int32

// parserIDBrigadierString is the ClientboundCommands argument-parser registry id for
// brigadier:string. Since 1.19 (proto 759+) the parser in a command-graph ArgumentNode is a
// VarInt INDEX into net.minecraft.commands.synchronization.ArgumentTypeInfos' registration
// order, NOT an Identifier string (the pre-1.19 wire). Registration order (javap'd from
// temp/cache/26.2-inner.jar, ArgumentTypeInfos.bootstrap): bool=0, float=1, double=2,
// integer=3, long=4, string=5. Emitting the old Identifier here made a real 26.2 client
// "Failed to decode packet clientbound/minecraft:commands" — the client read the string bytes
// where it expected the VarInt id and mis-framed the rest of the graph.
const parserIDBrigadierString = 5

func (s StringParser) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		pk.VarInt(parserIDBrigadierString), // parser registry id (NOT an Identifier — proto 759+)
		pk.VarInt(s),                       // StringArgumentType behavior: 0 word, 1 phrase, 2 greedy
	}.WriteTo(w)
}

func (s StringParser) Parse(cmd string) (left string, value ParsedData, err error) {
	switch s {
	case 2: // Greedy Phrase
		return "", cmd, nil
	case 1: // Quotable Phrase
		if len(cmd) > 0 && cmd[0] == '"' {
			var sb strings.Builder
			var isEscaping bool
			for i, v := range cmd[1:] {
				if isEscaping {
					isEscaping = false
					switch v {
					case '\\':
						sb.WriteRune('\\')
					case '"':
						sb.WriteRune('"')
					}
				} else if v == '\\' {
					isEscaping = true
				} else if v == '"' {
					return cmd[:i], sb.String(), nil
				} else {
					sb.WriteRune(v)
				}
			}
			return cmd, nil, ParseErr{
				Pos: len(cmd) - 1,
				Err: "expected '\"'",
			}
		}
		fallthrough
	case 0: // Single Word
		i := strings.IndexAny(cmd, "\t\n\v\f\r ")
		if i == -1 {
			return "", cmd, nil
		}
		return cmd[i:], cmd[:i], nil
	default:
		panic("StringParser: unknown format 0x" + strconv.FormatInt(int64(s), 16))
	}
}

type ParseErr struct {
	Pos int
	Err string
}

func (p ParseErr) Error() string {
	return p.Err
}
