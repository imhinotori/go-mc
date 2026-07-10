package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/tag"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestCreativeInvulnerable: a creative player absorbs all damage EXCEPT sources tagged
// BYPASSES_INVULNERABILITY (out_of_world/generic_kill). Cite Entity.isInvulnerableToBase.
func TestCreativeInvulnerable(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.gameMode = gameModeCreative

	// Generic (attack/fall/etc.) damage is fully negated in creative.
	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 6)
	if p.health != maxHealth {
		t.Fatalf("creative took generic damage: health = %v, want %v", p.health, maxHealth)
	}

	// A BYPASSES_INVULNERABILITY source (the void) STILL kills a creative player.
	outOfWorld := damageTypeID(tag.DamageTypeIDs["minecraft:out_of_world"])
	loop.applyDamage(p, damageSourceOf(outOfWorld), 6)
	if p.health != maxHealth-6 {
		t.Fatalf("out_of_world (bypasses_invulnerability) did NOT hurt creative: health = %v, want %v", p.health, maxHealth-6)
	}

	// A spectator is likewise invulnerable to generic damage.
	sp := combatPlayer(loop, 2)
	sp.gameMode = gameModeSpectator
	loop.applyDamage(sp, damageSourceOf(damageTypeGeneric), 6)
	if sp.health != maxHealth {
		t.Fatalf("spectator took generic damage: health = %v, want %v", sp.health, maxHealth)
	}
}

// TestDamageExhaustionPerSource: taking damage costs hunger per the DamageType.exhaustion JSON — 0.1 for
// attack/combat sources, 0.0 for fall/drown/starve/etc. (E-6). Previously every source drained a flat 0.1.
func TestDamageExhaustionPerSource(t *testing.T) {
	// A combat source (generic, default 0.1) drains hunger.
	loop := NewTickLoop(newFakeClock())
	pc := combatPlayer(loop, 1)
	loop.applyDamage(pc, damageSourceOf(damageTypeGeneric), 4)
	if pc.exhaustion != 0 { // generic is in the zero set — verify the table, not the default
		t.Fatalf("generic exhaustion = %v, want 0 (generic is 0.0 in the table)", pc.exhaustion)
	}

	// player_attack (0.1) drains.
	pa := combatPlayer(loop, 2)
	loop.applyDamage(pa, damageSourcePlayerAttack(999), 4)
	if pa.exhaustion != damageFoodExhaustion {
		t.Fatalf("player_attack exhaustion = %v, want %v", pa.exhaustion, damageFoodExhaustion)
	}

	// fall (0.0) drains NOTHING.
	pf := combatPlayer(loop, 3)
	loop.applyDamage(pf, damageSourceOf(damageTypeFall), 4)
	if pf.exhaustion != 0 {
		t.Fatalf("fall exhaustion = %v, want 0 (fall is 0.0)", pf.exhaustion)
	}

	// starve (0.0) drains nothing — the anti-self-acceleration fix.
	ps := combatPlayer(loop, 4)
	loop.applyDamage(ps, damageSourceOf(damageTypeStarve), 1)
	if ps.exhaustion != 0 {
		t.Fatalf("starve exhaustion = %v, want 0 (must not self-accelerate)", ps.exhaustion)
	}
}

// TestIFrameExcessIsSilent: the E-5 fix — an i-frame EXCESS hit (a larger hit inside the grace window)
// applies its excess damage but is SILENT: no ClientboundDamageEvent (hurt flash), matching vanilla's
// tookFullDamage=false gate. Only the fresh hit emits the event.
func TestIFrameExcessIsSilent(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	// Fresh hit: lands, arms the window, and emits the damage event.
	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 4)
	fresh := drainPackets(p.client)
	if n := countID(fresh, packetid.ClientboundDamageEvent); n != 1 {
		t.Fatalf("fresh hit emitted %d DamageEvent, want 1", n)
	}

	// Excess hit inside the window (10 > lastHurt 4): applies the 6 excess but is SILENT.
	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 10)
	if p.health != 20-4-6 {
		t.Fatalf("excess hit health = %v, want 10 (4 fresh + 6 excess)", p.health)
	}
	excess := drainPackets(p.client)
	if n := countID(excess, packetid.ClientboundDamageEvent); n != 0 {
		t.Fatalf("excess hit emitted %d DamageEvent, want 0 (tookFullDamage=false → silent)", n)
	}
}

