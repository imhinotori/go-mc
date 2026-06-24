package server

import (
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// movePlayerPos builds a ServerboundMovePlayerPos: Double x,y,z + UnsignedByte flags.
func movePlayerPos(x, y, z float64, flags uint8) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundMovePlayerPos),
		pk.Double(x), pk.Double(y), pk.Double(z), pk.UnsignedByte(flags))
}

// movePlayerPosRot builds a ServerboundMovePlayerPosRot: Double x,y,z + Float yaw,pitch + UnsignedByte flags.
func movePlayerPosRot(x, y, z float64, yaw, pitch float32, flags uint8) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundMovePlayerPosRot),
		pk.Double(x), pk.Double(y), pk.Double(z), pk.Float(yaw), pk.Float(pitch), pk.UnsignedByte(flags))
}

// movePlayerRot builds a ServerboundMovePlayerRot: Float yaw,pitch + UnsignedByte flags.
func movePlayerRot(yaw, pitch float32, flags uint8) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundMovePlayerRot),
		pk.Float(yaw), pk.Float(pitch), pk.UnsignedByte(flags))
}

// movePlayerStatusOnly builds a ServerboundMovePlayerStatusOnly: UnsignedByte flags only.
func movePlayerStatusOnly(flags uint8) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundMovePlayerStatusOnly), pk.UnsignedByte(flags))
}

// confirmedPlayer builds a tickPlayer that has already confirmed its teleport, so
// applyInput accepts movement. A real center is set so re-center math has a basis.
func confirmedPlayer() *tickPlayer {
	return &tickPlayer{
		confirmedTeleport: true,
		center:            level.ChunkPos{0, 0},
		viewDist:          serverViewDistance,
		sentChunks:        map[level.ChunkPos]bool{},
	}
}

// TestMovementDecode covers all four ServerboundMovePlayer* layouts decoding into
// tick-owned position, the packed-flags-byte semantics (NEVER a Boolean), and the
// malformed-payload safety (return without mutating, no panic).
func TestMovementDecode(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	at := loop.clock.Now()

	t.Run("Pos", func(t *testing.T) {
		p := confirmedPlayer()
		loop.applyInput(p, SubtickInput{At: at, Packet: movePlayerPos(10.5, 64.0, -7.25, 0x01)})
		if p.x != 10.5 || p.y != 64.0 || p.z != -7.25 {
			t.Fatalf("Pos position = (%v,%v,%v), want (10.5,64,-7.25)", p.x, p.y, p.z)
		}
		if !p.onGround {
			t.Fatal("Pos flags 0x01 should set onGround")
		}
	})

	t.Run("PosRot", func(t *testing.T) {
		p := confirmedPlayer()
		loop.applyInput(p, SubtickInput{At: at, Packet: movePlayerPosRot(1.0, 2.0, 3.0, 90.0, -45.0, 0x01)})
		if p.x != 1.0 || p.y != 2.0 || p.z != 3.0 {
			t.Fatalf("PosRot position = (%v,%v,%v), want (1,2,3)", p.x, p.y, p.z)
		}
		if p.yaw != 90.0 || p.pitch != -45.0 {
			t.Fatalf("PosRot look = (%v,%v), want (90,-45)", p.yaw, p.pitch)
		}
	})

	t.Run("Rot", func(t *testing.T) {
		p := confirmedPlayer()
		p.x, p.y, p.z = 100, 200, 300
		loop.applyInput(p, SubtickInput{At: at, Packet: movePlayerRot(180.0, 30.0, 0x00)})
		if p.x != 100 || p.y != 200 || p.z != 300 {
			t.Fatalf("Rot must NOT change position; got (%v,%v,%v)", p.x, p.y, p.z)
		}
		if p.yaw != 180.0 || p.pitch != 30.0 {
			t.Fatalf("Rot look = (%v,%v), want (180,30)", p.yaw, p.pitch)
		}
		if p.onGround {
			t.Fatal("Rot flags 0x00 should clear onGround")
		}
	})

	t.Run("StatusOnly", func(t *testing.T) {
		p := confirmedPlayer()
		p.x, p.y, p.z = 5, 6, 7
		p.center = level.ChunkPos{9, 9} // a status-only update must not re-center
		loop.applyInput(p, SubtickInput{At: at, Packet: movePlayerStatusOnly(0x01)})
		if p.x != 5 || p.y != 6 || p.z != 7 {
			t.Fatalf("StatusOnly must NOT change position; got (%v,%v,%v)", p.x, p.y, p.z)
		}
		if !p.onGround {
			t.Fatal("StatusOnly flags 0x01 should set onGround")
		}
		if p.center != (level.ChunkPos{9, 9}) {
			t.Fatalf("StatusOnly must not re-center; center=%v", p.center)
		}
	})

	t.Run("FlagsByteMasked", func(t *testing.T) {
		// A flags byte of 0x03 sets BOTH onGround (bit0) and horizontalCollision (bit1).
		// Reading the trailing field as a Boolean would consume only 1 byte the same way,
		// but a value of 0x03 is NOT a valid Boolean (only 0x00/0x01) — and crucially the
		// SEMANTICS differ: a Boolean read yields true and discards bit1. We assert the
		// byte is masked: onGround true AND the packet framed correctly (no trailing-byte
		// error, position decoded cleanly preceding the flags).
		p := confirmedPlayer()
		loop.applyInput(p, SubtickInput{At: at, Packet: movePlayerPos(8.0, 9.0, 10.0, 0x03)})
		if p.x != 8.0 || p.y != 9.0 || p.z != 10.0 {
			t.Fatalf("flags 0x03 mis-framed the packet: position=(%v,%v,%v)", p.x, p.y, p.z)
		}
		if !p.onGround {
			t.Fatal("flags 0x03 must set onGround (bit0)")
		}
	})

	t.Run("MalformedReturnsWithoutMutating", func(t *testing.T) {
		p := confirmedPlayer()
		p.x, p.y, p.z = 42, 43, 44
		// A Pos packet truncated mid-body: only the id-tagged Double x, no y/z/flags.
		truncated := pk.Packet{
			ID:   int32(packetid.ServerboundMovePlayerPos),
			Data: pk.Marshal(int32(packetid.ServerboundMovePlayerPos), pk.Double(1.0)).Data,
		}
		loop.applyInput(p, SubtickInput{At: at, Packet: truncated}) // must not panic
		if p.x != 42 || p.y != 43 || p.z != 44 {
			t.Fatalf("malformed payload mutated position to (%v,%v,%v)", p.x, p.y, p.z)
		}
	})
}

