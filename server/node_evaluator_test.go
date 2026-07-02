package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// node_evaluator_test.go — the NodeEvaluator path-malus port: PathType classification, the jar malus
// table, the per-mob malus overrides, and the observable path-cost effects (avoid-water routing,
// impassable lava, canFloat swim). All PURE over a hand-built pathRegion snapshot (no world, no
// *TickLoop) — the same purity contract pathfinder_test.go asserts. Verified against the unobfuscated
// 26.2 jar (net.minecraft.world.level.pathfinder.PathType / WalkNodeEvaluator / Mob.getPathfindingMalus).

// TestPathTypeMalusTable asserts the jar PathType static-ctor malus table (VERIFIED CFR PathType.java):
// the exact per-type default costs. A negative malus is impassable; WATER 8, FIRE 16 are the hazard costs.
func TestPathTypeMalusTable(t *testing.T) {
	cases := []struct {
		p    pathType
		want float32
	}{
		{pathBlocked, -1.0},
		{pathOpen, 0.0},
		{pathWalkable, 0.0},
		{pathWalkableDoor, 0.0},
		{pathTrapdoor, 0.0},
		{pathPowderSnow, -1.0},
		{pathOnTopOfPowderSnow, 0.0},
		{pathFence, -1.0},
		{pathLava, -1.0},
		{pathWater, 8.0},
		{pathWaterBorder, 8.0},
		{pathRail, 0.0},
		{pathUnpassableRail, -1.0},
		{pathFireInNeighbor, 8.0},
		{pathFire, 16.0},
		{pathDamagingInNeighbor, 8.0},
		{pathDamaging, -1.0},
		{pathDoorOpen, 0.0},
		{pathDoorWoodClosed, -1.0},
		{pathDoorIronClosed, -1.0},
		{pathBreach, 4.0},
		{pathLeaves, -1.0},
		{pathStickyHoney, 8.0},
		{pathCocoa, 0.0},
		{pathDamageCautious, 0.0},
		{pathOnTopOfTrapdoor, 0.0},
		{pathBigMobsCloseToDanger, 4.0},
	}
	for _, c := range cases {
		if got := c.p.malus(); got != c.want {
			t.Errorf("pathType %d malus = %v, want %v", int(c.p), got, c.want)
		}
	}
}

// TestMobMalusOverride asserts Mob.getPathfindingMalus semantics: an unset type returns the PathType
// default; a set override wins. The Animal FIRE_IN_NEIGHBOR 16 / FIRE -1 overrides differ from the
// PathType defaults (8 / 16), so an Animal is more fire-averse and treats a fire node as impassable.
func TestMobMalusOverride(t *testing.T) {
	var m mobMalus
	// Unset: pure defaults.
	if got := m.getPathfindingMalus(pathWater); got != 8.0 {
		t.Errorf("unset WATER malus = %v, want default 8.0", got)
	}
	if got := m.getPathfindingMalus(pathFire); got != 16.0 {
		t.Errorf("unset FIRE malus = %v, want default 16.0", got)
	}
	// Apply the Animal overrides.
	m.setPathfindingMalus(pathFireInNeighbor, 16.0)
	m.setPathfindingMalus(pathFire, -1.0)
	if got := m.getPathfindingMalus(pathFire); got != -1.0 {
		t.Errorf("Animal FIRE override = %v, want -1.0 (impassable)", got)
	}
	if got := m.getPathfindingMalus(pathFireInNeighbor); got != 16.0 {
		t.Errorf("Animal FIRE_IN_NEIGHBOR override = %v, want 16.0", got)
	}
	// A non-overridden type still returns its default.
	if got := m.getPathfindingMalus(pathWater); got != 8.0 {
		t.Errorf("WATER (not overridden) = %v, want 8.0", got)
	}
	// copy() is an independent snapshot: mutating the source after a copy does not change the copy.
	snap := m.copy()
	m.setPathfindingMalus(pathWater, 0.0)
	if got := snap.getPathfindingMalus(pathWater); got != 8.0 {
		t.Errorf("copy WATER = %v, want the frozen 8.0 (the copy must not alias the live map)", got)
	}
}

// TestApplyAnimalMalus asserts applyAnimalPathfindingMalus stamps exactly the Animal.<init> overrides.
func TestApplyAnimalMalus(t *testing.T) {
	m := &mobAI{}
	applyAnimalPathfindingMalus(m)
	if got := m.malus.getPathfindingMalus(pathFire); got != -1.0 {
		t.Errorf("Animal FIRE = %v, want -1.0", got)
	}
	if got := m.malus.getPathfindingMalus(pathFireInNeighbor); got != 16.0 {
		t.Errorf("Animal FIRE_IN_NEIGHBOR = %v, want 16.0", got)
	}
	// The pig IS an animal; a hostile (zombie) is not.
	if !isAnimalType(entity.Pig.ID) {
		t.Error("pig must be an Animal type")
	}
	if isAnimalType(entity.Zombie.ID) {
		t.Error("zombie must NOT be an Animal type")
	}
}

