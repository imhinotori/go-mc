package server

// inventory_join.go is the GAMEPLAY-03 seam (Plan 17-01): the authoritative
// ContainerSetContent is sent exactly once per player, on the first tick after
// drainRegistrations registers them, so the client's inventory window 0 is populated on join
// (without this the window is empty until the player first clicks). sendContent already builds
// and sends the correct packet (inventory.go) and calls ensureInventory, so a nil inventory
// sends an empty-but-valid window — exactly what survival expects.
//
// The per-player bootstrapped flag guards the send so it fires once and is never re-sent on
// subsequent ticks. The call site (tickEntities -> syncJoinInventories) is wired in
// tick_phases.go; this file owns only the body. Tick-owned (TICK-05): runs on the owner over
// the tick-owned players slice + inventory.

// syncJoinInventories sends each not-yet-bootstrapped player its authoritative
// ContainerSetContent once, then marks it bootstrapped. Called from tickEntities each tick.
func (t *TickLoop) syncJoinInventories() {
	for _, p := range t.players {
		// Skip an already-synced player and a player without a connection (a test fixture or
		// a player mid-registration) — sendContent enqueues on p.client, so a nil client must
		// not be dereferenced, exactly the tracker's p.client==nil discipline.
		if p == nil || p.bootstrapped || p.client == nil {
			continue
		}
		t.sendContent(p)
		p.bootstrapped = true
	}
}
