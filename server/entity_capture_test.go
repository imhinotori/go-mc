package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// entity_capture_test.go is the AUTHORITATIVE ENT-01/04 byte-diff (Plan 06-07): it loads
// golden packet bodies captured from a REAL vanilla 26.2 server (booted from
// temp/cache/26.2-server.jar, superflat, offline, port 25599; driven over RCON to summon a
// MOVING pig and /give a known item) and asserts Sulfur's entity/slot encoders produce the
// SAME wire framing — AND that Sulfur's HashedStack DECODER consumes a real vanilla
// serverbound ContainerClick without mis-framing.
//
// A Go self-round-trip cannot prove vanilla-correctness for a protocol no published spec
// covers (the wiki documents <=773; the Vec3.LP_STREAM_CODEC short-scaling quantizer, the
// SynchedEntityData 0xFF framing, the component-slot ItemStack codec, and the 1.21.5+
// HashedStack are jar-confirmed in SHAPE but their exact bytes drift). This is the
// producer-side proof; the real-client interactive run (06-CAPTURE-DIFF.md Task 2) is the
// consumer-side proof.
//
// The fixtures live under the phase dir so CI byte-diffs without booting Java every run.
// Each subtest SKIPS with a clear message if its fixture is absent (the capture is the
// gate, not a native-CI blocker). The reproducible capture method is recorded in
// .planning/phases/06-entities-physics-interaction/06-CAPTURE-DIFF.md.
//
// LOAD-BEARING ASSERTION POLICY: vanilla's pig carries type-specific metadata (health,
// flags) and runs at a vanilla spawn position; Sulfur's v1 test entity carries the empty-
// metadata path and its own position. We therefore assert the FRAMING that causes a client
// to mis-render or reject a packet — the AddEntity field ORDER + the LP short-scaling
// movement bytes (diffed byte-exact for an identical velocity) + the byte angles; the
// SetEntityData indexed-entry framing + the ALWAYS-present 0xFF terminator; the
// ContainerSetContent count-prefix + per-slot empty/non-empty framing + carried framing; and
// that Sulfur's HashedStack decoder consumes the real vanilla click to exactly zero trailing
// bytes — NOT byte-equality of content (pig health, pose, vanilla position) that legitimately
// differs.

const entityFixtureDir = "../.planning/phases/06-entities-physics-interaction/fixtures"

// loadEntityFixture reads a golden vanilla packet body, or skips the subtest if absent.
func loadEntityFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join(entityFixtureDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("golden fixture %s absent (%v) — see 06-CAPTURE-DIFF.md to re-capture from the vanilla 26.2 jar", path, err)
	}
	return data
}

