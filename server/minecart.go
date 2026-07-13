package server

// minecart.go — MINECART + RAILS: a 1:1 port of the DEFAULT vanilla minecart movement
// (net.minecraft.world.entity.vehicle.minecart.AbstractMinecart + OldMinecartBehavior) over the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session). A minecart is a NON-mob
// rideable Entity (isMinecart) whose whole behavior is the rail-follow physics — the sibling of the
// arrow (projectile.go) and the dropped item (item_entity.go): no AI, just a per-tick physics pass.
//
// WHICH BEHAVIOR: 26.2 splits minecart movement into two MinecartBehavior classes chosen by the
// MINECART_IMPROVEMENTS feature flag (AbstractMinecart.useExperimentalMovement ==
// enabledFeatures().contains(FeatureFlags.MINECART_IMPROVEMENTS)). That flag is EXPERIMENTAL and OFF by
// default, so the DEFAULT vanilla minecart uses OldMinecartBehavior — the classic physics ported here.
// NewMinecartBehavior (the experimental sub-block-step movement) is a CITED DEFERRAL below.
//	[VERIFIED CFR AbstractMinecart.<init>: behavior = useExperimentalMovement(level) ?
//	 new NewMinecartBehavior(this) : new OldMinecartBehavior(this).]
//
// PORTED SURFACES (all CFR/javap-cited at the call site):
//   - AbstractMinecart.tick → OldMinecartBehavior.tick: applyGravity, read the rail below, moveAlongTrack
//     (on a rail) OR comeOffTrack (off a rail), derive the yaw + the flipped latch.
//   - OldMinecartBehavior.moveAlongTrack: the load-bearing rail physics — the ascending slide accel, the
//     EXITS-table velocity projection, the position snap onto the rail, the powered-rail boost/brake, the
//     halt-track brake, the ascend Y delta, applyNaturalSlowdown, the block-cross velocity redirect.
//   - AbstractMinecart.exits / EXITS: the per-RailShape (exit0, exit1) unit-vector pair.
//   - AbstractMinecart.getMaxSpeed (0.4 land / 0.2 water) / applyNaturalSlowdown (0.997 ridden / 0.96) /
//     getDefaultGravity (0.04 land / 0.005 water) / comeOffTrack / isRedstoneConductor.
//   - AbstractMinecart.getCurrentBlockPosOrRailBelow (the rail-one-below snap).
//
// v1 DEFERRALS (each cited):
//   - NewMinecartBehavior (the experimental MINECART_IMPROVEMENTS movement): DEFERRED — off by default,
//     so the observable default-server behavior is OldMinecartBehavior (this file). CITE useExperimentalMovement.
//   - pushAndPickupEntities (minecart<->entity push, mob auto-ride): the single-cart RAIL physics is the
//     target; the entity-push broad phase is a CITED DEFERRAL (OldMinecartBehavior.pushAndPickupEntities).
//   - applyEffectsFromBlocks (soul-sand slow, honey, bubble columns): CITED DEFERRAL — a not-yet-built
//     block-effect subsystem; the rail-follow numeric ops are unaffected.
//   - the rider WASD momentum (getLastClientMoveIntent 0.001 push): the input-intent packet path is not
//     wired; a RIDDEN cart still rolls with momentum (0.75 scale + 0.997 ridden slowdown). CITE below.
//   - activateMinecart body (TNT prime / hopper enable / spawner) is a no-op on a plain/chest cart exactly
//     as AbstractMinecart.activateMinecart is empty (the subclasses override it) — CITE.
//   - FurnaceMinecart fuel-drive, CommandBlock/Spawner/TNT minecarts: CITED DEFERRALS (plain + chest +
//     hopper minecart are the target).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// --- CONSTANTS (all CFR-verified exact float values) ---------------------------------------------

const (
	// minecartMaxSpeedLand is OldMinecartBehavior.getMaxSpeed on land (MAX_SPEED_ON_LAND / ABSOLUTE_MAX_SPEED
	// == 0.4). The per-axis clamp in moveAlongTrack + comeOffTrack + getKnownMovement uses this.
	//	[VERIFIED CFR OldMinecartBehavior: getMaxSpeed { return isInWater() ? 0.2 : 0.4; }.]
	minecartMaxSpeedLand = 0.4
	// minecartMaxSpeedWater is OldMinecartBehavior.getMaxSpeed in water (MAX_SPEED_IN_WATER == 0.2).
	minecartMaxSpeedWater = 0.2

	// minecartGravityLand is AbstractMinecart.getDefaultGravity on land (0.04); in water 0.005.
	//	[VERIFIED CFR AbstractMinecart.getDefaultGravity: return isInWater() ? 0.005 : 0.04.]
	minecartGravityLand  = 0.04
	minecartGravityWater = 0.005

	// minecartSlowdownRidden is OldMinecartBehavior.getSlowdownFactor when carrying a passenger (0.997);
	// empty carts slow faster (0.96). applyNaturalSlowdown multiplies horizontal velocity by this.
	//	[VERIFIED CFR OldMinecartBehavior.getSlowdownFactor: return isVehicle() ? 0.997 : 0.96.]
	minecartSlowdownRidden = 0.997
	minecartSlowdownEmpty  = 0.96

	// minecartAirDrag is AbstractMinecart.getAirDrag (0.95) — applied off-rail while airborne (comeOffTrack).
	//	[VERIFIED CFR AbstractMinecart.getAirDrag: return 0.95F.]
	minecartAirDrag = 0.95

	// minecartSlideSpeed is OldMinecartBehavior.moveAlongTrack's ascending-slope acceleration (0.0078125),
	// scaled by 0.2 in water. Added to velocity along the descending axis of an ascending rail.
	//	[VERIFIED CFR OldMinecartBehavior.moveAlongTrack: double slideSpeed = 0.0078125; if (isInWater())
	//	 slideSpeed *= 0.2.]
	minecartSlideSpeed = 0.0078125

	// minecartPoweredBoost is the powered-rail push (0.06) added along the velocity direction when the
	// cart is already moving on a POWERED powered_rail.
	//	[VERIFIED CFR OldMinecartBehavior.moveAlongTrack: double speed = 0.06; setDeltaMovement(add(x/len*0.06,
	//	 0, z/len*0.06)).]
	minecartPoweredBoost = 0.06

	// minecartPoweredKick is the powered-rail launch (0.02) applied to a STOPPED cart on a POWERED
	// powered_rail when a redstone-conductor block sits on one side (the cart is pushed away from it).
	//	[VERIFIED CFR OldMinecartBehavior.moveAlongTrack: dx/dz = ±0.02 based on isRedstoneConductor(west/
	//	 east/north/south).]
	minecartPoweredKick = 0.02

	// minecartRideScale is the 0.75 velocity scale applied to a RIDDEN cart's move (an occupied cart moves
	// at 3/4 speed); an empty cart uses 1.0.
	//	[VERIFIED CFR OldMinecartBehavior.moveAlongTrack: double scale = isVehicle() ? 0.75 : 1.0.]
	minecartRideScale = 0.75
)

