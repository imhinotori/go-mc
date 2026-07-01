//go:build botlive

package botclient

import (
	"context"
	"math"
	"testing"
	"time"
)

// TestLiveZombieOrbitNoStall reproduces "el zombie se queda parado al lado del jugador": the bot spawns
// a zombie, then STRAFES around it at close range (staying near/at melee reach) instead of walking in a
// straight line away. The failure mode is the zombie sitting still beside the player when the player is
// technically in its attack box but off to the side — the melee goal only re-paths on a ≥1-block move,
// a dead nav path (out of reach), or a 5% nudge, so a player circling at ~1-2 blocks can leave the mob
// parked. A healthy pursuit re-orients toward the player every couple of ticks and keeps attacking, so
// the mob should MOVE (non-zero displacement) and the bot should keep taking hits.
func TestLiveZombieOrbitNoStall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "orbitbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	if err := c.WaitTicks(ctx, 10); err != nil {
		t.Fatal(err)
	}
	// settle on ground (no spawn fall confounder)
	for i := 0; i < 20; i++ {
		if st := c.State(); st.OnGround && st.HealthSeen {
			break
		}
		if err := c.WaitTicks(ctx, 2); err != nil {
			t.Fatal(err)
		}
	}

	center := c.State()
	if err := c.Chat("/dbg zombie"); err != nil {
		t.Fatalf("/dbg zombie: %v", err)
	}
	// Open a small gap so the zombie doesn't instantly kill the bot; then orbit.
	for d := 1; d <= 4; d++ {
		_ = c.MoveTo(ctx, center.X+float64(d), center.Y, center.Z)
		if err := c.WaitTicks(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	zid, ok := dbgSpawnedEID(c)
	if !ok {
		t.Fatalf("no [dbg] zombie spawn-eid ack (chat=%v)", c.RecentChat(10))
	}
	if st := c.State(); st.HealthSeen && st.Health <= 0 {
		t.Fatalf("bot died before orbit (health %.1f)", st.Health)
	}

	// Orbit the zombie at radius ~2.5 blocks. Sample the zombie displacement each waypoint; a mob that
	// re-orients to keep facing/closing on the circling player MOVES, a parked one does not.
	const radius = 2.0
	const steps = 80 // FINE steps (a small angular move + short wait each) ≈ smooth human strafing
	var samples []float64 // zombie displacement per sample
	var prevX, prevZ float64
	havePrev := false
	zx0, zz0 := 0.0, 0.0 // zombie start
	minHP := c.State().Health

	// anchor the orbit around the zombie's current position
	if z, alive := nearestPigByID(c, zid); alive {
		zx0, zz0 = z.X, z.Z
	}

	for i := 0; i < steps; i++ {
		ang := (float64(i) / float64(steps)) * 2 * math.Pi * 2 // two full laps, many small steps
		wx := zx0 + radius*math.Cos(ang)
		wz := zz0 + radius*math.Sin(ang)
		_ = c.MoveTo(ctx, wx, center.Y, wz)
		if err := c.WaitTicks(ctx, 2); err != nil {
			t.Fatal(err)
		}
		if st := c.State(); st.HealthSeen && st.Health < minHP {
			minHP = st.Health
		}
		z, alive := nearestPigByID(c, zid)
		if !alive {
			continue
		}
		if havePrev {
			samples = append(samples, math.Hypot(z.X-prevX, z.Z-prevZ))
		}
		prevX, prevZ = z.X, z.Z
		havePrev = true
	}

	if len(samples) < 8 {
		t.Fatalf("too few zombie samples (%d)", len(samples))
	}
	// Count "parked" samples (near-zero displacement while the bot is orbiting nearby).
	parked := 0
	for _, d := range samples {
		if d < 0.05 {
			parked++
		}
	}
	parkedFrac := float64(parked) / float64(len(samples))
	t.Logf("orbit: samples=%d parked=%d (%.0f%%) startHP=%.1f minHP=%.1f", len(samples), parked, parkedFrac*100, c.State().Health, minHP)

	if parkedFrac > 0.5 {
		t.Errorf("zombie PARKED beside the player %.0f%% of the orbit (want ≤50%%) — the 'se queda parado al lado' stall", parkedFrac*100)
	}
}
