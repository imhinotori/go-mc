package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// dripstone_test.go -- the POINTED DRIPSTONE random-tick growth-core gate (dripstone.go). It proves:
//   - each tick draws EXACTLY two nextFloat (maybeTransferFluid + growth gate) in order;
//   - the growth gate fires only when nextFloat #2 < 0.011377778 AND isStalactiteStartPos holds;
//   - isStalactiteStartPos: a downward tip with no dripstone of the same block directly above.
// Uses the shared newRandomTickLoop / mustGet helpers.

func stalactiteTip() block.StateID {
	s, ok := block.ToStateID[block.PointedDripstone{
		Thickness:         block.SpeleothemThicknessTip,
		VerticalDirection: block.Down,
	}]
	if !ok {
		panic("no pointed dripstone tip state")
	}
	return s
}

// TestDripstoneDrawsTwoNextFloats: a pointed dripstone tick consumes EXACTLY two nextFloat draws (the
// maybeTransferFluid draw + the SpeleothemBlock growth-gate draw), in that order, regardless of
// whether growth fires. CITE: PointedDripstoneBlock.randomTick; SpeleothemBlock.randomTick.
func TestDripstoneDrawsTwoNextFloats(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	const seed = int64(0xD819)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	drip := pk.Position{X: 5, Y: 70, Z: 5}
	mgr.SetBlock(drip, stalactiteTip(), dimMinY)

	loop.dripstoneRandomTick(r, stalactiteTip(), drip)

	// After the handler, the stream must be advanced by exactly two nextFloat draws.
	fresh := levelgen.NewLegacyRandomSource(seed)
	fresh.NextFloat()
	fresh.NextFloat()
	if r.levelRandom.NextFloat() != fresh.NextFloat() {
		t.Fatal("pointed dripstone tick did not draw exactly two nextFloats")
	}
}

// TestDripstoneGrowGateNoDripstoneBlockAbove: even with a growth-roll < 0.011377778 and a valid
// stalactite start pos, growth is blocked (canGrow false) when there is no dripstone_block above. The
// block is unchanged and only the two nextFloats are drawn (grow body, incl. its nextBoolean, is the
// cited deferral so it draws nothing). CITE: SpeleothemBlock.canGrow.
func TestDripstoneGrowGateNoDripstoneBlockAbove(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	// Find a seed whose SECOND nextFloat is < the growth probability (the first is maybeTransferFluid).
	var seed int64
	for s := int64(1); s < 2000000; s++ {
		rr := levelgen.NewLegacyRandomSource(s)
		rr.NextFloat()
		if rr.NextFloat() < float32(block.DripstoneGrowthProbability) {
			seed = s
			break
		}
	}
	if seed == 0 {
		t.Fatal("no growth-roll seed found")
	}
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	drip := pk.Position{X: 6, Y: 70, Z: 6}
	mgr.SetBlock(drip, stalactiteTip(), dimMinY)
	// Above is air (EmptyChunk): isStalactiteStartPos holds (no same dripstone above) but canGrow fails.

	loop.dripstoneRandomTick(r, stalactiteTip(), drip)

	if got := mustGet(t, mgr, drip); got != stalactiteTip() {
		t.Fatalf("dripstone changed without a dripstone_block above; got %d", got)
	}
	// exactly two nextFloats drawn (deferred grow draws nothing).
	fresh := levelgen.NewLegacyRandomSource(seed)
	fresh.NextFloat()
	fresh.NextFloat()
	if r.levelRandom.NextFloat() != fresh.NextFloat() {
		t.Fatal("dripstone drew != 2 nextFloats (deferred grow must not draw)")
	}
}

