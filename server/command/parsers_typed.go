package command

import (
	"io"
	"strings"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// parsers_typed.go -- typed brigadier argument parsers for the ClientboundCommands graph.
// See VERIFIED notes per type; ids are the ArgumentTypeInfos.bootstrap registration index
// (javap'd 1:1 from temp/cache/26.2-inner.jar). Each parser WriteTo emits the VarInt id then
// the exact properties its *ArgumentInfo.serializeToNetwork writes. Parse consumes one word so
// dispatch walks a real multi-node tree; handlers reconstruct their tail (see server/commands_*).
// Protocol/command surface only -- no gameplay perturbation, pig oracle unaffected.

const (
	parserIDBrigadierBool     = 0
	parserIDBrigadierFloat    = 1
	parserIDBrigadierDouble   = 2
	parserIDBrigadierInteger  = 3
	parserIDBrigadierLong     = 4
	parserIDEntity            = 6
	parserIDGameProfile       = 7
	parserIDBlockPos          = 8
	parserIDVec3              = 10
	parserIDVec2              = 11
	parserIDBlockState        = 12
	parserIDItemStack         = 14
	parserIDTeamColor         = 16
	parserIDComponent         = 18
	parserIDMessage           = 20
	parserIDObjective         = 24
	parserIDObjectiveCriteria = 25
	parserIDScoreboardSlot    = 30
	parserIDScoreHolder       = 31
	parserIDTeam              = 33
	parserIDResourceLocation  = 36
	parserIDEntityAnchor      = 38
	parserIDIntRange          = 39
	parserIDFloatRange        = 40
	parserIDDimension         = 41
	parserIDGameMode          = 42
	parserIDTime              = 43
	parserIDResource          = 46
	parserIDResourceKey       = 47
)

// whitespaceCutset is the brigadier command whitespace set (space, tab, LF, VT, FF, CR),
// built from byte codes to avoid escape-sequence hazards in the source.
var whitespaceCutset = string([]byte{9, 10, 11, 12, 13, 32})

// parseWord consumes one leading whitespace-delimited token (StringArgumentType word semantics).
func parseWord(cmd string) (left string, value ParsedData, err error) {
	i := strings.IndexAny(cmd, whitespaceCutset)
	if i == -1 {
		return "", cmd, nil
	}
	return cmd[i:], cmd[:i], nil
}

// parseTokens consumes n whitespace-delimited tokens (n<=0 means 1), returning the remainder and
// the consumed tokens joined by a single space. Coordinate argument types (vec3/block_pos consume
// three, vec2 two) span several tokens on the wire; the leaf consumes the same count so the
// dispatcher descends the tree exactly as the client parses it, and the rebuilt tail is faithful.
func parseTokens(cmd string, n int) (string, ParsedData, error) {
	if n <= 0 {
		n = 1
	}
	rest := cmd
	got := make([]string, 0, n)
	for i := 0; i < n; i++ {
		left, v, err := parseWord(rest)
		if err != nil {
			return cmd, nil, err
		}
		s, _ := v.(string)
		got = append(got, s)
		rest = strings.TrimLeft(left, whitespaceCutset)
		if rest == "" && i+1 < n {
			break
		}
	}
	return rest, strings.Join(got, " "), nil
}

func numberFlags(hasMin, hasMax bool) byte {
	var f byte
	if hasMin {
		f |= 1
	}
	if hasMax {
		f |= 2
	}
	return f
}

// BoolParser is brigadier:bool (id 0), a contextFree SingletonArgumentInfo with NO properties.
type BoolParser struct{}

func (BoolParser) WriteTo(w io.Writer) (int64, error) {
	return pk.VarInt(parserIDBrigadierBool).WriteTo(w)
}
func (BoolParser) Parse(cmd string) (string, ParsedData, error) { return parseWord(cmd) }

// IntegerParser is brigadier:integer (id 3): flags byte (createNumberFlags) + optional min/max ints.
// [VERIFIED javap IntegerArgumentInfo.serializeToNetwork.]
type IntegerParser struct {
	Min, Max int32
	HasMin   bool
	HasMax   bool
}

func (p IntegerParser) WriteTo(w io.Writer) (int64, error) {
	fields := pk.Tuple{pk.VarInt(parserIDBrigadierInteger), pk.Byte(numberFlags(p.HasMin, p.HasMax))}
	if p.HasMin {
		fields = append(fields, pk.Int(p.Min))
	}
	if p.HasMax {
		fields = append(fields, pk.Int(p.Max))
	}
	return fields.WriteTo(w)
}
func (p IntegerParser) Parse(cmd string) (string, ParsedData, error) { return parseWord(cmd) }

// FloatParser is brigadier:float (id 1). [VERIFIED javap FloatArgumentInfo.serializeToNetwork.]
type FloatParser struct {
	Min, Max float32
	HasMin   bool
	HasMax   bool
}

func (p FloatParser) WriteTo(w io.Writer) (int64, error) {
	fields := pk.Tuple{pk.VarInt(parserIDBrigadierFloat), pk.Byte(numberFlags(p.HasMin, p.HasMax))}
	if p.HasMin {
		fields = append(fields, pk.Float(p.Min))
	}
	if p.HasMax {
		fields = append(fields, pk.Float(p.Max))
	}
	return fields.WriteTo(w)
}
func (p FloatParser) Parse(cmd string) (string, ParsedData, error) { return parseWord(cmd) }

// DoubleParser is brigadier:double (id 2). [VERIFIED javap DoubleArgumentInfo.serializeToNetwork.]
type DoubleParser struct {
	Min, Max float64
	HasMin   bool
	HasMax   bool
}

func (p DoubleParser) WriteTo(w io.Writer) (int64, error) {
	fields := pk.Tuple{pk.VarInt(parserIDBrigadierDouble), pk.Byte(numberFlags(p.HasMin, p.HasMax))}
	if p.HasMin {
		fields = append(fields, pk.Double(p.Min))
	}
	if p.HasMax {
		fields = append(fields, pk.Double(p.Max))
	}
	return fields.WriteTo(w)
}
func (p DoubleParser) Parse(cmd string) (string, ParsedData, error) { return parseWord(cmd) }

// LongParser is brigadier:long (id 4). [VERIFIED javap LongArgumentInfo.serializeToNetwork.]
type LongParser struct {
	Min, Max int64
	HasMin   bool
	HasMax   bool
}

func (p LongParser) WriteTo(w io.Writer) (int64, error) {
	fields := pk.Tuple{pk.VarInt(parserIDBrigadierLong), pk.Byte(numberFlags(p.HasMin, p.HasMax))}
	if p.HasMin {
		fields = append(fields, pk.Long(p.Min))
	}
	if p.HasMax {
		fields = append(fields, pk.Long(p.Max))
	}
	return fields.WriteTo(w)
}
func (p LongParser) Parse(cmd string) (string, ParsedData, error) { return parseWord(cmd) }

// EntityParser is minecraft:entity (id 6): flags byte bit0=single, bit1=playersOnly.
// [VERIFIED javap EntityArgument$Info.serializeToNetwork.]
type EntityParser struct {
	Single      bool
	PlayersOnly bool
}

func (p EntityParser) WriteTo(w io.Writer) (int64, error) {
	var flags byte
	if p.Single {
		flags |= 1
	}
	if p.PlayersOnly {
		flags |= 2
	}
	return pk.Tuple{pk.VarInt(parserIDEntity), pk.Byte(flags)}.WriteTo(w)
}
func (p EntityParser) Parse(cmd string) (string, ParsedData, error) { return parseWord(cmd) }

// ScoreHolderParser is minecraft:score_holder (id 31): flags byte bit0=multiple.
// [VERIFIED javap ScoreHolderArgument$Info.serializeToNetwork.]
type ScoreHolderParser struct {
	Multiple bool
}

func (p ScoreHolderParser) WriteTo(w io.Writer) (int64, error) {
	var flags byte
	if p.Multiple {
		flags |= 1
	}
	return pk.Tuple{pk.VarInt(parserIDScoreHolder), pk.Byte(flags)}.WriteTo(w)
}
func (p ScoreHolderParser) Parse(cmd string) (string, ParsedData, error) { return parseWord(cmd) }

// TimeParser is minecraft:time (id 43): writeInt(min) always. [VERIFIED javap TimeArgument$Info.serializeToNetwork.]
type TimeParser struct {
	Min int32
}

func (p TimeParser) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{pk.VarInt(parserIDTime), pk.Int(p.Min)}.WriteTo(w)
}
func (p TimeParser) Parse(cmd string) (string, ParsedData, error) { return parseWord(cmd) }

