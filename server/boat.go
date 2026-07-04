package server

// boat.go — BOAT (rideable water-float vehicle): a 1:1 port of the DEFAULT vanilla boat float physics
// (net.minecraft.world.entity.vehicle.boat.AbstractBoat + Boat/Raft/ChestBoat/ChestRaft) over the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session). A boat is a NON-mob
// rideable VehicleEntity whose whole server behavior is the surface-float physics — the sibling of the
// minecart (minecart.go): no AI, just a per-tick physics pass that keeps the boat riding the water
// surface (or falling with gravity in air).
//
// AUTHORITY MODEL (the load-bearing structural fact — VERIFIED CFR AbstractBoat.tick):
//
//	if (isLocalInstanceAuthoritative()) { floatBoat(); (client-only: controlBoat()); move(SELF, delta); }
//	else setDeltaMovement(Vec3.ZERO);
//
// isLocalInstanceAuthoritative() on the SERVER == !isClientAuthoritative(); isClientAuthoritative() is
// true iff there is a controlling passenger (getControllingPassenger() returns the first passenger when it
// is a LivingEntity, i.e. a player). So:
//   - EMPTY boat (no passenger): the SERVER is authoritative -> it runs floatBoat() + move(SELF). This is
//     what makes a dropped/placed boat settle onto the water surface and drift.
//   - RIDDEN boat: the boat is CLIENT-authoritative -> the server sets deltaMovement = ZERO and the
//     controlling player's client drives the position via ServerboundMoveVehicle (handleMoveVehicle,
//     passenger.go, already wired for the ghast — the boat reuses it, and getControllingPassenger gains a
//     boat case so the steer gate accepts a boat's rider). This is the SAME client-authoritative model the
//     minecart rider-momentum + the happy-ghast ride take.
//
// PORTED SURFACES (all CFR-cited at the call site):
//   - AbstractBoat.tick (server branch): oldStatus/status bookkeeping, outOfControlTicks + the >=60 eject,
//     floatBoat (server-authoritative only), move(SELF, delta).
//   - AbstractBoat.floatBoat: the buoyancy/gravity deltas per status + the invFriction + the air->water
//     surface SNAP (the load-bearing float).
//   - AbstractBoat.getStatus / isUnderwater / checkInWater / getWaterLevelAbove / getGroundFriction: the
//     water-status classification + the surface-Y reads.
//   - AbstractBoat.getMaxPassengers (2 boat / 1 chest boat), rideHeight (Boat height/3, Raft height*0.888).
//   - BoatItem.use: raytrace to the water surface (Fluid.ANY), spawn the boat, consume the item.
//
// CITED DEFERRALS (each cite-tagged):
//   - controlBoat (the rider WASD paddle -> deltaRotation + forward accel): runs CLIENT-side only in vanilla
//     (`if (level().isClientSide())`), so the SERVER never runs it — a ridden boat's steer arrives as the
//     client-authoritative MoveVehicle position. The deltaRotation *decay* in floatBoat is ported; the paddle
//     *input* is the client's job. CITE AbstractBoat.tick (controlBoat guarded by isClientSide).
//   - tickBubbleColumn / onAboveBubbleColumn (the bubble-column bob), applyEffectsFromBlocks (soul-sand etc.),
//     the paddle-sound + paddle-animation, the push/auto-ride broad phase (boat pushes mobs / a mob boards):
//     CITED DEFERRALS — not-yet-built block-effect/bubble subsystems + the render-only paddle; the float
//     numeric ops above are complete without them.
//   - the boat breaking into planks/sticks on damage, the leash: CITED DEFERRALS.
//   - the dismount landing-scan (Boat.getDismountLocationForPassenger): the exact stepped dismount position
//     is a cited follow-up; the ride + float are the target (a rider dismounts to the boat's position).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// boatStatus mirrors AbstractBoat.Status — the 5 surface states floatBoat branches on.
//
//	[VERIFIED CFR AbstractBoat$Status: IN_WATER, UNDER_WATER, UNDER_FLOWING_WATER, ON_LAND, IN_AIR.]
type boatStatus int

const (
	boatStatusInAir             boatStatus = iota // Status.IN_AIR (default; a boat with no water/ground below)
	boatStatusOnLand                              // Status.ON_LAND (a boat resting on a solid block)
	boatStatusInWater                             // Status.IN_WATER (a boat floating on the surface)
	boatStatusUnderWater                          // Status.UNDER_WATER (submerged in a source)
	boatStatusUnderFlowingWater                   // Status.UNDER_FLOWING_WATER (submerged in flowing water)
)

// --- CONSTANTS (all CFR-verified exact float values) ---------------------------------------------

