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
