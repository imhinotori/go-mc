//go:build botlive

package botclient

import (
	"context"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestLivePassiveMobsSpawnAndBehave is the Phase-34 live verification (in lieu of the AFK human
// checkpoint on plan 34-04): connect a bot, /dbg spawn cow / sheep / chicken, and confirm each
// actually appears in the running server with the RIGHT entity type, then wanders (the shared
// 8-goal stroll), and the server survives all four passive mobs. The cow-milk / sheep-shear interact
// PATHS are covered by the headless unit tests (TestMilkCow*, TestSheepShear*); this proves the mobs
// boot-load + spawn + behave live (the dogfood payoff: the same goal runtime the pig uses drives them).
func TestLivePassiveMobsSpawnAndBehave(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "passivebot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	if err := c.WaitTicks(ctx, 5); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		cmd    string
		typeID int32
		name   string
	}{
		{"/dbg cow", int32(entity.Cow.ID), "cow"},
		{"/dbg sheep", int32(entity.Sheep.ID), "sheep"},
		{"/dbg chicken", int32(entity.Chicken.ID), "chicken"},
	}

	for _, tc := range cases {
		if err := c.Chat(tc.cmd); err != nil {
			t.Fatalf("%s: %v", tc.cmd, err)
		}
		if err := c.WaitTicks(ctx, 12); err != nil {
			t.Fatal(err)
		}
		id, ok := dbgSpawnedEID(c)
		if !ok {
			t.Fatalf("%s: no [dbg] spawn-eid ack (chat=%v)", tc.name, c.RecentChat(10))
		}

		// The mob must exist with the RIGHT entity type.
		snap, alive := nearestPigByID(c, id) // id-based lookup; "pig" in the name is historical, works for any mob
		if !alive {
			t.Fatalf("%s eid=%d never appeared in the entity table", tc.name, id)
		}
		if snap.TypeID != tc.typeID {
			t.Fatalf("%s eid=%d has TypeID %d, want %d (wrong entity type spawned)", tc.name, id, snap.TypeID, tc.typeID)
		}
		t.Logf("%s eid=%d spawned with correct TypeID %d at (%.1f, %.1f, %.1f)", tc.name, id, snap.TypeID, snap.X, snap.Y, snap.Z)

		// It must WANDER (the shared WaterAvoidingRandomStrollGoal) — sample the farthest it strays from
		// its spawn point over a window. A jammed/dead mob (the 30.1 wedge bug) would never move.
		// The stroll goal's canUse is gated 1-in-reducedTickDelay(120)=60 per eval (a nextInt draw), so it
		// fires probabilistically — observe a generous window (~150 evals) so every mob reliably strolls at
		// least once, not just the lucky ones. A genuinely wedged mob (the 30.1 bug) never moves across it.
		start := [2]float64{snap.X, snap.Z}
		maxAway := 0.0
		for i := 0; i < 150; i++ {
			if err := c.WaitTicks(ctx, 2); err != nil {
				t.Fatal(err)
			}
			cur, ok := nearestPigByID(c, id)
			if !ok {
				t.Fatalf("%s eid=%d despawned mid-observation", tc.name, id)
			}
			if d := dist(start, [2]float64{cur.X, cur.Z}); d > maxAway {
				maxAway = d
			}
		}
		if maxAway < 0.5 {
			t.Errorf("%s eid=%d barely moved (max %.2f blocks from spawn) — stroll goal not driving it (wedge?)", tc.name, id, maxAway)
		} else {
			t.Logf("PASS: %s wandered up to %.2f blocks (stroll goal live)", tc.name, maxAway)
		}
	}

	// Server still alive + responsive after all 4 passive mobs (pig from earlier sessions + the 3 new).
	if err := c.Chat("/dbg pig"); err != nil {
		t.Fatalf("/dbg pig after the new mobs: %v", err)
	}
	if err := c.WaitTicks(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, ok := dbgSpawnedEID(c); !ok {
		t.Fatalf("pig spawn after new mobs failed — server may have wedged (chat=%v)", c.RecentChat(10))
	}
	t.Logf("PASS: all 4 vanilla passive mobs spawn + behave live; server survived")
}
