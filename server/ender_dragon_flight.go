package server

// ender_dragon_flight.go -- the EnderDragon flight AI: the phase-instance per-phase state, the phase
// manager (setPhase), the per-phase doServerTick bodies, and the aiStep flight integrator that steers the
// dragon toward the current phase getFlyTargetLocation. A 1:1 port of net.minecraft.world.entity.boss
// .enderdragon.EnderDragon.aiStep + the phases.* doServerTick methods (26.2 jar, CFR this task).
//
// All RNG draws are on the dragon OWN mobRandom stream. DragonFireball (STRAFE) + AreaEffectCloud
// (SITTING_FLAMING dragon_breath) spawns are routed through cited seam calls that no-op until those entity
// types land.

import "math"

// dragonPhaseState is the union of every phase instance's mutable fields (vanilla holds one instance per
// phase; we fold them into the dragonState since only one phase is active at a time and begin() resets the
// active phase's fields). Cite the phases.* instance fields.
type dragonPhaseState struct {
	// HOLDING_PATTERN / STRAFE / LANDING_APPROACH / TAKEOFF flight path + target.
	path      *dragonPath
	targetX   float64
	targetY   float64
	targetZ   float64
	hasTarget bool
	clockwise bool // holdingPatternClockwise
	// STRAFE_PLAYER
	fireballCharge int
	strafeTargetID int32 // attackTarget entity id (a player); 0 == none
	// SITTING_FLAMING
	flameTicks int
	flameCount int
	// SITTING_SCANNING
	scanningTime int
	// SITTING_ATTACKING
	attackingTicks int
	// CHARGING_PLAYER
	chargeTargetX   float64
	chargeTargetY   float64
	chargeTargetZ   float64
	hasChargeTgt    bool
	timeSinceCharge int
}

// dragonSetPhase ports EnderDragonPhaseManager.setPhase: no-op if already in `target`; else end() the old
// phase, switch, and begin() the new. begin()/end() reset the per-phase fields. Cite EnderDragonPhaseManager
// .setPhase + each phase begin()/end().
func (t *TickLoop) dragonSetPhase(e *Entity, target dragonPhase) {
	d := e.dragon
	if d.phase == target {
		return
	}
	// end() of the OUTGOING phase.
	switch d.phase {
	case dragonPhaseSittingFlaming:
		// DragonSittingFlamingPhase.end(): discard the flame cloud (cited seam -- no-op until AEC lands).
		t.dragonDiscardBreathCloud(e)
	}
	d.phase = target
	// begin() of the INCOMING phase.
	ph := &d.phaseState
	switch target {
	case dragonPhaseHolding:
		// DragonHoldingPatternPhase.begin(): currentPath = null; targetLocation = null.
		ph.path = nil
		ph.hasTarget = false
	case dragonPhaseStrafePlayer:
		// DragonStrafePlayerPhase.begin(): fireballCharge=0; targetLocation=null; currentPath=null; attackTarget=null.
		ph.fireballCharge = 0
		ph.hasTarget = false
		ph.path = nil
		ph.strafeTargetID = 0
	case dragonPhaseLandingApproach:
		ph.path = nil
		ph.hasTarget = false
	case dragonPhaseLanding:
		// DragonLandingPhase.begin(): targetLocation = null.
		ph.hasTarget = false
	case dragonPhaseTakeoff:
		// DragonTakeoffPhase.begin(): firstTick=true; currentPath=null; targetLocation=null.
		d.takeoffFirstTick = true
		ph.path = nil
		ph.hasTarget = false
	case dragonPhaseSittingFlaming:
		// DragonSittingFlamingPhase.begin(): flameTicks=0; ++flameCount.
		ph.flameTicks = 0
		ph.flameCount++
	case dragonPhaseSittingScanning:
		// DragonSittingScanningPhase.begin(): scanningTime = 0.
		ph.scanningTime = 0
	case dragonPhaseSittingAttack:
		// DragonSittingAttackingPhase.begin(): attackingTicks = 0.
		ph.attackingTicks = 0
	case dragonPhaseChargingPlayer:
		// DragonChargePlayerPhase.begin(): targetLocation=null; timeSinceCharge=0.
		ph.hasChargeTgt = false
		ph.timeSinceCharge = 0
	}
}

// dragonPhaseIsSitting reports whether the current phase is a sitting phase (SITTING_* or HOVERING).
// AbstractDragonSittingPhase.isSitting()==true; DragonHoverPhase.isSitting()==true. Cite the phases.
func dragonPhaseIsSitting(d *dragonState) bool {
	switch d.phase {
	case dragonPhaseSittingFlaming, dragonPhaseSittingScanning, dragonPhaseSittingAttack, dragonPhaseHovering:
		return true
	}
	return false
}

// dragonPhaseFlySpeed ports the getFlySpeed() per phase: Abstract 0.6, LANDING 1.5, CHARGING 3.0,
// HOVERING 1.0. Cite the phases getFlySpeed.
func dragonPhaseFlySpeed(d *dragonState) float32 {
	switch d.phase {
	case dragonPhaseLanding:
		return 1.5
	case dragonPhaseChargingPlayer:
		return 3.0
	case dragonPhaseHovering:
		return 1.0
	}
	return 0.6
}

