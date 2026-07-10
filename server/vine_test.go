package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// vine_test.go -- the VINE random-tick spread gate (vine.go). It proves:
//   - the nextInt(4)!=0 early-out draws exactly one int and does nothing (no spread);
//   - the horizontal side-attach: with the picked direction hitting a solid (face-sturdy) neighbour
//     that is NOT air, the vine sets that face on itself (state[dir]=true);
//   - the density gate (canSpread) blocks spread when >4 vines surround the cell.
// Uses the shared newRandomTickLoop / mustGet helpers.

func vineState() block.StateID { return block.VineDefaultState() }

// vineRoll advances a fresh seed's stream and returns (earlyOut nextInt(4), dir nextInt(6)) so a test
// can pick a seed whose draws land on a chosen branch. Mirrors the handler's first two draws.
func vineRoll(seed int64) (int32, block.Direction) {
	r := levelgen.NewLegacyRandomSource(seed)
	e := r.NextIntN(4)
	d := block.Direction(r.NextIntN(6))
	return e, d
}

// findVineSeedHoriz returns a seed whose first draw is 0 (spread proceeds) and whose second draw
// picks a specific horizontal direction.
func findVineSeedHoriz(want block.Direction) int64 {
	for seed := int64(1); seed < 500000; seed++ {
		e, d := vineRoll(seed)
		if e == 0 && d == want {
			return seed
		}
	}
	panic("no vine horiz seed found")
}

// TestVineEarlyOutNoSpread: a nextInt(4)!=0 first draw returns immediately -- the vine is unchanged
// and only one int is drawn. CITE: VineBlock.randomTick (nextInt(4)!=0 return).
func TestVineEarlyOutNoSpread(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	// Find a seed whose first nextInt(4) is NOT 0.
	var seed int64
	for s := int64(1); s < 100000; s++ {
		if e, _ := vineRoll(s); e != 0 {
			seed = s
			break
		}
	}
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	vinePos := pk.Position{X: 5, Y: 70, Z: 5}
	mgr.SetBlock(vinePos, vineState(), dimMinY)
	loop.vineRandomTick(r, vineState(), vinePos)

	if got := mustGet(t, mgr, vinePos); got != vineState() {
		t.Fatalf("early-out vine changed (%d -> %d); must not spread", vineState(), got)
	}
	// Exactly one nextInt(4) drawn: stream after == a fresh stream advanced by one nextInt(4).
	fresh := levelgen.NewLegacyRandomSource(seed)
	fresh.NextIntN(4)
	if r.levelRandom.NextIntN(999) != fresh.NextIntN(999) {
		t.Fatal("early-out drew != 1 int")
	}
}

// TestVineHorizontalSideAttach: with the picked horizontal direction hitting a solid, face-sturdy,
// non-air neighbour, the vine adds that face to itself (state[dir]=true). CITE: VineBlock.randomTick
// (target not air -> isAcceptableNeighbour(target, dir) -> setBlock(pos, state[dir]=true)).
func TestVineHorizontalSideAttach(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	want := block.North
	seed := findVineSeedHoriz(want)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	vinePos := pk.Position{X: 8, Y: 70, Z: 8}
	mgr.SetBlock(vinePos, vineState(), dimMinY)
	// A full stone block in the picked direction (north = -Z): non-air + face-sturdy toward north.
	stonePos := relative(vinePos, want)
	mgr.SetBlock(stonePos, block.DefaultStateID["minecraft:stone"], dimMinY)

	loop.vineRandomTick(r, vineState(), vinePos)

	got := mustGet(t, mgr, vinePos)
	if !block.VineFace(got, want) {
		t.Fatalf("vine did not attach its %v face to the solid neighbour; got state %d", want, got)
	}
}

// TestVineDensityGateBlocksSpread: with more than 4 vines around the cell, canSpread returns false and
// the horizontal branch does nothing even on a 0 early-out + horizontal direction. CITE:
// VineBlock.canSpread (counter 5, returns false past 4 vines).
func TestVineDensityGateBlocksSpread(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	want := block.North
	seed := findVineSeedHoriz(want)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	vinePos := pk.Position{X: 8, Y: 70, Z: 8}
	mgr.SetBlock(vinePos, vineState(), dimMinY)
	stonePos := relative(vinePos, want)
	mgr.SetBlock(stonePos, block.DefaultStateID["minecraft:stone"], dimMinY)
	// Pack 5 extra vines within the 9x3x9 canSpread box so the density gate trips (>4 total incl. self).
	for i := 0; i < 5; i++ {
		mgr.SetBlock(pk.Position{X: vinePos.X + 1 + i, Y: vinePos.Y, Z: vinePos.Z + 2}, vineState(), dimMinY)
	}

	loop.vineRandomTick(r, vineState(), vinePos)

	if got := mustGet(t, mgr, vinePos); block.VineFace(got, want) {
		t.Fatalf("vine spread despite the density gate; got state %d", got)
	}
}
