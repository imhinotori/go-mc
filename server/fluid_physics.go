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
	if t.world() == nil {
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

// playerInLava reports whether the player's AABB intersects any lava cell — the player port of
// Entity.isInLava (EntityFluidInteraction.isInFluid(FluidTags.LAVA)). Identical AABB cell walk to
// playerInWater, testing the lava flag instead. Players have no firstTick field (never sampled on the
// spawn tick), so the vanilla `!firstTick` guard is not needed here. Cite Entity.isInLava.
func (t *TickLoop) playerInLava(p *tickPlayer) bool {
	if t.world() == nil {
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
				if t.fluidAt(pk.Position{X: x, Y: y, Z: z}).isLava {
					return true
				}
			}
		}
	}
	return false
}

// fluidKind selects which fluid tag a mob fluid scan/height read targets — the Go analogue of the
// FluidTags.WATER / FluidTags.LAVA TagKey passed to Entity.getFluidHeight(TagKey) / isInFluid(TagKey).
type fluidKind int

const (
	fluidWater fluidKind = iota // FluidTags.WATER
	fluidLava                   // FluidTags.LAVA
)

// matchesKind reports whether a decoded fluid cell belongs to the requested fluid tag. The
// FluidTags membership test (FluidState.is(TagKey)) collapsed to the isWater/isLava flags.
func (fs fluidState) matchesKind(kind fluidKind) bool {
	switch kind {
	case fluidWater:
		return fs.isWater
	case fluidLava:
		return fs.isLava
	default:
		return false
	}
}

// mobInWater reports whether the mob's AABB intersects any water cell. PORT of
// EntityFluidInteraction.isInFluid(FluidTags.WATER) (== getFluidHeight(WATER) > 0): scan the block
// cells spanning the entity's collision box (feet at e.y, top at e.y+e.height, half-width
// e.width/2) and return true if any is water. The exact mirror of playerInWater (this file) but on
// *Entity using the entity's data/entity Width/Height instead of the player dims. Cite
// net.minecraft.world.entity.EntityFluidInteraction.isInFluid / Entity.isInWater.
func (t *TickLoop) mobInWater(e *Entity) bool {
	return t.mobInFluid(e, fluidWater)
}

// mobInLava reports whether the mob's AABB intersects any lava cell. PORT of
// EntityFluidInteraction.isInFluid(FluidTags.LAVA) / Entity.isInLava — the same AABB scan as
// mobInWater gated on the lava flag (the full-lava read; never const-false). Cite
// net.minecraft.world.entity.Entity.isInLava.
func (t *TickLoop) mobInLava(e *Entity) bool {
	return t.mobInFluid(e, fluidLava)
}

// mobInFluid is the shared AABB membership scan for mobInWater/mobInLava — EntityFluidInteraction.
// isInFluid over the entity's collision box. It mirrors playerInWater's floor-bounded cell walk
// exactly, substituting e.width/2 for the half-width and e.y .. e.y+e.height for the vertical span.
// Each cell is decoded via fluidAt (which returns the zero fluidState for an unloaded chunk — the
// same guard playerInWater relies on, so an AABB straddling a chunk edge never panics; T-30-01).
func (t *TickLoop) mobInFluid(e *Entity, kind fluidKind) bool {
	if e == nil || t.world() == nil {
		return false
	}
	hw := e.width / 2
	minX := int(math.Floor(e.x - hw))
	maxX := int(math.Floor(e.x + hw))
	minY := int(math.Floor(e.y))
	maxY := int(math.Floor(e.y + e.height))
	minZ := int(math.Floor(e.z - hw))
	maxZ := int(math.Floor(e.z + hw))
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				if t.fluidAt(pk.Position{X: x, Y: y, Z: z}).matchesKind(kind) {
					return true
				}
			}
		}
	}
	return false
}

// fluidSurfaceHeightOf is the kind-parameterized FlowingFluid.getHeight: 1.0 if the cell directly
// above holds the SAME fluid (hasSameAbove), else getOwnHeight = amount/9. It is the lava-aware
// generalization of breath.go's fluidSurfaceHeight (which hardcodes the water above-check) — the
// Task-1 audit point: the same-fluid-above test must match lava-above-lava for the LAVA path, never
// water-above-lava. breath.go's water-only helper is left untouched so no water consumer is
// perturbed. Cite net.minecraft.world.level.material.FlowingFluid.getHeight / getOwnHeight.
func (t *TickLoop) fluidSurfaceHeightOf(cell pk.Position, fs fluidState, kind fluidKind) float64 {
	above := t.fluidAt(pk.Position{X: cell.X, Y: cell.Y + 1, Z: cell.Z})
	if above.matchesKind(kind) {
		return 1.0 // hasSameAbove -> full cell height
	}
	amount := fs.amount
	if amount <= 0 {
		amount = 1
	}
	return float64(amount) / 9.0
}