// dragonPhaseTurnSpeed ports getTurnSpeed(): Abstract 0.7/dist/rotSpeed where rotSpeed = horizontalDist+1,
// dist = min(rotSpeed,40); LANDING returns dist/rotSpeed (no 0.7 factor). Cite the phases getTurnSpeed.
func dragonPhaseTurnSpeed(e *Entity) float32 {
	d := e.dragon
	rotSpeed := float32(math.Sqrt(e.vx*e.vx+e.vz*e.vz)) + 1.0
	dist := rotSpeed
	if dist > 40.0 {
		dist = 40.0
	}
	if d.phase == dragonPhaseLanding {
		return dist / rotSpeed
	}
	return 0.7 / dist / rotSpeed
}

// dragonHeadPos returns the world position of the dragon's head sub-part (the STRAFE/FLAMING aim origin).
// Reads the recomputed head box center. Cite EnderDragon.head.getX()/getY(0.5)/getZ().
func (t *TickLoop) dragonHeadPos(e *Entity) (hx, hy, hz float64) {
	for i := range e.dragon.parts {
		p := &e.dragon.parts[i]
		if p.name == "head" {
			cx := (p.minX + p.maxX) / 2.0
			cy := (p.minY + p.maxY) / 2.0
			cz := (p.minZ + p.maxZ) / 2.0
			return cx, cy, cz
		}
	}
	return e.x, e.y, e.z
}

// dragonPodiumTop is the exit-podium heightmap pos (EndPodiumFeature.getLocation(fightOrigin) is the origin
// x,z; the Y is the MOTION_BLOCKING_NO_LEAVES top). Cite EndPodiumFeature.getLocation + getHeightmapPos.
func (t *TickLoop) dragonPodiumTop(e *Entity) (px, py, pz int) {
	d := e.dragon
	ox := mthFloor(d.originX)
	oz := mthFloor(d.originZ)
	return ox, t.dragonHeightmapTop(ox, oz) - 1, oz
}

// dragonDoServerTick dispatches the current phase's doServerTick body. Cite the phases.* doServerTick +
// EnderDragon.aiStep (currentPhase.doServerTick(level)).
func (t *TickLoop) dragonDoServerTick(e *Entity) {
	switch e.dragon.phase {
	case dragonPhaseHolding:
		t.dragonHoldingTick(e)
	case dragonPhaseStrafePlayer:
		t.dragonStrafeTick(e)
	case dragonPhaseLandingApproach:
		t.dragonLandingApproachTick(e)
	case dragonPhaseLanding:
		t.dragonLandingTick(e)
	case dragonPhaseTakeoff:
		t.dragonTakeoffTick(e)
	case dragonPhaseSittingFlaming:
		t.dragonSittingFlamingTick(e)
	case dragonPhaseSittingScanning:
		t.dragonSittingScanningTick(e)
	case dragonPhaseSittingAttack:
		t.dragonSittingAttackTick(e)
	case dragonPhaseChargingPlayer:
		t.dragonChargeTick(e)
	case dragonPhaseHovering:
		t.dragonHoverTick(e)
	}
}

// dragonFlyTargetLocation returns the current phase's getFlyTargetLocation (the point aiStep steers toward),
// or ok=false when the phase has none. Cite the phases getFlyTargetLocation.
func (t *TickLoop) dragonFlyTargetLocation(e *Entity) (x, y, z float64, ok bool) {
	ph := &e.dragon.phaseState
	switch e.dragon.phase {
	case dragonPhaseHolding, dragonPhaseStrafePlayer, dragonPhaseLandingApproach, dragonPhaseLanding, dragonPhaseTakeoff:
		if ph.hasTarget {
			return ph.targetX, ph.targetY, ph.targetZ, true
		}
	case dragonPhaseChargingPlayer:
		if ph.hasChargeTgt {
			return ph.chargeTargetX, ph.chargeTargetY, ph.chargeTargetZ, true
		}
	case dragonPhaseHovering:
		if ph.hasTarget {
			return ph.targetX, ph.targetY, ph.targetZ, true
		}
	}
	return 0, 0, 0, false
}

// dragonNavigateToNextPathNode advances the path cursor by one node and sets targetLocation to
// (node.x, node.y + nextFloat()*20, node.z) (the while-loop always draws exactly ONE nextFloat since the
// candidate y = floor+rand*20 is never below floor). One RNG draw on the dragon stream. Cite
// DragonHoldingPatternPhase.navigateToNextPathNode.
func (t *TickLoop) dragonNavigateToNextPathNode(e *Entity) {
	ph := &e.dragon.phaseState
	p := ph.path
	if p != nil && !p.isDone() {
		cur := p.getNextNodePos()
		p.advance()
		xTarget := float64(cur.x)
		zTarget := float64(cur.z)
		yTarget := float64(float32(cur.y) + mobRandom(e).nextFloat()*20.0)
		for yTarget < float64(cur.y) {
			yTarget = float64(float32(cur.y) + mobRandom(e).nextFloat()*20.0)
		}
		ph.targetX, ph.targetY, ph.targetZ = xTarget, yTarget, zTarget
		ph.hasTarget = true
	}
}

