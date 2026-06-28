package server

import (
	"context"
	"io"
	"math"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/save"

	"github.com/google/uuid"
)

// position_load_test.go covers GAMEPLAY-02: a reconnecting player's persisted Pos drives
// the SINGLE bootstrap teleport AND seeds tickPlayer.x/y/z + center, so the player spawns
// at its saved location with the view ring centered there (no second teleport, Pitfall 3).

// TestPositionLoadApplied writes a persisted .dat with a non-origin Pos, runs AcceptPlayer
// against it, captures the registered tickPlayer off the loop.register channel, and asserts
// the persisted position (not the hardcoded spawn) reached the player + its view center.
func TestPositionLoadApplied(t *testing.T) {
	dir := t.TempDir()
	id := uuid.New()

	const wantX, wantY, wantZ = 100.5, 70.0, -40.5
	const wantYaw, wantPitch = 12.0, -7.0
	want := save.PlayerData{
		Pos:                 [3]float64{wantX, wantY, wantZ},
		Rotation:            [2]float32{wantYaw, wantPitch},
		Health:              18,
		FoodLevel:           17,
		FoodSaturationLevel: 4,
	}
	if err := savePlayer(dir, id, want); err != nil {
		t.Fatalf("savePlayer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	inbound := make(chan Intent, 64)
	loop := NewTickLoop(newFakeClock())
	keep := NewKeepAlive()
	// KeepAlive.ClientJoin sends on an unbuffered channel, so AcceptPlayer blocks at join
	// until KeepAlive.Run consumes it — run it (and drain inbound) so AcceptPlayer reaches
	// the register handoff.
	go keep.Run(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-inbound:
			}
		}
	}()
	g := NewGameTick(inbound, loop, keep, -48, SpawnPoint{X: 8.5, Y: -46, Z: 8.5})
	g.SetWorldDir(dir)

	server, client := newPipe(t)
	// Drain the client end so the connection's writeLoop never blocks on the bootstrap
	// packets AcceptPlayer enqueues before it registers the player.
	go io.Copy(io.Discard, client)

	go g.AcceptPlayer("reconnect", id, nil, nil, ProtocolVersion, server, 0)

	// AcceptPlayer constructs the tickPlayer off-tick and hands it to the owner via
	// loop.register. Capture it directly (the loop is not Run here, so the channel is the
	// authoritative handoff).
	var p *tickPlayer
	select {
	case p = <-loop.register:
	case <-time.After(2 * time.Second):
		t.Fatal("AcceptPlayer never registered a player")
	}

	if p.x != wantX || p.y != wantY || p.z != wantZ {
		t.Fatalf("persisted pos not applied: got (%v,%v,%v), want (%v,%v,%v)", p.x, p.y, p.z, wantX, wantY, wantZ)
	}
	if p.yaw != wantYaw || p.pitch != wantPitch {
		t.Fatalf("persisted rotation not applied: got (%v,%v), want (%v,%v)", p.yaw, p.pitch, wantYaw, wantPitch)
	}
	wantCenter := chunkCenterOf(int32(math.Floor(wantX)), int32(math.Floor(wantZ)))
	if p.center != wantCenter {
		t.Fatalf("view center not seeded from pos: got %v, want %v (must NOT be {0,0})", p.center, wantCenter)
	}
}
