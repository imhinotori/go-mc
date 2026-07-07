package server

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"

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

// Fluid current-push constants - VERIFIED via javap on temp/cache/26.2-inner.jar this session:
//
//	net.minecraft.world.entity.Entity.updateFluidInteraction:
//	  WATER applyCurrentTo motionScale = 0.014d                    (ldc2_w #1815)
//	  LAVA  applyCurrentTo motionScale = FAST_LAVA(nether) 0.007d  (ldc2_w #1828)
//	                                     else (overworld) 0.0023333333333333335d (ldc2_w #1830)
//	net.minecraft.world.entity.EntityFluidInteraction$Tracker.applyCurrentTo:
//	  accumulatedCurrent.lengthSqr() < 9.999999747378752E-6 -> return (the tiny-current skip)
//	  non-Player: current = accumulatedCurrent.normalize(); scale(motionScale)
//	  min-current boost: |dm.x|<0.003 && |dm.z|<0.003 && current.length()<0.0045
//	                     -> current = current.normalize().scale(0.0045)
//	net.minecraft.world.entity.EntityFluidInteraction.update:
//	  per matching fluid cell: getFlow; if tracker.height < 0.4 flow = flow.scale(height);
//	  accumulateCurrent(flow)   (the shallow-water attenuation)
const (
	// waterCurrentScale is Entity.updateFluidInteraction's WATER applyCurrentTo motionScale = 0.014d.
	waterCurrentScale = 0.014

	// lavaCurrentScaleOverworld is the OVERWORLD (non-fast) LAVA applyCurrentTo motionScale =
	// 0.0023333333333333335d. Sulfur v1 targets the overworld; the nether FAST_LAVA 0.007 is a
	// per-dimension EnvironmentAttribute read deferred with the rest of the fast/slow-lava split
	// (mirrors fluid.go's overworld-only lava flow constants). Cite Entity.updateFluidInteraction.
	lavaCurrentScaleOverworld = 0.0023333333333333335

	// currentTinySkipSqr is Tracker.applyCurrentTo's accumulatedCurrent.lengthSqr() skip threshold
	// (9.999999747378752E-6 - the float 1e-5 widened to double). Below it the accumulated current is
	// negligible and no push is applied.
	currentTinySkipSqr = 9.999999747378752e-6

	// currentBoostAxisBand / currentBoostFloor are the min-current boost literals: when the entity's
	// horizontal velocity is nearly still (|dm.x|<0.003 && |dm.z|<0.003) and the scaled current is
	// weaker than 0.0045, the current is re-normalized to exactly 0.0045 so a stationary entity is
	// still nudged downstream. Cite Tracker.applyCurrentTo (0.003d ; 0.0045000000000000005d).
	currentBoostAxisBand = 0.003
	currentBoostFloor    = 0.0045000000000000005

	// currentShallowHeightCutoff is EntityFluidInteraction.update's per-cell shallow-water flow
	// attenuation cutoff: while the accumulated fluid height is < 0.4, each cell's getFlow vector is
	// scaled by that height before accumulation (shallow water pushes less). Cite update (ldc2_w 0.4d).
	currentShallowHeightCutoff = 0.4
)

