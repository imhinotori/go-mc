//go:build botlive

package botclient

import (
	"context"
	"math"
	"testing"
	"time"
)

// TestLiveZombieWalkSmooth observes a hunting zombie's GAIT while the bot keeps walking away in a
// straight line. It is the live-verification of the two pathing fixes this session:
//   - the MeleeAttackGoal path-recompute THROTTLE (no per-tick want re-set → no jitter), and
//   - the arrival-clear re-engage bridge (no freezing short of the target → no "se queda parado").
//
// Method: spawn a zombie at the bot, then repeatedly step the bot +X by 1 block and sample the
// zombie's (x,z) every 2 ticks. For each sample compute the zombie's displacement since the previous
// sample. A healthy pursuit produces a steady stream of NON-ZERO displacements while the zombie is
// farther than melee reach. A FREEZE shows up as a run of ~0-displacement samples while still out of
// reach. A JITTER (the pre-fix bug) shows up as displacement that reverses sign sample-to-sample (the
// mob stepping back and forth). We assert: (a) the zombie net-approaches, (b) it is moving a healthy
// fraction of the time it is out of reach (no long freeze), (c) it does not oscillate every sample.
func TestLiveZombieWalkSmooth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "walkbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	if err := c.WaitTicks(ctx, 10); err != nil {
		t.Fatal(err)
	}

	// Settle on the superflat floor first: the bot can spawn well above the flat top (y=-48) and take
	// heavy FALL damage on landing (≈43 dmg from a ~45-block drop), which alone near-kills it and poisons
	// the pursuit test. Wait for it to land + heal isn't needed — just confirm it is on the ground and
	// alive before spawning the zombie. (Fall damage is itself vanilla-correct; we just don't want it
	// confounding the walk measurement.)
	for i := 0; i < 20; i++ {
		if st := c.State(); st.OnGround {
			break
		}
		if err := c.WaitTicks(ctx, 2); err != nil {
			t.Fatal(err)
		}
	}
	start := c.State()
	if st := c.State(); st.HealthSeen && st.Health <= 0 {
		t.Fatalf("bot already dead before spawn (health %.1f) — likely fall damage on a high spawn; the superflat boot or spawn-Y needs a fix", st.Health)
	}
	if err := c.Chat("/dbg zombie"); err != nil {
		t.Fatalf("/dbg zombie: %v", err)
	}
	// /dbg spawns the zombie ON the bot, which would melee it to death in a few ticks and turn the rest
	// of the run into "drag a corpse" (a dead player is not a valid melee target, so the zombie stops
	// chasing). Open a gap IMMEDIATELY: dash the bot +8 blocks before the zombie can land enough hits,
	// so the bot stays ALIVE and the zombie has to pursue. Then settle into the slow gait walk below.
	for d := 1; d <= 8; d++ {
		_ = c.MoveTo(ctx, start.X+float64(d), start.Y, start.Z)
		if err := c.WaitTicks(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	zid, ok := dbgSpawnedEID(c)
	if !ok {
		t.Fatalf("no [dbg] zombie spawn-eid ack (chat=%v)", c.RecentChat(10))
	}
	if st := c.State(); st.HealthSeen && st.Health <= 0 {
		t.Fatalf("bot died before the walk could start (health %.1f) — spawn-on-top killed it; needs a bigger initial gap or fresh playerdata", st.Health)
	}
	// Re-baseline the start AFTER the dash so the gait walk continues from where the bot now is.
	start = c.State()

	// melee reach (Mob.DEFAULT_ATTACK_REACH ≈ 0.828) plus the two half-widths (~0.3 player + ~0.3 zombie)
	// is ~1.4 blocks of center-to-center distance at which the zombie is "in reach" and standing is OK.
	const inReach = 1.6

	type sample struct {
		dispXZ   float64 // zombie center displacement since the previous sample
		dx       float64 // signed x-step (to detect oscillation)
		distBot  float64 // zombie→bot center distance at this sample
	}
	var samples []sample
	var prevX, prevZ float64
	havePrev := false

	// Walk the bot away from the zombie at ~3.3 blocks/sec (1 block every 6 ticks) — SLOWER than a
	// zombie's ground speed (~4.3 b/s) so a healthy pursuit CAN keep up; this makes "fell behind" a real
	// failure rather than the bot teleport-outrunning the mob. Sample twice per step to see the gait.
	for step := 0; step < 30; step++ {
		_ = c.MoveTo(ctx, start.X+float64(step+1), start.Y, start.Z)
		// ~2.2 b/s (1 block / 9 ticks) — SLOWER than the zombie's ground speed so a healthy pursuit
		// keeps up; sample once per step (the gait shows over the full window).
		if err := c.WaitTicks(ctx, 9); err != nil {
			t.Fatal(err)
		}
		z, alive := nearestPigByID(c, zid)
		if !alive {
			continue // briefly out of the snapshot table between move packets
		}
		bs := c.State()
		t.Logf("step=%2d bot.x=%.1f zombie=(%.1f,%.1f) dist=%.1f moves=%d", step, bs.X, z.X, z.Z, math.Hypot(z.X-bs.X, z.Z-bs.Z), z.MoveCount)
		s := sample{distBot: math.Hypot(z.X-bs.X, z.Z-bs.Z)}
		if havePrev {
			s.dispXZ = math.Hypot(z.X-prevX, z.Z-prevZ)
			s.dx = z.X - prevX
		}
		samples = append(samples, s)
		prevX, prevZ = z.X, z.Z
		havePrev = true
	}

	if len(samples) < 10 {
		t.Fatalf("too few zombie samples (%d) — zombie never tracked", len(samples))
	}

	// (a) NET APPROACH or KEEP-UP: the zombie should be roughly within striking distance by the end,
	// not left far behind. Use the median of the last 5 distances (robust to a single stale sample).
	tail := samples[len(samples)-5:]
	var tailDist []float64
	for _, s := range tail {
		tailDist = append(tailDist, s.distBot)
	}
	medTail := median(tailDist)

	// (b) NO LONG FREEZE: among samples where the zombie was OUT OF REACH, it should be MOVING most of
	// the time. Count out-of-reach samples and how many of them had ~0 displacement (a stall tick).
	outReach, stalls, jitterFlips := 0, 0, 0
	for i, s := range samples {
		if s.distBot > inReach {
			outReach++
			if s.dispXZ < 0.02 { // essentially no movement while it should be chasing
				stalls++
			}
		}
		// oscillation: x-step reverses direction vs the previous sample while moving a meaningful amount
		if i > 0 && samples[i-1].dx != 0 && s.dx != 0 {
			if (s.dx > 0) != (samples[i-1].dx > 0) && math.Abs(s.dx) > 0.05 && math.Abs(samples[i-1].dx) > 0.05 {
				jitterFlips++
			}
		}
	}
	stallFrac := 0.0
	if outReach > 0 {
		stallFrac = float64(stalls) / float64(outReach)
	}
	flipFrac := float64(jitterFlips) / float64(len(samples))

	t.Logf("samples=%d outOfReach=%d stalls=%d (%.0f%%) jitterFlips=%d (%.0f%%) medianTailDist=%.2f",
		len(samples), outReach, stalls, stallFrac*100, jitterFlips, flipFrac*100, medTail)

	if medTail > 4.0 {
		t.Errorf("zombie fell behind: median tail distance %.2f (want ≤4.0) — not keeping up", medTail)
	}
	if outReach >= 5 && stallFrac > 0.4 {
		t.Errorf("zombie FROZE %.0f%% of out-of-reach samples (want ≤40%%) — the 'se queda parado' stall", stallFrac*100)
	}
	if flipFrac > 0.4 {
		t.Errorf("zombie OSCILLATED on %.0f%% of samples (want ≤40%%) — jitter / 'tries to go back'", flipFrac*100)
	}
	if medTail <= 4.0 && stallFrac <= 0.4 && flipFrac <= 0.4 {
		t.Logf("PASS: smooth pursuit — keeps up (tail %.2f), no long freeze (%.0f%% stalls), no jitter (%.0f%% flips)",
			medTail, stallFrac*100, flipFrac*100)
	}
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sortFloat(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func sortFloat(s []float64) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
