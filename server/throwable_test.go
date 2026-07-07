package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/world"
)

// TestItemToThrowableKind maps the three throwable items to their kinds and rejects non-throwables.
func TestItemToThrowableKind(t *testing.T) {
	cases := map[int32]int{
		int32(item.Snowball.ID):   throwSnowball,
		int32(item.Egg.ID):        throwEgg,
		int32(item.EnderPearl.ID): throwEnderPearl,
	}
	for id, want := range cases {
		if got, ok := itemToThrowableKind(id); !ok || got != want {
			t.Fatalf("itemToThrowableKind(%d) = (%d,%v), want (%d,true)", id, got, ok, want)
		}
	}
	if _, ok := itemToThrowableKind(int32(item.Stone.ID)); ok {
		t.Fatalf("stone should not be a throwable")
	}
}

// TestThrowableFliesAndLands: a spawned snowball arcs (gravity pulls vy down, drag scales the velocity)
// and, over enough ticks with no target, either lands on a block or despawns — never runs forever.
func TestThrowableFliesAndLands(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	ow := world.NewChunkManager()
	for _, r := range loop.regions {
		r.entities = newEntityStore()
		r.world = ow
	}
	// Launch a snowball horizontally from high up over open air.
	e := loop.spawnThrowable(999, throwSnowball, 8.0, 100.0, 8.0, 1.0, 0.0, 0.0)
	startVX, startVY := e.vx, e.vy

	loop.withRegion(loop.regions[globalRegion], func() { loop.tickThrowable(e) })
	// After one tick: gravity applied (vy dropped by ~0.03 then *0.99 drag), vx scaled by drag (~0.99).
	if e.vy >= startVY {
		t.Fatalf("vy did not drop under gravity: start=%.4f now=%.4f", startVY, e.vy)
	}
	if e.vx >= startVX {
		t.Fatalf("vx did not decay under drag: start=%.4f now=%.4f", startVX, e.vx)
	}
	// It should not still exist forever: drive up to the despawn cap; it must be gone (landed or aged).
	present := true
	for i := 0; i < throwDespawnTicks+5 && present; i++ {
		loop.withRegion(loop.regions[globalRegion], func() { loop.tickThrowable(e) })
		_, present = loop.regions[globalRegion].entities.get(e.id)
	}
	if present {
		t.Fatalf("snowball never landed or despawned after %d ticks", throwDespawnTicks+5)
	}
}
