package server

// portal_forcer_test.go -- behaviour tests for the PortalForcer port (portal_forcer.go): an existing
// destination portal within the radius is REUSED (findClosestPortalPosition); with no portal a fresh 4x5
// obsidian frame is BUILT (createPortal); a created/lit portal is INDEXED in the dimension POI (so a
// return trip can find it); and a round trip nether<->overworld resolves back to the same portal. The
// PortalForcer draws ZERO from any pig/gameplay RNG stream, so the pig oracle is untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// readyNetherChunk inserts a loaded all-air nether chunk (8 sections, minY 0) at col so SetBlock/GetBlock
// resolve in the nether world.
func readyNetherChunk(m *world.ChunkManager, col level.ChunkPos) {
	ch := level.EmptyChunk(dimNetherSecs)
	ch.Status = level.StatusFull
	m.Insert(col, ch)
}

// TestFindClosestPortalReusesExisting: an existing nether_portal within the 16-block nether radius is
// found and reused (its column returned), NOT rebuilt. CITE: PortalForcer.findClosestPortalPosition.
func TestFindClosestPortalReusesExisting(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	nether := world.NewChunkManager()
	loop.netherWorld = nether
	readyNetherChunk(nether, level.ChunkPos{0, 0})

	// Place a real nether_portal block + index it in the nether POI (as chunk-load / create would).
	portalPos := pk.Position{X: 6, Y: 64, Z: 4}
	portalState := block.ToStateID[block.NetherPortal{Axis: block.X}]
	if !nether.SetBlock(portalPos, portalState, dimNetherMinY) {
		t.Fatalf("failed to place nether_portal block for the test")
	}
	loop.dimPoiManager(dimNether).add(portalPos, poiTypeNetherPortal)

	g := loop.portalDimGeomFor(dimNether)
	// Search from a nearby column (within 16 blocks).
	got, ok := loop.findClosestPortalPosition(g, pk.Position{X: 8, Y: 64, Z: 8}, true)
	if !ok {
		t.Fatalf("findClosestPortalPosition found nothing; expected the existing portal at %v", portalPos)
	}
	if got != portalPos {
		t.Fatalf("findClosestPortalPosition = %v, want the existing portal %v", got, portalPos)
	}
}

// TestFindClosestPortalRadius: a portal OUTSIDE the 16-block nether radius is NOT found (the getInSquare
// radius bound). CITE: PortalForcer.findClosestPortalPosition (NETHER_PORTAL_RADIUS=16).
func TestFindClosestPortalRadius(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	nether := world.NewChunkManager()
	loop.netherWorld = nether
	readyNetherChunk(nether, level.ChunkPos{0, 0})
	readyNetherChunk(nether, level.ChunkPos{1, 0})
	readyNetherChunk(nether, level.ChunkPos{2, 0})

	portalPos := pk.Position{X: 40, Y: 64, Z: 0} // 40 blocks away -> outside radius 16
	portalState := block.ToStateID[block.NetherPortal{Axis: block.X}]
	nether.SetBlock(portalPos, portalState, dimNetherMinY)
	loop.dimPoiManager(dimNether).add(portalPos, poiTypeNetherPortal)

	g := loop.portalDimGeomFor(dimNether)
	if _, ok := loop.findClosestPortalPosition(g, pk.Position{X: 0, Y: 64, Z: 0}, true); ok {
		t.Fatal("findClosestPortalPosition found a portal 40 blocks away (radius is 16)")
	}
}

// TestCreatePortalBuildsFrame: with a solid floor and open air above (a canHostFrame column) and NO
// existing portal, createPortal builds a 4x5 obsidian frame with a 2x3 nether_portal interior at the
// closest hostable spot. CITE: PortalForcer.createPortal.
func TestCreatePortalBuildsFrame(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	nether := world.NewChunkManager()
	loop.netherWorld = nether
	// Lay a plain of netherrack (solid) at y=63 across the origin chunks, all air above -> every column can
	// host a frame. Use several chunks so the spiral has room.
	for _, cx := range []int32{-1, 0, 1} {
		for _, cz := range []int32{-1, 0, 1} {
			readyNetherChunk(nether, level.ChunkPos{cx, cz})
		}
	}
	netherrack := block.ToStateID[block.Netherrack{}]
	floorY := 63
	for x := -16; x <= 16; x++ {
		for z := -16; z <= 16; z++ {
			nether.SetBlock(pk.Position{X: x, Y: floorY, Z: z}, netherrack, dimNetherMinY)
		}
	}

	g := loop.portalDimGeomFor(dimNether)
	exit := pk.Position{X: 4, Y: 64, Z: 4}
	bottomLeft, axis, ok := loop.createPortal(g, exit, block.X)
	if !ok {
		t.Fatalf("createPortal reported failure; expected a built frame")
	}
	if axis != block.X {
		t.Fatalf("createPortal axis = %v, want X", axis)
	}
	// The interior base cell must be a nether_portal (fill), and the frame corners obsidian.
	interior, _ := nether.GetBlock(bottomLeft, dimNetherMinY)
	if !isNetherPortalBlock(interior) {
		t.Fatalf("interior base %v is not a nether_portal after createPortal (state %d)", bottomLeft, interior)
	}
	// The frame floor directly below the interior (bottomLeft - Y 1) must be obsidian (frame ring).
	below := pk.Position{X: bottomLeft.X, Y: bottomLeft.Y - 1, Z: bottomLeft.Z}
	belowState, _ := nether.GetBlock(below, dimNetherMinY)
	if _, isOb := block.StateList[belowState].(block.Obsidian); !isOb {
		t.Fatalf("frame floor %v is not obsidian after createPortal (state %d)", below, belowState)
	}
}

