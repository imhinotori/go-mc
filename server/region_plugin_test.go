package server

import (
	"sync/atomic"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world"
)

// region_plugin_test.go — Phase-27 STEP-3 Task-2 gates: THE REGION-01 GATE (a declared-mob goal
// callback for an entity in region R runs on R's goroutine and re-resolves R's store) and the
// cross-region tracker (a player near the seam sees an entity in the adjacent region).

// columnInRegion finds a chunk column owned by the given region near the origin (the test seam
// picker). It fails the test if none is found in a small window.
func columnInRegion(t *testing.T, want regionID) level.ChunkPos {
	t.Helper()
	for x := int32(0); x <= 16; x++ {
		for z := int32(0); z <= 16; z++ {
			c := level.ChunkPos{x, z}
			if regionOf(c) == want {
				return c
			}
		}
	}
	t.Fatalf("no column maps to region %d in the search window", want)
	return level.ChunkPos{}
}

// newRegionPluginLoop builds a physics loop (2 regions, shared world) with a floor laid across the
// chunks both regions' test columns use, and the wanderer mob registry installed.
func newRegionPluginLoop(t *testing.T, cols ...level.ChunkPos) (*TickLoop, *world.ChunkManager) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	// Wire the shared world directly into EVERY region (no off-tick worker — like newPhysicsLoop —
	// to avoid SetWorld's worker.Results() goroutine; the physics/AI only READ the world).
	for _, r := range loop.regions {
		r.world = mgr
	}
	const floorY = 64
	for _, c := range cols {
		ch := putChunk(mgr, c)
		fillFloor(ch, floorY)
	}
	installVanillaPigRegistry(loop)
	return loop, mgr
}

// spawnDeclaredMobInRegion spawns a declared mob and places it (via the store) into the given
// region's store at a column that region owns — the test seam for "a mob owned by region R".
func spawnDeclaredMobInRegion(t *testing.T, loop *TickLoop, decl *mobDecl, col level.ChunkPos, want regionID) *Entity {
	t.Helper()
	x := float64(col[0])*16 + 8.5
	z := float64(col[1])*16 + 8.5
	// Build via spawnDeclaredMob (lands in globalRegion's store on the test goroutine), then move it
	// into the OWNING region's store so it is genuinely region-R-owned for the gate.
	e := loop.spawnDeclaredMob(decl, x, 65.0, z)
	dest := loop.regions[want]
	if src := loop.owningRegion(e.id); src != nil && src != dest {
		src.entities.remove(e.id)
		dest.entities.add(e)
	}
	if _, ok := dest.entities.get(e.id); !ok {
		t.Fatalf("setup: mob not in region %d after placement", want)
	}
	return e
}