// dragonHoldingFindNewTarget ports DragonHoldingPatternPhase.findNewTarget: the crystals/strafe/landing
// decision when the current path is done, then re-path around the pillar ring + navigate. Cite
// DragonHoldingPatternPhase.findNewTarget.
func (t *TickLoop) dragonHoldingFindNewTarget(e *Entity) {
	ph := &e.dragon.phaseState
	d := e.dragon
	if ph.path != nil && ph.path.isDone() {
		ex, _, ez := t.dragonPodiumTop(e)
		_ = ez
		crystals := t.dragonAliveCrystals(e)
		// nextInt(crystals+3) == 0 -> LANDING_APPROACH.
		if mobRandom(e).nextInt(crystals+3) == 0 {
			t.dragonSetPhase(e, dragonPhaseLandingApproach)
			return
		}
		// distSqr toward the podium/egg; strafe roll.
		px, py, pz, hasP := nearestPlayerAt(t, float64(ex), float64(t.dragonHeightmapTop(ex, mthFloor(d.originZ))-1), float64(mthFloor(d.originZ)), 1.0e9)
		var distSqr float64 = 64.0
		if hasP {
			ddx := float64(ex) - px
			ddy := float64(t.dragonHeightmapTop(ex, mthFloor(d.originZ))-1) - py
			ddz := float64(mthFloor(d.originZ)) - pz
			distSqr = (ddx*ddx + ddy*ddy + ddz*ddz) / 512.0
		}
		if hasP && (mobRandom(e).nextInt(int(distSqr+2.0)) == 0 || mobRandom(e).nextInt(crystals+2) == 0) {
			if id, ok := nearestPlayerIDAt(t, e.x, e.y, e.z, 1.0e9); ok {
				t.dragonStrafePlayer(e, id)
				return
			}
		}
	}
	if ph.path == nil || ph.path.isDone() {
		currentNodeIndex := t.dragonFindClosestNode(e)
		targetNodeIndex := currentNodeIndex
		if mobRandom(e).nextInt(8) == 0 {
			ph.clockwise = !ph.clockwise
			targetNodeIndex += 6
		}
		if ph.clockwise {
			targetNodeIndex++
		} else {
			targetNodeIndex--
		}
		if t.dragonAliveCrystals(e) < 0 {
			targetNodeIndex -= 12
			targetNodeIndex &= 7
			targetNodeIndex += 12
		} else {
			targetNodeIndex %= 12
			if targetNodeIndex < 0 {
				targetNodeIndex += 12
			}
		}
		ph.path = t.dragonFindPath(e, currentNodeIndex, targetNodeIndex, nil)
		if ph.path != nil {
			ph.path.advance()
		}
	}
	t.dragonNavigateToNextPathNode(e)
}

// dragonHoldingTick ports DragonHoldingPatternPhase.doServerTick: re-target when the current target is out
// of the [100, 22500] range or on a collision. Cite DragonHoldingPatternPhase.doServerTick.
func (t *TickLoop) dragonHoldingTick(e *Entity) {
	ph := &e.dragon.phaseState
	var distToTarget float64
	if ph.hasTarget {
		dx := ph.targetX - e.x
		dy := ph.targetY - e.y
		dz := ph.targetZ - e.z
		distToTarget = dx*dx + dy*dy + dz*dz
	}
	if distToTarget < 100.0 || distToTarget > 22500.0 || e.horizontalCollision || e.verticalCollision {
		t.dragonHoldingFindNewTarget(e)
	}
}

// dragonStrafePlayer ports DragonHoldingPatternPhase.strafePlayer: switch to STRAFE_PLAYER and set the
// target. Cite DragonHoldingPatternPhase.strafePlayer + DragonStrafePlayerPhase.setTarget.
func (t *TickLoop) dragonStrafePlayer(e *Entity, playerID int32) {
	t.dragonSetPhase(e, dragonPhaseStrafePlayer)
	t.dragonStrafeSetTarget(e, playerID)
}

// dragonStrafeSetTarget ports DragonStrafePlayerPhase.setTarget: path to a node above the player, then
// navigate. Cite DragonStrafePlayerPhase.setTarget.
func (t *TickLoop) dragonStrafeSetTarget(e *Entity, playerID int32) {
	ph := &e.dragon.phaseState
	ph.strafeTargetID = playerID
	p := t.playerByEntityID(playerID)
	if p == nil {
		return
	}
	currentNodeIndex := t.dragonFindClosestNode(e)
	targetNodeIndex := t.dragonFindClosestNodeAt(e, p.x, p.y, p.z)
	finalX := mthFloor(p.x)
	finalZ := mthFloor(p.z)
	xd := float64(finalX) - e.x
	zd := float64(finalZ) - e.z
	sd := math.Sqrt(xd*xd + zd*zd)
	ho := math.Min(float64(0.4)+sd/80.0-1.0, 10.0)
	finalY := mthFloor(p.y + ho)
	final := &dragonPathPoint{x: finalX, y: finalY, z: finalZ}
	ph.path = t.dragonFindPath(e, currentNodeIndex, targetNodeIndex, final)
	if ph.path != nil {
		ph.path.advance()
		t.dragonNavigateToNextPathNode(e)
	}
}