// TestDripstoneStalactiteStartPos: isStalactiteStartPos requires a downward tip with no pointed
// dripstone of the same block directly above; a pointed dripstone above defeats it. CITE:
// SpeleothemBlock.isStalactiteStartPos.
func TestDripstoneStalactiteStartPos(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()

	drip := pk.Position{X: 7, Y: 70, Z: 7}
	mgr.SetBlock(drip, stalactiteTip(), dimMinY)
	// No block above -> start pos true.
	if !loop.dripstoneIsStalactiteStartPos(stalactiteTip(), drip) {
		t.Fatal("a lone downward tip should be a stalactite start pos")
	}
	// Pointed dripstone directly above -> NOT a start pos.
	mgr.SetBlock(above(drip), stalactiteTip(), dimMinY)
	if loop.dripstoneIsStalactiteStartPos(stalactiteTip(), drip) {
		t.Fatal("a tip with dripstone above must not be a stalactite start pos")
	}
}

// dripstoneBlockState / waterSource / stalagmiteTip build the fixtures the growth-traversal tests need.
func dripstoneBlockState() block.StateID { return block.ToStateID[block.DripstoneBlock{}] }
func waterSource() block.StateID         { return block.ToStateID[block.Water{Level: 0}] }
func stalagmiteTip() block.StateID {
	s, ok := block.ToStateID[block.PointedDripstone{
		Thickness:         block.SpeleothemThicknessTip,
		VerticalDirection: block.Up,
	}]
	if !ok {
		panic("no pointed dripstone up tip state")
	}
	return s
}

// growSeed finds a seed whose growth-roll (nextFloat #2, after the maybeTransferFluid nextFloat #1)
// is < the growth probability AND whose subsequent nextBoolean equals wantBool -- so the growth chain
// reaches grow (nextBoolean true -> stalactite DOWN) vs growStalagmiteBelow (false).
func growSeed(t *testing.T, wantBool bool) int64 {
	t.Helper()
	for s := int64(1); s < 5000000; s++ {
		rr := levelgen.NewLegacyRandomSource(s)
		rr.NextFloat() // maybeTransferFluid draw (transfer gated out by geometry in these tests)
		if rr.NextFloat() >= float32(block.DripstoneGrowthProbability) {
			continue
		}
		if rr.NextBoolean() == wantBool {
			return s
		}
	}
	t.Fatalf("no growth seed with nextBoolean=%v found", wantBool)
	return 0
}

// TestDripstoneTipTraversalGrowsStalactiteDown: a valid stalactite start (dripstone_block above, water
// SOURCE at above(2)) whose free-hanging tip sits at the start and has AIR below grows a new TIP one
// cell DOWN when nextBoolean is true. Exercises canGrow(water-source gate) + findTip(self) +
// isFreeHangingStalactite + canTipGrow(air) + grow(DOWN) + createSpeleothem. CITE:
// SpeleothemBlock.growStalactiteOrStalagmiteIfPossible; grow; createSpeleothem.
func TestDripstoneTipTraversalGrowsStalactiteDown(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	seed := growSeed(t, true)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	// Column (top to bottom): water source (y+2), dripstone_block (y+1), stalactite TIP (y), air (y-1).
	base := pk.Position{X: 3, Y: 70, Z: 3}
	mgr.SetBlock(pk.Position{X: base.X, Y: base.Y + 2, Z: base.Z}, waterSource(), dimMinY)
	mgr.SetBlock(above(base), dripstoneBlockState(), dimMinY)
	mgr.SetBlock(base, stalactiteTip(), dimMinY)
	// below(base) stays air (EmptyChunk).

	loop.dripstoneRandomTick(r, stalactiteTip(), base)

	got := mustGet(t, mgr, below(base))
	if !block.IsPointedDripstone(got) {
		t.Fatalf("expected a new pointed dripstone tip below the stalactite; got %d", got)
	}
	if !block.IsFreeHangingStalactite(got) {
		t.Fatalf("the grown cell should be a free-hanging DOWN tip; got %d", got)
	}
}