// mobFluidHeight ports Entity.getFluidHeight(TagKey) for the requested fluid tag: the MAX over the
// mob's AABB cells (for cells whose decoded fluid matches kind) of the cell's fluid surface height
// above its own cell base. EntityFluidInteraction.update walks the entity's fluid box and, per
// matching cell, takes `(cellY + getHeight(cell)) - aabb.minY` clamped non-negative, keeping the
// max into the per-fluid Tracker.height — which getFluidHeight then returns (dconst_0 when no
// matching cell, javap EntityFluidInteraction.getFluidHeight). We mirror the surface math from
// eyeInWater (breath.go: surface = cellY + fluidSurfaceHeight), reusing fluidSurfaceHeight per cell
// but PARAMETERIZED on kind so the same-fluid-above check matches lava-above-lava (the Task-1 audit
// point — the water path is unchanged). Returns 0.0 when no matching fluid cell is found.
// Cite net.minecraft.world.entity.Entity.getFluidHeight / EntityFluidInteraction.update.
func (t *TickLoop) mobFluidHeight(e *Entity, kind fluidKind) float64 {
	if e == nil || t.world() == nil {
		return 0
	}
	hw := e.width / 2
	minX := int(math.Floor(e.x - hw))
	maxX := int(math.Floor(e.x + hw))
	minY := int(math.Floor(e.y))
	maxY := int(math.Floor(e.y + e.height))
	minZ := int(math.Floor(e.z - hw))
	maxZ := int(math.Floor(e.z + hw))
	maxHeight := 0.0
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				cell := pk.Position{X: x, Y: y, Z: z}
				fs := t.fluidAt(cell)
				if !fs.matchesKind(kind) {
					continue
				}
				// surface = cellY + getHeight; the fluid column top within (and above) this cell,
				// measured from the AABB base (e.y). EntityFluidInteraction.update keeps the max.
				surface := float64(y) + t.fluidSurfaceHeightOf(cell, fs, kind)
				h := surface - e.y
				if h > maxHeight {
					maxHeight = h
				}
			}
		}
	}
	return maxHeight
}

// getFluidJumpThreshold ports Entity.getFluidJumpThreshold():
//
//	getEyeHeight() < 0.4 ? 0.0 : 0.4    (javap Entity.getFluidJumpThreshold:
//	    getEyeHeight; f2d; ldc2_w 0.4; dcmpg; iflt -> dconst_0; else ldc2_w 0.4)
//
// There is NO Mob override — Mob inherits Entity's. Eye height is the not-yet-built read; until an
// EntityDimensions/eye-height subsystem lands it is computed from the vanilla DEFAULT factor
// (EntityDimensions ctor: eyeHeight = height * 0.85f, javap-verified — `ldc float 0.85f`), so the
// value is structured to become a real per-type eye-height read later (CLAUDE.md: cite the default,
// never bake it away). The pig (height 0.9 -> eye 0.765 >= 0.4) thus reads 0.4, NOT 0.0; a short
// entity (eye < 0.4) reads 0.0. Cite net.minecraft.world.entity.Entity.getFluidJumpThreshold.
func (t *TickLoop) getFluidJumpThreshold(e *Entity) float64 {
	if mobEyeHeight(e) < fluidJumpThresholdEyeCutoff {
		return 0.0
	}
	return fluidJumpThresholdEyeCutoff
}

const (
	// fluidJumpThresholdEyeCutoff is BOTH the eye-height cutoff and the returned threshold in
	// Entity.getFluidJumpThreshold (getEyeHeight() < 0.4 ? 0.0 : 0.4 — the same 0.4 literal,
	// javap-verified `ldc2_w 0.4`).
	fluidJumpThresholdEyeCutoff = 0.4

	// defaultEyeHeightScale is the EntityDimensions default eye-height factor (height * 0.85f —
	// javap EntityDimensions <init>: `ldc float 0.85f`). Used until a per-type eye-height read
	// exists; the pig/most mobs have no eye-height override so this is their real eye height.
	defaultEyeHeightScale = 0.85
)

// mobEyeHeight is the entity's eye height. Vanilla reads getEyeHeight() off the per-pose
// EntityDimensions; with no eye-height subsystem yet it is the DEFAULT height*0.85 (the value the
// pig and most mobs actually carry — they declare no withEyeHeight override). Structured to become
// a per-type read later (CLAUDE.md). Cite net.minecraft.world.entity.EntityDimensions (eyeHeight
// default = height * 0.85f).
func mobEyeHeight(e *Entity) float64 {
	if e == nil {
		return 0
	}
	return e.height * defaultEyeHeightScale
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