// TestGetPathTypeClassification asserts getPathType over a snapshot: WALKABLE (floor below, clear
// body), OPEN (no floor), BLOCKED (body cell solid), WATER (feet in water), LAVA (feet in lava).
func TestGetPathTypeClassification(t *testing.T) {
	const floorY = 64
	r := newTestRegion(-2, floorY, -2, 6, floorY+4, 6, floorY)

	// WALKABLE: (0, floorY+1, 0) has the solid floor at floorY below and clear air above.
	if got := getPathType(r, 0, floorY+1, 0, 0.9); got != pathWalkable {
		t.Errorf("floor cell = %d, want WALKABLE", int(got))
	}
	// OPEN: a cell high in the air with nothing solid below within the body has no floor.
	if got := getPathType(r, 0, floorY+3, 0, 0.9); got != pathOpen {
		t.Errorf("air cell = %d, want OPEN", int(got))
	}
	// BLOCKED: the feet cell itself is inside the solid floor.
	if got := getPathType(r, 0, floorY, 0, 0.9); got != pathBlocked {
		t.Errorf("solid feet cell = %d, want BLOCKED", int(got))
	}
	// WATER: put water at a feet cell with a floor below.
	r.setFluid(3, floorY+1, 3, pathFluidWater)
	if got := getPathType(r, 3, floorY+1, 3, 0.9); got != pathWater {
		t.Errorf("water feet cell = %d, want WATER", int(got))
	}
	// LAVA: put lava at a feet cell — LAVA takes precedence and is classified even over solidity.
	r.setFluid(4, floorY+1, 4, pathFluidLava)
	if got := getPathType(r, 4, floorY+1, 4, 0.9); got != pathLava {
		t.Errorf("lava feet cell = %d, want LAVA", int(got))
	}
}

// TestAvoidWaterPathCost: a pool of water between A and B forces a mob that AVOIDS water (a higher
// WATER malus that makes crossing costly) to route AROUND it, while a default mob may cross. The
// avoidance is expressed via the per-mob malus threaded into the request.
func TestAvoidWaterPathCost(t *testing.T) {
	const floorY = 64
	// A wide flat floor with a 1-wide water strip at x==4 spanning z in [-1,1], blocking the direct line.
	r := newTestRegion(-2, floorY, -4, 12, floorY+4, 4, floorY)
	for z := -1; z <= 1; z++ {
		r.setFluid(4, floorY+1, z, pathFluidWater)
	}

	// A water-avoider: WATER malus cranked up so the A* strongly prefers a dry detour. (Vanilla's
	// water avoidance for the stroll goal is the GoalUtils.isWater candidate reject, but a higher WATER
	// malus is the pathfinding-cost analogue that reroutes the A* — the observable "avoids water".)
	var avoider mobMalus
	avoider.setPathfindingMalus(pathWater, 64.0)
	reqAvoid := pathRequest{
		startX: 0, startY: floorY + 1, startZ: 0,
		targetX: 8, targetY: floorY + 1, targetZ: 0,
		region: r, mobW: 0.9, mobH: 0.9,
		followRange: 24, reachRange: 1, maxVisited: 4096,
		malus: avoider,
	}
	pAvoid := computePath(reqAvoid)
	if pAvoid == nil {
		t.Fatal("avoider got no path")
	}
	// The avoider's path must not step onto the water strip at x==4, z in [-1,1] (it detours around).
	for _, nd := range pAvoid.nodes {
		if nd.x == 4 && nd.y == floorY+1 && nd.z >= -1 && nd.z <= 1 {
			t.Fatalf("water-avoider path stepped onto water at (%d,%d,%d)", nd.x, nd.y, nd.z)
		}
	}
	// The path still reaches the target.
	last := pAvoid.nodes[len(pAvoid.nodes)-1]
	if abs(last.x-8) > 1 || abs(last.z-0) > 1 {
		t.Fatalf("avoider path did not reach target (8,0); ended (%d,%d)", last.x, last.z)
	}
}

// TestLavaImpassable: a lava cell (malus -1) is NEVER accepted as a path node — a mob routes around
// it. A full lava wall blocking the direct line forces a detour, never a step into lava.
func TestLavaImpassable(t *testing.T) {
	const floorY = 64
	r := newTestRegion(-2, floorY, -4, 12, floorY+4, 4, floorY)
	// A lava wall at x==4 for all z in the corridor except a gap at z==3 (the only way around).
	for z := -3; z <= 2; z++ {
		r.setFluid(4, floorY+1, z, pathFluidLava)
	}
	req := pathRequest{
		startX: 0, startY: floorY + 1, startZ: 0,
		targetX: 8, targetY: floorY + 1, targetZ: 0,
		region: r, mobW: 0.9, mobH: 0.9,
		followRange: 24, reachRange: 1, maxVisited: 8192,
	}
	p := computePath(req)
	if p == nil {
		t.Fatal("expected a path around the lava, got nil")
	}
	for _, nd := range p.nodes {
		if r.fluidAt(nd.x, nd.y, nd.z) == pathFluidLava {
			t.Fatalf("path stepped onto lava at (%d,%d,%d)", nd.x, nd.y, nd.z)
		}
	}
}