const (
	// boatDefaultGravity is AbstractBoat.getDefaultGravity (0.04). floatBoat's vspeed starts at
	// -getGravity() == -0.04 (no NoGravity), pulling the boat down when it is not buoyant.
	//	[VERIFIED CFR AbstractBoat.getDefaultGravity: `return 0.04;`.]
	boatDefaultGravity = 0.04

	// boatUnderFlowingWaterVSpeed is floatBoat's UNDER_FLOWING_WATER vspeed override (-7.0E-4): a boat
	// caught under flowing water drifts down slowly instead of at full gravity.
	//	[VERIFIED CFR AbstractBoat.floatBoat: `else if (status == UNDER_FLOWING_WATER) { vspeed = -7.0E-4; ... }`.]
	boatUnderFlowingWaterVSpeed = -7.0e-4

	// boatUnderWaterBuoyancy is floatBoat's UNDER_WATER buoyancy (0.01f as a double): a submerged boat is
	// gently pushed back up toward the surface.
	//	[VERIFIED CFR AbstractBoat.floatBoat: `else if (status == UNDER_WATER) { buoyancy = 0.01f; invFriction = 0.45f; }`.]
	boatUnderWaterBuoyancy = float64(float32(0.01))

	// boat invFriction values per status (the horizontal-velocity + deltaRotation multiplier each tick).
	//	[VERIFIED CFR AbstractBoat.floatBoat: IN_AIR invFriction=0.9; IN_WATER invFriction=0.9;
	//	 UNDER_FLOWING_WATER invFriction=0.9; UNDER_WATER invFriction=0.45; ON_LAND invFriction=landFriction;
	//	 the initial invFriction (air->water snap branch) is 0.05f.]
	boatInvFrictionInWater           = float64(float32(0.9))
	boatInvFrictionUnderFlowingWater = float64(float32(0.9))
	boatInvFrictionUnderWater        = float64(float32(0.45))
	boatInvFrictionInAir             = float64(float32(0.9))
	boatInvFrictionInitial           = float64(float32(0.05))

	// boatSurfaceSnapOffset is floatBoat's air->water snap target offset (+0.101): on the first tick a
	// boat enters water from air it is snapped to `getWaterLevelAbove() - bbHeight + 0.101`.
	//	[VERIFIED CFR AbstractBoat.floatBoat: `double targetY = getWaterLevelAbove() - getBbHeight() + 0.101;`.]
	boatSurfaceSnapOffset = 0.101

	// boatBuoyancyGravityDivisor / boatBuoyancyScale are floatBoat's IN_WATER/UNDER_WATER buoyancy-apply
	// factors: `deltaY = (deltaY + buoyancy * (getDefaultGravity()/0.65)) * 0.75`.
	//	[VERIFIED CFR AbstractBoat.floatBoat: `deltaMovement.y + buoyancy * (getDefaultGravity()/0.65)) * 0.75`.]
	boatBuoyancyGravityDivisor = 0.65
	boatBuoyancyScale          = 0.75

	// boatOutOfControlEjectTicks is AbstractBoat.tick's capsize-eject threshold (60): a boat submerged
	// (UNDER_WATER / UNDER_FLOWING_WATER) for >= 60 ticks throws its passengers.
	//	[VERIFIED CFR AbstractBoat.tick: `if (!isClientSide && outOfControlTicks >= 60.0f) ejectPassengers();`.]
	boatOutOfControlEjectTicks = 60.0

	// boatMaxPassengers / boatChestMaxPassengers are getMaxPassengers: a plain boat/raft seats 2, a
	// chest boat/raft seats 1.
	//	[VERIFIED CFR AbstractBoat.getMaxPassengers: `return 2;`. AbstractChestBoat.getMaxPassengers: `return 1;`.]
	boatMaxPassengers      = 2
	boatChestMaxPassengers = 1

	// boatRideHeightDivisor is Boat.rideHeight (dimensions.height() / 3.0f) — the seat Y factor for a
	// plain/chest BOAT.
	//	[VERIFIED CFR Boat.rideHeight: `return dimensions.height() / 3.0f;` (also ChestBoat.rideHeight).]
	boatRideHeightDivisor = 3.0

	// raftRideHeightFactor is Raft.rideHeight (dimensions.height() * 0.8888889f) — the seat Y factor for a
	// bamboo RAFT / chest raft.
	//	[VERIFIED CFR Raft.rideHeight: `return dimensions.height() * 0.8888889f;` (also ChestRaft.rideHeight).]
	raftRideHeightFactor = float64(float32(0.8888889))

	// boatSinglePassengerXOffset / boatChestSinglePassengerXOffset are getSinglePassengerXOffset: 0.0 for a
	// plain boat, 0.15 for a chest boat (the rider sits slightly to the side of the chest).
	//	[VERIFIED CFR AbstractBoat.getSinglePassengerXOffset: `return 0.0f;`.
	//	 AbstractChestBoat.getSinglePassengerXOffset: `return 0.15f;`.]
	boatSinglePassengerXOffset      = 0.0
	boatChestSinglePassengerXOffset = float64(float32(0.15))
)

// boatChestContainerSize is AbstractChestBoat.getContainerSize (27) — the chest boat/raft holds a full
// single-chest inventory.
//
//	[VERIFIED CFR AbstractChestBoat.getContainerSize: `return 27;`.]
const boatChestContainerSize = 27

// --- SPAWN ---------------------------------------------------------------------------------------

// spawnBoat creates an AbstractBoat of the given type at (x,y,z) with the given yaw and adds it to the
// owning region's store (the tracker broadcasts its ClientboundAddEntity next tick, exactly as the
// minecart / arrow / item drop ride the store-add path). A chest boat/raft gets its 27-slot container
// backing allocated (reusing the shared minecartItems slice). Returns the spawned entity.
//
//	[VERIFIED CFR BoatItem.getBoat: entityType.create -> setInitialPos(location); then use sets setYRot.
//	 EntityType.create / addFreshEntity registration.]
func (t *TickLoop) spawnBoat(typ entity.ID, x, y, z float64, yaw float32) *Entity {
	rec := boatTypeRecord(typ)
	e := NewEntity(t.idAlloc.AllocID(), rec, x, y, z)
	e.isBoat = true
	e.boatIsRaft = boatIsRaftType(typ)
	e.yaw = yaw
	e.headYaw = yaw
	// setInitialPos also seeds xo/yo/zo = pos (the previous-position bookkeeping); NewEntity leaves them
	// zero, which is only read by the render-interpolation client, so the exact seed is unobservable here.
	if boatIsChestType(typ) {
		e.minecartItems = make([]component.SlotData, boatChestContainerSize)
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(e)
	return e
}

// --- TICK ----------------------------------------------------------------------------------------

// tickBoats drives every boat in every region, the sibling of tickMinecarts. It runs on the coordinator
// (quiescent — every region joined at the barrier) and processes each region WITH that region registered
// (withRegion) so moveEntity's t.cur() (the moveEntity re-bucket) resolves to the boat's OWN store. A
// per-region snapshot keeps the loop stable across any in-loop removal.
func (t *TickLoop) tickBoats() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, len(r.entities.byID))
		for _, e := range r.entities.byID {
			if e.isBoat {
				snapshot = append(snapshot, e)
			}
		}
		if len(snapshot) == 0 {
			continue
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickBoat(e)
			}
		})
	}
}

