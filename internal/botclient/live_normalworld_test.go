//go:build botlive

package botclient

import (
	"context"
	"testing"
	"time"
)

// TestLiveNormalWorldNoBackpressure reproduces the open "chunk backpressure with WORLD NORMAL (noise
// terrain)" issue: a player joining a NOISE-generated world is kicked (reason=backpressure) because the
// heavy worldgen streams chunks too slowly / the outbound queue fills. Superflat loads fine. This test
// joins, then holds for ~300 ticks and also walks a little (forcing new chunk columns to stream); a
// backpressure kick closes the connection and WaitTicks returns the fatal error. Passing means the
// player stayed connected through the join chunk flood + some movement.
//
// RUN AGAINST A NOISE WORLD: boot the server WITHOUT SULFUR_SUPERFLAT (default noise generator).
func TestLiveNormalWorldNoBackpressure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "normworldbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	// Survive the join chunk flood.
	if err := c.WaitTicks(ctx, 60); err != nil {
		t.Fatalf("kicked during join chunk flood (backpressure?): %v", err)
	}
	start := c.State()

	// Walk outward in a few directions to force new chunk columns to stream (the ongoing sender load).
	dirs := [][2]float64{{16, 0}, {0, 16}, {16, 16}, {32, 0}}
	for _, d := range dirs {
		_ = c.MoveTo(ctx, start.X+d[0], start.Y, start.Z+d[1])
		if err := c.WaitTicks(ctx, 40); err != nil {
			t.Fatalf("kicked while streaming new chunks at offset %v (backpressure?): %v", d, err)
		}
	}

	// Hold a while longer to be sure the steady-state traffic doesn't overflow.
	if err := c.WaitTicks(ctx, 80); err != nil {
		t.Fatalf("kicked in steady state (backpressure?): %v", err)
	}
	t.Logf("PASS: survived noise-world join + movement, no backpressure kick (final pos %.1f,%.1f)", c.State().X, c.State().Z)
}