// minecartWaterSlowdown is AbstractMinecart.applyNaturalSlowdown's extra in-water scale (WATER_SLOWDOWN_
// FACTOR == 0.95F): newMovement.scale(0.95) when isInWater(). v1 minecarts are not in water in the test/
// common path, but the branch is ported for fidelity.
//
//	[VERIFIED CFR AbstractMinecart.applyNaturalSlowdown: if (isInWater()) newMovement = newMovement.scale(0.95F).]
const minecartWaterSlowdown = 0.95

// railExit is a Vec3i (exit0/exit1) from the EXITS table — the unit direction of one of the two ends a
// rail shape connects (with a -1 Y for the low end of an ascending rail).
type railExit struct{ x, y, z int }

// minecartExits ports AbstractMinecart.EXITS: the (exit0, exit1) unit-vector pair for each RailShape.
// VERBATIM from the EXITS static init (Direction unit vectors: WEST=(-1,0,0), EAST=(1,0,0), NORTH=
// (0,0,-1), SOUTH=(0,0,1); .below() subtracts 1 from Y). Indexed by the RailShape enum value (which
// matches the Java ordinal 1:1 — see level/block/properties_enum.go).
//
//	[VERIFIED CFR AbstractMinecart.EXITS: NORTH_SOUTH(zNeg,zPos), EAST_WEST(xNeg,xPos),
//	 ASCENDING_EAST(xNegBelow,xPos), ASCENDING_WEST(xNeg,xPosBelow), ASCENDING_NORTH(zNeg,zPosBelow),
//	 ASCENDING_SOUTH(zNegBelow,zPos), SOUTH_EAST(zPos,xPos), SOUTH_WEST(zPos,xNeg), NORTH_WEST(zNeg,xNeg),
//	 NORTH_EAST(zNeg,xPos).]
var minecartExits = map[block.RailShape][2]railExit{
	block.RailShapeNorthSouth:     {{0, 0, -1}, {0, 0, 1}},
	block.RailShapeEastWest:       {{-1, 0, 0}, {1, 0, 0}},
	block.RailShapeAscendingEast:  {{-1, -1, 0}, {1, 0, 0}},
	block.RailShapeAscendingWest:  {{-1, 0, 0}, {1, -1, 0}},
	block.RailShapeAscendingNorth: {{0, 0, -1}, {0, -1, 1}},
	block.RailShapeAscendingSouth: {{0, -1, -1}, {0, 0, 1}},
	block.RailShapeSouthEast:      {{0, 0, 1}, {1, 0, 0}},
	block.RailShapeSouthWest:      {{0, 0, 1}, {-1, 0, 0}},
	block.RailShapeNorthWest:      {{0, 0, -1}, {-1, 0, 0}},
	block.RailShapeNorthEast:      {{0, 0, -1}, {1, 0, 0}},
}

// minecartExitsOf ports AbstractMinecart.exits(shape) == EXITS.get(shape).
func minecartExitsOf(shape block.RailShape) [2]railExit {
	return minecartExits[shape]
}

// railShapeIsSlope ports RailShape.isSlope(): the four ASCENDING shapes.
//
//	[VERIFIED CFR RailShape.isSlope: this == ASCENDING_NORTH/EAST/SOUTH/WEST.]
func railShapeIsSlope(shape block.RailShape) bool {
	switch shape {
	case block.RailShapeAscendingEast, block.RailShapeAscendingWest,
		block.RailShapeAscendingNorth, block.RailShapeAscendingSouth:
		return true
	}
	return false
}

// minecartContainerSize returns the container slot count for a container-minecart type, or 0 for a plain
// (non-container) minecart. MinecartChest.getContainerSize()==27; MinecartHopper.getContainerSize()==5.
//
//	[VERIFIED CFR MinecartChest.getContainerSize: return 27; MinecartHopper.getContainerSize: return 5.]
func minecartContainerSize(typ entity.ID) int {
	switch typ {
	case entity.ChestMinecart.ID:
		return 27
	case entity.HopperMinecart.ID:
		return 5
	}
	return 0
}

// --- SPAWN ---------------------------------------------------------------------------------------

// spawnMinecart creates an AbstractMinecart of the given type at (x,y,z) and adds it to the owning
// region's store (the tracker broadcasts its ClientboundAddEntity next tick, exactly as the arrow /
// item drop rides the store-add path). A chest/hopper minecart gets its container backing allocated.
// Returns the spawned entity. Cite AbstractMinecart.createMinecart + the EntityType registration.
func (t *TickLoop) spawnMinecart(typ entity.ID, x, y, z float64) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), minecartTypeRecord(typ), x, y, z)
	e.isMinecart = true
	// A container minecart carries its itemStacks (AbstractMinecartContainer.itemStacks, sized by type).
	if sz := minecartContainerSize(typ); sz > 0 {
		e.minecartItems = make([]component.SlotData, sz)
	}
	// A TNT minecart starts UN-primed: MinecartTNT.<init> sets fuse = -1 (NO_FUSE). primeFuse (an
	// activator rail / a high-speed crash / a burning-arrow hit) sets it to 80. CITE MinecartTNT.<init>
	// (fuse = -1; explosionPowerBase = 4.0; explosionSpeedFactor = 1.0).
	if typ == entity.TntMinecart.ID {
		e.mcTntFuse = -1
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// minecartTypeRecord returns the data/entity.Entity table record for a minecart type id (for NewEntity's
// AABB/dims copy). Falls back to the plain Minecart record for an unknown id.
func minecartTypeRecord(typ entity.ID) entity.Entity {
	switch typ {
	case entity.ChestMinecart.ID:
		return entity.ChestMinecart
	case entity.HopperMinecart.ID:
		return entity.HopperMinecart
	case entity.FurnaceMinecart.ID:
		return entity.FurnaceMinecart
	case entity.TntMinecart.ID:
		return entity.TntMinecart
	}
	return entity.Minecart
}

// --- TICK ----------------------------------------------------------------------------------------

// tickMinecarts drives every minecart in every region, the sibling of tickArrows/tickItems. It runs on
// the coordinator (quiescent — every region joined at the barrier) and processes each region WITH that
// region registered (withRegion) so moveMinecart's t.cur() (the moveEntity re-bucket) resolves to the
// cart's OWN store. A per-region snapshot keeps the loop stable across any in-loop removal.
func (t *TickLoop) tickMinecarts() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, r.entities.len())
		for _, e := range r.entities.all() {
			if e.isMinecart {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickMinecart(e)
			}
		})
	}
}