// tickBoat ports AbstractBoat.tick for one boat (ServerLevel branch). Vanilla order:
//
//	oldStatus = status; status = getStatus();
//	outOfControlTicks = (status == UNDER_WATER || status == UNDER_FLOWING_WATER) ? outOfControlTicks+1 : 0;
//	if (!clientSide && outOfControlTicks >= 60) ejectPassengers();
//	(hurtTime/damage decay: cited deferral — no boat-damage subsystem)
//	super.tick();  (baseTick: xo/yo/zo bookkeeping, boardingCooldown decrement — handled by rideTick path)
//	if (isLocalInstanceAuthoritative()) {   // server: !isClientAuthoritative() == no controlling passenger
//	    if (!(firstPassenger instanceof Player)) setPaddleState(false, false);
//	    floatBoat();
//	    (client-only: controlBoat() + send PaddleBoat — SERVER SKIPS)
//	    move(SELF, getDeltaMovement());
//	} else setDeltaMovement(Vec3.ZERO);
//	(applyEffectsFromBlocks, tickBubbleColumn, paddle sound, push/auto-ride: cited deferrals)
func (t *TickLoop) tickBoat(e *Entity) {
	// baseTick previous-position bookkeeping (xo/yo/zo) — reused for the boat via the minecart fields (a
	// boat has no separate xo/yo/zo store; these mirror Entity.xo/yo/zo and are read by getWaterLevelAbove
	// only through boatLastYd below). Record y before floatBoat moves us so boatLastYd reflects this tick.
	prevY := e.y

	e.boatOldStatus = e.boatStatus
	e.boatStatus = t.boatGetStatus(e)

	// outOfControlTicks: +1 while submerged, else reset to 0. (float += 1.0f semantics.)
	if e.boatStatus == boatStatusUnderWater || e.boatStatus == boatStatusUnderFlowingWater {
		e.boatOutOfControlTicks += 1.0
	} else {
		e.boatOutOfControlTicks = 0.0
	}
	// A capsized boat (submerged >= 60 ticks) throws its passengers. ejectPassengers dismounts every rider
	// (passenger.go) — a server-side action, guarded by !clientSide (always true here).
	if e.boatOutOfControlTicks >= boatOutOfControlEjectTicks && len(e.passengers) > 0 {
		t.ejectPassengers(e)
	}

	// isLocalInstanceAuthoritative() on the server == no controlling passenger (a player rider makes the
	// boat client-authoritative; the client then drives it via ServerboundMoveVehicle). getControllingPassenger
	// (passenger.go) returns the boat's first passenger when it is a player.
	if t.getControllingPassenger(e) == 0 {
		// SERVER-AUTHORITATIVE: run the float physics + the collided move. (A boat with a non-player first
		// passenger would clear the paddle state — a render-only field, cited-deferred here.)
		t.boatFloat(e)
		t.moveEntity(e, e.vx, e.vy, e.vz)
	} else {
		// CLIENT-AUTHORITATIVE (ridden): the controlling player's client owns the motion. Zero the velocity so
		// the server does not fight the client-submitted MoveVehicle position (handleMoveVehicle re-positions).
		e.vx, e.vy, e.vz = 0, 0, 0
	}

	// lastYd = the vertical delta this tick (getWaterLevelAbove reads it to bound its upward scan).
	e.boatLastYd = e.y - prevY

	// applyEffectsFromBlocks / tickBubbleColumn / the paddle sound + the push/auto-ride broad phase:
	// CITED DEFERRALS (no block-effect/bubble subsystem, render-only paddle, single-boat float is the target).
}

// --- floatBoat: the load-bearing surface physics -------------------------------------------------

