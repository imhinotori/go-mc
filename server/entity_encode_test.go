package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// entity_encode_test.go covers the ENT-01 jar-derived clientbound entity encoders
// (entity_encode.go): the AddEntity field order + LP-quantized stationary movement, the
// SetEntityData ALWAYS-present 0xFF terminator, and the move/teleport/rotate/remove
// layouts. These are SYMMETRIC self-round-trips against the fork's pk.* codecs — they are
// regression guards for the field order/framing, NOT proof of byte-identity with vanilla.
// Byte-identity (especially the LP quantizer for non-zero velocity and the v1 metadata) is
// sealed by Plan 06-07's capture-diff against a real vanilla 26.2 server.

// TestAddEntityWire asserts encodeAddEntity emits ClientboundAddEntity with the jar-derived
// field order (VarInt id, UUID, VarInt typeId, Double x/y/z, LP movement, Byte
// xRot/yRot/yHeadRot, VarInt data) and that a stationary entity's LP movement is the single
// 0x00 zero-vector byte (06-CAPTURE-DIFF §2/§3).
func TestAddEntityWire(t *testing.T) {
	id := uuid.New()
	e := &Entity{
		id:   77,
		typ:  entity.SulfurCube.ID, // 130
		uuid: id,
		x:    8.5, y: 64.0, z: -12.25,
		yaw: 90, pitch: 0, headYaw: 90,
		// velocity zero: the LP movement must be a single 0x00 byte
	}

	p := encodeAddEntity(e)
	if p.ID != int32(packetid.ClientboundAddEntity) {
		t.Fatalf("AddEntity packet id = %d, want %d", p.ID, packetid.ClientboundAddEntity)
	}

	var (
		gotID                pk.VarInt
		gotUUID              pk.UUID
		gotType              pk.VarInt
		gx, gy, gz           pk.Double
		zeroMove             pk.UnsignedByte // a stationary entity → single 0x00 LP byte
		xRot, yRot, yHeadRot pk.Angle
		data                 pk.VarInt
	)
	if err := p.Scan(&gotID, &gotUUID, &gotType, &gx, &gy, &gz, &zeroMove, &xRot, &yRot, &yHeadRot, &data); err != nil {
		t.Fatalf("AddEntity scan failed: %v", err)
	}

	if int32(gotID) != e.id {
		t.Errorf("id = %d, want %d", gotID, e.id)
	}
	if uuid.UUID(gotUUID) != e.uuid {
		t.Errorf("uuid = %v, want %v", uuid.UUID(gotUUID), e.uuid)
	}
	if int32(gotType) != int32(e.typ) {
		t.Errorf("typeId = %d, want %d (entity type id BEFORE x/y/z)", gotType, e.typ)
	}
	if float64(gx) != e.x || float64(gy) != e.y || float64(gz) != e.z {
		t.Errorf("pos = (%v,%v,%v), want (%v,%v,%v)", gx, gy, gz, e.x, e.y, e.z)
	}
	if zeroMove != 0x00 {
		t.Errorf("stationary LP movement first byte = %#x, want 0x00 (zero-vector fast path)", zeroMove)
	}
	// Byte angles round-trip within ±1 step of the source degrees (1 step = 360/256°).
	if got := xRot.ToDeg(); got < -2 || got > 2 {
		t.Errorf("xRot deg = %v, want ~0", got)
	}
	if got := yRot.ToDeg(); got < 88 || got > 92 {
		t.Errorf("yRot deg = %v, want ~90", got)
	}
	if got := yHeadRot.ToDeg(); got < 88 || got > 92 {
		t.Errorf("yHeadRot deg = %v, want ~90", got)
	}
	if int32(data) != 0 {
		t.Errorf("data = %d, want 0", data)
	}
}