// tickMinecart ports AbstractMinecart.tick → OldMinecartBehavior.tick for one cart (ServerLevel branch):
// baseTick position bookkeeping (xo/yo/zo/yRotO), applyGravity, read the rail at the current cell, then
// moveAlongTrack (on a rail) OR comeOffTrack (off a rail), then derive the yaw + the flipped latch.
//
// Vanilla order (OldMinecartBehavior.tick, ServerLevel branch):
//
//	applyGravity();
//	pos = getCurrentBlockPosOrRailBelow(); state = getBlockState(pos); onRails = isRail(state); setOnRails;
//	if (onRails) { moveAlongTrack(level); if ACTIVATOR_RAIL activateMinecart(...) } else comeOffTrack(level);
//	applyEffectsFromBlocks();  // v1: cited deferral (no block-effect subsystem)
//	setXRot(0); derive yRot from (xo-x, zo-z); flip latch on a >170deg wrap; setXRot%360; setYRot%360;
//	pushAndPickupEntities();   // v1: cited deferral (single-cart rail physics is the target)
func (t *TickLoop) tickMinecart(e *Entity) {
	// baseTick previous-position bookkeeping: xo/yo/zo/yRotO = current, BEFORE moveAlongTrack moves us.
	e.minecartXo, e.minecartYo, e.minecartZo = e.x, e.y, e.z
	e.minecartYRotO = e.yaw

	// applyGravity: deltaMovement.y -= getDefaultGravity() (0.04 land). (isInWater() water branch deferred
	// — v1 minecarts run on land; the 0.005 water gravity is exposed via minecartGravityWater for later.)
	e.vy -= minecartGravityLand

	// getCurrentBlockPosOrRailBelow (OLD branch): the cart's block cell, but if the cell one BELOW is a
	// rail, snap down to it (a cart sits 1/16 above the rail top, so its own cell is the air above).
	pos := t.minecartCurrentBlockPosOrRailBelow(e)
	state, ok := t.world().GetBlock(pos, dimMinY)
	onRails := ok && block.IsRail(state)

	if onRails {
		t.minecartMoveAlongTrack(e, pos, state)
		// ACTIVATOR_RAIL: activateMinecart(x, y, z, POWERED). AbstractMinecart.activateMinecart is EMPTY on
		// the base/plain/chest cart, but MinecartTNT OVERRIDES it: `if (powered && fuse < 0) primeFuse(null)`
		// — a TNT minecart crossing a POWERED activator rail lights its fuse. Read the rail's POWERED state
		// at the cart's cell and dispatch the override for a TNT cart. CITE: PoweredRailBlock/ActivatorRail
		// (Minecart passes over -> BaseRailBlock; the powered read), MinecartTNT.activateMinecart.
		if block.IsActivatorRailBlock(state) {
			if pw, ok := block.RailPowered(state); ok && pw {
				t.tntMinecartActivate(e)
			}
		}

		// DETECTOR_RAIL entityInside: a cart on an UNPOWERED detector rail powers it (DetectorRailBlock.
		// entityInside -> checkPressed). Re-read the rail state at the cart's post-move cell (moveAlongTrack
		// may have shifted it into a new cell). CITE: DetectorRailBlock.entityInside.
		cellPos := t.minecartCurrentBlockPosOrRailBelow(e)
		if cs, ok := t.world().GetBlock(cellPos, dimMinY); ok && block.IsDetectorRailBlock(cs) {
			if pw, ok := block.RailPowered(cs); !ok || !pw {
				t.detectorRailCheckPressed(cellPos, cs)
			}
		}
	} else {
		t.minecartComeOffTrack(e)
	}
	// applyEffectsFromBlocks(): CITED DEFERRAL — no block-effect subsystem (soul sand slow / honey / bubble
	// columns). The rail-follow numeric ops above are complete without it.

	// setXRot(0): a minecart on a rail keeps a level pitch. Then derive the yaw from the tick's motion.
	e.pitch = 0
	xDiff := e.minecartXo - e.x
	zDiff := e.minecartZo - e.z
	if xDiff*xDiff+zDiff*zDiff > 0.001 {
		e.yaw = float32(mthAtan2(zDiff, xDiff) * 180.0 / math.Pi)
		if e.minecartFlipped {
			e.yaw += 180.0
		}
	}
	// The flipped latch: a >=170deg yaw turn this tick means the cart reversed direction — add 180 and
	// toggle flipped so it renders facing the new way instead of spinning.
	rotDiff := float64(wrapDegreesF(e.yaw - e.minecartYRotO))
	if rotDiff < -170.0 || rotDiff >= 170.0 {
		e.yaw += 180.0
		e.minecartFlipped = !e.minecartFlipped
	}
	e.pitch = float32(math.Mod(float64(e.pitch), 360.0))
	e.yaw = float32(math.Mod(float64(e.yaw), 360.0))
	e.headYaw = e.yaw

	// pushAndPickupEntities(): CITED DEFERRAL — the minecart<->entity push + mob auto-ride broad phase. The
	// single-cart rail physics is the target; a ridden cart's passenger is re-positioned by rideTickVehicles.

	// MinecartTNT.tick tail (runs AFTER super.tick() above, which is the rail physics + activateMinecart):
	// the fuse countdown and, at 0, the velocity-scaled explode. Gated on the TNT-minecart type so every
	// other cart is unaffected. CITE MinecartTNT.tick.
	if e.typ == entity.TntMinecart.ID {
		t.tickTntMinecartFuse(e)
	}
}