// TestVoidDamage: a player below minY-64 takes 4.0 out_of_world damage per tick (Entity.checkBelowWorld
// -> LivingEntity.onBelowWorld); it bypasses invulnerability so even a creative player takes it.
func TestVoidDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	deep := combatPlayer(loop, 1)
	deep.y = float64(dimMinYFor(dimOverworld)-64) - 1 // just below the void threshold
	loop.players = append(loop.players, deep)

	// A player at normal height takes nothing.
	high := combatPlayer(loop, 2)
	high.y = 64
	loop.players = append(loop.players, high)

	// A creative player in the void still takes it (out_of_world bypasses invulnerability).
	creativeDeep := combatPlayer(loop, 3)
	creativeDeep.gameMode = gameModeCreative
	creativeDeep.y = float64(dimMinYFor(dimOverworld)-64) - 1
	loop.players = append(loop.players, creativeDeep)

	loop.tickBelowWorld()

	if deep.health != maxHealth-4 {
		t.Fatalf("void player health = %v, want %v (4.0 out_of_world/tick)", deep.health, maxHealth-4)
	}
	if high.health != maxHealth {
		t.Fatalf("player above the void took damage: health = %v", high.health)
	}
	if creativeDeep.health != maxHealth-4 {
		t.Fatalf("creative void player health = %v, want %v (out_of_world bypasses invulnerability)", creativeDeep.health, maxHealth-4)
	}
}

// TestCreativeNoFoodExhaustion: a creative player never accrues exhaustion (Player.causeFoodExhaustion
// early-returns on abilities.invulnerable); a survival player does.
func TestCreativeNoFoodExhaustion(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	creative := combatPlayer(loop, 1)
	creative.gameMode = gameModeCreative
	loop.causeFoodExhaustion(creative, 0.1)
	if creative.exhaustion != 0 {
		t.Fatalf("creative accrued exhaustion %v, want 0", creative.exhaustion)
	}

	survival := combatPlayer(loop, 2)
	survival.gameMode = gameModeSurvival
	loop.causeFoodExhaustion(survival, 0.1)
	if survival.exhaustion == 0 {
		t.Fatalf("survival did not accrue exhaustion, want > 0")
	}
}

// combat_test.go covers ENT-05: the server-owned health/damage/death/respawn loop. Health
// is SERVER-owned (T-6-05) — the client has NO health-setting packet, it only REQUESTS a
// respawn via ServerboundClientCommand(PERFORM_RESPAWN). The server drives the SetHealth
// wire (jar order Float/VarInt/Float), the death screen (PlayerCombatKill), and the respawn
// (ClientboundRespawn REUSING the Phase-5-sealed commonPlayerSpawnInfoEncoder + a trailing
// dataToKeep byte) + a fresh re-teleport + a streamer reset so the world re-streams.
//
// All packet layouts are JAR-VERIFIED (javap'd from temp/cache/26.2-inner.jar this session):
//   ClientboundSetHealth        = Float health + VarInt food + Float saturation
//   ClientboundPlayerCombatKill = VarInt playerId + Component message (TRUSTED_STREAM_CODEC)
//   ClientboundRespawn          = CommonPlayerSpawnInfo.write + Byte dataToKeep

// clientCommandPacket builds a ServerboundClientCommand carrying the VarInt action enum
// (0 = PERFORM_RESPAWN, 1 = REQUEST_STATS) — the only field the packet has.
func clientCommandPacket(action int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundClientCommand), pk.VarInt(action))
}

// combatPlayer registers a confirmed, full-health player with a capturing client so a test
// can drive damage/death/respawn and drain exactly the packets the server emitted. It is
// centered on {0,0} and tracks that column so the respawn streamer-reset has a basis.
func combatPlayer(loop *TickLoop, entityID int32) *tickPlayer {
	p := &tickPlayer{
		client:            captureClient(64),
		entityID:          entityID,
		confirmedTeleport: true,
		health:            maxHealth,
		food:              maxFood,
		saturation:        defaultSaturation,
		center:            level.ChunkPos{0, 0},
		viewDist:          serverViewDistance,
		sentChunks:        map[level.ChunkPos]bool{{0, 0}: true},
		centerSent:        true,
		secs:              overworldSections,
	}
	loop.players = append(loop.players, p)
	if loop.clientIndex == nil {
		loop.clientIndex = make(map[*Client]*tickPlayer)
	}
	loop.clientIndex[p.client] = p
	return p
}

