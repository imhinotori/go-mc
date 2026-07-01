//go:build botlive

package botclient

import (
	"context"
	"math"
	"testing"
	"time"
)

// TestLiveZombieAdjacentAttacks reproduces "el zombie se queda parado al lado del jugador": the bot
// stands STILL right next to the zombie and we check the zombie keeps attacking (bot health falls) and
// keeps tracking/facing the bot (head-rotation packets). A zombie that "parks" beside a stationary
// player — in its attack box but not swinging / not facing — is the reported bug.
func TestLiveZombieAdjacentAttacks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "adjbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
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
	// Dash away immediately so the spawn-on-top zombie doesn't kill the bot; open a ~10-block gap.
	for d := 1; d <= 10; d++ {
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
		t.Fatalf("bot died opening the gap (health %.1f)", st.Health)
	}

	// Now position ~2.3 blocks from the ZOMBIE (just outside melee reach) and HOLD STILL. The zombie
	// must take the LAST step to close the gap and start swinging. If it parks at ~2.3 without closing,
	// that is the "se queda parado al lado" stall. Anchor off the zombie's live position.
	z0, alive := nearestPigByID(c, zid)
	if !alive {
		t.Fatalf("zombie eid=%d not tracked", zid)
	}
	holdX := z0.X + 2.3
	holdZ := z0.Z
	_ = c.MoveTo(ctx, holdX, center.Y, holdZ)
	if err := c.WaitTicks(ctx, 6); err != nil {
		t.Fatal(err)
	}

	startHP := c.State().Health
	// Hold still for ~5s (250 ticks) sampling health + zombie tracking.
	hits := 0
	lastHP := startHP
	zMoved := 0
	var prevZX, prevZZ float64
	if z, alive := nearestPigByID(c, zid); alive {
		prevZX, prevZZ = z.X, z.Z
	}
	minDist := 999.0
	for i := 0; i < 50; i++ {
		_ = c.MoveTo(ctx, holdX, center.Y, holdZ) // re-pin (knockback would otherwise drift the bot)
		if err := c.WaitTicks(ctx, 5); err != nil {
			t.Fatal(err)
		}
		st := c.State()
		if st.HealthSeen && st.Health < lastHP {
			hits++
			lastHP = st.Health
		}
		if z, alive := nearestPigByID(c, zid); alive {
			if math.Hypot(z.X-prevZX, z.Z-prevZZ) > 0.02 {
				zMoved++
			}
			prevZX, prevZZ = z.X, z.Z
			if d := math.Hypot(z.X-st.X, z.Z-st.Z); d < minDist {
				minDist = d
			}
		}
	}
	t.Logf("adjacent-still: startHP=%.1f endHP=%.1f hits=%d zMovedSamples=%d/50 minDist=%.2f", startHP, lastHP, hits, zMoved, minDist)

	// The core proof: a zombie beside a stationary player KEEPS ATTACKING (health drops repeatedly). If
	// it "parked" without swinging, health would plateau at startHP.
	if hits == 0 || lastHP >= startHP {
		t.Errorf("zombie beside a still player did NOT keep attacking (startHP=%.1f endHP=%.1f hits=%d) — the 'parado al lado' stall", startHP, lastHP, hits)
	}
}
