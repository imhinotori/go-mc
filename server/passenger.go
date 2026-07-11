package server

// passenger.go — the PASSENGER / VEHICLE (ride) subsystem, PORTED 1:1 from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p / CFR this task). It is the Go re-expression of
// net.minecraft.world.entity.Entity's passenger API — startRiding / stopRiding / removeVehicle /
// addPassenger / removePassenger / ejectPassengers / canAddPassenger / getControllingPassenger — plus
// the seat-offset math (positionRider / getPassengerRidingPosition / getVehicleAttachmentPoint) and the
// ClientboundSetPassengers broadcast, wired for the happy-ghast player ride.
//
// THE FOLIA RULE (as elsewhere in this codebase): a live *Entity/*tickPlayer reference NEVER crosses
// into stored state. The vehicle stores its passengers as an ordered []int32 of THIN entity ids; a
// riding player stores its vehicle's id. The v1 vehicle is a mob (*Entity), the v1 passenger is a
// player (tickPlayer) — so the ride ops here operate between an *Entity vehicle and a tickPlayer rider.
//
// PORTED classes/methods (all javap/CFR-cited):
//   - Entity.startRiding(Entity, force, sendEventAndTriggers): the guard chain (already-vehicle,
//     couldAcceptPassenger, cycle check, canRide && canAddPassenger unless force), stopRiding if already
//     a passenger, then vehicle = entityToRide; vehicle.addPassenger(this).
//   - Entity.addPassenger(Entity): append to the ImmutableList; a server-side Player is PREPENDED ahead
//     of a non-Player first passenger (so a player becomes the controlling first passenger).
//   - Entity.removePassenger(Entity): filter the passenger out; passenger.boardingCooldown = 60.
//   - Entity.removeVehicle()/stopRiding(): vehicle = null; oldVehicle.removePassenger(this).
//   - Entity.ejectPassengers(): for i = size-1..0 { passengers[i].stopRiding() }.
//   - Entity.canAddPassenger(Entity): default passengers.isEmpty(); HappyGhast override: size < 4.
//   - Entity.getControllingPassenger(): default null; HappyGhast override: firstPassenger (a Player)
//     while wearing body armor (harness) && not on still-timeout.
//   - Entity.positionRider(passenger, MoveFunction): pos = getPassengerRidingPosition(passenger);
//     off = passenger.getVehicleAttachmentPoint(this); move(pos - off).
//   - Entity.getPassengerRidingPosition: position() + getPassengerAttachmentPoint(clamped PASSENGER
//     attachment at the passenger's seat index, rotated by the vehicle's yRot).
//   - EntityAttachments.transformPoint(point, rotY): point.yRot(-rotY * PI/180).
//
// v1 STUBS (cited): (1) the harness "body armor" ITEM requirement (HappyGhast.isWearingBodyArmor reads
// the BODY equipment slot) is not yet a subsystem — v1 allows the ride via right-click on an adult happy
// ghast unconditionally and CITES this as the one permitted reduction (`happyGhastHasHarness` const-true
// stub, structured to become an equipment read). (2) Mth.sin/cos (the 65536-entry SIN table) is
// approximated by math.Sin/Cos at float32 precision — the SAME documented deviation ai_goals_rabbit.go
// takes; the seat offset is a client-authoritative RENDER position (the riding client places itself at
// the seat locally), so the tiny table-vs-libm delta is not observable. (3) ravager-riders are
// structurally supported (a Ravager could carry an illager via the same startRiding path) but INERT in
// v1 (needs a raid ravager+illager pair; no raid-spawn wires it yet).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// removePassengerBoardingCooldown is Entity.removePassenger's `passenger.boardingCooldown = 60` — the
// re-mount lockout a just-dismounted entity carries (canRide gates on boardingCooldown <= 0).
//
//	[VERIFIED CFR Entity.removePassenger: `passenger.boardingCooldown = 60;`.]
const removePassengerBoardingCooldown = 60

// entityByIDAnyRegion re-resolves an entity by its id across ALL regions (the cross-region "drop if
// gone" lookup owningRegion performs), returning the live *Entity or nil. The ride paths run either in
// the global subtick pre-phase (interact/MoveVehicle — no region registered) or on the coordinator at
// the barrier (rideTickVehicles/eject), so a region-independent by-id resolve is required (cur() would
// panic under strictRegion in the pre-phase). O(regionCount), owner-side, never mid-region-tick.
func (t *TickLoop) entityByIDAnyRegion(id int32) *Entity {
	if id == 0 {
		return nil
	}
	r := t.owningRegion(id)
	if r == nil {
		return nil
	}
	e, ok := r.entities.get(id)
	if !ok {
		return nil
	}
	return e
}

// happyGhastMaxPassengers is HappyGhast.canAddPassenger's `getPassengers().size() < 4` cap (also
// HappyGhast.MAX_PASSANGERS). The happy ghast seats up to 4 players.
//
//	[VERIFIED CFR HappyGhast.canAddPassenger: `return this.getPassengers().size() < 4;`.]
const happyGhastMaxPassengers = 4

// happyGhastHasHarness is the v1 stub for HappyGhast.isWearingBodyArmor() — vanilla reads the BODY
// equipment slot (the harness item) to gate whether the ghast can be ridden/steered. Sulfur has no
// horse-armor/harness equipment subsystem yet, so this is a cited const-true: an adult happy ghast can
// be ridden by right-click in v1. It is structured to become a real BODY-slot read (return
// !getItemBySlot(BODY).isEmpty()) once the equipment surface lands — never bake the requirement away.
//
//	[VERIFIED CFR HappyGhast.mobInteract: `if (this.isWearingBodyArmor() && !player.isSecondaryUseActive())
//	 { this.doPlayerRide(player); return SUCCESS; }`; getControllingPassenger gates on isWearingBodyArmor.]
func happyGhastHasHarness(e *Entity) bool { return true }

