package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// sound_test.go exercises the sound wire subsystem (sound.go): the positional ClientboundSound
// encoder wire body, the getRange(volume)-radius broadcast gate for playSound / playSoundEntity, and
// the mob hurt-sound broadcast. The pig oracle draws ZERO sounds during its AI window (it deals no
// damage and starts no drink/raid), so none of these paths add draws to a pinned mob stream.

// newSoundViewer builds a tickPlayer at (x,y,z) with a capture client so drainPackets can read
// whatever a sound broadcast sent it.
func newSoundViewer(x, y, z float64) *tickPlayer {
	return &tickPlayer{x: x, y: y, z: z, client: captureClient(64)}
}

// TestEncodeSoundWireBody pins the positional ClientboundSound body byte-for-byte against the
// jar-verified field order: VarInt(id+1), VarInt(source), Int(x*8), Int(y*8), Int(z*8), Float(volume),
// Float(pitch), Long(seed). The x/y/z fixed-point is (int)(coord*8.0).
func TestEncodeSoundWireBody(t *testing.T) {
	const (
		soundID = int32(1777) // entity.witch.drink
		source  = soundSourceHostile
		x, y, z = 10.5, 65.0, -3.25
		volume  = float32(1.0)
		pitch   = float32(0.9)
		seed    = int64(0x0123456789ABCDEF)
	)
	got := encodeSound(soundID, source, x, y, z, volume, pitch, seed)
	if got.ID != int32(packetid.ClientboundSound) {
		t.Fatalf("packet id = %d, want ClientboundSound (%d)", got.ID, int32(packetid.ClientboundSound))
	}

	var want bytes.Buffer
	for _, f := range []pk.FieldEncoder{
		pk.VarInt(soundID + 1),
		pk.VarInt(int32(source)),
		pk.Int(int32(x * 8.0)),
		pk.Int(int32(y * 8.0)),
		pk.Int(int32(z * 8.0)),
		pk.Float(volume),
		pk.Float(pitch),
		pk.Long(seed),
	} {
		if _, err := f.WriteTo(&want); err != nil {
			t.Fatalf("building expected body: %v", err)
		}
	}
	if !bytes.Equal(got.Data, want.Bytes()) {
		t.Fatalf("encodeSound body mismatch:\n got %x\nwant %x", got.Data, want.Bytes())
	}
}

// TestPlaySoundBroadcast: a player within getRange(1.0)==16 of the sound point receives the positional
// ClientboundSound; a player outside it does not. Pins the dx*dx+dy*dy+dz*dz < radius*radius gate.
func TestPlaySoundBroadcast(t *testing.T) {
	loop := &TickLoop{}
	near := newSoundViewer(4, 64, 4)    // ~well within 16 of (10,64,10)
	far := newSoundViewer(100, 64, 100) // > 16 blocks away
	loop.players = append(loop.players, near, far)

	loop.playSound(1777, soundSourceHostile, 10, 64, 10, 1.0, 1.0, 42)

	if got := drainPackets(near.client); len(got) != 1 {
		t.Fatalf("near player received %d packets, want 1", len(got))
	} else if got[0].ID != int32(packetid.ClientboundSound) {
		t.Fatalf("near packet id = %d, want ClientboundSound (%d)", got[0].ID, int32(packetid.ClientboundSound))
	}
	if got := drainPackets(far.client); len(got) != 0 {
		t.Fatalf("far player (>16) received %d packets, want 0", len(got))
	}
}

// TestPlaySoundRadiusScalesWithVolume: getRange(volume) = volume>1 ? volume*16 : 16, so a volume-64 horn
// (raid horn) reaches a player 100 blocks away that a volume-1 sound would not. Pins the volume->radius
// scaling in soundRange.
func TestPlaySoundRadiusScalesWithVolume(t *testing.T) {
	// A volume-1 sound (radius 16) must NOT reach a player ~141 blocks away.
	loopLow := &TickLoop{}
	low := newSoundViewer(100, 64, 100)
	loopLow.players = append(loopLow.players, low)
	loopLow.playSound(1354, soundSourceNeutral, 0, 64, 0, 1.0, 1.0, 7) // volume 1 -> radius 16
	if got := drainPackets(low.client); len(got) != 0 {
		t.Fatalf("volume-1 sound reached the 141-block player (%d packets), want 0", len(got))
	}

	// The SAME player DOES hear a volume-64 sound (radius 64*16 == 1024) -- the raid-horn volume.
	loopHi := &TickLoop{}
	hi := newSoundViewer(100, 64, 100)
	loopHi.players = append(loopHi.players, hi)
	loopHi.playSound(1354, soundSourceNeutral, 0, 64, 0, 64.0, 1.0, 7) // volume 64 -> radius 1024
	if got := drainPackets(hi.client); len(got) != 1 {
		t.Fatalf("volume-64 sound reached the 141-block player %d times, want 1", len(got))
	}
}

// TestPlaySoundEntityBroadcast: playSoundEntity uses the entity's position for the radius test and emits
// a ClientboundSoundEntity (not a positional packet). A tracking-independent nearby player hears it.
func TestPlaySoundEntityBroadcast(t *testing.T) {
	loop := &TickLoop{}
	mob := &Entity{id: 1, x: 20, y: 64, z: 20}
	near := newSoundViewer(22, 64, 22)
	far := newSoundViewer(200, 64, 200)
	loop.players = append(loop.players, near, far)

	loop.playSoundEntity(mob, 1269, soundSourceNeutral, 1.0, 1.0, 99)

	if got := drainPackets(near.client); len(got) != 1 {
		t.Fatalf("near player received %d packets, want 1", len(got))
	} else if got[0].ID != int32(packetid.ClientboundSoundEntity) {
		t.Fatalf("near packet id = %d, want ClientboundSoundEntity (%d)", got[0].ID, int32(packetid.ClientboundSoundEntity))
	}
	if got := drainPackets(far.client); len(got) != 0 {
		t.Fatalf("far player received %d packets, want 0", len(got))
	}
}

// TestPlayMobHurtSoundBroadcasts: the mob hurt-sound limb (playMobHurtSound) sends the entity-attached
// hurt ClientboundSoundEntity to a player tracking the mob. This is the "a hurt broadcasts sound to a
// nearby player" gate. playMobHurtSound uses broadcastToTrackers, so the viewer tracks the mob id.
func TestPlayMobHurtSoundBroadcasts(t *testing.T) {
	loop := &TickLoop{}
	mob := &Entity{id: 1, x: 0, y: 64, z: 0}
	viewer := &tickPlayer{client: captureClient(64), entityID: 1000, tracked: map[int32]bool{mob.id: true}}
	loop.players = append(loop.players, viewer)

	loop.playMobHurtSound(mob)

	got := drainPackets(viewer.client)
	if len(got) != 1 {
		t.Fatalf("tracking player received %d packets, want 1 (hurt sound)", len(got))
	}
	if got[0].ID != int32(packetid.ClientboundSoundEntity) {
		t.Fatalf("hurt packet id = %d, want ClientboundSoundEntity (%d)", got[0].ID, int32(packetid.ClientboundSoundEntity))
	}
}
