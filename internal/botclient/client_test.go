package botclient

import (
	"testing"

	"github.com/google/uuid"
	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// newTestClient returns an initialized Client suitable for exercising the read-loop recorders
// directly (no live connection). The recorders mutate the mu-guarded state exactly as they do
// when driven off the wire.
func newTestClient() *Client {
	c := New()
	c.connected.Store(true) // so the recorders' ensureInit path matches the live case
	return c
}

// marshalToPacket marshals fields the way the server's encoders do, then re-parses the wire
// bytes back into a pk.Packet (ID + Data) so the recorders see EXACTLY the on-wire framing.
func marshalToPacket(t *testing.T, id int32, fields ...pk.FieldEncoder) pk.Packet {
	t.Helper()
	return pk.Marshal(id, fields...)
}

func TestRecordAddEntityPopulatesTable(t *testing.T) {
	c := newTestClient()
	// encodeAddEntity wire: VarInt id, UUID, VarInt typeId, Double x,y,z, ... (we only need the
	// leading fields the recorder reads; trailing fields are tolerated by the reader stopping early
	// once it has x/y/z, but we include a plausible tail to mirror the real packet length).
	id := int32(42)
	typ := int32(100) // minecraft:pig wire type
	u := uuid.New()
	p := marshalToPacket(t, int32(packetid.ClientboundAddEntity),
		pk.VarInt(id), pk.UUID(u), pk.VarInt(typ),
		pk.Double(1.5), pk.Double(64), pk.Double(-3.25),
		pk.Short(0), pk.Short(0), pk.Short(0), // velocity-ish trailing bytes
	)
	c.recordAddEntity(p)

	ents := c.Entities()
	if len(ents) != 1 {
		t.Fatalf("entity count = %d, want 1", len(ents))
	}
	e := ents[0]
	if e.ID != id || e.TypeID != typ {
		t.Fatalf("got id=%d type=%d, want id=%d type=%d", e.ID, e.TypeID, id, typ)
	}
	if e.X != 1.5 || e.Y != 64 || e.Z != -3.25 {
		t.Fatalf("got pos (%v,%v,%v), want (1.5,64,-3.25)", e.X, e.Y, e.Z)
	}
	if e.MoveCount != 0 {
		t.Fatalf("fresh entity move_count = %d, want 0", e.MoveCount)
	}
}

func TestRecordMoveBumpsCountAndPosition(t *testing.T) {
	c := newTestClient()
	id := int32(7)
	// Spawn it first.
	c.recordAddEntity(marshalToPacket(t, int32(packetid.ClientboundAddEntity),
		pk.VarInt(id), pk.UUID(uuid.New()), pk.VarInt(100),
		pk.Double(0), pk.Double(64), pk.Double(0),
		pk.Short(0), pk.Short(0), pk.Short(0),
	))

	// MoveEntityPos delta wire: VarInt id, Short xa,ya,za (4096-scaled), Boolean onGround.
	// 4096 ticks = +1.0 block in X.
	c.recordMovePos(marshalToPacket(t, int32(packetid.ClientboundMoveEntityPos),
		pk.VarInt(id), pk.Short(4096), pk.Short(0), pk.Short(0), pk.Boolean(true),
	))
	c.recordMovePos(marshalToPacket(t, int32(packetid.ClientboundMoveEntityPos),
		pk.VarInt(id), pk.Short(0), pk.Short(0), pk.Short(2048), pk.Boolean(true),
	))

	e := findEntity(t, c, id)
	if e.MoveCount != 2 {
		t.Fatalf("move_count = %d, want 2", e.MoveCount)
	}
	if e.X != 1.0 {
		t.Fatalf("X after +1 delta = %v, want 1.0", e.X)
	}
	if e.Z != 0.5 {
		t.Fatalf("Z after +0.5 delta = %v, want 0.5", e.Z)
	}
}

func TestRecordAbsoluteMoveSetsPosition(t *testing.T) {
	c := newTestClient()
	id := int32(9)
	// TeleportEntity wire: VarInt id, Double x,y,z, Double dx,dy,dz, Float yaw,pitch, Int flags, Bool og.
	c.recordAbsoluteMove(marshalToPacket(t, int32(packetid.ClientboundTeleportEntity),
		pk.VarInt(id),
		pk.Double(100), pk.Double(70), pk.Double(-50),
		pk.Double(0), pk.Double(0), pk.Double(0),
		pk.Float(0), pk.Float(0), pk.Int(0), pk.Boolean(true),
	))
	e := findEntity(t, c, id)
	if e.X != 100 || e.Y != 70 || e.Z != -50 {
		t.Fatalf("absolute pos = (%v,%v,%v), want (100,70,-50)", e.X, e.Y, e.Z)
	}
	if e.MoveCount != 1 {
		t.Fatalf("move_count = %d, want 1", e.MoveCount)
	}
}

func TestRecordHeadRotFlags(t *testing.T) {
	c := newTestClient()
	id := int32(11)
	// RotateHead wire: VarInt id, Byte headYaw (Angle).
	c.recordHeadRot(marshalToPacket(t, int32(packetid.ClientboundRotateHead),
		pk.VarInt(id), pk.Angle(5),
	))
	e := findEntity(t, c, id)
	if !e.SawHeadRot {
		t.Fatalf("saw_head_rot = false, want true")
	}
}

func TestRecordRemoveEntitiesDeletes(t *testing.T) {
	c := newTestClient()
	for _, id := range []int32{1, 2, 3} {
		c.recordAddEntity(marshalToPacket(t, int32(packetid.ClientboundAddEntity),
			pk.VarInt(id), pk.UUID(uuid.New()), pk.VarInt(100),
			pk.Double(0), pk.Double(64), pk.Double(0),
			pk.Short(0), pk.Short(0), pk.Short(0),
		))
	}
	// RemoveEntities wire: VarInt count, count×VarInt id.
	c.recordRemoveEntities(marshalToPacket(t, int32(packetid.ClientboundRemoveEntities),
		pk.VarInt(2), pk.VarInt(1), pk.VarInt(3),
	))
	ents := c.Entities()
	if len(ents) != 1 || ents[0].ID != 2 {
		t.Fatalf("after remove, entities = %+v, want only id=2", ents)
	}
}

func TestRecordSystemChatRing(t *testing.T) {
	c := newTestClient()
	// SystemChat wire: chat.Message content, Boolean overlay.
	for _, txt := range []string{"hello", "[dbg] spawned pig eid=42 at (1.0,64.0,2.0)", "world"} {
		c.recordSystemChat(marshalToPacket(t, int32(packetid.ClientboundSystemChat),
			chat.Message{Text: txt}, pk.Boolean(false),
		))
	}
	got := c.RecentChat(2)
	if len(got) != 2 {
		t.Fatalf("RecentChat(2) len = %d, want 2", len(got))
	}
	if got[0] != "[dbg] spawned pig eid=42 at (1.0,64.0,2.0)" || got[1] != "world" {
		t.Fatalf("RecentChat(2) = %v, want the last two", got)
	}
	all := c.RecentChat(0)
	if len(all) != 3 {
		t.Fatalf("RecentChat(0) len = %d, want 3 (whole ring)", len(all))
	}
}

func TestRecentChatRingBounded(t *testing.T) {
	c := newTestClient()
	for i := 0; i < defaultRecentChat+20; i++ {
		c.recordSystemChat(marshalToPacket(t, int32(packetid.ClientboundSystemChat),
			chat.Message{Text: "line"}, pk.Boolean(false),
		))
	}
	if got := len(c.RecentChat(0)); got != defaultRecentChat {
		t.Fatalf("ring length = %d, want capped at %d", got, defaultRecentChat)
	}
}

func TestStepToward(t *testing.T) {
	cases := []struct{ cur, target, step, want float64 }{
		{0, 1, 0.2, 0.2},
		{0.9, 1, 0.2, 1.0}, // would overshoot -> clamp to target
		{1, 0, 0.2, 0.8},
		{5, 5, 0.2, 5}, // already there
	}
	for _, tc := range cases {
		if got := stepToward(tc.cur, tc.target, tc.step); got != tc.want {
			t.Errorf("stepToward(%v,%v,%v) = %v, want %v", tc.cur, tc.target, tc.step, got, tc.want)
		}
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort uint16
	}{
		{"127.0.0.1:25565", "127.0.0.1", 25565},
		{"localhost:30000", "localhost", 30000},
		{"example.com", "example.com", 25565}, // no port -> default
		{":25565", "localhost", 25565},        // empty host -> localhost
	}
	for _, tc := range cases {
		h, p := splitHostPort(tc.in)
		if h != tc.wantHost || p != tc.wantPort {
			t.Errorf("splitHostPort(%q) = (%q,%d), want (%q,%d)", tc.in, h, p, tc.wantHost, tc.wantPort)
		}
	}
}

// findEntity returns the table entry for id or fails the test.
func findEntity(t *testing.T, c *Client, id int32) EntitySnapshot {
	t.Helper()
	for _, e := range c.Entities() {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("entity id=%d not found in table", id)
	return EntitySnapshot{}
}
