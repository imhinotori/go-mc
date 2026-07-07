package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// despawnMob builds a live AI zombie (categoryMonster => despawnDistance 128) at (x,y,z), registered in
// the single region with a seeded rng so checkDespawn's gated nextInt(800) is deterministic if reached.
func despawnMob(loop *TickLoop, id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Zombie, x, y, z)
	e.health = 20
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	loop.only().entities.add(e)
	return e
}

// TestCheckDespawnInstantCullFarFromPlayer: a mob past despawnDistance² (128² = 16384) from the nearest
// player is culled outright (Mob.checkDespawn instant branch).
func TestCheckDespawnInstantCullFarFromPlayer(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mob := despawnMob(loop, 1, 0, 64, 0)
	// Player 200 blocks away on X: d = 40000 > 16384.
	loop.players = append(loop.players, &tickPlayer{x: 200, y: 64, z: 0, health: maxHealth})

	loop.checkDespawn(mob)
	if !mob.dead {
		t.Fatal("mob 200 blocks from the nearest player should be instantly culled (d > 128²)")
	}
	if _, ok := loop.only().entities.get(mob.id); ok {
		t.Fatal("culled mob must be removed from its region (discard())")
	}
}

// TestCheckDespawnKeepsNearMobAndResetsIdle: a mob within noDespawnDistance² (32² = 1024) of a player is
// never culled and its idle counter resets to 0 (the else-branch).
func TestCheckDespawnKeepsNearMobAndResetsIdle(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mob := despawnMob(loop, 1, 0, 64, 0)
	mob.ai.noActionTime = 1200 // idle for a while
	loop.players = append(loop.players, &tickPlayer{x: 10, y: 64, z: 0, health: maxHealth})

	loop.checkDespawn(mob)
	if mob.dead {
		t.Fatal("mob 10 blocks from a player must NOT despawn (d < 32²)")
	}
	if mob.ai.noActionTime != 0 {
		t.Fatalf("noActionTime not reset near a player: %d, want 0", mob.ai.noActionTime)
	}
}

// TestCheckDespawnPersistenceRequired: a persistence-required mob never despawns even far from players,
// and its idle counter resets (the early-return branch).
func TestCheckDespawnPersistenceRequired(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mob := despawnMob(loop, 1, 0, 64, 0)
	mob.ai.persistenceRequired = true
	mob.ai.noActionTime = 1200
	loop.players = append(loop.players, &tickPlayer{x: 500, y: 64, z: 0, health: maxHealth})

	loop.checkDespawn(mob)
	if mob.dead {
		t.Fatal("persistence-required mob must never despawn")
	}
	if mob.ai.noActionTime != 0 {
		t.Fatalf("persistence-required early return must reset noActionTime: %d, want 0", mob.ai.noActionTime)
	}
}

// TestCheckDespawnNoPlayersIsNoop: with no players present getNearestPlayer returns null, so checkDespawn
// returns early without culling (a lone mob in an empty world is not removed by this path).
func TestCheckDespawnNoPlayersIsNoop(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mob := despawnMob(loop, 1, 0, 64, 0)

	loop.checkDespawn(mob)
	if mob.dead {
		t.Fatal("no players => getNearestPlayer null => no cull")
	}
}

// TestCheckDespawnSpectatorIgnored: a spectator player does not count as "near" (NO_SPECTATORS), so a mob
// with only a nearby spectator is treated as playerless and not reset/culled by proximity.
func TestCheckDespawnSpectatorIgnored(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mob := despawnMob(loop, 1, 0, 64, 0)
	mob.ai.noActionTime = 1200
	loop.players = append(loop.players, &tickPlayer{x: 5, y: 64, z: 0, health: maxHealth, gameMode: gameModeSpectator})

	loop.checkDespawn(mob)
	// No non-spectator player => nearestPlayerNoSpectator nil => early return, idle NOT reset.
	if mob.ai.noActionTime != 1200 {
		t.Fatalf("a spectator must not reset noActionTime: %d, want 1200 (NO_SPECTATORS)", mob.ai.noActionTime)
	}
}