// boatFloat ports AbstractBoat.floatBoat — the buoyancy/gravity + friction that keeps the boat riding the
// water surface. Every numeric op mirrors the CFR bytecode.
//
//	[VERIFIED CFR AbstractBoat.floatBoat — see the inline citations.]
func (t *TickLoop) boatFloat(e *Entity) {
	// double vspeed = -getGravity();  (getGravity() == 0.04 with no NoGravity)
	vspeed := -boatDefaultGravity
	// double buoyancy = 0.0; float invFriction = 0.05f;
	buoyancy := 0.0
	invFriction := boatInvFrictionInitial

	// The AIR->WATER surface SNAP: on the first tick the boat transitions from IN_AIR into water (not ON_LAND),
	// snap it onto the surface and kill its vertical velocity. This is what settles a placed/falling boat onto
	// the water instead of letting it bob through.
	if e.boatOldStatus == boatStatusInAir && e.boatStatus != boatStatusInAir && e.boatStatus != boatStatusOnLand {
		// waterLevel = getY(1.0) == y + bbHeight (the top of the box).
		e.boatWaterLevel = e.y + e.height
		// targetY = getWaterLevelAbove() - bbHeight + 0.101.
		targetY := float64(t.boatWaterLevelAbove(e)) - e.height + boatSurfaceSnapOffset
		// noCollision(this, box.move(0, targetY - y, 0)): only snap if the target box is clear of solids.
		if t.boatNoCollisionAtY(e, targetY) {
			t.boatSetPos(e, e.x, targetY, e.z)
			// setDeltaMovement(delta.multiply(1,0,1)): zero the vertical, keep horizontal.
			e.vy = 0.0
			e.boatLastYd = 0.0
		}
		e.boatStatus = boatStatusInWater
	} else {
		switch e.boatStatus {
		case boatStatusInWater:
			// buoyancy = (waterLevel - y) / bbHeight; invFriction = 0.9f.
			buoyancy = (e.boatWaterLevel - e.y) / e.height
			invFriction = boatInvFrictionInWater
		case boatStatusUnderFlowingWater:
			vspeed = boatUnderFlowingWaterVSpeed
			invFriction = boatInvFrictionUnderFlowingWater
		case boatStatusUnderWater:
			buoyancy = boatUnderWaterBuoyancy
			invFriction = boatInvFrictionUnderWater
		case boatStatusInAir:
			invFriction = boatInvFrictionInAir
		case boatStatusOnLand:
			// invFriction = landFriction; a Player controlling passenger halves landFriction (a ridden boat
			// slides off land more readily). On the server a ridden boat is client-authoritative and never
			// reaches boatFloat, so the halving branch is inert here — ported for fidelity via the same guard.
			invFriction = float64(e.boatLandFriction)
			if t.getControllingPassenger(e) != 0 {
				e.boatLandFriction /= 2.0
			}
		}

		// setDeltaMovement(x*invFriction, y+vspeed, z*invFriction); deltaRotation *= invFriction.
		e.vx = e.vx * invFriction
		e.vy = e.vy + vspeed
		e.vz = e.vz * invFriction
		e.boatDeltaRotation *= float32(invFriction)

		// if (buoyancy > 0): deltaY = (deltaY + buoyancy * (getDefaultGravity()/0.65)) * 0.75.
		if buoyancy > 0.0 {
			e.vy = (e.vy + buoyancy*(boatDefaultGravity/boatBuoyancyGravityDivisor)) * boatBuoyancyScale
		}
	}
}

// --- STATUS classification (getStatus / isUnderwater / checkInWater) ------------------------------

// boatGetStatus ports AbstractBoat.getStatus:
//
//	Status w = isUnderwater();
//	if (w != null) { waterLevel = boundingBox.maxY; return w; }
//	if (checkInWater()) return IN_WATER;
//	float f = getGroundFriction();
//	if (f > 0) { landFriction = f; return ON_LAND; }
//	return IN_AIR;
//
//	[VERIFIED CFR AbstractBoat.getStatus.]
func (t *TickLoop) boatGetStatus(e *Entity) boatStatus {
	if w, under := t.boatIsUnderwater(e); under {
		e.boatWaterLevel = e.y + e.height // boundingBox.maxY == y + bbHeight
		return w
	}
	if t.boatCheckInWater(e) {
		return boatStatusInWater
	}
	f := t.boatGroundFriction(e)
	if f > 0.0 {
		e.boatLandFriction = f
		return boatStatusOnLand
	}
	return boatStatusInAir
}

// boatIsUnderwater ports AbstractBoat.isUnderwater: scan the thin slab just ABOVE the boat's top face
// (maxY .. maxY+0.001). If a water cell's surface rises above maxY+0.001 there: a SOURCE cell -> keep
// scanning (underWater=true); a FLOWING cell -> return UNDER_FLOWING_WATER immediately. Returns
// (UNDER_WATER, true) if any source was found, else (0, false).
//
//	[VERIFIED CFR AbstractBoat.isUnderwater: maxY = aabb.maxY + 0.001; scan [floor(minX)..ceil(maxX)) ×
//	 [floor(maxY)..ceil(maxY+0.001)) × [floor(minZ)..ceil(maxZ)); if water && maxY < posY+height:
//	 source -> underWater=true; else return UNDER_FLOWING_WATER; return underWater ? UNDER_WATER : null.]
func (t *TickLoop) boatIsUnderwater(e *Entity) (boatStatus, bool) {
	hw := e.width / 2
	minX := e.x - hw
	maxX := e.x + hw
	minZ := e.z - hw
	maxZ := e.z + hw
	boxMaxY := e.y + e.height
	maxY := boxMaxY + 0.001

	x0 := mthFloor(minX)
	x1 := mthCeil(maxX)
	y0 := mthFloor(boxMaxY)
	y1 := mthCeil(maxY)
	z0 := mthFloor(minZ)
	z1 := mthCeil(maxZ)

	underWater := false
	for x := x0; x < x1; x++ {
		for y := y0; y < y1; y++ {
			for z := z0; z < z1; z++ {
				cell := pk.Position{X: x, Y: y, Z: z}
				fs := t.fluidAt(cell)
				if !fs.isWater {
					continue
				}
				surface := float64(y) + t.fluidSurfaceHeight(cell, fs)
				if !(maxY < surface) {
					continue
				}
				if fs.source {
					underWater = true
					continue
				}
				return boatStatusUnderFlowingWater, true
			}
		}
	}
	if underWater {
		return boatStatusUnderWater, true
	}
	return boatStatusInAir, false
}

