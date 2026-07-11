package server

// cat_behavior_test.go -- the Cat CHARACTER-LAYER pins that complement cat_taming_test.go /
// cat_comfort_test.go: (1) fish-feeding TAMES an untamed cat on the 1-in-3 nextInt(3)==0 roll, and (2) a
// WILD (untamed) cat FLEES a nearby player via the Go-native CatAvoidEntityGoal (kind="cat_avoid_player"),
// while a TAMED cat never flees (the !isTame() gate reproduces Cat.reassessTameGoals' remove-on-tame). A
// 1:1 port of net.minecraft.world.entity.animal.feline.Cat$CatAvoidEntityGoal (javap this session). The
// pig oracle is untouched (cat-only goal; a passive pig declares no avoid goal).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
)

// TestCatFeedingTamesOnRoll: an untamed cat right-clicked with COD on the tame-SUCCESS roll (nextInt(3)
// == 0) becomes tame; on a FAIL roll it stays wild (the fish is consumed either way). This mirrors the
// wolf 1-in-3 tame, tamed by CAT_FOOD (cod/salmon). Cite Cat.mobInteract (tryToTame) + Cat.tryToTame.
func TestCatFeedingTamesOnRoll(t *testing.T) {
	// SUCCESS seed: nextInt(3) == 0.
	loop := NewTickLoop(newFakeClock())
	cat := newTestCat(wolfTameSuccessSeed)
	p := newTestPlayerHolding(loop, 42, int32(item.Cod.ID))
	if r := newEntityRandom(wolfTameSuccessSeed).nextInt(3); r != 0 {
		t.Fatalf("precondition: success seed nextInt(3) = %d, want 0", r)
	}
	if !loop.tryCatInteract(p, cat) {
		t.Fatal("cod feed on an untamed cat returned false, want true (interact consumed)")
	}
	if !cat.tame {
		t.Fatal("cat.tame = false after a tame-success cod feed, want true (the roll tamed it)")
	}

	// FAIL seed: nextInt(3) != 0 -> the fish is consumed but the cat stays wild.
	loop2 := NewTickLoop(newFakeClock())
	cat2 := newTestCat(wolfTameFailSeed)
	p2 := newTestPlayerHolding(loop2, 43, int32(item.Salmon.ID))
	if r := newEntityRandom(wolfTameFailSeed).nextInt(3); r == 0 {
		t.Fatalf("precondition: fail seed nextInt(3) = 0, want != 0")
	}
	if !loop2.tryCatInteract(p2, cat2) {
		t.Fatal("salmon feed on an untamed cat returned false, want true (fish eaten on a fail roll)")
	}
	if cat2.tame {
		t.Fatal("cat.tame = true after a tame-FAIL feed, want false (the roll did not tame it)")
	}
}

// TestWildCatFleesPlayer: a WILD (untamed) cat with a nearby player fires CatAvoidEntityGoal.canUse
// (flee), committing a want target AWAY from the player on start. A TAMED cat's canUse is always false
// (the !isTame() gate), so it never flees. Cite Cat.reassessTameGoals + Cat$CatAvoidEntityGoal.
func TestWildCatFleesPlayer(t *testing.T) {
	loop, _ := newFluidLoop()
	cat := newTestCat(1)
	cat.x, cat.y, cat.z = 8.5, 64, 8.5
	cat.tame = false // WILD
	loop.only().entities.add(cat)
	// A player within the 16-block avoid radius.
	p := &tickPlayer{entityID: 55, x: 11.5, y: 64, z: 8.5, health: maxHealth}
	loop.players = append(loop.players, p)

	g := newCatAvoidPlayerGoal()

	// WILD cat + a nearby player -> canUse fires (a flee pos away from the player is found + committed).
	if !g.canUse(loop, cat) {
		t.Fatal("wild cat avoid canUse false with a player 3 blocks away, want true (a wild cat flees)")
	}
	g.start(loop, cat)
	if cat.ai == nil || !cat.ai.hasTarget {
		t.Fatal("wild cat did not commit a flee want on start (should path away from the player)")
	}
	// The committed flee pos must be FARTHER from the player than the cat currently is (do not flee toward
	// the player) -- the goal's own acceptance gate guarantees this.
	dWant := (g.wantX-p.x)*(g.wantX-p.x) + (g.wantZ-p.z)*(g.wantZ-p.z)
	dCat := (cat.x-p.x)*(cat.x-p.x) + (cat.z-p.z)*(cat.z-p.z)
	if dWant < dCat {
		t.Fatalf("flee want (%.1f,%.1f) is CLOSER to the player than the cat is (%.1f vs %.1f), want farther", g.wantX, g.wantZ, dWant, dCat)
	}

	// A TAMED cat never flees (the !isTame() gate short-circuits before the scan).
	tamed := newTestCat(1)
	tamed.x, tamed.y, tamed.z = 8.5, 64, 8.5
	tamed.tame = true
	loop.only().entities.add(tamed)
	g2 := newCatAvoidPlayerGoal()
	if g2.canUse(loop, tamed) {
		t.Fatal("TAMED cat avoid canUse true, want false (a tamed cat never flees players)")
	}
}