// happyGhastPassengerSeats are HappyGhast's 4 PASSENGER EntityAttachment points, VERBATIM from the
// EntityType.HAPPY_GHAST builder (EntityTypes.HAPPY_GHAST: passengerAttachments(new Vec3(0,4,1.7),
// new Vec3(-1.7,4,0), new Vec3(0,4,-1.7), new Vec3(1.7,4,0))). getPassengerAttachmentPoint clamps the
// seat index into [0,3] and rotates the chosen point by the vehicle's yRot (transformPoint).
//
//	[VERIFIED CFR EntityTypes.HAPPY_GHAST: .passengerAttachments(new Vec3(0.0, 4.0, 1.7),
//	 new Vec3(-1.7, 4.0, 0.0), new Vec3(0.0, 4.0, -1.7), new Vec3(1.7, 4.0, 0.0)).]
var happyGhastPassengerSeats = [4]Pos{
	{X: 0.0, Y: 4.0, Z: 1.7},
	{X: -1.7, Y: 4.0, Z: 0.0},
	{X: 0.0, Y: 4.0, Z: -1.7},
	{X: 1.7, Y: 4.0, Z: 0.0},
}

// playerVehicleAttachment is the Player's VEHICLE EntityAttachment point — Avatar.DEFAULT_VEHICLE_ATTACHMENT
// == new Vec3(0.0, 0.6, 0.0) (the STANDING_DIMENSIONS .withAttachments(attach(VEHICLE, DEFAULT))). A
// passenger's getVehicleAttachmentPoint(vehicle) returns this rotated by the passenger's OWN yRot;
// positionRider subtracts it from the seat position so the player's feet land at the seat.
//
//	[VERIFIED CFR Avatar: `public static final Vec3 DEFAULT_VEHICLE_ATTACHMENT = new Vec3(0.0, 0.6, 0.0);`
//	 STANDING_DIMENSIONS = scalable(0.6,1.8).withEyeHeight(1.62).withAttachments(builder().attach(VEHICLE,
//	 DEFAULT_VEHICLE_ATTACHMENT)).]
var playerVehicleAttachment = Pos{X: 0.0, Y: 0.6, Z: 0.0}

// transformPoint ports EntityAttachments.transformPoint(point, rotY): point.yRot(-rotY * PI/180). Vec3.yRot
// rotates around the Y axis: x' = x*cos + z*sin ; z' = z*cos - x*sin ; y' = y. Mth.cos/sin are the vanilla
// SIN-table lookups; v1 uses math.Cos/Sin at float32 precision (the ai_goals_rabbit.go convention). The
// result is a RENDER-space seat offset (client-authoritative), so the table-vs-libm delta is unobservable.
//
//	[VERIFIED CFR EntityAttachments.transformPoint: `return point.yRot(-rotY * ((float)Math.PI / 180));`
//	 Vec3.yRot(r): cos=Mth.cos(r); sin=Mth.sin(r); xx = x*cos + z*sin; zz = z*cos - x*sin; return (xx,y,zz).]
func transformPoint(p Pos, rotY float32) Pos {
	rad := float64(float32(-rotY * (math.Pi / 180.0)))
	cos := float64(float32(math.Cos(rad)))
	sin := float64(float32(math.Sin(rad)))
	return Pos{
		X: p.X*cos + p.Z*sin,
		Y: p.Y,
		Z: p.Z*cos - p.X*sin,
	}
}

// vehiclePassengerAttachment ports Entity.getPassengerAttachmentPoint -> getDefaultPassengerAttachmentPoint:
// the clamped PASSENGER attachment for the passenger's seat index, rotated by the vehicle's yRot. For a
// happy ghast the seat table is happyGhastPassengerSeats; any other vehicle falls back to the AT_HEIGHT
// default (a single seat at (0, height, 0)) — EntityAttachment.PASSENGER's Fallback.AT_HEIGHT.
//
//	[VERIFIED CFR Entity.getDefaultPassengerAttachmentPoint: idx = getPassengers().indexOf(passenger);
//	 attachments.getClamped(PASSENGER, idx, yRot). EntityAttachments.getClamped: points.get(Mth.clamp(idx,
//	 0, size-1)) then transformPoint(point, yRot). EntityAttachment.PASSENGER fallback == AT_HEIGHT
//	 (List.of(new Vec3(0, height, 0))).]
func (e *Entity) vehiclePassengerAttachment(seatIndex int) Pos {
	if e.typ == entity.HappyGhast.ID {
		idx := seatIndex
		if idx < 0 {
			idx = 0
		}
		if idx > happyGhastMaxPassengers-1 {
			idx = happyGhastMaxPassengers - 1
		}
		return transformPoint(happyGhastPassengerSeats[idx], e.yaw)
	}
	// A boat overrides getPassengerAttachmentPoint with its own (0, rideHeight, xOffset).yRot seat math
	// (boat.go: boatPassengerAttachment) — the boat rider sits low in the hull, two abreast front-to-back.
	//	[VERIFIED CFR AbstractBoat.getPassengerAttachmentPoint (boat.go boatPassengerAttachment).]
	if e.isBoat {
		return e.boatPassengerAttachment(seatIndex)
	}
	// A CAMEL overrides getPassengerAttachmentPoint with its own two-seat math (Camel
	// .getPassengerAttachmentPoint): the seat X/Z offset is +0.5 for the front (index 0) rider and -0.7 for
	// the rear (index >= 1) rider (an Animal rear passenger gets an extra +0.2, inert for player riders),
	// rotated by the camel's yRot. The Y is getBodyAnchorAnimationYOffset (the sit/stand animation lift) --
	// a client-authoritative RENDER offset (like the boat/ghast), so v1 uses the STANDING body-anchor Y
	// (0.375 above the sit-diff baseline) and CITES the sit-transition lerp as render-only deferral. CITE
	// Camel.getPassengerAttachmentPoint (seatOffset 0.5 / -0.7; the +0.2 Animal add).
	if e.typ == entity.Camel.ID {
		return e.camelPassengerAttachment(seatIndex)
	}
	// Fallback AT_HEIGHT: a single seat at (0, height, 0), rotated by yRot.
	return transformPoint(Pos{X: 0, Y: e.height, Z: 0}, e.yaw)
}

