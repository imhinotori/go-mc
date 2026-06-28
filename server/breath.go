package server

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// breath.go (Plan 17-13) is the 1:1 port of the vanilla air-supply / drowning logic from
// net.minecraft.world.entity.LivingEntity.baseTick and net.minecraft.world.entity.Entity's air
// accessors. ALL constants are VERIFIED via javap on temp/cache/26.2-inner.jar; each is cited at
// its use site. The single owner is the tick goroutine (tickBreath runs inside tickEntities), so
// airSupply is -race clean by the same single-owner discipline as the rest of tickPlayer.
//
// Cited bytecode (paths relative to temp/cache/26.2-inner.jar):
//
//	net.minecraft.world.entity.Entity.getMaxAirSupply:           `sipush 300; ireturn`           => 300
//	net.minecraft.world.entity.Entity ctor:                      `define(DATA_AIR_SUPPLY_ID, getMaxAirSupply())` => default 300
//	net.minecraft.world.entity.LivingEntity.getWaterSlowDown:    (movement; see fluid_physics.go)
//	net.minecraft.world.entity.LivingEntity.decreaseAirSupply:   OXYGEN_BONUS==0 for a bare player => `iload_1; iconst_1; isub; ireturn` => air-1
//	net.minecraft.world.entity.LivingEntity.increaseAirSupply:   `iload_1; iconst_4; iadd; getMaxAirSupply; Math.min; ireturn` => min(air+4, 300)
//	net.minecraft.world.entity.LivingEntity.shouldTakeDrowningDamage: `getAirSupply; bipush -20; if_icmpgt 13; iconst_1...` => air <= -20
//	net.minecraft.world.entity.LivingEntity.baseTick (water branch): on submerged-eyes:
//	    setAirSupply(decreaseAirSupply(getAirSupply()));
//	    if (shouldTakeDrowningDamage()) { setAirSupply(0); broadcastEntityEvent(67); hurtServer(DROWN, 2.0F); }
//	    else if (getAirSupply() < getMaxAirSupply() && shouldEffectsRefillAirsupply()) setAirSupply(increaseAirSupply(getAirSupply()));
//	  on NOT-submerged:
//	    if (getAirSupply() < getMaxAirSupply()) setAirSupply(increaseAirSupply(getAirSupply()));

// Breath / drowning constants (VERIFIED via javap — see file header).
const (
	// maxAirSupply is Entity.getMaxAirSupply() == 300 (`sipush 300; ireturn`). It is both the bubble
	// bar's full value and the fresh-player default (Entity ctor seeds DATA_AIR_SUPPLY_ID to it).
	maxAirSupply int32 = 300

	// airRefillPerTick is the increaseAirSupply increment == 4 (`iconst_4; iadd`): out of water (and
	// while not drowning) air climbs by 4/tick, clamped to maxAirSupply.
	airRefillPerTick int32 = 4

	// drowningThreshold is shouldTakeDrowningDamage()'s threshold: air <= -20 (`bipush -20;
	// if_icmpgt`). At or below it the entity resets air to 0 and takes drowning damage.
	drowningThreshold int32 = -20

	// drownDamage is the DROWN damage amount == 2.0F (`fconst_2`) passed to hurtServer in baseTick.
	drownDamage float32 = 2.0

	// playerStandingEyeHeight is the player's standing eye height (1.8 height * 0.9 standing scale ==
	// 1.62). baseTick's air branch gates on isEyeInFluid(WATER) — the EYES, not the feet — so the
	// water sample must be taken at getEyeY() == y + eyeHeight, not at the feet.
	playerStandingEyeHeight = 1.62
)

// decreaseAirSupply is the bare-player port of LivingEntity.decreaseAirSupply(int): with the
// OXYGEN_BONUS attribute at its base 0 (no Respiration enchant) the random-skip branch never fires
// (`d <= 0.0` -> falls through to `iload_1; iconst_1; isub`), so it is unconditionally air-1. The
// attribute parameter is omitted because v1 has no OXYGEN_BONUS source; when one is added this is
// where the `nextDouble() >= 1/(d+1)` skip slots in with no caller change.
func decreaseAirSupply(air int32) int32 {
	return air - 1
}

// increaseAirSupply is the port of LivingEntity.increaseAirSupply(int): `Math.min(air+4, max)`.
func increaseAirSupply(air int32) int32 {
	v := air + airRefillPerTick
	if v > maxAirSupply {
		return maxAirSupply
	}
	return v
}

// shouldTakeDrowningDamage is the port of LivingEntity.shouldTakeDrowningDamage(): air <= -20.
func shouldTakeDrowningDamage(air int32) bool {
	return air <= drowningThreshold
}