// updateFluidCurrent ports net.minecraft.world.entity.EntityFluidInteraction.update (the flow
// accumulation over the entity AABB) + Tracker.applyCurrentTo (the normalize + motionScale + min-
// current boost) for a mob *Entity, applied to e.vx/vy/vz. It scans the entity collision-box cells
// for the given fluid kind, sums each cell getFlow (attenuated by the running fluid height while
// that height is < 0.4), normalizes the accumulated vector (the non-Player branch), scales by
// motionScale (WATER 0.014 / LAVA overworld 0.0023333...), applies the min-current boost, and adds
// the result to the mob velocity. This is Entity.baseTick fluid-push, run BEFORE the travel/move
// step (so the current is in deltaMovement when moveEntity integrates it), exactly as vanilla orders
// updateFluidInteraction (baseTick) before aiStep->travel.
//
// ORACLE GATE: a DRY mob has zero matching cells -> accumulatedCurrent stays ZERO -> the count == 0
// / lengthSqr()<1e-5 skip returns immediately with NO velocity change and NO getFlow call, so a pig
// on dry land is byte-identical (verified by TestFluidPushDryEntityNoChange). RNG-free.
//
//	Cite: net.minecraft.world.entity.EntityFluidInteraction.update / Tracker.accumulateCurrent /
//	Tracker.applyCurrentTo; Entity.updateFluidInteraction (motionScale constants).
func (t *TickLoop) updateFluidCurrent(e *Entity, kind fluidKind, motionScale float64) {
	if e == nil || t.world() == nil {
		return
	}
	hw := e.width / 2
	minX := int(math.Floor(e.x - hw))
	maxX := int(math.Floor(e.x + hw))
	minY := int(math.Floor(e.y))
	maxY := int(math.Floor(e.y + e.height))
	minZ := int(math.Floor(e.z - hw))
	maxZ := int(math.Floor(e.z + hw))

	var accumulated vec3d
	count := 0
	height := 0.0 // Tracker.height: running max fluid height over matching cells (== mobFluidHeight)
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				cell := pk.Position{X: x, Y: y, Z: z}
				fs := t.fluidAt(cell)
				if !fs.matchesKind(kind) {
					continue
				}
				surface := float64(y) + t.fluidSurfaceHeightOf(cell, fs, kind)
				if h := surface - e.y; h > height {
					height = h
				}
				flow := t.getFlow(cell, fs)
				if height < currentShallowHeightCutoff {
					flow = flow.scale(height)
				}
				accumulated = accumulated.add(flow.x, flow.y, flow.z)
				count++
			}
		}
	}

	if count == 0 || accumulated.lengthSqr() < currentTinySkipSqr {
		return
	}
	current := accumulated.normalize()
	current = current.scale(motionScale)
	if math.Abs(e.vx) < currentBoostAxisBand && math.Abs(e.vz) < currentBoostAxisBand &&
		current.length() < currentBoostFloor {
		current = current.normalize().scale(currentBoostFloor)
	}
	e.vx += current.x
	e.vy += current.y
	e.vz += current.z
}

// mobIsFree ports net.minecraft.world.entity.Entity.isFree(double,double,double): the target box
// (the entity AABB moved by dx,dy,dz) is FREE iff it collides with no solid AND contains no liquid.
// noCollision -> collectBlockCollisions is empty; containsAnyLiquid -> the block-cell scan of the
// moved box finds no fluid. So jumpOutOfFluid only fires into OPEN AIR above the wall, never into
// more fluid. Cite Entity.isFree (noCollision(box.move) && !containsAnyLiquid(box.move)).
func (t *TickLoop) mobIsFree(e *Entity, dx, dy, dz float64) bool {
	box := entityBoxOf(e)
	box.MinX += dx
	box.MaxX += dx
	box.MinY += dy
	box.MaxY += dy
	box.MinZ += dz
	box.MaxZ += dz
	if len(t.collectBlockCollisions(box)) != 0 {
		return false
	}
	return !t.boxContainsLiquid(box)
}

// boxContainsLiquid ports net.minecraft.world.level.Level.containsAnyLiquid(AABB): scan every block
// cell the box spans and report true if any holds a fluid (water OR lava). Used by mobIsFree so the
// water-jump only fires when the space above-forward is truly open air (not more fluid). Cite
// net.minecraft.world.level.Level.containsAnyLiquid (getFluidState(pos).isEmpty() over the box).
func (t *TickLoop) boxContainsLiquid(box block.Box) bool {
	minX := int(math.Floor(box.MinX))
	maxX := int(math.Ceil(box.MaxX)) - 1
	minY := int(math.Floor(box.MinY))
	maxY := int(math.Ceil(box.MaxY)) - 1
	minZ := int(math.Floor(box.MinZ))
	maxZ := int(math.Ceil(box.MaxZ)) - 1
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				if t.fluidAt(pk.Position{X: x, Y: y, Z: z}).isFluid() {
					return true
				}
			}
		}
	}
	return false
}