// tntMinecartActivate is the port of MinecartTNT.activateMinecart(x, y, z, powered): a POWERED activator
// rail primes an un-primed TNT minecart. Called from tickMinecart's activator-rail branch (already gated
// on powered==true), so this only needs the fuse<0 (not-yet-primed) guard before priming.
//
//	[VERIFIED javap MinecartTNT.activateMinecart: `if (powered && fuse < 0) primeFuse(null);`.]
func (t *TickLoop) tntMinecartActivate(e *Entity) {
	if e.mcTntFuse < 0 {
		t.tntMinecartPrimeFuse(e)
	}
}

// tntMinecartPrimeFuse is the port of MinecartTNT.primeFuse(DamageSource): if TNT_EXPLODES is on, set the
// fuse to 80 (and mark primed) + (in vanilla) play the primed sound (cite-deferred client cue). The
// ignitionSource bookkeeping (the arrow/attacker attribution) is cite-deferred — v1 attributes the blast
// generically (the explode call excludes the cart itself, exactly as the entity blast does).
//
//	[VERIFIED javap MinecartTNT.primeFuse: if (!TNT_EXPLODES) return; fuse = 80; if (!isClientSide) { ...
//	 playSound(TNT_PRIMED) ... }.]
func (t *TickLoop) tntMinecartPrimeFuse(e *Entity) {
	if !tntExplodes {
		return
	}
	e.mcTntFuse = tntDefaultFuseTime
	e.mcTntPrimed = true
	// playSound(SoundEvents.TNT_PRIMED): cite-deferred client cue.
}

// tickTntMinecartFuse is the port of MinecartTNT.tick's fuse branch: while the fuse is > 0, count it down
// (the client SMOKE particle is a cite-deferred render cue); at exactly 0, explode with the velocity-scaled
// power (explode(ignitionSource, deltaMovement.horizontalDistanceSqr())). A fuse < 0 (un-primed) is inert.
//
//	[VERIFIED javap MinecartTNT.tick: if (fuse > 0) { --fuse; addParticle(SMOKE...); } else if (fuse == 0)
//	 explode(ignitionSource, getDeltaMovement().horizontalDistanceSqr()). (The horizontalCollision crash-
//	 prime branch is cite-deferred — no horizontalCollision seam on the minecart entity; the activator-rail
//	 prime is the wired path.)]
func (t *TickLoop) tickTntMinecartFuse(e *Entity) {
	if e.mcTntFuse > 0 {
		e.mcTntFuse--
		// addParticle(SMOKE, x, y+0.5, z, 0,0,0): cite-deferred client render cue.
	} else if e.mcTntFuse == 0 {
		// horizontalDistanceSqr() of the deltaMovement (the horizontal speed², vx²+vz²).
		horizSqr := e.vx*e.vx + e.vz*e.vz
		t.tntMinecartExplode(e, horizSqr)
	}
}

// tntMinecartExplode is the port of MinecartTNT.explode(DamageSource, double horizDistSqr): if TNT_EXPLODES
// is on, run the ServerExplosion at the cart with a velocity-scaled power then discard the cart. The power
// formula is EXACT (bytecode): power = explosionPowerBase(4.0) + explosionSpeedFactor(1.0) * nextDouble() *
// 1.5 * min(sqrt(horizDistSqr), 5.0). The nextDouble() is drawn from the OWNING region's levelRandom
// (this.random == the entity random in vanilla; here we draw from the region levelRandom, the same stream
// the entity-blast rays draw from — a cited reduction, gated on a TNT minecart existing so the pig oracle
// stream is unperturbed). Reuses t.explode (server/explosion.go).
//
//	[VERIFIED javap MinecartTNT.explode: if (TNT_EXPLODES) { d = min(sqrt(horizDistSqr), 5.0); level.explode
//	 (this, source, null, x, y, z, (float)(explosionPowerBase + explosionSpeedFactor*random.nextDouble()*1.5
//	 *d), false, ExplosionInteraction.TNT); } if (isPrimed()) discard().]
func (t *TickLoop) tntMinecartExplode(e *Entity, horizDistSqr float64) {
	if tntExplodes {
		capped := math.Sqrt(horizDistSqr)
		if capped > 5.0 {
			capped = 5.0
		}
		roll := 1.0 // explosionSpeedFactor default 1.0
		if r := t.cur(); r != nil && r.levelRandom != nil {
			roll = r.levelRandom.NextDouble()
		}
		power := tntDefaultExplosionPower + 1.0*roll*1.5*capped // explosionPowerBase 4.0, factor 1.0
		// ExplosionInteraction.TNT, fire=false (MinecartTNT.explode). Always destroys terrain.
		t.explodeWith(e.id, e.x, e.y, e.z, float32Of(power), explosionInteractionTNT, false, nil)
	}
	// if (isPrimed()) discard(): a primed minecart is removed after the blast.
	if e.mcTntPrimed {
		t.cur().entities.remove(e.id)
	}
}

// float32Of narrows a float64 to the float MinecartTNT.explode's (float) cast performs before passing the
// power to level.explode, then widens it back for t.explode's float64 radius param — preserving the
// vanilla single-precision truncation of the power.
func float32Of(v float64) float64 { return float64(float32(v)) }

// minecartCurrentBlockPosOrRailBelow ports AbstractMinecart.getCurrentBlockPosOrRailBelow (the OLD-movement
// branch): the cart's floored block cell, but decremented by one in Y if the cell directly below is a rail
// (the cart floats 1/16 above the rail, so its own cell is the air above the track).
//
//	[VERIFIED CFR AbstractMinecart.getCurrentBlockPosOrRailBelow: xt/yt/zt = floor(x/y/z); (OLD branch)
//	 if (getBlockState(xt, yt-1, zt).is(BlockTags.RAILS)) --yt; return new BlockPos(xt, yt, zt).]
func (t *TickLoop) minecartCurrentBlockPosOrRailBelow(e *Entity) pk.Position {
	xt := mthFloor(e.x)
	yt := mthFloor(e.y)
	zt := mthFloor(e.z)
	below := pk.Position{X: xt, Y: yt - 1, Z: zt}
	if s, ok := t.world().GetBlock(below, dimMinY); ok && block.IsRail(s) {
		yt--
	}
	return pk.Position{X: xt, Y: yt, Z: zt}
}