// eyeInWater reports whether the player's EYES are submerged in water — the v1 port of
// Entity.isEyeInFluid(FluidTags.WATER) that baseTick's air branch gates on. Vanilla samples the
// fluid at the block containing getEyeY(); we mirror that by reading the single block cell at
// (floor(x), floor(y+eyeHeight), floor(z)). This is intentionally the EYE position, NOT the
// full-AABB playerInWater used by the movement physics (fluid_physics.go) and the fall-damage
// reset — air only drains when the head is under, exactly like vanilla.
// playerSwimmingEyeHeight is the swim-pose eye height: the SWIMMING dimensions are 0.6 tall with
// the eye at 0.4 (Player POSES / EntityDimensions.withEyeHeight(0.4f)). A swimming player's eyes
// are far lower than the standing 1.62, so the submersion check must use this while swimming.
const playerSwimmingEyeHeight = 0.4

// updateSwimming ports Entity.updateSwimming (the v1 subset, no passenger): a player is swimming
// while sprinting AND in water — to START swimming the eyes must be underwater (isUnderWater),
// to KEEP swimming the body just needs to be in water (isInWater). Sets p.swimming. The result
// drives the eye height the breath check samples at.
func (t *TickLoop) updateSwimming(p *tickPlayer) {
	if p.swimming {
		p.swimming = p.sprinting && t.playerInWater(p)
		return
	}
	p.swimming = p.sprinting && t.eyesUnderWaterStanding(p)
}

// eyesUnderWaterStanding samples the standing eye cell for water — the isUnderWater test that gates
// STARTING to swim (before the pose flips, the standing eye height applies).
func (t *TickLoop) eyesUnderWaterStanding(p *tickPlayer) bool {
	if t.only().world == nil {
		return false
	}
	bx := int(math.Floor(p.x))
	by := int(math.Floor(p.y + playerStandingEyeHeight))
	bz := int(math.Floor(p.z))
	return t.fluidAt(pk.Position{X: bx, Y: by, Z: bz}).isWater
}

func (t *TickLoop) eyeInWater(p *tickPlayer) bool {
	if t.only().world == nil {
		return false
	}
	eyeHeight := playerStandingEyeHeight
	if p.swimming {
		eyeHeight = playerSwimmingEyeHeight
	}
	eyeY := p.y + eyeHeight
	bx := int(math.Floor(p.x))
	by := int(math.Floor(eyeY))
	bz := int(math.Floor(p.z))
	cell := pk.Position{X: bx, Y: by, Z: bz}
	fs := t.fluidAt(cell)
	if !fs.isWater {
		return false
	}
	// Vanilla EntityFluidInteraction.update: eyes are inside iff eyeY is within
	// [cellY, cellY + FluidState.getHeight]. getHeight is 1.0 when the cell ABOVE holds the same
	// fluid (hasSameAbove), else getOwnHeight = amount/9 (source -> 8/9 ≈ 0.888). Without this
	// height test the eye reads as submerged across the whole block cell, but a cell whose surface
	// is below the eye (no water above, only an 8/9-tall source) should NOT count — and the inverse
	// matters too. CITE EntityFluidInteraction.update (eyeY >= cellY && eyeY <= cellY+getHeight).
	height := t.fluidSurfaceHeight(cell, fs)
	surface := float64(by) + height
	return eyeY >= float64(by) && eyeY <= surface
}

// fluidSurfaceHeight ports FlowingFluid.getHeight: 1.0 if the cell directly above holds the same
// fluid (hasSameAbove), else getOwnHeight = amount/9 (a full source ≈ 0.888). This is the top of
// the fluid column within the cell, used to decide whether the eye is below the water surface.
func (t *TickLoop) fluidSurfaceHeight(cell pk.Position, fs fluidState) float64 {
	above := t.fluidAt(pk.Position{X: cell.X, Y: cell.Y + 1, Z: cell.Z})
	if above.isWater {
		return 1.0 // hasSameAbove -> full cell height
	}
	amount := fs.amount
	if amount <= 0 {
		amount = 1
	}
	return float64(amount) / 9.0
}