// dragonStrafeFindNewTarget ports DragonStrafePlayerPhase.findNewTarget (re-path around the ring).
func (t *TickLoop) dragonStrafeFindNewTarget(e *Entity) {
	ph := &e.dragon.phaseState
	if ph.path == nil || ph.path.isDone() {
		currentNodeIndex := t.dragonFindClosestNode(e)
		targetNodeIndex := currentNodeIndex
		if mobRandom(e).nextInt(8) == 0 {
			ph.clockwise = !ph.clockwise
			targetNodeIndex += 6
		}
		if ph.clockwise {
			targetNodeIndex++
		} else {
			targetNodeIndex--
		}
		if t.dragonAliveCrystals(e) <= 0 {
			targetNodeIndex -= 12
			targetNodeIndex &= 7
			targetNodeIndex += 12
		} else {
			targetNodeIndex %= 12
			if targetNodeIndex < 0 {
				targetNodeIndex += 12
			}
		}
		ph.path = t.dragonFindPath(e, currentNodeIndex, targetNodeIndex, nil)
		if ph.path != nil {
			ph.path.advance()
		}
	}
	t.dragonNavigateToNextPathNode(e)
}

// dragonStrafeTick ports DragonStrafePlayerPhase.doServerTick: chase the target, charge the fireball, and
// when charged + aligned fire a DragonFireball (cited seam) then return to HOLDING.
func (t *TickLoop) dragonStrafeTick(e *Entity) {
	ph := &e.dragon.phaseState
	target := t.playerByEntityID(ph.strafeTargetID)
	if target == nil {
		t.dragonSetPhase(e, dragonPhaseHolding)
		return
	}
	if ph.path != nil && ph.path.isDone() {
		xd := target.x - e.x
		zd := target.z - e.z
		dist := math.Sqrt(xd*xd + zd*zd)
		heightOffset := math.Min(float64(0.4)+dist/80.0-1.0, 10.0)
		ph.targetX, ph.targetY, ph.targetZ = target.x, target.y+heightOffset, target.z
		ph.hasTarget = true
	}
	var distToTarget float64
	if ph.hasTarget {
		dx := ph.targetX - e.x
		dy := ph.targetY - e.y
		dz := ph.targetZ - e.z
		distToTarget = dx*dx + dy*dy + dz*dz
	}
	if distToTarget < 100.0 || distToTarget > 22500.0 {
		t.dragonStrafeFindNewTarget(e)
	}
	ddx := target.x - e.x
	ddy := target.y - e.y
	ddz := target.z - e.z
	if ddx*ddx+ddy*ddy+ddz*ddz < 4096.0 {
		if t.dragonHasLineOfSightTo(e, target) {
			ph.fireballCharge++
			aimX := target.x - e.x
			aimZ := target.z - e.z
			aimLen := math.Sqrt(aimX*aimX + aimZ*aimZ)
			if aimLen > 0 {
				aimX /= aimLen
				aimZ /= aimLen
			}
			dirX := float64(mthSin(float64(e.yaw) * float64(degToRad)))
			dirZ := float64(-mthCos(float64(e.yaw) * float64(degToRad)))
			dirLen := math.Sqrt(dirX*dirX + dirZ*dirZ)
			if dirLen > 0 {
				dirX /= dirLen
				dirZ /= dirLen
			}
			dot := float32(dirX*aimX + dirZ*aimZ)
			angleDegs := float32(math.Acos(float64(dot))*57.2957763671875) + 0.5
			if ph.fireballCharge >= 5 && angleDegs >= 0.0 && angleDegs < 10.0 {
				hx, hy, hz := t.dragonHeadPos(e)
				vx, vy, vz := t.dragonViewVector(e)
				_ = vy
				startX := hx - vx*1.0
				startY := hy + 0.5
				startZ := hz - vz*1.0
				dirToTgt := [3]float64{target.x - startX, target.y - startY, target.z - startZ}
				t.dragonSpawnFireball(e, startX, startY, startZ, dirToTgt)
				ph.fireballCharge = 0
				if ph.path != nil {
					for !ph.path.isDone() {
						ph.path.advance()
					}
				}
				t.dragonSetPhase(e, dragonPhaseHolding)
			}
		} else if ph.fireballCharge > 0 {
			ph.fireballCharge--
		}
	} else if ph.fireballCharge > 0 {
		ph.fireballCharge--
	}
}

