package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestMobLavaDamageAndIgnite: a living mob standing in lava takes 4.0 lava damage/tick, is ignited for
// 15s (300 fire ticks), and has its fallDistance halved (LavaFluid.entityInside → lavaIgnite + lavaHurt;
// Entity.baseTick fallDistance *= 0.5). A dry mob takes nothing.
func TestMobLavaDamageAndIgnite(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	hot := NewEntity(1, entity.Zombie, 8.5, 64.0, 8.5)
	hot.health = 20
	hot.ai = &mobAI{}
	hot.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	hot.fallDistance = 6.0

	loop.tickEntityLava(hot)
	// 4.0 lava damage, minus the mob's armor fold (lava is is_fire, NOT bypasses_armor, so the armor
	// attribute reduces it slightly) — so health drops below 20 but stays near 16, never unchanged.
	if hot.health >= 20 || hot.health < 15 {
		t.Fatalf("mob in lava health = %v, want ~16 (4.0 lava damage, armor-folded)", hot.health)
	}
	if hot.remainingFireTicks != 300 {
		t.Fatalf("mob in lava remainingFireTicks = %d, want 300 (igniteForSeconds 15)", hot.remainingFireTicks)
	}
	if hot.fallDistance != 3.0 {
		t.Fatalf("mob in lava fallDistance = %v, want 3.0 (halved)", hot.fallDistance)
	}

	// A dry mob is a no-op.
	dry := NewEntity(2, entity.Zombie, 100.0, 64.0, 100.0)
	dry.health = 20
	dry.ai = &mobAI{}
	dry.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	loop.tickEntityLava(dry)
	if dry.health != 20 || dry.remainingFireTicks != 0 {
		t.Fatalf("dry mob affected by lava tick: health=%v fire=%d", dry.health, dry.remainingFireTicks)
	}
}

// TestPlayerLavaDamage: a player in lava takes 4.0 lava damage/tick and is ignited; a player out of lava
// takes nothing.
func TestPlayerLavaDamage(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	p := &tickPlayer{x: 8.5, y: 64.0, z: 8.5, health: maxHealth, client: captureClient(16)}
	p.playerEntity = NewEntity(1, entity.Player, 8.5, 64.0, 8.5)
	loop.players = append(loop.players, p)

	loop.tickLavaPlayers()
	if p.health != maxHealth-4 {
		t.Fatalf("player in lava health = %v, want %v (4.0 lava damage)", p.health, maxHealth-4)
	}
	if p.playerEntity.remainingFireTicks != 300 {
		t.Fatalf("player in lava fire ticks = %d, want 300 (igniteForSeconds 15)", p.playerEntity.remainingFireTicks)
	}

	// A player above the lava takes nothing.
	dry := &tickPlayer{x: 100.0, y: 64.0, z: 100.0, health: maxHealth, client: captureClient(16)}
	dry.playerEntity = NewEntity(2, entity.Player, 100.0, 64.0, 100.0)
	loop.players = append(loop.players, dry)
	loop.tickLavaPlayers()
	if dry.health != maxHealth {
		t.Fatalf("dry player took lava damage: health = %v", dry.health)
	}
}