// jumpOutOfFluid ports net.minecraft.world.entity.LivingEntity.jumpOutOfFluid(double d) 1:1: a mob
// swimming against a wall in fluid hops out at the edge. Called at the END of travelInWater/
// travelInLava (AFTER the move, so horizontalCollision and getY are the settled values), with d ==
// the y captured at the START of travelInFluid (oldY). Verified bytecode:
//
//	Vec3 dm = getDeltaMovement();
//	if horizontalCollision and isFree(dm.x, dm.y + 0.6 - getY() + d, dm.z):
//	    setDeltaMovement(dm.x, 0.3, dm.z)
//
// The y offset (dm.y + 0.6 - getY() + oldY) probes the box moved up to just above the wall lip: 0.6
// is the step-lip, and (oldY - getY()) corrects for the vertical displacement the move just applied.
// When that box is FREE (open air, no solid, no liquid - mobIsFree), the mob is launched up at 0.3.
// GATE: only runs when horizontalCollision is set, i.e. the mob is pressed against a wall - a mob
// swimming in open water never hops. RNG-free. Cite LivingEntity.jumpOutOfFluid.
func (t *TickLoop) jumpOutOfFluid(e *Entity, oldY float64) {
	if !e.horizontalCollision {
		return
	}
	dy := e.vy + 0.6000000238418579 - e.y + oldY
	if t.mobIsFree(e, e.vx, dy, e.vz) {
		e.vy = 0.30000001192092896
	}
}

const (
	// lavaHorizontalDrag is the LAVA horizontal (and DEEP-lava all-axis) velocity multiplier: the
	// literal 0.5d that travelInLava applies via deltaMovement.multiply(0.5d, _, 0.5d) in the shallow
	// branch and deltaMovement.scale(0.5d) in the deep branch. Lava is far thicker than water, so its
	// drag (0.5) is far more aggressive than water's 0.8 (getWaterSlowDown) -- a mob in lava barely
	// glides. This REPLACES the dry horizontalFriction for an in-lava mob.
	//	[VERIFIED javap LivingEntity.travelInLava: ldc2_w #424 // double 0.5d on X and Z (shallow
	//	 multiply) and the single ldc2_w #424 for the deep-lava scale.]
	lavaHorizontalDrag = 0.5

	// lavaGravityDivisor is travelInLava's OUTER gravity divisor: after the drag, the lava-specific
	// gravity pull is deltaMovement.add(0.0, -baseGravity/4.0, 0.0) -- baseGravity/4 == 0.08/4 == 0.02.
	// This is DISTINCT from (and applied ON TOP OF, in the shallow case, alongside) the
	// getFluidFallingAdjustedMovement baseGravity/16 pull water uses; lava has this extra outer sink.
	//	[VERIFIED javap LivingEntity.travelInLava: ldc2_w #3046 // double 4.0d ; dneg then ddiv on the
	//	 gravity add -- d = -baseGravity/4.0, add(0, d, 0), gated on `baseGravity != 0.0`.]
	lavaGravityDivisor = 4.0
)

