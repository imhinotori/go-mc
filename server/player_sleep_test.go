package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestStopSleepingCommandWakesPlayer: the ServerboundPlayerCommand STOP_SLEEPING action (ordinal 0, the
// "Leave bed" button) wakes a sleeping player. Without the handler the player is stuck in bed until dawn
// (ServerGamePacketListenerImpl.handlePlayerCommand: isSleeping → stopSleepInBed(false, true)).
func TestStopSleepingCommandWakesPlayer(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{client: &Client{}}
	loop.players = append(loop.players, p)
	loop.clientIndex = map[*Client]*tickPlayer{p.client: p}

	// Put the player to sleep (nil world → stopSleeping's OCCUPIED clear is skipped, wake still runs).
	sp := pk.Position{X: 1, Y: 64, Z: 1}
	p.sleepingPos = &sp
	if !p.isSleeping() {
		t.Fatal("precondition: player must be sleeping")
	}

	// STOP_SLEEPING is enum ordinal 0 on the wire: (entityId, actionId=0, data=0).
	cmd := pk.Marshal(int32(packetid.ServerboundPlayerCommand),
		pk.VarInt(0), pk.VarInt(0), pk.VarInt(0))
	loop.dispatch(p.client, cmd)

	if p.isSleeping() {
		t.Fatal("player still sleeping after STOP_SLEEPING command — the Leave-bed button did nothing")
	}
	// stopSleepInBed(false, ...) sets sleepCounter to SLEEP_DURATION for the waking unwind.
	if p.sleepCounter != sleepDuration {
		t.Fatalf("sleepCounter = %d after wake, want %d (stopSleepInBed false → the waking unwind)", p.sleepCounter, sleepDuration)
	}
}

// TestStopSleepingCommandAwakeIsNoop: the STOP_SLEEPING command on an awake player is a harmless no-op
// (the isSleeping() guard).
func TestStopSleepingCommandAwakeIsNoop(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{client: &Client{}}
	loop.players = append(loop.players, p)
	loop.clientIndex = map[*Client]*tickPlayer{p.client: p}

	cmd := pk.Marshal(int32(packetid.ServerboundPlayerCommand),
		pk.VarInt(0), pk.VarInt(0), pk.VarInt(0))
	loop.dispatch(p.client, cmd)

	if p.isSleeping() || p.sleepCounter != 0 {
		t.Fatalf("awake player perturbed by STOP_SLEEPING: sleeping=%v counter=%d", p.isSleeping(), p.sleepCounter)
	}
}