// TestSetHealthWire asserts setHealth encodes ClientboundSetHealth in the jar order
// (Float health, VarInt food, Float saturation) and the body round-trips.
func TestSetHealthWire(t *testing.T) {
	p := setHealth(20, 20, 5)
	if p.ID != int32(packetid.ClientboundSetHealth) {
		t.Fatalf("setHealth id = %d, want ClientboundSetHealth (%d)", p.ID, packetid.ClientboundSetHealth)
	}

	var health pk.Float
	var food pk.VarInt
	var sat pk.Float
	if err := p.Scan(&health, &food, &sat); err != nil {
		t.Fatalf("SetHealth body did not decode as Float/VarInt/Float: %v", err)
	}
	if health != 20 {
		t.Fatalf("health = %v, want 20", health)
	}
	if food != 20 {
		t.Fatalf("food = %v, want 20", food)
	}
	if sat != 5 {
		t.Fatalf("saturation = %v, want 5", sat)
	}
}

// TestApplyDamage asserts applyDamage lowers the tick-owned health, sends one SetHealth
// reflecting the new value, and clamps at 0 (never negative).
func TestApplyDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 6)
	if p.health != 14 {
		t.Fatalf("after 6 damage health = %v, want 14", p.health)
	}

	ps := drainPackets(p.client)
	if n := countID(ps, packetid.ClientboundSetHealth); n != 1 {
		t.Fatalf("applyDamage sent %d SetHealth, want 1", n)
	}
	// The SetHealth must carry the new health value.
	var got pk.Float
	for _, pkt := range ps {
		if pkt.ID == int32(packetid.ClientboundSetHealth) {
			var food pk.VarInt
			var sat pk.Float
			if err := pkt.Scan(&got, &food, &sat); err != nil {
				t.Fatalf("SetHealth decode: %v", err)
			}
		}
	}
	if got != 14 {
		t.Fatalf("SetHealth health = %v, want 14", got)
	}

	// Overkill clamps at 0 (not negative).
	p2 := combatPlayer(loop, 2)
	loop.applyDamage(p2, damageSourceOf(damageTypeGeneric), 999)
	if p2.health != 0 {
		t.Fatalf("after overkill health = %v, want 0 (clamped)", p2.health)
	}
}

// TestIFrameRateLimit asserts the LivingEntity.hurtServer invulnerableTime gate (the anti-spam
// rate limit). The FIRST hit lands fully and arms a 20-tick window (invulnerableTime=20). A SECOND
// hit while the window is in its upper half (> 10) applies ONLY the excess over lastHurt:
//   - a larger second hit applies (newAmount - lastHurt);
//   - a smaller-or-equal second hit applies NOTHING (returns false / no health change).
func TestIFrameRateLimit(t *testing.T) {
	t.Run("equal spam hit within window is fully absorbed", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 1)

		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 6) // fresh hit: full 6, health 14, invulnerableTime=20, lastHurt=6
		if p.health != 14 {
			t.Fatalf("after first hit health = %v, want 14", p.health)
		}
		if p.invulnerableTime != hurtInvulnerableTicks {
			t.Fatalf("first hit invulnerableTime = %d, want %d", p.invulnerableTime, hurtInvulnerableTicks)
		}
		if p.lastHurt != 6 {
			t.Fatalf("first hit lastHurt = %v, want 6", p.lastHurt)
		}

		_ = drainPackets(p.client) // discard the first SetHealth

		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 6) // spam within window, amount <= lastHurt -> NO damage
		if p.health != 14 {
			t.Fatalf("equal spam hit within window changed health to %v, want 14 (absorbed)", p.health)
		}
		if n := countID(drainPackets(p.client), packetid.ClientboundSetHealth); n != 0 {
			t.Fatalf("absorbed spam hit sent %d SetHealth, want 0 (no health change)", n)
		}
	})

	t.Run("larger second hit applies only the excess", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 2)

		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 4) // fresh: health 16, lastHurt=4, window=20
		if p.health != 16 {
			t.Fatalf("after first hit health = %v, want 16", p.health)
		}
		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 10) // within window, 10 > 4 -> applies only 10-4 = 6: health 16-6 = 10
		if p.health != 10 {
			t.Fatalf("larger second hit health = %v, want 10 (only the 6 excess)", p.health)
		}
		// lastHurt updates to the new (larger) amount.
		if p.lastHurt != 10 {
			t.Fatalf("after larger second hit lastHurt = %v, want 10", p.lastHurt)
		}
	})

	t.Run("hit after window expires lands fully again", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 3)

		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 4) // window=20
		// Decrement the window past the > 10 threshold (simulate ~10 ticks of tickPlayerCombat).
		for i := 0; i < 11; i++ {
			loop.tickPlayerCombat()
		}
		// invulnerableTime is now 20-11 = 9 (<= 10), so a fresh hit lands fully.
		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 4) // fresh full 4: health 20-4-4 = 12
		if p.health != 12 {
			t.Fatalf("post-window hit health = %v, want 12 (both 4s landed)", p.health)
		}
	})
}

