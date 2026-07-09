package server

// fox_avoid_test.go — the Fox CHARACTER-LAYER AvoidEntityGoal wiring tests (1:1 jar port):
// player/wolf/polar-bear avoidance. Each test drives the goal methods directly with a
// deterministic per-entity rng (reseeded by id) and asserts the jar-faithful gating +
// transitions. The pig oracle is byte-identically untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// foxAvoidTestMobWithFaction builds a fox + a faction entity (wolf/polar-bear) at given
// positions. The mob lives in the entity store; the player lives in t.players (separate path).
func foxAvoidTestMobWithFaction(t *testing.T, factionType entity.Entity, factionTame bool, fx, fy, fz, mx, my, mz int) (*TickLoop, *Entity, *Entity) {
	t.Helper()
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 200
	fox := foxTestMob(int32(fx*1000+mx), float64(fx), float64(fy), float64(fz))
	loop.only().entities.add(fox)
	mob := NewEntity(int32(mx*1000+fy), factionType, float64(mx), float64(my), float64(mz))
	mob.health = 12.0
	mob.tame = factionTame
	loop.only().entities.add(mob)
	return loop, fox, mob
}

// foxAddTestPlayer adds a tickPlayer at (x,y,z) for the player-avoid test path. Players live
// in t.players (not the entity store), so the fox's player-avoid seam (newAvoidEntityGoalPlayer)
// finds them via nearestPlayerIDAt.
func foxAddTestPlayer(loop *TickLoop, x, y, z float64) *tickPlayer {
	p := &tickPlayer{x: x, y: y, z: z, entityID: 9000}
	loop.players = append(loop.players, p)
	return p
}

func TestFoxAvoidPlayerWithinRange(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 200
	fox := foxTestMob(9000, 0, 64, 0)
	loop.only().entities.add(fox)
	// A player 5 blocks east (+X) — within foxAvoidPlayerMaxDist=16.
	foxAddTestPlayer(loop, fox.x+5, fox.y, fox.z)

	g := newFoxAvoidPlayerGoal()
	if !g.canUse(loop, fox) {
		t.Fatal("fox_avoid_player.canUse did not fire for a player within 16 blocks")
	}
	if g.toAvoid == 0 {
		t.Fatal("fox_avoid_player.toAvoid is 0 after canUse fired")
	}
}

func TestFoxAvoidPlayerOutOfRange(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 200
	fox := foxTestMob(9010, 0, 64, 0)
	loop.only().entities.add(fox)
	// A player 20 blocks east — beyond foxAvoidPlayerMaxDist=16.
	foxAddTestPlayer(loop, fox.x+20, fox.y, fox.z)

	g := newFoxAvoidPlayerGoal()
	if g.canUse(loop, fox) {
		t.Fatal("fox_avoid_player.canUse fired for a player 20 blocks away (out of 16-block range)")
	}
}

func TestFoxAvoidPlayerTrustedSkipped(t *testing.T) {
	// A fox trusts player (player id is in foxTrusted0); the avoid predicate drops a trusted player.
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 200
	fox := foxTestMob(9020, 0, 64, 0)
	loop.only().entities.add(fox)
	// A player 5 blocks east; mark the player id as trusted.
	p := foxAddTestPlayer(loop, fox.x+5, fox.y, fox.z)
	fox.foxTrusted0 = p.entityID

	g := newFoxAvoidPlayerGoal()
	if g.canUse(loop, fox) {
		t.Fatal("fox_avoid_player.canUse fired for a TRUSTED player (the !fox.trusts gate failed)")
	}
}

func TestFoxAvoidWolfWithinRange(t *testing.T) {
	// A wild (untamed) wolf within 8 blocks triggers the wolf-avoid.
	loop, fox, _ := foxAvoidTestMobWithFaction(t, entity.Wolf, false, 0, 64, 0, 3, 64, 0)

	g := newFoxAvoidWolfGoal()
	if !g.canUse(loop, fox) {
		t.Fatal("fox_avoid_wolf.canUse did not fire for a WILD wolf within 8 blocks")
	}
}

func TestFoxAvoidWolfTamedSkipped(t *testing.T) {
	// A tamed wolf is NOT a threat to a fox (the !wolf.isTame() gate).
	loop, fox, _ := foxAvoidTestMobWithFaction(t, entity.Wolf, true, 0, 64, 0, 3, 64, 0)

	g := newFoxAvoidWolfGoal()
	if g.canUse(loop, fox) {
		t.Fatal("fox_avoid_wolf.canUse fired for a TAMED wolf (the !isTame gate failed)")
	}
}

func TestFoxAvoidWolfOutOfRange(t *testing.T) {
	// A wild wolf 20 blocks away — outside 8-block range.
	loop, fox, _ := foxAvoidTestMobWithFaction(t, entity.Wolf, false, 0, 64, 0, 20, 64, 0)

	g := newFoxAvoidWolfGoal()
	if g.canUse(loop, fox) {
		t.Fatal("fox_avoid_wolf.canUse fired for a wolf 20 blocks away (out of 8-block range)")
	}
}

func TestFoxAvoidPolarBearWithinRange(t *testing.T) {
	// A polar bear within 8 blocks triggers the avoid (no tame filter on polar bears).
	loop, fox, _ := foxAvoidTestMobWithFaction(t, entity.PolarBear, false, 0, 64, 0, 3, 64, 0)

	g := newFoxAvoidPolarBearGoal()
	if !g.canUse(loop, fox) {
		t.Fatal("fox_avoid_polar_bear.canUse did not fire for a polar bear within 8 blocks")
	}
}

func TestFoxAvoidPolarBearOutOfRange(t *testing.T) {
	// A polar bear 20 blocks away — outside 8-block range.
	loop, fox, _ := foxAvoidTestMobWithFaction(t, entity.PolarBear, false, 0, 64, 0, 20, 64, 0)

	g := newFoxAvoidPolarBearGoal()
	if g.canUse(loop, fox) {
		t.Fatal("fox_avoid_polar_bear.canUse fired for a polar bear 20 blocks away (out of 8-block range)")
	}
}

func TestFoxAvoidPlayerDefendingSkipped(t *testing.T) {
	// A fox that is DEFENDING ignores the player-avoid (the !isDefending gate).
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 200
	fox := foxTestMob(9030, 0, 64, 0)
	foxSetDefending(fox, true)
	loop.only().entities.add(fox)
	foxAddTestPlayer(loop, fox.x+5, fox.y, fox.z)

	g := newFoxAvoidPlayerGoal()
	if g.canUse(loop, fox) {
		t.Fatal("fox_avoid_player.canUse fired for a DEFENDING fox (the !isDefending gate failed)")
	}
}
