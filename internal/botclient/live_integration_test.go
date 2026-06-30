//go:build botlive

// Live end-to-end verification of the bot against a RUNNING Sulfur server. Opt-in via the
// `botlive` build tag so it never runs in the normal suite (it needs a live server on
// SULFUR_BOT_ADDR, default 127.0.0.1:25577). Run:
//
//	SULFUR_SUPERFLAT=1 SULFUR_TEST_KIT=1 ./sulfur -addr 127.0.0.1:25577 &
//	CGO_ENABLED=0 go test -tags botlive ./internal/botclient/ -run TestLive -v -timeout 120s
package botclient

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
)

func liveAddr() string {
	if a := os.Getenv("SULFUR_BOT_ADDR"); a != "" {
		return a
	}
	return "127.0.0.1:25577"
}

// TestLiveSpawnAndObservePig is the core verification loop: connect, spawn a vanilla pig via the
// /dbg command, wait, then assert the pig appears in the entity table AND has moved (MoveCount > 0)
// — proving it spawned, is alive, and its AI (stroll/look) is driving it. This is the headless
// stand-in for the operator's in-game play-test of Phase 30.1 (the pig wanders, no wedge).
func TestLiveSpawnAndObservePig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	c := &Client{}
	if err := c.Connect(ctx, liveAddr(), "verifierbot"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	st := c.State()
	if !st.Connected {
		t.Fatalf("not connected after Connect")
	}
	t.Logf("connected at (%.1f,%.1f,%.1f)", st.X, st.Y, st.Z)

	// Spawn a pig at the bot via the /dbg command (gated on command.tp, which v1 grants everyone).
	if err := c.Chat("/dbg pig"); err != nil {
		t.Fatalf("chat /dbg pig: %v", err)
	}
	if err := c.WaitTicks(ctx, 10); err != nil {
		t.Fatalf("wait after spawn: %v", err)
	}

	// The [dbg] ack arrives as a SystemChat: "[dbg] spawned pig eid=N at (...)".
	acks := c.RecentChat(20)
	t.Logf("recent chat: %v", acks)
	sawAck := false
	for _, m := range acks {
		if strings.Contains(m, "[dbg]") && strings.Contains(m, "pig") {
			sawAck = true
		}
	}
	if !sawAck {
		t.Errorf("no [dbg] pig spawn ack in chat — /dbg pig may have failed (chat=%v)", acks)
	}

	// Let the pig's AI drive it for ~5s so stroll/look produce move + head-rot packets.
	if err := c.WaitTicks(ctx, 100); err != nil {
		t.Fatalf("wait for AI: %v", err)
	}

	ents := c.Entities()
	t.Logf("entity table (%d):", len(ents))
	var pig *EntitySnapshot
	for i := range ents {
		e := ents[i]
		t.Logf("  id=%d type=%d pos=(%.1f,%.1f,%.1f) moves=%d headrot=%v",
			e.ID, e.TypeID, e.X, e.Y, e.Z, e.MoveCount, e.SawHeadRot)
		if e.TypeID == int32(entity.Pig.ID) {
			pig = &ents[i]
		}
	}
	if pig == nil {
		t.Fatalf("no pig (type %d) in the entity table after /dbg pig — spawn/observe failed", int32(entity.Pig.ID))
	}
	// The pig must have MOVED — proves it's alive and its AI is ticking (the Phase 30.1 fix: it
	// wanders instead of wedging). A wedged/dead pig would have MoveCount 0 after 100 ticks.
	if pig.MoveCount == 0 {
		t.Errorf("pig eid=%d never moved over 100 ticks (MoveCount=0) — wedged or AI not driving it", pig.ID)
	} else {
		t.Logf("PASS: pig eid=%d moved %d times (alive + wandering)", pig.ID, pig.MoveCount)
	}
}