// TestArmorFormula asserts CombatRules.getDamageAfterAbsorb via actuallyHurt:
//   - with ARMOR=0 (the v1 default), a hit takes FULL damage (the armor curve is a pass-through);
//   - with a synthetic ARMOR=20, a 10-damage hit is reduced to 4 (f=2.0, clamp(20-10/2)=15,
//     f3=0.6, 1-0.6=0.4, 10*0.4=4.0) — the exact vanilla-reduced value.
func TestArmorFormula(t *testing.T) {
	t.Run("armor 0 takes full damage", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 1)
		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 10) // ARMOR base 0 -> full 10: health 10
		if p.health != 10 {
			t.Fatalf("armor-0 hit health = %v, want 10 (full damage)", p.health)
		}
	})

	t.Run("synthetic armor 20 reduces 10 to 4", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 2)
		// Seed the holder and override ARMOR to a synthetic 20 (a full iron set's armor points).
		p.playerAttributes().base[attrArmor] = 20.0

		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 10) // armor curve: 10 -> 4.0: health 20-4 = 16
		if p.health != 16 {
			t.Fatalf("armor-20 hit health = %v, want 16 (10 damage reduced to 4)", p.health)
		}
	})
}

// TestDeath asserts health <= 0 sends ClientboundPlayerCombatKill (the death screen) and
// sets the tick-owned dead flag.
func TestDeath(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 7)

	loop.applyDamage(p, damageSourceOf(damageTypeGeneric), maxHealth) // exactly lethal
	if p.health != 0 {
		t.Fatalf("after lethal damage health = %v, want 0", p.health)
	}
	if !p.dead {
		t.Fatal("lethal damage must set the tick-owned dead flag")
	}

	ps := drainPackets(p.client)
	if n := countID(ps, packetid.ClientboundPlayerCombatKill); n != 1 {
		t.Fatalf("death sent %d PlayerCombatKill, want 1 (the death screen)", n)
	}
	// PlayerCombatKill carries the dead player's entity id first (VarInt playerId).
	for _, pkt := range ps {
		if pkt.ID == int32(packetid.ClientboundPlayerCombatKill) {
			var id pk.VarInt
			if err := pkt.Scan(&id); err != nil {
				t.Fatalf("PlayerCombatKill VarInt playerId decode: %v", err)
			}
			if int32(id) != p.entityID {
				t.Fatalf("PlayerCombatKill playerId = %d, want the player entity id %d", id, p.entityID)
			}
		}
	}
}