// dragonLandingApproachTick ports DragonLandingApproachPhase.doServerTick + findNewTarget.
func (t *TickLoop) dragonLandingApproachTick(e *Entity) {
	ph := &e.dragon.phaseState
	var distToTarget float64
	if ph.hasTarget {
		dx := ph.targetX - e.x
		dy := ph.targetY - e.y
		dz := ph.targetZ - e.z
		distToTarget = dx*dx + dy*dy + dz*dz
	}
	if distToTarget < 100.0 || distToTarget > 22500.0 || e.horizontalCollision || e.verticalCollision {
		if ph.path == nil || ph.path.isDone() {
			currentNodeIndex := t.dragonFindClosestNode(e)
			ex, ey, ez := t.dragonPodiumTop(e)
			var targetNodeIndex int
			if px, _, pz, ok := nearestPlayerAt(t, float64(ex), float64(ey), float64(ez), 1.0e9); ok {
				aimLen := math.Sqrt(px*px + pz*pz)
				aimx, aimz := px, pz
				if aimLen > 0 {
					aimx, aimz = px/aimLen, pz/aimLen
				}
				targetNodeIndex = t.dragonFindClosestNodeAt(e, -aimx*40.0, 105.0, -aimz*40.0)
			} else {
				targetNodeIndex = t.dragonFindClosestNodeAt(e, 40.0, float64(ey), 0.0)
			}
			final := &dragonPathPoint{x: ex, y: ey, z: ez}
			ph.path = t.dragonFindPath(e, currentNodeIndex, targetNodeIndex, final)
			if ph.path != nil {
				ph.path.advance()
			}
		}
		t.dragonNavigateToNextPathNode(e)
		if ph.path != nil && ph.path.isDone() {
			t.dragonSetPhase(e, dragonPhaseLanding)
		}
	}
}

// dragonLandingTick ports DragonLandingPhase.doServerTick.
func (t *TickLoop) dragonLandingTick(e *Entity) {
	ph := &e.dragon.phaseState
	if !ph.hasTarget {
		ex, ey, ez := t.dragonPodiumTop(e)
		ph.targetX = float64(ex) + 0.5
		ph.targetY = float64(ey)
		ph.targetZ = float64(ez) + 0.5
		ph.hasTarget = true
	}
	dx := ph.targetX - e.x
	dy := ph.targetY - e.y
	dz := ph.targetZ - e.z
	if dx*dx+dy*dy+dz*dz < 1.0 {
		ph.flameCount = 0
		t.dragonSetPhase(e, dragonPhaseSittingScanning)
	}
}

// dragonTakeoffTick ports DragonTakeoffPhase.doServerTick + findNewTarget.
func (t *TickLoop) dragonTakeoffTick(e *Entity) {
	ph := &e.dragon.phaseState
	d := e.dragon
	if d.takeoffFirstTick || ph.path == nil {
		d.takeoffFirstTick = false
		currentNodeIndex := t.dragonFindClosestNode(e)
		vx, _, vz := t.dragonHeadLookVector(e)
		targetNodeIndex := t.dragonFindClosestNodeAt(e, -vx*40.0, 105.0, -vz*40.0)
		if t.dragonAliveCrystals(e) <= 0 {
			targetNodeIndex -= 12
			targetNodeIndex &= 7
			targetNodeIndex += 12
		} else {
			targetNodeIndex %= 12
			if targetNodeIndex < 0 {
				targetNodeIndex += 12
			}
		}
		ph.path = t.dragonFindPath(e, currentNodeIndex, targetNodeIndex, nil)
		if ph.path != nil {
			ph.path.advance()
			if !ph.path.isDone() {
				cur := ph.path.getNextNodePos()
				ph.path.advance()
				yTarget := float64(float32(cur.y) + mobRandom(e).nextFloat()*20.0)
				for yTarget < float64(cur.y) {
					yTarget = float64(float32(cur.y) + mobRandom(e).nextFloat()*20.0)
				}
				ph.targetX, ph.targetY, ph.targetZ = float64(cur.x), yTarget, float64(cur.z)
				ph.hasTarget = true
			}
		}
	} else {
		ex, ey, ez := t.dragonPodiumTop(e)
		ddx := (float64(ex) + 0.5) - e.x
		ddy := float64(ey) - e.y
		ddz := (float64(ez) + 0.5) - e.z
		if ddx*ddx+ddy*ddy+ddz*ddz > 100.0 {
			t.dragonSetPhase(e, dragonPhaseHolding)
		}
	}
}

// dragonSittingFlamingTick ports DragonSittingFlamingPhase.doServerTick: at flameTick 200 transition (>=4
// flames -> TAKEOFF, else SITTING_SCANNING); at flameTick 10 spawn the dragon_breath AreaEffectCloud
// (cited seam). Cite DragonSittingFlamingPhase.doServerTick.
func (t *TickLoop) dragonSittingFlamingTick(e *Entity) {
	ph := &e.dragon.phaseState
	ph.flameTicks++
	if ph.flameTicks >= 200 {
		if ph.flameCount >= 4 {
			t.dragonSetPhase(e, dragonPhaseTakeoff)
		} else {
			t.dragonSetPhase(e, dragonPhaseSittingScanning)
		}
	} else if ph.flameTicks == 10 {
		hx, hy, hz := t.dragonHeadPos(e)
		lookX := hx - e.x
		lookZ := hz - e.z
		llen := math.Sqrt(lookX*lookX + lookZ*lookZ)
		if llen > 0 {
			lookX /= llen
			lookZ /= llen
		}
		x := hx + lookX*5.0/2.0
		z := hz + lookZ*5.0/2.0
		initialY := hy
		y := initialY
		for t.isEmptyBlockXYZ(mthFloor(x), mthFloor(y), mthFloor(z)) {
			y -= 1.0
			if y < 0.0 {
				y = initialY
				break
			}
		}
		y = float64(mthFloor(y) + 1)
		// AreaEffectCloud (radius 5, duration 200, dragon_breath, INSTANT_DAMAGE) -- cited seam.
		t.dragonSpawnBreathCloud(e, x, y, z)
	}
}