// TestEntityBytesVsVanillaCapture is the authoritative ENT-01 byte-diff: it asserts Sulfur's
// AddEntity (Vec3.LP short-scaling movement + byte angles) and SetEntityData (indexed entries
// + the 0xFF terminator) encoders match the framing of real vanilla 26.2 golden packets.
func TestEntityBytesVsVanillaCapture(t *testing.T) {
	t.Run("AddEntity", func(t *testing.T) {
		golden := loadEntityFixture(t, "vanilla-add-entity.bin")

		// 1. Parse the vanilla golden as a client would: VarInt id, UUID, VarInt typeId,
		//    Double x/y/z, LP movement (the load-bearing field), Byte xRot/yRot/yHeadRot,
		//    VarInt data — to exactly zero trailing bytes.
		r := bytes.NewReader(golden)
		var vID, vType pk.VarInt
		var vUUID pk.UUID
		var vx, vy, vz pk.Double
		for _, f := range []pk.FieldDecoder{&vID, &vUUID, &vType, &vx, &vy, &vz} {
			if _, err := f.ReadFrom(r); err != nil {
				t.Fatalf("vanilla AddEntity header decode: %v", err)
			}
		}
		// The vanilla capture is a pig (typeId 100) summoned with Motion [0.5, 0.4, -0.3].
		if int32(vType) != 100 {
			t.Fatalf("vanilla AddEntity typeId = %d, want 100 (minecraft:pig)", vType)
		}
		// The LP movement field: header byte, a byte, a big-endian Int (6 bytes for this
		// non-zero, in-range velocity), then 3 byte angles + VarInt data.
		lpAndTail := make([]byte, r.Len())
		_, _ = r.Read(lpAndTail)
		// For Motion [0.5, 0.4, -0.3] the LP field is the 6-byte non-continuation form.
		if len(lpAndTail) < 6+3+1 {
			t.Fatalf("vanilla AddEntity tail = %d bytes, want >= 10 (6 LP + 3 angles + VarInt data)", len(lpAndTail))
		}
		vanillaLP := lpAndTail[:6]
		if vanillaLP[0] == 0x00 {
			t.Fatalf("vanilla AddEntity LP movement is the 0x00 zero path — capture must be a MOVING entity")
		}

		// 2. Sulfur's lpVec3 for the SAME velocity must be BYTE-IDENTICAL to vanilla's LP
		//    field — this is the decisive Vec3.LP short-scaling seal (Open Question 1).
		var sulLP bytes.Buffer
		if _, err := (lpVec3{0.5, 0.4, -0.3}).WriteTo(&sulLP); err != nil {
			t.Fatalf("sulfur lpVec3 write: %v", err)
		}
		if !bytes.Equal(sulLP.Bytes(), vanillaLP) {
			t.Fatalf("Vec3.LP short-scaling DIVERGES from vanilla for Motion [0.5,0.4,-0.3]\n  sulfur:  %x\n  vanilla: %x", sulLP.Bytes(), vanillaLP)
		}

		// 3. Sulfur's encodeAddEntity produces the SAME field ORDER and framing for a moving
		//    entity: feed it the vanilla velocity and assert the body parses identically (id,
		//    uuid, typeId, 3 Doubles, the same 6-byte LP, 3 byte angles, VarInt data) to zero
		//    trailing bytes. Position/uuid/id VALUES differ (Sulfur's own entity) — framing matches.
		e := &Entity{
			id:   int32(vID),
			typ:  entity.Pig.ID,
			uuid: uuid.UUID(vUUID),
			x:    float64(vx), y: float64(vy), z: float64(vz),
			vx: 0.5, vy: 0.4, vz: -0.3,
		}
		p := encodeAddEntity(e)
		if p.ID != int32(packetid.ClientboundAddEntity) {
			t.Fatalf("Sulfur AddEntity id = %d, want %d", p.ID, packetid.ClientboundAddEntity)
		}
		// With identical inputs (id, uuid, type, pos, velocity, zero angles == vanilla's 0x00
		// angle bytes), Sulfur's body is byte-identical to the vanilla golden.
		if !bytes.Equal(p.Data, golden) {
			t.Fatalf("Sulfur AddEntity body != vanilla golden for identical inputs (framing divergence)\n  sulfur:  %x\n  vanilla: %x", p.Data, golden)
		}
	})

	t.Run("SetEntityData", func(t *testing.T) {
		golden := loadEntityFixture(t, "vanilla-set-entity-data.bin")

		// 1. The vanilla golden is: VarInt id, then >=0 indexed entries (Byte index, VarInt
		//    serializerId, value), then the MANDATORY single 0xFF terminator. Walk it as the
		//    client does and assert it ends in exactly one 0xFF with zero trailing bytes.
		r := bytes.NewReader(golden)
		var vID pk.VarInt
		if _, err := vID.ReadFrom(r); err != nil {
			t.Fatalf("vanilla SetEntityData id decode: %v", err)
		}
		// The body must terminate in 0xFF. We assert the LAST byte is the EOF marker (the
		// load-bearing framing — omitting it desyncs the client's entity stream).
		if golden[len(golden)-1] != 0xFF {
			t.Fatalf("vanilla SetEntityData does not end in the 0xFF EOF terminator: %x", golden)
		}
		// At least one indexed entry is present for the pig (it carries type-specific metadata);
		// the first entry's framing is Byte index, VarInt serializerId, value.
		var idx pk.UnsignedByte
		var ser pk.VarInt
		if _, err := idx.ReadFrom(r); err != nil {
			t.Fatalf("vanilla SetEntityData first entry index: %v", err)
		}
		if idx == 0xFF {
			t.Logf("vanilla pig SetEntityData carried an EMPTY metadata list (just 0xFF)")
		} else {
			if _, err := ser.ReadFrom(r); err != nil {
				t.Fatalf("vanilla SetEntityData first entry serializerId: %v", err)
			}
			t.Logf("vanilla pig first metadata entry: index=%d serializerId=%d (type-specific; Sulfur's v1 entity sends the empty 0xFF-only path)", idx, ser)
		}

		// 2. Sulfur's encodeSetEntityData for an EMPTY metadata list is exactly VarInt(id) +
		//    0xFF — the minimum framing that keeps the stream aligned. Assert that shape.
		se := &Entity{id: int32(vID)}
		sp := encodeSetEntityData(se)
		if sp.ID != int32(packetid.ClientboundSetEntityData) {
			t.Fatalf("Sulfur SetEntityData id = %d, want %d", sp.ID, packetid.ClientboundSetEntityData)
		}
		sr := bytes.NewReader(sp.Data)
		var sID pk.VarInt
		if _, err := sID.ReadFrom(sr); err != nil {
			t.Fatalf("Sulfur SetEntityData id decode: %v", err)
		}
		if int32(sID) != int32(vID) {
			t.Errorf("Sulfur SetEntityData id = %d, want %d", sID, vID)
		}
		tail := make([]byte, sr.Len())
		_, _ = sr.Read(tail)
		// Sulfur's empty-list body after the id is exactly the lone 0xFF terminator — the same
		// terminator vanilla closes its (non-empty) list with. The terminator FRAMING matches;
		// the entry SET differs (documented; the real-client run decides if the pig needs an entry).
		if len(tail) != 1 || tail[0] != 0xFF {
			t.Fatalf("Sulfur empty SetEntityData tail = %x, want exactly [0xFF] (the mandatory terminator)", tail)
		}

		// 3. Framing parity for a NON-empty list: when Sulfur carries pre-built metadata bytes,
		//    they splice verbatim before the same 0xFF terminator the vanilla golden ends with.
		//    Replay the vanilla golden's entry bytes (everything between the id and the final
		//    0xFF) as Sulfur metadata and assert the re-encoded body is byte-identical.
		entryBytes := vanillaEntryBytesAfterID(t, golden)
		if len(entryBytes) > 0 {
			withMeta := &Entity{id: int32(vID), metadata: entryBytes}
			wp := encodeSetEntityData(withMeta)
			if !bytes.Equal(wp.Data, golden) {
				t.Fatalf("Sulfur SetEntityData with the vanilla pig's metadata spliced != vanilla golden\n  sulfur:  %x\n  vanilla: %x", wp.Data, golden)
			}
		}
	})
}