// camelPassengerAttachment ports Camel.getPassengerAttachmentPoint(passenger, dims, partialTick) for the
// two seats. The horizontal offset is +0.5 for the front rider (seat 0) and -0.7 for a rear rider (seat >=
// 1); a rear ANIMAL passenger adds +0.2 (v1 riders are players -> inert, ported for fidelity). The vertical
// offset is getBodyAnchorAnimationYOffset -- the render lift of the camel's body during the sit/stand
// animation; v1 uses the STANDING body-anchor Y (dims.height - 0.375, the isCamelSitting==false, non-
// transition case of getBodyAnchorAnimationYOffset), CITING the mid-transition lerp as a render-only
// deferral (the seat is client-authoritative, so the delta is unobservable). The whole point is rotated by
// the camel's yRot (transformPoint). CITE Camel.getPassengerAttachmentPoint + getBodyAnchorAnimationYOffset.
func (e *Entity) camelPassengerAttachment(seatIndex int) Pos {
	offset := float64(float32(0.5)) // front seat (index 0)
	if seatIndex != 0 {
		offset = float64(float32(-0.7)) // rear seat
		// An Animal rear passenger sits +0.2 further forward. v1 passengers are players (not Animals), so
		// this branch is inert; ported for fidelity (a future mob-on-camel passenger would take it).
	}
	// getBodyAnchorAnimationYOffset standing, non-transition: dims.height - bodyBaseline (0.375 adult; the
	// isBaby 0.09375 is the baby camel). height is the entity's collision height.
	baseline := float64(0.375)
	if e.isBaby() {
		baseline = float64(0.09375)
	}
	y := e.height - baseline
	return transformPoint(Pos{X: 0.0, Y: y, Z: offset}, e.yaw)
}

// passengerIndexOf returns the seat index of the given passenger id in the vehicle's passenger list, or
// -1 if it is not a passenger (mirrors getPassengers().indexOf(passenger)).
func (e *Entity) passengerIndexOf(passengerID int32) int {
	for i, id := range e.passengers {
		if id == passengerID {
			return i
		}
	}
	return -1
}

// isVehicle ports Entity.isVehicle(): !passengers.isEmpty().
//
//	[VERIFIED CFR Entity.isVehicle: `return !this.passengers.isEmpty();`.]
func (e *Entity) isVehicle() bool { return len(e.passengers) > 0 }

// canAddPassengerVehicle ports Entity.canAddPassenger with the HappyGhast override. Default:
// passengers.isEmpty() (a single-seat vehicle). HappyGhast: getPassengers().size() < 4.
//
//	[VERIFIED CFR Entity.canAddPassenger: `return this.passengers.isEmpty();`. HappyGhast.canAddPassenger:
//	 `return this.getPassengers().size() < 4;`.]
func (e *Entity) canAddPassengerVehicle() bool {
	if e.typ == entity.HappyGhast.ID {
		return len(e.passengers) < happyGhastMaxPassengers
	}
	// AbstractBoat.canAddPassenger: `getPassengers().size() < getMaxPassengers() && !isEyeInFluid(WATER)`
	// (2 seats for a plain boat/raft, 1 for a chest boat/raft; a submerged boat cannot be boarded). The
	// eye-in-water guard is approximated by the boat's UNDER_WATER/UNDER_FLOWING_WATER status (a boat whose
	// eye is in water is one the float classifier marks submerged) — a floating boat (IN_WATER/IN_AIR) is
	// boardable. CITE AbstractBoat.canAddPassenger; boat.go boatMaxPassengersOf.
	if e.isBoat {
		if e.boatStatus == boatStatusUnderWater || e.boatStatus == boatStatusUnderFlowingWater {
			return false
		}
		return len(e.passengers) < boatMaxPassengersOf(e)
	}
	// Camel.canAddPassenger: getPassengers().size() <= 2 -> a camel seats TWO riders (canAddPassenger is
	// called BEFORE the new passenger is appended, so size <= 2 admits mounting onto an empty or 1-seat
	// camel; a 2-seat camel is full). CITE Camel.canAddPassenger.
	if e.typ == entity.Camel.ID {
		return len(e.passengers) <= camelMaxPassengers
	}
	return len(e.passengers) == 0
}

// playerStartRiding is the port of Entity.startRiding(Entity entityToRide, boolean force, boolean
// sendEventAndTriggers) for the v1 case: the passenger is a PLAYER, the vehicle is a mob (*Entity). It
// mirrors the vanilla guard chain and the mutation order EXACTLY:
//
//	if entityToRide == vehicle -> false
//	if !entityToRide.couldAcceptPassenger() -> false
//	(server-side type.canSerialize() gate: v1 vehicles are always serializable mobs -> pass)
//	cycle check: walk entityToRide.vehicle chain; if it reaches this -> false (a player has no vehicle
//	  it is a vehicle-of, so the walk is bounded)
//	if !(force || (canRide(entityToRide) && entityToRide.canAddPassenger(this))) -> false
//	if isPassenger() -> stopRiding()
//	setPose(STANDING) (v1: players have no pose subsystem gating this — cited no-op)
//	vehicle = entityToRide ; vehicle.addPassenger(this)
//	(gameEvent ENTITY_MOUNT + START_RIDING_TRIGGER advancement: cited no-op — no game-event/advancement
//	 subsystem yet; the observable mount is the SetPassengers broadcast)
//
// Returns true on a successful mount. force==true skips the canRide/canAddPassenger gate (vanilla's
// forced mount). The SetPassengers broadcast is emitted by the caller (via t.broadcastSetPassengers)
// after a true return, mirroring where vanilla's addPassenger triggers the tracker's mount packet.
//
//	[VERIFIED CFR Entity.startRiding(Entity,boolean,boolean): the guard chain + `this.vehicle =
//	 entityToRide; this.vehicle.addPassenger(this);` mutation order above.]
func (t *TickLoop) playerStartRiding(p *tickPlayer, vehicle *Entity, force bool) bool {
	if vehicle == nil {
		return false
	}
	if vehicle.id == p.vehicleID {
		return false // entityToRide == this.vehicle
	}
	if !vehicle.couldAcceptPassenger() {
		return false
	}
	// Cycle check: a player never has passengers in v1, so entityToRide's vehicle chain can never reach
	// the player — but the walk is preserved for faithfulness (and to reject riding a vehicle whose
	// vehicle chain loops back, which a mob-on-mob stack could form).
	for cur := vehicle; cur.vehicle != 0; {
		nxt := t.entityByIDAnyRegion(cur.vehicle)
		if nxt == nil {
			break
		}
		if nxt.id == p.entityID {
			return false
		}
		cur = nxt
	}
	if !(force || (t.playerCanRide(p) && vehicle.canAddPassengerVehicle())) {
		return false
	}
	if p.vehicleID != 0 {
		t.playerStopRiding(p) // isPassenger() -> stopRiding()
	}
	// setPose(STANDING): players have no pose subsystem gating the mount in v1 (cited no-op).
	p.vehicleID = vehicle.id
	t.vehicleAddPassenger(vehicle, p.entityID, true) // addPassenger; passenger is a Player -> prepend
	return true
}

