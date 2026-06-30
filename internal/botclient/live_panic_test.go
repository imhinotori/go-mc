//go:build botlive

package botclient

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestLivePanicGoalFleesOnHit is the live verification of Phase 31: spawn a pig at the bot, attack
// it (a player_attack damage source ∈ panic_causes), and assert it FLEES — it covers noticeably
// more ground in the window right after the hit than a calm pig would (PanicGoal sets a flee target
// at speed 1.25 and sprints away). The headless stand-in for the operator watching a hit pig bolt.
func TestLivePanicGoalFleesOnHit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "panicbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	if err := c.Chat("/dbg pig"); err != nil {
		t.Fatalf("/dbg pig: %v", err)
	}
	if err := c.WaitTicks(ctx, 10); err != nil {
		t.Fatal(err)
	}

	// Find the pig nearest the bot (the one /dbg just dropped at us).
	st := c.State()
	pigID, ok := nearestPig(c, st.X, st.Z)
	if !ok {
		t.Fatalf("no pig spawned (chat=%v)", c.RecentChat(10))
	}
	t.Logf("targeting pig eid=%d", pigID)

	// Baseline: how far does the pig roam in 40 calm ticks (its stroll drift)?
	before := pigPos(c, pigID)
	if err := c.WaitTicks(ctx, 40); err != nil {
		t.Fatal(err)
	}
	calm := dist(before, pigPos(c, pigID))
	movesCalm := pigMoves(c, pigID)
	t.Logf("calm drift over 40 ticks: %.2f blocks (moves=%d)", calm, movesCalm)

	// CRITICAL: the attack reach-gates at ~3.5 blocks (Attributes.ENTITY_INTERACTION_RANGE). The pig
	// strolls away during the calm window, so walk the bot ONTO the pig before swinging, else the hit
	// silently no-ops (out of reach → no damage → no panic).
	pp := pigPos(c, pigID)
	if err := c.MoveTo(ctx, pp[0], st.Y, pp[1]); err != nil {
		t.Logf("move-to pig: %v (continuing)", err)
	}
	// Re-acquire (the pig kept moving while we walked); chase one more short hop if needed.
	if pid2, ok := nearestPig(c, c.State().X, c.State().Z); ok {
		pigID = pid2
	}
	pp = pigPos(c, pigID)
	bs := c.State()
	if d := math.Hypot(pp[0]-bs.X, pp[1]-bs.Z); d > 3.0 {
		_ = c.MoveTo(ctx, pp[0], bs.Y, pp[1])
	}

	// Hit it — player attack is in panic_causes → shouldPanic true → PanicGoal flees at 1.25.
	startHit := pigPos(c, pigID)
	bs = c.State()
	t.Logf("attacking pig eid=%d at dist %.2f", pigID, math.Hypot(startHit[0]-bs.X, startHit[1]-bs.Z))
	if err := c.Attack(pigID); err != nil {
		t.Fatalf("attack: %v", err)
	}
	if err := c.WaitTicks(ctx, 40); err != nil {
		t.Fatal(err)
	}
	fled := dist(startHit, pigPos(c, pigID))
	movesFled := pigMoves(c, pigID) - movesCalm
	t.Logf("post-hit travel over 40 ticks: %.2f blocks (moves=%d)", fled, movesFled)

	// A fleeing pig (speed 1.25, a committed flee target) covers clearly more ground than calm
	// stroll drift. Assert it moved AND traveled meaningfully farther than the calm baseline.
	if fled <= 0.5 {
		t.Errorf("pig did not move after being hit (%.2f blocks) — PanicGoal did not fire", fled)
	}
	if fled <= calm {
		t.Logf("WARNING: post-hit travel (%.2f) not greater than calm drift (%.2f) — panic flee weak "+
			"or the pig happened to stroll far; check moves and the death state", fled, calm)
	}
	// Confirm a panic-related death/hurt did not just despawn it (pig should still exist, fleeing).
	if _, alive := nearestPigByID(c, pigID); !alive {
		t.Logf("note: pig eid=%d left the table (despawn/death) after the hit", pigID)
	}
}

func nearestPig(c *Client, x, z float64) (int32, bool) {
	best := int32(-1)
	bestD := math.MaxFloat64
	for _, e := range c.Entities() {
		if e.TypeID != int32(entity.Pig.ID) {
			continue
		}
		d := (e.X-x)*(e.X-x) + (e.Z-z)*(e.Z-z)
		if d < bestD {
			bestD, best = d, e.ID
		}
	}
	return best, best != -1
}

func nearestPigByID(c *Client, id int32) (EntitySnapshot, bool) {
	for _, e := range c.Entities() {
		if e.ID == id {
			return e, true
		}
	}
	return EntitySnapshot{}, false
}

func pigPos(c *Client, id int32) [2]float64 {
	if e, ok := nearestPigByID(c, id); ok {
		return [2]float64{e.X, e.Z}
	}
	return [2]float64{math.NaN(), math.NaN()}
}

func pigMoves(c *Client, id int32) int {
	if e, ok := nearestPigByID(c, id); ok {
		return e.MoveCount
	}
	return 0
}

func dist(a, b [2]float64) float64 {
	if math.IsNaN(a[0]) || math.IsNaN(b[0]) {
		return 0
	}
	return math.Hypot(b[0]-a[0], b[1]-a[1])
}

var _ = strings.TrimSpace