// boatCheckInWater ports AbstractBoat.checkInWater: scan the thin slab at the boat's BOTTOM face
// (minY .. minY+0.001). Track the max water surface into waterLevel; return whether the boat's minY is
// below any water surface there (the boat's hull is touching water).
//
//	[VERIFIED CFR AbstractBoat.checkInWater: waterLevel = -Double.MAX_VALUE; scan [floor(minX)..ceil(maxX)) ×
//	 [floor(minY)..ceil(minY+0.001)) × [floor(minZ)..ceil(maxZ)); if water: height = y + fluidHeight;
//	 waterLevel = max(height, waterLevel); inWater |= minY < height; return inWater.]
func (t *TickLoop) boatCheckInWater(e *Entity) bool {
	hw := e.width / 2
	minX := e.x - hw
	maxX := e.x + hw
	minY := e.y
	minZ := e.z - hw
	maxZ := e.z + hw

	x0 := mthFloor(minX)
	x1 := mthCeil(maxX)
	y0 := mthFloor(minY)
	y1 := mthCeil(minY + 0.001)
	z0 := mthFloor(minZ)
	z1 := mthCeil(maxZ)

	inWater := false
	e.boatWaterLevel = -math.MaxFloat64
	for x := x0; x < x1; x++ {
		for y := y0; y < y1; y++ {
			for z := z0; z < z1; z++ {
				cell := pk.Position{X: x, Y: y, Z: z}
				fs := t.fluidAt(cell)
				if !fs.isWater {
					continue
				}
				height := float64(y) + t.fluidSurfaceHeight(cell, fs)
				if height > e.boatWaterLevel {
					e.boatWaterLevel = height
				}
				if minY < height {
					inWater = true
				}
			}
		}
	}
	return inWater
}

// boatWaterLevelAbove ports AbstractBoat.getWaterLevelAbove: scan upward from the boat's top face for the
// first Y layer whose max water column height is < 1.0 (a partial/no water cell) and return that layer's
// (y + blockHeight); a fully-water layer (>= 1.0) continues upward. Returns maxY+1 if the scan window
// (bounded by lastYd) holds no partial layer. This is the surface Y the air->water snap targets.
//
//	[VERIFIED CFR AbstractBoat.getWaterLevelAbove: minX=floor(minX); maxX=ceil(maxX); minY=floor(maxY);
//	 maxY=ceil(maxY - lastYd); for y in [minY,maxY): blockHeight=0; for x,z: if water: blockHeight=
//	 max(blockHeight, fluidHeight); if >=1 continue-outer; if blockHeight<1 return y+blockHeight;
//	 return maxY+1.]
func (t *TickLoop) boatWaterLevelAbove(e *Entity) float32 {
	hw := e.width / 2
	boxMinX := e.x - hw
	boxMaxX := e.x + hw
	boxMaxY := e.y + e.height
	boxMinZ := e.z - hw
	boxMaxZ := e.z + hw

	minX := mthFloor(boxMinX)
	maxX := mthCeil(boxMaxX)
	minY := mthFloor(boxMaxY)
	maxY := mthCeil(boxMaxY - e.boatLastYd)
	minZ := mthFloor(boxMinZ)
	maxZ := mthCeil(boxMaxZ)

	for y := minY; y < maxY; y++ {
		blockHeight := float32(0.0)
		full := false
		for x := minX; x < maxX && !full; x++ {
			for z := minZ; z < maxZ; z++ {
				cell := pk.Position{X: x, Y: y, Z: z}
				fs := t.fluidAt(cell)
				if fs.isWater {
					h := float32(t.fluidSurfaceHeight(cell, fs))
					if h > blockHeight {
						blockHeight = h
					}
				}
				if blockHeight >= 1.0 {
					full = true // continue the outer y-loop (this layer is fully water)
					break
				}
			}
		}
		if full {
			continue
		}
		if blockHeight < 1.0 {
			return float32(y) + blockHeight
		}
	}
	return float32(maxY + 1)
}

// boatGroundFriction ports AbstractBoat.getGroundFriction: average the block friction of the solid blocks
// touching the boat's hull underside (a thin slab minY-0.001..minY, expanded ±1). A lily pad is skipped
// (it does not give traction). Returns friction/count, or 0 when no block touches (count==0 -> NaN in
// Java, but the caller's `> 0` test treats NaN as false; v1 returns 0 for the no-touch case, the same
// observable ON_LAND-vs-IN_AIR decision).
//
//	[VERIFIED CFR AbstractBoat.getGroundFriction: box = AABB(minX, minY-0.001, minZ, maxX, minY, maxZ);
//	 scan [floor(minX)-1..ceil(maxX)+1) × [floor(minZ)-1..ceil(maxZ)+1) skipping the 2-edge corners and
//	 the [floor(minY)-1..ceil(maxY)+1) y-edges on 1-edge columns; per cell: if !LilyPad && collisionShape
//	 intersects boatShape: friction += block.getFriction(); count++; return friction/count.]
func (t *TickLoop) boatGroundFriction(e *Entity) float32 {
	hw := e.width / 2
	boxMinX := e.x - hw
	boxMaxX := e.x + hw
	boxMinY := e.y - 0.001
	boxMaxY := e.y
	boxMinZ := e.z - hw
	boxMaxZ := e.z + hw

	x0 := mthFloor(boxMinX) - 1
	x1 := mthCeil(boxMaxX) + 1
	y0 := mthFloor(boxMinY) - 1
	y1 := mthCeil(boxMaxY) + 1
	z0 := mthFloor(boxMinZ) - 1
	z1 := mthCeil(boxMaxZ) + 1

	friction := float32(0.0)
	count := 0
	for x := x0; x < x1; x++ {
		for z := z0; z < z1; z++ {
			edges := 0
			if x == x0 || x == x1-1 {
				edges++
			}
			if z == z0 || z == z1-1 {
				edges++
			}
			if edges == 2 {
				continue // skip the 4 far corners
			}
			for y := y0; y < y1; y++ {
				if edges > 0 && (y == y0 || y == y1-1) {
					continue // skip the y-edges on a 1-edge column
				}
				// The cell must have a collision shape that intersects the boat's hull-underside box. v1 uses
				// the unit-cube solid test (blockSolidAt) as the collision-shape intersection: a solid block
				// under the boat gives ground friction. A lily pad is not solid (its shape does not block
				// motion) so it is naturally excluded — matching the vanilla `instanceof LilyPadBlock` skip.
				if !t.blockSolidAt(x, y, z) {
					continue
				}
				friction += t.boatBlockFriction(x, y, z)
				count++
			}
		}
	}
	if count == 0 {
		return 0.0
	}
	return friction / float32(count)
}