// playerCanRide ports Entity.canRide(Entity): `!isShiftKeyDown() && boardingCooldown <= 0`. v1 has no
// sneak-pose decode for a player (chest_open.go cites the same false stub), so isShiftKeyDown is const
// false here — the mount is gated ONLY on the boarding cooldown. Structured to read a real sneak flag
// once one is decoded.
//
//	[VERIFIED CFR Entity.canRide: `return !this.isShiftKeyDown() && this.boardingCooldown <= 0;`.]
func (t *TickLoop) playerCanRide(p *tickPlayer) bool {
	return p.boardingCooldown <= 0
}

// couldAcceptPassenger ports Entity.couldAcceptPassenger(): default true (only a few entities — e.g. a
// leashed/removed entity — return false). v1 mobs always could accept a passenger.
//
//	[VERIFIED CFR Entity.couldAcceptPassenger: `return true;`.]
func (e *Entity) couldAcceptPassenger() bool { return true }

// vehicleAddPassenger ports Entity.addPassenger(Entity) for a THIN-id passenger list. If the list is
// empty the passenger becomes the sole passenger; otherwise a server-side PLAYER is PREPENDED ahead of a
// non-Player first passenger (so a player is the controlling first passenger), else appended. isPlayer
// marks whether the passenger is a player (the v1 passenger always is; a future mob-passenger passes
// false).
//
//	[VERIFIED CFR Entity.addPassenger: if passengers.isEmpty() { passengers = ImmutableList.of(passenger) }
//	 else { newList = copy; if (!isClientSide && passenger instanceof Player && !(getFirstPassenger()
//	 instanceof Player)) newList.add(0, passenger) else newList.add(passenger); passengers = copyOf(newList) }.]
func (t *TickLoop) vehicleAddPassenger(vehicle *Entity, passengerID int32, isPlayer bool) {
	if len(vehicle.passengers) == 0 {
		vehicle.passengers = []int32{passengerID}
		return
	}
	firstIsPlayer := t.playerByEntityID(vehicle.passengers[0]) != nil
	if isPlayer && !firstIsPlayer {
		// Prepend the player ahead of a non-Player first passenger.
		vehicle.passengers = append([]int32{passengerID}, vehicle.passengers...)
		return
	}
	vehicle.passengers = append(vehicle.passengers, passengerID)
}

// playerStopRiding ports Entity.stopRiding() -> removeVehicle() for a player passenger: if riding, clear
// the player's vehicle, call the vehicle's removePassenger (which sets the player's boardingCooldown to
// 60), and broadcast the updated (shrunk) passenger list. The gameEvent ENTITY_DISMOUNT is a cited no-op
// (no game-event subsystem). A non-riding player is a no-op.
//
//	[VERIFIED CFR Entity.stopRiding -> removeVehicle: `if (vehicle != null) { oldVehicle = vehicle;
//	 vehicle = null; oldVehicle.removePassenger(this); ... gameEvent(ENTITY_DISMOUNT, ...); }`.]
func (t *TickLoop) playerStopRiding(p *tickPlayer) {
	if p.vehicleID == 0 {
		return
	}
	vehicleID := p.vehicleID
	p.vehicleID = 0
	// Re-resolve the vehicle through its OWNING region (by id; nil if gone) — this runs both in the
	// global subtick pre-phase (dismount via interact/MoveVehicle) and in a region context (eject on
	// vehicle death), so owningRegion (not cur()) is the region-correct lookup, exactly as handleInteract.
	vehicle := t.entityByIDAnyRegion(vehicleID)
	if vehicle != nil {
		t.vehicleRemovePassenger(p, vehicle)
		t.broadcastSetPassengers(vehicle)
	}
}

// vehicleRemovePassenger ports Entity.removePassenger(Entity): filter the passenger out of the list
// (empty -> nil), then `passenger.boardingCooldown = 60`.
//
//	[VERIFIED CFR Entity.removePassenger: `this.passengers = (size==1 && get(0)==passenger) ?
//	 ImmutableList.of() : passengers.stream().filter(p -> p != passenger).collect(toImmutableList());
//	 passenger.boardingCooldown = 60;`.]
func (t *TickLoop) vehicleRemovePassenger(p *tickPlayer, vehicle *Entity) {
	if len(vehicle.passengers) == 1 && vehicle.passengers[0] == p.entityID {
		vehicle.passengers = nil
	} else {
		out := vehicle.passengers[:0:0]
		for _, id := range vehicle.passengers {
			if id != p.entityID {
				out = append(out, id)
			}
		}
		vehicle.passengers = out
	}
	p.boardingCooldown = removePassengerBoardingCooldown
}

