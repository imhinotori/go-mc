package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"

	"github.com/google/uuid"
)

// play_join_capture_test.go is the AUTHORITATIVE PLAY-01/02/05 byte-diff (Plan 05-03):
// it loads golden packet bodies captured from a REAL vanilla 26.2 server (booted from
// temp/cache/26.2-server.jar, superflat, offline) and asserts Sulfur's encoders produce
// the SAME wire framing. A Go self-round-trip cannot prove vanilla-correctness for a
// protocol no published spec covers (the wiki documents <=773; the PlayerInfoUpdate entry
// sub-encoding and the 26.x RespawnData/GlobalPos restructure are jar-confirmed in SHAPE
// but their exact bytes drift between versions). This is the producer-side proof; the
// real-client walk-around (05-CAPTURE-DIFF.md Task 2) is the consumer-side proof.
//
// The fixtures live under the phase dir so CI byte-diffs without booting Java every run.
// Each subtest SKIPS with a clear message if its fixture is absent (the capture is the
// gate, not a native-CI blocker). The reproducible capture method is recorded in
// .planning/phases/05-player-session-in-world-first-playable/05-CAPTURE-DIFF.md.
//
// LOAD-BEARING ASSERTION POLICY: vanilla's join PlayerInfoUpdate carries ALL 8 actions
// (mask 0xff) and runs in creative; Sulfur's minimal self-entry carries 3 (mask 0x0d) in
// survival. We therefore assert the FRAMING that causes a client to reject a packet — the
// 1-byte action mask width, the VarInt count-prefix, the per-action body order/encoding for
// the actions Sulfur DOES send, the RespawnData GlobalPos field order, and the jar-derived
// ForgetLevelChunk packed-Long layout — NOT byte-equality of action-set/content that
// legitimately differs (game mode, surface Y, the extra vanilla actions).

const captureFixtureDir = "../.planning/phases/05-player-session-in-world-first-playable/fixtures"

// loadFixture reads a golden vanilla packet body, or skips the subtest if absent.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join(captureFixtureDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("golden fixture %s absent (%v) — see 05-CAPTURE-DIFF.md to re-capture from the vanilla 26.2 jar", path, err)
	}
	return data
}

// vanillaPlayerInfoEntry is the parsed framing of a single PlayerInfoUpdate self-entry,
// walked as a 26.2 client would: 1-byte action mask, VarInt count, then per-entry the UUID
// and the present actions' bodies in enum order.
type vanillaPlayerInfoEntry struct {
	mask      byte
	count     int32
	uuid      uuid.UUID
	name      string
	propCount int32
	gameMode  int32 // -1 if UPDATE_GAME_MODE absent
	listed    int   // -1 if UPDATE_LISTED absent, else 0/1
}

// piu action bits (jar enum order). Mirrors play_join.go's constants but local to the test
// so the test pins the bit layout independently.
const (
	bitAddPlayer         = 0x01
	bitInitializeChat    = 0x02
	bitUpdateGameMode    = 0x04
	bitUpdateListed      = 0x08
	bitUpdateLatency     = 0x10
	bitUpdateDisplayName = 0x20
	bitUpdateListOrder   = 0x40
	bitUpdateHat         = 0x80
)

// parsePlayerInfoUpdateBody walks a ClientboundPlayerInfoUpdate body (everything after the
// packet-id varint) as a client decoder would, asserting it consumes to exactly zero
// trailing bytes. It supports the full 8-action set (vanilla) and any subset (Sulfur).
func parsePlayerInfoUpdateBody(t *testing.T, body []byte) vanillaPlayerInfoEntry {
	t.Helper()
	r := bytes.NewReader(body)
	read := func(f pk.FieldDecoder) {
		t.Helper()
		if _, err := f.ReadFrom(r); err != nil {
			t.Fatalf("PlayerInfoUpdate body decode: %v", err)
		}
	}
	var mask pk.Byte
	read(&mask)
	var count pk.VarInt
	read(&count)

	e := vanillaPlayerInfoEntry{mask: byte(mask), count: int32(count), gameMode: -1, listed: -1}

	var id pk.UUID
	read(&id)
	e.uuid = uuid.UUID(id)

	m := byte(mask)
	if m&bitAddPlayer != 0 {
		var name pk.String
		read(&name)
		e.name = string(name)
		var props pk.VarInt
		read(&props)
		e.propCount = int32(props)
		// 0 properties for offline; each property would be String name, String value,
		// Boolean signed, optional String signature — not present here (propCount==0).
		for k := int32(0); k < int32(props); k++ {
			var pn, pv pk.String
			var signed pk.Boolean
			read(&pn)
			read(&pv)
			read(&signed)
			if signed {
				var sig pk.String
				read(&sig)
			}
		}
	}
	if m&bitInitializeChat != 0 {
		var present pk.Boolean
		read(&present)
		if present {
			t.Fatalf("INITIALIZE_CHAT present=true unexpected in offline capture")
		}
	}
	if m&bitUpdateGameMode != 0 {
		var gm pk.VarInt
		read(&gm)
		e.gameMode = int32(gm)
	}
	if m&bitUpdateListed != 0 {
		var listed pk.Boolean
		read(&listed)
		if listed {
			e.listed = 1
		} else {
			e.listed = 0
		}
	}
	if m&bitUpdateLatency != 0 {
		var lat pk.VarInt
		read(&lat)
	}
	if m&bitUpdateDisplayName != 0 {
		var present pk.Boolean
		read(&present)
		if present {
			t.Fatalf("UPDATE_DISPLAY_NAME present=true unexpected in offline capture")
		}
	}
	if m&bitUpdateListOrder != 0 {
		var order pk.VarInt
		read(&order)
	}
	if m&bitUpdateHat != 0 {
		var showHat pk.Boolean
		read(&showHat)
	}
	if r.Len() != 0 {
		t.Fatalf("PlayerInfoUpdate body has %d trailing bytes — framing mismatch", r.Len())
	}
	return e
}

