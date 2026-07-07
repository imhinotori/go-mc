package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestSpectatorCannotPlace: a spectator holding a block cannot place it (blockActionRestricted). The
// world is unchanged. Adventure is likewise restricted; survival/creative are not (covered by TestPlaceBlock).
func TestSpectatorCannotPlace(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSpectator
	setHeldItem(p, item.Stone.ID, 5)

	hit := pk.Position{X: 1, Y: 64, Z: 1}
	placed := pk.Position{X: 1, Y: 65, Z: 1}
	mgr.SetBlock(hit, block.ToStateID[block.Stone{}], dimMinY)

	ui := useItemOnPacket(0, hit, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, 7)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if got, ok := mgr.GetBlock(placed, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("spectator placed a block: GetBlock(adjacent) = (%v, ok=%v), want air", got, ok)
	}
}

// TestSpectatorCannotBreak: a spectator cannot break a block (blockActionRestricted); the block survives.
func TestSpectatorCannotBreak(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSpectator

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	pa := playerActionPacket(0 /*START_DESTROY_BLOCK*/, target, 1, 42)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	if got, ok := mgr.GetBlock(target, dimMinY); !ok || got != block.ToStateID[block.Stone{}] {
		t.Fatalf("spectator broke a block: GetBlock = (%v, ok=%v), want stone", got, ok)
	}
}

// TestAdventureCannotBreak: an adventure player (no item break permissions in v1) cannot break a block.
func TestAdventureCannotBreak(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeAdventure

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	pa := playerActionPacket(0, target, 1, 42)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	if got, ok := mgr.GetBlock(target, dimMinY); !ok || got != block.ToStateID[block.Stone{}] {
		t.Fatalf("adventure broke a block: GetBlock = (%v, ok=%v), want stone", got, ok)
	}
}

// TestBlockActionRestrictedMatrix pins the game-mode matrix: survival/creative unrestricted; adventure/
// spectator restricted; attack gates on spectator only (adventure may attack).
func TestBlockActionRestrictedMatrix(t *testing.T) {
	cases := []struct {
		mode       int32
		restricted bool
		specAttack bool
	}{
		{gameModeSurvival, false, false},
		{gameModeCreative, false, false},
		{gameModeAdventure, true, false},
		{gameModeSpectator, true, true},
	}
	for _, c := range cases {
		p := &tickPlayer{gameMode: c.mode}
		if got := blockActionRestricted(p); got != c.restricted {
			t.Errorf("mode %d: blockActionRestricted = %v, want %v", c.mode, got, c.restricted)
		}
		if got := isSpectatorMode(p); got != c.specAttack {
			t.Errorf("mode %d: isSpectatorMode = %v, want %v", c.mode, got, c.specAttack)
		}
	}
}