// minecartComeOffTrack ports AbstractMinecart.comeOffTrack: clamp velocity to maxSpeed, halve it when on
// the ground, move via the swept resolver, then air-drag while airborne. This is the normal free-fall /
// slide physics a minecart runs when it is NOT on a rail.
//
//	[VERIFIED CFR AbstractMinecart.comeOffTrack: maxSpeed = getMaxSpeed(level); movement = getDeltaMovement();
//	 setDeltaMovement(clamp(x,-max,max), y, clamp(z,-max,max)); if (onGround) scale(0.5); move(SELF, delta);
//	 if (!onGround) scale(getAirDrag()==0.95).]
func (t *TickLoop) minecartComeOffTrack(e *Entity) {
	maxSpeed := t.minecartGetMaxSpeed(e)
	e.vx = clampF(e.vx, -maxSpeed, maxSpeed)
	e.vz = clampF(e.vz, -maxSpeed, maxSpeed)
	if e.onGround {
		e.vx *= 0.5
		e.vy *= 0.5
		e.vz *= 0.5
	}
	// move(MoverType.SELF, delta): the swept per-axis collision (moveEntity), which sets onGround + zeroes a
	// blocked-axis velocity. (Vanilla's applyEffectsFromBlocks tail of move is the cited block-effect deferral.)
	t.moveEntity(e, e.vx, e.vy, e.vz)
	if !e.onGround {
		e.vx *= minecartAirDrag
		e.vy *= minecartAirDrag
		e.vz *= minecartAirDrag
	}
}

// minecartGetMaxSpeed ports AbstractMinecart.getMaxSpeed → OldMinecartBehavior.getMaxSpeed: 0.4 on land,
// 0.2 in water. v1 has no minecart-water-immersion check wired, so the land value is used (the water
// branch is exposed via minecartMaxSpeedWater for the later fluid wire).
//
// A FURNACE minecart overrides getMaxSpeed to super.getMaxSpeed() * (isInWater ? 0.75 : 0.5) -- a fueled
// furnace cart is capped at HALF the normal land speed (0.2) so it rolls slower than a plain cart. With
// water deferred, the land base * 0.5 is applied.
//
//	[VERIFIED CFR OldMinecartBehavior.getMaxSpeed: return isInWater() ? 0.2 : 0.4;
//	 MinecartFurnace.getMaxSpeed: return super.getMaxSpeed(level) * (isInWater() ? 0.75 : 0.5).]
func (t *TickLoop) minecartGetMaxSpeed(e *Entity) float64 {
	base := minecartMaxSpeedLand
	if e.typ == entity.FurnaceMinecart.ID {
		return base * minecartFurnaceMaxSpeedFactor // isInWater()==false path (water deferred): * 0.5
	}
	return base
}

// minecartFurnaceMaxSpeedFactor is MinecartFurnace.getMaxSpeed's land multiplier (ldc2_w 0.5d); in water
// it is 0.75d (deferred with the rest of the minecart water wire). See minecartGetMaxSpeed.
const minecartFurnaceMaxSpeedFactor = 0.5

// minecartSlowdownFactor ports OldMinecartBehavior.getSlowdownFactor: 0.997 while carrying a passenger,
// 0.96 empty. The single most-felt minecart tunable (a ridden cart coasts far; an empty one stops fast).
//
//	[VERIFIED CFR OldMinecartBehavior.getSlowdownFactor: return isVehicle() ? 0.997 : 0.96.]
func (e *Entity) minecartSlowdownFactor() float64 {
	if e.isVehicle() {
		return minecartSlowdownRidden
	}
	return minecartSlowdownEmpty
}

// minecartApplyNaturalSlowdown ports AbstractMinecart.applyNaturalSlowdown: multiply the horizontal
// velocity by the behavior's slowdown factor (Y untouched), then an extra 0.95 in water.
//
//	[VERIFIED CFR AbstractMinecart.applyNaturalSlowdown: newMovement = movement.multiply(slowdownFactor,
//	 0.0, slowdownFactor); if (isInWater()) newMovement = newMovement.scale(0.95F); return newMovement.]
func (t *TickLoop) minecartApplyNaturalSlowdown(e *Entity) {
	f := e.minecartSlowdownFactor()
	e.vx *= f
	e.vz *= f
	// Y component: multiply(f, 0.0, f) zeroes Y — but moveAlongTrack has already set Y separately; vanilla's
	// multiply(f, 0.0, f) keeps Y at 0.0 in the horizontal-only slowdown (the cart's Y is driven by the rail
	// snap, not carried velocity). We mirror the (f, 0, f) multiply exactly.
	e.vy *= 0.0
	// isInWater() extra scale: deferred (no minecart-water wire); minecartWaterSlowdown holds the 0.95 factor.
	_ = minecartWaterSlowdown
}

// --- moveAlongTrack: the load-bearing rail physics ----------------------------------------------