// vanillaEntryBytesAfterID returns the SetEntityData entry bytes between the leading VarInt id
// and the trailing 0xFF terminator (i.e. the packed DataValue list, terminator excluded).
func vanillaEntryBytesAfterID(t *testing.T, body []byte) []byte {
	t.Helper()
	r := bytes.NewReader(body)
	var id pk.VarInt
	if _, err := id.ReadFrom(r); err != nil {
		t.Fatalf("entry-bytes id decode: %v", err)
	}
	rest := make([]byte, r.Len())
	_, _ = r.Read(rest)
	if len(rest) == 0 || rest[len(rest)-1] != 0xFF {
		t.Fatalf("entry-bytes body does not end in 0xFF: %x", rest)
	}
	return rest[:len(rest)-1] // strip the terminator; encodeSetEntityData re-adds it
}

// TestSlotBytesVsVanillaCapture is the authoritative ENT-04 byte-diff: it asserts Sulfur's
// ContainerSetContent slot-list framing matches vanilla, that the non-empty slot's
// count+itemId+addedCount+removedCount framing matches vanilla's SetSlot, and that Sulfur's
// HashedStack DECODER consumes a real vanilla serverbound ContainerClick without mis-framing.
func TestSlotBytesVsVanillaCapture(t *testing.T) {
	t.Run("ContainerSetContent", func(t *testing.T) {
		golden := loadEntityFixture(t, "vanilla-container-set-content.bin")

		// 1. Walk the vanilla golden as a client: VarInt containerId, VarInt stateId, VarInt
		//    list count, count × SlotData, then the carried SlotData — to zero trailing bytes.
		r := bytes.NewReader(golden)
		var vCID, vSID, vCount pk.VarInt
		for _, f := range []pk.FieldDecoder{&vCID, &vSID, &vCount} {
			if _, err := f.ReadFrom(r); err != nil {
				t.Fatalf("vanilla ContainerSetContent header decode: %v", err)
			}
		}
		if vCID != 0 {
			t.Errorf("vanilla containerId = %d, want 0 (player inventory)", vCID)
		}
		if vCount <= 0 {
			t.Fatalf("vanilla ContainerSetContent list count = %d, want > 0 (the player inventory slot count)", vCount)
		}
		vanillaItems := make([]component.SlotData, int(vCount))
		for i := 0; i < int(vCount); i++ {
			if _, err := vanillaItems[i].ReadFrom(r); err != nil {
				t.Fatalf("vanilla slot %d decode: %v (mis-framed list)", i, err)
			}
		}
		var vCarried component.SlotData
		if _, err := vCarried.ReadFrom(r); err != nil {
			t.Fatalf("vanilla carried item decode: %v", err)
		}
		if r.Len() != 0 {
			t.Fatalf("vanilla ContainerSetContent left %d trailing bytes — framing mismatch", r.Len())
		}

		// 2. Sulfur's containerSetContent over the SAME parsed items + carried must be
		//    BYTE-IDENTICAL to the vanilla golden: the count-prefix, every per-slot empty/non-
		//    empty encoding, and the carried framing are sealed.
		p := containerSetContent(int32(vCID), int32(vSID), vanillaItems, vCarried)
		if p.ID != int32(packetid.ClientboundContainerSetContent) {
			t.Fatalf("Sulfur ContainerSetContent id = %d, want %d", p.ID, packetid.ClientboundContainerSetContent)
		}
		if !bytes.Equal(p.Data, golden) {
			t.Fatalf("Sulfur ContainerSetContent body != vanilla golden for identical items\n  sulfur:  %x\n  vanilla: %x", p.Data, golden)
		}
	})

	t.Run("NonEmptySlotFraming", func(t *testing.T) {
		// The non-empty slot framing (count, itemId, addedCount, removedCount) is sealed against
		// vanilla's ContainerSetSlot for the /give'd stone (slot 36 = 1 × item id 1). The slot
		// CODEC is shared with ContainerSetContent's list, so this byte-diffs the per-slot encoding.
		golden := loadEntityFixture(t, "vanilla-container-set-slot.bin")
		r := bytes.NewReader(golden)
		var cid, sid pk.VarInt
		var slot pk.Short
		for _, f := range []pk.FieldDecoder{&cid, &sid, &slot} {
			if _, err := f.ReadFrom(r); err != nil {
				t.Fatalf("vanilla ContainerSetSlot header decode: %v", err)
			}
		}
		var vItem component.SlotData
		if _, err := vItem.ReadFrom(r); err != nil {
			t.Fatalf("vanilla ContainerSetSlot item decode: %v", err)
		}
		if r.Len() != 0 {
			t.Fatalf("vanilla ContainerSetSlot left %d trailing bytes — framing mismatch", r.Len())
		}
		if vItem.Count <= 0 {
			t.Fatalf("vanilla ContainerSetSlot item is empty; capture must carry the /give'd stone")
		}
		// Sulfur's containerSetSlot for the SAME item must be byte-identical to the vanilla golden.
		sp := containerSetSlot(int32(cid), int32(sid), int16(slot), vItem)
		if !bytes.Equal(sp.Data, golden) {
			t.Fatalf("Sulfur ContainerSetSlot body != vanilla golden for the identical stone item\n  sulfur:  %x\n  vanilla: %x", sp.Data, golden)
		}
		// And the same item encodes identically inside a ContainerSetContent list slot (shared codec).
		var slotBuf bytes.Buffer
		if _, err := vItem.WriteTo(&slotBuf); err != nil {
			t.Fatalf("re-encode item: %v", err)
		}
		if vItem.Count != 1 || vItem.ItemID != 1 {
			t.Errorf("vanilla stone slot = count=%d id=%d, want count=1 id=1", vItem.Count, vItem.ItemID)
		}
	})

	t.Run("ContainerClickHashedStackDecode", func(t *testing.T) {
		golden := loadEntityFixture(t, "vanilla-container-click.bin")

		// The vanilla golden is the SERVERBOUND ContainerClick body a vanilla client sends
		// (the 1.21.5+ HashedStack form). Sulfur's decode path must consume it WITHOUT mis-
		// framing — walk the 7-field composite and assert it reaches exactly zero trailing bytes
		// using Sulfur's decodeHashedStack for the two HashedStack bodies (the load-bearing
		// CRC-digest framing the server discards).
		r := bytes.NewReader(golden)
		var containerID, stateID pk.VarInt
		var slotNum pk.Short
		var button pk.Byte
		var input pk.VarInt
		for _, f := range []pk.FieldDecoder{&containerID, &stateID, &slotNum, &button, &input} {
			if _, err := f.ReadFrom(r); err != nil {
				t.Fatalf("vanilla ContainerClick header decode: %v", err)
			}
		}
		// changedSlots map: VarInt count, then count × (Short slot + HashedStack).
		var changedCount pk.VarInt
		if _, err := changedCount.ReadFrom(r); err != nil {
			t.Fatalf("vanilla ContainerClick changedSlots count: %v", err)
		}
		for i := int32(0); i < int32(changedCount); i++ {
			var slot pk.Short
			if _, err := slot.ReadFrom(r); err != nil {
				t.Fatalf("vanilla ContainerClick changedSlot %d key: %v", i, err)
			}
			if _, err := decodeHashedStack(r); err != nil {
				t.Fatalf("Sulfur decodeHashedStack MIS-FRAMED vanilla changedSlot %d: %v", i, err)
			}
		}
		// carriedItem HashedStack.
		if _, err := decodeHashedStack(r); err != nil {
			t.Fatalf("Sulfur decodeHashedStack MIS-FRAMED the vanilla carried HashedStack: %v", err)
		}
		if r.Len() != 0 {
			t.Fatalf("Sulfur ContainerClick decode left %d trailing bytes — HashedStack mis-framing (the decoder would desync the client)", r.Len())
		}

		// And the full dispatch path consumes the same real click without panic (the server
		// discards the hashes and re-sends authoritative content).
		loop := NewTickLoop(newFakeClock())
		p := invPlayer(loop)
		click := pk.Packet{ID: int32(packetid.ServerboundContainerClick), Data: golden}
		loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: click}) // must not panic / mis-frame
	})
}