// dragonSittingScanningTick ports DragonSittingScanningPhase.doServerTick: turn toward a near player; after
// 25 ticks with a near target -> SITTING_ATTACKING; after 100 ticks idle -> TAKEOFF or CHARGING_PLAYER.
// Cite DragonSittingScanningPhase.doServerTick.
func (t *TickLoop) dragonSittingScanningTick(e *Entity) {
	ph := &e.dragon.phaseState
	ph.scanningTime++
	// scanTargeting: within 20.0 and |y-dragonY| <= 10.0.
	pid, ok := t.dragonScanNearPlayer(e, 20.0, 10.0)
	if ok {
		p := t.playerByEntityID(pid)
		if ph.scanningTime > 25 {
			t.dragonSetPhase(e, dragonPhaseSittingAttack)
		} else if p != nil {
			aimX := p.x - e.x
			aimZ := p.z - e.z
			aimLen := math.Sqrt(aimX*aimX + aimZ*aimZ)
			if aimLen > 0 {
				aimX /= aimLen
				aimZ /= aimLen
			}
			dirX := float64(mthSin(float64(e.yaw) * float64(degToRad)))
			dirZ := float64(-mthCos(float64(e.yaw) * float64(degToRad)))
			dot := float32(dirX*aimX + dirZ*aimZ)
			angle := float32(math.Acos(float64(dot))*57.2957763671875) + 0.5
			if angle < 0.0 || angle > 10.0 {
				hx, _, hz := t.dragonHeadPos(e)
				xAttackDist := p.x - hx
				zAttackDist := p.z - hz
				yRotDelta := clampF(wrapDegreesD(180.0-mthAtan2(xAttackDist, zAttackDist)*57.2957763671875-float64(e.yaw)), -100.0, 100.0)
				e.dragon.yRotA *= 0.8
				dist := float32(math.Sqrt(xAttackDist*xAttackDist+zAttackDist*zAttackDist)) + 1.0
				rotSpeed := dist
				if dist > 40.0 {
					dist = 40.0
				}
				e.dragon.yRotA += float32(yRotDelta) * (0.7 / dist / rotSpeed)
				e.yaw += e.dragon.yRotA
			}
		}
	} else if ph.scanningTime >= 100 {
		cid, cok := t.dragonScanChargePlayer(e, 150.0)
		t.dragonSetPhase(e, dragonPhaseTakeoff)
		if cok {
			cp := t.playerByEntityID(cid)
			if cp != nil {
				t.dragonSetPhase(e, dragonPhaseChargingPlayer)
				ph.chargeTargetX, ph.chargeTargetY, ph.chargeTargetZ = cp.x, cp.y, cp.z
				ph.hasChargeTgt = true
			}
		}
	}
}

// dragonSittingAttackTick ports DragonSittingAttackingPhase.doServerTick: after 40 ticks -> SITTING_FLAMING.
// Cite DragonSittingAttackingPhase.doServerTick.
func (t *TickLoop) dragonSittingAttackTick(e *Entity) {
	ph := &e.dragon.phaseState
	ph.attackingTicks++
	if ph.attackingTicks >= 40 {
		t.dragonSetPhase(e, dragonPhaseSittingFlaming)
	}
}

// dragonChargeTick ports DragonChargePlayerPhase.doServerTick. Cite DragonChargePlayerPhase.doServerTick.
func (t *TickLoop) dragonChargeTick(e *Entity) {
	ph := &e.dragon.phaseState
	if !ph.hasChargeTgt {
		t.dragonSetPhase(e, dragonPhaseHolding)
		return
	}
	if ph.timeSinceCharge > 0 {
		ph.timeSinceCharge++
		if ph.timeSinceCharge >= 10 {
			t.dragonSetPhase(e, dragonPhaseHolding)
			return
		}
	}
	dx := ph.chargeTargetX - e.x
	dy := ph.chargeTargetY - e.y
	dz := ph.chargeTargetZ - e.z
	distToTarget := dx*dx + dy*dy + dz*dz
	if distToTarget < 100.0 || distToTarget > 22500.0 || e.horizontalCollision || e.verticalCollision {
		ph.timeSinceCharge++
	}
}

// dragonHoverTick ports DragonHoverPhase.doServerTick: pin the target to the current position. Cite
// DragonHoverPhase.doServerTick.
func (t *TickLoop) dragonHoverTick(e *Entity) {
	ph := &e.dragon.phaseState
	if !ph.hasTarget {
		ph.targetX, ph.targetY, ph.targetZ = e.x, e.y, e.z
		ph.hasTarget = true
	}
}

// wrapDegreesD ports Mth.wrapDegrees(double). Cite Mth.wrapDegrees.
func wrapDegreesD(angle float64) float64 {
	a := math.Mod(angle, 360.0)
	if a >= 180.0 {
		a -= 360.0
	}
	if a < -180.0 {
		a += 360.0
	}
	return a
}

// dragonViewVector ports Entity.getViewVector(1.0) for the dragon (yaw+pitch -> unit look). Reuses the
// player view-vector formula. Cite Entity.getViewVector.
func (t *TickLoop) dragonViewVector(e *Entity) (vx, vy, vz float64) {
	return playerViewVector(e.yaw, e.pitch)
}