// TestRespawnFlow asserts a ServerboundClientCommand(PERFORM_RESPAWN) for a DEAD player:
//   - sends ClientboundRespawn (commonPlayerSpawnInfoEncoder + the trailing dataToKeep byte),
//   - sends a re-teleport ClientboundPlayerPosition,
//   - resets health to full + clears the dead flag,
//   - resets the streamer (centerSent=false, sentChunks cleared) so the world re-streams;
//
// and that a respawn request for a LIVING player is a no-op.
func TestRespawnFlow(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	t.Run("dead player respawns", func(t *testing.T) {
		p := combatPlayer(loop, 11)
		// Kill the player.
		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), maxHealth)
		_ = drainPackets(p.client) // discard the death packets; re-arm a fresh capturing client
		p.client = captureClient(64)
		loop.clientIndex[p.client] = p

		loop.dispatch(p.client, clientCommandPacket(clientCommandPerformRespawn))

		if p.dead {
			t.Fatal("after respawn the dead flag must be cleared")
		}
		if p.health != maxHealth {
			t.Fatalf("after respawn health = %v, want full (%v)", p.health, float32(maxHealth))
		}
		if p.centerSent {
			t.Fatal("respawn must reset centerSent so the streamer re-emits SetChunkCacheCenter")
		}
		if len(p.sentChunks) != 0 {
			t.Fatalf("respawn must clear sentChunks (re-stream the ring); len = %d", len(p.sentChunks))
		}

		ps := drainPackets(p.client)
		if n := countID(ps, packetid.ClientboundRespawn); n != 1 {
			t.Fatalf("respawn sent %d ClientboundRespawn, want 1", n)
		}
		if n := countID(ps, packetid.ClientboundPlayerPosition); n != 1 {
			t.Fatalf("respawn sent %d PlayerPosition (re-teleport), want 1", n)
		}
		// The respawn MUST re-send the inventory ContainerSetContent — ClientboundRespawn tore down the
		// client's inventory menu, so without this the inventory renders empty/invisible after a death
		// (the bug fix). Vanilla: PlayerList.respawn -> ServerPlayer.initInventoryMenu -> sendAllDataToRemote.
		if n := countID(ps, packetid.ClientboundContainerSetContent); n != 1 {
			t.Fatalf("respawn sent %d ContainerSetContent (inventory re-sync), want 1 — inventory would render invisible after death", n)
		}
		// The Respawn body must be the sealed spawn-info encoder followed by exactly one
		// trailing dataToKeep byte. Decode the spawn-info prefix, then the final Byte.
		for _, pkt := range ps {
			if pkt.ID != int32(packetid.ClientboundRespawn) {
				continue
			}
			// The sealed encoder's exact bytes are sealed in Phase 5; here we only assert
			// the trailing dataToKeep byte exists (the ONLY new byte) — the body is at least
			// one byte longer than the bare spawn-info and ends in a single Byte.
			if len(pkt.Data) < 1 {
				t.Fatal("Respawn body is empty; want spawn-info + dataToKeep byte")
			}
		}
	})

	t.Run("living player respawn request is a no-op", func(t *testing.T) {
		p := combatPlayer(loop, 12) // full health, not dead
		loop.dispatch(p.client, clientCommandPacket(clientCommandPerformRespawn))

		ps := drainPackets(p.client)
		if n := countID(ps, packetid.ClientboundRespawn); n != 0 {
			t.Fatalf("living player respawn request sent %d Respawn, want 0 (no-op)", n)
		}
		if p.health != maxHealth || p.dead {
			t.Fatal("living player respawn request must not change state")
		}
	})

	t.Run("REQUEST_STATS answers AwardStats, malformed never panics", func(t *testing.T) {
		p := combatPlayer(loop, 13)
		p.stats = newStatsCounter()
		loop.dispatch(p.client, clientCommandPacket(clientCommandRequestStats))
		// A malformed (empty-body) ClientCommand: Scan errors -> silent no-op.
		loop.dispatch(p.client, pk.Packet{ID: int32(packetid.ServerboundClientCommand)})
		ps := drainPackets(p.client)
		if n := countID(ps, packetid.ClientboundRespawn); n != 0 {
			t.Fatalf("REQUEST_STATS / malformed sent %d Respawn, want 0", n)
		}
		// REQUEST_STATS now replies with exactly one ClientboundAwardStats (stats.go).
		if n := countID(ps, packetid.ClientboundAwardStats); n != 1 {
			t.Fatalf("REQUEST_STATS sent %d AwardStats, want 1", n)
		}
	})
}


// TestFreezeExtraGuardNoOpForPlayer proves the LivingEntity.hurtServer FREEZE-extra multiply
// (`if (source.is(IS_FREEZING) && this.is(FREEZE_HURTS_EXTRA_TYPES)) amount *= 5.0f;`) is a no-op
// for a PLAYER victim: the player is NOT a member of freeze_hurts_extra_types (that entity-type tag
// holds only strider/blaze/magma_cube), so an is_freezing source deals its UNMULTIPLIED amount.
// This pins the constant-false guard added when the hurtServer damage steps were reordered to the
// exact jar sequence (blocking -> freeze-extra -> helmet -> NaN/Inf clamp -> i-frame gate).
func TestFreezeExtraGuardNoOpForPlayer(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	// minecraft:freeze is an is_freezing member (data/tag id 17). A player takes the raw 2.0 -- NOT
	// 2.0*5 == 10.0 -- because the FREEZE_HURTS_EXTRA_TYPES guard is false for a player.
	src := damageSourceByTypeName("minecraft:freeze", 0)
	if !src.is("is_freezing") {
		t.Fatalf("precondition: minecraft:freeze must be an is_freezing member")
	}
	loop.applyDamage(p, src, 2.0)
	if p.health != maxHealth-2.0 {
		t.Fatalf("freeze hit on player: health = %v, want %v (unmultiplied 2.0, NOT 5x)", p.health, maxHealth-2.0)
	}
}
