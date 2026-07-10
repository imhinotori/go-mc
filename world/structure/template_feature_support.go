package structure

// template_feature_support.go exposes the StructureTemplate helper TemplateFeature needs that
// the runtime placer did not yet expose: getRotatedOffset (the axis-centering offset the
// TemplateFeature applies to origin before placeInWorld).
//
// Ported (idiomatic Go, no GPL paste) from CFR/javap -c
// net.minecraft.world.level.levelgen.feature.TemplateFeature.getRotatedOffset(Rotation, Axis,
// StructureTemplate):
//
//	return rotation.rotate(axis.getNegative()).getUnitVec3i().multiply(template.getSize().get(axis) / 2)
//
// axis.getNegative(): X -> WEST, Z -> NORTH (Direction.Axis.getNegative). rotation.rotate is
// Rotation.rotate(Direction) (rotateDirection, in-package). getUnitVec3i is the direction's
// unit normal. multiply(n) scales all three components by n. The size component is the
// UN-rotated template size along the axis, integer-divided by 2.

import "github.com/imhinotori/sulfur/level/block"

// Axis selects an X or Z axis for RotatedOffset (TemplateFeature only uses X and Z).
type Axis int

const (
	// AxisX is Direction.Axis.X.
	AxisX Axis = iota
	// AxisZ is Direction.Axis.Z.
	AxisZ
)

// axisNegative ports Direction.Axis.getNegative(): X -> WEST, Z -> NORTH.
func axisNegative(a Axis) block.Direction {
	if a == AxisX {
		return block.West
	}
	return block.North
}

// directionUnitVec ports Direction.getUnitVec3i() for the horizontal directions used here
// (WEST/NORTH and their rotations). Returns the direction normal (dx,dy,dz).
func directionUnitVec(d block.Direction) (int, int, int) {
	switch d {
	case block.Down:
		return 0, -1, 0
	case block.Up:
		return 0, 1, 0
	case block.North:
		return 0, 0, -1
	case block.South:
		return 0, 0, 1
	case block.West:
		return -1, 0, 0
	case block.East:
		return 1, 0, 0
	}
	return 0, 0, 0
}

// RotatedOffset ports TemplateFeature.getRotatedOffset for the given rotation and axis. The
// template's UN-rotated size along the axis is taken from t.Size ([x,y,z]); the returned
// (dx,dy,dz) is the direction normal scaled by size/2. It is added to the feature origin
// (once per axis) before PlaceInWorld, centering the template on the origin.
func (t *StructureTemplate) RotatedOffset(rot Rotation, a Axis) (int, int, int) {
	dir := rot.rotateDirection(axisNegative(a))
	nx, ny, nz := directionUnitVec(dir)
	var size int
	if a == AxisX {
		size = t.Size[0]
	} else {
		size = t.Size[2]
	}
	mul := size / 2
	return nx * mul, ny * mul, nz * mul
}