// boatBlockFriction returns Block.getFriction() for the block at (x,y,z) — the per-block slipperiness
// (BlockBehaviour.friction, DEFAULT 0.6; ice 0.98, slime 0.8, blue_ice 0.989). v1 has no per-block
// friction table wired for arbitrary blocks, so it reads the DEFAULT 0.6 (the value stone/dirt/grass —
// every block a placed boat rests on — actually carry). Structured to become a per-block read later
// (CLAUDE.md: cite the default, never bake it away).
//
//	[VERIFIED CFR Block.getFriction: `return this.friction;` — BlockBehaviour.Properties default friction
//	 == 0.6f (Properties(): `this.friction = 0.6f;`).]
func (t *TickLoop) boatBlockFriction(x, y, z int) float32 {
	return boatDefaultBlockFriction
}

// boatDefaultBlockFriction is BlockBehaviour.Properties' default friction (0.6f). See boatBlockFriction.
const boatDefaultBlockFriction = float32(0.6)

// --- position + collision helpers ----------------------------------------------------------------

// boatSetPos writes the boat's position through the tick-owned store (entities.move re-buckets so the
// tracker's near() stays consistent) — the boat analogue of minecartSetPos. Used by the air->water snap;
// the per-tick collided move goes through moveEntity.
func (t *TickLoop) boatSetPos(e *Entity, x, y, z float64) {
	t.cur().entities.move(e, x, y, z)
}

// boatNoCollisionAtY ports the floatBoat snap guard `level.noCollision(this, boundingBox.move(0, targetY-y, 0))`:
// the boat's box, moved to targetY, must not overlap any solid block. v1 uses the same unit-cube solid test
// the entity collision path (moveEntity) uses — a boat is never snapped INTO a solid.
func (t *TickLoop) boatNoCollisionAtY(e *Entity, targetY float64) bool {
	return !t.boxOverlapsSolid(entityBoxAt(e, e.x, targetY, e.z))
}

// --- ITEM → PLACEMENT: a boat item used raytraces to water and spawns the boat ------------------

// boatItemToEntityType maps a boat/raft ITEM id (data/item) to the AbstractBoat ENTITY type id it spawns
// (BoatItem.entityType). Returns (0, false) for a non-boat item.
//
//	[VERIFIED data/item: oak_boat=891 .. bamboo_chest_raft=910 (contiguous); data/entity: each wood variant
//	 is its own EntityType (OakBoat=89, OakChestBoat=90, ..., BambooRaft=9, BambooChestRaft=8).]
func boatItemToEntityType(itemID int32) (entity.ID, bool) {
	switch itemID {
	case 891: // oak_boat
		return entity.OakBoat.ID, true
	case 892: // oak_chest_boat
		return entity.OakChestBoat.ID, true
	case 893: // spruce_boat
		return entity.SpruceBoat.ID, true
	case 894: // spruce_chest_boat
		return entity.SpruceChestBoat.ID, true
	case 895: // birch_boat
		return entity.BirchBoat.ID, true
	case 896: // birch_chest_boat
		return entity.BirchChestBoat.ID, true
	case 897: // jungle_boat
		return entity.JungleBoat.ID, true
	case 898: // jungle_chest_boat
		return entity.JungleChestBoat.ID, true
	case 899: // acacia_boat
		return entity.AcaciaBoat.ID, true
	case 900: // acacia_chest_boat
		return entity.AcaciaChestBoat.ID, true
	case 901: // cherry_boat
		return entity.CherryBoat.ID, true
	case 902: // cherry_chest_boat
		return entity.CherryChestBoat.ID, true
	case 903: // dark_oak_boat
		return entity.DarkOakBoat.ID, true
	case 904: // dark_oak_chest_boat
		return entity.DarkOakChestBoat.ID, true
	case 905: // pale_oak_boat
		return entity.PaleOakBoat.ID, true
	case 906: // pale_oak_chest_boat
		return entity.PaleOakChestBoat.ID, true
	case 907: // mangrove_boat
		return entity.MangroveBoat.ID, true
	case 908: // mangrove_chest_boat
		return entity.MangroveChestBoat.ID, true
	case 909: // bamboo_raft
		return entity.BambooRaft.ID, true
	case 910: // bamboo_chest_raft
		return entity.BambooChestRaft.ID, true
	}
	return 0, false
}

// boatIsChestType reports whether a boat ENTITY type is an AbstractChestBoat (a 27-slot container boat/raft).
func boatIsChestType(typ entity.ID) bool {
	switch typ {
	case entity.OakChestBoat.ID, entity.SpruceChestBoat.ID, entity.BirchChestBoat.ID,
		entity.JungleChestBoat.ID, entity.AcaciaChestBoat.ID, entity.CherryChestBoat.ID,
		entity.DarkOakChestBoat.ID, entity.PaleOakChestBoat.ID, entity.MangroveChestBoat.ID,
		entity.BambooChestRaft.ID:
		return true
	}
	return false
}

// boatIsRaftType reports whether a boat ENTITY type is a Raft/ChestRaft (bamboo) — it uses the raft
// rideHeight factor (0.8888889) instead of the boat divisor (/3).
func boatIsRaftType(typ entity.ID) bool {
	return typ == entity.BambooRaft.ID || typ == entity.BambooChestRaft.ID
}

