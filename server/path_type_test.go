package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
)

// path_type_test.go -- C-2: the WalkNodeEvaluator path-type hazard classification tests. Asserts the
// block -> PathType map (classifyBlockPathType / getPathTypeStatic), the neighbour-hazard upgrade
// (checkNeighbourBlocks), the jar malus table, and that an A* path routes AROUND a lava pool.

func def(id string) block.StateID { return block.DefaultStateID[id] }

// TestPathTypeBlockMapping asserts classifyBlockPathType (ported getPathTypeFromState) maps each block
// to its vanilla PathType. Values VERIFIED against the 26.2 jar bytecode this session.
func TestPathTypeBlockMapping(t *testing.T) {
	cases := []struct {
		id   string
		want pathType
	}{
		{"minecraft:air", pathOpen},
		{"minecraft:stone", pathBlocked},
		{"minecraft:lava", pathLava},
		{"minecraft:water", pathWater},
		{"minecraft:fire", pathFire},
		{"minecraft:magma_block", pathFire},
		{"minecraft:lava_cauldron", pathFire},
		{"minecraft:campfire", pathFire},
		{"minecraft:cactus", pathDamaging},
		{"minecraft:sweet_berry_bush", pathDamaging},
		{"minecraft:honey_block", pathStickyHoney},
		{"minecraft:cocoa", pathCocoa},
		{"minecraft:wither_rose", pathDamageCautious},
		{"minecraft:powder_snow", pathPowderSnow},
		{"minecraft:lily_pad", pathTrapdoor},
		{"minecraft:oak_fence", pathFence},
		{"minecraft:cobblestone_wall", pathFence},
		{"minecraft:oak_door", pathDoorWoodClosed},
		{"minecraft:iron_door", pathDoorIronClosed},
		{"minecraft:rail", pathRail},
		{"minecraft:oak_leaves", pathLeaves},
	}
	for _, c := range cases {
		if got := classifyBlockPathType(def(c.id)); got != c.want {
			t.Errorf("classifyBlockPathType(%s) = %d, want %d", c.id, int(got), int(c.want))
		}
	}
}

// TestPathTypeStaticStanding asserts getPathTypeStatic over a snapshot: air over a solid floor is
// WALKABLE (via checkNeighbourBlocks), air over nothing is OPEN, and a hazard feet cell returns its
// own type.
func TestPathTypeStaticStanding(t *testing.T) {
	const floorY = 64
	r := newPathRegion(-2, floorY-1, -2, 6, floorY+4, 6)
	for x := -2; x <= 6; x++ {
		for z := -2; z <= 6; z++ {
			r.setBlockType(x, floorY, z, pathBlocked)
		}
	}
	if got := getPathTypeStatic(r, 0, floorY+1, 0); got != pathWalkable {
		t.Errorf("air over solid floor = %d, want WALKABLE(%d)", int(got), int(pathWalkable))
	}
	if got := getPathTypeStatic(r, 0, floorY+3, 0); got != pathOpen {
		t.Errorf("air over air = %d, want OPEN(%d)", int(got), int(pathOpen))
	}
	r.setBlockType(2, floorY+1, 2, pathDamaging)
	if got := getPathTypeStatic(r, 2, floorY+1, 2); got != pathDamaging {
		t.Errorf("cactus feet = %d, want DAMAGING(%d)", int(got), int(pathDamaging))
	}
}

