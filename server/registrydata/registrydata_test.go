package registrydata

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/nbt/dynbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/registry"
)

// bufConn is a minimal sink/source that records the packets WriteRegistryData /
// WriteTags emit, so the NET-04 payload can be round-tripped through the fork's
// own bot/ client decoder without a real socket. It mirrors the wire framing the
// live *net.Conn uses at threshold -1 (no compression).
type bufConn struct {
	packets []pk.Packet
}

func (b *bufConn) WritePacket(p pk.Packet) error {
	// Round-trip the packet through Pack/UnPack at threshold -1 so the test
	// exercises the real wire bytes, not just the in-memory Packet struct.
	var buf bytes.Buffer
	if err := p.Pack(&buf, -1); err != nil {
		return err
	}
	var decoded pk.Packet
	if err := decoded.UnPack(&buf, -1); err != nil {
		return err
	}
	b.packets = append(b.packets, decoded)
	return nil
}

// TestRegistryDataRoundTrip feeds the packets WriteRegistryData emits into the
// fork's bot/ client registry decoder (registry.NewNetworkCodec) and asserts the
// real 26.2 entries survive the network-NBT round-trip — proving wire-shape
// compatibility against the fork's own decoder. minecraft:overworld (a typed
// Dimension decode) and minecraft:plains (a RawMessage decode) must both appear.
func TestRegistryDataRoundTrip(t *testing.T) {
	conn := &bufConn{}
	if err := WriteRegistryData(conn); err != nil {
		t.Fatalf("WriteRegistryData: %v", err)
	}
	if len(conn.packets) == 0 {
		t.Fatal("WriteRegistryData emitted no packets")
	}

	codec := registry.NewNetworkCodec()
	seen := map[string]bool{}
	for _, p := range conn.packets {
		if p.ID != int32(packetid.ClientboundConfigRegistryData) {
			t.Fatalf("unexpected packet id %d, want ClientboundConfigRegistryData", p.ID)
		}
		r := bytes.NewReader(p.Data)
		var registryID pk.Identifier
		if _, err := registryID.ReadFrom(r); err != nil {
			t.Fatalf("read registry id: %v", err)
		}
		seen[string(registryID)] = true
		reg := codec.Registry(string(registryID))
		if _, err := reg.ReadFrom(r); err != nil {
			t.Fatalf("decode registry %s with fork bot/ decoder: %v", registryID, err)
		}
	}

	for _, want := range []string{
		"minecraft:chat_type",
		"minecraft:damage_type",
		"minecraft:dimension_type",
		"minecraft:worldgen/biome",
	} {
		if !seen[want] {
			t.Errorf("registry %s was not emitted", want)
		}
	}

	// The fork decode succeeded; assert the specific authoritative entries are
	// present in the populated codec.
	if _, ow := codec.DimensionType.Get("minecraft:overworld"); ow == nil {
		t.Error("dimension_type minecraft:overworld missing after round-trip")
	}
	if _, pl := codec.WorldGenBiome.Get("minecraft:plains"); pl == nil {
		t.Error("worldgen/biome minecraft:plains missing after round-trip")
	}
}

// TestRegistryNBTTagTypes is the TYPE-FAITHFULNESS guard (Wave-2 gate for the
// float64->TagDouble trap). It marshals the dimension_type overworld entry to
// network NBT and re-decodes it as a concrete dynbt tag tree (NOT nbt.RawMessage,
// which accepts any shape and would hide the bug), then asserts:
//   - integer fields (min_y, monster_spawn_block_light_limit) decode as TagInt
//   - byte fields (has_skylight, has_ceiling — Minecraft booleans) decode as TagByte
//   - NONE of them is TagDouble
//
// NOTE on field choice: the plan suggested piglin_safe/has_raids as the byte
// field, but in the real 26.2 dimension_type schema those moved into the nested
// attributes map and are no longer top-level. The top-level boolean fields in
// 26.2 are has_skylight/has_ceiling/has_ender_dragon_fight, which are the
// authoritative TagByte fields asserted here.
func TestRegistryNBTTagTypes(t *testing.T) {
	regs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var overworld *dynbt.Value
	for _, r := range regs {
		if r.ID != "minecraft:dimension_type" {
			continue
		}
		for _, e := range r.Entries {
			if e.Key == "minecraft:overworld" {
				overworld = e.NBT
			}
		}
	}
	if overworld == nil {
		t.Fatal("dimension_type minecraft:overworld not loaded")
	}

	// Marshal to network NBT, then decode back into a generic dynbt tree.
	var buf bytes.Buffer
	if _, err := (pk.NBTField{V: overworld}).WriteTo(&buf); err != nil {
		t.Fatalf("marshal overworld to network NBT: %v", err)
	}
	var decoded dynbt.Value
	if _, err := (pk.NBTField{V: &decoded, AllowUnknownFields: true}).ReadFrom(&buf); err != nil {
		t.Fatalf("decode overworld network NBT into dynbt tree: %v", err)
	}

	tagName := map[byte]string{
		nbt.TagByte: "TagByte", nbt.TagShort: "TagShort", nbt.TagInt: "TagInt",
		nbt.TagLong: "TagLong", nbt.TagFloat: "TagFloat", nbt.TagDouble: "TagDouble",
		nbt.TagString: "TagString", nbt.TagCompound: "TagCompound", nbt.TagList: "TagList",
	}

	assertTag := func(field string, want byte) {
		v := decoded.Get(field)
		if v == nil {
			t.Fatalf("field %q missing after decode", field)
		}
		got := v.TagType()
		if got == nbt.TagDouble && want != nbt.TagDouble {
			t.Errorf("field %q decoded as TagDouble — the float64->TagDouble typing trap is NOT defused", field)
		}
		if got != want {
			t.Errorf("field %q: got %s, want %s", field, tagName[got], tagName[want])
		}
	}

	// Integer fields MUST be TagInt (never TagDouble).
	assertTag("min_y", nbt.TagInt)
	assertTag("monster_spawn_block_light_limit", nbt.TagInt)
	assertTag("height", nbt.TagInt)
	// Byte (boolean) fields MUST be TagByte (never TagDouble).
	assertTag("has_skylight", nbt.TagByte)
	assertTag("has_ceiling", nbt.TagByte)
	// Double fields stay TagDouble (sanity: the heuristic does not over-correct).
	assertTag("ambient_light", nbt.TagDouble)
	assertTag("coordinate_scale", nbt.TagDouble)
	// Nested 26.2 schema must survive as a compound, not be flattened/dropped.
	assertTag("attributes", nbt.TagCompound)
	assertTag("default_clock", nbt.TagString)
}