// travelInLavaVertical is the port of the velocity ops of LivingEntity.travelInLava (its drag +
// getFluidFallingAdjustedMovement + the outer -baseGravity/4 gravity add) for a mob *Entity, called
// from tickPhysics IN PLACE OF the dry `vy -= 0.08; vy *= 0.98` (and the dry horizontal friction)
// whenever mobInLava(e) is true. It is the LAVA sibling of travelInWaterVertical (fluid_travel.go):
// the move(SELF, dm) and the trailing jumpOutOfFluid(oldY) that vanilla runs INSIDE travelInLava are
// factored out to the shared moveEntity + jumpOutOfFluid at the tail of the tickPhysics loop (the
// same architecture the water branch uses), so this helper is only the deltaMovement mutation. It
// mutates e.vx/vy/vz. Verified bytecode of net.minecraft.world.entity.LivingEntity.travelInLava:
//
//	moveRelative(0.02f, input); move(SELF, dm);            // horizontal swim input + the move (factored out)
//	if (isInShallowFluid(FluidTags.LAVA)) {
//	    dm = dm.multiply(0.5d, 0.800000011920929d, 0.5d);  // shallow: horiz drag 0.5, vert drag 0.8
//	    dm = getFluidFallingAdjustedMovement(baseGravity, isFalling, dm);  // reduced /16 gravity + neutral guard
//	} else {
//	    dm = dm.scale(0.5d);                               // deep: uniform 0.5 on ALL three axes
//	}
//	if (baseGravity != 0.0) dm = dm.add(0.0, -baseGravity/4.0, 0.0);  // the outer lava gravity (-0.02)
//	jumpOutOfFluid(oldY);                                  // (factored out to the shared tail)
//
// The shallow/deep split turns on isInShallowFluid(LAVA) == getFluidHeight(LAVA) <=
// getFluidJumpThreshold() (jump.go). SHALLOW lava applies the (0.5, 0.8, 0.5) drag then
// getFluidFallingAdjustedMovement (the reduced baseGravity/16 == 0.005 pull, with the neutral-point
// guard), then the outer -baseGravity/4 == -0.02 add -- BOTH gravity terms. DEEP lava applies a
// uniform 0.5 scale (vertical drag 0.5, NOT 0.8, and NO getFluidFallingAdjustedMovement), then only
// the outer -0.02 add. isFalling is `getDeltaMovement().y <= 0.0` from travelInFluid, read on the
// PRE-drag velocity exactly as vanilla. baseGravity == getEffectiveGravity() == gravityPerTick (0.08)
// for a default v1 mob (no slow-falling). PURE (no RNG draw): a DRY mob never enters this branch (the
// mobInLava gate in tickPhysics), so the pig oracle stream is byte-identical -- the dry oracle world
// has no lava, so travelInLavaVertical never runs there at all.
//
//	Cite: net.minecraft.world.entity.LivingEntity.travelInLava / getFluidFallingAdjustedMovement /
//	isInShallowFluid / getEffectiveGravity (0.08); travelInFluid (isFalling = deltaMovement.y <= 0).
func (t *TickLoop) travelInLavaVertical(e *Entity) {
	// isFalling = getDeltaMovement().y <= 0.0 -- read on the CURRENT (pre-drag) velocity, exactly as
	// travelInFluid computes `var 2` before dispatching to travelInLava. The jumpInLiquid +0.04 impulse
	// (applied in tickAI, before tickPhysics) has already landed in e.vy, so a freshly-impulsed mob
	// reads isFalling=false (vy > 0). Only consumed by getFluidFallingAdjustedMovement in the shallow branch.
	isFalling := e.vy <= 0.0

	if t.isInShallowFluid(e, fluidLava) {
		// SHALLOW lava: dm.multiply(0.5d, 0.800000011920929d, 0.5d) -- horizontal drag 0.5, vertical
		// drag 0.8 (the SAME 0.800000011920929d literal water uses on Y). Then the
		// getFluidFallingAdjustedMovement reduced-gravity adjustment (baseGravity/16 == 0.005).
		e.vx *= lavaHorizontalDrag
		e.vy *= waterVerticalDrag
		e.vz *= lavaHorizontalDrag
		e.vy = fluidFallingAdjustedY(gravityPerTick, isFalling, e.vy)
	} else {
		// DEEP lava: dm.scale(0.5d) -- a uniform 0.5 multiplier on ALL three axes (vertical drag 0.5,
		// NOT 0.8, and NO getFluidFallingAdjustedMovement). Lava's deep-body physics is a plain half-scale.
		e.vx *= lavaHorizontalDrag
		e.vy *= lavaHorizontalDrag
		e.vz *= lavaHorizontalDrag
	}

	// The OUTER lava gravity add: `if (baseGravity != 0.0) dm = dm.add(0.0, -baseGravity/4.0, 0.0)`.
	// baseGravity (0.08) != 0 for a v1 mob, so it always fires: vy -= 0.08/4 == 0.02. This runs in
	// BOTH the shallow and deep branches (it is after the if/else in the bytecode), so a shallow-lava
	// mob takes BOTH the reduced /16 pull AND this /4 pull, a deep-lava mob takes only this /4 pull.
	e.vy += -gravityPerTick / lavaGravityDivisor
}