// ResourceParser is minecraft:resource (id 46) / resource_key (id 47): props = one Identifier
// (writeResourceKey -> writeIdentifier of the registry key). [VERIFIED javap ResourceArgument$Info /
// ResourceKeyArgument$Info.serializeToNetwork + FriendlyByteBuf.writeResourceKey.]
type ResourceParser struct {
	Registry string
	Key      bool
}

func (p ResourceParser) WriteTo(w io.Writer) (int64, error) {
	id := int32(parserIDResource)
	if p.Key {
		id = parserIDResourceKey
	}
	return pk.Tuple{pk.VarInt(id), pk.Identifier(p.Registry)}.WriteTo(w)
}
func (p ResourceParser) Parse(cmd string) (string, ParsedData, error) { return parseWord(cmd) }

// singletonParser: any SingletonArgumentInfo (contextFree/contextAware) whose serializer writes
// NO properties -- the wire is just the VarInt id. [VERIFIED javap ArgumentTypeInfos.]
type singletonParser struct {
	id     int32
	greedy bool
	tokens int // number of whitespace tokens this parser consumes (0 -> 1; coord types: vec3/block_pos=3, vec2=2)
}

func (p singletonParser) WriteTo(w io.Writer) (int64, error) {
	return pk.VarInt(p.id).WriteTo(w)
}
func (p singletonParser) Parse(cmd string) (string, ParsedData, error) {
	if p.greedy {
		return "", cmd, nil
	}
	return parseTokens(cmd, p.tokens)
}

