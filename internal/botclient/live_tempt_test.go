//go:build botlive

package botclient

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
)

// dbgSpawnedEID parses the "[dbg] spawned pig eid=N at (...)" SystemChat ack so the test tracks the
// EXACT pig /dbg created, ignoring any natural-spawn pigs roaming the world.
func dbgSpawnedEID(c *Client) (int32, bool) {
	// Scan NEWEST-first so repeated /dbg spawns (a test that spawns several mobs in sequence) read the
	// LATEST spawn ack, not the first stale one still in the chat ring.
	recent := c.RecentChat(30)
	for k := len(recent) - 1; k >= 0; k-- {
		m := recent[k]
		i := strings.Index(m, "eid=")
		if i < 0 || !strings.Contains(m, "[dbg]") {
			continue
		}
		rest := m[i+4:]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j == 0 {
			continue
		}
		if n, err := strconv.Atoi(rest[:j]); err == nil {
			return int32(n), true
		}
	}
	return 0, false
}

// TestLiveTemptGoalFollowsCarrot is the live verification of Phase 32: the bot holds a carrot
// (pig_food, hotbar slot 2 in the test kit), spawns a pig, then stands still — a tempted pig should
// navigate TOWARD the bot and close the distance (TemptGoal@4, speed 1.2, stopDistance 2.5). The
// headless stand-in for the operator holding a carrot and watching a pig come running.
func TestLiveTemptGoalFollowsCarrot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "temptbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	// Hold the carrot: hotbar index 2 (test-kit slot 38 = pig_food).
	if err := c.SelectSlot(2); err != nil {
		t.Fatalf("select carrot slot: %v", err)
	}
	if err := c.WaitTicks(ctx, 5); err != nil {
		t.Fatal(err)
	}

	if err := c.Chat("/dbg pig"); err != nil {
		t.Fatalf("/dbg pig: %v", err)
	}
	if err := c.WaitTicks(ctx, 10); err != nil {
		t.Fatal(err)
	}
	// Track the EXACT pig /dbg just spawned (the chat ack carries "spawned pig eid=N"), so other
	// natural-spawn pigs in the world don't confuse the measurement.
	pigID, ok := dbgSpawnedEID(c)
	if !ok {
		t.Fatalf("no [dbg] spawn-eid ack (chat=%v)", c.RecentChat(10))
	}

	// The pig spawns AT the bot, then strolls. Wait for it to wander a bit AWAY (so "comes back to the
	// carrot" is a meaningful signal), recording the farthest it gets, then measure whether holding the
	// carrot pulls it back in.
	maxAway := 0.0
	for i := 0; i < 40; i++ {
		if err := c.WaitTicks(ctx, 2); err != nil {
			t.Fatal(err)
		}
		bs := c.State()
		pp := pigPos(c, pigID)
		d := math.Hypot(pp[0]-bs.X, pp[1]-bs.Z)
		if d > maxAway {
			maxAway = d
		}
	}
	t.Logf("pig wandered up to %.2f blocks away while the bot holds the carrot", maxAway)

	// Hold still and let TemptGoal pull the pig in. Sample the CLOSEST the pig gets over a steady-state
	// window (robust against a single snapshot landing mid-step) — a tempted pig homes to stopDistance
	// (2.5) and stays there, so its minimum distance must be small.
	minNear := math.MaxFloat64
	for i := 0; i < 60; i++ {
		if err := c.WaitTicks(ctx, 2); err != nil {
			t.Fatal(err)
		}
		if _, alive := nearestPigByID(c, pigID); !alive {
			t.Fatalf("pig eid=%d despawned during the test", pigID)
		}
		bs := c.State()
		pp := pigPos(c, pigID)
		if d := math.Hypot(pp[0]-bs.X, pp[1]-bs.Z); d < minNear {
			minNear = d
		}
	}
	t.Logf("closest the carrot-held pig got: %.2f blocks (had strayed up to %.2f)", minNear, maxAway)

	// A tempted pig must come CLOSE — within ~5 blocks (stopDistance 2.5 + nav/step slack). A pig that
	// is NOT tempted (TemptGoal not pulling it) would stroll freely past TEMPT_RANGE (10) and never home.
	if minNear > 5.0 {
		t.Errorf("pig never came to the carrot: closest %.2f > 5.0 (TemptGoal not pulling it in)", minNear)
	} else {
		t.Logf("PASS: pig tempted to within %.2f blocks by the held carrot", minNear)
	}
	_ = entity.Pig
}