// tickBreath is the per-tick air-supply step: the 1:1 port of the air/drowning branch of
// LivingEntity.baseTick, run inside tickEntities (the same fixed phase as tickFallDamage, no
// phase reorder). For a bare survival player (no Water Breathing effect, no Respiration, not
// creative/invulnerable) the vanilla guards collapse to "decrement while eyes submerged, refill
// otherwise":
//
//	if (isEyeInFluid(WATER) && !inBubbleColumn) {            // canBreatheUnderwater()==false for a player
//	    setAirSupply(decreaseAirSupply(getAirSupply()));
//	    if (shouldTakeDrowningDamage()) { setAirSupply(0); hurtServer(DROWN, 2.0F); }
//	    else if (air < max && shouldEffectsRefillAirsupply()) setAirSupply(increaseAirSupply(air));
//	} else {
//	    if (air < max) setAirSupply(increaseAirSupply(air));
//	}
//
// The BUBBLE_COLUMN guard and the Water-Breathing / Respiration / invulnerable branches are
// faithful no-ops in v1 (no bubble columns, no effects, no flight invulnerability), so they are
// omitted — the collapsed form is byte-for-byte equivalent for the survival player. The else-if
// "refill while submerged" branch (shouldEffectsRefillAirsupply, true only under the Respiration-
// like Water Breathing effect) is likewise never taken for a bare player and is omitted. Drowning
// routes through applyDamage (Sulfur's hurtServer port) so the DROWN hit obeys i-frames exactly as
// vanilla — air resets to 0 and re-drains across the 20-tick window, yielding one 2.0 hit per
// second, the vanilla drowning cadence.
func (t *TickLoop) tickBreath() {
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}

		// Update the swim pose first (Entity.updateSwimming) so eyeInWater samples at the correct
		// eye height (0.4 swimming, 1.62 standing).
		t.updateSwimming(p)

		if t.eyeInWater(p) {
			// setAirSupply(decreaseAirSupply(getAirSupply())): air-1 for a bare player.
			p.airSupply = decreaseAirSupply(p.airSupply)
			if shouldTakeDrowningDamage(p.airSupply) {
				// setAirSupply(0); hurtServer(DROWN, 2.0F).
				p.airSupply = 0
				t.applyDamage(p, drownDamage)
			}
			// (else-if refill-while-submerged branch is Water-Breathing-only -> omitted for v1.)
		} else if p.airSupply < maxAirSupply {
			// setAirSupply(increaseAirSupply(getAirSupply())): min(air+4, 300).
			p.airSupply = increaseAirSupply(p.airSupply)
		}

		// Push the synched DATA_AIR_SUPPLY_ID to the client(s) when it changed. The bubble bar is
		// driven by this field — the client does NOT locally simulate air in multiplayer — so the
		// authoritative server value MUST be sent or the bar never moves (the Plan 17-13 gap this
		// plan closes). Only the DIRTY value is sent (vanilla's SynchedEntityData semantics): a
		// resend every tick would be wire spam, and a no-change tick (full bar out of water) sends
		// nothing.
		t.syncAirSupply(p)
	}
}

// syncAirSupply pushes the player's DATA_AIR_SUPPLY_ID to its observers when it changed since the
// last send. The PRIMARY target is the player's OWN client — vanilla's ServerPlayer is a watcher
// of its own SynchedEntityData, and the local bubble bar reads air off the wire, so the self-send
// is what makes the bar deplete/refill for the player who is drowning. The SAME packet is also
// broadcast through the tracker to every OTHER player tracking this one (broadcastSetEntityData),
// so a second player sees the first's bubbles deplete. A no-change tick sends nothing (dirty-only).
// Tick-owned: airSupply / lastAirSent / playerEntity are all single-owner tick state (TICK-05).
func (t *TickLoop) syncAirSupply(p *tickPlayer) {
	if p.airSupply == p.lastAirSent {
		return // not dirty: nothing to broadcast (vanilla only sends CHANGED synched fields)
	}
	p.lastAirSent = p.airSupply
	if p.playerEntity == nil {
		return // no store Entity yet (pre-join seam): nothing to address the packet to
	}
	pkt := encodeSetEntityData(p.playerEntity, airDataEntry(p.airSupply))
	// SELF send: the local bubble bar reads DATA_AIR_SUPPLY_ID off the wire for the player's own
	// entity. The tracker self-skips (e.id == p.entityID), so the player never receives its own
	// metadata via the tracker — this direct send is the only path for the local bar.
	if p.client != nil {
		p.client.Send(pkt)
	}
	// OBSERVER send: every OTHER player currently tracking this player's entity also needs the
	// update so a remote viewer sees the bubbles deplete. The tracker already maintains who tracks
	// whom; reuse it so this stays in-fence (no new visibility logic here).
	t.broadcastSetEntityDataToTrackers(p, pkt)
}

// broadcastSetEntityDataToTrackers sends an already-built SetEntityData packet for player p's
// entity to every OTHER player whose tracker currently has p's entity id in its tracked set — the
// observers who have an AddEntity'd avatar for p and would otherwise see a stale (full) bubble bar.
// It reuses the tracker's tick-owned `tracked` map (the authoritative who-sees-whom set) so no new
// visibility/broad-phase logic is introduced. Tick-owned (called from tickBreath on the owner).
func (t *TickLoop) broadcastSetEntityDataToTrackers(p *tickPlayer, pkt pk.Packet) {
	for _, other := range t.players {
		if other == nil || other == p || other.client == nil {
			continue
		}
		if other.tracked[p.entityID] {
			other.client.Send(pkt)
		}
	}
}