// dragonHeadLookVector ports EnderDragon.getHeadLookVector(1.0): in LANDING/TAKEOFF it tilts xRot toward the
// egg; in a sitting phase it uses xRot -45; else the plain view vector. Restores xRot after. Cite
// EnderDragon.getHeadLookVector.
func (t *TickLoop) dragonHeadLookVector(e *Entity) (vx, vy, vz float64) {
	d := e.dragon
	if d.phase == dragonPhaseLanding || d.phase == dragonPhaseTakeoff {
		ex, ey, ez := t.dragonPodiumTop(e)
		ddx := (float64(ex) + 0.5) - e.x
		ddy := float64(ey) - e.y
		ddz := (float64(ez) + 0.5) - e.z
		distSqr := ddx*ddx + ddy*ddy + ddz*ddz
		dist := float32(math.Sqrt(distSqr)) / 4.0
		if dist < 1.0 {
			dist = 1.0
		}
		yOffset := 6.0 / dist
		return playerViewVector(e.yaw, -yOffset*1.5*5.0)
	}
	if dragonPhaseIsSitting(d) {
		return playerViewVector(e.yaw, -45.0)
	}
	return playerViewVector(e.yaw, e.pitch)
}

// dragonHasLineOfSightTo is LivingEntity.hasLineOfSight for the dragon->player. Reuses the mob LoS raycast.
// Cite LivingEntity.hasLineOfSight (STRAFE fire gate).
func (t *TickLoop) dragonHasLineOfSightTo(e *Entity, p *tickPlayer) bool {
	return t.mobHasLineOfSight(e, p)
}

// dragonScanNearPlayer returns the nearest player within `rng` blocks whose |y - dragonY| <= yBand (the
// SITTING scanTargeting). Cite DragonSittingScanningPhase.scanTargeting.
func (t *TickLoop) dragonScanNearPlayer(e *Entity, rng, yBand float64) (id int32, ok bool) {
	best := rng * rng
	for _, p := range t.players {
		if p == nil {
			continue
		}
		if math.Abs(p.y-e.y) > yBand {
			continue
		}
		dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
		d2 := dx*dx + dy*dy + dz*dz
		if d2 <= best {
			best = d2
			id, ok = p.entityID, true
		}
	}
	return id, ok
}

// dragonScanChargePlayer returns the nearest player within `rng` (the SITTING CHARGE_TARGETING range 150).
// Cite DragonSittingScanningPhase.CHARGE_TARGETING.
func (t *TickLoop) dragonScanChargePlayer(e *Entity, rng float64) (id int32, ok bool) {
	return nearestPlayerIDAt(t, e.x, e.y, e.z, rng)
}

// --- cited seams (DragonFireball / AreaEffectCloud not yet ported) --------------------------------------

// dragonSpawnFireball is the STRAFE fireball spawn seam (DragonStrafePlayerPhase: new DragonFireball(level,
// dragon, dir.normalize()); snapTo(start); addFreshEntity). No-op until DragonFireball lands (another agent
// may be porting it). dir is the un-normalized (dx,dy,dz) toward the target. Cite DragonStrafePlayerPhase.
func (t *TickLoop) dragonSpawnFireball(e *Entity, startX, startY, startZ float64, dir [3]float64) {
	// DragonFireball entity not yet present; the phase transition (STRAFE -> HOLDING) has already run.
	_ = e
	_, _, _ = startX, startY, startZ
	_ = dir
}

// dragonSpawnBreathCloud is the SITTING_FLAMING dragon_breath AreaEffectCloud seam (radius 5, duration 200,
// INSTANT_DAMAGE, potionDurationScale 0.25). No-op until AreaEffectCloud lands. Cite DragonSittingFlamingPhase.
func (t *TickLoop) dragonSpawnBreathCloud(e *Entity, x, y, z float64) {
	_ = e
	_, _, _ = x, y, z
}

// dragonDiscardBreathCloud is the SITTING_FLAMING end() cleanup (flame.discard()). No-op until AEC lands.
func (t *TickLoop) dragonDiscardBreathCloud(e *Entity) { _ = e }

