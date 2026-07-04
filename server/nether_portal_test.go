package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// nether_portal_test.go — NETHER PORTAL: a valid obsidian frame + flint&steel on the inside floor fills
// the interior with NETHER_PORTAL blocks of the correct AXIS; an incomplete frame does NOT create a
// portal; a too-large (>21) interior is invalid. The dims/AXIS are asserted against the jar
// (PortalShape: MIN_WIDTH=2, MAX_WIDTH=21, MIN_HEIGHT=3, MAX_HEIGHT=21). All geometry is kept inside the
// single test chunk (column {0,0}, block x/z in [0,15]).

// setObsidian / setAir write frame / interior cells over the tick-owned manager.
func setObsidian(mgr *world.ChunkManager, x, y, z int) {
	mgr.SetBlock(pk.Position{X: x, Y: y, Z: z}, block.ToStateID[block.Obsidian{}], dimMinY)
}

// buildXAxisFrame builds a minimal (interior 2 wide × 3 tall) obsidian frame in the X-Y plane at the
// given z, with the interior columns at x0..x0+1 and y0..y0+2. The full obsidian ring (including corners)
// is placed; the interior is left air. Returns the interior bottom-left cell (x0, y0, z).
func buildXAxisFrame(mgr *world.ChunkManager, x0, y0, z int) pk.Position {
	// Left / right walls (x0-1 and x0+2): full 5-tall obsidian columns from y0-1 .. y0+3.
	for y := y0 - 1; y <= y0+3; y++ {
		setObsidian(mgr, x0-1, y, z)
		setObsidian(mgr, x0+2, y, z)
	}
	// Floor (y0-1) and ceiling (y0+3) across the two interior columns.
	for x := x0; x <= x0+1; x++ {
		setObsidian(mgr, x, y0-1, z)
		setObsidian(mgr, x, y0+3, z)
	}
	return pk.Position{X: x0, Y: y0, Z: z}
}

// flintPlayer registers a player holding a flint&steel in the active hotbar slot, near the frame so
// reach passes. Reuses blockPlayer's scaffolding (block_interact_test.go).
func flintPlayer(loop *TickLoop, x, y, z float64) *tickPlayer {
	p := blockPlayer(loop, x, y, z)
	setHeldItem(p, item.FlintAndSteel.ID, 1)
	return p
}

// TestNetherPortalIgniteFillsFrame: a valid 2×3-interior obsidian frame + flint&steel on the inside
// floor fills the interior with NETHER_PORTAL blocks whose AXIS matches the frame plane (X here). The
// six interior cells become nether_portal; the frame obsidian is untouched.
func TestNetherPortalIgniteFillsFrame(t *testing.T) {
	loop, mgr := newBlockLoop()
	// Interior x∈{8,9}, y∈{65,66,67}, z=8 (all inside the {0,0} chunk).
	const x0, y0, z = 8, 65, 8
	buildXAxisFrame(mgr, x0, y0, z)

	// Player right next to the frame so reach passes; yaw 0 (facing SOUTH) — irrelevant here since both
	// axes are tried and only the X frame closes.
	p := flintPlayer(loop, float64(x0)+0.5, float64(y0)+1.0, float64(z)+2.5)

	// Right-click the floor obsidian at (x0, y0-1, z) on its TOP face (UP=1): relativePos = (x0, y0, z),
	// the interior bottom-left cell.
	floor := pk.Position{X: x0, Y: y0 - 1, Z: z}
	ui := useItemOnPacket(0 /*main hand*/, floor, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 1)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// The AXIS-X nether portal state.
	want := block.ToStateID[block.NetherPortal{Axis: block.X}]

	// All six interior cells are now nether_portal with AXIS=X.
	filled := 0
	for x := x0; x <= x0+1; x++ {
		for y := y0; y <= y0+2; y++ {
			got, ok := mgr.GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinY)
			if !ok {
				t.Fatalf("interior cell (%d,%d,%d) unreadable", x, y, z)
			}
			if got != want {
				t.Fatalf("interior cell (%d,%d,%d) = state %d (%s), want nether_portal AXIS=X (%d)",
					x, y, z, got, block.StateList[got].ID(), want)
			}
			// Assert the AXIS property really is X.
			np, isNP := block.StateList[got].(block.NetherPortal)
			if !isNP || np.Axis != block.X {
				t.Fatalf("interior cell (%d,%d,%d) axis = %v, want X", x, y, z, np.Axis)
			}
			filled++
		}
	}
	if filled != 6 {
		t.Fatalf("filled %d interior cells, want 6 (2 wide × 3 tall)", filled)
	}

	// The frame obsidian is untouched.
	if got, _ := mgr.GetBlock(pk.Position{X: x0 - 1, Y: y0, Z: z}, dimMinY); got != block.ToStateID[block.Obsidian{}] {
		t.Fatalf("left frame wall was altered: state %d, want obsidian", got)
	}
}