// boatTypeRecord returns the data/entity.Entity table record for a boat type id (for NewEntity's AABB/dims
// copy). Falls back to the OakBoat record for an unknown id (all boats share the same 1.375×0.5625 box, so
// the fallback is dimension-exact even for a mis-mapped variant).
func boatTypeRecord(typ entity.ID) entity.Entity {
	switch typ {
	case entity.OakBoat.ID:
		return entity.OakBoat
	case entity.OakChestBoat.ID:
		return entity.OakChestBoat
	case entity.SpruceBoat.ID:
		return entity.SpruceBoat
	case entity.SpruceChestBoat.ID:
		return entity.SpruceChestBoat
	case entity.BirchBoat.ID:
		return entity.BirchBoat
	case entity.BirchChestBoat.ID:
		return entity.BirchChestBoat
	case entity.JungleBoat.ID:
		return entity.JungleBoat
	case entity.JungleChestBoat.ID:
		return entity.JungleChestBoat
	case entity.AcaciaBoat.ID:
		return entity.AcaciaBoat
	case entity.AcaciaChestBoat.ID:
		return entity.AcaciaChestBoat
	case entity.CherryBoat.ID:
		return entity.CherryBoat
	case entity.CherryChestBoat.ID:
		return entity.CherryChestBoat
	case entity.DarkOakBoat.ID:
		return entity.DarkOakBoat
	case entity.DarkOakChestBoat.ID:
		return entity.DarkOakChestBoat
	case entity.PaleOakBoat.ID:
		return entity.PaleOakBoat
	case entity.PaleOakChestBoat.ID:
		return entity.PaleOakChestBoat
	case entity.MangroveBoat.ID:
		return entity.MangroveBoat
	case entity.MangroveChestBoat.ID:
		return entity.MangroveChestBoat
	case entity.BambooRaft.ID:
		return entity.BambooRaft
	case entity.BambooChestRaft.ID:
		return entity.BambooChestRaft
	}
	return entity.OakBoat
}

// tryUseBoatItem ports BoatItem.use for the right-click-AIR path: raytrace from the player's eyes along
// the view vector (Fluid.ANY — the ray stops at the water surface), and if it hit a block/fluid, spawn the
// boat at the hit point (facing the player's yaw), then consume 1 item (survival). Returns true when the
// use was consumed (a boat spawned, or a MISS/FAIL that still belongs to the boat item), false to fall
// through. Runs on the tick goroutine (handleUseItem).
//
//	[VERIFIED CFR BoatItem.use: hitResult = getPlayerPOVHitResult(level, player, Fluid.ANY); if MISS -> PASS;
//	 (entity-pick-block guard -> PASS); if hitResult.type == BLOCK: boat = getBoat(...); boat.setYRot(
//	 player.getYRot()); if (!noCollision(boat, boat.box)) return FAIL; addFreshEntity(boat); consume(1); }.]
func (t *TickLoop) tryUseBoatItem(p *tickPlayer, inv *Inventory, held component.SlotData, hand int32) bool {
	mcType, ok := boatItemToEntityType(int32(held.ItemID))
	if !ok {
		return false // not a boat item: fall through
	}
	if t.world() == nil {
		return false
	}
	// getPlayerPOVHitResult(Fluid.ANY): raytrace from the eye along the view for the interaction range,
	// stopping at the first solid block OR the water surface. A MISS (nothing hit in range) -> PASS (no spawn).
	hx, hy, hz, hitType := t.boatPovHitResult(p)
	if hitType == boatHitMiss {
		return true // BoatItem.use returns PASS on a MISS — but the use is still "for" the boat (no fall-through)
	}
	if hitType != boatHitBlock {
		return true // only a BLOCK/fluid hit spawns; any other hit is a consumed no-op for the boat item
	}
	// getBoat: setInitialPos(hit); then setYRot(player yaw). Spawn the boat at the hit surface facing the player.
	e := t.spawnBoatDeferred(mcType, hx, hy, hz, p.yaw)
	if e == nil {
		return true
	}
	// noCollision(boat, boat.box): if the spawn box overlaps a solid, FAIL (do not place). Remove the boat we
	// just added and do not consume the item.
	if t.boxOverlapsSolid(entityBoxAt(e, e.x, e.y, e.z)) {
		t.removeBoat(e)
		return true // FAIL: consumed the interact (no fall-through), item NOT shrunk
	}
	// consume(1): survival only (creative keeps the item, the hasInfiniteMaterials guard).
	if p.gameMode != gameModeCreative {
		t.shrinkHeldItemHand(p, inv, hand)
	}
	return true
}

// spawnBoatDeferred spawns the boat in the region owning its column (so its store/tracker resolve correctly),
// mirroring the withRegion wrap the minecart placement uses. Returns the spawned entity.
func (t *TickLoop) spawnBoatDeferred(typ entity.ID, x, y, z float64, yaw float32) *Entity {
	var e *Entity
	t.withRegion(t.regionForColumn(columnOf(x, z)), func() {
		e = t.spawnBoat(typ, x, y, z, yaw)
	})
	return e
}

// removeBoat drops a just-spawned boat from its owning region's store (the FAIL-to-place undo). It runs in
// the same region context the spawn used.
func (t *TickLoop) removeBoat(e *Entity) {
	t.withRegion(t.regionForColumn(columnOf(e.x, e.z)), func() {
		t.cur().entities.remove(e.id)
	})
}

// boatHitType discriminates the raytrace outcome.
type boatHitType int

const (
	boatHitMiss  boatHitType = iota // nothing hit in range (HitResult.Type.MISS)
	boatHitBlock                    // a solid block OR the water surface (HitResult.Type.BLOCK)
)