// ejectPassengers ports Entity.ejectPassengers(): dismount every passenger from LAST to FIRST (`for i =
// size-1; i >= 0; i--) passengers.get(i).stopRiding()`). Each stopRiding removes that passenger AND
// broadcasts the shrunk list (playerStopRiding). Called when the vehicle is removed/dies so no player is
// left orphaned on a despawned mob. The reverse order matches vanilla (the list mutates as we go).
//
//	[VERIFIED CFR Entity.ejectPassengers: `for (int i = passengers.size()-1; i >= 0; --i)
//	 passengers.get(i).stopRiding();`.]
func (t *TickLoop) ejectPassengers(vehicle *Entity) {
	for i := len(vehicle.passengers) - 1; i >= 0; i-- {
		id := vehicle.passengers[i]
		if p := t.playerByEntityID(id); p != nil {
			t.playerStopRiding(p)
		} else {
			// A non-player passenger (a future mob rider): drop it directly from the list. v1 never hits
			// this (players are the only passengers), but it keeps the eject total (no orphaned id).
			vehicle.passengers = append(vehicle.passengers[:i], vehicle.passengers[i+1:]...)
		}
	}
}

// getControllingPassenger ports Entity.getControllingPassenger() with the HappyGhast override, returning
// the controlling passenger's PLAYER id (0 == none). Default (a plain mob): 0 — no controlling passenger.
// HappyGhast: the FIRST passenger if it is a Player AND the ghast wears its harness (isWearingBodyArmor)
// AND is not on the still-timeout; else 0. The controlling passenger steers the hover (the ridden travel).
//
//	[VERIFIED CFR Entity.getControllingPassenger: `return null;`. HappyGhast.getControllingPassenger:
//	 `firstPassenger = getFirstPassenger(); if (isWearingBodyArmor() && !isOnStillTimeout() &&
//	 firstPassenger instanceof Player p) return p; return super.getControllingPassenger();`.]
func (t *TickLoop) getControllingPassenger(vehicle *Entity) int32 {
	// A BOAT's getControllingPassenger is the first passenger when it is a LivingEntity (a player always is)
	// — AbstractBoat.getControllingPassenger: `entity = getFirstPassenger(); return entity instanceof
	// LivingEntity ? entity : super.getControllingPassenger();`. This makes a ridden boat client-authoritative
	// (isClientAuthoritative()), so tickBoat skips the server float physics and the client drives it via
	// ServerboundMoveVehicle (handleMoveVehicle). CITE AbstractBoat.getControllingPassenger.
	if vehicle.isBoat {
		if len(vehicle.passengers) == 0 {
			return 0
		}
		first := vehicle.passengers[0]
		if t.playerByEntityID(first) != nil {
			return first
		}
		return 0
	}
	// A CAMEL's getControllingPassenger is the first passenger when it is a LivingEntity (a player always
	// is) AND the camel does not refuseToMove (a sitting / mid-transition camel cannot be steered). This
	// makes a ridden, standing camel client-authoritative (like the boat): tickPhysics skips its server
	// walk and the client drives it via ServerboundMoveVehicle (handleMoveVehicle). CITE
	// Camel.getControllingPassenger (AbstractHorse.getControllingPassenger -> firstPassenger if
	// LivingEntity) + Camel.refuseToMove gate (getRiddenInput returns ZERO while refuseToMove, so a
	// sitting camel is effectively un-steerable).
	if vehicle.typ == entity.Camel.ID {
		if len(vehicle.passengers) == 0 {
			return 0
		}
		if vehicle.camelRefuseToMove(t.gametime) {
			return 0
		}
		first := vehicle.passengers[0]
		if t.playerByEntityID(first) != nil {
			return first
		}
		return 0
	}
	// A SADDLED STRIDER's getControllingPassenger is the first passenger when it is a Player holding
	// WARPED_FUNGUS_ON_A_STICK: `if (isSaddled()) { first = getFirstPassenger(); if (first instanceof Player p
	// && p.isHolding(WARPED_FUNGUS_ON_A_STICK)) return p; } return super.getControllingPassenger();`. This makes
	// a steered strider client-authoritative (the client drives it via ServerboundMoveVehicle, like the boat/
	// ghast). Cite Strider.getControllingPassenger. NOTE: the strider/pig/horse-family type checks below MUST
	// precede the ghast-default `!= HappyGhast -> return 0` guard so these equine steer branches are reachable.
	if vehicle.typ == entity.Strider.ID {
		if !striderIsSaddled(vehicle) || len(vehicle.passengers) == 0 {
			return 0
		}
		first := vehicle.passengers[0]
		if rp := t.playerByEntityID(first); rp != nil && t.striderRiderHoldingControlItem(rp) {
			return first // Player p holding WARPED_FUNGUS_ON_A_STICK -> the controlling passenger
		}
		return 0 // super.getControllingPassenger() (Animal) -> null
	}
	// A SADDLED PIG's getControllingPassenger is the first passenger when it is a Player holding
	// CARROT_ON_A_STICK: `if (isSaddled()) { first = getFirstPassenger(); if (first instanceof Player p &&
	// p.isHolding(CARROT_ON_A_STICK)) return p; } return super.getControllingPassenger();`. This makes a
	// steered pig client-authoritative (the client drives it via ServerboundMoveVehicle, like the strider/
	// boat/ghast). MIRRORS the strider branch above exactly. An un-saddled pig (the pig oracle) has
	// pigIsSaddled == false, so this returns 0 (super) and draws ZERO extra RNG. Cite Pig.getControllingPassenger.
	if vehicle.typ == entity.Pig.ID {
		if !pigIsSaddled(vehicle) || len(vehicle.passengers) == 0 {
			return 0
		}
		first := vehicle.passengers[0]
		if rp := t.playerByEntityID(first); rp != nil && t.pigRiderHoldingControlItem(rp) {
			return first // Player p holding CARROT_ON_A_STICK -> the controlling passenger
		}
		return 0 // super.getControllingPassenger() (Animal) -> null
	}
	// An AbstractHorse's getControllingPassenger is the first passenger when it is a Player AND the horse is
	// SADDLED: `if (isSaddled()) { first = getFirstPassenger(); if (first instanceof Player p) return p; }
	// return super.getControllingPassenger();`. No held-item gate (unlike the pig/strider). A saddled horse
	// with a player first-passenger is client-authoritative (the client drives it via ServerboundMoveVehicle).
	// v1 horseIsSaddled is const-false (no saddle-slot item yet -- the cited deferral), so this returns 0
	// until the SADDLE-slot API lands; the branch is wired 1:1 so it activates the moment isSaddled reads a
	// real slot. Cite AbstractHorse.getControllingPassenger.
	if vehicle.isHorseFamily {
		if !vehicle.horseIsSaddled() || len(vehicle.passengers) == 0 {
			return 0
		}
		first := vehicle.passengers[0]
		if t.playerByEntityID(first) != nil {
			return first // Player p -> the controlling passenger
		}
		return 0 // super.getControllingPassenger() (Animal) -> null
	}
	// Any other non-ghast vehicle has no controlling passenger (Entity.getControllingPassenger default null).
	// This guard runs AFTER the boat/camel/strider/pig/horse branches so those equine steers stay reachable.
	if vehicle.typ != entity.HappyGhast.ID {
		return 0
	}
	if len(vehicle.passengers) == 0 {
		return 0
	}
	first := vehicle.passengers[0]
	// isOnStillTimeout(): staysStill() || serverStillTimeout > 0. A harnessed ghast is steerable ONLY when
	// NOT on the still timeout -- while a player stands on top (scanPlayerAboveGhast -> serverStillTimeout
	// 10) the ghast FREEZES and the ride cannot steer it. Cite HappyGhast.getControllingPassenger
	// (isWearingBodyArmor() && !isOnStillTimeout() && firstPassenger instanceof Player).
	onStillTimeout := happyGhastIsOnStillTimeout(vehicle)
	if happyGhastHasHarness(vehicle) && !onStillTimeout {
		if t.playerByEntityID(first) != nil {
			return first
		}
	}
	return 0
}

