//go:build botlive

package botclient

import (
	"context"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestLiveBreedingSpawnsBaby is the live Phase-33 DOGFOOD GATE verification: the bot holds a carrot
// (pig_food), spawns two pigs, feeds each (right-click = ServerboundInteract), then waits — two
// in-love adults in range should breed and a THIRD (baby) pig appears in the entity table. The
// headless stand-in for the operator watching two fed pigs make a baby.
func TestLiveBreedingSpawnsBaby(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "breedbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	// Hold the carrot (hotbar slot 2 = test-kit slot 38 = pig_food).
	if err := c.SelectSlot(2); err != nil {
		t.Fatalf("select carrot: %v", err)
	}
	if err := c.WaitTicks(ctx, 5); err != nil {
		t.Fatal(err)
	}

	// Spawn two pigs at the bot (the /dbg pig command drops one each call, at the bot's feet).
	if err := c.Chat("/dbg pig"); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitTicks(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err := c.Chat("/dbg pig"); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitTicks(ctx, 10); err != nil {
		t.Fatal(err)
	}

	// Collect the pigs nearest the bot — the two we just spawned (ignore far natural-spawn pigs).
	st := c.State()
	var near []int32
	for _, e := range c.Entities() {
		if e.TypeID != int32(entity.Pig.ID) {
			continue
		}
		dx, dz := e.X-st.X, e.Z-st.Z
		if dx*dx+dz*dz < 36 { // within 6 blocks of the bot
			near = append(near, e.ID)
		}
	}
	t.Logf("pigs near the bot before breeding: %v", near)
	if len(near) < 2 {
		t.Fatalf("expected >=2 pigs near the bot after two /dbg pig, got %d (chat=%v)", len(near), c.RecentChat(8))
	}
	beforeCount := countNearPigs(c, st.X, st.Z)

	// Feed each pig (right-click with the carrot held) → each falls in love.
	for _, id := range near[:2] {
		if err := c.Interact(id); err != nil {
			t.Fatalf("interact/feed pig %d: %v", id, err)
		}
		if err := c.WaitTicks(ctx, 3); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("fed pigs %v — waiting for breeding", near[:2])

	// Two in-love adults near each other breed at loveTime>=60 (~3s). Wait generously and watch for a
	// NEW pig (the baby) appearing near the bot.
	bred := false
	for i := 0; i < 80 && !bred; i++ {
		if err := c.WaitTicks(ctx, 5); err != nil {
			t.Fatal(err)
		}
		if countNearPigs(c, st.X, st.Z) > beforeCount {
			bred = true
		}
	}
	afterCount := countNearPigs(c, st.X, st.Z)
	t.Logf("near-pig count: before=%d after=%d", beforeCount, afterCount)

	if !bred {
		t.Errorf("no baby pig appeared after feeding two pigs (count stayed %d) — breeding did not fire", afterCount)
	} else {
		t.Logf("PASS: a baby pig spawned (count %d -> %d) — live breeding confirmed", beforeCount, afterCount)
	}
}

func countNearPigs(c *Client, x, z float64) int {
	n := 0
	for _, e := range c.Entities() {
		if e.TypeID != int32(entity.Pig.ID) {
			continue
		}
		dx, dz := e.X-x, e.Z-z
		if dx*dx+dz*dz < 100 { // within 10 blocks
			n++
		}
	}
	return n
}