// TestDripstoneCanTipGrowWaterGate: with the SAME valid stalactite start but the cell below the tip
// holding a WATER SOURCE, canTipGrow returns false (a non-empty fluid in front of the tip blocks
// growth) and the stalactite does NOT grow -- the tip cell stays air-free and no dripstone appears one
// further cell down. CITE: SpeleothemBlock.canTipGrow (fluidState not empty -> false).
func TestDripstoneCanTipGrowWaterGate(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	seed := growSeed(t, true)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	base := pk.Position{X: 4, Y: 70, Z: 4}
	mgr.SetBlock(pk.Position{X: base.X, Y: base.Y + 2, Z: base.Z}, waterSource(), dimMinY)
	mgr.SetBlock(above(base), dripstoneBlockState(), dimMinY)
	mgr.SetBlock(base, stalactiteTip(), dimMinY)
	// Put a WATER SOURCE directly below the tip: canTipGrow sees a non-empty fluid -> false.
	mgr.SetBlock(below(base), waterSource(), dimMinY)

	loop.dripstoneRandomTick(r, stalactiteTip(), base)

	// The water below is unchanged (no dripstone grew into it) and no tip appeared two cells down.
	if got := mustGet(t, mgr, below(base)); !block.IsPointedDripstone(got) {
		// water still water is the expected outcome (no growth).
	} else {
		t.Fatalf("canTipGrow water gate failed: a dripstone grew into the water cell (got %d)", got)
	}
	twoDown := pk.Position{X: base.X, Y: base.Y - 2, Z: base.Z}
	if got := mustGet(t, mgr, twoDown); block.IsPointedDripstone(got) {
		t.Fatalf("canTipGrow water gate failed: growth propagated past the water cell (got %d)", got)
	}
}

// TestDripstoneMudToClayFluidTransfer: a stalactite start whose ROOT (the dripstone_block above) has
// MUD directly above it, on a chance roll below the WATER transfer ceiling, converts that MUD to CLAY
// -- the maybeTransferFluid MUD->CLAY branch (no cauldron needed). Because a WATER drip is inferred
// from MUD, canGrow (which needs a WATER SOURCE at above(2), i.e. the MUD cell) is NOT satisfied, so
// no growth occurs and only the MUD->CLAY conversion is observed. CITE:
// PointedDripstoneBlock.maybeTransferFluid (MUD -> CLAY); getFluidAboveStalactite (MUD -> WATER).
func TestDripstoneMudToClayFluidTransfer(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	// Seed whose FIRST nextFloat (the maybeTransferFluid chance) is < the WATER transfer ceiling.
	var seed int64
	for s := int64(1); s < 5000000; s++ {
		rr := levelgen.NewLegacyRandomSource(s)
		if rr.NextFloat() < float32(dripWaterTransferProbability) {
			seed = s
			break
		}
	}
	if seed == 0 {
		t.Fatal("no water-transfer seed found")
	}
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	// Column: MUD (y+2, the FluidInfo source == root.above()), dripstone_block ROOT (y+1),
	// stalactite TIP (y). isStalactiteStartPos holds (no dripstone above the tip is a dripstone_block,
	// which is not pointed dripstone). getFluidAboveStalactite: root == dripstone_block at y+1, its
	// above() == MUD at y+2 -> WATER. maybeTransferFluid converts the MUD to CLAY.
	base := pk.Position{X: 5, Y: 70, Z: 5}
	mudPos := pk.Position{X: base.X, Y: base.Y + 2, Z: base.Z}
	mgr.SetBlock(mudPos, block.ToStateID[block.Mud{}], dimMinY)
	mgr.SetBlock(above(base), dripstoneBlockState(), dimMinY)
	mgr.SetBlock(base, stalactiteTip(), dimMinY)

	loop.dripstoneRandomTick(r, stalactiteTip(), base)

	if got := mustGet(t, mgr, mudPos); got != block.ToStateID[block.Clay{}] {
		t.Fatalf("MUD above the stalactite root was not converted to CLAY; got %d", got)
	}
}