// TestCanFloatSwim: with canFloat set, WATER is a standable surface node, so a mob paths ACROSS a
// water body. Without canFloat and with a WATER-avoiding malus, the mob would route around instead.
func TestCanFloatSwim(t *testing.T) {
	const floorY = 64
	// A narrow corridor (z fixed at 0) with water at x in [3,5] — the ONLY way to the target is through
	// the water. Walls at z==-1 and z==1 keep the mob in the corridor.
	r := newTestRegion(-2, floorY, -1, 10, floorY+4, 1, floorY)
	for x := 3; x <= 5; x++ {
		r.setFluid(x, floorY+1, 0, pathFluidWater)
	}
	// Wall the corridor so there is no dry detour AND no climb-over: solidify BOTH body cells (y+1 and
	// y+2) at z==-1 and z==1 across the water span (a mob cannot stand there — BLOCKED — and cannot jump
	// onto the wall top because the cell above is solid too).
	for x := 3; x <= 5; x++ {
		r.setSolid(x, floorY+1, -1)
		r.setSolid(x, floorY+2, -1)
		r.setSolid(x, floorY+1, 1)
		r.setSolid(x, floorY+2, 1)
	}

	// A floating mob (canFloat): WATER is standable, so it swims straight across.
	reqFloat := pathRequest{
		startX: 0, startY: floorY + 1, startZ: 0,
		targetX: 8, targetY: floorY + 1, targetZ: 0,
		region: r, mobW: 0.9, mobH: 0.9,
		followRange: 24, reachRange: 1, maxVisited: 4096,
		canFloat: true,
	}
	pFloat := computePath(reqFloat)
	if pFloat == nil {
		t.Fatal("canFloat mob got no path across water")
	}
	last := pFloat.nodes[len(pFloat.nodes)-1]
	if abs(last.x-8) > 1 || abs(last.z-0) > 1 {
		t.Fatalf("canFloat path did not reach target across water; ended (%d,%d)", last.x, last.z)
	}
	// The path must actually traverse a water cell (it swam through, not around — there is no around).
	swam := false
	for _, nd := range pFloat.nodes {
		if r.fluidAt(nd.x, nd.y, nd.z) == pathFluidWater {
			swam = true
			break
		}
	}
	if !swam {
		t.Fatal("canFloat path did not traverse any water cell (expected a swim through the corridor)")
	}
}

// TestTurtleAmphibiousMalus asserts applyTurtleAmphibiousMalus stamps the AmphibiousNodeEvaluator.prepare
// overrides (WATER 0 / WALKABLE 6 / WATER_BORDER 4) and sets canFloat, so the turtle prefers water.
func TestTurtleAmphibiousMalus(t *testing.T) {
	m := &mobAI{}
	applyTurtleAmphibiousMalus(m)
	if got := m.malus.getPathfindingMalus(pathWater); got != 0.0 {
		t.Errorf("turtle WATER malus = %v, want 0.0 (free swim)", got)
	}
	if got := m.malus.getPathfindingMalus(pathWalkable); got != 6.0 {
		t.Errorf("turtle WALKABLE malus = %v, want 6.0 (dry land costly)", got)
	}
	if got := m.malus.getPathfindingMalus(pathWaterBorder); got != 4.0 {
		t.Errorf("turtle WATER_BORDER malus = %v, want 4.0", got)
	}
	if !m.navigation.canFloat {
		t.Error("turtle must have canFloat set (TurtlePathNavigation swims)")
	}

	// Behavioral: with the turtle's malus (WATER 0, WALKABLE 6) + canFloat, given a choice between a
	// short water crossing and a long dry detour, the A* prefers the water. Build a floor where the
	// direct line (z==0) crosses water and a dry detour bulges out to z==3.
	const floorY = 64
	r := newTestRegion(-2, floorY, -4, 12, floorY+4, 4, floorY)
	for x := 3; x <= 5; x++ {
		r.setFluid(x, floorY+1, 0, pathFluidWater)
	}
	req := pathRequest{
		startX: 0, startY: floorY + 1, startZ: 0,
		targetX: 8, targetY: floorY + 1, targetZ: 0,
		region: r, mobW: 1.2, mobH: 0.4, // a turtle
		followRange: 24, reachRange: 1, maxVisited: 8192,
		malus:    m.malus.copy(),
		canFloat: m.navigation.canFloat,
	}
	p := computePath(req)
	if p == nil {
		t.Fatal("turtle got no path")
	}
	// The turtle should swim through the water (WATER malus 0 is cheaper than a dry detour + WALKABLE 6).
	swam := false
	for _, nd := range p.nodes {
		if r.fluidAt(nd.x, nd.y, nd.z) == pathFluidWater {
			swam = true
			break
		}
	}
	if !swam {
		t.Error("turtle (WATER 0, WALKABLE 6) should prefer swimming through the water, not the dry detour")
	}
}
