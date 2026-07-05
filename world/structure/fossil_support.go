package structure

// fossil_support.go — the StructureTemplate helper FossilFeature needs that the STRUCT-05
// runtime placer did not yet expose: getZeroPositionWithTransform (the anchor shift so a
// rotated template's min-corner lands at the intended footprint) and the rotated size.
//
// Ported (idiomatic Go, no GPL paste) from CFR/javap -c
// net.minecraft.world.level.levelgen.structure.templatesystem.StructureTemplate:
//   - getZeroPositionWithTransform(BlockPos, Mirror, Rotation)         (the 3-arg overload)
//   - getZeroPositionWithTransform(BlockPos, Mirror, Rotation, int, int) (the 5-arg core)
//   - getSize(Rotation)                                                 (X<->Z swap for 90s)
//
// The 5-arg core (with sizeX,sizeZ pre-decremented by 1; i5/i6 the mirror offsets) maps the
// Rotation ordinals through StructureTemplate$1.$SwitchMap (verified via javap -c on the
// synthetic switch class): COUNTERCLOCKWISE_90->1, CLOCKWISE_90->2, CLOCKWISE_180->3,
// NONE->4. With Mirror.NONE (i5=i6=0):
//
//	NONE                : offset(0,         0, 0)
//	CLOCKWISE_90        : offset(sizeZ-1,   0, 0)
//	CLOCKWISE_180       : offset(sizeX-1,   0, sizeZ-1)
//	COUNTERCLOCKWISE_90 : offset(0,         0, sizeX-1)
//
// This shift, added to the anchor, is the `origin` PlaceInWorld translates the pivot-rotated
// (pivot 0,0) template blocks by — so the rotated footprint sits at the anchor's min corner,
// matching StructureTemplate.getZeroPositionWithTransform in the jar.

// RotatedSize ports StructureTemplate.getSize(Rotation): for CLOCKWISE_90/COUNTERCLOCKWISE_90
// the X and Z extents swap (the Y is unchanged); NONE/CLOCKWISE_180 keep the raw size.
func (t *StructureTemplate) RotatedSize(rot Rotation) (sx, sy, sz int) {
	switch rot {
	case RotClockwise90, RotCounterclockwise90:
		return t.Size[2], t.Size[1], t.Size[0]
	default:
		return t.Size[0], t.Size[1], t.Size[2]
	}
}

// ZeroPositionWithTransform ports the 3-arg StructureTemplate.getZeroPositionWithTransform
// (which calls the 5-arg core with the template's UN-rotated getSize().getX()/getZ()). It
// returns the anchor SHIFT (dx,dy,dz) to add to the base position so a `mir`/`rot`-transformed
// template lands with its min corner at the base — the value FossilFeature passes to
// placeInWorld as its origin. Only Mirror.NONE is exercised by FossilFeature; the general
// mirror offsets are transcribed for completeness.
func (t *StructureTemplate) ZeroPositionWithTransform(mir Mirror, rot Rotation) (dx, dy, dz int) {
	// The 5-arg core decrements both sizes by 1 (iinc 3,-1 / iinc 4,-1).
	sizeX := t.Size[0] - 1
	sizeZ := t.Size[2] - 1
	// i5 = (mirror == FRONT_BACK) ? sizeX : 0 ; i6 = (mirror == LEFT_RIGHT) ? sizeZ : 0.
	i5 := 0
	if mir == MirrorFrontBack {
		i5 = sizeX
	}
	i6 := 0
	if mir == MirrorLeftRight {
		i6 = sizeZ
	}
	switch rot {
	case RotNone:
		return i5, 0, i6
	case RotClockwise90:
		return sizeZ - i6, 0, i5
	case RotClockwise180:
		return sizeX - i5, 0, sizeZ - i6
	case RotCounterclockwise90:
		return i6, 0, sizeX - i5
	default:
		return i5, 0, i6
	}
}
