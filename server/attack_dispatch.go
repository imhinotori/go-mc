package server

import (
	pk "github.com/imhinotori/sulfur/net/packet"
)

// attack_dispatch.go — GAMEPLAY-04 (the PvP attack half). applyInput (subtick.go) routes a
// drained ServerboundAttack here; this resolves the NAMED target to a tickPlayer, reach-gates
// it server-side, and applies a SERVER-supplied damage amount into the existing, tested
// applyDamage->die flow (combat.go). The packet only NAMES a target — the client never claims
// a damage amount (T-6-05 / V4); the server is authoritative.
//
// JAR-VERIFIED WIRE LAYOUT (javap -c -p net.minecraft.network.protocol.game.ServerboundAttackPacket
// from temp/cache/26.2-inner.jar this session):
//
//	ServerboundAttackPacket = record(int entityId)
//	STREAM_CODEC = StreamCodec.composite(ByteBufCodecs.VAR_INT, …)  // a SINGLE VarInt entityId
//
// This is a 26.2 SHIFT the ≤1.21 docs miss: the old ServerboundInteractPacket carried an
// {INTERACT, ATTACK, INTERACT_AT} Action enum and ATTACK lived inside it. In 26.2 ATTACK was
// SPLIT into its own ServerboundAttackPacket whose entire body is the target entity id, while
// ServerboundInteractPacket is now ONLY the right-click interact
// (entityId + InteractionHand + Vec3 location + Boolean usingSecondaryAction — NO Action enum,
// jar-verified). So the entity ATTACK is carried by ServerboundAttack, and ServerboundInteract
// is NOT an attack — handleInteract treats it as a non-damaging interaction no-op for v1
// (right-click-on-entity actions are a later concern).

// baseAttackDamage is the bare-hand (no weapon) base attack damage a player deals, in HP. It
// is SERVER-supplied — the attack packet only names a target, never an amount (T-6-05 / V4).
// Source: the jar player attributes — Attributes.ATTACK_DAMAGE base for a Player is 1.0
// (net.minecraft.world.entity.player.Player registers a default GENERIC_ATTACK_DAMAGE of 1.0;
// hand-to-hand vanilla damage is 1 HP = half a heart before weapon/enchant/strength modifiers,
// which v1 does not yet apply). Held-item/critical/cooldown scaling is a later concern.
const baseAttackDamage float32 = 1.0

// attackReach is the server-authoritative max distance (blocks, player-center to player-center)
// at which a melee attack can land. Vanilla survival entity-interaction reach is ~3.0 blocks
// (Attributes.ENTITY_INTERACTION_RANGE base = 3.0 in the jar); a small slack absorbs the
// position jitter between the client's hit-frame and the server's tick-sampled position so a
// legitimate just-in-range hit is not falsely rejected. A target beyond this is a silent no-op
// (the reach gate, mirroring withinReach for blocks — T-6-01). Kept distinct from blockReach
// (6.0) because entity reach is shorter than block reach in vanilla.
const attackReach = 3.0 + 0.5

// handleAttack resolves a ServerboundAttack on-tick. It decodes defensively (a Scan error is a
// silent no-op — never a panic, T-6-04 / V5), resolves the named target entity id to its
// owning tickPlayer (the 17-01 reverse lookup), rejects a nil/self/out-of-reach target
// silently, and applies the SERVER-supplied baseAttackDamage through applyDamage (which lowers
// health, sends the authoritative SetHealth, and drives die() if lethal). Runs on the tick
// goroutine over tick-owned state (TICK-05).
func (t *TickLoop) handleAttack(p *tickPlayer, pkt pk.Packet) {
	var targetID pk.VarInt
	if err := pkt.Scan(&targetID); err != nil {
		return // malformed/short payload: no-op, never panic (defensive decode)
	}

	victim := t.lookupPlayerByEntityID(int32(targetID))
	if victim == nil || victim == p {
		return // forged/unknown target, or a self-attack: silent no-op
	}

	// Server-authoritative reach gate (T-6-01): reject an out-of-range target silently. The
	// client cannot melee across the map even if it names a valid entity id.
	if !t.withinAttackReach(p, victim) {
		return
	}

	// The packet only NAMED the target; the SERVER supplies the amount (T-6-05 / V4). Route
	// through the existing applyDamage->die flow (combat.go) — death + respawn already work.
	t.applyDamage(victim, baseAttackDamage)
}

// withinAttackReach reports whether the victim is close enough to the attacker for a melee
// hit. Euclidean distance between the two players' positions, compared against attackReach.
// Squared-distance comparison avoids a sqrt. The server-authoritative entity reach gate
// (T-6-01) — mirrors withinReach (block edits) but with the shorter entity-interaction reach.
func (t *TickLoop) withinAttackReach(attacker, victim *tickPlayer) bool {
	dx := victim.x - attacker.x
	dy := victim.y - attacker.y
	dz := victim.z - attacker.z
	return dx*dx+dy*dy+dz*dz <= attackReach*attackReach
}

// handleInteract resolves a ServerboundInteract on-tick. In 26.2 this packet is the RIGHT-CLICK
// entity interaction (entityId + InteractionHand + Vec3 location + Boolean usingSecondaryAction
// — jar-verified, NO Action enum; ATTACK is a separate ServerboundAttack handled by
// handleAttack). v1 has no entity right-click behavior (mounting, trading, leashing, etc.), so
// this is a defensive-decode no-op: it never deals damage (only ServerboundAttack does). Kept
// as an explicit handler so the wire is consumed deliberately rather than via the default drop,
// and so a future plan can attach interaction behavior here. Decoding is best-effort; a
// malformed payload is simply ignored.
func (t *TickLoop) handleInteract(p *tickPlayer, pkt pk.Packet) {
	// No-op for v1. Intentionally does NOT damage: per the jar split, ServerboundInteract is
	// the right-click interaction, never the attack (T-6-05 — only the server-driven attack
	// path deals damage). Left as a named seam for future entity-interaction behavior.
	_ = p
	_ = pkt
}
