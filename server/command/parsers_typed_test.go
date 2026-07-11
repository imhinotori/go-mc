package command

import (
	"bytes"
	"io"
	"testing"
)

// wireParser is any parser that also serializes itself onto the command graph (pk.FieldEncoder).
type wireParser interface {
	Parser
	WriteTo(w io.Writer) (int64, error)
}

// parsers_typed_test.go -- wire-shape lock tests for the typed brigadier parsers. Each asserts the
// exact bytes WriteTo emits (VarInt parser id + the properties the jar's *ArgumentInfo.serialize-
// ToNetwork writes). Byte-frozen against temp/cache/26.2-inner.jar; a codec drift breaks the test.

func wire(t *testing.T, p wireParser) []byte {
	t.Helper()
	var b bytes.Buffer
	if _, err := p.WriteTo(&b); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	return b.Bytes()
}

func eq(t *testing.T, got, want []byte, name string) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("%s wire = %v, want %v", name, got, want)
	}
}

func TestParserWireBool(t *testing.T) {
	// brigadier:bool id 0, no props.
	eq(t, wire(t, BoolParser{}), []byte{0}, "bool")
}

func TestParserWireInteger(t *testing.T) {
	// brigadier:integer id 3. Unbounded -> flags 0x00, no min/max.
	eq(t, wire(t, IntegerParser{}), []byte{3, 0}, "integer unbounded")
	// min only (min=1) -> flags 0x01 + writeInt(1) big-endian.
	eq(t, wire(t, IntegerParser{Min: 1, HasMin: true}), []byte{3, 1, 0, 0, 0, 1}, "integer min=1")
	// min+max (1..255) -> flags 0x03 + writeInt(1) + writeInt(255).
	eq(t, wire(t, IntegerParser{Min: 1, Max: 255, HasMin: true, HasMax: true}),
		[]byte{3, 3, 0, 0, 0, 1, 0, 0, 0, 255}, "integer 1..255")
}

func TestParserWireFloatDoubleLong(t *testing.T) {
	eq(t, wire(t, FloatParser{}), []byte{1, 0}, "float unbounded")
	eq(t, wire(t, DoubleParser{}), []byte{2, 0}, "double unbounded")
	eq(t, wire(t, LongParser{}), []byte{4, 0}, "long unbounded")
}

func TestParserWireEntity(t *testing.T) {
	// minecraft:entity id 6 + flags byte: single=1, playersOnly=2.
	eq(t, wire(t, EntityParser{}), []byte{6, 0}, "entity any")
	eq(t, wire(t, EntityParser{Single: true}), []byte{6, 1}, "entity single")
	eq(t, wire(t, EntityParser{Single: true, PlayersOnly: true}), []byte{6, 3}, "entity single+players")
}

func TestParserWireScoreHolder(t *testing.T) {
	// minecraft:score_holder id 31 + flags: multiple=1.
	eq(t, wire(t, ScoreHolderParser{}), []byte{31, 0}, "score_holder single")
	eq(t, wire(t, ScoreHolderParser{Multiple: true}), []byte{31, 1}, "score_holder multiple")
}

func TestParserWireTime(t *testing.T) {
	// minecraft:time id 43 + writeInt(min) always. min=0 -> 4 zero bytes.
	eq(t, wire(t, TimeParser{Min: 0}), []byte{43, 0, 0, 0, 0}, "time min=0")
}

func TestParserWireResource(t *testing.T) {
	// minecraft:resource id 46 + Identifier "a:b" (String: VarInt len 3 + bytes).
	got := wire(t, ResourceParser{Registry: "a:b"})
	want := []byte{46, 3, 'a', ':', 'b'}
	eq(t, got, want, "resource a:b")
	// resource_key id 47.
	got = wire(t, ResourceParser{Registry: "a:b", Key: true})
	want = []byte{47, 3, 'a', ':', 'b'}
	eq(t, got, want, "resource_key a:b")
}

func TestParserWireSingletons(t *testing.T) {
	cases := []struct {
		p  wireParser
		id byte
		nm string
	}{
		{GameProfileParser().(wireParser), 7, "game_profile"},
		{BlockPosParser().(wireParser), 8, "block_pos"},
		{Vec3Parser().(wireParser), 10, "vec3"},
		{Vec2Parser().(wireParser), 11, "vec2"},
		{BlockStateParser().(wireParser), 12, "block_state"},
		{ItemStackParser().(wireParser), 14, "item_stack"},
		{TeamColorParser().(wireParser), 16, "team_color"},
		{ComponentParser().(wireParser), 18, "component"},
		{MessageParser().(wireParser), 20, "message"},
		{ObjectiveParser().(wireParser), 24, "objective"},
		{ObjectiveCriteriaParser().(wireParser), 25, "objective_criteria"},
		{ScoreboardSlotParser().(wireParser), 30, "scoreboard_slot"},
		{TeamParser().(wireParser), 33, "team"},
		{ResourceLocationParser().(wireParser), 36, "resource_location"},
		{EntityAnchorParser().(wireParser), 38, "entity_anchor"},
		{IntRangeParser().(wireParser), 39, "int_range"},
		{FloatRangeParser().(wireParser), 40, "float_range"},
		{DimensionParser().(wireParser), 41, "dimension"},
		{GameModeParser().(wireParser), 42, "gamemode"},
	}
	for _, c := range cases {
		eq(t, wire(t, c.p), []byte{c.id}, c.nm)
	}
}

// TestParserParseWord asserts a typed leaf consumes exactly one token and hands the remainder on.
func TestParserParseWord(t *testing.T) {
	left, v, err := GameModeParser().Parse("creative Steve")
	if err != nil {
		t.Fatal(err)
	}
	if v.(string) != "creative" || left != "Steve" {
		t.Fatalf("gamemode parse = (%q, %q), want (creative, Steve)", v, left)
	}
	// vec3 consumes three tokens joined by a single space.
	left, v, err = Vec3Parser().Parse("1 2 3 stone")
	if err != nil {
		t.Fatal(err)
	}
	if v.(string) != "1 2 3" || left != "stone" {
		t.Fatalf("vec3 parse = (%q, %q), want (1 2 3, stone)", v, left)
	}
	// message consumes the whole remainder (greedy).
	left, v, _ = MessageParser().Parse("hello there world")
	if v.(string) != "hello there world" || left != "" {
		t.Fatalf("message parse = (%q, %q), want greedy", v, left)
	}
}
