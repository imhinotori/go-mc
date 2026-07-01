package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/registryid"
)

// particles.go builds the SERVER-side particle broadcast subsystem: the faithful port of
// net.minecraft.server.level.ServerLevel.sendParticles (the general "the server spawns
// particles" path). The wire encoder already exists (encodeLevelParticles, entity_encode.go
// -- the javap-confirmed ClientboundLevelParticlesPacket layout); THIS file is the missing
// server-side dispatch: the particle-type registry-id lookup + the position-based broadcast
// that decides WHICH players get the packet.
//
// SCOPE -- the 1:1 truth about mob particles (javap-verified against all 22 merged mobs this
// session): NONE of them call ServerLevel.sendParticles. Every mob "particle" is one of two
// CLIENT-side seams, neither of which is a ClientboundLevelParticles packet:
//
//   1. isClientSide()-gated addParticle (EnderMan / Endermite PORTAL burst): the CLIENT spawns
//      these locally in its own aiStep; a dedicated server sends NOTHING. Emitting a
//      ClientboundLevelParticles here would DOUBLE the particles on the client -- a deviation.
//      These stay client-only (documented in ai_goals_endermite.go / the .star headers).
//   2. broadcastEntityEvent(this, <status>) -> the client's handleEntityEvent spawns the
//      particles locally (Witch idle WITCH burst = status 15; death poof = 60; spawnAnim = 20;
//      wolf-tame hearts/smoke = 7/6; in-love hearts = 18). These ride the ClientboundEntityEvent
//      seam (encodeEntityEvent), NOT this packet. The Witch idle roll is wired that way here
//      (witchAiStep -> encodeEntityEvent(id, 15)).
//
// So spawnParticle has ZERO mob callers today by design (using it for the mob deferrals above
// would be non-vanilla). It is built because it is the correct, jar-faithful home for the many
// FUTURE server-emitted particle paths that DO go through ServerLevel.sendParticles -- block
// break/place effects, splash potions, dispenser/brewing, /particle, fluid effects, etc. -- so
// those land as a wire call, not a rebuild. It is exercised by TestSpawnParticleBroadcast.
//
// Concurrency: spawnParticle runs ON the tick goroutine over tick-owned state (t.players), the
// same contract as broadcastToTrackers -- no goroutine, no new synchronization.

// particleSendRadius is ServerLevel.sendParticles's per-player visibility cutoff: a player
// receives the packet when its block-pos center is within 32.0 blocks of the particle point
// (overrideLimiter==false). Cite ServerLevel.sendParticles(ServerPlayer,...): `ldc2_w 32.0d`.
const particleSendRadius = 32.0

// particleSendRadiusLongDistance is the overrideLimiter==true cutoff: 512.0 blocks (the
// "long distance" / ignore-the-client-cap broadcast, used by explosions/large effects). Cite
// ServerLevel.sendParticles(ServerPlayer,...): `iload_2 ifeq -> ldc2_w 512.0d else 32.0d`.
const particleSendRadiusLongDistance = 512.0

// spawnParticle is the port of ServerLevel.sendParticles(ParticleOptions, overrideLimiter,
// alwaysShow, x, y, z, count, xDist, yDist, zDist, maxSpeed): build ONE ClientboundLevelParticles
// packet and send it to every player whose block-pos center is within the visibility radius of
// (x, y, z). overrideLimiter==true switches the radius to 512.0 (else 32.0) AND rides through to
// the client as the packet's overrideLimiter bit (ignore the client particle cap).
//
// The per-player gate is Vec3i.closerToCenterThan(Vec3(x,y,z), radius): the squared distance from
// the player's block-pos CENTER (floor(pos)+0.5 on each axis) to the particle point, compared
// < radius^2. This is the exact vanilla test -- measured from the block center, not the raw feet
// position (javap Vec3i.distToCenterSqr: `getX()+0.5 - x`, x3, summed).
//
//	[VERIFIED javap ServerLevel.sendParticles: builds ClientboundLevelParticlesPacket(particle,
//	 overrideLimiter, alwaysShow, x, y, z, xDist, yDist, zDist, maxSpeed, count); for each player
//	 -> private sendParticles: BlockPos(player).closerToCenterThan(Vec3(x,y,z), overrideLimiter?
//	 512.0:32.0) ? connection.send(packet). Vec3i.closerToCenterThan -> distToCenterSqr < square(r).]
func (t *TickLoop) spawnParticle(name string, overrideLimiter, alwaysShow bool, x, y, z float64, xDist, yDist, zDist, maxSpeed float32, count int32) {
	// Registries.PARTICLE_TYPE.getId(type): the VarInt registry index. menuTypeID is the codebase's
	// generic "registry key -> index" lookup (returns -1 not-found); registryid.ParticleType is the
	// generated minecraft:particle_type registry (data/registryid/particletype.go).
	id := menuTypeID(registryid.ParticleType, name)
	if id < 0 {
		return // unknown particle key: nothing to send (defensive -- never on a jar-derived key)
	}
	radius := particleSendRadius
	if overrideLimiter {
		radius = particleSendRadiusLongDistance
	}
	radiusSqr := radius * radius
	pkt := encodeLevelParticles(id, overrideLimiter, alwaysShow, x, y, z, xDist, yDist, zDist, maxSpeed, count)
	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue
		}
		if particleCloserToCenterThan(p.x, p.y, p.z, x, y, z, radiusSqr) {
			p.client.Send(pkt)
		}
	}
}

// particleCloserToCenterThan is Vec3i.closerToCenterThan over the player's BLOCK position: the
// squared distance from the block-pos center (floor(feet)+0.5 per axis) to the particle point,
// compared < radiusSqr. px/py/pz are the player's feet position (blockPosition() == floor(feet)).
//
//	[VERIFIED javap Vec3i.distToCenterSqr(D,D,D): dx = (getX()+0.5) - x; dy = (getY()+0.5) - y;
//	 dz = (getZ()+0.5) - z; return dx*dx + dy*dy + dz*dz. closerToCenterThan: distToCenterSqr <
//	 Mth.square(r). BlockPos(player) == blockPosition() == floor(pos) per axis.]
func particleCloserToCenterThan(px, py, pz, x, y, z, radiusSqr float64) bool {
	dx := (math.Floor(px) + 0.5) - x
	dy := (math.Floor(py) + 0.5) - y
	dz := (math.Floor(pz) + 0.5) - z
	return dx*dx+dy*dy+dz*dz < radiusSqr
}