// positionRider ports Entity.positionRider(passenger, MoveFunction): for each passenger, position it at
// its seat. For a v1 PLAYER passenger the MoveFunction is setPos on the tickPlayer (and its playerEntity
// snapshot), keeping the player's SERVER position at the seat so tracking/collision stay consistent. The
// riding CLIENT renders itself at the seat locally (client-authoritative), so NO position packet is sent
// to the rider — sending one would fight the client's own attach.
//
// Math (Entity.positionRider(passenger, moveFunction)):
//
//	position = getPassengerRidingPosition(passenger) = vehicle.position() + vehiclePassengerAttachment(idx)
//	offset   = passenger.getVehicleAttachmentPoint(this) = transformPoint(playerVehicleAttachment, player.yaw)
//	moveFunction.accept(passenger, position - offset)
//
//	[VERIFIED CFR Entity.positionRider(passenger, moveFunction): `Vec3 pos = getPassengerRidingPosition(
//	 passenger); Vec3 off = passenger.getVehicleAttachmentPoint(this); moveFunction.accept(passenger,
//	 pos.x-off.x, pos.y-off.y, pos.z-off.z);` getPassengerRidingPosition: `position().add(
//	 getPassengerAttachmentPoint(passenger, dimensions, 1.0f));`.]
func (t *TickLoop) positionRider(vehicle *Entity) {
	for i, id := range vehicle.passengers {
		p := t.playerByEntityID(id)
		if p == nil {
			continue // a non-player passenger has no tickPlayer position to drive in v1
		}
		seat := vehicle.vehiclePassengerAttachment(i)
		px := vehicle.x + seat.X
		py := vehicle.y + seat.Y
		pz := vehicle.z + seat.Z
		off := transformPoint(playerVehicleAttachment, p.yaw)
		p.x = px - off.X
		p.y = py - off.Y
		p.z = pz - off.Z
		// Keep the player's tracked instance in sync so the tracker broadcasts the ridden position and
		// its onGround/fall state does not fire while airborne on the vehicle.
		if p.playerEntity != nil {
			p.playerEntity.x = p.x
			p.playerEntity.y = p.y
			p.playerEntity.z = p.z
		}
		// A riding player is never "falling" — reset fall accumulation so it takes no fall damage while
		// airborne on the ghast (vanilla resetFallDistance for a passenger; the ghast has no gravity).
		p.fallDistance = 0
		p.wasOnGround = p.onGround
		p.lastY = p.y
	}
}

// rideTickVehicles is the per-tick ride driver, the Sulfur analogue of Entity.rideTick's
// `getVehicle().positionRider(this)` fanned across the store: every vehicle re-positions its passengers
// AFTER physics moves the vehicle, so a passenger tracks its moving mount. It also decrements the
// vehicle's and each riding player's boardingCooldown (Entity.baseTick: `if (boardingCooldown > 0)
// --boardingCooldown`). Runs on the tick goroutine over the live store; a store with no vehicles is a
// cheap no-op (the pig oracle has none, so its stream is unperturbed).
//
//	[VERIFIED CFR Entity.rideTick: `getVehicle().positionRider(this)`; Entity.baseTick decrements
//	 boardingCooldown while > 0.]
func (t *TickLoop) rideTickVehicles() {
	// Decrement each riding player's boarding cooldown (the dismount lockout). Global player scan.
	for _, p := range t.players {
		if p != nil && p.boardingCooldown > 0 {
			p.boardingCooldown--
		}
	}
	// Vehicles live across ALL regions and this runs on the COORDINATOR (quiescent — every region
	// joined at the barrier), so iterate EACH region's store with that region registered (withRegion),
	// exactly like tickEntityMovement — otherwise a vehicle outside region 0 would never re-position
	// its passengers. positionRider / playerByEntityID touch only the global player list + this
	// vehicle's own fields, so the per-region registration is for correctness parity, not a store read.
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := r.entities.all()
		t.withRegion(r, func() {
			for _, e := range snapshot {
				if e == nil {
					continue
				}
				if e.boardingCooldown > 0 {
					e.boardingCooldown--
				}
				if len(e.passengers) > 0 {
					t.positionRider(e)
				}
			}
		})
	}
}

