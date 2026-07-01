//go:build botlive

package botclient

import (
	"context"
	"testing"
	"time"
)

// TestLiveFreshJoinHealth checks that a freshly-joined bot is at FULL health after settling on the
// ground — i.e. the join spawn does NOT drop the player from a height and deal fall damage. The walk
// test surfaced a bot arriving dead (43 fall damage), which would mean the server places the player
// entity above the floor and lets it fall before/instead of honoring the spawn position. Uses a fresh
// name so no stale playerdata .dat (a prior dead bot's hp 0) confounds the read.
func TestLiveFreshJoinHealth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "freshhpbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	// Let it settle (land + the first SetHealth arrive).
	for i := 0; i < 30; i++ {
		if err := c.WaitTicks(ctx, 2); err != nil {
			t.Fatal(err)
		}
		st := c.State()
		if st.OnGround && st.HealthSeen {
			break
		}
	}
	st := c.State()
	t.Logf("fresh join: pos.Y=%.2f onGround=%v health=%.1f healthSeen=%v", st.Y, st.OnGround, st.Health, st.HealthSeen)
	if st.HealthSeen && st.Health < 20 {
		t.Errorf("fresh-join bot at health %.1f (<20) — the spawn deals fall damage (player placed above the floor, not at the teleport Y)", st.Health)
	}
}
