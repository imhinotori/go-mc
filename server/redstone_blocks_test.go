package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// redstone_blocks_test.go - validation gates for the NoteBlock / TargetBlock / DaylightDetector /
// TripWire port (redstone_blocks.go). Each asserts the ported state/power values against the 26.2 jar.

// setSkyLight writes a uniform sky-light nibble across every section of a chunk (the light the
// LevelLightEngine would compute); a daylight detector reads it via getEffectiveSkyBrightness.
func setSkyLight(ch *level.Chunk, value byte) {
	nib := value&0xF | (value&0xF)<<4
	for i := range ch.Sections {
		sky := make([]byte, 2048)
		for j := range sky {
			sky[j] = nib
		}
		ch.Sections[i].SkyLight = sky
	}
}

// newRedstoneBlockLoop wires a TickLoop with one ready chunk + a registered block-tick container for
// column (0,0). Mirrors newRedstoneLoop.
func newRedstoneBlockLoop() (*TickLoop, *world.ChunkManager, *level.Chunk) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr, ch
}

// TestNoteBlockPlaysNoteAndPowersOnRisingEdge: a note block with clear air above, on a redstone rising
// edge (an adjacent lever toggles ON), plays its note and sets POWERED; on the falling edge it clears
// POWERED and does NOT replay. CITE: NoteBlock.neighborChanged/playNote.
func TestNoteBlockPlaysNoteAndPowersOnRisingEdge(t *testing.T) {
	loop, mgr, _ := newRedstoneBlockLoop()
	notePos := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(notePos, block.ToStateID[block.NoteBlock{}], dimMinY)

	leverPos := pk.Position{X: 5, Y: 64, Z: 4}
	lever := block.ToStateID[block.Lever{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(pk.Position{X: 5, Y: 63, Z: 4}, stoneState(), dimMinY)
	mgr.SetBlock(leverPos, lever, dimMinY)

	loop.useLever(leverPos, lever)

	ns, _ := mgr.GetBlock(notePos, dimMinY)
	if !block.NoteBlockPowered(ns) {
		t.Fatal("note block should be POWERED after an adjacent lever turns ON")
	}
	if len(loop.only().notesPlayed) != 1 || loop.only().notesPlayed[0] != notePos {
		t.Fatalf("note should have played exactly once at %v, got %v", notePos, loop.only().notesPlayed)
	}

	leverOn, _ := mgr.GetBlock(leverPos, dimMinY)
	loop.useLever(leverPos, leverOn)
	ns2, _ := mgr.GetBlock(notePos, dimMinY)
	if block.NoteBlockPowered(ns2) {
		t.Fatal("note block should clear POWERED after the lever turns OFF")
	}
	if len(loop.only().notesPlayed) != 1 {
		t.Fatalf("note must NOT replay on the falling edge; plays=%d", len(loop.only().notesPlayed))
	}
}

// TestNoteBlockCoveredStaysSilent: a covered harp note block (BASE_BLOCK instrument, solid block above)
// does NOT play on a rising edge but still updates POWERED. CITE: NoteBlock.playNote gate.
func TestNoteBlockCoveredStaysSilent(t *testing.T) {
	loop, mgr, _ := newRedstoneBlockLoop()
	notePos := pk.Position{X: 6, Y: 64, Z: 6}
	mgr.SetBlock(notePos, block.ToStateID[block.NoteBlock{}], dimMinY)
	mgr.SetBlock(above(notePos), stoneState(), dimMinY)

	leverPos := pk.Position{X: 7, Y: 64, Z: 6}
	lever := block.ToStateID[block.Lever{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(pk.Position{X: 7, Y: 63, Z: 6}, stoneState(), dimMinY)
	mgr.SetBlock(leverPos, lever, dimMinY)

	loop.useLever(leverPos, lever)

	ns, _ := mgr.GetBlock(notePos, dimMinY)
	if !block.NoteBlockPowered(ns) {
		t.Fatal("covered note block should still become POWERED")
	}
	if len(loop.only().notesPlayed) != 0 {
		t.Fatalf("covered harp note block must stay silent; plays=%d", len(loop.only().notesPlayed))
	}
}

// TestDaylightDetectorScalesWithSkyLight: a normal detector outputs POWER == sky brightness at day
// (SUN_ANGLE 0 -> cos term 1). CITE: DaylightDetectorBlock.updateSignalStrength.
func TestDaylightDetectorScalesWithSkyLight(t *testing.T) {
	loop, mgr, ch := newRedstoneBlockLoop()
	setSkyLight(ch, 15)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(pos, block.ToStateID[block.DaylightDetector{}], dimMinY)

	loop.daylightUpdateSignalStrength(pos, loop.redstoneBlockAt(pos))
	s, _ := mgr.GetBlock(pos, dimMinY)
	if got := block.DaylightPower(s); got != 15 {
		t.Fatalf("daylight detector POWER at sky 15 = %d, want 15", got)
	}
	for _, d := range redstoneDirs {
		if sig := loop.stateGetSignal(s, pos, d); sig != 15 {
			t.Fatalf("daylight detector getSignal(%v) = %d, want 15", d, sig)
		}
	}

	setSkyLight(ch, 8)
	loop.daylightUpdateSignalStrength(pos, loop.redstoneBlockAt(pos))
	s8, _ := mgr.GetBlock(pos, dimMinY)
	if got := block.DaylightPower(s8); got != 8 {
		t.Fatalf("daylight detector POWER at sky 8 = %d, want 8", got)
	}
}

// TestInvertedDaylightDetector: an INVERTED detector outputs 15 - skyBrightness. CITE:
// DaylightDetectorBlock.updateSignalStrength (INVERTED branch).
func TestInvertedDaylightDetector(t *testing.T) {
	loop, mgr, ch := newRedstoneBlockLoop()
	setSkyLight(ch, 15)
	pos := pk.Position{X: 9, Y: 64, Z: 9}
	mgr.SetBlock(pos, block.ToStateID[block.DaylightDetector{Inverted: true}], dimMinY)

	loop.daylightUpdateSignalStrength(pos, loop.redstoneBlockAt(pos))
	s, _ := mgr.GetBlock(pos, dimMinY)
	if got := block.DaylightPower(s); got != 0 {
		t.Fatalf("inverted detector POWER at sky 15 = %d, want 0", got)
	}

	setSkyLight(ch, 8)
	loop.daylightUpdateSignalStrength(pos, loop.redstoneBlockAt(pos))
	s7, _ := mgr.GetBlock(pos, dimMinY)
	if got := block.DaylightPower(s7); got != 7 {
		t.Fatalf("inverted detector POWER at sky 8 = %d, want 7 (15-8)", got)
	}
}

// TestDaylightDetectorSelfReschedulesEvery20: the tick recomputes POWER and reschedules onto the next
// game-time multiple of 20. CITE: DaylightDetectorBlock.tickEntity (gameTime mod 20 == 0).
func TestDaylightDetectorSelfReschedulesEvery20(t *testing.T) {
	loop, mgr, ch := newRedstoneBlockLoop()
	setSkyLight(ch, 15)
	pos := pk.Position{X: 10, Y: 64, Z: 10}
	mgr.SetBlock(pos, block.ToStateID[block.DaylightDetector{}], dimMinY)

	loop.scheduleDaylightTick(pos)
	if !loop.hasScheduledBlockTick(pos, daylightDetectorTickType) {
		t.Fatal("daylight detector should have a scheduled tick after scheduleDaylightTick")
	}
	loop.gametime = 20
	loop.tickScheduledBlocks()
	s, _ := mgr.GetBlock(pos, dimMinY)
	if got := block.DaylightPower(s); got != 15 {
		t.Fatalf("after the 20-tick daylight tick, POWER = %d, want 15", got)
	}
	if !loop.hasScheduledBlockTick(pos, daylightDetectorTickType) {
		t.Fatal("daylight detector should reschedule its next tick after firing")
	}
}

// TestTargetBlockOutputsOnHitThenResets: a center projectile hit sets OUTPUT_POWER 15 and schedules the
// reset (20 ticks arrow); the scheduled tick clears it to 0. CITE: TargetBlock.updateRedstoneOutput/tick.
func TestTargetBlockOutputsOnHitThenResets(t *testing.T) {
	loop, mgr, _ := newRedstoneBlockLoop()
	pos := pk.Position{X: 11, Y: 64, Z: 11}
	mgr.SetBlock(pos, block.ToStateID[block.Target{}], dimMinY)

	got := loop.targetProjectileHit(pos, loop.redstoneBlockAt(pos), block.Up, float64(pos.X)+0.5, float64(pos.Y)+1.0, float64(pos.Z)+0.5, true)
	if got != 15 {
		t.Fatalf("center arrow hit strength = %d, want 15", got)
	}
	s, _ := mgr.GetBlock(pos, dimMinY)
	if op := block.TargetOutputPower(s); op != 15 {
		t.Fatalf("target OUTPUT_POWER after center hit = %d, want 15", op)
	}
	if sig := loop.stateGetSignal(s, pos, block.North); sig != 15 {
		t.Fatalf("target getSignal(NORTH) = %d, want 15", sig)
	}
	if !loop.hasScheduledBlockTick(pos, targetTickType) {
		t.Fatal("target should schedule a reset tick after a hit")
	}
	loop.gametime = 20
	loop.tickScheduledBlocks()
	sr, _ := mgr.GetBlock(pos, dimMinY)
	if op := block.TargetOutputPower(sr); op != 0 {
		t.Fatalf("target OUTPUT_POWER after the 20-tick reset = %d, want 0", op)
	}
}

// TestTargetRimHitIsOne: a corner hit yields strength 1 (the max(1, ...) floor). CITE:
// TargetBlock.getRedstoneStrength.
func TestTargetRimHitIsOne(t *testing.T) {
	if got := targetGetRedstoneStrength(block.Up, 11.0, 65.0, 11.0); got != 1 {
		t.Fatalf("rim hit strength = %d, want 1", got)
	}
}

// TestTripwireHookTripsWhenWireCrossed: two facing hooks with a wire between them form a connected span
// (ATTACHED). When the wire is crossed (POWERED), both hooks go POWERED and emit 15. CITE:
// TripWireHookBlock.calculateState / ownSignal.
func TestTripwireHookTripsWhenWireCrossed(t *testing.T) {
	loop, mgr, _ := newRedstoneBlockLoop()
	y := 64
	hookA := pk.Position{X: 2, Y: y, Z: 3}
	hookB := pk.Position{X: 5, Y: y, Z: 3}
	wire1 := pk.Position{X: 3, Y: y, Z: 3}
	wire2 := pk.Position{X: 4, Y: y, Z: 3}

	mgr.SetBlock(hookA, block.ToStateID[block.TripwireHook{Facing: block.East}], dimMinY)
	mgr.SetBlock(hookB, block.ToStateID[block.TripwireHook{Facing: block.West}], dimMinY)
	mgr.SetBlock(wire1, block.ToStateID[block.Tripwire{}], dimMinY)
	mgr.SetBlock(wire2, block.ToStateID[block.Tripwire{}], dimMinY)

	loop.tripwireHookCalculateState(hookA, loop.redstoneBlockAt(hookA), false, -1, 0, false)

	sa, _ := mgr.GetBlock(hookA, dimMinY)
	sb, _ := mgr.GetBlock(hookB, dimMinY)
	if !block.TripwireHookAttached(sa) || !block.TripwireHookAttached(sb) {
		t.Fatalf("both hooks should be ATTACHED (A=%v B=%v)", block.TripwireHookAttached(sa), block.TripwireHookAttached(sb))
	}
	if block.TripwireHookPowered(sa) {
		t.Fatal("hook A should NOT be POWERED before the wire is crossed")
	}

	loop.tripwireCheckPressed(wire1, true)

	w1, _ := mgr.GetBlock(wire1, dimMinY)
	if !block.TripwirePowered(w1) {
		t.Fatal("crossed tripwire should be POWERED")
	}
	sa2, _ := mgr.GetBlock(hookA, dimMinY)
	sb2, _ := mgr.GetBlock(hookB, dimMinY)
	if !block.TripwireHookPowered(sa2) || !block.TripwireHookPowered(sb2) {
		t.Fatalf("both hooks should be POWERED when the wire is crossed (A=%v B=%v)", block.TripwireHookPowered(sa2), block.TripwireHookPowered(sb2))
	}
	if sig := loop.stateGetSignal(sa2, hookA, block.North); sig != 15 {
		t.Fatalf("powered hook getSignal(NORTH) = %d, want 15", sig)
	}
	if ds := loop.stateGetDirectSignal(sa2, hookA, block.East); ds != 15 {
		t.Fatalf("powered hook getDirectSignal(FACING=EAST) = %d, want 15", ds)
	}
	if ds := loop.stateGetDirectSignal(sa2, hookA, block.West); ds != 0 {
		t.Fatalf("powered hook getDirectSignal(non-FACING) = %d, want 0", ds)
	}
}

// TestTripwireEntityScanPowersAndReleasesHooks: an entity stepping onto a connected tripwire span makes
// tripwireEntitiesPresent true, so checkPressed powers the wire and both hooks; when the entity leaves the
// box the scheduled recheck (tripwireTick) sees an empty box and unpowers the wire + hooks. A MARKER armor
// stand overlapping the wire does NOT press it (Entity.isIgnoringBlockTriggers -> ArmorStand.isMarker()).
// CITE: TripWireBlock.checkPressed(Level,BlockPos) getEntities scan + TripWireBlock.tick;
// Entity/ArmorStand.isIgnoringBlockTriggers.
func TestTripwireEntityScanPowersAndReleasesHooks(t *testing.T) {
	loop, mgr, _ := newRedstoneBlockLoop()
	y := 64
	hookA := pk.Position{X: 2, Y: y, Z: 3}
	hookB := pk.Position{X: 5, Y: y, Z: 3}
	wire1 := pk.Position{X: 3, Y: y, Z: 3}
	wire2 := pk.Position{X: 4, Y: y, Z: 3}

	mgr.SetBlock(hookA, block.ToStateID[block.TripwireHook{Facing: block.East}], dimMinY)
	mgr.SetBlock(hookB, block.ToStateID[block.TripwireHook{Facing: block.West}], dimMinY)
	mgr.SetBlock(wire1, block.ToStateID[block.Tripwire{}], dimMinY)
	mgr.SetBlock(wire2, block.ToStateID[block.Tripwire{}], dimMinY)

	// Form the ATTACHED span (both hooks ATTACHED, wires ATTACHED).
	loop.tripwireHookCalculateState(hookA, loop.redstoneBlockAt(hookA), false, -1, 0, false)

	// No entity yet: the scan reports empty.
	if loop.tripwireEntitiesPresent(wire1) {
		t.Fatal("empty wire cell should report no entities present")
	}

	// An entity standing on wire1 (feet at the block, centered) overlaps the wire shape box.
	e := NewEntity(1, entity.Pig, 3.5, float64(y), 3.5)
	loop.only().entities.add(e)

	if !loop.tripwireEntitiesPresent(wire1) {
		t.Fatal("a pig standing on the wire should be detected by the getEntities scan")
	}

	// Drive checkPressed with the scanned presence -> wire + both hooks go POWERED.
	loop.tripwireCheckPressed(wire1, loop.tripwireEntitiesPresent(wire1))

	w1, _ := mgr.GetBlock(wire1, dimMinY)
	if !block.TripwirePowered(w1) {
		t.Fatal("wire should be POWERED with an entity on it")
	}
	sa, _ := mgr.GetBlock(hookA, dimMinY)
	sb, _ := mgr.GetBlock(hookB, dimMinY)
	if !block.TripwireHookPowered(sa) || !block.TripwireHookPowered(sb) {
		t.Fatalf("both hooks should be POWERED (A=%v B=%v)", block.TripwireHookPowered(sa), block.TripwireHookPowered(sb))
	}

	// The entity leaves. tripwireTick re-runs checkPressed via the scan (now empty) -> unpower.
	e.dead = true
	w1p, _ := mgr.GetBlock(wire1, dimMinY)
	loop.tripwireTick(w1p, wire1)

	w1after, _ := mgr.GetBlock(wire1, dimMinY)
	if block.TripwirePowered(w1after) {
		t.Fatal("wire should UNPOWER once the entity leaves the box")
	}
	saAfter, _ := mgr.GetBlock(hookA, dimMinY)
	sbAfter, _ := mgr.GetBlock(hookB, dimMinY)
	if block.TripwireHookPowered(saAfter) || block.TripwireHookPowered(sbAfter) {
		t.Fatalf("both hooks should UNPOWER (A=%v B=%v)", block.TripwireHookPowered(saAfter), block.TripwireHookPowered(sbAfter))
	}

	// A MARKER armor stand overlapping the wire does NOT press it (isIgnoringBlockTriggers).
	stand := NewEntity(2, entity.ArmorStand, 3.5, float64(y), 3.5)
	stand.armorStandFlags = armorStandFlagMarker
	loop.only().entities.add(stand)
	if loop.tripwireEntitiesPresent(wire1) {
		t.Fatal("a MARKER armor stand ignores block triggers and must not press the tripwire")
	}
}