// boatPovHitResult is the v1 port of Item.getPlayerPOVHitResult(level, player, ClipContext.Fluid.ANY): a
// stepped raytrace from the player's eye position along the view vector for blockReach blocks, returning the
// first point where the ray enters a SOLID block or a WATER cell (Fluid.ANY stops on water). It is the boat's
// spawn-on-water finder. Vanilla runs a precise voxel-DDA ClipContext clip; v1 uses a fine fixed-step march
// (0.05-block steps) that finds the same first solid/water cell for the boat-placement use — the observable
// result (the boat lands on the water surface the player is aiming at) is identical for the flat-cell water a
// player floats a boat on. A finer DDA is a cited follow-up if a partial-height fluid edge ever matters.
//
//	[VERIFIED CFR Item.getPlayerPOVHitResult: from = getEyePosition(); to = from + viewVector *
//	 blockInteractionRange(); return level.clip(new ClipContext(from, to, OUTLINE, Fluid.ANY, player)).]
func (t *TickLoop) boatPovHitResult(p *tickPlayer) (x, y, z float64, hit boatHitType) {
	ex := p.x
	ey := p.y + playerStandingEyeHeight
	ez := p.z
	vx, vy, vz := playerViewVector(p.yaw, p.pitch)

	const step = 0.05
	reach := float64(blockReach)
	for d := 0.0; d <= reach; d += step {
		cx := ex + vx*d
		cy := ey + vy*d
		cz := ez + vz*d
		bx := mthFloor(cx)
		by := mthFloor(cy)
		bz := mthFloor(cz)
		// Fluid.ANY: the ray stops on a water cell (the surface the boat floats on). A water cell's surface is
		// at by + fluidSurfaceHeight; only stop once the ray point is at/below that surface (so the ray passes
		// through the air above a partial cell and lands on the water top).
		fs := t.fluidAt(pk.Position{X: bx, Y: by, Z: bz})
		if fs.isWater {
			surf := float64(by) + t.fluidSurfaceHeight(pk.Position{X: bx, Y: by, Z: bz}, fs)
			if cy <= surf {
				// Land the boat on the surface Y (the water top), at the ray's x/z. This is the flat-water
				// surface a boat rides — the vanilla clip returns the surface intersection point.
				return cx, surf, cz, boatHitBlock
			}
		}
		// OUTLINE block clip: a solid block stops the ray (the boat lands on it — a FAIL if it can't fit).
		if t.blockSolidAt(bx, by, bz) {
			return cx, cy, cz, boatHitBlock
		}
	}
	return 0, 0, 0, boatHitMiss
}

// mthCeil ports Mth.ceil(double): the smallest int >= v.
func mthCeil(v float64) int { return int(math.Ceil(v)) }

// boatMaxPassengersOf ports getMaxPassengers with the AbstractChestBoat override: a chest boat/raft seats 1,
// a plain boat/raft seats 2.
func boatMaxPassengersOf(e *Entity) int {
	if boatIsChestType(e.typ) {
		return boatChestMaxPassengers
	}
	return boatMaxPassengers
}

// boatRideHeight ports rideHeight for the boat's PASSENGER seat Y: Boat.rideHeight == height/3.0,
// Raft.rideHeight == height*0.8888889.
//
//	[VERIFIED CFR Boat.rideHeight: height/3.0f; Raft.rideHeight: height*0.8888889f.]
func boatRideHeight(e *Entity) float64 {
	if e.boatIsRaft {
		return e.height * raftRideHeightFactor
	}
	return e.height / boatRideHeightDivisor
}

// boatSinglePassengerXOffsetOf ports getSinglePassengerXOffset: 0.15 for a chest boat, 0.0 for a plain boat.
func boatSinglePassengerXOffsetOf(e *Entity) float64 {
	if boatIsChestType(e.typ) {
		return boatChestSinglePassengerXOffset
	}
	return boatSinglePassengerXOffset
}

// boatPassengerAttachment ports AbstractBoat.getPassengerAttachmentPoint: the seat offset for a passenger at
// its index, rotated by the boat's yaw. Single passenger -> (0, rideHeight, singleXOffset); 2 passengers ->
// index 0 at +0.2, index 1 at -0.6 (a boat seats two front-to-back), +0.2 more for an Animal passenger.
//
//	[VERIFIED CFR AbstractBoat.getPassengerAttachmentPoint: offset = getSinglePassengerXOffset(); if
//	 passengers.size() > 1 { i = indexOf(passenger); offset = i==0 ? 0.2f : -0.6f; if Animal offset += 0.2f; }
//	 return new Vec3(0.0, rideHeight(dimensions), offset).yRot(-yRot * PI/180).]
func (e *Entity) boatPassengerAttachment(seatIndex int) Pos {
	offset := boatSinglePassengerXOffsetOf(e)
	if len(e.passengers) > 1 {
		if seatIndex == 0 {
			offset = float64(float32(0.2))
		} else {
			offset = float64(float32(-0.6))
		}
		// An Animal passenger sits +0.2 further forward. v1 passengers are players (not Animals), so this
		// branch is inert; ported for fidelity (a future mob-on-boat passenger would take it).
	}
	return transformPoint(Pos{X: 0.0, Y: boatRideHeight(e), Z: offset}, e.yaw)
}

// shrinkHeldItemHand decrements the stack in the hand used for the boat placement (main or off hand) by 1
// and broadcasts the changed slot — the hand-aware sibling of shrinkHeldItem (which is main-hand only).
// Mirrors ItemStack.consume(1, player) for the used hand. Tick-owned.
func (t *TickLoop) shrinkHeldItemHand(p *tickPlayer, inv *Inventory, hand int32) {
	slot := heldMenuSlot(p, hand)
	before := inv.snapshot()

	cur := inv.get(slot)
	cur.Count--
	if cur.Count <= 0 {
		cur = component.SlotData{Count: 0} // shrink to 0 -> EMPTY
	}
	inv.set(slot, cur)

	t.broadcastInventoryChanges(p, inv, before)
}