// dragonFlightStep is the flight-integration half of EnderDragon.aiStep (the SERVER branch): run the
// current phase doServerTick (re-running it once if the phase changed), then steer toward the phase's
// getFlyTargetLocation with the moveRelative + slide-friction integrator, and finally re-position the head/
// body parts so the hit-resolve boxes track the flight. Cite EnderDragon.aiStep (ServerLevel branch).
func (t *TickLoop) dragonFlightStep(e *Entity) {
	d := e.dragon
	// flightHistory.record(getY(), getYRot()).
	d.flight.record(e.y, e.yaw)

	prevPhase := d.phase
	t.dragonDoServerTick(e)
	if d.phase != prevPhase {
		// currentPhase changed -> run the NEW phase once more this tick.
		t.dragonDoServerTick(e)
	}

	tx, ty, tz, ok := t.dragonFlyTargetLocation(e)
	if ok {
		xdd := tx - e.x
		ydd := ty - e.y
		zdd := tz - e.z
		distToTarget := xdd*xdd + ydd*ydd + zdd*zdd
		maxSpeed := dragonPhaseFlySpeed(d)
		horizontalDist := math.Sqrt(xdd*xdd + zdd*zdd)
		if horizontalDist > 0.0 {
			ydd = clampF(ydd/horizontalDist, float64(-maxSpeed), float64(maxSpeed))
		}
		e.vy += ydd * 0.01
		e.yaw = wrapDegreesF(e.yaw)
		// aim = target - pos, normalized.
		aimLen := math.Sqrt(xdd*xdd + ydd*ydd + zdd*zdd)
		var aimX, aimY, aimZ float64
		if aimLen > 0 {
			aimX, aimY, aimZ = xdd/aimLen, ydd/aimLen, zdd/aimLen
		}
		// dir = (sin(yaw), vy, -cos(yaw)) normalized.
		dirX := float64(mthSin(float64(e.yaw) * float64(degToRad)))
		dirY := e.vy
		dirZ := float64(-mthCos(float64(e.yaw) * float64(degToRad)))
		dlen := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
		if dlen > 0 {
			dirX, dirY, dirZ = dirX/dlen, dirY/dlen, dirZ/dlen
		}
		dot := (float32(dirX*aimX+dirY*aimY+dirZ*aimZ) + 0.5) / 1.5
		if dot < 0.0 {
			dot = 0.0
		}
		if math.Abs(xdd) > 1.0e-5 || math.Abs(zdd) > 1.0e-5 {
			yRotD := mthClampF(wrapDegreesF(180.0-float32(mthAtan2(xdd, zdd))*57.295776-e.yaw), -50.0, 50.0)
			d.yRotA *= 0.8
			d.yRotA += yRotD * dragonPhaseTurnSpeed(e)
			e.yaw += d.yRotA * 0.1
		}
		span := float32(2.0 / (distToTarget + 1.0))
		// moveRelative(0.06f * (dot*span + (1-span)), Vec3(0,0,-1)): forward accel along -cos/sin heading.
		accel := 0.06 * (dot*span + (1.0 - span))
		t.dragonMoveRelative(e, float64(accel))
		// move(SELF, deltaMovement) (0.8x if inWall). The dragon has noPhysics; integrate the raw delta.
		scale := 1.0
		if d.inWall {
			scale = 0.8
		}
		t.regionForEntity(e).entities.move(e, e.x+e.vx*scale, e.y+e.vy*scale, e.z+e.vz*scale)
		// slide friction: delta.normalize().dot(dir); slide = 0.8 + 0.15*(dot+1)/2; delta *= (slide, 0.91, slide).
		dvLen := math.Sqrt(e.vx*e.vx + e.vy*e.vy + e.vz*e.vz)
		var nvx, nvy, nvz float64
		if dvLen > 0 {
			nvx, nvy, nvz = e.vx/dvLen, e.vy/dvLen, e.vz/dvLen
		}
		slide := 0.8 + 0.15*((nvx*dirX+nvy*dirY+nvz*dirZ)+1.0)/2.0
		e.vx *= slide
		e.vy *= 0.91
		e.vz *= slide
	}
	e.headYaw = e.yaw
	t.dragonRecomputeParts(e)
}

// dragonMoveRelative ports Entity.moveRelative(speed, Vec3(0,0,-1)) for the dragon: rotate the input by
// the body yaw and add to deltaMovement. With input (0,0,-1), the yaw-rotated vector is
// (sin(yaw)*1, 0, cos(yaw)*1) scaled by speed (Vec3.yRot). Cite Entity.moveRelative.
func (t *TickLoop) dragonMoveRelative(e *Entity, speed float64) {
	// getInputVector: scale = speed (input length 1); then yRot by yaw.
	rad := float64(e.yaw) * float64(degToRad)
	sin := math.Sin(rad)
	cos := math.Cos(rad)
	inX := 0.0
	inZ := -1.0
	// Vec3.yRot(-yawRad): x' = x*cos + z*sin; z' = z*cos - x*sin. Entity.moveRelative uses yRot(-yaw*PI/180).
	rx := inX*cos + inZ*sin
	rz := inZ*cos - inX*sin
	e.vx += rx * speed
	e.vz += rz * speed
}

// dragonPhaseOnHurt ports the per-phase onHurt(source, damage): AbstractDragonPhaseInstance.onHurt returns
// damage unchanged; AbstractDragonSittingPhase.onHurt returns 0 for an arrow/wind_charge DIRECT hit (and
// igniteForSeconds(1) the projectile -- ignite is a cited seam, no projectile ref threaded). The arrow/
// wind_charge test uses the source damage-type (arrow/trident/wind_charge are the AbstractArrow/WindCharge
// direct-entity sources). Cite AbstractDragonSittingPhase.onHurt + AbstractDragonPhaseInstance.onHurt.
func dragonPhaseOnHurt(d *dragonState, src damageSource, damage float32) float32 {
	if dragonPhaseIsSitting(d) {
		switch src.typeTag {
		case damageTypeArrow, damageTypeTrident, damageTypeWindCharge:
			// source.getDirectEntity().igniteForSeconds(1.0f) -- cited seam (no projectile ref); return 0.
			return 0.0
		}
	}
	return damage
}