// TestAddEntityLPMovementNonZero asserts the LP quantizer for a NON-zero velocity emits the
// multi-byte quantized form (header+2 bytes + a big-endian Int = at least 6 bytes), NOT the
// single 0x00 fast path — and round-trips back to approximately the source velocity through
// the inverse unpack. Byte-identity with vanilla is sealed by 06-07.
func TestAddEntityLPMovementNonZero(t *testing.T) {
	var buf bytes.Buffer
	if _, err := (lpVec3{0.5, -0.25, 0.1}).WriteTo(&buf); err != nil {
		t.Fatalf("lpVec3 write failed: %v", err)
	}
	if buf.Len() < 6 {
		t.Fatalf("non-zero LP movement encoded in %d bytes, want >= 6 (quantized form, not the 0x00 fast path)", buf.Len())
	}
	if buf.Bytes()[0] == 0x00 {
		t.Fatalf("non-zero LP movement must not use the 0x00 zero-vector path")
	}
}

// TestSetEntityDataWire asserts encodeSetEntityData ALWAYS emits the single 0xFF terminator
// after the entity id: an empty list is exactly VarInt(id) + 0xFF, and a one-entry list is
// Byte index, VarInt serializerId, value, then 0xFF (06-CAPTURE-DIFF §4). The terminator is
// the load-bearing framing — omitting it desyncs the client.
func TestSetEntityDataWire(t *testing.T) {
	e := &Entity{id: 55}

	// Empty list: body is VarInt(id) then the lone 0xFF terminator.
	p := encodeSetEntityData(e)
	if p.ID != int32(packetid.ClientboundSetEntityData) {
		t.Fatalf("SetEntityData id = %d, want %d", p.ID, packetid.ClientboundSetEntityData)
	}
	var gotID pk.VarInt
	rest := bytes.NewReader(p.Data)
	if _, err := gotID.ReadFrom(rest); err != nil {
		t.Fatalf("scan id failed: %v", err)
	}
	if int32(gotID) != e.id {
		t.Errorf("id = %d, want %d", gotID, e.id)
	}
	remaining := make([]byte, rest.Len())
	_, _ = rest.Read(remaining)
	if len(remaining) != 1 || remaining[0] != 0xFF {
		t.Fatalf("empty SetEntityData trailing bytes = %v, want exactly [0xFF]", remaining)
	}

	// One entry: Byte index, VarInt serializerId, value, then 0xFF.
	p2 := encodeSetEntityData(e, entityDataEntry{index: 0, serializerID: 0, value: pk.Byte(0)})
	r2 := bytes.NewReader(p2.Data)
	var id2 pk.VarInt
	if _, err := id2.ReadFrom(r2); err != nil {
		t.Fatalf("scan id2 failed: %v", err)
	}
	var index pk.UnsignedByte
	var serID pk.VarInt
	var val pk.Byte
	if _, err := index.ReadFrom(r2); err != nil {
		t.Fatalf("scan entry index failed: %v", err)
	}
	if _, err := serID.ReadFrom(r2); err != nil {
		t.Fatalf("scan serializerId failed: %v", err)
	}
	if _, err := val.ReadFrom(r2); err != nil {
		t.Fatalf("scan entry value failed: %v", err)
	}
	if index != 0 {
		t.Errorf("entry index = %d, want 0", index)
	}
	tail := make([]byte, r2.Len())
	_, _ = r2.Read(tail)
	if len(tail) != 1 || tail[0] != 0xFF {
		t.Fatalf("one-entry SetEntityData trailing bytes = %v, want exactly [0xFF]", tail)
	}
}