// TestPlayBytesVsVanillaCapture is the authoritative PLAY-01/02/05 byte-diff: it asserts
// Sulfur's PlayerInfoUpdate / SetDefaultSpawnPosition / ForgetLevelChunk encoders match the
// framing of real vanilla 26.2 golden packets.
func TestPlayBytesVsVanillaCapture(t *testing.T) {
	t.Run("PlayerInfoUpdate", func(t *testing.T) {
		golden := loadFixture(t, "vanilla-player-info-update.bin")

		// 1. The vanilla golden parses cleanly to zero trailing bytes (the framing
		//    the client itself uses). Vanilla's join entry carries the full 8-action set.
		van := parsePlayerInfoUpdateBody(t, golden)
		if van.count != 1 {
			t.Fatalf("vanilla entry count = %d, want 1", van.count)
		}
		if van.propCount != 0 {
			t.Errorf("vanilla property count = %d, want 0 (offline join sends no signed properties)", van.propCount)
		}
		// The action mask is exactly ONE byte (writeEnumSet over the 8-action enum). This is
		// the load-bearing width: a 2-byte mask would mis-frame every entry and kick the client.
		if van.mask&bitAddPlayer == 0 || van.mask&bitUpdateGameMode == 0 || van.mask&bitUpdateListed == 0 {
			t.Errorf("vanilla mask 0x%02x missing ADD_PLAYER|UPDATE_GAME_MODE|UPDATE_LISTED", van.mask)
		}
		if van.listed != 1 {
			t.Errorf("vanilla UPDATE_LISTED = %d, want 1", van.listed)
		}

		// 2. Sulfur's encoder produces the SAME framing for the actions it sends. The action
		//    SET differs (Sulfur sends the minimal 0x0d, vanilla 0xff) — that is documented and
		//    expected; we assert the framing of the shared actions, not the extra vanilla ones.
		id := uuid.New()
		p := writePlayerInfoUpdateAdd(id, "SulCap", gameModeSurvival)
		if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundPlayerInfoUpdate {
			t.Fatalf("Sulfur PlayerInfoUpdate id = %d, want %d", p.ID, packetid.ClientboundPlayerInfoUpdate)
		}
		sul := parsePlayerInfoUpdateBody(t, p.Data)

		// Framing assertions: 1-byte mask, VarInt(1) count, the ADD_PLAYER name + VarInt(0)
		// property count-prefix (the sealed MEDIUM-confidence sub-encoding), UPDATE_GAME_MODE
		// VarInt, UPDATE_LISTED Boolean — all matching vanilla's shared-action framing.
		if sul.count != van.count {
			t.Errorf("Sulfur count = %d, want vanilla's %d", sul.count, van.count)
		}
		if sul.mask&(bitAddPlayer|bitUpdateGameMode|bitUpdateListed) != (bitAddPlayer | bitUpdateGameMode | bitUpdateListed) {
			t.Errorf("Sulfur mask 0x%02x missing the listed-self actions", sul.mask)
		}
		if sul.propCount != van.propCount {
			t.Errorf("Sulfur property count = %d, want vanilla's %d (the sealed count-prefix shape)", sul.propCount, van.propCount)
		}
		if sul.name != "SulCap" {
			t.Errorf("Sulfur entry name = %q, want %q", sul.name, "SulCap")
		}
		if sul.uuid != id {
			t.Errorf("Sulfur entry uuid = %s, want %s", sul.uuid, id)
		}
		if sul.gameMode != gameModeSurvival {
			t.Errorf("Sulfur gameMode = %d, want %d", sul.gameMode, gameModeSurvival)
		}
		if sul.listed != 1 {
			t.Errorf("Sulfur UPDATE_LISTED = %d, want 1 (player appears in its own tab list)", sul.listed)
		}
	})

	t.Run("SetDefaultSpawnPosition", func(t *testing.T) {
		golden := loadFixture(t, "vanilla-set-default-spawn-position.bin")

		// Parse the vanilla golden as RespawnData = GlobalPos(Identifier dimension + packed
		// BlockPos Long) + Float yaw + Float pitch, asserting zero trailing bytes.
		parseSpawn := func(t *testing.T, body []byte) (dim string, pos pk.Position, yaw, pitch float32) {
			t.Helper()
			r := bytes.NewReader(body)
			var d pk.Identifier
			var p pk.Position
			var y, pi pk.Float
			for _, f := range []pk.FieldDecoder{&d, &p, &y, &pi} {
				if _, err := f.ReadFrom(r); err != nil {
					t.Fatalf("SetDefaultSpawnPosition decode: %v", err)
				}
			}
			if r.Len() != 0 {
				t.Fatalf("SetDefaultSpawnPosition body has %d trailing bytes — framing mismatch", r.Len())
			}
			return string(d), p, float32(y), float32(pi)
		}

		vDim, vPos, vYaw, vPitch := parseSpawn(t, golden)
		if vDim != overworldDimensionName {
			t.Errorf("vanilla dimension = %q, want %q", vDim, overworldDimensionName)
		}
		// Vanilla superflat default spawn is (0, -60, 0). The Y VALUE is generator-specific;
		// the load-bearing fact is the GlobalPos field ORDER (Identifier, then packed Long).
		if vPos.X != 0 || vPos.Z != 0 {
			t.Errorf("vanilla spawn x/z = (%d,%d), want (0,0)", vPos.X, vPos.Z)
		}
		if vYaw != 0 || vPitch != 0 {
			t.Errorf("vanilla yaw/pitch = (%v,%v), want (0,0)", vYaw, vPitch)
		}

		// Sulfur's encoder must produce the SAME framing for the same dimension. Sulfur's
		// surface Y differs (stone at -48 vs vanilla grass at -60) — content, not framing.
		p := writeSetDefaultSpawnPosition(overworldDimensionName, pk.Position{X: 0, Y: vPos.Y, Z: 0}, 0, 0)
		if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundSetDefaultSpawnPosition {
			t.Fatalf("Sulfur SetDefaultSpawnPosition id = %d, want %d", p.ID, packetid.ClientboundSetDefaultSpawnPosition)
		}
		// With the SAME inputs as vanilla (dimension + the vanilla spawn pos), Sulfur's body
		// must be BYTE-IDENTICAL to the vanilla golden: the RespawnData/GlobalPos restructure
		// is sealed.
		if !bytes.Equal(p.Data, golden) {
			t.Errorf("Sulfur SetDefaultSpawnPosition body != vanilla golden for identical inputs\n  sulfur:  %x\n  vanilla: %x", p.Data, golden)
		}
		sDim, sPos, sYaw, sPitch := parseSpawn(t, p.Data)
		if sDim != vDim || sPos != vPos || sYaw != vYaw || sPitch != vPitch {
			t.Errorf("Sulfur framing (%q,%v,%v,%v) != vanilla (%q,%v,%v,%v)", sDim, sPos, sYaw, sPitch, vDim, vPos, vYaw, vPitch)
		}
	})

	t.Run("ForgetLevelChunk", func(t *testing.T) {
		golden := loadFixture(t, "vanilla-forget-level-chunk.bin")
		// The golden is the jar-derived 26.2 ChunkPos.pack layout for chunk (5,7):
		// pack(x,z) = (x & 0xFFFFFFFF) | ((z & 0xFFFFFFFF) << 32), written as a single
		// big-endian Long (verified via javap on ClientboundForgetLevelChunkPacket.write ->
		// FriendlyByteBuf.writeChunkPos -> ChunkPos.pack -> writeLong). Vanilla emits this only
		// on a real out-of-range walk (the Task-2 human gate); the layout itself has no
		// version-drift ambiguity — it is a single writeLong of the packed pos.
		var vanLong pk.Long
		if _, err := vanLong.ReadFrom(bytes.NewReader(golden)); err != nil {
			t.Fatalf("forget golden decode: %v", err)
		}
		// Sulfur's ForgetLevelChunk for (5,7) must be byte-identical to the jar-derived golden.
		p := world.ForgetLevelChunk(5, 7)
		if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundForgetLevelChunk {
			t.Fatalf("Sulfur ForgetLevelChunk id = %d, want %d", p.ID, packetid.ClientboundForgetLevelChunk)
		}
		if !bytes.Equal(p.Data, golden) {
			t.Errorf("Sulfur ForgetLevelChunk(5,7) body != jar-derived golden\n  sulfur:  %x\n  golden:  %x", p.Data, golden)
		}
		// Decode-confirm x=low32, z=high32 round-trips (negative-correct via int32 cast).
		var sulLong pk.Long
		if _, err := sulLong.ReadFrom(bytes.NewReader(p.Data)); err != nil {
			t.Fatalf("Sulfur forget decode: %v", err)
		}
		if int32(uint64(sulLong)) != 5 || int32(uint64(sulLong)>>32) != 7 {
			t.Errorf("Sulfur packed long unpacks to (x=%d,z=%d), want (5,7)", int32(uint64(sulLong)), int32(uint64(sulLong)>>32))
		}
	})
}