// TestTeleportGate covers PLAY-02: applyInput drops movement until confirmedTeleport,
// and dispatch confirms ONLY on an echoed id matching awaitingTeleport (T-5-01/03).
func TestTeleportGate(t *testing.T) {
	t.Run("MovementDroppedBeforeConfirm", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := &tickPlayer{confirmedTeleport: false, center: level.ChunkPos{0, 0}}
		loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: movePlayerPosRot(99, 99, 99, 0, 0, 0x01)})
		if p.x != 0 || p.y != 0 || p.z != 0 {
			t.Fatalf("movement accepted before teleport confirm: position=(%v,%v,%v)", p.x, p.y, p.z)
		}
	})

	t.Run("MovementAcceptedAfterConfirm", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := confirmedPlayer()
		loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: movePlayerPosRot(11, 22, 33, 0, 0, 0x01)})
		if p.x != 11 || p.y != 22 || p.z != 33 {
			t.Fatalf("movement dropped after confirm: position=(%v,%v,%v)", p.x, p.y, p.z)
		}
	})

	t.Run("DispatchConfirmsOnlyMatchingID", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := &tickPlayer{client: &Client{}, awaitingTeleport: 7}
		loop.players = append(loop.players, p)
		loop.clientIndex = map[*Client]*tickPlayer{p.client: p}

		// A wrong/forged id must NOT confirm the gate.
		wrong := pk.Marshal(int32(packetid.ServerboundAcceptTeleportation), pk.VarInt(6))
		loop.dispatch(p.client, wrong)
		if p.confirmedTeleport {
			t.Fatal("a forged teleport id (6 != 7) must not confirm the gate (T-5-01)")
		}

		// The matching id confirms.
		match := pk.Marshal(int32(packetid.ServerboundAcceptTeleportation), pk.VarInt(7))
		loop.dispatch(p.client, match)
		if !p.confirmedTeleport {
			t.Fatal("a matching teleport id (7) must confirm the gate")
		}
	})

	t.Run("PlayerLoadedNoop", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := &tickPlayer{client: &Client{}}
		loop.players = append(loop.players, p)
		loop.clientIndex = map[*Client]*tickPlayer{p.client: p}
		loaded := pk.Marshal(int32(packetid.ServerboundPlayerLoaded))
		loop.dispatch(p.client, loaded) // must not panic, never blocks
		if !p.loaded {
			t.Fatal("ServerboundPlayerLoaded should record loaded=true")
		}
		if p.subtick.len() != 0 {
			t.Fatal("PlayerLoaded must not append a subtick input")
		}
	})
}

// TestSubtickHookFiresBeforeGate guards the hook-before-gate ordering that the existing
// TestSubtickOrdering/TestSubtickBufferCap depend on: those build tickPlayer{} with
// confirmedTeleport==false and assert the applyInputHook fires for every input. If the
// teleport gate ran before the hook, the hook would not fire for an unconfirmed player.
func TestSubtickHookFiresBeforeGate(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	var fired int
	loop.applyInputHook = func(_ *tickPlayer, _ SubtickInput) { fired++ }
	p := &tickPlayer{confirmedTeleport: false} // gate closed
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: movePlayerStatusOnly(0x00)})
	if fired != 1 {
		t.Fatalf("applyInputHook fired %d times for an unconfirmed player, want 1 (hook must precede the gate)", fired)
	}
	_ = time.Now
}