// TestMoveAndRemoveWire asserts the move/teleport/rotate/remove encoders match the jar
// layout (06-CAPTURE-DIFF §5): TeleportEntity round-trips absolute position+rotation;
// RotateHead is VarInt id + byte angle; RemoveEntities is a VarInt-count-prefixed id list.
func TestMoveAndRemoveWire(t *testing.T) {
	e := &Entity{id: 9, x: 100.5, y: 70, z: -3.5, yaw: 45, pitch: -10, onGround: true}

	// TeleportEntity: VarInt id, Double x/y/z, Double dx/dy/dz, Float yaw/pitch, Int flags,
	// Boolean onGround.
	tp := encodeTeleportEntity(e)
	if tp.ID != int32(packetid.ClientboundTeleportEntity) {
		t.Fatalf("TeleportEntity id = %d, want %d", tp.ID, packetid.ClientboundTeleportEntity)
	}
	var (
		tid        pk.VarInt
		tx, ty, tz pk.Double
		dx, dy, dz pk.Double
		yaw, pitch pk.Float
		flags      pk.Int
		onGround   pk.Boolean
	)
	if err := tp.Scan(&tid, &tx, &ty, &tz, &dx, &dy, &dz, &yaw, &pitch, &flags, &onGround); err != nil {
		t.Fatalf("TeleportEntity scan failed: %v", err)
	}
	if int32(tid) != e.id || float64(tx) != e.x || float64(ty) != e.y || float64(tz) != e.z {
		t.Errorf("teleport id/pos = (%d,%v,%v,%v), want (%d,%v,%v,%v)", tid, tx, ty, tz, e.id, e.x, e.y, e.z)
	}
	if float32(yaw) != e.yaw || float32(pitch) != e.pitch {
		t.Errorf("teleport rot = (%v,%v), want (%v,%v)", yaw, pitch, e.yaw, e.pitch)
	}
	if int32(flags) != 0 {
		t.Errorf("teleport relative flags = %d, want 0 (absolute)", flags)
	}
	if !bool(onGround) {
		t.Errorf("teleport onGround = false, want true")
	}

	// RotateHead: VarInt id, Byte headYaw.
	rh := encodeRotateHead(e.id, 90)
	if rh.ID != int32(packetid.ClientboundRotateHead) {
		t.Fatalf("RotateHead id = %d, want %d", rh.ID, packetid.ClientboundRotateHead)
	}
	var rhID pk.VarInt
	var headYaw pk.Angle
	if err := rh.Scan(&rhID, &headYaw); err != nil {
		t.Fatalf("RotateHead scan failed: %v", err)
	}
	if int32(rhID) != e.id {
		t.Errorf("RotateHead id = %d, want %d", rhID, e.id)
	}
	if got := headYaw.ToDeg(); got < 88 || got > 92 {
		t.Errorf("RotateHead headYaw deg = %v, want ~90", got)
	}

	// RemoveEntities: VarInt count, then N VarInt ids.
	rm := encodeRemoveEntities([]int32{3, 9, 27})
	if rm.ID != int32(packetid.ClientboundRemoveEntities) {
		t.Fatalf("RemoveEntities id = %d, want %d", rm.ID, packetid.ClientboundRemoveEntities)
	}
	var count pk.VarInt
	var a, b, c pk.VarInt
	if err := rm.Scan(&count, &a, &b, &c); err != nil {
		t.Fatalf("RemoveEntities scan failed: %v", err)
	}
	if int32(count) != 3 {
		t.Errorf("RemoveEntities count = %d, want 3", count)
	}
	if int32(a) != 3 || int32(b) != 9 || int32(c) != 27 {
		t.Errorf("RemoveEntities ids = (%d,%d,%d), want (3,9,27)", a, b, c)
	}
}

// TestMoveDeltaShort asserts the VecDeltaCodec 4096-scaled short delta (06-CAPTURE-DIFF §5):
// a +1.0 block move encodes to 4096, and the Pos encoder carries the shorts in order.
func TestMoveDeltaShort(t *testing.T) {
	if got := moveDeltaShort(0, 1.0); got != 4096 {
		t.Fatalf("moveDeltaShort(0,1.0) = %d, want 4096 (round(1.0*4096))", got)
	}
	if got := moveDeltaShort(10.0, 10.0); got != 0 {
		t.Fatalf("moveDeltaShort(10,10) = %d, want 0 (no movement)", got)
	}
	p := encodeMoveEntityPos(5, 4096, -4096, 0, true)
	if p.ID != int32(packetid.ClientboundMoveEntityPos) {
		t.Fatalf("MoveEntityPos id = %d, want %d", p.ID, packetid.ClientboundMoveEntityPos)
	}
	var id pk.VarInt
	var xa, ya, za pk.Short
	var onGround pk.Boolean
	if err := p.Scan(&id, &xa, &ya, &za, &onGround); err != nil {
		t.Fatalf("MoveEntityPos scan failed: %v", err)
	}
	if int32(id) != 5 || xa != 4096 || ya != -4096 || za != 0 || !bool(onGround) {
		t.Fatalf("MoveEntityPos = (id=%d,xa=%d,ya=%d,za=%d,og=%v), want (5,4096,-4096,0,true)", id, xa, ya, za, onGround)
	}
}
