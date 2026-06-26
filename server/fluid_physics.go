package server

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// fluid_physics.go (GAMEPLAY-05 Task 3) ports the 26.2 player-in-water physics. In 26.2 the old
// pre-26.2 Entity fluid-push method was REFACTORED into Entity.updateFluidInteraction()
// delegating to the new net.minecraft.world.entity.EntityFluidInteraction class
// (update / applyCurrentTo); the pre-26.2 name no longer exists. The constants are VERIFIED via
// javap on temp/cache/26.2-inner.jar:
//
//	net.minecraft.world.entity.Entity.updateFluidInteraction:        water push scale 0.014d (ldc2_w)
//	net.minecraft.world.entity.LivingEntity.getWaterSlowDown:        0.8f  (freturn)
//	net.minecraft.world.entity.EntityFluidInteraction:               update/applyCurrentTo/isInFluid
//
// VANILLA AUTHORITY MODEL (BUG-1, 17-15): Minecraft player movement is CLIENT-authoritative.
// In net.minecraft.server.network.ServerGamePacketListenerImpl.handleMovePlayer the server
// applies the submitted delta via ServerPlayer.move(MoverType.PLAYER, Vec3) — which is PURE
// COLLISION resolution, NOT travel()/travelInFluid() — and then absSnapTo(x,y,z), accepting the
// client's submitted position verbatim (subject only to the "moved wrongly" anti-clip teleport
// back). LivingEntity.travel/travelInFluid (the 0.8 getWaterSlowDown + 0.014 buoyant push) runs
// CLIENT-side for the local player, so the client already sends its water-slowed position. The
// server therefore MUST NOT re-apply that slowdown to the player — doing so produced a position
// the client never predicted, and the client closed the connection (the water-jump disconnect).
//
// CONSEQUENCE: moveWithFluidPhysics/applyFluidPhysics are NOT called on the player movement path
// (see server/subtick.go — it accepts the collided position directly). They are retained for the
// SERVER-CONTROLLED entity path (mobs swimming in water), where the server IS authoritative and
// DOES run travelInFluid; that wiring lands with the mob-AI tick. playerInWater stays in active
// use by the fall-damage / breath systems.

// Water physics constants (VERIFIED via javap, see file header).
const (
	// waterSlowDown is LivingEntity.getWaterSlowDown() = 0.8f: the per-tick horizontal velocity
	// multiplier applied while in water.
	waterSlowDown = 0.8

	// waterPushScale is the Entity.updateFluidInteraction water push scale = 0.014d: the upward
	// buoyant component applied per tick to an entity in water (the simplified single-step
	// substitute for the full per-fluid-height current accumulation of EntityFluidInteraction).
	waterPushScale = 0.014
)

// playerInWater reports whether the player's AABB intersects any water block. PORT of
// EntityFluidInteraction.isInFluid (v1 subset): sample the block cells spanning the player's
// collision box (feet at p.y, head at p.y+playerHeight, width playerWidth) and return true if
// any is water. A waterlogged block is treated as a read-only source contributor (Assumption
// A3) — detection only; the block is never overwritten.
func (t *TickLoop) playerInWater(p *tickPlayer) bool {
	if t.world == nil {
		return false
	}
	hw := playerWidth / 2
	minX := int(math.Floor(p.x - hw))
	maxX := int(math.Floor(p.x + hw))
	minY := int(math.Floor(p.y))
	maxY := int(math.Floor(p.y + playerHeight))
	minZ := int(math.Floor(p.z - hw))
	maxZ := int(math.Floor(p.z + hw))
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				if t.fluidAt(pk.Position{X: x, Y: y, Z: z}).isWater {
					return true
				}
			}
		}
	}
	return false
}

// applyFluidPhysics adjusts a proposed per-tick movement delta for the player's fluid state.
// PORT of the LivingEntity.travelInFluid + Entity.updateFluidInteraction effect (v1 subset over
// the accepted-delta model — see file header):
//
//   - in water: scale the horizontal delta by getWaterSlowDown (0.8) and add the buoyant push
//     (+waterPushScale) to the vertical delta (reduced sinking / gentle lift).
//   - out of water: return the delta unchanged.
//
// Returns the adjusted (dx, dy, dz). Pure (no mutation) so it is trivially unit-testable and
// can be dropped into the movement-accept path by the integration owner.
func (t *TickLoop) applyFluidPhysics(p *tickPlayer, dx, dy, dz float64) (float64, float64, float64) {
	if !t.playerInWater(p) {
		return dx, dy, dz
	}
	dx *= waterSlowDown
	dz *= waterSlowDown
	dy += waterPushScale // buoyant upward component (reduces net descent)
	return dx, dy, dz
}

// moveWithFluidPhysics applies LivingEntity.travelInFluid's getWaterSlowDown (0.8 horizontal) plus
// Entity.updateFluidInteraction's buoyant push (0.014 vertical) to a proposed movement, returning
// the adjusted target position:
//
//   - target = the proposed (already collided) position.
//   - delta  = target - current, the per-tick movement.
//   - apply applyFluidPhysics(delta): out of water it is the identity; in water it scales
//     horizontal by 0.8 and adds +0.014 buoyancy to vertical.
//   - return current + adjustedDelta.
//
// IT IS NOT CALLED ON THE PLAYER MOVEMENT PATH (BUG-1): the player is client-authoritative and the
// client already applies its own water physics, so the server accepts the submitted position
// verbatim (see the file header and server/subtick.go). This helper is RESERVED for the
// SERVER-CONTROLLED entity path (mobs swimming), where the server runs travelInFluid itself; that
// wiring lands with the mob-AI tick.
func (t *TickLoop) moveWithFluidPhysics(p *tickPlayer, targetX, targetY, targetZ float64) (float64, float64, float64) {
	dx := targetX - p.x
	dy := targetY - p.y
	dz := targetZ - p.z
	dx, dy, dz = t.applyFluidPhysics(p, dx, dy, dz)
	return p.x + dx, p.y + dy, p.z + dz
}