// TestRegistryDataFrame asserts each emitted RegistryData packet body is exactly
// Identifier registryId + VarInt count + per-entry (Identifier key, Boolean
// hasData, network-NBT) — the correct 776 frame.
func TestRegistryDataFrame(t *testing.T) {
	conn := &bufConn{}
	if err := WriteRegistryData(conn); err != nil {
		t.Fatalf("WriteRegistryData: %v", err)
	}

	regs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(conn.packets) != len(regs) {
		t.Fatalf("emitted %d packets, want one per registry (%d)", len(conn.packets), len(regs))
	}

	for i, p := range conn.packets {
		if p.ID != int32(packetid.ClientboundConfigRegistryData) {
			t.Fatalf("packet %d id %d, want ClientboundConfigRegistryData", i, p.ID)
		}
		r := bytes.NewReader(p.Data)

		var registryID pk.Identifier
		if _, err := registryID.ReadFrom(r); err != nil {
			t.Fatalf("packet %d: read registry id: %v", i, err)
		}
		if string(registryID) != regs[i].ID {
			t.Errorf("packet %d registry id = %q, want %q (send order)", i, registryID, regs[i].ID)
		}

		var count pk.VarInt
		if _, err := count.ReadFrom(r); err != nil {
			t.Fatalf("packet %d: read count: %v", i, err)
		}
		if int(count) != len(regs[i].Entries) {
			t.Errorf("packet %d (%s) count = %d, want %d", i, registryID, count, len(regs[i].Entries))
		}

		for j := 0; j < int(count); j++ {
			var key pk.Identifier
			var hasData pk.Boolean
			if _, err := key.ReadFrom(r); err != nil {
				t.Fatalf("packet %d entry %d: read key: %v", i, j, err)
			}
			if _, err := hasData.ReadFrom(r); err != nil {
				t.Fatalf("packet %d entry %d: read hasData: %v", i, j, err)
			}
			if !bool(hasData) {
				t.Errorf("packet %d entry %d (%s): hasData=false, want true", i, j, key)
			}
			// network-NBT body
			var body dynbt.Value
			if _, err := (pk.NBTField{V: &body, AllowUnknownFields: true}).ReadFrom(r); err != nil {
				t.Fatalf("packet %d entry %d (%s): decode network-NBT: %v", i, j, key, err)
			}
			if body.TagType() != nbt.TagCompound {
				t.Errorf("packet %d entry %d (%s): root tag = %d, want TagCompound", i, j, key, body.TagType())
			}
		}
	}
}

// TestUpdateTagsPresent asserts WriteTags emits a present (empty-but-valid)
// ClientboundConfigUpdateTags the fork's bot/ client decodes without error.
// The minimal valid body is VarInt(0): present but with no per-registry tag sets.
func TestUpdateTagsPresent(t *testing.T) {
	conn := &bufConn{}
	if err := WriteTags(conn); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}
	if len(conn.packets) != 1 {
		t.Fatalf("WriteTags emitted %d packets, want 1", len(conn.packets))
	}
	p := conn.packets[0]
	if p.ID != int32(packetid.ClientboundConfigUpdateTags) {
		t.Fatalf("packet id %d, want ClientboundConfigUpdateTags", p.ID)
	}

	// Mirror the bot/ client decode: VarInt length, then `length` per-registry
	// tag sets. An empty-but-present body is length 0.
	r := bytes.NewReader(p.Data)
	var length pk.VarInt
	if _, err := length.ReadFrom(r); err != nil {
		t.Fatalf("read update-tags length: %v", err)
	}
	if length < 0 {
		t.Errorf("update-tags length = %d, want >= 0 (present body)", length)
	}
	// Decode each registry tag set with the fork's ReadTagsFrom to prove the
	// body is wire-valid for the client.
	codec := registry.NewNetworkCodec()
	for i := 0; i < int(length); i++ {
		var registryID pk.Identifier
		if _, err := registryID.ReadFrom(r); err != nil {
			t.Fatalf("update-tags entry %d: read registry id: %v", i, err)
		}
		reg := codec.Registry(string(registryID))
		if _, err := reg.ReadTagsFrom(r); err != nil {
			t.Fatalf("update-tags entry %d (%s): ReadTagsFrom: %v", i, registryID, err)
		}
	}
	if r.Len() != 0 {
		t.Errorf("update-tags body has %d trailing bytes, want fully consumed", r.Len())
	}
}