// minecartMoveAlongTrack ports OldMinecartBehavior.moveAlongTrack(ServerLevel) — the rail-follow physics.
// Every numeric op mirrors the CFR bytecode: the ascending-slope accel, the EXITS-table velocity
// projection + flip, the rider push, the halt-track brake, the position snap onto the rail, the maxSpeed-
// clamped move, the ascend Y step, applyNaturalSlowdown, the newPos Y-descent momentum add, the block-
// cross velocity redirect, and the powered-rail boost/launch.
//
//	[VERIFIED CFR OldMinecartBehavior.moveAlongTrack — see the block-by-block citations inline below.]
func (t *TickLoop) minecartMoveAlongTrack(e *Entity, pos pk.Position, state block.StateID) {
	e.fallDistance = 0 // minecart.resetFallDistance()

	x := e.x
	y := e.y
	z := e.z
	oldPos, oldPosOK := t.minecartRailPos(x, y, z) // getPos(x,y,z) BEFORE the move (for the Y-descent momentum)

	y = float64(pos.Y)

	// powerTrack / haltTrack: a POWERED powered_rail boosts; an UNPOWERED powered_rail brakes.
	powerTrack := false
	haltTrack := false
	if block.IsPoweredRailBlock(state) {
		if p, ok := block.RailPowered(state); ok {
			powerTrack = p
			haltTrack = !p
		}
	}

	// slideSpeed = 0.0078125 (×0.2 in water — deferred). The ascending-slope acceleration.
	slideSpeed := minecartSlideSpeed

	shape, _ := block.RailShapeOf(state)

	// Ascending shapes: add the slide accel along the DOWNHILL axis, and raise the working Y by 1 (the cart
	// climbs to the top of the ascending rail's cell). VERIFIED per-case:
	switch shape {
	case block.RailShapeAscendingEast:
		e.vx += -slideSpeed
		y += 1.0
	case block.RailShapeAscendingWest:
		e.vx += slideSpeed
		y += 1.0
	case block.RailShapeAscendingNorth:
		e.vz += slideSpeed
		y += 1.0
	case block.RailShapeAscendingSouth:
		e.vz += -slideSpeed
		y += 1.0
	}

	// EXITS projection: project the current velocity onto the rail axis (exit0→exit1), flipping the axis to
	// match the velocity's direction, then rescale to min(2.0, horizontalDistance). This is what keeps a cart
	// travelling ALONG the rail (and redirects it around a curve).
	exits := minecartExitsOf(shape)
	exit0, exit1 := exits[0], exits[1]
	xD := float64(exit1.x - exit0.x)
	zD := float64(exit1.z - exit0.z)
	length := math.Sqrt(xD*xD + zD*zD)
	flip := e.vx*xD + e.vz*zD
	if flip < 0.0 {
		xD = -xD
		zD = -zD
	}
	pow := math.Min(2.0, math.Hypot(e.vx, e.vz))
	e.vx = pow * xD / length
	e.vz = pow * zD / length

	// Rider WASD momentum (getLastClientMoveIntent): a Player controlling passenger's move-intent nudges a
	// near-stopped cart by 0.001 along the intent. CITED DEFERRAL — the client move-intent packet path is not
	// wired in v1 (moveIntent == Vec3.ZERO), so this branch is inert; a ridden cart still rolls with the
	// projected momentum above + the 0.75 ride scale below. Structured to read a real intent once decoded.
	//	[VERIFIED CFR OldMinecartBehavior.moveAlongTrack: if controllingPassenger instanceof Player &&
	//	 moveIntent.lengthSqr()>0 && ownDist<0.01 -> deltaMovement.add(intent.x*0.001,0,intent.z*0.001); haltTrack=false.]

	// haltTrack (unpowered powered_rail): brake. Below 0.03 horizontal → stop dead; else halve horizontal.
	if haltTrack {
		if math.Hypot(e.vx, e.vz) < 0.03 {
			e.vx, e.vy, e.vz = 0, 0, 0
		} else {
			e.vx *= 0.5
			e.vy = 0.0
			e.vz *= 0.5
		}
	}

	// Position SNAP onto the rail: compute the two rail-end midpoints (x0,z0)-(x1,z1), project the cart's
	// (x,z) onto that segment (progress), and set the cart's position to the projected point. This is what
	// keeps a cart glued to the centre of the track no matter its free (x,z).
	x0 := float64(pos.X) + 0.5 + float64(exit0.x)*0.5
	z0 := float64(pos.Z) + 0.5 + float64(exit0.z)*0.5
	x1 := float64(pos.X) + 0.5 + float64(exit1.x)*0.5
	z1 := float64(pos.Z) + 0.5 + float64(exit1.z)*0.5
	xD = x1 - x0
	zD = z1 - z0
	var progress float64
	if xD == 0.0 {
		progress = z - float64(pos.Z)
	} else if zD == 0.0 {
		progress = x - float64(pos.X)
	} else {
		xx := x - x0
		zz := z - z0
		progress = (xx*xD + zz*zD) * 2.0
	}
	x = x0 + xD*progress
	z = z0 + zD*progress
	t.minecartSetPos(e, x, y, z)

	// The maxSpeed-clamped horizontal move: a ridden cart moves at 0.75 scale. move(SELF, (clamp(scale*vx),
	// 0, clamp(scale*vz))) — Y is 0 here (the rail snap + the ascend step below own Y).
	scale := 1.0
	if e.isVehicle() {
		scale = minecartRideScale
	}
	maxSpeed := t.minecartGetMaxSpeed(e)
	dx := clampF(scale*e.vx, -maxSpeed, maxSpeed)
	dz := clampF(scale*e.vz, -maxSpeed, maxSpeed)
	t.moveEntity(e, dx, 0.0, dz)

	// The ascend Y step: if the cart floored into the low end (exit0.y != 0) or the high end (exit1.y != 0)
	// of an ascending rail's neighbour cell, bump Y by that end's Y so it climbs/descends the slope cleanly.
	if exit0.y != 0 && mthFloor(e.x)-pos.X == exit0.x && mthFloor(e.z)-pos.Z == exit0.z {
		t.minecartSetPos(e, e.x, e.y+float64(exit0.y), e.z)
	} else if exit1.y != 0 && mthFloor(e.x)-pos.X == exit1.x && mthFloor(e.z)-pos.Z == exit1.z {
		t.minecartSetPos(e, e.x, e.y+float64(exit1.y), e.z)
	}

	// applyNaturalSlowdown: the 0.997 ridden / 0.96 empty horizontal friction (Y zeroed).
	t.minecartApplyNaturalSlowdown(e)

	// The Y-descent momentum add: compare the rail-projected Y before vs after the move; a NET DESCENT
	// (oldPos.y > newPos.y) adds a small forward speed proportional to the drop (so a cart accelerates
	// downhill), and re-snaps Y to the new rail Y.
	newPos, newPosOK := t.minecartRailPos(e.x, e.y, e.z)
	if newPosOK && oldPosOK {
		speed := (oldPos[1] - newPos[1]) * 0.05
		otherPow := math.Hypot(e.vx, e.vz)
		if otherPow > 0.0 {
			e.vx = e.vx * (otherPow + speed) / otherPow
			e.vz = e.vz * (otherPow + speed) / otherPow
		}
		t.minecartSetPos(e, e.x, newPos[1], e.z)
	}

	// The block-cross velocity redirect: if the move carried the cart into a NEW block column (its floored
	// x/z left the rail cell), re-point the horizontal velocity toward the crossed axis at the current speed
	// (so momentum survives the cell handoff onto the next rail).
	xn := mthFloor(e.x)
	zn := mthFloor(e.z)
	if xn != pos.X || zn != pos.Z {
		otherPow := math.Hypot(e.vx, e.vz)
		e.vx = otherPow * float64(xn-pos.X)
		e.vz = otherPow * float64(zn-pos.Z)
	}

	// The powered-rail boost/launch: on a POWERED powered_rail, either boost an already-moving cart by 0.06
	// along its direction, or — if it is essentially stopped — launch it at 0.02 away from an adjacent
	// redstone-conductor block (so a cart pushed onto a powered rail against a wall starts rolling).
	if powerTrack {
		speedLength := math.Hypot(e.vx, e.vz)
		if speedLength > 0.01 {
			e.vx += e.vx / speedLength * minecartPoweredBoost
			e.vz += e.vz / speedLength * minecartPoweredBoost
		} else {
			ndx := e.vx
			ndz := e.vz
			switch shape {
			case block.RailShapeEastWest:
				if t.minecartIsRedstoneConductor(relative(pos, block.West)) {
					ndx = minecartPoweredKick
				} else if t.minecartIsRedstoneConductor(relative(pos, block.East)) {
					ndx = -minecartPoweredKick
				}
			case block.RailShapeNorthSouth:
				if t.minecartIsRedstoneConductor(relative(pos, block.North)) {
					ndz = minecartPoweredKick
				} else if t.minecartIsRedstoneConductor(relative(pos, block.South)) {
					ndz = -minecartPoweredKick
				}
			default:
				return // any other shape: no launch (the vanilla `else return`).
			}
			e.vx = ndx
			e.vz = ndz
		}
	}
}