// TestPluginHookRunsOnOwningRegion is THE REGION-01 GATE: a declared mob in region R, when its goal
// callback fires, (1) re-resolves against R's store (the mob is found there) AND (2) the callback
// runs on R's tick goroutine (NOT the coordinator's, NOT region 0's). The frozen registry is shared
// safely; only the handles resolve per-region.
func TestPluginHookRunsOnOwningRegion(t *testing.T) {
	col1 := columnInRegion(t, 1)
	loop, _ := newRegionPluginLoop(t, col1)

	r := loadMobRegistry(t, mobpluginsRoot)
	decl := r.byName["wanderer"]
	mob := spawnDeclaredMobInRegion(t, loop, decl, col1, 1)

	// Capture each region's tick goroutine id (set on the region goroutine via the tickHook).
	regionGID := make([]atomic.Int64, len(loop.regions))
	for i := range loop.regions {
		i := i
		loop.regions[i].tickHook = func() { regionGID[i].Store(curGoroutineID()) }
	}

	// Capture, from inside the goal callback, the goroutine it ran on + whether the mob resolves in
	// region 1's store at that moment.
	var callbackGID atomic.Int64
	var sawMobInRegion1 atomic.Bool
	var fired atomic.Bool
	loop.onGoalCall = func(e *Entity) {
		if e.id != mob.id {
			return
		}
		fired.Store(true)
		callbackGID.Store(curGoroutineID())
		if _, ok := loop.regions[1].entities.get(e.id); ok {
			sawMobInRegion1.Store(true)
		}
	}

	// Drive ticks until the goal callback fires (the MOVE goal must claim its flag + run).
	for i := 0; i < 50 && !fired.Load(); i++ {
		loop.tickOnce()
	}

	if !fired.Load() {
		t.Fatal("the declared mob's goal callback never fired across 50 ticks (the gate could not be exercised)")
	}
	if !sawMobInRegion1.Load() {
		t.Fatal("the goal callback fired but the mob did NOT resolve in region 1's store (the handle did not bind the owning region)")
	}
	// The callback must have run on region 1's tick goroutine — NOT region 0's, NOT the coordinator's.
	cb := callbackGID.Load()
	r1 := regionGID[1].Load()
	r0 := regionGID[0].Load()
	if cb != r1 {
		t.Fatalf("the goal callback ran on goroutine %d; want region 1's tick goroutine %d (the hook for an entity in R must run on R's goroutine)", cb, r1)
	}
	if cb == r0 {
		t.Fatal("the goal callback ran on region 0's goroutine — a mob in region 1 must run on region 1's goroutine, not region 0's")
	}

	for i := range loop.regions {
		loop.regions[i].tickHook = nil
	}
	loop.onGoalCall = nil
}

// TestTrackerSeesAcrossRegions proves the cross-region tracker (Pitfall 2): a player standing near
// the region seam, with an entity in the ADJACENT region within pickupMergeScanChunks, SEES that entity (the
// tracker's near() spans both regions at the barrier). It asserts the cross-region broad-phase
// (entitiesNearAcrossRegions) returns the seam-adjacent entity.
func TestTrackerSeesAcrossRegions(t *testing.T) {
	// Two adjacent columns in different regions (a real seam).
	var colA, colB level.ChunkPos
	found := false
	for x := int32(0); x <= 8 && !found; x++ {
		for z := int32(0); z <= 8 && !found; z++ {
			a := level.ChunkPos{x, z}
			b := level.ChunkPos{x + 1, z}
			if regionOf(a) != regionOf(b) {
				colA, colB = a, b
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no adjacent cross-region seam found")
	}

	loop := NewTickLoop(newFakeClock())
	regA := loop.regions[regionOf(colA)]
	regB := loop.regions[regionOf(colB)]

	// A player sits at the EAST edge of colA (close to the colB seam); an entity sits at the WEST edge
	// of colB — within pickupMergeScanChunks (6 columns) horizontally. They are in DIFFERENT regions.
	playerX := float64(colA[0])*16 + 15.5
	playerZ := float64(colA[1])*16 + 8.5
	mobX := float64(colB[0])*16 + 0.5
	mobZ := float64(colB[1])*16 + 8.5

	mob := testEntity(7, entity.SulfurCube, mobX, 65.0, mobZ)
	regB.entities.add(mob)

	// The cross-region broad-phase from the player's position must INCLUDE the adjacent-region mob.
	near := loop.entitiesNearAcrossRegions(playerX, playerZ, pickupMergeScanChunks)
	saw := false
	for _, e := range near {
		if e.id == mob.id {
			saw = true
		}
	}
	if !saw {
		t.Fatal("the cross-region tracker broad-phase did NOT see the entity in the adjacent region — a player near the seam must see across the boundary")
	}

	// And a single-region near() (the OLD behavior) would MISS it — confirm the seam genuinely splits
	// the two: regA's own near() does not contain the colB mob (it lives in regB's store).
	if _, ok := regA.entities.get(mob.id); ok {
		t.Fatal("setup invalid: the mob must live in region B's store, not region A's")
	}
}
