//go:build botlive

package botclient

import (
	"context"
	"math"
	"testing"
	"time"
)

// TestLiveZombieStartStopSmooth stresses the "parte mal, luego camina bien" start-from-standstill case:
// the bot repeatedly JUMPS to a new nearby position (a different direction each time) and holds, forcing
// the zombie to stop, re-orient, and start walking again over and over. Each start is where the old code
// lunged in the stale facing / span around. We sample the zombie's per-tick heading and flag SHARP
// direction reversals (a step whose direction differs from the previous by > ~120°), which are the
// visible "raro" spin/moonwalk. The ported travel physics (accelerate along yaw, friction, rotlerp)
// should turn smoothly (≤90°/tick body) so reversals are rare.
func TestLiveZombieStartStopSmooth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "startstopbot"); err != nil {
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
	// open a gap so the spawn-on-top zombie doesn't kill the bot
	for d := 1; d <= 8; d++ {
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

	// A set of hold points around the spawn — the bot teleports to each, holds ~1s, so the zombie must
	// start/stop and re-orient repeatedly. Radius ~6 so the zombie always has to walk (not just stand).
	base := c.State()
	offsets := [][2]float64{{6, 0}, {0, 6}, {-6, 0}, {0, -6}, {5, 5}, {-5, 5}, {-5, -5}, {5, -5}}

	sharpReversals := 0
	steps := 0
	var prevDX, prevDZ float64
	havePrevDir := false
	var prevZX, prevZZ float64
	havePrevPos := false

	for _, off := range offsets {
		hx := base.X + off[0]
		hz := base.Z + off[1]
		_ = c.MoveTo(ctx, hx, base.Y, hz)
		// hold ~1s (20 ticks) sampling the zombie's motion every 2 ticks
		for k := 0; k < 10; k++ {
			if err := c.WaitTicks(ctx, 2); err != nil {
				t.Fatal(err)
			}
			z, alive := nearestPigByID(c, zid)
			if !alive {
				continue
			}
			if havePrevPos {
				mx := z.X - prevZX
				mz := z.Z - prevZZ
				mlen := math.Hypot(mx, mz)
				if mlen > 0.03 { // only judge direction when the mob actually moved this sample
					steps++
					if havePrevDir {
						// angle between this move direction and the previous move direction
						dot := (mx*prevDX + mz*prevDZ) / (mlen * math.Hypot(prevDX, prevDZ))
						if dot < -0.5 { // > 120° reversal — a sharp spin/backstep
							sharpReversals++
						}
					}
					prevDX, prevDZ = mx, mz
					havePrevDir = true
				}
			}
			prevZX, prevZZ = z.X, z.Z
			havePrevPos = true
		}
	}

	revFrac := 0.0
	if steps > 0 {
		revFrac = float64(sharpReversals) / float64(steps)
	}
	t.Logf("start-stop: movingSamples=%d sharpReversals=%d (%.0f%%)", steps, sharpReversals, revFrac*100)

	if steps < 20 {
		t.Fatalf("too few moving samples (%d) — zombie barely moved", steps)
	}
	if revFrac > 0.2 {
		t.Errorf("zombie reversed direction sharply on %.0f%% of moves (want ≤20%%) — the start/strafe spin ('se mueve raro')", revFrac*100)
	}
}