// minecartRailPos ports OldMinecartBehavior.getPos(x,y,z): the exact point on the rail at (x,y,z) — the
// rail-end midpoints, the progress projection, and the +0.5/+1.0 Y bump for a slope. Returns (point, true)
// on a rail, (zero, false) off a rail. Used for the Y-descent momentum (the before/after rail-Y diff).
//
//	[VERIFIED CFR OldMinecartBehavior.getPos: xt/yt/zt = floor; if (getBlockState(xt,yt-1,zt).is(RAILS)) --yt;
//	 if isRail: shape, exits; x0=xt+0.5+e0.x*0.5, y0=yt+0.0625+e0.y*0.5, z0=zt+0.5+e0.z*0.5 (and e1); yD=
//	 (y1-y0)*2; project x/y/z by progress; if yD<0 y+=1 else if yD>0 y+=0.5; return Vec3(x,y,z).]
func (t *TickLoop) minecartRailPos(x, y, z float64) ([3]float64, bool) {
	xt := mthFloor(x)
	yt := mthFloor(y)
	zt := mthFloor(z)
	below := pk.Position{X: xt, Y: yt - 1, Z: zt}
	if s, ok := t.world().GetBlock(below, dimMinY); ok && block.IsRail(s) {
		yt--
	}
	state, ok := t.world().GetBlock(pk.Position{X: xt, Y: yt, Z: zt}, dimMinY)
	if !ok || !block.IsRail(state) {
		return [3]float64{}, false
	}
	shape, _ := block.RailShapeOf(state)
	exits := minecartExitsOf(shape)
	exit0, exit1 := exits[0], exits[1]
	x0 := float64(xt) + 0.5 + float64(exit0.x)*0.5
	y0 := float64(yt) + 0.0625 + float64(exit0.y)*0.5
	z0 := float64(zt) + 0.5 + float64(exit0.z)*0.5
	x1 := float64(xt) + 0.5 + float64(exit1.x)*0.5
	y1 := float64(yt) + 0.0625 + float64(exit1.y)*0.5
	z1 := float64(zt) + 0.5 + float64(exit1.z)*0.5
	xD := x1 - x0
	yD := (y1 - y0) * 2.0
	zD := z1 - z0
	var progress float64
	if xD == 0.0 {
		progress = z - float64(zt)
	} else if zD == 0.0 {
		progress = x - float64(xt)
	} else {
		xx := x - x0
		zz := z - z0
		progress = (xx*xD + zz*zD) * 2.0
	}
	x = x0 + xD*progress
	y = y0 + yD*progress
	z = z0 + zD*progress
	if yD < 0.0 {
		y += 1.0
	} else if yD > 0.0 {
		y += 0.5
	}
	return [3]float64{x, y, z}, true
}

// minecartSetPos ports MinecartBehavior.setPos → Entity.setPos through the tick-owned store (entities.move
// re-buckets so the tracker's near() stays consistent). This is the single position-write path in the
// rail physics; it does NOT run collision (the rail snap is exact) — moveEntity is used only for the
// horizontal move step (which DOES collide).
func (t *TickLoop) minecartSetPos(e *Entity, x, y, z float64) {
	t.cur().entities.move(e, x, y, z)
}

// minecartIsRedstoneConductor ports AbstractMinecart.isRedstoneConductor(pos): the block at pos is a
// redstone conductor (a full opaque cube). Used by the powered-rail launch to find a wall to push off.
//
//	[VERIFIED CFR AbstractMinecart.isRedstoneConductor: return getBlockState(pos).isRedstoneConductor(level,pos).]
func (t *TickLoop) minecartIsRedstoneConductor(pos pk.Position) bool {
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	return block.IsRedstoneConductor(s)
}

// --- small Mth helpers (float64-exact ports) -----------------------------------------------------

// clampF ports Mth.clamp(double, double, double).
func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// --- DETECTOR RAIL (net.minecraft.world.level.block.DetectorRailBlock) -----------------------------

// detectorRailSearchInset is DetectorRailBlock.getSearchBB's 0.2-block inset: the box (pos+0.2, pos.y,
// pos+0.2)..(pos+0.8, pos+0.8, pos+0.8) it tests for a minecart.
//
//	[VERIFIED CFR DetectorRailBlock.getSearchBB: double b = 0.2; new AABB(x+0.2, y, z+0.2, x+1-0.2,
//	 y+1-0.2, z+1-0.2).]
const detectorRailSearchInset = 0.2

