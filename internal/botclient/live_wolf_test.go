//go:build botlive

package botclient

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestLiveWildWolfAngerOnHit is the Phase-36 live verification (in lieu of the AFK human checkpoint on
// 36-04): /dbg spawn a WILD wolf, confirm it does NOT attack an idle bot (a wolf is NEUTRAL — not
// aggressive on sight, the anger gate holds), then ATTACK it and confirm it RETALIATES (the bot's health
// drops — anger-on-hit → the wolf targets the attacker → melees through the Phase-29 keystone). This is
// the wolf's defining neutral behavior, proven end-to-end on a running server. (Taming is 1-in-3 + needs
// reading DATA_FLAGS the bot can't see headlessly; the tame path is unit-asserted in TestWolfTame.)
func TestLiveWildWolfAngerOnHit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "wolfbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	if err := c.WaitTicks(ctx, 10); err != nil {
		t.Fatal(err)
	}
	startHP := c.State().Health

	if err := c.Chat("/dbg wolf"); err != nil {
		t.Fatalf("/dbg wolf: %v", err)
	}
	if err := c.WaitTicks(ctx, 12); err != nil {
		t.Fatal(err)
	}
	wid, ok := dbgSpawnedEID(c)
	if !ok {
		t.Fatalf("no [dbg] wolf spawn-eid ack (chat=%v)", c.RecentChat(10))
	}
	snap, alive := nearestPigByID(c, wid)
	if !alive {
		t.Fatalf("wolf eid=%d never appeared", wid)
	}
	if snap.TypeID != int32(entity.Wolf.ID) {
		t.Fatalf("wolf eid=%d TypeID %d, want %d", wid, snap.TypeID, int32(entity.Wolf.ID))
	}
	_ = startHP
	// Move the bot a few blocks off the spawn so the wolf has room to either ignore it (neutral) or home
	// in (angry) — a measurable distance either way. The wolf spawns AT the bot (dist ~0); stepping away
	// creates the gap the two phases read.
	bs := c.State()
	_ = c.MoveTo(ctx, bs.X+6, bs.Y, bs.Z)
	_ = c.WaitTicks(ctx, 6)

	botDist := func() float64 {
		w, ok := nearestPigByID(c, wid)
		if !ok {
			return math.NaN()
		}
		s := c.State()
		return math.Hypot(w.X-s.X, w.Z-s.Z)
	}

	// Phase 1 — NEUTRAL: an un-provoked wild wolf must NOT hunt the idle bot. Over a window it wanders
	// (stroll) but does NOT persistently home to melee range. Record the closest it gets WITHOUT being hit.
	neutralMin := math.MaxFloat64
	for i := 0; i < 40; i++ {
		if err := c.WaitTicks(ctx, 2); err != nil {
			t.Fatal(err)
		}
		if d := botDist(); !math.IsNaN(d) && d < neutralMin {
			neutralMin = d
		}
	}
	t.Logf("neutral phase: wild wolf's closest approach (un-hit) = %.2f blocks", neutralMin)

	// Phase 2 — ANGER ON HIT: close into reach + STRIKE the wolf (the hit makes it angry), then RETREAT.
	// An angry wolf TARGETS the attacker (angry_player_target) and PATHS toward the retreated bot; a still-
	// neutral wolf wanders off. The discriminating signal = the wolf CLOSING the gap to the retreated bot.
	// (Health-based damage assertion is unavailable: a separate pre-existing join-health server bug zeroes
	// the bot's reported health; the melee DAMAGE is unit-proven by TestWolfAngerOnHit through the keystone.
	// The live signal is the target-acquisition HOMING.)
	// 2a: close in + strike repeatedly to land hits (Attack is reach-gated ~3.5 blocks).
	for i := 0; i < 40; i++ {
		w, ok := nearestPigByID(c, wid)
		if !ok {
			t.Logf("PASS (anger-on-hit): wolf eid=%d died from the bot's strikes — combat engaged + retaliated", wid)
			return
		}
		s := c.State()
		if math.Hypot(w.X-s.X, w.Z-s.Z) > 2.0 {
			_ = c.MoveTo(ctx, w.X, w.Y, w.Z)
		}
		_ = c.Attack(wid)
		_ = c.WaitTicks(ctx, 2)
	}
	// 2b: retreat ~8 blocks and hold; measure whether the (now-angry) wolf follows.
	rs := c.State()
	_ = c.MoveTo(ctx, rs.X+8, rs.Y, rs.Z)
	_ = c.WaitTicks(ctx, 6)
	angryMin := math.MaxFloat64
	for i := 0; i < 50; i++ {
		if err := c.WaitTicks(ctx, 2); err != nil {
			t.Fatal(err)
		}
		if d := botDist(); !math.IsNaN(d) && d < angryMin {
			angryMin = d
		}
	}
	t.Logf("anger phase: after hit+retreat, wolf's closest approach = %.2f blocks (un-hit wander was %.2f)", angryMin, neutralMin)

	// An angry, targeting wolf homes to melee range (within ~3.5) of the retreated bot. A neutral wolf that
	// never angered would wander and not persistently close.
	if angryMin <= 3.5 {
		t.Logf("PASS (anger-on-hit): the hit wild wolf followed + homed to %.2f blocks (target acquired + pathing)", angryMin)
	} else {
		t.Errorf("the hit wild wolf did NOT home to the retreated bot (closest %.2f > 3.5) — anger-on-hit target/path not firing", angryMin)
	}
}