// TestCheckNeighbourBlocks asserts the neighbour-hazard upgrade: a WALKABLE floor cell adjacent to a
// hazard is upgraded to the matching DANGER type. VERIFIED against WalkNodeEvaluator.checkNeighbourBlocks.
func TestCheckNeighbourBlocks(t *testing.T) {
	const floorY = 64
	build := func(hazard pathType) *pathRegion {
		r := newPathRegion(-2, floorY-1, -2, 4, floorY+4, 4)
		for x := -2; x <= 4; x++ {
			for z := -2; z <= 4; z++ {
				r.setBlockType(x, floorY, z, pathBlocked)
			}
		}
		r.setBlockType(2, floorY+1, 1, hazard)
		return r
	}
	if got := getPathTypeStatic(build(pathLava), 1, floorY+1, 1); got != pathFireInNeighbor {
		t.Errorf("walkable next to lava = %d, want FIRE_IN_NEIGHBOR(%d)", int(got), int(pathFireInNeighbor))
	}
	if got := getPathTypeStatic(build(pathFire), 1, floorY+1, 1); got != pathFireInNeighbor {
		t.Errorf("walkable next to fire = %d, want FIRE_IN_NEIGHBOR(%d)", int(got), int(pathFireInNeighbor))
	}
	if got := getPathTypeStatic(build(pathDamaging), 1, floorY+1, 1); got != pathDamagingInNeighbor {
		t.Errorf("walkable next to cactus = %d, want DAMAGING_IN_NEIGHBOR(%d)", int(got), int(pathDamagingInNeighbor))
	}
	if got := getPathTypeStatic(build(pathWater), 1, floorY+1, 1); got != pathWaterBorder {
		t.Errorf("walkable next to water = %d, want WATER_BORDER(%d)", int(got), int(pathWaterBorder))
	}
	r := newPathRegion(-2, floorY-1, -2, 4, floorY+4, 4)
	for x := -2; x <= 4; x++ {
		for z := -2; z <= 4; z++ {
			r.setBlockType(x, floorY, z, pathBlocked)
		}
	}
	if got := getPathTypeStatic(r, 1, floorY+1, 1); got != pathWalkable {
		t.Errorf("walkable with no hazard neighbour = %d, want WALKABLE(%d)", int(got), int(pathWalkable))
	}
}

// TestPathTypeMalusMatchesJar asserts the ported PathType default malus table matches the 26.2 jar
// PathType static ctor (VERIFIED bytecode): lava/fence/cactus/blocked impassable (-1), water and the
// DANGER neighbour types costly-but-passable (8), fire 16.
func TestPathTypeMalusMatchesJar(t *testing.T) {
	want := map[pathType]float32{
		pathBlocked:            -1,
		pathOpen:               0,
		pathWalkable:           0,
		pathPowderSnow:         -1,
		pathFence:              -1,
		pathLava:               -1,
		pathWater:              8,
		pathWaterBorder:        8,
		pathFireInNeighbor:     8,
		pathFire:               16,
		pathDamagingInNeighbor: 8,
		pathDamaging:           -1,
		pathDoorWoodClosed:     -1,
		pathDoorIronClosed:     -1,
		pathLeaves:             -1,
		pathStickyHoney:        8,
		pathBreach:             4,
		pathRail:               0,
		pathUnpassableRail:     -1,
	}
	for pt, m := range want {
		if got := pt.malus(); got != m {
			t.Errorf("pathType %d malus = %v, want %v", int(pt), got, m)
		}
	}
}

// TestDangerLavaPoolAvoidance: an A* path routes AROUND a lava pool (classified via blockType, not just
// the fluid array) rather than through it. The lava cells have malus -1 (impassable), so the path must
// detour to the single dry gap.
func TestDangerLavaPoolAvoidance(t *testing.T) {
	const floorY = 64
	r := newPathRegion(-2, floorY-1, -4, 12, floorY+4, 4)
	for x := -2; x <= 12; x++ {
		for z := -4; z <= 4; z++ {
			r.setBlockType(x, floorY, z, pathBlocked)
			r.set(x, floorY, z, true)
		}
	}
	for z := -3; z <= 2; z++ {
		r.setBlockType(4, floorY+1, z, pathLava)
	}
	req := pathRequest{
		startX: 0, startY: floorY + 1, startZ: 0,
		targetX: 8, targetY: floorY + 1, targetZ: 0,
		region: r, mobW: 0.9, mobH: 0.9,
		followRange: 24, reachRange: 1, maxVisited: 8192,
	}
	p := computePath(req)
	if p == nil {
		t.Fatal("expected a path around the lava pool, got nil")
	}
	for _, nd := range p.nodes {
		if r.blockTypeAt(nd.x, nd.y, nd.z) == pathLava {
			t.Fatalf("path stepped onto a lava block at (%d,%d,%d)", nd.x, nd.y, nd.z)
		}
	}
	last := p.nodes[len(p.nodes)-1]
	if abs(last.x-8) > 1 || abs(last.z-0) > 1 {
		t.Fatalf("lava-avoider path did not reach target (8,0); ended (%d,%d)", last.x, last.z)
	}
}