// TestCreatePortalIndexesPoi: a portal built by createPortal is indexed in the dimension POI (via
// portalSetBlock -> updatePOIOnBlockStateChange), so a subsequent findClosestPortalPosition finds it.
// CITE: portalSetBlock (updatePoiOnBlockStateChangeIn on every laid nether_portal cell).
func TestCreatePortalIndexesPoi(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	nether := world.NewChunkManager()
	loop.netherWorld = nether
	for _, cx := range []int32{-1, 0, 1} {
		for _, cz := range []int32{-1, 0, 1} {
			readyNetherChunk(nether, level.ChunkPos{cx, cz})
		}
	}
	netherrack := block.ToStateID[block.Netherrack{}]
	for x := -16; x <= 16; x++ {
		for z := -16; z <= 16; z++ {
			nether.SetBlock(pk.Position{X: x, Y: 63, Z: z}, netherrack, dimNetherMinY)
		}
	}

	g := loop.portalDimGeomFor(dimNether)
	exit := pk.Position{X: 4, Y: 64, Z: 4}
	bottomLeft, _, ok := loop.createPortal(g, exit, block.X)
	if !ok {
		t.Fatalf("createPortal reported failure")
	}

	// The POI must now hold the built portal; findClosest from the exit resolves it.
	got, found := loop.findClosestPortalPosition(g, exit, true)
	if !found {
		t.Fatalf("built portal was not indexed in the POI (findClosestPortalPosition found nothing)")
	}
	// The found pos must be one of the interior cells (the base at bottomLeft is the lowest -> preferred by
	// the thenComparingInt(Y) tiebreak among equal-distance cells).
	if got.Y != bottomLeft.Y {
		t.Fatalf("findClosest after create returned Y=%d, want the interior base Y=%d", got.Y, bottomLeft.Y)
	}
}

// TestResolveNetherPortalRoundTrip: a full overworld->nether->overworld round trip resolves back to the
// SAME overworld portal. First an overworld portal is indexed; traveling nether->overworld from the scaled
// column resolves to that overworld portal (findClosestPortalPosition within the 128 overworld radius),
// rather than building a new one. CITE: NetherPortalBlock.getPortalDestination round trip.
func TestResolveNetherPortalRoundTrip(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	ow := world.NewChunkManager()
	for _, r := range loop.regions {
		r.world = ow
	}
	loop.netherWorld = world.NewChunkManager()
	// A loaded overworld chunk at (0,0) with an indexed nether_portal (the origin portal).
	readyOverworldChunk(ow)
	owPortal := pk.Position{X: 8, Y: 70, Z: 8}
	portalState := block.ToStateID[block.NetherPortal{Axis: block.X}]
	ow.SetBlock(owPortal, portalState, dimMinY)
	loop.withRegion(loop.only(), func() {
		loop.dimPoiManager(dimOverworld).add(owPortal, poiTypeNetherPortal)
	})

	// Travel nether->overworld: the scaled column near the origin portal must resolve back to it (within the
	// 128 overworld radius). resolveNetherPortalDestination reads the overworld POI (region-owned), so run it
	// inside the region context.
	loop.withRegion(loop.only(), func() {
		rx, _, rz, ok := loop.resolveNetherPortalDestination(dimOverworld, 8.0, 8.0)
		if !ok {
			t.Fatalf("resolveNetherPortalDestination(overworld) found nothing; expected the origin portal")
		}
		// Landed at the origin portal column center.
		if int(rx) != owPortal.X || int(rz) != owPortal.Z {
			t.Fatalf("round-trip landed at (%.1f,%.1f), want the origin portal column (%d,%d)", rx, rz, owPortal.X, owPortal.Z)
		}
	})
}