// TestNetherPortalIncompleteFrameNoPortal: an INCOMPLETE frame (one wall cell missing, so no valid empty
// frame resolves) creates NO portal — the interior stays air (v1 places nothing when isPortal is false;
// the plain-fire path is a cited follow-up).
func TestNetherPortalIncompleteFrameNoPortal(t *testing.T) {
	loop, mgr := newBlockLoop()
	const x0, y0, z = 8, 65, 8
	buildXAxisFrame(mgr, x0, y0, z)
	// Break the frame: remove one wall obsidian (turn it to air) so the frame no longer closes.
	mgr.SetBlock(pk.Position{X: x0 - 1, Y: y0 + 1, Z: z}, block.ToStateID[block.Air{}], dimMinY)

	p := flintPlayer(loop, float64(x0)+0.5, float64(y0)+1.0, float64(z)+2.5)
	floor := pk.Position{X: x0, Y: y0 - 1, Z: z}
	ui := useItemOnPacket(0, floor, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 2)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// No portal anywhere in the (would-be) interior — every cell stays air (not nether_portal).
	for x := x0; x <= x0+1; x++ {
		for y := y0; y <= y0+2; y++ {
			got, ok := mgr.GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinY)
			if !ok || !block.IsAir(got) {
				t.Fatalf("incomplete frame created a portal at (%d,%d,%d) = %d, want air", x, y, z, got)
			}
		}
	}
}

// TestNetherPortalTooWideInvalid: an interior wider than MAX_WIDTH (21) is invalid (PortalShape.isValid
// requires width ≤ 21), so no portal forms. We build a 22-wide interior floor/ceiling with walls 22 apart
// (interior x span 23 cells) — but keep it within a fresh multi-column world.
func TestNetherPortalTooWideInvalid(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	// Insert enough columns to cover x in [0, 31], z=8 (columns {0,0},{1,0}).
	for cx := int32(0); cx <= 1; cx++ {
		ch := level.EmptyChunk(blockTestSecs)
		ch.Status = level.StatusFull
		mgr.Insert(level.ChunkPos{cx, 0}, ch)
	}

	// Interior width 22 (> MAX_WIDTH 21): interior x∈[1,22], walls at x=0 and x=23, floor/ceiling y=64/68.
	const y0, z = 65, 8
	for y := y0 - 1; y <= y0+3; y++ {
		setObsidian(mgr, 0, y, z)
		setObsidian(mgr, 23, y, z)
	}
	for x := 1; x <= 22; x++ {
		setObsidian(mgr, x, y0-1, z)
		setObsidian(mgr, x, y0+3, z)
	}

	p := flintPlayer(loop, 1.5, float64(y0)+1.0, float64(z)+2.5)
	floor := pk.Position{X: 1, Y: y0 - 1, Z: z}
	ui := useItemOnPacket(0, floor, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 3)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// The 22-wide interior exceeds MAX_WIDTH, so no portal forms — interior stays air.
	for x := 1; x <= 22; x++ {
		if got, ok := mgr.GetBlock(pk.Position{X: x, Y: y0, Z: z}, dimMinY); !ok || !block.IsAir(got) {
			t.Fatalf("too-wide frame created a portal at (%d,%d,%d) = %d, want air (width 22 > MAX_WIDTH 21)", x, y0, z, got)
		}
	}
}

// TestPortalShapeDimsMatchJar: a unit check that the ported bounds equal the jar constants
// (PortalShape: MIN_WIDTH=2, MAX_WIDTH=21, MIN_HEIGHT=3, MAX_HEIGHT=21) and that isValid honors them.
func TestPortalShapeDimsMatchJar(t *testing.T) {
	if portalMinWidth != 2 || portalMaxWidth != 21 || portalMinHeight != 3 || portalMaxHeight != 21 {
		t.Fatalf("portal bounds = w[%d,%d] h[%d,%d], want w[2,21] h[3,21] (PortalShape constants)",
			portalMinWidth, portalMaxWidth, portalMinHeight, portalMaxHeight)
	}
	// A minimal valid shape: 2 wide, 3 tall.
	if !(portalShape{width: 2, height: 3}).isValid() {
		t.Fatal("2×3 shape must be valid (MIN_WIDTH × MIN_HEIGHT)")
	}
	// Below-min and above-max are invalid.
	if (portalShape{width: 1, height: 3}).isValid() {
		t.Fatal("width 1 (< MIN_WIDTH) must be invalid")
	}
	if (portalShape{width: 2, height: 2}).isValid() {
		t.Fatal("height 2 (< MIN_HEIGHT) must be invalid")
	}
	if (portalShape{width: 22, height: 3}).isValid() {
		t.Fatal("width 22 (> MAX_WIDTH) must be invalid")
	}
	if (portalShape{width: 2, height: 22}).isValid() {
		t.Fatal("height 22 (> MAX_HEIGHT) must be invalid")
	}
}
