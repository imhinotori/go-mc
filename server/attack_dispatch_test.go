package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// attack_dispatch_test.go covers GAMEPLAY-04 (the PvP attack half): applyInput now has a
// case for ServerboundAttack — the 26.2 entity-attack packet (jar-verified:
// ServerboundAttackPacket.STREAM_CODEC = a single ByteBufCodecs.VAR_INT entityId, with NO
// Action enum; the old ServerboundInteractPacket{Action} was split and ATTACK got its own
// packet). The server resolves the named target to a tickPlayer, reach-gates it, and applies
// a SERVER-supplied baseAttackDamage (the packet only NAMES a target — T-6-05 / V4). A
// forged/out-of-reach/self/non-attack input is a silent no-op (T-6-04 / V5).

// attackPacket builds a ServerboundAttack carrying the VarInt target entityId — the entire
// 26.2 wire body (ServerboundAttackPacket = VarInt entityId).
func attackPacket(targetID int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundAttack), pk.VarInt(targetID))
}

// placeAttackPlayer registers a confirmed combat player at the given position so reach can be
// exercised. It reuses combatPlayer (full health, capturing client, confirmedTeleport) and
// then positions it.
func placeAttackPlayer(loop *TickLoop, entityID int32, x, y, z float64) *tickPlayer {
	p := combatPlayer(loop, entityID)
	p.x, p.y, p.z = x, y, z
	return p
}

// TestAttackDispatchDamages: A attacks B in reach -> B.health drops by baseAttackDamage and
// a SetHealth was sent to B.
func TestAttackDispatchDamages(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	victim := placeAttackPlayer(loop, 2, 1, 64, 0) // 1 block away, well within reach

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	want := maxHealth - baseAttackDamage
	if victim.health != want {
		t.Fatalf("after attack victim health = %v, want %v", victim.health, want)
	}
	ps := drainPackets(victim.client)
	if n := countID(ps, packetid.ClientboundSetHealth); n != 1 {
		t.Fatalf("attack sent %d SetHealth to victim, want 1", n)
	}
}

// TestAttackOutOfReach: A attacks B beyond reach -> no damage (silent no-op).
func TestAttackOutOfReach(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	victim := placeAttackPlayer(loop, 2, 100, 64, 0) // far away

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	if victim.health != maxHealth {
		t.Fatalf("out-of-reach attack changed health to %v, want %v (no-op)", victim.health, float32(maxHealth))
	}
	if n := countID(drainPackets(victim.client), packetid.ClientboundSetHealth); n != 0 {
		t.Fatalf("out-of-reach attack sent %d SetHealth, want 0", n)
	}
}

// TestAttackForgedTarget: ATTACK an unknown entity id -> nil victim, no panic, no damage.
func TestAttackForgedTarget(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)

	// 9999 is not a registered entity id.
	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(9999)})

	if victim.health != maxHealth {
		t.Fatalf("forged-target attack changed an unrelated victim's health to %v", victim.health)
	}
}

// TestAttackSelf: A attacks its own entityID -> no damage (self-attack guard).
func TestAttackSelf(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(attacker.entityID)})

	if attacker.health != maxHealth {
		t.Fatalf("self-attack changed health to %v, want %v (guarded)", attacker.health, float32(maxHealth))
	}
	if n := countID(drainPackets(attacker.client), packetid.ClientboundSetHealth); n != 0 {
		t.Fatalf("self-attack sent %d SetHealth, want 0", n)
	}
}

// TestAttackLethalDeath: B at 1 HP attacked -> health 0, die() called (dead flag set).
func TestAttackLethalDeath(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)
	victim.health = baseAttackDamage // exactly lethal with one bare-hand hit

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	if victim.health != 0 {
		t.Fatalf("lethal attack health = %v, want 0", victim.health)
	}
	if !victim.dead {
		t.Fatal("lethal attack must set the dead flag (die() called)")
	}
	if n := countID(drainPackets(victim.client), packetid.ClientboundPlayerCombatKill); n != 1 {
		t.Fatal("lethal attack must send PlayerCombatKill (the death screen)")
	}
}

// TestAttackMalformedNoPanic: an empty-body ServerboundAttack (Scan fails) is a silent no-op,
// never a panic (T-6-04 / V5 defensive decode).
func TestAttackMalformedNoPanic(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)

	// must not panic
	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: pk.Packet{ID: int32(packetid.ServerboundAttack)}})
}
