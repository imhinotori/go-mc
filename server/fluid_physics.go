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
// INTEGRATION NOTE (the one MEDIUM-confidence sub-area, 17-RESEARCH Pattern 5 (F)): Sulfur's
// player movement is currently position-authoritative (subtick.go applyInput accepts the
// client's claimed position through collidePlayer; there is no server-side velocity integrator
// yet). So applyFluidPhysics operates on the ACCEPTED MOVEMENT DELTA: it scales the horizontal
// delta by getWaterSlowDown and adds the buoyant push to the vertical delta. The call site (one
// line in subtick.go's collidePlayer/accept path) is owned by 17-03 in this wave (subtick.go is
// 17-03's declared file), so the wiring is DEFERRED — this file ships the fully-unit-tested
// physics function ready to be called. See 17-02-SUMMARY for the GAMEPLAY-07 feel note.

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