// broadcastSetPassengers builds ClientboundSetPassengers for the vehicle and sends it to every player
// tracking the vehicle (sendToTrackingPlayers) AND to each riding player (so the passenger's own client
// attaches itself to the vehicle). Vanilla sends the packet on any passenger-list change (mount/dismount)
// via the tracker; here we broadcast it explicitly at the mutation site.
//
//	[VERIFIED CFR ClientboundSetPassengersPacket(Entity): vehicle = entity.getId(); passengers[] =
//	 entity.getPassengers().stream().mapToInt(Entity::getId); ServerEntity broadcasts it on a passenger
//	 change via broadcast(this, packet) (sendToTrackingPlayers + the passenger self).]
func (t *TickLoop) broadcastSetPassengers(vehicle *Entity) {
	pkt := encodeSetPassengers(vehicle.id, vehicle.passengers)
	// Trackers of the vehicle (players that can SEE it) — the actor is the vehicle (a mob, no self).
	t.broadcastToTrackers(vehicle.id, pkt)
	// Each riding player MUST also receive it so its own client mounts (a rider may not "track" the mob
	// it rides — its own tracked set excludes... nothing, but a self-mount packet is required regardless).
	for _, id := range vehicle.passengers {
		if p := t.playerByEntityID(id); p != nil && p.client != nil {
			p.client.Send(pkt)
		}
	}
}

// --- HAPPY-GHAST PLAYER RIDE: the mobInteract mount ------------------------------------------------
//
// tryHappyGhastRide ports HappyGhast.mobInteract's ride branch for the v1 right-click mount. Vanilla:
//
//	if (isBaby()) return super.mobInteract(...);         // a ghastling cannot be ridden
//	(item-use branch: a held item that interactsLivingEntity consumes the action first — v1 skips; the
//	 feed/harness item surface is not wired, so a held item falls through to the ride check)
//	if (isWearingBodyArmor() && !player.isSecondaryUseActive()) { doPlayerRide(player); return SUCCESS; }
//	return super.mobInteract(...);
//
// doPlayerRide == `if (!isClientSide) player.startRiding(this)`. v1 stubs isWearingBodyArmor to true
// (happyGhastHasHarness — the harness-item requirement is the cited reduction). usingSecondaryAction is
// the ServerboundInteract wire boolean (the real sneak-at-interact flag, decoded by the caller). Returns
// true when the interact was consumed by the ride (a successful startRiding OR the harnessed-but-secondary
// no-op that still belongs to the ghast), false to fall through to the feed path (a baby ghast).
//
//	[VERIFIED CFR HappyGhast.mobInteract: baby -> super; item interact branch; `if (isWearingBodyArmor()
//	 && !player.isSecondaryUseActive()) { doPlayerRide(player); return SUCCESS; }`; doPlayerRide ->
//	 `if (!level().isClientSide()) player.startRiding(this);`.]
func (t *TickLoop) tryHappyGhastRide(p *tickPlayer, ghast *Entity, usingSecondaryAction bool) bool {
	if ghast.isBaby() {
		return false // a ghastling is not rideable -> fall through to super.mobInteract (feed)
	}
	if !happyGhastHasHarness(ghast) {
		return false // no harness -> not rideable in v1 (fall through)
	}
	if usingSecondaryAction {
		// isSecondaryUseActive(): a shift-right-click does NOT mount (vanilla returns super.mobInteract).
		// v1 has no other harness/shift behavior, so this falls through to the feed path.
		return false
	}
	// doPlayerRide: player.startRiding(this). On a successful mount, broadcast the passenger list so the
	// rider's client attaches and every tracker renders the seated player.
	if t.playerStartRiding(p, ghast, false) {
		t.broadcastSetPassengers(ghast)
	}
	return true // the interact belongs to the ghast (SUCCESS), whether or not the mount took
}

// --- PLAYER INPUT: the movement bitfield + the camel dash trigger ----------------------------------
//
// handlePlayerInput ports ServerGamePacketListenerImpl.handlePlayerInput: decode the single-byte Input
// bitfield (net.minecraft.world.entity.player.Input) and store it as the player's lastInput{Forward,...,
// Jump} flags (the vanilla setLastClientInput). The controlling-passenger steer reads these AS the
// controller's xxa/zza/isJumping. The v1 load-bearing consumer is the CAMEL DASH: while the player controls
// a STANDING camel, the RISING EDGE of the jump key (jump now, not-jumping last tick) arms the dash via
// camelOnPlayerJump -- the server mirror of LocalPlayer.aiStep -> jumpableVehicle.handleStartJump (a full
// charge, scale 1.0 -> handleStartJump(90)). The camel then launches on its next grounded tick
// (camelAiStep -> executeRidersJump). A malformed/short payload is a silent no-op.
//
//	[VERIFIED javap Input flag layout: FLAG_FORWARD 1, FLAG_BACKWARD 2, FLAG_LEFT 4, FLAG_RIGHT 8,
//	 FLAG_JUMP 16, FLAG_SHIFT 32, FLAG_SPRINT 64. Camel.handleStartJump/onPlayerJump: a rider jump on a
//	 saddled, off-cooldown, grounded camel arms the dash launch.]
func (t *TickLoop) handlePlayerInput(p *tickPlayer, pkt pk.Packet) {
	var flags pk.UnsignedByte
	if err := pkt.Scan(&flags); err != nil {
		return // malformed/short: no mutation
	}
	const (
		inputFlagForward  = 1
		inputFlagBackward = 2
		inputFlagLeft     = 4
		inputFlagRight    = 8
		inputFlagJump     = 16
		inputFlagShift    = 32
		inputFlagSprint   = 64
	)
	prevJump := p.lastInputJump
	p.lastInputForward = flags&inputFlagForward != 0
	p.lastInputBackward = flags&inputFlagBackward != 0
	p.lastInputLeft = flags&inputFlagLeft != 0
	p.lastInputRight = flags&inputFlagRight != 0
	p.lastInputJump = flags&inputFlagJump != 0
	_ = inputFlagShift
	_ = inputFlagSprint
	// CAMEL DASH: on the rising edge of the jump key, if the player is the controlling passenger of a camel,
	// arm the dash. LocalPlayer sends a full-charge jump (scale 1.0 == handleStartJump(90)); the server
	// mirror passes charge 90 to camelOnPlayerJump -> getPlayerJumpPendingScale(90) == 1.0f.
	if p.lastInputJump && !prevJump && p.vehicleID != 0 {
		vehicle := t.entityByIDAnyRegion(p.vehicleID)
		if vehicle != nil && vehicle.typ == entity.Camel.ID && t.getControllingPassenger(vehicle) == p.entityID {
			vehicle.camelOnPlayerJump(90) // full charge -> pending scale 1.0
		}
	}
}