// detectorRailHasMinecart reports whether any minecart's feet-anchored box intersects the detector rail's
// getSearchBB at pos (getInteractingMinecartOfType(AbstractMinecart.class, getSearchBB, e -> true)).
func (t *TickLoop) detectorRailHasMinecart(pos pk.Position) bool {
	loX := float64(pos.X) + detectorRailSearchInset
	loY := float64(pos.Y)
	loZ := float64(pos.Z) + detectorRailSearchInset
	hiX := float64(pos.X) + 1.0 - detectorRailSearchInset
	hiY := float64(pos.Y) + 1.0 - detectorRailSearchInset
	hiZ := float64(pos.Z) + 1.0 - detectorRailSearchInset
	cx := float64(pos.X) + 0.5
	cz := float64(pos.Z) + 0.5
	for _, e := range t.entitiesNearAcrossRegions(cx, cz, 1) {
		if !e.isMinecart {
			continue
		}
		ihw := e.width / 2
		if hiX <= e.x-ihw || e.x+ihw <= loX ||
			hiY <= e.y || e.y+e.height <= loY ||
			hiZ <= e.z-ihw || e.z+ihw <= loZ {
			continue
		}
		return true
	}
	return false
}

// detectorRailCheckPressed ports DetectorRailBlock.checkPressed: recompute whether a minecart is on the
// rail; on a wasPressed→shouldBePressed transition flip POWERED, write the state, notify neighbors at pos
// and pos.below (so an adjacent redstone consumer / comparator reacts), and — while pressed — schedule a
// re-check tick 20 later that clears POWERED once the cart leaves. The comparator analog output (a
// container-minecart's fill fraction) is surfaced via getAnalogOutputSignal (getRedstoneSignalFromContainer),
// which the neighbor comparator reads on its own neighborChanged.
//
//	[VERIFIED CFR DetectorRailBlock.checkPressed: shouldBePressed = !getInteractingMinecart().isEmpty();
//	 on transition setValue(POWERED, x) + setBlock + updatePowerToConnected + updateNeighborsAt(pos) +
//	 updateNeighborsAt(pos.below); if (shouldBePressed) scheduleTick(pos, this, 20);
//	 updateNeighbourForOutputSignal(pos, this).]
func (t *TickLoop) detectorRailCheckPressed(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	wasPressed := false
	if p, ok := block.RailPowered(state); ok {
		wasPressed = p
	}
	shouldBePressed := t.detectorRailHasMinecart(pos)

	if shouldBePressed != wasPressed {
		newState, ok := block.SetRailPowered(state, shouldBePressed)
		if ok && t.world().SetBlock(pos, newState, dimMinY) {
			t.broadcastBlockUpdate(pos, newState)
			// updateNeighborsAt(pos) + updateNeighborsAt(pos.below): notify the redstone consumers so a wire/
			// repeater/comparator/piston beside or under the rail reacts to the POWERED change.
			t.onRedstoneEdit(pos)
			t.onRedstoneEdit(relative(pos, block.Down))
		}
	}
	// while pressed: schedule the 20-tick re-check that clears POWERED once the cart rolls off.
	if shouldBePressed {
		t.scheduleBlockTick(pos, detectorRailTickType, 20)
	}
}

// --- ITEM → PLACEMENT: a minecart item used on a rail spawns the entity ---------------------------

// minecartItemToEntityType maps a minecart ITEM id (data/item) to the AbstractMinecart ENTITY type id it
// spawns (the MinecartItem.type). Returns (0, false) for a non-minecart item. FurnaceMinecart / TntMinecart
// map to their entity types too (they are spawnable placeables); their runtime behavior (fuel-drive / TNT
// explosion) is a cited deferral, but the entity still spawns + rides the rail via the shared physics.
//
//	[VERIFIED data/item: minecart=882, chest_minecart=883, furnace_minecart=884, tnt_minecart=885,
//	 hopper_minecart=886; the entity ids are Minecart=85, ChestMinecart=25, HopperMinecart=65, etc.]
func minecartItemToEntityType(itemID int32) (entity.ID, bool) {
	switch itemID {
	case 882: // minecraft:minecart
		return entity.Minecart.ID, true
	case 883: // minecraft:chest_minecart
		return entity.ChestMinecart.ID, true
	case 884: // minecraft:furnace_minecart
		return entity.FurnaceMinecart.ID, true
	case 885: // minecraft:tnt_minecart
		return entity.TntMinecart.ID, true
	case 886: // minecraft:hopper_minecart
		return entity.HopperMinecart.ID, true
	}
	return 0, false
}

// tryPlaceMinecartOnRail ports MinecartItem.useOn: if the clicked block is a RAIL, spawn the cart at
// (x+0.5, y+0.0625+slopeOffset, z+0.5) [offset 0.5 for a slope shape], broadcast it (via the store add →
// tracker), and shrink the held item by 1 (survival). Returns true when the rail was clicked (cart spawned),
// false when the clicked block is NOT a rail (FAIL — placement continues). Runs on the dispatch goroutine;
// the store add is wrapped in the owning region so the cart lands in the right store (like reconcileEdit).
//
//	[VERIFIED CFR MinecartItem.useOn: if (!blockState.is(BlockTags.RAILS)) return FAIL; shape read;
//	 offset = isSlope()?0.5:0; spawnPos = (x+0.5, y+0.0625+offset, z+0.5); addFreshEntity; itemStack.shrink(1).]
func (t *TickLoop) tryPlaceMinecartOnRail(p *tickPlayer, inv *Inventory, pos pk.Position, mcType entity.ID) bool {
	if t.world() == nil {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsRail(state) {
		return false // not a rail: MinecartItem.useOn returns FAIL (placement continues → no-op item)
	}
	// Reach-gate (server-authoritative, like the place/break paths).
	if !t.withinReach(p, pos) {
		return false
	}
	offset := 0.0
	if shape, ok := block.RailShapeOf(state); ok && railShapeIsSlope(shape) {
		offset = 0.5
	}
	sx := float64(pos.X) + 0.5
	sy := float64(pos.Y) + 0.0625 + offset
	sz := float64(pos.Z) + 0.5

	// addFreshEntity in the region owning the rail column (so the cart's store/tracker resolve correctly),
	// mirroring the withRegion wrap the block-place path uses for its per-region scheduling.
	t.withRegion(t.regionForColumn(columnOf(sx, sz)), func() {
		t.spawnMinecart(mcType, sx, sy, sz)
	})

	// itemStack.shrink(1) — survival only (creative keeps the item, ServerPlayerGameMode.useItemOn's
	// hasInfiniteMaterials guard, the same shrinkHeldItem gate the block-place path uses).
	if p.gameMode != gameModeCreative {
		t.shrinkHeldItem(p, inv)
	}
	return true
}