func GameProfileParser() Parser       { return singletonParser{id: parserIDGameProfile} }
func BlockPosParser() Parser          { return singletonParser{id: parserIDBlockPos, tokens: 3} }
func Vec3Parser() Parser              { return singletonParser{id: parserIDVec3, tokens: 3} }
func Vec2Parser() Parser              { return singletonParser{id: parserIDVec2, tokens: 2} }
func BlockStateParser() Parser        { return singletonParser{id: parserIDBlockState} }
func ItemStackParser() Parser         { return singletonParser{id: parserIDItemStack} }
func TeamColorParser() Parser         { return singletonParser{id: parserIDTeamColor} }
func ComponentParser() Parser         { return singletonParser{id: parserIDComponent, greedy: true} }
func MessageParser() Parser           { return singletonParser{id: parserIDMessage, greedy: true} }
func ObjectiveParser() Parser         { return singletonParser{id: parserIDObjective} }
func ObjectiveCriteriaParser() Parser { return singletonParser{id: parserIDObjectiveCriteria} }
func ScoreboardSlotParser() Parser    { return singletonParser{id: parserIDScoreboardSlot} }
func TeamParser() Parser              { return singletonParser{id: parserIDTeam} }
func ResourceLocationParser() Parser  { return singletonParser{id: parserIDResourceLocation} }
func EntityAnchorParser() Parser      { return singletonParser{id: parserIDEntityAnchor} }
func IntRangeParser() Parser          { return singletonParser{id: parserIDIntRange} }
func FloatRangeParser() Parser        { return singletonParser{id: parserIDFloatRange} }
func DimensionParser() Parser         { return singletonParser{id: parserIDDimension} }
func GameModeParser() Parser          { return singletonParser{id: parserIDGameMode} }