// --- CAMEL RIDE: the mobInteract mount -------------------------------------------------------------
//
// tryCamelRide ports Camel.mobInteract's ride branch (Camel.mobInteract, the doPlayerRide fork). Vanilla,
// after the secondary-use inventory branch + the held-item interactLivingEntity + the isFood feed branch:
//
//	if (getPassengers().size() < 2 && !isBaby()) doPlayerRide(player);
//	... return CONSUME;
//
// doPlayerRide (AbstractHorse.doPlayerRide) == `if (!isClientSide) { player.startRiding(this) ... }`. So an
// adult camel with fewer than 2 riders mounts the player as a passenger (up to 2 total). tryCamelRide
// returns true when the interact belongs to the camel (a mount attempt on an adult non-full camel), false
// for a baby / full camel (fall through to the feed/other path). Camel-gated so it is a zero-cost no-op for
// every other mob; no RNG draw (the pig oracle stream is unperturbed). CITE Camel.mobInteract (doPlayerRide
// fork) + AbstractHorse.doPlayerRide.
func (t *TickLoop) tryCamelRide(p *tickPlayer, camel *Entity) bool {
	if camel.isBaby() {
		return false // a baby camel is not rideable -> fall through
	}
	if len(camel.passengers) >= camelMaxPassengers {
		return false // full (2 riders) -> fall through
	}
	// doPlayerRide: player.startRiding(this). On a successful mount, broadcast the passenger list so the
	// rider's client attaches and every tracker renders the seated player.
	if t.playerStartRiding(p, camel, false) {
		t.broadcastSetPassengers(camel)
	}
	return true // the interact belongs to the camel (CONSUME), whether or not the mount took
}

// --- SERVERBOUND MOVE VEHICLE: the controlling-passenger steer -------------------------------------
//
// handleMoveVehicle ports ServerGamePacketListenerImpl.handleMoveVehicle for the v1 client-authoritative
// vehicle steer. When the sender is the controlling passenger of its root vehicle, the client sends the
// vehicle's new absolute position + rotation (ServerboundMoveVehiclePacket: Vec3 position, Float yRot,
// Float xRot, Boolean onGround). The server applies it to the vehicle — this IS the steer: the
// controlling passenger drives the ghast's hover by moving locally and reporting the new position. The
// server then re-positions all passengers (positionRider) so they track the moved vehicle.
//
// v1 REDUCTION (cited): vanilla runs an anti-cheat "moved too quickly" clamp (movedDist - expectedDist >
// 100 -> reject + resend) and per-axis collision via vehicle.absMoveTo + move. v1 accepts the client
// position VERBATIM (the SAME client-authoritative model the player-movement path takes in subtick.go —
// "accept the client's submitted position; LivingEntity.travel runs client-side"), with only a NaN/finite
// guard. The anti-cheat clamp is a hardening pass (T-6-06 sibling), not a gameplay behavior, and is cited
// as a follow-up. The observable ride (steer the ghast by looking + moving) is faithful.
//
//	[VERIFIED CFR ServerGamePacketListenerImpl.handleMoveVehicle: `Entity vehicle = player.getRootVehicle();
//	 if (vehicle != player && vehicle.getControllingPassenger() == player && vehicle == lastVehicle) {
//	 ... targetX/Y/Z = clamp(packet.position()); vehicle.absMoveTo(x,y,z,yRot,xRot); ... }`.
//	 ServerboundMoveVehiclePacket record: (Vec3 position, float yRot, float xRot, boolean onGround).]
func (t *TickLoop) handleMoveVehicle(p *tickPlayer, pkt pk.Packet) {
	if p.vehicleID == 0 {
		return // not riding: nothing to steer
	}
	var x, y, z pk.Double
	var yRot, xRot pk.Float
	var onGround pk.Boolean
	if err := pkt.Scan(&x, &y, &z, &yRot, &xRot, &onGround); err != nil {
		return // malformed/short: no mutation
	}
	// NaN/finite guard (vanilla containsInvalidValues): reject a non-finite claim outright.
	fx, fy, fz := float64(x), float64(y), float64(z)
	if math.IsNaN(fx) || math.IsNaN(fy) || math.IsNaN(fz) ||
		math.IsInf(fx, 0) || math.IsInf(fy, 0) || math.IsInf(fz, 0) {
		return
	}
	vehicle := t.entityByIDAnyRegion(p.vehicleID)
	if vehicle == nil {
		return // the vehicle despawned: drop
	}
	// getControllingPassenger() == player: ONLY the controlling passenger may steer (a back-seat
	// passenger's MoveVehicle is ignored). This is the anti-spoof gate (a non-controller cannot drive).
	if t.getControllingPassenger(vehicle) != p.entityID {
		return
	}
	// Accept the client-authoritative vehicle position + rotation (the v1 client-authoritative model).
	vehicle.x, vehicle.y, vehicle.z = fx, fy, fz
	vehicle.yaw = float32(yRot)
	vehicle.pitch = float32(xRot)
	vehicle.onGround = bool(onGround)
	// A steered vehicle's server-side AI must not fight the client — the client owns the motion. Zero the
	// vehicle's velocity so tickPhysics' drift does not add to the client's authoritative position.
	vehicle.vx, vehicle.vy, vehicle.vz = 0, 0, 0
	// Re-position passengers so they (and their trackers) follow the steered vehicle this tick.
	t.positionRider(vehicle)
}
